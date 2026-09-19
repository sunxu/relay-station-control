# Phase 8 — Stage 2 Frontend Foundation

> Status: **STAGE 2 CLOSED — Final independent review passed**
> Phase: **Phase 8 — Stage 2**
> Scope: **Frontend Foundation Implementation**
> Stage 2 readiness baseline: `e457887717ca461ffd3aca420409f23011b35e5f`
> Stage 1 frontend baseline: `e3b52987a35ed470eba958b3f6764188bb4197f2`
> Stage 1 frontend tree: `58e8f8af4157edf2f78e3cb07586029a7358c334`

## 1. Status and authority

```text
Phase 8 — Stage 2
Frontend Foundation Implementation

Status = CLOSED
Entry readiness = PASS
Stage 1 dependency = CLOSED / SATISFIED
FINAL_STAGE2_IMPLEMENTATION_READINESS = PASS
FINAL_INDEPENDENT_REVIEW = PASS
STAGE2_FINAL_FREEZE = YES
STAGE2_STATUS = CLOSED
STAGE2_EXIT = PASS
STAGE3_IMPLEMENTATION_AUTHORIZED = YES
```

Stage 3 authorization only permits beginning the approved TD-16 real-surface
vertical slices. It does not claim that any Stage 3 slice has been implemented.

The product/frontend implementation baseline remains the Stage 1 baseline. The
Stage 2 commits and acceptance corrective are local implementation history; they
do not redefine the Stage 1 product SHA.

## 2. Stage 2 boundary

Stage 2 owns:

- i18next / react-i18next foundation and `AppLocale`;
- persisted explicit locale preference and `LocaleSwitcher`;
- app locale to Ant Design locale synchronization;
- typed static translation resources, parity, and referenced-key proof;
- `PageShell`, `PageHeader`, and `ReadState`;
- locale-aware formatting;
- deterministic Browser locale fixture and Stage 2 Browser locale acceptance;
- the default `1280x720` Browser viewport;
- locator remediation only when Stage 2 modifies a historical test.

Stage 2 does not own business-page translation migration, Auth/Assets/Topology/
Accounts/Jobs/Problems migration, routing redesign, backend or business
semantics, CSS modernization of existing domain surfaces, repository-wide
historical locator cleanup, or Stage 3 vertical slices.

## 3. Task disposition

```text
S2-1 Foundation Bootstrap = PASS
S2-2 Locale Persistence & Switcher = PASS
S2-3 Translation Proof Infrastructure = PASS
S2-4 Shared Presentation Foundation = PASS
S2-5 Formatting Foundation = PASS
S2-6 Browser Locale Foundation = PASS
S2-7 Locator Remediation = NONE REQUIRED
S2-8 Foundation Acceptance = PASS
```

Ordered local commits:

```text
7e5aab1 feat(web): bootstrap stage2 frontend foundation
96145aa feat(web): add locale persistence and switching
be50838 test(web): enforce translation resource contracts
d89b972 feat(web): add shared frontend presentation primitives
337d16f feat(web): add locale-aware formatting foundation
02e4246 test(web): add deterministic browser locale foundation
3e0606a fix(web): harden stage2 locale foundation
```

## 4. AppLocale contract

```text
AppLocale = "zh-CN" | "en"
storage key = relay-control.locale
```

Resolution order:

```text
valid persisted locale
→ normalized browser locale
→ zh-CN fallback
```

Normalization is:

```text
zh / zh-* → zh-CN
en / en-* → en
unsupported → zh-CN
```

Automatic resolution does not write localStorage. Explicit selection persists
the exact `AppLocale`. Storage read/write failures do not crash the app; a write
failure still applies the selected locale to the current session.

## 5. i18n contract

```text
i18next = 26.4.2
react-i18next = 17.0.14
```

The foundation uses static bundled TypeScript resources, explicit `lng`,
`fallbackLng: "zh-CN"`, `supportedLngs: ["zh-CN", "en"]`,
`initAsync: false`, and `escapeValue: false`.

There is no detector plugin, HTTP backend, localStorage plugin, or runtime
translation service.

```text
RESOURCE_PARITY = PASS
REFERENCED_KEY_COMPLETENESS = PASS
INVALID_LITERAL_KEY_REJECTED = PASS
```

## 6. Provider and presentation boundaries

`FrontendFoundationProvider` owns only AppLocale state, explicit locale change,
i18next binding, Ant Design locale, and Ant Design theme. It does not own auth,
routing, QueryClient, query cache, session, CSRF, or business state.

The QueryClient remains module-level and outside the foundation provider.

The shared presentation foundation is:

```text
PageShell
PageHeader
ReadState
```

`ReadState` supports loading, error, empty, content, and retry presentation.
Callers retain error taxonomy, business semantics, retry eligibility, and domain
state ownership. Foundation defaults are typed translation resources.

## 7. Formatting contract

Formatting uses `Intl.DateTimeFormat` and `Intl.NumberFormat`, with no date
library. Locale changes presentation only; the browser/system timezone remains
the source of timezone truth.

## 8. Browser locale foundation

```text
Playwright = 1.62.1
Browser = Playwright bundled Chromium
Chrome for Testing = 151.0.7922.34
Host execution policy = APPLIED
Global viewport = 1280x720
```

Existing suite-specific responsive overrides remain:

```text
Topology: 1280x900, 390x844
Problems: 390x844
```

The independent Stage 2 locale suite passed 6/6 scenarios:

1. context `zh-CN` with stored `zh-CN`;
2. stored `en` takes precedence over context `zh-CN`;
3. invalid persisted locale falls back;
4. context `en` with stored `en`;
5. context `en` with no stored preference;
6. explicit `en` survives reload.

All new interactions use `getByTestId()`. Historical suites were not modified:

```text
S2_7_REQUIRED_REMEDIATION = NONE
```

## 9. Pre-acceptance corrective

Commit `3e0606a` corrected the reload persistence fixture, localized the
LocaleSwitcher accessible label through the typed resource contract, moved
ReadState default copy into typed resources, and made static i18n initialization
deterministic with `initAsync: false`.

The corrective did not expand Stage 2 scope.

## 10. Non-blocking follow-ups

Known locator debt remains documented and accepted:

```text
POST_POLICY_NONCOMPLIANT:
authentication
node-lifecycle
topology

GRANDFATHERED:
gateway-management
problems
```

`@ant-design/icons` remains a `REMOVE_CANDIDATE`; it has no production import
and is not removed in this closeout. The narrow Ant Design `List` usage in
`OneTimeMaterialPage` is deferred to Stage 3 Auth Slice 1A evaluation.

## 11. Final disposition

```text
TD_INVALIDATION = NONE
STAGE3_SCOPE_ENTERED = NO
P0_BLOCKERS = 0
P1_BLOCKERS = 0
STAGE2_FINAL_FREEZE = YES
STAGE2_STATUS = CLOSED
STAGE2_EXIT = PASS
STAGE3_IMPLEMENTATION_AUTHORIZED = YES
```
