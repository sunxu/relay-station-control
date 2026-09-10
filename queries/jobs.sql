-- name: GetAsyncJobByIdempotencyKey :one
SELECT * FROM public.control_get_async_job_by_idempotency_key($1);

-- name: EnqueueAsyncJob :one
SELECT * FROM public.control_enqueue_async_job(
    sqlc.arg(job_id)::uuid,
    sqlc.arg(idempotency_key)::text,
    sqlc.arg(job_kind)::text,
    sqlc.arg(payload_schema_version)::integer,
    sqlc.arg(operation_id)::uuid,
    sqlc.arg(payload)::jsonb,
    sqlc.arg(payload_hash)::bytea,
    sqlc.arg(priority)::smallint,
    sqlc.arg(outbox_event_id)::uuid,
    sqlc.arg(outbox_event_key)::text,
    sqlc.arg(publisher_enabled)::boolean
);

-- name: RequestAsyncJobCancel :one
SELECT * FROM public.control_request_async_job_cancel(
    sqlc.arg(job_id)::uuid,
    sqlc.arg(reason_code)::text
);

-- name: ClaimRunnableAsyncJob :one
SELECT * FROM public.control_claim_async_job(
    sqlc.arg(lease_owner)::text,
    sqlc.arg(lease_fencing_token)::uuid
);

-- name: RenewAsyncJobLease :one
SELECT * FROM public.control_renew_async_job_lease(
    sqlc.arg(job_id)::uuid,
    sqlc.arg(lease_fencing_token)::uuid,
    sqlc.arg(lease_seconds)::integer
);

-- name: TransitionAsyncJobFenced :one
SELECT * FROM public.control_transition_async_job_fenced(
    sqlc.arg(job_id)::uuid,
    sqlc.arg(expected_status)::text,
    sqlc.arg(lease_fencing_token)::uuid,
    sqlc.arg(target_status)::text,
    sqlc.arg(event_type)::text,
    sqlc.arg(retry_delay_seconds)::integer,
    sqlc.narg(reason_code)::text,
    sqlc.narg(error_code)::text,
    sqlc.narg(error_summary)::text,
    sqlc.arg(actor_type)::text,
    sqlc.arg(release_lease)::boolean
);

-- name: ClaimExpiredAsyncJob :one
SELECT
    claimed.*,
    (claimed.deadline_at <= clock_timestamp())::boolean AS deadline_exceeded
FROM public.control_claim_expired_async_job(
    sqlc.arg(lease_owner)::text,
    sqlc.arg(lease_fencing_token)::uuid
) AS claimed;

-- name: LockAsyncJob :one
SELECT * FROM public.control_lock_async_job(sqlc.arg(job_id)::uuid);

-- name: ClaimOutboxEvent :one
SELECT * FROM public.control_claim_outbox_event(
    sqlc.arg(lease_owner)::text,
    sqlc.arg(lease_fencing_token)::uuid,
    sqlc.arg(lease_seconds)::integer
);

-- name: TransitionOutboxEventFenced :one
SELECT * FROM public.control_transition_outbox_fenced(
    sqlc.arg(event_id)::uuid,
    sqlc.arg(lease_fencing_token)::uuid,
    sqlc.arg(target_status)::text,
    sqlc.arg(retry_delay_seconds)::integer,
    sqlc.narg(error_code)::text
);

-- name: RenewOutboxEventLease :one
SELECT * FROM public.control_renew_outbox_lease(
    sqlc.arg(event_id)::uuid,
    sqlc.arg(lease_fencing_token)::uuid,
    sqlc.arg(lease_seconds)::integer
);

-- name: ListAsyncJobsPublic :many
SELECT
    job.job_id,
    job.operation_id,
    job.job_kind,
    job.status,
    job.attempt_count,
    job.max_attempts,
    job.available_at,
    job.started_at,
    job.completed_at,
    (job.cancel_requested_at IS NOT NULL)::boolean AS cancel_requested,
    job.error_code,
    COALESCE(outbox.status, 'none')::text AS outbox_status,
    job.created_at,
    job.updated_at
