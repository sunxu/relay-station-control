package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	store "github.com/sunxu/relay-station-control/internal/store"
)

func TestGatewayDirectoryHistoricalNullRunIsReconciledWithoutNewSchedule(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()
	gatewayID := uuid.New()
	secretRef := "file://historical-null/reader"
	insertGatewayInstance(t, ctx, database.owner, gatewayID, "http://historical-null.test", secretRef)
	waitDirectoryRuntimeClaimWindow(t, database)
	repository, err := store.NewGatewayDirectoryIngestionRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	run, created, skipped, err := repository.ScheduleCurrent(ctx, gatewayID)
	if err != nil || !created || skipped {
		t.Fatalf("initial schedule = run=%+v created=%v skipped=%v err=%v", run, created, skipped, err)
	}
	claimed, err := repository.ClaimRunnable(ctx, gatewayID, uuid.New())
	if err != nil || claimed == nil {
		t.Fatalf("initial claim = run=%+v err=%v", claimed, err)
	}
	if claimed.IngestionRunID != run.IngestionRunID {
		t.Fatalf("claim changed durable run: got=%v want=%v", claimed.IngestionRunID, run.IngestionRunID)
	}
	if _, err := database.owner.Exec(ctx, `UPDATE gateway_instances SET reader_secret_ref = NULL WHERE instance_id = $1`, gatewayID); err != nil {
		t.Fatal(err)
	}
	if _, created, skipped, err := repository.ScheduleCurrent(ctx, gatewayID); err != nil || created || !skipped {
		t.Fatalf("historical NULL schedule = created=%v skipped=%v err=%v", created, skipped, err)
	}
	var runCount int
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM gateway_directory_ingestion_runs WHERE gateway_instance_id=$1`, gatewayID).Scan(&runCount); err != nil {
		t.Fatal(err)
	}
	if runCount != 1 {
		t.Fatalf("historical NULL created a new run: count=%d", runCount)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		var expired bool
		if err := database.owner.QueryRow(ctx, `SELECT lease_expires_at <= clock_timestamp() FROM gateway_directory_ingestion_runs WHERE ingestion_run_id=$1`, run.IngestionRunID).Scan(&expired); err != nil {
			t.Fatal(err)
		}
		if expired {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("historical run lease did not expire")
		}
		time.Sleep(100 * time.Millisecond)
	}
	reconciled, err := repository.ReconcileOne(ctx)
	if err != nil || reconciled == nil {
		t.Fatalf("historical NULL reconcile = %+v err=%v", reconciled, err)
	}
	if reconciled.RunID != uuid.UUID(run.IngestionRunID.Bytes) || reconciled.From != "running" || reconciled.FailureClass != "lease_lost" || (reconciled.To != "retry_wait" && reconciled.To != "failed") {
		t.Fatalf("historical NULL reconciled wrong run/state: %+v", reconciled)
	}
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM gateway_directory_ingestion_runs WHERE gateway_instance_id=$1`, gatewayID).Scan(&runCount); err != nil {
		t.Fatal(err)
	}
	if runCount != 1 {
		t.Fatalf("reconciliation changed historical run count: %d", runCount)
	}
}
