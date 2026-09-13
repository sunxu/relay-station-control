package store_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	jobstore "github.com/sunxu/relay-station-control/internal/store"
)

type fakeGatewayDirectoryMetricsProvider struct {
	snapshot jobstore.GatewayDirectoryMetricsSnapshot
	err      error
}

func (provider fakeGatewayDirectoryMetricsProvider) GatewayDirectoryMetricsSnapshot(context.Context) (jobstore.GatewayDirectoryMetricsSnapshot, error) {
	return provider.snapshot, provider.err
}

func assertNoCanary(t *testing.T, text, canary string) {
	t.Helper()
	if strings.Contains(text, canary) {
		t.Fatalf("leaked canary %q in %q", canary, text)
	}
}

func insertGatewayDirectorySucceededRun(
	t *testing.T,
	ctx context.Context,
	pool interface {
		Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	},
	runID, gatewayID, snapshotID uuid.UUID,
	scheduledAt, startedAt, sourceGeneratedAt, receivedAt time.Time,
) {
	t.Helper()
	if _, err := pool.Exec(ctx, `INSERT INTO gateway_directory_ingestion_runs(
		ingestion_run_id, gateway_instance_id, scheduled_at, status, attempt_count,
		created_at, first_started_at, last_started_at, lease_expires_at, lease_fencing_token,
		terminal_at, outcome, last_failure_class, source_generated_at, received_at,
		content_fingerprint, snapshot_id, account_count
	) VALUES ($1,$2,$3,'succeeded',1,$4,$5,$5,NULL,NULL,$6,'changed',NULL,$7,$8,$9,$10,1)`,
		runID, gatewayID, scheduledAt.UTC(), startedAt.UTC(), startedAt.UTC(), receivedAt.UTC(), sourceGeneratedAt.UTC(), receivedAt.UTC(), make([]byte, 32), snapshotID); err != nil {
		t.Fatal(err)
	}
}

