package historyruntime

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeHistoryRepository struct {
	mu sync.Mutex

	events                       []string
	plan                         func(context.Context, PlanRequest) (PlanResult, error)
	claim                        func(context.Context, ClaimRequest) (*CompactionClaim, error)
	renew                        func(context.Context, LeaseRequest) error
	reconcile                    func(context.Context, ReconcileRequest) (ReconcileResult, error)
	summarize                    func(context.Context, FencedRequest) (SummarizeResult, error)
	deleteBatch                  func(context.Context, DeleteBatchRequest) (DeleteBatchResult, error)
	complete                     func(context.Context, CompleteRequest) (CompleteResult, error)
	fail                         func(context.Context, FailRequest) error
	claimRollup                  func(context.Context, RollupClaimRequest) (*RollupClaim, error)
	renewRollup                  func(context.Context, RollupLeaseRequest) error
	reconcileRollups             func(context.Context, RollupReconcileRequest) (RollupReconcileResult, error)
	finalizeRollup               func(context.Context, RollupFencedRequest) (RollupFinalizeResult, error)
	failRollup                   func(context.Context, RollupFailRequest) error
	deletePollRetention          func(context.Context, RetentionRequest) (RetentionResult, error)
	deleteRollupRowRetention     func(context.Context, RetentionRequest) (RetentionResult, error)
	deleteRollupRunRetention     func(context.Context, RetentionRequest) (RetentionResult, error)
	deleteCompactionRunRetention func(context.Context, RetentionRequest) (RetentionResult, error)
}

func (fake *fakeHistoryRepository) record(event string) {
	fake.mu.Lock()
	fake.events = append(fake.events, event)
	fake.mu.Unlock()
}

func (fake *fakeHistoryRepository) recorded() []string {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return append([]string(nil), fake.events...)
}

func (fake *fakeHistoryRepository) Plan(ctx context.Context, request PlanRequest) (PlanResult, error) {
	fake.record("plan")
	if fake.plan != nil {
		return fake.plan(ctx, request)
	}
	return PlanResult{}, nil
}

func (fake *fakeHistoryRepository) ClaimCompaction(ctx context.Context, request ClaimRequest) (*CompactionClaim, error) {
	fake.record("claim")
	if fake.claim != nil {
		return fake.claim(ctx, request)
	}
	return nil, ErrNoHistoryWork
}

func (fake *fakeHistoryRepository) RenewCompaction(ctx context.Context, request LeaseRequest) error {
	fake.record("renew")
	if fake.renew != nil {
		return fake.renew(ctx, request)
	}
	return nil
}

func (fake *fakeHistoryRepository) ReconcileCompactions(
	ctx context.Context, request ReconcileRequest,
) (ReconcileResult, error) {
	fake.record("reconcile")
	if fake.reconcile != nil {
		return fake.reconcile(ctx, request)
	}
	return ReconcileResult{}, nil
}

func (fake *fakeHistoryRepository) SummarizeCompaction(
	ctx context.Context, request FencedRequest,
) (SummarizeResult, error) {
	fake.record("summarize")
	if fake.summarize != nil {
		return fake.summarize(ctx, request)
	}
	return SummarizeResult{SourceChecksum: make([]byte, 32)}, nil
}

func (fake *fakeHistoryRepository) DeleteSnapshotBatch(
	ctx context.Context, request DeleteBatchRequest,
) (DeleteBatchResult, error) {
	fake.record("delete")
	if fake.deleteBatch != nil {
		return fake.deleteBatch(ctx, request)
	}
	return DeleteBatchResult{}, nil
}

func (fake *fakeHistoryRepository) CompleteCompaction(
	ctx context.Context, request CompleteRequest,
) (CompleteResult, error) {
	fake.record("complete")
	if fake.complete != nil {
		return fake.complete(ctx, request)
	}
	return CompleteResult{Completed: true}, nil
}

func (fake *fakeHistoryRepository) FailCompaction(ctx context.Context, request FailRequest) error {
	fake.record("fail:" + string(request.Reason))
	if fake.fail != nil {
		return fake.fail(ctx, request)
	}
	return nil
}

func (fake *fakeHistoryRepository) ClaimRollup(ctx context.Context, request RollupClaimRequest) (*RollupClaim, error) {
	fake.record("claim-rollup")
	if fake.claimRollup != nil {
		return fake.claimRollup(ctx, request)
	}
	return nil, ErrNoHistoryWork
}

func (fake *fakeHistoryRepository) RenewRollup(ctx context.Context, request RollupLeaseRequest) error {
	fake.record("renew-rollup")
	if fake.renewRollup != nil {
		return fake.renewRollup(ctx, request)
	}
	return nil
}

func (fake *fakeHistoryRepository) ReconcileRollups(
	ctx context.Context, request RollupReconcileRequest,
) (RollupReconcileResult, error) {
	fake.record("reconcile-rollup")
	if fake.reconcileRollups != nil {
		return fake.reconcileRollups(ctx, request)
	}
	return RollupReconcileResult{}, nil
}

func (fake *fakeHistoryRepository) FinalizeRollup(
	ctx context.Context, request RollupFencedRequest,
) (RollupFinalizeResult, error) {
	fake.record("finalize-rollup")
	if fake.finalizeRollup != nil {
		return fake.finalizeRollup(ctx, request)
	}
	return validRuntimeRollupFinalizeResult(), nil
}

func (fake *fakeHistoryRepository) FailRollup(ctx context.Context, request RollupFailRequest) error {
	fake.record("fail-rollup:" + string(request.Reason))
	if fake.failRollup != nil {
		return fake.failRollup(ctx, request)
	}
	return nil
}

func (fake *fakeHistoryRepository) DeletePollRetention(
	ctx context.Context, request RetentionRequest,
) (RetentionResult, error) {
	fake.record("retain-poll")
	if fake.deletePollRetention != nil {
		return fake.deletePollRetention(ctx, request)
	}
	return RetentionResult{}, nil
}

func (fake *fakeHistoryRepository) DeleteRollupRowRetention(
	ctx context.Context, request RetentionRequest,
) (RetentionResult, error) {
	fake.record("retain-rollup-row")
	if fake.deleteRollupRowRetention != nil {
		return fake.deleteRollupRowRetention(ctx, request)
	}
	return RetentionResult{}, nil
}

