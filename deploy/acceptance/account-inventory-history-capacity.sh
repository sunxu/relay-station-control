#!/usr/bin/env bash
set -euo pipefail
umask 077

unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
export NO_PROXY='*' no_proxy='*'
export GOPROXY='https://goproxy.cn,direct'

case "${1:-}" in
  smoke|864000|1152000) scale="$1" ;;
  *) echo 'account_inventory_history_capacity=failed reason=invalid_scale' >&2; exit 1 ;;
esac
[ "$#" -eq 1 ] || { echo 'account_inventory_history_capacity=failed reason=invalid_arguments' >&2; exit 1; }

script_directory="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
repository_root="$(CDPATH='' cd -- "$script_directory/../.." && pwd)"
compose_file="$script_directory/account-inventory-history-capacity.compose.yaml"
runtime_directory=''
project_name=''

fixed_failure() {
  echo "account_inventory_history_capacity=failed reason=$1" >&2
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
  case "${runtime_directory##*/}" in
    relay-control-history-capacity.*) rm -rf -- "$runtime_directory" ;;
  esac
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
  case "${runtime_directory##*/}" in
    relay-control-history-capacity.*) rm -rf -- "$runtime_directory" ;;
    *) fixed_failure 'cleanup_runtime_path_invalid' ;;
  esac
  [ ! -e "$runtime_directory" ] || fixed_failure 'cleanup_runtime_residual'
  trap - EXIT HUP INT TERM
}

main() {
  local published_port port container_id oom evidence_count postgres_peak_bytes
  command -v docker >/dev/null 2>&1 || fixed_failure 'required_command_unavailable'
  command -v go >/dev/null 2>&1 || fixed_failure 'required_command_unavailable'
  runtime_directory="$(mktemp -d -t relay-control-history-capacity.XXXXXX)"
  project_name="relay-control-history-capacity-$(printf '%s' "${runtime_directory##*.}" | tr '[:upper:]' '[:lower:]')"
  export CONTROL_HISTORY_POSTGRES_PROJECT="$project_name"
  trap cleanup EXIT
  trap 'exit 130' HUP INT TERM

  compose up --detach --wait postgres >"$runtime_directory/compose-up.log" 2>&1 \
    || fixed_failure 'postgres_start_failed'
  container_id="$(compose ps --quiet postgres)" || fixed_failure 'postgres_container_unavailable'
  [ -n "$container_id" ] || fixed_failure 'postgres_container_unavailable'
  published_port="$(compose port postgres 5432 2>/dev/null)" \
    || fixed_failure 'postgres_port_discovery_failed'
  case "$published_port" in
    127.0.0.1:[0-9]*) port="${published_port##*:}" ;;
    *) fixed_failure 'postgres_port_invalid' ;;
  esac
  export CONTROL_DATABASE_TEST_URL="postgres://relay_control_migrator:relay_control_migrator_dev_only@127.0.0.1:${port}/relay_station_control?sslmode=disable"
  export CONTROL_RUNTIME_DATABASE_TEST_URL="postgres://relay_control_app_dev:relay_control_runtime_dev_only@127.0.0.1:${port}/relay_station_control?sslmode=disable"

  if ! (
    cd "$repository_root"
    env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
      CONTROL_HISTORY_CAPACITY_SCALE="$scale" \
      go test ./internal/store -run '^TestAccountInventoryHistoryCapacityAcceptance$' \
        -count=1 -v
  ) >"$runtime_directory/capacity.log" 2>&1; then
    oom="$(env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
      docker inspect --format '{{.State.OOMKilled}}' "$container_id" 2>/dev/null || true)"
    [ "$oom" != true ] || fixed_failure 'postgres_oom'
    if [ "${CONTROL_HISTORY_CAPACITY_KEEP_LOGS:-}" = "1" ]; then
      cp "$runtime_directory/capacity.log" /tmp/history-capacity-failed.log >/dev/null 2>&1 || true
    fi
    fixed_failure 'capacity_gate_failed'
  fi
  oom="$(env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    docker inspect --format '{{.State.OOMKilled}}' "$container_id" 2>/dev/null)" \
    || fixed_failure 'postgres_oom_inspection_failed'
  [ "$oom" = false ] || fixed_failure 'postgres_oom'

  evidence_count="$(grep -c 'history_capacity_evidence=' "$runtime_directory/capacity.log" || true)"
  [ "$evidence_count" -eq 1 ] || fixed_failure 'capacity_evidence_invalid'
  postgres_peak_bytes="$(compose exec -T postgres sh -c '
    if [ -r /sys/fs/cgroup/memory.peak ]; then
      cat /sys/fs/cgroup/memory.peak
    else
      cat /sys/fs/cgroup/memory/memory.max_usage_in_bytes
    fi
  ' 2>/dev/null | tr -d '\r')" || fixed_failure 'postgres_peak_memory_unavailable'
  case "$postgres_peak_bytes" in
    ''|*[!0-9]*) fixed_failure 'postgres_peak_memory_invalid' ;;
  esac
  [ "$postgres_peak_bytes" -gt 0 ] || fixed_failure 'postgres_peak_memory_invalid'
  sed -n 's/^.*history_capacity_evidence=/history_capacity_evidence=/p' "$runtime_directory/capacity.log"
  echo "history_capacity_postgres_peak_bytes=$postgres_peak_bytes"
  strict_cleanup
  if [ "$scale" = smoke ]; then
    echo 'account_inventory_history_capacity=success scale=smoke evidence=not_evidence cleanup_containers=0 cleanup_volumes=0 cleanup_networks=0 cleanup_temp=0 cleanup_lock=0'
  else
    echo "account_inventory_history_capacity=success scale=$scale evidence=full cleanup_containers=0 cleanup_volumes=0 cleanup_networks=0 cleanup_temp=0 cleanup_lock=0"
  fi
}

main
