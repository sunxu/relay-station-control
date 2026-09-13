package inventorypoll

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/sunxu/relay-station-control/internal/drivers"
)

type Worker struct {
	repository Repository
	invoker    DriverInvoker
	config     ValidatedConfig
	clock      Clock
	wake       chan struct{}

	dispatchMu sync.Mutex
	stopping   bool
}

func NewWorker(repository Repository, invoker DriverInvoker, configuration Config) (*Worker, error) {
	validated, err := configuration.Validate()
	if err != nil || repository == nil || invoker == nil {
		return nil, ErrInvalidConfig
	}
	return newWorker(repository, invoker, validated), nil
}

func newWorker(repository Repository, invoker DriverInvoker, config ValidatedConfig) *Worker {
	return &Worker{repository: repository, invoker: invoker, config: config, clock: config.clock, wake: make(chan struct{}, 1)}
}

func (worker *Worker) Wake() {
	if worker == nil {
		return
	}
	select {
	case worker.wake <- struct{}{}:
	default:
	}
}

func (worker *Worker) Run(ctx context.Context) error {
	if worker == nil || ctx == nil {
		return ErrInvalidConfig
	}
	operationRoot, cancelOperations := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelOperations()
	semaphore := make(chan struct{}, worker.config.concurrency)
	var active sync.WaitGroup
	timer := worker.clock.NewTimer(0)
	defer timer.Stop()
	failures := 0

	for {
		select {
		case <-ctx.Done():
			worker.stopDispatch()
			worker.config.observer.Observe(context.WithoutCancel(ctx), Event{Component: EventComponentWorker, Action: EventActionShutdown, Result: EventResultSuccess, Reason: ControlReasonShutdown})
			return worker.drain(&active, cancelOperations)
		case <-worker.wake:
		case <-timer.C():
		}

		launched, databaseFailed := false, false
		for len(semaphore) < cap(semaphore) && ctx.Err() == nil {
			// The token is acquired before ClaimRunnable. A record cannot become
			// running while it waits for in-process HTTP capacity.
			semaphore <- struct{}{}
			claimContext, cancelClaim := context.WithTimeout(ctx, DefaultClaimBudget)
			claim, err := worker.repository.ClaimRunnable(claimContext, ClaimRequest{
				Token: uuid.New(), LeaseDuration: worker.config.leaseDuration,
			})
			cancelClaim()
			if err != nil {
				<-semaphore
				if !errors.Is(err, ErrNoWork) && !errors.Is(err, context.Canceled) && ctx.Err() == nil {
					databaseFailed = true
					worker.config.observer.Observe(ctx, Event{Component: EventComponentWorker, Action: EventActionClaim, Result: EventResultFailure, Reason: ControlReasonDatabaseUnavailable})
				}
				break
			}
			if validateClaim(claim) != nil {
				<-semaphore
				databaseFailed = true
				worker.config.observer.Observe(ctx, Event{Component: EventComponentWorker, Action: EventActionClaim, Result: EventResultFailure, Reason: ControlReasonInvalidClaim})
				break
			}
			launched = true
			active.Add(1)
			go func(claimed ClaimedRun) {
				defer active.Done()
				defer func() {
					<-semaphore
					worker.Wake()
				}()
				worker.execute(operationRoot, claimed)
			}(*claim)
		}

		delay := worker.config.workerScanInterval
		if databaseFailed {
			failures++
			delay = worker.backoff().delay(failures)
		} else {
			failures = 0
			if launched && len(semaphore) < cap(semaphore) {
				delay = 0
			}
		}
		resetTimer(timer, delay)
	}
}

