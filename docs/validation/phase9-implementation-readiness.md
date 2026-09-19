# Relay Station Phase 9 — Implementation Readiness

> Status: **READY FOR IMPLEMENTATION AUTHORIZATION**  
> Mode: **READ_ONLY PLANNING EVIDENCE**  
> Phase 8: **CLOSED / PASS**  
> Phase 9 planning: **COMPLETE / FROZEN**  
> Phase 9 implementation: **NOT AUTHORIZED**  
> Next gate: **DOCUMENTATION GIT CHECKPOINT → EXPLICIT IMPLEMENTATION AUTHORIZATION**

## 1. Authoritative state

Canonical plan:

```text
docs/validation/phase9-plan.md
```

Accepted state:

```text
PHASE9_PLAN = FROZEN
P0 = 0
P1 = 0
OPEN_ARCHITECTURE_QUESTIONS = NONE
```

Validated implementation-planning baselines:

```text
Formal Phase 9 Control comparison baseline
e9e0d97dcbad82595e7d4b323d8cbb4a537434e8

Manifest inspection Control revision
98e7996de1c72c59fd9189f0cf137e4ecd23d7a1

Phase 8 closed Control HEAD
93a7691305bb6b0f663045d189cf79790f8a9db1

relay-station-ops
7f4c35c49ac8e1d2200a8567d86674ca495ef964

relay-station-node-cliproxyapi
0b34a22fcaec392d39f710f3a8418595b491607d

relay-station-gateway
b2512a314dbeae7d3dfbb05c5253d214a7f32102
```

Validation note:

> Delta refresh is embedded in this implementation-readiness record; no standalone `phase9-delta-refresh.md` is required.


```text
e9e0d97d → 98e7996d
= 1 commit
= frontend / browser-E2E only
= no Phase 9 deployment/test-ownership impact

e9e0d97d → 93a76913
= Phase 8 closeout delta refresh PASS
= no configuration / Compose / control-init /
  Gateway / Node / Phase-9 test-ownership delta
```

Therefore the File-level Implementation Manifest and TCCR do not require refresh.

This document consolidates:

```text
A. FILE_LEVEL_IMPLEMENTATION_MANIFEST
B. PHASE9_TEST_CONTRACT_COVERAGE_REVIEW
```

No implementation is authorized.

---

# A. File-level Implementation Manifest

## 2. Stage 1A — Deployment cleanup

Frozen scope:

```text
remove gateway-proxy
remove gateway-nginx.conf
remove CONTROL_MANAGEMENT_CIDR
remove legacy secret mappings/reference wiring
remove deprecated rollout/config switches
remove obsolete proxy update/recovery/provenance logic
wire 127.0.0.1:18082 → Gateway:8080
```

| Repo | File / Symbol | Action | Primary Test Owner |
|---|---|---|---|
| ops | `dev/compose.yaml :: gateway-proxy` | DELETE | OPS_STATIC_TEST |
| ops | `dev/compose.yaml :: Gateway ports` | MODIFY | OPS_STATIC_TEST |
| ops | `dev/gateway-nginx.conf` | DELETE | OPS_STATIC_TEST |
| ops | `dev/devctl :: render_gateway_proxy_config()` | DELETE | OPS_STATIC_TEST |
| ops | `dev/devctl :: CONTROL_MANAGEMENT_CIDR` | DELETE | OPS_STATIC_TEST |
| ops | `dev/devctl :: init_runtime()` legacy Node mapping files | MODIFY | OPS_STATIC_TEST |
| ops | `dev/devctl :: gateway-directory-secret-map` | MODIFY | OPS_STATIC_TEST |
| ops | `dev/devctl :: check_stack()` | MODIFY | COMPOSE_SMOKE |
| ops | `dev/local_operations.py :: SERVICES["gateway"]` | MODIFY | INTEGRATION |
| ops | `dev/local_operations.py :: _proxy_identity()` | DELETE | INTEGRATION |
| ops | `dev/local_operations.py :: _restart_pair()` | MODIFY / rename | INTEGRATION |
| ops | proxy backup/recovery/provenance metadata | DELETE | INTEGRATION |
| ops | `dev/test_http_topology.py` | MODIFY | OPS_STATIC_TEST |
| ops | `dev/test_update_runtime.py` | MODIFY | INTEGRATION |
| ops | `dev/test_update_admission.py` | MODIFY | INTEGRATION |
| ops | `dev/test_local_operations.py` | MODIFY | INTEGRATION |
| ops | `dev/test_directory_operations.py` | MODIFY | INTEGRATION |
| ops | `dev/DEPLOYMENT.md` | MODIFY | DOC |
| ops | `dev/OPERATIONS.md` | MODIFY | DOC |
| ops | `dev/README.md` | MODIFY | DOC |
| control | `cmd/control/node_drivers.go` | MODIFY | CONTROL_RUNTIME_TEST |
| control | `cmd/control/node_drivers_test.go` | MODIFY | UNIT |
| control | `cmd/control/account_inventory_poll.go` | MODIFY | CONTROL_RUNTIME_TEST |
| control | `cmd/control/account_inventory_poll_test.go` | MODIFY | UNIT |
| control | affected acceptance env setup | MODIFY | CONTROL_RUNTIME_TEST |
| control | current runbooks describing removed flags/mappings | MODIFY | DOC |
| gateway | application source | KEEP | existing tests |

