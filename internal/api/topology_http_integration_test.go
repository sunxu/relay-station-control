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
	store "github.com/sunxu/relay-station-control/internal/store"
)

type topologyProviderReader struct {
	view  store.AccountInventoryProviderStates
	err   error
	calls int
}

func (f *topologyProviderReader) GetProviderStates(ctx context.Context, id uuid.UUID) (store.AccountInventoryProviderStates, error) {
	f.calls++
	if _, ok := ctx.Deadline(); !ok {
		return f.view, errors.New("missing timeout")
	}
	return f.view, f.err
}

type topologyHistoryReader struct {
	page   store.CrossNodeDuplicateOccurrencePage
	err    error
	node   uuid.UUID
	status string
	cursor *store.CrossNodeDuplicateOccurrenceCursor
	limit  int
	calls  int
}

func (f *topologyHistoryReader) ListCrossNodeDuplicateOccurrencesHistoricallyInvolvingNode(ctx context.Context, id uuid.UUID, status string, cursor *store.CrossNodeDuplicateOccurrenceCursor, limit int) (store.CrossNodeDuplicateOccurrencePage, error) {
	f.calls++
	f.node = id
	f.status = status
	f.cursor = cursor
	f.limit = limit
	return f.page, f.err
}

func TestTopologyHTTPReadContracts(t *testing.T) {
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
	token := "topology-session-" + uuid.NewString()
	digest, err := authn.ComputeDigest(config.Keyring, authn.DomainSessionDigest, token)
	if err != nil {
		t.Fatal(err)
	}
	csrf, err := authn.ComputeDigest(config.Keyring, authn.DomainCSRFDigest, "topology-csrf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at) VALUES($1,$2,'Topology reader','enabled',CURRENT_TIMESTAMP)`, adminID, "topology_"+adminID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO control_admin_sessions(session_id,admin_id,token_digest,csrf_digest,key_version,mfa_method,absolute_expires_at) VALUES($1,$2,$3,$4,$5,'none',CURRENT_TIMESTAMP+interval '12 hours')`, uuid.New(), adminID, digest.Sum[:], csrf.Sum[:], int32(digest.KeyVersion)); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	snapshot := now.Add(-5 * time.Minute)
	state, reason := "current", "identity_incomplete"
	degraded := true
	node := uuid.New()
	occurrence := uuid.New()
	providers := &topologyProviderReader{view: store.AccountInventoryProviderStates{InstanceID: node, ObservedAt: now, Providers: []store.AccountInventoryProviderState{{Provider: "openai", MonitoringStatus: "active", State: &state, CurrentScheduledAt: &snapshot, LastCompleteAt: &snapshot, SnapshotFreshness: "fresh", HealthScheduledAt: &now, HealthDegraded: &degraded, HealthReason: &reason}}}}
	history := &topologyHistoryReader{page: store.CrossNodeDuplicateOccurrencePage{ObservedAt: &now, HasMore: true, Items: []store.CrossNodeDuplicateOccurrenceSummary{{OccurrenceID: occurrence, EnvironmentID: "test", AccountKey: "openai:synthetic@example.invalid", ConflictType: "cross_node_duplicate_ownership", Status: "RESOLVED", Severity: "Critical", FirstSeenAt: snapshot, LastSeenAt: now, EvidenceState: "complete", AffectedNodes: []uuid.UUID{}}}}}
	server := NewAuthenticatedServer("test", service)
	server.SetAccountInventoryProviderStateReader(providers)
	server.SetNodeDuplicateHistoryReader(history)
	handler := HandlerWithOptions(server, ChiServerOptions{ErrorHandlerFunc: server.PrepareGeneratedError})
	do := func(path, cookie string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: authn.DevSessionCookieName, Value: cookie})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Request-ID") == "" {
			t.Fatalf("missing sensitive headers: %v", w.Header())
		}
		return w
	}
	providerPath := "/api/account-inventory/nodes/" + node.String() + "/providers"
	historyPath := "/api/topology/nodes/" + node.String() + "/duplicate-history"
	t.Run("authenticated nine field fresh and degraded projection", func(t *testing.T) {
		w := do(providerPath, token)
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		var got NodeInventoryProviderStatesResponse
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Providers) != 1 || got.Providers[0].SnapshotFreshness != "fresh" || got.Providers[0].HealthDegraded == nil || !*got.Providers[0].HealthDegraded || !got.Providers[0].LastCompleteAt.Equal(snapshot) || !got.Providers[0].HealthScheduledAt.Equal(now) {
			t.Fatalf("projection: %+v", got)
		}
		var raw struct {
			Providers []map[string]json.RawMessage `json:"providers"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &raw)
		if len(raw.Providers[0]) != 9 {
			t.Fatalf("unexpected fields %v", raw.Providers[0])
		}
	})
	t.Run("history cursor is scoped to target and status", func(t *testing.T) {
		w := do(historyPath+"?status=RESOLVED", token)
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		var got NodeDuplicateHistoryResponse
		_ = json.Unmarshal(w.Body.Bytes(), &got)
		if got.NextCursor == nil || got.Involvement != "historical" || got.InstanceId != node || history.limit != 25 || history.status != "RESOLVED" {
			t.Fatalf("history=%+v", got)
		}
		w = do(historyPath+"?status=RESOLVED&cursor="+*got.NextCursor, token)
		if w.Code != 200 || history.cursor == nil || history.cursor.OccurrenceID != occurrence {
			t.Fatal("cursor not forwarded")
		}
		before := history.calls
		for _, path := range []string{historyPath + "?cursor=" + *got.NextCursor, "/api/topology/nodes/" + uuid.NewString() + "/duplicate-history?status=RESOLVED&cursor=" + *got.NextCursor, historyPath + "?limit=201", historyPath + "?status=invalid", historyPath + "?cursor=" + strings.Repeat("x", 513)} {
			if w := do(path, token); w.Code != 400 {
				t.Fatalf("invalid query status=%d", w.Code)
			}
		}
		if history.calls != before {
			t.Fatal("invalid cursor reached reader")
		}
	})
	t.Run("failures are unavailable never empty", func(t *testing.T) {
		providers.err = errors.New("secret raw database error")
		history.err = context.DeadlineExceeded
		defer func() { providers.err = nil; history.err = nil }()
		for _, path := range []string{providerPath, historyPath} {
			w := do(path, token)
			if w.Code != 503 || strings.Contains(w.Body.String(), "providers") || strings.Contains(w.Body.String(), "secret") {
				t.Fatalf("failure leaked/hidden: %d %s", w.Code, w.Body)
			}
		}
		providers.err = store.ErrAccountInventoryProviderStateNotFound
		history.err = store.ErrAssetNotFound
		for _, path := range []string{providerPath, historyPath} {
			if w := do(path, token); w.Code != 404 {
				t.Fatalf("not found: %d %s", w.Code, w.Body)
			}
		}
	})
	t.Run("unauthenticated invalid and forbidden requests do not read", func(t *testing.T) {
		beforeP, beforeH := providers.calls, history.calls
		for _, path := range []string{providerPath, historyPath} {
			if w := do(path, ""); w.Code != 401 {
				t.Fatalf("unauth %d", w.Code)
			}
		}
		for _, path := range []string{"/api/account-inventory/nodes/invalid/providers", "/api/topology/nodes/" + uuid.Nil.String() + "/duplicate-history"} {
			if w := do(path, token); w.Code != 400 {
				t.Fatalf("invalid %d", w.Code)
			}
		}
		// Only this disposable test DB permits a synthetic non-super-admin role.
		// Production retains its fixed-role constraint; this tests defense in depth.
		if _, err := owner.Exec(ctx, `ALTER TABLE control_admin_users DROP CONSTRAINT control_admin_users_role_fixed; UPDATE control_admin_users SET role='observer'`); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{providerPath, historyPath} {
			if w := do(path, token); w.Code != 403 {
				t.Fatalf("forbidden %d %s", w.Code, w.Body)
			}
		}
		if providers.calls != beforeP || history.calls != beforeH {
			t.Fatal("unauthorized request reached store")
		}
	})
}
