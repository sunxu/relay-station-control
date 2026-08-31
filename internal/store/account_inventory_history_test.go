package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	historyruntime "github.com/sunxu/relay-station-control/internal/historyruntime"
	generated "github.com/sunxu/relay-station-control/internal/store/sqlc"
)

type fakeAccountInventoryHistoryQueries struct {
	compatibility            []byte
	compatibilityErr         error
	plan                     []byte
	planErr                  error
	claim                    generated.AccountInventoryCompactionRun
	claimErr                 error
	renew                    pgtype.UUID
	renewErr                 error
	reconcile                []byte
	reconcileErr             error
	summarize                []byte
	summarizeErr             error
	deleteBatch              []byte
	deleteBatchErr           error
	complete                 []byte
	completeErr              error
	fail                     []byte
	failErr                  error
	rollupClaim              generated.AccountInventoryDailyRollupRun
	rollupClaimErr           error
	rollupRenew              pgtype.UUID
	rollupRenewErr           error
	rollupReconcile          []byte
	rollupReconcileErr       error
	rollupFinalize           []byte
	rollupFinalizeErr        error
	rollupFail               []byte
	rollupFailErr            error
	pollRetention            []byte
	pollRetentionErr         error
	pollRetentionLimit       int32
	rowRetention             []byte
	rowRetentionErr          error
	rowRetentionLimit        int32
	runRetention             []byte
	runRetentionErr          error
	runRetentionLimit        int32
	compactionRetention      []byte
	compactionRetentionErr   error
	compactionRetentionLimit int32
	historyMetrics           []byte
	historyMetricsErr        error
	historyMetricsCalls      int
	metrics                  []generated.ListAccountInventoryHistoryCoverageMetricsRow
	metricsErr               error
}

func (fake *fakeAccountInventoryHistoryQueries) DeleteAccountInventoryPollRetention(
	_ context.Context, limit int32,
) ([]byte, error) {
	fake.pollRetentionLimit = limit
	return fake.pollRetention, fake.pollRetentionErr
}

func (fake *fakeAccountInventoryHistoryQueries) DeleteAccountInventoryRollupRowRetention(
	_ context.Context, limit int32,
) ([]byte, error) {
	fake.rowRetentionLimit = limit
	return fake.rowRetention, fake.rowRetentionErr
}

func (fake *fakeAccountInventoryHistoryQueries) DeleteAccountInventoryRollupRunRetention(
	_ context.Context, limit int32,
) ([]byte, error) {
	fake.runRetentionLimit = limit
	return fake.runRetention, fake.runRetentionErr
}

func (fake *fakeAccountInventoryHistoryQueries) DeleteAccountInventoryCompactionRunRetention(
	_ context.Context, limit int32,
) ([]byte, error) {
	fake.compactionRetentionLimit = limit
	return fake.compactionRetention, fake.compactionRetentionErr
}

func (fake *fakeAccountInventoryHistoryQueries) GetAccountInventoryHistoryMetricsSnapshot(
	context.Context,
) ([]byte, error) {
	fake.historyMetricsCalls++
	return fake.historyMetrics, fake.historyMetricsErr
}

func (fake *fakeAccountInventoryHistoryQueries) ClaimAccountInventoryHistoryDailyRollup(
	context.Context, generated.ClaimAccountInventoryHistoryDailyRollupParams,
) (generated.AccountInventoryDailyRollupRun, error) {
	return fake.rollupClaim, fake.rollupClaimErr
}

func (fake *fakeAccountInventoryHistoryQueries) RenewAccountInventoryHistoryDailyRollup(
	context.Context, generated.RenewAccountInventoryHistoryDailyRollupParams,
) (pgtype.UUID, error) {
	return fake.rollupRenew, fake.rollupRenewErr
}

func (fake *fakeAccountInventoryHistoryQueries) ReconcileAccountInventoryHistoryDailyRollups(
	context.Context, int32,
) ([]byte, error) {
	return fake.rollupReconcile, fake.rollupReconcileErr
}

func (fake *fakeAccountInventoryHistoryQueries) FinalizeAccountInventoryHistoryDailyRollup(
	context.Context, generated.FinalizeAccountInventoryHistoryDailyRollupParams,
) ([]byte, error) {
	return fake.rollupFinalize, fake.rollupFinalizeErr
}

func (fake *fakeAccountInventoryHistoryQueries) FailAccountInventoryHistoryDailyRollup(
	context.Context, generated.FailAccountInventoryHistoryDailyRollupParams,
) ([]byte, error) {
	return fake.rollupFail, fake.rollupFailErr
}

func (fake *fakeAccountInventoryHistoryQueries) RenewAccountInventoryHistoryCompaction(
	context.Context, generated.RenewAccountInventoryHistoryCompactionParams,
) (pgtype.UUID, error) {
	return fake.renew, fake.renewErr
}

func (fake *fakeAccountInventoryHistoryQueries) ReconcileAccountInventoryHistoryCompactions(
	context.Context, int32,
) ([]byte, error) {
	return fake.reconcile, fake.reconcileErr
}

func (fake *fakeAccountInventoryHistoryQueries) SummarizeAccountInventoryHistoryCompaction(
	context.Context, generated.SummarizeAccountInventoryHistoryCompactionParams,
) ([]byte, error) {
	return fake.summarize, fake.summarizeErr
}

func (fake *fakeAccountInventoryHistoryQueries) DeleteAccountInventoryHistorySnapshotBatch(
	context.Context, generated.DeleteAccountInventoryHistorySnapshotBatchParams,
) ([]byte, error) {
	return fake.deleteBatch, fake.deleteBatchErr
}

func (fake *fakeAccountInventoryHistoryQueries) CompleteAccountInventoryHistoryCompaction(
	context.Context, generated.CompleteAccountInventoryHistoryCompactionParams,
) ([]byte, error) {
	return fake.complete, fake.completeErr
}

func (fake *fakeAccountInventoryHistoryQueries) FailAccountInventoryHistoryCompaction(
	context.Context, generated.FailAccountInventoryHistoryCompactionParams,
) ([]byte, error) {
	return fake.fail, fake.failErr
}

func (fake *fakeAccountInventoryHistoryQueries) CheckAccountInventoryHistoryCompatibility(context.Context) ([]byte, error) {
	return fake.compatibility, fake.compatibilityErr
}

func (fake *fakeAccountInventoryHistoryQueries) PlanAccountInventoryHistory(context.Context, int32) ([]byte, error) {
	return fake.plan, fake.planErr
}

func (fake *fakeAccountInventoryHistoryQueries) ClaimAccountInventoryHistoryCompaction(
	context.Context, generated.ClaimAccountInventoryHistoryCompactionParams,
) (generated.AccountInventoryCompactionRun, error) {
	return fake.claim, fake.claimErr
}

func (fake *fakeAccountInventoryHistoryQueries) ListAccountInventoryHistoryCoverageMetrics(
	context.Context,
) ([]generated.ListAccountInventoryHistoryCoverageMetricsRow, error) {
	return fake.metrics, fake.metricsErr
}

func TestAccountInventoryHistoryCompatibilityFailsClosed(t *testing.T) {
	valid := []byte(`{"schema_version":1,"history_table_count":7,"core_sha256":true,"coverage_threshold_basis_points":9500,"snapshot_minimum_age_hours":72,"history_retention_days":30}`)
	for name, fake := range map[string]*fakeAccountInventoryHistoryQueries{
		"valid":              {compatibility: valid},
		"malformed":          {compatibility: []byte(`{"schema_version":1`)},
		"unknown key":        {compatibility: append(valid[:len(valid)-1], []byte(`,"identity":"must-not-escape"}`)...)},
		"legacy table count": {compatibility: []byte(`{"schema_version":1,"history_table_count":6,"core_sha256":true,"coverage_threshold_basis_points":9500,"snapshot_minimum_age_hours":72,"history_retention_days":30}`)},
		"wrong fixed value":  {compatibility: []byte(`{"schema_version":1,"history_table_count":5,"core_sha256":true,"coverage_threshold_basis_points":9500,"snapshot_minimum_age_hours":72,"history_retention_days":30}`)},
		"database":           {compatibilityErr: &pgconn.PgError{Code: "55000", Message: "catalog-marker"}},
	} {
		t.Run(name, func(t *testing.T) {
			repository := &AccountInventoryHistoryRepository{queries: fake}
			err := repository.CheckHistoryCompatibility(context.Background())
			if name == "valid" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if !errors.Is(err, ErrAccountInventoryHistoryIncompatible) || strings.Contains(fmt.Sprint(err), "marker") {
				t.Fatalf("error=%v", err)
			}
		})
	}
	if err := (*AccountInventoryHistoryRepository)(nil).CheckHistoryCompatibility(context.Background()); !errors.Is(err, ErrAccountInventoryHistoryIncompatible) {
		t.Fatalf("nil repository error=%v", err)
	}
}

