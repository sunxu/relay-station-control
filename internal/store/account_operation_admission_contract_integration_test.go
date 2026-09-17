package store_test

import (
	"context"
	"crypto/sha256"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	store "github.com/sunxu/relay-station-control/internal/store"
)

// This is intentionally a RED test for the post-closeout admission gap: the
// current resolver-facing fixture is active, but the durable Node has no
// current monitoring/capability/policy evidence. Admission must fail closed
// instead of trusting that an earlier resolver snapshot was positive.
func TestAccountAdmissionRechecksDurableNodeEligibilityPG18(t *testing.T) {
	ctx := context.Background()
	databaseURL, owner, cleanup := newGatewayLifecycleMigrationDatabase(t, ctx)
	defer cleanup()
	if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "50"); err != nil {
		t.Fatal(err)
	}
	admin, node := uuid.New(), uuid.New()
	if _, err := owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at) VALUES($1,$2,'Admission Admin','enabled',clock_timestamp())`, admin, "admission-"+admin.String()[:12]); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO node_drivers(node_type,driver_contract_version,display_name) VALUES('cliproxyapi','v1','CLIProxyAPI') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO driver_capabilities(node_type,driver_contract_version,capability) VALUES('cliproxyapi','v1','management_account_inventory_read') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO relay_node_assets(instance_id,display_name,node_type,driver_contract_version,management_endpoint) VALUES($1,'Admission Node','cliproxyapi','v1','http://node.example/')`, node); err != nil {
		t.Fatal(err)
	}
	owner.Close(ctx)

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE relay_control_runtime`)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository, err := store.NewAccountOperationRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	accountKey := "antigravity:stale-eligibility@example.invalid"
	hash := sha256.Sum256([]byte(accountKey))
	commandID := uuid.New()
	if _, err := repository.Accept(ctx, store.AccountOperationAcceptance{
		CommandID: commandID, ActorAdminID: admin, OperationKind: store.AccountDisable,
		NodeInstanceID: node, AccountKey: accountKey, CanonicalIntentHash: hash[:],
	}); err != nil {
		t.Fatal(err)
	}
	admitted, operation, err := repository.AdmitAccountDispatch(ctx, commandID, node, accountKey, "stale-eligibility")
	if err != nil {
		t.Fatal(err)
	}
	if admitted || operation.ExecutionState != store.AccountFailed || operation.RemoteResultCode == nil || *operation.RemoteResultCode != "node_monitoring_ineligible" {
		t.Fatalf("admission did not fail closed on missing durable eligibility: admitted=%t operation=%#v", admitted, operation)
	}
}

func TestAccountAdmissionFencingMigrationUpgradesFromPreviousHeadPG18(t *testing.T) {
	database := newIsolatedJobDatabase(t, "up-to", "49")
	ctx := context.Background()
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "up-by-one"); err != nil {
		t.Fatal(err)
	}
	var version int32
	if err := database.owner.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 50 {
		t.Fatalf("migration version=%d, want 50", version)
	}
}

func TestAccountNoopAdmissionConvergesConcurrentPreparedRetriesPG18(t *testing.T) {
	ctx := context.Background()
	databaseURL, owner, cleanup := newGatewayLifecycleMigrationDatabase(t, ctx)
	defer cleanup()
	if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "50"); err != nil {
		t.Fatal(err)
	}
	admin, node, policyID := uuid.New(), uuid.New(), uuid.New()
	if _, err := owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at) VALUES($1,$2,'Noop Admin','enabled',clock_timestamp())`, admin, "noop-"+admin.String()[:12]); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO node_drivers(node_type,driver_contract_version,display_name) VALUES('cliproxyapi','v1','CLIProxyAPI') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO driver_capabilities(node_type,driver_contract_version,capability) VALUES('cliproxyapi','v1','management_account_inventory_read') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO relay_node_assets(instance_id,display_name,node_type,driver_contract_version,management_endpoint) VALUES($1,'Noop Node','cliproxyapi','v1','http://node.example/')`, node); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO node_capabilities(instance_id,node_type,driver_contract_version,capability) VALUES($1,'cliproxyapi','v1','management_account_inventory_read')`, node); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO provider_inventory_policy_versions(policy_version_id,node_type,driver_contract_version,active_providers,out_of_scope_providers,created_by) VALUES($1,'cliproxyapi','v1',ARRAY['antigravity'],ARRAY[]::text[],'noop-test')`, policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO provider_inventory_policy_activations(node_type,driver_contract_version,policy_version_id,effective_from,activated_by,created_at) SELECT 'cliproxyapi','v1',$1,t,'noop-test',t FROM (SELECT clock_timestamp()-interval '1 minute' AS t) AS boundary`, policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO relay_node_inventory_monitoring_activations(instance_id,effective_from,reason,actor,created_at) SELECT $1,t,'deployment_enable','noop-test',t FROM (SELECT clock_timestamp()-interval '1 minute' AS t) AS boundary`, node); err != nil {
		t.Fatal(err)
	}
	owner.Close(ctx)
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE relay_control_runtime`)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository, err := store.NewAccountOperationRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	accountKey := "antigravity:noop-convergence@example.invalid"
	hash := sha256.Sum256([]byte(accountKey))
	commandID := uuid.New()
	if _, err := repository.Accept(ctx, store.AccountOperationAcceptance{CommandID: commandID, ActorAdminID: admin, OperationKind: store.AccountDisable, NodeInstanceID: node, AccountKey: accountKey, CanonicalIntentHash: hash[:]}); err != nil {
		t.Fatal(err)
	}
	type result struct {
		admitted bool
		op       store.AccountAdminOperation
		err      error
	}
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			admitted, op, err := repository.AdmitAccountNoop(ctx, commandID, node, accountKey, "noop-convergence")
			results <- result{admitted: admitted, op: op, err: err}
		}()
	}
	for i := 0; i < 2; i++ {
		got := <-results
		if got.err != nil || !got.admitted || got.op.ExecutionState != store.AccountRemoteNoop {
			t.Fatalf("concurrent noop result=%#v", got)
		}
	}
	var operations, receipts, audits int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM account_admin_operations WHERE command_id=$1), (SELECT count(*) FROM account_admin_command_receipts WHERE command_id=$1), (SELECT count(*) FROM audit_logs WHERE action='account.operation_noop' AND details->>'command_id'=$1::text)`, commandID).Scan(&operations, &receipts, &audits); err != nil {
		t.Fatal(err)
	}
	if operations != 1 || receipts != 1 || audits != 1 {
		t.Fatalf("concurrent noop durable totals=%d/%d/%d", operations, receipts, audits)
	}
}
