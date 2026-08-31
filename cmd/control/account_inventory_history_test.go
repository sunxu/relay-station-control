package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	controlhistory "github.com/sunxu/relay-station-control/internal/historyruntime"
)

type fakeAccountInventoryHistoryRepository struct {
	controlhistory.Repository
	controlhistory.MetricsProvider
	mutex      sync.Mutex
	checkError error
	checks     int
	statuses   []controlhistory.RuntimeStatus
	state      *controlhistory.RuntimeStatusState
}

func (repository *fakeAccountInventoryHistoryRepository) CheckHistoryCompatibility(context.Context) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	repository.checks++
	return repository.checkError
}

func (repository *fakeAccountInventoryHistoryRepository) ObserveRuntimeStatus(status controlhistory.RuntimeStatus) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	repository.statuses = append(repository.statuses, status)
	if repository.state == nil {
		repository.state = controlhistory.NewRuntimeStatusState()
	}
	repository.state.ObserveRuntimeStatus(status)
}

func (repository *fakeAccountInventoryHistoryRepository) snapshot() (int, []controlhistory.RuntimeStatus) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	return repository.checks, append([]controlhistory.RuntimeStatus(nil), repository.statuses...)
}

func (repository *fakeAccountInventoryHistoryRepository) runtimeStatus() controlhistory.RuntimeStatus {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	if repository.state == nil {
		return controlhistory.NewRuntimeStatusState().RuntimeStatus()
	}
	return repository.state.RuntimeStatus()
}

type fakeAccountInventoryHistoryLoops struct {
	run func(string, context.Context, context.Context) error
}

func (loops *fakeAccountInventoryHistoryLoops) call(name string, claim, operation context.Context) error {
	if loops.run == nil {
		<-claim.Done()
		return nil
	}
	return loops.run(name, claim, operation)
}

func (loops *fakeAccountInventoryHistoryLoops) RunPlanner(ctx context.Context) error {
	return loops.call("planner", ctx, ctx)
}
func (loops *fakeAccountInventoryHistoryLoops) RunWorker(claim, operation context.Context) error {
	return loops.call("worker", claim, operation)
}
func (loops *fakeAccountInventoryHistoryLoops) RunReconciler(ctx context.Context) error {
	return loops.call("reconciler", ctx, ctx)
}
func (loops *fakeAccountInventoryHistoryLoops) RunRollupWorker(claim, operation context.Context) error {
	return loops.call("rollup_worker", claim, operation)
}
func (loops *fakeAccountInventoryHistoryLoops) RunRollupReconciler(ctx context.Context) error {
	return loops.call("rollup_reconciler", ctx, ctx)
}
func (loops *fakeAccountInventoryHistoryLoops) RunRetentionWorker(claim, operation context.Context) error {
	return loops.call("retention_worker", claim, operation)
}

type fakeAccountInventoryHistoryService struct{ run func(context.Context) error }

func (service fakeAccountInventoryHistoryService) Run(ctx context.Context) error {
	return service.run(ctx)
}

func accountInventoryHistoryTestFactory(
	repository *fakeAccountInventoryHistoryRepository,
	loops controlhistory.Loops,
	loopsCalls *int,
) accountInventoryHistoryRuntimeFactory {
	return accountInventoryHistoryRuntimeFactory{
		newRepository: func(*pgxpool.Pool) (accountInventoryHistoryRepository, error) {
			return repository, nil
		},
		newLoops: func(controlhistory.Config, controlhistory.Repository) (controlhistory.Loops, error) {
			*loopsCalls++
			return loops, nil
		},
		newService: func(
			configuration controlhistory.Config,
			checker controlhistory.CompatibilityChecker,
			serviceLoops controlhistory.Loops,
			observer controlhistory.StatusObserver,
		) (accountInventoryHistoryService, error) {
			if checker != repository || observer != repository || serviceLoops != loops && serviceLoops != nil {
				return nil, errors.New("history components were not wired to the same repository")
			}
			return controlhistory.NewService(configuration, checker, serviceLoops, observer)
		},
	}
}

func clearHistoryEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		historyEnabledEnvironment, historyScanIntervalEnvironment, historyClaimLeaseEnvironment,
		historyConcurrencyEnvironment, historyDeleteBatchEnvironment, historyStatementTimeoutEnvironment,
		historyDatabaseBackoffInitialEnvironment, historyDatabaseBackoffMaximumEnvironment,
		historyShutdownGraceEnvironment,
	} {
		t.Setenv(name, "")
	}
}

func TestLoadAccountInventoryHistoryRuntimeConfigDefaultsDisabled(t *testing.T) {
	clearHistoryEnvironment(t)
	configuration, err := loadAccountInventoryHistoryRuntimeConfig()
	if err != nil {
		t.Fatal(err)
	}
	validated, err := configuration.Validate()
	if err != nil {
		t.Fatal(err)
	}
	if validated.Enabled() || validated.ScanInterval() != 30*time.Second ||
		validated.ClaimLease() != 30*time.Second || validated.Concurrency() != 1 ||
		validated.DeleteBatchSize() != 500 || validated.StatementTimeout() != 10*time.Second {
		t.Fatalf("unexpected defaults: %+v", configuration)
	}
}

func TestLoadAccountInventoryHistoryRuntimeConfigAcceptsExplicitBounds(t *testing.T) {
	clearHistoryEnvironment(t)
	t.Setenv(historyEnabledEnvironment, "true")
	t.Setenv(historyScanIntervalEnvironment, controlhistory.MaximumScanInterval.String())
	t.Setenv(historyClaimLeaseEnvironment, controlhistory.MaximumClaimLease.String())
	t.Setenv(historyConcurrencyEnvironment, "8")
	t.Setenv(historyDeleteBatchEnvironment, "5000")
	t.Setenv(historyStatementTimeoutEnvironment, controlhistory.MaximumStatementTimeout.String())
	t.Setenv(historyDatabaseBackoffInitialEnvironment, controlhistory.MaximumDatabaseBackoff.String())
	t.Setenv(historyDatabaseBackoffMaximumEnvironment, controlhistory.MaximumDatabaseBackoff.String())
	t.Setenv(historyShutdownGraceEnvironment, controlhistory.MaximumShutdownGrace.String())
	configuration, err := loadAccountInventoryHistoryRuntimeConfig()
	if err != nil {
		t.Fatal(err)
	}
	validated, err := configuration.Validate()
	if err != nil || !validated.Enabled() || validated.Concurrency() != controlhistory.MaximumConcurrency ||
		validated.DeleteBatchSize() != controlhistory.MaximumDeleteBatchSize {
		t.Fatalf("explicit configuration=%+v/%v", configuration, err)
	}
}

func TestLoadAccountInventoryHistoryRuntimeConfigRejectsInvalidAndUnsafeCombinations(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
	}{
		{historyEnabledEnvironment, "maybe"},
		{historyScanIntervalEnvironment, "999ms"},
		{historyClaimLeaseEnvironment, "301s"},
		{historyConcurrencyEnvironment, "9"},
		{historyDeleteBatchEnvironment, "5001"},
		{historyStatementTimeoutEnvironment, "31s"},
		{historyDatabaseBackoffInitialEnvironment, "99ms"},
		{historyDatabaseBackoffMaximumEnvironment, "301s"},
		{historyShutdownGraceEnvironment, "61s"},
	} {
		t.Run(test.name, func(t *testing.T) {
			clearHistoryEnvironment(t)
			t.Setenv(test.name, test.value)
			if _, err := loadAccountInventoryHistoryRuntimeConfig(); err == nil {
				t.Fatal("invalid history configuration accepted")
			}
		})
	}

	clearHistoryEnvironment(t)
	t.Setenv(historyClaimLeaseEnvironment, "10s")
	t.Setenv(historyStatementTimeoutEnvironment, "10s")
	if _, err := loadAccountInventoryHistoryRuntimeConfig(); err == nil {
		t.Fatal("statement timeout equal to claim lease accepted")
	}

	clearHistoryEnvironment(t)
	t.Setenv(historyDatabaseBackoffInitialEnvironment, "10s")
	t.Setenv(historyDatabaseBackoffMaximumEnvironment, "5s")
	if _, err := loadAccountInventoryHistoryRuntimeConfig(); err == nil {
		t.Fatal("inverted database backoff accepted")
	}

	clearHistoryEnvironment(t)
	t.Setenv(historyStatementTimeoutEnvironment, "20s")
	t.Setenv(historyShutdownGraceEnvironment, "19s")
	if _, err := loadAccountInventoryHistoryRuntimeConfig(); err == nil {
		t.Fatal("shutdown grace below statement timeout accepted")
	}
}

