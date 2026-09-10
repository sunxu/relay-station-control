package jobs

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/prometheus/client_golang/prometheus"
)

type fakeRepository struct {
	mu                  sync.Mutex
	claims              []Lease
	claimErr            error
	expiredClaims       []Lease
	outboxClaims        []OutboxLease
	outboxClaimErr      error
	transitions         []Transition
	outboxTransitions   []OutboxTransition
	renewErr            error
	renewErrors         []error
	renewCalls          int
	outboxRenewErr      error
	transitionErr       error
	transitionOutcome   *TransitionOutcome
	outboxTransitionErr error
	validToken          uuid.UUID
	outboxValidToken    uuid.UUID
	claimCalls          int
	recoverableCalls    int
	outboxClaimCalls    int
}

func (repository *fakeRepository) ClaimRunnable(context.Context, ClaimRequest) (*Lease, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.claimCalls++
	if repository.claimErr != nil {
		return nil, repository.claimErr
	}
	if len(repository.claims) == 0 {
		return nil, ErrNotFound
	}
	lease := repository.claims[0]
	repository.claims = repository.claims[1:]
	return &lease, nil
}

func (repository *fakeRepository) RenewLease(context.Context, uuid.UUID, uuid.UUID, time.Duration) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.renewCalls++
	if len(repository.renewErrors) > 0 {
		err := repository.renewErrors[0]
		repository.renewErrors = repository.renewErrors[1:]
		return err
	}
	return repository.renewErr
}

func (repository *fakeRepository) TransitionFenced(ctx context.Context, transition Transition) (TransitionOutcome, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if err := transition.Validate(); err != nil {
		return TransitionOutcome{}, err
	}
	if repository.transitionErr != nil {
		return TransitionOutcome{}, repository.transitionErr
	}
	if repository.validToken != uuid.Nil && transition.Token != repository.validToken {
		return TransitionOutcome{}, ErrLostLease
	}
	before := len(repository.transitions)
	repository.transitions = append(repository.transitions, transition)
	if transition.Mutation != nil {
		if err := transition.Mutation(ctx, fakeDBTX{}); err != nil {
			repository.transitions = repository.transitions[:before]
			return TransitionOutcome{}, err
		}
	}
	if repository.transitionOutcome != nil {
		return *repository.transitionOutcome, nil
	}
	return TransitionOutcome{Status: transition.To, ErrorCode: transition.ErrorCode}, nil
}

type fakeDBTX struct{}

func (fakeDBTX) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (fakeDBTX) Query(context.Context, string, ...any) (pgx.Rows, error) { return nil, nil }
func (fakeDBTX) QueryRow(context.Context, string, ...any) pgx.Row        { return nil }

type eagerClock struct{}

func (eagerClock) NewTimer(time.Duration) Timer {
	timer := &eagerTimer{channel: make(chan time.Time, 1)}
	timer.Reset(0)
	return timer
}

type eagerTimer struct{ channel chan time.Time }

func (timer *eagerTimer) C() <-chan time.Time { return timer.channel }
func (timer *eagerTimer) Reset(time.Duration) {
	select {
	case timer.channel <- time.Now():
	default:
	}
}
func (*eagerTimer) Stop() bool { return true }

func (repository *fakeRepository) ClaimRecoverable(context.Context, ClaimRequest) (*Lease, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.recoverableCalls++
	if len(repository.expiredClaims) == 0 {
		return nil, ErrNotFound
	}
	lease := repository.expiredClaims[0]
	repository.expiredClaims = repository.expiredClaims[1:]
	return &lease, nil
}

func (repository *fakeRepository) ClaimOutbox(context.Context, OutboxClaimRequest) (*OutboxLease, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.outboxClaimCalls++
	if repository.outboxClaimErr != nil {
		return nil, repository.outboxClaimErr
	}
	if len(repository.outboxClaims) == 0 {
		return nil, ErrNotFound
	}
	lease := repository.outboxClaims[0]
	repository.outboxClaims = repository.outboxClaims[1:]
	return &lease, nil
}

func (repository *fakeRepository) RenewOutboxLease(context.Context, uuid.UUID, uuid.UUID, time.Duration) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return repository.outboxRenewErr
}

func (repository *fakeRepository) TransitionOutboxFenced(_ context.Context, transition OutboxTransition) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.outboxTransitionErr != nil {
		return repository.outboxTransitionErr
	}
	if repository.outboxValidToken != uuid.Nil && transition.Token != repository.outboxValidToken {
		return ErrLostLease
	}
	repository.outboxTransitions = append(repository.outboxTransitions, transition)
	return nil
}

func (repository *fakeRepository) lastTransition(t *testing.T) Transition {
	t.Helper()
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if len(repository.transitions) == 0 {
		t.Fatal("no transition recorded")
	}
	return repository.transitions[len(repository.transitions)-1]
}

type fakeExecutor struct {
	execute       func(context.Context, Execution) ExecuteResult
	verify        func(context.Context, Execution) VerifyResult
	rollback      func(context.Context, Execution) RollbackResult
	executions    atomic.Int32
	verifications atomic.Int32
	rollbacks     atomic.Int32
}

type captureLogger struct {
	mu      sync.Mutex
	records []LogRecord
}

func (logger *captureLogger) Log(_ context.Context, record LogRecord) {
	logger.mu.Lock()
	defer logger.mu.Unlock()
	logger.records = append(logger.records, record)
}

func (logger *captureLogger) snapshot() []LogRecord {
	logger.mu.Lock()
	defer logger.mu.Unlock()
	return append([]LogRecord(nil), logger.records...)
}

