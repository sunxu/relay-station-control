package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
	ID                    uuid.UUID
	InstanceID            uuid.UUID
	NodeType              drivers.NodeType
	DriverContractVersion drivers.DriverContractVersion
	ScheduledAt           time.Time
	ProviderPolicyVersion uuid.UUID
	Status                PollRunStatus
	AttemptCount          int
	MaxAttempts           int
	CreatedAt             time.Time
	FirstStartedAt        *time.Time
	LastStartedAt         *time.Time
	FinalizedAt           *time.Time
	AbandonedAt           *time.Time
	ExecutionReason       string
	ObservedAt            *time.Time
	Observation           *PollObservationSummary
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
}

type FinalizePollRunInput struct {
	PollRunID         uuid.UUID
	LeaseFencingToken uuid.UUID
	Observation       PollObservationSummary
	ProviderResults   []PollProviderResult
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
	InstanceID       uuid.UUID
	Provider         string
	SnapshotComplete bool
}

type InventoryPollRepository struct {
	queries *generated.Queries
}

var _ inventorypoll.Repository = (*InventoryPollRepository)(nil)

func NewInventoryPollRepository(pool *pgxpool.Pool) (*InventoryPollRepository, error) {
	if pool == nil {
		return nil, errors.New("store: account inventory poll database is unavailable")
	}
	return &InventoryPollRepository{queries: generated.New(pool)}, nil
}

func (repository *InventoryPollRepository) ScheduleCurrent(
	ctx context.Context, request inventorypoll.ScheduleRequest,
) (inventorypoll.ScheduleResult, error) {
	stored, err := repository.scheduleCurrent(ctx, request.Period, request.PollStartGrace, request.MaxAttempts, request.Limit)
	if errors.Is(err, ErrInvalidPollRunInput) {
		return inventorypoll.ScheduleResult{}, inventorypoll.ErrInvalidRepositoryResult
	}
	if err != nil {
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
		ProviderResults: providers,
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
	mode := pgtype.Text{}
	if input.Observation.InventoryMode != "" {
		mode = pgtype.Text{String: string(input.Observation.InventoryMode), Valid: true}
	}
	observation := input.Observation
	row, err := repository.queries.FinalizeAccountInventoryPollRun(ctx,
		generated.FinalizeAccountInventoryPollRunParams{
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
			ProviderResults: encodedProviders,
		})
	if errors.Is(err, pgx.ErrNoRows) {
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
		}
		if !validPollProviderResult(result) {
			return nil, ErrPollRunInconsistent
		}
		results = append(results, result)
	}
	return results, nil
}

func (repository *InventoryPollRepository) Metrics(ctx context.Context) ([]PollRunMetric, []PollProviderMetric, error) {
	runs, err := repository.queries.ListAccountInventoryPollRunMetrics(ctx)
	if err != nil {
		return nil, nil, err
	}
	providers, err := repository.queries.ListAccountInventoryProviderMetrics(ctx)
	if err != nil {
		return nil, nil, err
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
			return nil, nil, ErrPollRunInconsistent
		}
		runMetrics = append(runMetrics, metric)
	}
	providerMetrics := make([]PollProviderMetric, 0, len(providers))
	for _, row := range providers {
		if uuidFromPG(row.InstanceID) == uuid.Nil || !validProviderName(row.Provider) {
			return nil, nil, ErrPollRunInconsistent
		}
		providerMetrics = append(providerMetrics, PollProviderMetric{
			InstanceID: uuidFromPG(row.InstanceID), Provider: row.Provider,
			SnapshotComplete: row.SnapshotComplete,
		})
	}
	return runMetrics, providerMetrics, nil
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
		ObservedAt: nullableTime(row.ObservedAt),
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
	switch result.Reason {
	case "complete":
		return result.IdentityComplete && result.SnapshotComplete && !result.Degraded
	case "transport_failed", "contract_invalid", "disk_fallback", "node_identity_incomplete", "identity_incomplete":
		return result.Degraded
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
