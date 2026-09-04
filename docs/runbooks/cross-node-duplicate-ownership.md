# Cross-node Duplicate Ownership Runbook

## 1. 适用范围

本手册覆盖 `add-cross-node-duplicate-ownership` 的 detect/refresh/degrade/resolve/reopen 生命周期、restart reconciliation、并发模型、evidence 语义、alert/metrics 与故障处置。Cross-node Duplicate Ownership 是 Control 对同一 Gateway Account 被多个不同 Relay Node 同时声明为 owner 的检测与观测能力；它不修改 Gateway routing、不修改 CLIProxyAPI scheduling、不做自动 rebinding/remediation、不进入 AI request data path。

## 2. Duplicate 定义与 owner eligibility

- Duplicate 的 semantic key 是 `(environment_id, account_key)`；`account_key` 格式固定为 `provider:normalized_email`（例如 `openai:user@example.com`），全程明文，不做 HMAC/fingerprint/mask/rekey。
- 一个 account_key 在同一时刻被 **两个或以上不同 Relay Node** 标记为 owner，才构成 cross-node duplicate；单 Node 内部的重复（Node-local duplicate，`account_inventory_poll_duplicates`,迁移 00006）是完全独立的既有机制，两者在 persistence、alert、metric 上互不影响（见第 11 节证据）。
- Owner eligibility 由 `control_list_eligible_cross_node_owners_v1` 决定；只有 authoritative evidence（`control_evaluate_cross_node_duplicate_evidence_v1`）判定为 `owner_confirmed` 的 Node 才计入 duplicate 判定的分母，`owner_confirmed >= 2` 才能创建/维持 ACTIVE occurrence。

## 3. Detect

顺序（`internal/store/cross_node_duplicate_ownership_lifecycle.go`）：

1. `ListCrossNodeDuplicateCandidates`（discovery，基于当前 Inventory truth 找出候选 account_key）。
2. 单一 PostgreSQL statement 同时取得 `evaluationAt`（`clock_timestamp()`）与 per-node evidence（`EvaluateCrossNodeDuplicateEvidenceAtDatabaseNow`，MVCC 快照与取时同一 statement）。
3. authoritative evidence evaluation：只有 `owner_confirmed >= 2` 才继续。
4. 若无 existing ACTIVE occurrence：`INSERT ... ON CONFLICT DO NOTHING RETURNING`；若 INSERT 返回空（并发竞争），重新 `SELECT ... FOR UPDATE` existing ACTIVE row，重新 discovery + evaluation，走 refresh/reconcile 路径（不复制第二套 lifecycle）。
5. INSERT 成功 → 新 occurrence，触发 `active` alert transition。

## 4. Refresh / Degrade

- Existing occurrence：先 `FOR UPDATE` 锁定 occurrence row，再 discovery，再单 statement authoritative evaluation + evaluationAt，最后落地 lifecycle mutation（不在锁前预取时间戳）。
- Evidence 完整（本次 evaluation 每个 owner_confirmed Node 都取得新鲜、非 stale 的 evidence）→ `evidence_state = complete`，`last_fully_verified_at` 推进。
- 任一 owner Node 的 evidence 是 stale（存在但晚于容忍窗口）而非 fresh absent → `evidence_state = degraded`，**不 resolve**（stale ≠ absence，见第 9 节）。
- Refresh/degrade 不产生新 alert（同一 occurrence 保持同一 alert logical identity）。

## 5. Affected Node add/remove

- 当前 ACTIVE occurrence 的 owner 集合随 authoritative evaluation 结果增减（例如 A/B/C 中 C 变为 fresh absent → 收缩为 A/B，仍 ACTIVE）。
- Membership mutation（`cross_node_duplicate_occurrence_nodes` 的 INSERT/DELETE）与 occurrence projection 更新在同一事务提交，不产生 pairwise occurrence，不产生 phantom occurrence。

## 6. Resolve

- 当 authoritative evaluation 判定 owner_confirmed 数量降到 < 2（例如某 Node fresh absent，只剩 1 个真实 owner）→ occurrence 从 ACTIVE 转为 RESOLVED，`resolved_at = evaluationAt`。
- Resolve 触发 `resolved` alert transition（一次性 ACTIVE → RESOLVED 边界）。
- RESOLVED occurrence 的 identity 字段（`occurrence_id`、`environment_id`、`account_key`、`conflict_type`、`first_seen_at`）冻结不可变。

