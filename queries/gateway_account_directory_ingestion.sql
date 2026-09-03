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

-- name: GetGatewayDirectoryAttemptNow :one
SELECT clock_timestamp()::timestamptz AS db_now;

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

-- name: RecordGatewayDirectoryAttemptFailure :one
WITH db_now AS (
    SELECT clock_timestamp() AS db_now
), updated AS (
    UPDATE gateway_directory_ingestion_runs AS run
    SET status = CASE
        WHEN sqlc.arg(retryable)::boolean
             AND run.attempt_count < 2
             AND db_now.db_now < run.scheduled_at + interval '120 seconds'
        THEN 'retry_wait'
        ELSE 'failed'
    END,
    lease_expires_at = NULL,
    lease_fencing_token = NULL,
    terminal_at = CASE
        WHEN sqlc.arg(retryable)::boolean
             AND run.attempt_count < 2
             AND db_now.db_now < run.scheduled_at + interval '120 seconds'
        THEN NULL
        ELSE db_now.db_now
    END,
    last_failure_class = sqlc.arg(last_failure_class)::text
    FROM db_now
    WHERE run.ingestion_run_id = sqlc.arg(ingestion_run_id)::uuid
      AND run.gateway_instance_id = sqlc.arg(gateway_instance_id)::uuid
      AND run.status = 'running'
      AND run.lease_fencing_token = sqlc.arg(lease_fencing_token)::uuid
      AND run.lease_expires_at > db_now.db_now
    RETURNING *
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

-- name: LockGatewayDirectoryInstance :one
SELECT instance_id
FROM gateway_instances
WHERE instance_id = sqlc.arg(gateway_instance_id)::uuid
FOR UPDATE;

-- name: GetGatewayDirectoryCurrentStateForUpdate :one
SELECT *
FROM gateway_directory_current_state
WHERE gateway_instance_id = sqlc.arg(gateway_instance_id)::uuid
FOR UPDATE;

-- name: GetGatewayDirectoryFinalizeRunningRun :one
SELECT *
FROM gateway_directory_ingestion_runs
WHERE ingestion_run_id = sqlc.arg(ingestion_run_id)::uuid
  AND gateway_instance_id = sqlc.arg(gateway_instance_id)::uuid
  AND status = 'running'
  AND lease_fencing_token = sqlc.arg(lease_fencing_token)::uuid
  AND lease_expires_at > sqlc.arg(db_now)::timestamptz
FOR UPDATE;

-- name: InsertGatewayDirectorySnapshot :one
INSERT INTO gateway_directory_snapshots (
    gateway_instance_id,
    fingerprint,
    fingerprint_encoding_version,
    schema_version,
    account_count
) VALUES (
    sqlc.arg(gateway_instance_id)::uuid,
    sqlc.arg(fingerprint)::bytea,
    1,
    sqlc.arg(schema_version)::integer,
    sqlc.arg(account_count)::integer
)
ON CONFLICT (gateway_instance_id, fingerprint) DO NOTHING
RETURNING *;

-- name: UpsertGatewayDirectoryCurrentState :one
INSERT INTO gateway_directory_current_state (
    gateway_instance_id,
    current_snapshot_id,
    current_content_fingerprint,
    last_success_received_at,
    last_source_generated_at,
    last_success_run_id,
    updated_at
) VALUES (
    sqlc.arg(gateway_instance_id)::uuid,
    sqlc.arg(current_snapshot_id)::uuid,
    sqlc.arg(current_content_fingerprint)::bytea,
    sqlc.arg(last_success_received_at)::timestamptz,
    sqlc.arg(last_source_generated_at)::timestamptz,
    sqlc.arg(last_success_run_id)::uuid,
    sqlc.arg(updated_at)::timestamptz
)
ON CONFLICT (gateway_instance_id) DO UPDATE SET
    current_snapshot_id = EXCLUDED.current_snapshot_id,
    current_content_fingerprint = EXCLUDED.current_content_fingerprint,
    last_success_received_at = EXCLUDED.last_success_received_at,
    last_source_generated_at = EXCLUDED.last_source_generated_at,
    last_success_run_id = EXCLUDED.last_success_run_id,
    updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: FinalizeGatewayDirectoryIngestionRunSucceeded :one
WITH final_now AS (
    SELECT clock_timestamp() AS db_now
), lease_guard AS (
    SELECT
        EXISTS (
            SELECT 1
            FROM gateway_directory_ingestion_runs AS run, final_now
            WHERE run.ingestion_run_id = sqlc.arg(ingestion_run_id)::uuid
              AND run.gateway_instance_id = sqlc.arg(gateway_instance_id)::uuid
              AND run.status = 'running'
              AND run.lease_fencing_token = sqlc.arg(lease_fencing_token)::uuid
              AND run.lease_expires_at > final_now.db_now
        ) AS lease_valid,
        EXISTS (
            SELECT 1
            FROM gateway_directory_ingestion_runs AS run, final_now
            WHERE run.ingestion_run_id = sqlc.arg(ingestion_run_id)::uuid
              AND run.gateway_instance_id = sqlc.arg(gateway_instance_id)::uuid
              AND run.status = 'running'
              AND run.lease_fencing_token = sqlc.arg(lease_fencing_token)::uuid
              AND run.lease_expires_at > final_now.db_now
              AND sqlc.arg(source_generated_at)::timestamptz <= final_now.db_now + interval '30 seconds'
              AND sqlc.arg(source_generated_at)::timestamptz >= final_now.db_now - interval '24 hours'
              AND (
                  sqlc.arg(previous_source_generated_at)::timestamptz IS NULL
                  OR sqlc.arg(source_generated_at)::timestamptz >= sqlc.arg(previous_source_generated_at)::timestamptz - interval '5 minutes'
              )
        ) AS source_time_valid
), updated AS (
    UPDATE gateway_directory_ingestion_runs AS run
    SET status = 'succeeded',
        terminal_at = final_now.db_now,
        received_at = final_now.db_now,
        source_generated_at = sqlc.arg(source_generated_at)::timestamptz,
        content_fingerprint = sqlc.arg(content_fingerprint)::bytea,
        snapshot_id = sqlc.arg(snapshot_id)::uuid,
        account_count = sqlc.arg(account_count)::integer,
        outcome = sqlc.arg(outcome)::text,
        lease_expires_at = NULL,
        lease_fencing_token = NULL
    FROM final_now, lease_guard
    WHERE run.ingestion_run_id = sqlc.arg(ingestion_run_id)::uuid
      AND run.gateway_instance_id = sqlc.arg(gateway_instance_id)::uuid
      AND run.status = 'running'
      AND run.lease_fencing_token = sqlc.arg(lease_fencing_token)::uuid
      AND lease_guard.lease_valid
      AND lease_guard.source_time_valid
    RETURNING 'succeeded'::text AS decision, final_now.db_now::timestamptz AS final_received_at
), source_time_invalid AS (
    SELECT 'source_time_invalid'::text AS decision, final_now.db_now::timestamptz AS final_received_at
    FROM final_now, lease_guard
    WHERE lease_guard.lease_valid
      AND NOT lease_guard.source_time_valid
), lost_lease AS (
    SELECT 'lost_lease'::text AS decision, final_now.db_now::timestamptz AS final_received_at
    FROM final_now, lease_guard
    WHERE NOT lease_guard.lease_valid
)
SELECT decision, final_received_at FROM updated
UNION ALL
SELECT decision, final_received_at FROM source_time_invalid
UNION ALL
SELECT decision, final_received_at FROM lost_lease
LIMIT 1;
