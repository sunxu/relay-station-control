package api

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	accountadmin "github.com/sunxu/relay-station-control/internal/accountadmin"
	authn "github.com/sunxu/relay-station-control/internal/auth"
	"github.com/sunxu/relay-station-control/internal/drivers"
	"github.com/sunxu/relay-station-control/internal/drivers/cliproxyapi"
	assetstore "github.com/sunxu/relay-station-control/internal/store"
)

type integrationAccountResolver struct {
	state accountadmin.NodeState
}

func (r integrationAccountResolver) Resolve(context.Context, uuid.UUID, string) (accountadmin.NodeState, error) {
	return r.state, nil
}

// TestAccountOperationsHTTPPostgreSQLIntegration exercises the account API
// through real authentication, the real repository and controlled SQL
// functions. The native endpoint is a local synthetic server; no credential
// fixture is logged or persisted.
func TestAccountOperationsHTTPPostgreSQLIntegration(t *testing.T) {
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

	var snapshot atomic.Value
	snapshot.Store(`{"files":[{"name":"account.json","provider":"antigravity","email":"user@example.invalid","source":"file","runtime_only":false,"auth_index":"1","disabled":false}]}`)
	var artifact atomic.Value
	artifact.Store(true)
	var mutationCount atomic.Int32
	var mutationStatus atomic.Int32
	var mutationMode atomic.Int32
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if artifact.Load().(bool) {
				w.Header().Add("X-CPA-VERSION", cliproxyapi.FrozenRuntimeVersion)
				w.Header().Add("X-CPA-COMMIT", cliproxyapi.FrozenRuntimeCommit)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, snapshot.Load().(string))
			return
		}
		mutationCount.Add(1)
		if mutationMode.Load() == 2 {
			connection, _, hijackErr := w.(http.Hijacker).Hijack()
			if hijackErr == nil {
				_ = connection.Close()
			}
			return
		}
		if mutationMode.Load() == 3 {
			<-r.Context().Done()
			return
		}
		status := int(mutationStatus.Load())
		if status == 0 {
			status = http.StatusNoContent
		}
		w.WriteHeader(status)
	}))
	defer native.Close()

	management, err := (drivers.ManagementConfig{}).Validate()
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := cliproxyapi.NewNativeAdapter(native.URL, management, "synthetic-management-key")
	if err != nil {
		t.Fatal(err)
	}
	adminID, nodeID := uuid.New(), uuid.New()
	if _, err = owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,role,status,activated_at) VALUES($1,$2,'Account API integration','super_admin','enabled',clock_timestamp())`, adminID, "account_api_"+adminID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO relay_node_assets(instance_id,display_name,node_type,driver_contract_version,management_endpoint) VALUES($1,'Account API integration node','cliproxyapi','cliproxyapi.auth-files.v1',$2)`, nodeID, native.URL); err != nil {
		t.Fatal(err)
	}

	config := testValidatedConfig(t, authn.EnvironmentDev)
	authService, err := authn.NewService(runtime, config)
	if err != nil {
		t.Fatal(err)
	}
	token, csrf := "account-api-session-"+uuid.NewString(), "account-api-csrf-"+uuid.NewString()
	tokenDigest, err := authn.ComputeDigest(config.Keyring, authn.DomainSessionDigest, token)
	if err != nil {
		t.Fatal(err)
	}
	csrfDigest, err := authn.ComputeDigest(config.Keyring, authn.DomainCSRFDigest, csrf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO control_admin_sessions(session_id,admin_id,token_digest,csrf_digest,key_version,mfa_method,absolute_expires_at) VALUES($1,$2,$3,$4,$5,'none',clock_timestamp()+interval '1 hour')`, uuid.New(), adminID, tokenDigest.Sum[:], csrfDigest.Sum[:], int32(tokenDigest.KeyVersion)); err != nil {
		t.Fatal(err)
	}

	keyFile := t.TempDir() + "/intent.key"
	if err = os.WriteFile(keyFile, []byte(strings.Repeat("k", 32)), 0600); err != nil {
		t.Fatal(err)
	}
	repo, err := assetstore.NewAccountOperationRepository(runtime)
	if err != nil {
		t.Fatal(err)
	}
	resolver := integrationAccountResolver{state: accountadmin.NodeState{Adapter: adapter, LifecycleActive: true, MonitoringEligible: true, InventoryReadAllowed: true, ProviderPolicyActive: true}}
	accountService, err := accountadmin.NewServiceWithIntentKeyPath(repo, resolver, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	server := NewAuthenticatedServer("test", authService)
	if err = server.SetAccountOperationService(accountService, repo); err != nil {
		t.Fatal(err)
	}
	handler := HandlerWithOptions(server, ChiServerOptions{ErrorHandlerFunc: server.PrepareGeneratedError})
	post := func(path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18080"+path, strings.NewReader(body))
		r.Header.Set("Origin", "http://127.0.0.1:18080")
		r.Header.Set("X-CSRF-Token", csrf)
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: authn.DevSessionCookieName, Value: token})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	postUpload := func(path string, commandID uuid.UUID) *httptest.ResponseRecorder {
		var body strings.Builder
		mw := multipart.NewWriter(&body)
		requestPart, _ := mw.CreateFormField("request")
		_, _ = io.WriteString(requestPart, `{"command_id":"`+commandID.String()+`","node_instance_id":"`+nodeID.String()+`","account_key":"antigravity:user@example.invalid"}`)
		credential, _ := mw.CreateFormFile("credential", "credential.json")
		_, _ = io.WriteString(credential, `{"type":"antigravity","email":"user@example.invalid","refresh_token":"synthetic"}`)
		_ = mw.Close()
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18080"+path, strings.NewReader(body.String()))
		r.Header.Set("Origin", "http://127.0.0.1:18080")
		r.Header.Set("X-CSRF-Token", csrf)
		r.Header.Set("Content-Type", mw.FormDataContentType())
		r.AddCookie(&http.Cookie{Name: authn.DevSessionCookieName, Value: token})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	postUploadTimeout := func(path string, commandID uuid.UUID, timeout time.Duration) *httptest.ResponseRecorder {
		var body strings.Builder
		mw := multipart.NewWriter(&body)
		requestPart, _ := mw.CreateFormField("request")
		_, _ = io.WriteString(requestPart, `{"command_id":"`+commandID.String()+`","node_instance_id":"`+nodeID.String()+`","account_key":"antigravity:user@example.invalid"}`)
		credential, _ := mw.CreateFormFile("credential", "credential.json")
		_, _ = io.WriteString(credential, `{"type":"antigravity","email":"user@example.invalid","refresh_token":"synthetic"}`)
		_ = mw.Close()
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18080"+path, strings.NewReader(body.String())).WithContext(ctx)
		r.Header.Set("Origin", "http://127.0.0.1:18080")
		r.Header.Set("X-CSRF-Token", csrf)
		r.Header.Set("Content-Type", mw.FormDataContentType())
		r.AddCookie(&http.Cookie{Name: authn.DevSessionCookieName, Value: token})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	get := func(path string, cookie bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:18080"+path, nil)
		if cookie {
			r.AddCookie(&http.Cookie{Name: authn.DevSessionCookieName, Value: token})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	override := func(path string, commandID uuid.UUID, reason, confirmation string) *httptest.ResponseRecorder {
		body := `{"command_id":"` + commandID.String() + `","reason":"` + reason + `","confirmation":"` + confirmation + `"}`
		return post(path, body)
	}
	assertOperation := func(t *testing.T, id uuid.UUID, state, code string, wantReceipt, wantAudit bool) {
		t.Helper()
		var gotState string
		var gotCode *string
		if err := owner.QueryRow(ctx, `SELECT execution_state,remote_result_code FROM account_admin_operations WHERE command_id=$1`, id).Scan(&gotState, &gotCode); err != nil {
			t.Fatal(err)
		}
		if gotState != state || (code == "" && gotCode != nil) || (code != "" && (gotCode == nil || *gotCode != code)) {
			t.Fatalf("operation state=%s code=%v", gotState, gotCode)
		}
		var receipts, audits int
		if err := owner.QueryRow(ctx, `SELECT count(*) FROM account_admin_command_receipts WHERE command_id=$1`, id).Scan(&receipts); err != nil {
			t.Fatal(err)
		}
		if err := owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE details->>'command_id'=$1`, id.String()).Scan(&audits); err != nil {
			t.Fatal(err)
		}
		if (receipts > 0) != wantReceipt || (audits > 0) != wantAudit {
			t.Fatalf("receipt/audit=%d/%d", receipts, audits)
		}
	}

	// A syntactically valid but out-of-scope provider is rejected before the
	// resolver and therefore before any durable acceptance.
	unsupportedID := uuid.New()
	unsupported := post("/api/account-operations/disable", `{"command_id":"`+unsupportedID.String()+`","node_instance_id":"`+nodeID.String()+`","account_key":"openai:user@example.invalid"}`)
	if unsupported.Code != http.StatusConflict || !strings.Contains(unsupported.Body.String(), `"unsupported_provider"`) {
		t.Fatalf("unsupported provider: %d %s", unsupported.Code, unsupported.Body.String())
	}
	var count int
	if err = owner.QueryRow(ctx, `SELECT count(*) FROM admin_command_registry WHERE command_id=$1`, unsupportedID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("unsupported provider was accepted")
	}

	// Missing active policy has the same stable code, but is post-acceptance.
	policyResolver := integrationAccountResolver{state: accountadmin.NodeState{Adapter: adapter, LifecycleActive: true, MonitoringEligible: true, InventoryReadAllowed: true}}
	policyService, _ := accountadmin.NewServiceWithIntentKeyPath(repo, policyResolver, keyFile)
	policyServer := NewAuthenticatedServer("test", authService)
	_ = policyServer.SetAccountOperationService(policyService, repo)
	policyHandler := HandlerWithOptions(policyServer, ChiServerOptions{ErrorHandlerFunc: policyServer.PrepareGeneratedError})
	requestPolicy := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18080/api/account-operations/disable", strings.NewReader(`{"command_id":"`+uuid.NewString()+`","node_instance_id":"`+nodeID.String()+`","account_key":"antigravity:user@example.invalid"}`))
	requestPolicy.Header.Set("Origin", "http://127.0.0.1:18080")
	requestPolicy.Header.Set("X-CSRF-Token", csrf)
	requestPolicy.Header.Set("Content-Type", "application/json")
	requestPolicy.AddCookie(&http.Cookie{Name: authn.DevSessionCookieName, Value: token})
	policyID := uuid.New()
	requestPolicy.Body = io.NopCloser(strings.NewReader(`{"command_id":"` + policyID.String() + `","node_instance_id":"` + nodeID.String() + `","account_key":"antigravity:user@example.invalid"}`))
	policyResponse := httptest.NewRecorder()
	policyHandler.ServeHTTP(policyResponse, requestPolicy)
	if policyResponse.Code != http.StatusConflict || !strings.Contains(policyResponse.Body.String(), `"unsupported_provider"`) {
		t.Fatalf("missing policy: %d %s", policyResponse.Code, policyResponse.Body.String())
	}
	assertOperation(t, policyID, "failed", "unsupported_provider", true, true)

	// The five command kinds use the same real service/repository path.
	for _, tc := range []struct {
		name, kind, path, body, snapshot string
		disabled                         bool
	}{
		{"disable", "disable", "/api/account-operations/disable", `{"command_id":"%s","node_instance_id":"` + nodeID.String() + `","account_key":"antigravity:user@example.invalid"}`, `{"files":[{"name":"account.json","provider":"antigravity","email":"user@example.invalid","source":"file","runtime_only":false,"auth_index":"1","disabled":false}]}`, false},
		{"enable", "enable", "/api/account-operations/enable", `{"command_id":"%s","node_instance_id":"` + nodeID.String() + `","account_key":"antigravity:user@example.invalid"}`, `{"files":[{"name":"account.json","provider":"antigravity","email":"user@example.invalid","source":"file","runtime_only":false,"auth_index":"1","disabled":true}]}`, true},
		{"remove", "remove", "/api/account-operations/remove", `{"command_id":"%s","node_instance_id":"` + nodeID.String() + `","account_key":"antigravity:user@example.invalid","confirmation":"REMOVE"}`, `{"files":[{"name":"account.json","provider":"antigravity","email":"user@example.invalid","source":"file","runtime_only":false,"auth_index":"1","disabled":false}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := uuid.New()
			snapshot.Store(tc.snapshot)
			mutationStatus.Store(0)
			before := mutationCount.Load()
			response := post(tc.path, fmt.Sprintf(tc.body, id))
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			assertOperation(t, id, "remote_applied", "", true, true)
			if mutationCount.Load()-before != 1 {
				t.Fatalf("mutations=%d", mutationCount.Load()-before)
			}
		})
	}

	for _, replace := range []bool{false, true} {
		id := uuid.New()
		snapshot.Store(`{"files":[]}`)
		mutationStatus.Store(0)
		path := "/api/account-operations/upload-new"
		if replace {
			path = "/api/account-operations/replace-existing"
			snapshot.Store(`{"files":[{"name":"account.json","provider":"antigravity","email":"user@example.invalid","source":"file","runtime_only":false,"auth_index":"1","disabled":false}]}`)
		}
		before := mutationCount.Load()
		response := postUpload(path, id)
		if response.Code != http.StatusOK {
			t.Fatalf("upload status=%d body=%s", response.Code, response.Body.String())
		}
		assertOperation(t, id, "remote_applied", "", true, true)
		if mutationCount.Load()-before != 1 {
			t.Fatalf("upload mutations=%d", mutationCount.Load()-before)
		}
	}

	// Pre-dispatch failures are durable and never issue a mutation.
	for _, tc := range []struct{ name, code, files string }{
		{"not-found", "account_target_not_found", `{"files":[]}`},
		{"ambiguous", "account_target_ambiguous", `{"files":[{"name":"a.json","provider":"antigravity","email":"user@example.invalid","source":"file","runtime_only":false,"auth_index":"1","disabled":false},{"name":"b.json","provider":"antigravity","email":"user@example.invalid","source":"file","runtime_only":false,"auth_index":"2","disabled":false}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := uuid.New()
			snapshot.Store(tc.files)
			before := mutationCount.Load()
			response := post("/api/account-operations/remove", `{"command_id":"`+id.String()+`","node_instance_id":"`+nodeID.String()+`","account_key":"antigravity:user@example.invalid","confirmation":"REMOVE"}`)
			if response.Code != http.StatusNotFound && response.Code != http.StatusConflict {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			assertOperation(t, id, "failed", tc.code, true, true)
			if mutationCount.Load() != before {
				t.Fatal("pre-dispatch failure mutated native state")
			}
		})
	}

	// Invalid artifact is also pre-dispatch.
	artifact.Store(false)
	snapshot.Store(`{"files":[{"name":"account.json","provider":"antigravity","email":"user@example.invalid","source":"file","runtime_only":false,"auth_index":"1","disabled":false}]}`)
	id := uuid.New()
	before := mutationCount.Load()
	response := post("/api/account-operations/remove", `{"command_id":"`+id.String()+`","node_instance_id":"`+nodeID.String()+`","account_key":"antigravity:user@example.invalid","confirmation":"REMOVE"}`)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("artifact status=%d body=%s", response.Code, response.Body.String())
	}
	assertOperation(t, id, "failed", "unsupported_node_version", true, true)
	if mutationCount.Load() != before {
		t.Fatal("artifact failure mutated native state")
	}
	artifact.Store(true)

	// Reviewed upload 503 is deterministic and terminalized by the service.
	id = uuid.New()
	snapshot.Store(`{"files":[]}`)
	mutationStatus.Store(http.StatusServiceUnavailable)
	response = postUpload("/api/account-operations/upload-new", id)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("503 status=%d body=%s", response.Code, response.Body.String())
	}
	assertOperation(t, id, "failed", "node_management_unavailable", true, true)
	mutationStatus.Store(0)

	// An unreviewed mutation 5xx is conservatively durable as outcome_unknown.
	id = uuid.New()
	snapshot.Store(`{"files":[]}`)
	mutationStatus.Store(http.StatusInternalServerError)
	response = postUpload("/api/account-operations/upload-new", id)
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"outcome_unknown"`) {
		t.Fatalf("500 status=%d body=%s", response.Code, response.Body.String())
	}
	assertOperation(t, id, "outcome_unknown", "", false, false)
	mutationStatus.Store(0)

	// Override routes use the same durable operation and receipt path. Both
	// overrides leave execution state unchanged and are independently replayable.
	lifecycleID := uuid.New()
	lifecycleResponse := override("/api/account-operations/"+id.String()+"/lifecycle-override", lifecycleID, "process_restarted", "OVERRIDE UNKNOWN OPERATION LIFECYCLE BLOCK")
	if lifecycleResponse.Code != http.StatusOK {
		t.Fatalf("lifecycle override status=%d body=%s", lifecycleResponse.Code, lifecycleResponse.Body.String())
	}
	lifecycleBody := lifecycleResponse.Body.String()
	lifecycleReplay := override("/api/account-operations/"+id.String()+"/lifecycle-override", lifecycleID, "process_restarted", "OVERRIDE UNKNOWN OPERATION LIFECYCLE BLOCK")
	if lifecycleReplay.Code != lifecycleResponse.Code || lifecycleReplay.Body.String() != lifecycleBody {
		t.Fatal("lifecycle override replay changed receipt")
	}
	sameID := uuid.New()
	sameResponse := override("/api/account-operations/"+id.String()+"/same-account-override", sameID, "risk_accepted", "OVERRIDE UNKNOWN OPERATION SAME-ACCOUNT BLOCK")
	if sameResponse.Code != http.StatusOK {
		t.Fatalf("same-account override status=%d body=%s", sameResponse.Code, sameResponse.Body.String())
	}
	assertOperation(t, id, "outcome_unknown", "", false, false)

	operationResponse := get("/api/account-operations/"+id.String(), true)
	if operationResponse.Code != http.StatusOK || !strings.Contains(operationResponse.Body.String(), `"execution_state":"outcome_unknown"`) {
		t.Fatalf("operation GET status=%d body=%s", operationResponse.Code, operationResponse.Body.String())
	}
	unauthenticatedID := uuid.New()
	unauthenticated := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18080/api/account-operations/disable", strings.NewReader(`{"command_id":"`+unauthenticatedID.String()+`","node_instance_id":"`+nodeID.String()+`","account_key":"antigravity:user@example.invalid"}`))
	unauthenticated.Header.Set("Content-Type", "application/json")
	unauthenticatedResponse := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticatedResponse, unauthenticated)
	if unauthenticatedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d", unauthenticatedResponse.Code)
	}
	if err = owner.QueryRow(ctx, `SELECT count(*) FROM admin_command_registry WHERE command_id=$1`, unauthenticatedID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("unauthenticated request persisted")
	}

	// Response loss after the native request is issued is ambiguous.
	mutationMode.Store(2)
	id = uuid.New()
	snapshot.Store(`{"files":[]}`)
	response = postUpload("/api/account-operations/upload-new", id)
	if response.Code != http.StatusAccepted {
		t.Fatalf("response loss status=%d body=%s", response.Code, response.Body.String())
	}
	assertOperation(t, id, "outcome_unknown", "", false, false)
	mutationMode.Store(0)
	// A canceled request is likewise ambiguous after the native POST began.
	mutationMode.Store(3)
	id = uuid.New()
	response = postUploadTimeout("/api/account-operations/upload-new", id, 10*time.Millisecond)
	if response.Code != http.StatusAccepted {
		t.Fatalf("timeout status=%d body=%s", response.Code, response.Body.String())
	}
	assertOperation(t, id, "outcome_unknown", "", false, false)
	mutationMode.Store(0)

	// Exact terminal upload replay returns the stored bytes and sends no GET or mutation.
	id = uuid.New()
	snapshot.Store(`{"files":[]}`)
	first := postUpload("/api/account-operations/upload-new", id)
	if first.Code != http.StatusOK {
		t.Fatalf("replay setup=%d", first.Code)
	}
	firstBody := first.Body.String()
	getsBefore := mutationCount.Load()
	replay := postUpload("/api/account-operations/upload-new", id)
	if replay.Code != first.Code || replay.Body.String() != firstBody || mutationCount.Load() != getsBefore {
		t.Fatal("upload replay was reevaluated or mutated")
	}
}
