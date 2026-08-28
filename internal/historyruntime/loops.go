package historyruntime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	DefaultHistoryPlanLimit      = 100
	DefaultHistoryReconcileLimit = 100
)

var (
	ErrNoHistoryWork                  = errors.New("account inventory history has no work")
	ErrHistoryLeaseLost               = errors.New("account inventory history lease lost")
	ErrHistoryCommitUnknown           = errors.New("account inventory history commit result is unknown")
	ErrHistoryStatementTimeout        = errors.New("account inventory history statement timed out")
	ErrHistoryDatabaseUnavailable     = errors.New("account inventory history database unavailable")
	ErrHistoryStateInconsistent       = errors.New("account inventory history state is inconsistent")
	ErrHistorySourceDayMismatch       = errors.New("account inventory history source day mismatch")
	ErrHistorySourceCountMismatch     = errors.New("account inventory history source count mismatch")
	ErrHistorySourceChecksumMismatch  = errors.New("account inventory history source checksum mismatch")
	ErrHistoryActivationInconsistent  = errors.New("account inventory history activation is inconsistent")
	ErrHistorySegmentIncomplete       = errors.New("account inventory history rollup segment incomplete")
	ErrHistorySegmentCountMismatch    = errors.New("account inventory history rollup segment count mismatch")
	ErrHistorySegmentChecksumMismatch = errors.New("account inventory history rollup segment checksum mismatch")
	ErrHistoryRepositoryFailure       = errors.New("account inventory history repository failure")
)

type FailureReason string

const (
	FailureSourceDayMismatch       FailureReason = "source_day_mismatch"
	FailureSourceCountMismatch     FailureReason = "source_count_mismatch"
	FailureSourceChecksumMismatch  FailureReason = "source_checksum_mismatch"
	FailureActivationInconsistent  FailureReason = "activation_inconsistent"
	FailureSegmentIncomplete       FailureReason = "segment_incomplete"
	FailureSegmentCountMismatch    FailureReason = "segment_count_mismatch"
	FailureSegmentChecksumMismatch FailureReason = "segment_checksum_mismatch"
	FailureStatementTimeout        FailureReason = "statement_timeout"
	FailureLeaseExpired            FailureReason = "lease_expired"
	FailureDatabaseUnavailable     FailureReason = "database_unavailable"
	FailureInternal                FailureReason = "internal"
)

type PlanRequest struct{ Limit int }

type PlanResult struct {
	CompactionRunsCreated int
	RollupRunsCreated     int
}

type ClaimRequest struct {
	WorkerToken uuid.UUID
	Lease       time.Duration
}

type CompactionClaim struct {
	RunID                 uuid.UUID
	SummaryDate           time.Time
	InstanceID            uuid.UUID
	ProviderPolicyVersion uuid.UUID
	Status                CompactionState
	FailedFrom            FailedFrom
	LeaseExpiresAt        time.Time
	FencingToken          uuid.UUID
	Attempt               int
	SourceChecksum        []byte
	DeletedSnapshotCount  uint64
	SourceSnapshotCount   uint64
}

type LeaseRequest struct {
	RunID        uuid.UUID
	FencingToken uuid.UUID
	Lease        time.Duration
}

type ReconcileRequest struct{ Limit int }
type ReconcileResult struct{ Reconciled int }

type FencedRequest struct {
	RunID        uuid.UUID
	FencingToken uuid.UUID
}

type SummarizeResult struct {
	SourceChecksum      []byte
	SourceSnapshotCount uint64
}

type DeleteBatchRequest struct {
	RunID        uuid.UUID
	FencingToken uuid.UUID
	Limit        int
}

type DeleteBatchResult struct {
	DeletedRows      uint64
	RemainingRows    uint64
	TotalDeletedRows uint64
}

type CompleteRequest struct {
	RunID            uuid.UUID
	FencingToken     uuid.UUID
	ExpectedChecksum []byte
}

type CompleteResult struct{ Completed bool }

type FailRequest struct {
	RunID        uuid.UUID
	FencingToken uuid.UUID
	Reason       FailureReason
}

type RollupClaimRequest struct {
	WorkerToken uuid.UUID
	Lease       time.Duration
}

type RollupClaim struct {
	RunID          uuid.UUID
	SummaryDate    time.Time
	InstanceID     uuid.UUID
	LeaseExpiresAt time.Time
	FencingToken   uuid.UUID
	Attempt        int
}

type RollupLeaseRequest struct {
	RunID        uuid.UUID
	FencingToken uuid.UUID
	Lease        time.Duration
}

type RollupReconcileRequest struct{ Limit int }
type RollupReconcileResult struct{ Reconciled int }

type RollupFencedRequest struct {
	RunID        uuid.UUID
	FencingToken uuid.UUID
}

type RollupFinalizeResult struct {
	Completed             bool
	ExpectedSegmentCount  uint64
	CompletedSegmentCount uint64
	SegmentChecksum       []byte
}

type RollupFailRequest struct {
	RunID        uuid.UUID
	FencingToken uuid.UUID
	Reason       FailureReason
}

type RetentionRequest struct{ Limit int }

type RetentionResult struct {
	Processed   int
	DeletedRows uint64
}

