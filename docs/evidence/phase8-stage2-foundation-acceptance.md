# Phase 8 — Stage 2 Foundation Acceptance Evidence

> Status: **STAGE 2 CLOSED — Foundation acceptance passed**
> Evidence type: **Stage 2 frontend foundation executable evidence**
> Stage 2 readiness baseline: `e457887717ca461ffd3aca420409f23011b35e5f`
> Stage 1 frontend baseline: `e3b52987a35ed470eba958b3f6764188bb4197f2`
> Stage 1 frontend tree: `58e8f8af4157edf2f78e3cb07586029a7358c334`

## 1. Provenance

The Stage 2 implementation sequence is:

```text
7e5aab1  S2-1 Foundation Bootstrap
96145aa  S2-2 Locale Persistence & Switcher
be50838  S2-3 Translation Proof Infrastructure
d89b972  S2-4 Shared Presentation Foundation
337d16f  S2-5 Formatting Foundation
02e4246  S2-6 Browser Locale Foundation
3e0606a  Pre-acceptance corrective
```

The acceptance corrective is separate from the Stage 1 product/frontend
baseline and does not replace the Stage 1 source SHA.

## 2. Toolchain

```text
Node = v24.19.0
npm = 11.17.0
Playwright = 1.62.1
Browser = Playwright bundled Chromium
Chrome for Testing = 151.0.7922.34
```

Dependency pins:

```text
i18next = 26.4.2
react-i18next = 17.0.14
DEPENDENCY_PIN = PASS
```

## 3. Static, unit, and build evidence

```text
TYPECHECK = PASS
UNIT_COMPONENT = PASS
UNIT_COMPONENT_TEST_FILES = 37
UNIT_COMPONENT_TESTS = 278
PRODUCTION_BUILD = PASS
generate:api = PASS
GENERATED_DRIFT = NONE
```

The production-like frontend build completed successfully and generated the
internal web UI artifact. No generated source drift remained after the build.

## 4. Foundation proof matrix

```text
RESOURCE_PARITY = PASS
REFERENCED_KEY_COMPLETENESS = PASS
INVALID_LITERAL_KEY_REJECTED = PASS
LOCALE_CONTRACT = PASS
STORAGE_FAILURE_POLICY = PASS
ANTD_LOCALE_SYNC = PASS
FORMATTING_FOUNDATION = PASS
PRESENTATION_FOUNDATION = PASS
```

The evidence covers persisted preference precedence, browser locale resolution,
no automatic persistence, invalid persisted values, storage read/write failure,
current-session switching after write failure, i18next/AntD synchronization,
unchanged QueryClient ownership, Intl date/number formatting, system timezone
truth, semantic read states, and keyboard-reachable retry.

## 5. Browser locale acceptance

The repository-local production-like Compose runtime was started for the current
candidate. PostgreSQL, migration setup, secret-init, Node, node-counter, Control,
and TLS health checks passed. Host execution policy was applied, and the test
used Playwright bundled Chromium rather than system Chrome or unsafe flags.

```text
BROWSER_LOCALE_ACCEPTANCE = PASS
BROWSER_TESTS = 6/6 PASS
```

Covered scenarios:

```text
context zh-CN + stored zh-CN = PASS
stored en > context zh-CN = PASS
invalid persisted locale fallback = PASS
context en + stored en = PASS
context en + no stored locale = PASS
explicit en switch survives reload = PASS
```

Global and existing responsive contracts were preserved:

```text
RESPONSIVE_CONTRACT = PASS
global = 1280x720
Topology overrides = 1280x900, 390x844
Problems override = 390x844
```

All Stage 2 Browser interactions use `getByTestId()`:

```text
NEW_E2E_LOCATOR_POLICY = PASS
S2_7_REQUIRED_REMEDIATION = NONE
```

## 6. Runtime and secret hygiene

```text
BROWSER_RUNTIME_COMPOSE = PASS
SECRET_CLEANUP = PASS
```

After acceptance, the repo-external runtime directory, Compose project, volumes,
storage state, and temporary credentials were removed. No password, cookie,
CSRF token, bootstrap secret, management credential, raw credential file, or
signing key was retained as evidence.

## 7. Final result

```text
S2_8_FOUNDATION_ACCEPTANCE = PASS
TD_INVALIDATION = NONE
STAGE3_SCOPE_ENTERED = NO
P0_BLOCKERS = 0
P1_BLOCKERS = 0

FINAL_STAGE2_IMPLEMENTATION_READINESS = PASS
FINAL_INDEPENDENT_REVIEW = PASS
STAGE2_FINAL_FREEZE = YES
STAGE2_STATUS = CLOSED
STAGE2_EXIT = PASS
STAGE3_IMPLEMENTATION_AUTHORIZED = YES
```

Stage 3 authorization is an exit disposition only. No Stage 3 vertical slice is
claimed by this evidence document.