func (executor *fakeExecutor) Execute(ctx context.Context, execution Execution) ExecuteResult {
	executor.executions.Add(1)
	if executor.execute != nil {
		return executor.execute(ctx, execution)
	}
	return ExecuteResult{Disposition: ExecuteNeedsVerification}
}
func (executor *fakeExecutor) Verify(ctx context.Context, execution Execution) VerifyResult {
	executor.verifications.Add(1)
	if executor.verify != nil {
		return executor.verify(ctx, execution)
	}
	return VerifyResult{Disposition: VerifyEffectUnknown}
}
func (executor *fakeExecutor) Rollback(ctx context.Context, execution Execution) RollbackResult {
	executor.rollbacks.Add(1)
	if executor.rollback != nil {
		return executor.rollback(ctx, execution)
	}
	return RollbackResult{Disposition: RollbackUnknown}
}

func leasedJob(t *testing.T, registry *Registry, status Status, attempt int) Lease {
	t.Helper()
	payload, hash, definition, err := registry.ValidateAndHash("test.synthetic", 1, validPayload())
	if err != nil {
		t.Fatal(err)
	}
	verificationAttempt := 0
	if status == StatusVerifying || status == StatusRollingBack {
		verificationAttempt = 1
	}
	return Lease{Job: Job{ID: uuid.New(), OperationID: uuid.New(), Kind: "test.synthetic", SchemaVersion: 1, Payload: payload, PayloadHash: hash, Status: status, Attempt: attempt, MaxAttempts: definition.MaxAttempts, VerificationAttempt: verificationAttempt, MaxVerifyAttempts: definition.MaxVerifyAttempts, Timeout: definition.Timeout, LeaseDuration: definition.LeaseDuration, HeartbeatInterval: definition.HeartbeatInterval, ReplaySafe: definition.ReplaySafe, AllowUnknownEffectReplay: definition.AllowUnknownEffectReplay, AllowDirectSuccess: definition.AllowDirectSuccess, AllowRollback: definition.AllowRollback}, Owner: "test-worker", Token: uuid.New(), ExpiresAt: time.Now().Add(time.Minute)}
}

func newTestWorker(t *testing.T, repository Repository, executor Executor) (*Worker, *Registry) {
	t.Helper()
	registry, err := NewRegistry(testDefinition(executor))
	if err != nil {
		t.Fatal(err)
	}
	worker, err := NewWorker(repository, registry, WorkerConfig{Owner: "test-worker", Concurrency: 2, PollInterval: 10 * time.Millisecond, DatabaseBackoff: 20 * time.Millisecond, ShutdownGrace: time.Second, Retry: BackoffPolicy{Initial: time.Second, Maximum: 10 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	return worker, registry
}

func TestWorkerMapsClosedExecuteResultsWithoutArbitraryErrorMaterial(t *testing.T) {
	tests := []struct {
		name       string
		result     ExecuteResult
		attempt    int
		wantStatus Status
		wantEvent  EventType
		wantReason string
		wantCode   string
	}{
		{"verify", ExecuteResult{Disposition: ExecuteNeedsVerification}, 1, StatusVerifying, EventVerification, "", ""},
		{"unknown", ExecuteResult{Disposition: ExecuteResultUnknown}, 1, StatusVerifying, EventVerification, "", "execution_result_unknown"},
		{"retry", ExecuteResult{Disposition: ExecuteRetryableNoEffect, ErrorCode: "synthetic_failure"}, 1, StatusRetryWait, EventRetryScheduled, ReasonExecuteRetryableNoEffect, "synthetic_failure"},
		{"retry exhausted", ExecuteResult{Disposition: ExecuteRetryableNoEffect}, 3, StatusFailed, EventFailed, ReasonExecuteRetryableNoEffect, "max_attempts_exhausted"},
		{"permanent", ExecuteResult{Disposition: ExecutePermanentFailure}, 1, StatusFailed, EventFailed, "", "permanent_execution_failure"},
		{"invalid", ExecuteResult{Disposition: "arbitrary"}, 1, StatusFailed, EventFailed, "", "invalid_executor_result"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			repository := &fakeRepository{}
			executor := &fakeExecutor{execute: func(context.Context, Execution) ExecuteResult { return testCase.result }}
			worker, registry := newTestWorker(t, repository, executor)
			lease := leasedJob(t, registry, StatusRunning, testCase.attempt)
			worker.execute(context.Background(), lease)
			transition := repository.lastTransition(t)
			if transition.To != testCase.wantStatus || transition.Event != testCase.wantEvent || transition.ReasonCode != testCase.wantReason || transition.ErrorCode != testCase.wantCode {
				t.Fatalf("transition = %+v", transition)
			}
			if testCase.wantStatus == StatusVerifying && !transition.ReleaseLease {
				t.Fatal("verifying transition retained the execution lease")
			}
			if testCase.wantStatus == StatusRetryWait && transition.RetryAfter != time.Second {
				t.Fatalf("retry delay = %s", transition.RetryAfter)
			}
		})
	}
}

func TestBackoffIsExponentiallyBoundedAcrossRestartInputs(t *testing.T) {
	policy := BackoffPolicy{Initial: time.Second, Maximum: 8 * time.Second, Jitter: func(limit time.Duration) time.Duration { return limit * 2 }}
	if delay := policy.Delay(1); delay != 1250*time.Millisecond {
		t.Fatalf("attempt 1 delay = %s", delay)
	}
	if delay := policy.Delay(4); delay != 8*time.Second {
		t.Fatalf("attempt 4 delay = %s, exceeded cap", delay)
	}
	if delay := policy.Delay(100); delay != 8*time.Second {
		t.Fatalf("restarted high-attempt delay = %s, want persistent cap", delay)
	}
}

func TestProductionBackoffRandomJitterIsConcurrentAndBounded(t *testing.T) {
	policy := NewBackoffPolicy(time.Second, 8*time.Second)
	if policy.Jitter == nil {
		t.Fatal("production backoff has no jitter")
	}
	const workers = 64
	delays := make(chan time.Duration, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			delays <- policy.Delay(2)
		}()
	}
	group.Wait()
	close(delays)
	for delay := range delays {
		if delay < 2*time.Second || delay > 2500*time.Millisecond {
			t.Fatalf("randomized delay outside bound: %s", delay)
		}
	}
}

func TestWorkerFailsClosedBeforeExecutorForCorruptPayload(t *testing.T) {
	repository := &fakeRepository{}
	executor := &fakeExecutor{}
	worker, registry := newTestWorker(t, repository, executor)
	lease := leasedJob(t, registry, StatusRunning, 1)
	lease.PayloadHash[0] ^= 0xff
	worker.execute(context.Background(), lease)
	if executor.executions.Load() != 0 {
		t.Fatal("executor ran for corrupt payload")
	}
	if transition := repository.lastTransition(t); transition.To != StatusFailed || transition.ErrorCode != "payload_integrity_failed" {
		t.Fatalf("transition = %+v", transition)
	}
}

func TestWorkerFailsClosedBeforeExecutorForPersistedPolicyMismatch(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		mutate func(*Lease)
	}{
		{name: "lease", mutate: func(lease *Lease) { lease.LeaseDuration++ }},
		{name: "heartbeat", mutate: func(lease *Lease) { lease.HeartbeatInterval++ }},
		{name: "verification budget", mutate: func(lease *Lease) { lease.MaxVerifyAttempts++ }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repository := &fakeRepository{}
			executor := &fakeExecutor{}
			worker, registry := newTestWorker(t, repository, executor)
			lease := leasedJob(t, registry, StatusRunning, 1)
			testCase.mutate(&lease)

			worker.execute(context.Background(), lease)

			if executor.executions.Load() != 0 {
				t.Fatal("executor ran with mismatched persisted policy")
			}
			if transition := repository.lastTransition(t); transition.To != StatusFailed || transition.ErrorCode != "job_policy_mismatch" {
				t.Fatalf("transition = %+v", transition)
			}
		})
	}
}

