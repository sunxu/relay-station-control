-- name: GetGatewayAsset :one
SELECT
    instance_id,
    display_name,
    management_endpoint,
    COALESCE(reader_secret_configured, false)::boolean AS secret_configured,
    created_at,
    updated_at,
    lifecycle_status,
    revision,
    retired_at,
    retired_by,
    retire_reason
FROM gateway_instances
WHERE singleton_id = 1;

-- name: ListNodeDrivers :many
SELECT
    driver.node_type,
    driver.driver_contract_version,
    driver.display_name,
    driver.lifecycle_status,
    COALESCE(
        array_agg(capability.capability ORDER BY capability.capability)
            FILTER (WHERE capability.capability IS NOT NULL),
        ARRAY[]::text[]
    )::text[] AS capabilities,
    driver.created_at
FROM node_drivers AS driver
LEFT JOIN driver_capabilities AS capability
    ON capability.node_type = driver.node_type
   AND capability.driver_contract_version = driver.driver_contract_version
GROUP BY
    driver.node_type,
    driver.driver_contract_version,
    driver.display_name,
    driver.lifecycle_status,
    driver.created_at
ORDER BY driver.node_type, driver.driver_contract_version;

-- name: ListNodeAssets :many
SELECT
    node.instance_id,
    node.display_name,
    node.node_type,
    node.driver_contract_version,
    node.management_endpoint,
    COALESCE(node.reader_secret_configured, false)::boolean AS secret_configured,
    node.lifecycle_status,
    node.revision,
    node.retired_at,
    node.retired_by,
    node.retire_reason,
    COALESCE(
        (
            SELECT array_agg(capability.capability ORDER BY capability.capability)::text[]
            FROM node_capabilities AS capability
            WHERE capability.instance_id = node.instance_id
        ),
        ARRAY[]::text[]
    )::text[] AS capabilities,
    (monitoring.active_match_count = 1)::boolean AS monitoring_active,
    monitoring.active_match_count AS monitoring_match_count,
    monitoring.effective_from AS monitoring_effective_from,
    monitoring.effective_to AS monitoring_effective_to,
    node.created_at,
    node.updated_at
FROM relay_node_assets AS node
LEFT JOIN LATERAL (
    SELECT
        count(*)::bigint AS active_match_count,
        (CASE WHEN count(*) = 1 THEN min(activation.effective_from) END)::timestamptz AS effective_from,
        (CASE WHEN count(*) = 1 THEN min(activation.effective_to) END)::timestamptz AS effective_to
    FROM relay_node_inventory_monitoring_activations AS activation
    WHERE activation.instance_id = node.instance_id
      AND sqlc.arg(read_as_of)::timestamptz <@ activation.active_range
) AS monitoring ON true
WHERE (sqlc.arg(lifecycle)::text = 'all' OR node.lifecycle_status = sqlc.arg(lifecycle))
  AND (sqlc.narg(node_type)::text IS NULL OR node.node_type = sqlc.narg(node_type))
  AND (
      sqlc.narg(capability)::text IS NULL
      OR EXISTS (
          SELECT 1
          FROM node_capabilities AS filtered_capability
          WHERE filtered_capability.instance_id = node.instance_id
            AND filtered_capability.capability = sqlc.narg(capability)
      )
  )
  AND (
      sqlc.narg(monitoring_active)::boolean IS NULL
      OR monitoring.active_match_count > 1
      OR sqlc.narg(monitoring_active) = (monitoring.active_match_count = 1)
  )
  AND (sqlc.narg(after_instance_id)::uuid IS NULL OR node.instance_id > sqlc.narg(after_instance_id))
ORDER BY node.instance_id
LIMIT sqlc.arg(page_size);

