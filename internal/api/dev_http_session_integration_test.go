package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	authn "github.com/sunxu/relay-station-control/internal/auth"
)

func TestDevHTTPLoginSessionAndCSRF(t *testing.T) {
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
	id := uuid.New()
	password := "local http integration test password"
	hash, err := authn.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at) VALUES($1,'http_test_admin','HTTP test','enabled',CURRENT_TIMESTAMP)`, id); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO control_admin_passwords(admin_id,password_phc,parameter_version) VALUES($1,$2,1)`, id, hash); err != nil {
		t.Fatal(err)
	}
	service, err := authn.NewService(runtime, testValidatedConfig(t, authn.EnvironmentDev))
	if err != nil {
		t.Fatal(err)
	}
	server := NewAuthenticatedServer("test", service)
	handler := HandlerWithOptions(server, ChiServerOptions{ErrorHandlerFunc: server.PrepareGeneratedError})
	do := func(method, path, body, origin, csrf string, cookie *http.Cookie) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, "http://127.0.0.1:18080"+path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", origin)
		request.Header.Set("X-CSRF-Token", csrf)
		if cookie != nil {
			request.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	origin := "http://127.0.0.1:18080"
	login := do(http.MethodPost, "/api/auth/login", `{"login_name":"http_test_admin","password":"`+password+`"}`, origin, "", nil)
	if login.Code != 200 {
		t.Fatalf("login status=%d", login.Code)
	}
	cookies := login.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != authn.DevSessionCookieName || cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("invalid HTTP session cookie")
	}
	var session SessionResponse
	if err = json.Unmarshal(login.Body.Bytes(), &session); err != nil || session.CsrfToken == nil {
		t.Fatal("missing session CSRF")
	}
	cookie := cookies[0]
	refreshed := do(http.MethodGet, "/api/auth/session", "", origin, "", cookie)
	if refreshed.Code != 200 {
		t.Fatalf("session=%d", refreshed.Code)
	}
	// Session reads rotate CSRF; use the newly returned proof, as the Web client does.
	if err = json.Unmarshal(refreshed.Body.Bytes(), &session); err != nil || session.CsrfToken == nil {
		t.Fatal("missing refreshed CSRF")
	}
	for _, bad := range []struct{ origin, csrf string }{{"http://other.invalid", *session.CsrfToken}, {"https://127.0.0.1:18080", *session.CsrfToken}, {origin, "invalid"}, {origin, ""}} {
		if w := do(http.MethodPost, "/api/auth/logout", "", bad.origin, bad.csrf, cookie); w.Code != 403 {
			t.Fatalf("CSRF rejection=%d", w.Code)
		}
	}
	if w := do(http.MethodPost, "/api/auth/logout", "", origin, *session.CsrfToken, cookie); w.Code != 204 {
		t.Fatalf("logout=%d", w.Code)
	}
	if w := do(http.MethodGet, "/api/auth/session", "", origin, "", cookie); w.Code != 401 {
		t.Fatalf("revoked session=%d", w.Code)
	}
}
