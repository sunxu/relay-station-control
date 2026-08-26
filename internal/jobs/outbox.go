package jobs

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Publisher interface {
	Publish(context.Context, WakeEnvelope) error
}

type DisabledPublisher struct{}

func (DisabledPublisher) Publish(context.Context, WakeEnvelope) error { return ErrPublisherDisabled }

type WakeTarget interface{ Wake() }

// ConsumeWake deliberately does not load an executor or interpret a message as
// work. Valid, duplicate, delayed and out-of-order envelopes all collapse to a
// best-effort request for an earlier PostgreSQL scan.
func ConsumeWake(envelope WakeEnvelope, target WakeTarget) bool {
	if target == nil || envelope.SchemaVersion != 1 || envelope.Topic != "async_job_wake" ||
		envelope.EventID == uuid.Nil || envelope.JobID == uuid.Nil || envelope.OperationID == uuid.Nil {
		return false
	}
	target.Wake()
	return true
}

type DispatcherConfig struct {
	Owner           string
	Concurrency     int
	PollInterval    time.Duration
	DatabaseBackoff time.Duration
	LeaseDuration   time.Duration
	PublishTimeout  time.Duration
	ShutdownGrace   time.Duration
	Retry           BackoffPolicy
	Clock           Clock
	Logger          Logger
}

type Dispatcher struct {
	repository Repository
	publisher  Publisher
	config     DispatcherConfig
	logger     Logger
	wake       chan struct{}
}

func NewDispatcher(repository Repository, publisher Publisher, config DispatcherConfig) (*Dispatcher, error) {
	if repository == nil || publisher == nil || !validOwner(config.Owner) || config.Concurrency < 1 || config.Concurrency > MaxConcurrency ||
		config.PollInterval <= 0 || config.DatabaseBackoff <= 0 || config.LeaseDuration <= 0 || config.PublishTimeout <= 0 ||
		config.PublishTimeout >= config.LeaseDuration || config.ShutdownGrace <= 0 {
		return nil, fmt.Errorf("invalid dispatcher configuration")
	}
	if config.Clock == nil {
		config.Clock = RealClock{}
	}
	config.Logger = normalizedLogger(config.Logger)
	return &Dispatcher{repository: repository, publisher: publisher, config: config, logger: config.Logger, wake: make(chan struct{}, 1)}, nil
}

func (d *Dispatcher) Wake() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

func (d *Dispatcher) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	semaphore := make(chan struct{}, d.config.Concurrency)
	var active sync.WaitGroup
	timer := d.config.Clock.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-runCtx.Done():
			return waitForDrain(&active, d.config.ShutdownGrace)
		case <-d.wake:
		case <-timer.C():
		}
		launched, databaseFailed := false, false
		for len(semaphore) < cap(semaphore) {
			lease, err := d.repository.ClaimOutbox(runCtx, OutboxClaimRequest{Owner: d.config.Owner, Token: uuid.New(), LeaseDuration: d.config.LeaseDuration})
			if err != nil {
				if !errorsIs(err, ErrNotFound) && runCtx.Err() == nil {
					databaseFailed = true
					emitLog(runCtx, d.logger, nil, LogRecord{Component: ComponentDispatcher, Action: ActionClaim, Result: ResultFailure, ErrorCode: "database_unavailable"})
				}
				break
			}
			launched = true
			semaphore <- struct{}{}
			active.Add(1)
			go func(claim OutboxLease) {
				defer active.Done()
				defer func() { <-semaphore }()
				d.dispatch(runCtx, claim)
			}(*lease)
		}
		delay := d.config.PollInterval
		if databaseFailed {
			delay = d.config.DatabaseBackoff
		} else if launched && len(semaphore) < cap(semaphore) {
			delay = 0
		}
		resetTimer(timer, delay)
	}
}

func (d *Dispatcher) dispatch(parent context.Context, lease OutboxLease) {
	if lease.Status != OutboxPublishing || lease.Token == uuid.Nil || lease.Attempt < 1 || lease.MaxAttempts < lease.Attempt ||
		lease.Envelope.SchemaVersion != 1 || lease.Envelope.Topic != "async_job_wake" || lease.Envelope.EventID == uuid.Nil || lease.Envelope.JobID == uuid.Nil || lease.Envelope.OperationID == uuid.Nil {
		d.transition(parent, OutboxTransition{EventID: lease.Envelope.EventID, Token: lease.Token, To: OutboxFailed, ErrorCode: "invalid_wake_envelope", ReleaseLease: true})
		return
	}
	publishContext, cancel := context.WithTimeout(parent, d.config.PublishTimeout)
	defer cancel()
	err := d.publisher.Publish(publishContext, lease.Envelope)
	if publishContext.Err() != nil {
		err = publishContext.Err()
	}
	if err == nil {
		d.transition(parent, OutboxTransition{EventID: lease.Envelope.EventID, Token: lease.Token, To: OutboxSent, ReleaseLease: true})
		return
	}
	transition := OutboxTransition{EventID: lease.Envelope.EventID, Token: lease.Token, ReleaseLease: true, ErrorCode: "publisher_unavailable"}
	if lease.Attempt >= lease.MaxAttempts {
		transition.To = OutboxFailed
	} else {
		transition.To = OutboxRetryWait
		transition.RetryAfter = d.config.Retry.Delay(lease.Attempt)
	}
	d.transition(parent, transition)
}

func (d *Dispatcher) transition(ctx context.Context, transition OutboxTransition) {
	err := d.repository.TransitionOutboxFenced(ctx, transition)
	result := ResultSuccess
	code := transition.ErrorCode
	if transition.To == OutboxFailed || err != nil {
		result = ResultFailure
	}
	if err != nil {
		code = repositoryErrorCode(err)
	}
	emitLog(ctx, d.logger, nil, LogRecord{Component: ComponentDispatcher, Action: ActionPublish, Result: result, ErrorCode: code})
}
