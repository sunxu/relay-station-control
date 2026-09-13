-- name: GetAdminCommandReservation :one
SELECT command_id, actor_admin_id, command_domain, command_kind,
       intent_encoding_version, canonical_intent_hash,
       secret_fingerprint_key_version, reserved_at
FROM admin_command_registry
WHERE command_id = sqlc.arg(command_id)::uuid;

-- name: ReserveAdminCommand :one
SELECT command_id, actor_admin_id, command_domain, command_kind,
       intent_encoding_version, canonical_intent_hash,
       secret_fingerprint_key_version, reserved_at
FROM control_reserve_admin_command_v1(
    sqlc.arg(command_id)::uuid,
    sqlc.arg(actor_admin_id)::uuid,
    sqlc.arg(command_domain)::text,
    sqlc.arg(command_kind)::text,
    sqlc.arg(intent_encoding_version)::smallint,
    sqlc.arg(canonical_intent_hash)::bytea,
    sqlc.narg(secret_fingerprint_key_version)::smallint
);

-- name: InsertControlledAssetAdminCommandReceipt :exec
SELECT control_insert_asset_admin_command_receipt_v1(
    sqlc.arg(command_id)::uuid,
    sqlc.arg(command_kind)::text,
    sqlc.arg(intent_encoding_version)::smallint,
    sqlc.arg(canonical_intent_hash)::bytea,
    sqlc.arg(sanitized_result)::jsonb,
    sqlc.arg(response_status)::smallint,
    sqlc.arg(actor_admin_id)::uuid,
    sqlc.narg(committed_at)::timestamptz,
    sqlc.narg(secret_fingerprint_key_version)::smallint
);
