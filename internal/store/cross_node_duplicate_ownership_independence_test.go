package store_test

import (
	"context"
	"testing"

	productstore "github.com/sunxu/relay-station-control/internal/store"
)

// TestCrossNodeDuplicateOwnershipIndependentFromNodeLocalDuplicates closes
// task 4.9: Node-local duplicate detection (account_inventory_poll_duplicates,
// migrations/00006) and cross-node duplicate ownership (this capability)
// must use fully independent persistence, alert, and metric paths, never
// merged.
//
//   - Persistence: a full cross-node create/alert/metric pass must not
//     write to account_inventory_poll_duplicates (Phase 3 already covers
//     this for plain lifecycle evaluation; this test re-confirms it once
//     alert/metric observers are also wired, since those are new code paths
//     introduced in Phase 6).
//   - Alert: Node-local duplicate detection has no alert mechanism at all
//     anywhere in this codebase (grep for poll_duplicate/PollDuplicate next
//     to metric/prometheus/collector/alert/slog matches nothing) -- so the
//     cross-node alert observer installed here cannot possibly be shared
//     with or triggered by it; this test additionally proves the observer
//     only fires from CrossNodeDuplicateOwnershipLifecycleRepository.Evaluate,
//     never as a side effect of Node-local duplicate persistence.
//   - Metric: Node-local duplicate detection likewise has no Prometheus
//     metric anywhere in this codebase; relay_control_cross_node_duplicate_occurrences
//     (CrossNodeDuplicateOwnershipMetricsRepository) is sourced only from
//     cross_node_duplicate_occurrences and is unaffected by
//     account_inventory_poll_duplicates row counts.
func TestCrossNodeDuplicateOwnershipIndependentFromNodeLocalDuplicates(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	environmentID := "independence-4-9"
	newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
	lifecycle := newCrossNodeDuplicateOwnershipLifecycleRepository(t, database)
	alertObserver := &fakeCrossNodeDuplicateOwnershipAlertObserver{}
	lifecycle.SetAlertObserver(alertObserver)
	metricsRepository, err := productstore.NewCrossNodeDuplicateOwnershipMetricsRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}

	group := newOwnershipNodeGroup(t, ctx, database, "independence49", 2)
	accountKey := fixtureProviderName + ":independence-4-9@example.invalid"
	group.finalize(t, ctx, database, group.nodes[0], []string{"independence-4-9@example.invalid"}, 1, false)
	group.finalize(t, ctx, database, group.nodes[1], []string{"independence-4-9@example.invalid"}, 1, false)

	var nodeLocalDuplicatesBefore int
	if err := database.owner.QueryRow(ctx,
		`SELECT count(*) FROM account_inventory_poll_duplicates`).Scan(&nodeLocalDuplicatesBefore); err != nil {
		t.Fatal(err)
	}
	metricsBefore, err := metricsRepository.CrossNodeDuplicateOwnershipMetricsSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}

	created, err := lifecycle.Evaluate(ctx, environmentID, accountKey)
	if err != nil {
		t.Fatal(err)
	}
	if created == nil || !created.Created {
		t.Fatalf("created = %+v, want a fresh ACTIVE occurrence", created)
	}

	// Alert: exactly one alert fired, from the cross-node lifecycle path.
	events := alertObserver.snapshot()
	if len(events) != 1 || events[0].Transition != productstore.CrossNodeDuplicateOwnershipAlertActive {
		t.Fatalf("events = %+v, want exactly one active alert from the cross-node lifecycle path", events)
	}

	// Metric: the cross-node ACTIVE count for this environment increased
	// by exactly one, sourced only from cross_node_duplicate_occurrences.
	metricsAfter, err := metricsRepository.CrossNodeDuplicateOwnershipMetricsSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if activeDelta(metricsBefore, metricsAfter, environmentID) != 1 {
		t.Fatalf("ACTIVE occurrence count for %q did not increase by exactly one", environmentID)
	}

	// Persistence: Node-local duplicate detection is untouched by the
	// cross-node create + alert + metric pass above.
	var nodeLocalDuplicatesAfter int
	if err := database.owner.QueryRow(ctx,
		`SELECT count(*) FROM account_inventory_poll_duplicates`).Scan(&nodeLocalDuplicatesAfter); err != nil {
		t.Fatal(err)
	}
	if nodeLocalDuplicatesAfter != nodeLocalDuplicatesBefore {
		t.Fatal("cross-node duplicate create/alert/metric pass must not write to account_inventory_poll_duplicates")
	}
}

func activeCount(snapshot []productstore.CrossNodeDuplicateOwnershipMetricsSnapshot, environmentID string) int64 {
	var total int64
	for _, row := range snapshot {
		if row.EnvironmentID == environmentID && row.Status == "ACTIVE" {
			total += row.OccurrenceCount
		}
	}
	return total
}

func activeDelta(before, after []productstore.CrossNodeDuplicateOwnershipMetricsSnapshot, environmentID string) int64 {
	return activeCount(after, environmentID) - activeCount(before, environmentID)
}
