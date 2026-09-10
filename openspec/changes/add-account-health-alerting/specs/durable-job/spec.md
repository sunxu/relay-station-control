## ADDED Requirements

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

## MODIFIED Requirements

### Requirement: 状态转换、重试和取消有界且可审计
Control SHALL 将任务状态限制为 `pending|running|verifying|retry_wait|rolling_back|succeeded|failed|rolled_back|cancelled`，并 MUST 拒绝非法转换和离开终态。重试 MUST 使用持久化 attempt、数据库 `available_at`、有上限退避和最大尝试次数；每次状态转换 MUST 与一个脱敏不可变事件原子提交。取消运行中或结果未知的任务 MUST 先登记请求并验证安全，不能通过直接改为 cancelled 隐藏潜在副作用。对于启用 allow_unknown_effect_replay 的 job，已请求取消或已达终止期限 MUST 阻止自动重放；无法确认外部效果时进入 failed 而非伪造安全取消，普通 job 仍沿用 Verify-first 取消处理。

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

#### Scenario: 显式 policy 允许未知结果重试
- **WHEN** 有效 Worker 的 Execute 遇 timeout 或 write 后 connection reset 等未知外部结果，job snapshot 启用 allow_unknown_effect_replay 且通过 policy consistency check，仍有 Execute budget 且无取消/期限阻止
- **THEN** 在现有 fenced 事务中将同一任务转为 retry_wait，持久化退避与事件；后续正常 claim 后再次 Execute，不先验证、不伪造 effect_not_applied

#### Scenario: 缺省策略不放宽
- **WHEN** 普通 job 面临相同未知结果，但 policy 为 false 或缺省，即使 replay_safe=true
- **THEN** 不得直接 retry Execute，仍进入既有 Verify-first 路径；只有证明效果未生效且原重试条件满足才允许重放

### Requirement: Reconciler 对未知结果先验证再恢复
Control MUST 扫描 lease 已过期的 running、verifying 或 rolling-back 任务。通过 policy consistency check 且持久化 snapshot 未启用 allow_unknown_effect_replay 的 job MUST 使用新的恢复 lease 调用任务类型注册的 Verify 能力。仅对通过 policy consistency check 且持久化 snapshot 已启用该 policy 的 running expired-lease job，Reconciler SHALL 取得新的有效恢复 lease/fencing 后，在剩余 Execute budget 且无取消/期限阻止时按原持久退避进入 retry_wait；否则 failed。后续执行必须正常 Worker claim 新 execution lease/fencing，不得在恢复扫描内直接 Execute。Reconciler MUST 依据可证明的 `effect_applied`、`effect_not_applied`、`effect_partial_or_rollback_required` 或 `effect_unknown` 结果推进；未启用 policy 时，在未证明先前操作未生效时 MUST NOT 直接再次 Execute。该例外只授权明确启用 policy 的 unknown-result replay，不扩大 replay_safe；普通 verifying/rolling_back 规则保持。

#### Scenario: 外部效果已生效但回写前崩溃
- **WHEN** 未启用 policy 的 Worker 已完成操作但在提交任务成功前崩溃，Verify 使用稳定 operation ID 证明效果已经生效
- **THEN** Reconciler 以当前 fencing token 提交业务确认和 succeeded，不重复执行该操作

#### Scenario: 证明效果未生效
- **WHEN** 未启用 policy 的过期任务的 Verify 明确证明没有产生效果，类型声明可安全重放且仍有尝试预算
- **THEN** Reconciler 将原任务置为 retry_wait，后续继续使用同一 job ID、operation ID 和 payload

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
- **THEN** Reconciler 按有效恢复 lease/fencing 将原 job 置为 retry_wait；后续 claim 可重复通知，保留 job_id、operation_id、payload、payload hash、idempotency key 与原持久退避，旧 Worker 无权提交

#### Scenario: 第五次未知结果与重启
- **WHEN** DingTalk 第五次 Execute 后外部结果未知或 lease 过期，包括重启恢复
- **THEN** 原 attempt 不重置，任务进入 failed；不得发生第六次 Execute，不进入 verifying 或新增状态

### Requirement: 任务基础故障不得进入请求数据面
持久任务能力 SHALL 只影响 Control 控制面。Worker、Reconciler、Dispatcher、Control 进程或其 PostgreSQL 停止时，Gateway 和 Relay Node 的已有请求处理 MUST 继续独立运行。Phase 5 SHALL 仅增加 dingtalk_alert_delivery 生产类型及对应 DingTalk 外部通知执行器，启用 allow_unknown_effect_replay；Gateway/Node 调用、账号采集、历史压缩和资产修改不属于该 job。仅已配置通知且有已提交任务时才可发送 direct HTTPS DingTalk 请求；无配置或无任务时，启动、扫描、只读页面和停机不得产生通知请求。

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
