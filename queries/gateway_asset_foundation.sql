-- name: GetGatewayAssetByInstanceID :one
SELECT
    instance_id,
    singleton_id,
    lifecycle_status,
    display_name,
    management_endpoint,
    COALESCE(reader_secret_configured, false)::boolean AS secret_configured,
    created_at,
    updated_at,
    revision,
    retired_at,
    retired_by,
    retire_reason
FROM gateway_instances
WHERE instance_id = sqlc.arg(instance_id)::uuid;

-- name: LockGatewayAssetForUpdate :one
SELECT
    instance_id,
    singleton_id,
    lifecycle_status,
    display_name,
    management_endpoint,
    COALESCE(reader_secret_configured, false)::boolean AS secret_configured,
    created_at,
    updated_at,
    revision,
    retired_at,
    retired_by,
    retire_reason
FROM gateway_instances
WHERE instance_id = sqlc.arg(instance_id)::uuid
FOR UPDATE;

-- name: GetRuntimeCompatibilityMarker :one
SELECT singleton_id, schema_version, phase6_evidence_floor, updated_at
FROM control_runtime_compatibility
WHERE singleton_id = 1;

-- name: GetAssetAdminCommandReceipt :one
SELECT
    command_id,
    command_kind,
    intent_encoding_version,
    canonical_intent_hash,
    sanitized_result,
    response_status,
    actor_admin_id,
    committed_at,
    secret_fingerprint_key_version
FROM asset_admin_command_receipts
WHERE command_id = sqlc.arg(command_id)::uuid;

-- name: GetGatewayReplacementByOldInstanceID :one
SELECT
    replacement_id,
    old_instance_id,
    new_instance_id,
    replaced_at,
    replaced_by,
    command_id
FROM gateway_asset_replacements
WHERE old_instance_id = sqlc.arg(old_instance_id)::uuid;

-- name: GetGatewayReplacementByNewInstanceID :one
SELECT
    replacement_id,
    old_instance_id,
    new_instance_id,
    replaced_at,
    replaced_by,
    command_id
FROM gateway_asset_replacements
WHERE new_instance_id = sqlc.arg(new_instance_id)::uuid;
