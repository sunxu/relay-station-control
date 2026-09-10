package api

import (
	"bytes"
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
	store "github.com/sunxu/relay-station-control/internal/store"
)

type problemAccountHTTPReader struct {
	page  store.ProblemAccountPage
	err   error
	query store.ProblemAccountQuery
	calls int
}

func (r *problemAccountHTTPReader) ListProblemAccounts(ctx context.Context, query store.ProblemAccountQuery) (store.ProblemAccountPage, error) {
	r.calls++
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 5*time.Second {
		return store.ProblemAccountPage{}, errors.New("missing bounded timeout")
	}
	r.query = query
	return r.page, r.err
}

func problemAccountHTTPItem(node uuid.UUID, email string, now time.Time) store.ProblemAccountItem {
	refresh := now.Add(-time.Minute)
	expected := now.Add(time.Hour)
	retry := now.Add(30 * time.Second)
	success := now.Add(-2 * time.Minute)
	failure := now.Add(-time.Second)
	return store.ProblemAccountItem{
		InstanceID: node, NodeName: "Node A", AccountKey: "antigravity:" + email,
		Email: email, Provider: "antigravity",
		Issues: []store.ProblemAccountIssue{
			{OccurrenceID: uuid.MustParse("018f80d8-2017-7b3e-93ec-10f4b3672f2a"), Type: "CROSS_NODE_DUPLICATE_OWNERSHIP", Reason: "cross_node_duplicate_ownership", Severity: "Critical", Since: now.Add(-time.Hour)},
			{OccurrenceID: uuid.MustParse("018f80d8-2017-7b3e-93ec-10f4b3672f2b"), Type: "FORBIDDEN", Reason: "forbidden", Severity: "Warning", Since: now.Add(-30 * time.Minute)},
		},
		Availability: store.AccountAvailability{State: "UNKNOWN", Reason: "pending_confirmation", Since: &success},
		TokenState:   "INVALID", LastRefreshAt: &refresh, ExpectedValidUntil: &expected,
		NextRetryAt: &retry, LastSuccessAt: &success, LastFailureAt: &failure,
		HighestSeverity: "Critical", OldestActiveSince: now.Add(-time.Hour),
	}
}

