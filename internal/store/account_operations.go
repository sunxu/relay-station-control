package store

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const accountCommandDomain = "account_admin"
const accountUploadIntentHMACDomain = "relay-station/account-operation-upload-intent/v1"

type AccountOperationKind string

const (
	AccountDisable         AccountOperationKind = "disable"
	AccountEnable          AccountOperationKind = "enable"
	AccountRemove          AccountOperationKind = "remove"
	AccountUploadNew       AccountOperationKind = "upload_new"
	AccountReplaceExisting AccountOperationKind = "replace_existing"
)

type AccountOperationState string

const (
	AccountPrepared       AccountOperationState = "prepared"
	AccountDispatched     AccountOperationState = "dispatched"
	AccountRemoteApplied  AccountOperationState = "remote_applied"
	AccountRemoteNoop     AccountOperationState = "remote_noop"
	AccountOutcomeUnknown AccountOperationState = "outcome_unknown"
	AccountFailed         AccountOperationState = "failed"
)

type AccountFailurePhase string

const (
	AccountPreAcceptance         AccountFailurePhase = "pre_acceptance"
	AccountPreDispatchPostAccept AccountFailurePhase = "post_acceptance_pre_dispatch"
	AccountDispatchedPhase       AccountFailurePhase = "dispatched"
)

type AccountFailure struct {
	Code       string
	HTTPStatus int
	Phase      AccountFailurePhase
}

var accountFailureHTTPStatus = map[string]int{
	"authentication_required": 401, "authorization_required": 403, "csrf_failed": 403,
	"invalid_request": 400, "upload_too_large": 413, "upload_invalid": 400,
	"identity_mismatch": 400, "node_not_found": 404,
	"unsupported_provider": 409, "node_retired": 409,
	"node_monitoring_ineligible": 409, "account_target_not_found": 409,
	"account_target_ambiguous": 409, "account_target_exists": 409,
	"account_filename_conflict": 409, "account_operation_in_progress": 409,
	"unsupported_node_version": 503, "node_management_unavailable": 503,
	"service_unavailable": 503,
}

func NewAccountFailure(code string, phase AccountFailurePhase) (AccountFailure, error) {
	status, ok := accountFailureHTTPStatus[code]
	if !ok || (phase != AccountPreAcceptance && phase != AccountPreDispatchPostAccept && phase != AccountDispatchedPhase) {
		return AccountFailure{}, fmt.Errorf("store: unknown account failure %q/%q", code, phase)
	}
	return AccountFailure{Code: code, HTTPStatus: status, Phase: phase}, nil
}

var (
	ErrInvalidAccountOperation     = errors.New("store: invalid account operation")
	ErrAccountOperationNotFound    = errors.New("store: account operation not found")
	ErrAccountOperationState       = errors.New("store: invalid account operation state")
	ErrAccountOperationBlocked     = errors.New("store: account operation in progress")
	ErrAccountIntentKeyUnavailable = errors.New("store: account operation intent key unavailable")
	ErrAccountReceiptAlreadyExists = errors.New("store: account command receipt already exists")
)

type AccountAdminOperation struct {
	CommandID                   uuid.UUID
	NodeInstanceID              uuid.UUID
	AccountKey                  string
	OperationKind               AccountOperationKind
	ExecutionState              AccountOperationState
	DispatchStartedAt           *time.Time
	RemoteResultCode            *string
	UploadFingerprintKeyVersion *int16
	UploadIntentFingerprint     []byte
	LifecycleOverrideAt         *time.Time
	LifecycleOverrideBy         *uuid.UUID
	LifecycleOverrideReason     *string
	SameAccountOverrideAt       *time.Time
	SameAccountOverrideBy       *uuid.UUID
	SameAccountOverrideReason   *string
	CreatedAt                   time.Time
	UpdatedAt                   time.Time
}

type AccountOperationAcceptance struct {
	CommandID                uuid.UUID
	ActorAdminID             uuid.UUID
	OperationKind            AccountOperationKind
	NodeInstanceID           uuid.UUID
	AccountKey               string
	CanonicalIntentHash      []byte
	SecretFingerprintVersion *int16
	UploadIntentFingerprint  []byte
}

type AccountCommandReceipt struct {
	CommandID                uuid.UUID
	TargetOperationCommandID *uuid.UUID
	ActorAdminID             uuid.UUID
	CommandKind              string
	IntentEncodingVersion    int16
	CanonicalIntentHash      []byte
	SecretFingerprintVersion *int16
	HTTPStatus               int
	ContentType              string
	ResponseBody             []byte
	CommittedAt              time.Time
}

