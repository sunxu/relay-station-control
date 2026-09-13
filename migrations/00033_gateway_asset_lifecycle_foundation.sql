-- +goose Up

-- Phase 6 is a forward-only compatibility boundary.  The marker is kept
-- deliberately small: the external startup gate reads it, while the
-- application role cannot lower or remove it.
CREATE TABLE public.control_runtime_compatibility (
    singleton_id smallint PRIMARY KEY DEFAULT 1,
    schema_version integer NOT NULL DEFAULT 1,
    phase6_evidence_floor integer NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT control_runtime_compatibility_singleton_check
        CHECK (singleton_id = 1),
    CONSTRAINT control_runtime_compatibility_schema_version_check
        CHECK (schema_version = 1),
    CONSTRAINT control_runtime_compatibility_floor_check
        CHECK (phase6_evidence_floor IN (0, 1))
);

-- +goose StatementBegin
CREATE FUNCTION public.control_protect_runtime_compatibility()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
    IF TG_OP IN ('DELETE', 'TRUNCATE') THEN
        RAISE EXCEPTION 'runtime compatibility marker cannot be deleted'
            USING ERRCODE = '42501';
    END IF;
    IF NEW.singleton_id <> OLD.singleton_id
       OR NEW.schema_version <> OLD.schema_version
       OR NEW.phase6_evidence_floor < OLD.phase6_evidence_floor THEN
        RAISE EXCEPTION 'runtime compatibility marker is immutable or cannot move backwards'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER control_runtime_compatibility_guard
BEFORE UPDATE OR DELETE ON public.control_runtime_compatibility
FOR EACH ROW EXECUTE FUNCTION public.control_protect_runtime_compatibility();
CREATE TRIGGER control_runtime_compatibility_truncate_guard
BEFORE TRUNCATE ON public.control_runtime_compatibility
FOR EACH STATEMENT EXECUTE FUNCTION public.control_protect_runtime_compatibility();

INSERT INTO public.control_runtime_compatibility (singleton_id, schema_version, phase6_evidence_floor)
VALUES (1, 1, 1);

ALTER TABLE public.audit_logs DROP CONSTRAINT audit_logs_category_valid;
ALTER TABLE public.audit_logs ADD CONSTRAINT audit_logs_category_valid CHECK (
    category IN ('bootstrap', 'administrator', 'password', 'mfa', 'session',
                 'reauthentication', 'authorization', 'rate_limit',
                 'account_inventory', 'account_inventory_history', 'relay_binding',
                 'asset', 'asset_gateway')
);
ALTER TABLE public.audit_logs DROP CONSTRAINT audit_logs_action_valid;
ALTER TABLE public.audit_logs ADD CONSTRAINT audit_logs_action_valid CHECK (
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
        'account_inventory_history.completed', 'account_inventory_history.failed',
        'relay_binding.bind', 'relay_binding.unbind', 'relay_binding.rebind',
        'asset.gateway_directory_reader_configured',
        'gateway.register', 'gateway.edit', 'gateway.retire', 'gateway.replace',
        'gateway.health', 'gateway.connection_test'
    )
);

-- Stop application writers at the DDL boundary.  The order is shared by the
-- Gateway lifecycle and Directory/binding code so the FK transition cannot
-- observe a partially changed referenced key.
LOCK TABLE
    public.gateway_instances,
    public.gateway_directory_ingestion_runs,
    public.gateway_directory_snapshots,
    public.gateway_directory_current_state,
    public.relay_node_gateway_account_bindings
IN ACCESS EXCLUSIVE MODE;

-- Gateway lifecycle commands close current bindings with a machine-readable
-- reason distinct from administrator bind/rebind operations.
ALTER TABLE public.relay_node_gateway_account_bindings
    DROP CONSTRAINT relay_node_gateway_account_bindings_end_reason_fixed,
    ADD CONSTRAINT relay_node_gateway_account_bindings_end_reason_fixed CHECK (
        end_reason IS NULL OR end_reason IN (
            'administrator_unbind',
            'administrator_rebind',
            'gateway_retired',
            'gateway_replaced'
        )
    );

