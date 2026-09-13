## MODIFIED Requirements

### Requirement: Control SHALL 只为符合资格的 Node 创建唯一 UTC 固定槽

Control SHALL 使用 PostgreSQL UTC 时间计算五分钟固定 `scheduled_at`，并 MUST 只为该时间点处于显式账号监控激活区间、声明 `management_account_inventory_read` 且存在匹配 Node type/Driver contract 有效 Provider 策略的 Node 创建 poll run。每个 `(instance_id, scheduled_at)` MUST 最多一行，首次创建后 `provider_policy_version` MUST 不可改变。

Poll scheduling MUST require `relay_node_assets.lifecycle_status='active'`, a non-cancelled
monitoring activation covering the database slot time, the inventory capability and a matching
Provider policy. Retired Nodes, cancelled future activations and replaced old identities MUST not
create new poll runs.

#### Scenario: 当前槽首次调度
- **WHEN** 数据库时间进入一个五分钟槽，Node 的 capability、监控区间和 Provider 策略在该 `scheduled_at` 均有效
- **THEN** Control 创建一条 `status=pending` 的 poll run，并固定该槽对应的不可变策略版本

#### Scenario: 重复 tick 或重启后调度同一槽
- **WHEN** Scheduler 对同一 Node 和 `scheduled_at` 重复运行，或 Control 在当前槽内重启
- **THEN** Control 复用原 poll run，不创建第二行、不重置时间/attempt，也不把策略版本替换为当前新版本

#### Scenario: Node 不在监控区间或缺少能力
- **WHEN** `scheduled_at` 不属于 Node 监控激活区间，或 Node 未声明账号清单 capability
- **THEN** Control 不创建 poll run、不调用 Driver，且该槽不因 Gateway/Compose 状态被隐式纳入监控

#### Scenario: 资产或策略真相不一致
- **WHEN** capability/Driver contract 不匹配、策略激活重叠或 binding 与激活版本矛盾
- **THEN** Scheduler fail closed 并记录固定分类，不猜测策略、不自动修复数据，也不调用 Node

### Requirement: Worker SHALL 先取得并发额度再认领并立即调用 Driver

Worker SHALL 在认领数据库记录前取得有界 HTTP 并发额度。认领 MUST 使用短事务、`FOR UPDATE SKIP LOCKED`、数据库时间、随机 fencing token 和执行 lease，原子设置 running、增加 attempt 并返回剩余 grace；认领提交后 MUST 不再排入其他队列；Worker MUST 先完成下述独立 dispatch authorization 校验，再在其返回的 lease/grace/请求超时共同边界内立即调用固定 Node Driver。认领本身不授予 outbound 权限。

Claim and preflight MUST re-read the Node lifecycle and monitoring eligibility in a short
transaction. `account_inventory_poll_runs` MUST persist dispatch authorization as durable columns
distinct from claim/lease: `dispatch_authorized_attempt integer NULL` (MUST equal the row's
current `attempt_count` when set), `dispatch_authorized_at timestamptz NULL` (database
transition time) and `dispatch_authorized_fencing_token uuid NULL` (MUST equal the row's current
`lease_fencing_token` when set). The three columns MUST be all NULL or all non-NULL together, and
non-NULL IFF all of: `status='running'`, `dispatch_authorized_attempt=attempt_count`, and
`dispatch_authorized_fencing_token=lease_fencing_token`. For every other status
(`pending`, `retry_wait`, `finalized`, `abandoned`) the three columns MUST be all NULL. Their
lifecycle is frozen per current `attempt_count`:

- While `status='running'` for the current `attempt_count` and the three columns are NULL, a
  short Node-locked authorization transaction MUST read a single `database_now` and grant
  authorization only if ALL of the following hold: `status='running'`; the three authorization
  columns are NULL; `attempt_count` equals the expected current attempt; `lease_fencing_token`
  equals the expected/current fencing token; `lease_expires_at > database_now`;
  `scheduled_at + make_interval(secs => poll_start_grace_seconds) > database_now`; the Node's
  `lifecycle_status='active'`; and current monitoring eligibility is true. If any check fails, NO
  authorization column is set and NO outbound request MUST be made. On success the transaction
  sets all three columns once (`dispatch_authorized_attempt=attempt_count`,
  `dispatch_authorized_fencing_token=lease_fencing_token`, `dispatch_authorized_at=`db time)
  before releasing the Node lock and performing transport. Claiming the run (obtaining the lease,
  incrementing `attempt_count`) is NOT itself dispatch authorization. Exactly one
  `NULL -> authorized` transition is permitted per `running` attempt.
