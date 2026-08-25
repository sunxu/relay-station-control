package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	authn "github.com/sunxu/relay-station-control/internal/auth"
)

func TestProductionSessionCookieIsNonPersistentAndHostBound(t *testing.T) {
	cookie := sessionCookie(authn.SessionCookieName, "opaque", true)
	if cookie.Name != "__Host-relay_control_session" {
		t.Fatalf("cookie name = %q", cookie.Name)
	}
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("unsafe cookie flags: %+v", cookie)
	}
	if cookie.Path != "/" || cookie.Domain != "" {
		t.Fatalf("cookie scope = path %q domain %q", cookie.Path, cookie.Domain)
	}
	if cookie.MaxAge != 0 || !cookie.Expires.IsZero() {
		t.Fatalf("session cookie became persistent: %+v", cookie)
	}
}

func TestAuthenticationDelayStopsWhenRequestIsCanceled(t *testing.T) {
	server := NewServer("test")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	server.authenticationDelay(ctx, start)
	if elapsed := time.Since(start); elapsed >= 100*time.Millisecond {
		t.Fatalf("canceled response delay took %s", elapsed)
	}
}

func TestPublicAuthenticationResponseHasMinimumBudget(t *testing.T) {
	server := NewServer("test")
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	response := httptest.NewRecorder()
	start := time.Now()
	server.Login(response, request)
	if elapsed := time.Since(start); elapsed < authenticationResponseFloor {
		t.Fatalf("public authentication response completed in %s, want at least %s", elapsed, authenticationResponseFloor)
	}
}

func TestRequestIDRemainsStableForEntireRequest(t *testing.T) {
	server := NewServer("test")
	request := httptest.NewRequest(http.MethodGet, "/api/auth/session", nil)
	response := httptest.NewRecorder()
	server.prepare(response, request, true)
	responseID := response.Header().Get("X-Request-ID")
	if responseID == "" {
		t.Fatal("response request ID is empty")
	}
	if metaID := server.requestID(request); metaID != responseID {
		t.Fatalf("request IDs differ: response=%q subsequent=%q", responseID, metaID)
	}
}

func TestGeneratedMissingCSRFUsesUnifiedForbiddenEnvelope(t *testing.T) {
	server := NewServer("test")
	request := httptest.NewRequest(http.MethodPost, "/api/admins", nil)
	response := httptest.NewRecorder()
	server.PrepareGeneratedError(response, request, &RequiredHeaderError{ParamName: "X-CSRF-Token"})
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d", response.Code)
	}
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("security headers=%v", response.Header())
	}
	var envelope ErrorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.Code != ErrorCodeCsrfInvalid || envelope.RequestId == "" {
		t.Fatalf("envelope=%+v", envelope)
	}
}

func TestChallengeCookieUsesSecurePrefixAndNarrowPath(t *testing.T) {
	if authn.ChallengeCookieName != "__Secure-relay_control_mfa" {
		t.Fatalf("challenge cookie name = %q", authn.ChallengeCookieName)
	}
	cookie := &http.Cookie{Name: authn.ChallengeCookieName, Value: "opaque", Path: "/api/auth", MaxAge: 300, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode}
	if cookie.Path != "/api/auth" || cookie.MaxAge != 300 || !cookie.Secure || !cookie.HttpOnly {
		t.Fatalf("challenge cookie flags: %+v", cookie)
	}
}
