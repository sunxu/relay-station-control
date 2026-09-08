package tools_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"go.yaml.in/yaml/v3"
)

const openAPIPath = "../api/openapi.yaml"

type operationKey struct {
	method string
	path   string
}

func validateUniqueYAMLMappingKeys(node *yaml.Node, path string) error {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.MappingNode {
		seen := make(map[string]int, len(node.Content)/2)
		for index := 0; index < len(node.Content); index += 2 {
			key := node.Content[index]
			if key.Kind != yaml.ScalarNode {
				return fmt.Errorf("%s contains a non-scalar mapping key at line %d", path, key.Line)
			}
			if firstLine, exists := seen[key.Value]; exists {
				return fmt.Errorf("%s repeats mapping key %q at lines %d and %d", path, key.Value, firstLine, key.Line)
			}
			seen[key.Value] = key.Line
			if err := validateUniqueYAMLMappingKeys(node.Content[index+1], path+"."+key.Value); err != nil {
				return err
			}
		}
		return nil
	}
	for _, child := range node.Content {
		if err := validateUniqueYAMLMappingKeys(child, path); err != nil {
			return err
		}
	}
	if node.Kind == yaml.AliasNode {
		return validateUniqueYAMLMappingKeys(node.Alias, path)
	}
	return nil
}

func TestOpenAPIYAMLMappingsHaveUniqueKeys(t *testing.T) {
	contents, err := os.ReadFile(openAPIPath)
	if err != nil {
		t.Fatalf("read OpenAPI document: %v", err)
	}
	var document yaml.Node
	if err := yaml.Unmarshal(contents, &document); err != nil {
		t.Fatalf("parse OpenAPI YAML: %v", err)
	}
	if err := validateUniqueYAMLMappingKeys(&document, "openapi"); err != nil {
		t.Fatal(err)
	}

	duplicate := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Value: "name", Line: 1},
		{Kind: yaml.ScalarNode, Value: "first", Line: 1},
		{Kind: yaml.ScalarNode, Value: "name", Line: 2},
		{Kind: yaml.ScalarNode, Value: "second", Line: 2},
	}}
	if err := validateUniqueYAMLMappingKeys(duplicate, "fixture"); err == nil {
		t.Fatal("duplicate YAML mapping key fixture was accepted")
	}
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
		{http.MethodGet, "/api/healthz"}:                                                   "getHealthz",
		{http.MethodGet, "/api/bootstrap/status"}:                                          "getBootstrapStatus",
		{http.MethodPost, "/api/bootstrap/start"}:                                          "startBootstrap",
		{http.MethodPost, "/api/bootstrap/complete"}:                                       "completeBootstrap",
		{http.MethodPost, "/api/bootstrap/reset-pending"}:                                  "resetPendingBootstrap",
		{http.MethodPost, "/api/auth/login"}:                                               "login",
		{http.MethodPost, "/api/auth/mfa"}:                                                 "completeLoginMfa",
		{http.MethodPost, "/api/auth/logout"}:                                              "logout",
		{http.MethodGet, "/api/auth/session"}:                                              "getSession",
		{http.MethodPost, "/api/auth/reauthenticate"}:                                      "reauthenticate",
		{http.MethodPost, "/api/auth/password"}:                                            "changePassword",
		{http.MethodPost, "/api/auth/recovery-codes/regenerate"}:                           "regenerateRecoveryCodes",
		{http.MethodPost, "/api/admin-activations/complete"}:                               "completeAdministratorActivation",
		{http.MethodGet, "/api/admins"}:                                                    "listAdministrators",
		{http.MethodPost, "/api/admins"}:                                                   "createAdministrator",
		{http.MethodPost, "/api/admins/{id}/disable"}:                                      "disableAdministrator",
		{http.MethodPost, "/api/admins/{id}/activation-token"}:                             "regenerateAdministratorActivationToken",
		{http.MethodPost, "/api/admins/{id}/mfa-reset"}:                                    "resetAdministratorMfa",
		{http.MethodGet, "/api/environment"}:                                               "getEnvironment",
		{http.MethodGet, "/api/assets/gateway"}:                                            "getGatewayAsset",
		{http.MethodGet, "/api/assets/nodes"}:                                              "listNodeAssets",
		{http.MethodGet, "/api/assets/nodes/{instance_id}"}:                                "getNodeAsset",
		{http.MethodGet, "/api/assets/drivers"}:                                            "listNodeDrivers",
		{http.MethodGet, "/api/assets/provider-policies/current"}:                          "getCurrentProviderInventoryPolicy",
		{http.MethodGet, "/api/jobs"}:                                                      "listJobs",
		{http.MethodGet, "/api/jobs/{job_id}"}:                                             "getJob",
		{http.MethodPost, "/api/account-inventory/query"}:                                  "queryAccountInventory",
		{http.MethodGet, "/api/account-inventory/poll-capacity"}:                           "getAccountInventoryPollCapacity",
		{http.MethodGet, "/api/relay-bindings/nodes/{instance_id}"}:                        "getNodeRelayBinding",
		{http.MethodGet, "/api/relay-bindings/gateways/{instance_id}"}:                     "getGatewayAccountRelayBindings",
		{http.MethodGet, "/api/relay-bindings/unresolved"}:                                 "listUnresolvedRelayBindings",
		{http.MethodPost, "/api/relay-bindings/bind"}:                                      "bindRelayNode",
		{http.MethodPost, "/api/relay-bindings/rebind"}:                                    "rebindRelayNode",
		{http.MethodPost, "/api/relay-bindings/unbind"}:                                    "unbindRelayNode",
		{http.MethodGet, "/api/cross-node-duplicate-occurrences"}:                          "listCrossNodeDuplicateOccurrences",
		{http.MethodGet, "/api/cross-node-duplicate-occurrences/{occurrence_id}"}:          "getCrossNodeDuplicateOccurrence",
		{http.MethodGet, "/api/cross-node-duplicate-occurrences/{occurrence_id}/evidence"}: "listCrossNodeDuplicateOccurrenceEvidence",
		{http.MethodGet, "/api/topology/nodes/{instance_id}/duplicate-history"}:            "listNodeDuplicateHistory",
		{http.MethodGet, "/api/topology/nodes/{instance_id}/account-quality"}:              "getNodeAccountQuality",
		{http.MethodGet, "/api/account-inventory/nodes/{instance_id}/providers"}:           "getNodeInventoryProviderStates",
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

