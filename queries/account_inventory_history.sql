-- name: CheckAccountInventoryHistoryCompatibility :one
SELECT public.control_history_schema_compatibility_v1()::jsonb AS compatibility;

-- name: PlanAccountInventoryHistory :one
SELECT public.control_plan_account_inventory_history_v1(
    sqlc.arg(schedule_limit)::integer
)::jsonb AS plan_result;

-- name: ClaimAccountInventoryHistoryCompaction :one
SELECT
    claim.compaction_run_id,
    claim.summary_date,
    claim.instance_id,
    claim.provider_policy_version,
    claim.status,
    claim.failed_from,
    claim.claim_owner,
    claim.lease_expires_at,
    claim.fencing_token,
    claim.attempt_count,
    claim.checksum_version,
    claim.source_snapshot_count,
    claim.source_poll_count,
    claim.source_provider_result_count,
    claim.source_duplicate_count,
    claim.source_checksum,
    claim.deleted_snapshot_count,
    claim.deleted_poll_count,
    claim.deleted_provider_result_count,
    claim.deleted_duplicate_count,
    claim.failure_reason,
    claim.created_at,
    claim.summarized_at,
    claim.deleting_at,
    claim.completed_at,
    claim.failed_at,
    claim.updated_at
FROM public.control_claim_account_inventory_compaction_v1(
    sqlc.arg(worker_token)::uuid,
    sqlc.arg(lease_seconds)::integer
) AS claim;

-- name: RenewAccountInventoryHistoryCompaction :one
SELECT renewed.compaction_run_id
FROM public.control_renew_account_inventory_compaction_v1(
    sqlc.arg(compaction_run_id)::uuid,
    sqlc.arg(fencing_token)::uuid,
    sqlc.arg(lease_seconds)::integer
) AS renewed;

-- name: ReconcileAccountInventoryHistoryCompactions :one
SELECT public.control_reconcile_account_inventory_compactions_v1(
    sqlc.arg(reconcile_limit)::integer
)::jsonb AS reconcile_result;

-- name: SummarizeAccountInventoryHistoryCompaction :one
SELECT public.control_summarize_account_inventory_compaction_v1(
    sqlc.arg(compaction_run_id)::uuid,
    sqlc.arg(fencing_token)::uuid
)::jsonb AS summarize_result;

-- name: DeleteAccountInventoryHistorySnapshotBatch :one
SELECT public.control_delete_account_inventory_snapshot_batch_v1(
    sqlc.arg(compaction_run_id)::uuid,
    sqlc.arg(fencing_token)::uuid,
    sqlc.arg(delete_limit)::integer
)::jsonb AS delete_result;

-- name: CompleteAccountInventoryHistoryCompaction :one
SELECT public.control_complete_account_inventory_compaction_v1(
    sqlc.arg(compaction_run_id)::uuid,
    sqlc.arg(fencing_token)::uuid,
    sqlc.arg(expected_checksum)::bytea
)::jsonb AS complete_result;

-- name: FailAccountInventoryHistoryCompaction :one
SELECT public.control_fail_account_inventory_compaction_v1(
    sqlc.arg(compaction_run_id)::uuid,
    sqlc.arg(fencing_token)::uuid,
    sqlc.arg(fixed_reason)::text
)::jsonb AS fail_result;

-- name: ClaimAccountInventoryHistoryDailyRollup :one
SELECT
    claim.rollup_run_id,
    claim.summary_date,
    claim.instance_id,
    claim.status,
    claim.claim_owner,
    claim.lease_expires_at,
    claim.fencing_token,
    claim.completed_fencing_token,
    claim.attempt_count,
    claim.expected_segment_count,
    claim.completed_segment_count,
    claim.checksum_version,
    claim.segment_checksum,
    claim.failure_reason,
    claim.created_at,
    claim.completed_at,
    claim.failed_at,
    claim.updated_at
FROM public.control_claim_account_inventory_daily_rollup_v1(
    sqlc.arg(worker_token)::uuid,
    sqlc.arg(lease_seconds)::integer
) AS claim;

-- name: RenewAccountInventoryHistoryDailyRollup :one
SELECT renewed.rollup_run_id
FROM public.control_renew_account_inventory_daily_rollup_v1(
    sqlc.arg(rollup_run_id)::uuid,
    sqlc.arg(fencing_token)::uuid,
    sqlc.arg(lease_seconds)::integer
) AS renewed;

-- name: ReconcileAccountInventoryHistoryDailyRollups :one
SELECT public.control_reconcile_account_inventory_daily_rollups_v1(
    sqlc.arg(reconcile_limit)::integer
)::jsonb AS reconcile_result;

-- name: FinalizeAccountInventoryHistoryDailyRollup :one
SELECT public.control_finalize_account_inventory_daily_rollup_v1(
    sqlc.arg(rollup_run_id)::uuid,
    sqlc.arg(fencing_token)::uuid
)::jsonb AS finalize_result;

-- name: FailAccountInventoryHistoryDailyRollup :one
SELECT public.control_fail_account_inventory_daily_rollup_v1(
    sqlc.arg(rollup_run_id)::uuid,
    sqlc.arg(fencing_token)::uuid,
    sqlc.arg(fixed_reason)::text
)::jsonb AS fail_result;

-- name: DeleteAccountInventoryPollRetention :one
SELECT public.control_delete_account_inventory_poll_retention_v1(
    sqlc.arg(delete_limit)::integer
)::jsonb AS retention_result;

-- name: DeleteAccountInventoryRollupRowRetention :one
SELECT public.control_delete_account_inventory_rollup_row_retention_v1(
    sqlc.arg(delete_limit)::integer
)::jsonb AS retention_result;

-- name: DeleteAccountInventoryRollupRunRetention :one
SELECT public.control_delete_account_inventory_rollup_run_retention_v1(
    sqlc.arg(delete_limit)::integer
)::jsonb AS retention_result;

-- name: DeleteAccountInventoryCompactionRunRetention :one
SELECT public.control_delete_account_inventory_compaction_run_retention_v1(
    sqlc.arg(delete_limit)::integer
)::jsonb AS retention_result;

-- name: ListAccountInventoryHistoryCoverageMetrics :many
SELECT
    metric.instance_id::uuid AS instance_id,
    metric.provider::text AS provider,
    metric.coverage_ratio::double precision AS coverage_ratio,
    metric.coverage_complete::boolean AS coverage_complete
FROM public.control_list_account_inventory_history_metrics_v1() AS metric
ORDER BY metric.instance_id, metric.provider;

-- name: GetAccountInventoryHistoryMetricsSnapshot :one
SELECT public.control_account_inventory_history_metrics_snapshot_v1()::jsonb AS metrics_snapshot;
