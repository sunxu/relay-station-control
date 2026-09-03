package store

import (
	"context"

	"github.com/google/uuid"

	"github.com/sunxu/relay-station-control/internal/drivers"
)

const (
	GatewayDirectoryWorkStatusNoWork    = "no_work"
	GatewayDirectoryWorkStatusLostLease = "lost_lease"
	GatewayDirectoryWorkStatusRetryWait = "retry_wait"
	GatewayDirectoryWorkStatusFailed    = "failed"
	GatewayDirectoryWorkStatusSucceeded = "succeeded"
)

type GatewayDirectoryIngestionService struct {
	repository     *GatewayDirectoryIngestionRepository
	secretResolver drivers.SecretResolver
}

type GatewayDirectoryWorkResult struct {
	GatewayInstanceID uuid.UUID
	RunID             uuid.UUID
	Status            string
	Outcome           string
	FailureClass      string
}

func validateGatewayDirectoryAttemptResult(result GatewayDirectoryAttemptResult) error {
	if (result.Success == nil) == (result.Failure == nil) {
		return ErrGatewayDirectoryIngestionInconsistent
	}
	return nil
}

func NewGatewayDirectoryIngestionService(
	repository *GatewayDirectoryIngestionRepository,
	secretResolver drivers.SecretResolver,
) (*GatewayDirectoryIngestionService, error) {
	if repository == nil || secretResolver == nil {
		return nil, ErrInvalidGatewayDirectoryIngestionQuery
	}
	return &GatewayDirectoryIngestionService{
		repository:     repository,
		secretResolver: secretResolver,
	}, nil
}

func (service *GatewayDirectoryIngestionService) RunGatewayOnce(
	ctx context.Context,
	gatewayInstanceID uuid.UUID,
) (GatewayDirectoryWorkResult, error) {
	if service == nil || service.repository == nil || service.secretResolver == nil || gatewayInstanceID == uuid.Nil {
		return GatewayDirectoryWorkResult{}, ErrInvalidGatewayDirectoryIngestionQuery
	}
	if _, _, _, err := service.repository.ScheduleCurrent(ctx, gatewayInstanceID); err != nil {
		return GatewayDirectoryWorkResult{}, err
	}
	claimed, err := service.repository.ClaimRunnable(ctx, gatewayInstanceID, uuid.New())
	if err != nil {
		return GatewayDirectoryWorkResult{}, err
	}
	if claimed == nil {
		return GatewayDirectoryWorkResult{GatewayInstanceID: gatewayInstanceID, Status: GatewayDirectoryWorkStatusNoWork}, nil
	}

	attempt, err := service.repository.ExecuteAttempt(ctx, GatewayDirectoryAttemptRequest{
		IngestionRunID:    uuidFromPG(claimed.IngestionRunID),
		GatewayInstanceID: uuidFromPG(claimed.GatewayInstanceID),
		LeaseFencingToken: uuidFromPG(claimed.LeaseFencingToken),
	}, service.secretResolver)
	if err != nil {
		return GatewayDirectoryWorkResult{}, err
	}
	if err := validateGatewayDirectoryAttemptResult(attempt); err != nil {
		return GatewayDirectoryWorkResult{}, err
	}
	if attempt.Failure != nil {
		return service.handleAttemptFailure(ctx, attempt.Failure)
	}

	finalize, err := service.repository.FinalizeSuccessfulAttempt(ctx, *attempt.Success)
	if err != nil {
		return GatewayDirectoryWorkResult{}, err
	}
	if finalize.Success != nil {
		if finalize.Failure != nil {
			return GatewayDirectoryWorkResult{}, ErrGatewayDirectoryIngestionInconsistent
		}
		return GatewayDirectoryWorkResult{
			GatewayInstanceID: gatewayInstanceID,
			RunID:             attempt.Success.Request.IngestionRunID,
			Status:            GatewayDirectoryWorkStatusSucceeded,
			Outcome:           string(finalize.Success.Outcome),
		}, nil
	}
	if finalize.Failure == nil {
		return GatewayDirectoryWorkResult{}, ErrGatewayDirectoryIngestionInconsistent
	}
	switch finalize.Failure.Reason {
	case GatewayDirectoryFinalizeFailureLostLease:
		return GatewayDirectoryWorkResult{
			GatewayInstanceID: gatewayInstanceID,
			RunID:             attempt.Success.Request.IngestionRunID,
			Status:            GatewayDirectoryWorkStatusLostLease,
		}, nil
	case GatewayDirectoryFinalizeFailureSourceTimeInvalid:
		transition, transitionErr := service.repository.recordAttemptFailure(ctx, attempt.Success.Request, GatewayDirectoryAttemptFailureInput{
			Class:     string(gatewaydirectoryFailureClassSourceTimeInvalid),
			Retryable: false,
		})
		if transitionErr != nil {
			return GatewayDirectoryWorkResult{}, transitionErr
		}
		if transition == nil {
			return GatewayDirectoryWorkResult{
				GatewayInstanceID: gatewayInstanceID,
				RunID:             attempt.Success.Request.IngestionRunID,
				Status:            GatewayDirectoryWorkStatusLostLease,
			}, nil
		}
		return GatewayDirectoryWorkResult{
			GatewayInstanceID: gatewayInstanceID,
			RunID:             attempt.Success.Request.IngestionRunID,
			Status:            string(transition.Disposition),
			FailureClass:      transition.Class,
		}, nil
	default:
		return GatewayDirectoryWorkResult{}, ErrGatewayDirectoryIngestionInconsistent
	}
}

