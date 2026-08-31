#!/usr/bin/env bash
set -euo pipefail
umask 077

unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
export NO_PROXY='*' no_proxy='*'
export GOPROXY='https://goproxy.cn,direct'

if [ "$#" -ne 0 ]; then
  echo 'account_inventory_history_rollback=failed reason=invalid_arguments' >&2
  exit 1
fi

script_directory="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
repository_root="$(CDPATH='' cd -- "$script_directory/../.." && pwd)"
compose_file="$script_directory/account-inventory-history-postgres.compose.yaml"
runtime_directory=''
project_name=''
control_pid=''
lock_directory=''
lock_acquired=false
bootstrap_value=''
keyring_value=''
old_revision='d4310023b3128199e485670d4bde84da7607412f'

fixed_failure() {
  echo "account_inventory_history_rollback=failed reason=$1" >&2
  exit 1
}

compose() {
  env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    docker compose --project-name "$project_name" --file "$compose_file" "$@"
}

stop_control() {
  local attempts=100 exit_code=0
  if [ -z "$control_pid" ]; then
    return
  fi
  kill -TERM "$control_pid" >/dev/null 2>&1 || fixed_failure 'old_control_sigterm_failed'
  while kill -0 "$control_pid" >/dev/null 2>&1; do
    attempts=$((attempts - 1))
    [ "$attempts" -gt 0 ] || fixed_failure 'old_control_stop_timeout'
    sleep 0.1
  done
  wait "$control_pid" >/dev/null 2>&1 || exit_code=$?
  control_pid=''
  [ "$exit_code" -eq 0 ] || fixed_failure 'old_control_exit_failed'
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
    /tmp/relay-control-history-rollback.*|/private/tmp/relay-control-history-rollback.*)
      rm -rf -- "$runtime_directory"
      ;;
  esac
  if [ "$lock_acquired" = true ]; then
    rmdir "$lock_directory" >/dev/null 2>&1 || true
  fi
  bootstrap_value=''
  keyring_value=''
  return "$exit_code"
}

strict_cleanup() {
  local residual
  compose down --volumes --remove-orphans >/dev/null 2>&1 \
    || fixed_failure 'cleanup_compose_down_failed'
  residual="$(compose ps --all --quiet 2>/dev/null)" \
    || fixed_failure 'cleanup_compose_inspection_failed'
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
    /tmp/relay-control-history-rollback.*|/private/tmp/relay-control-history-rollback.*)
      rm -rf -- "$runtime_directory"
      ;;
    *) fixed_failure 'cleanup_runtime_path_invalid' ;;
  esac
  [ ! -e "$runtime_directory" ] || fixed_failure 'cleanup_runtime_residual'
  [ "$lock_acquired" = true ] || fixed_failure 'cleanup_lock_not_owned'
  rmdir "$lock_directory" >/dev/null 2>&1 || fixed_failure 'cleanup_lock_failed'
  lock_acquired=false
  lock_directory=''
  bootstrap_value=''
  keyring_value=''
  trap - EXIT HUP INT TERM
}

build_binaries() {
  local old_source="$runtime_directory/old-source"
  mkdir -p "$old_source/cmd/history-forward-probe"
  git cat-file -e "${old_revision}^{commit}" >/dev/null 2>&1 \
    || fixed_failure 'old_revision_unavailable'
  git archive --format=tar --output="$runtime_directory/old-source.tar" "$old_revision" \
    >/dev/null 2>&1 || fixed_failure 'old_revision_export_failed'
  tar -xf "$runtime_directory/old-source.tar" -C "$old_source" \
    || fixed_failure 'old_revision_export_failed'
  cp "$script_directory/account-inventory-history-rollback/main.go" \
    "$old_source/cmd/history-forward-probe/main.go" \
    || fixed_failure 'old_probe_source_copy_failed'
  if ! env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    GOCACHE="$runtime_directory/go-build" go build -trimpath \
      -o "$runtime_directory/history-prepare" ./deploy/acceptance/account-inventory-history-rollback \
      >"$runtime_directory/current-harness-build.log" 2>&1; then
    fixed_failure 'current_harness_build_failed'
  fi
  if ! (
    cd "$old_source"
    env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
      GOCACHE="$runtime_directory/go-build" go build -trimpath \
        -o "$runtime_directory/control-old" ./cmd/control
  ) >"$runtime_directory/old-control-build.log" 2>&1; then
    fixed_failure 'old_control_build_failed'
  fi
  if ! (
    cd "$old_source"
    env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
      GOCACHE="$runtime_directory/go-build" go build -trimpath \
        -o "$runtime_directory/history-old-probe" ./cmd/history-forward-probe
  ) >"$runtime_directory/old-probe-build.log" 2>&1; then
    fixed_failure 'old_probe_build_failed'
  fi
}

prepare_auth_files() {
  bootstrap_value="$(openssl rand -hex 32 2>/dev/null)" || fixed_failure 'secret_generation_failed'
  keyring_value="$(openssl rand -base64 32 2>/dev/null | tr -d '\r\n=')" \
    || fixed_failure 'secret_generation_failed'
  printf '%s\n' "$bootstrap_value" >"$runtime_directory/bootstrap-secret"
  printf '{"format_version":1,"environment":"dev","current":1,"keys":[{"version":1,"key":"%s"}]}\n' \
    "$keyring_value" >"$runtime_directory/auth-keyring.json"
  chmod 400 "$runtime_directory/bootstrap-secret" "$runtime_directory/auth-keyring.json"
}