Additional decisions:

```text
CONTROL_CLIPROXYAPI_DRIVER_ENABLED
→ remove

CONTROL_ACCOUNT_INVENTORY_LIFECYCLE_ENABLED
→ remove

CONTROL_ACCOUNT_INVENTORY_POLL_MAX_NODES
→ remove

CONTROL_PROVIDER_POLICY_MUTATION_ENABLED
→ remove from normal operator surface
```

Historical / recovery SQL may retain a guarded mutation path where still needed.

---

## 3. Stage 1B — Node provenance decoupling

Target:

```text
Control:
NO exact Node version gate
NO exact Node commit gate
NO runtime artifact compatibility negotiation

Node:
NO code change

Deployment:
select reviewed artifact
acceptance proves artifact
production pins exact immutable digest
```

Primary implementation file:

```text
relay-station-control/
internal/drivers/cliproxyapi/native_account_adapter.go
```

Disposition:

| Surface | Disposition |
|---|---|
| `FrozenRuntimeVersion` | DELETE |
| `FrozenRuntimeCommit` | DELETE |
| `exactRuntimeIdentity()` | DELETE |
| exact version/commit equality in mutation snapshot | DELETE |
| artifact mismatch → new `unsupported_node_version` | DELETE |
| fixed native API paths | KEEP |
| strict schema validation | KEEP |
| management auth | KEEP |
| endpoint / transport invariants | KEEP |
| `X-CPA-VERSION` observation | KEEP |
| `X-CPA-COMMIT` observation | KEEP |

Affected Control tests:

```text
internal/drivers/cliproxyapi/native_account_adapter_test.go
internal/api/account_operations_http_integration_test.go
internal/store/account_operations_orchestration_failure_schema_integration_test.go
internal/store/account_operations_schema_integration_test.go
internal/accountadmin/service_test.go
```

Historical compatibility remains:

```text
unsupported_node_version
```

Keep its durable receipt/audit/API/UI replay support. Do not add a DB migration solely to remove the runtime gate.

Observational version/commit persistence remains unchanged.

Acceptance files to reframe as deployment provenance:

```text
deploy/acceptance/runtime/gate-b-smoke.sh
deploy/acceptance/control-auth-e2e.sh
deploy/acceptance/runtime/run.sh
```

Final constraint:

```text
NODE_SOURCE_MODIFICATION = NONE
NODE_TEST_CHANGE_REQUIRED = NO
```

---

## 4. Stage 1C — stale image/provenance cleanup

Main files:

```text
relay-station-ops/dev/compose.yaml
relay-station-ops/dev/.env.example
relay-station-ops/dev/devctl
relay-station-ops/releases/compatibility.yaml
relay-station-ops/dev/README.md
relevant current deployment / architecture docs
```

Keep:

```text
developer local source HEAD ↔ local image revision verification
```

Remove from production path:

```text
local checkout as production truth
stale local default tags
old local-tag release truth
```

Historical Phase 0 / Phase 7 artifact records remain historical.

