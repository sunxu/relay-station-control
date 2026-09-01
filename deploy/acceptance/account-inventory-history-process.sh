#!/usr/bin/env bash
set -euo pipefail
umask 077

unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
export NO_PROXY='*' no_proxy='*'
export GOPROXY='https://goproxy.cn,direct'

if [ "$#" -ne 0 ]; then
  echo 'account_inventory_history_process=failed reason=invalid_arguments' >&2
  exit 1
fi

script_directory="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
repository_root="$(CDPATH='' cd -- "$script_directory/../.." && pwd)"
compose_file="$script_directory/account-inventory-history-process.compose.yaml"
temporary_root="${TMPDIR:-/tmp}"
temporary_root="${temporary_root%/}"
runtime_directory=''
project_name=''
control_binary=''
control_log=''
network_counter_binary=''
network_counter_config=''
network_counter_log=''
network_counter_pid=''
network_counter_endpoint='http://127.0.0.1:18085'
summary_lock_pid=''
bootstrap_file=''
keyring_file=''
bootstrap_value=''
keyring_value=''
control_pid=''
control_stage='unknown'
control_port='18084'
database_host_port='55439'
lock_directory="$temporary_root/relay-control-history-process-18084-18085-55439.lock"
lock_acquired=false
fixture_instance_id='00000000-0000-4000-8000-000000000909'
fixture_summary_date=''
fixture_provider='openai'
restart_fixture_instance_id='00000000-0000-4000-8000-000000000910'
restart_fixture_summary_date=''
restart_fixture_provider='openai'
shutdown_drain_instance_id='00000000-0000-4000-8000-000000000911'
shutdown_timeout_instance_id='00000000-0000-4000-8000-000000000912'
source_fixture_instance_id='00000000-0000-4000-8000-000000000913'
permission_fixture_instance_id='00000000-0000-4000-8000-000000000914'
trigger_fixture_instance_id='00000000-0000-4000-8000-000000000915'
dual_fixture_one_instance_id='00000000-0000-4000-8000-000000000916'
dual_fixture_two_instance_id='00000000-0000-4000-8000-000000000917'
planner_fault_instance_id='00000000-0000-4000-8000-000000000918'
rollup_fault_instance_id='00000000-0000-4000-8000-000000000919'
retention_fault_instance_id='00000000-0000-4000-8000-000000000920'

fixed_failure() {
  echo "account_inventory_history_process=failed reason=$1" >&2
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
  local exit_code=$? attempts=100
  trap - EXIT HUP INT TERM
  if [ -n "$summary_lock_pid" ]; then
    kill -TERM "$summary_lock_pid" >/dev/null 2>&1 || true
    wait "$summary_lock_pid" >/dev/null 2>&1 || true
  fi
  if [ -n "$control_pid" ]; then
    kill -TERM "$control_pid" >/dev/null 2>&1 || true
    while kill -0 "$control_pid" >/dev/null 2>&1 && [ "$attempts" -gt 0 ]; do
      attempts=$((attempts - 1))
      sleep 0.1
    done
    if kill -0 "$control_pid" >/dev/null 2>&1; then
      kill -KILL "$control_pid" >/dev/null 2>&1 || true
    fi
    wait "$control_pid" >/dev/null 2>&1 || true
  fi
  if [ -n "$network_counter_pid" ]; then
    kill -TERM "$network_counter_pid" >/dev/null 2>&1 || true
    wait "$network_counter_pid" >/dev/null 2>&1 || true
  fi
  if [ -n "$project_name" ]; then
    compose down --volumes --remove-orphans >/dev/null 2>&1 || true
  fi
  case "$runtime_directory" in
    "$temporary_root"/relay-control-history-process.*)
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
    "$temporary_root"/relay-control-history-process.*)
      rm -rf -- "$runtime_directory"
      ;;
    *) fixed_failure 'cleanup_runtime_path_invalid' ;;
  esac
  [ ! -e "$runtime_directory" ] || fixed_failure 'cleanup_runtime_residual'
  if ! rmdir "$lock_directory" >/dev/null 2>&1; then
    fixed_failure 'cleanup_lock_failed'
  fi
  lock_acquired=false
  bootstrap_value=''
  keyring_value=''
  trap - EXIT HUP INT TERM
}

database_port() {
  local published port
  published="$(compose port postgres 5432 2>/dev/null)" || fixed_failure 'postgres_port_unavailable'
  case "$published" in
    127.0.0.1:[0-9]*) port="${published##*:}" ;;
    *) fixed_failure 'postgres_port_invalid' ;;
  esac
  case "$port" in
    ''|*[!0-9]*) fixed_failure 'postgres_port_invalid' ;;
  esac
  if [ "$port" -lt 1024 ] || [ "$port" -gt 65535 ]; then
    fixed_failure 'postgres_port_invalid'
  fi
  printf '%s\n' "$port"
}

set_database_urls() {
  local port
  port="$(database_port)"
  export CONTROL_HISTORY_PROCESS_MIGRATOR_URL="postgres://relay_control_migrator:relay_control_migrator_dev_only@127.0.0.1:${port}/relay_station_control?sslmode=disable"
  export CONTROL_HISTORY_PROCESS_RUNTIME_URL="postgres://relay_control_app_dev:relay_control_runtime_dev_only@127.0.0.1:${port}/relay_station_control?sslmode=disable"
}

write_secrets() {
  bootstrap_value="$(openssl rand -hex 32 2>/dev/null)" || fixed_failure 'secret_generation_failed'
  keyring_value="$(openssl rand -base64 32 2>/dev/null | tr -d '\r\n=')" \
    || fixed_failure 'secret_generation_failed'
  printf '%s\n' "$bootstrap_value" >"$bootstrap_file"
  printf '{"format_version":1,"environment":"dev","current":1,"keys":[{"version":1,"key":"%s"}]}\n' \
    "$keyring_value" >"$keyring_file"
  printf '{"listen":"127.0.0.1:18085","management_key":"%s","email":"history-network-counter@example.invalid","token":"history-network-counter-token"}\n' \
    "$bootstrap_value" >"$network_counter_config"
  chmod 400 "$bootstrap_file" "$keyring_file" "$network_counter_config"
}

migrate_up() {
  if ! env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    DATABASE_URL="$CONTROL_HISTORY_PROCESS_MIGRATOR_URL" \
    make --silent migrate-up >"$runtime_directory/migration.log" 2>&1; then
    fixed_failure 'migration_up_failed'
  fi
}

assert_migration_and_seed_environment() {
  local version
  version="$(compose exec -T postgres psql --username relay_control_migrator --dbname relay_station_control \
    --tuples-only --no-align --command \
    'SELECT max(version_id) FROM goose_db_version WHERE is_applied' 2>/dev/null)" \
    || fixed_failure 'migration_version_check_failed'
  [ "$version" = '9' ] || fixed_failure 'migration_version_invalid'
  if ! compose exec -T postgres psql --username relay_control_migrator --dbname relay_station_control \
    --set ON_ERROR_STOP=1 --command \
    "INSERT INTO environments (environment_id, name, environment_type) VALUES ('history-process', 'History process acceptance', 'dev') ON CONFLICT (singleton_id) DO NOTHING" \
    >"$runtime_directory/environment-seed.log" 2>&1; then
    fixed_failure 'environment_seed_failed'
  fi
}