func (fake *fakeHistoryRepository) DeleteRollupRunRetention(
	ctx context.Context, request RetentionRequest,
) (RetentionResult, error) {
	fake.record("retain-rollup-run")
	if fake.deleteRollupRunRetention != nil {
		return fake.deleteRollupRunRetention(ctx, request)
	}
	return RetentionResult{}, nil
}

func (fake *fakeHistoryRepository) DeleteCompactionRunRetention(
	ctx context.Context, request RetentionRequest,
) (RetentionResult, error) {
	fake.record("retain-compaction-run")
	if fake.deleteCompactionRunRetention != nil {
		return fake.deleteCompactionRunRetention(ctx, request)
	}
	return RetentionResult{}, nil
}

func TestRepositoryLoopsResumePhaseMatrix(t *testing.T) {
	tests := []struct {
		name       string
		claim      CompactionClaim
		wantEvents []string
	}{
		{
			name:       "pending summarizes",
			claim:      validRuntimeCompactionClaim(CompactionPending, ""),
			wantEvents: []string{"renew", "summarize", "renew", "delete", "renew", "complete"},
		},
		{
			name:       "failed pending summarizes",
			claim:      validRuntimeCompactionClaim(CompactionFailed, FailedFromPending),
			wantEvents: []string{"renew", "summarize", "renew", "delete", "renew", "complete"},
		},
		{
			name:       "summarized resumes delete",
			claim:      validRuntimeCompactionClaim(CompactionSummarized, ""),
			wantEvents: []string{"renew", "delete", "renew", "complete"},
		},
		{
			name:       "failed summarized resumes delete",
			claim:      validRuntimeCompactionClaim(CompactionFailed, FailedFromSummarized),
			wantEvents: []string{"renew", "delete", "renew", "complete"},
		},
		{
			name:       "failed deleting resumes delete",
			claim:      validRuntimeCompactionClaim(CompactionFailed, FailedFromDeleting),
			wantEvents: []string{"renew", "delete", "renew", "complete"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &fakeHistoryRepository{}
			loops := newTestRepositoryLoops(t, repository)
			if err := loops.processCompaction(context.Background(), context.Background(), test.claim); err != nil {
				t.Fatal(err)
			}
			if got := repository.recorded(); fmt.Sprint(got) != fmt.Sprint(test.wantEvents) {
				t.Fatalf("events=%v want=%v", got, test.wantEvents)
			}
		})
	}
}

func TestRepositoryLoopsUnknownSummarizeCommitResumesWithoutReaggregation(t *testing.T) {
	claim := validRuntimeCompactionClaim(CompactionPending, "")
	summarizeCalls := 0
	repository := &fakeHistoryRepository{
		summarize: func(context.Context, FencedRequest) (SummarizeResult, error) {
			summarizeCalls++
			return SummarizeResult{SourceChecksum: make([]byte, 32)}, ErrHistoryCommitUnknown
		},
	}
	loops := newTestRepositoryLoops(t, repository)
	if err := loops.processCompaction(context.Background(), context.Background(), claim); !errors.Is(err, ErrHistoryCommitUnknown) {
		t.Fatalf("first result=%v", err)
	}
	if strings.Contains(fmt.Sprint(fixedHistoryError(fmt.Errorf("raw-marker"))), "marker") {
		t.Fatal("unknown repository error escaped fixed classification")
	}

	// A new claim reflects the transaction that may have committed. It resumes
	// from immutable summarized proof and must not invoke summarize again.
	resumed := validRuntimeCompactionClaim(CompactionSummarized, "")
	if err := loops.processCompaction(context.Background(), context.Background(), resumed); err != nil {
		t.Fatal(err)
	}
	if summarizeCalls != 1 {
		t.Fatalf("summarize calls=%d", summarizeCalls)
	}
}

func TestRepositoryLoopsSummarizeFixedFailuresDoNotLeakOrAdvance(t *testing.T) {
	for _, test := range []struct {
		name       string
		operation  error
		want       error
		wantEvents []string
	}{
		{
			name: "statement timeout", operation: ErrHistoryStatementTimeout,
			want:       ErrHistoryStatementTimeout,
			wantEvents: []string{"renew", "summarize", "fail:statement_timeout"},
		},
		{
			name: "database unavailable", operation: ErrHistoryDatabaseUnavailable,
			want:       ErrHistoryDatabaseUnavailable,
			wantEvents: []string{"renew", "summarize", "fail:database_unavailable"},
		},
		{
			name: "commit unknown", operation: ErrHistoryCommitUnknown,
			want:       ErrHistoryCommitUnknown,
			wantEvents: []string{"renew", "summarize"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &fakeHistoryRepository{
				summarize: func(context.Context, FencedRequest) (SummarizeResult, error) {
					return SummarizeResult{}, fmt.Errorf("protected-summarize-marker: %w", test.operation)
				},
			}
			loops := newTestRepositoryLoops(t, repository)
			err := loops.processCompaction(
				context.Background(), context.Background(), validRuntimeCompactionClaim(CompactionPending, ""),
			)
			if !errors.Is(err, test.want) || strings.Contains(err.Error(), "marker") {
				t.Fatalf("error=%v want=%v", err, test.want)
			}
			if got := repository.recorded(); fmt.Sprint(got) != fmt.Sprint(test.wantEvents) {
				t.Fatalf("events=%v want=%v", got, test.wantEvents)
			}
		})
	}
}

func TestRepositoryLoopsPartialDeleteUnknownCommitResumesFromPersistedCount(t *testing.T) {
	deleteCalls := 0
	remaining := uint64(3)
	total := uint64(0)
	repository := &fakeHistoryRepository{
		deleteBatch: func(context.Context, DeleteBatchRequest) (DeleteBatchResult, error) {
			deleteCalls++
			deleted := uint64(1)
			remaining--
			total++
			result := DeleteBatchResult{DeletedRows: deleted, RemainingRows: remaining, TotalDeletedRows: total}
			if deleteCalls == 2 {
				return result, ErrHistoryCommitUnknown
			}
			return result, nil
		},
	}
	loops := newTestRepositoryLoops(t, repository)
	claim := validRuntimeCompactionClaim(CompactionDeleting, "")
	claim.SourceSnapshotCount = 3
	if err := loops.processCompaction(context.Background(), context.Background(), claim); !errors.Is(err, ErrHistoryCommitUnknown) {
		t.Fatalf("first result=%v", err)
	}
	if remaining != 1 || total != 2 {
		t.Fatalf("persisted remaining=%d total=%d", remaining, total)
	}
	claim.DeletedSnapshotCount = total
	if err := loops.processCompaction(context.Background(), context.Background(), claim); err != nil {
		t.Fatal(err)
	}
	if deleteCalls != 3 || remaining != 0 || total != 3 {
		t.Fatalf("calls=%d remaining=%d total=%d", deleteCalls, remaining, total)
	}
}

