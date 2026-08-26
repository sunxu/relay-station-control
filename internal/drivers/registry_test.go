package drivers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type countingDriver struct {
	probeCalls     int
	inventoryCalls int
}

type registryTestObserver struct {
	calls     int
	operation Operation
	result    Result
	reason    Reason
}

func (observer *registryTestObserver) Record(_ context.Context, operation Operation, result Result, reason Reason, _ time.Duration) bool {
	observer.calls++
	observer.operation = operation
	observer.result = result
	observer.reason = reason
	return true
}

func (driver *countingDriver) Probe(context.Context, ProbeRequest) (ProbeObservation, error) {
	driver.probeCalls++
	return ProbeObservation{Reachable: true, Result: ResultSuccess, Reason: ReasonNone}, nil
}

func (driver *countingDriver) ListAccountInventory(context.Context, InventoryRequest) (InventoryObservation, error) {
	driver.inventoryCalls++
	return InventoryObservation{TransportSuccess: true, Result: ResultSuccess, Reason: ReasonNone}, nil
}

func TestRegistryIsClosedAndCapabilityBound(t *testing.T) {
	driver := &countingDriver{}
	registry, err := NewRegistry(Registration{
		NodeType: NodeTypeCLIProxyAPI, DriverContractVersion: DriverContractCLIProxyAPIAuthFilesV1,
		Capabilities: []Capability{CapabilityManagementHealthRead, CapabilityManagementAccountInventoryRead}, Driver: driver,
	})
	if err != nil {
		t.Fatalf("construct registry: %v", err)
	}
	target := NodeTarget{
		InstanceID: uuid.New(), NodeType: NodeTypeCLIProxyAPI,
		DriverContractVersion: DriverContractCLIProxyAPIAuthFilesV1,
		ManagementEndpoint:    "https://node-canary.example.invalid",
		ReaderSecretReference: NewSecretReference("file://reference-canary"),
		Capabilities:          []Capability{CapabilityManagementHealthRead, CapabilityManagementAccountInventoryRead},
	}
	if observation, err := registry.Probe(context.Background(), ProbeRequest{Target: target}); err != nil || !observation.Reachable {
		t.Fatalf("probe = %#v, %v", observation, err)
	}
	if _, err := registry.ListAccountInventory(context.Background(), InventoryRequest{Target: target}); err != nil {
		t.Fatalf("inventory: %v", err)
	}
	if driver.probeCalls != 1 || driver.inventoryCalls != 1 {
		t.Fatalf("calls = probe %d, inventory %d", driver.probeCalls, driver.inventoryCalls)
	}

	tests := []struct {
		name   string
		mutate func(*NodeTarget)
		want   error
	}{
		{"unknown node type", func(target *NodeTarget) { target.NodeType = "dynamic-node" }, ErrNodeTypeUnsupported},
		{"contract mismatch", func(target *NodeTarget) { target.DriverContractVersion = "future" }, ErrDriverContractMismatch},
		{"health capability missing", func(target *NodeTarget) { target.Capabilities = []Capability{CapabilityManagementAccountInventoryRead} }, ErrCapabilityUnsupported},
		{"unknown extra capability", func(target *NodeTarget) {
			target.Capabilities = []Capability{CapabilityManagementHealthRead, "management_write"}
		}, ErrCapabilityUnsupported},
		{"duplicate capability", func(target *NodeTarget) {
			target.Capabilities = []Capability{CapabilityManagementHealthRead, CapabilityManagementHealthRead}
		}, ErrCapabilityUnsupported},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			invalid := target
			test.mutate(&invalid)
			before := driver.probeCalls
			observation, err := registry.Probe(context.Background(), ProbeRequest{Target: invalid})
			if !errors.Is(err, test.want) || observation.Result != ResultUnsupported {
				t.Fatalf("observation/error = %#v/%v, want %v", observation, err, test.want)
			}
			if driver.probeCalls != before {
				t.Fatal("driver called before registry/capability rejection")
			}
		})
	}
}

