-- +goose Up

-- Stage 0 writes new Node/Gateway asset commands with intent encoding v2 while
-- retaining immutable v1 receipts for historical replay.
ALTER TABLE public.asset_admin_command_receipts
    DROP CONSTRAINT asset_admin_command_receipts_encoding_check,
    ADD CONSTRAINT asset_admin_command_receipts_encoding_check
        CHECK (intent_encoding_version IN (1, 2));

-- Stage 0 starts from a fresh/re-registered asset state.  Do this guard before
-- any DDL so a legacy reference cannot leave a partially upgraded database.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.relay_node_assets WHERE reader_secret_ref IS NOT NULL)
       OR EXISTS (SELECT 1 FROM public.gateway_instances WHERE reader_secret_ref IS NOT NULL) THEN
        RAISE EXCEPTION 'migration 51 requires all legacy reader_secret_ref values to be NULL'
            USING ERRCODE = '23514';
    END IF;
END;
$$;
-- +goose StatementEnd



ALTER TABLE public.relay_node_assets
    DROP COLUMN reader_secret_configured,
    ADD COLUMN management_credential_sealed bytea,
    ADD COLUMN reader_secret_configured boolean GENERATED ALWAYS AS (
        management_credential_sealed IS NOT NULL
    ) STORED,
    ADD CONSTRAINT relay_node_assets_management_credential_sealed_shape CHECK (
        management_credential_sealed IS NULL
        OR octet_length(management_credential_sealed) >= 29
    );

ALTER TABLE public.gateway_instances
    DROP COLUMN reader_secret_configured,
    ADD COLUMN directory_credential_sealed bytea,
    ADD COLUMN reader_secret_configured boolean GENERATED ALWAYS AS (
        directory_credential_sealed IS NOT NULL
    ) STORED,
    ADD CONSTRAINT gateway_instances_directory_credential_sealed_shape CHECK (
        directory_credential_sealed IS NULL
        OR octet_length(directory_credential_sealed) >= 29
    );

-- Stage 0 Node Register owns the initial sealed state in the same INSERT as
-- the asset row. It never accepts or persists a legacy secret reference.
-- +goose StatementBegin
CREATE FUNCTION public.control_register_relay_node_asset_stage0_v1(
    requested_instance_id uuid,
    requested_display_name text,
    requested_node_type text,
    requested_driver_contract_version text,
    requested_management_endpoint text,
    requested_management_credential_sealed bytea,
    requested_capabilities text[]
) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    created_instance_id uuid;
BEGIN
    IF requested_instance_id IS NULL
       OR requested_capabilities IS NULL
       OR cardinality(requested_capabilities) < 1
       OR cardinality(requested_capabilities) <> (
           SELECT count(DISTINCT capability) FROM unnest(requested_capabilities) capability
       )
       OR EXISTS (
           SELECT 1
           FROM unnest(requested_capabilities) requested(capability)
           LEFT JOIN public.driver_capabilities supported
             ON supported.node_type = requested_node_type
            AND supported.driver_contract_version = requested_driver_contract_version
            AND supported.capability = requested.capability
           WHERE supported.capability IS NULL
       ) THEN
        RAISE EXCEPTION 'invalid initial relay node capability declaration'
            USING ERRCODE = '23514';
    END IF;
    IF requested_management_credential_sealed IS NOT NULL
       AND octet_length(requested_management_credential_sealed) < 29 THEN
        RAISE EXCEPTION 'invalid sealed node credential' USING ERRCODE = '22023';
    END IF;
    INSERT INTO public.relay_node_assets(
        instance_id, display_name, node_type, driver_contract_version,
        management_endpoint, reader_secret_ref, management_credential_sealed,
        lifecycle_status, revision
    ) VALUES (
        requested_instance_id, requested_display_name, requested_node_type,
        requested_driver_contract_version, requested_management_endpoint,
        NULL, requested_management_credential_sealed, 'active', 1
    ) RETURNING instance_id INTO created_instance_id;
    INSERT INTO public.node_capabilities(instance_id, node_type, driver_contract_version, capability)
    SELECT created_instance_id, requested_node_type, requested_driver_contract_version, requested.capability
    FROM unnest(requested_capabilities) requested(capability)
    ORDER BY requested.capability;
    RETURN created_instance_id;
END;
$$;
-- +goose StatementEnd

-- Stage 0 Node Edit owns the resolved asset fields, sealed state, and revision
-- advance in one fixed UPDATE. PATCH tri-state resolution remains in the
-- repository; this function only accepts the resulting action and value.
-- +goose StatementBegin
CREATE FUNCTION public.control_edit_relay_node_asset_stage0_v1(
    target_instance_id uuid,
    expected_revision bigint,
    resolved_display_name text,
    resolved_management_endpoint text,
    credential_action text,
    sealed_value bytea
) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    current_lifecycle text;
    current_revision bigint;
