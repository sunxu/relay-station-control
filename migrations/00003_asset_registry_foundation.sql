-- +goose Up
CREATE EXTENSION IF NOT EXISTS btree_gist;

-- Existing deployments must already have a canonical environment identity.
ALTER TABLE environments
    ADD CONSTRAINT environments_environment_id_canonical CHECK (
        octet_length(environment_id) BETWEEN 1 AND 128
        AND environment_id ~ '^[A-Za-z0-9][A-Za-z0-9._-]*$'
    ) NOT VALID;
ALTER TABLE environments VALIDATE CONSTRAINT environments_environment_id_canonical;
ALTER TABLE environments
    ADD CONSTRAINT environments_name_canonical CHECK (
        char_length(name) BETWEEN 1 AND 100
        AND name ~ '^\S(?:.*\S)?$'
        AND name !~ '[[:cntrl:]]'
    ) NOT VALID;
ALTER TABLE environments VALIDATE CONSTRAINT environments_name_canonical;

-- +goose StatementBegin
CREATE FUNCTION public.control_protect_environment_identity() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
    IF TG_OP IN ('DELETE', 'TRUNCATE') THEN
        RAISE EXCEPTION 'environment identity cannot be deleted' USING ERRCODE = '23514';
    END IF;
    IF NEW.environment_id IS DISTINCT FROM OLD.environment_id
       OR NEW.environment_type IS DISTINCT FROM OLD.environment_type THEN
        RAISE EXCEPTION 'environment identity fields are immutable' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER environments_identity_guard
BEFORE UPDATE OR DELETE ON environments
FOR EACH ROW EXECUTE FUNCTION public.control_protect_environment_identity();
CREATE TRIGGER environments_identity_truncate_guard
BEFORE TRUNCATE ON environments
FOR EACH STATEMENT EXECUTE FUNCTION public.control_protect_environment_identity();

-- The function returns the one canonical representation accepted by table
-- constraints. It deliberately performs syntax validation only and has no DNS
-- or network side effects.
-- +goose StatementBegin
CREATE FUNCTION public.control_normalize_asset_endpoint(raw_endpoint text) RETURNS text
LANGUAGE plpgsql
IMMUTABLE STRICT
SET search_path = pg_catalog
AS $$
DECLARE
    scheme_end integer;
    scheme_value text;
    remainder text;
    slash_at integer;
    authority text;
    path_value text;
    host_value text;
    port_value text;
    close_bracket integer;
    colon_count integer;
    port_number integer;
    parsed_host inet;
    path_index integer;
    path_character text;
    encoded_octet text;
    normalized_path text := '';
    dot_segment_path text;
BEGIN
    IF raw_endpoint <> btrim(raw_endpoint)
       OR octet_length(raw_endpoint) NOT BETWEEN 8 AND 2048
       OR raw_endpoint ~ '[[:cntrl:][:space:]]'
       OR raw_endpoint ~ '[?#@]' THEN
        RAISE EXCEPTION 'asset endpoint is invalid' USING ERRCODE = '22023';
    END IF;
    scheme_end := strpos(raw_endpoint, '://');
    IF scheme_end = 0 THEN
        RAISE EXCEPTION 'asset endpoint is invalid' USING ERRCODE = '22023';
    END IF;
    scheme_value := lower(substr(raw_endpoint, 1, scheme_end - 1));
    IF scheme_value NOT IN ('http', 'https') THEN
        RAISE EXCEPTION 'asset endpoint is invalid' USING ERRCODE = '22023';
    END IF;
    remainder := substr(raw_endpoint, scheme_end + 3);
    slash_at := strpos(remainder, '/');
    IF slash_at = 0 THEN
        authority := remainder;
        path_value := '';
    ELSE
        authority := substr(remainder, 1, slash_at - 1);
        path_value := substr(remainder, slash_at);
    END IF;
    IF authority = '' OR path_value ~ '[?#]' THEN
        RAISE EXCEPTION 'asset endpoint is invalid' USING ERRCODE = '22023';
    END IF;

    path_index := 1;
    WHILE path_index <= char_length(path_value) LOOP
        path_character := substr(path_value, path_index, 1);
        IF path_character = E'\\' THEN
            RAISE EXCEPTION 'asset endpoint is invalid' USING ERRCODE = '22023';
        ELSIF path_character = '%' THEN
            encoded_octet := substr(path_value, path_index + 1, 2);
            IF char_length(encoded_octet) <> 2 OR encoded_octet !~ '^[0-9A-Fa-f]{2}$' THEN
                RAISE EXCEPTION 'asset endpoint is invalid' USING ERRCODE = '22023';
            END IF;
            encoded_octet := upper(encoded_octet);
            IF encoded_octet ~ '^(0[0-9A-F]|1[0-9A-F]|7F|2F|5C)$' THEN
                RAISE EXCEPTION 'asset endpoint is invalid' USING ERRCODE = '22023';
            END IF;
            normalized_path := normalized_path || '%' || encoded_octet;
            path_index := path_index + 3;
        ELSE
            normalized_path := normalized_path || path_character;
            path_index := path_index + 1;
        END IF;
    END LOOP;
    dot_segment_path := replace(normalized_path, '%2E', '.');
    IF dot_segment_path ~ '(^|/)\.{1,2}(/|$)' THEN
        RAISE EXCEPTION 'asset endpoint is invalid' USING ERRCODE = '22023';
    END IF;
    path_value := normalized_path;

    IF left(authority, 1) = '[' THEN
        close_bracket := strpos(authority, ']');
        IF close_bracket < 3
           OR substr(authority, 1, close_bracket) !~ '^\[[0-9A-Fa-f:.]+\]$' THEN
            RAISE EXCEPTION 'asset endpoint is invalid' USING ERRCODE = '22023';
        END IF;
        BEGIN
            parsed_host := substr(authority, 2, close_bracket - 2)::inet;
        EXCEPTION WHEN invalid_text_representation THEN
            RAISE EXCEPTION 'asset endpoint is invalid' USING ERRCODE = '22023';
        END;
        IF family(parsed_host) <> 6 THEN
            RAISE EXCEPTION 'asset endpoint is invalid' USING ERRCODE = '22023';
        END IF;
        host_value := '[' || host(parsed_host) || ']';
        IF length(authority) > close_bracket THEN
            IF substr(authority, close_bracket + 1, 1) <> ':' THEN
                RAISE EXCEPTION 'asset endpoint is invalid' USING ERRCODE = '22023';
            END IF;
            port_value := substr(authority, close_bracket + 2);
        END IF;
    ELSE
        colon_count := length(authority) - length(replace(authority, ':', ''));
        IF colon_count > 1 THEN
            RAISE EXCEPTION 'asset endpoint is invalid' USING ERRCODE = '22023';
        ELSIF colon_count = 1 THEN
            host_value := split_part(authority, ':', 1);
            port_value := split_part(authority, ':', 2);
        ELSE
            host_value := authority;
        END IF;
        IF host_value !~ '^([A-Za-z0-9]|[A-Za-z0-9][A-Za-z0-9.-]*[A-Za-z0-9])$'
           OR host_value LIKE '%.%.'
           OR host_value LIKE '%..%' THEN
            RAISE EXCEPTION 'asset endpoint is invalid' USING ERRCODE = '22023';
        END IF;
        host_value := lower(host_value);
    END IF;

    IF port_value IS NOT NULL THEN
        IF port_value !~ '^[0-9]{1,5}$' THEN
            RAISE EXCEPTION 'asset endpoint is invalid' USING ERRCODE = '22023';
        END IF;
        port_number := port_value::integer;
        IF port_number NOT BETWEEN 1 AND 65535 THEN
            RAISE EXCEPTION 'asset endpoint is invalid' USING ERRCODE = '22023';
        END IF;
        IF (scheme_value = 'http' AND port_number = 80)
           OR (scheme_value = 'https' AND port_number = 443) THEN
            port_value := NULL;
        END IF;
    END IF;

    RETURN scheme_value || '://' || host_value
        || CASE WHEN port_value IS NULL THEN '' ELSE ':' || port_value END
        || path_value;