func TestWorkerRenewalFailureCancelsExecutionAndLeavesLeaseForReconciler(t *testing.T) {
	repository := &fakeRepository{renewErrors: []error{nil, ErrLostLease}}
	cancelled := make(chan struct{})
	executor := &fakeExecutor{execute: func(ctx context.Context, _ Execution) ExecuteResult {
		<-ctx.Done()
		close(cancelled)
		return ExecuteResult{Disposition: ExecuteResultUnknown}
	}}
	definition := testDefinition(executor)
	registry, err := NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := NewWorker(repository, registry, WorkerConfig{Owner: "worker-renew", Concurrency: 1, PollInterval: time.Second, DatabaseBackoff: time.Second, ShutdownGrace: time.Second, Clock: eagerClock{}})
	if err != nil {
		t.Fatal(err)
	}
	worker.execute(context.Background(), leasedJob(t, registry, StatusRunning, 1))
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("executor context was not cancelled")
	}
	if len(repository.transitions) != 0 {
		t.Fatalf("lost worker committed transition: %+v", repository.transitions)
	}
}

func TestWorkerInitialRenewFailureNeverInvokesExecutor(t *testing.T) {
	repository := &fakeRepository{renewErr: ErrLostLease}
	executor := &fakeExecutor{}
	worker, registry := newTestWorker(t, repository, executor)

	worker.execute(context.Background(), leasedJob(t, registry, StatusRunning, 1))

	if executor.executions.Load() != 0 {
		t.Fatal("executor ran after the pre-execution fenced renewal failed")
	}
	if len(repository.transitions) != 0 {
		t.Fatal("worker transitioned a job after the pre-execution renewal failed")
	}
}

func TestStaleFencingTokenCannotCommit(t *testing.T) {
	valid := uuid.New()
	repository := &fakeRepository{validToken: valid}
	executor := &fakeExecutor{execute: func(context.Context, Execution) ExecuteResult {
		return ExecuteResult{Disposition: ExecutePermanentFailure}
	}}
	worker, registry := newTestWorker(t, repository, executor)
	lease := leasedJob(t, registry, StatusRunning, 1)
	lease.Token = uuid.New()
	worker.execute(context.Background(), lease)
	if len(repository.transitions) != 0 {
		t.Fatal("stale fencing token committed")
	}
}

