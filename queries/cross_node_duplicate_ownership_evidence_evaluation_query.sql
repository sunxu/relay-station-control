-- name: EvaluateCrossNodeDuplicateEvidenceAtDatabaseNow :many
-- Phase 4 (add-cross-node-duplicate-ownership) review item F/one: a single
-- PostgreSQL statement that both captures evaluationAt (clock_timestamp())
-- and reads cross-node duplicate evidence source truth through the
-- SECURITY DEFINER readonly function
-- control_evaluate_cross_node_duplicate_evidence_v1
-- (migrations/00015_cross_node_duplicate_ownership_evidence_evaluation_query_access.sql)
-- inside that same statement's own MVCC snapshot. This replaces the
-- previous two-statement SelectClockTimestamp + EvaluateCrossNodeDuplicateEvidence
-- sequence, which under READ COMMITTED could observe a promotion that
-- committed strictly *after* evaluationAt was captured (each statement in a
-- READ COMMITTED transaction gets its own fresh snapshot). The LEFT JOIN
-- LATERAL ... ON TRUE guarantees exactly one row per requested Node is
-- attempted, and evaluation_at is always returned even when the function
-- yields no evidence rows for a given instance_id (to_jsonb of an entirely
-- NULL outer-joined row is SQL NULL, decoded as a nullable evaluated
-- column). Same to_jsonb wrapping workaround as ListCrossNodeDuplicateCandidates:
-- sqlc cannot resolve individual columns of a multi-column TABLE-returning
-- function without a live database connection.
WITH evaluation AS (
    SELECT clock_timestamp()::timestamptz AS evaluation_at
)
SELECT
    evaluation.evaluation_at,
    to_jsonb(evaluated) AS evaluated
FROM evaluation
LEFT JOIN LATERAL public.control_evaluate_cross_node_duplicate_evidence_v1(
    sqlc.arg(account_key)::text,
    sqlc.arg(instance_ids)::uuid[],
    evaluation.evaluation_at
) AS evaluated ON TRUE;