func TestGatewayDirectoryRecoveryEvidence(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()
	repository, err := jobstore.NewGatewayDirectoryIngestionRepository(database.owner)
	if err != nil {
		t.Fatal(err)
	}

	secretRef := "file://gateway-directory/reader"

	t.Run("duplicate scheduler same slot is idempotent", func(t *testing.T) {
		gatewayID := uuid.New()
		insertGatewayInstance(t, ctx, database.owner, gatewayID, "http://gateway-directory.test", secretRef)
		run1, created1, skipped1, err := repository.ScheduleCurrent(ctx, gatewayID)
		if err != nil {
			t.Fatal(err)
		}
		run2, created2, skipped2, err := repository.ScheduleCurrent(ctx, gatewayID)
		if err != nil {
			t.Fatal(err)
		}
		if !created1 || skipped1 {
			t.Fatalf("first schedule = created=%v skipped=%v", created1, skipped1)
		}
		if created2 || skipped2 {
			t.Fatalf("second schedule = created=%v skipped=%v", created2, skipped2)
		}
		if run1.IngestionRunID != run2.IngestionRunID || run1.ScheduledAt != run2.ScheduledAt {
			t.Fatalf("same-slot schedule did not reuse run: first=%#v second=%#v", run1, run2)
		}
	})

	t.Run("expired lease reclaims same durable run and stale fencing is rejected", func(t *testing.T) {
		gatewayID := uuid.New()
		insertGatewayInstance(t, ctx, database.owner, gatewayID, "http://gateway-directory.test", secretRef)
		resolver := writeGatewayDirectorySecretResolver(t, secretRef, "reader-token")
		serverCalls := 0
		body1 := gatewayDirectoryJSON(t, time.Now().UTC().Add(-time.Minute), "Alpha")
		body2 := gatewayDirectoryJSON(t, time.Now().UTC().Add(-time.Minute), "Beta")
		server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			serverCalls++
			writer.Header().Set("Content-Type", "application/json")
			if serverCalls == 1 {
				_, _ = writer.Write(body1)
				return
			}
			_, _ = writer.Write(body2)
		}))
		defer server.Close()
		trustServerCertificate(t, server)

		run, created, skipped, err := repository.ScheduleCurrent(ctx, gatewayID)
		if err != nil {
			t.Fatal(err)
		}
		if !created || skipped {
			t.Fatalf("schedule = created=%v skipped=%v", created, skipped)
		}
		claimed1, err := repository.ClaimRunnable(ctx, gatewayID, uuid.New())
		if err != nil {
			t.Fatal(err)
		}
		if claimed1 == nil || claimed1.AttemptCount != 1 {
			t.Fatalf("claimed1 = %#v", claimed1)
		}

		attempt1, err := repository.ExecuteAttempt(ctx, jobstore.GatewayDirectoryAttemptRequest{
			IngestionRunID:    uuid.UUID(claimed1.IngestionRunID.Bytes),
			GatewayInstanceID: uuid.UUID(claimed1.GatewayInstanceID.Bytes),
			LeaseFencingToken: uuid.UUID(claimed1.LeaseFencingToken.Bytes),
		}, resolver)
		if err != nil {
			t.Fatal(err)
		}
		if attempt1.Success == nil {
			t.Fatalf("attempt1 = %#v", attempt1)
		}

		if _, err := database.owner.Exec(ctx, `UPDATE gateway_directory_ingestion_runs
			SET lease_expires_at = clock_timestamp() - interval '1 second'
			WHERE ingestion_run_id=$1 AND gateway_instance_id=$2`,
			run.IngestionRunID, gatewayID); err != nil {
			t.Fatal(err)
		}
		reconciled, err := repository.ReconcileOne(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if reconciled == nil || reconciled.From != "running" || reconciled.To != "retry_wait" || reconciled.FailureClass != "lease_lost" {
			t.Fatalf("reconciled = %#v", reconciled)
		}

		claimed2, err := repository.ClaimRunnable(ctx, gatewayID, uuid.New())
		if err != nil {
			t.Fatal(err)
		}
		if claimed2 == nil || claimed2.AttemptCount != 2 || claimed2.IngestionRunID != claimed1.IngestionRunID {
			t.Fatalf("claimed2 = %#v", claimed2)
		}
		if claimed2.LeaseFencingToken == claimed1.LeaseFencingToken {
			t.Fatal("retry claim reused old fencing token")
		}

		attempt2, err := repository.ExecuteAttempt(ctx, jobstore.GatewayDirectoryAttemptRequest{
			IngestionRunID:    uuid.UUID(claimed2.IngestionRunID.Bytes),
			GatewayInstanceID: uuid.UUID(claimed2.GatewayInstanceID.Bytes),
			LeaseFencingToken: uuid.UUID(claimed2.LeaseFencingToken.Bytes),
		}, resolver)
		if err != nil {
			t.Fatal(err)
		}
		if attempt2.Success == nil {
			t.Fatalf("attempt2 = %#v", attempt2)
		}
		finalize2, err := repository.FinalizeSuccessfulAttempt(ctx, *attempt2.Success)
		if err != nil {
			t.Fatal(err)
		}
		if finalize2.Success == nil {
			t.Fatalf("finalize2 = %#v", finalize2)
		}
		snapshotAfterSecond, receivedAfterSecond, _ := loadGatewayDirectoryCurrentState(t, ctx, database.owner, gatewayID)
		if got := finalize2.Success.ReceivedAt; !got.Equal(receivedAfterSecond) {
			t.Fatalf("finalize received_at = %v, current state = %v", got, receivedAfterSecond)
		}

		staleFinalize, err := repository.FinalizeSuccessfulAttempt(ctx, *attempt1.Success)
		if err != nil {
			t.Fatal(err)
		}
		if staleFinalize.Failure == nil || staleFinalize.Failure.Reason != jobstore.GatewayDirectoryFinalizeFailureLostLease {
			t.Fatalf("stale finalize = %#v", staleFinalize)
		}
		snapshotAfterStale, receivedAfterStale, _ := loadGatewayDirectoryCurrentState(t, ctx, database.owner, gatewayID)
		if snapshotAfterStale != snapshotAfterSecond || !receivedAfterStale.Equal(receivedAfterSecond) {
			t.Fatalf("stale finalize changed current state: snapshot %v/%v received %v/%v",
				snapshotAfterSecond, snapshotAfterStale, receivedAfterSecond, receivedAfterStale)
		}
		if serverCalls != 2 {
			t.Fatalf("serverCalls = %d, want 2", serverCalls)
		}
	})
}

