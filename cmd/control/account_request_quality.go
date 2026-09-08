package main

import (
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sunxu/relay-station-control/internal/requestquality"
	"github.com/sunxu/relay-station-control/internal/store"
)

func newAccountRequestQualityRuntime(pool *pgxpool.Pool, nodeDrivers nodeDriverRuntime, logger *slog.Logger) (*requestquality.Collector, error) {
	enabled, err := envBool("CONTROL_ACCOUNT_REQUEST_QUALITY_ENABLED", false)
	if err != nil {
		return nil, errors.New("request quality configuration is invalid")
	}
	if !enabled {
		return nil, nil
	}
	if !nodeDrivers.enabled || nodeDrivers.usageSource == nil {
		return nil, errors.New("request quality requires the CLIProxy management driver")
	}
	repository, err := store.NewAccountRequestQualityRepository(pool)
	if err != nil {
		return nil, err
	}
	return requestquality.NewCollector(repository, nodeDrivers.usageSource, repository, logger)
}
