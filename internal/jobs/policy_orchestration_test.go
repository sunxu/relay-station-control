package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func policyWorker(t *testing.T, repository *fakeRepository, definition Definition) (*Worker, *Registry) {
	t.Helper()
	registry, err := NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := NewWorker(repository, registry, WorkerConfig{
		Owner: "policy-worker", Concurrency: 1, PollInterval: time.Second,
		DatabaseBackoff: time.Second, ShutdownGrace: time.Second,
		Retry: BackoffPolicy{Initial: time.Second, Maximum: time.Minute, Jitter: func(time.Duration) time.Duration { return 0 }},
	})
	if err != nil {
		t.Fatal(err)
	}
	return worker, registry
}

func TestExecutionPolicyInvariantAndCatalogSnapshot(t *testing.T) {
	definition := testDefinition(nil)
	definition.AllowUnknownEffectReplay = true
	definition.ReplaySafe = false
	if _, err := NewRegistry(definition); err == nil {
		t.Fatal("unknown-effect replay without replay-safe was accepted")
	}
	definition.ReplaySafe = true
	definition.AllowDirectSuccess = true
	registry, err := NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	catalog := registry.Catalog()
	if len(catalog) != 1 || !catalog[0].AllowUnknownEffectReplay || !catalog[0].AllowDirectSuccess {
		t.Fatalf("catalog policy = %+v", catalog)
	}
	tx := &memoryTx{jobs: map[string]Job{}}
	request := EnqueueRequest{Kind: definition.Kind, SchemaVersion: 1, OperationID: uuid.New(), IdempotencyKey: "policy:snapshot", Payload: validPayload(), Actor: ActorService}
	created, err := EnqueueTx(context.Background(), tx, registry, request)
	if err != nil || !created.Created || !created.Job.AllowUnknownEffectReplay || !created.Job.AllowDirectSuccess {
		t.Fatalf("enqueue snapshot = %+v, err=%v", created.Job, err)
	}
	if _, err = EnqueueTx(context.Background(), tx, registry, request); err != nil {
		t.Fatalf("identical enqueue was not idempotent: %v", err)
	}
	tx.jobs[request.IdempotencyKey] = created.Job
	tx.jobs[request.IdempotencyKey] = func(job Job) Job { job.AllowDirectSuccess = false; return job }(tx.jobs[request.IdempotencyKey])
	if _, err = EnqueueTx(context.Background(), tx, registry, request); !errors.Is(err, ErrConflict) {
		t.Fatalf("policy mismatch enqueue error = %v", err)
	}
	tx.jobs[request.IdempotencyKey] = created.Job
	unknownChanged := definition
	unknownChanged.AllowUnknownEffectReplay = false
	unknownRegistry, err := NewRegistry(unknownChanged)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = EnqueueTx(context.Background(), tx, unknownRegistry, request); !errors.Is(err, ErrConflict) {
		t.Fatalf("unknown replay policy mismatch enqueue error = %v", err)
	}
	tx.jobs[request.IdempotencyKey] = created.Job
	changed := definition
	changed.MaxAttempts++
	// Restore the original direct-success snapshot so this case isolates
	// MaxAttempts compatibility rather than a prior policy mutation.
	changed.AllowDirectSuccess = definition.AllowDirectSuccess
	changedRegistry, err := NewRegistry(changed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = EnqueueTx(context.Background(), tx, changedRegistry, request); !errors.Is(err, ErrConflict) {
		t.Fatalf("MaxAttempts mismatch enqueue error = %v", err)
	}
}

func TestWorkerDirectSuccessRequiresPersistedPolicyAndFencing(t *testing.T) {
	definition := testDefinition(nil)
	definition.AllowDirectSuccess = true
	repository := &fakeRepository{}
	worker, registry := policyWorker(t, repository, definition)
	lease := leasedJob(t, registry, StatusRunning, 1)
	worker.applyExecuteResult(context.Background(), lease, definition, ExecuteResult{Disposition: ExecuteSucceeded})
	if got := repository.lastTransition(t); got.To != StatusSucceeded || got.Event != EventSucceeded || got.Actor != ActorWorker {
		t.Fatalf("direct success transition = %+v", got)
	}

	repository = &fakeRepository{}
	definition.AllowDirectSuccess = false
	worker, registry = policyWorker(t, repository, definition)
	lease = leasedJob(t, registry, StatusRunning, 1)
	worker.applyExecuteResult(context.Background(), lease, definition, ExecuteResult{Disposition: ExecuteSucceeded})
	if got := repository.lastTransition(t); got.To != StatusFailed || got.ErrorCode != "invalid_executor_result" {
		t.Fatalf("unauthorized direct success = %+v", got)
	}

	repository = &fakeRepository{validToken: uuid.New()}
	definition.AllowDirectSuccess = true
	worker, registry = policyWorker(t, repository, definition)
	lease = leasedJob(t, registry, StatusRunning, 1)
	worker.applyExecuteResult(context.Background(), lease, definition, ExecuteResult{Disposition: ExecuteSucceeded})
	if len(repository.transitions) != 0 {
		t.Fatal("stale fencing token committed direct success")
	}
}

func TestTransitionOutcomeDrivesWorkerAndReconcilerLogs(t *testing.T) {
	t.Run("worker direct success records committed failure", func(t *testing.T) {
		logger := &captureLogger{}
		repository := &fakeRepository{transitionOutcome: &TransitionOutcome{Status: StatusFailed, ErrorCode: "cancel_after_effect_applied"}}
		definition := testDefinition(&fakeExecutor{})
		definition.AllowDirectSuccess = true
		registry, err := NewRegistry(definition)
		if err != nil {
			t.Fatal(err)
		}
		worker, err := NewWorker(repository, registry, WorkerConfig{
			Owner: "outcome-worker", Concurrency: 1, PollInterval: time.Second,
			DatabaseBackoff: time.Second, ShutdownGrace: time.Second, Logger: logger,
		})
		if err != nil {
			t.Fatal(err)
		}
		lease := leasedJob(t, registry, StatusRunning, 1)
		worker.applyExecuteResult(context.Background(), lease, definition, ExecuteResult{Disposition: ExecuteSucceeded})
		records := logger.snapshot()
		if len(records) != 1 || records[0].Result != ResultFailure || records[0].ErrorCode != "cancel_after_effect_applied" {
			t.Fatalf("worker committed outcome log = %+v", records)
		}
	})

	t.Run("worker safe cancellation is skipped", func(t *testing.T) {
		logger := &captureLogger{}
		repository := &fakeRepository{transitionOutcome: &TransitionOutcome{Status: StatusCancelled, ErrorCode: "cancel_verified_safe"}}
		definition := testDefinition(&fakeExecutor{})
		registry, err := NewRegistry(definition)
		if err != nil {
			t.Fatal(err)
		}
		worker, err := NewWorker(repository, registry, WorkerConfig{
			Owner: "outcome-cancel-worker", Concurrency: 1, PollInterval: time.Second,
			DatabaseBackoff: time.Second, ShutdownGrace: time.Second, Logger: logger,
		})
		if err != nil {
			t.Fatal(err)
		}
		lease := leasedJob(t, registry, StatusRunning, 1)
		worker.applyExecuteResult(context.Background(), lease, definition, ExecuteResult{Disposition: ExecuteRetryableNoEffect})
		records := logger.snapshot()
		if len(records) != 1 || records[0].Result != ResultSkipped || records[0].ErrorCode != "cancel_verified_safe" {
			t.Fatalf("worker cancellation outcome log = %+v", records)
		}
	})

	t.Run("reconciler records committed unknown cancellation failure", func(t *testing.T) {
		logger := &captureLogger{}
		repository := &fakeRepository{transitionOutcome: &TransitionOutcome{Status: StatusFailed, ErrorCode: "cancel_after_unknown_effect"}}
		definition := testDefinition(&fakeExecutor{})
		registry, err := NewRegistry(definition)
		if err != nil {
			t.Fatal(err)
		}
		reconciler, err := NewReconciler(repository, registry, ReconcilerConfig{
			Owner: "outcome-reconciler", Concurrency: 1, PollInterval: time.Second,
			DatabaseBackoff: time.Second, ShutdownGrace: time.Second, Logger: logger,
		})
		if err != nil {
			t.Fatal(err)
		}
		lease := leasedJob(t, registry, StatusRunning, 1)
		requested := Transition{JobID: lease.ID, Token: lease.Token, From: []Status{StatusRunning}, To: StatusRetryWait, Event: EventRetryScheduled, Actor: ActorReconciler, ErrorCode: "execution_result_unknown", ReleaseLease: true}
		reconciler.transition(context.Background(), lease.Kind, ActionClaim, requested)
		records := logger.snapshot()
		if len(records) != 1 || records[0].Result != ResultFailure || records[0].ErrorCode != "cancel_after_unknown_effect" {
			t.Fatalf("reconciler committed outcome log = %+v", records)
		}
	})
}

func TestTransitionOutcomeRewritesRequestedRetryWait(t *testing.T) {
	t.Run("worker current unknown requested retry becomes committed failure", func(t *testing.T) {
		logger := &captureLogger{}
		repository := &fakeRepository{transitionOutcome: &TransitionOutcome{Status: StatusFailed, ErrorCode: "cancel_after_unknown_effect"}}
		definition := testDefinition(&fakeExecutor{})
		definition.AllowUnknownEffectReplay = true
		registry, err := NewRegistry(definition)
		if err != nil {
			t.Fatal(err)
		}
		worker, err := NewWorker(repository, registry, WorkerConfig{
			Owner: "unknown-outcome-worker", Concurrency: 1, PollInterval: time.Second,
			DatabaseBackoff: time.Second, ShutdownGrace: time.Second, Logger: logger,
		})
		if err != nil {
			t.Fatal(err)
		}
		lease := leasedJob(t, registry, StatusRunning, 1)
		worker.applyExecuteResult(context.Background(), lease, definition, ExecuteResult{Disposition: ExecuteResultUnknown})
		requested := repository.lastTransition(t)
		if requested.To != StatusRetryWait || requested.Event != EventRetryScheduled {
			t.Fatalf("requested transition = %+v, want retry_wait", requested)
		}
		records := logger.snapshot()
		if len(records) != 1 || records[0].Result != ResultFailure || records[0].ErrorCode != "cancel_after_unknown_effect" {
			t.Fatalf("worker committed unknown outcome log = %+v", records)
		}
	})

}

func TestTransitionOutcomeDoesNotFallbackRequestedErrorCode(t *testing.T) {
	logger := &captureLogger{}
	repository := &fakeRepository{transitionOutcome: &TransitionOutcome{Status: StatusCancelled}}
	definition := testDefinition(&fakeExecutor{})
	registry, err := NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := NewWorker(repository, registry, WorkerConfig{
		Owner: "empty-outcome-worker", Concurrency: 1, PollInterval: time.Second,
		DatabaseBackoff: time.Second, ShutdownGrace: time.Second, Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	lease := leasedJob(t, registry, StatusRunning, 1)
	worker.applyExecuteResult(context.Background(), lease, definition, ExecuteResult{Disposition: ExecuteRetryableNoEffect, ErrorCode: "synthetic_failure"})
	requested := repository.lastTransition(t)
	if requested.To != StatusRetryWait || requested.ErrorCode != "synthetic_failure" {
		t.Fatalf("requested transition = %+v, want retry_wait with requested error", requested)
	}
	records := logger.snapshot()
	if len(records) != 1 || records[0].Result != ResultSkipped || records[0].ErrorCode != "" {
		t.Fatalf("empty committed error code fell back to request = %+v", records)
	}
}

func TestReconcilerRecoveryRequestedRetryWaitUsesActualDBRewrite(t *testing.T) {
	for _, tc := range []struct {
		name string
		code string
	}{
		{name: "cancel", code: "cancel_after_unknown_effect"},
		{name: "deadline", code: "job_deadline_exceeded"},
		{name: "attempt budget", code: "max_attempts_exhausted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logger := &captureLogger{}
			repository := &fakeRepository{transitionOutcome: &TransitionOutcome{Status: StatusFailed, ErrorCode: tc.code}}
			definition := testDefinition(&fakeExecutor{})
			definition.AllowUnknownEffectReplay = true
			registry, err := NewRegistry(definition)
			if err != nil {
				t.Fatal(err)
			}
			reconciler, err := NewReconciler(repository, registry, ReconcilerConfig{
				Owner: "recovery-outcome-reconciler", Concurrency: 1, PollInterval: time.Second,
				DatabaseBackoff: time.Second, ShutdownGrace: time.Second, Logger: logger,
			})
			if err != nil {
				t.Fatal(err)
			}
			lease := leasedJob(t, registry, StatusRunning, 1)
			// Exercise the same recovery claim entry point used by the runtime;
			// the fake outcome models cancellation/deadline/budget changes that
			// become visible while the DB transition holds its row lock.
			repository.expiredClaims = []Lease{lease}
			claimed, err := repository.ClaimRecoverable(context.Background(), ClaimRequest{Owner: "recovery-outcome-reconciler", Token: uuid.New()})
			if err != nil {
				t.Fatal(err)
			}
			reconciler.reconcile(context.Background(), *claimed)
			requested := repository.lastTransition(t)
			if requested.To != StatusRetryWait || requested.Event != EventRetryScheduled {
				t.Fatalf("requested transition = %+v, want retry_wait", requested)
			}
			records := logger.snapshot()
			if len(records) != 1 || records[0].Result != ResultFailure || records[0].ErrorCode != tc.code {
				t.Fatalf("actual %s outcome log = %+v", tc.code, records)
			}
		})
	}
}

func TestWorkerAndReconcilerPolicyMismatchFailsClosedWithoutOperation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(*Definition)
		mutateJob func(*Job)
	}{
		{"unknown replay true persisted false", func(definition *Definition) { definition.AllowUnknownEffectReplay = true }, func(job *Job) { job.AllowUnknownEffectReplay = false }},
		{"unknown replay false persisted true", func(definition *Definition) {}, func(job *Job) { job.AllowUnknownEffectReplay = true }},
		{"direct success true persisted false", func(definition *Definition) { definition.AllowDirectSuccess = true }, func(job *Job) { job.AllowDirectSuccess = false }},
		{"direct success false persisted true", func(definition *Definition) {}, func(job *Job) { job.AllowDirectSuccess = true }},
	} {
		t.Run("worker/"+tc.name, func(t *testing.T) {
			executor := &fakeExecutor{}
			definition := testDefinition(executor)
			tc.configure(&definition)
			repository := &fakeRepository{}
			worker, registry := policyWorker(t, repository, definition)
			lease := leasedJob(t, registry, StatusRunning, 1)
			tc.mutateJob(&lease.Job)
			worker.execute(context.Background(), lease)
			got := repository.lastTransition(t)
			if got.To != StatusFailed || got.ErrorCode != "job_policy_mismatch" {
				t.Fatalf("mismatch transition = %+v", got)
			}
			if executor.executions.Load() != 0 {
				t.Fatalf("mismatch executed operation %d times", executor.executions.Load())
			}
		})
		t.Run("reconciler/"+tc.name, func(t *testing.T) {
			executor := &fakeExecutor{}
			definition := testDefinition(executor)
			tc.configure(&definition)
			registry, err := NewRegistry(definition)
			if err != nil {
				t.Fatal(err)
			}
			repository := &fakeRepository{}
			reconciler, err := NewReconciler(repository, registry, ReconcilerConfig{
				Owner: "policy-reconciler", Concurrency: 1, PollInterval: time.Second,
				DatabaseBackoff: time.Second, ShutdownGrace: time.Second,
				Retry: BackoffPolicy{Initial: time.Second, Maximum: time.Minute, Jitter: func(time.Duration) time.Duration { return 0 }},
			})
			if err != nil {
				t.Fatal(err)
			}
			lease := leasedJob(t, registry, StatusVerifying, 1)
			tc.mutateJob(&lease.Job)
			reconciler.reconcile(context.Background(), lease)
			got := repository.lastTransition(t)
			if got.To != StatusFailed || got.ErrorCode != "job_policy_mismatch" {
				t.Fatalf("mismatch recovery transition = %+v", got)
			}
			if executor.verifications.Load() != 0 || executor.executions.Load() != 0 {
				t.Fatalf("mismatch executed operation: execute=%d verify=%d", executor.executions.Load(), executor.verifications.Load())
			}
		})
	}
}