func TestAccountInventoryHistoryPlanIsBoundedAndStrict(t *testing.T) {
	repository := &AccountInventoryHistoryRepository{queries: &fakeAccountInventoryHistoryQueries{
		plan: []byte(`{"compaction_runs_created":2,"rollup_runs_created":1}`),
	}}
	result, err := repository.Plan(context.Background(), historyruntime.PlanRequest{Limit: 2})
	if err != nil || result.CompactionRunsCreated != 2 || result.RollupRunsCreated != 1 {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	for _, limit := range []int{0, 1001} {
		if _, err := repository.Plan(context.Background(), historyruntime.PlanRequest{Limit: limit}); !errors.Is(err, ErrInvalidAccountInventoryHistoryInput) {
			t.Fatalf("limit=%d error=%v", limit, err)
		}
	}
	for _, encoded := range [][]byte{
		[]byte(`{}`),
		[]byte(`{"compaction_runs_created":3,"rollup_runs_created":0}`),
		[]byte(`{"compaction_runs_created":1,"rollup_runs_created":3}`),
		[]byte(`{"compaction_runs_created":1,"rollup_runs_created":0,"policy":"forbidden"}`),
	} {
		repository.queries = &fakeAccountInventoryHistoryQueries{plan: encoded}
		if _, err := repository.Plan(context.Background(), historyruntime.PlanRequest{Limit: 2}); !errors.Is(err, ErrAccountInventoryHistoryInconsistent) {
			t.Fatalf("encoded=%s error=%v", encoded, err)
		}
	}
}

func TestAccountInventoryHistoryCompactionClaimMapping(t *testing.T) {
	worker := uuid.New()
	for _, status := range []AccountInventoryHistoryCompactionStatus{
		AccountInventoryHistoryCompactionPending,
		AccountInventoryHistoryCompactionSummarized,
		AccountInventoryHistoryCompactionDeleting,
	} {
		t.Run(string(status), func(t *testing.T) {
			row := validAccountInventoryHistoryClaimRow(worker, status)
			repository := &AccountInventoryHistoryRepository{queries: &fakeAccountInventoryHistoryQueries{claim: row}}
			claim, err := repository.ClaimCompaction(context.Background(), historyruntime.ClaimRequest{WorkerToken: worker, Lease: 30 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			if string(claim.Status) != string(status) || claim.FencingToken == uuid.Nil ||
				claim.SummaryDate.Location() != time.UTC {
				t.Fatalf("unexpected claim: %+v", claim)
			}
			if status == AccountInventoryHistoryCompactionPending && len(claim.SourceChecksum) != 0 {
				t.Fatal("pending claim exposed absent source proof")
			}
			if status != AccountInventoryHistoryCompactionPending &&
				len(claim.SourceChecksum) != 32 {
				t.Fatal("summarized claim lost source proof")
			}
		})
	}
}

func TestAccountInventoryHistoryCompactionClaimRejectsInvalidShapes(t *testing.T) {
	worker := uuid.New()
	tests := map[string]func(*generated.AccountInventoryCompactionRun){
		"worker":           func(row *generated.AccountInventoryCompactionRun) { row.ClaimOwner.String = uuid.NewString() },
		"fencing":          func(row *generated.AccountInventoryCompactionRun) { row.FencingToken = pgtype.UUID{} },
		"attempt":          func(row *generated.AccountInventoryCompactionRun) { row.AttemptCount = 0 },
		"checksum":         func(row *generated.AccountInventoryCompactionRun) { row.SourceChecksum = []byte("short") },
		"negative count":   func(row *generated.AccountInventoryCompactionRun) { row.SourceSnapshotCount.Int64 = -1 },
		"deleted overflow": func(row *generated.AccountInventoryCompactionRun) { row.DeletedSnapshotCount = 2 },
		"deleted poll":     func(row *generated.AccountInventoryCompactionRun) { row.DeletedPollCount = 1 },
		"deleted provider": func(row *generated.AccountInventoryCompactionRun) { row.DeletedProviderResultCount = 1 },
		"deleted duplicate": func(row *generated.AccountInventoryCompactionRun) {
			row.DeletedDuplicateCount = 1
		},
		"phase": func(row *generated.AccountInventoryCompactionRun) { row.Status = "completed" },
		"failed": func(row *generated.AccountInventoryCompactionRun) {
			row.FailedFrom = pgtype.Text{String: "pending", Valid: true}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			row := validAccountInventoryHistoryClaimRow(worker, AccountInventoryHistoryCompactionSummarized)
			mutate(&row)
			repository := &AccountInventoryHistoryRepository{queries: &fakeAccountInventoryHistoryQueries{claim: row}}
			if _, err := repository.ClaimCompaction(context.Background(), historyruntime.ClaimRequest{WorkerToken: worker, Lease: 30 * time.Second}); !errors.Is(err, ErrAccountInventoryHistoryInconsistent) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	for _, lease := range []time.Duration{time.Second, 301 * time.Second, 5500 * time.Millisecond} {
		repository := &AccountInventoryHistoryRepository{queries: &fakeAccountInventoryHistoryQueries{}}
		if _, err := repository.ClaimCompaction(context.Background(), historyruntime.ClaimRequest{WorkerToken: worker, Lease: lease}); !errors.Is(err, ErrInvalidAccountInventoryHistoryInput) {
			t.Fatalf("lease=%s error=%v", lease, err)
		}
	}
	repository := &AccountInventoryHistoryRepository{queries: &fakeAccountInventoryHistoryQueries{claimErr: pgx.ErrNoRows}}
	if _, err := repository.ClaimCompaction(context.Background(), historyruntime.ClaimRequest{WorkerToken: worker, Lease: 30 * time.Second}); !errors.Is(err, ErrAccountInventoryHistoryNoWork) {
		t.Fatalf("no work error=%v", err)
	}
}

func TestAccountInventoryHistoryCoverageMetricsFailClosed(t *testing.T) {
	instanceID := uuid.New()
	valid := []generated.ListAccountInventoryHistoryCoverageMetricsRow{
		{InstanceID: nullableUUID(instanceID), Provider: "openai", CoverageRatio: 0.95, CoverageComplete: true},
		{InstanceID: nullableUUID(instanceID), Provider: "claude", CoverageRatio: 0.5, CoverageComplete: false},
	}
	repository := &AccountInventoryHistoryRepository{queries: &fakeAccountInventoryHistoryQueries{metrics: valid}}
	metrics, err := repository.CoverageMetrics(context.Background())
	if err != nil || len(metrics) != 2 || metrics[0].InstanceID != instanceID {
		t.Fatalf("metrics=%+v error=%v", metrics, err)
	}
	for name, mutate := range map[string]func(*generated.ListAccountInventoryHistoryCoverageMetricsRow){
		"instance":  func(row *generated.ListAccountInventoryHistoryCoverageMetricsRow) { row.InstanceID = pgtype.UUID{} },
		"provider":  func(row *generated.ListAccountInventoryHistoryCoverageMetricsRow) { row.Provider = "OpenAI" },
		"ratio":     func(row *generated.ListAccountInventoryHistoryCoverageMetricsRow) { row.CoverageRatio = 1.01 },
		"threshold": func(row *generated.ListAccountInventoryHistoryCoverageMetricsRow) { row.CoverageComplete = false },
	} {
		t.Run(name, func(t *testing.T) {
			row := valid[0]
			mutate(&row)
			repository.queries = &fakeAccountInventoryHistoryQueries{metrics: []generated.ListAccountInventoryHistoryCoverageMetricsRow{row}}
			if _, err := repository.CoverageMetrics(context.Background()); !errors.Is(err, ErrAccountInventoryHistoryInconsistent) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	for name, rows := range map[string][]generated.ListAccountInventoryHistoryCoverageMetricsRow{
		"duplicate": {valid[0], valid[0]},
		"too many instances": func() []generated.ListAccountInventoryHistoryCoverageMetricsRow {
			rows := make([]generated.ListAccountInventoryHistoryCoverageMetricsRow, 51)
			for index := range rows {
				rows[index] = generated.ListAccountInventoryHistoryCoverageMetricsRow{
					InstanceID: nullableUUID(uuid.New()), Provider: "openai",
					CoverageRatio: 1, CoverageComplete: true,
				}
			}
			return rows
		}(),
		"too many providers": func() []generated.ListAccountInventoryHistoryCoverageMetricsRow {
			rows := make([]generated.ListAccountInventoryHistoryCoverageMetricsRow, 65)
			for index := range rows {
				rows[index] = generated.ListAccountInventoryHistoryCoverageMetricsRow{
					InstanceID: nullableUUID(instanceID), Provider: fmt.Sprintf("provider-%d", index),
					CoverageRatio: 1, CoverageComplete: true,
				}
			}
			return rows
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			repository.queries = &fakeAccountInventoryHistoryQueries{metrics: rows}
			if _, err := repository.CoverageMetrics(context.Background()); !errors.Is(err, ErrAccountInventoryHistoryInconsistent) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func validAccountInventoryHistoryMetricsJSON() []byte {
	return []byte(`{"compaction_runs":{"pending":1,"summarized":2,"deleting":3,"completed":4,"failed":5},` +
		`"rollup_runs":{"pending":6,"completed":7,"failed":8},` +
		`"oldest_eligible_unfinished_seconds":9.5,` +
		`"failures":{"pending":10,"summarized":11,"deleting":12},` +
		`"delete_backlog_rows":13}`)
}

func TestAccountInventoryHistoryMetricsSnapshotStatusGateAndMapping(t *testing.T) {
	instanceID := uuid.New()
	fake := &fakeAccountInventoryHistoryQueries{
		historyMetrics: validAccountInventoryHistoryMetricsJSON(),
		metrics: []generated.ListAccountInventoryHistoryCoverageMetricsRow{{
			InstanceID: nullableUUID(instanceID), Provider: "openai",
			CoverageRatio: 0.95, CoverageComplete: true,
		}},
	}
	repository := &AccountInventoryHistoryRepository{queries: fake}
	snapshot, err := repository.AccountInventoryHistoryMetricsSnapshot(context.Background())
	if err != nil || snapshot.Runtime.Reason != historyruntime.ReasonDisabled ||
		snapshot.CompactionRuns != nil || fake.historyMetricsCalls != 0 {
		t.Fatalf("unprobed snapshot=%+v calls=%d err=%v", snapshot, fake.historyMetricsCalls, err)
	}
	repository.ObserveRuntimeStatus(historyruntime.RuntimeStatus{
		Compatible: true, Reason: historyruntime.ReasonDisabled,
	})
	snapshot, err = repository.AccountInventoryHistoryMetricsSnapshot(context.Background())
	if err != nil || snapshot.Runtime.Reason != historyruntime.ReasonDisabled ||
		snapshot.CompactionRuns[historyruntime.CompactionCompleted] != 4 || fake.historyMetricsCalls != 1 {
		t.Fatalf("compatible disabled snapshot=%+v calls=%d err=%v", snapshot, fake.historyMetricsCalls, err)
	}
	repository.ObserveRuntimeStatus(historyruntime.RuntimeStatus{
		Configured: true, Reason: historyruntime.ReasonSchemaIncompatible,
	})
	snapshot, err = repository.AccountInventoryHistoryMetricsSnapshot(context.Background())
	if err != nil || snapshot.Runtime.Reason != historyruntime.ReasonSchemaIncompatible ||
		snapshot.CompactionRuns != nil || fake.historyMetricsCalls != 1 {
		t.Fatalf("incompatible snapshot=%+v calls=%d err=%v", snapshot, fake.historyMetricsCalls, err)
	}
	repository.ObserveRuntimeStatus(historyruntime.RuntimeStatus{
		Configured: true, Enabled: true, Compatible: true, Reason: historyruntime.ReasonReady,
	})
	snapshot, err = repository.AccountInventoryHistoryMetricsSnapshot(context.Background())
	if err != nil || fake.historyMetricsCalls != 2 ||
		snapshot.CompactionRuns[historyruntime.CompactionDeleting] != 3 ||
		snapshot.RollupRuns[historyruntime.RollupCompleted] != 7 ||
		snapshot.OldestEligibleUnfinishedSeconds != 9.5 ||
		snapshot.Failures[historyruntime.FailedFromDeleting] != 12 ||
		snapshot.DeleteBacklogRows != 13 || snapshot.DeleteRows[historyruntime.DeleteSuccess] != 0 ||
		snapshot.DeleteDurationSeconds[historyruntime.DeleteFailure] != 0 ||
		len(snapshot.ProviderCoverage) != 1 || snapshot.ProviderCoverage[0].InstanceID != instanceID {
		t.Fatalf("ready snapshot=%+v calls=%d err=%v", snapshot, fake.historyMetricsCalls, err)
	}
}

func TestAccountInventoryHistoryRetentionMetricsObserveAdaptersWithoutGuessingFailedRows(t *testing.T) {
	fake := &fakeAccountInventoryHistoryQueries{
		deleteBatch:     []byte(`{"status":"deleting","deleted_count":2,"remaining_count":1,"total_deleted_count":2}`),
		pollRetention:   []byte(`{"processed_count":2,"deleted_row_count":3}`),
		rowRetentionErr: &pgconn.PgError{Code: "57014", Message: "duration-canary"},
		historyMetrics:  validAccountInventoryHistoryMetricsJSON(),
	}
	repository := &AccountInventoryHistoryRepository{queries: fake}
	if _, err := repository.DeleteSnapshotBatch(context.Background(), historyruntime.DeleteBatchRequest{
		RunID: uuid.New(), FencingToken: uuid.New(), Limit: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.DeletePollRetention(context.Background(), historyruntime.RetentionRequest{Limit: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.DeleteRollupRowRetention(context.Background(), historyruntime.RetentionRequest{Limit: 2}); !errors.Is(err, ErrAccountInventoryHistoryTimeout) {
		t.Fatalf("failure error=%v", err)
	}
	repository.ObserveRuntimeStatus(historyruntime.RuntimeStatus{
		Configured: true, Enabled: true, Compatible: true, Reason: historyruntime.ReasonReady,
	})
	snapshot, err := repository.AccountInventoryHistoryMetricsSnapshot(context.Background())
	if err != nil || snapshot.DeleteRows[historyruntime.DeleteSuccess] != 5 ||
		snapshot.DeleteRows[historyruntime.DeleteFailure] != 0 ||
		snapshot.DeleteDurationSeconds[historyruntime.DeleteSuccess] < 0 ||
		snapshot.DeleteDurationSeconds[historyruntime.DeleteFailure] < 0 {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
}

func TestAccountInventoryHistoryRetentionMetricsAreRaceSafe(t *testing.T) {
	repository := &AccountInventoryHistoryRepository{}
	var workers sync.WaitGroup
	for index := 0; index < 64; index++ {
		workers.Add(1)
		go func(failed bool) {
			defer workers.Done()
			var err error
			if failed {
				err = ErrAccountInventoryHistoryTimeout
			}
			repository.observeAccountInventoryHistoryRetention(
				historyruntime.RetentionResult{DeletedRows: 2}, err, time.Millisecond,
			)
		}(index%2 == 0)
	}
	workers.Wait()
	repository.metricsMu.RLock()
	defer repository.metricsMu.RUnlock()
	if repository.deleteSuccessRows != 64 || repository.deleteSuccessDuration <= 0 ||
		repository.deleteFailureDuration <= 0 {
		t.Fatalf("rows=%d success=%f failure=%f", repository.deleteSuccessRows,
			repository.deleteSuccessDuration, repository.deleteFailureDuration)
	}
}

func TestAccountInventoryHistoryMetricsSnapshotRejectsUnsafeJSON(t *testing.T) {
	valid := string(validAccountInventoryHistoryMetricsJSON())
	cases := map[string]string{
		"malformed":       `{"compaction_runs":`,
		"unknown top key": valid[:len(valid)-1] + `,"identity":"canary"}`,
		"unknown state":   strings.Replace(valid, `"pending":1`, `"pending":1,"identity":1`, 1),
		"missing state":   strings.Replace(valid, `"deleting":3,`, ``, 1),
		"null state":      strings.Replace(valid, `"deleting":3`, `"deleting":null`, 1),
		"negative state":  strings.Replace(valid, `"deleting":3`, `"deleting":-1`, 1),
		"null oldest": strings.Replace(valid,
			`"oldest_eligible_unfinished_seconds":9.5`, `"oldest_eligible_unfinished_seconds":null`, 1),
		"negative oldest": strings.Replace(valid,
			`"oldest_eligible_unfinished_seconds":9.5`, `"oldest_eligible_unfinished_seconds":-0.5`, 1),
		"null backlog":     strings.Replace(valid, `"delete_backlog_rows":13`, `"delete_backlog_rows":null`, 1),
		"negative backlog": strings.Replace(valid, `"delete_backlog_rows":13`, `"delete_backlog_rows":-1`, 1),
		"trailing":         valid + ` {}`,
	}
	for name, encoded := range cases {
		t.Run(name, func(t *testing.T) {
			repository := &AccountInventoryHistoryRepository{queries: &fakeAccountInventoryHistoryQueries{
				historyMetrics: []byte(encoded),
			}}
			repository.ObserveRuntimeStatus(historyruntime.RuntimeStatus{
				Configured: true, Enabled: true, Compatible: true, Reason: historyruntime.ReasonReady,
			})
			if _, err := repository.AccountInventoryHistoryMetricsSnapshot(context.Background()); !errors.Is(err, ErrAccountInventoryHistoryInconsistent) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestAccountInventoryHistoryMetricsSnapshotErrorsAreRedacted(t *testing.T) {
	repository := &AccountInventoryHistoryRepository{queries: &fakeAccountInventoryHistoryQueries{
		historyMetricsErr: &pgconn.PgError{Code: "55000", Message: "metrics-secret-canary"},
	}}
	repository.ObserveRuntimeStatus(historyruntime.RuntimeStatus{
		Configured: true, Enabled: true, Compatible: true, Reason: historyruntime.ReasonReady,
	})
	snapshot, err := repository.AccountInventoryHistoryMetricsSnapshot(context.Background())
	if !errors.Is(err, ErrAccountInventoryHistoryInconsistent) || strings.Contains(fmt.Sprint(err), "canary") {
		t.Fatalf("unsafe error=%v", err)
	}
	if snapshot.Runtime.Reason != historyruntime.ReasonReady || !snapshot.Runtime.Enabled ||
		snapshot.CompactionRuns != nil {
		t.Fatalf("provider failure lost runtime isolation status: %+v", snapshot)
	}
}

func TestAccountInventoryHistoryCompactionTransactionsAreStrict(t *testing.T) {
	runID, fence := uuid.New(), uuid.New()
	checksum := make([]byte, 32)
	fake := &fakeAccountInventoryHistoryQueries{
		renew:     nullableUUID(runID),
		reconcile: []byte(`{"failed_count":2}`),
		summarize: []byte(`{"status":"summarized","source_rows":9,"source_snapshot_count":4,"source_checksum_hex":"` +
			strings.Repeat("00", 32) + `","account_segment_count":3,"provider_segment_count":2}`),
		deleteBatch: []byte(`{"status":"deleting","deleted_count":2,"remaining_count":2,"total_deleted_count":2}`),
		complete:    []byte(`{"status":"completed","source_checksum_hex":"` + strings.Repeat("00", 32) + `","idempotent":false,"failure_reason":null}`),
		fail:        []byte(`{"status":"failed","failure_reason":"internal"}`),
	}
	repository := &AccountInventoryHistoryRepository{queries: fake}
	if err := repository.RenewCompaction(context.Background(), historyruntime.LeaseRequest{
		RunID: runID, FencingToken: fence, Lease: 30 * time.Second,
	}); err != nil {
		t.Fatal(err)
	}
	reconciled, err := repository.ReconcileCompactions(context.Background(), historyruntime.ReconcileRequest{Limit: 3})
	if err != nil || reconciled.Reconciled != 2 {
		t.Fatalf("reconcile=%+v err=%v", reconciled, err)
	}
	summarized, err := repository.SummarizeCompaction(context.Background(), historyruntime.FencedRequest{
		RunID: runID, FencingToken: fence,
	})
	if err != nil || summarized.SourceSnapshotCount != 4 || !equalHistoryChecksum(summarized.SourceChecksum, checksum) {
		t.Fatalf("summarize=%+v err=%v", summarized, err)
	}
	deleted, err := repository.DeleteSnapshotBatch(context.Background(), historyruntime.DeleteBatchRequest{
		RunID: runID, FencingToken: fence, Limit: 2,
	})
	if err != nil || deleted.DeletedRows != 2 || deleted.RemainingRows != 2 || deleted.TotalDeletedRows != 2 {
		t.Fatalf("delete=%+v err=%v", deleted, err)
	}
	completed, err := repository.CompleteCompaction(context.Background(), historyruntime.CompleteRequest{
		RunID: runID, FencingToken: fence, ExpectedChecksum: checksum,
	})
	if err != nil || !completed.Completed {
		t.Fatalf("complete=%+v err=%v", completed, err)
	}
	if err := repository.FailCompaction(context.Background(), historyruntime.FailRequest{
		RunID: runID, FencingToken: fence, Reason: historyruntime.FailureInternal,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAccountInventoryHistoryCompactionTransactionsFailClosed(t *testing.T) {
	runID, fence := uuid.New(), uuid.New()
	for name, encoded := range map[string][]byte{
		"unknown": []byte(`{"status":"summarized","source_rows":1,"source_snapshot_count":1,"source_checksum_hex":"` +
			strings.Repeat("00", 32) + `","account_segment_count":1,"provider_segment_count":1,"identity":"forbidden"}`),
		"uppercase checksum": []byte(`{"status":"summarized","source_rows":1,"source_snapshot_count":1,"source_checksum_hex":"` +
			strings.Repeat("AA", 32) + `","account_segment_count":1,"provider_segment_count":1}`),
		"snapshot overflow": []byte(`{"status":"summarized","source_rows":1,"source_snapshot_count":2,"source_checksum_hex":"` +
			strings.Repeat("00", 32) + `","account_segment_count":1,"provider_segment_count":1}`),
	} {
		t.Run(name, func(t *testing.T) {
			repository := &AccountInventoryHistoryRepository{queries: &fakeAccountInventoryHistoryQueries{summarize: encoded}}
			_, err := repository.SummarizeCompaction(context.Background(), historyruntime.FencedRequest{RunID: runID, FencingToken: fence})
			if !errors.Is(err, ErrAccountInventoryHistoryInconsistent) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	for _, malformed := range [][]byte{
		[]byte(`{"status":"deleting"}`),
		[]byte(`{"status":"deleting","deleted_count":3,"remaining_count":0,"total_deleted_count":3}`),
		[]byte(`{"status":"deleting","deleted_count":1,"remaining_count":0,"total_deleted_count":0}`),
	} {
		repository := &AccountInventoryHistoryRepository{queries: &fakeAccountInventoryHistoryQueries{deleteBatch: malformed}}
		if _, err := repository.DeleteSnapshotBatch(context.Background(), historyruntime.DeleteBatchRequest{
			RunID: runID, FencingToken: fence, Limit: 2,
		}); !errors.Is(err, ErrAccountInventoryHistoryInconsistent) {
			t.Fatalf("delete error=%v", err)
		}
	}
	wrongChecksum := strings.Repeat("01", 32)
	repository := &AccountInventoryHistoryRepository{queries: &fakeAccountInventoryHistoryQueries{
		complete: []byte(`{"status":"completed","source_checksum_hex":"` + wrongChecksum + `","idempotent":true,"failure_reason":null}`),
	}}
	if _, err := repository.CompleteCompaction(context.Background(), historyruntime.CompleteRequest{
		RunID: runID, FencingToken: fence, ExpectedChecksum: make([]byte, 32),
	}); !errors.Is(err, ErrAccountInventoryHistoryInconsistent) {
		t.Fatalf("complete error=%v", err)
	}
	repository.queries = &fakeAccountInventoryHistoryQueries{complete: []byte(`{"status":"completed","source_checksum_hex":"` +
		strings.Repeat("00", 32) + `","idempotent":true}`)}
	if _, err := repository.CompleteCompaction(context.Background(), historyruntime.CompleteRequest{
		RunID: runID, FencingToken: fence, ExpectedChecksum: make([]byte, 32),
	}); !errors.Is(err, ErrAccountInventoryHistoryInconsistent) {
		t.Fatalf("complete missing key error=%v", err)
	}
	reason := string(historyruntime.FailureSourceChecksumMismatch)
	repository.queries = &fakeAccountInventoryHistoryQueries{complete: []byte(`{"status":"failed","source_checksum_hex":"` +
		strings.Repeat("00", 32) + `","idempotent":false,"failure_reason":"` + reason + `"}`)}
	if _, err := repository.CompleteCompaction(context.Background(), historyruntime.CompleteRequest{
		RunID: runID, FencingToken: fence, ExpectedChecksum: make([]byte, 32),
	}); !errors.Is(err, historyruntime.ErrHistorySourceChecksumMismatch) {
		t.Fatalf("complete fixed failure error=%v", err)
	}
	repository.queries = &fakeAccountInventoryHistoryQueries{reconcile: []byte(`{}`)}
	if _, err := repository.ReconcileCompactions(context.Background(), historyruntime.ReconcileRequest{Limit: 1}); !errors.Is(err, ErrAccountInventoryHistoryInconsistent) {
		t.Fatalf("reconcile missing key error=%v", err)
	}
	repository.queries = &fakeAccountInventoryHistoryQueries{fail: []byte(`{"status":"failed"}`)}
	if err := repository.FailCompaction(context.Background(), historyruntime.FailRequest{
		RunID: runID, FencingToken: fence, Reason: historyruntime.FailureInternal,
	}); !errors.Is(err, ErrAccountInventoryHistoryInconsistent) {
		t.Fatalf("fail missing key error=%v", err)
	}
	repository.queries = &fakeAccountInventoryHistoryQueries{renew: nullableUUID(uuid.New())}
	if err := repository.RenewCompaction(context.Background(), historyruntime.LeaseRequest{
		RunID: runID, FencingToken: fence, Lease: 30 * time.Second,
	}); !errors.Is(err, ErrAccountInventoryHistoryInconsistent) {
		t.Fatalf("renew mismatched row error=%v", err)
	}
}

func TestAccountInventoryHistoryFailureReasonDictionariesAreExact(t *testing.T) {
	repository := &AccountInventoryHistoryRepository{queries: &fakeAccountInventoryHistoryQueries{}}
	runID, fence := uuid.New(), uuid.New()
	for reason, want := range map[historyruntime.FailureReason][2]bool{
		historyruntime.FailureSourceDayMismatch:       {true, false},
		historyruntime.FailureSourceCountMismatch:     {true, false},
		historyruntime.FailureSourceChecksumMismatch:  {true, false},
		historyruntime.FailureSegmentIncomplete:       {false, true},
		historyruntime.FailureSegmentCountMismatch:    {false, true},
		historyruntime.FailureSegmentChecksumMismatch: {false, true},
		historyruntime.FailureActivationInconsistent:  {true, true},
		historyruntime.FailureStatementTimeout:        {true, true},
		historyruntime.FailureLeaseExpired:            {true, true},
		historyruntime.FailureDatabaseUnavailable:     {true, true},
		historyruntime.FailureInternal:                {true, true},
		"unknown":                                     {false, false},
	} {
		if got := validHistoryCompactionFailureReason(reason); got != want[0] {
			t.Errorf("compaction reason %q valid=%t want=%t", reason, got, want[0])
		}
		if got := validHistoryRollupFailureReason(reason); got != want[1] {
			t.Errorf("rollup reason %q valid=%t want=%t", reason, got, want[1])
		}
		if !want[0] {
			err := repository.FailCompaction(context.Background(), historyruntime.FailRequest{
				RunID: runID, FencingToken: fence, Reason: reason,
			})
			if !errors.Is(err, ErrInvalidAccountInventoryHistoryInput) {
				t.Errorf("compaction adapter accepted reason %q: %v", reason, err)
			}
		}
	}
}

func TestAccountInventoryHistoryRollupClaimMappingAndStrictShapes(t *testing.T) {
	worker := uuid.New()
	row := validAccountInventoryHistoryRollupClaimRow(worker)
	repository := &AccountInventoryHistoryRepository{queries: &fakeAccountInventoryHistoryQueries{rollupClaim: row}}
	claim, err := repository.ClaimRollup(context.Background(), historyruntime.RollupClaimRequest{
		WorkerToken: worker, Lease: 30 * time.Second,
	})
	if err != nil || claim.RunID == uuid.Nil || claim.InstanceID == uuid.Nil ||
		claim.FencingToken == uuid.Nil || claim.Attempt != 1 || claim.SummaryDate.Location() != time.UTC {
		t.Fatalf("claim=%+v error=%v", claim, err)
	}

	mutations := map[string]func(*generated.AccountInventoryDailyRollupRun){
		"worker": func(row *generated.AccountInventoryDailyRollupRun) {
			row.ClaimOwner.String = uuid.NewString()
		},
		"status":  func(row *generated.AccountInventoryDailyRollupRun) { row.Status = "failed" },
		"date":    func(row *generated.AccountInventoryDailyRollupRun) { row.SummaryDate = pgtype.Date{} },
		"fencing": func(row *generated.AccountInventoryDailyRollupRun) { row.FencingToken = pgtype.UUID{} },
		"attempt": func(row *generated.AccountInventoryDailyRollupRun) { row.AttemptCount = 0 },
		"completed fence": func(row *generated.AccountInventoryDailyRollupRun) {
			row.CompletedFencingToken = nullableUUID(uuid.New())
		},
		"counts": func(row *generated.AccountInventoryDailyRollupRun) {
			row.ExpectedSegmentCount = pgtype.Int4{Int32: 1, Valid: true}
		},
		"checksum": func(row *generated.AccountInventoryDailyRollupRun) { row.SegmentChecksum = make([]byte, 32) },
		"lease": func(row *generated.AccountInventoryDailyRollupRun) {
			row.LeaseExpiresAt.Time = row.UpdatedAt.Time
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			invalid := validAccountInventoryHistoryRollupClaimRow(worker)
			mutate(&invalid)
			repository.queries = &fakeAccountInventoryHistoryQueries{rollupClaim: invalid}
			if _, err := repository.ClaimRollup(context.Background(), historyruntime.RollupClaimRequest{
				WorkerToken: worker, Lease: 30 * time.Second,
			}); !errors.Is(err, ErrAccountInventoryHistoryInconsistent) {
				t.Fatalf("error=%v", err)
			}
		})
	}

	repository.queries = &fakeAccountInventoryHistoryQueries{rollupClaimErr: pgx.ErrNoRows}
	if _, err := repository.ClaimRollup(context.Background(), historyruntime.RollupClaimRequest{
		WorkerToken: worker, Lease: 30 * time.Second,
	}); !errors.Is(err, ErrAccountInventoryHistoryNoWork) {
		t.Fatalf("no work error=%v", err)
	}
	for _, request := range []historyruntime.RollupClaimRequest{
		{Lease: 30 * time.Second},
		{WorkerToken: worker, Lease: time.Second},
		{WorkerToken: worker, Lease: 5500 * time.Millisecond},
		{WorkerToken: worker, Lease: 301 * time.Second},
	} {
		if _, err := repository.ClaimRollup(context.Background(), request); !errors.Is(err, ErrInvalidAccountInventoryHistoryInput) {
			t.Fatalf("invalid request=%+v error=%v", request, err)
		}
	}
}

func TestAccountInventoryHistoryRollupTransactionsAreStrict(t *testing.T) {
	runID, fence := uuid.New(), uuid.New()
	checksumHex := strings.Repeat("00", 32)
	fake := &fakeAccountInventoryHistoryQueries{
		rollupRenew:     nullableUUID(runID),
		rollupReconcile: []byte(`{"failed_count":2}`),
		rollupFinalize: []byte(`{"status":"completed","expected_segment_count":2,"completed_segment_count":2,` +
			`"segment_checksum_hex":"` + checksumHex + `","account_rollup_count":1,"provider_rollup_count":1,` +
			`"idempotent":false,"failure_reason":null}`),
		rollupFail: []byte(`{"status":"failed","failure_reason":"internal"}`),
	}
	repository := &AccountInventoryHistoryRepository{queries: fake}
	if err := repository.RenewRollup(context.Background(), historyruntime.RollupLeaseRequest{
		RunID: runID, FencingToken: fence, Lease: 30 * time.Second,
	}); err != nil {
		t.Fatal(err)
	}
	reconciled, err := repository.ReconcileRollups(context.Background(), historyruntime.RollupReconcileRequest{Limit: 3})
	if err != nil || reconciled.Reconciled != 2 {
		t.Fatalf("reconcile=%+v error=%v", reconciled, err)
	}
	finalized, err := repository.FinalizeRollup(context.Background(), historyruntime.RollupFencedRequest{
		RunID: runID, FencingToken: fence,
	})
	if err != nil || !finalized.Completed || finalized.ExpectedSegmentCount != 2 ||
		finalized.CompletedSegmentCount != 2 || len(finalized.SegmentChecksum) != 32 {
		t.Fatalf("finalize=%+v error=%v", finalized, err)
	}
	if err := repository.FailRollup(context.Background(), historyruntime.RollupFailRequest{
		RunID: runID, FencingToken: fence, Reason: historyruntime.FailureInternal,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAccountInventoryHistoryRollupTransactionsFailClosed(t *testing.T) {
	runID, fence := uuid.New(), uuid.New()
	repository := &AccountInventoryHistoryRepository{queries: &fakeAccountInventoryHistoryQueries{}}

	for name, encoded := range map[string][]byte{
		"missing":       []byte(`{}`),
		"unknown":       []byte(`{"failed_count":1,"identity":"forbidden"}`),
		"negative":      []byte(`{"failed_count":-1}`),
		"over limit":    []byte(`{"failed_count":2}`),
		"wrong type":    []byte(`{"failed_count":"1"}`),
		"trailing json": []byte(`{"failed_count":1}{}`),
	} {
		t.Run("reconcile "+name, func(t *testing.T) {
			repository.queries = &fakeAccountInventoryHistoryQueries{rollupReconcile: encoded}
			if _, err := repository.ReconcileRollups(context.Background(), historyruntime.RollupReconcileRequest{Limit: 1}); !errors.Is(err, ErrAccountInventoryHistoryInconsistent) {
				t.Fatalf("error=%v", err)
			}
		})
	}

	baseFinalize := `{"status":"completed","expected_segment_count":2,"completed_segment_count":2,` +
		`"segment_checksum_hex":"` + strings.Repeat("00", 32) + `","account_rollup_count":1,` +
		`"provider_rollup_count":1,"idempotent":false,"failure_reason":null}`
	for name, encoded := range map[string][]byte{
		"missing":            []byte(`{"status":"completed"}`),
		"unknown":            []byte(baseFinalize[:len(baseFinalize)-1] + `,"identity":"forbidden"}`),
		"zero expected":      []byte(strings.Replace(baseFinalize, `"expected_segment_count":2`, `"expected_segment_count":0`, 1)),
		"count mismatch":     []byte(strings.Replace(baseFinalize, `"completed_segment_count":2`, `"completed_segment_count":1`, 1)),
		"uppercase checksum": []byte(strings.Replace(baseFinalize, strings.Repeat("00", 32), strings.Repeat("AA", 32), 1)),
	} {
		t.Run("finalize "+name, func(t *testing.T) {
			repository.queries = &fakeAccountInventoryHistoryQueries{rollupFinalize: encoded}
			if _, err := repository.FinalizeRollup(context.Background(), historyruntime.RollupFencedRequest{
				RunID: runID, FencingToken: fence,
			}); !errors.Is(err, ErrAccountInventoryHistoryInconsistent) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	repository.queries = &fakeAccountInventoryHistoryQueries{rollupFinalize: []byte(
		strings.Replace(baseFinalize, `"provider_rollup_count":1`, `"provider_rollup_count":0`, 1),
	)}
	if result, err := repository.FinalizeRollup(context.Background(), historyruntime.RollupFencedRequest{
		RunID: runID, FencingToken: fence,
	}); err != nil || !result.Completed {
		t.Fatalf("valid zero-Provider finalize=%+v error=%v", result, err)
	}

	failures := map[historyruntime.FailureReason]error{
		historyruntime.FailureSegmentIncomplete:       historyruntime.ErrHistorySegmentIncomplete,
		historyruntime.FailureSegmentCountMismatch:    historyruntime.ErrHistorySegmentCountMismatch,
		historyruntime.FailureSegmentChecksumMismatch: historyruntime.ErrHistorySegmentChecksumMismatch,
		historyruntime.FailureActivationInconsistent:  historyruntime.ErrHistoryActivationInconsistent,
	}
	for reason, want := range failures {
		t.Run("finalize "+string(reason), func(t *testing.T) {
			repository.queries = &fakeAccountInventoryHistoryQueries{rollupFinalize: []byte(
				`{"status":"failed","expected_segment_count":2,"completed_segment_count":1,` +
					`"segment_checksum_hex":"","account_rollup_count":0,"provider_rollup_count":0,` +
					`"idempotent":false,"failure_reason":"` + string(reason) + `"}`,
			)}
			if _, err := repository.FinalizeRollup(context.Background(), historyruntime.RollupFencedRequest{
				RunID: runID, FencingToken: fence,
			}); !errors.Is(err, want) {
				t.Fatalf("error=%v want=%v", err, want)
			}
		})
	}

	repository.queries = &fakeAccountInventoryHistoryQueries{rollupRenew: nullableUUID(uuid.New())}
	if err := repository.RenewRollup(context.Background(), historyruntime.RollupLeaseRequest{
		RunID: runID, FencingToken: fence, Lease: 30 * time.Second,
	}); !errors.Is(err, ErrAccountInventoryHistoryInconsistent) {
		t.Fatalf("renew mismatch error=%v", err)
	}
	repository.queries = &fakeAccountInventoryHistoryQueries{rollupRenewErr: pgx.ErrNoRows}
	if err := repository.RenewRollup(context.Background(), historyruntime.RollupLeaseRequest{
		RunID: runID, FencingToken: fence, Lease: 30 * time.Second,
	}); !errors.Is(err, ErrAccountInventoryHistoryLeaseLost) {
		t.Fatalf("renew no-row error=%v", err)
	}

	for name, encoded := range map[string][]byte{
		"missing":  []byte(`{"status":"failed"}`),
		"mismatch": []byte(`{"status":"failed","failure_reason":"lease_expired"}`),
		"unknown":  []byte(`{"status":"failed","failure_reason":"internal","identity":"forbidden"}`),
	} {
		t.Run("fail "+name, func(t *testing.T) {
			repository.queries = &fakeAccountInventoryHistoryQueries{rollupFail: encoded}
			if err := repository.FailRollup(context.Background(), historyruntime.RollupFailRequest{
				RunID: runID, FencingToken: fence, Reason: historyruntime.FailureInternal,
			}); !errors.Is(err, ErrAccountInventoryHistoryInconsistent) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestAccountInventoryHistoryRetentionAdaptersAreStrictAndBounded(t *testing.T) {
	type adapter struct {
		name      string
		configure func(*fakeAccountInventoryHistoryQueries, []byte, error)
		call      func(*AccountInventoryHistoryRepository, context.Context, historyruntime.RetentionRequest) (historyruntime.RetentionResult, error)
		limit     func(*fakeAccountInventoryHistoryQueries) int32
		valid     []byte
		processed int
		deleted   uint64
	}
	adapters := []adapter{
		{
			name: "poll cascade",
			configure: func(fake *fakeAccountInventoryHistoryQueries, encoded []byte, err error) {
				fake.pollRetention, fake.pollRetentionErr = encoded, err
			},
			call:  (*AccountInventoryHistoryRepository).DeletePollRetention,
			limit: func(fake *fakeAccountInventoryHistoryQueries) int32 { return fake.pollRetentionLimit },
			valid: []byte(`{"processed_count":1,"deleted_row_count":4}`), processed: 1, deleted: 4,
		},
		{
			name: "rollup rows",
			configure: func(fake *fakeAccountInventoryHistoryQueries, encoded []byte, err error) {
				fake.rowRetention, fake.rowRetentionErr = encoded, err
			},
			call:  (*AccountInventoryHistoryRepository).DeleteRollupRowRetention,
			limit: func(fake *fakeAccountInventoryHistoryQueries) int32 { return fake.rowRetentionLimit },
			valid: []byte(`{"processed_count":1,"deleted_row_count":1}`), processed: 1, deleted: 1,
		},
		{
			name: "rollup run",
			configure: func(fake *fakeAccountInventoryHistoryQueries, encoded []byte, err error) {
				fake.runRetention, fake.runRetentionErr = encoded, err
			},
			call:  (*AccountInventoryHistoryRepository).DeleteRollupRunRetention,
			limit: func(fake *fakeAccountInventoryHistoryQueries) int32 { return fake.runRetentionLimit },
			valid: []byte(`{"processed_count":1,"deleted_row_count":1}`), processed: 1, deleted: 1,
		},
		{
			name: "compaction run",
			configure: func(fake *fakeAccountInventoryHistoryQueries, encoded []byte, err error) {
				fake.compactionRetention, fake.compactionRetentionErr = encoded, err
			},
			call:  (*AccountInventoryHistoryRepository).DeleteCompactionRunRetention,
			limit: func(fake *fakeAccountInventoryHistoryQueries) int32 { return fake.compactionRetentionLimit },
			valid: []byte(`{"processed_count":1,"deleted_row_count":1}`), processed: 1, deleted: 1,
		},
	}

	for _, adapter := range adapters {
		t.Run(adapter.name, func(t *testing.T) {
			for _, limit := range []int{1, 5000} {
				fake := &fakeAccountInventoryHistoryQueries{}
				adapter.configure(fake, adapter.valid, nil)
				repository := &AccountInventoryHistoryRepository{queries: fake}
				result, err := adapter.call(repository, context.Background(), historyruntime.RetentionRequest{Limit: limit})
				if err != nil || result.Processed != adapter.processed || result.DeletedRows != adapter.deleted {
					t.Fatalf("limit=%d result=%+v error=%v", limit, result, err)
				}
				if got := adapter.limit(fake); got != int32(limit) {
					t.Fatalf("query limit=%d want=%d", got, limit)
				}
			}

			fake := &fakeAccountInventoryHistoryQueries{}
			adapter.configure(fake, []byte(`{"processed_count":0,"deleted_row_count":0}`), nil)
			result, err := adapter.call(&AccountInventoryHistoryRepository{queries: fake}, context.Background(), historyruntime.RetentionRequest{Limit: 1})
			if err != nil || result != (historyruntime.RetentionResult{}) {
				t.Fatalf("zero result=%+v error=%v", result, err)
			}

			for _, limit := range []int{0, 5001} {
				fake := &fakeAccountInventoryHistoryQueries{}
				adapter.configure(fake, adapter.valid, nil)
				_, err := adapter.call(&AccountInventoryHistoryRepository{queries: fake}, context.Background(), historyruntime.RetentionRequest{Limit: limit})
				if !errors.Is(err, ErrInvalidAccountInventoryHistoryInput) || adapter.limit(fake) != 0 {
					t.Fatalf("invalid limit=%d queryLimit=%d error=%v", limit, adapter.limit(fake), err)
				}
			}
		})
	}
}

func TestAccountInventoryHistoryRetentionAdaptersRejectMalformedResults(t *testing.T) {
	type adapter struct {
		name      string
		configure func(*fakeAccountInventoryHistoryQueries, []byte)
		call      func(*AccountInventoryHistoryRepository, context.Context, historyruntime.RetentionRequest) (historyruntime.RetentionResult, error)
	}
	adapters := []adapter{
		{
			name:      "poll",
			configure: func(fake *fakeAccountInventoryHistoryQueries, encoded []byte) { fake.pollRetention = encoded },
			call:      (*AccountInventoryHistoryRepository).DeletePollRetention,
		},
		{
			name:      "rollup rows",
			configure: func(fake *fakeAccountInventoryHistoryQueries, encoded []byte) { fake.rowRetention = encoded },
			call:      (*AccountInventoryHistoryRepository).DeleteRollupRowRetention,
		},
		{
			name:      "rollup run",
			configure: func(fake *fakeAccountInventoryHistoryQueries, encoded []byte) { fake.runRetention = encoded },
			call:      (*AccountInventoryHistoryRepository).DeleteRollupRunRetention,
		},
		{
			name:      "compaction run",
			configure: func(fake *fakeAccountInventoryHistoryQueries, encoded []byte) { fake.compactionRetention = encoded },
			call:      (*AccountInventoryHistoryRepository).DeleteCompactionRunRetention,
		},
	}
	malformed := map[string][]byte{
		"missing":          []byte(`{}`),
		"missing deleted":  []byte(`{"processed_count":1}`),
		"unknown":          []byte(`{"processed_count":1,"deleted_row_count":1,"identity":"forbidden"}`),
		"processed type":   []byte(`{"processed_count":"1","deleted_row_count":1}`),
		"deleted type":     []byte(`{"processed_count":1,"deleted_row_count":"1"}`),
		"processed null":   []byte(`{"processed_count":null,"deleted_row_count":0}`),
		"deleted null":     []byte(`{"processed_count":0,"deleted_row_count":null}`),
		"negative process": []byte(`{"processed_count":-1,"deleted_row_count":0}`),
		"negative delete":  []byte(`{"processed_count":0,"deleted_row_count":-1}`),
		"over limit":       []byte(`{"processed_count":3,"deleted_row_count":3}`),
		"zero positive":    []byte(`{"processed_count":0,"deleted_row_count":1}`),
		"deleted less":     []byte(`{"processed_count":2,"deleted_row_count":1}`),
		"trailing":         []byte(`{"processed_count":1,"deleted_row_count":1}{}`),
	}
	for _, adapter := range adapters {
		for name, encoded := range malformed {
			t.Run(adapter.name+" "+name, func(t *testing.T) {
				fake := &fakeAccountInventoryHistoryQueries{}
				adapter.configure(fake, encoded)
				_, err := adapter.call(&AccountInventoryHistoryRepository{queries: fake}, context.Background(), historyruntime.RetentionRequest{Limit: 2})
				if !errors.Is(err, ErrAccountInventoryHistoryInconsistent) {
					t.Fatalf("encoded=%s error=%v", encoded, err)
				}
			})
		}
	}
}

func TestAccountInventoryHistoryRetentionErrorsAreFixedAndRedacted(t *testing.T) {
	type adapter struct {
		name      string
		configure func(*fakeAccountInventoryHistoryQueries, error)
		call      func(*AccountInventoryHistoryRepository, context.Context, historyruntime.RetentionRequest) (historyruntime.RetentionResult, error)
	}
	adapters := []adapter{
		{name: "poll", configure: func(fake *fakeAccountInventoryHistoryQueries, err error) { fake.pollRetentionErr = err }, call: (*AccountInventoryHistoryRepository).DeletePollRetention},
		{name: "rollup rows", configure: func(fake *fakeAccountInventoryHistoryQueries, err error) { fake.rowRetentionErr = err }, call: (*AccountInventoryHistoryRepository).DeleteRollupRowRetention},
		{name: "rollup run", configure: func(fake *fakeAccountInventoryHistoryQueries, err error) { fake.runRetentionErr = err }, call: (*AccountInventoryHistoryRepository).DeleteRollupRunRetention},
		{name: "compaction run", configure: func(fake *fakeAccountInventoryHistoryQueries, err error) { fake.compactionRetentionErr = err }, call: (*AccountInventoryHistoryRepository).DeleteCompactionRunRetention},
	}
	for _, adapter := range adapters {
		for name, test := range map[string]struct {
			databaseError error
			want          error
		}{
			"database":       {databaseError: &pgconn.PgError{Code: "08006", Message: "retention-secret-marker"}, want: ErrAccountInventoryHistoryUnavailable},
			"unknown commit": {databaseError: errors.New("retention-secret-marker"), want: historyruntime.ErrHistoryCommitUnknown},
		} {
			t.Run(adapter.name+" "+name, func(t *testing.T) {
				fake := &fakeAccountInventoryHistoryQueries{}
				adapter.configure(fake, test.databaseError)
				_, err := adapter.call(&AccountInventoryHistoryRepository{queries: fake}, context.Background(), historyruntime.RetentionRequest{Limit: 1})
				if !errors.Is(err, test.want) || strings.Contains(fmt.Sprint(err), "marker") {
					t.Fatalf("error=%v want=%v", err, test.want)
				}
			})
		}
	}
}

func TestAccountInventoryHistoryDatabaseErrorsAreFixedAndRedacted(t *testing.T) {
	for code, want := range map[string]error{
		"22023": ErrInvalidAccountInventoryHistoryInput,
		"P0002": ErrAccountInventoryHistoryLeaseLost,
		"P1001": historyruntime.ErrHistorySourceDayMismatch,
		"P1002": historyruntime.ErrHistorySourceCountMismatch,
		"P1003": historyruntime.ErrHistorySourceChecksumMismatch,
		"P1004": historyruntime.ErrHistoryActivationInconsistent,
		"P1101": historyruntime.ErrHistorySegmentIncomplete,
		"P1102": historyruntime.ErrHistorySegmentCountMismatch,
		"P1103": historyruntime.ErrHistorySegmentChecksumMismatch,
		"P1104": historyruntime.ErrHistoryActivationInconsistent,
		"57014": ErrAccountInventoryHistoryTimeout,
		"23514": ErrAccountInventoryHistoryInconsistent,
		"42501": ErrAccountInventoryHistoryInconsistent,
		"55000": ErrAccountInventoryHistoryInconsistent,
		"08006": ErrAccountInventoryHistoryUnavailable,
		"XX000": ErrAccountInventoryHistoryUnavailable,
	} {
		err := accountInventoryHistoryDatabaseError(&pgconn.PgError{Code: code, Message: "raw-error-marker"})
		if !errors.Is(err, want) || strings.Contains(fmt.Sprint(err), "marker") {
			t.Fatalf("code=%s error=%v want=%v", code, err, want)
		}
	}
	if err := accountInventoryHistoryDatabaseError(context.Canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
	if err := accountInventoryHistoryMutationError(errors.New("raw-unknown-commit-marker")); !errors.Is(err, historyruntime.ErrHistoryCommitUnknown) || strings.Contains(err.Error(), "marker") {
		t.Fatalf("unknown commit error=%v", err)
	}
}

func TestAccountInventoryHistoryDTOFormattingIsRedacted(t *testing.T) {
	marker := "history-protected-marker"
	values := []any{
		AccountInventoryHistoryCompatibility{},
		historyruntime.PlanResult{},
		AccountInventoryHistorySourceProof{Checksum: []byte(marker)},
		AccountInventoryHistoryCompactionClaim{ProviderPolicyVersion: uuid.New()},
		AccountInventoryHistoryCoverageMetric{Provider: marker},
		generated.ClaimAccountInventoryHistoryCompactionParams{},
		generated.RenewAccountInventoryHistoryCompactionParams{},
		generated.SummarizeAccountInventoryHistoryCompactionParams{},
		generated.DeleteAccountInventoryHistorySnapshotBatchParams{},
		generated.CompleteAccountInventoryHistoryCompactionParams{ExpectedChecksum: []byte(marker)},
		generated.FailAccountInventoryHistoryCompactionParams{},
		generated.ClaimAccountInventoryHistoryDailyRollupParams{},
		generated.RenewAccountInventoryHistoryDailyRollupParams{},
		generated.FinalizeAccountInventoryHistoryDailyRollupParams{},
		generated.FailAccountInventoryHistoryDailyRollupParams{FixedReason: marker},
		generated.AccountInventoryCompactionRun{ClaimOwner: pgtype.Text{String: marker, Valid: true}, SourceChecksum: []byte(marker)},
		generated.AccountInventoryDailyRollupRun{SegmentChecksum: []byte(marker)},
		generated.AccountInventoryDailySummary{AccountKey: marker},
		generated.AccountInventoryDailyProviderSummary{Provider: marker},
		generated.AccountInventoryDailyAccountRollup{AccountKey: marker},
		generated.AccountInventoryDailyProviderRollup{Provider: marker},
		generated.ListAccountInventoryHistoryCoverageMetricsRow{Provider: marker},
	}
	for _, value := range values {
		for _, format := range []string{"%v", "%+v", "%#v"} {
			text := strings.ToLower(fmt.Sprintf(format, value))
			if strings.Contains(text, marker) || !strings.Contains(text, "redacted") {
				t.Fatalf("formatter leaked with %s: %s", format, text)
			}
		}
	}
}

func validAccountInventoryHistoryClaimRow(
	worker uuid.UUID, status AccountInventoryHistoryCompactionStatus,
) generated.AccountInventoryCompactionRun {
	created := time.Date(2026, 8, 20, 1, 0, 0, 0, time.UTC)
	row := generated.AccountInventoryCompactionRun{
		CompactionRunID: nullableUUID(uuid.New()),
		SummaryDate:     pgtype.Date{Time: time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC), Valid: true},
		InstanceID:      nullableUUID(uuid.New()), ProviderPolicyVersion: nullableUUID(uuid.New()),
		Status: string(status), ClaimOwner: pgtype.Text{String: worker.String(), Valid: true},
		LeaseExpiresAt: pgtype.Timestamptz{Time: created.Add(time.Minute), Valid: true},
		FencingToken:   nullableUUID(uuid.New()), AttemptCount: 1,
		DeletedSnapshotCount: 0, DeletedPollCount: 0,
		DeletedProviderResultCount: 0, DeletedDuplicateCount: 0,
		CreatedAt: pgtype.Timestamptz{Time: created, Valid: true},
		UpdatedAt: pgtype.Timestamptz{Time: created.Add(time.Second), Valid: true},
	}
	if status != AccountInventoryHistoryCompactionPending {
		row.ChecksumVersion = pgtype.Int2{Int16: 1, Valid: true}
		row.SourceSnapshotCount = pgtype.Int8{Int64: 1, Valid: true}
		row.SourcePollCount = pgtype.Int8{Int64: 1, Valid: true}
		row.SourceProviderResultCount = pgtype.Int8{Int64: 1, Valid: true}
		row.SourceDuplicateCount = pgtype.Int8{Int64: 0, Valid: true}
		row.SourceChecksum = make([]byte, 32)
		row.SummarizedAt = pgtype.Timestamptz{Time: created.Add(2 * time.Second), Valid: true}
	}
	if status == AccountInventoryHistoryCompactionDeleting {
		row.DeletingAt = pgtype.Timestamptz{Time: created.Add(3 * time.Second), Valid: true}
	}
	return row
}

func validAccountInventoryHistoryRollupClaimRow(worker uuid.UUID) generated.AccountInventoryDailyRollupRun {
	created := time.Date(2026, 8, 20, 1, 0, 0, 0, time.UTC)
	return generated.AccountInventoryDailyRollupRun{
		RollupRunID: nullableUUID(uuid.New()),
		SummaryDate: pgtype.Date{Time: time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC), Valid: true},
		InstanceID:  nullableUUID(uuid.New()), Status: "pending",
		ClaimOwner:     pgtype.Text{String: worker.String(), Valid: true},
		LeaseExpiresAt: pgtype.Timestamptz{Time: created.Add(time.Minute), Valid: true},
		FencingToken:   nullableUUID(uuid.New()), AttemptCount: 1,
		CreatedAt: pgtype.Timestamptz{Time: created, Valid: true},
		UpdatedAt: pgtype.Timestamptz{Time: created.Add(time.Second), Valid: true},
	}
}
