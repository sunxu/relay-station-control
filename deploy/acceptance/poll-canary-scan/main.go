package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	maximumArtifactFiles = 512
	maximumArtifactBytes = 64 << 20
	maximumOneFileBytes  = 8 << 20
)

var (
	errInvalidScanConfiguration = errors.New("poll canary scan configuration invalid")
	errUnsafeArtifact           = errors.New("poll canary scan artifact unsafe")
	errSensitiveCanaryFound     = errors.New("poll canary scan found sensitive material")
)

var pollCanaryEnvironmentNames = []string{
	"CONTROL_POLL_CANARY_ENDPOINT",
	"CONTROL_POLL_CANARY_SECRET_REFERENCE",
	"CONTROL_POLL_CANARY_SECRET_VALUE",
	"CONTROL_POLL_CANARY_EMAIL",
	"CONTROL_POLL_CANARY_RESPONSE_BODY",
	"CONTROL_POLL_CANARY_RESPONSE_HEADER",
	"CONTROL_POLL_CANARY_RAW_ERROR",
}

// Snapshot-specific canaries are an all-or-none extension so the existing
// poll-run scanner remains backward compatible while snapshot acceptance can
// also cover derived identities, unknown fields and SQL parameter artifacts.
var snapshotCanaryEnvironmentNames = []string{
	"CONTROL_POLL_CANARY_ACCOUNT_KEY",
	"CONTROL_POLL_CANARY_UNKNOWN_FIELD",
	"CONTROL_POLL_CANARY_SQL_PARAMETER",
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "account_inventory_poll_canary_scan=failed reason=fixed_security_check")
		os.Exit(1)
	}
	fmt.Println("account_inventory_poll_canary_scan=success")
}

func run() error {
	directory := os.Getenv("CONTROL_POLL_CANARY_SCAN_DIR")
	canaries := make([]string, 0, len(pollCanaryEnvironmentNames)+len(snapshotCanaryEnvironmentNames))
	for _, name := range pollCanaryEnvironmentNames {
		canaries = append(canaries, os.Getenv(name))
	}
	snapshotCanaryCount := 0
	for _, name := range snapshotCanaryEnvironmentNames {
		if os.Getenv(name) != "" {
			snapshotCanaryCount++
		}
	}
	if snapshotCanaryCount != 0 && snapshotCanaryCount != len(snapshotCanaryEnvironmentNames) {
		return errInvalidScanConfiguration
	}
	if snapshotCanaryCount == len(snapshotCanaryEnvironmentNames) {
		for _, name := range snapshotCanaryEnvironmentNames {
			canaries = append(canaries, os.Getenv(name))
		}
	}
	return scanDirectory(directory, canaries)
}

func scanDirectory(directory string, canaries []string) error {
	if directory == "" || !filepath.IsAbs(directory) || filepath.Clean(directory) != directory || !validCanaries(canaries) {
		return errInvalidScanConfiguration
	}
	information, err := os.Lstat(directory)
	if err != nil || !information.IsDir() || information.Mode()&os.ModeSymlink != 0 {
		return errUnsafeArtifact
	}
	fileCount := 0
	totalBytes := int64(0)
	return filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.Type()&os.ModeSymlink != 0 {
			return errUnsafeArtifact
		}
		relative, err := filepath.Rel(directory, path)
		if err != nil {
			return errUnsafeArtifact
		}
		for _, canary := range canaries {
			if strings.Contains(relative, canary) {
				return errSensitiveCanaryFound
			}
		}
		if entry.IsDir() {
			return nil
		}
		information, err := entry.Info()
		if err != nil || !information.Mode().IsRegular() || information.Size() < 0 || information.Size() > maximumOneFileBytes {
			return errUnsafeArtifact
		}
		fileCount++
		totalBytes += information.Size()
		if fileCount > maximumArtifactFiles || totalBytes > maximumArtifactBytes {
			return errUnsafeArtifact
		}
		encoded, err := os.ReadFile(path)
		if err != nil || int64(len(encoded)) != information.Size() {
			return errUnsafeArtifact
		}
		for _, canary := range canaries {
			if bytes.Contains(encoded, []byte(canary)) {
				return errSensitiveCanaryFound
			}
		}
		return nil
	})
}

func validCanaries(canaries []string) bool {
	if len(canaries) != len(pollCanaryEnvironmentNames) &&
		len(canaries) != len(pollCanaryEnvironmentNames)+len(snapshotCanaryEnvironmentNames) {
		return false
	}
	seen := make(map[string]struct{}, len(canaries))
	for _, canary := range canaries {
		if len(canary) < 8 || len(canary) > 512 || strings.TrimSpace(canary) != canary || strings.IndexAny(canary, "\r\n\x00") >= 0 {
			return false
		}
		if _, duplicate := seen[canary]; duplicate {
			return false
		}
		seen[canary] = struct{}{}
	}
	return true
}
