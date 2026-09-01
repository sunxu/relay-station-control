package historyprocessacceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

const (
	processURLEnvironment      = "CONTROL_HISTORY_PROCESS_URL"
	runtimeDatabaseEnvironment = "CONTROL_HISTORY_PROCESS_RUNTIME_DATABASE_URL"
	ownerDatabaseEnvironment   = "CONTROL_HISTORY_PROCESS_OWNER_DATABASE_URL"
	expectedReasonEnvironment  = "CONTROL_HISTORY_PROCESS_EXPECT_REASON"
	instanceIDEnvironment      = "CONTROL_HISTORY_PROCESS_INSTANCE_ID"
	summaryDateEnvironment     = "CONTROL_HISTORY_PROCESS_SUMMARY_DATE"
	providerEnvironment        = "CONTROL_HISTORY_PROCESS_PROVIDER"
	restartInstanceEnvironment = "CONTROL_HISTORY_PROCESS_RESTART_INSTANCE_ID"
	restartDateEnvironment     = "CONTROL_HISTORY_PROCESS_RESTART_SUMMARY_DATE"
	restartProviderEnvironment = "CONTROL_HISTORY_PROCESS_RESTART_PROVIDER"
	restartMatrixEnvironment   = "CONTROL_HISTORY_PROCESS_RESTART_PHASE_MATRIX"
	networkCounterEnvironment  = "CONTROL_HISTORY_PROCESS_NETWORK_COUNTER_ENDPOINT"
	forbiddenMarkerEnvironment = "CONTROL_HISTORY_PROCESS_FORBIDDEN_MARKER"
	expectedPhaseEnvironment   = "CONTROL_HISTORY_PROCESS_EXPECT_PHASE"
	sourceSnapshotsEnvironment = "CONTROL_HISTORY_PROCESS_SOURCE_SNAPSHOTS"
	faultStageEnvironment      = "CONTROL_HISTORY_PROCESS_FAULT_STAGE"

	processProbeTimeout = 35 * time.Second
	httpRequestTimeout  = 3 * time.Second
)

var derivedHistoryFamilies = []string{
	"relay_control_account_inventory_history_compaction_runs",
	"relay_control_account_inventory_history_rollup_runs",
	"relay_control_account_inventory_history_oldest_eligible_unfinished_seconds",
	"relay_control_account_inventory_history_compaction_failures",
	"relay_control_account_inventory_history_delete_backlog_rows",
	"relay_control_account_inventory_history_delete_rows_total",
	"relay_control_account_inventory_history_delete_duration_seconds_total",
	"relay_control_account_inventory_history_provider_coverage_ratio",
	"relay_control_account_inventory_history_provider_coverage_complete",
}

type databaseMetricsSnapshot struct {
	CompactionRuns                  map[string]int64 `json:"compaction_runs"`
	RollupRuns                      map[string]int64 `json:"rollup_runs"`
	OldestEligibleUnfinishedSeconds float64          `json:"oldest_eligible_unfinished_seconds"`
	Failures                        map[string]int64 `json:"failures"`
	DeleteBacklogRows               int64            `json:"delete_backlog_rows"`
}

type databaseCompatibility struct {
	SchemaVersion                *int  `json:"schema_version"`
	HistoryTableCount            *int  `json:"history_table_count"`
	CoreSHA256                   *bool `json:"core_sha256"`
	CoverageThresholdBasisPoints *int  `json:"coverage_threshold_basis_points"`
	SnapshotMinimumAgeHours      *int  `json:"snapshot_minimum_age_hours"`
	HistoryRetentionDays         *int  `json:"history_retention_days"`
}

type processFixture struct {
	instanceID  uuid.UUID
	summaryDate string
	provider    string
}

type seededZeroPollFixture struct {
	processFixture
	policyID uuid.UUID
	dayStart time.Time
}

func TestAccountInventoryHistoryProcessDisabledCompatibleMetrics(t *testing.T) {
	processURL := requireProcessURL(t)
	runtimePool := requireProcessPool(t, runtimeDatabaseEnvironment)
	ctx, cancel := context.WithTimeout(context.Background(), processProbeTimeout)
	defer cancel()

	var compatible []byte
	if err := runtimePool.QueryRow(ctx,
		`SELECT public.control_history_schema_compatibility_v1()`).Scan(&compatible); err != nil {
		t.Fatal("history process compatibility probe failed")
	}
	var compatibility databaseCompatibility
	if decodeStrictJSON(compatible, &compatibility) != nil || !validCompatibility(compatibility) {
		t.Fatal("history process compatibility result invalid")
	}
	var databaseEncoded []byte
	if err := runtimePool.QueryRow(ctx,
		`SELECT public.control_account_inventory_history_metrics_snapshot_v1()`).Scan(&databaseEncoded); err != nil {
		t.Fatal("history process database metrics read failed")
	}
	var databaseSnapshot databaseMetricsSnapshot
	if json.Unmarshal(databaseEncoded, &databaseSnapshot) != nil ||
		!validDatabaseSnapshot(databaseSnapshot) {
		t.Fatal("history process database metrics shape invalid")
	}

	families, _ := readProcessMetrics(t, ctx, processURL)
	requireMetricValue(t, families, "relay_control_account_inventory_history_enabled",
		map[string]string{"reason": "disabled"}, 0)
	for state, value := range databaseSnapshot.CompactionRuns {
		requireMetricValue(t, families, "relay_control_account_inventory_history_compaction_runs",
			map[string]string{"state": state}, float64(value))
	}
	for state, value := range databaseSnapshot.RollupRuns {
		requireMetricValue(t, families, "relay_control_account_inventory_history_rollup_runs",
			map[string]string{"state": state}, float64(value))
	}
	for phase, value := range databaseSnapshot.Failures {
		requireMetricValue(t, families, "relay_control_account_inventory_history_compaction_failures",
			map[string]string{"failed_from": phase}, float64(value))
	}
	requireMetricValue(t, families, "relay_control_account_inventory_history_delete_backlog_rows",
		nil, float64(databaseSnapshot.DeleteBacklogRows))
	oldest, exists := metricValue(families,
		"relay_control_account_inventory_history_oldest_eligible_unfinished_seconds", nil)
	if !exists || math.Abs(oldest-databaseSnapshot.OldestEligibleUnfinishedSeconds) > 10 {
		t.Fatal("history process oldest metric mismatch")
	}
	for _, result := range []string{"success", "failure"} {
		if _, exists := metricValue(families,
			"relay_control_account_inventory_history_delete_rows_total", map[string]string{"result": result}); !exists {
			t.Fatal("history process delete rows metric missing")
		}
		if _, exists := metricValue(families,
			"relay_control_account_inventory_history_delete_duration_seconds_total", map[string]string{"result": result}); !exists {
			t.Fatal("history process delete duration metric missing")
		}
	}
}

func TestAccountInventoryHistoryProcessMetricsFailureIsolation(t *testing.T) {
	processURL := requireProcessURL(t)
	expectedReason := requireExpectedReason(t)
	ctx, cancel := context.WithTimeout(context.Background(), processProbeTimeout)
	defer cancel()

	families, body := readProcessMetrics(t, ctx, processURL)
	enabled, exists := metricValue(families, "relay_control_account_inventory_history_enabled",
		map[string]string{"reason": expectedReason})
	wantedEnabled := 0.0
	if expectedReason == "ready" {
		wantedEnabled = 1
	}
	if !exists || enabled != wantedEnabled {
		t.Fatal("history process enabled metric missing during provider failure")
	}
	if _, exists := families["relay_control_assets"]; !exists {
		t.Fatal("history process provider failure poisoned unrelated metrics")
	}
	for _, family := range derivedHistoryFamilies {
		if _, exists := families[family]; exists {
			t.Fatal("history process provider failure exposed derived metrics")
		}
	}
	marker := os.Getenv(forbiddenMarkerEnvironment)
	if marker != "" && strings.Contains(body, marker) {
		t.Fatal("history process metrics leaked provider error")
	}
}

