-- name: GetLatestNodeDisableFence :one
SELECT command_id, committed_at
FROM public.asset_admin_command_receipts
WHERE command_kind = 'node.monitoring_disable'
  AND sanitized_result->>'instance_id' = sqlc.arg(instance_id)::text
ORDER BY committed_at DESC
LIMIT 1;

-- name: GetNodeMonitoringActivations :many
SELECT monitoring_activation_id, instance_id, effective_from, effective_to,
       reason, actor, created_at, end_reason, end_actor, end_recorded_at,
       cancelled_at, cancelled_by, cancel_reason, active_range
FROM public.relay_node_inventory_monitoring_activations
WHERE instance_id = sqlc.arg(instance_id)::uuid
ORDER BY effective_from, monitoring_activation_id;
