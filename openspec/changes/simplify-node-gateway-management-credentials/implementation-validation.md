# Phase 8 Stage 0 Implementation Validation

Implementation authorization:
GRANTED

Authorization scope:
Phase 8 Stage 0 only

Ops authorization baseline:
7f4c35c49ac8e1d2200a8567d86674ca495ef964

Control implementation baseline:
cff64ea681aff5eaaba6a9a7785a59aecc108731

Validated pre-Stage0 runtime provenance:
fab6aadc36a9f8ebe1309e5db457dcbac0136880

Current implementation status:
IN PROGRESS

Migration 00051:
NOT STARTED

Runtime acceptance:
NOT STARTED

Stage0 class-4 artifact:
NOT BUILT

## Gate 1 — K2 / Crypto Foundation

Tasks 1.1–1.5 were implemented as narrow internal foundation primitives with
tests first. No product workflow is wired to these primitives yet.

| Area | Result | Evidence |
|---|---|---|
| K2 structural loader matrix | PASS | `internal/assetcredential/k2_test.go`: missing, open failure, explicit valid-target symlink rejection, non-regular, 31/32/33 bytes, 0400/0600, unsafe mode, wrong owner; one-load/no-hot-reload behavior |
| K2 provisioning primitive | PASS | injected-reader create, repeat byte preservation, atomic failure, and OS-CSPRNG entry point; operator/deployment path is provided by `cmd/relay-control-asset-credential-key`, the Makefile target, and acceptance runtime wiring |
| K2 commitment derivation/state | PASS | exact SHA-256 domain formula; absent/match/mismatch/unavailable states without value-bearing evidence |
| AES-256-GCM and exact AAD | PASS | injected 12-byte nonce sequence; Node/Gateway domain and binary UUID binding; wrong key, cross-asset, cross-kind, tampered, truncated and too-short rejection |
| Startup warning and secret scanner primitives | PASS | one sanitized warning, production startup degradation integration, and test-only name-only scanner tests for plaintext, raw K2, sealed blob, commitment, credential-bearing header, and native body |

Final corrective review findings:

| Finding | Result |
|---|---|
| K2 fd lifecycle | PASS — every successful `unix.Open` has one close path; ownership transfers exactly once to `os.File` only after validation |
| Exact K2 permission mode | PASS — raw permission and special bits are checked together; only `0400` or `0600` are accepted |
| Symlink/no-follow | PASS — `O_NOFOLLOW` plus explicit valid-target symlink test |

Gate 2 and later work remain out of scope for this validation.

## Verification

The focused Gate 1 package verification passed:

```text
go test ./internal/assetcredential -count=1: PASS
git diff --check: PASS
```

The Gate 1 package, repository-wide Go regression, frontend build/test,
`go vet`, and `make test build` all passed. OpenSpec strict validation passed
for the change and all 31 repository items. Current status remains
`IN PROGRESS` because the worktree is intentionally uncommitted; Gate 6
Ops/Recovery evidence is recorded below.

## Gate 2 — Protected DB State / Compatibility 4/4

Gate 2 implementation artifacts were added for migration 00051, protected
owning-row state, the write-once K2 identity commitment initializer, and
compatibility class/floor 4. The generated SQL model was refreshed through
the repository's normal generation command.

The Gate 2 owning PostgreSQL proofs were run in the isolated PostgreSQL 18.6
environment. The long full-store suite is intentionally deferred to Stage 0
unified review.

| Area | Result | Evidence/status |
|---|---|---|
| Migration 00051 and legacy hard guard | PASS | real PostgreSQL owning tests |
| Protected sealed columns and structural minimum | PASS | real PostgreSQL owning tests |
| ACL isolation and `secret_configured` projection | PASS | real PostgreSQL owning tests |
| K2 commitment initialization, mismatch, and invalid sealed state | PASS | real PostgreSQL owning tests |
| K2-A/K2-B database initialization race | PASS | same-key and mixed-key real PostgreSQL concurrency proof |
| Compatibility class 4 / migration 51 floor coherence | PASS | unit and real PostgreSQL O05 gate path |
| O01–O04 | PASS (unit/owning) | exact class/floor and gate checks |
| O05 | PASS | real PostgreSQL compatibility-admission observer |

