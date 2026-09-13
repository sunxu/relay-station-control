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
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// TestSupportedWrapperAgainstFloorTwoPG18 exercises the supported deployment
// wrapper, not just the gate package. It uses an isolated database and an
// executable release artifact so a rejected class can be proven not to start.
func TestSupportedWrapperAgainstFloorTwoPG18(t *testing.T) {
	ctx := context.Background()
	base := os.Getenv("CONTROL_DATABASE_TEST_URL")
	if base == "" {
		t.Skip("set CONTROL_DATABASE_TEST_URL for PostgreSQL 18 wrapper acceptance")
	}
	root := repositoryRoot(t)
	parsed, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	databaseName := "control_compat_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	if _, err = admin.Exec(context.Background(), "CREATE DATABASE "+pgx.Identifier{databaseName}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{databaseName}.Sanitize()+" WITH (FORCE)")
		_ = admin.Close(context.Background())
	})
	parsed.Path = "/" + databaseName
	databaseURL := parsed.String()
	migrate := exec.Command("go", "tool", "goose", "-dir", "../migrations", "postgres", databaseURL, "up")
	migrate.Dir = filepath.Join(root, "tools")
	if output, err := migrate.CombinedOutput(); err != nil {
		t.Fatalf("migrate floor-one database: %v\n%s", err, output)
	}
	floorDB, err := pgx.Connect(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer floorDB.Close(context.Background())

	temporary := t.TempDir()
	gate := filepath.Join(temporary, "relay-control-compat-gate")
	build := exec.Command("go", "build", "-o", gate, "./cmd/relay-control-compat-gate")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build gate: %v\n%s", err, output)
	}
	artifact := filepath.Join(temporary, "control-artifact")
	started := filepath.Join(temporary, "started")
	if err := os.WriteFile(artifact, []byte("#!/bin/sh\nset -eu\nprintf started > \"$CONTROL_COMPAT_STARTED\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	artifactBytes, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(artifactBytes)
	digestText := "sha256:" + hex.EncodeToString(digest[:])
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	publicPath := filepath.Join(temporary, "manifest.ed25519.pub")
	if err := os.WriteFile(publicPath, public, 0o400); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(temporary, "manifest.v1.json")
	wrapper := filepath.Join(root, "deploy", "compatibility", "relay-control-compat-wrapper.sh")

	run := func(class int, database string) ([]byte, error) {
		t.Helper()
		if err := os.WriteFile(manifestPath, signedManifest(t, private, digestText, class), 0o600); err != nil {
			t.Fatal(err)
		}
		_ = os.Remove(started)
		command := exec.Command(wrapper)
		command.Env = append(os.Environ(),
			"CONTROL_COMPAT_GATE="+gate,
			"CONTROL_ARTIFACT="+artifact,
			"CONTROL_COMPAT_MANIFEST="+manifestPath,
			"CONTROL_COMPAT_PUBLIC_KEY="+publicPath,
			"CONTROL_COMPAT_DATABASE_URL_ENV=COMPAT_TEST_DATABASE_URL",
			"COMPAT_TEST_DATABASE_URL="+database,
			"CONTROL_COMPAT_STARTED="+started,
		)
		return command.CombinedOutput()
	}
	check := func(class int, database string) ([]byte, error) {
		t.Helper()
		if err := os.WriteFile(manifestPath, signedManifest(t, private, digestText, class), 0o600); err != nil {
			t.Fatal(err)
		}
		command := exec.Command(gate,
			"--manifest", manifestPath,
			"--public-key", publicPath,
			"--artifact", artifact,
			"--database-url-env", "COMPAT_TEST_DATABASE_URL",
			"--check-only",
		)
		command.Env = append(os.Environ(), "COMPAT_TEST_DATABASE_URL="+database)
		return command.CombinedOutput()
	}

	if output, err := run(0, databaseURL); err == nil {
		t.Fatalf("class zero unexpectedly started: %s", output)
	} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != ExitIncompatible {
		t.Fatalf("class zero exit=%v output=%s", err, output)
	}
	if _, err := os.Stat(started); !os.IsNotExist(err) {
		t.Fatal("class-zero artifact executed before compatibility rejection")
	}
	if _, err := floorDB.Exec(ctx, `ALTER TABLE public.control_runtime_compatibility DISABLE TRIGGER USER; DELETE FROM public.control_runtime_compatibility`); err != nil {
		t.Fatal(err)
	}
	if output, err := run(0, databaseURL); err == nil {
		t.Fatalf("class zero bypassed missing floor-one marker: %s", output)
	} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != ExitIncompatible {
		t.Fatalf("missing marker exit=%v output=%s", err, output)
	}
	if _, err := os.Stat(started); !os.IsNotExist(err) {
		t.Fatal("artifact executed with missing floor-one marker")
	}
	if _, err := floorDB.Exec(ctx, `INSERT INTO public.control_runtime_compatibility(singleton_id,schema_version,phase6_evidence_floor) VALUES(1,1,2); ALTER TABLE public.control_runtime_compatibility ENABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
	}
	if _, err := floorDB.Exec(ctx, `ALTER TABLE public.control_runtime_compatibility DISABLE TRIGGER USER; ALTER TABLE public.control_runtime_compatibility DROP CONSTRAINT control_runtime_compatibility_schema_version_check; UPDATE public.control_runtime_compatibility SET schema_version=2`); err != nil {
		t.Fatal(err)
	}
	if output, err := check(2, databaseURL); err == nil {
		t.Fatalf("malformed floor-two marker unexpectedly accepted: %s", output)
	} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != ExitIncompatible {
		t.Fatalf("malformed marker exit=%v output=%s", err, output)
	}
	if _, err := floorDB.Exec(ctx, `UPDATE public.control_runtime_compatibility SET schema_version=1; ALTER TABLE public.control_runtime_compatibility ADD CONSTRAINT control_runtime_compatibility_schema_version_check CHECK(schema_version=1); ALTER TABLE public.control_runtime_compatibility ENABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
	}
	if output, err := run(1, databaseURL); err == nil {
		t.Fatalf("class one unexpectedly started at floor two: %s", output)
	} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != ExitIncompatible {
		t.Fatalf("class one at floor two exit=%v output=%s", err, output)
	}
	if _, err := os.Stat(started); !os.IsNotExist(err) {
		t.Fatal("class-one artifact executed at floor two")
	}
	if output, err := run(2, databaseURL); runtime.GOOS == "linux" {
		if err != nil {
			t.Fatalf("class two wrapper start: %v output=%s", err, output)
		}
		if _, err := os.Stat(started); err != nil {
			t.Fatalf("class-two artifact was not executed: %v", err)
		}
	} else {
		exit, ok := err.(*exec.ExitError)
		if !ok || exit.ExitCode() != ExitExecFailure {
			t.Fatalf("unsupported descriptor exec exit=%v output=%s", err, output)
		}
		if _, err := os.Stat(started); !os.IsNotExist(err) {
			t.Fatal("artifact executed on an unsupported descriptor-exec platform")
		}
	}
	if output, err := run(2, "postgres://unavailable.invalid/test"); err == nil {
		t.Fatalf("unavailable database unexpectedly passed: %s", output)
	} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != ExitDatabaseUnavailable {
		t.Fatalf("unavailable database exit=%v output=%s", err, output)
	}
	if _, err := os.Stat(started); !os.IsNotExist(err) {
		t.Fatal("artifact executed while database was unavailable")
	}

	// The supported Compose path verifies the immutable OCI manifest digest
	// before invoking Docker and passes the exact digest-qualified image.
	if err := os.WriteFile(manifestPath, signedManifest(t, private, digestText, 2), 0o600); err != nil {
		t.Fatal(err)
	}
	fakeBin := filepath.Join(temporary, "bin")
	if err := os.Mkdir(fakeBin, 0o700); err != nil {
		t.Fatal(err)
	}
	dockerEvidence := filepath.Join(temporary, "docker-evidence")
	fakeDocker := filepath.Join(fakeBin, "docker")
	if err := os.WriteFile(fakeDocker, []byte("#!/bin/sh\nset -eu\nprintf '%s\\n' \"$CONTROL_IMAGE\" \"$*\" > \"$CONTROL_COMPAT_DOCKER_EVIDENCE\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	composeWrapper := filepath.Join(root, "deploy", "compatibility", "relay-control-compat-compose-wrapper.sh")
	runCompose := func(image string) ([]byte, error) {
		t.Helper()
		_ = os.Remove(dockerEvidence)
		command := exec.Command(composeWrapper, "--detach")
		command.Env = append(os.Environ(),
			"PATH="+fakeBin+":"+os.Getenv("PATH"),
			"CONTROL_COMPAT_GATE="+gate,
			"CONTROL_COMPAT_MANIFEST="+manifestPath,
			"CONTROL_COMPAT_PUBLIC_KEY="+publicPath,
			"CONTROL_COMPAT_DATABASE_URL_ENV=COMPAT_TEST_DATABASE_URL",
			"COMPAT_TEST_DATABASE_URL="+databaseURL,
			"CONTROL_IMAGE="+image,
			"CONTROL_COMPOSE_FILE=/protected/compose.yaml",
			"CONTROL_COMPAT_COMPOSE_FILE=/protected/compose.compatibility.yaml",
			"CONTROL_COMPAT_DOCKER_EVIDENCE="+dockerEvidence,
		)
		return command.CombinedOutput()
	}
	immutableImage := "registry.example/relay-control@" + digestText
	if output, err := runCompose(immutableImage); err != nil {
		t.Fatalf("immutable Compose wrapper: %v output=%s", err, output)
	}
	evidence, err := os.ReadFile(dockerEvidence)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(evidence), immutableImage) || !strings.Contains(string(evidence), "compose -f /protected/compose.yaml -f /protected/compose.compatibility.yaml up --detach control") {
		t.Fatalf("unexpected Compose launch evidence: %q", evidence)
	}
	if output, err := runCompose("registry.example/relay-control:latest"); err == nil {
		t.Fatalf("mutable tag unexpectedly accepted: %s", output)
	}
	if _, err := os.Stat(dockerEvidence); !os.IsNotExist(err) {
		t.Fatal("Docker invoked for mutable image tag")
	}
	wrongDigest := "registry.example/relay-control@sha256:" + strings.Repeat("0", 64)
	if output, err := runCompose(wrongDigest); err == nil {
		t.Fatalf("wrong immutable digest unexpectedly accepted: %s", output)
	}
	if _, err := os.Stat(dockerEvidence); !os.IsNotExist(err) {
		t.Fatal("Docker invoked for wrong image digest")
	}

	preDatabaseName := "control_compat_pre_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{preDatabaseName}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{preDatabaseName}.Sanitize()+" WITH (FORCE)")
	}()
	preURL, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	preURL.Path = "/" + preDatabaseName
	preDatabaseURL := preURL.String()
	preMigrate := exec.Command("go", "tool", "goose", "-dir", "../migrations", "postgres", preDatabaseURL, "up-to", "32")
	preMigrate.Dir = filepath.Join(root, "tools")
	if output, err := preMigrate.CombinedOutput(); err != nil {
		t.Fatalf("migrate pre-Stage1 database: %v\n%s", err, output)
	}
	if output, err := check(0, preDatabaseURL); err != nil {
		t.Fatalf("version 32 without marker should be floor zero: %v output=%s", err, output)
	}
	preDB, err := pgx.Connect(ctx, preDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := preDB.Exec(ctx, `CREATE TABLE public.control_runtime_compatibility(singleton_id smallint PRIMARY KEY,schema_version integer NOT NULL,phase6_evidence_floor integer NOT NULL); INSERT INTO public.control_runtime_compatibility VALUES(1,1,1)`); err != nil {
		preDB.Close(ctx)
		t.Fatal(err)
	}
	preDB.Close(ctx)
	if output, err := check(1, preDatabaseURL); err == nil {
		t.Fatalf("marker before migration 33 unexpectedly accepted: %s", output)
	} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != ExitIncompatible {
		t.Fatalf("marker/migration mismatch exit=%v output=%s", err, output)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("repository root not found")
		}
		directory = parent
	}
}
