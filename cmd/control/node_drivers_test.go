package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/sunxu/relay-station-control/internal/assetcredential"
	controlnodes "github.com/sunxu/relay-station-control/internal/drivers"
)

type unavailableNodeAssetResolver struct{}

func (unavailableNodeAssetResolver) ResolveAssetCredential(context.Context, assetcredential.CredentialKind, uuid.UUID) (*controlnodes.Secret, error) {
	return nil, controlnodes.ErrSecretUnavailable
}

func TestNodeDriverRuntimeDisabledHasNoDynamicRegistration(t *testing.T) {
	t.Skip("driver rollout flag removed; registry is fixed and deployment-owned")
	t.Setenv("CONTROL_CLIPROXYAPI_DRIVER_ENABLED", "false")
	t.Setenv("CONTROL_CLIPROXYAPI_MANAGEMENT_DNS", "invalid configuration ignored while disabled")
	runtime, err := loadNodeDriverRuntime(nil, unavailableNodeAssetResolver{})
	if err != nil {
		t.Fatal(err)
	}
	if runtime.enabled || runtime.registry == nil || runtime.metrics == nil {
		t.Fatalf("unexpected disabled runtime: %#v", runtime)
	}
	target := controlnodes.NodeTarget{
		InstanceID: uuid.New(), NodeType: controlnodes.NodeTypeCLIProxyAPI,
		DriverContractVersion: controlnodes.DriverContractCLIProxyAPIAuthFilesV1,
		Capabilities:          []controlnodes.Capability{controlnodes.CapabilityManagementHealthRead},
	}
	if err = runtime.registry.ValidateTargetCompatibility(target); err == nil {
		t.Fatal("disabled registry dynamically loaded a database node type")
	}
}

func TestNodeDriverRuntimeEnabledConstructsWithoutSecretOrNetworkAccess(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("CONTROL_CLIPROXYAPI_DRIVER_ENABLED", "true")
	// Retired target and CA policy must be ignored, including malformed values
	// and a path that does not exist.
	t.Setenv("CONTROL_CLIPROXYAPI_MANAGEMENT_DNS", "not a hostname")
	t.Setenv("CONTROL_CLIPROXYAPI_MANAGEMENT_CIDRS", "not-a-cidr")
	t.Setenv("CONTROL_CLIPROXYAPI_PLAIN_HTTP_CIDRS", "not-a-cidr")
	t.Setenv("CONTROL_CLIPROXYAPI_CA_FILE", filepath.Join(directory, "missing-ca.pem"))

	runtime, err := loadNodeDriverRuntime(nil, unavailableNodeAssetResolver{})
	if err != nil {
		t.Fatal(err)
	}
	if !runtime.enabled || runtime.registry == nil || runtime.metrics == nil {
		t.Fatalf("unexpected enabled runtime: %#v", runtime)
	}
	target := controlnodes.NodeTarget{
		InstanceID: uuid.New(), NodeType: controlnodes.NodeTypeCLIProxyAPI,
		DriverContractVersion: controlnodes.DriverContractCLIProxyAPIAuthFilesV1,
		ManagementEndpoint:    "https://node.example.invalid",
		Capabilities: []controlnodes.Capability{
			controlnodes.CapabilityManagementHealthRead,
			controlnodes.CapabilityManagementAccountInventoryRead,
		},
	}
	if err = runtime.registry.ValidateTargetCompatibility(target); err != nil {
		t.Fatalf("registered production contract: %v", err)
	}
}

func TestNodeDriverRuntimeFailsClosedWithoutLeakingConfiguration(t *testing.T) {
	t.Skip("driver rollout flag removed; invalid legacy flag is ignored")
	t.Setenv("CONTROL_CLIPROXYAPI_DRIVER_ENABLED", "true")
	canary := "management-config-canary.example.invalid"
	t.Setenv("CONTROL_CLIPROXYAPI_MANAGEMENT_DNS", canary)
	t.Setenv("CONTROL_CLIPROXYAPI_MANAGEMENT_CIDRS", "")
	t.Setenv("CONTROL_CLIPROXYAPI_DRIVER_ENABLED", "not-a-boolean-canary")
	_, err := loadNodeDriverRuntime(nil, unavailableNodeAssetResolver{})
	if err == nil || strings.Contains(err.Error(), "not-a-boolean-canary") {
		t.Fatalf("boolean configuration did not fail safely: %v", err)
	}
}

func TestDisabledNodeDriverRegistryCannotIssueRequests(t *testing.T) {
	t.Skip("driver rollout flag removed; registry is fixed and deployment-owned")
	t.Setenv("CONTROL_CLIPROXYAPI_DRIVER_ENABLED", "false")
	runtime, err := loadNodeDriverRuntime(nil, unavailableNodeAssetResolver{})
	if err != nil {
		t.Fatal(err)
	}
	observation, err := runtime.registry.Probe(context.Background(), controlnodes.ProbeRequest{Target: controlnodes.NodeTarget{
		InstanceID: uuid.New(), NodeType: controlnodes.NodeTypeCLIProxyAPI,
		DriverContractVersion: controlnodes.DriverContractCLIProxyAPIAuthFilesV1,
		ManagementEndpoint:    "https://network-must-not-be-reached.example.invalid",
		Capabilities:          []controlnodes.Capability{controlnodes.CapabilityManagementHealthRead},
	}})
	if err == nil || observation.Result != controlnodes.ResultUnsupported {
		t.Fatalf("disabled request = %#v, %v", observation, err)
	}
}