Repository-wide Go tests, `go vet ./...`, `make test build`, and strict
OpenSpec validation passed after the Gate 2 source changes. Overall
implementation remains `IN PROGRESS` because unified review and the final
class-4 artifact are still pending; supported provisioning, startup
composition, runtime cutover, API/frontend changes, and Gate 6 evidence are
recorded in later sections.

## Migration support policy and Gate 2 regression update

Stage 0 is a pre-deployment fresh-install target. The supported database path is
an empty database through migrations 00001–00051 to the latest schema. The
version-50 fixture used by Migration 00051 tests is a test-only forward safety
setup, not a supported deployed-database upgrade path. Historical latest-to-down, migration
rollback/round-trip compatibility, upgrades from an old deployed schema, and
current generated queries against historical schemas are outside the supported
Stage 0 matrix. Historical migration SQL remains retained and immutable;
Migration 00051 remains forward-only.

Pure historical rollback tests and their obsolete acceptance allowlists were
removed. Current-schema ACL, security, and runtime behavior tests remain on the
latest schema. Migration 00051 safety proofs remain focused on fresh install,
50-to-51 application, legacy hard guards, protected state shape and ACL,
`secret_configured`, commitment initialization and races, invalid sealed state,
and commitment immutability.

Gate 2 owning verification in the isolated PostgreSQL 18.6 environment:

```text
go test ./internal/store -run 'Stage0Migration51|Compatibility|Migration.*51' -count=1 -v: PASS
go test ./internal/compatgate -count=1: PASS
git diff --check: PASS
```

The full `go test ./internal/store/... -count=1` suite is deferred to Stage 0
unified review and is not counted as a pass. The repository-wide non-PostgreSQL
run passed after stale acceptance allowlist/output references for removed
historical readonly-query rollback tests were removed from the test-only
acceptance harness.

## Pre-Gate 4 historical compatibility cleanup

The supported database policy is now explicit in the active Stage 0 evidence:
`EMPTY DB -> complete migration chain -> latest`, forward-only. Tests whose
contract was an upgrade from a historical deployed schema, migration `Down`,
or migration round-trip were removed, including the old admission, durable-job,
asset-registry, account-quality, account-inventory-history, notification, and
problem-account migration fixtures. The Stage 7A real old-floor artifact test
and the history rollback CI stage/harness were removed as obsolete compatibility
scaffolding.

Current-schema ACL, security, lifecycle, and runtime behavior tests remain on a
latest-schema fixture. Migration SQL `00001` through `00051` and migration
identity/provenance checks remain unchanged. The version-50 fixture used by the
Migration 51 owning tests is a test-only forward safety setup; it does not claim
support for upgrading a deployed version-50 database.

Cleanup verification:

```text
EMPTY/current latest-schema owning test: PASS
Stage0Migration51 focused PostgreSQL: PASS
Compatgate and O01-O05: PASS
go test ./... -count=1: FAIL (retained Gate 4/runtime and existing integration findings)
go vet ./...: PASS
make test build: FAIL in test phase on the same retained findings
make build: PASS
OpenSpec change strict: PASS
OpenSpec all strict: PASS (31/31)
git diff --check: PASS
```

The full repository failures were not caused by the removed historical matrix;
they include API tests that still exercise pre-Gate-5 credential behavior,
environment fixture setup, and long store acceptance cases with legacy reader
reference assumptions. Gate 4 runtime wiring was verified separately.

