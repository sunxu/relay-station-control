package store_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	store "github.com/sunxu/relay-station-control/internal/store"
	"net/http"
	"net/http/httptest"
	"os"
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
		if err := db.owner.QueryRow(context.Background(), `SELECT 120-extract(epoch FROM clock_timestamp()-to_timestamp(floor(extract(epoch FROM clock_timestamp())/180)*180))`).Scan(&remaining); err != nil {
			t.Fatal(err)
		}
		if remaining > 35 {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatal("could not enter a runtime claim window")
}

func TestGatewayDirectoryRuntimeSourceLifecycle(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprintf("tls=%t", secure), func(t *testing.T) {
			db := newIsolatedJobDatabase(t)
			ctx := context.Background()
			waitDirectoryRuntimeClaimWindow(t, db)
			var phase atomic.Int32
			var calls atomic.Int32
			source := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Header.Get("Authorization") != "Bearer lifecycle-reader" {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				accounts := make([]map[string]any, 0, 4)
				for _, id := range []int64{9007199254740991, 9007199254740992, 9007199254740993, 9223372036854775807} {
					accounts = append(accounts, map[string]any{"id": id, "name": "lifecycle", "platform": "openai", "type": "apikey", "url": nil, "status": "active"})
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "generated_at": time.Now().UTC().Format(time.RFC3339Nano), "accounts": accounts})
			})
			var server *httptest.Server
			if secure {
				server = httptest.NewTLSServer(source)
			} else {
				server = httptest.NewServer(source)
			}
			defer server.Close()
			id := uuid.New()
			ref := "file://lifecycle/reader"
			insertGatewayInstance(t, ctx, db.owner, id, server.URL, ref)
			repo, err := store.NewGatewayDirectoryIngestionRepository(db.runtime)
			if err != nil {
				t.Fatal(err)
			}
			resolver, tokenPath := writeGatewayDirectorySecretResolverWithPath(t, ref, "lifecycle-reader")
			var previous time.Time
			for i, want := range []string{"changed", "unchanged", "failed", "unchanged", "failed", "unchanged"} {
				phase.Store(int32(i))
				if i == 2 {
					if err := os.WriteFile(tokenPath, []byte("lifecycle-reader-old\n"), 0o600); err != nil {
						t.Fatal(err)
					}
				} else if i == 3 {
					if err := os.WriteFile(tokenPath, []byte("lifecycle-reader\n"), 0o600); err != nil {
						t.Fatal(err)
					}
				} else if i == 4 {
					if err := os.Rename(tokenPath, tokenPath+".saved"); err != nil {
						t.Fatal(err)
					}
				} else if i == 5 {
					if err := os.Rename(tokenPath+".saved", tokenPath); err != nil {
						t.Fatal(err)
					}
				}
				// Seed a distinct historical running slot, never a successful observation.
				// Runtime process tests separately exercise real Schedule/Claim cadence.
				runID, fence := uuid.New(), uuid.New()
				if _, err := db.owner.Exec(ctx, `INSERT INTO gateway_directory_ingestion_runs
                  (ingestion_run_id,gateway_instance_id,scheduled_at,status,attempt_count,created_at,first_started_at,last_started_at,lease_expires_at,lease_fencing_token)
                  SELECT $1,$2,to_timestamp(floor(extract(epoch FROM clock_timestamp())/180)*180)-$4*interval '180 seconds','running',1,statement_timestamp(),statement_timestamp(),statement_timestamp(),statement_timestamp()+interval '15 seconds',$3`, runID, id, fence, i+1); err != nil {
					t.Fatal(err)
				}
				attemptCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				attempt, err := repo.ExecuteAttempt(attemptCtx, store.GatewayDirectoryAttemptRequest{IngestionRunID: runID, GatewayInstanceID: id, LeaseFencingToken: fence}, resolver)
				if err != nil {
					cancel()
					t.Fatal(err)
				}
				if want == "failed" {
					failureClass := "http_non_retryable"
					if i == 4 {
						failureClass = "secret_unavailable"
					}
					if attempt.Failure == nil || string(attempt.Failure.Disposition) != "failed" || attempt.Failure.Class != failureClass {
						cancel()
						t.Fatalf("failure: %+v", attempt)
					}
				} else {
					if attempt.Success == nil {
						cancel()
						t.Fatal("missing successful attempt")
					}
					finalized, err := repo.FinalizeSuccessfulAttempt(attemptCtx, *attempt.Success)
					if err != nil || finalized.Success == nil || string(finalized.Success.Outcome) != want {
						cancel()
						t.Fatalf("finalize outcome mismatch: %v", err)
					}
				}
				cancel()
				var observed time.Time
				if err := db.owner.QueryRow(ctx, `SELECT last_success_received_at FROM gateway_directory_current_state WHERE gateway_instance_id=$1`, id).Scan(&observed); err != nil {
					t.Fatal(err)
				}
				if want == "failed" {
					if !observed.Equal(previous) {
						t.Fatal("failure refreshed freshness")
					}
				} else if !previous.IsZero() && !observed.After(previous) {
					t.Fatal("success did not advance observation")
				}
				previous = observed

			}
			if calls.Load() != 5 {
				t.Fatalf("request count = %d, missing Secret should not issue HTTP", calls.Load())
			}
			var snapshots, bindings int
			if err := db.owner.QueryRow(ctx, `SELECT count(*) FROM gateway_directory_snapshots`).Scan(&snapshots); err != nil {
				t.Fatal(err)
			}
			if err := db.owner.QueryRow(ctx, `SELECT count(*) FROM relay_node_gateway_account_bindings`).Scan(&bindings); err != nil {
				t.Fatal(err)
			}
			if snapshots != 1 || bindings != 0 {
				t.Fatal("content dedupe or no-auto-binding invariant violated")
			}
			rows, err := db.owner.Query(ctx, `SELECT account_id FROM gateway_directory_snapshot_items ORDER BY account_id`)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			for _, want := range []int64{9007199254740991, 9007199254740992, 9007199254740993, 9223372036854775807} {
				if !rows.Next() {
					t.Fatal("missing identity")
				}
				var got int64
				if err := rows.Scan(&got); err != nil || got != want {
					t.Fatal("source int64 identity changed")
				}
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("unexpected account rows")
			}
		})
	}
}
