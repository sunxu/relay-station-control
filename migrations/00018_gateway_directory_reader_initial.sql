-- +goose Up

-- Keep the existing audit allowlist additive.  The conditional blocks make a
-- logical down/up cycle safe: the new action and its shape are intentionally
-- retained as historical schema.
ALTER TABLE public.audit_logs DROP CONSTRAINT audit_logs_category_valid;
ALTER TABLE public.audit_logs ADD CONSTRAINT audit_logs_category_valid CHECK (
    category IN ('bootstrap', 'administrator', 'password', 'mfa', 'session',
                 'reauthentication', 'authorization', 'rate_limit',
                 'account_inventory', 'account_inventory_history', 'relay_binding', 'asset')
);
ALTER TABLE public.audit_logs DROP CONSTRAINT audit_logs_action_valid;
ALTER TABLE public.audit_logs ADD CONSTRAINT audit_logs_action_valid CHECK (
    action IN (
        'bootstrap.start', 'bootstrap.complete', 'bootstrap.reset',
        'auth.login_password', 'auth.login_mfa', 'auth.logout',
        'auth.password_change', 'auth.mfa_enroll', 'auth.mfa_reset',
        'auth.recovery_code_use', 'auth.recovery_codes_regenerate',
        'session.create', 'session.revoke', 'auth.reauthenticate',
        'administrator.create', 'administrator.activate', 'administrator.disable',
        'administrator.activation_token_generate', 'authorization.check',
        'auth.rate_limit', 'auth.csrf', 'account_inventory.view',
        'account_inventory_history.summarized',
        'account_inventory_history.snapshot_delete_batch',
        'account_inventory_history.retention_delete_batch',
        'account_inventory_history.completed', 'account_inventory_history.failed',
        'relay_binding.bind', 'relay_binding.unbind', 'relay_binding.rebind',
        'asset.gateway_directory_reader_configured'
    )
);

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'audit_logs_asset_gateway_reader_shape') THEN
        ALTER TABLE public.audit_logs ADD CONSTRAINT audit_logs_asset_gateway_reader_shape CHECK (
            (category = 'asset') = (action = 'asset.gateway_directory_reader_configured')
            AND (
                category <> 'asset'
                OR (
                action = 'asset.gateway_directory_reader_configured'
                AND result = 'success'
                AND actor_admin_id IS NOT NULL
                AND target_admin_id IS NULL
                AND details ?& ARRAY['gateway_instance_id', 'reader_configured']
                AND details - ARRAY['gateway_instance_id', 'reader_configured'] = '{}'::jsonb
                AND jsonb_typeof(details->'gateway_instance_id') = 'string'
                AND (details->>'gateway_instance_id') ~
                    '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
                AND jsonb_typeof(details->'reader_configured') = 'boolean'
                AND (details->>'reader_configured')::boolean
                )
            )
        );
    END IF;
END;
$$;
-- +goose StatementEnd

-- Runtime already has generic audit INSERT privilege.  This trigger gives the
-- new action the same controlled-write gate used by history audit actions.
-- The gate is transaction-local and contains the generated request id.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_protect_asset_gateway_reader_audit()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
DECLARE
    audit_gate text := coalesce(current_setting('relay_control.asset_gateway_reader_audit_write', true), '');
BEGIN
    IF NEW.category <> 'asset' THEN
        RETURN NEW;
    END IF;
    IF current_user <> 'relay_control_migrator'
       OR NOT (
           pg_has_role(session_user, 'relay_control_asset_registrar', 'member')
           OR current_setting('role', true) = 'relay_control_asset_registrar'
       )
       OR (session_user = current_user AND current_setting('role', true) <> 'relay_control_asset_registrar')
       OR audit_gate <> NEW.request_id THEN
        RAISE EXCEPTION 'asset gateway reader audit requires controlled insertion' USING ERRCODE = '42501';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS audit_logs_asset_gateway_reader_guard ON public.audit_logs;
CREATE TRIGGER audit_logs_asset_gateway_reader_guard
BEFORE INSERT ON public.audit_logs
FOR EACH ROW EXECUTE FUNCTION public.control_protect_asset_gateway_reader_audit();

REVOKE EXECUTE ON FUNCTION public.control_protect_asset_gateway_reader_audit() FROM PUBLIC, relay_control_runtime, relay_control_asset_registrar;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_set_gateway_directory_reader_initial_v1(
    gateway_id uuid,
    reader_reference text,
    actor_admin_id uuid
) RETURNS text
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    gateway public.gateway_instances%ROWTYPE;
    request_id text;