O05 now has a dedicated real PostgreSQL observer proof in
`internal/compatgate/stage0_o05_postgres_integration_test.go`: class 3 is
rejected at floor 4 and class 4 is admitted after Migration 51, while Node
sealed state, Gateway sealed state, and K2 commitment remain unchanged. The
test evidence emits only unchanged booleans and never emits protected values.

## Gate 2 closeout status

```text
Gate 2 owning PostgreSQL: PASS
Migration 00051: PASS
Protected ACL and secret_configured: PASS
K2 initialization and same/different-key races: PASS
Commitment UPDATE/DELETE/TRUNCATE immutability: PASS
O01–O04: PASS (unit/owning compatgate evidence)
O05: PASS (real PostgreSQL compatibility-admission observer)
Full PostgreSQL store regression: DEFERRED TO STAGE0 UNIFIED REVIEW
```

Overall implementation remains `IN PROGRESS` because unified review and the
final class-4 artifact are still pending. Gate 3A, Gate 4, Gate 5, and Gate 6
implementation evidence is recorded in this document.

## Gate 3A / Stage B — Repository Cutover and Store Credential Semantics

Stage B keeps real K2 file loading and authenticated outbound Open for later
stages. The lifecycle repositories now receive a separate credential sealer
capability from the existing K1 intent key. When no sealer is supplied, the
constructor uses an explicit unavailable capability and never falls back to
`reader_secret_ref`.

| Area | Result | Evidence/status |
|---|---|---|
| Credential sealer capability and K1/K2 separation | PASS | separate capability and constructor argument |
| Explicit test sealers | PASS | deterministic, unavailable, counting and failing test-only sealers |
| Exact plaintext credential validation | PASS | byte length, UTF-8, NUL/CR/LF and exact-byte tests |
| Node Register/Edit/Retire/Replace cutover | PASS | real PostgreSQL lifecycle/replay tests |
| Gateway Register/Edit/Retire/Replace cutover | PASS | real PostgreSQL lifecycle/replay tests |
| Current `reader_secret_ref` persistence | ZERO | no current Node/Gateway lifecycle write path |
| Direct owning-row lifecycle writes | ZERO | fixed Migration 51 functions are the owning mutation boundary |
| Standalone protected writer calls | ZERO | Migration 51 A9 surface proof remains green |
| K2-unavailable Keep/Clear/Retire/unconfigured Replace | PASS | explicit unavailable-sealer store matrix |
| Command transaction rollback under downstream fault | PASS | real PostgreSQL audit-trigger fault injection preserves asset revision/sealed state and rolls back registry, receipt, and audit |
| Retire/Replace erase and lifecycle closure | PASS | Node/Gateway lifecycle, lineage, monitoring/binding closure, blocker and race integration suites |
| K2-unavailable repository operation matrix | PASS | Node/Gateway repository matrix plus recovery zero-outbound proof; Set and credential-bearing Replace fail closed |
| Set failure with unavailable sealer | PASS | explicit fail-closed unit proof |
| Actor/replay ordering and zero-Seal replay/conflict | PASS | focused Node/Gateway command regression; counting sealer is available for direct call-count assertions |
| Stage 0 Migration 51 and compatgate regression | PASS | fresh PostgreSQL focused suite and compatgate suite |
| Runtime Open consumers | PASS | Gate 4 resolver, outbound, fence, and zero-outbound evidence |
| Real K2 startup composition | PASS | Gate 3A Stage C production composition evidence |

The long Directory outbound acceptance test was migrated to the Stage 0
resolver during Gate 4. API/frontend and Ops evidence is recorded in the
later Gate 5 and Gate 6 sections; the worktree remains uncommitted.

## Gate 3A / Stage C — Production K2 Composition

`cmd/control` now verifies the Control environment after the database pool is
ready, then loads `CONTROL_ASSET_CREDENTIAL_KEY_FILE`, initializes/checks the
Gate 2 commitment, and injects the resulting narrow sealer capability into the
Node and Gateway lifecycle repositories. Invalid or unavailable credential
state produces one sanitized warning and an explicit unavailable capability;
the Control process remains startable. The runtime Open consumers and
`FileSecretResolver` is removed from the current production credential path;
the remaining utility/test references are outside that path.