func TestAccountInventoryHistoryProcessEnabledConvergesEligibleSource(t *testing.T) {
	processURL := requireProcessURL(t)
	ownerPool := requireProcessPool(t, ownerDatabaseEnvironment)
	fixture := requireProcessFixture(t)
	restartMatrix := os.Getenv(restartMatrixEnvironment)
	if restartMatrix != "" && restartMatrix != "false" && restartMatrix != "true" {
		t.Fatal("history process restart phase matrix configuration invalid")
	}
	ctx, cancel := context.WithTimeout(context.Background(), processProbeTimeout)
	defer cancel()

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		converged, sourceSnapshots := false, int64(0)
		if restartMatrix == "true" {
			converged = historyRestartPhaseMatrixConverged(t, ctx, ownerPool, fixture)
		} else {
			converged, sourceSnapshots = historyFixtureConverged(
				t, ctx, ownerPool, fixture, requireSourceSnapshots(t),
			)
		}
		if converged {
			families, _ := readProcessMetrics(t, ctx, processURL)
			if enabled, exists := metricValue(families,
				"relay_control_account_inventory_history_enabled", map[string]string{"reason": "ready"}); exists && enabled == 1 && enabledFixtureMetricsVisible(families, fixture, sourceSnapshots) {
				return
			}
		}
		select {
		case <-ctx.Done():
			if restartMatrix == "true" {
				t.Fatal("history process enabled convergence timed out class=restart_phase_matrix")
			}
			t.Fatalf("history process enabled convergence timed out class=%s",
				historyFixtureStateClass(ownerPool, fixture))
		case <-ticker.C:
		}
	}
}

func historyFixtureStateClass(pool *pgxpool.Pool, fixture processFixture) string {
	ctx, cancel := context.WithTimeout(context.Background(), httpRequestTimeout)
	defer cancel()
	var compactionPending, compactionSummarized, compactionDeleting int64
	var compactionCompleted, compactionFailed int64
	var rollupPending, rollupCompleted, rollupFailed int64
	err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM public.account_inventory_compaction_runs WHERE summary_date=$1::date AND instance_id=$2 AND status='pending'),
		(SELECT count(*) FROM public.account_inventory_compaction_runs WHERE summary_date=$1::date AND instance_id=$2 AND status='summarized'),
		(SELECT count(*) FROM public.account_inventory_compaction_runs WHERE summary_date=$1::date AND instance_id=$2 AND status='deleting'),
		(SELECT count(*) FROM public.account_inventory_compaction_runs WHERE summary_date=$1::date AND instance_id=$2 AND status='completed'),
		(SELECT count(*) FROM public.account_inventory_compaction_runs WHERE summary_date=$1::date AND instance_id=$2 AND status='failed'),
		(SELECT count(*) FROM public.account_inventory_daily_rollup_runs WHERE summary_date=$1::date AND instance_id=$2 AND status='pending'),
		(SELECT count(*) FROM public.account_inventory_daily_rollup_runs WHERE summary_date=$1::date AND instance_id=$2 AND status='completed'),
		(SELECT count(*) FROM public.account_inventory_daily_rollup_runs WHERE summary_date=$1::date AND instance_id=$2 AND status='failed')`,
		fixture.summaryDate, fixture.instanceID).Scan(
		&compactionPending, &compactionSummarized, &compactionDeleting,
		&compactionCompleted, &compactionFailed,
		&rollupPending, &rollupCompleted, &rollupFailed,
	)
	if err != nil {
		return "database_unavailable"
	}
	switch {
	case compactionFailed > 0:
		return "compaction_failed"
	case compactionDeleting > 0:
		return "compaction_deleting"
	case compactionSummarized > 0:
		return "compaction_summarized"
	case compactionPending > 0:
		return "compaction_pending"
	case compactionCompleted == 0:
		return "compaction_missing"
	case rollupFailed > 0:
		return "rollup_failed"
	case rollupPending > 0:
		return "rollup_pending"
	case rollupCompleted == 0:
		return "rollup_missing"
	default:
		return "metrics_missing"
	}
}

// TestAccountInventoryHistoryProcessSeedEligibleSource is run by the process
// acceptance shell before Control starts. The production finalize function
// intentionally timestamps observations with the database clock, so a
// backdated poll cannot be made valid without a test clock. Seed only a real
// historical policy/monitoring intersection: the production planner must
// create its zero-poll segment and final rollup without disabling immutable
// evidence triggers or inventing historical observations.
func TestAccountInventoryHistoryProcessSeedEligibleSource(t *testing.T) {
	if !processSeedConfigured() {
		t.Skip("history process seed acceptance is not enabled")
	}
	ownerPool := requireProcessPool(t, ownerDatabaseEnvironment)
	fixture := requireProcessFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), processProbeTimeout)
	defer cancel()

	sourceSnapshots := requireSourceSnapshots(t)
	seeded := seedZeroPollActivationFixture(t, ctx, ownerPool, fixture, sourceSnapshots)

	var policyActivations, monitoringActivations, pollRows, snapshotRows, existingRuns int
	if err := ownerPool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM public.provider_inventory_policy_activations AS activation
		 WHERE activation.policy_version_id=$1 AND activation.effective_from=$3),
		(SELECT count(*) FROM public.relay_node_inventory_monitoring_activations AS activation
		 WHERE activation.instance_id=$2 AND activation.effective_from=$3),
		(SELECT count(*) FROM public.account_inventory_poll_runs AS poll
		 WHERE poll.instance_id=$2
		   AND poll.scheduled_at >= $3
		   AND poll.scheduled_at < $3 + interval '1 day'),
		(SELECT count(*) FROM public.account_inventory_snapshot_items AS item
		 JOIN public.account_inventory_poll_runs AS poll ON poll.poll_run_id=item.poll_run_id
		 WHERE poll.instance_id=$2),
		(SELECT count(*) FROM public.account_inventory_compaction_runs AS run
		 WHERE run.summary_date=$3::date AND run.instance_id=$2
		   AND run.provider_policy_version=$1)`, seeded.policyID, fixture.instanceID,
		seeded.dayStart).Scan(
		&policyActivations, &monitoringActivations, &pollRows, &snapshotRows, &existingRuns,
	); err != nil {
		t.Fatal("history process seed verification failed")
	}
	expectedPollRows := 0
	if sourceSnapshots > 0 {
		expectedPollRows = 1
	}
	if policyActivations != 1 || monitoringActivations != 1 || pollRows != expectedPollRows ||
		snapshotRows != sourceSnapshots || existingRuns != 0 {
		t.Fatal("history process seed evidence invalid")
	}
}