func TestGatewayDirectorySensitiveValuesAreNotReflected(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()
	repository, err := jobstore.NewGatewayDirectoryIngestionRepository(database.owner)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("secret reference and token", func(t *testing.T) {
		gatewayID := uuid.New()
		secretRef := "file://gateway-directory/secret-canary"
		insertGatewayInstance(t, ctx, database.owner, gatewayID, "http://gateway-directory.test", secretRef)
		requireClaimWindow(t, gatewayDirectoryCurrentSlot(t, ctx, database.owner))
		resolver := writeGatewayDirectorySecretResolver(t, "file://gateway-directory/reader", "token-canary")
		run, _, _, err := repository.ScheduleCurrent(ctx, gatewayID)
		if err != nil {
			t.Fatal(err)
		}
		claimed, err := repository.ClaimRunnable(ctx, gatewayID, uuid.New())
		if err != nil {
			t.Fatal(err)
		}
		if claimed == nil {
			t.Fatal("claim should not be nil")
		}
		result, err := repository.ExecuteAttempt(ctx, jobstore.GatewayDirectoryAttemptRequest{
			IngestionRunID:    uuid.UUID(claimed.IngestionRunID.Bytes),
			GatewayInstanceID: uuid.UUID(claimed.GatewayInstanceID.Bytes),
			LeaseFencingToken: uuid.UUID(claimed.LeaseFencingToken.Bytes),
		}, resolver)
		if err != nil {
			t.Fatal(err)
		}
		if result.Failure == nil || result.Failure.Class != "secret_unavailable" {
			t.Fatalf("result = %#v", result)
		}
		assertNoCanary(t, fmt.Sprint(result), "secret-canary")
		assertNoCanary(t, fmt.Sprint(result), "token-canary")
		var failureClass string
		if err := database.owner.QueryRow(ctx, `SELECT last_failure_class FROM gateway_directory_ingestion_runs WHERE ingestion_run_id=$1`, run.IngestionRunID).Scan(&failureClass); err != nil {
			t.Fatal(err)
		}
		assertNoCanary(t, failureClass, "secret-canary")
		assertNoCanary(t, failureClass, "token-canary")
	})

	t.Run("malformed url", func(t *testing.T) {
		gatewayID := uuid.New()
		secretRef := "file://gateway-directory/reader"
		insertGatewayInstance(t, ctx, database.owner, gatewayID, "http://gateway-canary.invalid/path", secretRef)
		requireClaimWindow(t, gatewayDirectoryCurrentSlot(t, ctx, database.owner))
		resolver := writeGatewayDirectorySecretResolver(t, secretRef, "reader-token")
		if _, _, _, err := repository.ScheduleCurrent(ctx, gatewayID); err != nil {
			t.Fatal(err)
		}
		claimed, err := repository.ClaimRunnable(ctx, gatewayID, uuid.New())
		if err != nil {
			t.Fatal(err)
		}
		if claimed == nil {
			t.Fatal("claim should not be nil")
		}
		_, err = repository.ExecuteAttempt(ctx, jobstore.GatewayDirectoryAttemptRequest{
			IngestionRunID:    uuid.UUID(claimed.IngestionRunID.Bytes),
			GatewayInstanceID: uuid.UUID(claimed.GatewayInstanceID.Bytes),
			LeaseFencingToken: uuid.UUID(claimed.LeaseFencingToken.Bytes),
		}, resolver)
		if err == nil {
			t.Fatal("malformed url should fail")
		}
		assertNoCanary(t, err.Error(), "gateway-canary")
	})

	t.Run("response body", func(t *testing.T) {
		gatewayID := uuid.New()
		secretRef := "file://gateway-directory/reader"
		requireClaimWindow(t, gatewayDirectoryCurrentSlot(t, ctx, database.owner))
		resolver := writeGatewayDirectorySecretResolver(t, secretRef, "reader-token-canary")
		serverCanary := "body-canary"
		server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusInternalServerError)
			_, _ = writer.Write([]byte(`{"error":"` + serverCanary + `"}`))
		}))
		defer server.Close()
		trustServerCertificate(t, server)
		insertGatewayInstance(t, ctx, database.owner, gatewayID, server.URL, secretRef)
		run, _, _, err := repository.ScheduleCurrent(ctx, gatewayID)
		if err != nil {
			t.Fatal(err)
		}
		claimed, err := repository.ClaimRunnable(ctx, gatewayID, uuid.New())
		if err != nil {
			t.Fatal(err)
		}
		if claimed == nil {
			t.Fatal("claim should not be nil")
		}
		result, err := repository.ExecuteAttempt(ctx, jobstore.GatewayDirectoryAttemptRequest{
			IngestionRunID:    uuid.UUID(claimed.IngestionRunID.Bytes),
			GatewayInstanceID: uuid.UUID(claimed.GatewayInstanceID.Bytes),
			LeaseFencingToken: uuid.UUID(claimed.LeaseFencingToken.Bytes),
		}, resolver)
		if err != nil {
			t.Fatal(err)
		}
		if result.Failure == nil || result.Failure.Class != "http_5xx" {
			t.Fatalf("result = %#v", result)
		}
		assertNoCanary(t, fmt.Sprint(result), serverCanary)
		var failureClass string
		if err := database.owner.QueryRow(ctx, `SELECT last_failure_class FROM gateway_directory_ingestion_runs WHERE ingestion_run_id=$1`, run.IngestionRunID).Scan(&failureClass); err != nil {
			t.Fatal(err)
		}
		if failureClass != "http_5xx" {
			t.Fatalf("failure class = %q", failureClass)
		}
		assertNoCanary(t, failureClass, serverCanary)
	})
}

