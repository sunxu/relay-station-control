package main

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"os"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sunxu/relay-station-control/internal/assetcredential"
	assetstore "github.com/sunxu/relay-station-control/internal/store"
)

var errAssetCredentialUnavailable = errors.New("asset credential cipher unavailable")
var errAssetCredentialInitialization = errors.New("asset credential identity initialization failed")

func classifyAssetCredentialInitializationStatus(status string) (bool, error) {
	switch status {
	case "available", "initialized":
		return true, nil
	case "unavailable", "invalid_database_state":
		return false, nil
	default:
		return false, errAssetCredentialInitialization
	}
}

type stage0AssetCredentialSealer struct {
	key       []byte
	available bool
}

func (s stage0AssetCredentialSealer) Available() bool { return s.available }

func (s stage0AssetCredentialSealer) Seal(kind assetcredential.CredentialKind, id uuid.UUID, plaintext []byte) ([]byte, error) {
	if !s.available {
		return nil, errAssetCredentialUnavailable
	}
	return assetcredential.Seal(rand.Reader, s.key, kind, id, plaintext)
}

func (s stage0AssetCredentialSealer) Open(kind assetcredential.CredentialKind, id uuid.UUID, sealed []byte) ([]byte, error) {
	if !s.available {
		return nil, errAssetCredentialUnavailable
	}
	return assetcredential.Open(s.key, kind, id, sealed)
}

var _ assetstore.AssetCredentialSealer = stage0AssetCredentialSealer{}

func loadStage0AssetCredentialSealer(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) (assetstore.AssetCredentialSealer, error) {
	sealer := stage0AssetCredentialSealer{}
	warn := func(reason string) {
		var warning assetcredential.StartupWarning
		warning.Emit(func(message string) {
			if logger != nil {
				logger.Warn(message, "component", "asset_credential", "reason", reason)
			}
		})
	}
	path := os.Getenv("CONTROL_ASSET_CREDENTIAL_KEY_FILE")
	if path == "" {
		warn("unavailable")
		return sealer, nil
	}
	loader := assetcredential.NewKeyLoader(path, uint32(os.Getuid()))
	key, err := loader.Load()
	if err != nil {
		warn("unavailable")
		return sealer, nil
	}
	if pool == nil {
		return nil, errAssetCredentialInitialization
	}
	commitment := assetcredential.IdentityCommitment(key)
	var status string
	if err := pool.QueryRow(ctx, `SELECT public.control_initialize_asset_credential_key_v1($1)`, commitment[:]).Scan(&status); err != nil {
		return nil, errAssetCredentialInitialization
	}
	available, err := classifyAssetCredentialInitializationStatus(status)
	if err != nil {
		return nil, err
	}
	if !available {
		warn("unavailable")
		return sealer, nil
	}
	return stage0AssetCredentialSealer{key: key, available: true}, nil
}
