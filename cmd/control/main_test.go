package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	controljobs "github.com/sunxu/relay-station-control/internal/jobs"
	assetstore "github.com/sunxu/relay-station-control/internal/store"
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

func TestNewDatabasePoolConfigMaximumConnections(t *testing.T) {
	const databaseURL = "postgres://control@127.0.0.1/control?sslmode=disable"
	baseline, err := newDatabasePoolConfig(databaseURL, "")
	if err != nil {
		t.Fatal(err)
	}
	pgxDefault, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.MaxConns != pgxDefault.MaxConns {
		t.Fatalf("missing maximum changed pgx default: got %d want %d", baseline.MaxConns, pgxDefault.MaxConns)
	}

	for _, maximum := range []int32{databaseMaxConnsMinimum, databaseMaxConnsMaximum} {
		config, configErr := newDatabasePoolConfig(databaseURL, strconv.FormatInt(int64(maximum), 10))
		if configErr != nil {
			t.Fatalf("maximum %d: %v", maximum, configErr)
		}
		if config.MaxConns != maximum {
			t.Fatalf("maximum %d produced %d", maximum, config.MaxConns)
		}
	}

	for _, invalid := range []string{"0", "101", "-1", "invalid"} {
		if _, configErr := newDatabasePoolConfig(databaseURL, invalid); configErr == nil || configErr.Error() != "database configuration is invalid" {
			t.Fatalf("invalid maximum %q returned %v", invalid, configErr)
		}
	}
}

func TestNewDatabasePoolReadsMaximumConnectionsEnvironment(t *testing.T) {
	t.Setenv(databaseMaxConnsEnvironment, "1")
	pool, err := newDatabasePool(context.Background(), "postgres://control@127.0.0.1/control?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if pool.Stat().MaxConns() != databaseMaxConnsMinimum {
		t.Fatalf("pool maximum = %d, want %d", pool.Stat().MaxConns(), databaseMaxConnsMinimum)
	}
}

func TestNewDatabasePoolRejectsInvalidMaximumWithoutLeaking(t *testing.T) {
	const canary = "101-database-pool-canary"
	t.Setenv(databaseMaxConnsEnvironment, canary)
	_, err := newDatabasePool(context.Background(), "postgres://control@127.0.0.1/control?sslmode=disable")
	if err == nil || err.Error() != "database configuration is invalid" {
		t.Fatalf("invalid maximum returned %v", err)
	}
	if strings.Contains(err.Error(), canary) {
		t.Fatalf("database configuration error leaked value: %v", err)
	}
}

func TestLoadJobRuntimeConfigDefaultsAndBounds(t *testing.T) {
	for _, name := range []string{
		"CONTROL_JOB_WORKER_CONCURRENCY", "CONTROL_JOB_RECONCILER_CONCURRENCY",
		"CONTROL_JOB_POLL_INTERVAL", "CONTROL_JOB_RECONCILE_INTERVAL",
		"CONTROL_JOB_DATABASE_BACKOFF", "CONTROL_JOB_SHUTDOWN_GRACE",
	} {
		t.Setenv(name, "")
	}
	config, err := loadJobRuntimeConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.workerConcurrency != 4 || config.reconcilerConcurrency != 2 ||
		config.pollInterval != time.Second || config.reconcileInterval != 5*time.Second ||
		config.databaseBackoff != 5*time.Second || config.shutdownGrace != 8*time.Second {
		t.Fatalf("unexpected durable job defaults: %+v", config)
	}

	t.Setenv("CONTROL_JOB_WORKER_CONCURRENCY", "33")
	if _, err = loadJobRuntimeConfig(); err == nil {
		t.Fatal("worker concurrency above the closed maximum was accepted")
	}
	t.Setenv("CONTROL_JOB_WORKER_CONCURRENCY", "4")
	t.Setenv("CONTROL_JOB_POLL_INTERVAL", "99ms")
	if _, err = loadJobRuntimeConfig(); err == nil {
		t.Fatal("poll interval below the safe minimum was accepted")
	}
}

func TestJobCatalogMatchesEveryPersistedPolicyField(t *testing.T) {
	database := []assetstore.JobKindPolicy{{
		JobKind: "z.test", PayloadSchemaVersion: 2, Timeout: 10 * time.Second,
		LeaseDuration: 20 * time.Second, HeartbeatInterval: 5 * time.Second,
		MaxAttempts: 3, MaxVerificationAttempts: 4, ReplaySafe: true, RollbackAllowed: true,
	}}
	runtime := []controljobs.CatalogEntry{{
		Kind: "z.test", SchemaVersion: 2, Timeout: 10 * time.Second,
		LeaseDuration: 20 * time.Second, HeartbeatInterval: 5 * time.Second,
		MaxAttempts: 3, MaxVerifyAttempts: 4, ReplaySafe: true, AllowRollback: true,
	}}
	if !jobCatalogMatches(database, runtime) {
		t.Fatal("identical catalogs did not match")
	}

	mismatches := []func(*assetstore.JobKindPolicy){
		func(policy *assetstore.JobKindPolicy) { policy.JobKind = "a.test" },
		func(policy *assetstore.JobKindPolicy) { policy.PayloadSchemaVersion++ },
		func(policy *assetstore.JobKindPolicy) { policy.Timeout++ },
		func(policy *assetstore.JobKindPolicy) { policy.LeaseDuration++ },
		func(policy *assetstore.JobKindPolicy) { policy.HeartbeatInterval++ },
		func(policy *assetstore.JobKindPolicy) { policy.MaxAttempts++ },
		func(policy *assetstore.JobKindPolicy) { policy.MaxVerificationAttempts++ },
		func(policy *assetstore.JobKindPolicy) { policy.ReplaySafe = false },
		func(policy *assetstore.JobKindPolicy) { policy.RollbackAllowed = false },
	}
	for index, mutate := range mismatches {
		copy := append([]assetstore.JobKindPolicy(nil), database...)
		mutate(&copy[0])
		if jobCatalogMatches(copy, runtime) {
			t.Fatalf("catalog mismatch %d was accepted", index)
		}
	}
	if jobCatalogMatches(nil, runtime) || jobCatalogMatches(database, nil) {
		t.Fatal("catalog cardinality mismatch was accepted")
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

func TestRetiredCookieVariableDoesNotAffectStartup(t *testing.T) {
	for _, value := range []string{"true", "false", "invalid-retired-value"} {
		t.Run(value, func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=TestControlEnvironmentGateHelperProcess")
			command.Env = []string{
				"PATH=" + os.Getenv("PATH"),
				"GO_WANT_CONTROL_ENVIRONMENT_GATE_HELPER=1",
				"CONTROL_ENVIRONMENT_ID=local-http-test", "CONTROL_ENVIRONMENT=dev",
				"CONTROL_HTTP_ADDR=0.0.0.0:8080", "CONTROL_COOKIE_SECURE=" + value,
			}
			output, err := command.CombinedOutput()
			if err == nil || !strings.Contains(string(output), "database configuration is required") {
				t.Fatalf("retired variable prevented config validation: %s", output)
			}
			if strings.Contains(string(output), value) {
				t.Fatal("retired variable echoed into startup log")
			}
		})
	}
}
