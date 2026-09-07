-- name: GetAccountInventoryProviderStatesV1 :one
SELECT statement_timestamp()::timestamptz AS observed_at,
       COALESCE(
           jsonb_agg(to_jsonb(provider_state) ORDER BY provider_state.provider),
           '[]'::jsonb
       )::jsonb AS providers
FROM public.control_query_account_inventory_provider_states_v1(
    sqlc.arg(instance_id)::uuid
) AS provider_state;
