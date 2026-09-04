package store_test

import (
	"context"
	"sync"
	"testing"

	productstore "github.com/sunxu/relay-station-control/internal/store"
)

// fakeCrossNodeDuplicateOwnershipAlertObserver records every alert event it
// receives, guarded by a mutex since Evaluate() invokes it after commit and
// tests may exercise it under real concurrency elsewhere.
type fakeCrossNodeDuplicateOwnershipAlertObserver struct {
	mu     sync.Mutex
	events []productstore.CrossNodeDuplicateOwnershipAlertEvent
}

func (observer *fakeCrossNodeDuplicateOwnershipAlertObserver) Observe(_ context.Context, event productstore.CrossNodeDuplicateOwnershipAlertEvent) {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.events = append(observer.events, event)
}

func (observer *fakeCrossNodeDuplicateOwnershipAlertObserver) snapshot() []productstore.CrossNodeDuplicateOwnershipAlertEvent {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	return append([]productstore.CrossNodeDuplicateOwnershipAlertEvent(nil), observer.events...)
}

// TestCrossNodeDuplicateOwnershipAlertObserver covers Phase 6a: an ACTIVE
// duplicate fires exactly one "active" alert on create, a refresh/degrade
// pass that stays ACTIVE fires nothing (same occurrence keeps the same
// alert logical identity, no alert-per-refresh), a resolve fires exactly
// one "resolved" alert, and a reopen after resolve fires a new "active"
// alert for the new occurrence. Severity is always fixed "Critical".
func TestCrossNodeDuplicateOwnershipAlertObserver(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	environmentID := "alert-observer"
	newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
	lifecycle := newCrossNodeDuplicateOwnershipLifecycleRepository(t, database)
	observer := &fakeCrossNodeDuplicateOwnershipAlertObserver{}
	lifecycle.SetAlertObserver(observer)

	group := newOwnershipNodeGroup(t, ctx, database, "alertobserver", 2)
	accountKey := fixtureProviderName + ":alert-observer@example.invalid"
	group.finalize(t, ctx, database, group.nodes[0], []string{"alert-observer@example.invalid"}, 1, false)
	group.finalize(t, ctx, database, group.nodes[1], []string{"alert-observer@example.invalid"}, 1, false)

	created, err := lifecycle.Evaluate(ctx, environmentID, accountKey)
	if err != nil {
		t.Fatal(err)
	}
	if created == nil || !created.Created {
		t.Fatalf("created = %+v, want a fresh ACTIVE occurrence", created)
	}
	events := observer.snapshot()
	if len(events) != 1 || events[0].Transition != productstore.CrossNodeDuplicateOwnershipAlertActive {
		t.Fatalf("events after create = %+v, want exactly one active alert", events)
	}
	if events[0].Severity != "Critical" || events[0].OccurrenceID != created.OccurrenceID ||
		events[0].EnvironmentID != environmentID || events[0].AccountKey != accountKey || events[0].Provider != fixtureProviderName {
		t.Fatalf("active event = %+v, want Critical severity and matching identity", events[0])
	}
	if len(events[0].AffectedNodes) != 2 {
		t.Fatalf("active event affected nodes = %v, want 2", events[0].AffectedNodes)
	}

	// A refresh pass that observes the exact same evidence again must not
	// create a second alert for the same occurrence.
	refreshed, err := lifecycle.Evaluate(ctx, environmentID, accountKey)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed == nil || refreshed.Created || refreshed.Status != "ACTIVE" {
		t.Fatalf("refreshed = %+v, want a non-creating ACTIVE refresh", refreshed)
	}
	if events := observer.snapshot(); len(events) != 1 {
		t.Fatalf("events after refresh = %+v, want still exactly one (no alert-per-refresh)", events)
	}

	// Node B goes fresh-absent: this must resolve the occurrence and fire
	// exactly one resolved alert, carrying the *original* first_seen_at.
	group.finalize(t, ctx, database, group.nodes[1], nil, 0, false)
	resolved, err := lifecycle.Evaluate(ctx, environmentID, accountKey)
	if err != nil {
		t.Fatal(err)
	}
	if resolved == nil || resolved.Status != "RESOLVED" {
		t.Fatalf("resolved = %+v, want Status=RESOLVED", resolved)
	}
	events = observer.snapshot()
	if len(events) != 2 || events[1].Transition != productstore.CrossNodeDuplicateOwnershipAlertResolved {
		t.Fatalf("events after resolve = %+v, want a second resolved alert", events)
	}
	if events[1].OccurrenceID != created.OccurrenceID || events[1].Severity != "Critical" ||
		!events[1].FirstSeenAt.Equal(events[0].FirstSeenAt) {
		t.Fatalf("resolved event = %+v, want same occurrence_id/severity/first_seen_at as the original create", events[1])
	}

	// Reopen: Node B reappears, both owners qualify again -- this must
	// create a brand-new occurrence (new occurrence_id) and fire a new
	// "active" alert, not reuse the resolved one.
	group.finalize(t, ctx, database, group.nodes[1], []string{"alert-observer@example.invalid"}, 1, false)
	reopened, err := lifecycle.Evaluate(ctx, environmentID, accountKey)
	if err != nil {
		t.Fatal(err)
	}
	if reopened == nil || !reopened.Created || reopened.OccurrenceID == created.OccurrenceID {
		t.Fatalf("reopened = %+v, want a brand-new Created=true occurrence distinct from %s", reopened, created.OccurrenceID)
	}
	events = observer.snapshot()
	if len(events) != 3 || events[2].Transition != productstore.CrossNodeDuplicateOwnershipAlertActive || events[2].OccurrenceID != reopened.OccurrenceID {
		t.Fatalf("events after reopen = %+v, want a third active alert for the new occurrence", events)
	}
}

// TestCrossNodeDuplicateOwnershipAlertObserverDefaultIsNoop proves the
// default (never-set) observer is a safe no-op: Evaluate must still
// succeed and behave identically whether or not an observer is installed.
func TestCrossNodeDuplicateOwnershipAlertObserverDefaultIsNoop(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	environmentID := "alert-observer-default"
	newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, environmentID)
	lifecycle := newCrossNodeDuplicateOwnershipLifecycleRepository(t, database)

	group := newOwnershipNodeGroup(t, ctx, database, "alertobserverdefault", 2)
	accountKey := fixtureProviderName + ":alert-observer-default@example.invalid"
	group.finalize(t, ctx, database, group.nodes[0], []string{"alert-observer-default@example.invalid"}, 1, false)
	group.finalize(t, ctx, database, group.nodes[1], []string{"alert-observer-default@example.invalid"}, 1, false)

	created, err := lifecycle.Evaluate(ctx, environmentID, accountKey)
	if err != nil {
		t.Fatal(err)
	}
	if created == nil || !created.Created {
		t.Fatalf("created = %+v, want a fresh ACTIVE occurrence even without an alert observer installed", created)
	}
}
