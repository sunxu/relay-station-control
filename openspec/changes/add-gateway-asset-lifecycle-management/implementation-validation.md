## Implementation validation

Validation date: 2026-09-13 (Asia/Shanghai)

- implementation baseline: `5012757753c6392d8d1ba3d3c9d5a7e2c479cd19`
- change: `add-gateway-asset-lifecycle-management`
- implementation: COMPLETE
- Runtime Acceptance: PASS
- independent architecture re-review: PASS (P0=0 / P1=0 / P2=0)
- independent implementation review: PASS (P0=0 / P1=0 / P2=0)
- archive: NOT RUN
- git add / commit / push: NOT RUN
- Stage 2 implementation: NOT STARTED
- Stage 3 implementation: NOT STARTED

本轮只实施 Stage 1 Gateway/shared foundation。生产修改覆盖 Migration 33、shared command receipt/canonical intent/K1、compatibility gate/wrapper、Gateway lifecycle/read model/API/UI、binding closure、Directory lifecycle fences、Gateway management probe、bounded audit/metrics 及对应测试；未实现 Node lifecycle、Node monitoring cancellation、node_generation、poll dispatch authorization 或 Stage 3 Node operations。

## Runtime Acceptance matrix

以下保留未受影响的既有验收结果。上一轮 Independent implementation review 的 3 个 P1 与 2 个 P2 已修复；shared receipt/K1 P1已由contract simplification解决planning gap，Tasks 9/11已实现且受影响Runtime Acceptance已重跑通过。

