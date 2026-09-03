package store_test

import (
	"context"
	"encoding/json"
	"testing"
)

func TestGatewayDirectoryAndRelayBindingMigrationsUpDownUp(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()

	var version int32
	if err := database.owner.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 11 {
		t.Fatalf("migration version = %d, want 11", version)
	}

	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "down"); err != nil {
		t.Fatal(err)
	}
	if err := database.owner.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 10 {
		t.Fatalf("after down migration version = %d, want 10", version)
	}

	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "up-by-one"); err != nil {
		t.Fatal(err)
	}
	if err := database.owner.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 11 {
		t.Fatalf("after up migration version = %d, want 11", version)
	}
}

func TestRelayBindingMigrationRollbackFailClosedWithHistory(t *testing.T) {
	ctx := context.Background()

	t.Run("fails when binding records exist", func(t *testing.T) {
		database := newIsolatedJobDatabase(t)
		fixture := newRelayBindingSchemaFixture(t, ctx, database)
		nodeID := fixture.insertNode(t, ctx, database)
		fixture.insertBinding(t, ctx, database, nodeID, fixture.accountIDs[0], "administrator_bind")

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
