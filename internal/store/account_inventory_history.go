package store

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	historyruntime "github.com/sunxu/relay-station-control/internal/historyruntime"
	generated "github.com/sunxu/relay-station-control/internal/store/sqlc"
)

var (
	ErrInvalidAccountInventoryHistoryInput = errors.New("store: invalid account inventory history input")
	ErrAccountInventoryHistoryNoWork       = historyruntime.ErrNoHistoryWork
	ErrAccountInventoryHistoryLeaseLost    = historyruntime.ErrHistoryLeaseLost
	ErrAccountInventoryHistoryTimeout      = historyruntime.ErrHistoryStatementTimeout
	ErrAccountInventoryHistoryUnavailable  = historyruntime.ErrHistoryDatabaseUnavailable
	ErrAccountInventoryHistoryIncompatible = errors.New("store: account inventory history database incompatible")
	ErrAccountInventoryHistoryInconsistent = historyruntime.ErrHistoryStateInconsistent
)

type AccountInventoryHistoryCompactionStatus string

const (
	AccountInventoryHistoryCompactionPending    AccountInventoryHistoryCompactionStatus = "pending"
	AccountInventoryHistoryCompactionSummarized AccountInventoryHistoryCompactionStatus = "summarized"
	AccountInventoryHistoryCompactionDeleting   AccountInventoryHistoryCompactionStatus = "deleting"
)

type AccountInventoryHistoryCompatibility struct {
	SchemaVersion                int  `json:"schema_version"`
	HistoryTableCount            int  `json:"history_table_count"`
	CoreSHA256                   bool `json:"core_sha256"`
	CoverageThresholdBasisPoints int  `json:"coverage_threshold_basis_points"`
	SnapshotMinimumAgeHours      int  `json:"snapshot_minimum_age_hours"`
	HistoryRetentionDays         int  `json:"history_retention_days"`
}

type accountInventoryHistoryPlanWire struct {
	CompactionRunsCreated int `json:"compaction_runs_created"`
	RollupRunsCreated     int `json:"rollup_runs_created"`
}

type accountInventoryHistoryReconcileWire struct {
	FailedCount int `json:"failed_count"`
}

type accountInventoryHistorySummarizeWire struct {
	Status               string `json:"status"`
	SourceRows           int64  `json:"source_rows"`
	SourceSnapshotCount  int64  `json:"source_snapshot_count"`
	SourceChecksumHex    string `json:"source_checksum_hex"`
	AccountSegmentCount  int64  `json:"account_segment_count"`
	ProviderSegmentCount int64  `json:"provider_segment_count"`
}

type accountInventoryHistoryDeleteWire struct {
	Status            string `json:"status"`
	DeletedCount      int64  `json:"deleted_count"`
	RemainingCount    int64  `json:"remaining_count"`
	TotalDeletedCount int64  `json:"total_deleted_count"`
}

type accountInventoryHistoryCompleteWire struct {
	Status            string  `json:"status"`
	SourceChecksumHex string  `json:"source_checksum_hex"`
	Idempotent        bool    `json:"idempotent"`
	FailureReason     *string `json:"failure_reason"`
}

type accountInventoryHistoryFailWire struct {
	Status        string `json:"status"`
	FailureReason string `json:"failure_reason"`
}

type accountInventoryHistoryRollupFinalizeWire struct {
	Status                string  `json:"status"`
	ExpectedSegmentCount  int64   `json:"expected_segment_count"`
	CompletedSegmentCount int64   `json:"completed_segment_count"`
	SegmentChecksumHex    string  `json:"segment_checksum_hex"`
	AccountRollupCount    int64   `json:"account_rollup_count"`
	ProviderRollupCount   int64   `json:"provider_rollup_count"`
	Idempotent            bool    `json:"idempotent"`
	FailureReason         *string `json:"failure_reason"`
}

type accountInventoryHistoryRetentionWire struct {
	ProcessedCount  *int64 `json:"processed_count"`
	DeletedRowCount *int64 `json:"deleted_row_count"`
}

type accountInventoryHistoryMetricsWire struct {
	CompactionRuns                  map[historyruntime.CompactionState]*int64 `json:"compaction_runs"`
	RollupRuns                      map[historyruntime.RollupState]*int64     `json:"rollup_runs"`
	OldestEligibleUnfinishedSeconds *float64                                  `json:"oldest_eligible_unfinished_seconds"`
	Failures                        map[historyruntime.FailedFrom]*int64      `json:"failures"`
	DeleteBacklogRows               *int64                                    `json:"delete_backlog_rows"`
}

type AccountInventoryHistorySourceProof struct {
	ChecksumVersion     int16
	SnapshotCount       uint64
	PollCount           uint64
	ProviderResultCount uint64
	DuplicateCount      uint64
	Checksum            []byte
}

type AccountInventoryHistoryCompactionClaim struct {
	RunID                 uuid.UUID
	SummaryDate           time.Time
	InstanceID            uuid.UUID
	ProviderPolicyVersion uuid.UUID
	Status                AccountInventoryHistoryCompactionStatus
	WorkerToken           uuid.UUID
	LeaseExpiresAt        time.Time
	FencingToken          uuid.UUID
	AttemptCount          int
	SourceProof           *AccountInventoryHistorySourceProof
	DeletedSnapshotCount  uint64
	CreatedAt             time.Time
	SummarizedAt          *time.Time
	DeletingAt            *time.Time
	UpdatedAt             time.Time
}

type AccountInventoryHistoryCoverageMetric struct {
	InstanceID uuid.UUID
	Provider   string
	Ratio      float64
	Complete   bool
}

