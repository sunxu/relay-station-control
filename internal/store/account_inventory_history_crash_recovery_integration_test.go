package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAccountInventoryHistoryCrashRecoveryMatrix(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleSchemaFixture(t, ctx, database)
	targetDate := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -4)
	pollIDs := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}

	if _, err := database.owner.Exec(ctx, `UPDATE provider_inventory_policy_activations
		SET effective_from=$1,created_at=$1 WHERE policy_version_id=$2`,
		targetDate, fixture.policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_inventory_monitoring_activations(
		instance_id,effective_from,reason,actor,created_at
	) VALUES($1,$2,'reconciliation','crash-recovery-test',$2)`,
		fixture.instanceID, targetDate); err != nil {
		t.Fatal(err)
	}
	fixtureTx, err := database.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer fixtureTx.Rollback(ctx)
	if _, err := fixtureTx.Exec(ctx, `ALTER TABLE account_inventory_poll_provider_results
		DISABLE TRIGGER account_inventory_poll_provider_results_guard;
		ALTER TABLE account_inventory_snapshot_items
		DISABLE TRIGGER account_inventory_snapshot_items_insert_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureTx.Exec(ctx, `INSERT INTO account_inventory_poll_runs(
		poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,
		provider_policy_version,status,attempt_count,max_attempts,poll_start_grace_seconds,
		created_at,first_started_at,last_started_at,finalized_at,observed_at,
		transport_success,response_shape_valid,contract_valid,inventory_mode,
		node_identity_complete,snapshot_complete,degraded,result,reason,
		source_record_count,identifiable_record_count,unidentified_record_count,
		unsupported_provider_count,out_of_scope_provider_count,node_version,node_commit
	) SELECT poll_id,$4,$5,$6,$7::timestamptz+(ordinality-1)*interval '5 minutes',
		$8,'finalized',1,2,299,$7::timestamptz+(ordinality-1)*interval '5 minutes'+interval '1 second',
		$7::timestamptz+(ordinality-1)*interval '5 minutes'+interval '2 seconds',
		$7::timestamptz+(ordinality-1)*interval '5 minutes'+interval '2 seconds',
		$7::timestamptz+(ordinality-1)*interval '5 minutes'+interval '4 seconds',
		$7::timestamptz+(ordinality-1)*interval '5 minutes'+interval '3 seconds',
		true,true,true,'runtime',true,true,false,'success','none',1,1,0,0,0,'unknown','unknown'
	FROM unnest(ARRAY[$1,$2,$3]::uuid[]) WITH ORDINALITY AS source(poll_id,ordinality)`,
		pollIDs[0], pollIDs[1], pollIDs[2], fixture.instanceID, fixture.nodeType,
		fixture.contract, targetDate.Add(12*time.Hour), fixture.policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureTx.Exec(ctx, `INSERT INTO account_inventory_poll_provider_results(
		poll_run_id,provider,identifiable_count,missing_identity_count,duplicate_identity_count,
		identity_complete,snapshot_complete,degraded,reason,promotion_applied
	) SELECT poll_id,'openai',1,0,0,true,true,false,'complete',true
	FROM unnest(ARRAY[$1,$2,$3]::uuid[]) AS source(poll_id)`,
		pollIDs[0], pollIDs[1], pollIDs[2]); err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureTx.Exec(ctx, `INSERT INTO account_inventory_snapshot_items(
		poll_run_id,instance_id,provider,account_key,normalized_email,basic_status,
		success_count,failed_count,recent_request_count,observed_at
	) SELECT poll_id,$4,'openai','openai:crash-recovery@example.invalid',
		'crash-recovery@example.invalid','active',ordinality,0,0,
		$5::timestamptz+(ordinality-1)*interval '5 minutes'+interval '3 seconds'
	FROM unnest(ARRAY[$1,$2,$3]::uuid[]) WITH ORDINALITY AS source(poll_id,ordinality)`,
		pollIDs[0], pollIDs[1], pollIDs[2], fixture.instanceID,
		targetDate.Add(12*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureTx.Exec(ctx, `ALTER TABLE account_inventory_poll_provider_results
		ENABLE TRIGGER account_inventory_poll_provider_results_guard;
		ALTER TABLE account_inventory_snapshot_items
		ENABLE TRIGGER account_inventory_snapshot_items_insert_guard`); err != nil {
		t.Fatal(err)
	}
	if err := fixtureTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	var planned int
	if err := database.runtime.QueryRow(ctx, `SELECT
		(public.control_plan_account_inventory_history_v1(10)->>'compaction_runs_created')::integer`).
		Scan(&planned); err != nil || planned != 1 {
		t.Fatalf("plan compactions=%d err=%v", planned, err)
	}
	var runID, fence uuid.UUID
	if err := database.runtime.QueryRow(ctx, `SELECT compaction_run_id,fencing_token
		FROM public.control_claim_account_inventory_compaction_v1($1,300)`, uuid.New()).
		Scan(&runID, &fence); err != nil {
		t.Fatal(err)
	}

	historyCommitUnknown(t, ctx, database,
		`SELECT public.control_summarize_account_inventory_compaction_v1($1,$2)`, runID, fence)
	var summarizeFingerprint, checksumHex string
	var sourceSnapshots, accountSegments, providerSegments, summarizedAudits int
	if err := database.owner.QueryRow(ctx, `SELECT to_jsonb(run)::text,
		encode(run.source_checksum,'hex'),run.source_snapshot_count,
		(SELECT count(*) FROM account_inventory_daily_summaries WHERE compaction_run_id=$1),
		(SELECT count(*) FROM account_inventory_daily_provider_summaries WHERE compaction_run_id=$1),
		(SELECT count(*) FROM audit_logs WHERE action='account_inventory_history.summarized'
		 AND details->>'instance'=$2::uuid::text AND details->>'summary_date'=$3::date::text)
	FROM account_inventory_compaction_runs AS run WHERE compaction_run_id=$1`,
		runID, fixture.instanceID, targetDate).Scan(&summarizeFingerprint, &checksumHex,
		&sourceSnapshots, &accountSegments, &providerSegments, &summarizedAudits); err != nil {
		t.Fatal(err)
	}
	if sourceSnapshots != 3 || accountSegments != 1 || providerSegments != 1 ||
		summarizedAudits != 1 || len(checksumHex) != 64 ||
		!strings.Contains(summarizeFingerprint, `"status": "summarized"`) {
		t.Fatalf("unknown summarize did not commit exactly once snapshots=%d segments=%d/%d audits=%d",
			sourceSnapshots, accountSegments, providerSegments, summarizedAudits)
	}
	var replayStatus, replayFingerprint string
	if err := database.runtime.QueryRow(ctx, `SELECT
		public.control_summarize_account_inventory_compaction_v1($1,$2)->>'status'`,
		runID, fence).Scan(&replayStatus); err != nil {
		t.Fatal(err)
	}
	if err := database.owner.QueryRow(ctx, `SELECT to_jsonb(run)::text
		FROM account_inventory_compaction_runs AS run WHERE compaction_run_id=$1`, runID).
		Scan(&replayFingerprint); err != nil {
		t.Fatal(err)
	}
	if replayStatus != "summarized" || replayFingerprint != summarizeFingerprint {
		t.Fatal("summarize commit-unknown replay recalculated persisted proof")
	}

	for batch := 0; batch < 3; batch++ {
		historyPreCommitBackendCrash(t, ctx, database,
			`SELECT public.control_delete_account_inventory_snapshot_batch_v1($1,$2,1)`, runID, fence)
		assertHistoryCrashDeleteProgress(t, ctx, database, runID, fixture.instanceID,
			targetDate, batch, 3-batch)

		historyCommitUnknown(t, ctx, database,
			`SELECT public.control_delete_account_inventory_snapshot_batch_v1($1,$2,1)`, runID, fence)
		assertHistoryCrashDeleteProgress(t, ctx, database, runID, fixture.instanceID,
			targetDate, batch+1, 2-batch)
	}

	// The final batch's unknown commit drops the worker connection before complete.
	// A crash inside complete must leave the durable deleting proof unchanged.
	historyPreCommitBackendCrash(t, ctx, database,
		`SELECT public.control_complete_account_inventory_compaction_v1(
			$1,$2,decode($3,'hex'))`, runID, fence, checksumHex)
	assertHistoryCrashDeleteProgress(t, ctx, database, runID, fixture.instanceID,
		targetDate, 3, 0)
	var completedAudits, prematureRollups int
	if err := database.owner.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM audit_logs WHERE action='account_inventory_history.completed'
		 AND details->>'phase'='complete' AND details->>'instance'=$1::uuid::text
		 AND details->>'summary_date'=$2::date::text),
		(SELECT count(*) FROM account_inventory_daily_rollup_runs
		 WHERE instance_id=$1 AND summary_date=$2)`, fixture.instanceID, targetDate).
		Scan(&completedAudits, &prematureRollups); err != nil {
		t.Fatal(err)
	}
	if completedAudits != 0 || prematureRollups != 0 {
		t.Fatalf("crashed complete published audit=%d rollups=%d", completedAudits, prematureRollups)
	}

	// Recovery starts from durable deleting state and completes with the same proof.
	var completeStatus string
	if err := database.runtime.QueryRow(ctx, `SELECT
		public.control_complete_account_inventory_compaction_v1(
			$1,$2,decode($3,'hex'))->>'status'`, runID, fence, checksumHex).
		Scan(&completeStatus); err != nil || completeStatus != "completed" {
		t.Fatalf("complete after connection crash status=%s err=%v", completeStatus, err)
	}

	if err := database.runtime.QueryRow(ctx, `SELECT
		(public.control_plan_account_inventory_history_v1(10)->>'rollup_runs_created')::integer`).
		Scan(&planned); err != nil || planned != 1 {
		t.Fatalf("plan rollups=%d err=%v", planned, err)
	}
	var rollupRunID, rollupFence uuid.UUID
	if err := database.runtime.QueryRow(ctx, `SELECT rollup_run_id,fencing_token
		FROM public.control_claim_account_inventory_daily_rollup_v1($1,300)`, uuid.New()).
		Scan(&rollupRunID, &rollupFence); err != nil {
		t.Fatal(err)
	}
	historyPreCommitBackendCrash(t, ctx, database,
		`SELECT public.control_finalize_account_inventory_daily_rollup_v1($1,$2)`,
		rollupRunID, rollupFence)
	var rollupStatus string
	var accountFinals, providerFinals, rollupAudits int
	if err := database.owner.QueryRow(ctx, `SELECT run.status,
		(SELECT count(*) FROM account_inventory_daily_account_rollups WHERE rollup_run_id=$1),
		(SELECT count(*) FROM account_inventory_daily_provider_rollups WHERE rollup_run_id=$1),
		(SELECT count(*) FROM audit_logs WHERE action='account_inventory_history.completed'
		 AND details->>'phase'='rollup_complete' AND details->>'instance'=$2::uuid::text
		 AND details->>'summary_date'=$3::date::text)
	FROM account_inventory_daily_rollup_runs AS run WHERE rollup_run_id=$1`,
		rollupRunID, fixture.instanceID, targetDate).
		Scan(&rollupStatus, &accountFinals, &providerFinals, &rollupAudits); err != nil {
		t.Fatal(err)
	}
	if rollupStatus != "pending" || accountFinals != 0 || providerFinals != 0 || rollupAudits != 0 {
		t.Fatalf("crashed rollup partially published status=%s finals=%d/%d audits=%d",
			rollupStatus, accountFinals, providerFinals, rollupAudits)
	}
	if err := database.runtime.QueryRow(ctx, `SELECT
		public.control_finalize_account_inventory_daily_rollup_v1($1,$2)->>'status'`,
		rollupRunID, rollupFence).Scan(&rollupStatus); err != nil || rollupStatus != "completed" {
		t.Fatalf("rollup recovery status=%s err=%v", rollupStatus, err)
	}
	var compactionAudits int
	if err := database.owner.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM account_inventory_daily_account_rollups WHERE rollup_run_id=$1),
		(SELECT count(*) FROM account_inventory_daily_provider_rollups WHERE rollup_run_id=$1),
		(SELECT count(*) FROM audit_logs WHERE action='account_inventory_history.completed'
		 AND details->>'phase'='complete' AND details->>'instance'=$2::uuid::text
		 AND details->>'summary_date'=$3::date::text),
		(SELECT count(*) FROM audit_logs WHERE action='account_inventory_history.completed'
		 AND details->>'phase'='rollup_complete' AND details->>'instance'=$2::uuid::text
		 AND details->>'summary_date'=$3::date::text)`,
		rollupRunID, fixture.instanceID, targetDate).
		Scan(&accountFinals, &providerFinals, &compactionAudits, &rollupAudits); err != nil {
		t.Fatal(err)
	}
	if accountFinals != 1 || providerFinals != 1 || compactionAudits != 1 || rollupAudits != 1 {
		t.Fatalf("recovery did not converge exactly once finals=%d/%d audits=%d/%d",
			accountFinals, providerFinals, compactionAudits, rollupAudits)
	}
}

func historyCommitUnknown(
	t *testing.T, ctx context.Context, database *isolatedJobDatabase, statement string, arguments ...any,
) {
	t.Helper()
	connection, err := database.runtime.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	if _, err := connection.Exec(ctx, `BEGIN`); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, statement, arguments...); err != nil {
		_, _ = connection.Exec(ctx, `ROLLBACK`)
		t.Fatal(err)
	}
	unknownContext, cancel := context.WithTimeout(ctx, 75*time.Millisecond)
	_, err = connection.Exec(unknownContext, `COMMIT; SELECT pg_sleep(30)`)
	cancel()
	if err == nil {
		t.Fatal("commit-unknown injection returned a known result")
	}
}

