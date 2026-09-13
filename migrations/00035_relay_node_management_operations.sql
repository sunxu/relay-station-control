-- +goose Up

ALTER TABLE public.relay_node_inventory_monitoring_activations
    DROP CONSTRAINT node_monitoring_activation_reason_fixed,
    ADD CONSTRAINT node_monitoring_activation_reason_fixed CHECK (
        reason IN ('deployment_enable','scheduled_enable','reconciliation','administrator_enable')
    ),
    DROP CONSTRAINT relay_node_monitoring_cancellation_shape,
    ADD CONSTRAINT relay_node_monitoring_cancellation_shape CHECK (
        (cancelled_at IS NULL AND cancelled_by IS NULL AND cancel_reason IS NULL)
        OR
        (cancelled_at IS NOT NULL AND cancelled_by IS NOT NULL
         AND cancel_reason IN ('node_retired','node_replaced','administrator_disable')
         AND cancelled_at <= effective_from)
    ),
    DROP CONSTRAINT node_monitoring_activation_end_metadata_valid,
    ADD CONSTRAINT node_monitoring_activation_end_metadata_valid CHECK (
        (effective_to IS NULL AND end_reason IS NULL AND end_actor IS NULL AND end_recorded_at IS NULL)
        OR
        (effective_to IS NOT NULL AND end_reason IN (
            'deployment_disable','scheduled_disable','reconciliation',
            'administrator_disable','node_retired','node_replaced')
         AND end_actor IS NOT NULL AND octet_length(end_actor) BETWEEN 1 AND 128
         AND end_actor ~ '^[A-Za-z0-9][A-Za-z0-9._@:-]*$'
         AND end_recorded_at IS NOT NULL AND end_recorded_at >= created_at)
    );

ALTER TABLE public.audit_logs DROP CONSTRAINT audit_logs_action_valid;
ALTER TABLE public.audit_logs ADD CONSTRAINT audit_logs_action_valid CHECK (action IN (
    'bootstrap.start','bootstrap.complete','bootstrap.reset','auth.login_password',
    'auth.login_mfa','auth.logout','auth.password_change','auth.mfa_enroll','auth.mfa_reset',
    'auth.recovery_code_use','auth.recovery_codes_regenerate','session.create','session.revoke',
    'auth.reauthenticate','administrator.create','administrator.activate','administrator.disable',
    'administrator.activation_token_generate','authorization.check','auth.rate_limit','auth.csrf',
    'account_inventory.view','account_inventory_history.summarized',
    'account_inventory_history.snapshot_delete_batch','account_inventory_history.retention_delete_batch',
    'account_inventory_history.completed','account_inventory_history.failed',
    'relay_binding.bind','relay_binding.unbind','relay_binding.rebind',
    'asset.gateway_directory_reader_configured','gateway.register','gateway.edit','gateway.retire',
    'gateway.replace','gateway.health','gateway.connection_test',
    'node.register','node.edit','node.retire','node.replace',
    'node.health','node.connection_test','node.monitoring_enable','node.monitoring_disable'));

-- Administrator Disable is also allowed to shorten an already planned close;
-- identity and schedule history remain protected by the Stage 2 guard.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_protect_node_monitoring_cancellation()
RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
    IF TG_OP IN ('DELETE','TRUNCATE') THEN
        RAISE EXCEPTION 'node monitoring history is immutable' USING ERRCODE='42501';
    END IF;
    IF NEW.monitoring_activation_id IS DISTINCT FROM OLD.monitoring_activation_id
       OR NEW.instance_id IS DISTINCT FROM OLD.instance_id
       OR NEW.effective_from IS DISTINCT FROM OLD.effective_from
       OR NEW.reason IS DISTINCT FROM OLD.reason
       OR NEW.actor IS DISTINCT FROM OLD.actor
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'node monitoring identity is immutable' USING ERRCODE='23514';
    END IF;
    IF OLD.cancelled_at IS NOT NULL THEN
        RAISE EXCEPTION 'cancelled node monitoring history is immutable' USING ERRCODE='23514';
    END IF;
    IF OLD.effective_to IS NOT NULL AND ROW(
        NEW.effective_to, NEW.end_reason, NEW.end_actor, NEW.end_recorded_at
    ) IS DISTINCT FROM ROW(
        OLD.effective_to, OLD.end_reason, OLD.end_actor, OLD.end_recorded_at
    ) AND NOT (
        NEW.effective_to < OLD.effective_to
        AND NEW.end_reason IN ('administrator_disable', 'node_retired', 'node_replaced')
        AND NEW.end_actor IS NOT NULL
        AND NEW.end_recorded_at = NEW.effective_to
    ) THEN
        RAISE EXCEPTION 'closed node monitoring history is immutable' USING ERRCODE='23514';
    END IF;
    IF NEW.cancelled_at IS NOT NULL AND (
        NEW.effective_to IS DISTINCT FROM OLD.effective_to
        OR NEW.end_reason IS DISTINCT FROM OLD.end_reason
        OR NEW.end_actor IS DISTINCT FROM OLD.end_actor
        OR NEW.end_recorded_at IS DISTINCT FROM OLD.end_recorded_at
    ) THEN
        RAISE EXCEPTION 'node monitoring cancellation cannot rewrite schedule history' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
