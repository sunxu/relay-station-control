-- name: GetRelayBindingDBTime :one
SELECT clock_timestamp()::timestamptz;

-- name: LockRelayNodeAssetForBinding :one
SELECT instance_id
FROM relay_node_assets
WHERE instance_id = sqlc.arg(instance_id)::uuid
FOR UPDATE;

-- name: LockGatewayDirectoryCurrentState :one
SELECT *
FROM gateway_directory_current_state
WHERE gateway_instance_id = sqlc.arg(gateway_instance_id)::uuid
FOR UPDATE;

-- name: GetGatewayDirectorySnapshotItem :one
SELECT snapshot_id, account_id, name, platform, type, url, status
FROM gateway_directory_snapshot_items
WHERE snapshot_id = sqlc.arg(snapshot_id)::uuid
  AND account_id = sqlc.arg(account_id)::bigint;

-- name: GetCurrentRelayNodeGatewayAccountBindingByNode :one
SELECT *
FROM relay_node_gateway_account_bindings
WHERE relay_node_id = sqlc.arg(relay_node_id)::uuid
  AND ended_at IS NULL;

-- name: GetCurrentRelayNodeGatewayAccountBindingByAccount :one
SELECT *
FROM relay_node_gateway_account_bindings
WHERE gateway_instance_id = sqlc.arg(gateway_instance_id)::uuid
  AND gateway_account_id = sqlc.arg(gateway_account_id)::bigint
  AND ended_at IS NULL;

-- name: LockCurrentRelayNodeGatewayAccountBindingByNode :one
SELECT *
FROM relay_node_gateway_account_bindings
WHERE relay_node_id = sqlc.arg(relay_node_id)::uuid
  AND ended_at IS NULL
FOR UPDATE;

-- name: LockCurrentRelayNodeGatewayAccountBindingByAccount :one
SELECT *
FROM relay_node_gateway_account_bindings
WHERE gateway_instance_id = sqlc.arg(gateway_instance_id)::uuid
  AND gateway_account_id = sqlc.arg(gateway_account_id)::bigint
  AND ended_at IS NULL
FOR UPDATE;

-- name: InsertOpenRelayNodeGatewayAccountBinding :one
INSERT INTO relay_node_gateway_account_bindings (
    relay_node_id,
    gateway_instance_id,
    gateway_account_id,
    evidence_snapshot_id,
    bound_at,
    bound_by,
    bind_reason
) VALUES (
    sqlc.arg(relay_node_id)::uuid,
    sqlc.arg(gateway_instance_id)::uuid,
    sqlc.arg(gateway_account_id)::bigint,
    sqlc.arg(evidence_snapshot_id)::uuid,
    sqlc.arg(bound_at)::timestamptz,
    sqlc.arg(bound_by)::uuid,
    sqlc.arg(bind_reason)::text
)
RETURNING *;

-- name: CloseCurrentRelayNodeGatewayAccountBinding :one
UPDATE relay_node_gateway_account_bindings
SET ended_at = sqlc.arg(ended_at)::timestamptz,
    ended_by = sqlc.arg(ended_by)::uuid,
    end_reason = sqlc.arg(end_reason)::text
WHERE binding_id = sqlc.arg(binding_id)::uuid
  AND relay_node_id = sqlc.arg(relay_node_id)::uuid
  AND gateway_instance_id = sqlc.arg(gateway_instance_id)::uuid
  AND gateway_account_id = sqlc.arg(gateway_account_id)::bigint
  AND ended_at IS NULL
RETURNING *;
