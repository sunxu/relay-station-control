# Phase 10 Stage 3A Validation Evidence

## Final status

```text
PHASE10_STAGE3A = CLOSED / PASS
Accounts canonical route = /accounts
STAGE3B_READINESS = READY FOR AUTHORIZATION
```

Stage 3A makes `/accounts` the canonical account workspace while retaining
`/topology` compatibility and topology-only diagnostics. Existing generated
Control APIs and account operation contracts are reused; no backend contract
was added.

## Account domain proof

```text
Accounts IA = PASS
Node context = PASS
Inventory = PASS
Account list/detail = PASS
Quality = PASS
Availability = PASS
Request History = PASS
Incidents = PASS
Account Operations = PASS
Unknown / Empty / Unavailable semantics = PASS
outcome_unknown semantics = PASS
```

The shared `AccountWorkspace` is used by `/accounts` and the account portion
of `/topology`; topology binding, duplicate-ownership, provider, and evidence
diagnostics remain topology-only. Node selection resets account state and
focused tests prove a delayed Node A response cannot repopulate Node B.

```text
Account global totals = ZERO
Fake metrics = ZERO
account_key URL/storage leak = ZERO
Secret/credential leak = ZERO
Automatic Node/Gateway probes = ZERO
Direct Node/Gateway browser calls = ZERO
```

Account presentation uses the shared foundation date, number, and percentage
formatters. Account filters, cursor pagination, details, availability history,
quality incidents, upload entry, and existing operation presentation retain
their existing API/security semantics.

## Locale and desktop proof

```text
zh-CN = PASS
en = PASS
1280x720 = PASS
1440x900 = PASS
Accessibility = PASS
Playwright locator policy = PASS
```

The focused Accounts Browser suite passed three cases: zh-CN 1280×720,
English 1280×720, and zh-CN 1440×900. It verified account detail and tabs,
same-origin Control API transport, no direct Node/Gateway requests, no health
or connection probes, and no page-level overflow. Existing Topology owning
compatibility passed in both the local fixture harness and the production-like
general acceptance harness.

## Validation

```text
Unit/component = PASS (43 files, 321 tests)
Typecheck = PASS
Build = PASS
Translation audit = PASS (resource parity tests 14/14; locale Browser 6/6)
Browser proof = PASS
Relevant legacy regression = PASS
Generated drift = NONE
git diff --check = PASS
```

The repository-owned isolated production-like HTTPS general harness passed
authentication/TOTP, Gateway, Node, Problems, and Topology (6/6), using the
previously recorded Stage 1 rebuilt Node acceptance artifact:

```text
source = 0b34a22fcaec392d39f710f3a8418595b491607d
version = 7.3.2
image ID = sha256:0d927726081869825f4ac444a3f83687e560b01c207de7e22032fb33efccac2d
```

```text
Backend/API change = NO
Database/Migration change = NO
Gateway change = NO
Relay Node source change = NO
```

## Independent review

```text
P0 = 0
P1 = 0
P2 = 0
```

Review covered account ownership versus topology diagnostics, filtering and
cursor semantics, stale-response and 401 cache/session boundaries,
`outcome_unknown`, credential lifecycle, URL/storage leakage, transport
boundaries, i18n/formatting, accessibility, locator stability, desktop scope,
and Stage 3B scope leakage.

## Independent review corrective

The corrective keeps the existing six Stage 3A commits intact and adds no
backend or cross-repository changes.

```text
Node-list 401 boundary = PASS
Selected-node 401 boundary = PASS
Provider-state 401 boundary = PASS
Account-query 401 boundary = PASS
Stale prior-context response isolation = PASS
Full-origin Control transport proof = PASS
Direct Node/Gateway browser calls = ZERO
Automatic health/connection probes = ZERO
Accounts operation Browser wiring = PASS
outcome_unknown presentation = PASS
Override visibility contract = PASS
zh-CN Accounts ordinary English leak = ZERO
```

Focused Browser proof passed 4/4 cases (zh-CN 1280×720, English 1280×720,
zh-CN 1440×900, and the canonical account-operation flow). Full Vitest passed
44 files / 325 tests; typecheck and production build passed, and generated
client drift remained zero.

The repository production-like general harness was also attempted with the
recorded Stage 1 rebuilt Node artifact. Its runtime reached secret
initialization, Node identity, database migration, Control/TLS startup, but
stopped at the pre-existing deployment contract check:
`bootstrap status lacks Cache-Control: no-store`. This corrective does not
change Control HTTP behavior; fixing that backend contract is outside the
Stage 3A frontend scope. Existing accepted production evidence above remains
unchanged; this run is recorded as a revalidation environment blocker rather
than a new PASS claim.
