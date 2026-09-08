package api

import (
	"context"
	"encoding/base64"
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
	store "github.com/sunxu/relay-station-control/internal/store"
)

type requestHistoryHTTPReader struct {
	page  store.AccountRequestHistoryPage
	err   error
	calls int
	query store.AccountRequestHistoryQuery
}

func (r *requestHistoryHTTPReader) ListAccountRequestHistory(_ context.Context, q store.AccountRequestHistoryQuery) (store.AccountRequestHistoryPage, error) {
	r.calls++
	r.query = q
	return r.page, r.err
}

func TestAccountRequestHistoryHTTPReadContracts(t *testing.T) {
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
	cfg := testValidatedConfig(t, authn.EnvironmentDev, false)
	service, err := authn.NewService(runtime, cfg)
	if err != nil {
		t.Fatal(err)
	}
	node, admin := uuid.New(), uuid.New()
	token := "history-session-" + uuid.NewString()
	digest, err := authn.ComputeDigest(cfg.Keyring, authn.DomainSessionDigest, token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at) VALUES($1,$2,'History reader','enabled',CURRENT_TIMESTAMP)`, admin, "history_"+admin.String()[:8]); err != nil {
		t.Fatal(err)
	}
	csrf, err := authn.ComputeDigest(cfg.Keyring, authn.DomainCSRFDigest, "history-csrf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO control_admin_sessions(session_id,admin_id,token_digest,csrf_digest,key_version,mfa_method,absolute_expires_at) VALUES($1,$2,$3,$4,$5,'none',CURRENT_TIMESTAMP+interval '12 hours')`, uuid.New(), admin, digest.Sum[:], csrf.Sum[:], int32(digest.KeyVersion)); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 8, 1, 2, 3, 123456000, time.UTC)
	duration := int64(321)
	failure := "upstream"
	reader := &requestHistoryHTTPReader{page: store.AccountRequestHistoryPage{Items: []store.AccountRequestHistoryItem{{EventHash: strings.Repeat("a", 64), RequestID: "req-1", Model: "gpt-test", OccurredAt: at, DurationMS: &duration, Success: false, FailureClass: &failure}}, HasMore: true}}
	server := NewAuthenticatedServer("test", service)
	server.SetAccountRequestHistoryReader(reader)
	handler := HandlerWithOptions(server, ChiServerOptions{ErrorHandlerFunc: server.PrepareGeneratedError})
	path := "/api/topology/nodes/" + node.String() + "/request-history"
	account := "openai:alice@example.invalid"
	do := func(method, target, cookie string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, nil)
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: authn.DevSessionCookieName, Value: cookie})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if method == http.MethodGet && (w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Request-ID") == "") {
			t.Fatalf("headers=%v", w.Header())
		}
		return w
	}
	t.Run("success six fields and cursor", func(t *testing.T) {
		w := do(http.MethodGet, path+"?account_key="+account+"&limit=25", token)
		if w.Code != 200 || reader.query.AccountKey != account || reader.query.Limit != 25 {
			t.Fatalf("status/query=%d/%+v", w.Code, reader.query)
		}
		var got NodeAccountRequestHistoryResponse
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Items) != 1 || got.Items[0].RequestId != "req-1" || got.Items[0].OccurredAt.UnixNano() != at.UnixNano() || got.Items[0].DurationMs == nil || *got.Items[0].DurationMs != duration || got.Items[0].FailureClass == nil || strings.Contains(w.Body.String(), "event_hash") {
			t.Fatalf("response=%s", w.Body.String())
		}
		var envelope struct {
			Items []map[string]json.RawMessage `json:"items"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if len(envelope.Items) != 1 {
			t.Fatalf("items=%d", len(envelope.Items))
		}
		for _, key := range []string{"occurred_at", "model", "success", "failure_class", "duration_ms", "request_id"} {
			if _, ok := envelope.Items[0][key]; !ok {
				t.Fatalf("missing field %s", key)
			}
		}
		if len(envelope.Items[0]) != 6 {
			t.Fatalf("item fields=%v", envelope.Items[0])
		}
		if got.NextCursor == nil {
			t.Fatal("missing cursor")
		}
		if w = do(http.MethodGet, path+"?account_key="+account+"&cursor="+*got.NextCursor, token); w.Code != 200 || reader.query.AfterEventHash != strings.Repeat("a", 64) || reader.query.AfterOccurredAt == nil || !reader.query.AfterOccurredAt.Equal(at) {
			t.Fatalf("cursor=%d query=%+v", w.Code, reader.query)
		}
		decoded, err := base64.RawURLEncoding.DecodeString(*got.NextCursor)
		if err != nil {
			t.Fatal(err)
		}
		if w = do(http.MethodGet, path+"?account_key="+account+"&limit=100", token); w.Code != 200 || reader.query.Limit != 100 {
			t.Fatalf("limit=100 status/query=%d/%+v", w.Code, reader.query)
		}
		var cursor nodeAccountRequestHistoryCursor
		if err := json.Unmarshal(decoded, &cursor); err != nil {
			t.Fatal(err)
		}
		for name, mutate := range map[string]func(*nodeAccountRequestHistoryCursor){
			"node":     func(c *nodeAccountRequestHistoryCursor) { c.InstanceID = uuid.New() },
			"account":  func(c *nodeAccountRequestHistoryCursor) { c.AccountKey = "openai:bob@example.invalid" },
			"time":     func(c *nodeAccountRequestHistoryCursor) { c.OccurredAt = time.Time{} },
			"hash":     func(c *nodeAccountRequestHistoryCursor) { c.EventHash = "" },
			"hash-nul": func(c *nodeAccountRequestHistoryCursor) { c.EventHash = "hash\x00bad" },
		} {
			copyCursor := cursor
			mutate(&copyCursor)
			raw, _ := json.Marshal(copyCursor)
			encoded := base64.RawURLEncoding.EncodeToString(raw)
			if w := do(http.MethodGet, path+"?account_key="+account+"&cursor="+encoded, token); w.Code != 400 {
				t.Fatalf("%s cursor status=%d", name, w.Code)
			}
		}
	})
	t.Run("validation and auth do not read", func(t *testing.T) {
		before := reader.calls
		for _, target := range []string{path + "?account_key=bad", path + "?account_key=" + account + "&limit=0", path + "?account_key=" + account + "&limit=-1", path + "?account_key=" + account + "&limit=101", path + "?account_key=" + account + "&limit=abc", path + "?account_key=" + account + "&cursor=x", path + "?account_key=" + account + "&cursor=" + strings.Repeat("x", 8193), path + "?account_key=" + account + "%00bad"} {
			if w := do(http.MethodGet, target, token); w.Code != 400 {
				t.Fatalf("%s => %d", target, w.Code)
			}
		}
		if w := do(http.MethodGet, path+"?account_key="+account, ""); w.Code != 401 {
			t.Fatalf("unauth=%d", w.Code)
		}
		if w := do(http.MethodPost, path+"?account_key="+account, token); w.Code != 405 {
			t.Fatalf("post=%d", w.Code)
		}
		if reader.calls != before {
			t.Fatalf("calls=%d before=%d", reader.calls, before)
		}
	})
	t.Run("empty and nullable fields remain explicit", func(t *testing.T) {
		reader.page = store.AccountRequestHistoryPage{Items: []store.AccountRequestHistoryItem{{EventHash: "b", Model: "gpt", OccurredAt: at, Success: true}}}
		w := do(http.MethodGet, path+"?account_key="+account, token)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"duration_ms":null`) || !strings.Contains(w.Body.String(), `"failure_class":null`) {
			t.Fatalf("nullable=%d %s", w.Code, w.Body.String())
		}
		reader.page = store.AccountRequestHistoryPage{}
		w = do(http.MethodGet, path+"?account_key="+account, token)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"items":[]`) || strings.Contains(w.Body.String(), `"event_hash"`) {
			t.Fatalf("empty=%d %s", w.Code, w.Body.String())
		}
	})
	t.Run("failure notfound and recovery", func(t *testing.T) {
		reader.err = errors.New("db")
		if w := do(http.MethodGet, path+"?account_key="+account, token); w.Code != 503 {
			t.Fatalf("db=%d", w.Code)
		}
		reader.err = store.ErrAccountInventoryInstanceNotFound
		if w := do(http.MethodGet, path+"?account_key="+account, token); w.Code != 404 {
			t.Fatalf("notfound=%d", w.Code)
		}
		reader.err = nil
		if w := do(http.MethodGet, path+"?account_key="+account, token); w.Code != 200 {
			t.Fatalf("recovery=%d", w.Code)
		}
	})
	t.Run("non super admin forbidden without reading", func(t *testing.T) {
		if _, err := owner.Exec(ctx, `ALTER TABLE control_admin_users DROP CONSTRAINT control_admin_users_role_fixed; UPDATE control_admin_users SET role='observer'`); err != nil {
			t.Fatal(err)
		}
		before := reader.calls
		if w := do(http.MethodGet, path+"?account_key="+account, token); w.Code != 403 {
			t.Fatalf("observer status=%d body=%s", w.Code, w.Body)
		}
		if reader.calls != before {
			t.Fatal("forbidden request read history")
		}
	})
}
