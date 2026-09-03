package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	generated "github.com/sunxu/relay-station-control/internal/store/sqlc"
)

type RelayBindingOutcome string

const (
	RelayBindingOutcomeSuccess              RelayBindingOutcome = "success"
	RelayBindingOutcomeAlreadyUnbound       RelayBindingOutcome = "already_unbound"
	RelayBindingOutcomeNoCurrentBinding     RelayBindingOutcome = "no_current_binding"
	RelayBindingOutcomeDirectoryUnavailable RelayBindingOutcome = "directory_unavailable"
	RelayBindingOutcomeDirectoryStale       RelayBindingOutcome = "directory_stale"
	RelayBindingOutcomeAccountNotFound      RelayBindingOutcome = "account_not_found"
	RelayBindingOutcomeNodeNotFound         RelayBindingOutcome = "node_not_found"
	RelayBindingOutcomeGatewayNotFound      RelayBindingOutcome = "gateway_not_found"
	RelayBindingOutcomeNodeConflict         RelayBindingOutcome = "node_conflict"
	RelayBindingOutcomeAccountConflict      RelayBindingOutcome = "account_conflict"
)

const (
	RelayBindingAuditCategory = "relay_binding"
	RelayBindingAuditBind     = "relay_binding.bind"
	RelayBindingAuditUnbind   = "relay_binding.unbind"
	RelayBindingAuditRebind   = "relay_binding.rebind"

	RelayBindingReasonAdminBind   = "administrator_bind"
	RelayBindingReasonAdminRebind = "administrator_rebind"
	RelayBindingReasonAdminUnbind = "administrator_unbind"
)

type RelayNodeGatewayAccountBinding struct {
	BindingID          uuid.UUID
	RelayNodeID        uuid.UUID
	GatewayInstanceID  uuid.UUID
	GatewayAccountID   int64
	EvidenceSnapshotID uuid.UUID
	BoundAt            time.Time
	BoundBy            uuid.UUID
	BindReason         string
	EndedAt            *time.Time
	EndedBy            *uuid.UUID
	EndReason          *string
}

type RelayBindingResult struct {
	Outcome         RelayBindingOutcome
	Binding         *RelayNodeGatewayAccountBinding
	PreviousBinding *RelayNodeGatewayAccountBinding
	OperationAt     time.Time
}

type BindParams struct {
	RelayNodeID       uuid.UUID
	GatewayInstanceID uuid.UUID
	GatewayAccountID  int64
	AdminID           uuid.UUID
	RequestID         string
}

type RebindParams struct {
	RelayNodeID          uuid.UUID
	NewGatewayInstanceID uuid.UUID
	NewGatewayAccountID  int64
	AdminID              uuid.UUID
	RequestID            string
}

type UnbindParams struct {
	RelayNodeID uuid.UUID
	AdminID     uuid.UUID
	RequestID   string
}

type RelayBindingRepository struct {
	queries *generated.Queries
	pool    *pgxpool.Pool
}

func NewRelayBindingRepository(pool *pgxpool.Pool) (*RelayBindingRepository, error) {
	if pool == nil {
		return nil, errors.New("store: relay binding database is unavailable")
	}
	return &RelayBindingRepository{
		queries: generated.New(pool),
		pool:    pool,
	}, nil
}