func TestOpenAPIAssetRegistryIsProtectedReadOnlyAndSecretFree(t *testing.T) {
	document := loadDocument(t)
	assets := map[string]string{
		"/api/environment":                      "#/components/schemas/EnvironmentAsset",
		"/api/assets/gateway":                   "#/components/schemas/GatewayAssetResponse",
		"/api/assets/nodes":                     "#/components/schemas/NodeAssetListResponse",
		"/api/assets/nodes/{instance_id}":       "#/components/schemas/NodeAsset",
		"/api/assets/drivers":                   "#/components/schemas/NodeDriverListResponse",
		"/api/assets/provider-policies/current": "#/components/schemas/CurrentProviderInventoryPolicyResponse",
	}
	for path, schemaRef := range assets {
		item := document.Paths.Find(path)
		if item == nil || item.Get == nil {
			t.Errorf("missing asset GET %s", path)
			continue
		}
		if len(item.Operations()) != 1 {
			t.Errorf("asset path %s declares non-GET operations", path)
		}
		if item.Get.Security != nil {
			t.Errorf("asset GET %s must inherit administrator session security", path)
		}
		for _, status := range []int{http.StatusUnauthorized, http.StatusServiceUnavailable} {
			if item.Get.Responses.Status(status) == nil {
				t.Errorf("asset GET %s lacks %d response", path, status)
			}
		}
		response := item.Get.Responses.Status(http.StatusOK)
		if response == nil || response.Value == nil {
			t.Errorf("asset GET %s lacks 200 response", path)
			continue
		}
		media := response.Value.Content.Get("application/json")
		if media == nil || media.Schema == nil || media.Schema.Ref != schemaRef {
			t.Errorf("asset GET %s schema = %#v, want %s", path, media, schemaRef)
		}
	}

	for _, schemaName := range []string{"GatewayAsset", "NodeAsset"} {
		schema := document.Components.Schemas[schemaName]
		if schema == nil || schema.Value == nil {
			t.Fatalf("missing schema %s", schemaName)
		}
		if _, ok := schema.Value.Properties["reader_secret_ref"]; ok {
			t.Errorf("schema %s exposes reader_secret_ref", schemaName)
		}
		if _, ok := schema.Value.Properties["secret_configured"]; !ok {
			t.Errorf("schema %s lacks secret_configured boolean", schemaName)
		}
	}
	canonicalStrings := map[string]struct {
		min     uint64
		pattern string
	}{
		"NodeType":              {min: 2, pattern: "^[a-z0-9][a-z0-9._-]*$"},
		"DriverContractVersion": {min: 1, pattern: "^[a-z0-9][a-z0-9._-]*$"},
		"ProviderName":          {min: 1, pattern: "^[a-z0-9][a-z0-9._-]*$"},
	}
	for schemaName, expected := range canonicalStrings {
		schema := document.Components.Schemas[schemaName]
		if schema == nil || schema.Value == nil || schema.Value.MinLength != expected.min || schema.Value.Pattern != expected.pattern {
			t.Errorf("schema %s canonical constraint = %#v, want min=%d pattern=%q", schemaName, schema, expected.min, expected.pattern)
		}
	}
}

