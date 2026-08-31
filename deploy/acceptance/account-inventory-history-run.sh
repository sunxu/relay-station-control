#!/usr/bin/env bash
set -euo pipefail
umask 077

unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
export NO_PROXY='*' no_proxy='*'
export GOPROXY='https://goproxy.cn,direct'

if [ "$#" -gt 1 ]; then
  echo 'account_inventory_history_acceptance=failed reason=invalid_arguments' >&2
  exit 1
fi

script_directory="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
repository_root="$(CDPATH='' cd -- "$script_directory/../.." && pwd)"
mode="${1:-static}"
temporary_root="${TMPDIR:-/tmp}"
temporary_root="${temporary_root%/}"
runtime_directory=''

fixed_failure() {
  echo "account_inventory_history_acceptance=failed reason=$1" >&2
  exit 1
}

cleanup() {
  local exit_code=$?
  trap - EXIT HUP INT TERM
  case "$runtime_directory" in
    "$temporary_root"/relay-control-history-run.*)
      rm -rf -- "$runtime_directory"
      ;;
  esac
  return "$exit_code"
}

strict_cleanup() {
  case "$runtime_directory" in
    "$temporary_root"/relay-control-history-run.*)
      rm -rf -- "$runtime_directory"
      ;;
    *) fixed_failure 'cleanup_runtime_path_invalid' ;;
  esac
  [ ! -e "$runtime_directory" ] || fixed_failure 'cleanup_runtime_residual'
  trap - EXIT HUP INT TERM
}

require_test() {
  local package="$1" exact_name="$2" listing
  listing="$runtime_directory/$(printf '%s' "$exact_name" | tr -c '[:alnum:]' '_').tests"
  if ! env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    go test "$package" -list "^${exact_name}$" >"$listing" 2>&1; then
    fixed_failure 'test_discovery_failed'
  fi
  grep -Fxq "$exact_name" "$listing" || fixed_failure 'required_test_unavailable'
}