func (worker *Worker) execute(parent context.Context, claim ClaimedRun) {
	// A claimed run that has not entered its driver call when shutdown starts is
	// deliberately left leased for the database-time reconciler. Calls admitted
	// before this gate are active work and receive the bounded drain period.
	if !worker.beginDispatch() {
		return
	}
	authorizationStarted := time.Now()
	authorization, err := worker.repository.AuthorizeDispatch(parent, DispatchAuthorizationRequest{
		PollRunID: claim.PollRunID, FencingToken: claim.FencingToken,
		Attempt: claim.Attempt, RequestTimeout: worker.config.worstCasePollDuration,
	})
	if err != nil {
		return
	}
	budget := worker.config.worstCasePollDuration
	graceLimited := authorization.GraceRemaining <= budget
	if authorization.GraceRemaining < budget {
		budget = authorization.GraceRemaining
	}
	if authorization.LeaseRemaining <= budget {
		budget = authorization.LeaseRemaining
		graceLimited = true
	}
	absoluteDeadline := authorizationStarted.Add(budget)
	if budget <= 0 || !absoluteDeadline.After(time.Now()) || parent.Err() != nil {
		return
	}
	driverContext, cancelDriver := context.WithDeadline(parent, absoluteDeadline)
	worker.config.observer.Observe(driverContext, Event{
		Component: EventComponentWorker, Action: EventActionDispatch, Result: EventResultSuccess,
		Reason: ControlReasonNone, State: StatusRunning, AttemptBucket: attemptBucket(claim.Attempt, claim.MaxAttempts),
		InstanceID: claim.InstanceID,
	})
	observation, driverErr := worker.invoker.ListAccountInventory(driverContext, drivers.InventoryRequest{
		Target: claim.Target, ProviderPolicy: claim.ProviderPolicy,
	})
	driverContextErr := driverContext.Err()
	cancelDriver()

	// Shutdown and a grace-limited deadline are Control execution interruptions,
	// not Node observations. Leave the lease for database-time reconciliation.
	if parent.Err() != nil || errors.Is(driverContextErr, context.Canceled) ||
		(graceLimited && errors.Is(driverContextErr, context.DeadlineExceeded)) {
		return
	}
	if errors.Is(driverContextErr, context.DeadlineExceeded) && !graceLimited {
		observation = drivers.InventoryObservation{
			Result: drivers.ResultFailed, Reason: drivers.ReasonTimeout,
			Version: "unknown", Commit: "unknown",
		}
	}
	_ = driverErr // The fixed observation is authoritative; raw errors are never persisted or logged.
	node, providers, snapshotItems, duplicates, err := projectObservation(claim.ProviderPolicy, observation)
	if err != nil {
		return
	}
	finalizeContext, cancelFinalize := context.WithTimeout(parent, worker.config.finalizeMargin)
	defer cancelFinalize()
	finalizeErr := worker.repository.FinalizeFenced(finalizeContext, FinalizeRequest{
		PollRunID: claim.PollRunID, FencingToken: claim.FencingToken,
		Node: node, Providers: providers, SnapshotItems: snapshotItems, Duplicates: duplicates,
	})
	result, reason, state := EventResultSuccess, ControlReasonNone, StatusFinalized
	if errors.Is(finalizeErr, ErrLostLease) {
		result, reason, state = EventResultSkipped, ControlReasonLostLease, StatusRunning
	} else if finalizeErr != nil {
		result, reason, state = EventResultFailure, ControlReasonDatabaseUnavailable, StatusRunning
	}
	worker.config.observer.Observe(finalizeContext, Event{
		Component: EventComponentWorker, Action: EventActionFinalize, Result: result, Reason: reason,
		State: state, AttemptBucket: attemptBucket(claim.Attempt, claim.MaxAttempts), InstanceID: claim.InstanceID,
	})
	// Low-latency trigger: a successful finalize is one of the two points
	// this package calls the optional LifecycleObserver hook (the other is
	// Reconciler.ReconcileOnce's periodic/startup success path, see
	// reconciler.go) -- this one exists so brand-new Account Inventory
	// truth is observed without waiting for the next periodic reconcile
	// interval. Uses parent (not finalizeContext, which is about to be
	// canceled by the caller's defer and may already be near its own
	// deadline), matching the design intent that the hook bounds its own
	// work. A failed finalize never triggers it: this path must never let
	// a downstream reconciliation observe truth this Worker itself failed
	// to commit.
	if finalizeErr == nil {
		observerContext, cancelObserver := context.WithTimeout(parent, DefaultLifecycleObserverBudget)
		worker.config.lifecycleObserver(observerContext)
		cancelObserver()
	}
}

func attemptBucket(attempt, maximum int) AttemptBucket {
	if attempt <= 1 {
		return AttemptFirst
	}
	if attempt >= maximum {
		return AttemptExhausted
	}
	return AttemptRetry
}

func (worker *Worker) beginDispatch() bool {
	worker.dispatchMu.Lock()
	defer worker.dispatchMu.Unlock()
	return !worker.stopping
}

func (worker *Worker) stopDispatch() {
	worker.dispatchMu.Lock()
	worker.stopping = true
	worker.dispatchMu.Unlock()
}

func (worker *Worker) drain(active *sync.WaitGroup, cancelOperations context.CancelFunc) error {
	done := make(chan struct{})
	go func() {
		active.Wait()
		close(done)
	}()
	timer := worker.clock.NewTimer(worker.config.shutdownGrace)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C():
		cancelOperations()
	}
	second := worker.clock.NewTimer(worker.config.shutdownGrace)
	defer second.Stop()
	select {
	case <-done:
		return nil
	case <-second.C():
		return ErrShutdownTimedOut
	}
}

func (worker *Worker) backoff() backoffPolicy {
	return backoffPolicy{initial: worker.config.databaseBackoffInitial, maximum: worker.config.databaseBackoffMaximum}
}
