package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/sunxu/relay-station-control/internal/auth"
	"github.com/sunxu/relay-station-control/internal/requestquality"
	generated "github.com/sunxu/relay-station-control/internal/store/sqlc"
)

type AccountQualityQuery struct {
	InstanceID      uuid.UUID
	Window          time.Duration
	Provider        string
	Lifecycle       string
	BasicStatus     string
	Email           string
	Quality         string
	AfterAccountKey string
	Limit           int
}

type AccountQualityItem struct {
	AccountKey         string
	Email              string
	Provider           string
	Quality            string
	TokenState         *string
	ExpectedValidUntil *time.Time
	Stats              requestquality.Quality
	Inventory          AccountInventoryItem
	RecentRequests     []AccountRequestHistoryItem
}

type AccountQualityPage struct {
	Items   []AccountQualityItem
	HasMore bool
}

type AccountQualityViewAudit = AccountInventoryViewAudit
type accountQualityQueryExecutor interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

type accountQualityInventoryWire struct {
	InstanceID              uuid.UUID  `json:"instance_id"`
	Provider                string     `json:"provider"`
	Email                   string     `json:"email"`
	BasicStatus             string     `json:"basic_status"`
	Lifecycle               string     `json:"lifecycle"`
	ConsecutiveMissingCount int        `json:"consecutive_missing_count"`
	FirstSeenAt             time.Time  `json:"first_seen_at"`
	LastSeenAt              time.Time  `json:"last_seen_at"`
	MissingSince            *time.Time `json:"missing_since"`
	OutOfScopeSince         *time.Time `json:"out_of_scope_since"`
	LastRefreshAt           *time.Time `json:"last_refresh_at"`
	NextRetryAt             *time.Time `json:"next_retry_at"`
	SourceUpdatedAt         *time.Time `json:"source_updated_at"`
	ProviderLastCompleteAt  time.Time  `json:"provider_last_complete_at"`
	ProviderDegraded        bool       `json:"provider_degraded"`
	SnapshotFreshness       string     `json:"snapshot_freshness"`
}
type accountQualityRecentWire struct {
	EventHash    string    `json:"event_hash"`
	RequestID    string    `json:"request_id"`
	Model        string    `json:"model"`
	OccurredAt   time.Time `json:"occurred_at"`
	DurationMS   *int64    `json:"duration_ms"`
	Success      bool      `json:"success"`
	FailureClass *string   `json:"failure_class"`
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
	if query.Lifecycle != "" && query.Lifecycle != "present" && query.Lifecycle != "suspected_missing" && query.Lifecycle != "missing" && query.Lifecycle != "out_of_scope" {
		return AccountQualityPage{}, ErrInvalidAccountInventoryQuery
	}
	if query.BasicStatus != "" && query.BasicStatus != "reported_active" && query.BasicStatus != "disabled" && query.BasicStatus != "unavailable" && query.BasicStatus != "error" && query.BasicStatus != "unknown" {
		return AccountQualityPage{}, ErrInvalidAccountInventoryQuery
	}
	return r.listAccountQuality(ctx, r.pool, query)
}

func (r *AccountRequestQualityRepository) ListAccountQualityAndAudit(ctx context.Context, query AccountQualityQuery, audit AccountQualityViewAudit) (AccountQualityPage, error) {
	if r == nil || r.pool == nil {
		return AccountQualityPage{}, ErrAccountInventoryInconsistent
	}
	if !validAccountInventoryViewAudit(audit) || query.InstanceID == uuid.Nil || (query.Window != 15*time.Minute && query.Window != time.Hour) || query.Limit < 1 || query.Limit > 100 || (query.Quality != "" && query.Quality != "good" && query.Quality != "degraded" && query.Quality != "bad" && query.Quality != "unknown") || (query.Lifecycle != "" && query.Lifecycle != "present" && query.Lifecycle != "suspected_missing" && query.Lifecycle != "missing" && query.Lifecycle != "out_of_scope") || (query.BasicStatus != "" && query.BasicStatus != "reported_active" && query.BasicStatus != "disabled" && query.BasicStatus != "unavailable" && query.BasicStatus != "error" && query.BasicStatus != "unknown") {
		return AccountQualityPage{}, ErrInvalidAccountInventoryQuery
	}
	var page AccountQualityPage
	err := pgx.BeginTxFunc(ctx, r.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var err error
		page, err = r.listAccountQuality(ctx, tx, query)
		if err != nil {
			return err
		}
		details, err := auth.SanitizeAuditDetails(auth.AuditAccountInventoryView, map[string]any{"instance_id": query.InstanceID.String(), "provider_filter_used": query.Provider != "", "lifecycle_filter_used": query.Lifecycle != "", "basic_status_filter_used": query.BasicStatus != "", "email_filter_used": query.Email != "", "cursor_used": query.AfterAccountKey != "", "result_count": len(page.Items)})
		if err != nil {
			return ErrAccountInventoryInconsistent
		}
		category, err := auth.AuditCategoryFor(auth.AuditAccountInventoryView)
		if err != nil {
			return ErrAccountInventoryInconsistent
		}
		encoded, err := json.Marshal(details)
		if err != nil {
			return ErrAccountInventoryInconsistent
		}
		_, err = generated.New(tx).InsertAuditLog(ctx, generated.InsertAuditLogParams{AuditID: nullableUUID(uuid.New()), Category: string(category), Action: string(auth.AuditAccountInventoryView), Result: string(auth.AuditResultSuccess), ActorAdminID: nullableUUID(audit.ActorAdminID), SourceFingerprint: audit.SourceFingerprint, RequestID: audit.RequestID, Details: encoded})
		return err
	})
	if err != nil {
		return AccountQualityPage{}, err
	}
	return page, nil
}

