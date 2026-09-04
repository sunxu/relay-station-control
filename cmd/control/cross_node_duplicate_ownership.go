package main

import (
	"context"
	"log/slog"
	"time"

	assetstore "github.com/sunxu/relay-station-control/internal/store"
)

// crossNodeDuplicateOwnershipReconciliationTimeout bounds one production
// reconciliation pass so the Account Inventory poll runtime (Worker.execute
// or Service.Run startup catch-up) that triggers it is never blocked
// indefinitely by cross-node duplicate ownership work.
const crossNodeDuplicateOwnershipReconciliationTimeout = 30 * time.Second

// newCrossNodeDuplicateOwnershipReconciliationTrigger builds the
// inventorypoll.Config.LifecycleObserver callback that is the sole
// production entrypoint for CrossNodeDuplicateOwnershipReconciler.Reconcile.
// It reuses the existing Account Inventory poll control loop (startup
// catch-up in Service.Run, and once per successfully finalized poll run in
// Worker.execute) purely as a source-truth-changed trigger; it is not a new
// scheduler, lease, or fencing mechanism, and it never touches Account
// Inventory poll state itself. A duplicate reconciliation failure is only
// ever logged (fixed, low-cardinality fields; never account_key/email) and
// never propagated back into the poll runtime, so it cannot corrupt Account
// Inventory poll source truth.
func newCrossNodeDuplicateOwnershipReconciliationTrigger(
	reconciler *assetstore.CrossNodeDuplicateOwnershipReconciler, environmentID string, logger *slog.Logger,
) func(context.Context) {
	return func(parent context.Context) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), crossNodeDuplicateOwnershipReconciliationTimeout)
		defer cancel()
		if _, err := reconciler.Reconcile(ctx, environmentID); err != nil {
			logger.Error("cross-node duplicate ownership reconciliation failed",
				"component", "cross_node_duplicate_ownership", "action", "reconcile", "result", "failure")
		}
	}
}

// crossNodeDuplicateOwnershipSlogAlertObserver is the concrete production
// CrossNodeDuplicateOwnershipAlertObserver: it writes each alert-worthy
// lifecycle transition (fresh detect/reopen -> active, ACTIVE->RESOLVED ->
// resolved) to the existing structured logger, mirroring the jobSlogLogger
// adapter pattern used for durable jobs. It never creates a notification
// persistence/platform of its own. account_key/email/provider/affected
// Nodes/occurrence identity are permitted here (this is the one alert
// context they may appear in); credentials, API keys, tokens, passwords,
// secrets, and raw upstream payloads are never present in the event this
// observes and are therefore never logged.
type crossNodeDuplicateOwnershipSlogAlertObserver struct {
	logger *slog.Logger
}

func (observer crossNodeDuplicateOwnershipSlogAlertObserver) Observe(
	ctx context.Context, event assetstore.CrossNodeDuplicateOwnershipAlertEvent,
) {
	if observer.logger == nil {
		return
	}
	affectedNodes := make([]string, 0, len(event.AffectedNodes))
	for _, nodeID := range event.AffectedNodes {
		affectedNodes = append(affectedNodes, nodeID.String())
	}
	observer.logger.LogAttrs(ctx, slog.LevelError, "cross-node duplicate ownership alert",
		slog.String("component", "cross_node_duplicate_ownership"),
		slog.String("action", "alert"),
		slog.String("transition", string(event.Transition)),
		slog.String("severity", event.Severity),
		slog.String("occurrence_id", event.OccurrenceID.String()),
		slog.String("environment_id", event.EnvironmentID),
		slog.String("account_key", event.AccountKey),
		slog.String("provider", event.Provider),
		slog.Any("affected_nodes", affectedNodes),
		slog.Time("first_seen_at", event.FirstSeenAt),
	)
}
