package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	authn "github.com/sunxu/relay-station-control/internal/auth"
	accountstore "github.com/sunxu/relay-station-control/internal/store"
)

type fakeAccountInventoryReader struct {
	mu               sync.Mutex
	page             accountstore.AccountInventoryPage
	err              error
	calls            []accountInventoryReaderCall
	forbiddenNetwork forbiddenAccountInventoryNetworkCounters
}

type forbiddenAccountInventoryNetworkCounters struct {
	node       atomic.Int64
	gateway    atomic.Int64
	prometheus atomic.Int64
	model      atomic.Int64
	url        atomic.Int64
}

// These extra adapter-shaped methods deliberately remain outside
// AccountInventoryReader. If the HTTP path ever type-asserts to or invokes an
// external dependency, the final zero-network assertion catches it.
func (reader *fakeAccountInventoryReader) CallNode(context.Context) {
	reader.forbiddenNetwork.node.Add(1)
}
func (reader *fakeAccountInventoryReader) CallGateway(context.Context) {
	reader.forbiddenNetwork.gateway.Add(1)
}
func (reader *fakeAccountInventoryReader) CallPrometheus(context.Context) {
	reader.forbiddenNetwork.prometheus.Add(1)
}
func (reader *fakeAccountInventoryReader) CallModelDataPlane(context.Context) {
	reader.forbiddenNetwork.model.Add(1)
}
func (reader *fakeAccountInventoryReader) OpenURL(context.Context, string) {
	reader.forbiddenNetwork.url.Add(1)
}

func (reader *fakeAccountInventoryReader) assertNoForbiddenNetworkCalls(t *testing.T) {
	t.Helper()
	counts := map[string]int64{
		"node": reader.forbiddenNetwork.node.Load(), "gateway": reader.forbiddenNetwork.gateway.Load(),
		"prometheus": reader.forbiddenNetwork.prometheus.Load(), "model": reader.forbiddenNetwork.model.Load(),
		"url": reader.forbiddenNetwork.url.Load(),
	}
	for name, count := range counts {
		if count != 0 {
			t.Fatalf("forbidden %s network calls = %d, want 0", name, count)
		}
	}
}

type accountInventoryReaderCall struct {
	query accountstore.AccountInventoryQuery
	audit accountstore.AccountInventoryViewAudit
}

func (reader *fakeAccountInventoryReader) QueryPageAndAudit(
	_ context.Context, query accountstore.AccountInventoryQuery, audit accountstore.AccountInventoryViewAudit,
) (accountstore.AccountInventoryPage, error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	reader.calls = append(reader.calls, accountInventoryReaderCall{query: query, audit: audit})
	return reader.page, reader.err
}

func (reader *fakeAccountInventoryReader) CheckCompatibility(context.Context) error { return nil }

func (reader *fakeAccountInventoryReader) setResult(page accountstore.AccountInventoryPage, err error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	reader.page, reader.err = page, err
}

func (reader *fakeAccountInventoryReader) snapshotCalls() []accountInventoryReaderCall {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	return append([]accountInventoryReaderCall(nil), reader.calls...)
}

func assertJSONObjectKeys(t *testing.T, value map[string]any, want ...string) {
	t.Helper()
	got := make([]string, 0, len(value))
	for key := range value {
		got = append(got, key)
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("JSON keys = %v, want %v", got, want)
	}
}