type accountInventoryHistoryQueries interface {
	CheckAccountInventoryHistoryCompatibility(context.Context) ([]byte, error)
	PlanAccountInventoryHistory(context.Context, int32) ([]byte, error)
	ClaimAccountInventoryHistoryCompaction(
		context.Context, generated.ClaimAccountInventoryHistoryCompactionParams,
	) (generated.AccountInventoryCompactionRun, error)
	RenewAccountInventoryHistoryCompaction(
		context.Context, generated.RenewAccountInventoryHistoryCompactionParams,
	) (pgtype.UUID, error)
	ReconcileAccountInventoryHistoryCompactions(context.Context, int32) ([]byte, error)
	SummarizeAccountInventoryHistoryCompaction(
		context.Context, generated.SummarizeAccountInventoryHistoryCompactionParams,
	) ([]byte, error)
	DeleteAccountInventoryHistorySnapshotBatch(
		context.Context, generated.DeleteAccountInventoryHistorySnapshotBatchParams,
	) ([]byte, error)
	CompleteAccountInventoryHistoryCompaction(
		context.Context, generated.CompleteAccountInventoryHistoryCompactionParams,
	) ([]byte, error)
	FailAccountInventoryHistoryCompaction(
		context.Context, generated.FailAccountInventoryHistoryCompactionParams,
	) ([]byte, error)
	ClaimAccountInventoryHistoryDailyRollup(
		context.Context, generated.ClaimAccountInventoryHistoryDailyRollupParams,
	) (generated.AccountInventoryDailyRollupRun, error)
	RenewAccountInventoryHistoryDailyRollup(
		context.Context, generated.RenewAccountInventoryHistoryDailyRollupParams,
	) (pgtype.UUID, error)
	ReconcileAccountInventoryHistoryDailyRollups(context.Context, int32) ([]byte, error)
	FinalizeAccountInventoryHistoryDailyRollup(
		context.Context, generated.FinalizeAccountInventoryHistoryDailyRollupParams,
	) ([]byte, error)
	FailAccountInventoryHistoryDailyRollup(
		context.Context, generated.FailAccountInventoryHistoryDailyRollupParams,
	) ([]byte, error)
	DeleteAccountInventoryPollRetention(context.Context, int32) ([]byte, error)
	DeleteAccountInventoryRollupRowRetention(context.Context, int32) ([]byte, error)
	DeleteAccountInventoryRollupRunRetention(context.Context, int32) ([]byte, error)
	DeleteAccountInventoryCompactionRunRetention(context.Context, int32) ([]byte, error)
	GetAccountInventoryHistoryMetricsSnapshot(context.Context) ([]byte, error)
	ListAccountInventoryHistoryCoverageMetrics(
		context.Context,
	) ([]generated.ListAccountInventoryHistoryCoverageMetricsRow, error)
}

type AccountInventoryHistoryRepository struct {
	queries               accountInventoryHistoryQueries
	statusOnce            sync.Once
	runtimeState          *historyruntime.RuntimeStatusState
	metricsMu             sync.RWMutex
	deleteSuccessRows     uint64
	deleteSuccessDuration float64
	deleteFailureDuration float64
}

var _ historyruntime.Repository = (*AccountInventoryHistoryRepository)(nil)
var _ historyruntime.MetricsProvider = (*AccountInventoryHistoryRepository)(nil)
var _ historyruntime.StatusObserver = (*AccountInventoryHistoryRepository)(nil)

func NewAccountInventoryHistoryRepository(pool *pgxpool.Pool) (*AccountInventoryHistoryRepository, error) {
	if pool == nil {
		return nil, ErrAccountInventoryHistoryUnavailable
	}
	return &AccountInventoryHistoryRepository{queries: generated.New(pool)}, nil
}

func (repository *AccountInventoryHistoryRepository) historyRuntimeState() *historyruntime.RuntimeStatusState {
	repository.statusOnce.Do(func() {
		repository.runtimeState = historyruntime.NewRuntimeStatusState()
	})
	return repository.runtimeState
}

// ObserveRuntimeStatus lets the same repository bridge Service state into its
// database-backed metrics snapshot without introducing a second source of
// truth or querying history tables while the feature is disabled/incompatible.
func (repository *AccountInventoryHistoryRepository) ObserveRuntimeStatus(status historyruntime.RuntimeStatus) {
	if repository == nil {
		return
	}
	repository.historyRuntimeState().ObserveRuntimeStatus(status)
}

// CheckHistoryCompatibility implements the default-disabled runtime gate. It
// intentionally collapses all catalog and transport details into one stable
// error so schema identities and raw PostgreSQL errors cannot reach logs.
func (repository *AccountInventoryHistoryRepository) CheckHistoryCompatibility(ctx context.Context) error {
	if repository == nil || repository.queries == nil {
		return ErrAccountInventoryHistoryIncompatible
	}
	encoded, err := repository.queries.CheckAccountInventoryHistoryCompatibility(ctx)
	if err != nil {
		return ErrAccountInventoryHistoryIncompatible
	}
	var compatibility AccountInventoryHistoryCompatibility
	if decodeStrictJSON(encoded, &compatibility) != nil || !validAccountInventoryHistoryCompatibility(compatibility) {
		return ErrAccountInventoryHistoryIncompatible
	}
	return nil
}

func (repository *AccountInventoryHistoryRepository) Plan(
	ctx context.Context, request historyruntime.PlanRequest,
) (historyruntime.PlanResult, error) {
	scheduleLimit := request.Limit
	if repository == nil || repository.queries == nil || scheduleLimit < 1 || scheduleLimit > 1000 {
		return historyruntime.PlanResult{}, ErrInvalidAccountInventoryHistoryInput
	}
	encoded, err := repository.queries.PlanAccountInventoryHistory(ctx, int32(scheduleLimit))
	if err != nil {
		return historyruntime.PlanResult{}, accountInventoryHistoryDatabaseError(err)
	}
	var result accountInventoryHistoryPlanWire
	if decodeStrictJSON(encoded, &result) != nil ||
		!requireHistoryJSONKeys(encoded, "compaction_runs_created", "rollup_runs_created") ||
		result.CompactionRunsCreated < 0 ||
		result.CompactionRunsCreated > scheduleLimit || result.RollupRunsCreated < 0 ||
		result.RollupRunsCreated > scheduleLimit {
		return historyruntime.PlanResult{}, ErrAccountInventoryHistoryInconsistent
	}
	return historyruntime.PlanResult{
		CompactionRunsCreated: result.CompactionRunsCreated,
		RollupRunsCreated:     result.RollupRunsCreated,
	}, nil
}

func (repository *AccountInventoryHistoryRepository) ClaimCompaction(
	ctx context.Context, request historyruntime.ClaimRequest,
) (*historyruntime.CompactionClaim, error) {
	workerToken, lease := request.WorkerToken, request.Lease
	if repository == nil || repository.queries == nil || workerToken == uuid.Nil ||
		lease < 5*time.Second || lease > 5*time.Minute || lease%time.Second != 0 {
		return nil, ErrInvalidAccountInventoryHistoryInput
	}
	row, err := repository.queries.ClaimAccountInventoryHistoryCompaction(ctx,
		generated.ClaimAccountInventoryHistoryCompactionParams{
			WorkerToken: nullableUUID(workerToken), LeaseSeconds: int32(lease / time.Second),
		})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrAccountInventoryHistoryNoWork
	}
	if err != nil {
		return nil, accountInventoryHistoryDatabaseError(err)
	}
	claim, err := accountInventoryHistoryCompactionClaimFromRow(workerToken, row)
	if err != nil {
		return nil, err
	}
	result := historyruntime.CompactionClaim{
		RunID: claim.RunID, SummaryDate: claim.SummaryDate,
		InstanceID: claim.InstanceID, ProviderPolicyVersion: claim.ProviderPolicyVersion,
		Status: historyruntime.CompactionState(claim.Status), LeaseExpiresAt: claim.LeaseExpiresAt,
		FencingToken: claim.FencingToken, Attempt: claim.AttemptCount,
		DeletedSnapshotCount: claim.DeletedSnapshotCount,
	}
	if claim.SourceProof != nil {
		result.SourceChecksum = append([]byte(nil), claim.SourceProof.Checksum...)
		result.SourceSnapshotCount = claim.SourceProof.SnapshotCount
	}
	return &result, nil
}

