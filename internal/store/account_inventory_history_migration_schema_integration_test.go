package store_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	history "github.com/sunxu/relay-station-control/internal/history"
)

const historyCompatibilityFunction = "public.control_history_schema_compatibility_v1()"

var historyTables = []string{
	"account_inventory_compaction_runs",
	"account_inventory_daily_rollup_runs",
	"account_inventory_history_retired_days",
	"account_inventory_daily_summaries",
	"account_inventory_daily_provider_summaries",
	"account_inventory_daily_account_rollups",
	"account_inventory_daily_provider_rollups",
}

func TestAccountInventoryHistoryMigrationEmptyDownUpAndChecksumGolden(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	requireHistoryMigrationVersion(t, ctx, database, 9)

	var emptyDigest, singleDigest, multipleDigest string
	if err := database.owner.QueryRow(ctx, `SELECT
		encode(public.control_history_checksum_chain_v1(ARRAY[]::bytea[]),'hex'),
		encode(public.control_history_checksum_chain_v1(ARRAY[
			public.control_history_canonical_row_v1(
				convert_to('instance-a','UTF8'),convert_to('7','UTF8'),convert_to('t','UTF8')
			)
		]),'hex'),
		encode(public.control_history_checksum_chain_v1(ARRAY[
			public.control_history_canonical_row_v1(
				convert_to('instance-a','UTF8'),convert_to('7','UTF8'),convert_to('t','UTF8')
			),
			public.control_history_canonical_row_v1(
				convert_to('instance-b','UTF8'),NULL::bytea,convert_to('f','UTF8')
			)
		]),'hex')`).Scan(&emptyDigest, &singleDigest, &multipleDigest); err != nil {
		t.Fatal(err)
	}
	if emptyDigest != string(make([]byte, 64)) {
		// A zero-valued string is not the hexadecimal representation of zero32.
		if emptyDigest != "0000000000000000000000000000000000000000000000000000000000000000" {
			t.Fatalf("empty checksum golden = %s", emptyDigest)
		}
	}
	if singleDigest != "136d4d2c579f30c7ac22bcab7b50a6dbe8c0100f551d39f219b8ad2cb3f9e967" {
		t.Fatalf("single checksum golden = %s", singleDigest)
	}
	if multipleDigest != "1b049d7b8bdcfce3c849b9bbcd34a4c77503e326ec4c1aa61215e5f0d77e86aa" {
		t.Fatalf("multiple checksum golden = %s", multipleDigest)
	}
	var finalSegmentDigest string
	if err := database.owner.QueryRow(ctx, `SELECT encode(
		public.control_history_checksum_chain_v1(ARRAY[
			public.control_history_canonical_row_v1(VARIADIC ARRAY[
				convert_to('account_segment','UTF8'),convert_to('2026-08-20','UTF8'),
				convert_to('10000000-0000-0000-0000-000000000001','UTF8'),
				convert_to('openai','UTF8'),convert_to('openai:account-a','UTF8'),
				convert_to('00000000-0000-0000-0000-000000000001','UTF8'),
				convert_to('2026-08-20T00:00:00Z','UTF8'),
				convert_to('2026-08-20T00:05:00Z','UTF8'),
				convert_to('2026-08-20T00:00:01Z','UTF8'),
				convert_to('2026-08-20T00:05:01Z','UTF8'),convert_to('active','UTF8'),
				convert_to('2','UTF8'),convert_to('0','UTF8'),convert_to('0','UTF8'),
				convert_to('0','UTF8'),convert_to('2','UTF8'),convert_to('0','UTF8'),
				convert_to('10','UTF8'),convert_to('9','UTF8'),convert_to('1','UTF8'),
				convert_to('0','UTF8'),convert_to('0','UTF8'),convert_to('0','UTF8')
			]::bytea[]),
			public.control_history_canonical_row_v1(VARIADIC ARRAY[
				convert_to('account_segment','UTF8'),convert_to('2026-08-20','UTF8'),
				convert_to('10000000-0000-0000-0000-000000000001','UTF8'),
				convert_to('openai','UTF8'),convert_to('openai:account-a','UTF8'),
				convert_to('00000000-0000-0000-0000-000000000002','UTF8'),
				convert_to('2026-08-20T00:10:00Z','UTF8'),
				convert_to('2026-08-20T00:15:00Z','UTF8'),
				convert_to('2026-08-20T00:10:01Z','UTF8'),
				convert_to('2026-08-20T00:15:01Z','UTF8'),convert_to('error','UTF8'),
				convert_to('2','UTF8'),convert_to('0','UTF8'),convert_to('0','UTF8'),
				convert_to('2','UTF8'),convert_to('0','UTF8'),convert_to('0','UTF8'),
				convert_to('5','UTF8'),convert_to('4','UTF8'),convert_to('1','UTF8'),
				convert_to('1','UTF8'),convert_to('1','UTF8'),convert_to('0','UTF8')
			]::bytea[]),
			public.control_history_canonical_row_v1(VARIADIC ARRAY[
				convert_to('provider_segment','UTF8'),convert_to('2026-08-20','UTF8'),
				convert_to('10000000-0000-0000-0000-000000000001','UTF8'),
				convert_to('openai','UTF8'),
				convert_to('00000000-0000-0000-0000-000000000001','UTF8'),
				convert_to('10','UTF8'),convert_to('9','UTF8'),convert_to('9','UTF8'),
				convert_to('9','UTF8'),convert_to('9','UTF8'),convert_to('1','UTF8'),
				convert_to('0','UTF8'),convert_to('0','UTF8'),convert_to('0','UTF8'),
				convert_to('2026-08-20T00:00:00Z','UTF8'),
				convert_to('2026-08-20T00:00:00Z','UTF8'),convert_to('9','UTF8'),
				convert_to('10','UTF8'),convert_to('9500','UTF8'),convert_to('partial','UTF8')
			]::bytea[]),
			public.control_history_canonical_row_v1(VARIADIC ARRAY[
				convert_to('provider_segment','UTF8'),convert_to('2026-08-20','UTF8'),
				convert_to('10000000-0000-0000-0000-000000000001','UTF8'),
				convert_to('openai','UTF8'),
				convert_to('00000000-0000-0000-0000-000000000002','UTF8'),
				convert_to('10','UTF8'),convert_to('10','UTF8'),convert_to('10','UTF8'),
				convert_to('10','UTF8'),convert_to('10','UTF8'),convert_to('0','UTF8'),
				convert_to('0','UTF8'),convert_to('0','UTF8'),convert_to('0','UTF8'),
				convert_to('2026-08-20T00:10:00Z','UTF8'),
				convert_to('2026-08-20T00:10:00Z','UTF8'),convert_to('10','UTF8'),
				convert_to('10','UTF8'),convert_to('9500','UTF8'),convert_to('complete','UTF8')
			]::bytea[])
		]),'hex')`).Scan(&finalSegmentDigest); err != nil {
		t.Fatal(err)
	}
	if finalSegmentDigest != "fe4d0d137bc6db7df3d667f5e7e226780cec7441add4503a197dd11fb97abea0" {
		t.Fatalf("PostgreSQL/Go final segment checksum golden = %s", finalSegmentDigest)
	}
	if _, err := hex.DecodeString(multipleDigest); err != nil {
		t.Fatal("checksum golden is not hexadecimal")
	}
	var wholeSecond, trimmedFraction string
	if err := database.owner.QueryRow(ctx, `SELECT
		public.control_history_time_text_v1(timestamptz '2026-01-02 03:04:05+08'),
		public.control_history_time_text_v1(timestamptz '2026-01-01 19:04:05.123400+00')`).
		Scan(&wholeSecond, &trimmedFraction); err != nil {
		t.Fatal(err)
	}
	if wholeSecond != "2026-01-01T19:04:05Z" || trimmedFraction != "2026-01-01T19:04:05.1234Z" {
		t.Fatalf("history time canonicalization whole=%q fractional=%q", wholeSecond, trimmedFraction)
	}

	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "down"); err != nil {
		t.Fatalf("empty Migration 9 down: %v", err)
	}
	requireHistoryMigrationVersion(t, ctx, database, 8)
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "up-by-one"); err != nil {
		t.Fatalf("Migration 9 up after empty down: %v", err)
	}
	requireHistoryMigrationVersion(t, ctx, database, 9)
}

