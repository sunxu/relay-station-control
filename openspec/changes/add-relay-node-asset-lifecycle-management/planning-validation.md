## Planning validation

本文件记录 Phase 6 Stage 2 Node lifecycle planning 的可复核边界，不代表 implementation
或 runtime acceptance 已通过。

### Independent readiness review round 1 disposition

第一轮 independent readiness review 结果：P0=0，P1=8，P2=3，Planning readiness = CHANGES
REQUIRED。本轮已针对全部 11 项修复：

| # | Finding | Disposition |
|---|---|---|
| 1 | asset-registry mutation contradiction | 写场景改为仅允许本 change 冻结的四个 Node lifecycle mutation 路径，其余路径（含未声明 Gateway action）仍拒绝；Node `DELETE` 永不存在 |
| 2 | canonical intent exact bytes | 冻结逐字节 Register/Edit/Retire/Replace 数组（均以 `1` 开头）与 `secret_triplet` 三元组，完全复用 Stage 1 SHA-256/K1 HMAC，新增 encoding fixture 校验 |
| 3 | monitoring lifecycle close reason | `cancel_reason` 收窄为仅 `node_retired|node_replaced`（`cancelled_by` admin UUID FK）；current close `end_reason` 独立 additive 扩展 `node_retired|node_replaced`，`end_actor` 仍为既有 text 列 |
| 4 | outbound race contract | 重写为 dispatch authorization 短事务提交顺序语义，不再依赖 wall-clock socket-send 时刻 |
| 5 | lifecycle-abandoned poll physical state | `account_inventory_poll_runs.execution_reason` additive 扩展 `node_retired|node_replaced`；已有 transport evidence 的 run 不得伪造为 abandoned，走既有 finalize+promotion fence 路径 |
| 6 | 收窄 durable-job scope | 移除本 change 的 `durable-job` capability delta（当前唯一注册 job kind `dingtalk_alert_delivery` 无 NodeOwned 声明）；Node-owned durable work 明确只是 `account_inventory_poll_runs` |
| 7 | floor 1→2 原子 barrier | 新增规范性 Requirement「Node lifecycle SHALL raise compatibility floor atomically before schema exposure」，冻结 12 步原子生产顺序 |
| 8 | Node lifecycle UI ownership | asset-registry「只读资产页面处理空状态与故障」新增 MODIFIED delta，明确 Register/Edit/Retire/Replace 控件归属本 change，operations 控件（Health/Connection Test/Monitoring Enable-Disable）deferred |
| 9 | Node list cursor generation | 冻结显式 `node_registry_generation` 单调 token，绑定 encoding_version/environment/filters/last_instance_id，mismatch 返回 `409 cursor_stale` |
| 10 | current_identity_conflict | 从错误 taxonomy 中移除（Node 无 Gateway 式 singleton current identity invariant，无消费者），identity 冲突统一 `duplicate_identity` |
| 11 | NodeAsset success projection | Register/Edit/Retire/Replace 响应与 receipt 固定返回完整 Node 投影（含 node_type/driver_contract_version/capabilities/monitoring），retired asset 的 monitoring 显式 `current:false` |

### Independent readiness review round 2 disposition

第二轮 independent readiness review 结果：P0=0，P1=4，P2=2，Planning readiness = CHANGES
REQUIRED。本轮已针对全部 6 项修复：

