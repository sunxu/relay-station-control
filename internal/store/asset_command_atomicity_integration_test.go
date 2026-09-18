package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	assetstore "github.com/sunxu/relay-station-control/internal/store"
)

func TestGatewayCredentialSetRollsBackOnAuditFaultPG18(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	owner, _, repository, adminID, cleanup := newGatewayRuntimeFixtureWithOwner(t, ctx)
	defer cleanup()

	instanceID := uuid.New()
	register := assetstore.GatewayCommand{
		CommandID:          uuid.New(),
		ActorAdminID:       adminID,
		RequestID:          "atomicity-register",
		NewInstanceID:      instanceID,
		DisplayName:        assetstore.StringPatch{Present: true, Value: "Atomic Gateway"},
		ManagementEndpoint: assetstore.StringPatch{Present: true, Value: "http://atomic-gateway.example"},
		Secret:             assetstore.SecretPatch{Operation: assetstore.SecretSet, Value: "gateway-secret-before"},
	}
	if _, err := repository.Register(ctx, register); err != nil {
		t.Fatal(err)
	}

	var beforeRevision int64
	var beforeConfigured bool
	var beforeSealed []byte
	if err := owner.QueryRow(ctx, `SELECT revision,reader_secret_configured,directory_credential_sealed FROM gateway_instances WHERE instance_id=$1`, instanceID).Scan(&beforeRevision, &beforeConfigured, &beforeSealed); err != nil {
		t.Fatal(err)
	}

	if _, err := owner.Exec(ctx, `
CREATE FUNCTION public.test_fail_asset_audit() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'test audit fault' USING ERRCODE = 'P0901';
END
$$`); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `
CREATE TRIGGER test_fail_asset_audit
BEFORE INSERT ON public.audit_logs
FOR EACH ROW EXECUTE FUNCTION public.test_fail_asset_audit()`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = owner.Exec(ctx, `DROP TRIGGER IF EXISTS test_fail_asset_audit ON public.audit_logs`)
		_, _ = owner.Exec(ctx, `DROP FUNCTION IF EXISTS public.test_fail_asset_audit()`)
	}()

	command := assetstore.GatewayCommand{
		CommandID:          uuid.New(),
		ActorAdminID:       adminID,
		RequestID:          "atomicity-audit-fault",
		InstanceID:         instanceID,
		ExpectedRevision:   beforeRevision,
		DisplayName:        assetstore.StringPatch{Present: true, Value: "Atomic Gateway Updated"},
		ManagementEndpoint: assetstore.StringPatch{Present: true, Value: "http://atomic-gateway-updated.example"},
		Secret:             assetstore.SecretPatch{Operation: assetstore.SecretSet, Value: "gateway-secret-after"},
	}
	if _, err := repository.Edit(ctx, command); err == nil {
		t.Fatal("audit fault unexpectedly allowed credential edit")
	}

	var afterRevision int64
	var afterConfigured bool
	var afterSealed []byte
	if err := owner.QueryRow(ctx, `SELECT revision,reader_secret_configured,directory_credential_sealed FROM gateway_instances WHERE instance_id=$1`, instanceID).Scan(&afterRevision, &afterConfigured, &afterSealed); err != nil {
		t.Fatal(err)
	}
	if afterRevision != beforeRevision || afterConfigured != beforeConfigured || string(afterSealed) != string(beforeSealed) {
		t.Fatalf("asset mutation survived rollback: revision %d/%d configured %t/%t sealed_changed=%t", beforeRevision, afterRevision, beforeConfigured, afterConfigured, string(beforeSealed) != string(afterSealed))
	}

	var registryCount, receiptCount, auditCount int
	if err := owner.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM admin_command_registry WHERE command_id=$1),
 (SELECT count(*) FROM asset_admin_command_receipts WHERE command_id=$1),
 (SELECT count(*) FROM audit_logs WHERE request_id=$2)`, command.CommandID, command.RequestID).Scan(&registryCount, &receiptCount, &auditCount); err != nil {
		t.Fatal(err)
	}
	if registryCount != 0 || receiptCount != 0 || auditCount != 0 {
		t.Fatalf("failed command left durable evidence: registry=%d receipt=%d audit=%d", registryCount, receiptCount, auditCount)
	}
}

func TestNodeK2UnavailableRepositoryMatrixPG18(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	databaseURL, owner, cleanup := newGatewayLifecycleMigrationDatabase(t, ctx)
	defer cleanup()
	if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "51"); err != nil {
		t.Fatal(err)
	}
	adminID := uuid.New()
	if _, err := owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at) VALUES($1,'node-k2-matrix','Node K2 Matrix','enabled',clock_timestamp())`, adminID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO node_drivers(node_type,driver_contract_version,display_name) VALUES('cliproxyapi','v1','CLIProxyAPI')`); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO driver_capabilities(node_type,driver_contract_version,capability) VALUES('cliproxyapi','v1','management_health_read')`); err != nil {
		t.Fatal(err)
	}
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
	available, err := assetstore.NewNodeLifecycleRepositoryWithSealer(pool, []byte("01234567890123456789012345678901"), newAvailableTestSealer())
	if err != nil {
		t.Fatal(err)
	}
	unavailable, err := assetstore.NewNodeLifecycleRepositoryWithSealer(pool, []byte("01234567890123456789012345678901"), newUnavailableTestSealer())
	if err != nil {
		t.Fatal(err)
	}
	register := func(commandID, instanceID uuid.UUID, requestID string) {
		t.Helper()
		if _, err := available.Register(ctx, assetstore.NodeCommand{CommandID: commandID, ActorAdminID: adminID, RequestID: requestID, NewInstanceID: instanceID, DisplayName: assetstore.StringPatch{Present: true, Value: "K2 Matrix Node"}, ManagementEndpoint: assetstore.StringPatch{Present: true, Value: "http://k2-matrix.example"}, NodeType: "cliproxyapi", DriverContractVersion: "v1", Capabilities: []string{"management_health_read"}, Secret: assetstore.SecretPatch{Operation: assetstore.SecretSet, Value: "node-k2-matrix-secret"}}); err != nil {
			t.Fatal(err)
		}
	}

	first := uuid.New()
	register(uuid.New(), first, "node-k2-register")
	keep := assetstore.NodeCommand{CommandID: uuid.New(), ActorAdminID: adminID, RequestID: "node-k2-keep", InstanceID: first, ExpectedRevision: 1, DisplayName: assetstore.StringPatch{Present: true, Value: "K2 Matrix Node Kept"}, Secret: assetstore.SecretPatch{Operation: assetstore.SecretAbsent}}
	if _, err := unavailable.Edit(ctx, keep); err != nil {
		t.Fatalf("Keep without K2: %v", err)
	}
	clear := assetstore.NodeCommand{CommandID: uuid.New(), ActorAdminID: adminID, RequestID: "node-k2-clear", InstanceID: first, ExpectedRevision: 2, Secret: assetstore.SecretPatch{Operation: assetstore.SecretClear}}
	if _, err := unavailable.Edit(ctx, clear); err != nil {
		t.Fatalf("Clear without K2: %v", err)
	}
	retire := assetstore.NodeCommand{CommandID: uuid.New(), ActorAdminID: adminID, RequestID: "node-k2-retire", InstanceID: first, ExpectedRevision: 3, Secret: assetstore.SecretPatch{Operation: assetstore.SecretAbsent}}
	retired, err := unavailable.Retire(ctx, retire)
	if err != nil || retired.HTTPStatus != 200 {
		t.Fatalf("Retire without K2: result=%#v err=%v", retired, err)
	}
	if replay, replayErr := unavailable.Retire(ctx, retire); replayErr != nil || !replay.Replayed {
		t.Fatalf("terminal replay without K2: result=%#v err=%v", replay, replayErr)
	}

	second := uuid.New()
	register(uuid.New(), second, "node-k2-replace-source")
	replacement := uuid.New()
	replace := assetstore.NodeCommand{CommandID: uuid.New(), ActorAdminID: adminID, RequestID: "node-k2-replace-absent", InstanceID: second, ExpectedRevision: 1, NewInstanceID: replacement, DisplayName: assetstore.StringPatch{Present: true, Value: "K2 Matrix Replacement"}, ManagementEndpoint: assetstore.StringPatch{Present: true, Value: "http://k2-matrix-replacement.example"}, NodeType: "cliproxyapi", DriverContractVersion: "v1", Capabilities: []string{"management_health_read"}, Secret: assetstore.SecretPatch{Operation: assetstore.SecretAbsent}}
	if result, replaceErr := unavailable.Replace(ctx, replace); replaceErr != nil || result.HTTPStatus != 200 {
		t.Fatalf("unconfigured Replace without K2: result=%#v err=%v", result, replaceErr)
	}

	set := assetstore.NodeCommand{CommandID: uuid.New(), ActorAdminID: adminID, RequestID: "node-k2-set-fails", InstanceID: replacement, ExpectedRevision: 1, DisplayName: assetstore.StringPatch{Present: true, Value: "must not mutate"}, Secret: assetstore.SecretPatch{Operation: assetstore.SecretSet, Value: "node-k2-new-secret"}}
	if _, err := unavailable.Edit(ctx, set); !errors.Is(err, assetstore.ErrInvalidNodeSecret) {
		t.Fatalf("Set without K2 error=%v", err)
	}
	replaceSet := assetstore.NodeCommand{CommandID: uuid.New(), ActorAdminID: adminID, RequestID: "node-k2-replace-set-fails", InstanceID: replacement, ExpectedRevision: 1, NewInstanceID: uuid.New(), DisplayName: assetstore.StringPatch{Present: true, Value: "must not replace"}, ManagementEndpoint: assetstore.StringPatch{Present: true, Value: "http://must-not-replace.example"}, NodeType: "cliproxyapi", DriverContractVersion: "v1", Capabilities: []string{"management_health_read"}, Secret: assetstore.SecretPatch{Operation: assetstore.SecretSet, Value: "node-k2-replacement-secret"}}
	if _, err := unavailable.Replace(ctx, replaceSet); !errors.Is(err, assetstore.ErrInvalidNodeSecret) {
		t.Fatalf("credential-bearing Replace without K2 error=%v", err)
	}
	var revision int64
	if err := owner.QueryRow(ctx, `SELECT revision FROM relay_node_assets WHERE instance_id=$1`, replacement).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if revision != 1 {
		t.Fatalf("Set without K2 changed revision=%d", revision)
	}
}