| Area | Result | Evidence |
|---|---|---|
| K2 startup composition and ordering | PASS | `cmd/control/main.go`, focused composition tests |
| K2 loaded once / no hot reload | PASS | `assetcredential.KeyLoader` composition and Gate 1 loader proof |
| Representative invalid-file degradation | PASS | missing and invalid K2 startup tests |
| Commitment initialization/match/mismatch contract | PASS | real PostgreSQL Stage0 commitment/race suite and startup status mapping |
| Invalid DB state fail-closed | PASS | real PostgreSQL Stage0 invalid-state proof and unavailable composition |
| Production AES-256-GCM sealer / exact AAD | PASS | frozen-AAD round trip and cross-kind rejection test |
| Sanitized startup warning | PASS | warning contains no path, K2, commitment, blob, or credential value |
| K1/K2 separation | PASS | independent intent-key and sealer constructor capabilities |
| Runtime key generation / hot reload / rotation | ZERO | no startup generation, refresh, or rotation path |
| Stage0 Migration 51 / O05 / compatgate | PASS | fresh focused PostgreSQL and compatgate verification |
| Gate 4 runtime Open consumers | NOT STARTED | intentionally deferred |

## Gate 3A final status

```text
Stage A — fixed lifecycle mutation functions: PASS
Stage B — repository cutover and store credential semantics: PASS
Stage C — production K2 composition: PASS
Gate 4 runtime Open cutover: NOT STARTED
Overall implementation: IN PROGRESS
Commit: NOT CREATED
Push: NOT RUN
```

## Gate 4 — Runtime Credential Open Cutover (PASS)

## Pre-Gate 5 Test Suite Consolidation

The supported Stage 0 test matrix keeps current sealed-credential, lease/fence,
zero-outbound, recovery, redaction, and environment-identity proofs. Tests whose
only contract was reader-reference initialization, mapping-file production
wiring, legacy reader claim timing, or old reader-reference concurrency were
removed. The `FileSecretResolver` utility remains covered only where it is an
isolated helper; it is not a current Control production credential contract.

Gate 5 API credential fixtures remain deferred to Gate 5 and were not changed.
The environment identity integration test remains a current invariant. Its
real PostgreSQL fixture now bootstraps the current environment singleton through
the owner connection before the restricted runtime read; the missing-singleton
negative contract remains covered separately.

Consolidation evidence:

```text
KEEP: Stage0Migration51, compatgate/O01-O05, protected/fenced Directory,
      current Node/Inventory, lease/recovery, zero-outbound, redaction,
      environment identity contract
MERGE: store Directory fixtures now use explicit static test resolver; driver
       transport/worker fixtures use explicit Stage0 asset resolver
DELETE: reader-reference mutation/rejection/concurrency/history tests,
        legacy mapping runtime fixture, mapping-based local tooling/canary
        acceptance
DEFER: Gate 5 API credential fixtures and OpenAPI credential wiring
  - TestGatewayAssetHTTPRoutesAndMutationSecurityPG
Environment identity fixture: PASS on real PostgreSQL, not skipped
Gate 4 owned failures: ZERO
Environment/current-schema failures: ZERO
Unknown failures: ZERO
```

The production `cmd/control` wiring now passes a Stage 0 asset resolver to the
Node driver, Phase 7 account-operation resolver, and Gateway Directory service.
Node authenticated inventory, usage-queue, and identity requests use the
protected Node read plus K2 Open. Gateway Directory uses the fenced protected
read bound to run, Gateway UUID, lease fencing token, active lifecycle, current
singleton, and unexpired running lease.