build_control() {
  if ! env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    go build -trimpath -o "$control_binary" ./cmd/control \
    >"$runtime_directory/control-build.log" 2>&1; then
    fixed_failure 'control_build_failed'
  fi
  if ! env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    go build -trimpath -o "$network_counter_binary" \
      ./deploy/acceptance/account-inventory-history-fake-node \
      >"$runtime_directory/network-counter-build.log" 2>&1; then
    fixed_failure 'network_counter_build_failed'
  fi
  chmod 0555 "$network_counter_binary"
}

start_network_counter() {
  local attempts=100
  "$network_counter_binary" -config "$network_counter_config" >"$network_counter_log" 2>&1 &
  network_counter_pid=$!
  until grep -Fqx 'account_inventory_history_fake_node=ready' "$network_counter_log"; do
    if ! kill -0 "$network_counter_pid" >/dev/null 2>&1; then
      wait "$network_counter_pid" >/dev/null 2>&1 || true
      network_counter_pid=''
      fixed_failure 'network_counter_exited'
    fi
    attempts=$((attempts - 1))
    [ "$attempts" -gt 0 ] || fixed_failure 'network_counter_start_timeout'
    sleep 0.1
  done
}

stop_network_counter() {
  local attempts=100 exit_code=0
  kill -TERM "$network_counter_pid" >/dev/null 2>&1 || fixed_failure 'network_counter_sigterm_failed'
  while kill -0 "$network_counter_pid" >/dev/null 2>&1; do
    attempts=$((attempts - 1))
    [ "$attempts" -gt 0 ] || fixed_failure 'network_counter_stop_timeout'
    sleep 0.1
  done
  wait "$network_counter_pid" >/dev/null 2>&1 || exit_code=$?
  network_counter_pid=''
  [ "$exit_code" -eq 0 ] || fixed_failure 'network_counter_exit_failed'
  [ "$(grep -Fxc 'account_inventory_history_fake_node=stopped total=0 health=0 inventory=0 unauthorized=0 rejected=0' "$network_counter_log")" -eq 1 ] \
    || fixed_failure 'network_counter_nonzero'
}

require_process_test() {
  local exact_name="$1" listing
  listing="$runtime_directory/$(printf '%s' "$exact_name" | tr -c '[:alnum:]' '_').tests"
  if ! env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    go test ./deploy/acceptance/account-inventory-history-process \
    -list "^${exact_name}$" >"$listing" 2>&1; then
    fixed_failure 'process_test_discovery_failed'
  fi
  grep -Fxq "$exact_name" "$listing" || fixed_failure 'required_process_test_unavailable'
}

wait_for_control() {
  local attempts=100
  until curl --noproxy '*' --silent --show-error --fail \
    "http://127.0.0.1:${control_port}/api/healthz" >/dev/null 2>&1
  do
    if [ -n "$control_pid" ] && ! kill -0 "$control_pid" >/dev/null 2>&1; then
      wait "$control_pid" >/dev/null 2>&1 || true
      control_pid=''
      if grep -Fq '"msg":"invalid control configuration"' "$control_log"; then
        fixed_failure "control_start_${control_stage}_invalid_configuration"
      fi
      if grep -Fq '"msg":"account inventory history initialization failed"' "$control_log"; then
        fixed_failure "control_start_${control_stage}_history_initialization_failed"
      fi
      if grep -Fq '"msg":"database initialization failed"' "$control_log"; then
        fixed_failure "control_start_${control_stage}_database_initialization_failed"
      fi
      if grep -Fq '"msg":"database unavailable"' "$control_log"; then
        fixed_failure "control_start_${control_stage}_database_unavailable"
      fi
      if grep -Fq '"msg":"control stopped unexpectedly"' "$control_log"; then
        fixed_failure "control_start_${control_stage}_listen_failed"
      fi
      fixed_failure "control_start_${control_stage}_failed"
    fi
    attempts=$((attempts - 1))
    [ "$attempts" -gt 0 ] || fixed_failure 'control_start_timeout'
    sleep 0.1
  done
}

wait_for_history_ready() {
  local attempts=50 ready_metrics="$runtime_directory/restart-ready.prom"
  until curl --noproxy '*' --silent --show-error --fail \
    "http://127.0.0.1:${control_port}/metrics" >"$ready_metrics" 2>/dev/null &&
    grep -Fqx 'relay_control_account_inventory_history_enabled{reason="ready"} 1' "$ready_metrics"
  do
    if [ -z "$control_pid" ] || ! kill -0 "$control_pid" >/dev/null 2>&1; then
      fixed_failure 'control_exited_before_history_ready'
    fi
    attempts=$((attempts - 1))
    [ "$attempts" -gt 0 ] || fixed_failure 'history_ready_timeout'
    sleep 0.1
  done
}

start_default_disabled_control() {
  control_stage='default_disabled'
  CONTROL_HTTP_ADDR="127.0.0.1:${control_port}" \
  CONTROL_ENVIRONMENT_ID='history-process' \
  CONTROL_ENVIRONMENT='dev' \
  CONTROL_COOKIE_SECURE='false' \
  CONTROL_MFA_REQUIRED='false' \
  CONTROL_BOOTSTRAP_SECRET_FILE="$bootstrap_file" \
  CONTROL_AUTH_KEYRING_FILE="$keyring_file" \
  CONTROL_ACCOUNT_INVENTORY_POLL_ENABLED='false' \
  DATABASE_URL="$CONTROL_HISTORY_PROCESS_RUNTIME_URL" \
    env -u CONTROL_ACCOUNT_INVENTORY_HISTORY_ENABLED \
      -u CONTROL_ACCOUNT_INVENTORY_HISTORY_SCAN_INTERVAL \
      -u CONTROL_ACCOUNT_INVENTORY_HISTORY_CLAIM_LEASE \
      -u CONTROL_ACCOUNT_INVENTORY_HISTORY_CONCURRENCY \
      -u CONTROL_ACCOUNT_INVENTORY_HISTORY_DELETE_BATCH_SIZE \
      -u CONTROL_ACCOUNT_INVENTORY_HISTORY_STATEMENT_TIMEOUT \
      -u CONTROL_ACCOUNT_INVENTORY_HISTORY_DATABASE_BACKOFF_INITIAL \
      -u CONTROL_ACCOUNT_INVENTORY_HISTORY_DATABASE_BACKOFF_MAXIMUM \
      -u CONTROL_ACCOUNT_INVENTORY_HISTORY_SHUTDOWN_GRACE \
      -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u all_proxy -u NO_PROXY -u no_proxy \
      http_proxy="$network_counter_endpoint" https_proxy="$network_counter_endpoint" \
      "$control_binary" >>"$control_log" 2>&1 &
  control_pid=$!
  wait_for_control
}

