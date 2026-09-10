# Phase 5 Slice A implementation validation

Date: 2026-09-10. Scope: durable-job execution-policy foundation only.

Detailed Requirements: FROZEN; Architecture Review: PASS; Implementation: IN PROGRESS;
Runtime Acceptance: NOT STARTED. Slice A awaits Implementation Review. No commit/push.

## Implementation baseline

All four worktrees were clean before implementation. These are the committed
implementation baselines, not this uncommitted implementation's SHA or a new approval.

| Repository | HEAD |
| --- | --- |
| Control | `ec0e8a2951cc158509c62942f0e622b98bd4e27f` |
| Ops | `6d7f5d86deb613d62262475b675e75d55b571148` |
| Gateway | `6b045698e6e5e62e35dbd103abf20c1407f8a0bb` |
| CLIProxyAPI | `273d624c70f6eb8bdd7b049df396c306acd3f8d0` |

The historical amendment review remains in [planning-validation.md](./planning-validation.md).

## Implemented boundary

- Independent `AllowUnknownEffectReplay` / `AllowDirectSuccess` booleans in
  Definition, CatalogEntry and Job; registry validation, enqueue snapshot,
  same-key compatibility, DB catalog comparison and Worker/Reconciler compatibility.
- Additive Goose `00028_durable_job_execution_policies.sql`: two default-false
  fields on each existing table, unknown-replay/replay-safe CHECKs, immutable job
  snapshots, existing lifecycle/event/fenced functions. Historical migrations unchanged.
- `ExecuteSucceeded` can complete directly only with the persisted direct policy
  and valid fencing/lease. Ordinary jobs remain Verify-first; unauthorized success fails closed.
- Opted-in unknown results use existing retry_wait/backoff/normal Worker claim;
  expired running recovery does not Execute or increment verification attempts.
  Budget, cancellation and deadline remain fail-closed.
- The DB rechecks lease expiry after acquiring the row lock. Cancellation before
  unknown retry commit fails atomically; cancellation after unknown retry commit
  also fails rather than claiming safe cancellation. Existing job events preserve
  unknown-effect evidence across subsequent attempts: a later no-effect attempt
  cannot prove an earlier unknown attempt had no effect. No new ledger/table/state.
- `queries/jobs.sql` and the store adapter map both policies; sqlc artifacts were
  generated with `make generate`, not manually edited.

No production DingTalk job kind/executor/config/HTTP client is registered. No Token,
Quality v2, Problems, occurrence enqueue, UI, Phase 6/7, Gateway or CLIProxyAPI changes.
No new job status, event type, policy table or retry engine.

## Validation

Commands use the workspace-prescribed DevRAM caches. PostgreSQL checks use a
dedicated scratch database on the existing local PG18 test container, not the
running Relay Station deployment. Test credentials are local development fixtures.
The dedicated scratch database was removed after validation; it can be recreated
by the test setup. No running deployment database was altered.

| Check | Result |
| --- | --- |
| `go test ./internal/jobs/... ./cmd/control/... -count=1` | PASS |
| `go test ./internal/store/... -run '^TestDurableJob' -count=1` with owner/runtime DB URLs | PASS (16.748s, main-agent rerun) |
| All durable-job tests plus eight affected historical compatibility cases after fixture pinning | PASS (31.367s, main-agent rerun) |
| `go test ./internal/store/... -count=1` with owner/runtime DB URLs | FAIL (472.322s); baseline/fixture findings below, not claimed PASS |
| Clean install through 00028; existing 00027 DB/job forward upgrade defaults | PASS (isolated PostgreSQL tests) |
| Existing durable-job constraint/idempotency/Verify/rollback/recovery tests | PASS |
| New policy persistence, independent combinations, DB authorization, expired fencing, cancellation and budget tests | PASS |
| `make generate` | PASS |
| `make test build` | PASS; default run without DB URLs, not substituted for PostgreSQL checks |
| Frontend unit tests / typecheck / build | PASS (22 files, 133 tests); no frontend source changes |
| Current change OpenSpec strict / all OpenSpec strict | PASS (20/20 items) |
| Ops YAML parse and changed Markdown local-reference targets | PASS |
| `git diff --check` (Control/Ops) | PASS |