ALTER FUNCTION public.control_protect_node_monitoring_cancellation() OWNER TO relay_control_migrator;

CREATE UNIQUE INDEX asset_admin_command_receipts_node_disable_fence_idx
ON public.asset_admin_command_receipts
    ((sanitized_result->>'instance_id'), committed_at DESC)
    INCLUDE (command_id)
WHERE command_kind = 'node.monitoring_disable';

-- A Disable receipt is an immutable operational fence.  Serialize direct
-- inserts by Node and reject non-finite or non-increasing timestamps.
-- +goose StatementBegin
CREATE FUNCTION public.control_validate_node_disable_receipt()
RETURNS trigger LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE
    instance_text text;
    previous_committed_at timestamptz;
BEGIN
    IF NEW.command_kind <> 'node.monitoring_disable' THEN RETURN NEW; END IF;
    instance_text := NEW.sanitized_result->>'instance_id';
    IF instance_text IS NULL
       OR instance_text !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
       OR instance_text <> lower(instance_text)
       OR NEW.secret_fingerprint_key_version IS NOT NULL
       OR NOT isfinite(NEW.committed_at) THEN
        RAISE EXCEPTION 'node monitoring disable receipt shape is invalid' USING ERRCODE='23514';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM public.relay_node_assets
        WHERE instance_id = instance_text::uuid
    ) THEN
        RAISE EXCEPTION 'node monitoring disable receipt target is not registered' USING ERRCODE='23514';
    END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended('node-disable-fence:' || instance_text, 0));
    SELECT max(receipt.committed_at) INTO previous_committed_at
    FROM public.asset_admin_command_receipts AS receipt
    WHERE receipt.command_kind='node.monitoring_disable'
      AND receipt.sanitized_result->>'instance_id'=instance_text;
    IF previous_committed_at IS NOT NULL
       AND (NOT isfinite(previous_committed_at) OR NEW.committed_at <= previous_committed_at) THEN
        RAISE EXCEPTION 'node monitoring disable receipt committed_at must increase strictly' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_validate_node_disable_receipt() OWNER TO relay_control_migrator;
REVOKE EXECUTE ON FUNCTION public.control_validate_node_disable_receipt() FROM PUBLIC;
DROP TRIGGER IF EXISTS asset_admin_command_receipts_node_disable_guard ON public.asset_admin_command_receipts;
CREATE TRIGGER asset_admin_command_receipts_node_disable_guard
BEFORE INSERT ON public.asset_admin_command_receipts
FOR EACH ROW EXECUTE FUNCTION public.control_validate_node_disable_receipt();

-- Operational writers form their intent before the write transaction.  This
-- narrow read surface exposes only the latest immutable Disable fence token;
-- the write function re-reads it after acquiring the Node lock.
-- +goose StatementBegin
CREATE FUNCTION public.control_latest_node_disable_fence_v1(target_instance_id uuid)
RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER STABLE SET search_path = pg_catalog AS $$
DECLARE
    effective_role text := NULLIF(current_setting('role', true), 'none');
    is_runtime boolean;
    is_registrar boolean;
    latest_fence uuid;
