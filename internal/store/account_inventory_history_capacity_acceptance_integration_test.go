package store_test

import (
	"context"
	"os"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

func TestAccountInventoryHistoryCapacityAcceptance(t *testing.T) {
	scaleName := os.Getenv("CONTROL_HISTORY_CAPACITY_SCALE")
	if scaleName == "" {
		t.Skip("history capacity acceptance is opt-in")
	}
	snapshots := 0
	evidence := "full"
	switch scaleName {
	case "smoke":
		snapshots, evidence = 40, "not_evidence"
	case "864000":
		snapshots = 864000
	case "1152000":
		snapshots = 1152000
	default:
		t.Fatalf("unsupported history capacity scale %q", scaleName)
	}

	database := newIsolatedJobDatabase(t)
	timeout := 6 * time.Hour
	if scaleName == "smoke" {
		timeout = 15 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	fixture := newLifecycleSchemaFixture(t, ctx, database)
	oldestDate := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -8)
	if _, err := database.owner.Exec(ctx, `UPDATE provider_inventory_policy_activations
		SET effective_from=$1,created_at=$1 WHERE policy_version_id=$2`, oldestDate, fixture.policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_inventory_monitoring_activations(
		instance_id,effective_from,reason,actor,created_at
	) VALUES($1,$2,'reconciliation','history-capacity',$2)`, fixture.instanceID, oldestDate); err != nil {
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

	var walStart string
	var databaseBytesStart, indexBytesStart, blocksReadStart, blocksHitStart, tempBytesStart, deadlocksStart int64
	if err := database.owner.QueryRow(ctx, `SELECT pg_current_wal_lsn()::text,
		pg_database_size(current_database())::bigint,
		coalesce((SELECT sum(pg_indexes_size(c.oid)) FROM pg_class c
			JOIN pg_namespace n ON n.oid=c.relnamespace
			WHERE n.nspname='public' AND c.relkind IN ('r','p')),0)::bigint,
		d.blks_read,d.blks_hit,d.temp_bytes,d.deadlocks
		FROM pg_stat_database d WHERE d.datname=current_database()`).Scan(
		&walStart, &databaseBytesStart, &indexBytesStart, &blocksReadStart, &blocksHitStart,
		&tempBytesStart, &deadlocksStart); err != nil {
		t.Fatal(err)
	}
	rssStart := historyCapacityMaxRSS(t)

	const samples = 5
	for sample := 0; sample < samples; sample++ {
		targetDate := oldestDate.AddDate(0, 0, sample)
		slot := targetDate.Add(12 * time.Hour)
		pollID := uuid.New()
		if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_poll_runs(
			poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,
			provider_policy_version,status,attempt_count,max_attempts,poll_start_grace_seconds,
			created_at,first_started_at,last_started_at,finalized_at,observed_at,
			transport_success,response_shape_valid,contract_valid,inventory_mode,
			node_identity_complete,snapshot_complete,degraded,result,reason,
			source_record_count,identifiable_record_count,unidentified_record_count,
			unsupported_provider_count,out_of_scope_provider_count,node_version,node_commit
		) VALUES($1,$2,$3,$4,$5::timestamptz,$6,'finalized',1,2,299,
			$5::timestamptz+interval '1 second',$5::timestamptz+interval '2 seconds',$5::timestamptz+interval '2 seconds',
			$5::timestamptz+interval '4 seconds',$5::timestamptz+interval '3 seconds',true,true,true,'runtime',
			true,true,false,'success','none',$7,$7,0,0,0,'unknown','unknown')`,
			pollID, fixture.instanceID, fixture.nodeType, fixture.contract, slot,
			fixture.policyID, snapshots); err != nil {
			t.Fatal(err)
		}
		if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_poll_provider_results(
			poll_run_id,provider,identifiable_count,missing_identity_count,
			duplicate_identity_count,identity_complete,snapshot_complete,degraded,reason,
			promotion_applied,promotion_skipped_reason
		) VALUES($1,'openai',$2,0,0,true,true,false,'complete',true,NULL)`, pollID, snapshots); err != nil {
			t.Fatal(err)
		}
		if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_snapshot_items(
			poll_run_id,instance_id,provider,account_key,normalized_email,basic_status,
			success_count,failed_count,recent_request_count,observed_at
		) SELECT $1,$2,'openai',
			'openai:history-capacity-' || $3::integer::text || '-' || item::text || '@example.invalid',
			'history-capacity-' || $3::integer::text || '-' || item::text || '@example.invalid',
			'active',item,0,0,$4::timestamptz+interval '3 seconds'
		FROM generate_series(1,$5) AS item`, pollID, fixture.instanceID, sample, slot, snapshots); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_poll_provider_results
		ENABLE TRIGGER account_inventory_poll_provider_results_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_snapshot_items
		ENABLE TRIGGER account_inventory_snapshot_items_insert_guard`); err != nil {
		t.Fatal(err)
	}

	var maximumLockWaiters atomic.Int64
	stopSampling, samplingDone := make(chan struct{}), make(chan struct{})
	go sampleLifecycleLockWaiters(ctx, database.owner, stopSampling, samplingDone, &maximumLockWaiters)
	defer func() {
		select {
		case <-samplingDone:
		default:
			close(stopSampling)
			<-samplingDone
		}
	}()

	var plannedCompactions int
	if err := database.runtime.QueryRow(ctx, `SELECT
		(public.control_plan_account_inventory_history_v1(1000)->>'compaction_runs_created')::integer`).
		Scan(&plannedCompactions); err != nil || plannedCompactions != samples {
		t.Fatalf("planned compactions=%d err=%v", plannedCompactions, err)
	}
	summaryDurations := make([]time.Duration, 0, samples)
	totalDeleteBatches, totalDeleted := 0, 0
	deleteLimit := snapshots / 20
	if deleteLimit < 1 {
		deleteLimit = 1
	}
	if deleteLimit > 5000 {
		deleteLimit = 5000
	}
	for sample := 0; sample < samples; sample++ {
		var runID, fence uuid.UUID
		if err := database.runtime.QueryRow(ctx, `SELECT compaction_run_id,fencing_token
			FROM public.control_claim_account_inventory_compaction_v1($1,300)`, uuid.New()).
			Scan(&runID, &fence); err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		var checksum string
		var sourceSnapshots, accountSegments int
		if err := database.runtime.QueryRow(ctx, `WITH summarized AS (
			SELECT public.control_summarize_account_inventory_compaction_v1($1,$2) AS value
		) SELECT value->>'source_checksum_hex',(value->>'source_snapshot_count')::integer,
			(value->>'account_segment_count')::integer FROM summarized`, runID, fence).
			Scan(&checksum, &sourceSnapshots, &accountSegments); err != nil {
			t.Fatal(err)
		}
		summaryDurations = append(summaryDurations, time.Since(started))
		if len(checksum) != 64 || sourceSnapshots != snapshots || accountSegments != snapshots {
			t.Fatalf("sample=%d snapshots=%d segments=%d checksum_length=%d",
				sample, sourceSnapshots, accountSegments, len(checksum))
		}
		deletedForRun := 0
		for {
			var deleted, remaining int
			if err := database.runtime.QueryRow(ctx, `WITH batch AS (
				SELECT public.control_delete_account_inventory_snapshot_batch_v1($1,$2,$3) AS value
			) SELECT (value->>'deleted_count')::integer,(value->>'remaining_count')::integer
				FROM batch`, runID, fence, deleteLimit).Scan(&deleted, &remaining); err != nil {
				t.Fatal(err)
			}
			totalDeleteBatches++
			deletedForRun += deleted
			if remaining == 0 {
				break
			}
			if deleted == 0 {
				t.Fatalf("sample=%d delete made no progress with remaining=%d", sample, remaining)
			}
		}
		if deletedForRun != snapshots {
			t.Fatalf("sample=%d deleted=%d want=%d", sample, deletedForRun, snapshots)
		}
		totalDeleted += deletedForRun
		var status string
		if err := database.runtime.QueryRow(ctx, `SELECT
			public.control_complete_account_inventory_compaction_v1(
				$1,$2,decode($3,'hex'))->>'status'`, runID, fence, checksum).Scan(&status); err != nil || status != "completed" {
			t.Fatalf("sample=%d compaction status=%q err=%v", sample, status, err)
		}
	}
	if totalDeleteBatches < 20 {
		t.Fatalf("delete batches=%d want at least 20", totalDeleteBatches)
	}

	var plannedRollups int
	if err := database.runtime.QueryRow(ctx, `SELECT
		(public.control_plan_account_inventory_history_v1(1000)->>'rollup_runs_created')::integer`).
		Scan(&plannedRollups); err != nil || plannedRollups != samples {
		t.Fatalf("planned rollups=%d err=%v", plannedRollups, err)
	}
	rollupDurations := make([]time.Duration, 0, samples)
	for sample := 0; sample < samples; sample++ {
		var runID, fence uuid.UUID
		if err := database.runtime.QueryRow(ctx, `SELECT rollup_run_id,fencing_token
			FROM public.control_claim_account_inventory_daily_rollup_v1($1,300)`, uuid.New()).
			Scan(&runID, &fence); err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		var status string
		if err := database.runtime.QueryRow(ctx, `SELECT
			public.control_finalize_account_inventory_daily_rollup_v1($1,$2)->>'status'`, runID, fence).
			Scan(&status); err != nil || status != "completed" {
			t.Fatalf("sample=%d rollup status=%q err=%v", sample, status, err)
		}
		rollupDurations = append(rollupDurations, time.Since(started))
	}
	close(stopSampling)
	<-samplingDone

	var walBytes, databaseBytes, indexBytes, blocksRead, blocksHit, tempBytes, deadlocks int64
	if err := database.owner.QueryRow(ctx, `SELECT
		pg_wal_lsn_diff(pg_current_wal_lsn(),$1::pg_lsn)::bigint,
		pg_database_size(current_database())::bigint,
		coalesce((SELECT sum(pg_indexes_size(c.oid)) FROM pg_class c
			JOIN pg_namespace n ON n.oid=c.relnamespace
			WHERE n.nspname='public' AND c.relkind IN ('r','p')),0)::bigint,
		d.blks_read,d.blks_hit,d.temp_bytes,d.deadlocks
		FROM pg_stat_database d WHERE d.datname=current_database()`, walStart).Scan(
		&walBytes, &databaseBytes, &indexBytes, &blocksRead, &blocksHit, &tempBytes, &deadlocks); err != nil {
		t.Fatal(err)
	}
	deadlockDelta := deadlocks - deadlocksStart
	if deadlockDelta != 0 {
		t.Fatalf("deadlock delta=%d", deadlockDelta)
	}

	var compactions, rollups, remainingSnapshots, accountRollups, providerRollups int
	if err := database.owner.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM account_inventory_compaction_runs WHERE status='completed'),
		(SELECT count(*) FROM account_inventory_daily_rollup_runs WHERE status='completed'),
		(SELECT count(*) FROM account_inventory_snapshot_items),
		(SELECT count(*) FROM account_inventory_daily_account_rollups),
		(SELECT count(*) FROM account_inventory_daily_provider_rollups)`).Scan(
		&compactions, &rollups, &remainingSnapshots, &accountRollups, &providerRollups); err != nil {
		t.Fatal(err)
	}
	expectedRows := snapshots * samples
	if compactions != samples || rollups != samples || remainingSnapshots != 0 ||
		accountRollups != expectedRows || providerRollups != samples || totalDeleted != expectedRows {
		t.Fatalf("conservation compactions=%d rollups=%d snapshots=%d account_rollups=%d provider_rollups=%d deleted=%d expected=%d",
			compactions, rollups, remainingSnapshots, accountRollups, providerRollups, totalDeleted, expectedRows)
	}

	summaryP50, summaryP95, summaryP99 := readonlyQueryCapacityPercentiles(summaryDurations)
	rollupP50, rollupP95, rollupP99 := readonlyQueryCapacityPercentiles(rollupDurations)
	rssEnd := historyCapacityMaxRSS(t)
	t.Logf("history_capacity_evidence=%s scale=%s snapshots_per_sample=%d samples=%d delete_batches=%d summary_p50_ms=%d summary_p95_ms=%d summary_p99_ms=%d rollup_p50_ms=%d rollup_p95_ms=%d rollup_p99_ms=%d database_bytes=%d database_growth_bytes=%d index_bytes=%d index_growth_bytes=%d wal_bytes=%d harness_max_rss_before_bytes=%d harness_max_rss_after_bytes=%d blocks_read=%d blocks_hit=%d temp_bytes=%d max_lock_waiters=%d deadlock_delta=%d source_rows=%d deleted_rows=%d rollup_rows=%d conservation=passed",
		evidence, scaleName, snapshots, samples, totalDeleteBatches,
		summaryP50.Milliseconds(), summaryP95.Milliseconds(), summaryP99.Milliseconds(),
		rollupP50.Milliseconds(), rollupP95.Milliseconds(), rollupP99.Milliseconds(),
		databaseBytes, databaseBytes-databaseBytesStart, indexBytes, indexBytes-indexBytesStart,
		walBytes, rssStart, rssEnd, blocksRead-blocksReadStart, blocksHit-blocksHitStart,
		tempBytes-tempBytesStart, maximumLockWaiters.Load(), deadlockDelta,
		expectedRows, totalDeleted, accountRollups)
}

func historyCapacityMaxRSS(t *testing.T) uint64 {
	t.Helper()
	var usage unix.Rusage
	if err := unix.Getrusage(unix.RUSAGE_SELF, &usage); err != nil || usage.Maxrss < 0 {
		t.Fatalf("read max RSS: value=%d err=%v", usage.Maxrss, err)
	}
	value := uint64(usage.Maxrss)
	if runtime.GOOS != "darwin" {
		value *= 1024
	}
	return value
}
