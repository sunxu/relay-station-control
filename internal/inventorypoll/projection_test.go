package inventorypoll

import (
	"encoding/json"
	"errors"
	"fmt"
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
		{Provider: "antigravity", Email: emailCanary, State: drivers.AccountStateActive, OccurrenceCount: 2},
		{Provider: "antigravity", Email: emailCanary, State: drivers.AccountStateError, OccurrenceCount: 2},
		{Provider: "antigravity", Email: "second@example.invalid", State: drivers.AccountStateActive, OccurrenceCount: 1},
	}
	observation.Providers[0].DuplicateIdentityCount = 1
	observation.Providers[0].SnapshotComplete = false
	observation.Providers[0].Degraded = true
	observation.Result = drivers.ResultDegraded

	node, providers, items, duplicates, err := projectObservation(policy, observation)
	if err != nil {
		t.Fatalf("project observation: %v", err)
	}
	if node.RecognizedRecordCount != 3 || node.SnapshotComplete || !node.Degraded ||
		providers[0].RecognizedRecordCount != 3 || providers[0].DuplicateIdentityCount != 1 ||
		providers[0].Reason != ProviderReasonIdentityIncomplete || len(items) != 1 ||
		items[0].AccountKey != "antigravity:second@example.invalid" || len(duplicates) != 1 {
		t.Fatalf("node=%#v providers=%#v item_count=%d duplicate_count=%d", node, providers, len(items), len(duplicates))
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
			node, providers, items, duplicates, err := projectObservation(policy, test.observation)
			if err != nil {
				t.Fatal(err)
			}
			if len(providers) != 2 || providers[0].Reason != test.wantReason || providers[1].Reason != test.wantReason ||
				!providers[0].IdentityComplete || !providers[1].IdentityComplete ||
				providers[0].SnapshotComplete || !providers[0].Degraded || node.SnapshotComplete || !node.Degraded ||
				node.Version != "unknown" || node.Commit != "unknown" || len(items) != 0 || len(duplicates) != 0 {
				t.Fatalf("node=%#v providers=%#v", node, providers)
			}
		})
	}
}

func TestProjectionKeepsProviderIdentityLocalWhenNodeIdentityIsIncomplete(t *testing.T) {
	policy := drivers.ProviderPolicySnapshot{VersionID: uuid.New(), ActiveProviders: []string{"antigravity"}}
	observation := successfulObservation(policy.ActiveProviders)
	observation.NodeIdentityComplete = false
	observation.UnidentifiedRecordCount = 1
	observation.Result = drivers.ResultDegraded
	observation.Providers[0].SnapshotComplete = false
	observation.Providers[0].Degraded = true

	node, providers, items, duplicates, err := projectObservation(policy, observation)
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 1 || !providers[0].IdentityComplete || providers[0].SnapshotComplete ||
		providers[0].Reason != ProviderReasonNodeIdentityIncomplete || !providers[0].Degraded ||
		node.NodeIdentityComplete || node.SnapshotComplete || !node.Degraded ||
		len(items) != 0 || len(duplicates) != 0 {
		t.Fatalf("node=%#v providers=%#v item_count=%d duplicate_count=%d",
			node, providers, len(items), len(duplicates))
	}
}

func TestProjectionRuntimeDiskAndInvalidProviderSets(t *testing.T) {
	policy := drivers.ProviderPolicySnapshot{VersionID: uuid.New(), ActiveProviders: []string{"antigravity", "codex"}}
	runtimeObservation := successfulObservation(policy.ActiveProviders)
	node, providers, _, _, err := projectObservation(policy, runtimeObservation)
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
	node, providers, _, _, err = projectObservation(policy, disk)
	if err != nil || node.SnapshotComplete || !node.Degraded || providers[0].Reason != ProviderReasonDiskFallback {
		t.Fatalf("disk node=%#v providers=%#v err=%v", node, providers, err)
	}

	missing := runtimeObservation
	missing.Providers = missing.Providers[:1]
	if _, _, _, _, err := projectObservation(policy, missing); !errors.Is(err, ErrInvalidObservation) {
		t.Fatalf("missing provider error=%v", err)
	}
	extra := runtimeObservation
	extra.Providers = append(extra.Providers, drivers.ProviderObservation{Provider: "other"})
	if _, _, _, _, err := projectObservation(policy, extra); !errors.Is(err, ErrInvalidObservation) {
		t.Fatalf("extra provider error=%v", err)
	}
	duplicate := runtimeObservation
	duplicate.Providers = append(duplicate.Providers, duplicate.Providers[0])
	if _, _, _, _, err := projectObservation(policy, duplicate); !errors.Is(err, ErrInvalidObservation) {
		t.Fatalf("duplicate provider error=%v", err)
	}
}

