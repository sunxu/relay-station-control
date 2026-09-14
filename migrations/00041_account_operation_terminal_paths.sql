-- +goose Up

-- Round 3 adds only the two frozen terminal paths that are needed after
-- dispatch admission: a proven native no-op and the reviewed deterministic
-- native failure. Both functions derive audit/receipt identity from the
-- registry and operation rows and commit in the caller's transaction.

-- +goose StatementBegin
CREATE FUNCTION public.control_terminalize_account_operation_noop_v1(
    noop_command_id uuid,
    noop_request_id text
)
RETURNS public.account_admin_operations
LANGUAGE plpgsql SECURITY DEFINER VOLATILE SET search_path = pg_catalog AS $$
DECLARE operation public.account_admin_operations%ROWTYPE;
DECLARE actor uuid;
DECLARE kind text;
DECLARE intent_hash bytea;
DECLARE secret_version smallint;
DECLARE response_body bytea;
BEGIN
    SELECT * INTO operation FROM public.account_admin_operations
    WHERE command_id=noop_command_id FOR UPDATE;
    IF NOT FOUND OR operation.execution_state <> 'prepared' THEN
        RAISE EXCEPTION 'account operation is not prepared for noop' USING ERRCODE='23514';
    END IF;
    SELECT r.actor_admin_id,r.command_kind,r.canonical_intent_hash,r.secret_fingerprint_key_version
      INTO actor,kind,intent_hash,secret_version
    FROM public.admin_command_registry r WHERE r.command_id=noop_command_id;
    UPDATE public.account_admin_operations
       SET execution_state='remote_noop',remote_result_code=NULL,updated_at=clock_timestamp()
     WHERE command_id=noop_command_id
     RETURNING * INTO operation;
    response_body := convert_to(jsonb_build_object('operation', public.control_account_operation_projection_v1(operation))::text, 'UTF8');
    INSERT INTO public.audit_logs(audit_id,category,action,result,actor_admin_id,request_id,details)
    VALUES(gen_random_uuid(),'account_admin','account.operation_noop','success',actor,
           COALESCE(NULLIF(noop_request_id,''),'account-operation'),
           jsonb_build_object('command_id',noop_command_id::text));
    INSERT INTO public.account_admin_command_receipts(
        command_id,target_operation_command_id,actor_admin_id,command_domain,command_kind,
        intent_encoding_version,canonical_intent_hash,secret_fingerprint_key_version,
        http_status,content_type,response_body)
    VALUES(noop_command_id,noop_command_id,actor,'account_admin',kind,1,intent_hash,secret_version,
           200,'application/json',response_body);
    RETURN operation;
END;
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_terminalize_account_operation_noop_v1(uuid,text) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_terminalize_account_operation_noop_v1(uuid,text) FROM PUBLIC, relay_control_asset_registrar;

-- +goose StatementBegin
CREATE FUNCTION public.control_terminalize_dispatched_account_operation_failure_v1(
    failure_command_id uuid,
    failure_code text,
    failure_request_id text
)
RETURNS public.account_admin_operations
LANGUAGE plpgsql SECURITY DEFINER VOLATILE SET search_path = pg_catalog AS $$
DECLARE operation public.account_admin_operations%ROWTYPE;
DECLARE actor uuid;
DECLARE kind text;
DECLARE intent_hash bytea;
DECLARE secret_version smallint;
DECLARE response_body bytea;
BEGIN
    IF failure_code <> 'node_management_unavailable' THEN
        RAISE EXCEPTION 'invalid dispatched failure code' USING ERRCODE='23514';
    END IF;
    SELECT * INTO operation FROM public.account_admin_operations
    WHERE command_id=failure_command_id FOR UPDATE;
    IF NOT FOUND OR operation.execution_state <> 'dispatched' THEN
        RAISE EXCEPTION 'account operation is not dispatched' USING ERRCODE='23514';
    END IF;
    SELECT r.actor_admin_id,r.command_kind,r.canonical_intent_hash,r.secret_fingerprint_key_version
      INTO actor,kind,intent_hash,secret_version
    FROM public.admin_command_registry r WHERE r.command_id=failure_command_id;
    UPDATE public.account_admin_operations
       SET execution_state='failed',remote_result_code=failure_code,updated_at=clock_timestamp()
     WHERE command_id=failure_command_id
     RETURNING * INTO operation;
    response_body := convert_to(jsonb_build_object(
        'error', jsonb_build_object('code', failure_code, 'message', failure_code),
        'operation', public.control_account_operation_projection_v1(operation))::text, 'UTF8');
    INSERT INTO public.audit_logs(audit_id,category,action,result,actor_admin_id,request_id,details)
    VALUES(gen_random_uuid(),'account_admin','account.operation_failed','failure',actor,
           COALESCE(NULLIF(failure_request_id,''),'account-operation'),
           jsonb_build_object('command_id',failure_command_id::text,'error_code',failure_code));
    INSERT INTO public.account_admin_command_receipts(
        command_id,target_operation_command_id,actor_admin_id,command_domain,command_kind,
        intent_encoding_version,canonical_intent_hash,secret_fingerprint_key_version,
        http_status,content_type,response_body)
    VALUES(failure_command_id,failure_command_id,actor,'account_admin',kind,1,intent_hash,secret_version,
           503,'application/json',response_body);
    RETURN operation;
END;
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_terminalize_dispatched_account_operation_failure_v1(uuid,text,text) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_terminalize_dispatched_account_operation_failure_v1(uuid,text,text) FROM PUBLIC, relay_control_asset_registrar;

GRANT EXECUTE ON FUNCTION public.control_terminalize_account_operation_noop_v1(uuid,text) TO relay_control_runtime;
GRANT EXECUTE ON FUNCTION public.control_terminalize_dispatched_account_operation_failure_v1(uuid,text,text) TO relay_control_runtime;

-- +goose Down
DO $$
BEGIN
    RAISE EXCEPTION 'migration 41 is forward-only' USING ERRCODE='55000';
END;
$$;
