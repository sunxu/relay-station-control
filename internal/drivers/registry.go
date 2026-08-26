package drivers

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalidRegistration    = errors.New("node driver registry: invalid registration")
	ErrDuplicateRegistration  = errors.New("node driver registry: duplicate registration")
	ErrNodeTypeUnsupported    = errors.New("node driver registry: node type unsupported")
	ErrDriverContractMismatch = errors.New("node driver registry: driver contract mismatch")
	ErrCapabilityUnsupported  = errors.New("node driver registry: capability unsupported")
	ErrTargetIncompatible     = errors.New("node driver registry: target incompatible")
)

type Registration struct {
	NodeType              NodeType
	DriverContractVersion DriverContractVersion
	Capabilities          []Capability
	Driver                NodeDriver
}

type registeredDriver struct {
	contract     DriverContractVersion
	capabilities map[Capability]struct{}
	driver       NodeDriver
}

// Registry is immutable after construction. Database node_type values select
// code already present here; they never dynamically register implementations.
type Registry struct {
	drivers  map[NodeType]registeredDriver
	observer RegistryObserver
}

type RegistryObserver interface {
	Record(context.Context, Operation, Result, Reason, time.Duration) bool
}

func NewRegistry(registrations ...Registration) (*Registry, error) {
	return NewRegistryWithObserver(nil, registrations...)
}

func NewRegistryWithObserver(observer RegistryObserver, registrations ...Registration) (*Registry, error) {
	registry := &Registry{drivers: make(map[NodeType]registeredDriver, len(registrations)), observer: observer}
	for _, registration := range registrations {
		if err := validateRegistration(registration); err != nil {
			return nil, err
		}
		if _, exists := registry.drivers[registration.NodeType]; exists {
			return nil, ErrDuplicateRegistration
		}
		capabilities := make(map[Capability]struct{}, len(registration.Capabilities))
		for _, capability := range registration.Capabilities {
			if _, duplicate := capabilities[capability]; duplicate {
				return nil, ErrInvalidRegistration
			}
			capabilities[capability] = struct{}{}
		}
		registry.drivers[registration.NodeType] = registeredDriver{
			contract: registration.DriverContractVersion, capabilities: capabilities, driver: registration.Driver,
		}
	}
	return registry, nil
}

func validateRegistration(registration Registration) error {
	if registration.NodeType != NodeTypeCLIProxyAPI ||
		registration.DriverContractVersion != DriverContractCLIProxyAPIAuthFilesV1 ||
		registration.Driver == nil || len(registration.Capabilities) != 2 {
		return ErrInvalidRegistration
	}
	for _, capability := range registration.Capabilities {
		if capability != CapabilityManagementHealthRead && capability != CapabilityManagementAccountInventoryRead {
			return ErrInvalidRegistration
		}
	}
	return nil
}

// ValidateTargetCompatibility compares persisted asset strings with the fixed
// code registry. It neither mutates the target nor registers a database value.
func (registry *Registry) ValidateTargetCompatibility(target NodeTarget) error {
	if registry == nil || target.InstanceID == [16]byte{} || target.ManagementEndpoint == "" || target.ReaderSecretReference.value == "" {
		return ErrTargetIncompatible
	}
	driver, exists := registry.drivers[target.NodeType]
	if !exists {
		return ErrNodeTypeUnsupported
	}
	if target.DriverContractVersion != driver.contract {
		return ErrDriverContractMismatch
	}
	if len(target.Capabilities) == 0 {
		return ErrTargetIncompatible
	}
	seen := make(map[Capability]struct{}, len(target.Capabilities))
	for _, capability := range target.Capabilities {
		if _, duplicate := seen[capability]; duplicate {
			return ErrTargetIncompatible
		}
		seen[capability] = struct{}{}
		if _, supported := driver.capabilities[capability]; !supported {
			return ErrCapabilityUnsupported
		}
	}
	return nil
}

func (registry *Registry) Probe(ctx context.Context, request ProbeRequest) (ProbeObservation, error) {
	started := time.Now()
	driver, err := registry.driverFor(request.Target, CapabilityManagementHealthRead)
	if err != nil {
		reason := reasonForRegistryError(err)
		registry.observeRejection(ctx, OperationProbe, reason, time.Since(started))
		return ProbeObservation{Result: ResultUnsupported, Reason: reason}, err
	}
	return driver.Probe(ctx, request)
}

func (registry *Registry) ListAccountInventory(ctx context.Context, request InventoryRequest) (InventoryObservation, error) {
	started := time.Now()
	driver, err := registry.driverFor(request.Target, CapabilityManagementAccountInventoryRead)
	if err != nil {
		reason := reasonForRegistryError(err)
		registry.observeRejection(ctx, OperationAccountInventory, reason, time.Since(started))
		return InventoryObservation{Result: ResultUnsupported, Reason: reason}, err
	}
	return driver.ListAccountInventory(ctx, request)
}

func (registry *Registry) observeRejection(ctx context.Context, operation Operation, reason Reason, elapsed time.Duration) {
	if registry != nil && registry.observer != nil {
		registry.observer.Record(ctx, operation, ResultUnsupported, reason, elapsed)
	}
}

func (registry *Registry) driverFor(target NodeTarget, required Capability) (NodeDriver, error) {
	if registry == nil {
		return nil, ErrNodeTypeUnsupported
	}
	driver, exists := registry.drivers[target.NodeType]
	if !exists {
		return nil, ErrNodeTypeUnsupported
	}
	if target.DriverContractVersion != driver.contract {
		return nil, ErrDriverContractMismatch
	}
	if _, supported := driver.capabilities[required]; !supported {
		return nil, ErrCapabilityUnsupported
	}
	declared := false
	seen := make(map[Capability]struct{}, len(target.Capabilities))
	for _, capability := range target.Capabilities {
		if _, duplicate := seen[capability]; duplicate {
			return nil, ErrCapabilityUnsupported
		}
		seen[capability] = struct{}{}
		if _, supported := driver.capabilities[capability]; !supported {
			return nil, ErrCapabilityUnsupported
		}
		if capability == required {
			declared = true
		}
	}
	if !declared {
		return nil, ErrCapabilityUnsupported
	}
	return driver.driver, nil
}

func reasonForRegistryError(err error) Reason {
	switch {
	case errors.Is(err, ErrNodeTypeUnsupported):
		return ReasonNodeTypeUnsupported
	case errors.Is(err, ErrDriverContractMismatch):
		return ReasonDriverContractMismatch
	default:
		return ReasonCapabilityUnsupported
	}
}