END;
$$;
-- +goose StatementEnd

CREATE FUNCTION public.control_valid_secret_reference(secret_reference text) RETURNS boolean
LANGUAGE sql
IMMUTABLE STRICT
SET search_path = pg_catalog
RETURN octet_length(secret_reference) BETWEEN 6 AND 512
   AND secret_reference = btrim(secret_reference)
   AND secret_reference !~ '[[:cntrl:][:space:]?#@]'
   AND secret_reference !~ '^(http|https)://'
   AND secret_reference ~ '^[a-z][a-z0-9+.-]{1,31}://[A-Za-z0-9][A-Za-z0-9._~:/+-]*$';

-- +goose StatementBegin
CREATE FUNCTION public.control_normalize_provider_set(provider_values text[]) RETURNS text[]
LANGUAGE plpgsql
IMMUTABLE STRICT
SET search_path = pg_catalog
AS $$
DECLARE
    normalized text[];
BEGIN
    IF array_ndims(provider_values) IS DISTINCT FROM 1
       OR EXISTS (
           SELECT 1 FROM unnest(provider_values) AS provider(value)
           WHERE value IS NULL
              OR octet_length(value) NOT BETWEEN 1 AND 64
              OR value !~ '^[a-z0-9][a-z0-9._-]*$'
       ) THEN
        RAISE EXCEPTION 'provider set is invalid' USING ERRCODE = '22023';
    END IF;
    SELECT COALESCE(array_agg(DISTINCT value ORDER BY value), ARRAY[]::text[])
    INTO normalized
    FROM unnest(provider_values) AS provider(value);
    RETURN normalized;
END;
$$;
-- +goose StatementEnd

CREATE FUNCTION public.control_provider_policy_hash(
    policy_node_type text,
    policy_driver_contract_version text,
    policy_active_providers text[],
    policy_out_of_scope_providers text[]
) RETURNS bytea
LANGUAGE sql
IMMUTABLE STRICT
SET search_path = pg_catalog
RETURN sha256(convert_to(
    policy_node_type || chr(31)
    || policy_driver_contract_version || chr(31)
    || array_to_string(policy_active_providers, chr(30)) || chr(31)
    || array_to_string(policy_out_of_scope_providers, chr(30)),
    'UTF8'
));

CREATE TABLE gateway_instances (
    singleton_id smallint PRIMARY KEY DEFAULT 1 CHECK (singleton_id = 1),
    instance_id uuid NOT NULL UNIQUE,
    display_name text NOT NULL,
    management_endpoint text NOT NULL,
    reader_secret_ref text,
    reader_secret_configured boolean GENERATED ALWAYS AS (
        reader_secret_ref IS NOT NULL
    ) STORED,
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT gateway_instances_display_name_valid CHECK (
        char_length(display_name) BETWEEN 1 AND 100
        AND display_name ~ '^\S(?:.*\S)?$'
        AND display_name !~ '[[:cntrl:]]'
    ),
    CONSTRAINT gateway_instances_endpoint_canonical CHECK (
        management_endpoint = public.control_normalize_asset_endpoint(management_endpoint)
    ),
    CONSTRAINT gateway_instances_secret_ref_valid CHECK (
        reader_secret_ref IS NULL OR public.control_valid_secret_reference(reader_secret_ref)
    ),
    CONSTRAINT gateway_instances_timestamps_valid CHECK (updated_at >= created_at)
);

CREATE TABLE node_drivers (
    node_type text NOT NULL,
    driver_contract_version text NOT NULL,
    display_name text NOT NULL,
    lifecycle_status text NOT NULL DEFAULT 'active',
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (node_type, driver_contract_version),
    CONSTRAINT node_drivers_node_type_valid CHECK (
        octet_length(node_type) BETWEEN 2 AND 64
        AND node_type ~ '^[a-z0-9][a-z0-9._-]*$'
    ),
    CONSTRAINT node_drivers_contract_version_valid CHECK (
        octet_length(driver_contract_version) BETWEEN 1 AND 64
        AND driver_contract_version ~ '^[a-z0-9][a-z0-9._-]*$'
    ),
    CONSTRAINT node_drivers_display_name_valid CHECK (
        char_length(display_name) BETWEEN 1 AND 100
        AND display_name ~ '^\S(?:.*\S)?$'
        AND display_name !~ '[[:cntrl:]]'
    ),
    CONSTRAINT node_drivers_lifecycle_status_valid CHECK (
        lifecycle_status IN ('active', 'deprecated', 'retired')
    )
);

CREATE TABLE driver_capabilities (
    node_type text NOT NULL,
    driver_contract_version text NOT NULL,
    capability text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (node_type, driver_contract_version, capability),
    CONSTRAINT driver_capabilities_capability_fixed CHECK (
        capability IN ('management_health_read', 'management_account_inventory_read')
    ),
    FOREIGN KEY (node_type, driver_contract_version)
        REFERENCES node_drivers (node_type, driver_contract_version)
        ON UPDATE RESTRICT ON DELETE RESTRICT
);

