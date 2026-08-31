package historyshellacceptance

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

var requiredHistoryRaceTests = []struct {
	packageName string
	testName    string
}{
	{"./internal/history", "TestUTCDayEligibilityBoundary"},
	{"./internal/history", "TestPlanExpectedSlotsHalfOpenAndAligned"},
	{"./internal/history", "TestChainV1GoldenVectors"},
	{"./internal/history", "TestChainV1MillionRowsDoesNotRetainRows"},
	{"./internal/history", "TestRollupAccountSegmentsAddsBoundaryResets"},
	{"./internal/history", "TestRollupProviderSegmentsRecalculatesRatio"},
	{"./internal/history", "TestCompactionTransitionMatrix"},
	{"./internal/history", "TestCompactionStaleFenceAndActiveLeaseHaveZeroEffect"},
	{"./internal/history", "TestRollupFencingCompletionFailureAndImmutability"},
	{"./internal/history", "TestFinalRollupSegmentChecksumV1GoldenAndOrderIndependence"},
	{"./internal/history", "TestRetentionStageOrderAtInclusiveBoundaries"},
	{"./internal/history", "TestRetentionPollRequiresCompletedConservedCompaction"},
	{"./internal/history", "TestObserveChecksumV1MillionRowsReportsMaxRSSWithoutThreshold"},
	{"./internal/historyruntime", "TestRepositoryLoopsResumePhaseMatrix"},
	{"./internal/historyruntime", "TestRepositoryLoopsUnknownSummarizeCommitResumesWithoutReaggregation"},
	{"./internal/historyruntime", "TestRepositoryLoopsSummarizeFixedFailuresDoNotLeakOrAdvance"},
	{"./internal/historyruntime", "TestRepositoryLoopsUnknownCompleteCommitUsesBoundedIdempotentReadRetry"},
	{"./internal/historyruntime", "TestRepositoryLoopsFinalizesRollupWithBoundedUnknownCommitReplay"},
	{"./internal/historyruntime", "TestRepositoryLoopsRollupShutdownDrainsCurrentFinalizeOnly"},
	{"./internal/historyruntime", "TestRepositoryLoopsShareConcurrencyAcrossCompactionAndRollup"},
	{"./internal/historyruntime", "TestRepositoryLoopsShutdownStopsClaimsButFinishesShortOperation"},
	{"./internal/historyruntime", "TestRepositoryLoopsShutdownBetweenDeleteBatchesStartsNoNewTransaction"},
	{"./internal/historyruntime", "TestRepositoryLoopsRetentionRoundUsesDependencyOrderAndBoundedTransactions"},
	{"./internal/historyruntime", "TestRepositoryLoopsRetentionUnknownCommitWaitsForNextScanWithoutReplay"},
	{"./internal/historyruntime", "TestRepositoryLoopsRetentionShutdownDrainsCurrentTransactionOnly"},
	{"./internal/historyruntime", "TestRepositoryLoopsRetentionShutdownWhileQueuedStartsNoTransaction"},
	{"./internal/historyruntime", "TestRepositoryLoopsRetentionSharesTotalConcurrencyLimit"},
	{"./internal/historyruntime", "TestFatalIntegritySignalAllowsOnlyOriginatingTerminalFailAndBlocksOtherTransactions"},
	{"./internal/historyruntime", "TestRepositoryLoopsRollupFatalStopsCompactionAfterCurrentBatch"},
	{"./internal/historyruntime", "TestDisabledServiceChecksCompatibilityWithoutStartingLoops"},
	{"./internal/historyruntime", "TestCompatibilityFailureDisablesOnlyHistory"},
	{"./internal/historyruntime", "TestCompatibleServiceStopsClaimsBeforeBoundedOperations"},
	{"./internal/historyruntime", "TestFatalLoopStopsRuntimeAfterActiveTransactionDrain"},
	{"./internal/historyruntime", "TestUnexpectedLoopExitStopsRuntime"},
	{"./internal/historyruntime", "TestCollectorExportsOnlyClosedLowCardinalityLabels"},
	{"./internal/historyruntime", "TestCollectorSchemaIncompatibleOmitsDatabaseDerivedFamilies"},
	{"./internal/historyruntime", "TestCollectorFailsClosedOnUnknownLabelsOrRawProviderError"},
	{"./internal/historyruntime", "TestCollectorProviderFailureDoesNotPoisonProcessMetrics"},
	{"./internal/historyruntime", "TestCollectorFailsClosedOnNonFiniteMetrics"},
	{"./internal/historyruntime", "TestRuntimeStatusStateRejectsInvalidTransitions"},
	{"./internal/store", "TestAccountInventoryHistoryMetricsSnapshotStatusGateAndMapping"},
	{"./internal/store", "TestAccountInventoryHistoryRetentionMetricsObserveAdaptersWithoutGuessingFailedRows"},
	{"./internal/store", "TestAccountInventoryHistoryRetentionMetricsAreRaceSafe"},
	{"./internal/store", "TestAccountInventoryHistoryMetricsSnapshotRejectsUnsafeJSON"},
	{"./internal/store", "TestAccountInventoryHistoryMetricsSnapshotErrorsAreRedacted"},
	{"./cmd/control", "TestAccountInventoryHistoryRuntimeDisabledStillChecksCompatibilityWithoutLoops"},
	{"./cmd/control", "TestAccountInventoryHistoryRuntimeEnabledStartsAllLoopsAndShutsDown"},
	{"./cmd/control", "TestAccountInventoryHistoryRuntimeIncompatibleDisablesOnlyHistory"},
	{"./cmd/control", "TestAccountInventoryHistoryRuntimeFatalStopsOnlyHistoryAndLogsFixedReason"},
	{"./cmd/control", "TestAccountInventoryHistoryRuntimeShutdownTimeoutLogIsFixed"},
}

