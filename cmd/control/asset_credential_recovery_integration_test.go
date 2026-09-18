package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sunxu/relay-station-control/internal/assetcredential"
	controlnodes "github.com/sunxu/relay-station-control/internal/drivers"
	"github.com/sunxu/relay-station-control/internal/drivers/gatewaydirectory"
	assetstore "github.com/sunxu/relay-station-control/internal/store"
)

func TestStage0CredentialRecoveryK2MatrixPG(t *testing.T) {
	owner, runtime := isolatedCrossNodeDuplicateOwnershipDatabase(t)
	ctx := context.Background()
	directory := t.TempDir()
	keyA := bytes.Repeat([]byte{0x41}, assetcredential.KeySize)
	keyB := bytes.Repeat([]byte{0x42}, assetcredential.KeySize)
	pathA := filepath.Join(directory, "k2-a")
	pathB := filepath.Join(directory, "k2-b")
	if err := os.WriteFile(pathA, keyA, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pathB, keyB, 0o600); err != nil {
		t.Fatal(err)
	}
	commitment := assetcredential.IdentityCommitment(keyA)
	var status string
	if err := owner.QueryRow(ctx, `SELECT public.control_initialize_asset_credential_key_v1($1)`, commitment[:]).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "initialized" {
		t.Fatalf("initial commitment status=%q", status)
	}

	withKey := func(path string) assetstoreCredentialSealer {
		t.Setenv("CONTROL_ASSET_CREDENTIAL_KEY_FILE", path)
		sealer, err := loadStage0AssetCredentialSealer(ctx, runtime, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)))
		if err != nil {
			t.Fatal(err)
		}
		return sealer
	}
	if !withKey(pathA).Available() {
		t.Fatal("matching K2 was unavailable")
	}
	if withKey(pathB).Available() {
		t.Fatal("wrong K2 was accepted")
	}
	if withKey(filepath.Join(directory, "missing")).Available() {
		t.Fatal("missing K2 was accepted")
	}
	var stored []byte
	if err := owner.QueryRow(ctx, `SELECT k2_identity_commitment FROM control_asset_credential_key_identity WHERE singleton_id=1`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, commitment[:]) {
		t.Fatal("K2 mismatch changed the stored commitment")
	}
}