func TestAccountInventoryHistoryProcessStaleFenceHasZeroImpact(t *testing.T) {
	if !processSeedConfigured() {
		t.Skip("history process stale fence acceptance is not enabled")
	}
	ownerPool := requireProcessPool(t, ownerDatabaseEnvironment)
	runtimePool := requireProcessPool(t, runtimeDatabaseEnvironment)
	fixture := requireProcessFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), processProbeTimeout)
	defer cancel()

	var planned int
	if err := runtimePool.QueryRow(ctx, `SELECT
		(public.control_plan_account_inventory_history_v1(1)
		 ->>'compaction_runs_created')::integer`).Scan(&planned); err != nil || planned != 1 {
		t.Fatal("history process stale fence planner failed")
	}
	worker := uuid.New()
	var runID, oldFence uuid.UUID
	if err := runtimePool.QueryRow(ctx, `SELECT compaction_run_id,fencing_token
		FROM public.control_claim_account_inventory_compaction_v1($1,5)`, worker).
		Scan(&runID, &oldFence); err != nil {
		t.Fatal("history process stale fence old claim failed")
	}
	timer := time.NewTimer(5100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		t.Fatal("history process stale fence lease wait timed out")
	case <-timer.C:
	}
	var reconciled int
	if err := runtimePool.QueryRow(ctx, `SELECT
		(public.control_reconcile_account_inventory_compactions_v1(1)
		 ->>'failed_count')::integer`).Scan(&reconciled); err != nil || reconciled != 1 {
		t.Fatal("history process stale fence reconcile failed")
	}
	var claimedRunID, newFence uuid.UUID
	if err := runtimePool.QueryRow(ctx, `SELECT compaction_run_id,fencing_token
		FROM public.control_claim_account_inventory_compaction_v1($1,5)`, uuid.New()).
		Scan(&claimedRunID, &newFence); err != nil || claimedRunID != runID || newFence == oldFence {
		t.Fatal("history process stale fence new claim failed")
	}
	var before, after string
	if err := ownerPool.QueryRow(ctx, `SELECT to_jsonb(run)::text
		FROM public.account_inventory_compaction_runs AS run
		WHERE compaction_run_id=$1`, runID).Scan(&before); err != nil {
		t.Fatal("history process stale fence fingerprint read failed")
	}
	_, err := runtimePool.Exec(ctx, `SELECT
		public.control_summarize_account_inventory_compaction_v1($1,$2)`, runID, oldFence)
	var databaseError *pgconn.PgError
	if !errors.As(err, &databaseError) || databaseError.Code != "P0002" {
		t.Fatal("history process stale fence was not rejected")
	}
	var valid bool
	if err := ownerPool.QueryRow(ctx, `SELECT to_jsonb(run)::text,
		run.status='pending' AND run.attempt_count=2 AND run.fencing_token=$2
		AND run.instance_id=$3 AND run.summary_date=$4::date
		AND (SELECT count(*)=2 FROM public.account_inventory_snapshot_items AS item
			JOIN public.account_inventory_poll_runs AS poll ON poll.poll_run_id=item.poll_run_id
			WHERE poll.instance_id=run.instance_id)
		AND (SELECT count(*)=0 FROM public.account_inventory_daily_provider_summaries
			WHERE compaction_run_id=run.compaction_run_id)
		AND (SELECT count(*)=0 FROM public.account_inventory_daily_summaries
			WHERE compaction_run_id=run.compaction_run_id)
		FROM public.account_inventory_compaction_runs AS run
		WHERE compaction_run_id=$1`, runID, newFence, fixture.instanceID, fixture.summaryDate).
		Scan(&after, &valid); err != nil {
		t.Fatal("history process stale fence verification failed")
	}
	if !valid || before != after {
		t.Fatal("history process stale fence changed source or run state")
	}
}

// TestAccountInventoryHistoryProcessSeedClaimedForRestart leaves three real
// compaction claims at pending, summarized, and deleting with bounded leases.
// The acceptance shell restarts Control and PostgreSQL before they converge.
func TestAccountInventoryHistoryProcessSeedClaimedForRestart(t *testing.T) {
	if !processRestartSeedConfigured() {
		t.Skip("history process restart seed acceptance is not enabled")
	}
	ownerPool := requireProcessPool(t, ownerDatabaseEnvironment)
	runtimePool := requireProcessPool(t, runtimeDatabaseEnvironment)
	fixture := requireRestartProcessFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), processProbeTimeout)
	defer cancel()

	seeded := seedZeroPollActivationFixture(t, ctx, ownerPool, fixture, 0)
	var planned int
	if err := runtimePool.QueryRow(ctx, `SELECT
		(public.control_plan_account_inventory_history_v1(1000)
			->>'compaction_runs_created')::integer`).Scan(&planned); err != nil {
		t.Fatal("history process restart planner failed")
	}
	if planned != 3 {
		t.Fatal("history process restart planner did not create phase matrix")
	}

	for index, phase := range []string{"pending", "summarized", "deleting"} {
		workerToken := uuid.New()
		var runID, claimedInstanceID, claimedPolicyID, fencingToken uuid.UUID
		var summaryDate, leaseExpiresAt time.Time
		var status, claimOwner string
		if err := runtimePool.QueryRow(ctx, `SELECT
			compaction_run_id,instance_id,provider_policy_version,summary_date,
			status,claim_owner,fencing_token,lease_expires_at
			FROM public.control_claim_account_inventory_compaction_v1($1,20)`, workerToken).Scan(
			&runID, &claimedInstanceID, &claimedPolicyID, &summaryDate,
			&status, &claimOwner, &fencingToken, &leaseExpiresAt,
		); err != nil {
			t.Fatal("history process restart claim failed")
		}
		if runID == uuid.Nil || claimedInstanceID != fixture.instanceID ||
			claimedPolicyID != seeded.policyID || !summaryDate.Equal(seeded.dayStart.AddDate(0, 0, index)) ||
			status != "pending" || claimOwner != workerToken.String() || fencingToken == uuid.Nil ||
			leaseExpiresAt.IsZero() {
			t.Fatal("history process restart claim target invalid")
		}
		if phase != "pending" {
			if err := runtimePool.QueryRow(ctx, `SELECT
				public.control_summarize_account_inventory_compaction_v1($1,$2)->>'status'`,
				runID, fencingToken).Scan(&status); err != nil || status != "summarized" {
				t.Fatal("history process restart summarize failed")
			}
		}
		if phase == "deleting" {
			if err := runtimePool.QueryRow(ctx, `SELECT
				public.control_delete_account_inventory_snapshot_batch_v1($1,$2,1)->>'status'`,
				runID, fencingToken).Scan(&status); err != nil || status != "deleting" {
				t.Fatal("history process restart delete failed")
			}
		}
	}

	var validMatrix bool
	if err := ownerPool.QueryRow(ctx, `WITH target AS (
		SELECT * FROM public.account_inventory_compaction_runs
		WHERE instance_id=$2 AND summary_date BETWEEN $1::date AND $1::date+2
	) SELECT
		(SELECT count(*)=3 AND count(DISTINCT summary_date)=3
		 AND count(*) FILTER (WHERE status='pending')=1
		 AND count(*) FILTER (WHERE status='summarized')=1
		 AND count(*) FILTER (WHERE status='deleting')=1
		 AND bool_and(claim_owner IS NOT NULL AND fencing_token IS NOT NULL
			AND lease_expires_at > clock_timestamp()
			AND lease_expires_at <= clock_timestamp()+interval '21 seconds'
			AND attempt_count=1) FROM target)
		AND (SELECT count(*)=2 FROM public.account_inventory_daily_provider_summaries AS summary
			JOIN target ON target.compaction_run_id=summary.compaction_run_id)
		AND (SELECT count(*)=0 FROM public.account_inventory_daily_summaries AS summary
			JOIN target ON target.compaction_run_id=summary.compaction_run_id)
		AND (SELECT count(*)=0 FROM public.account_inventory_daily_rollup_runs
			WHERE instance_id=$2 AND summary_date BETWEEN $1::date AND $1::date+2)`,
		seeded.dayStart, fixture.instanceID).Scan(&validMatrix); err != nil || !validMatrix {
		t.Fatal("history process restart phase matrix verification failed")
	}
}

