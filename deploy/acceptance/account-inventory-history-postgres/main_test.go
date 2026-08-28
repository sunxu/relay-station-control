package historypostgresacceptance

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var historyTables = []string{
	"account_inventory_compaction_runs",
	"account_inventory_daily_rollup_runs",
	"account_inventory_history_retired_days",
	"account_inventory_daily_summaries",
	"account_inventory_daily_provider_summaries",
	"account_inventory_daily_account_rollups",
	"account_inventory_daily_provider_rollups",
}

func openHistoryPool(t *testing.T, environmentName string) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv(environmentName)
	if databaseURL == "" {
		t.Skip("PostgreSQL history acceptance is not enabled")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatal("history database configuration rejected")
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatal("history database unavailable")
	}
	return pool
}

func TestAccountInventoryHistoryPostgresSchemaSmoke(t *testing.T) {
	ctx := context.Background()
	migrator := openHistoryPool(t, "CONTROL_HISTORY_MIGRATOR_TEST_URL")
	runtimePool := openHistoryPool(t, "CONTROL_HISTORY_RUNTIME_TEST_URL")
	registrarPool := openHistoryPool(t, "CONTROL_HISTORY_REGISTRAR_TEST_URL")

	var version int
	if err := migrator.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil || version != 9 {
		t.Fatal("Migration 9 is not the current schema")
	}

	var compatibilityBytes []byte
	if err := runtimePool.QueryRow(ctx, `SELECT public.control_history_schema_compatibility_v1()`).Scan(&compatibilityBytes); err != nil {
		t.Fatal("runtime compatibility probe is unavailable")
	}
	var compatibility struct {
		SchemaVersion                int  `json:"schema_version"`
		HistoryTableCount            int  `json:"history_table_count"`
		CoreSHA256                   bool `json:"core_sha256"`
		CoverageThresholdBasisPoints int  `json:"coverage_threshold_basis_points"`
		SnapshotMinimumAgeHours      int  `json:"snapshot_minimum_age_hours"`
		HistoryRetentionDays         int  `json:"history_retention_days"`
	}
	if err := json.Unmarshal(compatibilityBytes, &compatibility); err != nil ||
		compatibility.SchemaVersion != 1 || compatibility.HistoryTableCount != len(historyTables) ||
		!compatibility.CoreSHA256 || compatibility.CoverageThresholdBasisPoints != 9_500 ||
		compatibility.SnapshotMinimumAgeHours != 72 || compatibility.HistoryRetentionDays != 30 {
		t.Fatal("history compatibility projection is invalid")
	}

	var coreSHA, singleGolden, multipleGolden string
	if err := migrator.QueryRow(ctx, `SELECT encode(sha256(''::bytea), 'hex')`).Scan(&coreSHA); err != nil ||
		coreSHA != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatal("PostgreSQL core SHA-256 is incompatible")
	}
	if err := migrator.QueryRow(ctx, `
		SELECT encode(public.control_history_checksum_chain_v1(ARRAY[
			public.control_history_canonical_row_v1(VARIADIC ARRAY[
				convert_to('instance-a','UTF8'), convert_to('7','UTF8'), convert_to('t','UTF8')
			])
		]), 'hex')`).Scan(&singleGolden); err != nil ||
		singleGolden != "136d4d2c579f30c7ac22bcab7b50a6dbe8c0100f551d39f219b8ad2cb3f9e967" {
		t.Fatal("single-row canonical checksum golden mismatch")
	}
	if err := migrator.QueryRow(ctx, `
		SELECT encode(public.control_history_checksum_chain_v1(ARRAY[
			public.control_history_canonical_row_v1(VARIADIC ARRAY[
				convert_to('instance-a','UTF8'), convert_to('7','UTF8'), convert_to('t','UTF8')
			]),
			public.control_history_canonical_row_v1(VARIADIC ARRAY[
				convert_to('instance-b','UTF8'), NULL::bytea, convert_to('f','UTF8')
			])
		]), 'hex')`).Scan(&multipleGolden); err != nil ||
		multipleGolden != "1b049d7b8bdcfce3c849b9bbcd34a4c77503e326ec4c1aa61215e5f0d77e86aa" {
		t.Fatal("multiple-row canonical checksum golden mismatch")
	}

	for _, table := range historyTables {
		var count int
		if err := migrator.QueryRow(ctx, `SELECT count(*) FROM public.`+pgx.Identifier{table}.Sanitize()).Scan(&count); err != nil || count != 0 {
			t.Fatal("history table is not empty after migration")
		}
		assertRuntimeTableDenied(t, ctx, runtimePool, table)
	}

	rows, err := runtimePool.Query(ctx, `SELECT * FROM public.control_list_account_inventory_history_metrics_v1()`)
	if err != nil {
		t.Fatal("runtime history metrics function is unavailable")
	}
	if rows.Next() {
		rows.Close()
		t.Fatal("empty history published a metric row")
	}
	if rows.Err() != nil {
		rows.Close()
		t.Fatal("runtime history metrics read failed")
	}
	rows.Close()
	var metricsSnapshot []byte
	if err := runtimePool.QueryRow(ctx,
		`SELECT public.control_account_inventory_history_metrics_snapshot_v1()`).Scan(&metricsSnapshot); err != nil {
		t.Fatal("runtime aggregate history metrics snapshot is unavailable")
	}
	var aggregate struct {
		CompactionRuns map[string]int64 `json:"compaction_runs"`
		RollupRuns     map[string]int64 `json:"rollup_runs"`
		Oldest         float64          `json:"oldest_eligible_unfinished_seconds"`
		Failures       map[string]int64 `json:"failures"`
		Backlog        int64            `json:"delete_backlog_rows"`
	}
	if err := json.Unmarshal(metricsSnapshot, &aggregate); err != nil ||
		len(aggregate.CompactionRuns) != 5 || len(aggregate.RollupRuns) != 3 ||
		len(aggregate.Failures) != 3 || aggregate.Oldest != 0 || aggregate.Backlog != 0 {
		t.Fatal("empty aggregate history metrics snapshot is invalid")
	}

	var planBytes []byte
	if err := runtimePool.QueryRow(ctx, `SELECT public.control_plan_account_inventory_history_v1(1)`).Scan(&planBytes); err != nil {
		t.Fatal("runtime history planner function is unavailable")
	}
	var claimed int
	if err := runtimePool.QueryRow(ctx, `
		SELECT count(*) FROM public.control_claim_account_inventory_compaction_v1(
			'00000000-0000-0000-0000-000000000001'::uuid, 5
		)`).Scan(&claimed); err != nil || claimed != 0 {
		t.Fatal("runtime history claim function is invalid on an empty database")
	}

	allowedFunctions := []string{
		"public.control_plan_account_inventory_history_v1(integer)",
		"public.control_claim_account_inventory_compaction_v1(uuid,integer)",
		"public.control_renew_account_inventory_compaction_v1(uuid,uuid,integer)",
		"public.control_reconcile_account_inventory_compactions_v1(integer)",
		"public.control_summarize_account_inventory_compaction_v1(uuid,uuid)",
		"public.control_delete_account_inventory_snapshot_batch_v1(uuid,uuid,integer)",
		"public.control_complete_account_inventory_compaction_v1(uuid,uuid,bytea)",
		"public.control_fail_account_inventory_compaction_v1(uuid,uuid,text)",
		"public.control_list_account_inventory_history_metrics_v1()",
		"public.control_account_inventory_history_metrics_snapshot_v1()",
		"public.control_history_schema_compatibility_v1()",
	}
	deniedFunctions := []string{
		"public.control_history_canonical_row_v1(bytea[])",
		"public.control_history_checksum_chain_v1(bytea[])",
		"public.control_refresh_account_inventory_provider_health_v1()",
	}
	for _, signature := range allowedFunctions {
		assertFunctionPrivilege(t, ctx, migrator, "relay_control_app_dev", signature, true)
		assertFunctionPrivilege(t, ctx, migrator, "relay_control_asset_registrar_dev", signature, false)
	}
	for _, signature := range deniedFunctions {
		assertFunctionPrivilege(t, ctx, migrator, "relay_control_app_dev", signature, false)
		assertFunctionPrivilege(t, ctx, migrator, "relay_control_asset_registrar_dev", signature, false)
	}

	var roleRestricted bool
	if err := migrator.QueryRow(ctx, `
		SELECT NOT rolsuper AND NOT rolcreatedb AND NOT rolcreaterole
		       AND NOT rolreplication AND NOT rolbypassrls
		FROM pg_roles WHERE rolname = 'relay_control_app_dev'`).Scan(&roleRestricted); err != nil || !roleRestricted {
		t.Fatal("runtime login role is not restricted")
	}

	var ignored []byte
	err = runtimePool.QueryRow(ctx, `SELECT public.control_history_canonical_row_v1(VARIADIC ARRAY['x'::bytea])`).Scan(&ignored)
	if !isInsufficientPrivilege(err) {
		t.Fatal("runtime unexpectedly executed internal canonical encoder")
	}
	err = registrarPool.QueryRow(ctx, `SELECT public.control_history_schema_compatibility_v1()`).Scan(&ignored)
	if !isInsufficientPrivilege(err) {
		t.Fatal("asset registrar unexpectedly executed a history function")
	}
}

func assertFunctionPrivilege(t *testing.T, ctx context.Context, pool *pgxpool.Pool, role, signature string, want bool) {
	t.Helper()
	var got bool
	if err := pool.QueryRow(ctx, `SELECT has_function_privilege($1, $2, 'EXECUTE')`, role, signature).Scan(&got); err != nil || got != want {
		t.Fatal("history function privilege allowlist mismatch")
	}
}

func assertRuntimeTableDenied(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table string) {
	t.Helper()
	var count int
	err := pool.QueryRow(ctx, `SELECT count(*) FROM public.`+pgx.Identifier{table}.Sanitize()).Scan(&count)
	if !isInsufficientPrivilege(err) {
		t.Fatal("runtime unexpectedly read a protected history table")
	}
	_, err = pool.Exec(ctx, `TRUNCATE TABLE public.`+pgx.Identifier{table}.Sanitize())
	if !isInsufficientPrivilege(err) {
		t.Fatal("runtime unexpectedly mutated a protected history table")
	}
}

func isInsufficientPrivilege(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "42501"
}