| Gate | Result | Evidence |
|---|---|---|
| Stage 1 migration | PASS | PostgreSQL 18 isolated databases applied migrations through 33; invalid legacy HTTPS/path-bearing Gateway targets failed atomically at version 32 and left no compatibility marker. |
| Gateway PK/FK proof | PASS | PostgreSQL 18 verified physical `PRIMARY KEY(instance_id)`, six direct/lineage FKs backed by the PK, nullable singleton slot, exact final indexes, temporary unique removal, floor 1, failed-migration rollback, and migration waiting for a concurrent Gateway reader before completing. |
| receipt/replay | PASS | Transaction advisory lock and receipt lookup now precede lazy intent construction. Actor mismatch wins before endpoint/Secret/K1/revision/lifecycle checks; matching actors use receipt-recorded kind, encoding and key version, replay persisted status/body, and reject unknown encoding with 503. Same-command concurrency, rollback and API error-order tests passed. |
| canonical intent / K1 | PASS | K1 is loaded only for SecretSet. Missing required K1 returns 503 with zero mutation/receipt/audit; non-SecretSet commands and NULL-key receipts run/replay without K1. Correct K1 replays, different Secret or a structurally valid replacement K1 returns 409, and restoring the historical K1 restores replay. File safety/length tests passed. |
| compatibility gate | PASS | Linux `execveat(AT_EMPTY_PATH)` test proved pathname swap cannot replace the verified file object; OCI mode verified the signed immutable image manifest digest before Docker; bad signature, digest, metadata and unavailable DB fail closed. |
| mandatory wrapper | PASS | PostgreSQL 18 wrapper acceptance covered migration/marker consistency, class0 rejection, Linux same-FD class1 launch, mutable OCI tag rejection, wrong digest rejection and exact digest-qualified Compose launch. |
| class0 against floor1 | REJECTED | Exact Control `5e2caeb031a47510744cada56978b963da39b4e9` artifact, signed as class0, exited 78 with `compatibility_floor_rejected`; floor and durable row counts were unchanged. |
| class1 startup | PASS | Rebuilt Stage 1 artifact, signed as class1, started only through the mandatory wrapper and returned `{"status":"ok","version":"dev"}` from `/api/healthz`. |
| Gateway lifecycle | PASS | Register/Edit/Retire/Replace, revision conflicts, current-slot contention, terminal retirement, zero-current state, replay and rollback integration/API tests passed. |
| replacement lineage | PASS | A valid replacement was retained; self/cycle/fork/merge constraints and UPDATE/DELETE/TRUNCATE immutability were exercised on PostgreSQL 18. |
| binding races | PASS | Both lock serializations for Retire/Bind and Replace/Rebind passed; lifecycle-first conflicted and binding-first was closed with the required reason; no retired Gateway retained a current binding. |
| Directory lifecycle fences | PASS | Pre-fetch and pre-promotion lifecycle wins persist terminal non-retryable `gateway_retired`/`gateway_replaced`; zero-current scheduling, promotion-first history, independent replacement freshness and row-lock serialization passed. |
| HTTP-only transport | PASS | Directory and Gateway management clients accepted fixed HTTP targets; HTTPS was rejected before outbound; no TLS, redirect, retry, fallback, arbitrary method/path, or credential-bearing probe branch was introduced. |
| Health / Connection Test | PASS | Exact API routes executed fixed `GET /health`, returned bounded observations, wrote sanitized distinct audits/metrics, and did not mutate lifecycle/revision/Directory truth or create command receipts. |
| audit / metrics / security | PASS | Real mutations wrote exactly one bounded transition audit; replay wrote none; fixed low-cardinality metrics, 401/403/CSRF/no-store/body bounds and sanitized errors passed. |
| history / pagination / counts | PASS | Current/list/detail/lineage routes, UUID ordering, cursor scope/tamper/generation fences, stable two-page traversal, legacy total counts and additive active/retired/total counts passed. |
| Secret validation / exact API shape | PASS | Application and PostgreSQL validators accept the same opaque-reference language; HTTP(S), whitespace/control and `?#@` reject before mutation. Active retirement fields, detail lineage fields and list cursor are required nullable fields and serialize explicit null. |
| Gateway UI/API E2E | PASS | Production embedded UI authenticated flow covered Edit, stale-revision feedback, Health, Connection Test, Replace, Retire, Register, history/detail, Control-only requests and Secret non-disclosure. |
| restart/recovery | PASS | Receipt replay after repository restart, persisted lineage/history, compatibility restart gate and migration rollback evidence passed. |
| production build | PASS | Vite production build emitted the lazy `AssetsPage` chunk under the archived `/static/` namespace and embedded it in the rebuilt Control binary. |
| broader verification | PASS | `make test` and `make build` completed successfully. |

## Commands and actual results

- `make generate`: PASS; Go/sqlc/TypeScript generation completed and the final generated sources are reproducible.
- `make test`: PASS; all Go packages, 29 Vitest files / 191 tests, and TypeScript typecheck passed.
- `make build`: PASS; production Vite build and Control binary build completed.
- PostgreSQL 18 targeted Stage 1 suite: PASS for Store, API and compatibility packages.
- `deploy/acceptance/relay-control-compat-gate.sh`: PASS.
- `npm run test:e2e -- e2e/gateway-management.spec.ts`: PASS, 1/1.
- `openspec validate add-gateway-asset-lifecycle-management --strict`: PASS.
- `openspec validate --all --strict`: PASS, 27 passed / 0 failed.
- `git diff --check`: PASS.

## Independent implementation review fixes

