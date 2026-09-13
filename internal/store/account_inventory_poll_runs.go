package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sunxu/relay-station-control/internal/drivers"
	"github.com/sunxu/relay-station-control/internal/inventorypoll"
	generated "github.com/sunxu/relay-station-control/internal/store/sqlc"
)

var (
	ErrInvalidPollRunInput = errors.New("store: invalid account inventory poll input")
	ErrPollRunInconsistent = errors.New("store: account inventory poll state is inconsistent")
	ErrPollRunLeaseLost    = errors.New("store: account inventory poll lease was lost")
)

type PollRunStatus string

const (
	PollRunPending   PollRunStatus = "pending"
	PollRunRunning   PollRunStatus = "running"
	PollRunRetryWait PollRunStatus = "retry_wait"
	PollRunFinalized PollRunStatus = "finalized"
	PollRunAbandoned PollRunStatus = "abandoned"
)

type PollRun struct {
	ID                     uuid.UUID
	InstanceID             uuid.UUID
	NodeType               drivers.NodeType
	DriverContractVersion  drivers.DriverContractVersion
	ScheduledAt            time.Time
	ProviderPolicyVersion  uuid.UUID
	Status                 PollRunStatus
	AttemptCount           int
	MaxAttempts            int
	CreatedAt              time.Time
	FirstStartedAt         *time.Time
	LastStartedAt          *time.Time
	FinalizedAt            *time.Time
	AbandonedAt            *time.Time
	ExecutionReason        string
	PromotionSkippedReason string
	ObservedAt             *time.Time
	Observation            *PollObservationSummary
}

type PollObservationSummary struct {
	TransportSuccess         bool
	ResponseShapeValid       bool
	ContractValid            bool
	InventoryMode            drivers.InventoryMode
	NodeIdentityComplete     bool
	SnapshotComplete         bool
	Degraded                 bool
	Result                   drivers.Result
	Reason                   drivers.Reason
	SourceRecordCount        int
	IdentifiableRecordCount  int
	UnidentifiedRecordCount  int
	UnsupportedProviderCount int
	OutOfScopeProviderCount  int
	NodeVersion              string
	NodeCommit               string
}

type PollScheduleResult struct {
	ScheduledAt   time.Time `json:"scheduled_at"`
	EligibleCount int64     `json:"eligible_count"`
	CreatedCount  int64     `json:"created_count"`
}

type PollClaim struct {
	PollRun
	LeaseExpiresAt    time.Time
	LeaseFencingToken uuid.UUID
	GraceRemaining    time.Duration
	Target            drivers.NodeTarget
	ProviderPolicy    drivers.ProviderPolicySnapshot
}

type PollProviderResult struct {
	Provider               string `json:"provider"`
	IdentifiableCount      int    `json:"identifiable_count"`
	MissingIdentityCount   int    `json:"missing_identity_count"`
	DuplicateIdentityCount int    `json:"duplicate_identity_count"`
	IdentityComplete       bool   `json:"identity_complete"`
	SnapshotComplete       bool   `json:"snapshot_complete"`
	Degraded               bool   `json:"degraded"`
	Reason                 string `json:"reason"`
	PromotionApplied       bool   `json:"promotion_applied,omitempty"`
	PromotionSkippedReason string `json:"promotion_skipped_reason,omitempty"`
}

type FinalizePollRunInput struct {
	PollRunID         uuid.UUID
	LeaseFencingToken uuid.UUID
	Observation       PollObservationSummary
	ProviderResults   []PollProviderResult
	SnapshotItems     []pollSnapshotItem
	Duplicates        []pollDuplicateEvidence
}

type PollRunMetric struct {
	InstanceID       uuid.UUID
	Status           PollRunStatus
	ScheduledAt      time.Time
	SchedulerLag     time.Duration
	QueueWait        time.Duration
	PollStartLag     *time.Duration
	TransportSuccess *bool
	ContractValid    *bool
}

type PollProviderMetric struct {
	InstanceID             uuid.UUID
	Provider               string
	SnapshotComplete       bool
	PromotionEvaluated     bool
	PromotionApplied       bool
	PromotionSkippedReason string
}

type AccountInventoryLifecycle string

const (
	AccountInventoryPresent          AccountInventoryLifecycle = "present"
	AccountInventorySuspectedMissing AccountInventoryLifecycle = "suspected_missing"
	AccountInventoryMissing          AccountInventoryLifecycle = "missing"
	AccountInventoryOutOfScope       AccountInventoryLifecycle = "out_of_scope"
)

type AccountInventoryLifecycleMetric struct {
	InstanceID uuid.UUID
	Provider   string
	Lifecycle  AccountInventoryLifecycle
	Count      uint64
}

type CurrentAccountInventoryLifecycleItem struct {
	Provider                string
	AccountKey              string
	NormalizedEmail         string
	BasicStatus             drivers.AccountState
	SuccessCount            uint64
	FailedCount             uint64
	RecentRequestCount      uint64
	LastRefreshAt           *time.Time
	NextRetryAt             *time.Time
	SourceUpdatedAt         *time.Time
	Lifecycle               AccountInventoryLifecycle
	ConsecutiveMissingCount int
	MissingSince            *time.Time
	OutOfScopeSince         *time.Time
	FirstSeenAt             time.Time
	LastSeenAt              time.Time
	CurrentPollRunID        *uuid.UUID
	CurrentScheduledAt      time.Time
	SourceObservedAt        time.Time
	SourceNodeVersion       string
	SourceNodeCommit        string
	UpdatedAt               time.Time
}

type CurrentAccountInventorySnapshotItem struct {
	AccountKey         string
	NormalizedEmail    string
	BasicStatus        drivers.AccountState
	SuccessCount       uint64
	FailedCount        uint64
	RecentRequestCount uint64
	LastRefreshAt      *time.Time
	NextRetryAt        *time.Time
	SourceUpdatedAt    *time.Time
	ObservedAt         time.Time
}

func (FinalizePollRunInput) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED FinalizePollRunInput]"))
}

