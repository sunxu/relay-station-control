package tools_test

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

const openAPIPath = "../api/openapi.yaml"

type operationKey struct {
	method string
	path   string
}

func loadDocument(t *testing.T) *openapi3.T {
	t.Helper()

	loader := openapi3.NewLoader()
	document, err := loader.LoadFromFile(openAPIPath)
	if err != nil {
		t.Fatalf("load OpenAPI document: %v", err)
	}
	if err := document.Validate(context.Background()); err != nil {
		t.Fatalf("validate OpenAPI document: %v", err)
	}
	return document
}

func allOperations(document *openapi3.T) map[operationKey]*openapi3.Operation {
	operations := make(map[operationKey]*openapi3.Operation)
	for path, item := range document.Paths.Map() {
		for method, operation := range item.Operations() {
			operations[operationKey{method: method, path: path}] = operation
		}
	}
	return operations
}

func TestOpenAPIContainsAuthenticationFoundationOperations(t *testing.T) {
	document := loadDocument(t)
	operations := allOperations(document)

	expected := map[operationKey]string{
		{http.MethodGet, "/api/healthz"}:                         "getHealthz",
		{http.MethodGet, "/api/bootstrap/status"}:                "getBootstrapStatus",
		{http.MethodPost, "/api/bootstrap/start"}:                "startBootstrap",
		{http.MethodPost, "/api/bootstrap/complete"}:             "completeBootstrap",
		{http.MethodPost, "/api/bootstrap/reset-pending"}:        "resetPendingBootstrap",
		{http.MethodPost, "/api/auth/login"}:                     "login",
		{http.MethodPost, "/api/auth/mfa"}:                       "completeLoginMfa",
		{http.MethodPost, "/api/auth/logout"}:                    "logout",
		{http.MethodGet, "/api/auth/session"}:                    "getSession",
		{http.MethodPost, "/api/auth/reauthenticate"}:            "reauthenticate",
		{http.MethodPost, "/api/auth/password"}:                  "changePassword",
		{http.MethodPost, "/api/auth/recovery-codes/regenerate"}: "regenerateRecoveryCodes",
		{http.MethodPost, "/api/admin-activations/complete"}:     "completeAdministratorActivation",
		{http.MethodGet, "/api/admins"}:                          "listAdministrators",
		{http.MethodPost, "/api/admins"}:                         "createAdministrator",
		{http.MethodPost, "/api/admins/{id}/disable"}:            "disableAdministrator",
		{http.MethodPost, "/api/admins/{id}/activation-token"}:   "regenerateAdministratorActivationToken",
		{http.MethodPost, "/api/admins/{id}/mfa-reset"}:          "resetAdministratorMfa",
	}

	if len(operations) != len(expected) {
		t.Fatalf("operation count = %d, want %d; operations: %v", len(operations), len(expected), sortedOperationKeys(operations))
	}
	for key, operationID := range expected {
		operation, ok := operations[key]
		if !ok {
			t.Errorf("missing %s %s", key.method, key.path)
			continue
		}
		if operation.OperationID != operationID {
			t.Errorf("%s %s operationId = %q, want %q", key.method, key.path, operation.OperationID, operationID)
		}
	}
}

func TestOpenAPISecurityBoundary(t *testing.T) {
	document := loadDocument(t)
	if len(document.Security) != 1 {
		t.Fatalf("top-level security requirements = %d, want 1", len(document.Security))
	}
	if _, ok := document.Security[0]["SessionCookie"]; !ok {
		t.Fatalf("top-level security does not require SessionCookie: %#v", document.Security)
	}
	scheme := document.Components.SecuritySchemes["SessionCookie"]
	if scheme == nil || scheme.Value == nil {
		t.Fatal("SessionCookie security scheme is missing")
	}
	if scheme.Value.Type != "apiKey" || scheme.Value.In != "cookie" || scheme.Value.Name != "__Host-relay_control_session" {
		t.Fatalf("SessionCookie = type %q, in %q, name %q", scheme.Value.Type, scheme.Value.In, scheme.Value.Name)
	}

	anonymous := map[operationKey]bool{
		{http.MethodGet, "/api/healthz"}:                     true,
		{http.MethodGet, "/api/bootstrap/status"}:            true,
		{http.MethodPost, "/api/bootstrap/start"}:            true,
		{http.MethodPost, "/api/bootstrap/complete"}:         true,
		{http.MethodPost, "/api/bootstrap/reset-pending"}:    true,
		{http.MethodPost, "/api/auth/login"}:                 true,
		{http.MethodPost, "/api/auth/mfa"}:                   true,
		{http.MethodPost, "/api/auth/logout"}:                true,
		{http.MethodPost, "/api/admin-activations/complete"}: true,
	}

	for key, operation := range allOperations(document) {
		if anonymous[key] {
			if operation.Security == nil || len(*operation.Security) != 0 {
				t.Errorf("%s %s must explicitly opt out of session security", key.method, key.path)
			}
			continue
		}
		if operation.Security != nil {
			t.Errorf("%s %s must inherit default SessionCookie security", key.method, key.path)
		}
	}
}