| # | Finding | Disposition |
|---|---|---|
| 1 | Stage1+Stage2 overlapping MODIFIED 合成 | `asset-registry` 的三个 Requirement（数据库保存单环境资产关系/管理员可通过受保护只读API查看资产/只读资产页面处理空状态与故障）与 `relay-node-gateway-account-binding` 的三个 Requirement（temporal interval/原子并发binding/Account与Node生命周期边界）已重写为 `baseline + Stage1 Gateway delta + Stage2 Node delta` 的完整合成文本，保留全部 Gateway lifecycle 语义（0..1 current active/0..N retired、Register/Edit/Retire/Replace、history routes、lifecycle UI、gateway_retired/gateway_replaced、active/current bind 校验、Gateway lifecycle race）并新增 Node 语义；新增 semantic composition check（见下）与 tasks.md final archive-readiness task |
| 2 | Node-coupled writers 校验 active lifecycle | asset-registry「Node 账号监控状态源自显式激活区间」新增 monitoring writer 先锁 Node 验证 `lifecycle_status=active` 再锁 monitoring rows，新增 Retire-vs-monitor-writer race 场景；`relay-node-gateway-account-binding`「原子处理并发binding写入」冻结 Bind/Rebind 锁顺序 Node(active)→Gateway(active)→Directory→binding，新增 Retire-vs-Bind race 场景（两个方向） |
| 3 | dispatch authorization 物理表示 | 冻结 `account_inventory_poll_runs` 新增列 `dispatch_authorized_attempt/_at/_fencing_token`，CHECK 三列同时 NULL/非NULL、attempt 匹配、仅 pending/running 可写一次；明确 claim≠authorization、每次 retry 需独立授权、Retire-vs-authorization 双向 race、restart/reconciler 按三列区分 authorized/非authorized attempt；新增 migration/CHECK/PG18 task（16a） |
| 4 | node_registry_generation 物理真相 | 冻结 `asset_registry_generations` 单例表（`singleton_id=1`,`node_generation bigint`），Node lifecycle mutation 与 monitoring current-state 变化在同事务内锁定单例行并单调递增，overflow fail closed、不可下降；List 首页在一致 DB snapshot 内绑定 generation；新增 migration task（16b） |
| 5 | UI normative wording | asset-registry「只读资产页面处理空状态与故障」的 Node lifecycle 控件由 `MAY 显示` 改为 `MUST 提供`，仍不含 Health/Connection Test/Monitoring Enable-Disable |
| 6 | canonical v1 immutability wording | 「Node canonical intent SHALL reuse shared v1 encoding exactly」body 与场景改为：已发布 `command_kind` 的 v1 数组不可变；新 `command_kind` 可定义自己的 shared-v1 数组；修改既有 `command_kind` 字段集/语义需要显式评审新 encoding version 或经证明的向后兼容机制，不允许原地扩展 |

### Independent readiness review round 3 disposition

第三轮 independent readiness review 结果：P0=0，P1=4，P2=1，Planning readiness = CHANGES
REQUIRED。本轮已针对全部 6 项修复：

| # | Finding | Disposition |
|---|---|---|
| 1 | dispatch authorization per-attempt current marker | 修正为真实字段名 `attempt_count`/`lease_fencing_token`（不存在 `attempt` 列）；冻结生命周期：`running` 且三列 NULL 时一次性写入并绑定当前 `attempt_count`/`lease_fencing_token`；同一 attempt 内第二次写入被拒绝；合法 `running→retry_wait` 原子清空三列；`retry_wait→running`（新 attempt_count、新 fencing token）三列须为 NULL 并独立重新授权；`abandoned/finalized` 后不可再写 |
| 2 | Retire/Replace 对 authorized running poll 的处理 | 冻结按 run 状态区分：`pending`/`retry_wait`/无有效 current-attempt authorization 的 `running` 立即 abandoned；持有效 current-attempt authorization 的 `running` 不立即 abandon——lifecycle transaction 正常提交，authorized attempt MAY 完成 transport 并 finalize（promotion 跳过），此后不授予新 retry/attempt/authorization；authorized worker 崩溃/lease 到期且无 evidence 时 reconciler 必须终态为 abandoned，不复活 outbound |
| 3 | Node list cursor `read_as_of` | 新增 cursor field `read_as_of`（首页事务的 DB transaction timestamp），与 `node_generation` 一起在同一致快照读取并绑定进 cursor；cursor chain 内后续页的 monitoring-derived projection/filter 必须按 `read_as_of` 计算，不得每页读取新的 current DB time；纯时间流逝跨越 `effective_from`/`effective_to` boundary 不改变已发 cursor chain 内的 membership，只有 `node_generation` 前进才触发 `409 cursor_stale`；新增跨 boundary 分页测试任务 |
| 4 | tasks.md implementation closure 措辞 | 最终 implementation task（61）由「production-code unchanged/不得 apply」改为 archive-readiness 措辞：approved-scope reconciliation、无 out-of-scope production 变更、生成物可复现、tests/evidence 完整、durable truth reconciliation、authorized commits/worktree reconciliation、Runtime Acceptance evidence 就绪、archive readiness awaiting review；本文件当前状态行（`production code changed=false`/`apply=NOT RUN`）保持不变，因为这是 planning 阶段的准确描述，与未来 implementation 阶段措辞分离 |
| 5 | patch terminology | `absent/null/set` 改为 Stage 1 `absent/clear/set` 词汇；明确 `display_name_patch`/`management_endpoint_patch` 只支持 `absent\|set`（`clear` 对这两个字段无效），仅 `reader_secret_ref`（`secret_triplet`）支持完整 `absent\|clear\|set` 三态；未改变已冻结的 canonical v1 字节数组结构 |
| 6 | Validation | 见下方 Validation status 小节 |



