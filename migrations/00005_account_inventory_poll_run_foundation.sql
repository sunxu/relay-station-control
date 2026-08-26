-- +goose Up

CREATE TABLE account_inventory_poll_runs (
    poll_run_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    instance_id uuid NOT NULL,
    node_type text NOT NULL,
    driver_contract_version text NOT NULL,
    scheduled_at timestamptz NOT NULL,
    provider_policy_version uuid NOT NULL,
    status text NOT NULL DEFAULT 'pending',
    attempt_count integer NOT NULL DEFAULT 0,
    max_attempts integer NOT NULL DEFAULT 2,
    poll_start_grace_seconds integer NOT NULL DEFAULT 120,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    first_started_at timestamptz,
    last_started_at timestamptz,
    lease_expires_at timestamptz,
    lease_fencing_token uuid,
    finalized_at timestamptz,
    abandoned_at timestamptz,
    execution_reason text,
    observed_at timestamptz,
    transport_success boolean,
    response_shape_valid boolean,
    contract_valid boolean,
    inventory_mode text,
    node_identity_complete boolean,
    snapshot_complete boolean,
    degraded boolean,
    result text,
    reason text,
    source_record_count integer,
    identifiable_record_count integer,
    unidentified_record_count integer,
    unsupported_provider_count integer,
    out_of_scope_provider_count integer,
    node_version text,
    node_commit text,
    CONSTRAINT account_inventory_poll_runs_status_fixed CHECK (
        status IN ('pending', 'running', 'retry_wait', 'finalized', 'abandoned')
    ),
    CONSTRAINT account_inventory_poll_runs_slot_aligned CHECK (
        extract(epoch FROM scheduled_at)::bigint % 300 = 0
    ),
    CONSTRAINT account_inventory_poll_runs_attempts_bounded CHECK (
        max_attempts BETWEEN 1 AND 10
        AND attempt_count BETWEEN 0 AND max_attempts
        AND poll_start_grace_seconds BETWEEN 1 AND 299
    ),
    CONSTRAINT account_inventory_poll_runs_times_ordered CHECK (
        created_at >= scheduled_at
        AND (first_started_at IS NULL OR first_started_at >= created_at)
        AND (last_started_at IS NULL OR last_started_at >= first_started_at)
        AND (finalized_at IS NULL OR finalized_at >= last_started_at)
        AND (abandoned_at IS NULL OR abandoned_at >= created_at)
        AND (observed_at IS NULL OR observed_at >= last_started_at)
    ),
    CONSTRAINT account_inventory_poll_runs_execution_reason_fixed CHECK (
        execution_reason IS NULL OR execution_reason IN (
            'lease_expired', 'poll_start_grace_expired', 'max_attempts_exhausted'
        )
    ),
    CONSTRAINT account_inventory_poll_runs_result_fixed CHECK (
        result IS NULL OR result IN ('success', 'failed', 'degraded', 'unsupported')
    ),
    CONSTRAINT account_inventory_poll_runs_reason_fixed CHECK (
        reason IS NULL OR reason IN (
            'none', 'capability_unsupported', 'node_type_unsupported',
            'driver_contract_mismatch', 'secret_unavailable',
            'secret_reference_unknown', 'secret_provider_unknown',
            'secret_file_unsafe', 'target_rejected', 'dns_rejected',
            'network_unavailable', 'tls_rejected', 'redirect_rejected',
            'timeout', 'cancelled', 'http_status', 'response_invalid',
            'response_too_large', 'record_limit', 'contract_invalid'
        )
    ),
    CONSTRAINT account_inventory_poll_runs_mode_fixed CHECK (
        inventory_mode IS NULL OR inventory_mode IN ('runtime', 'disk_fallback')
    ),
    CONSTRAINT account_inventory_poll_runs_counts_bounded CHECK (
        (source_record_count IS NULL OR source_record_count BETWEEN 0 AND 1000)
        AND (identifiable_record_count IS NULL OR identifiable_record_count BETWEEN 0 AND 1000)
        AND (unidentified_record_count IS NULL OR unidentified_record_count BETWEEN 0 AND 1000)
        AND (unsupported_provider_count IS NULL OR unsupported_provider_count BETWEEN 0 AND 1000)
        AND (out_of_scope_provider_count IS NULL OR out_of_scope_provider_count BETWEEN 0 AND 1000)
    ),
    CONSTRAINT account_inventory_poll_runs_version_allowlist CHECK (
        node_version IS NULL OR node_version = 'unknown'
        OR (octet_length(node_version) BETWEEN 1 AND 64
            AND node_version ~ '^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$')
    ),
    CONSTRAINT account_inventory_poll_runs_commit_allowlist CHECK (
        node_commit IS NULL OR node_commit = 'unknown'
        OR (octet_length(node_commit) BETWEEN 7 AND 64
            AND node_commit ~ '^[0-9a-f]{7,64}$')
    ),
    CONSTRAINT account_inventory_poll_runs_state_shape CHECK (
        (
            status = 'pending'
            AND attempt_count = 0
            AND first_started_at IS NULL AND last_started_at IS NULL
            AND lease_expires_at IS NULL AND lease_fencing_token IS NULL
            AND finalized_at IS NULL AND abandoned_at IS NULL
            AND execution_reason IS NULL
            AND observed_at IS NULL AND transport_success IS NULL
            AND response_shape_valid IS NULL AND contract_valid IS NULL
            AND inventory_mode IS NULL AND node_identity_complete IS NULL
            AND snapshot_complete IS NULL AND degraded IS NULL
            AND result IS NULL AND reason IS NULL
            AND source_record_count IS NULL AND identifiable_record_count IS NULL
            AND unidentified_record_count IS NULL AND unsupported_provider_count IS NULL
            AND out_of_scope_provider_count IS NULL AND node_version IS NULL AND node_commit IS NULL
        ) OR (
            status = 'running'
            AND attempt_count > 0
            AND first_started_at IS NOT NULL AND last_started_at IS NOT NULL
            AND lease_expires_at > last_started_at AND lease_fencing_token IS NOT NULL
            AND finalized_at IS NULL AND abandoned_at IS NULL
            AND execution_reason IS NULL
            AND observed_at IS NULL AND transport_success IS NULL
            AND response_shape_valid IS NULL AND contract_valid IS NULL
            AND inventory_mode IS NULL AND node_identity_complete IS NULL
            AND snapshot_complete IS NULL AND degraded IS NULL
            AND result IS NULL AND reason IS NULL
            AND source_record_count IS NULL AND identifiable_record_count IS NULL
            AND unidentified_record_count IS NULL AND unsupported_provider_count IS NULL
            AND out_of_scope_provider_count IS NULL AND node_version IS NULL AND node_commit IS NULL
        ) OR (
            status = 'retry_wait'
            AND attempt_count > 0
            AND first_started_at IS NOT NULL AND last_started_at IS NOT NULL
            AND lease_expires_at IS NULL AND lease_fencing_token IS NULL
            AND finalized_at IS NULL AND abandoned_at IS NULL
            AND execution_reason = 'lease_expired'
            AND observed_at IS NULL AND transport_success IS NULL
            AND response_shape_valid IS NULL AND contract_valid IS NULL
            AND inventory_mode IS NULL AND node_identity_complete IS NULL
            AND snapshot_complete IS NULL AND degraded IS NULL
            AND result IS NULL AND reason IS NULL
            AND source_record_count IS NULL AND identifiable_record_count IS NULL
            AND unidentified_record_count IS NULL AND unsupported_provider_count IS NULL
            AND out_of_scope_provider_count IS NULL AND node_version IS NULL AND node_commit IS NULL
        ) OR (
            status = 'finalized'
            AND attempt_count > 0
            AND first_started_at IS NOT NULL AND last_started_at IS NOT NULL
            AND lease_expires_at IS NULL AND lease_fencing_token IS NULL
            AND finalized_at IS NOT NULL AND abandoned_at IS NULL
            AND execution_reason IS NULL AND observed_at IS NOT NULL
            AND transport_success IS NOT NULL AND response_shape_valid IS NOT NULL
            AND contract_valid IS NOT NULL AND node_identity_complete IS NOT NULL
            AND snapshot_complete IS NOT NULL AND degraded IS NOT NULL
            AND result IS NOT NULL AND reason IS NOT NULL
            AND source_record_count IS NOT NULL AND identifiable_record_count IS NOT NULL
            AND unidentified_record_count IS NOT NULL AND unsupported_provider_count IS NOT NULL
            AND out_of_scope_provider_count IS NOT NULL
            AND node_version IS NOT NULL AND node_commit IS NOT NULL
            AND (contract_valid OR inventory_mode IS NULL)
            AND (NOT contract_valid OR inventory_mode IS NOT NULL)
            AND (NOT response_shape_valid OR transport_success)
            AND (NOT contract_valid OR response_shape_valid)
            AND (NOT snapshot_complete OR contract_valid)
            AND (reason = 'none') = contract_valid
            AND (result IN ('success', 'degraded')) = contract_valid
            AND (result <> 'success' OR (snapshot_complete AND NOT degraded))
            AND (result <> 'degraded' OR degraded)
            AND (contract_valid OR degraded)
        ) OR (
            status = 'abandoned'
            AND finalized_at IS NULL AND abandoned_at IS NOT NULL
            AND lease_expires_at IS NULL AND lease_fencing_token IS NULL
            AND execution_reason IN ('poll_start_grace_expired', 'max_attempts_exhausted')
            AND observed_at IS NULL AND transport_success IS NULL
            AND response_shape_valid IS NULL AND contract_valid IS NULL
            AND inventory_mode IS NULL AND node_identity_complete IS NULL
            AND snapshot_complete IS NULL AND degraded IS NULL
            AND result IS NULL AND reason IS NULL
            AND source_record_count IS NULL AND identifiable_record_count IS NULL
            AND unidentified_record_count IS NULL AND unsupported_provider_count IS NULL
            AND out_of_scope_provider_count IS NULL AND node_version IS NULL AND node_commit IS NULL
        )
    ),
    FOREIGN KEY (instance_id, node_type, driver_contract_version)
        REFERENCES relay_node_assets (instance_id, node_type, driver_contract_version)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    FOREIGN KEY (provider_policy_version, node_type, driver_contract_version)
        REFERENCES provider_inventory_policy_versions
            (policy_version_id, node_type, driver_contract_version)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    UNIQUE (instance_id, scheduled_at)
);