The initial build hit DevRAM capacity; only reproducible Go build cache was cleared,
then build checks passed. The legacy migration-00004 empty down/up test remains
on that schema, then upgrades before exercising the current adapter's concurrent
enqueue and transaction-bound success/rollback assertions. Other 00004 evidence
protection fixtures remain on 00004; 00028 has its own forward-only test. No
existing Verify-first or adapter mutation assertion was removed or weakened.

### Full-store failure boundary

The full database suite is **not green**. A separate exported copy of committed
Control HEAD `ec0e8a2951cc158509c62942f0e622b98bd4e27f` was used for comparison:

- A selected 21-test baseline run failed in 31.365s: 11 existing test functions
  already assumed an obsolete latest migration/down target (history/provider
  query, request history, Gateway/Binding and Node Quality rollback fixtures).
- A separate baseline run reproduced Gateway recovery/scheduling/TLS and fixture
  FK/unique failures in `TestGatewayDirectoryRecoveryEvidence` and
  `TestGatewayDirectoryRepositoryWorkflowAndRecovery` (2.201s).
- The full current run also hit a query-plan assertion and Gateway TLS failures;
  the query-plan and coordinator cases passed in an isolated current rerun.
  This does not establish a green combined suite or resolve those existing test risks.
- Eight previously passing historical rollback tests were newly intercepted by
  forward-only 00028. Only their fixtures were pinned to the prior Phase 4 schema
  27 (including re-up where applicable); assertions and production code remain
  unchanged. Availability's fixture accepts an optional migration target, with
  the default still testing latest. This avoids relaxing the new safety boundary.

Unrelated failed fixtures/product behavior were not redesigned to make the suite
green. Full-store green status remains a separate validation limitation to review.

Not executed: Phase 5 full deploy/runtime acceptance, real DingTalk delivery and
subsequent slices. Unit/integration results do not imply Runtime Acceptance PASS.

## Task accounting and stop point

Completed: 1.1, 3.1b, 3.1c, 3.1d, 3.1f, 4.5c, 4.5f (7/47).
3.1a/3.1e foundation work is implemented, but their real DingTalk enablement is
not, so those aggregate checkboxes remain open. Full-Phase delivery/acceptance
tasks remain open even where this slice ran a subset of the eventual checks.

STOP after Slice A. Implementation Review is pending; no Slice B work is authorized here.

## Cancellation amendment implementation — 2026-09-10

本节追加 resumed Slice A 证据；上文为此前实现与验证历史，不覆盖或重写。

### Reviewed baseline 与 Stage 1 status commits

- Reviewed Control: `b209ad1c42034f98c759a3885191d404981147d8`
- Reviewed Ops: `0652f80a9a16c4481d452d918ae71d4d3d11067e`
- Control approval/status commit（本轮实施基线）: `d0540d9018d53819c4f091ffafe31fb9311d3504`
- Ops approval/status commit: `dd3041f916dc16578f21790e71e49c3f751decda`
- Gateway: `6b045698e6e5e62e35dbd103abf20c1407f8a0bb`
- CLIProxyAPI: `273d624c70f6eb8bdd7b049df396c306acd3f8d0`

Stage 1 current/all OpenSpec strict（20/20）、Markdown fences/38 local references、Ops YAML/status 与 diff checks 均通过；提交内容只有规划/evidence Markdown 和 Ops compatibility metadata。Stage 1 前后 production 文件集合 SHA-256 一致，未改已有实施、迁移、测试。用户授权的目录级文档提交同时纳入原有未提交 slice-a-validation.md，其历史内容未修改；实施代码未提交，未 push。

### 实施边界

- 直接修订未提交/未 release/deploy 的 00028；未新建 00029，未修改历史 migrations。
- 三个 framework-owned reason codes 与 ErrorCode 分离；即使 Executor allowlist 包含保留字符串，也不能用 ErrorCode 伪造效果证据。Worker/current unknown、expired-running recovery、current no-effect 和 VerifyEffectAbsent 均有对应固定 reason。
- DB 按 immutable event sequence 查 latest effect_absent_verified 之后的 unknown；当前待提交 unknown 也在锁内计入。当前 no-effect 不抹除旧 unknown，Verify reset 不永久 sticky。policy 只控制重放授权，不作为效果事实。
- running→cancelled 只允许有效 Worker lease/fence、可见取消、当前 known-no-effect proof、无 unresolved prior unknown；原子写 cancelled/worker/cancel_verified_safe，释放 lease。直接成功遇并发取消 failed/cancel_after_effect_applied，不自动 rollback。
- 既有未提交的其它 Slice A 基础实现与八项 historical fixture pinning 保留；无 Token/Problems/DingTalk HTTP、Gateway/CLIProxyAPI、Phase 6/7 实施，无新增 policy/status/event type/table/retry engine。

