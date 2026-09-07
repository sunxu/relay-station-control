-- +goose Up

-- A bounded, retention-safe Provider projection for the readonly topology
-- surface.  The expected set is policy/monitoring truth; held states keep
-- historical out-of-scope Providers visible without changing their truth.
-- No account rows are consulted.
-- +goose StatementBegin
CREATE FUNCTION public.control_query_account_inventory_provider_states_v1(
    target_instance_id uuid
) RETURNS TABLE (
    provider text,
    monitoring_status text,
    state text,
    current_scheduled_at timestamptz,
    last_complete_at timestamptz,
    snapshot_freshness text,
    health_scheduled_at timestamptz,
    health_degraded boolean,
    health_reason text
)
LANGUAGE plpgsql
SECURITY DEFINER
STABLE
SET search_path = pg_catalog
SET TimeZone = 'UTC'
AS $$
BEGIN
    IF target_instance_id IS NULL
       OR target_instance_id = '00000000-0000-0000-0000-000000000000'::uuid THEN
        RAISE EXCEPTION 'invalid account inventory Provider state query'
            USING ERRCODE = '22023';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM public.relay_node_assets AS asset
        WHERE asset.instance_id = target_instance_id
    ) THEN
        RAISE EXCEPTION 'account inventory instance is not registered'
            USING ERRCODE = 'P0404';
    END IF;
    IF EXISTS (
        SELECT 1
        FROM public.account_inventory_provider_states AS state
        WHERE state.instance_id = target_instance_id
          AND (
              state.state IS DISTINCT FROM 'current'
              OR state.monitoring_status NOT IN ('active', 'out_of_scope')
              OR state.current_scheduled_at IS NULL
              OR state.last_complete_at IS NULL
              OR state.health_scheduled_at IS NULL
              OR state.health_degraded IS NULL
              OR state.health_reason IS NULL
              OR state.health_reason NOT IN (
                  'none', 'transport_failed', 'contract_invalid', 'disk_fallback',
                  'node_identity_incomplete', 'identity_incomplete'
              )
              OR state.health_degraded <> (state.health_reason <> 'none')
          )
    ) THEN
        RAISE EXCEPTION 'account inventory Provider state is inconsistent'
            USING ERRCODE = 'P0503';
    END IF;

    RETURN QUERY
WITH target AS (
    SELECT asset.instance_id, asset.node_type, asset.driver_contract_version
    FROM public.relay_node_assets AS asset
    WHERE asset.instance_id = target_instance_id
),
expected AS (
    SELECT target.instance_id, provider.value AS provider
    FROM target
    JOIN public.provider_inventory_policy_activations AS activation
      ON activation.node_type = target.node_type
     AND activation.driver_contract_version = target.driver_contract_version
     AND activation.active_range @> statement_timestamp()
    JOIN public.provider_inventory_policy_versions AS policy
      ON policy.policy_version_id = activation.policy_version_id
     AND policy.node_type = activation.node_type
     AND policy.driver_contract_version = activation.driver_contract_version
    CROSS JOIN LATERAL unnest(policy.active_providers) AS provider(value)
    WHERE EXISTS (
        SELECT 1
        FROM public.relay_node_inventory_monitoring_activations AS monitoring
        WHERE monitoring.instance_id = target.instance_id
          AND monitoring.active_range @> statement_timestamp()
    )
),
held AS (
    SELECT state.instance_id, state.provider
    FROM public.account_inventory_provider_states AS state
    JOIN target ON target.instance_id = state.instance_id
),
provider_set AS (
    SELECT expected.instance_id, expected.provider FROM expected
    UNION
    SELECT held.instance_id, held.provider FROM held
)
SELECT provider_set.provider,
       CASE WHEN state.instance_id IS NULL THEN 'active'
            ELSE state.monitoring_status END AS monitoring_status,
       state.state,
       state.current_scheduled_at,
       state.last_complete_at,
       CASE
           WHEN state.instance_id IS NULL THEN 'unknown'
           WHEN state.monitoring_status = 'out_of_scope' THEN 'out_of_scope'
           WHEN statement_timestamp() - state.last_complete_at <= interval '15 minutes'
               THEN 'fresh'
           ELSE 'stale'
       END AS snapshot_freshness,
       state.health_scheduled_at,
       state.health_degraded,
       state.health_reason
FROM provider_set
LEFT JOIN public.account_inventory_provider_states AS state
  ON state.instance_id = provider_set.instance_id
 AND state.provider = provider_set.provider
ORDER BY provider_set.provider;
END;
$$;
-- +goose StatementEnd

ALTER FUNCTION public.control_query_account_inventory_provider_states_v1(uuid)
    OWNER TO relay_control_migrator;
REVOKE EXECUTE ON FUNCTION public.control_query_account_inventory_provider_states_v1(uuid)
    FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_query_account_inventory_provider_states_v1(uuid)
    TO relay_control_runtime;

-- +goose Down

DROP FUNCTION public.control_query_account_inventory_provider_states_v1(uuid);
