package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/sunxu/relay-station-control/internal/assetcredential"
	assetcredentialruntime "github.com/sunxu/relay-station-control/internal/assetcredentialruntime"
)

func TestLoadStage0AssetCredentialSealerMissingPathIsUnavailable(t *testing.T) {
	t.Setenv("CONTROL_ASSET_CREDENTIAL_KEY_FILE", "")
	var output strings.Builder
	logger := slog.New(slog.NewTextHandler(&output, nil))
	sealer, err := loadStage0AssetCredentialSealer(context.Background(), nil, logger)
	if err != nil {
		t.Fatal(err)
	}
	if sealer.Available() {
		t.Fatal("missing K2 path produced an available sealer")
	}
	if !strings.Contains(output.String(), "asset credential cipher unavailable") {
		t.Fatalf("sanitized warning missing: %q", output.String())
	}
	if strings.Contains(output.String(), "CONTROL_ASSET_CREDENTIAL_KEY_FILE") {
		t.Fatalf("warning exposed configuration details: %q", output.String())
	}
}

func TestLoadStage0AssetCredentialSealerRepresentativeFileFailureIsUnavailable(t *testing.T) {
	directory := t.TempDir()
	path := directory + "/k2"
	if err := os.WriteFile(path, []byte("too short"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONTROL_ASSET_CREDENTIAL_KEY_FILE", path)
	sealer, err := loadStage0AssetCredentialSealer(context.Background(), nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if sealer.Available() {
		t.Fatal("invalid K2 file produced an available sealer")
	}
}

func TestStage0AssetCredentialSealerUsesFrozenAAD(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	sealer := assetcredentialruntime.NewTestSealer(key, true)
	id := uuid.New()
	plaintext := []byte("test credential")
	blob, err := sealer.Seal(assetcredential.NodeCredential, id, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := assetcredential.Open(key, assetcredential.NodeCredential, id, blob)
	if err != nil || !bytes.Equal(opened, plaintext) {
		t.Fatalf("node sealer round trip failed: err=%v", err)
	}
	if _, err = assetcredential.Open(key, assetcredential.GatewayCredential, id, blob); err == nil {
		t.Fatal("gateway AAD unexpectedly opened node blob")
	}
}

func TestStage0AssetCredentialSealerDoesNotGenerateWhenUnavailable(t *testing.T) {
	sealer := assetcredentialruntime.NewTestSealer(nil, false)
	if _, err := sealer.Seal(assetcredential.NodeCredential, uuid.New(), []byte("credential")); err == nil {
		t.Fatal("unavailable sealer generated or sealed a credential")
	}
}

func TestClassifyAssetCredentialInitializationStatusFailsClosed(t *testing.T) {
	for _, status := range []string{"available", "initialized"} {
		available, err := classifyAssetCredentialInitializationStatus(status)
		if err != nil || !available {
			t.Fatalf("status %q classified as unavailable: available=%v err=%v", status, available, err)
		}
	}
	for _, status := range []string{"unavailable", "invalid_database_state"} {
		available, err := classifyAssetCredentialInitializationStatus(status)
		if err != nil || available {
			t.Fatalf("status %q classified as startup fatal: available=%v err=%v", status, available, err)
		}
	}
	if _, err := classifyAssetCredentialInitializationStatus("unexpected_status"); !errors.Is(err, errAssetCredentialInitialization) {
		t.Fatalf("unexpected status error = %v", err)
	}
}
