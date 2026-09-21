# Phase 10 Stage 1 Validation Evidence

## Final status

```text
PHASE10_STAGE1 = CLOSED / PASS
STAGE2_READINESS = READY FOR AUTHORIZATION
MOBILE_ACCEPTANCE = SUPERSEDED
DESKTOP_ACCEPTANCE = PASS
```

Stage 1 remains limited to the Control Web foundation. No Control business
source, API, database, migration, Gateway, or Relay Node change was introduced.

## Node acceptance provenance

The historical local Node artifact was unavailable. Stage 1 acceptance therefore
used a newly rebuilt, explicitly identified artifact from the frozen source:

```text
source commit: 0b34a22fcaec392d39f710f3a8418595b491607d
version: 7.3.2
commit header: 0b34a22fcaec392d39f710f3a8418595b491607d
image: relay-station-node:phase10-stage1-rebuild-0b34a22f
image ID: sha256:0d927726081869825f4ac444a3f83687e560b01c207de7e22032fb33efccac2d
platform: linux/arm64
build time: 2026-09-21T10:21:13Z
NODE_RUNTIME = PASS
```

This is new Phase 10 Stage 1 acceptance provenance. It does not restore,
replace, or rewrite the historical Phase 8 Node artifact evidence.

## Browser regression evidence

The existing `deploy/acceptance/control-auth-e2e.sh` isolated production-like
runtime was used with the rebuilt Node artifact. Acceptance secrets, TLS
material, PostgreSQL, and runtime containers were ephemeral and external to the
repository.

```text
Authentication regression = PASS
Problems regression = PASS
Topology regression = PASS
Representative locale regression = PASS (3/3)
Foundation Browser proof = PASS (3/3)
```

The representative locale cases ran with the existing acceptance HTTPS/runtime
topology and verified English auth, management/data/Jobs surfaces, and live
locale transition. The Foundation cases covered `1280×720` zh-CN, `1280×720`
en, and `1440×900` zh-CN.

Problems and Topology retained their desktop business, read-only, security,
pagination, keyboard, failure-isolation, identity-protection, and no-mutation
assertions. Only the superseded 390px mobile presentation/overflow gates were
removed from the active tests.

## Static validation

```text
Unit/component tests = PASS (40 files, 312 tests)
Typecheck = PASS
Build = PASS
Translation audit = PASS
Generated drift = NONE
git diff --check = PASS
Backend/API change = NO
Database/Migration change = NO
Gateway change = NO
Relay Node source change = NO
```

The active `web/e2e` suite contains no remaining mandatory 390px/mobile
overflow gate. Mobile wording retained in the OpenSpec files is historical
reconciliation evidence describing the superseded contract.
