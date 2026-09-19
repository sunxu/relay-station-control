# Relay Station Phase 9 — Plan

> Status: **PLANNING FROZEN**
>
> Phase 8: **CLOSED / PASS**
>
> Phase 9 planning: **COMPLETE / FROZEN**
>
> Phase 9 implementation: **NOT AUTHORIZED**
>
> Next gate: **DOCUMENTATION GIT CHECKPOINT → EXPLICIT IMPLEMENTATION AUTHORIZATION**

---

## 1. Goal

Phase 9 solves only deployment and production-readiness problems.

Target operator experience:

```text
docker compose up -d
```

Normal production deployment must not require operators to understand:

```text
devctl
manual SQL
manual Control migration
manual environment seed
manual Provider Policy activation
legacy secret mapping files
local source ↔ local image reconciliation
Control-side Node artifact pinning
```

Phase 9 does not include account-management product improvements. Those remain Phase 10 scope.

---

## 2. Four-stage model

```text
Stage 0 — Inventory & Decisions
Stage 1 — Cleanup
Stage 2 — Automatic Deployment
Stage 3 — Production Acceptance & Closeout
```

Hard boundaries:

```text
Stage 1 != deployment automation
Stage 2 != product-state auto-provisioning
Stage 3 != new architecture/design work
```

---

## 3. Validated planning baselines

```text
relay-station-control
98e7996de1c72c59fd9189f0cf137e4ecd23d7a1

relay-station-ops
7f4c35c49ac8e1d2200a8567d86674ca495ef964

relay-station-node-cliproxyapi
0b34a22fcaec392d39f710f3a8418595b491607d

relay-station-gateway
b2512a314dbeae7d3dfbb05c5253d214a7f32102
```

These baselines were validated against the closed Phase 8 state. The Phase 9 delta refresh has already passed with no architecture impact.

---

## 4. Configuration decisions

### REMOVE

```text
CONTROL_CLIPROXYAPI_DRIVER_ENABLED
CONTROL_ACCOUNT_INVENTORY_LIFECYCLE_ENABLED
CONTROL_ACCOUNT_INVENTORY_POLL_MAX_NODES

legacy CLIProxyAPI secret mapping surfaces
legacy Gateway Directory secret mapping surfaces
legacy secret-reference deployment wiring

CONTROL_PROVIDER_POLICY_MUTATION_ENABLED
→ remove from normal operator surface

CONTROL_MANAGEMENT_CIDR
→ remove with gateway-proxy cleanup
```

### KEEP_OPERATIONAL

```text
CONTROL_ACCOUNT_INVENTORY_POLL_ENABLED
CONTROL_ACCOUNT_REQUEST_QUALITY_ENABLED
CONTROL_ACCOUNT_INVENTORY_HISTORY_ENABLED
CONTROL_GATEWAY_DIRECTORY_ENABLED
```

These are real operational controls / kill switches, not rollout residue.

### KEEP

```text
CONTROL_MFA_REQUIRED
```

### DB desired state

```text
Provider Policy / activation
Node monitoring
Node lifecycle
Gateway lifecycle
```

These remain PostgreSQL-owned, not environment-variable-owned.

### PRESENCE_BASED

```text
DingTalk webhook/signing configuration
```

Do not introduce an extra `CONTROL_DINGTALK_ENABLED`.

---

## 5. Gateway boundary

### Final target

```text
gateway-proxy = REMOVE
```

Target topology:

```text
Host
→ 127.0.0.1:${GATEWAY_PUBLIC_PORT:-18082}
→ Gateway:8080
```

Control continues to use:

```text
http://gateway:8080/internal/v1/api-account-directory
```

with existing Bearer authentication.

Accepted simplification:

```text
/internal/v1/*
→ may remain reachable through the public Gateway listener
→ existing Bearer authentication is sufficient
```

Therefore Phase 9 does not add:

```text
public/internal listener split
internal-only port
replacement Nginx
Traefik
Envoy
service mesh
Gateway code change
```

Cleanup includes:

```text
gateway-proxy service
gateway-nginx.conf
proxy config generation
CONTROL_MANAGEMENT_CIDR
proxy update/recovery/provenance bookkeeping
```

