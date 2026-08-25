\set ON_ERROR_STOP on

-- Required psql variables: node_instance_id, enabled, effective_at,
-- reason and actor. Empty effective_at means database time now. Reasons are
-- restricted by the schema to the deployment/scheduled/reconciliation enum.

SELECT (
    :'effective_at' = ''
    OR :'effective_at' ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}([.][0-9]+)?(Z|[+-][0-9]{2}:[0-9]{2})$'
) AS effective_at_valid
\gset
\if :effective_at_valid
\else
    \echo 'effective_at must be empty or offset-qualified RFC3339'
    \quit 2
\endif

BEGIN ISOLATION LEVEL SERIALIZABLE;
SET LOCAL TIME ZONE 'UTC';

SELECT public.control_set_node_inventory_monitoring(
    :'node_instance_id'::uuid,
    :'enabled'::boolean,
    NULLIF(:'effective_at', '')::timestamptz,
    :'reason',
    :'actor'
);

COMMIT;