CREATE TABLE relay_node_assets (
    instance_id uuid PRIMARY KEY,
    display_name text NOT NULL,
    node_type text NOT NULL,
    driver_contract_version text NOT NULL,
    management_endpoint text NOT NULL,
    reader_secret_ref text,
    reader_secret_configured boolean GENERATED ALWAYS AS (
        reader_secret_ref IS NOT NULL
    ) STORED,
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT relay_node_assets_display_name_valid CHECK (
        char_length(display_name) BETWEEN 1 AND 100
        AND display_name ~ '^\S(?:.*\S)?$'
        AND display_name !~ '[[:cntrl:]]'
    ),
    CONSTRAINT relay_node_assets_endpoint_canonical CHECK (
        management_endpoint = public.control_normalize_asset_endpoint(management_endpoint)
    ),
    CONSTRAINT relay_node_assets_secret_ref_valid CHECK (
        reader_secret_ref IS NULL OR public.control_valid_secret_reference(reader_secret_ref)
    ),
    CONSTRAINT relay_node_assets_timestamps_valid CHECK (updated_at >= created_at),
    FOREIGN KEY (node_type, driver_contract_version)
        REFERENCES node_drivers (node_type, driver_contract_version)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    UNIQUE (instance_id, node_type, driver_contract_version)
);

CREATE TABLE node_capabilities (
    instance_id uuid NOT NULL,
    node_type text NOT NULL,
    driver_contract_version text NOT NULL,
    capability text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (instance_id, capability),
    FOREIGN KEY (instance_id, node_type, driver_contract_version)
        REFERENCES relay_node_assets (instance_id, node_type, driver_contract_version)
        ON UPDATE RESTRICT ON DELETE CASCADE,
    FOREIGN KEY (node_type, driver_contract_version, capability)
        REFERENCES driver_capabilities (node_type, driver_contract_version, capability)
        ON UPDATE RESTRICT ON DELETE RESTRICT
);
CREATE INDEX node_capabilities_filter_idx
    ON node_capabilities (capability, instance_id);

CREATE TABLE provider_inventory_policy_versions (
    policy_version_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    node_type text NOT NULL,
    driver_contract_version text NOT NULL,
    active_providers text[] NOT NULL,
    out_of_scope_providers text[] NOT NULL,
    content_hash bytea GENERATED ALWAYS AS (
        public.control_provider_policy_hash(
            node_type,
            driver_contract_version,
            active_providers,
            out_of_scope_providers
        )
    ) STORED,
    created_by text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT provider_policy_active_canonical CHECK (
        active_providers = public.control_normalize_provider_set(active_providers)
    ),
    CONSTRAINT provider_policy_out_of_scope_canonical CHECK (
        out_of_scope_providers = public.control_normalize_provider_set(out_of_scope_providers)
    ),
    CONSTRAINT provider_policy_nonempty CHECK (
        cardinality(active_providers) + cardinality(out_of_scope_providers) > 0
    ),
    CONSTRAINT provider_policy_sets_disjoint CHECK (
        NOT (active_providers && out_of_scope_providers)
    ),
    CONSTRAINT provider_policy_created_by_valid CHECK (
        octet_length(created_by) BETWEEN 1 AND 128
        AND created_by ~ '^[A-Za-z0-9][A-Za-z0-9._@:-]*$'
    ),
    FOREIGN KEY (node_type, driver_contract_version)
        REFERENCES node_drivers (node_type, driver_contract_version)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    UNIQUE (node_type, driver_contract_version, content_hash),
    UNIQUE (policy_version_id, node_type, driver_contract_version)
);

-- +goose StatementBegin
CREATE FUNCTION public.control_reject_provider_policy_mutation() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
    RAISE EXCEPTION 'provider policy versions are immutable' USING ERRCODE = '42501';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER provider_inventory_policy_versions_immutable
BEFORE UPDATE OR DELETE ON provider_inventory_policy_versions
FOR EACH ROW EXECUTE FUNCTION public.control_reject_provider_policy_mutation();
CREATE TRIGGER provider_inventory_policy_versions_truncate_immutable
BEFORE TRUNCATE ON provider_inventory_policy_versions
FOR EACH STATEMENT EXECUTE FUNCTION public.control_reject_provider_policy_mutation();

CREATE TABLE provider_inventory_policy_bindings (
    node_type text NOT NULL,
    driver_contract_version text NOT NULL,
    policy_version_id uuid NOT NULL,
    bound_by text NOT NULL,
    bound_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (node_type, driver_contract_version),
    CONSTRAINT provider_policy_binding_actor_valid CHECK (
        octet_length(bound_by) BETWEEN 1 AND 128
        AND bound_by ~ '^[A-Za-z0-9][A-Za-z0-9._@:-]*$'
    ),
    FOREIGN KEY (policy_version_id, node_type, driver_contract_version)
        REFERENCES provider_inventory_policy_versions
            (policy_version_id, node_type, driver_contract_version)
        ON UPDATE RESTRICT ON DELETE RESTRICT
);

CREATE TABLE provider_inventory_policy_activations (
    activation_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    node_type text NOT NULL,
    driver_contract_version text NOT NULL,
    policy_version_id uuid NOT NULL,
    effective_from timestamptz NOT NULL,
    effective_to timestamptz,
    active_range tstzrange GENERATED ALWAYS AS (
        tstzrange(effective_from, effective_to, '[)')
    ) STORED,
    activated_by text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT provider_policy_activation_interval_valid CHECK (
        effective_to IS NULL OR effective_to > effective_from
    ),
    CONSTRAINT provider_policy_activation_no_backfill CHECK (
        effective_from >= created_at
    ),
    CONSTRAINT provider_policy_activation_actor_valid CHECK (
        octet_length(activated_by) BETWEEN 1 AND 128
        AND activated_by ~ '^[A-Za-z0-9][A-Za-z0-9._@:-]*$'
    ),
    FOREIGN KEY (policy_version_id, node_type, driver_contract_version)
        REFERENCES provider_inventory_policy_versions
            (policy_version_id, node_type, driver_contract_version)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    EXCLUDE USING gist (
        node_type WITH =,
        driver_contract_version WITH =,
        active_range WITH &&
    )
);
CREATE INDEX provider_policy_activations_current_idx
    ON provider_inventory_policy_activations
        (node_type, driver_contract_version, effective_from DESC);

