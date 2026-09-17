-- +goose Up

-- Round post-closeout corrective: admission must use current durable Node
-- evidence.  The helper is database-owned and intentionally not executable by
-- runtime callers; only the two controlled admission functions invoke it.
-- The caller locks relay_node_assets first and releases all locks before native
-- HTTP, so this function only observes the same short transaction snapshot.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_account_node_admission_failure_v1(
    admission_node_instance_id uuid,
    admission_account_key text
) RETURNS text
LANGUAGE plpgsql SECURITY DEFINER STABLE SET search_path = pg_catalog AS $$
DECLARE node public.relay_node_assets%ROWTYPE;
DECLARE provider_name text;
BEGIN
    SELECT * INTO node
    FROM public.relay_node_assets
    WHERE instance_id = admission_node_instance_id;
    IF NOT FOUND OR node.lifecycle_status IS DISTINCT FROM 'active' THEN
        RETURN 'node_retired';
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM public.relay_node_inventory_monitoring_activations monitoring
        WHERE monitoring.instance_id = admission_node_instance_id
          AND monitoring.cancelled_at IS NULL
          AND clock_timestamp() <@ monitoring.active_range
    ) THEN
        RETURN 'node_monitoring_ineligible';
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM public.node_capabilities capability
        WHERE capability.instance_id = admission_node_instance_id
          AND capability.node_type = node.node_type
          AND capability.driver_contract_version = node.driver_contract_version
          AND capability.capability = 'management_account_inventory_read'
    ) THEN
        RETURN 'node_management_unavailable';
    END IF;
    provider_name := split_part(admission_account_key, ':', 1);
    IF NOT EXISTS (
        SELECT 1
        FROM public.provider_inventory_policy_activations activation
        JOIN public.provider_inventory_policy_versions policy
          ON policy.policy_version_id = activation.policy_version_id
         AND policy.node_type = activation.node_type
         AND policy.driver_contract_version = activation.driver_contract_version
        WHERE activation.node_type = node.node_type
          AND activation.driver_contract_version = node.driver_contract_version
          AND activation.active_range @> clock_timestamp()
          AND provider_name = ANY(policy.active_providers)
    ) THEN
        RETURN 'unsupported_provider';
    END IF;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_account_node_admission_failure_v1(uuid,text) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_account_node_admission_failure_v1(uuid,text) FROM PUBLIC, relay_control_runtime, relay_control_asset_registrar;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_admit_account_dispatch_v1(
    admission_command_id uuid,
    admission_node_instance_id uuid,
    admission_account_key text,
    admission_request_id text
)
RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER VOLATILE SET search_path = pg_catalog AS $$
DECLARE current public.account_admin_operations%ROWTYPE;
DECLARE blocker uuid;
DECLARE failure_code text;
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended(
        'account-dispatch:' || admission_node_instance_id::text || ':' || admission_account_key, 0));
    SELECT * INTO current FROM public.account_admin_operations
    WHERE command_id=admission_command_id FOR UPDATE;
    IF NOT FOUND OR current.node_instance_id <> admission_node_instance_id
       OR current.account_key <> admission_account_key THEN
        RAISE EXCEPTION 'account operation identity mismatch' USING ERRCODE='23514';
    END IF;
    IF current.execution_state <> 'prepared' THEN
        RETURN FALSE;
    END IF;
    failure_code := public.control_account_node_admission_failure_v1(
        admission_node_instance_id, admission_account_key);
    IF failure_code IS NOT NULL THEN
        PERFORM public.control_terminalize_account_operation_failure_v1(
            admission_command_id, failure_code, admission_request_id);
        RETURN FALSE;
    END IF;
    SELECT command_id INTO blocker
    FROM public.account_admin_operations
    WHERE node_instance_id=admission_node_instance_id
      AND account_key=admission_account_key
      AND command_id<>admission_command_id
      AND execution_state IN ('dispatched','outcome_unknown')
      AND same_account_override_at IS NULL
    ORDER BY command_id FOR UPDATE LIMIT 1;
    IF blocker IS NOT NULL THEN
        PERFORM public.control_terminalize_account_operation_failure_v1(
            admission_command_id, 'account_operation_in_progress', admission_request_id);
        RETURN FALSE;
    END IF;
    UPDATE public.account_admin_operations
    SET execution_state='dispatched', dispatch_started_at=clock_timestamp(),
        updated_at=clock_timestamp()
    WHERE command_id=admission_command_id;
    RETURN TRUE;
