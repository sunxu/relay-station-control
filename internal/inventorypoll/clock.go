package inventorypoll

import "time"

// Clock controls only in-process wakeups and relative elapsed time. Persistent
// UTC slot and lease decisions remain Repository responsibilities.
type Clock interface {
	Now() time.Time
	NewTimer(time.Duration) Timer
}

type Timer interface {
	C() <-chan time.Time
	Reset(time.Duration)
	Stop() bool
}

type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }
func (RealClock) NewTimer(duration time.Duration) Timer {
	return &realTimer{timer: time.NewTimer(duration)}
}

type realTimer struct{ timer *time.Timer }

func (timer *realTimer) C() <-chan time.Time       { return timer.timer.C }
func (timer *realTimer) Reset(delay time.Duration) { timer.timer.Reset(delay) }
func (timer *realTimer) Stop() bool                { return timer.timer.Stop() }

type backoffPolicy struct {
	initial time.Duration
	maximum time.Duration
}

func (policy backoffPolicy) delay(failures int) time.Duration {
	if failures < 1 {
		failures = 1
	}
	delay := policy.initial
	for attempt := 1; attempt < failures && delay < policy.maximum; attempt++ {
		if delay > policy.maximum/2 {
			return policy.maximum
		}
		delay *= 2
	}
	if delay > policy.maximum {
		return policy.maximum
	}
	return delay
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
