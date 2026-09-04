package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestGatewayDirectoryAndRelayBindingMigrationsUpDownUp(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()

	var version int32
	if err := database.owner.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 12 {
		t.Fatalf("migration version = %d, want 12", version)
	}

	assertVersion := func(t *testing.T, want int32) {
		t.Helper()
		var got int32
		if err := database.owner.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("migration version = %d, want %d", got, want)
		}
	}

	// Clean environment: 12 -> 11 -> 10 -> 11 -> 12, asserting the exact
	// version at every step. This proves migration 12's down/up is
	// symmetric, and that migration 11's own down/up behavior (already
	// covered pre-migration-12) has not been altered by the additive
	// migration 12 privilege patch.
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "down"); err != nil {
		t.Fatal(err)
	}
	assertVersion(t, 11)

	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "down"); err != nil {
		t.Fatal(err)
	}
	assertVersion(t, 10)

	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "up-by-one"); err != nil {
		t.Fatal(err)
	}
	assertVersion(t, 11)

	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "up-by-one"); err != nil {
		t.Fatal(err)
	}
	assertVersion(t, 12)
}

// TestRelayBindingRuntimeLockPrivilegeMigrationUpDownUp proves migration 12
// grants exactly the row-lock privilege needed by the binding mutation
// transactions, that it is symmetrically revoked on down, and that the
// pre-existing migration 11 fail-closed rollback guard (SQLSTATE 55000 when
// binding/audit history exists) is unaffected by this additive privilege
// migration.
func TestRelayBindingRuntimeLockPrivilegeMigrationUpDownUp(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()

	// Real Gateway and Relay Node rows must exist so the lock queries
	// exercise an actual row, not just an empty-result no-op scan.
	fixture := newRelayBindingSchemaFixture(t, ctx, database)
	fixture.insertNode(t, ctx, database)

	assertLockable := func(t *testing.T) {
		t.Helper()
		tx, err := database.runtime.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		rows, err := tx.Query(ctx, `SELECT instance_id FROM relay_node_assets FOR UPDATE`)
		if err != nil {
			t.Fatalf("expected relay_node_assets row lock to succeed after migration 12 up, got: %v", err)
		}
		nodeRows := 0
		for rows.Next() {
			nodeRows++
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if nodeRows == 0 {
			t.Fatal("expected at least one relay_node_assets row to be locked")
		}
		rows, err = tx.Query(ctx, `SELECT instance_id FROM gateway_instances FOR UPDATE`)
		if err != nil {
			t.Fatalf("expected gateway_instances row lock to succeed after migration 12 up, got: %v", err)
		}
		gatewayRows := 0
		for rows.Next() {
			gatewayRows++
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if gatewayRows == 0 {
			t.Fatal("expected at least one gateway_instances row to be locked")
		}
	}
	assertNotLockable := func(t *testing.T) {
		t.Helper()
		tx, err := database.runtime.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		_, nodeErr := tx.Exec(ctx, `SELECT instance_id FROM relay_node_assets FOR UPDATE`)
		if nodeErr == nil {
			t.Fatal("expected relay_node_assets row lock to fail without migration 12 privileges")
		}
		var pgErr *pgconn.PgError
		if !errors.As(nodeErr, &pgErr) || pgErr.Code != "42501" {
			t.Fatalf("expected SQLSTATE 42501 permission denied for relay_node_assets, got: %v", nodeErr)
		}
		_ = tx.Rollback(ctx)

		tx2, err := database.runtime.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx2.Rollback(ctx) }()
		_, gatewayErr := tx2.Exec(ctx, `SELECT instance_id FROM gateway_instances FOR UPDATE`)
		if gatewayErr == nil {
			t.Fatal("expected gateway_instances row lock to fail without migration 12 privileges")
		}
		if !errors.As(gatewayErr, &pgErr) || pgErr.Code != "42501" {
			t.Fatalf("expected SQLSTATE 42501 permission denied for gateway_instances, got: %v", gatewayErr)
		}
	}
	assertIdentityAndSecretsRemainProtected := func(t *testing.T) {
		t.Helper()
		// created_at is the column migration 12 grants UPDATE on to
		// satisfy the FOR UPDATE ACL check; it must still be rejected for
		// any real UPDATE because the endpoint CHECK constraints invoke
		// control_normalize_asset_endpoint(), whose EXECUTE privilege is
		// not granted to relay_control_runtime.
		cases := []string{
			`UPDATE relay_node_assets SET instance_id = instance_id`,
			`UPDATE relay_node_assets SET management_endpoint = management_endpoint`,
			`UPDATE relay_node_assets SET reader_secret_ref = reader_secret_ref`,
			`UPDATE relay_node_assets SET created_at = created_at`,
			`UPDATE gateway_instances SET instance_id = instance_id`,
			`UPDATE gateway_instances SET management_endpoint = management_endpoint`,
			`UPDATE gateway_instances SET reader_secret_ref = reader_secret_ref`,
			`UPDATE gateway_instances SET created_at = created_at`,
		}
		for _, statement := range cases {
			_, err := database.runtime.Exec(ctx, statement)
			if err == nil {
				t.Fatalf("expected %q to be denied for relay_control_runtime", statement)
			}
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
				t.Fatalf("expected SQLSTATE 42501 permission denied for %q, got: %v", statement, err)
			}
		}

		var canExecuteNormalize bool
		if err := database.owner.QueryRow(ctx, `SELECT has_function_privilege(
			'relay_control_runtime', 'public.control_normalize_asset_endpoint(text)', 'EXECUTE'
		)`).Scan(&canExecuteNormalize); err != nil {
			t.Fatal(err)
		}
		if canExecuteNormalize {
			t.Fatal("expected relay_control_runtime to have no EXECUTE privilege on control_normalize_asset_endpoint")
		}
	}

	// Migration 12 Up (already applied by newIsolatedJobDatabase's initial
	// "up"): binding row locks are usable, but identity/endpoint/secret
	// columns remain fully protected against real UPDATE.
	assertLockable(t)
	assertIdentityAndSecretsRemainProtected(t)

	// Migration 12 Down: the added lock privilege is revoked.
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "down"); err != nil {
		t.Fatal(err)
	}
	var version int32
	if err := database.owner.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 11 {
		t.Fatalf("after migration 12 down, version = %d, want 11", version)
	}
	assertNotLockable(t)

	// Migration 12 Up again: privilege is restored.
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "up-by-one"); err != nil {
		t.Fatal(err)
	}
	if err := database.owner.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 12 {
		t.Fatalf("after migration 12 up-by-one, version = %d, want 12", version)
	}
	assertLockable(t)
	assertIdentityAndSecretsRemainProtected(t)

	// The pre-existing migration 11 fail-closed rollback guard (binding
	// history blocks 11 -> 10) must be unaffected by migration 12.
	nodeID := fixture.insertNode(t, ctx, database)
	fixture.insertBinding(t, ctx, database, nodeID, fixture.accountIDs[0], "administrator_bind")

	// Step down from 12 -> 11: migration 12 only revokes a privilege grant
	// and carries no history guard, so this must succeed.
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "down"); err != nil {
		t.Fatal(err)
	}
	if err := database.owner.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 11 {
		t.Fatalf("after migration 12 down, version = %d, want 11", version)
	}

	// Step down from 11 -> 10: migration 11's fail-closed guard must block
	// this because binding history exists.
	err := runAssetGoose(t, ctx, "../..", database.ownerURL, "down")
	if err == nil {
		t.Fatal("expected goose down (11 -> 10) to fail closed with binding history present")
	}
	if !strings.Contains(err.Error(), "55000") {
		t.Fatalf("expected SQLSTATE 55000 from migration 11 fail-closed guard, got: %v", err)
	}
	if err := database.owner.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 11 {
		t.Fatalf("after failed 11 -> 10 rollback, version = %d, want 11", version)
	}
}