func TestReconcilerRunningRecoveryValidationFailuresUseClaimAction(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(*Lease)
	}{
		{"unknown job definition", func(lease *Lease) { lease.Kind = "missing.synthetic" }},
		{"policy mismatch", func(lease *Lease) { lease.AllowDirectSuccess = !lease.AllowDirectSuccess }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			executor := &fakeExecutor{}
			definition := testDefinition(executor)
			registry, err := NewRegistry(definition)
			if err != nil {
				t.Fatal(err)
			}
			logger := &captureLogger{}
			repository := &fakeRepository{}
			reconciler, err := NewReconciler(repository, registry, ReconcilerConfig{
				Owner: "policy-reconciler", Concurrency: 1, PollInterval: time.Second,
				DatabaseBackoff: time.Second, ShutdownGrace: time.Second, Logger: logger,
				Retry: BackoffPolicy{Initial: time.Second, Maximum: time.Minute, Jitter: func(time.Duration) time.Duration { return 0 }},
			})
			if err != nil {
				t.Fatal(err)
			}
			lease := leasedJob(t, registry, StatusRunning, 1)
			tc.configure(&lease)
			reconciler.reconcile(context.Background(), lease)
			got := repository.lastTransition(t)
			if got.To != StatusFailed {
				t.Fatalf("validation failure transition = %+v", got)
			}
			records := logger.snapshot()
			if len(records) != 1 || records[0].Action != ActionClaim {
				t.Fatalf("validation failure log = %+v", records)
			}
			if executor.executions.Load() != 0 || executor.verifications.Load() != 0 {
				t.Fatalf("validation failure executed operation: execute=%d verify=%d", executor.executions.Load(), executor.verifications.Load())
			}
		})
	}
}

