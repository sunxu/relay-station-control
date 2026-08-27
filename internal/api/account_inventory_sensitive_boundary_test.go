package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	authn "github.com/sunxu/relay-station-control/internal/auth"
	accountstore "github.com/sunxu/relay-station-control/internal/store"
	generatedstore "github.com/sunxu/relay-station-control/internal/store/sqlc"
)

func TestAccountInventorySensitiveCanaryFinalArtifactMatrix(t *testing.T) {
	actorID := uuid.MustParse("10000000-0000-4000-8000-000000000701")
	instanceID := uuid.MustParse("20000000-0000-4000-8000-000000000702")
	emailCanary := strings.Join([]string{"identity", "canary-7f21", "example.invalid"}, "@")
	accountKeyCanary := "openai:" + emailCanary
	key := base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{0x71}, 32))
	keyring, err := authn.ParseKeyring([]byte(fmt.Sprintf(
		`{"format_version":1,"environment":"dev","current":1,"keys":[{"version":1,"key":%q}]}`,
		key,
	)), authn.EnvironmentDev)
	if err != nil {
		t.Fatalf("parse test keyring: %v", err)
	}
	codec, err := accountstore.NewAccountInventoryCursorCodec(keyring)
	if err != nil {
		t.Fatalf("new cursor codec: %v", err)
	}
	cursorCanary, err := codec.Encode(actorID, instanceID,
		accountstore.AccountInventoryCursorFilters{Provider: "openai", Email: emailCanary}, accountKeyCanary)
	if err != nil {
		t.Fatalf("encode cursor canary: %v", err)
	}
	canaries := []string{
		emailCanary,
		accountKeyCanary,
		cursorCanary,
		"https://endpoint-canary-7f21.invalid/private",
		"secret-reference-canary-7f21",
		"secret-value-canary-7f21",
		"poll-id-canary-7f21",
		"policy-id-canary-7f21",
		"version-canary-7f21",
		"commit-canary-7f21",
		"raw-error-canary-7f21",
	}

	// The authorized success response is the one intentional external email
	// location. Project it to a bounded classification before retaining an
	// artifact; account_key and all operational canaries remain forbidden even
	// in this response.
	now := time.Date(2026, 8, 27, 8, 0, 0, 0, time.UTC)
	item := accountstore.AccountInventoryItem{
		InstanceID: instanceID, Provider: "openai", Email: emailCanary,
		BasicStatus: accountstore.AccountInventoryBasicStatusReportedActive,
		Lifecycle:   accountstore.AccountInventoryPresent, FirstSeenAt: now, LastSeenAt: now,
		ProviderLastCompleteAt: now,
		SnapshotFreshness:      accountstore.AccountInventorySnapshotFreshnessFresh,
	}
	success := httptest.NewRecorder()
	writeJSON(success, http.StatusOK, AccountInventoryQueryResponse{
		Items: []AccountInventoryItem{accountInventoryResponseItem(item)}, NextCursor: &cursorCanary,
	})
	if success.Code != http.StatusOK || !strings.Contains(success.Body.String(), emailCanary) ||
		!strings.Contains(success.Body.String(), cursorCanary) || strings.Contains(success.Body.String(), accountKeyCanary) {
		t.Fatal("authorized response did not preserve the narrow identity allowlist")
	}
	for _, forbidden := range canaries[3:] {
		if strings.Contains(success.Body.String(), forbidden) {
			t.Fatal("authorized response exposed an operational canary")
		}
	}
	empty := httptest.NewRecorder()
	writeJSON(empty, http.StatusOK, AccountInventoryQueryResponse{Items: []AccountInventoryItem{}})
	if empty.Code != http.StatusOK || !strings.Contains(empty.Body.String(), `"items":[]`) {
		t.Fatal("empty result was not projected to a bounded response")
	}

	server := NewServer("test")
	requestBody := `{"instance_id":"` + instanceID.String() + `","email":"` + emailCanary +
		`","cursor":"` + cursorCanary + `"}`
	request := httptest.NewRequest(http.MethodPost, "/api/account-inventory/query", strings.NewReader(requestBody))
	request.Header.Set("Content-Type", "application/json")
	if request.URL.RawQuery != "" || request.URL.Path != "/api/account-inventory/query" {
		t.Fatal("sensitive query material entered the request URL")
	}
	accessLogFixture := fmt.Sprintf("method=%s path=%s status=%d", request.Method, request.URL.EscapedPath(), http.StatusOK)
	if strings.Contains(accessLogFixture, requestBody) {
		t.Fatal("access-log fixture included the POST body")
	}

	invalidEmail := NormalizedAccountEmail("INVALID-" + emailCanary)
	invalidRequest := AccountInventoryQueryRequest{InstanceId: instanceID, Email: &invalidEmail}
	if validAccountInventoryRequest(invalidRequest) {
		t.Fatal("invalid filter canary was accepted")
	}
	invalidResponse := httptest.NewRecorder()
	server.prepare(invalidResponse, request, true)
	server.writeError(invalidResponse, request, authn.ErrInvalid)

	replacement := "A"
	if strings.HasSuffix(cursorCanary, replacement) {
		replacement = "B"
	}
	tamperedCursor := cursorCanary[:len(cursorCanary)-1] + replacement
	if _, err := codec.Decode(tamperedCursor, actorID, instanceID,
		accountstore.AccountInventoryCursorFilters{Provider: "openai", Email: emailCanary}); !errors.Is(err, accountstore.ErrInvalidAccountInventoryCursor) {
		t.Fatalf("tampered cursor error = %v", err)
	}
	cursorRequest := httptest.NewRequest(http.MethodPost, "/api/account-inventory/query",
		strings.NewReader(`{"instance_id":"`+instanceID.String()+`","cursor":"`+tamperedCursor+`"}`))
	cursorResponse := httptest.NewRecorder()
	server.prepare(cursorResponse, cursorRequest, true)
	server.accountInventoryError(cursorResponse, cursorRequest, accountstore.ErrInvalidAccountInventoryCursor)

	var logBuffer bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuffer, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })
	auditResponse := httptest.NewRecorder()
	server.prepare(auditResponse, request, true)
	server.accountInventoryError(auditResponse, request, errors.New(strings.Join(canaries, "|")))
	slog.SetDefault(previousLogger)

	for name, response := range map[string]*httptest.ResponseRecorder{
		"invalid_filter": invalidResponse,
		"cursor_failure": cursorResponse,
		"audit_failure":  auditResponse,
	} {
		if response.Code != map[string]int{
			"invalid_filter": http.StatusBadRequest,
			"cursor_failure": http.StatusBadRequest,
			"audit_failure":  http.StatusServiceUnavailable,
		}[name] || response.Header().Get("Cache-Control") != "no-store" ||
			response.Header().Get("Location") != "" || response.Code >= 300 && response.Code < 400 {
			t.Fatalf("%s response escaped its fixed non-redirect contract", name)
		}
	}

	auditDetails, err := authn.SanitizeAuditDetails(authn.AuditAccountInventoryView, map[string]any{
		"instance_id": instanceID.String(), "provider_filter_used": true,
		"lifecycle_filter_used": true, "basic_status_filter_used": true,
		"email_filter_used": true, "cursor_used": true, "result_count": 1,
	})
	if err != nil {
		t.Fatalf("sanitize view audit: %v", err)
	}
	auditArtifact, err := json.Marshal(auditDetails)
	if err != nil {
		t.Fatalf("marshal audit artifact: %v", err)
	}
	for _, forbidden := range map[string]any{
		"email": emailCanary, "account_key": accountKeyCanary, "cursor": cursorCanary,
		"filter_hash": canaries[7], "result_identity": emailCanary,
	} {
		_, rejected := authn.SanitizeAuditDetails(authn.AuditAccountInventoryView, map[string]any{"unknown": forbidden})
		if rejected == nil {
			t.Fatal("sensitive audit detail was accepted")
		}
	}

	metrics := NewAccountInventoryMetrics()
	for _, observation := range []struct {
		result accountInventoryMetricResult
		code   accountInventoryMetricError
		count  int
	}{
		{accountInventoryMetricResultSuccess, accountInventoryMetricErrorNone, 1},
		{accountInventoryMetricResultSuccess, accountInventoryMetricErrorNone, 0},
		{accountInventoryMetricResultFailure, accountInventoryMetricErrorInvalid, 0},
		{accountInventoryMetricResultFailure, accountInventoryMetricErrorUnavailable, 0},
	} {
		if err := metrics.record(observation.result, observation.code, observation.count, time.Millisecond); err != nil {
			t.Fatalf("record bounded metric: %v", err)
		}
	}
	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(metrics)
	metricFamilies, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	metricsArtifact, err := json.Marshal(metricFamilies)
	if err != nil {
		t.Fatalf("marshal metrics artifact: %v", err)
	}

	formatArtifact := strings.Join([]string{
		fmt.Sprintf("%+v", accountstore.AccountInventoryQuery{
			InstanceID: instanceID, Filters: accountstore.AccountInventoryQueryFilters{
				Provider: "openai", Email: emailCanary,
			}, AfterAccountKey: accountKeyCanary, Limit: 1,
		}),
		fmt.Sprintf("%+v", accountstore.AccountInventoryCursorFilters{Provider: "openai", Email: emailCanary}),
		fmt.Sprintf("%+v", item),
		fmt.Sprintf("%+v", generatedstore.QueryCurrentAccountInventoryV1Params{
			Provider: "openai", NormalizedEmail: emailCanary, AfterAccountKey: accountKeyCanary, PageLimit: 2,
		}),
	}, "\n")

	retainedArtifact := bytes.Join([][]byte{
		[]byte("scenario=success status=200 result_bucket=1_25"),
		[]byte("scenario=empty status=200 result_bucket=zero"),
		[]byte(accessLogFixture),
		invalidResponse.Body.Bytes(),
		cursorResponse.Body.Bytes(),
		auditResponse.Body.Bytes(),
		logBuffer.Bytes(),
		auditArtifact,
		metricsArtifact,
		[]byte(formatArtifact),
	}, []byte{'\n'})
	artifactPath := filepath.Join(t.TempDir(), "account-inventory-bounded-acceptance.log")
	if err := os.WriteFile(artifactPath, retainedArtifact, 0o600); err != nil {
		t.Fatalf("write bounded artifact: %v", err)
	}
	finalArtifact, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatalf("read bounded artifact: %v", err)
	}
	for _, canary := range canaries {
		if bytes.Contains(finalArtifact, []byte(canary)) {
			t.Fatal("bounded final artifact contains a sensitive canary")
		}
	}
}
