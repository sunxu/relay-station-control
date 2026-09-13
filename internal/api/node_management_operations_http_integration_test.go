package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	authn "github.com/sunxu/relay-station-control/internal/auth"
	"github.com/sunxu/relay-station-control/internal/drivers"
	"github.com/sunxu/relay-station-control/internal/drivers/cliproxyapi"
	assetstore "github.com/sunxu/relay-station-control/internal/store"
)

type nodeProbeCountingSecretResolver struct{ calls atomic.Int32 }

func (r *nodeProbeCountingSecretResolver) Resolve(context.Context, drivers.SecretReference) (*drivers.Secret, error) {
	r.calls.Add(1)
	return nil, errors.New("probe SecretResolver must not be called")
}

// TestNodeManagementOperationsHTTPIntegration exercises the generated HTTP
// routes through the real session middleware and migration-35 runtime store.
// The remote Node is a bounded local fixture. The real CLIProxyAPI Driver is
// used so this test proves that probe authorization and transport remain
// secret-free.
func TestNodeManagementOperationsHTTPIntegration(t *testing.T) {
	var probeRequests atomic.Int32
	probeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probeRequests.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/healthz" || r.URL.RawQuery != "" {
			t.Errorf("unexpected Node probe: method=%s target=%s", r.Method, r.URL.String())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer probeServer.Close()
	secretResolver := &nodeProbeCountingSecretResolver{}
	probeDriver, err := cliproxyapi.NewDriver(cliproxyapi.DriverConfig{SecretResolver: secretResolver})
	if err != nil {
		t.Fatal(err)
	}
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

	adminID, token, csrf := uuid.New(), "node-ops-session-"+uuid.NewString(), "node-ops-csrf-"+uuid.NewString()
	config := testValidatedConfig(t, authn.EnvironmentDev)
	service, err := authn.NewService(runtime, config)
	if err != nil {
		t.Fatal(err)
	}
	tokenDigest, err := authn.ComputeDigest(config.Keyring, authn.DomainSessionDigest, token)
	if err != nil {
		t.Fatal(err)
	}
	csrfDigest, err := authn.ComputeDigest(config.Keyring, authn.DomainCSRFDigest, csrf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `
		INSERT INTO control_admin_users(admin_id,login_name,display_name,role,status,activated_at)
		VALUES($1,$2,'Node operations integration admin','super_admin','enabled',clock_timestamp())
		ON CONFLICT (admin_id) DO NOTHING`, adminID, "node_ops_"+adminID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `
		INSERT INTO control_admin_sessions(session_id,admin_id,token_digest,csrf_digest,key_version,mfa_method,absolute_expires_at)
		VALUES($1,$2,$3,$4,$5,'none',clock_timestamp()+interval '1 hour')`,
		uuid.New(), adminID, tokenDigest.Sum[:], csrfDigest.Sum[:], int32(tokenDigest.KeyVersion)); err != nil {
		t.Fatal(err)
	}

	if _, err = owner.Exec(ctx, `
		INSERT INTO node_drivers(node_type,driver_contract_version,display_name)
		VALUES('cliproxyapi','cliproxyapi.auth-files.v1','CLIProxyAPI')
		ON CONFLICT (node_type,driver_contract_version) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `
		INSERT INTO driver_capabilities(node_type,driver_contract_version,capability)
		VALUES ('cliproxyapi','cliproxyapi.auth-files.v1','management_health_read'),
		       ('cliproxyapi','cliproxyapi.auth-files.v1','management_account_inventory_read')
		ON CONFLICT (node_type,driver_contract_version,capability) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	instanceID := uuid.New()
	if _, err = owner.Exec(ctx, `
		INSERT INTO relay_node_assets(instance_id,display_name,node_type,driver_contract_version,management_endpoint,reader_secret_ref)
		VALUES($1,'HTTP integration Node','cliproxyapi','cliproxyapi.auth-files.v1',$2,NULL)`, instanceID, probeServer.URL); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `
		INSERT INTO node_capabilities(instance_id,node_type,driver_contract_version,capability)
		VALUES($1,'cliproxyapi','cliproxyapi.auth-files.v1','management_health_read'),
		       ($1,'cliproxyapi','cliproxyapi.auth-files.v1','management_account_inventory_read')`, instanceID); err != nil {
		t.Fatal(err)
	}

	assets, err := assetstore.NewAssetRepository(runtime)
	if err != nil {
		t.Fatal(err)
	}
	monitoring, err := assetstore.NewNodeMonitoringRepository(runtime)
	if err != nil {
		t.Fatal(err)
	}
	metrics := NewAssetMetrics()
	server, err := NewAuthenticatedServerWithAssets("test", service, assets, metrics)
	if err != nil {
		t.Fatal(err)
	}
	if err = server.SetNodeProbeAuthorizer(monitoring); err != nil {
		t.Fatal(err)
	}
	server.SetNodeProbeAuditWriter(monitoring)
	server.SetNodeMonitoringOperator(monitoring)
	if err = server.SetNodeProbeRegistry(probeDriver); err != nil {
		t.Fatal(err)
	}
	handler := HandlerWithOptions(server, ChiServerOptions{ErrorHandlerFunc: server.PrepareGeneratedError})

	request := func(method, path, body, origin, proof string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "http://127.0.0.1:18080"+path, strings.NewReader(body))
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if proof != "" {
			r.Header.Set("X-CSRF-Token", proof)
		}
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: authn.DevSessionCookieName, Value: token})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Request-ID") == "" {
			t.Fatalf("%s %s missing security headers: %v", method, path, w.Header())
		}
		return w
	}
	postOrigin := "http://127.0.0.1:18080"

	health := request(http.MethodGet, "/api/assets/nodes/"+instanceID.String()+"/health", "", "", "")
	if health.Code != http.StatusOK || !strings.Contains(health.Body.String(), `"result":"success"`) || probeRequests.Load() != 1 {
		t.Fatalf("health status=%d calls=%d body=%s", health.Code, probeRequests.Load(), health.Body.String())
	}
	connection := request(http.MethodPost, "/api/assets/nodes/"+instanceID.String()+"/connection-test", `{}`, postOrigin, csrf)
	if connection.Code != http.StatusOK || probeRequests.Load() != 2 {
		t.Fatalf("connection-test status=%d calls=%d body=%s", connection.Code, probeRequests.Load(), connection.Body.String())
	}
	if secretResolver.calls.Load() != 0 {
		t.Fatalf("probe SecretResolver calls=%d, want 0", secretResolver.calls.Load())
	}
	if strings.Contains(health.Body.String()+connection.Body.String(), "reader_secret_ref") {
		t.Fatal("probe response leaked reader_secret_ref")
	}
	var auditCount int
	if err = owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE actor_admin_id=$1 AND action='node.health'`, adminID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("health audit count=%d", auditCount)
	}
	if err = owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE actor_admin_id=$1 AND action='node.connection_test'`, adminID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("connection-test audit count=%d", auditCount)
	}
	var auditDetails string
	if err = owner.QueryRow(ctx, `SELECT coalesce(string_agg(details::text, ' '), '') FROM audit_logs WHERE actor_admin_id=$1 AND action IN ('node.health','node.connection_test')`, adminID).Scan(&auditDetails); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(auditDetails, "reader_secret_ref") || strings.Contains(auditDetails, "vault://node/ops") {
		t.Fatalf("probe audit leaked secret metadata: %s", auditDetails)
	}

	commandID := uuid.New()
	enableBody := `{"command_id":"` + commandID.String() + `"}`
	enable := request(http.MethodPost, "/api/assets/nodes/"+instanceID.String()+"/monitoring-enable", enableBody, postOrigin, csrf)
	var enableProjection struct {
		Result string `json:"result"`
	}
	if enable.Code != http.StatusOK || json.Unmarshal(enable.Body.Bytes(), &enableProjection) != nil || enableProjection.Result != "enabled" {
		t.Fatalf("enable status=%d body=%s", enable.Code, enable.Body.String())
	}
	disableID := uuid.New()
	disableBody := `{"command_id":"` + disableID.String() + `"}`
	disable := request(http.MethodPost, "/api/assets/nodes/"+instanceID.String()+"/monitoring-disable", disableBody, postOrigin, csrf)
	if disable.Code != http.StatusOK {
		t.Fatalf("disable status=%d body=%s", disable.Code, disable.Body.String())
	}
	disableReplay := request(http.MethodPost, "/api/assets/nodes/"+instanceID.String()+"/monitoring-disable", disableBody, postOrigin, csrf)
	if disableReplay.Code != disable.Code || disableReplay.Body.String() != disable.Body.String() {
		t.Errorf("disable replay status/body=%d/%s want %d/%s", disableReplay.Code, disableReplay.Body.String(), disable.Code, disable.Body.String())
	}

	for _, test := range []struct {
		name, method, path, body, origin, proof string
		want                                    int
	}{
		{"malformed", http.MethodPost, "/api/assets/nodes/" + instanceID.String() + "/monitoring-enable", `{"command_id":`, postOrigin, csrf, http.StatusBadRequest},
		{"unknown field", http.MethodPost, "/api/assets/nodes/" + instanceID.String() + "/monitoring-enable", `{"command_id":"` + uuid.NewString() + `","extra":1}`, postOrigin, csrf, http.StatusBadRequest},
		{"oversize", http.MethodPost, "/api/assets/nodes/" + instanceID.String() + "/monitoring-enable", `{"command_id":"` + uuid.NewString() + `","extra":"` + strings.Repeat("x", 2048) + `"}`, postOrigin, csrf, http.StatusBadRequest},
		{"missing csrf", http.MethodPost, "/api/assets/nodes/" + instanceID.String() + "/monitoring-enable", `{}`, postOrigin, "", http.StatusForbidden},
		{"wrong same-origin", http.MethodPost, "/api/assets/nodes/" + instanceID.String() + "/monitoring-enable", `{}`, "http://other.invalid", csrf, http.StatusForbidden},
		{"not found", http.MethodGet, "/api/assets/nodes/" + uuid.NewString() + "/health", "", "", "", http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := request(test.method, test.path, test.body, test.origin, test.proof)
			if response.Code != test.want {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			var envelope ErrorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || envelope.Code == "" || envelope.Message == "" || envelope.RequestId == "" {
				t.Fatalf("not fixed error envelope: %s", response.Body.String())
			}
		})
	}

	unsupportedID := uuid.New()
	if _, err = owner.Exec(ctx, `
		INSERT INTO relay_node_assets(instance_id,display_name,node_type,driver_contract_version,management_endpoint,reader_secret_ref)
		VALUES($1,'Unsupported probe Node','cliproxyapi','cliproxyapi.auth-files.v1','http://node.invalid/management','vault://node/unsupported')`, unsupportedID); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `
		INSERT INTO node_capabilities(instance_id,node_type,driver_contract_version,capability)
		VALUES($1,'cliproxyapi','cliproxyapi.auth-files.v1','management_account_inventory_read')`, unsupportedID); err != nil {
		t.Fatal(err)
	}
	conflict := request(http.MethodGet, "/api/assets/nodes/"+unsupportedID.String()+"/health", "", "", "")
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflict status=%d body=%s", conflict.Code, conflict.Body.String())
	}
	var conflictEnvelope ErrorResponse
	if err = json.Unmarshal(conflict.Body.Bytes(), &conflictEnvelope); err != nil || conflictEnvelope.Code != ErrorCode("capability_unsupported") {
		t.Fatalf("conflict envelope=%s", conflict.Body.String())
	}

	withoutProbe := NewAuthenticatedServer("test", service)
	withoutProbeHandler := HandlerWithOptions(withoutProbe, ChiServerOptions{ErrorHandlerFunc: withoutProbe.PrepareGeneratedError})
	serviceUnavailable := httptest.NewRecorder()
	serviceUnavailableRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:18080/api/assets/nodes/"+instanceID.String()+"/health", nil)
	serviceUnavailableRequest.AddCookie(&http.Cookie{Name: authn.DevSessionCookieName, Value: token})
	withoutProbeHandler.ServeHTTP(serviceUnavailable, serviceUnavailableRequest)
	if serviceUnavailable.Code != http.StatusServiceUnavailable || serviceUnavailable.Header().Get("Cache-Control") != "no-store" || serviceUnavailable.Header().Get("X-Request-ID") == "" {
		t.Fatalf("503 response status=%d headers=%v body=%s", serviceUnavailable.Code, serviceUnavailable.Header(), serviceUnavailable.Body.String())
	}
	var unavailableEnvelope ErrorResponse
	if err = json.Unmarshal(serviceUnavailable.Body.Bytes(), &unavailableEnvelope); err != nil || unavailableEnvelope.Code == "" || unavailableEnvelope.Message == "" {
		t.Fatalf("503 envelope=%s", serviceUnavailable.Body.String())
	}
}