func TestRepositoryLoopsUnknownCompleteCommitUsesBoundedIdempotentReadRetry(t *testing.T) {
	completeCalls := 0
	var firstRequest CompleteRequest
	repository := &fakeHistoryRepository{
		complete: func(_ context.Context, request CompleteRequest) (CompleteResult, error) {
			completeCalls++
			if completeCalls == 1 {
				firstRequest = request
				return CompleteResult{}, ErrHistoryCommitUnknown
			}
			if request.RunID != firstRequest.RunID || request.FencingToken != firstRequest.FencingToken ||
				string(request.ExpectedChecksum) != string(firstRequest.ExpectedChecksum) {
				return CompleteResult{}, ErrHistoryStateInconsistent
			}
			return CompleteResult{Completed: true}, nil
		},
	}
	loops := newTestRepositoryLoops(t, repository)
	if err := loops.processCompaction(
		context.Background(), context.Background(), validRuntimeCompactionClaim(CompactionSummarized, ""),
	); err != nil {
		t.Fatal(err)
	}
	if completeCalls != 2 {
		t.Fatalf("complete calls=%d", completeCalls)
	}

	completeCalls = 0
	repository.complete = func(context.Context, CompleteRequest) (CompleteResult, error) {
		completeCalls++
		return CompleteResult{}, ErrHistoryCommitUnknown
	}
	err := loops.processCompaction(
		context.Background(), context.Background(), validRuntimeCompactionClaim(CompactionSummarized, ""),
	)
	if !errors.Is(err, ErrHistoryCommitUnknown) || completeCalls != 2 {
		t.Fatalf("bounded retry calls=%d error=%v", completeCalls, err)
	}
}

func TestRepositoryLoopsStaleFenceStopsWithoutFailureMutation(t *testing.T) {
	repository := &fakeHistoryRepository{
		renew: func(context.Context, LeaseRequest) error { return ErrHistoryLeaseLost },
	}
	loops := newTestRepositoryLoops(t, repository)
	err := loops.processCompaction(
		context.Background(), context.Background(), validRuntimeCompactionClaim(CompactionPending, ""),
	)
	if !errors.Is(err, ErrHistoryLeaseLost) {
		t.Fatalf("error=%v", err)
	}
	if got := repository.recorded(); fmt.Sprint(got) != fmt.Sprint([]string{"renew"}) {
		t.Fatalf("stale fence performed extra mutations: %v", got)
	}
}

func TestRepositoryLoopsFinalizesRollupWithBoundedUnknownCommitReplay(t *testing.T) {
	claim := validRuntimeRollupClaim()
	requests := make([]RollupFencedRequest, 0, 2)
	repository := &fakeHistoryRepository{
		finalizeRollup: func(_ context.Context, request RollupFencedRequest) (RollupFinalizeResult, error) {
			requests = append(requests, request)
			if len(requests) == 1 {
				return RollupFinalizeResult{}, ErrHistoryCommitUnknown
			}
			return validRuntimeRollupFinalizeResult(), nil
		},
	}
	loops := newTestRepositoryLoops(t, repository)
	if err := loops.processRollup(context.Background(), context.Background(), claim); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 || requests[0] != requests[1] ||
		requests[0].RunID != claim.RunID || requests[0].FencingToken != claim.FencingToken {
		t.Fatalf("finalize requests=%+v", requests)
	}
	if got := repository.recorded(); fmt.Sprint(got) != fmt.Sprint([]string{
		"renew-rollup", "finalize-rollup", "finalize-rollup",
	}) {
		t.Fatalf("events=%v", got)
	}

	repository = &fakeHistoryRepository{
		finalizeRollup: func(context.Context, RollupFencedRequest) (RollupFinalizeResult, error) {
			return RollupFinalizeResult{}, ErrHistoryCommitUnknown
		},
	}
	loops = newTestRepositoryLoops(t, repository)
	err := loops.processRollup(context.Background(), context.Background(), claim)
	if !errors.Is(err, ErrHistoryCommitUnknown) {
		t.Fatalf("bounded unknown commit error=%v", err)
	}
	if got := repository.recorded(); fmt.Sprint(got) != fmt.Sprint([]string{
		"renew-rollup", "finalize-rollup", "finalize-rollup",
	}) {
		t.Fatalf("bounded replay events=%v", got)
	}

	repository = &fakeHistoryRepository{}
	loops = newTestRepositoryLoops(t, repository)
	repository.finalizeRollup = func(context.Context, RollupFencedRequest) (RollupFinalizeResult, error) {
		loops.triggerFatal()
		return RollupFinalizeResult{}, ErrHistoryCommitUnknown
	}
	err = loops.processRollup(context.Background(), context.Background(), claim)
	if !errors.Is(err, ErrRuntimeStopped) {
		t.Fatalf("fatal between retries error=%v", err)
	}
	if got := repository.recorded(); fmt.Sprint(got) != fmt.Sprint([]string{
		"renew-rollup", "finalize-rollup",
	}) {
		t.Fatalf("fatal retry started a new transaction: %v", got)
	}
}

func TestRepositoryLoopsRollupStaleFenceHasNoFailureMutation(t *testing.T) {
	for name, repository := range map[string]*fakeHistoryRepository{
		"renew": {
			renewRollup: func(context.Context, RollupLeaseRequest) error { return ErrHistoryLeaseLost },
		},
		"finalize": {
			finalizeRollup: func(context.Context, RollupFencedRequest) (RollupFinalizeResult, error) {
				return RollupFinalizeResult{}, ErrHistoryLeaseLost
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			loops := newTestRepositoryLoops(t, repository)
			err := loops.processRollup(context.Background(), context.Background(), validRuntimeRollupClaim())
			if !errors.Is(err, ErrHistoryLeaseLost) {
				t.Fatalf("error=%v", err)
			}
			for _, event := range repository.recorded() {
				if strings.HasPrefix(event, "fail-rollup:") {
					t.Fatalf("stale fence performed failure mutation: %v", repository.recorded())
				}
			}
		})
	}
}