func TestAccountInventoryHistoryMigrationBackfillsHealthWithoutHistoryOrIdentityCopy(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "down"); err != nil {
		t.Fatal("prepare Migration 8 database")
	}
	requireHistoryMigrationVersion(t, ctx, database, 8)

	fixture := newLifecycleSchemaFixture(t, ctx, database)
	if _, err := database.owner.Exec(ctx, `INSERT INTO driver_capabilities(
		node_type,driver_contract_version,capability
	) VALUES($1,$2,'management_account_inventory_read')`, fixture.nodeType, fixture.contract); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO node_capabilities(
		instance_id,node_type,driver_contract_version,capability
	) VALUES($1,$2,$3,'management_account_inventory_read')`,
		fixture.instanceID, fixture.nodeType, fixture.contract); err != nil {
		t.Fatal(err)
	}
	pollID := fixture.finalize(t, ctx, database, []lifecycleAccount{{
		email: "history-health-backfill@example.invalid", successCount: 17,
	}})

	var beforeState string
	if err := database.owner.QueryRow(ctx, `SELECT (to_jsonb(state.*)-ARRAY[
		'health_scheduled_at','health_degraded','health_reason'
	])::text FROM account_inventory_provider_states AS state
	WHERE instance_id=$1 AND provider='openai'`, fixture.instanceID).Scan(&beforeState); err != nil {
		t.Fatal(err)
	}
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "up-by-one"); err != nil {
		t.Fatalf("apply Migration 9 over current state: %v", err)
	}
	requireHistoryMigrationVersion(t, ctx, database, 9)

	var afterState string
	var healthMatches bool
	if err := database.owner.QueryRow(ctx, `SELECT
		(to_jsonb(state.*)-ARRAY['health_scheduled_at','health_degraded','health_reason'])::text,
		state.health_scheduled_at=run.scheduled_at
		AND state.health_degraded=result.degraded
		AND state.health_reason='none'
	FROM account_inventory_provider_states AS state
	JOIN account_inventory_poll_runs AS run ON run.poll_run_id=state.current_poll_run_id
	JOIN account_inventory_poll_provider_results AS result
	  ON result.poll_run_id=run.poll_run_id AND result.provider=state.provider
	WHERE state.instance_id=$1 AND state.provider='openai'`, fixture.instanceID).
		Scan(&afterState, &healthMatches); err != nil {
		t.Fatal(err)
	}
	if afterState != beforeState || !healthMatches {
		t.Fatal("Migration 9 changed current state beyond the exact health backfill")
	}

	var historyRows, copiedEmailColumns int
	if err := database.owner.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM account_inventory_compaction_runs)
		+(SELECT count(*) FROM account_inventory_daily_rollup_runs)
		+(SELECT count(*) FROM account_inventory_history_retired_days)
		+(SELECT count(*) FROM account_inventory_daily_summaries)
		+(SELECT count(*) FROM account_inventory_daily_provider_summaries)
		+(SELECT count(*) FROM account_inventory_daily_account_rollups)
		+(SELECT count(*) FROM account_inventory_daily_provider_rollups),
		(SELECT count(*) FROM information_schema.columns
		 WHERE table_schema='public'
		   AND table_name=ANY($1::text[]) AND column_name='normalized_email')`, historyTables).
		Scan(&historyRows, &copiedEmailColumns); err != nil {
		t.Fatal(err)
	}
	if historyRows != 0 || copiedEmailColumns != 0 {
		t.Fatalf("migration generated history or copied email columns: rows=%d columns=%d",
			historyRows, copiedEmailColumns)
	}

	// Model the ON DELETE SET NULL result without deleting immutable poll
	// evidence.  Both current tables accept only this exact no-other-field shape.
	if _, err := database.owner.Exec(ctx, `UPDATE account_inventory_provider_states
		SET current_poll_run_id=NULL WHERE instance_id=$1 AND provider='openai'`, fixture.instanceID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `UPDATE account_inventory
		SET current_poll_run_id=NULL WHERE instance_id=$1`, fixture.instanceID); err != nil {
		t.Fatal(err)
	}
	var queryRows int
	var degraded bool
	if err := database.runtime.QueryRow(ctx, `SELECT count(*),bool_or(provider_degraded)
		FROM public.control_query_current_account_inventory_v1($1,'','','','','',10)`,
		fixture.instanceID).Scan(&queryRows, &degraded); err != nil {
		t.Fatalf("query after legal current source cleanup: %v", err)
	}
	if queryRows != 1 || degraded {
		t.Fatalf("query changed after current source cleanup: rows=%d degraded=%t poll=%s",
			queryRows, degraded, pollID)
	}

	// A newer finalized but non-promoted Provider observation refreshes health
	// without recreating or moving the retained current snapshot pointer.
	failedPollID, fence := uuid.New(), uuid.New()
	scheduledAt := fixture.baseSlot.Add(time.Duration(fixture.nextPoll) * 5 * time.Minute)
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_poll_runs(
		poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,
		provider_policy_version,max_attempts,poll_start_grace_seconds,created_at
	) VALUES($1,$2,$3,$4,$5,$6,2,299,clock_timestamp())`, failedPollID,
		fixture.instanceID, fixture.nodeType, fixture.contract, scheduledAt, fixture.policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `UPDATE account_inventory_poll_runs
		SET status='running',attempt_count=1,first_started_at=clock_timestamp(),
		last_started_at=clock_timestamp(),lease_expires_at=clock_timestamp()+interval '60 seconds',
		lease_fencing_token=$2 WHERE poll_run_id=$1`, failedPollID, fence); err != nil {
		t.Fatal(err)
	}
	var finalized int
	if err := database.runtime.QueryRow(ctx, `SELECT count(*)
		FROM public.control_finalize_account_inventory_poll_run_with_lifecycle(
			$1,$2,true,true,false,NULL,true,false,true,'failed','contract_invalid',
			0,0,0,0,0,'v1.0.0','abcdef1',
			'[{
			  "provider":"openai","identifiable_count":0,"missing_identity_count":0,
			  "duplicate_identity_count":0,"identity_complete":true,
			  "snapshot_complete":false,"degraded":true,"reason":"contract_invalid"
			}]'::jsonb,'[]'::jsonb,'[]'::jsonb
		)`, failedPollID, fence).Scan(&finalized); err != nil || finalized != 1 {
		t.Fatalf("finalize degraded health observation rows=%d err=%v", finalized, err)
	}
	var currentPointer *uuid.UUID
	var healthScheduled time.Time
	var healthReason string
	if err := database.owner.QueryRow(ctx, `SELECT current_poll_run_id,
		health_scheduled_at,health_reason FROM account_inventory_provider_states
		WHERE instance_id=$1 AND provider='openai'`, fixture.instanceID).
		Scan(&currentPointer, &healthScheduled, &healthReason); err != nil {
		t.Fatal(err)
	}
	if currentPointer != nil || !healthScheduled.Equal(scheduledAt) || healthReason != "contract_invalid" {
		t.Fatalf("degraded health moved pointer or lost reason: pointer=%v time=%s reason=%s",
			currentPointer, healthScheduled, healthReason)
	}
	if err := database.runtime.QueryRow(ctx, `SELECT bool_and(provider_degraded)
		FROM public.control_query_current_account_inventory_v1($1,'','','','','',10)`,
		fixture.instanceID).Scan(&degraded); err != nil || !degraded {
		t.Fatalf("query did not consume newer Provider health: degraded=%t err=%v", degraded, err)
	}
}

func TestAccountInventoryHistoryMigrationBackfillsLegacyPollThenRetiresWithoutResurrection(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "down"); err != nil {
		t.Fatal(err)
	}
	requireHistoryMigrationVersion(t, ctx, database, 8)
	fixture := newLifecycleSchemaFixture(t, ctx, database)
	targetDate := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -31)
	slot := targetDate.Add(12 * time.Hour)
	if _, err := database.owner.Exec(ctx, `UPDATE provider_inventory_policy_activations
		SET effective_from=$1,created_at=$1 WHERE policy_version_id=$2`,
		targetDate, fixture.policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_inventory_monitoring_activations(
		instance_id,effective_from,reason,actor,created_at
	) VALUES($1,$2,'reconciliation','legacy-upgrade-test',$2)`,
		fixture.instanceID, targetDate); err != nil {
		t.Fatal(err)
	}
	pollID := uuid.New()
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_poll_runs(
		poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,
		provider_policy_version,status,attempt_count,max_attempts,poll_start_grace_seconds,
		created_at,first_started_at,last_started_at,finalized_at,observed_at,
		transport_success,response_shape_valid,contract_valid,inventory_mode,
		node_identity_complete,snapshot_complete,degraded,result,reason,
		source_record_count,identifiable_record_count,unidentified_record_count,
		unsupported_provider_count,out_of_scope_provider_count,node_version,node_commit
	) VALUES($1,$2,$3,$4,$5,$6,'finalized',1,2,299,
		$5::timestamptz+interval '1 second',$5::timestamptz+interval '2 seconds',
		$5::timestamptz+interval '2 seconds',$5::timestamptz+interval '4 seconds',
		$5::timestamptz+interval '3 seconds',true,true,true,'runtime',true,true,
		false,'success','none',0,0,0,0,0,'unknown','unknown')`, pollID,
		fixture.instanceID, fixture.nodeType, fixture.contract, slot, fixture.policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_poll_provider_results
		DISABLE TRIGGER account_inventory_poll_provider_results_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_poll_provider_results(
		poll_run_id,provider,identifiable_count,missing_identity_count,
		duplicate_identity_count,identity_complete,snapshot_complete,degraded,reason,
		promotion_applied,promotion_skipped_reason
	) VALUES($1,'openai',0,0,0,true,true,false,'complete',true,NULL)`, pollID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_poll_provider_results
		ENABLE TRIGGER account_inventory_poll_provider_results_guard`); err != nil {
		t.Fatal(err)
	}
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "up-by-one"); err != nil {
		t.Fatalf("apply Migration 9 over legacy poll: %v", err)
	}
	requireHistoryMigrationVersion(t, ctx, database, 9)
	var migrationCompactions, migrationRollups, migrationMarkers int
	if err := database.owner.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM account_inventory_compaction_runs),
		(SELECT count(*) FROM account_inventory_daily_rollup_runs),
		(SELECT count(*) FROM account_inventory_history_retired_days)`).Scan(
		&migrationCompactions, &migrationRollups, &migrationMarkers); err != nil {
		t.Fatal(err)
	}
	if migrationCompactions != 0 || migrationRollups != 0 || migrationMarkers != 0 {
		t.Fatalf("Migration 9 generated legacy history: compactions=%d rollups=%d markers=%d",
			migrationCompactions, migrationRollups, migrationMarkers)
	}

	var createdCompactions int
	if err := database.runtime.QueryRow(ctx, `SELECT
		(public.control_plan_account_inventory_history_v1(100)->>'compaction_runs_created')::integer`).
		Scan(&createdCompactions); err != nil || createdCompactions < 1 {
		t.Fatalf("legacy planner compactions=%d err=%v", createdCompactions, err)
	}
	var runID uuid.UUID
	if err := database.owner.QueryRow(ctx, `SELECT compaction_run_id
		FROM account_inventory_compaction_runs
		WHERE summary_date=$1 AND instance_id=$2 AND provider_policy_version=$3`,
		targetDate, fixture.instanceID, fixture.policyID).Scan(&runID); err != nil {
		t.Fatalf("legacy day was not backfilled: %v", err)
	}
	var claimedRunID, fence uuid.UUID
	if err := database.runtime.QueryRow(ctx, `SELECT compaction_run_id,fencing_token
		FROM public.control_claim_account_inventory_compaction_v1($1,30)`, uuid.New()).
		Scan(&claimedRunID, &fence); err != nil || claimedRunID != runID {
		t.Fatalf("legacy claim=%s want=%s err=%v", claimedRunID, runID, err)
	}
	var checksumHex string
	if err := database.runtime.QueryRow(ctx, `SELECT
		public.control_summarize_account_inventory_compaction_v1($1,$2)->>'source_checksum_hex'`,
		runID, fence).Scan(&checksumHex); err != nil || len(checksumHex) != 64 {
		t.Fatalf("legacy summarize checksum=%q err=%v", checksumHex, err)
	}
	var deletedStatus string
	if err := database.runtime.QueryRow(ctx, `SELECT
		public.control_delete_account_inventory_snapshot_batch_v1($1,$2,1)->>'status'`,
		runID, fence).Scan(&deletedStatus); err != nil || deletedStatus != "deleting" {
		t.Fatalf("legacy snapshot delete status=%q err=%v", deletedStatus, err)
	}
	var completedStatus string
	if err := database.runtime.QueryRow(ctx, `SELECT
		public.control_complete_account_inventory_compaction_v1(
			$1,$2,decode($3,'hex'))->>'status'`, runID, fence, checksumHex).
		Scan(&completedStatus); err != nil || completedStatus != "completed" {
		t.Fatalf("legacy complete status=%q err=%v", completedStatus, err)
	}
	var plannedRollups int
	if err := database.runtime.QueryRow(ctx, `SELECT
		(public.control_plan_account_inventory_history_v1(100)->>'rollup_runs_created')::integer`).
		Scan(&plannedRollups); err != nil || plannedRollups != 1 {
		t.Fatalf("legacy rollup plan=%d err=%v", plannedRollups, err)
	}
	var rollupID, rollupFence uuid.UUID
	if err := database.runtime.QueryRow(ctx, `SELECT rollup_run_id,fencing_token
		FROM public.control_claim_account_inventory_daily_rollup_v1($1,30)`, uuid.New()).
		Scan(&rollupID, &rollupFence); err != nil {
		t.Fatal(err)
	}
	if err := database.runtime.QueryRow(ctx, `SELECT
		public.control_finalize_account_inventory_daily_rollup_v1($1,$2)->>'status'`,
		rollupID, rollupFence).Scan(&completedStatus); err != nil || completedStatus != "completed" {
		t.Fatalf("legacy rollup finalize=%q err=%v", completedStatus, err)
	}

	oldCompleted := targetDate.Add(24 * time.Hour)
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_compaction_runs
		DISABLE TRIGGER account_inventory_compaction_runs_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `UPDATE account_inventory_compaction_runs SET
		created_at=$2,summarized_at=$2,deleting_at=$2,completed_at=$2,updated_at=$2
		WHERE compaction_run_id=$1`, runID, oldCompleted); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_compaction_runs
		ENABLE TRIGGER account_inventory_compaction_runs_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_daily_rollup_runs
		DISABLE TRIGGER account_inventory_daily_rollup_runs_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `UPDATE account_inventory_daily_rollup_runs SET
		created_at=$2,completed_at=$2,updated_at=$2 WHERE rollup_run_id=$1`,
		rollupID, oldCompleted); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_daily_rollup_runs
		ENABLE TRIGGER account_inventory_daily_rollup_runs_guard`); err != nil {
		t.Fatal(err)
	}
	secondPolicyID := uuid.New()
	if _, err := database.owner.Exec(ctx, `INSERT INTO provider_inventory_policy_versions(
		policy_version_id,node_type,driver_contract_version,active_providers,
		out_of_scope_providers,created_by,created_at
	) VALUES($1,$2,$3,ARRAY['anthropic'],ARRAY[]::text[],'legacy-upgrade-test',$4)`,
		secondPolicyID, fixture.nodeType, fixture.contract, targetDate); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_compaction_runs(
		summary_date,instance_id,provider_policy_version,status,checksum_version,
		source_snapshot_count,source_poll_count,source_provider_result_count,
		source_duplicate_count,source_checksum,deleted_snapshot_count,created_at,
		summarized_at,deleting_at,completed_at,updated_at
	) VALUES($1,$2,$3,'completed',1,0,0,0,0,decode(repeat('55',32),'hex'),0,
		$4,$4,$4,$4,$4)`, targetDate, fixture.instanceID, secondPolicyID, oldCompleted); err != nil {
		t.Fatal(err)
	}
	retention := func(function string) {
		t.Helper()
		var processed int
		query := `SELECT (public.` + function + `(5000)->>'processed_count')::integer`
		if err := database.runtime.QueryRow(ctx, query).Scan(&processed); err != nil || processed < 1 {
			t.Fatalf("legacy retention %s processed=%d err=%v", function, processed, err)
		}
	}
	for _, function := range []string{
		"control_delete_account_inventory_poll_retention_v1",
		"control_delete_account_inventory_rollup_row_retention_v1",
		"control_delete_account_inventory_rollup_run_retention_v1",
	} {
		retention(function)
	}
	var markerCount, compactionBeforeFinalDelete, rollupBeforeFinalDelete, pollsAtCut int
	if err := database.owner.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM account_inventory_history_retired_days
		 WHERE summary_date=$1 AND instance_id=$2),
		(SELECT count(*) FROM account_inventory_compaction_runs
		 WHERE summary_date=$1 AND instance_id=$2),
		(SELECT count(*) FROM account_inventory_daily_rollup_runs
		 WHERE summary_date=$1 AND instance_id=$2),
		(SELECT count(*) FROM account_inventory_poll_runs
		 WHERE instance_id=$2 AND scheduled_at >= ($1::date::timestamp AT TIME ZONE 'UTC')
		   AND scheduled_at < (($1::date+1)::timestamp AT TIME ZONE 'UTC'))`,
		targetDate, fixture.instanceID).Scan(
		&markerCount, &compactionBeforeFinalDelete, &rollupBeforeFinalDelete, &pollsAtCut); err != nil {
		t.Fatal(err)
	}
	if markerCount != 1 || compactionBeforeFinalDelete != 2 ||
		rollupBeforeFinalDelete != 0 || pollsAtCut != 0 {
		t.Fatalf("retirement cut marker=%d compactions=%d rollups=%d polls=%d",
			markerCount, compactionBeforeFinalDelete, rollupBeforeFinalDelete, pollsAtCut)
	}
	var cutCompactions, cutRollups int
	if err := database.runtime.QueryRow(ctx, `WITH planned AS (
		SELECT public.control_plan_account_inventory_history_v1(100) AS value
	) SELECT (value->>'compaction_runs_created')::integer,
		(value->>'rollup_runs_created')::integer FROM planned`).Scan(
		&cutCompactions, &cutRollups); err != nil {
		t.Fatal(err)
	}
	if cutCompactions != 0 || cutRollups != 0 {
		t.Fatalf("planner crossed retired marker before compaction delete: %d/%d",
			cutCompactions, cutRollups)
	}
	var deletedCompactions int
	if err := database.runtime.QueryRow(ctx, `SELECT
		(public.control_delete_account_inventory_compaction_run_retention_v1(1)
		 ->>'processed_count')::integer`).Scan(&deletedCompactions); err != nil || deletedCompactions != 1 {
		t.Fatalf("partial compaction retention=%d err=%v", deletedCompactions, err)
	}
	var compactionsAfterPartial int
	if err := database.owner.QueryRow(ctx, `SELECT count(*)
		FROM account_inventory_compaction_runs WHERE summary_date=$1 AND instance_id=$2`,
		targetDate, fixture.instanceID).Scan(&compactionsAfterPartial); err != nil || compactionsAfterPartial != 1 {
		t.Fatalf("partial compaction remainder=%d err=%v", compactionsAfterPartial, err)
	}
	if err := database.runtime.QueryRow(ctx, `SELECT
		(public.control_plan_account_inventory_history_v1(100)->>'rollup_runs_created')::integer`).
		Scan(&cutRollups); err != nil || cutRollups != 0 {
		t.Fatalf("planner resurrected rollup during partial compaction deletion=%d err=%v",
			cutRollups, err)
	}
	if err := database.runtime.QueryRow(ctx, `SELECT
		(public.control_delete_account_inventory_compaction_run_retention_v1(1)
		 ->>'processed_count')::integer`).Scan(&deletedCompactions); err != nil || deletedCompactions != 1 {
		t.Fatalf("final compaction retention=%d err=%v", deletedCompactions, err)
	}
	var recreatedCompactions, recreatedRollups, retiredLineage int
	if err := database.runtime.QueryRow(ctx, `WITH planned AS (
		SELECT public.control_plan_account_inventory_history_v1(100) AS value
	) SELECT (value->>'compaction_runs_created')::integer,
		(value->>'rollup_runs_created')::integer FROM planned`).Scan(
		&recreatedCompactions, &recreatedRollups); err != nil {
		t.Fatal(err)
	}
	if err := database.owner.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM account_inventory_compaction_runs
		 WHERE summary_date=$1 AND instance_id=$2)
		+(SELECT count(*) FROM account_inventory_daily_rollup_runs
		  WHERE summary_date=$1 AND instance_id=$2)`, targetDate, fixture.instanceID).
		Scan(&retiredLineage); err != nil {
		t.Fatal(err)
	}
	if recreatedCompactions != 0 || recreatedRollups != 0 || retiredLineage != 0 {
		t.Fatalf("legacy lineage resurrected compactions=%d rollups=%d rows=%d",
			recreatedCompactions, recreatedRollups, retiredLineage)
	}
	// Exercise the poll trigger through a runtime-owned enqueue boundary.  The
	// helper is local to this disposable database and deliberately has only the
	// narrow INSERT privilege shape of the production scheduler.
	if _, err := database.owner.Exec(ctx, `CREATE FUNCTION public.test_runtime_insert_history_poll(
		p_instance_id uuid,p_node_type text,p_contract text,p_scheduled_at timestamptz,p_policy_id uuid
	) RETURNS uuid LANGUAGE sql SECURITY DEFINER
	SET search_path=pg_catalog SET TimeZone='UTC'
	AS $function$
		INSERT INTO public.account_inventory_poll_runs(
			instance_id,node_type,driver_contract_version,scheduled_at,provider_policy_version
		) VALUES(p_instance_id,p_node_type,p_contract,p_scheduled_at,p_policy_id)
		RETURNING poll_run_id
	$function$;
	ALTER FUNCTION public.test_runtime_insert_history_poll(uuid,text,text,timestamptz,uuid)
		OWNER TO relay_control_migrator;
	REVOKE ALL ON FUNCTION public.test_runtime_insert_history_poll(uuid,text,text,timestamptz,uuid)
		FROM PUBLIC;
	GRANT EXECUTE ON FUNCTION public.test_runtime_insert_history_poll(uuid,text,text,timestamptz,uuid)
		TO relay_control_runtime`); err != nil {
		t.Fatal(err)
	}
	nonRetiredSlot := time.Now().UTC().Truncate(5 * time.Minute).Add(-5 * time.Minute)
	var runtimePollID uuid.UUID
	if err := database.runtime.QueryRow(ctx, `SELECT public.test_runtime_insert_history_poll(
		$1,$2,$3,$4,$5)`, fixture.instanceID, fixture.nodeType, fixture.contract,
		nonRetiredSlot, fixture.policyID).Scan(&runtimePollID); err != nil || runtimePollID == uuid.Nil {
		t.Fatalf("runtime normal-day poll insert=%s err=%v", runtimePollID, err)
	}
	if _, err := database.runtime.Exec(ctx, `SELECT public.test_runtime_insert_history_poll(
		$1,$2,$3,$4,$5)`, fixture.instanceID, fixture.nodeType, fixture.contract,
		slot, fixture.policyID); err == nil {
		t.Fatal("runtime inserted new poll evidence into a retired day")
	} else {
		requireHistorySQLState(t, err, "23514")
	}
	if _, err := database.owner.Exec(ctx, `DROP FUNCTION
		public.test_runtime_insert_history_poll(uuid,text,text,timestamptz,uuid)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_history_retired_days(
		summary_date,instance_id,retired_at
	) VALUES($1,$2,clock_timestamp())`, targetDate, fixture.instanceID); err == nil {
		t.Fatal("migration owner forged a retired-day marker")
	} else {
		requireHistorySQLState(t, err, "42501")
	}
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "down"); err == nil {
		t.Fatal("Migration 9 down accepted a durable retired-day marker")
	}
	requireHistoryMigrationVersion(t, ctx, database, 9)
}

func TestAccountInventoryHistoryZeroPollLineageCompletesAcrossRetentionCutoff(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleSchemaFixture(t, ctx, database)
	targetDate := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -31)
	if _, err := database.owner.Exec(ctx, `UPDATE provider_inventory_policy_activations
		SET effective_from=$1,created_at=$1 WHERE policy_version_id=$2`,
		targetDate, fixture.policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_inventory_monitoring_activations(
		instance_id,effective_from,reason,actor,created_at
	) VALUES($1,$2,'reconciliation','zero-poll-cutoff-test',$2)`,
		fixture.instanceID, targetDate); err != nil {
		t.Fatal(err)
	}
	var compactionID uuid.UUID
	if err := database.owner.QueryRow(ctx, `INSERT INTO account_inventory_compaction_runs(
		summary_date,instance_id,provider_policy_version
	) VALUES($1,$2,$3) RETURNING compaction_run_id`, targetDate, fixture.instanceID,
		fixture.policyID).Scan(&compactionID); err != nil {
		t.Fatal(err)
	}
	var pollCount int
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM account_inventory_poll_runs
		WHERE instance_id=$1 AND scheduled_at >= ($2::date::timestamp AT TIME ZONE 'UTC')
		  AND scheduled_at < (($2::date+1)::timestamp AT TIME ZONE 'UTC')`,
		fixture.instanceID, targetDate).Scan(&pollCount); err != nil || pollCount != 0 {
		t.Fatalf("zero-poll fixture count=%d err=%v", pollCount, err)
	}
	var claimedID, compactionFence uuid.UUID
	if err := database.runtime.QueryRow(ctx, `SELECT compaction_run_id,fencing_token
		FROM public.control_claim_account_inventory_compaction_v1($1,30)`, uuid.New()).
		Scan(&claimedID, &compactionFence); err != nil || claimedID != compactionID {
		t.Fatalf("zero-poll claim=%s want=%s err=%v", claimedID, compactionID, err)
	}
	var checksumHex string
	if err := database.runtime.QueryRow(ctx, `SELECT
		public.control_summarize_account_inventory_compaction_v1($1,$2)->>'source_checksum_hex'`,
		compactionID, compactionFence).Scan(&checksumHex); err != nil || len(checksumHex) != 64 {
		t.Fatalf("zero-poll summarize checksum=%q err=%v", checksumHex, err)
	}
	var deleteStatus string
	if err := database.runtime.QueryRow(ctx, `SELECT
		public.control_delete_account_inventory_snapshot_batch_v1($1,$2,1)->>'status'`,
		compactionID, compactionFence).Scan(&deleteStatus); err != nil || deleteStatus != "deleting" {
		t.Fatalf("zero-poll snapshot deletion=%q err=%v", deleteStatus, err)
	}
	var compactionStatus string
	if err := database.runtime.QueryRow(ctx, `SELECT
		public.control_complete_account_inventory_compaction_v1(
			$1,$2,decode($3,'hex'))->>'status'`, compactionID, compactionFence,
		checksumHex).Scan(&compactionStatus); err != nil || compactionStatus != "completed" {
		t.Fatalf("zero-poll compaction completion=%q err=%v", compactionStatus, err)
	}
	var oldestBeforeRollup float64
	if err := database.runtime.QueryRow(ctx, `SELECT
		(public.control_account_inventory_history_metrics_snapshot_v1()
		 ->>'oldest_eligible_unfinished_seconds')::double precision`).Scan(&oldestBeforeRollup); err != nil ||
		oldestBeforeRollup < 72*time.Hour.Seconds() {
		t.Fatalf("zero-poll completed compaction oldest=%f err=%v", oldestBeforeRollup, err)
	}
	var createdRollups int
	if err := database.runtime.QueryRow(ctx, `SELECT
		(public.control_plan_account_inventory_history_v1(100)->>'rollup_runs_created')::integer`).
		Scan(&createdRollups); err != nil || createdRollups != 1 {
		t.Fatalf("zero-poll lineage rollup plan=%d err=%v", createdRollups, err)
	}
	var rollupID, fence uuid.UUID
	if err := database.runtime.QueryRow(ctx, `SELECT rollup_run_id,fencing_token
		FROM public.control_claim_account_inventory_daily_rollup_v1($1,30)`, uuid.New()).
		Scan(&rollupID, &fence); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := database.runtime.QueryRow(ctx, `SELECT
		public.control_finalize_account_inventory_daily_rollup_v1($1,$2)->>'status'`,
		rollupID, fence).Scan(&status); err != nil || status != "completed" {
		t.Fatalf("zero-poll lineage finalize=%q err=%v", status, err)
	}
	var completedDate time.Time
	if err := database.owner.QueryRow(ctx, `SELECT summary_date
		FROM account_inventory_daily_rollup_runs WHERE rollup_run_id=$1`, rollupID).
		Scan(&completedDate); err != nil || !completedDate.Equal(targetDate) {
		t.Fatalf("zero-poll completed day=%s want=%s err=%v", completedDate, targetDate, err)
	}
}

