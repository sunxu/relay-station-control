-- +goose Up

ALTER TABLE account_inventory_poll_runs
    ADD COLUMN promotion_skipped_reason text,
    ADD CONSTRAINT account_inventory_poll_runs_promotion_reason_fixed CHECK (
        promotion_skipped_reason IS NULL OR promotion_skipped_reason = 'policy_changed'
    ),
    ADD CONSTRAINT account_inventory_poll_runs_promotion_terminal CHECK (
        promotion_skipped_reason IS NULL OR status = 'finalized'
    ),
    ADD CONSTRAINT account_inventory_poll_runs_id_instance_unique
        UNIQUE (poll_run_id, instance_id);

ALTER TABLE account_inventory_poll_provider_results
    ADD COLUMN promotion_applied boolean NOT NULL DEFAULT false,
    ADD COLUMN promotion_skipped_reason text;

ALTER TABLE account_inventory_poll_provider_results
    ADD CONSTRAINT account_inventory_poll_provider_promotion_reason_fixed CHECK (
        promotion_skipped_reason IS NULL OR promotion_skipped_reason IN (
            'policy_changed', 'transport_failed', 'contract_invalid',
            'disk_fallback', 'provider_identity_incomplete',
            'provider_duplicate', 'stale_poll'
        )
    ),
    ADD CONSTRAINT account_inventory_poll_provider_promotion_shape CHECK (
        NOT promotion_applied OR promotion_skipped_reason IS NULL
    );

CREATE TABLE account_inventory_snapshot_items (
    poll_run_id uuid NOT NULL,
    instance_id uuid NOT NULL,
    provider text NOT NULL,
    account_key text NOT NULL,
    normalized_email text NOT NULL,
    basic_status text NOT NULL,
    success_count bigint NOT NULL,
    failed_count bigint NOT NULL,
    recent_request_count bigint NOT NULL,
    last_refresh_at timestamptz,
    next_retry_at timestamptz,
    source_updated_at timestamptz,
    observed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (poll_run_id, instance_id, account_key),
    CONSTRAINT account_inventory_snapshot_provider_valid CHECK (
        octet_length(provider) BETWEEN 1 AND 64
        AND provider ~ '^[a-z0-9][a-z0-9._-]*$'
    ),
    CONSTRAINT account_inventory_snapshot_email_valid CHECK (
        octet_length(normalized_email) BETWEEN 1 AND 320
        AND normalized_email = lower(btrim(normalized_email))
        AND normalized_email !~ '[[:cntrl:]]'
    ),
    CONSTRAINT account_inventory_snapshot_key_valid CHECK (
        octet_length(account_key) BETWEEN 3 AND 385
        AND account_key = provider || ':' || normalized_email
    ),
    CONSTRAINT account_inventory_snapshot_status_fixed CHECK (
        basic_status IN ('disabled', 'unavailable', 'error', 'active', 'unknown')
    ),
    CONSTRAINT account_inventory_snapshot_counts_bounded CHECK (
        success_count BETWEEN 0 AND 9223372036854775807
        AND failed_count BETWEEN 0 AND 9223372036854775807
        AND recent_request_count BETWEEN 0 AND 1000
    ),
    CONSTRAINT account_inventory_snapshot_source_times_bounded CHECK (
        (last_refresh_at IS NULL OR last_refresh_at BETWEEN
            '0001-01-01 00:00:01+00'::timestamptz AND '9999-12-31 23:59:59+00'::timestamptz)
        AND (next_retry_at IS NULL OR next_retry_at BETWEEN
            '0001-01-01 00:00:01+00'::timestamptz AND '9999-12-31 23:59:59+00'::timestamptz)
        AND (source_updated_at IS NULL OR source_updated_at BETWEEN
            '0001-01-01 00:00:01+00'::timestamptz AND '9999-12-31 23:59:59+00'::timestamptz)
    ),
    FOREIGN KEY (poll_run_id, instance_id)
        REFERENCES account_inventory_poll_runs (poll_run_id, instance_id)
        ON UPDATE RESTRICT ON DELETE CASCADE,
    FOREIGN KEY (poll_run_id, provider)
        REFERENCES account_inventory_poll_provider_results (poll_run_id, provider)
        ON UPDATE RESTRICT ON DELETE CASCADE
);
CREATE INDEX account_inventory_snapshot_items_current_idx
    ON account_inventory_snapshot_items (instance_id, provider, account_key, poll_run_id);

CREATE TABLE account_inventory_poll_duplicates (
    poll_run_id uuid NOT NULL,
    instance_id uuid NOT NULL,
    provider text NOT NULL,
    account_key text NOT NULL,
    occurrence_count integer NOT NULL,
    observed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (poll_run_id, instance_id, account_key),
    CONSTRAINT account_inventory_duplicate_provider_valid CHECK (
        octet_length(provider) BETWEEN 1 AND 64
        AND provider ~ '^[a-z0-9][a-z0-9._-]*$'
    ),
    CONSTRAINT account_inventory_duplicate_key_valid CHECK (
        octet_length(account_key) BETWEEN 3 AND 385
        AND account_key LIKE provider || ':%'
        AND substring(account_key FROM octet_length(provider) + 2)
            = lower(btrim(substring(account_key FROM octet_length(provider) + 2)))
        AND substring(account_key FROM octet_length(provider) + 2) !~ '[[:cntrl:]]'
        AND octet_length(substring(account_key FROM octet_length(provider) + 2)) BETWEEN 1 AND 320
    ),
    CONSTRAINT account_inventory_duplicate_occurrences_bounded CHECK (
        occurrence_count BETWEEN 2 AND 1000
    ),
    FOREIGN KEY (poll_run_id, instance_id)
        REFERENCES account_inventory_poll_runs (poll_run_id, instance_id)
        ON UPDATE RESTRICT ON DELETE CASCADE,
    FOREIGN KEY (poll_run_id, provider)
        REFERENCES account_inventory_poll_provider_results (poll_run_id, provider)
        ON UPDATE RESTRICT ON DELETE CASCADE
);

