package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/sunxu/relay-station-control/internal/requestquality"
)

type AccountQualityQuery struct {
	InstanceID      uuid.UUID
	Window          time.Duration
	Provider        string
	Quality         string
	AfterAccountKey string
	Limit           int
}

type AccountQualityItem struct {
	AccountKey string
	Email      string
	Provider   string
	Quality    string
	Stats      requestquality.Quality
}

type AccountQualityPage struct {
	Items   []AccountQualityItem
	HasMore bool
}

func (r *AccountRequestQualityRepository) ListAccountQuality(ctx context.Context, query AccountQualityQuery) (AccountQualityPage, error) {
	if r == nil || r.pool == nil {
		return AccountQualityPage{}, ErrAccountInventoryInconsistent
	}
	if query.InstanceID == uuid.Nil || (query.Window != 15*time.Minute && query.Window != time.Hour) || query.Limit < 1 || query.Limit > 100 {
		return AccountQualityPage{}, ErrInvalidAccountInventoryQuery
	}
	if query.Quality != "" && query.Quality != "good" && query.Quality != "degraded" && query.Quality != "bad" && query.Quality != "unknown" {
		return AccountQualityPage{}, ErrInvalidAccountInventoryQuery
	}
	rows, err := r.pool.Query(ctx, `SELECT account_key, normalized_email, provider, quality, request_count, success_count, failure_count, success_rate, p95_latency_ms, last_success_at, last_failure_at, last_failure_class FROM public.control_query_node_account_quality_v1($1,$2,$3,$4,$5::interval,$6) ORDER BY account_key`, query.InstanceID, query.Provider, query.Quality, query.AfterAccountKey, windowInterval(query.Window), query.Limit)
	if err != nil {
		return AccountQualityPage{}, accountInventoryDatabaseError(err)
	}
	defer rows.Close()
	page := AccountQualityPage{Items: make([]AccountQualityItem, 0, query.Limit)}
	for rows.Next() {
		var item AccountQualityItem
		var stats requestquality.Quality
		if err := rows.Scan(&item.AccountKey, &item.Email, &item.Provider, &item.Quality, &stats.RequestCount, &stats.SuccessCount, &stats.FailureCount, &stats.SuccessRate, &stats.P95LatencyMS, &stats.LastSuccessAt, &stats.LastFailureAt, &stats.LastFailureClass); err != nil {
			return AccountQualityPage{}, err
		}
		item.Stats = stats
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return AccountQualityPage{}, accountInventoryDatabaseError(err)
	}
	page.HasMore = len(page.Items) > query.Limit
	if page.HasMore {
		page.Items = page.Items[:query.Limit]
	}
	return page, nil
}

func windowInterval(window time.Duration) string {
	if window == time.Hour {
		return "1 hour"
	}
	return "15 minutes"
}
