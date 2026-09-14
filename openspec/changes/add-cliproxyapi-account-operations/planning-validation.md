# Phase 7 Change B Planning Validation — Native-First CLIProxyAPI Account Operations

## Current status

- Change: `add-cliproxyapi-account-operations`.
- Architecture direction: Native-First; Architecture Review PASS; Gate 1 CLOSED / PASS.
- CLIProxyAPI baseline: upstream release v7.3.2, exact commit `7fa443dc8bf8ca2f1ffd81c2472deb31b097b697`.
- Stage 7A dependency: satisfied; migration 37 and compatibility class/floor 3/3.
- Relay-specific Node mutation protocol: zero current dependency.
- Native-First Simplification: no Phase 7 verification state/workflow, scheduler, reconciler, durable job, lease or worker; normal Inventory remains independent business observation.
- Phase 7 v1 execution states: `prepared|dispatched|remote_applied|remote_noop|outcome_unknown|failed`; `remote_partial` is not part of the current state set.
- Same-account serialization: PostgreSQL durable truth only; lifecycle override retains Stage 7A command identity/replay without creating a separate workflow.
- Final independent architecture re-review: P0=0, P1=0, P2=3 non-blocking documentation/finalization findings / PASS; three finalization findings resolved and persisted as P0=0, P1=0, P2=0 / PASS.
- Planning reconciliation: Gate 1 finalization complete.
- Architecture status: `PASS`; Gate 1 is `CLOSED / PASS`; Gate 2 is `READY FOR INDEPENDENT REQUIREMENTS / OPENSPEC READINESS REVIEW`.
- ADR status: `ACCEPTED`.
- Detailed Requirements: `FREEZE CANDIDATE`.
- OpenSpec Change B: `READY CANDIDATE`.
- Planning / specification readiness: `READY FOR INDEPENDENT REQUIREMENTS / OPENSPEC READINESS REVIEW`.
- Runtime artifact identity: `NOT YET FROZEN`.
- Node revert: `NOT RUN`.
- Implementation: `NOT STARTED`.

This document does not claim Detailed Requirements FROZEN, final Implementation Readiness PASS or implementation authorization. Gate 1 architecture acceptance does not authorize Stage 7B implementation.

## Historical dependency record

The Node Account Management Contract v1, its implementation at Node revision `72c435b1b1b85b341a734e3860081c7782d9cbd2`, image `sha256:c5d2cc476c5c99cff994528920151c3ecee0f37832ba82943b8b54ab7d9610c4`, contract reviews and corrective amendments remain valid historical evidence of an implemented and reviewed design. Native-First Corrective Round 3 continues to mark that design a **HISTORICAL / SUPERSEDED CANDIDATE**. It is not the current Change B implementation dependency and not the current deployment baseline. No Node revert is part of this planning change.

## Reviewed native source evidence

Pinned v7.3.2 source inspection confirms the current planning subset:

- `GET /v0/management/auth-files` returns a broad native projection that Control must treat as untrusted and immediately reduce.
- `PATCH /v0/management/auth-files/status` accepts exact `name`, `auth_index` and `disabled` JSON fields.
- `DELETE /v0/management/auth-files` exposes single, multi and all-delete forms; Control permits only one URL-encoded exact basename in the `name` query.
- `POST /v0/management/auth-files?name=...` accepts a raw JSON auth object and may overwrite; Control does not use native multipart upload.
- Native fields/operations for fields, download, refresh and OAuth are outside the Change B adapter allowlist.
- Native responses do not provide a machine-stable physical-commit marker, compare-and-swap revision or remote execution proof.
- Manager-backed entries carry source/runtime-only/auth-index evidence; disk-fallback or malformed/degraded non-empty entries fail closed, while a clean version-valid structurally valid empty response permits Upload New absence evidence only.
- Management middleware supplies `X-CPA-VERSION` and `X-CPA-COMMIT`; exact reviewed runtime artifact identity remains a mandatory pre-implementation pin.
- `CanonicalCredentialMetadataKey()` maps the reviewed legacy aliases used by the complete runtime-control denylist.