CREATE INDEX account_inventory_poll_runs_claim_idx
    ON account_inventory_poll_runs (scheduled_at, instance_id)
    WHERE status IN ('pending', 'retry_wait');
CREATE INDEX account_inventory_poll_runs_reconcile_idx
    ON account_inventory_poll_runs (lease_expires_at, scheduled_at, instance_id)
    WHERE status IN ('pending', 'running', 'retry_wait');
CREATE INDEX account_inventory_poll_runs_metrics_idx
    ON account_inventory_poll_runs (instance_id, scheduled_at DESC);

CREATE TABLE account_inventory_poll_provider_results (
    poll_run_id uuid NOT NULL REFERENCES account_inventory_poll_runs(poll_run_id)
        ON UPDATE RESTRICT ON DELETE CASCADE,
    provider text NOT NULL,
    identifiable_count integer NOT NULL,
    missing_identity_count integer NOT NULL,
    duplicate_identity_count integer NOT NULL,
    identity_complete boolean NOT NULL,
    snapshot_complete boolean NOT NULL,
    degraded boolean NOT NULL,
    reason text NOT NULL,
    PRIMARY KEY (poll_run_id, provider),
    CONSTRAINT account_inventory_poll_provider_name_valid CHECK (
        octet_length(provider) BETWEEN 1 AND 64
        AND provider ~ '^[a-z0-9][a-z0-9._-]*$'
    ),
    CONSTRAINT account_inventory_poll_provider_counts_bounded CHECK (
        identifiable_count BETWEEN 0 AND 1000
        AND missing_identity_count BETWEEN 0 AND 1000
        AND duplicate_identity_count BETWEEN 0 AND 1000
    ),
    CONSTRAINT account_inventory_poll_provider_identity_consistent CHECK (
        identity_complete = (
            missing_identity_count = 0 AND duplicate_identity_count = 0
        )
    ),
    CONSTRAINT account_inventory_poll_provider_reason_fixed CHECK (
        reason IN (
            'complete', 'transport_failed', 'contract_invalid', 'disk_fallback',
            'node_identity_incomplete', 'identity_incomplete'
        )
    ),
    CONSTRAINT account_inventory_poll_provider_result_consistent CHECK (
        (reason = 'complete' AND identity_complete AND snapshot_complete AND NOT degraded)
        OR (reason <> 'complete' AND degraded)
    )
);

