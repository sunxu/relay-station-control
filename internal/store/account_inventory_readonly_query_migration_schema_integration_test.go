package store_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	productstore "github.com/sunxu/relay-station-control/internal/store"
)

type readonlyQueryMigrationTableState struct {
	rows        int64
	fingerprint [sha256.Size]byte
}

type readonlyQueryMigrationState struct {
	tables  []string
	columns []string
	rows    map[string]readonlyQueryMigrationTableState
}

func TestAccountInventoryReadonlyQueryMigrationPreservesExistingLifecycleState(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "down"); err != nil {
		t.Fatal("prepare isolated Migration 8 database")
	}
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "down"); err != nil {
		t.Fatal("prepare isolated Migration 7 database")
	}
	requireAccountInventoryReadonlyQueryMigrationVersion(t, ctx, database, 7)

	repository, err := productstore.NewAccountInventoryRepository(database.runtime)
	if err != nil {
		t.Fatal("create readonly query repository")
	}
	if err := repository.CheckCompatibility(ctx); !errors.Is(err, productstore.ErrAccountInventoryInconsistent) {
		t.Fatal("readonly query compatibility was available before Migration 8")
	}

	fixture := newLifecycleSchemaFixture(t, ctx, database)
	if _, err := database.owner.Exec(ctx, `INSERT INTO driver_capabilities(
		node_type,driver_contract_version,capability
	) VALUES($1,$2,'management_account_inventory_read')`, fixture.nodeType, fixture.contract); err != nil {
		t.Fatal("seed readonly driver capability")
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO node_capabilities(
		instance_id,node_type,driver_contract_version,capability
	) VALUES($1,$2,$3,'management_account_inventory_read')`,
		fixture.instanceID, fixture.nodeType, fixture.contract); err != nil {
		t.Fatal("seed readonly node capability")
	}
	fixture.finalize(t, ctx, database, []lifecycleAccount{
		{email: "migration-preserve-a@example.invalid", successCount: 11},
		{email: "migration-preserve-b@example.invalid", successCount: 12},
	})
	fixture.finalize(t, ctx, database, []lifecycleAccount{
		{email: "migration-preserve-b@example.invalid", successCount: 13},
	})

	before := captureReadonlyQueryMigrationState(t, ctx, database)
	requireReadonlyQueryMigrationSeedShape(t, before)
	requireReadonlyQueryIdentityColumnShape(t, before.columns)

	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "up-by-one"); err != nil {
		t.Fatal("apply Migration 8 to existing lifecycle state")
	}
	requireAccountInventoryReadonlyQueryMigrationVersion(t, ctx, database, 8)
	after := captureReadonlyQueryMigrationState(t, ctx, database)

	t.Run("tables_unchanged", func(t *testing.T) {
		if !reflect.DeepEqual(after.tables, before.tables) {
			t.Fatal("Migration 8 added or removed a product table")
		}
	})
	t.Run("columns_unchanged", func(t *testing.T) {
		if !reflect.DeepEqual(after.columns, before.columns) {
			t.Fatal("Migration 8 added, removed, or changed a product column")
		}
	})
	t.Run("rows_unchanged", func(t *testing.T) {
		if !reflect.DeepEqual(after.rows, before.rows) {
			t.Fatal("Migration 8 changed or copied existing product row state")
		}
	})
	requireReadonlyQueryIdentityColumnShape(t, after.columns)
	if err := repository.CheckCompatibility(ctx); err != nil {
		t.Fatal("readonly query compatibility did not become available after Migration 8")
	}
}

func captureReadonlyQueryMigrationState(
	t *testing.T, ctx context.Context, database *isolatedJobDatabase,
) readonlyQueryMigrationState {
	t.Helper()
	rows, err := database.owner.Query(ctx, `SELECT tablename
		FROM pg_catalog.pg_tables
		WHERE schemaname='public' AND tablename<>'goose_db_version'
		ORDER BY tablename`)
	if err != nil {
		t.Fatal("enumerate Migration product tables")
	}
	defer rows.Close()
	state := readonlyQueryMigrationState{rows: make(map[string]readonlyQueryMigrationTableState)}
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatal("read Migration product table name")
		}
		state.tables = append(state.tables, table)
	}
	if rows.Err() != nil {
		t.Fatal("enumerate Migration product table names")
	}

	for _, table := range state.tables {
		identifier := pgx.Identifier{"public", table}.Sanitize()
		var count int64
		var encoded string
		query := `SELECT count(*), COALESCE(
			jsonb_agg(to_jsonb(source) ORDER BY to_jsonb(source)::text), '[]'::jsonb
		)::text FROM ` + identifier + ` AS source`
		if err := database.owner.QueryRow(ctx, query).Scan(&count, &encoded); err != nil {
			t.Fatal("capture Migration product table state")
		}
		state.rows[table] = readonlyQueryMigrationTableState{
			rows: count, fingerprint: sha256.Sum256([]byte(encoded)),
		}
	}

	columnRows, err := database.owner.Query(ctx, `SELECT
		table_name || ':' || lpad(ordinal_position::text, 4, '0') || ':' ||
		column_name || ':' || udt_name || ':' || is_nullable
		FROM information_schema.columns
		WHERE table_schema='public' AND table_name<>'goose_db_version'
		ORDER BY table_name,ordinal_position`)
	if err != nil {
		t.Fatal("enumerate Migration product columns")
	}
	defer columnRows.Close()
	for columnRows.Next() {
		var column string
		if err := columnRows.Scan(&column); err != nil {
			t.Fatal("read Migration product column")
		}
		state.columns = append(state.columns, column)
	}
	if columnRows.Err() != nil {
		t.Fatal("enumerate Migration product column names")
	}
	return state
}

func requireReadonlyQueryMigrationSeedShape(t *testing.T, state readonlyQueryMigrationState) {
	t.Helper()
	wantRows := map[string]int64{
		"relay_node_assets":                       1,
		"provider_inventory_policy_versions":      1,
		"provider_inventory_policy_bindings":      1,
		"provider_inventory_policy_activations":   1,
		"account_inventory_poll_runs":             2,
		"account_inventory_poll_provider_results": 2,
		"account_inventory_snapshot_items":        3,
		"account_inventory_provider_states":       1,
		"account_inventory":                       2,
		"audit_logs":                              0,
	}
	for table, want := range wantRows {
		got, ok := state.rows[table]
		if !ok || got.rows != want {
			t.Fatal("Migration 7 lifecycle seed shape is incomplete")
		}
	}
}

func requireReadonlyQueryIdentityColumnShape(t *testing.T, columns []string) {
	t.Helper()
	var identityColumns []string
	for _, column := range columns {
		parts := strings.Split(column, ":")
		if len(parts) != 5 {
			t.Fatal("Migration product column descriptor is invalid")
		}
		for _, identity := range []string{"account_key", "normalized_email"} {
			if parts[2] == identity {
				identityColumns = append(identityColumns, column)
			}
		}
	}
	want := []string{
		"account_inventory:0003:account_key:text:NO",
		"account_inventory:0004:normalized_email:text:NO",
		"account_inventory_poll_duplicates:0004:account_key:text:NO",
		"account_inventory_snapshot_items:0004:account_key:text:NO",
		"account_inventory_snapshot_items:0005:normalized_email:text:NO",
	}
	if !reflect.DeepEqual(identityColumns, want) {
		t.Fatal("Migration identity column placement changed")
	}
}
