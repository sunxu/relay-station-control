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
	"github.com/jackc/pgx/v5"
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

func TestAccountInventoryHTTPRepositoryRetentionNullSourceAndInconsistentCurrentState(t *testing.T) {
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

	instanceID, policyID, pollID := uuid.New(), uuid.New(), uuid.New()
	if _, err = owner.Exec(ctx, `INSERT INTO node_drivers(node_type,driver_contract_version,display_name,lifecycle_status)
		VALUES('cliproxy.api','v1','CLI Proxy API','active');
		INSERT INTO driver_capabilities(node_type,driver_contract_version,capability)
		VALUES('cliproxy.api','v1','management_account_inventory_read');
		INSERT INTO relay_node_assets(
		instance_id,display_name,node_type,driver_contract_version,management_endpoint,reader_secret_ref
	) VALUES($1,'Retention Node','cliproxy.api','v1','http://retention-node.example',
		'docker-secret://synthetic/retention-reader');
		INSERT INTO node_capabilities(instance_id,node_type,driver_contract_version,capability)
		VALUES($1,'cliproxy.api','v1','management_account_inventory_read');
		INSERT INTO provider_inventory_policy_versions(
		policy_version_id,node_type,driver_contract_version,active_providers,out_of_scope_providers,created_by
	) VALUES($2,'cliproxy.api','v1',ARRAY['openai'],ARRAY[]::text[],'account-retention-http');
		WITH boundary AS (SELECT date_bin(interval '5 minutes',clock_timestamp()-interval '31 days',
			timestamptz '1970-01-01') AS at)
		INSERT INTO account_inventory_poll_runs(
			poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,
			provider_policy_version,status,attempt_count,created_at,first_started_at,last_started_at,
			finalized_at,observed_at,transport_success,response_shape_valid,contract_valid,inventory_mode,
			node_identity_complete,snapshot_complete,degraded,result,reason,source_record_count,
			identifiable_record_count,unidentified_record_count,unsupported_provider_count,
			out_of_scope_provider_count,node_version,node_commit
		) SELECT $3,$1,'cliproxy.api','v1',at,$2,'finalized',1,at,at,at,at,at,true,true,true,
			'runtime',true,true,false,'success','none',0,0,0,0,0,'unknown','unknown' FROM boundary;
		ALTER TABLE account_inventory_poll_provider_results DISABLE TRIGGER account_inventory_poll_provider_results_guard;
		INSERT INTO account_inventory_poll_provider_results(
			poll_run_id,provider,identifiable_count,missing_identity_count,duplicate_identity_count,
			identity_complete,snapshot_complete,degraded,reason,promotion_applied
		) VALUES($3,'openai',0,0,0,true,true,false,'complete',true);
		ALTER TABLE account_inventory_poll_provider_results ENABLE TRIGGER account_inventory_poll_provider_results_guard;
		ALTER TABLE account_inventory_provider_states DISABLE TRIGGER account_inventory_provider_states_guard;
		INSERT INTO account_inventory_provider_states(
			instance_id,provider,current_poll_run_id,current_scheduled_at,last_complete_at,
			source_observed_at,source_node_version,source_node_commit,state,updated_at,
			monitoring_status,health_scheduled_at,health_degraded,health_reason
		) SELECT instance_id,'openai',poll_run_id,scheduled_at,observed_at,observed_at,
			node_version,node_commit,'current',observed_at,'active',scheduled_at,false,'none'
		FROM account_inventory_poll_runs WHERE poll_run_id=$3;
		ALTER TABLE account_inventory_provider_states ENABLE TRIGGER account_inventory_provider_states_guard;
		SELECT set_config('relay_control.lifecycle_write','finalize',true);
		INSERT INTO account_inventory(
			instance_id,provider,account_key,normalized_email,basic_status,
			success_count,failed_count,recent_request_count,lifecycle,
			consecutive_missing_count,first_seen_at,last_seen_at,current_poll_run_id,
			current_scheduled_at,source_observed_at,source_node_version,source_node_commit,updated_at
		)
		SELECT instance_id,'openai','openai:retention@example.invalid',
			'retention@example.invalid','active',1,0,0,'present',0,
			source_observed_at,source_observed_at,current_poll_run_id,current_scheduled_at,
			source_observed_at,source_node_version,source_node_commit,source_observed_at
		FROM account_inventory_provider_states WHERE instance_id=$1 AND provider='openai'`,
		pgx.QueryExecModeSimpleProtocol, instanceID, policyID, pollID); err != nil {
		t.Fatal(err)
	}

	config := testValidatedConfig(t, authn.EnvironmentDev, false)
	service, err := authn.NewService(runtime, config)
	if err != nil {
		t.Fatal(err)
	}
	adminID := uuid.New()
	token, csrf := "retention-session-"+uuid.NewString(), "retention-csrf-"+uuid.NewString()
	tokenDigest, err := authn.ComputeDigest(config.Keyring, authn.DomainSessionDigest, token)
	if err != nil {
		t.Fatal(err)
	}
	csrfDigest, err := authn.ComputeDigest(config.Keyring, authn.DomainCSRFDigest, csrf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO control_admin_users(
		admin_id,login_name,display_name,status,activated_at
	) VALUES($1,$2,'Retention Reader','enabled',CURRENT_TIMESTAMP);
		INSERT INTO control_admin_sessions(
		session_id,admin_id,token_digest,csrf_digest,key_version,mfa_method,absolute_expires_at
		) VALUES($3,$1,$4,$5,$6,'none',CURRENT_TIMESTAMP+interval '12 hours')`,
		pgx.QueryExecModeSimpleProtocol, adminID, "retention_reader_"+adminID.String()[:8], uuid.New(), tokenDigest.Sum[:],
		csrfDigest.Sum[:], int32(tokenDigest.KeyVersion)); err != nil {
		t.Fatal(err)
	}
	repository, err := accountstore.NewAccountInventoryRepository(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if err = repository.CheckCompatibility(ctx); err != nil {
		t.Fatal(err)
	}
	server := NewAuthenticatedServer("test", service)
	server.accountInventory = repository
	server.accountInventoryCursor, err = accountstore.NewAccountInventoryCursorCodec(config.Keyring)
	if err != nil {
		t.Fatal(err)
	}
	handler := HandlerWithOptions(server, ChiServerOptions{ErrorHandlerFunc: server.PrepareGeneratedError})
	requestBody := `{"instance_id":"` + instanceID.String() + `","limit":10}`
	do := func() *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/api/account-inventory/query", strings.NewReader(requestBody))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-CSRF-Token", csrf)
		request.AddCookie(&http.Cookie{Name: authn.DevSessionCookieName, Value: token})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	before := do()
	if before.Code != http.StatusOK || !strings.Contains(before.Body.String(), "retention@example.invalid") {
		t.Fatalf("before retention status=%d body=%s", before.Code, before.Body.String())
	}
	if _, err = owner.Exec(ctx, `UPDATE account_inventory_provider_states SET current_poll_run_id=NULL
		WHERE instance_id=$1 AND provider='openai';
		UPDATE account_inventory SET current_poll_run_id=NULL WHERE instance_id=$1`,
		pgx.QueryExecModeSimpleProtocol, instanceID); err != nil {
		t.Fatal(err)
	}
	after := do()
	if after.Code != before.Code || after.Body.String() != before.Body.String() {
		t.Fatalf("HTTP changed across retention\nbefore=%d %s\nafter=%d %s",
			before.Code, before.Body.String(), after.Code, after.Body.String())
	}
	var providerPointer, accountPointer *uuid.UUID
	if err = owner.QueryRow(ctx, `SELECT
		(SELECT current_poll_run_id FROM account_inventory_provider_states WHERE instance_id=$1 AND provider='openai'),
		(SELECT current_poll_run_id FROM account_inventory WHERE instance_id=$1 AND account_key='openai:retention@example.invalid')`,
		instanceID).Scan(&providerPointer, &accountPointer); err != nil {
		t.Fatal(err)
	}
	if providerPointer != nil || accountPointer != nil {
		t.Fatalf("retention-shaped source clear failed: provider=%v account=%v", providerPointer, accountPointer)
	}

	if _, err = owner.Exec(ctx, `ALTER TABLE account_inventory_provider_states
		ALTER COLUMN health_degraded DROP NOT NULL;
		ALTER TABLE account_inventory_provider_states DISABLE TRIGGER account_inventory_provider_states_guard;
		UPDATE account_inventory_provider_states SET health_degraded=NULL
		WHERE instance_id=$1 AND provider='openai';
		ALTER TABLE account_inventory_provider_states ENABLE TRIGGER account_inventory_provider_states_guard`,
		pgx.QueryExecModeSimpleProtocol, instanceID); err != nil {
		t.Fatal(err)
	}
	_, err = repository.QueryPageAndAudit(ctx, accountstore.AccountInventoryQuery{
		InstanceID: instanceID, Limit: 10,
	}, accountstore.AccountInventoryViewAudit{
		ActorAdminID: adminID, SourceFingerprint: make([]byte, 32), RequestID: "retention-inconsistent",
	})
	if !errors.Is(err, accountstore.ErrAccountInventoryInconsistent) {
		t.Fatalf("missing current health error=%v", err)
	}
	unavailable := do()
	if unavailable.Code != http.StatusServiceUnavailable || unavailable.Header().Get("Cache-Control") != "no-store" ||
		!strings.Contains(unavailable.Body.String(), `"code":"temporarily_unavailable"`) ||
		strings.Contains(unavailable.Body.String(), "retention@example.invalid") {
		t.Fatalf("inconsistent current state status=%d headers=%v body=%s",
			unavailable.Code, unavailable.Header(), unavailable.Body.String())
	}
}