var requiredHistoryProcessTests = []string{
	"TestAccountInventoryHistoryProcessDisabledCompatibleMetrics",
	"TestAccountInventoryHistoryProcessMetricsFailureIsolation",
	"TestAccountInventoryHistoryProcessSeedEligibleSource",
	"TestAccountInventoryHistoryProcessEnabledConvergesEligibleSource",
	"TestAccountInventoryHistoryProcessSeedClaimedForRestart",
}

const (
	postgresImage = "postgres:18.6-alpine@sha256:d3e1620b530c944afa6e887d22eb899824da68e19c52024bf98f5220c88a65b2"
	proxyPrefix   = "env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy "
)

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("history shell test path unavailable")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", ".."))
}

func acceptanceRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join(repositoryRoot(t), "deploy", "acceptance")
}

func readAcceptanceFile(t *testing.T, name string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(acceptanceRoot(t), name))
	if err != nil {
		t.Fatal("history acceptance file unavailable")
	}
	return string(contents)
}

func TestHistoryAcceptanceBundleContract(t *testing.T) {
	scripts := []string{
		"account-inventory-history-run.sh",
		"account-inventory-history-postgres.sh",
		"account-inventory-history-process.sh",
		"account-inventory-history-rollback.sh",
		"account-inventory-history-safety-run.sh",
	}
	temporaryPrefixes := map[string]string{
		"account-inventory-history-run.sh":        "relay-control-history-run.",
		"account-inventory-history-postgres.sh":   "relay-control-history-postgres.",
		"account-inventory-history-process.sh":    "relay-control-history-process.",
		"account-inventory-history-rollback.sh":   "relay-control-history-rollback.",
		"account-inventory-history-safety-run.sh": "relay-control-history-safety.",
	}
	for _, name := range scripts {
		path := filepath.Join(acceptanceRoot(t), name)
		contents := readAcceptanceFile(t, name)
		for _, required := range []string{"set -euo pipefail", "umask 077", "unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy"} {
			if !strings.Contains(contents, required) {
				t.Errorf("%s lacks %q", name, required)
			}
		}
		for _, forbidden := range []string{"set -x", "printenv", "export -p", "eval ", "docker logs", "git checkout", "git worktree"} {
			if strings.Contains(contents, forbidden) {
				t.Errorf("%s contains forbidden %q", name, forbidden)
			}
		}
		prefix := temporaryPrefixes[name]
		for _, required := range []string{
			`temporary_root="${TMPDIR:-/tmp}"`,
			`temporary_root="${temporary_root%/}"`,
			`mktemp -d "$temporary_root/` + prefix,
			`"$temporary_root"/` + prefix,
		} {
			if !strings.Contains(contents, required) {
				t.Errorf("%s lacks temporary-root contract %q", name, required)
			}
		}
		for _, forbidden := range []string{"/tmp/relay-", "/private/tmp/relay-"} {
			if strings.Contains(contents, forbidden) {
				t.Errorf("%s hard-codes host temporary path %q", name, forbidden)
			}
		}
		if output, err := exec.Command("bash", "-n", path).CombinedOutput(); err != nil {
			t.Errorf("bash -n %s failed: %v: %s", name, err, output)
		}
	}

	runner := readAcceptanceFile(t, scripts[0])
	for _, required := range []string{
		`mode="${1:-static}"`, "static)", "postgres)", "process)", "rollback)", "all)",
		"account-inventory-history-postgres.sh", "account-inventory-history-process.sh", "-list \"^${exact_name}$\"",
		"account-inventory-history-rollback.sh", "rollback_gate=covered",
		"account-inventory-history-safety-run.sh", "sensitive_canary=partial_local_sinks", "external_requests=not_covered",
		"go test -race ./internal/history ./internal/historyruntime ./internal/store ./cmd/control",
		"exact_discovery=covered race=covered million_rows=covered", "process=covered",
	} {
		if !strings.Contains(runner, required) {
			t.Errorf("history runner lacks %q", required)
		}
	}
	assertExactHistoryDiscoveryAndRace(t, runner)
	for _, testName := range requiredHistoryProcessTests {
		discovery := "require_test ./deploy/acceptance/account-inventory-history-process " + testName
		if strings.Count(runner, discovery) != 1 {
			t.Errorf("history runner exact process discovery count for %s = %d", testName, strings.Count(runner, discovery))
		}
	}

	postgres := readAcceptanceFile(t, scripts[1])
	for _, required := range []string{
		"docker compose --project-name", "down --volumes --remove-orphans",
		"docker ps --all", "docker volume ls", "docker network ls",
		"label=com.docker.compose.project=", "GOOSE_DBSTRING=", "go tool goose \"$direction\"",
		"assert_version 9", "assert_version 8", "migrate down", "migrate up",
		"TestAccountInventoryHistoryPostgresSchemaSmoke", "core_sha256=covered",
		"TestAccountInventoryHistoryPostgresPlannerCatalogGate",
		"TestAccountInventoryHistoryPostgresRollupPublicationMatrix",
		"planner_catalog_utc_inclusive_72h=covered",
		"planner_eligibility_matrix=covered",
		"finalize_catalog_9500=covered",
		"utc_dst_72h_expression=covered",
		"slot_provider_matrix=covered",
		"incomplete_segment_gates=covered",
		"coverage_expression_9499_finalize_9474_9500_10000=covered",
		"last_segment_concurrency=covered",
		"TestAccountInventoryHistorySummarizeWriteFailuresAreAtomic",
		"TestAccountInventoryHistoryCompactionClaimRenewReclaimFencing",
		"TestAccountInventoryHistoryExpiredLeaseRequiresReconcileAcrossCompactionPhases",
		"compaction_claim_fencing=covered",
		"compaction_reconcile_phases=covered",
		"summarize_atomicity=covered",
		"summarize_timeout_disconnect_recovery=covered",
		"summarize_immutability=covered",
		"snapshot_delete_atomicity=covered",
		"TestAccountInventoryHistorySnapshotDeleteSelectionBoundariesAndNoLateInsert",
		"snapshot_delete_selection=covered",
		"TestAccountInventoryHistoryResumeDeleteNeverReaggregatesResidualSource",
		"snapshot_delete_resume=covered",
		"TestAccountInventoryHistoryCompletionCountMismatchFailsClosedAndRetainsPolls",
		"compaction_complete_count_gate=covered",
		"finalize_atomicity=covered",
		"final_immutability=covered",
		"metrics_completed_only=covered",
		"TestAccountInventoryDailyRollupNoProviderAtomicFinalize",
		"TestAccountInventoryDailyRollupPolicyBoundaryResetAndCoverage",
		"TestHistoryMetricsBacklogIncludesUnplannedEligibleSnapshotsAndDrains",
		"TestHistoryMetricsOldestIncludesEligibleSourceWithoutPlannedRun",
		"TestAccountInventoryHistoryRetentionBatchesConservationAndCurrentQuery",
		"TestAccountInventoryHistoryPollRetentionChildFailuresRollbackAndResume",
		"retention_child_atomicity=covered",
		"TestAccountInventoryHistoryPollRetentionRejectsIneligibleCandidates",
		"poll_candidate_rejections=covered",
		"TestAccountInventoryHistoryRetentionEligibilityBoundaries",
		"retention_eligibility_boundaries=covered",
		"TestAccountInventoryHistoryPlannerSerializesRetentionBoundary",
		"TestAccountInventoryHistoryPlannerLimitOneMakesPersistentProgress",
		"TestAccountInventoryHistoryRetiredDaySerializesLatePollInsertion",
		"TestAccountInventoryHistoryMigrationBackfillsLegacyPollThenRetiresWithoutResurrection",
		"TestAccountInventoryHistoryZeroPollLineageCompletesAcrossRetentionCutoff",
		"TestAccountInventoryHistoryCapacityOneTenFifty",
		"capacity_1_10_50_total_accounts=1000",
		"retention_planner_lock=covered",
		"planner_limit_progress=covered",
		"retired_day_poll_lock=covered",
		"legacy_retention_bootstrap=covered",
		"zero_poll_lineage_bootstrap=covered",
		"retired_day_no_resurrection=covered",
		"metrics_backlog_drain=covered",
	} {
		if !strings.Contains(postgres, required) {
			t.Errorf("history PostgreSQL runner lacks %q", required)
		}
	}
	if strings.Count(postgres, "run_rollup_matrix") != 2 {
		t.Error("history PostgreSQL runner must define and invoke the rollup matrix exactly once")
	}
	if strings.Count(postgres, "run_planner_catalog_gate") != 2 {
		t.Error("history PostgreSQL runner must define and invoke the planner catalog gate exactly once")
	}
	if strings.Contains(postgres, "go tool goose -dir") {
		t.Fatal("history PostgreSQL runner exposes the database URL via goose argv pattern")
	}
	if strings.Count(postgres, "label=com.docker.compose.project=") < 3 {
		t.Fatal("history PostgreSQL runner does not verify container, volume, and network cleanup")
	}

	compose := readAcceptanceFile(t, "account-inventory-history-postgres.compose.yaml")
	if !strings.Contains(compose, "image: "+postgresImage) || strings.Contains(compose, "container_name:") ||
		regexp.MustCompile(`127\.0\.0\.1:[1-9][0-9]*:`).MatchString(compose) ||
		!strings.Contains(compose, `127.0.0.1::5432`) || !strings.Contains(compose, "internal: true") {
		t.Fatal("history PostgreSQL compose isolation or image pin is invalid")
	}
	if !strings.Contains(postgres, "compose port postgres 5432") {
		t.Fatal("history PostgreSQL runner does not discover a Docker-assigned loopback port")
	}

	assertHistoryProcessContract(t)
	assertHistoryRollbackContract(t)
	assertHistorySafetyContract(t)
}

