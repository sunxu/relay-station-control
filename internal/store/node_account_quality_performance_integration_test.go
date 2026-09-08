package store_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	requestquality "github.com/sunxu/relay-station-control/internal/requestquality"
	productstore "github.com/sunxu/relay-station-control/internal/store"
)

type qualityQueryTracer struct{ count int }

func (t *qualityQueryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	t.count++
	return ctx
}
func (*qualityQueryTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestNodeAccountQualityPerformancePostgres(t *testing.T) {
	ctx := context.Background()
	accounts := make([]lifecycleAccount, 100)
	for i := range accounts {
		accounts[i] = lifecycleAccount{email: fmt.Sprintf("perf%03d@example.invalid", i)}
	}
	fixture := newReadonlyQueryFixture(t, ctx, accounts)
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
	t.Cleanup(func() { pool.Close() })
	repo, err := productstore.NewAccountRequestQualityRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	failed := "upstream"
	events := make([]requestquality.Event, 0, len(accounts)*100)
	now := time.Now().UTC()
	for i, account := range accounts {
		key := "openai:" + account.email
		for j := 0; j < 100; j++ {
			bad := i < 10
			var class *string
			if bad {
				class = &failed
			}
			events = append(events, requestquality.Event{EventHash: fmt.Sprintf("perf-%03d-%03d", i, j), NodeID: fixture.lifecycle.instanceID, Provider: "openai", AccountKey: &key, OccurredAt: now.Add(-time.Duration(j%30) * time.Second), DurationMS: int64Ptr(int64(20 + j)), Success: !bad, FailureClass: class})
		}
	}
	if err := repo.InsertRequestEvents(ctx, events); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name, provider, quality string
		window                  time.Duration
	}{
		{"all-15m", "", "", 15 * time.Minute}, {"all-1h", "", "", time.Hour},
		{"provider-15m", "openai", "", 15 * time.Minute}, {"provider-1h", "openai", "", time.Hour},
		{"quality-bad-15m", "", "bad", 15 * time.Minute}, {"quality-bad-1h", "", "bad", time.Hour},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := tracer.count
			started := time.Now()
			page, err := repo.ListAccountQuality(ctx, productstore.AccountQualityQuery{InstanceID: fixture.lifecycle.instanceID, Window: tc.window, Provider: tc.provider, Quality: tc.quality, Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			wantRows := 100
			if tc.quality == "bad" {
				wantRows = 10
			}
			if len(page.Items) != wantRows || page.HasMore {
				t.Fatalf("rows=%d more=%v, want %d final rows", len(page.Items), page.HasMore, wantRows)
			}
			for _, item := range page.Items {
				if item.Stats.RequestCount != 100 || (tc.quality != "" && item.Quality != tc.quality) {
					t.Fatalf("unexpected performance fixture result: %+v", item)
				}
			}
			if tracer.count-before != 1 {
				t.Fatalf("DB query count=%d, want 1", tracer.count-before)
			}
			if time.Since(started) >= time.Second {
				t.Fatalf("latency exceeded 1s: %s", time.Since(started))
			}
			t.Logf("accounts=%d events=%d window=%s provider=%q quality=%q rows=%d latency=%s db_queries=%d internal_quality_evaluations=%d (derived from returned inventory candidates)", len(accounts), len(events), tc.window, tc.provider, tc.quality, len(page.Items), time.Since(started), tracer.count-before, len(accounts))
		})
	}
}

func int64Ptr(value int64) *int64 { return &value }
