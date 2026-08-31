package historypostgresacceptance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

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

type historyMatrixDatabase struct {
	owner   *pgxpool.Pool
	runtime *pgxpool.Pool
}

func newHistoryMatrixDatabase(t *testing.T) *historyMatrixDatabase {
	t.Helper()
	if os.Getenv("CONTROL_HISTORY_MIGRATOR_TEST_URL") == "" ||
		os.Getenv("CONTROL_HISTORY_RUNTIME_TEST_URL") == "" {
		t.Skip("PostgreSQL history acceptance is not enabled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ownerConfig, err := pgx.ParseConfig(os.Getenv("CONTROL_HISTORY_MIGRATOR_TEST_URL"))
	if err != nil {
		t.Fatal("history matrix owner configuration rejected")
	}
	maintenanceConfig := ownerConfig.Copy()
	maintenanceConfig.Database = "postgres"
	maintenance, err := pgx.ConnectConfig(ctx, maintenanceConfig)
	if err != nil {
		t.Fatal("history matrix maintenance database unavailable")
	}
	databaseName := fmt.Sprintf("history_matrix_%d_%d", os.Getpid(), time.Now().UnixNano())
	if _, err := maintenance.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{databaseName}.Sanitize()+
		` WITH TEMPLATE `+pgx.Identifier{ownerConfig.Database}.Sanitize()); err != nil {
		maintenance.Close(ctx)
		t.Fatal("history matrix database clone failed")
	}
	ownerPoolConfig, err := pgxpool.ParseConfig(os.Getenv("CONTROL_HISTORY_MIGRATOR_TEST_URL"))
	if err != nil {
		maintenance.Close(ctx)
		t.Fatal("history matrix owner pool configuration rejected")
	}
	ownerPoolConfig.ConnConfig.Database = databaseName
	runtimePoolConfig, err := pgxpool.ParseConfig(os.Getenv("CONTROL_HISTORY_RUNTIME_TEST_URL"))
	if err != nil {
		maintenance.Close(ctx)
		t.Fatal("history matrix runtime pool configuration rejected")
	}
	runtimePoolConfig.ConnConfig.Database = databaseName
	database := &historyMatrixDatabase{}
	database.owner, err = pgxpool.NewWithConfig(ctx, ownerPoolConfig)
	if err == nil {
		database.runtime, err = pgxpool.NewWithConfig(ctx, runtimePoolConfig)
	}
	if err != nil {
		if database.owner != nil {
			database.owner.Close()
		}
		_, _ = maintenance.Exec(ctx, `DROP DATABASE `+pgx.Identifier{databaseName}.Sanitize()+` WITH (FORCE)`)
		maintenance.Close(ctx)
		t.Fatal("history matrix database unavailable")
	}
	t.Cleanup(func() {
		database.runtime.Close()
		database.owner.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_, _ = maintenance.Exec(cleanupCtx, `DROP DATABASE `+pgx.Identifier{databaseName}.Sanitize()+` WITH (FORCE)`)
		maintenance.Close(cleanupCtx)
	})
	return database
}

func TestAccountInventoryHistoryPostgresRollupPublicationMatrix(t *testing.T) {
	t.Run("UTC eligibility boundary", testHistoryUTCEligibilityBoundary)
	t.Run("planner and provider aggregation", testHistoryPlannerProviderMatrix)
	t.Run("incomplete segment publication gates", testHistoryIncompleteSegmentPublicationGates)
	t.Run("coverage boundaries", testHistoryCoveragePublicationMatrix)
	t.Run("finalize transaction atomicity", testHistoryRollupFinalizeAtomicity)
	t.Run("last segment concurrent completion", testHistoryLastSegmentConcurrentCompletion)
}

func TestAccountInventoryHistoryPostgresPlannerCatalogGate(t *testing.T) {
	ctx := context.Background()
	database := openHistoryPool(t, "CONTROL_HISTORY_MIGRATOR_TEST_URL")
	var plannerDefinition, timezone, finalizeDefinition string
	if err := database.QueryRow(ctx, `SELECT pg_get_functiondef(function.oid),
		coalesce((SELECT split_part(setting,'=',2) FROM unnest(function.proconfig) AS setting
			WHERE lower(split_part(setting,'=',1))='timezone'),''),
		pg_get_functiondef('public.control_finalize_account_inventory_daily_rollup_v1(uuid,uuid)'::regprocedure)
		FROM pg_proc AS function
		WHERE function.oid='public.control_plan_account_inventory_history_v1(integer)'::regprocedure`).
		Scan(&plannerDefinition, &timezone, &finalizeDefinition); err != nil {
		t.Fatal("history production planner catalog is unavailable")
	}
	normalizedPlanner := strings.ReplaceAll(
		strings.ToLower(strings.Join(strings.Fields(plannerDefinition), " ")), " ", "")
	eligibilityGate := "database_now:=clock_timestamp();eligible_last_date:=((database_now-interval'72hours')attimezone'utc')::date-1;"
	if strings.Count(normalizedPlanner, eligibilityGate) != 1 ||
		!strings.Contains(normalizedPlanner, "((eligible_last_date+1)::timestampattimezone'utc')") {
		t.Fatal("history production planner lost its UTC inclusive 72-hour gate")
	}
	if timezone != "UTC" {
		t.Fatal("history production planner does not force UTC")
	}
	normalizedFinalize := strings.ReplaceAll(
		strings.ToLower(strings.Join(strings.Fields(finalizeDefinition), " ")), " ", "")
	if strings.Count(normalizedFinalize,
		"promotion_applied_count*10000::bigint>=expected_poll_count*9500::bigint") != 1 {
		t.Fatal("history production rollup finalizer lost its 9500 basis-point threshold")
	}
}

func testHistoryRollupFinalizeAtomicity(t *testing.T) {
	ctx := context.Background()
	database := newHistoryMatrixDatabase(t)
	targetDate := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -4)
	const runID = "30000000-0000-0000-0000-000000000040"
	const fence = "40000000-0000-0000-0000-000000000040"
	if _, err := database.owner.Exec(ctx, `
		INSERT INTO node_drivers(node_type,driver_contract_version,display_name)
		VALUES('history-rollup-atomic','v1','History Rollup Atomic');
		INSERT INTO relay_node_assets(
			instance_id,display_name,node_type,driver_contract_version,management_endpoint
		) VALUES('10000000-0000-0000-0000-000000000040','atomic',
			'history-rollup-atomic','v1','http://history-rollup-atomic.invalid');
		INSERT INTO provider_inventory_policy_versions(
			policy_version_id,node_type,driver_contract_version,active_providers,
			out_of_scope_providers,created_by,created_at
		) VALUES('20000000-0000-0000-0000-000000000040','history-rollup-atomic','v1',
			ARRAY['openai'],ARRAY['legacy'],'acceptance',$1);
		INSERT INTO provider_inventory_policy_activations(
			node_type,driver_contract_version,policy_version_id,effective_from,effective_to,
			activated_by,created_at
		) VALUES('history-rollup-atomic','v1','20000000-0000-0000-0000-000000000040',
			$1,$1::timestamptz+interval '1 day','acceptance',$1);
		INSERT INTO relay_node_inventory_monitoring_activations(
			instance_id,effective_from,effective_to,reason,actor,
			end_reason,end_actor,end_recorded_at,created_at
		) VALUES('10000000-0000-0000-0000-000000000040',$1,$1::timestamptz+interval '1 day',
			'reconciliation','acceptance','reconciliation','acceptance',
			$1::timestamptz+interval '1 day',$1);
		INSERT INTO account_inventory_compaction_runs(
			compaction_run_id,summary_date,instance_id,provider_policy_version,status,
			checksum_version,source_snapshot_count,source_poll_count,
			source_provider_result_count,source_duplicate_count,source_checksum,
			deleted_snapshot_count,created_at,summarized_at,deleting_at,completed_at,updated_at
		) VALUES('50000000-0000-0000-0000-000000000040',$1::date,
			'10000000-0000-0000-0000-000000000040','20000000-0000-0000-0000-000000000040',
			'completed',1,0,0,0,0,decode(repeat('00',32),'hex'),0,
			$1::timestamptz+interval '1 day',$1::timestamptz+interval '1 day 1 second',
			$1::timestamptz+interval '1 day 2 seconds',$1::timestamptz+interval '1 day 3 seconds',
			$1::timestamptz+interval '1 day 3 seconds');
		INSERT INTO account_inventory_daily_summaries(
			compaction_run_id,summary_date,instance_id,provider,account_key,
			provider_policy_version,first_scheduled_at,last_scheduled_at,
			first_observed_at,last_observed_at,last_basic_status,sample_count,
			disabled_count,unavailable_count,error_count,active_count,unknown_count,
			first_success_count,last_success_count,success_reset_count,
			first_failed_count,last_failed_count,failed_reset_count,created_at
		) VALUES('50000000-0000-0000-0000-000000000040',$1::date,
			'10000000-0000-0000-0000-000000000040','openai','openai:atomic',
			'20000000-0000-0000-0000-000000000040',$1::timestamptz+interval '5 minutes',
			$1::timestamptz+interval '5 minutes',$1::timestamptz+interval '5 minutes 1 second',
			$1::timestamptz+interval '5 minutes 1 second','active',1,0,0,0,1,0,1,1,0,0,0,0,
			$1::timestamptz+interval '1 day');
		INSERT INTO account_inventory_daily_provider_summaries(
			compaction_run_id,summary_date,instance_id,provider,provider_policy_version,
			expected_poll_count,transport_success_count,contract_valid_count,
			snapshot_complete_count,promotion_applied_count,promotion_skipped_count,
			policy_changed_count,abandoned_count,degraded_count,first_promotion_at,last_promotion_at,
			coverage_numerator,coverage_denominator,coverage_ratio,
			coverage_threshold_basis_points,coverage_status,created_at
		) VALUES('50000000-0000-0000-0000-000000000040',$1::date,
			'10000000-0000-0000-0000-000000000040','openai',
			'20000000-0000-0000-0000-000000000040',1,1,1,1,1,0,0,0,0,
			$1::timestamptz+interval '5 minutes',$1::timestamptz+interval '5 minutes',
			1,1,1,9500,'complete',$1::timestamptz+interval '1 day');
		INSERT INTO account_inventory_daily_rollup_runs(
			rollup_run_id,summary_date,instance_id,status,claim_owner,lease_expires_at,
			fencing_token,attempt_count,created_at,updated_at
		) VALUES($2::uuid,$1::date,'10000000-0000-0000-0000-000000000040','pending',
			'atomic-worker',clock_timestamp()+interval '5 minutes',$3::uuid,1,
			$1::timestamptz+interval '1 day',clock_timestamp());
		CREATE FUNCTION public.test_reject_history_rollup_finalize()
		RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
		BEGIN
			RAISE EXCEPTION 'synthetic history rollup finalize failure';
		END;
		$$`, pgx.QueryExecModeSimpleProtocol, targetDate, runID, fence); err != nil {
		t.Fatalf("history rollup atomicity fixture failed SQLSTATE=%s", postgresSQLState(err))
	}

	failures := []struct {
		name, create, drop string
		deferredCommit     bool
	}{
		{"account final row", `CREATE TRIGGER zz_test_reject_rollup_account
			BEFORE INSERT ON account_inventory_daily_account_rollups FOR EACH ROW
			EXECUTE FUNCTION public.test_reject_history_rollup_finalize()`,
			`DROP TRIGGER zz_test_reject_rollup_account ON account_inventory_daily_account_rollups`, false},
		{"provider final row", `CREATE TRIGGER zz_test_reject_rollup_provider
			BEFORE INSERT ON account_inventory_daily_provider_rollups FOR EACH ROW
			EXECUTE FUNCTION public.test_reject_history_rollup_finalize()`,
			`DROP TRIGGER zz_test_reject_rollup_provider ON account_inventory_daily_provider_rollups`, false},
		{"segment proof and completed run", `CREATE TRIGGER zz_test_reject_rollup_run
			BEFORE UPDATE ON account_inventory_daily_rollup_runs FOR EACH ROW
			EXECUTE FUNCTION public.test_reject_history_rollup_finalize()`,
			`DROP TRIGGER zz_test_reject_rollup_run ON account_inventory_daily_rollup_runs`, false},
		{"deferred audit commit", `CREATE CONSTRAINT TRIGGER zz_test_reject_rollup_audit
			AFTER INSERT ON audit_logs DEFERRABLE INITIALLY DEFERRED FOR EACH ROW
			EXECUTE FUNCTION public.test_reject_history_rollup_finalize()`,
			`DROP TRIGGER zz_test_reject_rollup_audit ON audit_logs`, true},
	}
	for _, failure := range failures {
		t.Run(failure.name, func(t *testing.T) {
			if _, err := database.owner.Exec(ctx, failure.create); err != nil {
				t.Fatal("history rollup rejection trigger creation failed")
			}
			t.Cleanup(func() { _, _ = database.owner.Exec(ctx, failure.drop) })
			if failure.deferredCommit {
				tx, err := database.runtime.Begin(ctx)
				if err != nil {
					t.Fatal("history rollup deferred transaction unavailable")
				}
				var status string
				var accountRows, providerRows int
				if err := tx.QueryRow(ctx, `WITH finalized AS (
					SELECT public.control_finalize_account_inventory_daily_rollup_v1($1::uuid,$2::uuid) AS value
				) SELECT value->>'status',
					(value->>'account_rollup_count')::integer,
					(value->>'provider_rollup_count')::integer
					FROM finalized`, runID, fence).Scan(&status, &accountRows, &providerRows); err != nil ||
					status != "completed" || accountRows != 1 || providerRows != 1 {
					_ = tx.Rollback(ctx)
					t.Fatal("history rollup did not reach the deferred commit barrier")
				}
				var idempotent bool
				if err := tx.QueryRow(ctx, `WITH finalized AS (
					SELECT public.control_finalize_account_inventory_daily_rollup_v1($1::uuid,$2::uuid) AS value
				) SELECT (value->>'account_rollup_count')::integer,
					(value->>'provider_rollup_count')::integer,(value->>'idempotent')::boolean
					FROM finalized`, runID, fence).Scan(&accountRows, &providerRows, &idempotent); err != nil ||
					accountRows != 1 || providerRows != 1 || !idempotent {
					_ = tx.Rollback(ctx)
					t.Fatal("history rollup final rows were not visible before deferred commit")
				}
				if err := tx.Commit(ctx); err == nil {
					t.Fatal("history rollup deferred audit commit unexpectedly succeeded")
				}
			} else if _, err := database.runtime.Exec(ctx, `SELECT
				public.control_finalize_account_inventory_daily_rollup_v1($1::uuid,$2::uuid)`,
				runID, fence); err == nil {
				t.Fatal("history rollup finalize ignored an injected failure")
			}
			var atomic bool
			if err := database.owner.QueryRow(ctx, `SELECT run.status='pending'
				AND run.fencing_token=$2::uuid AND run.expected_segment_count IS NULL
				AND run.completed_segment_count IS NULL AND run.checksum_version IS NULL
				AND run.segment_checksum IS NULL AND run.completed_fencing_token IS NULL
				AND run.completed_at IS NULL
				AND NOT EXISTS (SELECT 1 FROM account_inventory_daily_account_rollups WHERE rollup_run_id=$1::uuid)
				AND NOT EXISTS (SELECT 1 FROM account_inventory_daily_provider_rollups WHERE rollup_run_id=$1::uuid)
				AND NOT EXISTS (SELECT 1 FROM audit_logs WHERE category='account_inventory_history'
					AND action='account_inventory_history.completed'
					AND details->>'phase'='rollup_complete')
				FROM account_inventory_daily_rollup_runs AS run WHERE run.rollup_run_id=$1::uuid`,
				runID, fence).Scan(&atomic); err != nil || !atomic {
				t.Fatal("history rollup failure left a partial publication")
			}
		})
	}

	var status string
	var accountRows, providerRows, expected, completed, auditRows int
	var checksumPresent bool
	if err := database.runtime.QueryRow(ctx, `WITH finalized AS (
		SELECT public.control_finalize_account_inventory_daily_rollup_v1($1::uuid,$2::uuid) AS value
	) SELECT value->>'status',(value->>'account_rollup_count')::integer,
		(value->>'provider_rollup_count')::integer,(value->>'expected_segment_count')::integer,
		(value->>'completed_segment_count')::integer,value->>'segment_checksum_hex' IS NOT NULL
		FROM finalized`, runID, fence).Scan(&status, &accountRows, &providerRows,
		&expected, &completed, &checksumPresent); err != nil {
		t.Fatal("history rollup retry failed")
	}
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs
		WHERE category='account_inventory_history' AND action='account_inventory_history.completed'
		AND details->>'phase'='rollup_complete'`).Scan(&auditRows); err != nil {
		t.Fatal("history rollup success audit query failed")
	}
	if status != "completed" || accountRows != 1 || providerRows != 1 || expected != 1 ||
		completed != 1 || !checksumPresent || auditRows != 1 {
		t.Fatalf("history rollup retry status=%s account=%d provider=%d expected=%d completed=%d checksum=%t audit=%d",
			status, accountRows, providerRows, expected, completed, checksumPresent, auditRows)
	}
}

func testHistoryIncompleteSegmentPublicationGates(t *testing.T) {
	ctx := context.Background()
	database := newHistoryMatrixDatabase(t)
	targetDate := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -4)
	if _, err := database.owner.Exec(ctx, `
		INSERT INTO node_drivers(node_type,driver_contract_version,display_name)
		VALUES('history-status-matrix','v1','History Status Matrix');
		INSERT INTO relay_node_assets(
			instance_id,display_name,node_type,driver_contract_version,management_endpoint
		) SELECT instance_id,label,'history-status-matrix','v1','http://history-status-matrix.invalid'
		  FROM (VALUES
			('10000000-0000-0000-0000-000000000030'::uuid,'missing'),
			('10000000-0000-0000-0000-000000000031'::uuid,'pending'),
			('10000000-0000-0000-0000-000000000032'::uuid,'deleting'),
			('10000000-0000-0000-0000-000000000033'::uuid,'failed')
		  ) AS fixture(instance_id,label);
		INSERT INTO provider_inventory_policy_versions(
			policy_version_id,node_type,driver_contract_version,active_providers,
			out_of_scope_providers,created_by,created_at
		) VALUES('20000000-0000-0000-0000-000000000030','history-status-matrix','v1',
			ARRAY['openai'],ARRAY['legacy'],'acceptance',$1);
		INSERT INTO provider_inventory_policy_activations(
			node_type,driver_contract_version,policy_version_id,effective_from,effective_to,
			activated_by,created_at
		) VALUES('history-status-matrix','v1','20000000-0000-0000-0000-000000000030',
			$1,$1::timestamptz+interval '1 day','acceptance',$1);
		INSERT INTO relay_node_inventory_monitoring_activations(
			instance_id,effective_from,effective_to,reason,actor,
			end_reason,end_actor,end_recorded_at,created_at
		) SELECT instance_id,$1,$1::timestamptz+interval '1 day','reconciliation','acceptance',
			'reconciliation','acceptance',$1::timestamptz+interval '1 day',$1
		  FROM relay_node_assets WHERE node_type='history-status-matrix';
		INSERT INTO account_inventory_compaction_runs(
			compaction_run_id,summary_date,instance_id,provider_policy_version,status
		) VALUES('30000000-0000-0000-0000-000000000031',$1::date,
			'10000000-0000-0000-0000-000000000031','20000000-0000-0000-0000-000000000030','pending');
		INSERT INTO account_inventory_compaction_runs(
			compaction_run_id,summary_date,instance_id,provider_policy_version,status,
			claim_owner,lease_expires_at,fencing_token,attempt_count,checksum_version,
			source_snapshot_count,source_poll_count,source_provider_result_count,
			source_duplicate_count,source_checksum,deleted_snapshot_count,
			created_at,summarized_at,deleting_at,updated_at
		) VALUES('30000000-0000-0000-0000-000000000032',$1::date,
			'10000000-0000-0000-0000-000000000032','20000000-0000-0000-0000-000000000030',
			'deleting','status-worker',clock_timestamp()+interval '5 minutes',
			'40000000-0000-0000-0000-000000000032',1,1,0,0,0,0,
			decode(repeat('00',32),'hex'),0,$1::timestamptz+interval '1 day',
			$1::timestamptz+interval '1 day 1 second',$1::timestamptz+interval '1 day 2 seconds',clock_timestamp());
		INSERT INTO account_inventory_compaction_runs(
			compaction_run_id,summary_date,instance_id,provider_policy_version,status,
			failed_from,failure_reason,created_at,failed_at,updated_at
		) VALUES('30000000-0000-0000-0000-000000000033',$1::date,
			'10000000-0000-0000-0000-000000000033','20000000-0000-0000-0000-000000000030',
			'failed','pending','internal',$1::timestamptz+interval '1 day',
			$1::timestamptz+interval '1 day 1 second',$1::timestamptz+interval '1 day 1 second')`,
		pgx.QueryExecModeSimpleProtocol, targetDate); err != nil {
		t.Fatalf("history incomplete-segment fixture failed SQLSTATE=%s", postgresSQLState(err))
	}
	var created, rollups int
	if err := database.runtime.QueryRow(ctx, `WITH planned AS (
		SELECT public.control_plan_account_inventory_history_v1(10) AS value
	) SELECT (value->>'compaction_runs_created')::integer,
		(value->>'rollup_runs_created')::integer FROM planned`).Scan(&created, &rollups); err != nil ||
		created != 1 || rollups != 0 {
		t.Fatal("history incomplete segment published a rollup")
	}
	var shapeOK bool
	if err := database.owner.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE status='pending')=2
		AND count(*) FILTER (WHERE status='deleting')=1
		AND count(*) FILTER (WHERE status='failed')=1
		AND NOT EXISTS (SELECT 1 FROM account_inventory_daily_rollup_runs)
		FROM account_inventory_compaction_runs`).Scan(&shapeOK); err != nil || !shapeOK {
		t.Fatal("history incomplete segment status matrix mismatch")
	}
}

