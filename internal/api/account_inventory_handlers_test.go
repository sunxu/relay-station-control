package api

import (
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestAccountInventoryRequestValidation(t *testing.T) {
	instanceID := uuid.New()
	provider := ProviderName("openai")
	lifecycle := AccountInventoryLifecyclePresent
	basicStatus := AccountInventoryBasicStatusReportedActive
	email := NormalizedAccountEmail("operator@example.com")
	cursor := strings.Repeat("a", maxAccountInventoryCursorBytes)
	limit := maxAccountInventoryPageLimit
	valid := AccountInventoryQueryRequest{
		InstanceId: instanceID, Provider: &provider, Lifecycle: &lifecycle,
		BasicStatus: &basicStatus, Email: &email, Cursor: &cursor, Limit: &limit,
	}
	if !validAccountInventoryRequest(valid) {
		t.Fatal("valid account inventory request was rejected")
	}

	for name, mutate := range map[string]func(*AccountInventoryQueryRequest){
		"nil instance": func(request *AccountInventoryQueryRequest) { request.InstanceId = uuid.Nil },
		"provider": func(request *AccountInventoryQueryRequest) {
			value := ProviderName("OpenAI")
			request.Provider = &value
		},
		"lifecycle": func(request *AccountInventoryQueryRequest) {
			value := AccountInventoryLifecycle("deleted")
			request.Lifecycle = &value
		},
		"basic status": func(request *AccountInventoryQueryRequest) {
			value := AccountInventoryBasicStatus("active")
			request.BasicStatus = &value
		},
		"email not normalized": func(request *AccountInventoryQueryRequest) {
			value := NormalizedAccountEmail(" Operator@Example.com ")
			request.Email = &value
		},
		"email control": func(request *AccountInventoryQueryRequest) {
			value := NormalizedAccountEmail("operator@example.com\n")
			request.Email = &value
		},
		"cursor too long": func(request *AccountInventoryQueryRequest) {
			value := strings.Repeat("a", maxAccountInventoryCursorBytes+1)
			request.Cursor = &value
		},
		"zero limit": func(request *AccountInventoryQueryRequest) {
			value := 0
			request.Limit = &value
		},
		"large limit": func(request *AccountInventoryQueryRequest) {
			value := maxAccountInventoryPageLimit + 1
			request.Limit = &value
		},
	} {
		t.Run(name, func(t *testing.T) {
			request := valid
			mutate(&request)
			if validAccountInventoryRequest(request) {
				t.Fatal("invalid account inventory request was accepted")
			}
		})
	}
}

func TestAccountInventoryGeneratedResponseFieldAllowlist(t *testing.T) {
	typeOf := reflect.TypeOf(AccountInventoryItem{})
	fields := make([]string, 0, typeOf.NumField())
	for index := 0; index < typeOf.NumField(); index++ {
		name := strings.Split(typeOf.Field(index).Tag.Get("json"), ",")[0]
		fields = append(fields, name)
	}
	sort.Strings(fields)
	want := []string{
		"basic_status", "consecutive_missing_count", "email", "first_seen_at", "instance_id",
		"last_refresh_at", "last_seen_at", "lifecycle", "missing_since", "next_retry_at",
		"out_of_scope_since", "provider", "provider_degraded", "provider_last_complete_at",
		"snapshot_freshness", "source_updated_at",
	}
	sort.Strings(want)
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("generated account inventory fields = %v, want %v", fields, want)
	}
}

func TestAccountInventoryBodyRejectsUnknownFieldsAndOversize(t *testing.T) {
	for name, body := range map[string]string{
		"unknown field":                `{"instance_id":"4b58290d-3b20-4f45-a5c5-14a5aebfd3a6","email":"operator@example.com","unexpected":true}`,
		"oversize":                     `{"instance_id":"4b58290d-3b20-4f45-a5c5-14a5aebfd3a6","email":"` + strings.Repeat("a", maxAccountInventoryRequestBytes) + `"}`,
		"oversize trailing whitespace": `{"instance_id":"4b58290d-3b20-4f45-a5c5-14a5aebfd3a6"}` + strings.Repeat(" ", maxAccountInventoryRequestBytes),
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest("POST", "/api/account-inventory/query", strings.NewReader(body))
			response := httptest.NewRecorder()
			var decoded AccountInventoryQueryRequest
			if decodeJSONWithLimit(response, request, &decoded, maxAccountInventoryRequestBytes) {
				t.Fatal("invalid account inventory body was decoded")
			}
			if response.Code != 400 || strings.Contains(response.Body.String(), "operator@example.com") {
				t.Fatalf("response status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}