---

## 5. Stage 2A — immutable Production Image inputs

Current Control/Gateway/Node workflows already provide:

```text
deploy-v*
→ GitHub Actions
→ amd64 + arm64
→ GHCR
→ multi-arch manifest
→ OCI source/revision
```

Therefore:

```text
GITHUB_WORKFLOW_ARCHITECTURE_CHANGE_REQUIRED = NO
```

Recommended new deployment file:

```text
relay-station-ops/production/compose.yaml
```

Target immutable references:

```text
Control    @sha256
Gateway    @sha256
Node       @sha256
PostgreSQL @sha256
Redis      @sha256
```

Production does not rebuild application images locally.

---

## 6. Stage 2B — control-secret-init convergence

Control-owned create-once secrets:

```text
auth keyring
asset credential K2
asset intent K1
account-operation intent key
bootstrap secret
```

Hard invariant:

```text
K1 != K2 != account-operation intent key
```

| Secret | Current primitive | Target creator | Persistence | Phase 9 delta |
|---|---|---|---|---|
| Auth keyring | `LoadKeyringFile` + tests | `control-secret-init` | persistent | create/preserve/conflict proof |
| K2 | `relay-control-asset-credential-key` | `control-secret-init` | persistent | wire existing create-once semantics |
| K1 | runtime validator | `control-secret-init` | persistent | new distinct create-once secret |
| account-op intent key | runtime validator | `control-secret-init` | persistent | stop per-run recreation |
| bootstrap secret | auth validator | `control-secret-init` unless pre-provisioned | persistent | generated-or-preprovisioned mode |

Recommended mount targets:

```text
/run/control-secrets/auth-keyring.json
/run/control-secrets/asset-credential-key
/run/control-secrets/asset-intent-key
/run/control-secrets/account-operation-intent-key
/run/control-secrets/bootstrap-secret
```

Rules:

```text
missing deployment-owned secret → create once
valid existing → preserve exact bytes
invalid/conflicting existing → fail
silent overwrite → forbidden
automatic rotation → out of scope
```

---

## 7. Stage 2C — control-init

Frozen architecture:

```text
NEW_LONG_RUNNING_SERVICE_REQUIRED = NO
NEW_ONE_SHOT_SERVICE_REQUIRED = 1
```

Recommended location:

```text
relay-station-control/
  cmd/relay-control-init/main.go
  internal/deploymentinit/
  internal/deploymentinit/integration_test.go
  Dockerfile
```

Same Control release artifact, dedicated init command.

Responsibilities:

```text
1. migrate
2. seed/verify environment
3. seed/verify static Driver catalog
4. seed initial Provider Policy
5. bounded deployment verify
6. exit
```

Reuse current primitives:

```text
existing migration chain
internal/environment.Validate / Verify
existing Driver constants
public.control_register_node_driver(...)
internal/store.AssetRepository
CurrentProviderPolicy()
Drivers()
public.control_activate_provider_policy_with_lifecycle(...)
public.control_reconcile_asset_registry(...)
```

Do not introduce a generic reconciler.

Current `deploy/postgres/init/001-runtime-role.sql` is dev-only. Production deployment therefore requires a production-safe first-start DB role bootstrap using supplied credentials. This is deployment wiring, not a schema migration.

Exit contract:

```text
0:
  schema current
  environment valid
  Driver catalog valid
  usable Provider Policy
  deployment verify clean

nonzero:
  fixed safe failure classification
  no product-state repair
  no secret output
```

---

## 8. Stage 2D — Compose orchestration

Target services:

```text
control-postgres
gateway-postgres
redis

control-secret-init
control-init

control
gateway
node
```

Removed:

```text
gateway-proxy
```

Target dependency outline:

```text
control-postgres healthy
+
control-secret-init completed
→ control-init
→ control

gateway-postgres healthy
+ redis healthy
→ gateway

node
→ independent native service
```

Gateway host mapping:

```text
127.0.0.1:${GATEWAY_PUBLIC_PORT:-18082}:8080
```

Gateway keeps existing embedded migration/AUTO_SETUP behavior.

No new long-running service is introduced.

---

## 9. File summary

### FILES_TO_DELETE

