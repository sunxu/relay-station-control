# Relay Station Phase 8 — Stage 2 Formal Implementation Readiness

> Status: **PASS — S2-1 FOUNDATION BOOTSTRAP AUTHORIZED TO START**
> Review date: 2026-09-19
> Mode: Formal readiness / no retained implementation in this review
> Current Control HEAD: `4ae621afaed692f2c4510a05746b64478edb7504`
> Frozen product/frontend source: `e3b52987a35ed470eba958b3f6764188bb4197f2`
> Frozen frontend tree: `58e8f8af4157edf2f78e3cb07586029a7358c334`
> Acceptance harness: `4ef6e934637ac886852d26a70970820e887d0a03`

## 1. Final disposition

```text
STAGE2_READINESS_ENTRY_GATE = PASS
PRE_READINESS_DELTA_REVALIDATION = PASS
TD_IMPLEMENTATION_CONTRACT = READY
APP_LOCALE_CONTRACT = READY
FOUNDATION_BOUNDARY = READY
TRANSLATION_CORPUS = READY
TRANSLATION_PROOF_CONTRACT = READY
STYLING_MIGRATION_MANIFEST = READY
E2E_REMEDIATION_MANIFEST = READY
BROWSER_LOCALE_FIXTURE_CONTRACT = READY
RESPONSIVE_CONTRACT = READY
STAGE2_TASK_DECOMPOSITION = READY
STAGE2_EXIT_CRITERIA = READY
P0_BLOCKERS = 0
P1_BLOCKERS = 0
FINAL_STAGE2_IMPLEMENTATION_READINESS = PASS
NEXT = START_S2_1_FOUNDATION_BOOTSTRAP
```

## 2. Entry and provenance

Current remote `main` resolves to `4ae621afaed692f2c4510a05746b64478edb7504`.

Stage 1 is closed and records:

```text
Stage 0 = CLOSED
Phase 2 Human Approval = APPROVED
TD-01..TD-16 = VALID / APPROVED / NOT_INVALIDATED
Post-Stage0 delta refresh = PASS
Non-Browser baseline = PASS
Browser E2E baseline = PASS
Executable baseline = COMPLETE
Stage 1 final freeze = YES
Stage 1 exit = PASS
Stage 2 implementation = AUTHORIZED
```

The current `web/` tree is still exactly:

```text
58e8f8af4157edf2f78e3cb07586029a7358c334
```

and is identical between the frozen product/frontend source and current Stage-1-closeout main. Later acceptance/docs commits did not create a new frontend implementation identity.

## 3. Pre-readiness delta revalidation

The old pre-readiness baseline was historical and is no longer used as execution truth. Its foundation/i18n/testing design remains structurally valid.

Revalidation against current main found:

```text
web/package.json          unchanged
web/package-lock.json     unchanged
web/playwright.config.ts  unchanged
web/src/main.tsx          unchanged
web/src/styles.css        unchanged
web/src/time.ts           unchanged
```

The material frontend delta since the old pre-readiness baseline is the completed Stage-0 credential work: generated API credential fields, Asset Registry credential UI/tests, and a small authentication E2E fixture adjustment. Those changes are already included in the frozen Stage 1 frontend tree and do not invalidate TD-01..TD-16.

## 4. Dependency readiness

Recommended exact Stage-2 pins remain:

```text
i18next = 26.4.2
react-i18next = 17.0.14
```

Current upstream package metadata establishes:

```text
react-i18next 17.0.14:
  i18next >= 26.2.0
  react >= 16.8.0
  typescript ^5 || ^6 || ^7

upstream dev matrix:
  react 19.2.8
  react-dom 19.2.8
  typescript 6.0.3
  vitest 4.1.11
  i18next ^26.4.0

i18next 26.4.2:
  typescript ^5 || ^6 || ^7
  upstream dev: TypeScript 6 / Vitest 4.1.11
```

Therefore:

```text
DEPENDENCY_METADATA_COMPATIBILITY = PASS
CANDIDATE_I18NEXT_VERSION = 26.4.2
CANDIDATE_REACT_I18NEXT_VERSION = 17.0.14
```