对 `asset-registry` 与 `relay-node-gateway-account-binding` 中同时被
`add-gateway-asset-lifecycle-management`（Stage 1）与本 change（Stage 2）MODIFIED 的
Requirement，已逐个人工核对：本 change 的 spec delta 文本 = baseline 原文 + Stage 1 已批准
delta 的全部语义（Gateway 0..1 current active/0..N retired、Gateway
Register/Edit/Retire/Replace、Gateway history routes/lifecycle UI、
gateway_retired/gateway_replaced、Gateway active/current bind 校验、Gateway lifecycle race
锁顺序）+ Stage 2 Node 语义（Node lifecycle、Node lifecycle mutation 路径、
node_registry_generation、Node UI ownership、node_retired/node_replaced、Node lock order）。
未发现 Stage 2 重新禁止或收窄 Gateway lifecycle mutation/UI 的情况。

Dependency/archive reconciliation 顺序冻结为：Gateway Stage 1 truth first ->
Node Stage 2 truth second（`add-gateway-asset-lifecycle-management` 先 apply/archive，
`add-relay-node-asset-lifecycle-management` 后 apply/archive）；本轮未执行 apply，仅确认
该顺序作为未来 archive 的规划前提。

结果：`PASS`（不只与 `openspec/specs/**` old baseline 比较 heading，而是逐个 Requirement
比较合成后的完整语义边界）。

### Independent readiness review round 4 disposition

第四轮 independent readiness review 结果：P0=0，P1=3，Planning readiness = CHANGES REQUIRED。
本轮已针对全部 3 项修复：

| # | Finding | Disposition |
|---|---|---|
| 1 | Node lifecycle promotion skip reason | 复用既有两层 `promotion_skipped_reason`（`account_inventory_poll_runs`：`NULL\|policy_changed` additive 扩展 `node_retired\|node_replaced`；`account_inventory_poll_provider_results`：既有 `policy_changed\|transport_failed\|contract_invalid\|disk_fallback\|provider_identity_incomplete\|provider_duplicate\|stale_poll` additive 扩展同两值），未新增字段；冻结 precedence：Node lifecycle fence（node_retired/node_replaced）> policy_changed > 既有 provider-specific evaluation；finalize 时 Node 已 retired/replaced，run 与全部 pinned active Provider 行统一设为该 reason、`promotion_applied=false`，保留 evidence，零 snapshot/current/lifecycle/availability/request-quality 真相变更 |
| 2 | dispatch authorization 重新验证 lease + grace | 授权事务读取单一 `database_now` 后同时校验 `status=running`、三列 NULL、`attempt_count`/`lease_fencing_token` 匹配、`lease_expires_at > database_now`、`scheduled_at+poll_start_grace_seconds > database_now`、Node active、monitoring eligibility；任一失败零授权零 outbound；成功后返回 `lease_remaining`/`grace_remaining`；Worker transport deadline 冻结为 `min(lease_expires_at, scheduled_at+poll_start_grace_seconds, database_now+T)`（`T`=既有 account-inventory-poll-capacity 最坏请求时长，未新增第二套 timeout 配置或新 durable 列）；新增 3 个测试场景（lease 过期 token 未变、grace 过期 lease 仍在、deadline 上限约束） |
| 3 | MODIFIED baseline lease/recovery Requirement | 新增 exact MODIFIED heading `poll run SHALL 使用有界 lease、fencing 和恢复尝试`（复用 baseline 标题与全部 4 个既有场景），扩展 Reconciler 处理过期 running 的顺序：先重读 Node lifecycle，active 时保留 baseline retry_wait/abandoned，retired 时 abandoned(node_retired)，Replace 的 old identity 时 abandoned(node_replaced)；retired/replaced 分支禁止 retry_wait/新 attempt/新 lease/新 authorization/新 outbound；terminal transition 同一事务清空 lease_expires_at/lease_fencing_token/三个 dispatch authorization 列并设置 abandoned_at/execution_reason；新增 5 个场景 |

