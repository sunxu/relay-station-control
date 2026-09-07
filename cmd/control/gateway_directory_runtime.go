package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	controlnodes "github.com/sunxu/relay-station-control/internal/drivers"
	controlstore "github.com/sunxu/relay-station-control/internal/store"
)

const (
	gatewayDirectoryEnabledEnvironment       = "CONTROL_GATEWAY_DIRECTORY_ENABLED"
	gatewayDirectorySecretMappingEnvironment = "CONTROL_GATEWAY_DIRECTORY_SECRET_MAPPING_FILE"
	gatewayDirectoryTickInterval             = 5 * time.Second
)

type gatewayDirectoryRuntimeConfig struct {
	enabled     bool
	mappingFile string
}

type gatewayDirectoryRuntime struct {
	enabled   bool
	service   gatewayDirectoryService
	collector prometheus.Collector
}

type gatewayDirectoryService interface {
	ReconcileTick(context.Context) ([]controlstore.GatewayDirectoryReconcileResult, error)
	WorkOnce(context.Context) ([]controlstore.GatewayDirectoryWorkResult, error)
}

func loadGatewayDirectoryRuntimeConfig() (gatewayDirectoryRuntimeConfig, error) {
	enabled, err := envBool(gatewayDirectoryEnabledEnvironment, false)
	if err != nil {
		return gatewayDirectoryRuntimeConfig{}, errors.New("gateway directory configuration is invalid")
	}
	mappingFile := ""
	if enabled {
		mappingFile = envOrDefault(gatewayDirectorySecretMappingEnvironment, "")
		if mappingFile == "" {
			return gatewayDirectoryRuntimeConfig{}, errors.New("gateway directory configuration is invalid")
		}
	}
	return gatewayDirectoryRuntimeConfig{enabled: enabled, mappingFile: mappingFile}, nil
}

func newGatewayDirectoryRuntime(
	prometheusRegisterer prometheus.Registerer,
	pool *pgxpool.Pool,
	configuration gatewayDirectoryRuntimeConfig,
) (gatewayDirectoryRuntime, error) {
	repository, err := controlstore.NewGatewayDirectoryIngestionRepository(pool)
	if err != nil {
		return gatewayDirectoryRuntime{}, err
	}
	metrics, err := controlstore.NewGatewayDirectoryMetrics(repository)
	if err != nil {
		return gatewayDirectoryRuntime{}, err
	}
	if prometheusRegisterer != nil {
		if err := prometheusRegisterer.Register(metrics); err != nil {
			return gatewayDirectoryRuntime{}, err
		}
	}
	runtime := gatewayDirectoryRuntime{enabled: configuration.enabled, collector: metrics}
	if !configuration.enabled {
		return runtime, nil
	}
	resolver, err := controlnodes.NewFileSecretResolver(controlnodes.FileSecretResolverConfig{
		MappingFile:     configuration.mappingFile,
		Provider:        controlnodes.FileSecretProvider,
		MaxMappingBytes: controlnodes.DefaultSecretMappingBytes,
		MaxSecretBytes:  controlnodes.DefaultSecretBytes,
	})
	if err != nil {
		return gatewayDirectoryRuntime{}, errors.New("gateway directory secret configuration is invalid")
	}
	service, err := controlstore.NewGatewayDirectoryIngestionService(repository, resolver)
	if err != nil {
		return gatewayDirectoryRuntime{}, errors.New("gateway directory service initialization failed")
	}
	runtime.service = service
	return runtime, nil
}

func (runtime gatewayDirectoryRuntime) run(ctx context.Context, logger *slog.Logger) {
	if !runtime.enabled || runtime.service == nil {
		return
	}
	ticker := time.NewTicker(gatewayDirectoryTickInterval)
	defer ticker.Stop()
	for {
		runtime.tick(ctx, logger)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (runtime gatewayDirectoryRuntime) tick(ctx context.Context, logger *slog.Logger) {
	if _, err := runtime.service.ReconcileTick(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logGatewayDirectoryRuntimeError(logger, "reconcile_failed")
	}
	if ctx.Err() != nil {
		return
	}
	if _, err := runtime.service.WorkOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logGatewayDirectoryRuntimeError(logger, "work_failed")
	}
}

func logGatewayDirectoryRuntimeError(logger *slog.Logger, reason string) {
	if logger != nil {
		logger.Error("gateway directory runtime tick failed", "component", "gateway_directory", "reason", reason)
	}
}