run_static() {
  command -v go >/dev/null 2>&1 || fixed_failure 'required_command_unavailable'
  require_test ./internal/history TestUTCDayEligibilityBoundary
  require_test ./internal/history TestPlanExpectedSlotsHalfOpenAndAligned
  require_test ./internal/history TestChainV1GoldenVectors
  require_test ./internal/history TestChainV1MillionRowsDoesNotRetainRows
  require_test ./internal/history TestRollupAccountSegmentsAddsBoundaryResets
  require_test ./internal/history TestRollupProviderSegmentsRecalculatesRatio
  require_test ./internal/history TestCompactionTransitionMatrix
  require_test ./internal/history TestCompactionStaleFenceAndActiveLeaseHaveZeroEffect
  require_test ./internal/history TestRollupFencingCompletionFailureAndImmutability
  require_test ./internal/history TestFinalRollupSegmentChecksumV1GoldenAndOrderIndependence
  require_test ./internal/history TestRetentionStageOrderAtInclusiveBoundaries
  require_test ./internal/history TestRetentionPollRequiresCompletedConservedCompaction
  require_test ./internal/history TestObserveChecksumV1MillionRowsReportsMaxRSSWithoutThreshold
  require_test ./internal/historyruntime TestRepositoryLoopsResumePhaseMatrix
  require_test ./internal/historyruntime TestRepositoryLoopsUnknownSummarizeCommitResumesWithoutReaggregation
  require_test ./internal/historyruntime TestRepositoryLoopsSummarizeFixedFailuresDoNotLeakOrAdvance
  require_test ./internal/historyruntime TestRepositoryLoopsUnknownCompleteCommitUsesBoundedIdempotentReadRetry
  require_test ./internal/historyruntime TestRepositoryLoopsFinalizesRollupWithBoundedUnknownCommitReplay
  require_test ./internal/historyruntime TestRepositoryLoopsRollupShutdownDrainsCurrentFinalizeOnly
  require_test ./internal/historyruntime TestRepositoryLoopsShareConcurrencyAcrossCompactionAndRollup
  require_test ./internal/historyruntime TestRepositoryLoopsShutdownStopsClaimsButFinishesShortOperation
  require_test ./internal/historyruntime TestRepositoryLoopsShutdownBetweenDeleteBatchesStartsNoNewTransaction
  require_test ./internal/historyruntime TestRepositoryLoopsRetentionRoundUsesDependencyOrderAndBoundedTransactions
  require_test ./internal/historyruntime TestRepositoryLoopsRetentionUnknownCommitWaitsForNextScanWithoutReplay
  require_test ./internal/historyruntime TestRepositoryLoopsRetentionShutdownDrainsCurrentTransactionOnly
  require_test ./internal/historyruntime TestRepositoryLoopsRetentionShutdownWhileQueuedStartsNoTransaction
  require_test ./internal/historyruntime TestRepositoryLoopsRetentionSharesTotalConcurrencyLimit
  require_test ./internal/historyruntime TestFatalIntegritySignalAllowsOnlyOriginatingTerminalFailAndBlocksOtherTransactions
  require_test ./internal/historyruntime TestRepositoryLoopsRollupFatalStopsCompactionAfterCurrentBatch
  require_test ./internal/historyruntime TestDisabledServiceChecksCompatibilityWithoutStartingLoops
  require_test ./internal/historyruntime TestCompatibilityFailureDisablesOnlyHistory
  require_test ./internal/historyruntime TestCompatibleServiceStopsClaimsBeforeBoundedOperations
  require_test ./internal/historyruntime TestFatalLoopStopsRuntimeAfterActiveTransactionDrain
  require_test ./internal/historyruntime TestUnexpectedLoopExitStopsRuntime
  require_test ./internal/historyruntime TestCollectorExportsOnlyClosedLowCardinalityLabels
  require_test ./internal/historyruntime TestCollectorSchemaIncompatibleOmitsDatabaseDerivedFamilies
  require_test ./internal/historyruntime TestCollectorFailsClosedOnUnknownLabelsOrRawProviderError
  require_test ./internal/historyruntime TestCollectorProviderFailureDoesNotPoisonProcessMetrics
  require_test ./internal/historyruntime TestCollectorFailsClosedOnNonFiniteMetrics
  require_test ./internal/historyruntime TestRuntimeStatusStateRejectsInvalidTransitions
  require_test ./internal/store TestAccountInventoryHistoryMetricsSnapshotStatusGateAndMapping
  require_test ./internal/store TestAccountInventoryHistoryRetentionMetricsObserveAdaptersWithoutGuessingFailedRows
  require_test ./internal/store TestAccountInventoryHistoryRetentionMetricsAreRaceSafe
  require_test ./internal/store TestAccountInventoryHistoryMetricsSnapshotRejectsUnsafeJSON
  require_test ./internal/store TestAccountInventoryHistoryMetricsSnapshotErrorsAreRedacted
  require_test ./internal/store TestAccountInventoryHistoryFailureReasonDictionariesAreExact
  require_test ./cmd/control TestAccountInventoryHistoryRuntimeDisabledStillChecksCompatibilityWithoutLoops
  require_test ./cmd/control TestAccountInventoryHistoryRuntimeEnabledStartsAllLoopsAndShutsDown
  require_test ./cmd/control TestAccountInventoryHistoryRuntimeIncompatibleDisablesOnlyHistory
  require_test ./cmd/control TestAccountInventoryHistoryRuntimeFatalStopsOnlyHistoryAndLogsFixedReason
  require_test ./cmd/control TestAccountInventoryHistoryRuntimeFailureLogsUseFixedReasons
  require_test ./deploy/acceptance/account-inventory-history-process TestAccountInventoryHistoryProcessDisabledCompatibleMetrics
  require_test ./deploy/acceptance/account-inventory-history-process TestAccountInventoryHistoryProcessMetricsFailureIsolation
  require_test ./deploy/acceptance/account-inventory-history-process TestAccountInventoryHistoryProcessSeedEligibleSource
  require_test ./deploy/acceptance/account-inventory-history-process TestAccountInventoryHistoryProcessEnabledConvergesEligibleSource
  require_test ./deploy/acceptance/account-inventory-history-process TestAccountInventoryHistoryProcessSeedClaimedForRestart
  require_test ./deploy/acceptance/account-inventory-history-process TestAccountInventoryHistoryProcessHeldTransactionPoolExhaustion
  require_test ./deploy/acceptance/account-inventory-history-process TestAccountInventoryHistoryProcessClaimRetainedUntilLeaseExpiry
  require_test ./deploy/acceptance/account-inventory-history-process TestAccountInventoryHistoryProcessReconcilerRecoveredClaim
  require_test ./deploy/acceptance/account-inventory-history-process TestAccountInventoryHistoryProcessHeldTransactionTimeoutIsAtomic
  require_test ./deploy/acceptance/account-inventory-history-shell TestHistoryAcceptanceBundleContract

  if ! env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    go test -race ./internal/history ./internal/historyruntime ./internal/store ./cmd/control \
      -run '^(TestUTCDayEligibilityBoundary|TestPlanExpectedSlotsHalfOpenAndAligned|TestChainV1GoldenVectors|TestChainV1MillionRowsDoesNotRetainRows|TestRollupAccountSegmentsAddsBoundaryResets|TestRollupProviderSegmentsRecalculatesRatio|TestCompactionTransitionMatrix|TestCompactionStaleFenceAndActiveLeaseHaveZeroEffect|TestRollupFencingCompletionFailureAndImmutability|TestFinalRollupSegmentChecksumV1GoldenAndOrderIndependence|TestRetentionStageOrderAtInclusiveBoundaries|TestRetentionPollRequiresCompletedConservedCompaction|TestObserveChecksumV1MillionRowsReportsMaxRSSWithoutThreshold|TestRepositoryLoopsResumePhaseMatrix|TestRepositoryLoopsUnknownSummarizeCommitResumesWithoutReaggregation|TestRepositoryLoopsSummarizeFixedFailuresDoNotLeakOrAdvance|TestRepositoryLoopsUnknownCompleteCommitUsesBoundedIdempotentReadRetry|TestRepositoryLoopsFinalizesRollupWithBoundedUnknownCommitReplay|TestRepositoryLoopsRollupShutdownDrainsCurrentFinalizeOnly|TestRepositoryLoopsShareConcurrencyAcrossCompactionAndRollup|TestRepositoryLoopsShutdownStopsClaimsButFinishesShortOperation|TestRepositoryLoopsShutdownBetweenDeleteBatchesStartsNoNewTransaction|TestRepositoryLoopsRetentionRoundUsesDependencyOrderAndBoundedTransactions|TestRepositoryLoopsRetentionUnknownCommitWaitsForNextScanWithoutReplay|TestRepositoryLoopsRetentionShutdownDrainsCurrentTransactionOnly|TestRepositoryLoopsRetentionShutdownWhileQueuedStartsNoTransaction|TestRepositoryLoopsRetentionSharesTotalConcurrencyLimit|TestFatalIntegritySignalAllowsOnlyOriginatingTerminalFailAndBlocksOtherTransactions|TestRepositoryLoopsRollupFatalStopsCompactionAfterCurrentBatch|TestDisabledServiceChecksCompatibilityWithoutStartingLoops|TestCompatibilityFailureDisablesOnlyHistory|TestCompatibleServiceStopsClaimsBeforeBoundedOperations|TestFatalLoopStopsRuntimeAfterActiveTransactionDrain|TestUnexpectedLoopExitStopsRuntime|TestCollectorExportsOnlyClosedLowCardinalityLabels|TestCollectorSchemaIncompatibleOmitsDatabaseDerivedFamilies|TestCollectorFailsClosedOnUnknownLabelsOrRawProviderError|TestCollectorProviderFailureDoesNotPoisonProcessMetrics|TestCollectorFailsClosedOnNonFiniteMetrics|TestRuntimeStatusStateRejectsInvalidTransitions|TestAccountInventoryHistoryMetricsSnapshotStatusGateAndMapping|TestAccountInventoryHistoryRetentionMetricsObserveAdaptersWithoutGuessingFailedRows|TestAccountInventoryHistoryRetentionMetricsAreRaceSafe|TestAccountInventoryHistoryMetricsSnapshotRejectsUnsafeJSON|TestAccountInventoryHistoryMetricsSnapshotErrorsAreRedacted|TestAccountInventoryHistoryFailureReasonDictionariesAreExact|TestAccountInventoryHistoryRuntimeDisabledStillChecksCompatibilityWithoutLoops|TestAccountInventoryHistoryRuntimeEnabledStartsAllLoopsAndShutsDown|TestAccountInventoryHistoryRuntimeIncompatibleDisablesOnlyHistory|TestAccountInventoryHistoryRuntimeFatalStopsOnlyHistoryAndLogsFixedReason|TestAccountInventoryHistoryRuntimeFailureLogsUseFixedReasons)$' \
      -count=1 >"$runtime_directory/history-core.log" 2>&1; then
    fixed_failure 'history_core_gate_failed'
  fi
  if ! env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    go test ./deploy/acceptance/account-inventory-history-shell \
      -count=1 >"$runtime_directory/shell-contract.log" 2>&1; then
    fixed_failure 'shell_contract_gate_failed'
  fi
  "$script_directory/account-inventory-history-safety-run.sh"
}

