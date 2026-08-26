package pollobservability

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestObserverEmitsOnlyClosedLogAllowlist(t *testing.T) {
	var output bytes.Buffer
	observer := NewObserver(slog.New(slog.NewJSONHandler(&output, nil)))
	record := LogRecord{
		Component: ComponentWorker, Action: ActionFinalize, Result: LogResultSuccess,
		Reason: ReasonNodeObservationFinalized, NodeType: NodeTypeCLIProxyAPI,
		State: StateFinalized, AttemptBucket: AttemptFirst, InstanceID: uuid.New(),
	}
	if !observer.Record(context.Background(), record) {
		t.Fatal("valid poll log was dropped")
	}
	var fields map[string]any
	if err := json.Unmarshal(output.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]struct{}{
		"time": {}, "level": {}, "msg": {}, "component": {}, "action": {}, "result": {},
		"reason": {}, "node_type": {}, "state": {}, "attempt_bucket": {}, "instance_id": {},
	}
	for field := range fields {
		if _, ok := allowed[field]; !ok {
			t.Fatalf("log field outside allowlist: %s", field)
		}
	}
	for _, forbidden := range []string{
		"poll_run_id", "policy_version", "provider", "email", "account", "endpoint", "hostname", "ip",
		"secret", "management_key", "version", "commit", "error", "body", "header",
	} {
		if _, exists := fields[forbidden]; exists {
			t.Fatalf("forbidden poll log field %q", forbidden)
		}
	}
}

func TestObserverDropsCanariesAcrossEveryUntrustedField(t *testing.T) {
	canary := "endpoint-secret-email-body-header-error-canary"
	invalid := []LogRecord{
		{Component: Component(canary), Action: ActionSchedule, Result: LogResultFailure, Reason: ReasonDatabaseUnavailable},
		{Component: ComponentWorker, Action: Action(canary), Result: LogResultFailure, Reason: ReasonDatabaseUnavailable},
		{Component: ComponentWorker, Action: ActionClaim, Result: LogResult(canary), Reason: ReasonDatabaseUnavailable},
		{Component: ComponentWorker, Action: ActionClaim, Result: LogResultFailure, Reason: Reason(canary)},
		{Component: ComponentWorker, Action: ActionClaim, Result: LogResultFailure, Reason: ReasonDatabaseUnavailable, NodeType: NodeType(canary)},
		{Component: ComponentWorker, Action: ActionClaim, Result: LogResultFailure, Reason: ReasonDatabaseUnavailable, State: State(canary)},
		{Component: ComponentWorker, Action: ActionClaim, Result: LogResultFailure, Reason: ReasonDatabaseUnavailable, AttemptBucket: AttemptBucket(canary)},
	}
	var output bytes.Buffer
	observer := NewObserver(slog.New(slog.NewJSONHandler(&output, nil)))
	for _, record := range invalid {
		if observer.Record(context.Background(), record) {
			t.Fatal("canary-bearing record was accepted")
		}
		for _, format := range []string{"%v", "%+v", "%#v"} {
			if strings.Contains(fmt.Sprintf(format, record), canary) {
				t.Fatalf("record formatter %s leaked canary", format)
			}
		}
	}
	if strings.Contains(output.String(), canary) || output.Len() != 0 {
		t.Fatalf("invalid log emitted output: %s", output.String())
	}
}
