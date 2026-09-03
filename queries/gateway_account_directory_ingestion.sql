-- name: GetGatewayDirectoryReadTarget :one
SELECT instance_id, management_endpoint, reader_secret_ref
FROM gateway_instances
WHERE instance_id = sqlc.arg(gateway_instance_id)::uuid;

-- name: CreateOrGetGatewayDirectoryIngestionRun :one
WITH inserted AS (
    INSERT INTO gateway_directory_ingestion_runs (
        gateway_instance_id,
        scheduled_at
    )
    VALUES (
        sqlc.arg(gateway_instance_id)::uuid,
        sqlc.arg(scheduled_at)::timestamptz
    )
    ON CONFLICT (gateway_instance_id, scheduled_at) DO NOTHING
    RETURNING *
)
SELECT * FROM inserted
UNION ALL
SELECT existing.*
FROM gateway_directory_ingestion_runs AS existing
WHERE existing.gateway_instance_id = sqlc.arg(gateway_instance_id)::uuid
  AND existing.scheduled_at = sqlc.arg(scheduled_at)::timestamptz
  AND NOT EXISTS (SELECT 1 FROM inserted)
LIMIT 1;

-- name: GetGatewayDirectoryIngestionRun :one
SELECT *
FROM gateway_directory_ingestion_runs
WHERE ingestion_run_id = sqlc.arg(ingestion_run_id)::uuid;

-- name: GetGatewayDirectorySnapshotByFingerprint :one
SELECT *
FROM gateway_directory_snapshots
WHERE gateway_instance_id = sqlc.arg(gateway_instance_id)::uuid
  AND fingerprint = sqlc.arg(fingerprint)::bytea;

-- name: ListGatewayDirectorySnapshotItems :many
SELECT snapshot_id, account_id, name, platform, type, url, status
FROM gateway_directory_snapshot_items
WHERE snapshot_id = sqlc.arg(snapshot_id)::uuid
ORDER BY account_id ASC;

-- name: GetGatewayDirectoryCurrentState :one
SELECT *
FROM gateway_directory_current_state
WHERE gateway_instance_id = sqlc.arg(gateway_instance_id)::uuid;
