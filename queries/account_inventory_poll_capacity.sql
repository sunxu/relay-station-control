-- name: GetAccountInventoryPollCapacityV1 :one
SELECT capacity.evaluated_at::timestamptz AS evaluated_at,
       capacity.evaluated_slot::timestamptz AS evaluated_slot,
       capacity.eligible_node_count::bigint AS eligible_node_count
FROM public.control_query_account_inventory_poll_capacity_v1()
    AS capacity(evaluated_at, evaluated_slot, eligible_node_count);