The complete prospective `npm install -> typecheck -> test -> build` disposable proof was not executed in this review environment. The pre-readiness contract makes that proof optional during formal readiness, but any such proof must be disposable before retained implementation. It is therefore frozen as the first **S2-1 pre-commit gate**:

```text
S2_1_DEPENDENCY_EXECUTION_GATE:
  npm install exact pins
  inspect package/package-lock diff
  npm run typecheck
  npm test
  npm run build
  generated drift clean
  PASS required before first retained S2-1 commit
```

No remote temporary commit/branch was created to simulate this proof.

## 5. AppLocale contract

Canonical contract:

```text
type AppLocale = "zh-CN" | "en"
storage key = relay-control.locale
```

Resolution:

```text
valid stored preference
-> navigator.language normalization
-> zh-CN fallback
```

Normalization:

```text
zh / zh-* -> zh-CN
en / en-* -> en
other      -> zh-CN
```

Rules:

- automatic resolution never writes a preference;
- explicit user selection updates in-memory locale and persists the exact `AppLocale`;
- invalid stored values are ignored and are not automatically rewritten;
- localStorage read failure falls back to browser/fallback resolution;
- localStorage write failure does not cancel the current-session locale change and does not create an alternate persistence store.

Repository search found no existing production `relay-control.locale` key, so this does not collide with an established application namespace.

## 6. Foundation boundary

Allowed Stage-2 foundation:

```text
web/src/foundation/
  FrontendFoundationProvider.tsx
  locale.ts
  i18n.ts
  resources.ts
  theme.ts
  LocaleSwitcher.tsx
  PageShell.tsx
  ReadState.tsx

web/src/format/
  dateTime.ts
  number.ts
```

`ReadState` is the canonical shared read-state primitive; do not create a parallel `AsyncState` abstraction unless an actual independent need appears.

`FrontendFoundationProvider` owns only:

```text
AppLocale state
static i18next binding
AntD locale mapping
AntD ThemeConfig
```

It must not own authentication, routing, Query cache, CSRF/session refresh, domain state, asset lifecycle, inventory truth, or account-operation state.

The existing module-level QueryClient remains one stable instance across locale changes.

Forbidden generic wrappers remain:

```text
AppButton / AppInput / AppSelect / AppTable / AppModal / AppDrawer / AppCard
```

## 7. Locale / AntD integration

Mapping:

```text
zh-CN -> antd/es/locale/zh_CN
en    -> antd/es/locale/en_US
```

The app locale and AntD locale must change in one state transition. i18next receives an explicit `lng`; no browser detector, HTTP backend, localStorage plugin, or competing locale precedence policy is introduced.

Resources are statically bundled.

## 8. Translation corpus and resource boundary

The final current source inventory confirms user-facing hardcoded copy across the existing real surfaces, including:

```text
App/Auth shell
Login / Bootstrap / Activation / one-time material
Management
Asset Registry, including Stage-0 credential copy
Jobs
Topology
Problems
Account list/details/capacity/incidents/history/operations
```

Large current copy hotspots include Asset Registry, Topology, Management, Jobs and Account Operations. This inventory is **migration input**, not authorization to translate every page during Stage 2.

Classification is frozen as:

### TRANSLATE

page/navigation titles, actions, form labels, placeholders, validation guidance, empty/loading/unavailable messages, modal/drawer copy, confirmations, human status labels, human-readable aria labels.

### RAW

command/operation/job/request IDs, account/instance IDs, provider/model identifiers, commit SHAs/digests, stable backend error codes, and raw forensic protocol/lifecycle/execution values.

### RAW + LOCALIZED LABEL

execution state, quality, capacity, availability, operation outcome and stable error-code presentation where both human meaning and raw evidence are useful.

Hard invariant:

```text
outcome_unknown != success != failed
```

Stage 2 initial translation resources must contain **foundation-owned keys only**. Existing domain-page copy migrates with Stage 3 vertical slices rather than through a Stage-2 big-bang translation diff.

## 9. Translation proof contract

Use TypeScript resource objects with `zh-CN` as the canonical shape and `en` constrained to the same recursive key shape.

Required proof:

```text
resource structural parity
referenced-key completeness
typed/literal t(...) usage
no unbounded dynamic key construction
```

Use `i18next` `CustomTypeOptions` to bind resource typing. Raw business values map through finite typed presentation maps to known translation keys; do not use unrestricted constructions such as `t(`status.${rawValue}`)`.