// Repository is the only side-effect boundary used by the loops. Every
// method represents one bounded PostgreSQL transaction and must return only
// the fixed errors declared above; implementations must never perform network
// collection or include raw database errors in returned values.
type Repository interface {
	Plan(context.Context, PlanRequest) (PlanResult, error)
	ClaimCompaction(context.Context, ClaimRequest) (*CompactionClaim, error)
	RenewCompaction(context.Context, LeaseRequest) error
	ReconcileCompactions(context.Context, ReconcileRequest) (ReconcileResult, error)
	SummarizeCompaction(context.Context, FencedRequest) (SummarizeResult, error)
	DeleteSnapshotBatch(context.Context, DeleteBatchRequest) (DeleteBatchResult, error)
	CompleteCompaction(context.Context, CompleteRequest) (CompleteResult, error)
	FailCompaction(context.Context, FailRequest) error
	ClaimRollup(context.Context, RollupClaimRequest) (*RollupClaim, error)
	RenewRollup(context.Context, RollupLeaseRequest) error
	ReconcileRollups(context.Context, RollupReconcileRequest) (RollupReconcileResult, error)
	FinalizeRollup(context.Context, RollupFencedRequest) (RollupFinalizeResult, error)
	FailRollup(context.Context, RollupFailRequest) error
	DeletePollRetention(context.Context, RetentionRequest) (RetentionResult, error)
	DeleteRollupRowRetention(context.Context, RetentionRequest) (RetentionResult, error)
	DeleteRollupRunRetention(context.Context, RetentionRequest) (RetentionResult, error)
	DeleteCompactionRunRetention(context.Context, RetentionRequest) (RetentionResult, error)
}

type RepositoryLoops struct {
	config         ValidatedConfig
	repository     Repository
	wait           func(context.Context, time.Duration) error
	newWorkerToken func() uuid.UUID
	workSlots      chan struct{}
	fatalContext   context.Context
	stopFatal      context.CancelFunc
	fatalOnce      sync.Once
}

var _ Loops = (*RepositoryLoops)(nil)

func NewRepositoryLoops(configuration Config, repository Repository) (*RepositoryLoops, error) {
	validated, err := configuration.Validate()
	if err != nil || repository == nil {
		return nil, ErrInvalidConfig
	}
	fatalContext, stopFatal := context.WithCancel(context.Background())
	return &RepositoryLoops{
		config: validated, repository: repository,
		wait: waitForHistoryInterval, newWorkerToken: uuid.New,
		workSlots:    make(chan struct{}, validated.concurrency),
		fatalContext: fatalContext, stopFatal: stopFatal,
	}, nil
}

func (loops *RepositoryLoops) RunPlanner(ctx context.Context) error {
	if !loops.valid() || ctx == nil {
		return ErrInvalidConfig
	}
	backoff := newHistoryBackoff(loops.config.databaseBackoffInitial, loops.config.databaseBackoffMaximum)
	for ctx.Err() == nil {
		if loops.runtimeStopped() {
			return ErrRuntimeStopped
		}
		operationContext, cancel := context.WithTimeout(ctx, loops.config.statementTimeout)
		result, err := loops.repository.Plan(operationContext, PlanRequest{Limit: DefaultHistoryPlanLimit})
		cancel()
		if err == nil && validPlanResult(result, DefaultHistoryPlanLimit) {
			if loops.runtimeStopped() {
				return ErrRuntimeStopped
			}
			backoff.Reset()
			if loops.waitForNext(ctx, loops.config.scanInterval) != nil {
				if loops.runtimeStopped() {
					return ErrRuntimeStopped
				}
				return nil
			}
			continue
		}
		if err == nil {
			err = ErrHistoryStateInconsistent
		}
		if loops.triggerFatalIfNeeded(err) {
			return ErrRuntimeStopped
		}
		if ctx.Err() != nil {
			return nil
		}
		if loops.waitForNext(ctx, backoff.Next()) != nil {
			if loops.runtimeStopped() {
				return ErrRuntimeStopped
			}
			return nil
		}
	}
	return nil
}

func (loops *RepositoryLoops) RunWorker(claimContext, operationContext context.Context) error {
	if !loops.valid() || claimContext == nil || operationContext == nil {
		return ErrInvalidConfig
	}
	workerErrors := make(chan error, loops.config.concurrency)
	var workers sync.WaitGroup
	workers.Add(loops.config.concurrency)
	for range loops.config.concurrency {
		workerToken := loops.newWorkerToken()
		go func() {
			defer workers.Done()
			workerErrors <- loops.runCompactionWorker(claimContext, operationContext, workerToken)
		}()
	}
	workers.Wait()
	close(workerErrors)
	for err := range workerErrors {
		if errors.Is(err, ErrRuntimeStopped) {
			return ErrRuntimeStopped
		}
	}
	return nil
}

func (loops *RepositoryLoops) RunRollupWorker(claimContext, operationContext context.Context) error {
	if !loops.valid() || claimContext == nil || operationContext == nil {
		return ErrInvalidConfig
	}
	workerErrors := make(chan error, loops.config.concurrency)
	var workers sync.WaitGroup
	workers.Add(loops.config.concurrency)
	for range loops.config.concurrency {
		workerToken := loops.newWorkerToken()
		go func() {
			defer workers.Done()
			workerErrors <- loops.runRollupWorker(claimContext, operationContext, workerToken)
		}()
	}
	workers.Wait()
	close(workerErrors)
	for err := range workerErrors {
		if errors.Is(err, ErrRuntimeStopped) {
			return ErrRuntimeStopped
		}
	}
	return nil
}