verify_disabled_metrics() {
  if ! CONTROL_HISTORY_PROCESS_URL="http://127.0.0.1:${control_port}" \
    CONTROL_HISTORY_PROCESS_RUNTIME_DATABASE_URL="$CONTROL_HISTORY_PROCESS_RUNTIME_URL" \
    env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
      go test ./deploy/acceptance/account-inventory-history-process \
      -run '^TestAccountInventoryHistoryProcessDisabledCompatibleMetrics$' -count=1 \
      >"$runtime_directory/disabled-process-probe.log" 2>&1; then
    fixed_failure 'disabled_process_probe_failed'
  fi
}

set_metrics_snapshot_execute() {
  local action="$1" statement
  case "$action" in
    revoke)
      statement='REVOKE EXECUTE ON FUNCTION public.control_account_inventory_history_metrics_snapshot_v1() FROM relay_control_runtime'
      ;;
    grant)
      statement='GRANT EXECUTE ON FUNCTION public.control_account_inventory_history_metrics_snapshot_v1() TO relay_control_runtime'
      ;;
    *) fixed_failure 'metrics_permission_action_invalid' ;;
  esac
  if ! compose exec -T postgres psql --username relay_control_migrator --dbname relay_station_control \
    --set ON_ERROR_STOP=1 --command "$statement" \
    >"$runtime_directory/metrics-permission-${action}.log" 2>&1; then
    fixed_failure "metrics_permission_${action}_failed"
  fi
}

verify_disabled_metrics_failure_isolation() {
  if ! CONTROL_HISTORY_PROCESS_URL="http://127.0.0.1:${control_port}" \
    CONTROL_HISTORY_PROCESS_EXPECT_REASON='disabled' \
    CONTROL_HISTORY_PROCESS_FORBIDDEN_MARKER='control_account_inventory_history_metrics_snapshot_v1' \
    env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
      go test ./deploy/acceptance/account-inventory-history-process \
      -run '^TestAccountInventoryHistoryProcessMetricsFailureIsolation$' -count=1 \
      >"$runtime_directory/disabled-metrics-failure-probe.log" 2>&1; then
    fixed_failure 'disabled_metrics_failure_isolation_failed'
  fi
}

stop_control() {
  local attempts=100 exit_code=0
  if [ -z "$control_pid" ] || ! kill -0 "$control_pid" >/dev/null 2>&1; then
    fixed_failure 'control_not_running'
  fi
  kill -TERM "$control_pid" >/dev/null 2>&1 || fixed_failure 'control_sigterm_failed'
  while kill -0 "$control_pid" >/dev/null 2>&1; do
    attempts=$((attempts - 1))
    [ "$attempts" -gt 0 ] || fixed_failure 'control_stop_timeout'
    sleep 0.1
  done
  wait "$control_pid" >/dev/null 2>&1 || exit_code=$?
  control_pid=''
  [ "$exit_code" -eq 0 ] || fixed_failure 'control_exit_failed'
  if curl --noproxy '*' --silent --show-error --fail \
    "http://127.0.0.1:${control_port}/api/healthz" >/dev/null 2>&1; then
    fixed_failure 'control_still_available'
  fi
}

seed_eligible_source() {
  local age_days="${1:-4}" source_snapshots="${2:-0}"
  fixture_summary_date="$(compose exec -T postgres psql --username relay_control_migrator \
    --dbname relay_station_control --tuples-only --no-align --command \
    "SELECT (current_date - ${age_days})::text" 2>/dev/null)" \
    || fixed_failure 'fixture_date_unavailable'
  case "$fixture_summary_date" in
    ????-??-??) ;;
    *) fixed_failure 'fixture_date_invalid' ;;
  esac
  if ! CONTROL_HISTORY_PROCESS_OWNER_DATABASE_URL="$CONTROL_HISTORY_PROCESS_MIGRATOR_URL" \
    CONTROL_HISTORY_PROCESS_RUNTIME_DATABASE_URL="$CONTROL_HISTORY_PROCESS_RUNTIME_URL" \
    CONTROL_HISTORY_PROCESS_INSTANCE_ID="$fixture_instance_id" \
    CONTROL_HISTORY_PROCESS_SUMMARY_DATE="$fixture_summary_date" \
    CONTROL_HISTORY_PROCESS_PROVIDER="$fixture_provider" \
    CONTROL_HISTORY_PROCESS_NETWORK_COUNTER_ENDPOINT="$network_counter_endpoint" \
    CONTROL_HISTORY_PROCESS_SOURCE_SNAPSHOTS="$source_snapshots" \
    env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
      go test ./deploy/acceptance/account-inventory-history-process \
      -run '^TestAccountInventoryHistoryProcessSeedEligibleSource$' -count=1 \
      >"$runtime_directory/seed-eligible-source.log" 2>&1; then
    for class in reference_violation duplicate constraint_violation permission_denied guard_rejected database_failure
    do
      if grep -Fq "class=${class}" \
        "$runtime_directory/seed-eligible-source.log"; then
        fixed_failure "eligible_source_seed_${class}"
      fi
    done
    class="$(grep -Eo 'class=sqlstate_[A-Z0-9]{5}' \
      "$runtime_directory/seed-eligible-source.log" | head -1 || true)"
    case "$class" in
      class=sqlstate_?????) fixed_failure "eligible_source_seed_${class#class=}" ;;
    esac
    fixed_failure 'eligible_source_seed_failed'
  fi
}

verify_stale_fence_zero_impact() {
  if ! CONTROL_HISTORY_PROCESS_OWNER_DATABASE_URL="$CONTROL_HISTORY_PROCESS_MIGRATOR_URL" \
    CONTROL_HISTORY_PROCESS_RUNTIME_DATABASE_URL="$CONTROL_HISTORY_PROCESS_RUNTIME_URL" \
    CONTROL_HISTORY_PROCESS_INSTANCE_ID="$fixture_instance_id" \
    CONTROL_HISTORY_PROCESS_SUMMARY_DATE="$fixture_summary_date" \
    CONTROL_HISTORY_PROCESS_PROVIDER="$fixture_provider" \
    env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
      go test ./deploy/acceptance/account-inventory-history-process \
      -run '^TestAccountInventoryHistoryProcessStaleFenceHasZeroImpact$' -count=1 \
      >"$runtime_directory/stale-fence-zero-impact.log" 2>&1; then
    fixed_failure 'stale_fence_zero_impact_failed'
  fi
}

