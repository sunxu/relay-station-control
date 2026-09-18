package store_test

import (
	"context"
	"github.com/google/uuid"
	store "github.com/sunxu/relay-station-control/internal/store"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestGatewayDirectoryRuntimeAttemptTimeout(t *testing.T) {
	for _, stage := range []string{"headers", "body"} {
		t.Run(stage, func(t *testing.T) {
			db := newIsolatedJobDatabase(t)
			ctx := context.Background()
			waitDirectoryRuntimeClaimWindow(t, db)
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if stage == "body" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"schema_version":1,`))
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
			}))
			defer server.Close()
			id := uuid.New()
			ref := "file://runtime/reader"
			insertGatewayInstance(t, ctx, db.owner, id, server.URL, ref)
			repo, err := store.NewGatewayDirectoryIngestionRepository(db.runtime)
			if err != nil {
				t.Fatal(err)
			}
			service, err := store.NewGatewayDirectoryIngestionService(repo, writeGatewayDirectorySecretResolver(t, ref, "test-reader"))
			if err != nil {
				t.Fatal(err)
			}
			for i, want := range []string{"retry_wait", "failed"} {
				start := time.Now()
				result, err := service.RunGatewayOnce(ctx, id)
				elapsed := time.Since(start)
				if err != nil {
					t.Fatal(err)
				}
				if result.Status != want || result.FailureClass != "timeout" {
					t.Fatalf("attempt %d: %+v", i+1, result)
				}
				if elapsed < 4500*time.Millisecond || elapsed > 8*time.Second {
					t.Fatalf("attempt bound: %v", elapsed)
				}
			}
			var count int
			if err := db.owner.QueryRow(ctx, `SELECT count(*) FROM gateway_directory_current_state`).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 || calls.Load() != 2 {
				t.Fatal("timeout refreshed observation or retried outside budget")
			}
		})
	}
}

func TestGatewayDirectoryRuntimeUnconfiguredNoWork(t *testing.T) {
	db := newIsolatedJobDatabase(t)
	ctx := context.Background()
	id := uuid.New()
	if _, err := db.owner.Exec(ctx, `SELECT control_register_gateway($1,'Unconfigured','http://127.0.0.1:1',NULL)`, id); err != nil {
		t.Fatal(err)
	}
	repo, err := store.NewGatewayDirectoryIngestionRepository(db.runtime)
	if err != nil {
		t.Fatal(err)
	}
	service, err := store.NewGatewayDirectoryIngestionService(repo, writeGatewayDirectorySecretResolver(t, "file://runtime/reader", "test-reader"))
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		results, err := service.WorkOnce(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 1 || results[0].Status != store.GatewayDirectoryWorkStatusNoWork {
			t.Fatalf("unexpected result: %+v", results)
		}
	}
	var count int
	if err := db.owner.QueryRow(ctx, `SELECT count(*) FROM gateway_directory_ingestion_runs WHERE gateway_instance_id=$1`, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("NULL reference created a run")
	}
}

func TestGatewayDirectoryRuntimeFinalizeDeadlineAndRecovery(t *testing.T) {
	db := newIsolatedJobDatabase(t)
	ctx := context.Background()
	id := uuid.New()
	ref := "file://runtime/reader"
	fetched := make(chan struct{})
	release := make(chan struct{})
	body := gatewayDirectoryJSON(t, time.Now().UTC(), "test")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(fetched)
		select {
		case <-release:
			_, _ = w.Write(body)
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	insertGatewayInstance(t, ctx, db.owner, id, server.URL, ref)
	waitDirectoryRuntimeClaimWindow(t, db)
	repo, err := store.NewGatewayDirectoryIngestionRepository(db.runtime)
	if err != nil {
		t.Fatal(err)
	}
	service, err := store.NewGatewayDirectoryIngestionService(repo, writeGatewayDirectorySecretResolver(t, ref, "test"))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := service.RunGatewayOnce(ctx, id); done <- err }()
	select {
	case <-fetched:
	case <-time.After(10 * time.Second):
		t.Fatal("fetch did not start")
	}
	lock, err := db.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(ctx)
	if _, err := lock.Exec(ctx, `SELECT instance_id FROM gateway_instances WHERE instance_id=$1 FOR UPDATE`, id); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("blocked finalize unexpectedly succeeded")
		}
	case <-time.After(8 * time.Second):
		t.Fatal("finalize ignored attempt deadline")
	}
	if err := lock.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var observations int
	if err := db.owner.QueryRow(ctx, `SELECT count(*) FROM gateway_directory_current_state`).Scan(&observations); err != nil {
		t.Fatal(err)
	}
	if observations != 0 {
		t.Fatal("expired attempt committed success")
	}
	// Let the real lease expire without changing the database state-shape contract.
	expiryDeadline := time.Now().Add(20 * time.Second)
	for {
		var expired bool
		if err := db.owner.QueryRow(ctx, `SELECT lease_expires_at <= clock_timestamp() FROM gateway_directory_ingestion_runs WHERE gateway_instance_id=$1 AND status='running'`, id).Scan(&expired); err != nil {
			t.Fatal(err)
		}
		if expired {
			break
		}
		if time.Now().After(expiryDeadline) {
			t.Fatal("lease did not expire")
		}
		time.Sleep(100 * time.Millisecond)
	}

	restarted, err := store.NewGatewayDirectoryIngestionService(repo, writeGatewayDirectorySecretResolver(t, ref, "test"))
	if err != nil {
		t.Fatal(err)
	}
	results, err := restarted.ReconcileTick(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || (results[0].To != "retry_wait" && results[0].To != "failed") {
		t.Fatalf("restart reconciliation: %+v", results)
	}
}

// Unlike legacy fixtures, runtime acceptance must not pass by skipping late slots.
func waitDirectoryRuntimeClaimWindow(t *testing.T, db *isolatedJobDatabase) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		var remaining float64
		if err := db.owner.QueryRow(context.Background(), `WITH db_now AS (
			SELECT COALESCE(NULLIF(current_setting('control.test_gateway_directory_now', true), '')::timestamptz, clock_timestamp()) AS now
		) SELECT 120-extract(epoch FROM db_now.now-to_timestamp(floor(extract(epoch FROM db_now.now)/180)*180)) FROM db_now`).Scan(&remaining); err != nil {
			t.Fatal(err)
		}
		if remaining > 35 {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatal("could not enter a runtime claim window")
}