func assertHistoryRollbackContract(t *testing.T) {
	t.Helper()
	rollback := readAcceptanceFile(t, "account-inventory-history-rollback.sh")
	harness := readAcceptanceFile(t, "account-inventory-history-rollback/main.go")
	fakeNode := readAcceptanceFile(t, "account-inventory-history-fake-node/main.go")
	for _, required := range []string{
		"d4310023b3128199e485670d4bde84da7607412f",
		"git archive --format=tar", `-o "$runtime_directory/control-old" ./cmd/control`,
		`CGO_ENABLED=0 GOOS=linux GOCACHE="$runtime_directory/go-build" go build -trimpath \
      -o "$runtime_directory/history-fake-node" ./deploy/acceptance/account-inventory-history-fake-node`,
		"CONTROL_ACCOUNT_INVENTORY_LIFECYCLE_ENABLED=true",
		"CONTROL_ACCOUNT_INVENTORY_POLL_ENABLED=true", "CONTROL_CLIPROXYAPI_DRIVER_ENABLED=true",
		"CONTROL_COOKIE_SECURE=true",
		`history-harness" wait`, `history-harness" http-query`, `history-harness" verify`,
		`session_value="$(sed -n`, `csrf_value="$(sed -n`,
		`"$management_key" "$fake_token" "$session_value" "$csrf_value"`, `"$runtime_directory"/*.log`,
		"account_inventory_history_fake_node=stopped total=1 health=0 inventory=1 unauthorized=0 rejected=0",
		"lock_acquired=false", `[ "$lock_acquired" = true ]`,
		`lock_directory="$temporary_root/relay-control-history-rollback-${control_port}.lock"`,
		"snapshot_cleanup=controlled", "poll_cleanup=controlled", "current_fk_null=covered",
		"old_control_poll_promotion=covered", "old_control_http_current_query=covered",
		"history_and_history_audit_unchanged=covered", "production_down=not_used",
		`if [ -n "$old_control_name" ]`, `if [ -n "$fake_node_name" ]`,
		`docker_no_proxy rm --force`, `docker_no_proxy container inspect`,
		"down --volumes --remove-orphans", "docker ps --all", "docker volume ls", "docker network ls",
		"cleanup_runtime_residual", `rmdir "$lock_directory"`,
		"cleanup_containers=0", "cleanup_volumes=0", "cleanup_networks=0",
		"cleanup_temp=0", "cleanup_lock=0",
	} {
		if !strings.Contains(rollback, required) {
			t.Errorf("history rollback runner lacks %q", required)
		}
	}
	for _, required := range []string{
		`case "wait":`, `case "http-query":`, `case "verify":`,
		`controlauth.SessionCookieName`, `request.Header.Set("X-CSRF-Token", session.CSRF)`,
		"promotion_applied", "current_poll_run_id=$2",
		"category='account_inventory' AND action='account_inventory.view' AND result='success'",
	} {
		if !strings.Contains(harness, required) {
			t.Errorf("history rollback harness lacks %q", required)
		}
	}
	for _, required := range []string{
		`case "/v0/management/auth-files":`, `bump(&node.counts.inventory)`,
		"total=%d health=%d inventory=%d unauthorized=%d rejected=%d",
	} {
		if !strings.Contains(fakeNode, required) {
			t.Errorf("history rollback fake node lacks %q", required)
		}
	}
	for _, forbidden := range []string{
		"history-forward-probe", "history-old-probe", "old-probe",
		"POLL_ENABLED=false", "POLL_ENABLED='false'", `POLL_ENABLED="false"`,
	} {
		if strings.Contains(rollback+"\n"+harness, forbidden) {
			t.Errorf("history rollback gate contains forbidden %q", forbidden)
		}
	}
	if strings.Contains(rollback, "git checkout") || strings.Contains(rollback, "git worktree") ||
		strings.Count(rollback, "label=com.docker.compose.project=") < 3 {
		t.Fatal("history rollback export or cleanup contract is invalid")
	}
}