verify_source_backed_retention() {
  if ! CONTROL_HISTORY_PROCESS_OWNER_DATABASE_URL="$CONTROL_HISTORY_PROCESS_MIGRATOR_URL" \
    CONTROL_HISTORY_PROCESS_INSTANCE_ID="$fixture_instance_id" \
    CONTROL_HISTORY_PROCESS_SUMMARY_DATE="$fixture_summary_date" \
    CONTROL_HISTORY_PROCESS_PROVIDER="$fixture_provider" \
    env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
      go test ./deploy/acceptance/account-inventory-history-process \
      -run '^TestAccountInventoryHistoryProcessSourceBackedRetentionCompleted$' -count=1 \
      >"$runtime_directory/source-backed-retention.log" 2>&1; then
    fixed_failure 'source_backed_retention_failed'
  fi
}

set_history_execute() {
  local target="$1" action="$2" signature statement
  case "$target" in
    planner) signature='public.control_plan_account_inventory_history_v1(integer)' ;;
    summarize) signature='public.control_summarize_account_inventory_compaction_v1(uuid,uuid)' ;;
    rollup) signature='public.control_finalize_account_inventory_daily_rollup_v1(uuid,uuid)' ;;
    retention) signature='public.control_delete_account_inventory_poll_retention_v1(integer)' ;;
    *) fixed_failure 'history_permission_target_invalid' ;;
  esac
  case "$action" in
    revoke) statement="REVOKE EXECUTE ON FUNCTION ${signature} FROM relay_control_runtime" ;;
    grant) statement="GRANT EXECUTE ON FUNCTION ${signature} TO relay_control_runtime" ;;
    *) fixed_failure 'history_permission_action_invalid' ;;
  esac
  compose exec -T postgres psql --username relay_control_migrator --dbname relay_station_control \
    --set ON_ERROR_STOP=1 --command "$statement" \
    >"$runtime_directory/${target}-permission-${action}.log" 2>&1 \
    || fixed_failure "${target}_permission_${action}_failed"
}

set_summarize_trigger_fault() {
  local action="$1"
  case "$action" in
    create)
      compose exec -T postgres psql --username relay_control_migrator --dbname relay_station_control \
        --set ON_ERROR_STOP=1 --command \
        "CREATE FUNCTION public.test_history_process_terminal_fault() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS \$function\$ BEGIN RAISE EXCEPTION 'history-process-trigger-raw-marker' USING ERRCODE='23514'; END \$function\$; CREATE TRIGGER zz_test_history_process_terminal_fault BEFORE INSERT ON public.account_inventory_daily_summaries FOR EACH STATEMENT EXECUTE FUNCTION public.test_history_process_terminal_fault()" \
        >"$runtime_directory/trigger-fault-create.log" 2>&1 \
        || fixed_failure 'trigger_fault_create_failed'
      ;;
    drop)
      compose exec -T postgres psql --username relay_control_migrator --dbname relay_station_control \
        --set ON_ERROR_STOP=1 --command \
        'DROP TRIGGER zz_test_history_process_terminal_fault ON public.account_inventory_daily_summaries; DROP FUNCTION public.test_history_process_terminal_fault()' \
        >"$runtime_directory/trigger-fault-drop.log" 2>&1 \
        || fixed_failure 'trigger_fault_drop_failed'
      ;;
    *) fixed_failure 'trigger_fault_action_invalid' ;;
  esac
}

verify_terminal_internal_source_preserved() {
  local label="$1" fault_stage="${2:-compaction}"
  if ! CONTROL_HISTORY_PROCESS_URL="http://127.0.0.1:${control_port}" \
    CONTROL_HISTORY_PROCESS_OWNER_DATABASE_URL="$CONTROL_HISTORY_PROCESS_MIGRATOR_URL" \
    CONTROL_HISTORY_PROCESS_INSTANCE_ID="$fixture_instance_id" \
    CONTROL_HISTORY_PROCESS_SUMMARY_DATE="$fixture_summary_date" \
    CONTROL_HISTORY_PROCESS_PROVIDER="$fixture_provider" \
    CONTROL_HISTORY_PROCESS_FAULT_STAGE="$fault_stage" \
    env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
      go test ./deploy/acceptance/account-inventory-history-process \
      -run '^TestAccountInventoryHistoryProcessTerminalInternalPreservesSource$' -count=1 \
      >"$runtime_directory/${label}-terminal-internal.log" 2>&1; then
    fixed_failure "${label}_terminal_internal_failed"
  fi
  grep -F '"msg":"account inventory history stopped","component":"account_inventory_history","reason":"runtime_stopped"' \
    "$control_log" >/dev/null || fixed_failure "${label}_runtime_stopped_log_missing"
  if grep -Fq 'history-process-trigger-raw-marker' "$control_log"; then
    fixed_failure "${label}_raw_error_leaked"
  fi
}

seed_claimed_for_restart() {
  restart_fixture_summary_date="$(compose exec -T postgres psql --username relay_control_migrator \
    --dbname relay_station_control --tuples-only --no-align --command \
    "SELECT (current_date - 6)::text" 2>/dev/null)" \
    || fixed_failure 'restart_fixture_date_unavailable'
  case "$restart_fixture_summary_date" in
    ????-??-??) ;;
    *) fixed_failure 'restart_fixture_date_invalid' ;;
  esac
  if ! CONTROL_HISTORY_PROCESS_OWNER_DATABASE_URL="$CONTROL_HISTORY_PROCESS_MIGRATOR_URL" \
    CONTROL_HISTORY_PROCESS_RUNTIME_DATABASE_URL="$CONTROL_HISTORY_PROCESS_RUNTIME_URL" \
    CONTROL_HISTORY_PROCESS_RESTART_INSTANCE_ID="$restart_fixture_instance_id" \
    CONTROL_HISTORY_PROCESS_RESTART_SUMMARY_DATE="$restart_fixture_summary_date" \
    CONTROL_HISTORY_PROCESS_RESTART_PROVIDER="$restart_fixture_provider" \
    CONTROL_HISTORY_PROCESS_NETWORK_COUNTER_ENDPOINT="$network_counter_endpoint" \
    env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
      go test ./deploy/acceptance/account-inventory-history-process \
      -run '^TestAccountInventoryHistoryProcessSeedClaimedForRestart$' -count=1 \
      >"$runtime_directory/seed-claimed-for-restart.log" 2>&1; then
    if grep -Fq 'history process restart planner did not create phase matrix' \
      "$runtime_directory/seed-claimed-for-restart.log"; then
      fixed_failure 'restart_claim_planner_matrix_invalid'
    fi
    if grep -Fq 'history process restart planner failed' \
      "$runtime_directory/seed-claimed-for-restart.log"; then
      fixed_failure 'restart_claim_planner_failed'
    fi
    if grep -Fq 'history process restart claim target invalid' \
      "$runtime_directory/seed-claimed-for-restart.log"; then
      fixed_failure 'restart_claim_target_invalid'
    fi
    if grep -Fq 'history process restart claim failed' \
      "$runtime_directory/seed-claimed-for-restart.log"; then
      fixed_failure 'restart_claim_failed'
    fi
    if grep -Fq 'history process restart claim produced history output' \
      "$runtime_directory/seed-claimed-for-restart.log"; then
      fixed_failure 'restart_claim_output_not_empty'
    fi
    if grep -Fq 'history process restart claim verification failed' \
      "$runtime_directory/seed-claimed-for-restart.log"; then
      fixed_failure 'restart_claim_verification_failed'
    fi
    if grep -Fq 'history process restart phase matrix verification failed' \
      "$runtime_directory/seed-claimed-for-restart.log"; then
      fixed_failure 'restart_claim_phase_matrix_invalid'
    fi
    fixed_failure 'restart_claim_seed_failed'
  fi
}