- P1-1 artifact identity: FIXED. Bare/systemd Linux hashes and executes the same opened file description; unsupported descriptor-exec platforms fail closed. Compose verifies a signed immutable OCI manifest digest before container creation and fixes the verified overlay last.
- P1-2 marker + migration floor: FIXED. The read-only gate jointly validates Goose migration 33 and the singleton marker; migration/marker missing or inconsistent states exit 78. PostgreSQL 18 acceptance covers pre-33/no-marker floor0, migration33 missing/malformed marker, pre-33 marker mismatch, floor1 class0 rejection and valid floor1 class1.
- P1-3 Directory taxonomy: FIXED. Migration, state shape, SQL/generated queries, Go mapping, metrics and race acceptance use terminal non-retryable `gateway_retired` and `gateway_replaced`; missing/corrupt identities remain `contract_invalid`.
- P2-1 Secret validator: FIXED. A shared application validator mirrors `public.control_valid_secret_reference`; API integration proves invalid values leave zero Gateway rows, receipts and transition audits.
- P2-2 nullable response shape: FIXED. OpenAPI and generated Go/TypeScript clients require nullable retirement, lineage and cursor fields; raw API tests assert field presence and explicit null.
- Affected PostgreSQL 18 Store/API/compatibility tests: PASS.
- Linux descriptor execution and mandatory wrapper acceptance: PASS.
- Generated-client reproducibility: PASS.
- Task 9 PostgreSQL 18/API acceptance: PASS. Actor mismatch beat invalid endpoint, invalid Secret, missing K1, stale revision and retired target; no extra Gateway, receipt or transition audit was created. Receipt kind mismatch returned 409 and unknown encoding returned 503 before domain-state evaluation.
- Task 11 PostgreSQL 18/unit acceptance: PASS. Non-SecretSet Edit/Clear/Retire and NULL-key-version replay succeeded without K1; required missing K1 returned 503 with zero side effects; correct K1 replayed, different Secret and structurally valid replacement K1 returned 409, and restoring K1 restored replay. Regular-file, symlink, permissions and exact-length checks passed.
- Affected same-command concurrency/restart replay suite: PASS.
- Affected full verification rerun: `make test` PASS（Go all packages; 29 Vitest files / 191 tests; TypeScript typecheck）; `make build` PASS（production Vite and Control binary）.

## K1 contract simplification disposition

- Final review result: P0=0 / P1=0 / P2=0. Independent architecture re-review = PASS. Independent implementation review = PASS.
- Actor-first receipt lookup and lazy canonical-intent construction are implemented and verified. Task 9 = COMPLETE. Task 11 = COMPLETE.
- Existing durable truth records only `secret_fingerprint_key_version=1`; the protected key loader validates regular-file safety, permissions and exact 32-byte length, but does not authenticate the loaded bytes as the historical K1 version 1.
- Architecture Review rejected signed K1 identity/digest anchors and extra deployment trust metadata. Stage 1 now accepts that a structurally valid replacement K1 is indistinguishable from the historical K1.
- The corrected contract requires K1 only for SecretSet. Missing or structurally invalid required K1 returns 503; any canonical hash successfully computed with a structurally valid K1 either replays on equality or returns 409 command_conflict on mismatch. Restoring the correct historical K1 restores replay.
- This trade-off preserves fail-closed durable correctness while accepting less precise diagnosis. It does not alter the receipt schema, migration 33, compatibility class 1 or floor 1.
- K1 identity architecture gap = RESOLVED BY CONTRACT SIMPLIFICATION. K1 architecture decision = COMPLETE. Independent architecture re-review = PASS（P0=0 / P1=0 / P2=0）. Planning readiness = PASS / READY. Implementation readiness = READY.
- Tasks 9 and 11 are implemented. PostgreSQL 18 Store/API acceptance, canonical fixture and key-file tests, same-command concurrency, `make test`, and `make build` passed. The accepted structurally-valid replacement-key trade-off remains 409 command_conflict and no identity anchor was added.

## Remaining gate

Tasks 9 and 11 are complete; 41 of 42 implementation tasks are checked. Task 42 is unblocked but remains unchecked: implementation evidence and durable-truth reconciliation are complete, while Git/worktree closeout awaits explicit authorization. Task 42 closeout evidence reconciliation = COMPLETE；Git/worktree closeout = PENDING EXPLICIT AUTHORIZATION；Archive readiness = PENDING GIT CLOSEOUT。The worktree intentionally retains the uncommitted Stage 1 implementation. No archive, staging, commit or push action was run.