BEGIN
    IF target_instance_id IS NULL OR expected_revision < 1 THEN
        RAISE EXCEPTION 'invalid node edit identity or revision' USING ERRCODE = '22023';
    END IF;
    IF credential_action IS NULL
       OR credential_action NOT IN ('keep', 'set', 'clear') THEN
        RAISE EXCEPTION 'invalid node credential action' USING ERRCODE = '22023';
    END IF;
    IF credential_action = 'set'
       AND (sealed_value IS NULL OR octet_length(sealed_value) < 29) THEN
        RAISE EXCEPTION 'invalid sealed node credential' USING ERRCODE = '22023';
    END IF;
    IF credential_action IN ('keep', 'clear') AND sealed_value IS NOT NULL THEN
        RAISE EXCEPTION 'sealed node credential is not allowed for this action' USING ERRCODE = '22023';
    END IF;

    UPDATE public.relay_node_assets
    SET display_name = resolved_display_name,
        management_endpoint = resolved_management_endpoint,
        management_credential_sealed = CASE credential_action
            WHEN 'keep' THEN management_credential_sealed
            WHEN 'set' THEN sealed_value
            WHEN 'clear' THEN NULL
        END,
        revision = revision + 1,
        updated_at = clock_timestamp()
    WHERE instance_id = target_instance_id
      AND lifecycle_status = 'active'
      AND revision = expected_revision
      AND revision < 9223372036854775807;

    IF NOT FOUND THEN
        SELECT lifecycle_status, revision
          INTO current_lifecycle, current_revision
          FROM public.relay_node_assets
         WHERE instance_id = target_instance_id
         FOR UPDATE;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'node lifecycle target not found' USING ERRCODE = 'P0002';
        ELSIF current_lifecycle <> 'active' THEN
            RAISE EXCEPTION 'node lifecycle target retired' USING ERRCODE = 'P0003';
        ELSIF current_revision = 9223372036854775807 THEN
            RAISE EXCEPTION 'node asset revision exhausted' USING ERRCODE = 'P0004';
        ELSE
            RAISE EXCEPTION 'node asset revision stale' USING ERRCODE = 'P0005';
        END IF;
    END IF;
END;
$$;
-- +goose StatementEnd

-- Stage 0 Node Retire owns the lifecycle transition and credential erase in a
-- single fixed UPDATE. External monitoring, binding, audit, and receipt work
-- remains in the repository transaction around this row mutation.
-- +goose StatementBegin
CREATE FUNCTION public.control_retire_relay_node_asset_stage0_v1(
    target_instance_id uuid,
    expected_revision bigint,
    retire_boundary timestamptz,
    retired_by_actor uuid,
    retire_reason_value text,
    updated_boundary timestamptz
) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    current_lifecycle text;
    current_revision bigint;
BEGIN
    IF target_instance_id IS NULL
       OR expected_revision < 1
       OR retire_boundary IS NULL
       OR retired_by_actor IS NULL
       OR updated_boundary IS NULL
       OR retire_reason_value IS NULL
       OR retire_reason_value <> 'administrator_retire' THEN
        RAISE EXCEPTION 'invalid node retire request' USING ERRCODE = '22023';
    END IF;

    UPDATE public.relay_node_assets
    SET management_credential_sealed = NULL,
        lifecycle_status = 'retired',
        revision = revision + 1,
        retired_at = retire_boundary,
        retired_by = retired_by_actor,
        retire_reason = retire_reason_value,
        updated_at = updated_boundary
    WHERE instance_id = target_instance_id
      AND lifecycle_status = 'active'
      AND revision = expected_revision
      AND revision < 9223372036854775807;

    IF NOT FOUND THEN
        SELECT lifecycle_status, revision
          INTO current_lifecycle, current_revision
          FROM public.relay_node_assets
         WHERE instance_id = target_instance_id
         FOR UPDATE;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'node lifecycle target not found' USING ERRCODE = 'P0002';
        ELSIF current_lifecycle <> 'active' THEN
            RAISE EXCEPTION 'node lifecycle target retired' USING ERRCODE = 'P0003';
        ELSIF current_revision = 9223372036854775807 THEN
            RAISE EXCEPTION 'node asset revision exhausted' USING ERRCODE = 'P0004';
        ELSE
            RAISE EXCEPTION 'node asset revision stale' USING ERRCODE = 'P0005';
        END IF;
    END IF;
END;
$$;
-- +goose StatementEnd