func (service *GatewayDirectoryIngestionService) handleAttemptFailure(
	ctx context.Context,
	failure *GatewayDirectoryAttemptFailure,
) (GatewayDirectoryWorkResult, error) {
	if failure == nil {
		return GatewayDirectoryWorkResult{}, ErrGatewayDirectoryIngestionInconsistent
	}
	if failure.Disposition == GatewayDirectoryAttemptLostLease {
		return GatewayDirectoryWorkResult{
			GatewayInstanceID: failure.Request.GatewayInstanceID,
			RunID:             failure.Request.IngestionRunID,
			Status:            GatewayDirectoryWorkStatusLostLease,
			FailureClass:      failure.Class,
		}, nil
	}
	return GatewayDirectoryWorkResult{
		GatewayInstanceID: failure.Request.GatewayInstanceID,
		RunID:             failure.Request.IngestionRunID,
		Status:            string(failure.Disposition),
		FailureClass:      failure.Class,
	}, nil
}

func (service *GatewayDirectoryIngestionService) ScheduleRegisteredGateways(ctx context.Context) error {
	if service == nil || service.repository == nil {
		return ErrInvalidGatewayDirectoryIngestionQuery
	}
	ids, err := service.repository.ListGatewayInstanceIDs(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, _, _, err := service.repository.ScheduleCurrent(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (service *GatewayDirectoryIngestionService) ReconcileOne(ctx context.Context) (*GatewayDirectoryReconcileResult, error) {
	if service == nil || service.repository == nil {
		return nil, ErrInvalidGatewayDirectoryIngestionQuery
	}
	return service.repository.ReconcileOne(ctx)
}

func (service *GatewayDirectoryIngestionService) ReconcileAvailable(ctx context.Context, limit int) ([]GatewayDirectoryReconcileResult, error) {
	if service == nil || service.repository == nil || limit < 1 || limit > 100 {
		return nil, ErrInvalidGatewayDirectoryIngestionQuery
	}
	results := make([]GatewayDirectoryReconcileResult, 0, limit)
	for len(results) < limit {
		result, err := service.ReconcileOne(ctx)
		if err != nil {
			return nil, err
		}
		if result == nil {
			return results, nil
		}
		results = append(results, *result)
	}
	return results, nil
}

func (service *GatewayDirectoryIngestionService) WorkOnce(ctx context.Context) ([]GatewayDirectoryWorkResult, error) {
	if service == nil || service.repository == nil {
		return nil, ErrInvalidGatewayDirectoryIngestionQuery
	}
	ids, err := service.repository.ListGatewayInstanceIDs(ctx)
	if err != nil {
		return nil, err
	}
	results := make([]GatewayDirectoryWorkResult, 0, len(ids))
	for _, id := range ids {
		if ctx.Err() != nil {
			return results, ctx.Err()
		}
		result, err := service.RunGatewayOnce(ctx, id)
		if err != nil {
			return results, err
		}
		results = append(results, result)
	}
	return results, nil
}

func (service *GatewayDirectoryIngestionService) ScheduleTick(ctx context.Context) error {
	return service.ScheduleRegisteredGateways(ctx)
}

func (service *GatewayDirectoryIngestionService) ReconcileTick(ctx context.Context) ([]GatewayDirectoryReconcileResult, error) {
	return service.ReconcileAvailable(ctx, 100)
}
