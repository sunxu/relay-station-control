package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanDirectoryFindsCanaryWithoutProjectingIt(t *testing.T) {
	directory := t.TempDir()
	canaries := []string{
		"endpoint-canary-71a2", "reference-canary-71a2", "key-canary-71a2",
		"email-canary-71a2", "body-canary-71a2", "header-canary-71a2", "error-canary-71a2",
	}
	artifact := filepath.Join(directory, "acceptance.log")
	if err := os.WriteFile(artifact, []byte("fixed aggregate evidence only"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := scanDirectory(directory, canaries); err != nil {
		t.Fatalf("clean artifacts rejected: %v", err)
	}
	if err := os.WriteFile(artifact, []byte("fixed\n"+canaries[4]+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := scanDirectory(directory, canaries)
	if !errors.Is(err, errSensitiveCanaryFound) {
		t.Fatalf("canary result = %v", err)
	}
	for _, canary := range canaries {
		if strings.Contains(err.Error(), canary) {
			t.Fatal("scanner error projected canary")
		}
	}
	if err := os.WriteFile(artifact, []byte("fixed aggregate evidence only"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, canaries[0]+".log"), []byte("fixed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := scanDirectory(directory, canaries); !errors.Is(err, errSensitiveCanaryFound) {
		t.Fatalf("canary filename result = %v", err)
	}
}

func TestScanDirectoryRejectsSymlinkAndMalformedCanaries(t *testing.T) {
	directory := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.log")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(directory, "artifact-link")); err != nil {
		t.Fatal(err)
	}
	canaries := []string{"canary-01", "canary-02", "canary-03", "canary-04", "canary-05", "canary-06", "canary-07"}
	if err := scanDirectory(directory, canaries); !errors.Is(err, errUnsafeArtifact) {
		t.Fatalf("symlink scan result = %v", err)
	}
	canaries[6] = canaries[0]
	if err := scanDirectory(t.TempDir(), canaries); !errors.Is(err, errInvalidScanConfiguration) {
		t.Fatalf("duplicate canary result = %v", err)
	}
}

func TestScanDirectoryCoversSnapshotIdentityUnknownFieldAndSQLCanaries(t *testing.T) {
	directory := t.TempDir()
	canaries := []string{
		"endpoint-canary-43b7", "reference-canary-43b7", "key-canary-43b7",
		"email-canary-43b7", "body-canary-43b7", "header-canary-43b7", "error-canary-43b7",
		"account-key-canary-43b7", "unknown-field-canary-43b7", "sql-parameter-canary-43b7",
	}
	artifact := filepath.Join(directory, "snapshot-acceptance.log")
	if err := os.WriteFile(artifact, []byte("fixed promotion classification only"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := scanDirectory(directory, canaries); err != nil {
		t.Fatalf("clean snapshot artifacts rejected: %v", err)
	}
	for _, index := range []int{7, 8, 9} {
		if err := os.WriteFile(artifact, []byte(canaries[index]), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := scanDirectory(directory, canaries); !errors.Is(err, errSensitiveCanaryFound) {
			t.Fatalf("snapshot canary %d result = %v", index, err)
		}
	}
	if err := scanDirectory(directory, canaries[:8]); !errors.Is(err, errInvalidScanConfiguration) {
		t.Fatalf("partial snapshot canary set result = %v", err)
	}
}