// RunRetentionWorker executes the four retention transactions in dependency
// order. It deliberately uses one ordered scanner: capacity is bounded by the
// shared workSlots semaphore, while parallel scanners could invert the delete
// order and create avoidable dependency races.
func (loops *RepositoryLoops) RunRetentionWorker(claimContext, operationContext context.Context) error {
	if !loops.valid() || claimContext == nil || operationContext == nil {
		return ErrInvalidConfig
	}
	backoff := newHistoryBackoff(loops.config.databaseBackoffInitial, loops.config.databaseBackoffMaximum)
	for claimContext.Err() == nil && !loops.runtimeStopped() {
		progressed, err := loops.runRetentionRound(claimContext, operationContext)
		if operationContext.Err() != nil {
			return nil
		}
		if loops.triggerFatalIfNeeded(err) || loops.runtimeStopped() {
			return ErrRuntimeStopped
		}
		if claimContext.Err() != nil {
			return nil
		}

		wait := time.Duration(0)
		if err == nil {
			backoff.Reset()
			if !progressed {
				wait = loops.config.scanInterval
			}
		} else {
			switch fixedHistoryError(err) {
			case ErrHistoryStatementTimeout, ErrHistoryDatabaseUnavailable:
				wait = backoff.Next()
			case ErrHistoryCommitUnknown:
				backoff.Reset()
				wait = loops.config.scanInterval
			default:
				// All non-transient errors are fatal and returned above. Keep this
				// defensive branch bounded if a new fixed error is introduced.
				wait = backoff.Next()
			}
		}
		if wait == 0 {
			continue
		}
		if loops.waitForNext(claimContext, wait) != nil {
			if loops.runtimeStopped() {
				return ErrRuntimeStopped
			}
			return nil
		}
	}
	if loops.runtimeStopped() {
		return ErrRuntimeStopped
	}
	return nil
}

type retentionOperation func(context.Context, RetentionRequest) (RetentionResult, error)

func (loops *RepositoryLoops) runRetentionRound(
	claimContext, operationContext context.Context,
) (bool, error) {
	operations := [...]retentionOperation{
		loops.repository.DeletePollRetention,
		loops.repository.DeleteRollupRowRetention,
		loops.repository.DeleteRollupRunRetention,
		loops.repository.DeleteCompactionRunRetention,
	}
	request := RetentionRequest{Limit: loops.config.deleteBatchSize}
	progressed := false
	for _, operation := range operations {
		if err := loops.claimGateError(claimContext); err != nil {
			return progressed, err
		}
		if err := loops.acquireWorkSlot(claimContext); err != nil {
			return progressed, err
		}
		// Cancellation can race with a ready semaphore send. Re-check after
		// acquisition so shutdown never opens the next retention transaction.
		if err := loops.claimGateError(claimContext); err != nil {
			loops.releaseWorkSlot()
			return progressed, err
		}
		transactionContext, cancel := context.WithTimeout(operationContext, loops.config.statementTimeout)
		result, err := operation(transactionContext, request)
		cancel()
		loops.releaseWorkSlot()
		if err != nil {
			fixed := fixedHistoryError(err)
			// Retention has no claim and reports no work as a successful 0/0
			// result. Claim-only errors therefore indicate an adapter contract
			// violation rather than an idle scanner.
			if errors.Is(fixed, ErrNoHistoryWork) || errors.Is(fixed, ErrHistoryLeaseLost) {
				fixed = ErrHistoryStateInconsistent
			}
			return progressed, fixed
		}
		if !validRetentionResult(result, request.Limit) {
			loops.triggerFatal()
			return progressed, ErrHistoryStateInconsistent
		}
		progressed = progressed || result.Processed > 0
	}
	return progressed, nil
}

func (loops *RepositoryLoops) RunReconciler(ctx context.Context) error {
	if !loops.valid() || ctx == nil {
		return ErrInvalidConfig
	}
	backoff := newHistoryBackoff(loops.config.databaseBackoffInitial, loops.config.databaseBackoffMaximum)
	for ctx.Err() == nil {
		if loops.runtimeStopped() {
			return ErrRuntimeStopped
		}
		operationContext, cancel := context.WithTimeout(ctx, loops.config.statementTimeout)
		result, err := loops.repository.ReconcileCompactions(
			operationContext, ReconcileRequest{Limit: DefaultHistoryReconcileLimit},
		)
		cancel()
		if err == nil && result.Reconciled >= 0 && result.Reconciled <= DefaultHistoryReconcileLimit {
			if loops.runtimeStopped() {
				return ErrRuntimeStopped
			}
			backoff.Reset()
			if loops.waitForNext(ctx, loops.config.scanInterval) != nil {
				if loops.runtimeStopped() {
					return ErrRuntimeStopped
				}
				return nil
			}
			continue
		}
		if err == nil {
			err = ErrHistoryStateInconsistent
		}
		if loops.triggerFatalIfNeeded(err) {
			return ErrRuntimeStopped
		}
		if ctx.Err() != nil {
			return nil
		}
		if loops.waitForNext(ctx, backoff.Next()) != nil {
			if loops.runtimeStopped() {
				return ErrRuntimeStopped
			}
			return nil
		}
	}
	return nil
}

func (loops *RepositoryLoops) RunRollupReconciler(ctx context.Context) error {
	if !loops.valid() || ctx == nil {
		return ErrInvalidConfig
	}
	backoff := newHistoryBackoff(loops.config.databaseBackoffInitial, loops.config.databaseBackoffMaximum)
	for ctx.Err() == nil {
		if loops.runtimeStopped() {
			return ErrRuntimeStopped
		}
		operationContext, cancel := context.WithTimeout(ctx, loops.config.statementTimeout)
		result, err := loops.repository.ReconcileRollups(
			operationContext, RollupReconcileRequest{Limit: DefaultHistoryReconcileLimit},
		)
		cancel()
		if err == nil && result.Reconciled >= 0 && result.Reconciled <= DefaultHistoryReconcileLimit {
			if loops.runtimeStopped() {
				return ErrRuntimeStopped
			}
			backoff.Reset()
			if loops.waitForNext(ctx, loops.config.scanInterval) != nil {
				if loops.runtimeStopped() {
					return ErrRuntimeStopped
				}
				return nil
			}
			continue
		}
		if err == nil {
			err = ErrHistoryStateInconsistent
		}
		if loops.triggerFatalIfNeeded(err) {
			return ErrRuntimeStopped
		}
		if ctx.Err() != nil {
			return nil
		}
		if loops.waitForNext(ctx, backoff.Next()) != nil {
			if loops.runtimeStopped() {
				return ErrRuntimeStopped
			}
			return nil
		}
	}
	return nil
}