```text
relay-station-ops/dev/gateway-nginx.conf
```

### FILES_TO_MODIFY — core set

```text
relay-station-ops/dev/compose.yaml
relay-station-ops/dev/devctl
relay-station-ops/dev/local_operations.py
relay-station-ops/dev/.env.example
relay-station-ops/dev/test_http_topology.py
relay-station-ops/dev/test_local_operations.py
relay-station-ops/dev/test_update_runtime.py
relay-station-ops/dev/test_update_admission.py
relay-station-ops/dev/test_directory_operations.py
relay-station-ops/dev/README.md
relay-station-ops/dev/DEPLOYMENT.md
relay-station-ops/dev/OPERATIONS.md
relay-station-ops/releases/compatibility.yaml

relay-station-control/cmd/control/node_drivers.go
relay-station-control/cmd/control/node_drivers_test.go
relay-station-control/cmd/control/account_inventory_poll.go
relay-station-control/cmd/control/account_inventory_poll_test.go
relay-station-control/internal/drivers/cliproxyapi/native_account_adapter.go
relay-station-control/internal/drivers/cliproxyapi/native_account_adapter_test.go
relay-station-control/internal/api/account_operations_http_integration_test.go
relay-station-control/internal/store/account_operations_orchestration_failure_schema_integration_test.go
relay-station-control/internal/store/account_operations_schema_integration_test.go
relay-station-control/internal/accountadmin/service_test.go
relay-station-control/deploy/acceptance/runtime/gate-b-smoke.sh
relay-station-control/deploy/acceptance/control-auth-e2e.sh
relay-station-control/deploy/acceptance/runtime/run.sh
relay-station-control/deploy/acceptance/compose.yaml
relay-station-control/Dockerfile
relevant current Control runbooks/specs
```

### FILES_TO_ADD — recommended layout

```text
relay-station-ops/production/compose.yaml
relay-station-ops/production/README.md
relay-station-ops/production/control-secret-init.sh
relay-station-ops/production/test_compose_contract.py
relay-station-ops/production/test_control_secret_init.py
production-safe Control PostgreSQL role bootstrap file

relay-station-control/cmd/relay-control-init/main.go
relay-station-control/internal/deploymentinit/*
relay-station-control/internal/deploymentinit/integration_test.go
```

### PRODUCTION_FILES_UNCHANGED

```text
relay-station-gateway application source
relay-station-node-cliproxyapi application source
Control/Gateway/Node release workflow architecture
existing Control DB migrations 00001..00051
existing Gateway migration implementation
Inventory version/commit observational persistence schema
historical Phase 0 / Phase 7 evidence
```

Architecture check:

```text
NODE_FILES_MODIFIED = NONE
DB_MIGRATIONS_TO_ADD = NONE
NEW_LONG_RUNNING_SERVICE = NONE
NEW_PROXY = NONE
ARCHITECTURE_DELTA_DETECTED = NO
```

---

# B. Phase 9 Test Contract Coverage Review

## 10. Ownership principle

```text
prove each fact once
at the lowest owning layer
```

Primary owners:

```text
UNIT
INTEGRATION
DB_INTEGRATION
CONTROL_INIT_INTEGRATION
SECRET_INIT_INTEGRATION
CONTROL_RUNTIME_TEST
OPS_STATIC_TEST
COMPOSE_SMOKE
PRODUCTION_ACCEPTANCE
MANUAL_REVIEW
```

---

## 11. Contract ownership matrix