CREATE TABLE account_inventory_provider_states (
    instance_id uuid NOT NULL,
    provider text NOT NULL,
    current_poll_run_id uuid,
    current_scheduled_at timestamptz NOT NULL,
    last_complete_at timestamptz NOT NULL,
    source_observed_at timestamptz NOT NULL,
    source_node_version text NOT NULL,
    source_node_commit text NOT NULL,
    state text NOT NULL DEFAULT 'current',
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (instance_id, provider),
    CONSTRAINT account_inventory_provider_state_provider_valid CHECK (
        octet_length(provider) BETWEEN 1 AND 64
        AND provider ~ '^[a-z0-9][a-z0-9._-]*$'
    ),
    CONSTRAINT account_inventory_provider_state_fixed CHECK (state = 'current'),
    CONSTRAINT account_inventory_provider_state_slot_aligned CHECK (
        extract(epoch FROM current_scheduled_at)::bigint % 300 = 0
    ),
    CONSTRAINT account_inventory_provider_state_times_ordered CHECK (
        last_complete_at = source_observed_at
        AND updated_at >= source_observed_at
    ),
    CONSTRAINT account_inventory_provider_state_version_allowlist CHECK (
        source_node_version = 'unknown'
        OR (octet_length(source_node_version) BETWEEN 1 AND 64
            AND source_node_version ~ '^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$')
    ),
    CONSTRAINT account_inventory_provider_state_commit_allowlist CHECK (
        source_node_commit = 'unknown'
        OR (octet_length(source_node_commit) BETWEEN 7 AND 64
            AND source_node_commit ~ '^[0-9a-f]{7,64}$')
    ),
    FOREIGN KEY (instance_id) REFERENCES relay_node_assets(instance_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    FOREIGN KEY (current_poll_run_id, instance_id)
        REFERENCES account_inventory_poll_runs(poll_run_id, instance_id)
        ON UPDATE RESTRICT ON DELETE SET NULL (current_poll_run_id)
);

-- +goose StatementBegin
CREATE FUNCTION public.control_reject_account_inventory_snapshot_mutation() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
    RAISE EXCEPTION 'account inventory snapshot evidence is immutable'
        USING ERRCODE = '42501';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER account_inventory_snapshot_items_immutable
BEFORE UPDATE OR DELETE ON account_inventory_snapshot_items
FOR EACH ROW EXECUTE FUNCTION public.control_reject_account_inventory_snapshot_mutation();
CREATE TRIGGER account_inventory_snapshot_items_truncate_immutable
BEFORE TRUNCATE ON account_inventory_snapshot_items
FOR EACH STATEMENT EXECUTE FUNCTION public.control_reject_account_inventory_snapshot_mutation();
CREATE TRIGGER account_inventory_poll_duplicates_immutable
BEFORE UPDATE OR DELETE ON account_inventory_poll_duplicates
FOR EACH ROW EXECUTE FUNCTION public.control_reject_account_inventory_snapshot_mutation();
CREATE TRIGGER account_inventory_poll_duplicates_truncate_immutable
BEFORE TRUNCATE ON account_inventory_poll_duplicates
FOR EACH STATEMENT EXECUTE FUNCTION public.control_reject_account_inventory_snapshot_mutation();

-- +goose StatementBegin
CREATE FUNCTION public.control_validate_account_inventory_snapshot_insert() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
DECLARE parent_status text;
DECLARE provider_applied boolean;
DECLARE provider_duplicate_count integer;
BEGIN
    SELECT run.status, result.promotion_applied, result.duplicate_identity_count
    INTO parent_status, provider_applied, provider_duplicate_count
    FROM public.account_inventory_poll_runs AS run
    JOIN public.account_inventory_poll_provider_results AS result
      ON result.poll_run_id = run.poll_run_id
     AND result.provider = NEW.provider
    WHERE run.poll_run_id = NEW.poll_run_id
      AND run.instance_id = NEW.instance_id
    FOR KEY SHARE OF run, result;
    IF parent_status IS DISTINCT FROM 'running'
       OR (TG_TABLE_NAME = 'account_inventory_snapshot_items' AND NOT provider_applied)
       OR (TG_TABLE_NAME = 'account_inventory_poll_duplicates' AND provider_duplicate_count < 1) THEN
        RAISE EXCEPTION 'account inventory snapshot evidence requires controlled running finalize'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER account_inventory_snapshot_items_insert_guard
BEFORE INSERT ON account_inventory_snapshot_items
FOR EACH ROW EXECUTE FUNCTION public.control_validate_account_inventory_snapshot_insert();
CREATE TRIGGER account_inventory_poll_duplicates_insert_guard
BEFORE INSERT ON account_inventory_poll_duplicates
FOR EACH ROW EXECUTE FUNCTION public.control_validate_account_inventory_snapshot_insert();

-- +goose StatementBegin
CREATE FUNCTION public.control_protect_account_inventory_provider_state() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NOT EXISTS (
            SELECT 1 FROM public.account_inventory_poll_provider_results AS result
            WHERE result.poll_run_id = NEW.current_poll_run_id
              AND result.provider = NEW.provider
              AND result.promotion_applied
        ) THEN
            RAISE EXCEPTION 'account inventory provider state requires applied promotion'
                USING ERRCODE = '23514';
        END IF;
        RETURN NEW;
    END IF;
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'account inventory provider state cannot be deleted'
            USING ERRCODE = '42501';
    END IF;
    IF NEW.instance_id <> OLD.instance_id OR NEW.provider <> OLD.provider THEN
        RAISE EXCEPTION 'account inventory provider state identity is immutable'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.current_poll_run_id IS NULL
       AND OLD.current_poll_run_id IS NOT NULL
       AND NEW.current_scheduled_at = OLD.current_scheduled_at
       AND NEW.last_complete_at = OLD.last_complete_at
       AND NEW.source_observed_at = OLD.source_observed_at
       AND NEW.source_node_version = OLD.source_node_version
       AND NEW.source_node_commit = OLD.source_node_commit
       AND NEW.state = OLD.state
       AND NEW.updated_at = OLD.updated_at THEN
        RETURN NEW;
    END IF;
    IF NEW.current_scheduled_at <= OLD.current_scheduled_at THEN
        RAISE EXCEPTION 'account inventory provider pointer must advance'
            USING ERRCODE = '23514';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM public.account_inventory_poll_provider_results AS result
        WHERE result.poll_run_id = NEW.current_poll_run_id
          AND result.provider = NEW.provider
          AND result.promotion_applied
    ) THEN
        RAISE EXCEPTION 'account inventory provider state requires applied promotion'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER account_inventory_provider_states_guard
