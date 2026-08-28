package store_test

import (
	"context"
	"testing"
)

const accountInventoryReadonlyQueryFunction = "public.control_query_current_account_inventory_v1(uuid,text,text,text,text,text,integer)"

func TestAccountInventoryReadonlyQueryRuntimeAndUnauthorizedPermissionMatrix(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleSchemaFixture(t, ctx, database)
	if _, err := database.owner.Exec(ctx, `INSERT INTO driver_capabilities(
		node_type,driver_contract_version,capability
	) VALUES ($1,$2,'management_account_inventory_read')`, fixture.nodeType, fixture.contract); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO node_capabilities(
		instance_id,node_type,driver_contract_version,capability
	) VALUES ($1,$2,$3,'management_account_inventory_read')`,
		fixture.instanceID, fixture.nodeType, fixture.contract); err != nil {
		t.Fatal(err)
	}
	fixture.finalize(t, ctx, database, []lifecycleAccount{{
		email: "permission-matrix@example.invalid", successCount: 1,
	}})

	var runtimeCanExecute, unauthorizedCanExecute bool
	if err := database.owner.QueryRow(ctx, `SELECT
		has_function_privilege('relay_control_runtime',$1,'EXECUTE'),
		has_function_privilege('relay_control_asset_registrar',$1,'EXECUTE')`,
		accountInventoryReadonlyQueryFunction).Scan(
		&runtimeCanExecute, &unauthorizedCanExecute,
	); err != nil {
		t.Fatal(err)
	}
	if !runtimeCanExecute || unauthorizedCanExecute {
		t.Fatalf("readonly query function privilege mismatch: runtime=%v unauthorized=%v",
			runtimeCanExecute, unauthorizedCanExecute)
	}

	var rows int
	if err := database.runtime.QueryRow(ctx, `SELECT count(*)
		FROM public.control_query_current_account_inventory_v1($1,'','','','','',2)`,
		fixture.instanceID).Scan(&rows); err != nil {
		t.Fatalf("runtime controlled query: %v", err)
	}
	if rows != 1 {
		t.Fatalf("runtime controlled query rows = %d, want 1", rows)
	}

	tables := []struct {
		name         string
		updateColumn string
	}{
		{name: "account_inventory", updateColumn: "lifecycle"},
		{name: "account_inventory_provider_states", updateColumn: "state"},
		{name: "account_inventory_snapshot_items", updateColumn: "provider"},
		{name: "account_inventory_poll_duplicates", updateColumn: "provider"},
		{name: "relay_node_assets", updateColumn: "display_name"},
	}
	privileges := []string{
		"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER",
	}
	for _, table := range tables {
		for _, privilege := range privileges {
			var allowed bool
			if err := database.owner.QueryRow(ctx,
				`SELECT has_table_privilege('relay_control_runtime',$1,$2)`,
				"public."+table.name, privilege).Scan(&allowed); err != nil {
				t.Fatalf("inspect runtime %s on %s: %v", privilege, table.name, err)
			}
			if allowed {
				t.Fatalf("runtime unexpectedly has table-level %s on %s", privilege, table.name)
			}
		}

		statements := []string{
			"SELECT * FROM public." + table.name + " LIMIT 0",
			"INSERT INTO public." + table.name + " DEFAULT VALUES",
			"UPDATE public." + table.name + " SET " + table.updateColumn + "=" + table.updateColumn + " WHERE false",
			"DELETE FROM public." + table.name + " WHERE false",
			"TRUNCATE public." + table.name,
		}
		for _, statement := range statements {
			_, err := database.runtime.Exec(ctx, statement)
			requirePostgresCode(t, err, "42501")
		}
	}

	var runtimeCanReadAssetSecret bool
	if err := database.owner.QueryRow(ctx, `SELECT has_column_privilege(
		'relay_control_runtime','public.relay_node_assets','reader_secret_ref','SELECT'
	)`).Scan(&runtimeCanReadAssetSecret); err != nil {
		t.Fatal(err)
	}
	if runtimeCanReadAssetSecret {
		t.Fatal("runtime unexpectedly has access to the asset reader Secret reference")
	}
	if _, err := database.runtime.Exec(ctx,
		`SELECT reader_secret_ref FROM public.relay_node_assets LIMIT 1`); err == nil {
		t.Fatal("runtime enumerated the asset reader Secret reference")
	} else {
		requirePostgresCode(t, err, "42501")
	}

	unauthorized, err := database.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unauthorized.Rollback(context.Background()) }()
	if _, err := unauthorized.Exec(ctx, `SET LOCAL ROLE relay_control_asset_registrar`); err != nil {
		t.Fatalf("assume unauthorized role: %v", err)
	}
	if _, err := unauthorized.Exec(ctx, `SELECT count(*)
		FROM public.control_query_current_account_inventory_v1($1,'','','','','',2)`,
		fixture.instanceID); err == nil {
		t.Fatal("unauthorized role executed the readonly query function")
	} else {
		requirePostgresCode(t, err, "42501")
	}
}
