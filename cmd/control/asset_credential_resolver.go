package main

import (
	"github.com/jackc/pgx/v5/pgxpool"
	assetcredentialruntime "github.com/sunxu/relay-station-control/internal/assetcredentialruntime"
)

type stage0AssetCredentialResolver = assetcredentialruntime.Resolver

func newStage0AssetCredentialResolver(pool *pgxpool.Pool, opener stage0AssetCredentialOpener) *stage0AssetCredentialResolver {
	return assetcredentialruntime.NewResolver(pool, opener)
}
