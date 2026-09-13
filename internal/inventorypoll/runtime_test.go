package inventorypoll

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sunxu/relay-station-control/internal/drivers"
)

type fakeRepository struct {
	mu sync.Mutex

	scheduleCalls    int
	scheduleRequests []ScheduleRequest
	scheduleResult   ScheduleResult
	scheduleErr      error

	claimCalls      int
	claimErr        error
	claims          []ClaimedRun
	claimDeadline   time.Time
	authorizeCalls  int
	authorizeErr    error
	authorizeResult DispatchAuthorization
	authorizeDelay  time.Duration

	finalizeCalls int
	finalizes     []FinalizeRequest
	finalizeErr   error

	reconcileCalls    int
	reconcileRequests []ReconcileRequest
	reconcileResult   ReconcileResult
	reconcileErr      error
	reconcileHook     func(context.Context) error
}

type eventObserver struct {
	mu     sync.Mutex
	events []Event
}

type recordingTimer struct {
	ch     chan time.Time
	mu     sync.Mutex
	delays []time.Duration
}

func (timer *recordingTimer) C() <-chan time.Time { return timer.ch }
func (timer *recordingTimer) Reset(delay time.Duration) {
	timer.mu.Lock()
	timer.delays = append(timer.delays, delay)
	timer.mu.Unlock()
}
func (timer *recordingTimer) Stop() bool { return true }

type recordingClock struct {
	mu    sync.Mutex
	timer *recordingTimer
}

func (clock *recordingClock) Now() time.Time { return time.Now() }
func (clock *recordingClock) NewTimer(delay time.Duration) Timer {
	timer := &recordingTimer{ch: make(chan time.Time, 1), delays: []time.Duration{delay}}
	clock.mu.Lock()
	clock.timer = timer
	clock.mu.Unlock()
	return timer
}
func (clock *recordingClock) Timer() *recordingTimer {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.timer
}

