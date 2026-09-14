# Phase 7 Change B Planning Validation — Native-First CLIProxyAPI Account Operations

## Current status

- Change: `add-cliproxyapi-account-operations`.
- Architecture direction: Native-First Corrective Round 5.
- CLIProxyAPI baseline: upstream release v7.3.2, exact commit `7fa443dc8bf8ca2f1ffd81c2472deb31b097b697`.
- Stage 7A dependency: satisfied; migration 37 and compatibility class/floor 3/3.
- Relay-specific Node mutation protocol: zero current dependency.
- Previous independent re-review: P0=0, P1=1, P2=1 / CHANGES REQUIRED.
- All Round 5 P1/P2 resolutions: incorporated.
- Planning reconciliation: complete revision candidate.
- Candidate findings: P0=0, P1=0 candidate, P2=0 candidate.
- Architecture status: `READY FOR INDEPENDENT ARCHITECTURE RE-REVIEW`.
- ADR status: `PROPOSED`.
- Runtime artifact identity: `NOT YET FROZEN`.
- Node revert: `NOT RUN`.
- Implementation: `NOT STARTED`.

This document does not claim Architecture Review PASS, Detailed Requirements FROZEN, implementation readiness READY or implementation authorization.

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
- Manager-backed entries carry source/runtime-only/auth-index evidence; disk-fallback entries omit it, and an empty response cannot prove manager-backed mode.
- Management middleware supplies `X-CPA-VERSION` and `X-CPA-COMMIT`; exact reviewed runtime artifact identity remains a mandatory pre-implementation pin.
- `CanonicalCredentialMetadataKey()` maps the reviewed legacy aliases used by the complete runtime-control denylist.

## Native-First corrective resolutions incorporated

Round 5 additionally reconciles the effective Ops architecture body and separates runtime HTTP header identity from image-digest deployment evidence; the source-reviewed v7.3.2 upload POST 503-before-body-read/write mapping remains terminal `failed/node_management_unavailable` with receipt and zero mutation; other unreviewed 503/5xx responses remain `outcome_unknown`.

It also freezes the source-reviewed native upload exception: `POST /v0/management/auth-files` HTTP 503 from `authManager == nil` before body read/write is terminal `failed/node_management_unavailable` with a receipt and zero mutation; other unreviewed 503/5xx responses remain `outcome_unknown`.

1. **Native API boundary:** exact four-route allowlist; no arbitrary management passthrough, all/multi delete, fields/download/refresh/OAuth or browser-visible Management Key.
2. **Safe snapshot:** provider/type, normalized email, validated basename, bounded auth_index and disabled only; raw response/path/token-adjacent/runtime/unknown fields are discarded.
3. **Target resolution:** every target-existing mutation uses a fresh exactly-one provider+normalized-email match; Inventory and filename convention cannot select the target.
4. **Write semantics:** Upload New is best-effort create and Replace Existing is best-effort replace under native last-writer-wins. Concurrent create/refresh lost-update risk is explicit and accepted; no Relay CAS is claimed.
5. **Credential ingress:** CLIProxyAPI owns schema truth. Control enforces top-level object/type/email, 1 MiB ingress, filename bounds and a reviewed runtime-control denylist without silently stripping fields.
6. **Outcome mapping:** known 2xx is terminal; reviewed provably pre-mutation 4xx may fail; timeout, connection/response loss and ambiguous native 5xx become `outcome_unknown`. There is no automatic mutation redispatch or inferred execution stage.
7. **Durable serialization/lifecycle:** PostgreSQL serializes same-account operations and retains dispatched/unresolved blockers across restart; Node lifecycle follows the same Node-first lock order. High-risk override releases only the lifecycle block.
8. **Verification/replay:** Inventory proves only business convergence. Stable terminal POST truth uses a separate immutable account receipt; `outcome_unknown` has no receipt and same-command replay returns current projection with zero redispatch.
9. **Physical target eligibility:** source/runtime/auth-index are classified transiently before projection; memory/runtime-only/incomplete/disk-fallback and manager-unproven empty snapshots fail closed.
10. **Runtime identity:** exact-once bounded version/commit headers must match the independently pinned runtime artifact before snapshot interpretation; upstream source baseline and runtime artifact commit are distinct concepts.
11. **Noop and override:** Disable/Enable noop is decided before PATCH; lifecycle override has its own global command/receipt and never unblocks another account mutation.
12. **Exact command equality:** all six command kinds have fixed canonical JSON arrays; upload uses the separate 32-byte key/HMAC contract with deterministic wrong-key replay.
13. **Create collision:** Upload New requires both identity absence and generated basename absence, while preserving the accepted concurrent last-writer-wins race.
14. **Schema cleanup:** Phase 7 v1 uses only the six frozen execution states and the administrator FK is `control_admin_users(admin_id)` with restrictive updates/deletes.

## Readiness acceptance still required

Independent architecture re-review must verify:

- the exact native subset and safe projection match pinned v7.3.2 source;
- the final runtime artifact commit to enforce is independently frozen; until then implementation readiness remains not ready;
- all previous custom Node protocol dependencies are historical or removed from current normative text;
- durable same-account serialization and lifecycle blocker matrices are complete;
- best-effort create/replace races and conservative unknown outcomes are accepted explicitly;
- public API, terminal receipt, lifecycle override, Inventory verification, audit and Secret boundaries are internally consistent;
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

Disposition: `NATIVE-FIRST CORRECTIVE ROUND 3 / P0=0 P1=0-candidate P2=0-candidate / READY FOR INDEPENDENT ARCHITECTURE RE-REVIEW / IMPLEMENTATION NOT STARTED`.