CREATE TABLE relay_node_inventory_monitoring_activations (
    monitoring_activation_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    instance_id uuid NOT NULL REFERENCES relay_node_assets(instance_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    effective_from timestamptz NOT NULL,
    effective_to timestamptz,
    active_range tstzrange GENERATED ALWAYS AS (
        tstzrange(effective_from, effective_to, '[)')
    ) STORED,
    reason text NOT NULL,
    actor text NOT NULL,
    end_reason text,
    end_actor text,
    end_recorded_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT node_monitoring_activation_interval_valid CHECK (
        effective_to IS NULL OR effective_to > effective_from
    ),
    CONSTRAINT node_monitoring_activation_no_backfill CHECK (
        effective_from >= created_at
    ),
    CONSTRAINT node_monitoring_activation_reason_fixed CHECK (
        reason IN (
            'deployment_enable',
            'scheduled_enable',
            'reconciliation'
        )
    ),
    CONSTRAINT node_monitoring_activation_actor_valid CHECK (
        octet_length(actor) BETWEEN 1 AND 128
        AND actor ~ '^[A-Za-z0-9][A-Za-z0-9._@:-]*$'
    ),
    CONSTRAINT node_monitoring_activation_end_metadata_valid CHECK (
        (
            effective_to IS NULL
            AND end_reason IS NULL
            AND end_actor IS NULL
            AND end_recorded_at IS NULL
        ) OR (
            effective_to IS NOT NULL
            AND end_reason IS NOT NULL
            AND end_reason IN ('deployment_disable', 'scheduled_disable', 'reconciliation')
            AND end_actor IS NOT NULL
            AND octet_length(end_actor) BETWEEN 1 AND 128
            AND end_actor ~ '^[A-Za-z0-9][A-Za-z0-9._@:-]*$'
            AND end_recorded_at IS NOT NULL
            AND end_recorded_at >= created_at
        )
    ),
    EXCLUDE USING gist (instance_id WITH =, active_range WITH &&)
);
CREATE INDEX relay_node_monitoring_current_idx
    ON relay_node_inventory_monitoring_activations
        (instance_id, effective_from DESC);

-- Registration helpers are the registrar role's only write surface. Their
-- SECURITY DEFINER owner is the migration role and every object reference is
-- schema-qualified under a fixed safe search_path.
-- +goose StatementBegin
CREATE FUNCTION public.control_register_gateway(
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
    INSERT INTO public.gateway_instances (
        singleton_id, instance_id, display_name, management_endpoint, reader_secret_ref
    ) VALUES (
        1, gateway_instance_id, gateway_display_name, normalized_endpoint, gateway_reader_secret_ref
    ) ON CONFLICT (singleton_id) DO NOTHING;
    SELECT * INTO existing FROM public.gateway_instances WHERE singleton_id = 1;
    IF existing.instance_id IS DISTINCT FROM gateway_instance_id
       OR existing.display_name IS DISTINCT FROM gateway_display_name
       OR existing.management_endpoint IS DISTINCT FROM normalized_endpoint
       OR existing.reader_secret_ref IS DISTINCT FROM gateway_reader_secret_ref THEN
        RAISE EXCEPTION 'gateway registration conflicts with existing identity'
            USING ERRCODE = '23505';
    END IF;
    RETURN existing.instance_id;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_register_node_driver(
    driver_node_type text,
    driver_version text,
    driver_display_name text,
    driver_lifecycle_status text,
    supported_capabilities text[]
) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    normalized_capabilities text[];
    existing_capabilities text[];
    existing_driver public.node_drivers%ROWTYPE;
BEGIN
    IF driver_node_type IS NULL
       OR octet_length(driver_node_type) NOT BETWEEN 2 AND 64
       OR driver_node_type !~ '^[a-z0-9][a-z0-9._-]*$'
       OR driver_version IS NULL
       OR octet_length(driver_version) NOT BETWEEN 1 AND 64
       OR driver_version !~ '^[a-z0-9][a-z0-9._-]*$'
       OR driver_display_name IS NULL
       OR char_length(driver_display_name) NOT BETWEEN 1 AND 100
       OR driver_display_name !~ '^\S(?:.*\S)?$'
       OR driver_display_name ~ '[[:cntrl:]]'
       OR driver_lifecycle_status IS NULL
       OR driver_lifecycle_status NOT IN ('active', 'deprecated', 'retired')
       OR supported_capabilities IS NULL THEN
        RAISE EXCEPTION 'driver registration rejected' USING ERRCODE = '22023';
    END IF;
    SELECT COALESCE(array_agg(DISTINCT capability ORDER BY capability), ARRAY[]::text[])
    INTO normalized_capabilities
    FROM unnest(supported_capabilities) AS input(capability);
    IF normalized_capabilities IS DISTINCT FROM supported_capabilities
       OR cardinality(normalized_capabilities) = 0
       OR EXISTS (
           SELECT 1 FROM unnest(normalized_capabilities) AS input(capability)
           WHERE capability NOT IN (
               'management_health_read',
               'management_account_inventory_read'
           )
       ) THEN
        RAISE EXCEPTION 'driver registration rejected' USING ERRCODE = '22023';
    END IF;
    INSERT INTO public.node_drivers (
        node_type, driver_contract_version, display_name, lifecycle_status
    ) VALUES (
        driver_node_type, driver_version, driver_display_name, driver_lifecycle_status
    ) ON CONFLICT (node_type, driver_contract_version) DO NOTHING;
    SELECT * INTO existing_driver
    FROM public.node_drivers
    WHERE node_type = driver_node_type
      AND driver_contract_version = driver_version;
    IF existing_driver.display_name IS DISTINCT FROM driver_display_name
       OR existing_driver.lifecycle_status IS DISTINCT FROM driver_lifecycle_status THEN
        RAISE EXCEPTION 'driver registration conflicts with existing definition'
            USING ERRCODE = '23505';
    END IF;
    INSERT INTO public.driver_capabilities (
        node_type, driver_contract_version, capability
    )
    SELECT driver_node_type, driver_version, capability
    FROM unnest(normalized_capabilities) AS input(capability)
    ON CONFLICT DO NOTHING;
    SELECT COALESCE(array_agg(capability ORDER BY capability), ARRAY[]::text[])
    INTO existing_capabilities
    FROM public.driver_capabilities
    WHERE node_type = driver_node_type
      AND driver_contract_version = driver_version;
    IF existing_capabilities IS DISTINCT FROM normalized_capabilities THEN
        RAISE EXCEPTION 'driver registration conflicts with existing capabilities'
            USING ERRCODE = '23505';
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_register_relay_node(
    node_instance_id uuid,
    node_display_name text,
    registered_node_type text,
    registered_driver_version text,
    node_management_endpoint text,
    node_reader_secret_ref text,
    declared_capabilities text[]
) RETURNS uuid
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    normalized_endpoint text;
    normalized_capabilities text[];
    existing_capabilities text[];
    existing_node public.relay_node_assets%ROWTYPE;
BEGIN
    IF node_instance_id IS NULL
       OR node_display_name IS NULL
       OR char_length(node_display_name) NOT BETWEEN 1 AND 100
       OR node_display_name !~ '^\S(?:.*\S)?$'
       OR node_display_name ~ '[[:cntrl:]]'
       OR registered_node_type IS NULL
       OR octet_length(registered_node_type) NOT BETWEEN 2 AND 64
       OR registered_node_type !~ '^[a-z0-9][a-z0-9._-]*$'
       OR registered_driver_version IS NULL
       OR octet_length(registered_driver_version) NOT BETWEEN 1 AND 64
       OR registered_driver_version !~ '^[a-z0-9][a-z0-9._-]*$'
       OR node_management_endpoint IS NULL
       OR declared_capabilities IS NULL
       OR (node_reader_secret_ref IS NOT NULL
           AND NOT public.control_valid_secret_reference(node_reader_secret_ref)) THEN
        RAISE EXCEPTION 'relay node registration rejected' USING ERRCODE = '22023';
    END IF;
    normalized_endpoint := public.control_normalize_asset_endpoint(node_management_endpoint);
    SELECT COALESCE(array_agg(DISTINCT capability ORDER BY capability), ARRAY[]::text[])
    INTO normalized_capabilities
    FROM unnest(declared_capabilities) AS input(capability);
    IF normalized_capabilities IS DISTINCT FROM declared_capabilities
       OR cardinality(normalized_capabilities) = 0
       OR EXISTS (
           SELECT 1 FROM unnest(normalized_capabilities) AS input(capability)
           WHERE NOT EXISTS (
               SELECT 1 FROM public.driver_capabilities AS supported
               WHERE supported.node_type = registered_node_type
                 AND supported.driver_contract_version = registered_driver_version
                 AND supported.capability = input.capability
           )
       )
       THEN
        RAISE EXCEPTION 'relay node registration rejected' USING ERRCODE = '22023';
    END IF;
    INSERT INTO public.relay_node_assets (
        instance_id,
        display_name,
        node_type,
        driver_contract_version,
        management_endpoint,
        reader_secret_ref
    ) VALUES (
        node_instance_id,
        node_display_name,
        registered_node_type,
        registered_driver_version,
        normalized_endpoint,
        node_reader_secret_ref
    ) ON CONFLICT (instance_id) DO NOTHING;
    SELECT * INTO existing_node
    FROM public.relay_node_assets
    WHERE instance_id = node_instance_id;
    IF existing_node.display_name IS DISTINCT FROM node_display_name
       OR existing_node.node_type IS DISTINCT FROM registered_node_type
       OR existing_node.driver_contract_version IS DISTINCT FROM registered_driver_version
       OR existing_node.management_endpoint IS DISTINCT FROM normalized_endpoint
       OR existing_node.reader_secret_ref IS DISTINCT FROM node_reader_secret_ref THEN
        RAISE EXCEPTION 'relay node registration conflicts with existing identity'
            USING ERRCODE = '23505';
    END IF;
    INSERT INTO public.node_capabilities (
        instance_id, node_type, driver_contract_version, capability
    )
    SELECT node_instance_id, registered_node_type, registered_driver_version, capability
    FROM unnest(normalized_capabilities) AS input(capability)
    ON CONFLICT DO NOTHING;
    SELECT COALESCE(array_agg(capability ORDER BY capability), ARRAY[]::text[])
    INTO existing_capabilities
    FROM public.node_capabilities
    WHERE instance_id = node_instance_id;
    IF existing_capabilities IS DISTINCT FROM normalized_capabilities THEN
        RAISE EXCEPTION 'relay node registration conflicts with existing capabilities'
            USING ERRCODE = '23505';
    END IF;
    RETURN existing_node.instance_id;
END;
$$;
-- +goose StatementEnd

-- Binding is the per-scope serialization/latest-selection pointer. The policy
-- effective at any instant is determined exclusively by the non-overlapping
-- activation history and database time, so future activation needs no worker.
-- +goose StatementBegin
CREATE FUNCTION public.control_activate_provider_policy(
    policy_node_type text,
    policy_driver_version text,
    requested_active_providers text[],
    requested_out_of_scope_providers text[],
    policy_actor text,
    requested_effective_at timestamptz DEFAULT NULL
) RETURNS uuid
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    normalized_active text[];
    normalized_out_of_scope text[];
    requested_hash bytea;
    selected_policy_id uuid;
    boundary timestamptz;
    current_activation public.provider_inventory_policy_activations%ROWTYPE;
    existing_activation public.provider_inventory_policy_activations%ROWTYPE;
    next_boundary timestamptz;
    result_activation_id uuid;
BEGIN
    IF policy_node_type IS NULL
       OR octet_length(policy_node_type) NOT BETWEEN 2 AND 64
       OR policy_node_type !~ '^[a-z0-9][a-z0-9._-]*$'
       OR policy_driver_version IS NULL
       OR octet_length(policy_driver_version) NOT BETWEEN 1 AND 64
       OR policy_driver_version !~ '^[a-z0-9][a-z0-9._-]*$'
       OR requested_active_providers IS NULL
       OR requested_out_of_scope_providers IS NULL
       OR policy_actor IS NULL
       OR octet_length(policy_actor) NOT BETWEEN 1 AND 128
       OR policy_actor !~ '^[A-Za-z0-9][A-Za-z0-9._@:-]*$' THEN
        RAISE EXCEPTION 'provider policy registration rejected' USING ERRCODE = '22023';
    END IF;
    normalized_active := public.control_normalize_provider_set(requested_active_providers);
    normalized_out_of_scope := public.control_normalize_provider_set(requested_out_of_scope_providers);
    IF cardinality(normalized_active) + cardinality(normalized_out_of_scope) = 0
       OR normalized_active && normalized_out_of_scope THEN
        RAISE EXCEPTION 'provider policy registration rejected' USING ERRCODE = '22023';
    END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended(
        policy_node_type || chr(31) || policy_driver_version,
        0
    ));
    PERFORM 1 FROM public.node_drivers
    WHERE node_type = policy_node_type
      AND driver_contract_version = policy_driver_version;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'provider policy scope is not registered' USING ERRCODE = '23503';
    END IF;

    requested_hash := public.control_provider_policy_hash(
        policy_node_type,
        policy_driver_version,
        normalized_active,
        normalized_out_of_scope
    );
    SELECT policy_version_id INTO selected_policy_id
    FROM public.provider_inventory_policy_versions
    WHERE node_type = policy_node_type
      AND driver_contract_version = policy_driver_version
      AND content_hash = requested_hash;
    IF selected_policy_id IS NULL THEN
        selected_policy_id := gen_random_uuid();
        INSERT INTO public.provider_inventory_policy_versions (
            policy_version_id,
            node_type,
            driver_contract_version,
            active_providers,
            out_of_scope_providers,
            created_by
        ) VALUES (
            selected_policy_id,
            policy_node_type,
            policy_driver_version,
            normalized_active,
            normalized_out_of_scope,
            policy_actor
        );
    END IF;

    PERFORM 1 FROM public.provider_inventory_policy_bindings
    WHERE node_type = policy_node_type
      AND driver_contract_version = policy_driver_version
    FOR UPDATE;

    boundary := COALESCE(requested_effective_at, clock_timestamp());
    IF requested_effective_at IS NOT NULL AND boundary < clock_timestamp() THEN
        RAISE EXCEPTION 'provider policy activation cannot be backfilled' USING ERRCODE = '22023';
    END IF;

    SELECT * INTO existing_activation
    FROM public.provider_inventory_policy_activations
    WHERE node_type = policy_node_type
      AND driver_contract_version = policy_driver_version
      AND policy_version_id = selected_policy_id
      AND (
          effective_from = boundary
          OR (requested_effective_at IS NULL AND boundary <@ active_range)
      )
    ORDER BY effective_from DESC
    LIMIT 1;
    IF FOUND THEN
        INSERT INTO public.provider_inventory_policy_bindings (
            node_type, driver_contract_version, policy_version_id, bound_by, bound_at
        ) VALUES (
            policy_node_type, policy_driver_version, selected_policy_id, policy_actor, clock_timestamp()
        )
        ON CONFLICT (node_type, driver_contract_version) DO UPDATE
        SET policy_version_id = EXCLUDED.policy_version_id,
            bound_by = EXCLUDED.bound_by,
            bound_at = EXCLUDED.bound_at;
        RETURN existing_activation.activation_id;
    END IF;

    SELECT * INTO current_activation
    FROM public.provider_inventory_policy_activations
    WHERE node_type = policy_node_type
      AND driver_contract_version = policy_driver_version
      AND boundary <@ active_range
    FOR UPDATE;
    IF FOUND THEN
        IF current_activation.effective_from = boundary THEN
            RAISE EXCEPTION 'provider policy activation boundary conflicts'
                USING ERRCODE = '23P01';
        END IF;
        UPDATE public.provider_inventory_policy_activations
        SET effective_to = boundary
        WHERE activation_id = current_activation.activation_id;
    END IF;

    SELECT min(effective_from) INTO next_boundary
    FROM public.provider_inventory_policy_activations
    WHERE node_type = policy_node_type
      AND driver_contract_version = policy_driver_version
      AND effective_from > boundary;

    result_activation_id := gen_random_uuid();
    INSERT INTO public.provider_inventory_policy_activations (
        activation_id,
        node_type,
        driver_contract_version,
        policy_version_id,
        effective_from,
        effective_to,
        activated_by,
        created_at
    ) VALUES (
        result_activation_id,
        policy_node_type,
        policy_driver_version,
        selected_policy_id,
        boundary,
        next_boundary,
        policy_actor,
        LEAST(clock_timestamp(), boundary)
    );
    INSERT INTO public.provider_inventory_policy_bindings (
        node_type, driver_contract_version, policy_version_id, bound_by, bound_at
    ) VALUES (
        policy_node_type, policy_driver_version, selected_policy_id, policy_actor, clock_timestamp()
    )
    ON CONFLICT (node_type, driver_contract_version) DO UPDATE
    SET policy_version_id = EXCLUDED.policy_version_id,
        bound_by = EXCLUDED.bound_by,
        bound_at = EXCLUDED.bound_at;
    RETURN result_activation_id;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_set_node_inventory_monitoring(
    monitored_instance_id uuid,
    monitoring_enabled boolean,
    requested_effective_at timestamptz,
    monitoring_reason text,
    monitoring_actor text
) RETURNS uuid
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    boundary timestamptz;
    activation_id uuid;
    existing public.relay_node_inventory_monitoring_activations%ROWTYPE;
BEGIN
    IF monitored_instance_id IS NULL
       OR monitoring_enabled IS NULL
       OR monitoring_reason IS NULL
       OR monitoring_actor IS NULL
       OR octet_length(monitoring_actor) NOT BETWEEN 1 AND 128
       OR monitoring_actor !~ '^[A-Za-z0-9][A-Za-z0-9._@:-]*$'
       OR (
           monitoring_enabled
           AND monitoring_reason NOT IN (
               'deployment_enable', 'scheduled_enable', 'reconciliation'
           )
       )
       OR (
           NOT monitoring_enabled
           AND monitoring_reason NOT IN (
               'deployment_disable', 'scheduled_disable', 'reconciliation'
           )
       ) THEN
        RAISE EXCEPTION 'monitoring activation metadata is invalid' USING ERRCODE = '22023';
    END IF;
    PERFORM 1 FROM public.relay_node_assets
    WHERE instance_id = monitored_instance_id
    FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'monitoring target is not registered' USING ERRCODE = '23503';
    END IF;
    boundary := COALESCE(requested_effective_at, clock_timestamp());
    IF requested_effective_at IS NOT NULL AND boundary < clock_timestamp() THEN
        RAISE EXCEPTION 'monitoring activation cannot be backfilled' USING ERRCODE = '22023';
    END IF;
    SELECT * INTO existing
    FROM public.relay_node_inventory_monitoring_activations
    WHERE instance_id = monitored_instance_id
      AND boundary <@ active_range
    FOR UPDATE;
    IF monitoring_enabled THEN
        IF FOUND THEN
            RETURN existing.monitoring_activation_id;
        END IF;
        activation_id := gen_random_uuid();
        INSERT INTO public.relay_node_inventory_monitoring_activations (
            monitoring_activation_id,
            instance_id,
            effective_from,
            reason,
            actor,
            created_at
        ) VALUES (
            activation_id,
            monitored_instance_id,
            boundary,
            monitoring_reason,
            monitoring_actor,
            LEAST(clock_timestamp(), boundary)
        );
        RETURN activation_id;
    END IF;

    IF NOT FOUND THEN
        SELECT * INTO existing
        FROM public.relay_node_inventory_monitoring_activations
        WHERE instance_id = monitored_instance_id
          AND effective_to = boundary
        ORDER BY end_recorded_at DESC
        LIMIT 1
        FOR UPDATE;
        IF FOUND THEN
            IF existing.end_reason IS DISTINCT FROM monitoring_reason
               OR existing.end_actor IS DISTINCT FROM monitoring_actor THEN
                RAISE EXCEPTION 'monitoring deactivation conflicts with existing boundary'
                    USING ERRCODE = '23505';
            END IF;
            RETURN existing.monitoring_activation_id;
        END IF;
        RETURN NULL;
    END IF;
    IF existing.effective_from = boundary THEN
        RAISE EXCEPTION 'monitoring activation boundary conflicts'
            USING ERRCODE = '23P01';
    END IF;
    UPDATE public.relay_node_inventory_monitoring_activations
    SET effective_to = boundary,
        end_reason = monitoring_reason,
        end_actor = monitoring_actor,
        end_recorded_at = clock_timestamp()
    WHERE monitoring_activation_id = existing.monitoring_activation_id;
    RETURN existing.monitoring_activation_id;
END;
$$;
-- +goose StatementEnd

-- Reconciliation exposes fixed issue codes and aggregate counts only. Running
-- it through this definer function lets the registrar verify state without
-- receiving direct SELECT privileges on tables containing Secret references.
-- +goose StatementBegin
CREATE FUNCTION public.control_reconcile_asset_registry(
    expected_environment_id text,
    expected_environment_type text
) RETURNS TABLE (issue_code text, issue_count bigint)
LANGUAGE plpgsql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
BEGIN
    IF expected_environment_id IS NULL
       OR expected_environment_type IS NULL
       OR octet_length(expected_environment_id) NOT BETWEEN 1 AND 128
       OR expected_environment_id !~ '^[A-Za-z0-9][A-Za-z0-9._-]*$'
       OR expected_environment_type NOT IN ('dev', 'staging', 'production') THEN
        RAISE EXCEPTION 'reconciliation identity input is invalid' USING ERRCODE = '22023';
    END IF;
    RETURN QUERY
    SELECT 'environment_identity_mismatch'::text, count(*)::bigint
    FROM public.environments
    WHERE singleton_id <> 1
       OR environment_id <> expected_environment_id
       OR environment_type <> expected_environment_type
    UNION ALL
    SELECT 'environment_singleton_count', abs(count(*) - 1)::bigint
    FROM public.environments
    UNION ALL
    SELECT 'gateway_count_exceeds_one', greatest(count(*) - 1, 0)::bigint
    FROM public.gateway_instances
    UNION ALL
    SELECT 'node_driver_orphan', count(*)::bigint
    FROM public.relay_node_assets AS node
    LEFT JOIN public.node_drivers AS driver
      ON driver.node_type = node.node_type
     AND driver.driver_contract_version = node.driver_contract_version
    WHERE driver.node_type IS NULL
    UNION ALL
    SELECT 'node_capability_orphan', count(*)::bigint
    FROM public.node_capabilities AS declared
    LEFT JOIN public.driver_capabilities AS supported
      ON supported.node_type = declared.node_type
     AND supported.driver_contract_version = declared.driver_contract_version
     AND supported.capability = declared.capability
    WHERE supported.capability IS NULL
    UNION ALL
    SELECT 'policy_binding_scope_mismatch', count(*)::bigint
    FROM public.provider_inventory_policy_bindings AS binding
    LEFT JOIN public.provider_inventory_policy_versions AS version
      ON version.policy_version_id = binding.policy_version_id
     AND version.node_type = binding.node_type
     AND version.driver_contract_version = binding.driver_contract_version
    WHERE version.policy_version_id IS NULL
    UNION ALL
    SELECT 'policy_current_overlap', count(*)::bigint
    FROM (
        SELECT node_type, driver_contract_version
        FROM public.provider_inventory_policy_activations
        WHERE CURRENT_TIMESTAMP <@ active_range
        GROUP BY node_type, driver_contract_version
        HAVING count(*) > 1
    ) AS overlapping
    UNION ALL
    SELECT 'policy_activation_without_binding', count(*)::bigint
    FROM public.provider_inventory_policy_activations AS activation
    LEFT JOIN public.provider_inventory_policy_bindings AS binding
      ON binding.node_type = activation.node_type
     AND binding.driver_contract_version = activation.driver_contract_version
    WHERE binding.node_type IS NULL
    UNION ALL
    SELECT 'monitoring_current_overlap', count(*)::bigint
    FROM (
        SELECT instance_id
        FROM public.relay_node_inventory_monitoring_activations
        WHERE CURRENT_TIMESTAMP <@ active_range
        GROUP BY instance_id
        HAVING count(*) > 1
    ) AS overlapping
    ORDER BY 1;
END;
$$;
-- +goose StatementEnd

-- Provisioning is intentionally external to the migration in production.
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'relay_control_runtime') THEN
        RAISE EXCEPTION 'database role relay_control_runtime must be provisioned before migration'
            USING ERRCODE = '42704';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'relay_control_asset_registrar') THEN
        RAISE EXCEPTION 'database role relay_control_asset_registrar must be provisioned before migration'
            USING ERRCODE = '42704';
    END IF;
