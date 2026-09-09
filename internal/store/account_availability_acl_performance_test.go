package store_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	store "github.com/sunxu/relay-station-control/internal/store"
)

func TestAccountAvailabilityACLAndMigrationPostgres(t *testing.T) {
	f := newAvailabilityFixture(t, 1)
	ctx := context.Background()
	f.reconcile(t)
	for _, table := range []string{"account_availability_checkpoints", "account_availability_occurrences", "account_inventory_provider_states", "account_request_quality_events"} {
		if _, err := f.db.runtime.Exec(ctx, "SELECT * FROM "+table); err == nil {
			t.Fatalf("runtime direct SELECT %s allowed", table)
		}
	}
	var functions int
	if err := f.db.owner.QueryRow(ctx, `SELECT count(*) FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner WHERE p.proname IN ('control_query_account_availability_v1','control_query_account_availability_occurrences_v1','control_reconcile_account_availability_v1','control_insert_account_request_quality_events_v2','control_finalize_account_inventory_poll_run_v2','control_finalize_account_inventory_poll_run_with_lifecycle_v2') AND p.prosecdef AND p.proconfig @> ARRAY['search_path=pg_catalog'] AND r.rolname='relay_control_migrator' AND NOT EXISTS(SELECT 1 FROM aclexplode(p.proacl) a WHERE a.grantee=0 AND a.privilege_type='EXECUTE')`).Scan(&functions); err != nil || functions != 6 {
		t.Fatalf("secured functions=%d err=%v", functions, err)
	}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`SELECT public.control_query_account_availability_v1($1,NULL)`, []any{f.node}},
		{`SELECT public.control_query_account_availability_v1($1,$2)`, []any{f.node, make([]string, 101)}},
		{`SELECT public.control_query_account_availability_occurrences_v1($1,NULL,'other',NULL,NULL,25)`, []any{f.node}},
		{`SELECT public.control_query_account_availability_occurrences_v1($1,NULL,'ACTIVE',NULL,NULL,101)`, []any{f.node}},
	} {
		if _, err := f.db.runtime.Exec(ctx, q.sql, q.args...); err == nil {
			t.Fatal("unbounded/invalid query accepted")
		}
	}
	if _, err := f.repo.ListAccountAvailabilityOccurrences(ctx, store.AccountAvailabilityOccurrenceQuery{InstanceID: uuid.New(), Status: "ACTIVE", Limit: 25}); err != store.ErrAccountInventoryInstanceNotFound {
		t.Fatalf("unknown Node=%v", err)
	}
	// Old event writer still records NULL, replay through v2 does not backfill it.
	f.event(t, 0, "r1", "new", "token_invalid", f.now.Add(-time.Minute))
	raw := fmt.Sprintf(`[{"event_hash":"legacy","node_id":"%s","provider":"antigravity","account_key":"%s","occurred_at":"%s","success":false,"failure_class":"auth"}]`, f.node, f.keys[0], f.now.Format(time.RFC3339Nano))
	if _, err := f.db.runtime.Exec(ctx, `SELECT public.control_insert_account_request_quality_events_v1($1::jsonb)`, raw); err != nil {
		t.Fatal(err)
	}
	f.event(t, 0, "legacy-r", "legacy", "account_blocked", f.now)
	var reason *string
	if err := f.db.owner.QueryRow(ctx, `SELECT auth_failure_reason FROM account_request_quality_events WHERE event_hash='legacy'`).Scan(&reason); err != nil || reason != nil {
		t.Fatalf("legacy backfilled=%v err=%v", reason, err)
	}
	var count int
	if err := runAssetGoose(t, ctx, "../..", f.db.ownerURL, "down"); err != nil {
		t.Fatal(err)
	}
	if err := f.db.owner.QueryRow(ctx, `SELECT count(*) FROM account_inventory WHERE instance_id=$1`, f.node).Scan(&count); err != nil || count != 1 {
		t.Fatalf("Down lost inventory=%d %v", count, err)
	}
	if err := f.db.owner.QueryRow(ctx, `SELECT count(*) FROM account_request_quality_events WHERE node_id=$1`, f.node).Scan(&count); err != nil || count != 2 {
		t.Fatalf("Down lost events=%d %v", count, err)
	}
	if err := runAssetGoose(t, ctx, "../..", f.db.ownerURL, "up"); err != nil {
		t.Fatal(err)
	}
	if err := f.db.owner.QueryRow(ctx, `SELECT count(*) FROM account_request_quality_events WHERE auth_failure_reason IS NOT NULL`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("Up backfilled=%d %v", count, err)
	}
	f.reconcile(t)
	f.state(t, 0, "UNKNOWN")
}