case "$mode" in
  static|postgres|process|rollback|all) ;;
  *)
    echo 'account_inventory_history_acceptance=failed reason=invalid_mode' >&2
    exit 1
    ;;
esac

cd "$repository_root"
runtime_directory="$(mktemp -d "$temporary_root/relay-control-history-run.XXXXXX")"
trap cleanup EXIT
trap 'exit 130' HUP INT TERM

case "$mode" in
  static)
    run_static
    strict_cleanup
    echo 'account_inventory_history_acceptance=success mode=static exact_discovery=covered race=covered million_rows=covered sensitive_canary=partial_local_sinks external_requests=not_covered'
    ;;
  postgres)
    "$script_directory/account-inventory-history-postgres.sh"
    strict_cleanup
    echo 'account_inventory_history_acceptance=success mode=postgres'
    ;;
  process)
    "$script_directory/account-inventory-history-process.sh"
    strict_cleanup
    echo 'account_inventory_history_acceptance=success mode=process fake_network_counter=covered external_requests=partial_process_paths'
    ;;
  rollback)
    "$script_directory/account-inventory-history-rollback.sh"
    strict_cleanup
    echo 'account_inventory_history_acceptance=success mode=rollback'
    ;;
  all)
    run_static
    "$script_directory/account-inventory-history-postgres.sh"
    "$script_directory/account-inventory-history-process.sh"
    "$script_directory/account-inventory-history-rollback.sh"
    strict_cleanup
    echo 'account_inventory_history_acceptance=success mode=all exact_discovery=covered race=covered million_rows=covered process=covered rollback_gate=covered sensitive_canary=covered local_sink_canary=covered sensitive_canary_database_sinks=covered fake_network_counter=covered external_requests=partial_process_paths'
    ;;
esac