-- +goose StatementBegin
CREATE FUNCTION public.control_protect_account_inventory_poll_run() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
DECLARE
    expected_providers text[];
    stored_providers text[];
BEGIN
    IF TG_OP = 'DELETE' OR OLD.status IN ('finalized', 'abandoned') THEN
        RAISE EXCEPTION 'terminal account inventory poll evidence is immutable'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.poll_run_id <> OLD.poll_run_id
       OR NEW.instance_id <> OLD.instance_id
       OR NEW.node_type <> OLD.node_type
       OR NEW.driver_contract_version <> OLD.driver_contract_version
       OR NEW.scheduled_at <> OLD.scheduled_at
       OR NEW.provider_policy_version <> OLD.provider_policy_version
       OR NEW.max_attempts <> OLD.max_attempts
       OR NEW.poll_start_grace_seconds <> OLD.poll_start_grace_seconds
       OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'account inventory poll identity is immutable'
            USING ERRCODE = '23514';
    END IF;
    IF NOT (
        (OLD.status = 'pending' AND NEW.status IN ('running', 'abandoned'))
        OR (OLD.status = 'running' AND NEW.status IN ('retry_wait', 'finalized', 'abandoned'))
        OR (OLD.status = 'retry_wait' AND NEW.status IN ('running', 'abandoned'))
    ) THEN
        RAISE EXCEPTION 'invalid account inventory poll transition'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.status = 'abandoned' AND EXISTS (
        SELECT 1 FROM public.account_inventory_poll_provider_results AS result
        WHERE result.poll_run_id = NEW.poll_run_id
    ) THEN
        RAISE EXCEPTION 'abandoned account inventory poll cannot contain provider evidence'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.status = 'finalized' THEN
        SELECT policy.active_providers INTO expected_providers
        FROM public.provider_inventory_policy_versions AS policy
        WHERE policy.policy_version_id = NEW.provider_policy_version
          AND policy.node_type = NEW.node_type
          AND policy.driver_contract_version = NEW.driver_contract_version;
        SELECT array_agg(result.provider ORDER BY result.provider)
        INTO stored_providers
        FROM public.account_inventory_poll_provider_results AS result
        WHERE result.poll_run_id = NEW.poll_run_id;
        IF stored_providers IS DISTINCT FROM expected_providers THEN
            RAISE EXCEPTION 'finalized account inventory poll provider set is incomplete'
                USING ERRCODE = '23514';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER account_inventory_poll_runs_guard
