-- +goose Up

-- Phase 4 (openspec/changes/add-cross-node-duplicate-ownership) review item
-- F: control_evaluate_cross_node_duplicate_evidence_v1 (migrations/00015)
-- takes at_time as a plain parameter, not a transaction-wide consistent
-- snapshot bound -- under READ COMMITTED, the statement that calls this
-- function gets its own fresh snapshot at execution time, which can be
-- strictly after the SelectClockTimestamp statement that produced at_time.
-- If a promotion or health refresh commits in that (tiny) window, this
-- function's original body could classify that Node using data with
-- last_complete_at/health_scheduled_at *after* at_time: the freshness check
-- `(at_time - state.last_complete_at) <= interval '15 minutes'` is true even
-- when last_complete_at > at_time (the difference is simply negative),
-- which would let a promotion from strictly after the evaluation instant be
-- treated as owner_confirmed/absence_confirmed evidence "as of" at_time,
-- and would persist cross_node_duplicate_occurrence_evidence.source_completed_at
-- > evaluation_at (proven directly by
-- TestCrossNodeDuplicateOwnershipEvidenceEvaluationSnapshotRace, which fails
-- against the pre-00016 function body and passes after this migration).
--
-- Fix: any provider_state row whose last_complete_at or health_scheduled_at
-- is strictly after at_time is excluded from the result entirely -- treated
-- exactly like "no account_inventory_provider_states row visible this
-- pass" (the caller already handles a missing instance_id as
-- "could not be evaluated", contributing to evidence_state=degraded without
-- a synthesized evidence row, per migrations/00015's existing contract).
-- This closes the race for both branches: once last_complete_at <= at_time
-- is guaranteed for every returned row, the existing
-- `(at_time - state.last_complete_at) <= interval '15 minutes'` freshness
-- check is correct (the subtraction can no longer be negative), and the
-- degraded branch can never cite a health_scheduled_at from the future
-- either. Phase 4 Final Review item two (defense-in-depth after the
-- single-statement snapshot fix in EvaluateCrossNodeDuplicateEvidenceAtDatabaseNow
-- closes the root cause): every other source-metadata timestamp this
-- function can possibly cite is independently bounded by at_time too --
-- state.updated_at (the degraded branch's source_completed_at proxy) and
-- account.updated_at (a policy-only lifecycle transition can advance
-- account.updated_at without moving last_complete_at). No new table,
-- column, or persistence truth is introduced; this is a pure function-body
-- correction, symmetrical with 00015's own rollback shape (clean 16 -> 15
-- always possible).

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_evaluate_cross_node_duplicate_evidence_v1(
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
            account.updated_at AS account_updated_at,
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
        CASE WHEN j.fresh_verifiable AND j.account_lifecycle IS DISTINCT FROM 'out_of_scope'
             THEN j.current_scheduled_at ELSE j.health_scheduled_at END AS source_scheduled_at,
        CASE WHEN j.fresh_verifiable AND j.account_lifecycle IS DISTINCT FROM 'out_of_scope'
             THEN j.last_complete_at ELSE j.state_updated_at END AS source_completed_at,
        CASE WHEN j.fresh_verifiable AND j.account_lifecycle IS DISTINCT FROM 'out_of_scope'
             THEN j.current_poll_run_id ELSE NULL END AS source_poll_run_id
    FROM joined j
    -- A Node with no account_inventory_provider_states row at all for this
    -- provider cannot be classified or cited with any source metadata; it is
    -- simply omitted. Defense-in-depth (Phase 4 Final Review item two): even
    -- though the single-statement snapshot fix (see
    -- EvaluateCrossNodeDuplicateEvidenceAtDatabaseNow) now removes the root
    -- cause, every source-metadata timestamp this function could possibly
    -- cite (last_complete_at, health_scheduled_at, state_updated_at -- the
    -- degraded branch's source_completed_at proxy -- and account.updated_at,
    -- which a policy-only transition can advance without moving
    -- last_complete_at) must independently be <= at_time, or the row is
    -- omitted entirely -- never cited as if it existed at at_time.
    WHERE j.provider_state IS NOT NULL
      AND j.last_complete_at <= at_time
      AND j.health_scheduled_at <= at_time
      AND j.state_updated_at <= at_time
      AND (j.account_updated_at IS NULL OR j.account_updated_at <= at_time)
    ORDER BY j.instance_id;
END;
$$;
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_evaluate_cross_node_duplicate_evidence_v1(
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
        CASE WHEN j.fresh_verifiable AND j.account_lifecycle IS DISTINCT FROM 'out_of_scope'
             THEN j.current_scheduled_at ELSE j.health_scheduled_at END AS source_scheduled_at,
        CASE WHEN j.fresh_verifiable AND j.account_lifecycle IS DISTINCT FROM 'out_of_scope'
             THEN j.last_complete_at ELSE j.state_updated_at END AS source_completed_at,
        CASE WHEN j.fresh_verifiable AND j.account_lifecycle IS DISTINCT FROM 'out_of_scope'
             THEN j.current_poll_run_id ELSE NULL END AS source_poll_run_id
    FROM joined j
    WHERE j.provider_state IS NOT NULL
    ORDER BY j.instance_id;
END;
$$;
-- +goose StatementEnd