assert_restart_claim_active() {
  local active
  active="$(compose exec -T postgres psql --username relay_control_migrator \
    --dbname relay_station_control --tuples-only --no-align --command \
    "SELECT (count(*)=3
        AND count(DISTINCT summary_date)=3
        AND count(*) FILTER (WHERE status='pending')=1
        AND count(*) FILTER (WHERE status='summarized')=1
        AND count(*) FILTER (WHERE status='deleting')=1
        AND bool_and(claim_owner IS NOT NULL AND fencing_token IS NOT NULL
          AND lease_expires_at >= clock_timestamp() + interval '5 seconds'))::integer
      FROM account_inventory_compaction_runs
      WHERE summary_date BETWEEN '${restart_fixture_summary_date}'::date
            AND '${restart_fixture_summary_date}'::date+2
        AND instance_id='${restart_fixture_instance_id}'::uuid" 2>/dev/null)" \
    || fixed_failure 'restart_claim_active_check_failed'
  [ "$active" = '1' ] || fixed_failure 'restart_phase_matrix_not_active_before_outage'
}

start_enabled_control() {
  local maximum_connections="${1:-}" statement_timeout="${2:-1s}"
  local shutdown_grace="${3:-2s}" claim_lease="${4:-5s}"
  local concurrency="${5:-1}"
  control_stage="${6:-enabled}"
  local scan_interval="${7:-1s}"
  CONTROL_HTTP_ADDR="127.0.0.1:${control_port}" \
  CONTROL_ENVIRONMENT_ID='history-process' \
  CONTROL_ENVIRONMENT='dev' \
  CONTROL_COOKIE_SECURE='false' \
  CONTROL_MFA_REQUIRED='false' \
  CONTROL_BOOTSTRAP_SECRET_FILE="$bootstrap_file" \
  CONTROL_AUTH_KEYRING_FILE="$keyring_file" \
  CONTROL_ACCOUNT_INVENTORY_POLL_ENABLED='false' \
  CONTROL_DATABASE_MAX_CONNS="$maximum_connections" \
  CONTROL_ACCOUNT_INVENTORY_HISTORY_ENABLED='true' \
  CONTROL_ACCOUNT_INVENTORY_HISTORY_SCAN_INTERVAL="$scan_interval" \
  CONTROL_ACCOUNT_INVENTORY_HISTORY_CLAIM_LEASE="$claim_lease" \
  CONTROL_ACCOUNT_INVENTORY_HISTORY_CONCURRENCY="$concurrency" \
  CONTROL_ACCOUNT_INVENTORY_HISTORY_DELETE_BATCH_SIZE='1' \
  CONTROL_ACCOUNT_INVENTORY_HISTORY_STATEMENT_TIMEOUT="$statement_timeout" \
  CONTROL_ACCOUNT_INVENTORY_HISTORY_DATABASE_BACKOFF_INITIAL='100ms' \
  CONTROL_ACCOUNT_INVENTORY_HISTORY_DATABASE_BACKOFF_MAXIMUM='1s' \
  CONTROL_ACCOUNT_INVENTORY_HISTORY_SHUTDOWN_GRACE="$shutdown_grace" \
  DATABASE_URL="${CONTROL_HISTORY_PROCESS_RUNTIME_URL}&application_name=history_process_control" \
    env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u all_proxy -u NO_PROXY -u no_proxy \
      http_proxy="$network_counter_endpoint" https_proxy="$network_counter_endpoint" \
      "$control_binary" >>"$control_log" 2>&1 &
  control_pid=$!
  wait_for_control
}

start_summary_lock() {
  local label="$1"
  compose exec -T --env PGAPPNAME=history_process_summary_lock postgres \
    psql --username relay_control_migrator --dbname relay_station_control \
    --set ON_ERROR_STOP=1 --command \
    "BEGIN; LOCK TABLE public.account_inventory_daily_provider_summaries IN ACCESS EXCLUSIVE MODE; SELECT pg_sleep(60); COMMIT" \
    >"$runtime_directory/${label}-summary-lock.log" 2>&1 &
  summary_lock_pid=$!
  local attempts=50 held
  while [ "$attempts" -gt 0 ]; do
    held="$(compose exec -T postgres psql --username relay_control_migrator \
      --dbname relay_station_control --tuples-only --no-align --command \
      "SELECT count(*) FROM pg_stat_activity WHERE application_name='history_process_summary_lock' AND state='active'" 2>/dev/null)" \
      || fixed_failure 'summary_lock_inspection_failed'
    [ "$held" = '1' ] && return
    attempts=$((attempts - 1))
    sleep 0.1
  done
  fixed_failure 'summary_lock_start_timeout'
}

release_summary_lock() {
  local attempts=50 held
  if ! compose exec -T postgres psql --username relay_control_migrator \
    --dbname relay_station_control --set ON_ERROR_STOP=1 --command \
    "SELECT pg_cancel_backend(pid) FROM pg_stat_activity WHERE application_name='history_process_summary_lock'" \
    >/dev/null 2>&1; then
    fixed_failure 'summary_lock_release_failed'
  fi
  while [ "$attempts" -gt 0 ]; do
    held="$(compose exec -T postgres psql --username relay_control_migrator \
      --dbname relay_station_control --tuples-only --no-align --command \
      "SELECT count(*) FROM pg_stat_activity WHERE application_name='history_process_summary_lock'" 2>/dev/null)" \
      || fixed_failure 'summary_lock_release_inspection_failed'
    [ "$held" = '0' ] && break
    attempts=$((attempts - 1))
    sleep 0.1
  done
  [ "$held" = '0' ] || fixed_failure 'summary_lock_release_timeout'
  wait "$summary_lock_pid" >/dev/null 2>&1 || true
  summary_lock_pid=''
}

wait_for_control_summarize_lock() {
  local expected="${1:-1}" attempts=100 waiting
  while [ "$attempts" -gt 0 ]; do
    waiting="$(compose exec -T postgres psql --username relay_control_migrator \
      --dbname relay_station_control --tuples-only --no-align --command \
      "SELECT count(*) FROM pg_stat_activity WHERE application_name='history_process_control' AND state='active' AND wait_event_type='Lock' AND query LIKE '%control_summarize_account_inventory_compaction_v1%'" 2>/dev/null)" \
      || fixed_failure 'held_transaction_inspection_failed'
    [ "$waiting" = "$expected" ] && return
    if [ -z "$control_pid" ] || ! kill -0 "$control_pid" >/dev/null 2>&1; then
      fixed_failure 'control_exited_before_held_transaction'
    fi
    attempts=$((attempts - 1))
    sleep 0.1
  done
  fixed_failure 'held_transaction_start_timeout'
}

