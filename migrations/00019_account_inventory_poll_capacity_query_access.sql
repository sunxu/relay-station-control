-- +goose Up

-- Read-only capacity projection.  Its eligibility predicate intentionally
-- mirrors Migration 5's scheduler predicate so admission and scheduling use
-- the same Node/policy/monitoring truth.
-- +goose StatementBegin
CREATE FUNCTION public.control_query_account_inventory_poll_capacity_v1()
RETURNS TABLE (
    evaluated_at timestamptz,
    evaluated_slot timestamptz,
    eligible_node_count bigint
)
LANGUAGE plpgsql
SECURITY DEFINER
STABLE
SET search_path = pg_catalog
SET TimeZone = 'UTC'
AS $$
DECLARE
    at_time timestamptz := statement_timestamp();
    slot_time timestamptz := to_timestamp(floor(extract(epoch FROM at_time) / 300) * 300);
    inconsistent_count bigint;
BEGIN
    SELECT count(*) INTO inconsistent_count
    FROM public.relay_node_assets AS asset
    WHERE EXISTS (
        SELECT 1 FROM public.node_capabilities AS capability
        WHERE capability.instance_id = asset.instance_id
          AND capability.node_type = asset.node_type
          AND capability.driver_contract_version = asset.driver_contract_version
          AND capability.capability = 'management_account_inventory_read'
    )
      AND EXISTS (
        SELECT 1 FROM public.relay_node_inventory_monitoring_activations AS monitoring
        WHERE monitoring.instance_id = asset.instance_id
          AND monitoring.active_range @> slot_time
    )
      AND (
        (SELECT count(*) FROM public.provider_inventory_policy_activations AS activation
         WHERE activation.node_type = asset.node_type
           AND activation.driver_contract_version = asset.driver_contract_version
           AND activation.active_range @> slot_time) <> 1
        OR NOT EXISTS (
            SELECT 1
            FROM public.provider_inventory_policy_activations AS activation
            JOIN public.provider_inventory_policy_bindings AS binding
              ON binding.node_type = activation.node_type
             AND binding.driver_contract_version = activation.driver_contract_version
             AND binding.policy_version_id = activation.policy_version_id
            JOIN public.provider_inventory_policy_versions AS policy
              ON policy.policy_version_id = activation.policy_version_id
             AND policy.node_type = activation.node_type
             AND policy.driver_contract_version = activation.driver_contract_version
            WHERE activation.node_type = asset.node_type
              AND activation.driver_contract_version = asset.driver_contract_version
              AND activation.active_range @> slot_time
              AND cardinality(policy.active_providers) > 0
        )
      );
    IF inconsistent_count <> 0 THEN
        RAISE EXCEPTION 'account inventory poll eligibility is inconsistent'
            USING ERRCODE = '23514';
    END IF;

    RETURN QUERY
    SELECT at_time, slot_time, count(DISTINCT asset.instance_id)
    FROM public.relay_node_assets AS asset
    JOIN public.node_capabilities AS capability
      ON capability.instance_id = asset.instance_id
     AND capability.node_type = asset.node_type
     AND capability.driver_contract_version = asset.driver_contract_version
     AND capability.capability = 'management_account_inventory_read'
    JOIN public.relay_node_inventory_monitoring_activations AS monitoring
      ON monitoring.instance_id = asset.instance_id
     AND monitoring.active_range @> slot_time
    JOIN public.provider_inventory_policy_activations AS activation
      ON activation.node_type = asset.node_type
     AND activation.driver_contract_version = asset.driver_contract_version
     AND activation.active_range @> slot_time
    JOIN public.provider_inventory_policy_bindings AS binding
      ON binding.node_type = activation.node_type
     AND binding.driver_contract_version = activation.driver_contract_version
     AND binding.policy_version_id = activation.policy_version_id
    JOIN public.provider_inventory_policy_versions AS policy
      ON policy.policy_version_id = activation.policy_version_id
     AND policy.node_type = activation.node_type
     AND policy.driver_contract_version = activation.driver_contract_version
     AND cardinality(policy.active_providers) > 0;
END;
$$;
-- +goose StatementEnd

ALTER FUNCTION public.control_query_account_inventory_poll_capacity_v1()
    OWNER TO relay_control_migrator;
REVOKE EXECUTE ON FUNCTION public.control_query_account_inventory_poll_capacity_v1()
    FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_query_account_inventory_poll_capacity_v1()
    TO relay_control_runtime;

-- +goose Down

DROP FUNCTION public.control_query_account_inventory_poll_capacity_v1();