func TestOpenAPIProtectedUnsafeOperationsRequireCSRF(t *testing.T) {
	document := loadDocument(t)
	for key, operation := range allOperations(document) {
		if key.method != http.MethodPost || operation.Security != nil {
			continue
		}
		var found bool
		for _, parameterRef := range operation.Parameters {
			if parameterRef.Value != nil && parameterRef.Value.In == openapi3.ParameterInHeader && parameterRef.Value.Name == "X-CSRF-Token" {
				found = parameterRef.Value.Required
			}
		}
		if !found {
			t.Errorf("protected unsafe operation %s %s lacks required X-CSRF-Token", key.method, key.path)
		}
	}
}

func TestOpenAPILogoutIsIdempotentWithoutASession(t *testing.T) {
	document := loadDocument(t)
	operation := document.Paths.Find("/api/auth/logout").Post
	if operation.Security == nil || len(*operation.Security) != 0 {
		t.Fatal("logout must explicitly allow a missing or already revoked session")
	}
	if operation.Responses.Status(http.StatusNoContent) == nil {
		t.Fatal("logout does not declare the idempotent 204 response")
	}
	if operation.Responses.Status(http.StatusUnauthorized) != nil {
		t.Fatal("logout must not return 401 for a missing or already revoked session")
	}
	for _, parameterRef := range operation.Parameters {
		if parameterRef.Value != nil && parameterRef.Value.Name == "X-CSRF-Token" && parameterRef.Value.Required {
			t.Fatal("logout CSRF header must be conditionally required only when a parseable session cookie is present")
		}
	}
}

func TestOpenAPIBootstrapCompletionDeclaresInvalidTOTP(t *testing.T) {
	document := loadDocument(t)
	operation := document.Paths.Find("/api/bootstrap/complete").Post
	response := operation.Responses.Status(http.StatusUnauthorized)
	if response == nil || response.Ref != "#/components/responses/Error" {
		t.Fatalf("bootstrap completion 401 response = %#v, want uniform Error response", response)
	}
}

func TestOpenAPISensitiveResponsesAreNoStoreAndErrorsAreUniform(t *testing.T) {
	document := loadDocument(t)
	for key, operation := range allOperations(document) {
		if key.path == "/api/healthz" {
			continue
		}
		for _, status := range operation.Responses.Keys() {
			responseRef := operation.Responses.Value(status)
			if responseRef == nil || responseRef.Value == nil {
				t.Errorf("%s %s response %s is unresolved", key.method, key.path, status)
				continue
			}
			cacheControl := responseRef.Value.Headers["Cache-Control"]
			if cacheControl == nil {
				t.Errorf("%s %s response %s lacks Cache-Control: no-store", key.method, key.path, status)
			} else if cacheControl.Ref != "#/components/headers/NoStore" {
				t.Errorf("%s %s response %s Cache-Control does not use the no-store contract", key.method, key.path, status)
			}
			if status[0] != '2' && responseRef.Ref != "#/components/responses/Error" {
				t.Errorf("%s %s response %s does not use the uniform Error response", key.method, key.path, status)
			}
		}
	}
}

func sortedOperationKeys(operations map[operationKey]*openapi3.Operation) []string {
	keys := make([]string, 0, len(operations))
	for key := range operations {
		keys = append(keys, fmt.Sprintf("%s %s", key.method, key.path))
	}
	sort.Strings(keys)
	return keys
}