---

## 6. Node artifact / compatibility boundary

Final target:

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

Unknown / arbitrary Node artifacts are:

```text
OUT_OF_SCOPE
```

Supporting a new Node artifact means:

```text
review
→ acceptance
→ release
→ deployment digest change
```

not runtime negotiation.

Existing:

```text
X-CPA-VERSION
X-CPA-COMMIT
```

may remain observational/diagnostic metadata, but must not determine whether Control may operate the Node.

Historical `unsupported_node_version` receipts/audit remain replay-compatible. No DB migration is required solely for this change.

---

## 7. Production Image contract

Formal Production Images are built from release tags, not every commit.

```text
normal push / PR
→ test / build verification only

deploy-v* release tag
→ GitHub Actions
→ multi-arch image
→ GHCR
→ immutable digest
→ Production Compose
```

Application images:

```text
Control
Gateway
Node
```

Infrastructure images:

```text
PostgreSQL
Redis
```

Production must not rebuild application images locally.

Core invariant:

```text
accepted artifact
=
deployed artifact
```

Minimum provenance:

```text
source commit
↔ release tag
↔ OCI source/revision
↔ immutable image digest
↔ production Compose
↔ running container
```

No release database or complex `RELEASE-MANIFEST.yaml` is required.

GitHub Actions build/publish artifacts only; Phase 9 does not introduce automatic SSH/CD into production.

---

## 8. Secret model

No Secret Manager platform is introduced.

Control-owned create-once secrets remain managed by the existing `control-secret-init` role.

At minimum:

```text
auth keyring
asset credential K2
asset intent K1
account-operation intent key
bootstrap secret
```

Rules:

```text
missing and deployment-owned
→ create once

valid existing
→ preserve exact bytes

invalid/conflicting existing
→ fail

silent overwrite
→ forbidden

automatic rotation
→ not in Phase 9
```

Keep separate:

```text
Asset intent K1
!= Asset credential K2
!= Account-operation intent key
```

External/native credentials remain explicit deployment/operator inputs.

DingTalk remains optional and presence-based.

Bootstrap secret stays simple:

```text
if pre-provisioned by operator
→ preserve exact value

otherwise
→ control-secret-init creates once

then:
persist
mount read-only
no automatic rotation/delete lifecycle
```

The deployment must use exactly one of these two input modes; it must not require both.

---

## 9. Automatic deployment owner model

Current normal local entry:

```text
./dev/devctl up
```

Target normal production entry:

```text
docker compose up -d
```

`devctl` remains for:

```text
developer build validation
diagnostics
backup/update/recovery
fault drills
```

Normal production deployment must no longer depend on it.

---

## 10. `control-init` one-shot

Phase 9 introduces one new bounded logical one-shot:

```text
control-init
```

It uses the same Control release artifact with a dedicated init command/entrypoint.

It exists to keep migrator/bootstrap privileges out of the long-running Control runtime.

Responsibilities:

```text
1. migrate Control DB to latest
2. seed/verify environment identity
3. seed/verify static Driver catalog
4. seed initial Provider Policy
5. verify deployment invariants
6. exit
```

Do not split this into five separate containers.

```text
NEW_LONG_RUNNING_SERVICE_REQUIRED = NO
NEW_ONE_SHOT_SERVICE_REQUIRED = 1
```

Gateway keeps its existing embedded migration/AUTO_SETUP path.

Node has no Relay Station DB migration.

---

## 11. Provider Policy — seed-only model

Provider Policy is PostgreSQL desired state.

Deployment owns **initial seed only**, not permanent equality with deployment config.

Target semantics:

```text
ABSENT
→ create initial Provider Policy

PRESENT + structurally valid + usable active policy
→ leave untouched

INVALID / no usable active policy
→ fail clearly
```

After the initial seed:

```text
PostgreSQL Provider Policy
→ sole source of truth
```

A later legitimate administrator change must not make a future `docker compose up -d` fail merely because it differs from the original seed input.

`CONTROL_PROVIDER_POLICY_MUTATION_ENABLED` must not remain a normal operator-facing deployment flag.

---

## 12. Environment and Driver catalog ownership