func TestRepositoryLoopsRollupFailureClassificationAndFatalGate(t *testing.T) {
	for _, test := range []struct {
		name       string
		err        error
		wantReason FailureReason
		wantFatal  bool
	}{
		{name: "incomplete is recoverable", err: ErrHistorySegmentIncomplete, wantReason: FailureSegmentIncomplete},
		{name: "count mismatch", err: ErrHistorySegmentCountMismatch, wantFatal: true},
		{name: "checksum mismatch", err: ErrHistorySegmentChecksumMismatch, wantFatal: true},
		{name: "activation", err: ErrHistoryActivationInconsistent, wantFatal: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var failedReason FailureReason
			repository := &fakeHistoryRepository{
				finalizeRollup: func(context.Context, RollupFencedRequest) (RollupFinalizeResult, error) {
					return RollupFinalizeResult{}, fmt.Errorf("protected-rollup-marker: %w", test.err)
				},
				failRollup: func(_ context.Context, request RollupFailRequest) error {
					failedReason = request.Reason
					return nil
				},
			}
			loops := newTestRepositoryLoops(t, repository)
			err := loops.processRollup(context.Background(), context.Background(), validRuntimeRollupClaim())
			if !errors.Is(err, test.err) || strings.Contains(fmt.Sprint(err), "marker") {
				t.Fatalf("error=%v", err)
			}
			if failedReason != test.wantReason || loops.runtimeStopped() != test.wantFatal {
				t.Fatalf("reason=%s stopped=%t", failedReason, loops.runtimeStopped())
			}
		})
	}
}

func TestRepositoryLoopsRollupInvalidFinalizeFailsClosed(t *testing.T) {
	repository := &fakeHistoryRepository{
		finalizeRollup: func(context.Context, RollupFencedRequest) (RollupFinalizeResult, error) {
			return RollupFinalizeResult{
				Completed: true, ExpectedSegmentCount: 2, CompletedSegmentCount: 1,
				SegmentChecksum: make([]byte, 32),
			}, nil
		},
	}
	loops := newTestRepositoryLoops(t, repository)
	err := loops.processRollup(context.Background(), context.Background(), validRuntimeRollupClaim())
	if !errors.Is(err, ErrHistoryStateInconsistent) || !loops.runtimeStopped() {
		t.Fatalf("error=%v stopped=%t", err, loops.runtimeStopped())
	}
	if got := repository.recorded(); fmt.Sprint(got) != fmt.Sprint([]string{
		"renew-rollup", "finalize-rollup",
	}) {
		t.Fatalf("events=%v", got)
	}
}

func TestRepositoryLoopsRejectsDeleteConservationMismatch(t *testing.T) {
	repository := &fakeHistoryRepository{
		deleteBatch: func(context.Context, DeleteBatchRequest) (DeleteBatchResult, error) {
			return DeleteBatchResult{DeletedRows: 1, TotalDeletedRows: 2, RemainingRows: 0}, nil
		},
	}
	loops := newTestRepositoryLoops(t, repository)
	claim := validRuntimeCompactionClaim(CompactionDeleting, "")
	claim.SourceSnapshotCount = 1
	err := loops.processCompaction(context.Background(), context.Background(), claim)
	if !errors.Is(err, ErrHistoryStateInconsistent) {
		t.Fatalf("error=%v", err)
	}
	if got := repository.recorded(); fmt.Sprint(got) != fmt.Sprint([]string{"renew", "delete", "fail:internal"}) {
		t.Fatalf("events=%v", got)
	}
}

func TestRepositoryLoopsMapsFixedFailureWithoutRawError(t *testing.T) {
	var failedReason FailureReason
	repository := &fakeHistoryRepository{
		summarize: func(context.Context, FencedRequest) (SummarizeResult, error) {
			return SummarizeResult{}, fmt.Errorf("source-identity-marker: %w", ErrHistorySourceChecksumMismatch)
		},
		fail: func(_ context.Context, request FailRequest) error {
			failedReason = request.Reason
			return nil
		},
	}
	loops := newTestRepositoryLoops(t, repository)
	err := loops.processCompaction(
		context.Background(), context.Background(), validRuntimeCompactionClaim(CompactionPending, ""),
	)
	if !errors.Is(err, ErrHistorySourceChecksumMismatch) || strings.Contains(fmt.Sprint(err), "marker") {
		t.Fatalf("error=%v", err)
	}
	if failedReason != FailureSourceChecksumMismatch {
		t.Fatalf("failed reason=%s", failedReason)
	}
}

func TestRepositoryLoopsPlannerAndReconcilerUseBoundedBackoff(t *testing.T) {
	for name, configure := range map[string]func(*fakeHistoryRepository, *int){
		"planner": func(repository *fakeHistoryRepository, calls *int) {
			repository.plan = func(_ context.Context, request PlanRequest) (PlanResult, error) {
				(*calls)++
				if request.Limit != DefaultHistoryPlanLimit {
					return PlanResult{}, ErrHistoryStateInconsistent
				}
				if *calls == 1 {
					return PlanResult{}, ErrHistoryDatabaseUnavailable
				}
				return PlanResult{CompactionRunsCreated: 1}, nil
			}
		},
		"reconciler": func(repository *fakeHistoryRepository, calls *int) {
			repository.reconcile = func(_ context.Context, request ReconcileRequest) (ReconcileResult, error) {
				(*calls)++
				if request.Limit != DefaultHistoryReconcileLimit {
					return ReconcileResult{}, ErrHistoryStateInconsistent
				}
				if *calls == 1 {
					return ReconcileResult{}, ErrHistoryDatabaseUnavailable
				}
				return ReconcileResult{Reconciled: 1}, nil
			}
		},
		"rollup reconciler": func(repository *fakeHistoryRepository, calls *int) {
			repository.reconcileRollups = func(
				_ context.Context, request RollupReconcileRequest,
			) (RollupReconcileResult, error) {
				(*calls)++
				if request.Limit != DefaultHistoryReconcileLimit {
					return RollupReconcileResult{}, ErrHistoryStateInconsistent
				}
				if *calls == 1 {
					return RollupReconcileResult{}, ErrHistoryDatabaseUnavailable
				}
				return RollupReconcileResult{Reconciled: 1}, nil
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			repository := &fakeHistoryRepository{}
			calls := 0
			configure(repository, &calls)
			loops := newTestRepositoryLoops(t, repository)
			ctx, cancel := context.WithCancel(context.Background())
			var waits []time.Duration
			loops.wait = func(_ context.Context, duration time.Duration) error {
				waits = append(waits, duration)
				if len(waits) == 2 {
					cancel()
					return context.Canceled
				}
				return nil
			}
			var err error
			switch name {
			case "planner":
				err = loops.RunPlanner(ctx)
			case "reconciler":
				err = loops.RunReconciler(ctx)
			default:
				err = loops.RunRollupReconciler(ctx)
			}
			if err != nil || calls != 2 || len(waits) != 2 ||
				waits[0] != MinimumDatabaseBackoff || waits[1] != MinimumScanInterval {
				t.Fatalf("calls=%d waits=%v error=%v", calls, waits, err)
			}
		})
	}
}

