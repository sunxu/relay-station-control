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

func TestAccountRequestHistoryACLAndRollbackPostgres(t *testing.T) {
	ctx := context.Background()
	fixture := newReadonlyQueryFixture(t, ctx, []lifecycleAccount{{email: "retained@example.invalid"}})
	db := fixture.database
	repo, err := store.NewAccountRequestQualityRepository(db.runtime)
	if err != nil {
		t.Fatal(err)
	}
	q := store.AccountRequestHistoryQuery{InstanceID: fixture.lifecycle.instanceID, AccountKey: "openai:retained@example.invalid", Limit: 25}
	if _, err := db.owner.Exec(ctx, `INSERT INTO account_request_quality_events(event_hash,node_id,provider,account_key,occurred_at,success) VALUES ('retained-history',$1,'openai',$2,statement_timestamp()-interval '1 minute',true)`, q.InstanceID, q.AccountKey); err != nil {
		t.Fatal(err)
	}
	if page, err := repo.ListAccountRequestHistory(ctx, q); err != nil || len(page.Items) != 1 || page.Items[0].EventHash != "retained-history" {
		t.Fatalf("runtime execute: %+v %v", page, err)
	}
	sig := "public.control_query_account_request_history_v1(uuid,text,timestamptz,text,integer)"
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
	const objectsSQL = `SELECT (SELECT string_agg(pg_get_functiondef(oid),E'\n' ORDER BY proname) FROM pg_proc WHERE proname IN ('control_query_current_account_inventory_v1','control_query_account_request_quality_v1','control_query_node_account_quality_v1')) || (SELECT string_agg(indexdef,E'\n' ORDER BY indexname) FROM pg_indexes WHERE schemaname='public' AND tablename='account_request_quality_events')`
	var objectsBefore, objectsAfter string
	if err := db.owner.QueryRow(ctx, objectsSQL).Scan(&objectsBefore); err != nil {
		t.Fatal(err)
	}
	if err := runAssetGoose(t, ctx, "../..", db.ownerURL, "down-to", "21"); err != nil {
		t.Fatal(err)
	}
	if page, err := repo.ListAccountRequestHistory(ctx, q); err == nil || len(page.Items) != 0 {
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
	if page, err := repo.ListAccountRequestHistory(ctx, q); err != nil || len(page.Items) != 1 || page.Items[0].EventHash != "retained-history" {
		t.Fatalf("Up lost exact retained event: %+v %v", page, err)
	}
}

func TestAccountRequestHistoryPerformancePostgres(t *testing.T) {
	ctx := context.Background()
	fixture := newReadonlyQueryFixture(t, ctx, []lifecycleAccount{{email: "history-perf@example.invalid"}})
	config, err := pgxpool.ParseConfig(fixture.database.runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	tracer := &qualityQueryTracer{}
	config.ConnConfig.Tracer = tracer
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	repo, err := store.NewAccountRequestQualityRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	q := store.AccountRequestHistoryQuery{InstanceID: fixture.lifecycle.instanceID, AccountKey: "openai:history-perf@example.invalid", Limit: 25}
	if _, err := fixture.database.owner.Exec(ctx, `INSERT INTO account_request_quality_events(event_hash,node_id,provider,account_key,occurred_at,success) SELECT 'history-'||lpad(i::text,5,'0'),$1,'openai',$2,statement_timestamp()-interval '1 minute',true FROM generate_series(1,10000) i`, q.InstanceID, q.AccountKey); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.owner.Exec(ctx, `ANALYZE account_request_quality_events`); err != nil {
		t.Fatal(err)
	}
	var first store.AccountRequestHistoryPage
	for _, tc := range []struct {
		name           string
		limit, highest int
	}{{"first-25", 25, 10000}, {"next-25", 25, 9975}, {"limit-100", 100, 10000}} {
		t.Run(tc.name, func(t *testing.T) {
			query := q
			query.Limit = tc.limit
			if tc.name == "next-25" {
				if len(first.Items) != 25 {
					t.Fatal("first page unavailable")
				}
				last := first.Items[24]
				query.AfterOccurredAt = &last.OccurredAt
				query.AfterEventHash = last.EventHash
			}
			before := tracer.count
			start := time.Now()
			page, err := repo.ListAccountRequestHistory(ctx, query)
			elapsed := time.Since(start)
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Items) != tc.limit || !page.HasMore {
				t.Fatalf("page length/more: %d %v", len(page.Items), page.HasMore)
			}
			for i, item := range page.Items {
				if item.EventHash != fmt.Sprintf("history-%05d", tc.highest-i) {
					t.Fatalf("row %d: %s", i, item.EventHash)
				}
			}
			if tracer.count-before != 1 {
				t.Fatalf("queries=%d", tracer.count-before)
			}
			if elapsed >= time.Second {
				t.Fatalf("query exceeds local 1s threshold: %s", elapsed)
			}
			if tc.name == "first-25" {
				first = page
			}
			t.Logf("accounts=1 events=10000 tied_timestamps=true rows=%d queries=%d latency=%s", len(page.Items), tracer.count-before, elapsed)
		})
	}
	rows, err := fixture.database.owner.Query(ctx, `EXPLAIN (ANALYZE,BUFFERS) SELECT event_hash FROM account_request_quality_events WHERE node_id=$1 AND account_key=$2 AND account_key IS NOT NULL AND occurred_at>=statement_timestamp()-interval '7 days' AND occurred_at<=statement_timestamp() ORDER BY occurred_at DESC,event_hash DESC LIMIT 26`, q.InstanceID, q.AccountKey)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		t.Log(line)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
