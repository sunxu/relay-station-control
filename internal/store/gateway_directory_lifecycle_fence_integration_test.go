package store_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sunxu/relay-station-control/internal/store"
)

const gatewayLifecycleTestKey = "01234567890123456789012345678901"

func insertLifecycleGateway(t *testing.T, ctx context.Context, pool *pgxpool.Pool, gatewayID uuid.UUID, endpoint, secretRef string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `INSERT INTO gateway_instances(
		singleton_id, instance_id, display_name, management_endpoint, reader_secret_ref
	) VALUES (1, $1, 'Directory lifecycle test Gateway', $2, $3)`, gatewayID, endpoint, secretRef); err != nil {
		t.Fatal(err)
	}
}

type gatewayDirectoryLifecycleFixture struct {
	database   *pgxpool.Pool
	repository *store.GatewayDirectoryIngestionRepository
	assets     *store.GatewayLifecycleRepository
	adminID    uuid.UUID
}

func newGatewayDirectoryLifecycleFixture(t *testing.T, ctx context.Context) *gatewayDirectoryLifecycleFixture {
	t.Helper()
	databaseURL, connection, cleanup := newGatewayLifecycleMigrationDatabase(t, ctx)
	if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "33"); err != nil {
		cleanup()
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	adminID := uuid.New()
	if _, err := connection.Exec(ctx, `INSERT INTO control_admin_users(
		admin_id, login_name, display_name, role, status, activated_at
	) VALUES ($1, $2, 'Directory lifecycle admin', 'super_admin', 'enabled', clock_timestamp())`,
		adminID, "directory_lifecycle_"+adminID.String()[:8]); err != nil {
		connection.Close(ctx)
		t.Fatal(err)
	}
	connection.Close(ctx)
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close() })
	repository, err := store.NewGatewayDirectoryIngestionRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	assets, err := store.NewGatewayLifecycleRepository(pool, []byte(gatewayLifecycleTestKey))
	if err != nil {
		t.Fatal(err)
	}
	return &gatewayDirectoryLifecycleFixture{database: pool, repository: repository, assets: assets, adminID: adminID}
}

func gatewayDirectoryDatabaseNow(t *testing.T, ctx context.Context, pool *pgxpool.Pool) time.Time {
	t.Helper()
	var now time.Time
	if err := pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	return now.UTC()
}

