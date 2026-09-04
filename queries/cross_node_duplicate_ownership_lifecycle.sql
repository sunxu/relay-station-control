-- Phase 3 (add-cross-node-duplicate-ownership) lifecycle transaction DML.
-- These queries operate directly on the tables created by migration 00013;
-- relay_control_runtime already has the exact grants they need
-- (SELECT/INSERT on cross_node_duplicate_occurrences plus UPDATE on the
-- mutable projection columns, SELECT/INSERT/DELETE on
-- cross_node_duplicate_occurrence_nodes, SELECT/INSERT on
-- cross_node_duplicate_occurrence_evidence -- see migrations/00013 grants
-- section). No new migration/grant is required for this file.

-- name: SelectClockTimestamp :one
-- A single, explicit wall-clock read taken once per lifecycle evaluation
-- pass and threaded through every subsequent statement in the same
-- transaction (occurrence timestamps, evidence evaluation_at, evidence
-- source metadata via EvaluateCrossNodeDuplicateEvidence's at_time
-- parameter) so the whole pass is coherent against one instant in time.
SELECT clock_timestamp()::timestamptz AS now;

-- name: SelectActiveCrossNodeDuplicateOccurrenceForUpdate :one
-- Row-locks the existing ACTIVE occurrence (if any) for this semantic key
-- before re-evaluating source truth, per design.md §1B.8.
SELECT occurrence_id, environment_id, account_key, conflict_type, status,
    severity, first_seen_at, last_seen_at, resolved_at, evidence_state,
    last_fully_verified_at, latest_evaluation_id
FROM cross_node_duplicate_occurrences
WHERE environment_id = sqlc.arg(environment_id)
  AND account_key = sqlc.arg(account_key)
  AND conflict_type = 'cross_node_duplicate_ownership'
  AND status = 'ACTIVE'
FOR UPDATE;

-- name: InsertCrossNodeDuplicateOccurrence :one
-- Optimistic detect/reopen INSERT. ON CONFLICT DO NOTHING relies on the
-- partial unique index cross_node_duplicate_occurrence_active_uidx
-- (environment_id, account_key, conflict_type) WHERE status='ACTIVE'; a
-- conflict here never returns the existing row (PostgreSQL semantics), so
-- the caller must re-run SelectActiveCrossNodeDuplicateOccurrenceForUpdate
-- when zero rows come back.
INSERT INTO cross_node_duplicate_occurrences (
    environment_id, account_key, conflict_type, status, severity,
    first_seen_at, last_seen_at, evidence_state, last_fully_verified_at,
    latest_evaluation_id
) VALUES (
    sqlc.arg(environment_id), sqlc.arg(account_key), 'cross_node_duplicate_ownership',
    'ACTIVE', 'Critical', sqlc.arg(evaluation_at), sqlc.arg(evaluation_at),
    'complete', sqlc.arg(evaluation_at), NULL
)
ON CONFLICT (environment_id, account_key, conflict_type) WHERE status = 'ACTIVE' DO NOTHING
RETURNING occurrence_id;

-- name: ListCrossNodeDuplicateOccurrenceNodes :many
SELECT instance_id, first_confirmed_at
FROM cross_node_duplicate_occurrence_nodes
WHERE occurrence_id = sqlc.arg(occurrence_id)
ORDER BY instance_id;

-- name: InsertCrossNodeDuplicateOccurrenceNode :exec
INSERT INTO cross_node_duplicate_occurrence_nodes (
    occurrence_id, instance_id, first_confirmed_at
) VALUES (
    sqlc.arg(occurrence_id), sqlc.arg(instance_id), sqlc.arg(first_confirmed_at)
)
ON CONFLICT (occurrence_id, instance_id) DO NOTHING;

-- name: DeleteCrossNodeDuplicateOccurrenceNode :exec
DELETE FROM cross_node_duplicate_occurrence_nodes
WHERE occurrence_id = sqlc.arg(occurrence_id) AND instance_id = sqlc.arg(instance_id);

-- name: InsertCrossNodeDuplicateOccurrenceEvidence :exec
INSERT INTO cross_node_duplicate_occurrence_evidence (
    occurrence_id, instance_id, observation_kind, source_poll_run_id,
    source_provider, source_scheduled_at, source_completed_at,
    evaluation_id, evaluation_at
) VALUES (
    sqlc.arg(occurrence_id), sqlc.arg(instance_id), sqlc.arg(observation_kind),
    sqlc.narg(source_poll_run_id), sqlc.arg(source_provider),
    sqlc.arg(source_scheduled_at), sqlc.arg(source_completed_at),
    sqlc.arg(evaluation_id), sqlc.arg(evaluation_at)
);

-- name: RefreshCrossNodeDuplicateOccurrenceProjection :exec
-- Non-resolving pass (detect seed / refresh / add / remove / degrade): the
-- occurrence stays ACTIVE, only the mutable projection columns move.
--
-- has_evidence is false specifically for the zero-evidence degraded pass
-- (Phase 3 review item 3): when a reconcile pass appended no evidence rows
-- at all this evaluation (every retained Node was unclassifiable), the
-- caller passes has_evidence=false so latest_evaluation_id (and
-- last_fully_verified_at) keep pointing at the last evaluation that
-- actually has evidence backing it -- the 00013 latest_evaluation_id
-- consistency trigger requires an EXISTS evidence row for whatever
-- latest_evaluation_id is set to, and would reject an evaluation_id with
-- zero evidence rows this pass.
UPDATE cross_node_duplicate_occurrences
SET last_seen_at = sqlc.arg(evaluation_at),
    evidence_state = sqlc.arg(evidence_state),
    last_fully_verified_at = CASE
        WHEN NOT sqlc.arg(has_evidence)::boolean THEN last_fully_verified_at
        WHEN sqlc.arg(evidence_state)::text = 'complete' THEN sqlc.arg(evaluation_at)
        ELSE last_fully_verified_at
    END,
    latest_evaluation_id = CASE WHEN sqlc.arg(has_evidence)::boolean
        THEN sqlc.arg(evaluation_id) ELSE latest_evaluation_id END
WHERE occurrence_id = sqlc.arg(occurrence_id) AND status = 'ACTIVE';

-- name: ResolveCrossNodeDuplicateOccurrence :exec
-- ACTIVE -> RESOLVED transition. resolved_at is set in the same UPDATE as
-- the status change, satisfying the occurrence mutation-guard trigger
-- (migrations/00013).
UPDATE cross_node_duplicate_occurrences
SET status = 'RESOLVED',
    resolved_at = sqlc.arg(evaluation_at),
    last_seen_at = sqlc.arg(evaluation_at),
    evidence_state = 'complete',
    last_fully_verified_at = sqlc.arg(evaluation_at),
    latest_evaluation_id = sqlc.arg(evaluation_id)
WHERE occurrence_id = sqlc.arg(occurrence_id) AND status = 'ACTIVE';
