package pollobservability

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
)

type Component string
type Action string
type LogResult string
type Reason string
type AttemptBucket string
type NodeType string

const (
	ComponentScheduler  Component = "poll_scheduler"
	ComponentWorker     Component = "poll_worker"
	ComponentReconciler Component = "poll_reconciler"

	ActionSchedule  Action = "schedule"
	ActionClaim     Action = "claim"
	ActionDispatch  Action = "dispatch"
	ActionFinalize  Action = "finalize"
	ActionReconcile Action = "reconcile"
	ActionShutdown  Action = "shutdown"

	LogResultSuccess LogResult = "success"
	LogResultFailure LogResult = "failure"
	LogResultSkipped LogResult = "skipped"

	ReasonNone                        Reason = "none"
	ReasonInvalidRuntimeConfig        Reason = "invalid_runtime_config"
	ReasonDatabaseUnavailable         Reason = "database_unavailable"
	ReasonAssetIneligible             Reason = "asset_ineligible"
	ReasonAssetInconsistent           Reason = "asset_inconsistent"
	ReasonCapacityUnavailable         Reason = "capacity_unavailable"
	ReasonInvalidClaim                Reason = "invalid_claim"
	ReasonGraceExhausted              Reason = "grace_exhausted"
	ReasonLeaseExpired                Reason = "lease_expired"
	ReasonLostFencing                 Reason = "lost_fencing"
	ReasonLostLease                   Reason = "lost_lease"
	ReasonAttemptsExhausted           Reason = "attempts_exhausted"
	ReasonNodeObservationFinalized    Reason = "node_observation_finalized"
	ReasonControlExecutionInterrupted Reason = "control_execution_interrupted"
	ReasonShutdown                    Reason = "shutdown"

	AttemptNone      AttemptBucket = ""
	AttemptFirst     AttemptBucket = "first"
	AttemptRetry     AttemptBucket = "retry"
	AttemptExhausted AttemptBucket = "exhausted"

	NodeTypeCLIProxyAPI NodeType = "cliproxyapi"
)

// LogRecord is a closed projection: there is no field for a poll ID, policy
// version, endpoint, provider account, Secret, response, version or error.
type LogRecord struct {
	Component     Component
	Action        Action
	Result        LogResult
	Reason        Reason
	NodeType      NodeType
	State         State
	AttemptBucket AttemptBucket
	InstanceID    uuid.UUID
}

func (LogRecord) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED PollLogRecord]"))
}

func (record LogRecord) Valid() bool {
	if !validComponent(record.Component) || !validAction(record.Action) || !validLogResult(record.Result) ||
		!validLogReason(record.Reason) || (record.NodeType != "" && record.NodeType != NodeTypeCLIProxyAPI) ||
		(record.State != "" && !record.State.Valid()) || !validAttemptBucket(record.AttemptBucket) {
		return false
	}
	return true
}

type Observer struct {
	logger *slog.Logger
}

func NewObserver(logger *slog.Logger) *Observer { return &Observer{logger: logger} }

func (observer *Observer) Record(ctx context.Context, record LogRecord) bool {
	if observer == nil || observer.logger == nil || !record.Valid() {
		return false
	}
	attributes := []slog.Attr{
		slog.String("component", string(record.Component)),
		slog.String("action", string(record.Action)),
		slog.String("result", string(record.Result)),
		slog.String("reason", string(record.Reason)),
	}
	if record.NodeType != "" {
		attributes = append(attributes, slog.String("node_type", string(record.NodeType)))
	}
	if record.State != "" {
		attributes = append(attributes, slog.String("state", string(record.State)))
	}
	if record.AttemptBucket != AttemptNone {
		attributes = append(attributes, slog.String("attempt_bucket", string(record.AttemptBucket)))
	}
	if record.InstanceID != uuid.Nil {
		attributes = append(attributes, slog.String("instance_id", record.InstanceID.String()))
	}
	observer.logger.LogAttrs(ctx, slog.LevelInfo, "account inventory poll", attributes...)
	return true
}

func validComponent(value Component) bool {
	return value == ComponentScheduler || value == ComponentWorker || value == ComponentReconciler
}

func validAction(value Action) bool {
	switch value {
	case ActionSchedule, ActionClaim, ActionDispatch, ActionFinalize, ActionReconcile, ActionShutdown:
		return true
	default:
		return false
	}
}

func validLogResult(value LogResult) bool {
	return value == LogResultSuccess || value == LogResultFailure || value == LogResultSkipped
}

func validLogReason(value Reason) bool {
	switch value {
	case ReasonNone, ReasonInvalidRuntimeConfig, ReasonDatabaseUnavailable, ReasonAssetIneligible,
		ReasonAssetInconsistent, ReasonCapacityUnavailable, ReasonInvalidClaim, ReasonGraceExhausted,
		ReasonLeaseExpired, ReasonLostFencing, ReasonLostLease, ReasonAttemptsExhausted, ReasonNodeObservationFinalized,
		ReasonControlExecutionInterrupted, ReasonShutdown:
		return true
	default:
		return false
	}
}

func validAttemptBucket(value AttemptBucket) bool {
	return value == AttemptNone || value == AttemptFirst || value == AttemptRetry || value == AttemptExhausted
}