func (CurrentAccountInventorySnapshotItem) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED CurrentAccountInventorySnapshotItem]"))
}

func (CurrentAccountInventoryLifecycleItem) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED CurrentAccountInventoryLifecycleItem]"))
}

type pollSnapshotItem struct {
	Provider                    string               `json:"provider"`
	AccountKey                  string               `json:"account_key"`
	Email                       string               `json:"email"`
	BasicStatus                 drivers.AccountState `json:"basic_status"`
	SuccessCount                uint64               `json:"success_count"`
	FailedCount                 uint64               `json:"failed_count"`
	RecentRequestCount          uint64               `json:"recent_request_count"`
	LastRefreshUnix             *int64               `json:"last_refresh_unix"`
	NextRetryUnix               *int64               `json:"next_retry_unix"`
	UpdatedAtUnix               *int64               `json:"updated_at_unix"`
	AvailabilityRuntimeEvidence *string              `json:"availability_runtime_evidence"`
	AuthFailureReason           *string              `json:"auth_failure_reason"`
}

type pollDuplicateEvidence struct {
	Provider        string `json:"provider"`
	AccountKey      string `json:"account_key"`
	OccurrenceCount uint32 `json:"occurrence_count"`
}

type InventoryPollRepository struct {
	pool    *pgxpool.Pool
	queries *generated.Queries
}

var _ inventorypoll.Repository = (*InventoryPollRepository)(nil)

func NewInventoryPollRepository(pool *pgxpool.Pool) (*InventoryPollRepository, error) {
	if pool == nil {
		return nil, errors.New("store: account inventory poll database is unavailable")
	}
	return &InventoryPollRepository{pool: pool, queries: generated.New(pool)}, nil
}

func (repository *InventoryPollRepository) AuthorizeDispatch(ctx context.Context, request inventorypoll.DispatchAuthorizationRequest) (inventorypoll.DispatchAuthorization, error) {
	if request.PollRunID == uuid.Nil || request.FencingToken == uuid.Nil || request.Attempt < 1 || request.RequestTimeout <= 0 {
		return inventorypoll.DispatchAuthorization{}, ErrInvalidPollRunInput
	}
	var leaseSeconds, graceSeconds float64
	err := repository.pool.QueryRow(ctx,
		`SELECT lease_remaining_seconds, grace_remaining_seconds
		 FROM control_authorize_account_inventory_poll_dispatch($1,$2,$3)`,
		request.PollRunID, request.Attempt, request.FencingToken,
	).Scan(&leaseSeconds, &graceSeconds)
	if errors.Is(err, pgx.ErrNoRows) {
		return inventorypoll.DispatchAuthorization{}, ErrPollRunLeaseLost
	}
	if err != nil {
		return inventorypoll.DispatchAuthorization{}, err
	}
	return inventorypoll.DispatchAuthorization{LeaseRemaining: time.Duration(leaseSeconds * float64(time.Second)), GraceRemaining: time.Duration(graceSeconds * float64(time.Second))}, nil
}

func (repository *InventoryPollRepository) ScheduleCurrent(
	ctx context.Context, request inventorypoll.ScheduleRequest,
) (inventorypoll.ScheduleResult, error) {
	stored, err := repository.scheduleCurrent(ctx, request.Period, request.PollStartGrace, request.MaxAttempts, request.Limit)
	if errors.Is(err, ErrInvalidPollRunInput) {
		return inventorypoll.ScheduleResult{}, inventorypoll.ErrInvalidRepositoryResult
	}
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "22023" && pgErr.Message == "account inventory poll capacity exceeded" {
			return inventorypoll.ScheduleResult{}, inventorypoll.ErrCapacityExceeded
		}
		return inventorypoll.ScheduleResult{}, err
	}
	existing := stored.EligibleCount - stored.CreatedCount
	return inventorypoll.ScheduleResult{
		ScheduledAt: stored.ScheduledAt, Eligible: int(stored.EligibleCount),
		Created: int(stored.CreatedCount), Existing: int(existing),
	}, nil
}

func (repository *InventoryPollRepository) ClaimRunnable(
	ctx context.Context, request inventorypoll.ClaimRequest,
) (*inventorypoll.ClaimedRun, error) {
	stored, err := repository.claim(ctx, request.Token, request.LeaseDuration)
	if errors.Is(err, ErrInvalidPollRunInput) {
		return nil, inventorypoll.ErrInvalidRepositoryResult
	}
	if err != nil {
		return nil, err
	}
	if stored == nil {
		return nil, inventorypoll.ErrNoWork
	}
	return &inventorypoll.ClaimedRun{
		PollRunID: stored.ID, InstanceID: stored.InstanceID,
		PolicyVersionID: stored.ProviderPolicyVersion, ScheduledAt: stored.ScheduledAt,
		Attempt: stored.AttemptCount, MaxAttempts: stored.MaxAttempts,
		FencingToken: stored.LeaseFencingToken, GraceRemaining: stored.GraceRemaining,
		Target: stored.Target, ProviderPolicy: stored.ProviderPolicy,
	}, nil
}

