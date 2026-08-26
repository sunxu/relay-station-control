## Context

`asset-registry` 已持久化 Relay Node、声明 capability、`relay_node_inventory_monitoring_activations`、不可变 `provider_inventory_policy_versions` 及其激活区间；`cliproxyapi-readonly-driver` 已提供无代理、无重定向、Secret 隔离且无隐藏重试的 `ListAccountInventory`。当前没有周期调用者，也没有账号采集表。

系统设计 v1.0 要求账号采集按数据库 UTC 五分钟固定槽运行，每个 Node/槽至多一个 poll run，并明确区分：Node 请求失败但证据已落库、Control 执行中断、超过启动宽限无法补采。通用 `async_jobs` 面向可能有外部副作用的业务任务，租约过期后必须 Verify；账号清单 GET 是专用、高频、只读且带 `scheduled_at` 语义的状态机，不能强塞进通用任务表或由通用 Worker 自动重放。

本 change 只把 Driver 观察转换为聚合 poll 证据。账号明细、HMAC `account_key`、完整快照、当前 Provider/账号状态、missing/out-of-scope、压缩、覆盖率和告警仍属于后续 change。

## Goals / Non-Goals

**Goals:**

- 以 PostgreSQL UTC 时钟、唯一键和状态约束建立幂等五分钟时间槽。
- 只为显式受监控且能力/策略匹配的 Node 创建 poll run，并固定创建时策略版本。
- 在发起 Node 请求前取得有限并发额度，并以 `poll_start_grace` 阻止过期槽调用当前接口。
- 用 lease/fencing、有界 attempt 和 Reconciler 恢复 Control 崩溃、数据库短时故障和过期执行。
- 将 Node 失败与 Control 执行失败分开；任何 finalized 证据都在一个事务中完整落库。
- 保存足以审查 transport、contract、mode、Node/Provider 完整性与降级的聚合证据，且不保存原始或逐账号数据。
- 提供低泄露观测、容量校验、Runbook 和真实 Driver 有速率保护的验收。

**Non-Goals:**

- 不创建账号 snapshot items、当前账号/Provider 状态、HMAC 账号标识、missing/out-of-scope 生命周期或跨 Node 重复检测。
- 不实现 `promotion_applied`、stale 当前快照、日级覆盖率、历史压缩、清理、告警或趋势页面；没有真实提升时不伪造 promotion 指标。
- 不新增管理员写操作、产品只读 API/UI、人工重试/补采入口或 poll-run 删除入口。
- 不调用 Probe、Gateway、Sub2API、Prometheus 上游或任何写接口，不修改 Node/Gateway 协议。
- 不引入 Redis、选主、分布式锁、多 Control 实例协调或复用 `async_jobs`。

## Decisions

### 1. poll run 使用专用 additive 表与封闭状态机

新增一个 forward Migration：

- `account_inventory_poll_runs` 保存 UUID `poll_run_id`、`instance_id`、固定槽 `scheduled_at`、`status`、`attempt_count/max_attempts`、`created_at`、首次/最近 `started_at`、`lease_expires_at`、随机 `lease_fencing_token`、`finalized_at`、`abandoned_at`、固定执行错误码、不可变 `provider_policy_version`、聚合源/可识别/无法识别/unsupported/out-of-scope 计数、可空 `inventory_mode`、transport/response-shape/contract/Node identity/snapshot-complete/degraded 布尔值、固定 reason 集、有界 Node version/commit 和数据库生成的 `observed_at`。
- `account_inventory_poll_provider_results` 为 finalized run 固定策略中的每个 active Provider 保存一行，包含可识别数、缺失 identity 数、节点内重复 identity 数、identity/snapshot complete、degraded 和固定 reason 集；唯一键 `(poll_run_id, provider)`。

数据库检查约束封闭 `pending|running|retry_wait|finalized|abandoned`、字段组合、长度、计数和时间关系。`scheduled_at` 为 `timestamptz`，Unix epoch 秒必须被 300 整除；唯一键 `(instance_id, scheduled_at)`。`finalized` 必须具有完整 Node 结果和恰好覆盖固定 active Provider 集的 provider results；`abandoned` 不得伪造观察结果或 provider rows。provider results 对 poll run 使用 `ON DELETE CASCADE`，但本 change 不提供运行时删除权限或清理任务。

状态转换固定为：

```text
pending -> running -> finalized
   |          |
   |          +-> retry_wait -> running
   +-------------------------> abandoned
retry_wait ------------------> abandoned
running ---------------------> abandoned
```