```text
Node/Gateway driver package tests: PASS
Node account-operation resolver wiring: PASS
Gateway Directory main/process acceptance: PASS
Stage0Migration51 PostgreSQL: PASS
Compatgate: PASS
go test ./... -run '^$': PASS
go vet ./...: PASS
make build: PASS
git diff --check: PASS
```

Gate 4 final disposition:

```text
Runtime sealed credential Open cutover: PASS
FileSecretResolver production dependency: ZERO
reader_secret_ref runtime credential dependency: ZERO
mapping-file runtime credential dependency: ZERO
Node authenticated outbound: PASS
Gateway Directory fenced outbound: PASS
K2 unavailable / corrupt / wrong-AAD zero-outbound: PASS
Phase 7 runtime regression: PASS
Gate 4 focused: PASS (~13–16s)
Full internal/store: PASS (232.790s)
GATE4: PASS
```

Gate 4 progress since the initial cutover:

```text
Node driver authenticated inventory/usage/identity: PASS
Node account-operation resolver wiring: PASS
Gateway fenced Directory resolver: PASS
Gateway Directory main-process acceptance: PASS
Gateway Directory process recovery: PASS
Gateway Directory same-slot competition: PASS
Stage0Migration51 and compatgate focused regression: PASS
Production cmd/control legacy credential scan: ZERO
```

The full store timing sample was executed once with the real PostgreSQL test
URLs. It took 554.6 seconds and exposed slow acceptance/bootstrap cost rather
than a Gate 4 production failure. The daily commands are now separated into:

```text
fast: scripts/test-store-fast.sh
Gate 4 focused: scripts/test-gate4-focused.sh
slow Directory process acceptance: scripts/test-gate4-slow.sh
full closeout: go test ./internal/store -count=1
```

The same-slot competition test originally waited on the real 180-second slot
and 120-second claim window, which produced a 105.7-second wall-clock wait.
The test now injects a logical PostgreSQL time only into its child test
connections. The scheduled slot remains production-aligned, the lease fields
continue to use real database time, and the two processes still race for one
row. Three consecutive runs completed in 1.16s, 1.28s, and 1.25s of test time.

The production main-process and process-recovery acceptance tests also contain
deliberate real slot waits, so they were moved with same-slot competition to
the slow process-acceptance script. The focused Gate 4 corrective group keeps
their current-schema/fence and credential owning coverage without repeating
those wall-clock waits. Current unique invariant coverage was not removed; the
full store command remains a closeout/unified-review check and is not part of
the corrective loop.

PostgreSQL fixture optimization:

```text
immutable per-process template database: IMPLEMENTED
default latest-schema isolated fixtures: template clone
migration-owning fixtures with version arguments: raw migration path
environment singleton in template: NO
K2 commitment or sealed credential in template: NO
```

The complete store package was run after the current fixture corrections.
Wall time decreased from the 554.6-second historical sample to 232.790
seconds and passed. The pre-migrated immutable template database remains in
use for latest-schema fixtures; raw empty-database migration paths remain in
place for Migration 51 and fresh-install ownership proofs. The remaining
fixture/bootstrap cost is a non-blocking test-infrastructure backlog.

The Gate 5 API request and generated/frontend credential contract is now wired
through `management_credential` and `directory_credential`. Register and
Replace reject explicit null, Edit maps null to Clear, and omitted Edit fields
remain Keep. Generated Go and TypeScript outputs were refreshed with
`make generate`; the Node and Gateway HTTP owning tests pass, including
credential response non-reflection and Set/Clear/Replace cases. The Asset
Registry forms expose explicit Keep/Set/Clear behavior and send omitted,
string, or null properties accordingly. `go test ./... -count=1 -failfast`,
frontend unit tests, typecheck, and build pass.

