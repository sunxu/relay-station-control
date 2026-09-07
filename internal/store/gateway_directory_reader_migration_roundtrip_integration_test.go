package store_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestGatewayDirectoryReaderMigrationRoundTrip(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()
	adminID := uuid.New()
	gatewayID := uuid.New()
	if _, err := database.owner.Exec(ctx, `
		INSERT INTO control_admin_users
			(admin_id, login_name, display_name, role, status, activated_at)
		VALUES ($1, $2, 'Round Trip Admin', 'super_admin', 'enabled', clock_timestamp())`,
		adminID, "roundtrip_"+adminID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `SELECT public.control_register_gateway($1, 'Round Trip Gateway', 'http://round-trip', NULL)`, gatewayID); err != nil {
		t.Fatal(err)
	}
	configure := func() string {
		t.Helper()
		tx, err := database.owner.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, `SET LOCAL ROLE relay_control_asset_registrar`); err != nil {
			t.Fatal(err)
		}
		var result string
		if err := tx.QueryRow(ctx, `SELECT public.control_set_gateway_directory_reader_initial_v1($1,$2,$3)`, gatewayID, "file://round-trip/reader", adminID).Scan(&result); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		return result
	}
	if got := configure(); got != "configured" {
		t.Fatalf("initial result = %q, want configured", got)
	}
	var before int
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action='asset.gateway_directory_reader_configured'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if before != 1 {
		t.Fatalf("audit count before down = %d, want 1", before)
	}
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "down"); err != nil {
		t.Fatal(err)
	}
	var functionCount, auditCount, shapeCount int
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM pg_proc WHERE proname='control_set_gateway_directory_reader_initial_v1'`).Scan(&functionCount); err != nil {
		t.Fatal(err)
	}
	if functionCount != 0 {
		t.Fatalf("function count after down = %d, want 0", functionCount)
	}
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action='asset.gateway_directory_reader_configured'`).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("audit count after down = %d, want 1", auditCount)
	}
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM pg_constraint WHERE conname='audit_logs_asset_gateway_reader_shape'`).Scan(&shapeCount); err != nil {
		t.Fatal(err)
	}
	if shapeCount != 1 {
		t.Fatalf("audit shape count after down = %d, want 1", shapeCount)
	}
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "up-by-one"); err != nil {
		t.Fatal(err)
	}
	if got := configure(); got != "no_op" {
		t.Fatalf("replay result after up = %q, want no_op", got)
	}
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action='asset.gateway_directory_reader_configured'`).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("audit count after up replay = %d, want 1", auditCount)
	}
}
