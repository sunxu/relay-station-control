package jobs

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/google/uuid"
)

type BackoffPolicy struct {
	Initial time.Duration
	Maximum time.Duration
	Jitter  func(time.Duration) time.Duration
}

// NewBackoffPolicy returns the production policy. Tests may construct a
// BackoffPolicy directly and inject deterministic Jitter.
func NewBackoffPolicy(initial, maximum time.Duration) BackoffPolicy {
	return BackoffPolicy{Initial: initial, Maximum: maximum, Jitter: boundedRandomJitter}
}

// boundedRandomJitter is concurrency-safe because crypto/rand.Reader is safe
// for concurrent use and carries no shared mutable pseudo-random state. A
// random-source failure degrades to zero jitter while retaining the hard cap.
func boundedRandomJitter(limit time.Duration) time.Duration {
	if limit <= 0 {
		return 0
	}
	upper := new(big.Int).SetInt64(int64(limit))
	upper.Add(upper, big.NewInt(1))
	value, err := rand.Int(rand.Reader, upper)
	if err != nil {
		return 0
	}
	return time.Duration(value.Int64())
}

func (p BackoffPolicy) Delay(attempt int) time.Duration {
	initial := p.Initial
	if initial <= 0 {
		initial = time.Second
	}
	maximum := p.Maximum
	if maximum < initial {
		maximum = time.Minute
	}
	delay := initial
	for count := 1; count < attempt && delay < maximum; count++ {
		if delay > maximum/2 {
			delay = maximum
			break
		}
		delay *= 2
	}
	if delay > maximum {
		delay = maximum
	}
	if p.Jitter != nil {
		jitter := p.Jitter(delay / 4)
		if jitter < 0 {
			jitter = 0
		}
		if jitter > delay/4 {
			jitter = delay / 4
		}
		if jitter > maximum-delay {
			delay = maximum
		} else {
			delay += jitter
		}
	}
	return delay
}

type WorkerConfig struct {
	Owner           string
	Concurrency     int
	PollInterval    time.Duration
	DatabaseBackoff time.Duration
	ShutdownGrace   time.Duration
	Retry           BackoffPolicy
	Clock           Clock
	Logger          Logger
}

type Worker struct {
	repository Repository
	registry   *Registry
	config     WorkerConfig
	logger     Logger
	wake       chan struct{}
}

func NewWorker(repository Repository, registry *Registry, config WorkerConfig) (*Worker, error) {
	if repository == nil || registry == nil || !validOwner(config.Owner) ||
		config.Concurrency < 1 || config.Concurrency > MaxConcurrency ||
		config.PollInterval <= 0 || config.DatabaseBackoff <= 0 || config.ShutdownGrace <= 0 {
		return nil, fmt.Errorf("invalid worker configuration")
	}
	if config.Clock == nil {
		config.Clock = RealClock{}
	}
	config.Logger = normalizedLogger(config.Logger)
	return &Worker{repository: repository, registry: registry, config: config, logger: config.Logger, wake: make(chan struct{}, 1)}, nil
}

func (w *Worker) Wake() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *Worker) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	semaphore := make(chan struct{}, w.config.Concurrency)
	var active sync.WaitGroup
	timer := w.config.Clock.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-runCtx.Done():
			cancel()
			return waitForDrain(&active, w.config.ShutdownGrace)
		case <-w.wake:
		case <-timer.C():
		}

		launched, databaseFailed := false, false
		for len(semaphore) < cap(semaphore) {
			definitionLease, err := w.claim(runCtx)
			if err != nil {
				if !errorsIs(err, ErrNotFound) && runCtx.Err() == nil {
					databaseFailed = true
					emitLog(runCtx, w.logger, w.registry, LogRecord{Component: ComponentWorker, Action: ActionClaim, Result: ResultFailure, ErrorCode: "database_unavailable"})
				}
				break
			}
			launched = true
			semaphore <- struct{}{}
			active.Add(1)
			go func(lease Lease) {
				defer active.Done()
				defer func() { <-semaphore }()
				w.execute(runCtx, lease)
			}(*definitionLease)
		}

		delay := w.config.PollInterval
		if databaseFailed {
			delay = w.config.DatabaseBackoff
		} else if launched && len(semaphore) < cap(semaphore) {
			delay = 0
		}
		resetTimer(timer, delay)
	}
}

