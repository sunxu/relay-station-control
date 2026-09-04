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

	claimCalls int
	claimErr   error
	claims     []ClaimedRun

	finalizeCalls int
	finalizes     []FinalizeRequest
	finalizeErr   error

	reconcileCalls    int
	reconcileRequests []ReconcileRequest
	reconcileResult   ReconcileResult
	reconcileErr      error
	reconcileHook     func(context.Context) error
}

func (repository *fakeRepository) ScheduleCurrent(_ context.Context, request ScheduleRequest) (ScheduleResult, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.scheduleCalls++
	repository.scheduleRequests = append(repository.scheduleRequests, request)
	return repository.scheduleResult, repository.scheduleErr
}

func (repository *fakeRepository) ClaimRunnable(_ context.Context, request ClaimRequest) (*ClaimedRun, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.claimCalls++
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
	if request.Period != 5*time.Minute || request.PollStartGrace != 10*time.Second || request.MaxAttempts != 2 {
		t.Fatalf("schedule request=%#v", request)
	}

	repository.scheduleResult.ScheduledAt = time.Unix(3_001, 0).UTC()
	if _, err := scheduler.ScheduleOnce(context.Background()); !errors.Is(err, ErrInvalidRepositoryResult) {
		t.Fatalf("misaligned database slot error=%v", err)
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
	if request.PollStartGrace != 10*time.Second || request.Limit != 10 {
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
	configuration.MaxMonitoredNodes = 2
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

	repository = &fakeRepository{}
	driver = &fakeDriver{}
	worker = newWorker(repository, driver, configuration)
	claim = testClaim("antigravity")
	claim.GraceRemaining = 500 * time.Millisecond
	worker.execute(context.Background(), claim)
	deadlines = driver.deadlineSnapshot()
	if len(deadlines) != 1 || deadlines[0] < 350*time.Millisecond || deadlines[0] > 550*time.Millisecond {
		t.Fatalf("grace-limited deadline=%v", deadlines)
	}
}

func TestWorkerGraceDeadlineLeavesLeaseButRequestTimeoutFinalizes(t *testing.T) {
	validated, _ := smallTestConfig().Validate()

	t.Run("grace deadline", func(t *testing.T) {
		repository := &fakeRepository{}
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
	configuration.MaxMonitoredNodes = 50
	configuration.Concurrency = 10
	configuration.ScheduleLimit = 50
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