func TestWorkerUnknownReplayBudgetCancellationDeadlineAndOrdinaryPaths(t *testing.T) {
	definition := testDefinition(nil)
	definition.AllowUnknownEffectReplay = true
	for _, tc := range []struct {
		name             string
		attempt          int
		cancel, deadline bool
		want             Status
		code             string
	}{
		{"within budget", 1, false, false, StatusRetryWait, "execution_result_unknown"},
		{"final attempt", 3, false, false, StatusFailed, "max_attempts_exhausted"},
		{"cancel", 1, true, false, StatusFailed, "cancel_after_unknown_effect"},
		{"deadline", 1, false, true, StatusFailed, "job_deadline_exceeded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repository := &fakeRepository{}
			worker, registry := policyWorker(t, repository, definition)
			lease := leasedJob(t, registry, StatusRunning, tc.attempt)
			lease.CancelRequested, lease.DeadlineExceeded = tc.cancel, tc.deadline
			worker.applyExecuteResult(context.Background(), lease, definition, ExecuteResult{Disposition: ExecuteResultUnknown})
			got := repository.lastTransition(t)
			if got.To != tc.want || got.ErrorCode != tc.code {
				t.Fatalf("unknown transition = %+v", got)
			}
			if tc.want == StatusRetryWait && got.RetryAfter != time.Second {
				t.Fatalf("retry delay = %s", got.RetryAfter)
			}
		})
	}

	definition.AllowUnknownEffectReplay = false
	repository := &fakeRepository{}
	worker, registry := policyWorker(t, repository, definition)
	lease := leasedJob(t, registry, StatusRunning, 1)
	worker.applyExecuteResult(context.Background(), lease, definition, ExecuteResult{Disposition: ExecuteResultUnknown})
	if got := repository.lastTransition(t); got.To != StatusVerifying {
		t.Fatalf("ordinary unknown path = %+v", got)
	}
}

