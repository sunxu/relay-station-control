package api

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	authn "github.com/sunxu/relay-station-control/internal/auth"
	poll "github.com/sunxu/relay-station-control/internal/inventorypoll"
	store "github.com/sunxu/relay-station-control/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type capacityReaderFixture struct {
	snapshot store.InventoryPollCapacity
	err      error
	calls    int
}

func (f *capacityReaderFixture) GetInventoryPollCapacity(ctx context.Context) (store.InventoryPollCapacity, error) {
	f.calls++
	if _, ok := ctx.Deadline(); !ok {
		return f.snapshot, errors.New("missing deadline")
	}
	return f.snapshot, f.err
}
func TestInventoryPollCapacityHTTP(t *testing.T) {
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

	now := time.Now().UTC()
	reader := &capacityReaderFixture{snapshot: store.InventoryPollCapacity{EvaluatedAt: now, EvaluatedSlot: now.Truncate(5 * time.Minute), EligibleNodeCount: 3}}
	budget, err := (poll.Config{Concurrency: 1}).Validate()
	if err != nil {
		t.Fatal(err)
	}
	server := NewAuthenticatedServer("test", service)
	server.SetInventoryPollCapacityReader(reader, true, budget)
	handler := HandlerWithOptions(server, ChiServerOptions{ErrorHandlerFunc: server.PrepareGeneratedError})
	do := func(cookie string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/api/account-inventory/poll-capacity", nil)
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: authn.DevSessionCookieName, Value: cookie})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := do(""); w.Code != 401 || reader.calls != 0 {
		t.Fatalf("unauthorized status=%d calls=%d", w.Code, reader.calls)
	}
	for _, tc := range []struct {
		name    string
		count   int64
		enabled bool
		want    AccountInventoryPollCapacityStatus
	}{
		{"over", 3, true, AccountInventoryPollCapacityStatusCapacityExceeded}, {"recovered", 2, true, AccountInventoryPollCapacityStatusReady}, {"disabled", 3, false, AccountInventoryPollCapacityStatusDisabled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader.snapshot.EligibleNodeCount = tc.count
			server.SetInventoryPollCapacityReader(reader, tc.enabled, budget)
			w := do(token)
			if w.Code != 200 {
				t.Fatalf("status=%d", w.Code)
			}
			if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
				t.Fatal("cacheable diagnostics")
			}
			var got AccountInventoryPollCapacity
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Status != tc.want || got.EffectiveCapacity != 2 || got.Concurrency != 1 || got.EligibleNodeCount != tc.count || got.ClaimTimeoutMs != 1000 || got.LifecycleTimeoutMs != 30000 || !got.EvaluatedSlot.Equal(now.Truncate(5*time.Minute)) {
				t.Fatalf("wrong diagnostic: %+v", got)
			}
		})
	}
	reader.err = errors.New("secret raw database error")
	w := do(token)
	if w.Code != 503 || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("failure not sanitized: %d", w.Code)
	}
	reader.err = nil
	reader.snapshot.EligibleNodeCount = -1
	if w := do(token); w.Code != 503 {
		t.Fatalf("invalid count status=%d", w.Code)
	}
	reader.snapshot.EligibleNodeCount = 2
	server = NewAuthenticatedServer("restarted", service)
	server.SetInventoryPollCapacityReader(reader, true, budget)
	handler = HandlerWithOptions(server, ChiServerOptions{ErrorHandlerFunc: server.PrepareGeneratedError})
	if w := do(token); w.Code != 200 {
		t.Fatalf("restart read status=%d", w.Code)
	}
	var views int
	if err := owner.QueryRow(ctx, "SELECT count(*) FROM audit_logs WHERE action = 'account_inventory.view'").Scan(&views); err != nil {
		t.Fatal(err)
	}
	if views != 0 {
		t.Fatal("capacity GET produced account-view audit")
	}

}
