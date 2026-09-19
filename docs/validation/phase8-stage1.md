# Phase 8 — Stage 1 Frontend Baseline & Technical Decisions

> Status: **EXECUTABLE BASELINE COMPLETE — Human approval pending**
> Phase: **Phase 8 — Stage 1**
> Scope: **Frontend Baseline & Technical Decisions**
> Current Control source baseline: `e3b52987a35ed470eba958b3f6764188bb4197f2`
> Current frontend tree: `58e8f8af4157edf2f78e3cb07586029a7358c334`
> Acceptance harness: `4ef6e934637ac886852d26a70970820e887d0a03` (acceptance-only; not a frontend baseline)
> Historical pre-Stage-0 Control baseline: `7e73fb87ea46e8930afd94a3bd25f009ece5bedb`
> Stage 0: **CLOSED**
> Post-Stage-0 delta refresh: **PASS**
> Canonical technical decisions TD-01..TD-16: **NO INVALIDATION**
> Non-Browser executable baseline: **PASS**
> Browser E2E: **PASS**
> Phase 2 human approval: **PENDING**
> Stage 1 final freeze: **PENDING HUMAN APPROVAL**
> Stage 2 implementation: **NOT AUTHORIZED**

---

## 1. Purpose

This document is the authoritative Phase 8 Stage 1 frontend baseline and technical-decision record for `relay-station-control`.

It consolidates the useful outputs of the historical Stage 1 Phase 1 Frontend Baseline Inventory and Stage 1 Phase 2 Frontend Technical Decision Review, then reconciles them with the completed Stage 0 implementation and the current post-Stage-0 repository state.

This document replaces those historical drafts as the source of current Stage 1 truth. Their obsolete Stage 0 status and pre-Stage-0 executable assumptions are not current truth.

Project-wide validation rules are defined by:

`docs/validation/principles.md`

This document does not authorize Stage 2 implementation.

---

## 2. Stage 1 Boundary

Phase 8 Stage 1 is:

```text
Frontend Baseline & Technical Decisions
```

Stage 1 owns frontend baseline, styling/token strategy, shared frontend foundation boundary, i18n strategy, locale rules, Ant Design locale synchronization, formatting policy, routing strategy, desktop/responsive acceptance, Browser locale contract, translation-resource contract, presentation-mapping ownership, accessibility foundation, E2E locator-remediation ownership, Stage 3 migration slicing, post-Stage-0 delta reconciliation, and executable frontend baseline.

Stage 1 does not change backend/database/business semantics, start Stage 2 foundation implementation, or start Stage 3 page migration.

---

## 3. Historical Input Baseline

### 3.1 Phase 1 historical baseline

The historical Phase 1 inventory reviewed:

```text
Control:
7e73fb87ea46e8930afd94a3bd25f009ece5bedb
```

Frontend stack:

```text
React 19.2.8
Ant Design 6.6.1
TanStack React Query 5.102.3
Vite 8.2.2
TypeScript 6.0.3
Orval 8.26.0
Vitest 4.1.11
Playwright 1.62.1
```

Baseline facts:

```text
router package = NONE
i18n package = NONE
app-owned generic design-system primitive layer = NONE
```

Current UI composition was Ant Design direct usage + small global `styles.css` + inline styles + domain composites.

Routing was `AuthContext` + `AuthRoute` + `history.pushState` + `popstate` + pathname mapping.

The major frontend hotspots were `AssetRegistryView`, `TopologyView`, `ManagementPage`, and `AccountOperationsPanel`.

Account List, Account Details, and Account Operations were confirmed as nested product surfaces rather than independent route-level pages.

### 3.2 Historical executable state

The Phase 1 review was source-only:

```text
npm run typecheck = NOT EXECUTED
npm run test      = NOT EXECUTED
npm run build     = NOT EXECUTED
npm run test:e2e  = NOT EXECUTED
```

No historical frontend executable PASS is inferred from that inventory.

---

## 4. Post-Stage-0 Delta Refresh

Stage 0 is now closed.

### 4.1 Current source identity

```text
CONTROL_SOURCE_SHA =
e3b52987a35ed470eba958b3f6764188bb4197f2

FRONTEND_TREE =
58e8f8af4157edf2f78e3cb07586029a7358c334

ACCEPTANCE_HARNESS_SHA =
4ef6e934637ac886852d26a70970820e887d0a03

The acceptance harness identity is recorded separately and is not a new
frontend implementation baseline.
```

### 4.2 Delta result

