package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	authn "github.com/sunxu/relay-station-control/internal/auth"
	"github.com/sunxu/relay-station-control/internal/requestquality"
	store "github.com/sunxu/relay-station-control/internal/store"
)

type accountQualityHTTPReader struct {
	page  store.AccountQualityPage
	err   error
	calls int
	query store.AccountQualityQuery
}

func (r *accountQualityHTTPReader) ListAccountQuality(ctx context.Context, query store.AccountQualityQuery) (store.AccountQualityPage, error) {
	r.calls++
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 5*time.Second {
		return store.AccountQualityPage{}, errors.New("missing bounded timeout")
	}
	r.query = query
	return r.page, r.err
}

func TestAccountQualityHTTPReadContracts(t *testing.T) {
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
	node := uuid.New()
	admin := uuid.New()
	token := "quality-session-" + uuid.NewString()
	digest, err := authn.ComputeDigest(config.Keyring, authn.DomainSessionDigest, token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at) VALUES($1,$2,'Quality reader','enabled',CURRENT_TIMESTAMP)`, admin, "quality_"+admin.String()[:8]); err != nil {
		t.Fatal(err)
	}
	csrf, err := authn.ComputeDigest(config.Keyring, authn.DomainCSRFDigest, "quality-csrf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO control_admin_sessions(session_id,admin_id,token_digest,csrf_digest,key_version,mfa_method,absolute_expires_at) VALUES($1,$2,$3,$4,$5,'none',CURRENT_TIMESTAMP+interval '12 hours')`, uuid.New(), admin, digest.Sum[:], csrf.Sum[:], int32(digest.KeyVersion)); err != nil {
		t.Fatal(err)
	}
	rate, p95 := 0.987654321, 12345.125
	failure := "upstream"
	now := time.Date(2026, 9, 8, 1, 2, 3, 0, time.UTC)
	reader := &accountQualityHTTPReader{page: store.AccountQualityPage{Items: []store.AccountQualityItem{{AccountKey: "openai:alice@example.invalid", Email: "alice@example.invalid", Provider: "openai", Quality: "good", Stats: requestQualityStats(10, 9, 1, &rate, &p95, &now, &now, &failure)}}, HasMore: true}}
	server := NewAuthenticatedServer("test", service)
	server.SetAccountQualityReader(reader)
	handler := HandlerWithOptions(server, ChiServerOptions{ErrorHandlerFunc: server.PrepareGeneratedError})
	path := "/api/topology/nodes/" + node.String() + "/account-quality"
	do := func(method, target, cookie string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, nil)
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: authn.DevSessionCookieName, Value: cookie})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if method == http.MethodGet && (w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Request-ID") == "") {
			t.Fatalf("missing sensitive headers: %v", w.Header())
		}
		return w
	}

	t.Run("default projection and cursor", func(t *testing.T) {
		w := do(http.MethodGet, path, token)
		if w.Code != http.StatusOK || reader.query.Window != 15*time.Minute || reader.query.Limit != 25 || reader.query.Provider != "" || reader.query.Quality != "" || reader.query.Lifecycle != "" {
			t.Fatalf("status/query=%d/%+v body=%s", w.Code, reader.query, w.Body.String())
		}
		var got NodeAccountQualityResponse
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Items[0].SuccessRate == nil || *got.Items[0].SuccessRate != rate {
			t.Fatal("rate precision lost")
		}
		if got.Window != "15m" || len(got.Items) != 1 || got.NextCursor == nil || got.Items[0].Email != "alice@example.invalid" || got.Items[0].P95LatencyMs == nil || *got.Items[0].P95LatencyMs != p95 {
			t.Fatalf("projection=%+v", got)
		}
	})

	t.Run("cursor is bound to filters", func(t *testing.T) {
		w := do(http.MethodGet, path+"?window=1h&provider=openai&quality=good&limit=1", token)
		var got NodeAccountQualityResponse
		_ = json.Unmarshal(w.Body.Bytes(), &got)
		if w.Code != 200 || got.NextCursor == nil || reader.query.Window != time.Hour || reader.query.Provider != "openai" || reader.query.Quality != "good" || reader.query.Limit != 1 {
			t.Fatalf("query=%+v response=%s", reader.query, w.Body.String())
		}
		if w = do(http.MethodGet, path+"?window=1h&provider=other&quality=good&cursor="+*got.NextCursor, token); w.Code != 400 {
			t.Fatalf("provider mismatch=%d", w.Code)
		}
		if w = do(http.MethodGet, path+"?window=15m&cursor="+*got.NextCursor, token); w.Code != 400 {
			t.Fatalf("window mismatch=%d", w.Code)
		}
		otherNodePath := "/api/topology/nodes/" + uuid.NewString() + "/account-quality"
		if w = do(http.MethodGet, otherNodePath+"?window=1h&provider=openai&quality=good&cursor="+*got.NextCursor, token); w.Code != 400 {
			t.Fatal("node cursor mismatch accepted")
		}
		if w = do(http.MethodGet, path+"?window=1h&provider=openai&quality=bad&cursor="+*got.NextCursor, token); w.Code != 400 {
			t.Fatal("quality cursor mismatch accepted")
		}
		if w = do(http.MethodGet, path+"?window=1h&provider=openai&quality=good&cursor="+*got.NextCursor, token); w.Code != 200 || reader.query.AfterAccountKey != "openai:alice@example.invalid" {
			t.Fatalf("cursor roundtrip=%d query=%+v", w.Code, reader.query)
		}
	})

	t.Run("lifecycle filter and cursor compatibility", func(t *testing.T) {
		for _, lifecycle := range []string{"present", "missing", "suspected_missing", "out_of_scope"} {
			w := do(http.MethodGet, path+"?lifecycle="+lifecycle, token)
			var got NodeAccountQualityResponse
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 200 || got.NextCursor == nil || reader.query.Lifecycle != lifecycle {
				t.Fatalf("lifecycle projection status=%d", w.Code)
			}
			before := reader.calls
			other := "present"
			if lifecycle == other {
				other = "missing"
			}
			for _, filter := range []string{"", other} {
				target := path + "?cursor=" + *got.NextCursor
				if filter != "" {
					target += "&lifecycle=" + filter
				}
				if w := do(http.MethodGet, target, token); w.Code != 400 {
					t.Fatalf("mismatch status=%d", w.Code)
				}
			}
			if reader.calls != before {
				t.Fatal("mismatch reached reader")
			}
			if w := do(http.MethodGet, path+"?lifecycle="+lifecycle+"&cursor="+*got.NextCursor, token); w.Code != 200 || reader.query.AfterAccountKey != "openai:alice@example.invalid" {
				t.Fatal("lifecycle cursor roundtrip failed")
			}
		}
		w := do(http.MethodGet, path, token)
		var got NodeAccountQualityResponse
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.NextCursor == nil {
			t.Fatal("missing all cursor")
		}
		if w := do(http.MethodGet, path+"?cursor="+*got.NextCursor, token); w.Code != 200 || reader.query.Lifecycle != "" {
			t.Fatal("old all cursor incompatible")
		}
		before := reader.calls
		if w := do(http.MethodGet, path+"?lifecycle=present&cursor="+*got.NextCursor, token); w.Code != 400 || reader.calls != before {
			t.Fatal("all cursor accepted for present")
		}
	})

	t.Run("invalid and authorization requests do not read", func(t *testing.T) {
		before := reader.calls
		for _, target := range []string{path + "?lifecycle=invalid", path + "?lifecycle=", path + "?window=2h", path + "?quality=invalid", path + "?provider=OpenAI", path + "?limit=101", path + "?cursor=" + strings.Repeat("x", 2049)} {
			if w := do(http.MethodGet, target, token); w.Code != 400 {
				t.Fatalf("target=%s status=%d", target, w.Code)
			}
		}
		if w := do(http.MethodGet, path, ""); w.Code != 401 {
			t.Fatalf("unauth=%d", w.Code)
		}
		if w := do(http.MethodPost, path, token); w.Code != 405 {
			t.Fatalf("post=%d", w.Code)
		}
		if reader.calls != before {
			t.Fatalf("reader calls=%d before=%d", reader.calls, before)
		}
	})

	t.Run("failure and not found are not empty success", func(t *testing.T) {
		reader.err = errors.New("database unavailable")
		if w := do(http.MethodGet, path, token); w.Code != 503 {
			t.Fatalf("error=%d", w.Code)
		}
		reader.err = store.ErrAccountInventoryInstanceNotFound
		if w := do(http.MethodGet, path, token); w.Code != 404 {
			t.Fatalf("notfound=%d", w.Code)
		}
		reader.err = store.ErrAccountInventoryCapabilityUnsupported
		if w := do(http.MethodGet, path, token); w.Code != 503 {
			t.Fatalf("unsupported=%d", w.Code)
		}
		reader.err = nil
		if w := do(http.MethodGet, path, token); w.Code != 200 {
			t.Fatalf("recovery=%d", w.Code)
		}
	})
	t.Run("zero requests serialize explicit nulls and nil reader is unavailable", func(t *testing.T) {
		reader.page = store.AccountQualityPage{Items: []store.AccountQualityItem{{AccountKey: "openai:unknown@example.invalid", Email: "unknown@example.invalid", Provider: "openai", Quality: "unknown"}}}
		w := do(http.MethodGet, path, token)
		var body struct {
			Items []map[string]json.RawMessage `json:"items"`
			Next  json.RawMessage              `json:"next_cursor"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != 200 || len(body.Items) != 1 {
			t.Fatalf("unknown projection %s", w.Body)
		}
		for _, field := range []string{"success_rate", "p95_latency_ms", "last_success_at", "last_failure_at", "last_failure_class"} {
			if string(body.Items[0][field]) != "null" {
				t.Fatalf("%s not explicit null", field)
			}
		}
		for _, field := range []string{"request_count", "success_count", "failure_count"} {
			if string(body.Items[0][field]) != "0" {
				t.Fatalf("%s not zero", field)
			}
		}
		if string(body.Items[0]["quality"]) != `"unknown"` || string(body.Next) != "null" {
			t.Fatal("unknown semantics")
		}
		server.SetAccountQualityReader(nil)
		if w := do(http.MethodGet, path, token); w.Code != 503 {
			t.Fatalf("nil reader=%d", w.Code)
		}
		server.SetAccountQualityReader(reader)
	})
	t.Run("non super admin forbidden without reading", func(t *testing.T) {
		// Only this isolated test database permits the synthetic observer role.
		if _, err := owner.Exec(ctx, `ALTER TABLE control_admin_users DROP CONSTRAINT control_admin_users_role_fixed; UPDATE control_admin_users SET role='observer'`); err != nil {
			t.Fatal(err)
		}
		before := reader.calls
		if w := do(http.MethodGet, path, token); w.Code != 403 {
			t.Fatalf("observer status=%d body=%s", w.Code, w.Body)
		}
		if reader.calls != before {
			t.Fatal("forbidden request read accounts")
		}
	})
}

func requestQualityStats(requests, success, failures int64, rate, p95 *float64, successAt, failureAt *time.Time, failureClass *string) (q requestquality.Quality) {
	return requestquality.Quality{RequestCount: requests, SuccessCount: success, FailureCount: failures, SuccessRate: rate, P95LatencyMS: p95, LastSuccessAt: successAt, LastFailureAt: failureAt, LastFailureClass: failureClass}
}