-- name: GetNodeAsset :one
SELECT
    node.instance_id,
    node.display_name,
    node.node_type,
    node.driver_contract_version,
    node.management_endpoint,
    COALESCE(node.reader_secret_configured, false)::boolean AS secret_configured,
    node.lifecycle_status,
    node.revision,
    node.retired_at,
    node.retired_by,
    node.retire_reason,
    COALESCE(
        (
            SELECT array_agg(capability.capability ORDER BY capability.capability)::text[]
            FROM node_capabilities AS capability
            WHERE capability.instance_id = node.instance_id
        ),
        ARRAY[]::text[]
    )::text[] AS capabilities,
    (monitoring.active_match_count = 1)::boolean AS monitoring_active,
    monitoring.active_match_count AS monitoring_match_count,
    monitoring.effective_from AS monitoring_effective_from,
    monitoring.effective_to AS monitoring_effective_to,
    node.created_at,
    node.updated_at
FROM relay_node_assets AS node
LEFT JOIN LATERAL (
    SELECT
        count(*)::bigint AS active_match_count,
        (CASE WHEN count(*) = 1 THEN min(activation.effective_from) END)::timestamptz AS effective_from,
        (CASE WHEN count(*) = 1 THEN min(activation.effective_to) END)::timestamptz AS effective_to
    FROM relay_node_inventory_monitoring_activations AS activation
    WHERE activation.instance_id = node.instance_id
      AND transaction_timestamp() <@ activation.active_range
) AS monitoring ON true
WHERE node.instance_id = $1;

-- name: GetNodeRegistryGeneration :one
SELECT node_generation FROM asset_registry_generations WHERE singleton_id=1;

-- name: GetNodeRegistryReadAsOf :one
SELECT transaction_timestamp()::timestamptz;

-- name: GetNodeLifecycleCounts :one
SELECT count(*) FILTER (WHERE lifecycle_status='active')::bigint AS active,
       count(*) FILTER (WHERE lifecycle_status='retired')::bigint AS retired,
       count(*)::bigint AS total
FROM relay_node_assets;

-- name: GetCurrentProviderInventoryPolicy :one
WITH current_policy AS (
    SELECT
        policy.policy_version_id,
        policy.node_type,
        policy.driver_contract_version,
        policy.active_providers,
        policy.out_of_scope_providers,
        activation.effective_from,
        activation.effective_to,
        policy.created_at
    FROM provider_inventory_policy_activations AS activation
    JOIN provider_inventory_policy_versions AS policy
      ON policy.policy_version_id = activation.policy_version_id
     AND policy.node_type = activation.node_type
     AND policy.driver_contract_version = activation.driver_contract_version
    WHERE activation.node_type = $1
      AND activation.driver_contract_version = $2
      AND CURRENT_TIMESTAMP <@ activation.active_range
), matched AS (
    SELECT count(*)::bigint AS current_match_count
    FROM current_policy
)
SELECT
    matched.current_match_count,
    policy.policy_version_id,
    policy.node_type,
    policy.driver_contract_version,
    policy.active_providers,
    policy.out_of_scope_providers,
    policy.effective_from,
    policy.effective_to,
    policy.created_at
FROM matched
LEFT JOIN current_policy AS policy
  ON matched.current_match_count = 1;

-- name: GetAssetCounts :one
SELECT
    (SELECT count(instance_id) FROM gateway_instances)::bigint AS gateways,
    (SELECT count(instance_id) FROM relay_node_assets)::bigint AS nodes,
    (SELECT count(node_type) FROM node_drivers)::bigint AS drivers,
    (SELECT count(policy_version_id) FROM provider_inventory_policy_versions)::bigint AS provider_policies;

-- name: ActivateProviderPolicyWithLifecycle :one
SELECT public.control_activate_provider_policy_with_lifecycle(
    sqlc.arg(node_type)::text,
    sqlc.arg(driver_contract_version)::text,
    sqlc.arg(active_providers)::text[],
    sqlc.arg(out_of_scope_providers)::text[],
    sqlc.arg(actor)::text,
    sqlc.arg(reason)::text,
    sqlc.narg(effective_at)::timestamptz
)::uuid AS activation_id;
