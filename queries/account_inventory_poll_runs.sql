-- name: ScheduleCurrentAccountInventoryPollRuns :one
SELECT public.control_schedule_account_inventory_poll_runs(
    sqlc.arg(period_seconds)::integer,
    sqlc.arg(poll_start_grace_seconds)::integer,
    sqlc.arg(max_attempts)::integer,
    sqlc.arg(schedule_limit)::integer
)::jsonb AS schedule_result;

-- name: ClaimAccountInventoryPollRun :one
SELECT public.control_claim_account_inventory_poll_run(
    sqlc.arg(lease_fencing_token)::uuid,
    sqlc.arg(lease_seconds)::integer
)::jsonb AS claim_result;

-- name: ReconcileAccountInventoryPollRun :one
SELECT * FROM public.control_reconcile_account_inventory_poll_run(
);

-- name: FinalizeAccountInventoryPollRun :one
SELECT * FROM public.control_finalize_account_inventory_poll_run(
    sqlc.arg(poll_run_id)::uuid,
    sqlc.arg(lease_fencing_token)::uuid,
    sqlc.arg(transport_success)::boolean,
    sqlc.arg(response_shape_valid)::boolean,
    sqlc.arg(contract_valid)::boolean,
    sqlc.narg(inventory_mode)::text,
    sqlc.arg(node_identity_complete)::boolean,
    sqlc.arg(snapshot_complete)::boolean,
    sqlc.arg(degraded)::boolean,
    sqlc.arg(result)::text,
    sqlc.arg(reason)::text,
    sqlc.arg(source_record_count)::integer,
    sqlc.arg(identifiable_record_count)::integer,
    sqlc.arg(unidentified_record_count)::integer,
    sqlc.arg(unsupported_provider_count)::integer,
    sqlc.arg(out_of_scope_provider_count)::integer,
    sqlc.arg(node_version)::text,
    sqlc.arg(node_commit)::text,
    sqlc.arg(provider_results)::jsonb
);

-- name: GetAccountInventoryPollRun :one
SELECT *
FROM account_inventory_poll_runs
WHERE poll_run_id = sqlc.arg(poll_run_id)::uuid;

-- name: ListAccountInventoryPollProviderResults :many
SELECT *
FROM account_inventory_poll_provider_results
WHERE poll_run_id = sqlc.arg(poll_run_id)::uuid
ORDER BY provider;

-- name: ListAccountInventoryPollRunMetrics :many
WITH current_slot AS (
    SELECT to_timestamp(floor(extract(epoch FROM clock_timestamp()) / 300) * 300) AS scheduled_at
), latest AS (
    SELECT DISTINCT ON (run.instance_id)
        run.instance_id,
        run.status,
        run.scheduled_at,
        run.created_at,
        run.first_started_at,
        run.last_started_at
    FROM account_inventory_poll_runs AS run
    ORDER BY run.instance_id, run.scheduled_at DESC
), latest_finalized AS (
    SELECT DISTINCT ON (run.instance_id)
        run.instance_id,
        run.scheduled_at,
        run.transport_success,
        run.contract_valid
    FROM account_inventory_poll_runs AS run
    WHERE run.status = 'finalized'
    ORDER BY run.instance_id, run.scheduled_at DESC
)
SELECT
    latest.instance_id,
    latest.status,
    latest.scheduled_at,
    greatest(0, extract(epoch FROM (
        current_slot.scheduled_at - coalesce(latest_finalized.scheduled_at, latest.scheduled_at)
    )))::double precision AS scheduler_lag_seconds,
    greatest(0, extract(epoch FROM (
        coalesce(latest.last_started_at, clock_timestamp()) - latest.created_at
    )))::double precision AS queue_wait_seconds,
    (latest.first_started_at IS NOT NULL)::boolean AS poll_started,
    coalesce(greatest(0, extract(epoch FROM (
        latest.first_started_at - latest.scheduled_at
    ))), 0)::double precision AS poll_start_lag_seconds,
    latest_finalized.transport_success,
    latest_finalized.contract_valid
FROM latest
CROSS JOIN current_slot
LEFT JOIN latest_finalized USING (instance_id)
ORDER BY latest.instance_id;

-- name: ListAccountInventoryProviderMetrics :many
WITH latest_finalized AS (
    SELECT DISTINCT ON (run.instance_id)
        run.instance_id, run.poll_run_id
    FROM account_inventory_poll_runs AS run
    WHERE run.status = 'finalized'
    ORDER BY run.instance_id, run.scheduled_at DESC
)
SELECT latest_finalized.instance_id, result.provider, result.snapshot_complete
FROM latest_finalized
JOIN account_inventory_poll_provider_results AS result
  ON result.poll_run_id = latest_finalized.poll_run_id
ORDER BY latest_finalized.instance_id, result.provider;
