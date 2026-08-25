package api

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// isolatedRuntimeDatabaseURLs gives HTTP security tests their own schema state.
// Assertions such as "last enabled administrator" cannot be made reliably
// against the shared integration database, whose enabled administrators are
// intentionally retained by other lifecycle tests.
func isolatedRuntimeDatabaseURLs(t *testing.T) (string, string) {
	t.Helper()
	ownerBase := os.Getenv("CONTROL_DATABASE_TEST_URL")
	runtimeBase := os.Getenv("CONTROL_RUNTIME_DATABASE_TEST_URL")
	if ownerBase == "" || runtimeBase == "" {
		t.Skip("set CONTROL_DATABASE_TEST_URL and CONTROL_RUNTIME_DATABASE_TEST_URL")
	}
	ownerParsed, err := url.Parse(ownerBase)
	if err != nil || ownerParsed.Scheme == "" {
		t.Fatalf("parse owner database URL: %v", err)
	}
	runtimeParsed, err := url.Parse(runtimeBase)
	if err != nil || runtimeParsed.Scheme == "" {
		t.Fatalf("parse runtime database URL: %v", err)
	}
	admin, err := pgx.Connect(context.Background(), ownerBase)
	if err != nil {
		t.Fatal(err)
	}
	databaseName := "control_api_accept_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	if _, err = admin.Exec(context.Background(), "CREATE DATABASE "+pgx.Identifier{databaseName}.Sanitize()); err != nil {
		_ = admin.Close(context.Background())
		t.Fatalf("create isolated API database: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{databaseName}.Sanitize()+" WITH (FORCE)")
		_ = admin.Close(context.Background())
	})
	ownerParsed.Path = "/" + databaseName
	runtimeParsed.Path = "/" + databaseName

	repositoryRoot := apiRepositoryRoot(t)
	command := exec.Command("go", "tool", "goose", "-dir", "../migrations", "postgres", ownerParsed.String(), "up")
	command.Dir = filepath.Join(repositoryRoot, "tools")
	if output, migrationErr := command.CombinedOutput(); migrationErr != nil {
		t.Fatalf("migrate isolated API database: %v\n%s", migrationErr, output)
	}
	return ownerParsed.String(), runtimeParsed.String()
}

func apiRepositoryRoot(t *testing.T) string {
	t.Helper()
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for candidate := workingDirectory; ; candidate = filepath.Dir(candidate) {
		if _, statErr := os.Stat(filepath.Join(candidate, "go.mod")); statErr == nil {
			return candidate
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			t.Fatal("repository root not found")
		}
	}
}