func TestRelayBindingMigrationRollbackFailClosedWithHistory(t *testing.T) {
	ctx := context.Background()

	t.Run("fails when binding records exist", func(t *testing.T) {
		database := newIsolatedJobDatabase(t)
		fixture := newRelayBindingSchemaFixture(t, ctx, database)
		nodeID := fixture.insertNode(t, ctx, database)
		fixture.insertBinding(t, ctx, database, nodeID, fixture.accountIDs[0], "administrator_bind")

		// Step down 12 -> 11 first: migration 12 only revokes a lock
		// privilege grant and carries no history guard, so this succeeds.
		if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "down"); err != nil {
			t.Fatal(err)
		}

		err := runAssetGoose(t, ctx, "../..", database.ownerURL, "down")
		if err == nil {
			t.Fatal("expected goose down to fail when binding records exist, got nil")
		}

		var version int32
		if err := database.owner.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil {
			t.Fatal(err)
		}
		if version != 11 {
			t.Fatalf("migration version = %d, want 11", version)
		}

		var count int
		if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM relay_node_gateway_account_bindings`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("binding count = %d, want 1", count)
		}
	})

	t.Run("fails when only relay_binding audit records exist", func(t *testing.T) {
		database := newIsolatedJobDatabase(t)
		fixture := newRelayBindingSchemaFixture(t, ctx, database)
		nodeID := fixture.insertNode(t, ctx, database)

		encoded, err := json.Marshal(map[string]any{
			"relay_node_id":          nodeID,
			"gateway_instance_id":    fixture.gatewayID,
			"old_gateway_account_id": nil,
			"new_gateway_account_id": fixture.accountIDs[0],
			"evidence_snapshot_id":   fixture.snapshotID,
			"reason_code":            "administrator_bind",
		})
		if err != nil {
			t.Fatal(err)
		}

		if _, err := database.owner.Exec(ctx, `INSERT INTO audit_logs(
			category,action,result,actor_admin_id,request_id,details
		) VALUES ('relay_binding','relay_binding.bind','success',$1,'req-audit-rollback',$2::jsonb)`,
			fixture.adminID, encoded); err != nil {
			t.Fatal(err)
		}

		// Step down 12 -> 11 first: migration 12 only revokes a lock
		// privilege grant and carries no history guard, so this succeeds.
		if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "down"); err != nil {
			t.Fatal(err)
		}

		err = runAssetGoose(t, ctx, "../..", database.ownerURL, "down")
		if err == nil {
			t.Fatal("expected goose down to fail when relay_binding audit records exist, got nil")
		}

		var version int32
		if err := database.owner.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil {
			t.Fatal(err)
		}
		if version != 11 {
			t.Fatalf("migration version = %d, want 11", version)
		}

		var count int
		if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE category = 'relay_binding'`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("audit count = %d, want 1", count)
		}
	})
}
