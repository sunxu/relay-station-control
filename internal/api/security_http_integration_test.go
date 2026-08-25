package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	authn "github.com/sunxu/relay-station-control/internal/auth"
)

func TestHTTPAuthenticationRouteMatrixAndSecurityEnvelope(t *testing.T) {
	server, cleanup := authenticatedTestServer(t)
	defer cleanup()
	handler := HandlerWithOptions(server, ChiServerOptions{ErrorHandlerFunc: server.PrepareGeneratedError})

	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		csrf       string
		bearer     string
		wantStatus int
		wantCode   ErrorCode
	}{
		{name: "health is anonymous", method: http.MethodGet, path: "/api/healthz", wantStatus: http.StatusOK},
		{name: "bootstrap status is anonymous", method: http.MethodGet, path: "/api/bootstrap/status", wantStatus: http.StatusOK},
		{name: "login is anonymous but validates input", method: http.MethodPost, path: "/api/auth/login", body: `{}`, wantStatus: http.StatusBadRequest, wantCode: ErrorCodeValidationFailed},
		{name: "activation is anonymous but validates input", method: http.MethodPost, path: "/api/admin-activations/complete", body: `{}`, wantStatus: http.StatusBadRequest, wantCode: ErrorCodeValidationFailed},
		{name: "logout is anonymous and idempotent", method: http.MethodPost, path: "/api/auth/logout", wantStatus: http.StatusNoContent},
		{name: "session requires authentication", method: http.MethodGet, path: "/api/auth/session", wantStatus: http.StatusUnauthorized, wantCode: ErrorCodeUnauthorized},
		{name: "administrator list requires authentication", method: http.MethodGet, path: "/api/admins", wantStatus: http.StatusUnauthorized, wantCode: ErrorCodeUnauthorized},
		{name: "service bearer is not an administrator session", method: http.MethodGet, path: "/api/admins", bearer: "gateway-service-credential", wantStatus: http.StatusUnauthorized, wantCode: ErrorCodeUnauthorized},
		{name: "unsafe operation rejects missing csrf before authentication", method: http.MethodPost, path: "/api/admins", body: `{}`, wantStatus: http.StatusForbidden, wantCode: ErrorCodeCsrfInvalid},
		{name: "unsafe operation with csrf still requires authentication", method: http.MethodPost, path: "/api/admins", body: `{}`, csrf: "opaque-proof", wantStatus: http.StatusUnauthorized, wantCode: ErrorCodeUnauthorized},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			if test.body != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			if test.csrf != "" {
				request.Header.Set("X-CSRF-Token", test.csrf)
			}
			if test.bearer != "" {
				request.Header.Set("Authorization", "Bearer "+test.bearer)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if test.path != "/api/healthz" && response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("missing no-store: %v", response.Header())
			}
			if response.Header().Get("X-Request-ID") == "" {
				t.Fatalf("missing request ID: %v", response.Header())
			}
			if test.wantCode != "" {
				var envelope ErrorResponse
				if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
					t.Fatalf("decode error envelope: %v (%s)", err, response.Body.String())
				}
				if envelope.Code != test.wantCode || envelope.RequestId == "" || envelope.Message == "" {
					t.Fatalf("error envelope=%+v", envelope)
				}
			}
		})
	}
}

func TestLogoutDatabaseFailureReturns503WithoutClearingSessionCookie(t *testing.T) {
	databaseURL := os.Getenv("CONTROL_RUNTIME_DATABASE_TEST_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("CONTROL_DATABASE_TEST_URL")
	}
	if databaseURL == "" {
		t.Skip("set CONTROL_RUNTIME_DATABASE_TEST_URL or CONTROL_DATABASE_TEST_URL")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	service, err := authn.NewService(pool, testValidatedConfig(t, authn.EnvironmentDev, false))
	if err != nil {
		t.Fatal(err)
	}
	server := NewAuthenticatedServer("test", service)
	pool.Close()

	request := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	request.AddCookie(&http.Cookie{Name: authn.DevSessionCookieName, Value: "opaque-session"})
	response := httptest.NewRecorder()
	server.Logout(response, request, LogoutParams{})

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if values := response.Header().Values("Set-Cookie"); len(values) != 0 {
		t.Fatalf("logout cleared cookie despite database failure: %v", values)
	}
}

func TestSameOriginPolicyRejectsCrossSiteAndMissingProductionProof(t *testing.T) {
	databaseURL := os.Getenv("CONTROL_DATABASE_TEST_URL")
	if databaseURL == "" {
		t.Skip("set CONTROL_DATABASE_TEST_URL")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	service, err := authn.NewService(pool, testValidatedConfig(t, authn.EnvironmentStaging, true))
	if err != nil {
		t.Fatal(err)
	}
	server := NewAuthenticatedServer("test", service)

	for name, origin := range map[string]string{
		"cross site":   "https://attacker.invalid",
		"wrong scheme": "http://control.example",
		"missing":      "",
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "https://control.example/api/auth/logout", nil)
			request.Host = "control.example"
			if origin != "" {
				request.Header.Set("Origin", origin)
			}
			if server.sameOrigin(request) {
				t.Fatalf("origin %q accepted", origin)
			}
		})
	}
	request := httptest.NewRequest(http.MethodPost, "https://control.example/api/auth/logout", nil)
	request.Host = "control.example"
	request.Header.Set("Origin", "https://control.example")
	if !server.sameOrigin(request) {
		t.Fatal("exact HTTPS origin was rejected")
	}
}