-- Stage 0 Node Replace retires the predecessor and creates the replacement
-- with its independently supplied sealed state in one fixed function call.
-- Lineage and other lifecycle-domain closures remain repository-owned.
-- +goose StatementBegin
CREATE FUNCTION public.control_replace_relay_node_asset_stage0_v1(
    predecessor_instance_id uuid,
    expected_revision bigint,
    replace_boundary timestamptz,
    retired_by_actor uuid,
    replacement_instance_id uuid,
    replacement_display_name text,
    replacement_node_type text,
    replacement_driver_contract_version text,
    replacement_management_endpoint text,
    replacement_sealed_value bytea,
    replacement_capabilities text[]
) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    current_lifecycle text;
    current_revision bigint;
BEGIN
    IF predecessor_instance_id IS NULL
       OR expected_revision < 1
       OR replace_boundary IS NULL
       OR retired_by_actor IS NULL
       OR replacement_instance_id IS NULL
       OR replacement_instance_id = predecessor_instance_id
       OR replacement_capabilities IS NULL
       OR cardinality(replacement_capabilities) < 1
       OR cardinality(replacement_capabilities) <> (
           SELECT count(DISTINCT capability)
           FROM unnest(replacement_capabilities) capability
       )
       OR EXISTS (
           SELECT 1
           FROM unnest(replacement_capabilities) requested(capability)
           LEFT JOIN public.driver_capabilities supported
             ON supported.node_type = replacement_node_type
            AND supported.driver_contract_version = replacement_driver_contract_version
            AND supported.capability = requested.capability
           WHERE supported.capability IS NULL
       ) THEN
        RAISE EXCEPTION 'invalid relay node replacement declaration'
            USING ERRCODE = '23514';
    END IF;
    IF replacement_sealed_value IS NOT NULL
       AND octet_length(replacement_sealed_value) < 29 THEN
        RAISE EXCEPTION 'invalid sealed replacement node credential'
            USING ERRCODE = '22023';
    END IF;
    IF EXISTS (
        SELECT 1
        FROM public.relay_node_assets
        WHERE instance_id = replacement_instance_id
    ) THEN
        RAISE EXCEPTION 'replacement relay node identity already exists'
            USING ERRCODE = '23505';
    END IF;

    UPDATE public.relay_node_assets
    SET management_credential_sealed = NULL,
        lifecycle_status = 'retired',
        revision = revision + 1,
        retired_at = replace_boundary,
        retired_by = retired_by_actor,
        retire_reason = 'replacement',
        updated_at = replace_boundary
    WHERE instance_id = predecessor_instance_id
      AND lifecycle_status = 'active'
      AND revision = expected_revision
      AND revision < 9223372036854775807;

    IF NOT FOUND THEN
        SELECT lifecycle_status, revision
          INTO current_lifecycle, current_revision
          FROM public.relay_node_assets
         WHERE instance_id = predecessor_instance_id
         FOR UPDATE;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'node lifecycle target not found' USING ERRCODE = 'P0002';
        ELSIF current_lifecycle <> 'active' THEN
            RAISE EXCEPTION 'node lifecycle target retired' USING ERRCODE = 'P0003';
        ELSIF current_revision = 9223372036854775807 THEN
            RAISE EXCEPTION 'node asset revision exhausted' USING ERRCODE = 'P0004';
        ELSE
            RAISE EXCEPTION 'node asset revision stale' USING ERRCODE = 'P0005';
        END IF;
    END IF;

    INSERT INTO public.relay_node_assets(
        instance_id, display_name, node_type, driver_contract_version,
        management_endpoint, reader_secret_ref, management_credential_sealed,
        lifecycle_status, revision
    ) VALUES (
        replacement_instance_id, replacement_display_name,
        replacement_node_type, replacement_driver_contract_version,
        replacement_management_endpoint, NULL, replacement_sealed_value,
        'active', 1
    );
    INSERT INTO public.node_capabilities(instance_id, node_type, driver_contract_version, capability)
    SELECT replacement_instance_id, replacement_node_type,
           replacement_driver_contract_version, requested.capability
    FROM unnest(replacement_capabilities) requested(capability)
    ORDER BY requested.capability;

    RETURN replacement_instance_id;
END;
$$;
-- +goose StatementEnd

-- Stage 0 Gateway Register owns the initial sealed state in the same INSERT as
-- the current singleton row. It never accepts or persists a legacy reference.
-- +goose StatementBegin
CREATE FUNCTION public.control_register_gateway_asset_stage0_v1(
    requested_instance_id uuid,
    requested_display_name text,
    requested_management_endpoint text,
    requested_directory_credential_sealed bytea
) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    created_instance_id uuid;
BEGIN
    IF requested_instance_id IS NULL THEN
        RAISE EXCEPTION 'invalid gateway identity' USING ERRCODE = '22023';
    END IF;
    IF requested_directory_credential_sealed IS NOT NULL
       AND octet_length(requested_directory_credential_sealed) < 29 THEN
        RAISE EXCEPTION 'invalid sealed gateway credential' USING ERRCODE = '22023';
    END IF;

    INSERT INTO public.gateway_instances(
        singleton_id, instance_id, display_name, management_endpoint,
        reader_secret_ref, directory_credential_sealed,
        lifecycle_status, revision
    ) VALUES (
        1, requested_instance_id, requested_display_name,
        requested_management_endpoint, NULL,
        requested_directory_credential_sealed, 'active', 1
    ) RETURNING instance_id INTO created_instance_id;

    RETURN created_instance_id;
