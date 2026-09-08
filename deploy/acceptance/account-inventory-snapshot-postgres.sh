#!/usr/bin/env bash
set -euo pipefail

unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
export NO_PROXY='*' no_proxy='*'
export GOPROXY="${CONTROL_SNAPSHOT_ACCEPTANCE_GOPROXY:-https://goproxy.cn,direct}"
export npm_config_registry='https://registry.npmmirror.com'

script_directory="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
repository_root="$(CDPATH='' cd -- "$script_directory/../.." && pwd)"
compose_file="$script_directory/account-inventory-snapshot-postgres.compose.yaml"
temporary_root="${TMPDIR:-/tmp}"
temporary_root="${temporary_root%/}"
runtime_directory="$(mktemp -d "$temporary_root/relay-control-snapshot-postgres.XXXXXX")"
project_name="relay-control-snapshot-pg-$$"
harness="$runtime_directory/snapshot-postgres-recovery"
ready_file="$runtime_directory/interrupted-finalize.ready"
harness_pid=''
export CONTROL_SNAPSHOT_POSTGRES_PROJECT="$project_name"

fixed_failure() {
  echo "account_inventory_snapshot_postgres=failed reason=$1" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || fixed_failure 'required_command_unavailable'
}

compose() {
  docker compose --project-name "$project_name" --file "$compose_file" "$@"
}

cleanup() {
  local exit_code=$?
  trap - EXIT HUP INT TERM
  if [ -n "$harness_pid" ]; then
    kill "$harness_pid" >/dev/null 2>&1 || true
    wait "$harness_pid" >/dev/null 2>&1 || true
  fi
  compose down --volumes --remove-orphans >/dev/null 2>&1 || true
  case "$runtime_directory" in
    "$temporary_root"/relay-control-snapshot-postgres.*)
      rm -rf -- "$runtime_directory"
      ;;
  esac
  return "$exit_code"
}

wait_for_postgres() {
  local attempts=60
  until compose exec -T postgres pg_isready \
    --username relay_control_migrator --dbname relay_station_control >/dev/null 2>&1
  do
    attempts=$((attempts - 1))
    if [ "$attempts" -eq 0 ]; then
      fixed_failure 'postgres_unavailable'
    fi
    sleep 1
  done
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

set_database_urls() {
  local port
  port="$(database_port)"
  export CONTROL_SNAPSHOT_RECOVERY_OWNER_URL="postgres://relay_control_migrator:relay_control_migrator_dev_only@127.0.0.1:${port}/relay_station_control?sslmode=disable"
  export CONTROL_SNAPSHOT_RECOVERY_RUNTIME_URL="postgres://relay_control_app_dev:relay_control_runtime_dev_only@127.0.0.1:${port}/relay_station_control?sslmode=disable"
}

migrate_database() {
  local migration_log="$runtime_directory/migration.log"
  if ! (
    cd "$repository_root/tools"
    go tool goose -dir ../migrations postgres "$CONTROL_SNAPSHOT_RECOVERY_OWNER_URL" up
  ) >"$migration_log" 2>&1; then
    fixed_failure 'migration_failed'
  fi
}

run_focused_store_gate() {
  local store_log="$runtime_directory/store.log"
  if ! compose exec -T postgres psql --username relay_control_migrator \
    --dbname relay_station_control --set ON_ERROR_STOP=1 --command \
    'ALTER ROLE relay_control_app_dev CONNECTION LIMIT -1' \
    >"$runtime_directory/store-role-reset.log" 2>&1; then
    cat "$runtime_directory/store-role-reset.log" >&2
    fixed_failure 'focused_store_role_reset_failed'
  fi
  if ! CONTROL_DATABASE_TEST_URL="$CONTROL_SNAPSHOT_RECOVERY_OWNER_URL" \
    CONTROL_RUNTIME_DATABASE_TEST_URL="$CONTROL_SNAPSHOT_RECOVERY_RUNTIME_URL" \
    go test ./internal/store \
      -run '^(TestAccountInventorySnapshot|TestInventorySnapshot)' -count=1 \
      >"$store_log" 2>&1
  then
    cat "$store_log" >&2
    fixed_failure 'focused_store_gate_failed'
  fi
}

run_interrupted_finalize() {
  local attempts=100
  export CONTROL_SNAPSHOT_RECOVERY_READY_FILE="$ready_file"
  "$harness" interrupted-finalize &
  harness_pid=$!
  until [ -f "$ready_file" ]; do
    if ! kill -0 "$harness_pid" >/dev/null 2>&1; then
      wait "$harness_pid" >/dev/null 2>&1 || true
      harness_pid=''
      fixed_failure 'interrupted_finalize_not_ready'
    fi
    attempts=$((attempts - 1))
    if [ "$attempts" -eq 0 ]; then
      fixed_failure 'interrupted_finalize_not_ready'
    fi
    sleep 0.05
  done
  compose stop --timeout 1 postgres >/dev/null 2>&1 || fixed_failure 'postgres_stop_failed'
  if ! wait "$harness_pid"; then
    harness_pid=''
    fixed_failure 'interrupted_finalize_failed'
  fi
  harness_pid=''
  unset CONTROL_SNAPSHOT_RECOVERY_READY_FILE
}

main() {
  trap cleanup EXIT
  trap 'exit 130' HUP INT TERM
  require_command docker
  require_command go

  if ! compose up --detach --wait postgres >/dev/null 2>&1; then
    fixed_failure 'postgres_start_failed'
  fi
  set_database_urls

  migrate_database
  if ! go build -o "$harness" ./deploy/acceptance/account-inventory-snapshot-postgres-recovery; then
    fixed_failure 'harness_build_failed'
  fi

  "$harness" prepare
  run_interrupted_finalize
  "$harness" outage
  compose start postgres >/dev/null 2>&1 || fixed_failure 'postgres_restart_failed'
  wait_for_postgres
  set_database_urls
  "$harness" wait
  "$harness" recover
  "$harness" transaction-timeout
  "$harness" exhaustion
  "$harness" lifecycle-before-outage

  compose stop --timeout 1 postgres >/dev/null 2>&1 || fixed_failure 'postgres_second_stop_failed'
  "$harness" data-plane-only
  compose start postgres >/dev/null 2>&1 || fixed_failure 'postgres_second_restart_failed'
  wait_for_postgres
  set_database_urls
  "$harness" wait
  "$harness" restart-verify
  run_focused_store_gate

  echo 'account_inventory_snapshot_postgres=success server_major=18 stop_rollback=true recovered_snapshot_count=2 recovered_state_count=2 synthetic_data_plane_http=50/50 management_requests=0'
}

main "$@"
