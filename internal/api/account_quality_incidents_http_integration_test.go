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

type accountQualityIncidentsHTTPReader struct {
	page  store.AccountQualityIncidentPage
	err   error
	calls int
	query store.AccountQualityIncidentQuery
}

func (r *accountQualityIncidentsHTTPReader) ListAccountQualityIncidents(ctx context.Context, query store.AccountQualityIncidentQuery) (store.AccountQualityIncidentPage, error) {
	r.calls++
	if _, ok := ctx.Deadline(); !ok {
		return store.AccountQualityIncidentPage{}, errors.New("missing bounded timeout")
	}
	r.query = query
	return r.page, r.err
}

func TestAccountQualityIncidentsHTTPReadContracts(t *testing.T) {
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
	config := testValidatedConfig(t, authn.EnvironmentDev)
	service, err := authn.NewService(runtime, config)
	if err != nil {
		t.Fatal(err)
	}
	node := uuid.New()
	admin := uuid.New()
	token := "incident-session-" + uuid.NewString()
	digest, err := authn.ComputeDigest(config.Keyring, authn.DomainSessionDigest, token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at) VALUES($1,$2,'Incident reader','enabled',CURRENT_TIMESTAMP)`, admin, "incident_"+admin.String()[:8]); err != nil {
		t.Fatal(err)
	}
	csrf, err := authn.ComputeDigest(config.Keyring, authn.DomainCSRFDigest, "incident-csrf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO control_admin_sessions(session_id,admin_id,token_digest,csrf_digest,key_version,mfa_method,absolute_expires_at) VALUES($1,$2,$3,$4,$5,'none',CURRENT_TIMESTAMP+interval '12 hours')`, uuid.New(), admin, digest.Sum[:], csrf.Sum[:], int32(digest.KeyVersion)); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 8, 1, 2, 3, 123456000, time.UTC)
	reader := &accountQualityIncidentsHTTPReader{page: store.AccountQualityIncidentPage{Items: []store.AccountQualityIncident{{NodeID: node, AccountKey: "openai:alice@example.invalid", Provider: "openai", FailureClass: "auth", Status: "active", FirstSeen: now.Add(-2 * time.Minute), LastSeen: now, HitCount: 3, LastSuccessAt: nil}}, HasMore: true}}
	server := NewAuthenticatedServer("test", service)
	server.SetAccountQualityIncidentsReader(reader)
	handler := HandlerWithOptions(server, ChiServerOptions{ErrorHandlerFunc: server.PrepareGeneratedError})
	path := "/api/topology/nodes/" + node.String() + "/incidents"
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

	t.Run("default active page and exact nullable projection", func(t *testing.T) {
		w := do(http.MethodGet, path, token)
		if w.Code != 200 {
			t.Fatalf("status=%d body=%s", w.Code, w.Body)
		}
		if reader.query.Provider != "" || reader.query.FailureClass != "" || reader.query.Limit != 25 || reader.query.AfterLastSeen != nil {
			t.Fatalf("query=%+v", reader.query)
		}
		var body map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		var item map[string]json.RawMessage
		var items []map[string]json.RawMessage
		_ = json.Unmarshal(body["items"], &items)
		item = items[0]
		for _, key := range []string{"node_id", "account_key", "provider", "failure_class", "status", "first_seen", "last_seen", "hit_count", "last_success_at"} {
			if _, ok := item[key]; !ok {
				t.Fatalf("missing %s", key)
			}
		}
		if len(item) != 9 || string(item["last_success_at"]) != "null" || string(item["account_key"]) != `"openai:alice@example.invalid"` {
			t.Fatalf("projection=%s", w.Body)
		}
		if string(body["next_cursor"]) == "null" {
			t.Fatal("expected cursor")
		}
	})

	t.Run("filters, max limit and cursor binding", func(t *testing.T) {
		w := do(http.MethodGet, path+"?status=active&provider=openai&failure_class=auth&limit=100", token)
		if w.Code != 200 || reader.query.Limit != 100 || reader.query.Provider != "openai" || reader.query.FailureClass != "auth" {
			t.Fatalf("status=%d query=%+v", w.Code, reader.query)
		}
		var body NodeAccountQualityIncidentResponse
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		cursor := *body.NextCursor
		cases := []string{"?status=recovered&cursor=" + cursor, "?status=active&provider=other&failure_class=auth&cursor=" + cursor, "?status=active&provider=openai&failure_class=quota&cursor=" + cursor, "?status=active&provider=openai&failure_class=auth&cursor=" + cursor}
		for i, target := range cases {
			got := do(http.MethodGet, path+target, token)
			want := 400
			if i == 3 {
				want = 200
			}
			if got.Code != want {
				t.Fatalf("case %d status=%d want=%d", i, got.Code, want)
			}
		}
		decoded, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			t.Fatal(err)
		}
		var original nodeAccountQualityIncidentCursor
		if err := json.Unmarshal(decoded, &original); err != nil {
			t.Fatal(err)
		}
		if reader.query.AfterLastSeen == nil || !reader.query.AfterLastSeen.Equal(original.LastSeen) || reader.query.AfterAccountKey != original.AccountKey || reader.query.AfterFailureClass != original.RowClass {
			t.Fatalf("cursor roundtrip query=%+v cursor=%+v", reader.query, original)
		}
		mutations := []struct {
			name   string
			mutate func(*nodeAccountQualityIncidentCursor)
		}{
			{"node", func(c *nodeAccountQualityIncidentCursor) { c.InstanceID = uuid.New() }},
			{"status", func(c *nodeAccountQualityIncidentCursor) { c.Status = "recovered" }},
			{"provider", func(c *nodeAccountQualityIncidentCursor) { c.Provider = "other" }},
			{"failure filter", func(c *nodeAccountQualityIncidentCursor) { c.FailureClass = "quota" }},
			{"last seen", func(c *nodeAccountQualityIncidentCursor) { c.LastSeen = time.Time{} }},
			{"account key", func(c *nodeAccountQualityIncidentCursor) { c.AccountKey = "openai:\x00" }},
			{"row class", func(c *nodeAccountQualityIncidentCursor) { c.RowClass = "quota" }},
		}
		beforeCalls := reader.calls
		for _, mutation := range mutations {
			copy := original
			mutation.mutate(&copy)
			raw, err := json.Marshal(copy)
			if err != nil {
				t.Fatal(err)
			}
			encoded := base64.RawURLEncoding.EncodeToString(raw)
			if got := do(http.MethodGet, path+"?status=active&provider=openai&failure_class=auth&cursor="+encoded, token); got.Code != 400 {
				t.Fatalf("mutated cursor %s status=%d", mutation.name, got.Code)
			}
		}
		if reader.calls != beforeCalls {
			t.Fatalf("invalid cursors reached reader: before=%d after=%d", beforeCalls, reader.calls)
		}
		var nodeMismatch nodeAccountQualityIncidentCursor = original
		nodeMismatch.InstanceID = uuid.New()
		raw, _ := json.Marshal(nodeMismatch)
		if got := do("GET", "/api/topology/nodes/"+uuid.NewString()+"/incidents?status=active&provider=openai&failure_class=auth&cursor="+base64.RawURLEncoding.EncodeToString(raw), token); got.Code != 400 {
			t.Fatalf("path node mismatch=%d", got.Code)
		}
	})

	t.Run("invalid, auth and method do not read", func(t *testing.T) {
		before := reader.calls
		for _, target := range []string{path + "?status=recovered", path + "?failure_class=unknown", path + "?provider=OpenAI", path + "?limit=0", path + "?limit=101", path + "?cursor=" + strings.Repeat("x", 8193)} {
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

	t.Run("failure notfound recovery and empty remain distinct", func(t *testing.T) {
		reader.err = errors.New("database unavailable")
		if w := do(http.MethodGet, path, token); w.Code != 503 {
			t.Fatalf("error=%d", w.Code)
		}
		reader.err = store.ErrAccountInventoryInstanceNotFound
		if w := do(http.MethodGet, path, token); w.Code != 404 {
			t.Fatalf("notfound=%d", w.Code)
		}
		reader.err = nil
		reader.page = store.AccountQualityIncidentPage{}
		if w := do(http.MethodGet, path, token); w.Code != 200 || !strings.Contains(w.Body.String(), `"items":[]`) {
			t.Fatalf("empty=%d body=%s", w.Code, w.Body)
		}
	})

	t.Run("non super admin forbidden without reading", func(t *testing.T) {
		if _, err := owner.Exec(ctx, `ALTER TABLE control_admin_users DROP CONSTRAINT control_admin_users_role_fixed; UPDATE control_admin_users SET role='observer'`); err != nil {
			t.Fatal(err)
		}
		before := reader.calls
		if w := do(http.MethodGet, path, token); w.Code != 403 {
			t.Fatalf("observer=%d", w.Code)
		}
		if reader.calls != before {
			t.Fatal("forbidden request read incidents")
		}
	})
}
