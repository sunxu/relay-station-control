-- +goose Up

-- Migration 38 exposed direct DML while the persistence foundation was being
-- assembled.  The runtime role keeps read access, but all writes now cross
-- these bounded, SECURITY DEFINER functions.

-- +goose StatementBegin
CREATE FUNCTION public.control_accept_account_admin_operation_v1(
    reservation_command_id uuid,
    reservation_actor_admin_id uuid,
    reservation_command_kind text,
    reservation_canonical_intent_hash bytea,
    reservation_secret_fingerprint_key_version smallint,
    operation_node_instance_id uuid,
    operation_account_key text,
    operation_kind text,
    operation_upload_intent_fingerprint bytea
)
RETURNS public.account_admin_operations
LANGUAGE plpgsql SECURITY DEFINER VOLATILE SET search_path = pg_catalog AS $$
DECLARE created public.account_admin_operations%ROWTYPE;
BEGIN
    PERFORM command_id FROM public.control_reserve_admin_command_v1(
        reservation_command_id, reservation_actor_admin_id, 'account_admin'::text,
        reservation_command_kind, 1::smallint, reservation_canonical_intent_hash,
        reservation_secret_fingerprint_key_version
    );
    INSERT INTO public.account_admin_operations(
        command_id,node_instance_id,account_key,operation_kind,execution_state,
        upload_fingerprint_key_version,upload_intent_fingerprint
    ) VALUES (
        reservation_command_id,operation_node_instance_id,operation_account_key,
        operation_kind,'prepared',reservation_secret_fingerprint_key_version,
        operation_upload_intent_fingerprint
    ) RETURNING * INTO created;
    RETURN created;
END;
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_accept_account_admin_operation_v1(uuid,uuid,text,bytea,smallint,uuid,text,text,bytea) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_accept_account_admin_operation_v1(uuid,uuid,text,bytea,smallint,uuid,text,text,bytea) FROM PUBLIC, relay_control_asset_registrar;