func (w *Worker) claim(ctx context.Context) (*Lease, error) {
	return w.repository.ClaimRunnable(ctx, ClaimRequest{
		Owner: w.config.Owner, Token: uuid.New(),
	})
}

func (w *Worker) execute(parent context.Context, lease Lease) {
	if lease.ID == uuid.Nil || lease.OperationID == uuid.Nil || lease.Token == uuid.Nil ||
		lease.Status != StatusRunning || lease.Attempt < 1 || lease.MaxAttempts < lease.Attempt {
		return
	}
	definition, ok := w.registry.Lookup(lease.Kind)
	if !ok || definition.Executor == nil || definition.SchemaVersion != lease.SchemaVersion {
		_ = w.failClosed(parent, lease, "unknown_job_definition")
		return
	}
	if !policyMatches(lease.Job, definition) {
		_ = w.failClosed(parent, lease, "job_policy_mismatch")
		return
	}
	canonical, hash, _, err := w.registry.ValidateAndHash(lease.Kind, lease.SchemaVersion, lease.Payload)
	if err != nil || subtle.ConstantTimeCompare(hash[:], lease.PayloadHash[:]) != 1 {
		_ = w.failClosed(parent, lease, "payload_integrity_failed")
		return
	}

	execution := Execution{
		JobID: lease.ID, OperationID: lease.OperationID, Kind: lease.Kind,
		Payload: append([]byte(nil), canonical...), Attempt: lease.Attempt,
		FencingToken: lease.Token,
	}
	result, completed := runWithLeaseHeartbeat(parent, w.config.Clock, definition.Timeout, definition.HeartbeatInterval,
		func(ctx context.Context) error {
			return w.repository.RenewLease(ctx, lease.ID, lease.Token, definition.LeaseDuration)
		},
		func(ctx context.Context) ExecuteResult { return definition.Executor.Execute(ctx, execution) },
	)
	if !completed {
		// Timeout, shutdown, or a failed renewal makes the effect unknown. Keep
		// the persistent running lease for the Reconciler; never guess success.
		emitLog(parent, w.logger, w.registry, LogRecord{Component: ComponentWorker, Action: ActionExecute, Result: ResultFailure, JobKind: lease.Kind, ErrorCode: "lease_or_timeout"})
		return
	}
	w.applyExecuteResult(parent, lease, definition, result)
}

func (w *Worker) failClosed(ctx context.Context, lease Lease, code string) error {
	transition := Transition{
		JobID: lease.ID, Token: lease.Token, From: []Status{StatusRunning}, To: StatusFailed,
		Event: EventFailed, Actor: ActorWorker, ErrorCode: code, ReleaseLease: true,
	}
	outcome, err := w.repository.TransitionFenced(ctx, transition)
	emitTransitionLog(ctx, w.logger, w.registry, ComponentWorker, ActionExecute, lease.Kind, outcome.Status, outcome.ErrorCode, err)
	return err
}