END;
$$;
-- +goose StatementEnd

-- Stage 0 Gateway Edit owns the resolved current-row fields, sealed state,
-- and revision advance in one fixed UPDATE. PATCH resolution remains outside
-- the database function.
-- +goose StatementBegin
CREATE FUNCTION public.control_edit_gateway_asset_stage0_v1(
    target_instance_id uuid,
    expected_revision bigint,
    resolved_display_name text,
    resolved_management_endpoint text,
    credential_action text,
    sealed_value bytea
) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    current_lifecycle text;
    current_revision bigint;
    current_singleton_id bigint;
BEGIN
    IF target_instance_id IS NULL OR expected_revision < 1 THEN
        RAISE EXCEPTION 'invalid gateway edit identity or revision' USING ERRCODE = '22023';
    END IF;
    IF credential_action IS NULL
       OR credential_action NOT IN ('keep', 'set', 'clear') THEN
        RAISE EXCEPTION 'invalid gateway credential action' USING ERRCODE = '22023';
    END IF;
    IF credential_action = 'set'
       AND (sealed_value IS NULL OR octet_length(sealed_value) < 29) THEN
        RAISE EXCEPTION 'invalid sealed gateway credential' USING ERRCODE = '22023';
    END IF;
    IF credential_action IN ('keep', 'clear') AND sealed_value IS NOT NULL THEN
        RAISE EXCEPTION 'sealed gateway credential is not allowed for this action' USING ERRCODE = '22023';
    END IF;

    UPDATE public.gateway_instances
    SET display_name = resolved_display_name,
        management_endpoint = resolved_management_endpoint,
        directory_credential_sealed = CASE credential_action
            WHEN 'keep' THEN directory_credential_sealed
            WHEN 'set' THEN sealed_value
            WHEN 'clear' THEN NULL
        END,
        revision = revision + 1,
        updated_at = clock_timestamp()
    WHERE instance_id = target_instance_id
      AND singleton_id = 1
      AND lifecycle_status = 'active'
      AND revision = expected_revision
      AND revision < 9223372036854775807;

    IF NOT FOUND THEN
        SELECT lifecycle_status, revision, singleton_id
          INTO current_lifecycle, current_revision, current_singleton_id
          FROM public.gateway_instances
         WHERE instance_id = target_instance_id
         FOR UPDATE;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'gateway lifecycle target not found' USING ERRCODE = 'P0002';
        ELSIF current_lifecycle <> 'active' OR current_singleton_id IS DISTINCT FROM 1 THEN
            RAISE EXCEPTION 'gateway lifecycle target retired' USING ERRCODE = 'P0003';
        ELSIF current_revision = 9223372036854775807 THEN
            RAISE EXCEPTION 'gateway asset revision exhausted' USING ERRCODE = 'P0004';
        ELSE
            RAISE EXCEPTION 'gateway asset revision stale' USING ERRCODE = 'P0005';
        END IF;
    END IF;
END;
$$;
-- +goose StatementEnd

-- Stage 0 Gateway Retire releases the current slot, erases the protected
-- credential, and records retirement in one fixed owning-row UPDATE.
-- +goose StatementBegin
CREATE FUNCTION public.control_retire_gateway_asset_stage0_v1(
    target_instance_id uuid,
    expected_revision bigint,
    retire_boundary timestamptz,
    retired_by_actor uuid,
    retire_reason_value text
) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    current_lifecycle text;
    current_revision bigint;
    current_singleton_id bigint;
BEGIN
    IF target_instance_id IS NULL
       OR expected_revision < 1
       OR retire_boundary IS NULL
       OR retired_by_actor IS NULL
       OR retire_reason_value IS NULL
       OR retire_reason_value <> 'administrator_retire' THEN
        RAISE EXCEPTION 'invalid gateway retire request' USING ERRCODE = '22023';
    END IF;

    UPDATE public.gateway_instances
    SET singleton_id = NULL,
        directory_credential_sealed = NULL,
        lifecycle_status = 'retired',
        revision = revision + 1,
        retired_at = retire_boundary,
        retired_by = retired_by_actor,
        retire_reason = retire_reason_value,
        updated_at = retire_boundary
    WHERE instance_id = target_instance_id
      AND singleton_id = 1
      AND lifecycle_status = 'active'
      AND revision = expected_revision
      AND revision < 9223372036854775807;

    IF NOT FOUND THEN
        SELECT lifecycle_status, revision, singleton_id
          INTO current_lifecycle, current_revision, current_singleton_id
          FROM public.gateway_instances
         WHERE instance_id = target_instance_id
         FOR UPDATE;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'gateway lifecycle target not found' USING ERRCODE = 'P0002';
        ELSIF current_lifecycle <> 'active' OR current_singleton_id IS DISTINCT FROM 1 THEN
            RAISE EXCEPTION 'gateway lifecycle target retired' USING ERRCODE = 'P0003';
        ELSIF current_revision = 9223372036854775807 THEN
            RAISE EXCEPTION 'gateway asset revision exhausted' USING ERRCODE = 'P0004';
        ELSE
            RAISE EXCEPTION 'gateway asset revision stale' USING ERRCODE = 'P0005';
        END IF;
    END IF;
