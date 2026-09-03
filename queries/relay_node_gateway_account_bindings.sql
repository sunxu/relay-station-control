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

-- name: GetNodeCentricBindingView :one
WITH db_now AS (
    SELECT clock_timestamp()::timestamptz AS db_now
)
SELECT
    node.instance_id AS relay_node_id,
    binding.binding_id,
    binding.gateway_instance_id,
    binding.gateway_account_id,
    binding.evidence_snapshot_id,
    binding.bound_at,
    binding.bound_by,
    binding.bind_reason,
    current_state.current_snapshot_id,
    current_state.last_success_received_at,
    current_item.account_id AS current_account_id,
    current_item.name AS current_name,
    current_item.platform AS current_platform,
    current_item.type AS current_type,
    current_item.url AS current_url,
    current_item.status AS current_status,
    evidence_item.account_id AS evidence_account_id,
    evidence_item.name AS evidence_name,
    evidence_item.platform AS evidence_platform,
    evidence_item.type AS evidence_type,
    evidence_item.url AS evidence_url,
    evidence_item.status AS evidence_status,
    db_now.db_now AS db_now
FROM relay_node_assets AS node
CROSS JOIN db_now
LEFT JOIN relay_node_gateway_account_bindings AS binding
    ON binding.relay_node_id = node.instance_id AND binding.ended_at IS NULL
LEFT JOIN gateway_directory_current_state AS current_state
    ON current_state.gateway_instance_id = binding.gateway_instance_id
LEFT JOIN gateway_directory_snapshot_items AS current_item
    ON current_item.snapshot_id = current_state.current_snapshot_id
    AND current_item.account_id = binding.gateway_account_id
LEFT JOIN gateway_directory_snapshot_items AS evidence_item
    ON evidence_item.snapshot_id = binding.evidence_snapshot_id
    AND evidence_item.account_id = binding.gateway_account_id
WHERE node.instance_id = sqlc.arg(relay_node_id)::uuid;

-- name: ListGatewayAccountCentricBindingViews :many
WITH db_now AS (
    SELECT clock_timestamp()::timestamptz AS db_now
), target_gateway AS (
    SELECT instance_id
    FROM gateway_instances
    WHERE instance_id = sqlc.arg(gateway_instance_id)::uuid
)
SELECT
    target_gateway.instance_id AS gateway_instance_id,
    current_state.current_snapshot_id,
    current_state.last_success_received_at,
    items.account_id AS gateway_account_id,
    items.name,
    items.platform,
    items.type,
    items.url,
    items.status,
    binding.binding_id,
    binding.relay_node_id,
    binding.evidence_snapshot_id,
    binding.bound_at,
    binding.bound_by,
    binding.bind_reason,
    db_now.db_now AS db_now
FROM target_gateway
CROSS JOIN db_now
LEFT JOIN gateway_directory_current_state AS current_state
    ON current_state.gateway_instance_id = target_gateway.instance_id
LEFT JOIN gateway_directory_snapshot_items AS items
    ON items.snapshot_id = current_state.current_snapshot_id
LEFT JOIN relay_node_gateway_account_bindings AS binding
    ON binding.gateway_instance_id = target_gateway.instance_id
    AND binding.gateway_account_id = items.account_id
    AND binding.ended_at IS NULL
ORDER BY items.account_id ASC;

-- name: ListUnresolvedGatewayAccountBindings :many
WITH db_now AS (
    SELECT clock_timestamp()::timestamptz AS db_now
), target_gateway AS (
    SELECT instance_id
    FROM gateway_instances
    WHERE instance_id = sqlc.arg(gateway_instance_id)::uuid
)
SELECT
    binding.binding_id,
    binding.relay_node_id,
    binding.gateway_instance_id,
    binding.gateway_account_id,
    binding.evidence_snapshot_id,
    binding.bound_at,
    binding.bound_by,
    binding.bind_reason,
    current_state.current_snapshot_id,
    current_state.last_success_received_at,
    evidence_item.name AS evidence_name,
    evidence_item.platform AS evidence_platform,
    evidence_item.type AS evidence_type,
    evidence_item.url AS evidence_url,
    evidence_item.status AS evidence_status,
    db_now.db_now AS db_now
FROM target_gateway
CROSS JOIN db_now
JOIN gateway_directory_current_state AS current_state
    ON current_state.gateway_instance_id = target_gateway.instance_id
JOIN relay_node_gateway_account_bindings AS binding
    ON binding.gateway_instance_id = target_gateway.instance_id
    AND binding.ended_at IS NULL
LEFT JOIN gateway_directory_snapshot_items AS current_item
    ON current_item.snapshot_id = current_state.current_snapshot_id
    AND current_item.account_id = binding.gateway_account_id
LEFT JOIN gateway_directory_snapshot_items AS evidence_item
    ON evidence_item.snapshot_id = binding.evidence_snapshot_id
    AND evidence_item.account_id = binding.gateway_account_id
WHERE current_item.account_id IS NULL
ORDER BY binding.gateway_account_id ASC, binding.relay_node_id ASC;
