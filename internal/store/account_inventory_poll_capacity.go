package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	generated "github.com/sunxu/relay-station-control/internal/store/sqlc"
)

var (
	ErrInvalidInventoryPollCapacity      = errors.New("store: invalid account inventory poll capacity")
	ErrInventoryPollCapacityInconsistent = errors.New("store: account inventory poll eligibility is inconsistent")
)

type InventoryPollCapacity struct {
	EvaluatedAt       time.Time
	EvaluatedSlot     time.Time
	EligibleNodeCount int64
}

type InventoryPollCapacityReader interface {
	GetInventoryPollCapacity(context.Context) (InventoryPollCapacity, error)
}

type InventoryPollCapacityRepository struct{ queries *generated.Queries }

var _ InventoryPollCapacityReader = (*InventoryPollCapacityRepository)(nil)

func NewInventoryPollCapacityRepository(pool *pgxpool.Pool) (*InventoryPollCapacityRepository, error) {
	if pool == nil {
		return nil, errors.New("store: account inventory poll capacity database is unavailable")
	}
	return &InventoryPollCapacityRepository{queries: generated.New(pool)}, nil
}

func (r *InventoryPollCapacityRepository) GetInventoryPollCapacity(ctx context.Context) (InventoryPollCapacity, error) {
	if r == nil || r.queries == nil {
		return InventoryPollCapacity{}, ErrInvalidInventoryPollCapacity
	}
	queryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	row, err := r.queries.GetAccountInventoryPollCapacityV1(queryCtx)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23514" && pgErr.Message == "account inventory poll eligibility is inconsistent" {
			return InventoryPollCapacity{}, ErrInventoryPollCapacityInconsistent
		}
		return InventoryPollCapacity{}, err
	}
	if !row.EvaluatedAt.Valid || !row.EvaluatedSlot.Valid || row.EligibleNodeCount < 0 {
		return InventoryPollCapacity{}, ErrInvalidInventoryPollCapacity
	}
	return InventoryPollCapacity{EvaluatedAt: row.EvaluatedAt.Time.UTC(), EvaluatedSlot: row.EvaluatedSlot.Time.UTC(), EligibleNodeCount: row.EligibleNodeCount}, nil
}