未新增无关表、framework、scheduler 或 capability；`promotion_skipped_reason`、
`dispatch_authorized_*`、`lease_expires_at`/`lease_fencing_token`、
`poll_start_grace_seconds` 均为既有列，仅 additive 扩展 CHECK allowlist 与 Requirement 文本。

### Independent readiness review round 5 disposition

第五轮 independent readiness review 结果：P0=0，P1=1，Planning readiness = CHANGES REQUIRED。
本轮修复 1 项：

| # | Finding | Disposition |
|---|---|---|
| 1 | Provider health consumer 未把 node_retired/node_replaced 视为 lifecycle-ineligible promotion evidence | 在 `Provider 当前快照状态 SHALL 独立且可在历史清理后解释` Requirement 中补充：`control_refresh_account_inventory_provider_health_v1()`（及其它等价 current-health consumer）MUST 把 `promotion_skipped_reason IN (policy_changed, stale_poll, node_retired, node_replaced)` 一视同仁地排除在 health 刷新之外——命中时不得刷新 `health_scheduled_at`/`health_degraded`/`health_reason`，也不得间接推进 availability/request-quality current truth；transport/provider evidence 与 history/compaction 证据完整保留，不删除/不重解释；`transport_failed`/`contract_invalid`/`disk_fallback`/`provider_identity_incomplete`/`provider_duplicate` 等既有 provider-specific 原因不受影响。扩展既有场景 `旧策略或迟到结果不得刷新health`，新增 3 个场景（node_retired 不刷新、node_replaced 不刷新、普通 provider-specific 原因不受影响）。design.md §8.1 新增触发器函数行为说明，§13 新增 migration 步骤 6b（仅更新既有触发器函数体，不新增列/触发器）。tasks.md 新增任务 17b，覆盖 4 类测试：finalized+node_retired、finalized+node_replaced、普通 transport_failed/contract_invalid 回归、policy_changed/stale_poll 既有行为不变。 |

未新增字段/表/触发器；仅 additive 扩展既有触发器函数
`control_refresh_account_inventory_provider_health_v1()` 内的判断条件。

### Independent readiness review — FINAL

最终 independent readiness review 结论由正式审查确认，本次仅记录 documentation hardening，
不修改 spec/design contract，也不授权 implementation。

- P0 = 0
- P1 = 0
- Systemic MODIFIED baseline-preservation audit = PASS
- Planning readiness = PASS / READY
- Implementation readiness = READY

OpenSpec MODIFIED Requirement replaces the whole Requirement。因此每个 MODIFIED Requirement
必须满足以下完整合成原则：

```text
baseline normative body
+ baseline scenarios
+ approved prior active-change semantics where applicable
+ Stage 2 Node lifecycle delta
= complete resulting Requirement
```

不得使用 “existing baseline behavior remains intact” 来代替被 MODIFIED 替换掉的 baseline
normative body。No MODIFIED Requirement relies solely on a generic "baseline remains intact"
sentence to preserve omitted normative text.

本次最终审查已逐项检查全部 Stage 2 MODIFIED Requirements，覆盖如下：

| Capability | Result | Baseline-preservation audit evidence |
|---|---|---|
| asset-registry | PASS | baseline + approved Gateway Stage1 delta + Stage2 delta 完整合成 |
| relay-node-gateway-account-binding | PASS | baseline + approved Gateway Stage1 delta + Stage2 delta 完整合成 |
| account-inventory-poll-run | PASS | 所有 MODIFIED Requirement 保留 baseline normative body/scenarios，再追加 Node lifecycle / dispatch authorization / recovery / promotion fence |
| account-inventory-snapshot | PASS | baseline policy locking / completeness / promotion / atomic finalize / Provider state contract 保留，再追加 Node lifecycle fence |
| account-inventory-lifecycle | PASS | baseline promotion eligibility / atomicity / readonly isolation 保留，再追加 active Node / monitoring eligibility fence |
| account-request-quality | PASS | baseline 15m/1h windows、DB UTC、request/success/failure counts、success_rate、p95_latency_ms=percentile_cont(0.95)、last success/failure fields、History membership semantics 保留，再追加 Node lifecycle eligibility |
| antigravity-account-availability | PASS | baseline six states、AVAILABLE/UNKNOWN/DISABLED semantics、no duration/repetition escalation、business UNKNOWN vs read Unavailable separation、security/read contracts、occurrence/current projection semantics 保留，再追加 Node lifecycle / monitoring eligibility |

