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
