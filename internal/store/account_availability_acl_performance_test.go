package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	store "github.com/sunxu/relay-station-control/internal/store"
)

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
