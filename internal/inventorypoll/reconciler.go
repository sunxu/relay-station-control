package inventorypoll

import (
	"context"
	"errors"
)

type Reconciler struct {
	repository Repository
	config     ValidatedConfig
	clock      Clock
	notify     func()
}

func NewReconciler(repository Repository, configuration Config, notify func()) (*Reconciler, error) {
	validated, err := configuration.Validate()
	if err != nil || repository == nil {
		return nil, ErrInvalidConfig
	}
	return newReconciler(repository, validated, notify), nil
}

func newReconciler(repository Repository, config ValidatedConfig, notify func()) *Reconciler {
	if notify == nil {
		notify = func() {}
	}
	return &Reconciler{repository: repository, config: config, clock: config.clock, notify: notify}
}

// ReconcileOnce asks PostgreSQL to classify expired leases and grace windows.
// No process time is supplied, and the Reconciler never calls a Node Driver.
func (reconciler *Reconciler) ReconcileOnce(ctx context.Context) (ReconcileResult, error) {
	if reconciler == nil || reconciler.repository == nil || ctx == nil {
		return ReconcileResult{}, ErrInvalidConfig
	}
	result, err := reconciler.repository.ReconcileExpired(ctx, ReconcileRequest{
		PollStartGrace: reconciler.config.pollStartGrace, Limit: reconciler.config.reconcileLimit,
	})
	if err != nil {
		reconciler.config.observer.Observe(ctx, Event{Component: EventComponentReconciler, Action: EventActionReconcile, Result: EventResultFailure, Reason: ControlReasonDatabaseUnavailable})
		return ReconcileResult{}, err
	}
	if result.RetryWait < 0 || result.Abandoned < 0 || result.RetryWait+result.Abandoned > reconciler.config.reconcileLimit {
		reconciler.config.observer.Observe(ctx, Event{Component: EventComponentReconciler, Action: EventActionReconcile, Result: EventResultFailure, Reason: ControlReasonInvalidClaim})
		return ReconcileResult{}, ErrInvalidRepositoryResult
	}
	if result.RetryWait > 0 || result.Abandoned > 0 {
		state := StatusRetryWait
		reason := ControlReasonLostLease
		if result.Abandoned > 0 {
			state, reason = StatusAbandoned, ControlReasonGraceExhausted
		}
		reconciler.config.observer.Observe(ctx, Event{Component: EventComponentReconciler, Action: EventActionReconcile, Result: EventResultSuccess, Reason: reason, State: state})
	}
	if result.RetryWait > 0 {
		reconciler.notify()
	}
	reconciler.config.lifecycleObserver(ctx)
	return result, nil
}

func (reconciler *Reconciler) Run(ctx context.Context) error {
	if reconciler == nil || ctx == nil {
		return ErrInvalidConfig
	}
	timer := reconciler.clock.NewTimer(reconciler.config.reconcileInterval)
	defer timer.Stop()
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C():
		}
		_, err := reconciler.ReconcileOnce(ctx)
		delay := reconciler.config.reconcileInterval
		if err != nil && !errors.Is(err, context.Canceled) && ctx.Err() == nil {
			failures++
			delay = reconciler.backoff().delay(failures)
		} else {
			failures = 0
		}
		resetTimer(timer, delay)
	}
}

func (reconciler *Reconciler) backoff() backoffPolicy {
	return backoffPolicy{initial: reconciler.config.databaseBackoffInitial, maximum: reconciler.config.databaseBackoffMaximum}
}
