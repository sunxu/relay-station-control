package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	generated "github.com/sunxu/relay-station-control/internal/store/sqlc"
)

var (
	ErrInvalidGatewayDirectoryIngestionQuery = errors.New("store: invalid gateway directory ingestion query")
	ErrGatewayDirectoryIngestionInconsistent = errors.New("store: gateway directory ingestion state is inconsistent")
)

type GatewayDirectoryIngestionRepository struct {
	queries *generated.Queries
}

func NewGatewayDirectoryIngestionRepository(pool *pgxpool.Pool) (*GatewayDirectoryIngestionRepository, error) {
	if pool == nil {
		return nil, errors.New("store: gateway directory ingestion database is unavailable")
	}
	return &GatewayDirectoryIngestionRepository{queries: generated.New(pool)}, nil
}

func (repository *GatewayDirectoryIngestionRepository) ScheduleCurrent(
	ctx context.Context, gatewayInstanceID uuid.UUID,
) (generated.GatewayDirectoryIngestionRun, error) {
	if gatewayInstanceID == uuid.Nil {
		return generated.GatewayDirectoryIngestionRun{}, ErrInvalidGatewayDirectoryIngestionQuery
	}
	scheduledAt, err := repository.queries.GetGatewayDirectoryCurrentScheduledAt(ctx)
	if err != nil {
		return generated.GatewayDirectoryIngestionRun{}, err
	}
	if !scheduledAt.Valid || scheduledAt.Time.IsZero() {
		return generated.GatewayDirectoryIngestionRun{}, ErrGatewayDirectoryIngestionInconsistent
	}
	return repository.queries.CreateOrGetGatewayDirectoryIngestionRun(ctx,
		generated.CreateOrGetGatewayDirectoryIngestionRunParams{
			GatewayInstanceID: nullableUUID(gatewayInstanceID),
			ScheduledAt:       scheduledAt,
		})
}

func (repository *GatewayDirectoryIngestionRepository) ClaimRunnable(
	ctx context.Context, gatewayInstanceID, fencingToken uuid.UUID,
) (*generated.GatewayDirectoryIngestionRun, error) {
	if gatewayInstanceID == uuid.Nil || fencingToken == uuid.Nil {
		return nil, ErrInvalidGatewayDirectoryIngestionQuery
	}
	row, err := repository.queries.ClaimGatewayDirectoryIngestionRun(ctx,
		generated.ClaimGatewayDirectoryIngestionRunParams{
			GatewayInstanceID: nullableUUID(gatewayInstanceID),
			LeaseFencingToken: nullableUUID(fencingToken),
		})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !validGatewayDirectoryIngestionRun(row) {
		return nil, ErrGatewayDirectoryIngestionInconsistent
	}
	return &row, nil
}

func (repository *GatewayDirectoryIngestionRepository) LeaseValid(
	ctx context.Context, ingestionRunID, gatewayInstanceID, fencingToken uuid.UUID,
) (*generated.GatewayDirectoryIngestionRun, error) {
	if ingestionRunID == uuid.Nil || gatewayInstanceID == uuid.Nil || fencingToken == uuid.Nil {
		return nil, ErrInvalidGatewayDirectoryIngestionQuery
	}
	row, err := repository.queries.GetGatewayDirectoryIngestionRunLease(ctx,
		generated.GetGatewayDirectoryIngestionRunLeaseParams{
			IngestionRunID:    nullableUUID(ingestionRunID),
			GatewayInstanceID: nullableUUID(gatewayInstanceID),
			LeaseFencingToken: nullableUUID(fencingToken),
		})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !validGatewayDirectoryIngestionRun(row) {
		return nil, ErrGatewayDirectoryIngestionInconsistent
	}
	return &row, nil
}

func (repository *GatewayDirectoryIngestionRepository) ExpiredRunningRuns(
	ctx context.Context, limit int,
) ([]generated.GatewayDirectoryIngestionRun, error) {
	if limit < 1 || limit > 1000 {
		return nil, ErrInvalidGatewayDirectoryIngestionQuery
	}
	rows, err := repository.queries.ListExpiredGatewayDirectoryIngestionRuns(ctx, int32(limit))
	if err != nil {
		return nil, err
	}
	runs := make([]generated.GatewayDirectoryIngestionRun, 0, len(rows))
	for _, row := range rows {
		if !validGatewayDirectoryIngestionRun(row) {
			return nil, ErrGatewayDirectoryIngestionInconsistent
		}
		runs = append(runs, row)
	}
	return runs, nil
}

func validGatewayDirectoryIngestionRun(row generated.GatewayDirectoryIngestionRun) bool {
	return uuidFromPG(row.IngestionRunID) != uuid.Nil &&
		uuidFromPG(row.GatewayInstanceID) != uuid.Nil &&
		row.ScheduledAt.Valid &&
		row.Status != "" &&
		row.AttemptCount >= 0 && row.AttemptCount <= 2 &&
		row.CreatedAt.Valid
}