func TestAccountInventoryHistorySchemaACLAndProtectedDown(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleSchemaFixture(t, ctx, database)

	for _, table := range historyTables {
		for _, privilege := range []string{
			"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER",
		} {
			var allowed bool
			if err := database.owner.QueryRow(ctx,
				`SELECT has_table_privilege('relay_control_runtime',$1,$2)`,
				"public."+table, privilege).Scan(&allowed); err != nil {
				t.Fatal(err)
			}
			if allowed {
				t.Fatalf("runtime unexpectedly has %s on %s", privilege, table)
			}
		}
	}
	var runtimeCanExecute, registrarCanExecute bool
	if err := database.owner.QueryRow(ctx, `SELECT
		has_function_privilege('relay_control_runtime',$1,'EXECUTE'),
		has_function_privilege('relay_control_asset_registrar',$1,'EXECUTE')`,
		historyCompatibilityFunction).Scan(&runtimeCanExecute, &registrarCanExecute); err != nil {
		t.Fatal(err)
	}
	if !runtimeCanExecute || registrarCanExecute {
		t.Fatalf("history compatibility ACL runtime=%t registrar=%t",
			runtimeCanExecute, registrarCanExecute)
	}
	fakeAudit := `INSERT INTO audit_logs(
		category,action,result,request_id,details
	) VALUES('account_inventory_history','account_inventory_history.completed','success',
		'history-compaction-system',jsonb_build_object(
			'instance',$1::uuid::text,'summary_date',
			((clock_timestamp() AT TIME ZONE 'UTC')::date-4)::text,
			'phase','complete','row_count',0))`
	if _, err := database.runtime.Exec(ctx, fakeAudit, fixture.instanceID); err == nil {
		t.Fatal("runtime forged a history completion audit")
	} else {
		requireHistorySQLState(t, err, "42501")
	}
	if _, err := database.owner.Exec(ctx, fakeAudit, fixture.instanceID); err == nil {
		t.Fatal("migration owner forged a history completion audit")
	} else {
		requireHistorySQLState(t, err, "42501")
	}

	var compatibilityTableCount int
	if err := database.runtime.QueryRow(ctx, `SELECT
		(control_history_schema_compatibility_v1()->>'history_table_count')::integer`).
		Scan(&compatibilityTableCount); err != nil || compatibilityTableCount != 7 {
		t.Fatalf("runtime compatibility table count=%d err=%v", compatibilityTableCount, err)
	}
	var securedFunctionCount int
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM pg_proc AS procedure
		WHERE procedure.oid=ANY(ARRAY[
			to_regprocedure('public.control_reject_account_inventory_retired_day_poll()'),
			to_regprocedure('public.control_refresh_account_inventory_provider_health_v1()'),
			to_regprocedure('public.control_plan_account_inventory_history_v1(integer)'),
			to_regprocedure('public.control_claim_account_inventory_compaction_v1(uuid,integer)'),
			to_regprocedure('public.control_renew_account_inventory_compaction_v1(uuid,uuid,integer)'),
			to_regprocedure('public.control_reconcile_account_inventory_compactions_v1(integer)'),
			to_regprocedure('public.control_summarize_account_inventory_compaction_v1(uuid,uuid)'),
			to_regprocedure('public.control_delete_account_inventory_snapshot_batch_v1(uuid,uuid,integer)'),
			to_regprocedure('public.control_complete_account_inventory_compaction_v1(uuid,uuid,bytea)'),
			to_regprocedure('public.control_fail_account_inventory_compaction_v1(uuid,uuid,text)'),
			to_regprocedure('public.control_claim_account_inventory_daily_rollup_v1(uuid,integer)'),
			to_regprocedure('public.control_renew_account_inventory_daily_rollup_v1(uuid,uuid,integer)'),
			to_regprocedure('public.control_reconcile_account_inventory_daily_rollups_v1(integer)'),
			to_regprocedure('public.control_finalize_account_inventory_daily_rollup_v1(uuid,uuid)'),
			to_regprocedure('public.control_fail_account_inventory_daily_rollup_v1(uuid,uuid,text)'),
			to_regprocedure('public.control_delete_account_inventory_poll_retention_v1(integer)'),
			to_regprocedure('public.control_delete_account_inventory_rollup_row_retention_v1(integer)'),
			to_regprocedure('public.control_delete_account_inventory_rollup_run_retention_v1(integer)'),
			to_regprocedure('public.control_delete_account_inventory_compaction_run_retention_v1(integer)'),
			to_regprocedure('public.control_list_account_inventory_history_metrics_v1()'),
			to_regprocedure('public.control_history_schema_compatibility_v1()'),
			to_regprocedure('public.control_query_current_account_inventory_v1(uuid,text,text,text,text,text,integer)')
		]) AND procedure.prosecdef
		AND procedure.proconfig @> ARRAY['search_path=pg_catalog','TimeZone=UTC']`).
		Scan(&securedFunctionCount); err != nil || securedFunctionCount != 22 {
		t.Fatalf("secured history function settings=%d err=%v", securedFunctionCount, err)
	}
	var rollupClaimExists, rollupClaimIndexCoversFailed bool
	if err := database.owner.QueryRow(ctx, `SELECT
		to_regprocedure('public.control_claim_account_inventory_daily_rollup_v1(uuid,integer)') IS NOT NULL,
		coalesce((SELECT pg_get_expr(index_row.indpred,index_row.indrelid)
			LIKE '%pending%' AND pg_get_expr(index_row.indpred,index_row.indrelid) LIKE '%failed%'
		FROM pg_index AS index_row
		JOIN pg_class AS index_relation ON index_relation.oid=index_row.indexrelid
		WHERE index_relation.relname='account_inventory_daily_rollup_runs_claim_idx'
		  AND index_row.indisvalid AND index_row.indisready),false)`).
		Scan(&rollupClaimExists, &rollupClaimIndexCoversFailed); err != nil ||
		!rollupClaimExists || !rollupClaimIndexCoversFailed {
		t.Fatalf("rollup claim exists=%t failed-index=%t err=%v",
			rollupClaimExists, rollupClaimIndexCoversFailed, err)
	}

	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_daily_summaries
		DISABLE TRIGGER account_inventory_daily_summaries_immutable`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.runtime.Exec(ctx,
		`SELECT public.control_history_schema_compatibility_v1()`); err == nil {
		t.Fatal("compatibility accepted a disabled immutable trigger")
	} else {
		requireHistorySQLState(t, err, "55000")
	}
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_daily_summaries
		ENABLE TRIGGER account_inventory_daily_summaries_immutable`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.runtime.Exec(ctx,
		`SELECT public.control_history_schema_compatibility_v1()`); err != nil {
		t.Fatal("compatibility did not recover after trigger restoration")
	}
	if _, err := database.owner.Exec(ctx, `DROP INDEX account_inventory_daily_rollup_runs_claim_idx;
		CREATE INDEX account_inventory_daily_rollup_runs_claim_idx
		ON account_inventory_daily_rollup_runs(
			status,lease_expires_at,summary_date,instance_id
		) WHERE status='pending'`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.runtime.Exec(ctx,
		`SELECT public.control_history_schema_compatibility_v1()`); err == nil {
		t.Fatal("compatibility accepted a rollup claim index that omits recoverable failures")
	} else {
		requireHistorySQLState(t, err, "55000")
	}
	if _, err := database.owner.Exec(ctx, `DROP INDEX account_inventory_daily_rollup_runs_claim_idx;
		CREATE INDEX account_inventory_daily_rollup_runs_claim_idx
		ON account_inventory_daily_rollup_runs(
			status,lease_expires_at,summary_date,instance_id
		) WHERE status IN ('pending','failed')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.runtime.Exec(ctx,
		`SELECT public.control_history_schema_compatibility_v1()`); err != nil {
		t.Fatal("compatibility did not recover after rollup claim index restoration")
	}
	if _, err := database.owner.Exec(ctx, `ALTER FUNCTION
		public.control_plan_account_inventory_history_v1(integer) RESET TimeZone`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.runtime.Exec(ctx,
		`SELECT public.control_history_schema_compatibility_v1()`); err == nil {
		t.Fatal("compatibility accepted a SECURITY DEFINER function without UTC")
	} else {
		requireHistorySQLState(t, err, "55000")
	}
	if _, err := database.owner.Exec(ctx, `ALTER FUNCTION
		public.control_plan_account_inventory_history_v1(integer) SET TimeZone='UTC'`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `GRANT EXECUTE ON FUNCTION
		public.control_plan_account_inventory_history_v1(integer) TO PUBLIC`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.runtime.Exec(ctx,
		`SELECT public.control_history_schema_compatibility_v1()`); err == nil {
		t.Fatal("compatibility accepted PUBLIC history mutation access")
	} else {
		requireHistorySQLState(t, err, "55000")
	}
	if _, err := database.owner.Exec(ctx, `REVOKE EXECUTE ON FUNCTION
		public.control_plan_account_inventory_history_v1(integer) FROM PUBLIC`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.runtime.Exec(ctx,
		`SELECT public.control_history_schema_compatibility_v1()`); err != nil {
		t.Fatal("compatibility did not recover after function ACL restoration")
	}

	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_compaction_runs(
		summary_date,instance_id,provider_policy_version
	) VALUES ((clock_timestamp() AT TIME ZONE 'UTC')::date-4,$1,$2)`,
		fixture.instanceID, fixture.policyID); err != nil {
		t.Fatal(err)
	}
	claimedConnection, err := database.runtime.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer claimedConnection.Release()
	var claimedRows int
	if err := claimedConnection.QueryRow(ctx, `SELECT count(*)
		FROM public.control_claim_account_inventory_compaction_v1($1,30)`, uuid.New()).
		Scan(&claimedRows); err != nil || claimedRows != 1 {
		t.Fatalf("runtime compaction claim rows=%d err=%v", claimedRows, err)
	}
	var leakedGate string
	if err := claimedConnection.QueryRow(ctx, `SELECT coalesce(
		current_setting('relay_control.history_run_write',true),'')`).Scan(&leakedGate); err != nil {
		t.Fatal(err)
	}
	if leakedGate != "" {
		t.Fatalf("history run write gate leaked after claim: %q", leakedGate)
	}
	if _, err := claimedConnection.Exec(ctx, `UPDATE account_inventory_compaction_runs
		SET updated_at=clock_timestamp() WHERE instance_id=$1`, fixture.instanceID); err == nil {
		t.Fatal("runtime directly updated a run after controlled claim")
	} else {
		requireHistorySQLState(t, err, "42501")
	}

	ownerTransaction, err := database.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ownerTransaction.Exec(ctx, `SELECT set_config(
		'relay_control.history_run_write','account_inventory_compaction_runs',true)`); err != nil {
		_ = ownerTransaction.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err := ownerTransaction.Exec(ctx, `UPDATE account_inventory_compaction_runs
		SET updated_at=clock_timestamp() WHERE instance_id=$1`, fixture.instanceID); err == nil {
		_ = ownerTransaction.Rollback(ctx)
		t.Fatal("migration owner forged the history run gate")
	} else {
		requireHistorySQLState(t, err, "42501")
	}
	_ = ownerTransaction.Rollback(ctx)

	var plannedRollups, rollupRuns int
	if err := database.runtime.QueryRow(ctx, `SELECT
		(public.control_plan_account_inventory_history_v1(10)->>'rollup_runs_created')::integer`).
		Scan(&plannedRollups); err != nil || plannedRollups != 0 {
		t.Fatalf("planner reported premature rollup runs=%d err=%v", plannedRollups, err)
	}
	if err := database.owner.QueryRow(ctx, `SELECT count(*)
		FROM account_inventory_daily_rollup_runs`).Scan(&rollupRuns); err != nil || rollupRuns != 0 {
		t.Fatalf("planner created premature rollup runs=%d err=%v", rollupRuns, err)
	}
	if _, err := database.owner.Exec(ctx, `UPDATE account_inventory_compaction_runs
		SET updated_at=clock_timestamp() WHERE instance_id=$1`, fixture.instanceID); err == nil {
		t.Fatal("direct compaction run update was accepted")
	} else {
		requireHistorySQLState(t, err, "42501")
	}
	if _, err := database.owner.Exec(ctx, `TRUNCATE account_inventory_daily_summaries`); err == nil {
		t.Fatal("history summary truncate was accepted")
	} else {
		requireHistorySQLState(t, err, "42501")
	}

	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "down"); err == nil {
		t.Fatal("Migration 9 down accepted existing history run")
	}
	requireHistoryMigrationVersion(t, ctx, database, 9)
	var runs int
	if err := database.owner.QueryRow(ctx, `SELECT count(*)
		FROM account_inventory_compaction_runs WHERE instance_id=$1`, fixture.instanceID).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if runs != 1 {
		t.Fatal("protected down changed history state")
	}
}

func TestAccountInventoryHistorySummarizeWriteFailuresAreAtomic(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleSchemaFixture(t, ctx, database)
	targetDate := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -4)
	slot := targetDate.Add(12 * time.Hour)

	if _, err := database.owner.Exec(ctx, `UPDATE provider_inventory_policy_activations
		SET effective_from=$1,created_at=$1 WHERE policy_version_id=$2`,
		targetDate, fixture.policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_inventory_monitoring_activations(
		instance_id,effective_from,reason,actor,created_at
	) VALUES($1,$2,'reconciliation','summarize-atomic-test',$2)`,
		fixture.instanceID, targetDate); err != nil {
		t.Fatal(err)
	}
	pollID := uuid.New()
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_poll_runs(
		poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,
		provider_policy_version,status,attempt_count,max_attempts,poll_start_grace_seconds,
		created_at,first_started_at,last_started_at,finalized_at,observed_at,
		transport_success,response_shape_valid,contract_valid,inventory_mode,
		node_identity_complete,snapshot_complete,degraded,result,reason,
		source_record_count,identifiable_record_count,unidentified_record_count,
		unsupported_provider_count,out_of_scope_provider_count,node_version,node_commit
	) VALUES($1,$2,$3,$4,$5,$6,'finalized',1,2,299,
		$5::timestamptz+interval '1 second',$5::timestamptz+interval '2 seconds',
		$5::timestamptz+interval '2 seconds',$5::timestamptz+interval '4 seconds',
		$5::timestamptz+interval '3 seconds',true,true,true,'runtime',true,true,false,
		'success','none',1,1,0,0,0,'unknown','unknown')`, pollID, fixture.instanceID,
		fixture.nodeType, fixture.contract, slot, fixture.policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_poll_provider_results
		DISABLE TRIGGER account_inventory_poll_provider_results_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_snapshot_items
		DISABLE TRIGGER account_inventory_snapshot_items_insert_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_poll_provider_results(
		poll_run_id,provider,identifiable_count,missing_identity_count,
		duplicate_identity_count,identity_complete,snapshot_complete,degraded,reason,
		promotion_applied,promotion_skipped_reason
	) VALUES($1,'openai',1,0,0,true,true,false,'complete',true,NULL)`, pollID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_snapshot_items(
		poll_run_id,instance_id,provider,account_key,normalized_email,basic_status,
		success_count,failed_count,recent_request_count,observed_at
	) VALUES($1,$2,'openai','openai:summarize-atomic@example.invalid',
		'summarize-atomic@example.invalid','active',1,0,0,$3::timestamptz+interval '3 seconds')`,
		pollID, fixture.instanceID, slot); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_poll_provider_results
		ENABLE TRIGGER account_inventory_poll_provider_results_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_snapshot_items
		ENABLE TRIGGER account_inventory_snapshot_items_insert_guard`); err != nil {
		t.Fatal(err)
	}

	var runID uuid.UUID
	if err := database.owner.QueryRow(ctx, `INSERT INTO account_inventory_compaction_runs(
		summary_date,instance_id,provider_policy_version
	) VALUES($1,$2,$3) RETURNING compaction_run_id`, targetDate,
		fixture.instanceID, fixture.policyID).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	var claimedID, fence uuid.UUID
	if err := database.runtime.QueryRow(ctx, `SELECT compaction_run_id,fencing_token
		FROM public.control_claim_account_inventory_compaction_v1($1,300)`, uuid.New()).
		Scan(&claimedID, &fence); err != nil || claimedID != runID {
		t.Fatalf("claim run=%s want=%s err=%v", claimedID, runID, err)
	}
	var originalRun string
	if err := database.owner.QueryRow(ctx, `SELECT to_jsonb(run)::text
		FROM account_inventory_compaction_runs AS run WHERE compaction_run_id=$1`, runID).
		Scan(&originalRun); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `CREATE FUNCTION public.test_reject_history_summarize_write()
		RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
		BEGIN
			IF TG_TABLE_NAME <> 'audit_logs'
			   OR to_jsonb(NEW)->'details'->>'phase'='summarize' THEN
				RAISE EXCEPTION 'synthetic summarize write failure' USING ERRCODE='P0001';
			END IF;
			RETURN NEW;
		END;
		$$`); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name, operation, table, level string
	}{
		{"account_segment", "INSERT", "account_inventory_daily_summaries", "STATEMENT"},
		{"provider_segment", "INSERT", "account_inventory_daily_provider_summaries", "STATEMENT"},
		{"source_counts_checksum_and_status", "UPDATE", "account_inventory_compaction_runs", "STATEMENT"},
		{"summarized_audit", "INSERT", "audit_logs", "ROW"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := database.owner.Exec(ctx, fmt.Sprintf(`CREATE TRIGGER zz_test_reject_history_summarize_write
				BEFORE %s ON %s FOR EACH %s
				EXECUTE FUNCTION public.test_reject_history_summarize_write()`,
				test.operation, test.table, test.level)); err != nil {
				t.Fatal(err)
			}
			defer func() {
				_, _ = database.owner.Exec(ctx, fmt.Sprintf(`DROP TRIGGER IF EXISTS
					zz_test_reject_history_summarize_write ON %s`, test.table))
			}()
			if _, err := database.runtime.Exec(ctx, `SELECT
				public.control_summarize_account_inventory_compaction_v1($1,$2)`, runID, fence); err == nil {
				t.Fatal("summarize ignored the injected write failure")
			} else {
				requireHistorySQLState(t, err, "P0001")
			}
			var persistedRun string
			var accountSegments, providerSegments, audits int
			if err := database.owner.QueryRow(ctx, `SELECT to_jsonb(run)::text,
				(SELECT count(*) FROM account_inventory_daily_summaries WHERE compaction_run_id=$1),
				(SELECT count(*) FROM account_inventory_daily_provider_summaries WHERE compaction_run_id=$1),
				(SELECT count(*) FROM audit_logs
				 WHERE action='account_inventory_history.summarized'
				   AND details->>'instance'=$2::uuid::text
				   AND details->>'summary_date'=$3::date::text)
			FROM account_inventory_compaction_runs AS run WHERE compaction_run_id=$1`,
				runID, fixture.instanceID, targetDate).Scan(
				&persistedRun, &accountSegments, &providerSegments, &audits); err != nil {
				t.Fatal(err)
			}
			if persistedRun != originalRun || accountSegments != 0 || providerSegments != 0 || audits != 0 {
				t.Fatalf("partial summarize state: run_changed=%t account=%d provider=%d audits=%d",
					persistedRun != originalRun, accountSegments, providerSegments, audits)
			}
		})
	}
	if _, err := database.owner.Exec(ctx,
		`DROP FUNCTION public.test_reject_history_summarize_write()`); err != nil {
		t.Fatal(err)
	}
	var status, checksumHex string
	var accountSegments, providerSegments int
	if err := database.runtime.QueryRow(ctx, `WITH summarized AS (
		SELECT public.control_summarize_account_inventory_compaction_v1($1,$2) AS value
	) SELECT value->>'status',(value->>'account_segment_count')::integer,
		(value->>'provider_segment_count')::integer,value->>'source_checksum_hex'
		FROM summarized`, runID, fence).
		Scan(&status, &accountSegments, &providerSegments, &checksumHex); err != nil {
		t.Fatal(err)
	}
	if status != "summarized" || accountSegments != 1 || providerSegments != 1 || len(checksumHex) != 64 {
		t.Fatalf("retry status=%s account=%d provider=%d checksum=%q",
			status, accountSegments, providerSegments, checksumHex)
	}

	readSegments := func() (string, string) {
		t.Helper()
		var accounts, providers string
		if err := database.owner.QueryRow(ctx, `SELECT
			(SELECT jsonb_agg(to_jsonb(summary) ORDER BY summary_id)::text
			 FROM account_inventory_daily_summaries AS summary WHERE compaction_run_id=$1),
			(SELECT jsonb_agg(to_jsonb(summary) ORDER BY provider_summary_id)::text
			 FROM account_inventory_daily_provider_summaries AS summary
			 WHERE compaction_run_id=$1)`, runID).Scan(&accounts, &providers); err != nil {
			t.Fatal(err)
		}
		return accounts, providers
	}
	originalAccounts, originalProviders := readSegments()
	var replayStatus, replayChecksum string
	if err := database.runtime.QueryRow(ctx, `WITH summarized AS (
		SELECT public.control_summarize_account_inventory_compaction_v1($1,$2) AS value
	) SELECT value->>'status',value->>'source_checksum_hex' FROM summarized`, runID, fence).
		Scan(&replayStatus, &replayChecksum); err != nil {
		t.Fatal(err)
	}
	var summarizedAudits, compactionsCreated, compactionRuns int
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs
		WHERE action='account_inventory_history.summarized'
		  AND details->>'instance'=$1::uuid::text
		  AND details->>'summary_date'=$2::date::text`, fixture.instanceID, targetDate).
		Scan(&summarizedAudits); err != nil {
		t.Fatal(err)
	}
	if err := database.runtime.QueryRow(ctx, `SELECT
		(public.control_plan_account_inventory_history_v1(10)->>'compaction_runs_created')::integer`).
		Scan(&compactionsCreated); err != nil {
		t.Fatal(err)
	}
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM account_inventory_compaction_runs
		WHERE summary_date=$1 AND instance_id=$2 AND provider_policy_version=$3`,
		targetDate, fixture.instanceID, fixture.policyID).Scan(&compactionRuns); err != nil {
		t.Fatal(err)
	}
	if replayStatus != "summarized" || replayChecksum != checksumHex || summarizedAudits != 1 ||
		compactionsCreated != 0 || compactionRuns != 1 {
		t.Fatalf("summarized replay status=%s checksum_changed=%t audits=%d created=%d runs=%d",
			replayStatus, replayChecksum != checksumHex, summarizedAudits,
			compactionsCreated, compactionRuns)
	}

	rejectDirectDML := func(phase string) {
		t.Helper()
		for _, table := range []string{
			"account_inventory_daily_summaries",
			"account_inventory_daily_provider_summaries",
		} {
			if _, err := database.runtime.Exec(ctx, fmt.Sprintf(`INSERT INTO %s DEFAULT VALUES`, table)); err == nil {
				t.Fatalf("runtime directly inserted %s segment %s", phase, table)
			} else {
				requireHistorySQLState(t, err, "42501")
			}
			for _, statement := range []string{
				fmt.Sprintf(`UPDATE %s SET created_at=created_at`, table),
				fmt.Sprintf(`DELETE FROM %s`, table),
				fmt.Sprintf(`TRUNCATE %s`, table),
			} {
				if _, err := database.owner.Exec(ctx, statement); err == nil {
					t.Fatalf("migration owner directly mutated %s segment with %s", phase, statement)
				} else {
					requireHistorySQLState(t, err, "42501")
				}
			}
		}
	}
	rejectDirectDML("summarized")
	if accounts, providers := readSegments(); accounts != originalAccounts || providers != originalProviders {
		t.Fatal("ordinary DML changed summarized segment rows")
	}

	var deleteStatus, completeStatus string
	if err := database.runtime.QueryRow(ctx, `SELECT
		public.control_delete_account_inventory_snapshot_batch_v1($1,$2,1)->>'status'`,
		runID, fence).Scan(&deleteStatus); err != nil || deleteStatus != "deleting" {
		t.Fatalf("delete status=%q err=%v", deleteStatus, err)
	}
	if err := database.runtime.QueryRow(ctx, `SELECT
		public.control_complete_account_inventory_compaction_v1(
			$1,$2,decode($3,'hex'))->>'status'`, runID, fence, checksumHex).
		Scan(&completeStatus); err != nil || completeStatus != "completed" {
		t.Fatalf("complete status=%q err=%v", completeStatus, err)
	}
	if _, err := database.runtime.Exec(ctx, `SELECT
		public.control_summarize_account_inventory_compaction_v1($1,$2)`, runID, fence); err == nil {
		t.Fatal("completed compaction accepted summarize replay")
	} else {
		requireHistorySQLState(t, err, "P0002")
	}
	rejectDirectDML("completed")
	if accounts, providers := readSegments(); accounts != originalAccounts || providers != originalProviders {
		t.Fatal("completed summarize replay changed segment rows")
	}
}

