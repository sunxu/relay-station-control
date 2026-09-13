package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	authn "github.com/sunxu/relay-station-control/internal/auth"
	assetstore "github.com/sunxu/relay-station-control/internal/store"
)

func TestGatewayAssetHTTPRoutesAndMutationSecurityPG(t *testing.T) {
	var probeRequests atomic.Int32
	probeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probeRequests.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/health" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected Gateway probe: method=%s target=%s authorization=%q", r.Method, r.URL.String(), r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer probeServer.Close()
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

	if _, err = owner.Exec(ctx, `INSERT INTO environments(environment_id,name,environment_type) VALUES('gateway-http-test','Gateway HTTP Test','dev')`); err != nil {
		t.Fatal(err)
	}

	config := testValidatedConfig(t, authn.EnvironmentDev)
	service, err := authn.NewService(runtime, config)
	if err != nil {
		t.Fatal(err)
	}
	adminID := uuid.New()
	token := "gateway-http-session-" + uuid.NewString()
	csrf := "gateway-http-csrf-" + uuid.NewString()
	tokenDigest, err := authn.ComputeDigest(config.Keyring, authn.DomainSessionDigest, token)
	if err != nil {
		t.Fatal(err)
	}
	csrfDigest, err := authn.ComputeDigest(config.Keyring, authn.DomainCSRFDigest, csrf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,role,status,activated_at)
		VALUES($1,$2,'Gateway API Super Admin','super_admin','enabled',CURRENT_TIMESTAMP)`, adminID, "gateway_http_"+adminID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO control_admin_sessions
		(session_id,admin_id,token_digest,csrf_digest,key_version,mfa_method,absolute_expires_at)
		VALUES($1,$2,$3,$4,$5,'none',CURRENT_TIMESTAMP+interval '12 hours')`,
		uuid.New(), adminID, tokenDigest.Sum[:], csrfDigest.Sum[:], int32(tokenDigest.KeyVersion)); err != nil {
		t.Fatal(err)
	}

	assets, err := assetstore.NewAssetRepository(runtime)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := assetstore.NewGatewayLifecycleRepository(runtime, []byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewAuthenticatedServerWithAssets("test", service, assets, NewAssetMetrics())
	if err != nil {
		t.Fatal(err)
	}
	if err = server.SetGatewayLifecycleManager(manager); err != nil {
		t.Fatal(err)
	}
	handler := HandlerWithOptions(server, ChiServerOptions{ErrorHandlerFunc: server.PrepareGeneratedError})

	doAs := func(method, path, body, csrfProof, sessionToken string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, "http://127.0.0.1:18080"+path, strings.NewReader(body))
		request.Header.Set("Origin", "http://127.0.0.1:18080")
		request.Header.Set("Content-Type", "application/json")
		if csrfProof != "" {
			request.Header.Set("X-CSRF-Token", csrfProof)
		}
		request.AddCookie(&http.Cookie{Name: authn.DevSessionCookieName, Value: sessionToken})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Request-ID") == "" {
			t.Fatalf("%s %s missing security headers: %v", method, path, response.Header())
		}
		return response
	}
	do := func(method, path, body, csrfProof string) *httptest.ResponseRecorder {
		return doAs(method, path, body, csrfProof, token)
	}

	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/assets/gateway", nil))
	if unauthenticated.Code != http.StatusUnauthorized || unauthenticated.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unauthenticated read status=%d headers=%v", unauthenticated.Code, unauthenticated.Header())
	}

	missingCSRF := do(http.MethodPost, "/api/assets/gateways", `{}`, "")
	if missingCSRF.Code != http.StatusForbidden || strings.Contains(missingCSRF.Body.String(), "Gateway API Super Admin") {
		t.Fatalf("missing csrf status=%d body=%s", missingCSRF.Code, missingCSRF.Body.String())
	}

	for _, invalidSecret := range []string{
		"http://secret", "https://secret", "vault://has space", "vault://has\tcontrol",
		"vault://has?query", "vault://has#fragment", "vault://has@identity",
	} {
		body := fmt.Sprintf(`{"command_id":"%s","new_instance_id":"%s","display_name":"Invalid Secret","management_endpoint":"http://invalid-secret.example","reader_secret_ref":%q}`,
			uuid.New(), uuid.New(), invalidSecret)
		response := do(http.MethodPost, "/api/assets/gateways", body, csrf)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"secret_configuration_invalid"`) {
			t.Fatalf("invalid secret %q status=%d body=%s", invalidSecret, response.Code, response.Body.String())
		}
	}
	var rejectedRows, rejectedReceipts, rejectedAudits int
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM gateway_instances`).Scan(&rejectedRows); err != nil {
		t.Fatal(err)
	}
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM asset_admin_command_receipts`).Scan(&rejectedReceipts); err != nil {
		t.Fatal(err)
	}
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE category='asset_gateway' AND action='gateway.register' AND result='success'`).Scan(&rejectedAudits); err != nil {
		t.Fatal(err)
	}
	if rejectedRows != 0 || rejectedReceipts != 0 || rejectedAudits != 0 {
		t.Fatalf("invalid secrets mutated rows/receipts/audits=%d/%d/%d", rejectedRows, rejectedReceipts, rejectedAudits)
	}

	firstID := uuid.New()
	registerCommandID := uuid.New()
	registerBody := fmt.Sprintf(`{"command_id":"%s","new_instance_id":"%s","display_name":"Gateway A","management_endpoint":%q,"reader_secret_ref":"vault://valid/reference"}`,
		registerCommandID, firstID, probeServer.URL)
	registered := do(http.MethodPost, "/api/assets/gateways", registerBody, csrf)
	if registered.Code != http.StatusCreated || strings.Contains(registered.Body.String(), "reader_secret_ref") {
		t.Fatalf("register status=%d body=%s", registered.Code, registered.Body.String())
	}
	registerReplay := do(http.MethodPost, "/api/assets/gateways", registerBody, csrf)
	if registerReplay.Code != http.StatusCreated || !gatewayJSONEqual(registerReplay.Body.Bytes(), registered.Body.Bytes()) {
		t.Fatalf("register replay status=%d body=%s want=%s", registerReplay.Code, registerReplay.Body.String(), registered.Body.String())
	}

	otherAdminID := uuid.New()
	otherToken := "gateway-http-other-session-" + uuid.NewString()
	otherCSRF := "gateway-http-other-csrf-" + uuid.NewString()
	otherTokenDigest, err := authn.ComputeDigest(config.Keyring, authn.DomainSessionDigest, otherToken)
	if err != nil {
		t.Fatal(err)
	}
	otherCSRFDigest, err := authn.ComputeDigest(config.Keyring, authn.DomainCSRFDigest, otherCSRF)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,role,status,activated_at)
		VALUES($1,$2,'Other Gateway API Super Admin','super_admin','enabled',CURRENT_TIMESTAMP)`, otherAdminID, "gateway_http_"+otherAdminID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO control_admin_sessions
		(session_id,admin_id,token_digest,csrf_digest,key_version,mfa_method,absolute_expires_at)
		VALUES($1,$2,$3,$4,$5,'none',CURRENT_TIMESTAMP+interval '12 hours')`,
		uuid.New(), otherAdminID, otherTokenDigest.Sum[:], otherCSRFDigest.Sum[:], int32(otherTokenDigest.KeyVersion)); err != nil {
		t.Fatal(err)
	}
	withoutKey, err := assetstore.NewGatewayLifecycleRepository(runtime, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = server.SetGatewayLifecycleManager(withoutKey); err != nil {
		t.Fatal(err)
	}
	actorMismatchRequests := []struct {
		name, method, path, body string
	}{
		{"invalid endpoint", http.MethodPost, "/api/assets/gateways", fmt.Sprintf(`{"command_id":"%s","new_instance_id":"%s","display_name":"Actor mismatch","management_endpoint":"https://invalid.example","reader_secret_ref":"vault://valid/reference"}`, registerCommandID, uuid.New())},
		{"invalid secret", http.MethodPost, "/api/assets/gateways", fmt.Sprintf(`{"command_id":"%s","new_instance_id":"%s","display_name":"Actor mismatch","management_endpoint":"http://valid.example","reader_secret_ref":"http://invalid-secret"}`, registerCommandID, uuid.New())},
		{"missing K1", http.MethodPost, "/api/assets/gateways", fmt.Sprintf(`{"command_id":"%s","new_instance_id":"%s","display_name":"Actor mismatch","management_endpoint":"http://valid.example","reader_secret_ref":"vault://valid/reference"}`, registerCommandID, uuid.New())},
		{"stale revision", http.MethodPatch, "/api/assets/gateways/" + firstID.String(), fmt.Sprintf(`{"command_id":"%s","expected_revision":"999","display_name":"Actor mismatch"}`, registerCommandID)},
		{"retired target", http.MethodPost, "/api/assets/gateways/" + uuid.NewString() + "/retire", fmt.Sprintf(`{"command_id":"%s","expected_revision":"1"}`, registerCommandID)},
	}
	for _, test := range actorMismatchRequests {
		response := doAs(test.method, test.path, test.body, otherCSRF, otherToken)
		if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"command_conflict"`) {
			t.Fatalf("actor mismatch %s status=%d body=%s", test.name, response.Code, response.Body.String())
		}
	}
	if err = server.SetGatewayLifecycleManager(manager); err != nil {
		t.Fatal(err)
	}
	var actorMismatchRows, actorMismatchReceipts, actorMismatchAudits int
	if err = owner.QueryRow(ctx, `SELECT count(*) FROM gateway_instances`).Scan(&actorMismatchRows); err != nil {
		t.Fatal(err)
	}
	if err = owner.QueryRow(ctx, `SELECT count(*) FROM asset_admin_command_receipts`).Scan(&actorMismatchReceipts); err != nil {
		t.Fatal(err)
	}
	if err = owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE category='asset_gateway' AND action='gateway.register' AND result='success'`).Scan(&actorMismatchAudits); err != nil {
		t.Fatal(err)
	}
	if actorMismatchRows != 1 || actorMismatchReceipts != 1 || actorMismatchAudits != 1 {
		t.Fatalf("actor mismatch side effects rows/receipts/audits=%d/%d/%d", actorMismatchRows, actorMismatchReceipts, actorMismatchAudits)
	}

	current := do(http.MethodGet, "/api/assets/gateway", "", "")
	if current.Code != http.StatusOK || !strings.Contains(current.Body.String(), firstID.String()) {
		t.Fatalf("current status=%d body=%s", current.Code, current.Body.String())
	}
	assertActiveGatewayNullableFields(t, current.Body.Bytes(), "gateway")
	list := do(http.MethodGet, "/api/assets/gateways?lifecycle=active&limit=50", "", "")
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), firstID.String()) || !strings.Contains(list.Body.String(), `"active":1`) {
		t.Fatalf("active list status=%d body=%s", list.Code, list.Body.String())
	}
	assertJSONFields(t, list.Body.Bytes(), "items", "next_cursor", "gateway_counts")
	assertJSONNullFields(t, list.Body.Bytes(), "next_cursor")
	detail := do(http.MethodGet, "/api/assets/gateways/"+firstID.String(), "", "")
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), firstID.String()) {
		t.Fatalf("detail status=%d body=%s", detail.Code, detail.Body.String())
	}
	assertJSONFields(t, detail.Body.Bytes(), "asset", "predecessor", "successor")
	assertJSONNullFields(t, detail.Body.Bytes(), "predecessor", "successor")
	assertActiveGatewayNullableFields(t, detail.Body.Bytes(), "asset")
	health := do(http.MethodGet, "/api/assets/gateways/"+firstID.String()+"/health", "", "")
	if health.Code != http.StatusOK || !strings.Contains(health.Body.String(), `"result":"healthy"`) {
		t.Fatalf("health status=%d body=%s", health.Code, health.Body.String())
	}
	connection := do(http.MethodPost, "/api/assets/gateways/"+firstID.String()+"/connection-test", `{}`, csrf)
	if connection.Code != http.StatusOK || !strings.Contains(connection.Body.String(), `"result":"healthy"`) || probeRequests.Load() != 2 {
		t.Fatalf("connection status=%d requests=%d body=%s", connection.Code, probeRequests.Load(), connection.Body.String())
	}
	var probeAudits int
	if err = owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE category='asset_gateway' AND action IN ('gateway.health','gateway.connection_test') AND result='success' AND details->>'instance_id'=$1`, firstID.String()).Scan(&probeAudits); err != nil || probeAudits != 2 {
		t.Fatalf("probe audits=%d err=%v", probeAudits, err)
	}

	editCommandID := uuid.New()
	editBody := fmt.Sprintf(`{"command_id":"%s","expected_revision":"1","display_name":"Gateway A edited"}`, editCommandID)
	edited := do(http.MethodPatch, "/api/assets/gateways/"+firstID.String(), editBody, csrf)
	if edited.Code != http.StatusOK || !strings.Contains(edited.Body.String(), `"revision":"2"`) {
		t.Fatalf("edit status=%d body=%s", edited.Code, edited.Body.String())
	}
	stale := do(http.MethodPatch, "/api/assets/gateways/"+firstID.String(), fmt.Sprintf(`{"command_id":"%s","expected_revision":"1","display_name":"stale"}`, uuid.New()), csrf)
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), `"code":"stale_revision"`) {
		t.Fatalf("stale edit status=%d body=%s", stale.Code, stale.Body.String())
	}
	invalidHTTPS := do(http.MethodPatch, "/api/assets/gateways/"+firstID.String(), fmt.Sprintf(`{"command_id":"%s","expected_revision":"2","management_endpoint":"https://gateway.example"}`, uuid.New()), csrf)
	if invalidHTTPS.Code != http.StatusBadRequest || !strings.Contains(invalidHTTPS.Body.String(), `"code":"invalid_endpoint"`) || strings.Contains(invalidHTTPS.Body.String(), "https://") {
		t.Fatalf("https edit status=%d body=%s", invalidHTTPS.Code, invalidHTTPS.Body.String())
	}

	secondID := uuid.New()
	replaceBody := fmt.Sprintf(`{"command_id":"%s","expected_revision":"2","new_instance_id":"%s","display_name":"Gateway B","management_endpoint":"http://gateway-b.example"}`,
		uuid.New(), secondID)
	replaced := do(http.MethodPost, "/api/assets/gateways/"+firstID.String()+"/replace", replaceBody, csrf)
	if replaced.Code != http.StatusOK || !strings.Contains(replaced.Body.String(), secondID.String()) {
		t.Fatalf("replace status=%d body=%s", replaced.Code, replaced.Body.String())
	}
	replaceReplay := do(http.MethodPost, "/api/assets/gateways/"+firstID.String()+"/replace", replaceBody, csrf)
	if replaceReplay.Code != http.StatusOK || !gatewayJSONEqual(replaceReplay.Body.Bytes(), replaced.Body.Bytes()) {
		t.Fatalf("replace replay status=%d body=%s want=%s", replaceReplay.Code, replaceReplay.Body.String(), replaced.Body.String())
	}
	oldDetail := do(http.MethodGet, "/api/assets/gateways/"+firstID.String(), "", "")
	if oldDetail.Code != http.StatusOK || !strings.Contains(oldDetail.Body.String(), `"lifecycle_status":"retired"`) || !strings.Contains(oldDetail.Body.String(), secondID.String()) {
		t.Fatalf("retired detail status=%d body=%s", oldDetail.Code, oldDetail.Body.String())
	}
	all := do(http.MethodGet, "/api/assets/gateways?lifecycle=all&limit=50", "", "")
	if all.Code != http.StatusOK || !strings.Contains(all.Body.String(), firstID.String()) || !strings.Contains(all.Body.String(), secondID.String()) || !strings.Contains(all.Body.String(), `"retired":1`) {
		t.Fatalf("all list status=%d body=%s", all.Code, all.Body.String())
	}

	retireBody := fmt.Sprintf(`{"command_id":"%s","expected_revision":"1"}`, uuid.New())
	retired := do(http.MethodPost, "/api/assets/gateways/"+secondID.String()+"/retire", retireBody, csrf)
	if retired.Code != http.StatusOK || !strings.Contains(retired.Body.String(), `"lifecycle_status":"retired"`) {
		t.Fatalf("retire status=%d body=%s", retired.Code, retired.Body.String())
	}
	noCurrent := do(http.MethodGet, "/api/assets/gateway", "", "")
	if noCurrent.Code != http.StatusOK || !strings.Contains(noCurrent.Body.String(), "not_registered") {
		t.Fatalf("no current status=%d body=%s", noCurrent.Code, noCurrent.Body.String())
	}

	var receiptCount, auditCount int
	if err = owner.QueryRow(ctx, `SELECT count(*) FROM asset_admin_command_receipts`).Scan(&receiptCount); err != nil {
		t.Fatal(err)
	}
	if err = owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE category='asset_gateway' AND action IN ('gateway.register','gateway.edit','gateway.retire','gateway.replace') AND result='success'`).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if receiptCount != 4 || auditCount != 4 {
		t.Fatalf("receipt/audit count=%d/%d, want 4/4", receiptCount, auditCount)
	}
	if _, err = owner.Exec(ctx, `ALTER TABLE asset_admin_command_receipts DISABLE TRIGGER asset_admin_command_receipts_immutable`); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `ALTER TABLE asset_admin_command_receipts DROP CONSTRAINT asset_admin_command_receipts_encoding_check`); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `UPDATE asset_admin_command_receipts SET intent_encoding_version=2 WHERE command_id=$1`, registerCommandID); err != nil {
		t.Fatal(err)
	}
	unknownEncoding := do(http.MethodPost, "/api/assets/gateways", registerBody, csrf)
	if unknownEncoding.Code != http.StatusServiceUnavailable || !strings.Contains(unknownEncoding.Body.String(), `"code":"service_unavailable"`) {
		t.Fatalf("unknown receipt encoding status=%d body=%s", unknownEncoding.Code, unknownEncoding.Body.String())
	}

	var response GatewayAssetListResponse
	if err = json.Unmarshal(all.Body.Bytes(), &response); err != nil || response.GatewayCounts.Total != 2 {
		t.Fatalf("decode all response=%+v err=%v", response, err)
	}
}