BEFORE UPDATE OR DELETE ON account_inventory_poll_runs
FOR EACH ROW EXECUTE FUNCTION public.control_protect_account_inventory_poll_run();

-- +goose StatementBegin
CREATE FUNCTION public.control_protect_account_inventory_provider_result() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
DECLARE parent_status text;
BEGIN
    IF TG_OP IN ('UPDATE', 'DELETE') THEN
        RAISE EXCEPTION 'account inventory provider evidence is immutable'
            USING ERRCODE = '23514';
    END IF;
    SELECT run.status INTO parent_status
    FROM public.account_inventory_poll_runs AS run
    WHERE run.poll_run_id = NEW.poll_run_id
    FOR KEY SHARE;
    IF parent_status <> 'running' THEN
        RAISE EXCEPTION 'provider evidence requires a running poll'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER account_inventory_poll_provider_results_guard
BEFORE INSERT OR UPDATE OR DELETE ON account_inventory_poll_provider_results
FOR EACH ROW EXECUTE FUNCTION public.control_protect_account_inventory_provider_result();

-- +goose StatementBegin
CREATE FUNCTION public.control_schedule_account_inventory_poll_runs(
    poll_period_seconds integer,
    poll_start_grace_seconds integer,
    poll_max_attempts integer,
    schedule_limit integer
) RETURNS jsonb
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    database_now timestamptz := clock_timestamp();
    current_slot timestamptz;
    inconsistent_count bigint;
    eligible_count bigint;
    schedule_result jsonb;