func TestWorkerGracefulStopCancelsActiveExecutionAndStopsClaims(t *testing.T) {
	repository := &fakeRepository{}
	started := make(chan struct{})
	stopped := make(chan struct{})
	executor := &fakeExecutor{execute: func(ctx context.Context, _ Execution) ExecuteResult {
		close(started)
		<-ctx.Done()
		close(stopped)
		return ExecuteResult{Disposition: ExecuteResultUnknown}
	}}
	worker, registry := newTestWorker(t, repository, executor)
	repository.claims = []Lease{leasedJob(t, registry, StatusRunning, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	<-started
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("active executor was not stopped")
	}
	repository.mu.Lock()
	claimCalls := repository.claimCalls
	repository.mu.Unlock()
	time.Sleep(20 * time.Millisecond)
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.claimCalls != claimCalls {
		t.Fatal("worker continued claiming after shutdown")
	}
}

func TestWorkerConcurrencyIsBounded(t *testing.T) {
	repository := &fakeRepository{}
	var current atomic.Int32
	var maximum atomic.Int32
	release := make(chan struct{})
	started := make(chan struct{}, 8)
	executor := &fakeExecutor{execute: func(ctx context.Context, _ Execution) ExecuteResult {
		active := current.Add(1)
		defer current.Add(-1)
		for {
			observed := maximum.Load()
			if active <= observed || maximum.CompareAndSwap(observed, active) {
				break
			}
		}
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return ExecuteResult{Disposition: ExecuteNeedsVerification}
	}}
	worker, registry := newTestWorker(t, repository, executor)
	for range 5 {
		repository.claims = append(repository.claims, leasedJob(t, registry, StatusRunning, 1))
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("worker did not fill configured concurrency")
		}
	}
	select {
	case <-started:
		t.Fatal("worker exceeded configured concurrency")
	case <-time.After(30 * time.Millisecond):
	}
	cancel()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if maximum.Load() != 2 {
		t.Fatalf("maximum concurrency = %d, want 2", maximum.Load())
	}
}

func TestReconcilerClosedVerificationMatrixNeverExecutes(t *testing.T) {
	tests := []struct {
		name       string
		result     VerifyResult
		configure  func(*Definition, *Lease)
		wantStatus Status
		wantCode   string
	}{
		{"applied", VerifyResult{Disposition: VerifyEffectApplied}, nil, StatusSucceeded, ""},
		{"absent replay", VerifyResult{Disposition: VerifyEffectAbsent}, nil, StatusRetryWait, ""},
		{"partial rollback", VerifyResult{Disposition: VerifyEffectPartial}, nil, StatusRollingBack, ""},
		{"unknown retry", VerifyResult{Disposition: VerifyEffectUnknown}, nil, StatusVerifying, "effect_unknown"},
		{"unknown exhausted", VerifyResult{Disposition: VerifyEffectUnknown}, func(_ *Definition, lease *Lease) { lease.VerificationAttempt = 2 }, StatusFailed, "verification_exhausted"},
		{"unknown past deadline", VerifyResult{Disposition: VerifyEffectUnknown}, func(_ *Definition, lease *Lease) { lease.DeadlineExceeded = true }, StatusFailed, "job_deadline_exceeded"},
		{"cancel absent", VerifyResult{Disposition: VerifyEffectAbsent}, func(_ *Definition, lease *Lease) { lease.CancelRequested = true }, StatusCancelled, "cancel_verified_safe"},
		{"partial no rollback", VerifyResult{Disposition: VerifyEffectPartial}, func(definition *Definition, _ *Lease) { definition.AllowRollback = false }, StatusFailed, "rollback_not_permitted"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			repository := &fakeRepository{}
			executor := &fakeExecutor{verify: func(context.Context, Execution) VerifyResult { return testCase.result }}
			definition := testDefinition(executor)
			registry, _ := NewRegistry(definition)
			lease := leasedJob(t, registry, StatusVerifying, 1)
			if testCase.configure != nil {
				testCase.configure(&definition, &lease)
				registry, _ = NewRegistry(definition)
				lease.Timeout = definition.Timeout
				lease.LeaseDuration = definition.LeaseDuration
				lease.HeartbeatInterval = definition.HeartbeatInterval
				lease.MaxVerifyAttempts = definition.MaxVerifyAttempts
				lease.ReplaySafe = definition.ReplaySafe
				lease.AllowRollback = definition.AllowRollback
			}
			reconciler, err := NewReconciler(repository, registry, ReconcilerConfig{Owner: "test-reconciler", Concurrency: 1, PollInterval: time.Second, DatabaseBackoff: time.Second, ShutdownGrace: time.Second, Retry: BackoffPolicy{Initial: time.Second, Maximum: time.Minute}})
			if err != nil {
				t.Fatal(err)
			}
			reconciler.reconcile(context.Background(), lease)
			transition := repository.lastTransition(t)
			if transition.To != testCase.wantStatus || transition.ErrorCode != testCase.wantCode {
				t.Fatalf("transition = %+v", transition)
			}
			if executor.executions.Load() != 0 || executor.verifications.Load() != 1 {
				t.Fatalf("execute=%d verify=%d", executor.executions.Load(), executor.verifications.Load())
			}
		})
	}
}