func (repository *AccountInventoryHistoryRepository) RenewCompaction(
	ctx context.Context, request historyruntime.LeaseRequest,
) error {
	if repository == nil || repository.queries == nil || request.RunID == uuid.Nil ||
		request.FencingToken == uuid.Nil || !validHistoryLease(request.Lease) {
		return ErrInvalidAccountInventoryHistoryInput
	}
	renewedRunID, err := repository.queries.RenewAccountInventoryHistoryCompaction(ctx,
		generated.RenewAccountInventoryHistoryCompactionParams{
			CompactionRunID: nullableUUID(request.RunID),
			FencingToken:    nullableUUID(request.FencingToken),
			LeaseSeconds:    int32(request.Lease / time.Second),
		})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAccountInventoryHistoryLeaseLost
	}
	if err != nil {
		return accountInventoryHistoryMutationError(err)
	}
	if uuidFromPG(renewedRunID) != request.RunID {
		return ErrAccountInventoryHistoryInconsistent
	}
	return nil
}

func (repository *AccountInventoryHistoryRepository) ReconcileCompactions(
	ctx context.Context, request historyruntime.ReconcileRequest,
) (historyruntime.ReconcileResult, error) {
	if repository == nil || repository.queries == nil || request.Limit < 1 || request.Limit > 1000 {
		return historyruntime.ReconcileResult{}, ErrInvalidAccountInventoryHistoryInput
	}
	encoded, err := repository.queries.ReconcileAccountInventoryHistoryCompactions(ctx, int32(request.Limit))
	if err != nil {
		return historyruntime.ReconcileResult{}, accountInventoryHistoryMutationError(err)
	}
	var wire accountInventoryHistoryReconcileWire
	if decodeStrictJSON(encoded, &wire) != nil || !requireHistoryJSONKeys(encoded, "failed_count") ||
		wire.FailedCount < 0 || wire.FailedCount > request.Limit {
		return historyruntime.ReconcileResult{}, ErrAccountInventoryHistoryInconsistent
	}
	return historyruntime.ReconcileResult{Reconciled: wire.FailedCount}, nil
}

func (repository *AccountInventoryHistoryRepository) SummarizeCompaction(
	ctx context.Context, request historyruntime.FencedRequest,
) (historyruntime.SummarizeResult, error) {
	if repository == nil || repository.queries == nil || !validHistoryFence(request.RunID, request.FencingToken) {
		return historyruntime.SummarizeResult{}, ErrInvalidAccountInventoryHistoryInput
	}
	encoded, err := repository.queries.SummarizeAccountInventoryHistoryCompaction(ctx,
		generated.SummarizeAccountInventoryHistoryCompactionParams{
			CompactionRunID: nullableUUID(request.RunID), FencingToken: nullableUUID(request.FencingToken),
		})
	if err != nil {
		return historyruntime.SummarizeResult{}, accountInventoryHistoryMutationError(err)
	}
	var wire accountInventoryHistorySummarizeWire
	if decodeStrictJSON(encoded, &wire) != nil || !requireHistoryJSONKeys(encoded,
		"status", "source_rows", "source_snapshot_count", "source_checksum_hex",
		"account_segment_count", "provider_segment_count") {
		return historyruntime.SummarizeResult{}, ErrAccountInventoryHistoryInconsistent
	}
	checksum, checksumErr := decodeHistoryChecksum(wire.SourceChecksumHex)
	if checksumErr != nil || wire.Status != "summarized" || wire.SourceRows < 0 ||
		wire.SourceSnapshotCount < 0 || wire.SourceSnapshotCount > wire.SourceRows ||
		wire.AccountSegmentCount < 0 || wire.ProviderSegmentCount < 0 {
		return historyruntime.SummarizeResult{}, ErrAccountInventoryHistoryInconsistent
	}
	return historyruntime.SummarizeResult{
		SourceChecksum: checksum, SourceSnapshotCount: uint64(wire.SourceSnapshotCount),
	}, nil
}

func (repository *AccountInventoryHistoryRepository) DeleteSnapshotBatch(
	ctx context.Context, request historyruntime.DeleteBatchRequest,
) (result historyruntime.DeleteBatchResult, operationError error) {
	if repository == nil || repository.queries == nil || !validHistoryFence(request.RunID, request.FencingToken) ||
		request.Limit < 1 || request.Limit > 5000 {
		return historyruntime.DeleteBatchResult{}, ErrInvalidAccountInventoryHistoryInput
	}
	startedAt := time.Now()
	defer func() {
		repository.observeAccountInventoryHistoryRetention(
			historyruntime.RetentionResult{DeletedRows: result.DeletedRows},
			operationError, time.Since(startedAt),
		)
	}()
	encoded, err := repository.queries.DeleteAccountInventoryHistorySnapshotBatch(ctx,
		generated.DeleteAccountInventoryHistorySnapshotBatchParams{
			CompactionRunID: nullableUUID(request.RunID), FencingToken: nullableUUID(request.FencingToken),
			DeleteLimit: int32(request.Limit),
		})
	if err != nil {
		return historyruntime.DeleteBatchResult{}, accountInventoryHistoryMutationError(err)
	}
	var wire accountInventoryHistoryDeleteWire
	if decodeStrictJSON(encoded, &wire) != nil || !requireHistoryJSONKeys(encoded,
		"status", "deleted_count", "remaining_count", "total_deleted_count") ||
		wire.Status != "deleting" || wire.DeletedCount < 0 ||
		wire.DeletedCount > int64(request.Limit) || wire.RemainingCount < 0 || wire.TotalDeletedCount < 0 ||
		wire.TotalDeletedCount < wire.DeletedCount {
		return historyruntime.DeleteBatchResult{}, ErrAccountInventoryHistoryInconsistent
	}
	return historyruntime.DeleteBatchResult{
		DeletedRows: uint64(wire.DeletedCount), RemainingRows: uint64(wire.RemainingCount),
		TotalDeletedRows: uint64(wire.TotalDeletedCount),
	}, nil
}