func TestAccountInventoryHistoryProcessHeldTransactionPoolExhaustion(t *testing.T) {
	if !processSeedConfigured() {
		t.Skip("history process held transaction acceptance is not enabled")
	}
	ownerPool := requireProcessPool(t, ownerDatabaseEnvironment)
	fixture := requireProcessFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), processProbeTimeout)
	defer cancel()

	var valid bool
	if err := ownerPool.QueryRow(ctx, `WITH target AS (
		SELECT * FROM public.account_inventory_compaction_runs
		WHERE summary_date=$1::date AND instance_id=$2
	), control_connection AS (
		SELECT * FROM pg_stat_activity
		WHERE datname=current_database() AND application_name='history_process_control'
	) SELECT
		(SELECT count(*)=1 AND bool_and(status='pending' AND attempt_count=1
			AND claim_owner IS NOT NULL AND fencing_token IS NOT NULL
			AND lease_expires_at>clock_timestamp()) FROM target)
		AND (SELECT count(*)=1 FROM control_connection)
		AND (SELECT count(*)=1 FROM control_connection
			WHERE state='active' AND wait_event_type='Lock'
			  AND query LIKE '%control_summarize_account_inventory_compaction_v1%')`,
		fixture.summaryDate, fixture.instanceID).Scan(&valid); err != nil || !valid {
		t.Fatal("history process held transaction or single-connection exhaustion not observed")
	}
}

func TestAccountInventoryHistoryProcessClaimRetainedUntilLeaseExpiry(t *testing.T) {
	if !processSeedConfigured() {
		t.Skip("history process held transaction acceptance is not enabled")
	}
	ownerPool := requireProcessPool(t, ownerDatabaseEnvironment)
	fixture := requireProcessFixture(t)
	expectedPhase := os.Getenv(expectedPhaseEnvironment)
	if expectedPhase != "pending" && expectedPhase != "summarized" {
		t.Fatal("history process expected retained phase invalid")
	}
	ctx, cancel := context.WithTimeout(context.Background(), processProbeTimeout)
	defer cancel()

	var valid bool
	if err := ownerPool.QueryRow(ctx, `SELECT count(*)=1 AND bool_and(
		status=$3 AND attempt_count=1 AND claim_owner IS NOT NULL
		AND fencing_token IS NOT NULL AND lease_expires_at>clock_timestamp())
		FROM public.account_inventory_compaction_runs
		WHERE summary_date=$1::date AND instance_id=$2`,
		fixture.summaryDate, fixture.instanceID, expectedPhase).Scan(&valid); err != nil || !valid {
		t.Fatal("history process unexpired claim was changed or taken over")
	}
}

func TestAccountInventoryHistoryProcessReconcilerRecoveredClaim(t *testing.T) {
	if !processSeedConfigured() {
		t.Skip("history process held transaction acceptance is not enabled")
	}
	ownerPool := requireProcessPool(t, ownerDatabaseEnvironment)
	fixture := requireProcessFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), processProbeTimeout)
	defer cancel()

	var valid bool
	if err := ownerPool.QueryRow(ctx, `SELECT
		(SELECT count(*)=1 AND bool_and(status='completed' AND attempt_count=2
			AND claim_owner IS NULL AND fencing_token IS NULL AND lease_expires_at IS NULL)
		 FROM public.account_inventory_compaction_runs
		 WHERE summary_date=$1::date AND instance_id=$2)
		AND (SELECT count(*)=1 AND bool_and(status='completed')
		 FROM public.account_inventory_daily_rollup_runs
		 WHERE summary_date=$1::date AND instance_id=$2)`,
		fixture.summaryDate, fixture.instanceID).Scan(&valid); err != nil || !valid {
		t.Fatal("history process reconciler did not recover the retained claim exactly once")
	}
}

func TestAccountInventoryHistoryProcessHeldTransactionTimeoutIsAtomic(t *testing.T) {
	if !processSeedConfigured() {
		t.Skip("history process held transaction acceptance is not enabled")
	}
	ownerPool := requireProcessPool(t, ownerDatabaseEnvironment)
	fixture := requireProcessFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), processProbeTimeout)
	defer cancel()
	expectedSourceSnapshots := requireSourceSnapshots(t)

	var runValid, historyEmpty, auditValid, sourcePreserved bool
	if err := ownerPool.QueryRow(ctx, `WITH target AS (
		SELECT * FROM public.account_inventory_compaction_runs
		WHERE summary_date=$1::date AND instance_id=$2
	) SELECT
		(SELECT count(*)=1 AND bool_and(status='failed' AND failed_from='pending'
			AND failure_reason='statement_timeout' AND attempt_count=1
			AND claim_owner IS NULL AND fencing_token IS NULL AND lease_expires_at IS NULL
			AND source_checksum IS NULL AND source_snapshot_count IS NULL) FROM target),
		(SELECT count(*)=0 FROM public.account_inventory_daily_provider_summaries AS summary
			JOIN target ON target.compaction_run_id=summary.compaction_run_id)
		AND (SELECT count(*)=0 FROM public.account_inventory_daily_summaries AS summary
			JOIN target ON target.compaction_run_id=summary.compaction_run_id)
		AND (SELECT count(*)=0 FROM public.account_inventory_daily_rollup_runs
			WHERE summary_date=$1::date AND instance_id=$2),
		(SELECT count(*)=1 FROM public.audit_logs
			WHERE category='account_inventory_history'
			  AND action='account_inventory_history.failed'
			  AND details->>'instance'=$2::uuid::text
			  AND details->>'summary_date'=$1::date::text
			  AND details->>'phase'='fail_pending')
		AND (SELECT count(*)=0 FROM public.audit_logs
			WHERE category='account_inventory_history'
			  AND action IN ('account_inventory_history.summarized','account_inventory_history.completed')
			  AND details->>'instance'=$2::uuid::text
			  AND details->>'summary_date'=$1::date::text),
		(SELECT count(*)=$3 FROM public.account_inventory_snapshot_items AS item
			JOIN public.account_inventory_poll_runs AS poll ON poll.poll_run_id=item.poll_run_id
			WHERE poll.instance_id=$2)`, fixture.summaryDate, fixture.instanceID,
		expectedSourceSnapshots).Scan(
		&runValid, &historyEmpty, &auditValid, &sourcePreserved,
	); err != nil {
		t.Fatal("history process held transaction timeout verification failed")
	}
	if !runValid {
		t.Fatal("history process held transaction timeout run state invalid")
	}
	if !historyEmpty {
		t.Fatal("history process held transaction timeout left partial history")
	}
	if !auditValid {
		t.Fatal("history process held transaction timeout audit invalid")
	}
	if !sourcePreserved {
		t.Fatal("history process held transaction timeout changed source")
	}
}

