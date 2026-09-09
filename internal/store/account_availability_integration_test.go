package store_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sunxu/relay-station-control/internal/requestquality"
	store "github.com/sunxu/relay-station-control/internal/store"
)

type availabilityFixture struct {
	db     *isolatedJobDatabase
	node   uuid.UUID
	keys   []string
	repo   *store.AccountAvailabilityRepository
	events *store.AccountRequestQualityRepository
	now    time.Time
}

func newAvailabilityFixture(t *testing.T, n int) *availabilityFixture {
	t.Helper()
	ctx := context.Background()
	db := newIsolatedJobDatabase(t)
	src := insertSnapshotPollFixtureWithProviders(t, ctx, db, []string{"antigravity"})
	token := uuid.New()
	setAvailabilityPollRunning(t, ctx, db, src.pollRunID, token)
	repo, err := store.NewAccountAvailabilityRepository(db.runtime)
	if err != nil {
		t.Fatal(err)
	}
	events, err := store.NewAccountRequestQualityRepository(db.runtime)
	if err != nil {
		t.Fatal(err)
	}
	f := &availabilityFixture{db: db, node: src.instanceID, repo: repo, events: events}
	evidence := "file_active"
	request := availabilityFinalizeRequest(src, token, "unused@example.invalid", &evidence, nil)
	request.SnapshotItems = nil
	request.Node.RecognizedRecordCount = uint32(n)
	request.Providers[0].RecognizedRecordCount = uint32(n)
	for i := 0; i < n; i++ {
		email := fmt.Sprintf("account%03d@example.invalid", i)
		f.keys = append(f.keys, "antigravity:"+email)
		item := availabilityFinalizeRequest(src, token, email, &evidence, nil).SnapshotItems[0]
		request.SnapshotItems = append(request.SnapshotItems, item)
	}
	inventory, err := store.NewInventoryPollRepository(db.runtime)
	if err != nil {
		t.Fatal(err)
	}
	if err = inventory.FinalizeFenced(ctx, request); err != nil {
		t.Fatal(err)
	}
	if _, err = db.owner.Exec(ctx, `INSERT INTO relay_node_inventory_monitoring_activations(instance_id,effective_from,created_at,reason,actor) VALUES($1,clock_timestamp()-interval '1 hour',clock_timestamp()-interval '2 hours','reconciliation','availability-test')`, f.node); err != nil {
		t.Fatal(err)
	}
	if err = db.owner.QueryRow(ctx, `SELECT statement_timestamp()`).Scan(&f.now); err != nil {
		t.Fatal(err)
	}
	// Clock fixtures are edited only via the isolated test owner. Production
	// evaluation and reads always use the restricted runtime role and real SQL.
	f.clock(t, "file_active", nil, f.now.Add(-10*time.Minute), f.now.Add(-10*time.Minute).Truncate(5*time.Minute))
	return f
}
func (f *availabilityFixture) clock(t *testing.T, evidence string, reason *string, observed, slot time.Time) {
	t.Helper()
	ctx := context.Background()
	tx, err := f.db.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `ALTER TABLE account_inventory DISABLE TRIGGER USER; ALTER TABLE account_inventory_provider_states DISABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE account_inventory SET current_poll_run_id=NULL,availability_runtime_evidence=$2,auth_failure_reason=$3,current_scheduled_at=$4,source_observed_at=$5,last_seen_at=$5,first_seen_at=least(first_seen_at,$5),updated_at=greatest(updated_at,$5) WHERE instance_id=$1`, f.node, evidence, reason, slot, observed); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE account_inventory_provider_states SET current_poll_run_id=NULL,current_scheduled_at=$2,health_scheduled_at=$2,last_complete_at=$3,source_observed_at=$3,health_degraded=false,health_reason='none',updated_at=greatest(updated_at,$3) WHERE instance_id=$1`, f.node, slot, observed); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `ALTER TABLE account_inventory ENABLE TRIGGER USER; ALTER TABLE account_inventory_provider_states ENABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}