func TestGatewayDirectoryLifecycleFenceAcceptancePG18(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	t.Run("zero current does not schedule", func(t *testing.T) {
		fixture := newGatewayDirectoryLifecycleFixture(t, ctx)
		gatewayID := uuid.New()
		insertLifecycleGateway(t, ctx, fixture.database, gatewayID, "http://zero-current.example", "file://directory/reader")
		if _, err := fixture.assets.Retire(ctx, store.GatewayCommand{
			CommandID: uuid.New(), ActorAdminID: fixture.adminID, RequestID: "zero-current-retire", InstanceID: gatewayID, ExpectedRevision: 1,
			Secret: store.SecretPatch{Operation: store.SecretAbsent},
		}); err != nil {
			t.Fatal(err)
		}
		_, created, skipped, err := fixture.repository.ScheduleCurrent(ctx, gatewayID)
		if err != nil {
			t.Fatal(err)
		}
		if created || !skipped {
			t.Fatalf("schedule after retire = created=%v skipped=%v", created, skipped)
		}
		var runs int
		if err := fixture.database.QueryRow(ctx, `SELECT count(*) FROM gateway_directory_ingestion_runs WHERE gateway_instance_id=$1`, gatewayID).Scan(&runs); err != nil {
			t.Fatal(err)
		}
		if runs != 0 {
			t.Fatalf("retired Gateway has %d new runs", runs)
		}
	})

	t.Run("lifecycle wins before outbound authorization", func(t *testing.T) {
		for _, replacement := range []bool{false, true} {
			t.Run(map[bool]string{false: "retire", true: "replace"}[replacement], func(t *testing.T) {
				fixture := newGatewayDirectoryLifecycleFixture(t, ctx)
				gatewayID := uuid.New()
				secretRef := "file://directory/reader"
				insertLifecycleGateway(t, ctx, fixture.database, gatewayID, "http://outbound-fence.example", secretRef)
				runID, lease := uuid.New(), uuid.New()
				slot := gatewayDirectoryCurrentSlot(t, ctx, fixture.database)
				startedAt := gatewayDirectoryDatabaseNow(t, ctx, fixture.database)
				insertGatewayDirectoryRunningRun(t, ctx, fixture.database, runID, gatewayID, lease, slot, startedAt, 1, "")
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					w.WriteHeader(http.StatusInternalServerError)
				}))
				defer server.Close()

				if replacement {
					if _, err := fixture.assets.Replace(ctx, store.GatewayCommand{
						CommandID: uuid.New(), ActorAdminID: fixture.adminID, RequestID: "outbound-replace", InstanceID: gatewayID, ExpectedRevision: 1, NewInstanceID: uuid.New(),
						DisplayName:        store.StringPatch{Present: true, Value: "replacement"},
						ManagementEndpoint: store.StringPatch{Present: true, Value: server.URL},
						Secret:             store.SecretPatch{Operation: store.SecretAbsent},
					}); err != nil {
						t.Fatal(err)
					}
				} else if _, err := fixture.assets.Retire(ctx, store.GatewayCommand{
					CommandID: uuid.New(), ActorAdminID: fixture.adminID, RequestID: "outbound-retire", InstanceID: gatewayID, ExpectedRevision: 1,
					Secret: store.SecretPatch{Operation: store.SecretAbsent},
				}); err != nil {
					t.Fatal(err)
				}

				attempt, err := fixture.repository.ExecuteAttempt(ctx, store.GatewayDirectoryAttemptRequest{
					IngestionRunID: runID, GatewayInstanceID: gatewayID, LeaseFencingToken: lease,
				}, writeGatewayDirectorySecretResolver(t, secretRef, "reader-token"))
				if err != nil {
					t.Fatal(err)
				}
				expectedFailure := map[bool]string{false: "gateway_retired", true: "gateway_replaced"}[replacement]
				if attempt.Success != nil || attempt.Failure == nil || attempt.Failure.Class != expectedFailure || attempt.Failure.Retryable {
					t.Fatalf("outbound fence result = %#v", attempt)
				}
				if calls.Load() != 0 {
					t.Fatalf("retired/replaced Gateway received %d HTTP requests", calls.Load())
				}
				var status, failureClass string
				if err := fixture.database.QueryRow(ctx, `SELECT status,last_failure_class FROM gateway_directory_ingestion_runs WHERE ingestion_run_id=$1`, runID).Scan(&status, &failureClass); err != nil {
					t.Fatal(err)
				}
				if status != "failed" || failureClass != expectedFailure {
					t.Fatalf("run status/class = %q/%q, want failed/%s", status, failureClass, expectedFailure)
				}
			})
		}
	})

	t.Run("lifecycle wins before promotion", func(t *testing.T) {
		for _, replacement := range []bool{false, true} {
			t.Run(map[bool]string{false: "retire", true: "replace"}[replacement], func(t *testing.T) {
				fixture := newGatewayDirectoryLifecycleFixture(t, ctx)
				gatewayID := uuid.New()
				secretRef := "file://directory/reader"
				insertLifecycleGateway(t, ctx, fixture.database, gatewayID, "http://promotion-fence.example", secretRef)
				runID, lease := uuid.New(), uuid.New()
				slot := gatewayDirectoryCurrentSlot(t, ctx, fixture.database)
				startedAt := gatewayDirectoryDatabaseNow(t, ctx, fixture.database)
				insertGatewayDirectoryRunningRun(t, ctx, fixture.database, runID, gatewayID, lease, slot, startedAt, 1, "")
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write(gatewayDirectoryJSON(t, time.Now().UTC(), "before-fence"))
				}))
				defer server.Close()
				if _, err := fixture.database.Exec(ctx, `UPDATE gateway_instances SET management_endpoint=$2 WHERE instance_id=$1`, gatewayID, server.URL); err != nil {
					t.Fatal(err)
				}
				attempt, err := fixture.repository.ExecuteAttempt(ctx, store.GatewayDirectoryAttemptRequest{
					IngestionRunID: runID, GatewayInstanceID: gatewayID, LeaseFencingToken: lease,
				}, writeGatewayDirectorySecretResolver(t, secretRef, "reader-token"))
				if err != nil || attempt.Success == nil {
					t.Fatalf("attempt = %#v, err=%v", attempt, err)
				}

				if replacement {
					if _, err := fixture.assets.Replace(ctx, store.GatewayCommand{
						CommandID: uuid.New(), ActorAdminID: fixture.adminID, RequestID: "promotion-replace", InstanceID: gatewayID, ExpectedRevision: 1, NewInstanceID: uuid.New(),
						DisplayName:        store.StringPatch{Present: true, Value: "replacement"},
						ManagementEndpoint: store.StringPatch{Present: true, Value: "http://new-promotion.example"},
						Secret:             store.SecretPatch{Operation: store.SecretAbsent},
					}); err != nil {
						t.Fatal(err)
					}
				} else if _, err := fixture.assets.Retire(ctx, store.GatewayCommand{
					CommandID: uuid.New(), ActorAdminID: fixture.adminID, RequestID: "promotion-retire", InstanceID: gatewayID, ExpectedRevision: 1,
					Secret: store.SecretPatch{Operation: store.SecretAbsent},
				}); err != nil {
					t.Fatal(err)
				}
				finalized, err := fixture.repository.FinalizeSuccessfulAttempt(ctx, *attempt.Success)
				if err != nil || finalized.Failure == nil || finalized.Failure.Reason != store.GatewayDirectoryFinalizeFailureGatewayInactive {
					t.Fatalf("finalize after lifecycle = %#v, err=%v", finalized, err)
				}
				expectedFailure := map[bool]string{false: "gateway_retired", true: "gateway_replaced"}[replacement]
				var durableFailure string
				if err := fixture.database.QueryRow(ctx, `SELECT last_failure_class FROM gateway_directory_ingestion_runs WHERE ingestion_run_id=$1`, runID).Scan(&durableFailure); err != nil {
					t.Fatal(err)
				}
				if durableFailure != expectedFailure {
					t.Fatalf("durable failure = %q, want %q", durableFailure, expectedFailure)
				}
				var snapshots, current int
				if err := fixture.database.QueryRow(ctx, `SELECT count(*) FROM gateway_directory_snapshots WHERE gateway_instance_id=$1`, gatewayID).Scan(&snapshots); err != nil {
					t.Fatal(err)
				}
				if err := fixture.database.QueryRow(ctx, `SELECT count(*) FROM gateway_directory_current_state WHERE gateway_instance_id=$1`, gatewayID).Scan(&current); err != nil {
					t.Fatal(err)
				}
				if snapshots != 0 || current != 0 {
					t.Fatalf("post-fence promotion left snapshots=%d current=%d", snapshots, current)
				}
			})
		}
	})

	t.Run("promotion first is retained and replacement starts independently", func(t *testing.T) {
		fixture := newGatewayDirectoryLifecycleFixture(t, ctx)
		oldID, newID := uuid.New(), uuid.New()
		secretRef := "file://directory/reader"
		oldServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(gatewayDirectoryJSON(t, time.Now().UTC(), "old"))
		}))
		defer oldServer.Close()
		newServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(gatewayDirectoryJSON(t, time.Now().UTC(), "new"))
		}))
		defer newServer.Close()
		insertLifecycleGateway(t, ctx, fixture.database, oldID, oldServer.URL, secretRef)
		oldRun, oldLease := uuid.New(), uuid.New()
		oldSlot := gatewayDirectoryCurrentSlot(t, ctx, fixture.database)
		oldStartedAt := gatewayDirectoryDatabaseNow(t, ctx, fixture.database)
		insertGatewayDirectoryRunningRun(t, ctx, fixture.database, oldRun, oldID, oldLease, oldSlot, oldStartedAt, 1, "")
		resolver := writeGatewayDirectorySecretResolver(t, secretRef, "reader-token")
		oldAttempt, err := fixture.repository.ExecuteAttempt(ctx, store.GatewayDirectoryAttemptRequest{IngestionRunID: oldRun, GatewayInstanceID: oldID, LeaseFencingToken: oldLease}, resolver)
		if err != nil || oldAttempt.Success == nil {
			t.Fatalf("old attempt = %#v, err=%v", oldAttempt, err)
		}
		oldFinalized, err := fixture.repository.FinalizeSuccessfulAttempt(ctx, *oldAttempt.Success)
		if err != nil || oldFinalized.Success == nil {
			t.Fatalf("old finalize = %#v, err=%v", oldFinalized, err)
		}

		if _, err := fixture.assets.Replace(ctx, store.GatewayCommand{
			CommandID: uuid.New(), ActorAdminID: fixture.adminID, RequestID: "independent-replace", InstanceID: oldID, ExpectedRevision: 1, NewInstanceID: newID,
			DisplayName:        store.StringPatch{Present: true, Value: "new Gateway"},
			ManagementEndpoint: store.StringPatch{Present: true, Value: newServer.URL},
			Secret:             store.SecretPatch{Operation: store.SecretSet, Value: secretRef},
		}); err != nil {
			t.Fatal(err)
		}
		newRun, newLease := uuid.New(), uuid.New()
		newSlot := gatewayDirectoryCurrentSlot(t, ctx, fixture.database)
		newStartedAt := gatewayDirectoryDatabaseNow(t, ctx, fixture.database)
		insertGatewayDirectoryRunningRun(t, ctx, fixture.database, newRun, newID, newLease, newSlot, newStartedAt, 1, "")
		newAttempt, err := fixture.repository.ExecuteAttempt(ctx, store.GatewayDirectoryAttemptRequest{IngestionRunID: newRun, GatewayInstanceID: newID, LeaseFencingToken: newLease}, resolver)
		if err != nil || newAttempt.Success == nil {
			t.Fatalf("new attempt = %#v, err=%v", newAttempt, err)
		}
		newFinalized, err := fixture.repository.FinalizeSuccessfulAttempt(ctx, *newAttempt.Success)
		if err != nil || newFinalized.Success == nil {
			t.Fatalf("new finalize = %#v, err=%v", newFinalized, err)
		}
		var oldSnapshots, newSnapshots, oldCurrent, newCurrent int
		for _, query := range []struct {
			id   uuid.UUID
			name string
			out  *int
		}{
			{oldID, "old snapshots", &oldSnapshots}, {newID, "new snapshots", &newSnapshots},
		} {
			if err := fixture.database.QueryRow(ctx, `SELECT count(*) FROM gateway_directory_snapshots WHERE gateway_instance_id=$1`, query.id).Scan(query.out); err != nil {
				t.Fatal(query.name, err)
			}
		}
		if err := fixture.database.QueryRow(ctx, `SELECT count(*) FROM gateway_directory_current_state WHERE gateway_instance_id=$1`, oldID).Scan(&oldCurrent); err != nil {
			t.Fatal(err)
		}
		if err := fixture.database.QueryRow(ctx, `SELECT count(*) FROM gateway_directory_current_state WHERE gateway_instance_id=$1`, newID).Scan(&newCurrent); err != nil {
			t.Fatal(err)
		}
		if oldSnapshots != 1 || newSnapshots != 1 || oldCurrent != 1 || newCurrent != 1 {
			t.Fatalf("independent history/current = old snapshots=%d current=%d, new snapshots=%d current=%d", oldSnapshots, oldCurrent, newSnapshots, newCurrent)
		}
	})
}

