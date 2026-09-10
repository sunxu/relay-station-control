package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
)

type controlledClock struct{ created chan *controlledTimer }

func newControlledClock() *controlledClock {
	return &controlledClock{created: make(chan *controlledTimer, 8)}
}

func (clock *controlledClock) NewTimer(time.Duration) Timer {
	timer := &controlledTimer{ticks: make(chan time.Time, 8), resets: make(chan time.Duration, 32)}
	clock.created <- timer
	return timer
}

type controlledTimer struct {
	ticks  chan time.Time
	resets chan time.Duration
}

func (timer *controlledTimer) C() <-chan time.Time { return timer.ticks }
func (timer *controlledTimer) Reset(delay time.Duration) {
	timer.resets <- delay
}
func (*controlledTimer) Stop() bool  { return true }
func (timer *controlledTimer) Fire() { timer.ticks <- time.Now() }

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not satisfied before deadline")
}

func TestWorkerLoopUsesDatabaseEvidenceAndBacksOffOnOutage(t *testing.T) {
	registry, err := NewRegistry(testDefinition(&fakeExecutor{}))
	if err != nil {
		t.Fatal(err)
	}
	repository := &fakeRepository{claimErr: errors.New("database-secret-canary")}
	clock := newControlledClock()
	logger := &captureLogger{}
	worker, err := NewWorker(repository, registry, WorkerConfig{
		Owner: "outage-worker", Concurrency: 1, PollInterval: 17 * time.Millisecond,
		DatabaseBackoff: 43 * time.Millisecond, ShutdownGrace: time.Second,
		Clock: clock, Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	timer := <-clock.created

	timer.Fire()
	if delay := <-timer.resets; delay != 43*time.Millisecond {
		t.Fatalf("database outage reset = %s, want backoff", delay)
	}
	repository.mu.Lock()
	if repository.claimCalls != 1 {
		t.Fatalf("claim calls during one outage tick = %d", repository.claimCalls)
	}
	repository.claimErr = ErrNotFound
	repository.mu.Unlock()

	timer.Fire()
	if delay := <-timer.resets; delay != 17*time.Millisecond {
		t.Fatalf("not-due database evidence reset = %s, want poll interval", delay)
	}
	repository.mu.Lock()
	repository.claimErr = nil
	repository.claims = []Lease{leasedJob(t, registry, StatusRunning, 1)}
	repository.mu.Unlock()
	timer.Fire()
	waitFor(t, func() bool {
		repository.mu.Lock()
		defer repository.mu.Unlock()
		return len(repository.transitions) == 1
	})

	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	for _, record := range logger.snapshot() {
		if strings.Contains(record.ErrorCode, "canary") {
			t.Fatalf("database error leaked to log: %+v", record)
		}
	}
}

func TestWorkerTimeoutLeavesUnknownEffectForDatabaseReconciliation(t *testing.T) {
	repository := &fakeRepository{}
	cancelled := make(chan struct{})
	executor := &fakeExecutor{execute: func(ctx context.Context, _ Execution) ExecuteResult {
		<-ctx.Done()
		close(cancelled)
		return ExecuteResult{Disposition: ExecuteResultUnknown}
	}}
	definition := testDefinition(executor)
	definition.Timeout = time.Second
	registry, err := NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := NewWorker(repository, registry, WorkerConfig{
		Owner: "timeout-worker", Concurrency: 1, PollInterval: time.Second,
		DatabaseBackoff: time.Second, ShutdownGrace: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	worker.execute(context.Background(), leasedJob(t, registry, StatusRunning, 1))
	if elapsed := time.Since(started); elapsed < 900*time.Millisecond || elapsed > 1500*time.Millisecond {
		t.Fatalf("executor timeout elapsed = %s", elapsed)
	}
	select {
	case <-cancelled:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("executor context was not cancelled at its registered timeout")
	}
	if len(repository.transitions) != 0 {
		t.Fatal("timeout guessed a persistent result instead of leaving reconciliation evidence")
	}
}

type crashRepository struct {
	mu                 sync.Mutex
	job                Job
	leaseToken         uuid.UUID
	claimFailures      int
	transitionFailures int
	transitions        []Transition
}

func newCrashRepository(job Job) *crashRepository { return &crashRepository{job: job} }

func (repository *crashRepository) ClaimRunnable(_ context.Context, request ClaimRequest) (*Lease, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.claimFailures > 0 {
		repository.claimFailures--
		return nil, errors.New("claim transaction aborted")
	}
	if repository.job.Status != StatusPending && repository.job.Status != StatusRetryWait {
		return nil, ErrNotFound
	}
	repository.job.Status = StatusRunning
	repository.job.Attempt++
	repository.leaseToken = request.Token
	return repository.lease(request), nil
}

func (repository *crashRepository) RenewLease(_ context.Context, jobID, token uuid.UUID, _ time.Duration) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.job.ID != jobID || repository.leaseToken != token ||
		(repository.job.Status != StatusRunning && repository.job.Status != StatusVerifying && repository.job.Status != StatusRollingBack) {
		return ErrLostLease
	}
	return nil
}

func (repository *crashRepository) TransitionFenced(ctx context.Context, transition Transition) (TransitionOutcome, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if err := transition.Validate(); err != nil {
		return TransitionOutcome{}, err
	}
	if repository.transitionFailures > 0 {
		repository.transitionFailures--
		return TransitionOutcome{}, errors.New("transition transaction interrupted")
	}
	if repository.job.ID != transition.JobID || repository.leaseToken != transition.Token || len(transition.From) != 1 || transition.From[0] != repository.job.Status || repository.job.Status.Terminal() {
		return TransitionOutcome{}, ErrLostLease
	}
	previous := repository.job
	repository.job.Status = transition.To
	if transition.Mutation != nil {
		if err := transition.Mutation(ctx, fakeDBTX{}); err != nil {
			repository.job = previous
			return TransitionOutcome{}, err
		}
	}
	repository.transitions = append(repository.transitions, transition)
	if transition.ReleaseLease {
		repository.leaseToken = uuid.Nil
	}
	return TransitionOutcome{Status: transition.To, ErrorCode: transition.ErrorCode}, nil
}

func (repository *crashRepository) ClaimRecoverable(_ context.Context, request ClaimRequest) (*Lease, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.job.Status != StatusRunning && repository.job.Status != StatusVerifying && repository.job.Status != StatusRollingBack {
		return nil, ErrNotFound
	}
	if repository.job.Status == StatusRunning {
		repository.job.Status = StatusVerifying
	}
	repository.job.VerificationAttempt++
	repository.leaseToken = request.Token
	return repository.lease(request), nil
}

func (repository *crashRepository) lease(request ClaimRequest) *Lease {
	return &Lease{Job: repository.job, Owner: request.Owner, Token: request.Token, ExpiresAt: time.Now().Add(time.Minute)}
}

func (*crashRepository) ClaimOutbox(context.Context, OutboxClaimRequest) (*OutboxLease, error) {
	return nil, ErrNotFound
}
func (*crashRepository) RenewOutboxLease(context.Context, uuid.UUID, uuid.UUID, time.Duration) error {
	return ErrNotFound
}
func (*crashRepository) TransitionOutboxFenced(context.Context, OutboxTransition) error {
	return ErrNotFound
}

type crashExecutor struct {
	effect      atomic.Bool
	executions  atomic.Int32
	verifies    atomic.Int32
	rollbacks   atomic.Int32
	verifyLimit atomic.Int32
}

func (executor *crashExecutor) Execute(context.Context, Execution) ExecuteResult {
	executor.executions.Add(1)
	executor.effect.Store(true)
	return ExecuteResult{Disposition: ExecuteNeedsVerification}
}

func (executor *crashExecutor) Verify(context.Context, Execution) VerifyResult {
	executor.verifies.Add(1)
	if executor.verifyLimit.Add(-1) >= 0 {
		return VerifyResult{Disposition: VerifyEffectUnknown}
	}
	if executor.effect.Load() {
		return VerifyResult{Disposition: VerifyEffectApplied}
	}
	return VerifyResult{Disposition: VerifyEffectAbsent}
}

func (executor *crashExecutor) Rollback(context.Context, Execution) RollbackResult {
	executor.rollbacks.Add(1)
	executor.effect.Store(false)
	return RollbackResult{Disposition: RollbackCompleted}
}

func crashRuntime(t *testing.T, initial Status) (*crashRepository, *crashExecutor, *Registry, *Worker, *Reconciler) {
	t.Helper()
	executor := &crashExecutor{}
	definition := testDefinition(executor)
	registry, err := NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	job := leasedJob(t, registry, initial, 0).Job
	job.Attempt = 0
	if initial == StatusVerifying || initial == StatusRollingBack {
		job.Attempt = 1
		job.VerificationAttempt = 0
	}
	repository := newCrashRepository(job)
	worker, err := NewWorker(repository, registry, WorkerConfig{
		Owner: "crash-worker", Concurrency: 1, PollInterval: time.Second,
		DatabaseBackoff: time.Second, ShutdownGrace: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := NewReconciler(repository, registry, ReconcilerConfig{
		Owner: "crash-reconciler", Concurrency: 1, PollInterval: time.Second,
		DatabaseBackoff: time.Second, ShutdownGrace: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return repository, executor, registry, worker, reconciler
}

func recoverClaim(t *testing.T, repository *crashRepository, reconciler *Reconciler) {
	t.Helper()
	lease, err := repository.ClaimRecoverable(context.Background(), ClaimRequest{Owner: "crash-reconciler", Token: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	reconciler.reconcile(context.Background(), *lease)
}

func TestNoNetworkCrashRecoveryMatrix(t *testing.T) {
	t.Run("claim transaction before commit", func(t *testing.T) {
		repository, executor, _, worker, reconciler := crashRuntime(t, StatusPending)
		repository.claimFailures = 1
		if _, err := repository.ClaimRunnable(context.Background(), ClaimRequest{Owner: "crash-worker", Token: uuid.New()}); err == nil {
			t.Fatal("injected claim failure committed")
		}
		if repository.job.Status != StatusPending || repository.job.Attempt != 0 {
			t.Fatalf("failed claim mutated job: %+v", repository.job)
		}
		lease, _ := repository.ClaimRunnable(context.Background(), ClaimRequest{Owner: "crash-worker", Token: uuid.New()})
		_ = lease // process crashes after the committed claim and before Execute
		recoverClaim(t, repository, reconciler)
		if repository.job.Status != StatusRetryWait || executor.executions.Load() != 0 {
			t.Fatalf("pre-execute recovery status=%s execute=%d", repository.job.Status, executor.executions.Load())
		}
		lease, _ = repository.ClaimRunnable(context.Background(), ClaimRequest{Owner: "crash-worker", Token: uuid.New()})
		worker.execute(context.Background(), *lease)
		recoverClaim(t, repository, reconciler)
		if repository.job.Status != StatusSucceeded || executor.executions.Load() != 1 {
			t.Fatalf("recovered status=%s execute=%d", repository.job.Status, executor.executions.Load())
		}
	})

	t.Run("committed claim before Execute", func(t *testing.T) {
		repository, executor, _, _, reconciler := crashRuntime(t, StatusPending)
		_, _ = repository.ClaimRunnable(context.Background(), ClaimRequest{Owner: "crash-worker", Token: uuid.New()})
		recoverClaim(t, repository, reconciler)
		if repository.job.Status != StatusRetryWait || executor.executions.Load() != 0 {
			t.Fatalf("status=%s execute=%d", repository.job.Status, executor.executions.Load())
		}
	})

	t.Run("Execute after effect before writeback", func(t *testing.T) {
		repository, executor, _, worker, reconciler := crashRuntime(t, StatusPending)
		repository.transitionFailures = 1
		lease, _ := repository.ClaimRunnable(context.Background(), ClaimRequest{Owner: "crash-worker", Token: uuid.New()})
		worker.execute(context.Background(), *lease)
		if repository.job.Status != StatusRunning || !executor.effect.Load() {
			t.Fatalf("crash evidence status=%s effect=%v", repository.job.Status, executor.effect.Load())
		}
		recoverClaim(t, repository, reconciler)
		if repository.job.Status != StatusSucceeded || executor.executions.Load() != 1 {
			t.Fatalf("status=%s execute=%d", repository.job.Status, executor.executions.Load())
		}
	})

	t.Run("verifying", func(t *testing.T) {
		repository, executor, _, _, reconciler := crashRuntime(t, StatusVerifying)
		executor.effect.Store(true)
		recoverClaim(t, repository, reconciler)
		if repository.job.Status != StatusSucceeded || executor.executions.Load() != 0 {
			t.Fatalf("status=%s execute=%d", repository.job.Status, executor.executions.Load())
		}
	})

	t.Run("rolling back", func(t *testing.T) {
		repository, executor, _, _, reconciler := crashRuntime(t, StatusRollingBack)
		executor.effect.Store(true)
		recoverClaim(t, repository, reconciler)
		if repository.job.Status != StatusRolledBack || executor.effect.Load() || executor.rollbacks.Load() != 1 {
			t.Fatalf("status=%s effect=%v rollback=%d", repository.job.Status, executor.effect.Load(), executor.rollbacks.Load())
		}
	})

	t.Run("after terminal commit", func(t *testing.T) {
		repository, executor, _, _, _ := crashRuntime(t, StatusSucceeded)
		if _, err := repository.ClaimRecoverable(context.Background(), ClaimRequest{Owner: "crash-reconciler", Token: uuid.New()}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("terminal recovery claim = %v", err)
		}
		if executor.executions.Load()+executor.verifies.Load()+executor.rollbacks.Load() != 0 {
			t.Fatal("terminal job re-entered an executor")
		}
		terminal := repository.job.Status
		if _, err := repository.TransitionFenced(context.Background(), Transition{
			JobID: repository.job.ID, From: []Status{StatusSucceeded}, To: StatusFailed,
			Event: EventFailed, Actor: ActorSystem, ReleaseLease: true,
		}); !errors.Is(err, ErrLostLease) || repository.job.Status != terminal {
			t.Fatalf("terminal transition err=%v status=%s", err, repository.job.Status)
		}
	})
}

func TestFailClosedDiagnosticsAndRecoveryMatrix(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		mutate func(*Lease)
		code   string
	}{
		{name: "unknown kind", mutate: func(lease *Lease) { lease.Kind = "unknown.kind" }, code: "unknown_job_definition"},
		{name: "unknown schema", mutate: func(lease *Lease) { lease.SchemaVersion++ }, code: "unknown_job_definition"},
		{name: "payload hash", mutate: func(lease *Lease) { lease.PayloadHash[0] ^= 0xff }, code: "payload_integrity_failed"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repository := &fakeRepository{}
			executor := &fakeExecutor{}
			worker, registry := newTestWorker(t, repository, executor)
			lease := leasedJob(t, registry, StatusRunning, 1)
			testCase.mutate(&lease)
			worker.execute(context.Background(), lease)
			if executor.executions.Load() != 0 {
				t.Fatal("untrusted job reached executor")
			}
			if transition := repository.lastTransition(t); transition.To != StatusFailed || transition.ErrorCode != testCase.code {
				t.Fatalf("transition = %+v", transition)
			}
		})
	}

	t.Run("Verify unavailable then restored", func(t *testing.T) {
		repository, executor, _, _, reconciler := crashRuntime(t, StatusVerifying)
		executor.effect.Store(true)
		executor.verifyLimit.Store(1)
		recoverClaim(t, repository, reconciler)
		if repository.job.Status != StatusVerifying || executor.executions.Load() != 0 {
			t.Fatalf("unavailable verify status=%s execute=%d", repository.job.Status, executor.executions.Load())
		}
		recoverClaim(t, repository, reconciler)
		if repository.job.Status != StatusSucceeded || executor.verifies.Load() != 2 {
			t.Fatalf("restored verify status=%s calls=%d", repository.job.Status, executor.verifies.Load())
		}
	})

	t.Run("database interruption then restored", func(t *testing.T) {
		repository, executor, _, _, reconciler := crashRuntime(t, StatusVerifying)
		executor.effect.Store(true)
		repository.transitionFailures = 1
		recoverClaim(t, repository, reconciler)
		if repository.job.Status != StatusVerifying {
			t.Fatalf("failed transaction changed status to %s", repository.job.Status)
		}
		recoverClaim(t, repository, reconciler)
		if repository.job.Status != StatusSucceeded || executor.executions.Load() != 0 {
			t.Fatalf("restored status=%s execute=%d", repository.job.Status, executor.executions.Load())
		}
	})
}

type blockingPublisher struct {
	active  atomic.Int32
	maximum atomic.Int32
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
	err     error
}

func (publisher *blockingPublisher) Publish(ctx context.Context, _ WakeEnvelope) error {
	publisher.calls.Add(1)
	active := publisher.active.Add(1)
	defer publisher.active.Add(-1)
	for {
		observed := publisher.maximum.Load()
		if active <= observed || publisher.maximum.CompareAndSwap(observed, active) {
			break
		}
	}
	if publisher.started != nil {
		publisher.started <- struct{}{}
	}
	if publisher.release != nil {
		select {
		case <-publisher.release:
		case <-ctx.Done():
		}
	}
	return publisher.err
}

func TestDispatcherConcurrencyAndDatabaseOutageBackoff(t *testing.T) {
	repository := &fakeRepository{}
	for range 5 {
		repository.outboxClaims = append(repository.outboxClaims, validOutboxLease())
	}
	publisher := &blockingPublisher{started: make(chan struct{}, 8), release: make(chan struct{})}
	dispatcher, err := NewDispatcher(repository, publisher, DispatcherConfig{
		Owner: "concurrent-dispatcher", Concurrency: 2, PollInterval: 10 * time.Millisecond,
		DatabaseBackoff: 40 * time.Millisecond, LeaseDuration: time.Minute,
		PublishTimeout: 5 * time.Second, ShutdownGrace: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- dispatcher.Run(ctx) }()
	for range 2 {
		select {
		case <-publisher.started:
		case <-time.After(time.Second):
			t.Fatal("dispatcher did not fill bounded pool")
		}
	}
	select {
	case <-publisher.started:
		t.Fatal("dispatcher exceeded configured concurrency")
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	close(publisher.release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if publisher.maximum.Load() != 2 {
		t.Fatalf("publisher concurrency = %d", publisher.maximum.Load())
	}
	staleRepository := &fakeRepository{outboxValidToken: uuid.New()}
	staleDispatcher, _ := NewDispatcher(staleRepository, &fakePublisher{}, DispatcherConfig{
		Owner: "stale-dispatcher", Concurrency: 1, PollInterval: time.Second,
		DatabaseBackoff: time.Second, LeaseDuration: time.Minute,
		PublishTimeout: time.Second, ShutdownGrace: time.Second,
	})
	staleDispatcher.dispatch(context.Background(), validOutboxLease())
	if len(staleRepository.outboxTransitions) != 0 {
		t.Fatal("stale Outbox fencing token committed sent state")
	}

	outageRepository := &fakeRepository{outboxClaimErr: errors.New("database unavailable")}
	clock := newControlledClock()
	outageDispatcher, err := NewDispatcher(outageRepository, &fakePublisher{}, DispatcherConfig{
		Owner: "outage-dispatcher", Concurrency: 1, PollInterval: 13 * time.Millisecond,
		DatabaseBackoff: 37 * time.Millisecond, LeaseDuration: time.Minute,
		PublishTimeout: time.Second, ShutdownGrace: time.Second, Clock: clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	done = make(chan error, 1)
	go func() { done <- outageDispatcher.Run(ctx) }()
	timer := <-clock.created
	timer.Fire()
	if delay := <-timer.resets; delay != 37*time.Millisecond {
		t.Fatalf("dispatcher outage reset = %s", delay)
	}
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

type wakeCounter struct{ calls atomic.Int32 }

func (counter *wakeCounter) Wake() { counter.calls.Add(1) }

func TestNotificationFailureMatrixNeverBecomesJobTruth(t *testing.T) {
	first, second := validOutboxLease().Envelope, validOutboxLease().Envelope
	counter := &wakeCounter{}
	for _, envelope := range []WakeEnvelope{second, first, second, first} {
		if !ConsumeWake(envelope, counter) {
			t.Fatal("valid duplicate or out-of-order wake was rejected")
		}
	}
	if counter.calls.Load() != 4 {
		t.Fatalf("wake scans = %d", counter.calls.Load())
	}

	repository := &fakeRepository{}
	publisher := &blockingPublisher{}
	dispatcher, _ := NewDispatcher(repository, publisher, DispatcherConfig{
		Owner: "crash-dispatcher", Concurrency: 1, PollInterval: time.Second,
		DatabaseBackoff: time.Second, LeaseDuration: time.Minute,
		PublishTimeout: time.Second, ShutdownGrace: time.Second,
	})
	lease := validOutboxLease()
	repository.outboxTransitionErr = errors.New("crash after publish before sent commit")
	dispatcher.dispatch(context.Background(), lease)
	if publisher.calls.Load() != 1 || len(repository.outboxTransitions) != 0 {
		t.Fatal("publish/writeback crash was not modeled")
	}
	repository.outboxTransitionErr = nil
	dispatcher.dispatch(context.Background(), lease)
	if publisher.calls.Load() != 2 || repository.outboxTransitions[0].To != OutboxSent {
		t.Fatal("at-least-once redelivery did not converge to sent")
	}

	unavailableRepository := &fakeRepository{}
	unavailable := &blockingPublisher{err: errors.New("publisher down indefinitely")}
	unavailableDispatcher, _ := NewDispatcher(unavailableRepository, unavailable, DispatcherConfig{
		Owner: "unavailable-dispatcher", Concurrency: 1, PollInterval: time.Second,
		DatabaseBackoff: time.Second, LeaseDuration: time.Minute,
		PublishTimeout: time.Second, ShutdownGrace: time.Second,
	})
	failedLease := validOutboxLease()
	failedLease.Attempt = failedLease.MaxAttempts
	unavailableDispatcher.dispatch(context.Background(), failedLease)
	if unavailableRepository.outboxTransitions[0].To != OutboxFailed {
		t.Fatal("long publisher outage did not reach bounded failed state")
	}
	// Lost notifications and failed Outbox rows do not alter the independently
	// persisted job state; normal polling remains available.
	jobRepository := &fakeRepository{}
	executor := &fakeExecutor{execute: func(context.Context, Execution) ExecuteResult {
		return ExecuteResult{Disposition: ExecutePermanentFailure}
	}}
	worker, registry := newTestWorker(t, jobRepository, executor)
	jobRepository.claims = []Lease{leasedJob(t, registry, StatusRunning, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	waitFor(t, func() bool {
		jobRepository.mu.Lock()
		defer jobRepository.mu.Unlock()
		return len(jobRepository.transitions) == 1
	})
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if transition := jobRepository.lastTransition(t); transition.To != StatusFailed || executor.executions.Load() != 1 {
		t.Fatal("job polling became dependent on notification availability")
	}
}

func TestEnvelopeLogsMetricsAndTraceLikeSinkExcludeCanaries(t *testing.T) {
	registryDefinition := testDefinition(nil)
	registry, err := NewRegistry(registryDefinition)
	if err != nil {
		t.Fatal(err)
	}
	payloadCanary := "payloadcanary"
	tx := &memoryTx{jobs: map[string]Job{}}
	result, err := EnqueueTx(context.Background(), tx, registry, EnqueueRequest{
		Kind: "test.synthetic", SchemaVersion: 1, OperationID: uuid.New(),
		IdempotencyKey: "idempotency-canary", Priority: 10, PublisherEnabled: true,
		Actor: ActorService, Payload: []byte(`{"revision":1,"target_id":"2cf45c9d-ea70-4d1a-ae2b-550701c22a55","enabled":true,"mode":"payloadcanary","providers":["a"]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope := tx.bundles[0].Envelope
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	logger := &captureLogger{}
	lease := validOutboxLease()
	lease.Envelope = envelope
	dispatcher, _ := NewDispatcher(&fakeRepository{}, &fakePublisher{err: errors.New("raw-response-canary")}, DispatcherConfig{
		Owner: "canary-dispatcher", Concurrency: 1, PollInterval: time.Second,
		DatabaseBackoff: time.Second, LeaseDuration: time.Minute, PublishTimeout: time.Second,
		ShutdownGrace: time.Second, Logger: logger,
	})
	dispatcher.dispatch(context.Background(), lease)
	collector, _ := NewCollector(staticMetrics{snapshot: MetricsSnapshot{JobsByStatus: map[Status]int64{StatusPending: 1}}})
	metricsRegistry := prometheus.NewRegistry()
	metricsRegistry.MustRegister(collector)
	families, err := metricsRegistry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	traceLike := fmt.Sprintf("%+v", logger.snapshot())
	combined := string(encoded) + traceLike + fmt.Sprintf("%+v", families)
	canaries := []string{payloadCanary, result.Job.IdempotencyKey, lease.Token.String(), "raw-response-canary"}
	for _, canary := range canaries {
		if strings.Contains(combined, canary) {
			t.Fatalf("canary %q escaped into envelope/observability sink", canary)
		}
	}
	if strings.Contains(combined, "job_kind=") || strings.Contains(combined, "operation_id=") {
		t.Fatal("high-cardinality identity appeared in metrics")
	}
}

type blockingRoundTripper struct{ calls atomic.Int32 }

func (transport *blockingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	transport.calls.Add(1)
	return nil, errors.New("network blocked by acceptance test")
}

type readOnlyAPIMock struct{ reads atomic.Int32 }

func (reader *readOnlyAPIMock) Read(context.Context) { reader.reads.Add(1) }

func TestEmptyProductionRuntimeHasZeroNetworkAndAssetSideEffects(t *testing.T) {
	transport := &blockingRoundTripper{}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	defer func() { http.DefaultTransport = originalTransport }()

	registry := NewProductionRegistry()
	repository := &fakeRepository{}
	worker, err := NewWorker(repository, registry, WorkerConfig{
		Owner: "empty-worker", Concurrency: 1, PollInterval: 10 * time.Millisecond,
		DatabaseBackoff: 10 * time.Millisecond, ShutdownGrace: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := NewReconciler(repository, registry, ReconcilerConfig{
		Owner: "empty-reconciler", Concurrency: 1, PollInterval: 10 * time.Millisecond,
		DatabaseBackoff: 10 * time.Millisecond, ShutdownGrace: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := NewDispatcher(repository, DisabledPublisher{}, DispatcherConfig{
		Owner: "empty-dispatcher", Concurrency: 1, PollInterval: 10 * time.Millisecond,
		DatabaseBackoff: 10 * time.Millisecond, LeaseDuration: time.Minute,
		PublishTimeout: time.Second, ShutdownGrace: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var loops sync.WaitGroup
	for _, run := range []func(context.Context) error{worker.Run, reconciler.Run, dispatcher.Run} {
		loops.Add(1)
		go func(run func(context.Context) error) {
			defer loops.Done()
			_ = run(ctx)
		}(run)
	}
	reader := &readOnlyAPIMock{}
	var assetWrites atomic.Int32
	reader.Read(ctx)
	waitFor(t, func() bool {
		repository.mu.Lock()
		defer repository.mu.Unlock()
		return repository.claimCalls > 0 && repository.recoverableCalls > 0 && repository.outboxClaimCalls > 0
	})
	cancel()
	loops.Wait()
	if registry.Len() != 0 || reader.reads.Load() != 1 || transport.calls.Load() != 0 || assetWrites.Load() != 0 {
		t.Fatalf("registry=%d reads=%d network=%d asset_writes=%d", registry.Len(), reader.reads.Load(), transport.calls.Load(), assetWrites.Load())
	}
	if len(repository.transitions) != 0 || len(repository.outboxTransitions) != 0 {
		t.Fatal("empty production runtime created job or asset-side mutations")
	}
}
