#!/usr/bin/env bash
set -euo pipefail
umask 077

unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
export NO_PROXY='*' no_proxy='*'
export GOPROXY='https://goproxy.cn,direct'

if [ "$#" -ne 0 ]; then
  echo 'account_inventory_history_postgres=failed reason=invalid_arguments' >&2
  exit 1
fi

script_directory="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
repository_root="$(CDPATH='' cd -- "$script_directory/../.." && pwd)"
compose_file="$script_directory/account-inventory-history-postgres.compose.yaml"
temporary_root="${TMPDIR:-/tmp}"
temporary_root="${temporary_root%/}"
runtime_directory=''
project_name=''

fixed_failure() {
  echo "account_inventory_history_postgres=failed reason=$1" >&2
  exit 1
}

compose() {
  env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    docker compose --project-name "$project_name" --file "$compose_file" "$@"
}

cleanup() {
  local exit_code=$?
  trap - EXIT HUP INT TERM
  if [ -n "$project_name" ]; then
    compose down --volumes --remove-orphans >/dev/null 2>&1 || true
  fi
  case "$runtime_directory" in
    "$temporary_root"/relay-control-history-postgres.*)
      rm -rf -- "$runtime_directory"
      ;;
  esac
  return "$exit_code"
}

strict_cleanup() {
  local residual
  if ! compose down --volumes --remove-orphans >/dev/null 2>&1; then
    fixed_failure 'cleanup_compose_down_failed'
  fi
  residual="$(compose ps --all --quiet 2>/dev/null)" || fixed_failure 'cleanup_compose_inspection_failed'
  [ -z "$residual" ] || fixed_failure 'cleanup_container_residual'
  residual="$(env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    docker ps --all --quiet --filter "label=com.docker.compose.project=${project_name}" 2>/dev/null)" \
    || fixed_failure 'cleanup_container_inspection_failed'
  [ -z "$residual" ] || fixed_failure 'cleanup_project_container_residual'
  residual="$(env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    docker volume ls --quiet --filter "label=com.docker.compose.project=${project_name}" 2>/dev/null)" \
    || fixed_failure 'cleanup_volume_inspection_failed'
  [ -z "$residual" ] || fixed_failure 'cleanup_volume_residual'
  residual="$(env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    docker network ls --quiet --filter "label=com.docker.compose.project=${project_name}" 2>/dev/null)" \
    || fixed_failure 'cleanup_network_inspection_failed'
  [ -z "$residual" ] || fixed_failure 'cleanup_network_residual'
  case "$runtime_directory" in
    "$temporary_root"/relay-control-history-postgres.*)
      rm -rf -- "$runtime_directory"
      ;;
    *) fixed_failure 'cleanup_runtime_path_invalid' ;;
  esac
  [ ! -e "$runtime_directory" ] || fixed_failure 'cleanup_runtime_residual'
  trap - EXIT HUP INT TERM
}

require_test() {
  local exact_name="$1" listing="$runtime_directory/postgres.tests"
  if ! env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    GOCACHE="$runtime_directory/go-build" go test ./deploy/acceptance/account-inventory-history-postgres \
      -list "^${exact_name}$" >"$listing" 2>&1; then
    fixed_failure 'test_discovery_failed'
  fi
  grep -Fxq "$exact_name" "$listing" || fixed_failure 'required_test_unavailable'
}

migrate() {
  local direction="$1" log_name="$2"
  if ! (
    cd "$repository_root/tools"
    env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
      GOCACHE="$runtime_directory/go-build" GOOSE_DRIVER=postgres \
      GOOSE_DBSTRING="$CONTROL_HISTORY_MIGRATOR_TEST_URL" GOOSE_MIGRATION_DIR=../migrations \
      go tool goose "$direction"
  ) >"$runtime_directory/$log_name" 2>&1; then
    fixed_failure "migration_${direction}_failed"
  fi
}

assert_version() {
  local expected="$1" log_name="$2"
  if ! compose exec -T postgres psql --username relay_control_migrator --dbname relay_station_control \
    --tuples-only --no-align --command \
    'SELECT max(version_id) FROM goose_db_version WHERE is_applied' >"$runtime_directory/$log_name" 2>&1; then
    fixed_failure 'migration_version_check_failed'
  fi
  grep -Fxq "$expected" "$runtime_directory/$log_name" || fixed_failure 'migration_version_invalid'
}

run_probe() {
  local log_name="$1"
  if ! env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    GOCACHE="$runtime_directory/go-build" go test ./deploy/acceptance/account-inventory-history-postgres \
      -run '^TestAccountInventoryHistoryPostgresSchemaSmoke$' -count=1 >"$runtime_directory/$log_name" 2>&1; then
    fixed_failure 'postgres_schema_smoke_failed'
  fi
}

run_planner_catalog_gate() {
  if ! env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    GOCACHE="$runtime_directory/go-build" go test ./deploy/acceptance/account-inventory-history-postgres \
      -run '^TestAccountInventoryHistoryPostgresPlannerCatalogGate$' -count=1 \
      >"$runtime_directory/planner-catalog-gate.log" 2>&1; then
    fixed_failure 'postgres_planner_catalog_gate_failed'
  fi
}

