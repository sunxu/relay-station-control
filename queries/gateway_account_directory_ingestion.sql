-- name: GetGatewayDirectoryReadTarget :one
SELECT instance_id, management_endpoint, reader_secret_ref
FROM gateway_instances
WHERE instance_id = sqlc.arg(gateway_instance_id)::uuid;

-- name: GetGatewayDirectoryCurrentScheduledAt :one
SELECT to_timestamp(floor(extract(epoch FROM clock_timestamp()) / 180) * 180)::timestamptz AS scheduled_at;

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

-- name: ClaimGatewayDirectoryIngestionRun :one
WITH db_now AS (
    SELECT clock_timestamp() AS db_now
), candidate AS (
    SELECT run.ingestion_run_id
    FROM gateway_directory_ingestion_runs AS run, db_now
    WHERE run.gateway_instance_id = sqlc.arg(gateway_instance_id)::uuid
      AND run.status IN ('pending', 'retry_wait')
      AND run.attempt_count < 2
      AND db_now.db_now < run.scheduled_at + interval '120 seconds'
    ORDER BY run.scheduled_at ASC
    FOR UPDATE SKIP LOCKED
    LIMIT 1
), updated AS (
    UPDATE gateway_directory_ingestion_runs AS run
    SET status = 'running',
        attempt_count = run.attempt_count + 1,
        first_started_at = COALESCE(run.first_started_at, db_now.db_now),
        last_started_at = db_now.db_now,
        lease_expires_at = db_now.db_now + interval '15 seconds',
        lease_fencing_token = sqlc.arg(lease_fencing_token)::uuid
    FROM candidate, db_now
    WHERE run.ingestion_run_id = candidate.ingestion_run_id
    RETURNING run.*
)
SELECT * FROM updated;

-- name: GetGatewayDirectoryIngestionRunLease :one
SELECT *
FROM gateway_directory_ingestion_runs
WHERE ingestion_run_id = sqlc.arg(ingestion_run_id)::uuid
  AND gateway_instance_id = sqlc.arg(gateway_instance_id)::uuid
  AND status = 'running'
  AND lease_fencing_token = sqlc.arg(lease_fencing_token)::uuid
  AND lease_expires_at > clock_timestamp();

-- name: ListExpiredGatewayDirectoryIngestionRuns :many
SELECT *
FROM gateway_directory_ingestion_runs
WHERE status = 'running'
  AND lease_expires_at IS NOT NULL
  AND lease_expires_at <= clock_timestamp()
ORDER BY lease_expires_at, scheduled_at, gateway_instance_id
LIMIT sqlc.arg(page_limit)::integer;

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