`control-init` owns only bounded deployment/static state.

### Environment identity

```text
missing
→ create

same identity/type
→ no-op

different identity/type
→ fail closed
```

### Static Driver catalog

```text
missing
→ seed code-defined catalog

compatible/current
→ verify/no-op

conflicting/inconsistent
→ fail clearly
```

Driver catalog bootstrap must happen before initial Provider Policy seed.

---

## 13. Deployment verify — bounded scope

The old broad wording `deployment reconcile` is replaced by:

```text
deployment verify
```

`control-init` may reuse existing reconciliation primitives internally, but its deployment contract is bounded to verifying already-approved deployment/static invariants.

It must not create or mutate product-owned state:

```text
Gateway registration
Node registration
management credential entry
Node monitoring activation
bindings
accounts
product routing/state
```

No generic always-on reconciliation subsystem is introduced.

---

## 14. Product provisioning boundary

These remain explicit authenticated product/admin actions:

```text
first administrator security bootstrap
Gateway registration
Node registration
Gateway Directory credential entry
Node management credential entry
Node monitoring enable/disable
Gateway↔Node/account bindings
accounts
other administrator-owned product state
```

Therefore:

```text
deployment ready
!= administrator exists
!= Node registered
!= monitoring enabled
!= inventory populated
```

Compose must not become the product control plane.

---

## 15. Readiness

No new `/livez` / `/readyz` framework is introduced.

Existing health endpoints remain sufficient:

```text
Control  /api/healthz
Gateway  /health
Node     /healthz
Postgres pg_isready
Redis    ping
```

Deployment readiness is proven by:

```text
control-secret-init completed
+
control-init completed
+
required service healthchecks healthy
```

`control-init` success proves:

```text
schema current
environment identity valid
Driver catalog valid
initial Provider Policy available/usable
deployment invariants valid
```

Optional integrations/workers do not gate readiness.

Core invariant:

```text
deployment ready
!= every product feature configured
```

---

## 16. Target Compose role set

```text
control-postgres
gateway-postgres
redis

control-secret-init   # existing one-shot
control-init          # one new one-shot

control
gateway
node
```

Removed:

```text
gateway-proxy
```

No new long-running service is required.

---

## 17. Idempotency contract

Target:

```text
docker compose up -d
→ first install safe

docker compose up -d
→ repeat safe

docker compose restart
→ safe
```

Secrets:

```text
missing → create once
valid existing → preserve
invalid/conflicting → fail
```

Migration:

```text
fresh → migrate
latest → no-op
failure → fail
```

Environment/static deployment state:

```text
missing → seed
valid/current → verify/no-op
conflict → fail
```

Provider Policy:

```text
absent → initial seed
present + usable → preserve
invalid/unusable → fail
```

---

## 18. Normal operator inputs

Normal operators still provide genuine external inputs:

```text
environment identity/config
database/native credentials
initial Provider Policy seed
Node/Gateway native external credentials/config
optional DingTalk configuration

Control bootstrap secret:
  only when using pre-provisioned mode;
  otherwise control-secret-init creates it once
```

Application image digests are frozen by the production release Compose rather than manually typed by normal operators.

Manual post-start product actions remain:

```text
first administrator security bootstrap
explicit Gateway/Node enrollment
explicit monitoring opt-in
```

---

## 19. Minimal implementation order

### Stage 1A — Deployment cleanup

```text
remove gateway-proxy
remove gateway-nginx.conf
remove CONTROL_MANAGEMENT_CIDR
remove legacy secret mappings/reference wiring
remove deprecated rollout/config switches
remove obsolete proxy update/recovery/provenance logic

wire:
127.0.0.1:18082 → Gateway:8080
```

### Stage 1B — Node provenance decoupling

Control:

```text
remove FrozenRuntimeVersion gating
remove FrozenRuntimeCommit gating
remove exact version/commit rejection
```

Keep:

```text
version/commit observation
schema validation
fixed native API paths
auth/security/transport invariants
historical error replay compatibility
```

Node:

```text
NO CHANGE
```

### Stage 1C — Image/provenance cleanup

```text
remove stale source-controlled application image defaults
remove production reliance on local repo HEAD
remove old local-tag release truth
```

