## Why

Phase 7 frozen requirements/Architecture Review at `sunxu/relay-station-ops@594a349435dbb6c2d4265be79fb913015b1b05c5` approve explicit CLIProxyAPI account Disable/Enable/Remove and repaired Antigravity auth-file Create/Replace. Native CLIProxyAPI management endpoints exist, but they operate physical auth records and lack Control global command identity, durable response-loss recovery, lifecycle/quiescence fencing and normal-Inventory verification semantics.

This change plans the Control account-operation product surface after the global command registry prerequisite, while preserving CLIProxyAPI credential/runtime truth and data-plane isolation.

## What Changes

- Add a minimal `account_admin_operations` durable execution/recovery projection with orthogonal `execution_state` and `verification_state`; it is not a generic workflow engine.
- Provide explicit single-account Disable, Enable, Remove, Upload New and Replace Existing administrator operations for Antigravity.
- Resolve `(node_instance_id, account_key)` to exactly one current physical target at operation time and require a generic opaque Node-side precondition.
- Depend on external `Node Account Management Contract v1`: persistence errors, serialized mutation, strict 256 KiB single-file Antigravity allowlist, explicit create/replace, atomic replacement, secret-safe postcondition proof, bounded synchronous mutation, Control `dispatch_token` reuse as Node `dispatch_token_v1`, durable same-token fenced recovery resolve, and stable sanitized errors.
- Use the Change A global command registry/actor-first namespace; use a separate Phase 7 upload-intent fingerprint key rather than expanding Phase 6 asset K1 semantics.
- Add bounded dispatch/quiescence fences that block Node Retire/Replace until remote mutation can no longer begin/continue; require active monitoring/capability/provider-policy eligibility before dispatch.
- Wake/request only the existing fixed-slot Inventory scheduler, then verify business convergence from accepted normal Inventory evidence. No off-grid/special Phase 7 poll is created.
- Add protected API/UI, audit/metrics, recovery, compatibility and runtime acceptance planning.

## Capabilities

### New Capabilities

- `account-admin-operation`: Phase 7 command/API state machine, target resolution, remote mutation/recovery, Secret handling, verification, audit/metrics and UI behavior.

### Modified Capabilities

- `asset-admin-command`: Phase 7 uses the global registry from Change A, keeps existing asset K1 unchanged and materializes terminal replay evidence only under the reviewed account-operation contract.
- `relay-node-asset-lifecycle`: Retire/Replace must reject while a dispatched account operation lacks proven remote quiescence.
- `account-inventory-poll-run`: account operations may only wake/request the existing UTC fixed-slot scheduler; dispatch also requires current monitoring/capability/policy eligibility.
- `account-inventory-snapshot`: Inventory verifies only business convergence and cannot by itself prove uploaded credential bytes.

## Dependencies

1. Ops frozen baseline `594a349435dbb6c2d4265be79fb913015b1b05c5`.
2. `add-global-admin-command-registry` planning/implementation and its compatibility barrier.
3. External pinned `Node Account Management Contract v1` hardened artifact. Planning discovery baseline: local fork `273d624c70f6eb8bdd7b049df396c306acd3f8d0`, observed upstream `ac02da6c05e18f465aa7e3ed5b0a65a2f060917d`; implementation must re-check freshness and pin final fork/image digest.
4. Existing Phase 6 Node lifecycle/monitoring, account Inventory and HTTP-only management contracts.

## Impact

Implementation is expected to add additive Control schema/state, OpenAPI routes/generated clients, a bounded Node driver adapter, admin UI, audit/metrics and acceptance harnesses. It does not modify Gateway. Node product hardening is an external prerequisite and is not implemented by this Control planning change.

## Non-Goals

No OAuth/Re-auth, automatic repair/move/remove, Credential Vault, generic Workflow Engine, Provider Repair Framework, batch/all operation, Gateway Account/Group mutation, new Inventory subsystem, special off-grid polling, CLIProxyAPI scheduler replacement, internal HTTPS/TLS or data-plane participation.

## Planning status

Planning = COMPLETE candidate. Dependency readiness = WAITING ON `add-global-admin-command-registry` implementation/readiness plus pinned Node Account Management Contract v1. Independent readiness review = REQUIRED. Implementation = NOT STARTED.
