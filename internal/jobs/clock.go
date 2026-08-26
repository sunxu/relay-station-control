package jobs

import "time"

// Clock only schedules in-process loop and heartbeat wakeups. Repository
// implementations remain solely responsible for all persistent time decisions.
type Clock interface {
	NewTimer(time.Duration) Timer
}

type Timer interface {
	C() <-chan time.Time
	Reset(time.Duration)
	Stop() bool
}

type RealClock struct{}

func (RealClock) NewTimer(duration time.Duration) Timer {
	return &realTimer{timer: time.NewTimer(duration)}
}

type realTimer struct{ timer *time.Timer }

func (timer *realTimer) C() <-chan time.Time       { return timer.timer.C }
func (timer *realTimer) Reset(delay time.Duration) { timer.timer.Reset(delay) }
func (timer *realTimer) Stop() bool                { return timer.timer.Stop() }