No extraction service or translation backend is required.

## 10. Styling migration manifest

Current private AntD DOM coupling remains exactly concentrated in `web/src/styles.css`:

```text
.secret-list .ant-list-item
.topology-view .ant-card
.topology-view .ant-card-head-wrapper
.topology-view .ant-card-head-title
.topology-view .ant-table-wrapper
.topology-view .ant-table-cell
```

Disposition:

```text
KEEP_APP_CSS:
  centered-page, auth-card, management-page, form-column,
  asset-card-grid, secret-panel and other true app layout semantics

MOVE_TO_ANTD_TOKEN:
  only product-wide component theme semantics when an actual need is defined

MOVE_TO_SEMANTIC_STYLES:
  Topology Card/Table internal-region rules when Topology is migrated

REMOVE_WHEN_SURFACE_MIGRATES:
  secret-list .ant-list-item when OneTimeMaterialPage/Auth slice is migrated
```

Current page-local inline style props remain surface-migration debt; Stage 2 does not normalize unrelated pages merely to modernize styling.

No parallel token system is authorized.

## 11. AntD List and icons

Current production `List` use is still limited to `OneTimeMaterialPage` recovery codes. Do not add new legacy List use and do not wrap it in a foundation abstraction. Evaluate plain semantic JSX vs Listy only when Stage 3 Auth Slice 1A touches that surface.

`@ant-design/icons` is still present only in `package.json` / `package-lock.json`; current production source has no import.

Disposition:

```text
@ant-design/icons = REMOVE_CANDIDATE_DEFERRED
```

Do not combine this unrelated cleanup with S2-1's i18n lockfile change unless it receives a separate explicit scope decision.

AntD remains pinned at the existing 6.6.1 during Stage 2 foundation work; no AntD upgrade is required for readiness.

## 12. E2E remediation manifest

Current classification remains:

```text
POST_POLICY_NONCOMPLIANT:
  authentication.spec.ts
  node-lifecycle.spec.ts
  topology.spec.ts

GRANDFATHERED:
  gateway-management.spec.ts
  problems.spec.ts

CURRENT_POLICY_COMPLIANT:
  account-operations-* specs
```

Stage 2 should avoid modifying those five historical suites. Add a new foundation-locale Browser spec and locale fixture/helper instead. Therefore no historical locator remediation is mandatory merely to start Stage 2.

If Stage 2 later modifies any historical Playwright file, every interaction locator in that file must be brought to current `getByTestId()` policy in the same change.

## 13. Browser locale fixture contract

The fixture owns both:

```text
BrowserContext locale
relay-control.locale
```

It must preserve authenticated storage state, cookies and unrelated localStorage values.

Recommended deterministic model:

1. deep-copy the supplied storageState;
2. reconcile only the locale entry for the application origin before the first page navigation;
3. preserve cookies and every unrelated origin/localStorage entry;
4. pair the transformed state with the requested BrowserContext `locale`.

Required cases:

```text
1. context zh-CN + stored zh-CN -> zh-CN
2. context en + stored en -> en
3. context en + no stored preference -> en; no preference auto-written
4. context zh-CN + stored en -> en
5. context zh-CN + invalid stored value -> zh-CN
6. explicit zh-CN -> select en -> reload -> en persists
```

Formal Browser evidence continues to use repository-authorized Playwright bundled Chromium with host execution where required.

## 14. LocaleSwitcher boundary

`LocaleSwitcher` is Stage-2-owned presentation infrastructure.

It may expose:

```text
locale-selector
locale-option-zh-CN
locale-option-en
```

or an equivalently stable semantic test-ID contract finalized during S2-2.

It must not own localStorage directly, initialize i18next, touch auth/session, change route/backend, or require full-page reload as its primary switching mechanism.

It may be mounted once at the application foundation boundary so explicit user switching can receive Browser proof without migrating domain pages.

## 15. Formatting boundary

Stage 2 adds new locale-aware helpers under `web/src/format/` using native `Intl` and no explicit `timeZone` option. Browser/system timezone remains truth.

Stage 2 does **not** mass-rewrite existing page callers. Adoption of the new formatting helpers occurs with the owning Stage-3 surface slices.

