package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	accountadmin "github.com/sunxu/relay-station-control/internal/accountadmin"
	"github.com/sunxu/relay-station-control/internal/assetcredential"
	"github.com/sunxu/relay-station-control/internal/drivers"
	assetstore "github.com/sunxu/relay-station-control/internal/store"
)

type testAccountAssetResolver struct{}

func (testAccountAssetResolver) ResolveAssetCredential(context.Context, assetcredential.CredentialKind, uuid.UUID) (*drivers.Secret, error) {
	return drivers.NewSecretFromBytes([]byte("synthetic-resolver-management-key")), nil
}

func TestProductionAccountNodeResolverUsesMigratedCapabilitySchema(t *testing.T) {
	owner, runtime := isolatedCrossNodeDuplicateOwnershipDatabase(t)
	ctx := context.Background()
	nodeType, contract := "cliproxyapi", "cliproxyapi.auth-files.v1"
	policyID := uuid.New()
	adminID := uuid.New()
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO environments(environment_id,name,environment_type) VALUES('development','Account resolver wiring','production')`, nil},
		{`INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at) VALUES($1,'resolver-admin','Resolver Admin','enabled',clock_timestamp())`, []any{adminID}},
		{`INSERT INTO node_drivers(node_type,driver_contract_version,display_name) VALUES($1,$2,'Account resolver driver')`, []any{nodeType, contract}},
		{`INSERT INTO driver_capabilities(node_type,driver_contract_version,capability) VALUES ($1,$2,'management_health_read'),($1,$2,'management_account_inventory_read')`, []any{nodeType, contract}},
		{`INSERT INTO provider_inventory_policy_versions(policy_version_id,node_type,driver_contract_version,active_providers,out_of_scope_providers,created_by) VALUES($1,$2,$3,ARRAY['antigravity']::text[],ARRAY[]::text[],'resolver-test')`, []any{policyID, nodeType, contract}},
		{`INSERT INTO provider_inventory_policy_bindings(node_type,driver_contract_version,policy_version_id,bound_by,bound_at) VALUES($1,$2,$3,'resolver-test',clock_timestamp())`, []any{nodeType, contract, policyID}},
		{`INSERT INTO provider_inventory_policy_activations(node_type,driver_contract_version,policy_version_id,effective_from,activated_by,created_at) VALUES($1,$2,$3,statement_timestamp(),'resolver-test',statement_timestamp())`, []any{nodeType, contract, policyID}},
	}
	for _, statement := range statements {
		if _, err := owner.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}

	type fixture struct {
		name           string
		capabilities   []string
		monitoring     bool
		retired        bool
		wantInventory  bool
		wantMonitoring bool
		wantLifecycle  bool
	}
	fixtures := []fixture{
		{name: "required capability", capabilities: []string{"management_account_inventory_read"}, monitoring: true, wantInventory: true, wantMonitoring: true, wantLifecycle: true},
		{name: "required capability missing", capabilities: nil, monitoring: true, wantInventory: false, wantMonitoring: true, wantLifecycle: true},
		{name: "multiple capabilities", capabilities: []string{"management_health_read", "management_account_inventory_read"}, monitoring: true, wantInventory: true, wantMonitoring: true, wantLifecycle: true},
		{name: "retired and monitoring ineligible", capabilities: []string{"management_account_inventory_read"}, retired: true, wantInventory: true, wantMonitoring: false, wantLifecycle: false},
	}

	secretDirectory := t.TempDir()
	management, err := (drivers.ManagementConfig{}).Validate()
	if err != nil {
		t.Fatal(err)
	}
	policies, err := assetstore.NewAssetRepository(runtime)
	if err != nil {
		t.Fatal(err)
	}
	assetResolver := testAccountAssetResolver{}
	resolver := productionAccountNodeResolverWithPolicy{pool: runtime, management: management, assetSecrets: assetResolver, policies: policies}
	intentKeyPath := filepath.Join(secretDirectory, "account-operation-intent-key")
	if err := os.WriteFile(intentKeyPath, []byte("01234567890123456789012345678901"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONTROL_ACCOUNT_OPERATION_INTENT_KEY_FILE", intentKeyPath)
	service, _, err := newAccountOperationService(runtime, management, assetResolver, policies)
	if err != nil {
		t.Fatal(err)
	}
	intentCommand := accountadmin.Command{CommandID: uuid.New(), ActorAdminID: adminID, NodeInstanceID: uuid.New(), AccountKey: "antigravity:intent-smoke@example.invalid", Kind: assetstore.AccountUploadNew, Credential: []byte(`{"type":"antigravity","email":"intent-smoke@example.invalid"}`)}
	if _, err := service.CanonicalIntentForCommand(intentCommand); err != nil {
		t.Fatalf("production intent-key path: %v", err)
	}

	for _, test := range fixtures {
		t.Run(test.name, func(t *testing.T) {
			nodeID := uuid.New()
			if test.retired {
				if _, err := owner.Exec(ctx, `INSERT INTO relay_node_assets(
					instance_id,display_name,node_type,driver_contract_version,management_endpoint,reader_secret_ref,
					lifecycle_status,retired_at,retired_by,retire_reason,created_at,updated_at)
					VALUES($1,$2,$3,$4,'http://127.0.0.1:1','file://resolver/node-management',
					'retired',clock_timestamp(),$5,'administrator_retire',clock_timestamp()-interval '1 second',clock_timestamp())`,
					nodeID, test.name, nodeType, contract, adminID); err != nil {
					t.Fatal(err)
				}
			} else if _, err := owner.Exec(ctx, `INSERT INTO relay_node_assets(instance_id,display_name,node_type,driver_contract_version,management_endpoint,reader_secret_ref)
				VALUES($1,$2,$3,$4,'http://127.0.0.1:1','file://resolver/node-management')`, nodeID, test.name, nodeType, contract); err != nil {
				t.Fatal(err)
			}
			for _, capability := range test.capabilities {
				if _, err := owner.Exec(ctx, `INSERT INTO node_capabilities(instance_id,node_type,driver_contract_version,capability) VALUES($1,$2,$3,$4)`, nodeID, nodeType, contract, capability); err != nil {
					t.Fatal(err)
				}
			}
			if test.monitoring {
				if _, err := owner.Exec(ctx, `INSERT INTO relay_node_inventory_monitoring_activations(instance_id,effective_from,reason,actor,created_at)
					VALUES($1,statement_timestamp(), 'deployment_enable','resolver-test',statement_timestamp())`, nodeID); err != nil {
					t.Fatal(err)
				}
			}

			state, err := resolver.Resolve(ctx, nodeID, "antigravity")
			if err != nil {
				t.Fatalf("resolve against migration-47 schema: %v", err)
			}
			if state.InventoryReadAllowed != test.wantInventory || state.MonitoringEligible != test.wantMonitoring || state.LifecycleActive != test.wantLifecycle {
				t.Fatalf("state = lifecycle:%v monitoring:%v inventory:%v", state.LifecycleActive, state.MonitoringEligible, state.InventoryReadAllowed)
			}
			fullyEligible := test.wantLifecycle && test.wantMonitoring && test.wantInventory
			if state.ProviderPolicyActive != fullyEligible || (state.Adapter != nil) != fullyEligible {
				t.Fatalf("provider/adapter = %v/%v", state.ProviderPolicyActive, state.Adapter)
			}
			if test.name == "required capability missing" {
				command := accountadmin.Command{CommandID: uuid.New(), ActorAdminID: adminID, NodeInstanceID: nodeID, AccountKey: "antigravity:missing-capability@example.invalid", Kind: assetstore.AccountDisable, RequestID: "resolver-missing-capability"}
				command.CanonicalIntent, err = service.CanonicalIntentForCommand(command)
				if err != nil {
					t.Fatal(err)
				}
				operation, err := service.Execute(ctx, command)
				if err != nil {
					t.Fatal(err)
				}
				if operation.ExecutionState != assetstore.AccountFailed || operation.RemoteResultCode == nil || *operation.RemoteResultCode != "node_management_unavailable" {
					t.Fatalf("missing capability operation = %#v", operation)
				}
			}
		})
	}
}

func TestAccountNodeResolverMissingCapabilityMapsToFrozenFailure(t *testing.T) {
	// The orchestration owns the stable outcome for a successfully resolved Node
	// without Inventory-read capability; keep this assertion beside the real-PG
	// resolver proof so the production wiring cannot silently treat absence as success.
	failure, err := assetstore.NewAccountFailure("node_management_unavailable", assetstore.AccountPreDispatchPostAccept)
	if err != nil || failure.Code != "node_management_unavailable" {
		t.Fatalf("missing capability failure = %#v, %v", failure, err)
	}
}
