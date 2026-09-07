package store_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestGatewayDirectoryReaderInitialConfiguration(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()
	adminID := uuid.New()
	gatewayID := uuid.New()
	if _, err := database.owner.Exec(ctx, `
		INSERT INTO control_admin_users
			(admin_id, login_name, display_name, role, status, activated_at)
		VALUES ($1, $2, 'Reader Test Admin', 'super_admin', 'enabled', clock_timestamp())`,
		adminID, "reader_"+adminID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `SELECT public.control_register_gateway($1, 'Reader Test Gateway', 'http://reader-test', NULL)`, gatewayID); err != nil {
		t.Fatal(err)
	}

	tx, err := database.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE relay_control_asset_registrar`); err != nil {
		t.Fatal(err)
	}
	var result string
	if err := tx.QueryRow(ctx, `SELECT public.control_set_gateway_directory_reader_initial_v1($1, $2, $3)`, gatewayID, "secret://gateway-reader", adminID).Scan(&result); err != nil {
		t.Fatal(err)
	}
	if result != "configured" {
		t.Fatalf("result = %q, want configured", result)
	}
	if err := tx.QueryRow(ctx, `SELECT public.control_set_gateway_directory_reader_initial_v1($1, $2, $3)`, gatewayID, "secret://gateway-reader", adminID).Scan(&result); err != nil {
		t.Fatal(err)
	}
	if result != "no_op" {
		t.Fatalf("replay result = %q, want no_op", result)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var ref string
	if err := database.owner.QueryRow(ctx, `SELECT reader_secret_ref FROM public.gateway_instances WHERE instance_id = $1`, gatewayID).Scan(&ref); err != nil {
		t.Fatal(err)
	}
	if ref != "secret://gateway-reader" {
		t.Fatalf("reader reference = %q", ref)
	}
	var audits int
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM public.audit_logs WHERE action = 'asset.gateway_directory_reader_configured' AND details->>'gateway_instance_id' = $1`, gatewayID.String()).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 1 {
		t.Fatalf("audit count = %d, want 1", audits)
	}
	var registrarExec, runtimeExec, publicExec bool
	if err := database.owner.QueryRow(ctx, `SELECT
		has_function_privilege('relay_control_asset_registrar', 'public.control_set_gateway_directory_reader_initial_v1(uuid,text,uuid)', 'EXECUTE'),
		has_function_privilege('relay_control_runtime', 'public.control_set_gateway_directory_reader_initial_v1(uuid,text,uuid)', 'EXECUTE'),
		has_function_privilege('public', 'public.control_set_gateway_directory_reader_initial_v1(uuid,text,uuid)', 'EXECUTE')`).Scan(&registrarExec, &runtimeExec, &publicExec); err != nil {
		t.Fatal(err)
	}
	if !registrarExec || runtimeExec || publicExec {
		t.Fatalf("function ACL registrar=%v runtime=%v public=%v", registrarExec, runtimeExec, publicExec)
	}
	if _, err := database.runtime.Exec(ctx, `INSERT INTO audit_logs(category, action, result, actor_admin_id, request_id, details)
		VALUES ('asset', 'asset.gateway_directory_reader_configured', 'success', $1, 'forged', $2::jsonb)`, adminID,
		`{"gateway_instance_id":"`+gatewayID.String()+`","reader_configured":true}`); err == nil {
		t.Fatal("runtime forged asset audit unexpectedly succeeded")
	}
}