func TestGatewayDirectoryLifecycleRowLockSerializationPG18(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fixture := newGatewayDirectoryLifecycleFixture(t, ctx)
	gatewayID := uuid.New()
	insertLifecycleGateway(t, ctx, fixture.database, gatewayID, "http://serialization.example", "file://directory/reader")

	lockTx, err := fixture.database.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockTx.Exec(ctx, `SELECT instance_id FROM gateway_instances WHERE instance_id=$1 FOR UPDATE`, gatewayID); err != nil {
		_ = lockTx.Rollback(ctx)
		t.Fatal(err)
	}

	retired := make(chan error, 1)
	go func() {
		_, err := fixture.assets.Retire(ctx, store.GatewayCommand{
			CommandID: uuid.New(), ActorAdminID: fixture.adminID, RequestID: "serialization-retire", InstanceID: gatewayID, ExpectedRevision: 1,
			Secret: store.SecretPatch{Operation: store.SecretAbsent},
		})
		retired <- err
	}()
	select {
	case err := <-retired:
		_ = lockTx.Rollback(ctx)
		t.Fatalf("Retire bypassed Gateway row lock: %v", err)
	case <-time.After(200 * time.Millisecond):
	}

	scheduled := make(chan struct {
		created, skipped bool
		err              error
	}, 1)
	go func() {
		_, created, skipped, err := fixture.repository.ScheduleCurrent(ctx, gatewayID)
		scheduled <- struct {
			created, skipped bool
			err              error
		}{created, skipped, err}
	}()
	if err := lockTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-retired; err != nil {
		t.Fatal(err)
	}
	result := <-scheduled
	if result.err != nil {
		t.Fatal(result.err)
	}
	if result.created || !result.skipped {
		t.Fatalf("schedule after serialized Retire = created=%v skipped=%v", result.created, result.skipped)
	}
	var runs int
	if err := fixture.database.QueryRow(ctx, `SELECT count(*) FROM gateway_directory_ingestion_runs WHERE gateway_instance_id=$1`, gatewayID).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if runs != 0 {
		t.Fatalf("serialized Retire left %d ingestion runs", runs)
	}
}