Required unit proof: same instant + same system timezone + different app locale may produce different presentation while represented temporal truth remains unchanged.

## 16. Responsive contract

```text
global primary viewport = 1280x720
Topology overrides = 1280x900 and 390x844
Problems override = 390x844
```

S2-6 may add the global viewport to Playwright config. Existing suite-level `setViewportSize` calls remain authoritative and must not be removed or normalized.

## 17. Stage-2 implementation plan

### S2-1 — Foundation bootstrap

- exact-pin `i18next@26.4.2` and `react-i18next@17.0.14`;
- first run the mandatory dependency execution gate before retained commit;
- add AppLocale resolver, static resources, i18n instance, `FrontendFoundationProvider`, AntD locale/theme mapping;
- no domain-page translation.

### S2-2 — Locale persistence and switcher

- implement explicit-preference persistence and storage-failure semantics;
- implement/mount thin LocaleSwitcher at the app foundation boundary;
- unit/component proof.

### S2-3 — Translation proof infrastructure

- typed resource shape;
- zh-CN/en parity;
- referenced-key completeness;
- finite presentation-key mappings.

### S2-4 — Shared presentation primitives

- PageShell;
- ReadState;
- no domain semantics and no broad adoption into existing pages yet.

### S2-5 — Formatting foundation

- locale-aware date/time and number/percent helpers;
- timezone-truth regression tests;
- no mass caller migration.

### S2-6 — Browser locale foundation

- explicit global 1280x720 viewport;
- locale/storageState fixture;
- new policy-compliant foundation-locale Browser spec;
- representative zh-CN/en and persistence/reload proof;
- preserve Topology/Problems viewport overrides.

### S2-7 — Conditional locator remediation

No historical test rewrite by default. If a historical Playwright file is modified or blocks Stage 2, remediate all interactions in that file.

### S2-8 — Foundation acceptance

Run typecheck, unit/component tests, production build, generated drift check, translation proofs, Browser locale smoke, accessibility review and business-behavior regression checks.

## 18. Stage-2 exit criteria

| Requirement | Required result |
|---|---|
| exact dependency pins | PASS |
| S2-1 dependency execution gate | PASS |
| AppLocale parsing/normalization | PASS |
| persisted preference precedence | PASS |
| no auto-persist on navigator resolution | PASS |
| storage read/write failure semantics | PASS |
| app locale / i18next / AntD synchronization | PASS |
| QueryClient identity across locale switch | PASS |
| zh-CN/en resource parity | PASS |
| referenced-key completeness | PASS |
| raw IDs/codes preserved | PASS |
| `outcome_unknown` distinction preserved | PASS |
| Intl formatting / timezone truth | PASS |
| PageShell component proof | PASS |
| ReadState component proof | PASS |
| LocaleSwitcher accessibility | PASS |
| Browser zh-CN/en representative proof | PASS |
| explicit locale switch persists across reload | PASS |
| global 1280x720 | PASS |
| Topology/Problems responsive overrides preserved | PASS |
| every touched E2E file interaction-locator compliant | PASS |
| `npm run typecheck` | PASS |
| unit/component suite | PASS |
| production build | PASS |
| generated drift | CLEAN |
| official bundled-Chromium Browser foundation acceptance | PASS |
| product/domain business semantics changed | NO |
| Stage-3 page/domain migration performed | NO |

## 19. Non-blocking follow-ups

These do not block Stage 2 readiness:

- existing accepted locator-policy debt in untouched historical suites;
- legacy List use in OneTimeMaterialPage, owned by Stage 3 Auth migration;
- unused `@ant-design/icons` dependency cleanup candidate;
- pre-existing private AntD selectors, owned by the corresponding Stage 3 surface migration;
- project-wide deployment completeness and Node-freeze-boundary follow-ups tracked outside the Stage-2 frontend foundation scope.

## 20. Final readiness decision

```text
P0_BLOCKERS = 0
P1_BLOCKERS = 0
FINAL_STAGE2_IMPLEMENTATION_READINESS = PASS
S2_1_FOUNDATION_BOOTSTRAP = AUTHORIZED_TO_START
```

No repository source, package, test, CSS, Playwright config, commit or remote branch was modified by this readiness review.