func assertHistorySafetyContract(t *testing.T) {
	t.Helper()
	safety := readAcceptanceFile(t, "account-inventory-history-safety-run.sh")
	for _, required := range []string{
		"CONTROL_HISTORY_CANARY_SCAN_DIR", "CONTROL_HISTORY_CANARY_ENDPOINT",
		"CONTROL_HISTORY_CANARY_SECRET_REFERENCE", "CONTROL_HISTORY_CANARY_SECRET_VALUE",
		"CONTROL_HISTORY_CANARY_EMAIL", "CONTROL_HISTORY_CANARY_ACCOUNT_KEY",
		"CONTROL_HISTORY_CANARY_RUN_ID", "CONTROL_HISTORY_CANARY_FENCING_TOKEN",
		"CONTROL_HISTORY_CANARY_CHECKSUM", "CONTROL_HISTORY_CANARY_RAW_ERROR",
		"CONTROL_HISTORY_CANARY_SQL_PARAMETER", "CONTROL_HISTORY_CANARY_POLL_ID",
		"CONTROL_HISTORY_CANARY_POLICY_ID", "TestHistoryLocalOutputsExcludeCanaries",
		"TestHistoryProductionSourcesHaveNoDirectNetworkImports",
		"account-inventory-lifecycle-canary-scan", "local_sink_canary_found",
		"local_sink_canary=covered", "direct_network_client_imports=0",
		"sensitive_canary_complete=not_covered", "external_requests=not_covered",
		"process_fake_endpoint_counter=not_covered", "database_non_identity_sink=not_covered",
		"cleanup_temp=0",
	} {
		if !strings.Contains(safety, required) {
			t.Errorf("history safety runner lacks %q", required)
		}
	}
	for _, testName := range []string{
		"TestHistoryLocalOutputsExcludeCanaries", "TestHistoryProductionSourcesHaveNoDirectNetworkImports",
	} {
		if strings.Count(safety, "require_test "+testName) != 1 {
			t.Errorf("history safety exact discovery count for %s is invalid", testName)
		}
	}
}

