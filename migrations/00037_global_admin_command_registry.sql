-- +goose Up

-- Stage 7A introduces the single durable namespace for every administrator
-- command.  The complete migration is transactional: backfill, enforcement
-- and the compatibility floor become visible together.
CREATE TABLE public.admin_command_registry (
    command_id uuid PRIMARY KEY,
    actor_admin_id uuid NOT NULL,
    command_domain text NOT NULL,
    command_kind text NOT NULL,
    intent_encoding_version smallint NOT NULL,
    canonical_intent_hash bytea NOT NULL,
    secret_fingerprint_key_version smallint,
    reserved_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT admin_command_registry_actor_fkey
        FOREIGN KEY (actor_admin_id)
        REFERENCES public.control_admin_users(admin_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    CONSTRAINT admin_command_registry_domain_check
        CHECK (command_domain IN ('asset_admin', 'account_admin')),
    CONSTRAINT admin_command_registry_kind_check CHECK (
        octet_length(command_kind) BETWEEN 1 AND 96
        AND command_kind ~ '^[a-z][a-z0-9]*(\.[a-z][a-z0-9_]*)+$'
    ),
    CONSTRAINT admin_command_registry_encoding_check
        CHECK (intent_encoding_version > 0),
    CONSTRAINT admin_command_registry_hash_check
        CHECK (octet_length(canonical_intent_hash) = 32),
    CONSTRAINT admin_command_registry_secret_version_check CHECK (
        secret_fingerprint_key_version IS NULL
        OR secret_fingerprint_key_version > 0
    ),
    CONSTRAINT admin_command_registry_reserved_at_finite_check
        CHECK (isfinite(reserved_at))
);

-- Fail before writing anything if historical data cannot be represented
-- exactly.  Existing receipt constraints are not accepted as a substitute
-- for this migration-time proof.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM public.asset_admin_command_receipts AS receipt
        LEFT JOIN public.control_admin_users AS actor
          ON actor.admin_id = receipt.actor_admin_id
        WHERE actor.admin_id IS NULL
           OR receipt.intent_encoding_version <> 1
           OR octet_length(receipt.canonical_intent_hash) <> 32
           OR (receipt.secret_fingerprint_key_version IS NOT NULL
               AND receipt.secret_fingerprint_key_version <> 1)
           OR NOT isfinite(receipt.committed_at)
           OR octet_length(receipt.command_kind) NOT BETWEEN 1 AND 96
           OR receipt.command_kind !~ '^[a-z][a-z0-9]*(\.[a-z][a-z0-9_]*)+$'
    ) THEN
        RAISE EXCEPTION 'historical admin command receipt cannot be represented exactly'
            USING ERRCODE = '23514';
    END IF;
END;
$$;
-- +goose StatementEnd

INSERT INTO public.admin_command_registry (
    command_id,
    actor_admin_id,
    command_domain,
    command_kind,
    intent_encoding_version,
    canonical_intent_hash,
    secret_fingerprint_key_version,
    reserved_at
)
SELECT receipt.command_id,
       receipt.actor_admin_id,
       'asset_admin',
       receipt.command_kind,
       receipt.intent_encoding_version,
       receipt.canonical_intent_hash,
       receipt.secret_fingerprint_key_version,
       receipt.committed_at
FROM public.asset_admin_command_receipts AS receipt;

-- +goose StatementBegin
DO $$
DECLARE
    receipt_count bigint;
    registry_count bigint;
BEGIN
    SELECT count(*) INTO receipt_count
    FROM public.asset_admin_command_receipts;
    SELECT count(*) INTO registry_count
    FROM public.admin_command_registry
    WHERE command_domain = 'asset_admin';
    IF registry_count <> receipt_count THEN
        RAISE EXCEPTION 'admin command registry backfill count mismatch'
            USING ERRCODE = '23514';
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_reject_admin_command_registry_mutation()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
    RAISE EXCEPTION 'admin command registry reservations are immutable'
        USING ERRCODE = '42501';
END;
$$;
-- +goose StatementEnd

ALTER FUNCTION public.control_reject_admin_command_registry_mutation()
    OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_reject_admin_command_registry_mutation()
    FROM PUBLIC, relay_control_runtime, relay_control_asset_registrar;

CREATE TRIGGER admin_command_registry_immutable
BEFORE UPDATE OR DELETE ON public.admin_command_registry
FOR EACH ROW EXECUTE FUNCTION public.control_reject_admin_command_registry_mutation();
CREATE TRIGGER admin_command_registry_truncate_guard
BEFORE TRUNCATE ON public.admin_command_registry
FOR EACH STATEMENT EXECUTE FUNCTION public.control_reject_admin_command_registry_mutation();

ALTER TABLE public.asset_admin_command_receipts
    ADD CONSTRAINT asset_admin_command_receipts_registry_fkey
    FOREIGN KEY (command_id)
    REFERENCES public.admin_command_registry(command_id)
    ON UPDATE RESTRICT ON DELETE RESTRICT;

-- +goose StatementBegin
CREATE FUNCTION public.control_validate_asset_admin_command_registry_v1()
RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    reservation public.admin_command_registry%ROWTYPE;
BEGIN
    SELECT * INTO reservation
    FROM public.admin_command_registry
    WHERE command_id = NEW.command_id;
    IF NOT FOUND
       OR reservation.command_domain <> 'asset_admin'
       OR reservation.actor_admin_id IS DISTINCT FROM NEW.actor_admin_id
       OR reservation.command_kind IS DISTINCT FROM NEW.command_kind
       OR reservation.intent_encoding_version IS DISTINCT FROM NEW.intent_encoding_version
       OR reservation.canonical_intent_hash IS DISTINCT FROM NEW.canonical_intent_hash
       OR reservation.secret_fingerprint_key_version IS DISTINCT FROM NEW.secret_fingerprint_key_version THEN
        RAISE EXCEPTION 'asset admin command receipt registry mismatch'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