END;
$$;
-- +goose StatementEnd

-- Stage 0 Gateway Replace performs the current-slot handoff, predecessor
-- credential erase, and replacement insert in one fixed function call.
-- Directory binding closure and lineage remain repository-owned.
-- +goose StatementBegin
CREATE FUNCTION public.control_replace_gateway_asset_stage0_v1(
    predecessor_instance_id uuid,
    expected_revision bigint,
    replace_boundary timestamptz,
    retired_by_actor uuid,
    replacement_instance_id uuid,
    replacement_display_name text,
    replacement_management_endpoint text,
    replacement_sealed_value bytea
) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    current_lifecycle text;
    current_revision bigint;
    current_singleton_id bigint;
BEGIN
    IF predecessor_instance_id IS NULL
       OR expected_revision < 1
       OR replace_boundary IS NULL
       OR retired_by_actor IS NULL
       OR replacement_instance_id IS NULL
       OR replacement_instance_id = predecessor_instance_id THEN
        RAISE EXCEPTION 'invalid gateway replacement identity' USING ERRCODE = '22023';
    END IF;
    IF replacement_sealed_value IS NOT NULL
       AND octet_length(replacement_sealed_value) < 29 THEN
        RAISE EXCEPTION 'invalid sealed replacement gateway credential'
            USING ERRCODE = '22023';
    END IF;
    IF EXISTS (
        SELECT 1
        FROM public.gateway_instances
        WHERE instance_id = replacement_instance_id
    ) THEN
        RAISE EXCEPTION 'replacement gateway identity already exists'
            USING ERRCODE = '23505';
    END IF;

    UPDATE public.gateway_instances
    SET singleton_id = NULL,
        directory_credential_sealed = NULL,
        lifecycle_status = 'retired',
        revision = revision + 1,
        retired_at = replace_boundary,
        retired_by = retired_by_actor,
        retire_reason = 'replacement',
        updated_at = replace_boundary
    WHERE instance_id = predecessor_instance_id
      AND singleton_id = 1
      AND lifecycle_status = 'active'
      AND revision = expected_revision
      AND revision < 9223372036854775807;

    IF NOT FOUND THEN
        SELECT lifecycle_status, revision, singleton_id
          INTO current_lifecycle, current_revision, current_singleton_id
          FROM public.gateway_instances
         WHERE instance_id = predecessor_instance_id
         FOR UPDATE;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'gateway lifecycle target not found' USING ERRCODE = 'P0002';
        ELSIF current_lifecycle <> 'active' OR current_singleton_id IS DISTINCT FROM 1 THEN
            RAISE EXCEPTION 'gateway lifecycle target retired' USING ERRCODE = 'P0003';
        ELSIF current_revision = 9223372036854775807 THEN
            RAISE EXCEPTION 'gateway asset revision exhausted' USING ERRCODE = 'P0004';
        ELSE
            RAISE EXCEPTION 'gateway asset revision stale' USING ERRCODE = 'P0005';
        END IF;
    END IF;

    INSERT INTO public.gateway_instances(
        singleton_id, instance_id, display_name, management_endpoint,
        reader_secret_ref, directory_credential_sealed,
        lifecycle_status, revision
    ) VALUES (
        1, replacement_instance_id, replacement_display_name,
        replacement_management_endpoint, NULL,
        replacement_sealed_value, 'active', 1
    );

    RETURN replacement_instance_id;
END;
$$;
-- +goose StatementEnd

-- Recreate the existing safe projection privilege after replacing the
-- generated column.  The protected blobs themselves remain unreachable by
-- direct runtime-role column access.
GRANT SELECT (reader_secret_configured)
    ON public.relay_node_assets, public.gateway_instances
    TO relay_control_runtime;

CREATE TABLE public.control_asset_credential_key_identity (
    singleton_id smallint PRIMARY KEY DEFAULT 1,
    k2_identity_commitment bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT control_asset_credential_key_identity_singleton_check
        CHECK (singleton_id = 1),
    CONSTRAINT control_asset_credential_key_identity_commitment_shape
        CHECK (octet_length(k2_identity_commitment) = 32)
);

