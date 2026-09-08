package main

import (
	"errors"
	"log/slog"
	"os"
	"time"

	controlnodes "github.com/sunxu/relay-station-control/internal/drivers"
	controlcliproxy "github.com/sunxu/relay-station-control/internal/drivers/cliproxyapi"
)

type nodeDriverRuntime struct {
	enabled     bool
	usageSource *controlcliproxy.Driver
	registry    *controlnodes.Registry
	metrics     *controlcliproxy.DriverMetrics
}

// loadNodeDriverRuntime constructs only the fixed registry and its immutable
// dependencies. It does not resolve a target, read a target Secret, open a
// network connection, start a goroutine, or register a durable job.
func loadNodeDriverRuntime(logger *slog.Logger) (nodeDriverRuntime, error) {
	enabled, err := envBool("CONTROL_CLIPROXYAPI_DRIVER_ENABLED", false)
	if err != nil {
		return nodeDriverRuntime{}, errors.New("node driver configuration is invalid")
	}
	metrics := controlcliproxy.NewDriverMetrics()
	if !enabled {
		registry, registryErr := controlnodes.NewRegistry()
		if registryErr != nil {
			return nodeDriverRuntime{}, errors.New("node driver registry is invalid")
		}
		return nodeDriverRuntime{registry: registry, metrics: metrics}, nil
	}

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
	mappingBytes, err := envIntBounded(
		"CONTROL_CLIPROXYAPI_SECRET_MAPPING_MAX_BYTES",
		int(controlnodes.DefaultSecretMappingBytes),
		1,
		1<<20,
	)
	if err != nil || mappingBytes < secretBytes {
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
		MaxSecretMappingBytes:     int64(mappingBytes),
	}
	if _, err = management.Validate(); err != nil {
		return nodeDriverRuntime{}, errors.New("node driver configuration is invalid")
	}
	secretResolver, err := controlnodes.NewFileSecretResolver(controlnodes.FileSecretResolverConfig{
		MappingFile:     os.Getenv("CONTROL_CLIPROXYAPI_SECRET_MAPPING_FILE"),
		Provider:        controlnodes.FileSecretProvider,
		MaxMappingBytes: int64(mappingBytes),
		MaxSecretBytes:  int64(secretBytes),
	})
	if err != nil {
		return nodeDriverRuntime{}, errors.New("node driver secret configuration is invalid")
	}
	observer := controlcliproxy.NewObserver(metrics, logger)
	driver, err := controlcliproxy.NewDriver(controlcliproxy.DriverConfig{
		Management:     management,
		SecretResolver: secretResolver,
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
	return nodeDriverRuntime{enabled: true, registry: registry, metrics: metrics, usageSource: driver}, nil
}
