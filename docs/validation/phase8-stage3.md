# Phase 8 — Stage 3 Real Surface Vertical Migration

> Status: **STAGE 3 CLOSED — Final independent review passed**
> Entry baseline: `6acfa23ef84226ade839e6fa262b558bb3d6c1c5`
> Final implementation candidate: `11b5e0e6fe5261cf183771f33f6db8171483038c`

## 1. Final disposition

```text
STAGE3_UNIFIED_ACCEPTANCE = PASS
FINAL_INDEPENDENT_REVIEW = PASS
STAGE3_FINAL_FREEZE = YES
STAGE3_STATUS = CLOSED
STAGE3_EXIT = PASS
P0 = 0
P1 = 0
TD_INVALIDATION = NONE
BACKEND_CHANGED = NO
DATABASE_CHANGED = NO
BUSINESS_SEMANTICS_CHANGED = NO
DEFERRED_PHASE9_ENTERED = NO
DEFERRED_PHASE10_ENTERED = NO
```

## 2. Scope

Stage 3 migrated the following real surfaces:

```text
Slice 1A — Auth
Slice 1B — Management shell
Slice 2  — Asset Registry
Slice 3  — Topology Core
Slice 4  — Account Read
Slice 5  — Account Operations
Slice 6  — Jobs
Slice 7  — Problems
```

The implementation remained a presentation migration. Auth, routing,
backend, database, inventory, account-operation, Node execution, receipt,
audit, and credential-security semantics were preserved.

## 3. Ordered implementation and acceptance history

The authoritative retained commit sequence from the Stage 3 entry baseline is:

```text
a705840 feat(web): migrate auth surfaces to stage3 foundation
d62d266 feat(web): migrate management shell to stage3 foundation
efe6f77 feat(web): migrate asset registry surface
fce88c2 feat(web): migrate topology core surface
50169f0 feat(web): migrate account read surfaces
787d81a feat(web): migrate account operation surfaces
f682516 feat(web): migrate jobs surface
1cef979 feat(web): migrate problems surface
2ef400c fix(web): complete stage3 surface localization
3c286e8 fix(web): remove stage3 locale coupling
0b49676 test(web): provide stage3 foundation test harness
f4e88ba test(acceptance): separate general browser suite ownership
6efd13a test(acceptance): pin general browser locale
e9e0d97 test(acceptance): make general browser locale deterministic
588650f test(web): remediate blocking stage3 browser contracts
1c97fd1 test(acceptance): make focused browser locale deterministic
5b667ac test(acceptance): cover same-account override
451b0ad test(acceptance): harden same-account override evidence
531401e test(acceptance): add representative English surface proof
072f605 test(acceptance): align English management proof
52e68d5 test(acceptance): align English jobs proof
b9b3a54 test(acceptance): tighten English key leak proof
2c8b8e3 test(acceptance): narrow English translation leak check
2c06648 fix(web): complete runtime locale reactivity
e8fc54a test(web): remediate gateway management browser contract
11b5e0e fix(web): complete remaining stage3 translations
```

## 4. Corrective categories

The subsequent corrective work was classified as:

```text
presentation correctness
acceptance harness
historical E2E debt
acceptance coverage
```

It included translation completeness, locale-coupling hygiene, foundation test
harness context, Browser suite ownership, deterministic General and focused
locale setup, historical Node/Topology/Gateway locator remediation,
Same-account Override coverage, representative English proof, runtime locale
switching, localized Topology error states, Node Retire copy, and final
Gateway/MFA translations. These were not product regressions.

## 5. Acceptance evidence

```text
TYPECHECK = PASS
UNIT_COMPONENT = 37 files / 286 tests PASS
PRODUCTION_BUILD = PASS
GENERATED_DRIFT = NONE
RESOURCE_PARITY = PASS
REFERENCED_KEY_COMPLETENESS = PASS
TRANSLATION_SOURCE_AUDIT = PASS
```

General Browser zh-CN:

```text
Authentication = PASS
Gateway management = PASS
Node lifecycle = PASS
Topology = PASS
Problems = PASS
```

Focused Account Operations:

```text
Upload New = PASS
Disable = PASS
Enable fixture = PASS
Enable = PASS
Replace Existing = PASS
Remove = PASS
Lifecycle Override = PASS
Same-account Override = PASS
Security Replay = PASS
```

Representative English passed for Auth, Management, a data surface, an
operation surface, Jobs, error-state presentation, Gateway form presentation,
and Management MFA presentation. Mounted locale switching passed for Assets,
Jobs, and Problems.

Responsive evidence remained:

```text
Global = 1280x720 PASS
Topology = 1280x900 PASS; 390x844 PASS
Problems = 390x844 PASS
```

## 6. Security and operation invariants

```text
CREDENTIAL_LEAK = ZERO
AUDIT_RECEIPT = PASS
OUTCOME_UNKNOWN_SEMANTICS = PASS
INVENTORY_USED_AS_EXECUTION_TRUTH = NO
RUNTIME_PROVENANCE = PASS
```

Upload New remains manual identity, single file, single account, and single
operation. Credential-derived identity, multi-file upload, and batch operation
behavior were not introduced.

## 7. Architecture and E2E policy

Ant Design 6, i18next/react-i18next, typed bundled resources, AppLocale
resolution, Ant Design locale synchronization, and Intl formatting remain the
frozen architecture. No React Router, Tailwind, shadcn, second design system,
or generic wrapper framework was introduced. Topology targeted private Ant
Design selector debt was removed.

Stage 3 touched/current-policy E2E scope included:

```text
gateway-management.spec.ts
node-lifecycle.spec.ts
topology.spec.ts
account-operations-override.spec.ts
stage3-representative-en.spec.ts
```

```text
NONCOMPLIANT_INTERACTION_LOCATORS = NONE
```

This does not claim repository-wide historical locator debt is absent.
Untouched Authentication and Problems debt retains its accepted prior
disposition.

## 8. Deferred scope

Phase 9 remains deferred: configuration-surface inventory/reduction,
gateway-proxy decommission, deployment/bootstrap automation, desired-state
reconciliation/readiness, Node provenance freeze boundary, and production
deployment/upgrade/recovery work.

Phase 10 remains deferred: credential-derived Upload identity, removal of
manual email, multi-file upload, per-file operations, and partial-success
batch UX.
