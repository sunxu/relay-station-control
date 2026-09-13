# Phase 6 Stage 3 implementation validation

## Baseline and scope

- Stage 1: CLOSED / IMPLEMENTED / ARCHIVED.
- Stage 2: CLOSED / IMPLEMENTED / ARCHIVED.
- Stage 2 archive commit: `5199b611a99ac36b46a5a0309db1c01d3fe50929`.
- Stage 3 planning readiness: PASS / READY.
- `openspec instructions apply add-relay-node-management-operations`: RUN.
- Compatibility class/floor remain `2 / 2`.
- Migration 35 adds no business table or column. It adds the frozen Disable-fence index, reason/audit allowlist extensions, and narrow controlled functions.

## Runtime Acceptance matrix

| Area | Result | Evidence |
|---|---|---|
| Canonical baseline reconciliation | PASS | `check-planning.py` compares every MODIFIED title and canonical scenario set against current `openspec/specs/**`; manual review confirms the complete Stage 1/2 normative semantics are retained and Stage 3 changes are additive. |
| Disable fence index and bounded lookup | PASS | PostgreSQL 18.6 `EXPLAIN` selects `asset_admin_command_receipts_node_disable_fence_idx`; the helper orders only by `committed_at DESC LIMIT 1`. |
| Strict Disable `committed_at` | PASS | PostgreSQL 18.6 A/B/C receipts are strictly increasing; equal timestamps are rejected and latest lookup returns C. |
| Receipt immutability | PASS | Runtime UPDATE/DELETE/TRUNCATE are rejected; same-command replay creates no new receipt/fence. |
| F0/F1 writer race | PASS | A READ COMMITTED writer blocked on the Node lock observes a Disable committed while waiting and fails with SQLSTATE 55000 with zero activation. |
| Already-disabled fence and restart semantics | PASS | A new no-op Disable advances the fence; an old F0 conflicts and a subsequently captured F0 can proceed. No in-memory F0 is persisted. |
| Operational writer ACL and old five-parameter writer | PASS | PostgreSQL 18.6 executes the formal deployment template as `relay_control_asset_registrar`; registrar and runtime can execute the six-parameter writer only with their owned reason families, cross-owner reasons are rejected with 42501, registrar direct table DML is rejected, and the old five-parameter entrypoint fails closed. Migration 35 preserves only migration 34's class2 runtime lifecycle-column UPDATE needed by Retire/Replace on forward schema. |
| Enable / Disable | PASS | PG18 covers enabled, replay, current disable, future-only cancellation, already-disabled, exact counts, audit and generation behavior. |
| Generation rollback | PASS | PG18 max-generation Enable fails with SQLSTATE 22003 and rolls back the activation. |
| Shared receipt/canonical intent | PASS | Golden arrays are 90/92 UTF-8 bytes with the frozen hashes; actor/kind/encoding checks precede domain state; first response and replay use the same persisted JSONB bytes. |
| Health / Connection Test | PASS | One secret-free authorization path returns only identity/lifecycle/type/contract/endpoint/capabilities. A Node with `reader_secret_ref=NULL` completes one Health Probe and one Connection Test Probe; Driver registry/`Driver.Probe` use a zero-value ReaderSecretReference, SecretResolver call count is 0, and responses/audits contain no Secret reference. Fixed bounded observations retain separate audit/metric actions; audit failure returns 503 without a second Probe. |
| HTTP/API security | PASS | PG18 HTTP integration covers session, super-admin path, GET Health without CSRF, POST CSRF/same-origin, malformed/unknown/oversize, 404/409/503 and no-store/request ID envelopes. |
| Probe transport | PASS | Existing CLIProxyAPI Driver tests cover fixed HTTP health target, non-200, invalid/oversize, timeout/cancellation, transport failure, redirect/proxy/retry rejection and one request. No Secret resolver is called by Probe. |
| UI | PASS | 29 frontend test files / 197 tests; active detail exposes four explicit controls, mount performs zero Probe, Disable confirms future cancellation, stable command UUID and late-response isolation are covered, retired detail has no controls. |
| Authenticated UI E2E | PASS | Real headless Chromium: `node-lifecycle.spec.ts`, 1 passed; lifecycle and four Stage 3 controls execute through same-origin Control API mocks, with no browser request to Node/Secret target. |
| Stage 2 class2 rollback compatibility | PASS | A real Linux artifact built from fixed Stage2 commit `5199b611a99ac36b46a5a0309db1c01d3fe50929` (`sha256:b8cc8d7a6a95fff3c57146dff11eb924d7fd5fd4aa52764d8e917df42f21b507`) starts through the formal signed-manifest wrapper as class2 at floor2 against an isolated PostgreSQL 18.6 database migrated through 35. After committed Stage3 Disable/cancellation truth and process/session restart, a fresh F0 completes a six-parameter scheduled write, the old five-parameter entrypoint fails closed, and the old binary completes Node read, reconcile, Replace and Retire smoke without interpreting Disable `committed_at` as business conflict truth. |
| Stage 2 regression | PASS | `make test` includes Node lifecycle, monitoring-ineligible promotion, lifecycle fences, generation/read_as_of, capability immutability, shared receipts/K1 and compatibility tests. |
| Generated artifacts | PASS | The final correction candidate ran `make generate` twice with no second generated delta; the ordered generated-artifact checksum aggregate is `e4c6fd55335faa2ecf607bf4b3623c5684d77043402882d90fff9c588db9887a`. UI uses the generated Stage 3 client functions. |
| Production build | PASS | `make build`; Vite production bundle and Control binary completed. |
| Broad tests | PASS | `make test`; Go/tools/frontend tests and TypeScript typecheck completed. |
| OpenSpec strict | PASS | `openspec validate --all --strict`: 28 passed / 0 failed. |
| Diff hygiene | PASS | `git diff --check`. |