-- +goose StatementBegin
CREATE FUNCTION public.control_protect_asset_credential_key_identity()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
    RAISE EXCEPTION 'K2 identity commitment is write-once'
        USING ERRCODE = '42501';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER control_asset_credential_key_identity_guard
BEFORE UPDATE OR DELETE ON public.control_asset_credential_key_identity
FOR EACH ROW EXECUTE FUNCTION public.control_protect_asset_credential_key_identity();

CREATE TRIGGER control_asset_credential_key_identity_truncate_guard
BEFORE TRUNCATE ON public.control_asset_credential_key_identity
FOR EACH STATEMENT EXECUTE FUNCTION public.control_protect_asset_credential_key_identity();

-- +goose StatementBegin
CREATE FUNCTION public.control_initialize_asset_credential_key_v1(
    expected_commitment bytea
) RETURNS text
LANGUAGE plpgsql SECURITY DEFINER VOLATILE SET search_path = pg_catalog
AS $$
DECLARE
    stored_commitment bytea;
    sealed_count bigint;
BEGIN
    IF expected_commitment IS NULL OR octet_length(expected_commitment) <> 32 THEN
        RAISE EXCEPTION 'K2 identity commitment has invalid shape' USING ERRCODE = '22023';
    END IF;

    LOCK TABLE public.environments IN SHARE ROW EXCLUSIVE MODE;

    SELECT k2_identity_commitment INTO stored_commitment
    FROM public.control_asset_credential_key_identity
    WHERE singleton_id = 1
    FOR UPDATE;
    IF FOUND THEN
        IF stored_commitment = expected_commitment THEN
            RETURN 'available';
        END IF;
        RETURN 'unavailable';
    END IF;

    SELECT
        (SELECT count(*) FROM public.relay_node_assets
         WHERE management_credential_sealed IS NOT NULL)
        +
        (SELECT count(*) FROM public.gateway_instances
         WHERE directory_credential_sealed IS NOT NULL)
    INTO sealed_count;
    IF sealed_count <> 0 THEN
        RETURN 'invalid_database_state';
    END IF;

    INSERT INTO public.control_asset_credential_key_identity
        (singleton_id, k2_identity_commitment)
    VALUES (1, expected_commitment);
    RETURN 'initialized';
END;
$$;
-- +goose StatementEnd

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
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_read_node_management_credential_sealed_v1(
    target_instance_id uuid
) RETURNS bytea
LANGUAGE sql SECURITY DEFINER STABLE SET search_path = pg_catalog
AS $$
    SELECT management_credential_sealed
    FROM public.relay_node_assets
    WHERE instance_id = target_instance_id
      AND lifecycle_status = 'active'
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_read_gateway_directory_credential_sealed_v1(
    target_instance_id uuid
) RETURNS bytea
LANGUAGE sql SECURITY DEFINER STABLE SET search_path = pg_catalog
AS $$
    SELECT directory_credential_sealed
    FROM public.gateway_instances
    WHERE instance_id = target_instance_id
      AND singleton_id = 1
      AND lifecycle_status = 'active'
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_read_gateway_directory_credential_fenced_v1(
    target_run_id uuid,
    target_gateway_id uuid,
    target_fencing_token uuid
) RETURNS bytea
LANGUAGE sql SECURITY DEFINER STABLE SET search_path = pg_catalog
AS $$
    SELECT gateway.directory_credential_sealed
    FROM public.gateway_directory_ingestion_runs AS run
    JOIN public.gateway_instances AS gateway
      ON gateway.instance_id = run.gateway_instance_id
    WHERE run.ingestion_run_id = target_run_id
      AND run.gateway_instance_id = target_gateway_id
      AND run.lease_fencing_token = target_fencing_token
      AND run.status = 'running'
      AND run.lease_expires_at > statement_timestamp()
      AND gateway.singleton_id = 1
      AND gateway.lifecycle_status = 'active'
      AND gateway.directory_credential_sealed IS NOT NULL
$$;
-- +goose StatementEnd

