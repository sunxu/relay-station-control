-- +goose Up

CREATE TABLE relay_node_gateway_account_bindings (
    binding_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    relay_node_id uuid NOT NULL,
    gateway_instance_id uuid NOT NULL,
    gateway_account_id bigint NOT NULL,
    evidence_snapshot_id uuid NOT NULL,
    bound_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    bound_by uuid NOT NULL,
    bind_reason text NOT NULL,
    ended_at timestamptz,
    ended_by uuid,
    end_reason text,
    CONSTRAINT relay_node_gateway_account_bindings_account_positive CHECK (
        gateway_account_id > 0
    ),
    CONSTRAINT relay_node_gateway_account_bindings_bind_reason_fixed CHECK (
        bind_reason IN ('administrator_bind', 'administrator_rebind')
    ),
    CONSTRAINT relay_node_gateway_account_bindings_end_reason_fixed CHECK (
        end_reason IS NULL
        OR end_reason IN ('administrator_unbind', 'administrator_rebind')
    ),
    CONSTRAINT relay_node_gateway_account_bindings_end_shape CHECK (
        (
            ended_at IS NULL
            AND ended_by IS NULL
            AND end_reason IS NULL
        )
        OR (
            ended_at IS NOT NULL
            AND ended_by IS NOT NULL
            AND end_reason IS NOT NULL
            AND ended_at >= bound_at
        )
    ),
    FOREIGN KEY (relay_node_id)
        REFERENCES relay_node_assets(instance_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    FOREIGN KEY (gateway_instance_id)
        REFERENCES gateway_instances(instance_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    FOREIGN KEY (evidence_snapshot_id, gateway_account_id)
        REFERENCES gateway_directory_snapshot_items(snapshot_id, account_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    FOREIGN KEY (evidence_snapshot_id, gateway_instance_id)
        REFERENCES gateway_directory_snapshots(snapshot_id, gateway_instance_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    FOREIGN KEY (bound_by)
        REFERENCES control_admin_users(admin_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    FOREIGN KEY (ended_by)
        REFERENCES control_admin_users(admin_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT
);

CREATE UNIQUE INDEX relay_node_gateway_account_bindings_current_node_idx
    ON relay_node_gateway_account_bindings(relay_node_id)
    WHERE ended_at IS NULL;

CREATE UNIQUE INDEX relay_node_gateway_account_bindings_current_account_idx
    ON relay_node_gateway_account_bindings(gateway_instance_id, gateway_account_id)
    WHERE ended_at IS NULL;

CREATE INDEX relay_node_gateway_account_bindings_node_history_idx
    ON relay_node_gateway_account_bindings(relay_node_id, bound_at DESC);

-- +goose StatementBegin
CREATE FUNCTION public.control_protect_relay_node_gateway_account_binding()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
    IF TG_OP IN ('DELETE', 'TRUNCATE') THEN
        RAISE EXCEPTION 'relay node gateway account binding history is immutable'
            USING ERRCODE = '42501';
    END IF;

    IF OLD.ended_at IS NOT NULL THEN
        RAISE EXCEPTION 'closed relay node gateway account binding is immutable'
            USING ERRCODE = '23514';
    END IF;

    IF NEW.binding_id IS DISTINCT FROM OLD.binding_id
       OR NEW.relay_node_id IS DISTINCT FROM OLD.relay_node_id
       OR NEW.gateway_instance_id IS DISTINCT FROM OLD.gateway_instance_id
       OR NEW.gateway_account_id IS DISTINCT FROM OLD.gateway_account_id
       OR NEW.evidence_snapshot_id IS DISTINCT FROM OLD.evidence_snapshot_id
       OR NEW.bound_at IS DISTINCT FROM OLD.bound_at
       OR NEW.bound_by IS DISTINCT FROM OLD.bound_by
       OR NEW.bind_reason IS DISTINCT FROM OLD.bind_reason THEN
        RAISE EXCEPTION 'relay node gateway account binding identity is immutable'
            USING ERRCODE = '23514';
    END IF;

    IF NEW.ended_at IS NULL
       OR NEW.ended_by IS NULL
       OR NEW.end_reason IS NULL THEN
        RAISE EXCEPTION 'relay node gateway account binding close metadata is incomplete'
            USING ERRCODE = '23514';
    END IF;

    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER relay_node_gateway_account_bindings_guard
BEFORE UPDATE OR DELETE ON relay_node_gateway_account_bindings
FOR EACH ROW EXECUTE FUNCTION public.control_protect_relay_node_gateway_account_binding();

CREATE TRIGGER relay_node_gateway_account_bindings_truncate_guard
BEFORE TRUNCATE ON relay_node_gateway_account_bindings
FOR EACH STATEMENT EXECUTE FUNCTION public.control_protect_relay_node_gateway_account_binding();

ALTER TABLE audit_logs DROP CONSTRAINT audit_logs_category_valid;
ALTER TABLE audit_logs ADD CONSTRAINT audit_logs_category_valid CHECK (
    category IN ('bootstrap', 'administrator', 'password', 'mfa', 'session',
                 'reauthentication', 'authorization', 'rate_limit',
                 'account_inventory', 'account_inventory_history', 'relay_binding')
);

ALTER TABLE audit_logs DROP CONSTRAINT audit_logs_action_valid;
ALTER TABLE audit_logs ADD CONSTRAINT audit_logs_action_valid CHECK (
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
        'account_inventory_history.completed',
        'account_inventory_history.failed',
        'relay_binding.bind', 'relay_binding.unbind', 'relay_binding.rebind'
    )
);

ALTER TABLE audit_logs ADD CONSTRAINT audit_logs_relay_binding_shape CHECK (
    (
        category = 'relay_binding'
    ) = (
        action IN (
            'relay_binding.bind',
            'relay_binding.unbind',
            'relay_binding.rebind'
        )
    )
    AND (
        category <> 'relay_binding'
        OR (
        action IN (
            'relay_binding.bind',
            'relay_binding.unbind',
            'relay_binding.rebind'
        )
        AND actor_admin_id IS NOT NULL
        AND details ?& ARRAY[
            'relay_node_id',
            'gateway_instance_id',
            'old_gateway_account_id',
            'new_gateway_account_id',
            'evidence_snapshot_id',
            'reason_code'
        ]
        AND details - ARRAY[
            'relay_node_id',
            'gateway_instance_id',
            'old_gateway_account_id',
            'new_gateway_account_id',
            'evidence_snapshot_id',
            'reason_code'
        ] = '{}'::jsonb
        AND jsonb_typeof(details->'relay_node_id') = 'string'
        AND jsonb_typeof(details->'gateway_instance_id') = 'string'
        AND jsonb_typeof(details->'evidence_snapshot_id') = 'string'
        AND (details->>'relay_node_id') ~
            '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
        AND (details->>'gateway_instance_id') ~
            '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
        AND (details->>'evidence_snapshot_id') ~
            '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
        AND (
            (action = 'relay_binding.bind'
             AND jsonb_typeof(details->'old_gateway_account_id') = 'null'
             AND jsonb_typeof(details->'new_gateway_account_id') = 'number'
             AND details->>'reason_code' = 'administrator_bind')
            OR
            (action = 'relay_binding.unbind'
             AND jsonb_typeof(details->'old_gateway_account_id') = 'number'
             AND jsonb_typeof(details->'new_gateway_account_id') = 'null'
             AND details->>'reason_code' = 'administrator_unbind')
            OR
            (action = 'relay_binding.rebind'
             AND jsonb_typeof(details->'old_gateway_account_id') = 'number'
             AND jsonb_typeof(details->'new_gateway_account_id') = 'number'
             AND details->>'reason_code' = 'administrator_rebind')
        )
        AND (
            jsonb_typeof(details->'old_gateway_account_id') = 'null'
            OR (
                (details->>'old_gateway_account_id') ~ '^[1-9][0-9]{0,18}$'
                AND (details->>'old_gateway_account_id')::numeric <= 9223372036854775807
            )
        )
        AND (
            jsonb_typeof(details->'new_gateway_account_id') = 'null'
            OR (
                (details->>'new_gateway_account_id') ~ '^[1-9][0-9]{0,18}$'
                AND (details->>'new_gateway_account_id')::numeric <= 9223372036854775807
            )
        )
        )
    )
);

REVOKE ALL ON TABLE relay_node_gateway_account_bindings
FROM PUBLIC, relay_control_runtime;

GRANT SELECT, INSERT ON TABLE relay_node_gateway_account_bindings
TO relay_control_runtime;

GRANT UPDATE (ended_at, ended_by, end_reason)
ON TABLE relay_node_gateway_account_bindings
TO relay_control_runtime;

REVOKE EXECUTE ON FUNCTION public.control_protect_relay_node_gateway_account_binding()
FROM PUBLIC;

GRANT EXECUTE ON FUNCTION public.control_protect_relay_node_gateway_account_binding()
TO relay_control_runtime;

-- +goose Down

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM relay_node_gateway_account_bindings LIMIT 1)
       OR EXISTS (SELECT 1 FROM audit_logs WHERE category = 'relay_binding' LIMIT 1) THEN
        RAISE EXCEPTION 'cannot rollback relay node gateway account binding foundation when binding or audit records exist'
            USING ERRCODE = '55000';
    END IF;
END;
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION public.control_protect_relay_node_gateway_account_binding()
FROM relay_control_runtime;

REVOKE ALL ON TABLE relay_node_gateway_account_bindings
FROM relay_control_runtime;

ALTER TABLE audit_logs DROP CONSTRAINT audit_logs_relay_binding_shape;

ALTER TABLE audit_logs DROP CONSTRAINT audit_logs_action_valid;
ALTER TABLE audit_logs ADD CONSTRAINT audit_logs_action_valid CHECK (
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
        'account_inventory_history.completed',
        'account_inventory_history.failed'
    )
);

ALTER TABLE audit_logs DROP CONSTRAINT audit_logs_category_valid;
ALTER TABLE audit_logs ADD CONSTRAINT audit_logs_category_valid CHECK (
    category IN ('bootstrap', 'administrator', 'password', 'mfa', 'session',
                 'reauthentication', 'authorization', 'rate_limit',
                 'account_inventory', 'account_inventory_history')
);

DROP TRIGGER relay_node_gateway_account_bindings_truncate_guard
    ON relay_node_gateway_account_bindings;
DROP TRIGGER relay_node_gateway_account_bindings_guard
    ON relay_node_gateway_account_bindings;
DROP FUNCTION public.control_protect_relay_node_gateway_account_binding();
DROP TABLE relay_node_gateway_account_bindings;