func TestAuthenticationCanaryDoesNotLeakAcrossHTTPAuditOrMetrics(t *testing.T) {
	databaseURL := os.Getenv("CONTROL_DATABASE_TEST_URL")
	if databaseURL == "" {
		t.Skip("set CONTROL_DATABASE_TEST_URL")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	config := testValidatedConfig(t, authn.EnvironmentDev, false)
	service, err := authn.NewService(pool, config)
	if err != nil {
		t.Fatal(err)
	}
	server := NewAuthenticatedServer("test", service)
	canary := "SECRET-CANARY-" + strings.Repeat("x", 24)
	runID := strings.ReplaceAll(uuid.NewString(), "-", "")
	loginName := "canary_" + runID[:12]
	source := netip.MustParseAddr("2001:db8:" + runID[:4] + ":" + runID[4:8] + ":" + runID[8:12] + ":" + runID[12:16] + "::1")
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(fmt.Sprintf(`{"login_name":%q,"password":%q}`, loginName, canary)))
	request.RemoteAddr = "[" + source.String() + "]:12345"
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Login(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	requestID := response.Header().Get("X-Request-ID")
	if requestID == "" {
		t.Fatal("missing request ID")
	}
	account, err := authn.LoginFingerprint(config.Keyring, loginName)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := service.RequestMeta(source, requestID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM audit_logs WHERE request_id=$1`, requestID)
		_, _ = pool.Exec(ctx, `DELETE FROM control_auth_failure_windows WHERE key_version=$1 AND subject_fingerprint IN ($2,$3)`, int32(account.KeyVersion), account.Sum[:], meta.SourceFingerprint.Sum[:])
	})
	var auditJSON string
	if err = pool.QueryRow(ctx, `SELECT row_to_json(a)::text FROM audit_logs AS a WHERE request_id=$1 AND action='auth.login_password'`, requestID).Scan(&auditJSON); err != nil {
		t.Fatal(err)
	}
	metricJSON, err := json.Marshal(service.Metrics().Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	for surface, value := range map[string]string{"response": response.Body.String(), "audit": auditJSON, "metrics": string(metricJSON)} {
		if strings.Contains(value, canary) {
			t.Fatalf("secret canary leaked through %s: %s", surface, value)
		}
	}
}

func TestRuntimeRoleDisableAdministratorHTTPAndLastAdminGuard(t *testing.T) {
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
	config := testValidatedConfig(t, authn.EnvironmentDev, false)
	service, err := authn.NewService(runtime, config)
	if err != nil {
		t.Fatal(err)
	}
	server := NewAuthenticatedServer("test", service)
	handler := HandlerWithOptions(server, ChiServerOptions{ErrorHandlerFunc: server.PrepareGeneratedError})

	actorID, targetID := uuid.New(), uuid.New()
	for id, login := range map[uuid.UUID]string{actorID: "runtime_actor_" + actorID.String()[:8], targetID: "runtime_target_" + targetID.String()[:8]} {
		if _, err = owner.Exec(ctx, `INSERT INTO control_admin_users
			(admin_id,login_name,display_name,status,activated_at)
			VALUES ($1,$2,$3,'enabled',CURRENT_TIMESTAMP)`, id, login, "Runtime Disable Operator"); err != nil {
			t.Fatal(err)
		}
	}
	actorToken, csrfToken, targetToken, targetCSRFToken := "actor-"+uuid.NewString(), "csrf-"+uuid.NewString(), "target-"+uuid.NewString(), "target-csrf-"+uuid.NewString()
	actorDigest, err := authn.ComputeDigest(config.Keyring, authn.DomainSessionDigest, actorToken)
	if err != nil {
		t.Fatal(err)
	}
	csrfDigest, err := authn.ComputeDigest(config.Keyring, authn.DomainCSRFDigest, csrfToken)
	if err != nil {
		t.Fatal(err)
	}
	targetDigest, err := authn.ComputeDigest(config.Keyring, authn.DomainSessionDigest, targetToken)
	if err != nil {
		t.Fatal(err)
	}
	targetCSRFDigest, err := authn.ComputeDigest(config.Keyring, authn.DomainCSRFDigest, targetCSRFToken)
	if err != nil {
		t.Fatal(err)
	}
	actorSessionID, targetSessionID := uuid.New(), uuid.New()
	if _, err = owner.Exec(ctx, `INSERT INTO control_admin_sessions
		(session_id,admin_id,token_digest,csrf_digest,key_version,mfa_method,absolute_expires_at,reauthenticated_at)
		VALUES ($1,$2,$3,$4,$5,'none',CURRENT_TIMESTAMP+interval '12 hours',CURRENT_TIMESTAMP)`,
		actorSessionID, actorID, actorDigest.Sum[:], csrfDigest.Sum[:], int32(actorDigest.KeyVersion)); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO control_admin_sessions
		(session_id,admin_id,token_digest,csrf_digest,key_version,mfa_method,absolute_expires_at)
		VALUES ($1,$2,$3,$4,$5,'none',CURRENT_TIMESTAMP+interval '12 hours')`,
		targetSessionID, targetID, targetDigest.Sum[:], targetCSRFDigest.Sum[:], int32(targetDigest.KeyVersion)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = owner.Exec(ctx, `DELETE FROM audit_logs WHERE actor_admin_id IN ($1,$2) OR target_admin_id IN ($1,$2)`, actorID, targetID)
		_, _ = owner.Exec(ctx, `DELETE FROM control_admin_sessions WHERE admin_id IN ($1,$2)`, actorID, targetID)
		_, _ = owner.Exec(ctx, `ALTER TABLE control_admin_users DISABLE TRIGGER control_admin_users_protect_last_enabled`)
		_, _ = owner.Exec(ctx, `DELETE FROM control_admin_users WHERE admin_id IN ($1,$2)`, actorID, targetID)
		_, _ = owner.Exec(ctx, `ALTER TABLE control_admin_users ENABLE TRIGGER control_admin_users_protect_last_enabled`)
	})

	doDisable := func(t *testing.T, target uuid.UUID) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/api/admins/"+target.String()+"/disable", strings.NewReader(`{"reason":"runtime role security regression"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-CSRF-Token", csrfToken)
		request.AddCookie(&http.Cookie{Name: authn.DevSessionCookieName, Value: actorToken})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	self := doDisable(t, actorID)
	if self.Code != http.StatusForbidden {
		t.Fatalf("self-disable status=%d body=%s", self.Code, self.Body.String())
	}
	var selfError ErrorResponse
	if err = json.Unmarshal(self.Body.Bytes(), &selfError); err != nil || selfError.Code != ErrorCodeAdministratorSelfDisableForbidden {
		t.Fatalf("self-disable envelope=%+v err=%v", selfError, err)
	}

	legal := doDisable(t, targetID)
	if legal.Code != http.StatusOK {
		t.Fatalf("runtime legal disable status=%d body=%s", legal.Code, legal.Body.String())
	}
	if legal.Header().Get("Cache-Control") != "no-store" || legal.Header().Get("X-Request-ID") == "" {
		t.Fatalf("runtime legal disable headers=%v", legal.Header())
	}
	var status string
	var revoked bool
	var reason string
	if err = owner.QueryRow(ctx, `SELECT status FROM control_admin_users WHERE admin_id=$1`, targetID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if err = owner.QueryRow(ctx, `SELECT revoked_at IS NOT NULL, revoke_reason FROM control_admin_sessions WHERE session_id=$1`, targetSessionID).Scan(&revoked, &reason); err != nil {
		t.Fatal(err)
	}
	if status != "disabled" || !revoked || reason != string(authn.SessionRevokeAdministrator) {
		t.Fatalf("disable state status=%q revoked=%v reason=%q", status, revoked, reason)
	}

	var guardSelect, guardUpdate bool
	if err = runtime.QueryRow(ctx, `SELECT
		has_table_privilege(current_user,'control_admin_safety_guard','SELECT'),
		has_table_privilege(current_user,'control_admin_safety_guard','UPDATE')`).Scan(&guardSelect, &guardUpdate); err != nil {
		t.Fatal(err)
	}
	if guardSelect || guardUpdate {
		t.Fatalf("runtime role can access safety guard: select=%v update=%v", guardSelect, guardUpdate)
	}
	if _, err = runtime.Exec(ctx, `SELECT singleton_id FROM control_admin_safety_guard`); err == nil {
		t.Fatal("runtime role directly selected the safety guard")
	} else {
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Fatalf("guard permission error=%v", err)
		}
	}
	if _, err = runtime.Exec(ctx, `UPDATE control_admin_users SET status='disabled', disabled_at=CURRENT_TIMESTAMP WHERE admin_id=$1`, actorID); err == nil {
		t.Fatal("runtime role disabled the last enabled administrator")
	} else {
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
			t.Fatalf("last-administrator error=%v", err)
		}
	}
	if err = owner.QueryRow(ctx, `SELECT status FROM control_admin_users WHERE admin_id=$1`, actorID).Scan(&status); err != nil || status != "enabled" {
		t.Fatalf("last administrator state=%q err=%v", status, err)
	}
}