func TestProjectionDoesNotApplyNodeWideScopeDegradationToCompleteProviders(t *testing.T) {
	policy := drivers.ProviderPolicySnapshot{
		VersionID: uuid.New(), ActiveProviders: []string{"antigravity", "codex"},
	}
	observation := successfulObservation(policy.ActiveProviders)
	observation.Result = drivers.ResultDegraded
	observation.UnsupportedProviderCount = 1
	for index := range observation.Providers {
		// CLIProxyAPI currently carries its parsed Node-wide degraded bit into
		// each ProviderObservation. SnapshotComplete remains the authoritative
		// Provider-local boundary.
		observation.Providers[index].Degraded = true
	}

	node, providers, items, duplicates, err := projectObservation(policy, observation)
	if err != nil {
		t.Fatal(err)
	}
	if !node.Degraded || !node.SnapshotComplete || node.UnsupportedProviderCount != 1 ||
		len(providers) != 2 || providers[0].Degraded || providers[1].Degraded ||
		providers[0].Reason != ProviderReasonComplete || providers[1].Reason != ProviderReasonComplete ||
		len(items) != 0 || len(duplicates) != 0 {
		t.Fatalf("node=%#v providers=%#v item_count=%d duplicate_count=%d",
			node, providers, len(items), len(duplicates))
	}
}

func TestNormalizeAccountIdentityDeterministicBoundaries(t *testing.T) {
	provider, email, key, err := normalizeAccountIdentity(" Antigravity ", " ÄBC+Tag@例子.测试 ")
	if err != nil || provider != "antigravity" || email != "äbc+tag@例子.测试" || key != "antigravity:äbc+tag@例子.测试" {
		t.Fatalf("provider=%q email=%q key=%q err=%v", provider, email, key, err)
	}
	provider, email, key, err = normalizeAccountIdentity(provider, email)
	if err != nil || key != provider+":"+email {
		t.Fatalf("normalization is not idempotent: provider=%q email=%q key=%q err=%v", provider, email, key, err)
	}

	maximumProvider := "a" + strings.Repeat("b", maximumProviderBytes-1)
	maximumEmail := strings.Repeat("x", maximumEmailBytes)
	if _, _, _, err := normalizeAccountIdentity(maximumProvider, maximumEmail); err != nil {
		t.Fatalf("boundary identity rejected: %v", err)
	}
	invalidUTF8 := string([]byte{'a', '@', 0xff})
	for _, test := range []struct {
		name     string
		provider string
		email    string
	}{
		{"empty provider", " ", "user@example.invalid"},
		{"provider too long", maximumProvider + "c", "user@example.invalid"},
		{"provider control", "anti\ngravity", "user@example.invalid"},
		{"empty email", "antigravity", "  "},
		{"email too long", "antigravity", maximumEmail + "x"},
		{"email control", "antigravity", "user\n@example.invalid"},
		{"email invalid utf8", "antigravity", invalidUTF8},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, _, normalizeErr := normalizeAccountIdentity(test.provider, test.email)
			if !errors.Is(normalizeErr, ErrInvalidObservation) || strings.Contains(normalizeErr.Error(), test.email) {
				t.Fatalf("error=%v", normalizeErr)
			}
		})
	}
}

