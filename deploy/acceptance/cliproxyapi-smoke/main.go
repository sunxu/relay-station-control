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

const requestWait = 10 * time.Second

var errSummaryReported = errors.New("smoke_summary_reported")

type smokeNode struct {
	endpoint        string
	secretReference drivers.SecretReference
}

type accountIdentity struct {
	provider string
	email    string
}

type smokeAggregate struct {
	accountCount            uint64
	crossNodeDuplicateCount uint64
}

func main() {
	if err := run(); err != nil {
		if !errors.Is(err, errSummaryReported) {
			fmt.Fprintln(os.Stderr, "cliproxyapi_readonly_smoke=failed reason=fixed_acceptance_failure")
		}
		os.Exit(1)
	}
}

func run() error {
	management := drivers.ManagementConfig{
		AllowedDNSNames:        splitCSV(os.Getenv("CONTROL_DRIVER_SMOKE_MANAGEMENT_DNS")),
		AllowedManagementCIDRs: splitCSV(os.Getenv("CONTROL_DRIVER_SMOKE_MANAGEMENT_CIDRS")),
		AllowedPlainHTTPCIDRs:  splitCSV(os.Getenv("CONTROL_DRIVER_SMOKE_PLAIN_HTTP_CIDRS")),
	}
	secretResolver, err := drivers.NewFileSecretResolver(drivers.FileSecretResolverConfig{
		MappingFile: os.Getenv("CONTROL_DRIVER_SMOKE_SECRET_MAPPING_FILE"),
	})
	if err != nil {
		return errors.New("configuration_invalid")
	}
	rootCAs, err := cliproxyapi.LoadRootCAs(os.Getenv("CONTROL_DRIVER_SMOKE_CA_FILE"))
	if err != nil {
		return errors.New("configuration_invalid")
	}
	driver, err := cliproxyapi.NewDriver(cliproxyapi.DriverConfig{
		Management: management, SecretResolver: secretResolver, RootCAs: rootCAs,
	})
	if err != nil {
		return errors.New("configuration_invalid")
	}
	registry, err := drivers.NewRegistry(drivers.Registration{
		NodeType:              drivers.NodeTypeCLIProxyAPI,
		DriverContractVersion: drivers.DriverContractCLIProxyAPIAuthFilesV1,
		Capabilities: []drivers.Capability{
			drivers.CapabilityManagementHealthRead,
			drivers.CapabilityManagementAccountInventoryRead,
		},
		Driver: driver,
	})
	if err != nil {
		return errors.New("configuration_invalid")
	}
	policyVersion, err := uuid.Parse(os.Getenv("CONTROL_DRIVER_SMOKE_PROVIDER_POLICY_ID"))
	if err != nil || policyVersion == uuid.Nil {
		return errors.New("configuration_invalid")
	}
	policy := drivers.ProviderPolicySnapshot{
		VersionID:           policyVersion,
		ActiveProviders:     splitCSV(os.Getenv("CONTROL_DRIVER_SMOKE_ACTIVE_PROVIDERS")),
		OutOfScopeProviders: splitCSV(os.Getenv("CONTROL_DRIVER_SMOKE_OUT_OF_SCOPE_PROVIDERS")),
	}
	nodes, err := loadNodes()
	if err != nil {
		return err
	}
	inventories := make([]drivers.InventoryObservation, 0, len(nodes))
	for index, node := range nodes {
		var inventory drivers.InventoryObservation
		if inventory, err = smokeOneNode(context.Background(), registry, index+1, node, policy); err != nil {
			return err
		}
		inventories = append(inventories, inventory)
	}
	aggregate := aggregateInventories(inventories)
	summary, duplicateFailure := formatSmokeSummary(len(nodes), aggregate)
	fmt.Println(summary)
	if duplicateFailure {
		return errSummaryReported
	}
	return nil
}

func loadNodes() ([]smokeNode, error) {
	nodes := make([]smokeNode, 0, 2)
	for ordinal := 1; ordinal <= 2; ordinal++ {
		endpoint := os.Getenv(fmt.Sprintf("CONTROL_DRIVER_SMOKE_NODE_%d_ENDPOINT", ordinal))
		reference := os.Getenv(fmt.Sprintf("CONTROL_DRIVER_SMOKE_NODE_%d_SECRET_REFERENCE", ordinal))
		if endpoint == "" && reference == "" {
			continue
		}
		if endpoint == "" || reference == "" {
			return nil, errors.New("configuration_invalid")
		}
		nodes = append(nodes, smokeNode{endpoint: endpoint, secretReference: drivers.NewSecretReference(reference)})
	}
	if len(nodes) == 0 || len(nodes) > 2 {
		return nil, errors.New("configuration_invalid")
	}
	return nodes, nil
}

