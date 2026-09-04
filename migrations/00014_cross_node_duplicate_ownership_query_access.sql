-- +goose Up

-- Phase 2 production access for Cross-node Duplicate Ownership
-- (openspec/changes/add-cross-node-duplicate-ownership). relay_control_runtime
-- has no direct SELECT on account_inventory / account_inventory_provider_states
-- (REVOKE ALL, migrations/00007_account_inventory_lifecycle_foundation.sql
-- L841-843) and this migration does not change that boundary. Instead, it
-- follows the same SECURITY DEFINER readonly-query pattern already used by
-- control_query_current_account_inventory_v1
-- (migrations/00008_account_inventory_readonly_query.sql): two narrow,
-- read-only functions expose exactly the Phase 1A frozen eligible-owner
-- predicate, owned by relay_control_migrator, with EXECUTE granted only to
-- relay_control_runtime. Neither function creates, modifies, or reads any
-- occurrence/evidence row, and neither reads Gateway Directory, Binding, or
-- scheduler/runtime state.

-- +goose StatementBegin
CREATE FUNCTION public.control_list_eligible_cross_node_owners_v1(
    target_account_key text
) RETURNS TABLE (instance_id uuid)
LANGUAGE plpgsql
SECURITY DEFINER
STABLE
SET search_path = pg_catalog
AS $$
DECLARE
    database_now timestamptz := clock_timestamp();
BEGIN
    IF target_account_key IS NULL
       OR octet_length(target_account_key) NOT BETWEEN 3 AND 385
       OR position(':' in target_account_key) <= 0
       OR octet_length(split_part(target_account_key, ':', 1)) NOT BETWEEN 1 AND 64
       OR split_part(target_account_key, ':', 1) !~ '^[a-z0-9][a-z0-9._-]*$'
       OR octet_length(substr(target_account_key, position(':' in target_account_key) + 1))
           NOT BETWEEN 1 AND 320
       OR substr(target_account_key, position(':' in target_account_key) + 1)
           <> lower(btrim(substr(target_account_key, position(':' in target_account_key) + 1)))
       OR substr(target_account_key, position(':' in target_account_key) + 1) ~ '[[:cntrl:]]' THEN
        RAISE EXCEPTION 'invalid cross-node duplicate ownership account_key'
            USING ERRCODE = '22023';
    END IF;

    RETURN QUERY
    SELECT account.instance_id
    FROM public.account_inventory AS account
    JOIN public.account_inventory_provider_states AS state
      ON state.instance_id = account.instance_id
     AND state.provider = account.provider
    WHERE account.account_key = target_account_key
      AND account.lifecycle = 'present'
      AND state.state = 'current'
      AND state.last_complete_at IS NOT NULL
      AND (database_now - state.last_complete_at) <= interval '15 minutes'
      AND (account.lifecycle = 'out_of_scope') = (state.monitoring_status = 'out_of_scope')
    ORDER BY account.instance_id;
END;
$$;
-- +goose StatementEnd

ALTER FUNCTION public.control_list_eligible_cross_node_owners_v1(text)
    OWNER TO relay_control_migrator;
REVOKE EXECUTE ON FUNCTION public.control_list_eligible_cross_node_owners_v1(text)
    FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_list_eligible_cross_node_owners_v1(text)
    TO relay_control_runtime;

-- +goose StatementBegin
CREATE FUNCTION public.control_list_cross_node_duplicate_candidates_v1()
RETURNS TABLE (account_key text, owner_instance_ids uuid[])
LANGUAGE plpgsql
SECURITY DEFINER
STABLE
SET search_path = pg_catalog
AS $$
DECLARE
    database_now timestamptz := clock_timestamp();
BEGIN
    RETURN QUERY
    SELECT account.account_key,
           array_agg(DISTINCT account.instance_id ORDER BY account.instance_id)::uuid[]
    FROM public.account_inventory AS account
    JOIN public.account_inventory_provider_states AS state
      ON state.instance_id = account.instance_id
     AND state.provider = account.provider
    WHERE account.lifecycle = 'present'
      AND state.state = 'current'
      AND state.last_complete_at IS NOT NULL
      AND (database_now - state.last_complete_at) <= interval '15 minutes'
      AND (account.lifecycle = 'out_of_scope') = (state.monitoring_status = 'out_of_scope')
    GROUP BY account.account_key
    HAVING count(DISTINCT account.instance_id) >= 2
    ORDER BY account.account_key;
END;
$$;
-- +goose StatementEnd

ALTER FUNCTION public.control_list_cross_node_duplicate_candidates_v1()
    OWNER TO relay_control_migrator;
REVOKE EXECUTE ON FUNCTION public.control_list_cross_node_duplicate_candidates_v1()
    FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_list_cross_node_duplicate_candidates_v1()
    TO relay_control_runtime;

-- +goose Down

REVOKE EXECUTE ON FUNCTION public.control_list_cross_node_duplicate_candidates_v1()
    FROM relay_control_runtime;
DROP FUNCTION public.control_list_cross_node_duplicate_candidates_v1();

REVOKE EXECUTE ON FUNCTION public.control_list_eligible_cross_node_owners_v1(text)
    FROM relay_control_runtime;
DROP FUNCTION public.control_list_eligible_cross_node_owners_v1(text);