- Stage1 + Stage2 overlapping semantic composition = PASS
- MODIFIED heading exact-title comparison = PASS

### Baseline and dependency

- Ops Architecture Review：`5add9cb54f0a488cc547b2ba72287c8f492a32f7`，PASS
- Frozen Requirements：`8c7cdbcf5ea3480da16d51408581a4be3e72c994`
- Control Phase 6 Stage 1 planning / Gateway shared foundation：`14c0e80`
- reviewed pre-Phase6 Control：`5e2caeb031a47510744cada56978b963da39b4e9`
- Gateway baseline：`6b045698e6e5e62e35dbd103abf20c1407f8a0bb`
- CLIProxyAPI baseline：`273d624c70f6eb8bdd7b049df396c306acd3f8d0`
- static resource prefix change：Planning readiness PASS/READY，未修改
- `add-gateway-asset-lifecycle-management`：Planning readiness PASS/READY，未修改
- `add-relay-node-management-operations`：Detailed planning DEFERRED，未修改

### Scope and source scan

本轮只修改 `openspec/changes/add-relay-node-asset-lifecycle-management/**`。已实际读取
`openspec/specs/**`、`migrations/00003/00005/00011`、`migrations/00031_dingtalk_alert_delivery.sql`
（确认当前唯一注册 async job kind 无 NodeOwned 声明）、Inventory poll-run/snapshot/lifecycle
migrations、`queries/assets.sql`、`queries/account_inventory_poll_runs.sql`、
`queries/relay_node_gateway_account_bindings.sql`、现有 Node API/OpenAPI 路由和 Gateway
Stage 1 artifacts。确认真实 Node 字段为 instance_id/display_name/node_type/
driver_contract_version/management_endpoint/reader_secret_ref/capabilities；没有凭空引入
其他 Node identity 字段。

### P2-3 final disposition

| Finding | Status | Frozen planning decision |
|---|---|---|
| Shared receipt/intent/revision | RESOLVED/shared | 复用 Stage 1 receipt、global command_id、advisory lock、actor-first replay、canonical intent（逐字节冻结）、K1/HMAC、revision |
| Node lifecycle persistence | RESOLVED | active→retired terminal、revision、retirement actor/reason/checks、禁止 DELETE/reuse |
| Replacement lineage | RESOLVED | dedicated immutable relation，UNIQUE predecessor/successor，A→B→C only |
| Monitoring cancellation | RESOLVED/P2-3 | future rows durable cancel（`cancel_reason` 仅 node_retired/node_replaced），empty `tstzrange`，current 用 effective_to close（`end_reason` 独立扩展）；operations API 仍 deferred |
| Lock order | RESOLVED | Node→monitoring stable order→binding；Node lifecycle 不再拿 Gateway/Directory |
| Inventory/poll-run fencing | RESOLVED | scheduler/claim/dispatch-authorization/promotion active fences；`account_inventory_poll_runs` fixed terminal abandonment；generic `async_jobs` 不受影响 |
| Compatibility | RESOLVED | class0/pre-Phase6、class1/Gateway-aware、class2/Node-aware；floor 1→2 原子 barrier 现为规范性 Requirement |

### Compatibility decision

class 1 不能安全理解本 change 的 retired historical Node、monitoring cancellation metadata、
cancelled future empty range、replacement lineage、Node binding end reasons 和 Inventory
lifecycle fences。继续运行 class 1 会 poll retired Node、将 cancelled schedule 当 eligible、
重试 old durable work 或错误推进 current truth。因此本 change MUST 推进
`compatibility_class=2` 与 `phase6_evidence_floor/runtime floor=2`，且不允许出现
class1-incompatible schema 已提交但 floor 仍为 1 的中间态（见
`relay-node-asset-lifecycle` capability 的原子 barrier Requirement）。class 1 rollback 在
floor 2 失败关闭；不编造未来 class 2 artifact digest。