END;
$$;
-- +goose StatementEnd

REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON environments FROM relay_control_runtime;
GRANT SELECT ON environments TO relay_control_runtime;

REVOKE ALL ON TABLE
    gateway_instances,
    node_drivers,
    driver_capabilities,
    relay_node_assets,
    node_capabilities,
    provider_inventory_policy_versions,
    provider_inventory_policy_bindings,
    provider_inventory_policy_activations,
    relay_node_inventory_monitoring_activations
FROM PUBLIC, relay_control_runtime, relay_control_asset_registrar;

GRANT SELECT (
    singleton_id,
    instance_id,
    display_name,
    management_endpoint,
    reader_secret_configured,
    created_at,
    updated_at
) ON gateway_instances TO relay_control_runtime;
GRANT SELECT (
    instance_id,
    display_name,
    node_type,
    driver_contract_version,
    management_endpoint,
    reader_secret_configured,
    created_at,
    updated_at
) ON relay_node_assets TO relay_control_runtime;
GRANT SELECT ON TABLE
    node_drivers,
    driver_capabilities,
    node_capabilities,
    provider_inventory_policy_versions,
    provider_inventory_policy_bindings,
    provider_inventory_policy_activations,
    relay_node_inventory_monitoring_activations
TO relay_control_runtime;

