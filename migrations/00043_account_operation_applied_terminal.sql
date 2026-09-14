-- +goose Up

-- Stable native success is a terminal command result and therefore must
-- persist its immutable replay receipt in the same transaction as the state
-- transition.
-- +goose StatementBegin
CREATE FUNCTION public.control_terminalize_account_operation_applied_v1(
    applied_command_id uuid,
    applied_request_id text
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
    WHERE command_id=applied_command_id FOR UPDATE;
    IF NOT FOUND OR operation.execution_state <> 'dispatched' THEN
        RAISE EXCEPTION 'account operation is not dispatched' USING ERRCODE='23514';
    END IF;
    SELECT r.actor_admin_id,r.command_kind,r.canonical_intent_hash,r.secret_fingerprint_key_version
      INTO actor,kind,intent_hash,secret_version
    FROM public.admin_command_registry r WHERE r.command_id=applied_command_id;
    UPDATE public.account_admin_operations
       SET execution_state='remote_applied',remote_result_code=NULL,updated_at=clock_timestamp()
     WHERE command_id=applied_command_id
     RETURNING * INTO operation;
    response_body := convert_to(jsonb_build_object(
        'operation', public.control_account_operation_projection_v1(operation))::text, 'UTF8');
    INSERT INTO public.audit_logs(audit_id,category,action,result,actor_admin_id,request_id,details)
    VALUES(gen_random_uuid(),'account_admin','account.operation_applied','success',actor,
           COALESCE(NULLIF(applied_request_id,''),'account-operation'),
           jsonb_build_object('command_id',applied_command_id::text));
    INSERT INTO public.account_admin_command_receipts(
        command_id,target_operation_command_id,actor_admin_id,command_domain,command_kind,
        intent_encoding_version,canonical_intent_hash,secret_fingerprint_key_version,
        http_status,content_type,response_body)
    VALUES(applied_command_id,applied_command_id,actor,'account_admin',kind,1,intent_hash,
           secret_version,200,'application/json',response_body);
    RETURN operation;
END;
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_terminalize_account_operation_applied_v1(uuid,text) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_terminalize_account_operation_applied_v1(uuid,text) FROM PUBLIC, relay_control_asset_registrar;
GRANT EXECUTE ON FUNCTION public.control_terminalize_account_operation_applied_v1(uuid,text) TO relay_control_runtime;

-- +goose Down
DO $$
BEGIN
    RAISE EXCEPTION 'migration 43 is forward-only' USING ERRCODE='55000';
END;
$$;
