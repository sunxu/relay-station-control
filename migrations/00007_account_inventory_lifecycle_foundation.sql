-- +goose Up

-- A legacy scheduled activation advances the binding immediately even though
-- the old policy remains effective until its future boundary. Lifecycle scope
-- cannot follow that boundary without a second durable executor, so refuse the
-- upgrade instead of installing a schema whose monitoring state can diverge.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM public.provider_inventory_policy_bindings AS binding
        JOIN public.provider_inventory_policy_activations AS future
          ON future.node_type = binding.node_type
         AND future.driver_contract_version = binding.driver_contract_version
         AND future.policy_version_id = binding.policy_version_id
         AND future.effective_from > CURRENT_TIMESTAMP
        LEFT JOIN public.provider_inventory_policy_activations AS current_activation
          ON current_activation.node_type = binding.node_type
         AND current_activation.driver_contract_version = binding.driver_contract_version
         AND current_activation.active_range @> CURRENT_TIMESTAMP
        WHERE current_activation.policy_version_id IS DISTINCT FROM binding.policy_version_id
    ) THEN
        RAISE EXCEPTION 'account inventory lifecycle migration requires immediately effective policy bindings'
            USING ERRCODE = '55000';
    END IF;
END;
$$;
-- +goose StatementEnd

-- The v3 helper treated PostgreSQL's canonical empty array (array_ndims=NULL)
-- as malformed, even though the policy contract permits either side to be
-- empty when the combined Provider set is nonempty. Keep all element
-- validation and canonical sorting while accepting that one representation.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_normalize_provider_set(provider_values text[]) RETURNS text[]
LANGUAGE plpgsql
IMMUTABLE STRICT
SET search_path = pg_catalog
AS $$
DECLARE
    normalized text[];
BEGIN
    IF cardinality(provider_values) = 0 THEN
        RETURN ARRAY[]::text[];
    END IF;
    IF array_ndims(provider_values) IS DISTINCT FROM 1
       OR EXISTS (
           SELECT 1 FROM unnest(provider_values) AS provider(value)
           WHERE value IS NULL
              OR octet_length(value) NOT BETWEEN 1 AND 64
              OR value !~ '^[a-z0-9][a-z0-9._-]*$'
       ) THEN
        RAISE EXCEPTION 'provider set is invalid' USING ERRCODE = '22023';
    END IF;
    SELECT COALESCE(array_agg(DISTINCT value ORDER BY value), ARRAY[]::text[])
    INTO normalized
    FROM unnest(provider_values) AS provider(value);
    RETURN normalized;
END;
$$;
-- +goose StatementEnd

ALTER TABLE account_inventory_provider_states
    ADD COLUMN monitoring_status text NOT NULL DEFAULT 'active',
    ADD COLUMN out_of_scope_since timestamptz,
    ADD CONSTRAINT account_inventory_provider_monitoring_status_fixed CHECK (
        monitoring_status IN ('active', 'out_of_scope')
    ),
    ADD CONSTRAINT account_inventory_provider_monitoring_shape CHECK (
        (monitoring_status = 'active' AND out_of_scope_since IS NULL)
        OR (monitoring_status = 'out_of_scope' AND out_of_scope_since IS NOT NULL)
    );