func assertJSONFields(t *testing.T, body []byte, fields ...string) {
	t.Helper()
	var object map[string]any
	if err := json.Unmarshal(body, &object); err != nil {
		t.Fatal(err)
	}
	for _, field := range fields {
		if _, exists := object[field]; !exists {
			t.Fatalf("response omitted required field %q: %s", field, body)
		}
	}
}

func assertJSONNullFields(t *testing.T, body []byte, fields ...string) {
	t.Helper()
	var object map[string]any
	if err := json.Unmarshal(body, &object); err != nil {
		t.Fatal(err)
	}
	for _, field := range fields {
		value, exists := object[field]
		if !exists || value != nil {
			t.Fatalf("response field %q = %#v, exists=%v; want explicit null", field, value, exists)
		}
	}
}

func assertActiveGatewayNullableFields(t *testing.T, body []byte, envelope string) {
	t.Helper()
	var object map[string]any
	if err := json.Unmarshal(body, &object); err != nil {
		t.Fatal(err)
	}
	asset, ok := object[envelope].(map[string]any)
	if !ok {
		t.Fatalf("response omitted asset envelope %q: %s", envelope, body)
	}
	for _, field := range []string{"retired_at", "retired_by", "retire_reason"} {
		value, exists := asset[field]
		if !exists || value != nil {
			t.Fatalf("active asset field %q = %#v, exists=%v; want explicit null", field, value, exists)
		}
	}
}

func gatewayJSONEqual(left, right []byte) bool {
	var leftValue, rightValue any
	return json.Unmarshal(left, &leftValue) == nil && json.Unmarshal(right, &rightValue) == nil && fmt.Sprint(leftValue) == fmt.Sprint(rightValue)
}
