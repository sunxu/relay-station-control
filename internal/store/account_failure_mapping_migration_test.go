package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestAccountFailureHTTPMappingMigration49(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	checkMapping := func(t *testing.T, owner *pgx.Conn) {
		t.Helper()
		var got, ambiguous, exists, filename int
		if err := owner.QueryRow(ctx, `SELECT public.control_account_failure_http_status_v1('account_target_not_found'), public.control_account_failure_http_status_v1('account_target_ambiguous'), public.control_account_failure_http_status_v1('account_target_exists'), public.control_account_failure_http_status_v1('account_filename_conflict')`).Scan(&got, &ambiguous, &exists, &filename); err != nil {
			t.Fatal(err)
		}
		if got != 404 || ambiguous != 409 || exists != 409 || filename != 409 {
			t.Fatalf("mapping=%d/%d/%d/%d", got, ambiguous, exists, filename)
		}
	}

	t.Run("fresh_0_to_49", func(t *testing.T) {
		databaseURL, owner, cleanup := newGatewayLifecycleMigrationDatabase(t, ctx)
		defer cleanup()
		if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "49"); err != nil {
			t.Fatal(err)
		}
		checkMapping(t, owner)
	})

	t.Run("previous_latest_48_to_49", func(t *testing.T) {
		databaseURL, owner, cleanup := newGatewayLifecycleMigrationDatabase(t, ctx)
		defer cleanup()
		if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "48"); err != nil {
			t.Fatal(err)
		}
		if _, err := owner.Exec(ctx, `INSERT INTO audit_logs(category, action, result, request_id, details) VALUES ('account_admin', 'account.operation_failed', 'failure', 'migration49-preserve', '{"error_code":"account_target_not_found"}')`); err != nil {
			t.Fatal(err)
		}
		owner.Close(ctx)
		if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "49"); err != nil {
			t.Fatal(err)
		}
		check, err := pgx.Connect(ctx, databaseURL)
		if err != nil {
			t.Fatal(err)
		}
		defer check.Close(ctx)
		checkMapping(t, check)
		var auditCount int
		if err := check.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE request_id='migration49-preserve'`).Scan(&auditCount); err != nil {
			t.Fatal(err)
		}
		if auditCount != 1 {
			t.Fatalf("audit rows=%d, want 1", auditCount)
		}
	})
}