func TestOpenAPIDurableJobsAreProtectedReadOnlyAndRedacted(t *testing.T) {
	document := loadDocument(t)
	jobs := map[string]string{
		"/api/jobs":          "#/components/schemas/JobListResponse",
		"/api/jobs/{job_id}": "#/components/schemas/JobDetail",
	}
	for path, schemaRef := range jobs {
		item := document.Paths.Find(path)
		if item == nil || item.Get == nil {
			t.Errorf("missing durable job GET %s", path)
			continue
		}
		if len(item.Operations()) != 1 {
			t.Errorf("durable job path %s declares non-GET operations", path)
		}
		if item.Get.Security != nil {
			t.Errorf("durable job GET %s must inherit administrator session security", path)
		}
		for _, status := range []int{http.StatusUnauthorized, http.StatusServiceUnavailable} {
			if item.Get.Responses.Status(status) == nil {
				t.Errorf("durable job GET %s lacks %d response", path, status)
			}
		}
		response := item.Get.Responses.Status(http.StatusOK)
		if response == nil || response.Value == nil {
			t.Errorf("durable job GET %s lacks 200 response", path)
			continue
		}
		media := response.Value.Content.Get("application/json")
		if media == nil || media.Schema == nil || media.Schema.Ref != schemaRef {
			t.Errorf("durable job GET %s schema = %#v, want %s", path, media, schemaRef)
		}
	}

	forbidden := map[string]bool{
		"payload": true, "payload_hash": true, "idempotency_key": true,
		"lease_owner": true, "lease_fencing_token": true, "lease_expires_at": true,
		"error_summary": true, "envelope": true,
	}
	for _, schemaName := range []string{"JobSummary", "JobDetail", "JobLifecycleEvent"} {
		schema := document.Components.Schemas[schemaName]
		if schema == nil || schema.Value == nil {
			t.Fatalf("missing schema %s", schemaName)
		}
		for property := range schema.Value.Properties {
			if forbidden[property] {
				t.Errorf("schema %s exposes forbidden property %s", schemaName, property)
			}
		}
	}
}

