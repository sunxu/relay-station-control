package historyruntime

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeChecker struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (checker *fakeChecker) CheckHistoryCompatibility(context.Context) error {
	checker.mu.Lock()
	defer checker.mu.Unlock()
	checker.calls++
	return checker.err
}

func (checker *fakeChecker) Calls() int {
	checker.mu.Lock()
	defer checker.mu.Unlock()
	return checker.calls
}

type recordingStatus struct {
	mu       sync.Mutex
	statuses []RuntimeStatus
}

func (observer *recordingStatus) ObserveRuntimeStatus(status RuntimeStatus) {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.statuses = append(observer.statuses, status)
}

func (observer *recordingStatus) Last() RuntimeStatus {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if len(observer.statuses) == 0 {
		return RuntimeStatus{}
	}
	return observer.statuses[len(observer.statuses)-1]
}

type blockingLoops struct {
	plannerStarted    chan struct{}
	workerStarted     chan struct{}
	reconcilerStarted chan struct{}
	retentionStarted  chan struct{}
	claimStopped      chan struct{}
	operationStopped  chan struct{}
	onceClaim         sync.Once
	onceOperation     sync.Once
}

type fatalDrainLoops struct {
	trigger          chan struct{}
	plannerStarted   chan struct{}
	workerStarted    chan struct{}
	operationRelease chan struct{}
	operationStopped chan struct{}
	plannerErr       error
	stopOnce         sync.Once
}

func newFatalDrainLoops() *fatalDrainLoops {
	return &fatalDrainLoops{
		trigger: make(chan struct{}), plannerStarted: make(chan struct{}),
		workerStarted: make(chan struct{}), operationRelease: make(chan struct{}),
		operationStopped: make(chan struct{}), plannerErr: errors.New("raw-fatal-marker"),
	}
}

func (loops *fatalDrainLoops) RunPlanner(context.Context) error {
	close(loops.plannerStarted)
	<-loops.trigger
	return loops.plannerErr
}

func (loops *fatalDrainLoops) RunWorker(claimContext, operationContext context.Context) error {
	close(loops.workerStarted)
	<-claimContext.Done()
	select {
	case <-loops.operationRelease:
		return nil
	case <-operationContext.Done():
		loops.stopOnce.Do(func() { close(loops.operationStopped) })
		return operationContext.Err()
	}
}