func (repository *RelayBindingRepository) Bind(
	ctx context.Context,
	params BindParams,
) (RelayBindingResult, error) {
	if repository == nil || repository.pool == nil {
		return RelayBindingResult{}, errors.New("store: relay binding repository is unavailable")
	}
	if params.RelayNodeID == uuid.Nil || params.GatewayInstanceID == uuid.Nil ||
		params.GatewayAccountID <= 0 || params.AdminID == uuid.Nil {
		return RelayBindingResult{}, ErrInvalidAssetQuery
	}
	requestID := params.RequestID
	if requestID == "" {
		requestID = "req-" + uuid.NewString()
	}

	tx, err := repository.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return RelayBindingResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	txQueries := repository.queries.WithTx(tx)

	// 1. Lock Node asset identity (lock order 1)
	_, err = txQueries.LockRelayNodeAssetForBinding(ctx, nullableUUID(params.RelayNodeID))
	if errors.Is(err, pgx.ErrNoRows) {
		return RelayBindingResult{Outcome: RelayBindingOutcomeNodeNotFound}, nil
	}
	if err != nil {
		return RelayBindingResult{}, err
	}

	// 2. Lock Gateway Directory current state (lock order 2)
	currentState, err := txQueries.LockGatewayDirectoryCurrentState(ctx, nullableUUID(params.GatewayInstanceID))
	if errors.Is(err, pgx.ErrNoRows) {
		// Check if gateway exists at all
		_, gErr := txQueries.LockGatewayDirectoryInstance(ctx, nullableUUID(params.GatewayInstanceID))
		if errors.Is(gErr, pgx.ErrNoRows) {
			return RelayBindingResult{Outcome: RelayBindingOutcomeGatewayNotFound}, nil
		}
		return RelayBindingResult{Outcome: RelayBindingOutcomeDirectoryUnavailable}, nil
	}
	if err != nil {
		return RelayBindingResult{}, err
	}

	// 3. Lock current Node binding (lock order 3)
	existingNodeBinding, err := txQueries.LockCurrentRelayNodeGatewayAccountBindingByNode(ctx, nullableUUID(params.RelayNodeID))
	if err == nil {
		b := bindingFromSQLC(existingNodeBinding)
		return RelayBindingResult{Outcome: RelayBindingOutcomeNodeConflict, Binding: &b}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return RelayBindingResult{}, err
	}

	// 4. Lock target Account binding (lock order 4)
	existingAccountBinding, err := txQueries.LockCurrentRelayNodeGatewayAccountBindingByAccount(ctx,
		generated.LockCurrentRelayNodeGatewayAccountBindingByAccountParams{
			GatewayInstanceID: nullableUUID(params.GatewayInstanceID),
			GatewayAccountID:  params.GatewayAccountID,
		})
	if err == nil {
		b := bindingFromSQLC(existingAccountBinding)
		return RelayBindingResult{Outcome: RelayBindingOutcomeAccountConflict, Binding: &b}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return RelayBindingResult{}, err
	}

	// 5. Get single DB wall-clock time after all locks acquired
	dbTime, err := txQueries.GetRelayBindingDBTime(ctx)
	if err != nil {
		return RelayBindingResult{}, err
	}
	operationAt := dbTime.Time.UTC()

	// 6. Validate Directory freshness against operationAt
	if !currentState.CurrentSnapshotID.Valid || !currentState.LastSuccessReceivedAt.Valid {
		return RelayBindingResult{Outcome: RelayBindingOutcomeDirectoryUnavailable, OperationAt: operationAt}, nil
	}

	lastSuccessAt := currentState.LastSuccessReceivedAt.Time.UTC()
	if operationAt.Sub(lastSuccessAt) > 540*time.Second {
		return RelayBindingResult{Outcome: RelayBindingOutcomeDirectoryStale, OperationAt: operationAt}, nil
	}

	// 7. Verify target account exists in current snapshot
	_, err = txQueries.GetGatewayDirectorySnapshotItem(ctx, generated.GetGatewayDirectorySnapshotItemParams{
		SnapshotID: currentState.CurrentSnapshotID,
		AccountID:  params.GatewayAccountID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return RelayBindingResult{Outcome: RelayBindingOutcomeAccountNotFound, OperationAt: operationAt}, nil
	}
	if err != nil {
		return RelayBindingResult{}, err
	}

	// 8. Insert open binding
	evidenceSnapshotID := uuidFromPG(currentState.CurrentSnapshotID)
	createdBinding, err := txQueries.InsertOpenRelayNodeGatewayAccountBinding(ctx,
		generated.InsertOpenRelayNodeGatewayAccountBindingParams{
			RelayNodeID:        nullableUUID(params.RelayNodeID),
			GatewayInstanceID:  nullableUUID(params.GatewayInstanceID),
			GatewayAccountID:   params.GatewayAccountID,
			EvidenceSnapshotID: currentState.CurrentSnapshotID,
			BoundAt:            dbTime,
			BoundBy:            nullableUUID(params.AdminID),
			BindReason:         RelayBindingReasonAdminBind,
		})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			if strings.Contains(pgErr.ConstraintName, "current_node_idx") {
				return RelayBindingResult{Outcome: RelayBindingOutcomeNodeConflict, OperationAt: operationAt}, nil
			}
			if strings.Contains(pgErr.ConstraintName, "current_account_idx") {
				return RelayBindingResult{Outcome: RelayBindingOutcomeAccountConflict, OperationAt: operationAt}, nil
			}
		}
		return RelayBindingResult{}, err
	}

	// 9. Insert audit log
	auditDetails, err := json.Marshal(map[string]any{
		"relay_node_id":          params.RelayNodeID.String(),
		"gateway_instance_id":    params.GatewayInstanceID.String(),
		"old_gateway_account_id": nil,
		"new_gateway_account_id": params.GatewayAccountID,
		"evidence_snapshot_id":   evidenceSnapshotID.String(),
		"reason_code":            RelayBindingReasonAdminBind,
	})
	if err != nil {
		return RelayBindingResult{}, err
	}

	_, err = txQueries.InsertAuditLog(ctx, generated.InsertAuditLogParams{
		AuditID:           nullableUUID(uuid.New()),
		Category:          RelayBindingAuditCategory,
		Action:            RelayBindingAuditBind,
		Result:            "success",
		ActorAdminID:      nullableUUID(params.AdminID),
		TargetAdminID:     pgtype.UUID{},
		ActorFingerprint:  nil,
		SourceFingerprint: nil,
		Reason:            pgtype.Text{String: RelayBindingReasonAdminBind, Valid: true},
		RequestID:         requestID,
		Details:           auditDetails,
	})
	if err != nil {
		return RelayBindingResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return RelayBindingResult{}, err
	}

	resBinding := bindingFromSQLC(createdBinding)
	return RelayBindingResult{
		Outcome:     RelayBindingOutcomeSuccess,
		Binding:     &resBinding,
		OperationAt: operationAt,
	}, nil
}

