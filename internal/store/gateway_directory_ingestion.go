package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
) (generated.GatewayDirectoryIngestionRun, bool, bool, error) {
	if gatewayInstanceID == uuid.Nil {
		return generated.GatewayDirectoryIngestionRun{}, false, false, ErrInvalidGatewayDirectoryIngestionQuery
	}
	row, err := repository.queries.CreateOrGetGatewayDirectoryIngestionRun(ctx, nullableUUID(gatewayInstanceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return generated.GatewayDirectoryIngestionRun{}, false, true, nil
	}
	if err != nil {
		return generated.GatewayDirectoryIngestionRun{}, false, false, err
	}
	run := gatewayDirectoryIngestionRunFromCreateRow(row)
	if !validGatewayDirectoryIngestionRun(run) {
		return generated.GatewayDirectoryIngestionRun{}, false, false, ErrGatewayDirectoryIngestionInconsistent
	}
	return run, row.Created, false, nil
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
	run := gatewayDirectoryIngestionRunFromClaimRow(row)
	if !validGatewayDirectoryIngestionRun(run) {
		return nil, ErrGatewayDirectoryIngestionInconsistent
	}
	return &run, nil
}

// LeaseValid is only a read-side observation/pre-check.
// It does not provide fencing-write permission by itself; every later state
// update must atomically check ingestion_run_id + status='running' +
// lease_fencing_token in the same UPDATE/transaction that mutates the row.
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

func gatewayDirectoryIngestionRunFromCreateRow(row generated.CreateOrGetGatewayDirectoryIngestionRunRow) generated.GatewayDirectoryIngestionRun {
	return generated.GatewayDirectoryIngestionRun{
		IngestionRunID:     row.IngestionRunID,
		GatewayInstanceID:  row.GatewayInstanceID,
		ScheduledAt:        row.ScheduledAt,
		Status:             row.Status,
		AttemptCount:       row.AttemptCount,
		CreatedAt:          row.CreatedAt,
		FirstStartedAt:     row.FirstStartedAt,
		LastStartedAt:      row.LastStartedAt,
		LeaseExpiresAt:     row.LeaseExpiresAt,
		LeaseFencingToken:  row.LeaseFencingToken,
		TerminalAt:         row.TerminalAt,
		Outcome:            row.Outcome,
		LastFailureClass:   row.LastFailureClass,
		SourceGeneratedAt:  row.SourceGeneratedAt,
		ReceivedAt:         row.ReceivedAt,
		ContentFingerprint: row.ContentFingerprint,
		SnapshotID:         row.SnapshotID,
		AccountCount:       row.AccountCount,
	}
}

func gatewayDirectoryIngestionRunFromClaimRow(row generated.ClaimGatewayDirectoryIngestionRunRow) generated.GatewayDirectoryIngestionRun {
	return generated.GatewayDirectoryIngestionRun{
		IngestionRunID:     row.IngestionRunID,
		GatewayInstanceID:  row.GatewayInstanceID,
		ScheduledAt:        row.ScheduledAt,
		Status:             row.Status,
		AttemptCount:       row.AttemptCount,
		CreatedAt:          row.CreatedAt,
		FirstStartedAt:     row.FirstStartedAt,
		LastStartedAt:      row.LastStartedAt,
		LeaseExpiresAt:     row.LeaseExpiresAt,
		LeaseFencingToken:  row.LeaseFencingToken,
		TerminalAt:         row.TerminalAt,
		Outcome:            row.Outcome,
		LastFailureClass:   row.LastFailureClass,
		SourceGeneratedAt:  row.SourceGeneratedAt,
		ReceivedAt:         row.ReceivedAt,
		ContentFingerprint: row.ContentFingerprint,
		SnapshotID:         row.SnapshotID,
		AccountCount:       row.AccountCount,
	}
}
