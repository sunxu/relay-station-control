package dingtalk

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sunxu/relay-station-control/internal/jobs"
)

type noopExecutor struct{}

func (noopExecutor) Execute(context.Context, jobs.Execution) jobs.ExecuteResult {
	return jobs.ExecuteResult{}
}
func (noopExecutor) Verify(context.Context, jobs.Execution) jobs.VerifyResult {
	return jobs.VerifyResult{}
}
func (noopExecutor) Rollback(context.Context, jobs.Execution) jobs.RollbackResult {
	return jobs.RollbackResult{}
}

func TestDefinitionContract(t *testing.T) {
	definition := Definition(noopExecutor{})
	if definition.Kind != JobKind || definition.SchemaVersion != 1 || definition.Timeout != 10*time.Second || definition.LeaseDuration != 30*time.Second || definition.HeartbeatInterval != 5*time.Second || definition.MaxAttempts != 5 || definition.MaxVerifyAttempts != 1 || !definition.ReplaySafe || !definition.AllowUnknownEffectReplay || !definition.AllowDirectSuccess || definition.AllowRollback {
		t.Fatalf("definition contract = %+v", definition)
	}
	if len(definition.Schema.Fields) != 14 {
		t.Fatalf("schema field count = %d, want 14", len(definition.Schema.Fields))
	}
	if _, err := jobs.NewRegistry(definition); err != nil {
		t.Fatalf("definition rejected by jobs registry: %v", err)
	}
	nodeNames := definition.Schema.Fields["node_names"]
	if !nodeNames.AllowDuplicates || !nodeNames.PreserveOrder {
		t.Fatal("node_names must preserve positional duplicate display names")
	}
	if definition.ValidatePayload == nil {
		t.Fatal("definition must validate canonical payload semantics")
	}
	for _, code := range []string{"dingtalk_disabled", "dingtalk_invalid_payload", "dingtalk_config_invalid", "dingtalk_connect_failed", "dingtalk_result_unknown", "dingtalk_http_retry", "dingtalk_http_rejected", "dingtalk_business_retry", "dingtalk_business_rejected", "dingtalk_invalid_response", "dingtalk_unsupported_operation"} {
		if _, ok := definition.ErrorCodes[code]; !ok {
			t.Fatalf("missing error code %q", code)
		}
	}
}

func TestDefinitionRegistryRejectsInvalidIssueTuples(t *testing.T) {
	registry, err := jobs.NewRegistry(Definition(noopExecutor{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range [][]byte{
		bytes.Replace(validPayloadJSON(), []byte(`"TOKEN_INVALID"`), []byte(`"FORBIDDEN"`), 1),
		bytes.Replace(validPayloadJSON(), []byte(`"token_invalid"`), []byte(`"account_blocked"`), 1),
	} {
		if _, _, _, err := registry.ValidateAndHash(JobKind, 1, raw); !errors.Is(err, jobs.ErrInvalidPayload) {
			t.Fatalf("got %v, want ErrInvalidPayload", err)
		}
	}
}
