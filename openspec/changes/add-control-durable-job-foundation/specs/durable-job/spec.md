## Purpose

为单实例 Relay Station Control 提供以 PostgreSQL 为唯一真相源、可从崩溃恢复且不依赖 Redis 正确性的持久任务基础，使后续控制面操作能够原子入队、受租约执行、先验证再恢复并由管理员安全查看状态。

## ADDED Requirements

### Requirement: PostgreSQL 保存完整持久任务真相
Control SHALL 在 PostgreSQL 保存已注册任务类型、任务、不可变生命周期事件和事务 Outbox。每个任务 MUST 具有唯一幂等键、稳定 operation ID、固定任务类型与 payload schema version、有界非 Secret payload 及其确定性 hash、封闭状态、尝试预算和数据库 UTC 时间；进程内队列、goroutine、Redis 通知、日志和指标 MUST NOT 作为任务存在、顺序或完成状态的真相源。

#### Scenario: 提交一个合法任务
- **WHEN** 已评审的内部业务 service 使用已注册类型、合法 payload、稳定 operation ID 和未使用幂等键提交事务
- **THEN** PostgreSQL 保存一条 pending 任务、初始不可变事件和对应 Outbox，进程重启后仍可发现同一任务

#### Scenario: 相同请求重放
- **WHEN** 调用方用相同幂等键及完全相同的任务类型、operation ID、payload hash 和执行策略再次入队
- **THEN** Control 返回既有 job ID，不创建第二个任务、事件或 Outbox

#### Scenario: 幂等键内容冲突
- **WHEN** 已存在幂等键被用于不同类型、operation ID、payload 或执行策略
- **THEN** Control fail closed 并保留原任务，不覆盖内容也不创建第二条记录

#### Scenario: 未注册或危险 payload
- **WHEN** 调用方提交未知任务类型/schema、未知字段、超过大小上限、Secret-like 字段、凭证、原始外部响应或 hash 不一致 payload
- **THEN** Control 拒绝入队，且数据库、日志、指标、Trace 和审计不得留下该敏感内容

### Requirement: 业务状态、审计、任务和 Outbox 原子提交
Control MUST 提供加入调用方现有数据库事务的入队原语，使未来业务能力能够把期望状态、对应成功审计、任务、初始任务事件和 Outbox 在一个事务中提交或回滚。入队原语 MUST NOT 隐式独立提交，也 MUST NOT 在数据库事务提交前发布任何唤醒通知。

#### Scenario: 原子提交成功
- **WHEN** 业务状态、成功审计、任务、事件和 Outbox 的全部写入均成功且调用方提交事务
- **THEN** 后续数据库扫描可以同时看到完整业务状态和任务证据，并且提交后唤醒可以安全发生

#### Scenario: 任一写入失败
- **WHEN** 业务状态、审计、任务、事件或 Outbox 任一步失败，或调用方回滚事务
- **THEN** 所有相关写入均不可见，Worker 不得发现或执行孤立任务，且不得提前发送通知

#### Scenario: 提交响应丢失后重试
- **WHEN** 数据库已经提交但调用方没有收到成功响应并以同一幂等键重试
- **THEN** Control 返回已提交的同一任务及业务操作关联，不重复业务状态、审计或 Outbox

### Requirement: Worker 以租约和 fencing 独占执行权
Control SHALL 使用 `FOR UPDATE SKIP LOCKED` 在短事务中认领到期的 pending 或 retry-wait 任务，并 MUST 原子写入 running、增加 attempt、设置数据库计算的 lease expiry、随机 fencing token 和生命周期事件。续租和任何业务/终态提交 MUST 同时匹配当前状态、未过期 lease 和 fencing token；失去执行权的 Worker MUST NOT 提交结果。

#### Scenario: 并发 Worker 认领同一任务
- **WHEN** 多个进程内 Worker 同时扫描同一条可执行任务
- **THEN** 只有一个 Worker 获得有效 lease 和 fencing token，其他 Worker 跳过该记录且不会增加 attempt

#### Scenario: 旧执行者在 lease 过期后回写
- **WHEN** 原 Worker 超时或暂停，lease 已过期或被 Reconciler 替换后尝试提交成功结果
- **THEN** fenced 更新影响零行，业务结果、任务终态和事件均不得由旧执行者提交

#### Scenario: 续租失败
- **WHEN** Worker 因数据库中断、token 不匹配或 lease 已过期而无法续租
- **THEN** Control 取消其本地执行 context，将结果视为未知，并等待 Reconciler 根据持久证据恢复

#### Scenario: 未来到期任务
- **WHEN** 任务的数据库 `available_at` 晚于当前数据库时间
- **THEN** Worker 不认领该任务；应用时区或本地 Clock 变化不得使它提前执行

