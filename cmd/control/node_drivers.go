package main

import (
	"errors"
	"log/slog"
	"time"

	controlnodes "github.com/sunxu/relay-station-control/internal/drivers"
	controlcliproxy "github.com/sunxu/relay-station-control/internal/drivers/cliproxyapi"
)

type nodeDriverRuntime struct {
	enabled     bool
	usageSource *controlcliproxy.Driver
	registry    *controlnodes.Registry
	metrics     *controlcliproxy.DriverMetrics
	management  controlnodes.ValidatedManagementConfig
	secrets     controlnodes.SecretResolver
	assetSecret controlnodes.AssetCredentialResolver
}

// loadNodeDriverRuntime constructs only the fixed registry and its immutable
// dependencies. It does not resolve a target, read a target Secret, open a
// network connection, start a goroutine, or register a durable job.
func loadNodeDriverRuntime(logger *slog.Logger, assetResolver controlnodes.AssetCredentialResolver) (nodeDriverRuntime, error) {
	var err error
	metrics := controlcliproxy.NewDriverMetrics()

	connectTimeout, err := envDurationBounded(
		"CONTROL_CLIPROXYAPI_CONNECT_TIMEOUT",
		controlnodes.DefaultConnectTimeout,
		100*time.Millisecond,
		10*time.Second,
	)
	if err != nil {
		return nodeDriverRuntime{}, errors.New("node driver configuration is invalid")
	}
	requestTimeout, err := envDurationBounded(
		"CONTROL_CLIPROXYAPI_REQUEST_TIMEOUT",
		controlnodes.DefaultRequestTimeout,
		time.Second,
		30*time.Second,
	)
	if err != nil {
		return nodeDriverRuntime{}, errors.New("node driver configuration is invalid")
	}
	healthBytes, err := envIntBounded(
		"CONTROL_CLIPROXYAPI_HEALTH_MAX_BYTES",
		int(controlnodes.DefaultHealthResponseBytes),
		128,
		1<<20,
	)
	if err != nil {
		return nodeDriverRuntime{}, errors.New("node driver configuration is invalid")
	}
	inventoryBytes, err := envIntBounded(
		"CONTROL_CLIPROXYAPI_INVENTORY_MAX_BYTES",
		int(controlnodes.DefaultInventoryResponseBytes),
		1024,
		int(controlnodes.DefaultInventoryResponseBytes),
	)
	if err != nil {
		return nodeDriverRuntime{}, errors.New("node driver configuration is invalid")
	}
	inventoryRecords, err := envIntBounded(
		"CONTROL_CLIPROXYAPI_INVENTORY_MAX_RECORDS",
		controlnodes.DefaultInventoryRecords,
		1,
		controlnodes.DefaultInventoryRecords,
	)
	if err != nil {
		return nodeDriverRuntime{}, errors.New("node driver configuration is invalid")
	}
	secretBytes, err := envIntBounded(
		"CONTROL_CLIPROXYAPI_SECRET_MAX_BYTES",
		int(controlnodes.DefaultSecretBytes),
		1,
		64<<10,
	)
	if err != nil {
		return nodeDriverRuntime{}, errors.New("node driver configuration is invalid")
	}
	if assetResolver == nil {
		return nodeDriverRuntime{}, errors.New("node driver configuration is invalid")
	}

	// Legacy target allowlists and CA configuration are intentionally ignored.
	management := controlnodes.ManagementConfig{
		ConnectTimeout:            connectTimeout,
		RequestTimeout:            requestTimeout,
		MaxHealthResponseBytes:    int64(healthBytes),
		MaxInventoryResponseBytes: int64(inventoryBytes),
		MaxInventoryRecords:       inventoryRecords,
		MaxSecretBytes:            int64(secretBytes),
		MaxSecretMappingBytes:     int64(controlnodes.DefaultSecretMappingBytes),
	}
	if _, err = management.Validate(); err != nil {
		return nodeDriverRuntime{}, errors.New("node driver configuration is invalid")
	}
	var secretResolver controlnodes.SecretResolver
	observer := controlcliproxy.NewObserver(metrics, logger)
	driver, err := controlcliproxy.NewDriver(controlcliproxy.DriverConfig{
		Management:     management,
		SecretResolver: secretResolver,
		AssetResolver:  assetResolver,
		Observer:       observer,
	})
	if err != nil {
		return nodeDriverRuntime{}, errors.New("node driver configuration is invalid")
	}
	registry, err := controlnodes.NewRegistryWithObserver(observer, controlnodes.Registration{
		NodeType:              controlnodes.NodeTypeCLIProxyAPI,
		DriverContractVersion: controlnodes.DriverContractCLIProxyAPIAuthFilesV1,
		Capabilities: []controlnodes.Capability{
			controlnodes.CapabilityManagementHealthRead,
			controlnodes.CapabilityManagementAccountInventoryRead,
		},
		Driver: driver,
	})
	if err != nil {
		return nodeDriverRuntime{}, errors.New("node driver registry is invalid")
	}
	validatedManagement, _ := management.Validate()
	return nodeDriverRuntime{enabled: true, registry: registry, metrics: metrics, usageSource: driver, management: validatedManagement, secrets: secretResolver, assetSecret: assetResolver}, nil
}
