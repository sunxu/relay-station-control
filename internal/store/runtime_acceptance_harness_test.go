package store_test

import (
	"context"
	"testing"
	"time"
)

// TestRuntimeAcceptanceLifecycleFixture is intentionally test-only. It reuses
// the existing store_test fixtures and reaches notification persistence only
// through production Reconcile/Evaluate paths. It never starts a worker.
func TestRuntimeAcceptanceLifecycleFixture(t *testing.T) {
	ctx := context.Background()

	t.Run("availability active resolved", func(t *testing.T) {
		fixture := newAvailabilityFixture(t, 1)
		availabilityNotificationEnvironment(t, fixture)
		registry := availabilityNotificationRegistry(t)
		fixture.repo.SetNotificationDelivery(registry, false)
		before := fixture.now.Add(-3 * time.Minute)
		fixture.event(t, 0, "harness-failure-1", "harness-failure-1", "token_invalid", before)
		fixture.event(t, 0, "harness-failure-2", "harness-failure-2", "token_invalid", before.Add(time.Second))
		fixture.reconcile(t)
		active := fixture.occurrences(t, 0, "ACTIVE")
		if len(active) != 1 {
			t.Fatalf("active occurrences=%d, want 1", len(active))
		}
		occurrenceID := active[0].OccurrenceID
		fixture.event(t, 0, "harness-success", "harness-success", "", before.Add(2*time.Minute))
		fixture.reconcile(t)
		if len(fixture.occurrences(t, 0, "ACTIVE")) != 0 || len(fixture.occurrences(t, 0, "RESOLVED")) != 1 {
			t.Fatal("availability transition did not resolve")
		}
		resolved := fixture.occurrences(t, 0, "RESOLVED")
		if resolved[0].OccurrenceID != occurrenceID {
			t.Fatal("availability resolution created a new occurrence")
		}
		fixture.reconcile(t)
		_, jobs, events, outbox := availabilityNotificationCounts(t, fixture)
		if jobs != 2 || events != 2 || outbox != 2 {
			t.Fatalf("durable bundle counts jobs=%d events=%d outbox=%d, want exactly two durable bundles", jobs, events, outbox)
		}
	})

	t.Run("duplicate active resolved", func(t *testing.T) {
		database := newIsolatedJobDatabase(t)
		newCrossNodeDuplicateLifecycleEnvironment(t, ctx, database, "runtime-acceptance-harness")
		repository := newCrossNodeDuplicateOwnershipLifecycleRepository(t, database)
		registry := availabilityNotificationRegistry(t)
		repository.SetNotificationDelivery(registry, false)
		group := newProblemOwnershipNodeGroup(t, ctx, database, "runtime-acceptance-harness", 2)
		accountKey := "antigravity:account000@example.invalid"
		group.finalize(t, ctx, database, group.nodes[0], []string{"account000@example.invalid"}, 1, false)
		group.finalize(t, ctx, database, group.nodes[1], []string{"account000@example.invalid"}, 1, false)
		active, err := repository.Evaluate(ctx, "runtime-acceptance-harness", accountKey)
		if err != nil || active == nil || !active.Created || active.Status != "ACTIVE" {
			t.Fatalf("duplicate active result=%+v err=%v", active, err)
		}
		occurrenceID := active.OccurrenceID
		group.finalize(t, ctx, database, group.nodes[1], nil, 0, false)
		resolved, err := repository.Evaluate(ctx, "runtime-acceptance-harness", accountKey)
		if err != nil || resolved == nil || resolved.OccurrenceID != occurrenceID || resolved.Status != "RESOLVED" {
			t.Fatalf("duplicate resolved result=%+v err=%v", resolved, err)
		}
		if _, err = repository.Evaluate(ctx, "runtime-acceptance-harness", accountKey); err != nil {
			t.Fatal(err)
		}
		var jobs int
		if err = database.owner.QueryRow(ctx, `SELECT count(*) FROM async_jobs WHERE job_kind='dingtalk_alert_delivery'`).Scan(&jobs); err != nil {
			t.Fatal(err)
		}
		if jobs != 2 {
			t.Fatalf("duplicate durable jobs=%d, want 2", jobs)
		}
	})
}
