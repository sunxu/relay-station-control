#!/usr/bin/env bash
set -euo pipefail
umask 077

unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
export NO_PROXY='*' no_proxy='*'
export GOPROXY='https://goproxy.cn,direct'

if [ "$#" -ne 0 ]; then
  echo 'account_inventory_history_data_plane=failed reason=invalid_arguments' >&2
  exit 1
fi

script_directory="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
repository_root="$(CDPATH='' cd -- "$script_directory/../.." && pwd)"
compose_file="$script_directory/account-inventory-readonly-query-postgres.compose.yaml"
temporary_root="${TMPDIR:-/tmp}"
temporary_root="${temporary_root%/}"
runtime_directory=''
project_name=''
control_binary=''
control_log=''
control_pid=''
control_port='18086'
lock_directory="$temporary_root/relay-control-history-data-plane-18086.lock"
lock_acquired=false

fixed_failure() {
  echo "account_inventory_history_data_plane=failed reason=$1" >&2
  exit 1
}

compose() {
  env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    docker compose --project-name "$project_name" --file "$compose_file" "$@"
}

cleanup() {
  local exit_code=$?
  trap - EXIT HUP INT TERM
  if [ -n "$control_pid" ]; then
    kill -TERM "$control_pid" >/dev/null 2>&1 || true
    wait "$control_pid" >/dev/null 2>&1 || true
  fi
  if [ -n "$project_name" ]; then
    compose down --volumes --remove-orphans >/dev/null 2>&1 || true
  fi
  case "$runtime_directory" in
    "$temporary_root"/relay-control-history-data-plane.*) rm -rf -- "$runtime_directory" ;;
  esac
  if [ "$lock_acquired" = true ]; then
    rmdir "$lock_directory" >/dev/null 2>&1 || true
  fi
  return "$exit_code"
}

strict_cleanup() {
  local residual
  compose down --volumes --remove-orphans >/dev/null 2>&1 \
    || fixed_failure 'cleanup_compose_down_failed'
  residual="$(compose ps --all --quiet 2>/dev/null)" \
    || fixed_failure 'cleanup_inspection_failed'
  [ -z "$residual" ] || fixed_failure 'cleanup_container_residual'
  residual="$(env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    docker volume ls --quiet --filter "label=com.docker.compose.project=${project_name}" 2>/dev/null)" \
    || fixed_failure 'cleanup_inspection_failed'
  [ -z "$residual" ] || fixed_failure 'cleanup_volume_residual'
  residual="$(env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    docker network ls --quiet --filter "label=com.docker.compose.project=${project_name}" 2>/dev/null)" \
    || fixed_failure 'cleanup_inspection_failed'
  [ -z "$residual" ] || fixed_failure 'cleanup_network_residual'
  case "$runtime_directory" in
    "$temporary_root"/relay-control-history-data-plane.*) rm -rf -- "$runtime_directory" ;;
    *) fixed_failure 'cleanup_runtime_path_invalid' ;;
  esac
  [ ! -e "$runtime_directory" ] || fixed_failure 'cleanup_runtime_residual'
  rmdir "$lock_directory" >/dev/null 2>&1 || fixed_failure 'cleanup_lock_failed'
  lock_acquired=false
  trap - EXIT HUP INT TERM
}

write_config() {
  local keyring_key data_plane_key
  openssl rand -hex 32 >"$runtime_directory/bootstrap-secret" 2>/dev/null \
    || fixed_failure 'secret_generation_failed'
  keyring_key="$(openssl rand -base64 32 2>/dev/null | tr -d '\r\n=')" \
    || fixed_failure 'secret_generation_failed'
  printf '{"format_version":1,"environment":"dev","current":1,"keys":[{"version":1,"key":"%s"}]}\n' \
    "$keyring_key" >"$runtime_directory/auth-keyring.json"
  unset keyring_key
  data_plane_key="$(openssl rand -hex 32 2>/dev/null)" \
    || fixed_failure 'secret_generation_failed'
  printf '%s\n' "$data_plane_key" >"$runtime_directory/data-plane-key"
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
  chmod 400 "$runtime_directory/bootstrap-secret" "$runtime_directory/auth-keyring.json"
  chmod 600 "$runtime_directory/data-plane-key" "$runtime_directory/config.yaml"
}

