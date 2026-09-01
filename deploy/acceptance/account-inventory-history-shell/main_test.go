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
	{"./internal/historyruntime", "TestRepositoryLoopsUsesConfiguredConcurrency"},
	{"./internal/historyruntime", "TestRepositoryLoopsFatalMismatchStopsPlannerAndNewWorkerTransactions"},
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
	{"./internal/store", "TestAccountInventoryHistoryFailureReasonDictionariesAreExact"},
	{"./cmd/control", "TestAccountInventoryHistoryRuntimeDisabledStillChecksCompatibilityWithoutLoops"},
	{"./cmd/control", "TestAccountInventoryHistoryRuntimeEnabledStartsAllLoopsAndShutsDown"},
	{"./cmd/control", "TestAccountInventoryHistoryRuntimeIncompatibleDisablesOnlyHistory"},
	{"./cmd/control", "TestAccountInventoryHistoryRuntimeFatalStopsOnlyHistoryAndLogsFixedReason"},
	{"./cmd/control", "TestAccountInventoryHistoryRuntimeFailureLogsUseFixedReasons"},
}

var requiredHistoryProcessTests = []string{
	"TestAccountInventoryHistoryProcessDisabledCompatibleMetrics",
	"TestAccountInventoryHistoryProcessMetricsFailureIsolation",
	"TestAccountInventoryHistoryProcessSeedEligibleSource",
	"TestAccountInventoryHistoryProcessStaleFenceHasZeroImpact",
	"TestAccountInventoryHistoryProcessSourceBackedRetentionCompleted",
	"TestAccountInventoryHistoryProcessTerminalInternalPreservesSource",
	"TestAccountInventoryHistoryProcessEnabledConvergesEligibleSource",
	"TestAccountInventoryHistoryProcessSeedClaimedForRestart",
	"TestAccountInventoryHistoryProcessHeldTransactionPoolExhaustion",
	"TestAccountInventoryHistoryProcessClaimRetainedUntilLeaseExpiry",
	"TestAccountInventoryHistoryProcessReconcilerRecoveredClaim",
	"TestAccountInventoryHistoryProcessHeldTransactionTimeoutIsAtomic",
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
		"account-inventory-history-data-plane.sh",
		"account-inventory-history-safety-run.sh",
	}
	temporaryPrefixes := map[string]string{
		"account-inventory-history-run.sh":        "relay-control-history-run.",
		"account-inventory-history-postgres.sh":   "relay-control-history-postgres.",
		"account-inventory-history-process.sh":    "relay-control-history-process.",
		"account-inventory-history-rollback.sh":   "relay-control-history-rollback.",
		"account-inventory-history-data-plane.sh": "relay-control-history-data-plane.",
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
		`mode="${1:-static}"`, "static)", "postgres)", "process)", "rollback)", "data-plane)", "all)",
		"account-inventory-history-postgres.sh", "account-inventory-history-process.sh", "-list \"^${exact_name}$\"",
		"account-inventory-history-rollback.sh", "rollback_gate=covered",
		"account-inventory-history-data-plane.sh", "data_plane_isolation=covered",
		"baseline_models=1/1", "outage_models=100/100", "gateway_inference_e2e=not_covered",
		"account-inventory-history-safety-run.sh", "sensitive_canary=partial_local_sinks", "external_requests=not_covered",
		"go test -race ./internal/history ./internal/historyruntime ./internal/store ./cmd/control",
		"history_targeted_race=covered",
		"exact_discovery=covered race=covered", "million_rows=covered", "process=covered",
		"sensitive_canary=covered", "sensitive_canary_database_sinks=covered",
		"fake_network_counter=covered", "external_requests=0",
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
	if discovery := "require_test ./deploy/acceptance/account-inventory-history-data-plane TestAccountInventoryHistoryDataPlaneContract"; strings.Count(runner, discovery) != 1 {
		t.Fatal("history runner exact data-plane contract discovery is invalid")
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
		"TestAccountInventoryHistoryConcurrentRetentionQueryPromotionAndScope",
		"go test -race ./internal/store",
		"retention_query_promotion_scope_race=covered",
		"TestAccountInventoryHistoryPollRetentionChildFailuresRollbackAndResume",
		"retention_child_atomicity=covered",
		"TestAccountInventoryHistoryPollRetentionRejectsIneligibleCandidates",
		"poll_candidate_rejections=covered",
		"TestAccountInventoryHistoryRetentionEligibilityBoundaries",
		"retention_eligibility_boundaries=covered",
		"retention_ordered_chain_coverage_omission=covered",
		"retention_query_promotion_scope_concurrency=covered",
		"TestAccountInventoryHistoryPlannerSerializesRetentionBoundary",
		"TestAccountInventoryHistoryPlannerLimitOneMakesPersistentProgress",
		"TestAccountInventoryHistoryRetiredDaySerializesLatePollInsertion",
		"TestAccountInventoryHistoryMigrationBackfillsLegacyPollThenRetiresWithoutResurrection",
		"TestAccountInventoryHistoryMigrationBackfillsHealthWithoutHistoryOrIdentityCopy",
		"account_inventory_history_migration8_fingerprint=success",
		"old_columns=count_digest_covered", "provider_health=current_poll_result",
		"TestAccountInventoryLifecycleConcurrentFinalizeAndScopeTransition",
		"TestInventorySnapshotContractFailureFinalizesWithoutPromotion",
		"TestAccountInventoryHistoryZeroPollLineageCompletesAcrossRetentionCutoff",
		"TestAccountInventoryHistoryAuditExactAllowlistAndRetentionBoundary",
		"history_audit_allowlist_retention_atomicity=covered",
		"TestAccountInventoryHistorySensitiveCanaryDatabaseSinks",
		"sensitive_canary_database_sinks=covered",
		"TestAccountInventoryHistoryCrashRecoveryMatrix",
		"crash_recovery_matrix=covered",
		"TestAccountInventoryHistorySecurityBoundaryMatrix",
		"security_boundary_matrix=covered",
		"TestAccountInventoryHistoryAggregateSchemaConstraintAndProtectionGateMatrix",
		"aggregate_schema_constraints_protection_gates=covered",
		"TestAccountInventoryHistoryCapacityOneTenFifty",
		"TestAccountInventoryRowsFailClosed",
		"TestAccountInventoryHTTPRepositoryRetentionNullSourceAndInconsistentCurrentState",
		"capacity_1_10_50_total_accounts=1000",
		"retention_planner_lock=covered",
		"planner_limit_progress=covered",
		"retired_day_poll_lock=covered",
		"legacy_retention_bootstrap=covered",
		"zero_poll_lineage_bootstrap=covered",
		"retired_day_no_resurrection=covered",
		"current_fields_after_retention=covered",
		"current_query_after_retention=covered",
		"current_health_query_matrix=covered",
		"current_http_null_source=covered",
		"provider_health_finalize_matrix=covered",
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
	if strings.Count(postgres, "run_api_probe") != 2 {
		t.Error("history PostgreSQL runner must define and invoke the API probe exactly once")
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
		`-o "$runtime_directory/control-current" ./cmd/control`,
		`CGO_ENABLED=0 GOOS=linux go build -trimpath \
      -o "$runtime_directory/history-fake-node" ./deploy/acceptance/account-inventory-history-fake-node`,
		"CONTROL_ACCOUNT_INVENTORY_LIFECYCLE_ENABLED=true",
		"CONTROL_ACCOUNT_INVENTORY_POLL_ENABLED=true", "CONTROL_CLIPROXYAPI_DRIVER_ENABLED=true",
		"CONTROL_COOKIE_SECURE=true",
		`history-harness" verify-baseline`, `history-harness" wait`,
		`history-harness" http-query`, `history-harness" verify`,
		`session_value="$(sed -n`, `csrf_value="$(sed -n`,
		`"$management_key" "$fake_token" "$session_value" "$csrf_value"`, `"$runtime_directory"/*.log`,
		"account_inventory_history_fake_node=stopped total=1 health=0 inventory=1 unauthorized=0 rejected=0",
		"lock_acquired=false", `[ "$lock_acquired" = true ]`,
		`lock_directory="$temporary_root/relay-control-history-rollback-${control_port}.lock"`,
		"snapshot_cleanup=controlled", "poll_cleanup=controlled", "current_fk_null=covered",
		"current_candidate_disabled=covered", "compatibility_gate=covered",
		"runner_stopped_before_old_binary=covered",
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
		`case "verify-baseline":`, `case "wait":`, `case "http-query":`, `case "verify":`,
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
	candidateStart := strings.LastIndex(rollback, "\n  start_current_disabled_control\n")
	candidateStop := strings.LastIndex(rollback, "\n  stop_current_disabled_control\n")
	baselineCheck := strings.LastIndex(rollback, `history-harness" verify-baseline`)
	oldStart := strings.LastIndex(rollback, "\n  start_old_control ")
	if candidateStart < 0 || candidateStop <= candidateStart || baselineCheck <= candidateStop || oldStart <= baselineCheck {
		t.Fatal("history rollback candidate-to-old handoff order is invalid")
	}
	if regexp.MustCompile(`(?m)^[^#\n]*(?:goose[^\n]*[[:space:]'\"]down(?:[[:space:]'\"]|$)|migrate[[:space:]'\"]+down(?:[[:space:]'\"]|$)|migrate-down|migration-down)`).MatchString(rollback) {
		t.Fatal("history rollback runner must not invoke migration down")
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
		"local_sink_canary=covered scenarios=7", "direct_network_client_imports=0",
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
		`lock_directory="$temporary_root/relay-control-history-process-18084-18085-55439.lock"`,
		`project_name="relay-control-history-process-${suffix}"`,
		"account-inventory-history-process.compose.yaml",
		"docker compose --project-name", "down --volumes --remove-orphans",
		"docker ps --all", "docker volume ls", "docker network ls",
		"label=com.docker.compose.project=", "./cmd/control",
		"./deploy/acceptance/account-inventory-history-fake-node",
		`network_counter_endpoint='http://127.0.0.1:18085'`,
		`http_proxy="$network_counter_endpoint" https_proxy="$network_counter_endpoint"`,
		"account_inventory_history_fake_node=stopped total=0 health=0 inventory=0 unauthorized=0 rejected=0",
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
		"staging_concurrency_1=covered", "staging_delete_batch_1=covered",
		"staging_source_snapshots=2", "staging_current_query_equivalent=covered",
		"wait_for_history_ready", "assert_restart_claim_active",
		"postgres_restart_recovery=covered",
		"CONTROL_HISTORY_PROCESS_RESTART_PHASE_MATRIX", "restart_phase_matrix",
		"control_postgres_restart_phase_matrix=covered",
		"held_sql_sigterm_drain=covered", "held_sql_statement_timeout_atomicity=covered",
		"max_conns_1_pool_wait=covered", "unexpired_lease=preserved",
		"reconciler_restart_takeover=covered",
		"source_backed_snapshots=2", "history_concurrency=2", "dual_workers_observed=2",
		"stale_fence_zero_impact=covered", "source_backed_retention=covered",
		"source_backed_postgres_restart=covered", "source_backed_max_conns_1=covered",
		"source_backed_statement_timeout_recovery=covered",
		"permission_terminal_internal=covered", "trigger_terminal_internal=covered",
		"planner_runtime_stopped=covered", "rollup_runtime_stopped=covered",
		"retention_runtime_stopped=covered",
		"terminal_source_preserved=covered",
		"sigterm_exit=bounded", "log_redaction=covered",
		"fake_network_counter=covered", "external_requests=0", "node=0", "gateway=0",
		"prometheus=0", "internet=0", "model=0",
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
	if strings.Count(process, `http_proxy="$network_counter_endpoint"`) != 2 {
		t.Fatal("history process network counter is not scoped to both Control launch paths")
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
			"/v1/models", "/v1/chat/completions", "CLIPROXY", "GATEWAY_URL", "PROMETHEUS",
			"gateway_inference_e2e=covered", "gateway_inference_e2e=true", "management endpoint",
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
	if !strings.Contains(runner, "gateway_inference_e2e=not_covered") {
		t.Fatal("history runner does not preserve the bounded non-Gateway claim")
	}
	compose := readAcceptanceFile(t, "account-inventory-history-postgres.compose.yaml")
	if strings.Count(compose, "services:") != 1 || strings.Count(compose, "  postgres:") != 1 ||
		!strings.Contains(compose, "internal: true") {
		t.Fatal("history PostgreSQL acceptance is not isolated to one internal PostgreSQL service")
	}
}

func TestHistoryAcceptanceCleanupAndReportContract(t *testing.T) {
	cleanupKeys := "cleanup_containers cleanup_volumes cleanup_networks cleanup_temp cleanup_lock"
	scripts := []struct {
		name       string
		prefix     string
		markerKeys string
		usesDocker bool
		usesLock   bool
	}{
		{
			"account-inventory-history-run.sh", "account_inventory_history_acceptance",
			"account_inventory_history_acceptance mode exact_discovery race history_targeted_race million_rows sensitive_canary external_requests fake_network_counter data_plane_isolation baseline_models outage_models gateway_inference_e2e process rollback_gate local_sink_canary sensitive_canary_database_sinks " + cleanupKeys,
			false, false,
		},
		{
			"account-inventory-history-postgres.sh", "account_inventory_history_postgres",
			"account_inventory_history_postgres server_major migration up_down_up core_sha256 canonical_golden compaction_main_path crash_recovery_matrix security_boundary_matrix aggregate_schema_constraints_protection_gates compaction_claim_fencing compaction_reconcile_phases summarize_atomicity summarize_timeout_disconnect_recovery summarize_immutability snapshot_delete_atomicity snapshot_delete_selection snapshot_delete_resume compaction_complete_count_gate final_rollup finalize_atomicity final_immutability metrics_completed_only zero_provider planner_catalog_utc_inclusive_72h planner_eligibility_matrix finalize_catalog_9500 utc_dst_72h_expression slot_provider_matrix incomplete_segment_gates coverage_expression_9499_finalize_9474_9500_10000 last_segment_concurrency policy_boundary metrics_backlog_drain retention_batches retention_child_atomicity poll_candidate_rejections retention_eligibility_boundaries retention_ordered_chain_coverage_omission retention_query_promotion_scope_concurrency retention_query_promotion_scope_race retention_planner_lock planner_limit_progress retired_day_poll_lock legacy_retention_bootstrap zero_poll_lineage_bootstrap retired_day_no_resurrection current_fields_after_retention current_query_after_retention current_health_query_matrix current_http_null_source provider_health_finalize_matrix lease_expiry history_audit_allowlist_retention_atomicity sensitive_canary_database_sinks capacity_1_10_50_total_accounts audit_gate runtime_table_dml runtime_functions " + cleanupKeys,
			true, false,
		},
		{
			"account-inventory-history-process.sh", "account_inventory_history_process",
			"account_inventory_history_process migration default_disabled metrics_http metrics_failure_isolation enabled_zero_source staging_concurrency_1 staging_delete_batch_1 staging_source_snapshots staging_current_query_equivalent source_backed_snapshots history_concurrency dual_workers_observed stale_fence_zero_impact source_backed_retention source_backed_postgres_restart source_backed_max_conns_1 source_backed_statement_timeout_recovery held_sql_sigterm_drain held_sql_statement_timeout_atomicity max_conns_1_pool_wait unexpired_lease reconciler_restart_takeover postgres_restart_recovery control_postgres_restart_phase_matrix permission_terminal_internal trigger_terminal_internal planner_runtime_stopped rollup_runtime_stopped retention_runtime_stopped terminal_source_preserved sigterm_exit log_redaction fake_network_counter external_requests node gateway prometheus internet model " + cleanupKeys,
			true, true,
		},
		{
			"account-inventory-history-rollback.sh", "account_inventory_history_rollback",
			"account_inventory_history_rollback migration current_candidate_disabled compatibility_gate runner_stopped_before_old_binary pinned_old_revision snapshot_cleanup poll_cleanup current_fk_null old_control_poll_promotion old_control_http_current_query history_and_history_audit_unchanged fake_node_inventory_requests production_down " + cleanupKeys,
			true, true,
		},
		{
			"account-inventory-history-safety-run.sh", "account_inventory_history_safety",
			"account_inventory_history_safety local_sink_canary scenarios direct_network_client_imports sensitive_canary_complete external_requests process_fake_endpoint_counter database_non_identity_sink " + cleanupKeys,
			false, false,
		},
		{
			"account-inventory-history-data-plane.sh", "account_inventory_history_data_plane",
			"account_inventory_history_data_plane official_image baseline_models control_stopped postgres_stopped outage_models scope gateway_inference_e2e " + cleanupKeys,
			true, true,
		},
		{
			"account-inventory-history-capacity.sh", "account_inventory_history_capacity",
			"account_inventory_history_capacity scale evidence " + cleanupKeys,
			true, false,
		},
	}
	cleanupFields := "cleanup_containers=0 cleanup_volumes=0 cleanup_networks=0 cleanup_temp=0 cleanup_lock=0"
	for _, script := range scripts {
		contents := readAcceptanceFile(t, script.name)
		for _, required := range []string{
			"cleanup() {", "strict_cleanup() {", "trap cleanup EXIT",
			`rm -rf -- "$runtime_directory"`, `[ ! -e "$runtime_directory" ]`,
			`echo "` + script.prefix + `=failed reason=$1" >&2`,
		} {
			if !strings.Contains(contents, required) {
				t.Errorf("%s lacks cleanup contract %q", script.name, required)
			}
		}
		if script.usesDocker {
			for _, required := range []string{"down --volumes --remove-orphans", "docker volume ls", "docker network ls"} {
				if !strings.Contains(contents, required) {
					t.Errorf("%s lacks Docker cleanup contract %q", script.name, required)
				}
			}
		}
		if script.usesLock && !strings.Contains(contents, `rmdir "$lock_directory"`) {
			t.Errorf("%s lacks lock cleanup contract", script.name)
		}
		successLines := 0
		for _, line := range strings.Split(contents, "\n") {
			if !strings.Contains(line, script.prefix+"=success") {
				continue
			}
			successLines++
			if !strings.Contains(line, cleanupFields) {
				t.Errorf("%s success report lacks fixed zero-residual fields", script.name)
			}
			if err := validateHistoryMarkerKeys(line, script.prefix, script.markerKeys); err != nil {
				t.Errorf("%s success report: %v", script.name, err)
			}
		}
		if successLines == 0 {
			t.Errorf("%s lacks a fixed success report", script.name)
		}
	}

	capacitySource, err := os.ReadFile(filepath.Join(repositoryRoot(t), "internal", "store", "account_inventory_history_capacity_acceptance_integration_test.go"))
	if err != nil {
		t.Fatal("history capacity evidence source unavailable")
	}
	capacityEvidenceKeys := "history_capacity_evidence scale nodes snapshots_per_sample samples delete_batches summary_p50_ms summary_p95_ms summary_p99_ms rollup_p50_ms rollup_p95_ms rollup_p99_ms database_bytes database_growth_bytes index_bytes index_growth_bytes wal_bytes harness_max_rss_before_bytes harness_max_rss_after_bytes blocks_read blocks_hit temp_bytes max_lock_waiters deadlock_delta source_rows deleted_rows account_rollup_rows provider_rollup_rows conservation"
	evidenceLines := 0
	for _, line := range strings.Split(string(capacitySource), "\n") {
		if !strings.Contains(line, "history_capacity_evidence=") {
			continue
		}
		evidenceLines++
		if err := validateHistoryMarkerKeys(line, "history_capacity_evidence", capacityEvidenceKeys); err != nil {
			t.Fatalf("history capacity evidence: %v", err)
		}
	}
	if evidenceLines != 1 {
		t.Fatalf("history capacity evidence marker count=%d want=1", evidenceLines)
	}

	postgresSource := readAcceptanceFile(t, "account-inventory-history-postgres.sh")
	for _, line := range strings.Split(postgresSource, "\n") {
		if strings.Contains(line, "account_inventory_history_migration8_fingerprint=success") {
			if err := validateHistoryMarkerKeys(line, "account_inventory_history_migration8_fingerprint", "account_inventory_history_migration8_fingerprint old_columns provider_health"); err != nil {
				t.Fatalf("history migration fingerprint report: %v", err)
			}
		}
	}
	capacityRunner := readAcceptanceFile(t, "account-inventory-history-capacity.sh")
	for _, line := range strings.Split(capacityRunner, "\n") {
		if strings.Contains(line, "history_capacity_postgres_peak_bytes=") {
			if err := validateHistoryMarkerKeys(line, "history_capacity_postgres_peak_bytes", "history_capacity_postgres_peak_bytes"); err != nil {
				t.Fatalf("history capacity PostgreSQL peak report: %v", err)
			}
		}
	}
}

func TestHistoryAcceptanceMarkerAllowlistRejectsIdentityFields(t *testing.T) {
	line := "account_inventory_history_capacity=success scale=smoke evidence=not_evidence cleanup_containers=0 cleanup_volumes=0 cleanup_networks=0 cleanup_temp=0 cleanup_lock=0"
	allowed := "account_inventory_history_capacity scale evidence cleanup_containers cleanup_volumes cleanup_networks cleanup_temp cleanup_lock"
	for _, key := range []string{"instance_id", "fencing_token", "checksum", "endpoint", "raw_error", "api_key"} {
		t.Run(key, func(t *testing.T) {
			if err := validateHistoryMarkerKeys(line+" "+key+"=canary", "account_inventory_history_capacity", allowed); err == nil {
				t.Fatalf("marker allowlist accepted %s", key)
			}
		})
	}
}

func validateHistoryMarkerKeys(line, prefix, allowedKeys string) error {
	start := strings.Index(line, prefix+"=")
	if start < 0 {
		return fmt.Errorf("marker %q is missing", prefix)
	}
	allowed := make(map[string]struct{}, len(strings.Fields(allowedKeys)))
	for _, key := range strings.Fields(allowedKeys) {
		allowed[key] = struct{}{}
	}
	seen := make(map[string]struct{}, len(allowed))
	for _, field := range strings.Fields(line[start:]) {
		field = strings.Trim(field, "'\",;()")
		key, _, ok := strings.Cut(field, "=")
		if !ok {
			return fmt.Errorf("malformed marker field %q", field)
		}
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("unknown marker key %q", key)
		}
		if _, ok := seen[key]; ok {
			return fmt.Errorf("duplicate marker key %q", key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func TestHistoryAcceptanceAllInvalidArgumentsAreFixedAndRedacted(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		arguments []string
		expected  string
	}{
		{"account-inventory-history-run.sh", []string{"invalid", "extra"}, "account_inventory_history_acceptance=failed reason=invalid_arguments"},
		{"account-inventory-history-postgres.sh", []string{"unexpected"}, "account_inventory_history_postgres=failed reason=invalid_arguments"},
		{"account-inventory-history-process.sh", []string{"unexpected"}, "account_inventory_history_process=failed reason=invalid_arguments"},
		{"account-inventory-history-rollback.sh", []string{"unexpected"}, "account_inventory_history_rollback=failed reason=invalid_arguments"},
		{"account-inventory-history-safety-run.sh", []string{"unexpected"}, "account_inventory_history_safety=failed reason=invalid_arguments"},
		{"account-inventory-history-data-plane.sh", []string{"unexpected"}, "account_inventory_history_data_plane=failed reason=invalid_arguments"},
		{"account-inventory-history-capacity.sh", []string{"unknown"}, "account_inventory_history_capacity=failed reason=invalid_scale"},
		{"account-inventory-history-capacity.sh", []string{"smoke", "unexpected"}, "account_inventory_history_capacity=failed reason=invalid_arguments"},
	} {
		assertFixedScriptFailure(t, testCase.name, testCase.arguments, testCase.expected)
	}
}

func TestHistoryAcceptancePostStartupFailureCleansResources(t *testing.T) {
	if os.Getenv("CONTROL_HISTORY_FAILURE_CLEANUP_ACCEPTANCE") != "1" {
		t.Skip("set CONTROL_HISTORY_FAILURE_CLEANUP_ACCEPTANCE=1 for the Docker failure-cleanup gate")
	}
	assertNoHistoryDataPlaneDockerResiduals(t)
	temporaryRoot := strings.TrimSuffix(os.TempDir(), string(os.PathSeparator))
	fakeBin := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(fakeBin, 0o700); err != nil {
		t.Fatal("fake command directory unavailable")
	}
	if err := os.WriteFile(filepath.Join(fakeBin, "go"), []byte("#!/bin/sh\nexit 97\n"), 0o700); err != nil {
		t.Fatal("fake go command unavailable")
	}

	environment := historyEnvironmentWithoutProxy()
	filtered := environment[:0]
	for _, value := range environment {
		if !strings.HasPrefix(value, "PATH=") {
			filtered = append(filtered, value)
		}
	}
	environment = append(filtered,
		"PATH="+fakeBin+":"+os.Getenv("PATH"),
	)
	command := exec.Command("bash", filepath.Join(acceptanceRoot(t), "account-inventory-history-data-plane.sh"))
	command.Env = environment
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("history data-plane runner unexpectedly survived injected migration failure")
	}
	if got, want := strings.TrimSpace(string(output)), "account_inventory_history_data_plane=failed reason=migration_failed"; got != want {
		t.Fatalf("history post-start failure output=%q want=%q", got, want)
	}
	for _, pattern := range []string{
		filepath.Join(temporaryRoot, "relay-control-history-data-plane.*"),
		filepath.Join(temporaryRoot, "relay-control-history-data-plane-*.lock"),
	} {
		matches, globErr := filepath.Glob(pattern)
		if globErr != nil || len(matches) != 0 {
			t.Fatalf("history post-start failure left local residuals for %s", filepath.Base(pattern))
		}
	}
	assertNoHistoryDataPlaneDockerResiduals(t)
}

func assertNoHistoryDataPlaneDockerResiduals(t *testing.T) {
	t.Helper()
	for _, arguments := range [][]string{
		{"ps", "--all", "--quiet", "--filter", "name=relay-control-history-data-plane-"},
		{"volume", "ls", "--quiet", "--filter", "name=relay-control-history-data-plane-"},
		{"network", "ls", "--quiet", "--filter", "name=relay-control-history-data-plane-"},
	} {
		command := exec.Command("docker", arguments...)
		command.Env = historyEnvironmentWithoutProxy()
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("history Docker cleanup inspection failed for %s", arguments[0])
		}
		if strings.TrimSpace(string(output)) != "" {
			t.Fatalf("history Docker cleanup left %s residuals", arguments[0])
		}
	}
}

func historyEnvironmentWithoutProxy() []string {
	environment := make([]string, 0, len(os.Environ()))
	for _, value := range os.Environ() {
		name := strings.SplitN(value, "=", 2)[0]
		switch name {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy":
			continue
		}
		environment = append(environment, value)
	}
	return environment
}