func TestDisableAdministratorRejectsMissingOrShortReasonWithoutDatabaseWrites(t *testing.T) {
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

	config := testValidatedConfig(t, authn.EnvironmentDev, false)
	actorID, targetID := uuid.New(), uuid.New()
	for id, login := range map[uuid.UUID]string{
		actorID:  "reason_actor_" + actorID.String()[:8],
		targetID: "reason_target_" + targetID.String()[:8],
	} {
		if _, err = owner.Exec(ctx, `INSERT INTO control_admin_users
			(admin_id,login_name,display_name,status,activated_at)
			VALUES ($1,$2,$3,'enabled',CURRENT_TIMESTAMP)`, id, login, "Reason Validation Operator"); err != nil {
			t.Fatal(err)
		}
	}
	actorToken, csrfToken := "reason-actor-"+uuid.NewString(), "reason-csrf-"+uuid.NewString()
	targetToken, targetCSRF := "reason-target-"+uuid.NewString(), "reason-target-csrf-"+uuid.NewString()
	actorDigest, err := authn.ComputeDigest(config.Keyring, authn.DomainSessionDigest, actorToken)
	if err != nil {
		t.Fatal(err)
	}
	csrfDigest, err := authn.ComputeDigest(config.Keyring, authn.DomainCSRFDigest, csrfToken)
	if err != nil {
		t.Fatal(err)
	}
	targetDigest, err := authn.ComputeDigest(config.Keyring, authn.DomainSessionDigest, targetToken)
	if err != nil {
		t.Fatal(err)
	}
	targetCSRFDigest, err := authn.ComputeDigest(config.Keyring, authn.DomainCSRFDigest, targetCSRF)
	if err != nil {
		t.Fatal(err)
	}
	actorSessionID, targetSessionID := uuid.New(), uuid.New()
	if _, err = owner.Exec(ctx, `INSERT INTO control_admin_sessions
		(session_id,admin_id,token_digest,csrf_digest,key_version,mfa_method,absolute_expires_at,reauthenticated_at)
		VALUES ($1,$2,$3,$4,$5,'none',CURRENT_TIMESTAMP+interval '12 hours',CURRENT_TIMESTAMP)`,
		actorSessionID, actorID, actorDigest.Sum[:], csrfDigest.Sum[:], int32(actorDigest.KeyVersion)); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO control_admin_sessions
		(session_id,admin_id,token_digest,csrf_digest,key_version,mfa_method,absolute_expires_at)
		VALUES ($1,$2,$3,$4,$5,'none',CURRENT_TIMESTAMP+interval '12 hours')`,
		targetSessionID, targetID, targetDigest.Sum[:], targetCSRFDigest.Sum[:], int32(targetDigest.KeyVersion)); err != nil {
		t.Fatal(err)
	}
	service, err := authn.NewService(runtime, config)
	if err != nil {
		t.Fatal(err)
	}
	server := NewAuthenticatedServer("test", service)
	handler := HandlerWithOptions(server, ChiServerOptions{ErrorHandlerFunc: server.PrepareGeneratedError})

	var targetUpdatedBefore string
	var targetRevokedBefore bool
	if err = owner.QueryRow(ctx, `SELECT updated_at::text FROM control_admin_users WHERE admin_id=$1`, targetID).Scan(&targetUpdatedBefore); err != nil {
		t.Fatal(err)
	}
	if err = owner.QueryRow(ctx, `SELECT revoked_at IS NOT NULL FROM control_admin_sessions WHERE session_id=$1`, targetSessionID).Scan(&targetRevokedBefore); err != nil {
		t.Fatal(err)
	}
	if targetRevokedBefore {
		t.Fatal("target fixture session starts revoked")
	}

	requestIDs := make([]string, 0, 2)
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "missing", body: `{}`},
		{name: "short", body: `{"reason":"short"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/admins/"+targetID.String()+"/disable", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-CSRF-Token", csrfToken)
			request.AddCookie(&http.Cookie{Name: authn.DevSessionCookieName, Value: actorToken})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Request-ID") == "" {
				t.Fatalf("security headers=%v", response.Header())
			}
			var envelope ErrorResponse
			if err = json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("decode envelope: %v", err)
			}
			if envelope.Code != ErrorCodeValidationFailed || envelope.RequestId != response.Header().Get("X-Request-ID") {
				t.Fatalf("envelope=%+v headers=%v", envelope, response.Header())
			}
			requestIDs = append(requestIDs, envelope.RequestId)
		})
	}

	var targetStatus, targetUpdatedAfter string
	var targetRevokedAfter bool
	if err = owner.QueryRow(ctx, `SELECT status,updated_at::text FROM control_admin_users WHERE admin_id=$1`, targetID).Scan(&targetStatus, &targetUpdatedAfter); err != nil {
		t.Fatal(err)
	}
	if err = owner.QueryRow(ctx, `SELECT revoked_at IS NOT NULL FROM control_admin_sessions WHERE session_id=$1`, targetSessionID).Scan(&targetRevokedAfter); err != nil {
		t.Fatal(err)
	}
	if targetStatus != "enabled" || targetUpdatedAfter != targetUpdatedBefore || targetRevokedAfter != targetRevokedBefore {
		t.Fatalf("target mutated: status=%q updated=%q/%q revoked=%v/%v", targetStatus, targetUpdatedBefore, targetUpdatedAfter, targetRevokedBefore, targetRevokedAfter)
	}
	var successAudits, requestAudits int
	if err = owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs
		WHERE action='administrator.disable' AND result='success' AND target_admin_id=$1`, targetID).Scan(&successAudits); err != nil {
		t.Fatal(err)
	}
	if err = owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE request_id=ANY($1)`, requestIDs).Scan(&requestAudits); err != nil {
		t.Fatal(err)
	}
	if successAudits != 0 || requestAudits != 0 {
		t.Fatalf("invalid reason wrote audit rows: success=%d request_ids=%d", successAudits, requestAudits)
	}
}

