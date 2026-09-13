package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/sunxu/relay-station-control/internal/drivers"
	"github.com/sunxu/relay-station-control/internal/drivers/gatewaydirectory"
	generated "github.com/sunxu/relay-station-control/internal/store/sqlc"
)

var ErrGatewayDirectoryIngestionLostLease = errors.New("store: gateway directory ingestion lease lost")

type GatewayDirectoryAttemptRequest struct {
	IngestionRunID    uuid.UUID
	GatewayInstanceID uuid.UUID
	LeaseFencingToken uuid.UUID
}

type GatewayDirectoryAttemptSuccess struct {
	Request      GatewayDirectoryAttemptRequest
	Directory    gatewaydirectory.DirectoryResponse
	GeneratedAt  time.Time
	Fingerprint  [sha256.Size]byte
	AccountCount int
}

type GatewayDirectoryAttemptFailureDisposition string

const (
	GatewayDirectoryAttemptRetryWait GatewayDirectoryAttemptFailureDisposition = "retry_wait"
	GatewayDirectoryAttemptFailed    GatewayDirectoryAttemptFailureDisposition = "failed"
	GatewayDirectoryAttemptLostLease GatewayDirectoryAttemptFailureDisposition = "lost_lease"
)

type GatewayDirectoryAttemptFailure struct {
	Request     GatewayDirectoryAttemptRequest
	Class       string
	Retryable   bool
	Disposition GatewayDirectoryAttemptFailureDisposition
}

type GatewayDirectoryAttemptResult struct {
	Success *GatewayDirectoryAttemptSuccess
	Failure *GatewayDirectoryAttemptFailure
}

type GatewayDirectoryFinalizeOutcome string

const (
	GatewayDirectoryFinalizeOutcomeChanged   GatewayDirectoryFinalizeOutcome = "changed"
	GatewayDirectoryFinalizeOutcomeUnchanged GatewayDirectoryFinalizeOutcome = "unchanged"
)

type GatewayDirectoryFinalizeFailureReason string

const (
	GatewayDirectoryFinalizeFailureLostLease         GatewayDirectoryFinalizeFailureReason = "lost_lease"
	GatewayDirectoryFinalizeFailureSourceTimeInvalid GatewayDirectoryFinalizeFailureReason = "source_time_invalid"
	GatewayDirectoryFinalizeFailureGatewayInactive   GatewayDirectoryFinalizeFailureReason = "gateway_inactive"
)

type FinalizeSuccessResult struct {
	Outcome    GatewayDirectoryFinalizeOutcome
	SnapshotID uuid.UUID
	ReceivedAt time.Time
}

type GatewayDirectoryFinalizeFailure struct {
	Reason GatewayDirectoryFinalizeFailureReason
}

type GatewayDirectoryFinalizeResult struct {
	Success *FinalizeSuccessResult
	Failure *GatewayDirectoryFinalizeFailure
}

type GatewayDirectoryAttemptFailureInput struct {
	Class     string
	Retryable bool
}

func (repository *GatewayDirectoryIngestionRepository) ExecuteAttempt(
	ctx context.Context,
	request GatewayDirectoryAttemptRequest,
	secretResolver drivers.SecretResolver,
) (GatewayDirectoryAttemptResult, error) {
	return repository.executeAttempt(ctx, ctx, request, secretResolver)
}