-- Gateway lifecycle fences are durable terminal outcomes, distinct from
-- malformed Directory payloads or unrelated storage corruption. Recompose both
-- the failure allowlist and complete state-shape constraint in this migration.
ALTER TABLE public.gateway_directory_ingestion_runs
    DROP CONSTRAINT gateway_directory_ingestion_runs_failure_fixed,
    ADD CONSTRAINT gateway_directory_ingestion_runs_failure_fixed CHECK (
        last_failure_class IS NULL OR last_failure_class IN (
            'transport', 'timeout', 'partial_read', 'http_429', 'http_5xx',
            'http_non_retryable', 'contract_invalid', 'source_time_invalid',
            'hard_limit', 'secret_unavailable', 'finalize_transient',
            'lease_lost', 'unknown_execution', 'start_deadline_expired',
            'gateway_retired', 'gateway_replaced'
        )
    ),
    DROP CONSTRAINT gateway_directory_ingestion_runs_state_shape,
    ADD
    CONSTRAINT gateway_directory_ingestion_runs_state_shape CHECK (
        (
            status = 'pending'
            AND attempt_count = 0
            AND first_started_at IS NULL
            AND last_started_at IS NULL
            AND lease_expires_at IS NULL
            AND lease_fencing_token IS NULL
            AND terminal_at IS NULL
            AND outcome IS NULL
            AND last_failure_class IS NULL
            AND source_generated_at IS NULL
            AND received_at IS NULL
            AND content_fingerprint IS NULL
            AND snapshot_id IS NULL
            AND account_count IS NULL
        )
        OR (
            status = 'running'
            AND attempt_count BETWEEN 1 AND 2
            AND first_started_at IS NOT NULL
            AND last_started_at IS NOT NULL
            AND lease_expires_at = last_started_at + interval '15 seconds'
            AND lease_fencing_token IS NOT NULL
            AND terminal_at IS NULL
            AND outcome IS NULL
            AND source_generated_at IS NULL
            AND received_at IS NULL
            AND content_fingerprint IS NULL
            AND snapshot_id IS NULL
            AND account_count IS NULL
            AND (
                (attempt_count = 1 AND last_failure_class IS NULL)
                OR (
                    attempt_count = 2
                    AND last_failure_class IN (
                        'transport', 'timeout', 'partial_read', 'http_429',
                        'http_5xx', 'finalize_transient', 'lease_lost',
                        'unknown_execution'
                    )
                )
            )
        )
        OR (
            status = 'retry_wait'
            AND attempt_count = 1
            AND first_started_at IS NOT NULL
            AND last_started_at IS NOT NULL
            AND lease_expires_at IS NULL
            AND lease_fencing_token IS NULL
            AND terminal_at IS NULL
            AND outcome IS NULL
            AND source_generated_at IS NULL
            AND received_at IS NULL
            AND content_fingerprint IS NULL
            AND snapshot_id IS NULL
            AND account_count IS NULL
            AND last_failure_class IN (
                'transport', 'timeout', 'partial_read', 'http_429',
                'http_5xx', 'finalize_transient', 'lease_lost',
                'unknown_execution'
            )
        )
        OR (
            status = 'succeeded'
            AND attempt_count BETWEEN 1 AND 2
            AND first_started_at IS NOT NULL
            AND last_started_at IS NOT NULL
            AND lease_expires_at IS NULL
            AND lease_fencing_token IS NULL
            AND terminal_at IS NOT NULL
            AND outcome IN ('changed', 'unchanged')
            AND source_generated_at IS NOT NULL
            AND received_at IS NOT NULL
            AND terminal_at = received_at
            AND content_fingerprint IS NOT NULL
            AND octet_length(content_fingerprint) = 32
            AND snapshot_id IS NOT NULL
            AND account_count BETWEEN 0 AND 10000
            AND (
                (attempt_count = 1 AND last_failure_class IS NULL)
                OR (
                    attempt_count = 2
                    AND last_failure_class IN (
                        'transport', 'timeout', 'partial_read', 'http_429',
                        'http_5xx', 'finalize_transient', 'lease_lost',
                        'unknown_execution'
                    )
                )
            )
        )
        OR (
            status = 'failed'
            AND attempt_count BETWEEN 0 AND 2
            AND lease_expires_at IS NULL
            AND lease_fencing_token IS NULL
            AND terminal_at IS NOT NULL
            AND outcome IS NULL
            AND source_generated_at IS NULL
            AND received_at IS NULL
            AND content_fingerprint IS NULL
            AND snapshot_id IS NULL
            AND account_count IS NULL
            AND last_failure_class IN (
                'transport', 'timeout', 'partial_read', 'http_429',
                'http_5xx', 'http_non_retryable', 'contract_invalid',
                'source_time_invalid', 'hard_limit', 'secret_unavailable',
                'finalize_transient', 'lease_lost', 'unknown_execution',
                'start_deadline_expired', 'gateway_retired', 'gateway_replaced'
            )
            AND (
                (
                    attempt_count = 0
                    AND first_started_at IS NULL
                    AND last_started_at IS NULL
                    AND last_failure_class = 'start_deadline_expired'
                )
                OR (
                    attempt_count BETWEEN 1 AND 2
                    AND first_started_at IS NOT NULL
                    AND last_started_at IS NOT NULL
                )
            )
        )
    );

