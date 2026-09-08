package store_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	requestquality "github.com/sunxu/relay-station-control/internal/requestquality"
	store "github.com/sunxu/relay-station-control/internal/store"
)

func TestNodeAccountQualityUnifiedMigrationPostgres(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	database := newIsolatedJobDatabase(t, "up-to", "24")
	functionName := "public.control_query_node_account_quality_v3(uuid,text,text,text,text,text,text,interval,integer)"
	if _, err := database.owner.Exec(ctx, "SELECT 1"); err != nil {
		t.Fatal(err)
	}
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "up"); err != nil {
		t.Fatalf("apply v3 migration: %v", err)
	}
	var owner, volatility string
	var securityDefiner, publicExecute, runtimeExecute bool
	var searchPath []string
	if err := database.owner.QueryRow(ctx, `
		SELECT pg_get_userbyid(p.proowner), p.provolatile, p.prosecdef,
		       p.proconfig,
		       has_function_privilege('public', $1, 'EXECUTE')
		FROM pg_proc p WHERE p.oid=$1::regprocedure`, functionName).
		Scan(&owner, &volatility, &securityDefiner, &searchPath, &publicExecute); err != nil {
		t.Fatal(err)
	}
	// The runtime privilege is checked separately because current_user above is
	// the migrator connection used for catalog inspection.
	if err := database.runtime.QueryRow(ctx, `SELECT has_function_privilege(current_user, $1, 'EXECUTE')`, functionName).Scan(&runtimeExecute); err != nil {
		t.Fatal(err)
	}
	if owner != "relay_control_migrator" || volatility != "s" || !securityDefiner || publicExecute || !runtimeExecute {
		t.Fatalf("v3 ACL/security mismatch: owner=%q volatility=%q definer=%v public=%v runtime=%v", owner, volatility, securityDefiner, publicExecute, runtimeExecute)
	}
	if len(searchPath) != 1 || searchPath[0] != "search_path=pg_catalog" {
		t.Fatalf("v3 search_path=%v, want [search_path=pg_catalog]", searchPath)
	}

	// A pre-existing event is the data-preservation sentinel across Down/Up.
	nodeID := uuid.New()
	accountKey := "openai:migration@example.invalid"
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_request_quality_events(event_hash,node_id,provider,account_key,occurred_at,success) VALUES ('migration-sentinel',$1,'openai',$2,clock_timestamp(),true)`, nodeID, accountKey); err != nil {
		t.Fatal(err)
	}
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "down"); err != nil {
		t.Fatalf("remove v3 migration: %v", err)
	}
	var v3, v2 bool
	if err := database.owner.QueryRow(ctx, `SELECT to_regprocedure($1) IS NOT NULL, to_regprocedure($2) IS NOT NULL`, functionName, "public.control_query_node_account_quality_v2(uuid,text,text,text,text,interval,integer)").Scan(&v3, &v2); err != nil {
		t.Fatal(err)
	}
	if v3 || !v2 {
		t.Fatalf("down left wrong function set: v3=%v v2=%v", v3, v2)
	}
	var events int
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM account_request_quality_events WHERE event_hash='migration-sentinel'`).Scan(&events); err != nil || events != 1 {
		t.Fatalf("down damaged pre-existing event: count=%d err=%v", events, err)
	}
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "up"); err != nil {
		t.Fatalf("reapply v3 migration: %v", err)
	}
	if err := database.owner.QueryRow(ctx, `SELECT to_regprocedure($1) IS NOT NULL`, functionName).Scan(&v3); err != nil || !v3 {
		t.Fatalf("up did not restore v3: exists=%v err=%v", v3, err)
	}
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM account_request_quality_events WHERE event_hash='migration-sentinel'`).Scan(&events); err != nil || events != 1 {
		t.Fatalf("up did not preserve pre-existing event: count=%d err=%v", events, err)
	}
}

func TestNodeAccountQualityUnifiedAuditPostgres(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture := newReadonlyQueryFixture(t, ctx, []lifecycleAccount{{email: "audit-a@example.invalid"}, {email: "audit-b@example.invalid"}})
	repo, err := store.NewAccountRequestQualityRepository(fixture.database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	node := fixture.lifecycle.instanceID
	key := "openai:audit-a@example.invalid"
	if err := repo.InsertRequestEvents(ctx, []requestquality.Event{{EventHash: "audit-event", NodeID: node, Provider: "openai", AccountKey: &key, OccurredAt: time.Now().UTC(), Success: true}}); err != nil {
		t.Fatal(err)
	}
	audit := store.AccountQualityViewAudit{ActorAdminID: fixture.audit.ActorAdminID, SourceFingerprint: fixture.audit.SourceFingerprint, RequestID: "quality-audit-1"}
	query := store.AccountQualityQuery{InstanceID: node, Window: time.Hour, Limit: 1}
	_, plainErr := repo.ListAccountQuality(ctx, query)
	if plainErr != nil {
		t.Fatalf("plain quality preflight: %v", plainErr)
	}
	first, err := repo.ListAccountQualityAndAudit(ctx, query, audit)
	if err != nil || len(first.Items) != 1 || !first.HasMore {
		t.Fatalf("first audited page=%d more=%v err=%v", len(first.Items), first.HasMore, err)
	}
	var auditRows int
	if err := fixture.database.owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE request_id='quality-audit-1' AND action='account_inventory.view'`).Scan(&auditRows); err != nil || auditRows != 1 {
		t.Fatalf("first page audit rows=%d err=%v", auditRows, err)
	}
	var details []byte
	if err := fixture.database.owner.QueryRow(ctx, `SELECT details FROM audit_logs WHERE request_id='quality-audit-1'`).Scan(&details); err != nil {
		t.Fatal(err)
	}
	detailText := string(details)
	for _, forbidden := range []string{"audit-a@example.invalid", "openai:audit-a@example.invalid", "filter_value", "filter_hash"} {
		if strings.Contains(detailText, forbidden) {
			t.Fatalf("audit details leaked %q: %s", forbidden, detailText)
		}
	}
	var detailMap map[string]any
	if err := json.Unmarshal(details, &detailMap); err != nil {
		t.Fatal(err)
	}
	allowedKeys := map[string]bool{"instance_id": true, "provider_filter_used": true, "lifecycle_filter_used": true, "basic_status_filter_used": true, "email_filter_used": true, "quality_filter_used": true, "window": true, "cursor_used": true, "result_count": true}
	for key := range detailMap {
		if !allowedKeys[key] {
			t.Fatalf("unexpected audit detail key %q", key)
		}
	}
	secondAudit := audit
	secondAudit.RequestID = "quality-audit-2"
	second, err := repo.ListAccountQualityAndAudit(ctx, store.AccountQualityQuery{InstanceID: node, Window: time.Hour, AfterAccountKey: first.Items[0].AccountKey, Limit: 1}, secondAudit)
	if err != nil || len(second.Items) != 1 || second.HasMore {
		t.Fatalf("second audited page=%d more=%v err=%v", len(second.Items), second.HasMore, err)
	}
	emptyAudit := audit
	emptyAudit.RequestID = "quality-audit-empty"
	empty, err := repo.ListAccountQualityAndAudit(ctx, store.AccountQualityQuery{InstanceID: node, Window: time.Hour, Quality: "bad", Limit: 1}, emptyAudit)
	if err != nil || len(empty.Items) != 0 || empty.HasMore {
		t.Fatalf("empty audited page=%d more=%v err=%v", len(empty.Items), empty.HasMore, err)
	}
	if err := fixture.database.owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action='account_inventory.view' AND request_id IN ('quality-audit-1','quality-audit-2','quality-audit-empty')`).Scan(&auditRows); err != nil || auditRows != 3 {
		t.Fatalf("page audit count=%d err=%v", auditRows, err)
	}

	if _, err := fixture.database.owner.Exec(ctx, `CREATE FUNCTION public.test_quality_audit_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'quality audit sentinel'; END; $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.owner.Exec(ctx, `CREATE TRIGGER test_quality_audit_failure BEFORE INSERT ON audit_logs FOR EACH ROW WHEN (NEW.request_id = 'quality-audit-fail') EXECUTE FUNCTION public.test_quality_audit_failure()`); err != nil {
		t.Fatal(err)
	}
	failingAudit := audit
	failingAudit.RequestID = "quality-audit-fail"
	failing, err := repo.ListAccountQualityAndAudit(ctx, query, failingAudit)
	if err == nil || len(failing.Items) != 0 {
		t.Fatalf("audit failure returned data: items=%d err=%v", len(failing.Items), err)
	}
	if _, err := fixture.database.owner.Exec(ctx, `DROP TRIGGER test_quality_audit_failure ON audit_logs; DROP FUNCTION public.test_quality_audit_failure()`); err != nil {
		t.Fatal(err)
	}
	var failedAuditRows int
	if err := fixture.database.owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE request_id='quality-audit-fail'`).Scan(&failedAuditRows); err != nil || failedAuditRows != 0 {
		t.Fatalf("failed audit committed rows=%d err=%v", failedAuditRows, err)
	}
}

// This test exercises the v3 composition at the persistence boundary.  In
// particular, recent_requests is evidence only: it must not change the
// inventory-driven account set or the existing quality aggregation.
func TestNodeAccountQualityUnifiedReadModelPostgres(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	accounts := []lifecycleAccount{
		{email: "recent@example.invalid"},
		{email: "empty@example.invalid"},
		{email: "other@example.invalid"},
	}
	fixture := newReadonlyQueryFixture(t, ctx, accounts)
	repo, err := store.NewAccountRequestQualityRepository(fixture.database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	key := "openai:recent@example.invalid"
	failure := "auth"
	events := make([]requestquality.Event, 0, 16)
	// Give each tied event a distinct model so the returned order proves the
	// event_hash tie-breaker without exposing event_hash in the read contract.
	tieTime := now.Add(-30 * time.Minute)
	for i := 0; i < 12; i++ {
		d := int64(i + 1)
		e := requestquality.Event{
			EventHash: fmt.Sprintf("tie-%02d", i), RequestID: fmt.Sprintf("req-%02d", i),
			NodeID: fixture.lifecycle.instanceID, Provider: "openai", AccountKey: &key,
			Model: fmt.Sprintf("model-%02d", i), OccurredAt: tieTime,
			DurationMS: &d, Success: i%2 == 0,
		}
		if !e.Success {
			e.FailureClass = &failure
		}
		events = append(events, e)
	}
	// The seven-day boundary and future events must not enter recent_requests.
	events = append(events,
		requestquality.Event{EventHash: "old", NodeID: fixture.lifecycle.instanceID, Provider: "openai", AccountKey: &key, OccurredAt: now.Add(-8 * 24 * time.Hour), Success: true},
		requestquality.Event{EventHash: "future", NodeID: fixture.lifecycle.instanceID, Provider: "openai", AccountKey: &key, OccurredAt: now.Add(2 * time.Hour), Success: true},
		requestquality.Event{EventHash: "unresolved", NodeID: fixture.lifecycle.instanceID, Provider: "openai", OccurredAt: now.Add(-time.Minute), Success: false, FailureClass: &failure},
		requestquality.Event{EventHash: "event-only", NodeID: fixture.lifecycle.instanceID, Provider: "openai", AccountKey: stringPtr("openai:ghost@example.invalid"), OccurredAt: now.Add(-time.Minute), Success: false, FailureClass: &failure},
	)
	if err := repo.InsertRequestEvents(ctx, events); err != nil {
		t.Fatal(err)
	}
	page, err := repo.ListAccountQuality(ctx, store.AccountQualityQuery{
		InstanceID: fixture.lifecycle.instanceID, Window: time.Hour, Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != len(accounts) || page.HasMore {
		t.Fatalf("inventory-driven page=%d more=%v", len(page.Items), page.HasMore)
	}
	if page.Items[0].AccountKey != "openai:empty@example.invalid" || page.Items[2].AccountKey != key {
		t.Fatalf("unexpected stable account ordering: %+v", page.Items)
	}
	empty := page.Items[0]
	if empty.Quality != "unknown" || empty.Stats.RequestCount != 0 || empty.Stats.SuccessRate != nil || len(empty.RecentRequests) != 0 {
		t.Fatalf("empty account was not unknown/empty: %+v", empty)
	}
	recent := page.Items[2]
	if len(recent.RecentRequests) != 10 {
		t.Fatalf("recent request bound=%d, want 10", len(recent.RecentRequests))
	}
	for i, event := range recent.RecentRequests {
		wantModel := fmt.Sprintf("model-%02d", 11-i)
		if event.Model != wantModel {
			t.Fatalf("recent[%d].model=%q, want %q (timestamp/hash order)", i, event.Model, wantModel)
		}
	}
	if recent.Stats.RequestCount != 12 || recent.Stats.SuccessCount != 6 || recent.Stats.FailureCount != 6 {
		t.Fatalf("quality aggregate contaminated or wrong: %+v", recent.Stats)
	}
	if len(page.Items[1].RecentRequests) != 0 {
		t.Fatalf("unrelated account received recent events: %+v", page.Items[1])
	}
	filtered, err := repo.ListAccountQuality(ctx, store.AccountQualityQuery{
		InstanceID: fixture.lifecycle.instanceID, Window: time.Hour, Provider: "openai", Quality: "good", Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Empty is intentionally excluded from a good filter; this guards the
	// quality-filter path without accepting an event-only row.
	if len(filtered.Items) != 0 || filtered.HasMore {
		t.Fatalf("quality filter leaked rows: %+v", filtered)
	}

	var directCount int
	if err := fixture.database.runtime.QueryRow(ctx, `SELECT count(*) FROM public.account_request_quality_events`).Scan(&directCount); err == nil {
		t.Fatal("runtime unexpectedly has direct SELECT on event table")
	}
	if err := fixture.database.runtime.QueryRow(ctx, `SELECT count(*) FROM public.account_inventory`).Scan(&directCount); err == nil {
		t.Fatal("runtime unexpectedly has direct SELECT on inventory table")
	}
	if recent.Inventory.InstanceID == fixture.lifecycle.instanceID && recent.Inventory.Provider != "openai" {
		t.Fatalf("inventory metadata mismatch: %+v", recent.Inventory)
	}
	if recent.Inventory.LastSeenAt.IsZero() || recent.Inventory.ProviderLastCompleteAt.IsZero() || recent.RecentRequests[0].RequestID != "req-11" || recent.RecentRequests[0].DurationMS == nil {
		t.Fatalf("unreliable inventory/recent metadata: inventory=%+v recent=%+v", recent.Inventory, recent.RecentRequests[0])
	}
	var functionExists bool
	if err := fixture.database.runtime.QueryRow(ctx, `SELECT to_regprocedure('public.control_query_node_account_quality_v3(uuid,text,text,text,text,text,text,interval,integer)') IS NOT NULL`).Scan(&functionExists); err != nil || !functionExists {
		t.Fatalf("v3 readonly function unavailable: exists=%v err=%v", functionExists, err)
	}
}

func TestNodeAccountQualityUnifiedPerformancePostgres(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	accounts := make([]lifecycleAccount, 100)
	for i := range accounts {
		accounts[i] = lifecycleAccount{email: fmt.Sprintf("perf-%03d@example.invalid", i)}
	}
	fixture := newReadonlyQueryFixture(t, ctx, accounts)
	repo, err := store.NewAccountRequestQualityRepository(fixture.database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(-time.Minute)
	events := make([]requestquality.Event, 0, 10000)
	for accountIndex := range accounts {
		key := "openai:" + accounts[accountIndex].email
		failure := "auth"
		for eventIndex := 0; eventIndex < 100; eventIndex++ {
			duration := int64(eventIndex + 1)
			events = append(events, requestquality.Event{
				EventHash: fmt.Sprintf("perf-%03d-%03d", accountIndex, eventIndex),
				RequestID: fmt.Sprintf("perf-request-%03d-%03d", accountIndex, eventIndex),
				NodeID:    fixture.lifecycle.instanceID, Provider: "openai", AccountKey: &key,
				Model: "perf-model", OccurredAt: now, DurationMS: &duration, Success: eventIndex != 0,
			})
			if eventIndex == 0 {
				events[len(events)-1].FailureClass = &failure
			}
		}
	}
	if err := repo.InsertRequestEvents(ctx, events); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	page, err := repo.ListAccountQuality(ctx, store.AccountQualityQuery{InstanceID: fixture.lifecycle.instanceID, Window: 15 * time.Minute, Limit: 25})
	latency := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 25 || !page.HasMore {
		t.Fatalf("performance first page=%d more=%v", len(page.Items), page.HasMore)
	}
	if latency > 5*time.Second {
		t.Fatalf("100-account first page exceeded local budget: %s", latency)
	}

	for _, query := range []store.AccountQualityQuery{
		{InstanceID: fixture.lifecycle.instanceID, Window: time.Hour, Provider: "openai", Limit: 25},
		{InstanceID: fixture.lifecycle.instanceID, Window: 15 * time.Minute, Provider: "openai", Quality: "good", Limit: 25},
	} {
		filtered, queryErr := repo.ListAccountQuality(ctx, query)
		if queryErr != nil || len(filtered.Items) == 0 {
			t.Fatalf("filtered performance query returned %d: %v", len(filtered.Items), queryErr)
		}
	}
	// A second page must continue strictly after the first page's stable key.
	second, err := repo.ListAccountQuality(ctx, store.AccountQualityQuery{
		InstanceID: fixture.lifecycle.instanceID, Window: 15 * time.Minute, Limit: 25,
		AfterAccountKey: page.Items[len(page.Items)-1].AccountKey,
	})
	if err != nil || len(second.Items) != 25 || second.Items[0].AccountKey <= page.Items[len(page.Items)-1].AccountKey {
		t.Fatalf("keyset page failed: len=%d first=%q err=%v", len(second.Items), second.Items[0].AccountKey, err)
	}
	// Events for another Node are not part of this Node's quality or recent
	// request projection, even when the account key is identical.
	otherNode := uuid.New()
	otherKey := "openai:perf-000@example.invalid"
	failure := "auth"
	if err := repo.InsertRequestEvents(ctx, []requestquality.Event{{EventHash: "other-node", RequestID: "other-node", NodeID: otherNode, Provider: "openai", AccountKey: &otherKey, OccurredAt: now, Success: false, FailureClass: &failure}}); err != nil {
		t.Fatal(err)
	}
	isolated, err := repo.ListAccountQuality(ctx, store.AccountQualityQuery{InstanceID: fixture.lifecycle.instanceID, Window: time.Hour, Limit: 1})
	if err != nil || isolated.Items[0].Stats.RequestCount != 100 {
		t.Fatalf("other Node contaminated quality: %+v err=%v", isolated.Items[0], err)
	}
	t.Logf("100 accounts/10000 events first-page latency=%s query=single store call", latency)
}