### Requirement: 状态转换、重试和取消有界且可审计
Control SHALL 将任务状态限制为 `pending|running|verifying|retry_wait|rolling_back|succeeded|failed|rolled_back|cancelled`，并 MUST 拒绝非法转换和离开终态。重试 MUST 使用持久化 attempt、数据库 `available_at`、有上限退避和最大尝试次数；每次状态转换 MUST 与一个脱敏不可变事件原子提交。取消运行中或结果未知的任务 MUST 先登记请求并验证安全，不能通过直接改为 cancelled 隐藏潜在副作用。

#### Scenario: 可安全重试的执行失败
- **WHEN** 当前执行器明确证明未产生外部副作用、attempt 尚未耗尽且错误可重试
- **THEN** 任务进入 retry_wait，持久化下次数据库可用时间和固定错误码，重启不会重置预算或退避

#### Scenario: 达到尝试上限
- **WHEN** 任务已耗尽最大执行或验证预算
- **THEN** 任务进入 failed 人工处置终态并产生事件，不再自动认领或无限重试

#### Scenario: 取消尚未执行的任务
- **WHEN** 内部受控调用对 pending 或 retry_wait 任务请求取消
- **THEN** 任务原子进入 cancelled 并产生事件，Worker 随后不得认领它

#### Scenario: 取消结果未知的任务
- **WHEN** running、verifying 或 rolling_back 任务收到取消请求
- **THEN** Control 只持久化取消请求，由有效执行器或 Reconciler 验证后决定 cancelled、rolling_back 或 failed，不伪造副作用不存在

#### Scenario: 修改不可变事件或终态
- **WHEN** 任意运行时调用方尝试更新/删除生命周期事件，或把 succeeded、failed、rolled_back、cancelled 改回可执行状态
- **THEN** 数据库拒绝操作并保留原任务证据

### Requirement: Reconciler 对未知结果先验证再恢复
Control MUST 扫描 lease 已过期的 running、verifying 或 rolling-back 任务，并使用新的恢复 lease 调用任务类型注册的 Verify 能力。Reconciler MUST 依据可证明的 `effect_applied`、`effect_not_applied`、`effect_partial_or_rollback_required` 或 `effect_unknown` 结果推进；在未证明先前操作未生效时 MUST NOT 直接再次 Execute。

#### Scenario: 外部效果已生效但回写前崩溃
- **WHEN** Worker 已完成操作但在提交任务成功前崩溃，Verify 使用稳定 operation ID 证明效果已经生效
- **THEN** Reconciler 以当前 fencing token 提交业务确认和 succeeded，不重复执行该操作

#### Scenario: 证明效果未生效
- **WHEN** 过期任务的 Verify 明确证明没有产生效果，类型声明可安全重放且仍有尝试预算
- **THEN** Reconciler 将原任务置为 retry_wait，后续继续使用同一 job ID、operation ID 和 payload

#### Scenario: 部分效果需要回滚
- **WHEN** Verify 证明只完成部分效果，且已注册任务类型允许并定义回滚
- **THEN** 任务进入 rolling_back 并通过受 lease/fencing 保护的回滚流程处置

#### Scenario: 实际状态无法确认
- **WHEN** Verify 不可用、返回未知、类型/schema 不匹配或 operation ID 不能定位实际状态
- **THEN** Control 进行有限验证重试并最终进入 failed 人工处置，不盲目重放、伪造成功或删除任务

#### Scenario: Reconciler 与旧 Worker 竞态
- **WHEN** Reconciler 接管过期任务的同时旧 Worker 恢复并尝试提交
- **THEN** 新 fencing token 只允许一个状态推进，另一个提交失败且不得产生部分业务写入

### Requirement: Outbox 通知仅作为可选唤醒
Control SHALL 将最小唤醒 Outbox 与任务同事务保存。配置 Publisher 时，Dispatcher MUST 使用 lease、fencing、有限重试和 at-least-once 语义发布；未配置 Publisher 时 Outbox MUST 明确进入 suppressed 状态。无论通知丢失、重复、乱序、延迟、永久失败或未配置，Worker MUST 继续定期扫描 PostgreSQL，任务结果 MUST NOT 依赖通知。

#### Scenario: 未配置 Publisher
- **WHEN** Control 以默认 PostgreSQL-only 模式创建任务
- **THEN** Outbox 记录为 publisher-disabled/suppressed，Worker 仍通过数据库扫描发现并推进任务，且不要求 Redis 可用

#### Scenario: 通知丢失或 Publisher 不可用
- **WHEN** Outbox 一直未发送或 Publisher 返回故障
- **THEN** Dispatcher 按有限策略重试并暴露积压，任务仍能由数据库轮询执行

#### Scenario: 发布成功后回写前崩溃
- **WHEN** Publisher 已接收通知但 Dispatcher 在标记 sent 前崩溃
- **THEN** 租约恢复后可以重复发布同一 event ID，消费者只提前扫描 PostgreSQL，不直接按消息执行任务

