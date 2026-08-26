\set ON_ERROR_STOP on

-- Required psql variables: node_type, driver_contract_version,
-- active_providers_csv, out_of_scope_providers_csv, actor, reason, effective_at.
-- Provider lists may be empty individually but must be sorted, unique,
-- normalized and not overlap. Empty effective_at means database time now.

\getenv policy_mutation_enabled CONTROL_PROVIDER_POLICY_MUTATION_ENABLED
\if :{?policy_mutation_enabled}
SELECT :'policy_mutation_enabled' = 'true' AS policy_mutation_enabled_valid
\gset
\if :policy_mutation_enabled_valid
\else
    DO $$ BEGIN
        RAISE EXCEPTION 'Provider policy mutation is disabled' USING ERRCODE = '42501';
    END $$;
\endif
\else
    DO $$ BEGIN
        RAISE EXCEPTION 'Provider policy mutation is disabled' USING ERRCODE = '42501';
    END $$;
\endif

SELECT (
    octet_length(:'reason') BETWEEN 1 AND 500
    AND :'reason' !~ '[[:cntrl:]]'
) AS reason_valid
\gset
\if :reason_valid
\else
    DO $$ BEGIN
        RAISE EXCEPTION 'Provider policy reason is invalid' USING ERRCODE = '22023';
    END $$;
\endif

SELECT (
    :'effective_at' = ''
    OR :'effective_at' ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}([.][0-9]+)?(Z|[+-][0-9]{2}:[0-9]{2})$'
) AS effective_at_valid
\gset
\if :effective_at_valid
\else
    DO $$ BEGIN
        RAISE EXCEPTION 'Provider policy effective time is invalid' USING ERRCODE = '22023';
    END $$;
\endif

BEGIN ISOLATION LEVEL SERIALIZABLE;
SET LOCAL TIME ZONE 'UTC';

SELECT public.control_activate_provider_policy_with_lifecycle(
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
    :'reason',
    NULLIF(:'effective_at', '')::timestamptz
);

COMMIT;
