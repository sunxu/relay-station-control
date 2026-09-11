# durable-job Specification

## Purpose
为单实例 Relay Station Control 提供以 PostgreSQL 为唯一真相源、可从崩溃恢复且不依赖 Redis 正确性的持久任务基础，使后续控制面操作能够原子入队、受租约执行、先验证再恢复并由管理员安全查看状态。

## Requirements

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
Control SHALL 将任务状态限制为 `pending|running|verifying|retry_wait|rolling_back|succeeded|failed|rolled_back|cancelled`，并 MUST 拒绝非法转换和离开终态。重试 MUST 使用持久化 attempt、数据库 `available_at`、有上限退避和最大尝试次数；每次状态转换 MUST 与一个脱敏不可变事件原子提交。取消运行中或结果未知的任务 MUST 先登记请求并验证安全，不能通过直接改为 cancelled 隐藏潜在副作用。对于启用 allow_unknown_effect_replay 的 job，已请求取消或已达终止期限 MUST 阻止自动重放；无法确认外部效果时进入 failed 而非伪造安全取消，普通 job 仍沿用 Verify-first 取消处理。

#### Scenario: 可安全重试的执行失败
- **WHEN** 当前执行器明确证明本次 attempt 未产生外部副作用、attempt 尚未耗尽、错误可重试且锁内无取消
- **THEN** 任务进入 retry_wait，记录 execute_retryable_no_effect，持久化下次数据库可用时间和固定错误码；此前 unknown 不被消解，重启不重置预算或退避；锁内有取消则按上述 A/C 收敛

#### Scenario: 达到尝试上限
- **WHEN** 任务已耗尽最大执行或验证预算
- **THEN** 任务进入 failed 人工处置终态并产生事件，不再自动认领或无限重试

#### Scenario: 取消尚未执行的任务
- **WHEN** 内部受控调用对没有 unresolved unknown evidence 的 pending 或 retry_wait 任务请求取消
- **THEN** 任务原子进入 cancelled 并产生事件，Worker 随后不得认领它

#### Scenario: 取消结果未知的任务
- **WHEN** running、verifying 或 rolling_back 任务收到取消请求
- **THEN** Control 先持久化取消请求，由有效执行器或 Reconciler 根据效果证据决定；Worker 已证明当前 no-effect 且无 unresolved unknown 时适用上述窄 running→cancelled，其余沿用 Verify-first 或显式 unknown/direct-success 取消规则，不伪造副作用不存在

#### Scenario: 修改不可变事件或终态
- **WHEN** 任意运行时调用方尝试更新/删除生命周期事件，或把 succeeded、failed、rolled_back、cancelled 改回可执行状态
- **THEN** 数据库拒绝操作并保留原任务证据

#### Scenario: 显式 policy 允许未知结果重试
- **WHEN** 有效 Worker 的 Execute 遇 timeout 或 write 后 connection reset 等未知外部结果，job snapshot 启用 allow_unknown_effect_replay 且通过 policy consistency check，仍有 Execute budget 且无取消/期限阻止
- **THEN** 在现有 fenced 事务中将同一任务转为 retry_wait，记录 reason_code=effect_unknown_unverified 及 error_code=execution_result_unknown，持久化退避；锁内重检取消/期限/预算，后续正常 claim 后再次 Execute，不先验证、不伪造 effect_not_applied

#### Scenario: 缺省策略不放宽
- **WHEN** 普通 job 面临相同未知结果，但 policy 为 false 或缺省，即使 replay_safe=true
- **THEN** 不得直接 retry Execute，仍进入既有 Verify-first 路径；只有证明效果未生效且原重试条件满足才允许重放