func TestFrameworkReasonSourcesAreSeparateFromExecutorErrorCodes(t *testing.T) {
	tests := []struct {
		name       string
		configure  func(*Definition)
		result     ExecuteResult
		wantStatus Status
		wantReason string
		wantCode   string
	}{
		{
			name:       "ordinary unknown remains verify first",
			result:     ExecuteResult{Disposition: ExecuteResultUnknown, ErrorCode: ReasonEffectAbsentVerified},
			wantStatus: StatusVerifying, wantCode: "unclassified_executor_error",
		},
		{
			name:       "ordinary unknown preserves legal executor error",
			result:     ExecuteResult{Disposition: ExecuteResultUnknown, ErrorCode: "synthetic_failure"},
			wantStatus: StatusVerifying, wantCode: "synthetic_failure",
		},
		{
			name:       "authorized unknown gets framework reason and fixed error",
			configure:  func(definition *Definition) { definition.AllowUnknownEffectReplay = true },
			result:     ExecuteResult{Disposition: ExecuteResultUnknown, ErrorCode: ReasonExecuteRetryableNoEffect},
			wantStatus: StatusRetryWait, wantReason: ReasonEffectUnknownUnverified, wantCode: "execution_result_unknown",
		},
		{
			name:       "no effect reason is current attempt proof",
			result:     ExecuteResult{Disposition: ExecuteRetryableNoEffect, ErrorCode: ReasonEffectUnknownUnverified},
			wantStatus: StatusRetryWait, wantReason: ReasonExecuteRetryableNoEffect, wantCode: "unclassified_executor_error",
		},
		{
			name:       "permanent failure does not become effect proof",
			result:     ExecuteResult{Disposition: ExecutePermanentFailure, ErrorCode: ReasonEffectUnknownUnverified},
			wantStatus: StatusFailed, wantCode: "unclassified_executor_error",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			definition := testDefinition(nil)
			// Deliberately allow the reserved strings in the executor
			// allowlist. The assertions below prove that framework ownership,
			// rather than ordinary allowlist rejection, keeps them out of
			// ErrorCode and assigns them only to ReasonCode.
			for _, reserved := range []string{
				ReasonEffectUnknownUnverified, ReasonExecuteRetryableNoEffect, ReasonEffectAbsentVerified,
			} {
				definition.ErrorCodes[reserved] = struct{}{}
			}
			if testCase.configure != nil {
				testCase.configure(&definition)
			}
			repository := &fakeRepository{}
			worker, registry := policyWorker(t, repository, definition)
			lease := leasedJob(t, registry, StatusRunning, 1)
			worker.applyExecuteResult(context.Background(), lease, definition, testCase.result)
			transition := repository.lastTransition(t)
			if transition.To != testCase.wantStatus || transition.ReasonCode != testCase.wantReason || transition.ErrorCode != testCase.wantCode {
				t.Fatalf("transition = %+v", transition)
			}
		})
	}
}