`finalized|abandoned` 是终态。Node 返回失败仍进入 finalized；只有未能完整提交 Control 证据的执行才可能进入 retry_wait。所有转换通过最小权限 SQL/function 完成，状态更新与必要字段必须同事务提交。

不新增 poll event 表：状态、attempt、时间、固定错误码和应用脱敏日志足以满足本阶段恢复证据；避免为每五分钟每 Node 产生第二套高频历史。后续若人工操作需要不可变事件，必须由独立 change 评审。

### 2. 调度只处理当前 UTC 槽，不回填过去响应

Scheduler 周期醒来后从 PostgreSQL `clock_timestamp()` 计算 `date_bin('5 minutes', db_now, epoch)` 等价固定槽。在一个短事务中选择同时满足以下条件的 Node：

1. 资产存在且声明 `management_account_inventory_read`；
2. `scheduled_at` 落在 Node 监控 `[effective_from,effective_to)` 区间；
3. 该 Node type/Driver contract 在该槽存在唯一 active Provider 策略激活，并与当前 binding/不可变版本一致；
4. `scheduled_at + poll_start_grace > db_now`。

以 `INSERT ... ON CONFLICT (instance_id, scheduled_at) DO NOTHING` 创建 pending run，并把 policy version 固定为首次插入版本。重复调度、进程重启和同进程多个 scheduler tick 只复用原行，不更新策略版本。策略随后切换也不能重新解释该 run。

Scheduler 不枚举当前槽以前的空槽。已创建但过期的 pending/retry_wait 由 Reconciler 标记 abandoned；从未创建的过期槽保持无行，并由后续覆盖率 change 依据激活区间计算缺口。Gateway enabled/draining/disabled、Compose 状态或资产页面读取不得隐式开启/关闭监控区间。

如果激活区间/策略数据重叠、binding 不一致、Node capability 与 Driver 不匹配，调度 fail closed、输出固定错误分类且不调用 Node；不得猜测一个策略或自动修复资产真相。

### 3. 先取得 HTTP 并发额度，再认领并立即 dispatch

Poll service 使用进程级有界 semaphore。Worker 必须先取得一个 HTTP 槽，再开启短认领事务；没有并发额度的 pending run 保持数据库 pending，不能提前成为 running 并让 lease 在队列中耗尽。

认领查询使用 `FOR UPDATE SKIP LOCKED` 和稳定顺序，只选择仍在宽限期内的 pending/retry_wait 或已由 Reconciler 释放的记录。它原子写入 running、增加 attempt、保留首次 started time、写最近 started time、随机 fencing token、数据库计算的 30 秒 lease，并返回数据库计算的 `grace_remaining`。事务提交后不再进入其他队列，Worker 立即构造以 `grace_remaining` 和 15 秒请求总超时中较小者为 deadline 的 context，并调用固定 Driver。

相对 deadline 从认领时数据库返回的剩余时长递减，不依赖 Go wall clock 判断 UTC。现有 Driver 在 Secret/DNS/拨号和 HTTP 各阶段检查 context；若预处理耗尽宽限，必须在真正发出 HTTP 前停止并进入 Control 执行恢复路径。poll service 不设置代理、不添加 Driver 重试，也不能表达任意 method/path/header/body。

初始配置：period=300s、`poll_start_grace=120s`、worst-case request=15s、lease=30s、max attempts=2、poll concurrency 至少 10 且有安全上限。启动时按配置的最大受监控 Node 数计算：

```text
last_batch_start = (ceil(max_node_count / poll_concurrency) - 1) * worst_case_poll_duration
```

`last_batch_start` 必须严格小于 grace 并保留显式数据库/调度余量；lease 必须覆盖 request timeout、解析和 finalize 余量。默认最多 50 个 Node 时 concurrency 小于 10 或任何危险组合都在启动 poll service/网络调用前拒绝。该校验不改变整个 Control 的数据面隔离，可使 poll readiness/指标明确失败。

### 4. Node 失败 finalized；未知 Control 执行才有限恢复

Driver 被调用并返回可分类观察后，无论 HTTP 成功、非 200、timeout、契约无效、disk fallback 或 identity 不完整，Worker 都尝试 finalize。非 200/调用失败保存 `transport_success=false`、`contract_valid=false`；HTTP 200 但 JSON/shape/mode 无效保存 transport 成功而 contract 失败。它们是该槽真实观察证据，不进入 retry_wait，也不由 poll service再次请求以掩盖失败。

