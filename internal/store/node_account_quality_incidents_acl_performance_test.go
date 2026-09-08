package store_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	store "github.com/sunxu/relay-station-control/internal/store"
)

func TestAccountQualityIncidentsACLAndRollbackPostgres(t *testing.T) {
	ctx := context.Background()
	fixture := newReadonlyQueryFixture(t, ctx, []lifecycleAccount{{email: "retained@example.invalid"}})
	db := fixture.database
	repo, err := store.NewAccountRequestQualityRepository(db.runtime)
	if err != nil {
		t.Fatal(err)
	}
	q := store.AccountQualityIncidentQuery{InstanceID: fixture.lifecycle.instanceID, Limit: 25}
	if _, err := db.owner.Exec(ctx, `INSERT INTO account_request_quality_events(event_hash,node_id,provider,account_key,occurred_at,success,failure_class) SELECT 'retained-incident-'||i,$1,'openai','openai:retained@example.invalid',statement_timestamp()-interval '1 minute',false,'auth' FROM generate_series(1,3) i`, q.InstanceID); err != nil {
		t.Fatal(err)
	}
	if page, err := repo.ListAccountQualityIncidents(ctx, q); err != nil || len(page.Items) != 1 || page.Items[0].AccountKey != "openai:retained@example.invalid" || page.Items[0].FailureClass != "auth" || page.Items[0].HitCount != 3 {
		t.Fatalf("runtime execute: %+v %v", page, err)
	}
	sig := "public.control_query_node_account_quality_incidents_v1(uuid,text,text,timestamptz,text,text,integer)"
	var definer, runtimeExec, publicExec bool
	var volatility, owner string
	var config []string
	if err := db.owner.QueryRow(ctx, `SELECT p.prosecdef,p.provolatile::text,r.rolname,p.proconfig,has_function_privilege('relay_control_runtime',p.oid,'EXECUTE'),EXISTS(SELECT 1 FROM aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a WHERE a.grantee=0 AND a.privilege_type='EXECUTE') FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner WHERE p.oid=$1::regprocedure`, sig).Scan(&definer, &volatility, &owner, &config, &runtimeExec, &publicExec); err != nil {
		t.Fatal(err)
	}
	if !definer || volatility != "s" || owner != "relay_control_migrator" || !runtimeExec || publicExec || strings.Join(config, ",") != "search_path=pg_catalog" {
		t.Fatalf("unsafe function: %v %s %s %v %v %v", definer, volatility, owner, config, runtimeExec, publicExec)
	}
	for _, table := range []string{"account_inventory", "account_inventory_provider_states", "account_request_quality_events"} {
		_, err := db.runtime.Exec(ctx, "SELECT * FROM public."+table+" LIMIT 1")
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.Code != "42501" {
			t.Fatalf("%s direct SELECT: %v", table, err)
		}
	}
	before := captureReadonlyQueryMigrationState(t, ctx, db)
	const objectsSQL = `SELECT (SELECT string_agg(pg_get_functiondef(oid),E'\n' ORDER BY proname) FROM pg_proc WHERE proname IN ('control_query_current_account_inventory_v1','control_query_account_request_quality_v1','control_query_node_account_quality_v1','control_query_account_request_history_v1')) || (SELECT string_agg(indexdef,E'\n' ORDER BY indexname) FROM pg_indexes WHERE schemaname='public' AND tablename='account_request_quality_events')`
	var objectsBefore, objectsAfter string
	if err := db.owner.QueryRow(ctx, objectsSQL).Scan(&objectsBefore); err != nil {
		t.Fatal(err)
	}
	if err := runAssetGoose(t, ctx, "../..", db.ownerURL, "down-to", "22"); err != nil {
		t.Fatal(err)
	}
	if page, err := repo.ListAccountQualityIncidents(ctx, q); err == nil || len(page.Items) != 0 {
		t.Fatal("missing function returned successful data/empty")
	}
	if !reflect.DeepEqual(before, captureReadonlyQueryMigrationState(t, ctx, db)) {
		t.Fatal("Down changed persistence")
	}
	if err := db.owner.QueryRow(ctx, objectsSQL).Scan(&objectsAfter); err != nil || objectsAfter != objectsBefore {
		t.Fatalf("Down changed existing functions/indexes: %v", err)
	}
	if err := runAssetGoose(t, ctx, "../..", db.ownerURL, "up"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, captureReadonlyQueryMigrationState(t, ctx, db)) {
		t.Fatal("Up changed persistence")
	}
	if err := db.owner.QueryRow(ctx, objectsSQL).Scan(&objectsAfter); err != nil || objectsAfter != objectsBefore {
		t.Fatalf("Up changed existing functions/indexes: %v", err)
	}
	if page, err := repo.ListAccountQualityIncidents(ctx, q); err != nil || len(page.Items) != 1 || page.Items[0].AccountKey != "openai:retained@example.invalid" || page.Items[0].FailureClass != "auth" || page.Items[0].HitCount != 3 {
		t.Fatalf("Up lost exact retained event: %+v %v", page, err)
	}
}