-- Control-managed Gateway endpoints are HTTP-only in the archived transport
-- baseline.  Do not rewrite a legacy target: fail the forward migration so
-- an operator must correct the configuration explicitly.
-- +goose StatementBegin
DO $$
BEGIN
    -- Reuse the existing database validator first so malformed authorities,
    -- ports, credentials, queries, fragments, and encoded path tricks cannot
    -- be grandfathered into the floor-1 current slot.
    PERFORM public.control_normalize_asset_endpoint(management_endpoint)
    FROM public.gateway_instances;
    IF EXISTS (
        SELECT 1
        FROM public.gateway_instances
        WHERE left(management_endpoint, 7) <> 'http://'
           OR (
                strpos(substr(management_endpoint, 8), '/') <> 0
                AND strpos(substr(management_endpoint, 8), '/') <> length(substr(management_endpoint, 8))
           )
    ) THEN
        RAISE EXCEPTION 'gateway migration requires HTTP-only management endpoints; legacy target must be corrected first'
            USING ERRCODE = '22023';
    END IF;
END;
$$;
-- +goose StatementEnd

-- Add lifecycle state in a nullable/backfillable form first.  Existing rows
-- are then made valid before strict constraints and NOT NULL are installed.
ALTER TABLE public.gateway_instances
    ADD COLUMN lifecycle_status text,
    ADD COLUMN retired_at timestamptz,
    ADD COLUMN retired_by uuid,
    ADD COLUMN retire_reason text,
    ADD COLUMN revision bigint;

UPDATE public.gateway_instances
SET lifecycle_status = 'active',
    retired_at = NULL,
    retired_by = NULL,
    retire_reason = NULL,
    revision = 1;

ALTER TABLE public.gateway_instances
    ALTER COLUMN lifecycle_status SET DEFAULT 'active',
    ALTER COLUMN lifecycle_status SET NOT NULL,
    ALTER COLUMN revision SET DEFAULT 1,
    ALTER COLUMN revision SET NOT NULL;

-- Preserve the existing UNIQUE(instance_id) while the new PK is installed;
-- all direct FKs remain valid until they are explicitly recreated below.
CREATE UNIQUE INDEX gateway_instances_instance_id_pk_stage
    ON public.gateway_instances (instance_id);

ALTER TABLE public.gateway_instances
    DROP CONSTRAINT gateway_instances_pkey;

ALTER TABLE public.gateway_instances
    ADD CONSTRAINT gateway_instances_pkey
    PRIMARY KEY USING INDEX gateway_instances_instance_id_pk_stage;

ALTER TABLE public.gateway_directory_ingestion_runs
    DROP CONSTRAINT gateway_directory_ingestion_runs_gateway_instance_id_fkey;
