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
    sqlc.arg(provider_results)::jsonb,
    sqlc.arg(snapshot_items)::jsonb,
    sqlc.arg(duplicate_evidence)::jsonb
);

-- name: FinalizeAccountInventoryPollRunWithLifecycle :one
SELECT * FROM public.control_finalize_account_inventory_poll_run_with_lifecycle(
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
    sqlc.arg(provider_results)::jsonb,
    sqlc.arg(snapshot_items)::jsonb,
    sqlc.arg(duplicate_evidence)::jsonb
);

-- name: CheckAccountInventoryLifecycleCompatibility :one
SELECT (
    to_regclass('public.account_inventory') IS NOT NULL
    AND to_regprocedure(
        'public.control_finalize_account_inventory_poll_run_with_lifecycle(uuid,uuid,boolean,boolean,boolean,text,boolean,boolean,boolean,text,text,integer,integer,integer,integer,integer,text,text,jsonb,jsonb,jsonb)'
    ) IS NOT NULL
    AND coalesce(has_function_privilege(
        current_user,
        to_regprocedure(
            'public.control_finalize_account_inventory_poll_run_with_lifecycle(uuid,uuid,boolean,boolean,boolean,text,boolean,boolean,boolean,text,text,integer,integer,integer,integer,integer,text,text,jsonb,jsonb,jsonb)'
        ),
        'EXECUTE'
    ), false)
    AND to_regprocedure(
        'public.control_list_current_account_inventory_lifecycle(uuid,text,text,text,integer)'
    ) IS NOT NULL
    AND coalesce(has_function_privilege(
        current_user,
        to_regprocedure(
            'public.control_list_current_account_inventory_lifecycle(uuid,text,text,text,integer)'
        ),
        'EXECUTE'
    ), false)
    AND to_regprocedure(
        'public.control_list_account_inventory_lifecycle_metrics()'
    ) IS NOT NULL
    AND coalesce(has_function_privilege(
        current_user,
        to_regprocedure('public.control_list_account_inventory_lifecycle_metrics()'),
        'EXECUTE'
    ), false)
    AND NOT coalesce(has_function_privilege(
        current_user,
        to_regprocedure(
            'public.control_finalize_account_inventory_poll_run(uuid,uuid,boolean,boolean,boolean,text,boolean,boolean,boolean,text,text,integer,integer,integer,integer,integer,text,text,jsonb,jsonb,jsonb)'
        ),
        'EXECUTE'
    ), false)
)::boolean AS compatible;

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
SELECT latest_finalized.instance_id, result.provider, result.snapshot_complete,
       result.promotion_applied,
       (result.promotion_applied OR result.promotion_skipped_reason IS NOT NULL)::boolean
           AS promotion_evaluated,
       result.promotion_skipped_reason
FROM latest_finalized
JOIN account_inventory_poll_provider_results AS result
  ON result.poll_run_id = latest_finalized.poll_run_id
ORDER BY latest_finalized.instance_id, result.provider;

-- name: ListCurrentAccountInventorySnapshot :many
SELECT snapshot.account_key::text AS account_key,
       snapshot.normalized_email::text AS normalized_email,
       snapshot.basic_status::text AS basic_status,
       snapshot.success_count::bigint AS success_count,
       snapshot.failed_count::bigint AS failed_count,
       snapshot.recent_request_count::bigint AS recent_request_count,
       snapshot.last_refresh_at::timestamptz AS last_refresh_at,
       snapshot.next_retry_at::timestamptz AS next_retry_at,
       snapshot.source_updated_at::timestamptz AS source_updated_at,
       snapshot.observed_at::timestamptz AS observed_at
FROM public.control_list_current_account_inventory_snapshot(
    sqlc.arg(instance_id)::uuid,
    sqlc.arg(provider)::text,
    sqlc.arg(after_account_key)::text,
    sqlc.arg(page_limit)::integer
) AS snapshot;

-- name: ListCurrentAccountInventoryLifecycle :many
SELECT inventory.provider::text AS provider,
       inventory.account_key::text AS account_key,
       inventory.normalized_email::text AS normalized_email,
       inventory.basic_status::text AS basic_status,
       inventory.success_count::bigint AS success_count,
       inventory.failed_count::bigint AS failed_count,
       inventory.recent_request_count::bigint AS recent_request_count,
       inventory.last_refresh_at::timestamptz AS last_refresh_at,
       inventory.next_retry_at::timestamptz AS next_retry_at,
       inventory.source_updated_at::timestamptz AS source_updated_at,
       inventory.lifecycle::text AS lifecycle,
       inventory.consecutive_missing_count::integer AS consecutive_missing_count,
       inventory.missing_since::timestamptz AS missing_since,
       inventory.out_of_scope_since::timestamptz AS out_of_scope_since,
       inventory.first_seen_at::timestamptz AS first_seen_at,
       inventory.last_seen_at::timestamptz AS last_seen_at,
       inventory.current_poll_run_id::uuid AS current_poll_run_id,
       inventory.current_scheduled_at::timestamptz AS current_scheduled_at,
       inventory.source_observed_at::timestamptz AS source_observed_at,
       inventory.source_node_version::text AS source_node_version,
       inventory.source_node_commit::text AS source_node_commit,
       inventory.updated_at::timestamptz AS updated_at
FROM public.control_list_current_account_inventory_lifecycle(
    sqlc.arg(instance_id)::uuid,
    sqlc.arg(provider)::text,
    sqlc.arg(lifecycle)::text,
    sqlc.arg(after_account_key)::text,
    sqlc.arg(page_limit)::integer
) AS inventory;

-- name: ListAccountInventoryLifecycleMetrics :many
SELECT metric.instance_id::uuid AS instance_id,
       metric.provider::text AS provider,
       metric.lifecycle::text AS lifecycle,
       metric.account_count::bigint AS account_count
FROM public.control_list_account_inventory_lifecycle_metrics() AS metric;