### Stage 2A — Immutable Production Image inputs

Production Compose consumes released immutable artifacts.

### Stage 2B — Secret-init convergence

Converge existing `control-secret-init` to create/preserve/validate the required Control-owned secret set.

### Stage 2C — `control-init`

Implement:

```text
migration
environment seed/verify
Driver catalog seed/verify
Provider Policy initial seed
bounded deployment verify
```

### Stage 2D — Compose orchestration

Wire:

```text
depends_on
service_completed_successfully
healthchecks
persistent volumes
direct Gateway port
runtime credentials
```

### Stage 2E — Implementation smoke

Only:

```text
fresh up
repeat up
restart
basic negative secret/config
```

### Stage 3A — Production acceptance

Run the frozen minimal matrix below.

### Stage 3B — Closeout

Only:

```text
evidence
decision reconciliation
runbook/docs
candidate freeze
clean worktree
final validation
```

No new implementation.

---

## 20. Acceptance ownership

Prove each fact at the lowest owning layer.

### `control-init` integration

```text
migration fresh/no-op/failure
environment create/no-op/conflict
Driver catalog seed/verify/conflict
Provider Policy initial seed / existing usable / invalid
deployment verify
```

### `control-secret-init` integration

```text
create once
preserve exact bytes
invalid/conflicting existing secret fails
```

### Control tests

```text
exact Node version/commit gates removed
version/commit remain observational
historical error replay compatibility preserved
```

### Production Compose acceptance

```text
fresh docker compose up -d
repeat docker compose up -d
restart/recreate
gateway-proxy absent
Gateway direct host exposure
Directory Bearer valid/invalid
Node exact digest pinned
acceptance artifacts == deployed artifacts
secrets absent from images / Compose literals / logs
```

Do not duplicate lower-layer state-machine branches as heavy Compose E2E.

---

## 21. Upgrade / restore scope

To keep Phase 9 minimal, required production acceptance is:

```text
fresh install
repeat up
restart/recreate
DB migration behavior
```

Not required for Phase 9 closeout:

```text
full upgrade workflow
full restore drill
disaster-recovery platform
```

Existing `devctl` / administrator backup-update-recovery tooling remains available and may be improved separately if required later.

A restore drill is not part of the current Phase 9 acceptance contract unless a new production requirement explicitly adds it.

---

## 22. Non-goals

Phase 9 does not introduce:

```text
Node compatibility framework
Node capability negotiation
compatibility DB / allowed-version table
new proxy / ingress platform
service mesh
automatic GitHub CD / SSH deployment
Secret Manager platform
automatic key rotation
key-history framework
new readiness subsystem
new deployment-state database table
automatic product asset enrollment
Upload New Account functional changes
```

---

## 23. Planning status

```text
CONFIGURATION_SURFACE_INVENTORY = COMPLETE
GATEWAY_PROXY_REMOVAL_DECISION = COMPLETE
NODE_PROVENANCE_DECISION = COMPLETE
PRODUCTION_IMAGE_CONTRACT = COMPLETE
TARGET_DEPLOYMENT_FLOW_REVIEW = PASS
PHASE9_PLAN_FINAL_REVIEW = PASS

P0 = 0
P1 = 0
OPEN_ARCHITECTURE_QUESTIONS = NONE

PHASE9_PLAN = FROZEN
```

---

## 24. Planning freeze and next gate

Phase 8 is closed and the Phase 9 delta refresh passed without architecture impact.

```text
PHASE8_STATUS = CLOSED
PHASE8_EXIT = PASS

PHASE9_DELTA_REFRESH = PASS
ARCHITECTURE_DELTA_DETECTED = NO

P0 = 0
P1 = 0
OPEN_ARCHITECTURE_QUESTIONS = NONE

PHASE9_PLAN = FROZEN
```

Authorization boundary:

```text
PHASE9_PLANNING_AUTHORIZED = YES
PHASE9_IMPLEMENTATION_AUTHORIZED = NO
```

The next repository checkpoint is a documentation-only commit:

```text
docs(phase9): freeze implementation planning
```

Do not begin Stage 1A until explicit implementation authorization is granted.