func (loops *RepositoryLoops) runCompactionWorker(
	claimContext, operationContext context.Context, workerToken uuid.UUID,
) error {
	backoff := newHistoryBackoff(loops.config.databaseBackoffInitial, loops.config.databaseBackoffMaximum)
	for claimContext.Err() == nil && !loops.runtimeStopped() {
		if err := loops.acquireWorkSlot(claimContext); err != nil {
			if loops.runtimeStopped() {
				return ErrRuntimeStopped
			}
			return nil
		}
		requestContext, cancel := context.WithTimeout(claimContext, loops.config.statementTimeout)
		claim, err := loops.repository.ClaimCompaction(requestContext, ClaimRequest{
			WorkerToken: workerToken, Lease: loops.config.claimLease,
		})
		cancel()
		if err != nil {
			if claimContext.Err() != nil {
				loops.releaseWorkSlot()
				return nil
			}
			fixed := fixedHistoryError(err)
			fatal := loops.triggerFatalIfNeeded(fixed)
			loops.releaseWorkSlot()
			if fatal {
				return ErrRuntimeStopped
			}
			if errors.Is(fixed, ErrNoHistoryWork) {
				backoff.Reset()
				if loops.waitForNext(claimContext, loops.config.scanInterval) != nil {
					if loops.runtimeStopped() {
						return ErrRuntimeStopped
					}
					return nil
				}
				continue
			}
			if loops.waitForNext(claimContext, backoff.Next()) != nil {
				if loops.runtimeStopped() {
					return ErrRuntimeStopped
				}
				return nil
			}
			continue
		}
		if !validCompactionClaim(claim) {
			loops.triggerFatal()
			loops.releaseWorkSlot()
			return ErrRuntimeStopped
		}
		if loops.runtimeStopped() {
			loops.releaseWorkSlot()
			return ErrRuntimeStopped
		}
		backoff.Reset()
		err = loops.processCompaction(claimContext, operationContext, *claim)
		loops.releaseWorkSlot()
		if operationContext.Err() != nil {
			return nil
		}
		if loops.triggerFatalIfNeeded(err) || loops.runtimeStopped() {
			return ErrRuntimeStopped
		}
		if err != nil && loops.waitForNext(claimContext, backoff.Next()) != nil {
			if loops.runtimeStopped() {
				return ErrRuntimeStopped
			}
			return nil
		}
	}
	if loops.runtimeStopped() {
		return ErrRuntimeStopped
	}
	return nil
}

func (loops *RepositoryLoops) runRollupWorker(
	claimContext, operationContext context.Context, workerToken uuid.UUID,
) error {
	backoff := newHistoryBackoff(loops.config.databaseBackoffInitial, loops.config.databaseBackoffMaximum)
	for claimContext.Err() == nil && !loops.runtimeStopped() {
		if err := loops.acquireWorkSlot(claimContext); err != nil {
			if loops.runtimeStopped() {
				return ErrRuntimeStopped
			}
			return nil
		}
		requestContext, cancel := context.WithTimeout(claimContext, loops.config.statementTimeout)
		claim, err := loops.repository.ClaimRollup(requestContext, RollupClaimRequest{
			WorkerToken: workerToken, Lease: loops.config.claimLease,
		})
		cancel()
		if err != nil {
			if claimContext.Err() != nil {
				loops.releaseWorkSlot()
				return nil
			}
			fixed := fixedHistoryError(err)
			fatal := loops.triggerFatalIfNeeded(fixed)
			loops.releaseWorkSlot()
			if fatal {
				return ErrRuntimeStopped
			}
			if errors.Is(fixed, ErrNoHistoryWork) {
				backoff.Reset()
				if loops.waitForNext(claimContext, loops.config.scanInterval) != nil {
					if loops.runtimeStopped() {
						return ErrRuntimeStopped
					}
					return nil
				}
				continue
			}
			if loops.waitForNext(claimContext, backoff.Next()) != nil {
				if loops.runtimeStopped() {
					return ErrRuntimeStopped
				}
				return nil
			}
			continue
		}
		if !validRollupClaim(claim) {
			loops.triggerFatal()
			loops.releaseWorkSlot()
			return ErrRuntimeStopped
		}
		if loops.runtimeStopped() {
			loops.releaseWorkSlot()
			return ErrRuntimeStopped
		}
		backoff.Reset()
		err = loops.processRollup(claimContext, operationContext, *claim)
		loops.releaseWorkSlot()
		if operationContext.Err() != nil {
			return nil
		}
		if loops.triggerFatalIfNeeded(err) || loops.runtimeStopped() {
			return ErrRuntimeStopped
		}
		if err != nil && loops.waitForNext(claimContext, backoff.Next()) != nil {
			if loops.runtimeStopped() {
				return ErrRuntimeStopped
			}
			return nil
		}
	}
	if loops.runtimeStopped() {
		return ErrRuntimeStopped
	}
	return nil
}

