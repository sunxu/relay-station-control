-- +goose Up

-- PostgreSQL's FOR UPDATE row-locking clause requires UPDATE privilege on at
-- least one column of the target table in addition to SELECT, even for a
-- pure no-op lock that never issues a real UPDATE statement. The binding
-- mutation transactions (Bind/Rebind/Unbind) lock relay_node_assets
-- (LockRelayNodeAssetForBinding) and gateway_instances
-- (LockGatewayDirectoryCurrentState / LockGatewayDirectoryInstance) with
-- SELECT ... FOR UPDATE, so relay_control_runtime needs this privilege.
--
-- A GRANT UPDATE on a GENERATED ALWAYS column (reader_secret_configured)
-- was evaluated first and empirically does NOT satisfy PostgreSQL's ACL
-- check for FOR UPDATE (verified against a running instance: the lock
-- still fails with "permission denied for table ..."). A real, storage
-- column is required.
--
-- created_at is chosen as the minimal-blast-radius real column: it is not
-- part of Node/Gateway identity, not a secret reference, and not the
-- management endpoint. Even with this grant, relay_control_runtime cannot
-- actually perform any UPDATE on these tables in practice: both tables'
-- endpoint CHECK constraints (relay_node_assets_endpoint_canonical /
-- gateway_instances_endpoint_canonical) invoke
-- public.control_normalize_asset_endpoint(), whose EXECUTE privilege is
-- not granted to relay_control_runtime, and PostgreSQL re-evaluates all
-- CHECK constraints on any UPDATE regardless of which column changed. So
-- any real UPDATE attempt (including "SET created_at = created_at") fails
-- with "permission denied for function control_normalize_asset_endpoint",
-- while SELECT ... FOR UPDATE (a lock only, no constraint evaluation)
-- succeeds.

GRANT UPDATE (created_at) ON TABLE relay_node_assets
TO relay_control_runtime;

GRANT UPDATE (created_at) ON TABLE gateway_instances
TO relay_control_runtime;

-- +goose Down

REVOKE UPDATE (created_at) ON TABLE relay_node_assets
FROM relay_control_runtime;

REVOKE UPDATE (created_at) ON TABLE gateway_instances
FROM relay_control_runtime;
