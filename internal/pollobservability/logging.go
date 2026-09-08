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
	ActionPromote   Action = "promote"
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
	ReasonCapacityExceeded            Reason = "capacity_exceeded"
	ReasonInvalidClaim                Reason = "invalid_claim"
	ReasonGraceExhausted              Reason = "grace_exhausted"
	ReasonLeaseExpired                Reason = "lease_expired"
	ReasonLostFencing                 Reason = "lost_fencing"
	ReasonLostLease                   Reason = "lost_lease"
	ReasonAttemptsExhausted           Reason = "attempts_exhausted"
	ReasonNodeObservationFinalized    Reason = "node_observation_finalized"
	ReasonControlExecutionInterrupted Reason = "control_execution_interrupted"
	ReasonPolicyChanged               Reason = "policy_changed"
	ReasonTransportFailed             Reason = "transport_failed"
	ReasonContractInvalid             Reason = "contract_invalid"
	ReasonDiskFallback                Reason = "disk_fallback"
	ReasonProviderIdentityIncomplete  Reason = "provider_identity_incomplete"
	ReasonProviderDuplicate           Reason = "provider_duplicate"
	ReasonStalePoll                   Reason = "stale_poll"
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
	Provider      string
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
	if record.Action == ActionPromote {
		if record.Component != ComponentWorker || record.InstanceID == uuid.Nil || record.State != StateFinalized ||
			len(record.Provider) > maximumProviderBytes || !providerLabelPattern.MatchString(record.Provider) {
			return false
		}
		if record.Result == LogResultSuccess {
			return record.Reason == ReasonNone
		}
		return record.Result == LogResultSkipped && validPromotionLogReason(record.Reason)
	}
	if record.Provider != "" || validPromotionLogReason(record.Reason) {
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
	if record.Provider != "" {
		attributes = append(attributes, slog.String("provider", record.Provider))
	}
	observer.logger.LogAttrs(ctx, slog.LevelInfo, "account inventory poll", attributes...)
	return true
}

func validComponent(value Component) bool {
	return value == ComponentScheduler || value == ComponentWorker || value == ComponentReconciler
}

func validAction(value Action) bool {
	switch value {
	case ActionSchedule, ActionClaim, ActionDispatch, ActionFinalize, ActionPromote, ActionReconcile, ActionShutdown:
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
		ReasonAssetInconsistent, ReasonCapacityUnavailable, ReasonCapacityExceeded, ReasonInvalidClaim, ReasonGraceExhausted,
		ReasonLeaseExpired, ReasonLostFencing, ReasonLostLease, ReasonAttemptsExhausted, ReasonNodeObservationFinalized,
		ReasonControlExecutionInterrupted, ReasonPolicyChanged, ReasonTransportFailed, ReasonContractInvalid,
		ReasonDiskFallback, ReasonProviderIdentityIncomplete, ReasonProviderDuplicate, ReasonStalePoll, ReasonShutdown:
		return true
	default:
		return false
	}
}

func validPromotionLogReason(value Reason) bool {
	switch value {
	case ReasonPolicyChanged, ReasonTransportFailed, ReasonContractInvalid, ReasonDiskFallback,
		ReasonProviderIdentityIncomplete, ReasonProviderDuplicate, ReasonStalePoll:
		return true
	default:
		return false
	}
}

func validAttemptBucket(value AttemptBucket) bool {
	return value == AttemptNone || value == AttemptFirst || value == AttemptRetry || value == AttemptExhausted
}