BEGIN
    IF poll_period_seconds <> 300
       OR poll_start_grace_seconds < 1 OR poll_start_grace_seconds >= poll_period_seconds
       OR poll_max_attempts < 1 OR poll_max_attempts > 10
       OR schedule_limit < 1 OR schedule_limit > 1000 THEN
        RAISE EXCEPTION 'invalid account inventory poll scheduling policy'
            USING ERRCODE = '22023';
    END IF;
    current_slot := to_timestamp(
        floor(extract(epoch FROM database_now) / 300) * 300
    );
    IF current_slot + make_interval(secs => poll_start_grace_seconds) <= database_now THEN
        RETURN jsonb_build_object(
            'scheduled_at', current_slot,
            'eligible_count', 0,
            'created_count', 0
        );
    END IF;

    SELECT count(*) INTO inconsistent_count
    FROM public.relay_node_assets AS asset
    WHERE EXISTS (
        SELECT 1 FROM public.node_capabilities AS capability
        WHERE capability.instance_id = asset.instance_id
          AND capability.node_type = asset.node_type
          AND capability.driver_contract_version = asset.driver_contract_version
          AND capability.capability = 'management_account_inventory_read'
    )
      AND EXISTS (
        SELECT 1 FROM public.relay_node_inventory_monitoring_activations AS monitoring
        WHERE monitoring.instance_id = asset.instance_id
          AND monitoring.active_range @> current_slot
    )
      AND (
        (SELECT count(*) FROM public.provider_inventory_policy_activations AS activation
         WHERE activation.node_type = asset.node_type
           AND activation.driver_contract_version = asset.driver_contract_version
           AND activation.active_range @> current_slot) <> 1
        OR NOT EXISTS (
            SELECT 1
            FROM public.provider_inventory_policy_activations AS activation
            JOIN public.provider_inventory_policy_bindings AS binding
              ON binding.node_type = activation.node_type
             AND binding.driver_contract_version = activation.driver_contract_version
             AND binding.policy_version_id = activation.policy_version_id
            JOIN public.provider_inventory_policy_versions AS policy
              ON policy.policy_version_id = activation.policy_version_id
             AND policy.node_type = activation.node_type
             AND policy.driver_contract_version = activation.driver_contract_version
            WHERE activation.node_type = asset.node_type
              AND activation.driver_contract_version = asset.driver_contract_version
              AND activation.active_range @> current_slot
              AND cardinality(policy.active_providers) > 0
        )
      );
    IF inconsistent_count <> 0 THEN
        RAISE EXCEPTION 'account inventory poll eligibility is inconsistent'
            USING ERRCODE = '23514';
    END IF;

    SELECT count(DISTINCT asset.instance_id) INTO eligible_count
    FROM public.relay_node_assets AS asset
    JOIN public.node_capabilities AS capability
      ON capability.instance_id = asset.instance_id
     AND capability.node_type = asset.node_type
     AND capability.driver_contract_version = asset.driver_contract_version
     AND capability.capability = 'management_account_inventory_read'
    JOIN public.relay_node_inventory_monitoring_activations AS monitoring
      ON monitoring.instance_id = asset.instance_id
     AND monitoring.active_range @> current_slot
    JOIN public.provider_inventory_policy_activations AS activation
      ON activation.node_type = asset.node_type
     AND activation.driver_contract_version = asset.driver_contract_version
     AND activation.active_range @> current_slot
    JOIN public.provider_inventory_policy_bindings AS binding
      ON binding.node_type = activation.node_type
     AND binding.driver_contract_version = activation.driver_contract_version
     AND binding.policy_version_id = activation.policy_version_id
    JOIN public.provider_inventory_policy_versions AS policy
      ON policy.policy_version_id = activation.policy_version_id
     AND policy.node_type = activation.node_type
     AND policy.driver_contract_version = activation.driver_contract_version
     AND cardinality(policy.active_providers) > 0;
    IF eligible_count > schedule_limit THEN
        RAISE EXCEPTION 'account inventory poll capacity exceeded'
            USING ERRCODE = '22023';
    END IF;

    WITH eligible AS (
        SELECT asset.instance_id, asset.node_type, asset.driver_contract_version,
               activation.policy_version_id
        FROM public.relay_node_assets AS asset
        JOIN public.node_capabilities AS capability
          ON capability.instance_id = asset.instance_id
         AND capability.node_type = asset.node_type
         AND capability.driver_contract_version = asset.driver_contract_version
         AND capability.capability = 'management_account_inventory_read'
        JOIN public.relay_node_inventory_monitoring_activations AS monitoring
          ON monitoring.instance_id = asset.instance_id
         AND monitoring.active_range @> current_slot
        JOIN public.provider_inventory_policy_activations AS activation
          ON activation.node_type = asset.node_type
         AND activation.driver_contract_version = asset.driver_contract_version
         AND activation.active_range @> current_slot
        JOIN public.provider_inventory_policy_bindings AS binding
          ON binding.node_type = activation.node_type
         AND binding.driver_contract_version = activation.driver_contract_version
         AND binding.policy_version_id = activation.policy_version_id
        JOIN public.provider_inventory_policy_versions AS policy
          ON policy.policy_version_id = activation.policy_version_id
         AND policy.node_type = activation.node_type
         AND policy.driver_contract_version = activation.driver_contract_version
         AND cardinality(policy.active_providers) > 0
        ORDER BY EXISTS (
            SELECT 1 FROM public.account_inventory_poll_runs AS existing
            WHERE existing.instance_id = asset.instance_id
              AND existing.scheduled_at = current_slot
        ), asset.instance_id
        LIMIT schedule_limit
    ), inserted AS (
        INSERT INTO public.account_inventory_poll_runs (
            poll_run_id, instance_id, node_type, driver_contract_version,
            scheduled_at, provider_policy_version, max_attempts,
            poll_start_grace_seconds, created_at
        )
        SELECT gen_random_uuid(), eligible.instance_id, eligible.node_type,
               eligible.driver_contract_version, current_slot,
               eligible.policy_version_id, poll_max_attempts,
               poll_start_grace_seconds, database_now
        FROM eligible
        ON CONFLICT (instance_id, scheduled_at) DO NOTHING
        RETURNING 1
    )
    SELECT jsonb_build_object(
        'scheduled_at', current_slot,
        'eligible_count', eligible_count,
        'created_count', (SELECT count(*) FROM inserted)
    )
    INTO schedule_result;
    RETURN schedule_result;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_claim_account_inventory_poll_run(
    new_fencing_token uuid,
    lease_seconds integer
) RETURNS jsonb
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    database_now timestamptz := clock_timestamp();
    candidate_id uuid;
    claim_result jsonb;