func TestStage0CredentialBackupRestoreSameK2PG(t *testing.T) {
	owner, runtime := isolatedCrossNodeDuplicateOwnershipDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	directory := t.TempDir()
	keyA := bytes.Repeat([]byte{0x51}, assetcredential.KeySize)
	keyB := bytes.Repeat([]byte{0x52}, assetcredential.KeySize)
	pathA := filepath.Join(directory, "k2-source")
	pathRestored := filepath.Join(directory, "migrated", "k2-restored")
	pathWrong := filepath.Join(directory, "k2-wrong")
	if err := os.MkdirAll(filepath.Dir(pathRestored), 0o700); err != nil {
		t.Fatal(err)
	}
	for path, key := range map[string][]byte{pathA: keyA, pathWrong: keyB} {
		if err := os.WriteFile(path, key, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(pathRestored, keyA, 0o600); err != nil {
		t.Fatal(err)
	}

	commitment := assetcredential.IdentityCommitment(keyA)
	var status string
	if err := owner.QueryRow(ctx, `SELECT public.control_initialize_asset_credential_key_v1($1)`, commitment[:]).Scan(&status); err != nil || status != "initialized" {
		t.Fatalf("source commitment initialization status=%q err=%v", status, err)
	}
	admin := uuid.New()
	if _, err := owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at) VALUES($1,$2,'Recovery Admin','enabled',clock_timestamp())`, admin, "recovery-"+admin.String()[:12]); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO node_drivers(node_type,driver_contract_version,display_name) VALUES('cliproxyapi','v1','Recovery Driver') ON CONFLICT DO NOTHING; INSERT INTO driver_capabilities(node_type,driver_contract_version,capability) VALUES('cliproxyapi','v1','management_health_read') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	var sourceDatabase string
	if err := owner.QueryRow(ctx, `SELECT current_database()`).Scan(&sourceDatabase); err != nil {
		t.Fatal(err)
	}
	productionRuntimeConfig, err := pgxpool.ParseConfig(os.Getenv("CONTROL_DATABASE_TEST_URL"))
	if err != nil {
		t.Fatal(err)
	}
	productionRuntimeConfig.ConnConfig.Database = sourceDatabase
	productionRuntimeConfig.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE relay_control_runtime`)
		return err
	}
	productionRuntime, err := pgxpool.NewWithConfig(ctx, productionRuntimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer productionRuntime.Close()
	runtime.Close()
	sealer := stage0AssetCredentialSealer{key: keyA, available: true}
	nodeRepository, err := assetstore.NewNodeLifecycleRepositoryWithSealer(productionRuntime, bytes.Repeat([]byte{0x61}, 32), sealer)
	if err != nil {
		t.Fatal(err)
	}
	gatewayRequests := 0
	gatewayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gatewayRequests++
		if r.Header.Get("Authorization") != "Bearer gateway-recovery-credential" {
			t.Error("recovery outbound did not receive the restored Gateway credential")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"schema_version":1,"generated_at":"2026-09-18T00:00:00Z","accounts":[]}`))
	}))
	defer gatewayServer.Close()
	gatewayRepository, err := assetstore.NewGatewayLifecycleRepositoryWithSealer(productionRuntime, bytes.Repeat([]byte{0x62}, 32), sealer)
	if err != nil {
		t.Fatal(err)
	}
	nodeID := uuid.New()
	gatewayID := uuid.New()
	nodeResult, err := nodeRepository.Register(ctx, assetstore.NodeCommand{
		CommandID: uuid.New(), ActorAdminID: admin, RequestID: "recovery-node-register", NewInstanceID: nodeID,
		DisplayName:        assetstore.StringPatch{Present: true, Value: "Recovery Node"},
		ManagementEndpoint: assetstore.StringPatch{Present: true, Value: "http://recovery-node.example"},
		Secret:             assetstore.SecretPatch{Operation: assetstore.SecretSet, Value: "node-recovery-credential"},
		NodeType:           "cliproxyapi", DriverContractVersion: "v1", Capabilities: []string{"management_health_read"},
	})
	if err != nil || nodeResult.HTTPStatus != http.StatusCreated {
		t.Fatalf("source Node registration failed: status=%d err=%v", nodeResult.HTTPStatus, err)
	}
	gatewayResult, err := gatewayRepository.Register(ctx, assetstore.GatewayCommand{
		CommandID: uuid.New(), ActorAdminID: admin, RequestID: "recovery-gateway-register", NewInstanceID: gatewayID,
		DisplayName:        assetstore.StringPatch{Present: true, Value: "Recovery Gateway"},
		ManagementEndpoint: assetstore.StringPatch{Present: true, Value: gatewayServer.URL},
		Secret:             assetstore.SecretPatch{Operation: assetstore.SecretSet, Value: "gateway-recovery-credential"},
	})
	if err != nil || gatewayResult.HTTPStatus != http.StatusCreated {
		t.Fatalf("source Gateway registration failed: status=%d err=%v", gatewayResult.HTTPStatus, err)
	}
	sourceResolver := stage0AssetCredentialResolver{pool: productionRuntime, opener: sealer}
	assertRecoveredSecret := func(resolver stage0AssetCredentialResolver, kind assetcredential.CredentialKind, id uuid.UUID, expected string) {
		t.Helper()
		secret, resolveErr := resolver.ResolveAssetCredential(ctx, kind, id)
		if resolveErr != nil || secret == nil {
			t.Fatalf("source credential resolve failed: kind=%s err=%v", kind, resolveErr)
		}
		defer secret.Destroy()
		if useErr := secret.Use(func(value string) error {
			if value != expected {
				t.Error("resolved credential did not match the source value")
			}
			return nil
		}); useErr != nil {
			t.Fatal(useErr)
		}
	}
	assertRecoveredSecret(sourceResolver, assetcredential.NodeCredential, nodeID, "node-recovery-credential")
	assertRecoveredSecret(sourceResolver, assetcredential.GatewayCredential, gatewayID, "gateway-recovery-credential")

	dump := postgres18Dump(t, ctx, sourceDatabase)
	if bytes.Contains(dump, keyA) || bytes.Contains(dump, []byte("node-recovery-credential")) || bytes.Contains(dump, []byte("gateway-recovery-credential")) {
		t.Fatal("backup artifact contained raw recovery material")
	}
	restoredOwner, restoredRuntime, restoredDatabase := restorePostgres18Dump(t, ctx, dump)
	defer restoredOwner.Close()
	defer restoredRuntime.Close()

	t.Setenv("CONTROL_ASSET_CREDENTIAL_KEY_FILE", pathRestored)
	restoredSealerValue, err := loadStage0AssetCredentialSealer(ctx, restoredRuntime, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)))
	if err != nil || restoredSealerValue == nil || !restoredSealerValue.Available() {
		t.Fatalf("same-K2 restored composition unavailable: err=%v", err)
	}
	restoredSealer := restoredSealerValue.(stage0AssetCredentialOpener)
	restoredResolver := stage0AssetCredentialResolver{pool: restoredRuntime, opener: restoredSealer}
	assertRecoveredSecret(restoredResolver, assetcredential.NodeCredential, nodeID, "node-recovery-credential")
	assertRecoveredSecret(restoredResolver, assetcredential.GatewayCredential, gatewayID, "gateway-recovery-credential")

	var restoredNodeBlob, restoredGatewayBlob []byte
	if err := restoredOwner.QueryRow(ctx, `SELECT public.control_read_node_management_credential_sealed_v1($1)`, nodeID).Scan(&restoredNodeBlob); err != nil {
		t.Fatal(err)
	}
	if err := restoredOwner.QueryRow(ctx, `SELECT public.control_read_gateway_directory_credential_sealed_v1($1)`, gatewayID).Scan(&restoredGatewayBlob); err != nil {
		t.Fatal(err)
	}
	runID, fencing := uuid.New(), uuid.New()
	started := time.Now().UTC().Truncate(time.Microsecond)
	scheduled := time.Unix((started.Unix()/180)*180, 0).UTC()
	if _, err := restoredOwner.Exec(ctx, `INSERT INTO gateway_directory_ingestion_runs(ingestion_run_id,gateway_instance_id,scheduled_at,status,attempt_count,created_at,first_started_at,last_started_at,lease_expires_at,lease_fencing_token) VALUES($1,$2,$3,'running',1,$4::timestamptz,$4::timestamptz,$4::timestamptz,$4::timestamptz+interval '15 seconds',$5)`, runID, gatewayID, scheduled, started, fencing); err != nil {
		t.Fatal(err)
	}
	client, err := gatewaydirectory.NewClientWithGatewayDirectoryCredentialResolver(gatewayServer.URL, restoredResolver)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := client.FetchAssetFenced(ctx, runID, gatewayID, fencing); err != nil {
		t.Fatalf("restored authenticated Directory outbound failed: %v", err)
	}
	if gatewayRequests != 1 {
		t.Fatalf("restored authenticated outbound count=%d, want 1", gatewayRequests)
	}

	assertUnavailable := func(path string) {
		t.Helper()
		t.Setenv("CONTROL_ASSET_CREDENTIAL_KEY_FILE", path)
		unavailable, loadErr := loadStage0AssetCredentialSealer(ctx, restoredRuntime, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)))
		if loadErr != nil || unavailable == nil || unavailable.Available() {
			t.Fatalf("unavailable K2 composition unexpectedly available: err=%v", loadErr)
		}
		resolver := stage0AssetCredentialResolver{pool: restoredRuntime, opener: unavailable.(stage0AssetCredentialOpener)}
		if _, resolveErr := resolver.ResolveAssetCredential(ctx, assetcredential.NodeCredential, nodeID); !errors.Is(resolveErr, controlnodes.ErrSecretUnavailable) {
			t.Fatalf("unavailable Node resolve error=%v", resolveErr)
		}
		unavailableClient, clientErr := gatewaydirectory.NewClientWithGatewayDirectoryCredentialResolver(gatewayServer.URL, resolver)
		if clientErr != nil {
			t.Fatal(clientErr)
		}
		if _, _, fetchErr := unavailableClient.FetchAssetFenced(ctx, runID, gatewayID, fencing); fetchErr == nil {
			t.Fatal("unavailable K2 unexpectedly permitted authenticated outbound")
		}
		if gatewayRequests != 1 {
			t.Fatalf("unavailable K2 made outbound requests=%d", gatewayRequests)
		}
	}
	assertUnavailable(pathWrong)
	assertUnavailable(filepath.Join(directory, "missing-k2"))
	var restoredCommitment []byte
	if err := restoredOwner.QueryRow(ctx, `SELECT k2_identity_commitment FROM control_asset_credential_key_identity WHERE singleton_id=1`).Scan(&restoredCommitment); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restoredCommitment, commitment[:]) {
		t.Fatal("wrong or missing K2 changed the restored commitment")
	}
	var finalNodeBlob, finalGatewayBlob []byte
	if err := restoredOwner.QueryRow(ctx, `SELECT public.control_read_node_management_credential_sealed_v1($1)`, nodeID).Scan(&finalNodeBlob); err != nil {
		t.Fatal(err)
	}
	if err := restoredOwner.QueryRow(ctx, `SELECT public.control_read_gateway_directory_credential_sealed_v1($1)`, gatewayID).Scan(&finalGatewayBlob); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(finalNodeBlob, restoredNodeBlob) || !bytes.Equal(finalGatewayBlob, restoredGatewayBlob) {
		t.Fatal("wrong or missing K2 changed restored sealed state")
	}
	_ = restoredDatabase
}

func postgres18Dump(t *testing.T, ctx context.Context, database string) []byte {
	t.Helper()
	command := exec.CommandContext(ctx, "docker", "exec", "relay-control-stage0-pg", "pg_dump", "-Fc", "-U", "relay_control_migrator", "-d", database)
	dump, err := command.Output()
	if err != nil {
		t.Fatalf("PostgreSQL 18 pg_dump failed: %v", err)
	}
	return dump
}

func restorePostgres18Dump(t *testing.T, ctx context.Context, dump []byte) (owner, runtime *pgxpool.Pool, database string) {
	t.Helper()
	baseOwner, err := pgx.ParseConfig(os.Getenv("CONTROL_DATABASE_TEST_URL"))
	if err != nil {
		t.Fatal(err)
	}
	baseOwner.Database = "postgres"
	maintenance, err := pgx.ConnectConfig(ctx, baseOwner)
	if err != nil {
		t.Fatal(err)
	}
	database = "control_recovery_restore_" + uuid.NewString()[:16]
	if _, err := maintenance.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{database}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	maintenance.Close(ctx)
	cleanup := func() {
		cleanupConfig, configErr := pgx.ParseConfig(os.Getenv("CONTROL_DATABASE_TEST_URL"))
		if configErr != nil {
			return
		}
		cleanupConfig.Database = "postgres"
		cleanupConn, connectErr := pgx.ConnectConfig(context.Background(), cleanupConfig)
		if connectErr != nil {
			return
		}
		_, _ = cleanupConn.Exec(context.Background(), `DROP DATABASE IF EXISTS `+pgx.Identifier{database}.Sanitize()+` WITH (FORCE)`)
		cleanupConn.Close(context.Background())
	}
	t.Cleanup(cleanup)
	restore := exec.CommandContext(ctx, "docker", "exec", "-i", "relay-control-stage0-pg", "pg_restore", "--exit-on-error", "-U", "relay_control_migrator", "-d", database)
	restore.Stdin = bytes.NewReader(dump)
	if err := restore.Run(); err != nil {
		t.Fatalf("PostgreSQL 18 pg_restore failed: %v", err)
	}
	ownerConfig, err := pgxpool.ParseConfig(os.Getenv("CONTROL_DATABASE_TEST_URL"))
	if err != nil {
		t.Fatal(err)
	}
	ownerConfig.ConnConfig.Database = database
	owner, err = pgxpool.NewWithConfig(ctx, ownerConfig)
	if err != nil {
		t.Fatal(err)
	}
	runtimeConfig, err := pgxpool.ParseConfig(os.Getenv("CONTROL_RUNTIME_DATABASE_TEST_URL"))
	if err != nil {
		owner.Close()
		t.Fatal(err)
	}
	runtimeConfig.ConnConfig.Database = database
	runtimeConfig.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE relay_control_runtime`)
		return err
	}
	runtime, err = pgxpool.NewWithConfig(ctx, runtimeConfig)
	if err != nil {
		owner.Close()
		t.Fatal(err)
	}
	return owner, runtime, database
}

func TestStage0AssetCredentialInitializerDatabaseFailureIsStartupFatalPG(t *testing.T) {
	_, runtime := isolatedCrossNodeDuplicateOwnershipDatabase(t)
	directory := t.TempDir()
	path := filepath.Join(directory, "k2")
	if err := os.WriteFile(path, bytes.Repeat([]byte{0x41}, assetcredential.KeySize), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime.Close()
	t.Setenv("CONTROL_ASSET_CREDENTIAL_KEY_FILE", path)
	sealer, err := loadStage0AssetCredentialSealer(context.Background(), runtime, slog.Default())
	if err == nil {
		t.Fatal("closed PostgreSQL pool was treated as credential unavailable")
	}
	if sealer != nil {
		t.Fatal("database failure returned a usable sealer")
	}
}

// assetstoreCredentialSealer keeps this test independent of the concrete
// store package name while only asserting the public capability contract.
type assetstoreCredentialSealer interface {
	Available() bool
}
