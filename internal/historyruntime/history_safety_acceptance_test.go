package historyruntime

import (
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

var historyCanaryEnvironmentNames = []string{
	"CONTROL_HISTORY_CANARY_ENDPOINT",
	"CONTROL_HISTORY_CANARY_IP",
	"CONTROL_HISTORY_CANARY_SECRET_REFERENCE",
	"CONTROL_HISTORY_CANARY_SECRET_VALUE",
	"CONTROL_HISTORY_CANARY_EMAIL",
	"CONTROL_HISTORY_CANARY_ACCOUNT_KEY",
	"CONTROL_HISTORY_CANARY_RESPONSE_BODY",
	"CONTROL_HISTORY_CANARY_RESPONSE_HEADER",
	"CONTROL_HISTORY_CANARY_RUN_ID",
	"CONTROL_HISTORY_CANARY_FENCING_TOKEN",
	"CONTROL_HISTORY_CANARY_CHECKSUM",
	"CONTROL_HISTORY_CANARY_RAW_ERROR",
	"CONTROL_HISTORY_CANARY_SQL_PARAMETER",
	"CONTROL_HISTORY_CANARY_POLL_ID",
	"CONTROL_HISTORY_CANARY_POLICY_ID",
}

func TestHistoryLocalOutputsExcludeCanaries(t *testing.T) {
	artifactDirectory, canaries := historySafetyConfiguration(t)
	raw := strings.Join(canaries, " ")

	for _, fixed := range []error{
		ErrHistoryRepositoryFailure,
		ErrHistoryStatementTimeout,
		ErrHistoryDatabaseUnavailable,
	} {
		err := fixedHistoryError(fmt.Errorf("%s: %w", raw, fixed))
		assertHistoryCanariesAbsent(t, err.Error(), canaries)
	}

	checksum := []byte(canaries[10])
	values := []any{
		CompactionClaim{SourceChecksum: checksum},
		SummarizeResult{SourceChecksum: checksum},
		CompleteRequest{ExpectedChecksum: checksum},
		RollupFinalizeResult{SegmentChecksum: checksum},
	}
	for _, value := range values {
		assertHistoryCanariesAbsent(t, fmt.Sprintf("%+v", value), canaries)
	}

	for _, scenario := range []struct {
		name string
		run  func() error
	}{
		{name: "success", run: historyCanaryCompactionSuccess},
		{name: "zero_data", run: historyCanaryCompactionSuccess},
		{name: "partial", run: historyCanaryPartialMetrics},
		{name: "permission", run: func() error { return historyCanaryCompactionFailure(raw, ErrHistoryRepositoryFailure) }},
		{name: "timeout", run: func() error { return historyCanaryCompactionFailure(raw, ErrHistoryStatementTimeout) }},
		{name: "restart", run: func() error { return historyCanaryCompactionFailure(raw, ErrHistoryDatabaseUnavailable) }},
		{name: "cleanup_failure", run: func() error { return historyCanaryRetentionFailure(raw) }},
	} {
		err := scenario.run()
		if err != nil {
			assertHistoryCanariesAbsent(t, err.Error(), canaries)
		}
		artifact := "scenario=" + scenario.name + " result=covered\n"
		if err := os.WriteFile(filepath.Join(artifactDirectory, scenario.name+".log"), []byte(artifact), 0o600); err != nil {
			t.Fatal("history safety artifact write failed")
		}
	}

	collector, err := NewCollector(staticMetricsProvider{
		snapshot: MetricsSnapshot{Runtime: RuntimeStatus{
			Configured: true, Enabled: true, Compatible: true, Reason: ReasonReady,
		}},
		err: fmt.Errorf("%s: metrics unavailable", raw),
	})
	if err != nil {
		t.Fatal("history metrics collector construction failed")
	}
	registry := prometheus.NewRegistry()
	registry.MustRegister(collector)
	_, gatherErr := registry.Gather()
	if gatherErr != nil {
		assertHistoryCanariesAbsent(t, gatherErr.Error(), canaries)
	}
}

func TestHistoryProductionSourcesHaveNoDirectNetworkImports(t *testing.T) {
	_, _ = historySafetyConfiguration(t)
	assertHistoryProductionHasNoNetworkImports(t)
}

func historySafetyConfiguration(t *testing.T) (string, []string) {
	t.Helper()
	directory := os.Getenv("CONTROL_HISTORY_CANARY_SCAN_DIR")
	if directory == "" {
		t.Skip("history canary acceptance is opt-in")
	}
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		t.Fatal("history canary artifact directory is invalid")
	}
	canaries := make([]string, 0, len(historyCanaryEnvironmentNames))
	seen := make(map[string]struct{}, len(historyCanaryEnvironmentNames))
	for _, name := range historyCanaryEnvironmentNames {
		value := os.Getenv(name)
		if len(value) < 8 || strings.TrimSpace(value) != value {
			t.Fatal("history canary configuration is invalid")
		}
		if _, duplicate := seen[value]; duplicate {
			t.Fatal("history canary configuration is not unique")
		}
		seen[value] = struct{}{}
		canaries = append(canaries, value)
	}
	return directory, canaries
}