只有以下 Control 执行中断可恢复：进程在 Driver 返回前/后崩溃、数据库无法提交完整 finalize、或 lease 过期导致 fencing 提交失败。Reconciler 对 lease 已过期的 running 使用数据库时间判断：

- 仍在 `scheduled_at + poll_start_grace` 内且 attempt 未耗尽：以 fenced 短事务转为 retry_wait，清除旧 lease；下一 Worker 复用同一 poll run。由于接口是固定只读 GET，该次恢复允许再次调用，但 attempt 可见且最多两次。
- 已过 grace 或 attempt 耗尽：转为 abandoned，保存固定 Control 执行分类但不保存 Node 观察、不生成 provider rows、不再调用接口。
- pending/retry_wait 到期：直接 abandoned。

旧 Worker 的 finalize 必须同时匹配 `status=running`、未过期 lease 和 fencing token；零行更新表示执行权丢失，旧 Worker 丢弃内存结果且不能覆盖恢复者。finalized 和 abandoned 永不重开。人工 SQL/API 不允许补写 `observed_at` 或把当前响应挂到历史槽。

本状态机不走通用 job Reconciler，因为该 Reconciler 的 Verify/副作用语义与 poll slot 不同；可以复用循环/退避编码模式，但不能共享表或状态转换。

### 5. finalize 事务保存完整、固定策略的聚合证据

Worker 使用 poll run 固定的 Provider policy 构造 Driver request。finalize 开启一个事务并锁定 poll run，检查有效 lease/fencing 后：

1. 只从 Driver 白名单 DTO 复制聚合计数、固定 mode/result/reason、有界版本/提交；`observed_at` 使用数据库 `clock_timestamp()`，不使用 Node 时间或 `scheduled_at` 冒充。
2. 为固定策略的每个 active Provider 写入一行结果。即使 transport/contract 失败导致 Driver 没有 provider rows，service 也依据固定策略补齐 `snapshot_complete=false` 和相应固定降级原因。
3. 验证 provider 集与 pinned policy 完全相等、所有计数有界且 Node 汇总与 Provider 结果一致。
4. 同事务写完 provider rows、poll result、清除 lease 并置 finalized；任一步失败则全部回滚，记录保持 running 等待 Reconciler。

策略在 run 创建后切换不改变解析集合或结果行。由于本 change 不提升 snapshot/current state，finalize 不锁当前 policy binding、不写 `promotion_applied`，也不把“未实现提升”错误标为 `provider_incomplete` 或 `policy_changed`。后续快照 change 必须把策略指针锁定、promotion 判定、snapshot items 和当前指针加入同一个 finalize 事务，届时才能新增 promotion 字段/指标。

不保存 Driver `Accounts`、email、account key、duplicate account identity、未知记录、原始 body/header、endpoint、Secret reference、Management Key 或原始 error。节点内重复仅保存按 Provider 的聚合计数；逐账号重复证据留待 HMAC account key change。

### 6. 配置、进程生命周期与数据库故障

Poll scheduler/worker/reconciler 只在环境身份、Migration 版本、Driver registry 和容量配置验证通过后启动。停止顺序先停止 scheduler 创建/认领，再取消尚未 dispatch 的 context，给短 finalize 事务有限收尾时间；未确认的 running 保留 lease，由重启 Reconciler 处理。停机钩子不得猜测请求结果或把 running 强制 finalized。

PostgreSQL 不可用时各循环有界退避、不 busy-loop、不调用 Node、不重置内存计数冒充恢复。数据库恢复后先 Reconcile 过期行，再处理仍有效的当前槽；过去槽超过 grace 只 abandoned。Node/Secret/DNS/HTTP 故障作为 Driver 观察 finalized，下一固定槽自然再次采集，不在同槽隐藏重试。

单 Control 进程可有多个 Worker，但首期仍只部署一个 Control；本 change 不宣称多实例 HA。数据库唯一键、SKIP LOCKED 和 fencing 仍防止同进程竞态、重启残留和误并发。

### 7. 指标、日志、审计与敏感信息边界

实现并从数据库持久状态恢复以下指标：

```text
relay_control_account_inventory_poll_run_state{instance_id,state}
relay_control_account_inventory_scheduler_lag_seconds{instance_id}
relay_control_account_inventory_queue_wait_seconds{instance_id}
relay_control_account_inventory_poll_start_lag_seconds{instance_id}
relay_control_account_inventory_transport_success{instance_id}
relay_control_account_inventory_contract_valid{instance_id}
relay_control_account_inventory_provider_snapshot_complete{instance_id,provider}
```