### Requirement: Reconciler 对未知结果先验证再恢复
Control MUST 扫描 lease 已过期的 running、verifying 或 rolling-back 任务。通过 policy consistency check 且持久化 snapshot 未启用 allow_unknown_effect_replay 的 job MUST 使用新的恢复 lease 调用任务类型注册的 Verify 能力。仅对通过 policy consistency check 且持久化 snapshot 已启用该 policy 的 running expired-lease job，Reconciler SHALL 取得新的有效恢复 lease/fencing 后，在剩余 Execute budget 且无取消/期限阻止时按原持久退避进入 retry_wait；否则 failed。后续执行必须正常 Worker claim 新 execution lease/fencing，不得在恢复扫描内直接 Execute。Reconciler MUST 依据可证明的 `effect_applied`、`effect_not_applied`、`effect_partial_or_rollback_required` 或 `effect_unknown` 结果推进；未启用 policy 时，在未证明先前操作未生效时 MUST NOT 直接再次 Execute。该例外只授权明确启用 policy 的 unknown-result replay，不扩大 replay_safe；普通 verifying/rolling_back 规则保持。

#### Scenario: 外部效果已生效但回写前崩溃
- **WHEN** 未启用 policy 的 Worker 已完成操作但在提交任务成功前崩溃，Verify 使用稳定 operation ID 证明效果已经生效
- **THEN** Reconciler 以当前 fencing token 提交业务确认和 succeeded，不重复执行该操作

#### Scenario: 证明效果未生效
- **WHEN** 既有 verifying 路径的 VerifyEffectAbsent 明确证明稳定 operation 没有效果，类型声明可安全重放且既有重试条件满足
- **THEN** Reconciler 将原任务由 verifying 置为 retry_wait，记录 effect_absent_verified 消解此前 unknown；后续继续使用同一 job ID、operation ID 和 payload，不引入 DingTalk Verify 路径

#### Scenario: 部分效果需要回滚
- **WHEN** 普通 job 的 Verify 证明只完成部分效果，且已注册任务类型允许并定义回滚
- **THEN** 任务进入 rolling_back 并通过受 lease/fencing 保护的回滚流程处置

#### Scenario: 实际状态无法确认
- **WHEN** 未启用 policy 的 Verify 不可用、返回未知或 operation ID 不能定位实际状态；或任意任务类型/schema/策略不匹配
- **THEN** Control 对可验证者进行有限验证重试，无法安全验证者 fail closed，最终进入 failed 人工处置，不盲目重放、伪造成功或删除任务

#### Scenario: Reconciler 与旧 Worker 竞态
- **WHEN** Reconciler 接管过期任务的同时旧 Worker 恢复并尝试提交
- **THEN** 新 fencing token 只允许一个状态推进，另一个提交失败且不得产生部分业务写入

#### Scenario: DingTalk 成功后回写前崩溃
- **WHEN** 已启用 policy 的 DingTalk POST 实际成功但未提交 succeeded，running lease 过期且仍有 Execute budget
- **THEN** Reconciler 在无取消/期限阻止时按有效恢复 lease/fencing 将原 job 置为 retry_wait，记录 effect_unknown_unverified；后续 claim 可重复通知，保留 job_id、operation_id、payload、payload hash、idempotency key 与原持久退避，旧 Worker 无权提交

#### Scenario: 第五次未知结果与重启
- **WHEN** DingTalk 第五次 Execute 后外部结果未知或 lease 过期，包括重启恢复
- **THEN** 原 attempt 不重置，任务进入 failed；不得发生第六次 Execute，不进入 verifying 或新增状态

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
持久任务能力 SHALL 只影响 Control 控制面。Worker、Reconciler、Dispatcher、Control 进程或其 PostgreSQL 停止时，Gateway 和 Relay Node 的已有请求处理 MUST 继续独立运行。Phase 5 SHALL 仅增加 dingtalk_alert_delivery 生产类型及对应 DingTalk 外部通知执行器，启用独立的 allow_unknown_effect_replay 与 allow_direct_success；Gateway/Node 调用、账号采集、历史压缩和资产修改不属于该 job。仅已配置通知且有已提交任务时才可发送 direct HTTPS DingTalk 请求；无配置或无任务时，启动、扫描、只读页面和停机不得产生通知请求。