BEGIN
    effective_role := COALESCE(effective_role, session_user);
    is_runtime := coalesce(pg_has_role(effective_role, 'relay_control_runtime', 'member'), false);
    is_registrar := coalesce(pg_has_role(effective_role, 'relay_control_asset_registrar', 'member'), false);
    IF is_runtime = is_registrar THEN
        RAISE EXCEPTION 'node monitoring fence lookup role is not permitted' USING ERRCODE = '42501';
    END IF;
    SELECT receipt.command_id INTO latest_fence
    FROM public.asset_admin_command_receipts AS receipt
    WHERE receipt.command_kind = 'node.monitoring_disable'
      AND receipt.sanitized_result->>'instance_id' = target_instance_id::text
    ORDER BY receipt.committed_at DESC
    LIMIT 1;
    RETURN latest_fence;
END;
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_latest_node_disable_fence_v1(uuid) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_latest_node_disable_fence_v1(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_latest_node_disable_fence_v1(uuid)
    TO relay_control_runtime, relay_control_asset_registrar;

DROP FUNCTION public.control_set_node_inventory_monitoring(uuid, boolean, timestamptz, text, text);

-- +goose StatementBegin
CREATE FUNCTION public.control_set_node_inventory_monitoring(
    monitored_instance_id uuid,
    monitoring_enabled boolean,
    requested_effective_at timestamptz,
    monitoring_reason text,
    monitoring_actor text,
    expected_disable_fence uuid
) RETURNS jsonb
LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = pg_catalog AS $$
DECLARE
    effective_role text := NULLIF(current_setting('role', true), 'none');
    boundary timestamptz;
    database_now timestamptz;
    activation_id uuid;
    existing public.relay_node_inventory_monitoring_activations%ROWTYPE;
    latest_disable_fence uuid;
    node_active boolean;
    administrator_operation boolean := monitoring_reason IN ('administrator_enable','administrator_disable');
    is_runtime boolean;
    is_registrar boolean;
    closed_count integer := 0;
    cancelled_count integer := 0;
BEGIN
    effective_role := COALESCE(effective_role, session_user);
    is_runtime := coalesce(pg_has_role(effective_role, 'relay_control_runtime', 'member'), false);
    is_registrar := coalesce(pg_has_role(effective_role, 'relay_control_asset_registrar', 'member'), false);
    IF is_runtime = is_registrar THEN
        RAISE EXCEPTION 'node monitoring writer role is not permitted' USING ERRCODE = '42501';
    END IF;
    IF is_runtime AND monitoring_reason NOT IN ('administrator_enable','administrator_disable') THEN
        RAISE EXCEPTION 'node monitoring reason is not permitted for runtime role' USING ERRCODE = '42501';
    END IF;
    IF is_registrar AND monitoring_reason NOT IN (
        'deployment_enable','scheduled_enable','deployment_disable',
        'scheduled_disable','reconciliation'
    ) THEN
        RAISE EXCEPTION 'node monitoring reason is not permitted for registrar role' USING ERRCODE = '42501';
    END IF;
    IF monitored_instance_id IS NULL OR monitoring_enabled IS NULL
       OR monitoring_reason IS NULL OR monitoring_actor IS NULL
       OR octet_length(monitoring_actor) NOT BETWEEN 1 AND 128
       OR monitoring_actor !~ '^[A-Za-z0-9][A-Za-z0-9._@:-]*$'
       OR (monitoring_enabled AND monitoring_reason NOT IN
           ('deployment_enable','scheduled_enable','reconciliation','administrator_enable'))
       OR (NOT monitoring_enabled AND monitoring_reason NOT IN
           ('deployment_disable','scheduled_disable','reconciliation','administrator_disable'))
       OR (administrator_operation AND requested_effective_at IS NOT NULL) THEN
        RAISE EXCEPTION 'monitoring activation metadata is invalid' USING ERRCODE='22023';
    END IF;
    SELECT lifecycle_status='active' INTO node_active
    FROM public.relay_node_assets WHERE instance_id=monitored_instance_id FOR UPDATE;
    IF NOT FOUND THEN RAISE EXCEPTION 'monitoring target is not registered' USING ERRCODE='23503'; END IF;
    IF NOT node_active THEN RAISE EXCEPTION 'monitoring target is retired' USING ERRCODE='23514'; END IF;

    -- The next SQL command after Node FOR UPDATE is the fresh READ COMMITTED
    -- fence lookup.  A Disable committed while this function waited is seen.
    SELECT receipt.command_id INTO latest_disable_fence
    FROM public.asset_admin_command_receipts AS receipt
    WHERE receipt.command_kind='node.monitoring_disable'
      AND receipt.sanitized_result->>'instance_id'=monitored_instance_id::text
    ORDER BY receipt.committed_at DESC LIMIT 1;
    IF latest_disable_fence IS DISTINCT FROM expected_disable_fence THEN
        RAISE EXCEPTION 'monitoring disable fence changed' USING ERRCODE='55000';
    END IF;

    PERFORM monitoring_activation_id
    FROM public.relay_node_inventory_monitoring_activations
    WHERE instance_id=monitored_instance_id
    ORDER BY effective_from, monitoring_activation_id FOR UPDATE;
    database_now := clock_timestamp();
    boundary := COALESCE(requested_effective_at, database_now);
    IF requested_effective_at IS NOT NULL AND boundary < database_now THEN
        RAISE EXCEPTION 'monitoring activation cannot be backfilled' USING ERRCODE='22023';
    END IF;
    SELECT * INTO existing
    FROM public.relay_node_inventory_monitoring_activations
    WHERE instance_id=monitored_instance_id AND cancelled_at IS NULL
      AND boundary <@ active_range
    ORDER BY effective_from, monitoring_activation_id LIMIT 1;

    IF monitoring_enabled THEN
        IF administrator_operation AND EXISTS (
            SELECT 1 FROM public.relay_node_inventory_monitoring_activations
            WHERE instance_id=monitored_instance_id AND cancelled_at IS NULL AND effective_from > boundary
        ) THEN
            RAISE EXCEPTION 'future monitoring activation conflicts with immediate enable' USING ERRCODE='23505';
        END IF;
        IF FOUND THEN
            RETURN jsonb_build_object(
                'activation_id', existing.monitoring_activation_id,
                'boundary', boundary,
                'closed_count', 0,
                'cancelled_count', 0,
                'current_changed', false);
        END IF;
        activation_id := gen_random_uuid();
        INSERT INTO public.relay_node_inventory_monitoring_activations(
            monitoring_activation_id,instance_id,effective_from,reason,actor,created_at)
        VALUES(activation_id,monitored_instance_id,boundary,monitoring_reason,
               monitoring_actor,LEAST(clock_timestamp(),boundary));
        IF boundary <= database_now THEN
            UPDATE public.asset_registry_generations SET node_generation=node_generation+1
            WHERE singleton_id=1 AND node_generation<9223372036854775807;
            IF NOT FOUND THEN RAISE EXCEPTION 'node generation exhausted' USING ERRCODE='22003'; END IF;
        END IF;
        RETURN jsonb_build_object(
            'activation_id', activation_id,
            'boundary', boundary,
            'closed_count', 0,
            'cancelled_count', 0,
            'current_changed', boundary <= database_now);
    END IF;

    IF administrator_operation THEN
        IF FOUND AND existing.effective_from=boundary THEN
            RAISE EXCEPTION 'monitoring activation boundary conflicts' USING ERRCODE='23P01';
        END IF;
        IF FOUND THEN
            UPDATE public.relay_node_inventory_monitoring_activations
            SET effective_to=boundary,end_reason='administrator_disable',
                end_actor=monitoring_actor,end_recorded_at=boundary
            WHERE monitoring_activation_id=existing.monitoring_activation_id;
            UPDATE public.asset_registry_generations SET node_generation=node_generation+1
            WHERE singleton_id=1 AND node_generation<9223372036854775807;
            IF NOT FOUND THEN RAISE EXCEPTION 'node generation exhausted' USING ERRCODE='22003'; END IF;
            closed_count := 1;
        END IF;
        UPDATE public.relay_node_inventory_monitoring_activations
        SET cancelled_at=boundary,cancelled_by=monitoring_actor::uuid,cancel_reason='administrator_disable'
        WHERE instance_id=monitored_instance_id AND cancelled_at IS NULL AND effective_from > boundary;
        GET DIAGNOSTICS cancelled_count = ROW_COUNT;
        RETURN jsonb_build_object(
            'activation_id', existing.monitoring_activation_id,
            'boundary', boundary,
            'closed_count', closed_count,
            'cancelled_count', cancelled_count,
            'current_changed', closed_count = 1);
    END IF;

    IF NOT FOUND THEN
        SELECT * INTO existing
        FROM public.relay_node_inventory_monitoring_activations
        WHERE instance_id=monitored_instance_id AND effective_to=boundary
        ORDER BY end_recorded_at DESC LIMIT 1;
        IF FOUND THEN
            IF existing.end_reason IS DISTINCT FROM monitoring_reason OR existing.end_actor IS DISTINCT FROM monitoring_actor THEN
                RAISE EXCEPTION 'monitoring deactivation conflicts with existing boundary' USING ERRCODE='23505';
            END IF;
            RETURN jsonb_build_object(
                'activation_id', existing.monitoring_activation_id,
                'boundary', boundary,
                'closed_count', 0,
                'cancelled_count', 0,
                'current_changed', false);
        END IF;
        RETURN jsonb_build_object(
            'activation_id', NULL,
            'boundary', boundary,
            'closed_count', 0,
            'cancelled_count', 0,
            'current_changed', false);
    END IF;
    IF existing.effective_from=boundary THEN RAISE EXCEPTION 'monitoring activation boundary conflicts' USING ERRCODE='23P01'; END IF;
    UPDATE public.relay_node_inventory_monitoring_activations
    SET effective_to=boundary,end_reason=monitoring_reason,end_actor=monitoring_actor,end_recorded_at=clock_timestamp()
    WHERE monitoring_activation_id=existing.monitoring_activation_id;
    IF boundary <= database_now THEN
        UPDATE public.asset_registry_generations SET node_generation=node_generation+1
        WHERE singleton_id=1 AND node_generation<9223372036854775807;
        IF NOT FOUND THEN RAISE EXCEPTION 'node generation exhausted' USING ERRCODE='22003'; END IF;
    END IF;
    RETURN jsonb_build_object(
        'activation_id', existing.monitoring_activation_id,
        'boundary', boundary,
        'closed_count', CASE WHEN boundary <= database_now THEN 1 ELSE 0 END,
        'cancelled_count', 0,
        'current_changed', boundary <= database_now);
END;
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_set_node_inventory_monitoring(uuid,boolean,timestamptz,text,text,uuid)
    OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_set_node_inventory_monitoring(uuid,boolean,timestamptz,text,text,uuid)
    FROM PUBLIC, relay_control_runtime, relay_control_asset_registrar;
GRANT EXECUTE ON FUNCTION public.control_set_node_inventory_monitoring(uuid,boolean,timestamptz,text,text,uuid)
    TO relay_control_runtime, relay_control_asset_registrar;

REVOKE INSERT, DELETE, TRUNCATE
    ON public.relay_node_inventory_monitoring_activations
    FROM PUBLIC, relay_control_runtime, relay_control_asset_registrar;
REVOKE UPDATE
    ON public.relay_node_inventory_monitoring_activations
    FROM PUBLIC, relay_control_asset_registrar;
GRANT UPDATE (
    effective_to, end_reason, end_actor, end_recorded_at,
    cancelled_at, cancelled_by, cancel_reason
) ON public.relay_node_inventory_monitoring_activations TO relay_control_runtime;
GRANT SELECT ON public.relay_node_inventory_monitoring_activations TO relay_control_runtime;

-- The product probe authorization reads one immutable target snapshot and
-- releases the database transaction before any network operation.
CREATE FUNCTION public.control_authorize_node_probe_v1(target_instance_id uuid)
RETURNS TABLE(
    instance_id uuid,
    lifecycle_status text,
    node_type text,
    driver_contract_version text,
    management_endpoint text,
    capabilities text[]
)
LANGUAGE sql SECURITY DEFINER STABLE SET search_path = pg_catalog AS $$
    SELECT asset.instance_id,
           asset.lifecycle_status,
           asset.node_type,
           asset.driver_contract_version,
           asset.management_endpoint,
           COALESCE(array_agg(capability.capability ORDER BY capability.capability)
               FILTER (WHERE capability.capability IS NOT NULL), ARRAY[]::text[])
    FROM public.relay_node_assets AS asset
    LEFT JOIN public.node_capabilities AS capability
      ON capability.instance_id = asset.instance_id
     AND capability.node_type = asset.node_type
     AND capability.driver_contract_version = asset.driver_contract_version
    WHERE asset.instance_id = target_instance_id
    GROUP BY asset.instance_id, asset.lifecycle_status, asset.node_type,
             asset.driver_contract_version, asset.management_endpoint
$$;
ALTER FUNCTION public.control_authorize_node_probe_v1(uuid) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_authorize_node_probe_v1(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_authorize_node_probe_v1(uuid) TO relay_control_runtime;

-- +goose Down
-- Forward-only: receipt fence and monitoring history are retained on rollback.