func TestAccountInventoryHistoryProcessTerminalInternalPreservesSource(t *testing.T) {
	if !processSeedConfigured() || os.Getenv(processURLEnvironment) == "" {
		t.Skip("history process terminal internal acceptance is not enabled")
	}
	processURL := requireProcessURL(t)
	ownerPool := requireProcessPool(t, ownerDatabaseEnvironment)
	fixture := requireProcessFixture(t)
	faultStage := os.Getenv(faultStageEnvironment)
	if faultStage == "" {
		faultStage = "compaction"
	}
	if faultStage != "compaction" && faultStage != "planner" &&
		faultStage != "rollup" && faultStage != "retention" {
		t.Fatal("history process fault stage invalid")
	}
	ctx, cancel := context.WithTimeout(context.Background(), processProbeTimeout)
	defer cancel()

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		var valid bool
		var err error
		switch faultStage {
		case "compaction":
			err = ownerPool.QueryRow(ctx, `WITH target AS (
			SELECT * FROM public.account_inventory_compaction_runs
			WHERE summary_date=$1::date AND instance_id=$2
		) SELECT
			(SELECT count(*)=1 AND bool_and(status='failed' AND failed_from='pending'
				AND failure_reason='internal' AND attempt_count=1
				AND claim_owner IS NULL AND fencing_token IS NULL AND lease_expires_at IS NULL
				AND source_checksum IS NULL AND source_snapshot_count IS NULL) FROM target)
			AND (SELECT count(*)=2 FROM public.account_inventory_snapshot_items AS item
				JOIN public.account_inventory_poll_runs AS poll ON poll.poll_run_id=item.poll_run_id
				WHERE poll.instance_id=$2)
			AND (SELECT count(*)=0 FROM public.account_inventory_daily_provider_summaries AS summary
				JOIN target ON target.compaction_run_id=summary.compaction_run_id)
			AND (SELECT count(*)=0 FROM public.account_inventory_daily_summaries AS summary
				JOIN target ON target.compaction_run_id=summary.compaction_run_id)
			AND (SELECT count(*)=0 FROM public.account_inventory_daily_rollup_runs
				WHERE summary_date=$1::date AND instance_id=$2)
			AND (SELECT count(*)=1 FROM public.audit_logs
				WHERE category='account_inventory_history'
				  AND action='account_inventory_history.failed'
				  AND details->>'instance'=$2::uuid::text
				  AND details->>'summary_date'=$1::date::text
				  AND details->>'phase'='fail_pending')`, fixture.summaryDate, fixture.instanceID).
				Scan(&valid)
		case "planner", "retention":
			err = ownerPool.QueryRow(ctx, `SELECT
			(SELECT count(*)=2 FROM public.account_inventory_snapshot_items AS item
				JOIN public.account_inventory_poll_runs AS poll ON poll.poll_run_id=item.poll_run_id
				WHERE poll.instance_id=$2)
			AND (SELECT count(*)=0 FROM public.account_inventory_compaction_runs
				WHERE summary_date=$1::date AND instance_id=$2 AND status='completed')
			AND (SELECT count(*)=0 FROM public.account_inventory_daily_rollup_runs
				WHERE summary_date=$1::date AND instance_id=$2 AND status='completed')
			AND (SELECT count(*)=0 FROM public.audit_logs
				WHERE category='account_inventory_history'
				  AND action IN ('account_inventory_history.failed',
					'account_inventory_history.summarized','account_inventory_history.completed')
				  AND details->>'instance'=$2::uuid::text
				  AND details->>'summary_date'=$1::date::text)`,
				fixture.summaryDate, fixture.instanceID).Scan(&valid)
		case "rollup":
			err = ownerPool.QueryRow(ctx, `WITH target AS (
			SELECT * FROM public.account_inventory_compaction_runs
			WHERE summary_date=$1::date AND instance_id=$2
		) SELECT
			(SELECT count(*)=1 AND bool_and(status='completed' AND source_snapshot_count=2
				AND deleted_snapshot_count=2 AND octet_length(source_checksum)=32) FROM target)
			AND (SELECT count(*)=0 FROM public.account_inventory_snapshot_items AS item
				JOIN public.account_inventory_poll_runs AS poll ON poll.poll_run_id=item.poll_run_id
				WHERE poll.instance_id=$2)
			AND (SELECT count(*)=1 FROM public.account_inventory_daily_provider_summaries AS summary
				JOIN target ON target.compaction_run_id=summary.compaction_run_id)
			AND (SELECT count(*)=2 FROM public.account_inventory_daily_summaries AS summary
				JOIN target ON target.compaction_run_id=summary.compaction_run_id)
			AND (SELECT count(*)=1 AND bool_and(status='pending'
				AND failure_reason IS NULL AND claim_owner IS NOT NULL
				AND fencing_token IS NOT NULL AND lease_expires_at IS NOT NULL)
				FROM public.account_inventory_daily_rollup_runs
				WHERE summary_date=$1::date AND instance_id=$2)
			AND (SELECT count(*)=0 FROM public.audit_logs
				WHERE category='account_inventory_history'
				  AND action='account_inventory_history.failed'
				  AND details->>'instance'=$2::uuid::text
				  AND details->>'summary_date'=$1::date::text
				  AND details->>'phase'='rollup_fail_pending')`,
				fixture.summaryDate, fixture.instanceID).Scan(&valid)
		}
		if err == nil && valid {
			families, _ := readProcessMetrics(t, ctx, processURL)
			if stopped, exists := metricValue(families,
				"relay_control_account_inventory_history_enabled",
				map[string]string{"reason": "runtime_stopped"}); exists && stopped == 0 {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal("history process terminal internal source preservation timed out")
		case <-ticker.C:
		}
	}
}

func TestAccountInventoryHistoryProcessSourceBackedRetentionCompleted(t *testing.T) {
	if !processSeedConfigured() {
		t.Skip("history process source-backed retention acceptance is not enabled")
	}
	ownerPool := requireProcessPool(t, ownerDatabaseEnvironment)
	fixture := requireProcessFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), processProbeTimeout)
	defer cancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		var valid bool
		err := ownerPool.QueryRow(ctx, `WITH target AS (
			SELECT * FROM public.account_inventory_compaction_runs
			WHERE summary_date=$1::date AND instance_id=$2
		) SELECT
			(SELECT count(*)=1 AND bool_and(status='completed'
				AND attempt_count>=2 AND source_poll_count=1 AND source_snapshot_count=2
				AND deleted_snapshot_count=2 AND octet_length(source_checksum)=32
				AND claim_owner IS NULL AND fencing_token IS NULL AND lease_expires_at IS NULL)
				FROM target)
			AND (SELECT count(*)=1 FROM public.account_inventory_daily_provider_summaries AS summary
				JOIN target ON target.compaction_run_id=summary.compaction_run_id)
			AND (SELECT count(*)=2 FROM public.account_inventory_daily_summaries AS summary
				JOIN target ON target.compaction_run_id=summary.compaction_run_id)
			AND (SELECT count(*)=1 FROM public.account_inventory_daily_rollup_runs
				WHERE summary_date=$1::date AND instance_id=$2 AND status='completed')
			AND (SELECT count(*)=0 FROM public.account_inventory_poll_runs
				WHERE instance_id=$2 AND scheduled_at >= $1::date
				  AND scheduled_at < $1::date+1)
			AND (SELECT count(*)=1 FROM public.audit_logs
				WHERE category='account_inventory_history'
				  AND action='account_inventory_history.completed'
				  AND details->>'phase'='complete' AND details->>'instance'=$2::uuid::text
				  AND details->>'summary_date'=$1::date::text)`, fixture.summaryDate, fixture.instanceID).
			Scan(&valid)
		if err == nil && valid {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("history process source-backed retention did not complete exactly once")
		case <-ticker.C:
		}
	}
}

