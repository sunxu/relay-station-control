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

// RuntimeSecretExecutorForTest uses the same production client with only the
// existing fixture CA trust override. Secrets are ephemeral test inputs.
func RuntimeSecretExecutorForTest(t *testing.T, server *httptest.Server, token, secret string) *Executor {
	t.Helper()
	e := executorAt(t, server, secret)
	config, err := LoadConfig(func(key string) string {
		if key == "DINGTALK_WEBHOOK_URL" {
			return server.URL + "/robot/send?access_token=" + token
		}
		return secret
	})
	if err != nil {
		t.Fatal("synthetic runtime configuration invalid")
	}
	e.config = config
	return e
}
