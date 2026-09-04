package store_test

import (
	"context"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	productstore "github.com/sunxu/relay-station-control/internal/store"
)

type fakeCrossNodeDuplicateOwnershipMetricsProvider struct {
	snapshot []productstore.CrossNodeDuplicateOwnershipMetricsSnapshot
	err      error
}

func (provider *fakeCrossNodeDuplicateOwnershipMetricsProvider) CrossNodeDuplicateOwnershipMetricsSnapshot(context.Context) ([]productstore.CrossNodeDuplicateOwnershipMetricsSnapshot, error) {
	return provider.snapshot, provider.err
}

// TestCrossNodeDuplicateOwnershipMetricsCollector covers Phase 6c: the
// collector exposes exactly one metric family with only the four allowed
// low-cardinality labels (environment, conflict_type, status, severity),
// conflict_type/severity are always the fixed constants, and an unhealthy
// provider yields a gather error (invalid metric) rather than a
// stale/fabricated value.
func TestCrossNodeDuplicateOwnershipMetricsCollector(t *testing.T) {
	t.Run("exposes counts with fixed conflict_type/severity labels", func(t *testing.T) {
		provider := &fakeCrossNodeDuplicateOwnershipMetricsProvider{snapshot: []productstore.CrossNodeDuplicateOwnershipMetricsSnapshot{
			{EnvironmentID: "dev", Status: "ACTIVE", OccurrenceCount: 3},
			{EnvironmentID: "dev", Status: "RESOLVED", OccurrenceCount: 7},
		}}
		collector, err := productstore.NewCrossNodeDuplicateOwnershipMetricsCollector(provider)
		if err != nil {
			t.Fatal(err)
		}
		if count := testutil.CollectAndCount(collector); count != 2 {
			t.Fatalf("metric count = %d, want 2", count)
		}
		expected := `
# HELP relay_control_cross_node_duplicate_occurrences Current cross-node duplicate ownership occurrence count by closed status.
# TYPE relay_control_cross_node_duplicate_occurrences gauge
relay_control_cross_node_duplicate_occurrences{conflict_type="cross_node_duplicate_ownership",environment="dev",severity="Critical",status="ACTIVE"} 3
relay_control_cross_node_duplicate_occurrences{conflict_type="cross_node_duplicate_ownership",environment="dev",severity="Critical",status="RESOLVED"} 7
`
		if err := testutil.CollectAndCompare(collector, strings.NewReader(expected)); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("provider failure surfaces as a gather error, not a stale value", func(t *testing.T) {
		provider := &fakeCrossNodeDuplicateOwnershipMetricsProvider{err: context.DeadlineExceeded}
		collector, err := productstore.NewCrossNodeDuplicateOwnershipMetricsCollector(provider)
		if err != nil {
			t.Fatal(err)
		}
		if err := testutil.CollectAndCompare(collector, strings.NewReader("")); err == nil {
			t.Fatal("expected a gather error for an unhealthy provider, got none")
		}
	})

	t.Run("nil provider is rejected", func(t *testing.T) {
		if _, err := productstore.NewCrossNodeDuplicateOwnershipMetricsCollector(nil); err == nil {
			t.Fatal("expected an error for a nil provider")
		}
	})
}

// TestCrossNodeDuplicateOwnershipMetricsRepository proves the Phase 6c
// source query works against real PostgreSQL and groups only by
// (environment_id, status), never by account_key/instance_id.
func TestCrossNodeDuplicateOwnershipMetricsRepository(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	environmentID := "metrics-repo"
	newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
	lifecycle := newCrossNodeDuplicateOwnershipLifecycleRepository(t, database)

	group := newOwnershipNodeGroup(t, ctx, database, "metricsrepo", 2)
	accountKey := fixtureProviderName + ":metrics-repo@example.invalid"
	group.finalize(t, ctx, database, group.nodes[0], []string{"metrics-repo@example.invalid"}, 1, false)
	group.finalize(t, ctx, database, group.nodes[1], []string{"metrics-repo@example.invalid"}, 1, false)
	if _, err := lifecycle.Evaluate(ctx, environmentID, accountKey); err != nil {
		t.Fatal(err)
	}

	repository, err := productstore.NewCrossNodeDuplicateOwnershipMetricsRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := repository.CrossNodeDuplicateOwnershipMetricsSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, row := range snapshot {
		if row.EnvironmentID == environmentID && row.Status == "ACTIVE" {
			found = true
			if row.OccurrenceCount < 1 {
				t.Fatalf("occurrence count = %d, want >= 1", row.OccurrenceCount)
			}
		}
	}
	if !found {
		t.Fatalf("snapshot = %+v, want an ACTIVE row for environment %q", snapshot, environmentID)
	}
}
