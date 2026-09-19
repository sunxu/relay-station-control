# Phase 8 — Stage 1 Frontend Executable Baseline Evidence

> Status: **NON-BROWSER BASELINE PASS — Browser E2E deferred**
> Phase: **Phase 8 — Stage 1**
> Evidence type: **Frontend executable baseline**
> Control source: `e3b52987a35ed470eba958b3f6764188bb4197f2`
> Frontend tree: `58e8f8af4157edf2f78e3cb07586029a7358c334`

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
| Browser E2E | **DEFERRED** | intentionally not executed in this evidence pass |

Overall:

```text
NON_BROWSER_EXECUTABLE_BASELINE = PASS
EXECUTABLE_BASELINE_REMAINING = BROWSER_E2E_ONLY
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

Browser E2E was intentionally deferred for a later Stage 1 executable pass.

Current evidence state:

```text
BROWSER_E2E = DEFERRED
```

Deferred is not PASS.

The later Browser evidence must be bound to the accepted final Stage 1 source/artifact baseline and record the relevant production-like runtime provenance.

Until that evidence is present:

```text
STAGE1_FINAL_FREEZE_ELIGIBLE = NO
```

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
DEFERRED

NON_BROWSER_EXECUTABLE_BASELINE =
PASS

EXECUTABLE_BASELINE_REMAINING =
BROWSER_E2E_ONLY
```

No code changes, test changes, dependency changes, timeout changes, commit, or push were required to obtain this baseline.