BEGIN
    IF new_fencing_token IS NULL OR lease_seconds < 1 OR lease_seconds > 120 THEN
        RAISE EXCEPTION 'invalid account inventory poll claim policy'
            USING ERRCODE = '22023';
    END IF;
    SELECT run.poll_run_id INTO candidate_id
    FROM public.account_inventory_poll_runs AS run
    WHERE run.status IN ('pending', 'retry_wait')
      AND run.attempt_count < run.max_attempts
      AND run.scheduled_at + make_interval(secs => run.poll_start_grace_seconds) > database_now
    ORDER BY run.scheduled_at, run.instance_id
    FOR UPDATE SKIP LOCKED
    LIMIT 1;
    IF candidate_id IS NULL THEN
        RETURN NULL;
    END IF;

    UPDATE public.account_inventory_poll_runs AS run
    SET status = 'running',
        attempt_count = run.attempt_count + 1,
        first_started_at = coalesce(run.first_started_at, database_now),
        last_started_at = database_now,
        lease_expires_at = database_now + make_interval(secs => lease_seconds),
        lease_fencing_token = new_fencing_token,
        execution_reason = NULL
    WHERE run.poll_run_id = candidate_id;

    SELECT jsonb_build_object(
           'poll_run_id', run.poll_run_id,
           'instance_id', run.instance_id,
           'node_type', run.node_type,
           'driver_contract_version', run.driver_contract_version,
           'scheduled_at', run.scheduled_at,
           'provider_policy_version', run.provider_policy_version,
           'attempt_count', run.attempt_count,
           'max_attempts', run.max_attempts,
           'first_started_at', run.first_started_at,
           'last_started_at', run.last_started_at,
           'lease_expires_at', run.lease_expires_at,
           'lease_fencing_token', run.lease_fencing_token,
           'grace_remaining_milliseconds', greatest(0::bigint, floor(extract(epoch FROM (
               run.scheduled_at + make_interval(secs => run.poll_start_grace_seconds) - database_now
           )) * 1000)::bigint),
           'management_endpoint', asset.management_endpoint,
           'reader_secret_ref', asset.reader_secret_ref,
           'active_providers', policy.active_providers,
           'out_of_scope_providers', policy.out_of_scope_providers
    ) INTO claim_result
    FROM public.account_inventory_poll_runs AS run
    JOIN public.relay_node_assets AS asset
      ON asset.instance_id = run.instance_id
     AND asset.node_type = run.node_type
     AND asset.driver_contract_version = run.driver_contract_version
    JOIN public.provider_inventory_policy_versions AS policy
      ON policy.policy_version_id = run.provider_policy_version
     AND policy.node_type = run.node_type
     AND policy.driver_contract_version = run.driver_contract_version
    WHERE run.poll_run_id = candidate_id;
    RETURN claim_result;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_reconcile_account_inventory_poll_run()
RETURNS SETOF public.account_inventory_poll_runs
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    database_now timestamptz := clock_timestamp();
    candidate_id uuid;