func TestAccountQualityIncidentsPerformancePostgres(t *testing.T) {
	ctx := context.Background()
	accounts := make([]lifecycleAccount, 100)
	for i := range accounts {
		accounts[i] = lifecycleAccount{email: fmt.Sprintf("incident-perf%03d@example.invalid", i)}
	}
	f := newReadonlyQueryFixture(t, ctx, accounts)
	cfg, err := pgxpool.ParseConfig(f.database.runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	tracer := &qualityQueryTracer{}
	cfg.ConnConfig.Tracer = tracer
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	repo, err := store.NewAccountRequestQualityRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.owner.Exec(ctx, `INSERT INTO account_request_quality_events(event_hash,node_id,provider,account_key,occurred_at,success,failure_class)
 SELECT 'incident-'||a||'-'||e,$1,'openai','openai:incident-perf'||lpad(a::text,3,'0')||'@example.invalid',statement_timestamp()-interval '1 minute',false,(ARRAY['auth','quota','rate_limit','upstream'])[e%4+1] FROM generate_series(0,99) a CROSS JOIN generate_series(1,100) e`, f.lifecycle.instanceID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.owner.Exec(ctx, `ANALYZE account_request_quality_events`); err != nil {
		t.Fatal(err)
	}
	var first store.AccountQualityIncidentPage
	classes := []string{"auth", "quota", "rate_limit", "upstream"}
	for _, tc := range []struct {
		name, provider, class string
		limit, start          int
	}{
		{"active", "", "", 25, 0}, {"provider", "openai", "", 25, 0}, {"failure", "", "auth", 25, 0}, {"next", "", "", 25, 25}, {"limit100", "", "", 100, 0}, {"provider-empty", "anthropic", "", 25, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := store.AccountQualityIncidentQuery{InstanceID: f.lifecycle.instanceID, Provider: tc.provider, FailureClass: tc.class, Limit: tc.limit}
			if tc.name == "next" {
				if len(first.Items) != 25 {
					t.Fatal("missing first page")
				}
				last := first.Items[24]
				q.AfterLastSeen = &last.LastSeen
				q.AfterAccountKey = last.AccountKey
				q.AfterFailureClass = last.FailureClass
			}
			before := tracer.count
			start := time.Now()
			page, err := repo.ListAccountQualityIncidents(ctx, q)
			elapsed := time.Since(start)
			if err != nil {
				t.Fatal(err)
			}
			count := tc.limit
			if tc.name == "provider-empty" {
				count = 0
			}
			if len(page.Items) != count || page.HasMore != (count > 0) {
				t.Fatalf("rows=%d more=%v", len(page.Items), page.HasMore)
			}
			for i, row := range page.Items {
				position := i + tc.start
				account := position / 4
				class := classes[position%4]
				if tc.class != "" {
					account = i
					class = tc.class
				}
				key := fmt.Sprintf("openai:incident-perf%03d@example.invalid", account)
				if row.NodeID != q.InstanceID || row.AccountKey != key || row.Provider != "openai" || row.FailureClass != class || row.Status != "active" || row.HitCount != 25 || row.LastSuccessAt != nil || !row.FirstSeen.Equal(row.LastSeen) {
					t.Fatalf("row %d: %+v", i, row)
				}
			}
			if tracer.count-before != 1 {
				t.Fatalf("query count=%d", tracer.count-before)
			}
			if elapsed >= time.Second {
				t.Fatalf("local 1s budget exceeded %s", elapsed)
			}
			if tc.name == "active" {
				first = page
			}
			t.Logf("accounts=100 events=10000 rows=%d queries=%d latency=%s", count, tracer.count-before, elapsed)
		})
	}
}
