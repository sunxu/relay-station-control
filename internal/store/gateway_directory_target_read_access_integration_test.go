package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	store "github.com/sunxu/relay-station-control/internal/store"
)

func TestGatewayDirectoryTargetReadAccess(t *testing.T) {
	db := newIsolatedJobDatabase(t)
	ctx := context.Background()
	waitDirectoryRuntimeClaimWindow(t, db)
	gatewayID := uuid.New()
	secretRef := "file://target-read/reader"
	insertGatewayInstance(t, ctx, db.owner, gatewayID, "http://target-read.example", secretRef)
	repo, err := store.NewGatewayDirectoryIngestionRepository(db.runtime)
	if err != nil {
		t.Fatal(err)
	}
	run, created, skipped, err := repo.ScheduleCurrent(ctx, gatewayID)
	if err != nil {
		t.Fatal(err)
	}
	if !created || skipped {
		t.Fatalf("ScheduleCurrent created=%v skipped=%v run=%+v", created, skipped, run)
	}
	claimed, err := repo.ClaimRunnable(ctx, gatewayID, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if claimed == nil {
		t.Fatal("ClaimRunnable returned no running lease")
	}
	var instanceID uuid.UUID
	var endpoint, ref string
	if err := db.runtime.QueryRow(ctx, `SELECT instance_id, management_endpoint, reader_secret_ref
		FROM public.control_query_gateway_directory_target_v1($1,$2,$3)`,
		claimed.IngestionRunID, gatewayID, claimed.LeaseFencingToken).Scan(&instanceID, &endpoint, &ref); err != nil {
		t.Fatal(err)
	}
	if instanceID != gatewayID || endpoint != "http://target-read.example" || ref != secretRef {
		t.Fatalf("target = %s %q %q", instanceID, endpoint, ref)
	}
	var count int
	if err := db.runtime.QueryRow(ctx, `SELECT count(*) FROM public.control_query_gateway_directory_target_v1($1,$2,$3)`, claimed.IngestionRunID, uuid.New(), claimed.LeaseFencingToken).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("wrong gateway count = %d", count)
	}
	if err := db.runtime.QueryRow(ctx, `SELECT count(*) FROM public.control_query_gateway_directory_target_v1($1,$2,$3)`, claimed.IngestionRunID, gatewayID, uuid.New()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("wrong token count = %d", count)
	}
	var directRef string
	err = db.runtime.QueryRow(ctx, `SELECT reader_secret_ref FROM public.gateway_instances WHERE instance_id=$1`, gatewayID).Scan(&directRef)
	requireSQLState(t, err, "42501")
	if directRef != "" {
		t.Fatal("runtime unexpectedly read the secret reference")
	}
	registrarTx, err := db.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer registrarTx.Rollback(ctx)
	if _, err := registrarTx.Exec(ctx, `SET LOCAL ROLE relay_control_asset_registrar`); err != nil {
		t.Fatal(err)
	}
	err = registrarTx.QueryRow(ctx, `SELECT public.control_query_gateway_directory_target_v1($1,$2,$3)`, claimed.IngestionRunID, gatewayID, claimed.LeaseFencingToken).Scan(&instanceID, &endpoint, &ref)
	requireSQLState(t, err, "42501")
	// Preserve the real lease semantics: once the fifteen-second lease expires,
	// the fenced reader returns no row even with the original token.
	time.Sleep(16 * time.Second)
	if err := db.runtime.QueryRow(ctx, `SELECT count(*) FROM public.control_query_gateway_directory_target_v1($1,$2,$3)`, claimed.IngestionRunID, gatewayID, claimed.LeaseFencingToken).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expired lease count = %d", count)
	}
}
