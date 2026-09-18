package main

import (
	"context"
	"errors"
	"os"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	accountadmin "github.com/sunxu/relay-station-control/internal/accountadmin"
	"github.com/sunxu/relay-station-control/internal/assetcredential"
	controlnodes "github.com/sunxu/relay-station-control/internal/drivers"
	controlcliproxy "github.com/sunxu/relay-station-control/internal/drivers/cliproxyapi"
	assetstore "github.com/sunxu/relay-station-control/internal/store"
)

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func newAccountOperationService(pool *pgxpool.Pool, management controlnodes.ValidatedManagementConfig, secrets controlnodes.AssetCredentialResolver, policies interface {
	CurrentProviderPolicy(context.Context, string, string) (*assetstore.ProviderInventoryPolicy, error)
}) (*accountadmin.Service, *assetstore.AccountOperationRepository, error) {
	repo, err := assetstore.NewAccountOperationRepository(pool)
	if err != nil {
		return nil, nil, err
	}
	if secrets == nil {
		return nil, nil, errors.New("account operation credential resolver unavailable")
	}
	resolver := productionAccountNodeResolverWithPolicy{pool: pool, management: management, assetSecrets: secrets, policies: policies}
	service, err := accountadmin.NewServiceWithIntentKeyPath(repo, resolver, os.Getenv("CONTROL_ACCOUNT_OPERATION_INTENT_KEY_FILE"))
	return service, repo, err
}

type productionAccountNodeResolverWithPolicy struct {
	pool         *pgxpool.Pool
	management   controlnodes.ValidatedManagementConfig
	assetSecrets controlnodes.AssetCredentialResolver
	policies     interface {
		CurrentProviderPolicy(context.Context, string, string) (*assetstore.ProviderInventoryPolicy, error)
	}
}

func (r productionAccountNodeResolverWithPolicy) Resolve(ctx context.Context, nodeID uuid.UUID, provider string) (accountadmin.NodeState, error) {
	if r.pool == nil || r.policies == nil || r.assetSecrets == nil {
		return accountadmin.NodeState{}, accountadmin.ErrNodeNotFound
	}
	var lifecycle, nodeType, contract, endpoint string
	var inventoryReadAllowed, monitoring bool
	err := r.pool.QueryRow(ctx, `
		SELECT lifecycle_status,node_type,driver_contract_version,management_endpoint,
			EXISTS (
				SELECT 1 FROM public.node_capabilities c
				WHERE c.instance_id=relay_node_assets.instance_id
				  AND c.node_type=relay_node_assets.node_type
				  AND c.driver_contract_version=relay_node_assets.driver_contract_version
				  AND c.capability='management_account_inventory_read'
			),
			EXISTS (
				SELECT 1 FROM relay_node_inventory_monitoring_activations m
				WHERE m.instance_id=relay_node_assets.instance_id
				  AND m.effective_from<=clock_timestamp()
				  AND (m.effective_to IS NULL OR m.effective_to>clock_timestamp())
				  AND m.cancelled_at IS NULL
			)
		FROM public.relay_node_assets WHERE instance_id=$1`, nodeID).
		Scan(&lifecycle, &nodeType, &contract, &endpoint, &inventoryReadAllowed, &monitoring)
	if errors.Is(err, pgx.ErrNoRows) {
		return accountadmin.NodeState{}, accountadmin.ErrNodeNotFound
	}
	if err != nil {
		return accountadmin.NodeState{}, err
	}
	state := accountadmin.NodeState{LifecycleActive: lifecycle == "active", MonitoringEligible: monitoring, InventoryReadAllowed: inventoryReadAllowed}
	if !state.LifecycleActive || !state.MonitoringEligible || !state.InventoryReadAllowed {
		return state, nil
	}
	policy, err := r.policies.CurrentProviderPolicy(ctx, nodeType, contract)
	if err != nil {
		return accountadmin.NodeState{}, err
	}
	providerActive := policy != nil && contains(policy.ActiveProviders, provider) && !contains(policy.OutOfScopeProviders, provider)
	state.ProviderPolicyActive = providerActive
	if !providerActive {
		return state, nil
	}
	secret, err := r.assetSecrets.ResolveAssetCredential(ctx, assetcredential.NodeCredential, nodeID)
	if err != nil {
		return accountadmin.NodeState{}, err
	}
	defer secret.Destroy()
	var adapter *controlcliproxy.NativeAdapter
	err = secret.Use(func(key string) error {
		adapter, err = controlcliproxy.NewNativeAdapter(endpoint, r.management, key)
		return err
	})
	if err != nil {
		return accountadmin.NodeState{}, err
	}
	state.Adapter = adapter
	return state, nil
}