GRANT SELECT ON environments TO relay_control_asset_registrar;

REVOKE EXECUTE ON FUNCTION public.control_normalize_asset_endpoint(text) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_valid_secret_reference(text) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_normalize_provider_set(text[]) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_provider_policy_hash(text, text, text[], text[]) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_register_gateway(uuid, text, text, text) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_register_node_driver(text, text, text, text, text[]) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_register_relay_node(uuid, text, text, text, text, text, text[]) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_activate_provider_policy(text, text, text[], text[], text, timestamptz) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_set_node_inventory_monitoring(uuid, boolean, timestamptz, text, text) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_reconcile_asset_registry(text, text) FROM PUBLIC;

GRANT EXECUTE ON FUNCTION public.control_normalize_asset_endpoint(text) TO relay_control_asset_registrar;
GRANT EXECUTE ON FUNCTION public.control_normalize_provider_set(text[]) TO relay_control_asset_registrar;
GRANT EXECUTE ON FUNCTION public.control_provider_policy_hash(text, text, text[], text[]) TO relay_control_asset_registrar;
GRANT EXECUTE ON FUNCTION public.control_register_gateway(uuid, text, text, text) TO relay_control_asset_registrar;
GRANT EXECUTE ON FUNCTION public.control_register_node_driver(text, text, text, text, text[]) TO relay_control_asset_registrar;
GRANT EXECUTE ON FUNCTION public.control_register_relay_node(uuid, text, text, text, text, text, text[]) TO relay_control_asset_registrar;
GRANT EXECUTE ON FUNCTION public.control_activate_provider_policy(text, text, text[], text[], text, timestamptz) TO relay_control_asset_registrar;
GRANT EXECUTE ON FUNCTION public.control_set_node_inventory_monitoring(uuid, boolean, timestamptz, text, text) TO relay_control_asset_registrar;
GRANT EXECUTE ON FUNCTION public.control_reconcile_asset_registry(text, text) TO relay_control_asset_registrar;