func TestHistoryPolicyThresholdsCannotBeConfigured(t *testing.T) {
	clearHistoryEnvironment(t)
	t.Setenv("CONTROL_ACCOUNT_INVENTORY_HISTORY_ELIGIBILITY_DELAY", "1h")
	t.Setenv("CONTROL_ACCOUNT_INVENTORY_HISTORY_RETENTION", "1h")
	t.Setenv("CONTROL_ACCOUNT_INVENTORY_HISTORY_COVERAGE_BASIS_POINTS", "1")
	configuration, err := loadAccountInventoryHistoryRuntimeConfig()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = configuration.Validate(); err != nil ||
		controlhistory.SnapshotEligibilityDelay != 72*time.Hour ||
		controlhistory.HistoryRetention != 30*24*time.Hour ||
		controlhistory.CompleteCoverageBasisPts != 9500 {
		t.Fatal("environment reconfigured fixed history policy")
	}
}

func TestAccountInventoryHistoryRuntimeDisabledStillChecksCompatibilityWithoutLoops(t *testing.T) {
	repository := &fakeAccountInventoryHistoryRepository{}
	loopsCalls := 0
	factory := accountInventoryHistoryTestFactory(repository, nil, &loopsCalls)
	factory.newLoops = nil
	runtime, err := newAccountInventoryHistoryRuntimeWithFactory(
		nil,
		controlhistory.Config{},
		factory,
	)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.enabled || runtime.repository != repository || loopsCalls != 0 {
		t.Fatalf("disabled runtime constructed active loops: enabled=%v loops=%d", runtime.enabled, loopsCalls)
	}
	if err = runAccountInventoryHistoryRuntime(context.Background(), runtime, nil); err != nil {
		t.Fatal(err)
	}
	checks, statuses := repository.snapshot()
	if checks != 1 || len(statuses) == 0 || statuses[len(statuses)-1].Reason != controlhistory.ReasonDisabled {
		t.Fatalf("disabled compatibility/status = %d/%+v", checks, statuses)
	}
	if status := repository.runtimeStatus(); status.Reason != controlhistory.ReasonDisabled || !status.Compatible {
		t.Fatalf("disabled repository status rejected by real state: %+v", status)
	}
}

