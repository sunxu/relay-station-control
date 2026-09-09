package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInvalidAccountAvailabilityQuery = errors.New("store: invalid account availability query")
	ErrAccountAvailabilityInconsistent = errors.New("store: account availability read model is inconsistent")
)

type AccountAvailability struct {
	State  string     `json:"state"`
	Reason string     `json:"reason"`
	Since  *time.Time `json:"since"`
}

type AccountAvailabilityOccurrenceQuery struct {
	InstanceID        uuid.UUID
	AccountKey        string
	Status            string
	AfterConfirmedAt  *time.Time
	AfterOccurrenceID uuid.UUID
	Limit             int
}

type AccountAvailabilityOccurrence struct {
	OccurrenceID  uuid.UUID  `json:"occurrence_id"`
	InstanceID    uuid.UUID  `json:"node_id"`
	AccountKey    string     `json:"account_key"`
	Reason        string     `json:"reason"`
	Severity      string     `json:"severity"`
	Status        string     `json:"status"`
	FirstSeenAt   time.Time  `json:"first_seen_at"`
	LastFailureAt time.Time  `json:"last_failure_at"`
	ConfirmedAt   time.Time  `json:"confirmed_at"`
	ResolvedAt    *time.Time `json:"resolved_at"`
}

type AccountAvailabilityOccurrencePage struct {
	Items   []AccountAvailabilityOccurrence
	HasMore bool
}

type AccountAvailabilityReader interface {
	BatchAccountAvailability(context.Context, uuid.UUID, []string) (map[string]AccountAvailability, error)
	ListAccountAvailabilityOccurrences(context.Context, AccountAvailabilityOccurrenceQuery) (AccountAvailabilityOccurrencePage, error)
}

type AccountAvailabilityRepository struct{ pool *pgxpool.Pool }

var _ AccountAvailabilityReader = (*AccountAvailabilityRepository)(nil)

func NewAccountAvailabilityRepository(pool *pgxpool.Pool) (*AccountAvailabilityRepository, error) {
	if pool == nil {
		return nil, errors.New("store: account availability database is unavailable")
	}
	return &AccountAvailabilityRepository{pool: pool}, nil
}

func (r *AccountAvailabilityRepository) BatchAccountAvailability(ctx context.Context, nodeID uuid.UUID, accountKeys []string) (map[string]AccountAvailability, error) {
	if r == nil || r.pool == nil || nodeID == uuid.Nil || len(accountKeys) > 100 {
		return nil, ErrInvalidAccountAvailabilityQuery
	}
	encoded := []byte{}
	if err := r.pool.QueryRow(ctx, `SELECT public.control_query_account_availability_v1($1,$2)`, nodeID, accountKeys).Scan(&encoded); err != nil {
		return nil, accountInventoryDatabaseError(err)
	}
	var rows []struct {
		AccountKey string     `json:"account_key"`
		State      string     `json:"state"`
		Reason     *string    `json:"reason"`
		Since      *time.Time `json:"since"`
	}
	if err := decodeAvailabilityJSON(encoded, &rows); err != nil {
		return nil, err
	}
	result := make(map[string]AccountAvailability, len(rows))
	for _, row := range rows {
		if row.AccountKey == "" || row.State == "" {
			return nil, ErrAccountAvailabilityInconsistent
		}
		reason := ""
		if row.Reason != nil {
			reason = *row.Reason
		}
		result[row.AccountKey] = AccountAvailability{State: row.State, Reason: reason, Since: row.Since}
	}
	return result, nil
}

func (r *AccountAvailabilityRepository) ListAccountAvailabilityOccurrences(ctx context.Context, query AccountAvailabilityOccurrenceQuery) (AccountAvailabilityOccurrencePage, error) {
	if r == nil || r.pool == nil || query.InstanceID == uuid.Nil || query.Limit < 1 || query.Limit > 100 || (query.AfterConfirmedAt == nil) != (query.AfterOccurrenceID == uuid.Nil) {
		return AccountAvailabilityOccurrencePage{}, ErrInvalidAccountAvailabilityQuery
	}
	if query.Status == "" {
		query.Status = "ACTIVE"
	}
	if query.Status != "ACTIVE" && query.Status != "RESOLVED" {
		return AccountAvailabilityOccurrencePage{}, ErrInvalidAccountAvailabilityQuery
	}
	rows, err := r.pool.Query(ctx, `SELECT value FROM public.control_query_account_availability_occurrences_v1($1,$2,$3,$4,$5,$6) AS value`, query.InstanceID, nullableText(query.AccountKey), query.Status, query.AfterConfirmedAt, nullableUUID(query.AfterOccurrenceID), query.Limit)
	if err != nil {
		return AccountAvailabilityOccurrencePage{}, accountInventoryDatabaseError(err)
	}
	defer rows.Close()
	page := AccountAvailabilityOccurrencePage{Items: make([]AccountAvailabilityOccurrence, 0, query.Limit)}
	for rows.Next() {
		var encoded []byte
		if err := rows.Scan(&encoded); err != nil {
			return AccountAvailabilityOccurrencePage{}, accountInventoryDatabaseError(err)
		}
		var item AccountAvailabilityOccurrence
		if err := decodeAvailabilityJSON(encoded, &item); err != nil || item.OccurrenceID == uuid.Nil || item.InstanceID != query.InstanceID {
			return AccountAvailabilityOccurrencePage{}, ErrAccountAvailabilityInconsistent
		}
		if len(page.Items) == query.Limit {
			page.HasMore = true
			continue
		}
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return AccountAvailabilityOccurrencePage{}, accountInventoryDatabaseError(err)
	}
	return page, nil
}

func decodeAvailabilityJSON(encoded []byte, target any) error {
	if len(encoded) == 0 {
		return ErrAccountAvailabilityInconsistent
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return ErrAccountAvailabilityInconsistent
	}
	return nil
}