#### Scenario: 空生产 registry 启动
- **WHEN** Control 在没有业务 job kind 和 Executor 的数据库上启动全部任务循环
- **THEN** 循环安全扫描空队列并提供指标/只读空状态，不发起任何外部请求

#### Scenario: 无通知任务启动
- **WHEN** Phase 5 已注册 DingTalk kind，但无通知配置或可执行通知任务
- **THEN** 循环安全扫描并提供指标/只读状态，不发起任何外部通知；未配置时不 enqueue，也不补发历史 ACTIVE

#### Scenario: Control 或数据库停止
- **WHEN** Control 或任务数据库不可用
- **THEN** 控制面任务暂停且不被伪造成功，Gateway 和 Relay Node 继续处理既有业务请求

#### Scenario: 数据库恢复和进程重启
- **WHEN** PostgreSQL 恢复或 Control 在已有非终态任务上重启
- **THEN** Control 仅依据持久任务、lease 和事件恢复；未知结果交给 Reconciler 按显式 policy 恢复；默认 Verify-first，DingTalk 可在预算内重放，不依赖内存消息补写或重放

#### Scenario: 应用回滚
- **WHEN** Control 回滚到不识别任务表的兼容旧版本
- **THEN** 新表、任务、Outbox 和事件继续保留且不会影响数据面，重新升级后可以从持久状态恢复

### Requirement: Job-kind SHALL explicitly opt in to unknown-effect replay
Control SHALL 为 job-kind 定义独立 execution policy `allow_unknown_effect_replay`，默认 false；Phase 5 仅 `dingtalk_alert_delivery` 启用。策略 MUST 通过既有 registration/execution-policy contract 显式声明并保持一致，不得通过 `if job_kind == dingtalk...` 硬编码，不得扩大 `replay_safe` 定义。仅 replay_safe=true 不授权未知结果重放。

策略 MUST 沿用现有 per-job persisted execution-policy snapshot 模式：async_job_kinds.allow_unknown_effect_replay 为 boolean、default false；async_jobs.allow_unknown_effect_replay 为 boolean，在 enqueue 时从已验证的 job-kind definition/catalog 复制到具体 job，与既有 Timeout/LeaseDuration/HeartbeatInterval/MaxAttempts/MaxVerifyAttempts/ReplaySafe/AllowRollback 一起持久化。以上为本轮冻结的架构契约，不是 migration 实现。

同幂等键重入队的 execution-policy compatibility check、Registry/Catalog 与 DB catalog comparison、Worker/Reconciler 的 job-policy consistency check MUST 包含该字段。Worker/Reconciler MUST 依据 job 上的持久化 snapshot，而非当前进程 registry/config 动态授予旧 job replay 权限；snapshot 与当前 registry/catalog policy 不同 MUST fail closed / policy mismatch，不得覆盖 snapshot 或按新权限继续执行。重启、重入队或注册变化不得默默改变既有授权。未启用者保持 Verify-first；unknown 不得伪装为 effect_not_applied。

Invariant：allow_unknown_effect_replay=true REQUIRES replay_safe=true。Go Registry validation、DB catalog/schema constraint 或等价 validation（同时覆盖 catalog 与 job snapshot 持久化）、catalog compatibility validation MUST fail closed，拒绝 replay_safe=false / allow_unknown_effect_replay=true。DingTalk 固定 replay_safe=true、allow_unknown_effect_replay=true；普通 job 缺省 unknown replay=false。

启用策略只改变未知效果下的重放许可，不新增状态、retry engine、领域表、execution policy table、policy DSL、queue、notification table/outbox、delivery ledger、workflow engine 或 RBAC。job_id、operation_id、payload、payload hash、idempotency key、durable backoff 与 lease/fencing contract MUST 保持。DingTalk 的 max Execute attempts=5（包括第一次和重启后的重放），预算耗尽 failed；重复外部通知是明确接受的 at-least-once 行为，不保证永久失败下最终送达。

