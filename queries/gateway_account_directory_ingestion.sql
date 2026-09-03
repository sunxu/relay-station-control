-- name: GetGatewayDirectoryReadTarget :one
SELECT instance_id, management_endpoint, reader_secret_ref
FROM gateway_instances
WHERE instance_id = sqlc.arg(gateway_instance_id)::uuid;

-- name: CreateOrGetGatewayDirectoryIngestionRun :one
WITH locked_gateway AS (
    SELECT instance_id
    FROM gateway_instances
    WHERE instance_id = sqlc.arg(gateway_instance_id)::uuid
    FOR UPDATE
), db_now AS (
    SELECT clock_timestamp() AS db_now
), current_slot AS (
    SELECT to_timestamp(floor(extract(epoch FROM db_now.db_now) / 180) * 180)::timestamptz AS scheduled_at
    FROM db_now
), active_gateway AS (
    SELECT 1
    FROM gateway_directory_ingestion_runs AS active
    JOIN locked_gateway ON active.gateway_instance_id = locked_gateway.instance_id
    WHERE active.status IN ('pending', 'running', 'retry_wait')
    LIMIT 1
), inserted AS (
    INSERT INTO gateway_directory_ingestion_runs (
        gateway_instance_id,
        scheduled_at
    ) SELECT
        locked_gateway.instance_id,
        current_slot.scheduled_at
    FROM locked_gateway
    CROSS JOIN current_slot
    WHERE NOT EXISTS (SELECT 1 FROM active_gateway)
    ON CONFLICT (gateway_instance_id, scheduled_at) DO NOTHING
    RETURNING *, true AS created
)
SELECT * FROM inserted
UNION ALL
SELECT existing.*, false AS created
FROM gateway_directory_ingestion_runs AS existing
JOIN locked_gateway ON existing.gateway_instance_id = locked_gateway.instance_id
JOIN current_slot ON existing.scheduled_at = current_slot.scheduled_at
WHERE NOT EXISTS (SELECT 1 FROM active_gateway)
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