BEFORE INSERT OR UPDATE OR DELETE ON account_inventory_provider_states
FOR EACH ROW EXECUTE FUNCTION public.control_protect_account_inventory_provider_state();
CREATE TRIGGER account_inventory_provider_states_truncate_immutable
BEFORE TRUNCATE ON account_inventory_provider_states
FOR EACH STATEMENT EXECUTE FUNCTION public.control_reject_account_inventory_snapshot_mutation();

-- +goose StatementBegin
CREATE FUNCTION public.control_validate_account_inventory_promotion() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
DECLARE inconsistent_count bigint;
BEGIN
    IF NEW.status <> 'finalized' THEN
        RETURN NEW;
    END IF;
    SELECT count(*) INTO inconsistent_count
    FROM public.account_inventory_poll_provider_results AS result
    WHERE result.poll_run_id = NEW.poll_run_id
      AND (
        (NOT result.promotion_applied AND result.promotion_skipped_reason IS NULL)
        OR
        (result.promotion_applied AND (
            NOT NEW.contract_valid OR NEW.inventory_mode <> 'runtime'
            OR NOT NEW.node_identity_complete
            OR NOT result.snapshot_complete
            OR NOT EXISTS (
                SELECT 1 FROM public.account_inventory_provider_states AS state
                WHERE state.instance_id = NEW.instance_id
                  AND state.provider = result.provider
                  AND state.current_poll_run_id = NEW.poll_run_id
            )
        ))
        OR (result.promotion_skipped_reason = 'policy_changed'
            AND NEW.promotion_skipped_reason IS DISTINCT FROM 'policy_changed')
        OR (NEW.promotion_skipped_reason = 'policy_changed'
            AND result.promotion_skipped_reason IS DISTINCT FROM 'policy_changed')
        OR (NOT result.promotion_applied AND EXISTS (
            SELECT 1 FROM public.account_inventory_snapshot_items AS item
            WHERE item.poll_run_id = NEW.poll_run_id
              AND item.provider = result.provider
        ))
      );
    IF inconsistent_count <> 0 THEN
        RAISE EXCEPTION 'account inventory promotion evidence is inconsistent'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER account_inventory_poll_runs_promotion_guard
BEFORE UPDATE ON account_inventory_poll_runs
FOR EACH ROW EXECUTE FUNCTION public.control_validate_account_inventory_promotion();

REVOKE EXECUTE ON FUNCTION public.control_finalize_account_inventory_poll_run(
    uuid, uuid, boolean, boolean, boolean, text, boolean, boolean, boolean,
    text, text, integer, integer, integer, integer, integer, text, text, jsonb
) FROM relay_control_runtime;

-- +goose StatementBegin
CREATE FUNCTION public.control_finalize_account_inventory_poll_run(
    target_poll_run_id uuid,
    expected_fencing_token uuid,
    result_transport_success boolean,
    result_response_shape_valid boolean,
    result_contract_valid boolean,
    result_inventory_mode text,
    result_node_identity_complete boolean,
    result_snapshot_complete boolean,
    result_degraded boolean,
    result_result text,
    result_reason text,
    result_source_record_count integer,
    result_identifiable_record_count integer,
    result_unidentified_record_count integer,
    result_unsupported_provider_count integer,
    result_out_of_scope_provider_count integer,
    result_node_version text,
    result_node_commit text,
    provider_results jsonb,
    snapshot_items jsonb,
    duplicate_evidence jsonb
) RETURNS SETOF public.account_inventory_poll_runs
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    database_now timestamptz := clock_timestamp();
    observation_time timestamptz;
    run_record public.account_inventory_poll_runs%ROWTYPE;
    expected_providers text[];
    supplied_providers text[];
    current_policy_version uuid;
    policy_matches boolean;
    provider_item jsonb;
    snapshot_item jsonb;
    duplicate_item jsonb;
    provider_name text;
    provider_identifiable integer;
    provider_missing integer;
    provider_duplicate integer;
    provider_identity_complete boolean;
    provider_snapshot_complete boolean;
    provider_degraded boolean;
    provider_reason text;
    provider_promotion_applied boolean;
    provider_skip_reason text;
    account_key_value text;
    email_value text;
    status_value text;
    success_value bigint;
    failed_value bigint;
    recent_value bigint;
    last_refresh_value bigint;
    next_retry_value bigint;
    updated_at_value bigint;
    occurrence_value integer;
    supplied_item_count bigint;
    supplied_duplicate_count bigint;
    supplied_occurrences bigint;
    supplied_total bigint;
    supplied_distinct bigint;
    summed_identifiable bigint := 0;
    summed_missing bigint := 0;
    all_snapshot_complete boolean := true;