#### Scenario: 伪造或重复通知
- **WHEN** 消费者收到重复、乱序或不存在 job ID 的唤醒通知
- **THEN** 消费者仅触发有界数据库扫描，数据库中没有可执行任务时不产生任何操作或状态

### Requirement: 管理员只能读取脱敏任务状态
Control SHALL 提供 `GET /api/jobs` 和 `GET /api/jobs/{job_id}`，仅允许现有有效实名 `super_admin` 会话，并 MUST 使用 `Cache-Control: no-store`、稳定有界 cursor 分页和固定过滤器。响应 MUST NOT 包含 payload、payload hash、幂等键、lease owner/token、Outbox envelope、错误摘要、Secret 或原始外部响应；本 change MUST NOT 提供任务创建、重试、取消或删除的产品 HTTP API。

#### Scenario: 管理员查看任务列表和详情
- **WHEN** 有效 `super_admin` 读取任务列表或现有详情
- **THEN** Control 返回公开标识、固定类型/状态、attempt、UTC 时间、固定错误码、Outbox 聚合状态和脱敏事件，并设置 `no-store`

#### Scenario: 未认证或失效会话读取
- **WHEN** 缺少、过期、撤销或伪造会话访问任务 API
- **THEN** Control 按现有管理员访问边界拒绝请求，且不泄露任务是否存在

#### Scenario: 尝试通过 HTTP 操作任务
- **WHEN** 客户端对任务路径发送 `POST`、`PUT`、`PATCH` 或 `DELETE`
- **THEN** Control 不创建、重试、取消、篡改或删除任务，OpenAPI 也不声明这些操作

#### Scenario: 数据库读取故障与恢复
- **WHEN** 任务 API 读取 PostgreSQL 失败，随后数据库连接恢复
- **THEN** 故障请求返回脱敏可重试 `503` 且不返回旧缓存；后续请求无需重启即可返回当前任务状态

#### Scenario: 只读页面
- **WHEN** 管理员打开懒加载任务页面
- **THEN** 页面只调用同源生成客户端并展示脱敏状态，不访问 Gateway/Node endpoint，也不显示任务操作控件

### Requirement: 任务可观测性保持固定低基数
Control SHALL 暴露任务状态数量、最老可执行 pending 时长、过期 lease 数和最老待发送 Outbox 时长。指标标签 MUST 仅使用封闭任务状态；job kind、job ID、operation ID、幂等键、payload、错误内容、lease/Outbox 标识和任务时间 MUST NOT 作为标签。结构化日志和事件 MUST 使用固定分类并保持敏感信息脱敏。

#### Scenario: 抓取任务指标
- **WHEN** Prometheus 抓取 Control 指标
- **THEN** 返回固定状态聚合和无身份标签的时长/数量，不产生逐任务时间序列

#### Scenario: 记录任务失败
- **WHEN** Worker、Reconciler 或 Dispatcher 发生执行、验证、数据库或发布故障
- **THEN** Control 只记录固定 component/action/result/job-kind/error-code 和安全标识，不记录 payload、Secret、错误原文、SQL 参数或外部响应

#### Scenario: 指标或日志后端故障
- **WHEN** 指标采集或日志输出失败
- **THEN** 任务正确性仍只由 PostgreSQL 事务、状态、lease 和 fencing 决定，不因此放行非法转换或回滚已提交状态

### Requirement: 任务基础故障不得进入请求数据面
持久任务能力 SHALL 只影响 Control 控制面。Worker、Reconciler、Dispatcher、Control 进程或其 PostgreSQL 停止时，Gateway 和 Relay Node 的已有请求处理 MUST 继续独立运行。本 change 的生产任务类型和外部执行器集合 MUST 为空，启动、扫描、只读页面、故障恢复和停机 MUST NOT 调用 Gateway、Node 或互联网服务，也不得采集账号、压缩账号历史或修改资产。

#### Scenario: 空生产 registry 启动
- **WHEN** 新版本 Control 在没有业务 job kind 和 Executor 的数据库上启动全部任务循环
- **THEN** 循环安全扫描空队列并提供指标/只读空状态，不发起任何外部请求

#### Scenario: Control 或数据库停止
- **WHEN** Control 或任务数据库不可用
- **THEN** 控制面任务暂停且不被伪造成功，Gateway 和 Relay Node 继续处理既有业务请求

#### Scenario: 数据库恢复和进程重启
- **WHEN** PostgreSQL 恢复或 Control 在已有非终态任务上重启
- **THEN** Control 仅依据持久任务、lease 和事件恢复；未知结果交给 Reconciler，不依赖内存消息补写或重放

#### Scenario: 应用回滚
- **WHEN** Control 回滚到不识别任务表的兼容旧版本
- **THEN** 新表、任务、Outbox 和事件继续保留且不会影响数据面，重新升级后可以从持久状态恢复
