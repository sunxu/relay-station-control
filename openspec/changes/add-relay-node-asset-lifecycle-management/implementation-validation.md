# Phase 6 Stage 2 Implementation Validation

## Status

- Stage 1: CLOSED / IMPLEMENTED / ARCHIVED
- Stage 1 archive commit: `cf5109a980d41fbe8187e302335a3df820715889`
- Stage 2 instructions apply: RUN
- Planning readiness: PASS / READY
- Implementation readiness: READY
- Independent architecture re-review: PASS
- Architecture review findings: P0 = 0 / P1 = 0 / P2 = 0
- Monitoring-ineligible taxonomy architecture gap: RESOLVED
- Architecture decision: `monitoring_ineligible` promotion skip reason
- Implementation: COMPLETE
- Runtime Acceptance: PASS
- Independent implementation review: PASS
- Implementation review findings: P0 = 0 / P1 = 0 / P2 = 0
- Previous review findings: P1-1 through P1-4 and P2-1 are FIXED; independent implementation re-review = PASS
- completed implementation tasks: 67 / 67
- Task 61: COMPLETE
- Task 61 closeout evidence reconciliation: COMPLETE
- Stage 2 implementation commit: `ff36a39a8b41db52b84a1e3966a5f84c81c23b1d`
- Stage 2 closeout commit: `fab572e58743533abda889118fa1a43f2f9ce094`
- implementation commit: CREATED
- Git/worktree closeout: COMPLETE
- Archive-readiness P2 finding: FIXED
- Independent archive-readiness re-review: AWAITING RE-REVIEW
- Archive readiness: READY FOR INDEPENDENT ARCHIVE-READINESS RE-REVIEW
- Archive: NOT RUN
- Stage 3 implementation: NOT STARTED

## Historical independent implementation review findings

Independent implementation re-review = PASS (P0 = 0 / P1 = 0 / P2 = 0). All findings below
are FIXED and retained as historical review evidence.

- P1-1 corrected: the Worker captures local monotonic time before dispatch authorization and uses
  an absolute deadline derived from the minimum DB-returned lease/grace budget and existing request
  timeout. Delayed-return and exhausted-budget tests prove authorization latency is deducted and
  exhausted budget issues zero HTTP.
- P1-2 corrected: finalize locks the run and Node, reads one DB time, applies lifecycle before
  monitoring before policy precedence, and persists aligned run/Provider
  `monitoring_ineligible` evidence with zero current projection. PostgreSQL 18 tests cover both
  Node-lock orders, natural expiry, policy precedence, lifecycle precedence, and Provider-health
  exclusion.
- P1-3 corrected: runtime direct capability INSERT/UPDATE/DELETE/TRUNCATE is revoked. The narrow
  SECURITY DEFINER initial-declaration function atomically creates only a new revision-1 identity
  and its validated complete capability set; Register and Replace use this path. PostgreSQL 18
  rejects direct append against active and retired identities without changing revision,
  generation, audit, or receipt truth.
- P1-4 corrected: Register/Edit/Retire/Replace load the complete post-mutation Node projection from
  the same transaction before constructing both the HTTP body and immutable receipt result.
  Monitored Edit, Retire, Replace, detail consistency, and exact replay tests pass.
- P2-1 corrected: Node mutations use a Node-specific fixed result classifier with tested
  success/replay/invalid/conflict/unavailable families and no identity labels.

The affected owning tasks and acceptance reruns are complete. Task 61 closeout evidence
reconciliation completed before Git closeout. Git/worktree reconciliation was subsequently
explicitly authorized and completed, Task 61 is COMPLETE, and the Stage 2 implementation commit is
`ff36a39a8b41db52b84a1e3966a5f84c81c23b1d`. Archive remains NOT RUN and requires separate
authorization; Stage 3 implementation remains NOT STARTED.

## Implemented durable truth

- Migration 34 raises the compatibility evidence floor from 1 to 2 before exposing Node
  lifecycle truth. It adds Node lifecycle/revision fields, immutable replacement lineage,
  monitoring cancellation metadata and empty-range semantics, dispatch authorization fields,
  Node lifecycle promotion/health reason extensions, and the singleton
  `asset_registry_generations.node_generation`.
- Node Register/Edit/Retire/Replace reuse Stage 1 global receipts, advisory locking,
  actor-first replay, lazy canonical intent and SecretSet-only K1. Existing Gateway canonical
  arrays and receipt behavior are unchanged.
- Retire/Replace use one DB boundary for lifecycle, monitoring, binding, eligible poll-run,
  generation, audit and receipt updates. An authorized running attempt may finish historical
  evidence, while the lifecycle-first promotion fence prevents every current-truth update.
- Scheduler, claim, dispatch authorization, reconciler, finalize/promotion and current reads
  compose active Node and monitoring eligibility. Migration 34 preserves the existing bounded
  history-retention delete gate while adding dispatch authorization guards.
- API/UI expose only Node lifecycle operations and history. Node Health, Connection Test and
  Monitoring Enable/Disable remain absent for Stage 3.

## Runtime Acceptance matrix