BEGIN
    SELECT * INTO run_record
    FROM public.account_inventory_poll_runs AS run
    WHERE run.poll_run_id = target_poll_run_id
      AND run.status = 'running'
      AND run.lease_fencing_token = expected_fencing_token
      AND run.lease_expires_at > database_now
    FOR UPDATE;
    IF NOT FOUND THEN
        RETURN;
    END IF;

    SELECT policy.active_providers INTO expected_providers
    FROM public.provider_inventory_policy_versions AS policy
    WHERE policy.policy_version_id = run_record.provider_policy_version
      AND policy.node_type = run_record.node_type
      AND policy.driver_contract_version = run_record.driver_contract_version;

    IF jsonb_typeof(provider_results) IS DISTINCT FROM 'array'
       OR jsonb_array_length(provider_results) <> cardinality(expected_providers)
       OR jsonb_typeof(snapshot_items) IS DISTINCT FROM 'array'
       OR jsonb_array_length(snapshot_items) > 1000
       OR jsonb_typeof(duplicate_evidence) IS DISTINCT FROM 'array'
       OR jsonb_array_length(duplicate_evidence) > 1000 THEN
        RAISE EXCEPTION 'account inventory snapshot finalize set is invalid'
            USING ERRCODE = '23514';
    END IF;
    SELECT array_agg(value->>'provider' ORDER BY value->>'provider')
    INTO supplied_providers
    FROM jsonb_array_elements(provider_results) AS item(value);
    IF supplied_providers IS DISTINCT FROM expected_providers THEN
        RAISE EXCEPTION 'account inventory provider result set does not match pinned policy'
            USING ERRCODE = '23514';
    END IF;

    SELECT count(*), count(DISTINCT (value->>'provider', value->>'account_key'))
    INTO supplied_total, supplied_distinct
    FROM jsonb_array_elements(snapshot_items) AS item(value);
    IF supplied_total <> supplied_distinct THEN
        RAISE EXCEPTION 'duplicate account inventory snapshot candidate'
            USING ERRCODE = '23514';
    END IF;
    SELECT count(*), count(DISTINCT (value->>'provider', value->>'account_key'))
    INTO supplied_total, supplied_distinct
    FROM jsonb_array_elements(duplicate_evidence) AS item(value);
    IF supplied_total <> supplied_distinct OR EXISTS (
        SELECT 1
        FROM jsonb_array_elements(snapshot_items) AS candidate(value)
        JOIN jsonb_array_elements(duplicate_evidence) AS duplicate(value)
          ON duplicate.value->>'provider' = candidate.value->>'provider'
         AND duplicate.value->>'account_key' = candidate.value->>'account_key'
    ) THEN
        RAISE EXCEPTION 'duplicate account inventory evidence key'
            USING ERRCODE = '23514';
    END IF;

    FOR duplicate_item IN SELECT value FROM jsonb_array_elements(duplicate_evidence) AS item(value)
    LOOP
        IF jsonb_typeof(duplicate_item) IS DISTINCT FROM 'object'
           OR NOT (duplicate_item ?& ARRAY['provider','account_key','occurrence_count'])
           OR duplicate_item - ARRAY['provider','account_key','occurrence_count'] <> '{}'::jsonb
           OR jsonb_typeof(duplicate_item->'provider') IS DISTINCT FROM 'string'
           OR jsonb_typeof(duplicate_item->'account_key') IS DISTINCT FROM 'string'
           OR jsonb_typeof(duplicate_item->'occurrence_count') IS DISTINCT FROM 'number' THEN
            RAISE EXCEPTION 'invalid account inventory duplicate evidence shape'
                USING ERRCODE = '23514';
        END IF;
        BEGIN
            provider_name := duplicate_item->>'provider';
            account_key_value := duplicate_item->>'account_key';
            occurrence_value := (duplicate_item->>'occurrence_count')::integer;
        EXCEPTION WHEN invalid_text_representation OR numeric_value_out_of_range THEN
            RAISE EXCEPTION 'invalid account inventory duplicate evidence value'
                USING ERRCODE = '23514';
        END;
        IF provider_name <> ALL(expected_providers)
           OR account_key_value IS NULL
           OR octet_length(account_key_value) NOT BETWEEN 3 AND 385
           OR account_key_value NOT LIKE provider_name || ':%'
           OR substring(account_key_value FROM octet_length(provider_name) + 2)
                IS DISTINCT FROM lower(btrim(substring(
                    account_key_value FROM octet_length(provider_name) + 2
                )))
           OR substring(account_key_value FROM octet_length(provider_name) + 2) ~ '[[:cntrl:]]'
           OR octet_length(substring(account_key_value FROM octet_length(provider_name) + 2))
                NOT BETWEEN 1 AND 320
           OR occurrence_value NOT BETWEEN 2 AND 1000 THEN
            RAISE EXCEPTION 'invalid account inventory duplicate evidence value'
                USING ERRCODE = '23514';
        END IF;
    END LOOP;

    PERFORM 1
    FROM public.provider_inventory_policy_bindings AS binding
    WHERE binding.node_type = run_record.node_type
      AND binding.driver_contract_version = run_record.driver_contract_version
    FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'account inventory policy binding is unavailable'
            USING ERRCODE = '23514';
    END IF;
    database_now := clock_timestamp();
    IF run_record.lease_expires_at <= database_now THEN
        RAISE EXCEPTION 'account inventory poll lease expired during finalize'
            USING ERRCODE = 'P0002';
    END IF;
    observation_time := database_now;
    SELECT activation.policy_version_id INTO current_policy_version
    FROM public.provider_inventory_policy_activations AS activation
    WHERE activation.node_type = run_record.node_type
      AND activation.driver_contract_version = run_record.driver_contract_version
      AND activation.active_range @> database_now;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'account inventory active policy is unavailable'
            USING ERRCODE = '23514';
    END IF;
    policy_matches := current_policy_version = run_record.provider_policy_version;

    FOR provider_item IN SELECT value FROM jsonb_array_elements(provider_results) AS item(value)
    LOOP
        IF jsonb_typeof(provider_item) IS DISTINCT FROM 'object'
           OR NOT (provider_item ?& ARRAY[
               'provider','identifiable_count','missing_identity_count',
               'duplicate_identity_count','identity_complete','snapshot_complete',
               'degraded','reason'
           ])
           OR provider_item - ARRAY[
               'provider','identifiable_count','missing_identity_count',
               'duplicate_identity_count','identity_complete','snapshot_complete',
               'degraded','reason'
           ] <> '{}'::jsonb
           OR jsonb_typeof(provider_item->'provider') IS DISTINCT FROM 'string'
           OR jsonb_typeof(provider_item->'identifiable_count') IS DISTINCT FROM 'number'
           OR jsonb_typeof(provider_item->'missing_identity_count') IS DISTINCT FROM 'number'
           OR jsonb_typeof(provider_item->'duplicate_identity_count') IS DISTINCT FROM 'number'
           OR jsonb_typeof(provider_item->'identity_complete') IS DISTINCT FROM 'boolean'
           OR jsonb_typeof(provider_item->'snapshot_complete') IS DISTINCT FROM 'boolean'
           OR jsonb_typeof(provider_item->'degraded') IS DISTINCT FROM 'boolean'
           OR jsonb_typeof(provider_item->'reason') IS DISTINCT FROM 'string' THEN
            RAISE EXCEPTION 'invalid account inventory provider result shape'
                USING ERRCODE = '23514';
        END IF;
        BEGIN
            provider_name := provider_item->>'provider';
            provider_identifiable := (provider_item->>'identifiable_count')::integer;
            provider_missing := (provider_item->>'missing_identity_count')::integer;
            provider_duplicate := (provider_item->>'duplicate_identity_count')::integer;
            provider_identity_complete := (provider_item->>'identity_complete')::boolean;
            provider_snapshot_complete := (provider_item->>'snapshot_complete')::boolean;
            provider_degraded := (provider_item->>'degraded')::boolean;
            provider_reason := provider_item->>'reason';
        EXCEPTION WHEN invalid_text_representation OR numeric_value_out_of_range THEN
            RAISE EXCEPTION 'invalid account inventory provider result value'
                USING ERRCODE = '23514';
        END;
        IF NOT result_node_identity_complete AND provider_snapshot_complete THEN
            RAISE EXCEPTION 'account inventory provider completeness contradicts node identity'
                USING ERRCODE = '23514';
        END IF;
        summed_identifiable := summed_identifiable + provider_identifiable;
        summed_missing := summed_missing + provider_missing;
        all_snapshot_complete := all_snapshot_complete AND provider_snapshot_complete;

        SELECT count(*), coalesce(sum((value->>'occurrence_count')::integer), 0)
        INTO supplied_duplicate_count, supplied_occurrences
        FROM jsonb_array_elements(duplicate_evidence) AS duplicate(value)
        WHERE value->>'provider' = provider_name;
        SELECT count(*) INTO supplied_item_count
        FROM jsonb_array_elements(snapshot_items) AS candidate(value)
        WHERE value->>'provider' = provider_name;
        IF supplied_duplicate_count <> provider_duplicate
           OR supplied_item_count + supplied_occurrences <> provider_identifiable
           OR (provider_snapshot_complete AND supplied_duplicate_count <> 0) THEN
            RAISE EXCEPTION 'account inventory provider account counts are inconsistent'
                USING ERRCODE = '23514';
        END IF;

        IF NOT policy_matches THEN
            provider_promotion_applied := false;
            provider_skip_reason := 'policy_changed';
        ELSIF NOT result_transport_success THEN
            provider_promotion_applied := false;
            provider_skip_reason := 'transport_failed';
        ELSIF NOT result_contract_valid THEN
            provider_promotion_applied := false;
            provider_skip_reason := 'contract_invalid';
        ELSIF result_inventory_mode = 'disk_fallback' THEN
            provider_promotion_applied := false;
            provider_skip_reason := 'disk_fallback';
        ELSIF NOT result_node_identity_complete THEN
            provider_promotion_applied := false;
            provider_skip_reason := 'provider_identity_incomplete';
        ELSIF provider_duplicate > 0 THEN
            provider_promotion_applied := false;
            provider_skip_reason := 'provider_duplicate';
        ELSIF NOT provider_identity_complete OR NOT provider_snapshot_complete THEN
            provider_promotion_applied := false;
            provider_skip_reason := 'provider_identity_incomplete';
        ELSIF EXISTS (
            SELECT 1 FROM public.account_inventory_provider_states AS state
            WHERE state.instance_id = run_record.instance_id
              AND state.provider = provider_name
              AND state.current_scheduled_at >= run_record.scheduled_at
        ) THEN
            provider_promotion_applied := false;
            provider_skip_reason := 'stale_poll';
        ELSE
            provider_promotion_applied := true;
            provider_skip_reason := NULL;
        END IF;

        INSERT INTO public.account_inventory_poll_provider_results (
            poll_run_id, provider, identifiable_count, missing_identity_count,
            duplicate_identity_count, identity_complete, snapshot_complete,
            degraded, reason, promotion_applied, promotion_skipped_reason
        ) VALUES (
            target_poll_run_id, provider_name, provider_identifiable, provider_missing,
            provider_duplicate, provider_identity_complete, provider_snapshot_complete,
            provider_degraded, provider_reason, provider_promotion_applied, provider_skip_reason
        );

    END LOOP;

    FOR snapshot_item IN SELECT value FROM jsonb_array_elements(snapshot_items) AS item(value)
    LOOP
        IF jsonb_typeof(snapshot_item) IS DISTINCT FROM 'object'
           OR NOT (snapshot_item ?& ARRAY[
               'provider','account_key','email','basic_status','success_count',
               'failed_count','recent_request_count','last_refresh_unix',
               'next_retry_unix','updated_at_unix'
           ])
           OR snapshot_item - ARRAY[
               'provider','account_key','email','basic_status','success_count',
               'failed_count','recent_request_count','last_refresh_unix',
               'next_retry_unix','updated_at_unix'
           ] <> '{}'::jsonb
           OR jsonb_typeof(snapshot_item->'provider') IS DISTINCT FROM 'string'
           OR jsonb_typeof(snapshot_item->'account_key') IS DISTINCT FROM 'string'
           OR jsonb_typeof(snapshot_item->'email') IS DISTINCT FROM 'string'
           OR jsonb_typeof(snapshot_item->'basic_status') IS DISTINCT FROM 'string'
           OR jsonb_typeof(snapshot_item->'success_count') IS DISTINCT FROM 'number'
           OR jsonb_typeof(snapshot_item->'failed_count') IS DISTINCT FROM 'number'
           OR jsonb_typeof(snapshot_item->'recent_request_count') IS DISTINCT FROM 'number'
           OR jsonb_typeof(snapshot_item->'last_refresh_unix') NOT IN ('number','null')
           OR jsonb_typeof(snapshot_item->'next_retry_unix') NOT IN ('number','null')
           OR jsonb_typeof(snapshot_item->'updated_at_unix') NOT IN ('number','null') THEN
            RAISE EXCEPTION 'invalid account inventory snapshot item shape'
                USING ERRCODE = '23514';
        END IF;
        BEGIN
            provider_name := snapshot_item->>'provider';
            account_key_value := snapshot_item->>'account_key';
            email_value := snapshot_item->>'email';
            status_value := snapshot_item->>'basic_status';
            success_value := (snapshot_item->>'success_count')::bigint;
            failed_value := (snapshot_item->>'failed_count')::bigint;
            recent_value := (snapshot_item->>'recent_request_count')::bigint;
            last_refresh_value := CASE WHEN snapshot_item->'last_refresh_unix' = 'null'::jsonb
                THEN NULL ELSE (snapshot_item->>'last_refresh_unix')::bigint END;
            next_retry_value := CASE WHEN snapshot_item->'next_retry_unix' = 'null'::jsonb
                THEN NULL ELSE (snapshot_item->>'next_retry_unix')::bigint END;
            updated_at_value := CASE WHEN snapshot_item->'updated_at_unix' = 'null'::jsonb
                THEN NULL ELSE (snapshot_item->>'updated_at_unix')::bigint END;
        EXCEPTION WHEN invalid_text_representation OR numeric_value_out_of_range THEN
            RAISE EXCEPTION 'invalid account inventory snapshot item value'
                USING ERRCODE = '23514';
        END;
        IF provider_name <> ALL(expected_providers)
           OR account_key_value IS DISTINCT FROM provider_name || ':' || email_value
           OR email_value IS NULL OR octet_length(email_value) NOT BETWEEN 1 AND 320
           OR email_value IS DISTINCT FROM lower(btrim(email_value))
           OR email_value ~ '[[:cntrl:]]'
           OR status_value NOT IN ('disabled','unavailable','error','active','unknown')
           OR success_value < 0 OR failed_value < 0 OR recent_value NOT BETWEEN 0 AND 1000
           OR (last_refresh_value IS NOT NULL AND last_refresh_value NOT BETWEEN 1 AND 253402300799)
           OR (next_retry_value IS NOT NULL AND next_retry_value NOT BETWEEN 1 AND 253402300799)
           OR (updated_at_value IS NOT NULL AND updated_at_value NOT BETWEEN 1 AND 253402300799) THEN
            RAISE EXCEPTION 'invalid account inventory snapshot item value'
                USING ERRCODE = '23514';
        END IF;
        IF EXISTS (
            SELECT 1 FROM public.account_inventory_poll_provider_results AS result
            WHERE result.poll_run_id = target_poll_run_id
              AND result.provider = provider_name
              AND result.promotion_applied
        ) THEN
            INSERT INTO public.account_inventory_snapshot_items (
                poll_run_id, instance_id, provider, account_key, normalized_email,
                basic_status, success_count, failed_count, recent_request_count,
                last_refresh_at, next_retry_at, source_updated_at, observed_at
            ) VALUES (
                target_poll_run_id, run_record.instance_id, provider_name,
                account_key_value, email_value, status_value, success_value,
                failed_value, recent_value,
                CASE WHEN last_refresh_value IS NULL THEN NULL ELSE to_timestamp(last_refresh_value) END,
                CASE WHEN next_retry_value IS NULL THEN NULL ELSE to_timestamp(next_retry_value) END,
                CASE WHEN updated_at_value IS NULL THEN NULL ELSE to_timestamp(updated_at_value) END,
                observation_time
            );
        END IF;
    END LOOP;

    FOR duplicate_item IN SELECT value FROM jsonb_array_elements(duplicate_evidence) AS item(value)
    LOOP
        IF jsonb_typeof(duplicate_item) IS DISTINCT FROM 'object'
           OR NOT (duplicate_item ?& ARRAY['provider','account_key','occurrence_count'])
           OR duplicate_item - ARRAY['provider','account_key','occurrence_count'] <> '{}'::jsonb
           OR jsonb_typeof(duplicate_item->'provider') IS DISTINCT FROM 'string'
           OR jsonb_typeof(duplicate_item->'account_key') IS DISTINCT FROM 'string'
           OR jsonb_typeof(duplicate_item->'occurrence_count') IS DISTINCT FROM 'number' THEN
            RAISE EXCEPTION 'invalid account inventory duplicate evidence shape'
                USING ERRCODE = '23514';
        END IF;
        BEGIN
            provider_name := duplicate_item->>'provider';
            account_key_value := duplicate_item->>'account_key';
            occurrence_value := (duplicate_item->>'occurrence_count')::integer;
        EXCEPTION WHEN invalid_text_representation OR numeric_value_out_of_range THEN
            RAISE EXCEPTION 'invalid account inventory duplicate evidence value'
                USING ERRCODE = '23514';
        END;
        IF provider_name <> ALL(expected_providers)
           OR account_key_value IS NULL
           OR octet_length(account_key_value) NOT BETWEEN 3 AND 385
           OR account_key_value NOT LIKE provider_name || ':%'
           OR substring(account_key_value FROM octet_length(provider_name) + 2)
                IS DISTINCT FROM lower(btrim(substring(
                    account_key_value FROM octet_length(provider_name) + 2
                )))
           OR substring(account_key_value FROM octet_length(provider_name) + 2) ~ '[[:cntrl:]]'
           OR octet_length(substring(account_key_value FROM octet_length(provider_name) + 2))
                NOT BETWEEN 1 AND 320
           OR occurrence_value NOT BETWEEN 2 AND 1000 THEN
            RAISE EXCEPTION 'invalid account inventory duplicate evidence value'
                USING ERRCODE = '23514';
        END IF;
        INSERT INTO public.account_inventory_poll_duplicates (
            poll_run_id, instance_id, provider, account_key,
            occurrence_count, observed_at
        ) VALUES (
            target_poll_run_id, run_record.instance_id, provider_name,
            account_key_value, occurrence_value, observation_time
        );
    END LOOP;

    IF summed_identifiable <> result_identifiable_record_count
       OR summed_missing > result_unidentified_record_count
       OR result_snapshot_complete <> (result_node_identity_complete AND all_snapshot_complete)
       OR result_source_record_count <> result_identifiable_record_count
            + result_unidentified_record_count
            + result_unsupported_provider_count
            + result_out_of_scope_provider_count THEN
        RAISE EXCEPTION 'account inventory aggregate result is inconsistent'
            USING ERRCODE = '23514';
    END IF;

    database_now := clock_timestamp();
    IF run_record.lease_expires_at <= database_now THEN
        RAISE EXCEPTION 'account inventory poll lease expired during finalize'
            USING ERRCODE = 'P0002';
    END IF;
    INSERT INTO public.account_inventory_provider_states (
        instance_id, provider, current_poll_run_id, current_scheduled_at,
        last_complete_at, source_observed_at, source_node_version,
        source_node_commit, state, updated_at
    )
    SELECT run_record.instance_id, result.provider, target_poll_run_id,
           run_record.scheduled_at, observation_time, observation_time,
           result_node_version, result_node_commit, 'current', database_now
    FROM public.account_inventory_poll_provider_results AS result
    WHERE result.poll_run_id = target_poll_run_id
      AND result.promotion_applied
    ON CONFLICT (instance_id, provider) DO UPDATE
    SET current_poll_run_id = EXCLUDED.current_poll_run_id,
        current_scheduled_at = EXCLUDED.current_scheduled_at,
        last_complete_at = EXCLUDED.last_complete_at,
        source_observed_at = EXCLUDED.source_observed_at,
        source_node_version = EXCLUDED.source_node_version,
        source_node_commit = EXCLUDED.source_node_commit,
        state = EXCLUDED.state,
        updated_at = EXCLUDED.updated_at;

    database_now := clock_timestamp();
    IF run_record.lease_expires_at <= database_now THEN
        RAISE EXCEPTION 'account inventory poll lease expired during finalize'
            USING ERRCODE = 'P0002';
    END IF;

    RETURN QUERY
    UPDATE public.account_inventory_poll_runs AS run
    SET status = 'finalized', finalized_at = database_now, observed_at = observation_time,
        lease_expires_at = NULL, lease_fencing_token = NULL,
        transport_success = result_transport_success,
        response_shape_valid = result_response_shape_valid,
        contract_valid = result_contract_valid,
        inventory_mode = result_inventory_mode,
        node_identity_complete = result_node_identity_complete,
        snapshot_complete = result_snapshot_complete,
        degraded = result_degraded,
        result = result_result, reason = result_reason,
        source_record_count = result_source_record_count,
        identifiable_record_count = result_identifiable_record_count,
        unidentified_record_count = result_unidentified_record_count,
        unsupported_provider_count = result_unsupported_provider_count,
        out_of_scope_provider_count = result_out_of_scope_provider_count,
        node_version = result_node_version, node_commit = result_node_commit,
        promotion_skipped_reason = CASE WHEN policy_matches THEN NULL ELSE 'policy_changed' END
    WHERE run.poll_run_id = target_poll_run_id
      AND run.status = 'running'
      AND run.lease_fencing_token = expected_fencing_token
    RETURNING run.*;
