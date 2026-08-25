\set ON_ERROR_STOP on

-- Required psql variables:
-- gateway_instance_id, gateway_display_name, gateway_endpoint,
-- driver_node_type,
-- driver_contract_version, driver_display_name, driver_lifecycle_status,
-- driver_capabilities_csv (sorted), node_instance_id, node_display_name,
-- node_endpoint,
-- node_capabilities_csv (sorted).
-- Secret references are read from the fixed environment variables
-- CONTROL_GATEWAY_READER_SECRET_REF and CONTROL_NODE_READER_SECRET_REF. Set an
-- environment variable to the empty string to register no Secret reference.
-- Only opaque Secret references are accepted; never pass credential contents.

\getenv gateway_reader_secret_ref CONTROL_GATEWAY_READER_SECRET_REF
\getenv node_reader_secret_ref CONTROL_NODE_READER_SECRET_REF

BEGIN ISOLATION LEVEL SERIALIZABLE;
SET LOCAL TIME ZONE 'UTC';

SELECT public.control_register_node_driver(
    :'driver_node_type',
    :'driver_contract_version',
    :'driver_display_name',
    :'driver_lifecycle_status',
    string_to_array(:'driver_capabilities_csv', ',')
);

SELECT public.control_register_gateway(
    :'gateway_instance_id'::uuid,
    :'gateway_display_name',
    :'gateway_endpoint',
    NULLIF(substr($1, 2), '')
)
\bind x:gateway_reader_secret_ref
\g

SELECT public.control_register_relay_node(
    :'node_instance_id'::uuid,
    :'node_display_name',
    :'driver_node_type',
    :'driver_contract_version',
    :'node_endpoint',
    NULLIF(substr($1, 2), ''),
    string_to_array(:'node_capabilities_csv', ',')
)
\bind x:node_reader_secret_ref
\g

COMMIT;
