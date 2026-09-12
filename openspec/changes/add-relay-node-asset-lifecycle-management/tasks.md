## Apply Gate

以下均为未来 implementation tasks；本 planning-only change 不授权 `openspec apply`。
每项预计不超过 2 小时，必须按依赖顺序执行并留下可审计 evidence。

## Shared foundation and compatibility

- [ ] 1. 验证并接入 Gateway Stage 1 `asset_admin_command_receipts`、全局 command_id、
  actor-first replay、canonical intent、K1 HMAC、advisory lock、revision helper（≤2h）。
- [ ] 2. 为 Node action 注册 shared canonical intent fixtures，覆盖 Register/Edit/Retire/
  Replace 的 field order、patch presence、decimal revision 和 Secret fingerprint（≤2h）。
- [ ] 3. 实现/验证 class 2 signed manifest、`relay-control-compat-gate` 和 mandatory
  production wrapper 的 Node evidence path（≤2h）。
- [ ] 4. 实现 compatibility floor 1→2 单调更新与 class1 rollback fail-closed acceptance
  （≤2h）。

## PostgreSQL schema and migration

- [ ] 5. 在 PG18 隔离库 proof `relay_node_assets` lifecycle columns、backfill active/revision=1
  和 NOT VALID→VALIDATE checks（≤2h）。
- [ ] 6. 实现 Node retired shape、revision overflow、retired actor FK RESTRICT 和
  DELETE/identity/lifecycle mutation guard（≤2h）。
- [ ] 7. 迁移并冻结 Node mutable/immutable field privileges，禁止 capability ownership
  被 lifecycle path 改写（≤2h）。
- [ ] 8. 创建 `relay_node_asset_replacements`、FK/index/UNIQUE/CHECK 和 append-only guard
  （≤2h）。
- [ ] 9. 补 lineage fork/merge/cycle/identity reuse 数据库与 transaction tests（≤2h）。
- [ ] 10. 增加 monitoring `cancelled_at/cancelled_by/cancel_reason`、FK、reason/shape
  checks 与 privilege boundary（≤2h）。
- [ ] 11. 扩展 monitoring history guard：future NULL→non-NULL 一次、禁止 uncancel/修改
  schedule/cancel current-past/DELETE/TRUNCATE（≤2h）。
- [ ] 12. 用真实 PostgreSQL 18 验证 generated stored `active_range` expression alteration
  是否允许；记录可执行 proof 和失败 rollback behavior（≤2h）。
- [ ] 13. 按“drop dependent GiST exclusion（如需要）→change generated expression→recreate
  exclusion→validate”实现 additive active-range migration proof，确认旧 rows 保留（≤2h）。
- [ ] 14. 以 `'empty'::tstzrange` 验证 GiST overlap、current eligibility、expected-slot
  exclusion 和 cancelled row reach-effective-from 行为（≤2h）。
- [ ] 15. 扩展 binding end reason CHECK/guard 为 `node_retired|node_replaced`，验证
  immutable history 与 actor FK（≤2h）。
- [ ] 16. 更新 compatibility marker/floor migration，证明旧 runtime 在 floor2 下不能启动
  （≤2h）。
- [ ] 17. 在 `account_inventory_poll_runs` 增加 `dispatch_authorized_attempt`、
  `dispatch_authorized_at`、`dispatch_authorized_fencing_token` 列与 CHECK/触发器 guard（三列
  必须同时 NULL 或同时非 NULL，且非 NULL 当且仅当 `status='running' AND
  dispatch_authorized_attempt=attempt_count AND
  dispatch_authorized_fencing_token=lease_fencing_token`；`pending/retry_wait/finalized/
  abandoned` 四态下三列必须全为 NULL；同一 `attempt_count` 只允许一次 `NULL→authorized` 写入，
  禁止覆盖；`running→retry_wait`、`running→finalized`、`running→abandoned` 三种转换必须各自
  原子清空三列），并验证 PG18 migration/constraint（≤2h）。
- [ ] 17a. 扩展既有两层 promotion-skip CHECK allowlist（不新增字段）：
  `account_inventory_poll_runs_promotion_reason_fixed` 由 `NULL|policy_changed` additive 扩展为
  `NULL|policy_changed|node_retired|node_replaced`；
  `account_inventory_poll_provider_promotion_reason_fixed` 在既有 `policy_changed|
  transport_failed|contract_invalid|disk_fallback|provider_identity_incomplete|
  provider_duplicate|stale_poll` 基础上 additive 扩展 `node_retired|node_replaced`；验证既有
  `promotion_terminal`/`promotion_shape` guard 不变，并验证 PG18 migration（≤2h）。