func TestReconcilerFrameworkReasonsAndCancellationRequestProof(t *testing.T) {
	t.Run("expired authorized unknown keeps reason when Go selects failed", func(t *testing.T) {
		definition := testDefinition(nil)
		definition.AllowUnknownEffectReplay = true
		registry, err := NewRegistry(definition)
		if err != nil {
			t.Fatal(err)
		}
		repository := &fakeRepository{}
		reconciler, err := NewReconciler(repository, registry, ReconcilerConfig{
			Owner: "reason-reconciler", Concurrency: 1, PollInterval: time.Second,
			DatabaseBackoff: time.Second, ShutdownGrace: time.Second,
			Retry: BackoffPolicy{Initial: time.Second, Maximum: time.Minute, Jitter: func(time.Duration) time.Duration { return 0 }},
		})
		if err != nil {
			t.Fatal(err)
		}
		lease := leasedJob(t, registry, StatusRunning, 1)
		lease.CancelRequested = true
		reconciler.reconcile(context.Background(), lease)
		transition := repository.lastTransition(t)
		if transition.To != StatusFailed || transition.ErrorCode != "cancel_after_unknown_effect" || transition.ReasonCode != ReasonEffectUnknownUnverified {
			t.Fatalf("transition = %+v", transition)
		}
	})

	t.Run("verify absent emits verified marker", func(t *testing.T) {
		definition := testDefinition(nil)
		registry, err := NewRegistry(definition)
		if err != nil {
			t.Fatal(err)
		}
		repository := &fakeRepository{}
		reconciler, err := NewReconciler(repository, registry, ReconcilerConfig{
			Owner: "reason-verify", Concurrency: 1, PollInterval: time.Second,
			DatabaseBackoff: time.Second, ShutdownGrace: time.Second,
			Retry: BackoffPolicy{Initial: time.Second, Maximum: time.Minute, Jitter: func(time.Duration) time.Duration { return 0 }},
		})
		if err != nil {
			t.Fatal(err)
		}
		lease := leasedJob(t, registry, StatusVerifying, 1)
		reconciler.applyVerification(context.Background(), lease, definition, VerifyResult{
			Disposition: VerifyEffectAbsent, ErrorCode: ReasonEffectUnknownUnverified,
		})
		transition := repository.lastTransition(t)
		if transition.To != StatusRetryWait || transition.ReasonCode != ReasonEffectAbsentVerified || transition.ErrorCode != "unclassified_executor_error" {
			t.Fatalf("transition = %+v", transition)
		}
	})

	t.Run("direct success leaves cancellation convergence to DB", func(t *testing.T) {
		definition := testDefinition(nil)
		definition.AllowDirectSuccess = true
		repository := &fakeRepository{}
		worker, registry := policyWorker(t, repository, definition)
		lease := leasedJob(t, registry, StatusRunning, 1)
		lease.CancelRequested = true
		worker.applyExecuteResult(context.Background(), lease, definition, ExecuteResult{Disposition: ExecuteSucceeded})
		transition := repository.lastTransition(t)
		if transition.To != StatusSucceeded || transition.ReasonCode != "" || transition.ErrorCode != "" {
			t.Fatalf("transition = %+v", transition)
		}
	})
}