## 7. Reopen

- 同一 semantic key 在 RESOLVED 之后再次满足 `owner_confirmed >= 2` → 走 Detect 路径，**插入全新的 occurrence_id**（ACTIVE-only partial unique index允许旧 RESOLVED row 与新 ACTIVE row 共存），不复用旧 occurrence_id。
- 新 occurrence 触发新的 `active` alert（新 alert occurrence，不复用旧 alert 状态）。

## 8. Restart / 漏跑 reconciliation

`CrossNodeDuplicateOwnershipReconciler.Reconcile(environmentID)`（`internal/store/cross_node_duplicate_ownership_reconciliation.go`）：

- Key set = `ListCrossNodeDuplicateCandidates`（当前候选） ∪ `ListActiveCrossNodeDuplicateOccurrenceKeys`（当前数据库中仍 ACTIVE 的 occurrence key，覆盖"候选列表里已经消失，但数据库还是 ACTIVE"的场景）。
- 去重后，对每个 key 复用现有 `LifecycleRepository.Evaluate()`，**不复制第二套 lifecycle 逻辑**。
- 幂等：同一 Inventory truth 下连续 reconcile 2 次、3 次，ACTIVE occurrence 数量不增加、occurrence_id 不变化、membership 不重复。Evidence observation 可以每次 append 新记录（这不算不幂等，只要 occurrence identity/lifecycle 不重复）。
- 覆盖场景：duplicate 已存在 restart 后 reuse；duplicate 停机期间首次出现 restart 后创建；Node fresh absent 停机期间 restart 后 resolve；Node stale 停机期间 restart 后 degrade（不 resolve）；A/B/C 其一 fresh absent 停机期间 restart 后收缩仍 ACTIVE。

## 9. 并发模型：为什么不需要 lease/fencing

当前正确性完全由 PostgreSQL primitives 保证，未引入分布式锁：

- `cross_node_duplicate_occurrences` 的 ACTIVE-only partial unique index（`(environment_id, account_key) WHERE status='ACTIVE'`）保证同一 semantic key 不会有两条 ACTIVE row。
- occurrence `FOR UPDATE` 保证同一 occurrence 的并发 lifecycle 调用串行化。
- evidence `(occurrence_id, evaluation_id, instance_id)` unique 保证同一次 evaluation 的每个 Node 只留一条 evidence row。
- 单事务 lifecycle：discovery → 单 statement authoritative evaluation（`evaluationAt` 与 evidence 读取在同一 MVCC snapshot 中获得）→ mutation，全部在一个 PostgreSQL 事务内提交或整体回滚。
- Insert-race 显式处理：`INSERT ... ON CONFLICT DO NOTHING RETURNING` 为空 → 重新 `FOR UPDATE` + 重新 discovery/evaluation，再 reconcile。

已用真实独立 PostgreSQL connection/transaction（非仅 Go goroutine）证明：并发 first-detect 只产生一条 ACTIVE occurrence；并发 refresh/refresh 串行完成且 membership 正确；并发 refresh/resolve 时后拿锁者重新读取 source truth，不允许旧判断覆盖已 RESOLVED 的结果；并发 add/remove 不丢更新；并发 reopen 只创建一条新 ACTIVE occurrence；reconciler 与 normal worker 并发操作同一 key，最终只有一条 ACTIVE occurrence 或正确 RESOLVED，无 stale overwrite；authoritative evaluation 的单 statement MVCC snapshot 证明不会把 evaluationAt 之后才提交的 Inventory 事实,误判为 evaluationAt 时刻已存在的证据（不产生 `source_completed_at > evaluation_at` / `source_scheduled_at > evaluation_at`）。

**结论：Cross-node Duplicate Ownership 不需要额外 lease/fencing。** 不为了减少重复计算引入分布式锁。

## 10. Evidence 来源语义：stale 不等于 absence

