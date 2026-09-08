-- Phase 3 (add-cross-node-duplicate-ownership) lifecycle transaction DML.
-- These queries operate directly on the tables created by migration 00013;
-- relay_control_runtime already has the exact grants they need
-- (SELECT/INSERT on cross_node_duplicate_occurrences plus UPDATE on the
-- mutable projection columns, SELECT/INSERT/DELETE on
-- cross_node_duplicate_occurrence_nodes, SELECT/INSERT on
-- cross_node_duplicate_occurrence_evidence -- see migrations/00013 grants
-- section). No new migration/grant is required for this file.

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

-- name: ListCrossNodeDuplicateOccurrenceLatestCheckpoint :many
-- Reads the evidence rows referenced by latest_evaluation_id. The lifecycle
-- caller already holds the occurrence row lock, so this is the persisted
-- checkpoint used for the material-change comparison.
SELECT instance_id, observation_kind, source_poll_run_id,
       source_provider, source_scheduled_at, source_completed_at
FROM cross_node_duplicate_occurrence_evidence
WHERE occurrence_id = sqlc.arg(occurrence_id)
  AND evaluation_id = sqlc.arg(evaluation_id)
ORDER BY instance_id;

-- name: RefreshCrossNodeDuplicateOccurrenceProjection :exec
-- Non-resolving pass (detect seed / refresh / add / remove / degrade): the
-- occurrence stays ACTIVE, only the mutable projection columns move.
--
-- has_checkpoint means this pass appended real material evidence rows.
-- Unchanged or zero-evidence evaluations retain latest_evaluation_id.
-- has_verified_evidence is independent: complete authoritative no-op passes
-- still advance last_fully_verified_at; zero-evidence degraded passes do not.
UPDATE cross_node_duplicate_occurrences
SET last_seen_at = sqlc.arg(evaluation_at),
    evidence_state = sqlc.arg(evidence_state),
    last_fully_verified_at = CASE
        WHEN NOT sqlc.arg(has_verified_evidence)::boolean THEN last_fully_verified_at
        WHEN sqlc.arg(evidence_state)::text = 'complete' THEN sqlc.arg(evaluation_at)
        ELSE last_fully_verified_at
    END,
    latest_evaluation_id = CASE WHEN sqlc.arg(has_checkpoint)::boolean
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

-- name: ListActiveCrossNodeDuplicateOccurrenceKeys :many
-- Phase 4 (add-cross-node-duplicate-ownership) restart/backfill
-- reconciliation (design.md §5.1): every (environment_id, account_key) that
-- currently has an ACTIVE occurrence, so a reconciliation pass can also
-- resolve/degrade occurrences whose account_key has dropped out of the
-- current duplicate candidate set (e.g. down to a single owner, or all the
-- way to zero eligible owners) since the last detect/refresh pass.
-- relay_control_runtime already has SELECT on this table (migrations/00013);
-- no new grant is required.
SELECT environment_id, account_key
FROM cross_node_duplicate_occurrences
WHERE status = 'ACTIVE';