func TestReconcilerUnknownRecoveryMatrixNeverExecutes(t *testing.T) {
	for _, tc := range []struct {
		name             string
		attempt          int
		cancel, deadline bool
		want             Status
		code             string
	}{
		{"within budget", 1, false, false, StatusRetryWait, "execution_result_unknown"},
		{"cancel requested", 1, true, false, StatusFailed, "cancel_after_unknown_effect"},
		{"deadline exceeded", 1, false, true, StatusFailed, "job_deadline_exceeded"},
		{"final attempt", 3, false, false, StatusFailed, "max_attempts_exhausted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			executor := &fakeExecutor{}
			definition := testDefinition(executor)
			definition.AllowUnknownEffectReplay = true
			registry, err := NewRegistry(definition)
			if err != nil {
				t.Fatal(err)
			}
			repository := &fakeRepository{}
			logger := &captureLogger{}
			reconciler, err := NewReconciler(repository, registry, ReconcilerConfig{
				Owner: "policy-reconciler", Concurrency: 1, PollInterval: time.Second,
				DatabaseBackoff: time.Second, ShutdownGrace: time.Second,
				Retry:  BackoffPolicy{Initial: time.Second, Maximum: time.Minute, Jitter: func(time.Duration) time.Duration { return 0 }},
				Logger: logger,
			})
			if err != nil {
				t.Fatal(err)
			}
			lease := leasedJob(t, registry, StatusRunning, tc.attempt)
			lease.CancelRequested, lease.DeadlineExceeded = tc.cancel, tc.deadline
			reconciler.reconcile(context.Background(), lease)
			got := repository.lastTransition(t)
			if got.To != tc.want || got.ErrorCode != tc.code || got.Actor != ActorReconciler || got.From[0] != StatusRunning {
				t.Fatalf("recovery transition = %+v", got)
			}
			if tc.want == StatusRetryWait && (got.Event != EventRetryScheduled || got.RetryAfter != time.Second) {
				t.Fatalf("retry transition = %+v", got)
			}
			if executor.executions.Load() != 0 || executor.verifications.Load() != 0 {
				t.Fatalf("recovery unexpectedly executed or verified: execute=%d verify=%d", executor.executions.Load(), executor.verifications.Load())
			}
			records := logger.snapshot()
			if len(records) != 1 || records[0].Action != ActionClaim {
				t.Fatalf("recovery log = %+v", records)
			}
		})
	}
}