- Evidence 判定读取 `account_inventory` 的 `state.last_complete_at`、`state.health_scheduled_at`、`state.updated_at`、`account.updated_at`，均要求 `<= evaluationAt`（migration 00016 timestamp 上界 guard，defense-in-depth，即便单 statement MVCC snapshot 已从根因解决该问题）。
- **Stale**（存在证据但已过期）与 **absence**（确认不存在）是两种不同结果：stale 只能推导 `degraded`（保留 ACTIVE，不 resolve）；只有明确 fresh absent 才能推导 owner 数量减少乃至 resolve。
- Provider policy transition 可能只推进 `monitoring_status`/`updated_at` 而不推进 `last_complete_at`；00016 的 guard 同时约束 `state.updated_at` 与 `account.updated_at`，防止把这类晚于 evaluationAt 的 source metadata 当作可持久化的 degraded evidence。

## 11. Severity / Alert / Metrics

- Severity 固定为 `Critical`，不随 Gateway binding、Gateway Account status、scheduler state、traffic 或 health score 动态调整。
- Alert（`CrossNodeDuplicateOwnershipAlertObserver`，`internal/store/cross_node_duplicate_ownership_lifecycle.go`）：`Created==true` → `active` transition（覆盖首次 detect 与 reopen）；`Created==false && Status=="RESOLVED"` → `resolved` transition（reconcile 内唯一的 ACTIVE→RESOLVED 边界）；refresh/degrade/add-remove 保持 ACTIVE 时静默,不重复创建 alert。context 可包含 `account_key`、`provider`、`email`、affected Nodes、`occurrence_id`、`first_seen_at`；绝不包含 credential/API key/access token/refresh token/password/secret/raw upstream payload。
- Metrics：Prometheus gauge `relay_control_cross_node_duplicate_occurrences`（`internal/store/cross_node_duplicate_ownership_metrics.go`），labels 为低基数的 `environment`/`conflict_type`/`status`/`severity`；不含 account_key/email/occurrence_id/instance_id 等高基数或敏感 label。
- 4.9 独立性证据：Node-local duplicate（`account_inventory_poll_duplicates`）在整个代码库中没有任何 alert/metric 机制，因此与 cross-node duplicate 的 persistence/alert/metric 天然独立（`internal/store/cross_node_duplicate_ownership_independence_test.go`）。
- 生产触发（`cmd/control/cross_node_duplicate_ownership.go`）：复用现有 Account Inventory poll runtime 的既有 control loop 作为 source-truth-changed 触发器，**不是新的 request routing scheduler，也不是新的 Duplicate Ownership scheduler**——只是把 `CrossNodeDuplicateOwnershipReconciler.Reconcile(ctx, environmentID)` 挂到 `inventorypoll.Config.LifecycleObserver`（`internal/inventorypoll/config.go`）这个可选回调上，共三类触发语义：
  - Startup catch-up：`inventorypoll.Service.reconcileUntilAvailable()` 首次成功调用 `Reconciler.ReconcileOnce()` 时，该次成功即已触发一次 lifecycle observer（见下），因此 Control 启动、且 DB 中已存在 duplicate Inventory truth 时会创建/刷新 ACTIVE occurrence，无需人工调用；`Service.Run()` 不再重复显式调用，避免启动时触发两次。
  - Periodic freshness reconciliation：`inventorypoll.Reconciler.ReconcileOnce()`（`internal/inventorypoll/reconciler.go`）在每次成功完成数据库 `ReconcileExpired` 后都会调用一次 lifecycle observer——即使该次 `RetryWait=0`/`Abandoned=0`（没有任何 lease 需要处理）。这一路径专门覆盖“owner freshness 随数据库时间自然从 fresh 变 stale，但没有任何新 finalize 发生”的场景：既有 Account Inventory reconcile 周期循环本身就是 duplicate ownership 的 freshness/time-based 触发器，不是新增的 Duplicate Ownership scheduler。DB reconcile 失败时不会调用该回调。
  - Low-latency ongoing reconciliation：`inventorypoll.Worker.execute()` 在每次成功 `FinalizeFenced`（即每次 promotion 写入 `account_inventory`）之后调用一次（`internal/inventorypoll/worker.go`），用于对新 Inventory truth 做低延迟响应。
  - 该回调默认 nil/no-op，失败只记录固定低敏错误日志（`component=cross_node_duplicate_ownership`，不含 account_key/email），不会回写或篡改 Account Inventory poll 的 source truth。回调上下文使用 `context.WithTimeout(parent, ...)`（而非 `WithoutCancel`），因此父 context 取消会正确传播到进行中的 reconciliation。
