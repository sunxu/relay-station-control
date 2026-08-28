package historyruntime

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	ErrRuntimeStopped   = errors.New("account inventory history runtime stopped")
	ErrShutdownTimedOut = errors.New("account inventory history shutdown timed out")
)

type CompatibilityChecker interface {
	// CheckHistoryCompatibility must only inspect PostgreSQL schema, function,
	// ACL, core-sha256, provider-health, and current-query compatibility.
	CheckHistoryCompatibility(context.Context) error
}

// Loops is the integration boundary for the PostgreSQL-backed planner,
// worker, and reconciler. Worker receives a separate operation context so
// cancellation first stops new claims and then permits bounded transaction
// completion. Implementations must not make data-plane or internet requests.
type Loops interface {
	RunPlanner(context.Context) error
	RunWorker(claimContext, operationContext context.Context) error
	RunReconciler(context.Context) error
	RunRollupWorker(claimContext, operationContext context.Context) error
	RunRollupReconciler(context.Context) error
	RunRetentionWorker(claimContext, operationContext context.Context) error
}

type RuntimeReason string

const (
	ReasonDisabled           RuntimeReason = "disabled"
	ReasonReady              RuntimeReason = "ready"
	ReasonSchemaIncompatible RuntimeReason = "schema_incompatible"
	ReasonRuntimeStopped     RuntimeReason = "runtime_stopped"
)

func (reason RuntimeReason) Valid() bool {
	switch reason {
	case ReasonDisabled, ReasonReady, ReasonSchemaIncompatible, ReasonRuntimeStopped:
		return true
	default:
		return false
	}
}

type RuntimeStatus struct {
	Configured bool
	Enabled    bool
	Compatible bool
	Reason     RuntimeReason
}

type StatusObserver interface {
	ObserveRuntimeStatus(RuntimeStatus)
}

type Service struct {
	config  ValidatedConfig
	checker CompatibilityChecker
	loops   Loops
	status  StatusObserver
}

func NewService(configuration Config, checker CompatibilityChecker, loops Loops, observer StatusObserver) (*Service, error) {
	validated, err := configuration.Validate()
	if err != nil {
		return nil, ErrInvalidConfig
	}
	if checker == nil || (validated.enabled && loops == nil) {
		return nil, ErrInvalidConfig
	}
	if observer == nil {
		observer = discardStatusObserver{}
	}
	return &Service{config: validated, checker: checker, loops: loops, status: observer}, nil
}

// Run deliberately treats an incompatible forward schema as a disabled
// history runner. This keeps the existing poll, lifecycle, and current-query
// services available during a partial rollout.
func (service *Service) Run(ctx context.Context) error {
	if service == nil || ctx == nil {
		return ErrInvalidConfig
	}
	compatibilityContext, cancelCompatibility := context.WithTimeout(ctx, service.config.statementTimeout)
	err := service.checker.CheckHistoryCompatibility(compatibilityContext)
	cancelCompatibility()
	if err != nil {
		service.status.ObserveRuntimeStatus(RuntimeStatus{
			Configured: service.config.enabled, Reason: ReasonSchemaIncompatible,
		})
		return nil
	}
	if !service.config.enabled {
		service.status.ObserveRuntimeStatus(RuntimeStatus{
			Compatible: true, Reason: ReasonDisabled,
		})
		return nil
	}
	service.status.ObserveRuntimeStatus(RuntimeStatus{
		Configured: true, Enabled: true, Compatible: true, Reason: ReasonReady,
	})
	return service.runLoops(ctx)
}

func (service *Service) runLoops(ctx context.Context) error {
	claimContext, stopClaims := context.WithCancel(ctx)
	defer stopClaims()
	operationContext, stopOperations := context.WithCancel(context.WithoutCancel(ctx))
	defer stopOperations()

	runners := []func() error{
		func() error { return service.loops.RunPlanner(claimContext) },
		func() error { return service.loops.RunWorker(claimContext, operationContext) },
		func() error { return service.loops.RunReconciler(claimContext) },
		func() error { return service.loops.RunRollupWorker(claimContext, operationContext) },
		func() error { return service.loops.RunRollupReconciler(claimContext) },
		func() error { return service.loops.RunRetentionWorker(claimContext, operationContext) },
	}
	errorsChannel := make(chan error, len(runners))
	var loops sync.WaitGroup
	loops.Add(len(runners))
	for _, run := range runners {
		go func() {
			defer loops.Done()
			errorsChannel <- run()
		}()
	}

	done := make(chan struct{})
	go func() {
		loops.Wait()
		close(done)
	}()

	completed := 0
	for completed < len(runners) {
		select {
		case <-errorsChannel:
			completed++
			// Every loop is expected to live until the parent service is stopped.
			// A nil or context.Canceled return while the parent is still live is
			// therefore just as fatal as an explicit error: otherwise one critical
			// planner/worker could disappear while the service kept reporting ready.
			if ctx.Err() == nil {
				service.status.ObserveRuntimeStatus(RuntimeStatus{
					Configured: true, Compatible: true, Reason: ReasonRuntimeStopped,
				})
				stopClaims()
				// A fatal consistency signal must stop planning and all new claims
				// immediately, while the one bounded transaction already running in
				// each worker is allowed to drain before operations are cancelled.
				_ = service.waitForDrain(done, stopOperations)
				return ErrRuntimeStopped
			}
		case <-ctx.Done():
			stopClaims()
			return service.waitForDrain(done, stopOperations)
		}
	}
	return nil
}

func (service *Service) waitForDrain(done <-chan struct{}, stopOperations context.CancelFunc) error {
	timer := time.NewTimer(service.config.shutdownGrace)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		stopOperations()
		return ErrShutdownTimedOut
	}
}

type discardStatusObserver struct{}

func (discardStatusObserver) ObserveRuntimeStatus(RuntimeStatus) {}