| # | Contract | Primary Owner | Gap |
|---|---|---|---|
| C01 | Removed Control rollout flags no longer affect runtime | CONTROL_RUNTIME_TEST | NONE |
| C02 | Legacy mapping / CIDR deployment surfaces absent | OPS_STATIC_TEST | NONE |
| C03 | `gateway-proxy` absent and host maps directly to Gateway | OPS_STATIC_TEST | NONE |
| C04 | Exact Node version/commit no longer gates operation | CONTROL_RUNTIME_TEST | NONE |
| C05 | Version/commit remain observational only | CONTROL_RUNTIME_TEST | NONE |
| C06 | Historical `unsupported_node_version` replay remains compatible | CONTROL_RUNTIME_TEST | NONE |
| C07 | Production images originate from release-tag GHCR workflows | MANUAL_REVIEW | NONE |
| C08 | Production Compose pins immutable digests | OPS_STATIC_TEST | NONE |
| C09 | Acceptance artifact equals deployed artifact | PRODUCTION_ACCEPTANCE | NONE |
| C10 | Control secrets create once | SECRET_INIT_INTEGRATION | NONE |
| C11 | Valid existing secrets preserve exact bytes | SECRET_INIT_INTEGRATION | NONE |
| C12 | Invalid/conflicting existing secret fails | SECRET_INIT_INTEGRATION | NONE |
| C13 | K1/K2/account-op key remain distinct | SECRET_INIT_INTEGRATION | NONE |
| C14 | Fresh Control DB roles exist before migration | CONTROL_INIT_INTEGRATION | NONE |
| C15 | Migration fresh / no-op / failure behavior | CONTROL_INIT_INTEGRATION | NONE |
| C16 | Environment create / no-op / conflict | CONTROL_INIT_INTEGRATION | NONE |
| C17 | Driver catalog seed / verify / conflict | CONTROL_INIT_INTEGRATION | NONE |
| C18 | Provider Policy absent → initial seed | CONTROL_INIT_INTEGRATION | NONE |
| C19 | Existing usable Provider Policy preserved | CONTROL_INIT_INTEGRATION | NONE |
| C20 | Invalid / unusable Provider Policy fails | CONTROL_INIT_INTEGRATION | NONE |
| C21 | Deployment verify is bounded/read-only | CONTROL_INIT_INTEGRATION | NONE |
| C22 | Fresh `docker compose up -d` reaches ready | PRODUCTION_ACCEPTANCE | NONE |
| C23 | Repeat `docker compose up -d` is non-destructive | PRODUCTION_ACCEPTANCE | NONE |
| C24 | Restart/recreate is safe | PRODUCTION_ACCEPTANCE | NONE |
| C25 | Gateway direct host access + Directory Bearer auth | PRODUCTION_ACCEPTANCE | NONE |
| C26 | Running Node equals pinned production digest | PRODUCTION_ACCEPTANCE | NONE |
| C27 | Secret material absent from images / Compose literals / logs | PRODUCTION_ACCEPTANCE | NONE |
| C28 | Deployment ready does not require admin / Node registration / monitoring | PRODUCTION_ACCEPTANCE | NONE |

Totals:

```text
TOTAL_CONTRACTS = 28
CONTRACTS_WITH_OWNER = 28
COVERAGE_GAPS = 0
```

---

## 12. Existing tests disposition

### KEEP

```text
internal/drivers/cliproxyapi/parser_test.go
Inventory projection/persistence tests
asset registry DB integration tests
environment identity verification tests
Gateway Directory Bearer route tests
auth/keyring validators
K2 provisioner tests
secret-redaction tests
frontend historical unsupported_node_version mapping
```

### MODIFY

```text
cmd/control/node_drivers_test.go
cmd/control/account_inventory_poll_test.go
internal/drivers/cliproxyapi/native_account_adapter_test.go
internal/api/account_operations_http_integration_test.go
internal/store/account_operations_orchestration_failure_schema_integration_test.go
internal/store/account_operations_schema_integration_test.go
internal/accountadmin/service_test.go
deploy/acceptance/runtime/gate-b-smoke.sh
deploy/acceptance/control-auth-e2e.sh
deploy/acceptance/runtime/run.sh

ops/dev/test_http_topology.py
ops/dev/test_update_runtime.py
ops/dev/test_update_admission.py
ops/dev/test_local_operations.py
ops/dev/test_directory_operations.py
```

### DELETE / REPLACE individual test cases

```text
TestExactRuntimeIdentityRejectsMissingDuplicateAndMismatch
TestNativeAdapterRuntimeGuardPreventsMutation
TestNativeAdapterRejectsSupersededRuntimeArtifactBeforeMutation
TestNativeAdapterArtifactChangeBlocksNextMutation
TestNativeAdapterArtifactGuardIsEnforcedByMutation
```

Replace them with metadata-non-gating / deployment-provenance tests.