func assertHistoryProcessContract(t *testing.T) {
	t.Helper()
	process := readAcceptanceFile(t, "account-inventory-history-process.sh")
	for _, required := range []string{
		`lock_directory="$temporary_root/relay-control-history-process-18084-55439.lock"`,
		`project_name="relay-control-history-process-${suffix}"`,
		"account-inventory-history-process.compose.yaml",
		"docker compose --project-name", "down --volumes --remove-orphans",
		"docker ps --all", "docker volume ls", "docker network ls",
		"label=com.docker.compose.project=", "./cmd/control",
		`-list "^${exact_name}$"`, `grep -Fxq "$exact_name"`,
		`CONTROL_HTTP_ADDR="127.0.0.1:${control_port}"`,
		`kill -TERM "$control_pid"`, `wait "$control_pid"`,
		"control_stop_timeout", "control_exit_failed", "control_still_available",
		"compose stop --timeout 1 postgres", "compose start postgres",
		"pg_isready --username relay_control_migrator",
		"postgres_outage_not_observed", "control_exited_during_postgres_outage",
		"control_exited_during_postgres_restart", "postgres_restart_timeout",
		"stop_postgres_and_wait_for_lease", "restart_postgres",
		"cleanup_container_residual", "cleanup_volume_residual", "cleanup_network_residual",
		"cleanup_runtime_residual", `rmdir "$lock_directory"`, "cleanup_lock_failed",
		"control_port_lock_unavailable", "lock_acquired=false",
		"account_inventory_history_process=success",
		"default_disabled=covered", "metrics_http=covered",
		"metrics_failure_isolation=covered", "enabled_zero_source=covered",
		"wait_for_history_ready", "assert_restart_claim_active",
		"postgres_restart_recovery=covered",
		"CONTROL_HISTORY_PROCESS_RESTART_PHASE_MATRIX", "restart_phase_matrix",
		"control_postgres_restart_phase_matrix=covered",
		"sigterm_exit=bounded", "log_redaction=covered",
		"cleanup_containers=0", "cleanup_volumes=0", "cleanup_networks=0",
		"cleanup_temp=0", "cleanup_lock=0",
	} {
		if !strings.Contains(process, required) {
			t.Errorf("history process runner lacks %q", required)
		}
	}
	for _, testName := range requiredHistoryProcessTests {
		discovery := "require_process_test " + testName
		if strings.Count(process, discovery) != 1 {
			t.Errorf("history process runner exact discovery count for %s = %d", testName, strings.Count(process, discovery))
		}
		exact := "-run '^" + testName + "$'"
		if strings.Count(process, exact) != 1 {
			t.Errorf("history process runner exact test count for %s = %d", testName, strings.Count(process, exact))
		}
	}
	if strings.Count(process, "label=com.docker.compose.project=") < 3 {
		t.Fatal("history process runner does not verify container, volume, and network cleanup")
	}

	compose := readAcceptanceFile(t, "account-inventory-history-process.compose.yaml")
	if !strings.Contains(compose, "image: "+postgresImage) || strings.Contains(compose, "container_name:") ||
		strings.Count(compose, "services:") != 1 || strings.Count(compose, "  postgres:") != 1 ||
		!strings.Contains(compose, `127.0.0.1:${CONTROL_HISTORY_PROCESS_DB_PORT`) ||
		!strings.Contains(compose, `name: ${CONTROL_HISTORY_PROCESS_PROJECT:?set CONTROL_HISTORY_PROCESS_PROJECT}`) {
		t.Fatal("history process compose isolation, image pin, or unique project contract is invalid")
	}
}

