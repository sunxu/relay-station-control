## Why

Phase 7 frozen baseline `594a349435dbb6c2d4265be79fb913015b1b05c5` requires one global `command_id` namespace shared by existing Gateway/Node/Monitoring administrator commands and future CLIProxyAPI account operations. Today `asset_admin_command_receipts.command_id` is unique only inside the receipt table; a second Phase 7 operation table with its own unique key would permit the same UUID to exist in two command domains and would break actor-first conflict priority.

This prerequisite introduces a true durable global reservation without changing data-plane behavior or starting Phase 7 account mutation.

## What Changes

- Add a Control-owned immutable `admin_command_registry` as the single global administrator command identity/reservation namespace.
- Backfill every existing `asset_admin_command_receipts` row into the registry before enforcement; preserve original actor, command kind, canonical intent encoding/hash and Secret fingerprint key-version metadata.
- Require all existing Gateway asset, Relay Node asset and Node Monitoring command writers to use the same UUID-derived transaction advisory serialization and actor-first registry lookup before domain/Secret/current-state validation.
- Keep `asset_admin_command_receipts` as immutable completed-command exact replay evidence; registry reservation is not a replacement receipt and does not change existing persisted success bodies.
- Add fail-closed registry↔receipt integrity, direct-DML/minimum-privilege protection, PostgreSQL 18 concurrency acceptance and compatibility/rollback planning.
- Advance the forward compatibility class/floor from `2 / 2` to the minimum monotonic next value `3 / 3`, so pre-registry class-2 writers cannot start after registry enforcement.

## Capabilities

### New Capabilities

- `admin-command-registry`: global command identity, reservation, actor-first conflict ordering, immutable reservation metadata, backfill and compatibility behavior.

### Modified Capabilities

- `asset-admin-command`: existing completed receipt writers must reserve/lookup through the global registry while preserving existing receipt replay and K1 semantics.
- `relay-node-management-operations`: Node Monitoring Enable/Disable commands join the same global namespace; Health/Connection Test remain non-command observations.

## Dependencies

- Ops Phase 7 frozen requirements and Architecture Review PASS baseline: `sunxu/relay-station-ops@594a349435dbb6c2d4265be79fb913015b1b05c5`.
- Control current planning baseline: `905c621b7db5c167af0ec6d0b0104504196f7b76`; Phase 6 product baseline remains `0491a1e8840596e08348ef123c48f1585b53ad3b`, migration 36, compatibility class/floor 2/2.
- Existing canonical `asset-admin-command`, `relay-node-asset-lifecycle`, `relay-node-management-operations` and compatibility barrier contracts.

## Impact

Implementation will require an additive PostgreSQL migration, controlled Store functions/queries and migration of existing command writers. It does not modify Gateway or CLIProxyAPI, does not add account mutation, does not change AI request routing/scheduling and does not change existing asset K1 semantics.

## Non-Goals

No Disable/Enable/Remove/upload account operation, no credential body handling, no `account_admin_operations`, no Phase 7 UI, no Node hardening, no generic workflow engine, no replacement of completed receipts, no data-plane participation.

## Planning status

Planning = COMPLETE. Independent implementation-readiness review = COMPLETE / READY. Implementation = COMPLETE — READY FOR INDEPENDENT IMPLEMENTATION REVIEW. Archive = NOT RUN. Stage 7N and Stage 7B remain NOT STARTED.