| Area | Historical state | Current state | Result |
|---|---|---|---|
| Frontend dependencies | React/AntD/TanStack; no router/i18n | unchanged | PASS |
| Routing | custom `AuthContext`/history model | unchanged | PASS |
| Styling | AntD + global CSS + inline styles | unchanged | PASS |
| Credential UI | legacy `reader_secret_ref` contract | protected write-only credentials | EXPECTED CHANGE |
| Generated API | legacy secret-reference fields | credential request fields + `secret_configured` read state | EXPECTED CHANGE |
| Test-id architecture | semantic domain-oriented hooks | preserved | PASS |
| Stage 1 TD compatibility | pre-Stage-0 decisions | no invalidating delta | PASS |

Canonical delta answers:

```text
NEW_FRONTEND_DEPENDENCY = NO
NEW_ROUTE = NO
NEW_STYLING_MECHANISM = NO
CREDENTIAL_UI_CHANGED = YES
TD_INVALIDATION = NONE
```

### 4.3 Stage 0 credential UI delta

Gateway:

```text
reader_secret_ref
→ directory_credential
```

Node:

```text
reader_secret_ref
→ management_credential
```

Current behavior preserves Register missing/set, Edit keep/set/clear, Replace explicit set or unconfigured, `secret_configured`, write-only credential input, and no saved credential echo.

```text
STAGE0_CREDENTIAL_UI_DELTA = PASS
```

---

## 5. Final Stage 1 Source Baseline

```text
Control source:
e3b52987a35ed470eba958b3f6764188bb4197f2

Frontend tree:
58e8f8af4157edf2f78e3cb07586029a7358c334

web/package.json Git blob:
4cebc420e819ae063dff723e0579224aad30bf6a

web/package-lock.json Git blob:
276b28e70353d7f33b8582ddc02037b5cd46aec1
```

No later Stage 0 documentation-only commit changed the selected frontend tree.

---

## 6. Canonical Technical Decisions

The post-Stage-0 refresh found no invalidating delta.

### TD-01 — UI Library

Keep Ant Design 6. Do not add or replace it with a second component platform for Phase 8.

### TD-02 — Styling / Design Tokens

Canonical hierarchy:

```text
1. Ant Design ConfigProvider theme tokens
2. Ant Design component tokens / semantic classNames/styles
3. narrow app-owned CSS variables for application layout semantics
4. ordinary scoped CSS where needed
5. inline style only for genuinely dynamic/local geometry
```

Existing `.ant-*` overrides are migration debt to remove when the owning surface is migrated.

### TD-03 — Shared Primitive Boundary

Create a thin application foundation only: `FrontendFoundationProvider`, `PageShell` / `PageHeader`, `ReadState` / `AsyncState`, and `LocaleSwitcher` where useful.

Do not create generic wrappers merely to rename AntD primitives.

### TD-04 — i18n Library

Use:

```text
i18next
+
react-i18next
```

Supported locales:

```text
zh-CN
en
```

Use static bundled resources. No remote translation backend, server-side locale service, or detector plugin.

### TD-05 — Locale Resolution / Persistence

```text
AppLocale = "zh-CN" | "en"
```

Resolution:

```text
1. valid persisted app locale
2. normalized navigator/browser locale
3. zh-CN fallback
```

Normalize `zh / zh-* → zh-CN`, `en / en-* → en`, other → `zh-CN`.

Only explicit user selection is persisted; resolution alone does not persist.

### TD-06 — Ant Design Locale Synchronization

App locale and AntD locale move together through a single foundation provider.

### TD-07 — Raw Values vs Localized Presentation

Stable machine/business values remain raw. Localized labels are presentation only.

Hard invariant:

```text
outcome_unknown
!= success
!= failed
```

### TD-08 — Formatting

Keep one application formatting boundary. Locale changes presentation; browser/system local timezone remains truth. Use native `Intl`; no date library is required.

### TD-09 — Routing

Keep current `AuthContext` / browser-history routing for Phase 8. Do not add React Router or new Account route models.

### TD-10 — Desktop / Responsive Acceptance

Primary global Stage 2–4 desktop Browser viewport:

```text
1280x720
```

Existing suite-specific responsive acceptance remains authoritative and must not be weakened.

### TD-11 — Test Locale Contract

Primary Browser locale is `zh-CN`; `en` receives representative coverage.

The locale fixture owns both BrowserContext locale and application persisted locale state. Shared storage state must not override requested locale. Translated text must not become interaction identity.

### TD-12 — Translation Resource Contract

Use source-controlled `zh-CN` and `en` resources.

Required proof:

```text
resource parity
+
referenced-key completeness
```

Keys describe stable product semantics.

### TD-13 — State / Error Presentation Ownership

Centralize presentation mappings and shared read-state structure only where semantics are genuinely shared. Do not create a new generic business taxonomy.

### TD-14 — Accessibility Foundation

Preserve semantic labels, accessible names, keyboard/focus behavior, loading semantics, and non-color-only state communication. No new mandatory accessibility framework is frozen.

### TD-15 — E2E Locator Remediation