verify_held_transaction_pool_exhaustion() {
  if ! CONTROL_HISTORY_PROCESS_OWNER_DATABASE_URL="$CONTROL_HISTORY_PROCESS_MIGRATOR_URL" \
    CONTROL_HISTORY_PROCESS_INSTANCE_ID="$fixture_instance_id" \
    CONTROL_HISTORY_PROCESS_SUMMARY_DATE="$fixture_summary_date" \
    CONTROL_HISTORY_PROCESS_PROVIDER="$fixture_provider" \
    env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
      go test ./deploy/acceptance/account-inventory-history-process \
      -run '^TestAccountInventoryHistoryProcessHeldTransactionPoolExhaustion$' -count=1 \
      >"$runtime_directory/held-transaction-pool-exhaustion.log" 2>&1; then
    fixed_failure 'held_transaction_pool_exhaustion_failed'
  fi
}

verify_pool_wait_is_bounded() {
  if curl --noproxy '*' --silent --show-error --fail --max-time 1 \
    "http://127.0.0.1:${control_port}/metrics" \
    >"$runtime_directory/pool-wait.prom" 2>"$runtime_directory/pool-wait.log"; then
    fixed_failure 'max_conns_1_did_not_exhaust_pool'
  fi
  if [ -z "$control_pid" ] || ! kill -0 "$control_pid" >/dev/null 2>&1; then
    fixed_failure 'control_exited_during_pool_wait'
  fi
}

verify_claim_retained() {
  local expected_phase="$1" label="$2"
  if ! CONTROL_HISTORY_PROCESS_OWNER_DATABASE_URL="$CONTROL_HISTORY_PROCESS_MIGRATOR_URL" \
    CONTROL_HISTORY_PROCESS_INSTANCE_ID="$fixture_instance_id" \
    CONTROL_HISTORY_PROCESS_SUMMARY_DATE="$fixture_summary_date" \
    CONTROL_HISTORY_PROCESS_PROVIDER="$fixture_provider" \
    CONTROL_HISTORY_PROCESS_EXPECT_PHASE="$expected_phase" \
    env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
      go test ./deploy/acceptance/account-inventory-history-process \
      -run '^TestAccountInventoryHistoryProcessClaimRetainedUntilLeaseExpiry$' -count=1 \
      >"$runtime_directory/${label}-claim-retained.log" 2>&1; then
    fixed_failure "${label}_claim_not_retained"
  fi
}

verify_reconciler_recovered_claim() {
  local label="$1"
  if ! CONTROL_HISTORY_PROCESS_OWNER_DATABASE_URL="$CONTROL_HISTORY_PROCESS_MIGRATOR_URL" \
    CONTROL_HISTORY_PROCESS_INSTANCE_ID="$fixture_instance_id" \
    CONTROL_HISTORY_PROCESS_SUMMARY_DATE="$fixture_summary_date" \
    CONTROL_HISTORY_PROCESS_PROVIDER="$fixture_provider" \
    env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
      go test ./deploy/acceptance/account-inventory-history-process \
      -run '^TestAccountInventoryHistoryProcessReconcilerRecoveredClaim$' -count=1 \
      >"$runtime_directory/${label}-reconciler-recovery.log" 2>&1; then
    fixed_failure "${label}_reconciler_recovery_failed"
  fi
}

verify_held_transaction_timeout_atomicity() {
  local source_snapshots="${1:-0}"
  if ! CONTROL_HISTORY_PROCESS_OWNER_DATABASE_URL="$CONTROL_HISTORY_PROCESS_MIGRATOR_URL" \
    CONTROL_HISTORY_PROCESS_INSTANCE_ID="$fixture_instance_id" \
    CONTROL_HISTORY_PROCESS_SUMMARY_DATE="$fixture_summary_date" \
    CONTROL_HISTORY_PROCESS_PROVIDER="$fixture_provider" \
    CONTROL_HISTORY_PROCESS_SOURCE_SNAPSHOTS="$source_snapshots" \
    env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
      go test ./deploy/acceptance/account-inventory-history-process \
      -run '^TestAccountInventoryHistoryProcessHeldTransactionTimeoutIsAtomic$' -count=1 \
      >"$runtime_directory/held-transaction-timeout-atomicity.log" 2>&1; then
    if grep -Fq 'history process held transaction timeout run state invalid' \
      "$runtime_directory/held-transaction-timeout-atomicity.log"; then
      fixed_failure 'held_transaction_timeout_run_state_invalid'
    fi
    if grep -Fq 'history process held transaction timeout left partial history' \
      "$runtime_directory/held-transaction-timeout-atomicity.log"; then
      fixed_failure 'held_transaction_timeout_partial_history'
    fi
    if grep -Fq 'history process held transaction timeout audit invalid' \
      "$runtime_directory/held-transaction-timeout-atomicity.log"; then
      fixed_failure 'held_transaction_timeout_audit_invalid'
    fi
    fixed_failure 'held_transaction_timeout_atomicity_failed'
  fi
}

stop_control_with_held_transaction() {
  local release_during_grace="$1" attempts=120 exit_code=0
  kill -TERM "$control_pid" >/dev/null 2>&1 || fixed_failure 'held_transaction_sigterm_failed'
  if [ "$release_during_grace" = true ]; then
    sleep 0.2
    release_summary_lock
  fi
  while kill -0 "$control_pid" >/dev/null 2>&1; do
    attempts=$((attempts - 1))
    [ "$attempts" -gt 0 ] || fixed_failure 'held_transaction_stop_timeout'
    sleep 0.1
  done
  wait "$control_pid" >/dev/null 2>&1 || exit_code=$?
  control_pid=''
  [ "$exit_code" -eq 0 ] || fixed_failure 'held_transaction_control_exit_failed'
  if [ "$release_during_grace" = false ]; then
    release_summary_lock
  fi
}