func (repository *AccountInventoryHistoryRepository) CompleteCompaction(
	ctx context.Context, request historyruntime.CompleteRequest,
) (historyruntime.CompleteResult, error) {
	if repository == nil || repository.queries == nil || !validHistoryFence(request.RunID, request.FencingToken) ||
		len(request.ExpectedChecksum) != 32 {
		return historyruntime.CompleteResult{}, ErrInvalidAccountInventoryHistoryInput
	}
	encoded, err := repository.queries.CompleteAccountInventoryHistoryCompaction(ctx,
		generated.CompleteAccountInventoryHistoryCompactionParams{
			CompactionRunID: nullableUUID(request.RunID), FencingToken: nullableUUID(request.FencingToken),
			ExpectedChecksum: append([]byte(nil), request.ExpectedChecksum...),
		})
	if err != nil {
		return historyruntime.CompleteResult{}, accountInventoryHistoryMutationError(err)
	}
	var wire accountInventoryHistoryCompleteWire
	if decodeStrictJSON(encoded, &wire) != nil || !requireHistoryJSONKeys(encoded,
		"status", "source_checksum_hex", "idempotent", "failure_reason") {
		return historyruntime.CompleteResult{}, ErrAccountInventoryHistoryInconsistent
	}
	checksum, checksumErr := decodeHistoryChecksum(wire.SourceChecksumHex)
	if checksumErr != nil {
		return historyruntime.CompleteResult{}, ErrAccountInventoryHistoryInconsistent
	}
	if wire.Status == "failed" && wire.FailureReason != nil {
		if wire.Idempotent || (*wire.FailureReason != string(historyruntime.FailureSourceCountMismatch) &&
			*wire.FailureReason != string(historyruntime.FailureSourceChecksumMismatch)) {
			return historyruntime.CompleteResult{}, ErrAccountInventoryHistoryInconsistent
		}
		return historyruntime.CompleteResult{}, accountInventoryHistoryFailureError(*wire.FailureReason)
	}
	if wire.Status != "completed" || wire.FailureReason != nil ||
		!equalHistoryChecksum(checksum, request.ExpectedChecksum) {
		return historyruntime.CompleteResult{}, ErrAccountInventoryHistoryInconsistent
	}
	return historyruntime.CompleteResult{Completed: true}, nil
}

func (repository *AccountInventoryHistoryRepository) FailCompaction(
	ctx context.Context, request historyruntime.FailRequest,
) error {
	if repository == nil || repository.queries == nil || !validHistoryFence(request.RunID, request.FencingToken) ||
		!validHistoryCompactionFailureReason(request.Reason) {
		return ErrInvalidAccountInventoryHistoryInput
	}
	encoded, err := repository.queries.FailAccountInventoryHistoryCompaction(ctx,
		generated.FailAccountInventoryHistoryCompactionParams{
			CompactionRunID: nullableUUID(request.RunID), FencingToken: nullableUUID(request.FencingToken),
			FixedReason: string(request.Reason),
		})
	if err != nil {
		return accountInventoryHistoryMutationError(err)
	}
	var wire accountInventoryHistoryFailWire
	if decodeStrictJSON(encoded, &wire) != nil ||
		!requireHistoryJSONKeys(encoded, "status", "failure_reason") ||
		wire.Status != "failed" || wire.FailureReason != string(request.Reason) {
		return ErrAccountInventoryHistoryInconsistent
	}
	return nil
}

func (repository *AccountInventoryHistoryRepository) ClaimRollup(
	ctx context.Context, request historyruntime.RollupClaimRequest,
) (*historyruntime.RollupClaim, error) {
	if repository == nil || repository.queries == nil || request.WorkerToken == uuid.Nil ||
		!validHistoryLease(request.Lease) {
		return nil, ErrInvalidAccountInventoryHistoryInput
	}
	row, err := repository.queries.ClaimAccountInventoryHistoryDailyRollup(ctx,
		generated.ClaimAccountInventoryHistoryDailyRollupParams{
			WorkerToken: nullableUUID(request.WorkerToken), LeaseSeconds: int32(request.Lease / time.Second),
		})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrAccountInventoryHistoryNoWork
	}
	if err != nil {
		return nil, accountInventoryHistoryMutationError(err)
	}
	claim := historyruntime.RollupClaim{
		RunID: uuidFromPG(row.RollupRunID), InstanceID: uuidFromPG(row.InstanceID),
		FencingToken: uuidFromPG(row.FencingToken), Attempt: int(row.AttemptCount),
	}
	if row.SummaryDate.Valid {
		claim.SummaryDate = dateAtUTC(row.SummaryDate.Time)
	}
	if row.LeaseExpiresAt.Valid {
		claim.LeaseExpiresAt = row.LeaseExpiresAt.Time.UTC()
	}
	if claim.RunID == uuid.Nil || claim.InstanceID == uuid.Nil || claim.FencingToken == uuid.Nil ||
		claim.SummaryDate.IsZero() || claim.LeaseExpiresAt.IsZero() || claim.Attempt < 1 ||
		row.Status != "pending" || !row.ClaimOwner.Valid || row.ClaimOwner.String != request.WorkerToken.String() ||
		!row.CreatedAt.Valid || !row.UpdatedAt.Valid || row.UpdatedAt.Time.Before(row.CreatedAt.Time) ||
		!claim.LeaseExpiresAt.After(row.UpdatedAt.Time) || row.CompletedFencingToken.Valid ||
		row.ExpectedSegmentCount.Valid || row.CompletedSegmentCount.Valid || row.ChecksumVersion.Valid ||
		len(row.SegmentChecksum) != 0 || row.FailureReason.Valid || row.CompletedAt.Valid || row.FailedAt.Valid {
		return nil, ErrAccountInventoryHistoryInconsistent
	}
	return &claim, nil
}

func (repository *AccountInventoryHistoryRepository) RenewRollup(
	ctx context.Context, request historyruntime.RollupLeaseRequest,
) error {
	if repository == nil || repository.queries == nil || !validHistoryFence(request.RunID, request.FencingToken) ||
		!validHistoryLease(request.Lease) {
		return ErrInvalidAccountInventoryHistoryInput
	}
	renewedRunID, err := repository.queries.RenewAccountInventoryHistoryDailyRollup(ctx,
		generated.RenewAccountInventoryHistoryDailyRollupParams{
			RollupRunID: nullableUUID(request.RunID), FencingToken: nullableUUID(request.FencingToken),
			LeaseSeconds: int32(request.Lease / time.Second),
		})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAccountInventoryHistoryLeaseLost
	}
	if err != nil {
		return accountInventoryHistoryMutationError(err)
	}
	if uuidFromPG(renewedRunID) != request.RunID {
		return ErrAccountInventoryHistoryInconsistent
	}
	return nil
}

func (repository *AccountInventoryHistoryRepository) ReconcileRollups(
	ctx context.Context, request historyruntime.RollupReconcileRequest,
) (historyruntime.RollupReconcileResult, error) {
	if repository == nil || repository.queries == nil || request.Limit < 1 || request.Limit > 1000 {
		return historyruntime.RollupReconcileResult{}, ErrInvalidAccountInventoryHistoryInput
	}
	encoded, err := repository.queries.ReconcileAccountInventoryHistoryDailyRollups(ctx, int32(request.Limit))
	if err != nil {
		return historyruntime.RollupReconcileResult{}, accountInventoryHistoryMutationError(err)
	}
	var wire accountInventoryHistoryReconcileWire
	if decodeStrictJSON(encoded, &wire) != nil || !requireHistoryJSONKeys(encoded, "failed_count") ||
		wire.FailedCount < 0 || wire.FailedCount > request.Limit {
		return historyruntime.RollupReconcileResult{}, ErrAccountInventoryHistoryInconsistent
	}
	return historyruntime.RollupReconcileResult{Reconciled: wire.FailedCount}, nil
}

