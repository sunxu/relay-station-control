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

type unifiedAccountAuditedHTTPReader struct {
	accountQualityHTTPReader
	audit store.AccountInventoryViewAudit
}

func (r *unifiedAccountAuditedHTTPReader) ListAccountQualityAndAudit(ctx context.Context, q store.AccountQualityQuery, a store.AccountInventoryViewAudit) (store.AccountQualityPage, error) {
	r.audit = a
	return r.ListAccountQuality(ctx, q)
}
func TestUnifiedAccountPOSTContracts(t *testing.T) {
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

	reader := &unifiedAccountAuditedHTTPReader{accountQualityHTTPReader: accountQualityHTTPReader{page: store.AccountQualityPage{Items: []store.AccountQualityItem{{AccountKey: "openai:alice@example.invalid", Email: "alice@example.invalid", Provider: "openai", Quality: "unknown"}}, HasMore: true}}}
	now := time.Now().UTC().Truncate(time.Microsecond)
	latency := int64(123)
	failure := "auth"
	reader.page.Items[0].Inventory = store.AccountInventoryItem{InstanceID: node, Provider: "openai", Email: "alice@example.invalid", BasicStatus: store.AccountInventoryBasicStatus("reported_active"), Lifecycle: store.AccountInventoryPresent, FirstSeenAt: now, LastSeenAt: now, ProviderLastCompleteAt: now, SnapshotFreshness: store.AccountInventorySnapshotFreshness("fresh")}
	reader.page.Items[0].RecentRequests = []store.AccountRequestHistoryItem{{RequestID: "recent-safe-id", Model: "model", OccurredAt: now, DurationMS: &latency, Success: false, FailureClass: &failure}}
	server := NewAuthenticatedServer("test", service)
	server.SetAccountQualityReader(reader)
	server.accountInventoryCursor, err = store.NewAccountInventoryCursorCodec(config.Keyring)
	if err != nil {
		t.Fatal(err)
	}
	handler := HandlerWithOptions(server, ChiServerOptions{ErrorHandlerFunc: server.PrepareGeneratedError})
	path := "/api/topology/nodes/" + node.String() + "/account-quality/query"
	do := func(body, cookie, proof string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", proof)
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: authn.DevSessionCookieName, Value: cookie})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("missing no-store")
		}
		return w
	}
	if w := do(`{}`, "", "quality-csrf"); w.Code != 401 {
		t.Fatalf("no session=%d", w.Code)
	}
	if w := do(`{}`, token, ""); w.Code != 400 {
		t.Fatalf("no CSRF=%d %s", w.Code, w.Body.String())
	}
	if w := do(`{}`, token, "wrong-csrf"); w.Code != 403 {
		t.Fatalf("invalid CSRF=%d", w.Code)
	}
	w := do(`{}`, token, "quality-csrf")
	if w.Code != 200 {
		t.Fatalf("default=%d %s", w.Code, w.Body.String())
	}
	if reader.query.Window != 15*time.Minute || reader.query.Limit != 25 || reader.query.Lifecycle != "" {
		t.Fatalf("defaults=%+v", reader.query)
	}
	if reader.audit.ActorAdminID != admin || reader.audit.RequestID == "" || len(reader.audit.SourceFingerprint) == 0 {
		t.Fatalf("missing audit metadata=%+v", reader.audit)
	}
	var page NodeAccountQualityResponse
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || page.NextCursor == nil {
		t.Fatalf("cursor=%v err=%v", page.NextCursor, err)
	}
	if page.Items[0].Inventory.InstanceId != node || string(page.Items[0].Inventory.BasicStatus) != "reported_active" || len(page.Items[0].RecentRequests) != 1 || page.Items[0].RecentRequests[0].RequestId != "recent-safe-id" || page.Items[0].RecentRequests[0].Success || page.Items[0].RecentRequests[0].DurationMs == nil || *page.Items[0].RecentRequests[0].DurationMs != 123 {
		t.Fatalf("unified projection incorrect: %+v", page.Items[0])
	}
	for _, field := range []string{"window", "quality", "provider", "lifecycle", "basic_status", "email"} {
		values := map[string]string{"window": "1h", "quality": "good", "provider": "other", "lifecycle": "present", "basic_status": "disabled", "email": "alice@example.invalid"}
		body, _ := json.Marshal(map[string]any{"cursor": *page.NextCursor, field: values[field]})
		if w := do(string(body), token, "quality-csrf"); w.Code != 400 {
			t.Errorf("cursor %s mismatch=%d", field, w.Code)
		}
	}
	body, _ := json.Marshal(map[string]any{"cursor": *page.NextCursor})
	if w := do(string(body), token, "quality-csrf"); w.Code != 200 {
		t.Fatalf("next=%d %s", w.Code, w.Body.String())
	}
	if reader.query.AfterAccountKey != "openai:alice@example.invalid" {
		t.Fatal("cursor lost key")
	}
	for _, invalid := range []string{`{"unknown":1}`, `{"limit":0}`, `{"limit":101}`, `{"window":"7d"}`, `{"quality":"healthy"}`, `{"email":"UPPER@example.invalid"}`, `{"email":"` + strings.Repeat("a", 321) + `"}`, `{"cursor":"bad"}`, `{"email":"` + strings.Repeat("a", 17000) + `"}`} {
		if w := do(invalid, token, "quality-csrf"); w.Code != 400 && w.Code != 413 {
			t.Errorf("invalid request status=%d", w.Code)
		}
	}
	reader.err = errors.New("audit unavailable")
	if w := do(`{}`, token, "quality-csrf"); w.Code != 503 || strings.Contains(w.Body.String(), "alice@example.invalid") {
		t.Fatalf("audit failure=%d %s", w.Code, w.Body.String())
	}
	reader.err = nil
	before := reader.calls
	if _, err := owner.Exec(ctx, `ALTER TABLE control_admin_users DROP CONSTRAINT control_admin_users_role_fixed; UPDATE control_admin_users SET role='observer'`); err != nil {
		t.Fatal(err)
	}
	if w := do(`{}`, token, "quality-csrf"); w.Code != 403 || reader.calls != before {
		t.Fatalf("non-admin read=%d", w.Code)
	}
	if _, err := owner.Exec(ctx, `UPDATE control_admin_users SET role='super_admin'`); err != nil {
		t.Fatal(err)
	}
	server.accountInventoryCursor = nil
	if w := do(`{}`, token, "quality-csrf"); w.Code != 503 {
		t.Fatalf("missing codec=%d", w.Code)
	}
}