func testHistoryUTCEligibilityBoundary(t *testing.T) {
	ctx := context.Background()
	database := newHistoryMatrixDatabase(t)
	var matched bool
	if err := database.owner.QueryRow(ctx, `SELECT bool_and(
		(((summary_date+1)::timestamp AT TIME ZONE 'UTC') <= database_now-interval '72 hours')=want)
		FROM (VALUES
			(DATE '2026-03-08',timestamptz '2026-03-11 23:59:59.999999+00',false),
			(DATE '2026-03-08',timestamptz '2026-03-12 00:00:00+00',true),
			(DATE '2026-03-08',timestamptz '2026-03-11 17:00:00-07',true)
		) AS boundary(summary_date,database_now,want)`).Scan(&matched); err != nil || !matched {
		t.Fatal("history UTC 72-hour eligibility boundary mismatch")
	}
	for _, zone := range []string{"America/Los_Angeles", "Asia/Shanghai"} {
		if err := database.owner.QueryRow(ctx, `WITH configured AS (
			SELECT set_config('TimeZone',$1,true)
		) SELECT extract(epoch FROM (
			((DATE '2026-03-08'+1)::timestamp AT TIME ZONE 'UTC')
			-(DATE '2026-03-08'::timestamp AT TIME ZONE 'UTC')))=86400
		FROM configured`, zone).Scan(&matched); err != nil || !matched {
			t.Fatal("history UTC day changed with session timezone")
		}
	}
}