func (repository *RelayBindingRepository) Rebind(
	ctx context.Context,
	params RebindParams,
) (RelayBindingResult, error) {
	if repository == nil || repository.pool == nil {
		return RelayBindingResult{}, errors.New("store: relay binding repository is unavailable")
	}
	if params.RelayNodeID == uuid.Nil || params.NewGatewayInstanceID == uuid.Nil ||
		params.NewGatewayAccountID <= 0 || params.AdminID == uuid.Nil {
		return RelayBindingResult{}, ErrInvalidAssetQuery
	}
	requestID := params.RequestID
	if requestID == "" {
		requestID = "req-" + uuid.NewString()
	}

	tx, err := repository.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return RelayBindingResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	txQueries := repository.queries.WithTx(tx)

	// 1. Lock Node asset identity (lock order 1)
	_, err = txQueries.LockRelayNodeAssetForBinding(ctx, nullableUUID(params.RelayNodeID))
	if errors.Is(err, pgx.ErrNoRows) {
		return RelayBindingResult{Outcome: RelayBindingOutcomeNodeNotFound}, nil
	}
	if err != nil {
		return RelayBindingResult{}, err
	}

	// 2. Lock new Gateway Directory current state (lock order 2)
	currentState, err := txQueries.LockGatewayDirectoryCurrentState(ctx, nullableUUID(params.NewGatewayInstanceID))
	if errors.Is(err, pgx.ErrNoRows) {
		_, gErr := txQueries.LockGatewayDirectoryInstance(ctx, nullableUUID(params.NewGatewayInstanceID))
		if errors.Is(gErr, pgx.ErrNoRows) {
			return RelayBindingResult{Outcome: RelayBindingOutcomeGatewayNotFound}, nil
		}
		return RelayBindingResult{Outcome: RelayBindingOutcomeDirectoryUnavailable}, nil
	}
	if err != nil {
		return RelayBindingResult{}, err
	}

	// 3. Lock Node's current binding (lock order 3); if not bound, fail with no_current_binding
	existingNodeBinding, err := txQueries.LockCurrentRelayNodeGatewayAccountBindingByNode(ctx, nullableUUID(params.RelayNodeID))
	if errors.Is(err, pgx.ErrNoRows) {
		return RelayBindingResult{Outcome: RelayBindingOutcomeNoCurrentBinding}, nil
	}
	if err != nil {
		return RelayBindingResult{}, err
	}
	oldBinding := bindingFromSQLC(existingNodeBinding)

	// 4. Lock target new account binding (lock order 4)
	existingAccountBinding, err := txQueries.LockCurrentRelayNodeGatewayAccountBindingByAccount(ctx,
		generated.LockCurrentRelayNodeGatewayAccountBindingByAccountParams{
			GatewayInstanceID: nullableUUID(params.NewGatewayInstanceID),
			GatewayAccountID:  params.NewGatewayAccountID,
		})
	if err == nil {
		b := bindingFromSQLC(existingAccountBinding)
		return RelayBindingResult{Outcome: RelayBindingOutcomeAccountConflict, Binding: &b, PreviousBinding: &oldBinding}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return RelayBindingResult{}, err
	}

	// 5. Get single DB wall-clock time after all locks acquired
	dbTime, err := txQueries.GetRelayBindingDBTime(ctx)
	if err != nil {
		return RelayBindingResult{}, err
	}
	operationAt := dbTime.Time.UTC()

	// 6. Validate Directory freshness against operationAt
	if !currentState.CurrentSnapshotID.Valid || !currentState.LastSuccessReceivedAt.Valid {
		return RelayBindingResult{Outcome: RelayBindingOutcomeDirectoryUnavailable, PreviousBinding: &oldBinding, OperationAt: operationAt}, nil
	}

	lastSuccessAt := currentState.LastSuccessReceivedAt.Time.UTC()
	if operationAt.Sub(lastSuccessAt) > 540*time.Second {
		return RelayBindingResult{Outcome: RelayBindingOutcomeDirectoryStale, PreviousBinding: &oldBinding, OperationAt: operationAt}, nil
	}

	// 7. Verify target account exists in current snapshot
	_, err = txQueries.GetGatewayDirectorySnapshotItem(ctx, generated.GetGatewayDirectorySnapshotItemParams{
		SnapshotID: currentState.CurrentSnapshotID,
		AccountID:  params.NewGatewayAccountID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return RelayBindingResult{Outcome: RelayBindingOutcomeAccountNotFound, PreviousBinding: &oldBinding, OperationAt: operationAt}, nil
	}
	if err != nil {
		return RelayBindingResult{}, err
	}

	// 8. Close old binding with exact expected identity
	closedRow, err := txQueries.CloseCurrentRelayNodeGatewayAccountBinding(ctx,
		generated.CloseCurrentRelayNodeGatewayAccountBindingParams{
			EndedAt:           dbTime,
			EndedBy:           nullableUUID(params.AdminID),
			EndReason:         RelayBindingReasonAdminRebind,
			BindingID:         existingNodeBinding.BindingID,
			RelayNodeID:       existingNodeBinding.RelayNodeID,
			GatewayInstanceID: existingNodeBinding.GatewayInstanceID,
			GatewayAccountID:  existingNodeBinding.GatewayAccountID,
		})
	if err != nil {
		return RelayBindingResult{}, err
	}
	closedBinding := bindingFromSQLC(closedRow)

	// 9. Insert new open binding with EXACT SAME dbTime
	evidenceSnapshotID := uuidFromPG(currentState.CurrentSnapshotID)
	createdBinding, err := txQueries.InsertOpenRelayNodeGatewayAccountBinding(ctx,
		generated.InsertOpenRelayNodeGatewayAccountBindingParams{
			RelayNodeID:        nullableUUID(params.RelayNodeID),
			GatewayInstanceID:  nullableUUID(params.NewGatewayInstanceID),
			GatewayAccountID:   params.NewGatewayAccountID,
			EvidenceSnapshotID: currentState.CurrentSnapshotID,
			BoundAt:            dbTime,
			BoundBy:            nullableUUID(params.AdminID),
			BindReason:         RelayBindingReasonAdminRebind,
		})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			if strings.Contains(pgErr.ConstraintName, "current_node_idx") {
				return RelayBindingResult{Outcome: RelayBindingOutcomeNodeConflict, OperationAt: operationAt}, nil
			}
			if strings.Contains(pgErr.ConstraintName, "current_account_idx") {
				return RelayBindingResult{Outcome: RelayBindingOutcomeAccountConflict, OperationAt: operationAt}, nil
			}
		}
		return RelayBindingResult{}, err
	}

	// 10. Insert audit log
	auditDetails, err := json.Marshal(map[string]any{
		"relay_node_id":          params.RelayNodeID.String(),
		"gateway_instance_id":    params.NewGatewayInstanceID.String(),
		"old_gateway_account_id": oldBinding.GatewayAccountID,
		"new_gateway_account_id": params.NewGatewayAccountID,
		"evidence_snapshot_id":   evidenceSnapshotID.String(),
		"reason_code":            RelayBindingReasonAdminRebind,
	})
	if err != nil {
		return RelayBindingResult{}, err
	}

	_, err = txQueries.InsertAuditLog(ctx, generated.InsertAuditLogParams{
		AuditID:           nullableUUID(uuid.New()),
		Category:          RelayBindingAuditCategory,
		Action:            RelayBindingAuditRebind,
		Result:            "success",
		ActorAdminID:      nullableUUID(params.AdminID),
		TargetAdminID:     pgtype.UUID{},
		ActorFingerprint:  nil,
		SourceFingerprint: nil,
		Reason:            pgtype.Text{String: RelayBindingReasonAdminRebind, Valid: true},
		RequestID:         requestID,
		Details:           auditDetails,
	})
	if err != nil {
		return RelayBindingResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return RelayBindingResult{}, err
	}

	resBinding := bindingFromSQLC(createdBinding)
	return RelayBindingResult{
		Outcome:         RelayBindingOutcomeSuccess,
		Binding:         &resBinding,
		PreviousBinding: &closedBinding,
		OperationAt:     operationAt,
	}, nil
}