func (repository *AccountInventoryHistoryRepository) FinalizeRollup(
	ctx context.Context, request historyruntime.RollupFencedRequest,
) (historyruntime.RollupFinalizeResult, error) {
	if repository == nil || repository.queries == nil || !validHistoryFence(request.RunID, request.FencingToken) {
		return historyruntime.RollupFinalizeResult{}, ErrInvalidAccountInventoryHistoryInput
	}
	encoded, err := repository.queries.FinalizeAccountInventoryHistoryDailyRollup(ctx,
		generated.FinalizeAccountInventoryHistoryDailyRollupParams{
			RollupRunID: nullableUUID(request.RunID), FencingToken: nullableUUID(request.FencingToken),
		})
	if err != nil {
		return historyruntime.RollupFinalizeResult{}, accountInventoryHistoryMutationError(err)
	}
	var wire accountInventoryHistoryRollupFinalizeWire
	if decodeStrictJSON(encoded, &wire) != nil || !requireHistoryJSONKeys(encoded,
		"status", "expected_segment_count", "completed_segment_count", "segment_checksum_hex",
		"account_rollup_count", "provider_rollup_count", "idempotent", "failure_reason") ||
		wire.ExpectedSegmentCount < 0 || wire.CompletedSegmentCount < 0 ||
		wire.AccountRollupCount < 0 || wire.ProviderRollupCount < 0 {
		return historyruntime.RollupFinalizeResult{}, ErrAccountInventoryHistoryInconsistent
	}
	if wire.Status == "failed" && wire.FailureReason != nil && !wire.Idempotent &&
		wire.AccountRollupCount == 0 && wire.ProviderRollupCount == 0 && wire.SegmentChecksumHex == "" {
		return historyruntime.RollupFinalizeResult{}, accountInventoryHistoryRollupFailureError(*wire.FailureReason)
	}
	checksum, checksumErr := decodeHistoryChecksum(wire.SegmentChecksumHex)
	if checksumErr != nil || wire.Status != "completed" || wire.FailureReason != nil ||
		wire.ExpectedSegmentCount < 1 || wire.CompletedSegmentCount != wire.ExpectedSegmentCount {
		return historyruntime.RollupFinalizeResult{}, ErrAccountInventoryHistoryInconsistent
	}
	return historyruntime.RollupFinalizeResult{
		Completed: true, ExpectedSegmentCount: uint64(wire.ExpectedSegmentCount),
		CompletedSegmentCount: uint64(wire.CompletedSegmentCount), SegmentChecksum: checksum,
	}, nil
}

func (repository *AccountInventoryHistoryRepository) FailRollup(
	ctx context.Context, request historyruntime.RollupFailRequest,
) error {
	if repository == nil || repository.queries == nil || !validHistoryFence(request.RunID, request.FencingToken) ||
		!validHistoryRollupFailureReason(request.Reason) {
		return ErrInvalidAccountInventoryHistoryInput
	}
	encoded, err := repository.queries.FailAccountInventoryHistoryDailyRollup(ctx,
		generated.FailAccountInventoryHistoryDailyRollupParams{
			RollupRunID: nullableUUID(request.RunID), FencingToken: nullableUUID(request.FencingToken),
			FixedReason: string(request.Reason),
		})
	if err != nil {
		return accountInventoryHistoryMutationError(err)
	}
	var wire accountInventoryHistoryFailWire
	if decodeStrictJSON(encoded, &wire) != nil || !requireHistoryJSONKeys(encoded, "status", "failure_reason") ||
		wire.Status != "failed" || wire.FailureReason != string(request.Reason) {
		return ErrAccountInventoryHistoryInconsistent
	}
	return nil
}

func (repository *AccountInventoryHistoryRepository) DeletePollRetention(
	ctx context.Context, request historyruntime.RetentionRequest,
) (historyruntime.RetentionResult, error) {
	if repository == nil || repository.queries == nil {
		return historyruntime.RetentionResult{}, ErrInvalidAccountInventoryHistoryInput
	}
	return repository.accountInventoryHistoryRetentionObserved(
		ctx, request, repository.queries.DeleteAccountInventoryPollRetention,
	)
}

func (repository *AccountInventoryHistoryRepository) DeleteRollupRowRetention(
	ctx context.Context, request historyruntime.RetentionRequest,
) (historyruntime.RetentionResult, error) {
	if repository == nil || repository.queries == nil {
		return historyruntime.RetentionResult{}, ErrInvalidAccountInventoryHistoryInput
	}
	return repository.accountInventoryHistoryRetentionObserved(
		ctx, request, repository.queries.DeleteAccountInventoryRollupRowRetention,
	)
}

func (repository *AccountInventoryHistoryRepository) DeleteRollupRunRetention(
	ctx context.Context, request historyruntime.RetentionRequest,
) (historyruntime.RetentionResult, error) {
	if repository == nil || repository.queries == nil {
		return historyruntime.RetentionResult{}, ErrInvalidAccountInventoryHistoryInput
	}
	return repository.accountInventoryHistoryRetentionObserved(
		ctx, request, repository.queries.DeleteAccountInventoryRollupRunRetention,
	)
}

func (repository *AccountInventoryHistoryRepository) DeleteCompactionRunRetention(
	ctx context.Context, request historyruntime.RetentionRequest,
) (historyruntime.RetentionResult, error) {
	if repository == nil || repository.queries == nil {
		return historyruntime.RetentionResult{}, ErrInvalidAccountInventoryHistoryInput
	}
	return repository.accountInventoryHistoryRetentionObserved(
		ctx, request, repository.queries.DeleteAccountInventoryCompactionRunRetention,
	)
}

func (repository *AccountInventoryHistoryRepository) accountInventoryHistoryRetentionObserved(
	ctx context.Context,
	request historyruntime.RetentionRequest,
	operation func(context.Context, int32) ([]byte, error),
) (historyruntime.RetentionResult, error) {
	if operation == nil || request.Limit < 1 || request.Limit > 5000 {
		return historyruntime.RetentionResult{}, ErrInvalidAccountInventoryHistoryInput
	}
	startedAt := time.Now()
	result, err := accountInventoryHistoryRetention(ctx, request, operation)
	repository.observeAccountInventoryHistoryRetention(result, err, time.Since(startedAt))
	return result, err
}