- Alert observer 生产实现：`crossNodeDuplicateOwnershipSlogAlertObserver`（`cmd/control/cross_node_duplicate_ownership.go`）在 `main()` 中通过 `lifecycle.SetAlertObserver(...)` 安装，把 active/resolved transition 写入既有结构化 logger；生产环境不使用默认 no-op observer。Metrics collector 已在 `cmd/control/main.go` 注册（只读、报告当前 DB 状态）。

## 12. Read API

复用既有 Control HTTP/API 架构与 `super_admin` 认证边界（`internal/api/cross_node_duplicate_occurrence_handlers.go`）：

- `GET /api/cross-node-duplicate-occurrences`：分页 list，支持 `status`（ACTIVE/RESOLVED）、`instance_id`、`account_key` 过滤，bounded page size（合理默认 + 最大值）。
- `GET /api/cross-node-duplicate-occurrences/{occurrence_id}`：detail。
- `GET /api/cross-node-duplicate-occurrences/{occurrence_id}/evidence`：单独分页的 evidence history（list 端点默认不返回全部 evidence）。
- 只读，无 mutation 端点；`account_key` 在 API 中明文展示，可拆为 `provider`/`normalized_email`。

## 13. Troubleshooting

| 现象 | 排查 |
|---|---|
| occurrence 一直 `degraded` 不 `resolve` | 检查该 owner Node 的 `account_inventory` 是否只是 stale（poll 间隔过长/暂时失败），而不是真正的 fresh absent；stale 是设计上的保守行为,不会被当作 resolve 的证据。 |
| 同一 duplicate 短时间内出现多个 occurrence_id | 检查是否发生 resolve→reopen；同一 key 的 reopen 设计上会产生新 occurrence_id，这是预期行为，不是 bug。 |
| Reconcile 后 ACTIVE 数量增加 | 违反幂等性,是回归；检查是否有代码路径绕开了 `ListCrossNodeDuplicateCandidates ∪ ListActiveCrossNodeDuplicateOccurrenceKeys` 或复制了第二套 lifecycle。 |
| Metric `relay_control_cross_node_duplicate_occurrences` 一直是 0 | 确认 Account Inventory poll 是否已启用（`lifecycleEnabled`）——生产 reconciliation trigger 挂在其既有 control loop 上，poll 未启用时 duplicate reconciliation 也不会运行；检查 `cross_node_duplicate_ownership` 相关错误日志。 |
| Evidence 显示 `source_completed_at > evaluation_at` | 违反 00013 `no_future_eval` CHECK 与单 statement MVCC 快照设计,是回归；检查是否有代码路径绕开了 `EvaluateCrossNodeDuplicateEvidenceAtDatabaseNow` 而分两条 statement 取时间与证据。 |

## 14. Non-goals（本 change 不做）

- 不修改 Gateway routing 或 Gateway Account/Binding 语义。
- 不修改 CLIProxyAPI scheduling、provider、cooldown 或 health decision。
- 不做 automatic rebinding、automatic remediation 或基于 duplicate 判定的自动写操作。
- 不进入 AI request data path。
- 不引入 lease/fencing、新 scheduler 或新表/字段/index（超出 detect/refresh/resolve/reopen/reconcile 所必需的最小 schema）。
- 不为 account_key/email 引入 HMAC/fingerprint/mask/rekey。
- 不在 Prometheus label 中使用 account/email。

## 15. 验收命令

```sh
make generate
go test -race -count=1 ./internal/store/... -run TestCrossNodeDuplicateOwnership
go test -count=1 ./internal/api/... -run CrossNodeDuplicate
go test -count=1 ./...
openspec validate add-cross-node-duplicate-ownership --type change --strict --no-interactive
git diff --check
```

PostgreSQL 相关 integration/acceptance 测试需要设置 `CONTROL_DATABASE_TEST_URL` 与 `CONTROL_RUNTIME_DATABASE_TEST_URL`（指向拥有 `relay_control_runtime` capability 的 LOGIN 角色，例如开发环境的 `relay_control_app_dev`，而不是 migration owner `relay_control_migrator`）；未设置时用例会 `SKIP`，不构成验收证据。用错 migration-owner 凭据会让 runtime 权限矩阵测试静默通过（因为 owner 拥有全部权限），必须使用真正受限的 runtime 角色凭据才能验证最小权限边界。
