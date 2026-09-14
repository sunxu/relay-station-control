# Phase 7 Change B Planning Validation — CLIProxyAPI Account Operations

## Status

- Ops requirements baseline: `594a349435dbb6c2d4265be79fb913015b1b05c5` — Detailed Requirements FROZEN / Architecture Review PASS / P0-P1-P2 0-0-0.
- Control planning parent includes `add-global-admin-command-registry` planning.
- Planning: `COMPLETE` candidate.
- Dependency readiness: `WAITING ON registry + pinned Node Account Management Contract v1`.
- Independent readiness review: `REQUIRED`.
- Implementation readiness: `NOT READY`.
- Implementation: `NOT STARTED`.

## Frozen planning decisions carried forward

- canonical target `(node_instance_id,account_key)` + fresh exactly-one resolution;
- opaque physical-target precondition;
- strict Antigravity allowlist, single file, 256 KiB;
- explicit Upload New / Replace Existing;
- separate Phase 7 keyed-fingerprint key, asset K1 unchanged;
- minimal mutable `account_admin_operations` + global command registry + immutable terminal replay separation;
- execution/verification orthogonal states and direct `prepared -> dispatched` transition;
- bounded synchronous Node mutation plus durable same-token fenced resolve; client timeout/deadline alone is not proof;
- active/current-monitoring/capability/provider-policy required at dispatch with Node-first lock order;
- existing fixed-slot Inventory scheduler wake/request only;
- Create/Replace require Node postcondition proof plus Inventory business convergence;
- selective Node upstream port, pinned artifact, HTTP-only management.

## External prerequisite — Node Account Management Contract v1

Must be independently implemented/reviewed in `relay-station-node-cliproxyapi` before Change B apply can be ready. Required contract: persistence error propagation; management mutation serialization; generic precondition; <=256 KiB single-file upload; strict Antigravity allowlist; explicit create/replace; atomic same-filesystem replacement; secret-safe read-back postcondition; <=15s bounded synchronous mutation/no background continuation; every mutation carrying Control's durable `dispatch_token` as lowercase `dispatch_token_v1`; response-loss recovery using the same UUID as `fence_dispatch_token_v1` so Node durably fences late admission before shared-gate read-back; stable sanitized errors. Implementation must re-check upstream freshness, avoid wholesale rebase solely for Phase 7, and pin final fork/image digest.

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

- Planning P0 blockers: 0 candidate.
- Planning P1 blockers in the document set: 0 candidate.
- Hard dependency blocker: pinned Node Contract v1 Corrective Amendment 1 implementation and independent re-review.

Disposition: `PLANNING COMPLETE / CHANGES REQUIRED / DEPENDENCY WAITING / IMPLEMENTATION NOT STARTED`.