func smokeOneNode(ctx context.Context, registry *drivers.Registry, ordinal int, node smokeNode, policy drivers.ProviderPolicySnapshot) (drivers.InventoryObservation, error) {
	target := drivers.NodeTarget{
		InstanceID: uuid.New(), NodeType: drivers.NodeTypeCLIProxyAPI,
		DriverContractVersion: drivers.DriverContractCLIProxyAPIAuthFilesV1,
		ManagementEndpoint:    node.endpoint,
		ReaderSecretReference: node.secretReference,
		Capabilities: []drivers.Capability{
			drivers.CapabilityManagementHealthRead,
			drivers.CapabilityManagementAccountInventoryRead,
		},
	}
	probe, probeErr := registry.Probe(ctx, drivers.ProbeRequest{Target: target})
	fmt.Printf("node=%d operation=probe result=%s reason=%s\n", ordinal, probe.Result, probe.Reason)
	time.Sleep(requestWait)
	if probeErr != nil || probe.Result != drivers.ResultSuccess {
		return drivers.InventoryObservation{}, errors.New("probe_failed")
	}

	inventory, inventoryErr := registry.ListAccountInventory(ctx, drivers.InventoryRequest{Target: target, ProviderPolicy: policy})
	fmt.Printf(
		"node=%d operation=account_inventory result=%s reason=%s mode=%s\n",
		ordinal, inventory.Result, inventory.Reason, inventory.Mode,
	)
	time.Sleep(requestWait)
	if inventoryErr != nil || (inventory.Result != drivers.ResultSuccess && inventory.Result != drivers.ResultDegraded) {
		return drivers.InventoryObservation{}, errors.New("inventory_failed")
	}
	return inventory, nil
}

// aggregateInventories keeps normalized identities only in process memory. An
// identity is a cross-Node duplicate when it appears in at least two distinct
// Node observations; repeated records inside one Node still count only once
// for this comparison.
func aggregateInventories(inventories []drivers.InventoryObservation) smokeAggregate {
	aggregate := smokeAggregate{}
	nodeOccurrences := make(map[accountIdentity]uint8)
	for _, inventory := range inventories {
		aggregate.accountCount += uint64(inventory.UnsupportedProviderCount) +
			uint64(inventory.OutOfScopeProviderCount) + uint64(inventory.UnidentifiedRecordCount)
		nodeIdentities := make(map[accountIdentity]struct{})
		for _, provider := range inventory.Providers {
			aggregate.accountCount += uint64(len(provider.Accounts))
			for _, account := range provider.Accounts {
				identity := accountIdentity{
					provider: strings.ToLower(strings.TrimSpace(account.Provider)),
					email:    strings.ToLower(strings.TrimSpace(account.Email)),
				}
				if identity.provider == "" || identity.email == "" {
					continue
				}
				nodeIdentities[identity] = struct{}{}
			}
		}
		for identity := range nodeIdentities {
			nodeOccurrences[identity]++
		}
	}
	for _, count := range nodeOccurrences {
		if count > 1 {
			aggregate.crossNodeDuplicateCount++
		}
	}
	return aggregate
}

func formatSmokeSummary(nodeCount int, aggregate smokeAggregate) (string, bool) {
	if nodeCount == 1 {
		return fmt.Sprintf(
			"cliproxyapi_readonly_smoke=success node_count=1 account_count=%d cross_node_duplicate_count=not_applicable request_wait_seconds=%d",
			aggregate.accountCount, int(requestWait/time.Second),
		), false
	}
	if aggregate.crossNodeDuplicateCount != 0 {
		return fmt.Sprintf(
			"cliproxyapi_readonly_smoke=failed reason=cross_node_duplicate node_count=%d account_count=%d cross_node_duplicate_count=%d request_wait_seconds=%d",
			nodeCount, aggregate.accountCount, aggregate.crossNodeDuplicateCount, int(requestWait/time.Second),
		), true
	}
	return fmt.Sprintf(
		"cliproxyapi_readonly_smoke=success node_count=%d account_count=%d cross_node_duplicate_count=0 request_wait_seconds=%d",
		nodeCount, aggregate.accountCount, int(requestWait/time.Second),
	), false
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
