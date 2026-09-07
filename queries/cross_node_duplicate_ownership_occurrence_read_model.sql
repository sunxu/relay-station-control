-- Phase 5 (add-cross-node-duplicate-ownership) read-only occurrence read
-- model. These queries only SELECT from the Phase 1B tables
-- (cross_node_duplicate_occurrences / _nodes / _evidence,
-- migrations/00013_cross_node_duplicate_ownership_foundation.sql) which
-- relay_control_runtime already holds SELECT on -- no new migration/grant is
-- needed here (unlike Phase 2's Account Inventory query-access boundary).
-- account_key is returned as plaintext by design (frozen non-goal: no
-- masked/HMAC/fingerprint handling for this capability).

-- Historical involvement is intentionally separate from the current
-- instance_id filter below. EXISTS over immutable evidence preserves a
-- Node's history after absence removes its current membership; the lateral
-- affected-node projection remains the current set.
-- name: ListCrossNodeDuplicateOccurrencesHistoricallyInvolvingNode :many
SELECT
    occurrence.occurrence_id,
    occurrence.environment_id,
    occurrence.account_key,
    occurrence.conflict_type,
    occurrence.status,
    occurrence.severity,
    occurrence.first_seen_at,
    occurrence.last_seen_at,
    occurrence.resolved_at,
    occurrence.evidence_state,
    occurrence.last_fully_verified_at,
    occurrence.latest_evaluation_id,
    COALESCE(affected.instance_ids, ARRAY[]::uuid[])::uuid[] AS affected_node_ids
FROM cross_node_duplicate_occurrences AS occurrence
LEFT JOIN LATERAL (
    SELECT array_agg(node.instance_id ORDER BY node.instance_id) AS instance_ids
    FROM cross_node_duplicate_occurrence_nodes AS node
    WHERE node.occurrence_id = occurrence.occurrence_id
) AS affected ON true
WHERE occurrence.status = COALESCE(sqlc.narg(status)::text, occurrence.status)
  AND EXISTS (
      SELECT 1
      FROM cross_node_duplicate_occurrence_evidence AS evidence
      WHERE evidence.occurrence_id = occurrence.occurrence_id
        AND evidence.instance_id = sqlc.arg(target_node_id)::uuid
  )
  AND (
      sqlc.narg(after_last_seen_at)::timestamptz IS NULL
      OR (occurrence.last_seen_at, occurrence.occurrence_id) < (
          sqlc.narg(after_last_seen_at)::timestamptz,
          sqlc.narg(after_occurrence_id)::uuid
      )
  )
ORDER BY occurrence.last_seen_at DESC, occurrence.occurrence_id DESC
LIMIT sqlc.arg(page_size);

-- name: ListCrossNodeDuplicateOccurrences :many
-- Bounded, keyset-paginated list ordered by (last_seen_at DESC,
-- occurrence_id DESC) -- most recently active first. affected_nodes is
-- aggregated via a LATERAL subquery so the list response never needs a
-- second round-trip per occurrence. instance_id filtering uses EXISTS
-- against cross_node_duplicate_occurrence_nodes rather than a JOIN, so an
-- occurrence with N affected Nodes is never duplicated in the page.
SELECT
    occurrence.occurrence_id,
    occurrence.environment_id,
    occurrence.account_key,
    occurrence.conflict_type,
    occurrence.status,
    occurrence.severity,
    occurrence.first_seen_at,
    occurrence.last_seen_at,
    occurrence.resolved_at,
    occurrence.evidence_state,
    occurrence.last_fully_verified_at,
    occurrence.latest_evaluation_id,
    COALESCE(affected.instance_ids, ARRAY[]::uuid[])::uuid[] AS affected_node_ids
FROM cross_node_duplicate_occurrences AS occurrence
LEFT JOIN LATERAL (
    SELECT array_agg(node.instance_id ORDER BY node.instance_id) AS instance_ids
    FROM cross_node_duplicate_occurrence_nodes AS node
    WHERE node.occurrence_id = occurrence.occurrence_id
) AS affected ON true
WHERE (sqlc.narg(status)::text IS NULL OR occurrence.status = sqlc.narg(status))
  AND (sqlc.narg(account_key)::text IS NULL OR occurrence.account_key = sqlc.narg(account_key))
  AND (
      sqlc.narg(instance_id)::uuid IS NULL
      OR EXISTS (
          SELECT 1 FROM cross_node_duplicate_occurrence_nodes AS filter_node
          WHERE filter_node.occurrence_id = occurrence.occurrence_id
            AND filter_node.instance_id = sqlc.narg(instance_id)::uuid
      )
  )
  AND (
      sqlc.narg(after_last_seen_at)::timestamptz IS NULL
      OR (occurrence.last_seen_at, occurrence.occurrence_id) < (
          sqlc.narg(after_last_seen_at)::timestamptz,
          sqlc.narg(after_occurrence_id)::uuid
      )
  )
ORDER BY occurrence.last_seen_at DESC, occurrence.occurrence_id DESC
LIMIT sqlc.arg(page_size);

-- name: GetCrossNodeDuplicateOccurrence :one
SELECT
    occurrence.occurrence_id,
    occurrence.environment_id,
    occurrence.account_key,
    occurrence.conflict_type,
    occurrence.status,
    occurrence.severity,
    occurrence.first_seen_at,
    occurrence.last_seen_at,
    occurrence.resolved_at,
    occurrence.evidence_state,
    occurrence.last_fully_verified_at,
    occurrence.latest_evaluation_id,
    COALESCE(affected.instance_ids, ARRAY[]::uuid[])::uuid[] AS affected_node_ids
FROM cross_node_duplicate_occurrences AS occurrence
LEFT JOIN LATERAL (
    SELECT array_agg(node.instance_id ORDER BY node.instance_id) AS instance_ids
    FROM cross_node_duplicate_occurrence_nodes AS node
    WHERE node.occurrence_id = occurrence.occurrence_id
) AS affected ON true
WHERE occurrence.occurrence_id = sqlc.arg(occurrence_id)::uuid;

-- name: ListCrossNodeDuplicateOccurrenceEvidence :many
-- Bounded, keyset-paginated evidence history for one occurrence, ordered
-- (recorded_at DESC, observation_id DESC) -- most recent observation first.
-- This is a separate, independently-paginated query so a list-occurrence
-- call never has to return unbounded evidence history inline.
SELECT
    evidence.observation_id,
    evidence.instance_id,
    evidence.observation_kind,
    evidence.source_provider,
    evidence.source_scheduled_at,
    evidence.source_completed_at,
    evidence.source_poll_run_id,
    evidence.evaluation_id,
    evidence.evaluation_at,
    evidence.recorded_at
FROM cross_node_duplicate_occurrence_evidence AS evidence
WHERE evidence.occurrence_id = sqlc.arg(occurrence_id)::uuid
  AND (
      sqlc.narg(after_recorded_at)::timestamptz IS NULL
      OR (evidence.recorded_at, evidence.observation_id) < (
          sqlc.narg(after_recorded_at)::timestamptz,
          sqlc.narg(after_observation_id)::uuid
      )
  )
ORDER BY evidence.recorded_at DESC, evidence.observation_id DESC
LIMIT sqlc.arg(page_size);