- [ ] 17b. 更新 `control_refresh_account_inventory_provider_health_v1()` 触发器函数
  （及其它等价 `account_inventory_provider_states` current-health consumer/guard/test），
  使 run 级排除条件从 `promotion_skipped_reason IS DISTINCT FROM 'policy_changed'` 扩展为
  同时排除 `node_retired`/`node_replaced`，provider 级排除条件从
  `policy_changed`/`stale_poll` 扩展为同时排除 `node_retired`/`node_replaced`；
  不新增列/触发器，不改变函数签名/挂载点/owner。测试至少覆盖：
  finalized+node_retired → `health_scheduled_at`/`health_degraded`/`health_reason` 不变；
  finalized+node_replaced → 同上不变；普通 `transport_failed`/`contract_invalid` →
  保持既有 health refresh 语义不受影响；`policy_changed`/`stale_poll` → 既有行为不变（≤2h）。
- [ ] 18. 创建 `asset_registry_generations` 单例表（`singleton_id=1`、
  `node_generation bigint NOT NULL DEFAULT 0`），验证单调递增、overflow fail closed 与
  Node/monitoring 事务内锁顺序（≤2h）。

## Durable command and lifecycle transactions

- [ ] 19. 实现 Node Register transaction：new identity absent、capability validation、
  revision=1、audit/receipt atomicity（≤2h）。
- [ ] 20. 实现 Node Edit transaction：只允许 display/endpoint/opaque secret patch、
  expected_revision 和 revision overflow（≤2h）。
- [ ] 21. 实现 Node Retire 的锁顺序与 single DB boundary，覆盖 current close/future
  cancellation、binding close、`account_inventory_poll_runs` abandonment、audit/receipt
  （≤2h）。
- [ ] 22. 实现 Node Replace 的锁顺序与 single replacement boundary，覆盖 new identity、
  old/new lifecycle、lineage 和 no-inheritance（≤2h）。
- [ ] 23. 实现 Retire/Replace replay、actor mismatch、intent mismatch、stale revision、
  duplicate identity 和 commit-unknown response recovery（≤2h）。
- [ ] 24. 验证所有 lifecycle timestamps 使用 DB time，poll run transport evidence 不被覆盖并
  记录 abandonment transition time 原因（≤2h）。

## Monitoring and binding

- [ ] 25. 更新所有 monitoring writers 先锁 Node 再按 `effective_from,activation_id`
  稳定锁 rows，增加 lock-order regression test（≤2h）。
- [ ] 26. 实现 current interval boundary close 的合法 edge handling，禁止 retrospective
  UTC history rewrite（≤2h）。
- [ ] 27. 实现 future monitoring cancellation 的 durable query/read model 与 counts（≤2h）。
- [ ] 28. 实现 Node lifecycle binding close `node_retired/node_replaced`，验证不拿
  Gateway/Directory lock 的 inversion guard（≤2h）。
- [ ] 29. 覆盖 binding-vs-Retire、binding-vs-Replace、monitor-writer-vs-Retire/Replace
  races 与 partial unique constraints（≤2h）。

## Inventory and Node-owned durable work

- [ ] 30. 更新 Inventory scheduler eligibility：active Node、non-cancelled monitoring、
  capability、policy，禁止 retired poll（≤2h）。
- [ ] 31. 更新 claim/preflight fence，处理 pending/retry_wait retired run 为 durable
  abandoned，确保 restart 不重开（≤2h）。
- [ ] 32. 更新 outbound pre-dispatch fence 为 dispatch authorization 短事务：读取单一
  `database_now` 后同时验证 `status='running'`、三列为 NULL、`attempt_count`/
  `lease_fencing_token` 匹配当前值、`lease_expires_at > database_now`、`scheduled_at +
  poll_start_grace_seconds > database_now`、Node active 与当前 monitoring eligibility，任一
  不满足则不写入授权列、不发 outbound；成功后写入
  `dispatch_authorized_attempt/_at/_fencing_token` 绑定当前 `attempt_count`/
  `lease_fencing_token`，返回 `lease_remaining`/`grace_remaining`，Worker transport deadline
  不晚于 `min(lease_expires_at, scheduled_at+poll_start_grace_seconds, database_now+T)`
  （`T` 为既有 account-inventory-poll-capacity 最坏请求时长，不新增第二套 timeout 配置或新
  durable deadline 列）；证明 claim≠authorization、同一 attempt 内只写一次、
  `running→retry_wait`/`running→finalized`/`running→abandoned` 均原子清空三列、下一 attempt
  需独立重新授权、不跨 HTTP 长持 Node DB lock，且 race 由 authorization 事务提交顺序而非
  wall-clock 决定；restart/reconciler 按这三列是否匹配当前
  `attempt_count`/`lease_fencing_token` 区分 authorized/非 authorized attempt（≤2h）。