run_held_transaction_shutdown_case() {
  local label="$1" instance_id="$2" age_days="$3" expected_phase="$4"
  local release_during_grace="$5" source_snapshots="${6:-0}"
  fixture_instance_id="$instance_id"
  seed_eligible_source "$age_days" "$source_snapshots"
  start_summary_lock "$label"
  start_enabled_control 1 10s 11s 20s 1 "$label"
  wait_for_control_summarize_lock
  verify_held_transaction_pool_exhaustion
  verify_pool_wait_is_bounded
  stop_control_with_held_transaction "$release_during_grace"
  if [ "$release_during_grace" = true ]; then
    verify_claim_retained "$expected_phase" "${label}-stopped"
    start_enabled_control 1 10s 11s 20s 1 "${label}_recovery"
    wait_for_history_ready
    verify_claim_retained "$expected_phase" "${label}-restarted"
    verify_enabled_convergence "$fixture_instance_id" "$fixture_summary_date" \
      "$fixture_provider" "$runtime_directory/${label}-convergence.log" \
      "${label}_convergence" 'false'
    verify_reconciler_recovered_claim "$label"
    stop_control
  else
    verify_held_transaction_timeout_atomicity "$source_snapshots"
    start_enabled_control 1 10s 11s 20s 2 "${label}_timeout_recovery"
    wait_for_history_ready
    verify_enabled_convergence "$fixture_instance_id" "$fixture_summary_date" \
      "$fixture_provider" "$runtime_directory/${label}-recovery-convergence.log" \
      "${label}_recovery_convergence" 'false' "$source_snapshots"
    stop_control
  fi
}

verify_enabled_convergence() {
  local instance_id="${1:-$fixture_instance_id}"
  local summary_date="${2:-$fixture_summary_date}"
  local provider="${3:-$fixture_provider}"
  local probe_log="${4:-$runtime_directory/enabled-convergence-probe.log}"
  local failure_prefix="${5:-enabled_convergence}"
  local restart_phase_matrix="${6:-false}"
  local source_snapshots="${7:-0}"
  if ! CONTROL_HISTORY_PROCESS_URL="http://127.0.0.1:${control_port}" \
    CONTROL_HISTORY_PROCESS_OWNER_DATABASE_URL="$CONTROL_HISTORY_PROCESS_MIGRATOR_URL" \
    CONTROL_HISTORY_PROCESS_INSTANCE_ID="$instance_id" \
    CONTROL_HISTORY_PROCESS_SUMMARY_DATE="$summary_date" \
    CONTROL_HISTORY_PROCESS_PROVIDER="$provider" \
    CONTROL_HISTORY_PROCESS_RESTART_PHASE_MATRIX="$restart_phase_matrix" \
    CONTROL_HISTORY_PROCESS_SOURCE_SNAPSHOTS="$source_snapshots" \
    env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
      go test ./deploy/acceptance/account-inventory-history-process \
      -run '^TestAccountInventoryHistoryProcessEnabledConvergesEligibleSource$' -count=1 \
      >"$probe_log" 2>&1; then
    for class in database_unavailable compaction_failed compaction_deleting compaction_summarized \
      compaction_pending compaction_missing rollup_failed rollup_pending rollup_missing metrics_missing \
      restart_phase_matrix
    do
      if grep -Fq "history process enabled convergence timed out class=${class}" \
        "$probe_log"; then
        fixed_failure "${failure_prefix}_${class}"
      fi
    done
    if grep -Fq 'history process convergence database read failed' \
      "$probe_log"; then
      fixed_failure "${failure_prefix}_database_read_failed"
    fi
    if grep -Fq 'history process metrics request invalid' "$probe_log"; then
      fixed_failure "${failure_prefix}_metrics_request_invalid"
    fi
    if grep -Fq 'history process metrics request failed' "$probe_log"; then
      fixed_failure "${failure_prefix}_metrics_request_failed"
    fi
    if grep -Fq 'history process metrics response failed' "$probe_log"; then
      fixed_failure "${failure_prefix}_metrics_response_failed"
    fi
    if grep -Fq 'history process metrics response invalid' "$probe_log"; then
      fixed_failure "${failure_prefix}_metrics_response_invalid"
    fi
    fixed_failure "${failure_prefix}_failed"
  fi
}

stop_postgres_and_wait_for_lease() {
  local checks=205
  if ! compose stop --timeout 1 postgres >"$runtime_directory/postgres-stop.log" 2>&1; then
    fixed_failure 'postgres_stop_failed'
  fi
  if [ -z "$control_pid" ] || ! kill -0 "$control_pid" >/dev/null 2>&1; then
    fixed_failure 'control_exited_during_postgres_stop'
  fi
  if compose exec -T postgres pg_isready --username relay_control_migrator \
    --dbname relay_station_control >/dev/null 2>&1; then
    fixed_failure 'postgres_outage_not_observed'
  fi
  while [ "$checks" -gt 0 ]; do
    if ! kill -0 "$control_pid" >/dev/null 2>&1; then
      fixed_failure 'control_exited_during_postgres_outage'
    fi
    sleep 0.1
    checks=$((checks - 1))
  done
}

restart_postgres() {
  local attempts=100
  if ! compose start postgres >"$runtime_directory/postgres-restart.log" 2>&1; then
    fixed_failure 'postgres_restart_failed'
  fi
  until compose exec -T postgres pg_isready --username relay_control_migrator \
    --dbname relay_station_control >/dev/null 2>&1
  do
    if ! kill -0 "$control_pid" >/dev/null 2>&1; then
      fixed_failure 'control_exited_during_postgres_restart'
    fi
    attempts=$((attempts - 1))
    [ "$attempts" -gt 0 ] || fixed_failure 'postgres_restart_timeout'
    sleep 0.1
  done
  set_database_urls
}

verify_redacted_log() {
  for forbidden in \
    'postgres://' \
    'relay_control_runtime_dev_only' \
    "$bootstrap_value" \
    "$keyring_value"
  do
    if grep -Fq -- "$forbidden" "$control_log"; then
      fixed_failure 'control_log_not_redacted'
    fi
  done
}