ALTER TABLE public.gateway_directory_snapshots
    DROP CONSTRAINT gateway_directory_snapshots_gateway_instance_id_fkey;
ALTER TABLE public.gateway_directory_current_state
    DROP CONSTRAINT gateway_directory_current_state_gateway_instance_id_fkey;
ALTER TABLE public.relay_node_gateway_account_bindings
    DROP CONSTRAINT relay_node_gateway_account_bindings_gateway_instance_id_fkey;

-- The old UNIQUE(instance_id) is no longer needed: the new physical PK is the
-- sole referenced key.  Dropping it here also avoids a duplicate backing
-- index in the final schema.
ALTER TABLE public.gateway_instances
    DROP CONSTRAINT gateway_instances_instance_id_key;

ALTER TABLE public.gateway_directory_ingestion_runs
    ADD CONSTRAINT gateway_directory_ingestion_runs_gateway_instance_id_fkey
    FOREIGN KEY (gateway_instance_id)
    REFERENCES public.gateway_instances(instance_id)
    ON UPDATE RESTRICT ON DELETE RESTRICT;
ALTER TABLE public.gateway_directory_snapshots
    ADD CONSTRAINT gateway_directory_snapshots_gateway_instance_id_fkey
    FOREIGN KEY (gateway_instance_id)
    REFERENCES public.gateway_instances(instance_id)
    ON UPDATE RESTRICT ON DELETE RESTRICT;
ALTER TABLE public.gateway_directory_current_state
    ADD CONSTRAINT gateway_directory_current_state_gateway_instance_id_fkey
    FOREIGN KEY (gateway_instance_id)
    REFERENCES public.gateway_instances(instance_id)
    ON UPDATE RESTRICT ON DELETE RESTRICT;
ALTER TABLE public.relay_node_gateway_account_bindings
    ADD CONSTRAINT relay_node_gateway_account_bindings_gateway_instance_id_fkey
    FOREIGN KEY (gateway_instance_id)
    REFERENCES public.gateway_instances(instance_id)
    ON UPDATE RESTRICT ON DELETE RESTRICT;

ALTER TABLE public.gateway_instances
    ALTER COLUMN singleton_id DROP NOT NULL;

CREATE UNIQUE INDEX gateway_instances_current_slot_uidx
    ON public.gateway_instances (singleton_id)
    WHERE singleton_id = 1;

