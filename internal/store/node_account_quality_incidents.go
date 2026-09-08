package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type AccountQualityIncidentQuery struct {
	InstanceID        uuid.UUID
	Provider          string
	FailureClass      string
	AfterLastSeen     *time.Time
	AfterAccountKey   string
	AfterFailureClass string
	Limit             int
}

type AccountQualityIncident struct {
	NodeID        uuid.UUID
	AccountKey    string
	Provider      string
	FailureClass  string
	Status        string
	FirstSeen     time.Time
	LastSeen      time.Time
	HitCount      int64
	LastSuccessAt *time.Time
}

type AccountQualityIncidentPage struct {
	Items   []AccountQualityIncident
	HasMore bool
}

func (r *AccountRequestQualityRepository) ListAccountQualityIncidents(ctx context.Context, query AccountQualityIncidentQuery) (AccountQualityIncidentPage, error) {
	if r == nil || r.pool == nil {
		return AccountQualityIncidentPage{}, ErrAccountInventoryInconsistent
	}
	if query.InstanceID == uuid.Nil || query.Limit < 1 || query.Limit > 100 || (query.AfterLastSeen == nil && (query.AfterAccountKey != "" || query.AfterFailureClass != "")) || (query.AfterLastSeen != nil && (query.AfterAccountKey == "" || query.AfterFailureClass == "")) {
		return AccountQualityIncidentPage{}, ErrInvalidAccountInventoryQuery
	}
	rows, err := r.pool.Query(ctx, `SELECT node_id, account_key, provider, failure_class, status, first_seen, last_seen, hit_count, last_success_at FROM public.control_query_node_account_quality_incidents_v1($1,$2,$3,$4,$5,$6,$7) ORDER BY last_seen DESC, account_key ASC, failure_class ASC`, query.InstanceID, query.Provider, query.FailureClass, query.AfterLastSeen, query.AfterAccountKey, query.AfterFailureClass, query.Limit)
	if err != nil {
		return AccountQualityIncidentPage{}, accountInventoryDatabaseError(err)
	}
	defer rows.Close()
	page := AccountQualityIncidentPage{Items: make([]AccountQualityIncident, 0, query.Limit)}
	for rows.Next() {
		var item AccountQualityIncident
		if err := rows.Scan(&item.NodeID, &item.AccountKey, &item.Provider, &item.FailureClass, &item.Status, &item.FirstSeen, &item.LastSeen, &item.HitCount, &item.LastSuccessAt); err != nil {
			return AccountQualityIncidentPage{}, accountInventoryDatabaseError(err)
		}
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return AccountQualityIncidentPage{}, accountInventoryDatabaseError(err)
	}
	page.HasMore = len(page.Items) > query.Limit
	if page.HasMore {
		page.Items = page.Items[:query.Limit]
	}
	return page, nil
}