func seedZeroPollActivationFixture(
	t *testing.T, ctx context.Context, ownerPool *pgxpool.Pool, fixture processFixture,
	sourceSnapshots int,
) seededZeroPollFixture {
	t.Helper()
	policyID := uuid.New()
	compactInstanceID := strings.ReplaceAll(fixture.instanceID.String(), "-", "")
	nodeType := "history-process-" + compactInstanceID[len(compactInstanceID)-12:]
	const contract = "v1"
	networkCounter := requireNetworkCounterEndpoint(t)

	var dayStart time.Time
	var eligible, retained bool
	if err := ownerPool.QueryRow(ctx, `SELECT
		($1::date::timestamp AT TIME ZONE 'UTC'),
		(($1::date + 1)::timestamp AT TIME ZONE 'UTC')
			<= clock_timestamp() - interval '72 hours',
		(($1::date + 1)::timestamp AT TIME ZONE 'UTC')
			> clock_timestamp() - interval '30 days'`, fixture.summaryDate).Scan(
		&dayStart, &eligible, &retained,
	); err != nil {
		t.Fatal("history process seed date calculation failed")
	}
	if !eligible || !retained && sourceSnapshots == 0 {
		t.Fatal("history process seed date is outside the eligible retained window")
	}

	transaction, err := ownerPool.Begin(ctx)
	if err != nil {
		t.Fatal("history process seed transaction failed")
	}
	defer func() { _ = transaction.Rollback(context.Background()) }()
	if _, err := transaction.Exec(ctx, `SET LOCAL TimeZone = 'UTC'`); err != nil {
		t.Fatal("history process seed transaction failed")
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO public.node_drivers(
		node_type,driver_contract_version,display_name,created_at
	) VALUES($1,$2,'History Process Driver',$3)`, nodeType, contract, dayStart); err != nil {
		t.Fatal("history process seed driver write failed")
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO public.relay_node_assets(
		instance_id,display_name,node_type,driver_contract_version,
		management_endpoint,reader_secret_ref,created_at,updated_at
	) VALUES($1,'History Process Node',$2,$3,$4,
		'docker-secret://synthetic/history-process-reader',$5,$5)`,
		fixture.instanceID, nodeType, contract, networkCounter, dayStart); err != nil {
		t.Fatal("history process seed asset write failed")
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO public.provider_inventory_policy_versions(
		policy_version_id,node_type,driver_contract_version,active_providers,
		out_of_scope_providers,created_by,created_at
	) VALUES($1,$2,$3,ARRAY[$4]::text[],ARRAY[]::text[],
		'history-process-seed',$5)`, policyID, nodeType, contract, fixture.provider, dayStart); err != nil {
		t.Fatal("history process seed policy write failed")
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO public.provider_inventory_policy_bindings(
		node_type,driver_contract_version,policy_version_id,bound_by,bound_at
	) VALUES($1,$2,$3,'history-process-seed',$4)`,
		nodeType, contract, policyID, dayStart); err != nil {
		t.Fatal("history process seed policy binding write failed")
	}
	effectiveTo := any(nil)
	if sourceSnapshots > 0 {
		effectiveTo = dayStart.AddDate(0, 0, 1)
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO public.provider_inventory_policy_activations(
		node_type,driver_contract_version,policy_version_id,effective_from,
		effective_to,activated_by,created_at
	) VALUES($1,$2,$3,$4,$5::timestamptz,'history-process-seed',$4)`,
		nodeType, contract, policyID, dayStart, effectiveTo); err != nil {
		t.Fatal("history process seed policy activation write failed")
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO public.relay_node_inventory_monitoring_activations(
		instance_id,effective_from,effective_to,reason,actor,end_reason,end_actor,
		end_recorded_at,created_at
	) VALUES($1,$2,$3::timestamptz,'reconciliation','history-process-seed',
		CASE WHEN $3::timestamptz IS NULL THEN NULL ELSE 'reconciliation' END,
		CASE WHEN $3::timestamptz IS NULL THEN NULL ELSE 'history-process-seed' END,
		$3,$2)`, fixture.instanceID, dayStart, effectiveTo); err != nil {
		t.Fatal("history process seed monitoring activation write failed")
	}
	if sourceSnapshots > 0 {
		pollID := uuid.New()
		slot := dayStart.Add(12 * time.Hour)
		if _, err := transaction.Exec(ctx, `ALTER TABLE public.account_inventory_poll_provider_results
			DISABLE TRIGGER account_inventory_poll_provider_results_guard;
			ALTER TABLE public.account_inventory_snapshot_items
			DISABLE TRIGGER account_inventory_snapshot_items_insert_guard`); err != nil {
			t.Fatalf("history process source guard disable failed class=%s", processDatabaseErrorClass(err))
		}
		if _, err := transaction.Exec(ctx, `INSERT INTO public.account_inventory_poll_runs(
			poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,
			provider_policy_version,status,attempt_count,max_attempts,poll_start_grace_seconds,
			created_at,first_started_at,last_started_at,finalized_at,observed_at,
			transport_success,response_shape_valid,contract_valid,inventory_mode,
			node_identity_complete,snapshot_complete,degraded,result,reason,
			source_record_count,identifiable_record_count,unidentified_record_count,
			unsupported_provider_count,out_of_scope_provider_count,node_version,node_commit
		) VALUES($1,$2,$3,$4,$5::timestamptz,$6,'finalized',1,2,299,
			$5::timestamptz+interval '1 second',$5::timestamptz+interval '2 seconds',
			$5::timestamptz+interval '2 seconds',$5::timestamptz+interval '4 seconds',
			$5::timestamptz+interval '3 seconds',true,true,true,'runtime',
			true,true,false,'success','none',$7,$7,0,0,0,'unknown','unknown')`,
			pollID, fixture.instanceID, nodeType, contract, slot, policyID, sourceSnapshots); err != nil {
			t.Fatalf("history process source poll write failed class=%s", processDatabaseErrorClass(err))
		}
		if _, err := transaction.Exec(ctx, `INSERT INTO public.account_inventory_poll_provider_results(
			poll_run_id,provider,identifiable_count,missing_identity_count,
			duplicate_identity_count,identity_complete,snapshot_complete,degraded,reason,
			promotion_applied,promotion_skipped_reason
		) VALUES($1,$2,$3,0,0,true,true,false,'complete',true,NULL)`,
			pollID, fixture.provider, sourceSnapshots); err != nil {
			t.Fatalf("history process source provider result write failed class=%s", processDatabaseErrorClass(err))
		}
		for index := range sourceSnapshots {
			account := "history-process-" + strconv.Itoa(index) + "@example.invalid"
			if _, err := transaction.Exec(ctx, `INSERT INTO public.account_inventory_snapshot_items(
				poll_run_id,instance_id,provider,account_key,normalized_email,basic_status,
				success_count,failed_count,recent_request_count,observed_at
			) VALUES($1,$2,$3,$3::text||':'||$4::text,$4,'active',$5,0,0,
				$6::timestamptz+interval '3 seconds')`,
				pollID, fixture.instanceID, fixture.provider, account, index+1, slot); err != nil {
				t.Fatalf("history process source snapshot write failed class=%s", processDatabaseErrorClass(err))
			}
		}
		if _, err := transaction.Exec(ctx, `ALTER TABLE public.account_inventory_poll_provider_results
			ENABLE TRIGGER account_inventory_poll_provider_results_guard;
			ALTER TABLE public.account_inventory_snapshot_items
			ENABLE TRIGGER account_inventory_snapshot_items_insert_guard`); err != nil {
			t.Fatalf("history process source guard enable failed class=%s", processDatabaseErrorClass(err))
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		t.Fatal("history process seed transaction commit failed")
	}
	return seededZeroPollFixture{processFixture: fixture, policyID: policyID, dayStart: dayStart}
}

func requireNetworkCounterEndpoint(t *testing.T) string {
	t.Helper()
	raw := os.Getenv(networkCounterEnvironment)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.RawQuery != "" ||
		parsed.Fragment != "" || parsed.Path != "" || parsed.Port() == "" ||
		parsed.Hostname() != "127.0.0.1" {
		t.Fatal("history process network counter endpoint invalid")
	}
	return raw
}

func processSeedConfigured() bool {
	return os.Getenv(ownerDatabaseEnvironment) != "" &&
		os.Getenv(instanceIDEnvironment) != "" &&
		os.Getenv(summaryDateEnvironment) != "" &&
		os.Getenv(providerEnvironment) != ""
}

func requireSourceSnapshots(t *testing.T) int {
	t.Helper()
	raw := os.Getenv(sourceSnapshotsEnvironment)
	if raw == "" {
		return 0
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value != 0 && value != 2 {
		t.Fatal("history process source snapshot configuration invalid")
	}
	return value
}

func processRestartSeedConfigured() bool {
	return os.Getenv(ownerDatabaseEnvironment) != "" &&
		os.Getenv(runtimeDatabaseEnvironment) != "" &&
		os.Getenv(restartInstanceEnvironment) != "" &&
		os.Getenv(restartDateEnvironment) != "" &&
		os.Getenv(restartProviderEnvironment) != ""
}

func requireProcessURL(t *testing.T) string {
	t.Helper()
	raw := os.Getenv(processURLEnvironment)
	if raw == "" {
		t.Skip("history process acceptance is not enabled")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.RawQuery != "" ||
		parsed.Fragment != "" || parsed.Path != "" && parsed.Path != "/" || parsed.Port() == "" {
		t.Fatal("history process URL is invalid")
	}
	host := net.ParseIP(parsed.Hostname())
	if host == nil || !host.IsLoopback() || parsed.Hostname() != "127.0.0.1" {
		t.Fatal("history process URL is not loopback")
	}
	return strings.TrimSuffix(raw, "/")
}

func requireProcessPool(t *testing.T, environmentName string) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv(environmentName)
	if databaseURL == "" {
		t.Fatal("history process database configuration missing")
	}
	configuration, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal("history process database configuration invalid")
	}
	configuration.MaxConns = 1
	ctx, cancel := context.WithTimeout(context.Background(), httpRequestTimeout)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, configuration)
	if err != nil {
		t.Fatal("history process database unavailable")
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatal("history process database unavailable")
	}
	return pool
}