-- This function deliberately excludes prepared -> dispatched.  That edge has
-- one owner below so the same-account admission and transition share a tx.
-- +goose StatementBegin
CREATE FUNCTION public.control_transition_account_admin_operation_v1(
    transition_command_id uuid,
    expected_state text,
    next_state text,
    next_result_code text
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
    IF expected_state = 'prepared' THEN
        RAISE EXCEPTION 'prepared dispatch requires atomic admission' USING ERRCODE='23514';
    END IF;
    IF NOT ((expected_state='dispatched' AND next_state IN ('remote_applied','failed','outcome_unknown')))
       THEN RAISE EXCEPTION 'invalid account operation transition' USING ERRCODE='23514'; END IF;
    UPDATE public.account_admin_operations
    SET execution_state=next_state, remote_result_code=next_result_code,
        updated_at=clock_timestamp()
    WHERE command_id=transition_command_id
    RETURNING * INTO updated;
    RETURN updated;
END;
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_transition_account_admin_operation_v1(uuid,text,text,text) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_transition_account_admin_operation_v1(uuid,text,text,text) FROM PUBLIC, relay_control_asset_registrar;

-- The only writer for prepared -> dispatched.  The advisory key serializes
-- admission attempts for one node/account, then the blocker row and current
-- operation are locked in the same transaction.  FALSE means the operation
-- was terminalized as the deterministic blocker loser.
-- +goose StatementBegin
CREATE FUNCTION public.control_admit_account_dispatch_v1(
    admission_command_id uuid,
    admission_node_instance_id uuid,
    admission_account_key text,
    blocker_response_body bytea,
    blocker_http_status smallint,
    blocker_request_id text
)
RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER VOLATILE SET search_path = pg_catalog AS $$
DECLARE current public.account_admin_operations%ROWTYPE;
DECLARE blocker uuid;
DECLARE actor uuid;
DECLARE kind text;
DECLARE intent_hash bytea;
DECLARE secret_version smallint;
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
    IF blocker_response_body IS NULL OR blocker_http_status < 400 OR blocker_http_status > 599 THEN
        RAISE EXCEPTION 'blocker terminal response is invalid' USING ERRCODE='23514';
    END IF;
    SELECT r.actor_admin_id,r.command_kind,r.canonical_intent_hash,
           r.secret_fingerprint_key_version
      INTO actor,kind,intent_hash,secret_version
    FROM public.admin_command_registry r
    WHERE r.command_id=admission_command_id;
    UPDATE public.account_admin_operations
    SET execution_state='failed', remote_result_code='account_operation_in_progress',
        updated_at=clock_timestamp()
    WHERE command_id=admission_command_id;
    INSERT INTO public.audit_logs(audit_id,category,action,result,actor_admin_id,request_id,details)
    VALUES(gen_random_uuid(),'account_admin','account.operation_failed','failure',actor,
           COALESCE(NULLIF(blocker_request_id,''),'account-dispatch'),
           jsonb_build_object('command_id',admission_command_id::text,'error_code','account_operation_in_progress'));
    INSERT INTO public.account_admin_command_receipts(
        command_id,target_operation_command_id,actor_admin_id,command_domain,command_kind,
        intent_encoding_version,canonical_intent_hash,secret_fingerprint_key_version,
        http_status,content_type,response_body
    ) VALUES(admission_command_id,admission_command_id,actor,'account_admin',kind,1,intent_hash,
             secret_version,blocker_http_status,'application/json',blocker_response_body);
    RETURN FALSE;
END;
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_admit_account_dispatch_v1(uuid,uuid,text,bytea,smallint,text) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_admit_account_dispatch_v1(uuid,uuid,text,bytea,smallint,text) FROM PUBLIC, relay_control_asset_registrar;

-- Narrow terminalization for deterministic failures discovered after
-- acceptance and before dispatch.  It owns the operation update, audit and
-- receipt in one transaction.
-- +goose StatementBegin
CREATE FUNCTION public.control_terminalize_account_operation_failure_v1(
    failure_command_id uuid,
    failure_code text,
    failure_http_status smallint,
    failure_response_body bytea,
    failure_request_id text
)
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER VOLATILE SET search_path = pg_catalog AS $$
DECLARE operation_state text;
DECLARE actor uuid;
DECLARE kind text;
DECLARE intent_hash bytea;
DECLARE secret_version smallint;
BEGIN
    SELECT execution_state INTO operation_state FROM public.account_admin_operations
    WHERE command_id=failure_command_id FOR UPDATE;
    IF NOT FOUND THEN RAISE EXCEPTION 'account operation not found' USING ERRCODE='P0002'; END IF;
    IF operation_state <> 'prepared' THEN RAISE EXCEPTION 'account operation is not prepared' USING ERRCODE='23514'; END IF;
    IF failure_code IS NULL OR failure_response_body IS NULL OR failure_http_status < 400 OR failure_http_status > 599 THEN
        RAISE EXCEPTION 'account failure receipt is invalid' USING ERRCODE='23514';
    END IF;
    SELECT r.actor_admin_id,r.command_kind,r.canonical_intent_hash,r.secret_fingerprint_key_version
      INTO actor,kind,intent_hash,secret_version
    FROM public.admin_command_registry r WHERE r.command_id=failure_command_id;
    UPDATE public.account_admin_operations
    SET execution_state='failed',remote_result_code=failure_code,updated_at=clock_timestamp()
    WHERE command_id=failure_command_id;
    INSERT INTO public.audit_logs(audit_id,category,action,result,actor_admin_id,request_id,details)
    VALUES(gen_random_uuid(),'account_admin','account.operation_failed','failure',actor,
           COALESCE(NULLIF(failure_request_id,''),'account-operation'),
           jsonb_build_object('command_id',failure_command_id::text,'error_code',failure_code));
    INSERT INTO public.account_admin_command_receipts(
        command_id,target_operation_command_id,actor_admin_id,command_domain,command_kind,
        intent_encoding_version,canonical_intent_hash,secret_fingerprint_key_version,
        http_status,content_type,response_body
    ) VALUES(failure_command_id,failure_command_id,actor,'account_admin',kind,1,intent_hash,
             secret_version,failure_http_status,'application/json',failure_response_body);
END;
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_terminalize_account_operation_failure_v1(uuid,text,smallint,bytea,text) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_terminalize_account_operation_failure_v1(uuid,text,smallint,bytea,text) FROM PUBLIC, relay_control_asset_registrar;

-- Runtime has no direct mutation privilege.  The functions above are the
-- complete write surface for this round.
REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON public.account_admin_operations FROM relay_control_runtime;
REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON public.account_admin_command_receipts FROM relay_control_runtime;
GRANT EXECUTE ON FUNCTION public.control_accept_account_admin_operation_v1(uuid,uuid,text,bytea,smallint,uuid,text,text,bytea) TO relay_control_runtime;
GRANT EXECUTE ON FUNCTION public.control_transition_account_admin_operation_v1(uuid,text,text,text) TO relay_control_runtime;
GRANT EXECUTE ON FUNCTION public.control_admit_account_dispatch_v1(uuid,uuid,text,bytea,smallint,text) TO relay_control_runtime;
GRANT EXECUTE ON FUNCTION public.control_terminalize_account_operation_failure_v1(uuid,text,smallint,bytea,text) TO relay_control_runtime;

-- +goose Down
-- Forward-only: dropping the controlled write boundary would reopen the
-- runtime DML vulnerability for databases that have applied this migration.
-- +goose StatementBegin
DO $$
BEGIN
    RAISE EXCEPTION 'migration 39 is forward-only' USING ERRCODE='55000';
END;
$$;
-- +goose StatementEnd
