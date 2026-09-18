// Package assetcredentialruntime composes the Stage 0 asset credential
// capability shared by Control and acceptance-only processes.
package assetcredentialruntime

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sunxu/relay-station-control/internal/assetcredential"
)

var (
	ErrUnavailable    = errors.New("asset credential cipher unavailable")
	ErrInitialization = errors.New("asset credential identity initialization failed")
)

func ClassifyInitializationStatus(status string) (bool, error) {
	switch status {
	case "available", "initialized":
		return true, nil
	case "unavailable", "invalid_database_state":
		return false, nil
	default:
		return false, ErrInitialization
	}
}

// Sealer is the Stage 0 K2-backed credential sealer and opener.
type Sealer struct {
	key       []byte
	available bool
}

// NewTestSealer creates a deterministic sealer for package-level integration
// tests. Production composition must use Load so K2 ownership and commitment
// initialization remain enforced.
func NewTestSealer(key []byte, available bool) Sealer {
	return Sealer{key: append([]byte(nil), key...), available: available}
}

func (s Sealer) Available() bool { return s.available }

func (s Sealer) Seal(kind assetcredential.CredentialKind, id uuid.UUID, plaintext []byte) ([]byte, error) {
	if !s.available {
		return nil, ErrUnavailable
	}
	return assetcredential.Seal(rand.Reader, s.key, kind, id, plaintext)
}

func (s Sealer) Open(kind assetcredential.CredentialKind, id uuid.UUID, sealed []byte) ([]byte, error) {
	if !s.available {
		return nil, ErrUnavailable
	}
	return assetcredential.Open(s.key, kind, id, sealed)
}

// Load loads K2 and initializes/classifies its database commitment. Missing or
// invalid K2 and an unavailable/invalid database state remain fail-soft; SQL
// execution errors and unknown statuses remain startup-fatal.
func Load(ctx context.Context, pool *pgxpool.Pool, keyPath string, expectedOwner uint32, logger *slog.Logger) (*Sealer, error) {
	sealer := &Sealer{}
	warn := func(reason string) {
		var warning assetcredential.StartupWarning
		warning.Emit(func(message string) {
			if logger != nil {
				logger.Warn(message, "component", "asset_credential", "reason", reason)
			}
		})
	}
	if keyPath == "" {
		warn("unavailable")
		return sealer, nil
	}
	key, err := assetcredential.NewKeyLoader(keyPath, expectedOwner).Load()
	if err != nil {
		warn("unavailable")
		return sealer, nil
	}
	if pool == nil {
		return nil, ErrInitialization
	}
	commitment := assetcredential.IdentityCommitment(key)
	var status string
	if err := pool.QueryRow(ctx, `SELECT public.control_initialize_asset_credential_key_v1($1)`, commitment[:]).Scan(&status); err != nil {
		return nil, ErrInitialization
	}
	available, err := ClassifyInitializationStatus(status)
	if err != nil {
		return nil, err
	}
	if !available {
		warn("unavailable")
		return sealer, nil
	}
	return &Sealer{key: key, available: true}, nil
}
