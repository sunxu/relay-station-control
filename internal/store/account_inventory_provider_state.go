package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	generated "github.com/sunxu/relay-station-control/internal/store/sqlc"
)

var (
	ErrInvalidAccountInventoryProviderStateQuery = errors.New("store: invalid account inventory provider state query")
	ErrAccountInventoryProviderStateNotFound     = errors.New("store: account inventory provider state node was not found")
	ErrAccountInventoryProviderStateInconsistent = errors.New("store: account inventory provider state is inconsistent")
)

type AccountInventoryProviderState struct {
	Provider           string     `json:"provider"`
	MonitoringStatus   string     `json:"monitoring_status"`
	State              *string    `json:"state"`
	CurrentScheduledAt *time.Time `json:"current_scheduled_at"`
	LastCompleteAt     *time.Time `json:"last_complete_at"`
	SnapshotFreshness  string     `json:"snapshot_freshness"`
	HealthScheduledAt  *time.Time `json:"health_scheduled_at"`
	HealthDegraded     *bool      `json:"health_degraded"`
	HealthReason       *string    `json:"health_reason"`
}

type AccountInventoryProviderStates struct {
	InstanceID uuid.UUID
	ObservedAt time.Time
	Providers  []AccountInventoryProviderState
}

type AccountInventoryProviderStateReader interface {
	GetProviderStates(context.Context, uuid.UUID) (AccountInventoryProviderStates, error)
}

type AccountInventoryProviderStateRepository struct {
	queries *generated.Queries
}

var _ AccountInventoryProviderStateReader = (*AccountInventoryProviderStateRepository)(nil)

func NewAccountInventoryProviderStateRepository(pool *pgxpool.Pool) (*AccountInventoryProviderStateRepository, error) {
	if pool == nil {
		return nil, errors.New("store: account inventory Provider state database is unavailable")
	}
	return &AccountInventoryProviderStateRepository{queries: generated.New(pool)}, nil
}

func (repository *AccountInventoryProviderStateRepository) GetProviderStates(ctx context.Context, instanceID uuid.UUID) (AccountInventoryProviderStates, error) {
	if repository == nil || repository.queries == nil || instanceID == uuid.Nil {
		return AccountInventoryProviderStates{}, ErrInvalidAccountInventoryProviderStateQuery
	}
	queryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	row, err := repository.queries.GetAccountInventoryProviderStatesV1(queryCtx, pgtype.UUID{Bytes: instanceID, Valid: true})
	if err != nil {
		return AccountInventoryProviderStates{}, accountInventoryProviderStateDatabaseError(err)
	}
	page := AccountInventoryProviderStates{InstanceID: instanceID}
	if row.ObservedAt.Valid {
		page.ObservedAt = row.ObservedAt.Time.UTC()
	} else {
		return AccountInventoryProviderStates{}, ErrAccountInventoryProviderStateInconsistent
	}
	decoder := json.NewDecoder(bytes.NewReader(row.Providers))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&page.Providers); err != nil || page.Providers == nil {
		return AccountInventoryProviderStates{}, ErrAccountInventoryProviderStateInconsistent
	}
	return page, nil
}

func accountInventoryProviderStateDatabaseError(err error) error {
	var databaseError *pgconn.PgError
	if !errors.As(err, &databaseError) {
		return err
	}
	switch databaseError.Code {
	case "22023":
		return ErrInvalidAccountInventoryProviderStateQuery
	case "P0404":
		return ErrAccountInventoryProviderStateNotFound
	case "P0503":
		return ErrAccountInventoryProviderStateInconsistent
	default:
		return err
	}
}