// executeAttempt keeps the attempt deadline separate from bounded failure bookkeeping.
func (repository *GatewayDirectoryIngestionRepository) executeAttempt(
	ctx, failureParent context.Context,
	request GatewayDirectoryAttemptRequest,
	secretResolver drivers.SecretResolver,
) (GatewayDirectoryAttemptResult, error) {
	if request.IngestionRunID == uuid.Nil || request.GatewayInstanceID == uuid.Nil ||
		request.LeaseFencingToken == uuid.Nil || secretResolver == nil {
		return GatewayDirectoryAttemptResult{}, ErrInvalidGatewayDirectoryIngestionQuery
	}
	lease, err := repository.queries.GetGatewayDirectoryIngestionRunLease(ctx,
		generated.GetGatewayDirectoryIngestionRunLeaseParams{
			IngestionRunID:    nullableUUID(request.IngestionRunID),
			GatewayInstanceID: nullableUUID(request.GatewayInstanceID),
			LeaseFencingToken: nullableUUID(request.LeaseFencingToken),
		})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !validGatewayDirectoryIngestionRun(lease)) {
		return GatewayDirectoryAttemptResult{
			Failure: &GatewayDirectoryAttemptFailure{
				Request:     request,
				Class:       string(gatewaydirectoryFailureClassLeaseLost),
				Retryable:   false,
				Disposition: GatewayDirectoryAttemptLostLease,
			},
		}, nil
	}
	if err != nil {
		return GatewayDirectoryAttemptResult{}, err
	}

	target, err := repository.queries.GetGatewayDirectoryReadTarget(ctx, generated.GetGatewayDirectoryReadTargetParams{
		IngestionRunID: nullableUUID(request.IngestionRunID), GatewayInstanceID: nullableUUID(request.GatewayInstanceID), LeaseFencingToken: nullableUUID(request.LeaseFencingToken),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		failureClass, classifyErr := repository.gatewayLifecycleFailureClass(ctx, request.GatewayInstanceID)
		if classifyErr != nil {
			return GatewayDirectoryAttemptResult{}, classifyErr
		}
		transition, transitionErr := repository.finishAttemptFailure(failureParent, request, GatewayDirectoryAttemptFailureInput{
			Class: string(failureClass), Retryable: false,
		})
		if transitionErr != nil {
			return GatewayDirectoryAttemptResult{}, transitionErr
		}
		if transition == nil {
			return GatewayDirectoryAttemptResult{Failure: &GatewayDirectoryAttemptFailure{Request: request, Class: string(gatewaydirectoryFailureClassLeaseLost), Retryable: false, Disposition: GatewayDirectoryAttemptLostLease}}, nil
		}
		return GatewayDirectoryAttemptResult{Failure: transition}, nil
	}
	if err != nil {
		return GatewayDirectoryAttemptResult{}, err
	}
	if target.ReaderSecretRef == "" || target.ManagementEndpoint == "" {
		return GatewayDirectoryAttemptResult{}, ErrGatewayDirectoryIngestionInconsistent
	}

	client, err := gatewaydirectory.NewClient(target.ManagementEndpoint, secretResolver)
	if err != nil {
		return GatewayDirectoryAttemptResult{}, err
	}
	directory, fingerprint, err := client.Fetch(ctx, drivers.NewSecretReference(target.ReaderSecretRef))
	if err != nil {
		class, retryable, classifyErr := classifyGatewayDirectoryAttemptFailure(err)
		if classifyErr != nil {
			return GatewayDirectoryAttemptResult{}, classifyErr
		}
		transition, transitionErr := repository.finishAttemptFailure(failureParent, request, GatewayDirectoryAttemptFailureInput{
			Class:     class,
			Retryable: retryable,
		})
		if transitionErr != nil {
			return GatewayDirectoryAttemptResult{}, transitionErr
		}
		return GatewayDirectoryAttemptResult{Failure: transition}, nil
	}

	var previousGeneratedAt *time.Time
	currentState, err := repository.queries.GetGatewayDirectoryCurrentState(ctx, nullableUUID(request.GatewayInstanceID))
	if errors.Is(err, pgx.ErrNoRows) {
		currentState = generated.GatewayDirectoryCurrentState{}
	} else if err != nil {
		return GatewayDirectoryAttemptResult{}, err
	} else if currentState.LastSourceGeneratedAt.Valid {
		previous := currentState.LastSourceGeneratedAt.Time.UTC()
		previousGeneratedAt = &previous
	}

	dbNow, err := repository.queries.GetGatewayDirectoryAttemptNow(ctx)
	if err != nil {
		return GatewayDirectoryAttemptResult{}, err
	}
	if !dbNow.Valid {
		return GatewayDirectoryAttemptResult{}, ErrGatewayDirectoryIngestionInconsistent
	}
	if err := gatewaydirectory.ValidateSourceTime(gatewaydirectory.DefaultSourceTimePolicy(), directory.GeneratedAt, dbNow.Time.UTC(), previousGeneratedAt); err != nil {
		transition, transitionErr := repository.finishAttemptFailure(failureParent, request, GatewayDirectoryAttemptFailureInput{
			Class:     string(gatewaydirectoryFailureClassSourceTimeInvalid),
			Retryable: false,
		})
		if transitionErr != nil {
			return GatewayDirectoryAttemptResult{}, transitionErr
		}
		if transition == nil {
			return GatewayDirectoryAttemptResult{
				Failure: &GatewayDirectoryAttemptFailure{
					Request:     request,
					Class:       string(gatewaydirectoryFailureClassLeaseLost),
					Retryable:   false,
					Disposition: GatewayDirectoryAttemptLostLease,
				},
			}, nil
		}
		return GatewayDirectoryAttemptResult{Failure: transition}, nil
	}

	accountCount := len(directory.Accounts)
	return GatewayDirectoryAttemptResult{
		Success: &GatewayDirectoryAttemptSuccess{
			Request:      request,
			Directory:    directory,
			GeneratedAt:  directory.GeneratedAt,
			Fingerprint:  fingerprint,
			AccountCount: accountCount,
		},
	}, nil
}

