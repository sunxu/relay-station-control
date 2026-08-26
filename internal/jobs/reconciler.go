package jobs

import (
	"context"
	"crypto/subtle"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

type ReconcilerConfig struct {
	Owner           string
	Concurrency     int
	PollInterval    time.Duration
	DatabaseBackoff time.Duration
	ShutdownGrace   time.Duration
	Retry           BackoffPolicy
	Clock           Clock
	Logger          Logger
}

type Reconciler struct {
	repository Repository
	registry   *Registry
	config     ReconcilerConfig
	logger     Logger
	wake       chan struct{}
}

func NewReconciler(repository Repository, registry *Registry, config ReconcilerConfig) (*Reconciler, error) {
	if repository == nil || registry == nil || !validOwner(config.Owner) || config.Concurrency < 1 ||
		config.Concurrency > MaxConcurrency || config.PollInterval <= 0 || config.DatabaseBackoff <= 0 || config.ShutdownGrace <= 0 {
		return nil, fmt.Errorf("invalid reconciler configuration")
	}
	if config.Clock == nil {
		config.Clock = RealClock{}
	}
	config.Logger = normalizedLogger(config.Logger)
	return &Reconciler{repository: repository, registry: registry, config: config, logger: config.Logger, wake: make(chan struct{}, 1)}, nil
}

func (r *Reconciler) Wake() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *Reconciler) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	semaphore := make(chan struct{}, r.config.Concurrency)
	var active sync.WaitGroup
	timer := r.config.Clock.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-runCtx.Done():
			return waitForDrain(&active, r.config.ShutdownGrace)
		case <-r.wake:
		case <-timer.C():
		}
		launched, databaseFailed := false, false
		for len(semaphore) < cap(semaphore) {
			lease, err := r.repository.ClaimRecoverable(runCtx, ClaimRequest{Owner: r.config.Owner, Token: uuid.New()})
			if err != nil {
				if !errorsIs(err, ErrNotFound) && runCtx.Err() == nil {
					databaseFailed = true
					emitLog(runCtx, r.logger, r.registry, LogRecord{Component: ComponentReconciler, Action: ActionClaim, Result: ResultFailure, ErrorCode: "database_unavailable"})
				}
				break
			}
			launched = true
			semaphore <- struct{}{}
			active.Add(1)
			go func(claimed Lease) {
				defer active.Done()
				defer func() { <-semaphore }()
				r.reconcile(runCtx, claimed)
			}(*lease)
		}
		delay := r.config.PollInterval
		if databaseFailed {
			delay = r.config.DatabaseBackoff
		} else if launched && len(semaphore) < cap(semaphore) {
			delay = 0
		}
		resetTimer(timer, delay)
	}
}

func (r *Reconciler) reconcile(ctx context.Context, lease Lease) {
	if lease.ID == uuid.Nil || lease.OperationID == uuid.Nil || lease.Token == uuid.Nil ||
		(lease.Status != StatusVerifying && lease.Status != StatusRollingBack) ||
		lease.VerificationAttempt < 1 {
		return
	}
	action := ActionVerify
	if lease.Status == StatusRollingBack {
		action = ActionRollback
	}
	definition, ok := r.registry.Lookup(lease.Kind)
	if !ok || definition.Executor == nil || definition.SchemaVersion != lease.SchemaVersion {
		transition := Transition{
			JobID: lease.ID, Token: lease.Token, From: []Status{lease.Status}, To: StatusFailed,
			Event: EventFailed, Actor: ActorReconciler, ErrorCode: "unknown_job_definition", ReleaseLease: true,
		}
		r.transition(ctx, lease.Kind, action, transition)
		return
	}
	if !policyMatches(lease.Job, definition) {
		transition := Transition{
			JobID: lease.ID, Token: lease.Token, From: []Status{lease.Status}, To: StatusFailed,
			Event: EventFailed, Actor: ActorReconciler, ErrorCode: "job_policy_mismatch", ReleaseLease: true,
		}
		r.transition(ctx, lease.Kind, action, transition)
		return
	}
	canonical, hash, _, err := r.registry.ValidateAndHash(lease.Kind, lease.SchemaVersion, lease.Payload)
	if err != nil || subtle.ConstantTimeCompare(hash[:], lease.PayloadHash[:]) != 1 {
		transition := Transition{
			JobID: lease.ID, Token: lease.Token, From: []Status{lease.Status}, To: StatusFailed,
			Event: EventFailed, Actor: ActorReconciler, ErrorCode: "payload_integrity_failed", ReleaseLease: true,
		}
		r.transition(ctx, lease.Kind, action, transition)
		return
	}
	execution := Execution{JobID: lease.ID, OperationID: lease.OperationID, Kind: lease.Kind, Payload: canonical, Attempt: lease.Attempt, FencingToken: lease.Token}
	if lease.Status == StatusRollingBack {
		result, completed := runWithLeaseHeartbeat(ctx, r.config.Clock, definition.Timeout, definition.HeartbeatInterval,
			func(heartbeatCtx context.Context) error {
				return r.repository.RenewLease(heartbeatCtx, lease.ID, lease.Token, definition.LeaseDuration)
			},
			func(operationCtx context.Context) RollbackResult {
				return definition.Executor.Rollback(operationCtx, execution)
			},
		)
		if completed {
			r.applyRollback(ctx, lease, definition, result)
		} else {
			emitLog(ctx, r.logger, r.registry, LogRecord{Component: ComponentReconciler, Action: ActionRollback, Result: ResultFailure, JobKind: lease.Kind, ErrorCode: "lease_or_timeout"})
		}
		return
	}
	result, completed := runWithLeaseHeartbeat(ctx, r.config.Clock, definition.Timeout, definition.HeartbeatInterval,
		func(heartbeatCtx context.Context) error {
			return r.repository.RenewLease(heartbeatCtx, lease.ID, lease.Token, definition.LeaseDuration)
		},
		func(operationCtx context.Context) VerifyResult {
			return definition.Executor.Verify(operationCtx, execution)
		},
	)
	if completed {
		r.applyVerification(ctx, lease, definition, result)
	} else {
		emitLog(ctx, r.logger, r.registry, LogRecord{Component: ComponentReconciler, Action: ActionVerify, Result: ResultFailure, JobKind: lease.Kind, ErrorCode: "lease_or_timeout"})
	}
}

