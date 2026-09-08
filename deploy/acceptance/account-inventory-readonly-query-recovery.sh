#!/usr/bin/env bash
set -euo pipefail
umask 077

unset HTTP_PROXY
unset HTTPS_PROXY
unset ALL_PROXY
unset http_proxy
unset https_proxy
unset all_proxy
export NO_PROXY='*'
export no_proxy='*'
export GOPROXY='https://goproxy.cn,direct'

if [ "$#" -ne 0 ]; then
  echo 'account_inventory_readonly_query_recovery=failed reason=invalid_arguments' >&2
  exit 1
fi

script_directory="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
repository_root="$(CDPATH='' cd -- "$script_directory/../.." && pwd)"
compose_file="$script_directory/account-inventory-readonly-query-postgres.compose.yaml"
temporary_root="${TMPDIR:-/tmp}"
temporary_root="${temporary_root%/}"
runtime_directory=''
project_name=''
harness=''
control_binary=''
control_log=''
phase_log=''
ready_file=''
go_file=''
keyring_file=''
bootstrap_file=''
data_plane_key_file=''
session_file=''
control_port='18082'
lock_directory="${temporary_root}/relay-control-readonly-query-control-18082.lock"
lock_acquired=false
control_pid=''
outage_pid=''

fixed_failure() {
  echo "account_inventory_readonly_query_recovery=failed reason=$1" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || fixed_failure 'required_command_unavailable'
}

compose() {
  env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    docker compose --project-name "$project_name" --file "$compose_file" "$@"
}

cleanup() {
  local exit_code=$?
  trap - EXIT HUP INT TERM
  if [ -n "$outage_pid" ]; then
    kill "$outage_pid" >/dev/null 2>&1 || true
    wait "$outage_pid" >/dev/null 2>&1 || true
  fi
  if [ -n "$control_pid" ]; then
    kill "$control_pid" >/dev/null 2>&1 || true
    wait "$control_pid" >/dev/null 2>&1 || true
  fi
  if [ -n "$project_name" ]; then
    compose down --volumes --remove-orphans >/dev/null 2>&1 || true
  fi
  case "$runtime_directory" in
    "${temporary_root}"/relay-control-readonly-query-recovery.*)
      rm -rf -- "$runtime_directory"
      ;;
  esac
  if [ "$lock_acquired" = true ]; then
    rmdir "$lock_directory" >/dev/null 2>&1 || true
  fi
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
    "${temporary_root}"/relay-control-readonly-query-recovery.*)
      rm -rf -- "$runtime_directory"
      ;;
    *) fixed_failure 'cleanup_runtime_path_invalid' ;;
  esac
  if [ -e "$runtime_directory" ]; then
    fixed_failure 'cleanup_runtime_residual'
  fi
  if ! rmdir "$lock_directory" >/dev/null 2>&1; then
    fixed_failure 'cleanup_lock_failed'
  fi
  lock_acquired=false
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

set_database_urls() {
  local port
  port="$(database_port)"
  export CONTROL_READONLY_QUERY_RECOVERY_OWNER_URL="postgres://relay_control_migrator:relay_control_migrator_dev_only@127.0.0.1:${port}/relay_station_control?sslmode=disable"
  export CONTROL_READONLY_QUERY_RECOVERY_RUNTIME_URL="postgres://relay_control_app_dev:relay_control_runtime_dev_only@127.0.0.1:${port}/relay_station_control?sslmode=disable"
}

wait_for_postgres() {
  local attempts=60
  until compose exec -T postgres pg_isready --username relay_control_migrator --dbname relay_station_control >/dev/null 2>&1
  do
    attempts=$((attempts - 1))
    if [ "$attempts" -eq 0 ]; then
      fixed_failure 'postgres_unavailable'
    fi
    sleep 1
  done
}

wait_for_control() {
  local attempts=100
  export CONTROL_READONLY_QUERY_RECOVERY_CONTROL_URL="http://127.0.0.1:${control_port}"
  until "$harness" control-health >>"$runtime_directory/control-health.log" 2>&1
  do
    if [ -n "$control_pid" ] && ! kill -0 "$control_pid" >/dev/null 2>&1; then
      wait "$control_pid" >/dev/null 2>&1 || true
      control_pid=''
      fixed_failure 'control_start_failed'
    fi
    attempts=$((attempts - 1))
    if [ "$attempts" -eq 0 ]; then
      fixed_failure 'control_unavailable'
    fi
    sleep 0.1
  done
}