`state`、provider 和 reason 使用数据库/策略受控枚举；instance 数量受资产上限约束。禁止 poll-run ID、policy version、email、account identity、endpoint、hostname/IP、Secret reference/key、Node version/commit 或 error 文本作为标签。scheduler lag 按最近应执行固定槽减最近 finalized 槽计算，失败观察 finalized 也表示该槽已完成；queue wait 从 created 到最近 claim，poll start lag 从 scheduled 到首次实际 claim，重启不得清零。

结构化日志只允许固定 component/action/result/reason、node type、状态和 attempt bucket；稳定 instance ID 仅在受控排障字段出现，不输出 endpoint/provider account/Secret/error 原文。日志、测试输出和 acceptance artifact 使用唯一 canary 扫描。自动采集不是管理员操作，不新增 `audit_logs`；未来人工开启/暂停监控继续由 asset-registry 审计边界负责。

### 8. OpenAPI、生成物、测试与真实请求速率

本 change 不修改 OpenAPI/React。新增 Migration 与 sqlc query 后必须运行 `make generate`，提交可复现生成物并 clean check；不得手改 sqlc 生成文件。Go 单元测试使用 fake DB clock/Driver，PostgreSQL 集成测试验证约束、事务、并发认领、fencing、重启和数据库中断。容器验收证明 Control/DB 故障不影响模拟数据面，且无 Gateway/Node 写调用。

容量/并发验收对本地受控 fake/官方原版镜像执行，不接触真实账号。若执行阶段 0 的两个真实测试 Node 验收，全部管理请求必须全局串行，每次请求完成或失败后仍等待至少 10 秒（包括最后一次），只保存脱敏状态和聚合计数；真实 Node 验收不得用于测 10 并发容量。Go、npm、Docker、make 运行均显式清除本地代理；Driver 自身继续无条件禁用环境代理。

## Risks / Trade-offs

- [只保存聚合证据，暂时没有账号页面] → 本 change 可独立验证调度正确性且不提前固化账号模型；下一 change 才在相同 finalize 事务加入快照提升和 UI。
- [崩溃恢复可能在同一槽重复一次只读 GET] → max attempts=2、fencing 和 grace 严格限制请求；相比把未知请求结果伪造成 finalized，重复有界只读观察更可审计。
- [单实例 semaphore 不是分布式并发限制] → 首期部署约束为单 Control；数据库锁/fencing 保证正确性，未来多实例需独立 change 引入全局配额。
- [受控 instance/provider 指标增加序列] → 资产规模上限和 Provider 策略封闭集合限制基数；禁止逐账号指标，后续账号指标必须使用环境 HMAC。
- [app 暂停可能消耗 dispatch 窗口] → DB 返回相对剩余 grace 并通过 context 贯穿 Driver；过期只 abandoned，不补造历史。
- [应用回滚无法识别新表] → 表和证据保留且 scheduler 停止；forward Migration 不回退，重新部署新版可恢复。

## Migration Plan

1. 合并 additive Migration、sqlc、repository、状态机、指标和 Runbook，保持 poll service 默认显式启用且启动前校验环境/容量。
2. 在 dev 使用 fake Driver/PostgreSQL 验证重复 tick、并发认领、所有崩溃点、策略切换、宽限过期和数据库恢复；确认无逐账号/Secret 数据落库。
3. 使用官方 CLIProxyAPI 原版镜像与脱敏 fixtures 完成容器契约验收；真实测试 Node 如启用，按全局串行和请求后 10 秒冷却执行。
4. 在 staging 以最多 50 个 fake Node 验证 concurrency>=10、15 秒 worst case 下最后一批在 120 秒 grace 及余量内开始，并观察一个完整时间槽。
5. 生产先部署 Migration/应用并监控 poll state、lag 和 abandoned；发现异常时停止 poll service，不修改 Node/Gateway，保留证据排障。

应用回滚只停止新调度并保留数据库对象。只有所有新表为空、从未形成 poll run 的全新环境，才允许 DBA 按 Runbook 人工执行 down；普通回滚、存在 finalized/abandoned 证据或后续表引用时禁止 down。

## Open Questions

无。本 change 使用系统设计 v1.0 已批准的 300 秒周期、120 秒 grace、15 秒请求上限、30 秒 lease、最多 50 个 Node 与初始并发至少 10；实现中若容量测试需要改变这些政策，必须先更新 OpenSpec 和系统设计，而不是静默调整。
