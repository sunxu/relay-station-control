package compatgate

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestStage0O05CompatibilityAdmissionPreservesCredentialStatePG18(t *testing.T) {
	base := os.Getenv("CONTROL_DATABASE_TEST_URL")
	if base == "" {
		t.Skip("set CONTROL_DATABASE_TEST_URL for O05 PostgreSQL acceptance")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	databaseName := "control_stage0_o05_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{databaseName}.Sanitize()); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{databaseName}.Sanitize()+" WITH (FORCE)")
		_ = admin.Close(context.Background())
	})

	parsed, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "/" + databaseName
	databaseURL := parsed.String()
	migrate := exec.CommandContext(ctx, "go", "tool", "goose", "-dir", "../migrations", "postgres", databaseURL, "up")
	migrate.Dir = filepath.Join(repositoryRoot(t), "tools")
	if output, err := migrate.CombinedOutput(); err != nil {
		t.Fatalf("migrate O05 database: %v\n%s", err, output)
	}
	database, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close(context.Background())

	nodeID, gatewayID := uuid.New(), uuid.New()
	sealedNode := bytesForO05(0x11, 29)
	sealedGateway := bytesForO05(0x22, 29)
	commitment := bytesForO05(0x33, 32)
	if _, err := database.Exec(ctx, `INSERT INTO node_drivers
		(node_type, driver_contract_version, display_name)
		VALUES ('o05-driver', 'v1', 'O05 driver')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(ctx, `INSERT INTO relay_node_assets
		(instance_id, display_name, node_type, driver_contract_version,
		 management_endpoint, reader_secret_ref, management_credential_sealed)
		VALUES ($1, 'O05 node', 'o05-driver', 'v1', 'http://o05-node.invalid', NULL, $2)`, nodeID, sealedNode); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(ctx, `INSERT INTO gateway_instances
		(singleton_id, instance_id, display_name, management_endpoint,
		 reader_secret_ref, directory_credential_sealed)
		VALUES (1, $1, 'O05 gateway', 'http://o05-gateway.invalid', NULL, $2)`, gatewayID, sealedGateway); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(ctx, `INSERT INTO control_asset_credential_key_identity
		(singleton_id, k2_identity_commitment) VALUES (1, $1)`, commitment); err != nil {
		t.Fatal(err)
	}

	readState := func() (node, gateway, identity []byte) {
		t.Helper()
		if err := database.QueryRow(ctx, `SELECT management_credential_sealed
			FROM relay_node_assets WHERE instance_id=$1`, nodeID).Scan(&node); err != nil {
			t.Fatal(err)
		}
		if err := database.QueryRow(ctx, `SELECT directory_credential_sealed
			FROM gateway_instances WHERE singleton_id=1 AND instance_id=$1`, gatewayID).Scan(&gateway); err != nil {
			t.Fatal(err)
		}
		if err := database.QueryRow(ctx, `SELECT k2_identity_commitment
			FROM control_asset_credential_key_identity WHERE singleton_id=1`).Scan(&identity); err != nil {
			t.Fatal(err)
		}
		return
	}

	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + hex.EncodeToString(bytesForO05(0x44, 32))
	directory := t.TempDir()
	manifestPath, publicPath := filepath.Join(directory, "manifest.json"), filepath.Join(directory, "manifest.pub")
	if err := os.WriteFile(publicPath, public, 0o400); err != nil {
		t.Fatal(err)
	}
	validate := func(class int) error {
		t.Helper()
		if err := os.WriteFile(manifestPath, signedManifest(t, private, digest, class), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := ValidateOCIDigest(ctx, manifestPath, publicPath, digest, databaseURL)
		return err
	}
	assertUnchanged := func(before, after [][]byte) {
		t.Helper()
		if string(before[0]) != string(after[0]) || string(before[1]) != string(after[1]) || string(before[2]) != string(after[2]) {
			t.Fatalf("credential state unchanged=false")
		}
		t.Log("node_unchanged=true gateway_unchanged=true commitment_unchanged=true")
	}

	before := func() [][]byte {
		node, gateway, identity := readState()
		return [][]byte{node, gateway, identity}
	}
	initial := before()
	err = validate(3)
	var failure *Failure
	if !isCode(err, ExitIncompatible) || !errors.As(err, &failure) || failure.Reason != "compatibility_floor_rejected" {
		t.Fatalf("class-3 result=%v", err)
	}
	assertUnchanged(initial, before())

	if err := validate(4); err != nil {
		t.Fatalf("class-4 admission: %v", err)
	}
	assertUnchanged(initial, before())
}

func bytesForO05(value byte, length int) []byte {
	result := make([]byte, length)
	for i := range result {
		result[i] = value
	}
	return result
}