func (loops *RepositoryLoops) processRollup(
	claimContext, operationContext context.Context, claim RollupClaim,
) error {
	if !validRollupClaim(&claim) {
		loops.triggerFatal()
		return ErrHistoryStateInconsistent
	}
	if err := loops.claimGateError(claimContext); err != nil {
		return err
	}
	if err := loops.renewRollup(operationContext, claim); err != nil {
		return err
	}
	if err := loops.claimGateError(claimContext); err != nil {
		return err
	}
	request := RollupFencedRequest{RunID: claim.RunID, FencingToken: claim.FencingToken}
	result, operationErr := loops.finalizeRollup(claimContext, operationContext, request)
	if operationErr != nil {
		return loops.handleRollupOperationError(operationContext, claim, operationErr)
	}
	if !validRollupFinalizeResult(result) {
		loops.triggerFatal()
		return ErrHistoryStateInconsistent
	}
	return nil
}

func (loops *RepositoryLoops) processCompaction(
	claimContext, operationContext context.Context, claim CompactionClaim,
) error {
	phase, err := resumeCompactionPhase(claim)
	if err != nil {
		loops.triggerFatal()
		loops.failCompaction(operationContext, claim, FailureInternal)
		return ErrHistoryStateInconsistent
	}
	checksum := append([]byte(nil), claim.SourceChecksum...)
	if phase == CompactionPending {
		if err := loops.claimGateError(claimContext); err != nil {
			return err
		}
		if err := loops.renewCompaction(operationContext, claim); err != nil {
			return err
		}
		if err := loops.claimGateError(claimContext); err != nil {
			return err
		}
		transactionContext, cancel := context.WithTimeout(operationContext, loops.config.statementTimeout)
		result, operationErr := loops.repository.SummarizeCompaction(transactionContext, fencedRequest(claim))
		cancel()
		if operationErr != nil {
			return loops.handleOperationError(operationContext, claim, operationErr)
		}
		if len(result.SourceChecksum) != 32 {
			loops.triggerFatal()
			loops.failCompaction(operationContext, claim, FailureInternal)
			return ErrHistoryStateInconsistent
		}
		checksum = append([]byte(nil), result.SourceChecksum...)
		claim.SourceSnapshotCount = result.SourceSnapshotCount
		phase = CompactionSummarized
	}
	if (phase == CompactionSummarized || phase == CompactionDeleting) && len(checksum) != 32 {
		loops.triggerFatal()
		loops.failCompaction(operationContext, claim, FailureInternal)
		return ErrHistoryStateInconsistent
	}

	previousTotal := claim.DeletedSnapshotCount
	if previousTotal > claim.SourceSnapshotCount {
		loops.triggerFatal()
		loops.failCompaction(operationContext, claim, FailureInternal)
		return ErrHistoryStateInconsistent
	}
	previousRemaining := claim.SourceSnapshotCount - previousTotal
	for phase == CompactionSummarized || phase == CompactionDeleting {
		if err := loops.claimGateError(claimContext); err != nil {
			return err
		}
		if err := loops.renewCompaction(operationContext, claim); err != nil {
			return err
		}
		if err := loops.claimGateError(claimContext); err != nil {
			return err
		}
		transactionContext, cancel := context.WithTimeout(operationContext, loops.config.statementTimeout)
		result, operationErr := loops.repository.DeleteSnapshotBatch(transactionContext, DeleteBatchRequest{
			RunID: claim.RunID, FencingToken: claim.FencingToken, Limit: loops.config.deleteBatchSize,
		})
		cancel()
		if operationErr != nil {
			return loops.handleOperationError(operationContext, claim, operationErr)
		}
		if !validDeleteBatchResult(
			result, loops.config.deleteBatchSize, claim.SourceSnapshotCount, previousTotal, previousRemaining,
		) {
			loops.triggerFatal()
			loops.failCompaction(operationContext, claim, FailureInternal)
			return ErrHistoryStateInconsistent
		}
		previousTotal = result.TotalDeletedRows
		previousRemaining = result.RemainingRows
		phase = CompactionDeleting
		if result.RemainingRows == 0 {
			break
		}
	}

	if err := loops.claimGateError(claimContext); err != nil {
		return err
	}
	if err := loops.renewCompaction(operationContext, claim); err != nil {
		return err
	}
	if err := loops.claimGateError(claimContext); err != nil {
		return err
	}
	request := CompleteRequest{
		RunID: claim.RunID, FencingToken: claim.FencingToken,
		ExpectedChecksum: append([]byte(nil), checksum...),
	}
	result, operationErr := loops.completeCompaction(claimContext, operationContext, request)
	if operationErr != nil {
		return loops.handleOperationError(operationContext, claim, operationErr)
	}
	if !result.Completed {
		loops.triggerFatal()
		loops.failCompaction(operationContext, claim, FailureInternal)
		return ErrHistoryStateInconsistent
	}
	return nil
}

func (loops *RepositoryLoops) claimGateError(ctx context.Context) error {
	if ctx == nil {
		return context.Canceled
	}
	if loops.runtimeStopped() {
		return ErrRuntimeStopped
	}
	select {
	case <-ctx.Done():
		return context.Canceled
	default:
		return nil
	}
}

// Complete is the one transition retried immediately after an unknown commit:
// a committed run is no longer claimable, while the database contract makes
// completed+same-checksum idempotent even after its lease was cleared.
func (loops *RepositoryLoops) completeCompaction(
	claimContext, operationContext context.Context, request CompleteRequest,
) (CompleteResult, error) {
	for attempt := 0; attempt < 2; attempt++ {
		if err := loops.claimGateError(claimContext); err != nil {
			return CompleteResult{}, err
		}
		transactionContext, cancel := context.WithTimeout(operationContext, loops.config.statementTimeout)
		result, err := loops.repository.CompleteCompaction(transactionContext, request)
		cancel()
		if err == nil || !errors.Is(fixedHistoryError(err), ErrHistoryCommitUnknown) {
			return result, err
		}
	}
	return CompleteResult{}, ErrHistoryCommitUnknown
}

