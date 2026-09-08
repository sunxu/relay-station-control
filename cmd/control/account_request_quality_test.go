package main

import (
	"io"
	"log/slog"
	"testing"
)

func TestAccountRequestQualityRuntimeDisabledAndDependencies(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	t.Setenv("CONTROL_ACCOUNT_REQUEST_QUALITY_ENABLED", "")
	runtime, err := newAccountRequestQualityRuntime(nil, nodeDriverRuntime{}, logger)
	if err != nil || runtime != nil {
		t.Fatal("default must be disabled without dependencies")
	}
	t.Setenv("CONTROL_ACCOUNT_REQUEST_QUALITY_ENABLED", "true")
	if _, err := newAccountRequestQualityRuntime(nil, nodeDriverRuntime{}, logger); err == nil {
		t.Fatal("enabled without driver")
	}
	t.Setenv("CONTROL_ACCOUNT_REQUEST_QUALITY_ENABLED", "invalid")
	if _, err := newAccountRequestQualityRuntime(nil, nodeDriverRuntime{}, logger); err == nil {
		t.Fatal("invalid flag accepted")
	}
}