CREATE TABLE account_inventory (
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
    lifecycle text NOT NULL,
    consecutive_missing_count integer NOT NULL,
    missing_since timestamptz,
    out_of_scope_since timestamptz,
    first_seen_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    current_poll_run_id uuid,
    current_scheduled_at timestamptz NOT NULL,
    source_observed_at timestamptz NOT NULL,
    source_node_version text NOT NULL,
    source_node_commit text NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (instance_id, account_key),
    CONSTRAINT account_inventory_provider_valid CHECK (
        octet_length(provider) BETWEEN 1 AND 64
        AND provider ~ '^[a-z0-9][a-z0-9._-]*$'
    ),
    CONSTRAINT account_inventory_email_valid CHECK (
        octet_length(normalized_email) BETWEEN 1 AND 320
        AND normalized_email = lower(btrim(normalized_email))
        AND normalized_email !~ '[[:cntrl:]]'
    ),
    CONSTRAINT account_inventory_key_valid CHECK (
        octet_length(account_key) BETWEEN 3 AND 385
        AND account_key = provider || ':' || normalized_email
    ),
    CONSTRAINT account_inventory_basic_status_fixed CHECK (
        basic_status IN ('disabled', 'unavailable', 'error', 'active', 'unknown')
    ),
    CONSTRAINT account_inventory_counts_bounded CHECK (
        success_count BETWEEN 0 AND 9223372036854775807
        AND failed_count BETWEEN 0 AND 9223372036854775807
        AND recent_request_count BETWEEN 0 AND 1000
        AND consecutive_missing_count BETWEEN 0 AND 2
    ),
    CONSTRAINT account_inventory_source_times_bounded CHECK (
        (last_refresh_at IS NULL OR last_refresh_at BETWEEN
            '0001-01-01 00:00:01+00'::timestamptz AND '9999-12-31 23:59:59+00'::timestamptz)
        AND (next_retry_at IS NULL OR next_retry_at BETWEEN
            '0001-01-01 00:00:01+00'::timestamptz AND '9999-12-31 23:59:59+00'::timestamptz)
        AND (source_updated_at IS NULL OR source_updated_at BETWEEN
            '0001-01-01 00:00:01+00'::timestamptz AND '9999-12-31 23:59:59+00'::timestamptz)
    ),
    CONSTRAINT account_inventory_slot_aligned CHECK (
        extract(epoch FROM current_scheduled_at)::bigint % 300 = 0
    ),
    CONSTRAINT account_inventory_source_version_allowlist CHECK (
        source_node_version = 'unknown'
        OR (octet_length(source_node_version) BETWEEN 1 AND 64
            AND source_node_version ~ '^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$')
    ),
    CONSTRAINT account_inventory_source_commit_allowlist CHECK (
        source_node_commit = 'unknown'
        OR (octet_length(source_node_commit) BETWEEN 7 AND 64
            AND source_node_commit ~ '^[0-9a-f]{7,64}$')
    ),
    CONSTRAINT account_inventory_times_ordered CHECK (
        first_seen_at <= last_seen_at
        AND last_seen_at = source_observed_at
        AND updated_at >= source_observed_at
        AND (missing_since IS NULL OR missing_since > last_seen_at)
        AND (out_of_scope_since IS NULL OR out_of_scope_since >= last_seen_at)
    ),
    CONSTRAINT account_inventory_lifecycle_shape CHECK (
        (lifecycle = 'present'
         AND consecutive_missing_count = 0
         AND missing_since IS NULL AND out_of_scope_since IS NULL)
        OR (lifecycle = 'suspected_missing'
            AND consecutive_missing_count = 1
            AND missing_since IS NULL AND out_of_scope_since IS NULL)
        OR (lifecycle = 'missing'
            AND consecutive_missing_count = 2
            AND missing_since IS NOT NULL AND out_of_scope_since IS NULL)
        OR (lifecycle = 'out_of_scope'
            AND consecutive_missing_count = 0
            AND missing_since IS NULL AND out_of_scope_since IS NOT NULL)
    ),
    FOREIGN KEY (instance_id) REFERENCES relay_node_assets(instance_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    FOREIGN KEY (current_poll_run_id, instance_id)
        REFERENCES account_inventory_poll_runs(poll_run_id, instance_id)
        ON UPDATE RESTRICT ON DELETE SET NULL (current_poll_run_id)
);

CREATE INDEX account_inventory_lifecycle_read_idx
    ON account_inventory (instance_id, provider, lifecycle, account_key);

CREATE TABLE account_inventory_scope_transition_audits (
    audit_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    activation_id uuid NOT NULL REFERENCES provider_inventory_policy_activations(activation_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    node_type text NOT NULL,
    driver_contract_version text NOT NULL,
    actor text NOT NULL,
    reason text NOT NULL,
    moved_out_providers text[] NOT NULL,
    reactivated_providers text[] NOT NULL,
    transitioned_at timestamptz NOT NULL,
    CONSTRAINT account_inventory_scope_audit_scope_valid CHECK (
        octet_length(node_type) BETWEEN 2 AND 64
        AND node_type ~ '^[a-z0-9][a-z0-9._-]*$'
        AND octet_length(driver_contract_version) BETWEEN 1 AND 64
        AND driver_contract_version ~ '^[a-z0-9][a-z0-9._-]*$'
    ),
    CONSTRAINT account_inventory_scope_audit_actor_valid CHECK (
        octet_length(actor) BETWEEN 1 AND 128
        AND actor ~ '^[A-Za-z0-9][A-Za-z0-9._@:-]*$'
    ),
    CONSTRAINT account_inventory_scope_audit_reason_valid CHECK (
        octet_length(reason) BETWEEN 1 AND 500
        AND reason = btrim(reason)
        AND reason !~ '[[:cntrl:]]'
    ),
    CONSTRAINT account_inventory_scope_audit_provider_sets_valid CHECK (
        moved_out_providers = public.control_normalize_provider_set(moved_out_providers)
        AND reactivated_providers = public.control_normalize_provider_set(reactivated_providers)
        AND NOT (moved_out_providers && reactivated_providers)
        AND cardinality(moved_out_providers) + cardinality(reactivated_providers) > 0
    ),
    UNIQUE (activation_id)
);

-- +goose StatementBegin
CREATE FUNCTION public.control_reject_account_inventory_scope_audit_mutation() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
    RAISE EXCEPTION 'account inventory scope transition audit is immutable'
        USING ERRCODE = '42501';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER account_inventory_scope_transition_audits_immutable
BEFORE UPDATE OR DELETE ON account_inventory_scope_transition_audits
FOR EACH ROW EXECUTE FUNCTION public.control_reject_account_inventory_scope_audit_mutation();
CREATE TRIGGER account_inventory_scope_transition_audits_truncate_immutable
BEFORE TRUNCATE ON account_inventory_scope_transition_audits
FOR EACH STATEMENT EXECUTE FUNCTION public.control_reject_account_inventory_scope_audit_mutation();

-- The current-state table accepts writes only while one of the two versioned
-- SECURITY DEFINER functions has opened the transaction-local write gate.
-- Runtime roles still receive no table DML privilege; the gate additionally
-- protects accidental direct migration-role writes and validates the source.
-- +goose StatementBegin
CREATE FUNCTION public.control_protect_account_inventory() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
DECLARE
    write_gate text := coalesce(current_setting('relay_control.lifecycle_write', true), '');
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'account inventory lifecycle cannot be deleted'
            USING ERRCODE = '42501';
    END IF;
    IF TG_OP = 'UPDATE'
       AND NEW.current_poll_run_id IS NULL
       AND OLD.current_poll_run_id IS NOT NULL
       AND NEW.instance_id = OLD.instance_id
       AND NEW.provider = OLD.provider
       AND NEW.account_key = OLD.account_key
       AND NEW.normalized_email = OLD.normalized_email
       AND NEW.basic_status = OLD.basic_status
       AND NEW.success_count = OLD.success_count
       AND NEW.failed_count = OLD.failed_count
       AND NEW.recent_request_count = OLD.recent_request_count
       AND NEW.last_refresh_at IS NOT DISTINCT FROM OLD.last_refresh_at
       AND NEW.next_retry_at IS NOT DISTINCT FROM OLD.next_retry_at
       AND NEW.source_updated_at IS NOT DISTINCT FROM OLD.source_updated_at
       AND NEW.lifecycle = OLD.lifecycle
       AND NEW.consecutive_missing_count = OLD.consecutive_missing_count
       AND NEW.missing_since IS NOT DISTINCT FROM OLD.missing_since
       AND NEW.out_of_scope_since IS NOT DISTINCT FROM OLD.out_of_scope_since
       AND NEW.first_seen_at = OLD.first_seen_at
       AND NEW.last_seen_at = OLD.last_seen_at
       AND NEW.current_scheduled_at = OLD.current_scheduled_at
       AND NEW.source_observed_at = OLD.source_observed_at
       AND NEW.source_node_version = OLD.source_node_version
       AND NEW.source_node_commit = OLD.source_node_commit
       AND NEW.updated_at = OLD.updated_at THEN
        RETURN NEW;
    END IF;
    IF write_gate NOT IN ('finalize', 'policy') THEN
        RAISE EXCEPTION 'account inventory lifecycle requires a controlled write'
            USING ERRCODE = '42501';
    END IF;
    IF TG_OP = 'UPDATE' THEN
        IF NEW.instance_id <> OLD.instance_id
           OR NEW.provider <> OLD.provider
           OR NEW.account_key <> OLD.account_key
           OR NEW.normalized_email <> OLD.normalized_email
           OR NEW.first_seen_at <> OLD.first_seen_at
           OR NEW.last_seen_at < OLD.last_seen_at
           OR NEW.current_scheduled_at < OLD.current_scheduled_at THEN
            RAISE EXCEPTION 'account inventory lifecycle identity or source regressed'
                USING ERRCODE = '23514';
        END IF;
    END IF;
    IF write_gate = 'finalize' AND NEW.current_poll_run_id IS NOT NULL AND NOT EXISTS (
        SELECT 1
        FROM public.account_inventory_poll_runs AS run
        JOIN public.account_inventory_poll_provider_results AS result
          ON result.poll_run_id = run.poll_run_id
         AND result.provider = NEW.provider
        WHERE run.poll_run_id = NEW.current_poll_run_id
          AND run.instance_id = NEW.instance_id
          AND run.status = 'finalized'
          AND result.promotion_applied
    ) THEN
        RAISE EXCEPTION 'account inventory lifecycle source is not an applied promotion'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER account_inventory_guard
BEFORE INSERT OR UPDATE OR DELETE ON account_inventory
FOR EACH ROW EXECUTE FUNCTION public.control_protect_account_inventory();
CREATE TRIGGER account_inventory_truncate_guard
BEFORE TRUNCATE ON account_inventory
FOR EACH STATEMENT EXECUTE FUNCTION public.control_reject_account_inventory_snapshot_mutation();

-- Extend the existing Provider pointer guard with the policy-only monitoring
-- transition while preserving the snapshot pointer and retention semantics.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_protect_account_inventory_provider_state() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
DECLARE write_gate text := coalesce(current_setting('relay_control.lifecycle_write', true), '');
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.monitoring_status <> 'active' OR NEW.out_of_scope_since IS NOT NULL OR NOT EXISTS (
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
       AND NEW.updated_at = OLD.updated_at
       AND NEW.monitoring_status = OLD.monitoring_status
       AND NEW.out_of_scope_since IS NOT DISTINCT FROM OLD.out_of_scope_since THEN
        RETURN NEW;
    END IF;
    IF write_gate = 'policy'
       AND NEW.current_poll_run_id IS NOT DISTINCT FROM OLD.current_poll_run_id
       AND NEW.current_scheduled_at = OLD.current_scheduled_at
       AND NEW.last_complete_at = OLD.last_complete_at
       AND NEW.source_observed_at = OLD.source_observed_at
       AND NEW.source_node_version = OLD.source_node_version
       AND NEW.source_node_commit = OLD.source_node_commit
       AND NEW.state = OLD.state
       AND (
           (OLD.monitoring_status = 'active' AND NEW.monitoring_status = 'out_of_scope'
            AND NEW.out_of_scope_since IS NOT NULL)
           OR (OLD.monitoring_status = 'out_of_scope' AND NEW.monitoring_status = 'active'
               AND NEW.out_of_scope_since IS NULL)
       ) THEN
        RETURN NEW;
    END IF;
    IF NEW.monitoring_status <> 'active' OR NEW.out_of_scope_since IS NOT NULL
       OR NEW.current_scheduled_at <= OLD.current_scheduled_at THEN
        RAISE EXCEPTION 'account inventory provider pointer must advance while active'
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

-- +goose StatementBegin
CREATE FUNCTION public.control_finalize_account_inventory_poll_run_with_lifecycle(
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
    run_record public.account_inventory_poll_runs%ROWTYPE;
    finalized_record public.account_inventory_poll_runs%ROWTYPE;
    expected_providers text[];
    promoted_provider text;
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
    PERFORM 1
    FROM public.provider_inventory_policy_bindings AS binding
    WHERE binding.node_type = run_record.node_type
      AND binding.driver_contract_version = run_record.driver_contract_version
    FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'account inventory policy binding is unavailable'
            USING ERRCODE = '23514';
    END IF;
    PERFORM 1
    FROM public.account_inventory_provider_states AS state
    WHERE state.instance_id = run_record.instance_id
      AND state.provider = ANY(expected_providers)
    ORDER BY state.provider
    FOR UPDATE;
    PERFORM 1
    FROM public.account_inventory AS account
    WHERE account.instance_id = run_record.instance_id
      AND account.provider = ANY(expected_providers)
    ORDER BY account.provider, account.account_key
    FOR UPDATE;

    PERFORM set_config('relay_control.lifecycle_write', 'finalize', true);
    SELECT * INTO finalized_record
    FROM public.control_finalize_account_inventory_poll_run(
        target_poll_run_id, expected_fencing_token,
        result_transport_success, result_response_shape_valid, result_contract_valid,
        result_inventory_mode, result_node_identity_complete, result_snapshot_complete,
        result_degraded, result_result, result_reason, result_source_record_count,
        result_identifiable_record_count, result_unidentified_record_count,
        result_unsupported_provider_count, result_out_of_scope_provider_count,
        result_node_version, result_node_commit, provider_results, snapshot_items,
        duplicate_evidence
    );
    IF NOT FOUND THEN
        RETURN;
    END IF;

    FOR promoted_provider IN
        SELECT result.provider
        FROM public.account_inventory_poll_provider_results AS result
        WHERE result.poll_run_id = target_poll_run_id
          AND result.promotion_applied
        ORDER BY result.provider
    LOOP
        IF NOT EXISTS (
            SELECT 1 FROM public.account_inventory_provider_states AS state
            WHERE state.instance_id = finalized_record.instance_id
              AND state.provider = promoted_provider
              AND state.monitoring_status = 'active'
        ) THEN
            RAISE EXCEPTION 'promoted Provider is not actively monitored'
                USING ERRCODE = '23514';
        END IF;

        UPDATE public.account_inventory AS account
        SET lifecycle = CASE account.lifecycle
                WHEN 'present' THEN 'suspected_missing'
                WHEN 'suspected_missing' THEN 'missing'
                ELSE account.lifecycle
            END,
            consecutive_missing_count = CASE account.lifecycle
                WHEN 'present' THEN 1
                WHEN 'suspected_missing' THEN 2
                ELSE account.consecutive_missing_count
            END,
            missing_since = CASE account.lifecycle
                WHEN 'suspected_missing' THEN finalized_record.observed_at
                ELSE account.missing_since
            END,
            updated_at = CASE account.lifecycle
                WHEN 'present' THEN finalized_record.observed_at
                WHEN 'suspected_missing' THEN finalized_record.observed_at
                ELSE account.updated_at
            END
        WHERE account.instance_id = finalized_record.instance_id
          AND account.provider = promoted_provider
          AND account.lifecycle <> 'out_of_scope'
          AND NOT EXISTS (
              SELECT 1 FROM public.account_inventory_snapshot_items AS item
              WHERE item.poll_run_id = target_poll_run_id
                AND item.instance_id = account.instance_id
                AND item.provider = account.provider
                AND item.account_key = account.account_key
          );

        INSERT INTO public.account_inventory (
            instance_id, provider, account_key, normalized_email, basic_status,
            success_count, failed_count, recent_request_count, last_refresh_at,
            next_retry_at, source_updated_at, lifecycle,
            consecutive_missing_count, missing_since, out_of_scope_since,
            first_seen_at, last_seen_at, current_poll_run_id,
            current_scheduled_at, source_observed_at, source_node_version,
            source_node_commit, updated_at
        )
        SELECT item.instance_id, item.provider, item.account_key,
               item.normalized_email, item.basic_status, item.success_count,
               item.failed_count, item.recent_request_count,
               item.last_refresh_at, item.next_retry_at, item.source_updated_at,
               'present', 0, NULL, NULL, finalized_record.observed_at,
               finalized_record.observed_at, target_poll_run_id,
               finalized_record.scheduled_at, finalized_record.observed_at,
               finalized_record.node_version, finalized_record.node_commit,
               finalized_record.observed_at
        FROM public.account_inventory_snapshot_items AS item
        WHERE item.poll_run_id = target_poll_run_id
          AND item.instance_id = finalized_record.instance_id
          AND item.provider = promoted_provider
        ORDER BY item.account_key
        ON CONFLICT (instance_id, account_key) DO UPDATE
        SET basic_status = EXCLUDED.basic_status,
            success_count = EXCLUDED.success_count,
            failed_count = EXCLUDED.failed_count,
            recent_request_count = EXCLUDED.recent_request_count,
            last_refresh_at = EXCLUDED.last_refresh_at,
            next_retry_at = EXCLUDED.next_retry_at,
            source_updated_at = EXCLUDED.source_updated_at,
            lifecycle = 'present', consecutive_missing_count = 0,
            missing_since = NULL, out_of_scope_since = NULL,
            last_seen_at = EXCLUDED.last_seen_at,
            current_poll_run_id = EXCLUDED.current_poll_run_id,
            current_scheduled_at = EXCLUDED.current_scheduled_at,
            source_observed_at = EXCLUDED.source_observed_at,
            source_node_version = EXCLUDED.source_node_version,
            source_node_commit = EXCLUDED.source_node_commit,
            updated_at = EXCLUDED.updated_at;
    END LOOP;

    RETURN NEXT finalized_record;
END;
$$;
-- +goose StatementEnd

ALTER FUNCTION public.control_finalize_account_inventory_poll_run_with_lifecycle(
    uuid, uuid, boolean, boolean, boolean, text, boolean, boolean, boolean,
    text, text, integer, integer, integer, integer, integer, text, text,
    jsonb, jsonb, jsonb
) OWNER TO relay_control_migrator;

-- +goose StatementBegin
CREATE FUNCTION public.control_activate_provider_policy_with_lifecycle(
    policy_node_type text,
    policy_driver_version text,
    requested_active_providers text[],
    requested_out_of_scope_providers text[],
    policy_actor text,
    policy_reason text,
    requested_effective_at timestamptz DEFAULT NULL
) RETURNS uuid
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    database_now timestamptz := clock_timestamp();
    normalized_active text[];
    normalized_out_of_scope text[];
    old_active text[] := ARRAY[]::text[];
    old_out_of_scope text[] := ARRAY[]::text[];
    moved_out text[] := ARRAY[]::text[];
    reactivated text[] := ARRAY[]::text[];
    bound_policy_id uuid;
    active_policy_id uuid;
    result_activation_id uuid;
    transition_time timestamptz;
BEGIN
    IF policy_node_type IS NULL
       OR octet_length(policy_node_type) NOT BETWEEN 2 AND 64
       OR policy_node_type !~ '^[a-z0-9][a-z0-9._-]*$'
       OR policy_driver_version IS NULL
       OR octet_length(policy_driver_version) NOT BETWEEN 1 AND 64
       OR policy_driver_version !~ '^[a-z0-9][a-z0-9._-]*$'
       OR requested_active_providers IS NULL
       OR requested_out_of_scope_providers IS NULL
       OR policy_actor IS NULL
       OR octet_length(policy_actor) NOT BETWEEN 1 AND 128
       OR policy_actor !~ '^[A-Za-z0-9][A-Za-z0-9._@:-]*$'
       OR policy_reason IS NULL
       OR octet_length(policy_reason) NOT BETWEEN 1 AND 500
       OR policy_reason <> btrim(policy_reason)
       OR policy_reason ~ '[[:cntrl:]]' THEN
        RAISE EXCEPTION 'provider policy registration rejected' USING ERRCODE = '22023';
    END IF;
    IF requested_effective_at IS NOT NULL THEN
        RAISE EXCEPTION 'lifecycle-aware Provider policy activation must be immediate'
            USING ERRCODE = '22023';
    END IF;
    normalized_active := public.control_normalize_provider_set(requested_active_providers);
    normalized_out_of_scope := public.control_normalize_provider_set(requested_out_of_scope_providers);
    IF cardinality(normalized_active) + cardinality(normalized_out_of_scope) = 0
       OR normalized_active && normalized_out_of_scope THEN
        RAISE EXCEPTION 'provider policy registration rejected' USING ERRCODE = '22023';
    END IF;

    PERFORM pg_advisory_xact_lock(hashtextextended(
        policy_node_type || chr(31) || policy_driver_version, 0
    ));
    SELECT binding.policy_version_id INTO bound_policy_id
    FROM public.provider_inventory_policy_bindings AS binding
    WHERE binding.node_type = policy_node_type
      AND binding.driver_contract_version = policy_driver_version
    FOR UPDATE;
    IF FOUND THEN
        SELECT activation.policy_version_id INTO active_policy_id
        FROM public.provider_inventory_policy_activations AS activation
        WHERE activation.node_type = policy_node_type
          AND activation.driver_contract_version = policy_driver_version
          AND activation.active_range @> database_now;
        IF active_policy_id IS NULL OR active_policy_id <> bound_policy_id THEN
            RAISE EXCEPTION 'Provider policy binding is not immediately effective'
                USING ERRCODE = '23514';
        END IF;
        SELECT policy.active_providers, policy.out_of_scope_providers
        INTO old_active, old_out_of_scope
        FROM public.provider_inventory_policy_versions AS policy
        WHERE policy.policy_version_id = bound_policy_id;
        IF public.control_normalize_provider_set(old_active || old_out_of_scope)
           IS DISTINCT FROM
           public.control_normalize_provider_set(normalized_active || normalized_out_of_scope) THEN
            RAISE EXCEPTION 'lifecycle-aware Provider policy cannot add or remove registered Providers'
                USING ERRCODE = '22023';
        END IF;
    END IF;

    SELECT coalesce(array_agg(provider ORDER BY provider), ARRAY[]::text[])
    INTO moved_out
    FROM (
        SELECT unnest(old_active) AS provider
        INTERSECT
        SELECT unnest(normalized_out_of_scope) AS provider
    ) AS changed;
    SELECT coalesce(array_agg(provider ORDER BY provider), ARRAY[]::text[])
    INTO reactivated
    FROM (
        SELECT unnest(old_out_of_scope) AS provider
        INTERSECT
        SELECT unnest(normalized_active) AS provider
    ) AS changed;

    PERFORM 1
    FROM public.account_inventory_provider_states AS state
    JOIN public.relay_node_assets AS asset ON asset.instance_id = state.instance_id
    WHERE asset.node_type = policy_node_type
      AND asset.driver_contract_version = policy_driver_version
      AND state.provider = ANY(moved_out || reactivated)
    ORDER BY state.instance_id, state.provider
    FOR UPDATE OF state;
    PERFORM 1
    FROM public.account_inventory AS account
    JOIN public.relay_node_assets AS asset ON asset.instance_id = account.instance_id
    WHERE asset.node_type = policy_node_type
      AND asset.driver_contract_version = policy_driver_version
      AND account.provider = ANY(moved_out || reactivated)
    ORDER BY account.instance_id, account.provider, account.account_key
    FOR UPDATE OF account;

    PERFORM set_config('relay_control.lifecycle_write', 'policy', true);
    result_activation_id := public.control_activate_provider_policy(
        policy_node_type, policy_driver_version, normalized_active,
        normalized_out_of_scope, policy_actor, NULL
    );
    SELECT activation.effective_from INTO transition_time
    FROM public.provider_inventory_policy_activations AS activation
    WHERE activation.activation_id = result_activation_id;

    IF cardinality(moved_out) + cardinality(reactivated) > 0 THEN
        INSERT INTO public.account_inventory_scope_transition_audits (
            audit_id, activation_id, node_type, driver_contract_version,
            actor, reason, moved_out_providers, reactivated_providers,
            transitioned_at
        ) VALUES (
            gen_random_uuid(), result_activation_id, policy_node_type,
            policy_driver_version, policy_actor, policy_reason, moved_out,
            reactivated, transition_time
        );
    END IF;

    UPDATE public.account_inventory_provider_states AS state
    SET monitoring_status = 'out_of_scope', out_of_scope_since = transition_time,
        updated_at = transition_time
    FROM public.relay_node_assets AS asset
    WHERE asset.instance_id = state.instance_id
      AND asset.node_type = policy_node_type
      AND asset.driver_contract_version = policy_driver_version
      AND state.provider = ANY(moved_out);
    UPDATE public.account_inventory AS account
    SET lifecycle = 'out_of_scope', consecutive_missing_count = 0,
        missing_since = NULL, out_of_scope_since = transition_time,
        updated_at = transition_time
    FROM public.relay_node_assets AS asset
    WHERE asset.instance_id = account.instance_id
      AND asset.node_type = policy_node_type
      AND asset.driver_contract_version = policy_driver_version
      AND account.provider = ANY(moved_out);

    UPDATE public.account_inventory_provider_states AS state
    SET monitoring_status = 'active', out_of_scope_since = NULL,
        updated_at = transition_time
    FROM public.relay_node_assets AS asset
    WHERE asset.instance_id = state.instance_id
      AND asset.node_type = policy_node_type
      AND asset.driver_contract_version = policy_driver_version
      AND state.provider = ANY(reactivated);
    RETURN result_activation_id;
END;
$$;
-- +goose StatementEnd

ALTER FUNCTION public.control_activate_provider_policy_with_lifecycle(
    text, text, text[], text[], text, text, timestamptz
) OWNER TO relay_control_migrator;

-- +goose StatementBegin
CREATE FUNCTION public.control_list_current_account_inventory_lifecycle(
    target_instance_id uuid,
    target_provider text,
    target_lifecycle text,
    after_account_key text,
    page_limit integer
) RETURNS TABLE (
    provider text,
    account_key text,
    normalized_email text,
    basic_status text,
    success_count bigint,
    failed_count bigint,
    recent_request_count bigint,
    last_refresh_at timestamptz,
    next_retry_at timestamptz,
    source_updated_at timestamptz,
    lifecycle text,
    consecutive_missing_count integer,
    missing_since timestamptz,
    out_of_scope_since timestamptz,
    first_seen_at timestamptz,
    last_seen_at timestamptz,
    current_poll_run_id uuid,
    current_scheduled_at timestamptz,
    source_observed_at timestamptz,
    source_node_version text,
    source_node_commit text,
    updated_at timestamptz
)
LANGUAGE plpgsql
SECURITY DEFINER
STABLE
SET search_path = pg_catalog
AS $$
BEGIN
    IF target_instance_id IS NULL
       OR (coalesce(target_provider, '') <> '' AND (
           octet_length(target_provider) NOT BETWEEN 1 AND 64
           OR target_provider !~ '^[a-z0-9][a-z0-9._-]*$'))
       OR (coalesce(target_lifecycle, '') <> '' AND target_lifecycle NOT IN (
           'present', 'suspected_missing', 'missing', 'out_of_scope'))
       OR after_account_key IS NULL OR octet_length(after_account_key) > 385
       OR page_limit < 1 OR page_limit > 500 THEN
        RAISE EXCEPTION 'invalid account inventory lifecycle page'
            USING ERRCODE = '22023';
    END IF;
    RETURN QUERY
    SELECT account.provider, account.account_key, account.normalized_email,
           account.basic_status, account.success_count, account.failed_count,
           account.recent_request_count, account.last_refresh_at,
           account.next_retry_at, account.source_updated_at, account.lifecycle,
           account.consecutive_missing_count, account.missing_since,
           account.out_of_scope_since, account.first_seen_at,
           account.last_seen_at, account.current_poll_run_id,
           account.current_scheduled_at, account.source_observed_at,
           account.source_node_version, account.source_node_commit,
           account.updated_at
    FROM public.account_inventory AS account
    WHERE account.instance_id = target_instance_id
      AND (coalesce(target_provider, '') = '' OR account.provider = target_provider)
      AND (coalesce(target_lifecycle, '') = '' OR account.lifecycle = target_lifecycle)
      AND account.account_key > after_account_key
    ORDER BY account.account_key
    LIMIT page_limit;
END;
$$;
-- +goose StatementEnd

ALTER FUNCTION public.control_list_current_account_inventory_lifecycle(uuid, text, text, text, integer)
    OWNER TO relay_control_migrator;

CREATE FUNCTION public.control_list_account_inventory_lifecycle_metrics()
RETURNS TABLE (instance_id uuid, provider text, lifecycle text, account_count bigint)
LANGUAGE sql
SECURITY DEFINER
STABLE
SET search_path = pg_catalog
AS $$
    SELECT account.instance_id, account.provider, account.lifecycle, count(*)::bigint
    FROM public.account_inventory AS account
    GROUP BY account.instance_id, account.provider, account.lifecycle
    ORDER BY account.instance_id, account.provider, account.lifecycle
$$;

ALTER FUNCTION public.control_list_account_inventory_lifecycle_metrics()
    OWNER TO relay_control_migrator;

REVOKE ALL ON TABLE account_inventory, account_inventory_scope_transition_audits
FROM PUBLIC, relay_control_runtime, relay_control_asset_registrar;
REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON account_inventory_provider_states
FROM relay_control_runtime, relay_control_asset_registrar;

REVOKE EXECUTE ON FUNCTION public.control_finalize_account_inventory_poll_run(
    uuid, uuid, boolean, boolean, boolean, text, boolean, boolean, boolean,
    text, text, integer, integer, integer, integer, integer, text, text,
    jsonb, jsonb, jsonb
) FROM relay_control_runtime;
REVOKE EXECUTE ON FUNCTION public.control_activate_provider_policy(
    text, text, text[], text[], text, timestamptz
) FROM relay_control_asset_registrar;

REVOKE EXECUTE ON FUNCTION public.control_protect_account_inventory() FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_reject_account_inventory_scope_audit_mutation() FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_finalize_account_inventory_poll_run_with_lifecycle(
    uuid, uuid, boolean, boolean, boolean, text, boolean, boolean, boolean,
    text, text, integer, integer, integer, integer, integer, text, text,
    jsonb, jsonb, jsonb
) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_activate_provider_policy_with_lifecycle(
    text, text, text[], text[], text, text, timestamptz
) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_list_current_account_inventory_lifecycle(
    uuid, text, text, text, integer
) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_list_account_inventory_lifecycle_metrics() FROM PUBLIC;

GRANT EXECUTE ON FUNCTION public.control_finalize_account_inventory_poll_run_with_lifecycle(
    uuid, uuid, boolean, boolean, boolean, text, boolean, boolean, boolean,
    text, text, integer, integer, integer, integer, integer, text, text,
    jsonb, jsonb, jsonb
) TO relay_control_runtime;
GRANT EXECUTE ON FUNCTION public.control_list_current_account_inventory_lifecycle(
    uuid, text, text, text, integer
) TO relay_control_runtime;
GRANT EXECUTE ON FUNCTION public.control_list_account_inventory_lifecycle_metrics()
TO relay_control_runtime;
GRANT EXECUTE ON FUNCTION public.control_activate_provider_policy_with_lifecycle(
    text, text, text[], text[], text, text, timestamptz
) TO relay_control_asset_registrar;

-- +goose Down
-- +goose StatementBegin
DO $$
DECLARE lifecycle_count bigint;
DECLARE out_of_scope_count bigint;
DECLARE audit_count bigint;
DECLARE legacy_incompatible_policy_count bigint;
BEGIN
    LOCK TABLE account_inventory, account_inventory_provider_states,
        account_inventory_scope_transition_audits,
        provider_inventory_policy_versions
        IN ACCESS EXCLUSIVE MODE NOWAIT;
    SELECT count(*) INTO lifecycle_count FROM account_inventory;
    SELECT count(*) INTO audit_count FROM account_inventory_scope_transition_audits;
    SELECT count(*) INTO out_of_scope_count
    FROM account_inventory_provider_states
    WHERE monitoring_status <> 'active' OR out_of_scope_since IS NOT NULL;
    SELECT count(*) INTO legacy_incompatible_policy_count
    FROM provider_inventory_policy_versions
    WHERE cardinality(active_providers) = 0
       OR cardinality(out_of_scope_providers) = 0;
    IF lifecycle_count <> 0 OR out_of_scope_count <> 0 OR audit_count <> 0
       OR legacy_incompatible_policy_count <> 0 THEN
        RAISE EXCEPTION 'account inventory lifecycle migration down requires empty lifecycle state'
            USING ERRCODE = '55000';
    END IF;
END;
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION public.control_activate_provider_policy_with_lifecycle(
    text, text, text[], text[], text, text, timestamptz
) FROM relay_control_asset_registrar;
REVOKE EXECUTE ON FUNCTION public.control_list_account_inventory_lifecycle_metrics()
FROM relay_control_runtime;
REVOKE EXECUTE ON FUNCTION public.control_list_current_account_inventory_lifecycle(
    uuid, text, text, text, integer
) FROM relay_control_runtime;
REVOKE EXECUTE ON FUNCTION public.control_finalize_account_inventory_poll_run_with_lifecycle(
    uuid, uuid, boolean, boolean, boolean, text, boolean, boolean, boolean,
    text, text, integer, integer, integer, integer, integer, text, text,
    jsonb, jsonb, jsonb
) FROM relay_control_runtime;

DROP FUNCTION public.control_list_account_inventory_lifecycle_metrics();
DROP FUNCTION public.control_list_current_account_inventory_lifecycle(uuid, text, text, text, integer);
DROP FUNCTION public.control_activate_provider_policy_with_lifecycle(
    text, text, text[], text[], text, text, timestamptz
);
DROP FUNCTION public.control_finalize_account_inventory_poll_run_with_lifecycle(
    uuid, uuid, boolean, boolean, boolean, text, boolean, boolean, boolean,
    text, text, integer, integer, integer, integer, integer, text, text,
    jsonb, jsonb, jsonb
);

GRANT EXECUTE ON FUNCTION public.control_activate_provider_policy(
    text, text, text[], text[], text, timestamptz
) TO relay_control_asset_registrar;
GRANT EXECUTE ON FUNCTION public.control_finalize_account_inventory_poll_run(
    uuid, uuid, boolean, boolean, boolean, text, boolean, boolean, boolean,
    text, text, integer, integer, integer, integer, integer, text, text,
    jsonb, jsonb, jsonb
) TO relay_control_runtime;

DROP TRIGGER account_inventory_truncate_guard ON account_inventory;
DROP TRIGGER account_inventory_guard ON account_inventory;
DROP FUNCTION public.control_protect_account_inventory();
DROP TABLE account_inventory;
DROP TRIGGER account_inventory_scope_transition_audits_truncate_immutable
    ON account_inventory_scope_transition_audits;
DROP TRIGGER account_inventory_scope_transition_audits_immutable
    ON account_inventory_scope_transition_audits;
DROP FUNCTION public.control_reject_account_inventory_scope_audit_mutation();
DROP TABLE account_inventory_scope_transition_audits;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_protect_account_inventory_provider_state() RETURNS trigger
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

ALTER TABLE account_inventory_provider_states
    DROP CONSTRAINT account_inventory_provider_monitoring_shape,
    DROP CONSTRAINT account_inventory_provider_monitoring_status_fixed,
    DROP COLUMN out_of_scope_since,
    DROP COLUMN monitoring_status;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_normalize_provider_set(provider_values text[]) RETURNS text[]
LANGUAGE plpgsql
IMMUTABLE STRICT
SET search_path = pg_catalog
AS $$
DECLARE
    normalized text[];
BEGIN
    IF array_ndims(provider_values) IS DISTINCT FROM 1
       OR EXISTS (
           SELECT 1 FROM unnest(provider_values) AS provider(value)
           WHERE value IS NULL
              OR octet_length(value) NOT BETWEEN 1 AND 64
              OR value !~ '^[a-z0-9][a-z0-9._-]*$'
       ) THEN
        RAISE EXCEPTION 'provider set is invalid' USING ERRCODE = '22023';
    END IF;
    SELECT COALESCE(array_agg(DISTINCT value ORDER BY value), ARRAY[]::text[])
    INTO normalized
    FROM unnest(provider_values) AS provider(value);
    RETURN normalized;
END;
$$;
-- +goose StatementEnd
