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

-- F0 belongs to the intent formed before the write transaction. The controlled
-- writer takes the Node lock and performs a fresh F1 lookup as its next SQL
-- statement, rejecting a stale intent with SQLSTATE 55000.
SELECT COALESCE(
    public.control_latest_node_disable_fence_v1(:'node_instance_id'::uuid)::text,
    ''
) AS expected_disable_fence
\gset

BEGIN ISOLATION LEVEL READ COMMITTED;
SET LOCAL TIME ZONE 'UTC';

SELECT public.control_set_node_inventory_monitoring(
    :'node_instance_id'::uuid,
    :'enabled'::boolean,
    NULLIF(:'effective_at', '')::timestamptz,
    :'reason',
    :'actor',
    NULLIF(:'expected_disable_fence', '')::uuid
);

COMMIT;
