package inventorypoll

import (
	"context"
	"errors"
	"time"
)

type Scheduler struct {
	repository Repository
	config     ValidatedConfig
	clock      Clock
	notify     func()
}

func NewScheduler(repository Repository, configuration Config, notify func()) (*Scheduler, error) {
	validated, err := configuration.Validate()
	if err != nil || repository == nil {
		return nil, ErrInvalidConfig
	}
	return newScheduler(repository, validated, notify), nil
}

func newScheduler(repository Repository, config ValidatedConfig, notify func()) *Scheduler {
	if notify == nil {
		notify = func() {}
	}
	return &Scheduler{repository: repository, config: config, clock: config.clock, notify: notify}
}

// ScheduleOnce asks PostgreSQL to compute and schedule only its current UTC
// slot. The request type intentionally cannot carry a Go-generated timestamp.
func (scheduler *Scheduler) ScheduleOnce(ctx context.Context) (ScheduleResult, error) {
	if scheduler == nil || scheduler.repository == nil || ctx == nil {
		return ScheduleResult{}, ErrInvalidConfig
	}
	result, err := scheduler.repository.ScheduleCurrent(ctx, ScheduleRequest{
		Period: scheduler.config.period, PollStartGrace: scheduler.config.pollStartGrace,
		MaxAttempts: scheduler.config.maxAttempts, Limit: scheduler.config.scheduleLimit,
	})
	if err != nil {
		reason := ControlReasonDatabaseUnavailable
		if errors.Is(err, ErrCapacityExceeded) {
			reason = ControlReasonCapacityExceeded
		}
		scheduler.config.observer.Observe(ctx, Event{Component: EventComponentScheduler, Action: EventActionSchedule, Result: EventResultFailure, Reason: reason})
		return ScheduleResult{}, err
	}
	if err := validateScheduleResult(result, scheduler.config.period); err != nil {
		scheduler.config.observer.Observe(ctx, Event{Component: EventComponentScheduler, Action: EventActionSchedule, Result: EventResultFailure, Reason: ControlReasonInvalidClaim})
		return ScheduleResult{}, err
	}
	scheduler.config.observer.Observe(ctx, Event{Component: EventComponentScheduler, Action: EventActionSchedule, Result: EventResultSuccess, Reason: ControlReasonNone, State: StatusPending})
	if result.Created > 0 || result.Existing > 0 {
		scheduler.notify()
	}
	return result, nil
}

func validateScheduleResult(result ScheduleResult, period time.Duration) error {
	if result.ScheduledAt.IsZero() || period <= 0 || result.ScheduledAt.Unix()%int64(period/time.Second) != 0 ||
		result.Eligible < 0 || result.Created < 0 || result.Existing < 0 ||
		result.Created+result.Existing > result.Eligible {
		return ErrInvalidRepositoryResult
	}
	return nil
}

func (scheduler *Scheduler) Run(ctx context.Context) error {
	if scheduler == nil || ctx == nil {
		return ErrInvalidConfig
	}
	timer := scheduler.clock.NewTimer(0)
	defer timer.Stop()
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C():
		}
		_, err := scheduler.ScheduleOnce(ctx)
		delay := scheduler.config.schedulerInterval
		if err != nil && !errors.Is(err, context.Canceled) && ctx.Err() == nil {
			failures++
			delay = scheduler.backoff().delay(failures)
		} else {
			failures = 0
		}
		resetTimer(timer, delay)
	}
}

func (scheduler *Scheduler) backoff() backoffPolicy {
	return backoffPolicy{initial: scheduler.config.databaseBackoffInitial, maximum: scheduler.config.databaseBackoffMaximum}
}
