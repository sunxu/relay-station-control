-- name: QueryCurrentAccountInventoryV1 :many
SELECT to_jsonb(inventory) AS inventory
FROM public.control_query_current_account_inventory_v1(
    sqlc.arg(instance_id)::uuid,
    sqlc.arg(provider)::text,
    sqlc.arg(lifecycle)::text,
    sqlc.arg(basic_status)::text,
    sqlc.arg(normalized_email)::text,
    sqlc.arg(after_account_key)::text,
    sqlc.arg(page_limit)::integer
) AS inventory;

-- name: CheckAccountInventoryReadonlyQueryCompatibility :one
SELECT
    to_regprocedure(
        'public.control_query_current_account_inventory_v1(uuid,text,text,text,text,text,integer)'
    ) IS NOT NULL
    AND to_regclass('public.account_inventory_normalized_email_read_idx') IS NOT NULL
    AND to_regclass('public.account_inventory_basic_status_read_idx') IS NOT NULL
    AND has_function_privilege(
        current_user,
        'public.control_query_current_account_inventory_v1(uuid,text,text,text,text,text,integer)',
        'EXECUTE'
    )
    AND NOT has_table_privilege(current_user, 'public.account_inventory', 'SELECT')
    AND NOT has_table_privilege(current_user, 'public.account_inventory_provider_states', 'SELECT');