func TestAccountInventoryHistoryCompactionMainPathAndRecovery(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleSchemaFixture(t, ctx, database)
	targetDate := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -4)
	slot := targetDate.Add(12 * time.Hour)

	// Move the synthetic policy truth into the old source day and install the
	// corresponding node-monitoring truth. These rows remain immutable for the
	// application; the migration owner builds an isolated historical fixture.
	if _, err := database.owner.Exec(ctx, `UPDATE provider_inventory_policy_activations
		SET effective_from=$1,created_at=$1
		WHERE policy_version_id=$2`, targetDate, fixture.policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_inventory_monitoring_activations(
		instance_id,effective_from,reason,actor,created_at
	) VALUES($1,$2,'reconciliation','integration-test',$2)`,
		fixture.instanceID, targetDate); err != nil {
		t.Fatal(err)
	}

	pollID := uuid.New()
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_poll_runs(
		poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,
		provider_policy_version,status,attempt_count,max_attempts,poll_start_grace_seconds,
		created_at,first_started_at,last_started_at,finalized_at,observed_at,
		transport_success,response_shape_valid,contract_valid,inventory_mode,
		node_identity_complete,snapshot_complete,degraded,result,reason,
		source_record_count,identifiable_record_count,unidentified_record_count,
		unsupported_provider_count,out_of_scope_provider_count,node_version,node_commit
	) VALUES(
		$1,$2,$3,$4,$5::timestamptz,$6,'finalized',1,2,299,
		$5::timestamptz+interval '1 second',$5::timestamptz+interval '2 seconds',
		$5::timestamptz+interval '2 seconds',$5::timestamptz+interval '4 seconds',
		$5::timestamptz+interval '3 seconds',
		true,true,true,'runtime',true,true,false,'success','none',
		1,1,0,0,0,'unknown','unknown'
	)`, pollID, fixture.instanceID, fixture.nodeType, fixture.contract, slot,
		fixture.policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_poll_provider_results
		DISABLE TRIGGER account_inventory_poll_provider_results_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_snapshot_items
		DISABLE TRIGGER account_inventory_snapshot_items_insert_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_poll_provider_results(
		poll_run_id,provider,identifiable_count,missing_identity_count,
		duplicate_identity_count,identity_complete,snapshot_complete,degraded,reason,
		promotion_applied,promotion_skipped_reason
	) VALUES($1,'openai',1,0,0,true,true,false,'complete',true,NULL)`, pollID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_snapshot_items(
		poll_run_id,instance_id,provider,account_key,normalized_email,basic_status,
		success_count,failed_count,recent_request_count,observed_at
	) VALUES($1,$2,'openai','openai:history@example.invalid',
		'history@example.invalid','active',7,1,2,$3::timestamptz+interval '3 seconds')`,
		pollID, fixture.instanceID, slot); err != nil {
		t.Fatal(err)
	}
	// An additive source-table column is deliberately outside checksum v1.
	// A whole-row JSON checksum would silently change here.
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_snapshot_items
		ADD COLUMN checksum_v1_irrelevant text NOT NULL DEFAULT 'ignored'`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_poll_provider_results
		ENABLE TRIGGER account_inventory_poll_provider_results_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_snapshot_items
		ENABLE TRIGGER account_inventory_snapshot_items_insert_guard`); err != nil {
		t.Fatal(err)
	}

	var runID uuid.UUID
	if err := database.owner.QueryRow(ctx, `INSERT INTO account_inventory_compaction_runs(
		summary_date,instance_id,provider_policy_version
	) VALUES($1,$2,$3) RETURNING compaction_run_id`, targetDate,
		fixture.instanceID, fixture.policyID).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	worker := uuid.New()
	var claimedRunID, fence uuid.UUID
	if err := database.runtime.QueryRow(ctx, `SELECT compaction_run_id,fencing_token
		FROM public.control_claim_account_inventory_compaction_v1($1,30)`, worker).
		Scan(&claimedRunID, &fence); err != nil || claimedRunID != runID {
		t.Fatalf("claim run=%s fence=%s err=%v", claimedRunID, fence, err)
	}
	var renewed int
	if err := database.runtime.QueryRow(ctx, `SELECT count(*)
		FROM public.control_renew_account_inventory_compaction_v1($1,$2,30)`, runID, fence).
		Scan(&renewed); err != nil || renewed != 1 {
		t.Fatalf("renewed=%d err=%v", renewed, err)
	}

	var summarizeStatus, checksumHex string
	var sourceRows, sourceSnapshots, accountSegments, providerSegments int64
	if err := database.runtime.QueryRow(ctx, `WITH summarized AS (
		SELECT public.control_summarize_account_inventory_compaction_v1($1,$2) AS value
	) SELECT value->>'status',(value->>'source_rows')::bigint,
		(value->>'source_snapshot_count')::bigint,value->>'source_checksum_hex',
		(value->>'account_segment_count')::bigint,
		(value->>'provider_segment_count')::bigint FROM summarized`, runID, fence).
		Scan(&summarizeStatus, &sourceRows, &sourceSnapshots, &checksumHex,
			&accountSegments, &providerSegments); err != nil {
		t.Fatal(err)
	}
	if summarizeStatus != "summarized" || sourceRows != 3 || sourceSnapshots != 1 ||
		len(checksumHex) != 64 || accountSegments != 1 || providerSegments != 1 {
		t.Fatalf("summarize status=%s rows=%d snapshots=%d checksum=%s accounts=%d providers=%d",
			summarizeStatus, sourceRows, sourceSnapshots, checksumHex,
			accountSegments, providerSegments)
	}
	var expectedChain history.ChainV1
	if err := expectedChain.Add(
		history.TextField("poll"), history.TimeField(slot), history.TextField(pollID.String()),
		history.TextField("finalized"), history.TimeField(slot.Add(3*time.Second)),
		history.BoolField(true), history.BoolField(true), history.BoolField(true),
		history.BoolField(false),
	); err != nil {
		t.Fatal(err)
	}
	if err := expectedChain.Add(
		history.TextField("provider_result"), history.TimeField(slot),
		history.TextField(pollID.String()), history.TextField("openai"),
		history.BoolField(true), history.BoolField(true), history.NullField(),
		history.BoolField(false), history.TextField("complete"),
	); err != nil {
		t.Fatal(err)
	}
	if err := expectedChain.Add(
		history.TextField("snapshot"), history.TimeField(slot),
		history.TextField(pollID.String()), history.TextField(fixture.instanceID.String()),
		history.TextField("openai"), history.TextField("openai:history@example.invalid"),
		history.TimeField(slot.Add(3*time.Second)), history.TextField("active"),
		history.Int64Field(7), history.Int64Field(1),
	); err != nil {
		t.Fatal(err)
	}
	expectedChecksum := expectedChain.Sum()
	if checksumHex != hex.EncodeToString(expectedChecksum[:]) {
		t.Fatalf("PostgreSQL/Go checksum mismatch: postgres=%s go=%x", checksumHex, expectedChecksum)
	}
	var originalFieldDigest, changedFieldDigest string
	if err := database.owner.QueryRow(ctx, `SELECT
		encode(public.control_history_checksum_chain_v1(ARRAY[
			public.control_history_canonical_row_v1(
				convert_to('snapshot','UTF8'),convert_to('1','UTF8'))
		]),'hex'),
		encode(public.control_history_checksum_chain_v1(ARRAY[
			public.control_history_canonical_row_v1(
				convert_to('snapshot','UTF8'),convert_to('2','UTF8'))
		]),'hex')`).Scan(&originalFieldDigest, &changedFieldDigest); err != nil {
		t.Fatal(err)
	}
	if originalFieldDigest == changedFieldDigest {
		t.Fatal("checksum v1 ignored a declared field change")
	}

	ownerTransaction, err := database.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ownerTransaction.Exec(ctx, `SELECT set_config(
		'relay_control.history_snapshot_delete',$1,true)`, runID.String()); err != nil {
		_ = ownerTransaction.Rollback(ctx)
		t.Fatal(err)
	}
	_, err = ownerTransaction.Exec(ctx, `DELETE FROM account_inventory_snapshot_items
		WHERE poll_run_id=$1`, pollID)
	if err == nil {
		_ = ownerTransaction.Rollback(ctx)
		t.Fatal("migration owner forged snapshot delete gate")
	}
	requireHistorySQLState(t, err, "42501")
	_ = ownerTransaction.Rollback(ctx)

	connection, err := database.runtime.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	var deleteStatus string
	var deleted, totalDeleted, remaining int64
	if err := connection.QueryRow(ctx, `WITH deleted AS (
		SELECT public.control_delete_account_inventory_snapshot_batch_v1($1,$2,1) AS value
	) SELECT value->>'status',(value->>'deleted_count')::bigint,
		(value->>'total_deleted_count')::bigint,(value->>'remaining_count')::bigint
	FROM deleted`, runID, fence).Scan(&deleteStatus, &deleted, &totalDeleted, &remaining); err != nil {
		t.Fatal(err)
	}
	if deleteStatus != "deleting" || deleted != 1 || totalDeleted != 1 || remaining != 0 {
		t.Fatalf("delete status=%s deleted=%d total=%d remaining=%d",
			deleteStatus, deleted, totalDeleted, remaining)
	}
	var leakedDeleteGate string
	if err := connection.QueryRow(ctx, `SELECT coalesce(current_setting(
		'relay_control.history_snapshot_delete',true),'')`).Scan(&leakedDeleteGate); err != nil {
		t.Fatal(err)
	}
	if leakedDeleteGate != "" {
		t.Fatalf("snapshot delete gate leaked: %q", leakedDeleteGate)
	}
	var leakedAuditGate string
	if err := connection.QueryRow(ctx, `SELECT coalesce(current_setting(
		'relay_control.history_audit_write',true),'')`).Scan(&leakedAuditGate); err != nil {
		t.Fatal(err)
	}
	if leakedAuditGate != "" {
		t.Fatalf("history audit gate leaked: %q", leakedAuditGate)
	}

	var completeStatus string
	var idempotent bool
	if err := database.runtime.QueryRow(ctx, `WITH completed AS (
		SELECT public.control_complete_account_inventory_compaction_v1(
			$1,$2,decode($3,'hex')) AS value
	) SELECT value->>'status',(value->>'idempotent')::boolean FROM completed`,
		runID, fence, checksumHex).Scan(&completeStatus, &idempotent); err != nil {
		t.Fatal(err)
	}
	if completeStatus != "completed" || idempotent {
		t.Fatalf("complete status=%s idempotent=%t", completeStatus, idempotent)
	}
	if err := database.runtime.QueryRow(ctx, `SELECT
		(public.control_complete_account_inventory_compaction_v1(
			$1,$2,decode($3,'hex'))->>'idempotent')::boolean`, runID, fence, checksumHex).
		Scan(&idempotent); err != nil || !idempotent {
		t.Fatalf("idempotent complete=%t err=%v", idempotent, err)
	}
	if _, err := database.runtime.Exec(ctx, `SELECT
		public.control_complete_account_inventory_compaction_v1(
			$1,$2,decode(repeat('00',32),'hex'))`, runID, fence); err == nil {
		t.Fatal("completed replay accepted a different checksum")
	} else {
		requireHistorySQLState(t, err, "P1003")
	}

	var plannedRollups int
	if err := database.runtime.QueryRow(ctx, `SELECT
		(public.control_plan_account_inventory_history_v1(10)->>'rollup_runs_created')::integer`).
		Scan(&plannedRollups); err != nil || plannedRollups != 1 {
		t.Fatalf("planned rollups=%d err=%v", plannedRollups, err)
	}
	rollupWorker := uuid.New()
	var rollupRunID, rollupFence uuid.UUID
	if err := database.runtime.QueryRow(ctx, `SELECT rollup_run_id,fencing_token
		FROM public.control_claim_account_inventory_daily_rollup_v1($1,30)`, rollupWorker).
		Scan(&rollupRunID, &rollupFence); err != nil {
		t.Fatalf("claim rollup: %v", err)
	}
	var secondClaimCount int
	if err := database.runtime.QueryRow(ctx, `SELECT count(*)
		FROM public.control_claim_account_inventory_daily_rollup_v1($1,30)`, uuid.New()).
		Scan(&secondClaimCount); err != nil || secondClaimCount != 0 {
		t.Fatalf("concurrent rollup claim rows=%d err=%v", secondClaimCount, err)
	}

	var rollupStatus, segmentChecksumHex, rollupFailure string
	var expectedSegments, completedSegments, accountRollups, providerRollups int64
	if err := database.runtime.QueryRow(ctx, `WITH finalized AS (
		SELECT public.control_finalize_account_inventory_daily_rollup_v1($1,$2) AS value
	) SELECT value->>'status',(value->>'expected_segment_count')::bigint,
		(value->>'completed_segment_count')::bigint,value->>'segment_checksum_hex',
		(value->>'account_rollup_count')::bigint,(value->>'provider_rollup_count')::bigint,
		coalesce(value->>'failure_reason','') FROM finalized`, rollupRunID, rollupFence).
		Scan(&rollupStatus, &expectedSegments, &completedSegments, &segmentChecksumHex,
			&accountRollups, &providerRollups, &rollupFailure); err != nil {
		t.Fatalf("finalize rollup: %v", err)
	}
	if rollupStatus != "completed" || expectedSegments != 1 || completedSegments != 1 ||
		len(segmentChecksumHex) != 64 || accountRollups != 1 || providerRollups != 1 ||
		rollupFailure != "" {
		t.Fatalf("rollup status=%s expected=%d completed=%d checksum=%s accounts=%d providers=%d failure=%q",
			rollupStatus, expectedSegments, completedSegments, segmentChecksumHex,
			accountRollups, providerRollups, rollupFailure)
	}

	var accountProvider, accountKey, accountStatus string
	var firstScheduled, lastScheduled, firstObserved, lastObserved time.Time
	var sampleCount, disabledCount, unavailableCount, errorCount, activeCount, unknownCount int64
	var firstSuccess, lastSuccess, successResets, firstFailed, lastFailed, failedResets int64
	if err := database.owner.QueryRow(ctx, `SELECT provider,account_key,
		first_scheduled_at,last_scheduled_at,first_observed_at,last_observed_at,
		last_basic_status,sample_count,disabled_count,unavailable_count,error_count,
		active_count,unknown_count,first_success_count,last_success_count,
		success_reset_count,first_failed_count,last_failed_count,failed_reset_count
	FROM account_inventory_daily_summaries WHERE compaction_run_id=$1`, runID).Scan(
		&accountProvider, &accountKey, &firstScheduled, &lastScheduled,
		&firstObserved, &lastObserved, &accountStatus, &sampleCount, &disabledCount,
		&unavailableCount, &errorCount, &activeCount, &unknownCount, &firstSuccess,
		&lastSuccess, &successResets, &firstFailed, &lastFailed, &failedResets,
	); err != nil {
		t.Fatal(err)
	}
	var provider string
	var expectedPolls, transportSuccess, contractValid, snapshotComplete int64
	var promotionApplied, promotionSkipped, policyChanged, abandoned, degradedCount int64
	var firstPromotion, lastPromotion time.Time
	var coverageNumerator, coverageDenominator, coverageThreshold int64
	var coverageStatus string
	if err := database.owner.QueryRow(ctx, `SELECT provider,expected_poll_count,
		transport_success_count,contract_valid_count,snapshot_complete_count,
		promotion_applied_count,promotion_skipped_count,policy_changed_count,
		abandoned_count,degraded_count,first_promotion_at,last_promotion_at,
		coverage_numerator,coverage_denominator,coverage_threshold_basis_points,
		coverage_status FROM account_inventory_daily_provider_summaries
		WHERE compaction_run_id=$1`, runID).Scan(
		&provider, &expectedPolls, &transportSuccess, &contractValid, &snapshotComplete,
		&promotionApplied, &promotionSkipped, &policyChanged, &abandoned, &degradedCount,
		&firstPromotion, &lastPromotion, &coverageNumerator, &coverageDenominator,
		&coverageThreshold, &coverageStatus,
	); err != nil {
		t.Fatal(err)
	}
	var segmentChain history.ChainV1
	if err := segmentChain.Add(
		history.TextField("account_segment"), history.TextField(targetDate.Format("2006-01-02")),
		history.TextField(fixture.instanceID.String()), history.TextField(accountProvider),
		history.TextField(accountKey), history.TextField(fixture.policyID.String()),
		history.TimeField(firstScheduled), history.TimeField(lastScheduled),
		history.TimeField(firstObserved), history.TimeField(lastObserved),
		history.TextField(accountStatus), history.Int64Field(sampleCount),
		history.Int64Field(disabledCount), history.Int64Field(unavailableCount),
		history.Int64Field(errorCount), history.Int64Field(activeCount),
		history.Int64Field(unknownCount), history.Int64Field(firstSuccess),
		history.Int64Field(lastSuccess), history.Int64Field(successResets),
		history.Int64Field(firstFailed), history.Int64Field(lastFailed),
		history.Int64Field(failedResets),
	); err != nil {
		t.Fatal(err)
	}
	if err := segmentChain.Add(
		history.TextField("provider_segment"), history.TextField(targetDate.Format("2006-01-02")),
		history.TextField(fixture.instanceID.String()), history.TextField(provider),
		history.TextField(fixture.policyID.String()), history.Int64Field(expectedPolls),
		history.Int64Field(transportSuccess), history.Int64Field(contractValid),
		history.Int64Field(snapshotComplete), history.Int64Field(promotionApplied),
		history.Int64Field(promotionSkipped), history.Int64Field(policyChanged),
		history.Int64Field(abandoned), history.Int64Field(degradedCount),
		history.TimeField(firstPromotion), history.TimeField(lastPromotion),
		history.Int64Field(coverageNumerator), history.Int64Field(coverageDenominator),
		history.Int64Field(coverageThreshold), history.TextField(coverageStatus),
	); err != nil {
		t.Fatal(err)
	}
	wantSegmentChecksum := segmentChain.Sum()
	if segmentChecksumHex != hex.EncodeToString(wantSegmentChecksum[:]) {
		t.Fatalf("PostgreSQL/Go segment checksum mismatch: postgres=%s go=%x",
			segmentChecksumHex, wantSegmentChecksum)
	}
	var rollupCoverageStatus string
	var rollupCoverageNumerator, rollupCoverageDenominator int64
	if err := database.owner.QueryRow(ctx, `SELECT coverage_status,coverage_numerator,
		coverage_denominator FROM account_inventory_daily_provider_rollups
		WHERE rollup_run_id=$1`, rollupRunID).Scan(
		&rollupCoverageStatus, &rollupCoverageNumerator, &rollupCoverageDenominator,
	); err != nil {
		t.Fatal(err)
	}
	if rollupCoverageStatus != "partial" || rollupCoverageNumerator != 1 ||
		rollupCoverageDenominator != 288 {
		t.Fatalf("rollup coverage=%s %d/%d", rollupCoverageStatus,
			rollupCoverageNumerator, rollupCoverageDenominator)
	}
	if err := database.runtime.QueryRow(ctx, `SELECT
		(public.control_finalize_account_inventory_daily_rollup_v1($1,$2)->>'idempotent')::boolean`,
		rollupRunID, rollupFence).Scan(&idempotent); err != nil || !idempotent {
		t.Fatalf("idempotent rollup finalize=%t err=%v", idempotent, err)
	}
	if _, err := database.runtime.Exec(ctx, `SELECT
		public.control_finalize_account_inventory_daily_rollup_v1($1,$2)`,
		rollupRunID, uuid.New()); err == nil {
		t.Fatal("completed rollup replay accepted a different fence")
	} else {
		requireHistorySQLState(t, err, "P0002")
	}

	var auditRows int
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs
		WHERE category='account_inventory_history'
		  AND actor_admin_id IS NULL AND target_admin_id IS NULL
		  AND actor_fingerprint IS NULL AND source_fingerprint IS NULL
		  AND reason IS NULL
		  AND NOT (details ?| ARRAY['compaction_run_id','fencing_token',
			'provider_policy_version','source_checksum','account_key','email'])`).Scan(&auditRows); err != nil {
		t.Fatal(err)
	}
	if auditRows != 4 {
		t.Fatalf("history audit rows=%d want=4", auditRows)
	}

	// An active completion mismatch cannot be reported while leaving a
	// deleting run behind. It becomes a fixed failed state and audit in the
	// same transaction; a completed replay remains immutable instead.
	mismatchRun, mismatchFence := uuid.New(), uuid.New()
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_compaction_runs(
		compaction_run_id,summary_date,instance_id,provider_policy_version,status,
		claim_owner,lease_expires_at,fencing_token,attempt_count,checksum_version,
		source_snapshot_count,source_poll_count,source_provider_result_count,
		source_duplicate_count,source_checksum,deleted_snapshot_count,
		created_at,summarized_at,deleting_at,updated_at
	) VALUES($1,$2::timestamptz+interval '1 day',$3,$4,'deleting','mismatch-worker',
		clock_timestamp()+interval '30 seconds',$5,1,1,0,0,0,0,
		decode(repeat('11',32),'hex'),0,clock_timestamp()-interval '3 seconds',
		clock_timestamp()-interval '2 seconds',clock_timestamp()-interval '1 second',
		clock_timestamp())`, mismatchRun, targetDate, fixture.instanceID, fixture.policyID,
		mismatchFence); err != nil {
		t.Fatal(err)
	}
	var mismatchStatus, mismatchReason string
	if err := database.runtime.QueryRow(ctx, `WITH completed AS (
		SELECT public.control_complete_account_inventory_compaction_v1(
			$1,$2,decode(repeat('22',32),'hex')) AS value
	) SELECT value->>'status',value->>'failure_reason' FROM completed`,
		mismatchRun, mismatchFence).Scan(&mismatchStatus, &mismatchReason); err != nil {
		t.Fatal(err)
	}
	if mismatchStatus != "failed" || mismatchReason != "source_checksum_mismatch" {
		t.Fatalf("mismatch status=%s reason=%s", mismatchStatus, mismatchReason)
	}
	var persistedReason string
	if err := database.owner.QueryRow(ctx, `SELECT failure_reason
		FROM account_inventory_compaction_runs WHERE compaction_run_id=$1`, mismatchRun).
		Scan(&persistedReason); err != nil || persistedReason != mismatchReason {
		t.Fatalf("persisted mismatch reason=%s err=%v", persistedReason, err)
	}

	// Reconciliation persists a fixed failure, and claim atomically restores
	// failed_from before issuing a fresh fence.
	var recoveryRun uuid.UUID
	if err := database.owner.QueryRow(ctx, `INSERT INTO account_inventory_compaction_runs(
		summary_date,instance_id,provider_policy_version,status,claim_owner,
		lease_expires_at,fencing_token,attempt_count
	) VALUES($1::timestamptz-interval '1 day',$2,$3,'pending','expired-worker',clock_timestamp()-interval '1 second',
		$4,1) RETURNING compaction_run_id`, targetDate, fixture.instanceID,
		fixture.policyID, uuid.New()).Scan(&recoveryRun); err != nil {
		t.Fatal(err)
	}
	var reconciled int
	if err := database.runtime.QueryRow(ctx, `SELECT
		(public.control_reconcile_account_inventory_compactions_v1(10)->>'failed_count')::integer`).
		Scan(&reconciled); err != nil || reconciled != 1 {
		t.Fatalf("reconciled=%d err=%v", reconciled, err)
	}
	var recoveredStatus string
	var failedFrom *string
	if err := database.runtime.QueryRow(ctx, `SELECT status,failed_from
		FROM public.control_claim_account_inventory_compaction_v1($1,30)`, uuid.New()).
		Scan(&recoveredStatus, &failedFrom); err != nil {
		t.Fatal(err)
	}
	if recoveredStatus != "pending" || failedFrom != nil {
		t.Fatalf("recovered status=%s failed_from=%v", recoveredStatus, failedFrom)
	}
	var integrityStatus, integrityReason string
	if err := database.owner.QueryRow(ctx, `SELECT status,failure_reason
		FROM account_inventory_compaction_runs WHERE compaction_run_id=$1`, mismatchRun).
		Scan(&integrityStatus, &integrityReason); err != nil {
		t.Fatal(err)
	}
	if integrityStatus != "failed" || integrityReason != "source_checksum_mismatch" {
		t.Fatalf("integrity failure was reopened: status=%s reason=%s",
			integrityStatus, integrityReason)
	}
}

