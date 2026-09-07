\set ON_ERROR_STOP on

-- Required psql variables: gateway_instance_id, actor_admin_id.
-- CONTROL_GATEWAY_DIRECTORY_READER_SECRET_REF must contain only an opaque
-- Secret reference.  Credential contents must never be passed to SQL.
\getenv gateway_reader_secret_ref CONTROL_GATEWAY_DIRECTORY_READER_SECRET_REF

BEGIN ISOLATION LEVEL SERIALIZABLE;
SET LOCAL TIME ZONE 'UTC';

SELECT public.control_set_gateway_directory_reader_initial_v1(
    :'gateway_instance_id'::uuid,
    NULLIF(substr($1, 2), ''),
    :'actor_admin_id'::uuid
)
\bind x:gateway_reader_secret_ref
\g

COMMIT;