func (repository *InventoryPollRepository) FinalizeFenced(
	ctx context.Context, request inventorypoll.FinalizeRequest,
) error {
	sourceRecordCount := uint64(request.Node.RecognizedRecordCount) +
		uint64(request.Node.UnidentifiedRecordCount) +
		uint64(request.Node.UnsupportedProviderCount) +
		uint64(request.Node.OutOfScopeProviderCount)
	if sourceRecordCount > 1000 {
		return inventorypoll.ErrInvalidObservation
	}
	providers := make([]PollProviderResult, 0, len(request.Providers))
	for _, provider := range request.Providers {
		providers = append(providers, PollProviderResult{
			Provider:               provider.Provider,
			IdentifiableCount:      int(provider.RecognizedRecordCount),
			MissingIdentityCount:   int(provider.MissingIdentityCount),
			DuplicateIdentityCount: int(provider.DuplicateIdentityCount),
			IdentityComplete:       provider.IdentityComplete,
			SnapshotComplete:       provider.SnapshotComplete,
			Degraded:               provider.Degraded, Reason: string(provider.Reason),
		})
	}
	snapshotItems := make([]pollSnapshotItem, 0, len(request.SnapshotItems))
	for _, item := range request.SnapshotItems {
		if !validSnapshotCandidate(item) {
			return inventorypoll.ErrInvalidObservation
		}
		snapshotItems = append(snapshotItems, pollSnapshotItem{
			Provider: item.Provider, AccountKey: item.AccountKey, Email: item.Email,
			BasicStatus: item.BasicStatus, SuccessCount: item.SuccessCount,
			FailedCount: item.FailedCount, RecentRequestCount: item.RecentRequestCount,
			LastRefreshUnix: item.LastRefreshUnix, NextRetryUnix: item.NextRetryUnix,
			UpdatedAtUnix:               item.UpdatedAtUnix,
			AvailabilityRuntimeEvidence: item.AvailabilityRuntimeEvidence,
			AuthFailureReason:           item.AuthFailureReason,
		})
	}
	duplicates := make([]pollDuplicateEvidence, 0, len(request.Duplicates))
	for _, duplicate := range request.Duplicates {
		if !validDuplicateEvidence(duplicate) {
			return inventorypoll.ErrInvalidObservation
		}
		duplicates = append(duplicates, pollDuplicateEvidence{
			Provider: duplicate.Provider, AccountKey: duplicate.AccountKey,
			OccurrenceCount: duplicate.OccurrenceCount,
		})
	}
	_, err := repository.finalize(ctx, FinalizePollRunInput{
		PollRunID: request.PollRunID, LeaseFencingToken: request.FencingToken,
		Observation: PollObservationSummary{
			TransportSuccess:     request.Node.TransportSuccess,
			ResponseShapeValid:   request.Node.ResponseShapeValid,
			ContractValid:        request.Node.ContractValid,
			InventoryMode:        request.Node.InventoryMode,
			NodeIdentityComplete: request.Node.NodeIdentityComplete,
			SnapshotComplete:     request.Node.SnapshotComplete,
			Degraded:             request.Node.Degraded, Result: request.Node.Result,
			Reason: request.Node.Reason, SourceRecordCount: int(sourceRecordCount),
			IdentifiableRecordCount:  int(request.Node.RecognizedRecordCount),
			UnidentifiedRecordCount:  int(request.Node.UnidentifiedRecordCount),
			UnsupportedProviderCount: int(request.Node.UnsupportedProviderCount),
			OutOfScopeProviderCount:  int(request.Node.OutOfScopeProviderCount),
			NodeVersion:              request.Node.Version, NodeCommit: request.Node.Commit,
		},
		ProviderResults: providers, SnapshotItems: snapshotItems, Duplicates: duplicates,
	})
	if errors.Is(err, ErrPollRunLeaseLost) {
		return inventorypoll.ErrLostLease
	}
	if errors.Is(err, ErrInvalidPollRunInput) {
		return inventorypoll.ErrInvalidObservation
	}
	return err
}

func (repository *InventoryPollRepository) ReconcileExpired(
	ctx context.Context, request inventorypoll.ReconcileRequest,
) (inventorypoll.ReconcileResult, error) {
	if _, ok := boundedWholeSeconds(request.PollStartGrace, 1, 299); !ok || request.Limit < 1 || request.Limit > 1000 {
		return inventorypoll.ReconcileResult{}, inventorypoll.ErrInvalidRepositoryResult
	}
	var result inventorypoll.ReconcileResult
	for processed := 0; processed < request.Limit; processed++ {
		run, err := repository.ReconcileOne(ctx)
		if errors.Is(err, ErrInvalidPollRunInput) {
			return inventorypoll.ReconcileResult{}, inventorypoll.ErrInvalidRepositoryResult
		}
		if err != nil {
			return inventorypoll.ReconcileResult{}, err
		}
		if run == nil {
			break
		}
		switch run.Status {
		case PollRunRetryWait:
			result.RetryWait++
		case PollRunAbandoned:
			result.Abandoned++
		default:
			return inventorypoll.ReconcileResult{}, inventorypoll.ErrInvalidRepositoryResult
		}
	}
	return result, nil
}

func (repository *InventoryPollRepository) scheduleCurrent(
	ctx context.Context, period, grace time.Duration, maxAttempts, limit int,
) (PollScheduleResult, error) {
	periodSeconds, periodOK := boundedWholeSeconds(period, 1, 86400)
	graceSeconds, ok := boundedWholeSeconds(grace, 1, 300)
	if !periodOK || !ok || periodSeconds != 300 || maxAttempts < 1 || maxAttempts > 10 || limit < 1 || limit > 1000 {
		return PollScheduleResult{}, ErrInvalidPollRunInput
	}
	encoded, err := repository.queries.ScheduleCurrentAccountInventoryPollRuns(ctx,
		generated.ScheduleCurrentAccountInventoryPollRunsParams{
			PeriodSeconds: periodSeconds, PollStartGraceSeconds: graceSeconds,
			MaxAttempts: int32(maxAttempts), ScheduleLimit: int32(limit),
		})
	if err != nil {
		return PollScheduleResult{}, err
	}
	var result PollScheduleResult
	if err := decodeStrictJSON(encoded, &result); err != nil || result.ScheduledAt.IsZero() ||
		result.EligibleCount < 0 || result.CreatedCount < 0 || result.CreatedCount > result.EligibleCount {
		return PollScheduleResult{}, ErrPollRunInconsistent
	}
	result.ScheduledAt = result.ScheduledAt.UTC()
	return result, nil
}

