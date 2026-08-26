package main

import (
	"strings"
	"testing"
	"time"

	"github.com/sunxu/relay-station-control/internal/drivers"
)

func TestSmokeRateLimitAndNodeCardinalityAreFixed(t *testing.T) {
	if requestWait != 10*time.Second {
		t.Fatalf("request wait = %s", requestWait)
	}
	for _, name := range []string{
		"CONTROL_DRIVER_SMOKE_NODE_1_ENDPOINT", "CONTROL_DRIVER_SMOKE_NODE_1_SECRET_REFERENCE",
		"CONTROL_DRIVER_SMOKE_NODE_2_ENDPOINT", "CONTROL_DRIVER_SMOKE_NODE_2_SECRET_REFERENCE",
	} {
		t.Setenv(name, "")
	}
	if _, err := loadNodes(); err == nil {
		t.Fatal("zero-node smoke configuration accepted")
	}
	t.Setenv("CONTROL_DRIVER_SMOKE_NODE_1_ENDPOINT", "https://node.example.invalid")
	if _, err := loadNodes(); err == nil {
		t.Fatal("endpoint without opaque Secret reference accepted")
	}
	t.Setenv("CONTROL_DRIVER_SMOKE_NODE_1_SECRET_REFERENCE", "file://node/management-key")
	nodes, err := loadNodes()
	if err != nil || len(nodes) != 1 {
		t.Fatalf("one-node configuration = %d, %v", len(nodes), err)
	}
}

func TestSmokeCSVProjectionDoesNotInventValues(t *testing.T) {
	values := splitCSV(" antigravity, legacy-provider ,, ")
	if len(values) != 2 || values[0] != "antigravity" || values[1] != "legacy-provider" {
		t.Fatalf("CSV values = %#v", values)
	}
}

func TestAggregateInventoriesCountsCrossNodeDuplicatesWithoutProjectingIdentity(t *testing.T) {
	duplicateEmail := "duplicate-canary@example.invalid"
	nodeOneOnly := "node-one-canary@example.invalid"
	nodeTwoOnly := "node-two-canary@example.invalid"
	inventories := []drivers.InventoryObservation{
		{
			UnsupportedProviderCount: 1,
			Providers: []drivers.ProviderObservation{{Accounts: []drivers.AccountObservation{
				{Provider: "Antigravity", Email: " " + duplicateEmail + " "},
				{Provider: "antigravity", Email: duplicateEmail}, // same-Node repetition is one cross-Node identity
				{Provider: "antigravity", Email: nodeOneOnly},
			}}},
		},
		{
			OutOfScopeProviderCount: 1,
			UnidentifiedRecordCount: 1,
			Providers: []drivers.ProviderObservation{{Accounts: []drivers.AccountObservation{
				{Provider: " antigravity ", Email: strings.ToUpper(duplicateEmail)},
				{Provider: "antigravity", Email: nodeTwoOnly},
			}}},
		},
	}

	aggregate := aggregateInventories(inventories)
	if aggregate.accountCount != 8 || aggregate.crossNodeDuplicateCount != 1 {
		t.Fatalf("aggregate = %#v", aggregate)
	}
	summary, failed := formatSmokeSummary(2, aggregate)
	if !failed || !strings.Contains(summary, "account_count=8 cross_node_duplicate_count=1") {
		t.Fatalf("duplicate summary = %q, failed=%t", summary, failed)
	}
	for _, forbidden := range []string{duplicateEmail, nodeOneOnly, nodeTwoOnly, "antigravity"} {
		if strings.Contains(strings.ToLower(summary), strings.ToLower(forbidden)) {
			t.Fatalf("summary leaked identity %q: %s", forbidden, summary)
		}
	}
}

func TestAggregateInventoriesNoDuplicateAndSingleNodeSemantics(t *testing.T) {
	nodeOne := drivers.InventoryObservation{Providers: []drivers.ProviderObservation{{Accounts: []drivers.AccountObservation{
		{Provider: "antigravity", Email: "one@example.invalid"},
	}}}}
	nodeTwo := drivers.InventoryObservation{Providers: []drivers.ProviderObservation{{Accounts: []drivers.AccountObservation{
		{Provider: "antigravity", Email: "two@example.invalid"},
	}}}}

	twoNode := aggregateInventories([]drivers.InventoryObservation{nodeOne, nodeTwo})
	if twoNode.accountCount != 2 || twoNode.crossNodeDuplicateCount != 0 {
		t.Fatalf("two-Node aggregate = %#v", twoNode)
	}
	oneNode := aggregateInventories([]drivers.InventoryObservation{nodeOne})
	if oneNode.accountCount != 1 || oneNode.crossNodeDuplicateCount != 0 {
		t.Fatalf("one-Node aggregate = %#v", oneNode)
	}
	twoNodeSummary, failed := formatSmokeSummary(2, twoNode)
	if failed || !strings.Contains(twoNodeSummary, "account_count=2 cross_node_duplicate_count=0") {
		t.Fatalf("two-Node summary = %q, failed=%t", twoNodeSummary, failed)
	}
	summary, failed := formatSmokeSummary(1, oneNode)
	if failed || strings.Contains(summary, "one@example.invalid") || !strings.Contains(summary, "cross_node_duplicate_count=not_applicable") {
		t.Fatalf("unsafe single-Node summary: %s", summary)
	}
}
