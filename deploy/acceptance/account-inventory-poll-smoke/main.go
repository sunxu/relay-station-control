package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sunxu/relay-station-control/internal/drivers"
	"github.com/sunxu/relay-station-control/internal/drivers/cliproxyapi"
)

const requestCooldown = 10 * time.Second

var errRealNodeAdapterNotWired = errors.New("real Node poll acceptance adapter is not wired")

type requestAttempt func() bool
type waitAfterRequest func(time.Duration)

func main() {
	if os.Getenv("CONTROL_POLL_SMOKE_MODE") == "container" {
		if err := runContainer(); err == nil {
			return
		}
		fmt.Fprintln(os.Stderr, "account_inventory_poll_smoke=failed reason=fixed_acceptance_failure")
		os.Exit(1)
	}
	// Real Node access remains deliberately unwired. Container mode is fixed to
	// a synthetic empty official image and cannot accept an arbitrary target.
	fmt.Fprintln(os.Stderr, "account_inventory_poll_smoke=failed reason=real_node_adapter_not_wired")
	os.Exit(2)
}

func runContainer() error {
	management := drivers.ManagementConfig{
		AllowedDNSNames:        splitCSV(os.Getenv("CONTROL_POLL_SMOKE_MANAGEMENT_DNS")),
		AllowedManagementCIDRs: splitCSV(os.Getenv("CONTROL_POLL_SMOKE_MANAGEMENT_CIDRS")),
		AllowedPlainHTTPCIDRs:  splitCSV(os.Getenv("CONTROL_POLL_SMOKE_PLAIN_HTTP_CIDRS")),
	}
	resolver, err := drivers.NewFileSecretResolver(drivers.FileSecretResolverConfig{
		MappingFile: os.Getenv("CONTROL_POLL_SMOKE_SECRET_MAPPING_FILE"),
	})
	if err != nil {
		return errRealNodeAdapterNotWired
	}
	driver, err := cliproxyapi.NewDriver(cliproxyapi.DriverConfig{Management: management, SecretResolver: resolver})
	if err != nil {
		return errRealNodeAdapterNotWired
	}
	registry, err := drivers.NewRegistry(drivers.Registration{
		NodeType: drivers.NodeTypeCLIProxyAPI, DriverContractVersion: drivers.DriverContractCLIProxyAPIAuthFilesV1,
		Capabilities: []drivers.Capability{
			drivers.CapabilityManagementHealthRead,
			drivers.CapabilityManagementAccountInventoryRead,
		},
		Driver: driver,
	})
	if err != nil {
		return errRealNodeAdapterNotWired
	}
	policyID, err := uuid.Parse(os.Getenv("CONTROL_POLL_SMOKE_PROVIDER_POLICY_ID"))
	if err != nil || policyID == uuid.Nil {
		return errRealNodeAdapterNotWired
	}
	target := drivers.NodeTarget{
		InstanceID: uuid.New(), NodeType: drivers.NodeTypeCLIProxyAPI,
		DriverContractVersion: drivers.DriverContractCLIProxyAPIAuthFilesV1,
		ManagementEndpoint:    os.Getenv("CONTROL_POLL_SMOKE_NODE_ENDPOINT"),
		ReaderSecretReference: drivers.NewSecretReference(
			os.Getenv("CONTROL_POLL_SMOKE_NODE_SECRET_REFERENCE"),
		),
		Capabilities: []drivers.Capability{drivers.CapabilityManagementAccountInventoryRead},
	}
	policy := drivers.ProviderPolicySnapshot{
		VersionID: policyID, ActiveProviders: splitCSV(os.Getenv("CONTROL_POLL_SMOKE_ACTIVE_PROVIDERS")),
		OutOfScopeProviders: splitCSV(os.Getenv("CONTROL_POLL_SMOKE_OUT_OF_SCOPE_PROVIDERS")),
	}
	var observation drivers.InventoryObservation
	err = runSerial([]requestAttempt{func() bool {
		var requestErr error
		observation, requestErr = registry.ListAccountInventory(context.Background(), drivers.InventoryRequest{
			Target: target, ProviderPolicy: policy,
		})
		return requestErr == nil && (observation.Result == drivers.ResultSuccess || observation.Result == drivers.ResultDegraded)
	}}, time.Sleep)
	if err != nil {
		return err
	}
	accountCount := uint64(observation.UnidentifiedRecordCount) + uint64(observation.UnsupportedProviderCount) + uint64(observation.OutOfScopeProviderCount)
	for _, provider := range observation.Providers {
		accountCount += uint64(len(provider.Accounts))
	}
	fmt.Printf(
		"account_inventory_poll_smoke=success operation=account_inventory result=%s reason=%s mode=%s account_count=%d request_count=1 request_wait_seconds=%d\n",
		observation.Result, observation.Reason, observation.Mode, accountCount, int(requestCooldown/time.Second),
	)
	return nil
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if item := strings.TrimSpace(part); item != "" {
			result = append(result, item)
		}
	}
	return result
}

// runSerial is the only gate a future real-Node adapter may use. It runs no
// concurrent requests and waits after every completed success or failure,
// including the last request, before returning a fixed result.
func runSerial(requests []requestAttempt, wait waitAfterRequest) error {
	if len(requests) < 1 || len(requests) > 2 || wait == nil {
		return errRealNodeAdapterNotWired
	}
	for _, request := range requests {
		if request == nil {
			return errRealNodeAdapterNotWired
		}
		succeeded := safelyAttempt(request)
		wait(requestCooldown)
		if !succeeded {
			return errors.New("fixed poll acceptance request failure")
		}
	}
	return nil
}

func safelyAttempt(request requestAttempt) (succeeded bool) {
	defer func() {
		if recover() != nil {
			succeeded = false
		}
	}()
	return request()
}
