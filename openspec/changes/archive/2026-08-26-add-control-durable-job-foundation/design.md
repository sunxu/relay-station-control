## Context

参见 [proposal.md](./proposal.md) 的动机。Control 当前已有单环境数据库、不可变审计、资产注册、OpenAPI 生成链和 React 管理壳，但没有 `internal/jobs` 模块或持久任务表。系统设计 v1.0 第 20.3、21.1、24.2 节和阶段 1 明确要求 PostgreSQL 是任务与 Outbox 的唯一真相源，Redis 只能可选唤醒，Worker 使用租约，崩溃后由 Reconciler 在核对实际状态后恢复。

首期每个环境固定一个 Control 实例。因此本设计处理进程内多个 Worker 的并发、进程重启、数据库短时故障和人工重放，不宣称多 Control 实例高可用或分布式调度能力。所有持久时间判断使用 PostgreSQL UTC 时间；Go Clock 只控制循环何时醒来，不能裁决任务是否到期或租约是否有效。

## Goals / Non-Goals

**Goals:**

- 用数据库约束和显式状态转换建立可恢复、可审计且有界的通用任务基础。
- 让未来业务能力能够在同一事务内写入期望状态、成功审计、任务和唤醒 Outbox。
- 使用 lease owner 与随机 fencing token 阻止过期执行者提交结果。
- 对未知执行结果先验证再恢复，禁止租约过期后盲目重复外部副作用。
- 即使没有 Redis、通知丢失或 Control 重启，也能仅依赖 PostgreSQL 继续推进任务。
- 为管理员提供脱敏只读状态，并提供低基数指标和可执行 Runbook。

**Non-Goals:**

- 不实现账号采集 poll run、账号历史 compaction run 或 daily rollup；这些有独立状态机，后续 change 可复用本模块的循环、租约与观测模式，但不能把专用生命周期强塞进 `async_jobs`。
- 不实现 Gateway 配置写入、Node 部署、扩缩容、GitHub、Docker、SSH、CLIProxyAPI 或其他真实外部操作执行器。
- 不引入 Redis 客户端、Redis Compose 服务、选主、分布式锁、多实例 Control 或通用工作流 DSL。
- 不提供浏览器/API 创建、重试、取消、删除任务的入口；任务只能由后续已评审业务 service 在事务中创建。
- 不保存 Secret、凭证、原始 HTTP body、完整外部错误、任意可执行命令或未注册 payload。

## Decisions

### 1. PostgreSQL 表和不可变事件共同构成任务真相源

新增一个 forward Migration，创建四张表：

- `async_job_kinds`：由代码配套 Migration 登记固定 `job_kind`、payload schema version、默认超时、租约时长、最大尝试次数、是否允许回滚和生命周期状态。运行时不能新增、修改或删除类型；本 change 的生产 Migration 不登记任何真实业务类型。
- `async_jobs`：保存 UUID `job_id`、全局唯一 `idempotency_key`、`job_kind`、稳定 UUID `operation_id`、有界 JSONB `payload`、SHA-256 `payload_hash`、状态、优先级、`attempt_count`/`max_attempts`、`available_at`、`started_at`、`completed_at`、取消请求时间、固定错误码/脱敏摘要、lease owner、lease fencing token、`lease_expires_at` 和 UTC 创建/更新时间。
- `async_job_events`：保存每个任务单调递增 sequence、固定事件类型、from/to 状态、attempt、固定 reason/error code、actor 类型和 UTC 时间。触发器拒绝 `UPDATE`/`DELETE`；它用于恢复和排障证据，不替代面向实名管理员的 `audit_logs`。
- `operation_outbox`：保存 UUID 事件 ID、唯一 event key、任务/operation 引用、固定 topic、最小唤醒 envelope、状态、尝试次数、`available_at`、发布 lease/fencing、发送时间和固定错误码。Outbox 不保存业务 payload、Secret 或外部响应。

数据库检查约束封闭状态、actor、topic、长度、attempt、时间组合和终态字段。`payload` 编码上限为 64 KiB，必须是 JSON object；入队 service 先按任务类型注册的 schema 严格校验、拒绝未知字段和 Secret-like 字段，再以 RFC 8785 等价的确定性 JSON 编码计算 SHA-256。数据库保存原 payload 是为了重启后执行，但 API、UI、日志、Trace 和指标永不投影它；hash 仅用于完整性核对，不作为 Secret 安全措施。