start_old_control() {
  local attempts=100 control_port="$1"
  CONTROL_HTTP_ADDR="127.0.0.1:${control_port}" \
  CONTROL_ENVIRONMENT_ID='history-forward' \
  CONTROL_ENVIRONMENT='dev' \
  CONTROL_COOKIE_SECURE='false' \
  CONTROL_MFA_REQUIRED='false' \
  CONTROL_BOOTSTRAP_SECRET_FILE="$runtime_directory/bootstrap-secret" \
  CONTROL_AUTH_KEYRING_FILE="$runtime_directory/auth-keyring.json" \
  CONTROL_ACCOUNT_INVENTORY_POLL_ENABLED='false' \
  CONTROL_PROVIDER_POLICY_MUTATION_ENABLED='false' \
  DATABASE_URL="$CONTROL_HISTORY_ROLLBACK_RUNTIME_URL" \
    env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
      "$runtime_directory/control-old" >"$runtime_directory/control-old.log" 2>&1 &
  control_pid=$!
  until curl --noproxy '*' --silent --show-error --fail \
    "http://127.0.0.1:${control_port}/api/healthz" >/dev/null 2>&1
  do
    if ! kill -0 "$control_pid" >/dev/null 2>&1; then
      wait "$control_pid" >/dev/null 2>&1 || true
      control_pid=''
      fixed_failure 'old_control_start_failed'
    fi
    attempts=$((attempts - 1))
    [ "$attempts" -gt 0 ] || fixed_failure 'old_control_start_timeout'
    sleep 0.1
  done
}

main() {
  local suffix port published control_port
  trap cleanup EXIT
  trap 'exit 130' HUP INT TERM
  for command_name in curl docker git go make openssl tar tr; do
    command -v "$command_name" >/dev/null 2>&1 || fixed_failure 'required_command_unavailable'
  done
  runtime_directory="$(mktemp -d /tmp/relay-control-history-rollback.XXXXXX)"
  suffix="$(printf '%s' "${runtime_directory##*.}" | tr '[:upper:]' '[:lower:]')"
  project_name="relay-control-history-rollback-${suffix}"
  export CONTROL_HISTORY_POSTGRES_PROJECT="$project_name"
  control_port=$((24000 + ($$ % 10000)))
  lock_directory="/tmp/relay-control-history-rollback-${control_port}.lock"
  mkdir "$lock_directory" >/dev/null 2>&1 || fixed_failure 'control_port_lock_unavailable'
  lock_acquired=true

  compose up --detach --wait postgres >"$runtime_directory/compose-up.log" 2>&1 \
    || fixed_failure 'postgres_start_failed'
  published="$(compose port postgres 5432 2>/dev/null)" \
    || fixed_failure 'postgres_port_unavailable'
  case "$published" in
    127.0.0.1:[0-9]*) port="${published##*:}" ;;
    *) fixed_failure 'postgres_port_invalid' ;;
  esac
  export CONTROL_HISTORY_ROLLBACK_OWNER_URL="postgres://relay_control_migrator:relay_control_migrator_dev_only@127.0.0.1:${port}/relay_station_control?sslmode=disable"
  export CONTROL_HISTORY_ROLLBACK_RUNTIME_URL="postgres://relay_control_app_dev:relay_control_runtime_dev_only@127.0.0.1:${port}/relay_station_control?sslmode=disable"
  if ! env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    GOCACHE="$runtime_directory/go-build" DATABASE_URL="$CONTROL_HISTORY_ROLLBACK_OWNER_URL" \
      make --silent migrate-up >"$runtime_directory/migration.log" 2>&1; then
    fixed_failure 'migration_failed'
  fi
  build_binaries
  prepare_auth_files
  if ! "$runtime_directory/history-prepare" prepare >"$runtime_directory/prepare.log" 2>&1; then
    grep -E '^account_inventory_history_rollback_harness=failed reason=prepare_[A-Za-z0-9_]+$' \
      "$runtime_directory/prepare.log" >&2 || true
    fixed_failure 'forward_state_prepare_failed'
  fi
  start_old_control "$control_port"
  "$runtime_directory/history-old-probe" old-probe >"$runtime_directory/old-probe.log" 2>&1 \
    || fixed_failure 'old_forward_probe_failed'
  stop_control
  "$runtime_directory/history-prepare" verify >"$runtime_directory/verify.log" 2>&1 \
    || fixed_failure 'history_changed_during_old_control'
  for forbidden in 'postgres://' 'relay_control_runtime_dev_only' "$bootstrap_value" "$keyring_value"; do
    if grep -Fq -- "$forbidden" "$runtime_directory/control-old.log"; then
      fixed_failure 'old_control_log_not_redacted'
    fi
  done
  strict_cleanup
  echo 'account_inventory_history_rollback=success migration=9 pinned_old_revision=covered snapshot_cleanup=controlled poll_cleanup=controlled current_fk_null=covered old_control_start_stop=covered pinned_old_store_probe_poll_promotion=covered pinned_old_store_probe_current_query=covered history_and_history_audit_unchanged=covered production_down=not_used cleanup_containers=0 cleanup_volumes=0 cleanup_networks=0 cleanup_temp=0 cleanup_lock=0'
}

cd "$repository_root"
main
