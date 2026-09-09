package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type availabilityTriggerStub struct {
	entered chan context.Context
	done    chan struct{}
	calls   atomic.Int32
}

func (s *availabilityTriggerStub) Reconcile(ctx context.Context) (int, error) {
	s.calls.Add(1)
	s.entered <- ctx
	<-ctx.Done()
	close(s.done)
	return 0, ctx.Err()
}
func TestAccountAvailabilityTriggerIsolationAndCancellation(t *testing.T) {
	stub := &availabilityTriggerStub{entered: make(chan context.Context, 1), done: make(chan struct{})}
	var logs bytes.Buffer
	trigger := newAccountAvailabilityReconciliationTrigger(stub, slog.New(slog.NewTextHandler(&logs, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := time.Now()
	trigger(ctx)
	if time.Since(started) > time.Second {
		t.Fatal("availability blocked inventory callback")
	}
	var work context.Context
	select {
	case work = <-stub.entered:
	case <-time.After(time.Second):
		t.Fatal("not started")
	}
	deadline, ok := work.Deadline()
	if !ok || time.Until(deadline) > 15*time.Second {
		t.Fatal("missing bounded timeout")
	}
	for i := 0; i < 100; i++ {
		trigger(ctx)
	}
	if stub.calls.Load() != 1 {
		t.Fatal("unbounded concurrent branches")
	}
	cancel()
	select {
	case <-stub.done:
	case <-time.After(time.Second):
		t.Fatal("cancellation detached")
	}
	trigger(ctx)
	if stub.calls.Load() != 1 {
		t.Fatal("canceled callback started work")
	}
}

type failedAvailabilityStub struct{ done chan struct{} }

func (s failedAvailabilityStub) Reconcile(context.Context) (int, error) {
	defer close(s.done)
	return 0, errors.New("SECRET_CANARY_ACCOUNT_RAW")
}
func TestAccountAvailabilityTriggerErrorDoesNotEscape(t *testing.T) {
	// The error text must never be projected to logs; it may contain driver/DB details.
	logs := synchronizedAvailabilityLog{writes: make(chan string, 1)}
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	stub := failedAvailabilityStub{done: make(chan struct{})}
	trigger := newAccountAvailabilityReconciliationTrigger(stub, logger)
	trigger(context.Background())
	select {
	case <-stub.done:
	case <-time.After(time.Second):
		t.Fatal("not run")
	}
	select {
	case text := <-logs.writes:
		if strings.Contains(text, "CANARY") {
			t.Fatal("raw error leaked")
		}
	case <-time.After(time.Second):
		t.Fatal("no failure log")
	}
}

type synchronizedAvailabilityLog struct{ writes chan string }

func (l *synchronizedAvailabilityLog) Write(p []byte) (int, error) {
	l.writes <- string(p)
	return len(p), nil
}