func historyPreCommitBackendCrash(
	t *testing.T, ctx context.Context, database *isolatedJobDatabase, statement string, arguments ...any,
) {
	t.Helper()
	connection, err := database.runtime.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	var backendPID int32
	if err := connection.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&backendPID); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, `BEGIN`); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, statement, arguments...); err != nil {
		t.Fatal(err)
	}
	var terminated bool
	if err := database.owner.QueryRow(ctx, `SELECT pg_terminate_backend($1,5000)`, backendPID).
		Scan(&terminated); err != nil || !terminated {
		t.Fatalf("terminate backend=%t err=%v", terminated, err)
	}
	if _, err := connection.Exec(ctx, `ROLLBACK`); err == nil {
		t.Fatal("terminated transaction remained usable")
	}
}

func assertHistoryCrashDeleteProgress(
	t *testing.T, ctx context.Context, database *isolatedJobDatabase,
	runID, instanceID uuid.UUID, summaryDate time.Time, wantDeleted, wantRemaining int,
) {
	t.Helper()
	var status string
	var source, deleted, remaining, audits int
	if err := database.owner.QueryRow(ctx, `SELECT run.status,run.source_snapshot_count,
		run.deleted_snapshot_count,
		(SELECT count(*) FROM account_inventory_snapshot_items AS snapshot
		 JOIN account_inventory_poll_runs AS poll ON poll.poll_run_id=snapshot.poll_run_id
		 WHERE poll.instance_id=run.instance_id
		   AND poll.provider_policy_version=run.provider_policy_version
		   AND (poll.scheduled_at AT TIME ZONE 'UTC')::date=run.summary_date),
		(SELECT count(*) FROM audit_logs WHERE action='account_inventory_history.snapshot_delete_batch'
		 AND details->>'instance'=$2::uuid::text AND details->>'summary_date'=$3::date::text)
	FROM account_inventory_compaction_runs AS run WHERE run.compaction_run_id=$1`,
		runID, instanceID, summaryDate).Scan(&status, &source, &deleted, &remaining, &audits); err != nil {
		t.Fatal(err)
	}
	wantStatus := "summarized"
	if wantDeleted > 0 {
		wantStatus = "deleting"
	}
	if status != wantStatus || source != 3 || deleted != wantDeleted ||
		remaining != wantRemaining || audits != wantDeleted || deleted+remaining != source {
		t.Fatalf("delete progress status=%s source=%d deleted=%d remaining=%d audits=%d",
			status, source, deleted, remaining, audits)
	}
}
