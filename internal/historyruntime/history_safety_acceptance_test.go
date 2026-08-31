package historyruntime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/expfmt"
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
	var sinks strings.Builder

	for _, fixed := range []error{
		ErrHistoryRepositoryFailure,
		ErrHistoryStatementTimeout,
		ErrHistoryDatabaseUnavailable,
	} {
		err := fixedHistoryError(fmt.Errorf("%s: %w", raw, fixed))
		assertHistoryCanariesAbsent(t, err.Error(), canaries)
		sinks.WriteString(err.Error())
	}

	runID, runErr := uuid.Parse(canaries[8])
	fencingToken, fenceErr := uuid.Parse(canaries[9])
	policyID, policyErr := uuid.Parse(canaries[14])
	if runErr != nil || fenceErr != nil || policyErr != nil || len(canaries[10]) > 32 {
		t.Fatal("history canary identifiers are invalid")
	}
	checksum := make([]byte, 32)
	copy(checksum, canaries[10])
	claim := validRuntimeCompactionClaim(CompactionPending, "")
	claim.RunID, claim.FencingToken, claim.ProviderPolicyVersion = runID, fencingToken, policyID
	values := []any{
		claim,
		SummarizeResult{SourceChecksum: checksum},
		CompleteRequest{RunID: runID, FencingToken: fencingToken, ExpectedChecksum: checksum},
		RollupFinalizeResult{SegmentChecksum: checksum},
	}
	for _, value := range values {
		formatted := fmt.Sprintf("%+v", value)
		assertHistoryCanariesAbsent(t, formatted, canaries)
		sinks.WriteString(formatted)
	}

	sourceCalls := 0
	sourceBoundary := func() error {
		sourceCalls++
		for _, canary := range canaries {
			if !strings.Contains(raw, canary) {
				return ErrHistoryRepositoryFailure
			}
		}
		return nil
	}
	for _, scenario := range []struct {
		name string
		run  func() (string, error)
		want error
	}{
		{name: "success", run: func() (string, error) {
			return "", historyCanaryCompaction(claim, checksum, 1, sourceBoundary)
		}},
		{name: "zero_data", run: func() (string, error) {
			return historyCanaryZeroMetrics()
		}},
		{name: "partial", run: func() (string, error) {
			return historyCanaryPartialMetrics(sourceBoundary)
		}},
		{name: "permission", run: func() (string, error) {
			return "", historyCanaryCompactionFailure(raw, ErrHistoryRepositoryFailure)
		}, want: ErrHistoryRepositoryFailure},
		{name: "timeout", run: func() (string, error) {
			return "", historyCanaryCompactionFailure(raw, ErrHistoryStatementTimeout)
		}, want: ErrHistoryStatementTimeout},
		{name: "restart", run: func() (string, error) {
			return "", historyCanaryCompactionFailure(raw, ErrHistoryDatabaseUnavailable)
		}, want: ErrHistoryDatabaseUnavailable},
		{name: "cleanup_failure", run: func() (string, error) {
			return "", historyCanaryRetentionFailure(raw)
		}, want: ErrHistoryRepositoryFailure},
	} {
		output, err := scenario.run()
		if (scenario.want == nil && err != nil) || (scenario.want != nil && !errors.Is(err, scenario.want)) {
			t.Fatal("history canary scenario result mismatch")
		}
		assertHistoryCanariesAbsent(t, output, canaries)
		sinks.WriteString(output)
		if err != nil {
			assertHistoryCanariesAbsent(t, err.Error(), canaries)
			sinks.WriteString(err.Error())
		}
		artifact := "scenario=" + scenario.name + " result=covered\n"
		if err := os.WriteFile(filepath.Join(artifactDirectory, scenario.name+".log"), []byte(artifact), 0o600); err != nil {
			t.Fatal("history safety artifact write failed")
		}
	}
	if sourceCalls != 2 {
		t.Fatal("history fake source boundary coverage is incomplete")
	}

	metricsText, metricsErr := historyCanaryMetricsText(MetricsSnapshot{Runtime: RuntimeStatus{
		Configured: true, Enabled: true, Compatible: true, Reason: ReasonReady,
	}}, fmt.Errorf("%s: metrics unavailable", raw))
	if metricsErr != nil {
		assertHistoryCanariesAbsent(t, metricsErr.Error(), canaries)
		sinks.WriteString(metricsErr.Error())
	}
	assertHistoryCanariesAbsent(t, metricsText, canaries)
	sinks.WriteString(metricsText)

	var historyLog bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&historyLog, nil))
	for _, reason := range []string{"service_stopped", "runtime_stopped", "shutdown_timed_out"} {
		logger.Error("account inventory history stopped",
			"component", "account_inventory_history", "reason", reason)
	}
	assertHistoryCanariesAbsent(t, historyLog.String(), canaries)
	sinks.WriteString(historyLog.String())

	marker := "local_sink_canary=covered scenarios=7\n"
	if err := os.WriteFile(filepath.Join(artifactDirectory, "local-sink.log"), []byte(marker), 0o600); err != nil {
		t.Fatal("history safety marker write failed")
	}
	entries, err := os.ReadDir(artifactDirectory)
	if err != nil {
		t.Fatal("history safety artifact read failed")
	}
	for _, entry := range entries {
		contents, readErr := os.ReadFile(filepath.Join(artifactDirectory, entry.Name()))
		if readErr != nil {
			t.Fatal("history safety artifact read failed")
		}
		assertHistoryCanariesAbsent(t, string(contents), canaries)
		sinks.Write(contents)
	}
	assertHistoryCanariesAbsent(t, sinks.String(), canaries)
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