func TestProjectionBuildsAllowlistedSnapshotCandidates(t *testing.T) {
	policy := drivers.ProviderPolicySnapshot{VersionID: uuid.New(), ActiveProviders: []string{"antigravity", "codex"}}
	observation := successfulObservation(policy.ActiveProviders)
	observation.Providers[0].Accounts = []drivers.AccountObservation{{
		Provider: " Antigravity ", Email: " User@Example.Invalid ", State: drivers.AccountStateActive,
		OccurrenceCount: 1, SuccessCount: maximumSnapshotCounter, FailedCount: maximumSnapshotCounter,
		RecentRequestCount: maximumSnapshotRecords, LastRefreshUnix: minimumSourceUnix,
		NextRetryUnix: maximumSourceUnix,
	}}
	observation.Providers[1].Accounts = []drivers.AccountObservation{{
		Provider: "CODEX", Email: "user@example.invalid", State: drivers.AccountStateDisabled,
		OccurrenceCount: 1, UpdatedAtUnix: 1_777_777_777,
	}}

	node, providers, items, duplicates, err := projectObservation(policy, observation)
	if err != nil {
		t.Fatal(err)
	}
	if !node.SnapshotComplete || node.RecognizedRecordCount != 2 || len(providers) != 2 || len(items) != 2 || len(duplicates) != 0 {
		t.Fatalf("node=%#v providers=%#v item_count=%d duplicate_count=%d", node, providers, len(items), len(duplicates))
	}
	if items[0].AccountKey != "antigravity:user@example.invalid" || items[0].Email != "user@example.invalid" ||
		items[0].BasicStatus != drivers.AccountStateActive || items[0].SuccessCount != maximumSnapshotCounter ||
		items[0].FailedCount != maximumSnapshotCounter || items[0].RecentRequestCount != maximumSnapshotRecords ||
		items[0].LastRefreshUnix == nil || *items[0].LastRefreshUnix != minimumSourceUnix ||
		items[0].NextRetryUnix == nil || *items[0].NextRetryUnix != maximumSourceUnix || items[0].UpdatedAtUnix != nil {
		t.Fatalf("first candidate projection mismatch")
	}
	if items[1].AccountKey != "codex:user@example.invalid" || items[1].Provider != "codex" ||
		items[1].UpdatedAtUnix == nil || *items[1].UpdatedAtUnix != 1_777_777_777 {
		t.Fatalf("second candidate projection mismatch")
	}
}

func TestProjectionPreGroupsDuplicatesAndPromotesOtherProvider(t *testing.T) {
	policy := drivers.ProviderPolicySnapshot{VersionID: uuid.New(), ActiveProviders: []string{"antigravity", "codex"}}
	observation := successfulObservation(policy.ActiveProviders)
	observation.Result = drivers.ResultDegraded
	observation.Providers[0].Degraded = true
	observation.Providers[0].Accounts = []drivers.AccountObservation{
		{Provider: "antigravity", Email: " DUP@example.invalid ", State: drivers.AccountStateActive, OccurrenceCount: 2},
		{Provider: "ANTIGRAVITY", Email: "dup@EXAMPLE.INVALID", State: drivers.AccountStateError, OccurrenceCount: 2},
		{Provider: "antigravity", Email: "unique@example.invalid", State: drivers.AccountStateActive, OccurrenceCount: 1},
	}
	observation.Providers[1].Accounts = []drivers.AccountObservation{{
		Provider: "codex", Email: "dup@example.invalid", State: drivers.AccountStateActive, OccurrenceCount: 1,
	}}

	node, providers, items, duplicates, err := projectObservation(policy, observation)
	if err != nil {
		t.Fatal(err)
	}
	if node.SnapshotComplete || !node.Degraded || node.RecognizedRecordCount != 4 ||
		providers[0].SnapshotComplete || providers[0].IdentityComplete || providers[0].DuplicateIdentityCount != 1 ||
		!providers[1].SnapshotComplete || len(items) != 2 ||
		items[0].AccountKey != "antigravity:unique@example.invalid" || items[1].AccountKey != "codex:dup@example.invalid" ||
		len(duplicates) != 1 || duplicates[0].AccountKey != "antigravity:dup@example.invalid" || duplicates[0].OccurrenceCount != 2 {
		t.Fatalf("node=%#v providers=%#v items=%#v duplicates=%#v", node, providers, items, duplicates)
	}
}

