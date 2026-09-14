-- +goose Up

-- No-op admission performs the same durable same-account blocker decision as
-- dispatch admission, then terminalizes either the no-op or the blocker loser
-- before returning. No database transaction is retained by the caller.
-- +goose StatementBegin
CREATE FUNCTION public.control_admit_account_noop_v1(
    admission_command_id uuid,
    admission_node_instance_id uuid,
    admission_account_key text,
    admission_request_id text
)
RETURNS public.account_admin_operations
LANGUAGE plpgsql SECURITY DEFINER VOLATILE SET search_path = pg_catalog AS $$
DECLARE current public.account_admin_operations%ROWTYPE;
DECLARE blocker uuid;
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
       OR current.account_key <> admission_account_key
       OR current.execution_state <> 'prepared' THEN
        RAISE EXCEPTION 'account operation is not prepared for noop' USING ERRCODE='23514';
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
        UPDATE public.account_admin_operations
        SET execution_state='failed',remote_result_code='account_operation_in_progress',updated_at=clock_timestamp()
        WHERE command_id=admission_command_id
        RETURNING * INTO current;
        response_body := convert_to(jsonb_build_object(
            'error', jsonb_build_object('code','account_operation_in_progress','message','account_operation_in_progress'),
            'operation', public.control_account_operation_projection_v1(current))::text, 'UTF8');
        INSERT INTO public.audit_logs(audit_id,category,action,result,actor_admin_id,request_id,details)
        VALUES(gen_random_uuid(),'account_admin','account.operation_failed','failure',actor,
               COALESCE(NULLIF(admission_request_id,''),'account-dispatch'),
               jsonb_build_object('command_id',admission_command_id::text,'error_code','account_operation_in_progress'));
        INSERT INTO public.account_admin_command_receipts(
            command_id,target_operation_command_id,actor_admin_id,command_domain,command_kind,
            intent_encoding_version,canonical_intent_hash,secret_fingerprint_key_version,
            http_status,content_type,response_body)
        VALUES(admission_command_id,admission_command_id,actor,'account_admin',kind,1,intent_hash,
               secret_version,409,'application/json',response_body);
    END IF;
    RETURN current;
END;
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_admit_account_noop_v1(uuid,uuid,text,text) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_admit_account_noop_v1(uuid,uuid,text,text) FROM PUBLIC, relay_control_asset_registrar;
GRANT EXECUTE ON FUNCTION public.control_admit_account_noop_v1(uuid,uuid,text,text) TO relay_control_runtime;

-- +goose Down
DO $$
BEGIN
    RAISE EXCEPTION 'migration 42 is forward-only' USING ERRCODE='55000';
END;
$$;
