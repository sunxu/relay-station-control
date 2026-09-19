# Phase 8 — Stage 1 Frontend Executable Baseline Evidence

> Status: **STAGE 1 CLOSED — Final independent freeze passed**
> Phase: **Phase 8 — Stage 1**
> Evidence type: **Frontend executable baseline**
> Control source: `e3b52987a35ed470eba958b3f6764188bb4197f2`
> Frontend tree: `58e8f8af4157edf2f78e3cb07586029a7358c334`
> Acceptance harness: `4ef6e934637ac886852d26a70970820e887d0a03` (acceptance-only)

---

## 1. Purpose

This file records durable executable evidence for the final post-Stage-0 Stage 1 frontend baseline.

It is intentionally narrower than:

`docs/validation/phase8-stage1.md`

This evidence file records execution identity, results, historical failure reconciliation, and remaining executable work.

It does not define frontend architecture and does not authorize Stage 2 implementation.

---

## 2. Source and Toolchain Identity

### Control

```text
CONTROL_SOURCE_SHA =
e3b52987a35ed470eba958b3f6764188bb4197f2
```

### Frontend tree

```text
FRONTEND_TREE =
58e8f8af4157edf2f78e3cb07586029a7358c334

### Acceptance harness

```text
ACCEPTANCE_HARNESS_SHA =
4ef6e934637ac886852d26a70970820e887d0a03
```

This acceptance-only identity is separate from, and does not replace, the
product/frontend implementation baseline.
```

### Frontend manifests

```text
web/package.json Git blob =
4cebc420e819ae063dff723e0579224aad30bf6a

web/package-lock.json Git blob =
276b28e70353d7f33b8582ddc02037b5cd46aec1
```

### Runtime toolchain

```text
Node:
v24.19.0

npm:
11.17.0

which node:
/opt/homebrew/opt/node@24/bin/node

which npm:
/opt/homebrew/opt/node@24/bin/npm
```

This Node 24 execution is the formal local non-Browser baseline.

Earlier Node 26 runs were diagnostic-only and are not substituted for the Node 24 baseline.

---

## 3. Preflight

```text
HEAD =
4ef6e934637ac886852d26a70970820e887d0a03

PRODUCT_FRONTEND_SHA =
e3b52987a35ed470eba958b3f6764188bb4197f2

Worktree before execution =
CLEAN
```

The executable run used the selected final Stage 1 source baseline.

No source or test patch was introduced to obtain the result.

---

## 4. Result Matrix

| Check | Result | Notes |
|---|---|---|
| `npm ci` | **PASS** | Node 24 |
| unit/component | **PASS** | 31 files / 251 tests |
| `AssetRegistryView.test.tsx` | **PASS** | 16/16 |
| historical flaky cases | **PASS** | both passed under formal Node 24 baseline |
| `npm run typecheck` | **PASS** | Node 24 |
| `npm run build` | **PASS** | generate + TypeScript + Vite |
| `generate:api` | **PASS** | generated successfully |
| generated-source drift | **PASS** | `git diff --exit-code` clean |
| Vite production build | **PASS** | 1569 modules, ~2.99s |
| `internal/webui/dist` | **GENERATED** | 712 files, ~34 MB |
| General Browser E2E | **PASS** | 6 passed, 40.7s |
| Focused acceptance | **PASS** | upload, enable-fixture, enable, replace, remove, override |
| Disable / Security Replay | **REUSED / VALID** | durable Gate 7 evidence |

Overall:

```text
NON_BROWSER_EXECUTABLE_BASELINE = PASS
BROWSER_E2E_BASELINE = PASS
EXECUTABLE_BASELINE = COMPLETE
```

---

## 5. Unit / Component Baseline

Formal Node 24 result:

```text
Test files:
31 PASS

Tests:
251 PASS
```

No unit/component failure remained in the final non-Browser baseline.

Non-fatal jsdom warnings were observed for:

```text
getComputedStyle(..., pseudo-elements)
```

They did not fail the suite and are not classified as product failures.

---