func (f *availabilityFixture) event(t *testing.T, index int, id, hash, reason string, at time.Time) {
	t.Helper()
	e := requestquality.Event{NodeID: f.node, Provider: "antigravity", RequestID: id, EventHash: hash, OccurredAt: at, Success: reason == ""}
	if index >= 0 {
		e.AccountKey = &f.keys[index]
	}
	if reason != "" {
		e.FailureClass = stringPtr("auth")
		e.AuthFailureReason = &reason
	}
	if err := f.events.InsertRequestEvents(context.Background(), []requestquality.Event{e}); err != nil {
		t.Fatal(err)
	}
}
func (f *availabilityFixture) reconcile(t *testing.T) {
	t.Helper()
	if n, err := f.repo.Reconcile(context.Background()); err != nil || n != len(f.keys) {
		t.Fatalf("reconcile count=%d err=%v", n, err)
	}
}
func (f *availabilityFixture) state(t *testing.T, index int, want string) {
	t.Helper()
	values, err := f.repo.BatchAccountAvailability(context.Background(), f.node, []string{f.keys[index]})
	if err != nil {
		t.Fatal(err)
	}
	if values[f.keys[index]].State != want {
		t.Fatalf("account %d state=%+v want=%s", index, values[f.keys[index]], want)
	}
}
func (f *availabilityFixture) occurrences(t *testing.T, index int, status string) []store.AccountAvailabilityOccurrence {
	t.Helper()
	q := store.AccountAvailabilityOccurrenceQuery{InstanceID: f.node, AccountKey: f.keys[index], Status: status, Limit: 100}
	p, err := f.repo.ListAccountAvailabilityOccurrences(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	return p.Items
}

func TestAccountAvailabilityConfirmationPostgres(t *testing.T) {
	f := newAvailabilityFixture(t, 12)
	before := f.now.Add(-3 * time.Minute)
	f.event(t, 0, "same", "same-h1", "token_invalid", before)
	f.event(t, 0, "same", "same-h2", "token_invalid", before.Add(time.Second))
	f.event(t, 1, "r1", "two-h1", "token_invalid", before)
	f.event(t, 1, "r2", "two-h2", "token_invalid", before.Add(time.Second))
	f.event(t, 2, "", "noid-h1", "token_invalid", before)
	f.event(t, 2, "", "noid-h2", "token_invalid", before.Add(time.Second))
	f.event(t, 3, "f1", "403-h1", "forbidden", before)
	f.event(t, 3, "f2", "403-h2", "forbidden", before.Add(time.Second))
	f.event(t, 4, "b1", "block-h1", "account_blocked", before)
	f.event(t, 4, "b2", "block-h2", "account_blocked", before.Add(time.Second))
	f.event(t, 5, "o1", "other-h1", "other", before)
	f.event(t, 5, "o2", "other-h2", "other", before.Add(time.Second))
	f.event(t, 6, "conflict", "conflict-h1", "token_invalid", before)
	f.event(t, 6, "conflict", "conflict-h2", "account_blocked", before.Add(time.Second))
	f.event(t, -1, "u1", "null-h1", "token_invalid", before)
	f.event(t, -1, "u2", "null-h2", "token_invalid", before)
	f.event(t, 8, "old1", "old-h1", "token_invalid", f.now.Add(-16*time.Minute))
	f.event(t, 8, "old2", "old-h2", "token_invalid", f.now.Add(-16*time.Minute))
	f.event(t, 9, "future1", "future-h1", "token_invalid", f.now.Add(time.Hour))
	f.event(t, 9, "future2", "future-h2", "token_invalid", f.now.Add(time.Hour))
	f.event(t, 10, "s1", "equal-f1", "token_invalid", before)
	f.event(t, 10, "s2", "equal-f2", "token_invalid", before)
	f.event(t, 10, "success", "equal-s", "", before)
	f.reconcile(t)
	for i, want := range []string{"UNKNOWN", "TOKEN_INVALID", "UNKNOWN", "UNKNOWN", "ACCOUNT_BLOCKED", "AVAILABLE", "UNKNOWN", "AVAILABLE", "AVAILABLE", "AVAILABLE", "UNKNOWN", "AVAILABLE"} {
		f.state(t, i, want)
	}
	if len(f.occurrences(t, 0, "ACTIVE")) != 0 || len(f.occurrences(t, 3, "ACTIVE")) != 0 {
		t.Fatal("hashes or plain403 confirmed a fault")
	}
	for _, i := range []int{1, 4} {
		items := f.occurrences(t, i, "ACTIVE")
		if len(items) != 1 || items[0].Severity != "Critical" {
			t.Fatalf("occurrence=%+v", items)
		}
	}
	first := f.occurrences(t, 1, "ACTIVE")[0]
	for i := 0; i < 100; i++ {
		f.reconcile(t)
	}
	if after := f.occurrences(t, 1, "ACTIVE"); len(after) != 1 || after[0].OccurrenceID != first.OccurrenceID || !after[0].ConfirmedAt.Equal(first.ConfirmedAt) {
		t.Fatal("repeat reconciliation changed occurrence")
	}
	// Missing IDs can only confirm with fresh account-level runtime evidence.
	f.clock(t, "file_unavailable", nil, f.now.Add(-time.Minute), f.now.Truncate(5*time.Minute))
	f.reconcile(t)
	f.state(t, 2, "TOKEN_INVALID")
	f.state(t, 3, "FORBIDDEN")
	f.state(t, 5, "UNKNOWN")
	f.state(t, 7, "UNKNOWN")
	if items := f.occurrences(t, 3, "ACTIVE"); len(items) != 1 || items[0].Severity != "Warning" {
		t.Fatalf("403 cross evidence=%+v", items)
	}
	if len(f.occurrences(t, 5, "ACTIVE")) != 0 || len(f.occurrences(t, 7, "ACTIVE")) != 0 {
		t.Fatal("other/runtime-only created occurrence")
	}
	// Distinct source IDs with explicit runtime code and no request never confirm.
	blocked := "account_blocked"
	f.clock(t, "file_error", &blocked, f.now.Add(-30*time.Second), f.now.Truncate(5*time.Minute))
	f.reconcile(t)
	if len(f.occurrences(t, 7, "ACTIVE")) != 0 {
		t.Fatal("runtime-only created occurrence")
	}
}

func TestAccountAvailabilityFailuresMustFollowSuccessWatermarkPostgres(t *testing.T) {
	fa := newAvailabilityFixture(t, 1)
	t0 := fa.now.Add(-3 * time.Minute)
	// Case A: the failure at the success timestamp is discarded; only one
	// strictly-later request remains, which is insufficient to confirm.
	fa.event(t, 0, "success-a", "success-a", "", t0)
	fa.event(t, 0, "failure-a", "failure-a", "token_invalid", t0)
	fa.event(t, 0, "failure-b", "failure-b", "token_invalid", t0.Add(time.Second))
	fa.reconcile(t)

	// Case B: an equal-time no-request-id failure cannot pair with a later
	// runtime error because the request evidence is not strictly after success.
	fb := newAvailabilityFixture(t, 1)
	t0 = fb.now.Add(-3 * time.Minute)
	fb.event(t, 0, "success-b", "success-b", "", t0)
	fb.event(t, 0, "", "failure-no-id", "token_invalid", t0)
	fb.clock(t, "file_error", nil, t0.Add(time.Second), t0.Truncate(5*time.Minute))
	fb.reconcile(t)

	// Case C: two distinct request IDs strictly after success still confirm.
	fc := newAvailabilityFixture(t, 1)
	t0 = fc.now.Add(-3 * time.Minute)
	fc.event(t, 0, "success-c", "success-c", "", t0)
	fc.event(t, 0, "failure-c1", "failure-c1", "token_invalid", t0.Add(time.Second))
	fc.event(t, 0, "failure-c2", "failure-c2", "token_invalid", t0.Add(2*time.Second))
	fc.reconcile(t)

	if got := fa.occurrences(t, 0, "ACTIVE"); len(got) != 0 {
		t.Fatalf("equal-time failure contributed confirmation: %+v", got)
	}
	if got := fb.occurrences(t, 0, "ACTIVE"); len(got) != 0 {
		t.Fatalf("equal-time no-id failure contributed cross confirmation: %+v", got)
	}
	if got := fc.occurrences(t, 0, "ACTIVE"); len(got) != 1 || got[0].Reason != "token_invalid" {
		t.Fatalf("strictly-later failures did not confirm: %+v", got)
	}
}

func TestAccountAvailabilityRecoveryRestartAndRetentionPostgres(t *testing.T) {
	f := newAvailabilityFixture(t, 2)
	f.event(t, 0, "r1", "f1", "token_invalid", f.now.Add(-5*time.Minute))
	f.event(t, 0, "r2", "f2", "token_invalid", f.now.Add(-4*time.Minute))
	f.reconcile(t)
	original := f.occurrences(t, 0, "ACTIVE")[0]
	f.event(t, 0, "s1", "success", "", f.now.Add(-3*time.Minute))
	f.reconcile(t)
	if len(f.occurrences(t, 0, "ACTIVE")) != 0 || len(f.occurrences(t, 0, "RESOLVED")) != 1 {
		t.Fatal("success did not recover")
	}
	// Retention removes source events, never the bounded confirmation IDs.
	if _, err := f.db.owner.Exec(context.Background(), `DELETE FROM account_request_quality_events WHERE node_id=$1`, f.node); err != nil {
		t.Fatal(err)
	}
	f.repo, _ = store.NewAccountAvailabilityRepository(f.db.runtime)
	f.event(t, 0, "r1", "replay-new-h1", "token_invalid", f.now.Add(-2*time.Minute))
	f.event(t, 0, "r2", "replay-new-h2", "token_invalid", f.now.Add(-time.Minute))
	f.reconcile(t)
	if len(f.occurrences(t, 0, "ACTIVE")) != 0 {
		t.Fatal("consumed request IDs reopened after retention/restart")
	}
	f.event(t, 0, "new1", "new-h1", "token_invalid", f.now.Add(-30*time.Second))
	f.event(t, 0, "new2", "new-h2", "token_invalid", f.now.Add(-20*time.Second))
	f.reconcile(t)
	current := f.occurrences(t, 0, "ACTIVE")
	if len(current) != 1 || current[0].OccurrenceID == original.OccurrenceID {
		t.Fatal("recurrence did not get new ID")
	}
	// No traffic: one source cannot be counted twice or after restart.
	slot := f.now.Add(-20 * time.Minute).Truncate(5 * time.Minute)
	f.clock(t, "file_active", nil, f.now.Add(-10*time.Second), slot)
	f.reconcile(t)
	f.repo, _ = store.NewAccountAvailabilityRepository(f.db.runtime)
	for i := 0; i < 100; i++ {
		f.reconcile(t)
	}
	if len(f.occurrences(t, 0, "ACTIVE")) != 1 {
		t.Fatal("same snapshot supplied second recovery observation")
	}
	// A skipped slot is not consecutive; two adjacent later slots are.
	f.clock(t, "file_active", nil, f.now.Add(-8*time.Second), slot.Add(10*time.Minute))
	f.reconcile(t)
	if len(f.occurrences(t, 0, "ACTIVE")) != 1 {
		t.Fatal("skipped slot recovered")
	}
	f.clock(t, "file_active", nil, f.now.Add(-6*time.Second), slot.Add(15*time.Minute))
	f.reconcile(t)
	if len(f.occurrences(t, 0, "ACTIVE")) != 0 {
		t.Fatal("adjacent healthy sources did not recover")
	}
	if len(f.occurrences(t, 0, "RESOLVED")) != 2 {
		t.Fatal("history lost")
	}
}

func TestAccountAvailabilityFreshnessDisabledAndConcurrencyPostgres(t *testing.T) {
	f := newAvailabilityFixture(t, 2)
	ctx := context.Background()
	f.event(t, 0, "r1", "f1", "token_invalid", f.now.Add(-3*time.Minute))
	f.event(t, 0, "r2", "f2", "token_invalid", f.now.Add(-2*time.Minute))
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := f.repo.Reconcile(ctx); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(f.occurrences(t, 0, "ACTIVE")) != 1 {
		t.Fatal("concurrent duplicate ACTIVE")
	}
	f.clock(t, "file_disabled", nil, f.now.Add(-time.Minute), f.now.Truncate(5*time.Minute))
	f.reconcile(t)
	f.state(t, 0, "DISABLED")
	if len(f.occurrences(t, 0, "ACTIVE")) != 1 {
		t.Fatal("disabled resolved fault")
	}
	f.clock(t, "file_active", nil, f.now.Add(-16*time.Minute), f.now.Add(-20*time.Minute).Truncate(5*time.Minute))
	// Reads must overlay stale even before evaluator next runs.
	f.state(t, 0, "UNKNOWN")
	f.reconcile(t)
	if len(f.occurrences(t, 0, "ACTIVE")) != 1 {
		t.Fatal("stale resolved fault")
	}
	f.clock(t, "file_active", nil, f.now.Add(-time.Minute), f.now.Truncate(5*time.Minute))
	f.reconcile(t)
	tx, err := f.db.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `ALTER TABLE account_inventory_provider_states DISABLE TRIGGER USER; UPDATE account_inventory_provider_states SET health_degraded=true,health_reason='transport_failed'; ALTER TABLE account_inventory_provider_states ENABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	f.state(t, 0, "UNKNOWN")
	f.reconcile(t)
	f.event(t, 0, "s", "recovery-stale", "", f.now.Add(-30*time.Second))
	f.reconcile(t)
	if len(f.occurrences(t, 0, "ACTIVE")) != 0 {
		t.Fatal("new success should recover despite stale display")
	}
	f.state(t, 0, "UNKNOWN")
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = f.repo.Reconcile(canceled); err == nil {
		t.Fatal("cancellation ignored")
	}
}