FROM async_jobs AS job
LEFT JOIN LATERAL (
    SELECT event.status
    FROM operation_outbox AS event
    WHERE event.job_id = job.job_id
    ORDER BY event.created_at DESC, event.event_id DESC
    LIMIT 1
) AS outbox ON true
WHERE (sqlc.narg(job_kind)::text IS NULL OR job.job_kind = sqlc.narg(job_kind))
  AND (sqlc.narg(status)::text IS NULL OR job.status = sqlc.narg(status))
  AND (sqlc.narg(created_from)::timestamptz IS NULL OR job.created_at >= sqlc.narg(created_from))
  AND (sqlc.narg(created_to)::timestamptz IS NULL OR job.created_at < sqlc.narg(created_to))
  AND (
      sqlc.narg(after_created_at)::timestamptz IS NULL
      OR (job.created_at, job.job_id) < (
          sqlc.narg(after_created_at)::timestamptz,
          sqlc.narg(after_job_id)::uuid
      )
  )
ORDER BY job.created_at DESC, job.job_id DESC
LIMIT sqlc.arg(page_size);

-- name: GetAsyncJobPublic :one
SELECT
    job.job_id,
    job.operation_id,
    job.job_kind,
    job.status,
    job.attempt_count,
    job.max_attempts,
    job.available_at,
    job.started_at,
    job.completed_at,
    (job.cancel_requested_at IS NOT NULL)::boolean AS cancel_requested,
    job.error_code,
    COALESCE(outbox.status, 'none')::text AS outbox_status,
    job.created_at,
    job.updated_at
FROM async_jobs AS job
LEFT JOIN LATERAL (
    SELECT event.status
    FROM operation_outbox AS event
    WHERE event.job_id = job.job_id
    ORDER BY event.created_at DESC, event.event_id DESC
    LIMIT 1
) AS outbox ON true
WHERE job.job_id = $1;

-- name: ListAsyncJobEventsPublic :many
SELECT
    sequence,
    event_type,
    from_status,
    to_status,
    attempt,
    reason_code,
    error_code,
    actor_type,
    occurred_at
FROM async_job_events
WHERE job_id = $1
ORDER BY sequence;

-- name: ListActiveAsyncJobKinds :many
SELECT
    job_kind,
    payload_schema_version,
    default_timeout_seconds,
    lease_seconds,
    heartbeat_interval_seconds,
    default_max_attempts,
    default_max_verification_attempts,
    replay_safe,
    rollback_allowed,
    allow_unknown_effect_replay,
    allow_direct_success
FROM async_job_kinds
WHERE lifecycle_status = 'active'
ORDER BY job_kind;

-- name: GetAsyncJobMetrics :one
SELECT
    count(*) FILTER (WHERE status = 'pending')::bigint AS pending,
    count(*) FILTER (WHERE status = 'running')::bigint AS running,
    count(*) FILTER (WHERE status = 'verifying')::bigint AS verifying,
    count(*) FILTER (WHERE status = 'retry_wait')::bigint AS retry_wait,
    count(*) FILTER (WHERE status = 'rolling_back')::bigint AS rolling_back,
    count(*) FILTER (WHERE status = 'succeeded')::bigint AS succeeded,
    count(*) FILTER (WHERE status = 'failed')::bigint AS failed,
    count(*) FILTER (WHERE status = 'rolled_back')::bigint AS rolled_back,
    count(*) FILTER (WHERE status = 'cancelled')::bigint AS cancelled,
    COALESCE(extract(epoch FROM (
        clock_timestamp() - (min(created_at) FILTER (
            WHERE status IN ('pending', 'retry_wait') AND available_at <= clock_timestamp()
        ))
    )), 0)::double precision
        AS oldest_pending_seconds,
    count(*) FILTER (WHERE status IN ('running', 'verifying', 'rolling_back')
        AND lease_expires_at <= clock_timestamp())::bigint AS expired_leases
FROM async_jobs;

-- name: GetOutboxOldestPendingSeconds :one
SELECT COALESCE(
    extract(epoch FROM (
        clock_timestamp() - (min(created_at) FILTER (
            WHERE status IN ('pending', 'retry_wait') AND available_at <= clock_timestamp()
        ))
    )),
    0
)::double precision AS oldest_pending_seconds
FROM operation_outbox;

-- name: GetActiveAsyncJobKind :one
SELECT * FROM async_job_kinds
WHERE job_kind = $1 AND payload_schema_version = $2 AND lifecycle_status = 'active';