// Finalize is one bounded transaction. A completed rollup is immutable and
// replayable only with its original fence, so an unknown commit is retried once
// with the exact same request.
func (loops *RepositoryLoops) finalizeRollup(
	claimContext, operationContext context.Context, request RollupFencedRequest,
) (RollupFinalizeResult, error) {
	for attempt := 0; attempt < 2; attempt++ {
		if err := loops.claimGateError(claimContext); err != nil {
			return RollupFinalizeResult{}, err
		}
		transactionContext, cancel := context.WithTimeout(operationContext, loops.config.statementTimeout)
		result, err := loops.repository.FinalizeRollup(transactionContext, request)
		cancel()
		if err == nil || !errors.Is(fixedHistoryError(err), ErrHistoryCommitUnknown) {
			return result, err
		}
	}
	return RollupFinalizeResult{}, ErrHistoryCommitUnknown
}

func (loops *RepositoryLoops) renewCompaction(ctx context.Context, claim CompactionClaim) error {
	operationContext, cancel := context.WithTimeout(ctx, loops.config.statementTimeout)
	err := loops.repository.RenewCompaction(operationContext, LeaseRequest{
		RunID: claim.RunID, FencingToken: claim.FencingToken, Lease: loops.config.claimLease,
	})
	cancel()
	if err != nil {
		return loops.handleOperationError(ctx, claim, err)
	}
	return nil
}

func (loops *RepositoryLoops) renewRollup(ctx context.Context, claim RollupClaim) error {
	operationContext, cancel := context.WithTimeout(ctx, loops.config.statementTimeout)
	err := loops.repository.RenewRollup(operationContext, RollupLeaseRequest{
		RunID: claim.RunID, FencingToken: claim.FencingToken, Lease: loops.config.claimLease,
	})
	cancel()
	if err != nil {
		return loops.handleRollupOperationError(ctx, claim, err)
	}
	return nil
}

func (loops *RepositoryLoops) handleOperationError(
	ctx context.Context, claim CompactionClaim, operationErr error,
) error {
	fixed := fixedHistoryError(operationErr)
	loops.triggerFatalIfNeeded(fixed)
	if reason, ok := failureReasonForError(fixed); ok {
		loops.failCompaction(ctx, claim, reason)
	}
	return fixed
}

func (loops *RepositoryLoops) handleRollupOperationError(
	ctx context.Context, claim RollupClaim, operationErr error,
) error {
	fixed := fixedHistoryError(operationErr)
	if loops.triggerFatalIfNeeded(fixed) {
		return fixed
	}
	if reason, ok := failureReasonForError(fixed); ok {
		loops.failRollup(ctx, claim, reason)
	}
	return fixed
}

func (loops *RepositoryLoops) triggerFatalIfNeeded(err error) bool {
	if err == nil {
		return false
	}
	fixed := fixedHistoryError(err)
	if !isFatalHistoryError(fixed) {
		return false
	}
	loops.triggerFatal()
	return true
}

func isFatalHistoryError(err error) bool {
	for _, fatal := range []error{
		ErrHistoryStateInconsistent,
		ErrHistorySourceDayMismatch,
		ErrHistorySourceCountMismatch,
		ErrHistorySourceChecksumMismatch,
		ErrHistorySegmentCountMismatch,
		ErrHistorySegmentChecksumMismatch,
		ErrHistoryActivationInconsistent,
		ErrHistoryRepositoryFailure,
	} {
		if errors.Is(err, fatal) {
			return true
		}
	}
	return false
}

func (loops *RepositoryLoops) triggerFatal() {
	if loops == nil || loops.stopFatal == nil {
		return
	}
	loops.fatalOnce.Do(loops.stopFatal)
}

func (loops *RepositoryLoops) runtimeStopped() bool {
	if loops == nil || loops.fatalContext == nil {
		return true
	}
	return loops.fatalContext.Err() != nil
}

func (loops *RepositoryLoops) waitForNext(ctx context.Context, duration time.Duration) error {
	if loops.runtimeStopped() {
		return ErrRuntimeStopped
	}
	waitContext, cancel := context.WithCancel(ctx)
	stopFatalWait := context.AfterFunc(loops.fatalContext, cancel)
	err := loops.wait(waitContext, duration)
	stopFatalWait()
	cancel()
	if loops.runtimeStopped() {
		return ErrRuntimeStopped
	}
	return err
}

func (loops *RepositoryLoops) acquireWorkSlot(ctx context.Context) error {
	if ctx == nil {
		return context.Canceled
	}
	select {
	case loops.workSlots <- struct{}{}:
		if loops.runtimeStopped() {
			loops.releaseWorkSlot()
			return ErrRuntimeStopped
		}
		if ctx.Err() != nil {
			loops.releaseWorkSlot()
			return context.Canceled
		}
		return nil
	case <-loops.fatalContext.Done():
		return ErrRuntimeStopped
	case <-ctx.Done():
		return context.Canceled
	}
}

func (loops *RepositoryLoops) releaseWorkSlot() { <-loops.workSlots }

