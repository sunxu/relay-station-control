\set ON_ERROR_STOP on

-- Required psql variables: node_type, driver_contract_version,
-- active_providers_csv, out_of_scope_providers_csv, actor, effective_at.
-- Provider lists may be empty individually but must be sorted, unique,
-- normalized and not overlap. Empty effective_at means database time now.

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

SELECT public.control_activate_provider_policy(
    :'node_type',
    :'driver_contract_version',
    CASE WHEN :'active_providers_csv' = ''
        THEN ARRAY[]::text[]
        ELSE string_to_array(:'active_providers_csv', ',')
    END,
    CASE WHEN :'out_of_scope_providers_csv' = ''
        THEN ARRAY[]::text[]
        ELSE string_to_array(:'out_of_scope_providers_csv', ',')
    END,
    :'actor',
    NULLIF(:'effective_at', '')::timestamptz
);

COMMIT;