替代方案是只保存 payload 引用或让每个业务表自行充当队列。前者把恢复正确性转移到另一个未定义存储，后者会复制认领、租约和观测逻辑，因此采用统一任务表；大型数据集仍应保存于业务表，任务 payload 只携带稳定 ID、期望 revision 和有界参数。

### 2. 入队必须加入调用方事务，不隐式开启独立事务

`jobs.EnqueueTx` 接受已经开启的 `pgx.Tx`、已注册类型、调用方构造的幂等键、稳定 operation ID 和严格 payload。它依次验证类型/参数，插入或读取 `async_jobs`，插入初始 job event，并插入 Outbox；未来业务 service 必须在同一事务中先写期望状态和成功审计，再调用该原语，最后由调用方提交。

相同 idempotency key 只有在 `job_kind`、operation ID、payload hash 和执行策略全部一致时返回既有任务；任一字段冲突都 fail closed，不覆盖旧任务、不新增 Outbox。Outbox event key 从 job ID 和固定事件类型确定，相同事务重放不会重复。事务提交前不发送进程内或外部通知；提交成功后仅发送 best-effort 本地 wake signal，Dispatcher 仍从数据库查询 Outbox。

本 change 使用测试事务中的合成任务类型证明“业务行 + audit + job + event + Outbox”全部提交或全部回滚，不在生产库留下测试类型或公开通用入队 HTTP API。

替代方案是 `Enqueue` 内部自行开启事务。那会让调用方业务写入和任务分裂提交，因此不采用。

### 3. Worker 使用短认领事务、数据库时钟和 fencing token

Worker 的可配置并发具有保守默认值和上限；循环始终定期扫描 PostgreSQL，本地 wake 只触发提前扫描。每个 worker 先开启短事务，用 `FOR UPDATE SKIP LOCKED` 按固定 priority、`available_at`、创建时间和 job ID 顺序选择一条 `pending|retry_wait` 任务，并使用数据库 `clock_timestamp()` 判断可执行性。认领时原子完成：

1. 确认类型仍已注册、attempt 未达上限且未请求取消；
2. 状态改为 `running`，attempt 加一；
3. 写入本次 boot/worker 的有界 lease owner、新随机 UUID fencing token 和数据库计算的 `lease_expires_at`；
4. 写入对应不可变 job event并提交。

执行发生在认领事务之外。续租和任何状态提交都必须在 `WHERE job_id=? AND status=? AND lease_fencing_token=? AND lease_expires_at > clock_timestamp()` 条件下原子更新；零行更新表示执行权已丢失，旧执行者必须停止提交结果。执行器 context 的 deadline 不晚于任务 timeout 和租约续租安全边界，续租失败会取消本地 context，但不能假设外部副作用未发生。

单 Control 仍使用 fencing，因为同一进程的超时 goroutine、重启后的旧连接和人工操作可能并发。该机制不改变“只部署一个 Control”决策，也不允许直接扩成多实例。

### 4. 状态机、重试和取消保持封闭且有界

任务状态固定为：

```text
pending -> running -> verifying -> succeeded
   |          |           |
   |          +----------> retry_wait -> running
   |          |           +---------> rolling_back -> rolled_back
   |          +---------------------> failed
   +--------------------------------> cancelled
```

`succeeded|failed|rolled_back|cancelled` 是终态，数据库拒绝离开终态。仅在尚未产生未知外部结果时，`pending|retry_wait` 可以直接取消；`running|verifying|rolling_back` 的取消只写 `cancel_requested_at`，由持有有效 lease 的执行器或 Reconciler 在验证安全后进入 `cancelled|rolling_back|failed`。数据库 transition 函数还会联动校验 actor、from/to、event、`replay_safe` 与 `rollback_allowed`，因此运行时角色不能通过直接调用已授权函数跳过 verifying 或伪造成功。本 change 实现内部 `RequestCancelTx` 原语及真实 PostgreSQL 测试，但不暴露产品写入口。

执行器只能返回固定分类：成功且需验证、确定可重试且未产生副作用、确定永久失败、或结果未知。退避由数据库 `available_at` 固化，使用有上限的指数退避和有界 jitter；进程重启不能重置 attempt 或退避。达到 max attempts、超过任务 deadline、未知类型/版本或违反 payload hash 时进入固定 `failed`/人工处置分类，不无限重试。

错误持久化只接受注册表中的 `error_code` 和通过清洗的最多 512 字符摘要；禁止直接保存 `error.Error()`、HTTP body、SQL 参数、命令行或 Secret。状态转换和 job event 在同一事务提交。

