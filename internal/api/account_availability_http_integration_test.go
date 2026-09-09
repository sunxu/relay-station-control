package api

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	authn "github.com/sunxu/relay-station-control/internal/auth"
	store "github.com/sunxu/relay-station-control/internal/store"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type availabilityHTTPReader struct {
	page              store.AccountAvailabilityOccurrencePage
	values            map[string]store.AccountAvailability
	err               error
	calls, batchCalls int
	query             store.AccountAvailabilityOccurrenceQuery
	keys              []string
}

func (r *availabilityHTTPReader) ListAccountAvailabilityOccurrences(ctx context.Context, q store.AccountAvailabilityOccurrenceQuery) (store.AccountAvailabilityOccurrencePage, error) {
	if _, ok := ctx.Deadline(); !ok {
		return store.AccountAvailabilityOccurrencePage{}, errors.New("missing timeout")
	}
	r.calls++
	r.query = q
	return r.page, r.err
}
func (r *availabilityHTTPReader) BatchAccountAvailability(ctx context.Context, id uuid.UUID, keys []string) (map[string]store.AccountAvailability, error) {
	r.batchCalls++
	r.keys = keys
	return r.values, r.err
}
func TestAccountAvailabilityHTTPReadContracts(t *testing.T) {
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
	token := "availability-session-" + uuid.NewString()
	digest, err := authn.ComputeDigest(config.Keyring, authn.DomainSessionDigest, token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at) VALUES($1,$2,'Availability reader','enabled',CURRENT_TIMESTAMP)`, admin, "availability_"+admin.String()[:8]); err != nil {
		t.Fatal(err)
	}
	csrf, err := authn.ComputeDigest(config.Keyring, authn.DomainCSRFDigest, "availability-csrf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO control_admin_sessions(session_id,admin_id,token_digest,csrf_digest,key_version,mfa_method,absolute_expires_at) VALUES($1,$2,$3,$4,$5,'none',CURRENT_TIMESTAMP+interval '12 hours')`, uuid.New(), admin, digest.Sum[:], csrf.Sum[:], int32(digest.KeyVersion)); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	account := "antigravity:alice@example.invalid"
	reader := &availabilityHTTPReader{page: store.AccountAvailabilityOccurrencePage{Items: []store.AccountAvailabilityOccurrence{{OccurrenceID: uuid.New(), InstanceID: node, AccountKey: account, Reason: "forbidden", Severity: "Warning", Status: "ACTIVE", FirstSeenAt: now, LastFailureAt: now, ConfirmedAt: now}}, HasMore: true}}
	server := NewAuthenticatedServer("test", service)
	server.SetAccountAvailabilityReader(reader)
	handler := HandlerWithOptions(server, ChiServerOptions{ErrorHandlerFunc: server.PrepareGeneratedError})
	path := "/api/topology/nodes/" + node.String() + "/account-availability-occurrences"
	do := func(method, target, cookie string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, nil)
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: authn.DevSessionCookieName, Value: cookie})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if method == "GET" && (w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Request-ID") == "") {
			t.Fatalf("headers=%v", w.Header())
		}
		return w
	}
	t.Run("projection and bound cursor", func(t *testing.T) {
		target := path + "?account_key=" + url.QueryEscape(account)
		w := do("GET", target, token)
		if w.Code != 200 {
			t.Fatalf("status=%d body=%s", w.Code, w.Body)
		}
		var body NodeAccountAvailabilityOccurrenceResponse
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Items) != 1 || body.Items[0].Reason != "forbidden" || body.Items[0].Severity != "Warning" || body.Items[0].ResolvedAt != nil || body.NextCursor == nil {
			t.Fatalf("body=%s", w.Body)
		}
		cursor := *body.NextCursor
		if w = do("GET", target+"&cursor="+cursor, token); w.Code != 200 || reader.query.AfterOccurrenceID != body.Items[0].OccurrenceId || reader.query.AfterConfirmedAt == nil || !reader.query.AfterConfirmedAt.Equal(now) {
			t.Fatalf("cursor=%+v status=%d", reader.query, w.Code)
		}
		for _, bad := range []string{path + "?cursor=" + cursor, target + "&status=RESOLVED&cursor=" + cursor, "/api/topology/nodes/" + uuid.NewString() + "/account-availability-occurrences?account_key=" + url.QueryEscape(account) + "&cursor=" + cursor} {
			if w = do("GET", bad, token); w.Code != 400 {
				t.Fatalf("mismatch=%d", w.Code)
			}
		}
		if w = do("GET", path+"?status=RESOLVED&limit=100", token); w.Code != 200 || reader.query.Status != "RESOLVED" || reader.query.Limit != 100 {
			t.Fatalf("query=%+v code=%d", reader.query, w.Code)
		}
	})
	t.Run("invalid and unauthorized never read", func(t *testing.T) {
		before := reader.calls
		for _, q := range []string{"?status=unknown", "?account_key=", "?account_key=invalid", "?limit=0", "?limit=101", "?cursor=", "?cursor=" + strings.Repeat("a", 8193)} {
			if w := do("GET", path+q, token); w.Code != 400 {
				t.Fatalf("query=%s code=%d", q, w.Code)
			}
		}
		if w := do("GET", path, ""); w.Code != 401 {
			t.Fatalf("no auth=%d", w.Code)
		}
		for _, method := range []string{"POST", "PUT", "DELETE"} {
			if w := do(method, path, token); w.Code != 405 {
				t.Fatalf("method=%s code=%d", method, w.Code)
			}
		}
		if reader.calls != before {
			t.Fatal("invalid request reached store")
		}
	})
	t.Run("notfound unavailable and empty", func(t *testing.T) {
		reader.err = store.ErrAccountInventoryInstanceNotFound
		if w := do("GET", path, token); w.Code != 404 {
			t.Fatalf("notfound=%d", w.Code)
		}
		reader.err = errors.New("db failure")
		if w := do("GET", path, token); w.Code != 503 {
			t.Fatalf("failure=%d", w.Code)
		}
		reader.err = nil
		reader.page = store.AccountAvailabilityOccurrencePage{}
		if w := do("GET", path, token); w.Code != 200 || !strings.Contains(w.Body.String(), `"items":[]`) {
			t.Fatalf("empty=%d %s", w.Code, w.Body)
		}
	})
	t.Run("account page batch six states independent of quality", func(t *testing.T) {
		quality := &accountQualityHTTPReader{page: store.AccountQualityPage{}}
		reader.values = map[string]store.AccountAvailability{}
		states := []string{"AVAILABLE", "TOKEN_INVALID", "ACCOUNT_BLOCKED", "FORBIDDEN", "UNKNOWN", "DISABLED"}
		reasons := []string{"available", "token_invalid", "account_blocked", "forbidden", "stale", "disabled"}
		for i, state := range states {
			k := "antigravity:" + strings.ToLower(state) + "@example.invalid"
			quality.page.Items = append(quality.page.Items, store.AccountQualityItem{AccountKey: k, Provider: "antigravity", Quality: "unknown"})
			reader.values[k] = store.AccountAvailability{State: state, Reason: reasons[i], Since: &now}
		}
		quality.page.Items = append(quality.page.Items, store.AccountQualityItem{AccountKey: "openai:other@example.invalid", Provider: "openai", Quality: "bad"})
		server.SetAccountQualityReader(quality)
		qp := "/api/topology/nodes/" + node.String() + "/account-quality"
		before := reader.batchCalls
		w := do("GET", qp, token)
		if w.Code != 200 {
			t.Fatalf("page=%d %s", w.Code, w.Body)
		}
		var body NodeAccountQualityResponse
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if reader.batchCalls != before+1 || len(reader.keys) != 6 || len(body.Items) != 7 {
			t.Fatalf("batch calls=%d keys=%v", reader.batchCalls, reader.keys)
		}
		for i, state := range states {
			if body.Items[i].Availability == nil || string(body.Items[i].Availability.State) != state || body.Items[i].Quality != "unknown" {
				t.Fatalf("item=%+v", body.Items[i])
			}
		}
		if body.Items[6].Availability != nil {
			t.Fatal("other provider acquired availability")
		}
		reader.err = errors.New("db failed")
		if w = do("GET", qp, token); w.Code != 503 {
			t.Fatalf("failure=%d", w.Code)
		}
		reader.err = nil
		reader.values = map[string]store.AccountAvailability{}
		if w = do("GET", qp, token); w.Code != 503 {
			t.Fatalf("missing projection became unknown=%d", w.Code)
		}
	})
	t.Run("observer forbidden", func(t *testing.T) {
		if _, err := owner.Exec(ctx, `ALTER TABLE control_admin_users DROP CONSTRAINT control_admin_users_role_fixed; UPDATE control_admin_users SET role='observer'`); err != nil {
			t.Fatal(err)
		}
		before := reader.calls
		if w := do("GET", path, token); w.Code != 403 {
			t.Fatalf("observer=%d", w.Code)
		}
		if reader.calls != before {
			t.Fatal("forbidden read reached store")
		}
	})
}