func TestAccountInventoryDailyRollupNoProviderAtomicFinalize(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleSchemaFixture(t, ctx, database)
	targetDate := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -4)
	noProviderPolicy := uuid.New()

	if _, err := database.owner.Exec(ctx, `DELETE FROM provider_inventory_policy_activations
		WHERE policy_version_id=$1`, fixture.policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO provider_inventory_policy_versions(
		policy_version_id,node_type,driver_contract_version,active_providers,
		out_of_scope_providers,created_by,created_at
	) VALUES($1,$2,$3,ARRAY[]::text[],ARRAY['legacy'],'integration-test',$4)`,
		noProviderPolicy, fixture.nodeType, fixture.contract, targetDate); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO provider_inventory_policy_activations(
		node_type,driver_contract_version,policy_version_id,effective_from,
		activated_by,created_at
	) VALUES($1,$2,$3,$4,'integration-test',$4)`, fixture.nodeType, fixture.contract,
		noProviderPolicy, targetDate); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_inventory_monitoring_activations(
		instance_id,effective_from,reason,actor,created_at
	) VALUES($1,$2,'reconciliation','integration-test',$2)`, fixture.instanceID,
		targetDate); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_compaction_runs(
		summary_date,instance_id,provider_policy_version,status,attempt_count,
		checksum_version,source_snapshot_count,source_poll_count,
		source_provider_result_count,source_duplicate_count,source_checksum,
		deleted_snapshot_count,created_at,summarized_at,deleting_at,completed_at,updated_at
	) VALUES($1::date,$2,$3,'completed',0,1,0,0,0,0,decode(repeat('00',32),'hex'),0,
		$1::timestamptz+interval '1 day',$1::timestamptz+interval '1 day 1 second',
		$1::timestamptz+interval '1 day 2 seconds',$1::timestamptz+interval '1 day 3 seconds',
		$1::timestamptz+interval '1 day 3 seconds')`, targetDate, fixture.instanceID,
		noProviderPolicy); err != nil {
		t.Fatal(err)
	}
	var planned int
	if err := database.runtime.QueryRow(ctx, `SELECT
		(public.control_plan_account_inventory_history_v1(10)->>'rollup_runs_created')::integer`).
		Scan(&planned); err != nil || planned != 1 {
		t.Fatalf("no-Provider planned rollups=%d err=%v", planned, err)
	}
	var rollupRunID, fence uuid.UUID
	if err := database.runtime.QueryRow(ctx, `SELECT rollup_run_id,fencing_token
		FROM public.control_claim_account_inventory_daily_rollup_v1($1,30)`, uuid.New()).
		Scan(&rollupRunID, &fence); err != nil {
		t.Fatal(err)
	}
	var renewed int
	if err := database.runtime.QueryRow(ctx, `SELECT count(*)
		FROM public.control_renew_account_inventory_daily_rollup_v1($1,$2,30)`,
		rollupRunID, fence).Scan(&renewed); err != nil || renewed != 1 {
		t.Fatalf("renew rollup rows=%d err=%v", renewed, err)
	}
	if err := database.runtime.QueryRow(ctx, `SELECT count(*)
		FROM public.control_renew_account_inventory_daily_rollup_v1($1,$2,30)`,
		rollupRunID, uuid.New()).Scan(&renewed); err != nil || renewed != 0 {
		t.Fatalf("stale renew rollup rows=%d err=%v", renewed, err)
	}

	recoveryRunID, recoveryFence := uuid.New(), uuid.New()
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_daily_rollup_runs(
		rollup_run_id,summary_date,instance_id,status,claim_owner,lease_expires_at,
		fencing_token,attempt_count,created_at,updated_at
	) VALUES($1,$2::date-1,$3,'pending','expired-rollup-worker',
		clock_timestamp()-interval '1 second',$4,1,
		$2::timestamptz,clock_timestamp())`, recoveryRunID, targetDate,
		fixture.instanceID, recoveryFence); err != nil {
		t.Fatal(err)
	}
	var reconciled int
	if err := database.runtime.QueryRow(ctx, `SELECT
		(public.control_reconcile_account_inventory_daily_rollups_v1(10)->>'failed_count')::integer`).
		Scan(&reconciled); err != nil || reconciled != 1 {
		t.Fatalf("reconcile rollups=%d err=%v", reconciled, err)
	}
	var recoveredRunID, recoveredFence uuid.UUID
	if err := database.runtime.QueryRow(ctx, `SELECT rollup_run_id,fencing_token
		FROM public.control_claim_account_inventory_daily_rollup_v1($1,30)`, uuid.New()).
		Scan(&recoveredRunID, &recoveredFence); err != nil || recoveredRunID != recoveryRunID {
		t.Fatalf("recover rollup run=%s want=%s err=%v", recoveredRunID, recoveryRunID, err)
	}
	var failedStatus, failedReason string
	if err := database.runtime.QueryRow(ctx, `WITH failed AS (
		SELECT public.control_fail_account_inventory_daily_rollup_v1(
			$1,$2,'segment_checksum_mismatch') AS value
	) SELECT value->>'status',value->>'failure_reason' FROM failed`,
		recoveryRunID, recoveredFence).Scan(&failedStatus, &failedReason); err != nil {
		t.Fatal(err)
	}
	if failedStatus != "failed" || failedReason != "segment_checksum_mismatch" {
		t.Fatalf("failed rollup status=%s reason=%s", failedStatus, failedReason)
	}
	var permanentRetryCount int
	if err := database.runtime.QueryRow(ctx, `SELECT count(*)
		FROM public.control_claim_account_inventory_daily_rollup_v1($1,30)`, uuid.New()).
		Scan(&permanentRetryCount); err != nil || permanentRetryCount != 0 {
		t.Fatalf("permanent failed rollup was reclaimed rows=%d err=%v",
			permanentRetryCount, err)
	}

	if _, err := database.owner.Exec(ctx, `CREATE FUNCTION public.test_reject_rollup_audit()
		RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
		BEGIN
			IF NEW.category='account_inventory_history'
			   AND NEW.details->>'phase'='rollup_complete' THEN
				RAISE EXCEPTION 'synthetic rollup audit failure';
			END IF;
			RETURN NEW;
		END;
		$$;
		CREATE TRIGGER zz_test_reject_rollup_audit BEFORE INSERT ON audit_logs
		FOR EACH ROW EXECUTE FUNCTION public.test_reject_rollup_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.runtime.Exec(ctx, `SELECT
		public.control_finalize_account_inventory_daily_rollup_v1($1,$2)`,
		rollupRunID, fence); err == nil {
		t.Fatal("rollup finalize ignored an audit failure")
	}
	var status string
	var finalRows int
	if err := database.owner.QueryRow(ctx, `SELECT status,
		(SELECT count(*) FROM account_inventory_daily_account_rollups WHERE rollup_run_id=$1)
		+(SELECT count(*) FROM account_inventory_daily_provider_rollups WHERE rollup_run_id=$1)
		FROM account_inventory_daily_rollup_runs WHERE rollup_run_id=$1`, rollupRunID).
		Scan(&status, &finalRows); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || finalRows != 0 {
		t.Fatalf("failed audit left rollup status=%s final_rows=%d", status, finalRows)
	}
	if _, err := database.owner.Exec(ctx, `DROP TRIGGER zz_test_reject_rollup_audit ON audit_logs;
		DROP FUNCTION public.test_reject_rollup_audit()`); err != nil {
		t.Fatal(err)
	}

	var checksum string
	var accountCount, providerCount int
	if err := database.runtime.QueryRow(ctx, `WITH finalized AS (
		SELECT public.control_finalize_account_inventory_daily_rollup_v1($1,$2) AS value
	) SELECT value->>'status',value->>'segment_checksum_hex',
		(value->>'account_rollup_count')::integer,
		(value->>'provider_rollup_count')::integer FROM finalized`, rollupRunID, fence).
		Scan(&status, &checksum, &accountCount, &providerCount); err != nil {
		t.Fatal(err)
	}
	if status != "completed" || checksum !=
		"0000000000000000000000000000000000000000000000000000000000000000" ||
		accountCount != 0 || providerCount != 0 {
		t.Fatalf("no-Provider finalize status=%s checksum=%s account=%d provider=%d",
			status, checksum, accountCount, providerCount)
	}
	var coverageRows int
	if err := database.runtime.QueryRow(ctx, `SELECT count(*)
		FROM public.control_list_account_inventory_history_metrics_v1()
		WHERE instance_id=$1`, fixture.instanceID).Scan(&coverageRows); err != nil || coverageRows != 0 {
		t.Fatalf("0/0 Provider coverage rows=%d err=%v", coverageRows, err)
	}
}