func (w *Worker) applyExecuteResult(ctx context.Context, lease Lease, definition Definition, result ExecuteResult) {
	code := allowedErrorCode(definition, result.ErrorCode)
	transition := Transition{
		JobID: lease.ID, Token: lease.Token, From: []Status{StatusRunning}, Actor: ActorWorker,
		ErrorCode: code, ReleaseLease: true,
	}
	switch result.Disposition {
	case ExecuteSucceeded:
		if !lease.AllowDirectSuccess {
			transition.To, transition.Event, transition.ErrorCode = StatusFailed, EventFailed, "invalid_executor_result"
			break
		}
		transition.To, transition.Event = StatusSucceeded, EventSucceeded
	case ExecuteNeedsVerification:
		transition.To = StatusVerifying
		transition.Event = EventVerification
		// Verification is separate from Execute. Releasing the lease makes the
		// due verifying row immediately claimable by ClaimRecoverable without
		// ever routing it through Execute again.
		transition.ReleaseLease = true
	case ExecuteResultUnknown:
		if !lease.AllowUnknownEffectReplay {
			transition.To, transition.Event = StatusVerifying, EventVerification
			// Ordinary jobs remain Verify-first. This is not a retry proof, so
			// do not attach the unknown-replay reason to the verifying path.
			if transition.ErrorCode == "" {
				transition.ErrorCode = "execution_result_unknown"
			}
			break
		}
		// The framework, rather than the executor, owns this evidence. Keep
		// it on every authorized unknown outcome, including a budget/deadline
		// or cancellation failure selected before the fenced DB transition.
		transition.ReasonCode = ReasonEffectUnknownUnverified
		transition.ErrorCode = "execution_result_unknown"
		if lease.CancelRequested {
			transition.To, transition.Event, transition.ErrorCode = StatusFailed, EventFailed, "cancel_after_unknown_effect"
		} else if lease.DeadlineExceeded {
			transition.To, transition.Event, transition.ErrorCode = StatusFailed, EventFailed, "job_deadline_exceeded"
		} else if lease.Attempt >= lease.MaxAttempts {
			transition.To, transition.Event, transition.ErrorCode = StatusFailed, EventFailed, "max_attempts_exhausted"
		} else {
			transition.To, transition.Event, transition.ErrorCode = StatusRetryWait, EventRetryScheduled, "execution_result_unknown"
			transition.RetryAfter = w.config.Retry.Delay(lease.Attempt)
		}
	case ExecuteRetryableNoEffect:
		// This proof is scoped to the current Execute attempt. It never
		// clears an earlier unknown proof; the DB decides cancellation
		// convergence while holding the job lock.
		transition.ReasonCode = ReasonExecuteRetryableNoEffect
		if lease.Attempt >= lease.MaxAttempts {
			transition.To, transition.Event, transition.ErrorCode = StatusFailed, EventFailed, "max_attempts_exhausted"
		} else {
			transition.To, transition.Event = StatusRetryWait, EventRetryScheduled
			transition.RetryAfter = w.config.Retry.Delay(lease.Attempt)
		}
	case ExecutePermanentFailure:
		transition.To, transition.Event = StatusFailed, EventFailed
		if transition.ErrorCode == "" {
			transition.ErrorCode = "permanent_execution_failure"
		}
	default:
		transition.To, transition.Event, transition.ErrorCode = StatusFailed, EventFailed, "invalid_executor_result"
	}
	outcome, err := w.repository.TransitionFenced(ctx, transition)
	emitTransitionLog(ctx, w.logger, w.registry, ComponentWorker, ActionExecute, lease.Kind, outcome.Status, outcome.ErrorCode, err)
}

func runWithLeaseHeartbeat[T any](parent context.Context, clock Clock, timeout, heartbeatInterval time.Duration, renew func(context.Context) error, operation func(context.Context) T) (T, bool) {
	var zero T
	operationContext, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	// Re-fence immediately before invoking the external operation. A claim may
	// have been paused by scheduling or process pressure until after its lease
	// expired; the first side effect is forbidden unless database time confirms
	// this token still owns a renewable lease.
	if err := renew(operationContext); err != nil {
		return zero, false
	}
	result := make(chan T, 1)
	go func() { result <- operation(operationContext) }()
	heartbeat := clock.NewTimer(heartbeatInterval)
	defer heartbeat.Stop()
	for {
		select {
		case <-operationContext.Done():
			return zero, false
		case <-heartbeat.C():
			if err := renew(operationContext); err != nil {
				cancel()
				return zero, false
			}
			heartbeat.Reset(heartbeatInterval)
		case value := <-result:
			if operationContext.Err() != nil {
				return zero, false
			}
			return value, true
		}
	}
}

func allowedErrorCode(definition Definition, code string) string {
	if code == "" {
		return ""
	}
	if isFrameworkReasonCode(code) {
		return "unclassified_executor_error"
	}
	if !codePattern.MatchString(code) {
		return "unclassified_executor_error"
	}
	if _, allowed := definition.ErrorCodes[code]; !allowed {
		return "unclassified_executor_error"
	}
	return code
}

func validOwner(owner string) bool {
	return ownerPattern.MatchString(owner)
}

func resetTimer(timer Timer, delay time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C():
		default:
		}
	}
	timer.Reset(delay)
}

func waitForDrain(waitGroup *sync.WaitGroup, grace time.Duration) error {
	done := make(chan struct{})
	go func() {
		waitGroup.Wait()
		close(done)
	}()
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		return ErrShutdownTimedOut
	}
}