func assertExactHistoryDiscoveryAndRace(t *testing.T, runner string) {
	t.Helper()
	for _, required := range requiredHistoryRaceTests {
		discovery := "require_test " + required.packageName + " " + required.testName
		if strings.Count(runner, discovery) != 1 {
			t.Errorf("history runner exact discovery count for %s = %d", required.testName, strings.Count(runner, discovery))
		}
	}
	if strings.Count(runner,
		"require_test ./deploy/acceptance/account-inventory-history-shell TestHistoryAcceptanceBundleContract") != 1 {
		t.Fatal("history runner does not exact-discover its shell contract")
	}

	match := regexp.MustCompile(`-run '\^\(([^']+)\)\$'`).FindStringSubmatch(runner)
	if len(match) != 2 {
		t.Fatal("history runner race filter is not a single anchored exact-name alternation")
	}
	discovered := make(map[string]int)
	for _, name := range strings.Split(match[1], "|") {
		discovered[name]++
	}
	if len(discovered) != len(requiredHistoryRaceTests) {
		t.Fatalf("history runner race filter contains %d tests, want %d", len(discovered), len(requiredHistoryRaceTests))
	}
	for _, required := range requiredHistoryRaceTests {
		if discovered[required.testName] != 1 {
			t.Errorf("history runner race coverage for %s = %d", required.testName, discovered[required.testName])
		}
	}
}