ALTER TABLE public.control_asset_credential_key_identity OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_protect_asset_credential_key_identity() OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_initialize_asset_credential_key_v1(bytea) OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_read_node_management_credential_sealed_v1(uuid) OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_read_gateway_directory_credential_sealed_v1(uuid) OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_read_gateway_directory_credential_fenced_v1(uuid,uuid,uuid) OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_register_relay_node_asset_stage0_v1(uuid,text,text,text,text,bytea,text[]) OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_edit_relay_node_asset_stage0_v1(uuid,bigint,text,text,text,bytea) OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_retire_relay_node_asset_stage0_v1(uuid,bigint,timestamptz,uuid,text,timestamptz) OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_replace_relay_node_asset_stage0_v1(uuid,bigint,timestamptz,uuid,uuid,text,text,text,text,bytea,text[]) OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_register_gateway_asset_stage0_v1(uuid,text,text,bytea) OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_edit_gateway_asset_stage0_v1(uuid,bigint,text,text,text,bytea) OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_retire_gateway_asset_stage0_v1(uuid,bigint,timestamptz,uuid,text) OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_replace_gateway_asset_stage0_v1(uuid,bigint,timestamptz,uuid,uuid,text,text,bytea) OWNER TO relay_control_migrator;

REVOKE ALL ON TABLE public.control_asset_credential_key_identity
    FROM PUBLIC, relay_control_runtime, relay_control_asset_registrar;
REVOKE ALL ON FUNCTION public.control_initialize_asset_credential_key_v1(bytea)
    FROM PUBLIC, relay_control_runtime, relay_control_asset_registrar;
GRANT EXECUTE ON FUNCTION public.control_initialize_asset_credential_key_v1(bytea)
    TO relay_control_runtime;
REVOKE ALL ON FUNCTION public.control_read_node_management_credential_sealed_v1(uuid)
    FROM PUBLIC, relay_control_asset_registrar;
REVOKE ALL ON FUNCTION public.control_read_gateway_directory_credential_sealed_v1(uuid)
    FROM PUBLIC, relay_control_asset_registrar;
GRANT EXECUTE ON FUNCTION public.control_read_node_management_credential_sealed_v1(uuid)
    TO relay_control_runtime;
GRANT EXECUTE ON FUNCTION public.control_read_gateway_directory_credential_sealed_v1(uuid)
    TO relay_control_runtime;
REVOKE ALL ON FUNCTION public.control_read_gateway_directory_credential_fenced_v1(uuid,uuid,uuid)
    FROM PUBLIC, relay_control_asset_registrar;
GRANT EXECUTE ON FUNCTION public.control_read_gateway_directory_credential_fenced_v1(uuid,uuid,uuid)
    TO relay_control_runtime;
REVOKE ALL ON FUNCTION public.control_register_relay_node_asset_stage0_v1(uuid,text,text,text,text,bytea,text[])
    FROM PUBLIC, relay_control_asset_registrar;
GRANT EXECUTE ON FUNCTION public.control_register_relay_node_asset_stage0_v1(uuid,text,text,text,text,bytea,text[])
    TO relay_control_runtime;
REVOKE ALL ON FUNCTION public.control_edit_relay_node_asset_stage0_v1(uuid,bigint,text,text,text,bytea)
    FROM PUBLIC, relay_control_asset_registrar;
GRANT EXECUTE ON FUNCTION public.control_edit_relay_node_asset_stage0_v1(uuid,bigint,text,text,text,bytea)
    TO relay_control_runtime;
REVOKE ALL ON FUNCTION public.control_retire_relay_node_asset_stage0_v1(uuid,bigint,timestamptz,uuid,text,timestamptz)
    FROM PUBLIC, relay_control_asset_registrar;
GRANT EXECUTE ON FUNCTION public.control_retire_relay_node_asset_stage0_v1(uuid,bigint,timestamptz,uuid,text,timestamptz)
    TO relay_control_runtime;
REVOKE ALL ON FUNCTION public.control_replace_relay_node_asset_stage0_v1(uuid,bigint,timestamptz,uuid,uuid,text,text,text,text,bytea,text[])
    FROM PUBLIC, relay_control_asset_registrar;
GRANT EXECUTE ON FUNCTION public.control_replace_relay_node_asset_stage0_v1(uuid,bigint,timestamptz,uuid,uuid,text,text,text,text,bytea,text[])
    TO relay_control_runtime;
REVOKE ALL ON FUNCTION public.control_register_gateway_asset_stage0_v1(uuid,text,text,bytea)
    FROM PUBLIC, relay_control_asset_registrar;
GRANT EXECUTE ON FUNCTION public.control_register_gateway_asset_stage0_v1(uuid,text,text,bytea)
    TO relay_control_runtime;
REVOKE ALL ON FUNCTION public.control_edit_gateway_asset_stage0_v1(uuid,bigint,text,text,text,bytea)
    FROM PUBLIC, relay_control_asset_registrar;
GRANT EXECUTE ON FUNCTION public.control_edit_gateway_asset_stage0_v1(uuid,bigint,text,text,text,bytea)
    TO relay_control_runtime;
REVOKE ALL ON FUNCTION public.control_retire_gateway_asset_stage0_v1(uuid,bigint,timestamptz,uuid,text)
    FROM PUBLIC, relay_control_asset_registrar;