func (r *Reconciler) transition(ctx context.Context, kind string, action Action, transition Transition) {
	err := r.repository.TransitionFenced(ctx, transition)
	emitTransitionLog(ctx, r.logger, r.registry, ComponentReconciler, action, kind, transition.To, transition.ErrorCode, err)
}

func (r *Reconciler) applyVerification(ctx context.Context, lease Lease, definition Definition, result VerifyResult) {
	transition := Transition{JobID: lease.ID, Token: lease.Token, From: []Status{lease.Status}, Actor: ActorReconciler, ErrorCode: allowedErrorCode(definition, result.ErrorCode), ReleaseLease: true}
	switch result.Disposition {
	case VerifyEffectApplied:
		if lease.CancelRequested {
			if definition.AllowRollback {
				transition.To, transition.Event, transition.ErrorCode = StatusRollingBack, EventRollbackStarted, "cancel_after_effect_applied"
			} else {
				transition.To, transition.Event, transition.ErrorCode = StatusFailed, EventFailed, "cancel_after_effect_applied"
			}
		} else {
			transition.To, transition.Event = StatusSucceeded, EventSucceeded
		}
	case VerifyEffectAbsent:
		if lease.CancelRequested {
			transition.To, transition.Event, transition.ErrorCode = StatusCancelled, EventCancelled, "cancel_verified_safe"
		} else if definition.ReplaySafe && lease.Attempt < lease.MaxAttempts {
			transition.To, transition.Event = StatusRetryWait, EventRetryScheduled
			transition.RetryAfter = r.config.Retry.Delay(lease.Attempt)
		} else {
			transition.To, transition.Event, transition.ErrorCode = StatusFailed, EventFailed, "replay_not_permitted"
		}
	case VerifyEffectPartial:
		if definition.AllowRollback {
			transition.To, transition.Event = StatusRollingBack, EventRollbackStarted
		} else {
			transition.To, transition.Event, transition.ErrorCode = StatusFailed, EventFailed, "rollback_not_permitted"
		}
	case VerifyEffectUnknown:
		if lease.DeadlineExceeded {
			transition.To, transition.Event, transition.ErrorCode = StatusFailed, EventFailed, "job_deadline_exceeded"
		} else if lease.VerificationAttempt >= definition.MaxVerifyAttempts {
			transition.To, transition.Event, transition.ErrorCode = StatusFailed, EventFailed, "verification_exhausted"
		} else {
			transition.To, transition.Event, transition.ErrorCode = StatusVerifying, EventVerification, "effect_unknown"
			transition.RetryAfter = r.config.Retry.Delay(lease.VerificationAttempt + 1)
		}
	default:
		transition.To, transition.Event, transition.ErrorCode = StatusFailed, EventFailed, "invalid_verify_result"
	}
	if result.Mutation != nil {
		if result.Disposition == VerifyEffectApplied && transition.To == StatusSucceeded && transition.Event == EventSucceeded {
			transition.Mutation = result.Mutation
		} else {
			transition.To, transition.Event, transition.ErrorCode = StatusFailed, EventFailed, "invalid_transition_mutation"
			transition.RetryAfter = 0
		}
	}
	r.transition(ctx, lease.Kind, ActionVerify, transition)
}

func (r *Reconciler) applyRollback(ctx context.Context, lease Lease, definition Definition, result RollbackResult) {
	transition := Transition{JobID: lease.ID, Token: lease.Token, From: []Status{StatusRollingBack}, Actor: ActorReconciler, ErrorCode: allowedErrorCode(definition, result.ErrorCode), ReleaseLease: true}
	switch result.Disposition {
	case RollbackCompleted:
		transition.To, transition.Event = StatusRolledBack, EventRolledBack
	case RollbackRetryable, RollbackUnknown:
		if lease.DeadlineExceeded {
			transition.To, transition.Event, transition.ErrorCode = StatusFailed, EventFailed, "job_deadline_exceeded"
		} else if lease.VerificationAttempt >= definition.MaxVerifyAttempts {
			transition.To, transition.Event, transition.ErrorCode = StatusFailed, EventFailed, "rollback_exhausted"
		} else {
			transition.To, transition.Event = StatusRollingBack, EventRollbackStarted
			transition.RetryAfter = r.config.Retry.Delay(lease.VerificationAttempt + 1)
			if result.Disposition == RollbackUnknown {
				transition.ErrorCode = "rollback_result_unknown"
			}
		}
	case RollbackFailed:
		transition.To, transition.Event = StatusFailed, EventFailed
		if transition.ErrorCode == "" {
			transition.ErrorCode = "permanent_rollback_failure"
		}
	default:
		transition.To, transition.Event, transition.ErrorCode = StatusFailed, EventFailed, "invalid_rollback_result"
	}
	if result.Mutation != nil {
		if result.Disposition == RollbackCompleted && transition.To == StatusRolledBack && transition.Event == EventRolledBack {
			transition.Mutation = result.Mutation
		} else {
			transition.To, transition.Event, transition.ErrorCode = StatusFailed, EventFailed, "invalid_transition_mutation"
			transition.RetryAfter = 0
		}
	}
	r.transition(ctx, lease.Kind, ActionRollback, transition)
}