## Native-First corrective resolutions incorporated

Round 11 preserves request-driven resume for an accepted `prepared` operation and a separate one-time same-account risk override for `dispatched`/`outcome_unknown`; both override commands now require one-transaction reservation and terminal-result atomicity, without introducing a worker, scheduler, reconciler, lease or automatic redispatch. Normal Inventory remains independent; the source-reviewed v7.3.2 upload POST 503-before-body-read/write mapping remains terminal `failed/node_management_unavailable` with receipt and zero mutation; other unreviewed 503/5xx responses remain `outcome_unknown`.

It also freezes the source-reviewed native upload exception: `POST /v0/management/auth-files` HTTP 503 from `authManager == nil` before body read/write is terminal `failed/node_management_unavailable` with a receipt and zero mutation; other unreviewed 503/5xx responses remain `outcome_unknown`.

1. **Native API boundary:** exact four-route allowlist; no arbitrary management passthrough, all/multi delete, fields/download/refresh/OAuth or browser-visible Management Key.
2. **Safe snapshot:** provider/type, normalized email, validated basename, bounded auth_index and disabled only; raw response/path/token-adjacent/runtime/unknown fields are discarded.
3. **Target resolution:** every target-existing mutation uses a fresh exactly-one provider+normalized-email match; Inventory and filename convention cannot select the target.
4. **Write semantics:** Upload New is best-effort create and Replace Existing is best-effort replace under native last-writer-wins. Concurrent create/refresh lost-update risk is explicit and accepted; no Relay CAS is claimed.
5. **Credential ingress:** CLIProxyAPI owns schema truth. Control enforces top-level object/type/email, 1 MiB ingress, filename bounds and a reviewed runtime-control denylist without silently stripping fields.
6. **Outcome mapping:** known 2xx is terminal; stable reviewed pre-mutation status/context may fail; timeout, connection/response loss and ambiguous native 5xx become `outcome_unknown`. There is no automatic mutation redispatch or inferred execution stage.
7. **Durable serialization/lifecycle:** PostgreSQL serializes same-account operations and retains dispatched/unresolved blockers across restart; Node lifecycle follows the same Node-first lock order. Lifecycle override releases only the lifecycle block; the separate same-account override releases only the same-account blocker, with both using one-time fields, global command identity, exact replay and one-transaction reservation/terminalization atomicity. Existing ineligible override targets use `account_operation_not_overridable`; missing targets use error-only `operation_not_found` receipts.
8. **Observation/replay:** Inventory is independent business observation only. Stable terminal POST truth uses a separate immutable account receipt; a `prepared` same-command POST resumes the same operation, while `outcome_unknown` has no receipt and replay returns current projection with zero redispatch.
9. **Physical target eligibility:** source/runtime/auth-index are classified transiently before projection; memory/runtime-only/incomplete/disk-fallback evidence is ineligible for existing-target mutation, while a clean version-valid structurally valid empty snapshot permits Upload New absence evidence and returns `account_target_not_found` for existing-target operations.
10. **Runtime identity:** exact-once bounded version/commit headers must match the independently pinned runtime artifact before snapshot interpretation; upstream source baseline and runtime artifact commit are distinct concepts.
11. **Noop and override:** Disable/Enable noop is decided before PATCH; lifecycle and same-account overrides each have their own global command/receipt, remain orthogonal, and only the same-account override may waive the same-account blocker.
12. **Exact command equality:** all seven command kinds, including `account.lifecycle_override` and `account.same_account_override`, have fixed canonical JSON arrays; upload uses the separate 32-byte key/HMAC contract with deterministic wrong-key replay.
13. **Create collision:** Upload New requires both identity absence and generated basename absence, while preserving the accepted concurrent last-writer-wins race.
14. **Schema cleanup:** Phase 7 v1 uses only the six frozen execution states and the administrator FK is `control_admin_users(admin_id)` with restrictive updates/deletes. Stable override errors include `account_operation_not_overridable`, `lifecycle_override_already_set` and `same_account_override_already_set`.