func TestRegistryRejectsInvalidOrDuplicateRegistration(t *testing.T) {
	driver := &countingDriver{}
	valid := Registration{
		NodeType: NodeTypeCLIProxyAPI, DriverContractVersion: DriverContractCLIProxyAPIAuthFilesV1,
		Capabilities: []Capability{CapabilityManagementHealthRead, CapabilityManagementAccountInventoryRead}, Driver: driver,
	}
	tests := []struct {
		name          string
		registrations []Registration
		want          error
	}{
		{"unknown implementation", []Registration{{NodeType: "dynamic", DriverContractVersion: "v1", Capabilities: []Capability{CapabilityManagementHealthRead}, Driver: driver}}, ErrInvalidRegistration},
		{"unknown capability", []Registration{{NodeType: NodeTypeCLIProxyAPI, DriverContractVersion: DriverContractCLIProxyAPIAuthFilesV1, Capabilities: []Capability{"management_write"}, Driver: driver}}, ErrInvalidRegistration},
		{"duplicate capability", []Registration{{NodeType: NodeTypeCLIProxyAPI, DriverContractVersion: DriverContractCLIProxyAPIAuthFilesV1, Capabilities: []Capability{CapabilityManagementHealthRead, CapabilityManagementHealthRead}, Driver: driver}}, ErrInvalidRegistration},
		{"duplicate node type", []Registration{valid, valid}, ErrDuplicateRegistration},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewRegistry(test.registrations...); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestRegistryRejectionsUseOnlyClosedObservation(t *testing.T) {
	observer := &registryTestObserver{}
	registry, err := NewRegistryWithObserver(observer, Registration{
		NodeType: NodeTypeCLIProxyAPI, DriverContractVersion: DriverContractCLIProxyAPIAuthFilesV1,
		Capabilities: []Capability{CapabilityManagementHealthRead, CapabilityManagementAccountInventoryRead}, Driver: &countingDriver{},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = registry.Probe(context.Background(), ProbeRequest{Target: NodeTarget{
		InstanceID: uuid.New(), NodeType: "database-canary", DriverContractVersion: "future-canary",
		Capabilities: []Capability{CapabilityManagementHealthRead},
	}})
	if !errors.Is(err, ErrNodeTypeUnsupported) || observer.calls != 1 || observer.operation != OperationProbe ||
		observer.result != ResultUnsupported || observer.reason != ReasonNodeTypeUnsupported {
		t.Fatalf("closed observation = calls=%d operation=%s result=%s reason=%s err=%v",
			observer.calls, observer.operation, observer.result, observer.reason, err)
	}
}

func TestRegistryValidatesPersistedAssetCompatibilityWithoutDynamicRegistration(t *testing.T) {
	driver := &countingDriver{}
	registry, err := NewRegistry(Registration{
		NodeType: NodeTypeCLIProxyAPI, DriverContractVersion: DriverContractCLIProxyAPIAuthFilesV1,
		Capabilities: []Capability{CapabilityManagementHealthRead, CapabilityManagementAccountInventoryRead}, Driver: driver,
	})
	if err != nil {
		t.Fatal(err)
	}
	target := NodeTarget{
		InstanceID: uuid.New(), NodeType: NodeTypeCLIProxyAPI, DriverContractVersion: DriverContractCLIProxyAPIAuthFilesV1,
		ManagementEndpoint: "https://node.example.invalid", ReaderSecretReference: NewSecretReference("file://node/key"),
		Capabilities: []Capability{CapabilityManagementHealthRead},
	}
	if err := registry.ValidateTargetCompatibility(target); err != nil {
		t.Fatalf("valid persisted contract: %v", err)
	}
	unknown := target
	unknown.NodeType = "database-plugin-name"
	if err := registry.ValidateTargetCompatibility(unknown); !errors.Is(err, ErrNodeTypeUnsupported) {
		t.Fatalf("unknown node type = %v", err)
	}
	if driver.probeCalls != 0 || driver.inventoryCalls != 0 {
		t.Fatal("compatibility validation invoked the driver")
	}
	if err := registry.ValidateTargetCompatibility(target); err != nil {
		t.Fatalf("unknown asset validation modified registry: %v", err)
	}
}

func TestDriverBoundaryFormattingDoesNotProjectSensitiveValues(t *testing.T) {
	canaries := []string{"endpoint-format-canary", "reference-format-canary", "email-format-canary"}
	target := NodeTarget{
		ManagementEndpoint:    "https://endpoint-format-canary.example.invalid",
		ReaderSecretReference: NewSecretReference("file://reference-format-canary"),
	}
	objects := []any{
		target,
		ProbeRequest{Target: target},
		InventoryRequest{Target: target},
		NewSecretReference("file://reference-format-canary"),
		AccountObservation{Email: "email-format-canary@example.invalid"},
		ProviderObservation{Accounts: []AccountObservation{{Email: "email-format-canary@example.invalid"}}},
		InventoryObservation{Providers: []ProviderObservation{{Accounts: []AccountObservation{{Email: "email-format-canary@example.invalid"}}}}},
	}
	for _, object := range objects {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
			projected := fmt.Sprintf(format, object)
			for _, canary := range canaries {
				if strings.Contains(projected, canary) {
					t.Fatalf("format %q projected sensitive canary from %T", format, object)
				}
			}
		}
	}
}

func TestFixedEnums(t *testing.T) {
	if NodeTypeCLIProxyAPI != "cliproxyapi" || DriverContractCLIProxyAPIAuthFilesV1 != "cliproxyapi.auth-files.v1" {
		t.Fatal("node type or contract drifted")
	}
	if CapabilityManagementHealthRead != "management_health_read" || CapabilityManagementAccountInventoryRead != "management_account_inventory_read" {
		t.Fatal("capability enum drifted")
	}
}