func TestAccountInventoryDailyRollupPolicyBoundaryResetAndCoverage(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleSchemaFixture(t, ctx, database)
	targetDate := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -4)
	boundary := targetDate.Add(12 * time.Hour)
	secondPolicy, firstCompaction, secondCompaction := uuid.New(), uuid.New(), uuid.New()

	if _, err := database.owner.Exec(ctx, `DELETE FROM provider_inventory_policy_activations
		WHERE policy_version_id=$1`, fixture.policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO provider_inventory_policy_versions(
			policy_version_id,node_type,driver_contract_version,active_providers,
			out_of_scope_providers,created_by,created_at
		) VALUES($1,$2,$3,ARRAY['openai'],ARRAY['legacy-2'],'integration-test',$4)`,
		secondPolicy, fixture.nodeType, fixture.contract, targetDate); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO provider_inventory_policy_activations(
			node_type,driver_contract_version,policy_version_id,effective_from,
			effective_to,activated_by,created_at
		) VALUES($1,$2,$3,$5,$6,'integration-test',$5),
			($1,$2,$4,$6,NULL,'integration-test',$6)`, fixture.nodeType,
		fixture.contract, fixture.policyID, secondPolicy, targetDate, boundary); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_inventory_monitoring_activations(
			instance_id,effective_from,reason,actor,created_at
		) VALUES($1,$2,'reconciliation','integration-test',$2)`, fixture.instanceID,
		targetDate); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_compaction_runs(
		compaction_run_id,summary_date,instance_id,provider_policy_version,status,
		checksum_version,source_snapshot_count,source_poll_count,
		source_provider_result_count,source_duplicate_count,source_checksum,
		deleted_snapshot_count,created_at,summarized_at,deleting_at,completed_at,updated_at
	) VALUES
		($1,$3::date,$4,$5,'completed',1,0,0,0,0,decode(repeat('00',32),'hex'),0,
		 $3::timestamptz+interval '1 day',$3::timestamptz+interval '1 day 1 second',
		 $3::timestamptz+interval '1 day 2 seconds',$3::timestamptz+interval '1 day 3 seconds',
		 $3::timestamptz+interval '1 day 3 seconds'),
		($2,$3::date,$4,$6,'completed',1,0,0,0,0,decode(repeat('00',32),'hex'),0,
		 $3::timestamptz+interval '1 day',$3::timestamptz+interval '1 day 1 second',
		 $3::timestamptz+interval '1 day 2 seconds',$3::timestamptz+interval '1 day 3 seconds',
		 $3::timestamptz+interval '1 day 3 seconds')`, firstCompaction,
		secondCompaction, targetDate, fixture.instanceID, fixture.policyID, secondPolicy); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_daily_summaries(
		compaction_run_id,summary_date,instance_id,provider,account_key,
		provider_policy_version,first_scheduled_at,last_scheduled_at,
		first_observed_at,last_observed_at,last_basic_status,sample_count,
		disabled_count,unavailable_count,error_count,active_count,unknown_count,
		first_success_count,last_success_count,success_reset_count,
		first_failed_count,last_failed_count,failed_reset_count
	) VALUES
		($1,$3::date,$4,'openai','openai:boundary@example.invalid',$5,
		 $3::timestamptz,$3::timestamptz+interval '11 hours 55 minutes',
		 $3::timestamptz+interval '1 second',$3::timestamptz+interval '11 hours 55 minutes 1 second',
		 'active',2,0,0,0,2,0,100,120,1,1,10,0),
		($2,$3::date,$4,'openai','openai:boundary@example.invalid',$6,
		 $3::timestamptz+interval '12 hours',$3::timestamptz+interval '23 hours 55 minutes',
		 $3::timestamptz+interval '12 hours 1 second',$3::timestamptz+interval '23 hours 55 minutes 1 second',
		 'error',3,0,0,3,0,0,90,130,2,11,3,1)`, firstCompaction,
		secondCompaction, targetDate, fixture.instanceID, fixture.policyID, secondPolicy); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_daily_provider_summaries(
		compaction_run_id,summary_date,instance_id,provider,provider_policy_version,
		expected_poll_count,transport_success_count,contract_valid_count,
		snapshot_complete_count,promotion_applied_count,promotion_skipped_count,
		policy_changed_count,abandoned_count,degraded_count,first_promotion_at,
		last_promotion_at,coverage_numerator,coverage_denominator,coverage_ratio,
		coverage_threshold_basis_points,coverage_status
	) VALUES
		($1,$3::date,$4,'openai',$5,144,144,144,144,144,0,0,0,0,
		 $3::timestamptz,$3::timestamptz+interval '11 hours 55 minutes',144,144,1,9500,'complete'),
		($2,$3::date,$4,'openai',$6,144,100,100,100,100,44,2,0,0,
		 $3::timestamptz+interval '12 hours',$3::timestamptz+interval '23 hours 55 minutes',
		 100,144,round(100::numeric/144,8),9500,'partial')`, firstCompaction,
		secondCompaction, targetDate, fixture.instanceID, fixture.policyID, secondPolicy); err != nil {
		t.Fatal(err)
	}

	var planned int
	if err := database.runtime.QueryRow(ctx, `SELECT
		(public.control_plan_account_inventory_history_v1(20)->>'rollup_runs_created')::integer`).
		Scan(&planned); err != nil || planned != 1 {
		t.Fatalf("boundary planned rollups=%d err=%v", planned, err)
	}
	var rollupRunID, fence uuid.UUID
	if err := database.runtime.QueryRow(ctx, `SELECT rollup_run_id,fencing_token
		FROM public.control_claim_account_inventory_daily_rollup_v1($1,30)`, uuid.New()).
		Scan(&rollupRunID, &fence); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := database.runtime.QueryRow(ctx, `SELECT
		public.control_finalize_account_inventory_daily_rollup_v1($1,$2)->>'status'`,
		rollupRunID, fence).Scan(&status); err != nil || status != "completed" {
		t.Fatalf("boundary finalize status=%s err=%v", status, err)
	}
	var sampleCount, successResets, failedResets int
	var lastStatus string
	var firstSuccess, lastSuccess, firstFailed, lastFailed int64
	if err := database.owner.QueryRow(ctx, `SELECT sample_count,last_basic_status,
		first_success_count,last_success_count,success_reset_count,
		first_failed_count,last_failed_count,failed_reset_count
	FROM account_inventory_daily_account_rollups WHERE rollup_run_id=$1`, rollupRunID).
		Scan(&sampleCount, &lastStatus, &firstSuccess, &lastSuccess, &successResets,
			&firstFailed, &lastFailed, &failedResets); err != nil {
		t.Fatal(err)
	}
	if sampleCount != 5 || lastStatus != "error" || firstSuccess != 100 ||
		lastSuccess != 130 || successResets != 4 || firstFailed != 1 ||
		lastFailed != 3 || failedResets != 1 {
		t.Fatalf("boundary account samples=%d status=%s success=%d/%d resets=%d failed=%d/%d resets=%d",
			sampleCount, lastStatus, firstSuccess, lastSuccess, successResets,
			firstFailed, lastFailed, failedResets)
	}
	var applied, expected, policyChanged int
	var coverageStatus string
	if err := database.owner.QueryRow(ctx, `SELECT coverage_numerator,
		coverage_denominator,policy_changed_count,coverage_status
	FROM account_inventory_daily_provider_rollups WHERE rollup_run_id=$1`, rollupRunID).
		Scan(&applied, &expected, &policyChanged, &coverageStatus); err != nil {
		t.Fatal(err)
	}
	if applied != 244 || expected != 288 || policyChanged != 2 || coverageStatus != "partial" {
		t.Fatalf("boundary Provider applied=%d expected=%d policy_changed=%d status=%s",
			applied, expected, policyChanged, coverageStatus)
	}
}

func TestHistoryMetricsBacklogIncludesUnplannedEligibleSnapshotsAndDrains(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleSchemaFixture(t, ctx, database)
	targetDate := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -4)
	slot := targetDate.Add(12 * time.Hour)
	if _, err := database.owner.Exec(ctx, `UPDATE provider_inventory_policy_activations
		SET effective_from=$1,created_at=$1 WHERE policy_version_id=$2`, targetDate, fixture.policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_inventory_monitoring_activations(
		instance_id,effective_from,reason,actor,created_at
	) VALUES($1,$2,'reconciliation','metrics-backlog-test',$2)`, fixture.instanceID, targetDate); err != nil {
		t.Fatal(err)
	}
	pollID := uuid.New()
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_poll_runs(
		poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,
		provider_policy_version,status,attempt_count,max_attempts,poll_start_grace_seconds,
		created_at,first_started_at,last_started_at,finalized_at,observed_at,
		transport_success,response_shape_valid,contract_valid,inventory_mode,
		node_identity_complete,snapshot_complete,degraded,result,reason,
		source_record_count,identifiable_record_count,unidentified_record_count,
		unsupported_provider_count,out_of_scope_provider_count,node_version,node_commit
	) VALUES(
		$1,$2,$3,$4,$5::timestamptz,$6,'finalized',1,2,299,
		$5::timestamptz+interval '1 second',$5::timestamptz+interval '2 seconds',
		$5::timestamptz+interval '2 seconds',$5::timestamptz+interval '4 seconds',
		$5::timestamptz+interval '3 seconds',
		true,true,true,'runtime',true,true,false,'success','none',
		1,1,0,0,0,'unknown','unknown'
	)`, pollID, fixture.instanceID, fixture.nodeType, fixture.contract, slot, fixture.policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_poll_provider_results
		DISABLE TRIGGER account_inventory_poll_provider_results_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_snapshot_items
		DISABLE TRIGGER account_inventory_snapshot_items_insert_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_poll_provider_results(
		poll_run_id,provider,identifiable_count,missing_identity_count,
		duplicate_identity_count,identity_complete,snapshot_complete,degraded,reason,
		promotion_applied,promotion_skipped_reason
	) VALUES($1,'openai',1,0,0,true,true,false,'complete',true,NULL)`, pollID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_snapshot_items(
		poll_run_id,instance_id,provider,account_key,normalized_email,basic_status,
		success_count,failed_count,recent_request_count,observed_at
	) VALUES($1,$2,'openai','openai:metrics-backlog@example.invalid',
		'metrics-backlog@example.invalid','active',1,0,1,$3::timestamptz+interval '3 seconds')`,
		pollID, fixture.instanceID, slot); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_poll_provider_results
		ENABLE TRIGGER account_inventory_poll_provider_results_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_snapshot_items
		ENABLE TRIGGER account_inventory_snapshot_items_insert_guard`); err != nil {
		t.Fatal(err)
	}
	readBacklog := func() int64 {
		t.Helper()
		var backlog int64
		if err := database.runtime.QueryRow(ctx, `SELECT
			(public.control_account_inventory_history_metrics_snapshot_v1()
			 ->>'delete_backlog_rows')::bigint`).Scan(&backlog); err != nil {
			t.Fatal("runtime metrics backlog is unavailable")
		}
		return backlog
	}
	if backlog := readBacklog(); backlog != 1 {
		t.Fatalf("unplanned eligible snapshot backlog=%d want=1", backlog)
	}
	var runID uuid.UUID
	if err := database.owner.QueryRow(ctx, `INSERT INTO account_inventory_compaction_runs(
		summary_date,instance_id,provider_policy_version
	) VALUES($1,$2,$3) RETURNING compaction_run_id`, targetDate,
		fixture.instanceID, fixture.policyID).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	var claimedID, fence uuid.UUID
	if err := database.runtime.QueryRow(ctx, `SELECT compaction_run_id,fencing_token
		FROM public.control_claim_account_inventory_compaction_v1($1,30)`, uuid.New()).
		Scan(&claimedID, &fence); err != nil || claimedID != runID {
		t.Fatalf("metrics backlog claim=%s want=%s err=%v", claimedID, runID, err)
	}
	var checksumHex string
	if err := database.runtime.QueryRow(ctx, `SELECT
		public.control_summarize_account_inventory_compaction_v1($1,$2)->>'source_checksum_hex'`,
		runID, fence).Scan(&checksumHex); err != nil || len(checksumHex) != 64 {
		t.Fatalf("metrics backlog summarize checksum=%q err=%v", checksumHex, err)
	}
	var deleted, remaining int
	if err := database.runtime.QueryRow(ctx, `WITH result AS (
		SELECT public.control_delete_account_inventory_snapshot_batch_v1($1,$2,1) AS value
	) SELECT (value->>'deleted_count')::integer,(value->>'remaining_count')::integer FROM result`,
		runID, fence).Scan(&deleted, &remaining); err != nil || deleted != 1 || remaining != 0 {
		t.Fatalf("metrics backlog delete=%d remaining=%d err=%v", deleted, remaining, err)
	}
	var status string
	if err := database.runtime.QueryRow(ctx, `SELECT
		public.control_complete_account_inventory_compaction_v1(
			$1,$2,decode($3,'hex'))->>'status'`, runID, fence, checksumHex).Scan(&status); err != nil ||
		status != "completed" {
		t.Fatalf("metrics backlog complete=%q err=%v", status, err)
	}
	if backlog := readBacklog(); backlog != 0 {
		t.Fatalf("drained eligible snapshot backlog=%d want=0", backlog)
	}
}

func TestHistoryMetricsOldestIncludesEligibleSourceWithoutPlannedRun(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleSchemaFixture(t, ctx, database)
	targetDate := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -4)
	slot := targetDate.Add(12 * time.Hour)
	if _, err := database.owner.Exec(ctx, `UPDATE provider_inventory_policy_activations
		SET effective_from=$1,created_at=$1 WHERE policy_version_id=$2`, targetDate, fixture.policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_inventory_monitoring_activations(
		instance_id,effective_from,reason,actor,created_at
	) VALUES($1,$2,'reconciliation','metrics-test',$2)`, fixture.instanceID, targetDate); err != nil {
		t.Fatal(err)
	}

	readOldest := func() float64 {
		t.Helper()
		var encoded []byte
		if err := database.runtime.QueryRow(ctx,
			`SELECT public.control_account_inventory_history_metrics_snapshot_v1()`).Scan(&encoded); err != nil {
			t.Fatal("runtime metrics snapshot is unavailable")
		}
		var snapshot struct {
			Oldest float64 `json:"oldest_eligible_unfinished_seconds"`
		}
		if err := json.Unmarshal(encoded, &snapshot); err != nil {
			t.Fatal("runtime metrics snapshot has an invalid shape")
		}
		return snapshot.Oldest
	}
	if oldest := readOldest(); oldest < 72*time.Hour.Seconds() {
		t.Fatalf("zero-poll activation candidate oldest=%f", oldest)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_poll_runs(
		instance_id,node_type,driver_contract_version,scheduled_at,
		provider_policy_version,max_attempts,poll_start_grace_seconds,created_at
	) VALUES($1,$2,$3,$4,$5,2,299,clock_timestamp())`, fixture.instanceID,
		fixture.nodeType, fixture.contract, slot, fixture.policyID); err != nil {
		t.Fatal(err)
	}
	var runs int
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM account_inventory_compaction_runs
		WHERE summary_date=$1 AND instance_id=$2`, targetDate, fixture.instanceID).Scan(&runs); err != nil || runs != 0 {
		t.Fatalf("metrics test unexpectedly planned history runs=%d err=%v", runs, err)
	}
	if oldest := readOldest(); oldest < 72*time.Hour.Seconds() {
		t.Fatalf("source-backed unplanned day oldest=%f", oldest)
	}
}

