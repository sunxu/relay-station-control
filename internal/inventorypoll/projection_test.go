package inventorypoll

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/sunxu/relay-station-control/internal/drivers"
)

func TestProjectionPersistsOnlyAggregateAllowlist(t *testing.T) {
	const emailCanary = "sensitive-email-canary@example.invalid"
	policy := drivers.ProviderPolicySnapshot{VersionID: uuid.New(), ActiveProviders: []string{"antigravity", "codex"}}
	observation := successfulObservation(policy.ActiveProviders)
	observation.Providers[0].Accounts = []drivers.AccountObservation{
		{Provider: "antigravity", Email: emailCanary, OccurrenceCount: 2},
		{Provider: "antigravity", Email: "second@example.invalid", OccurrenceCount: 1},
	}
	observation.Providers[0].DuplicateIdentityCount = 1
	observation.Providers[0].SnapshotComplete = false
	observation.Providers[0].Degraded = true
	observation.Result = drivers.ResultDegraded

	node, providers, err := projectObservation(policy, observation)
	if err != nil {
		t.Fatalf("project observation: %v", err)
	}
	if node.RecognizedRecordCount != 2 || node.SnapshotComplete || !node.Degraded ||
		providers[0].RecognizedRecordCount != 2 || providers[0].DuplicateIdentityCount != 1 ||
		providers[0].Reason != ProviderReasonIdentityIncomplete {
		t.Fatalf("node=%#v providers=%#v", node, providers)
	}
	encoded, err := json.Marshal(struct {
		Node      NodeEvidence
		Providers []ProviderEvidence
	}{node, providers})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{emailCanary, "second@example.invalid", "Accounts", "Email", "Endpoint", "Secret"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("persistence projection contains %q: %s", forbidden, encoded)
		}
	}
}

func TestProjectionFillsPinnedProvidersForNodeFailures(t *testing.T) {
	policy := drivers.ProviderPolicySnapshot{VersionID: uuid.New(), ActiveProviders: []string{"antigravity", "codex"}}
	for _, test := range []struct {
		name        string
		observation drivers.InventoryObservation
		wantReason  ProviderReason
	}{
		{"transport", drivers.InventoryObservation{Result: drivers.ResultFailed, Reason: drivers.ReasonNetworkUnavailable}, ProviderReasonTransportFailed},
		{"contract", drivers.InventoryObservation{TransportSuccess: true, Result: drivers.ResultFailed, Reason: drivers.ReasonContractInvalid, Version: "unknown", Commit: "unknown"}, ProviderReasonContractInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			node, providers, err := projectObservation(policy, test.observation)
			if err != nil {
				t.Fatal(err)
			}
			if len(providers) != 2 || providers[0].Reason != test.wantReason || providers[1].Reason != test.wantReason ||
				providers[0].SnapshotComplete || !providers[0].Degraded || node.SnapshotComplete || !node.Degraded ||
				node.Version != "unknown" || node.Commit != "unknown" {
				t.Fatalf("node=%#v providers=%#v", node, providers)
			}
		})
	}
}

func TestProjectionRuntimeDiskAndInvalidProviderSets(t *testing.T) {
	policy := drivers.ProviderPolicySnapshot{VersionID: uuid.New(), ActiveProviders: []string{"antigravity", "codex"}}
	runtimeObservation := successfulObservation(policy.ActiveProviders)
	node, providers, err := projectObservation(policy, runtimeObservation)
	if err != nil || !node.SnapshotComplete || node.Degraded || len(providers) != 2 || providers[0].Reason != ProviderReasonComplete {
		t.Fatalf("runtime node=%#v providers=%#v err=%v", node, providers, err)
	}

	disk := runtimeObservation
	disk.Mode = drivers.InventoryModeDiskFallback
	disk.Result = drivers.ResultDegraded
	for index := range disk.Providers {
		disk.Providers[index].SnapshotComplete = false
		disk.Providers[index].Degraded = true
	}
	node, providers, err = projectObservation(policy, disk)
	if err != nil || node.SnapshotComplete || !node.Degraded || providers[0].Reason != ProviderReasonDiskFallback {
		t.Fatalf("disk node=%#v providers=%#v err=%v", node, providers, err)
	}

	missing := runtimeObservation
	missing.Providers = missing.Providers[:1]
	if _, _, err := projectObservation(policy, missing); !errors.Is(err, ErrInvalidObservation) {
		t.Fatalf("missing provider error=%v", err)
	}
	extra := runtimeObservation
	extra.Providers = append(extra.Providers, drivers.ProviderObservation{Provider: "other"})
	if _, _, err := projectObservation(policy, extra); !errors.Is(err, ErrInvalidObservation) {
		t.Fatalf("extra provider error=%v", err)
	}
	duplicate := runtimeObservation
	duplicate.Providers = append(duplicate.Providers, duplicate.Providers[0])
	if _, _, err := projectObservation(policy, duplicate); !errors.Is(err, ErrInvalidObservation) {
		t.Fatalf("duplicate provider error=%v", err)
	}
}

func successfulObservation(providers []string) drivers.InventoryObservation {
	observation := drivers.InventoryObservation{
		TransportSuccess: true, ResponseShapeValid: true, ContractValid: true,
		Mode: drivers.InventoryModeRuntime, Version: "v7.2.141", Commit: "abcdef1",
		NodeIdentityComplete: true, Result: drivers.ResultSuccess, Reason: drivers.ReasonNone,
	}
	for _, provider := range providers {
		observation.Providers = append(observation.Providers, drivers.ProviderObservation{
			Provider: provider, SnapshotComplete: true,
		})
	}
	return observation
}
