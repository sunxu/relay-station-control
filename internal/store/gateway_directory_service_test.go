package store

import "testing"

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