func TestVerifiedSuccessMutationRunsAfterFencedTransitionInSameRepositoryTransaction(t *testing.T) {
	repository := &fakeRepository{}
	var mutationCalled atomic.Bool
	executor := &fakeExecutor{verify: func(context.Context, Execution) VerifyResult {
		return VerifyResult{Disposition: VerifyEffectApplied, Mutation: func(_ context.Context, tx DBTX) error {
			if tx == nil {
				t.Fatal("mutation did not receive transaction-bound DBTX")
			}
			if len(repository.transitions) != 1 || repository.transitions[0].To != StatusSucceeded {
				t.Fatal("mutation ran before the fenced success transition")
			}
			mutationCalled.Store(true)
			return nil
		}}
	}}
	definition := testDefinition(executor)
	registry, err := NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	lease := leasedJob(t, registry, StatusVerifying, 1)
	reconciler, err := NewReconciler(repository, registry, ReconcilerConfig{
		Owner: "mutation-reconciler", Concurrency: 1, PollInterval: time.Second,
		DatabaseBackoff: time.Second, ShutdownGrace: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	reconciler.reconcile(context.Background(), lease)

	if !mutationCalled.Load() {
		t.Fatal("verified business mutation was not called")
	}
	if transition := repository.lastTransition(t); transition.Mutation == nil || transition.To != StatusSucceeded {
		t.Fatalf("transition = %+v", transition)
	}
}

func TestMutationFailureRollsBackFencedTransitionAndEvent(t *testing.T) {
	repository := &fakeRepository{}
	var mutationCalled atomic.Bool
	executor := &fakeExecutor{verify: func(context.Context, Execution) VerifyResult {
		return VerifyResult{Disposition: VerifyEffectApplied, Mutation: func(context.Context, DBTX) error {
			mutationCalled.Store(true)
			return errors.New("synthetic business confirmation failure")
		}}
	}}
	definition := testDefinition(executor)
	registry, _ := NewRegistry(definition)
	lease := leasedJob(t, registry, StatusVerifying, 1)
	reconciler, _ := NewReconciler(repository, registry, ReconcilerConfig{
		Owner: "rollback-mutation-reconciler", Concurrency: 1, PollInterval: time.Second,
		DatabaseBackoff: time.Second, ShutdownGrace: time.Second,
	})

	reconciler.reconcile(context.Background(), lease)

	if !mutationCalled.Load() {
		t.Fatal("business mutation was not attempted")
	}
	if len(repository.transitions) != 0 {
		t.Fatalf("fenced transition survived mutation rollback: %+v", repository.transitions)
	}
}

func TestIllegalVerificationMutationFailsClosedWithoutCallingMutation(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		result    VerifyResult
		configure func(*Lease)
	}{
		{name: "effect absent", result: VerifyResult{Disposition: VerifyEffectAbsent}},
		{name: "effect applied while cancellation diverts to rollback", result: VerifyResult{Disposition: VerifyEffectApplied}, configure: func(lease *Lease) { lease.CancelRequested = true }},
		{name: "effect unknown", result: VerifyResult{Disposition: VerifyEffectUnknown}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repository := &fakeRepository{}
			var mutationCalled atomic.Bool
			testCase.result.Mutation = func(context.Context, DBTX) error {
				mutationCalled.Store(true)
				return nil
			}
			definition := testDefinition(&fakeExecutor{})
			registry, _ := NewRegistry(definition)
			lease := leasedJob(t, registry, StatusVerifying, 1)
			if testCase.configure != nil {
				testCase.configure(&lease)
			}
			reconciler, _ := NewReconciler(repository, registry, ReconcilerConfig{
				Owner: "invalid-mutation-reconciler", Concurrency: 1, PollInterval: time.Second,
				DatabaseBackoff: time.Second, ShutdownGrace: time.Second,
			})

			reconciler.applyVerification(context.Background(), lease, definition, testCase.result)

			if mutationCalled.Load() {
				t.Fatal("illegal verification mutation was called")
			}
			transition := repository.lastTransition(t)
			if transition.To != StatusFailed || transition.Event != EventFailed || transition.ErrorCode != "invalid_transition_mutation" || transition.Mutation != nil {
				t.Fatalf("transition = %+v", transition)
			}
		})
	}
}

func TestReconcilerRenewalFailureCancelsVerifyWithoutTransition(t *testing.T) {
	repository := &fakeRepository{renewErrors: []error{nil, ErrLostLease}}
	cancelled := make(chan struct{})
	executor := &fakeExecutor{verify: func(ctx context.Context, _ Execution) VerifyResult {
		<-ctx.Done()
		close(cancelled)
		return VerifyResult{Disposition: VerifyEffectUnknown}
	}}
	definition := testDefinition(executor)
	registry, err := NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	lease := leasedJob(t, registry, StatusVerifying, 1)
	reconciler, err := NewReconciler(repository, registry, ReconcilerConfig{Owner: "renew-reconciler", Concurrency: 1, PollInterval: time.Second, DatabaseBackoff: time.Second, ShutdownGrace: time.Second, Clock: eagerClock{}})
	if err != nil {
		t.Fatal(err)
	}
	reconciler.reconcile(context.Background(), lease)
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("verify context was not cancelled after lost lease")
	}
	if len(repository.transitions) != 0 {
		t.Fatal("reconciler committed after losing its fencing lease")
	}
}