- [ ] 32a. 补 dispatch authorization 前置校验测试：lease 已过期但 fencing token 未变 →
  authorization 拒绝、零 HTTP；grace 已过期但 lease 仍有效 → authorization 拒绝、零 HTTP；
  authorization 临近 deadline → outbound context deadline 被
  `min(lease/grace/request-timeout)` 正确约束（≤2h）。
- [ ] 33. 更新 finalize/promotion fence，证明 retired/replaced 时 run 级与 Provider 级
  `promotion_skipped_reason` 均设为 `node_retired`/`node_replaced`（precedence：Node
  lifecycle fence 先于 `policy_changed`，`policy_changed` 先于既有 provider-specific
  evaluation），不更新 snapshot/current truth/account lifecycle/availability/
  request-quality（≤2h）。
- [ ] 34. 验证 old identity 不会被隐式 retarget 到 replacement identity（≤2h）。
- [ ] 35. 为 `account_inventory_poll_runs` 增加 `execution_reason IN
  (node_retired,node_replaced)`，覆盖 `pending`/`retry_wait` 与无有效 current-attempt
  authorization 的 `running`；不修改 generic `async_jobs`/`durable-job` capability（≤2h）。
- [ ] 36. 验证已有 transport evidence 的 poll run 不被伪造成 abandoned，而是走既有 finalize
  路径并由 promotion fence 跳过 current truth；runnable index/retry/reconciler 不会复活旧
  work（≤2h）。
- [ ] 36a. 验证持有效 current-attempt authorization 的 `running` poll run 在所属 Node
  Retire/Replace 提交时不被立即 abandoned：lifecycle transaction 正常提交，authorized attempt
  MAY 完成 transport 并 finalize（promotion 跳过），之后不得为其授予新
  retry/attempt/authorization；若该 attempt 崩溃或 lease 到期且无 evidence，reconciler 必须
  将其 abandoned 为 `node_retired`/`node_replaced`，不复活 outbound（≤2h）。
- [ ] 36b. 实现/验证 reconciler 对 lease 已过期的 `running` run 的处置顺序：先重新读取所属 Node
  当前 lifecycle 状态，active 时保留 baseline `retry_wait`/`abandoned` 行为，`retired` 时转
  `abandoned`(`node_retired`)，Replace 的 old identity 时转 `abandoned`(`node_replaced`)；
  `retired`/`replaced` 分支不得进入 retry_wait、不得递增 attempt_count、不得获得新
  lease/authorization/outbound，terminal transition 同一事务清空
  `lease_expires_at`/`lease_fencing_token`/三个 dispatch authorization 列并设置
  `abandoned_at`/`execution_reason`；覆盖 active+grace 仍在、active+grace 已过、
  retired+grace 仍在、replaced+attempt 仍有余量、retired Node 迟到 finalize 零真相变更 5 个
  测试场景（≤2h）。

## API, read model and generated artifacts

- [ ] 37. 更新 OpenAPI Register/Edit/Retire/Replace exact method/path/body/status/error
  schemas，锁定 revision decimal string 和 Secret redaction（≤2h）。
- [ ] 38. 更新 Node handlers 的 session/super_admin/CSRF/no-store/security-first error
  mapping 和 bounded receipt replay（≤2h）。
- [ ] 39. 更新 Node list/detail query，读取 `asset_registry_generations.node_generation`
  单例表（随 Node lifecycle mutation 和 monitoring current-state 变化在同事务内单调递增，
  非进程内存计数）与首页事务的 `read_as_of`（DB transaction timestamp），首页在一致 DB
  snapshot 内绑定两者，并把 cursor filter/generation/read_as_of binding、
  predecessor/successor 和 retired metadata 落地（≤2h）。
- [ ] 40. 更新 `GetAssetCounts` 保持 legacy `nodes=total`，新增 `node_counts` active/
  retired/total（≤2h）。
- [ ] 41. 运行 `make generate` 生成 Go/TypeScript clients，验证 generated files 可重现，
  不手工编辑 generated code（≤2h）。
- [ ] 42. 补 read API pagination `generation` stale（`409 cursor_stale`）、filter
  mismatch、default active 和 historical detail integration tests（≤2h）。