func TestOpenAPIJobDetailResponseSchemaIsSatisfiableAndStrict(t *testing.T) {
	document := loadDocument(t)
	operation := document.Paths.Find("/api/jobs/{job_id}").Get
	response := operation.Responses.Status(http.StatusOK)
	if response == nil || response.Value == nil {
		t.Fatal("job detail GET lacks a resolved 200 response")
	}
	media := response.Value.Content.Get("application/json")
	if media == nil || media.Schema == nil || media.Schema.Value == nil {
		t.Fatal("job detail GET lacks a resolved JSON response schema")
	}

	valid := map[string]any{
		"job_id":           "00000000-0000-4000-8000-000000000101",
		"operation_id":     "00000000-0000-4000-8000-000000000201",
		"job_kind":         "synthetic.noop",
		"status":           "running",
		"attempt_count":    1,
		"max_attempts":     3,
		"available_at":     "2026-08-25T10:00:00Z",
		"started_at":       "2026-08-25T10:01:00Z",
		"completed_at":     nil,
		"cancel_requested": false,
		"error_code":       nil,
		"outbox_status":    "suppressed",
		"created_at":       "2026-08-25T10:00:00Z",
		"updated_at":       "2026-08-25T10:01:00Z",
		"events": []any{map[string]any{
			"sequence":      1,
			"event_type":    "enqueued",
			"from_status":   nil,
			"to_status":     "pending",
			"attempt_count": 0,
			"actor_type":    "service",
			"reason_code":   "job_enqueued",
			"error_code":    nil,
			"occurred_at":   "2026-08-25T10:00:00Z",
		}},
	}
	options := []openapi3.SchemaValidationOption{openapi3.EnableJSONSchema2020(), openapi3.EnableFormatValidation()}
	if err := media.Schema.Value.VisitJSON(valid, options...); err != nil {
		t.Fatalf("valid job detail response does not satisfy its schema: %v", err)
	}

	withForbiddenField := make(map[string]any, len(valid)+1)
	for key, value := range valid {
		withForbiddenField[key] = value
	}
	withForbiddenField["payload"] = map[string]any{"secret": "canary"}
	if err := media.Schema.Value.VisitJSON(withForbiddenField, options...); err == nil {
		t.Fatal("job detail response schema accepted a forbidden unknown property")
	}

	withoutEvents := make(map[string]any, len(valid)-1)
	for key, value := range valid {
		if key != "events" {
			withoutEvents[key] = value
		}
	}
	if err := media.Schema.Value.VisitJSON(withoutEvents, options...); err == nil {
		t.Fatal("job detail response schema accepted a response without events")
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

func TestTopologyReadAndGatewayIdentityContracts(t *testing.T) {
	doc := loadDocument(t)
	for _, path := range []string{"/api/topology/nodes/{instance_id}/duplicate-history", "/api/account-inventory/nodes/{instance_id}/providers"} {
		item := doc.Paths.Find(path)
		if item == nil || item.Get == nil || len(item.Operations()) != 1 {
			t.Fatalf("not a GET-only surface: %s", path)
		}
		if item.Get.Security != nil {
			t.Fatalf("must inherit session security: %s", path)
		}
		for _, code := range []int{400, 401, 403, 404, 503} {
			if item.Get.Responses.Status(code) == nil {
				t.Fatalf("missing %d: %s", code, path)
			}
		}
	}
	fields := map[string]string{"GatewayAccountContext": "account_id", "RelayNodeGatewayAccountBindingDetail": "gateway_account_id", "NodeRelayBindingResponse": "gateway_account_id", "GatewayAccountCentricBindingItem": "gateway_account_id", "BindRelayNodeRequest": "gateway_account_id", "RebindRelayNodeRequest": "new_gateway_account_id"}
	for name, field := range fields {
		schema := doc.Components.Schemas[name].Value.Properties[field].Value
		for _, value := range []string{"9007199254740991", "9007199254740992", "9007199254740993", "9223372036854775807"} {
			if err := schema.VisitJSON(value); err != nil {
				t.Fatalf("%s rejected string: %v", name, err)
			}
		}
		if err := schema.VisitJSON(float64(42)); err == nil {
			t.Fatalf("%s accepted numeric identity", name)
		}
		for _, value := range []string{"0", "-1", "01", "+1", "1.0", "1e3", " 1", ""} {
			if err := schema.VisitJSON(value); err == nil {
				t.Fatalf("%s accepted %q", name, value)
			}
		}
	}
	provider := doc.Components.Schemas["NodeInventoryProviderState"].Value
	for _, field := range []string{"state", "health_reason"} {
		if err := provider.Properties[field].Value.VisitJSON(nil); err != nil {
			t.Fatalf("%s must permit not-yet-observed null: %v", field, err)
		}
		if err := provider.Properties[field].Value.VisitJSON("raw unsafe error"); err == nil {
			t.Fatalf("%s accepted unsafe value", field)
		}
	}
	if len(provider.Properties) != 9 || len(provider.Required) != 9 {
		t.Fatal("provider projection must contain exactly nine required fields")
	}
}