func TestAccountInventoryHistoryRuntimeEnabledStartsAllLoopsAndShutsDown(t *testing.T) {
	repository := &fakeAccountInventoryHistoryRepository{}
	started := make(chan string, 6)
	loops := &fakeAccountInventoryHistoryLoops{run: func(name string, claim, _ context.Context) error {
		started <- name
		<-claim.Done()
		return nil
	}}
	loopsCalls := 0
	runtime, err := newAccountInventoryHistoryRuntimeWithFactory(
		nil,
		controlhistory.Config{Enabled: true},
		accountInventoryHistoryTestFactory(repository, loops, &loopsCalls),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !runtime.enabled || loopsCalls != 1 {
		t.Fatalf("enabled runtime = %v, loops construction=%d", runtime.enabled, loopsCalls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runAccountInventoryHistoryRuntime(ctx, runtime, nil) }()
	seen := make(map[string]bool, 6)
	for range 6 {
		select {
		case name := <-started:
			seen[name] = true
		case <-time.After(time.Second):
			t.Fatal("history loops did not all start")
		}
	}
	cancel()
	select {
	case err = <-done:
		if err != nil {
			t.Fatalf("graceful shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("history runtime did not drain on shutdown")
	}
	if len(seen) != 6 {
		t.Fatalf("started history loops = %v", seen)
	}
	checks, statuses := repository.snapshot()
	if checks != 1 || len(statuses) == 0 || statuses[0].Reason != controlhistory.ReasonReady {
		t.Fatalf("enabled compatibility/status = %d/%+v", checks, statuses)
	}
}

func TestAccountInventoryHistoryRuntimeIncompatibleDisablesOnlyHistory(t *testing.T) {
	repository := &fakeAccountInventoryHistoryRepository{checkError: errors.New("postgres://secret-canary")}
	loopRuns := 0
	loops := &fakeAccountInventoryHistoryLoops{run: func(string, context.Context, context.Context) error {
		loopRuns++
		return nil
	}}
	loopsCalls := 0
	runtime, err := newAccountInventoryHistoryRuntimeWithFactory(
		nil,
		controlhistory.Config{Enabled: true},
		accountInventoryHistoryTestFactory(repository, loops, &loopsCalls),
	)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	if err = runAccountInventoryHistoryRuntime(context.Background(), runtime, logger); err != nil {
		t.Fatalf("incompatible history escaped as control-fatal: %v", err)
	}
	checks, statuses := repository.snapshot()
	if checks != 1 || loopsCalls != 1 || loopRuns != 0 || len(statuses) == 0 ||
		statuses[len(statuses)-1].Reason != controlhistory.ReasonSchemaIncompatible {
		t.Fatalf("incompatible runtime = checks=%d construct=%d runs=%d statuses=%+v", checks, loopsCalls, loopRuns, statuses)
	}
	if status := repository.runtimeStatus(); status.Reason != controlhistory.ReasonSchemaIncompatible || status.Compatible {
		t.Fatalf("incompatible repository status rejected by real state: %+v", status)
	}
	if strings.Contains(output.String(), "secret-canary") || strings.Contains(output.String(), "postgres://") {
		t.Fatalf("compatibility details leaked: %s", output.String())
	}
}

func TestAccountInventoryHistoryRuntimeFatalStopsOnlyHistoryAndLogsFixedReason(t *testing.T) {
	repository := &fakeAccountInventoryHistoryRepository{}
	loops := &fakeAccountInventoryHistoryLoops{run: func(name string, claim, _ context.Context) error {
		if name == "planner" {
			return controlhistory.ErrHistoryStateInconsistent
		}
		<-claim.Done()
		return nil
	}}
	loopsCalls := 0
	runtime, err := newAccountInventoryHistoryRuntimeWithFactory(
		nil,
		controlhistory.Config{Enabled: true},
		accountInventoryHistoryTestFactory(repository, loops, &loopsCalls),
	)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	err = runAccountInventoryHistoryRuntime(context.Background(), runtime, logger)
	if !errors.Is(err, controlhistory.ErrRuntimeStopped) {
		t.Fatalf("fatal history result = %v", err)
	}
	logged := output.String()
	if !strings.Contains(logged, `"component":"account_inventory_history"`) ||
		!strings.Contains(logged, `"reason":"runtime_stopped"`) ||
		strings.Contains(logged, "secret") || strings.Contains(logged, "postgres://") {
		t.Fatalf("history fatal log was not fixed and redacted: %s", logged)
	}
}

func TestAccountInventoryHistoryRuntimeFailureLogsUseFixedReasons(t *testing.T) {
	marker := errors.New("raw-history-error-marker")
	for _, test := range []struct {
		name   string
		err    error
		reason string
	}{
		{name: "service stopped", err: marker, reason: "service_stopped"},
		{name: "runtime stopped", err: errors.Join(controlhistory.ErrRuntimeStopped, marker), reason: "runtime_stopped"},
		{name: "shutdown timed out", err: errors.Join(controlhistory.ErrShutdownTimedOut, marker), reason: "shutdown_timed_out"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime := accountInventoryHistoryRuntime{enabled: true, service: fakeAccountInventoryHistoryService{
				run: func(context.Context) error { return test.err },
			}}
			var output bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&output, nil))
			if err := runAccountInventoryHistoryRuntime(context.Background(), runtime, logger); err != test.err {
				t.Fatalf("result = %v", err)
			}
			var logged map[string]any
			if json.Unmarshal(output.Bytes(), &logged) != nil || len(logged) != 5 ||
				logged["level"] != "ERROR" || logged["msg"] != "account inventory history stopped" ||
				logged["component"] != "account_inventory_history" || logged["reason"] != test.reason {
				t.Fatalf("unexpected structured log: %s", output.String())
			}
			if _, exists := logged["error"]; exists || strings.Contains(output.String(), marker.Error()) {
				t.Fatalf("raw error leaked: %s", output.String())
			}
		})
	}
}