func (repository *RelayBindingRepository) Unbind(
	ctx context.Context,
	params UnbindParams,
) (RelayBindingResult, error) {
	if repository == nil || repository.pool == nil {
		return RelayBindingResult{}, errors.New("store: relay binding repository is unavailable")
	}
	if params.RelayNodeID == uuid.Nil || params.AdminID == uuid.Nil {
		return RelayBindingResult{}, ErrInvalidAssetQuery
	}
	requestID := params.RequestID
	if requestID == "" {
		requestID = "req-" + uuid.NewString()
	}

	tx, err := repository.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return RelayBindingResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	txQueries := repository.queries.WithTx(tx)

	// 1. Lock Node asset identity (lock order 1)
	_, err = txQueries.LockRelayNodeAssetForBinding(ctx, nullableUUID(params.RelayNodeID))
	if errors.Is(err, pgx.ErrNoRows) {
		return RelayBindingResult{Outcome: RelayBindingOutcomeNodeNotFound}, nil
	}
	if err != nil {
		return RelayBindingResult{}, err
	}

	// 2. Lock Node's current binding; if already unbound, return stable already_unbound
	existingNodeBinding, err := txQueries.LockCurrentRelayNodeGatewayAccountBindingByNode(ctx, nullableUUID(params.RelayNodeID))
	if errors.Is(err, pgx.ErrNoRows) {
		dbTime, timeErr := txQueries.GetRelayBindingDBTime(ctx)
		if timeErr != nil {
			return RelayBindingResult{}, timeErr
		}
		return RelayBindingResult{
			Outcome:     RelayBindingOutcomeAlreadyUnbound,
			OperationAt: dbTime.Time.UTC(),
		}, nil
	}
	if err != nil {
		return RelayBindingResult{}, err
	}

	// 3. Get single DB wall-clock time after locks acquired
	dbTime, err := txQueries.GetRelayBindingDBTime(ctx)
	if err != nil {
		return RelayBindingResult{}, err
	}
	operationAt := dbTime.Time.UTC()

	// 4. Close current binding
	closedRow, err := txQueries.CloseCurrentRelayNodeGatewayAccountBinding(ctx,
		generated.CloseCurrentRelayNodeGatewayAccountBindingParams{
			EndedAt:           dbTime,
			EndedBy:           nullableUUID(params.AdminID),
			EndReason:         RelayBindingReasonAdminUnbind,
			BindingID:         existingNodeBinding.BindingID,
			RelayNodeID:       existingNodeBinding.RelayNodeID,
			GatewayInstanceID: existingNodeBinding.GatewayInstanceID,
			GatewayAccountID:  existingNodeBinding.GatewayAccountID,
		})
	if err != nil {
		return RelayBindingResult{}, err
	}
	closedBinding := bindingFromSQLC(closedRow)

	// 5. Insert audit log
	evidenceSnapshotID := uuidFromPG(existingNodeBinding.EvidenceSnapshotID)
	auditDetails, err := json.Marshal(map[string]any{
		"relay_node_id":          params.RelayNodeID.String(),
		"gateway_instance_id":    uuidFromPG(existingNodeBinding.GatewayInstanceID).String(),
		"old_gateway_account_id": existingNodeBinding.GatewayAccountID,
		"new_gateway_account_id": nil,
		"evidence_snapshot_id":   evidenceSnapshotID.String(),
		"reason_code":            RelayBindingReasonAdminUnbind,
	})
	if err != nil {
		return RelayBindingResult{}, err
	}

	_, err = txQueries.InsertAuditLog(ctx, generated.InsertAuditLogParams{
		AuditID:           nullableUUID(uuid.New()),
		Category:          RelayBindingAuditCategory,
		Action:            RelayBindingAuditUnbind,
		Result:            "success",
		ActorAdminID:      nullableUUID(params.AdminID),
		TargetAdminID:     pgtype.UUID{},
		ActorFingerprint:  nil,
		SourceFingerprint: nil,
		Reason:            pgtype.Text{String: RelayBindingReasonAdminUnbind, Valid: true},
		RequestID:         requestID,
		Details:           auditDetails,
	})
	if err != nil {
		return RelayBindingResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return RelayBindingResult{}, err
	}

	return RelayBindingResult{
		Outcome:         RelayBindingOutcomeSuccess,
		PreviousBinding: &closedBinding,
		OperationAt:     operationAt,
	}, nil
}