func TestRepositoryLoopsWorkerDatabaseOutageBacksOffWithoutRawError(t *testing.T) {
	repository := &fakeHistoryRepository{
		claim: func(context.Context, ClaimRequest) (*CompactionClaim, error) {
			return nil, fmt.Errorf("database-address-marker: %w", ErrHistoryDatabaseUnavailable)
		},
	}
	loops := newTestRepositoryLoops(t, repository)
	ctx, cancel := context.WithCancel(context.Background())
	waitCalls := 0
	loops.wait = func(context.Context, time.Duration) error {
		waitCalls++
		cancel()
		return context.Canceled
	}
	if err := loops.RunWorker(ctx, context.Background()); err != nil {
		t.Fatalf("worker error=%v", err)
	}
	if got := repository.recorded(); fmt.Sprint(got) != fmt.Sprint([]string{"claim"}) {
		t.Fatalf("events=%v", got)
	}
	if waitCalls != 1 {
		t.Fatalf("backoff waits=%d", waitCalls)
	}
}

func TestRepositoryLoopsShutdownStopsClaimsButFinishesShortOperation(t *testing.T) {
	claimContext, stopClaims := context.WithCancel(context.Background())
	operationContext, stopOperations := context.WithCancel(context.Background())
	defer stopOperations()
	entered := make(chan struct{})
	release := make(chan struct{})
	claimCalls := 0
	repository := &fakeHistoryRepository{
		claim: func(context.Context, ClaimRequest) (*CompactionClaim, error) {
			claimCalls++
			if claimCalls == 1 {
				claim := validRuntimeCompactionClaim(CompactionPending, "")
				return &claim, nil
			}
			return nil, ErrNoHistoryWork
		},
		summarize: func(ctx context.Context, _ FencedRequest) (SummarizeResult, error) {
			close(entered)
			select {
			case <-release:
				return SummarizeResult{SourceChecksum: make([]byte, 32)}, nil
			case <-ctx.Done():
				return SummarizeResult{}, ctx.Err()
			}
		},
	}
	loops := newTestRepositoryLoops(t, repository)
	done := make(chan error, 1)
	go func() { done <- loops.RunWorker(claimContext, operationContext) }()
	<-entered
	stopClaims()
	select {
	case err := <-done:
		t.Fatalf("worker exited before operation drain: %v", err)
	default:
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not drain bounded operation")
	}
	if claimCalls != 1 {
		t.Fatalf("new claim started during shutdown: %d", claimCalls)
	}
	if got := repository.recorded(); fmt.Sprint(got) != fmt.Sprint([]string{"claim", "renew", "summarize"}) {
		t.Fatalf("shutdown started another transaction: %v", got)
	}
}

