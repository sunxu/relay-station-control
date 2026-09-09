package main

import (
	"context"
	"log/slog"
	"time"
)

type accountAvailabilityReconciler interface {
	Reconcile(context.Context) (int, error)
}

// The existing lifecycle callback supplies startup, finalize and periodic
// triggers. One bounded in-flight branch avoids blocking inventory/duplicate
// work and prevents a slow database from creating unbounded goroutines.
func newAccountAvailabilityReconciliationTrigger(reconciler accountAvailabilityReconciler, logger *slog.Logger) func(context.Context) {
	busy := make(chan struct{}, 1)
	return func(parent context.Context) {
		if parent.Err() != nil {
			return
		}
		select {
		case busy <- struct{}{}:
		default:
			return
		}
		go func() {
			defer func() { <-busy }()
			ctx, cancel := context.WithTimeout(parent, 15*time.Second)
			defer cancel()
			if _, err := reconciler.Reconcile(ctx); err != nil && parent.Err() == nil {
				logger.Error("account availability reconciliation failed", "component", "account_availability", "action", "reconcile", "result", "failure")
			}
		}()
	}
}