## 6. Historical AssetRegistry Timeout Reconciliation

### 6.1 Historical failure

A GitHub Actions run on the same Stage 1 source baseline previously reported two timeouts in:

```text
web/src/pages/AssetRegistryView.test.tsx
```

Tests:

```text
shows explicit Stage 3 operations only inside active Node detail

ignores late detail responses after switching Nodes
```

Historical timeout observations:

```text
Test 1:
15000 ms timeout

Test 2:
5000 ms timeout
```

### 6.2 Diagnostic reproduction

Subsequent diagnostic runs under Node 26.7.0 produced:

```text
full AssetRegistryView test file:
3/3 runs PASS
16/16 each run

Test 1 isolated:
3/3 PASS

Test 2 isolated:
3/3 PASS
```

Observed approximate timings:

```text
full file:
14.54–15.14s

Test 1 isolated:
7.54–7.93s
test body ~6.85–6.96s

Test 2 isolated:
2.29–2.34s
test body ~1.62–1.67s
```

No blocking wait was observed in:

- `fireEvent`;
- fetch mocks;
- Drawer/Modal behavior;
- query state;
- component state updates;
- late-response stale guard.

### 6.3 Stage 0 causal review

Stage 0 changed the relevant frontend primarily through:

```text
reader_secret_ref
→ management_credential / directory_credential

credential keep / set / clear
generated API credential types
credential-related fixtures
```

The historical timeout paths rely on:

- `nodeDetail`;
- active Node conditional rendering;
- Stage 3 detail operations;
- `nodeDetailRequestRef`;
- request-id stale-response protection.

Those paths were not changed by the Stage 0 credential delta, and the affected tests do not submit or interact with the credential forms.

Result:

```text
STAGE0_CAUSAL_LINK = NO
```

### 6.4 Final classification

```text
CLASSIFICATION =
FLAKY_UNKNOWN

PRODUCT_BEHAVIOR_BUG =
NOT ESTABLISHED

TEST_CONTRACT_BUG =
NOT ESTABLISHED

ASYNC_TEST_BUG =
NOT ESTABLISHED

PERFORMANCE_TIMEOUT =
NOT ESTABLISHED

ENVIRONMENT_ROOT_CAUSE =
NOT ESTABLISHED

FIX_REQUIRED =
NO

PATCH =
NONE

TIMEOUT_INCREASE =
NONE
```

The historical failure remains part of the evidence record.

It is not rewritten as if it never occurred.

Because the historical Phase 1 pre-Stage-0 executable baseline was never run, the historical timeout cannot be classified as a Stage 0 regression from available evidence.

---

## 7. Typecheck

Formal result:

```text
npm run typecheck
PASS
```

No Stage 1 source change was required.

---

## 8. Production Build

Formal result:

```text
npm run build
PASS
```

The build completed:

```text
generate:api
→ TypeScript
→ Vite production build
```

Observed production build:

```text
1569 modules
~2.99s
```

Output:

```text
internal/webui/dist
712 files
~34 MB
```

---

## 9. Generated-Source Integrity

After generation/build:

```text
git diff --exit-code
PASS
```

Result:

```text
GENERATED_DRIFT = NO
```

The selected Stage 1 source baseline already contains the committed generated artifacts required by the current API contract.

---

## 10. Browser E2E

The repository-authoritative Browser baseline completed using host execution
required by `docs/testing/ACCEPTANCE_E2E_POLICY.md`.

General Browser:

```text
GENERAL_BROWSER_E2E = PASS
6 passed
duration = 40.7s

Authentication = PASS
Gateway management = PASS
Node lifecycle = PASS
Problems = PASS
Topology = PASS

global desktop = 1280x720
Problems responsive = 390x844
Topology responsive = 1280x900, 390x844
```

Focused acceptance:

```text
UPLOAD = PASS
ENABLE_FIXTURE = PASS
ENABLE = PASS
REPLACE = PASS
REMOVE = PASS
OVERRIDE = PASS
DISABLE = REUSED / VALID
SECURITY_REPLAY = REUSED / VALID
```