func (loops *RepositoryLoops) failCompaction(ctx context.Context, claim CompactionClaim, reason FailureReason) {
	if ctx.Err() != nil || !reason.Valid() {
		return
	}
	operationContext, cancel := context.WithTimeout(ctx, loops.config.statementTimeout)
	_ = loops.repository.FailCompaction(operationContext, FailRequest{
		RunID: claim.RunID, FencingToken: claim.FencingToken, Reason: reason,
	})
	cancel()
}

func (loops *RepositoryLoops) failRollup(ctx context.Context, claim RollupClaim, reason FailureReason) {
	if ctx.Err() != nil || !reason.Valid() {
		return
	}
	operationContext, cancel := context.WithTimeout(ctx, loops.config.statementTimeout)
	_ = loops.repository.FailRollup(operationContext, RollupFailRequest{
		RunID: claim.RunID, FencingToken: claim.FencingToken, Reason: reason,
	})
	cancel()
}

func (loops *RepositoryLoops) valid() bool {
	return loops != nil && loops.repository != nil && loops.wait != nil && loops.newWorkerToken != nil &&
		loops.workSlots != nil && cap(loops.workSlots) == loops.config.concurrency &&
		loops.fatalContext != nil && loops.stopFatal != nil
}

func validPlanResult(result PlanResult, limit int) bool {
	return result.CompactionRunsCreated >= 0 && result.CompactionRunsCreated <= limit &&
		result.RollupRunsCreated >= 0 && result.RollupRunsCreated <= limit
}

func validRetentionResult(result RetentionResult, limit int) bool {
	return limit >= 1 && limit <= MaximumDeleteBatchSize &&
		result.Processed >= 0 && result.Processed <= limit &&
		result.DeletedRows >= uint64(result.Processed) &&
		(result.Processed == 0) == (result.DeletedRows == 0)
}

func validCompactionClaim(claim *CompactionClaim) bool {
	if claim == nil || claim.RunID == uuid.Nil || claim.InstanceID == uuid.Nil ||
		claim.ProviderPolicyVersion == uuid.Nil || claim.FencingToken == uuid.Nil ||
		claim.SummaryDate.IsZero() || claim.LeaseExpiresAt.IsZero() || claim.Attempt < 1 ||
		claim.DeletedSnapshotCount > claim.SourceSnapshotCount {
		return false
	}
	phase, err := resumeCompactionPhase(*claim)
	if err != nil {
		return false
	}
	if phase == CompactionPending {
		return len(claim.SourceChecksum) == 0 && claim.SourceSnapshotCount == 0 && claim.DeletedSnapshotCount == 0
	}
	return len(claim.SourceChecksum) == 32
}

func validRollupClaim(claim *RollupClaim) bool {
	return claim != nil && claim.RunID != uuid.Nil && claim.InstanceID != uuid.Nil &&
		!claim.SummaryDate.IsZero() && !claim.LeaseExpiresAt.IsZero() &&
		claim.FencingToken != uuid.Nil && claim.Attempt > 0
}

func validRollupFinalizeResult(result RollupFinalizeResult) bool {
	return result.Completed && result.ExpectedSegmentCount > 0 &&
		result.CompletedSegmentCount == result.ExpectedSegmentCount && len(result.SegmentChecksum) == 32
}

func resumeCompactionPhase(claim CompactionClaim) (CompactionState, error) {
	switch claim.Status {
	case CompactionPending, CompactionSummarized, CompactionDeleting:
		if claim.FailedFrom != "" {
			return "", ErrHistoryStateInconsistent
		}
		return claim.Status, nil
	case CompactionFailed:
		switch claim.FailedFrom {
		case FailedFromPending:
			return CompactionPending, nil
		case FailedFromSummarized:
			return CompactionSummarized, nil
		case FailedFromDeleting:
			return CompactionDeleting, nil
		default:
			return "", ErrHistoryStateInconsistent
		}
	default:
		return "", ErrHistoryStateInconsistent
	}
}

func validDeleteBatchResult(
	result DeleteBatchResult, limit int, sourceRows, previousTotal, previousRemaining uint64,
) bool {
	if result.DeletedRows > uint64(limit) || result.TotalDeletedRows < previousTotal ||
		result.TotalDeletedRows-previousTotal != result.DeletedRows ||
		result.TotalDeletedRows > sourceRows || result.RemainingRows != sourceRows-result.TotalDeletedRows {
		return false
	}
	if previousRemaining == 0 {
		return result.DeletedRows == 0 && result.RemainingRows == 0
	}
	return result.DeletedRows > 0 && result.RemainingRows < previousRemaining
}

func fencedRequest(claim CompactionClaim) FencedRequest {
	return FencedRequest{RunID: claim.RunID, FencingToken: claim.FencingToken}
}

func fixedHistoryError(err error) error {
	if err == nil {
		return ErrHistoryRepositoryFailure
	}
	for _, fixed := range []error{
		ErrRuntimeStopped, ErrNoHistoryWork, ErrHistoryLeaseLost, ErrHistoryCommitUnknown,
		ErrHistoryStatementTimeout, ErrHistoryDatabaseUnavailable,
		ErrHistoryStateInconsistent, ErrHistorySourceDayMismatch,
		ErrHistorySourceCountMismatch, ErrHistorySourceChecksumMismatch,
		ErrHistorySegmentIncomplete, ErrHistorySegmentCountMismatch,
		ErrHistorySegmentChecksumMismatch,
		ErrHistoryActivationInconsistent,
	} {
		if errors.Is(err, fixed) {
			return fixed
		}
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrHistoryStatementTimeout
	}
	return ErrHistoryRepositoryFailure
}