### 5. 过期 lease 必须先由 Reconciler 验证实际状态

Reconciler 扫描 `running|verifying|rolling_back` 且 lease 已过期的任务，通过 `FOR UPDATE SKIP LOCKED` 取得恢复 lease 和新 fencing token，然后调用该任务类型注册的 `Verify` 方法。`Verify` 只能返回封闭结果：

- `effect_applied`：通过 transaction-bound mutation hook，在同一 fenced PostgreSQL 事务中写业务确认、任务 `succeeded` 和事件；hook 不能自行创建、提交或回滚事务，任一步失败都会整体回滚；
- `effect_not_applied`：仅在执行器明确声明重放安全且 attempt 未耗尽时进入 `retry_wait`；
- `effect_partial_or_rollback_required`：类型允许回滚时进入 `rolling_back`，否则进入人工处置 `failed`；
- `effect_unknown`：保留可见故障并按有限验证重试，耗尽后进入人工处置 `failed`，绝不直接再次 Execute。

未来真实执行器必须在对应业务 change 中定义可验证的 stable operation ID、目标系统查询方式、幂等/回滚能力和安全测试，才能登记 job kind。没有注册执行器、版本不匹配或验证接口不可用都 fail closed。每次调用 Execute、Verify 或 Rollback 前先向 PostgreSQL 做一次即时 fenced 续租；如果数据库时间判定 lease 已失效，则不调用执行器。进程收到停止信号后先停止认领，给当前短数据库事务收尾，取消执行 context；未确认工作保留 running lease，重启后走相同 Reconciler，而不是停机钩子猜测结果。

本 change 只用不发起网络请求的测试执行器和故障注入数据库验证各分支；生产执行器集合为空。

### 6. Outbox 是可选唤醒证据，不是任务顺序或完成依据

任务入队同事务产生一个最小 wake Outbox。部署未配置 Publisher 时，Outbox 以 `suppressed` 终态写入并记录固定 `publisher_disabled` reason；Worker 仍轮询任务。将来配置 Publisher 时，新 Outbox 从 `pending` 开始，Dispatcher 使用与 Worker 相同的短认领事务、lease/fencing 和有限退避，在事务外调用 Publisher，然后以 fenced 更新标记 `sent`。

Publisher 语义是 at-least-once：发布成功后、写 `sent` 前崩溃会重复通知。通知 envelope 只包含 schema version、event ID、job ID、operation ID 和固定 topic；消费者收到通知后只提前扫描 PostgreSQL，不按消息 payload 执行任务。达到发布尝试上限进入 `failed` 并告警，但对应 job 仍由数据库轮询推进。Outbox 不参与业务任务成功判定。

本 change 定义 Publisher 接口、disabled 实现和可控 fake，用它验证通知丢失、重复、延迟和故障；不加入 Redis 依赖。真实 Redis Publisher 必须由单独 change 引入并补充 TLS、认证和网络故障测试。

### 7. 只读 API/UI 不暴露执行材料

OpenAPI 新增 `GET /api/jobs` 和 `GET /api/jobs/{job_id}`，复用现有有效实名 `super_admin` 会话、Problem 响应和 `Cache-Control: no-store`。列表默认 50、最大 200，按 `(created_at, job_id)` 稳定倒序 cursor 分页，可用固定 job kind、status 和 created time 范围过滤；无效/篡改/跨过滤 cursor 返回 `400`。详情不存在返回 `404`，数据库不可用返回脱敏可重试 `503`。

响应只包含 job ID、operation ID、固定 kind/status、attempt/max attempts、公开 UTC 时间、取消是否请求、固定 error code、Outbox 聚合状态和脱敏 lifecycle events。它不得返回 payload、payload hash、idempotency key、lease owner/token、Outbox envelope、错误摘要或 SQL/外部响应。job kind 来自受控类型目录，可以作为页面过滤值，但禁止作为 Prometheus label。

React `/jobs` 路由懒加载，只调用生成客户端；显示空状态、状态过滤、cursor 翻页、详情时间线、数据库故障和显式重试。页面不轮询外部 endpoint，不提供创建、重试、取消、删除按钮，也不把旧响应冒充当前状态。

### 8. 指标、日志和审计保持固定低基数与脱敏

实现系统设计规定的：

```text
relay_control_async_jobs{status}
relay_control_async_job_oldest_pending_seconds
relay_control_async_job_expired_leases
relay_control_outbox_oldest_pending_seconds
```

