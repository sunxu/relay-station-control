package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sunxu/relay-station-control/internal/assetcredential"
)

func TestRunCreatesAndPreservesKeyWithoutPrintingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "asset-credential-key")
	var output bytes.Buffer
	if err := run([]string{"--path", path, "--mode", "0600"}, &output, &output); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != assetcredential.KeySize || strings.Contains(output.String(), string(first)) {
		t.Fatal("provisioning output exposed or failed to create key material")
	}
	output.Reset()
	if err := run([]string{"--path", path}, &output, &output); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) || output.String() != "asset credential key preserved\n" {
		t.Fatalf("repeat provisioning changed key or status: output=%q", output.String())
	}
}
