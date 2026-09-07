package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestValidateGatewayDirectoryAttemptResult(t *testing.T) {
	t.Run("double nil", func(t *testing.T) {
		if err := validateGatewayDirectoryAttemptResult(GatewayDirectoryAttemptResult{}); err != ErrGatewayDirectoryIngestionInconsistent {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("double set", func(t *testing.T) {
		if err := validateGatewayDirectoryAttemptResult(GatewayDirectoryAttemptResult{
			Success: &GatewayDirectoryAttemptSuccess{},
			Failure: &GatewayDirectoryAttemptFailure{},
		}); err != ErrGatewayDirectoryIngestionInconsistent {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("exactly one", func(t *testing.T) {
		if err := validateGatewayDirectoryAttemptResult(GatewayDirectoryAttemptResult{
			Success: &GatewayDirectoryAttemptSuccess{},
		}); err != nil {
			t.Fatalf("success-only error = %v", err)
		}
		if err := validateGatewayDirectoryAttemptResult(GatewayDirectoryAttemptResult{
			Failure: &GatewayDirectoryAttemptFailure{},
		}); err != nil {
			t.Fatalf("failure-only error = %v", err)
		}
	})
}

func TestWorkGatewayDirectoryInstancesContinuesAfterFailure(t *testing.T) {
	first, second := uuid.New(), uuid.New()
	var called []uuid.UUID
	results, err := workGatewayDirectoryInstances(context.Background(), []uuid.UUID{first, second}, func(_ context.Context, id uuid.UUID) (GatewayDirectoryWorkResult, error) {
		called = append(called, id)
		if id == first {
			return GatewayDirectoryWorkResult{}, errors.New("first gateway failed")
		}
		return GatewayDirectoryWorkResult{GatewayInstanceID: id, Status: GatewayDirectoryWorkStatusSucceeded}, nil
	})
	if err == nil || len(called) != 2 || called[0] != first || called[1] != second {
		t.Fatalf("failure did not preserve ordered continuation: called=%v results=%v err=%v", called, results, err)
	}
	if len(results) != 1 || results[0].GatewayInstanceID != second {
		t.Fatalf("unexpected successful results: %+v", results)
	}
}

func TestWorkGatewayDirectoryInstancesContinuesAfterUnconfiguredGateway(t *testing.T) {
	first, second := uuid.New(), uuid.New()
	var called []uuid.UUID
	results, err := workGatewayDirectoryInstances(context.Background(), []uuid.UUID{first, second}, func(_ context.Context, id uuid.UUID) (GatewayDirectoryWorkResult, error) {
		called = append(called, id)
		if id == first {
			return GatewayDirectoryWorkResult{GatewayInstanceID: id, Status: GatewayDirectoryWorkStatusNoWork}, nil
		}
		return GatewayDirectoryWorkResult{GatewayInstanceID: id, Status: GatewayDirectoryWorkStatusSucceeded}, nil
	})
	if err != nil || len(called) != 2 || len(results) != 2 || results[0].Status != GatewayDirectoryWorkStatusNoWork || results[1].Status != GatewayDirectoryWorkStatusSucceeded {
		t.Fatalf("unconfigured gateway did not preserve ordered continuation: called=%v results=%v err=%v", called, results, err)
	}
}

func TestWorkGatewayDirectoryInstancesContinuesAfterReportedFailure(t *testing.T) {
	first, second := uuid.New(), uuid.New()
	var called []uuid.UUID
	results, err := workGatewayDirectoryInstances(context.Background(), []uuid.UUID{first, second}, func(_ context.Context, id uuid.UUID) (GatewayDirectoryWorkResult, error) {
		called = append(called, id)
		if id == first {
			return GatewayDirectoryWorkResult{GatewayInstanceID: id, Status: GatewayDirectoryWorkStatusFailed, FailureClass: "secret_unavailable"}, nil
		}
		return GatewayDirectoryWorkResult{GatewayInstanceID: id, Status: GatewayDirectoryWorkStatusSucceeded}, nil
	})
	if err != nil || len(called) != 2 || len(results) != 2 || results[0].FailureClass != "secret_unavailable" || results[1].Status != GatewayDirectoryWorkStatusSucceeded {
		t.Fatalf("reported failure did not preserve ordered continuation: called=%v results=%v err=%v", called, results, err)
	}
}

func TestWorkGatewayDirectoryInstancesCancellationStopsNextGateway(t *testing.T) {
	first, second := uuid.New(), uuid.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var called []uuid.UUID
	results, err := workGatewayDirectoryInstances(ctx, []uuid.UUID{first, second}, func(_ context.Context, id uuid.UUID) (GatewayDirectoryWorkResult, error) {
		called = append(called, id)
		cancel()
		return GatewayDirectoryWorkResult{GatewayInstanceID: id, Status: GatewayDirectoryWorkStatusNoWork}, nil
	})
	if !errors.Is(err, context.Canceled) || len(called) != 1 || called[0] != first || len(results) != 1 {
		t.Fatalf("cancellation did not stop next gateway: called=%v results=%v err=%v", called, results, err)
	}
}

func TestWorkGatewayDirectoryInstancesRunsSlowFirstThenSecond(t *testing.T) {
	first, second := uuid.New(), uuid.New()
	started, release := make(chan struct{}), make(chan struct{})
	var called []uuid.UUID
	type outcome struct {
		results []GatewayDirectoryWorkResult
		err     error
	}
	done := make(chan outcome, 1)
	go func() {
		results, err := workGatewayDirectoryInstances(context.Background(), []uuid.UUID{first, second}, func(_ context.Context, id uuid.UUID) (GatewayDirectoryWorkResult, error) {
			called = append(called, id)
			if id == first {
				close(started)
				<-release
			}
			return GatewayDirectoryWorkResult{GatewayInstanceID: id, Status: GatewayDirectoryWorkStatusSucceeded}, nil
		})
		done <- outcome{results: results, err: err}
	}()
	<-started
	if len(called) != 1 || called[0] != first {
		t.Fatalf("second gateway ran before first was released: called=%v", called)
	}
	close(release)
	result := <-done
	results, err := result.results, result.err
	if err != nil || len(called) != 2 || len(results) != 2 {
		t.Fatalf("slow-first ordered traversal failed: called=%v results=%v err=%v", called, results, err)
	}
}