type AccountOperationRepository struct{ pool *pgxpool.Pool }

func NewAccountOperationRepository(pool *pgxpool.Pool) (*AccountOperationRepository, error) {
	if pool == nil {
		return nil, errors.New("store: nil account operation pool")
	}
	return &AccountOperationRepository{pool: pool}, nil
}

func accountCommandKind(kind AccountOperationKind) string { return "account." + string(kind) }

func validAccountOperationKind(kind AccountOperationKind) bool {
	switch kind {
	case AccountDisable, AccountEnable, AccountRemove, AccountUploadNew, AccountReplaceExisting:
		return true
	default:
		return false
	}
}

func (r *AccountOperationRepository) Accept(ctx context.Context, command AccountOperationAcceptance) (AccountAdminOperation, error) {
	if command.CommandID == uuid.Nil || command.ActorAdminID == uuid.Nil || command.NodeInstanceID == uuid.Nil ||
		!validAccountOperationKind(command.OperationKind) || len(command.CanonicalIntentHash) != sha256.Size || strings.TrimSpace(command.AccountKey) == "" {
		return AccountAdminOperation{}, ErrInvalidAccountOperation
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return AccountAdminOperation{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockAdminCommand(ctx, tx, command.CommandID); err != nil {
		return AccountAdminOperation{}, err
	}
	reservation, exists, err := lookupAccountCommandReservation(ctx, tx, command.CommandID)
	if err != nil {
		return AccountAdminOperation{}, err
	}
	if exists {
		if !reservation.matches(command.ActorAdminID, accountCommandKind(command.OperationKind), command.CanonicalIntentHash, command.SecretFingerprintVersion) {
			return AccountAdminOperation{}, ErrCommandConflict
		}
		operation, err := scanAccountOperation(tx.QueryRow(ctx, accountOperationSelect+" WHERE command_id=$1", command.CommandID))
		if errors.Is(err, pgx.ErrNoRows) {
			return AccountAdminOperation{}, ErrCommandRegistryInconsistent
		}
		return operation, err
	}
	operation, err := callAcceptAccountOperation(ctx, tx, command)
	if err != nil {
		return AccountAdminOperation{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return AccountAdminOperation{}, err
	}
	return operation, nil
}

// AcceptWithIntentKey composes the frozen pre-acceptance secret boundary with
// acceptance. A key failure returns before opening a database transaction.
func (r *AccountOperationRepository) AcceptWithIntentKey(ctx context.Context, keyPath string, command AccountOperationAcceptance, canonicalIntent []byte) (AccountAdminOperation, error) {
	key, err := LoadAccountOperationIntentKey(keyPath)
	if err != nil {
		return AccountAdminOperation{}, err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(accountUploadIntentHMACDomain))
	_, _ = mac.Write(canonicalIntent)
	version := int16(1)
	command.SecretFingerprintVersion = &version
	command.UploadIntentFingerprint = mac.Sum(nil)
	return r.Accept(ctx, command)
}

func lookupAccountCommandReservation(ctx context.Context, tx pgx.Tx, id uuid.UUID) (adminCommandReservation, bool, error) {
	var r adminCommandReservation
	err := tx.QueryRow(ctx, `SELECT actor_admin_id,command_domain,command_kind,intent_encoding_version,canonical_intent_hash,secret_fingerprint_key_version FROM admin_command_registry WHERE command_id=$1`, id).Scan(&r.ActorAdminID, &r.CommandDomain, &r.CommandKind, &r.IntentEncodingVersion, &r.CanonicalIntentHash, &r.SecretFingerprintKeyVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, false, nil
	}
	return r, err == nil, err
}

func (r adminCommandReservation) matches(actor uuid.UUID, kind string, hash []byte, keyVersion *int16) bool {
	return r.ActorAdminID == actor && r.CommandDomain == accountCommandDomain && r.CommandKind == kind && r.IntentEncodingVersion == 1 && hmac.Equal(r.CanonicalIntentHash, hash) && equalOptionalInt16(r.SecretFingerprintKeyVersion, keyVersion)
}

const accountOperationSelect = `SELECT command_id,node_instance_id,account_key,operation_kind,execution_state,dispatch_started_at,remote_result_code,upload_fingerprint_key_version,upload_intent_fingerprint,lifecycle_override_at,lifecycle_override_by,lifecycle_override_reason,same_account_override_at,same_account_override_by,same_account_override_reason,created_at,updated_at FROM account_admin_operations`

type rowScanner interface{ Scan(...any) error }

func scanAccountOperation(row rowScanner) (AccountAdminOperation, error) {
	var v AccountAdminOperation
	var kind, state string
	err := row.Scan(&v.CommandID, &v.NodeInstanceID, &v.AccountKey, &kind, &state, &v.DispatchStartedAt, &v.RemoteResultCode, &v.UploadFingerprintKeyVersion, &v.UploadIntentFingerprint, &v.LifecycleOverrideAt, &v.LifecycleOverrideBy, &v.LifecycleOverrideReason, &v.SameAccountOverrideAt, &v.SameAccountOverrideBy, &v.SameAccountOverrideReason, &v.CreatedAt, &v.UpdatedAt)
	v.OperationKind, v.ExecutionState = AccountOperationKind(kind), AccountOperationState(state)
	return v, err
}

func callAcceptAccountOperation(ctx context.Context, tx pgx.Tx, c AccountOperationAcceptance) (AccountAdminOperation, error) {
	return scanAccountOperation(tx.QueryRow(ctx, `SELECT * FROM control_accept_account_admin_operation_v1($1,$2,$3,$4,$5,$6,$7,$8,$9)`, c.CommandID, c.ActorAdminID, accountCommandKind(c.OperationKind), c.CanonicalIntentHash, nullableInt2(c.SecretFingerprintVersion), c.NodeInstanceID, c.AccountKey, c.OperationKind, c.UploadIntentFingerprint))
}

// TransitionAccountOperation applies the frozen explicit transition matrix.
// The operation row lock is held only for the database transition.
func (r *AccountOperationRepository) TransitionAccountOperation(ctx context.Context, id uuid.UUID, from, to AccountOperationState) (AccountAdminOperation, error) {
	if from == AccountPrepared || !allowedAccountTransition(from, to) {
		return AccountAdminOperation{}, ErrAccountOperationState
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return AccountAdminOperation{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockAdminCommand(ctx, tx, id); err != nil {
		return AccountAdminOperation{}, err
	}
	var current AccountOperationState
	if err = tx.QueryRow(ctx, `SELECT execution_state FROM account_admin_operations WHERE command_id=$1 FOR UPDATE`, id).Scan(&current); errors.Is(err, pgx.ErrNoRows) {
		return AccountAdminOperation{}, ErrAccountOperationNotFound
	} else if err != nil {
		return AccountAdminOperation{}, err
	}
	if current != from {
		return AccountAdminOperation{}, ErrAccountOperationState
	}
	if _, err = scanAccountOperation(tx.QueryRow(ctx, `SELECT * FROM control_transition_account_admin_operation_v1($1,$2,$3)`, id, from, to)); err != nil {
		return AccountAdminOperation{}, err
	}
	op, err := scanAccountOperation(tx.QueryRow(ctx, accountOperationSelect+` WHERE command_id=$1`, id))
	if err != nil {
		return AccountAdminOperation{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return AccountAdminOperation{}, err
	}
	return op, nil
}

func allowedAccountTransition(from, to AccountOperationState) bool {
	switch from {
	case AccountPrepared:
		return to == AccountDispatched || to == AccountRemoteNoop || to == AccountFailed
	case AccountDispatched:
		return to == AccountRemoteApplied || to == AccountFailed || to == AccountOutcomeUnknown
	default:
		return false
	}
}

func (r *AccountOperationRepository) TerminalizePreDispatchFailure(ctx context.Context, id uuid.UUID, failure AccountFailure, requestID string) error {
	if failure.Phase != AccountPreDispatchPostAccept || failure.Code == "" {
		return ErrInvalidAccountOperation
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockAdminCommand(ctx, tx, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT control_terminalize_account_operation_failure_v1($1,$2,$3)`, id, failure.Code, requestID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AdmitAccountDispatch atomically classifies the same-account blocker and the
// prepared -> dispatched transition. The caller may send native HTTP only
// after this method commits.
func (r *AccountOperationRepository) AdmitAccountDispatch(ctx context.Context, id, nodeID uuid.UUID, accountKey, requestID string) (bool, AccountAdminOperation, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, AccountAdminOperation{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockAdminCommand(ctx, tx, id); err != nil {
		return false, AccountAdminOperation{}, err
	}
	var admitted bool
	if err = tx.QueryRow(ctx, `SELECT control_admit_account_dispatch_v1($1,$2,$3,$4)`, id, nodeID, accountKey, requestID).Scan(&admitted); err != nil {
		return false, AccountAdminOperation{}, err
	}
	op, err := scanAccountOperation(tx.QueryRow(ctx, accountOperationSelect+` WHERE command_id=$1`, id))
	if err != nil {
		return false, AccountAdminOperation{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, AccountAdminOperation{}, err
	}
	return admitted, op, nil
}

func (r *AccountOperationRepository) SameAccountBlocked(ctx context.Context, nodeID uuid.UUID, accountKey string, except uuid.UUID) (bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	err = LockSameAccountBlocker(ctx, tx, nodeID, accountKey, except)
	if errors.Is(err, ErrAccountOperationBlocked) {
		return true, nil
	}
	return false, err
}

// LockSameAccountBlocker is retained as a narrow blocker inspection helper;
// atomic dispatch admission and row locking are owned by the controlled SQL
// function. No caller may hold a database transaction across native I/O.
func LockSameAccountBlocker(ctx context.Context, tx pgx.Tx, nodeID uuid.UUID, accountKey string, except uuid.UUID) error {
	var blocker uuid.UUID
	err := tx.QueryRow(ctx, `SELECT command_id FROM account_admin_operations WHERE node_instance_id=$1 AND account_key=$2 AND command_id<>$3 AND execution_state IN ('dispatched','outcome_unknown') AND same_account_override_at IS NULL ORDER BY command_id LIMIT 1`, nodeID, accountKey, except).Scan(&blocker)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return ErrAccountOperationBlocked
}

func (r *AccountOperationRepository) Receipt(ctx context.Context, commandID uuid.UUID) (AccountCommandReceipt, error) {
	var receipt AccountCommandReceipt
	err := r.pool.QueryRow(ctx, `SELECT command_id,target_operation_command_id,actor_admin_id,command_kind,intent_encoding_version,canonical_intent_hash,secret_fingerprint_key_version,http_status,content_type,response_body,committed_at FROM account_admin_command_receipts WHERE command_id=$1`, commandID).Scan(&receipt.CommandID, &receipt.TargetOperationCommandID, &receipt.ActorAdminID, &receipt.CommandKind, &receipt.IntentEncodingVersion, &receipt.CanonicalIntentHash, &receipt.SecretFingerprintVersion, &receipt.HTTPStatus, &receipt.ContentType, &receipt.ResponseBody, &receipt.CommittedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return AccountCommandReceipt{}, ErrAccountOperationNotFound
	}
	return receipt, err
}

// ReplayTerminal performs the exact terminal-command lookup used before any
// fresh admission checks. It validates the global reservation identity and
// returns only the immutable stored response.
func (r *AccountOperationRepository) ReplayTerminal(ctx context.Context, command AccountOperationAcceptance) (AccountCommandReceipt, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return AccountCommandReceipt{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockAdminCommand(ctx, tx, command.CommandID); err != nil {
		return AccountCommandReceipt{}, err
	}
	reservation, exists, err := lookupAccountCommandReservation(ctx, tx, command.CommandID)
	if err != nil {
		return AccountCommandReceipt{}, err
	}
	if !exists || !reservation.matches(command.ActorAdminID, accountCommandKind(command.OperationKind), command.CanonicalIntentHash, command.SecretFingerprintVersion) {
		return AccountCommandReceipt{}, ErrCommandConflict
	}
	var receipt AccountCommandReceipt
	err = tx.QueryRow(ctx, `SELECT command_id,target_operation_command_id,actor_admin_id,command_kind,intent_encoding_version,canonical_intent_hash,secret_fingerprint_key_version,http_status,content_type,response_body,committed_at FROM account_admin_command_receipts WHERE command_id=$1`, command.CommandID).Scan(&receipt.CommandID, &receipt.TargetOperationCommandID, &receipt.ActorAdminID, &receipt.CommandKind, &receipt.IntentEncodingVersion, &receipt.CanonicalIntentHash, &receipt.SecretFingerprintVersion, &receipt.HTTPStatus, &receipt.ContentType, &receipt.ResponseBody, &receipt.CommittedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return AccountCommandReceipt{}, ErrAccountOperationState
	}
	if err != nil {
		return AccountCommandReceipt{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return AccountCommandReceipt{}, err
	}
	return receipt, nil
}

func LoadAccountOperationIntentKey(path string) ([]byte, error) {
	if path == "" {
		return nil, ErrAccountIntentKeyUnavailable
	}
	info, err := os.Lstat(filepath.Clean(path))
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0600 {
		return nil, ErrAccountIntentKeyUnavailable
	}
	key, err := os.ReadFile(path)
	if err != nil || len(key) != sha256.Size {
		return nil, ErrAccountIntentKeyUnavailable
	}
	return append([]byte(nil), key...), nil
}