ALTER TABLE public.gateway_instances
    ADD CONSTRAINT gateway_instances_http_only_check
        CHECK (
            rtrim(public.control_normalize_asset_endpoint(management_endpoint), '/')
                = rtrim(management_endpoint, '/')
            AND
            left(management_endpoint, 7) = 'http://'
            AND (
                strpos(substr(management_endpoint, 8), '/') = 0
                OR strpos(substr(management_endpoint, 8), '/') = length(substr(management_endpoint, 8))
            )
        ),
    ADD CONSTRAINT gateway_instances_lifecycle_status_check
        CHECK (lifecycle_status IN ('active', 'retired')),
    ADD CONSTRAINT gateway_instances_revision_positive_check
        CHECK (revision >= 1),
    ADD CONSTRAINT gateway_instances_lifecycle_shape_check
        CHECK (
            (
                lifecycle_status = 'active'
                AND singleton_id = 1
                AND retired_at IS NULL
                AND retired_by IS NULL
                AND retire_reason IS NULL
            ) OR (
                lifecycle_status = 'retired'
                AND singleton_id IS NULL
                AND retired_at IS NOT NULL
                AND retired_by IS NOT NULL
                AND retire_reason IN ('administrator_retire', 'replacement')
                AND retired_at >= created_at
            )
        ),
    ADD CONSTRAINT gateway_instances_retired_by_fkey
        FOREIGN KEY (retired_by)
        REFERENCES public.control_admin_users(admin_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT;

-- Recompose the existing sensitive Directory target entry point with the
-- Phase 6 lifecycle/current-slot fence.  A stale worker receives no target
-- after Retire/Replace and therefore issues no new outbound request.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_query_gateway_directory_target_v1(
    target_run_id uuid,
    target_gateway_id uuid,
    target_fencing_token uuid
) RETURNS TABLE (
    instance_id uuid,
    management_endpoint text,
    reader_secret_ref text
)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
    SELECT gateway.instance_id, gateway.management_endpoint, gateway.reader_secret_ref
    FROM public.gateway_directory_ingestion_runs AS run
    JOIN public.gateway_instances AS gateway
      ON gateway.instance_id = run.gateway_instance_id
    WHERE run.ingestion_run_id = target_run_id
      AND run.gateway_instance_id = target_gateway_id
      AND run.lease_fencing_token = target_fencing_token
      AND run.status = 'running'
      AND run.lease_expires_at > statement_timestamp()
      AND gateway.lifecycle_status = 'active'
      AND gateway.singleton_id = 1
      AND gateway.reader_secret_ref IS NOT NULL
$$;
-- +goose StatementEnd

-- Keep the registrar helper usable for the new nullable current-slot model.
-- Its write surface remains the existing SECURITY DEFINER function; the
-- Gateway mutation API is implemented by later tasks.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_register_gateway(
    gateway_instance_id uuid,
    gateway_display_name text,
    gateway_endpoint text,
    gateway_reader_secret_ref text DEFAULT NULL
) RETURNS uuid
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    normalized_endpoint text;
    existing public.gateway_instances%ROWTYPE;
BEGIN
    IF gateway_instance_id IS NULL
       OR gateway_display_name IS NULL
       OR gateway_endpoint IS NULL
       OR char_length(gateway_display_name) NOT BETWEEN 1 AND 100
       OR gateway_display_name !~ '^\S(?:.*\S)?$'
       OR gateway_display_name ~ '[[:cntrl:]]'
       OR (gateway_reader_secret_ref IS NOT NULL
           AND NOT public.control_valid_secret_reference(gateway_reader_secret_ref)) THEN
        RAISE EXCEPTION 'gateway registration rejected' USING ERRCODE = '22023';
    END IF;
    normalized_endpoint := public.control_normalize_asset_endpoint(gateway_endpoint);
    IF left(normalized_endpoint, 7) <> 'http://' THEN
        RAISE EXCEPTION 'gateway registration requires an HTTP-only endpoint'
            USING ERRCODE = '22023';
    END IF;
    INSERT INTO public.gateway_instances (
        singleton_id, instance_id, display_name, management_endpoint, reader_secret_ref
    ) VALUES (
        1, gateway_instance_id, gateway_display_name, normalized_endpoint, gateway_reader_secret_ref
    ) ON CONFLICT DO NOTHING;
    SELECT * INTO existing
    FROM public.gateway_instances
    WHERE instance_id = gateway_instance_id;
    IF NOT FOUND
       OR existing.singleton_id IS DISTINCT FROM 1
       OR existing.lifecycle_status IS DISTINCT FROM 'active'
       OR existing.display_name IS DISTINCT FROM gateway_display_name
       OR existing.management_endpoint IS DISTINCT FROM normalized_endpoint
       OR existing.reader_secret_ref IS DISTINCT FROM gateway_reader_secret_ref THEN
        RAISE EXCEPTION 'gateway registration conflicts with existing identity or current slot'
            USING ERRCODE = '23505';
    END IF;
    RETURN existing.instance_id;
END;
$$;
-- +goose StatementEnd

-- Phase 6 Gateway registration is a durable admin command.  The legacy
-- registrar entry point has no command receipt or success audit and must not
-- remain an operational bypass once floor 1 is installed.
REVOKE EXECUTE ON FUNCTION public.control_register_gateway(uuid, text, text, text)
    FROM PUBLIC, relay_control_asset_registrar, relay_control_runtime;

-- Shared immutable command receipts.  The application stores the complete
-- sanitized success body in sanitized_result and its original 2xx status so
-- a replay never reconstructs a historical result from mutable asset state.
CREATE TABLE public.asset_admin_command_receipts (
    command_id uuid PRIMARY KEY,
    command_kind text NOT NULL,
    intent_encoding_version smallint NOT NULL DEFAULT 1,
    canonical_intent_hash bytea NOT NULL,
    sanitized_result jsonb NOT NULL,
    response_status smallint NOT NULL,
    actor_admin_id uuid NOT NULL,
    committed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    secret_fingerprint_key_version smallint,
    CONSTRAINT asset_admin_command_receipts_kind_check CHECK (
        octet_length(command_kind) BETWEEN 1 AND 128
        AND command_kind !~ '[[:cntrl:][:space:]]'
    ),
    CONSTRAINT asset_admin_command_receipts_encoding_check CHECK (
        intent_encoding_version = 1
    ),
    CONSTRAINT asset_admin_command_receipts_hash_check CHECK (
        octet_length(canonical_intent_hash) = 32
    ),
    CONSTRAINT asset_admin_command_receipts_result_check CHECK (
        jsonb_typeof(sanitized_result) = 'object'
        AND octet_length(sanitized_result::text) <= 16384
    ),
    CONSTRAINT asset_admin_command_receipts_status_check CHECK (
        response_status BETWEEN 200 AND 299
    ),
    CONSTRAINT asset_admin_command_receipts_secret_version_check CHECK (
        secret_fingerprint_key_version IS NULL
        OR secret_fingerprint_key_version = 1
    ),
    CONSTRAINT asset_admin_command_receipts_actor_fkey
        FOREIGN KEY (actor_admin_id)
        REFERENCES public.control_admin_users(admin_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT
);

-- +goose StatementBegin
CREATE FUNCTION public.control_reject_asset_admin_command_receipt_mutation()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
    RAISE EXCEPTION 'asset admin command receipts are immutable'
        USING ERRCODE = '42501';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER asset_admin_command_receipts_immutable
BEFORE UPDATE OR DELETE ON public.asset_admin_command_receipts
FOR EACH ROW EXECUTE FUNCTION public.control_reject_asset_admin_command_receipt_mutation();
CREATE TRIGGER asset_admin_command_receipts_truncate_guard
BEFORE TRUNCATE ON public.asset_admin_command_receipts
FOR EACH STATEMENT EXECUTE FUNCTION public.control_reject_asset_admin_command_receipt_mutation();

-- Direct-replacement lineage is intentionally typed to Gateway identities.
-- The insert trigger supplies a database guard for cycles in addition to the
-- command-level new-identity precondition.
CREATE TABLE public.gateway_asset_replacements (
    replacement_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    old_instance_id uuid NOT NULL,
    new_instance_id uuid NOT NULL,
    replaced_at timestamptz NOT NULL,
    replaced_by uuid NOT NULL,
    command_id uuid NOT NULL,
    CONSTRAINT gateway_asset_replacements_old_unique UNIQUE (old_instance_id),
    CONSTRAINT gateway_asset_replacements_new_unique UNIQUE (new_instance_id),
    CONSTRAINT gateway_asset_replacements_distinct_check
        CHECK (old_instance_id <> new_instance_id),
    CONSTRAINT gateway_asset_replacements_old_fkey
        FOREIGN KEY (old_instance_id)
        REFERENCES public.gateway_instances(instance_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    CONSTRAINT gateway_asset_replacements_new_fkey
        FOREIGN KEY (new_instance_id)
        REFERENCES public.gateway_instances(instance_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    CONSTRAINT gateway_asset_replacements_actor_fkey
        FOREIGN KEY (replaced_by)
        REFERENCES public.control_admin_users(admin_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT
);

-- +goose StatementBegin
CREATE FUNCTION public.control_reject_gateway_asset_replacement_cycle()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'gateway replacement lineage is immutable'
            USING ERRCODE = '42501';
    END IF;
    IF EXISTS (
        WITH RECURSIVE reachable(instance_id, path) AS (
            SELECT NEW.new_instance_id, ARRAY[NEW.new_instance_id]::uuid[]
            UNION ALL
            SELECT replacement.new_instance_id,
                   reachable.path || replacement.new_instance_id
            FROM public.gateway_asset_replacements AS replacement
            JOIN reachable
              ON replacement.old_instance_id = reachable.instance_id
            WHERE NOT replacement.new_instance_id = ANY(reachable.path)
        )
        SELECT 1
        FROM reachable
        WHERE instance_id = NEW.old_instance_id
    ) THEN
        RAISE EXCEPTION 'gateway replacement lineage cycle is forbidden'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER gateway_asset_replacements_cycle_guard
BEFORE INSERT OR UPDATE OR DELETE ON public.gateway_asset_replacements
FOR EACH ROW EXECUTE FUNCTION public.control_reject_gateway_asset_replacement_cycle();
CREATE TRIGGER gateway_asset_replacements_truncate_guard
BEFORE TRUNCATE ON public.gateway_asset_replacements
FOR EACH STATEMENT EXECUTE FUNCTION public.control_reject_gateway_asset_replacement_cycle();

-- Serialize lineage inserts so the recursive cycle check observes a single
-- committed predecessor/successor history even when commands race.
-- +goose StatementBegin
CREATE FUNCTION public.control_serialize_gateway_asset_replacement_insert()
RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
BEGIN
    LOCK TABLE public.gateway_asset_replacements IN SHARE ROW EXCLUSIVE MODE;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

ALTER FUNCTION public.control_serialize_gateway_asset_replacement_insert()
    OWNER TO relay_control_migrator;
REVOKE EXECUTE ON FUNCTION public.control_serialize_gateway_asset_replacement_insert()
    FROM PUBLIC, relay_control_runtime, relay_control_asset_registrar;

-- PostgreSQL fires same-kind triggers in name order.  The 00 prefix makes the
-- table serialization lock run before the recursive cycle guard evaluates
-- committed lineage.
CREATE TRIGGER gateway_asset_replacements_00_insert_lock
BEFORE INSERT ON public.gateway_asset_replacements
FOR EACH ROW EXECUTE FUNCTION public.control_serialize_gateway_asset_replacement_insert();

-- Only current reads are needed by the existing runtime role at this stage;
-- mutation functions and Gateway API handlers arrive in later tasks.
REVOKE ALL ON TABLE public.control_runtime_compatibility,
    public.asset_admin_command_receipts,
    public.gateway_asset_replacements
FROM PUBLIC, relay_control_runtime, relay_control_asset_registrar;
GRANT SELECT ON TABLE public.control_runtime_compatibility TO relay_control_runtime;
GRANT SELECT, INSERT ON TABLE public.asset_admin_command_receipts TO relay_control_runtime;
GRANT SELECT, INSERT ON TABLE public.gateway_asset_replacements TO relay_control_runtime;

GRANT SELECT (
    singleton_id,
    instance_id,
    display_name,
    management_endpoint,
    reader_secret_configured,
    created_at,
    updated_at,
    lifecycle_status,
    retired_at,
    retired_by,
    retire_reason,
    revision
) ON public.gateway_instances TO relay_control_runtime;
GRANT INSERT (
    singleton_id,
    instance_id,
    display_name,
    management_endpoint,
    reader_secret_ref,
    lifecycle_status,
    revision
) ON public.gateway_instances TO relay_control_runtime;
GRANT UPDATE (
    singleton_id,
    display_name,
    management_endpoint,
    reader_secret_ref,
    lifecycle_status,
    retired_at,
    retired_by,
    retire_reason,
    revision,
    updated_at
) ON public.gateway_instances TO relay_control_runtime;
GRANT EXECUTE ON FUNCTION public.control_normalize_asset_endpoint(text),
    public.control_valid_secret_reference(text)
TO relay_control_runtime;

-- +goose Down
-- Phase 6 durable evidence is a compatibility barrier.  Removing this
-- schema would make an old binary ambiguous and is therefore unsupported.
-- +goose StatementBegin
DO $$
BEGIN
    RAISE EXCEPTION 'gateway asset lifecycle foundation migration is forward-only'
        USING ERRCODE = '55000';
END;
$$;
-- +goose StatementEnd
