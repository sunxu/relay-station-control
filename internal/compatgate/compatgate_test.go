package compatgate

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func signedManifest(t *testing.T, private ed25519.PrivateKey, digest string, class int) []byte {
	t.Helper()
	payload, err := CanonicalPayload(ManifestVersion, digest, class)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{
		"version":                 ManifestVersion,
		"control_artifact_digest": digest,
		"compatibility_class":     class,
		"signature":               base64.StdEncoding.EncodeToString(ed25519.Sign(private, payload)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestVerifyManifestAndArtifactDigest(t *testing.T) {
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	artifact := filepath.Join(directory, "control")
	if err := os.WriteFile(artifact, []byte("control-test-artifact"), 0o700); err != nil {
		t.Fatal(err)
	}
	actual, err := digestArtifact(artifact)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(directory, "manifest.json")
	keyPath := filepath.Join(directory, "manifest.pub")
	if err := os.WriteFile(manifestPath, signedManifest(t, private, actual, 1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, public, 0o400); err != nil {
		t.Fatal(err)
	}
	manifest, err := ValidateArtifact(manifestPath, keyPath, artifact)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.CompatibilityClass != 1 || manifest.ControlArtifactDigest != actual {
		t.Fatalf("unexpected verified manifest: %+v", manifest)
	}

	if err := os.WriteFile(artifact, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateArtifact(manifestPath, keyPath, artifact); !isCode(err, ExitIncompatible) {
		t.Fatalf("tampered artifact error = %v", err)
	}
}

func TestVerifyManifestRejectsSignatureAndUnsupportedClass(t *testing.T) {
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	manifest := signedManifest(t, private, digest, 1)
	manifest[len(manifest)-4] ^= 1
	if _, err := VerifyManifest(manifest, public); !isCode(err, ExitIncompatible) {
		t.Fatalf("tampered signature error = %v", err)
	}
	if _, err := VerifyManifest(signedManifest(t, private, digest, 3), public); !isCode(err, ExitIncompatible) {
		t.Fatalf("unsupported class error = %v", err)
	}
	if _, err := VerifyManifest(signedManifest(t, private, "https://example.invalid", 1), public); !isCode(err, ExitIncompatible) {
		t.Fatalf("invalid digest error = %v", err)
	}
}

type fakeRow struct {
	values []any
	err    error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	for i := range dest {
		switch value := dest[i].(type) {
		case *bool:
			*value = r.values[i].(bool)
		case *int:
			*value = r.values[i].(int)
		}
	}
	return nil
}

type fakeDB struct {
	rows []fakeRow
}

func (db *fakeDB) QueryRow(context.Context, string, ...any) pgx.Row {
	row := db.rows[0]
	db.rows = db.rows[1:]
	return row
}

func isCode(err error, code int) bool {
	var failure *Failure
	return errors.As(err, &failure) && failure.Code == code
}

func TestReadFloorMissingAndValidMarker(t *testing.T) {
	floor, err := ReadFloor(context.Background(), &fakeDB{rows: []fakeRow{{values: []any{32, false, false}}, {values: []any{false}}}})
	if err != nil || floor != 0 {
		t.Fatalf("missing marker floor=%d err=%v", floor, err)
	}
	floor, err = ReadFloor(context.Background(), &fakeDB{rows: []fakeRow{{values: []any{33, true, false}}, {values: []any{true}}, {values: []any{1, 1}}}})
	if err != nil || floor != 1 {
		t.Fatalf("valid marker floor=%d err=%v", floor, err)
	}
	_, err = ReadFloor(context.Background(), &fakeDB{rows: []fakeRow{{values: []any{33, true, false}}, {values: []any{true}}, {err: pgx.ErrNoRows}}})
	if !isCode(err, ExitIncompatible) {
		t.Fatalf("missing singleton row error=%v", err)
	}
}

func TestReadFloorRejectsMigrationMarkerMismatch(t *testing.T) {
	tests := []struct {
		name string
		rows []fakeRow
	}{
		{"migration 33 without table", []fakeRow{{values: []any{33, true, false}}, {values: []any{false}}}},
		{"migration 33 without singleton", []fakeRow{{values: []any{33, true, false}}, {values: []any{true}}, {err: pgx.ErrNoRows}}},
		{"marker before migration 33", []fakeRow{{values: []any{32, false, false}}, {values: []any{true}}}},
		{"wrong marker schema", []fakeRow{{values: []any{33, true, false}}, {values: []any{true}}, {values: []any{2, 1}}}},
		{"migration 34 with floor one", []fakeRow{{values: []any{34, true, true}}, {values: []any{true}}, {values: []any{1, 1}}}},
		{"floor two before migration 34", []fakeRow{{values: []any{33, true, false}}, {values: []any{true}}, {values: []any{1, 2}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ReadFloor(context.Background(), &fakeDB{rows: test.rows}); !isCode(err, ExitIncompatible) {
				t.Fatalf("ReadFloor error = %v, want incompatible", err)
			}
		})
	}
}

func TestReadFloorDatabaseErrorIsUnavailable(t *testing.T) {
	_, err := ReadFloor(context.Background(), &fakeDB{rows: []fakeRow{{err: errors.New("connection refused")}}})
	if !isCode(err, ExitDatabaseUnavailable) {
		t.Fatalf("database error=%v", err)
	}
}

func TestDescriptorExecutionUsesVerifiedFileAfterPathSwap(t *testing.T) {
	if os.Getenv("CONTROL_COMPAT_DESCRIPTOR_EXEC_HELPER") == "1" {
		artifact := os.Getenv("CONTROL_COMPAT_DESCRIPTOR_ARTIFACT")
		replacement := os.Getenv("CONTROL_COMPAT_DESCRIPTOR_REPLACEMENT")
		file, err := openArtifact(artifact)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := digestOpenedArtifact(file); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(replacement, artifact); err != nil {
			t.Fatal(err)
		}
		if err := os.Setenv("CONTROL_COMPAT_DESCRIPTOR_ARTIFACT_EXECUTED", "1"); err != nil {
			t.Fatal(err)
		}
		if err := execVerifiedFile(file, artifact, []string{artifact, "-test.run=^TestDescriptorArtifactExecuted$", "-test.v"}); err != nil {
			var failure *Failure
			if errors.As(err, &failure) {
				_, _ = fmt.Fprintf(os.Stderr, "%s: %v", failure.Reason, failure.cause)
				os.Exit(failure.Code)
			}
			os.Exit(ExitExecFailure)
		}
		t.Fatal("descriptor exec returned")
	}

	directory := t.TempDir()
	artifact := filepath.Join(directory, "control")
	replacement := filepath.Join(directory, "replacement")
	copyExecutable := func(source, destination string) error {
		data, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, data, 0o700)
	}
	if err := copyExecutable(os.Args[0], artifact); err != nil {
		t.Fatal(err)
	}
	falsePath := "/bin/false"
	if _, err := os.Stat(falsePath); err != nil {
		falsePath = "/usr/bin/false"
	}
	if err := copyExecutable(falsePath, replacement); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestDescriptorExecutionUsesVerifiedFileAfterPathSwap$")
	command.Env = append(os.Environ(),
		"CONTROL_COMPAT_DESCRIPTOR_EXEC_HELPER=1",
		"CONTROL_COMPAT_DESCRIPTOR_ARTIFACT="+artifact,
		"CONTROL_COMPAT_DESCRIPTOR_REPLACEMENT="+replacement,
	)
	output, err := command.CombinedOutput()
	if runtime.GOOS == "linux" {
		if err != nil {
			t.Fatalf("descriptor execution: %v: %s", err, output)
		}
		if !strings.Contains(string(output), "VERIFIED_ARTIFACT_A") {
			t.Fatalf("verified artifact did not execute: %q", output)
		}
	} else {
		exit, ok := err.(*exec.ExitError)
		if !ok || exit.ExitCode() != ExitExecFailure {
			t.Fatalf("unsupported descriptor execution = %v output=%s", err, output)
		}
		if strings.Contains(string(output), "B") {
			t.Fatalf("replacement artifact executed: %q", output)
		}
	}
}

func TestDescriptorArtifactExecuted(t *testing.T) {
	if os.Getenv("CONTROL_COMPAT_DESCRIPTOR_ARTIFACT_EXECUTED") != "1" {
		t.Skip("descriptor execution helper")
	}
	fmt.Print("VERIFIED_ARTIFACT_A")
}

func TestValidateClassAgainstFloor(t *testing.T) {
	if err := ValidateClass(1, 1); err != nil {
		t.Fatalf("class 1 at floor 1: %v", err)
	}
	if err := ValidateClass(0, 1); !isCode(err, ExitIncompatible) {
		t.Fatalf("class 0 at floor 1: %v", err)
	}
	if err := ValidateClass(1, 2); !isCode(err, ExitIncompatible) {
		t.Fatalf("class 1 at floor 2: %v", err)
	}
	if err := ValidateClass(2, 2); err != nil {
		t.Fatalf("class 2 at floor 2: %v", err)
	}
	if err := ValidateClass(3, 0); !isCode(err, ExitIncompatible) {
		t.Fatalf("unknown class: %v", err)
	}
}
