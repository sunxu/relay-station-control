# Phase 10 Stage 3E Validation — Problems

## Status

```text
PHASE10_STAGE3E = CLOSED / PASS
STAGE3F_READINESS = READY FOR AUTHORIZATION
```

Implementation candidate:

```text
ab80406d0a1292a58a02a6ff8e2e390edd37a6ff
fix(phase10): preserve problem issue evidence
```

No backend, OpenAPI, migration, query, Gateway, Relay Node, or generated-client
files were changed.

## Source and candidate provenance

```text
Bootstrap no-store source proof = PASS
go test ./internal/api = PASS
go test ./tools (nested module) = PASS
Candidate source SHA = ab80406d0a1292a58a02a6ff8e2e390edd37a6ff
Candidate image revision = ab80406d0a1292a58a02a6ff8e2e390edd37a6ff
Candidate image ID = sha256:b42a12bef4e4733c9f9076f3614a67cbe2d6c95d8d36f289c0489d3afe1d1467
Candidate image platform = linux/arm64
Node artifact identity = PASS
```

The previous no-store failure was caused by the stale `relay-station/control:auth-e2e`
image, whose revision was the old default SHA. A fresh image built from the exact
candidate revision passed the source/runtime provenance and no-store checks. No
backend workaround or header injection was used.

## Problems contract

```text
/problems canonical owner = PASS
/problems/ trailing-slash ownership = PASS
English Problems naming = PASS
ordinary Issues label = ZERO
Problem query semantics = PASS
Issue taxonomy preserved = PASS
Machine-value reclassification = ZERO
Problem issue evidence completeness = PASS
Issue reason machine values preserved = PASS
Initial query = PASS ({limit:25})
Filter transport = PASS
Cursor pagination = PASS
CSRF = PASS
Fake totals/rates/trends = ZERO
Problem mutation controls = ZERO
Automatic Node/Gateway probes = ZERO
Control-origin-only fetch/xhr = PASS
Direct Node/Gateway/Prometheus/provider = ZERO
401 session boundary = PASS
Non-401 session preservation = PASS
STALE_PROBLEM_QUERY_ISOLATION = PASS
Empty / Filtered Empty / Unavailable = PASS
```

The focused Problems Browser matrix passed independently for:

```text
ZH_CN_1280 = PASS
EN_1280 = PASS
ZH_CN_1440 = PASS
ZH_CN_PROBLEMS_UNINTENDED_ENGLISH_LEAK = ZERO
```

The repository-owned production-like HTTPS runtime also passed the three Problems
cases, with all fetch/XHR traffic remaining on the Control origin.

## Cross-stage and runtime validation

```text
Playwright interaction testid policy = PASS
Accessibility = PASS
Unit/component = PASS (47 files / 323 tests)
Typecheck = PASS
Build = PASS
Translation audit = PASS
Focused Problems Browser = PASS (3/3)
Cross-stage Browser = PASS (22/22, retained Stage 3D final shared matrix)
Production-like HTTPS = PASS (5/5 general acceptance domains; 7/7 test cases)
Generated drift = NONE
git diff --check = PASS
```

The dedicated `ACCEPTANCE_*` account-operation suites remain out of scope for the
shared Phase 10 cross-stage gate. They were not counted as failures or included in
the denominator.

The host-local fixture Browser retry encountered a Chromium host execution
`SIGABRT` before test code ran; it did not alter the accepted production-like
HTTPS result. The pinned Browser execution in the isolated acceptance runtime
passed the owning Problems coverage.

## Scope and review

```text
Backend/API change = NO
Database/Migration change = NO
Gateway source change = NO
Relay Node source change = NO
P0 = 0
P1 = 0
P2 = 0
```

Stage 3E is complete. Stage 3F is only ready for authorization and was not started.