`status` 仅允许封闭任务状态；其他指标无标签。job kind、job ID、operation ID、idempotency key、payload、错误内容、lease owner/token 和 Outbox event ID 不得成为标签。明细只能通过受认证页面和固定字段结构化日志定位。日志使用固定 component、action、result、job_kind 和 error_code；job kind 是受控低基数日志字段，但 payload 和所有 Secret-like 值被拒绝。

`async_job_events` 记录机器状态转换。未来由实名管理员发起的业务操作仍必须把既有 `audit_logs` 成功事件与任务同事务提交；自动 Worker 不为每次扫描制造管理员审计。人工取消/重试等产品入口不在本 change，未来引入时必须复用重新认证、原因、CSRF 和不可变审计边界。

### 9. 启停、数据库故障和回滚不产生数据面副作用

Worker/Dispatcher/Reconciler 在环境身份校验和 Migration 版本检查通过后启动，在 HTTP server 停止认领前进入 drain；各循环有独立有界连接使用和退避，数据库不可用时不 busy-loop、不把任务标记成功，也不阻止健康诊断进程退出。数据库恢复后，循环重新扫描并依据持久状态恢复，无需重建内存队列。

本 change 的生产 job kind/Executor 为空，故不会发起任何 Gateway、Node 或互联网请求。即使未来存在任务，Control/数据库停止也只能暂停控制面，不能改变 Gateway/Node 已有请求路径。综合验收使用网络拒绝代理/调用计数器证明任务页面、空 Worker、恢复测试和应用停机均无外部副作用。

应用回滚保留新增表、任务和事件，旧版本忽略它们。Migration down 只允许四张表均为空；任何 job、event、Outbox 或已登记 job kind 存在时 fail closed。生产普通回滚不得执行 down，不能删除失败任务来消除告警。

## Risks / Trade-offs

- [通用任务表可能演变成无约束工作流平台] → job kind 必须由 Migration 与代码双重注册，payload schema 严格且有界；每个真实业务执行器仍需独立 OpenSpec 评审。
- [lease 过短导致执行权频繁丢失，过长导致恢复慢] → 类型登记固定已验证 timeout/lease/续租间隔，要求 lease 覆盖单次步骤并留安全余量；暴露过期 lease 指标并用故障测试校准。
- [发布成功但回写前崩溃会重复 Outbox 通知] → 明确 at-least-once，通知只触发 PostgreSQL 扫描，不承载执行指令。
- [错误摘要或 payload 泄露 Secret] → 严格 schema、Secret-like 字段拒绝、大小限制、错误码 allowlist、响应投影剔除和 canary 测试共同防护。
- [Reconciler 无法证明外部实际状态] → 未知结果有限验证后进入人工处置，绝不自动重放；具体业务 change 必须提供可验证 operation ID 才能注册。
- [任务表持续增长] → 本 change 保留完整证据，不实现清理；先监控规模，保留/压缩策略另开 change，禁止未经规格直接删除。
- [单实例仍存在控制面停机窗口] → 接受系统既定单实例边界，依赖 Compose 自动重启和 PostgreSQL 恢复；不把任务基础误当多实例 HA。

## Migration Plan

1. 在一次性 PostgreSQL 18 实例验证当前 Migration 基线、运行角色和 Migration owner 权限；确认数据库 session timezone 为 UTC。
2. 运行新的原子 forward Migration，创建任务类型、任务、事件和 Outbox 表、约束、索引、不可变触发器及最小权限；生产 Migration 不登记业务 job kind。
3. 发布包含 `internal/jobs`、空生产 Executor registry、PostgreSQL Worker/Reconciler、disabled Publisher 和只读 API 的 Control；默认并发/轮询配置必须可直接启动且不依赖 Redis。
4. 先用空队列验证健康、指标和任务页面，再在隔离验收数据库用合成类型执行原子入队、并发认领、故障注入、租约过期、Reconciler、Outbox 和优雅停机测试。
5. 发布只读任务 UI 和 Runbook，观察连接池、锁等待、扫描延迟、过期 lease 和 Outbox 指标；确认 Gateway/Node 请求路径完全不受影响。

应用回滚只回退二进制和前端，保留任务表与全部证据；重新升级后按数据库状态继续恢复。只有全新数据库且 `async_job_kinds`、`async_jobs`、`async_job_events`、`operation_outbox` 全空时，人工确认后才允许 Migration down。若发布后循环异常，先停用 Worker/Dispatcher/Reconciler 并保留只读诊断，禁止 truncate、改终态或延长 lease 来掩盖问题。