func (loops *fatalDrainLoops) RunReconciler(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func (loops *fatalDrainLoops) RunRollupWorker(claimContext, _ context.Context) error {
	<-claimContext.Done()
	return nil
}

func (loops *fatalDrainLoops) RunRollupReconciler(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func (loops *fatalDrainLoops) RunRetentionWorker(claimContext, _ context.Context) error {
	<-claimContext.Done()
	return nil
}

func newBlockingLoops() *blockingLoops {
	return &blockingLoops{
		plannerStarted: make(chan struct{}), workerStarted: make(chan struct{}),
		reconcilerStarted: make(chan struct{}), claimStopped: make(chan struct{}),
		retentionStarted: make(chan struct{}), operationStopped: make(chan struct{}),
	}
}

func (loops *blockingLoops) RunPlanner(ctx context.Context) error {
	close(loops.plannerStarted)
	<-ctx.Done()
	loops.onceClaim.Do(func() { close(loops.claimStopped) })
	return nil
}

func (loops *blockingLoops) RunWorker(claimContext, operationContext context.Context) error {
	close(loops.workerStarted)
	<-claimContext.Done()
	loops.onceClaim.Do(func() { close(loops.claimStopped) })
	<-operationContext.Done()
	loops.onceOperation.Do(func() { close(loops.operationStopped) })
	return nil
}

func (loops *blockingLoops) RunReconciler(ctx context.Context) error {
	close(loops.reconcilerStarted)
	<-ctx.Done()
	loops.onceClaim.Do(func() { close(loops.claimStopped) })
	return nil
}

func (loops *blockingLoops) RunRollupWorker(claimContext, _ context.Context) error {
	<-claimContext.Done()
	return nil
}

func (loops *blockingLoops) RunRollupReconciler(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func (loops *blockingLoops) RunRetentionWorker(claimContext, _ context.Context) error {
	close(loops.retentionStarted)
	<-claimContext.Done()
	return nil
}

func TestDisabledServiceChecksCompatibilityWithoutStartingLoops(t *testing.T) {
	checker := &fakeChecker{}
	observer := &recordingStatus{}
	service, err := NewService(Config{}, checker, nil, observer)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if checker.Calls() != 1 {
		t.Fatalf("compatibility calls=%d", checker.Calls())
	}
	if status := observer.Last(); status.Configured || status.Enabled || !status.Compatible ||
		status.Reason != ReasonDisabled {
		t.Fatalf("status=%+v", status)
	}
}

func TestCompatibilityFailureDisablesOnlyHistory(t *testing.T) {
	checker := &fakeChecker{err: errors.New("raw-schema-canary")}
	observer := &recordingStatus{}
	service, err := NewService(Config{Enabled: true}, checker, newBlockingLoops(), observer)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.Run(context.Background()); err != nil {
		t.Fatalf("compatibility failure escaped history boundary: %v", err)
	}
	if checker.Calls() != 1 {
		t.Fatalf("compatibility calls=%d", checker.Calls())
	}
	status := observer.Last()
	if !status.Configured || status.Enabled || status.Compatible || status.Reason != ReasonSchemaIncompatible {
		t.Fatalf("status=%+v", status)
	}
}

func TestCompatibleServiceStopsClaimsBeforeBoundedOperations(t *testing.T) {
	checker := &fakeChecker{}
	observer := &recordingStatus{}
	loops := newBlockingLoops()
	service, err := NewService(Config{
		Enabled: true, StatementTimeout: time.Second, ClaimLease: MinimumClaimLease,
		ShutdownGrace: time.Second,
	}, checker, loops, observer)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()
	for _, started := range []<-chan struct{}{
		loops.plannerStarted, loops.workerStarted, loops.reconcilerStarted, loops.retentionStarted,
	} {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("history loop did not start")
		}
	}
	status := observer.Last()
	if !status.Configured || !status.Enabled || !status.Compatible || status.Reason != ReasonReady {
		t.Fatalf("status=%+v", status)
	}
	cancel()
	select {
	case <-loops.claimStopped:
	case <-time.After(time.Second):
		t.Fatal("planner/claim did not stop")
	}
	select {
	case <-loops.operationStopped:
		t.Fatal("active transaction was cancelled before shutdown grace")
	case <-time.After(20 * time.Millisecond):
	}
	select {
	case err = <-done:
		if !errors.Is(err, ErrShutdownTimedOut) {
			t.Fatalf("shutdown error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("bounded shutdown did not return")
	}
	select {
	case <-loops.operationStopped:
	case <-time.After(time.Second):
		t.Fatal("active operation was not cancelled after grace")
	}
}

func TestFatalLoopStopsRuntimeAfterActiveTransactionDrain(t *testing.T) {
	observer := &recordingStatus{}
	loops := newFatalDrainLoops()
	service, err := NewService(Config{
		Enabled: true, StatementTimeout: time.Second, ClaimLease: MinimumClaimLease,
		ShutdownGrace: time.Second,
	}, &fakeChecker{}, loops, observer)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- service.Run(context.Background()) }()
	for _, started := range []<-chan struct{}{loops.plannerStarted, loops.workerStarted} {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("history loop did not start")
		}
	}
	close(loops.trigger)
	select {
	case err := <-done:
		t.Fatalf("service returned before the active transaction drained: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	select {
	case <-loops.operationStopped:
		t.Fatal("fatal signal cancelled the active transaction before shutdown grace")
	default:
	}
	close(loops.operationRelease)
	select {
	case err = <-done:
		if !errors.Is(err, ErrRuntimeStopped) || strings.Contains(err.Error(), "marker") {
			t.Fatalf("runtime error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("service did not return after the active transaction drained")
	}
	status := observer.Last()
	if !status.Configured || !status.Compatible || status.Reason != ReasonRuntimeStopped {
		t.Fatalf("status=%+v", status)
	}
}

func TestUnexpectedLoopExitStopsRuntime(t *testing.T) {
	for name, plannerErr := range map[string]error{
		"nil":              nil,
		"context canceled": context.Canceled,
	} {
		t.Run(name, func(t *testing.T) {
			observer := &recordingStatus{}
			loops := newFatalDrainLoops()
			loops.plannerErr = plannerErr
			service, err := NewService(Config{
				Enabled: true, StatementTimeout: time.Second, ClaimLease: MinimumClaimLease,
				ShutdownGrace: time.Second,
			}, &fakeChecker{}, loops, observer)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- service.Run(context.Background()) }()
			for _, started := range []<-chan struct{}{loops.plannerStarted, loops.workerStarted} {
				select {
				case <-started:
				case <-time.After(time.Second):
					t.Fatal("history loop did not start")
				}
			}
			close(loops.trigger)
			select {
			case err = <-done:
				t.Fatalf("service returned before the active transaction drained: %v", err)
			case <-time.After(20 * time.Millisecond):
			}
			close(loops.operationRelease)
			select {
			case err = <-done:
				if !errors.Is(err, ErrRuntimeStopped) {
					t.Fatalf("runtime error=%v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("service did not stop after unexpected loop exit")
			}
			if status := observer.Last(); status.Reason != ReasonRuntimeStopped ||
				!status.Configured || status.Enabled || !status.Compatible {
				t.Fatalf("status=%+v", status)
			}
		})
	}
}

func TestEnabledServiceRequiresDatabaseAdapters(t *testing.T) {
	if _, err := NewService(Config{Enabled: true}, nil, nil, nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("error=%v", err)
	}
	if _, err := NewService(Config{}, nil, nil, nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("disabled service without compatibility checker error=%v", err)
	}
}
