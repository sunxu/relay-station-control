package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/sunxu/relay-station-control/internal/jobs"
)

func TestDingTalkFinalFailureUsesExistingErrorLogger(t *testing.T) {
	for _, tc := range []struct {
		result      jobs.Result
		code, level string
	}{
		{jobs.ResultFailure, "max_attempts_exhausted", "ERROR"},
		{jobs.ResultFailure, "dingtalk_business_rejected", "ERROR"},
		{jobs.ResultSuccess, "", "INFO"},
		{jobs.ResultSkipped, "cancel_verified_safe", "INFO"},
	} {
		t.Run(string(tc.result)+tc.code, func(t *testing.T) {
			var output bytes.Buffer
			adapter := jobSlogLogger{logger: slog.New(slog.NewJSONHandler(&output, nil))}
			adapter.Log(context.Background(), jobs.LogRecord{Component: jobs.ComponentWorker,
				Action: jobs.ActionExecute, Result: tc.result, JobKind: "dingtalk_alert_delivery", ErrorCode: tc.code})
			var fields map[string]any
			if err := json.Unmarshal(output.Bytes(), &fields); err != nil {
				t.Fatal(err)
			}
			if fields["level"] != tc.level || fields["result"] != string(tc.result) || fields["error_code"] != tc.code || fields["job_kind"] != "dingtalk_alert_delivery" {
				t.Fatal("durable lifecycle log lost safe failure diagnostics or severity")
			}
			allowed := map[string]bool{"time": true, "level": true, "msg": true, "component": true, "action": true, "result": true, "job_kind": true, "error_code": true}
			for key := range fields {
				if !allowed[key] {
					t.Fatal("unexpected durable log field")
				}
			}
		})
	}
}