func TestAccountInventoryHistoryRetentionBatchesConservationAndCurrentQuery(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleSchemaFixture(t, ctx, database)
	if _, err := database.owner.Exec(ctx, `INSERT INTO driver_capabilities(
		node_type,driver_contract_version,capability
	) VALUES($1,$2,'management_account_inventory_read')`, fixture.nodeType, fixture.contract); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO node_capabilities(
		instance_id,node_type,driver_contract_version,capability
	) VALUES($1,$2,$3,'management_account_inventory_read')`,
		fixture.instanceID, fixture.nodeType, fixture.contract); err != nil {
		t.Fatal(err)
	}
	if err := database.owner.QueryRow(ctx, `SELECT date_bin(
		interval '5 minutes',clock_timestamp()-interval '31 days',timestamptz '1970-01-01'
	)`).Scan(&fixture.baseSlot); err != nil {
		t.Fatal(err)
	}
	firstPoll := fixture.finalize(t, ctx, database, []lifecycleAccount{{
		email: "retention@example.invalid", successCount: 10,
	}})
	secondPoll := fixture.finalize(t, ctx, database, []lifecycleAccount{{
		email: "retention@example.invalid", successCount: 11,
	}})
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_snapshot_items
		DISABLE TRIGGER account_inventory_snapshot_items_immutable`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `DELETE FROM account_inventory_snapshot_items
		WHERE poll_run_id=ANY($1::uuid[])`, []uuid.UUID{firstPoll, secondPoll}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_snapshot_items
		ENABLE TRIGGER account_inventory_snapshot_items_immutable`); err != nil {
		t.Fatal(err)
	}

	summaryDate := fixture.baseSlot.UTC().Truncate(24 * time.Hour)
	var compactionID uuid.UUID
	if err := database.owner.QueryRow(ctx, `INSERT INTO account_inventory_compaction_runs(
		summary_date,instance_id,provider_policy_version,status,checksum_version,
		source_snapshot_count,source_poll_count,source_provider_result_count,
		source_duplicate_count,source_checksum,deleted_snapshot_count,created_at,
		summarized_at,deleting_at,completed_at,updated_at
	) VALUES($1,$2,$3,'completed',1,2,2,2,0,decode(repeat('11',32),'hex'),2,
		$1::date+interval '1 day',$1::date+interval '1 day 1 hour',
		$1::date+interval '1 day 2 hours',$1::date+interval '1 day 3 hours',
		$1::date+interval '1 day 3 hours') RETURNING compaction_run_id`,
		summaryDate, fixture.instanceID, fixture.policyID).Scan(&compactionID); err != nil {
		t.Fatal(err)
	}
	var rollupID uuid.UUID
	if err := database.owner.QueryRow(ctx, `INSERT INTO account_inventory_daily_rollup_runs(
		summary_date,instance_id,status,completed_fencing_token,expected_segment_count,
		completed_segment_count,checksum_version,segment_checksum,created_at,completed_at,updated_at
	) VALUES($1,$2,'completed',$3,1,1,1,decode(repeat('22',32),'hex'),
		$1::date+interval '1 hour',$1::date+interval '2 hours',
		$1::date+interval '2 hours') RETURNING rollup_run_id`,
		summaryDate, fixture.instanceID, uuid.New()).Scan(&rollupID); err != nil {
		t.Fatal(err)
	}

	var beforeQuery string
	if err := database.runtime.QueryRow(ctx, `SELECT coalesce(jsonb_agg(to_jsonb(row_value)
		ORDER BY row_value.account_key),'[]'::jsonb)::text
		FROM public.control_query_current_account_inventory_v1($1,'','','','','',10) AS row_value`,
		fixture.instanceID).Scan(&beforeQuery); err != nil {
		t.Fatal(err)
	}

	ownerTransaction, err := database.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ownerTransaction.Exec(ctx, `SELECT set_config(
		'relay_control.history_poll_retention_delete',$1,true)`, firstPoll.String()); err != nil {
		_ = ownerTransaction.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err := ownerTransaction.Exec(ctx, `DELETE FROM account_inventory_poll_runs
		WHERE poll_run_id=$1`, firstPoll); err == nil {
		_ = ownerTransaction.Rollback(ctx)
		t.Fatal("migration owner forged the poll retention gate")
	} else {
		requireHistorySQLState(t, err, "23514")
	}
	_ = ownerTransaction.Rollback(ctx)

	if _, err := database.owner.Exec(ctx, `CREATE FUNCTION public.test_reject_retention_audit()
		RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
			IF NEW.action='account_inventory_history.retention_delete_batch' THEN
				RAISE EXCEPTION 'synthetic retention audit failure';
			END IF; RETURN NEW; END $$;
		CREATE TRIGGER zz_test_reject_retention_audit BEFORE INSERT ON audit_logs
		FOR EACH ROW EXECUTE FUNCTION public.test_reject_retention_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.runtime.Exec(ctx,
		`SELECT public.control_delete_account_inventory_poll_retention_v1(1)`); err == nil {
		t.Fatal("poll retention ignored an audit failure")
	}
	var polls, deletedPolls int
	if err := database.owner.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM account_inventory_poll_runs WHERE poll_run_id=ANY($1::uuid[])),
		deleted_poll_count FROM account_inventory_compaction_runs WHERE compaction_run_id=$2`,
		[]uuid.UUID{firstPoll, secondPoll}, compactionID).Scan(&polls, &deletedPolls); err != nil {
		t.Fatal(err)
	}
	if polls != 2 || deletedPolls != 0 {
		t.Fatalf("audit rollback polls=%d deleted_progress=%d", polls, deletedPolls)
	}
	if _, err := database.owner.Exec(ctx, `DROP TRIGGER zz_test_reject_retention_audit ON audit_logs;
		DROP FUNCTION public.test_reject_retention_audit()`); err != nil {
		t.Fatal(err)
	}

	for batch := 1; batch <= 2; batch++ {
		var processed, deleted int
		if err := database.runtime.QueryRow(ctx, `WITH result AS (
			SELECT public.control_delete_account_inventory_poll_retention_v1(1) AS value
		) SELECT (value->>'processed_count')::integer,
			(value->>'deleted_row_count')::integer FROM result`).Scan(&processed, &deleted); err != nil {
			t.Fatal(err)
		}
		if processed != 1 || deleted != 2 {
			t.Fatalf("poll retention batch=%d processed=%d deleted=%d", batch, processed, deleted)
		}
	}
	var afterQuery string
	if err := database.runtime.QueryRow(ctx, `SELECT coalesce(jsonb_agg(to_jsonb(row_value)
		ORDER BY row_value.account_key),'[]'::jsonb)::text
		FROM public.control_query_current_account_inventory_v1($1,'','','','','',10) AS row_value`,
		fixture.instanceID).Scan(&afterQuery); err != nil {
		t.Fatal(err)
	}
	if afterQuery != beforeQuery {
		t.Fatalf("current query changed across poll retention\nbefore=%s\nafter=%s", beforeQuery, afterQuery)
	}
	var mismatchScheduled time.Time
	if err := database.owner.QueryRow(ctx, `SELECT date_bin(
		interval '5 minutes',clock_timestamp()-interval '32 days',timestamptz '1970-01-01'
	)`).Scan(&mismatchScheduled); err != nil {
		t.Fatal(err)
	}
	mismatchPoll := uuid.New()
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_poll_runs(
		poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,
		provider_policy_version,status,created_at,abandoned_at,execution_reason
	) VALUES($1,$2,$3,$4,$5,$6,'abandoned',clock_timestamp(),clock_timestamp(),
		'poll_start_grace_expired')`, mismatchPoll, fixture.instanceID, fixture.nodeType,
		fixture.contract, mismatchScheduled, fixture.policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_compaction_runs(
		summary_date,instance_id,provider_policy_version,status,checksum_version,
		source_snapshot_count,source_poll_count,source_provider_result_count,
		source_duplicate_count,source_checksum,deleted_snapshot_count,created_at,
		summarized_at,deleting_at,completed_at,updated_at
	) VALUES(($1::timestamptz AT TIME ZONE 'UTC')::date,$2,$3,'completed',1,0,2,0,0,
		decode(repeat('44',32),'hex'),0,$1,$1,$1,$1,$1)`, mismatchScheduled,
		fixture.instanceID, fixture.policyID); err != nil {
		t.Fatal(err)
	}
	var mismatchProcessed int
	if err := database.runtime.QueryRow(ctx, `SELECT
		(public.control_delete_account_inventory_poll_retention_v1(1)
		 ->>'processed_count')::integer`).Scan(&mismatchProcessed); err != nil || mismatchProcessed != 0 {
		t.Fatalf("unconserved poll lineage processed=%d err=%v", mismatchProcessed, err)
	}
	var mismatchRemains bool
	if err := database.owner.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM account_inventory_poll_runs WHERE poll_run_id=$1)`, mismatchPoll).
		Scan(&mismatchRemains); err != nil || !mismatchRemains {
		t.Fatalf("unconserved poll evidence remains=%t err=%v", mismatchRemains, err)
	}
	var newerThanCutoff time.Time
	if err := database.owner.QueryRow(ctx, `SELECT date_bin(
		interval '5 minutes',clock_timestamp()-interval '30 days',timestamptz '1970-01-01'
	)+interval '5 minutes'`).Scan(&newerThanCutoff); err != nil {
		t.Fatal(err)
	}
	boundaryPoll := uuid.New()
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_poll_runs(
		poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,
		provider_policy_version,status,created_at,abandoned_at,execution_reason
	) VALUES($1,$2,$3,$4,$5,$6,'abandoned',clock_timestamp(),clock_timestamp(),
		'poll_start_grace_expired')`, boundaryPoll, fixture.instanceID, fixture.nodeType,
		fixture.contract, newerThanCutoff, fixture.policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_compaction_runs(
		summary_date,instance_id,provider_policy_version,status,checksum_version,
		source_snapshot_count,source_poll_count,source_provider_result_count,
		source_duplicate_count,source_checksum,deleted_snapshot_count,created_at,
		summarized_at,deleting_at,completed_at,updated_at
	) VALUES(($1::timestamptz AT TIME ZONE 'UTC')::date,$2,$3,'completed',1,0,1,0,0,
		decode(repeat('33',32),'hex'),0,clock_timestamp(),clock_timestamp(),
		clock_timestamp(),clock_timestamp(),clock_timestamp())`, newerThanCutoff,
		fixture.instanceID, fixture.policyID); err != nil {
		t.Fatal(err)
	}
	var boundaryProcessed int
	if err := database.runtime.QueryRow(ctx, `SELECT
		(public.control_delete_account_inventory_poll_retention_v1(1)
		 ->>'processed_count')::integer`).Scan(&boundaryProcessed); err != nil || boundaryProcessed != 0 {
		t.Fatalf("poll newer than the exact 30-day cutoff processed=%d err=%v",
			boundaryProcessed, err)
	}
	var currentPointers, deletedProviderResults, retentionAuditRows int
	if err := database.owner.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM account_inventory_provider_states
		 WHERE instance_id=$1 AND current_poll_run_id IS NOT NULL)
		+(SELECT count(*) FROM account_inventory
		  WHERE instance_id=$1 AND current_poll_run_id IS NOT NULL),
		deleted_provider_result_count,
		(SELECT count(*) FROM audit_logs
		 WHERE action='account_inventory_history.retention_delete_batch'
		   AND details->>'phase'='retention_poll')
		FROM account_inventory_compaction_runs WHERE compaction_run_id=$2`,
		fixture.instanceID, compactionID).Scan(
		&currentPointers, &deletedProviderResults, &retentionAuditRows); err != nil {
		t.Fatal(err)
	}
	if currentPointers != 0 || deletedProviderResults != 2 || retentionAuditRows != 2 {
		t.Fatalf("retention pointers=%d provider-progress=%d audits=%d",
			currentPointers, deletedProviderResults, retentionAuditRows)
	}

	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_daily_summaries(
		compaction_run_id,summary_date,instance_id,provider,account_key,
		provider_policy_version,first_scheduled_at,last_scheduled_at,
		first_observed_at,last_observed_at,last_basic_status,sample_count,
		disabled_count,unavailable_count,error_count,active_count,unknown_count,
		first_success_count,last_success_count,success_reset_count,
		first_failed_count,last_failed_count,failed_reset_count,created_at
	) VALUES($1,$2,$3,'openai','openai:retention@example.invalid',$4,
		$2::date+interval '1 hour',$2::date+interval '1 hour',
		$2::date+interval '1 hour 1 second',$2::date+interval '1 hour 1 second',
		'active',1,0,0,0,1,0,11,11,0,0,0,0,$2::date+interval '2 days')`,
		compactionID, summaryDate, fixture.instanceID, fixture.policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_daily_provider_summaries(
		compaction_run_id,summary_date,instance_id,provider,provider_policy_version,
		expected_poll_count,transport_success_count,contract_valid_count,
		snapshot_complete_count,promotion_applied_count,promotion_skipped_count,
		policy_changed_count,abandoned_count,degraded_count,first_promotion_at,
		last_promotion_at,coverage_numerator,coverage_denominator,coverage_ratio,
		coverage_threshold_basis_points,coverage_status,created_at
	) VALUES($1,$2,$3,'openai',$4,1,1,1,1,1,0,0,0,0,
		$2::date+interval '1 hour',$2::date+interval '1 hour',1,1,1,9500,
		'complete',$2::date+interval '2 days')`,
		compactionID, summaryDate, fixture.instanceID, fixture.policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_daily_account_rollups(
		rollup_run_id,summary_date,instance_id,provider,account_key,
		first_scheduled_at,last_scheduled_at,first_observed_at,last_observed_at,
		last_basic_status,sample_count,disabled_count,unavailable_count,error_count,
		active_count,unknown_count,first_success_count,last_success_count,
		success_reset_count,first_failed_count,last_failed_count,failed_reset_count,created_at
	) VALUES($1,$2,$3,'openai','openai:retention@example.invalid',
		$2::date+interval '1 hour',$2::date+interval '1 hour',
		$2::date+interval '1 hour 1 second',$2::date+interval '1 hour 1 second',
		'active',1,0,0,0,1,0,11,11,0,0,0,0,$2::date+interval '2 days')`,
		rollupID, summaryDate, fixture.instanceID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_daily_provider_rollups(
		rollup_run_id,summary_date,instance_id,provider,expected_poll_count,
		transport_success_count,contract_valid_count,snapshot_complete_count,
		promotion_applied_count,promotion_skipped_count,policy_changed_count,
		abandoned_count,degraded_count,first_promotion_at,last_promotion_at,
		coverage_numerator,coverage_denominator,coverage_ratio,
		coverage_threshold_basis_points,coverage_status,created_at
	) VALUES($1,$2,$3,'openai',1,1,1,1,1,0,0,0,0,
		$2::date+interval '1 hour',$2::date+interval '1 hour',1,1,1,9500,
		'complete',$2::date+interval '2 days')`,
		rollupID, summaryDate, fixture.instanceID); err != nil {
		t.Fatal(err)
	}
	var processed int
	for batch := 1; batch <= 4; batch++ {
		if err := database.runtime.QueryRow(ctx, `SELECT
			(public.control_delete_account_inventory_rollup_row_retention_v1(1)
			 ->>'processed_count')::integer`).Scan(&processed); err != nil || processed != 1 {
			t.Fatalf("rollup row retention batch=%d processed=%d err=%v", batch, processed, err)
		}
		var segmentAccounts, segmentProviders, finalAccounts, finalProviders int
		if err := database.owner.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM account_inventory_daily_summaries
			 WHERE compaction_run_id=$1),
			(SELECT count(*) FROM account_inventory_daily_provider_summaries
			 WHERE compaction_run_id=$1),
			(SELECT count(*) FROM account_inventory_daily_account_rollups
			 WHERE rollup_run_id=$2),
			(SELECT count(*) FROM account_inventory_daily_provider_rollups
			 WHERE rollup_run_id=$2)`, compactionID, rollupID).Scan(
			&segmentAccounts, &segmentProviders, &finalAccounts, &finalProviders); err != nil {
			t.Fatal(err)
		}
		want := [4][4]int{{0, 1, 1, 1}, {0, 0, 1, 1}, {0, 0, 0, 1}, {0, 0, 0, 0}}
		got := [4]int{segmentAccounts, segmentProviders, finalAccounts, finalProviders}
		if got != want[batch-1] {
			t.Fatalf("rollup row stage batch=%d got=%v want=%v", batch, got, want[batch-1])
		}
		if batch < 4 {
			if err := database.runtime.QueryRow(ctx, `SELECT
				(public.control_delete_account_inventory_rollup_run_retention_v1(1)
				 ->>'processed_count')::integer`).Scan(&processed); err != nil || processed != 0 {
				t.Fatalf("rollup run deleted before child rows drained: processed=%d err=%v",
					processed, err)
			}
		}
	}
	if _, err := database.owner.Exec(ctx, `CREATE FUNCTION public.test_reject_retired_marker_audit()
		RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
			IF NEW.action='account_inventory_history.retention_delete_batch'
			   AND NEW.details->>'phase'='retention_rollup_run' THEN
				RAISE EXCEPTION 'synthetic retired marker audit failure';
			END IF; RETURN NEW; END $$;
		CREATE TRIGGER zz_test_reject_retired_marker_audit BEFORE INSERT ON audit_logs
		FOR EACH ROW EXECUTE FUNCTION public.test_reject_retired_marker_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.runtime.Exec(ctx,
		`SELECT public.control_delete_account_inventory_rollup_run_retention_v1(1)`); err == nil {
		t.Fatal("retired marker insertion ignored an audit failure")
	}
	var markerAfterRollback, rollupAfterRollback int
	if err := database.owner.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM account_inventory_history_retired_days
		 WHERE summary_date=$1 AND instance_id=$2),
		(SELECT count(*) FROM account_inventory_daily_rollup_runs WHERE rollup_run_id=$3)`,
		summaryDate, fixture.instanceID, rollupID).Scan(
		&markerAfterRollback, &rollupAfterRollback); err != nil {
		t.Fatal(err)
	}
	if markerAfterRollback != 0 || rollupAfterRollback != 1 {
		t.Fatalf("retired marker audit rollback marker=%d rollup=%d",
			markerAfterRollback, rollupAfterRollback)
	}
	if _, err := database.owner.Exec(ctx, `DROP TRIGGER zz_test_reject_retired_marker_audit ON audit_logs;
		DROP FUNCTION public.test_reject_retired_marker_audit()`); err != nil {
		t.Fatal(err)
	}
	if err := database.runtime.QueryRow(ctx, `SELECT
		(public.control_delete_account_inventory_rollup_run_retention_v1(1)
		 ->>'processed_count')::integer`).Scan(&processed); err != nil || processed != 1 {
		t.Fatalf("rollup run retention processed=%d err=%v", processed, err)
	}
	if err := database.runtime.QueryRow(ctx, `SELECT
		(public.control_delete_account_inventory_compaction_run_retention_v1(1)
		 ->>'processed_count')::integer`).Scan(&processed); err != nil || processed != 1 {
		t.Fatalf("compaction run retention processed=%d err=%v", processed, err)
	}
	var retainedRuns int
	if err := database.owner.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM account_inventory_daily_rollup_runs WHERE rollup_run_id=$1)
		+(SELECT count(*) FROM account_inventory_compaction_runs WHERE compaction_run_id=$2)`,
		rollupID, compactionID).Scan(&retainedRuns); err != nil || retainedRuns != 0 {
		t.Fatalf("retained terminal runs=%d err=%v", retainedRuns, err)
	}
	var plannedCompactions, plannedRollups, retiredLineage int
	if err := database.runtime.QueryRow(ctx, `WITH planned AS (
		SELECT public.control_plan_account_inventory_history_v1(100) AS value
	) SELECT (value->>'compaction_runs_created')::integer,
		(value->>'rollup_runs_created')::integer FROM planned`).Scan(
		&plannedCompactions, &plannedRollups); err != nil {
		t.Fatal(err)
	}
	if err := database.owner.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM account_inventory_compaction_runs
		 WHERE summary_date=$1 AND instance_id=$2 AND provider_policy_version=$3)
		+(SELECT count(*) FROM account_inventory_daily_rollup_runs
		  WHERE summary_date=$1 AND instance_id=$2)`,
		summaryDate, fixture.instanceID, fixture.policyID).Scan(&retiredLineage); err != nil {
		t.Fatal(err)
	}
	if plannedCompactions != 0 || plannedRollups != 0 || retiredLineage != 0 {
		t.Fatalf("planner resurrected retained lineage: compactions=%d rollups=%d lineage=%d",
			plannedCompactions, plannedRollups, retiredLineage)
	}
}