run_rollup_matrix() {
  if ! env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    GOCACHE="$runtime_directory/go-build" go test ./deploy/acceptance/account-inventory-history-postgres \
      -run '^TestAccountInventoryHistoryPostgresRollupPublicationMatrix$' -count=1 \
      >"$runtime_directory/rollup-publication-matrix.log" 2>&1; then
    fixed_failure 'postgres_rollup_publication_matrix_failed'
  fi
}

require_store_test() {
  local exact_name="$1" listing="$runtime_directory/store.tests"
  if ! env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    GOCACHE="$runtime_directory/go-build" go test ./internal/store \
      -list "^${exact_name}$" >"$listing" 2>&1; then
    fixed_failure 'store_test_discovery_failed'
  fi
  grep -Fxq "$exact_name" "$listing" || fixed_failure 'required_store_test_unavailable'
}

run_store_probe() {
  if ! env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    GOCACHE="$runtime_directory/go-build" go test ./internal/store \
      -run '^(TestAccountInventoryHistoryCompactionMainPathAndRecovery|TestAccountInventoryHistorySnapshotDeleteSelectionBoundariesAndNoLateInsert|TestAccountInventoryHistoryResumeDeleteNeverReaggregatesResidualSource|TestAccountInventoryHistoryCompactionClaimRenewReclaimFencing|TestAccountInventoryHistorySummarizeWriteFailuresAreAtomic|TestAccountInventoryDailyRollupNoProviderAtomicFinalize|TestAccountInventoryDailyRollupPolicyBoundaryResetAndCoverage|TestHistoryMetricsBacklogIncludesUnplannedEligibleSnapshotsAndDrains|TestHistoryMetricsOldestIncludesEligibleSourceWithoutPlannedRun|TestAccountInventoryHistoryRetentionBatchesConservationAndCurrentQuery|TestAccountInventoryHistoryPlannerSerializesRetentionBoundary|TestAccountInventoryHistoryPlannerLimitOneMakesPersistentProgress|TestAccountInventoryHistoryRetiredDaySerializesLatePollInsertion|TestAccountInventoryHistoryMigrationBackfillsLegacyPollThenRetiresWithoutResurrection|TestAccountInventoryHistoryZeroPollLineageCompletesAcrossRetentionCutoff|TestAccountInventoryHistoryLeaseExpiryWhileWaitingForRunLock|TestAccountInventoryHistoryFailedShapesAndProviderDayBound|TestAccountInventoryHistoryCapacityOneTenFifty)$' \
      -count=1 >"$runtime_directory/store-main-path.log" 2>&1; then
    fixed_failure 'postgres_compaction_main_path_failed'
  fi
}