#### Scenario: 默认关闭与单一启用
- **WHEN** 注册普通 job 时缺省/关闭 policy，或注册 Phase 5 DingTalk job
- **THEN** 普通 job 保持 false；只有 DingTalk 注册为 true，策略经通用 execution-policy 路径执行，不按 kind 名特判

#### Scenario: 重放隔离对照
- **WHEN** DingTalk 与普通 job 遇到同样的 HTTP timeout、write 后 connection reset 或 running lease expiry
- **THEN** DingTalk 在预算内可进入 retry_wait 后由新有效 lease/token 执行；普通 job 不直接重放，保持 Verify-first；均不得伪造外部效果

#### Scenario: Enqueue 快照与幂等兼容
- **WHEN** 合法 job-kind 入队后，以相同幂等键及相同内容重入队，或只改变 allow_unknown_effect_replay 后重入队
- **THEN** 初次 enqueue 将 definition/catalog 的 boolean 复制到 async_jobs；完全匹配返回原 job，不匹配拒绝且保留原 snapshot，不创建新 job 或修改原授权

#### Scenario: 非法策略组合拒绝
- **WHEN** replay_safe=false 且 allow_unknown_effect_replay=true 被提交到 Go Registry、DB catalog/job 持久化或 catalog compatibility validation
- **THEN** 每个入口均 fail closed，registration / persistence rejected；不能通过绕过某个校验层启用更强执行授权

#### Scenario: Registry 与 DB catalog 比较
- **WHEN** Registry/Catalog 与 DB catalog 的 allow_unknown_effect_replay 不一致，包括 false/true 或 true/false
- **THEN** compatibility validation 拒绝该 catalog，不得基于不一致定义执行或恢复任务

#### Scenario: 旧 job 不继承当前 registry 权限
- **WHEN** 已持久化 job snapshot policy 与当前 registry/catalog policy 不同，包括重启后或 registry/config 更新后，Worker 或 Reconciler 尝试执行/恢复
- **THEN** fail closed / policy mismatch，不按新 registry 权限 Execute/replay，不修改旧 snapshot；只有兼容性验证通过后才可依据持久化 job policy 处理

### Requirement: Direct success SHALL require independent persisted authorization
Control SHALL 新增通用 Execute disposition `ExecuteSucceeded`，表示 Executor 从本次同步 Execute 获得足以确认操作成功的结果，且该 job kind 被显式授权跳过 Verify。它不是 unknown、effect_not_applied、needs verification 或 retryable result。

独立 execution-policy boolean `allow_direct_success` MUST 默认 false，Phase 5 仅 dingtalk_alert_delivery 为 true；不得按 kind 名硬编码。后续实现 MUST 在 async_job_kinds 与 async_jobs 分别使用 BOOLEAN NOT NULL DEFAULT FALSE，沿用 definition→enqueue snapshot→persisted per-job policy→Worker/Reconciler 的现有模式。jobs.Definition、jobs.CatalogEntry、jobs.Job、EnqueueTx、policyMatches、DB read/write mapping、Registry/Catalog 与 DB catalog comparison、同幂等键重入队及 Worker/Reconciler compatibility check MUST 包含此字段。任何 mismatch MUST fail closed，不覆盖旧 snapshot，registry/config 变化不得改变已存在 job 权限。本轮不实施 schema。

只有 ExecuteSucceeded、persisted job.allow_direct_success=true、policy consistency 校验通过且当前 running lease/fencing 有效、DB lock 下无取消请求，Worker 才 SHALL 直接 running→succeeded；复用 StatusSucceeded 与 EventSucceeded。DB lifecycle guard、event constraint、fenced-transition contract MUST 一致支持并约束该路径：from_status=running、to_status=succeeded、actor=worker，持久化 policy=true 是必要条件。未经授权返回 ExecuteSucceeded MUST fail closed，不得成功。旧/过期 token 不得提交，即使外部操作确已成功。