-- +goose Down
-- +goose StatementBegin
DO $$
DECLARE
    registered_count bigint;
BEGIN
    -- SHARE conflicts with every registrar write lock and is held by Goose's
    -- migration transaction through the subsequent DROP statements. This
    -- closes the count-then-drop race without requiring direct registrar
    -- privileges on the tables.
    LOCK TABLE
        gateway_instances,
        node_drivers,
        driver_capabilities,
        relay_node_assets,
        node_capabilities,
        provider_inventory_policy_versions,
        provider_inventory_policy_bindings,
        provider_inventory_policy_activations,
        relay_node_inventory_monitoring_activations
    IN SHARE MODE NOWAIT;
    SELECT
        (SELECT count(*) FROM gateway_instances)
        + (SELECT count(*) FROM node_drivers)
        + (SELECT count(*) FROM driver_capabilities)
        + (SELECT count(*) FROM relay_node_assets)
        + (SELECT count(*) FROM node_capabilities)
        + (SELECT count(*) FROM provider_inventory_policy_versions)
        + (SELECT count(*) FROM provider_inventory_policy_bindings)
        + (SELECT count(*) FROM provider_inventory_policy_activations)
        + (SELECT count(*) FROM relay_node_inventory_monitoring_activations)
    INTO registered_count;
    IF registered_count <> 0 THEN
        RAISE EXCEPTION 'asset registry migration down requires an empty registry'
            USING ERRCODE = '55000';
    END IF;
