package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func lifecycleCanaries() []string {
	canaries := make([]string, len(canaryEnvironmentNames))
	for index := range canaries {
		canaries[index] = "lifecycle-sensitive-canary-" + string(rune('a'+index)) + "-4f91"
	}
	return canaries
}

func TestScanDirectoryRejectsEveryLifecycleCanaryWithoutProjectingIt(t *testing.T) {
	directory := t.TempDir()
	artifact := filepath.Join(directory, "lifecycle-acceptance.log")
	canaries := lifecycleCanaries()
	if err := os.WriteFile(artifact, []byte("fixed aggregate classifications only"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := scanDirectory(directory, canaries); err != nil {
		t.Fatalf("clean artifact rejected: %v", err)
	}
	for _, canary := range canaries {
		if err := os.WriteFile(artifact, []byte(canary), 0o600); err != nil {
			t.Fatal(err)
		}
		err := scanDirectory(directory, canaries)
		if !errors.Is(err, errSensitiveCanary) {
			t.Fatalf("canary result = %v", err)
		}
		if strings.Contains(err.Error(), canary) {
			t.Fatal("scanner error projected canary")
		}
	}
	if err := os.WriteFile(artifact, []byte("fixed aggregate classifications only"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, canaries[0]+".log"), []byte("fixed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := scanDirectory(directory, canaries); !errors.Is(err, errSensitiveCanary) {
		t.Fatalf("filename canary result = %v", err)
	}
}

func TestScanDirectoryRejectsUnsafeArtifactsAndMalformedCanaries(t *testing.T) {
	directory := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.log")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(directory, "artifact-link")); err != nil {
		t.Fatal(err)
	}
	canaries := lifecycleCanaries()
	if err := scanDirectory(directory, canaries); !errors.Is(err, errUnsafeArtifact) {
		t.Fatalf("symlink result = %v", err)
	}
	canaries[len(canaries)-1] = canaries[0]
	if err := scanDirectory(t.TempDir(), canaries); !errors.Is(err, errInvalidConfiguration) {
		t.Fatalf("duplicate canary result = %v", err)
	}
}
