package store_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	assetstore "github.com/sunxu/relay-station-control/internal/store"
)

func TestAdminCommandRegistryMigrationBackfillAndPrivilegesPG18(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	databaseURL, owner, cleanup := newGatewayLifecycleMigrationDatabase(t, ctx)
	defer cleanup()
	if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "36"); err != nil {
		t.Fatal(err)
	}
	admin := uuid.New()
	if _, err := owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at) VALUES($1,$2,'Registry Admin','enabled',clock_timestamp())`, admin, "registry-"+admin.String()[:12]); err != nil {
		t.Fatal(err)
	}
	historical := []struct {
		id   uuid.UUID
		kind string
	}{
		{uuid.New(), "gateway.retire"},
		{uuid.New(), "node.register"},
		{uuid.New(), "node.monitoring_enable"},
	}
	historicalGatewayID := uuid.New()
	historicalGatewayBody := []byte(`{"result":"retired","historical":true}`)
	for _, item := range historical {
		intent := []byte(item.kind)
		body := []byte(`{"result":"historical"}`)
		if item.kind == "gateway.retire" {
			intent, _ = json.Marshal([]any{1, item.kind, historicalGatewayID.String(), "1", "administrator_retire"})
			body = historicalGatewayBody
		}
		hash := sha256.Sum256(intent)
		if _, err := owner.Exec(ctx, `INSERT INTO asset_admin_command_receipts(command_id,command_kind,intent_encoding_version,canonical_intent_hash,sanitized_result,response_status,actor_admin_id,secret_fingerprint_key_version) VALUES($1,$2,1,$3,$4,200,$5,NULL)`, item.id, item.kind, hash[:], body, admin); err != nil {
			t.Fatalf("seed historical %s: %v", item.kind, err)
		}
	}
	owner.Close(ctx)
	if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "37"); err != nil {
		t.Fatal(err)
	}
	owner, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close(ctx)
	var version, floor, receipts, reservations int
	if err = owner.QueryRow(ctx, `SELECT
		(SELECT max(version_id) FROM goose_db_version WHERE is_applied),
		(SELECT phase6_evidence_floor FROM control_runtime_compatibility WHERE singleton_id=1),
		(SELECT count(*) FROM asset_admin_command_receipts),
		(SELECT count(*) FROM admin_command_registry)`).Scan(&version, &floor, &receipts, &reservations); err != nil {
		t.Fatal(err)
	}
	if version != 37 || floor != 3 || receipts != len(historical) || reservations != receipts {
		t.Fatalf("version/floor/receipt/registry=%d/%d/%d/%d", version, floor, receipts, reservations)
	}
	for _, item := range historical {
		var domain, kind string
		var reservedAt, committedAt time.Time
		if err = owner.QueryRow(ctx, `SELECT registry.command_domain,registry.command_kind,registry.reserved_at,receipt.committed_at FROM admin_command_registry registry JOIN asset_admin_command_receipts receipt USING(command_id) WHERE registry.command_id=$1`, item.id).Scan(&domain, &kind, &reservedAt, &committedAt); err != nil {
			t.Fatal(err)
		}
		if domain != "asset_admin" || kind != item.kind || !reservedAt.Equal(committedAt) {
			t.Fatalf("historical mapping for %s = %s/%s/%s/%s", item.kind, domain, kind, reservedAt, committedAt)
		}
	}
	runtime := runtimePoolForRegistry(t, ctx, databaseURL)
	repository, err := assetstore.NewGatewayLifecycleRepository(runtime, nil)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := repository.Retire(ctx, assetstore.GatewayCommand{
		CommandID:        historical[0].id,
		ActorAdminID:     admin,
		InstanceID:       historicalGatewayID,
		ExpectedRevision: 1,
		Secret:           assetstore.SecretPatch{Operation: assetstore.SecretAbsent},
	})
	runtime.Close()
	if err != nil || !replayed.Replayed || replayed.HTTPStatus != 200 || !jsonEqual(replayed.Body, historicalGatewayBody) {
		t.Fatalf("historical post-migration replay=%#v err=%v", replayed, err)
	}

	if _, err = owner.Exec(ctx, `UPDATE admin_command_registry SET command_kind='gateway.edit' WHERE command_id=$1`, historical[0].id); err == nil {
		t.Fatal("registry UPDATE unexpectedly succeeded")
	}
	if _, err = owner.Exec(ctx, `DELETE FROM admin_command_registry WHERE command_id=$1`, historical[0].id); err == nil {
		t.Fatal("registry DELETE unexpectedly succeeded")
	}
	if _, err = owner.Exec(ctx, `TRUNCATE admin_command_registry`); err == nil {
		t.Fatal("registry TRUNCATE unexpectedly succeeded")
	}
	hash := sha256.Sum256([]byte("missing reservation"))
	if _, err = owner.Exec(ctx, `INSERT INTO asset_admin_command_receipts(command_id,command_kind,intent_encoding_version,canonical_intent_hash,sanitized_result,response_status,actor_admin_id) VALUES($1,'gateway.edit',1,$2,'{"result":"invalid"}',200,$3)`, uuid.New(), hash[:], admin); err == nil {
		t.Fatal("receipt without reservation unexpectedly succeeded")
	}

	runtime = runtimePoolForRegistry(t, ctx, databaseURL)
	defer runtime.Close()
	for _, attempt := range []struct {
		statement string
		arguments []any
	}{
		{`INSERT INTO admin_command_registry(command_id,actor_admin_id,command_domain,command_kind,intent_encoding_version,canonical_intent_hash,reserved_at) VALUES(gen_random_uuid(),$1,'asset_admin','gateway.edit',1,decode(repeat('00',32),'hex'),clock_timestamp())`, []any{admin}},
		{`UPDATE admin_command_registry SET command_kind='gateway.edit' WHERE command_id=$1`, []any{historical[0].id}},
		{`DELETE FROM admin_command_registry WHERE command_id=$1`, []any{historical[0].id}},
		{`TRUNCATE admin_command_registry`, nil},
	} {
		if _, mutationErr := runtime.Exec(ctx, attempt.statement, attempt.arguments...); mutationErr == nil {
			t.Fatalf("runtime registry DML unexpectedly succeeded: %s", attempt.statement)
		}
	}
	if _, err = runtime.Exec(ctx, `INSERT INTO asset_admin_command_receipts(command_id,command_kind,intent_encoding_version,canonical_intent_hash,sanitized_result,response_status,actor_admin_id) VALUES($1,'gateway.edit',1,$2,'{"result":"invalid"}',200,$3)`, uuid.New(), hash[:], admin); err == nil {
		t.Fatal("runtime direct receipt INSERT unexpectedly succeeded")
	}
	oldWriterGatewayID := uuid.New()
	oldWriterTx, err := runtime.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = oldWriterTx.Exec(ctx, `INSERT INTO gateway_instances(singleton_id,instance_id,display_name,management_endpoint,reader_secret_ref,lifecycle_status,revision) VALUES(1,$1,'Old Writer Gateway','http://old-writer.example',NULL,'active',1)`, oldWriterGatewayID); err != nil {
		t.Fatal(err)
	}
	if _, err = oldWriterTx.Exec(ctx, `INSERT INTO asset_admin_command_receipts(command_id,command_kind,intent_encoding_version,canonical_intent_hash,sanitized_result,response_status,actor_admin_id) VALUES($1,'gateway.register',1,$2,'{"result":"registered"}',201,$3)`, uuid.New(), hash[:], admin); err == nil {
		t.Fatal("pre-registry writer unexpectedly inserted a receipt")
	}
	_ = oldWriterTx.Rollback(ctx)
	var oldWriterRows int
	if err = owner.QueryRow(ctx, `SELECT count(*) FROM gateway_instances WHERE instance_id=$1`, oldWriterGatewayID).Scan(&oldWriterRows); err != nil || oldWriterRows != 0 {
		t.Fatalf("failed old writer left domain mutation count=%d err=%v", oldWriterRows, err)
	}
}

func TestAdminCommandRegistryInvalidHistoryFailsAtomicallyPG18(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	databaseURL, owner, cleanup := newGatewayLifecycleMigrationDatabase(t, ctx)
	defer cleanup()
	if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "36"); err != nil {
		t.Fatal(err)
	}
	admin := uuid.New()
	commandID := uuid.New()
	if _, err := owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at) VALUES($1,$2,'Invalid History Admin','enabled',clock_timestamp())`, admin, "invalid-history-"+admin.String()[:8]); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte("invalid-history"))
	if _, err := owner.Exec(ctx, `INSERT INTO asset_admin_command_receipts(command_id,command_kind,intent_encoding_version,canonical_intent_hash,sanitized_result,response_status,actor_admin_id) VALUES($1,'gateway.register',1,$2,'{"result":"historical"}',201,$3)`, commandID, hash[:], admin); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`ALTER TABLE asset_admin_command_receipts DISABLE TRIGGER asset_admin_command_receipts_immutable`,
		`ALTER TABLE asset_admin_command_receipts DROP CONSTRAINT asset_admin_command_receipts_hash_check`,
	} {
		if _, err := owner.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := owner.Exec(ctx, `UPDATE asset_admin_command_receipts SET canonical_intent_hash=decode('00','hex') WHERE command_id=$1`, commandID); err != nil {
		t.Fatal(err)
	}
	owner.Close(ctx)
	if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "37"); err == nil {
		t.Fatal("migration 37 unexpectedly accepted invalid history")
	}
	check, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close(ctx)
	var version int
	var registryExists bool
	if err = check.QueryRow(ctx, `SELECT (SELECT max(version_id) FROM goose_db_version WHERE is_applied),to_regclass('public.admin_command_registry') IS NOT NULL`).Scan(&version, &registryExists); err != nil {
		t.Fatal(err)
	}
	if version != 36 || registryExists {
		t.Fatalf("failed migration left version/table=%d/%v", version, registryExists)
	}
}