func (repository *InventoryPollRepository) claim(
	ctx context.Context, fencingToken uuid.UUID, lease time.Duration,
) (*PollClaim, error) {
	leaseSeconds, leaseOK := boundedWholeSeconds(lease, 1, 120)
	if fencingToken == uuid.Nil || !leaseOK {
		return nil, ErrInvalidPollRunInput
	}
	encoded, err := repository.queries.ClaimAccountInventoryPollRun(ctx,
		generated.ClaimAccountInventoryPollRunParams{
			LeaseFencingToken: nullableUUID(fencingToken), LeaseSeconds: leaseSeconds,
		})
	if err != nil {
		return nil, err
	}
	if len(encoded) == 0 || bytes.Equal(bytes.TrimSpace(encoded), []byte("null")) {
		return nil, nil
	}
	var stored pollClaimJSON
	if err := decodeStrictJSON(encoded, &stored); err != nil {
		return nil, ErrPollRunInconsistent
	}
	claim, err := stored.claim()
	if err != nil || claim.LeaseFencingToken != fencingToken {
		return nil, ErrPollRunInconsistent
	}
	return claim, nil
}

func (repository *InventoryPollRepository) ReconcileOne(
	ctx context.Context,
) (*PollRun, error) {
	row, err := repository.queries.ReconcileAccountInventoryPollRun(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	result, err := pollRunFromRow(row)
	return &result, err
}

func (repository *InventoryPollRepository) finalize(
	ctx context.Context, input FinalizePollRunInput,
) (PollRun, error) {
	if input.PollRunID == uuid.Nil || input.LeaseFencingToken == uuid.Nil ||
		!validPollObservation(input.Observation) || !validPollProviderResults(input.ProviderResults) {
		return PollRun{}, ErrInvalidPollRunInput
	}
	encodedProviders, err := json.Marshal(input.ProviderResults)
	if err != nil {
		return PollRun{}, ErrInvalidPollRunInput
	}
	encodedSnapshots, err := json.Marshal(input.SnapshotItems)
	if err != nil {
		return PollRun{}, ErrInvalidPollRunInput
	}
	encodedDuplicates, err := json.Marshal(input.Duplicates)
	if err != nil {
		return PollRun{}, ErrInvalidPollRunInput
	}
	mode := pgtype.Text{}
	if input.Observation.InventoryMode != "" {
		mode = pgtype.Text{String: string(input.Observation.InventoryMode), Valid: true}
	}
	observation := input.Observation
	row, err := repository.queries.FinalizeAccountInventoryPollRunWithLifecycle(ctx,
		generated.FinalizeAccountInventoryPollRunWithLifecycleParams{
			PollRunID: nullableUUID(input.PollRunID), LeaseFencingToken: nullableUUID(input.LeaseFencingToken),
			TransportSuccess: observation.TransportSuccess, ResponseShapeValid: observation.ResponseShapeValid,
			ContractValid: observation.ContractValid, InventoryMode: mode,
			NodeIdentityComplete: observation.NodeIdentityComplete, SnapshotComplete: observation.SnapshotComplete,
			Degraded: observation.Degraded, Result: string(observation.Result), Reason: string(observation.Reason),
			SourceRecordCount:        int32(observation.SourceRecordCount),
			IdentifiableRecordCount:  int32(observation.IdentifiableRecordCount),
			UnidentifiedRecordCount:  int32(observation.UnidentifiedRecordCount),
			UnsupportedProviderCount: int32(observation.UnsupportedProviderCount),
			OutOfScopeProviderCount:  int32(observation.OutOfScopeProviderCount),
			NodeVersion:              observation.NodeVersion, NodeCommit: observation.NodeCommit,
			ProviderResults: encodedProviders, SnapshotItems: encodedSnapshots,
			DuplicateEvidence: encodedDuplicates,
		})
	var databaseError *pgconn.PgError
	if errors.Is(err, pgx.ErrNoRows) || errors.As(err, &databaseError) && databaseError.Code == "P0002" {
		return PollRun{}, ErrPollRunLeaseLost
	}
	if err != nil {
		return PollRun{}, err
	}
	return pollRunFromRow(row)
}

func (repository *InventoryPollRepository) ProviderResults(
	ctx context.Context, pollRunID uuid.UUID,
) ([]PollProviderResult, error) {
	if pollRunID == uuid.Nil {
		return nil, ErrInvalidPollRunInput
	}
	rows, err := repository.queries.ListAccountInventoryPollProviderResults(ctx, nullableUUID(pollRunID))
	if err != nil {
		return nil, err
	}
	results := make([]PollProviderResult, 0, len(rows))
	for _, row := range rows {
		result := PollProviderResult{
			Provider: row.Provider, IdentifiableCount: int(row.IdentifiableCount),
			MissingIdentityCount: int(row.MissingIdentityCount), DuplicateIdentityCount: int(row.DuplicateIdentityCount),
			IdentityComplete: row.IdentityComplete, SnapshotComplete: row.SnapshotComplete,
			Degraded: row.Degraded, Reason: row.Reason,
			PromotionApplied:       row.PromotionApplied,
			PromotionSkippedReason: nullableTextValue(row.PromotionSkippedReason),
		}
		if !validPollProviderResult(result) {
			return nil, ErrPollRunInconsistent
		}
		results = append(results, result)
	}
	return results, nil
}

func (repository *InventoryPollRepository) Metrics(ctx context.Context) ([]PollRunMetric, []PollProviderMetric, error) {
	runs, providers, _, err := repository.metrics(ctx, false)
	return runs, providers, err
}

func (repository *InventoryPollRepository) MetricsWithLifecycle(
	ctx context.Context, includeLifecycle bool,
) ([]PollRunMetric, []PollProviderMetric, []AccountInventoryLifecycleMetric, error) {
	return repository.metrics(ctx, includeLifecycle)
}

func (repository *InventoryPollRepository) metrics(
	ctx context.Context, includeLifecycle bool,
) ([]PollRunMetric, []PollProviderMetric, []AccountInventoryLifecycleMetric, error) {
	runs, err := repository.queries.ListAccountInventoryPollRunMetrics(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	providers, err := repository.queries.ListAccountInventoryProviderMetrics(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	var lifecycles []generated.ListAccountInventoryLifecycleMetricsRow
	if includeLifecycle {
		lifecycles, err = repository.queries.ListAccountInventoryLifecycleMetrics(ctx)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	runMetrics := make([]PollRunMetric, 0, len(runs))
	for _, row := range runs {
		metric := PollRunMetric{
			InstanceID: uuidFromPG(row.InstanceID), Status: PollRunStatus(row.Status),
			ScheduledAt:      row.ScheduledAt.Time.UTC(),
			SchedulerLag:     secondsDuration(row.SchedulerLagSeconds),
			QueueWait:        secondsDuration(row.QueueWaitSeconds),
			PollStartLag:     optionalSecondsDuration(row.PollStarted, row.PollStartLagSeconds),
			TransportSuccess: boolPointer(row.TransportSuccess), ContractValid: boolPointer(row.ContractValid),
		}
		if metric.InstanceID == uuid.Nil || !validPollRunStatus(metric.Status) || metric.ScheduledAt.IsZero() {
			return nil, nil, nil, ErrPollRunInconsistent
		}
		runMetrics = append(runMetrics, metric)
	}
	providerMetrics := make([]PollProviderMetric, 0, len(providers))
	for _, row := range providers {
		if uuidFromPG(row.InstanceID) == uuid.Nil || !validProviderName(row.Provider) {
			return nil, nil, nil, ErrPollRunInconsistent
		}
		providerMetrics = append(providerMetrics, PollProviderMetric{
			InstanceID: uuidFromPG(row.InstanceID), Provider: row.Provider,
			SnapshotComplete: row.SnapshotComplete, PromotionEvaluated: row.PromotionEvaluated,
			PromotionApplied:       row.PromotionApplied,
			PromotionSkippedReason: nullableTextValue(row.PromotionSkippedReason),
		})
	}
	lifecycleMetrics := make([]AccountInventoryLifecycleMetric, 0, len(lifecycles))
	for _, row := range lifecycles {
		metric := AccountInventoryLifecycleMetric{
			InstanceID: uuidFromPG(row.InstanceID), Provider: row.Provider,
			Lifecycle: AccountInventoryLifecycle(row.Lifecycle),
		}
		if row.AccountCount < 0 || metric.InstanceID == uuid.Nil || !validProviderName(metric.Provider) ||
			!validAccountInventoryLifecycle(metric.Lifecycle) {
			return nil, nil, nil, ErrPollRunInconsistent
		}
		metric.Count = uint64(row.AccountCount)
		lifecycleMetrics = append(lifecycleMetrics, metric)
	}
	return runMetrics, providerMetrics, lifecycleMetrics, nil
}

func (repository *InventoryPollRepository) CheckLifecycleCompatibility(ctx context.Context) error {
	compatible, err := repository.queries.CheckAccountInventoryLifecycleCompatibility(ctx)
	if err != nil || !compatible {
		return errors.New("store: account inventory lifecycle database is incompatible")
	}
	return nil
}

func (repository *InventoryPollRepository) CurrentLifecycle(
	ctx context.Context, instanceID uuid.UUID, provider string, lifecycle AccountInventoryLifecycle,
	afterAccountKey string, limit int,
) ([]CurrentAccountInventoryLifecycleItem, error) {
	if instanceID == uuid.Nil || provider != "" && !validProviderName(provider) ||
		lifecycle != "" && !validAccountInventoryLifecycle(lifecycle) ||
		len(afterAccountKey) > 385 || limit < 1 || limit > 200 {
		return nil, ErrInvalidPollRunInput
	}
	rows, err := repository.queries.ListCurrentAccountInventoryLifecycle(ctx,
		generated.ListCurrentAccountInventoryLifecycleParams{
			InstanceID: nullableUUID(instanceID), Provider: provider, Lifecycle: string(lifecycle),
			AfterAccountKey: afterAccountKey, PageLimit: int32(limit),
		})
	if err != nil {
		return nil, err
	}
	items := make([]CurrentAccountInventoryLifecycleItem, 0, len(rows))
	for _, row := range rows {
		item := CurrentAccountInventoryLifecycleItem{
			Provider: row.Provider, AccountKey: row.AccountKey, NormalizedEmail: row.NormalizedEmail,
			BasicStatus:             drivers.AccountState(row.BasicStatus),
			Lifecycle:               AccountInventoryLifecycle(row.Lifecycle),
			ConsecutiveMissingCount: int(row.ConsecutiveMissingCount),
			SuccessCount:            uint64(row.SuccessCount), FailedCount: uint64(row.FailedCount),
			RecentRequestCount: uint64(row.RecentRequestCount),
			LastRefreshAt:      nullableTime(row.LastRefreshAt), NextRetryAt: nullableTime(row.NextRetryAt),
			SourceUpdatedAt: nullableTime(row.SourceUpdatedAt), MissingSince: nullableTime(row.MissingSince),
			OutOfScopeSince: nullableTime(row.OutOfScopeSince), FirstSeenAt: row.FirstSeenAt.Time.UTC(),
			LastSeenAt: row.LastSeenAt.Time.UTC(), CurrentPollRunID: nullableUUIDPointer(row.CurrentPollRunID),
			CurrentScheduledAt: row.CurrentScheduledAt.Time.UTC(), SourceObservedAt: row.SourceObservedAt.Time.UTC(),
			SourceNodeVersion: row.SourceNodeVersion, SourceNodeCommit: row.SourceNodeCommit,
			UpdatedAt: row.UpdatedAt.Time.UTC(),
		}
		if row.SuccessCount < 0 || row.FailedCount < 0 || row.RecentRequestCount < 0 ||
			!validCurrentLifecycleItem(item) {
			return nil, ErrPollRunInconsistent
		}
		items = append(items, item)
	}
	return items, nil
}

func (repository *InventoryPollRepository) CurrentProviderSnapshot(
	ctx context.Context, instanceID uuid.UUID, provider, afterAccountKey string, limit int,
) ([]CurrentAccountInventorySnapshotItem, error) {
	if instanceID == uuid.Nil || !validProviderName(provider) || len(afterAccountKey) > 385 || limit < 1 || limit > 500 {
		return nil, ErrInvalidPollRunInput
	}
	rows, err := repository.queries.ListCurrentAccountInventorySnapshot(ctx,
		generated.ListCurrentAccountInventorySnapshotParams{
			InstanceID: nullableUUID(instanceID), Provider: provider,
			AfterAccountKey: afterAccountKey, PageLimit: int32(limit),
		})
	if err != nil {
		return nil, err
	}
	result := make([]CurrentAccountInventorySnapshotItem, 0, len(rows))
	for _, row := range rows {
		if row.SuccessCount < 0 || row.FailedCount < 0 || row.RecentRequestCount < 0 ||
			row.ObservedAt.Time.IsZero() {
			return nil, ErrPollRunInconsistent
		}
		item := CurrentAccountInventorySnapshotItem{
			AccountKey: row.AccountKey, NormalizedEmail: row.NormalizedEmail,
			BasicStatus: drivers.AccountState(row.BasicStatus), SuccessCount: uint64(row.SuccessCount),
			FailedCount: uint64(row.FailedCount), RecentRequestCount: uint64(row.RecentRequestCount),
			LastRefreshAt: nullableTime(row.LastRefreshAt), NextRetryAt: nullableTime(row.NextRetryAt),
			SourceUpdatedAt: nullableTime(row.SourceUpdatedAt), ObservedAt: row.ObservedAt.Time.UTC(),
		}
		if !validCurrentSnapshotItem(provider, item) {
			return nil, ErrPollRunInconsistent
		}
		result = append(result, item)
	}
	return result, nil
}

type pollClaimJSON struct {
	PollRunID             uuid.UUID `json:"poll_run_id"`
	InstanceID            uuid.UUID `json:"instance_id"`
	NodeType              string    `json:"node_type"`
	DriverContractVersion string    `json:"driver_contract_version"`
	ScheduledAt           time.Time `json:"scheduled_at"`
	ProviderPolicyVersion uuid.UUID `json:"provider_policy_version"`
	AttemptCount          int       `json:"attempt_count"`
	MaxAttempts           int       `json:"max_attempts"`
	FirstStartedAt        time.Time `json:"first_started_at"`
	LastStartedAt         time.Time `json:"last_started_at"`
	LeaseExpiresAt        time.Time `json:"lease_expires_at"`
	LeaseFencingToken     uuid.UUID `json:"lease_fencing_token"`
	GraceRemainingMillis  int64     `json:"grace_remaining_milliseconds"`
	ManagementEndpoint    string    `json:"management_endpoint"`
	ReaderSecretReference string    `json:"reader_secret_ref"`
	ActiveProviders       []string  `json:"active_providers"`
	OutOfScopeProviders   []string  `json:"out_of_scope_providers"`
}

func (stored pollClaimJSON) claim() (*PollClaim, error) {
	if stored.PollRunID == uuid.Nil || stored.InstanceID == uuid.Nil || stored.ProviderPolicyVersion == uuid.Nil ||
		stored.LeaseFencingToken == uuid.Nil || stored.ScheduledAt.IsZero() || stored.FirstStartedAt.IsZero() ||
		stored.LastStartedAt.IsZero() || stored.LeaseExpiresAt.IsZero() || stored.AttemptCount < 1 ||
		stored.AttemptCount > stored.MaxAttempts || stored.GraceRemainingMillis <= 0 ||
		stored.ManagementEndpoint == "" || len(stored.ActiveProviders) == 0 {
		return nil, ErrPollRunInconsistent
	}
	first, last := stored.FirstStartedAt.UTC(), stored.LastStartedAt.UTC()
	return &PollClaim{
		PollRun: PollRun{
			ID: stored.PollRunID, InstanceID: stored.InstanceID,
			NodeType: drivers.NodeType(stored.NodeType), DriverContractVersion: drivers.DriverContractVersion(stored.DriverContractVersion),
			ScheduledAt: stored.ScheduledAt.UTC(), ProviderPolicyVersion: stored.ProviderPolicyVersion,
			Status: PollRunRunning, AttemptCount: stored.AttemptCount, MaxAttempts: stored.MaxAttempts,
			FirstStartedAt: &first, LastStartedAt: &last,
		},
		LeaseExpiresAt: stored.LeaseExpiresAt.UTC(), LeaseFencingToken: stored.LeaseFencingToken,
		GraceRemaining: time.Duration(stored.GraceRemainingMillis) * time.Millisecond,
		Target: drivers.NodeTarget{
			InstanceID: stored.InstanceID, NodeType: drivers.NodeType(stored.NodeType),
			DriverContractVersion: drivers.DriverContractVersion(stored.DriverContractVersion),
			ManagementEndpoint:    stored.ManagementEndpoint,
			ReaderSecretReference: drivers.NewSecretReference(stored.ReaderSecretReference),
			Capabilities:          []drivers.Capability{drivers.CapabilityManagementAccountInventoryRead},
		},
		ProviderPolicy: drivers.ProviderPolicySnapshot{
			VersionID:           stored.ProviderPolicyVersion,
			ActiveProviders:     append([]string(nil), stored.ActiveProviders...),
			OutOfScopeProviders: append([]string(nil), stored.OutOfScopeProviders...),
		},
	}, nil
}

func pollRunFromRow(row generated.AccountInventoryPollRun) (PollRun, error) {
	result := PollRun{
		ID: uuidFromPG(row.PollRunID), InstanceID: uuidFromPG(row.InstanceID),
		NodeType: drivers.NodeType(row.NodeType), DriverContractVersion: drivers.DriverContractVersion(row.DriverContractVersion),
		ScheduledAt: row.ScheduledAt.Time.UTC(), ProviderPolicyVersion: uuidFromPG(row.ProviderPolicyVersion),
		Status: PollRunStatus(row.Status), AttemptCount: int(row.AttemptCount), MaxAttempts: int(row.MaxAttempts),
		CreatedAt: row.CreatedAt.Time.UTC(), FirstStartedAt: nullableTime(row.FirstStartedAt),
		LastStartedAt: nullableTime(row.LastStartedAt), FinalizedAt: nullableTime(row.FinalizedAt),
		AbandonedAt: nullableTime(row.AbandonedAt), ExecutionReason: nullableTextValue(row.ExecutionReason),
		ObservedAt: nullableTime(row.ObservedAt), PromotionSkippedReason: nullableTextValue(row.PromotionSkippedReason),
	}
	if row.Status == string(PollRunFinalized) {
		result.Observation = &PollObservationSummary{
			TransportSuccess: row.TransportSuccess.Bool, ResponseShapeValid: row.ResponseShapeValid.Bool,
			ContractValid: row.ContractValid.Bool, InventoryMode: drivers.InventoryMode(nullableTextValue(row.InventoryMode)),
			NodeIdentityComplete: row.NodeIdentityComplete.Bool, SnapshotComplete: row.SnapshotComplete.Bool,
			Degraded: row.Degraded.Bool, Result: drivers.Result(nullableTextValue(row.Result)),
			Reason: drivers.Reason(nullableTextValue(row.Reason)), SourceRecordCount: int(row.SourceRecordCount.Int32),
			IdentifiableRecordCount:  int(row.IdentifiableRecordCount.Int32),
			UnidentifiedRecordCount:  int(row.UnidentifiedRecordCount.Int32),
			UnsupportedProviderCount: int(row.UnsupportedProviderCount.Int32),
			OutOfScopeProviderCount:  int(row.OutOfScopeProviderCount.Int32),
			NodeVersion:              nullableTextValue(row.NodeVersion), NodeCommit: nullableTextValue(row.NodeCommit),
		}
	}
	if result.ID == uuid.Nil || result.InstanceID == uuid.Nil || result.ProviderPolicyVersion == uuid.Nil ||
		!validPollRunStatus(result.Status) || result.ScheduledAt.IsZero() || result.CreatedAt.IsZero() ||
		result.AttemptCount < 0 || result.AttemptCount > result.MaxAttempts ||
		(result.Status == PollRunFinalized && (result.ObservedAt == nil || result.Observation == nil || !validPollObservation(*result.Observation))) {
		return PollRun{}, ErrPollRunInconsistent
	}
	return result, nil
}

func validPollObservation(observation PollObservationSummary) bool {
	if observation.SourceRecordCount < 0 || observation.SourceRecordCount > 1000 ||
		observation.IdentifiableRecordCount < 0 || observation.IdentifiableRecordCount > 1000 ||
		observation.UnidentifiedRecordCount < 0 || observation.UnidentifiedRecordCount > 1000 ||
		observation.UnsupportedProviderCount < 0 || observation.UnsupportedProviderCount > 1000 ||
		observation.OutOfScopeProviderCount < 0 || observation.OutOfScopeProviderCount > 1000 {
		return false
	}
	if !observation.TransportSuccess && (observation.ResponseShapeValid || observation.ContractValid) ||
		!observation.ResponseShapeValid && observation.ContractValid ||
		observation.SnapshotComplete && !observation.ContractValid {
		return false
	}
	if observation.ContractValid {
		if (observation.InventoryMode != drivers.InventoryModeRuntime && observation.InventoryMode != drivers.InventoryModeDiskFallback) ||
			observation.Reason != drivers.ReasonNone {
			return false
		}
		return observation.Result == drivers.ResultSuccess && observation.SnapshotComplete && !observation.Degraded ||
			observation.Result == drivers.ResultDegraded && observation.Degraded
	}
	return observation.InventoryMode == "" && observation.Result == drivers.ResultFailed &&
		observation.Reason != drivers.ReasonNone && observation.Degraded
}

func validPollProviderResults(results []PollProviderResult) bool {
	if len(results) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(results))
	for _, result := range results {
		if !validPollProviderResult(result) {
			return false
		}
		if _, duplicate := seen[result.Provider]; duplicate {
			return false
		}
		seen[result.Provider] = struct{}{}
	}
	return true
}

func validPollProviderResult(result PollProviderResult) bool {
	if !validProviderName(result.Provider) || result.IdentifiableCount < 0 || result.IdentifiableCount > 1000 ||
		result.MissingIdentityCount < 0 || result.MissingIdentityCount > 1000 ||
		result.DuplicateIdentityCount < 0 || result.DuplicateIdentityCount > 1000 ||
		result.IdentityComplete != (result.MissingIdentityCount == 0 && result.DuplicateIdentityCount == 0) {
		return false
	}
	baseValid := false
	switch result.Reason {
	case "complete":
		baseValid = result.IdentityComplete && result.SnapshotComplete && !result.Degraded
	case "transport_failed", "contract_invalid", "disk_fallback", "node_identity_incomplete", "identity_incomplete":
		baseValid = result.Degraded
	default:
		return false
	}
	if !baseValid || result.PromotionApplied && (result.PromotionSkippedReason != "" || result.Reason != "complete") {
		return false
	}
	if result.PromotionSkippedReason == "" {
		return true
	}
	if result.PromotionApplied {
		return false
	}
	switch result.PromotionSkippedReason {
	case "policy_changed", "transport_failed", "contract_invalid", "disk_fallback",
		"provider_identity_incomplete", "provider_duplicate", "stale_poll":
		return true
	default:
		return false
	}
}

func validSnapshotCandidate(item inventorypoll.SnapshotCandidate) bool {
	if !validProviderName(item.Provider) || !validNormalizedIdentity(item.Email, 320) ||
		item.Email != strings.ToLower(strings.TrimSpace(item.Email)) || len(item.AccountKey) < 3 || len(item.AccountKey) > 385 ||
		item.AccountKey != item.Provider+":"+item.Email || item.SuccessCount > math.MaxInt64 ||
		item.FailedCount > math.MaxInt64 || item.RecentRequestCount > 1000 ||
		!validSourceUnix(item.LastRefreshUnix) || !validSourceUnix(item.NextRetryUnix) ||
		!validSourceUnix(item.UpdatedAtUnix) {
		return false
	}
	switch item.BasicStatus {
	case drivers.AccountStateDisabled, drivers.AccountStateUnavailable, drivers.AccountStateError,
		drivers.AccountStateActive, drivers.AccountStateUnknown:
		return true
	default:
		return false
	}
}

func validDuplicateEvidence(duplicate inventorypoll.DuplicateEvidence) bool {
	suffix := strings.TrimPrefix(duplicate.AccountKey, duplicate.Provider+":")
	return validProviderName(duplicate.Provider) && validNormalizedIdentity(suffix, 320) && len(duplicate.AccountKey) >= 3 &&
		len(duplicate.AccountKey) <= 385 && strings.HasPrefix(duplicate.AccountKey, duplicate.Provider+":") &&
		len(suffix) >= 1 && len(suffix) <= 320 && suffix == strings.ToLower(strings.TrimSpace(suffix)) &&
		duplicate.OccurrenceCount >= 2 && duplicate.OccurrenceCount <= 1000
}

func validSourceUnix(value *int64) bool {
	return value == nil || *value >= 1 && *value <= 253402300799
}

func validNormalizedIdentity(value string, maximum int) bool {
	if !utf8.ValidString(value) || len(value) < 1 || len(value) > maximum {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validCurrentSnapshotItem(provider string, item CurrentAccountInventorySnapshotItem) bool {
	if !validNormalizedIdentity(item.NormalizedEmail, 320) ||
		item.NormalizedEmail != strings.ToLower(strings.TrimSpace(item.NormalizedEmail)) ||
		item.AccountKey != provider+":"+item.NormalizedEmail {
		return false
	}
	switch item.BasicStatus {
	case drivers.AccountStateDisabled, drivers.AccountStateUnavailable, drivers.AccountStateError,
		drivers.AccountStateActive, drivers.AccountStateUnknown:
		return true
	default:
		return false
	}
}

func validCurrentLifecycleItem(item CurrentAccountInventoryLifecycleItem) bool {
	if !validProviderName(item.Provider) || !validNormalizedIdentity(item.NormalizedEmail, 320) ||
		item.NormalizedEmail != strings.ToLower(strings.TrimSpace(item.NormalizedEmail)) ||
		item.AccountKey != item.Provider+":"+item.NormalizedEmail ||
		item.SuccessCount > math.MaxInt64 || item.FailedCount > math.MaxInt64 ||
		item.RecentRequestCount > 1000 || item.FirstSeenAt.IsZero() || item.LastSeenAt.IsZero() ||
		item.CurrentScheduledAt.IsZero() || item.SourceObservedAt.IsZero() || item.UpdatedAt.IsZero() ||
		item.FirstSeenAt.After(item.LastSeenAt) || item.LastSeenAt.After(item.UpdatedAt) ||
		!item.SourceObservedAt.Equal(item.LastSeenAt) || item.CurrentScheduledAt.Unix()%300 != 0 ||
		!validNormalizedIdentity(item.SourceNodeVersion, 64) || !validNormalizedIdentity(item.SourceNodeCommit, 64) ||
		item.MissingSince != nil && !item.MissingSince.After(item.LastSeenAt) ||
		item.OutOfScopeSince != nil && item.OutOfScopeSince.Before(item.LastSeenAt) {
		return false
	}
	switch item.BasicStatus {
	case drivers.AccountStateDisabled, drivers.AccountStateUnavailable, drivers.AccountStateError,
		drivers.AccountStateActive, drivers.AccountStateUnknown:
	default:
		return false
	}
	switch item.Lifecycle {
	case AccountInventoryPresent:
		return item.ConsecutiveMissingCount == 0 && item.MissingSince == nil && item.OutOfScopeSince == nil
	case AccountInventorySuspectedMissing:
		return item.ConsecutiveMissingCount == 1 && item.MissingSince == nil && item.OutOfScopeSince == nil
	case AccountInventoryMissing:
		return item.ConsecutiveMissingCount == 2 && item.MissingSince != nil && item.OutOfScopeSince == nil
	case AccountInventoryOutOfScope:
		return item.ConsecutiveMissingCount == 0 && item.MissingSince == nil && item.OutOfScopeSince != nil
	default:
		return false
	}
}

func validAccountInventoryLifecycle(value AccountInventoryLifecycle) bool {
	switch value {
	case AccountInventoryPresent, AccountInventorySuspectedMissing, AccountInventoryMissing, AccountInventoryOutOfScope:
		return true
	default:
		return false
	}
}

func validProviderName(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for index, character := range []byte(value) {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' ||
			index > 0 && (character == '.' || character == '_' || character == '-') {
			continue
		}
		return false
	}
	return true
}

func validPollRunStatus(status PollRunStatus) bool {
	switch status {
	case PollRunPending, PollRunRunning, PollRunRetryWait, PollRunFinalized, PollRunAbandoned:
		return true
	default:
		return false
	}
}

func boundedWholeSeconds(value time.Duration, minimum, maximum int32) (int32, bool) {
	if value%time.Second != 0 {
		return 0, false
	}
	seconds := value / time.Second
	return int32(seconds), seconds >= time.Duration(minimum) && seconds <= time.Duration(maximum)
}

func decodeStrictJSON(encoded []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) == nil {
		return errors.New("trailing JSON")
	}
	return nil
}

func nullableTextValue(value pgtype.Text) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func nullableUUIDPointer(value pgtype.UUID) *uuid.UUID {
	if !value.Valid {
		return nil
	}
	result := uuidFromPG(value)
	return &result
}

func boolPointer(value pgtype.Bool) *bool {
	if !value.Valid {
		return nil
	}
	result := value.Bool
	return &result
}

func secondsDuration(value float64) time.Duration {
	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return time.Duration(value * float64(time.Second))
}

func optionalSecondsDuration(valid bool, value float64) *time.Duration {
	if !valid {
		return nil
	}
	result := secondsDuration(value)
	return &result
}