func (repository *RelayBindingRepository) GetCurrentBindingByNode(
	ctx context.Context,
	relayNodeID uuid.UUID,
) (*RelayNodeGatewayAccountBinding, error) {
	if repository == nil || repository.pool == nil {
		return nil, errors.New("store: relay binding repository is unavailable")
	}
	if relayNodeID == uuid.Nil {
		return nil, ErrInvalidAssetQuery
	}
	row, err := repository.queries.GetCurrentRelayNodeGatewayAccountBindingByNode(ctx, nullableUUID(relayNodeID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	b := bindingFromSQLC(row)
	return &b, nil
}

func (repository *RelayBindingRepository) GetCurrentBindingByAccount(
	ctx context.Context,
	gatewayInstanceID uuid.UUID,
	gatewayAccountID int64,
) (*RelayNodeGatewayAccountBinding, error) {
	if repository == nil || repository.pool == nil {
		return nil, errors.New("store: relay binding repository is unavailable")
	}
	if gatewayInstanceID == uuid.Nil || gatewayAccountID <= 0 {
		return nil, ErrInvalidAssetQuery
	}
	row, err := repository.queries.GetCurrentRelayNodeGatewayAccountBindingByAccount(ctx,
		generated.GetCurrentRelayNodeGatewayAccountBindingByAccountParams{
			GatewayInstanceID: nullableUUID(gatewayInstanceID),
			GatewayAccountID:  gatewayAccountID,
		})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	b := bindingFromSQLC(row)
	return &b, nil
}

type RelayBindingResolution string

const (
	RelayBindingResolutionUnbound    RelayBindingResolution = "unbound"
	RelayBindingResolutionResolved   RelayBindingResolution = "resolved"
	RelayBindingResolutionUnresolved RelayBindingResolution = "unresolved"
	RelayBindingResolutionUnknown    RelayBindingResolution = "unknown"
)

type DirectoryFreshness string

const (
	DirectoryFreshnessFresh       DirectoryFreshness = "fresh"
	DirectoryFreshnessStale       DirectoryFreshness = "stale"
	DirectoryFreshnessUnavailable DirectoryFreshness = "unavailable"
)

type AccountContextSource string

const (
	AccountContextSourceCurrent   AccountContextSource = "current"
	AccountContextSourceLastKnown AccountContextSource = "last_known"
	AccountContextSourceNone      AccountContextSource = "none"
)

type GatewayAccountContext struct {
	AccountID int64
	Name      string
	Platform  string
	Type      string
	URL       *string
	Status    string
}

type NodeCentricBindingView struct {
	RelayNodeID              uuid.UUID
	CurrentBinding           *RelayNodeGatewayAccountBinding
	GatewayInstanceID        *uuid.UUID
	GatewayAccountID         *int64
	Resolution               RelayBindingResolution
	DirectoryFreshness       DirectoryFreshness
	LastSuccessObservationAt *time.Time
	ContextSource            AccountContextSource
	AccountContext           *GatewayAccountContext
	ObservedAt               time.Time
}

type GatewayAccountCentricBindingItem struct {
	GatewayAccountID int64
	AccountContext   GatewayAccountContext
	BoundRelayNodeID *uuid.UUID
	CurrentBinding   *RelayNodeGatewayAccountBinding
	Resolution       RelayBindingResolution
	ContextSource    AccountContextSource
}

type GatewayAccountCentricBindingView struct {
	GatewayInstanceID        uuid.UUID
	DirectoryFreshness       DirectoryFreshness
	CurrentSnapshotID        *uuid.UUID
	LastSuccessObservationAt *time.Time
	ObservedAt               time.Time
	Accounts                 []GatewayAccountCentricBindingItem
}

type UnresolvedGatewayAccountBindingItem struct {
	CurrentBinding           RelayNodeGatewayAccountBinding
	Resolution               RelayBindingResolution
	DirectoryFreshness       DirectoryFreshness
	LastSuccessObservationAt *time.Time
	ContextSource            AccountContextSource
	LastKnownAccountContext  *GatewayAccountContext
	ObservedAt               time.Time
}

func (repository *RelayBindingRepository) GetNodeCentricBindingView(
	ctx context.Context,
	relayNodeID uuid.UUID,
) (NodeCentricBindingView, error) {
	if repository == nil || repository.pool == nil {
		return NodeCentricBindingView{}, errors.New("store: relay binding repository is unavailable")
	}
	if relayNodeID == uuid.Nil {
		return NodeCentricBindingView{}, ErrInvalidAssetQuery
	}

	row, err := repository.queries.GetNodeCentricBindingView(ctx, nullableUUID(relayNodeID))
	if errors.Is(err, pgx.ErrNoRows) {
		return NodeCentricBindingView{}, ErrAssetNotFound
	}
	if err != nil {
		return NodeCentricBindingView{}, err
	}

	observedAt := row.DbNow.Time.UTC()
	result := NodeCentricBindingView{
		RelayNodeID: uuidFromPG(row.RelayNodeID),
		ObservedAt:  observedAt,
	}

	if !row.BindingID.Valid {
		result.Resolution = RelayBindingResolutionUnbound
		result.ContextSource = AccountContextSourceNone
		result.DirectoryFreshness = DirectoryFreshnessUnavailable
		return result, nil
	}

	binding := RelayNodeGatewayAccountBinding{
		BindingID:          uuidFromPG(row.BindingID),
		RelayNodeID:        uuidFromPG(row.RelayNodeID),
		GatewayInstanceID:  uuidFromPG(row.GatewayInstanceID),
		GatewayAccountID:   row.GatewayAccountID.Int64,
		EvidenceSnapshotID: uuidFromPG(row.EvidenceSnapshotID),
		BoundAt:            row.BoundAt.Time.UTC(),
		BoundBy:            uuidFromPG(row.BoundBy),
		BindReason:         row.BindReason.String,
	}
	result.CurrentBinding = &binding
	gatewayID := binding.GatewayInstanceID
	accountID := binding.GatewayAccountID
	result.GatewayInstanceID = &gatewayID
	result.GatewayAccountID = &accountID

	// Check Directory state for Gateway
	if !row.CurrentSnapshotID.Valid || !row.LastSuccessReceivedAt.Valid {
		result.DirectoryFreshness = DirectoryFreshnessUnavailable
		result.Resolution = RelayBindingResolutionUnknown
		if row.EvidenceAccountID.Valid {
			result.ContextSource = AccountContextSourceLastKnown
			var url *string
			if row.EvidenceUrl.Valid {
				u := row.EvidenceUrl.String
				url = &u
			}
			result.AccountContext = &GatewayAccountContext{
				AccountID: row.EvidenceAccountID.Int64,
				Name:      row.EvidenceName.String,
				Platform:  row.EvidencePlatform.String,
				Type:      row.EvidenceType.String,
				URL:       url,
				Status:    row.EvidenceStatus.String,
			}
		} else {
			result.ContextSource = AccountContextSourceNone
		}
		return result, nil
	}

	lastSuccessAt := row.LastSuccessReceivedAt.Time.UTC()
	result.LastSuccessObservationAt = &lastSuccessAt

	if observedAt.Sub(lastSuccessAt) > 540*time.Second {
		result.DirectoryFreshness = DirectoryFreshnessStale
		result.Resolution = RelayBindingResolutionUnknown
		if row.CurrentAccountID.Valid {
			result.ContextSource = AccountContextSourceLastKnown
			var url *string
			if row.CurrentUrl.Valid {
				u := row.CurrentUrl.String
				url = &u
			}
			result.AccountContext = &GatewayAccountContext{
				AccountID: row.CurrentAccountID.Int64,
				Name:      row.CurrentName.String,
				Platform:  row.CurrentPlatform.String,
				Type:      row.CurrentType.String,
				URL:       url,
				Status:    row.CurrentStatus.String,
			}
		} else if row.EvidenceAccountID.Valid {
			result.ContextSource = AccountContextSourceLastKnown
			var url *string
			if row.EvidenceUrl.Valid {
				u := row.EvidenceUrl.String
				url = &u
			}
			result.AccountContext = &GatewayAccountContext{
				AccountID: row.EvidenceAccountID.Int64,
				Name:      row.EvidenceName.String,
				Platform:  row.EvidencePlatform.String,
				Type:      row.EvidenceType.String,
				URL:       url,
				Status:    row.EvidenceStatus.String,
			}
		} else {
			result.ContextSource = AccountContextSourceNone
		}
		return result, nil
	}

	result.DirectoryFreshness = DirectoryFreshnessFresh
	if row.CurrentAccountID.Valid {
		result.Resolution = RelayBindingResolutionResolved
		result.ContextSource = AccountContextSourceCurrent
		var url *string
		if row.CurrentUrl.Valid {
			u := row.CurrentUrl.String
			url = &u
		}
		result.AccountContext = &GatewayAccountContext{
			AccountID: row.CurrentAccountID.Int64,
			Name:      row.CurrentName.String,
			Platform:  row.CurrentPlatform.String,
			Type:      row.CurrentType.String,
			URL:       url,
			Status:    row.CurrentStatus.String,
		}
	} else {
		result.Resolution = RelayBindingResolutionUnresolved
		result.ContextSource = AccountContextSourceNone
	}

	return result, nil
}

func (repository *RelayBindingRepository) GetGatewayAccountCentricBindingView(
	ctx context.Context,
	gatewayInstanceID uuid.UUID,
) (GatewayAccountCentricBindingView, error) {
	if repository == nil || repository.pool == nil {
		return GatewayAccountCentricBindingView{}, errors.New("store: relay binding repository is unavailable")
	}
	if gatewayInstanceID == uuid.Nil {
		return GatewayAccountCentricBindingView{}, ErrInvalidAssetQuery
	}

	rows, err := repository.queries.ListGatewayAccountCentricBindingViews(ctx, nullableUUID(gatewayInstanceID))
	if err != nil {
		return GatewayAccountCentricBindingView{}, err
	}
	if len(rows) == 0 {
		return GatewayAccountCentricBindingView{}, ErrAssetNotFound
	}

	firstRow := rows[0]
	observedAt := firstRow.DbNow.Time.UTC()
	view := GatewayAccountCentricBindingView{
		GatewayInstanceID: gatewayInstanceID,
		ObservedAt:        observedAt,
		Accounts:          []GatewayAccountCentricBindingItem{},
	}

	if firstRow.CurrentSnapshotID.Valid && firstRow.LastSuccessReceivedAt.Valid {
		snapshotID := uuidFromPG(firstRow.CurrentSnapshotID)
		view.CurrentSnapshotID = &snapshotID
		lastSuccessAt := firstRow.LastSuccessReceivedAt.Time.UTC()
		view.LastSuccessObservationAt = &lastSuccessAt
		if observedAt.Sub(lastSuccessAt) > 540*time.Second {
			view.DirectoryFreshness = DirectoryFreshnessStale
		} else {
			view.DirectoryFreshness = DirectoryFreshnessFresh
		}
	} else {
		view.DirectoryFreshness = DirectoryFreshnessUnavailable
	}

	for _, row := range rows {
		// If the snapshot has no items (empty snapshot or unavailable directory), gateway_account_id is NULL
		if !row.GatewayAccountID.Valid {
			continue
		}

		var url *string
		if row.Url.Valid {
			u := row.Url.String
			url = &u
		}
		item := GatewayAccountCentricBindingItem{
			GatewayAccountID: row.GatewayAccountID.Int64,
			AccountContext: GatewayAccountContext{
				AccountID: row.GatewayAccountID.Int64,
				Name:      row.Name.String,
				Platform:  row.Platform.String,
				Type:      row.Type.String,
				URL:       url,
				Status:    row.Status.String,
			},
		}

		if row.BindingID.Valid {
			binding := RelayNodeGatewayAccountBinding{
				BindingID:          uuidFromPG(row.BindingID),
				RelayNodeID:        uuidFromPG(row.RelayNodeID),
				GatewayInstanceID:  gatewayInstanceID,
				GatewayAccountID:   row.GatewayAccountID.Int64,
				EvidenceSnapshotID: uuidFromPG(row.EvidenceSnapshotID),
				BoundAt:            row.BoundAt.Time.UTC(),
				BoundBy:            uuidFromPG(row.BoundBy),
				BindReason:         row.BindReason.String,
			}
			item.CurrentBinding = &binding
			nodeID := binding.RelayNodeID
			item.BoundRelayNodeID = &nodeID

			if view.DirectoryFreshness == DirectoryFreshnessFresh {
				item.Resolution = RelayBindingResolutionResolved
				item.ContextSource = AccountContextSourceCurrent
			} else {
				item.Resolution = RelayBindingResolutionUnknown
				item.ContextSource = AccountContextSourceLastKnown
			}
		} else {
			item.Resolution = RelayBindingResolutionUnbound
			if view.DirectoryFreshness == DirectoryFreshnessFresh {
				item.ContextSource = AccountContextSourceCurrent
			} else {
				item.ContextSource = AccountContextSourceLastKnown
			}
		}

		view.Accounts = append(view.Accounts, item)
	}

	return view, nil
}

func (repository *RelayBindingRepository) ListUnresolvedGatewayAccountBindings(
	ctx context.Context,
	gatewayInstanceID uuid.UUID,
) ([]UnresolvedGatewayAccountBindingItem, error) {
	if repository == nil || repository.pool == nil {
		return nil, errors.New("store: relay binding repository is unavailable")
	}
	if gatewayInstanceID == uuid.Nil {
		return nil, ErrInvalidAssetQuery
	}

	rows, err := repository.queries.ListUnresolvedGatewayAccountBindings(ctx, nullableUUID(gatewayInstanceID))
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		_, err := repository.queries.LockGatewayDirectoryInstance(ctx, nullableUUID(gatewayInstanceID))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAssetNotFound
		}
		return []UnresolvedGatewayAccountBindingItem{}, nil
	}

	items := make([]UnresolvedGatewayAccountBindingItem, 0, len(rows))
	for _, row := range rows {
		observedAt := row.DbNow.Time.UTC()
		binding := RelayNodeGatewayAccountBinding{
			BindingID:          uuidFromPG(row.BindingID),
			RelayNodeID:        uuidFromPG(row.RelayNodeID),
			GatewayInstanceID:  uuidFromPG(row.GatewayInstanceID),
			GatewayAccountID:   row.GatewayAccountID,
			EvidenceSnapshotID: uuidFromPG(row.EvidenceSnapshotID),
			BoundAt:            row.BoundAt.Time.UTC(),
			BoundBy:            uuidFromPG(row.BoundBy),
			BindReason:         row.BindReason,
		}

		var lastKnownCtx *GatewayAccountContext
		if row.EvidenceName.Valid {
			var url *string
			if row.EvidenceUrl.Valid {
				u := row.EvidenceUrl.String
				url = &u
			}
			lastKnownCtx = &GatewayAccountContext{
				AccountID: row.GatewayAccountID,
				Name:      row.EvidenceName.String,
				Platform:  row.EvidencePlatform.String,
				Type:      row.EvidenceType.String,
				URL:       url,
				Status:    row.EvidenceStatus.String,
			}
		}

		item := UnresolvedGatewayAccountBindingItem{
			CurrentBinding:          binding,
			ObservedAt:              observedAt,
			LastKnownAccountContext: lastKnownCtx,
		}

		if row.LastSuccessReceivedAt.Valid {
			t := row.LastSuccessReceivedAt.Time.UTC()
			item.LastSuccessObservationAt = &t
			if observedAt.Sub(t) > 540*time.Second {
				item.DirectoryFreshness = DirectoryFreshnessStale
				item.Resolution = RelayBindingResolutionUnknown
				item.ContextSource = AccountContextSourceLastKnown
			} else {
				item.DirectoryFreshness = DirectoryFreshnessFresh
				item.Resolution = RelayBindingResolutionUnresolved
				item.ContextSource = AccountContextSourceNone
			}
		} else {
			item.DirectoryFreshness = DirectoryFreshnessUnavailable
			item.Resolution = RelayBindingResolutionUnknown
			item.ContextSource = AccountContextSourceLastKnown
		}

		items = append(items, item)
	}

	return items, nil
}

func bindingFromSQLC(row generated.RelayNodeGatewayAccountBinding) RelayNodeGatewayAccountBinding {
	var endedAt *time.Time
	if row.EndedAt.Valid {
		t := row.EndedAt.Time.UTC()
		endedAt = &t
	}
	var endedBy *uuid.UUID
	if row.EndedBy.Valid {
		u := uuidFromPG(row.EndedBy)
		endedBy = &u
	}
	var endReason *string
	if row.EndReason.Valid {
		r := row.EndReason.String
		endReason = &r
	}
	return RelayNodeGatewayAccountBinding{
		BindingID:          uuidFromPG(row.BindingID),
		RelayNodeID:        uuidFromPG(row.RelayNodeID),
		GatewayInstanceID:  uuidFromPG(row.GatewayInstanceID),
		GatewayAccountID:   row.GatewayAccountID,
		EvidenceSnapshotID: uuidFromPG(row.EvidenceSnapshotID),
		BoundAt:            row.BoundAt.Time.UTC(),
		BoundBy:            uuidFromPG(row.BoundBy),
		BindReason:         row.BindReason,
		EndedAt:            endedAt,
		EndedBy:            endedBy,
		EndReason:          endReason,
	}
}