Current classification:

```text
POST_POLICY_NONCOMPLIANT:
- authentication.spec.ts
- node-lifecycle.spec.ts
- topology.spec.ts

GRANDFATHERED:
- gateway-management.spec.ts
- problems.spec.ts

CURRENT_POLICY_COMPLIANT:
- account-operations-* suites
```

Grandfathered suites harden when modified/blocking; post-policy drift already requires remediation; current compliant suites preserve semantic test-id interaction contracts.

### TD-16 — Stage 3 Migration Slices

```text
Slice 1A — Auth flows
Slice 1B — Management root / shared shell
Slice 2 — Asset Registry
Slice 3 — Topology Core
Slice 4 — Account Read Surfaces
Slice 5 — Account Operations
Slice 6 — Jobs
Slice 7 — Problems
```

Migrate real current surfaces, not imagined route-level pages.

---

## 7. Cross-Cutting YAGNI Guardrails

Stage 1 does not authorize a new router platform, CSS utility framework, generic design-system framework, server locale persistence, translation-management backend, page-wide preparatory rewrite, or new Account route model.

---

## 8. Non-Browser Executable Baseline

Formal toolchain:

```text
Node v24.19.0
npm 11.17.0
node: /opt/homebrew/opt/node@24/bin/node
npm:  /opt/homebrew/opt/node@24/bin/npm
```

Results:

| Check | Result |
|---|---|
| `npm ci` | PASS |
| unit/component | PASS — 31 files / 251 tests |
| AssetRegistry historical flaky cases | PASS — 16/16 |
| `npm run typecheck` | PASS |
| `npm run build` | PASS |
| `generate:api` | PASS |
| generated-source drift | PASS — `git diff --exit-code` clean |
| Vite production build | PASS — 1569 modules, ~2.99s |
| Browser E2E | PASS |

Build output:

```text
internal/webui/dist generated
712 files
~34 MB
```

```text
NON_BROWSER_EXECUTABLE_BASELINE = PASS
EXECUTABLE_BASELINE = COMPLETE
```

Detailed evidence:

`docs/evidence/phase8-stage1-frontend-executable-baseline.md`

### Browser and focused acceptance

The repository-authoritative Browser baseline completed with host execution
according to `docs/testing/ACCEPTANCE_E2E_POLICY.md`.

```text
GENERAL_BROWSER_E2E = PASS
6 passed
duration = 40.7s

UPLOAD = PASS
ENABLE_FIXTURE = PASS
ENABLE = PASS
REPLACE = PASS
REMOVE = PASS
OVERRIDE = PASS
DISABLE = REUSED / VALID
SECURITY_REPLAY = REUSED / VALID
```

Coverage remained:

```text
global desktop = 1280x720
Problems = 390x844
Topology = 1280x900, 390x844
```

The final Browser runtime used Playwright bundled Chromium, Chrome for
Testing `151.0.7922.34`. Host execution was required after an initial
bundled Chromium startup failure at the host/sandbox boundary:

```text
HOST_EXECUTION_POLICY_APPLIED = YES
BROWSER_HOST_EXECUTION_BLOCKER = RESOLVED
```

No system Chrome, unsafe Chromium flags, Playwright configuration changes,
or Browser test changes were used.

Disable and Security Replay reuse durable Gate 7 evidence with equivalent
frontend/E2E/package inputs, runtime contract, and Node artifact provenance.

The acceptance-only deterministic inventory bootstrap is orchestration, not
production scheduler behavior:

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

It did not change production inventory semantics, poll timing, Browser
timeouts, or Browser tests.

---

## 9. Historical Flaky Event Reconciliation

A GitHub Actions run on the current Stage 1 source baseline reported two timeouts in `web/src/pages/AssetRegistryView.test.tsx`.

Subsequent diagnosis established:

```text
classification = FLAKY_UNKNOWN
Stage 0 causal link = NO
fix required = NO
patch = NONE
timeout increase = NONE
```

The full file passed 3/3 under Node 26 diagnostic runs, each affected test passed 3/3 in isolation, and the formal Node 24 baseline passed all 251 tests with `AssetRegistryView.test.tsx` at 16/16.

The historical failure remains part of the record. Because the historical pre-Stage-0 executable baseline was never run, it cannot be classified as a Stage 0 regression from available evidence.

---

## 10. Browser / E2E Baseline

Browser E2E and required focused acceptance are complete.

```text
BROWSER_E2E_BASELINE = PASS
EXECUTABLE_BASELINE = COMPLETE
```

```text
TD_INVALIDATION = NONE
PHASE2_HUMAN_APPROVAL = PENDING
STAGE1_FINAL_FREEZE_ELIGIBLE = PENDING_HUMAN_APPROVAL
STAGE1_FINAL_FREEZE = NO
STAGE2_IMPLEMENTATION_AUTHORIZED = NO
```

