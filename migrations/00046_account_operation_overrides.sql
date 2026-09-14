-- +goose Up

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
    'node.health','node.connection_test','node.monitoring_enable','node.monitoring_disable',
    'account.operation_failed','account.operation_applied','account.operation_noop',
    'account.lifecycle_override','account.same_account_override'
));

-- The two functions below are deliberately explicit. They are the only
-- runtime write paths for the two frozen override fields.
-- +goose StatementBegin
CREATE FUNCTION public.control_apply_lifecycle_override_v1(
    override_command_id uuid, override_actor_admin_id uuid,
    target_operation_id uuid, override_reason text, audit_detail text,
    canonical_hash bytea, request_id text
) RETURNS text LANGUAGE plpgsql SECURITY DEFINER VOLATILE SET search_path = pg_catalog AS $$
DECLARE op public.account_admin_operations%ROWTYPE;
DECLARE actor uuid; kind text; intent_hash bytea; secret_version smallint;
DECLARE body bytea; code text;
BEGIN
    IF override_reason NOT IN ('process_restarted','node_stopped','risk_accepted') OR octet_length(canonical_hash) <> 32 THEN
        RAISE EXCEPTION 'invalid lifecycle override' USING ERRCODE='23514';
    END IF;
    PERFORM public.control_reserve_admin_command_v1(override_command_id, override_actor_admin_id,
        'account_admin'::text, 'account.lifecycle_override'::text, 1::smallint, canonical_hash, NULL::smallint);
    SELECT r.actor_admin_id,r.command_kind,r.canonical_intent_hash,r.secret_fingerprint_key_version
      INTO actor,kind,intent_hash,secret_version
      FROM public.admin_command_registry r WHERE r.command_id=override_command_id;
    SELECT * INTO op FROM public.account_admin_operations WHERE command_id=target_operation_id FOR UPDATE;
    IF NOT FOUND THEN
        code := 'operation_not_found';
        body := convert_to(jsonb_build_object('error',jsonb_build_object('code',code,'message',code))::text,'UTF8');
        INSERT INTO public.audit_logs(audit_id,category,action,result,actor_admin_id,request_id,details)
        VALUES(gen_random_uuid(),'account_admin','account.operation_failed','failure',actor,COALESCE(NULLIF(request_id,''),'account-override'),jsonb_build_object('command_id',override_command_id::text,'target_operation_command_id',target_operation_id::text,'error_code',code));
        INSERT INTO public.account_admin_command_receipts(command_id,target_operation_command_id,actor_admin_id,command_domain,command_kind,intent_encoding_version,canonical_intent_hash,secret_fingerprint_key_version,http_status,content_type,response_body)
        VALUES(override_command_id,NULL,actor,'account_admin',kind,1,intent_hash,secret_version,404,'application/json',body);
        RETURN code;
    END IF;
    IF op.execution_state NOT IN ('dispatched','outcome_unknown') THEN code := 'account_operation_not_overridable';
    ELSIF op.lifecycle_override_at IS NOT NULL THEN code := 'lifecycle_override_already_set';
    ELSE
        UPDATE public.account_admin_operations SET lifecycle_override_at=clock_timestamp(), lifecycle_override_by=actor, lifecycle_override_reason=override_reason, updated_at=clock_timestamp()
        WHERE command_id=target_operation_id RETURNING * INTO op;
        body := convert_to(jsonb_build_object('operation',public.control_account_operation_projection_v1(op))::text,'UTF8');
        INSERT INTO public.audit_logs(audit_id,category,action,result,actor_admin_id,request_id,details)
        VALUES(gen_random_uuid(),'account_admin','account.lifecycle_override','success',actor,COALESCE(NULLIF(request_id,''),'account-override'),jsonb_build_object('command_id',override_command_id::text,'target_operation_command_id',target_operation_id::text,'reason',override_reason,'detail',audit_detail));
        INSERT INTO public.account_admin_command_receipts(command_id,target_operation_command_id,actor_admin_id,command_domain,command_kind,intent_encoding_version,canonical_intent_hash,secret_fingerprint_key_version,http_status,content_type,response_body)
        VALUES(override_command_id,target_operation_id,actor,'account_admin',kind,1,intent_hash,secret_version,200,'application/json',body);
        RETURN 'ok';
    END IF;
    body := convert_to(jsonb_build_object('error',jsonb_build_object('code',code,'message',code),'operation',public.control_account_operation_projection_v1(op))::text,'UTF8');
    INSERT INTO public.audit_logs(audit_id,category,action,result,actor_admin_id,request_id,details)
    VALUES(gen_random_uuid(),'account_admin','account.operation_failed','failure',actor,COALESCE(NULLIF(request_id,''),'account-override'),jsonb_build_object('command_id',override_command_id::text,'target_operation_command_id',target_operation_id::text,'error_code',code));
    INSERT INTO public.account_admin_command_receipts(command_id,target_operation_command_id,actor_admin_id,command_domain,command_kind,intent_encoding_version,canonical_intent_hash,secret_fingerprint_key_version,http_status,content_type,response_body)
    VALUES(override_command_id,target_operation_id,actor,'account_admin',kind,1,intent_hash,secret_version,409,'application/json',body);
    RETURN code;
END;
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_apply_lifecycle_override_v1(uuid,uuid,uuid,text,text,bytea,text) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_apply_lifecycle_override_v1(uuid,uuid,uuid,text,text,bytea,text) FROM PUBLIC, relay_control_asset_registrar;
GRANT EXECUTE ON FUNCTION public.control_apply_lifecycle_override_v1(uuid,uuid,uuid,text,text,bytea,text) TO relay_control_runtime;

