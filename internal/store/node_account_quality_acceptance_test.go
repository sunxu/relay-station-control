package store_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/sunxu/relay-station-control/internal/requestquality"
	store "github.com/sunxu/relay-station-control/internal/store"
)

func TestNodeAccountQualityAcceptancePostgres(t *testing.T) {
	ctx := context.Background()
	fixture := newReadonlyQueryFixture(t, ctx, []lifecycleAccount{
		{email: "rate100@example.invalid"}, {email: "rate095@example.invalid"},
		{email: "rate094@example.invalid"}, {email: "rate080@example.invalid"},
		{email: "rate079@example.invalid"}, {email: "unknown@example.invalid"},
		{email: "window@example.invalid"},
	})
	repo, err := store.NewAccountRequestQualityRepository(fixture.database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	var now time.Time
	if err := fixture.database.owner.QueryRow(ctx, `SELECT statement_timestamp()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	node := fixture.lifecycle.instanceID
	failure := "auth"
	events := []requestquality.Event{}
	for _, rate := range []int{100, 95, 94, 80, 79} {
		key := fmt.Sprintf("openai:rate%03d@example.invalid", rate)
		for i := 0; i < 100; i++ {
			duration := int64(i + 1)
			e := requestquality.Event{NodeID: node, Provider: "openai", AccountKey: &key, EventHash: fmt.Sprintf("rate%d-%d", rate, i), OccurredAt: now.Add(-time.Minute), DurationMS: &duration, Success: i < rate}
			if !e.Success {
				e.FailureClass = &failure
			}
			events = append(events, e)
		}
	}
	key := "openai:window@example.invalid"
	d := int64(200)
	events = append(events,
		requestquality.Event{NodeID: node, Provider: "openai", AccountKey: &key, EventHash: "recent", OccurredAt: now.Add(-time.Minute), Success: true, DurationMS: &d},
		requestquality.Event{NodeID: node, Provider: "openai", AccountKey: &key, EventHash: "old", OccurredAt: now.Add(-30 * time.Minute), FailureClass: &failure},
		requestquality.Event{NodeID: node, Provider: "openai", EventHash: "unresolved", OccurredAt: now.Add(-time.Minute), FailureClass: &failure},
		requestquality.Event{NodeID: node, Provider: "openai", AccountKey: stringPtr("openai:not-in-inventory@example.invalid"), EventHash: "event-only", OccurredAt: now.Add(-time.Minute), FailureClass: &failure},
	)
	if err := repo.InsertRequestEvents(ctx, events); err != nil {
		t.Fatal(err)
	}
	query := store.AccountQualityQuery{InstanceID: node, Window: 15 * time.Minute, Limit: 100}
	t.Run("exact classification counts nulls and account enumeration", func(t *testing.T) {
		page, err := repo.ListAccountQuality(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 7 || page.HasMore {
			t.Fatalf("items=%d more=%v", len(page.Items), page.HasMore)
		}
		want := map[string]string{"rate100": "good", "rate095": "good", "rate094": "degraded", "rate080": "degraded", "rate079": "bad", "unknown": "unknown", "window": "good"}
		previous := ""
		for _, item := range page.Items {
			if item.AccountKey <= previous {
				t.Fatal("non-deterministic key ordering")
			}
			previous = item.AccountKey
			name := strings.Split(item.Email, "@")[0]
			if want[name] != item.Quality || item.AccountKey != "openai:"+item.Email || item.Provider != "openai" {
				t.Fatalf("unexpected identity/classification: %+v", item)
			}
			q := item.Stats
			if name == "unknown" {
				if q.RequestCount != 0 || q.SuccessCount != 0 || q.FailureCount != 0 || q.SuccessRate != nil || q.P95LatencyMS != nil || q.LastSuccessAt != nil || q.LastFailureAt != nil || q.LastFailureClass != nil {
					t.Fatalf("unknown not zero/null: %+v", q)
				}
			} else if strings.HasPrefix(name, "rate") {
				var rate int
				fmt.Sscanf(name, "rate%d", &rate)
				if q.RequestCount != 100 || q.SuccessCount != int64(rate) || q.FailureCount != int64(100-rate) || q.SuccessRate == nil || *q.SuccessRate != float64(rate)/100 || q.P95LatencyMS == nil || math.Abs(*q.P95LatencyMS-95.05) > .00001 {
					t.Fatalf("statistics mismatch: %+v", q)
				}
				if q.LastSuccessAt == nil || !q.LastSuccessAt.Equal(now.Add(-time.Minute)) {
					t.Fatal("last success timestamp")
				}
				if rate < 100 && (q.LastFailureAt == nil || !q.LastFailureAt.Equal(now.Add(-time.Minute)) || q.LastFailureClass == nil || *q.LastFailureClass != "auth") {
					t.Fatal("last failure metadata")
				}
			}
		}
		// Quality reads must leave the existing lifecycle truth intact.
		var present int
		if err := fixture.database.owner.QueryRow(ctx, `SELECT count(*) FROM account_inventory WHERE instance_id=$1 AND lifecycle='present'`, node).Scan(&present); err != nil || present != 7 {
			t.Fatalf("inventory mutated: %d %v", present, err)
		}
	})
	t.Run("windows filters and keyset pages", func(t *testing.T) {
		for _, tc := range []struct {
			filter string
			count  int
		}{{"good", 3}, {"degraded", 2}, {"bad", 1}, {"unknown", 1}} {
			q := query
			q.Quality = tc.filter
			q.Provider = "openai"
			page, err := repo.ListAccountQuality(ctx, q)
			if err != nil || len(page.Items) != tc.count {
				t.Fatalf("filter %s: %d %v", tc.filter, len(page.Items), err)
			}
			for _, row := range page.Items {
				if row.Quality != tc.filter {
					t.Fatal("filter leakage")
				}
			}
		}
		q := query
		q.Provider = "anthropic"
		page, err := repo.ListAccountQuality(ctx, q)
		if err != nil || len(page.Items) != 0 {
			t.Fatalf("provider exclusion: %v", err)
		}
		q = query
		q.Window = time.Hour
		q.Quality = "bad"
		page, err = repo.ListAccountQuality(ctx, q)
		if err != nil || len(page.Items) != 2 {
			t.Fatalf("1h bad count %d %v", len(page.Items), err)
		}
		if last := page.Items[1]; last.AccountKey != key || last.Stats.RequestCount != 2 || last.Stats.SuccessCount != 1 || last.Stats.FailureCount != 1 || last.Stats.SuccessRate == nil || *last.Stats.SuccessRate != .5 {
			t.Fatalf("window or unresolved contamination: %+v", last)
		}
		q = query
		q.Limit = 2
		var keys []string
		for {
			page, err = repo.ListAccountQuality(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Items) > 2 {
				t.Fatal("page unbounded")
			}
			for _, row := range page.Items {
				if len(keys) > 0 && keys[len(keys)-1] >= row.AccountKey {
					t.Fatal("duplicate or out of order page")
				}
				keys = append(keys, row.AccountKey)
			}
			if !page.HasMore {
				break
			}
			if len(page.Items) == 0 {
				t.Fatal("empty continuation")
			}
			q.AfterAccountKey = keys[len(keys)-1]
		}
		if len(keys) != 7 {
			t.Fatalf("pagination lost accounts: %v", keys)
		}
	})
	t.Run("invalid missing inconsistent and cancellation", func(t *testing.T) {
		q := query
		q.InstanceID = uuid.New()
		if _, err := repo.ListAccountQuality(ctx, q); !errors.Is(err, store.ErrAccountInventoryInstanceNotFound) {
			t.Fatalf("missing node: %v", err)
		}
		q = query
		q.Provider = "UPPER"
		if _, err := repo.ListAccountQuality(ctx, q); !errors.Is(err, store.ErrInvalidAccountInventoryQuery) {
			t.Fatalf("invalid provider: %v", err)
		}
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := repo.ListAccountQuality(cancelled, query); err == nil {
			t.Fatal("cancellation returned successful empty")
		}
		if _, err := fixture.database.owner.Exec(ctx, `DELETE FROM node_capabilities WHERE instance_id=$1 AND capability='management_account_inventory_read'`, node); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.ListAccountQuality(ctx, query); !errors.Is(err, store.ErrAccountInventoryCapabilityUnsupported) {
			t.Fatalf("unsupported not unavailable: %v", err)
		}
	})
}

func TestNodeAccountQualityACLAndRollbackPostgres(t *testing.T) {
	ctx := context.Background()
	fixture := newReadonlyQueryFixture(t, ctx, []lifecycleAccount{{email: "retained@example.invalid"}})
	db := fixture.database
	repo, err := store.NewAccountRequestQualityRepository(db.runtime)
	if err != nil {
		t.Fatal(err)
	}
	q := store.AccountQualityQuery{InstanceID: fixture.lifecycle.instanceID, Window: time.Hour, Limit: 25}
	if _, err := repo.ListAccountQuality(ctx, q); err != nil {
		t.Fatal(err)
	}
	sig := "public.control_query_node_account_quality_v1(uuid,text,text,text,interval,integer)"
	var definer, runtimeExec, publicExec bool
	var volatility, owner string
	var config []string
	if err := db.owner.QueryRow(ctx, `SELECT p.prosecdef,p.provolatile::text,r.rolname,p.proconfig,has_function_privilege('relay_control_runtime',p.oid,'EXECUTE'),EXISTS(SELECT 1 FROM aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a WHERE a.grantee=0 AND a.privilege_type='EXECUTE') FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner WHERE p.oid=$1::regprocedure`, sig).Scan(&definer, &volatility, &owner, &config, &runtimeExec, &publicExec); err != nil {
		t.Fatal(err)
	}
	if !definer || volatility != "s" || owner != "relay_control_migrator" || !runtimeExec || publicExec || !strings.Contains(strings.Join(config, ","), "search_path=pg_catalog") {
		t.Fatalf("unsafe ACL %v %s %s %v %v %v", definer, volatility, owner, config, runtimeExec, publicExec)
	}
	for _, table := range []string{"account_inventory", "account_inventory_provider_states", "account_request_quality_events"} {
		_, err := db.runtime.Exec(ctx, "SELECT * FROM public."+table+" LIMIT 1")
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.Code != "42501" {
			t.Fatalf("%s direct select error: %v", table, err)
		}
	}
	before := captureReadonlyQueryMigrationState(t, ctx, db)
	var functionsBefore string
	const functionSQL = `SELECT string_agg(pg_get_functiondef(oid),E'\n' ORDER BY oid) FROM pg_proc WHERE proname IN ('control_query_current_account_inventory_v1','control_query_account_request_quality_v1')`
	if err := db.owner.QueryRow(ctx, functionSQL).Scan(&functionsBefore); err != nil {
		t.Fatal(err)
	}
	if err := runAssetGoose(t, ctx, "../..", db.ownerURL, "down-to", "20"); err != nil {
		t.Fatal(err)
	}
	if page, err := repo.ListAccountQuality(ctx, q); err == nil || len(page.Items) != 0 {
		t.Fatal("missing function must fail, not successful empty")
	}
	after := captureReadonlyQueryMigrationState(t, ctx, db)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("query access down modified persistence")
	}
	var functionsAfter string
	if err := db.owner.QueryRow(ctx, functionSQL).Scan(&functionsAfter); err != nil || functionsAfter != functionsBefore {
		t.Fatal("existing read functions changed")
	}
	if err := runAssetGoose(t, ctx, "../..", db.ownerURL, "up"); err != nil {
		t.Fatal(err)
	}
	page, err := repo.ListAccountQuality(ctx, q)
	if err != nil || len(page.Items) != 1 || page.Items[0].AccountKey != "openai:retained@example.invalid" || page.Items[0].Quality != "unknown" {
		t.Fatalf("query recovery lost inventory: %+v %v", page, err)
	}
}
