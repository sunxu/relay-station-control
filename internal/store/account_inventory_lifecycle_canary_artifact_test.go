package store_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	generated "github.com/sunxu/relay-station-control/internal/store/sqlc"
)

func TestAccountInventoryLifecycleCanariesTraverseSQLParameterArtifact(t *testing.T) {
	artifactRoot := os.Getenv("CONTROL_LIFECYCLE_CANARY_ARTIFACT_DIR")
	if artifactRoot == "" {
		t.Skip("lifecycle canary artifact acceptance is opt-in")
	}
	pollID, err := uuid.Parse(os.Getenv("CONTROL_LIFECYCLE_CANARY_POLL_ID"))
	if err != nil {
		t.Fatal("poll canary invalid")
	}
	sqlParameter := os.Getenv("CONTROL_LIFECYCLE_CANARY_SQL_PARAMETER")
	email := os.Getenv("CONTROL_LIFECYCLE_CANARY_EMAIL")
	accountKey := os.Getenv("CONTROL_LIFECYCLE_CANARY_ACCOUNT_KEY")
	version := os.Getenv("CONTROL_LIFECYCLE_CANARY_VERSION")
	commit := os.Getenv("CONTROL_LIFECYCLE_CANARY_COMMIT")
	if sqlParameter == "" || email == "" || accountKey == "" || version == "" || commit == "" {
		t.Fatal("SQL parameter canary configuration incomplete")
	}
	parameters := generated.FinalizeAccountInventoryPollRunWithLifecycleParams{
		PollRunID:   pgtype.UUID{Bytes: pollID, Valid: true},
		NodeVersion: version, NodeCommit: commit,
		ProviderResults:   []byte(`[{"provider":"openai","reason":"` + sqlParameter + `"}]`),
		SnapshotItems:     []byte(`[{"account_key":"` + accountKey + `","email":"` + email + `"}]`),
		DuplicateEvidence: []byte(`[{"account_key":"` + accountKey + `"}]`),
	}
	formatted := fmt.Sprintf("%v", parameters)
	for _, canary := range []string{pollID.String(), sqlParameter, email, accountKey, version, commit} {
		if strings.Contains(formatted, canary) {
			t.Fatal("sqlc lifecycle parameter formatter leaked a canary")
		}
	}
	directory := filepath.Join(artifactRoot, "rollback")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal("SQL parameter artifact directory unavailable")
	}
	if err := os.WriteFile(filepath.Join(directory, "sql-parameter.log"), []byte(formatted+"\n"), 0o600); err != nil {
		t.Fatal("SQL parameter artifact unavailable")
	}
}