func requireExpectedReason(t *testing.T) string {
	t.Helper()
	reason := os.Getenv(expectedReasonEnvironment)
	switch reason {
	case "disabled", "ready", "runtime_stopped":
		return reason
	default:
		t.Fatal("history process expected reason invalid")
		return ""
	}
}

func requireProcessFixture(t *testing.T) processFixture {
	t.Helper()
	return requireProcessFixtureFromEnvironment(
		t, instanceIDEnvironment, summaryDateEnvironment, providerEnvironment,
	)
}

func requireRestartProcessFixture(t *testing.T) processFixture {
	t.Helper()
	return requireProcessFixtureFromEnvironment(
		t, restartInstanceEnvironment, restartDateEnvironment, restartProviderEnvironment,
	)
}

func requireProcessFixtureFromEnvironment(
	t *testing.T, instanceEnvironment, dateEnvironment, fixtureProviderEnvironment string,
) processFixture {
	t.Helper()
	instanceID, err := uuid.Parse(os.Getenv(instanceEnvironment))
	summaryDate := os.Getenv(dateEnvironment)
	provider := os.Getenv(fixtureProviderEnvironment)
	if err != nil || instanceID == uuid.Nil || !validDate(summaryDate) || !validProvider(provider) {
		t.Fatal("history process fixture configuration invalid")
	}
	return processFixture{instanceID: instanceID, summaryDate: summaryDate, provider: provider}
}

func readProcessMetrics(
	t *testing.T, ctx context.Context, processURL string,
) (map[string]*dto.MetricFamily, string) {
	t.Helper()
	requestContext, cancel := context.WithTimeout(ctx, httpRequestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, processURL+"/metrics", nil)
	if err != nil {
		t.Fatal("history process metrics request invalid")
	}
	client := &http.Client{Timeout: httpRequestTimeout}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal("history process metrics request failed")
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, 2<<20)
	body, err := io.ReadAll(limited)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatal("history process metrics response failed")
	}
	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(bytes.NewReader(body))
	if err != nil {
		t.Fatal("history process metrics response invalid")
	}
	return families, string(body)
}

func metricValue(
	families map[string]*dto.MetricFamily, name string, wantedLabels map[string]string,
) (float64, bool) {
	family, exists := families[name]
	if !exists {
		return 0, false
	}
	for _, metric := range family.Metric {
		labels := make(map[string]string, len(metric.Label))
		for _, label := range metric.Label {
			labels[label.GetName()] = label.GetValue()
		}
		matched := len(labels) == len(wantedLabels)
		for key, value := range wantedLabels {
			matched = matched && labels[key] == value
		}
		if !matched {
			continue
		}
		if metric.Gauge != nil {
			return metric.Gauge.GetValue(), true
		}
		if metric.Counter != nil {
			return metric.Counter.GetValue(), true
		}
	}
	return 0, false
}

func requireMetricValue(
	t *testing.T, families map[string]*dto.MetricFamily, name string,
	labels map[string]string, wanted float64,
) {
	t.Helper()
	value, exists := metricValue(families, name, labels)
	if !exists || value != wanted {
		t.Fatal("history process metric mismatch")
	}
}

func validDatabaseSnapshot(snapshot databaseMetricsSnapshot) bool {
	if len(snapshot.CompactionRuns) != 5 || len(snapshot.RollupRuns) != 3 ||
		len(snapshot.Failures) != 3 || snapshot.OldestEligibleUnfinishedSeconds < 0 ||
		snapshot.DeleteBacklogRows < 0 {
		return false
	}
	for _, key := range []string{"pending", "summarized", "deleting", "completed", "failed"} {
		if snapshot.CompactionRuns[key] < 0 {
			return false
		}
	}
	for _, key := range []string{"pending", "completed", "failed"} {
		if snapshot.RollupRuns[key] < 0 {
			return false
		}
	}
	for _, key := range []string{"pending", "summarized", "deleting"} {
		if snapshot.Failures[key] < 0 {
			return false
		}
	}
	return true
}

func validCompatibility(value databaseCompatibility) bool {
	return value.SchemaVersion != nil && *value.SchemaVersion == 1 &&
		value.HistoryTableCount != nil && *value.HistoryTableCount == 7 &&
		value.CoreSHA256 != nil && *value.CoreSHA256 &&
		value.CoverageThresholdBasisPoints != nil && *value.CoverageThresholdBasisPoints == 9500 &&
		value.SnapshotMinimumAgeHours != nil && *value.SnapshotMinimumAgeHours == 72 &&
		value.HistoryRetentionDays != nil && *value.HistoryRetentionDays == 30
}