func failureReasonForError(err error) (FailureReason, bool) {
	switch {
	case errors.Is(err, ErrHistorySourceDayMismatch):
		return FailureSourceDayMismatch, true
	case errors.Is(err, ErrHistorySourceCountMismatch):
		return FailureSourceCountMismatch, true
	case errors.Is(err, ErrHistorySourceChecksumMismatch):
		return FailureSourceChecksumMismatch, true
	case errors.Is(err, ErrHistorySegmentIncomplete):
		return FailureSegmentIncomplete, true
	case errors.Is(err, ErrHistorySegmentCountMismatch):
		return FailureSegmentCountMismatch, true
	case errors.Is(err, ErrHistorySegmentChecksumMismatch):
		return FailureSegmentChecksumMismatch, true
	case errors.Is(err, ErrHistoryActivationInconsistent):
		return FailureActivationInconsistent, true
	case errors.Is(err, ErrHistoryStatementTimeout):
		return FailureStatementTimeout, true
	case errors.Is(err, ErrHistoryDatabaseUnavailable):
		return FailureDatabaseUnavailable, true
	case errors.Is(err, ErrHistoryStateInconsistent), errors.Is(err, ErrHistoryRepositoryFailure):
		return FailureInternal, true
	default:
		return "", false
	}
}

func (reason FailureReason) Valid() bool {
	switch reason {
	case FailureSourceDayMismatch, FailureSourceCountMismatch, FailureSourceChecksumMismatch,
		FailureSegmentIncomplete, FailureSegmentCountMismatch, FailureSegmentChecksumMismatch,
		FailureActivationInconsistent, FailureStatementTimeout, FailureLeaseExpired,
		FailureDatabaseUnavailable, FailureInternal:
		return true
	default:
		return false
	}
}

type historyBackoff struct {
	initial time.Duration
	maximum time.Duration
	next    time.Duration
}

func newHistoryBackoff(initial, maximum time.Duration) *historyBackoff {
	return &historyBackoff{initial: initial, maximum: maximum, next: initial}
}

func (backoff *historyBackoff) Next() time.Duration {
	result := backoff.next
	if backoff.next < backoff.maximum {
		backoff.next *= 2
		if backoff.next > backoff.maximum {
			backoff.next = backoff.maximum
		}
	}
	return result
}

func (backoff *historyBackoff) Reset() { backoff.next = backoff.initial }

func waitForHistoryInterval(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (PlanRequest) Format(state fmt.State, _ rune)  { writeHistoryRedacted(state, "PlanRequest") }
func (PlanResult) Format(state fmt.State, _ rune)   { writeHistoryRedacted(state, "PlanResult") }
func (ClaimRequest) Format(state fmt.State, _ rune) { writeHistoryRedacted(state, "ClaimRequest") }
func (CompactionClaim) Format(state fmt.State, _ rune) {
	writeHistoryRedacted(state, "CompactionClaim")
}
func (LeaseRequest) Format(state fmt.State, _ rune) { writeHistoryRedacted(state, "LeaseRequest") }
func (ReconcileRequest) Format(state fmt.State, _ rune) {
	writeHistoryRedacted(state, "ReconcileRequest")
}
func (ReconcileResult) Format(state fmt.State, _ rune) {
	writeHistoryRedacted(state, "ReconcileResult")
}
func (FencedRequest) Format(state fmt.State, _ rune) { writeHistoryRedacted(state, "FencedRequest") }
func (SummarizeResult) Format(state fmt.State, _ rune) {
	writeHistoryRedacted(state, "SummarizeResult")
}
func (DeleteBatchRequest) Format(state fmt.State, _ rune) {
	writeHistoryRedacted(state, "DeleteBatchRequest")
}
func (DeleteBatchResult) Format(state fmt.State, _ rune) {
	writeHistoryRedacted(state, "DeleteBatchResult")
}
func (CompleteRequest) Format(state fmt.State, _ rune) {
	writeHistoryRedacted(state, "CompleteRequest")
}
func (CompleteResult) Format(state fmt.State, _ rune) { writeHistoryRedacted(state, "CompleteResult") }
func (FailRequest) Format(state fmt.State, _ rune)    { writeHistoryRedacted(state, "FailRequest") }
func (RollupClaimRequest) Format(state fmt.State, _ rune) {
	writeHistoryRedacted(state, "RollupClaimRequest")
}
func (RollupClaim) Format(state fmt.State, _ rune) { writeHistoryRedacted(state, "RollupClaim") }
func (RollupLeaseRequest) Format(state fmt.State, _ rune) {
	writeHistoryRedacted(state, "RollupLeaseRequest")
}
func (RollupReconcileRequest) Format(state fmt.State, _ rune) {
	writeHistoryRedacted(state, "RollupReconcileRequest")
}
func (RollupReconcileResult) Format(state fmt.State, _ rune) {
	writeHistoryRedacted(state, "RollupReconcileResult")
}
func (RollupFencedRequest) Format(state fmt.State, _ rune) {
	writeHistoryRedacted(state, "RollupFencedRequest")
}
func (RollupFinalizeResult) Format(state fmt.State, _ rune) {
	writeHistoryRedacted(state, "RollupFinalizeResult")
}
func (RollupFailRequest) Format(state fmt.State, _ rune) {
	writeHistoryRedacted(state, "RollupFailRequest")
}
func (RetentionRequest) Format(state fmt.State, _ rune) {
	writeHistoryRedacted(state, "RetentionRequest")
}
func (RetentionResult) Format(state fmt.State, _ rune) {
	writeHistoryRedacted(state, "RetentionResult")
}

func writeHistoryRedacted(state fmt.State, name string) {
	_, _ = state.Write([]byte("[REDACTED " + name + "]"))
}
