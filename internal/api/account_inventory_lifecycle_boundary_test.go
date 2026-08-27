package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAccountInventoryLifecycleProductAPIEnumerationBoundary(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	contract, err := os.ReadFile(filepath.Join(repositoryRoot, "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"/api/account-inventory",
		"/api/account-inventory/lifecycle",
		"/api/accounts",
		"CurrentAccountInventoryLifecycle",
	} {
		if strings.Contains(string(contract), forbidden) {
			t.Fatalf("product contract unexpectedly exposes account lifecycle boundary %q", forbidden)
		}
	}

	handler := HandlerWithOptions(NewServer("test"), ChiServerOptions{})
	identityCanary := "enumeration-canary@example.invalid"
	paths := []string{
		"/api/account-inventory",
		"/api/account-inventory/lifecycle",
		"/api/account-inventory/lifecycle?email=" + identityCanary,
		"/api/accounts",
		"/api/assets/nodes/00000000-0000-0000-0000-000000000000/account-inventory",
	}
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		for _, path := range paths {
			request := httptest.NewRequest(method, path, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusNotFound {
				t.Fatalf("unexpected lifecycle product route method=%s path=%s status=%d", method, path, response.Code)
			}
			if body := response.Body.String(); strings.Contains(body, identityCanary) ||
				strings.Contains(strings.ToLower(body), "account_inventory") ||
				strings.Contains(strings.ToLower(body), "lifecycle") {
				t.Fatal("unregistered lifecycle route echoed enumeration input or implementation detail")
			}
		}
	}
}