END;
$$;
-- +goose StatementEnd

ALTER FUNCTION public.control_finalize_account_inventory_poll_run(
    uuid, uuid, boolean, boolean, boolean, text, boolean, boolean, boolean,
    text, text, integer, integer, integer, integer, integer, text, text,
    jsonb, jsonb, jsonb
) OWNER TO relay_control_migrator;

-- +goose StatementBegin
CREATE FUNCTION public.control_list_current_account_inventory_snapshot(
    target_instance_id uuid,
    target_provider text,
    after_account_key text,
    page_limit integer
) RETURNS TABLE (
    account_key text,
    normalized_email text,
    basic_status text,
    success_count bigint,
    failed_count bigint,
    recent_request_count bigint,
    last_refresh_at timestamptz,
    next_retry_at timestamptz,
    source_updated_at timestamptz,
    observed_at timestamptz
)
LANGUAGE plpgsql
SECURITY DEFINER
STABLE
SET search_path = pg_catalog
AS $$
BEGIN
    IF target_instance_id IS NULL
       OR target_provider IS NULL
       OR octet_length(target_provider) NOT BETWEEN 1 AND 64
       OR target_provider !~ '^[a-z0-9][a-z0-9._-]*$'
       OR after_account_key IS NULL
       OR octet_length(after_account_key) > 385
       OR page_limit < 1 OR page_limit > 500 THEN
        RAISE EXCEPTION 'invalid account inventory snapshot page'
            USING ERRCODE = '22023';
    END IF;
    RETURN QUERY
    SELECT item.account_key, item.normalized_email, item.basic_status,
           item.success_count, item.failed_count, item.recent_request_count,
           item.last_refresh_at, item.next_retry_at, item.source_updated_at,
           item.observed_at
    FROM public.account_inventory_provider_states AS state
    JOIN public.account_inventory_snapshot_items AS item
      ON item.poll_run_id = state.current_poll_run_id
     AND item.instance_id = state.instance_id
     AND item.provider = state.provider
    WHERE state.instance_id = target_instance_id
      AND state.provider = target_provider
      AND item.account_key > after_account_key
    ORDER BY item.account_key
    LIMIT page_limit;