- A second authorization write for the same `attempt_count` (columns already non-NULL for that
  attempt) MUST be rejected; there is exactly one authorization per attempt.
- Each of the following terminal/retry transitions out of `running` MUST atomically clear all
  three authorization columns back to NULL in the same UPDATE that performs the transition:
  `running -> retry_wait`, `running -> finalized`, `running -> abandoned`.
- When `retry_wait` transitions to `running` for `attempt_count + 1` (a new lease and a new
  `lease_fencing_token`), the authorization columns MUST be NULL for the new attempt; the new
  attempt MUST obtain its own authorization and MUST NOT be considered authorized by the previous
  attempt's now-cleared columns.
- Authorization columns MUST NOT be set (and MUST be NULL) while
  `status IN ('pending','retry_wait','abandoned','finalized')`.

On a successful authorization, Control MUST compute and return `lease_remaining` (`lease_expires_at
- database_now`) and `grace_remaining` (`scheduled_at + poll_start_grace_seconds - database_now`)
from the same authorization transaction's `database_now`. The Worker's outbound transport deadline
MUST NOT be later than
`min(lease_expires_at, scheduled_at + poll_start_grace_seconds, database_now + T)`, where `T` is
the existing worst-case per-request timeout budget already defined by
`account-inventory-poll-capacity` (no second timeout configuration and no new durable deadline
column are introduced; the deadline is derived in-process from the existing `lease_expires_at`,
`scheduled_at`/`poll_start_grace_seconds` and `T`).

Control MUST NOT hold the Node lock across HTTP. The claim-to-retire race resolves by commit
order of the authorization write (not wall-clock socket-send time): if Node Retire commits before
the authorization write, the write fails and no HTTP is sent; if the authorization write commits
first, this bounded attempt MAY still complete transport even if Retire commits afterward, and
finalize MUST still fence promotion. Restart/reconciler MUST distinguish an authorized attempt
(all three columns non-NULL and matching the current `attempt_count`/`lease_fencing_token`) from
a non-authorized attempt (columns NULL, or non-NULL but stale relative to the current
`attempt_count`/`lease_fencing_token`) before deciding recovery/abandonment.

#### Scenario: HTTP 并发额度已满
- **WHEN** 所有 poll 并发额度正在使用
- **THEN** 其他 poll run 保持 pending 且不开始 lease，释放额度后才允许认领

#### Scenario: 两个 Worker 竞争同一 poll run
- **WHEN** 两个 Worker 同时尝试认领同一 pending/retry_wait run
- **THEN** 只有一个 Worker 获得 running lease/fencing 并增加 `attempt_count`，另一个跳过该行且不
  调用 Node

#### Scenario: 认领后预处理耗尽 grace
- **WHEN** Secret/DNS 等 Driver 预处理尚未真正发出 HTTP 时，认领返回的剩余 grace 已耗尽
- **THEN** context 阻止 HTTP dispatch，Control 不越过宽限请求 Node，并按未完成 Control 执行恢复或 abandoned

#### Scenario: 容量配置危险
- **WHEN** 并发、最坏请求时长、grace、lease 和调度余量不能推导出至少一个安全 Node 容量，或 lease 校验不通过
- **THEN** poll service 在任何 Node 请求前拒绝启动并暴露固定配置错误；容量使用 account-inventory-poll-capacity 定义的统一公式，不使用人工 MAX_NODES 或独立50/10特例，Control 数据面隔离保持不变

#### Scenario: dispatch authorization 属于当前 attempt
- **WHEN** claim 把 `attempt_count` 增加到 N 后，dispatch authorization 事务写入
  `dispatch_authorized_attempt=N`、`dispatch_authorized_fencing_token=当前 lease_fencing_token`
- **THEN** 该行是本次 `attempt_count=N` 唯一合法的 authorized 记录，同一 attempt 内第二次授权写入
  被拒绝

#### Scenario: retry 清空并重新授权
- **WHEN** running attempt N 因合法 retry 条件转为 `retry_wait`，随后被重新 claim 为
  `attempt_count=N+1` 并进入新的 running