main() {
  local suffix dual_date_one dual_date_two
  trap cleanup EXIT
  trap 'exit 130' HUP INT TERM
  require_command docker
  require_command go
  require_command make
  require_command openssl
  require_command curl

  runtime_directory="$(mktemp -d "$temporary_root/relay-control-history-process.XXXXXX")"
  suffix="$(printf '%s' "${runtime_directory##*.}" | tr '[:upper:]' '[:lower:]')"
  project_name="relay-control-history-process-${suffix}"
  export CONTROL_HISTORY_PROCESS_PROJECT="$project_name"
  export CONTROL_HISTORY_PROCESS_DB_PORT="$database_host_port"
  control_binary="$runtime_directory/control"
  control_log="$runtime_directory/control.log"
  network_counter_binary="$runtime_directory/history-network-counter"
  network_counter_config="$runtime_directory/network-counter.json"
  network_counter_log="$runtime_directory/network-counter.log"
  bootstrap_file="$runtime_directory/bootstrap-secret"
  keyring_file="$runtime_directory/auth-keyring.json"

  if ! mkdir "$lock_directory" >/dev/null 2>&1; then
    fixed_failure 'control_port_lock_unavailable'
  fi
  lock_acquired=true
  write_secrets

  if ! compose up --detach --wait postgres >"$runtime_directory/compose-up.log" 2>&1; then
    fixed_failure 'postgres_start_failed'
  fi
  set_database_urls

  migrate_up
  assert_migration_and_seed_environment
  build_control
  start_network_counter
  require_process_test TestAccountInventoryHistoryProcessDisabledCompatibleMetrics
  require_process_test TestAccountInventoryHistoryProcessMetricsFailureIsolation
  require_process_test TestAccountInventoryHistoryProcessSeedEligibleSource
  require_process_test TestAccountInventoryHistoryProcessStaleFenceHasZeroImpact
  require_process_test TestAccountInventoryHistoryProcessSourceBackedRetentionCompleted
  require_process_test TestAccountInventoryHistoryProcessTerminalInternalPreservesSource
  require_process_test TestAccountInventoryHistoryProcessSeedClaimedForRestart
  require_process_test TestAccountInventoryHistoryProcessEnabledConvergesEligibleSource
  require_process_test TestAccountInventoryHistoryProcessHeldTransactionPoolExhaustion
  require_process_test TestAccountInventoryHistoryProcessClaimRetainedUntilLeaseExpiry
  require_process_test TestAccountInventoryHistoryProcessReconcilerRecoveredClaim
  require_process_test TestAccountInventoryHistoryProcessHeldTransactionTimeoutIsAtomic
  start_default_disabled_control
  verify_disabled_metrics
  set_metrics_snapshot_execute revoke
  verify_disabled_metrics_failure_isolation
  set_metrics_snapshot_execute grant
  stop_control
  seed_eligible_source
  start_enabled_control '' 1s 2s 5s 1 zero_source
  verify_enabled_convergence
  stop_control
  fixture_instance_id="$dual_fixture_one_instance_id"
  seed_eligible_source 12 2
  dual_date_one="$fixture_summary_date"
  fixture_instance_id="$dual_fixture_two_instance_id"
  seed_eligible_source 13 2
  dual_date_two="$fixture_summary_date"
  start_summary_lock dual-workers
  start_enabled_control 4 10s 11s 20s 2 dual_workers
  wait_for_control_summarize_lock 2
  release_summary_lock
  verify_enabled_convergence "$dual_fixture_one_instance_id" "$dual_date_one" \
    "$fixture_provider" "$runtime_directory/dual-worker-one-convergence.log" \
    'dual_worker_one_convergence' 'false' 2
  verify_enabled_convergence "$dual_fixture_two_instance_id" "$dual_date_two" \
    "$fixture_provider" "$runtime_directory/dual-worker-two-convergence.log" \
    'dual_worker_two_convergence' 'false' 2
  stop_control
  fixture_instance_id="$source_fixture_instance_id"
  seed_eligible_source 31 2
  verify_stale_fence_zero_impact
  start_summary_lock source-backed
  start_enabled_control 1 10s 11s 20s 2 source_restart
  wait_for_control_summarize_lock
  verify_pool_wait_is_bounded
  stop_postgres_and_wait_for_lease
  wait "$summary_lock_pid" >/dev/null 2>&1 || true
  summary_lock_pid=''
  restart_postgres
  verify_enabled_convergence "$fixture_instance_id" "$fixture_summary_date" \
    "$fixture_provider" "$runtime_directory/source-backed-convergence.log" \
    'source_backed_convergence' 'false' 2
  verify_source_backed_retention
  stop_control
  seed_claimed_for_restart
  start_enabled_control '' 1s 2s 5s 1 restart_control_one
  wait_for_history_ready
  assert_restart_claim_active
  stop_control
  start_enabled_control '' 1s 2s 5s 1 restart_control_two
  wait_for_history_ready
  assert_restart_claim_active
  stop_postgres_and_wait_for_lease
  restart_postgres
  verify_enabled_convergence "$restart_fixture_instance_id" "$restart_fixture_summary_date" \
    "$restart_fixture_provider" "$runtime_directory/restart-convergence-probe.log" \
    'postgres_restart_convergence' 'true'
  stop_control
  run_held_transaction_shutdown_case drain "$shutdown_drain_instance_id" 8 summarized true
  run_held_transaction_shutdown_case timeout "$shutdown_timeout_instance_id" 9 pending false 2
  fixture_instance_id="$permission_fixture_instance_id"
  start_enabled_control '' 10s 11s 20s 2 permission
  wait_for_history_ready
  set_history_execute summarize revoke
  seed_eligible_source 10 2
  verify_terminal_internal_source_preserved permission
  set_history_execute summarize grant
  stop_control
  fixture_instance_id="$trigger_fixture_instance_id"
  start_enabled_control '' 10s 11s 20s 2 trigger
  wait_for_history_ready
  set_summarize_trigger_fault create
  seed_eligible_source 11 2
  verify_terminal_internal_source_preserved trigger
  stop_control
  set_summarize_trigger_fault drop
  fixture_instance_id="$planner_fault_instance_id"
  start_enabled_control '' 10s 11s 20s 2 planner_fault 5s
  wait_for_history_ready
  seed_eligible_source 14 2
  set_history_execute planner revoke
  verify_terminal_internal_source_preserved planner planner
  set_history_execute planner grant
  stop_control
  fixture_instance_id="$rollup_fault_instance_id"
  start_enabled_control '' 10s 11s 20s 2 rollup_fault 5s
  wait_for_history_ready
  seed_eligible_source 15 2
  set_history_execute rollup revoke
  verify_terminal_internal_source_preserved rollup rollup
  set_history_execute rollup grant
  stop_control
  fixture_instance_id="$retention_fault_instance_id"
  start_enabled_control '' 10s 11s 20s 2 retention_fault 5s
  wait_for_history_ready
  seed_eligible_source 16 2
  set_history_execute retention revoke
  verify_terminal_internal_source_preserved retention retention
  set_history_execute retention grant
  stop_control
  verify_redacted_log
  stop_network_counter
  strict_cleanup
  echo 'account_inventory_history_process=success migration=9 default_disabled=covered metrics_http=covered metrics_failure_isolation=covered enabled_zero_source=covered source_backed_snapshots=2 history_concurrency=2 dual_workers_observed=2 stale_fence_zero_impact=covered source_backed_retention=covered source_backed_postgres_restart=covered source_backed_max_conns_1=covered source_backed_statement_timeout_recovery=covered held_sql_sigterm_drain=covered held_sql_statement_timeout_atomicity=covered max_conns_1_pool_wait=covered unexpired_lease=preserved reconciler_restart_takeover=covered postgres_restart_recovery=covered control_postgres_restart_phase_matrix=covered permission_terminal_internal=covered trigger_terminal_internal=covered planner_runtime_stopped=covered rollup_runtime_stopped=covered retention_runtime_stopped=covered terminal_source_preserved=covered sigterm_exit=bounded log_redaction=covered fake_network_counter=covered external_requests=0 node=0 gateway=0 prometheus=0 internet=0 model=0 cleanup_containers=0 cleanup_volumes=0 cleanup_networks=0 cleanup_temp=0 cleanup_lock=0'
}

cd "$repository_root"
main