- [ ] 42a. 补跨 `effective_from`/`effective_to` boundary 的分页测试：验证已发 cursor chain
  在纯 wall-clock 时间流逝跨越 boundary 且无 `node_generation` 变化时，后续页 membership 按
  `read_as_of` 保持与首页一致，不返回 `409 cursor_stale`；同时验证真实 `node_generation`
  推进时后续页仍正确返回 `409 cursor_stale`（≤2h）。

## UI ownership

- [ ] 43. 实现 Node Register/Edit 表单控件，仅调用本 change 冻结的 mutation 路径，
  `secret` 输入只提示 `secret_configured`、不回显引用（≤2h）。
- [ ] 44. 实现 Node Retire 确认与 Replace 流程控件，覆盖 `expected_revision` 冲突时的刷新
  提示，不静默重试（≤2h）。
- [ ] 45. 实现 retired Node 历史详情页，展示 predecessor/successor 导航，不提供任何使其
  复活为 current 的操作（≤2h）。
- [ ] 46. 验证 UI 不显示 Health、Connection Test、Monitoring Enable/Disable 控件（仍属于
  `add-relay-node-management-operations`）（≤2h）。

## Audit, metrics, security and operational reads

- [ ] 47. 扩展 `asset_node` audit category/action/check allowlist，验证成功 transition
  exactly one、replay zero second audit（≤2h）。
- [ ] 48. 接入 `control_asset_mutation_total` Node bounded labels/result taxonomy，验证
  Node ID/secret/endpoint/actor 不出现在 labels（≤2h）。
- [ ] 49. 更新 Inventory current、availability、request-quality reads 显式 active+
  monitoring eligibility fence，保留历史 evidence/rollup（≤2h）。
- [ ] 50. 验证 authentication、CSRF、no-store、secret/credential/raw response negative
  cases 及不新增 arbitrary HTTP client（≤2h）。

## Race, crash and compatibility acceptance

- [ ] 51. 补 concurrent Register same identity、concurrent Retire、concurrent Replace、
  Retire-vs-Replace、Edit-vs-Retire tests（≤2h）。
- [ ] 52. 补 monitoring cancellation reaches effective_from、restart-after-cancellation、
  future expected-slot and GiST non-overlap tests（≤2h）。
- [ ] 53. 补 Inventory claim/outbound/promotion-vs-Retire、old worker-after-Replace 和
  `account_inventory_poll_runs` restart tests（≤2h）。
- [ ] 54. 补 crash-before-commit no-partial-state、commit-response-loss replay original
  result/status tests（≤2h）。
- [ ] 55. 通过正式 wrapper 验证 pinned class1 rollback against floor2，证明 process/workers/
  HTTP 未启动且 DB truth 不变（≤2h）。
- [ ] 56. 运行 signed manifest tamper/digest/key/DB failure fail-closed acceptance（≤2h）。

## Build, evidence and readiness

- [ ] 57. 运行 Node-specific PostgreSQL 18 migration/ACL/constraint acceptance 并保存脱敏
  evidence（≤2h）。
- [ ] 58. 运行 targeted integration tests、restart/replay/race selectors 并记录结果（≤2h）。
- [ ] 59. 运行 `make test build`，处理失败时只增加明确的小任务并重新验证（≤2h）。
- [ ] 60. 更新 OpenAPI/runbook/compatibility evidence、migration reconciliation 和
  implementation task completion ledger（≤2h）。
- [ ] 61. 执行 `openspec validate --strict`、heading comparison、Stage1+Stage2 overlapping
  MODIFIED Requirement 语义合成校验（baseline + Stage1 delta + Stage2 delta 手工复核为完整
  Requirement，Gateway lifecycle 内容未被 Node delta 覆盖或收窄）。对每个 MODIFIED
  Requirement 逐项验证：exact Requirement title matches baseline；baseline normative body
  is preserved；baseline scenarios are preserved；approved prior active-change semantics
  are preserved where applicable；Stage 2 lifecycle delta is additive；resulting Requirement
  is self-contained after OpenSpec replacement semantics。不得以泛化的 baseline 保留声明代替
  完整规范正文。执行 `git diff --check`，确认
  approved-scope reconciliation（无 out-of-scope production 变更）、生成物可复现、全部
  tests/evidence 完整、durable truth reconciliation 完成、authorized commits/worktree
  reconciliation 完成、Runtime Acceptance evidence 就绪，提交 archive readiness awaiting
  review evidence（≤2h）。

当前 completed implementation tasks = 0；Implementation readiness = READY；openspec apply = NOT AUTHORIZED / NOT RUN。