## PostgreSQL 18.6 commands

The isolated migration-35 tests ran against `postgres:18.6-alpine` and passed:

- `TestNodeMonitoringOperationsMigrationPG18`
- `TestNodeMonitoringFenceIndexAndStrictOrderingPG18`
- `TestNodeMonitoringFenceRuntimeReceiptMutationRejectedPG18`
- `TestNodeMonitoringFenceF0F1WaitSeesCommittedDisablePG18`
- `TestNodeMonitoringFenceAlreadyDisabledBlocksOldF0ButAcceptsNewF0PG18`
- `TestNodeMonitoringFenceGenerationOverflowRollsBackPG18`
- `TestNodeManagementOperationsHTTPIntegration`
- `TestNodeMonitoringOperationalRegistrarACLAndScriptPG18`
- `TestStage2RealArtifactRollbackAgainstStage3ForwardSchemaPG18`
- `TestSupportedWrapperAgainstFloorTwoPG18`

## Independent implementation review round 1

Review disposition: P0 = 0, P1 = 3, P2 = 1; CHANGES REQUIRED.

- P1-1: operational writer ACL did not permit the supported `relay_control_asset_registrar` path and did not enforce caller/reason ownership.
- P1-2: Probe authorization unnecessarily read and required `reader_secret_ref`, contrary to the secret-free Probe contract.
- P1-3: the recorded Stage 2 rollback evidence used compatibility validation and static scans rather than a real artifact built from commit `5199b611a99ac36b46a5a0309db1c01d3fe50929` against forward migration 35 truth.
- P2-1: planning and implementation evidence retained stale planning-only/current-state claims.

The affected evidence rows above are historical results from the first candidate. The correction acceptance subsequently completed and is recorded below.

## Independent review correction

- P1-1 FIXED: the formal registrar deployment writer can capture F0 and execute the six-parameter function; DB role/reason ownership rejects registrar administrator reasons and runtime operational reasons, while the old five-parameter entrypoint remains unavailable.
- P1-2 FIXED: probe authorization no longer selects, returns, or requires `reader_secret_ref`; secretless Health and Connection Test each execute one Probe with zero SecretResolver calls.
- P1-3 FIXED: rollback acceptance now builds the fixed Stage2 commit, validates its signed class2 artifact through the mandatory wrapper against migration 35/floor2, discards pre-restart intent, and executes fresh-intent/read/reconcile/Retire/Replace smoke.
- P2-1 FIXED: current planning and implementation evidence now reflects the archived Stage1/2 baseline, applied Stage3 workflow, production changes, correction state, and actual validation.
- Targeted PostgreSQL 18.6, API, compatibility-wrapper and Stage2 lifecycle/promotion regressions: PASS.
- `make generate` twice: PASS; no second generated delta, ordered generated-artifact checksum aggregate `e4c6fd55335faa2ecf607bf4b3623c5684d77043402882d90fff9c588db9887a`.
- `make test`: PASS, including 29 frontend files / 197 tests and TypeScript typecheck.
- `make build`: PASS, including Vite production bundle and Control binary.

## Independent implementation re-review round 2

Review disposition: P0 = 0, P1 = 0, P2 = 1.

- The three P1 findings from round 1 were independently confirmed FIXED.
- The only remaining P2 finding was stale current-state wording in `planning-validation.md`; that wording now records migration 35 and the completed production implementation.
- P2-1 stale evidence: FIXED; final independent implementation re-review is pending.

## Final independent implementation re-review

Review disposition: P0 = 0, P1 = 0, P2 = 0; PASS.

- P1-1 operational writer ACL: FIXED.
- P1-2 secret-free Probe: FIXED.
- P1-3 real Stage2 class2 rollback: FIXED.
- P2-1 stale evidence: FIXED.
- Independent implementation review: COMPLETE / PASS.

## Current disposition

- Implementation: COMPLETE.
- Runtime Acceptance: PASS.
- First independent implementation review: CHANGES REQUIRED (historical P0 = 0, P1 = 3, P2 = 1); all findings FIXED.
- Second independent implementation re-review: P0 = 0, P1 = 0, P2 = 1; its sole documentation finding is FIXED.
- Final independent implementation re-review: PASS (P0 = 0, P1 = 0, P2 = 0).
- Independent implementation review: PASS.
- Production code changed: true.
- Stage 3 implementation commit: `b17c673f1146a6bdf7bc9a5e7fc4c33b8c597566`.
- Stage 3 closeout commit: `b1d4ebac1cbe6772918d872c2f067297104ae17a`.
- Implementation commit: CREATED.
- Completed implementation tasks: 58 / 58.
- Task 50: COMPLETE.
- Task 50 closeout evidence reconciliation: COMPLETE.
- Post-implementation-commit clean worktree verification: PASS.
- Git/worktree closeout: COMPLETE.
- Git closeout evidence: implementation and closeout commits recorded above; both were pushed before archive authorization.
- Archive-readiness review: PASS / APPROVED (P0 = 0, P1 = 0, P2 = 0).
- Archive readiness: PASS / APPROVED.
- Archive: COMPLETE.
- Stage 3: CLOSED / IMPLEMENTED / ARCHIVED.
