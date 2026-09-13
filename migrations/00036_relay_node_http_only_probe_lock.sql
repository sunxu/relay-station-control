-- +goose Up

-- Node management transport is HTTP-only.  Do not rewrite an existing
-- durable endpoint: an affected deployment must be corrected explicitly.
LOCK TABLE public.relay_node_assets IN ACCESS EXCLUSIVE MODE;

-- +goose StatementBegin
DO $$
DECLARE
    affected_instances text;
BEGIN
    SELECT string_agg(instance_id::text, ', ' ORDER BY instance_id)
      INTO affected_instances
    FROM public.relay_node_assets
    WHERE left(management_endpoint, 7) <> 'http://';

    IF affected_instances IS NOT NULL THEN
        RAISE EXCEPTION
            'relay Node migration requires HTTP-only management endpoints; affected instance IDs: %',
            affected_instances
            USING ERRCODE = '22023';
    END IF;
END;
$$;
-- +goose StatementEnd

ALTER TABLE public.relay_node_assets
    ADD CONSTRAINT relay_node_assets_http_only_check
    CHECK (left(management_endpoint, 7) = 'http://') NOT VALID;

ALTER TABLE public.relay_node_assets
    VALIDATE CONSTRAINT relay_node_assets_http_only_check;

-- Probe authorization takes a lifecycle read lock and returns only the fixed,
-- secret-free target projection.  The lock is released when the caller's
-- short transaction commits, before any network request is made.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_authorize_node_probe_v1(target_instance_id uuid)
RETURNS TABLE(
    instance_id uuid,
    lifecycle_status text,
    node_type text,
    driver_contract_version text,
    management_endpoint text,
    capabilities text[]
)
LANGUAGE plpgsql SECURITY DEFINER VOLATILE SET search_path = pg_catalog AS $$
DECLARE
    locked_instance_id uuid;
    locked_lifecycle_status text;
    locked_node_type text;
    locked_driver_contract_version text;
    locked_management_endpoint text;
    declared_capabilities text[];
BEGIN
    SELECT asset.instance_id,
           asset.lifecycle_status,
           asset.node_type,
           asset.driver_contract_version,
           asset.management_endpoint
      INTO locked_instance_id,
           locked_lifecycle_status,
           locked_node_type,
           locked_driver_contract_version,
           locked_management_endpoint
    FROM public.relay_node_assets AS asset
    WHERE asset.instance_id = target_instance_id
    FOR SHARE;

    IF NOT FOUND THEN
        RETURN;
    END IF;

    SELECT COALESCE(
               array_agg(capability.capability ORDER BY capability.capability)
                   FILTER (WHERE capability.capability IS NOT NULL),
               ARRAY[]::text[]
           )
      INTO declared_capabilities
    FROM public.node_capabilities AS capability
    WHERE capability.instance_id = locked_instance_id
      AND capability.node_type = locked_node_type
      AND capability.driver_contract_version = locked_driver_contract_version;

    instance_id := locked_instance_id;
    lifecycle_status := locked_lifecycle_status;
    node_type := locked_node_type;
    driver_contract_version := locked_driver_contract_version;
    management_endpoint := locked_management_endpoint;
    capabilities := declared_capabilities;
    RETURN NEXT;
END;
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_authorize_node_probe_v1(uuid) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_authorize_node_probe_v1(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_authorize_node_probe_v1(uuid) TO relay_control_runtime;

-- +goose Down
-- Forward-only: the HTTP-only durable boundary and probe lock contract are
-- retained on rollback.
-- +goose StatementBegin
DO $$
BEGIN
    RAISE EXCEPTION 'migration 36 is forward-only' USING ERRCODE = '55000';
END;
$$;
-- +goose StatementEnd
