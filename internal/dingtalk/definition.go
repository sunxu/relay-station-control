package dingtalk

import (
	"time"

	"github.com/sunxu/relay-station-control/internal/jobs"
)

const JobKind = "dingtalk_alert_delivery"

func Definition(executor jobs.Executor) jobs.Definition {
	return jobs.Definition{
		Kind: JobKind, SchemaVersion: 1, Schema: payloadSchema,
		ValidatePayload: validateCanonicalPayload,
		Timeout: 10 * time.Second, LeaseDuration: 30 * time.Second,
		HeartbeatInterval: 5 * time.Second, MaxAttempts: 5, MaxVerifyAttempts: 1,
		ReplaySafe: true, AllowUnknownEffectReplay: true, AllowDirectSuccess: true,
		AllowRollback: false, Executor: executor,
		ErrorCodes: map[string]struct{}{
			"dingtalk_disabled": {}, "dingtalk_invalid_payload": {},
			"dingtalk_config_invalid": {}, "dingtalk_connect_failed": {},
			"dingtalk_result_unknown": {}, "dingtalk_http_retry": {},
			"dingtalk_http_rejected": {}, "dingtalk_business_retry": {},
			"dingtalk_business_rejected": {}, "dingtalk_invalid_response": {},
			"dingtalk_unsupported_operation": {},
		},
	}
}