END;
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_admit_account_dispatch_v1(uuid,uuid,text,text) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_admit_account_dispatch_v1(uuid,uuid,text,text) FROM PUBLIC, relay_control_asset_registrar;
GRANT EXECUTE ON FUNCTION public.control_admit_account_dispatch_v1(uuid,uuid,text,text) TO relay_control_runtime;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_admit_account_noop_v1(
    admission_command_id uuid,
    admission_node_instance_id uuid,
    admission_account_key text,
    admission_request_id text
)
RETURNS public.account_admin_operations
LANGUAGE plpgsql SECURITY DEFINER VOLATILE SET search_path = pg_catalog AS $$
DECLARE current public.account_admin_operations%ROWTYPE;
DECLARE blocker uuid;
DECLARE failure_code text;
DECLARE actor uuid;
DECLARE kind text;
DECLARE intent_hash bytea;
DECLARE secret_version smallint;
DECLARE response_body bytea;
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended(
        'account-dispatch:' || admission_node_instance_id::text || ':' || admission_account_key, 0));
    SELECT * INTO current FROM public.account_admin_operations
    WHERE command_id=admission_command_id FOR UPDATE;
    IF NOT FOUND OR current.node_instance_id <> admission_node_instance_id
       OR current.account_key <> admission_account_key THEN
        RAISE EXCEPTION 'account operation identity mismatch' USING ERRCODE='23514';
    END IF;
    IF current.execution_state <> 'prepared' THEN
        RETURN current;
    END IF;
    failure_code := public.control_account_node_admission_failure_v1(
        admission_node_instance_id, admission_account_key);
    IF failure_code IS NOT NULL THEN
        PERFORM public.control_terminalize_account_operation_failure_v1(
            admission_command_id, failure_code, admission_request_id);
        SELECT * INTO current FROM public.account_admin_operations WHERE command_id=admission_command_id;
        RETURN current;
    END IF;
    SELECT command_id INTO blocker
    FROM public.account_admin_operations
    WHERE node_instance_id=admission_node_instance_id
      AND account_key=admission_account_key
      AND command_id<>admission_command_id
      AND execution_state IN ('dispatched','outcome_unknown')
      AND same_account_override_at IS NULL
    ORDER BY command_id FOR UPDATE LIMIT 1;
    SELECT r.actor_admin_id,r.command_kind,r.canonical_intent_hash,
           r.secret_fingerprint_key_version
      INTO actor,kind,intent_hash,secret_version
    FROM public.admin_command_registry r
    WHERE r.command_id=admission_command_id;
    IF blocker IS NULL THEN
        UPDATE public.account_admin_operations
        SET execution_state='remote_noop',remote_result_code=NULL,updated_at=clock_timestamp()
        WHERE command_id=admission_command_id
        RETURNING * INTO current;
        response_body := convert_to(jsonb_build_object(
            'operation', public.control_account_operation_projection_v1(current))::text, 'UTF8');
        INSERT INTO public.audit_logs(audit_id,category,action,result,actor_admin_id,request_id,details)
        VALUES(gen_random_uuid(),'account_admin','account.operation_noop','success',actor,
               COALESCE(NULLIF(admission_request_id,''),'account-dispatch'),
               jsonb_build_object('command_id',admission_command_id::text));
        INSERT INTO public.account_admin_command_receipts(
            command_id,target_operation_command_id,actor_admin_id,command_domain,command_kind,
            intent_encoding_version,canonical_intent_hash,secret_fingerprint_key_version,
            http_status,content_type,response_body)
        VALUES(admission_command_id,admission_command_id,actor,'account_admin',kind,1,intent_hash,
               secret_version,200,'application/json',response_body);
    ELSE
        PERFORM public.control_terminalize_account_operation_failure_v1(
            admission_command_id, 'account_operation_in_progress', admission_request_id);
        SELECT * INTO current FROM public.account_admin_operations WHERE command_id=admission_command_id;
    END IF;
    RETURN current;
END;
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_admit_account_noop_v1(uuid,uuid,text,text) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_admit_account_noop_v1(uuid,uuid,text,text) FROM PUBLIC, relay_control_asset_registrar;
GRANT EXECUTE ON FUNCTION public.control_admit_account_noop_v1(uuid,uuid,text,text) TO relay_control_runtime;

-- +goose Down
DO $$ BEGIN RAISE EXCEPTION 'migration 50 is forward-only' USING ERRCODE='55000'; END $$;
