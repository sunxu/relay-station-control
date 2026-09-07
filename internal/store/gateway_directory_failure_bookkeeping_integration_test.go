package store_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	store "github.com/sunxu/relay-station-control/internal/store"
)

// This deliberately uses a real owner transaction to hold the target row
// lock while failure bookkeeping tries to finish a 401 attempt.
func TestGatewayDirectoryFailureBookkeepingIsBoundedAndReconciles(t *testing.T) {
	db := newIsolatedJobDatabase(t)
	ctx := context.Background()
	waitDirectoryRuntimeClaimWindow(t, db)
	id := uuid.New()
	ref := "file://failure-bookkeeping/reader"
	fetched := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(fetched)
		select {
		case <-release:
			w.WriteHeader(http.StatusUnauthorized)
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	insertGatewayInstance(t, ctx, db.owner, id, server.URL, ref)
	repo, err := store.NewGatewayDirectoryIngestionRepository(db.runtime)
	if err != nil {
		t.Fatal(err)
	}
	service, err := store.NewGatewayDirectoryIngestionService(repo, writeGatewayDirectorySecretResolver(t, ref, "reader"))
	if err != nil {
		t.Fatal(err)
	}

	type runResult struct {
		result store.GatewayDirectoryWorkResult
		err    error
	}
	done := make(chan runResult, 1)
	go func() {
		result, runErr := service.RunGatewayOnce(ctx, id)
		done <- runResult{result: result, err: runErr}
	}()
	select {
	case <-fetched:
	case <-time.After(10 * time.Second):
		t.Fatal("failure source request did not start")
	}
	var runID uuid.UUID
	if err := db.owner.QueryRow(ctx, `SELECT ingestion_run_id FROM gateway_directory_ingestion_runs WHERE gateway_instance_id=$1 AND status='running' ORDER BY created_at DESC LIMIT 1`, id).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	lock, err := db.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(ctx)
	if _, err := lock.Exec(ctx, `SELECT ingestion_run_id FROM gateway_directory_ingestion_runs WHERE ingestion_run_id=$1 FOR UPDATE`, runID); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	close(release)
	var completed runResult
	select {
	case completed = <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("failure bookkeeping exceeded bounded attempt")
	}
	if completed.err == nil {
		t.Fatal("blocked failure bookkeeping unexpectedly succeeded")
	}
	if elapsed := time.Since(started); elapsed < 4500*time.Millisecond || elapsed > 8*time.Second {
		t.Fatalf("failure bookkeeping was not bounded: %v", elapsed)
	}
	var status string
	var failure *string
	if err := db.owner.QueryRow(ctx, `SELECT status,last_failure_class FROM gateway_directory_ingestion_runs WHERE ingestion_run_id=$1`, runID).Scan(&status, &failure); err != nil {
		t.Fatal(err)
	}
	if status != "running" || failure != nil {
		t.Fatalf("blocked run changed: status=%s failure=%v", status, failure)
	}
	var current int
	if err := db.owner.QueryRow(ctx, `SELECT count(*) FROM gateway_directory_current_state WHERE gateway_instance_id=$1`, id).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if current != 0 {
		t.Fatal("401 failure advanced current state")
	}
	if err := lock.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var expired bool
		if err := db.owner.QueryRow(ctx, `SELECT lease_expires_at <= clock_timestamp() FROM gateway_directory_ingestion_runs WHERE gateway_instance_id=$1 AND status='running'`, id).Scan(&expired); err != nil {
			t.Fatal(err)
		}
		if expired {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	results, err := service.ReconcileTick(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].RunID != runID || (results[0].To != "retry_wait" && results[0].To != "failed") {
		t.Fatalf("lease reconciliation: %+v", results)
	}
}

func TestGatewayDirectoryFailureBookkeepingHonorsParentCancellation(t *testing.T) {
	db := newIsolatedJobDatabase(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waitDirectoryRuntimeClaimWindow(t, db)
	id := uuid.New()
	ref := "file://failure-cancellation/reader"
	fetched := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(fetched)
		<-r.Context().Done()
	}))
	defer server.Close()
	insertGatewayInstance(t, context.Background(), db.owner, id, server.URL, ref)
	repo, err := store.NewGatewayDirectoryIngestionRepository(db.runtime)
	if err != nil {
		t.Fatal(err)
	}
	service, err := store.NewGatewayDirectoryIngestionService(repo, writeGatewayDirectorySecretResolver(t, ref, "reader"))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, runErr := service.RunGatewayOnce(ctx, id); done <- runErr }()
	select {
	case <-fetched:
		cancel()
	case <-time.After(10 * time.Second):
		t.Fatal("cancellation source request did not start")
	}
	select {
	case runErr := <-done:
		if runErr == nil {
			t.Fatal("parent cancellation unexpectedly succeeded")
		}
	case <-time.After(8 * time.Second):
		t.Fatal("parent cancellation left unbounded bookkeeping")
	}
	var current int
	if err := db.owner.QueryRow(context.Background(), `SELECT count(*) FROM gateway_directory_current_state WHERE gateway_instance_id=$1`, id).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if current != 0 {
		t.Fatal("cancellation advanced current state")
	}
	var status string
	if err := db.owner.QueryRow(context.Background(), `SELECT status FROM gateway_directory_ingestion_runs WHERE gateway_instance_id=$1 ORDER BY created_at DESC LIMIT 1`, id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "running" {
		t.Fatalf("cancellation finalized run as %s", status)
	}
}