END;
$$;
-- +goose StatementEnd

ALTER FUNCTION public.control_list_current_account_inventory_snapshot(uuid, text, text, integer)
    OWNER TO relay_control_migrator;

REVOKE ALL ON TABLE account_inventory_snapshot_items,
    account_inventory_poll_duplicates,
    account_inventory_provider_states
FROM PUBLIC, relay_control_runtime;
REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON account_inventory_poll_runs,
    account_inventory_poll_provider_results
FROM relay_control_runtime;

GRANT SELECT (promotion_skipped_reason)
ON account_inventory_poll_runs TO relay_control_runtime;

REVOKE EXECUTE ON FUNCTION public.control_reject_account_inventory_snapshot_mutation() FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_validate_account_inventory_snapshot_insert() FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_protect_account_inventory_provider_state() FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_validate_account_inventory_promotion() FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_finalize_account_inventory_poll_run(
    uuid, uuid, boolean, boolean, boolean, text, boolean, boolean, boolean,
    text, text, integer, integer, integer, integer, integer, text, text,
    jsonb, jsonb, jsonb
) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_list_current_account_inventory_snapshot(
    uuid, text, text, integer
) FROM PUBLIC;

GRANT EXECUTE ON FUNCTION public.control_finalize_account_inventory_poll_run(
    uuid, uuid, boolean, boolean, boolean, text, boolean, boolean, boolean,
    text, text, integer, integer, integer, integer, integer, text, text,
    jsonb, jsonb, jsonb
) TO relay_control_runtime;
GRANT EXECUTE ON FUNCTION public.control_list_current_account_inventory_snapshot(
    uuid, text, text, integer
) TO relay_control_runtime;