func TestProjectionRejectsInvalidAllowlistFields(t *testing.T) {
	policy := drivers.ProviderPolicySnapshot{VersionID: uuid.New(), ActiveProviders: []string{"antigravity"}}
	base := drivers.AccountObservation{
		Provider: "antigravity", Email: "user@example.invalid", State: drivers.AccountStateActive, OccurrenceCount: 1,
	}
	tests := []struct {
		name   string
		mutate func(*drivers.AccountObservation)
	}{
		{"unknown status", func(account *drivers.AccountObservation) { account.State = "future" }},
		{"zero occurrence", func(account *drivers.AccountObservation) { account.OccurrenceCount = 0 }},
		{"counter overflow", func(account *drivers.AccountObservation) { account.SuccessCount = maximumSnapshotCounter + 1 }},
		{"failed overflow", func(account *drivers.AccountObservation) { account.FailedCount = maximumSnapshotCounter + 1 }},
		{"recent overflow", func(account *drivers.AccountObservation) { account.RecentRequestCount = maximumSnapshotRecords + 1 }},
		{"negative source time", func(account *drivers.AccountObservation) { account.LastRefreshUnix = -1 }},
		{"source time overflow", func(account *drivers.AccountObservation) { account.NextRetryUnix = maximumSourceUnix + 1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			account := base
			test.mutate(&account)
			observation := successfulObservation(policy.ActiveProviders)
			observation.Providers[0].Accounts = []drivers.AccountObservation{account}
			if _, _, _, _, err := projectObservation(policy, observation); !errors.Is(err, ErrInvalidObservation) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestProjectionAcceptsDriverDuplicateShapeAndRejectsMixedOccurrences(t *testing.T) {
	policy := drivers.ProviderPolicySnapshot{VersionID: uuid.New(), ActiveProviders: []string{"antigravity"}}
	observation := successfulObservation(policy.ActiveProviders)
	observation.Result = drivers.ResultDegraded
	observation.Providers[0].Accounts = []drivers.AccountObservation{
		{Provider: "antigravity", Email: "dup@example.invalid", State: drivers.AccountStateActive, OccurrenceCount: 2},
		{Provider: "antigravity", Email: "dup@example.invalid", State: drivers.AccountStateError, OccurrenceCount: 2},
	}
	_, _, _, duplicates, err := projectObservation(policy, observation)
	if err != nil || len(duplicates) != 1 || duplicates[0].OccurrenceCount != 2 {
		t.Fatalf("duplicates=%#v err=%v", duplicates, err)
	}
	observation.Providers[0].Accounts[0].OccurrenceCount = 1
	if _, _, _, _, err := projectObservation(policy, observation); !errors.Is(err, ErrInvalidObservation) {
		t.Fatalf("mixed occurrence error=%v", err)
	}
}

func TestProjectionMissingIdentityAndUnsupportedContentNeverCreatesItem(t *testing.T) {
	policy := drivers.ProviderPolicySnapshot{
		VersionID: uuid.New(), ActiveProviders: []string{"antigravity"}, OutOfScopeProviders: []string{"codex"},
	}
	observation := successfulObservation(policy.ActiveProviders)
	observation.Result = drivers.ResultDegraded
	observation.UnsupportedProviderCount = 1
	observation.OutOfScopeProviderCount = 1
	observation.Providers[0].Accounts = []drivers.AccountObservation{{
		Provider: "antigravity", Email: " ", OccurrenceCount: 1,
	}}
	node, providers, items, duplicates, err := projectObservation(policy, observation)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 || len(duplicates) != 0 || providers[0].SnapshotComplete || providers[0].MissingIdentityCount != 1 ||
		node.UnidentifiedRecordCount != 1 || node.UnsupportedProviderCount != 1 || node.OutOfScopeProviderCount != 1 {
		t.Fatalf("node=%#v providers=%#v item_count=%d duplicate_count=%d", node, providers, len(items), len(duplicates))
	}

	for _, provider := range []string{"codex", "future"} {
		crossProvider := successfulObservation(policy.ActiveProviders)
		crossProvider.Providers[0].Accounts = []drivers.AccountObservation{{
			Provider: provider, Email: "cross@example.invalid", State: drivers.AccountStateActive, OccurrenceCount: 1,
		}}
		if _, _, _, _, crossErr := projectObservation(policy, crossProvider); !errors.Is(crossErr, ErrInvalidObservation) {
			t.Fatalf("cross-provider %q error=%v", provider, crossErr)
		}
	}
}

func TestAccountBearingFormattersAlwaysRedact(t *testing.T) {
	const canary = "sensitive-email-canary@example.invalid"
	unix := int64(1)
	item := SnapshotCandidate{Provider: "antigravity", AccountKey: "antigravity:" + canary, Email: canary, LastRefreshUnix: &unix}
	duplicate := DuplicateEvidence{Provider: "antigravity", AccountKey: "antigravity:" + canary, OccurrenceCount: 2}
	request := FinalizeRequest{SnapshotItems: []SnapshotCandidate{item}, Duplicates: []DuplicateEvidence{duplicate}}
	for _, value := range []any{item, duplicate, request} {
		formatted := fmt.Sprintf("%v|%+v|%#v|%s|%q", value, value, value, value, value)
		if strings.Contains(formatted, canary) || !strings.Contains(formatted, "REDACTED") {
			t.Fatalf("formatter leaked identity")
		}
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