database_port() {
  local published port
  published="$(compose port postgres 5432 2>/dev/null)" \
    || fixed_failure 'postgres_port_unavailable'
  port="${published##*:}"
  case "$port" in
    ''|*[!0-9]*) fixed_failure 'postgres_port_invalid' ;;
  esac
  printf '%s\n' "$port"
}

migrate_database() {
  local owner_url="$1"
  if ! (
    cd "$repository_root/tools"
    env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
      GOOSE_DRIVER=postgres GOOSE_DBSTRING="$owner_url" GOOSE_MIGRATION_DIR=../migrations \
      go tool goose up
  ) >"$runtime_directory/migration.log" 2>&1; then
    fixed_failure 'migration_failed'
  fi
}

seed_environment() {
  if ! compose exec -T postgres psql --username relay_control_migrator \
    --dbname relay_station_control --set ON_ERROR_STOP=1 --command \
    "INSERT INTO environments (environment_id,name,environment_type) VALUES ('history-data-plane','History data-plane acceptance','dev') ON CONFLICT (singleton_id) DO NOTHING" \
    >"$runtime_directory/environment-seed.log" 2>&1; then
    fixed_failure 'environment_seed_failed'
  fi
}

wait_for_data_plane() {
  local attempts=120
  until compose run --rm --entrypoint /bin/sh data-plane-probe \
    -c 'nc -z -w 1 data-plane 8317' >/dev/null 2>&1
  do
    attempts=$((attempts - 1))
    [ "$attempts" -gt 0 ] || fixed_failure 'official_data_plane_unavailable'
    sleep 0.25
  done
}

wait_for_control() {
  local attempts=100
  until curl --noproxy '*' --fail --silent --show-error \
    "http://127.0.0.1:${control_port}/api/healthz" >/dev/null 2>&1
  do
    if [ -n "$control_pid" ] && ! kill -0 "$control_pid" >/dev/null 2>&1; then
      wait "$control_pid" >/dev/null 2>&1 || true
      control_pid=''
      if grep -Fq '"msg":"invalid control configuration"' "$control_log"; then
        fixed_failure 'control_start_invalid_configuration'
      fi
      if grep -Fq '"msg":"database initialization failed"' "$control_log"; then
        fixed_failure 'control_start_database_initialization_failed'
      fi
      if grep -Fq '"msg":"database unavailable"' "$control_log"; then
        fixed_failure 'control_start_database_unavailable'
      fi
      if grep -Fq '"msg":"environment identity verification failed"' "$control_log"; then
        fixed_failure 'control_start_environment_identity_failed'
      fi
      if grep -Fq '"msg":"control stopped unexpectedly"' "$control_log"; then
        fixed_failure 'control_start_listen_failed'
      fi
      fixed_failure 'control_start_failed'
    fi
    attempts=$((attempts - 1))
    [ "$attempts" -gt 0 ] || fixed_failure 'control_start_timeout'
    sleep 0.1
  done
}

stop_control() {
  local attempts=150
  [ -n "$control_pid" ] && kill -0 "$control_pid" >/dev/null 2>&1 \
    || fixed_failure 'control_not_running'
  kill -TERM "$control_pid" >/dev/null 2>&1 || fixed_failure 'control_stop_failed'
  while kill -0 "$control_pid" >/dev/null 2>&1; do
    attempts=$((attempts - 1))
    [ "$attempts" -gt 0 ] || fixed_failure 'control_stop_timeout'
    sleep 0.1
  done
  wait "$control_pid" >/dev/null 2>&1 || true
  control_pid=''
  if curl --noproxy '*' --fail --silent --show-error \
    "http://127.0.0.1:${control_port}/api/healthz" >/dev/null 2>&1; then
    fixed_failure 'control_still_available'
  fi
}