func testHistoryPlannerProviderMatrix(t *testing.T) {
	ctx := context.Background()
	database := newHistoryMatrixDatabase(t)
	targetDate := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -4)
	if _, err := database.owner.Exec(ctx, `
		INSERT INTO node_drivers(node_type,driver_contract_version,display_name)
		VALUES('history-provider-matrix','v1','History Provider Matrix');
		INSERT INTO relay_node_assets(
			instance_id,display_name,node_type,driver_contract_version,management_endpoint
		) VALUES
			('10000000-0000-0000-0000-000000000020','mixed','history-provider-matrix','v1','http://history-provider-matrix.invalid'),
			('10000000-0000-0000-0000-000000000021','zero','history-provider-matrix','v1','http://history-provider-matrix.invalid'),
			('10000000-0000-0000-0000-000000000022','no-slot','history-provider-matrix','v1','http://history-provider-matrix.invalid'),
			('10000000-0000-0000-0000-000000000023','abandoned','history-provider-matrix','v1','http://history-provider-matrix.invalid'),
			('10000000-0000-0000-0000-000000000024','ineligible-next-day','history-provider-matrix','v1','http://history-provider-matrix.invalid');
		INSERT INTO provider_inventory_policy_versions(
			policy_version_id,node_type,driver_contract_version,active_providers,
			out_of_scope_providers,created_by,created_at
		) VALUES('20000000-0000-0000-0000-000000000020','history-provider-matrix','v1',
			ARRAY['anthropic','openai'],ARRAY['legacy'],'acceptance',$1);
		INSERT INTO provider_inventory_policy_activations(
			node_type,driver_contract_version,policy_version_id,effective_from,effective_to,
			activated_by,created_at
		) VALUES('history-provider-matrix','v1','20000000-0000-0000-0000-000000000020',
			$1::timestamptz+interval '2 minutes',$1::timestamptz+interval '1 day 10 minutes','acceptance',$1::timestamptz+interval '2 minutes');
		INSERT INTO relay_node_inventory_monitoring_activations(
			instance_id,effective_from,effective_to,reason,actor,
			end_reason,end_actor,end_recorded_at,created_at
		) VALUES
			('10000000-0000-0000-0000-000000000020',$1::timestamptz+interval '2 minutes',$1::timestamptz+interval '25 minutes','reconciliation','acceptance','reconciliation','acceptance',$1::timestamptz+interval '25 minutes',$1::timestamptz+interval '2 minutes'),
			('10000000-0000-0000-0000-000000000021',$1::timestamptz+interval '2 minutes',$1::timestamptz+interval '6 minutes','reconciliation','acceptance','reconciliation','acceptance',$1::timestamptz+interval '6 minutes',$1::timestamptz+interval '2 minutes'),
			('10000000-0000-0000-0000-000000000021',$1::timestamptz+interval '9 minutes',$1::timestamptz+interval '16 minutes','reconciliation','acceptance','reconciliation','acceptance',$1::timestamptz+interval '16 minutes',$1::timestamptz+interval '9 minutes'),
			('10000000-0000-0000-0000-000000000022',$1::timestamptz+interval '2 minutes',$1::timestamptz+interval '4 minutes','reconciliation','acceptance','reconciliation','acceptance',$1::timestamptz+interval '4 minutes',$1::timestamptz+interval '2 minutes'),
			('10000000-0000-0000-0000-000000000023',$1::timestamptz+interval '2 minutes',$1::timestamptz+interval '11 minutes','reconciliation','acceptance','reconciliation','acceptance',$1::timestamptz+interval '11 minutes',$1::timestamptz+interval '2 minutes'),
			('10000000-0000-0000-0000-000000000024',$1::timestamptz+interval '23 hours 55 minutes',$1::timestamptz+interval '1 day 10 minutes','reconciliation','acceptance','reconciliation','acceptance',$1::timestamptz+interval '1 day 10 minutes',$1::timestamptz+interval '23 hours 55 minutes');
		INSERT INTO account_inventory_poll_runs(
			poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,
			provider_policy_version,status,attempt_count,max_attempts,poll_start_grace_seconds,
			created_at,first_started_at,last_started_at,finalized_at,observed_at,
			transport_success,response_shape_valid,contract_valid,inventory_mode,
			node_identity_complete,snapshot_complete,degraded,promotion_skipped_reason,result,reason,
			source_record_count,identifiable_record_count,unidentified_record_count,
			unsupported_provider_count,out_of_scope_provider_count,node_version,node_commit
		) VALUES
			('50000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000020',
			 'history-provider-matrix','v1',$1::timestamptz+interval '5 minutes',
			 '20000000-0000-0000-0000-000000000020','finalized',1,2,299,
			 $1::timestamptz+interval '5 minutes 1 second',$1::timestamptz+interval '5 minutes 2 seconds',
			 $1::timestamptz+interval '5 minutes 2 seconds',$1::timestamptz+interval '5 minutes 4 seconds',
			 $1::timestamptz+interval '5 minutes 3 seconds',true,true,true,'runtime',true,false,true,NULL,
			 'degraded','none',0,0,0,0,0,'unknown','unknown'),
			('50000000-0000-0000-0000-000000000002','10000000-0000-0000-0000-000000000020',
			 'history-provider-matrix','v1',$1::timestamptz+interval '10 minutes',
			 '20000000-0000-0000-0000-000000000020','finalized',1,2,299,
			 $1::timestamptz+interval '10 minutes 1 second',$1::timestamptz+interval '10 minutes 2 seconds',
			 $1::timestamptz+interval '10 minutes 2 seconds',$1::timestamptz+interval '10 minutes 4 seconds',
			 $1::timestamptz+interval '10 minutes 3 seconds',true,true,true,'runtime',true,true,false,'policy_changed',
			 'success','none',0,0,0,0,0,'unknown','unknown');
		INSERT INTO account_inventory_poll_runs(
			poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,
			provider_policy_version,status,created_at,abandoned_at,execution_reason
		) VALUES('50000000-0000-0000-0000-000000000003','10000000-0000-0000-0000-000000000020',
			'history-provider-matrix','v1',$1::timestamptz+interval '15 minutes',
			'20000000-0000-0000-0000-000000000020','abandoned',$1::timestamptz+interval '15 minutes 1 second',
			$1::timestamptz+interval '15 minutes 2 seconds','poll_start_grace_expired'),
			('50000000-0000-0000-0000-000000000004','10000000-0000-0000-0000-000000000023',
			'history-provider-matrix','v1',$1::timestamptz+interval '5 minutes',
			'20000000-0000-0000-0000-000000000020','abandoned',$1::timestamptz+interval '5 minutes 1 second',
			$1::timestamptz+interval '5 minutes 2 seconds','poll_start_grace_expired'),
			('50000000-0000-0000-0000-000000000005','10000000-0000-0000-0000-000000000023',
			'history-provider-matrix','v1',$1::timestamptz+interval '10 minutes',
			'20000000-0000-0000-0000-000000000020','abandoned',$1::timestamptz+interval '10 minutes 1 second',
			$1::timestamptz+interval '10 minutes 2 seconds','poll_start_grace_expired');
		ALTER TABLE account_inventory_poll_provider_results DISABLE TRIGGER account_inventory_poll_provider_results_guard;
		INSERT INTO account_inventory_poll_provider_results(
			poll_run_id,provider,identifiable_count,missing_identity_count,duplicate_identity_count,
			identity_complete,snapshot_complete,degraded,reason,promotion_applied,promotion_skipped_reason
		) VALUES
			('50000000-0000-0000-0000-000000000001','anthropic',0,0,0,true,true,false,'complete',true,NULL),
			('50000000-0000-0000-0000-000000000001','openai',0,0,0,true,false,true,'contract_invalid',false,'contract_invalid'),
			('50000000-0000-0000-0000-000000000002','anthropic',0,0,0,true,true,false,'complete',false,'policy_changed'),
			('50000000-0000-0000-0000-000000000002','openai',0,0,0,true,true,false,'complete',false,'policy_changed');
		ALTER TABLE account_inventory_poll_provider_results ENABLE TRIGGER account_inventory_poll_provider_results_guard`, pgx.QueryExecModeSimpleProtocol, targetDate); err != nil {
		t.Fatalf("history planner/provider fixture failed SQLSTATE=%s", postgresSQLState(err))
	}
	var plannedCompactions int
	if err := database.runtime.QueryRow(ctx, `SELECT
		(public.control_plan_account_inventory_history_v1(10)->>'compaction_runs_created')::integer`).Scan(&plannedCompactions); err != nil || plannedCompactions != 4 {
		t.Fatal("history planner nonaligned/zero/no-slot matrix mismatch")
	}
	var shortRuns int
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM account_inventory_compaction_runs
		WHERE instance_id='10000000-0000-0000-0000-000000000022'`).Scan(&shortRuns); err != nil || shortRuns != 0 {
		t.Fatal("history planner invented a slot for a short interval")
	}
	var ineligibleRuns int
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM account_inventory_compaction_runs
		WHERE summary_date>$1::date`, targetDate).Scan(&ineligibleRuns); err != nil || ineligibleRuns != 0 {
		t.Fatal("history planner created an ineligible next-day segment")
	}
	for range 4 {
		var runID, fence string
		if err := database.runtime.QueryRow(ctx, `SELECT compaction_run_id::text,fencing_token::text
			FROM public.control_claim_account_inventory_compaction_v1(gen_random_uuid(),30)`).Scan(&runID, &fence); err != nil {
			t.Fatal("history planner/provider compaction claim failed")
		}
		var status string
		if err := database.runtime.QueryRow(ctx, `SELECT
			public.control_summarize_account_inventory_compaction_v1($1::uuid,$2::uuid)->>'status'`,
			runID, fence).Scan(&status); err != nil || status != "summarized" {
			t.Fatal("history planner/provider summarize failed")
		}
	}

	type providerSummary struct {
		expected, transport, contract, snapshot              int
		applied, skipped, policyChanged, abandoned, degraded int
		status                                               string
		firstPromotion, lastPromotion                        *time.Time
	}
	summaries := map[string]providerSummary{}
	rows, err := database.owner.Query(ctx, `SELECT provider,expected_poll_count,
		transport_success_count,contract_valid_count,snapshot_complete_count,
		promotion_applied_count,promotion_skipped_count,policy_changed_count,
		abandoned_count,degraded_count,coverage_status,first_promotion_at,last_promotion_at
		FROM account_inventory_daily_provider_summaries
		WHERE instance_id='10000000-0000-0000-0000-000000000020' ORDER BY provider`)
	if err != nil {
		t.Fatal("history provider summaries unavailable")
	}
	for rows.Next() {
		var provider string
		var summary providerSummary
		if err := rows.Scan(&provider, &summary.expected, &summary.transport, &summary.contract,
			&summary.snapshot, &summary.applied, &summary.skipped,
			&summary.policyChanged, &summary.abandoned, &summary.degraded, &summary.status,
			&summary.firstPromotion, &summary.lastPromotion); err != nil {
			rows.Close()
			t.Fatal("history provider summary result invalid")
		}
		summaries[provider] = summary
	}
	rows.Close()
	anthropic, openai := summaries["anthropic"], summaries["openai"]
	if len(summaries) != 2 || anthropic.expected != 4 || anthropic.transport != 2 ||
		anthropic.contract != 2 || anthropic.snapshot != 2 || anthropic.applied != 1 ||
		anthropic.skipped != 1 || anthropic.policyChanged != 1 || anthropic.abandoned != 1 ||
		anthropic.degraded != 0 || anthropic.status != "partial" ||
		anthropic.firstPromotion == nil || anthropic.lastPromotion == nil ||
		openai.expected != 4 || openai.transport != 2 || openai.contract != 2 ||
		openai.snapshot != 1 || openai.applied != 0 || openai.skipped != 2 ||
		openai.policyChanged != 1 || openai.abandoned != 1 || openai.degraded != 1 ||
		openai.status != "partial" || openai.firstPromotion != nil || openai.lastPromotion != nil {
		t.Fatal("history Provider aggregation was not independent")
	}
	var zeroRows int
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM account_inventory_daily_provider_summaries
		WHERE instance_id='10000000-0000-0000-0000-000000000021'
		  AND expected_poll_count=3 AND promotion_applied_count=0 AND coverage_status='partial'`).Scan(&zeroRows); err != nil || zeroRows != 2 {
		t.Fatal("history zero-data segment was not explicit")
	}
	var abandonedRows int
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM account_inventory_daily_provider_summaries
		WHERE instance_id='10000000-0000-0000-0000-000000000023'
		  AND expected_poll_count=2 AND transport_success_count=0
		  AND contract_valid_count=0 AND snapshot_complete_count=0
		  AND promotion_applied_count=0 AND abandoned_count=2
		  AND coverage_status='partial'`).Scan(&abandonedRows); err != nil || abandonedRows != 2 {
		t.Fatal("history pure-abandoned day aggregation mismatch")
	}
}