start_control() {
  CONTROL_HTTP_ADDR="127.0.0.1:${control_port}" \
  CONTROL_ENVIRONMENT_ID='readonly-query-recovery' \
  CONTROL_ENVIRONMENT='dev' \
  CONTROL_COOKIE_SECURE='false' \
  CONTROL_MFA_REQUIRED='false' \
  CONTROL_BOOTSTRAP_SECRET_FILE="$bootstrap_file" \
  CONTROL_AUTH_KEYRING_FILE="$keyring_file" \
  CONTROL_ACCOUNT_INVENTORY_POLL_ENABLED='false' \
  DATABASE_URL="$CONTROL_READONLY_QUERY_RECOVERY_RUNTIME_URL" \
    "$control_binary" >>"$control_log" 2>&1 &
  control_pid=$!
  wait_for_control
}

stop_control() {
  local attempts=150
  if [ -z "$control_pid" ] || ! kill -0 "$control_pid" >/dev/null 2>&1; then
    fixed_failure 'control_not_running'
  fi
  kill -TERM "$control_pid" >/dev/null 2>&1 || fixed_failure 'control_stop_failed'
  while kill -0 "$control_pid" >/dev/null 2>&1; do
    attempts=$((attempts - 1))
    if [ "$attempts" -eq 0 ]; then
      fixed_failure 'control_stop_timeout'
    fi
    sleep 0.1
  done
  wait "$control_pid" >/dev/null 2>&1 || true
  control_pid=''
  export CONTROL_READONLY_QUERY_RECOVERY_CONTROL_URL="http://127.0.0.1:${control_port}"
  if "$harness" control-health >>"$runtime_directory/control-health.log" 2>&1; then
    fixed_failure 'control_still_available'
  fi
}

crash_control() {
  local attempts=100
  if [ -z "$control_pid" ] || ! kill -0 "$control_pid" >/dev/null 2>&1; then
    fixed_failure 'control_not_running'
  fi
  kill -KILL "$control_pid" >/dev/null 2>&1 || fixed_failure 'control_crash_failed'
  while kill -0 "$control_pid" >/dev/null 2>&1; do
    attempts=$((attempts - 1))
    if [ "$attempts" -eq 0 ]; then
      fixed_failure 'control_crash_timeout'
    fi
    sleep 0.05
  done
  wait "$control_pid" >/dev/null 2>&1 || true
  control_pid=''
  export CONTROL_READONLY_QUERY_RECOVERY_CONTROL_URL="http://127.0.0.1:${control_port}"
  if "$harness" control-health >>"$runtime_directory/control-health.log" 2>&1; then
    fixed_failure 'control_still_available'
  fi
}

write_secrets() {
  local key data_plane_key
  openssl rand -hex 32 >"$bootstrap_file" 2>/dev/null || fixed_failure 'secret_generation_failed'
  key="$(openssl rand -base64 32 2>/dev/null | tr -d '\r\n=')" || fixed_failure 'secret_generation_failed'
  printf '{"format_version":1,"environment":"dev","current":1,"keys":[{"version":1,"key":"%s"}]}\n' "$key" >"$keyring_file"
  unset key
  data_plane_key="$(openssl rand -hex 32 2>/dev/null)" || fixed_failure 'secret_generation_failed'
  printf '%s\n' "$data_plane_key" >"$data_plane_key_file"
  mkdir "$runtime_directory/auth"
  printf '%s\n' \
    'host: "0.0.0.0"' \
    'port: 8317' \
    'remote-management:' \
    '  allow-remote: false' \
    '  disable-control-panel: true' \
    'auth-dir: "/run/readonly-query-auth"' \
    "api-keys: [\"${data_plane_key}\"]" \
    'debug: false' \
    'logging-to-file: false' \
    'usage-statistics-enabled: false' \
    'proxy-url: ""' >"$runtime_directory/config.yaml"
  unset data_plane_key
  chmod 700 "$runtime_directory/auth"
  chmod 400 "$bootstrap_file" "$keyring_file"
  chmod 600 "$data_plane_key_file" "$runtime_directory/config.yaml"
}

