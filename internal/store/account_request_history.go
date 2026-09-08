package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type AccountRequestHistoryQuery struct {
	InstanceID      uuid.UUID
	AccountKey      string
	AfterOccurredAt *time.Time
	AfterEventHash  string
	Limit           int
}

type AccountRequestHistoryItem struct {
	EventHash    string
	RequestID    string
	Model        string
	OccurredAt   time.Time
	DurationMS   *int64
	Success      bool
	FailureClass *string
}

type AccountRequestHistoryPage struct {
	Items   []AccountRequestHistoryItem
	HasMore bool
}

func (r *AccountRequestQualityRepository) ListAccountRequestHistory(ctx context.Context, query AccountRequestHistoryQuery) (AccountRequestHistoryPage, error) {
	if r == nil || r.pool == nil || query.InstanceID == uuid.Nil || query.AccountKey == "" || query.Limit < 1 || query.Limit > 100 {
		return AccountRequestHistoryPage{}, ErrInvalidAccountInventoryQuery
	}
	if (query.AfterOccurredAt == nil) != (query.AfterEventHash == "") {
		return AccountRequestHistoryPage{}, ErrInvalidAccountInventoryQuery
	}
	rows, err := r.pool.Query(ctx, `SELECT event_hash, request_id, model, occurred_at, duration_ms, success, failure_class FROM public.control_query_account_request_history_v1($1,$2,$3,$4,$5) ORDER BY occurred_at DESC, event_hash DESC`, query.InstanceID, query.AccountKey, query.AfterOccurredAt, nullableHistoryHash(query.AfterEventHash), query.Limit)
	if err != nil {
		return AccountRequestHistoryPage{}, accountInventoryDatabaseError(err)
	}
	defer rows.Close()
	page := AccountRequestHistoryPage{Items: make([]AccountRequestHistoryItem, 0, query.Limit)}
	for rows.Next() {
		var item AccountRequestHistoryItem
		if err := rows.Scan(&item.EventHash, &item.RequestID, &item.Model, &item.OccurredAt, &item.DurationMS, &item.Success, &item.FailureClass); err != nil {
			return AccountRequestHistoryPage{}, accountInventoryDatabaseError(err)
		}
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return AccountRequestHistoryPage{}, accountInventoryDatabaseError(err)
	}
	page.HasMore = len(page.Items) > query.Limit
	if page.HasMore {
		page.Items = page.Items[:query.Limit]
	}
	return page, nil
}

func nullableHistoryHash(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