func TestAccountInventoryHTTPAuthorizationPaginationAndErrorMapping(t *testing.T) {
	ownerURL, runtimeURL := isolatedRuntimeDatabaseURLs(t)
	ctx := context.Background()
	owner, err := pgxpool.New(ctx, ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	runtime, err := pgxpool.New(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if _, err = owner.Exec(ctx, `INSERT INTO environments(environment_id,name,environment_type) VALUES('account-http-test','Account HTTP Test','dev')`); err != nil {
		t.Fatal(err)
	}

	config := testValidatedConfig(t, authn.EnvironmentDev, false)
	service, err := authn.NewService(runtime, config)
	if err != nil {
		t.Fatal(err)
	}
	adminID, disabledAdminID, instanceID := uuid.New(), uuid.New(), uuid.New()
	token, csrf := "account-session-"+uuid.NewString(), "account-csrf-"+uuid.NewString()
	disabledToken, disabledCSRF := "disabled-account-session-"+uuid.NewString(), "disabled-account-csrf-"+uuid.NewString()
	tokenDigest, err := authn.ComputeDigest(config.Keyring, authn.DomainSessionDigest, token)
	if err != nil {
		t.Fatal(err)
	}
	csrfDigest, err := authn.ComputeDigest(config.Keyring, authn.DomainCSRFDigest, csrf)
	if err != nil {
		t.Fatal(err)
	}
	disabledTokenDigest, err := authn.ComputeDigest(config.Keyring, authn.DomainSessionDigest, disabledToken)
	if err != nil {
		t.Fatal(err)
	}
	disabledCSRFDigest, err := authn.ComputeDigest(config.Keyring, authn.DomainCSRFDigest, disabledCSRF)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at)
		VALUES($1,$2,'Account Reader','enabled',CURRENT_TIMESTAMP)`, adminID, "account_reader_"+adminID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO control_admin_sessions
		(session_id,admin_id,token_digest,csrf_digest,key_version,mfa_method,absolute_expires_at)
		VALUES($1,$2,$3,$4,$5,'none',CURRENT_TIMESTAMP+interval '12 hours')`,
		uuid.New(), adminID, tokenDigest.Sum[:], csrfDigest.Sum[:], int32(tokenDigest.KeyVersion)); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO control_admin_users
		(admin_id,login_name,display_name,status,activated_at,disabled_at)
		VALUES($1,$2,'Disabled Account Reader','disabled',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
		disabledAdminID, "disabled_account_reader_"+disabledAdminID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO control_admin_sessions
		(session_id,admin_id,token_digest,csrf_digest,key_version,mfa_method,absolute_expires_at)
		VALUES($1,$2,$3,$4,$5,'none',CURRENT_TIMESTAMP+interval '12 hours')`,
		uuid.New(), disabledAdminID, disabledTokenDigest.Sum[:], disabledCSRFDigest.Sum[:], int32(disabledTokenDigest.KeyVersion)); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 8, 27, 8, 0, 0, 0, time.UTC)
	identityCanary := "inventory-canary@example.invalid"
	item := accountstore.AccountInventoryItem{
		InstanceID: instanceID, Provider: "openai", Email: identityCanary,
		BasicStatus: accountstore.AccountInventoryBasicStatusReportedActive,
		Lifecycle:   accountstore.AccountInventoryPresent, ConsecutiveMissingCount: 0,
		FirstSeenAt: now.Add(-time.Hour), LastSeenAt: now,
		ProviderLastCompleteAt: now, ProviderDegraded: false,
		SnapshotFreshness: accountstore.AccountInventorySnapshotFreshnessFresh,
	}
	reader := &fakeAccountInventoryReader{page: accountstore.AccountInventoryPage{
		Items: []accountstore.AccountInventoryItem{item}, HasMore: true,
		ContinuationAccountKey: "openai:" + identityCanary,
	}}
	server := NewAuthenticatedServer("test", service)
	server.accountInventory = reader
	server.accountInventoryCursor, err = accountstore.NewAccountInventoryCursorCodec(config.Keyring)
	if err != nil {
		t.Fatal(err)
	}
	handler := HandlerWithOptions(server, ChiServerOptions{ErrorHandlerFunc: server.PrepareGeneratedError})
	do := func(body, session, proof string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/api/account-inventory/query", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		if proof != "" {
			request.Header.Set("X-CSRF-Token", proof)
		}
		if session != "" {
			request.AddCookie(&http.Cookie{Name: authn.DevSessionCookieName, Value: session})
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	requestBody := `{"instance_id":"` + instanceID.String() + `","provider":"openai","lifecycle":"present","basic_status":"reported_active","email":"` + identityCanary + `","limit":1}`
	for name, response := range map[string]*httptest.ResponseRecorder{
		"missing csrf":    do(requestBody, token, ""),
		"missing session": do(requestBody, "", csrf),
		"disabled admin":  do(requestBody, disabledToken, disabledCSRF),
		"unknown field":   do(strings.TrimSuffix(requestBody, "}")+`,"unknown":true}`, token, csrf),
	} {
		if response.Code != map[string]int{"missing csrf": 403, "missing session": 401, "disabled admin": 401, "unknown field": 400}[name] ||
			response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Request-ID") == "" ||
			strings.Contains(response.Body.String(), identityCanary) {
			t.Fatalf("%s status=%d headers=%v body=%s", name, response.Code, response.Header(), response.Body.String())
		}
	}
	if calls := reader.snapshotCalls(); len(calls) != 0 {
		t.Fatalf("authentication/input rejections reached account Store: %d", len(calls))
	}

	first := do(requestBody, token, csrf)
	if first.Code != http.StatusOK || first.Header().Get("Cache-Control") != "no-store" ||
		!strings.Contains(first.Body.String(), identityCanary) || strings.Contains(first.Body.String(), "account_key") ||
		!strings.Contains(first.Body.String(), `"missing_since":null`) ||
		!strings.Contains(first.Body.String(), `"last_refresh_at":null`) ||
		!strings.Contains(first.Body.String(), `"source_updated_at":null`) {
		t.Fatalf("first page status=%d headers=%v body=%s", first.Code, first.Header(), first.Body.String())
	}
	var firstPage AccountInventoryQueryResponse
	if err = json.Unmarshal(first.Body.Bytes(), &firstPage); err != nil || len(firstPage.Items) != 1 || firstPage.NextCursor == nil ||
		strings.Contains(*firstPage.NextCursor, identityCanary) {
		t.Fatalf("first page=%+v err=%v", firstPage, err)
	}
	var responseObject map[string]any
	if err = json.Unmarshal(first.Body.Bytes(), &responseObject); err != nil {
		t.Fatal(err)
	}
	assertJSONObjectKeys(t, responseObject, "items", "next_cursor")
	encodedItems, ok := responseObject["items"].([]any)
	if !ok || len(encodedItems) != 1 {
		t.Fatalf("response items shape = %#v", responseObject["items"])
	}
	encodedItem, ok := encodedItems[0].(map[string]any)
	if !ok {
		t.Fatalf("response item shape = %#v", encodedItems[0])
	}
	assertJSONObjectKeys(t, encodedItem,
		"instance_id", "provider", "email", "basic_status", "lifecycle", "consecutive_missing_count",
		"first_seen_at", "last_seen_at", "missing_since", "out_of_scope_since", "last_refresh_at",
		"next_retry_at", "source_updated_at", "provider_last_complete_at", "provider_degraded", "snapshot_freshness",
	)
	calls := reader.snapshotCalls()
	if len(calls) != 1 || calls[0].query.AfterAccountKey != "" || calls[0].query.Limit != 1 ||
		calls[0].query.Filters.Provider != "openai" || calls[0].query.Filters.Lifecycle != accountstore.AccountInventoryPresent ||
		calls[0].query.Filters.BasicStatus != accountstore.AccountInventoryBasicStatusReportedActive ||
		calls[0].query.Filters.Email != identityCanary ||
		calls[0].audit.ActorAdminID != adminID || len(calls[0].audit.SourceFingerprint) != 32 || calls[0].audit.RequestID == "" {
		t.Fatalf("first query calls=%+v", calls)
	}

	reader.setResult(accountstore.AccountInventoryPage{Items: []accountstore.AccountInventoryItem{}}, nil)
	nextBody := strings.TrimSuffix(requestBody, "}") + `,"cursor":"` + *firstPage.NextCursor + `"}`
	next := do(nextBody, token, csrf)
	if next.Code != http.StatusOK || !strings.Contains(next.Body.String(), `"items":[]`) {
		t.Fatalf("next page status=%d body=%s", next.Code, next.Body.String())
	}
	calls = reader.snapshotCalls()
	if len(calls) != 2 || calls[1].query.AfterAccountKey != "openai:"+identityCanary {
		t.Fatalf("next query calls=%+v", calls)
	}

	beforeInvalidCursor := len(calls)
	invalidCursor := do(strings.TrimSuffix(requestBody, "}")+`,"cursor":"tampered"}`, token, csrf)
	if invalidCursor.Code != http.StatusBadRequest || len(reader.snapshotCalls()) != beforeInvalidCursor ||
		strings.Contains(invalidCursor.Body.String(), "tampered") {
		t.Fatalf("invalid cursor status=%d body=%s", invalidCursor.Code, invalidCursor.Body.String())
	}

	for name, test := range map[string]struct {
		err        error
		wantStatus int
		wantCode   ErrorCode
	}{
		"not found":     {accountstore.ErrAccountInventoryInstanceNotFound, 404, ErrorCodeNotFound},
		"unsupported":   {accountstore.ErrAccountInventoryCapabilityUnsupported, 409, ErrorCodeConflict},
		"inconsistent":  {accountstore.ErrAccountInventoryInconsistent, 503, ErrorCodeTemporarilyUnavailable},
		"audit failure": {errors.New("database audit commit canary"), 503, ErrorCodeTemporarilyUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			reader.setResult(accountstore.AccountInventoryPage{}, test.err)
			response := do(requestBody, token, csrf)
			if response.Code != test.wantStatus || response.Header().Get("Cache-Control") != "no-store" ||
				!strings.Contains(response.Body.String(), `"code":"`+string(test.wantCode)+`"`) ||
				strings.Contains(response.Body.String(), "canary") || strings.Contains(response.Body.String(), identityCanary) {
				t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
			}
		})
	}

	reader.setResult(accountstore.AccountInventoryPage{Items: []accountstore.AccountInventoryItem{}}, nil)
	const concurrentRequests = 12
	beforeConcurrent := len(reader.snapshotCalls())
	responses := make(chan *httptest.ResponseRecorder, concurrentRequests)
	var wait sync.WaitGroup
	for index := 0; index < concurrentRequests; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			responses <- do(requestBody, token, csrf)
		}()
	}
	wait.Wait()
	close(responses)
	for response := range responses {
		if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" ||
			!strings.Contains(response.Body.String(), `"items":[]`) {
			t.Fatalf("concurrent query status=%d body=%s", response.Code, response.Body.String())
		}
	}
	if got := len(reader.snapshotCalls()) - beforeConcurrent; got != concurrentRequests {
		t.Fatalf("concurrent Store calls=%d, want %d", got, concurrentRequests)
	}
	reader.assertNoForbiddenNetworkCalls(t)
}
