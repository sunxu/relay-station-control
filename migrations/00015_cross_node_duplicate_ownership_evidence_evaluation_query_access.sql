-- +goose Up

-- Phase 3 (openspec/changes/add-cross-node-duplicate-ownership) evidence
-- evaluation read model. The Phase 3 detect/refresh/degrade/add/remove/
-- resolve/reopen lifecycle transaction (implemented entirely in Go against
-- the tables and grants already created by migrations/00013 and 00014) needs,
-- for a given account_key and a candidate set of Node instance_ids, a
-- per-Node classification (owner_confirmed / absence_confirmed / degraded)
-- plus the retention-safe evidence source metadata
-- (source_provider/source_scheduled_at/source_completed_at/source_poll_run_id)
-- required by the cross_node_duplicate_occurrence_evidence CHECK constraints
-- (migrations/00013). This requires reading account_inventory and
-- account_inventory_provider_states, which relay_control_runtime has no
-- direct SELECT on (REVOKE ALL, migrations/00007 L841-843) -- unchanged by
-- this migration. As with migration 00014, access is exposed through one
-- narrow SECURITY DEFINER readonly function instead of widening table
-- grants. This migration creates no persistence truth: it is a pure
-- read-only query-access migration, symmetrical with 00014's rollback shape
-- (clean 15 -> 14 always possible; no history fail-closed guard needed).

-- +goose StatementBegin
CREATE FUNCTION public.control_evaluate_cross_node_duplicate_evidence_v1(
    target_account_key text,
    target_instance_ids uuid[],
    at_time timestamptz
) RETURNS TABLE (
    instance_id uuid,
    observation_kind text,
    source_provider text,
    source_scheduled_at timestamptz,
    source_completed_at timestamptz,
    source_poll_run_id uuid
)
LANGUAGE plpgsql
SECURITY DEFINER
STABLE
SET search_path = pg_catalog
AS $$
DECLARE
    provider_name text;
BEGIN
    -- Same canonical account_key validation as
    -- control_list_eligible_cross_node_owners_v1 (migrations/00014), copied
    -- verbatim rather than shared, matching the existing convention in this
    -- schema of literal CHECK/validation duplication over a shared function.
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
    IF at_time IS NULL THEN
        RAISE EXCEPTION 'control_evaluate_cross_node_duplicate_evidence_v1: at_time is required'
            USING ERRCODE = '22023';
    END IF;

    provider_name := split_part(target_account_key, ':', 1);

    RETURN QUERY
    WITH targets AS (
        SELECT DISTINCT unnest(target_instance_ids) AS instance_id
    ),
    joined AS (
        SELECT
            t.instance_id,
            account.lifecycle AS account_lifecycle,
            state.state AS provider_state,
            state.last_complete_at,
            state.current_scheduled_at,
            state.current_poll_run_id,
            state.health_scheduled_at,
            state.updated_at AS state_updated_at,
            (
                state.state = 'current'
                AND state.last_complete_at IS NOT NULL
                AND (at_time - state.last_complete_at) <= interval '15 minutes'
                AND (coalesce(account.lifecycle, '') = 'out_of_scope')
                    = (state.monitoring_status = 'out_of_scope')
            ) AS fresh_verifiable
        FROM targets t
        LEFT JOIN public.account_inventory_provider_states state
            ON state.instance_id = t.instance_id AND state.provider = provider_name
        LEFT JOIN public.account_inventory account
            ON account.instance_id = t.instance_id AND account.account_key = target_account_key
    )
    SELECT
        j.instance_id,
        CASE
            WHEN j.account_lifecycle = 'out_of_scope' THEN 'degraded'
            WHEN NOT j.fresh_verifiable THEN 'degraded'
            WHEN j.account_lifecycle IS NULL THEN 'absence_confirmed'
            WHEN j.account_lifecycle = 'present' THEN 'owner_confirmed'
            WHEN j.account_lifecycle IN ('suspected_missing', 'missing') THEN 'absence_confirmed'
            ELSE 'degraded'
        END AS observation_kind,
        provider_name AS source_provider,
        -- owner_confirmed/absence_confirmed cite the promoted Provider
        -- current state's current_scheduled_at (design.md §1B.4 revision);
        -- degraded cites health_scheduled_at, the slot that actually produced
        -- the unverifiable/stale reading, which can be materially more
        -- recent than a long-stale current_scheduled_at.
        CASE WHEN j.fresh_verifiable AND j.account_lifecycle IS DISTINCT FROM 'out_of_scope'
             THEN j.current_scheduled_at ELSE j.health_scheduled_at END AS source_scheduled_at,
        -- source_completed_at expresses complete/observed time (design.md
        -- §1B.4): for owner/absence it is the provider state's
        -- last_complete_at; for degraded there is no separate per-observation
        -- completed timestamp on account_inventory_provider_states, so
        -- updated_at (the row's own last-write time, always >= the
        -- health/degraded reading that produced it) is used as the
        -- completed/observed proxy.
        CASE WHEN j.fresh_verifiable AND j.account_lifecycle IS DISTINCT FROM 'out_of_scope'
             THEN j.last_complete_at ELSE j.state_updated_at END AS source_completed_at,
        -- source_poll_run_id is retention-safe metadata copied only for
        -- owner_confirmed/absence_confirmed (their identity is
        -- instance_id + source_scheduled_at + source_provider, and
        -- current_poll_run_id happens to be the promoted poll that produced
        -- current_scheduled_at). degraded's retention-safe identity is
        -- instance_id + health_scheduled_at + source_provider instead: this
        -- schema has no health_poll_run_id column that reliably corresponds
        -- to the health_scheduled_at slot, so current_poll_run_id must never
        -- be reused here -- it would misattribute a degraded reading to an
        -- unrelated (possibly much older) promoted poll run.
        CASE WHEN j.fresh_verifiable AND j.account_lifecycle IS DISTINCT FROM 'out_of_scope'
             THEN j.current_poll_run_id ELSE NULL END AS source_poll_run_id
    FROM joined j
    -- A Node with no account_inventory_provider_states row at all for this
    -- provider cannot be classified or cited with any source metadata; it is
    -- simply omitted (the caller must treat an instance_id absent from the
    -- result as "could not be evaluated this pass", contributing to
    -- evidence_state=degraded without a synthesized evidence row).
    WHERE j.provider_state IS NOT NULL
    ORDER BY j.instance_id;
END;
$$;
-- +goose StatementEnd

ALTER FUNCTION public.control_evaluate_cross_node_duplicate_evidence_v1(text, uuid[], timestamptz)
    OWNER TO relay_control_migrator;
REVOKE EXECUTE ON FUNCTION public.control_evaluate_cross_node_duplicate_evidence_v1(text, uuid[], timestamptz)
    FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_evaluate_cross_node_duplicate_evidence_v1(text, uuid[], timestamptz)
    TO relay_control_runtime;

-- +goose Down

REVOKE EXECUTE ON FUNCTION public.control_evaluate_cross_node_duplicate_evidence_v1(text, uuid[], timestamptz)
    FROM relay_control_runtime;
DROP FUNCTION public.control_evaluate_cross_node_duplicate_evidence_v1(text, uuid[], timestamptz);
