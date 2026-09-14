-- +goose Up

-- Replace the Round 1 result-bearing functions with a closed database-owned
-- error/result mapping. Runtime callers identify the operation and intent;
-- they do not supply terminal HTTP or response semantics.
DROP FUNCTION public.control_transition_account_admin_operation_v1(uuid,text,text,text);
DROP FUNCTION public.control_admit_account_dispatch_v1(uuid,uuid,text,bytea,smallint,text);
DROP FUNCTION public.control_terminalize_account_operation_failure_v1(uuid,text,smallint,bytea,text);

-- +goose StatementBegin
CREATE FUNCTION public.control_account_failure_http_status_v1(failure_code text)
RETURNS smallint
LANGUAGE sql IMMUTABLE STRICT SET search_path = pg_catalog AS $$
    SELECT CASE failure_code
        WHEN 'invalid_request' THEN 400
        WHEN 'upload_too_large' THEN 413
        WHEN 'upload_invalid' THEN 400
        WHEN 'identity_mismatch' THEN 400
        WHEN 'node_not_found' THEN 404
        WHEN 'unsupported_provider' THEN 409
        WHEN 'node_retired' THEN 409
        WHEN 'node_monitoring_ineligible' THEN 409
        WHEN 'account_target_not_found' THEN 409
        WHEN 'account_target_ambiguous' THEN 409
        WHEN 'account_target_exists' THEN 409
        WHEN 'account_filename_conflict' THEN 409
        WHEN 'account_operation_in_progress' THEN 409
        WHEN 'unsupported_node_version' THEN 503
        WHEN 'node_management_unavailable' THEN 503
        WHEN 'service_unavailable' THEN 503
        ELSE NULL
    END::smallint
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_account_failure_http_status_v1(text) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_account_failure_http_status_v1(text) FROM PUBLIC, relay_control_runtime, relay_control_asset_registrar;

-- +goose StatementBegin
CREATE FUNCTION public.control_account_operation_projection_v1(operation public.account_admin_operations)
RETURNS jsonb
LANGUAGE sql STABLE SET search_path = pg_catalog AS $$
    SELECT jsonb_build_object(
        'command_id', ($1).command_id,
        'node_instance_id', ($1).node_instance_id,
        'account_key', ($1).account_key,
        'operation_kind', ($1).operation_kind,
        'execution_state', ($1).execution_state,
        'result', CASE ($1).execution_state
            WHEN 'remote_applied' THEN 'applied'
            WHEN 'remote_noop' THEN 'noop'
            WHEN 'failed' THEN 'failed'
            ELSE NULL
        END,
        'error_code', ($1).remote_result_code,
        'lifecycle_overridden', (($1).lifecycle_override_at IS NOT NULL),
        'lifecycle_override_reason', ($1).lifecycle_override_reason,
        'same_account_overridden', (($1).same_account_override_at IS NOT NULL),
        'same_account_override_reason', ($1).same_account_override_reason,
        'created_at', ($1).created_at,
        'updated_at', ($1).updated_at
    )
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_account_operation_projection_v1(public.account_admin_operations) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_account_operation_projection_v1(public.account_admin_operations) FROM PUBLIC, relay_control_runtime, relay_control_asset_registrar;