wait_for_data_plane() {
  local attempts=240
  until compose run --rm data-plane-probe 1 >"$runtime_directory/data-plane-health.log" 2>&1
  do
    attempts=$((attempts - 1))
    if [ "$attempts" -eq 0 ]; then
      fixed_failure 'official_data_plane_unavailable'
    fi
    sleep 0.25
  done
}

start_data_plane() {
  if ! compose up --detach data-plane >/dev/null 2>&1; then
    fixed_failure 'official_data_plane_start_failed'
  fi
  wait_for_data_plane
  grep -Fqx 'account_inventory_readonly_query_data_plane=success official_data_plane_baseline=1' \
    "$runtime_directory/data-plane-health.log" || fixed_failure 'official_data_plane_baseline_invalid'
}

migrate_database() {
  if ! (
    cd "$repository_root/tools"
    env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
      GOOSE_DRIVER=postgres GOOSE_DBSTRING="$CONTROL_READONLY_QUERY_RECOVERY_OWNER_URL" GOOSE_MIGRATION_DIR=../migrations \
      go tool goose up-to 9
  ) >"$runtime_directory/migration.log" 2>&1; then
    fixed_failure 'migration_failed'
  fi
  if ! compose exec -T postgres psql --username relay_control_migrator --dbname relay_station_control \
    --tuples-only --no-align --command \
    'SELECT max(version_id) FROM goose_db_version WHERE is_applied' >"$runtime_directory/migration-version.log" 2>&1; then
    fixed_failure 'migration_version_check_failed'
  fi
  grep -Fxq '9' "$runtime_directory/migration-version.log" || fixed_failure 'migration_version_invalid'
}

build_binaries() {
  if ! env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    GOCACHE="$runtime_directory/go-cache" go build -o "$harness" ./deploy/acceptance/account-inventory-readonly-query-postgres-recovery >"$runtime_directory/harness-build.log" 2>&1; then
    fixed_failure 'harness_build_failed'
  fi
  if ! env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    GOCACHE="$runtime_directory/go-cache" go build -o "$control_binary" ./cmd/control >"$runtime_directory/control-build.log" 2>&1; then
    fixed_failure 'control_build_failed'
  fi
}

run_phase() {
  "$harness" "$1" >>"$phase_log" 2>&1 || fixed_failure "$2"
}

wait_for_outage_ready() {
  local attempts=200
  until [ -f "$ready_file" ]; do
    if ! kill -0 "$outage_pid" >/dev/null 2>&1; then
      wait "$outage_pid" >/dev/null 2>&1 || true
      outage_pid=''
      fixed_failure 'outage_probe_not_ready'
    fi
    attempts=$((attempts - 1))
    if [ "$attempts" -eq 0 ]; then
      fixed_failure 'outage_probe_not_ready'
    fi
    sleep 0.05
  done
}

run_outage_window() {
  export CONTROL_READONLY_QUERY_RECOVERY_READY_FILE="$ready_file"
  export CONTROL_READONLY_QUERY_RECOVERY_GO_FILE="$go_file"
  "$harness" outage-window >>"$phase_log" 2>&1 &
  outage_pid=$!
  wait_for_outage_ready

  stop_control
  compose stop --timeout 1 postgres >/dev/null 2>&1 || fixed_failure 'postgres_stop_failed'
  if compose exec -T postgres pg_isready --username relay_control_migrator --dbname relay_station_control >/dev/null 2>&1; then
    fixed_failure 'postgres_still_available'
  fi
  if ! compose run --rm data-plane-probe 100 >"$runtime_directory/data-plane-outage.log" 2>&1; then
    fixed_failure 'official_data_plane_outage_failed'
  fi
  grep -Fqx 'account_inventory_readonly_query_data_plane=success official_data_plane_http=100/100' \
    "$runtime_directory/data-plane-outage.log" || fixed_failure 'official_data_plane_outage_invalid'
  touch "$go_file"
  if ! wait "$outage_pid"; then
    outage_pid=''
    fixed_failure 'outage_window_failed'
  fi
  outage_pid=''
  unset CONTROL_READONLY_QUERY_RECOVERY_READY_FILE
  unset CONTROL_READONLY_QUERY_RECOVERY_GO_FILE
  grep -Fqx 'account_inventory_readonly_query_recovery=success phase=outage-window management_query_fail_closed=1' "$phase_log" || fixed_failure 'outage_summary_invalid'
}