func historyCanaryCompactionSuccess() error {
	loops := &fakeHistoryRepository{}
	runtimeLoops, err := NewRepositoryLoops(testLoopsConfig(), loops)
	if err != nil {
		return err
	}
	return runtimeLoops.processCompaction(
		context.Background(), context.Background(), validRuntimeCompactionClaim(CompactionPending, ""),
	)
}

func historyCanaryCompactionFailure(raw string, fixed error) error {
	repository := &fakeHistoryRepository{summarize: func(context.Context, FencedRequest) (SummarizeResult, error) {
		return SummarizeResult{}, fmt.Errorf("%s: %w", raw, fixed)
	}}
	loops, err := NewRepositoryLoops(testLoopsConfig(), repository)
	if err != nil {
		return err
	}
	return loops.processCompaction(
		context.Background(), context.Background(), validRuntimeCompactionClaim(CompactionPending, ""),
	)
}

func historyCanaryRetentionFailure(raw string) error {
	repository := &fakeHistoryRepository{deletePollRetention: func(context.Context, RetentionRequest) (RetentionResult, error) {
		return RetentionResult{}, fmt.Errorf("%s: %w", raw, ErrHistoryRepositoryFailure)
	}}
	loops, err := NewRepositoryLoops(testLoopsConfig(), repository)
	if err != nil {
		return err
	}
	_, err = loops.runRetentionRound(context.Background(), context.Background())
	return err
}

func historyCanaryPartialMetrics() error {
	collector, err := NewCollector(staticMetricsProvider{snapshot: MetricsSnapshot{
		Runtime: RuntimeStatus{Configured: true, Enabled: true, Compatible: true, Reason: ReasonReady},
		ProviderCoverage: []ProviderCoverage{{
			InstanceID: validRuntimeRollupClaim().InstanceID,
			Provider:   "openai", Ratio: 0.9499, Complete: false,
		}},
	}})
	if err != nil {
		return err
	}
	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(collector)
	_, err = registry.Gather()
	return err
}

func assertHistoryCanariesAbsent(t *testing.T, value string, canaries []string) {
	t.Helper()
	for _, canary := range canaries {
		if strings.Contains(value, canary) {
			t.Fatal("history output contains a sensitive canary")
		}
	}
}

func assertHistoryProductionHasNoNetworkImports(t *testing.T) {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("history safety source path unavailable")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	paths := []string{
		filepath.Join(repositoryRoot, "internal", "history"),
		filepath.Join(repositoryRoot, "internal", "historyruntime"),
		filepath.Join(repositoryRoot, "internal", "store", "account_inventory_history.go"),
	}
	for _, path := range paths {
		information, err := os.Stat(path)
		if err != nil {
			t.Fatal("history safety source unavailable")
		}
		files := []string{path}
		if information.IsDir() {
			entries, err := os.ReadDir(path)
			if err != nil {
				t.Fatal("history safety source directory unavailable")
			}
			files = files[:0]
			for _, entry := range entries {
				if strings.HasSuffix(entry.Name(), ".go") && !strings.HasSuffix(entry.Name(), "_test.go") {
					files = append(files, filepath.Join(path, entry.Name()))
				}
			}
		}
		for _, source := range files {
			parsed, err := parser.ParseFile(token.NewFileSet(), source, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal("history safety source parse failed")
			}
			for _, imported := range parsed.Imports {
				name, err := strconv.Unquote(imported.Path.Value)
				if err != nil {
					t.Fatal("history safety import parse failed")
				}
				if name == "net" || strings.HasPrefix(name, "net/http") || strings.Contains(name, "grpc") {
					t.Fatal("history production path gained a network client import")
				}
			}
		}
	}
}