main() {
  local port owner_url runtime_url
  trap cleanup EXIT
  trap 'exit 130' HUP INT TERM
  for command in docker go openssl curl; do
    command -v "$command" >/dev/null 2>&1 || fixed_failure 'required_command_unavailable'
  done
  mkdir "$lock_directory" 2>/dev/null || fixed_failure 'control_port_concurrent_or_stale_lock'
  lock_acquired=true
  runtime_directory="$(mktemp -d "$temporary_root/relay-control-history-data-plane.XXXXXX")"
  project_name="relay-control-history-data-plane-$(printf '%s' "${runtime_directory##*.}" | tr '[:upper:]' '[:lower:]')"
  control_binary="$runtime_directory/control"
  control_log="$runtime_directory/control.log"
  export CONTROL_READONLY_QUERY_POSTGRES_PROJECT="$project_name"
  export CONTROL_READONLY_QUERY_RUNTIME_DIRECTORY="$runtime_directory"
  export CONTROL_READONLY_QUERY_HOST_UID="$(id -u)"
  export CONTROL_READONLY_QUERY_HOST_GID="$(id -g)"

  write_config
  compose up --detach --wait postgres data-plane >"$runtime_directory/compose-up.log" 2>&1 \
    || fixed_failure 'dependency_start_failed'
  port="$(database_port)"
  owner_url="postgres://relay_control_migrator:relay_control_migrator_dev_only@127.0.0.1:${port}/relay_station_control?sslmode=disable"
  runtime_url="postgres://relay_control_app_dev:relay_control_runtime_dev_only@127.0.0.1:${port}/relay_station_control?sslmode=disable"
  migrate_database "$owner_url"
  seed_environment
  if ! env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    go build -o "$control_binary" ./cmd/control >"$runtime_directory/control-build.log" 2>&1; then
    fixed_failure 'control_build_failed'
  fi
  CONTROL_HTTP_ADDR="127.0.0.1:${control_port}" \
  CONTROL_ENVIRONMENT_ID='history-data-plane' \
  CONTROL_ENVIRONMENT='dev' \
  CONTROL_COOKIE_SECURE='false' \
  CONTROL_MFA_REQUIRED='false' \
  CONTROL_BOOTSTRAP_SECRET_FILE="$runtime_directory/bootstrap-secret" \
  CONTROL_AUTH_KEYRING_FILE="$runtime_directory/auth-keyring.json" \
  CONTROL_ACCOUNT_INVENTORY_POLL_ENABLED='false' \
  CONTROL_ACCOUNT_INVENTORY_HISTORY_ENABLED='false' \
  DATABASE_URL="$runtime_url" \
    "$control_binary" >"$control_log" 2>&1 &
  control_pid=$!
  wait_for_control
  wait_for_data_plane

  compose run --rm data-plane-probe 1 >"$runtime_directory/baseline.log" 2>&1 \
    || fixed_failure 'official_data_plane_baseline_failed'
  grep -Fqx 'account_inventory_readonly_query_data_plane=success official_data_plane_baseline=1' \
    "$runtime_directory/baseline.log" || fixed_failure 'official_data_plane_baseline_invalid'

  stop_control
  compose stop --timeout 1 postgres >/dev/null 2>&1 || fixed_failure 'postgres_stop_failed'
  if compose exec -T postgres pg_isready --username relay_control_migrator \
    --dbname relay_station_control >/dev/null 2>&1; then
    fixed_failure 'postgres_still_available'
  fi
  compose run --rm data-plane-probe 100 >"$runtime_directory/outage.log" 2>&1 \
    || fixed_failure 'official_data_plane_outage_failed'
  grep -Fqx 'account_inventory_readonly_query_data_plane=success official_data_plane_http=100/100' \
    "$runtime_directory/outage.log" || fixed_failure 'official_data_plane_outage_invalid'

  strict_cleanup
  echo 'account_inventory_history_data_plane=success official_image=pinned baseline_models=1/1 control_stopped=true postgres_stopped=true outage_models=100/100 scope=data_plane_isolation gateway_inference_e2e=not_covered cleanup_containers=0 cleanup_volumes=0 cleanup_networks=0 cleanup_temp=0 cleanup_lock=0'
}

cd "$repository_root"
main