-- +goose Down
-- +goose StatementBegin
DO $$
DECLARE evidence_count bigint;
DECLARE promotion_count bigint;
BEGIN
    LOCK TABLE account_inventory_poll_runs,
        account_inventory_poll_provider_results,
        account_inventory_snapshot_items,
        account_inventory_poll_duplicates,
        account_inventory_provider_states IN ACCESS EXCLUSIVE MODE NOWAIT;
    SELECT (SELECT count(*) FROM account_inventory_snapshot_items)
         + (SELECT count(*) FROM account_inventory_poll_duplicates)
         + (SELECT count(*) FROM account_inventory_provider_states)
    INTO evidence_count;
    SELECT (SELECT count(*) FROM account_inventory_poll_runs
            WHERE promotion_skipped_reason IS NOT NULL)
         + (SELECT count(*) FROM account_inventory_poll_provider_results
            WHERE promotion_applied OR promotion_skipped_reason IS NOT NULL)
    INTO promotion_count;
    IF evidence_count <> 0 OR promotion_count <> 0 THEN
        RAISE EXCEPTION 'account inventory snapshot migration down requires empty evidence and promotion markers'
            USING ERRCODE = '55000';
    END IF;
END;
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION public.control_list_current_account_inventory_snapshot(
    uuid, text, text, integer
) FROM relay_control_runtime;
REVOKE EXECUTE ON FUNCTION public.control_finalize_account_inventory_poll_run(
    uuid, uuid, boolean, boolean, boolean, text, boolean, boolean, boolean,
    text, text, integer, integer, integer, integer, integer, text, text,
    jsonb, jsonb, jsonb
) FROM relay_control_runtime;
DROP FUNCTION public.control_list_current_account_inventory_snapshot(uuid, text, text, integer);
DROP FUNCTION public.control_finalize_account_inventory_poll_run(
    uuid, uuid, boolean, boolean, boolean, text, boolean, boolean, boolean,
    text, text, integer, integer, integer, integer, integer, text, text,
    jsonb, jsonb, jsonb
);

