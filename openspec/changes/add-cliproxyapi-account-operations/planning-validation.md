# Phase 7 Change B Planning Validation — Native-First CLIProxyAPI Account Operations

## Current status

- Change: `add-cliproxyapi-account-operations`.
- Architecture direction: Native-First Corrective Round 1.
- CLIProxyAPI baseline: upstream release v7.3.2, exact commit `7fa443dc8bf8ca2f1ffd81c2472deb31b097b697`.
- Stage 7A dependency: satisfied; migration 37 and compatibility class/floor 3/3.
- Relay-specific Node mutation protocol: zero current dependency.
- Planning reconciliation: complete.
- Candidate findings: P0=0, P1=0 candidate, P2=0 candidate.
- Architecture status: `READY FOR INDEPENDENT ARCHITECTURE RE-REVIEW`.
- Implementation: `NOT STARTED`.

This document does not claim Architecture Review PASS, Detailed Requirements FROZEN, implementation readiness READY or implementation authorization.

## Historical dependency record

The Node Account Management Contract v1, its implementation at Node revision `72c435b1b1b85b341a734e3860081c7782d9cbd2`, image `sha256:c5d2cc476c5c99cff994528920151c3ecee0f37832ba82943b8b54ab7d9610c4`, contract reviews and corrective amendments remain valid historical evidence of an implemented and reviewed design. Native-First Corrective Round 1 marks that design a **HISTORICAL / SUPERSEDED CANDIDATE**. It is not the current Change B implementation dependency and not the current deployment baseline. No Node revert is part of this planning change.

## Reviewed native source evidence

Pinned v7.3.2 source inspection confirms the current planning subset:

- `GET /v0/management/auth-files` returns a broad native projection that Control must treat as untrusted and immediately reduce.
- `PATCH /v0/management/auth-files/status` accepts exact `name`, `auth_index` and `disabled` JSON fields.
- `DELETE /v0/management/auth-files` exposes single, multi and all-delete forms; Control permits only one URL-encoded exact basename in the `name` query.
- `POST /v0/management/auth-files?name=...` accepts a raw JSON auth object and may overwrite; Control does not use native multipart upload.
- Native fields/operations for fields, download, refresh and OAuth are outside the Change B adapter allowlist.
- Native responses do not provide a machine-stable physical-commit marker, compare-and-swap revision or remote execution proof.

## Native-First corrective resolutions incorporated

1. **Native API boundary:** exact four-route allowlist; no arbitrary management passthrough, all/multi delete, fields/download/refresh/OAuth or browser-visible Management Key.
2. **Safe snapshot:** provider/type, normalized email, validated basename, bounded auth_index and disabled only; raw response/path/token-adjacent/runtime/unknown fields are discarded.
3. **Target resolution:** every target-existing mutation uses a fresh exactly-one provider+normalized-email match; Inventory and filename convention cannot select the target.
4. **Write semantics:** Upload New is best-effort create and Replace Existing is best-effort replace under native last-writer-wins. Concurrent create/refresh lost-update risk is explicit and accepted; no Relay CAS is claimed.
5. **Credential ingress:** CLIProxyAPI owns schema truth. Control enforces top-level object/type/email, 1 MiB ingress, filename bounds and a reviewed runtime-control denylist without silently stripping fields.
6. **Outcome mapping:** known 2xx is terminal; reviewed provably pre-mutation 4xx may fail; timeout, connection/response loss and ambiguous native 5xx become `outcome_unknown`. There is no automatic mutation redispatch or inferred partial stage.
7. **Durable serialization/lifecycle:** PostgreSQL serializes same-account operations and retains dispatched/unresolved blockers across restart; Node lifecycle follows the same Node-first lock order. High-risk override releases only the lifecycle block.
8. **Verification/replay:** Inventory proves only business convergence. Stable terminal POST truth uses a separate immutable account receipt; `outcome_unknown` has no receipt and same-command replay returns current projection with zero redispatch.

## Readiness acceptance still required

Independent architecture re-review must verify:

- the exact native subset and safe projection match pinned v7.3.2 source;
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

Disposition: `NATIVE-FIRST CORRECTIVE ROUND 1 / READY FOR INDEPENDENT ARCHITECTURE RE-REVIEW / IMPLEMENTATION NOT STARTED`.