GRANT EXECUTE ON FUNCTION public.control_retire_gateway_asset_stage0_v1(uuid,bigint,timestamptz,uuid,text)
    TO relay_control_runtime;
REVOKE ALL ON FUNCTION public.control_replace_gateway_asset_stage0_v1(uuid,bigint,timestamptz,uuid,uuid,text,text,bytea)
    FROM PUBLIC, relay_control_asset_registrar;
GRANT EXECUTE ON FUNCTION public.control_replace_gateway_asset_stage0_v1(uuid,bigint,timestamptz,uuid,uuid,text,text,bytea)
    TO relay_control_runtime;

ALTER TABLE public.control_runtime_compatibility
    DROP CONSTRAINT control_runtime_compatibility_floor_check,
    ADD CONSTRAINT control_runtime_compatibility_floor_check
        CHECK (phase6_evidence_floor IN (0, 1, 2, 3, 4));
UPDATE public.control_runtime_compatibility
SET phase6_evidence_floor = 4,
    updated_at = clock_timestamp()
WHERE singleton_id = 1 AND phase6_evidence_floor < 4;

-- Latest-schema read fences: the lifecycle wrapper intentionally returns an
-- empty page for an ineligible but registered Node. A missing Node is a
-- distinct caller error and must remain observable to current repositories.
-- Keep the historical query bodies immutable under private stage names while
-- adding that current-schema distinction at the latest migration boundary.
-- +goose StatementBegin
DO $$ BEGIN
    EXECUTE 'ALTER FUNCTION public.control_query_node_account_quality_v4(uuid,text,text,text,text,text,text,interval,integer) RENAME TO control_query_node_account_quality_stage1_v4';
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION public.control_query_node_account_quality_v4(
    target_instance_id uuid, target_provider text, target_lifecycle text,
    target_basic_status text, target_email text, target_quality text,
    after_account_key text, window_size interval, page_limit integer
) RETURNS TABLE (
    account_key text, provider text, normalized_email text, quality text,
    request_count bigint, success_count bigint, failure_count bigint,
    success_rate double precision, p95_latency_ms double precision,
    last_success_at timestamptz, last_failure_at timestamptz,
    last_failure_class text, inventory jsonb, recent_requests jsonb,
    token_state text, expected_valid_until timestamptz
)
LANGUAGE plpgsql SECURITY DEFINER STABLE SET search_path = pg_catalog AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM public.relay_node_assets
        WHERE instance_id = target_instance_id
    ) THEN
        RAISE EXCEPTION 'node not found' USING ERRCODE = 'P0404';
    END IF;
    RETURN QUERY SELECT * FROM public.control_query_node_account_quality_stage1_v4(
        target_instance_id, target_provider, target_lifecycle, target_basic_status,
        target_email, target_quality, after_account_key, window_size, page_limit
    );
END;
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_query_node_account_quality_v4(
    uuid,text,text,text,text,text,text,interval,integer
) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_query_node_account_quality_v4(
    uuid,text,text,text,text,text,text,interval,integer
) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_query_node_account_quality_v4(
    uuid,text,text,text,text,text,text,interval,integer
) TO relay_control_runtime;

-- +goose StatementBegin
DO $$ BEGIN
    EXECUTE 'ALTER FUNCTION public.control_query_node_account_quality_incidents_v1(uuid,text,text,timestamptz,text,text,integer) RENAME TO control_query_node_account_quality_incidents_stage1_v1';
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION public.control_query_node_account_quality_incidents_v1(
    target_instance_id uuid, target_provider text, target_failure_class text,
    after_last_seen timestamptz, after_account_key text, after_failure_class text,
    page_limit integer
) RETURNS TABLE (
    node_id uuid, account_key text, provider text, failure_class text,
    status text, first_seen timestamptz, last_seen timestamptz, hit_count bigint,
    last_success_at timestamptz
)
LANGUAGE plpgsql SECURITY DEFINER STABLE SET search_path = pg_catalog AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM public.relay_node_assets
        WHERE instance_id = target_instance_id
    ) THEN
        RAISE EXCEPTION 'node not found' USING ERRCODE = 'P0404';
    END IF;
    RETURN QUERY SELECT * FROM public.control_query_node_account_quality_incidents_stage1_v1(
        target_instance_id, target_provider, target_failure_class,
        after_last_seen, after_account_key, after_failure_class, page_limit
    );
END;
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_query_node_account_quality_incidents_v1(
    uuid,text,text,timestamptz,text,text,integer
) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_query_node_account_quality_incidents_v1(
    uuid,text,text,timestamptz,text,text,integer
) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_query_node_account_quality_incidents_v1(
    uuid,text,text,timestamptz,text,text,integer
) TO relay_control_runtime;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    RAISE EXCEPTION 'migration 51 is forward-only' USING ERRCODE = '55000';
END $$;
-- +goose StatementEnd