### HISTORICAL_ONLY

```text
archived Phase 7 exact Node artifact evidence
old Gate 3 artifact identity
Phase 0 frozen Node baselines
```

Do not rewrite historical evidence.

---

## 13. Heavy proof duplication

Target ownership:

```text
control-init branches
→ CONTROL_INIT_INTEGRATION

secret state machine
→ SECRET_INIT_INTEGRATION

Control Node identity behavior
→ CONTROL_RUNTIME_TEST

Compose structure
→ OPS_STATIC_TEST

whole deployment facts
→ PRODUCTION_ACCEPTANCE
```

Production acceptance must not repeat every migration branch, Provider Policy branch, or secret-init permutation.

```text
DUPLICATED_HEAVY_PROOFS = 0
```

---

## 14. New Production Compose acceptance

Nine top-level cases are sufficient:

1. Fresh `docker compose up -d`, including ready before product provisioning.
2. Repeat `docker compose up -d`.
3. Restart/recreate.
4. `gateway-proxy` absent + direct Gateway host exposure.
5. Gateway Directory valid Bearer.
6. Gateway Directory invalid/missing Bearer.
7. Node running digest equals Production Compose pin.
8. Control/Gateway/Node acceptance artifacts equal deployed artifacts.
9. Secret leakage negative scan across images, Compose/config and logs.

```text
NEW_BROWSER_E2E_REQUIRED = NO
```

---

## 15. TCCR result

```text
PHASE9_TCCR = PASS

TOTAL_CONTRACTS = 28
CONTRACTS_WITH_OWNER = 28
COVERAGE_GAPS = 0
DUPLICATED_HEAVY_PROOFS = 0

NODE_TEST_CHANGE_REQUIRED = NO
NODE_SOURCE_CHANGE_REQUIRED = NO
DB_MIGRATION_REQUIRED = NO
NEW_BROWSER_E2E_REQUIRED = NO
NEW_COMPOSE_ACCEPTANCE_CASES = 9

P0 = 0
P1 = 0
```

No contract requires:

```text
Node source modification
DB schema migration
new proxy
new long-running service
runtime compatibility framework
```

---

# 16. Final implementation-readiness state

```text
FILE_LEVEL_IMPLEMENTATION_MANIFEST = COMPLETE
PHASE9_TCCR = PASS
ARCHITECTURE_DELTA_DETECTED = NO
P0 = 0
P1 = 0
OPEN_IMPLEMENTATION_OWNERSHIP_GAPS = NONE

TOTAL_CONTRACTS = 28
CONTRACTS_WITH_OWNER = 28
COVERAGE_GAPS = 0
DUPLICATED_HEAVY_PROOFS = 0

PHASE9_DELTA_REFRESH = PASS
MANIFEST_REFRESH_REQUIRED = NO
TCCR_REFRESH_REQUIRED = NO

PHASE9_PLANNING_BASELINE = FROZEN

WORKTREE_MUTATION = NONE
COMMIT = NONE
PUSH = NO
GITHUB_WRITE_OPERATIONS = NONE

PHASE9_IMPLEMENTATION_READINESS = PASS
```

Phase 8 is already formally closed:

```text
PHASE8_STATUS = CLOSED
PHASE8_EXIT = PASS
P0 = 0
P1 = 0

TD_INVALIDATION = NONE
BACKEND_CHANGED = NO
DATABASE_CHANGED = NO
BUSINESS_SEMANTICS_CHANGED = NO
```

Authorization boundary:

```text
PHASE9_PLANNING_AUTHORIZED = YES
PHASE9_IMPLEMENTATION_AUTHORIZED = NO
```

Next checkpoint:

```text
DOCUMENTATION_GIT_CHECKPOINT
```

Recommended planning commit:

```text
docs(phase9): freeze implementation planning
```

The documentation checkpoint should freeze:

```text
docs/validation/phase9-plan.md
docs/validation/phase9-implementation-readiness.md
```

After that:

```text
WAIT_FOR_EXPLICIT_PHASE9_IMPLEMENTATION_AUTHORIZATION
```

Do not begin Stage 1A until:

```text
PHASE9_IMPLEMENTATION_AUTHORIZED = YES
```