ALTER FUNCTION public.control_validate_asset_admin_command_registry_v1()
    OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_validate_asset_admin_command_registry_v1()
    FROM PUBLIC, relay_control_runtime, relay_control_asset_registrar;

CREATE TRIGGER asset_admin_command_receipts_registry_guard
BEFORE INSERT ON public.asset_admin_command_receipts
FOR EACH ROW EXECUTE FUNCTION public.control_validate_asset_admin_command_registry_v1();

-- +goose StatementBegin
CREATE FUNCTION public.control_reserve_admin_command_v1(
    reservation_command_id uuid,
    reservation_actor_admin_id uuid,
    reservation_command_domain text,
    reservation_command_kind text,
    reservation_intent_encoding_version smallint,
    reservation_canonical_intent_hash bytea,
    reservation_secret_fingerprint_key_version smallint
)
RETURNS public.admin_command_registry
LANGUAGE plpgsql
SECURITY DEFINER
VOLATILE
SET search_path = pg_catalog
AS $$
DECLARE
    created public.admin_command_registry%ROWTYPE;
BEGIN
    INSERT INTO public.admin_command_registry (
        command_id, actor_admin_id, command_domain, command_kind,
        intent_encoding_version, canonical_intent_hash,
        secret_fingerprint_key_version
    ) VALUES (
        reservation_command_id, reservation_actor_admin_id,
        reservation_command_domain, reservation_command_kind,
        reservation_intent_encoding_version,
        reservation_canonical_intent_hash,
        reservation_secret_fingerprint_key_version
    ) RETURNING * INTO created;
    RETURN created;
END;
$$;
-- +goose StatementEnd

ALTER FUNCTION public.control_reserve_admin_command_v1(uuid,uuid,text,text,smallint,bytea,smallint)
    OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_reserve_admin_command_v1(uuid,uuid,text,text,smallint,bytea,smallint)
    FROM PUBLIC, relay_control_asset_registrar;
GRANT EXECUTE ON FUNCTION public.control_reserve_admin_command_v1(uuid,uuid,text,text,smallint,bytea,smallint)
    TO relay_control_runtime;

-- +goose StatementBegin
CREATE FUNCTION public.control_insert_asset_admin_command_receipt_v1(
    receipt_command_id uuid,
    receipt_command_kind text,
    receipt_intent_encoding_version smallint,
    receipt_canonical_intent_hash bytea,
    receipt_sanitized_result jsonb,
    receipt_response_status smallint,
    receipt_actor_admin_id uuid,
    receipt_committed_at timestamptz,
    receipt_secret_fingerprint_key_version smallint
)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
VOLATILE
SET search_path = pg_catalog
AS $$
BEGIN
    INSERT INTO public.asset_admin_command_receipts (
        command_id, command_kind, intent_encoding_version,
        canonical_intent_hash, sanitized_result, response_status,
        actor_admin_id, committed_at, secret_fingerprint_key_version
    ) VALUES (
        receipt_command_id, receipt_command_kind,
        receipt_intent_encoding_version, receipt_canonical_intent_hash,
        receipt_sanitized_result, receipt_response_status,
        receipt_actor_admin_id,
        COALESCE(receipt_committed_at, clock_timestamp()),
        receipt_secret_fingerprint_key_version
    );
END;
$$;
-- +goose StatementEnd

ALTER FUNCTION public.control_insert_asset_admin_command_receipt_v1(uuid,text,smallint,bytea,jsonb,smallint,uuid,timestamptz,smallint)
    OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_insert_asset_admin_command_receipt_v1(uuid,text,smallint,bytea,jsonb,smallint,uuid,timestamptz,smallint)
    FROM PUBLIC, relay_control_asset_registrar;
GRANT EXECUTE ON FUNCTION public.control_insert_asset_admin_command_receipt_v1(uuid,text,smallint,bytea,jsonb,smallint,uuid,timestamptz,smallint)
    TO relay_control_runtime;

REVOKE ALL ON TABLE public.admin_command_registry
    FROM PUBLIC, relay_control_runtime, relay_control_asset_registrar;
GRANT SELECT ON TABLE public.admin_command_registry TO relay_control_runtime;
REVOKE INSERT ON TABLE public.asset_admin_command_receipts FROM relay_control_runtime;

-- A pre-registry class-2 artifact must not start after this schema becomes
-- authoritative.  The gate update and this monotonic marker ship together.
ALTER TABLE public.control_runtime_compatibility
    DROP CONSTRAINT control_runtime_compatibility_floor_check,
    ADD CONSTRAINT control_runtime_compatibility_floor_check
        CHECK (phase6_evidence_floor IN (0, 1, 2, 3));
UPDATE public.control_runtime_compatibility
SET phase6_evidence_floor = 3,
    updated_at = clock_timestamp()
WHERE singleton_id = 1 AND phase6_evidence_floor < 3;

-- +goose Down
-- Forward-only: global command identity and compatibility evidence are
-- retained on rollback.
-- +goose StatementBegin
DO $$
BEGIN
    RAISE EXCEPTION 'migration 37 is forward-only' USING ERRCODE = '55000';
END;
$$;
-- +goose StatementEnd
