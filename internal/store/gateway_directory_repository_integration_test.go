package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/sunxu/relay-station-control/internal/drivers/gatewaydirectory"
	jobstore "github.com/sunxu/relay-station-control/internal/store"
	generated "github.com/sunxu/relay-station-control/internal/store/sqlc"
)

type directoryServerState struct {
	mu     sync.Mutex
	status int
	body   []byte
}

func (state *directoryServerState) set(status int, body []byte) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.status = status
	state.body = append([]byte(nil), body...)
}

func (state *directoryServerState) snapshot() (int, []byte) {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.status, append([]byte(nil), state.body...)
}

func makeDirectoryFixture(t *testing.T, generatedAt time.Time, name string) (gatewaydirectory.DirectoryResponse, []byte) {
	t.Helper()
	response := gatewaydirectory.DirectoryResponse{
		SchemaVersion: 1,
		GeneratedAt:   generatedAt.UTC(),
		Accounts: []gatewaydirectory.Account{{
			ID:       1,
			Name:     name,
			Platform: "linux",
			Type:     "apikey",
			URL:      nil,
			Status:   "active",
		}},
	}
	body, err := json.Marshal(map[string]any{
		"schema_version": 1,
		"generated_at":   generatedAt.UTC().Format(time.RFC3339Nano),
		"accounts": []map[string]any{{
			"id":       1,
			"name":     name,
			"platform": "linux",
			"type":     "apikey",
			"url":      nil,
			"status":   "active",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return response, body
}

func insertGatewayDirectoryPendingRun(t *testing.T, ctx context.Context, pool interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, runID, gatewayID uuid.UUID, scheduledAt time.Time) {
	t.Helper()
	if _, err := pool.Exec(ctx, `INSERT INTO gateway_directory_ingestion_runs(
		ingestion_run_id, gateway_instance_id, scheduled_at, created_at
	) VALUES ($1,$2,$3,$4)`, runID, gatewayID, scheduledAt.UTC(), scheduledAt.UTC()); err != nil {
		t.Fatal(err)
	}
}

func insertGatewayDirectoryRunningRun(t *testing.T, ctx context.Context, pool interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, runID, gatewayID, leaseToken uuid.UUID, scheduledAt, startedAt time.Time, attemptCount int, failureClass string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `INSERT INTO gateway_directory_ingestion_runs(
		ingestion_run_id, gateway_instance_id, scheduled_at, status, attempt_count,
		created_at, first_started_at, last_started_at, lease_expires_at,
		lease_fencing_token, last_failure_class
	) VALUES (
		$1,$2,$3,'running',$4,
		$5,$6,$7,$8,
		$9,CASE WHEN $10 = '' THEN NULL ELSE $10 END
	)`,
		runID, gatewayID, scheduledAt.UTC(), attemptCount,
		startedAt.UTC(), startedAt.UTC(), startedAt.UTC(), startedAt.Add(15*time.Second).UTC(),
		leaseToken, failureClass,
	); err != nil {
		t.Fatal(err)
	}
}

func insertGatewayDirectoryRetryWaitRun(t *testing.T, ctx context.Context, pool interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, runID, gatewayID uuid.UUID, scheduledAt, startedAt time.Time, failureClass string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `INSERT INTO gateway_directory_ingestion_runs(
		ingestion_run_id, gateway_instance_id, scheduled_at, status, attempt_count,
		created_at, first_started_at, last_started_at, last_failure_class
	) VALUES (
		$1,$2,$3,'retry_wait',1,
		$4,$5,$6,$7
	)`,
		runID, gatewayID, scheduledAt.UTC(),
		startedAt.UTC(), startedAt.UTC(), startedAt.UTC(), failureClass,
	); err != nil {
		t.Fatal(err)
	}
}

func requireClaimWindow(t *testing.T, slot time.Time) time.Time {
	t.Helper()
	elapsed := time.Since(slot)
	if elapsed < 0 || elapsed >= 120*time.Second {
		t.Skip("current DB slot is outside the claim window for this test")
	}
	return slot
}

func loadGatewayDirectoryCurrentState(t *testing.T, ctx context.Context, query interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, gatewayID uuid.UUID) (uuid.UUID, time.Time, time.Time) {
	t.Helper()
	var snapshotID uuid.UUID
	var receivedAt time.Time
	var updatedAt time.Time
	if err := query.QueryRow(ctx, `SELECT current_snapshot_id, last_success_received_at, updated_at
		FROM gateway_directory_current_state WHERE gateway_instance_id=$1`, gatewayID).Scan(&snapshotID, &receivedAt, &updatedAt); err != nil {
		t.Fatal(err)
	}
	return snapshotID, receivedAt.UTC(), updatedAt.UTC()
}

func TestGatewayDirectoryRepositoryWorkflowAndRecovery(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()
	repository, err := jobstore.NewGatewayDirectoryIngestionRepository(database.owner)
	if err != nil {
		t.Fatal(err)
	}

	gatewayID := uuid.New()
	secretRef := "file://gateway-directory/reader"
	insertGatewayInstance(t, ctx, database.owner, gatewayID, "https://gateway-directory.test", secretRef)
	resolver := writeGatewayDirectorySecretResolver(t, secretRef, "reader-token")
	serverState := &directoryServerState{}
	serverState.set(http.StatusOK, []byte(`{"schema_version":1,"generated_at":"`+time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)+`","accounts":[{"id":1,"name":"Alpha","platform":"linux","type":"apikey","url":null,"status":"active"}]}`))
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		status, body := serverState.snapshot()
		if request.URL.Path != "/internal/v1/api-account-directory" {
			t.Fatalf("path = %s", request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer reader-token" {
			t.Fatalf("authorization = %q", got)
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(status)
		_, _ = writer.Write(body)
	}))
	defer server.Close()
	trustServerCertificate(t, server)

	currentSlot := gatewayDirectoryCurrentSlot(t, ctx, database.owner)

	t.Run("scheduler claim failure retry and stale token", func(t *testing.T) {
		run, created, skipped, err := repository.ScheduleCurrent(ctx, gatewayID)
		if err != nil {
			t.Fatal(err)
		}
		if !created || skipped {
			t.Fatalf("schedule = created=%v skipped=%v", created, skipped)
		}
		if uuid.UUID(run.IngestionRunID.Bytes) == uuid.Nil {
			t.Fatal("schedule returned nil run id")
		}

		claimed, err := repository.ClaimRunnable(ctx, gatewayID, uuid.New())
		if err != nil {
			t.Fatal(err)
		}
		if claimed == nil || claimed.Status != "running" || claimed.AttemptCount != 1 {
			t.Fatalf("claimed = %#v", claimed)
		}
		if lease, err := repository.LeaseValid(ctx, uuid.UUID(claimed.IngestionRunID.Bytes), uuid.UUID(claimed.GatewayInstanceID.Bytes), uuid.UUID(claimed.LeaseFencingToken.Bytes)); err != nil {
			t.Fatal(err)
		} else if lease == nil {
			t.Fatal("lease should be valid while running")
		}

		_, err = repository.ExecuteAttempt(ctx, jobstore.GatewayDirectoryAttemptRequest{
			IngestionRunID:    uuid.UUID(claimed.IngestionRunID.Bytes),
			GatewayInstanceID: uuid.UUID(claimed.GatewayInstanceID.Bytes),
			LeaseFencingToken: uuid.New(),
		}, resolver)
		if err != nil {
			t.Fatal(err)
		}

		serverState.set(http.StatusInternalServerError, []byte(`{"error":"boom"}`))
		result, err := repository.ExecuteAttempt(ctx, jobstore.GatewayDirectoryAttemptRequest{
			IngestionRunID:    uuid.UUID(claimed.IngestionRunID.Bytes),
			GatewayInstanceID: uuid.UUID(claimed.GatewayInstanceID.Bytes),
			LeaseFencingToken: uuid.UUID(claimed.LeaseFencingToken.Bytes),
		}, resolver)
		if err != nil {
			t.Fatal(err)
		}
		if result.Success != nil || result.Failure == nil || result.Failure.Disposition != jobstore.GatewayDirectoryAttemptRetryWait {
			t.Fatalf("result = %#v", result)
		}

		claimedAgain, err := repository.ClaimRunnable(ctx, gatewayID, uuid.New())
		if err != nil {
			t.Fatal(err)
		}
		if claimedAgain == nil {
			t.Fatal("retry_wait run should be claimable again")
		}
		result, err = repository.ExecuteAttempt(ctx, jobstore.GatewayDirectoryAttemptRequest{
			IngestionRunID:    uuid.UUID(claimedAgain.IngestionRunID.Bytes),
			GatewayInstanceID: uuid.UUID(claimedAgain.GatewayInstanceID.Bytes),
			LeaseFencingToken: uuid.UUID(claimedAgain.LeaseFencingToken.Bytes),
		}, resolver)
		if err != nil {
			t.Fatal(err)
		}
		if result.Success != nil || result.Failure == nil || result.Failure.Disposition != jobstore.GatewayDirectoryAttemptFailed {
			t.Fatalf("result = %#v", result)
		}
		if lease, err := repository.LeaseValid(ctx, uuid.UUID(claimedAgain.IngestionRunID.Bytes), uuid.UUID(claimedAgain.GatewayInstanceID.Bytes), uuid.UUID(claimedAgain.LeaseFencingToken.Bytes)); err != nil {
			t.Fatal(err)
		} else if lease != nil {
			t.Fatal("terminal failure should clear the lease")
		}
	})

	t.Run("schedule current concurrency", func(t *testing.T) {
		gatewayID := uuid.New()
		insertGatewayInstance(t, ctx, database.owner, gatewayID, "https://gateway-directory-concurrency.test", secretRef)
		start := make(chan struct{})
		errs := make(chan error, 2)
		results := make(chan bool, 2)
		for i := 0; i < 2; i++ {
			go func() {
				<-start
				_, created, skipped, err := repository.ScheduleCurrent(ctx, gatewayID)
				if err != nil {
					errs <- err
					return
				}
				results <- created && !skipped
			}()
		}
		close(start)
		var createdCount int
		for i := 0; i < 2; i++ {
			select {
			case err := <-errs:
				t.Fatal(err)
			case created := <-results:
				if created {
					createdCount++
				}
			case <-time.After(15 * time.Second):
				t.Fatal("timed out waiting for schedule concurrency")
			}
		}
		if createdCount != 1 {
			t.Fatalf("created count = %d, want 1", createdCount)
		}
		var activeCount int
		if err := database.owner.QueryRow(ctx, `SELECT count(*)
			FROM gateway_directory_ingestion_runs
			WHERE gateway_instance_id=$1 AND scheduled_at=$2 AND status IN ('pending','running','retry_wait')`,
			gatewayID, currentSlot).Scan(&activeCount); err != nil {
			t.Fatal(err)
		}
		if activeCount != 1 {
			t.Fatalf("active count = %d, want 1", activeCount)
		}
	})

	t.Run("claim runnable concurrency", func(t *testing.T) {
		requireClaimWindow(t, currentSlot)
		gatewayID := uuid.New()
		insertGatewayInstance(t, ctx, database.owner, gatewayID, "https://gateway-directory-claim.test", secretRef)
		runID := uuid.New()
		insertGatewayDirectoryPendingRun(t, ctx, database.owner, runID, gatewayID, currentSlot)
		start := make(chan struct{})
		errs := make(chan error, 2)
		results := make(chan *generated.GatewayDirectoryIngestionRun, 2)
		for i := 0; i < 2; i++ {
			go func() {
				<-start
				claimed, err := repository.ClaimRunnable(ctx, gatewayID, uuid.New())
				if err != nil {
					errs <- err
					return
				}
				results <- claimed
			}()
		}
		close(start)
		var claimedCount int
		for i := 0; i < 2; i++ {
			select {
			case err := <-errs:
				t.Fatal(err)
			case claimed := <-results:
				if claimed != nil {
					claimedCount++
				}
			case <-time.After(15 * time.Second):
				t.Fatal("timed out waiting for claim concurrency")
			}
		}
		if claimedCount != 1 {
			t.Fatalf("claimed count = %d, want 1", claimedCount)
		}
		var attemptCount int
		if err := database.owner.QueryRow(ctx, `SELECT attempt_count
			FROM gateway_directory_ingestion_runs
			WHERE gateway_instance_id=$1 AND ingestion_run_id=$2`,
			gatewayID, runID).Scan(&attemptCount); err != nil {
			t.Fatal(err)
		}
		if attemptCount != 1 {
			t.Fatalf("attempt_count = %d, want 1", attemptCount)
		}
	})

	t.Run("finalize changed unchanged source-time invalid and lost lease", func(t *testing.T) {
		fresh := time.Now().UTC().Add(-time.Minute)
		startedAt := time.Now().UTC().Add(-5 * time.Second)

		runID1 := uuid.New()
		leaseToken1 := uuid.New()
		insertGatewayDirectoryRunningRun(t, ctx, database.owner, runID1, gatewayID, leaseToken1,
			currentSlot.Add(-180*time.Second), startedAt, 1, "")
		success1, _ := makeDirectoryFixture(t, fresh, "Alpha")
		finalize1, err := repository.FinalizeSuccessfulAttempt(ctx, jobstore.GatewayDirectoryAttemptSuccess{
			Request: jobstore.GatewayDirectoryAttemptRequest{
				IngestionRunID:    runID1,
				GatewayInstanceID: gatewayID,
				LeaseFencingToken: leaseToken1,
			},
			Directory:    success1,
			GeneratedAt:  success1.GeneratedAt,
			Fingerprint:  success1.FingerprintV1(),
			AccountCount: len(success1.Accounts),
		})
		if err != nil {
			t.Fatal(err)
		}
		if finalize1.Success == nil || finalize1.Success.Outcome != jobstore.GatewayDirectoryFinalizeOutcomeChanged {
			t.Fatalf("finalize1 = %#v", finalize1)
		}
		firstSnapshotID, firstReceivedAt, _ := loadGatewayDirectoryCurrentState(t, ctx, database.owner, gatewayID)
		var snapshotCount int
		if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM gateway_directory_snapshots WHERE gateway_instance_id=$1`, gatewayID).Scan(&snapshotCount); err != nil {
			t.Fatal(err)
		}

		runID2 := uuid.New()
		leaseToken2 := uuid.New()
		insertGatewayDirectoryRunningRun(t, ctx, database.owner, runID2, gatewayID, leaseToken2,
			currentSlot.Add(-360*time.Second), time.Now().UTC().Add(-4*time.Second), 1, "")
		finalize2, err := repository.FinalizeSuccessfulAttempt(ctx, jobstore.GatewayDirectoryAttemptSuccess{
			Request: jobstore.GatewayDirectoryAttemptRequest{
				IngestionRunID:    runID2,
				GatewayInstanceID: gatewayID,
				LeaseFencingToken: leaseToken2,
			},
			Directory:    success1,
			GeneratedAt:  success1.GeneratedAt,
			Fingerprint:  success1.FingerprintV1(),
			AccountCount: len(success1.Accounts),
		})
		if err != nil {
			t.Fatal(err)
		}
		if finalize2.Success == nil || finalize2.Success.Outcome != jobstore.GatewayDirectoryFinalizeOutcomeUnchanged || finalize2.Success.SnapshotID != firstSnapshotID {
			t.Fatalf("finalize2 = %#v", finalize2)
		}
		if got := finalize2.Success.ReceivedAt; !got.After(firstReceivedAt) {
			t.Fatalf("unchanged received_at = %v, want after %v", got, firstReceivedAt)
		}
		snapshotIDAfterUnchanged, unchangedReceivedAt, _ := loadGatewayDirectoryCurrentState(t, ctx, database.owner, gatewayID)
		if snapshotIDAfterUnchanged != firstSnapshotID {
			t.Fatalf("snapshot changed on unchanged finalize: %v vs %v", snapshotIDAfterUnchanged, firstSnapshotID)
		}
		if !unchangedReceivedAt.After(firstReceivedAt) {
			t.Fatalf("last_success_received_at did not advance: %v vs %v", unchangedReceivedAt, firstReceivedAt)
		}
		var snapshotCountAfterUnchanged int
		if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM gateway_directory_snapshots WHERE gateway_instance_id=$1`, gatewayID).Scan(&snapshotCountAfterUnchanged); err != nil {
			t.Fatal(err)
		}
		if snapshotCountAfterUnchanged != snapshotCount {
			t.Fatalf("snapshot count changed on unchanged finalize: %d vs %d", snapshotCountAfterUnchanged, snapshotCount)
		}

		runID3 := uuid.New()
		leaseToken3 := uuid.New()
		changed := gatewaydirectory.DirectoryResponse{
			SchemaVersion: 1,
			GeneratedAt:   fresh.Add(30 * time.Second),
			Accounts: []gatewaydirectory.Account{{
				ID: 1, Name: "Beta", Platform: "linux", Type: "apikey", Status: "active",
			}},
		}
		insertGatewayDirectoryRunningRun(t, ctx, database.owner, runID3, gatewayID, leaseToken3,
			currentSlot.Add(-540*time.Second), time.Now().UTC().Add(-3*time.Second), 1, "")
		finalize3, err := repository.FinalizeSuccessfulAttempt(ctx, jobstore.GatewayDirectoryAttemptSuccess{
			Request: jobstore.GatewayDirectoryAttemptRequest{
				IngestionRunID:    runID3,
				GatewayInstanceID: gatewayID,
				LeaseFencingToken: leaseToken3,
			},
			Directory:    changed,
			GeneratedAt:  changed.GeneratedAt,
			Fingerprint:  changed.FingerprintV1(),
			AccountCount: len(changed.Accounts),
		})
		if err != nil {
			t.Fatal(err)
		}
		if finalize3.Success == nil || finalize3.Success.Outcome != jobstore.GatewayDirectoryFinalizeOutcomeChanged {
			t.Fatalf("finalize3 = %#v", finalize3)
		}
		snapshotIDAfterChanged, changedReceivedAt, _ := loadGatewayDirectoryCurrentState(t, ctx, database.owner, gatewayID)
		if snapshotIDAfterChanged == firstSnapshotID {
			t.Fatal("changed finalize reused old snapshot")
		}
		if !changedReceivedAt.After(unchangedReceivedAt) {
			t.Fatalf("received_at did not advance on changed finalize: %v vs %v", changedReceivedAt, unchangedReceivedAt)
		}

		runID4 := uuid.New()
		leaseToken4 := uuid.New()
		insertGatewayDirectoryRunningRun(t, ctx, database.owner, runID4, gatewayID, leaseToken4,
			currentSlot.Add(-720*time.Second), time.Now().UTC().Add(-2*time.Second), 1, "")
		finalize4, err := repository.FinalizeSuccessfulAttempt(ctx, jobstore.GatewayDirectoryAttemptSuccess{
			Request: jobstore.GatewayDirectoryAttemptRequest{
				IngestionRunID:    runID4,
				GatewayInstanceID: gatewayID,
				LeaseFencingToken: leaseToken4,
			},
			Directory:    success1,
			GeneratedAt:  success1.GeneratedAt,
			Fingerprint:  success1.FingerprintV1(),
			AccountCount: len(success1.Accounts),
		})
		if err != nil {
			t.Fatal(err)
		}
		if finalize4.Success == nil || finalize4.Success.Outcome != jobstore.GatewayDirectoryFinalizeOutcomeChanged || finalize4.Success.SnapshotID != firstSnapshotID {
			t.Fatalf("finalize4 = %#v", finalize4)
		}
		snapshotIDAfterReuse, reuseReceivedAt, _ := loadGatewayDirectoryCurrentState(t, ctx, database.owner, gatewayID)
		if snapshotIDAfterReuse != firstSnapshotID {
			t.Fatalf("snapshot reuse mismatch: %v vs %v", snapshotIDAfterReuse, firstSnapshotID)
		}
		if !reuseReceivedAt.After(changedReceivedAt) {
			t.Fatalf("received_at did not advance on A→B→A reuse: %v vs %v", reuseReceivedAt, changedReceivedAt)
		}
		var snapshotCountAfterReuse int
		if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM gateway_directory_snapshots WHERE gateway_instance_id=$1`, gatewayID).Scan(&snapshotCountAfterReuse); err != nil {
			t.Fatal(err)
		}
		if snapshotCountAfterReuse != 2 {
			t.Fatalf("snapshot count after reuse = %d, want 2", snapshotCountAfterReuse)
		}

		invalidRunID := uuid.New()
		leaseTokenInvalid := uuid.New()
		insertGatewayDirectoryRunningRun(t, ctx, database.owner, invalidRunID, gatewayID, leaseTokenInvalid,
			currentSlot.Add(-900*time.Second), time.Now().UTC().Add(-time.Second), 1, "")
		invalid := jobstore.GatewayDirectoryAttemptSuccess{
			Request: jobstore.GatewayDirectoryAttemptRequest{
				IngestionRunID:    invalidRunID,
				GatewayInstanceID: gatewayID,
				LeaseFencingToken: leaseTokenInvalid,
			},
			Directory:    success1,
			GeneratedAt:  time.Now().UTC().Add(-25 * time.Hour),
			Fingerprint:  success1.FingerprintV1(),
			AccountCount: len(success1.Accounts),
		}
		finalizeInvalid, err := repository.FinalizeSuccessfulAttempt(ctx, invalid)
		if err != nil {
			t.Fatal(err)
		}
		if finalizeInvalid.Failure == nil || finalizeInvalid.Failure.Reason != jobstore.GatewayDirectoryFinalizeFailureSourceTimeInvalid {
			t.Fatalf("finalizeInvalid = %#v", finalizeInvalid)
		}
		snapshotBeforeInvalid, receivedBeforeInvalid, _ := loadGatewayDirectoryCurrentState(t, ctx, database.owner, gatewayID)

		lostLease := invalid
		lostLease.Request.LeaseFencingToken = uuid.New()
		finalizeLost, err := repository.FinalizeSuccessfulAttempt(ctx, lostLease)
		if err != nil {
			t.Fatal(err)
		}
		if finalizeLost.Failure == nil || finalizeLost.Failure.Reason != jobstore.GatewayDirectoryFinalizeFailureLostLease {
			t.Fatalf("finalizeLost = %#v", finalizeLost)
		}
		snapshotAfterLost, receivedAfterLost, _ := loadGatewayDirectoryCurrentState(t, ctx, database.owner, gatewayID)
		if snapshotAfterLost != snapshotBeforeInvalid || !receivedAfterLost.Equal(receivedBeforeInvalid) {
			t.Fatalf("state changed on invalid/lost lease: snapshot %v/%v received %v/%v",
				snapshotBeforeInvalid, snapshotAfterLost, receivedBeforeInvalid, receivedAfterLost)
		}

		failureRunID := uuid.New()
		leaseTokenFailure := uuid.New()
		insertGatewayDirectoryRunningRun(t, ctx, database.owner, failureRunID, gatewayID, leaseTokenFailure,
			currentSlot.Add(-1080*time.Second), time.Now().UTC().Add(-2*time.Second), 1, "")
		beforeFailureSnapshot, beforeFailureReceived, _ := loadGatewayDirectoryCurrentState(t, ctx, database.owner, gatewayID)
		serverState.set(http.StatusInternalServerError, []byte(`{"error":"boom"}`))
		_, err = repository.ExecuteAttempt(ctx, jobstore.GatewayDirectoryAttemptRequest{
			IngestionRunID:    failureRunID,
			GatewayInstanceID: gatewayID,
			LeaseFencingToken: leaseTokenFailure,
		}, resolver)
		if err != nil {
			t.Fatal(err)
		}
		afterFailureSnapshot, afterFailureReceived, _ := loadGatewayDirectoryCurrentState(t, ctx, database.owner, gatewayID)
		if afterFailureSnapshot != beforeFailureSnapshot || !afterFailureReceived.Equal(beforeFailureReceived) {
			t.Fatalf("current state changed on failed attempt: %v/%v -> %v/%v",
				beforeFailureSnapshot, beforeFailureReceived, afterFailureSnapshot, afterFailureReceived)
		}
	})

	t.Run("reconcile expired running pending retry_wait and concurrency", func(t *testing.T) {
		retryWindowSlot := gatewayDirectoryCurrentSlot(t, ctx, database.owner)
		elapsed := time.Since(retryWindowSlot)
		if elapsed < 15*time.Second || elapsed >= 120*time.Second {
			t.Skip("current slot is outside a stable retry_wait window")
		}

		runningRunID := uuid.New()
		insertGatewayDirectoryRunningRun(t, ctx, database.owner, runningRunID, gatewayID, uuid.New(),
			retryWindowSlot, retryWindowSlot, 1, "")
		result, err := repository.ReconcileOne(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if result == nil || result.From != "running" || result.To != "retry_wait" || result.FailureClass != "lease_lost" {
			t.Fatalf("reconcile retry_wait = %#v", result)
		}

		pendingRunID := uuid.New()
		insertGatewayDirectoryPendingRun(t, ctx, database.owner, pendingRunID, gatewayID, retryWindowSlot.Add(-180*time.Second))
		result, err = repository.ReconcileOne(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if result == nil || result.From != "pending" || result.To != "failed" || result.FailureClass != "start_deadline_expired" {
			t.Fatalf("reconcile pending = %#v", result)
		}

		noRetryRunID := uuid.New()
		insertGatewayDirectoryRunningRun(t, ctx, database.owner, noRetryRunID, gatewayID, uuid.New(),
			retryWindowSlot.Add(-360*time.Second), retryWindowSlot.Add(-360*time.Second), 2, "lease_lost")
		result, err = repository.ReconcileOne(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if result == nil || result.From != "running" || result.To != "failed" || result.FailureClass != "lease_lost" {
			t.Fatalf("reconcile no retry = %#v", result)
		}

		retryWaitRunID := uuid.New()
		insertGatewayDirectoryRetryWaitRun(t, ctx, database.owner, retryWaitRunID, gatewayID,
			retryWindowSlot.Add(-180*time.Second), retryWindowSlot.Add(-180*time.Second), "lease_lost")
		result, err = repository.ReconcileOne(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if result == nil || result.From != "retry_wait" || result.To != "failed" || result.FailureClass != "lease_lost" {
			t.Fatalf("reconcile failed = %#v", result)
		}

		oneRunID := uuid.New()
		insertGatewayDirectoryRunningRun(t, ctx, database.owner, oneRunID, gatewayID, uuid.New(),
			retryWindowSlot.Add(-360*time.Second), retryWindowSlot.Add(-360*time.Second), 1, "")
		results := make(chan *jobstore.GatewayDirectoryReconcileResult, 2)
		errs := make(chan error, 2)
		for i := 0; i < 2; i++ {
			go func() {
				result, err := repository.ReconcileOne(ctx)
				if err != nil {
					errs <- err
					return
				}
				results <- result
			}()
		}
		var wins int
		for i := 0; i < 2; i++ {
			select {
			case err := <-errs:
				t.Fatal(err)
			case result := <-results:
				if result != nil {
					wins++
				}
			case <-time.After(15 * time.Second):
				t.Fatal("timed out waiting for concurrent reconcilers")
			}
		}
		if wins != 1 {
			t.Fatalf("concurrent reconciler wins = %d, want 1", wins)
		}
	})

	if _, err := repository.ExecuteAttempt(ctx, jobstore.GatewayDirectoryAttemptRequest{}, nil); !errors.Is(err, jobstore.ErrInvalidGatewayDirectoryIngestionQuery) {
		t.Fatalf("invalid request error = %v", err)
	}
}