func TestProblemAccountsPOSTHTTPContracts(t *testing.T) {
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
	adminID := uuid.New()
	token := "problem-session-" + uuid.NewString()
	csrfToken := "problem-csrf-" + uuid.NewString()
	tokenDigest, err := authn.ComputeDigest(config.Keyring, authn.DomainSessionDigest, token)
	if err != nil {
		t.Fatal(err)
	}
	csrfDigest, err := authn.ComputeDigest(config.Keyring, authn.DomainCSRFDigest, csrfToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at) VALUES($1,$2,'Problems reader','enabled',CURRENT_TIMESTAMP)`, adminID, "problem_"+adminID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO control_admin_sessions(session_id,admin_id,token_digest,csrf_digest,key_version,mfa_method,absolute_expires_at) VALUES($1,$2,$3,$4,$5,'none',CURRENT_TIMESTAMP+interval '12 hours')`, uuid.New(), adminID, tokenDigest.Sum[:], csrfDigest.Sum[:], int32(tokenDigest.KeyVersion)); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 9, 11, 1, 2, 3, 0, time.UTC)
	node := uuid.New()
	reader := &problemAccountHTTPReader{page: store.ProblemAccountPage{Items: []store.ProblemAccountItem{
		problemAccountHTTPItem(node, "alice@example.invalid", now),
		problemAccountHTTPItem(uuid.New(), "bob@example.invalid", now.Add(time.Minute)),
	}, HasMore: true}}
	server := NewAuthenticatedServer("test", service)
	if err := server.SetProblemAccountReader(reader); err != nil {
		t.Fatal(err)
	}
	handler := HandlerWithOptions(server, ChiServerOptions{ErrorHandlerFunc: server.PrepareGeneratedError})
	path := "/api/problem-accounts/query"
	do := func(method, body, session, proof string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		if proof != "" {
			req.Header.Set("X-CSRF-Token", proof)
		}
		if session != "" {
			req.AddCookie(&http.Cookie{Name: authn.DevSessionCookieName, Value: session})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if method == http.MethodPost && (w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Request-ID") == "") {
			t.Fatalf("sensitive headers missing: %v", w.Header())
		}
		return w
	}

	t.Run("default full projection and cursor", func(t *testing.T) {
		before := reader.calls
		w := do(http.MethodPost, `{}`, token, csrfToken)
		if w.Code != http.StatusOK || reader.calls != before+1 {
			t.Fatalf("status/calls=%d/%d body=%s", w.Code, reader.calls, w.Body.String())
		}
		var got ProblemAccountResponse
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Items) != 2 || got.NextCursor == nil {
			t.Fatalf("response=%+v", got)
		}
		item := got.Items[0]
		if item.Email != "alice@example.invalid" || item.Provider != "antigravity" || item.AccountKey != "antigravity:alice@example.invalid" || item.NodeName != "Node A" || item.TokenState != "INVALID" || item.HighestSeverity != "Critical" || item.LastRefreshAt == nil || item.ExpectedValidUntil == nil || item.NextRetryAt == nil || item.LastSuccessAt == nil || item.LastFailureAt == nil || item.OldestActiveSince.IsZero() || item.Availability == nil || len(item.Issues) != 2 {
			t.Fatalf("full projection lost fields: %+v", item)
		}
		if item.Issues[0].Type != "CROSS_NODE_DUPLICATE_OWNERSHIP" || item.Issues[1].Reason != "forbidden" {
			t.Fatalf("issues=%+v", item.Issues)
		}
		for _, field := range []string{"access_token", "refresh_token", "auth_file", "oauth_secret", "raw_response", "webhook_url", "signing_secret"} {
			if strings.Contains(w.Body.String(), `"`+field+`"`) {
				t.Fatalf("credential field leaked: %s", field)
			}
		}
	})

	t.Run("filters limit and cursor are forwarded and normalized", func(t *testing.T) {
		filterNode := reader.page.Items[0].InstanceID
		body := map[string]any{"provider": "antigravity", "node": filterNode, "severity": "Critical", "reason": "cross_node_duplicate_ownership", "email": "  ALICE@EXAMPLE.INVALID ", "limit": 1}
		encoded, _ := json.Marshal(body)
		w := do(http.MethodPost, string(encoded), token, csrfToken)
		if w.Code != http.StatusOK || reader.query.Limit != 1 || reader.query.Filters.Provider != "antigravity" || reader.query.Filters.NodeID != filterNode || reader.query.Filters.Email != "alice@example.invalid" || reader.query.Filters.Reason != "cross_node_duplicate_ownership" {
			t.Fatalf("status/query=%d/%+v body=%s", w.Code, reader.query, w.Body.String())
		}
		var got ProblemAccountResponse
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.NextCursor == nil {
			t.Fatalf("cursor=%v err=%v", got.NextCursor, err)
		}
		body["cursor"] = *got.NextCursor
		encoded, _ = json.Marshal(body)
		w = do(http.MethodPost, string(encoded), token, csrfToken)
		last := reader.page.Items[1]
		if w.Code != http.StatusOK || reader.query.After == nil || reader.query.After.Email != last.Email || reader.query.After.NodeID != last.InstanceID || reader.query.After.Severity != last.HighestSeverity {
			t.Fatalf("cursor query=%+v status=%d", reader.query, w.Code)
		}
	})

	for _, invalid := range []string{
		`{"reason":"not-a-reason"}`, `{"severity":"Severe"}`, `{"node":"00000000-0000-0000-0000-000000000000"}`,
		`{"provider":"bad provider"}`, `{"limit":0}`, `{"limit":101}`, `{"unknown":1}`,
		`{"provider":""}`, `{"severity":""}`, `{"reason":""}`,
	} {
		w := do(http.MethodPost, invalid, token, csrfToken)
		if w.Code != http.StatusBadRequest {
			t.Errorf("invalid %s status=%d body=%s", invalid, w.Code, w.Body.String())
		}
	}
	if w := do(http.MethodPost, `not-json`, token, csrfToken); w.Code != http.StatusBadRequest {
		t.Errorf("invalid body status=%d", w.Code)
	}
	if w := do(http.MethodPost, `{"provider":"ANTIGRAVITY"}`, token, csrfToken); w.Code != http.StatusBadRequest {
		t.Errorf("uppercase provider status=%d", w.Code)
	}

	t.Run("cursor tampering and unknown provider", func(t *testing.T) {
		w := do(http.MethodPost, `{}`, token, csrfToken)
		var page ProblemAccountResponse
		if json.Unmarshal(w.Body.Bytes(), &page) != nil || page.NextCursor == nil {
			t.Fatal("missing cursor")
		}
		for name, body := range map[string]string{
			"wrong filters":    `{"provider":"other","cursor":"` + *page.NextCursor + `"}`,
			"empty cursor":     `{"cursor":""}`,
			"malformed cursor": `{"cursor":"invalid!"}`,
			"unknown provider": `{"provider":"not-installed"}`,
		} {
			w := do(http.MethodPost, body, token, csrfToken)
			want := http.StatusOK
			if name != "unknown provider" {
				want = http.StatusBadRequest
			}
			if w.Code != want {
				t.Errorf("%s status=%d body=%s", name, w.Code, w.Body.String())
			}
		}
	})

	if w := do(http.MethodPost, `{}`, "", csrfToken); w.Code != http.StatusUnauthorized {
		t.Errorf("missing session=%d", w.Code)
	}
	if w := do(http.MethodPost, `{}`, token, ""); w.Code != http.StatusForbidden {
		t.Errorf("missing csrf=%d", w.Code)
	}
	if w := do(http.MethodPost, `{}`, token, "wrong-csrf"); w.Code != http.StatusForbidden {
		t.Errorf("bad csrf=%d", w.Code)
	}

	var csrfAudit, authzAudit int
	if err = owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE actor_admin_id=$1 AND action='auth.csrf' AND result='denied'`, adminID).Scan(&csrfAudit); err != nil {
		t.Fatal(err)
	}
	if csrfAudit < 1 {
		t.Fatalf("CSRF rejection was not audited: %d", csrfAudit)
	}
	if _, err = owner.Exec(ctx, `ALTER TABLE control_admin_users DROP CONSTRAINT control_admin_users_role_fixed`); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `UPDATE control_admin_users SET role='observer' WHERE admin_id=$1`, adminID); err != nil {
		t.Fatal(err)
	}
	before := reader.calls
	roleResponse := do(http.MethodPost, `{}`, token, csrfToken)
	if roleResponse.Code != http.StatusForbidden || reader.calls != before {
		t.Fatalf("role rejection status/calls=%d/%d", roleResponse.Code, reader.calls)
	}
	// Authenticate rejects unsupported roles before returning a trusted Session,
	// so its existing denial audit is correlated by request_id, not actor ID.
	if err = owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE request_id=$1 AND action=$2 AND result='denied'`, roleResponse.Header().Get("X-Request-ID"), string(authn.AuditAuthorization)).Scan(&authzAudit); err != nil {
		t.Fatal(err)
	}
	if authzAudit < 1 {
		t.Fatalf("authorization rejection was not audited: %d", authzAudit)
	}
	if _, err = owner.Exec(ctx, `UPDATE control_admin_users SET role='super_admin' WHERE admin_id=$1`, adminID); err != nil {
		t.Fatal(err)
	}

	reader.err = errors.New("SECRET postgres://problem-db-password")
	w := do(http.MethodPost, `{}`, token, csrfToken)
	if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "SECRET") || strings.Contains(w.Body.String(), "problem-db-password") {
		t.Fatalf("DB error leaked: status=%d body=%s", w.Code, w.Body.String())
	}
	reader.err = nil

	before = reader.calls
	if w = do(http.MethodGet, path, token, csrfToken); w.Code != http.StatusMethodNotAllowed || reader.calls != before {
		t.Fatalf("GET endpoint/status/calls=%d/%d", w.Code, reader.calls)
	}
	var jobsBefore, jobsAfter int
	if err = owner.QueryRow(ctx, `SELECT count(*) FROM async_jobs`).Scan(&jobsBefore); err != nil {
		t.Fatal(err)
	}
	if w = do(http.MethodPost, `{}`, token, csrfToken); w.Code != http.StatusOK {
		t.Fatalf("read status=%d", w.Code)
	}
	if err = owner.QueryRow(ctx, `SELECT count(*) FROM async_jobs`).Scan(&jobsAfter); err != nil {
		t.Fatal(err)
	}
	if jobsBefore != jobsAfter {
		t.Fatalf("read created jobs: before=%d after=%d", jobsBefore, jobsAfter)
	}
}

var _ store.ProblemAccountReader = (*problemAccountHTTPReader)(nil)
