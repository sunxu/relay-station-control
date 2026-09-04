-- name: EvaluateCrossNodeDuplicateEvidence :many
-- Phase 3 (add-cross-node-duplicate-ownership): calls the SECURITY DEFINER
-- readonly function control_evaluate_cross_node_duplicate_evidence_v1
-- (migrations/00015_cross_node_duplicate_ownership_evidence_evaluation_query_access.sql).
-- Same to_jsonb wrapping workaround as ListCrossNodeDuplicateCandidates:
-- sqlc cannot resolve individual columns of a multi-column TABLE-returning
-- function without a live database connection.
SELECT to_jsonb(evaluated) AS evaluated
FROM public.control_evaluate_cross_node_duplicate_evidence_v1(
    sqlc.arg(account_key)::text,
    sqlc.arg(instance_ids)::uuid[],
    sqlc.arg(at_time)::timestamptz
) AS evaluated;