- **THEN** 转 `retry_wait` 时三列被原子清空为 NULL；`attempt_count=N+1` 必须重新写入自己的
  authorization 列，不得复用 N 的旧值判定为已授权

#### Scenario: 终态转换原子清空 authorization 列
- **WHEN** 一个持有效 authorization 的 `running` attempt 转为 `finalized` 或转为 `abandoned`
- **THEN** 该 UPDATE 必须在同一事务原子把三个 authorization 列清空为 NULL；`finalized`/
  `abandoned` 行的三列永远为 NULL，不保留最后一次授权的痕迹

#### Scenario: lease 已过期但 fencing token 未变
- **WHEN** dispatch authorization 事务读取 `database_now` 时发现 `lease_expires_at <=
  database_now`，即使 `lease_fencing_token` 仍与预期一致
- **THEN** authorization 被拒绝，不写入任何授权列，不发出任何 outbound HTTP

#### Scenario: grace 已过期但 lease 仍有效
- **WHEN** dispatch authorization 事务读取 `database_now` 时发现
  `scheduled_at + make_interval(secs => poll_start_grace_seconds) <= database_now`，即使
  `lease_expires_at` 仍晚于 `database_now`
- **THEN** authorization 被拒绝，不写入任何授权列，不发出任何 outbound HTTP

#### Scenario: authorization 临近 deadline 时 outbound 被上限约束
- **WHEN** dispatch authorization 成功写入且 `lease_remaining`/`grace_remaining` 已接近零
- **THEN** Worker 的 outbound transport context deadline 不得晚于
  `min(lease_expires_at, scheduled_at + poll_start_grace_seconds, database_now + T)`，其中 `T`
  为 `account-inventory-poll-capacity` 既有最坏请求时长配置，不引入第二套 timeout 配置或新的
  durable deadline 列

#### Scenario: Retire 先于 dispatch authorization 提交
- **WHEN** Node Retire 事务先取得 Node 锁并 commit lifecycle_status=retired
- **THEN** 随后等待锁的 dispatch authorization 写入在验证 active lifecycle 时失败，run 转为
  abandoned（`node_retired`），不发出 HTTP

#### Scenario: dispatch authorization 先于 Retire 提交
- **WHEN** dispatch authorization 事务先持 Node 锁验证通过并提交三个授权列，随后释放锁开始 HTTP
- **THEN** 即使 Node Retire 随后提交，本次 bounded attempt MAY 完成 transport；finalize 仍必须
  按 Node lifecycle fence 跳过 promotion

#### Scenario: restart 区分 authorized 与未授权 attempt
- **WHEN** Control 在 dispatch authorization 写入之前或之后崩溃并重启
- **THEN** reconciler 通过三列是否非 NULL 且等于当前 `attempt_count`/`lease_fencing_token` 区分
  已授权与未授权 attempt，未授权的按 grace 规则终止，已授权的按有界 transport 恢复语义处理，不
  凭空推测

### Requirement: finalize MUST 原子保存固定策略的完整聚合结果

Control SHALL 在一个 fenced PostgreSQL 事务中保存 poll run、其固定策略中全部 active Provider 的聚合结果、节点内重复证据、允许的 Provider snapshot promotion 和对应账号 lifecycle 转换，并 MUST 使用数据库时间生成 `observed_at`。结果集合 MUST 与 pinned policy 完全相等。事务 MUST 锁定当前 policy binding：在 Node lifecycle fence 通过后，版本变化时只保存采集证据并以 `policy_changed` 跳过所有 promotion/lifecycle；Node lifecycle/monitoring fence 通过且版本未变化时只为完整 runtime Provider 原子写 snapshot items、更新其当前指针、推进 lifecycle 并设置 `promotion_applied=true`。任一检查/写入失败 MUST 整体回滚。

Finalize MUST lock the poll/run as required, lock the Node, read one `database_now`, and verify the
same instance identity, Node lifecycle, current non-cancelled monitoring eligibility and poll
fencing token before promotion. Both existing promotion-skip layers additively gain the Node
lifecycle reasons and the monitoring eligibility reason: `account_inventory_poll_runs.
promotion_skipped_reason` (`NULL|policy_changed`) gains
`monitoring_ineligible|node_retired|node_replaced`, and
`account_inventory_poll_provider_results.promotion_skipped_reason` (`policy_changed|
transport_failed|contract_invalid|disk_fallback|provider_identity_incomplete|
provider_duplicate|stale_poll`) gains `monitoring_ineligible|node_retired|node_replaced`. No new promotion-skip
column is introduced. Reason precedence at finalize time is frozen as:

1. Node lifecycle fence (`node_retired`/`node_replaced`) — evaluated first.
2. monitoring eligibility fence (`monitoring_ineligible`).
3. `policy_changed`.
4. existing provider-specific evaluation (`transport_failed`, `contract_invalid`,
   `disk_fallback`, `provider_identity_incomplete`, `provider_duplicate`, `stale_poll`).

If the Node is retired or replaced at finalize time, the run's `promotion_skipped_reason` MUST be
set to `node_retired`/`node_replaced` (matching the Node's terminal lifecycle state) and every
pinned active Provider result row MUST have `promotion_applied=false` and the same
`promotion_skipped_reason`, regardless of what a provider-specific evaluation would otherwise have
produced; transport/provider evidence MUST still be preserved. In that case: no snapshot promotion, no Provider current pointer update, no account lifecycle
advancement, and no availability/request-quality current refresh MUST occur. If the Node remains
active and has a current row satisfying `cancelled_at IS NULL AND database_now <@ active_range`,
existing `policy_changed` and provider-specific evaluation continue to apply unchanged. If the
Node identity exists and remains active but that monitoring predicate is false, the run MUST remain
`finalized`, its run-level reason MUST be `monitoring_ineligible`, and all pinned active Provider
rows MUST use `promotion_applied=false` and `promotion_skipped_reason=monitoring_ineligible`.
Transport/provider observation metadata and historical evidence MUST remain durable, while
Provider current pointers, inventory current snapshots, account lifecycle, availability,
request-quality and Provider current health MUST remain unchanged. The database promotion
validator MUST accept this evidence-only finalized shape, reject
`monitoring_ineligible+promotion_applied=true`, and require the run reason and all pinned active
Provider reasons to agree.

#### Scenario: 多 Provider 中一个不完整
- **WHEN** contract-valid runtime 观察中一个 active Provider 缓存缺 email/重复，而另一个 active Provider 完整
- **THEN** finalized provider rows 只将问题 Provider 标为不完整/degraded且不提升或推进 lifecycle，完整 Provider 保存快照、更新当前指针、推进 lifecycle并 promotion applied

#### Scenario: active Provider 返回零记录
- **WHEN** 合法 runtime 观察对某 active Provider 完整且返回零记录
- **THEN** finalize 为该 Provider 保存完整空范围聚合结果、零条 item，并原子推进其当前指针、现有 active 账号缺失状态与 promotion applied

#### Scenario: poll 创建后策略切换
- **WHEN** poll run 固定旧策略后当前 binding 已切换到新版本
- **THEN** 本轮 Driver 解析和 provider rows 仍使用旧版本，采集证据 finalized但全部 promotion 以 `policy_changed` 跳过，不按新策略重解释旧响应或推进 lifecycle

#### Scenario: Node retired/replaced 时 finalize 冻结 promotion reason
- **WHEN** finalize 发现所属 Node 已 `retired` 或 `replaced`
- **THEN** run 级 `promotion_skipped_reason` 设为对应的 `node_retired`/`node_replaced`；全部
  pinned active Provider 行 `promotion_applied=false` 且 `promotion_skipped_reason` 与 run 一致
  （即使该 Provider 本身传输/契约完整）；transport/provider evidence 保留；不发生 snapshot
  promotion、Provider 当前指针更新、账号 lifecycle 推进或 availability/request-quality 当前刷新

#### Scenario: active Node 在 finalize 时 monitoring ineligible
- **WHEN** finalize 的 Node 锁与单一 `database_now` 已确认 Node identity 存在且 active，但没有
  `cancelled_at IS NULL AND database_now <@ active_range` 的 monitoring activation
- **THEN** run 进入 `finalized` 且 run 与全部 pinned active Provider reason 为
  `monitoring_ineligible`、Provider `promotion_applied=false`；transport/Provider evidence 与
  observation metadata 保留，所有 current truth 保持不变

#### Scenario: lifecycle reason 优先于 monitoring 与 policy
- **WHEN** Node 已 retired/replaced，同时 monitoring 不 eligible 或 policy 已变化
- **THEN** run 与全部 pinned active Provider 使用 `node_retired`/`node_replaced`，不得改记为
  `monitoring_ineligible` 或 `policy_changed`

#### Scenario: monitoring reason 优先于 policy
- **WHEN** Node active 但 monitoring 不 eligible，同时 policy 已变化
- **THEN** run 与全部 pinned active Provider 使用 `monitoring_ineligible`，不使用
  `policy_changed`

#### Scenario: monitoring eligibility 因自然边界失效
- **WHEN** 没有管理员操作，但 finalize 的 `database_now` 已越过 monitoring `effective_to`
- **THEN** Control 使用 `monitoring_ineligible` 完成 evidence-only finalize，因为该 reason 描述
  finalize 时的 eligibility 事实而非某个产品操作原因

#### Scenario: 敏感或逐账号数据进入持久化路径
- **WHEN** Driver observation 包含账号 DTO、email、endpoint、Secret 元数据或原始错误上下文
- **THEN** poll 表只保存固定枚举与聚合值；只有合法完整 Provider 的标准化 email/account key 和字段白名单进入受保护 snapshot/duplicate/lifecycle 表，其他内容不落库

### Requirement: PostgreSQL SHALL 强制 poll-run 状态与时间不变量

PostgreSQL SHALL 强制五分钟 `scheduled_at`、唯一 Node/槽、封闭状态、attempt/lease/finalized/abandoned 字段组合、Provider 唯一性、promotion 字段组合和终态不可逆。`promotion_applied=true` MUST 只属于 finalized、contract-valid、runtime、snapshot-complete Provider，并与 snapshot items/Provider 当前指针在同一受控 finalize 中形成。运行时角色 MUST 只有调度、认领、恢复、finalize、history 受控函数和只读指标所需最小权限；普通路径不得直接删除历史或绕过状态转换，history 路径只可在对应 compaction completed、snapshot items 为空且保留期满足后删除 poll run。

Poll state guards MUST make lifecycle-abandoned runs non-runnable and terminal. The existing
`execution_reason` allowlist (`lease_expired|poll_start_grace_expired|max_attempts_exhausted`)
additively gains `node_retired|node_replaced`. When a Node Retire/Replace transaction needs to
terminalize a Node's poll runs, the disposition depends on run state:

- `pending` or `retry_wait` MUST transition directly to `abandoned` with `execution_reason IN
  (node_retired,node_replaced)` and `abandoned_at` set to the database transition time, following
  the existing transition guard shape.
- `running` WITHOUT a valid current-attempt dispatch authorization (the three authorization
  columns NULL, or non-NULL but not matching the current `attempt_count`/`lease_fencing_token`)
  MUST also transition directly to `abandoned` with the same reason/timestamp rule.
- `running` WITH a valid current-attempt dispatch authorization (all three authorization columns
  non-NULL and matching the current `attempt_count`/`lease_fencing_token`) MUST NOT be
  immediately abandoned by the lifecycle transaction: Node Retire/Replace still commits (the Node
  becomes retired/replaced), but this already-authorized bounded in-flight attempt MAY finish its
  transport; no new retry, no new attempt and no new dispatch authorization MUST be granted for
  this run afterward. When transport completes, the run reaches its existing `finalized` state
  with promotion/current truth skipped by the Node lifecycle fence (see finalize requirement),
  preserving the transport evidence. If the authorized worker crashes or its lease expires before
  producing evidence, it MUST NOT obtain another authorization or a new attempt after the Node's
  retirement/replacement; the reconciler MUST terminalize this run with `execution_reason IN
  (node_retired,node_replaced)` as an `abandoned` run without any outbound resurrection.

A run that has already produced real transport evidence (any `observed_at`/provider/snapshot
data) MUST NOT be forced into `abandoned` to fake "never executed": the existing
`abandoned`-with-evidence guard continues to reject that combination, and such a run instead
reaches its existing `finalized` state with promotion skipped by the Node lifecycle fence,
preserving the transport evidence. Existing `abandoned` poll semantics remain durable; no DELETE
or in-memory error may leave a run eligible after Node retirement. Current and historical
evidence remains queryable.

#### Scenario: pending/retry_wait 或未授权 running 因 Node lifecycle 终止
- **WHEN** 一个没有 transport evidence 的 `pending`、`retry_wait`，或没有有效当前 attempt
  authorization 的 `running` poll run 因所属 Node Retire/Replace 需要终止
- **THEN** Control 原子转换为 `abandoned`，`execution_reason` 为 `node_retired` 或
  `node_replaced`，`abandoned_at` 为数据库转换时间，不删除该行，重启后不可再次认领

#### Scenario: 已授权 running 不被立即 abandon
- **WHEN** 一个 `running` poll run 具有匹配当前 `attempt_count`/`lease_fencing_token` 的有效
  dispatch authorization，此时所属 Node Retire/Replace 提交
- **THEN** Node lifecycle transaction 正常提交，该 run 不被立即转为 abandoned；已授权的 bounded
  in-flight attempt MAY 完成 transport 并进入 finalized（promotion 由 lifecycle fence 跳过），
  且此后不得为该 run 授予新的 retry/attempt/authorization

#### Scenario: 已授权 worker 崩溃且未产生 evidence
- **WHEN** 已授权的 in-flight attempt 在 Node Retire/Replace 提交后崩溃或 lease 到期，且未产生
  任何 transport evidence
- **THEN** reconciler 不得为该 run 授予新的 authorization 或新 attempt，必须以
  `execution_reason IN (node_retired,node_replaced)` 将其 abandoned，不发起任何 outbound 复活

#### Scenario: 已有 transport evidence 不得伪造成 abandoned
- **WHEN** 一个 poll run 在 Node Retire/Replace 之前已经产生 transport evidence
- **THEN** 数据库拒绝将其标记为 `abandoned`；该 run 必须走既有 finalize 路径，由 Node lifecycle
  fence 跳过 promotion，同时保留其 transport evidence

#### Scenario: 非固定槽或重复 Node/槽写入
- **WHEN** 写入未对齐五分钟的 `scheduled_at` 或第二条相同 `(instance_id, scheduled_at)`
- **THEN** 数据库拒绝非法时间，重复调度只通过幂等路径取得既有 run

#### Scenario: abandoned 伪造 Node 结果
- **WHEN** 写入尝试为 abandoned run 设置 observed/transport/contract/mode、provider rows、snapshot items 或 promotion applied
- **THEN** 数据库约束/受控 finalize 函数拒绝该状态组合

#### Scenario: finalized Provider 集不完整
- **WHEN** finalize 缺少 pinned active Provider、包含额外/重复 Provider，或 applied 标记与 snapshot/current pointer 不一致
- **THEN** finalize 失败并整体回滚，poll run 不进入 finalized

#### Scenario: 运行时尝试删除或直接改终态
- **WHEN** Control 普通运行路径直接 DELETE poll run/snapshot 或 UPDATE finalized/promotion/current pointer
- **THEN** 最小权限和状态保护拒绝操作，只有满足全部 history 前置条件的受控清理函数可删除到期历史

### Requirement: poll run SHALL 使用有界 lease、fencing 和恢复尝试

Control SHALL 只允许持有当前未过期 lease 与 fencing token 的 Worker finalize。Reconciler MUST 使用
数据库时间处理过期 running；finalized/abandoned MUST 为不可重开的终态。当 Reconciler 处理一个
lease 已过期的 `running` run 时，MUST 先锁定并重新读取该 run 所属 Node 的当前 lifecycle 状态，
再决定处置，冻结顺序为：

1. 若 Node 仍 `active`：保留既有 baseline 行为——本槽 grace 尚未结束且
   `attempt_count < max_attempts` 时复用同一 run 转为 `retry_wait`；否则转为 `abandoned`
   （既有 `execution_reason`，如 `lease_expired|poll_start_grace_expired|max_attempts_exhausted`）。
2. 若 Node 已 `retired`：转为 `abandoned`，`execution_reason=node_retired`。
3. 若 Node 是已被 Replace 的 old identity：转为 `abandoned`，`execution_reason=node_replaced`。

Node `retired`/`replaced` 两个分支 MUST NOT 进入 `retry_wait`、MUST NOT 递增 `attempt_count`、
MUST NOT 获得新 lease、MUST NOT 获得新 dispatch authorization、MUST NOT 发起新的 outbound 请求
——即使该分支下 grace 尚未结束或 attempt 尚未耗尽。该 terminal transition MUST 在同一事务原子
执行：清空 `lease_expires_at`、清空 `lease_fencing_token`，清空三个 dispatch authorization 列
（`dispatch_authorized_attempt`、`dispatch_authorized_at`、`dispatch_authorized_fencing_token`），
设置 `abandoned_at`，设置对应 `execution_reason`。`finalized`/`abandoned` 仍是不可重开的终态；
持有旧 fencing token 的迟到 finalize 写入 MUST 影响零行。

#### Scenario: 旧 Worker 在 lease 丢失后回写
- **WHEN** 旧 Worker 的 lease 已过期或 fencing 已被恢复者替换后尝试 finalize
- **THEN** fenced 更新影响零行，旧 Worker 丢弃内存结果且不能覆盖新执行或终态

#### Scenario: running 在宽限内过期
- **WHEN** Control 崩溃使 running lease 过期，但数据库时间仍在本槽 grace 内且 attempt 未耗尽，
  且重新读取确认所属 Node 仍 `active`
- **THEN** Reconciler 将原 run 转为 retry_wait，下一 Worker 最多按剩余 attempt 再执行一次固定只读
  GET

#### Scenario: running 在宽限外过期
- **WHEN** lease 过期时本槽 grace 已结束，且重新读取确认所属 Node 仍 `active`
- **THEN** Reconciler 将原 run 置为 abandoned，不再次调用 Node、不保存旧 Worker 的迟到观察

#### Scenario: retired Node + 过期 lease + grace 仍在
- **WHEN** running lease 已过期且本槽 grace 尚未结束、attempt 未耗尽，但重新读取发现所属 Node 已
  `retired`
- **THEN** Reconciler MUST NOT 转为 `retry_wait`，必须原子转为 `abandoned`，
  `execution_reason=node_retired`，同一事务清空 lease/fencing/dispatch authorization 三列

#### Scenario: replaced old Node + 过期 lease + attempt 仍有余量
- **WHEN** running lease 已过期且 attempt 未耗尽，但重新读取发现所属 Node 是已被 Replace 的 old
  identity
- **THEN** Reconciler MUST NOT 递增 `attempt_count` 或获得新 lease，必须原子转为 `abandoned`，
  `execution_reason=node_replaced`

#### Scenario: retired Node 的迟到 finalize 零真相变更
- **WHEN** 一个在 Node retired 之后仍完成 transport 的迟到 Worker 尝试 finalize
- **THEN** finalize 按既有 Node lifecycle fence 保存 transport evidence 但不产生任何 snapshot
  promotion、Provider 当前指针、账号 lifecycle 或 availability/request-quality current 真相变更

#### Scenario: 终态被再次调度或人工重试
- **WHEN** Scheduler、Worker 或非授权操作尝试离开 finalized/abandoned
- **THEN** 数据库状态约束/最小权限拒绝转换，且不产生 Node 请求

### Requirement: poll service MUST 在重启和依赖故障后安全恢复

Control MUST 在环境、Migration、Driver registry 和容量校验后启动 poll service。停止时 MUST 先停止新调度/认领并有限收尾；未确认 running 保留 lease。PostgreSQL 不可用时 MUST 有界退避且不调用 Node；恢复后 MUST 依据持久状态和数据库时间 Reconcile，不依赖内存队列。

Restart/recovery MUST re-read Node lifecycle and monitoring truth from PostgreSQL. Retired old
work is not resurrected, and a new replacement identity is never inferred by the old run.

#### Scenario: Control 在 pending、running 或 finalize 中崩溃
- **WHEN** 进程分别在 poll 创建后、Driver 调用中或 finalize 事务中崩溃并重启
- **THEN** 唯一 poll run/attempt/lease 保留，宽限内按原 run 有界恢复，宽限外 abandoned，且无重复终态或部分 provider 结果

#### Scenario: PostgreSQL 在 Node 可用时中断
- **WHEN** 数据库不可用而 Node 管理接口仍可访问
- **THEN** Control 不脱离数据库创建内存真相、不发起无可持久化归属的请求；数据库恢复后只处理仍有效槽

#### Scenario: Node 或 Secret 恢复
- **WHEN** 某槽的 Node/Secret/DNS 故障已作为 finalized 失败证据，依赖随后恢复
- **THEN** 旧槽保持不变，Control 仅在下一有效固定槽重新观察，不重写失败历史

#### Scenario: Control 或数据库停止
- **WHEN** poll service、Control 或 PostgreSQL 停止
- **THEN** 账号采集暂停，但 Gateway 与 Relay Node 已有模型请求继续，且不存在 Gateway/Node 写操作