BEGIN
    SELECT run.poll_run_id INTO candidate_id
    FROM public.account_inventory_poll_runs AS run
    WHERE (run.status IN ('pending', 'retry_wait')
           AND run.scheduled_at + make_interval(secs => run.poll_start_grace_seconds) <= database_now)
       OR (run.status = 'running' AND run.lease_expires_at <= database_now)
    ORDER BY run.scheduled_at, run.instance_id
    FOR UPDATE SKIP LOCKED
    LIMIT 1;
    IF candidate_id IS NULL THEN
        RETURN;
    END IF;

    RETURN QUERY
    UPDATE public.account_inventory_poll_runs AS run
    SET status = CASE
            WHEN run.status = 'running'
             AND run.scheduled_at + make_interval(secs => run.poll_start_grace_seconds) > database_now
             AND run.attempt_count < run.max_attempts THEN 'retry_wait'
            ELSE 'abandoned'
        END,
        lease_expires_at = NULL,
        lease_fencing_token = NULL,
        abandoned_at = CASE
            WHEN run.status = 'running'
             AND run.scheduled_at + make_interval(secs => run.poll_start_grace_seconds) > database_now
             AND run.attempt_count < run.max_attempts THEN NULL
            ELSE database_now
        END,
        execution_reason = CASE
            WHEN run.status = 'running'
             AND run.scheduled_at + make_interval(secs => run.poll_start_grace_seconds) > database_now
             AND run.attempt_count < run.max_attempts THEN 'lease_expired'
            WHEN run.attempt_count >= run.max_attempts THEN 'max_attempts_exhausted'
            ELSE 'poll_start_grace_expired'
        END
    WHERE run.poll_run_id = candidate_id
    RETURNING run.*;
END;
$$;
-- +goose StatementEnd

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
    provider_results jsonb
) RETURNS SETOF public.account_inventory_poll_runs
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    database_now timestamptz := clock_timestamp();
    run_record public.account_inventory_poll_runs%ROWTYPE;
    expected_providers text[];
    supplied_providers text[];
    provider_item jsonb;
    provider_name text;
    provider_identifiable integer;
    provider_missing integer;
    provider_duplicate integer;
    provider_identity_complete boolean;
    provider_snapshot_complete boolean;
    provider_degraded boolean;
    provider_reason text;
    summed_identifiable bigint := 0;
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

    IF jsonb_typeof(provider_results) <> 'array'
       OR jsonb_array_length(provider_results) <> cardinality(expected_providers) THEN
        RAISE EXCEPTION 'account inventory provider result set is incomplete'
            USING ERRCODE = '23514';
    END IF;
    SELECT array_agg(value->>'provider' ORDER BY value->>'provider')
    INTO supplied_providers
    FROM jsonb_array_elements(provider_results) AS item(value);
    IF supplied_providers IS DISTINCT FROM expected_providers THEN
        RAISE EXCEPTION 'account inventory provider result set does not match pinned policy'
            USING ERRCODE = '23514';
    END IF;

    FOR provider_item IN SELECT value FROM jsonb_array_elements(provider_results) AS item(value)
    LOOP
        IF jsonb_typeof(provider_item) <> 'object'
           OR NOT (provider_item ?& ARRAY[
               'provider','identifiable_count','missing_identity_count',
               'duplicate_identity_count','identity_complete','snapshot_complete',
               'degraded','reason'
           ])
           OR provider_item - ARRAY[
               'provider','identifiable_count','missing_identity_count',
               'duplicate_identity_count','identity_complete','snapshot_complete',
               'degraded','reason'
           ] <> '{}'::jsonb THEN
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
        INSERT INTO public.account_inventory_poll_provider_results (
            poll_run_id, provider, identifiable_count, missing_identity_count,
            duplicate_identity_count, identity_complete, snapshot_complete,
            degraded, reason
        ) VALUES (
            target_poll_run_id, provider_name, provider_identifiable, provider_missing,
            provider_duplicate, provider_identity_complete, provider_snapshot_complete,
            provider_degraded, provider_reason
        );
        summed_identifiable := summed_identifiable + provider_identifiable;
        all_snapshot_complete := all_snapshot_complete AND provider_snapshot_complete;
    END LOOP;

    IF summed_identifiable <> result_identifiable_record_count
       OR result_snapshot_complete <> (result_node_identity_complete AND all_snapshot_complete)
       OR result_source_record_count < result_identifiable_record_count
            + result_unidentified_record_count
            + result_unsupported_provider_count
            + result_out_of_scope_provider_count THEN
        RAISE EXCEPTION 'account inventory aggregate result is inconsistent'
            USING ERRCODE = '23514';
    END IF;

    RETURN QUERY
    UPDATE public.account_inventory_poll_runs AS run
    SET status = 'finalized', finalized_at = database_now, observed_at = database_now,
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
        node_version = result_node_version, node_commit = result_node_commit
    WHERE run.poll_run_id = target_poll_run_id
      AND run.status = 'running'
      AND run.lease_fencing_token = expected_fencing_token
      AND run.lease_expires_at > database_now
    RETURNING run.*;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'relay_control_runtime') THEN
        RAISE EXCEPTION 'database role relay_control_runtime must be provisioned before migration'
            USING ERRCODE = '42704';
    END IF;