func (repository *GatewayDirectoryIngestionRepository) finishAttemptFailure(
	parent context.Context, request GatewayDirectoryAttemptRequest, input GatewayDirectoryAttemptFailureInput,
) (*GatewayDirectoryAttemptFailure, error) {
	if err := parent.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	return repository.recordAttemptFailure(ctx, request, input)
}

func (repository *GatewayDirectoryIngestionRepository) recordAttemptFailure(
	ctx context.Context,
	request GatewayDirectoryAttemptRequest,
	input GatewayDirectoryAttemptFailureInput,
) (*GatewayDirectoryAttemptFailure, error) {
	if input.Class == "" || request.IngestionRunID == uuid.Nil || request.GatewayInstanceID == uuid.Nil ||
		request.LeaseFencingToken == uuid.Nil {
		return nil, ErrInvalidGatewayDirectoryIngestionQuery
	}
	row, err := repository.queries.RecordGatewayDirectoryAttemptFailure(ctx,
		generated.RecordGatewayDirectoryAttemptFailureParams{
			IngestionRunID:    nullableUUID(request.IngestionRunID),
			GatewayInstanceID: nullableUUID(request.GatewayInstanceID),
			LeaseFencingToken: nullableUUID(request.LeaseFencingToken),
			Retryable:         input.Retryable,
			LastFailureClass:  input.Class,
		})
	if errors.Is(err, pgx.ErrNoRows) {
		return &GatewayDirectoryAttemptFailure{
			Request:     request,
			Class:       input.Class,
			Retryable:   input.Retryable,
			Disposition: GatewayDirectoryAttemptLostLease,
		}, nil
	}
	if err != nil {
		return nil, err
	}
	if !row.IngestionRunID.Valid || row.Status == "" {
		return nil, ErrGatewayDirectoryIngestionInconsistent
	}
	disposition := GatewayDirectoryAttemptFailed
	if row.Status == "retry_wait" {
		disposition = GatewayDirectoryAttemptRetryWait
	}
	return &GatewayDirectoryAttemptFailure{
		Request:     request,
		Class:       input.Class,
		Retryable:   input.Retryable,
		Disposition: disposition,
	}, nil
}

type gatewaydirectoryFailureClass string