-- +goose StatementBegin
CREATE FUNCTION public.control_apply_same_account_override_v1(
    override_command_id uuid, override_actor_admin_id uuid,
    target_operation_id uuid, override_reason text, audit_detail text,
    canonical_hash bytea, request_id text
) RETURNS text LANGUAGE plpgsql SECURITY DEFINER VOLATILE SET search_path = pg_catalog AS $$
DECLARE op public.account_admin_operations%ROWTYPE;
DECLARE actor uuid; kind text; intent_hash bytea; secret_version smallint;
DECLARE body bytea; code text;
BEGIN
    IF override_reason NOT IN ('process_restarted','node_stopped','risk_accepted') OR octet_length(canonical_hash) <> 32 THEN
        RAISE EXCEPTION 'invalid same-account override' USING ERRCODE='23514';
    END IF;
    PERFORM public.control_reserve_admin_command_v1(override_command_id, override_actor_admin_id,
        'account_admin'::text, 'account.same_account_override'::text, 1::smallint, canonical_hash, NULL::smallint);
    SELECT r.actor_admin_id,r.command_kind,r.canonical_intent_hash,r.secret_fingerprint_key_version
      INTO actor,kind,intent_hash,secret_version FROM public.admin_command_registry r WHERE r.command_id=override_command_id;
    SELECT * INTO op FROM public.account_admin_operations WHERE command_id=target_operation_id FOR UPDATE;
    IF NOT FOUND THEN
        code := 'operation_not_found';
        body := convert_to(jsonb_build_object('error',jsonb_build_object('code',code,'message',code))::text,'UTF8');
        INSERT INTO public.audit_logs(audit_id,category,action,result,actor_admin_id,request_id,details) VALUES(gen_random_uuid(),'account_admin','account.operation_failed','failure',actor,COALESCE(NULLIF(request_id,''),'account-override'),jsonb_build_object('command_id',override_command_id::text,'target_operation_command_id',target_operation_id::text,'error_code',code));
        INSERT INTO public.account_admin_command_receipts(command_id,target_operation_command_id,actor_admin_id,command_domain,command_kind,intent_encoding_version,canonical_intent_hash,secret_fingerprint_key_version,http_status,content_type,response_body) VALUES(override_command_id,NULL,actor,'account_admin',kind,1,intent_hash,secret_version,404,'application/json',body);
        RETURN code;
    END IF;
    IF op.execution_state NOT IN ('dispatched','outcome_unknown') THEN code := 'account_operation_not_overridable';
    ELSIF op.same_account_override_at IS NOT NULL THEN code := 'same_account_override_already_set';
    ELSE
        UPDATE public.account_admin_operations SET same_account_override_at=clock_timestamp(), same_account_override_by=actor, same_account_override_reason=override_reason, updated_at=clock_timestamp()
        WHERE command_id=target_operation_id RETURNING * INTO op;
        body := convert_to(jsonb_build_object('operation',public.control_account_operation_projection_v1(op))::text,'UTF8');
        INSERT INTO public.audit_logs(audit_id,category,action,result,actor_admin_id,request_id,details) VALUES(gen_random_uuid(),'account_admin','account.same_account_override','success',actor,COALESCE(NULLIF(request_id,''),'account-override'),jsonb_build_object('command_id',override_command_id::text,'target_operation_command_id',target_operation_id::text,'reason',override_reason,'detail',audit_detail));
        INSERT INTO public.account_admin_command_receipts(command_id,target_operation_command_id,actor_admin_id,command_domain,command_kind,intent_encoding_version,canonical_intent_hash,secret_fingerprint_key_version,http_status,content_type,response_body) VALUES(override_command_id,target_operation_id,actor,'account_admin',kind,1,intent_hash,secret_version,200,'application/json',body);
        RETURN 'ok';
    END IF;
    body := convert_to(jsonb_build_object('error',jsonb_build_object('code',code,'message',code),'operation',public.control_account_operation_projection_v1(op))::text,'UTF8');
    INSERT INTO public.audit_logs(audit_id,category,action,result,actor_admin_id,request_id,details) VALUES(gen_random_uuid(),'account_admin','account.operation_failed','failure',actor,COALESCE(NULLIF(request_id,''),'account-override'),jsonb_build_object('command_id',override_command_id::text,'target_operation_command_id',target_operation_id::text,'error_code',code));
    INSERT INTO public.account_admin_command_receipts(command_id,target_operation_command_id,actor_admin_id,command_domain,command_kind,intent_encoding_version,canonical_intent_hash,secret_fingerprint_key_version,http_status,content_type,response_body) VALUES(override_command_id,target_operation_id,actor,'account_admin',kind,1,intent_hash,secret_version,409,'application/json',body);
    RETURN code;
END;
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_apply_same_account_override_v1(uuid,uuid,uuid,text,text,bytea,text) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_apply_same_account_override_v1(uuid,uuid,uuid,text,text,bytea,text) FROM PUBLIC, relay_control_asset_registrar;
GRANT EXECUTE ON FUNCTION public.control_apply_same_account_override_v1(uuid,uuid,uuid,text,text,bytea,text) TO relay_control_runtime;

-- +goose Down
DO $$ BEGIN RAISE EXCEPTION 'migration 46 is forward-only' USING ERRCODE='55000'; END $$;