---

## 11. Human Approval and Freeze State

Historical Phase 2 review reached:

```text
P0 = 0
P1 = 0
P2 = 1
```

The remaining P2 was clerical and corrected without changing TD-01..TD-16.

Final Phase 2 review state:

```text
READY FOR HUMAN APPROVAL
```

Current status:

```text
PHASE2_HUMAN_APPROVAL = PENDING
```

Human approval may advance the decisions to `TECHNICAL_DECISIONS_APPROVED_PENDING_GATES`, but does not itself create the final Stage 1 freeze.

---

## 12. Final Stage 1 Freeze Gate

Required ordering:

```text
Stage 0 closeout
↓
post-Stage-0 frontend delta refresh
↓
select final Stage 1 Control/frontend baseline
↓
run executable baseline on that same baseline
↓
Phase 2 human approval
↓
no invalidating TD delta
↓
P0/P1 = 0
↓
STAGE1_FRONTEND_TECHNICAL_DECISIONS_FROZEN
↓
STAGE1 EXIT
```

Current state:

```text
Stage 0 closeout                         PASS
Post-Stage-0 frontend delta refresh      PASS
Final Stage 1 source baseline selected   PASS
TD invalidation                          NONE
Non-Browser executable baseline          PASS
Browser E2E                              PASS
Phase 2 human approval                   PENDING
P0/P1                                    0/0
```

Therefore the executable gate is complete, but final freeze remains pending
human approval:

```text
STAGE1_FRONTEND_TECHNICAL_DECISIONS_FROZEN = NO
STAGE1_FINAL_FREEZE_ELIGIBLE = PENDING_HUMAN_APPROVAL
STAGE2_IMPLEMENTATION_ALLOWED = NO
```

---

## 13. Stage 2 / Stage 3 Ownership

If Stage 1 later exits successfully, Stage 2 owns foundation implementation: AntD theme/foundation provider, narrow app tokens, i18next/react-i18next setup, locale resolver/persistence, AntD locale mapping, formatting helpers, thin shared page/read-state primitives, resource parity/completeness proof, and deterministic locale test fixture.

Stage 3 owns product-surface migration using TD-16 slices.

Business-sensitive auth, asset lifecycle, topology truth domains, and account-operation semantics must remain intact.

---

## 14. Review History Summary

### Phase 1

Historical inventory established frontend stack, routing, component/styling structure, i18n absence, E2E coupling, business-sensitive boundaries, and the requirement for a post-Stage-0 refresh. It intentionally did not execute the frontend baseline.

### Phase 2 Round 1

```text
P0 = 0
P1 = 5
P2 = 2
```

Corrections included canonical TD numbering, Stage 0 provenance, freeze sequencing, deterministic locale fixture, responsive acceptance preservation, referenced-key completeness, and Stage 3 slice granularity.

### Phase 2 Round 2

```text
P0 = 0
P1 = 2
P2 = 0
```

Corrections included explicit locale persistence semantics and final executable-baseline provenance.

### Phase 2 Round 3

```text
P0 = 0
P1 = 1
P2 = 0
```

Correction separated latest repository state from latest formal Stage 0 gate result.

### Phase 2 Round 4

```text
P0 = 0
P1 = 0
P2 = 1
```

The remaining P2 was clerical only. No canonical TD changed.

Disposition:

```text
PHASE2_HUMAN_APPROVAL_READY
```

### Post-Stage-0 reconciliation

```text
Post-Stage0 delta refresh = PASS
TD invalidation = NONE
Final Stage 1 source baseline selected
Non-Browser executable baseline = PASS
Browser E2E = PASS
```

---

## 15. Current Authoritative Disposition

```text
PHASE_8_STAGE_1 = IN_PROGRESS

STAGE0 = CLOSED

POST_STAGE0_DELTA_REFRESH = PASS

FINAL_STAGE1_CONTROL_SOURCE_BASELINE =
e3b52987a35ed470eba958b3f6764188bb4197f2

FINAL_STAGE1_FRONTEND_TREE =
58e8f8af4157edf2f78e3cb07586029a7358c334

TD_INVALIDATION = NONE

NON_BROWSER_EXECUTABLE_BASELINE = PASS

BROWSER_E2E_BASELINE = PASS

EXECUTABLE_BASELINE = COMPLETE

ACCEPTANCE_HARNESS_SHA =
4ef6e934637ac886852d26a70970820e887d0a03

PHASE2_HUMAN_APPROVAL =
PENDING

STAGE1_FRONTEND_TECHNICAL_DECISIONS_FROZEN =
NO

STAGE2_IMPLEMENTATION_ALLOWED =
NO

STAGE1_FINAL_FREEZE =
NO
```
