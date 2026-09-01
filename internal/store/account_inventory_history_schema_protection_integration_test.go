package store_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestAccountInventoryHistoryAggregateSchemaConstraintAndProtectionGateMatrix(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleSchemaFixture(t, ctx, database)
	targetDate := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -32)
	if _, err := database.owner.Exec(ctx, `UPDATE provider_inventory_policy_activations
		SET effective_from=$1,created_at=$1 WHERE policy_version_id=$2`, targetDate, fixture.policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_inventory_monitoring_activations(
		instance_id,effective_from,reason,actor,created_at
	) VALUES($1,$2,'reconciliation','history-schema-protection',$2)`, fixture.instanceID, targetDate); err != nil {
		t.Fatal(err)
	}

	fixture.baseSlot, fixture.nextPoll = targetDate.Add(12*time.Hour), 0
	oldPollID, oldPollFence := insertLifecycleGuardrailPoll(t, ctx, database, fixture, uuid.Nil, false)
	mode := "runtime"
	duplicateEvidence := lifecycleGuardrailEvidence{
		transportSuccess: true, responseShapeValid: true, contractValid: true,
		inventoryMode: &mode, nodeIdentityComplete: true, snapshotComplete: false,
		degraded: true, result: "degraded", reason: "none",
		sourceCount: 2, identifiableCount: 2,
		providerResults: []map[string]any{{
			"provider": fixtureProviderName, "identifiable_count": 2,
			"missing_identity_count": 0, "duplicate_identity_count": 1,
			"identity_complete": false, "snapshot_complete": false,
			"degraded": true, "reason": "identity_incomplete",
		}},
		snapshotItems: []map[string]any{},
		duplicateEvidence: []map[string]any{{
			"provider": fixtureProviderName, "account_key": fixtureProviderName + ":duplicate@example.invalid",
			"occurrence_count": 2,
		}},
	}
	if finalized, err := finalizeLifecycleGuardrailPoll(
		ctx, database, oldPollID, oldPollFence, duplicateEvidence,
	); err != nil || finalized != 1 {
		t.Fatalf("finalize retention poll rows=%d err=%v", finalized, err)
	}
	fixture.baseSlot, fixture.nextPoll = time.Now().UTC().Truncate(5*time.Minute).Add(-30*time.Minute), 0
	recentPollID := fixture.finalize(t, ctx, database, []lifecycleAccount{{
		email: "history-schema-protection@example.invalid", successCount: 1,
	}})
	recentSummaryDate := fixture.baseSlot.UTC().Truncate(24 * time.Hour)
	gateCompactionID := uuid.New()
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_compaction_runs(
		compaction_run_id,summary_date,instance_id,provider_policy_version,status,
		checksum_version,source_snapshot_count,source_poll_count,
		source_provider_result_count,source_duplicate_count,source_checksum,
		created_at,summarized_at,deleting_at,updated_at
	) VALUES($1,$2,$3,$4,'deleting',1,1,1,1,0,decode(repeat('40',32),'hex'),$5,$5,$5,$5)`,
		gateCompactionID, recentSummaryDate, fixture.instanceID, fixture.policyID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	compactionID, rollupID := uuid.New(), uuid.New()
	accountSummaryID, providerSummaryID := uuid.New(), uuid.New()
	accountRollupID, providerRollupID := uuid.New(), uuid.New()
	proofTime := targetDate.Add(26 * time.Hour)
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_compaction_runs(
		compaction_run_id,summary_date,instance_id,provider_policy_version,status,
		checksum_version,source_snapshot_count,source_poll_count,
		source_provider_result_count,source_duplicate_count,source_checksum,
		deleted_snapshot_count,created_at,summarized_at,deleting_at,completed_at,updated_at
	) VALUES($1,$2,$3,$4,'completed',1,0,1,1,1,decode(repeat('41',32),'hex'),0,
		$5,$5,$5,$5,$5);
	INSERT INTO account_inventory_daily_rollup_runs(
		rollup_run_id,summary_date,instance_id,status,completed_fencing_token,
		expected_segment_count,completed_segment_count,checksum_version,segment_checksum,
		created_at,completed_at,updated_at
	) VALUES($6,$2,$3,'completed',$7,1,1,1,decode(repeat('42',32),'hex'),$5,$5,$5)`,
		pgx.QueryExecModeSimpleProtocol, compactionID, targetDate, fixture.instanceID, fixture.policyID, proofTime,
		rollupID, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_daily_summaries(
		summary_id,compaction_run_id,summary_date,instance_id,provider,account_key,
		provider_policy_version,first_scheduled_at,last_scheduled_at,first_observed_at,
		last_observed_at,last_basic_status,sample_count,disabled_count,unavailable_count,
		error_count,active_count,unknown_count,first_success_count,last_success_count,
		success_reset_count,first_failed_count,last_failed_count,failed_reset_count,created_at
	) VALUES($1,$2,$3,$4,'openai','openai:schema@example.invalid',$5,
		$3::timestamptz+interval '1 hour',$3::timestamptz+interval '1 hour',
		$3::timestamptz+interval '1 hour 1 second',$3::timestamptz+interval '1 hour 1 second',
		'active',1,0,0,0,1,0,1,1,0,0,0,0,$6);
	INSERT INTO account_inventory_daily_provider_summaries(
		provider_summary_id,compaction_run_id,summary_date,instance_id,provider,
		provider_policy_version,expected_poll_count,transport_success_count,
		contract_valid_count,snapshot_complete_count,promotion_applied_count,
		promotion_skipped_count,policy_changed_count,abandoned_count,degraded_count,
		first_promotion_at,last_promotion_at,coverage_numerator,coverage_denominator,
		coverage_ratio,coverage_threshold_basis_points,coverage_status,created_at
	) VALUES($7,$2,$3,$4,'openai',$5,1,1,1,1,1,0,0,0,0,
		$3::timestamptz+interval '1 hour',$3::timestamptz+interval '1 hour',1,1,1,9500,'complete',$6);
	INSERT INTO account_inventory_daily_account_rollups(
		account_rollup_id,rollup_run_id,summary_date,instance_id,provider,account_key,
		first_scheduled_at,last_scheduled_at,first_observed_at,last_observed_at,
		last_basic_status,sample_count,disabled_count,unavailable_count,error_count,
		active_count,unknown_count,first_success_count,last_success_count,
		success_reset_count,first_failed_count,last_failed_count,failed_reset_count,created_at
	) VALUES($8,$9,$3,$4,'openai','openai:schema@example.invalid',
		$3::timestamptz+interval '1 hour',$3::timestamptz+interval '1 hour',
		$3::timestamptz+interval '1 hour 1 second',$3::timestamptz+interval '1 hour 1 second',
		'active',1,0,0,0,1,0,1,1,0,0,0,0,$6);
	INSERT INTO account_inventory_daily_provider_rollups(
		provider_rollup_id,rollup_run_id,summary_date,instance_id,provider,
		expected_poll_count,transport_success_count,contract_valid_count,
		snapshot_complete_count,promotion_applied_count,promotion_skipped_count,
		policy_changed_count,abandoned_count,degraded_count,first_promotion_at,last_promotion_at,
		coverage_numerator,coverage_denominator,coverage_ratio,
		coverage_threshold_basis_points,coverage_status,created_at
	) VALUES($10,$9,$3,$4,'openai',1,1,1,1,1,0,0,0,0,
		$3::timestamptz+interval '1 hour',$3::timestamptz+interval '1 hour',1,1,1,9500,'complete',$6)`,
		pgx.QueryExecModeSimpleProtocol, accountSummaryID, compactionID, targetDate, fixture.instanceID, fixture.policyID,
		proofTime, providerSummaryID, accountRollupID, rollupID, providerRollupID); err != nil {
		t.Fatal(err)
	}

	assertHistoryAggregateCatalog(t, ctx, database)

	tables := []struct {
		name       string
		primaryKey string
	}{
		{"account_inventory_daily_summaries", "summary_id"},
		{"account_inventory_daily_provider_summaries", "provider_summary_id"},
		{"account_inventory_daily_account_rollups", "account_rollup_id"},
		{"account_inventory_daily_provider_rollups", "provider_rollup_id"},
	}
	for _, table := range tables {
		columns := historyAggregateColumns[table.name]
		selectColumns := strings.Join(columns[1:], ",")
		statement := fmt.Sprintf("INSERT INTO %s(%s) SELECT %s FROM %s LIMIT 1",
			table.name, selectColumns, selectColumns, table.name)
		if _, err := database.owner.Exec(ctx, statement); err == nil {
			t.Fatalf("%s accepted duplicate business key", table.name)
		} else {
			requireHistorySQLState(t, err, "23505")
		}
	}
	invalidInserts := []string{
		`INSERT INTO account_inventory_daily_summaries SELECT
			(jsonb_populate_record(NULL::account_inventory_daily_summaries,
			 to_jsonb(source)||jsonb_build_object('summary_id',gen_random_uuid(),
			 'account_key','openai:invalid@example.invalid','active_count',2))).*
		 FROM account_inventory_daily_summaries AS source LIMIT 1`,
		`INSERT INTO account_inventory_daily_provider_summaries SELECT
			(jsonb_populate_record(NULL::account_inventory_daily_provider_summaries,
			 to_jsonb(source)||jsonb_build_object('provider_summary_id',gen_random_uuid(),
			 'provider','anthropic','coverage_status','partial'))).*
		 FROM account_inventory_daily_provider_summaries AS source LIMIT 1`,
		`INSERT INTO account_inventory_daily_account_rollups SELECT
			(jsonb_populate_record(NULL::account_inventory_daily_account_rollups,
			 to_jsonb(source)||jsonb_build_object('account_rollup_id',gen_random_uuid(),
			 'account_key','openai:invalid@example.invalid','sample_count',0))).*
		 FROM account_inventory_daily_account_rollups AS source LIMIT 1`,
		`INSERT INTO account_inventory_daily_provider_rollups SELECT
			(jsonb_populate_record(NULL::account_inventory_daily_provider_rollups,
			 to_jsonb(source)||jsonb_build_object('provider_rollup_id',gen_random_uuid(),
			 'provider','anthropic','expected_poll_count',289,'coverage_denominator',289))).*
		 FROM account_inventory_daily_provider_rollups AS source LIMIT 1`,
		`INSERT INTO account_inventory_daily_summaries SELECT
			(jsonb_populate_record(NULL::account_inventory_daily_summaries,
			 to_jsonb(source)||jsonb_build_object('summary_id',gen_random_uuid(),
			 'provider','openai_x','account_key','openaiZx:underscore@example.invalid'))).*
		 FROM account_inventory_daily_summaries AS source LIMIT 1`,
		`INSERT INTO account_inventory_daily_account_rollups SELECT
			(jsonb_populate_record(NULL::account_inventory_daily_account_rollups,
			 to_jsonb(source)||jsonb_build_object('account_rollup_id',gen_random_uuid(),
			 'provider','openai_x','account_key','openaiZx:underscore@example.invalid'))).*
		 FROM account_inventory_daily_account_rollups AS source LIMIT 1`,
	}
	for index, statement := range invalidInserts {
		if _, err := database.owner.Exec(ctx, statement); err == nil {
			t.Fatalf("aggregate invalid combination %d was accepted", index)
		} else {
			requireHistorySQLState(t, err, "23514")
		}
	}

	assertOwnerHistoryMutationsRejected(t, ctx, database, recentPollID, oldPollID,
		compactionID, rollupID, accountSummaryID, providerSummaryID, accountRollupID, providerRollupID)
	installHistoryWrongGateProbe(t, ctx, database)
	gateTargets := []struct {
		name, gate, value, wantState string
		pollID, rowID                uuid.UUID
	}{
		{"snapshot", "history_snapshot_delete", gateCompactionID.String(), "42501", recentPollID, uuid.Nil},
		{"provider", "history_poll_retention_delete", oldPollID.String(), "23514", oldPollID, uuid.Nil},
		{"duplicate", "history_poll_retention_delete", oldPollID.String(), "42501", oldPollID, uuid.Nil},
		{"poll", "history_poll_retention_delete", oldPollID.String(), "23514", oldPollID, uuid.Nil},
		{"account_summary", "history_rollup_row_retention_delete", "account_inventory_daily_summaries:" + accountSummaryID.String(), "42501", uuid.Nil, accountSummaryID},
		{"provider_summary", "history_rollup_row_retention_delete", "account_inventory_daily_provider_summaries:" + providerSummaryID.String(), "42501", uuid.Nil, providerSummaryID},
		{"account_rollup", "history_rollup_row_retention_delete", "account_inventory_daily_account_rollups:" + accountRollupID.String(), "42501", uuid.Nil, accountRollupID},
		{"provider_rollup", "history_rollup_row_retention_delete", "account_inventory_daily_provider_rollups:" + providerRollupID.String(), "42501", uuid.Nil, providerRollupID},
		{"compaction", "history_compaction_run_retention_delete", compactionID.String(), "42501", uuid.Nil, compactionID},
		{"rollup", "history_rollup_run_retention_delete", rollupID.String(), "42501", uuid.Nil, rollupID},
		{"retired", "history_retired_day_write", targetDate.Format("2006-01-02") + ":" + fixture.instanceID.String(), "42501", uuid.Nil, uuid.Nil},
	}
	tx, err := database.runtime.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var affected int
	err = tx.QueryRow(ctx, `SELECT public.test_history_wrong_gate(
		$1,$2,$3,$4,$5,$6,$7)`, "snapshot", "history_snapshot_delete", gateCompactionID.String(),
		recentPollID, uuid.Nil, targetDate, fixture.instanceID).Scan(&affected)
	if err != nil || affected != 1 {
		_ = tx.Rollback(ctx)
		t.Fatalf("same-day deleting compaction gate unreachable rows=%d err=%v", affected, err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	for _, target := range gateTargets {
		for _, probe := range []struct{ gate, value string }{
			{target.gate, uuid.New().String()},
			{"history_run_write", target.value},
		} {
			_, err := database.runtime.Exec(ctx, `SELECT public.test_history_wrong_gate(
				$1,$2,$3,$4,$5,$6,$7)`, target.name, probe.gate, probe.value,
				target.pollID, target.rowID, targetDate, fixture.instanceID)
			if err == nil {
				t.Fatalf("%s accepted wrong gate=%s value=%s", target.name, probe.gate, probe.value)
			}
			requireHistorySQLState(t, err, target.wantState)
		}
	}

	for _, function := range []string{
		"control_delete_account_inventory_poll_retention_v1",
		"control_delete_account_inventory_rollup_row_retention_v1",
		"control_delete_account_inventory_rollup_run_retention_v1",
		"control_delete_account_inventory_compaction_run_retention_v1",
	} {
		var processed int
		if err := database.runtime.QueryRow(ctx, `SELECT (public.`+function+`(100)
			->>'processed_count')::integer`).Scan(&processed); err != nil || processed < 1 {
			t.Fatalf("legal retention %s processed=%d err=%v", function, processed, err)
		}
	}
	var retainedExact bool
	if err := database.owner.QueryRow(ctx, `SELECT
		NOT EXISTS(SELECT 1 FROM account_inventory_poll_runs WHERE poll_run_id=$1)
		AND EXISTS(SELECT 1 FROM account_inventory_poll_runs WHERE poll_run_id=$2)
		AND NOT EXISTS(SELECT 1 FROM account_inventory_compaction_runs WHERE compaction_run_id=$3)
		AND NOT EXISTS(SELECT 1 FROM account_inventory_daily_rollup_runs WHERE rollup_run_id=$4)
		AND NOT EXISTS(SELECT 1 FROM account_inventory_daily_summaries WHERE summary_id=$5)
		AND NOT EXISTS(SELECT 1 FROM account_inventory_daily_provider_summaries WHERE provider_summary_id=$6)
		AND NOT EXISTS(SELECT 1 FROM account_inventory_daily_account_rollups WHERE account_rollup_id=$7)
		AND NOT EXISTS(SELECT 1 FROM account_inventory_daily_provider_rollups WHERE provider_rollup_id=$8)
		AND EXISTS(SELECT 1 FROM account_inventory_history_retired_days
			WHERE summary_date=$9 AND instance_id=$10)`, oldPollID, recentPollID, compactionID,
		rollupID, accountSummaryID, providerSummaryID, accountRollupID, providerRollupID,
		targetDate, fixture.instanceID).Scan(&retainedExact); err != nil || !retainedExact {
		t.Fatalf("legal retention final state exact=%t err=%v", retainedExact, err)
	}
	for _, statement := range []string{
		`UPDATE account_inventory_history_retired_days SET retired_at=retired_at`,
		`DELETE FROM account_inventory_history_retired_days`,
		`TRUNCATE account_inventory_history_retired_days`,
	} {
		if _, err := database.owner.Exec(ctx, statement); err == nil {
			t.Fatalf("owner mutated retired marker: %s", statement)
		} else {
			requireHistorySQLState(t, err, "42501")
		}
	}
}

var historyAggregateColumns = map[string][]string{
	"account_inventory_daily_summaries": {
		"summary_id", "compaction_run_id", "summary_date", "instance_id", "provider", "account_key",
		"provider_policy_version", "first_scheduled_at", "last_scheduled_at", "first_observed_at",
		"last_observed_at", "last_basic_status", "sample_count", "disabled_count", "unavailable_count",
		"error_count", "active_count", "unknown_count", "first_success_count", "last_success_count",
		"success_reset_count", "first_failed_count", "last_failed_count", "failed_reset_count", "created_at",
	},
	"account_inventory_daily_provider_summaries": {
		"provider_summary_id", "compaction_run_id", "summary_date", "instance_id", "provider",
		"provider_policy_version", "expected_poll_count", "transport_success_count", "contract_valid_count",
		"snapshot_complete_count", "promotion_applied_count", "promotion_skipped_count", "policy_changed_count",
		"abandoned_count", "degraded_count", "first_promotion_at", "last_promotion_at", "coverage_numerator",
		"coverage_denominator", "coverage_ratio", "coverage_threshold_basis_points", "coverage_status", "created_at",
	},
	"account_inventory_daily_account_rollups": {
		"account_rollup_id", "rollup_run_id", "summary_date", "instance_id", "provider", "account_key",
		"first_scheduled_at", "last_scheduled_at", "first_observed_at", "last_observed_at", "last_basic_status",
		"sample_count", "disabled_count", "unavailable_count", "error_count", "active_count", "unknown_count",
		"first_success_count", "last_success_count", "success_reset_count", "first_failed_count",
		"last_failed_count", "failed_reset_count", "created_at",
	},
	"account_inventory_daily_provider_rollups": {
		"provider_rollup_id", "rollup_run_id", "summary_date", "instance_id", "provider", "expected_poll_count",
		"transport_success_count", "contract_valid_count", "snapshot_complete_count", "promotion_applied_count",
		"promotion_skipped_count", "policy_changed_count", "abandoned_count", "degraded_count",
		"first_promotion_at", "last_promotion_at", "coverage_numerator", "coverage_denominator", "coverage_ratio",
		"coverage_threshold_basis_points", "coverage_status", "created_at",
	},
}

func assertHistoryAggregateCatalog(
	t *testing.T, ctx context.Context, database *isolatedJobDatabase,
) {
	t.Helper()
	uniqueKeys := map[string][]string{
		"account_inventory_daily_summaries":          {"summary_date", "instance_id", "account_key", "provider_policy_version"},
		"account_inventory_daily_provider_summaries": {"summary_date", "instance_id", "provider", "provider_policy_version"},
		"account_inventory_daily_account_rollups":    {"summary_date", "instance_id", "account_key"},
		"account_inventory_daily_provider_rollups":   {"summary_date", "instance_id", "provider"},
	}
	indexFragments := map[string]string{
		"account_inventory_daily_summaries_rollup_idx":                "(summary_date, instance_id, account_key, last_scheduled_at desc, provider_policy_version desc)",
		"account_inventory_daily_provider_rollups_completed_read_idx": "(instance_id, provider, summary_date desc, provider_rollup_id)",
	}
	extraIndexCounts := map[string]int{
		"account_inventory_daily_summaries":          1,
		"account_inventory_daily_provider_summaries": 0,
		"account_inventory_daily_account_rollups":    0,
		"account_inventory_daily_provider_rollups":   1,
	}
	for table, wantColumns := range historyAggregateColumns {
		var columns []string
		if err := database.owner.QueryRow(ctx, `SELECT array_agg(attribute.attname::text ORDER BY attribute.attnum)
			FROM pg_attribute AS attribute WHERE attribute.attrelid=to_regclass('public.' || $1)
			AND attribute.attnum>0 AND NOT attribute.attisdropped`, table).Scan(&columns); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(columns, wantColumns) {
			t.Fatalf("%s columns=%v want=%v", table, columns, wantColumns)
		}
		var primaryKey bool
		if err := database.owner.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM pg_constraint AS constraint_row
			WHERE constraint_row.conrelid=to_regclass('public.' || $1)
			AND constraint_row.contype='p' AND (
				SELECT array_agg(attribute.attname::text ORDER BY key.ordinality)
				FROM unnest(constraint_row.conkey) WITH ORDINALITY AS key(attnum,ordinality)
				JOIN pg_attribute AS attribute ON attribute.attrelid=constraint_row.conrelid
				AND attribute.attnum=key.attnum)=ARRAY[$2]::text[])`,
			table, wantColumns[0]).Scan(&primaryKey); err != nil || !primaryKey {
			t.Fatalf("%s primary key exact=%t err=%v", table, primaryKey, err)
		}
		var unique bool
		if err := database.owner.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM pg_constraint AS constraint_row
			WHERE constraint_row.conrelid=to_regclass('public.' || $1)
			AND constraint_row.contype='u' AND (
				SELECT array_agg(attribute.attname::text ORDER BY key.ordinality)
				FROM unnest(constraint_row.conkey) WITH ORDINALITY AS key(attnum,ordinality)
				JOIN pg_attribute AS attribute ON attribute.attrelid=constraint_row.conrelid
				AND attribute.attnum=key.attnum)=$2::text[])`, table, uniqueKeys[table]).Scan(&unique); err != nil || !unique {
			t.Fatalf("%s unique key exact=%t err=%v", table, unique, err)
		}
		var triggerCount int
		if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM pg_trigger
			WHERE tgrelid=to_regclass('public.' || $1) AND NOT tgisinternal AND tgenabled='O'
			AND tgname=ANY(ARRAY[$1 || '_immutable',$1 || '_truncate_immutable'])`, table).Scan(&triggerCount); err != nil || triggerCount != 2 {
			t.Fatalf("%s immutable triggers=%d err=%v", table, triggerCount, err)
		}
		var extraIndexCount int
		if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM pg_index
			WHERE indrelid=to_regclass('public.' || $1) AND NOT indisprimary AND NOT indisunique`,
			table).Scan(&extraIndexCount); err != nil || extraIndexCount != extraIndexCounts[table] {
			t.Fatalf("%s extra indexes=%d want=%d err=%v",
				table, extraIndexCount, extraIndexCounts[table], err)
		}
	}
	for index, fragment := range indexFragments {
		var definition string
		if err := database.owner.QueryRow(ctx, `SELECT pg_get_indexdef(to_regclass('public.' || $1))`, index).Scan(&definition); err != nil {
			t.Fatal(err)
		}
		normalized := strings.ToLower(strings.Join(strings.Fields(definition), " "))
		if !strings.Contains(normalized, fragment) {
			t.Fatalf("index %s definition=%s", index, normalized)
		}
	}
	for _, trigger := range []struct {
		name, table, function string
		typeBits              int
	}{
		{"account_inventory_poll_runs_guard", "account_inventory_poll_runs", "control_protect_account_inventory_poll_run", 27},
		{"account_inventory_poll_runs_truncate_guard", "account_inventory_poll_runs", "control_protect_account_inventory_poll_run", 34},
		{"account_inventory_poll_provider_results_truncate_guard", "account_inventory_poll_provider_results", "control_protect_account_inventory_provider_result", 34},
	} {
		var count int
		if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM pg_trigger AS trigger
			JOIN pg_proc AS function ON function.oid=trigger.tgfoid
			WHERE trigger.tgrelid=to_regclass('public.' || $1)
			AND trigger.tgname=$2 AND function.proname=$3
			AND trigger.tgtype=$4 AND trigger.tgenabled='O' AND NOT trigger.tgisinternal`,
			trigger.table, trigger.name, trigger.function, trigger.typeBits).Scan(&count); err != nil || count != 1 {
			t.Fatalf("poll protection trigger %s exact=%d err=%v", trigger.name, count, err)
		}
	}
	var metricsDefinition string
	if err := database.owner.QueryRow(ctx, `SELECT lower(pg_get_functiondef(
		'public.control_list_account_inventory_history_metrics_v1()'::regprocedure))`).Scan(&metricsDefinition); err != nil ||
		!strings.Contains(strings.Join(strings.Fields(metricsDefinition), " "), "where run.status = 'completed'") {
		t.Fatalf("completed-only metrics catalog drift: %v", err)
	}
}

func assertOwnerHistoryMutationsRejected(
	t *testing.T, ctx context.Context, database *isolatedJobDatabase,
	recentPollID, oldPollID, compactionID, rollupID,
	accountSummaryID, providerSummaryID, accountRollupID, providerRollupID uuid.UUID,
) {
	t.Helper()
	statements := []struct {
		statement, state, message string
	}{
		{fmt.Sprintf("UPDATE account_inventory_snapshot_items SET observed_at=observed_at WHERE poll_run_id='%s'", recentPollID), "42501", ""},
		{fmt.Sprintf("DELETE FROM account_inventory_snapshot_items WHERE poll_run_id='%s'", recentPollID), "42501", ""},
		{"TRUNCATE account_inventory_snapshot_items", "42501", ""},
		{fmt.Sprintf("UPDATE account_inventory_poll_provider_results SET degraded=degraded WHERE poll_run_id='%s'", oldPollID), "23514", "account inventory provider evidence is immutable"},
		{fmt.Sprintf("DELETE FROM account_inventory_poll_provider_results WHERE poll_run_id='%s'", oldPollID), "23514", "account inventory provider evidence is immutable"},
		{"TRUNCATE account_inventory_poll_provider_results CASCADE", "23514", "account inventory provider evidence is immutable"},
		{fmt.Sprintf("UPDATE account_inventory_poll_duplicates SET occurrence_count=occurrence_count WHERE poll_run_id='%s'", oldPollID), "42501", ""},
		{fmt.Sprintf("DELETE FROM account_inventory_poll_duplicates WHERE poll_run_id='%s'", oldPollID), "42501", ""},
		{"TRUNCATE account_inventory_poll_duplicates", "42501", ""},
		{fmt.Sprintf("UPDATE account_inventory_poll_runs SET status=status WHERE poll_run_id='%s'", oldPollID), "23514", "terminal account inventory poll evidence is immutable"},
		{fmt.Sprintf("DELETE FROM account_inventory_poll_runs WHERE poll_run_id='%s'", oldPollID), "23514", "terminal account inventory poll evidence is immutable"},
		{"TRUNCATE account_inventory_poll_runs CASCADE", "23514", "terminal account inventory poll evidence is immutable"},
	}
	for _, item := range []struct {
		table, key string
		id         uuid.UUID
	}{
		{"account_inventory_daily_summaries", "summary_id", accountSummaryID},
		{"account_inventory_daily_provider_summaries", "provider_summary_id", providerSummaryID},
		{"account_inventory_daily_account_rollups", "account_rollup_id", accountRollupID},
		{"account_inventory_daily_provider_rollups", "provider_rollup_id", providerRollupID},
		{"account_inventory_compaction_runs", "compaction_run_id", compactionID},
		{"account_inventory_daily_rollup_runs", "rollup_run_id", rollupID},
	} {
		truncateStatement := fmt.Sprintf("TRUNCATE %s", item.table)
		truncateMessage := ""
		if item.table == "account_inventory_compaction_runs" ||
			item.table == "account_inventory_daily_rollup_runs" {
			truncateStatement += " CASCADE"
			truncateMessage = "account inventory history run cannot be deleted directly"
		}
		statements = append(statements,
			struct{ statement, state, message string }{fmt.Sprintf("UPDATE %s SET created_at=created_at WHERE %s='%s'", item.table, item.key, item.id), "42501", ""},
			struct{ statement, state, message string }{fmt.Sprintf("DELETE FROM %s WHERE %s='%s'", item.table, item.key, item.id), "42501", ""},
			struct{ statement, state, message string }{truncateStatement, "42501", truncateMessage},
		)
	}
	for _, probe := range statements {
		if _, err := database.owner.Exec(ctx, probe.statement); err == nil {
			t.Fatalf("owner ordinary mutation succeeded: %s", probe.statement)
		} else {
			requireHistorySQLState(t, err, probe.state)
			if probe.message != "" && !strings.Contains(err.Error(), probe.message) {
				t.Fatalf("owner mutation error=%v want trigger message %q", err, probe.message)
			}
		}
	}
}

func installHistoryWrongGateProbe(
	t *testing.T, ctx context.Context, database *isolatedJobDatabase,
) {
	t.Helper()
	if _, err := database.owner.Exec(ctx, `CREATE FUNCTION public.test_history_wrong_gate(
		p_target text,p_gate text,p_value text,p_poll_id uuid,p_row_id uuid,
		p_summary_date date,p_instance_id uuid
	) RETURNS integer LANGUAGE plpgsql SECURITY DEFINER
	SET search_path=pg_catalog SET TimeZone='UTC' AS $function$
	DECLARE affected integer;
	BEGIN
		PERFORM set_config('relay_control.' || p_gate,p_value,true);
		CASE p_target
		WHEN 'snapshot' THEN DELETE FROM public.account_inventory_snapshot_items WHERE poll_run_id=p_poll_id;
		WHEN 'provider' THEN DELETE FROM public.account_inventory_poll_provider_results WHERE poll_run_id=p_poll_id;
		WHEN 'duplicate' THEN DELETE FROM public.account_inventory_poll_duplicates WHERE poll_run_id=p_poll_id;
		WHEN 'poll' THEN DELETE FROM public.account_inventory_poll_runs WHERE poll_run_id=p_poll_id;
		WHEN 'account_summary' THEN DELETE FROM public.account_inventory_daily_summaries WHERE summary_id=p_row_id;
		WHEN 'provider_summary' THEN DELETE FROM public.account_inventory_daily_provider_summaries WHERE provider_summary_id=p_row_id;
		WHEN 'account_rollup' THEN DELETE FROM public.account_inventory_daily_account_rollups WHERE account_rollup_id=p_row_id;
		WHEN 'provider_rollup' THEN DELETE FROM public.account_inventory_daily_provider_rollups WHERE provider_rollup_id=p_row_id;
		WHEN 'compaction' THEN DELETE FROM public.account_inventory_compaction_runs WHERE compaction_run_id=p_row_id;
		WHEN 'rollup' THEN DELETE FROM public.account_inventory_daily_rollup_runs WHERE rollup_run_id=p_row_id;
		WHEN 'retired' THEN INSERT INTO public.account_inventory_history_retired_days(summary_date,instance_id,retired_at)
			VALUES(p_summary_date,p_instance_id,clock_timestamp());
		ELSE RAISE EXCEPTION 'unknown test target';
		END CASE;
		GET DIAGNOSTICS affected = ROW_COUNT;
		RETURN affected;
	END
	$function$;
	REVOKE ALL ON FUNCTION public.test_history_wrong_gate(text,text,text,uuid,uuid,date,uuid) FROM PUBLIC;
	GRANT EXECUTE ON FUNCTION public.test_history_wrong_gate(text,text,text,uuid,uuid,date,uuid)
		TO relay_control_runtime`); err != nil {
		t.Fatal(err)
	}
}
