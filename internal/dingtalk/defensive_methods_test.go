package dingtalk

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/sunxu/relay-station-control/internal/jobs"
)

// These defensive interface methods must not create a remote verification or
// compensation protocol, even when an enabled executor has a valid payload.
func TestDefensiveVerifyRollbackDoNotPerformIO(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	executor := executorAt(t, server, "signing-canary")
	execution := jobs.Execution{Payload: executorTestPayload(), Attempt: 1}
	for _, cancelled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		if cancelled {
			cancel()
		}
		verified := executor.Verify(ctx, execution)
		rolledBack := executor.Rollback(ctx, execution)
		cancel()
		if verified.Disposition != jobs.VerifyEffectUnknown || verified.Mutation != nil {
			t.Fatal("defensive Verify must fail closed without a mutation")
		}
		if rolledBack.Disposition != jobs.RollbackFailed || rolledBack.Mutation != nil {
			t.Fatal("defensive Rollback must fail closed without a mutation")
		}
	}
	if hits.Load() != 0 {
		t.Fatal("defensive methods performed HTTP I/O")
	}
}