allow_direct_success 与 allow_unknown_effect_replay MUST 彼此独立：已知成功仅检查 direct-success 授权，未知效果重放仅检查 unknown-replay 授权及既有预算/取消/期限条件。不得相互推导授权；不新增 allow_direct_success⇒replay_safe invariant。已有 allow_unknown_effect_replay⇒replay_safe 保持。两个 policy 默认 false 的普通 job MUST 保持 ExecuteNeedsVerification→verifying→Verify 的既有成功/重试/回滚/失败路径。

此前 direct-success amendment SHALL 仅增加一个 disposition 与一个 persisted boolean；不得新增 status、event type、execution mode enum、policy table、strategy DSL、notification state machine、专用 queue/retry framework、verification receipt table 或 delivery ledger。

#### Scenario: Direct-success 默认与快照兼容
- **WHEN** 普通 kind 缺省注册或 DingTalk 注册后 enqueue，或 registry/catalog/同 key/job recovery 的 allow_direct_success 不匹配
- **THEN** 普通默认 false、DingTalk true，enqueue 保存快照；每个不匹配入口 fail closed，旧 job 不继承新 registry 权限

#### Scenario: Worker 与数据库授权对照
- **WHEN** 无取消请求的有效 Worker 返回 ExecuteSucceeded，分别使用 persisted allow_direct_success=true 与 false
- **THEN** 前者经已有 fenced 事务原子提交 succeeded 与 worker/running→succeeded event；后者 fail closed；直接调用 DB 也不能绕过 policy 检查

#### Scenario: 过期成功响应不能提交
- **WHEN** Worker 获得明确成功结果但 lease 已过期或 fencing 已被替换
- **THEN** succeeded 与其 event 均不能提交，不留下部分更新

#### Scenario: 三条结果路径分离
- **WHEN** DingTalk HTTP/business 均成功、DingTalk 外部结果未知、普通默认 job 外部结果未知
- **THEN** 分别为 无取消时 ExecuteSucceeded→direct succeeded、unknown policy 下有界 replay、Verify-first；不能伪造成功或 effect_not_applied

#### Scenario: 两个授权互不依赖
- **WHEN** synthetic kind 配置 direct=true/unknown=false/replay_safe=false，或 direct=false/unknown=true/replay_safe=true
- **THEN** 两种组合均不因额外 invariant 被拒绝；前者仅可明确直接成功，后者仅可未知重放且 ExecuteSucceeded 仍 fail closed

### Requirement: Cancellation SHALL use immutable effect evidence, not capability
Control MUST 使用既有 async_job_events.reason_code 记录 framework-generated 固定语义，Executor 不得任意提供这些 reason codes。allow_unknown_effect_replay 是权限，不是外部效果事实；control_request_async_job_cancel() 与 control_transition_async_job_fenced() MUST 根据真实证据判定安全性，不根据 policy boolean 推断历史效果。

- effect_unknown_unverified：Worker ExecuteResultUnknown 或 Reconciler 接管 expired running lease，在持久化 unknown-replay 授权及既有预算/取消/期限条件满足后转 retry_wait 时 MUST 记录，表示外部效果未知且尚未 Verify。
- execute_retryable_no_effect：Worker ExecuteRetryableNoEffect 的固定 reason，仅证明当前 attempt 无效果，不消解此前 unknown。
- effect_absent_verified：VerifyEffectAbsent 导致 verifying→retry_wait 时 MUST 记录，证明稳定 operation 无效果并消解此前 unknown。

未解决 unknown MUST 按同一 job 的既有 immutable event sequence 判断：只考虑最新 effect_absent_verified 之后的 effect_unknown_unverified（无 verified marker 时考虑全部）。当前 transition 的 unknown 证据也 MUST 在写事件前的锁内判断中计入。不得把“历史上曾 unknown”永久 sticky，也不得把 error_code 或 policy=true 替代这些固定语义证据。本规则 supersede 当前未提交 Slice A 中 capability/evidence 混淆及永久历史 unknown 判断；本轮不改实现。