const (
	gatewaydirectoryFailureClassTransport         gatewaydirectoryFailureClass = "transport"
	gatewaydirectoryFailureClassTimeout           gatewaydirectoryFailureClass = "timeout"
	gatewaydirectoryFailureClassPartialRead       gatewaydirectoryFailureClass = "partial_read"
	gatewaydirectoryFailureClassHTTP429           gatewaydirectoryFailureClass = "http_429"
	gatewaydirectoryFailureClassHTTP5xx           gatewaydirectoryFailureClass = "http_5xx"
	gatewaydirectoryFailureClassHTTPNonRetryable  gatewaydirectoryFailureClass = "http_non_retryable"
	gatewaydirectoryFailureClassContractInvalid   gatewaydirectoryFailureClass = "contract_invalid"
	gatewaydirectoryFailureClassSourceTimeInvalid gatewaydirectoryFailureClass = "source_time_invalid"
	gatewaydirectoryFailureClassHardLimit         gatewaydirectoryFailureClass = "hard_limit"
	gatewaydirectoryFailureClassSecretUnavailable gatewaydirectoryFailureClass = "secret_unavailable"
	gatewaydirectoryFailureClassLeaseLost         gatewaydirectoryFailureClass = "lease_lost"
	gatewaydirectoryFailureClassUnknownExecution  gatewaydirectoryFailureClass = "unknown_execution"
	gatewaydirectoryFailureClassGatewayRetired    gatewaydirectoryFailureClass = "gateway_retired"
	gatewaydirectoryFailureClassGatewayReplaced   gatewaydirectoryFailureClass = "gateway_replaced"
)

func lifecycleFailureClass(status, reason string) gatewaydirectoryFailureClass {
	if status != gatewayLifecycleRetired {
		return gatewaydirectoryFailureClassContractInvalid
	}
	switch reason {
	case "administrator_retire":
		return gatewaydirectoryFailureClassGatewayRetired
	case "replacement":
		return gatewaydirectoryFailureClassGatewayReplaced
	default:
		return gatewaydirectoryFailureClassContractInvalid
	}
}

func (repository *GatewayDirectoryIngestionRepository) gatewayLifecycleFailureClass(ctx context.Context, gatewayID uuid.UUID) (gatewaydirectoryFailureClass, error) {
	row, err := repository.queries.GetGatewayDirectoryLifecycleFailure(ctx, nullableUUID(gatewayID))
	if errors.Is(err, pgx.ErrNoRows) {
		return gatewaydirectoryFailureClassContractInvalid, nil
	}
	if err != nil {
		return "", err
	}
	return lifecycleFailureClass(row.LifecycleStatus, row.RetireReason.String), nil
}

func classifyGatewayDirectoryAttemptFailure(err error) (string, bool, error) {
	var fetchErr *gatewaydirectory.FetchError
	switch {
	case errors.Is(err, context.Canceled):
		return "", false, context.Canceled
	case errors.As(err, &fetchErr):
		switch fetchErr.Reason {
		case drivers.ReasonNetworkUnavailable:
			return string(gatewaydirectoryFailureClassTransport), true, nil
		case drivers.ReasonTimeout:
			return string(gatewaydirectoryFailureClassTimeout), true, nil
		case drivers.ReasonPartialRead:
			return string(gatewaydirectoryFailureClassPartialRead), true, nil
		case drivers.ReasonHTTPStatus:
			switch {
			case fetchErr.HTTPStatus == 429:
				return string(gatewaydirectoryFailureClassHTTP429), true, nil
			case fetchErr.HTTPStatus >= 500:
				return string(gatewaydirectoryFailureClassHTTP5xx), true, nil
			default:
				return string(gatewaydirectoryFailureClassHTTPNonRetryable), false, nil
			}
		case drivers.ReasonResponseTooLarge, drivers.ReasonRecordLimit:
			return string(gatewaydirectoryFailureClassHardLimit), false, nil
		case drivers.ReasonContractInvalid, drivers.ReasonResponseInvalid:
			return string(gatewaydirectoryFailureClassContractInvalid), false, nil
		case drivers.ReasonSecretUnavailable, drivers.ReasonSecretReferenceUnknown, drivers.ReasonSecretProviderUnknown, drivers.ReasonSecretFileUnsafe:
			return string(gatewaydirectoryFailureClassSecretUnavailable), false, nil
		case drivers.ReasonTargetRejected, drivers.ReasonTLSRejected, drivers.ReasonRedirectRejected:
			return string(gatewaydirectoryFailureClassContractInvalid), false, nil
		default:
			return string(gatewaydirectoryFailureClassUnknownExecution), true, nil
		}
	default:
		return string(gatewaydirectoryFailureClassUnknownExecution), true, nil
	}
}

