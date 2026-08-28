#!/usr/bin/env bash
set -euo pipefail
umask 077

unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
export NO_PROXY='*' no_proxy='*'
export GOPROXY='https://goproxy.cn,direct'

if [ "$#" -ne 0 ]; then
  echo 'account_inventory_readonly_query_postgres=failed reason=invalid_arguments' >&2
  exit 1
fi

script_directory="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
repository_root="$(CDPATH='' cd -- "$script_directory/../.." && pwd)"
compose_file="$script_directory/account-inventory-readonly-query-postgres.compose.yaml"
runtime_directory=''
project_name=''

fixed_failure() {
  echo "account_inventory_readonly_query_postgres=failed reason=$1" >&2
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
    /tmp/relay-control-readonly-query-postgres.*|/private/tmp/relay-control-readonly-query-postgres.*)
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
  if ! residual="$(compose ps --all --quiet 2>/dev/null)"; then
    fixed_failure 'cleanup_compose_inspection_failed'
  fi
  if [ -n "$residual" ]; then
    fixed_failure 'cleanup_container_residual'
  fi
  if ! residual="$(env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    docker ps --all --quiet --filter "label=com.docker.compose.project=${project_name}" 2>/dev/null)"; then
    fixed_failure 'cleanup_container_inspection_failed'
  fi
  if [ -n "$residual" ]; then
    fixed_failure 'cleanup_project_container_residual'
  fi
  if ! residual="$(env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    docker volume ls --quiet --filter "label=com.docker.compose.project=${project_name}" 2>/dev/null)"; then
    fixed_failure 'cleanup_volume_inspection_failed'
  fi
  if [ -n "$residual" ]; then
    fixed_failure 'cleanup_volume_residual'
  fi
  if ! residual="$(env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    docker network ls --quiet --filter "label=com.docker.compose.project=${project_name}" 2>/dev/null)"; then
    fixed_failure 'cleanup_network_inspection_failed'
  fi
  if [ -n "$residual" ]; then
    fixed_failure 'cleanup_network_residual'
  fi
  case "$runtime_directory" in
    /tmp/relay-control-readonly-query-postgres.*|/private/tmp/relay-control-readonly-query-postgres.*)
      rm -rf -- "$runtime_directory"
      ;;
    *) fixed_failure 'cleanup_runtime_path_invalid' ;;
  esac
  if [ -e "$runtime_directory" ]; then
    fixed_failure 'cleanup_runtime_residual'
  fi
  trap - EXIT HUP INT TERM
}

database_port() {
  local published port
  published="$(compose port postgres 5432 2>/dev/null)" || fixed_failure 'postgres_port_unavailable'
  port="${published##*:}"
  case "$port" in
    ''|*[!0-9]*) fixed_failure 'postgres_port_invalid' ;;
  esac
  printf '%s\n' "$port"
}

require_test() {
  local package="$1" exact_name="$2" listing="$runtime_directory/tests.list"
  if ! env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    go test "$package" -list "^${exact_name}$" >"$listing" 2>&1; then
    fixed_failure 'test_discovery_failed'
  fi
  grep -Fxq "$exact_name" "$listing" || fixed_failure 'required_test_unavailable'
}

