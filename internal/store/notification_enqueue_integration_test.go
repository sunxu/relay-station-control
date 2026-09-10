package store_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/sunxu/relay-station-control/internal/dingtalk"
	"github.com/sunxu/relay-station-control/internal/jobs"
	"github.com/sunxu/relay-station-control/internal/store"
)

// Replaying after deliberately discarding a successful commit acknowledgement
// tests the committed side of ambiguity, not an assumed transaction rollback.
func TestNotificationCommittedReplayPostgres(t *testing.T) {
	db := newIsolatedJobDatabase(t)
	ctx := context.Background()
	registry, err := jobs.NewRegistry(dingtalk.Definition(dingtalk.NewExecutor(dingtalk.Config{})))
	if err != nil {
		t.Fatal(err)
	}
	for _, domain := range []string{"availability", "duplicate"} {
		for _, transition := range []string{"ACTIVE", "RESOLVED"} {
			snapshot := store.NotificationSnapshotForTest{
				OccurrenceID: uuid.New(), OccurrenceType: "TOKEN_INVALID", Transition: transition,
				Reason: "token_invalid", Severity: "Critical", EnvironmentID: "test", EnvironmentName: "Test",
				AccountKey: "antigravity:a@example.invalid", Email: "a@example.invalid", Provider: "antigravity",
				InstanceIDs: []uuid.UUID{uuid.MustParse("10000000-0000-4000-8000-000000000001"), uuid.MustParse("20000000-0000-4000-8000-000000000002")},
				NodeNames:   []string{"Relay", "Relay"}, StartedAt: time.Date(2026, 9, 11, 1, 0, 0, 0, time.UTC), TransitionedAt: time.Date(2026, 9, 11, 1, 1, 0, 0, time.UTC),
			}
			if domain == "duplicate" {
				snapshot.OccurrenceType = "CROSS_NODE_DUPLICATE_OWNERSHIP"
				snapshot.Reason = "cross_node_duplicate_ownership"
			}
			key := fmt.Sprintf("dingtalk:%s:%s:%s", domain, snapshot.OccurrenceID, strings.ToLower(transition))
			var before string
			for attempt := 0; attempt < 2; attempt++ {
				tx, err := db.runtime.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
				if err != nil {
					t.Fatal(err)
				}
				if err = store.EnqueueNotificationForTest(ctx, tx, registry, snapshot); err != nil {
					_ = tx.Rollback(ctx)
					t.Fatal(err)
				}
				if err = tx.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				var fingerprint string
				if err = db.owner.QueryRow(ctx, `SELECT job_id::text || operation_id::text || payload::text || encode(payload_hash,'hex') FROM async_jobs WHERE idempotency_key=$1`, key).Scan(&fingerprint); err != nil {
					t.Fatal(err)
				}
				if attempt == 0 {
					before = fingerprint
				} else if fingerprint != before {
					t.Fatal("committed replay changed identity, payload or hash")
				}
			}
			var count int
			if err := db.owner.QueryRow(ctx, `SELECT count(*) FROM async_jobs WHERE idempotency_key=$1`, key).Scan(&count); err != nil || count != 1 {
				t.Fatalf("logical job count=%d err=%v", count, err)
			}
			var operationID uuid.UUID
			if err := db.owner.QueryRow(ctx, `SELECT operation_id FROM async_jobs WHERE idempotency_key=$1`, key).Scan(&operationID); err != nil {
				t.Fatal(err)
			}
			if operationID != uuid.NewSHA1(uuid.MustParse("94db90f6-d7e6-4cce-a045-890b63171d86"), []byte(key)) {
				t.Fatal("operation derivation mismatch")
			}
		}
	}
}

