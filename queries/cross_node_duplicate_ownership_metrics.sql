-- Phase 6c (add-cross-node-duplicate-ownership) minimal Prometheus metrics
-- source query. Only SELECTs from the Phase 1B occurrence table (already
-- granted to relay_control_runtime, see 00013) -- no new migration/grant.
-- Grouped only by (environment_id, status): conflict_type and severity are
-- both fixed single-valued strings for this capability
-- ("cross_node_duplicate_ownership" / "Critical"), so they are not
-- selected here; the metrics collector attaches them as constant labels.
-- Never groups by account_key/email/occurrence_id/instance_id (forbidden
-- high-cardinality Prometheus labels).

-- name: CountCrossNodeDuplicateOccurrencesByEnvironmentAndStatus :many
SELECT
    environment_id,
    status,
    count(*)::bigint AS occurrence_count
FROM cross_node_duplicate_occurrences
GROUP BY environment_id, status;
