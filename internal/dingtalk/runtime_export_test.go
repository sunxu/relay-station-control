package dingtalk

import (
	"net/http/httptest"
	"testing"
)

// ExecutorAtForRuntimeTest exposes the existing trusted httptest TLS wiring to
// the runtime-focused tests. It remains test-only; production has no test
// transport hook or certificate override.
func ExecutorAtForRuntimeTest(t *testing.T, server *httptest.Server, secret string) *Executor {
	return executorAt(t, server, secret)
}
