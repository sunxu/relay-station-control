package jobs

import "context"

type Component string
type Action string
type Result string

const (
	ComponentWorker     Component = "worker"
	ComponentReconciler Component = "reconciler"
	ComponentDispatcher Component = "dispatcher"

	ActionClaim    Action = "claim"
	ActionExecute  Action = "execute"
	ActionVerify   Action = "verify"
	ActionRollback Action = "rollback"
	ActionPublish  Action = "publish"

	ResultSuccess Result = "success"
	ResultFailure Result = "failure"
	ResultSkipped Result = "skipped"
)

type LogRecord struct {
	Component Component
	Action    Action
	Result    Result
	JobKind   string
	ErrorCode string
}

type Logger interface {
	Log(context.Context, LogRecord)
}

type noopLogger struct{}

func (noopLogger) Log(context.Context, LogRecord) {}

var fixedLogErrorCodes = map[string]struct{}{
	"database_unavailable": {}, "lost_lease": {}, "invalid_transition": {},
	"lease_or_timeout": {}, "unknown_job_definition": {}, "job_policy_mismatch": {},
	"payload_integrity_failed": {}, "execution_result_unknown": {},
	"max_attempts_exhausted": {}, "permanent_execution_failure": {},
	"invalid_executor_result": {}, "cancel_after_effect_applied": {}, "cancel_after_unknown_effect": {},
	"cancel_verified_safe": {}, "replay_not_permitted": {}, "rollback_not_permitted": {},
	"job_deadline_exceeded": {}, "verification_exhausted": {}, "effect_unknown": {},
	"invalid_verify_result": {}, "rollback_exhausted": {}, "rollback_result_unknown": {},
	"permanent_rollback_failure": {}, "invalid_rollback_result": {},
	"invalid_transition_mutation": {}, "invalid_wake_envelope": {},
	"publisher_unavailable": {}, "unclassified_executor_error": {},
}

// Valid prevents identifiers, payloads, arbitrary errors, URLs, SQL and
// external responses from entering the structured logging contract.
func (record LogRecord) Valid(registry *Registry) bool {
	if record.Component != ComponentWorker && record.Component != ComponentReconciler && record.Component != ComponentDispatcher {
		return false
	}
	if record.Result != ResultSuccess && record.Result != ResultFailure && record.Result != ResultSkipped {
		return false
	}
	if record.Action != ActionClaim && record.Action != ActionExecute && record.Action != ActionVerify &&
		record.Action != ActionRollback && record.Action != ActionPublish {
		return false
	}
	var definition Definition
	if record.JobKind != "" {
		if registry == nil {
			return false
		}
		var ok bool
		if definition, ok = registry.Lookup(record.JobKind); !ok {
			return false
		}
	}
	if record.ErrorCode == "" {
		return true
	}
	if isFrameworkReasonCode(record.ErrorCode) {
		return false
	}
	if _, ok := fixedLogErrorCodes[record.ErrorCode]; ok {
		return true
	}
	_, ok := definition.ErrorCodes[record.ErrorCode]
	return record.JobKind != "" && ok
}

func normalizedLogger(logger Logger) Logger {
	if logger == nil {
		return noopLogger{}
	}
	return logger
}

func emitLog(ctx context.Context, logger Logger, registry *Registry, record LogRecord) {
	if record.Valid(registry) {
		logger.Log(ctx, record)
	}
}

func repositoryErrorCode(err error) string {
	if err == nil {
		return ""
	}
	if errorsIs(err, ErrLostLease) {
		return "lost_lease"
	}
	if errorsIs(err, ErrInvalidTransition) {
		return "invalid_transition"
	}
	return "database_unavailable"
}

func emitTransitionLog(ctx context.Context, logger Logger, registry *Registry, component Component, action Action, kind string, status Status, transitionCode string, err error) {
	result := ResultSuccess
	code := transitionCode
	if status == StatusFailed {
		result = ResultFailure
	} else if status == StatusCancelled {
		result = ResultSkipped
	}
	if err == nil && !status.Valid() {
		result = ResultFailure
		code = "invalid_transition"
	}
	if err != nil {
		result = ResultFailure
		code = repositoryErrorCode(err)
	}
	if kind != "" {
		if registry == nil {
			kind = ""
		} else if _, registered := registry.Lookup(kind); !registered {
			kind = ""
		}
	}
	emitLog(ctx, logger, registry, LogRecord{Component: component, Action: action, Result: result, JobKind: kind, ErrorCode: code})
}