func (repository *AccountInventoryHistoryRepository) observeAccountInventoryHistoryRetention(
	result historyruntime.RetentionResult, operationError error, elapsed time.Duration,
) {
	seconds := elapsed.Seconds()
	if seconds < 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		seconds = 0
	}
	repository.metricsMu.Lock()
	defer repository.metricsMu.Unlock()
	if operationError != nil {
		repository.deleteFailureDuration += seconds
		return
	}
	if ^uint64(0)-repository.deleteSuccessRows < result.DeletedRows {
		repository.deleteSuccessRows = ^uint64(0)
	} else {
		repository.deleteSuccessRows += result.DeletedRows
	}
	repository.deleteSuccessDuration += seconds
}

func accountInventoryHistoryRetention(
	ctx context.Context,
	request historyruntime.RetentionRequest,
	operation func(context.Context, int32) ([]byte, error),
) (historyruntime.RetentionResult, error) {
	if operation == nil || request.Limit < 1 || request.Limit > 5000 {
		return historyruntime.RetentionResult{}, ErrInvalidAccountInventoryHistoryInput
	}
	encoded, err := operation(ctx, int32(request.Limit))
	if err != nil {
		return historyruntime.RetentionResult{}, accountInventoryHistoryMutationError(err)
	}
	var wire accountInventoryHistoryRetentionWire
	if decodeStrictJSON(encoded, &wire) != nil ||
		!requireHistoryJSONKeys(encoded, "processed_count", "deleted_row_count") ||
		wire.ProcessedCount == nil || wire.DeletedRowCount == nil ||
		*wire.ProcessedCount < 0 || *wire.ProcessedCount > int64(request.Limit) ||
		*wire.DeletedRowCount < *wire.ProcessedCount ||
		(*wire.ProcessedCount == 0) != (*wire.DeletedRowCount == 0) {
		return historyruntime.RetentionResult{}, ErrAccountInventoryHistoryInconsistent
	}
	return historyruntime.RetentionResult{
		Processed: int(*wire.ProcessedCount), DeletedRows: uint64(*wire.DeletedRowCount),
	}, nil
}

// AccountInventoryHistoryMetricsSnapshot returns only fixed-cardinality,
// identity-free aggregates from the controlled snapshot function.  A status
// that has not passed compatibility never touches migration-9 relations; the
// collector can still publish the independent enabled/reason signal while
// omitting database-derived families.
func (repository *AccountInventoryHistoryRepository) AccountInventoryHistoryMetricsSnapshot(
	ctx context.Context,
) (historyruntime.MetricsSnapshot, error) {
	if repository == nil {
		return historyruntime.MetricsSnapshot{}, ErrAccountInventoryHistoryUnavailable
	}
	status := repository.historyRuntimeState().RuntimeStatus()
	if !status.Compatible {
		return historyruntime.MetricsSnapshot{Runtime: status}, nil
	}
	if repository.queries == nil {
		return historyruntime.MetricsSnapshot{}, ErrAccountInventoryHistoryUnavailable
	}
	encoded, err := repository.queries.GetAccountInventoryHistoryMetricsSnapshot(ctx)
	if err != nil {
		return historyruntime.MetricsSnapshot{Runtime: status}, accountInventoryHistoryDatabaseError(err)
	}
	var wire accountInventoryHistoryMetricsWire
	if decodeStrictJSON(encoded, &wire) != nil || !requireHistoryJSONKeys(encoded,
		"compaction_runs", "rollup_runs", "oldest_eligible_unfinished_seconds",
		"failures", "delete_backlog_rows",
	) || !validAccountInventoryHistoryMetricsWire(wire) {
		return historyruntime.MetricsSnapshot{Runtime: status}, ErrAccountInventoryHistoryInconsistent
	}
	snapshot := historyruntime.MetricsSnapshot{
		Runtime:                         status,
		CompactionRuns:                  make(map[historyruntime.CompactionState]int64, len(wire.CompactionRuns)),
		RollupRuns:                      make(map[historyruntime.RollupState]int64, len(wire.RollupRuns)),
		OldestEligibleUnfinishedSeconds: *wire.OldestEligibleUnfinishedSeconds,
		Failures:                        make(map[historyruntime.FailedFrom]int64, len(wire.Failures)),
		DeleteBacklogRows:               *wire.DeleteBacklogRows,
		DeleteRows:                      make(map[historyruntime.DeleteResult]uint64, len(historyruntime.AllDeleteResults)),
		DeleteDurationSeconds:           make(map[historyruntime.DeleteResult]float64, len(historyruntime.AllDeleteResults)),
	}
	for state, count := range wire.CompactionRuns {
		snapshot.CompactionRuns[state] = *count
	}
	for state, count := range wire.RollupRuns {
		snapshot.RollupRuns[state] = *count
	}
	for phase, count := range wire.Failures {
		snapshot.Failures[phase] = *count
	}
	repository.metricsMu.RLock()
	snapshot.DeleteRows[historyruntime.DeleteSuccess] = repository.deleteSuccessRows
	snapshot.DeleteRows[historyruntime.DeleteFailure] = 0
	snapshot.DeleteDurationSeconds[historyruntime.DeleteSuccess] = repository.deleteSuccessDuration
	snapshot.DeleteDurationSeconds[historyruntime.DeleteFailure] = repository.deleteFailureDuration
	repository.metricsMu.RUnlock()
	coverage, err := repository.CoverageMetrics(ctx)
	if err != nil {
		return historyruntime.MetricsSnapshot{Runtime: status}, err
	}
	snapshot.ProviderCoverage = make([]historyruntime.ProviderCoverage, len(coverage))
	for index, metric := range coverage {
		snapshot.ProviderCoverage[index] = historyruntime.ProviderCoverage{
			InstanceID: metric.InstanceID, Provider: metric.Provider,
			Ratio: metric.Ratio, Complete: metric.Complete,
		}
	}
	return snapshot, nil
}

func validAccountInventoryHistoryMetricsWire(wire accountInventoryHistoryMetricsWire) bool {
	if wire.OldestEligibleUnfinishedSeconds == nil ||
		math.IsNaN(*wire.OldestEligibleUnfinishedSeconds) ||
		math.IsInf(*wire.OldestEligibleUnfinishedSeconds, 0) ||
		*wire.OldestEligibleUnfinishedSeconds < 0 || wire.DeleteBacklogRows == nil ||
		*wire.DeleteBacklogRows < 0 ||
		len(wire.CompactionRuns) != len(historyruntime.AllCompactionStates) ||
		len(wire.RollupRuns) != len(historyruntime.AllRollupStates) ||
		len(wire.Failures) != len(historyruntime.AllFailedFrom) {
		return false
	}
	for _, state := range historyruntime.AllCompactionStates {
		count, exists := wire.CompactionRuns[state]
		if !exists || count == nil || *count < 0 {
			return false
		}
	}
	for _, state := range historyruntime.AllRollupStates {
		count, exists := wire.RollupRuns[state]
		if !exists || count == nil || *count < 0 {
			return false
		}
	}
	for _, phase := range historyruntime.AllFailedFrom {
		count, exists := wire.Failures[phase]
		if !exists || count == nil || *count < 0 {
			return false
		}
	}
	return true
}