func historyCanaryCompaction(
	claim CompactionClaim, checksum []byte, sourceCount uint64, source func() error,
) error {
	repository := &fakeHistoryRepository{
		summarize: func(_ context.Context, request FencedRequest) (SummarizeResult, error) {
			if request.RunID != claim.RunID || request.FencingToken != claim.FencingToken {
				return SummarizeResult{}, ErrHistoryRepositoryFailure
			}
			if source != nil {
				if err := source(); err != nil {
					return SummarizeResult{}, err
				}
			}
			return SummarizeResult{SourceChecksum: append([]byte(nil), checksum...), SourceSnapshotCount: sourceCount}, nil
		},
		deleteBatch: func(_ context.Context, request DeleteBatchRequest) (DeleteBatchResult, error) {
			if request.RunID != claim.RunID || request.FencingToken != claim.FencingToken {
				return DeleteBatchResult{}, ErrHistoryRepositoryFailure
			}
			return DeleteBatchResult{DeletedRows: sourceCount, TotalDeletedRows: sourceCount}, nil
		},
		complete: func(_ context.Context, request CompleteRequest) (CompleteResult, error) {
			if request.RunID != claim.RunID || request.FencingToken != claim.FencingToken ||
				!bytes.Equal(request.ExpectedChecksum, checksum) {
				return CompleteResult{}, ErrHistoryRepositoryFailure
			}
			return CompleteResult{Completed: true}, nil
		},
	}
	runtimeLoops, err := NewRepositoryLoops(testLoopsConfig(), repository)
	if err != nil {
		return err
	}
	return runtimeLoops.processCompaction(
		context.Background(), context.Background(), claim,
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

func historyCanaryZeroMetrics() (string, error) {
	output, err := historyCanaryMetricsText(MetricsSnapshot{
		Runtime: RuntimeStatus{Configured: true, Enabled: true, Compatible: true, Reason: ReasonReady},
	}, nil)
	if err == nil && strings.Contains(output, "provider_coverage") {
		return "", ErrHistoryRepositoryFailure
	}
	return output, err
}

func historyCanaryPartialMetrics(source func() error) (string, error) {
	if err := source(); err != nil {
		return "", err
	}
	return historyCanaryMetricsText(MetricsSnapshot{
		Runtime: RuntimeStatus{Configured: true, Enabled: true, Compatible: true, Reason: ReasonReady},
		ProviderCoverage: []ProviderCoverage{{
			InstanceID: validRuntimeRollupClaim().InstanceID,
			Provider:   "openai", Ratio: 0.9499, Complete: false,
		}},
	}, nil)
}

func historyCanaryMetricsText(snapshot MetricsSnapshot, providerErr error) (string, error) {
	collector, err := NewCollector(staticMetricsProvider{snapshot: snapshot, err: providerErr})
	if err != nil {
		return "", err
	}
	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(collector)
	families, err := registry.Gather()
	if err != nil {
		return "", err
	}
	var output strings.Builder
	for _, family := range families {
		if _, err := expfmt.MetricFamilyToText(&output, family); err != nil {
			return "", err
		}
	}
	return output.String(), nil
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
