package environment

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	store "github.com/sunxu/relay-station-control/internal/store/sqlc"
)

func TestVerifyDatabaseIdentityAndRecovery(t *testing.T) {
	databaseURL := os.Getenv("CONTROL_RUNTIME_DATABASE_TEST_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("CONTROL_DATABASE_TEST_URL")
	}
	if databaseURL == "" {
		t.Skip("set CONTROL_RUNTIME_DATABASE_TEST_URL or CONTROL_DATABASE_TEST_URL")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	t.Cleanup(pool.Close)

	queries := store.New(pool)
	actual, err := queries.GetEnvironment(ctx)
	if err != nil {
		t.Fatalf("read environment singleton: %v", err)
	}
	wrong := Expected{ID: actual.EnvironmentID + "-wrong", Type: actual.EnvironmentType}
	if err = Verify(ctx, queries, wrong); ReasonOf(err) != ReasonIDMismatch {
		t.Fatalf("wrong identity reason = %q, error = %v", ReasonOf(err), err)
	}
	if err = Verify(ctx, queries, Expected{ID: actual.EnvironmentID, Type: actual.EnvironmentType}); err != nil {
		t.Fatalf("verification did not recover with corrected config: %v", err)
	}
}