GRANT EXECUTE ON FUNCTION public.control_finalize_account_inventory_poll_run(
    uuid, uuid, boolean, boolean, boolean, text, boolean, boolean, boolean,
    text, text, integer, integer, integer, integer, integer, text, text, jsonb
) TO relay_control_runtime;

DROP TRIGGER account_inventory_poll_runs_promotion_guard ON account_inventory_poll_runs;
DROP FUNCTION public.control_validate_account_inventory_promotion();
DROP TRIGGER account_inventory_provider_states_truncate_immutable ON account_inventory_provider_states;
DROP TRIGGER account_inventory_provider_states_guard ON account_inventory_provider_states;
DROP FUNCTION public.control_protect_account_inventory_provider_state();
DROP TRIGGER account_inventory_poll_duplicates_truncate_immutable ON account_inventory_poll_duplicates;
DROP TRIGGER account_inventory_poll_duplicates_immutable ON account_inventory_poll_duplicates;
DROP TRIGGER account_inventory_poll_duplicates_insert_guard ON account_inventory_poll_duplicates;
DROP TRIGGER account_inventory_snapshot_items_truncate_immutable ON account_inventory_snapshot_items;
DROP TRIGGER account_inventory_snapshot_items_immutable ON account_inventory_snapshot_items;
DROP TRIGGER account_inventory_snapshot_items_insert_guard ON account_inventory_snapshot_items;
DROP FUNCTION public.control_validate_account_inventory_snapshot_insert();
DROP FUNCTION public.control_reject_account_inventory_snapshot_mutation();

DROP TABLE account_inventory_provider_states;
DROP TABLE account_inventory_poll_duplicates;
DROP TABLE account_inventory_snapshot_items;

ALTER TABLE account_inventory_poll_provider_results
    DROP CONSTRAINT account_inventory_poll_provider_promotion_shape,
    DROP CONSTRAINT account_inventory_poll_provider_promotion_reason_fixed,
    DROP COLUMN promotion_skipped_reason,
    DROP COLUMN promotion_applied;
ALTER TABLE account_inventory_poll_runs
    DROP CONSTRAINT account_inventory_poll_runs_id_instance_unique,
    DROP CONSTRAINT account_inventory_poll_runs_promotion_terminal,
    DROP CONSTRAINT account_inventory_poll_runs_promotion_reason_fixed,
    DROP COLUMN promotion_skipped_reason;