只有有效 Worker、running lease/fencing、已可见 cancel_requested_at、当前 ExecuteRetryableNoEffect 的显式 framework proof、且无 unresolved prior unknown 时，DB SHALL 允许窄 running→cancelled：EventCancelled、actor=worker、error_code=cancel_verified_safe、release lease=true。当前 proof 的 reason 为 execute_retryable_no_effect。取消与 transition MUST 在 DB lock 下重检并与事件原子提交；不可仅依赖 claim 时读取的取消状态。缺少任何必要证据、旧/过期 fence、终态均不得绕过 guard。

当前 unknown 或此前 unresolved unknown 存在时，取消 MUST failed / cancel_after_unknown_effect，不能冒充安全取消。ExecuteSucceeded 的 direct-success fenced commit 若在锁内看到取消，MUST running→failed / cancel_after_effect_applied、actor=worker，不得 succeeded；不新增 generic direct-success 自动 rollback 或 running→rolling_back，未来需要时另开 architecture change。ExecuteNeedsVerification 加取消仍 running→verifying 并保留取消请求，由既有 Verify outcomes 决定；ExecutePermanentFailure 保持 fail-closed，不推导 no-effect。

本 cancellation amendment 的最大增量 SHALL 为一个窄 existing-state transition running→cancelled 加既有 immutable events 的 fixed reason semantics；不新增 status、event type、policy、列、表、ledger、queue、cancellation worker 或 retry engine。

#### Scenario: A 当前无效果且无此前 unresolved unknown
- **WHEN** unknown replay policy 为 true 或 false，当前 ExecuteRetryableNoEffect，取消在 claim 后、fenced commit 前可见，且无 unresolved unknown
- **THEN** 有效 Worker 原子 running→cancelled，EventCancelled、actor=worker、cancel_verified_safe 并释放 lease；不能因 policy=true 分类为 cancel_after_unknown_effect

#### Scenario: B 当前 unknown 与并发取消
- **WHEN** ExecuteResultUnknown 且 unknown replay 已授权，取消在 retry commit 前可见
- **THEN** failed / cancel_after_unknown_effect，不能 cancelled 或继续 replay

#### Scenario: C 此前 unknown 未消解而当前无效果
- **WHEN** attempt 1 unknown→retry_wait 记录 effect_unknown_unverified，attempt 2 known-no-effect→retry_wait 记录 execute_retryable_no_effect，此后取消（或取消在第二次 transition 前可见）
- **THEN** failed / cancel_after_unknown_effect；第二次无效果不抹除第一次未知效果

#### Scenario: D Verify 消解此前 unknown
- **WHEN** unknown 经 verifying 与 VerifyEffectAbsent 转 retry_wait，记录 effect_absent_verified，之后无新 unknown 而收到取消
- **THEN** cancelled / cancel_verified_safe；旧 unknown 不再污染任务，若 verified marker 后出现新 unknown 则仍须 fail closed

#### Scenario: E 普通 retry_wait 取消
- **WHEN** retry_wait 任务没有 unresolved unknown evidence 并收到取消
- **THEN** 保持既有 cancelled 语义；不因 unknown replay capability 开启而变成未知效果失败

#### Scenario: Direct success 与取消竞态
- **WHEN** 已获 direct 授权的有效 Worker 返回 ExecuteSucceeded，但 DB lock 下 cancel_requested_at 已非空
- **THEN** running→failed / cancel_after_effect_applied、actor=worker，不能 succeeded 或自动 rolling_back；两个 policy 仍独立

#### Scenario: 证据与授权边界回归
- **WHEN** 对 Worker/Reconciler unknown 恢复、known-no-effect、VerifyEffectAbsent、并发取消分别验证，并尝试过期 fence、耗尽预算或已到 deadline 的 replay
- **THEN** framework 生成对应固定 reason，Verify marker 只消解此前 unknown；lease/fencing、预算、deadline 仍阻止非法 replay；普通 job Verify-first 与 permanent-failure 行为不被放宽
