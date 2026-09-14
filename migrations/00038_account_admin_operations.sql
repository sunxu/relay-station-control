-- +goose Up

CREATE TABLE public.account_admin_operations (
    command_id uuid PRIMARY KEY
        REFERENCES public.admin_command_registry(command_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    node_instance_id uuid NOT NULL
        REFERENCES public.relay_node_assets(instance_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    account_key text NOT NULL,
    operation_kind text NOT NULL,
    execution_state text NOT NULL DEFAULT 'prepared',
    dispatch_started_at timestamptz,
    remote_result_code text,
    upload_fingerprint_key_version smallint,
    upload_intent_fingerprint bytea,
    lifecycle_override_at timestamptz,
    lifecycle_override_by uuid
        REFERENCES public.control_admin_users(admin_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    lifecycle_override_reason text,
    same_account_override_at timestamptz,
    same_account_override_by uuid
        REFERENCES public.control_admin_users(admin_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    same_account_override_reason text,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT account_admin_operations_account_key_valid CHECK (
        octet_length(account_key) BETWEEN 1 AND 512
    ),
    CONSTRAINT account_admin_operations_kind_valid CHECK (
        operation_kind IN ('disable','enable','remove','upload_new','replace_existing')
    ),
    CONSTRAINT account_admin_operations_state_valid CHECK (
        execution_state IN ('prepared','dispatched','remote_applied','remote_noop','outcome_unknown','failed')
    ),
    CONSTRAINT account_admin_operations_dispatch_shape CHECK (
        (execution_state IN ('prepared','remote_noop') AND dispatch_started_at IS NULL)
        OR
        (execution_state IN ('dispatched','remote_applied','outcome_unknown') AND dispatch_started_at IS NOT NULL)
        OR
        execution_state = 'failed'
    ),
    CONSTRAINT account_admin_operations_upload_fingerprint_shape CHECK (
        (upload_fingerprint_key_version IS NULL AND upload_intent_fingerprint IS NULL)
        OR
        (upload_fingerprint_key_version IS NOT NULL AND upload_fingerprint_key_version > 0
         AND upload_intent_fingerprint IS NOT NULL
         AND octet_length(upload_intent_fingerprint) = 32)
    ),
    CONSTRAINT account_admin_operations_lifecycle_override_shape CHECK (
        (lifecycle_override_at IS NULL AND lifecycle_override_by IS NULL AND lifecycle_override_reason IS NULL)
        OR
        (lifecycle_override_at IS NOT NULL AND lifecycle_override_by IS NOT NULL
         AND lifecycle_override_reason IN ('process_restarted','node_stopped','risk_accepted'))
    ),
    CONSTRAINT account_admin_operations_same_account_override_shape CHECK (
        (same_account_override_at IS NULL AND same_account_override_by IS NULL AND same_account_override_reason IS NULL)
        OR
        (same_account_override_at IS NOT NULL AND same_account_override_by IS NOT NULL
         AND same_account_override_reason IN ('process_restarted','node_stopped','risk_accepted'))
    ),
    CONSTRAINT account_admin_operations_timestamps_valid CHECK (
        updated_at >= created_at AND isfinite(created_at) AND isfinite(updated_at)
    )
);

CREATE INDEX account_admin_operations_node_account_idx
    ON public.account_admin_operations(node_instance_id, account_key, created_at DESC);

CREATE UNIQUE INDEX account_admin_operations_same_account_blocker_uidx
    ON public.account_admin_operations(node_instance_id, account_key)
    WHERE execution_state IN ('dispatched','outcome_unknown')
      AND same_account_override_at IS NULL;

CREATE TABLE public.account_admin_command_receipts (
    command_id uuid PRIMARY KEY
        REFERENCES public.admin_command_registry(command_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    target_operation_command_id uuid
        REFERENCES public.account_admin_operations(command_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    actor_admin_id uuid NOT NULL
        REFERENCES public.control_admin_users(admin_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    command_domain text NOT NULL DEFAULT 'account_admin',
    command_kind text NOT NULL,
    intent_encoding_version smallint NOT NULL,
    canonical_intent_hash bytea NOT NULL,
    secret_fingerprint_key_version smallint,
    http_status smallint NOT NULL,
    content_type text NOT NULL DEFAULT 'application/json',
    response_body bytea NOT NULL,
    committed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT account_admin_command_receipts_domain_check CHECK (command_domain = 'account_admin'),
    CONSTRAINT account_admin_command_receipts_kind_check CHECK (
        command_kind IN ('account.disable','account.enable','account.remove',
                         'account.upload_new','account.replace_existing',
                         'account.lifecycle_override','account.same_account_override')
    ),
    CONSTRAINT account_admin_command_receipts_encoding_check CHECK (intent_encoding_version = 1),
    CONSTRAINT account_admin_command_receipts_hash_check CHECK (octet_length(canonical_intent_hash) = 32),
    CONSTRAINT account_admin_command_receipts_secret_version_check CHECK (
        secret_fingerprint_key_version IS NULL OR secret_fingerprint_key_version > 0
    ),
    CONSTRAINT account_admin_command_receipts_status_check CHECK (http_status BETWEEN 200 AND 599),
    CONSTRAINT account_admin_command_receipts_content_type_check CHECK (content_type = 'application/json'),
    CONSTRAINT account_admin_command_receipts_committed_at_check CHECK (isfinite(committed_at))
);

CREATE INDEX account_admin_command_receipts_target_idx
    ON public.account_admin_command_receipts(target_operation_command_id)
    WHERE target_operation_command_id IS NOT NULL;

ALTER TABLE public.audit_logs DROP CONSTRAINT audit_logs_category_valid;
ALTER TABLE public.audit_logs ADD CONSTRAINT audit_logs_category_valid CHECK (
    category IN ('bootstrap','administrator','password','mfa','session',
                 'reauthentication','authorization','rate_limit',
                 'account_inventory','account_inventory_history','relay_binding','asset','account_admin')
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
    'node.health','node.connection_test','node.monitoring_enable','node.monitoring_disable',
    'account.operation_failed'
));

-- The command registry is the single global namespace.  These guards make a
-- direct insert unable to pair an account row or receipt with another domain.
-- +goose StatementBegin
CREATE FUNCTION public.control_validate_account_admin_operation_registry()
RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
DECLARE reservation public.admin_command_registry%ROWTYPE;
BEGIN
    SELECT * INTO reservation FROM public.admin_command_registry WHERE command_id = NEW.command_id;
    IF NOT FOUND OR reservation.command_domain <> 'account_admin'
       OR reservation.command_kind <> 'account.' || NEW.operation_kind THEN
        RAISE EXCEPTION 'account operation registry mismatch' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_validate_account_admin_operation_registry() OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_validate_account_admin_operation_registry() FROM PUBLIC, relay_control_runtime;
CREATE TRIGGER account_admin_operations_registry_guard
BEFORE INSERT ON public.account_admin_operations
FOR EACH ROW EXECUTE FUNCTION public.control_validate_account_admin_operation_registry();

-- +goose StatementBegin
CREATE FUNCTION public.control_validate_account_admin_receipt_registry()
RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
DECLARE reservation public.admin_command_registry%ROWTYPE;
BEGIN
    SELECT * INTO reservation FROM public.admin_command_registry WHERE command_id = NEW.command_id;
    IF NOT FOUND OR reservation.command_domain <> 'account_admin'
       OR reservation.actor_admin_id IS DISTINCT FROM NEW.actor_admin_id
       OR reservation.command_kind IS DISTINCT FROM NEW.command_kind
       OR reservation.intent_encoding_version IS DISTINCT FROM NEW.intent_encoding_version
       OR reservation.canonical_intent_hash IS DISTINCT FROM NEW.canonical_intent_hash
       OR reservation.secret_fingerprint_key_version IS DISTINCT FROM NEW.secret_fingerprint_key_version THEN
        RAISE EXCEPTION 'account receipt registry mismatch' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_validate_account_admin_receipt_registry() OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_validate_account_admin_receipt_registry() FROM PUBLIC, relay_control_runtime;
CREATE TRIGGER account_admin_command_receipts_registry_guard
BEFORE INSERT ON public.account_admin_command_receipts
FOR EACH ROW EXECUTE FUNCTION public.control_validate_account_admin_receipt_registry();

-- Operation identity is immutable; only the explicit state/dispatch/error and
-- one-time override columns may change.  The transition matrix is deliberately
-- small and lives in SQL rather than in a generic workflow abstraction.
-- +goose StatementBegin
CREATE FUNCTION public.control_protect_account_admin_operation()
RETURNS trigger LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    IF TG_OP IN ('DELETE','TRUNCATE') THEN
        RAISE EXCEPTION 'account operations are immutable records' USING ERRCODE = '42501';
    END IF;
    IF NEW.command_id IS DISTINCT FROM OLD.command_id
       OR NEW.node_instance_id IS DISTINCT FROM OLD.node_instance_id
       OR NEW.account_key IS DISTINCT FROM OLD.account_key
       OR NEW.operation_kind IS DISTINCT FROM OLD.operation_kind
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'account operation identity is immutable' USING ERRCODE = '23514';
    END IF;
    IF NOT ((OLD.execution_state='prepared' AND NEW.execution_state IN ('prepared','dispatched','remote_noop','failed'))
        OR (OLD.execution_state='dispatched' AND NEW.execution_state IN ('dispatched','remote_applied','failed','outcome_unknown'))
        OR (OLD.execution_state IN ('remote_applied','remote_noop','failed') AND NEW.execution_state=OLD.execution_state)
        OR (OLD.execution_state='outcome_unknown' AND NEW.execution_state=OLD.execution_state)) THEN
        RAISE EXCEPTION 'invalid account operation state transition' USING ERRCODE = '23514';
    END IF;
    IF OLD.execution_state='prepared' AND NEW.execution_state='prepared'
       AND NEW.dispatch_started_at IS DISTINCT FROM OLD.dispatch_started_at THEN
        RAISE EXCEPTION 'prepared operation cannot have dispatch timestamp' USING ERRCODE = '23514';
    END IF;
    IF NEW.execution_state IN ('dispatched','remote_applied','outcome_unknown') AND NEW.dispatch_started_at IS NULL THEN
        RAISE EXCEPTION 'dispatched operation requires dispatch timestamp' USING ERRCODE = '23514';
    END IF;
    IF OLD.lifecycle_override_at IS NOT NULL
       AND ROW(NEW.lifecycle_override_at,NEW.lifecycle_override_by,NEW.lifecycle_override_reason)
           IS DISTINCT FROM ROW(OLD.lifecycle_override_at,OLD.lifecycle_override_by,OLD.lifecycle_override_reason) THEN
        RAISE EXCEPTION 'lifecycle override is immutable' USING ERRCODE = '23514';
    END IF;
    IF OLD.same_account_override_at IS NOT NULL
       AND ROW(NEW.same_account_override_at,NEW.same_account_override_by,NEW.same_account_override_reason)
           IS DISTINCT FROM ROW(OLD.same_account_override_at,OLD.same_account_override_by,OLD.same_account_override_reason) THEN
        RAISE EXCEPTION 'same-account override is immutable' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_protect_account_admin_operation() OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_protect_account_admin_operation() FROM PUBLIC, relay_control_runtime;
CREATE TRIGGER account_admin_operations_guard
BEFORE UPDATE OR DELETE ON public.account_admin_operations
FOR EACH ROW EXECUTE FUNCTION public.control_protect_account_admin_operation();
CREATE TRIGGER account_admin_operations_truncate_guard
BEFORE TRUNCATE ON public.account_admin_operations
FOR EACH STATEMENT EXECUTE FUNCTION public.control_protect_account_admin_operation();

-- Receipts are replay truth and cannot be edited or removed.
-- +goose StatementBegin
CREATE FUNCTION public.control_reject_account_admin_receipt_mutation()
RETURNS trigger LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    RAISE EXCEPTION 'account command receipts are immutable' USING ERRCODE = '42501';
END;
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_reject_account_admin_receipt_mutation() OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_reject_account_admin_receipt_mutation() FROM PUBLIC, relay_control_runtime;
CREATE TRIGGER account_admin_command_receipts_immutable
BEFORE UPDATE OR DELETE ON public.account_admin_command_receipts
FOR EACH ROW EXECUTE FUNCTION public.control_reject_account_admin_receipt_mutation();
CREATE TRIGGER account_admin_command_receipts_truncate_guard
BEFORE TRUNCATE ON public.account_admin_command_receipts
FOR EACH STATEMENT EXECUTE FUNCTION public.control_reject_account_admin_receipt_mutation();

REVOKE ALL ON TABLE public.account_admin_operations, public.account_admin_command_receipts
    FROM PUBLIC, relay_control_asset_registrar;
GRANT SELECT, INSERT, UPDATE ON TABLE public.account_admin_operations TO relay_control_runtime;
GRANT SELECT, INSERT ON TABLE public.account_admin_command_receipts TO relay_control_runtime;
GRANT REFERENCES ON TABLE public.admin_command_registry TO relay_control_runtime;
REVOKE UPDATE, DELETE, TRUNCATE ON public.account_admin_command_receipts FROM relay_control_runtime;

-- +goose Down
DROP TABLE public.account_admin_command_receipts;
DROP TABLE public.account_admin_operations;
