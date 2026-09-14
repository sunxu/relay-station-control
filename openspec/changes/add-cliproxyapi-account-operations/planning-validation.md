# Phase 7 Change B Planning Validation — CLIProxyAPI Account Operations

## Status

- Ops requirements baseline: `594a349435dbb6c2d4265be79fb913015b1b05c5` — Detailed Requirements FROZEN / Architecture Review PASS / P0-P1-P2 0-0-0.
- Stage 7A dependency: `SATISFIED` — archived migration `37`, compatibility class/floor `3 / 3`.
- Stage 7N dependency: `SATISFIED` — accepted Node revision `72c435b1b1b85b341a734e3860081c7782d9cbd2`, image `sha256:c5d2cc476c5c99cff994528920151c3ecee0f37832ba82943b8b54ab7d9610c4`.
- Planning: `COMPLETE`.
- Dependency readiness: `SATISFIED candidate`.
- Independent readiness review: `RE-REVIEW REQUIRED`.
- Implementation readiness: `READY FOR INDEPENDENT RE-REVIEW`.
- Implementation: `NOT STARTED`.

## Frozen planning decisions carried forward

- canonical target `(node_instance_id,account_key)` + fresh exactly-one resolution;
- opaque physical-target precondition;
- strict Antigravity allowlist, single file, 256 KiB;
- explicit Upload New / Replace Existing;
- separate Phase 7 keyed-fingerprint key, asset K1 unchanged;
- minimal mutable `account_admin_operations` + global command registry + separate immutable `account_admin_command_receipts` exact terminal replay;
- execution/verification orthogonal states and direct `prepared -> dispatched` transition;
- bounded synchronous Node mutation plus durable same-token fenced resolve; client timeout/deadline alone is not proof;
- fresh authenticated Node v1 contract discovery plus active/current-monitoring/both-capabilities/provider-policy required at dispatch with Node-first lock order;
- existing fixed-slot Inventory scheduler wake/request only;
- Create/Replace require Node postcondition proof plus Inventory business convergence;
- selective Node upstream port, pinned artifact, HTTP-only management.

## External prerequisite — Node Account Management Contract v1

The dependency is satisfied by independently accepted Node revision `72c435b1b1b85b341a734e3860081c7782d9cbd2` and image `sha256:c5d2cc476c5c99cff994528920151c3ecee0f37832ba82943b8b54ab7d9610c4`. Before every mutation Control still MUST perform fresh authenticated discovery and verify the exact v1 contract, provider, both capabilities and frozen constants; the pinned artifact is evidence, not runtime authorization.

## Planned acceptance highlights

1. Global command collisions/actor-first behavior inherited from Change A.
2. Target missing/duplicate/precondition-change fail closed.
3. Monitoring disabled/future-only and capability/policy ineligible -> zero dispatch.
4. Control timeout while Node handler still executes -> lifecycle remains blocked; crash/restart restores fence.
5. Retire/Replace succeeds only after terminal `quiescent=true`, successful durable same-token fenced resolve, or proven exact-process termination/restart; deadline expiry alone never releases the fence and no mutation may later land on old Node.
6. Secret bytes absent from Control DB/receipt/audit/log/trace/metric/temp disk/response.
7. Upload allowlist/size/single-file/create-vs-replace and Phase 7 key fail-closed.
8. Response-loss postcondition recovery; Inventory identity alone never proves replacement.
9. Fixed-slot Inventory wake semantics and conservative verification.
10. Signed compatibility/rollback and pinned Node artifact mismatch fail closed.

## Validation execution note

The artifact set follows the repository's spec-driven planning shape. Corrective planning validation uses the repository OpenSpec CLI and records its actual strict result before commit. No product code, migration, OpenAPI generated output, Node/Gateway code or implementation apply workflow is changed by this planning correction.

```text
Corrective Amendment 1 planning reconciliation:
openspec validate add-cliproxyapi-account-operations --strict = PASS
openspec validate --all --strict = 30 passed / 0 failed
git diff --check = PASS
Stage 7B implementation = NOT STARTED
```

## Readiness findings

- B-P1-1 terminal receipt/replay contract: `RESOLUTION INCORPORATED`.
- B-P1-2 Node mutation capability/runtime discovery gate: `RESOLUTION INCORPORATED`.
- B-P1-3 exact Control product API contract: `RESOLUTION INCORPORATED`.
- Planning P0 blockers: 0 candidate.
- Planning P1 blockers: 0 candidate.
- Stage 7A and Stage 7N dependencies: `SATISFIED candidate`.

Disposition: `PLANNING COMPLETE / READY FOR INDEPENDENT RE-REVIEW / IMPLEMENTATION NOT STARTED`.