func TestAccountAvailabilityBatchPerformanceAndPaginationPostgres(t *testing.T) {
	f := newAvailabilityFixture(t, 110)
	ctx := context.Background()
	if _, err := f.db.owner.Exec(ctx, `INSERT INTO account_request_quality_events(event_hash,request_id,node_id,provider,account_key,occurred_at,duration_ms,success,failure_class,auth_failure_reason)
 SELECT 'perf-'||g,'request-'||g,$1,'antigravity','antigravity:account'||lpad(((g-1)%100)::text,3,'0')||'@example.invalid',statement_timestamp()-interval '1 minute',100,false,'auth','token_invalid' FROM generate_series(1,10000)g`, f.node); err != nil {
		t.Fatal(err)
	}
	// A qualifying account beyond the first 100 must not starve.
	f.event(t, 109, "last1", "last-h1", "account_blocked", f.now.Add(-30*time.Second))
	f.event(t, 109, "last2", "last-h2", "account_blocked", f.now.Add(-20*time.Second))
	start := time.Now()
	f.reconcile(t)
	t.Logf("110 accounts / 10002 events reconcile: %s; two bounded pages", time.Since(start))
	start = time.Now()
	values, err := f.repo.BatchAccountAvailability(ctx, f.node, f.keys[:100])
	elapsed := time.Since(start)
	if err != nil || len(values) != 100 {
		t.Fatalf("batch=%d err=%v", len(values), err)
	}
	t.Logf("100-account batch availability: %s, one SQL statement", elapsed)
	if elapsed > 5*time.Second {
		t.Fatal("batch exceeded API budget")
	}
	f.state(t, 109, "ACCOUNT_BLOCKED")
	var items []store.AccountAvailabilityOccurrence
	q := store.AccountAvailabilityOccurrenceQuery{InstanceID: f.node, Status: "ACTIVE", Limit: 25}
	for page := 0; page < 5; page++ {
		start = time.Now()
		p, err := f.repo.ListAccountAvailabilityOccurrences(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("occurrence page %d: %s, one SQL statement", page, time.Since(start))
		items = append(items, p.Items...)
		if !p.HasMore {
			break
		}
		last := p.Items[len(p.Items)-1]
		q.AfterConfirmedAt = &last.ConfirmedAt
		q.AfterOccurrenceID = last.OccurrenceID
	}
	if len(items) != 101 {
		t.Fatalf("paginated count=%d", len(items))
	}
	seen := map[uuid.UUID]bool{}
	for i, item := range items {
		if seen[item.OccurrenceID] {
			t.Fatal("pagination duplicate")
		}
		seen[item.OccurrenceID] = true
		if i > 0 && item.ConfirmedAt.After(items[i-1].ConfirmedAt) {
			t.Fatal("pagination order")
		}
	}
}

func TestAccountAvailabilityNodeIsolationPostgres(t *testing.T) {
	f := newAvailabilityFixture(t, 1)
	ctx := context.Background()
	other := uuid.New()
	tx, err := f.db.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO relay_node_assets(instance_id,display_name,node_type,driver_contract_version,management_endpoint,reader_secret_ref) SELECT $2,'Other availability Node',node_type,driver_contract_version,management_endpoint,reader_secret_ref FROM relay_node_assets WHERE instance_id=$1`, f.node, other); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `ALTER TABLE account_inventory DISABLE TRIGGER USER;ALTER TABLE account_inventory_provider_states DISABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"account_inventory", "account_inventory_provider_states"} {
		if _, err = tx.Exec(ctx, `INSERT INTO `+table+` SELECT (jsonb_populate_record(NULL::`+table+`,to_jsonb(i)||jsonb_build_object('instance_id',$2::uuid,'current_poll_run_id',NULL))).* FROM `+table+` i WHERE instance_id=$1`, f.node, other); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec(ctx, `ALTER TABLE account_inventory ENABLE TRIGGER USER;ALTER TABLE account_inventory_provider_states ENABLE TRIGGER USER;`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO relay_node_inventory_monitoring_activations(instance_id,effective_from,created_at,reason,actor) VALUES($1,clock_timestamp()-interval '1 hour',clock_timestamp()-interval '2 hours','reconciliation','availability-test')`, other); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	f.event(t, 0, "r1", "n1", "token_invalid", f.now.Add(-2*time.Minute))
	f.event(t, 0, "r2", "n2", "token_invalid", f.now.Add(-time.Minute))
	if n, err := f.repo.Reconcile(ctx); err != nil || n != 2 {
		t.Fatalf("reconcile=%d %v", n, err)
	}
	f.state(t, 0, "TOKEN_INVALID")
	values, err := f.repo.BatchAccountAvailability(ctx, other, f.keys)
	if err != nil || values[f.keys[0]].State != "AVAILABLE" {
		t.Fatalf("Node B contamination=%+v %v", values, err)
	}
	p, err := f.repo.ListAccountAvailabilityOccurrences(ctx, store.AccountAvailabilityOccurrenceQuery{InstanceID: other, Limit: 25, Status: "ACTIVE"})
	if err != nil || len(p.Items) != 0 {
		t.Fatalf("Node B occurrences=%+v %v", p, err)
	}
}
