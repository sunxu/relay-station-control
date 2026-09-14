-- +goose Up

-- Migration 46 exposed the canonical hash as an input to the SECURITY DEFINER
-- functions.  Remove that bypass and make the database derive the exact
-- frozen v1 override intent before reserving the global command.
DROP FUNCTION public.control_apply_lifecycle_override_v1(uuid,uuid,uuid,text,text,bytea,text);
DROP FUNCTION public.control_apply_same_account_override_v1(uuid,uuid,uuid,text,text,bytea,text);

-- Build the fixed-order compact JSON array without PostgreSQL's pretty
-- delimiter spaces.  Each scalar is quoted by PostgreSQL's JSON encoder, so
-- commas and control characters in the optional detail remain data, not
-- delimiters.  The returned bytes are the exact bytes hashed for identity.
-- +goose StatementBegin
CREATE FUNCTION public.control_account_override_intent_v1(
    intent_version text, override_kind text, target_operation_id uuid,
    override_reason text, override_confirmation text, audit_detail text
) RETURNS bytea LANGUAGE sql IMMUTABLE SET search_path = pg_catalog AS $$
    SELECT convert_to(
        '[' || to_json(intent_version)::text || ',' ||
        to_json(override_kind)::text || ',' ||
        to_json(target_operation_id::text)::text || ',' ||
        to_json(override_reason)::text || ',' ||
        to_json(override_confirmation)::text || ',' ||
        CASE WHEN audit_detail IS NULL THEN 'null' ELSE to_json(audit_detail)::text END ||
        ']', 'UTF8')
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_account_override_intent_v1(text,text,uuid,text,text,text) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_account_override_intent_v1(text,text,uuid,text,text,text) FROM PUBLIC, relay_control_asset_registrar, relay_control_runtime;

-- +goose StatementBegin
CREATE FUNCTION public.control_apply_account_override_v2(
    override_kind text, override_confirmation text,
    override_command_id uuid, override_actor_admin_id uuid,
    target_operation_id uuid, override_reason text,
    audit_detail text, request_id text
) RETURNS text LANGUAGE plpgsql SECURITY DEFINER VOLATILE SET search_path = pg_catalog AS $$
DECLARE op public.account_admin_operations%ROWTYPE;
DECLARE actor uuid; kind text; intent_hash bytea; secret_version smallint;
DECLARE body bytea; code text; detail text;
BEGIN
    IF override_kind NOT IN ('account.lifecycle_override','account.same_account_override')
       OR override_confirmation NOT IN ('OVERRIDE UNKNOWN OPERATION LIFECYCLE BLOCK','OVERRIDE UNKNOWN OPERATION SAME-ACCOUNT BLOCK')
       OR ((override_kind='account.lifecycle_override') <> (override_confirmation='OVERRIDE UNKNOWN OPERATION LIFECYCLE BLOCK'))
       OR override_reason NOT IN ('process_restarted','node_stopped','risk_accepted')
       OR (NULLIF(audit_detail, '') IS NOT NULL AND (octet_length(audit_detail) < 1 OR octet_length(audit_detail) > 512)) THEN
        RAISE EXCEPTION 'invalid account override' USING ERRCODE='23514';
    END IF;
    detail := NULLIF(audit_detail, '');
    intent_hash := sha256(public.control_account_override_intent_v1(
        'account-intent-v1', override_kind, target_operation_id,
        override_reason, override_confirmation, detail));
    PERFORM public.control_reserve_admin_command_v1(
        override_command_id, override_actor_admin_id, 'account_admin'::text,
        override_kind, 1::smallint, intent_hash, NULL::smallint);
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
    ELSIF (override_kind='account.lifecycle_override' AND op.lifecycle_override_at IS NOT NULL)
       OR (override_kind='account.same_account_override' AND op.same_account_override_at IS NOT NULL) THEN
        code := CASE WHEN override_kind='account.lifecycle_override' THEN 'lifecycle_override_already_set' ELSE 'same_account_override_already_set' END;
    ELSE
        IF override_kind='account.lifecycle_override' THEN
            UPDATE public.account_admin_operations SET lifecycle_override_at=clock_timestamp(), lifecycle_override_by=actor, lifecycle_override_reason=override_reason, updated_at=clock_timestamp()
            WHERE command_id=target_operation_id RETURNING * INTO op;
        ELSE
            UPDATE public.account_admin_operations SET same_account_override_at=clock_timestamp(), same_account_override_by=actor, same_account_override_reason=override_reason, updated_at=clock_timestamp()
            WHERE command_id=target_operation_id RETURNING * INTO op;
        END IF;
        body := convert_to(jsonb_build_object('operation',public.control_account_operation_projection_v1(op))::text,'UTF8');
        INSERT INTO public.audit_logs(audit_id,category,action,result,actor_admin_id,request_id,details)
        VALUES(gen_random_uuid(),'account_admin',override_kind,'success',actor,COALESCE(NULLIF(request_id,''),'account-override'),jsonb_build_object('command_id',override_command_id::text,'target_operation_command_id',target_operation_id::text,'reason',override_reason,'detail',detail));
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
ALTER FUNCTION public.control_apply_account_override_v2(text,text,uuid,uuid,uuid,text,text,text) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_apply_account_override_v2(text,text,uuid,uuid,uuid,text,text,text) FROM PUBLIC, relay_control_asset_registrar, relay_control_runtime;

-- +goose StatementBegin
CREATE FUNCTION public.control_apply_lifecycle_override_v1(
    override_command_id uuid, override_actor_admin_id uuid,
    target_operation_id uuid, override_reason text, audit_detail text,
    request_id text
) RETURNS text LANGUAGE sql SECURITY DEFINER VOLATILE SET search_path = pg_catalog AS $$
    SELECT public.control_apply_account_override_v2('account.lifecycle_override','OVERRIDE UNKNOWN OPERATION LIFECYCLE BLOCK',$1,$2,$3,$4,$5,$6)
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_apply_lifecycle_override_v1(uuid,uuid,uuid,text,text,text) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_apply_lifecycle_override_v1(uuid,uuid,uuid,text,text,text) FROM PUBLIC, relay_control_asset_registrar;
GRANT EXECUTE ON FUNCTION public.control_apply_lifecycle_override_v1(uuid,uuid,uuid,text,text,text) TO relay_control_runtime;

-- +goose StatementBegin
CREATE FUNCTION public.control_apply_same_account_override_v1(
    override_command_id uuid, override_actor_admin_id uuid,
    target_operation_id uuid, override_reason text, audit_detail text,
    request_id text
) RETURNS text LANGUAGE sql SECURITY DEFINER VOLATILE SET search_path = pg_catalog AS $$
    SELECT public.control_apply_account_override_v2('account.same_account_override','OVERRIDE UNKNOWN OPERATION SAME-ACCOUNT BLOCK',$1,$2,$3,$4,$5,$6)
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_apply_same_account_override_v1(uuid,uuid,uuid,text,text,text) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_apply_same_account_override_v1(uuid,uuid,uuid,text,text,text) FROM PUBLIC, relay_control_asset_registrar;
GRANT EXECUTE ON FUNCTION public.control_apply_same_account_override_v1(uuid,uuid,uuid,text,text,text) TO relay_control_runtime;

-- +goose Down
DO $$ BEGIN RAISE EXCEPTION 'migration 47 is forward-only' USING ERRCODE='55000'; END $$;