| Area | Result | Evidence |
|---|---|---|
| compatibility class 2 / floor 2 | PASS | PG18 migration marker/version assertion and compatibility gate tests |
| class 1 artifact against floor 2 | REJECTED AS REQUIRED | `TestSupportedWrapperAgainstFloorTwoPG18` |
| Node migration / ACL / constraints | PASS | `TestNodeAssetLifecycleMigrationPG18` on PostgreSQL 18.6 |
| Node lifecycle / revision / lineage | PASS | `TestNodeLifecycleCommandsReplayAndLineagePG18` |
| concurrent lifecycle serialization | PASS | `TestNodeLifecycleConcurrentSerializationPG18` |
| shared receipt / replay / K1 | PASS | Node canonical fixtures plus lifecycle replay, actor, key and intent cases |
| monitoring cancellation / empty range | PASS | Replace acceptance asserts current close, future durable cancellation and empty range |
| binding close and lifecycle fence | PASS | `TestRelayBindingRepository_NodeLifecycleClosesAndFencesBinding` |
| scheduler / claim | PASS | retired pending work becomes durable `abandoned(node_retired)` with no claim |
| dispatch authorization / lease / grace deadline | PASS | absolute deadline uses authorization-start time; delayed return, exhausted budget, and lease/grace/request-timeout minima pass |
| authorized-running lifecycle race | PASS | Replace leaves authorized attempt running until lifecycle-aware reconciler/finalize |
| reconciler lifecycle handling | PASS | expired authorized old identity becomes `abandoned(node_replaced)` and clears fences |
| promotion lifecycle/monitoring fence | PASS | PG18 run/Provider consistency, both Node-lock orders, natural expiry, and precedence acceptance |
| provider-health/current truth exclusion | PASS | PG18 baseline pointer/health/account/availability/request-quality truth remains unchanged |
| node_generation / read_as_of | PASS | API PG18 cursor chain crosses time boundary, then becomes stale after real mutation |
| list/history/counts compatibility | PASS | default active list, historical detail, `node_counts`, legacy total semantics |
| Node API / security | PASS | authenticated API integration covers CSRF, no-store, replay and Secret redaction |
| Node lifecycle UI | PASS | production-built authenticated Playwright lifecycle flow |
| Stage 3 controls absent | PASS | Playwright asserts no Node Health/Connection Test/Monitoring controls |
| audit / metrics | PASS | Node-specific fixed success/replay/invalid/conflict/unavailable metric taxonomy tests |
| restart / recovery | PASS | durable cancellation, receipt replay and old-work reconciliation cases |
| generated artifacts | PASS | `make generate` completed twice without a second generated delta |
| broader tests | PASS | `make test` |
| production build | PASS | `make build` |

## Previously executed evidence

The following evidence predates the latest correction round and remains valid. Tasks 7, 17a, 17b,
19-23, 32, 32a, 33, 48 and 58-60 were reopened by independent review and are complete again after
their affected implementation and acceptance reruns.

- PostgreSQL: `PostgreSQL 18.6`; a fresh database migrated in order from 1 through 34.
- Stage 2 isolated PG18 selectors: PASS for migration, lifecycle/replay/lineage,
  concurrency, binding close, retired claim, late finalize/promotion, API and compatibility
  wrapper cases.
- Historical retention selectors: PASS after composing migration 9's controlled retention gate
  into the migration 34 poll-run guard.
- `deploy/acceptance/relay-control-compat-gate.sh`: PASS.
- Node authenticated Playwright E2E: 1 passed.
- `make test`: PASS; Go packages PASS, frontend 29 files / 192 tests PASS, typecheck PASS.
- `make build`: PASS; production Vite build and `go build ./cmd/control` PASS.
- MODIFIED exact-title comparison: PASS (25 requirements).
- Stage 1 + Stage 2 overlapping semantic composition: PASS.
- `openspec validate add-relay-node-asset-lifecycle-management --strict`: PASS.
- `openspec validate add-relay-node-management-operations --strict`: PASS (read-only regression).
- `openspec validate --all --strict`: 28 passed / 0 failed.
- `git diff --check`: PASS.

## Current correction acceptance

- PostgreSQL 18.6 Stage 2 lifecycle selectors: PASS, including capability ACL/immutability,
  monitored mutation projection/replay, `monitoring_ineligible` evidence-only finalize, natural
  expiry, and both finalize-vs-monitoring Node-lock orders.
- Dispatch absolute deadline unit acceptance: PASS.
- Node metric taxonomy unit acceptance: PASS.
- `make generate` twice: PASS with identical worktree diff digest after the second run.
- `make test`: PASS; Go packages, frontend 29 files / 192 tests, and TypeScript typecheck pass.
- `make build`: PASS after placing Go caches under `/Volumes/DevRAM`; production Vite and Control
  binary builds pass.
- OpenSpec strict validation and `git diff --check`: recorded by the final validation below.

## Remaining gate

Task 61 is complete. Independent implementation re-review passed, approved-scope reconciliation
and Runtime Acceptance evidence are complete, and implementation commit
`ff36a39a8b41db52b84a1e3966a5f84c81c23b1d` was followed by a clean-worktree check, strict
OpenSpec validation (28 passed / 0 failed), and `git diff --check` PASS. The archive-readiness P2
evidence inconsistency is FIXED; archive readiness is READY FOR INDEPENDENT ARCHIVE-READINESS
RE-REVIEW, whose independent disposition remains pending. No archive command was run.