func TestGatewayDirectoryMetricsSnapshotAndCollector(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()
	repository, err := jobstore.NewGatewayDirectoryIngestionRepository(database.owner)
	if err != nil {
		t.Fatal(err)
	}

	freshGatewayID := uuid.New()
	staleGatewayID := uuid.New()
	unknownGatewayID := uuid.New()
	secretRef := "file://gateway-directory/reader"
	insertGatewayInstance(t, ctx, database.owner, freshGatewayID, "http://fresh.gateway.test", secretRef)
	insertGatewayInstance(t, ctx, database.owner, staleGatewayID, "http://stale.gateway.test", secretRef)
	insertGatewayInstance(t, ctx, database.owner, unknownGatewayID, "http://unknown.gateway.test", secretRef)

	currentSlot := requireClaimWindow(t, gatewayDirectoryCurrentSlot(t, ctx, database.owner))
	insertGatewayDirectoryPendingRun(t, ctx, database.owner, uuid.New(), freshGatewayID, currentSlot)
	insertGatewayDirectoryRunningRun(t, ctx, database.owner, uuid.New(), staleGatewayID, uuid.New(),
		currentSlot.Add(-360*time.Second), time.Now().UTC().Add(-2*time.Second), 1, "")
	staleSnapshotID := uuid.New()
	if _, err := database.owner.Exec(ctx, `INSERT INTO gateway_directory_snapshots(
		snapshot_id, gateway_instance_id, fingerprint, fingerprint_encoding_version, schema_version, account_count
	) VALUES ($1,$2,$3,1,1,1)`,
		staleSnapshotID, staleGatewayID, make([]byte, 32)); err != nil {
		t.Fatal(err)
	}
	freshSnapshotID := uuid.New()
	if _, err := database.owner.Exec(ctx, `INSERT INTO gateway_directory_snapshots(
		snapshot_id, gateway_instance_id, fingerprint, fingerprint_encoding_version, schema_version, account_count
	) VALUES ($1,$2,$3,1,1,1)`,
		freshSnapshotID, freshGatewayID, make([]byte, 32)); err != nil {
		t.Fatal(err)
	}
	staleRunID := uuid.New()
	staleSuccessAt := time.Now().UTC().Add(-10 * time.Minute)
	insertGatewayDirectorySucceededRun(t, ctx, database.owner, staleRunID, staleGatewayID, staleSnapshotID,
		currentSlot.Add(-720*time.Second), staleSuccessAt.Add(-2*time.Second), staleSuccessAt.Add(-time.Minute), staleSuccessAt)
	freshRunID := uuid.New()
	freshSuccessAt := time.Now().UTC().Add(-30 * time.Second)
	insertGatewayDirectorySucceededRun(t, ctx, database.owner, freshRunID, freshGatewayID, freshSnapshotID,
		currentSlot.Add(-540*time.Second), freshSuccessAt.Add(-2*time.Second), freshSuccessAt.Add(-time.Minute), freshSuccessAt)
	unknownRunID := uuid.New()
	unknownStartedAt := time.Now().UTC().Add(-2 * time.Second)
	if _, err := database.owner.Exec(ctx, `INSERT INTO gateway_directory_ingestion_runs(
		ingestion_run_id, gateway_instance_id, scheduled_at, status, attempt_count,
		created_at, first_started_at, last_started_at, terminal_at, outcome, last_failure_class,
		source_generated_at, received_at, content_fingerprint, snapshot_id, account_count
	) VALUES ($1,$2,$3,'failed',1,$4,$4,$4,$4,NULL,'http_5xx',NULL,NULL,NULL,NULL,NULL)`,
		unknownRunID, unknownGatewayID, currentSlot.Add(-900*time.Second), unknownStartedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO gateway_directory_current_state(
		gateway_instance_id, current_snapshot_id, current_content_fingerprint,
		last_success_received_at, last_source_generated_at, last_success_run_id, updated_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		staleGatewayID, staleSnapshotID, make([]byte, 32), staleSuccessAt, staleSuccessAt.Add(-time.Minute), staleRunID, staleSuccessAt); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO gateway_directory_current_state(
		gateway_instance_id, current_snapshot_id, current_content_fingerprint,
		last_success_received_at, last_source_generated_at, last_success_run_id, updated_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		freshGatewayID, freshSnapshotID, make([]byte, 32), freshSuccessAt, freshSuccessAt.Add(-time.Minute), freshRunID, freshSuccessAt); err != nil {
		t.Fatal(err)
	}

	snapshot, err := repository.GatewayDirectoryMetricsSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.RunStatusCounts["pending"] != 1 || snapshot.RunStatusCounts["running"] != 1 || snapshot.RunStatusCounts["failed"] != 1 {
		t.Fatalf("run status counts = %#v", snapshot.RunStatusCounts)
	}
	if snapshot.FailureClassCounts["http_5xx"] != 1 {
		t.Fatalf("failure class counts = %#v", snapshot.FailureClassCounts)
	}
	if snapshot.FreshnessCounts["fresh"] != 1 || snapshot.FreshnessCounts["stale"] != 1 || snapshot.FreshnessCounts["unknown"] != 1 {
		t.Fatalf("freshness counts = %#v", snapshot.FreshnessCounts)
	}

	metrics, err := jobstore.NewGatewayDirectoryMetrics(fakeGatewayDirectoryMetricsProvider{
		snapshot: jobstore.GatewayDirectoryMetricsSnapshot{
			RunStatusCounts: map[string]int64{
				"pending": 1, "running": 2, "retry_wait": 3, "succeeded": 4, "failed": 5,
				"gateway-canary": 9,
			},
			FailureClassCounts: map[string]int64{
				"http_5xx": 1, "lease_lost": 2, "secret-canary": 99,
			},
			FreshnessCounts: map[string]int64{
				"fresh": 1, "stale": 2, "unknown": 3, "token-canary": 4,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(metrics)
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	encoded := ""
	allowed := map[string]map[string]bool{
		"relay_control_gateway_directory_runs": {
			"status=pending": true, "status=running": true, "status=retry_wait": true, "status=succeeded": true, "status=failed": true,
		},
		"relay_control_gateway_directory_failures": {
			"failure_class=transport": true, "failure_class=timeout": true, "failure_class=partial_read": true,
			"failure_class=http_429": true, "failure_class=http_5xx": true, "failure_class=http_non_retryable": true,
			"failure_class=contract_invalid": true, "failure_class=source_time_invalid": true, "failure_class=hard_limit": true,
			"failure_class=gateway_retired": true, "failure_class=gateway_replaced": true,
			"failure_class=secret_unavailable": true, "failure_class=finalize_transient": true, "failure_class=lease_lost": true,
			"failure_class=unknown_execution": true, "failure_class=start_deadline_expired": true,
		},
		"relay_control_gateway_directory_freshness": {
			"freshness=fresh": true, "freshness=stale": true, "freshness=unknown": true,
		},
	}
	for _, family := range families {
		encoded += family.String()
		allowedLabels, ok := allowed[family.GetName()]
		if !ok {
			t.Fatalf("unexpected metric family %q", family.GetName())
		}
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				pair := label.GetName() + "=" + label.GetValue()
				if !allowedLabels[pair] {
					t.Fatalf("unexpected label %q in %s", pair, family.GetName())
				}
			}
		}
	}
	for _, canary := range []string{"gateway-canary", "secret-canary", "token-canary"} {
		assertNoCanary(t, encoded, canary)
	}
	if count := testutil.CollectAndCount(metrics); count == 0 {
		t.Fatal("gateway directory metrics were not collected")
	}
}