BEGIN
    IF gateway_id IS NULL OR actor_admin_id IS NULL
       OR reader_reference IS NULL
       OR btrim(reader_reference) <> reader_reference
       OR char_length(reader_reference) = 0
       OR NOT public.control_valid_secret_reference(reader_reference) THEN
        RAISE EXCEPTION 'gateway reader configuration rejected' USING ERRCODE = '22023';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM public.control_admin_users AS admin
        WHERE admin.admin_id = actor_admin_id
          AND admin.role = 'super_admin'
          AND admin.status = 'enabled'
          AND admin.activated_at IS NOT NULL
          AND admin.disabled_at IS NULL
    ) THEN
        RAISE EXCEPTION 'gateway reader configuration actor is not enabled' USING ERRCODE = '42501';
    END IF;

    SELECT * INTO gateway FROM public.gateway_instances
    WHERE instance_id = gateway_id FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'gateway is not registered' USING ERRCODE = 'P0404';
    END IF;
    IF gateway.reader_secret_ref IS NOT NULL THEN
        IF gateway.reader_secret_ref = reader_reference THEN
            RETURN 'no_op';
        END IF;
        RAISE EXCEPTION 'gateway reader reference already configured' USING ERRCODE = '23505';
    END IF;
    IF EXISTS (SELECT 1 FROM public.gateway_directory_ingestion_runs WHERE gateway_instance_id = gateway_id)
       OR EXISTS (SELECT 1 FROM public.gateway_directory_snapshots WHERE gateway_instance_id = gateway_id)
       OR EXISTS (SELECT 1 FROM public.gateway_directory_snapshot_items AS item
                  JOIN public.gateway_directory_snapshots AS snapshot ON snapshot.snapshot_id = item.snapshot_id
                  WHERE snapshot.gateway_instance_id = gateway_id)
       OR EXISTS (SELECT 1 FROM public.gateway_directory_current_state WHERE gateway_instance_id = gateway_id)
       OR EXISTS (SELECT 1 FROM public.relay_node_gateway_account_bindings WHERE gateway_instance_id = gateway_id) THEN
        RAISE EXCEPTION 'gateway reader configuration requires empty directory history' USING ERRCODE = '23505';
    END IF;

    UPDATE public.gateway_instances
    SET reader_secret_ref = reader_reference, updated_at = clock_timestamp()
    WHERE instance_id = gateway_id AND reader_secret_ref IS NULL;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'gateway reader reference changed concurrently' USING ERRCODE = '40001';
    END IF;
    request_id := gen_random_uuid()::text;
    PERFORM set_config('relay_control.asset_gateway_reader_audit_write', request_id, true);
    INSERT INTO public.audit_logs (
        occurred_at, category, action, result, actor_admin_id, target_admin_id,
        request_id, details
    ) VALUES (
        clock_timestamp(), 'asset', 'asset.gateway_directory_reader_configured',
        'success', actor_admin_id, NULL, request_id,
        jsonb_build_object('gateway_instance_id', gateway_id::text, 'reader_configured', true)
    );
    PERFORM set_config('relay_control.asset_gateway_reader_audit_write', '', true);
    RETURN 'configured';
END;
$$;
-- +goose StatementEnd

ALTER FUNCTION public.control_set_gateway_directory_reader_initial_v1(uuid, text, uuid)
    OWNER TO relay_control_migrator;
REVOKE EXECUTE ON FUNCTION public.control_set_gateway_directory_reader_initial_v1(uuid, text, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_set_gateway_directory_reader_initial_v1(uuid, text, uuid)
    TO relay_control_asset_registrar;

-- The runtime may read the sensitive reference only for its own live,
-- correctly-fenced run.  No table SELECT privilege is granted by this entry
-- point.
-- +goose StatementBegin
CREATE FUNCTION public.control_query_gateway_directory_target_v1(
    target_run_id uuid,
    target_gateway_id uuid,
    target_fencing_token uuid
) RETURNS TABLE (
    instance_id uuid,
    management_endpoint text,
    reader_secret_ref text
)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
    SELECT gateway.instance_id, gateway.management_endpoint, gateway.reader_secret_ref
    FROM public.gateway_directory_ingestion_runs AS run
    JOIN public.gateway_instances AS gateway
      ON gateway.instance_id = run.gateway_instance_id
    WHERE run.ingestion_run_id = target_run_id
      AND run.gateway_instance_id = target_gateway_id
      AND run.lease_fencing_token = target_fencing_token
      AND run.status = 'running'
      AND run.lease_expires_at > statement_timestamp()
      AND gateway.reader_secret_ref IS NOT NULL
$$;
-- +goose StatementEnd

ALTER FUNCTION public.control_query_gateway_directory_target_v1(uuid, uuid, uuid)
    OWNER TO relay_control_migrator;
REVOKE EXECUTE ON FUNCTION public.control_query_gateway_directory_target_v1(uuid, uuid, uuid)
    FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_query_gateway_directory_target_v1(uuid, uuid, uuid)
    TO relay_control_runtime;

-- +goose Down
REVOKE EXECUTE ON FUNCTION public.control_set_gateway_directory_reader_initial_v1(uuid, text, uuid)
    FROM relay_control_asset_registrar, PUBLIC;
DROP FUNCTION public.control_set_gateway_directory_reader_initial_v1(uuid, text, uuid);
REVOKE EXECUTE ON FUNCTION public.control_query_gateway_directory_target_v1(uuid, uuid, uuid)
    FROM relay_control_runtime, PUBLIC;
DROP FUNCTION public.control_query_gateway_directory_target_v1(uuid, uuid, uuid);
