package main

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestRunSerialWaitsTenSecondsAfterSuccessFailureAndLastRequest(t *testing.T) {
	var active atomic.Int32
	maximumActive := int32(0)
	requests := 0
	waits := make([]time.Duration, 0, 2)
	request := func(success bool) requestAttempt {
		return func() bool {
			current := active.Add(1)
			if current > maximumActive {
				maximumActive = current
			}
			requests++
			active.Add(-1)
			return success
		}
	}
	err := runSerial([]requestAttempt{request(true), request(false)}, func(duration time.Duration) {
		waits = append(waits, duration)
	})
	if err == nil || requests != 2 || maximumActive != 1 || len(waits) != 2 {
		t.Fatalf("serial gate = requests %d, active %d, waits %v, err %v", requests, maximumActive, waits, err)
	}
	for _, wait := range waits {
		if wait != 10*time.Second {
			t.Fatalf("post-request wait = %s", wait)
		}
	}
}

func TestRunSerialWaitsAfterSuccessfulLastRequest(t *testing.T) {
	waits := 0
	if err := runSerial([]requestAttempt{func() bool { return true }}, func(duration time.Duration) {
		if duration != requestCooldown {
			t.Fatalf("wait = %s", duration)
		}
		waits++
	}); err != nil || waits != 1 {
		t.Fatalf("last request cooldown = waits %d, err %v", waits, err)
	}
}

func TestRunSerialWaitsAfterPanickingAttempt(t *testing.T) {
	waits := 0
	err := runSerial([]requestAttempt{func() bool { panic("raw-error-canary") }}, func(time.Duration) { waits++ })
	if err == nil || waits != 1 {
		t.Fatalf("panic cooldown = waits %d, err %v", waits, err)
	}
}