func authenticatedTestServer(t *testing.T) (*Server, func()) {
	t.Helper()
	databaseURL := os.Getenv("CONTROL_RUNTIME_DATABASE_TEST_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("CONTROL_DATABASE_TEST_URL")
	}
	if databaseURL == "" {
		t.Skip("set CONTROL_RUNTIME_DATABASE_TEST_URL or CONTROL_DATABASE_TEST_URL")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	service, err := authn.NewService(pool, testValidatedConfig(t, authn.EnvironmentDev, false))
	if err != nil {
		pool.Close()
		t.Fatal(err)
	}
	return NewAuthenticatedServer("test", service), pool.Close
}

func testValidatedConfig(t *testing.T, environment authn.Environment, secure bool) *authn.ValidatedConfig {
	t.Helper()
	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index + 1)
	}
	document := fmt.Sprintf(`{"format_version":1,"environment":%q,"current":1,"keys":[{"version":1,"key":%q}]}`, environment, base64.RawStdEncoding.EncodeToString(key))
	keyring, err := authn.ParseKeyring([]byte(document), environment)
	if err != nil {
		t.Fatal(err)
	}
	return &authn.ValidatedConfig{Config: authn.Config{Environment: environment, BindAddress: "127.0.0.1:8080", CookieSecure: secure}, Keyring: keyring}
}