-- +goose StatementBegin
CREATE FUNCTION public.control_transition_account_admin_operation_v1(
    transition_command_id uuid,
    expected_state text,
    next_state text
)
RETURNS public.account_admin_operations
LANGUAGE plpgsql SECURITY DEFINER VOLATILE SET search_path = pg_catalog AS $$
DECLARE current public.account_admin_operations%ROWTYPE;
DECLARE updated public.account_admin_operations%ROWTYPE;
BEGIN
    SELECT * INTO current FROM public.account_admin_operations
    WHERE command_id = transition_command_id FOR UPDATE;
    IF NOT FOUND THEN RAISE EXCEPTION 'account operation not found' USING ERRCODE='P0002'; END IF;
    IF current.execution_state <> expected_state THEN RAISE EXCEPTION 'account operation state mismatch' USING ERRCODE='23514'; END IF;
    IF expected_state <> 'dispatched' OR next_state NOT IN ('remote_applied','outcome_unknown') THEN
        RAISE EXCEPTION 'invalid account operation transition' USING ERRCODE='23514';
    END IF;
    UPDATE public.account_admin_operations
    SET execution_state=next_state,
        remote_result_code=CASE next_state
            WHEN 'outcome_unknown' THEN 'remote_outcome_unknown'
            ELSE NULL
        END,
        updated_at=clock_timestamp()
    WHERE command_id=transition_command_id
    RETURNING * INTO updated;
    RETURN updated;
END;
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_transition_account_admin_operation_v1(uuid,text,text) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_transition_account_admin_operation_v1(uuid,text,text) FROM PUBLIC, relay_control_runtime, relay_control_asset_registrar;

-- +goose StatementBegin
CREATE FUNCTION public.control_admit_account_dispatch_v1(
    admission_command_id uuid,
    admission_node_instance_id uuid,
    admission_account_key text,
    admission_request_id text
)
RETURNS boolean
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
        RAISE EXCEPTION 'account operation is not prepared for dispatch' USING ERRCODE='23514';
    END IF;
    SELECT command_id INTO blocker
    FROM public.account_admin_operations
    WHERE node_instance_id=admission_node_instance_id
      AND account_key=admission_account_key
      AND command_id<>admission_command_id
      AND execution_state IN ('dispatched','outcome_unknown')
      AND same_account_override_at IS NULL
    ORDER BY command_id FOR UPDATE LIMIT 1;
    IF blocker IS NULL THEN
        UPDATE public.account_admin_operations
        SET execution_state='dispatched', dispatch_started_at=clock_timestamp(),
            updated_at=clock_timestamp()
        WHERE command_id=admission_command_id;
        RETURN TRUE;
    END IF;
    SELECT r.actor_admin_id,r.command_kind,r.canonical_intent_hash,
           r.secret_fingerprint_key_version
      INTO actor,kind,intent_hash,secret_version
    FROM public.admin_command_registry r
    WHERE r.command_id=admission_command_id;
    UPDATE public.account_admin_operations
    SET execution_state='failed', remote_result_code='account_operation_in_progress',
        updated_at=clock_timestamp()
    WHERE command_id=admission_command_id
    RETURNING * INTO current;
    response_body := convert_to(jsonb_build_object(
        'error', jsonb_build_object(
            'code', 'account_operation_in_progress',
            'message', 'account_operation_in_progress'
        ),
        'operation', public.control_account_operation_projection_v1(current)
    )::text, 'UTF8');
    INSERT INTO public.audit_logs(audit_id,category,action,result,actor_admin_id,request_id,details)
    VALUES(gen_random_uuid(),'account_admin','account.operation_failed','failure',actor,
           COALESCE(NULLIF(admission_request_id,''),'account-dispatch'),
           jsonb_build_object('command_id',admission_command_id::text,'error_code','account_operation_in_progress'));
    INSERT INTO public.account_admin_command_receipts(
        command_id,target_operation_command_id,actor_admin_id,command_domain,command_kind,
        intent_encoding_version,canonical_intent_hash,secret_fingerprint_key_version,
        http_status,content_type,response_body
    ) VALUES(admission_command_id,admission_command_id,actor,'account_admin',kind,1,intent_hash,
             secret_version,public.control_account_failure_http_status_v1('account_operation_in_progress'),
             'application/json',response_body);
    RETURN FALSE;
END;
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_admit_account_dispatch_v1(uuid,uuid,text,text) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_admit_account_dispatch_v1(uuid,uuid,text,text) FROM PUBLIC, relay_control_runtime, relay_control_asset_registrar;