END;
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION public.control_set_node_inventory_monitoring(uuid, boolean, timestamptz, text, text) FROM relay_control_asset_registrar;
REVOKE EXECUTE ON FUNCTION public.control_activate_provider_policy(text, text, text[], text[], text, timestamptz) FROM relay_control_asset_registrar;
REVOKE EXECUTE ON FUNCTION public.control_reconcile_asset_registry(text, text) FROM relay_control_asset_registrar;
REVOKE EXECUTE ON FUNCTION public.control_register_relay_node(uuid, text, text, text, text, text, text[]) FROM relay_control_asset_registrar;
REVOKE EXECUTE ON FUNCTION public.control_register_node_driver(text, text, text, text, text[]) FROM relay_control_asset_registrar;
REVOKE EXECUTE ON FUNCTION public.control_register_gateway(uuid, text, text, text) FROM relay_control_asset_registrar;
REVOKE EXECUTE ON FUNCTION public.control_provider_policy_hash(text, text, text[], text[]) FROM relay_control_asset_registrar;
REVOKE EXECUTE ON FUNCTION public.control_normalize_provider_set(text[]) FROM relay_control_asset_registrar;
REVOKE EXECUTE ON FUNCTION public.control_normalize_asset_endpoint(text) FROM relay_control_asset_registrar;

DROP FUNCTION public.control_set_node_inventory_monitoring(uuid, boolean, timestamptz, text, text);
DROP FUNCTION public.control_activate_provider_policy(text, text, text[], text[], text, timestamptz);
DROP FUNCTION public.control_reconcile_asset_registry(text, text);
DROP FUNCTION public.control_register_relay_node(uuid, text, text, text, text, text, text[]);
DROP FUNCTION public.control_register_node_driver(text, text, text, text, text[]);
DROP FUNCTION public.control_register_gateway(uuid, text, text, text);
DROP TABLE relay_node_inventory_monitoring_activations;
DROP TABLE provider_inventory_policy_activations;
DROP TABLE provider_inventory_policy_bindings;
DROP TRIGGER provider_inventory_policy_versions_truncate_immutable ON provider_inventory_policy_versions;
DROP TRIGGER provider_inventory_policy_versions_immutable ON provider_inventory_policy_versions;
DROP FUNCTION public.control_reject_provider_policy_mutation();
DROP TABLE provider_inventory_policy_versions;
DROP TABLE node_capabilities;
DROP TABLE relay_node_assets;
DROP TABLE driver_capabilities;
DROP TABLE node_drivers;
DROP TABLE gateway_instances;
DROP FUNCTION public.control_provider_policy_hash(text, text, text[], text[]);
DROP FUNCTION public.control_normalize_provider_set(text[]);
DROP FUNCTION public.control_valid_secret_reference(text);
DROP FUNCTION public.control_normalize_asset_endpoint(text);
DROP TRIGGER environments_identity_truncate_guard ON environments;
DROP TRIGGER environments_identity_guard ON environments;
DROP FUNCTION public.control_protect_environment_identity();
ALTER TABLE environments DROP CONSTRAINT environments_name_canonical;
ALTER TABLE environments DROP CONSTRAINT environments_environment_id_canonical;
GRANT INSERT ON environments TO relay_control_runtime;
REVOKE SELECT ON environments FROM relay_control_asset_registrar;