The OpenAPI write schemas are optional and nullable. The generated TypeScript
models preserve `undefined`, string, and `null`, and the frontend request
builders are covered for all three states. The current oapi-codegen Go output
uses `*string`, which cannot distinguish omitted from explicit null; the raw
JSON HTTP decoder is therefore the owning parser for Go request presence/null
semantics, while the generated Go types remain schema-compatible and are not
used as the tri-state parser. No generated Go nullable representation is
claimed by this evidence.

The representative Playwright Gateway/Node scenarios were executed with the
Playwright bundled Chromium in the approved host execution environment. The
repository Playwright configuration and test source were unchanged, and no
unsafe Chromium flags were used. Both owning lifecycle scenarios passed. The
Problems scenario also passed as baseline evidence; the Topology readonly
fixture remains a non-Gate-5 fixture bug, and Authentication remains blocked
only by its missing environment input. Neither unrelated result is counted as
a Gate 5-owned failure.

```text
Browser execution provenance: PASS
Official browser: Playwright bundled Chromium
Execution environment: host execution
Repository Playwright config: UNCHANGED
Playwright test source: UNCHANGED
Unsafe Chromium flags: ZERO
Node lifecycle Browser: PASS
Gateway lifecycle Browser: PASS
Gate 5-owned Browser failures: ZERO
Problems: PASS / baseline evidence
Topology readonly: FIXTURE_BUG / NON-GATE5-OWNING
Authentication: ENVIRONMENT INPUT MISSING / NON-GATE5-OWNING
```

Gate 4 test layers remain:

```text
fast: scripts/test-store-fast.sh
Gate 4 focused: scripts/test-gate4-focused.sh
Gate 4 slow acceptance: scripts/test-gate4-slow.sh
full store closeout: go test ./internal/store -count=1
```

## Gate 6 — Ops / Provisioning / Recovery

| Area | Result | Evidence/status |
|---|---|---|
| K2 provisioning and create-once preservation | PASS | `cmd/relay-control-asset-credential-key`, Makefile target, and `internal/assetcredential` provisioning tests; exact 32-byte OS-CSPRNG file, repeat preserves bytes, no raw key output |
| Deployment path-only wiring | PASS | acceptance Compose secret-init read-only installation, `CONTROL_ASSET_CREDENTIAL_KEY_FILE` injection, and runtime bootstrap wiring |
| Same-K2 / wrong-K2 / missing-K2 recovery matrix | PASS | `cmd/control/asset_credential_recovery_integration_test.go` (`TestStage0CredentialRecoveryK2MatrixPG`); mismatch/missing remain unavailable and commitment remains unchanged |
| Backup/recovery unit and operator runbook | PASS | `docs/runbooks/asset-credential-key.md`; `TestStage0CredentialBackupRestoreSameK2PG` performs real PostgreSQL 18 `pg_dump`/`pg_restore` into a fresh database, supplies the same K2 bytes at a different path, opens restored Node/Gateway credentials, and proves a Gateway Directory fenced authenticated outbound; host migration, container recreation, restart-required and K2-loss semantics are documented |
| Secret hygiene | PASS | deployment/static scans show no raw K2 literal in image/build/env surfaces or test output |

Gate 6 implementation evidence is complete. `go test ./... -count=1`,
`make test build`, `go vet ./...`, both strict OpenSpec validations, and
`git diff --check` passed. The full Go run used the real PostgreSQL test URLs;
no production K2, database, or API secret was printed.

## Current Stage 0 Review Status

```text
Gate 3A: PASS
Gate 4: PASS
Gate 5: PASS
Gate 6: PASS
Gate 5 owning Browser evidence: PASS
Official browser: Playwright bundled Chromium
Browser execution: host execution
Gate 5-owned Browser failures: ZERO
Topology readonly: FIXTURE_BUG / NON-GATE5-OWNING
Authentication: ENVIRONMENT INPUT MISSING / NON-GATE5-OWNING
Unified review: PENDING RE-REVIEW
Stage 0 class-4 artifact: NOT BUILT
Commit: NOT CREATED
Push: NOT RUN
```