Runtime/toolchain:

```text
Control image = sha256:5245b19d9557386804cfab210828d1c7bbfaf7eed9229b2913b646387b13e522
OCI revision = e3b52987a35ed470eba958b3f6764188bb4197f2
PostgreSQL migrations = 00001–00051 PASS
Control = PASS
PostgreSQL = PASS
Node = PASS
node-counter = PASS
TLS = PASS
secret-init = PASS
negative configuration checks = PASS

Playwright = 1.62.1
Browser = bundled Chromium / Chrome for Testing 151.0.7922.34
HOST_EXECUTION_POLICY_APPLIED = YES
BROWSER_HOST_EXECUTION_BLOCKER = RESOLVED
```

The initial bundled Chromium SIGABRT was classified as a host/sandbox
execution boundary issue, not a product or Browser test defect. The formal
run continued with bundled Chromium and did not use system Chrome, unsafe
flags, Playwright config changes, or test-source changes.

Focused evidence:

```text
Replace: inventory finalize PASS, Browser PASS, receipt PASS, audit PASS,
native POST = 1, Node PASS, secret scan PASS

Remove: inventory finalize PASS, Browser PASS, native DELETE = 1,
Node PASS, cancel PASS, secret scan PASS

Override: inventory finalize PASS, Browser PASS, lifecycle override PASS,
cancel PASS, secret scan PASS

Enable: fixture inventory PASS, HTTP PASS, PostgreSQL PASS, receipt PASS,
audit PASS, native PATCH = 1, Node PASS, secret scan PASS
```

Disable and Security Replay reuse durable Gate 7 evidence after confirming
frontend/E2E/package equivalence, runtime-contract equivalence, and identical
Node artifact provenance.

The acceptance-only deterministic bootstrap mapping is:

```text
disable           -> DISABLE_EMAIL
security-replay   -> DISABLE_EMAIL
enable-fixture    -> ENABLE_EMAIL
enable            -> ENABLE_EMAIL
replace           -> REPLACE_EMAIL
remove            -> REMOVE_EMAIL
override          -> OVERRIDE_LIFECYCLE_EMAIL
replace-discovery -> no deterministic bootstrap; scheduler discovery preserved
```

This orchestration did not alter production inventory semantics, poll timing,
Browser timeouts, or Browser tests.

---

## 11. Evidence Conclusion

```text
CONTROL_SOURCE_SHA =
e3b52987a35ed470eba958b3f6764188bb4197f2

FRONTEND_TREE =
58e8f8af4157edf2f78e3cb07586029a7358c334

NODE_VERSION =
v24.19.0

NPM_VERSION =
11.17.0

NPM_CI =
PASS

UNIT_COMPONENT =
PASS

TYPECHECK =
PASS

PRODUCTION_BUILD =
PASS

GENERATED_DRIFT =
PASS

HISTORICAL_ASSET_REGISTRY_FLAKE =
NOT REPRODUCED IN FINAL NODE 24 BASELINE

BROWSER_E2E =
PASS

NON_BROWSER_EXECUTABLE_BASELINE =
PASS

BROWSER_E2E_BASELINE =
PASS

EXECUTABLE_BASELINE =
COMPLETE

TD_INVALIDATION =
NONE

PHASE2_HUMAN_APPROVAL =
APPROVED

STAGE1_FINAL_FREEZE =
YES

STAGE2_IMPLEMENTATION_AUTHORIZED =
YES

STAGE1_STATUS =
CLOSED

STAGE1_EXIT =
PASS
```

Secret hygiene:

```text
SECRET_HYGIENE = PASS
repo-external focused runtimes removed
Compose projects removed
storageState removed
raw secrets not retained
worktree clean
```

The acceptance harness identity is separate from the product/frontend
identity:

```text
PRODUCT_FRONTEND_SHA =
e3b52987a35ed470eba958b3f6764188bb4197f2

ACCEPTANCE_HARNESS_SHA =
4ef6e934637ac886852d26a70970820e887d0a03
```