func TestReconcilerInitialRenewFailureNeverInvokesExecutor(t *testing.T) {
	repository := &fakeRepository{renewErr: ErrLostLease}
	executor := &fakeExecutor{}
	definition := testDefinition(executor)
	registry, err := NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	lease := leasedJob(t, registry, StatusVerifying, 1)
	reconciler, err := NewReconciler(repository, registry, ReconcilerConfig{
		Owner: "initial-renew-reconciler", Concurrency: 1, PollInterval: time.Second,
		DatabaseBackoff: time.Second, ShutdownGrace: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	reconciler.reconcile(context.Background(), lease)

	if executor.verifications.Load() != 0 || executor.rollbacks.Load() != 0 {
		t.Fatal("reconciler invoked executor after the pre-operation fenced renewal failed")
	}
	if len(repository.transitions) != 0 {
		t.Fatal("reconciler transitioned a job after the pre-operation renewal failed")
	}
}

func TestReconcilerFailsClosedBeforeVerifyForPersistedPolicyMismatch(t *testing.T) {
	repository := &fakeRepository{}
	executor := &fakeExecutor{}
	definition := testDefinition(executor)
	registry, err := NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	lease := leasedJob(t, registry, StatusVerifying, 1)
	lease.ReplaySafe = !definition.ReplaySafe
	reconciler, err := NewReconciler(repository, registry, ReconcilerConfig{
		Owner: "policy-reconciler", Concurrency: 1, PollInterval: time.Second,
		DatabaseBackoff: time.Second, ShutdownGrace: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	reconciler.reconcile(context.Background(), lease)

	if executor.verifications.Load() != 0 || executor.rollbacks.Load() != 0 {
		t.Fatal("reconciler invoked executor with mismatched persisted policy")
	}
	if transition := repository.lastTransition(t); transition.To != StatusFailed || transition.ErrorCode != "job_policy_mismatch" {
		t.Fatalf("transition = %+v", transition)
	}
}

func TestReconcilerRollbackMatrix(t *testing.T) {
	for _, testCase := range []struct {
		result RollbackResult
		want   Status
	}{
		{RollbackResult{Disposition: RollbackCompleted}, StatusRolledBack},
		{RollbackResult{Disposition: RollbackRetryable}, StatusRollingBack},
		{RollbackResult{Disposition: RollbackFailed}, StatusFailed},
	} {
		repository := &fakeRepository{}
		executor := &fakeExecutor{rollback: func(context.Context, Execution) RollbackResult { return testCase.result }}
		definition := testDefinition(executor)
		registry, _ := NewRegistry(definition)
		lease := leasedJob(t, registry, StatusRollingBack, 1)
		reconciler, _ := NewReconciler(repository, registry, ReconcilerConfig{Owner: "rollback-reconciler", Concurrency: 1, PollInterval: time.Second, DatabaseBackoff: time.Second, ShutdownGrace: time.Second, Retry: BackoffPolicy{Initial: time.Second, Maximum: time.Minute}})
		reconciler.reconcile(context.Background(), lease)
		if transition := repository.lastTransition(t); transition.To != testCase.want {
			t.Fatalf("rollback transition = %+v", transition)
		}
		if executor.executions.Load() != 0 || executor.rollbacks.Load() != 1 {
			t.Fatal("rollback path executed the external operation")
		}
	}
}

func TestCompletedRollbackMutationIsAtomicAndRetryMutationFailsClosed(t *testing.T) {
	definition := testDefinition(&fakeExecutor{})
	registry, _ := NewRegistry(definition)
	lease := leasedJob(t, registry, StatusRollingBack, 1)

	repository := &fakeRepository{}
	var completedCalled atomic.Bool
	reconciler, _ := NewReconciler(repository, registry, ReconcilerConfig{
		Owner: "rollback-complete-reconciler", Concurrency: 1, PollInterval: time.Second,
		DatabaseBackoff: time.Second, ShutdownGrace: time.Second,
	})
	reconciler.applyRollback(context.Background(), lease, definition, RollbackResult{
		Disposition: RollbackCompleted,
		Mutation: func(context.Context, DBTX) error {
			completedCalled.Store(true)
			return nil
		},
	})
	if !completedCalled.Load() {
		t.Fatal("completed rollback mutation was not called")
	}
	if transition := repository.lastTransition(t); transition.To != StatusRolledBack || transition.Mutation == nil {
		t.Fatalf("transition = %+v", transition)
	}

	repository = &fakeRepository{}
	var retryCalled atomic.Bool
	reconciler.repository = repository
	reconciler.applyRollback(context.Background(), lease, definition, RollbackResult{
		Disposition: RollbackRetryable,
		Mutation: func(context.Context, DBTX) error {
			retryCalled.Store(true)
			return nil
		},
	})
	if retryCalled.Load() {
		t.Fatal("retry rollback mutation was called")
	}
	if transition := repository.lastTransition(t); transition.To != StatusFailed || transition.ErrorCode != "invalid_transition_mutation" || transition.Mutation != nil {
		t.Fatalf("transition = %+v", transition)
	}
}

func TestTransitionValidateRejectsMutationOutsideAtomicTerminalPaths(t *testing.T) {
	mutation := func(context.Context, DBTX) error { return nil }
	valid := []Transition{
		{From: []Status{StatusVerifying}, To: StatusSucceeded, Event: EventSucceeded, Actor: ActorReconciler, ReleaseLease: true, Mutation: mutation},
		{From: []Status{StatusRollingBack}, To: StatusRolledBack, Event: EventRolledBack, Actor: ActorReconciler, ReleaseLease: true, Mutation: mutation},
		{From: []Status{StatusRunning}, To: StatusVerifying, Event: EventVerification, Actor: ActorWorker, ReleaseLease: true},
	}
	for _, transition := range valid {
		if err := transition.Validate(); err != nil {
			t.Fatalf("valid transition rejected: %+v: %v", transition, err)
		}
	}
	invalid := []Transition{
		{From: []Status{StatusRunning}, To: StatusSucceeded, Event: EventSucceeded, Actor: ActorWorker, ReleaseLease: true, Mutation: mutation},
		{From: []Status{StatusVerifying}, To: StatusSucceeded, Event: EventSucceeded, Actor: ActorReconciler, ReleaseLease: false, Mutation: mutation},
		{From: []Status{StatusVerifying}, To: StatusFailed, Event: EventFailed, Actor: ActorReconciler, ReleaseLease: true, Mutation: mutation},
		{From: []Status{StatusRollingBack}, To: StatusRolledBack, Event: EventRolledBack, Actor: ActorWorker, ReleaseLease: true, Mutation: mutation},
	}
	for _, transition := range invalid {
		if err := transition.Validate(); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("invalid mutation transition accepted: %+v: %v", transition, err)
		}
	}
}

type fakePublisher struct {
	err       error
	envelopes []WakeEnvelope
}

func (publisher *fakePublisher) Publish(_ context.Context, envelope WakeEnvelope) error {
	publisher.envelopes = append(publisher.envelopes, envelope)
	return publisher.err
}

func validOutboxLease() OutboxLease {
	return OutboxLease{Envelope: WakeEnvelope{SchemaVersion: 1, EventID: uuid.New(), JobID: uuid.New(), OperationID: uuid.New(), Topic: "async_job_wake"}, Status: OutboxPublishing, Attempt: 1, MaxAttempts: 3, Token: uuid.New()}
}

func TestDispatcherAtLeastOnceAndBoundedFailure(t *testing.T) {
	repository := &fakeRepository{}
	publisher := &fakePublisher{}
	dispatcher, err := NewDispatcher(repository, publisher, DispatcherConfig{Owner: "test-dispatcher", Concurrency: 1, PollInterval: time.Second, DatabaseBackoff: time.Second, LeaseDuration: time.Minute, PublishTimeout: time.Second, ShutdownGrace: time.Second, Retry: BackoffPolicy{Initial: time.Second, Maximum: time.Minute}})
	if err != nil {
		t.Fatal(err)
	}
	lease := validOutboxLease()
	dispatcher.dispatch(context.Background(), lease)
	if len(publisher.envelopes) != 1 || repository.outboxTransitions[0].To != OutboxSent {
		t.Fatalf("publish=%d transition=%+v", len(publisher.envelopes), repository.outboxTransitions)
	}
	// A crash before the fenced sent transition permits the same event ID to be
	// published again; consumers only use it as a wake signal.
	dispatcher.dispatch(context.Background(), lease)
	if len(publisher.envelopes) != 2 || publisher.envelopes[0].EventID != publisher.envelopes[1].EventID {
		t.Fatal("at-least-once duplicate did not retain event identity")
	}

	repository.outboxTransitions = nil
	publisher.err = errors.New("raw upstream response secret-canary")
	dispatcher.dispatch(context.Background(), lease)
	transition := repository.outboxTransitions[0]
	if transition.To != OutboxRetryWait || transition.ErrorCode != "publisher_unavailable" {
		t.Fatalf("publisher failure transition = %+v", transition)
	}
	if strings.Contains(transition.ErrorCode, "canary") {
		t.Fatal("raw publisher error persisted")
	}
	lease.Attempt = lease.MaxAttempts
	dispatcher.dispatch(context.Background(), lease)
	if repository.outboxTransitions[len(repository.outboxTransitions)-1].To != OutboxFailed {
		t.Fatal("publisher failure did not stop at max attempts")
	}
}

func TestDispatcherRejectsPublishTimeoutAtLeaseBoundary(t *testing.T) {
	_, err := NewDispatcher(&fakeRepository{}, &fakePublisher{}, DispatcherConfig{
		Owner: "unsafe-dispatcher", Concurrency: 1, PollInterval: time.Second,
		DatabaseBackoff: time.Second, LeaseDuration: time.Second, PublishTimeout: time.Second,
		ShutdownGrace: time.Second,
	})
	if err == nil {
		t.Fatal("dispatcher accepted publish timeout at lease boundary")
	}
}

type lateSuccessPublisher struct{ delay time.Duration }

func (publisher lateSuccessPublisher) Publish(context.Context, WakeEnvelope) error {
	time.Sleep(publisher.delay)
	return nil
}

func TestDispatcherDoesNotMarkLatePublisherSuccessSent(t *testing.T) {
	repository := &fakeRepository{}
	dispatcher, err := NewDispatcher(repository, lateSuccessPublisher{delay: 20 * time.Millisecond}, DispatcherConfig{
		Owner: "late-dispatcher", Concurrency: 1, PollInterval: time.Second,
		DatabaseBackoff: time.Second, LeaseDuration: 50 * time.Millisecond, PublishTimeout: 5 * time.Millisecond,
		ShutdownGrace: time.Second, Retry: BackoffPolicy{Initial: time.Second, Maximum: time.Minute},
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher.dispatch(context.Background(), validOutboxLease())
	if transition := repository.outboxTransitions[0]; transition.To != OutboxRetryWait || transition.ErrorCode != "publisher_unavailable" {
		t.Fatalf("late publisher transition = %+v", transition)
	}
}

type countingWakeTarget struct{ calls atomic.Int32 }

func (target *countingWakeTarget) Wake() { target.calls.Add(1) }

func TestWakeConsumerOnlyRequestsDatabaseScan(t *testing.T) {
	target := &countingWakeTarget{}
	envelope := validOutboxLease().Envelope
	if !ConsumeWake(envelope, target) || !ConsumeWake(envelope, target) {
		t.Fatal("valid duplicate wake rejected")
	}
	if target.calls.Load() != 2 {
		t.Fatalf("wake count = %d", target.calls.Load())
	}
	envelope.Topic = "arbitrary_execute_command"
	if ConsumeWake(envelope, target) || target.calls.Load() != 2 {
		t.Fatal("forged wake triggered execution path")
	}
}

type staticMetrics struct{ snapshot MetricsSnapshot }

func (metrics staticMetrics) JobMetricsSnapshot(context.Context) (MetricsSnapshot, error) {
	return metrics.snapshot, nil
}

func TestCollectorHasOnlyClosedStatusLabel(t *testing.T) {
	snapshot := MetricsSnapshot{JobsByStatus: map[Status]int64{StatusPending: 2, StatusSucceeded: 1}, OldestPendingSeconds: 4, ExpiredLeases: 1, OldestOutboxSeconds: 3}
	collector, err := NewCollector(staticMetrics{snapshot: snapshot})
	if err != nil {
		t.Fatal(err)
	}
	registry := prometheus.NewPedanticRegistry()
	if err := registry.Register(collector); err != nil {
		t.Fatal(err)
	}
	families, err := registry.Gather()
	if err != nil || len(families) != 4 {
		t.Fatalf("gather count=%d err=%v", len(families), err)
	}
	for _, family := range families {
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				if label.GetName() != "status" {
					t.Fatalf("unexpected metric label %q", label.GetName())
				}
			}
		}
	}
}

type blockingMetrics struct{}

func (blockingMetrics) JobMetricsSnapshot(ctx context.Context) (MetricsSnapshot, error) {
	<-ctx.Done()
	return MetricsSnapshot{}, ctx.Err()
}

func TestCollectorBoundsBlockedRepository(t *testing.T) {
	collector, err := NewCollector(blockingMetrics{})
	if err != nil {
		t.Fatal(err)
	}
	registry := prometheus.NewRegistry()
	if err := registry.Register(collector); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err = registry.Gather()
	if err == nil {
		t.Fatal("blocked metrics provider did not produce an invalid metric")
	}
	if elapsed := time.Since(started); elapsed > 2500*time.Millisecond {
		t.Fatalf("metrics collection exceeded fixed timeout: %s", elapsed)
	}
}

func TestCollectorRejectsUnknownStatusDimension(t *testing.T) {
	collector, _ := NewCollector(staticMetrics{snapshot: MetricsSnapshot{JobsByStatus: map[Status]int64{"attacker_kind": 1}}})
	registry := prometheus.NewRegistry()
	if err := registry.Register(collector); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Gather(); err == nil {
		t.Fatal("unknown status metric dimension accepted")
	}
}

func TestStructuredLogContractRejectsUnregisteredDimensions(t *testing.T) {
	registry, _ := NewRegistry(testDefinition(nil))
	valid := LogRecord{Component: ComponentWorker, Action: "claim", Result: ResultSuccess, JobKind: "test.synthetic"}
	if !valid.Valid(registry) {
		t.Fatal("valid fixed log record rejected")
	}
	for _, invalid := range []LogRecord{
		{Component: "payload-canary", Action: "claim", Result: ResultFailure},
		{Component: ComponentWorker, Action: "claim", Result: ResultFailure, JobKind: "attacker-kind"},
		{Component: ComponentWorker, Action: "https://secret.example", Result: ResultFailure},
		{Component: ComponentWorker, Action: "claim", Result: "job-id-canary"},
		{Component: ComponentWorker, Action: ActionExecute, Result: ResultFailure, JobKind: "test.synthetic", ErrorCode: "secret_canary"},
		{Component: ComponentWorker, Action: ActionExecute, Result: ResultFailure, JobKind: "test.synthetic", ErrorCode: ReasonEffectUnknownUnverified},
		{Component: ComponentWorker, Action: ActionExecute, Result: ResultFailure, JobKind: "test.synthetic", ErrorCode: ReasonExecuteRetryableNoEffect},
		{Component: ComponentWorker, Action: ActionExecute, Result: ResultFailure, JobKind: "test.synthetic", ErrorCode: ReasonEffectAbsentVerified},
	} {
		if invalid.Valid(registry) {
			t.Fatalf("unsafe log record accepted: %+v", invalid)
		}
	}
}

func TestRuntimeLoggingNeverForwardsExecutorPublisherOrDatabaseErrors(t *testing.T) {
	logger := &captureLogger{}
	canary := "secret-canary-raw-response"

	workerRepository := &fakeRepository{transitionErr: errors.New(canary)}
	workerExecutor := &fakeExecutor{execute: func(context.Context, Execution) ExecuteResult {
		return ExecuteResult{Disposition: ExecutePermanentFailure, ErrorCode: "secret_canary"}
	}}
	worker, registry := newTestWorker(t, workerRepository, workerExecutor)
	worker.logger = logger
	worker.execute(context.Background(), leasedJob(t, registry, StatusRunning, 1))

	publisherRepository := &fakeRepository{}
	dispatcher, err := NewDispatcher(publisherRepository, &fakePublisher{err: errors.New(canary)}, DispatcherConfig{
		Owner: "logging-dispatcher", Concurrency: 1, PollInterval: time.Second,
		DatabaseBackoff: time.Second, LeaseDuration: time.Minute, PublishTimeout: time.Second,
		ShutdownGrace: time.Second, Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher.dispatch(context.Background(), validOutboxLease())

	records := logger.snapshot()
	if len(records) != 2 {
		t.Fatalf("log record count = %d, want 2: %+v", len(records), records)
	}
	for _, record := range records {
		serialized := string(record.Component) + string(record.Action) + string(record.Result) + record.JobKind + record.ErrorCode
		if strings.Contains(serialized, canary) || strings.Contains(serialized, "secret_canary") {
			t.Fatalf("raw error material reached structured log: %+v", record)
		}
		if !record.Valid(registry) && record.Component != ComponentDispatcher {
			t.Fatalf("runtime emitted invalid log record: %+v", record)
		}
	}
	if records[0].ErrorCode != "database_unavailable" || records[1].ErrorCode != "publisher_unavailable" {
		t.Fatalf("errors were not mapped to fixed codes: %+v", records)
	}
}