func TestGatewayK2UnavailableRepositoryMatrixPG18(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	owner, pool, available, adminID, cleanup := newGatewayRuntimeFixtureWithOwner(t, ctx)
	defer cleanup()
	unavailable, err := assetstore.NewGatewayLifecycleRepositoryWithSealer(pool, []byte("01234567890123456789012345678901"), newUnavailableTestSealer())
	if err != nil {
		t.Fatal(err)
	}
	register := func(instanceID uuid.UUID, requestID string) {
		t.Helper()
		if _, err := available.Register(ctx, assetstore.GatewayCommand{CommandID: uuid.New(), ActorAdminID: adminID, RequestID: requestID, NewInstanceID: instanceID, DisplayName: assetstore.StringPatch{Present: true, Value: "Gateway K2 Matrix"}, ManagementEndpoint: assetstore.StringPatch{Present: true, Value: "http://gateway-k2-matrix.example"}, Secret: assetstore.SecretPatch{Operation: assetstore.SecretSet, Value: "gateway-k2-matrix-secret"}}); err != nil {
			t.Fatal(err)
		}
	}

	first := uuid.New()
	register(first, "gateway-k2-register")
	if _, err := unavailable.Edit(ctx, assetstore.GatewayCommand{CommandID: uuid.New(), ActorAdminID: adminID, RequestID: "gateway-k2-keep", InstanceID: first, ExpectedRevision: 1, DisplayName: assetstore.StringPatch{Present: true, Value: "Gateway K2 Kept"}, Secret: assetstore.SecretPatch{Operation: assetstore.SecretAbsent}}); err != nil {
		t.Fatalf("Keep without K2: %v", err)
	}
	if _, err := unavailable.Edit(ctx, assetstore.GatewayCommand{CommandID: uuid.New(), ActorAdminID: adminID, RequestID: "gateway-k2-clear", InstanceID: first, ExpectedRevision: 2, Secret: assetstore.SecretPatch{Operation: assetstore.SecretClear}}); err != nil {
		t.Fatalf("Clear without K2: %v", err)
	}
	retire := assetstore.GatewayCommand{CommandID: uuid.New(), ActorAdminID: adminID, RequestID: "gateway-k2-retire", InstanceID: first, ExpectedRevision: 3, Secret: assetstore.SecretPatch{Operation: assetstore.SecretAbsent}}
	if result, err := unavailable.Retire(ctx, retire); err != nil || result.HTTPStatus != 200 {
		t.Fatalf("Retire without K2: result=%#v err=%v", result, err)
	}
	if replay, replayErr := unavailable.Retire(ctx, retire); replayErr != nil || !replay.Replayed {
		t.Fatalf("terminal replay without K2: result=%#v err=%v", replay, replayErr)
	}

	second, replacement := uuid.New(), uuid.New()
	register(second, "gateway-k2-replace-source")
	if result, replaceErr := unavailable.Replace(ctx, assetstore.GatewayCommand{CommandID: uuid.New(), ActorAdminID: adminID, RequestID: "gateway-k2-replace-absent", InstanceID: second, ExpectedRevision: 1, NewInstanceID: replacement, DisplayName: assetstore.StringPatch{Present: true, Value: "Gateway K2 Replacement"}, ManagementEndpoint: assetstore.StringPatch{Present: true, Value: "http://gateway-k2-replacement.example"}, Secret: assetstore.SecretPatch{Operation: assetstore.SecretAbsent}}); replaceErr != nil || result.HTTPStatus != 200 {
		t.Fatalf("unconfigured Replace without K2: result=%#v err=%v", result, replaceErr)
	}
	set := assetstore.GatewayCommand{CommandID: uuid.New(), ActorAdminID: adminID, RequestID: "gateway-k2-set-fails", InstanceID: replacement, ExpectedRevision: 1, DisplayName: assetstore.StringPatch{Present: true, Value: "must not mutate"}, Secret: assetstore.SecretPatch{Operation: assetstore.SecretSet, Value: "gateway-k2-new-secret"}}
	if _, err := unavailable.Edit(ctx, set); !errors.Is(err, assetstore.ErrInvalidGatewaySecret) {
		t.Fatalf("Set without K2 error=%v", err)
	}
	replaceSet := assetstore.GatewayCommand{CommandID: uuid.New(), ActorAdminID: adminID, RequestID: "gateway-k2-replace-set-fails", InstanceID: replacement, ExpectedRevision: 1, NewInstanceID: uuid.New(), DisplayName: assetstore.StringPatch{Present: true, Value: "must not replace"}, ManagementEndpoint: assetstore.StringPatch{Present: true, Value: "http://must-not-replace.example"}, Secret: assetstore.SecretPatch{Operation: assetstore.SecretSet, Value: "gateway-k2-replacement-secret"}}
	if _, err := unavailable.Replace(ctx, replaceSet); !errors.Is(err, assetstore.ErrInvalidGatewaySecret) {
		t.Fatalf("credential-bearing Replace without K2 error=%v", err)
	}
	var revision int64
	if err := owner.QueryRow(ctx, `SELECT revision FROM gateway_instances WHERE instance_id=$1`, replacement).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if revision != 1 {
		t.Fatalf("Set without K2 changed revision=%d", revision)
	}
}