func TestReconcilerRunUsesTwentySecondDefaultInterval(t *testing.T) {
	repository := &fakeRepository{}
	clock := &recordingClock{}
	observer := &eventObserver{}
	var lifecycleCalls atomic.Int32
	config := Config{Clock: clock, Observer: observer, LifecycleObserver: func(context.Context) { lifecycleCalls.Add(1) }}
	reconciler, err := NewReconciler(repository, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- reconciler.Run(ctx) }()
	waitFor(t, time.Second, func() bool { return clock.Timer() != nil })
	timer := clock.Timer()
	timer.mu.Lock()
	initial := append([]time.Duration(nil), timer.delays...)
	timer.mu.Unlock()
	if len(initial) != 1 || initial[0] != 20*time.Second {
		t.Fatalf("initial timer delays=%v, want [20s]", initial)
	}
	timer.ch <- time.Now()
	waitFor(t, time.Second, func() bool { repository.mu.Lock(); defer repository.mu.Unlock(); return repository.reconcileCalls == 1 })
	waitFor(t, time.Second, func() bool {
		timer.mu.Lock()
		defer timer.mu.Unlock()
		return len(timer.delays) == 2
	})
	timer.mu.Lock()
	delays := append([]time.Duration(nil), timer.delays...)
	timer.mu.Unlock()
	if len(delays) != 2 || delays[1] != 20*time.Second {
		t.Fatalf("reset timer delays=%v, want [20s 20s]", delays)
	}
	waitFor(t, time.Second, func() bool { return lifecycleCalls.Load() == 1 })
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func (observer *eventObserver) Observe(_ context.Context, event Event) {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.events = append(observer.events, event)
}

func (repository *fakeRepository) ScheduleCurrent(_ context.Context, request ScheduleRequest) (ScheduleResult, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.scheduleCalls++
	repository.scheduleRequests = append(repository.scheduleRequests, request)
	return repository.scheduleResult, repository.scheduleErr
}

func (repository *fakeRepository) AuthorizeDispatch(ctx context.Context, _ DispatchAuthorizationRequest) (DispatchAuthorization, error) {
	if repository.authorizeDelay > 0 {
		timer := time.NewTimer(repository.authorizeDelay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return DispatchAuthorization{}, ctx.Err()
		case <-timer.C:
		}
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.authorizeCalls++
	if repository.authorizeErr != nil {
		return DispatchAuthorization{}, repository.authorizeErr
	}
	if repository.authorizeResult.LeaseRemaining > 0 {
		return repository.authorizeResult, nil
	}
	return DispatchAuthorization{LeaseRemaining: time.Hour, GraceRemaining: time.Hour}, nil
}

func TestWorkerRequiresDispatchAuthorizationBeforeOutbound(t *testing.T) {
	repository := &fakeRepository{authorizeErr: ErrLostLease}
	driver := &fakeDriver{}
	worker, err := NewWorker(repository, driver, smallTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	worker.execute(context.Background(), testClaim("antigravity"))
	if repository.authorizeCalls != 1 || driver.calls.Load() != 0 || repository.finalizeCalls != 0 {
		t.Fatalf("authorize/driver/finalize=%d/%d/%d", repository.authorizeCalls, driver.calls.Load(), repository.finalizeCalls)
	}
}

func (repository *fakeRepository) ClaimRunnable(ctx context.Context, request ClaimRequest) (*ClaimedRun, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.claimCalls++
	if deadline, ok := ctx.Deadline(); ok {
		repository.claimDeadline = deadline
	}
	if repository.claimErr != nil {
		return nil, repository.claimErr
	}
	if len(repository.claims) == 0 {
		return nil, ErrNoWork
	}
	claim := repository.claims[0]
	repository.claims = repository.claims[1:]
	claim.FencingToken = request.Token
	return &claim, nil
}

func TestWorkerUsesFixedClaimAndObserverBudgets(t *testing.T) {
	validated, err := smallTestConfig().Validate()
	if err != nil {
		t.Fatal(err)
	}
	repository := &fakeRepository{}
	worker, err := NewWorker(repository, &fakeDriver{}, smallTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	waitFor(t, time.Second, func() bool { _, claims, _, _ := repository.counts(); return claims > 0 })
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	remaining := time.Until(repository.claimDeadline)
	if remaining <= 0 || remaining > DefaultClaimBudget+100*time.Millisecond {
		t.Fatalf("claim deadline remaining=%s, want <=%s", remaining, DefaultClaimBudget)
	}

	var observerDeadline time.Time
	validated.lifecycleObserver = func(ctx context.Context) {
		observerDeadline, _ = ctx.Deadline()
	}
	worker = newWorker(&fakeRepository{}, &fakeDriver{}, validated)
	worker.execute(context.Background(), testClaim("antigravity"))
	if remaining := time.Until(observerDeadline); remaining <= 0 || remaining > DefaultLifecycleObserverBudget+100*time.Millisecond {
		t.Fatalf("observer deadline remaining=%s, want <=%s", remaining, DefaultLifecycleObserverBudget)
	}
}

func (repository *fakeRepository) FinalizeFenced(_ context.Context, request FinalizeRequest) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.finalizeCalls++
	if repository.finalizeErr != nil {
		return repository.finalizeErr
	}
	repository.finalizes = append(repository.finalizes, request)
	return nil
}

func (repository *fakeRepository) ReconcileExpired(ctx context.Context, request ReconcileRequest) (ReconcileResult, error) {
	repository.mu.Lock()
	repository.reconcileCalls++
	repository.reconcileRequests = append(repository.reconcileRequests, request)
	hook := repository.reconcileHook
	result, err := repository.reconcileResult, repository.reconcileErr
	repository.mu.Unlock()
	if hook != nil {
		if hookErr := hook(ctx); hookErr != nil {
			return ReconcileResult{}, hookErr
		}
	}
	return result, err
}

func (repository *fakeRepository) counts() (schedule, claim, finalize, reconcile int) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return repository.scheduleCalls, repository.claimCalls, repository.finalizeCalls, repository.reconcileCalls
}

func (repository *fakeRepository) finalizeSnapshot() []FinalizeRequest {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	result := make([]FinalizeRequest, len(repository.finalizes))
	copy(result, repository.finalizes)
	return result
}

type fakeDriver struct {
	calls   atomic.Int32
	current atomic.Int32
	maximum atomic.Int32

	mu        sync.Mutex
	requests  []drivers.InventoryRequest
	deadlines []time.Duration
	invoke    func(context.Context, drivers.InventoryRequest) (drivers.InventoryObservation, error)
}

func (driver *fakeDriver) ListAccountInventory(ctx context.Context, request drivers.InventoryRequest) (drivers.InventoryObservation, error) {
	driver.calls.Add(1)
	active := driver.current.Add(1)
	defer driver.current.Add(-1)
	for {
		observed := driver.maximum.Load()
		if active <= observed || driver.maximum.CompareAndSwap(observed, active) {
			break
		}
	}
	driver.mu.Lock()
	driver.requests = append(driver.requests, request)
	if deadline, ok := ctx.Deadline(); ok {
		driver.deadlines = append(driver.deadlines, time.Until(deadline))
	}
	driver.mu.Unlock()
	if driver.invoke != nil {
		return driver.invoke(ctx, request)
	}
	return successfulObservation(request.ProviderPolicy.ActiveProviders), nil
}

func (driver *fakeDriver) deadlineSnapshot() []time.Duration {
	driver.mu.Lock()
	defer driver.mu.Unlock()
	return append([]time.Duration(nil), driver.deadlines...)
}

func testClaim(providers ...string) ClaimedRun {
	instanceID := uuid.New()
	policyID := uuid.New()
	return ClaimedRun{
		PollRunID: uuid.New(), InstanceID: instanceID, PolicyVersionID: policyID,
		ScheduledAt: time.Unix(1_800, 0).UTC(), Attempt: 1, MaxAttempts: 2,
		FencingToken: uuid.New(), GraceRemaining: 5 * time.Second,
		Target: drivers.NodeTarget{
			InstanceID: instanceID, NodeType: drivers.NodeTypeCLIProxyAPI,
			DriverContractVersion: drivers.DriverContractCLIProxyAPIAuthFilesV1,
			ManagementEndpoint:    "https://node.example.invalid",
			ReaderSecretReference: drivers.NewSecretReference("opaque-reference"),
			Capabilities:          []drivers.Capability{drivers.CapabilityManagementAccountInventoryRead},
		},
		ProviderPolicy: drivers.ProviderPolicySnapshot{VersionID: policyID, ActiveProviders: providers},
	}
}

func TestSchedulerUsesOnlyDatabaseCurrentSlotAndIsIdempotencyNeutral(t *testing.T) {
	repository := &fakeRepository{scheduleResult: ScheduleResult{
		ScheduledAt: time.Unix(3_000, 0).UTC(), Eligible: 2, Created: 1, Existing: 1,
	}}
	var wakes atomic.Int32
	scheduler, err := NewScheduler(repository, smallTestConfig(), func() { wakes.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	result, err := scheduler.ScheduleOnce(context.Background())
	if err != nil || result.Created != 1 || wakes.Load() != 1 {
		t.Fatalf("result=%#v err=%v wakes=%d", result, err, wakes.Load())
	}
	repository.mu.Lock()
	request := repository.scheduleRequests[0]
	repository.mu.Unlock()
	if request.Period != 5*time.Minute || request.PollStartGrace != 60*time.Second || request.MaxAttempts != 2 || request.Limit != 2 {
		t.Fatalf("schedule request=%#v", request)
	}

	repository.scheduleResult.ScheduledAt = time.Unix(3_001, 0).UTC()
	if _, err := scheduler.ScheduleOnce(context.Background()); !errors.Is(err, ErrInvalidRepositoryResult) {
		t.Fatalf("misaligned database slot error=%v", err)
	}
}

func TestSchedulerClassifiesCapacityExceededSeparately(t *testing.T) {
	observer := &eventObserver{}
	configuration := smallTestConfig()
	configuration.Observer = observer
	repository := &fakeRepository{scheduleErr: ErrCapacityExceeded}
	scheduler, err := NewScheduler(repository, configuration, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scheduler.ScheduleOnce(context.Background()); !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("schedule error=%v", err)
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if len(observer.events) != 1 || observer.events[0].Reason != ControlReasonCapacityExceeded ||
		observer.events[0].Result != EventResultFailure {
		t.Fatalf("events=%#v", observer.events)
	}
}

func TestReconcilerDelegatesDatabaseTimeStateMachineAndNeverCallsDriver(t *testing.T) {
	repository := &fakeRepository{reconcileResult: ReconcileResult{RetryWait: 1, Abandoned: 2}}
	var wakes atomic.Int32
	reconciler, err := NewReconciler(repository, smallTestConfig(), func() { wakes.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	result, err := reconciler.ReconcileOnce(context.Background())
	if err != nil || result.RetryWait != 1 || result.Abandoned != 2 || wakes.Load() != 1 {
		t.Fatalf("result=%#v err=%v wakes=%d", result, err, wakes.Load())
	}
	repository.mu.Lock()
	request := repository.reconcileRequests[0]
	repository.mu.Unlock()
	if request.PollStartGrace != 60*time.Second || request.Limit != 10 {
		t.Fatalf("request=%#v", request)
	}
	repository.reconcileResult = ReconcileResult{RetryWait: 11}
	if _, err := reconciler.ReconcileOnce(context.Background()); !errors.Is(err, ErrInvalidRepositoryResult) {
		t.Fatalf("invalid reconcile result error=%v", err)
	}
}

// TestReconcilerCallsLifecycleObserverOnEverySuccessfulReconcileEvenWhenIdle
// covers acceptance A: duplicate owner freshness/staleness evolves purely
// with wall-clock time, with no new finalize required, so the periodic
// lifecycle trigger must fire on every successful ReconcileOnce database
// round trip -- including the common case where ReconcileExpired finds
// nothing to reclaim (RetryWait=0, Abandoned=0).
func TestReconcilerCallsLifecycleObserverOnEverySuccessfulReconcileEvenWhenIdle(t *testing.T) {
	configuration, err := smallTestConfig().Validate()
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	configuration.lifecycleObserver = func(context.Context) { calls.Add(1) }
	repository := &fakeRepository{reconcileResult: ReconcileResult{RetryWait: 0, Abandoned: 0}}
	reconciler := newReconciler(repository, configuration, nil)

	if _, err := reconciler.ReconcileOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("lifecycle observer calls after an idle (0/0) reconcile = %d, want 1", calls.Load())
	}

	if _, err := reconciler.ReconcileOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("lifecycle observer calls after a second idle reconcile = %d, want 2", calls.Load())
	}
}

// TestReconcilerNeverCallsLifecycleObserverOnDatabaseFailure covers
// acceptance B: a failed ReconcileExpired must not trigger downstream
// duplicate ownership reconciliation, since there is no new reconciled
// database state to react to.
func TestReconcilerNeverCallsLifecycleObserverOnDatabaseFailure(t *testing.T) {
	configuration, err := smallTestConfig().Validate()
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	configuration.lifecycleObserver = func(context.Context) { calls.Add(1) }
	repository := &fakeRepository{reconcileErr: errors.New("database-canary")}
	reconciler := newReconciler(repository, configuration, nil)

	if _, err := reconciler.ReconcileOnce(context.Background()); err == nil {
		t.Fatal("expected a database error")
	}
	if calls.Load() != 0 {
		t.Fatalf("lifecycle observer calls after a failed reconcile = %d, want 0", calls.Load())
	}
}

func TestWorkerAcquiresSemaphoreBeforeClaimAndHonorsConcurrency(t *testing.T) {
	configuration := smallTestConfig()
	configuration.Concurrency = 2
	repository := &fakeRepository{claims: []ClaimedRun{testClaim("antigravity"), testClaim("antigravity"), testClaim("antigravity")}}
	release := make(chan struct{})
	started := make(chan struct{}, 3)
	driver := &fakeDriver{invoke: func(_ context.Context, request drivers.InventoryRequest) (drivers.InventoryObservation, error) {
		started <- struct{}{}
		<-release
		return successfulObservation(request.ProviderPolicy.ActiveProviders), nil
	}}
	worker, err := NewWorker(repository, driver, configuration)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("worker did not fill concurrency")
		}
	}
	_, claimCalls, _, _ := repository.counts()
	if claimCalls != 2 {
		t.Fatalf("claimed while semaphore full: %d", claimCalls)
	}
	select {
	case <-started:
		t.Fatal("driver exceeded semaphore")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	waitFor(t, time.Second, func() bool { return driver.calls.Load() == 3 })
	waitFor(t, time.Second, func() bool { _, _, finalized, _ := repository.counts(); return finalized == 3 })
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if driver.maximum.Load() > 2 {
		t.Fatalf("maximum driver concurrency=%d", driver.maximum.Load())
	}
}

func TestWorkerRelativeDeadlineAndNodeFailureFinalization(t *testing.T) {
	configuration, err := smallTestConfig().Validate()
	if err != nil {
		t.Fatal(err)
	}
	repository := &fakeRepository{}
	driver := &fakeDriver{invoke: func(context.Context, drivers.InventoryRequest) (drivers.InventoryObservation, error) {
		return drivers.InventoryObservation{Result: drivers.ResultFailed, Reason: drivers.ReasonNetworkUnavailable}, errors.New("raw-network-canary")
	}}
	worker := newWorker(repository, driver, configuration)
	claim := testClaim("antigravity", "codex")
	claim.GraceRemaining = 2 * time.Second
	worker.execute(context.Background(), claim)
	deadlines := driver.deadlineSnapshot()
	if len(deadlines) != 1 || deadlines[0] <= 0 || deadlines[0] > configuration.worstCasePollDuration+100*time.Millisecond {
		t.Fatalf("request-limited deadline=%v", deadlines)
	}
	finalizes := repository.finalizeSnapshot()
	if len(finalizes) != 1 || finalizes[0].Node.TransportSuccess || finalizes[0].Node.ContractValid ||
		len(finalizes[0].Providers) != 2 || finalizes[0].Providers[0].Reason != ProviderReasonTransportFailed {
		t.Fatalf("finalize=%#v", finalizes)
	}
	encoded, _ := json.Marshal(finalizes[0])
	if string(encoded) == "" || containsAny(string(encoded), "raw-network-canary", "ManagementEndpoint", "ReaderSecretReference", "Email") {
		t.Fatalf("unsafe finalize projection: %s", encoded)
	}

	repository = &fakeRepository{authorizeResult: DispatchAuthorization{LeaseRemaining: 2 * time.Second, GraceRemaining: 500 * time.Millisecond}}
	driver = &fakeDriver{}
	worker = newWorker(repository, driver, configuration)
	claim = testClaim("antigravity")
	claim.GraceRemaining = 500 * time.Millisecond
	worker.execute(context.Background(), claim)
	deadlines = driver.deadlineSnapshot()
	if len(deadlines) != 1 || deadlines[0] < 350*time.Millisecond || deadlines[0] > 550*time.Millisecond {
		t.Fatalf("grace-limited deadline=%v", deadlines)
	}

	repository = &fakeRepository{authorizeResult: DispatchAuthorization{LeaseRemaining: 300 * time.Millisecond, GraceRemaining: 2 * time.Second}}
	driver = &fakeDriver{}
	worker = newWorker(repository, driver, configuration)
	claim = testClaim("antigravity")
	claim.GraceRemaining = 2 * time.Second
	worker.execute(context.Background(), claim)
	deadlines = driver.deadlineSnapshot()
	if len(deadlines) != 1 || deadlines[0] < 180*time.Millisecond || deadlines[0] > 350*time.Millisecond {
		t.Fatalf("lease-limited deadline=%v", deadlines)
	}
}

func TestWorkerDispatchDeadlineIncludesAuthorizationLatency(t *testing.T) {
	configuration, err := smallTestConfig().Validate()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("delayed return reduces lease budget", func(t *testing.T) {
		repository := &fakeRepository{
			authorizeDelay:  80 * time.Millisecond,
			authorizeResult: DispatchAuthorization{LeaseRemaining: 200 * time.Millisecond, GraceRemaining: time.Second},
		}
		driver := &fakeDriver{}
		newWorker(repository, driver, configuration).execute(context.Background(), testClaim("antigravity"))
		deadlines := driver.deadlineSnapshot()
		if len(deadlines) != 1 || deadlines[0] <= 0 || deadlines[0] > 140*time.Millisecond {
			t.Fatalf("authorization latency was not deducted: %v", deadlines)
		}
	})

	t.Run("delay exhausts budget without outbound", func(t *testing.T) {
		repository := &fakeRepository{
			authorizeDelay:  80 * time.Millisecond,
			authorizeResult: DispatchAuthorization{LeaseRemaining: 40 * time.Millisecond, GraceRemaining: time.Second},
		}
		driver := &fakeDriver{}
		newWorker(repository, driver, configuration).execute(context.Background(), testClaim("antigravity"))
		if driver.calls.Load() != 0 {
			t.Fatalf("driver calls=%d, want zero", driver.calls.Load())
		}
	})

	for _, test := range []struct {
		name          string
		authorization DispatchAuthorization
		requestLimit  time.Duration
		upperBound    time.Duration
	}{
		{"lease wins", DispatchAuthorization{LeaseRemaining: 120 * time.Millisecond, GraceRemaining: time.Second}, time.Second, 140 * time.Millisecond},
		{"grace wins", DispatchAuthorization{LeaseRemaining: time.Second, GraceRemaining: 120 * time.Millisecond}, time.Second, 140 * time.Millisecond},
		{"request timeout wins", DispatchAuthorization{LeaseRemaining: time.Second, GraceRemaining: time.Second}, 120 * time.Millisecond, 140 * time.Millisecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			configured := configuration
			configured.worstCasePollDuration = test.requestLimit
			repository := &fakeRepository{authorizeResult: test.authorization}
			driver := &fakeDriver{}
			newWorker(repository, driver, configured).execute(context.Background(), testClaim("antigravity"))
			deadlines := driver.deadlineSnapshot()
			if len(deadlines) != 1 || deadlines[0] <= 0 || deadlines[0] > test.upperBound {
				t.Fatalf("deadline=%v, want <=%v", deadlines, test.upperBound)
			}
		})
	}
}

func TestWorkerGraceDeadlineLeavesLeaseButRequestTimeoutFinalizes(t *testing.T) {
	validated, _ := smallTestConfig().Validate()

	t.Run("grace deadline", func(t *testing.T) {
		repository := &fakeRepository{authorizeResult: DispatchAuthorization{LeaseRemaining: time.Second, GraceRemaining: 40 * time.Millisecond}}
		driver := &fakeDriver{invoke: func(ctx context.Context, _ drivers.InventoryRequest) (drivers.InventoryObservation, error) {
			<-ctx.Done()
			return drivers.InventoryObservation{Result: drivers.ResultFailed, Reason: drivers.ReasonTimeout}, ctx.Err()
		}}
		worker := newWorker(repository, driver, validated)
		claim := testClaim("antigravity")
		claim.GraceRemaining = 40 * time.Millisecond
		worker.execute(context.Background(), claim)
		if _, _, finalized, _ := repository.counts(); finalized != 0 {
			t.Fatal("grace-expired observation was finalized")
		}
	})

	t.Run("request deadline", func(t *testing.T) {
		configuration := validated
		configuration.worstCasePollDuration = 40 * time.Millisecond
		repository := &fakeRepository{}
		driver := &fakeDriver{invoke: func(ctx context.Context, _ drivers.InventoryRequest) (drivers.InventoryObservation, error) {
			<-ctx.Done()
			return drivers.InventoryObservation{}, ctx.Err()
		}}
		worker := newWorker(repository, driver, configuration)
		claim := testClaim("antigravity")
		claim.GraceRemaining = time.Second
		worker.execute(context.Background(), claim)
		finalizes := repository.finalizeSnapshot()
		if len(finalizes) != 1 || finalizes[0].Node.Reason != drivers.ReasonTimeout || finalizes[0].Node.TransportSuccess {
			t.Fatalf("request timeout finalize=%#v", finalizes)
		}
	})
}

func TestWorkerInvalidClaimAndFinalizeFailureNeverHideRetry(t *testing.T) {
	configuration, _ := smallTestConfig().Validate()
	driver := &fakeDriver{}
	invalid := testClaim("antigravity")
	invalid.GraceRemaining = 0
	repository := &fakeRepository{claims: []ClaimedRun{invalid}}
	worker := newWorker(repository, driver, configuration)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	waitFor(t, time.Second, func() bool { _, claims, _, _ := repository.counts(); return claims >= 2 })
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if driver.calls.Load() != 0 {
		t.Fatal("invalid claim invoked driver")
	}

	repository = &fakeRepository{finalizeErr: errors.New("database-canary")}
	worker = newWorker(repository, driver, configuration)
	worker.execute(context.Background(), testClaim("antigravity"))
	if driver.calls.Load() != 1 {
		t.Fatalf("finalize failure retried driver: calls=%d", driver.calls.Load())
	}
}

func TestWorkerShutdownStopsClaimsAndAllowsBoundedFinalizeDrain(t *testing.T) {
	repository := &fakeRepository{claims: []ClaimedRun{testClaim("antigravity"), testClaim("antigravity")}}
	started := make(chan struct{})
	release := make(chan struct{})
	var cancelled atomic.Bool
	driver := &fakeDriver{invoke: func(ctx context.Context, request drivers.InventoryRequest) (drivers.InventoryObservation, error) {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			cancelled.Store(true)
		}
		return successfulObservation(request.ProviderPolicy.ActiveProviders), nil
	}}
	worker, err := NewWorker(repository, driver, smallTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	<-started
	cancel()
	time.Sleep(30 * time.Millisecond)
	if cancelled.Load() {
		t.Fatal("active driver was cancelled before shutdown drain grace")
	}
	_, claimsAtStop, _, _ := repository.counts()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	_, claimsAfter, finalized, _ := repository.counts()
	if claimsAfter != claimsAtStop || finalized != 1 {
		t.Fatalf("shutdown claims/finalize: before=%d after=%d finalized=%d", claimsAtStop, claimsAfter, finalized)
	}
}

func TestWorkerShutdownGateSuppressesUndispatchedClaim(t *testing.T) {
	configuration, err := smallTestConfig().Validate()
	if err != nil {
		t.Fatal(err)
	}
	repository := &fakeRepository{}
	driver := &fakeDriver{}
	worker := newWorker(repository, driver, configuration)

	worker.stopDispatch()
	worker.execute(context.Background(), testClaim("antigravity"))

	if driver.calls.Load() != 0 {
		t.Fatal("shutdown gate invoked driver for an undispatched claim")
	}
	if _, _, finalized, _ := repository.counts(); finalized != 0 {
		t.Fatal("shutdown gate finalized an undispatched claim")
	}
}

func TestDatabaseClaimFailureBacksOffAndNeverCallsNode(t *testing.T) {
	repository := &fakeRepository{claimErr: errors.New("database unavailable")}
	driver := &fakeDriver{}
	worker, err := NewWorker(repository, driver, smallTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	time.Sleep(75 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	_, claims, _, _ := repository.counts()
	if claims < 2 || claims > 6 || driver.calls.Load() != 0 {
		t.Fatalf("backoff claims=%d driver=%d", claims, driver.calls.Load())
	}
}

func TestServiceReconcilesBeforeSchedulerOrWorkerAfterDatabaseRecovery(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	repository := &fakeRepository{
		scheduleResult: ScheduleResult{ScheduledAt: time.Unix(3_000, 0).UTC()},
		reconcileHook: func(ctx context.Context) error {
			once.Do(func() { close(started) })
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
	driver := &fakeDriver{}
	service, err := NewService(repository, driver, smallTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()
	<-started
	time.Sleep(30 * time.Millisecond)
	schedules, claims, _, reconciles := repository.counts()
	if reconciles != 1 || schedules != 0 || claims != 0 || driver.calls.Load() != 0 {
		t.Fatalf("before recovery: schedule=%d claim=%d reconcile=%d driver=%d", schedules, claims, reconciles, driver.calls.Load())
	}
	close(release)
	waitFor(t, time.Second, func() bool { schedules, _, _, _ := repository.counts(); return schedules > 0 })
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestFiftyNodeCapacityNeverExceedsTenConcurrentDrivers(t *testing.T) {
	configuration := smallTestConfig()
	configuration.Concurrency = 10
	repository := &fakeRepository{}
	for range 50 {
		repository.claims = append(repository.claims, testClaim("antigravity"))
	}
	release := make(chan struct{})
	driver := &fakeDriver{invoke: func(_ context.Context, request drivers.InventoryRequest) (drivers.InventoryObservation, error) {
		<-release
		return successfulObservation(request.ProviderPolicy.ActiveProviders), nil
	}}
	worker, err := NewWorker(repository, driver, configuration)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	waitFor(t, time.Second, func() bool { return driver.calls.Load() == 10 })
	_, claims, _, _ := repository.counts()
	if claims != 10 {
		t.Fatalf("claimed beyond first capacity batch: %d", claims)
	}
	close(release)
	waitFor(t, 2*time.Second, func() bool { _, _, finalizes, _ := repository.counts(); return finalizes == 50 })
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if driver.maximum.Load() > 10 || driver.calls.Load() != 50 {
		t.Fatalf("max=%d calls=%d", driver.maximum.Load(), driver.calls.Load())
	}
}

func TestWorkerCallsLifecycleObserverOnlyAfterSuccessfulFinalize(t *testing.T) {
	configuration, err := smallTestConfig().Validate()
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	configuration.lifecycleObserver = func(context.Context) { calls.Add(1) }
	driver := &fakeDriver{}

	failing := &fakeRepository{finalizeErr: errors.New("database-canary")}
	worker := newWorker(failing, driver, configuration)
	worker.execute(context.Background(), testClaim("antigravity"))
	if calls.Load() != 0 {
		t.Fatalf("lifecycle observer called after failed finalize: %d", calls.Load())
	}

	succeeding := &fakeRepository{}
	worker = newWorker(succeeding, driver, configuration)
	worker.execute(context.Background(), testClaim("antigravity"))
	if calls.Load() != 1 {
		t.Fatalf("lifecycle observer not called exactly once after successful finalize: %d", calls.Load())
	}
	_, _, finalized, _ := succeeding.counts()
	if finalized != 1 {
		t.Fatalf("expected exactly one finalize, got %d", finalized)
	}
}

// TestServiceCallsLifecycleObserverExactlyOnceOnStartup covers acceptance C:
// the startup lifecycle observer call now happens implicitly inside
// Service.reconcileUntilAvailable's underlying successful
// Reconciler.ReconcileOnce call (see reconciler.go), so Service.Run must not
// also call it explicitly -- that would double-invoke it on startup. The
// periodic Reconciler.Run loop is given a very long interval so it cannot
// tick during this test window, isolating the startup call.
func TestServiceCallsLifecycleObserverExactlyOnceOnStartup(t *testing.T) {
	var calls atomic.Int32
	configuration := smallTestConfig()
	configuration.ReconcileInterval = 2 * time.Second
	configuration.LifecycleObserver = func(context.Context) { calls.Add(1) }
	repository := &fakeRepository{scheduleResult: ScheduleResult{ScheduledAt: time.Unix(3_000, 0).UTC()}}
	driver := &fakeDriver{}
	service, err := NewService(repository, driver, configuration)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()
	waitFor(t, time.Second, func() bool { return calls.Load() != 0 })
	// Give any (incorrect) second explicit call in Run a chance to land
	// before asserting the final count.
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("lifecycle observer calls on startup = %d, want exactly 1 (Service.Run must not call it a second time)", calls.Load())
	}
	_, _, _, reconciles := repository.counts()
	if reconciles != 1 {
		t.Fatalf("reconcile calls during startup window = %d, want exactly 1", reconciles)
	}
}

func TestNilLifecycleObserverDefaultsToNoopAndNeverPanics(t *testing.T) {
	configuration, err := smallTestConfig().Validate()
	if err != nil {
		t.Fatal(err)
	}
	driver := &fakeDriver{}
	repository := &fakeRepository{}
	worker := newWorker(repository, driver, configuration)
	worker.execute(context.Background(), testClaim("antigravity"))
	_, _, finalized, _ := repository.counts()
	if finalized != 1 {
		t.Fatalf("expected finalize to succeed with default no-op observer: %d", finalized)
	}
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not satisfied before timeout")
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if needle != "" && strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
