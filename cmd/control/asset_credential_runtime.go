package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	assetcredentialruntime "github.com/sunxu/relay-station-control/internal/assetcredentialruntime"
	assetstore "github.com/sunxu/relay-station-control/internal/store"
)

var errAssetCredentialUnavailable = assetcredentialruntime.ErrUnavailable
var errAssetCredentialInitialization = assetcredentialruntime.ErrInitialization

func classifyAssetCredentialInitializationStatus(status string) (bool, error) {
	return assetcredentialruntime.ClassifyInitializationStatus(status)
}

type stage0AssetCredentialSealer = assetcredentialruntime.Sealer
type stage0AssetCredentialOpener = assetcredentialruntime.Opener

func loadStage0AssetCredentialSealer(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) (assetstore.AssetCredentialSealer, error) {
	return assetcredentialruntime.Load(ctx, pool, os.Getenv("CONTROL_ASSET_CREDENTIAL_KEY_FILE"), uint32(os.Getuid()), logger)
}