func (repository *AccountInventoryHistoryRepository) CoverageMetrics(
	ctx context.Context,
) ([]AccountInventoryHistoryCoverageMetric, error) {
	if repository == nil || repository.queries == nil {
		return nil, ErrAccountInventoryHistoryUnavailable
	}
	rows, err := repository.queries.ListAccountInventoryHistoryCoverageMetrics(ctx)
	if err != nil {
		return nil, accountInventoryHistoryDatabaseError(err)
	}
	metrics := make([]AccountInventoryHistoryCoverageMetric, 0, len(rows))
	if len(rows) > historyruntime.MaximumCoverageInstances*historyruntime.MaximumCoverageProvidersPerInstance {
		return nil, ErrAccountInventoryHistoryInconsistent
	}
	instances := make(map[uuid.UUID]int)
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		metric := AccountInventoryHistoryCoverageMetric{
			InstanceID: uuidFromPG(row.InstanceID), Provider: row.Provider,
			Ratio: row.CoverageRatio, Complete: row.CoverageComplete,
		}
		if metric.InstanceID == uuid.Nil || !validProviderName(metric.Provider) ||
			math.IsNaN(metric.Ratio) || math.IsInf(metric.Ratio, 0) ||
			metric.Ratio < 0 || metric.Ratio > 1 || metric.Complete != (metric.Ratio >= 0.95) {
			return nil, ErrAccountInventoryHistoryInconsistent
		}
		key := metric.InstanceID.String() + "\x00" + metric.Provider
		if _, duplicate := seen[key]; duplicate {
			return nil, ErrAccountInventoryHistoryInconsistent
		}
		seen[key] = struct{}{}
		instances[metric.InstanceID]++
		if len(instances) > historyruntime.MaximumCoverageInstances ||
			instances[metric.InstanceID] > historyruntime.MaximumCoverageProvidersPerInstance {
			return nil, ErrAccountInventoryHistoryInconsistent
		}
		metrics = append(metrics, metric)
	}
	return metrics, nil
}

func accountInventoryHistoryCompactionClaimFromRow(
	workerToken uuid.UUID, row generated.AccountInventoryCompactionRun,
) (AccountInventoryHistoryCompactionClaim, error) {
	claim := AccountInventoryHistoryCompactionClaim{
		RunID: uuidFromPG(row.CompactionRunID), InstanceID: uuidFromPG(row.InstanceID),
		ProviderPolicyVersion: uuidFromPG(row.ProviderPolicyVersion),
		Status:                AccountInventoryHistoryCompactionStatus(row.Status),
		WorkerToken:           workerToken, FencingToken: uuidFromPG(row.FencingToken),
		AttemptCount: int(row.AttemptCount), DeletedSnapshotCount: uint64(row.DeletedSnapshotCount),
	}
	if row.SummaryDate.Valid {
		claim.SummaryDate = dateAtUTC(row.SummaryDate.Time)
	}
	if row.LeaseExpiresAt.Valid {
		claim.LeaseExpiresAt = row.LeaseExpiresAt.Time.UTC()
	}
	if row.CreatedAt.Valid {
		claim.CreatedAt = row.CreatedAt.Time.UTC()
	}
	if row.UpdatedAt.Valid {
		claim.UpdatedAt = row.UpdatedAt.Time.UTC()
	}
	claim.SummarizedAt = nullableTime(row.SummarizedAt)
	claim.DeletingAt = nullableTime(row.DeletingAt)
	if !validAccountInventoryHistoryCompactionClaim(workerToken, row, claim) {
		return AccountInventoryHistoryCompactionClaim{}, ErrAccountInventoryHistoryInconsistent
	}
	if claim.Status != AccountInventoryHistoryCompactionPending {
		claim.SourceProof = &AccountInventoryHistorySourceProof{
			ChecksumVersion: row.ChecksumVersion.Int16,
			SnapshotCount:   uint64(row.SourceSnapshotCount.Int64), PollCount: uint64(row.SourcePollCount.Int64),
			ProviderResultCount: uint64(row.SourceProviderResultCount.Int64),
			DuplicateCount:      uint64(row.SourceDuplicateCount.Int64),
			Checksum:            append([]byte(nil), row.SourceChecksum...),
		}
	}
	return claim, nil
}

func validAccountInventoryHistoryCompactionClaim(
	workerToken uuid.UUID, row generated.AccountInventoryCompactionRun, claim AccountInventoryHistoryCompactionClaim,
) bool {
	if claim.RunID == uuid.Nil || claim.InstanceID == uuid.Nil || claim.ProviderPolicyVersion == uuid.Nil ||
		claim.FencingToken == uuid.Nil || claim.SummaryDate.IsZero() || claim.LeaseExpiresAt.IsZero() ||
		claim.CreatedAt.IsZero() || claim.UpdatedAt.Before(claim.CreatedAt) ||
		!claim.LeaseExpiresAt.After(claim.UpdatedAt) ||
		claim.AttemptCount < 1 || row.DeletedSnapshotCount < 0 || row.DeletedPollCount != 0 ||
		row.DeletedProviderResultCount != 0 || row.DeletedDuplicateCount != 0 ||
		!row.ClaimOwner.Valid || row.ClaimOwner.String != workerToken.String() ||
		row.FailedFrom.Valid || row.FailureReason.Valid || row.CompletedAt.Valid || row.FailedAt.Valid {
		return false
	}
	switch claim.Status {
	case AccountInventoryHistoryCompactionPending:
		return !row.ChecksumVersion.Valid && !row.SourceSnapshotCount.Valid && !row.SourcePollCount.Valid &&
			!row.SourceProviderResultCount.Valid && !row.SourceDuplicateCount.Valid && len(row.SourceChecksum) == 0 &&
			row.DeletedSnapshotCount == 0 && !row.SummarizedAt.Valid && !row.DeletingAt.Valid
	case AccountInventoryHistoryCompactionSummarized, AccountInventoryHistoryCompactionDeleting:
		if !row.ChecksumVersion.Valid || row.ChecksumVersion.Int16 != 1 ||
			!validNonnegativeInt8(row.SourceSnapshotCount) || !validNonnegativeInt8(row.SourcePollCount) ||
			!validNonnegativeInt8(row.SourceProviderResultCount) || !validNonnegativeInt8(row.SourceDuplicateCount) ||
			len(row.SourceChecksum) != 32 || row.DeletedSnapshotCount > row.SourceSnapshotCount.Int64 ||
			!row.SummarizedAt.Valid || row.SummarizedAt.Time.Before(row.CreatedAt.Time) {
			return false
		}
		return (claim.Status == AccountInventoryHistoryCompactionSummarized && !row.DeletingAt.Valid &&
			row.DeletedSnapshotCount == 0) ||
			(claim.Status == AccountInventoryHistoryCompactionDeleting && row.DeletingAt.Valid &&
				!row.DeletingAt.Time.Before(row.SummarizedAt.Time))
	default:
		return false
	}
}

