-- name: ListEligibleOwnersByAccountKey :many
-- Phase 2 (add-cross-node-duplicate-ownership): calls the SECURITY DEFINER
-- readonly function control_list_eligible_cross_node_owners_v1
-- (migrations/00014_cross_node_duplicate_ownership_query_access.sql), which
-- implements the exact eligible-owner predicate frozen in design.md Phase 1A.
-- relay_control_runtime has EXECUTE on the function but no direct SELECT on
-- account_inventory / account_inventory_provider_states.
SELECT *
FROM public.control_list_eligible_cross_node_owners_v1(sqlc.arg(account_key)::text);

-- name: ListCrossNodeDuplicateCandidates :many
-- Same eligible-owner predicate as ListEligibleOwnersByAccountKey, via the
-- SECURITY DEFINER function control_list_cross_node_duplicate_candidates_v1,
-- grouped by account_key and filtered to account_keys with >= 2 distinct
-- eligible owner Nodes. One row per account_key (no Node-pair fan-out).
-- sqlc cannot resolve individual columns of a multi-column TABLE-returning
-- function without a live database connection (same limitation the existing
-- QueryCurrentAccountInventoryV1 already works around); the composite row is
-- therefore wrapped in to_jsonb and decoded on the Go side, exactly like
-- queries/account_inventory_readonly_query.sql.
SELECT to_jsonb(candidate) AS candidate
FROM public.control_list_cross_node_duplicate_candidates_v1() AS candidate;