### Cancellation matrix 与 focused validation

| 场景 | 结果 |
| --- | --- |
| A：current no-effect、无 prior unknown、claim 后取消 | PASS：cancelled / cancel_verified_safe；policy false/true 对照 |
| B：current unknown、commit 前取消 | PASS：failed / cancel_after_unknown_effect；含最后一次预算失败与并发取消 |
| C：prior unknown + later no-effect | PASS：取消在第二次 commit 前或后均 unsafe failed |
| D：VerifyEffectAbsent 消解旧 unknown | PASS：后续 cancelled / cancel_verified_safe |
| D2：verified marker 后有新 unknown | PASS：重新 unsafe failed |
| E：普通 retry_wait 无 unresolved unknown | PASS：cancelled；policy false/true 不改变事实 |
| F：direct success + cancel | PASS：failed / cancel_after_effect_applied；无自动 rollback |
| G：NeedsVerification + cancel | PASS：保留 verifying 与取消请求，沿用 Verify cancellation |
| H：PermanentFailure + cancel | PASS：仍 failed，不推导 no-effect |
| DB bypass / atomicity | PASS：无取消/无 proof/prior unknown 的显式 running→cancelled 拒绝；旧 fence/过期 lease 拒绝；非法 event reason/actor 回滚状态及事件 |
| 实际行锁等待 | PASS：取消事务持锁，fenced transition 等锁后看到已提交取消并安全收敛 |

- `go test ./internal/jobs/... ./cmd/control/... -count=1`：PASS（主流程最终检查 3.753s / 0.773s）。
- 配置 owner/runtime PostgreSQL URL 的全部 `^TestDurableJob`：PASS（39.130s，36 个顶层测试）；包括原始 policy snapshot/invariant、幂等/catalog mismatch、Verify-first、lease/fence、budget、clean install/forward upgrade。
- 随后仅加固锁等待测试清理并增加 final-attempt/cancel 对照；受影响测试分别复跑 PASS（1.679s / 3.055s），无新 failure signature。
- 八项 pinned historical compatibility tests：PASS（13.764s）；未进一步修改或弱化原 fixtures/断言。
- `make generate`：PASS；`make test build`：PASS。默认不配置 DB URL 的 make test 不替代上述真实 PostgreSQL tests。前端 22 files / 133 tests PASS；构建出现非阻塞 Go stat-cache 写权限 warning，退出码 0。
- Full-store：NOT GREEN / PRE-EXISTING BASELINE；本轮未重跑全 DB suite，也未修复已有 Gateway TLS/recovery、history rollback、query-plan/CI/Vitest baseline。未执行 Phase 5 全套 Runtime Acceptance。

新增完成项：3.1g、3.1h、3.1i。总进度 10/50；3.1、3.1a、3.1e 和真实 DingTalk/其它 slices/full acceptance 保持 open。Architecture Review PASS；Implementation readiness READY；Implementation IN PROGRESS；Runtime Acceptance NOT STARTED。

STOP：amended Slice A 等待 Implementation re-review；不开始 Slice B，不提交实施改动，不 push。

## Implementation Review fixes — committed outcome and ordinary cancellation — 2026-09-10

本节只追加本轮 P1/P2 修复证据，保留上文历史。实施基线为 Control `d0540d9018d53819c4f091ffafe31fb9311d3504`；Ops `dd3041f916dc16578f21790e71e49c3f751decda`，Gateway / CLIProxyAPI HEAD 与上文兼容基线相同。原有未提交 Slice A 改动保留；Architecture Review 继续 PASS，未 reopen。

### 修复与边界