func (r *AccountRequestQualityRepository) listAccountQuality(ctx context.Context, db accountQualityQueryExecutor, query AccountQualityQuery) (AccountQualityPage, error) {
	rows, err := db.Query(ctx, `SELECT account_key, normalized_email, provider, quality, request_count, success_count, failure_count, success_rate, p95_latency_ms, last_success_at, last_failure_at, last_failure_class, inventory, recent_requests, token_state, expected_valid_until FROM public.control_query_node_account_quality_v4($1,$2,$3,$4,$5,$6,$7,$8::interval,$9) ORDER BY account_key`, query.InstanceID, query.Provider, query.Lifecycle, query.BasicStatus, query.Email, query.Quality, query.AfterAccountKey, windowInterval(query.Window), query.Limit)
	if err != nil {
		return AccountQualityPage{}, accountInventoryDatabaseError(err)
	}
	defer rows.Close()
	page := AccountQualityPage{Items: make([]AccountQualityItem, 0, query.Limit)}
	for rows.Next() {
		var item AccountQualityItem
		var stats requestquality.Quality
		var inventoryJSON, recentJSON []byte
		if err := rows.Scan(&item.AccountKey, &item.Email, &item.Provider, &item.Quality, &stats.RequestCount, &stats.SuccessCount, &stats.FailureCount, &stats.SuccessRate, &stats.P95LatencyMS, &stats.LastSuccessAt, &stats.LastFailureAt, &stats.LastFailureClass, &inventoryJSON, &recentJSON, &item.TokenState, &item.ExpectedValidUntil); err != nil {
			return AccountQualityPage{}, err
		}
		var inventory accountQualityInventoryWire
		if err := json.Unmarshal(inventoryJSON, &inventory); err != nil {
			return AccountQualityPage{}, accountInventoryDatabaseError(err)
		}
		item.Inventory = AccountInventoryItem{InstanceID: inventory.InstanceID, Provider: inventory.Provider, Email: inventory.Email, BasicStatus: AccountInventoryBasicStatus(inventory.BasicStatus), Lifecycle: AccountInventoryLifecycle(inventory.Lifecycle), ConsecutiveMissingCount: inventory.ConsecutiveMissingCount, FirstSeenAt: inventory.FirstSeenAt, LastSeenAt: inventory.LastSeenAt, MissingSince: inventory.MissingSince, OutOfScopeSince: inventory.OutOfScopeSince, LastRefreshAt: inventory.LastRefreshAt, NextRetryAt: inventory.NextRetryAt, SourceUpdatedAt: inventory.SourceUpdatedAt, ProviderLastCompleteAt: inventory.ProviderLastCompleteAt, ProviderDegraded: inventory.ProviderDegraded, SnapshotFreshness: AccountInventorySnapshotFreshness(inventory.SnapshotFreshness)}
		var recent []accountQualityRecentWire
		if err := json.Unmarshal(recentJSON, &recent); err != nil {
			return AccountQualityPage{}, accountInventoryDatabaseError(err)
		}
		item.RecentRequests = make([]AccountRequestHistoryItem, 0, len(recent))
		for _, event := range recent {
			item.RecentRequests = append(item.RecentRequests, AccountRequestHistoryItem{EventHash: event.EventHash, RequestID: event.RequestID, Model: event.Model, OccurredAt: event.OccurredAt, DurationMS: event.DurationMS, Success: event.Success, FailureClass: event.FailureClass})
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