func testHistoryCoveragePublicationMatrix(t *testing.T) {
	ctx := context.Background()
	database := newHistoryMatrixDatabase(t)
	targetDate := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -4)
	if _, err := database.owner.Exec(ctx, `
		INSERT INTO node_drivers(node_type,driver_contract_version,display_name)
		VALUES('history-matrix','v1','History Matrix');
		INSERT INTO relay_node_assets(
			instance_id,display_name,node_type,driver_contract_version,management_endpoint
		) VALUES
			('10000000-0000-0000-0000-000000000001','partial','history-matrix','v1','http://history-matrix.invalid'),
			('10000000-0000-0000-0000-000000000002','threshold','history-matrix','v1','http://history-matrix.invalid'),
			('10000000-0000-0000-0000-000000000003','complete','history-matrix','v1','http://history-matrix.invalid');
		INSERT INTO provider_inventory_policy_versions(
			policy_version_id,node_type,driver_contract_version,active_providers,
			out_of_scope_providers,created_by,created_at
		) VALUES('20000000-0000-0000-0000-000000000001','history-matrix','v1',
			ARRAY['openai'],ARRAY['legacy'],'acceptance',$1);
		INSERT INTO provider_inventory_policy_activations(
			node_type,driver_contract_version,policy_version_id,effective_from,effective_to,
			activated_by,created_at
		) VALUES('history-matrix','v1','20000000-0000-0000-0000-000000000001',$1,$1::timestamptz+interval '1 day','acceptance',$1);
		INSERT INTO relay_node_inventory_monitoring_activations(
			instance_id,effective_from,effective_to,reason,actor,
			end_reason,end_actor,end_recorded_at,created_at
		) SELECT instance_id,$1,$1::timestamptz+interval '1 day','reconciliation','acceptance',
			'reconciliation','acceptance',$1::timestamptz+interval '1 day',$1
		  FROM relay_node_assets WHERE node_type='history-matrix';
		INSERT INTO account_inventory_compaction_runs(
			compaction_run_id,summary_date,instance_id,provider_policy_version,status,
			checksum_version,source_snapshot_count,source_poll_count,
			source_provider_result_count,source_duplicate_count,source_checksum,
			deleted_snapshot_count,created_at,summarized_at,deleting_at,completed_at,updated_at
		) SELECT gen_random_uuid(),$1::date,instance_id,'20000000-0000-0000-0000-000000000001',
			'completed',1,0,0,0,0,decode(repeat('00',32),'hex'),0,
			$1::timestamptz+interval '1 day',$1::timestamptz+interval '1 day 1 second',
			$1::timestamptz+interval '1 day 2 seconds',$1::timestamptz+interval '1 day 3 seconds',
			$1::timestamptz+interval '1 day 3 seconds'
		  FROM relay_node_assets WHERE node_type='history-matrix';
		INSERT INTO account_inventory_daily_provider_summaries(
			compaction_run_id,summary_date,instance_id,provider,provider_policy_version,
			expected_poll_count,transport_success_count,contract_valid_count,
			snapshot_complete_count,promotion_applied_count,promotion_skipped_count,
			policy_changed_count,abandoned_count,degraded_count,first_promotion_at,last_promotion_at,
			coverage_numerator,coverage_denominator,coverage_ratio,
			coverage_threshold_basis_points,coverage_status
		) SELECT run.compaction_run_id,run.summary_date,run.instance_id,'openai',run.provider_policy_version,
			value.expected,value.applied,value.applied,value.applied,value.applied,
			value.expected-value.applied,0,0,0,$1::timestamptz+interval '1 hour',
			$1::timestamptz+interval '1 hour',value.applied,value.expected,
			round(value.applied::numeric/value.expected,8),9500,value.status
		  FROM account_inventory_compaction_runs AS run
		  JOIN (VALUES
			('10000000-0000-0000-0000-000000000001'::uuid,19,18,'partial'),
			('10000000-0000-0000-0000-000000000002'::uuid,20,19,'complete'),
			('10000000-0000-0000-0000-000000000003'::uuid,7,7,'complete')
		  ) AS value(instance_id,expected,applied,status) USING(instance_id)
		 WHERE run.summary_date=$1::date;
		INSERT INTO account_inventory_daily_summaries(
			compaction_run_id,summary_date,instance_id,provider,account_key,
			provider_policy_version,first_scheduled_at,last_scheduled_at,
			first_observed_at,last_observed_at,last_basic_status,sample_count,
			disabled_count,unavailable_count,error_count,active_count,unknown_count,
			first_success_count,last_success_count,success_reset_count,
			first_failed_count,last_failed_count,failed_reset_count,created_at
		) SELECT compaction_run_id,summary_date,instance_id,'openai',
			'openai:'||instance_id::text,provider_policy_version,
			$1::timestamptz+interval '5 minutes',$1::timestamptz+interval '5 minutes',
			$1::timestamptz+interval '5 minutes 1 second',$1::timestamptz+interval '5 minutes 1 second',
			'active',1,0,0,0,1,0,1,1,0,0,0,0,$1::timestamptz+interval '1 day'
		  FROM account_inventory_compaction_runs WHERE summary_date=$1::date`,
		pgx.QueryExecModeSimpleProtocol, targetDate); err != nil {
		t.Fatalf("history coverage matrix fixture failed SQLSTATE=%s", postgresSQLState(err))
	}

	rows, err := database.owner.Query(ctx, `
		SELECT label, CASE WHEN expected=0 THEN NULL
			ELSE applied*10000::bigint >= expected*9500::bigint END
		FROM (VALUES ('0/0',0::bigint,0::bigint),('94.99%',9499,10000),
			('95%',19,20),('100%',7,7)) AS boundary(label,applied,expected)
		ORDER BY label`)
	if err != nil {
		t.Fatal("history coverage boundary query failed")
	}
	boundaries := map[string]*bool{}
	for rows.Next() {
		var label string
		var complete *bool
		if err := rows.Scan(&label, &complete); err != nil {
			rows.Close()
			t.Fatal("history coverage boundary result invalid")
		}
		boundaries[label] = complete
	}
	rows.Close()
	if boundaries["0/0"] != nil || boundaries["94.99%"] == nil || *boundaries["94.99%"] ||
		boundaries["95%"] == nil || !*boundaries["95%"] ||
		boundaries["100%"] == nil || !*boundaries["100%"] {
		t.Fatal("history coverage threshold matrix mismatch")
	}

	var planned int
	if err := database.runtime.QueryRow(ctx, `SELECT
		(public.control_plan_account_inventory_history_v1(20)->>'rollup_runs_created')::integer`).Scan(&planned); err != nil || planned != 3 {
		t.Fatal("history coverage rollup planning mismatch")
	}
	if _, err := database.owner.Exec(ctx, `
		INSERT INTO account_inventory_daily_rollup_runs(
			rollup_run_id,summary_date,instance_id,status,created_at,updated_at,
			failure_reason,failed_at
		) VALUES
			('60000000-0000-0000-0000-000000000001',$1::date+1,
			 '10000000-0000-0000-0000-000000000001','pending',$1,$1,NULL,NULL),
			('60000000-0000-0000-0000-000000000002',$1::date-2,
			 '10000000-0000-0000-0000-000000000001','failed',$1,$1,'internal',$1);
		INSERT INTO account_inventory_daily_provider_rollups(
			rollup_run_id,summary_date,instance_id,provider,expected_poll_count,
			transport_success_count,contract_valid_count,snapshot_complete_count,
			promotion_applied_count,promotion_skipped_count,policy_changed_count,
			abandoned_count,degraded_count,first_promotion_at,last_promotion_at,
			coverage_numerator,coverage_denominator,coverage_ratio,
			coverage_threshold_basis_points,coverage_status,created_at
		) VALUES
			('60000000-0000-0000-0000-000000000001',$1::date+1,
			 '10000000-0000-0000-0000-000000000001','openai',1,1,1,1,1,0,0,0,0,
			 $1::timestamptz+interval '1 day 1 hour',
			 $1::timestamptz+interval '1 day 1 hour',1,1,1,9500,'complete',$1::timestamptz+interval '2 days'),
			('60000000-0000-0000-0000-000000000002',$1::date-2,
			 '10000000-0000-0000-0000-000000000001','openai',1,0,0,0,0,0,0,1,0,
			 NULL,NULL,0,1,0,9500,'partial',$1)`, pgx.QueryExecModeSimpleProtocol, targetDate); err != nil {
		t.Fatalf("history unreadable rollup fixture failed SQLSTATE=%s", postgresSQLState(err))
	}
	var premature int
	if err := database.runtime.QueryRow(ctx, `SELECT count(*)
		FROM public.control_list_account_inventory_history_metrics_v1()
		WHERE instance_id::text LIKE '10000000-0000-0000-0000-%'`).Scan(&premature); err != nil || premature != 0 {
		t.Fatal("pending history rollups were published")
	}
	var replayRunID, replayFence string
	for index := range 3 {
		var runID, fence string
		if err := database.runtime.QueryRow(ctx, `SELECT rollup_run_id::text,fencing_token::text
			FROM public.control_claim_account_inventory_daily_rollup_v1(gen_random_uuid(),30)`).Scan(&runID, &fence); err != nil {
			t.Fatal("history coverage rollup claim failed")
		}
		var status string
		if err := database.runtime.QueryRow(ctx, `SELECT
			public.control_finalize_account_inventory_daily_rollup_v1($1::uuid,$2::uuid)->>'status'`,
			runID, fence).Scan(&status); err != nil || status != "completed" {
			t.Fatalf("history coverage rollup finalize failed SQLSTATE=%s status=%s", postgresSQLState(err), status)
		}
		if index == 0 {
			replayRunID, replayFence = runID, fence
		}
	}
	var partial, complete int
	if err := database.runtime.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE NOT coverage_complete),
		count(*) FILTER (WHERE coverage_complete)
		FROM public.control_list_account_inventory_history_metrics_v1()
		WHERE instance_id::text LIKE '10000000-0000-0000-0000-%'`).Scan(&partial, &complete); err != nil || partial != 1 || complete != 2 {
		t.Fatal("history coverage publication status mismatch")
	}
	var before, after string
	const finalSnapshot = `SELECT jsonb_build_object(
		'run',to_jsonb(run),
		'accounts',(SELECT jsonb_agg(to_jsonb(row) ORDER BY account_rollup_id)
			FROM account_inventory_daily_account_rollups AS row WHERE row.rollup_run_id=run.rollup_run_id),
		'providers',(SELECT jsonb_agg(to_jsonb(row) ORDER BY provider_rollup_id)
			FROM account_inventory_daily_provider_rollups AS row WHERE row.rollup_run_id=run.rollup_run_id)
	)::text FROM account_inventory_daily_rollup_runs AS run WHERE run.rollup_run_id=$1::uuid`
	if err := database.owner.QueryRow(ctx, finalSnapshot, replayRunID).Scan(&before); err != nil {
		t.Fatal("history completed rollup snapshot unavailable")
	}
	for _, table := range []string{
		"account_inventory_daily_summaries", "account_inventory_daily_provider_summaries",
	} {
		_, err := database.owner.Exec(ctx, `UPDATE public.`+pgx.Identifier{table}.Sanitize()+`
			SET created_at=created_at WHERE summary_date=$1::date
			AND instance_id=(SELECT instance_id FROM account_inventory_daily_rollup_runs
				WHERE rollup_run_id=$2::uuid)`, targetDate, replayRunID)
		if !isInsufficientPrivilege(err) {
			t.Fatal("history completed segment was mutable")
		}
	}
	for _, table := range []string{
		"account_inventory_daily_account_rollups", "account_inventory_daily_provider_rollups",
	} {
		for _, statement := range []string{
			`UPDATE public.` + pgx.Identifier{table}.Sanitize() + ` SET created_at=created_at`,
			`DELETE FROM public.` + pgx.Identifier{table}.Sanitize(),
			`TRUNCATE public.` + pgx.Identifier{table}.Sanitize(),
		} {
			if _, err := database.owner.Exec(ctx, statement); !isInsufficientPrivilege(err) {
				t.Fatal("history completed final rollup was mutable")
			}
		}
	}
	var replayStatus string
	var idempotent bool
	var replayAccounts, replayProviders int
	if err := database.runtime.QueryRow(ctx, `WITH finalized AS (
		SELECT public.control_finalize_account_inventory_daily_rollup_v1($1::uuid,$2::uuid) AS value
	) SELECT value->>'status',(value->>'idempotent')::boolean,
		(value->>'account_rollup_count')::integer,(value->>'provider_rollup_count')::integer
	FROM finalized`, replayRunID, replayFence).Scan(&replayStatus, &idempotent,
		&replayAccounts, &replayProviders); err != nil || replayStatus != "completed" || !idempotent ||
		replayAccounts != 1 || replayProviders != 1 {
		t.Fatal("history completed rollup same-fence replay was not idempotent")
	}
	if err := database.owner.QueryRow(ctx, finalSnapshot, replayRunID).Scan(&after); err != nil || after != before {
		t.Fatal("history completed rollup was overwritten by a repeated worker")
	}
}

func testHistoryLastSegmentConcurrentCompletion(t *testing.T) {
	ctx := context.Background()
	database := newHistoryMatrixDatabase(t)
	targetDate := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -4)
	const finalRun = "30000000-0000-0000-0000-000000000002"
	const finalFence = "40000000-0000-0000-0000-000000000002"
	if _, err := database.owner.Exec(ctx, `
		INSERT INTO node_drivers(node_type,driver_contract_version,display_name)
		VALUES('history-race','v1','History Race');
		INSERT INTO relay_node_assets(
			instance_id,display_name,node_type,driver_contract_version,management_endpoint
		) VALUES('10000000-0000-0000-0000-000000000010','race','history-race','v1','http://history-race.invalid');
		INSERT INTO provider_inventory_policy_versions(
			policy_version_id,node_type,driver_contract_version,active_providers,
			out_of_scope_providers,created_by,created_at
		) VALUES
			('20000000-0000-0000-0000-000000000010','history-race','v1',ARRAY['openai'],ARRAY['legacy'],'acceptance',$1),
			('20000000-0000-0000-0000-000000000011','history-race','v1',ARRAY['openai'],ARRAY['legacy-2'],'acceptance',$1);
		INSERT INTO provider_inventory_policy_activations(
			node_type,driver_contract_version,policy_version_id,effective_from,effective_to,
			activated_by,created_at
		) VALUES
			('history-race','v1','20000000-0000-0000-0000-000000000010',$1,$1::timestamptz+interval '12 hours','acceptance',$1),
			('history-race','v1','20000000-0000-0000-0000-000000000011',$1::timestamptz+interval '12 hours',$1::timestamptz+interval '1 day','acceptance',$1::timestamptz+interval '12 hours');
		INSERT INTO relay_node_inventory_monitoring_activations(
			instance_id,effective_from,effective_to,reason,actor,
			end_reason,end_actor,end_recorded_at,created_at
		) VALUES('10000000-0000-0000-0000-000000000010',$1,$1::timestamptz+interval '1 day',
			'reconciliation','acceptance','reconciliation','acceptance',$1::timestamptz+interval '1 day',$1);
		INSERT INTO account_inventory_compaction_runs(
			compaction_run_id,summary_date,instance_id,provider_policy_version,status,
			claim_owner,lease_expires_at,fencing_token,attempt_count,checksum_version,
			source_snapshot_count,source_poll_count,source_provider_result_count,
			source_duplicate_count,source_checksum,deleted_snapshot_count,
			created_at,summarized_at,deleting_at,completed_at,updated_at
		) VALUES
			('30000000-0000-0000-0000-000000000001',$1::date,'10000000-0000-0000-0000-000000000010',
			 '20000000-0000-0000-0000-000000000010','completed',NULL,NULL,NULL,0,1,0,0,0,0,
			 decode(repeat('00',32),'hex'),0,$1::timestamptz+interval '1 day',
			 $1::timestamptz+interval '1 day 1 second',$1::timestamptz+interval '1 day 2 seconds',
			 $1::timestamptz+interval '1 day 3 seconds',$1::timestamptz+interval '1 day 3 seconds'),
			($2::uuid,$1::date,'10000000-0000-0000-0000-000000000010',
			 '20000000-0000-0000-0000-000000000011','deleting','race-worker',clock_timestamp()+interval '5 minutes',
			 $3::uuid,1,1,0,0,0,0,decode(repeat('00',32),'hex'),0,
			 $1::timestamptz+interval '1 day',$1::timestamptz+interval '1 day 1 second',
			 $1::timestamptz+interval '1 day 2 seconds',NULL,clock_timestamp());
		INSERT INTO account_inventory_daily_provider_summaries(
			compaction_run_id,summary_date,instance_id,provider,provider_policy_version,
			expected_poll_count,transport_success_count,contract_valid_count,
			snapshot_complete_count,promotion_applied_count,promotion_skipped_count,
			policy_changed_count,abandoned_count,degraded_count,first_promotion_at,last_promotion_at,
			coverage_numerator,coverage_denominator,coverage_ratio,
			coverage_threshold_basis_points,coverage_status
		) SELECT compaction_run_id,summary_date,instance_id,'openai',provider_policy_version,
			144,144,144,144,144,0,0,0,0,$1::timestamptz+interval '1 hour',
			$1::timestamptz+interval '1 hour',144,144,1,9500,'complete'
		  FROM account_inventory_compaction_runs`, pgx.QueryExecModeSimpleProtocol, targetDate, finalRun, finalFence); err != nil {
		t.Fatalf("history last-segment race fixture failed SQLSTATE=%s", postgresSQLState(err))
	}
	var premature int
	if err := database.runtime.QueryRow(ctx, `SELECT
		(public.control_plan_account_inventory_history_v1(10)->>'rollup_runs_created')::integer`).Scan(&premature); err != nil || premature != 0 {
		t.Fatal("history rollup published before its last segment")
	}

	lockConnection, err := database.owner.Acquire(ctx)
	if err != nil {
		t.Fatal("history completion lock connection unavailable")
	}
	defer lockConnection.Release()
	lockTransaction, err := lockConnection.Begin(ctx)
	if err != nil {
		t.Fatal("history completion lock transaction unavailable")
	}
	defer func() { _ = lockTransaction.Rollback(ctx) }()
	var lockPID int32
	if err := lockTransaction.QueryRow(ctx, `SELECT pg_backend_pid()
		FROM account_inventory_compaction_runs WHERE compaction_run_id=$1::uuid FOR UPDATE`,
		finalRun).Scan(&lockPID); err != nil {
		t.Fatal("history completion target row lock failed")
	}
	completionConnection, err := database.runtime.Acquire(ctx)
	if err != nil {
		t.Fatal("history completion connection unavailable")
	}
	defer completionConnection.Release()
	var completionPID int32
	if err := completionConnection.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&completionPID); err != nil {
		t.Fatal("history completion backend identity unavailable")
	}
	completionResult := make(chan error, 1)
	go func() {
		var status string
		err := completionConnection.QueryRow(ctx, `SELECT
			public.control_complete_account_inventory_compaction_v1($1::uuid,$2::uuid,decode(repeat('00',32),'hex'))->>'status'`,
			finalRun, finalFence).Scan(&status)
		if err == nil && status != "completed" {
			err = errors.New("unexpected completion status")
		}
		completionResult <- err
	}()
	waitContext, cancelWait := context.WithTimeout(ctx, 5*time.Second)
	defer cancelWait()
	for {
		var blockedByTarget bool
		if err := database.owner.QueryRow(waitContext, `SELECT $1::integer=ANY(pg_blocking_pids($2::integer))`,
			lockPID, completionPID).Scan(&blockedByTarget); err != nil {
			t.Fatal("history completion row-lock barrier observation failed")
		}
		if blockedByTarget {
			break
		}
		select {
		case <-waitContext.Done():
			t.Fatal("history completion did not reach the target row lock")
		default:
		}
	}
	var duringCompletion int
	if err := database.runtime.QueryRow(ctx, `SELECT
		(public.control_plan_account_inventory_history_v1(10)->>'rollup_runs_created')::integer`).Scan(&duringCompletion); err != nil || duringCompletion != 0 {
		t.Fatal("history planner published while the last segment completion was blocked")
	}
	if err := lockTransaction.Commit(ctx); err != nil {
		t.Fatal("history completion target row lock release failed")
	}
	if err := <-completionResult; err != nil {
		t.Fatal("history last segment completion failed")
	}

	blocker, err := database.owner.Begin(ctx)
	if err != nil {
		t.Fatal("history planner barrier transaction unavailable")
	}
	defer func() { _ = blocker.Rollback(ctx) }()
	if _, err := blocker.Exec(ctx, `SELECT pg_advisory_xact_lock(72193847561029384)`); err != nil {
		t.Fatal("history planner barrier lock failed")
	}
	plannerConnections := make([]*pgxpool.Conn, 2)
	plannerPIDs := make([]int32, 2)
	for index := range plannerConnections {
		plannerConnections[index], err = database.runtime.Acquire(ctx)
		if err != nil {
			t.Fatal("history planner connection unavailable")
		}
		defer plannerConnections[index].Release()
		if err := plannerConnections[index].QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&plannerPIDs[index]); err != nil {
			t.Fatal("history planner backend identity unavailable")
		}
	}
	type plannerResult struct {
		created int
		err     error
	}
	results := make(chan plannerResult, 2)
	var workers sync.WaitGroup
	workers.Add(2)
	for _, connection := range plannerConnections {
		go func() {
			defer workers.Done()
			var result plannerResult
			result.err = connection.QueryRow(ctx, `SELECT
				(public.control_plan_account_inventory_history_v1(10)->>'rollup_runs_created')::integer`).Scan(&result.created)
			results <- result
		}()
	}
	plannerWaitContext, cancelPlannerWait := context.WithTimeout(ctx, 5*time.Second)
	defer cancelPlannerWait()
	for {
		var waiting int
		if err := database.owner.QueryRow(plannerWaitContext, `SELECT count(*) FROM pg_stat_activity
			WHERE pid=ANY($1::integer[]) AND wait_event='advisory'`, plannerPIDs).Scan(&waiting); err != nil {
			t.Fatal("history planner barrier observation failed")
		}
		if waiting == 2 {
			break
		}
		select {
		case <-plannerWaitContext.Done():
			t.Fatal("history planners did not reach the advisory barrier")
		default:
		}
	}
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal("history planner barrier release failed")
	}
	workers.Wait()
	created := 0
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatal("history concurrent rollup planning failed")
		}
		created += result.created
	}
	var rollupRuns int
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM account_inventory_daily_rollup_runs`).Scan(&rollupRuns); err != nil ||
		created != 1 || rollupRuns != 1 {
		t.Fatal("history last-segment race created duplicate or missing rollups")
	}
	var runID, fence string
	if err := database.runtime.QueryRow(ctx, `SELECT rollup_run_id::text,fencing_token::text
		FROM public.control_claim_account_inventory_daily_rollup_v1(gen_random_uuid(),30)`).Scan(&runID, &fence); err != nil {
		t.Fatal("history raced rollup claim failed")
	}
	var status string
	var expected, completed int
	if err := database.runtime.QueryRow(ctx, `WITH finalized AS (
		SELECT public.control_finalize_account_inventory_daily_rollup_v1($1::uuid,$2::uuid) AS value
	) SELECT value->>'status',(value->>'expected_segment_count')::integer,
		(value->>'completed_segment_count')::integer FROM finalized`, runID, fence).
		Scan(&status, &expected, &completed); err != nil || status != "completed" || expected != 2 || completed != 2 {
		t.Fatal("history raced rollup publication mismatch")
	}
}

func postgresSQLState(err error) string {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		return postgresError.Code + ":" + postgresError.ConstraintName
	}
	return "unknown"
}
