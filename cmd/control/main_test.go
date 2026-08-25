package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestNewDatabasePoolRejectsInvalidURLWithoutLeaking(t *testing.T) {
	_, err := newDatabasePool(context.Background(), "postgres://secret@%invalid/control")
	if err == nil {
		t.Fatal("invalid database URL unexpectedly accepted")
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "postgres://") {
		t.Fatalf("database configuration error leaked URL: %v", err)
	}
}

func TestNewDatabasePoolUsesUTC(t *testing.T) {
	databaseURL := os.Getenv("CONTROL_RUNTIME_DATABASE_TEST_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("CONTROL_DATABASE_TEST_URL")
	}
	if databaseURL == "" {
		t.Skip("set CONTROL_RUNTIME_DATABASE_TEST_URL or CONTROL_DATABASE_TEST_URL")
	}
	pool, err := newDatabasePool(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("new database pool: %v", err)
	}
	t.Cleanup(pool.Close)
	var timezone string
	if err = pool.QueryRow(context.Background(), "SHOW TIME ZONE").Scan(&timezone); err != nil {
		t.Fatalf("show time zone: %v", err)
	}
	if timezone != "UTC" {
		t.Fatalf("database time zone = %q, want UTC", timezone)
	}
}

func TestEnvironmentGateRejectsMissingIDBeforeDatabaseOrListener(t *testing.T) {
	command := exec.Command(os.Args[0], "-test.run=TestControlEnvironmentGateHelperProcess")
	command.Env = append(os.Environ(),
		"GO_WANT_CONTROL_ENVIRONMENT_GATE_HELPER=1",
		"CONTROL_ENVIRONMENT_ID=",
		"CONTROL_ENVIRONMENT=dev",
		"CONTROL_HTTP_ADDR=127.0.0.1:0",
		"DATABASE_URL=postgres://must-not-be-used.invalid/control",
	)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	err := command.Run()
	if err == nil {
		t.Fatal("control unexpectedly started without CONTROL_ENVIRONMENT_ID")
	}
	text := output.String()
	if !strings.Contains(text, `"component":"environment"`) || !strings.Contains(text, `"reason":"missing_config"`) {
		t.Fatalf("missing sanitized environment reason: %s", text)
	}
	for _, forbidden := range []string{"must-not-be-used.invalid", "postgres://"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("startup output exposed %q: %s", forbidden, text)
		}
	}
}

func TestControlEnvironmentGateHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_CONTROL_ENVIRONMENT_GATE_HELPER") != "1" {
		return
	}
	main()
}
