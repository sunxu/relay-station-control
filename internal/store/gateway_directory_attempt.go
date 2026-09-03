package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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

type GatewayDirectoryAttemptFailureInput struct {
	Class     string
	Retryable bool
}

func (repository *GatewayDirectoryIngestionRepository) ExecuteAttempt(
	ctx context.Context,
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
	if errors.Is(err, pgx.ErrNoRows) || !validGatewayDirectoryIngestionRun(lease) {
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

	target, err := repository.queries.GetGatewayDirectoryReadTarget(ctx, nullableUUID(request.GatewayInstanceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return GatewayDirectoryAttemptResult{}, ErrGatewayDirectoryIngestionInconsistent
	}
	if err != nil {
		return GatewayDirectoryAttemptResult{}, err
	}
	if !target.ReaderSecretRef.Valid || target.ManagementEndpoint == "" {
		return GatewayDirectoryAttemptResult{}, ErrGatewayDirectoryIngestionInconsistent
	}

	client, err := gatewaydirectory.NewClient(target.ManagementEndpoint, secretResolver)
	if err != nil {
		return GatewayDirectoryAttemptResult{}, err
	}
	directory, fingerprint, err := client.Fetch(ctx, drivers.NewSecretReference(target.ReaderSecretRef.String))
	if err != nil {
		class, retryable, classifyErr := classifyGatewayDirectoryAttemptFailure(err)
		if classifyErr != nil {
			return GatewayDirectoryAttemptResult{}, classifyErr
		}
		transition, transitionErr := repository.recordAttemptFailure(ctx, request, GatewayDirectoryAttemptFailureInput{
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
		transition, transitionErr := repository.recordAttemptFailure(ctx, request, GatewayDirectoryAttemptFailureInput{
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
)

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