func validNonnegativeInt8(value pgtype.Int8) bool {
	return value.Valid && value.Int64 >= 0
}

func validAccountInventoryHistoryCompatibility(value AccountInventoryHistoryCompatibility) bool {
	return value.SchemaVersion == 1 && value.HistoryTableCount == 7 && value.CoreSHA256 &&
		value.CoverageThresholdBasisPoints == 9500 && value.SnapshotMinimumAgeHours == 72 &&
		value.HistoryRetentionDays == 30
}

func accountInventoryHistoryDatabaseError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var databaseError *pgconn.PgError
	if !errors.As(err, &databaseError) {
		return ErrAccountInventoryHistoryUnavailable
	}
	switch databaseError.Code {
	case "22023":
		return ErrInvalidAccountInventoryHistoryInput
	case "P0002":
		return ErrAccountInventoryHistoryLeaseLost
	case "P1001":
		return historyruntime.ErrHistorySourceDayMismatch
	case "P1002":
		return historyruntime.ErrHistorySourceCountMismatch
	case "P1003":
		return historyruntime.ErrHistorySourceChecksumMismatch
	case "P1004":
		return historyruntime.ErrHistoryActivationInconsistent
	case "P1101":
		return historyruntime.ErrHistorySegmentIncomplete
	case "P1102":
		return historyruntime.ErrHistorySegmentCountMismatch
	case "P1103":
		return historyruntime.ErrHistorySegmentChecksumMismatch
	case "P1104":
		return historyruntime.ErrHistoryActivationInconsistent
	case "57014":
		return ErrAccountInventoryHistoryTimeout
	case "23503", "23505", "23514", "42501", "55000":
		return ErrAccountInventoryHistoryInconsistent
	default:
		return ErrAccountInventoryHistoryUnavailable
	}
}

func accountInventoryHistoryMutationError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return accountInventoryHistoryDatabaseError(err)
	}
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) || pgconn.SafeToRetry(err) {
		return accountInventoryHistoryDatabaseError(err)
	}
	return historyruntime.ErrHistoryCommitUnknown
}

func validHistoryLease(lease time.Duration) bool {
	return lease >= 5*time.Second && lease <= 5*time.Minute && lease%time.Second == 0
}

func validHistoryFence(runID, fencingToken uuid.UUID) bool {
	return runID != uuid.Nil && fencingToken != uuid.Nil
}

func validHistoryCompactionFailureReason(reason historyruntime.FailureReason) bool {
	switch reason {
	case historyruntime.FailureSourceDayMismatch,
		historyruntime.FailureSourceCountMismatch,
		historyruntime.FailureSourceChecksumMismatch,
		historyruntime.FailureActivationInconsistent,
		historyruntime.FailureStatementTimeout,
		historyruntime.FailureLeaseExpired,
		historyruntime.FailureDatabaseUnavailable,
		historyruntime.FailureInternal:
		return true
	default:
		return false
	}
}

func validHistoryRollupFailureReason(reason historyruntime.FailureReason) bool {
	switch reason {
	case historyruntime.FailureSegmentIncomplete,
		historyruntime.FailureSegmentCountMismatch,
		historyruntime.FailureSegmentChecksumMismatch,
		historyruntime.FailureActivationInconsistent,
		historyruntime.FailureStatementTimeout,
		historyruntime.FailureLeaseExpired,
		historyruntime.FailureDatabaseUnavailable,
		historyruntime.FailureInternal:
		return true
	default:
		return false
	}
}

func decodeHistoryChecksum(encoded string) ([]byte, error) {
	if len(encoded) != 64 {
		return nil, ErrAccountInventoryHistoryInconsistent
	}
	for _, character := range encoded {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return nil, ErrAccountInventoryHistoryInconsistent
		}
	}
	decoded := make([]byte, 32)
	if _, err := hex.Decode(decoded, []byte(encoded)); err != nil {
		return nil, ErrAccountInventoryHistoryInconsistent
	}
	return decoded, nil
}

func equalHistoryChecksum(left, right []byte) bool {
	return len(left) == 32 && len(right) == 32 && subtle.ConstantTimeCompare(left, right) == 1
}

func requireHistoryJSONKeys(encoded []byte, keys ...string) bool {
	var object map[string]json.RawMessage
	if json.Unmarshal(encoded, &object) != nil {
		return false
	}
	for _, key := range keys {
		if _, exists := object[key]; !exists {
			return false
		}
	}
	return true
}

func accountInventoryHistoryFailureError(reason string) error {
	switch historyruntime.FailureReason(reason) {
	case historyruntime.FailureSourceDayMismatch:
		return historyruntime.ErrHistorySourceDayMismatch
	case historyruntime.FailureSourceCountMismatch:
		return historyruntime.ErrHistorySourceCountMismatch
	case historyruntime.FailureSourceChecksumMismatch:
		return historyruntime.ErrHistorySourceChecksumMismatch
	case historyruntime.FailureActivationInconsistent:
		return historyruntime.ErrHistoryActivationInconsistent
	case historyruntime.FailureSegmentIncomplete:
		return historyruntime.ErrHistorySegmentIncomplete
	case historyruntime.FailureSegmentCountMismatch:
		return historyruntime.ErrHistorySegmentCountMismatch
	case historyruntime.FailureSegmentChecksumMismatch:
		return historyruntime.ErrHistorySegmentChecksumMismatch
	case historyruntime.FailureStatementTimeout:
		return historyruntime.ErrHistoryStatementTimeout
	case historyruntime.FailureDatabaseUnavailable:
		return historyruntime.ErrHistoryDatabaseUnavailable
	default:
		return ErrAccountInventoryHistoryInconsistent
	}
}

func accountInventoryHistoryRollupFailureError(reason string) error {
	switch historyruntime.FailureReason(reason) {
	case historyruntime.FailureSegmentIncomplete:
		return historyruntime.ErrHistorySegmentIncomplete
	case historyruntime.FailureSegmentCountMismatch:
		return historyruntime.ErrHistorySegmentCountMismatch
	case historyruntime.FailureSegmentChecksumMismatch:
		return historyruntime.ErrHistorySegmentChecksumMismatch
	case historyruntime.FailureActivationInconsistent:
		return historyruntime.ErrHistoryActivationInconsistent
	default:
		return ErrAccountInventoryHistoryInconsistent
	}
}

func dateAtUTC(value time.Time) time.Time {
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}

func (AccountInventoryHistoryCompatibility) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED AccountInventoryHistoryCompatibility]"))
}

func (AccountInventoryHistorySourceProof) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED AccountInventoryHistorySourceProof]"))
}

func (AccountInventoryHistoryCompactionClaim) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED AccountInventoryHistoryCompactionClaim]"))
}

func (AccountInventoryHistoryCoverageMetric) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED AccountInventoryHistoryCoverageMetric]"))
}
