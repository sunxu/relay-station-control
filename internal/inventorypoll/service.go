package inventorypoll

import (
	"context"
	"errors"
)

type Service struct {
	config     ValidatedConfig
	clock      Clock
	scheduler  *Scheduler
	worker     *Worker
	reconciler *Reconciler
}

func NewService(repository Repository, invoker DriverInvoker, configuration Config) (*Service, error) {
	validated, err := configuration.Validate()
	if err != nil || repository == nil || invoker == nil {
		return nil, ErrInvalidConfig
	}
	worker := newWorker(repository, invoker, validated)
	reconciler := newReconciler(repository, validated, worker.Wake)
	scheduler := newScheduler(repository, validated, worker.Wake)
	return &Service{config: validated, clock: validated.clock, scheduler: scheduler, worker: worker, reconciler: reconciler}, nil
}

// Run first obtains a successful database-time reconciliation barrier. Until
// that succeeds neither the Scheduler nor Worker starts, so a database outage
// cannot create an in-memory poll truth or invoke a Node.
func (service *Service) Run(ctx context.Context) error {
	if service == nil || ctx == nil {
		return ErrInvalidConfig
	}
	if err := service.reconcileUntilAvailable(ctx); err != nil {
		return err
	}
	// Startup catch-up happens implicitly here: reconcileUntilAvailable's
	// final successful Reconciler.ReconcileOnce call already invokes
	// lifecycleObserver (see reconciler.go), so no separate call is made in
	// Run itself -- doing so would invoke the observer twice on startup.
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	errorsChannel := make(chan error, 3)
	go func() { errorsChannel <- service.scheduler.Run(runContext) }()
	go func() { errorsChannel <- service.worker.Run(runContext) }()
	go func() { errorsChannel <- service.reconciler.Run(runContext) }()

	var firstError error
	for completed := 0; completed < 3; completed++ {
		err := <-errorsChannel
		if err != nil && firstError == nil {
			firstError = err
			cancel()
		}
		if ctx.Err() != nil {
			cancel()
		}
	}
	return firstError
}

func (service *Service) reconcileUntilAvailable(ctx context.Context) error {
	failures := 0
	for {
		_, err := service.reconciler.ReconcileOnce(ctx)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			return nil
		}
		failures++
		timer := service.clock.NewTimer(service.backoff().delay(failures))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C():
			timer.Stop()
		}
	}
}

func (service *Service) backoff() backoffPolicy {
	return backoffPolicy{initial: service.config.databaseBackoffInitial, maximum: service.config.databaseBackoffMaximum}
}
