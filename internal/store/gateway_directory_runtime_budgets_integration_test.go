package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	store "github.com/sunxu/relay-station-control/internal/store"
)

func TestGatewayDirectoryRuntimeFrozenBudgets(t *testing.T) {
	db := newIsolatedJobDatabase(t)
	ctx := context.Background()
	fixture := newRelayBindingSchemaFixture(t, ctx, db)
	waitDirectoryRuntimeClaimWindow(t, db)
	repo, err := store.NewGatewayDirectoryIngestionRepository(db.runtime)
	if err != nil {
		t.Fatal(err)
	}
	first, created, skipped, err := repo.ScheduleCurrent(ctx, fixture.gatewayID)
	if err != nil || !created || skipped {
		t.Fatalf("first schedule: created=%t skipped=%t err=%v", created, skipped, err)
	}
	if first.ScheduledAt.Time.Unix()%180 != 0 {
		t.Fatal("scheduled time is not a 180-second slot")
	}
	_, created, skipped, err = repo.ScheduleCurrent(ctx, fixture.gatewayID)
	// The existing active-run contract returns skipped/no row, not the old row.
	if err != nil || created || !skipped {
		t.Fatal("active run did not suppress duplicate scheduling")
	}
	var exactRuns, allRuns int
	if err := db.owner.QueryRow(ctx, `SELECT count(*) FILTER (WHERE ingestion_run_id=$2),count(*) FROM gateway_directory_ingestion_runs WHERE gateway_instance_id=$1`, fixture.gatewayID, first.IngestionRunID).Scan(&exactRuns, &allRuns); err != nil {
		t.Fatal(err)
	}
	if exactRuns != 1 || allRuns != 1 {
		t.Fatal("same slot did not retain exactly the original run")
	}
	claimed, err := repo.ClaimRunnable(ctx, fixture.gatewayID, uuid.New())
	if err != nil || claimed == nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed.LeaseExpiresAt.Time.Sub(claimed.LastStartedAt.Time) != 15*time.Second {
		t.Fatal("lease budget changed")
	}
	for _, sample := range []struct {
		age  int
		want string
	}{{539, "fresh"}, {541, "stale"}} {
		var observed time.Time
		if err := db.owner.QueryRow(ctx, `SELECT clock_timestamp()-$1*interval '1 second'`, sample.age).Scan(&observed); err != nil {
			t.Fatal(err)
		}
		insertGatewayDirectoryCurrentState(t, ctx, db, fixture.gatewayID, fixture.snapshotID, observed)
		metrics, err := repo.GatewayDirectoryMetricsSnapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if metrics.FreshnessCounts[sample.want] != 1 || metrics.FreshnessCounts["fresh"]+metrics.FreshnessCounts["stale"] != 1 || metrics.FreshnessCounts["unknown"] != 0 {
			t.Fatalf("age=%d freshness=%v", sample.age, metrics.FreshnessCounts)
		}
	}
}