func TestAdminCommandRegistryExistingWritersAndGlobalRacesPG18(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	databaseURL, owner, cleanup := newGatewayLifecycleMigrationDatabase(t, ctx)
	defer cleanup()
	if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "51"); err != nil {
		t.Fatal(err)
	}
	adminA, adminB := uuid.New(), uuid.New()
	for i, admin := range []uuid.UUID{adminA, adminB} {
		if _, err := owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at) VALUES($1,$2,$3,'enabled',clock_timestamp())`, admin, "global-command-"+admin.String()[:12], "Global Command Admin "+string(rune('A'+i))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := owner.Exec(ctx, `INSERT INTO node_drivers(node_type,driver_contract_version,display_name) VALUES('cliproxyapi','v1','CLIProxyAPI'); INSERT INTO driver_capabilities(node_type,driver_contract_version,capability) VALUES('cliproxyapi','v1','management_account_inventory_read'),('cliproxyapi','v1','management_health_read')`); err != nil {
		t.Fatal(err)
	}
	owner.Close(ctx)
	runtime := runtimePoolForRegistry(t, ctx, databaseURL)
	defer runtime.Close()
	key := []byte("01234567890123456789012345678901")
	gatewayRepository, err := assetstore.NewGatewayLifecycleRepositoryWithSealer(runtime, key, newAvailableTestSealer())
	if err != nil {
		t.Fatal(err)
	}
	nodeRepository, err := assetstore.NewNodeLifecycleRepositoryWithSealer(runtime, key, newAvailableTestSealer())
	if err != nil {
		t.Fatal(err)
	}
	monitoringRepository, err := assetstore.NewNodeMonitoringRepository(runtime)
	if err != nil {
		t.Fatal(err)
	}

	shared := uuid.New()
	gatewayID, nodeID := uuid.New(), uuid.New()
	gatewayCommand := assetstore.GatewayCommand{CommandID: shared, ActorAdminID: adminA, RequestID: "gateway-race", NewInstanceID: gatewayID, DisplayName: assetstore.StringPatch{Present: true, Value: "Registry Gateway"}, ManagementEndpoint: assetstore.StringPatch{Present: true, Value: "http://registry-gateway.example"}, Secret: assetstore.SecretPatch{Operation: assetstore.SecretSet, Value: "env://gateway/reader"}}
	nodeCommand := assetstore.NodeCommand{CommandID: shared, ActorAdminID: adminA, RequestID: "node-race", NewInstanceID: nodeID, DisplayName: assetstore.StringPatch{Present: true, Value: "Registry Node"}, ManagementEndpoint: assetstore.StringPatch{Present: true, Value: "http://registry-node.example"}, NodeType: "cliproxyapi", DriverContractVersion: "v1", Capabilities: []string{"management_account_inventory_read", "management_health_read"}, Secret: assetstore.SecretPatch{Operation: assetstore.SecretSet, Value: "vault://node/reader"}}
	var gatewayResult assetstore.GatewayCommandResult
	var gatewayErr, nodeErr error
	var nodeResult assetstore.NodeCommandResult
	var wait sync.WaitGroup
	wait.Add(2)
	go func() { defer wait.Done(); gatewayResult, gatewayErr = gatewayRepository.Register(ctx, gatewayCommand) }()
	go func() { defer wait.Done(); nodeResult, nodeErr = nodeRepository.Register(ctx, nodeCommand) }()
	wait.Wait()
	if (gatewayErr == nil) == (nodeErr == nil) {
		t.Fatalf("gateway/node race results gateway=%d/%v node=%d/%v", gatewayResult.HTTPStatus, gatewayErr, nodeResult.HTTPStatus, nodeErr)
	}
	loser := gatewayErr
	if loser == nil {
		loser = nodeErr
	}
	if !errors.Is(loser, assetstore.ErrCommandConflict) {
		t.Fatalf("gateway/node race loser=%v", loser)
	}
	var registryCount, receiptCount int
	if err = runtime.QueryRow(ctx, `SELECT (SELECT count(*) FROM admin_command_registry WHERE command_id=$1),(SELECT count(*) FROM asset_admin_command_receipts WHERE command_id=$1)`, shared).Scan(&registryCount, &receiptCount); err != nil || registryCount != 1 || receiptCount != 1 {
		t.Fatalf("global race evidence registry/receipt=%d/%d err=%v", registryCount, receiptCount, err)
	}

	// Ensure one Gateway and one Node exist for the monitoring-vs-asset race.
	if gatewayErr != nil {
		gatewayCommand.CommandID = uuid.New()
		if gatewayResult, err = gatewayRepository.Register(ctx, gatewayCommand); err != nil {
			t.Fatal(err)
		}
	}
	if nodeErr != nil {
		nodeCommand.CommandID = uuid.New()
		if nodeResult, err = nodeRepository.Register(ctx, nodeCommand); err != nil {
			t.Fatal(err)
		}
	}
	replay, err := gatewayRepository.Register(ctx, gatewayCommand)
	if err != nil || !replay.Replayed || !jsonEqual(replay.Body, gatewayResult.Body) {
		t.Fatalf("gateway restart/replay=%v/%v", replay, err)
	}

	secondShared := uuid.New()
	edit := assetstore.GatewayCommand{CommandID: secondShared, ActorAdminID: adminA, RequestID: "asset-monitoring-race", InstanceID: gatewayID, ExpectedRevision: 1, DisplayName: assetstore.StringPatch{Present: true, Value: "Registry Gateway Updated"}, Secret: assetstore.SecretPatch{Operation: assetstore.SecretAbsent}}
	monitor := assetstore.NodeMonitoringCommand{CommandID: secondShared, ActorAdminID: adminA, RequestID: "monitoring-asset-race", InstanceID: nodeID}
	var editErr, monitorErr error
	wait.Add(2)
	go func() { defer wait.Done(); _, editErr = gatewayRepository.Edit(ctx, edit) }()
	go func() { defer wait.Done(); _, monitorErr = monitoringRepository.Enable(ctx, monitor) }()
	wait.Wait()
	if (editErr == nil) == (monitorErr == nil) {
		t.Fatalf("asset/monitoring race edit=%v monitor=%v", editErr, monitorErr)
	}
	loser = editErr
	if loser == nil {
		loser = monitorErr
	}
	if !errors.Is(loser, assetstore.ErrCommandConflict) {
		t.Fatalf("asset/monitoring race loser=%v", loser)
	}
	rollbackCommandID := uuid.New()
	if _, err = gatewayRepository.Edit(ctx, assetstore.GatewayCommand{CommandID: rollbackCommandID, ActorAdminID: adminA, RequestID: "registry-rollback", InstanceID: gatewayID, ExpectedRevision: 999, DisplayName: assetstore.StringPatch{Present: true, Value: "Must Roll Back"}, Secret: assetstore.SecretPatch{Operation: assetstore.SecretAbsent}}); !errors.Is(err, assetstore.ErrStaleAssetRevision) {
		t.Fatalf("expected domain failure after reservation, got %v", err)
	}
	if err = runtime.QueryRow(ctx, `SELECT count(*) FROM admin_command_registry WHERE command_id=$1`, rollbackCommandID).Scan(&registryCount); err != nil || registryCount != 0 {
		t.Fatalf("failed domain mutation left registry reservation count=%d err=%v", registryCount, err)
	}

	// An account-domain reservation owned by A must hide all later validation
	// from B and must also conflict for A when the domain differs.
	crossDomainID := uuid.New()
	crossHash := sha256.Sum256([]byte("account-domain"))
	if _, err = runtime.Exec(ctx, `SELECT command_id FROM control_reserve_admin_command_v1($1::uuid,$2::uuid,'account_admin'::text,'account.disable'::text,1::smallint,$3::bytea,NULL::smallint)`, crossDomainID, adminA, crossHash[:]); err != nil {
		t.Fatal(err)
	}
	malformed := assetstore.GatewayCommand{CommandID: crossDomainID, ActorAdminID: adminB}
	if _, err = gatewayRepository.Register(ctx, malformed); !errors.Is(err, assetstore.ErrCommandConflict) {
		t.Fatalf("different actor was not rejected before validation: %v", err)
	}
	malformed.ActorAdminID = adminA
	if _, err = gatewayRepository.Register(ctx, malformed); !errors.Is(err, assetstore.ErrCommandConflict) {
		t.Fatalf("cross-domain reuse was not rejected: %v", err)
	}
}

func runtimePoolForRegistry(t *testing.T, ctx context.Context, databaseURL string) *pgxpool.Pool {
	t.Helper()
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
	return pool
}
