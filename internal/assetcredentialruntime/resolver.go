package assetcredentialruntime

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sunxu/relay-station-control/internal/assetcredential"
	"github.com/sunxu/relay-station-control/internal/drivers"
)

// Opener is the narrow Stage 0 opening capability used by the protected-read
// resolver. It is deliberately specific to asset credential runtime behavior.
type Opener interface {
	Open(assetcredential.CredentialKind, uuid.UUID, []byte) ([]byte, error)
}

// Resolver performs the protected Stage 0 reads used by Node and Gateway
// runtime consumers. Gateway Directory reads always use the fenced SQL path.
type Resolver struct {
	pool   *pgxpool.Pool
	opener Opener
}

func NewResolver(pool *pgxpool.Pool, opener Opener) *Resolver {
	return &Resolver{pool: pool, opener: opener}
}

func (r *Resolver) ResolveAssetCredential(ctx context.Context, kind assetcredential.CredentialKind, id uuid.UUID) (*drivers.Secret, error) {
	if r == nil || r.pool == nil || r.opener == nil || id == uuid.Nil {
		return nil, drivers.ErrSecretUnavailable
	}
	var sealed []byte
	var err error
	switch kind {
	case assetcredential.NodeCredential:
		err = r.pool.QueryRow(ctx, `SELECT public.control_read_node_management_credential_sealed_v1($1)`, id).Scan(&sealed)
	case assetcredential.GatewayCredential:
		err = r.pool.QueryRow(ctx, `SELECT public.control_read_gateway_directory_credential_sealed_v1($1)`, id).Scan(&sealed)
	default:
		return nil, drivers.ErrSecretUnavailable
	}
	if errors.Is(err, pgx.ErrNoRows) || err != nil || len(sealed) == 0 {
		return nil, drivers.ErrSecretUnavailable
	}
	plaintext, err := r.opener.Open(kind, id, sealed)
	if err != nil {
		return nil, drivers.ErrSecretUnavailable
	}
	return drivers.NewSecretFromBytes(plaintext), nil
}

func (r *Resolver) ResolveGatewayDirectoryCredential(ctx context.Context, runID, gatewayID, fencing uuid.UUID) (*drivers.Secret, error) {
	if r == nil || r.pool == nil || r.opener == nil || runID == uuid.Nil || gatewayID == uuid.Nil || fencing == uuid.Nil {
		return nil, drivers.ErrSecretUnavailable
	}
	var sealed []byte
	if err := r.pool.QueryRow(ctx, `SELECT public.control_read_gateway_directory_credential_fenced_v1($1,$2,$3)`, runID, gatewayID, fencing).Scan(&sealed); err != nil || len(sealed) == 0 {
		return nil, drivers.ErrSecretUnavailable
	}
	plaintext, err := r.opener.Open(assetcredential.GatewayCredential, gatewayID, sealed)
	if err != nil {
		return nil, drivers.ErrSecretUnavailable
	}
	return drivers.NewSecretFromBytes(plaintext), nil
}

var _ drivers.AssetCredentialResolver = (*Resolver)(nil)
var _ drivers.GatewayDirectoryCredentialResolver = (*Resolver)(nil)