-- +goose StatementBegin
CREATE FUNCTION public.control_terminalize_account_operation_failure_v1(
    failure_command_id uuid,
    failure_code text,
    failure_request_id text
)
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER VOLATILE SET search_path = pg_catalog AS $$
DECLARE operation public.account_admin_operations%ROWTYPE;
DECLARE actor uuid;
DECLARE kind text;
DECLARE intent_hash bytea;
DECLARE secret_version smallint;
DECLARE status smallint;
DECLARE response_body bytea;
BEGIN
    SELECT * INTO operation FROM public.account_admin_operations
    WHERE command_id=failure_command_id FOR UPDATE;
    IF NOT FOUND THEN RAISE EXCEPTION 'account operation not found' USING ERRCODE='P0002'; END IF;
    IF operation.execution_state <> 'prepared' THEN RAISE EXCEPTION 'account operation is not prepared' USING ERRCODE='23514'; END IF;
    IF failure_code NOT IN ('node_retired','node_monitoring_ineligible','unsupported_provider',
                            'unsupported_node_version','node_management_unavailable','invalid_request',
                            'account_target_not_found','account_target_ambiguous','account_target_exists',
                            'account_filename_conflict','account_operation_in_progress') THEN
        RAISE EXCEPTION 'account failure code is not valid for pre-dispatch terminalization' USING ERRCODE='23514';
    END IF;
    status := public.control_account_failure_http_status_v1(failure_code);
    SELECT r.actor_admin_id,r.command_kind,r.canonical_intent_hash,r.secret_fingerprint_key_version
      INTO actor,kind,intent_hash,secret_version
    FROM public.admin_command_registry r WHERE r.command_id=failure_command_id;
    UPDATE public.account_admin_operations
    SET execution_state='failed',remote_result_code=failure_code,updated_at=clock_timestamp()
    WHERE command_id=failure_command_id
    RETURNING * INTO operation;
    response_body := convert_to(jsonb_build_object(
        'error', jsonb_build_object('code', failure_code, 'message', failure_code),
        'operation', public.control_account_operation_projection_v1(operation)
    )::text, 'UTF8');
    INSERT INTO public.audit_logs(audit_id,category,action,result,actor_admin_id,request_id,details)
    VALUES(gen_random_uuid(),'account_admin','account.operation_failed','failure',actor,
           COALESCE(NULLIF(failure_request_id,''),'account-operation'),
           jsonb_build_object('command_id',failure_command_id::text,'error_code',failure_code));
    INSERT INTO public.account_admin_command_receipts(
        command_id,target_operation_command_id,actor_admin_id,command_domain,command_kind,
        intent_encoding_version,canonical_intent_hash,secret_fingerprint_key_version,
        http_status,content_type,response_body
    ) VALUES(failure_command_id,failure_command_id,actor,'account_admin',kind,1,intent_hash,
             secret_version,status,'application/json',response_body);
END;
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_terminalize_account_operation_failure_v1(uuid,text,text) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_terminalize_account_operation_failure_v1(uuid,text,text) FROM PUBLIC, relay_control_runtime, relay_control_asset_registrar;

GRANT EXECUTE ON FUNCTION public.control_transition_account_admin_operation_v1(uuid,text,text) TO relay_control_runtime;
GRANT EXECUTE ON FUNCTION public.control_admit_account_dispatch_v1(uuid,uuid,text,text) TO relay_control_runtime;
GRANT EXECUTE ON FUNCTION public.control_terminalize_account_operation_failure_v1(uuid,text,text) TO relay_control_runtime;

-- The old signatures were dropped above. Keep the accept function from 39;
-- it already derives registry identity inside the same transaction.

-- +goose Down
-- Forward-only: removing this mapping would restore caller-controlled result semantics.
-- +goose StatementBegin
DO $$
BEGIN
    RAISE EXCEPTION 'migration 40 is forward-only' USING ERRCODE='55000';
END;
$$;
-- +goose StatementEnd