main() {
  local port published_port down_state
  trap cleanup EXIT
  trap 'exit 130' HUP INT TERM
  command -v docker >/dev/null 2>&1 || fixed_failure 'required_command_unavailable'
  command -v go >/dev/null 2>&1 || fixed_failure 'required_command_unavailable'

  runtime_directory="$(mktemp -d "$temporary_root/relay-control-history-postgres.XXXXXX")"
  project_name="relay-control-history-postgres-$(printf '%s' "${runtime_directory##*.}" | tr '[:upper:]' '[:lower:]')"
  export CONTROL_HISTORY_POSTGRES_PROJECT="$project_name"

  if ! compose up --detach --wait postgres >"$runtime_directory/compose-up.log" 2>&1; then
    fixed_failure 'postgres_start_failed'
  fi
  published_port="$(compose port postgres 5432 2>/dev/null)" \
    || fixed_failure 'postgres_port_discovery_failed'
  case "$published_port" in
    127.0.0.1:[0-9]*) port="${published_port##*:}" ;;
    *) fixed_failure 'postgres_port_invalid' ;;
  esac
  case "$port" in
    ''|*[!0-9]*) fixed_failure 'postgres_port_invalid' ;;
  esac
  if [ "$port" -lt 1024 ] || [ "$port" -gt 65535 ]; then
    fixed_failure 'postgres_port_invalid'
  fi
  export CONTROL_HISTORY_MIGRATOR_TEST_URL="postgres://relay_control_migrator:relay_control_migrator_dev_only@127.0.0.1:${port}/relay_station_control?sslmode=disable"
  export CONTROL_HISTORY_RUNTIME_TEST_URL="postgres://relay_control_app_dev:relay_control_runtime_dev_only@127.0.0.1:${port}/relay_station_control?sslmode=disable"
  export CONTROL_HISTORY_REGISTRAR_TEST_URL="postgres://relay_control_asset_registrar_dev:relay_control_asset_registrar_dev_only@127.0.0.1:${port}/relay_station_control?sslmode=disable"
  export CONTROL_DATABASE_TEST_URL="$CONTROL_HISTORY_MIGRATOR_TEST_URL"
  export CONTROL_RUNTIME_DATABASE_TEST_URL="$CONTROL_HISTORY_RUNTIME_TEST_URL"

  migrate up migration-up.log
  assert_version 9 migration-version-up.log
  if ! compose exec -T postgres psql --username relay_control_migrator --dbname relay_station_control \
    --tuples-only --no-align --command 'SHOW server_version_num' >"$runtime_directory/server-version.log" 2>&1; then
    fixed_failure 'postgres_version_check_failed'
  fi
  grep -Eq '^18[0-9]{4}$' "$runtime_directory/server-version.log" || fixed_failure 'postgres_major_invalid'

  require_test TestAccountInventoryHistoryPostgresSchemaSmoke
  require_test TestAccountInventoryHistoryPostgresPlannerCatalogGate
  require_test TestAccountInventoryHistoryPostgresRollupPublicationMatrix
  require_store_test TestAccountInventoryHistoryCompactionMainPathAndRecovery
  require_store_test TestAccountInventoryHistorySnapshotDeleteSelectionBoundariesAndNoLateInsert
  require_store_test TestAccountInventoryHistoryResumeDeleteNeverReaggregatesResidualSource
  require_store_test TestAccountInventoryHistoryCompactionClaimRenewReclaimFencing
  require_store_test TestAccountInventoryHistorySummarizeWriteFailuresAreAtomic
  require_store_test TestAccountInventoryDailyRollupNoProviderAtomicFinalize
  require_store_test TestAccountInventoryDailyRollupPolicyBoundaryResetAndCoverage
  require_store_test TestHistoryMetricsBacklogIncludesUnplannedEligibleSnapshotsAndDrains
  require_store_test TestHistoryMetricsOldestIncludesEligibleSourceWithoutPlannedRun
  require_store_test TestAccountInventoryHistoryRetentionBatchesConservationAndCurrentQuery
  require_store_test TestAccountInventoryHistoryPlannerSerializesRetentionBoundary
  require_store_test TestAccountInventoryHistoryPlannerLimitOneMakesPersistentProgress
  require_store_test TestAccountInventoryHistoryRetiredDaySerializesLatePollInsertion
  require_store_test TestAccountInventoryHistoryMigrationBackfillsLegacyPollThenRetiresWithoutResurrection
  require_store_test TestAccountInventoryHistoryZeroPollLineageCompletesAcrossRetentionCutoff
  require_store_test TestAccountInventoryHistoryLeaseExpiryWhileWaitingForRunLock
  require_store_test TestAccountInventoryHistoryFailedShapesAndProviderDayBound
  require_store_test TestAccountInventoryHistoryCapacityOneTenFifty
  run_probe schema-first-up.log
  run_planner_catalog_gate
  run_rollup_matrix
  run_store_probe

  migrate down migration-down.log
  assert_version 8 migration-version-down.log
  down_state="$(compose exec -T postgres psql --username relay_control_migrator --dbname relay_station_control \
    --tuples-only --no-align --command \
    "SELECT count(*) FROM unnest(ARRAY['account_inventory_compaction_runs','account_inventory_daily_rollup_runs','account_inventory_daily_summaries','account_inventory_daily_provider_summaries','account_inventory_daily_account_rollups','account_inventory_daily_provider_rollups','account_inventory_history_retired_days']) AS name WHERE to_regclass('public.' || name) IS NOT NULL" 2>/dev/null)" \
    || fixed_failure 'migration_down_schema_check_failed'
  [ "$down_state" = '0' ] || fixed_failure 'migration_down_schema_residual'

  migrate up migration-second-up.log
  assert_version 9 migration-version-second-up.log
  run_probe schema-second-up.log

  strict_cleanup
  echo 'account_inventory_history_postgres=success server_major=18 migration=9 up_down_up=covered core_sha256=covered canonical_golden=covered compaction_main_path=covered compaction_claim_fencing=covered summarize_atomicity=covered summarize_immutability=covered snapshot_delete_atomicity=covered snapshot_delete_selection=covered snapshot_delete_resume=covered final_rollup=covered finalize_atomicity=covered final_immutability=covered metrics_completed_only=covered zero_provider=covered planner_catalog_utc_inclusive_72h=covered planner_eligibility_matrix=covered finalize_catalog_9500=covered utc_dst_72h_expression=covered slot_provider_matrix=covered incomplete_segment_gates=covered coverage_expression_9499_finalize_9474_9500_10000=covered last_segment_concurrency=covered policy_boundary=covered metrics_backlog_drain=covered retention_batches=covered retention_planner_lock=covered planner_limit_progress=covered retired_day_poll_lock=covered legacy_retention_bootstrap=covered zero_poll_lineage_bootstrap=covered retired_day_no_resurrection=covered current_query_after_retention=covered lease_expiry=covered capacity_1_10_50_total_accounts=1000 audit_gate=covered runtime_table_dml=denied runtime_functions=allowlisted cleanup_containers=0 cleanup_volumes=0 cleanup_networks=0'
}

cd "$repository_root"
main