run_http_inflight_stop() {
  ready_file="$runtime_directory/http-inflight.ready"
  go_file="$runtime_directory/http-inflight.go"
  export CONTROL_READONLY_QUERY_RECOVERY_READY_FILE="$ready_file"
  export CONTROL_READONLY_QUERY_RECOVERY_GO_FILE="$go_file"
  "$harness" http-inflight-stop >>"$phase_log" 2>&1 &
  outage_pid=$!
  wait_for_outage_ready
  crash_control
  touch "$go_file"
  if ! wait "$outage_pid"; then
    outage_pid=''
    fixed_failure 'http_inflight_stop_failed'
  fi
  outage_pid=''
  unset CONTROL_READONLY_QUERY_RECOVERY_READY_FILE
  unset CONTROL_READONLY_QUERY_RECOVERY_GO_FILE
  grep -Fqx 'account_inventory_readonly_query_recovery=success phase=http-inflight-stop response_delivered=0 audit_commits=0' \
    "$phase_log" || fixed_failure 'http_inflight_stop_summary_invalid'
}

main() {
  trap cleanup EXIT
  trap 'exit 130' HUP INT TERM
  require_command docker
  require_command go
  require_command openssl

  if ! mkdir "$lock_directory" 2>/dev/null; then
    fixed_failure 'control_port_concurrent_or_stale_lock'
  fi
  lock_acquired=true
  runtime_directory="$(mktemp -d "${temporary_root}/relay-control-readonly-query-recovery.XXXXXX")"
  project_name="relay-control-readonly-query-recovery-$(printf '%s' "${runtime_directory##*.}" | tr '[:upper:]' '[:lower:]')"
  harness="$runtime_directory/readonly-query-recovery"
  control_binary="$runtime_directory/control"
  control_log="$runtime_directory/control.log"
  phase_log="$runtime_directory/phases.log"
  ready_file="$runtime_directory/outage.ready"
  go_file="$runtime_directory/outage.go"
  keyring_file="$runtime_directory/auth-keyring.json"
  bootstrap_file="$runtime_directory/bootstrap-secret"
  data_plane_key_file="$runtime_directory/data-plane-key"
  session_file="$runtime_directory/http-session.json"
  export CONTROL_READONLY_QUERY_POSTGRES_PROJECT="$project_name"
  export CONTROL_READONLY_QUERY_RUNTIME_DIRECTORY="$runtime_directory"
  export CONTROL_READONLY_QUERY_HOST_UID="$(id -u)"
  export CONTROL_READONLY_QUERY_HOST_GID="$(id -g)"
  export CONTROL_READONLY_QUERY_RECOVERY_KEYRING_FILE="$keyring_file"
  export CONTROL_READONLY_QUERY_RECOVERY_SESSION_FILE="$session_file"

  write_secrets
  if ! compose up --detach --wait postgres >/dev/null 2>&1; then
    fixed_failure 'postgres_start_failed'
  fi
  set_database_urls
  wait_for_postgres
  migrate_database
  build_binaries
  start_data_plane
  run_phase prepare 'prepare_failed'
  start_control

  run_outage_window
  run_phase rotate-keyring 'keyring_rotation_failed'
  compose start postgres >/dev/null 2>&1 || fixed_failure 'postgres_restart_failed'
  wait_for_postgres
  set_database_urls
  run_phase wait 'postgres_restart_verification_failed'
  run_phase restart-verify 'current_truth_recovery_failed'
  start_control
  run_http_inflight_stop
  start_control
  run_phase http-restart-query 'http_restart_query_failed'

  run_phase statement-timeout 'statement_timeout_failed'
  run_phase connection-exhaustion 'connection_exhaustion_failed'
  run_phase audit-commit-failure 'audit_commit_failure_failed'

  stop_control
  strict_cleanup
  echo 'account_inventory_readonly_query_recovery=success server_major=18 postgres_stop_restart=1 control_process_stop_restart=1 http_inflight_stop_response_delivered=0 http_inflight_stop_audit_commits=0 http_restart_query=1 rotated_keyring_restart_session=1 statement_timeout_fail_closed=1 pool_connection_exhaustion_fail_closed=1 audit_commit_failure_fail_closed=1 store_recovered_current_items=1 official_data_plane_baseline=1 official_data_plane_http=100/100 cleanup_containers=0 cleanup_volumes=0 cleanup_networks=0'
}

main "$@"
