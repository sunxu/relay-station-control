package main

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sunxu/relay-station-control/internal/assetcredential"
	controlnodes "github.com/sunxu/relay-station-control/internal/drivers"
)

type stage0AssetCredentialOpener interface {
	Open(assetcredential.CredentialKind, uuid.UUID, []byte) ([]byte, error)
}

type stage0AssetCredentialResolver struct {
	pool   *pgxpool.Pool
	opener stage0AssetCredentialOpener
}

func (r stage0AssetCredentialResolver) ResolveAssetCredential(ctx context.Context, kind assetcredential.CredentialKind, id uuid.UUID) (*controlnodes.Secret, error) {
	if r.pool == nil || r.opener == nil || id == uuid.Nil {
		return nil, controlnodes.ErrSecretUnavailable
	}
	var sealed []byte
	var err error
	switch kind {
	case assetcredential.NodeCredential:
		err = r.pool.QueryRow(ctx, `SELECT public.control_read_node_management_credential_sealed_v1($1)`, id).Scan(&sealed)
	case assetcredential.GatewayCredential:
		err = r.pool.QueryRow(ctx, `SELECT public.control_read_gateway_directory_credential_sealed_v1($1)`, id).Scan(&sealed)
	default:
		return nil, controlnodes.ErrSecretUnavailable
	}
	if errors.Is(err, pgx.ErrNoRows) || err != nil || len(sealed) == 0 {
		return nil, controlnodes.ErrSecretUnavailable
	}
	plaintext, err := r.opener.Open(kind, id, sealed)
	if err != nil {
		return nil, controlnodes.ErrSecretUnavailable
	}
	return controlnodes.NewSecretFromBytes(plaintext), nil
}

func (r stage0AssetCredentialResolver) ResolveGatewayDirectoryCredential(ctx context.Context, runID, gatewayID, fencing uuid.UUID) (*controlnodes.Secret, error) {
	if r.pool == nil || r.opener == nil || runID == uuid.Nil || gatewayID == uuid.Nil || fencing == uuid.Nil {
		return nil, controlnodes.ErrSecretUnavailable
	}
	var sealed []byte
	if err := r.pool.QueryRow(ctx, `SELECT public.control_read_gateway_directory_credential_fenced_v1($1,$2,$3)`, runID, gatewayID, fencing).Scan(&sealed); err != nil || len(sealed) == 0 {
		return nil, controlnodes.ErrSecretUnavailable
	}
	plaintext, err := r.opener.Open(assetcredential.GatewayCredential, gatewayID, sealed)
	if err != nil {
		return nil, controlnodes.ErrSecretUnavailable
	}
	return controlnodes.NewSecretFromBytes(plaintext), nil
}

var _ controlnodes.AssetCredentialResolver = stage0AssetCredentialResolver{}
var _ controlnodes.GatewayDirectoryCredentialResolver = stage0AssetCredentialResolver{}
