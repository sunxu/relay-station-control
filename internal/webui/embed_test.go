package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-chi/chi/v5"
)

func TestHandlerStaticAndSPARouting(t *testing.T) {
	dist := fstest.MapFS{
		"index.html":     &fstest.MapFile{Data: []byte("<main>spa-shell</main>")},
		"assets/app.js":  &fstest.MapFile{Data: []byte("static-app")},
		"assets/lazy.js": &fstest.MapFile{Data: []byte("lazy-chunk")},
	}
	web := handler(dist)

	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantBody   string
		forbidBody string
	}{
		{name: "static hit", path: "/static/assets/app.js", wantStatus: http.StatusOK, wantBody: "static-app", forbidBody: "spa-shell"},
		{name: "lazy chunk hit", path: "/static/assets/lazy.js", wantStatus: http.StatusOK, wantBody: "lazy-chunk", forbidBody: "spa-shell"},
		{name: "static miss", path: "/static/missing.js", wantStatus: http.StatusNotFound, forbidBody: "spa-shell"},
		{name: "static root is not SPA", path: "/static/", wantStatus: http.StatusNotFound, forbidBody: "spa-shell"},
		{name: "assets", path: "/assets", wantStatus: http.StatusOK, wantBody: "spa-shell"},
		{name: "assets trailing slash", path: "/assets/", wantStatus: http.StatusOK, wantBody: "spa-shell"},
		{name: "existing deep link", path: "/jobs", wantStatus: http.StatusOK, wantBody: "spa-shell"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			web.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%q", response.Code, test.wantStatus, response.Body.String())
			}
			if test.wantBody != "" && !strings.Contains(response.Body.String(), test.wantBody) {
				t.Fatalf("body = %q, want %q", response.Body.String(), test.wantBody)
			}
			if test.forbidBody != "" && strings.Contains(response.Body.String(), test.forbidBody) {
				t.Fatalf("body = %q, must not contain %q", response.Body.String(), test.forbidBody)
			}
		})
	}
}

func TestExistingAPIRoutePrecedesSPAFallback(t *testing.T) {
	web := handler(fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<main>spa-shell</main>")},
	})
	router := chi.NewRouter()
	router.Get("/api/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	router.NotFound(web.ServeHTTP)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/healthz", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"ok"`) {
		t.Fatalf("API response = %d %q", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "spa-shell") {
		t.Fatalf("API response fell through to SPA: %q", response.Body.String())
	}
}

func TestHandlerRequiresSPAShell(t *testing.T) {
	web := handler(fstest.MapFS{})
	response := httptest.NewRecorder()
	web.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/assets", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
}
