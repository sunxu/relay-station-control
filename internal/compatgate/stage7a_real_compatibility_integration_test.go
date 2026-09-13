package compatgate

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	preStage7ARevision            = "fa9825be3239bbd395bbaf0ecd7294e10cf7af64"
	stage7AImplementationRevision = "a9463bc776ffa5cc7c6341f15f89385afa555d34"
)

func TestStage7ARealArtifactsAgainstFloorThreePG18(t *testing.T) {
	ownerBase := os.Getenv("CONTROL_DATABASE_TEST_URL")
	if ownerBase == "" {
		t.Skip("set CONTROL_DATABASE_TEST_URL for PostgreSQL 18 Stage 7A compatibility acceptance")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	root := repositoryRoot(t)
	admin, err := pgx.Connect(ctx, ownerBase)
	if err != nil {
		t.Fatal(err)
	}
	databaseName := "control_stage7a_compat_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{databaseName}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{databaseName}.Sanitize()+" WITH (FORCE)")
		_ = admin.Close(context.Background())
	})
	parsed, err := url.Parse(ownerBase)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "/" + databaseName
	databaseURL := parsed.String()
	migrate := exec.CommandContext(ctx, "go", "tool", "goose", "-dir", "../migrations", "postgres", databaseURL, "up-to", "37")
	migrate.Dir = filepath.Join(root, "tools")
	if output, migrateErr := migrate.CombinedOutput(); migrateErr != nil {
		t.Fatalf("migrate Stage 7A database: %v\n%s", migrateErr, output)
	}

	temporary := t.TempDir()
	archiveRevision := func(revision, directoryName string) string {
		t.Helper()
		source := filepath.Join(temporary, directoryName)
		if err := os.Mkdir(source, 0o700); err != nil {
			t.Fatal(err)
		}
		archive := filepath.Join(temporary, directoryName+".tar")
		archiveCommand := exec.CommandContext(ctx, "git", "archive", "--format=tar", "--output", archive, revision)
		archiveCommand.Dir = root
		if output, archiveErr := archiveCommand.CombinedOutput(); archiveErr != nil {
			t.Fatalf("archive source revision %s: %v\n%s", revision, archiveErr, output)
		}
		if output, extractErr := exec.CommandContext(ctx, "tar", "-xf", archive, "-C", source).CombinedOutput(); extractErr != nil {
			t.Fatalf("extract source revision %s: %v\n%s", revision, extractErr, output)
		}
		return source
	}
	oldSource := archiveRevision(preStage7ARevision, "pre-stage7a-source")
	stage7ASource := archiveRevision(stage7AImplementationRevision, "stage7a-source")
	build := func(source, outputPath, target string) {
		t.Helper()
		command := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", outputPath, target)
		command.Dir = source
		command.Env = append(os.Environ(), "CGO_ENABLED=0")
		if output, buildErr := command.CombinedOutput(); buildErr != nil {
			t.Fatalf("build %s from %s: %v\n%s", target, source, buildErr, output)
		}
		if err := os.Chmod(outputPath, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	oldArtifact := filepath.Join(temporary, "control-pre-stage7a")
	newArtifact := filepath.Join(temporary, "control-stage7a")
	gate := filepath.Join(temporary, "relay-control-compat-gate")
	build(oldSource, oldArtifact, "./cmd/control")
	build(stage7ASource, newArtifact, "./cmd/control")
	build(stage7ASource, gate, "./cmd/relay-control-compat-gate")
	digest := func(path string) string {
		t.Helper()
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		sum := sha256.Sum256(data)
		return "sha256:" + hex.EncodeToString(sum[:])
	}
	oldDigest, newDigest := digest(oldArtifact), digest(newArtifact)
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	publicPath := filepath.Join(temporary, "manifest.pub")
	manifestPath := filepath.Join(temporary, "manifest.json")
	if err = os.WriteFile(publicPath, publicKey, 0o400); err != nil {
		t.Fatal(err)
	}
	run := func(artifact, artifactDigest string, class int) ([]byte, error) {
		t.Helper()
		if err := os.WriteFile(manifestPath, signedManifest(t, privateKey, artifactDigest, class), 0o600); err != nil {
			t.Fatal(err)
		}
		command := exec.CommandContext(ctx, gate,
			"--manifest", manifestPath,
			"--public-key", publicPath,
			"--artifact", artifact,
			"--database-url-env", "STAGE7A_COMPAT_DATABASE_URL",
			"--check-only")
		command.Env = append(os.Environ(), "STAGE7A_COMPAT_DATABASE_URL="+databaseURL)
		return command.CombinedOutput()
	}
	if output, runErr := run(oldArtifact, oldDigest, 2); runErr == nil {
		t.Fatalf("pre-Stage7A class-2 artifact unexpectedly passed floor 3: %s", output)
	} else if exit, ok := runErr.(*exec.ExitError); !ok || exit.ExitCode() != ExitIncompatible {
		t.Fatalf("old artifact exit=%v output=%s", runErr, output)
	}
	if output, runErr := run(newArtifact, newDigest, 3); runErr != nil {
		t.Fatalf("Stage7A class-3 artifact rejected: %v output=%s", runErr, output)
	}
	t.Logf("pre_stage7a_source=%s old_digest=%s old_class=2 old_result=rejected stage7a_source=%s new_digest=%s new_class=3 new_result=accepted gate_source=%s migration=37 floor=3",
		preStage7ARevision, oldDigest, stage7AImplementationRevision, newDigest, stage7AImplementationRevision)
}