func TestRepositoryLoopsRollupShutdownDrainsCurrentFinalizeOnly(t *testing.T) {
	claimContext, stopClaims := context.WithCancel(context.Background())
	operationContext, stopOperations := context.WithCancel(context.Background())
	defer stopOperations()
	entered := make(chan struct{})
	release := make(chan struct{})
	claimCalls := 0
	repository := &fakeHistoryRepository{
		claimRollup: func(context.Context, RollupClaimRequest) (*RollupClaim, error) {
			claimCalls++
			if claimCalls == 1 {
				claim := validRuntimeRollupClaim()
				return &claim, nil
			}
			return nil, ErrNoHistoryWork
		},
		finalizeRollup: func(ctx context.Context, _ RollupFencedRequest) (RollupFinalizeResult, error) {
			close(entered)
			select {
			case <-release:
				return validRuntimeRollupFinalizeResult(), nil
			case <-ctx.Done():
				return RollupFinalizeResult{}, ctx.Err()
			}
		},
	}
	loops := newTestRepositoryLoops(t, repository)
	done := make(chan error, 1)
	go func() { done <- loops.RunRollupWorker(claimContext, operationContext) }()
	<-entered
	stopClaims()
	select {
	case err := <-done:
		t.Fatalf("rollup worker exited before finalize drained: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("rollup worker did not stop after finalize drained")
	}
	if claimCalls != 1 {
		t.Fatalf("claim calls=%d", claimCalls)
	}
	if got := repository.recorded(); fmt.Sprint(got) != fmt.Sprint([]string{
		"claim-rollup", "renew-rollup", "finalize-rollup",
	}) {
		t.Fatalf("shutdown started another rollup transaction: %v", got)
	}
}

func TestRepositoryLoopsShutdownBetweenDeleteBatchesStartsNoNewTransaction(t *testing.T) {
	claimContext, stopClaims := context.WithCancel(context.Background())
	operationContext, stopOperations := context.WithCancel(context.Background())
	defer stopOperations()
	entered := make(chan struct{})
	release := make(chan struct{})
	claimCalls := 0
	deleteCalls := 0
	repository := &fakeHistoryRepository{
		claim: func(context.Context, ClaimRequest) (*CompactionClaim, error) {
			claimCalls++
			claim := validRuntimeCompactionClaim(CompactionDeleting, "")
			claim.SourceSnapshotCount = 3
			return &claim, nil
		},
		deleteBatch: func(ctx context.Context, _ DeleteBatchRequest) (DeleteBatchResult, error) {
			deleteCalls++
			close(entered)
			select {
			case <-release:
				return DeleteBatchResult{DeletedRows: 1, TotalDeletedRows: 1, RemainingRows: 2}, nil
			case <-ctx.Done():
				return DeleteBatchResult{}, ctx.Err()
			}
		},
	}
	loops := newTestRepositoryLoops(t, repository)
	done := make(chan error, 1)
	go func() { done <- loops.RunWorker(claimContext, operationContext) }()
	<-entered
	stopClaims()
	select {
	case err := <-done:
		t.Fatalf("worker exited before delete transaction drain: %v", err)
	default:
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not stop between delete batches")
	}
	if claimCalls != 1 || deleteCalls != 1 {
		t.Fatalf("claim calls=%d delete calls=%d", claimCalls, deleteCalls)
	}
	if got := repository.recorded(); fmt.Sprint(got) != fmt.Sprint([]string{"claim", "renew", "delete"}) {
		t.Fatalf("shutdown started a second batch or completion: %v", got)
	}
}

func TestRepositoryLoopsUsesConfiguredConcurrency(t *testing.T) {
	requests := make(chan uuid.UUID, 4)
	repository := &fakeHistoryRepository{
		claim: func(ctx context.Context, request ClaimRequest) (*CompactionClaim, error) {
			requests <- request.WorkerToken
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	config := testLoopsConfig()
	config.Concurrency = 3
	loops, err := NewRepositoryLoops(config, repository)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- loops.RunWorker(ctx, context.Background()) }()
	seen := map[uuid.UUID]struct{}{}
	for len(seen) < 3 {
		select {
		case token := <-requests:
			seen[token] = struct{}{}
		case <-time.After(time.Second):
			t.Fatalf("only observed %d workers", len(seen))
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRepositoryLoopsShareConcurrencyAcrossCompactionAndRollup(t *testing.T) {
	for _, concurrency := range []int{1, 2} {
		t.Run(fmt.Sprintf("concurrency_%d", concurrency), func(t *testing.T) {
			var mu sync.Mutex
			active, peak := 0, 0
			compactionClaimed, rollupClaimed := false, false
			entered := make(chan string, 2)
			release := make(chan struct{}, 2)
			compactionDone := make(chan struct{})
			rollupDone := make(chan struct{})
			var compactionDoneOnce, rollupDoneOnce sync.Once
			enter := func(kind string) {
				mu.Lock()
				active++
				if active > peak {
					peak = active
				}
				mu.Unlock()
				entered <- kind
				<-release
				mu.Lock()
				active--
				mu.Unlock()
			}
			repository := &fakeHistoryRepository{
				claim: func(context.Context, ClaimRequest) (*CompactionClaim, error) {
					mu.Lock()
					defer mu.Unlock()
					if compactionClaimed {
						return nil, ErrNoHistoryWork
					}
					compactionClaimed = true
					claim := validRuntimeCompactionClaim(CompactionSummarized, "")
					return &claim, nil
				},
				deleteBatch: func(context.Context, DeleteBatchRequest) (DeleteBatchResult, error) {
					enter("compaction")
					return DeleteBatchResult{}, nil
				},
				complete: func(context.Context, CompleteRequest) (CompleteResult, error) {
					compactionDoneOnce.Do(func() { close(compactionDone) })
					return CompleteResult{Completed: true}, nil
				},
				claimRollup: func(context.Context, RollupClaimRequest) (*RollupClaim, error) {
					mu.Lock()
					defer mu.Unlock()
					if rollupClaimed {
						return nil, ErrNoHistoryWork
					}
					rollupClaimed = true
					claim := validRuntimeRollupClaim()
					return &claim, nil
				},
				finalizeRollup: func(context.Context, RollupFencedRequest) (RollupFinalizeResult, error) {
					enter("rollup")
					rollupDoneOnce.Do(func() { close(rollupDone) })
					return validRuntimeRollupFinalizeResult(), nil
				},
			}
			config := testLoopsConfig()
			config.Concurrency = concurrency
			loops, err := NewRepositoryLoops(config, repository)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			compactionWorkerDone := make(chan error, 1)
			rollupWorkerDone := make(chan error, 1)
			go func() { compactionWorkerDone <- loops.RunWorker(ctx, context.Background()) }()
			go func() { rollupWorkerDone <- loops.RunRollupWorker(ctx, context.Background()) }()

			receiveKind := func() string {
				t.Helper()
				select {
				case kind := <-entered:
					return kind
				case <-time.After(time.Second):
					t.Fatal("worker kind starved waiting for a shared slot")
					return ""
				}
			}
			first := receiveKind()
			if concurrency == 1 {
				select {
				case second := <-entered:
					t.Fatalf("%s and %s exceeded the shared single slot", first, second)
				case <-time.After(20 * time.Millisecond):
				}
				release <- struct{}{}
			}
			second := receiveKind()
			if first == second {
				t.Fatalf("worker kind starved: first=%s second=%s", first, second)
			}
			if concurrency == 2 {
				release <- struct{}{}
			}
			release <- struct{}{}

			for _, done := range []<-chan struct{}{compactionDone, rollupDone} {
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("worker kind did not complete")
				}
			}
			cancel()
			for name, done := range map[string]<-chan error{
				"compaction": compactionWorkerDone, "rollup": rollupWorkerDone,
			} {
				select {
				case err := <-done:
					if err != nil {
						t.Fatalf("%s worker error=%v", name, err)
					}
				case <-time.After(time.Second):
					t.Fatalf("%s worker did not stop", name)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if peak != concurrency || !compactionClaimed || !rollupClaimed || len(loops.workSlots) != 0 {
				t.Fatalf("peak=%d claims=%t/%t occupied_slots=%d",
					peak, compactionClaimed, rollupClaimed, len(loops.workSlots))
			}
		})
	}
}

func TestRepositoryLoopsFatalMismatchStopsPlannerAndNewWorkerTransactions(t *testing.T) {
	fatalClaim := validRuntimeCompactionClaim(CompactionDeleting, "")
	fatalClaim.SourceSnapshotCount = 1
	drainingClaim := validRuntimeCompactionClaim(CompactionDeleting, "")
	drainingClaim.SourceSnapshotCount = 2

	var mu sync.Mutex
	claimCalls := 0
	deleteCalls := map[uuid.UUID]int{}
	completeCalls := 0
	planStarted := make(chan struct{})
	drainingDeleteStarted := make(chan struct{})
	releaseDrainingDelete := make(chan struct{})
	repository := &fakeHistoryRepository{
		plan: func(context.Context, PlanRequest) (PlanResult, error) {
			select {
			case <-planStarted:
			default:
				close(planStarted)
			}
			return PlanResult{}, nil
		},
		claim: func(context.Context, ClaimRequest) (*CompactionClaim, error) {
			mu.Lock()
			defer mu.Unlock()
			claimCalls++
			switch claimCalls {
			case 1:
				claim := fatalClaim
				return &claim, nil
			case 2:
				claim := drainingClaim
				return &claim, nil
			default:
				return nil, ErrNoHistoryWork
			}
		},
		deleteBatch: func(_ context.Context, request DeleteBatchRequest) (DeleteBatchResult, error) {
			mu.Lock()
			deleteCalls[request.RunID]++
			mu.Unlock()
			if request.RunID == fatalClaim.RunID {
				<-drainingDeleteStarted
				return DeleteBatchResult{}, ErrHistorySourceChecksumMismatch
			}
			close(drainingDeleteStarted)
			<-releaseDrainingDelete
			return DeleteBatchResult{DeletedRows: 1, TotalDeletedRows: 1, RemainingRows: 1}, nil
		},
		complete: func(context.Context, CompleteRequest) (CompleteResult, error) {
			mu.Lock()
			completeCalls++
			mu.Unlock()
			return CompleteResult{Completed: true}, nil
		},
	}
	config := testLoopsConfig()
	config.Concurrency = 2
	loops, err := NewRepositoryLoops(config, repository)
	if err != nil {
		t.Fatal(err)
	}

	plannerDone := make(chan error, 1)
	go func() { plannerDone <- loops.RunPlanner(context.Background()) }()
	select {
	case <-planStarted:
	case <-time.After(time.Second):
		t.Fatal("planner did not start")
	}
	workerDone := make(chan error, 1)
	go func() { workerDone <- loops.RunWorker(context.Background(), context.Background()) }()

	select {
	case <-loops.fatalContext.Done():
	case <-time.After(time.Second):
		t.Fatal("checksum mismatch did not trip the shared fatal signal")
	}
	select {
	case err := <-plannerDone:
		if !errors.Is(err, ErrRuntimeStopped) {
			t.Fatalf("planner error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("planner did not stop after fatal mismatch")
	}
	select {
	case err := <-workerDone:
		t.Fatalf("worker returned before the active delete drained: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(releaseDrainingDelete)
	select {
	case err := <-workerDone:
		if !errors.Is(err, ErrRuntimeStopped) {
			t.Fatalf("worker error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not stop after the active delete drained")
	}

	mu.Lock()
	defer mu.Unlock()
	if claimCalls != 2 || deleteCalls[fatalClaim.RunID] != 1 || deleteCalls[drainingClaim.RunID] != 1 || completeCalls != 0 {
		t.Fatalf("claims=%d deletes=%v completes=%d", claimCalls, deleteCalls, completeCalls)
	}
}

func TestFatalIntegritySignalAllowsOnlyOriginatingTerminalFailAndBlocksOtherTransactions(t *testing.T) {
	claim := validRuntimeCompactionClaim(CompactionDeleting, "")
	claim.SourceSnapshotCount = 1
	var loops *RepositoryLoops
	repository := &fakeHistoryRepository{
		deleteBatch: func(context.Context, DeleteBatchRequest) (DeleteBatchResult, error) {
			return DeleteBatchResult{}, ErrHistorySourceChecksumMismatch
		},
		fail: func(_ context.Context, request FailRequest) error {
			if loops == nil || !loops.runtimeStopped() {
				t.Fatal("originating terminal fail ran before the shared fatal signal")
			}
			if request.RunID != claim.RunID || request.FencingToken != claim.FencingToken ||
				request.Reason != FailureSourceChecksumMismatch {
				t.Fatalf("terminal fail request=%+v", request)
			}
			return nil
		},
	}
	var err error
	loops, err = NewRepositoryLoops(testLoopsConfig(), repository)
	if err != nil {
		t.Fatal(err)
	}
	if err = loops.processCompaction(context.Background(), context.Background(), claim); !errors.Is(
		err, ErrHistorySourceChecksumMismatch,
	) {
		t.Fatalf("process error=%v", err)
	}
	if err = loops.RunPlanner(context.Background()); !errors.Is(err, ErrRuntimeStopped) {
		t.Fatalf("planner after fatal error=%v", err)
	}
	if err = loops.RunRetentionWorker(context.Background(), context.Background()); !errors.Is(err, ErrRuntimeStopped) {
		t.Fatalf("retention after fatal error=%v", err)
	}
	if events := repository.recorded(); !reflect.DeepEqual(events, []string{
		"renew", "delete", "fail:source_checksum_mismatch",
	}) {
		t.Fatalf("post-fatal transaction events=%v", events)
	}
}

func TestRepositoryLoopsRollupFatalStopsCompactionAfterCurrentBatch(t *testing.T) {
	compactionClaim := validRuntimeCompactionClaim(CompactionDeleting, "")
	compactionClaim.SourceSnapshotCount = 2
	rollupClaim := validRuntimeRollupClaim()
	deleteStarted := make(chan struct{})
	releaseDelete := make(chan struct{})
	var mu sync.Mutex
	compactionClaims, rollupClaims, deletes, completes := 0, 0, 0, 0
	repository := &fakeHistoryRepository{
		claim: func(context.Context, ClaimRequest) (*CompactionClaim, error) {
			mu.Lock()
			defer mu.Unlock()
			compactionClaims++
			if compactionClaims == 1 {
				claim := compactionClaim
				return &claim, nil
			}
			return nil, ErrNoHistoryWork
		},
		deleteBatch: func(context.Context, DeleteBatchRequest) (DeleteBatchResult, error) {
			mu.Lock()
			deletes++
			mu.Unlock()
			close(deleteStarted)
			<-releaseDelete
			return DeleteBatchResult{DeletedRows: 1, TotalDeletedRows: 1, RemainingRows: 1}, nil
		},
		complete: func(context.Context, CompleteRequest) (CompleteResult, error) {
			mu.Lock()
			completes++
			mu.Unlock()
			return CompleteResult{Completed: true}, nil
		},
		claimRollup: func(context.Context, RollupClaimRequest) (*RollupClaim, error) {
			mu.Lock()
			defer mu.Unlock()
			rollupClaims++
			if rollupClaims == 1 {
				claim := rollupClaim
				return &claim, nil
			}
			return nil, ErrNoHistoryWork
		},
		finalizeRollup: func(context.Context, RollupFencedRequest) (RollupFinalizeResult, error) {
			<-deleteStarted
			return RollupFinalizeResult{}, ErrHistorySegmentChecksumMismatch
		},
	}
	config := testLoopsConfig()
	config.Concurrency = 2
	loops, err := NewRepositoryLoops(config, repository)
	if err != nil {
		t.Fatal(err)
	}
	compactionDone := make(chan error, 1)
	rollupDone := make(chan error, 1)
	go func() { compactionDone <- loops.RunWorker(context.Background(), context.Background()) }()
	go func() { rollupDone <- loops.RunRollupWorker(context.Background(), context.Background()) }()

	select {
	case <-loops.fatalContext.Done():
	case <-time.After(time.Second):
		t.Fatal("rollup checksum mismatch did not stop the shared runtime")
	}
	select {
	case err := <-compactionDone:
		t.Fatalf("compaction returned before its current batch drained: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(releaseDelete)
	for name, done := range map[string]<-chan error{"compaction": compactionDone, "rollup": rollupDone} {
		select {
		case err := <-done:
			if !errors.Is(err, ErrRuntimeStopped) {
				t.Fatalf("%s error=%v", name, err)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s loop did not stop", name)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if compactionClaims < 1 || compactionClaims > 2 || rollupClaims < 1 || rollupClaims > 2 ||
		deletes != 1 || completes != 0 {
		t.Fatalf("compactionClaims=%d rollupClaims=%d deletes=%d completes=%d",
			compactionClaims, rollupClaims, deletes, completes)
	}
}

func TestFatalHistoryErrorClassification(t *testing.T) {
	for _, err := range []error{
		ErrHistoryStateInconsistent,
		ErrHistorySourceDayMismatch,
		ErrHistorySourceCountMismatch,
		ErrHistorySourceChecksumMismatch,
		ErrHistorySegmentCountMismatch,
		ErrHistorySegmentChecksumMismatch,
		ErrHistoryActivationInconsistent,
		ErrHistoryRepositoryFailure,
	} {
		if !isFatalHistoryError(fmt.Errorf("protected-marker: %w", err)) {
			t.Fatalf("error was not fatal: %v", err)
		}
	}
	for _, err := range []error{
		ErrNoHistoryWork, ErrHistoryLeaseLost, ErrHistoryCommitUnknown,
		ErrHistoryStatementTimeout, ErrHistoryDatabaseUnavailable,
		ErrHistorySegmentIncomplete, context.Canceled,
	} {
		if isFatalHistoryError(err) {
			t.Fatalf("transient error was fatal: %v", err)
		}
	}
}

func TestHistoryPlanResultBoundsBothRunKinds(t *testing.T) {
	if !validPlanResult(PlanResult{CompactionRunsCreated: 2, RollupRunsCreated: 3}, 3) {
		t.Fatal("bounded mixed plan was rejected")
	}
	for _, invalid := range []PlanResult{
		{CompactionRunsCreated: -1},
		{CompactionRunsCreated: 4},
		{RollupRunsCreated: -1},
		{RollupRunsCreated: 4},
	} {
		if validPlanResult(invalid, 3) {
			t.Fatalf("invalid plan accepted: %+v", invalid)
		}
	}
}

func TestRepositoryLoopDTOFormattingIsRedacted(t *testing.T) {
	marker := "protected-history-marker"
	values := []any{
		PlanRequest{}, PlanResult{}, ClaimRequest{},
		CompactionClaim{SourceChecksum: []byte(marker)}, LeaseRequest{},
		ReconcileRequest{}, ReconcileResult{}, FencedRequest{},
		SummarizeResult{SourceChecksum: []byte(marker)}, DeleteBatchRequest{}, DeleteBatchResult{},
		CompleteRequest{ExpectedChecksum: []byte(marker)}, CompleteResult{}, FailRequest{},
		RollupClaimRequest{}, RollupClaim{}, RollupLeaseRequest{},
		RollupReconcileRequest{}, RollupReconcileResult{}, RollupFencedRequest{},
		RollupFinalizeResult{SegmentChecksum: []byte(marker)}, RollupFailRequest{},
		RetentionRequest{}, RetentionResult{},
	}
	for _, value := range values {
		for _, format := range []string{"%v", "%+v", "%#v"} {
			formatted := strings.ToLower(fmt.Sprintf(format, value))
			if strings.Contains(formatted, marker) || !strings.Contains(formatted, "redacted") {
				t.Fatalf("formatter leaked with %s: %s", format, formatted)
			}
		}
	}
}

func newTestRepositoryLoops(t *testing.T, repository Repository) *RepositoryLoops {
	t.Helper()
	loops, err := NewRepositoryLoops(testLoopsConfig(), repository)
	if err != nil {
		t.Fatal(err)
	}
	return loops
}

func testLoopsConfig() Config {
	return Config{
		Enabled: true, ScanInterval: MinimumScanInterval, ClaimLease: MinimumClaimLease,
		Concurrency: 1, DeleteBatchSize: 2, StatementTimeout: MinimumStatementTimeout,
		DatabaseBackoffInitial: MinimumDatabaseBackoff, DatabaseBackoffMaximum: 400 * time.Millisecond,
		ShutdownGrace: MinimumStatementTimeout,
	}
}

func validRuntimeCompactionClaim(status CompactionState, failedFrom FailedFrom) CompactionClaim {
	claim := CompactionClaim{
		RunID: uuid.New(), SummaryDate: time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC),
		InstanceID: uuid.New(), ProviderPolicyVersion: uuid.New(), Status: status, FailedFrom: failedFrom,
		LeaseExpiresAt: time.Now().Add(time.Minute), FencingToken: uuid.New(), Attempt: 1,
	}
	phase, _ := resumeCompactionPhase(claim)
	if phase != CompactionPending {
		claim.SourceChecksum = make([]byte, 32)
	}
	return claim
}

func validRuntimeRollupClaim() RollupClaim {
	return RollupClaim{
		RunID: uuid.New(), SummaryDate: time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC),
		InstanceID: uuid.New(), LeaseExpiresAt: time.Now().Add(time.Minute),
		FencingToken: uuid.New(), Attempt: 1,
	}
}

func validRuntimeRollupFinalizeResult() RollupFinalizeResult {
	return RollupFinalizeResult{
		Completed: true, ExpectedSegmentCount: 2, CompletedSegmentCount: 2,
		SegmentChecksum: make([]byte, 32),
	}
}
