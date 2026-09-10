package store_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/sunxu/relay-station-control/internal/store"
)

func TestAccountAvailabilityNotificationConcurrentReconcileAndRecurrence(t *testing.T) {
	f := newAvailabilityFixture(t, 1)
	availabilityNotificationEnvironment(t, f)
	f.repo.SetNotificationDelivery(availabilityNotificationRegistry(t), false)
	f.event(t, 0, "first-1", "first-h1", "token_invalid", f.now.Add(-4*time.Minute))
	f.event(t, 0, "first-2", "first-h2", "token_invalid", f.now.Add(-3*time.Minute))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	start := make(chan struct{})
	results := make(chan error, 2)
	for n := 0; n < 2; n++ {
		go func() {
			<-start
			count, err := f.repo.Reconcile(ctx)
			if err == nil && count != 1 {
				err = fmt.Errorf("processed count=%d", count)
			}
			results <- err
		}()
	}
	close(start)
	for n := 0; n < 2; n++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if occurrences, jobs, events, outbox := availabilityNotificationCounts(t, f); occurrences != 1 || jobs != 1 || events != 1 || outbox != 1 {
		t.Fatalf("concurrent bundle=%d/%d/%d/%d", occurrences, jobs, events, outbox)
	}
	f.event(t, 0, "success", "success-h", "", f.now.Add(-2*time.Minute))
	f.reconcile(t)
	f.event(t, 0, "second-1", "second-h1", "token_invalid", f.now.Add(-time.Minute))
	f.event(t, 0, "second-2", "second-h2", "token_invalid", f.now.Add(-30*time.Second))
	f.reconcile(t)
	f.reconcile(t)
	if occurrences, jobs, events, outbox := availabilityNotificationCounts(t, f); occurrences != 2 || jobs != 3 || events != 3 || outbox != 3 {
		t.Fatalf("recurrence bundle=%d/%d/%d/%d", occurrences, jobs, events, outbox)
	}
	var distinctOccurrences, distinctOperations int
	if err := f.db.owner.QueryRow(ctx, `SELECT count(DISTINCT payload->>'occurrence_id'),count(DISTINCT operation_id) FROM async_jobs`).Scan(&distinctOccurrences, &distinctOperations); err != nil {
		t.Fatal(err)
	}
	if distinctOccurrences != 2 || distinctOperations != 3 {
		t.Fatalf("recurrence identities=%d/%d", distinctOccurrences, distinctOperations)
	}
}

func TestCrossNodeDuplicateNotificationRecurrenceStartsNewChain(t *testing.T) {
	ctx := context.Background()
	db := newIsolatedJobDatabase(t)
	environmentID := "notify-recurrence"
	newCrossNodeDuplicateLifecycleEnvironment(t, ctx, db, environmentID)
	group := newProblemOwnershipNodeGroup(t, ctx, db, "notify-recurrence", 2)
	email := "recurrence@example.invalid"
	for _, node := range group.nodes {
		group.finalize(t, ctx, db, node, []string{email}, 1, false)
	}
	repo := newCrossNodeDuplicateOwnershipLifecycleRepository(t, db)
	repo.SetNotificationDelivery(duplicateNotificationRegistry(t), false)
	type evaluationResult struct {
		value *store.CrossNodeDuplicateOwnershipEvaluation
		err   error
	}
	start := make(chan struct{})
	results := make(chan evaluationResult, 2)
	for n := 0; n < 2; n++ {
		go func() {
			<-start
			value, err := repo.Evaluate(ctx, environmentID, "antigravity:"+email)
			results <- evaluationResult{value, err}
		}()
	}
	close(start)
	var first *store.CrossNodeDuplicateOwnershipEvaluation
	for n := 0; n < 2; n++ {
		result := <-results
		if result.err != nil || result.value == nil {
			t.Fatalf("concurrent evaluation=%+v", result)
		}
		if first != nil && first.OccurrenceID != result.value.OccurrenceID {
			t.Fatal("concurrent evaluations changed occurrence identity")
		}
		first = result.value
	}
	if jobs, events, outbox := durableNotificationCounts(t, ctx, db); jobs != 1 || events != 1 || outbox != 1 {
		t.Fatalf("concurrent create=%d/%d/%d", jobs, events, outbox)
	}
	group.finalize(t, ctx, db, group.nodes[1], nil, 0, false)
	resolved, err := repo.Evaluate(ctx, environmentID, "antigravity:"+email)
	if err != nil || resolved == nil || resolved.Status != "RESOLVED" {
		t.Fatalf("resolve=%+v err=%v", resolved, err)
	}
	group.finalize(t, ctx, db, group.nodes[1], []string{email}, 2, false)
	second, err := repo.Evaluate(ctx, environmentID, "antigravity:"+email)
	if err != nil || second == nil || !second.Created || second.OccurrenceID == first.OccurrenceID {
		t.Fatalf("recurrence=%+v err=%v", second, err)
	}
	if _, err := repo.Evaluate(ctx, environmentID, "antigravity:"+email); err != nil {
		t.Fatal(err)
	}
	if jobs, events, outbox := durableNotificationCounts(t, ctx, db); jobs != 3 || events != 3 || outbox != 3 {
		t.Fatalf("recurrence counts=%d/%d/%d", jobs, events, outbox)
	}
}

func TestAccountAvailabilityNotificationForbiddenTupleAndDiagnosticSilence(t *testing.T) {
	f := newAvailabilityFixture(t, 1)
	availabilityNotificationEnvironment(t, f)
	f.repo.SetNotificationDelivery(availabilityNotificationRegistry(t), false)
	f.clock(t, "file_error", stringPtr("forbidden"), f.now.Add(-time.Minute), f.now.Add(-time.Minute).Truncate(5*time.Minute))
	f.event(t, 0, "forbidden-confirmed", "forbidden-h", "forbidden", f.now.Add(-2*time.Minute))
	f.reconcile(t)
	var kind, reason, severity string
	if err := f.db.owner.QueryRow(context.Background(), `SELECT payload->>'occurrence_type',payload->>'reason',payload->>'severity' FROM async_jobs`).Scan(&kind, &reason, &severity); err != nil {
		t.Fatal(err)
	}
	if kind != "FORBIDDEN" || reason != "forbidden" || severity != "Warning" {
		t.Fatalf("tuple=%s/%s/%s", kind, reason, severity)
	}
	f.clock(t, "file_disabled", nil, f.now.Add(-time.Second), f.now.Add(-time.Second).Truncate(5*time.Minute))
	f.reconcile(t)
	if occurrences, jobs, events, outbox := availabilityNotificationCounts(t, f); occurrences != 1 || jobs != 1 || events != 1 || outbox != 1 {
		t.Fatalf("diagnostic update notified=%d/%d/%d/%d", occurrences, jobs, events, outbox)
	}
}