main() {
  local port capacity_summary capacity_one capacity_ten capacity_fifty
  trap cleanup EXIT
  trap 'exit 130' HUP INT TERM
  command -v docker >/dev/null 2>&1 || fixed_failure 'required_command_unavailable'
  command -v go >/dev/null 2>&1 || fixed_failure 'required_command_unavailable'

  runtime_directory="$(mktemp -d /tmp/relay-control-readonly-query-postgres.XXXXXX)"
  project_name="relay-control-readonly-query-postgres-$(printf '%s' "${runtime_directory##*.}" | tr '[:upper:]' '[:lower:]')"
  export CONTROL_READONLY_QUERY_POSTGRES_PROJECT="$project_name"
  export CONTROL_READONLY_QUERY_RUNTIME_DIRECTORY="$runtime_directory"
  export CONTROL_READONLY_QUERY_HOST_UID="$(id -u)"
  export CONTROL_READONLY_QUERY_HOST_GID="$(id -g)"

  if ! compose up --detach --wait postgres >/dev/null 2>&1; then
    fixed_failure 'postgres_start_failed'
  fi
  port="$(database_port)"
  export CONTROL_DATABASE_TEST_URL="postgres://relay_control_migrator:relay_control_migrator_dev_only@127.0.0.1:${port}/relay_station_control?sslmode=disable"
  export CONTROL_RUNTIME_DATABASE_TEST_URL="postgres://relay_control_app_dev:relay_control_runtime_dev_only@127.0.0.1:${port}/relay_station_control?sslmode=disable"

  if ! (
    cd "$repository_root/tools"
    env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
      GOOSE_DRIVER=postgres GOOSE_DBSTRING="$CONTROL_DATABASE_TEST_URL" GOOSE_MIGRATION_DIR=../migrations \
      go tool goose up
  ) >"$runtime_directory/migration.log" 2>&1; then
    fixed_failure 'migration_failed'
  fi
  if ! compose exec -T postgres psql --username relay_control_migrator --dbname relay_station_control \
    --tuples-only --no-align --command 'SHOW server_version_num' >"$runtime_directory/server-version.log" 2>&1; then
    fixed_failure 'postgres_version_check_failed'
  fi
  grep -Eq '^18[0-9]{4}$' "$runtime_directory/server-version.log" || fixed_failure 'postgres_major_invalid'
  if ! compose exec -T postgres psql --username relay_control_migrator --dbname relay_station_control \
    --tuples-only --no-align --command \
    'SELECT max(version_id) FROM goose_db_version WHERE is_applied' >"$runtime_directory/migration-version.log" 2>&1; then
    fixed_failure 'migration_version_check_failed'
  fi
  grep -Fxq '8' "$runtime_directory/migration-version.log" || fixed_failure 'migration_version_invalid'

  require_test ./internal/store TestAccountInventoryReadonlyQueryStoreAndPermissionMatrix
  require_test ./internal/store TestAccountInventoryReadonlyQuerySeesOnlyCommittedPromotionsScopeAndRollback
  require_test ./internal/store TestAccountInventoryReadonlyQueryAuditCommitAndDisconnectSemantics
  require_test ./internal/store TestAccountInventoryReadonlyQueryDatabaseFaultsFailClosedAndRecover
  require_test ./internal/store TestAccountInventoryReadonlyQueryCapacityOneTenFifty
  require_test ./internal/api TestAccountInventoryHTTPAuthorizationPaginationAndErrorMapping

  if ! env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    go test ./internal/store \
      -run '^TestAccountInventoryReadonlyQuery(StoreAndPermissionMatrix|SeesOnlyCommittedPromotionsScopeAndRollback|AuditCommitAndDisconnectSemantics|DatabaseFaultsFailClosedAndRecover)$' \
      -count=1 >"$runtime_directory/store.log" 2>&1; then
    fixed_failure 'store_gate_failed'
  fi
  if ! env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    CONTROL_READONLY_QUERY_CAPACITY_ACCEPTANCE=1 go test ./internal/store \
      -run '^TestAccountInventoryReadonlyQueryCapacityOneTenFifty$' -count=1 -v >"$runtime_directory/capacity.log" 2>&1; then
    fixed_failure 'capacity_gate_failed'
  fi
  capacity_summary="$runtime_directory/capacity-summary.log"
  sed -n 's/^.*\(readonly_query_capacity=passed .*$\)/\1/p' \
    "$runtime_directory/capacity.log" >"$capacity_summary"
  if [ "$(wc -l <"$capacity_summary" | tr -d ' ')" -ne 3 ]; then
    fixed_failure 'capacity_summary_count_invalid'
  fi
  while IFS= read -r capacity_line; do
    if ! printf '%s\n' "$capacity_line" | grep -Eq \
      '^readonly_query_capacity=passed nodes=(1|10|50) accounts=1000 scenarios=7 samples=500 concurrent_admins=8 concurrent_queries=80 errors=0 p50_us=[0-9]+ p95_us=[0-9]+ p99_us=[0-9]+ max_plan_us=[0-9]+ max_buffers=[0-9]+ audit_rows=500 audit_adapter_overhead_p95_us=[0-9]+$'; then
      fixed_failure 'capacity_summary_schema_invalid'
    fi
  done <"$capacity_summary"
  capacity_one="$(grep -E '^readonly_query_capacity=passed nodes=1 ' "$capacity_summary")"
  capacity_ten="$(grep -E '^readonly_query_capacity=passed nodes=10 ' "$capacity_summary")"
  capacity_fifty="$(grep -E '^readonly_query_capacity=passed nodes=50 ' "$capacity_summary")"
  if [ -z "$capacity_one" ] || [ -z "$capacity_ten" ] || [ -z "$capacity_fifty" ]; then
    fixed_failure 'capacity_summary_matrix_invalid'
  fi
  if ! env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    go test ./internal/api -run '^TestAccountInventoryHTTPAuthorizationPaginationAndErrorMapping$' \
      -count=1 >"$runtime_directory/http.log" 2>&1; then
    fixed_failure 'http_gate_failed'
  fi
  if ! env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    go test -race ./internal/store ./internal/api \
      -run '^(TestAccountInventoryReadonlyQuery(StoreAndPermissionMatrix|SeesOnlyCommittedPromotionsScopeAndRollback|AuditCommitAndDisconnectSemantics|DatabaseFaultsFailClosedAndRecover)|TestAccountInventoryHTTPAuthorizationPaginationAndErrorMapping)$' \
      -count=1 >"$runtime_directory/race.log" 2>&1; then
    fixed_failure 'race_gate_failed'
  fi

  strict_cleanup
  echo 'account_inventory_readonly_query_postgres=success server_major=18 migration=8 permissions=covered query_semantics=covered http=covered concurrency=covered atomic_audit=covered database_faults=covered capacity_1_10_50=covered query_external_requests=0 cleanup_containers=0 cleanup_volumes=0 cleanup_networks=0'
  printf '%s\n' "$capacity_one" "$capacity_ten" "$capacity_fifty"
}

main "$@"