func decodeStrictJSON(encoded []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == io.EOF {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("trailing JSON value")
}

func historyFixtureConverged(
	t *testing.T, ctx context.Context, pool *pgxpool.Pool, fixture processFixture,
	expectedSourceSnapshots int,
) (bool, int64) {
	t.Helper()
	var completedCompactions, completedRollups, remainingSnapshots int64
	var sourcePolls, sourceSnapshots int64
	err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM public.account_inventory_compaction_runs
		 WHERE summary_date=$1::date AND instance_id=$2 AND status='completed'),
		(SELECT coalesce(sum(source_snapshot_count),0) FROM public.account_inventory_compaction_runs
		 WHERE summary_date=$1::date AND instance_id=$2 AND status='completed'),
		(SELECT coalesce(sum(source_poll_count),0) FROM public.account_inventory_compaction_runs
		 WHERE summary_date=$1::date AND instance_id=$2 AND status='completed'),
		(SELECT count(*) FROM public.account_inventory_daily_rollup_runs
		 WHERE summary_date=$1::date AND instance_id=$2 AND status='completed'),
		(SELECT count(*) FROM public.account_inventory_snapshot_items AS item
		 JOIN public.account_inventory_poll_runs AS poll ON poll.poll_run_id=item.poll_run_id
		 WHERE poll.instance_id=$2
		   AND poll.scheduled_at >= ($1::date::timestamp AT TIME ZONE 'UTC')
		   AND poll.scheduled_at < (($1::date+1)::timestamp AT TIME ZONE 'UTC'))`,
		fixture.summaryDate, fixture.instanceID).Scan(
		&completedCompactions, &sourceSnapshots, &sourcePolls, &completedRollups, &remainingSnapshots,
	)
	if err != nil {
		t.Fatalf("history process convergence database read failed class=%s", processDatabaseErrorClass(err))
	}
	expectedSourcePolls := int64(0)
	if expectedSourceSnapshots > 0 {
		expectedSourcePolls = 1
	}
	return completedCompactions == 1 && completedRollups == 1 && remainingSnapshots == 0 &&
			sourcePolls == expectedSourcePolls && sourceSnapshots == int64(expectedSourceSnapshots),
		sourceSnapshots
}

func historyRestartPhaseMatrixConverged(
	t *testing.T, ctx context.Context, pool *pgxpool.Pool, fixture processFixture,
) bool {
	t.Helper()
	var converged bool
	err := pool.QueryRow(ctx, `WITH target AS (
		SELECT * FROM public.account_inventory_compaction_runs
		WHERE instance_id=$2::uuid AND summary_date BETWEEN $1::date AND $1::date+2
	) SELECT
		(SELECT count(*)=3 AND count(DISTINCT summary_date)=3
		 AND count(*) FILTER (WHERE status='completed' AND attempt_count=2
			AND source_poll_count=0 AND source_snapshot_count=0
			AND deleted_snapshot_count=0 AND claim_owner IS NULL
			AND lease_expires_at IS NULL AND fencing_token IS NULL)=3 FROM target)
		AND (SELECT count(*)=3 FROM public.account_inventory_daily_provider_summaries AS summary
			JOIN target ON target.compaction_run_id=summary.compaction_run_id)
		AND (SELECT count(*)=0 FROM public.account_inventory_daily_summaries AS summary
			JOIN target ON target.compaction_run_id=summary.compaction_run_id)
		AND (SELECT count(*)=3 AND count(DISTINCT summary_date)=3
			FROM public.account_inventory_daily_rollup_runs
			WHERE instance_id=$2 AND summary_date BETWEEN $1::date AND $1::date+2
			  AND status='completed')
		AND (SELECT count(*)=3 FROM public.account_inventory_daily_provider_rollups
			WHERE instance_id=$2 AND summary_date BETWEEN $1::date AND $1::date+2)
		AND (SELECT count(*)=0 FROM public.account_inventory_daily_account_rollups
			WHERE instance_id=$2 AND summary_date BETWEEN $1::date AND $1::date+2)
		AND (SELECT count(*)=3 FROM public.audit_logs
			WHERE category='account_inventory_history'
			  AND action='account_inventory_history.summarized'
			  AND details->>'instance'=$2::uuid::text
			  AND (details->>'summary_date')::date BETWEEN $1::date AND $1::date+2)
		AND (SELECT count(*)=3 FROM public.audit_logs
			WHERE category='account_inventory_history'
			  AND action='account_inventory_history.completed'
			  AND details->>'phase'='complete' AND details->>'instance'=$2::uuid::text
			  AND (details->>'summary_date')::date BETWEEN $1::date AND $1::date+2)
		AND (SELECT count(*)=3 FROM public.audit_logs
			WHERE category='account_inventory_history'
			  AND action='account_inventory_history.completed'
			  AND details->>'phase'='rollup_complete' AND details->>'instance'=$2::uuid::text
			  AND (details->>'summary_date')::date BETWEEN $1::date AND $1::date+2)`,
		fixture.summaryDate, fixture.instanceID).Scan(&converged)
	if err != nil {
		t.Fatalf("history process convergence database read failed class=%s", processDatabaseErrorClass(err))
	}
	return converged
}

func enabledFixtureMetricsVisible(
	families map[string]*dto.MetricFamily, fixture processFixture, sourceSnapshots int64,
) bool {
	compactions, compactionsExist := metricValue(families,
		"relay_control_account_inventory_history_compaction_runs", map[string]string{"state": "completed"})
	rollups, rollupsExist := metricValue(families,
		"relay_control_account_inventory_history_rollup_runs", map[string]string{"state": "completed"})
	deleted, deletedExists := metricValue(families,
		"relay_control_account_inventory_history_delete_rows_total", map[string]string{"result": "success"})
	ratio, ratioExists := metricValue(families,
		"relay_control_account_inventory_history_provider_coverage_ratio",
		map[string]string{"instance_id": fixture.instanceID.String(), "provider": fixture.provider})
	complete, completeExists := metricValue(families,
		"relay_control_account_inventory_history_provider_coverage_complete",
		map[string]string{"instance_id": fixture.instanceID.String(), "provider": fixture.provider})
	backlog, backlogExists := metricValue(families,
		"relay_control_account_inventory_history_delete_backlog_rows", nil)
	return compactionsExist && compactions >= 1 && rollupsExist && rollups >= 1 &&
		deletedExists && deleted >= float64(sourceSnapshots) && ratioExists && ratio >= 0 && ratio <= 1 &&
		completeExists && (complete == 0 || complete == 1) && backlogExists && backlog == 0
}

func validDate(value string) bool {
	parsed, err := time.Parse("2006-01-02", value)
	return err == nil && parsed.Format("2006-01-02") == value
}

func validProvider(value string) bool {
	if len(value) < 1 || len(value) > 64 || !lowerAlphaNumeric(value[0]) {
		return false
	}
	for index := 1; index < len(value); index++ {
		character := value[index]
		if !lowerAlphaNumeric(character) && character != '.' && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func lowerAlphaNumeric(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9'
}

func processDatabaseErrorClass(err error) string {
	var databaseError *pgconn.PgError
	if !errors.As(err, &databaseError) {
		return "database_failure"
	}
	switch databaseError.Code {
	case "23503":
		return "reference_violation"
	case "23505":
		return "duplicate"
	case "23514":
		return "constraint_violation"
	case "42501":
		return "permission_denied"
	case "P0001":
		return "guard_rejected"
	default:
		if len(databaseError.Code) == 5 {
			for _, character := range databaseError.Code {
				if character < '0' || character > '9' && (character < 'A' || character > 'Z') {
					return "database_failure"
				}
			}
			return "sqlstate_" + databaseError.Code
		}
		return "database_failure"
	}
}