func TestHistoryAcceptanceCommandsClearAllProxySpellings(t *testing.T) {
	commandPattern := regexp.MustCompile(`(^|[;&|(!] *)((go|npm|npx|make|docker) +[^#\n]+)`)
	for _, name := range []string{
		"account-inventory-history-run.sh",
		"account-inventory-history-postgres.sh",
		"account-inventory-history-process.sh",
	} {
		rawContents := readAcceptanceFile(t, name)
		if strings.Count(rawContents, "unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy") != 1 ||
			!strings.Contains(rawContents, "export NO_PROXY='*' no_proxy='*'") ||
			strings.Contains(rawContents, "HTTP_PROXY=") || strings.Contains(rawContents, "HTTPS_PROXY=") ||
			strings.Contains(rawContents, "ALL_PROXY=") {
			t.Errorf("%s proxy environment contract is not closed", name)
		}
		contents := strings.ReplaceAll(rawContents, "\\\n", " ")
		for lineNumber, line := range strings.Split(contents, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.Contains(trimmed, "command -v ") {
				continue
			}
			for _, match := range commandPattern.FindAllStringSubmatch(trimmed, -1) {
				command := strings.TrimSpace(match[2])
				if prefixAt, commandAt := strings.Index(trimmed, proxyPrefix), strings.Index(trimmed, command); prefixAt < 0 || prefixAt > commandAt {
					t.Errorf("%s:%d command does not clear all proxy spellings", name, lineNumber+1)
				}
			}
		}
	}
}