- P1：既有 `Repository.TransitionFenced` 返回最小 `TransitionOutcome{Status, ErrorCode}`。store 捕获 fenced SQL 的最终 row，仅在原 pgx transaction 成功 commit 后返回；SQL/event/Mutation/commit 失败返回错误与 zero outcome。`Transition.Mutation` 仍位于原事务中，没有额外查询、post-commit mutation 或第二套事务。
- Worker（含 fail-closed 路径）与 Reconciler 只使用实际 committed status/error 生成日志，不回退 requested target/error。实际 failed 使用 `ResultFailure`；实际 cancelled 使用既有 `ResultSkipped`，保留实际 error，包括空值；repository error 仍遵循 lost-lease/database failure 日志语义。
- P2：直接修订未提交的 00028。pending 与普通 retry_wait 取消保持 cancelled、service/request reason、job/event error_code NULL。仅 retry_wait 有真实 `effect_absent_verified` 且无后续 unresolved unknown 才使用 `cancel_verified_safe`；unresolved unknown 仍 failed / `cancel_after_unknown_effect`。窄 running→cancelled 不变，继续使用 `cancel_verified_safe`。
- 无新 migration、状态、事件类型、policy、表或 workflow；没有 Token Health、Problems、DingTalk HTTP、Gateway / CLIProxyAPI 或 Phase 6/7 改动。

### Focused regression

| 检查面 | 结果 |
| --- | --- |
| Worker requested succeeded → actual failed | PASS：日志 failure / cancel_after_effect_applied |
| Worker current unknown requested retry_wait → actual failed | PASS：日志 failure / cancel_after_unknown_effect |
| Reconciler expired-running recovery requested retry_wait | PASS：cancel / deadline / max-attempt DB rewrite 均记录实际 failure/error |
| requested retry_wait → actual cancelled | PASS：caller 收到实际 cancelled；日志 skipped，不误报 success；空 actual error 不回退 requested error |
| Mutation 与状态/事件原子性 | PASS：verified success 与 completed rollback 原有断言保留；成功返回实际 outcome，mutation failure 回滚且 zero outcome；旧 fence/过期 lease 仍拒绝 |
| Pending / ordinary retry_wait | PASS：job 和最新 event 严格检查 error_code IS NULL；取消 reason/actor 保留 |
| A / B / C / D / D2 / E / F / G / H | PASS：窄安全取消、current unknown、prior unknown、Verify reset、reset 后新 unknown、普通 retry、direct success race、NeedsVerification、PermanentFailure |
| Original Slice A | PASS：policy snapshot、幂等/catalog mismatch、unknown⇒replay_safe、Verify-first、policy≠evidence、lease/fencing、attempt budget、clean install / forward upgrade |

### Validation

- `go test ./internal/jobs/... ./cmd/control/... -count=1`：PASS（4.021s / 1.379s），含 actual/requested 不同的 fake repository 日志对照。
- 配置隔离 owner/runtime PostgreSQL URL 的全部 `^TestDurableJob`：最终 PASS（37 个顶层测试，39.664s）。测试数据库使用本地开发 PostgreSQL 55432，未更改运行中的 Relay Station 部署数据库。
- 八项 pinned historical compatibility tests：PASS（14.815s），原 fixtures 与断言未进一步弱化。
- `make generate`：PASS；`make test build`：PASS（exit 0，前端 22 files / 133 tests、typecheck/build PASS）。Go stat-cache 写权限 warning 非阻塞；未为此修改缓存配置或无关实现。默认 make 未配置 DB URL，其结果不替代真实 PostgreSQL suite。
- 首次先行 make 在测试文件编辑中遇到语法错误，完成适配后复跑通过；首次 PostgreSQL suite 的 A outcome 与 ordinary reason/error 对照各有一处错误期望，均按冻结契约修正后重跑全部 suite 通过。这些是本轮测试修订问题，不归入 pre-existing baseline，也未通过放宽生产语义解决。
- Current change OpenSpec strict / all-repo strict：PASS（20/20）；Markdown/local-reference 与 `git diff --check`：PASS。
- Full-store：**NOT GREEN / PRE-EXISTING**；本轮未重跑完整数据库 suite，未修 Gateway TLS/history/CI/query-plan/Vitest baseline。Phase 5 全套 Runtime Acceptance 未执行。

P1 committed outcome propagation：RESOLVED；P2 ordinary cancellation semantics：RESOLVED。本轮 scoped review：P0=0、P1=0、P2=0；Slice A readiness for final Implementation re-review = READY，不代替正式 Implementation Review approval。

任务保持 10/50，3.1g/3.1h/3.1i 的 focused regression 已重新通过；未新增架构 task，未勾选后续 slices。Implementation = IN PROGRESS；Runtime Acceptance = NOT STARTED。

STOP：只完成这两个 findings；不恢复后续 Slice A 扩展、不开始 Slice B、不 commit/push。
