\set ON_ERROR_STOP on

-- Required psql variables: expected_environment_id and
-- expected_environment_type. Results contain only fixed issue codes and
-- aggregate counts; endpoint and Secret reference values are never selected.

BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;

SELECT issue_code, issue_count
FROM public.control_reconcile_asset_registry(
    :'expected_environment_id',
    :'expected_environment_type'
)
ORDER BY issue_code;

SELECT EXISTS (
    SELECT 1
    FROM public.control_reconcile_asset_registry(
        :'expected_environment_id',
        :'expected_environment_type'
    )
    WHERE issue_count <> 0
) AS reconciliation_failed
\gset

\if :reconciliation_failed
    \echo 'asset registry reconciliation failed'
    ROLLBACK;
    \quit 3
\endif

COMMIT;