func (repository *GatewayDirectoryIngestionRepository) FinalizeSuccessfulAttempt(
	ctx context.Context,
	success GatewayDirectoryAttemptSuccess,
) (GatewayDirectoryFinalizeResult, error) {
	if repository == nil || repository.pool == nil || success.Request.IngestionRunID == uuid.Nil ||
		success.Request.GatewayInstanceID == uuid.Nil || success.Request.LeaseFencingToken == uuid.Nil ||
		success.Directory.SchemaVersion != 1 {
		return GatewayDirectoryFinalizeResult{}, ErrInvalidGatewayDirectoryIngestionQuery
	}
	if success.AccountCount != len(success.Directory.Accounts) {
		return GatewayDirectoryFinalizeResult{}, ErrGatewayDirectoryIngestionInconsistent
	}
	if success.Directory.FingerprintV1() != success.Fingerprint {
		return GatewayDirectoryFinalizeResult{}, ErrGatewayDirectoryIngestionInconsistent
	}

	tx, err := repository.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return GatewayDirectoryFinalizeResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	txQueries := repository.queries.WithTx(tx)

	if _, err := txQueries.LockGatewayDirectoryInstance(ctx, nullableUUID(success.Request.GatewayInstanceID)); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			lifecycle, lookupErr := txQueries.LockGatewayDirectoryLifecycleFailure(ctx, nullableUUID(success.Request.GatewayInstanceID))
			failureClass := gatewaydirectoryFailureClassContractInvalid
			if lookupErr == nil {
				failureClass = lifecycleFailureClass(lifecycle.LifecycleStatus, lifecycle.RetireReason.String)
			} else if !errors.Is(lookupErr, pgx.ErrNoRows) {
				return GatewayDirectoryFinalizeResult{}, lookupErr
			}
			result, updateErr := tx.Exec(ctx, `UPDATE gateway_directory_ingestion_runs SET status='failed',terminal_at=clock_timestamp(),last_failure_class=$4,outcome=NULL,lease_expires_at=NULL,lease_fencing_token=NULL WHERE ingestion_run_id=$1 AND gateway_instance_id=$2 AND status='running' AND lease_fencing_token=$3`, success.Request.IngestionRunID, success.Request.GatewayInstanceID, success.Request.LeaseFencingToken, string(failureClass))
			if updateErr != nil {
				return GatewayDirectoryFinalizeResult{}, updateErr
			}
			if result.RowsAffected() == 0 {
				return GatewayDirectoryFinalizeResult{Failure: &GatewayDirectoryFinalizeFailure{Reason: GatewayDirectoryFinalizeFailureLostLease}}, nil
			}
			if err := tx.Commit(ctx); err != nil {
				return GatewayDirectoryFinalizeResult{}, err
			}
			return GatewayDirectoryFinalizeResult{Failure: &GatewayDirectoryFinalizeFailure{Reason: GatewayDirectoryFinalizeFailureGatewayInactive}}, nil
		}
		return GatewayDirectoryFinalizeResult{}, err
	}

	dbNow, err := txQueries.GetGatewayDirectoryAttemptNow(ctx)
	if err != nil {
		return GatewayDirectoryFinalizeResult{}, err
	}
	if !dbNow.Valid {
		return GatewayDirectoryFinalizeResult{}, ErrGatewayDirectoryIngestionInconsistent
	}
	candidateReceivedAt := dbNow.Time.UTC()

	runRow, err := txQueries.GetGatewayDirectoryFinalizeRunningRun(ctx, generated.GetGatewayDirectoryFinalizeRunningRunParams{
		IngestionRunID:    nullableUUID(success.Request.IngestionRunID),
		GatewayInstanceID: nullableUUID(success.Request.GatewayInstanceID),
		LeaseFencingToken: nullableUUID(success.Request.LeaseFencingToken),
		DbNow:             dbNow,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return GatewayDirectoryFinalizeResult{
			Failure: &GatewayDirectoryFinalizeFailure{Reason: GatewayDirectoryFinalizeFailureLostLease},
		}, nil
	}
	if err != nil {
		return GatewayDirectoryFinalizeResult{}, err
	}
	if !validGatewayDirectoryIngestionRun(runRow) {
		return GatewayDirectoryFinalizeResult{}, ErrGatewayDirectoryIngestionInconsistent
	}

	currentState, err := txQueries.GetGatewayDirectoryCurrentStateForUpdate(ctx, nullableUUID(success.Request.GatewayInstanceID))
	var currentSnapshotID uuid.UUID
	var currentContentFingerprint []byte
	var lastSourceGeneratedAt *time.Time
	var previousSourceGeneratedAt pgtype.Timestamptz
	if errors.Is(err, pgx.ErrNoRows) {
		currentState = generated.GatewayDirectoryCurrentState{}
	} else if err != nil {
		return GatewayDirectoryFinalizeResult{}, err
	} else {
		currentSnapshotID = uuidFromPG(currentState.CurrentSnapshotID)
		currentContentFingerprint = append([]byte(nil), currentState.CurrentContentFingerprint...)
		if currentState.LastSourceGeneratedAt.Valid {
			previous := currentState.LastSourceGeneratedAt.Time.UTC()
			lastSourceGeneratedAt = &previous
			previousSourceGeneratedAt = currentState.LastSourceGeneratedAt
		}
	}

	if err := gatewaydirectory.ValidateSourceTime(gatewaydirectory.DefaultSourceTimePolicy(), success.GeneratedAt, candidateReceivedAt, lastSourceGeneratedAt); err != nil {
		return GatewayDirectoryFinalizeResult{
			Failure: &GatewayDirectoryFinalizeFailure{Reason: GatewayDirectoryFinalizeFailureSourceTimeInvalid},
		}, nil
	}

	snapshotID := currentSnapshotID
	outcome := GatewayDirectoryFinalizeOutcomeChanged
	insertedSnapshot := false
	fingerprintBytes := success.Fingerprint[:]
	if currentState.CurrentSnapshotID.Valid && bytes.Equal(currentContentFingerprint, fingerprintBytes) {
		outcome = GatewayDirectoryFinalizeOutcomeUnchanged
	} else {
		snapshot, snapshotErr := txQueries.InsertGatewayDirectorySnapshot(ctx, generated.InsertGatewayDirectorySnapshotParams{
			GatewayInstanceID: nullableUUID(success.Request.GatewayInstanceID),
			Fingerprint:       fingerprintBytes,
			SchemaVersion:     int32(success.Directory.SchemaVersion),
			AccountCount:      int32(success.AccountCount),
		})
		if errors.Is(snapshotErr, pgx.ErrNoRows) {
			snapshot, snapshotErr = txQueries.GetGatewayDirectorySnapshotByFingerprint(ctx, generated.GetGatewayDirectorySnapshotByFingerprintParams{
				GatewayInstanceID: nullableUUID(success.Request.GatewayInstanceID),
				Fingerprint:       fingerprintBytes,
			})
		} else {
			insertedSnapshot = true
		}
		if snapshotErr != nil {
			return GatewayDirectoryFinalizeResult{}, snapshotErr
		}
		snapshotID = uuidFromPG(snapshot.SnapshotID)
		if snapshotID == uuid.Nil {
			return GatewayDirectoryFinalizeResult{}, ErrGatewayDirectoryIngestionInconsistent
		}
	}

	if insertedSnapshot {
		if err := copyGatewayDirectorySnapshotItems(ctx, tx, snapshotID, success.Directory.Accounts); err != nil {
			return GatewayDirectoryFinalizeResult{}, err
		}
	}

	finalizeResult, runErr := txQueries.FinalizeGatewayDirectoryIngestionRunSucceeded(ctx, generated.FinalizeGatewayDirectoryIngestionRunSucceededParams{
		IngestionRunID:            nullableUUID(success.Request.IngestionRunID),
		GatewayInstanceID:         nullableUUID(success.Request.GatewayInstanceID),
		LeaseFencingToken:         nullableUUID(success.Request.LeaseFencingToken),
		SourceGeneratedAt:         pgtype.Timestamptz{Time: success.GeneratedAt.UTC(), Valid: true},
		PreviousSourceGeneratedAt: previousSourceGeneratedAt,
		ContentFingerprint:        fingerprintBytes,
		SnapshotID:                nullableUUID(snapshotID),
		AccountCount:              int32(success.AccountCount),
		Outcome:                   string(outcome),
	})
	if runErr != nil {
		return GatewayDirectoryFinalizeResult{}, runErr
	}
	if !finalizeResult.FinalReceivedAt.Valid {
		return GatewayDirectoryFinalizeResult{}, ErrGatewayDirectoryIngestionInconsistent
	}
	finalReceivedAt := finalizeResult.FinalReceivedAt.Time.UTC()

	switch finalizeResult.Decision {
	case "succeeded":
	case "lost_lease":
		return GatewayDirectoryFinalizeResult{
			Failure: &GatewayDirectoryFinalizeFailure{Reason: GatewayDirectoryFinalizeFailureLostLease},
		}, nil
	case "source_time_invalid":
		return GatewayDirectoryFinalizeResult{
			Failure: &GatewayDirectoryFinalizeFailure{Reason: GatewayDirectoryFinalizeFailureSourceTimeInvalid},
		}, nil
	default:
		return GatewayDirectoryFinalizeResult{}, ErrGatewayDirectoryIngestionInconsistent
	}

	_, err = txQueries.UpsertGatewayDirectoryCurrentState(ctx, generated.UpsertGatewayDirectoryCurrentStateParams{
		GatewayInstanceID:         nullableUUID(success.Request.GatewayInstanceID),
		CurrentSnapshotID:         nullableUUID(snapshotID),
		CurrentContentFingerprint: fingerprintBytes,
		LastSuccessReceivedAt:     finalizeResult.FinalReceivedAt,
		LastSourceGeneratedAt:     pgtype.Timestamptz{Time: success.GeneratedAt.UTC(), Valid: true},
		LastSuccessRunID:          nullableUUID(success.Request.IngestionRunID),
		UpdatedAt:                 finalizeResult.FinalReceivedAt,
	})
	if err != nil {
		return GatewayDirectoryFinalizeResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return GatewayDirectoryFinalizeResult{}, err
	}
	return GatewayDirectoryFinalizeResult{
		Success: &FinalizeSuccessResult{
			Outcome:    outcome,
			SnapshotID: snapshotID,
			ReceivedAt: finalReceivedAt,
		},
	}, nil
}

func copyGatewayDirectorySnapshotItems(
	ctx context.Context,
	tx pgx.Tx,
	snapshotID uuid.UUID,
	accounts []gatewaydirectory.Account,
) error {
	if snapshotID == uuid.Nil {
		return ErrInvalidGatewayDirectoryIngestionQuery
	}
	if len(accounts) == 0 {
		return nil
	}
	rows := make([][]any, 0, len(accounts))
	for _, account := range accounts {
		var urlValue any
		if account.URL != nil {
			urlValue = *account.URL
		}
		rows = append(rows, []any{
			snapshotID,
			account.ID,
			account.Name,
			account.Platform,
			account.Type,
			urlValue,
			account.Status,
		})
	}
	_, err := tx.CopyFrom(
		ctx,
		pgx.Identifier{"gateway_directory_snapshot_items"},
		[]string{"snapshot_id", "account_id", "name", "platform", "type", "url", "status"},
		pgx.CopyFromRows(rows),
	)
	return err
}