END;
$$;
-- +goose StatementEnd

REVOKE ALL ON TABLE account_inventory_poll_runs,
    account_inventory_poll_provider_results
FROM PUBLIC, relay_control_runtime;
GRANT SELECT (
    poll_run_id, instance_id, node_type, driver_contract_version, scheduled_at,
    provider_policy_version, status, attempt_count, max_attempts,
    poll_start_grace_seconds, created_at,
    first_started_at, last_started_at, finalized_at, abandoned_at,
    execution_reason, observed_at, transport_success, response_shape_valid,
    contract_valid, inventory_mode, node_identity_complete, snapshot_complete,
    degraded, result, reason, source_record_count, identifiable_record_count,
    unidentified_record_count, unsupported_provider_count,
    out_of_scope_provider_count, node_version, node_commit
) ON account_inventory_poll_runs TO relay_control_runtime;
GRANT SELECT ON account_inventory_poll_provider_results TO relay_control_runtime;

REVOKE EXECUTE ON FUNCTION public.control_protect_account_inventory_poll_run() FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_protect_account_inventory_provider_result() FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_schedule_account_inventory_poll_runs(integer, integer, integer, integer) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_claim_account_inventory_poll_run(uuid, integer) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_reconcile_account_inventory_poll_run() FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_finalize_account_inventory_poll_run(
    uuid, uuid, boolean, boolean, boolean, text, boolean, boolean, boolean,
    text, text, integer, integer, integer, integer, integer, text, text, jsonb
) FROM PUBLIC;

GRANT EXECUTE ON FUNCTION public.control_schedule_account_inventory_poll_runs(integer, integer, integer, integer) TO relay_control_runtime;
GRANT EXECUTE ON FUNCTION public.control_claim_account_inventory_poll_run(uuid, integer) TO relay_control_runtime;
GRANT EXECUTE ON FUNCTION public.control_reconcile_account_inventory_poll_run() TO relay_control_runtime;
GRANT EXECUTE ON FUNCTION public.control_finalize_account_inventory_poll_run(
    uuid, uuid, boolean, boolean, boolean, text, boolean, boolean, boolean,
    text, text, integer, integer, integer, integer, integer, text, text, jsonb
) TO relay_control_runtime;

-- +goose Down
-- +goose StatementBegin
DO $$
DECLARE evidence_count bigint;
BEGIN
    LOCK TABLE account_inventory_poll_runs,
        account_inventory_poll_provider_results IN ACCESS EXCLUSIVE MODE NOWAIT;
    SELECT (SELECT count(*) FROM account_inventory_poll_runs)
         + (SELECT count(*) FROM account_inventory_poll_provider_results)
    INTO evidence_count;
    IF evidence_count <> 0 THEN
        RAISE EXCEPTION 'account inventory poll migration down requires empty evidence tables'
            USING ERRCODE = '55000';
    END IF;
END;
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION public.control_finalize_account_inventory_poll_run(
    uuid, uuid, boolean, boolean, boolean, text, boolean, boolean, boolean,
    text, text, integer, integer, integer, integer, integer, text, text, jsonb
) FROM relay_control_runtime;
REVOKE EXECUTE ON FUNCTION public.control_reconcile_account_inventory_poll_run() FROM relay_control_runtime;
REVOKE EXECUTE ON FUNCTION public.control_claim_account_inventory_poll_run(uuid, integer) FROM relay_control_runtime;
REVOKE EXECUTE ON FUNCTION public.control_schedule_account_inventory_poll_runs(integer, integer, integer, integer) FROM relay_control_runtime;
DROP FUNCTION public.control_finalize_account_inventory_poll_run(
    uuid, uuid, boolean, boolean, boolean, text, boolean, boolean, boolean,
    text, text, integer, integer, integer, integer, integer, text, text, jsonb
);
DROP FUNCTION public.control_reconcile_account_inventory_poll_run();
DROP FUNCTION public.control_claim_account_inventory_poll_run(uuid, integer);
DROP FUNCTION public.control_schedule_account_inventory_poll_runs(integer, integer, integer, integer);
DROP TRIGGER account_inventory_poll_provider_results_guard ON account_inventory_poll_provider_results;
DROP FUNCTION public.control_protect_account_inventory_provider_result();
DROP TRIGGER account_inventory_poll_runs_guard ON account_inventory_poll_runs;
DROP FUNCTION public.control_protect_account_inventory_poll_run();
DROP TABLE account_inventory_poll_provider_results;
DROP TABLE account_inventory_poll_runs;
