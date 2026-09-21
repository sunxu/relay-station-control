# Phase 10 Stage 3B Validation Evidence

## Final status

```text
PHASE10_STAGE3B = CLOSED / PASS
/nodes canonical Node owner = PASS
/assets auxiliary owner = PASS
Node controls duplicated in /assets = ZERO
STAGE3C_READINESS = READY FOR AUTHORIZATION
```

`/nodes` is the sole executable Relay Node management surface. `/assets`
retains Environment, Gateway, Driver catalog, and Provider Policy only; it no
longer mounts the Node registry or Node lifecycle/observation controls. Gateway
remains an auxiliary surface and is not modeled as a Relay Node.

## Node ownership and lifecycle proof

```text
Node list/filter/pagination = PASS
Node register = PASS
Node edit = PASS
Node replace = PASS
Node retire = PASS
Node lineage = PASS
Credential keep/set/clear = PASS
Credential DOM/storage/URL leak = ZERO
Retired executable controls = ZERO
Node Health explicit-only = PASS
Node Connection Test explicit-only = PASS
Monitoring Enable = PASS
Monitoring Disable confirmation = PASS
Automatic Node probes = ZERO
```

The existing Node lifecycle owning Browser test now enters `/nodes` and keeps
the lifecycle, revision, lineage, retired-history, explicit health,
connection-test, monitoring confirmation, and credential non-reflection
coverage. The `/assets` ownership component proof confirms that the auxiliary
surface does not mount the Node query or executable controls.

## Gateway auxiliary regression

```text
Gateway management regression = PASS
Environment = PASS
Driver catalog = PASS
Provider Policy = PASS
```

The existing Gateway management Browser proof remains owned by `/assets` and
continues to cover Gateway register/edit/replace/retire, health, connection
test, and credential keep/set/clear semantics.

## Browser transport and desktop proof

```text
Control-origin-only fetch/xhr = PASS
Direct Node browser calls = ZERO
Direct Gateway browser calls from /nodes = ZERO
zh-CN = PASS
en = PASS
1280x720 = PASS
1440x900 = PASS
Accessibility = PASS
Playwright locator policy = PASS
Browser proof = PASS
```

All relevant Browser fetch/xhr requests are captured with method, full URL,
origin, and pathname. The Node lifecycle proof asserts that every request stays
on the Control origin and that Node/Gateway management endpoints are never
called directly by the browser. Explicit health, connection-test, and
monitoring actions continue to be sent to Control APIs only.

The repository-owned isolated production-like HTTPS general harness passed
6/6: authentication/TOTP, Gateway management, Node lifecycle on `/nodes`,
Problems, and both Topology compatibility cases. It used the recorded Stage 1
acceptance Node artifact without changing its source or runtime identity:

```text
source = 0b34a22fcaec392d39f710f3a8418595b491607d
version = 7.3.2
image ID = sha256:0d927726081869825f4ac444a3f83687e560b01c207de7e22032fb33efccac2d
```

## Validation

```text
Unit/component = PASS (full suite: 45 files; 319 tests; focused ownership/resources: 19 tests)
Typecheck = PASS
Build = PASS
Translation audit = PASS
Generated drift = NONE
git diff --check = PASS
```

```text
Backend/API change = NO
Database/Migration change = NO
Gateway source change = NO
Relay Node source change = NO
```

## Independent review

```text
P0 = 0
P1 = 0
P2 = 0
```

Review covered unique Node ownership, `/assets` duplicate controls, Gateway
regression, credential and secret boundaries, revision/lifecycle semantics,
monitoring confirmation, explicit-only probes, stale detail protection,
session boundaries, browser transport, i18n, formatting, accessibility,
locator stability, desktop layout, and Stage 3C/3D scope leakage.

The existing deployment revalidation gate for bootstrap response cache policy
was exercised by the production-like harness and passed in this candidate.

## Final independent review corrective

```text
NODE_COMPONENT_OWNERSHIP = PASS
ASSET_NODE_LOGIC = ZERO
NODE_ASSET_MODE_SWITCH = ZERO
ZH_CN_NODES_ENGLISH_LEAK = ZERO
NODE_SESSION_BOUNDARY = PASS
STALE_DETAIL_PROTECTION = PASS
PLAYWRIGHT_INTERACTION_TESTID_POLICY = PASS
```

`AssetRegistryView` now has a physical auxiliary-only boundary and no longer
contains Node types, hooks, lifecycle controls, probes, or a combined-surface
mode. `NodeManagementView` owns the Node registry and all Node lifecycle,
credential, lineage, observation, and monitoring presentation. Node and Driver
401 focused tests cover the shared authenticated-session callback; detail,
lifecycle mutation, explicit health, and monitoring 401 cases are also covered.

The full repository-owned production-like HTTPS general harness was rerun with
the recorded rebuilt Node artifact and passed 6/6:

```text
authentication = PASS
gateway-management = PASS
node-lifecycle = PASS
problems = PASS
topology-account-detail = PASS
topology-navigation/recovery = PASS
PRODUCTION_LIKE_HTTPS = PASS
```

The run verified secret initialization, Node runtime identity, TLS/HTTPS
browser transport, same-origin Control API traffic, and Gateway/Node owning
regressions. No backend, database, Gateway, or Relay Node source was changed.
