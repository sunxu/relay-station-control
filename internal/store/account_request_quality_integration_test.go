package store_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sunxu/relay-station-control/internal/requestquality"
	"github.com/sunxu/relay-station-control/internal/store"
)

func TestAccountRequestQualityRepositoryPostgres(t *testing.T) {
	db := newIsolatedJobDatabase(t)
	repo, err := store.NewAccountRequestQualityRepository(db.runtime)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	node := uuid.New()
	account := "openai:user@example.com"
	now := time.Now().UTC()
	old := now.Add(-8 * 24 * time.Hour)
	duration := func(ms int64) *int64 { return &ms }
	failedClass := "auth"
	events := []requestquality.Event{
		{EventHash: "same", NodeID: node, Provider: "openai", AccountKey: &account, Model: "gpt", OccurredAt: now.Add(-2 * time.Minute), DurationMS: duration(100), Success: true},
		{EventHash: "auth-failure", NodeID: node, Provider: "openai", AccountKey: &account, Model: "gpt", OccurredAt: now.Add(-3 * time.Minute), DurationMS: duration(200), Success: false, FailureClass: &failedClass},
		{EventHash: "unresolved", NodeID: node, Provider: "openai", Model: "gpt", OccurredAt: now.Add(-4 * time.Minute), DurationMS: duration(300), Success: false, FailureClass: &failedClass},
		{EventHash: "outside-window", NodeID: node, Provider: "openai", AccountKey: &account, OccurredAt: old, DurationMS: duration(900), Success: true},
	}
	if err := repo.InsertRequestEvents(ctx, events); err != nil {
		t.Fatal(err)
	}
	// Same event is safe across collector restart; same hash on another Node is distinct.
	if err := repo.InsertRequestEvents(ctx, []requestquality.Event{events[0], {EventHash: "same", NodeID: uuid.New(), Provider: "openai", OccurredAt: now, Success: true}}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.owner.QueryRow(ctx, `SELECT count(*) FROM account_request_quality_events`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Fatalf("event count=%d, want 5", count)
	}

	quality, err := repo.AccountQuality(ctx, node, account, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if quality.RequestCount != 2 || quality.SuccessCount != 1 || quality.FailureCount != 1 || quality.UnresolvedRequestCount != 0 {
		t.Fatalf("account quality=%+v", quality)
	}
	if quality.P95LatencyMS == nil || *quality.P95LatencyMS != 195 {
		t.Fatalf("p95=%v", quality.P95LatencyMS)
	}

	nodeQuality, err := repo.NodeProviderQuality(ctx, node, "openai", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if nodeQuality.RequestCount != 3 || nodeQuality.SuccessCount != 1 || nodeQuality.FailureCount != 2 || nodeQuality.UnresolvedRequestCount != 1 {
		t.Fatalf("node/provider quality=%+v", nodeQuality)
	}
	short, err := repo.NodeProviderQuality(ctx, node, "openai", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if short.RequestCount != 3 {
		t.Fatalf("15m request count=%d, want 3", short.RequestCount)
	}
	empty, err := repo.AccountQuality(ctx, node, "missing:account", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if empty.RequestCount != 0 || empty.SuccessRate != nil || empty.P95LatencyMS != nil {
		t.Fatalf("empty quality=%+v", empty)
	}

	deleted, err := repo.DeleteOldRequestEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("deleted=%d, want 1", deleted)
	}

	if _, err := db.runtime.Exec(ctx, `SELECT * FROM account_request_quality_events`); err == nil {
		t.Fatal("runtime unexpectedly has direct table access")
	}
	for _, statement := range []string{
		"UPDATE account_request_quality_events SET success=true",
		"DELETE FROM account_request_quality_events",
		"TRUNCATE account_request_quality_events",
	} {
		if _, err := db.runtime.Exec(ctx, statement); err == nil {
			t.Fatalf("runtime mutation allowed: %s", statement)
		}
	}
	var fixed int
	if err := db.owner.QueryRow(ctx, `SELECT count(*) FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner WHERE p.proname IN ('control_insert_account_request_quality_events_v1','control_delete_old_account_request_quality_events_v1','control_query_account_request_quality_v1','control_query_account_request_quality_targets_v1') AND p.prosecdef AND r.rolname='relay_control_migrator' AND p.proconfig @> ARRAY['search_path=pg_catalog']`).Scan(&fixed); err != nil || fixed != 4 {
		t.Fatalf("function access contract count=%d err=%v", fixed, err)
	}

}

func TestAccountRequestQualityRepositoryPostgresPerformance(t *testing.T) {
	db := newIsolatedJobDatabase(t)
	repo, err := store.NewAccountRequestQualityRepository(db.runtime)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	node := uuid.New()
	account := "openai:perf@example.com"
	now := time.Now().UTC()
	batch := make([]requestquality.Event, 1000)
	for i := range batch {
		key := account
		failed := "upstream"
		batch[i] = requestquality.Event{EventHash: fmt.Sprintf("perf-%06d", i), NodeID: node, Provider: "openai", AccountKey: &key, OccurredAt: now.Add(-time.Duration(i%900) * time.Second), DurationMS: func() *int64 { v := int64(i%1000 + 1); return &v }(), Success: i%10 != 0, FailureClass: func() *string {
			if i%10 == 0 {
				return &failed
			}
			return nil
		}()}
	}
	for page := 0; page < 100; page++ {
		pageBatch := make([]requestquality.Event, len(batch))
		copy(pageBatch, batch)
		for i := range pageBatch {
			pageBatch[i].EventHash = fmt.Sprintf("perf-%06d", page*len(pageBatch)+i)
		}
		if err := repo.InsertRequestEvents(ctx, pageBatch); err != nil {
			t.Fatal(err)
		}
	}
	var countRows float64
	if err := db.owner.QueryRow(ctx, `SELECT count(*) FROM account_request_quality_events WHERE node_id=$1`, node).Scan(&countRows); err != nil {
		t.Fatal(err)
	}
	if countRows != 100000 {
		t.Fatalf("rows=%v, want 100000", countRows)
	}
	for _, window := range []time.Duration{15 * time.Minute, time.Hour} {
		started := time.Now()
		quality, err := repo.AccountQuality(ctx, node, account, window)
		if err != nil {
			t.Fatal(err)
		}
		if quality.RequestCount == 0 || quality.SuccessRate == nil || quality.P95LatencyMS == nil {
			t.Fatalf("window %s quality=%+v", window, quality)
		}
		t.Logf("100k quality window=%s count=%d success_rate=%.4f p95=%.1f elapsed=%s", window, quality.RequestCount, *quality.SuccessRate, *quality.P95LatencyMS, time.Since(started))
	}
	planRows, err := db.owner.Query(ctx, `EXPLAIN (ANALYZE, FORMAT TEXT) SELECT event_hash FROM public.account_request_quality_events WHERE node_id=$1 AND account_key=$2 AND occurred_at >= statement_timestamp()-interval '1 hour' AND occurred_at <= statement_timestamp()`, node, account)
	if err != nil {
		t.Fatal(err)
	}
	var plan []string
	for planRows.Next() {
		var line string
		if err := planRows.Scan(&line); err != nil {
			planRows.Close()
			t.Fatal(err)
		}
		plan = append(plan, line)
	}
	planRows.Close()
	if len(plan) == 0 || !strings.Contains(strings.Join(plan, "\n"), "Index") {
		t.Fatalf("expected indexed plan, got %v", plan)
	}
	t.Logf("100k direct indexed EXPLAIN:\n%s", strings.Join(plan, "\n"))
}