func TestAccountInventoryHistoryPlannerSerializesRetentionBoundary(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	transaction, err := database.runtime.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var planned []byte
	if err := transaction.QueryRow(ctx,
		`SELECT public.control_plan_account_inventory_history_v1(1)`).Scan(&planned); err != nil {
		_ = transaction.Rollback(ctx)
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		var retained []byte
		result <- database.runtime.QueryRow(ctx,
			`SELECT public.control_delete_account_inventory_poll_retention_v1(1)`).Scan(&retained)
	}()
	select {
	case err := <-result:
		_ = transaction.Rollback(ctx)
		t.Fatalf("retention did not wait for the planner boundary lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	if err := transaction.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("retention did not continue after planner transaction ended")
	}
}

func TestAccountInventoryHistoryPlannerLimitOneMakesPersistentProgress(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleSchemaFixture(t, ctx, database)
	var eligibleLastDate time.Time
	if err := database.owner.QueryRow(ctx, `SELECT
		((clock_timestamp()-interval '72 hours') AT TIME ZONE 'UTC')::date-1`).
		Scan(&eligibleLastDate); err != nil {
		t.Fatal(err)
	}
	firstDate := eligibleLastDate.AddDate(0, 0, -1)
	endDate := eligibleLastDate.AddDate(0, 0, 1)
	firstMidpoint := firstDate.Add(12 * time.Hour)
	secondMidpoint := eligibleLastDate.Add(12 * time.Hour)
	secondPolicyID := uuid.New()
	if _, err := database.owner.Exec(ctx, `INSERT INTO provider_inventory_policy_versions(
		policy_version_id,node_type,driver_contract_version,active_providers,
		out_of_scope_providers,created_by,created_at
	) VALUES($1,$2,$3,ARRAY['anthropic'],ARRAY[]::text[],
		'planner-limit-progress-test',$4)`, secondPolicyID, fixture.nodeType,
		fixture.contract, firstDate); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `UPDATE provider_inventory_policy_activations
		SET effective_from=$1,effective_to=$2,created_at=$1
		WHERE policy_version_id=$3`, firstDate, firstMidpoint, fixture.policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO provider_inventory_policy_activations(
		node_type,driver_contract_version,policy_version_id,effective_from,effective_to,
		activated_by,created_at
	) VALUES
		($1,$2,$3,$4,$5,'planner-limit-progress-test',$4),
		($1,$2,$6,$5,$7,'planner-limit-progress-test',$5)`, fixture.nodeType,
		fixture.contract, secondPolicyID, firstMidpoint, secondMidpoint,
		fixture.policyID, endDate); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_inventory_monitoring_activations(
		instance_id,effective_from,effective_to,reason,actor,end_reason,end_actor,
		end_recorded_at,created_at
	) VALUES($1,$2,$3,'reconciliation','planner-limit-progress-test',
		'reconciliation','planner-limit-progress-test',$3,$2)`,
		fixture.instanceID, firstDate, endDate); err != nil {
		t.Fatal(err)
	}
	plan := func() (int, int) {
		t.Helper()
		var compactions, rollups int
		if err := database.runtime.QueryRow(ctx, `WITH planned AS (
			SELECT public.control_plan_account_inventory_history_v1(1) AS value
		) SELECT (value->>'compaction_runs_created')::integer,
			(value->>'rollup_runs_created')::integer FROM planned`).Scan(
			&compactions, &rollups); err != nil {
			t.Fatal(err)
		}
		return compactions, rollups
	}
	completeOne := func(call int) {
		t.Helper()
		var runID, fence uuid.UUID
		if err := database.runtime.QueryRow(ctx, `SELECT compaction_run_id,fencing_token
			FROM public.control_claim_account_inventory_compaction_v1($1,30)`, uuid.New()).
			Scan(&runID, &fence); err != nil {
			t.Fatal(err)
		}
		var checksumHex string
		if err := database.runtime.QueryRow(ctx, `SELECT
			public.control_summarize_account_inventory_compaction_v1($1,$2)->>'source_checksum_hex'`,
			runID, fence).Scan(&checksumHex); err != nil || len(checksumHex) != 64 {
			t.Fatalf("limit-one summarize call=%d checksum=%q err=%v", call, checksumHex, err)
		}
		var status string
		if err := database.runtime.QueryRow(ctx, `SELECT
			public.control_delete_account_inventory_snapshot_batch_v1($1,$2,1)->>'status'`,
			runID, fence).Scan(&status); err != nil || status != "deleting" {
			t.Fatalf("limit-one delete call=%d status=%q err=%v", call, status, err)
		}
		if err := database.runtime.QueryRow(ctx, `SELECT
			public.control_complete_account_inventory_compaction_v1(
				$1,$2,decode($3,'hex'))->>'status'`, runID, fence, checksumHex).
			Scan(&status); err != nil || status != "completed" {
			t.Fatalf("limit-one complete call=%d status=%q err=%v", call, status, err)
		}
	}
	for call, want := range [][2]int{{1, 0}, {1, 0}, {1, 1}, {1, 0}} {
		compactions, rollups := plan()
		if got := [2]int{compactions, rollups}; got != want {
			t.Fatalf("limit-one interleaved plan call=%d got=%v want=%v", call+1, got, want)
		}
		completeOne(call + 1)
	}
	for call, want := range [][2]int{{0, 1}, {0, 0}, {0, 0}} {
		compactions, rollups := plan()
		if got := [2]int{compactions, rollups}; got != want {
			t.Fatalf("limit-one terminal plan call=%d got=%v want=%v", call+5, got, want)
		}
	}
	var compactionCount, compactionDayCount, completeDays int
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM (
			SELECT summary_date,count(*) AS keys_per_day
			FROM account_inventory_compaction_runs WHERE instance_id=$1
			GROUP BY summary_date
		) AS per_day WHERE keys_per_day=2`, fixture.instanceID).Scan(&completeDays); err != nil {
		t.Fatal(err)
	}
	if err := database.owner.QueryRow(ctx, `SELECT count(*),count(DISTINCT summary_date)
		FROM account_inventory_compaction_runs WHERE instance_id=$1`, fixture.instanceID).
		Scan(&compactionCount, &compactionDayCount); err != nil || compactionCount != 4 ||
		compactionDayCount != 2 || completeDays != 2 {
		t.Fatalf("limit-one compaction keys=%d days=%d complete-days=%d err=%v",
			compactionCount, compactionDayCount, completeDays, err)
	}
	var rollupCount, rollupDayCount int
	if err := database.owner.QueryRow(ctx, `SELECT count(*),count(DISTINCT summary_date)
		FROM account_inventory_daily_rollup_runs WHERE instance_id=$1`, fixture.instanceID).
		Scan(&rollupCount, &rollupDayCount); err != nil || rollupCount != 2 || rollupDayCount != 2 {
		t.Fatalf("limit-one rollup keys=%d days=%d err=%v", rollupCount, rollupDayCount, err)
	}
}

func TestAccountInventoryHistoryRetiredDaySerializesLatePollInsertion(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	if _, err := database.owner.Exec(ctx, `CREATE FUNCTION public.test_runtime_insert_retention_race_poll(
		p_instance_id uuid,p_node_type text,p_contract text,p_scheduled_at timestamptz,p_policy_id uuid
	) RETURNS uuid LANGUAGE sql SECURITY DEFINER
	SET search_path=pg_catalog SET TimeZone='UTC'
	AS $function$
		INSERT INTO public.account_inventory_poll_runs(
			instance_id,node_type,driver_contract_version,scheduled_at,provider_policy_version
		) VALUES(p_instance_id,p_node_type,p_contract,p_scheduled_at,p_policy_id)
		RETURNING poll_run_id
	$function$;
	ALTER FUNCTION public.test_runtime_insert_retention_race_poll(uuid,text,text,timestamptz,uuid)
		OWNER TO relay_control_migrator;
	REVOKE ALL ON FUNCTION public.test_runtime_insert_retention_race_poll(uuid,text,text,timestamptz,uuid)
		FROM PUBLIC;
	GRANT EXECUTE ON FUNCTION public.test_runtime_insert_retention_race_poll(uuid,text,text,timestamptz,uuid)
		TO relay_control_runtime`); err != nil {
		t.Fatal(err)
	}
	type retiredDayFixture struct {
		lifecycle   *lifecycleSchemaFixture
		summaryDate time.Time
		slot        time.Time
		rollupID    uuid.UUID
	}
	lifecycle := newLifecycleSchemaFixture(t, ctx, database)
	prepare := func(daysAgo int) retiredDayFixture {
		t.Helper()
		summaryDate := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -daysAgo)
		var rollupID uuid.UUID
		if err := database.owner.QueryRow(ctx, `INSERT INTO account_inventory_daily_rollup_runs(
			summary_date,instance_id,status,completed_fencing_token,expected_segment_count,
			completed_segment_count,checksum_version,segment_checksum,created_at,completed_at,updated_at
		) VALUES($1,$2,'completed',$3,1,1,1,decode(repeat('77',32),'hex'),
			$1::date+interval '1 hour',$1::date+interval '2 hours',$1::date+interval '2 hours')
		RETURNING rollup_run_id`, summaryDate, lifecycle.instanceID, uuid.New()).
			Scan(&rollupID); err != nil {
			t.Fatal(err)
		}
		return retiredDayFixture{
			lifecycle: lifecycle, summaryDate: summaryDate,
			slot: summaryDate.Add(12 * time.Hour), rollupID: rollupID,
		}
	}
	first := prepare(31)
	pollTransaction, err := database.runtime.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var firstPollID uuid.UUID
	if err := pollTransaction.QueryRow(ctx, `SELECT public.test_runtime_insert_retention_race_poll(
		$1,$2,$3,$4,$5)`, first.lifecycle.instanceID, first.lifecycle.nodeType,
		first.lifecycle.contract, first.slot, first.lifecycle.policyID).Scan(&firstPollID); err != nil {
		_ = pollTransaction.Rollback(ctx)
		t.Fatal(err)
	}
	type retentionCall struct {
		processed int
		err       error
	}
	retentionDone := make(chan retentionCall, 1)
	go func() {
		var processed int
		err := database.runtime.QueryRow(ctx, `SELECT
			(public.control_delete_account_inventory_rollup_run_retention_v1(1)
			 ->>'processed_count')::integer`).Scan(&processed)
		retentionDone <- retentionCall{processed: processed, err: err}
	}()
	select {
	case result := <-retentionDone:
		_ = pollTransaction.Rollback(ctx)
		t.Fatalf("retention did not wait for poll day lock: processed=%d err=%v",
			result.processed, result.err)
	case <-time.After(150 * time.Millisecond):
	}
	if err := pollTransaction.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-retentionDone:
		if result.err != nil || result.processed != 0 {
			t.Fatalf("retention marked day after committed poll: processed=%d err=%v",
				result.processed, result.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("retention did not resume after poll transaction committed")
	}
	var firstMarkers, firstRollups, firstPolls int
	if err := database.owner.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM account_inventory_history_retired_days
		 WHERE summary_date=$1 AND instance_id=$2),
		(SELECT count(*) FROM account_inventory_daily_rollup_runs WHERE rollup_run_id=$3),
		(SELECT count(*) FROM account_inventory_poll_runs WHERE poll_run_id=$4)`,
		first.summaryDate, first.lifecycle.instanceID, first.rollupID, firstPollID).Scan(
		&firstMarkers, &firstRollups, &firstPolls); err != nil {
		t.Fatal(err)
	}
	if firstMarkers != 0 || firstRollups != 1 || firstPolls != 1 {
		t.Fatalf("poll-won ordering marker=%d rollup=%d poll=%d",
			firstMarkers, firstRollups, firstPolls)
	}

	second := prepare(32)
	retentionTransaction, err := database.runtime.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var retained int
	if err := retentionTransaction.QueryRow(ctx, `SELECT
		(public.control_delete_account_inventory_rollup_run_retention_v1(1)
		 ->>'processed_count')::integer`).Scan(&retained); err != nil || retained != 1 {
		_ = retentionTransaction.Rollback(ctx)
		t.Fatalf("retention transaction processed=%d err=%v", retained, err)
	}
	pollDone := make(chan error, 1)
	go func() {
		var pollID uuid.UUID
		pollDone <- database.runtime.QueryRow(ctx, `SELECT public.test_runtime_insert_retention_race_poll(
			$1,$2,$3,$4,$5)`, second.lifecycle.instanceID, second.lifecycle.nodeType,
			second.lifecycle.contract, second.slot, second.lifecycle.policyID).Scan(&pollID)
	}()
	select {
	case err := <-pollDone:
		_ = retentionTransaction.Rollback(ctx)
		t.Fatalf("poll did not wait for retired-day marker lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	if err := retentionTransaction.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-pollDone:
		if err == nil {
			t.Fatal("poll inserted after retired-day marker committed")
		}
		requireHistorySQLState(t, err, "23514")
	case <-time.After(3 * time.Second):
		t.Fatal("poll did not resume after retired-day marker committed")
	}
	var secondMarkers, secondRollups, secondPolls int
	if err := database.owner.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM account_inventory_history_retired_days
		 WHERE summary_date=$1 AND instance_id=$2),
		(SELECT count(*) FROM account_inventory_daily_rollup_runs WHERE rollup_run_id=$3),
		(SELECT count(*) FROM account_inventory_poll_runs
		 WHERE instance_id=$2 AND scheduled_at=$4)`, second.summaryDate,
		second.lifecycle.instanceID, second.rollupID, second.slot).Scan(
		&secondMarkers, &secondRollups, &secondPolls); err != nil {
		t.Fatal(err)
	}
	if secondMarkers != 1 || secondRollups != 0 || secondPolls != 0 {
		t.Fatalf("retention-won ordering marker=%d rollup=%d poll=%d",
			secondMarkers, secondRollups, secondPolls)
	}
}

func TestAccountInventoryHistoryCompactionClaimRenewReclaimFencing(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleSchemaFixture(t, ctx, database)
	firstRunID, secondRunID := uuid.New(), uuid.New()
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_compaction_runs(
		compaction_run_id,summary_date,instance_id,provider_policy_version
	) VALUES
		($1,(clock_timestamp() AT TIME ZONE 'UTC')::date-5,$3,$4),
		($2,(clock_timestamp() AT TIME ZONE 'UTC')::date-4,$3,$4)`,
		firstRunID, secondRunID, fixture.instanceID, fixture.policyID); err != nil {
		t.Fatal(err)
	}

	type claimResult struct {
		runID, fence uuid.UUID
		owner        string
		attempt      int
		updatedAt    time.Time
		leaseUntil   time.Time
	}
	claim := func(queryContext context.Context, transaction pgx.Tx, worker uuid.UUID, leaseSeconds int) claimResult {
		t.Helper()
		var result claimResult
		if err := transaction.QueryRow(queryContext, `SELECT
			compaction_run_id,claim_owner,fencing_token,attempt_count,updated_at,lease_expires_at
			FROM public.control_claim_account_inventory_compaction_v1($1,$2)`,
			worker, leaseSeconds).Scan(&result.runID, &result.owner, &result.fence,
			&result.attempt, &result.updatedAt, &result.leaseUntil); err != nil {
			t.Fatal(err)
		}
		return result
	}

	firstWorker, secondWorker := uuid.New(), uuid.New()
	firstTransaction, err := database.runtime.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer firstTransaction.Rollback(ctx)
	first := claim(ctx, firstTransaction, firstWorker, 5)

	secondTransaction, err := database.runtime.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer secondTransaction.Rollback(ctx)
	secondContext, cancelSecond := context.WithTimeout(ctx, 2*time.Second)
	second := claim(secondContext, secondTransaction, secondWorker, 30)
	cancelSecond()

	for _, item := range []struct {
		claim       claimResult
		wantRun     uuid.UUID
		wantWorker  uuid.UUID
		wantSeconds time.Duration
	}{
		{first, firstRunID, firstWorker, 5 * time.Second},
		{second, secondRunID, secondWorker, 30 * time.Second},
	} {
		if item.claim.runID != item.wantRun || item.claim.owner != item.wantWorker.String() ||
			item.claim.fence == uuid.Nil || item.claim.attempt != 1 ||
			item.claim.leaseUntil.Sub(item.claim.updatedAt) != item.wantSeconds {
			t.Fatalf("claim=%+v want_run=%s want_worker=%s want_lease=%s",
				item.claim, item.wantRun, item.wantWorker, item.wantSeconds)
		}
	}
	if first.fence == second.fence {
		t.Fatal("independent claims reused a fencing token")
	}
	if err := secondTransaction.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := firstTransaction.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	expiryContext, cancelExpiry := context.WithTimeout(ctx, 8*time.Second)
	defer cancelExpiry()
	for {
		var expired bool
		if err := database.owner.QueryRow(expiryContext,
			`SELECT clock_timestamp() >= $1`, first.leaseUntil).Scan(&expired); err != nil {
			t.Fatal(err)
		}
		if expired {
			break
		}
		select {
		case <-expiryContext.Done():
			t.Fatal("database-time lease did not expire")
		case <-time.After(25 * time.Millisecond):
		}
	}

	var reconciled int
	if err := database.runtime.QueryRow(ctx, `SELECT
		(public.control_reconcile_account_inventory_compactions_v1(10)->>'failed_count')::integer`).
		Scan(&reconciled); err != nil || reconciled != 1 {
		t.Fatalf("reconciled=%d err=%v", reconciled, err)
	}
	var reconciledState bool
	if err := database.owner.QueryRow(ctx, `SELECT status='failed' AND failed_from='pending'
		AND failure_reason='lease_expired' AND claim_owner IS NULL
		AND lease_expires_at IS NULL AND fencing_token IS NULL AND attempt_count=1
		FROM account_inventory_compaction_runs WHERE compaction_run_id=$1`, firstRunID).
		Scan(&reconciledState); err != nil || !reconciledState {
		t.Fatalf("reconciled state=%t err=%v", reconciledState, err)
	}

	reclaimWorker := uuid.New()
	var reclaimed claimResult
	var status string
	var failedFrom *string
	if err := database.runtime.QueryRow(ctx, `SELECT
		compaction_run_id,claim_owner,fencing_token,attempt_count,updated_at,lease_expires_at,
		status,failed_from
		FROM public.control_claim_account_inventory_compaction_v1($1,30)`, reclaimWorker).Scan(
		&reclaimed.runID, &reclaimed.owner, &reclaimed.fence, &reclaimed.attempt,
		&reclaimed.updatedAt, &reclaimed.leaseUntil, &status, &failedFrom,
	); err != nil {
		t.Fatal(err)
	}
	if reclaimed.runID != firstRunID || reclaimed.owner != reclaimWorker.String() ||
		reclaimed.attempt != 2 || reclaimed.fence == uuid.Nil || reclaimed.fence == first.fence ||
		reclaimed.leaseUntil.Sub(reclaimed.updatedAt) != 30*time.Second ||
		status != "pending" || failedFrom != nil {
		t.Fatalf("reclaimed=%+v status=%s failed_from=%v", reclaimed, status, failedFrom)
	}

	var before, after string
	if err := database.owner.QueryRow(ctx, `SELECT to_jsonb(run)::text
		FROM account_inventory_compaction_runs AS run WHERE compaction_run_id=$1`, firstRunID).
		Scan(&before); err != nil {
		t.Fatal(err)
	}
	var staleRenewed int
	if err := database.runtime.QueryRow(ctx, `SELECT count(*)
		FROM public.control_renew_account_inventory_compaction_v1($1,$2,30)`,
		firstRunID, first.fence).Scan(&staleRenewed); err != nil || staleRenewed != 0 {
		t.Fatalf("stale renew rows=%d err=%v", staleRenewed, err)
	}
	if err := database.owner.QueryRow(ctx, `SELECT to_jsonb(run)::text
		FROM account_inventory_compaction_runs AS run WHERE compaction_run_id=$1`, firstRunID).
		Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("stale fencing token changed reclaimed run")
	}
}

func TestAccountInventoryHistoryLeaseExpiryWhileWaitingForRunLock(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleSchemaFixture(t, ctx, database)
	runID, fence := uuid.New(), uuid.New()
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_compaction_runs(
		compaction_run_id,summary_date,instance_id,provider_policy_version,status,
		claim_owner,lease_expires_at,fencing_token,attempt_count
	) VALUES($1,(clock_timestamp() AT TIME ZONE 'UTC')::date-4,$2,$3,'pending',
		'lease-race-worker',clock_timestamp()+interval '750 milliseconds',$4,1)`,
		runID, fixture.instanceID, fixture.policyID, fence); err != nil {
		t.Fatal(err)
	}
	lockTransaction, err := database.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockTransaction.Exec(ctx, `SELECT 1
		FROM account_inventory_compaction_runs WHERE compaction_run_id=$1 FOR UPDATE`, runID); err != nil {
		_ = lockTransaction.Rollback(ctx)
		t.Fatal(err)
	}
	type renewResult struct {
		rows int
		err  error
	}
	started := make(chan struct{})
	result := make(chan renewResult, 1)
	go func() {
		close(started)
		var rows int
		err := database.runtime.QueryRow(ctx, `SELECT count(*)
			FROM public.control_renew_account_inventory_compaction_v1($1,$2,5)`,
			runID, fence).Scan(&rows)
		result <- renewResult{rows: rows, err: err}
	}()
	<-started
	time.Sleep(1100 * time.Millisecond)
	if err := lockTransaction.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	renewed := <-result
	if renewed.err != nil || renewed.rows != 0 {
		t.Fatalf("expired lease revived after lock wait: rows=%d err=%v",
			renewed.rows, renewed.err)
	}
	var stillExpired bool
	if err := database.owner.QueryRow(ctx, `SELECT status='pending'
		AND lease_expires_at<=clock_timestamp() AND fencing_token=$2
		FROM account_inventory_compaction_runs WHERE compaction_run_id=$1`, runID, fence).
		Scan(&stillExpired); err != nil || !stillExpired {
		t.Fatalf("expired lease changed after stale renew: expired=%t err=%v", stillExpired, err)
	}
}

func TestAccountInventoryHistoryFailedShapesAndProviderDayBound(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleSchemaFixture(t, ctx, database)

	_, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_compaction_runs(
		summary_date,instance_id,provider_policy_version,status,failed_from,
		checksum_version,source_snapshot_count,source_poll_count,
		source_provider_result_count,source_duplicate_count,source_checksum,
		failure_reason,created_at,summarized_at,deleting_at,failed_at,updated_at
	) VALUES(
		(clock_timestamp() AT TIME ZONE 'UTC')::date-5,$1,$2,'failed','summarized',
		1,0,0,0,0,decode(repeat('00',32),'hex'),'internal',
		clock_timestamp()-interval '2 minutes',clock_timestamp()-interval '90 seconds',
		clock_timestamp()-interval '60 seconds',clock_timestamp(),clock_timestamp()
	)`, fixture.instanceID, fixture.policyID)
	if err == nil {
		t.Fatal("failed_from summarized accepted a deleting timestamp")
	}
	requireHistorySQLState(t, err, "23514")

	_, err = database.owner.Exec(ctx, `INSERT INTO account_inventory_daily_rollup_runs(
		summary_date,instance_id,status,expected_segment_count,completed_segment_count,
		failure_reason,created_at,failed_at,updated_at
	) VALUES(
		(clock_timestamp() AT TIME ZONE 'UTC')::date-6,$1,'failed',1,0,'internal',
		clock_timestamp()-interval '1 minute',clock_timestamp(),clock_timestamp()
	)`, fixture.instanceID)
	if err == nil {
		t.Fatal("failed rollup accepted partial segment evidence")
	}
	requireHistorySQLState(t, err, "23514")

	var rollupRunID uuid.UUID
	if err := database.owner.QueryRow(ctx, `INSERT INTO account_inventory_daily_rollup_runs(
		summary_date,instance_id
	) VALUES((clock_timestamp() AT TIME ZONE 'UTC')::date-7,$1)
	RETURNING rollup_run_id`, fixture.instanceID).Scan(&rollupRunID); err != nil {
		t.Fatal(err)
	}
	_, err = database.owner.Exec(ctx, `INSERT INTO account_inventory_daily_provider_rollups(
		rollup_run_id,summary_date,instance_id,provider,
		expected_poll_count,transport_success_count,contract_valid_count,
		snapshot_complete_count,promotion_applied_count,promotion_skipped_count,
		policy_changed_count,abandoned_count,degraded_count,
		coverage_numerator,coverage_denominator,coverage_ratio,
		coverage_threshold_basis_points,coverage_status
	) VALUES(
		$1,(clock_timestamp() AT TIME ZONE 'UTC')::date-7,$2,'openai',
		289,0,0,0,0,0,0,0,0,0,289,0,9500,'partial'
	)`, rollupRunID, fixture.instanceID)
	if err == nil {
		t.Fatal("Provider daily rollup accepted more than 288 five-minute slots")
	}
	requireHistorySQLState(t, err, "23514")
}

func requireHistoryMigrationVersion(
	t *testing.T, ctx context.Context, database *isolatedJobDatabase, want int64,
) {
	t.Helper()
	var version int64
	if err := database.owner.QueryRow(ctx, `SELECT max(version_id)
		FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil || version != want {
		t.Fatalf("history Migration version=%d want=%d err=%v", version, want, err)
	}
}

func requireHistorySQLState(t *testing.T, err error, want string) {
	t.Helper()
	var databaseError *pgconn.PgError
	if !errors.As(err, &databaseError) || databaseError.Code != want {
		t.Fatalf("PostgreSQL error=%v want SQLSTATE %s", err, want)
	}
}