### MODIFIED heading validation

Change deltas use exact baseline Requirement titles from:
`asset-registry`（数据库保存单环境资产关系；Node 账号监控状态源自显式激活区间；
管理员可通过受保护只读 API 查看资产；只读资产页面处理空状态与故障）、
`relay-node-gateway-account-binding`（Control SHALL
以 temporal interval 保存 current binding 与完整历史；Control SHALL 原子处理并发 binding
写入；Control SHALL 保留 Account与Node生命周期边界）、`account-inventory-poll-run`
（Control SHALL 只为符合资格的 Node 创建唯一 UTC 固定槽；Worker SHALL 先取得并发额度再
认领并立即调用 Driver；poll run SHALL 使用有界 lease、fencing 和恢复尝试；finalize MUST
原子保存固定策略的完整聚合结果；PostgreSQL SHALL 强制 poll-run 状态与时间不变量；
poll service MUST 在重启和依赖故障后安全恢复）、
`account-inventory-snapshot`（finalize MUST 锁定当前策略并区分 completeness 与 promotion；
完整 active Provider SHALL 独立推进当前快照；snapshot promotion SHALL 与 poll finalize
原子且受 fencing 保护；Provider 当前快照状态 SHALL 独立且可在历史清理后解释）、
`account-inventory-lifecycle`（只有 promotion-applied runtime Provider SHALL 推进生命周期；
lifecycle promotion MUST 原子、fenced、幂等且单调；lifecycle产品读取 MUST 保持只读与数据面隔离）、
`account-request-quality`（单账号窗口质量查询；Account Request History SHALL 受当前Inventory
membership约束）和 `antigravity-account-availability`（Availability SHALL use exactly six
evidence states；Availability reads SHALL preserve security and existing truths；Current
projection SHALL remain separate from occurrence lifecycle）。`durable-job` capability 本轮
已移除，不再是本 change 的 MODIFIED capability（无 Node-owned async job kind）。

本轮新增 MODIFIED Requirement `poll run SHALL 使用有界 lease、fencing 和恢复尝试`（重用
`account-inventory-poll-run` baseline 的 exact 标题），已核对与 baseline `openspec/specs/
account-inventory-poll-run/spec.md` 中同名 Requirement 的标题逐字符相同，且该 delta 完整保留
baseline 全部四个既有场景（旧 Worker 在 lease 丢失后回写；running 在宽限内过期；running 在宽限外
过期；终态被再次调度或人工重试）并新增五个 Node lifecycle 场景。

自动 heading comparison 结果：`PASS`。逐个比较每个 MODIFIED delta 与
`openspec/specs/**` 的 exact Requirement title set，未发现新增、缺失或相似标题替代。

### Validation status

- `openspec validate add-relay-node-asset-lifecycle-management --strict`：PASS
- `openspec validate add-gateway-asset-lifecycle-management --strict`：PASS（只读回归）
- `openspec validate add-relay-node-management-operations --strict`：PASS（只读回归）
- `openspec validate fix-control-web-static-resource-prefix --strict`：PASS（只读回归）
- `openspec validate --all --strict`：PASS（27 passed / 0 failed）
- Stage1 + Stage2 overlapping MODIFIED semantic composition check：PASS
- `git diff --check`：PASS
- `git status --short`：仅 `openspec/changes/add-relay-node-asset-lifecycle-management/**` 有变更
- production code changed：`false`（目标外 migrations/api/queries/internal/cmd/web/deploy/generated 均未修改）
- `openspec apply`：`NOT RUN`
- Implementation：`NOT STARTED`
- Runtime Acceptance：`NOT STARTED`
- completed implementation tasks：`0`（tasks.md 现为 61 项主任务 + 6 项细分子任务
  17a/17b/32a/36a/36b/42a，共 67 项，全部 ≤2h）

### Readiness gate

Independent readiness review = PASS
Planning readiness = PASS / READY
Implementation readiness = READY

openspec apply = NOT AUTHORIZED / NOT RUN
Implementation = NOT STARTED
Runtime Acceptance = NOT STARTED
completed implementation tasks = 0

最终 independent readiness review 已通过；READY 不等于 apply 授权。
本轮仅 documentation hardening，不执行 `openspec apply`，不修改 production code，不 commit/push。