func TestNotificationAbortedSerializableNoResiduePostgres(t *testing.T) {
	db := newIsolatedJobDatabase(t)
	ctx := context.Background()
	registry, err := jobs.NewRegistry(dingtalk.Definition(dingtalk.NewExecutor(dingtalk.Config{})))
	if err != nil {
		t.Fatal(err)
	}
	snapshot := store.NotificationSnapshotForTest{OccurrenceID: uuid.New(), OccurrenceType: "TOKEN_INVALID", Transition: "ACTIVE", Reason: "token_invalid", Severity: "Critical", EnvironmentID: "test", EnvironmentName: "Test", AccountKey: "antigravity:a@example.invalid", Email: "a@example.invalid", Provider: "antigravity", InstanceIDs: []uuid.UUID{uuid.New()}, NodeNames: []string{"Node"}, StartedAt: time.Date(2026, 9, 11, 1, 0, 0, 0, time.UTC), TransitionedAt: time.Date(2026, 9, 11, 1, 1, 0, 0, time.UTC)}
	tx, err := db.runtime.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.EnqueueNotificationForTest(ctx, tx, registry, snapshot); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `DO $$ BEGIN RAISE EXCEPTION 'injected serialization abort' USING ERRCODE='40001'; END $$`)
	if err == nil {
		t.Fatal("expected serialization abort")
	}
	_ = tx.Rollback(ctx)
	for _, table := range []string{"async_jobs", "async_job_events", "operation_outbox"} {
		var count int
		if err := db.owner.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s residue=%d err=%v", table, count, err)
		}
	}
	// No durable transition survived; a new attempt may have a new identity/time.
	snapshot.OccurrenceID = uuid.New()
	snapshot.TransitionedAt = snapshot.TransitionedAt.Add(time.Second)
	tx, err = db.runtime.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.EnqueueNotificationForTest(ctx, tx, registry, snapshot); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestNotificationMigrationPreservesV1Postgres(t *testing.T) {
	db := newIsolatedJobDatabase(t, "up-to", "31")
	ctx := context.Background()
	var before string
	if err := db.owner.QueryRow(ctx, `SELECT pg_get_functiondef('public.control_reconcile_account_availability_v1(uuid,text)'::regprocedure)`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	var tablesBefore int
	if err := db.owner.QueryRow(ctx, `SELECT count(*) FROM pg_tables WHERE schemaname='public'`).Scan(&tablesBefore); err != nil {
		t.Fatal(err)
	}
	if err := runAssetGoose(t, ctx, "../..", db.ownerURL, "up"); err != nil {
		t.Fatal(err)
	}
	var after string
	if err := db.owner.QueryRow(ctx, `SELECT pg_get_functiondef('public.control_reconcile_account_availability_v1(uuid,text)'::regprocedure)`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatal("forward migration changed v1")
	}
	var tablesAfter int
	if err := db.owner.QueryRow(ctx, `SELECT count(*) FROM pg_tables WHERE schemaname='public'`).Scan(&tablesAfter); err != nil {
		t.Fatal(err)
	}
	if tablesAfter != tablesBefore {
		t.Fatal("notification migration added a table")
	}
	for _, signature := range []string{"public.control_reconcile_account_availability_v2(uuid,text)", "public.control_notification_display_snapshot_v1(text,uuid[])"} {
		var owner string
		var definer, publicAllowed, runtimeAllowed bool
		var settings []string
		if err := db.owner.QueryRow(ctx, `SELECT pg_get_userbyid(proowner),prosecdef,proconfig,has_function_privilege('public',oid,'EXECUTE'),has_function_privilege('relay_control_runtime',oid,'EXECUTE') FROM pg_proc WHERE oid=$1::regprocedure`, signature).Scan(&owner, &definer, &settings, &publicAllowed, &runtimeAllowed); err != nil {
			t.Fatal(err)
		}
		if owner != "relay_control_migrator" || !definer || publicAllowed || !runtimeAllowed || len(settings) != 1 || settings[0] != "search_path=pg_catalog" {
			t.Fatalf("invalid ACL for %s: %s %v %v %v %v", signature, owner, definer, publicAllowed, runtimeAllowed, settings)
		}
	}
}

func TestNotificationConcurrentSameCommittedSnapshotPostgres(t *testing.T) {
	db := newIsolatedJobDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	registry, err := jobs.NewRegistry(dingtalk.Definition(dingtalk.NewExecutor(dingtalk.Config{})))
	if err != nil {
		t.Fatal(err)
	}
	snapshot := store.NotificationSnapshotForTest{OccurrenceID: uuid.New(), OccurrenceType: "TOKEN_INVALID", Transition: "ACTIVE", Reason: "token_invalid", Severity: "Critical", EnvironmentID: "test", EnvironmentName: "Test", AccountKey: "antigravity:a@example.invalid", Email: "a@example.invalid", Provider: "antigravity", InstanceIDs: []uuid.UUID{uuid.New()}, NodeNames: []string{"Node"}, StartedAt: time.Date(2026, 9, 11, 1, 0, 0, 0, time.UTC), TransitionedAt: time.Date(2026, 9, 11, 1, 1, 0, 0, time.UTC)}
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	results := make(chan error, 2)
	for n := 0; n < 2; n++ {
		go func() {
			for attempt := 0; attempt < 8; attempt++ {
				tx, err := db.runtime.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
				if err != nil {
					results <- err
					return
				}
				if attempt == 0 {
					// Establish both snapshots before either contender enqueues.
					_, err = tx.Exec(ctx, `SELECT count(*) FROM async_jobs`)
					ready <- struct{}{}
					select {
					case <-release:
					case <-ctx.Done():
						err = ctx.Err()
					}
				}
				if err == nil {
					err = store.EnqueueNotificationForTest(ctx, tx, registry, snapshot)
				}
				if err == nil {
					err = tx.Commit(ctx)
				}
				_ = tx.Rollback(ctx)
				if err == nil {
					results <- nil
					return
				}
				var pgErr *pgconn.PgError
				if !errors.As(err, &pgErr) || (pgErr.Code != "40001" && pgErr.Code != "40P01") {
					results <- err
					return
				}
			}
			results <- fmt.Errorf("serialization retry exhausted")
		}()
	}
	for n := 0; n < 2; n++ {
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	close(release)
	for n := 0; n < 2; n++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"async_jobs", "async_job_events", "operation_outbox"} {
		var count int
		if err := db.owner.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s count=%d err=%v", table, count, err)
		}
	}
}