func TestHistoryAcceptanceFailureOutputIsFixedAndRedacted(t *testing.T) {
	assertFixedScriptFailure(t, "account-inventory-history-run.sh",
		[]string{"invalid", "extra"}, "account_inventory_history_acceptance=failed reason=invalid_arguments")
	assertFixedScriptFailure(t, "account-inventory-history-run.sh",
		[]string{"invalid-mode"}, "account_inventory_history_acceptance=failed reason=invalid_mode")
	assertFixedScriptFailure(t, "account-inventory-history-postgres.sh",
		[]string{"unexpected"}, "account_inventory_history_postgres=failed reason=invalid_arguments")
	assertFixedScriptFailure(t, "account-inventory-history-process.sh",
		[]string{"unexpected"}, "account_inventory_history_process=failed reason=invalid_arguments")

	for _, name := range []string{
		"account-inventory-history-run.sh",
		"account-inventory-history-postgres.sh",
		"account-inventory-history-process.sh",
	} {
		contents := readAcceptanceFile(t, name)
		for _, forbidden := range []string{"cat \"$runtime_directory", "tee ", "docker logs", "error.Error"} {
			if strings.Contains(contents, forbidden) {
				t.Errorf("%s can expose retained/raw failure output via %q", name, forbidden)
			}
		}
		if name != "account-inventory-history-process.sh" && strings.Contains(contents, "DATABASE_URL=") {
			t.Errorf("%s can expose the database URL through an argument", name)
		}
		failurePrefix := "account_inventory_history_postgres"
		if name == "account-inventory-history-run.sh" {
			failurePrefix = "account_inventory_history_acceptance"
		} else if name == "account-inventory-history-process.sh" {
			failurePrefix = "account_inventory_history_process"
		}
		if !regexp.MustCompile(`(?m)^fixed_failure\(\) \{$`).MatchString(contents) ||
			!strings.Contains(contents, `echo "`+failurePrefix+`=failed reason=$1" >&2`) {
			t.Errorf("%s lacks the fixed failure formatter", name)
		}
	}
}

func assertFixedScriptFailure(t *testing.T, name string, arguments []string, expected string) {
	t.Helper()
	path := filepath.Join(acceptanceRoot(t), name)
	command := exec.Command("bash", append([]string{path}, arguments...)...)
	command.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HTTP_PROXY=http://proxy-secret-marker.invalid",
		"HTTPS_PROXY=http://proxy-secret-marker.invalid",
		"ALL_PROXY=http://proxy-secret-marker.invalid",
		"http_proxy=http://proxy-secret-marker.invalid",
		"https_proxy=http://proxy-secret-marker.invalid",
		"all_proxy=http://proxy-secret-marker.invalid",
	}
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("%s unexpectedly accepted invalid arguments", name)
	}
	if got := strings.TrimSpace(string(output)); got != expected || strings.Contains(got, "marker") {
		t.Fatalf("%s fixed failure output=%q want=%q (error class %s)", name, got, expected, fmt.Sprintf("%T", err))
	}
}

func TestHistoryAcceptanceHasNoDataPlaneOrExternalProbePath(t *testing.T) {
	runner := readAcceptanceFile(t, "account-inventory-history-run.sh")
	postgres := readAcceptanceFile(t, "account-inventory-history-postgres.sh")
	for name, contents := range map[string]string{"runner": runner, "postgres": postgres} {
		withoutToolProxy := strings.ReplaceAll(contents, "https://goproxy.cn,direct", "")
		for _, forbidden := range []string{
			"curl ", "wget ", "nc ", "ssh ", "scp ", "http://", "https://",
			"/v1/models", "CLIPROXY", "GATEWAY", "PROMETHEUS", "management endpoint",
		} {
			if strings.Contains(strings.ToUpper(withoutToolProxy), strings.ToUpper(forbidden)) {
				t.Errorf("%s contains an external/data-plane probe path %q", name, forbidden)
			}
		}
	}
	for _, required := range []string{
		"go test -race ./internal/history ./internal/historyruntime ./internal/store ./cmd/control",
		"go test ./deploy/acceptance/account-inventory-history-shell",
	} {
		if !strings.Contains(runner, required) {
			t.Errorf("static runner package boundary lacks %q", required)
		}
	}
	if strings.Contains(runner, "./internal/drivers") || strings.Contains(runner, "./internal/inventorypoll") {
		t.Fatal("static history runner reaches data-plane collection packages")
	}
	compose := readAcceptanceFile(t, "account-inventory-history-postgres.compose.yaml")
	if strings.Count(compose, "services:") != 1 || strings.Count(compose, "  postgres:") != 1 ||
		!strings.Contains(compose, "internal: true") {
		t.Fatal("history PostgreSQL acceptance is not isolated to one internal PostgreSQL service")
	}
}