## Readiness acceptance still required

Independent architecture re-review must verify:

- the exact native subset and safe projection match pinned v7.3.2 source;
- the final runtime artifact commit to enforce is independently frozen before final Implementation Readiness; Gate 2 is planning/specification readiness and does not require that final artifact pin;
- all previous custom Node protocol dependencies are historical or removed from current normative text;
- durable same-account serialization and lifecycle blocker matrices are complete;
- best-effort create/replace races and conservative unknown outcomes are accepted explicitly;
- public API, terminal receipt, lifecycle override, independent Inventory observation, audit and Secret boundaries are internally consistent;
- no current planning statement promotes Phase 7 or authorizes Stage 7B implementation.

## Validation record

The following values are updated from actual commands before local commit:

```text
openspec validate add-cliproxyapi-account-operations --strict = PASS
openspec validate --all --strict = 30 passed / 0 failed
git diff --check = PASS
scope = PASS — only openspec/changes/add-cliproxyapi-account-operations/**
push = NOT RUN
```

Disposition: `NATIVE-FIRST ARCHITECTURE REVIEW PASS / GATE 1 CLOSED / GATE 2 READY FOR INDEPENDENT REQUIREMENTS-OPENSPEC REVIEW / DETAILED REQUIREMENTS FREEZE CANDIDATE / OPENSPEC READY CANDIDATE / IMPLEMENTATION NOT STARTED`.

## Gate 2 acceptance matrix

The following matrix is the deterministic planning acceptance set for independent Gate 2 review. It is a requirements and testability contract only; it does not authorize implementation.

- Global command boundary: Stage 7A cross-domain conflict/replay; all seven canonical command intents; upload HMAC golden vectors; wrong-key replay.
- Native compatibility and boundary: fresh runtime header exact match, missing/duplicate/mismatch handling; the four-route native allowlist; safe snapshot projection; file-backed mutation eligibility; memory/runtime-only exclusion; disk-fallback and malformed/degraded evidence; clean empty Upload New.
- Admission and targeting: identity and basename collisions; UTF-8 238-byte create-email acceptance and 239-byte rejection; exactly-one existing-target resolution; safe basename validation.
- Outcomes and recovery: Disable/Enable pre-dispatch noop; sent stable 2xx; reviewed Upload POST 503; ambiguous 5xx; timeout, connection loss and response loss; prepared crash and exact resume; upload credential re-supply and wrong-credential conflict; concurrent prepared retry; dispatched/outcome_unknown restart; zero automatic redispatch.
- Serialization and overrides: same-account A/B race; PostgreSQL invariant; Retire-first/dispatch-first; lifecycle override and same-account override missing-target, invalid-state, already-set, success, exact replay and different-command race; cross-type override race; each override waives only its own blocker; old unknown request may complete after same-account override.
- Observation, secrecy and receipts: normal Inventory independent observation; no Phase 7 verification workflow/state/scheduler/reconciler; Secret scans; no raw native response, credential or Management Key exposure; error-only and error-plus-operation response classes; immutable receipt exact replay; override transaction rollback before commit and commit/response-loss replay.

## Gate 2 candidate status

```text
Architecture Review = PASS
ADR = ACCEPTED
Gate 1 = CLOSED / PASS
Detailed Requirements = FREEZE CANDIDATE
OpenSpec Change B = READY CANDIDATE
Planning / specification readiness = READY FOR INDEPENDENT REQUIREMENTS / OPENSPEC READINESS REVIEW
Runtime artifact identity = NOT YET FROZEN
Node alignment = NOT STARTED
Node revert = NOT RUN
Final Implementation Readiness = NOT READY
Stage 7B implementation = NOT STARTED
```
