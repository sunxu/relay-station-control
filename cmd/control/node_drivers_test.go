package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	controlnodes "github.com/sunxu/relay-station-control/internal/drivers"
)

func TestNodeDriverRuntimeDisabledHasNoDynamicRegistration(t *testing.T) {
	t.Setenv("CONTROL_CLIPROXYAPI_DRIVER_ENABLED", "false")
	t.Setenv("CONTROL_CLIPROXYAPI_MANAGEMENT_DNS", "invalid configuration ignored while disabled")
	runtime, err := loadNodeDriverRuntime(nil)
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
	mappingPath := filepath.Join(directory, "mapping.json")
	missingSecretPath := filepath.Join(directory, "must-not-be-read")
	mapping := fmt.Sprintf(`{"provider":"file","references":[{"reference":"file://node/test","path":%q}]}`, missingSecretPath)
	if err := os.WriteFile(mappingPath, []byte(mapping), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONTROL_CLIPROXYAPI_DRIVER_ENABLED", "true")
	t.Setenv("CONTROL_CLIPROXYAPI_MANAGEMENT_DNS", "node.example.invalid")
	t.Setenv("CONTROL_CLIPROXYAPI_MANAGEMENT_CIDRS", "10.42.0.0/16")
	t.Setenv("CONTROL_CLIPROXYAPI_PLAIN_HTTP_CIDRS", "")
	t.Setenv("CONTROL_CLIPROXYAPI_SECRET_MAPPING_FILE", mappingPath)
	t.Setenv("CONTROL_CLIPROXYAPI_CA_FILE", "")

	runtime, err := loadNodeDriverRuntime(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !runtime.enabled || runtime.registry == nil || runtime.metrics == nil {
		t.Fatalf("unexpected enabled runtime: %#v", runtime)
	}
	if _, err = os.Stat(missingSecretPath); !os.IsNotExist(err) {
		t.Fatalf("constructor accessed or created target Secret: %v", err)
	}
	target := controlnodes.NodeTarget{
		InstanceID: uuid.New(), NodeType: controlnodes.NodeTypeCLIProxyAPI,
		DriverContractVersion: controlnodes.DriverContractCLIProxyAPIAuthFilesV1,
		ManagementEndpoint:    "https://node.example.invalid",
		ReaderSecretReference: controlnodes.NewSecretReference("file://node/test"),
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
	t.Setenv("CONTROL_CLIPROXYAPI_DRIVER_ENABLED", "true")
	canary := "management-config-canary.example.invalid"
	t.Setenv("CONTROL_CLIPROXYAPI_MANAGEMENT_DNS", canary)
	t.Setenv("CONTROL_CLIPROXYAPI_MANAGEMENT_CIDRS", "")
	_, err := loadNodeDriverRuntime(nil)
	if err == nil {
		t.Fatal("missing management CIDR accepted")
	}
	if strings.Contains(err.Error(), canary) {
		t.Fatalf("configuration error leaked value: %v", err)
	}

	t.Setenv("CONTROL_CLIPROXYAPI_DRIVER_ENABLED", "not-a-boolean-canary")
	_, err = loadNodeDriverRuntime(nil)
	if err == nil || strings.Contains(err.Error(), "not-a-boolean-canary") {
		t.Fatalf("boolean configuration did not fail safely: %v", err)
	}
}

func TestDisabledNodeDriverRegistryCannotIssueRequests(t *testing.T) {
	t.Setenv("CONTROL_CLIPROXYAPI_DRIVER_ENABLED", "false")
	runtime, err := loadNodeDriverRuntime(nil)
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
