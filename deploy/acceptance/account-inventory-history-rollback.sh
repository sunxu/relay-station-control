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
temporary_root="${TMPDIR:-/tmp}"
temporary_root="${temporary_root%/}"
runtime_directory=''
project_name=''
network_name=''
host_network_name=''
fake_node_name=''
old_control_name=''
fake_node_active=false
old_control_active=false
lock_directory=''
lock_acquired=false
bootstrap_value=''
keyring_value=''
management_key=''
fake_token=''
session_value=''
csrf_value=''
old_revision='d4310023b3128199e485670d4bde84da7607412f'
runtime_image='alpine:3.22@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce'

fixed_failure() {
  echo "account_inventory_history_rollback=failed reason=$1" >&2
  exit 1
}

compose() {
  env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    docker compose --project-name "$project_name" --file "$compose_file" "$@"
}

docker_no_proxy() {
  env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    docker "$@"
}

cleanup() {
  local exit_code=$?
  trap - EXIT HUP INT TERM
  if [ -n "$old_control_name" ]; then
    docker_no_proxy rm --force "$old_control_name" >/dev/null 2>&1 || true
  fi
  if [ -n "$fake_node_name" ]; then
    docker_no_proxy rm --force "$fake_node_name" >/dev/null 2>&1 || true
  fi
  if [ -n "$project_name" ]; then
    compose down --volumes --remove-orphans >/dev/null 2>&1 || true
  fi
  case "$runtime_directory" in
    "$temporary_root"/relay-control-history-rollback.*)
      rm -rf -- "$runtime_directory"
      ;;
  esac
  if [ "$lock_acquired" = true ]; then
    rmdir "$lock_directory" >/dev/null 2>&1 || true
  fi
  bootstrap_value=''
  keyring_value=''
  management_key=''
  fake_token=''
  session_value=''
  csrf_value=''
  return "$exit_code"
}

strict_cleanup() {
  local residual
  if docker_no_proxy container inspect "$old_control_name" >/dev/null 2>&1; then
    fixed_failure 'cleanup_old_control_residual'
  fi
  if docker_no_proxy container inspect "$fake_node_name" >/dev/null 2>&1; then
    fixed_failure 'cleanup_fake_node_residual'
  fi
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
    "$temporary_root"/relay-control-history-rollback.*)
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
  management_key=''
  fake_token=''
  session_value=''
  csrf_value=''
  trap - EXIT HUP INT TERM
}

build_binaries() {
  local old_source="$runtime_directory/old-source"
  mkdir -p "$old_source"
  git cat-file -e "${old_revision}^{commit}" >/dev/null 2>&1 \
    || fixed_failure 'old_revision_unavailable'
  git archive --format=tar --output="$runtime_directory/old-source.tar" "$old_revision" \
    >/dev/null 2>&1 || fixed_failure 'old_revision_export_failed'
  tar -xf "$runtime_directory/old-source.tar" -C "$old_source" \
    || fixed_failure 'old_revision_export_failed'
  if ! env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    GOCACHE="$runtime_directory/go-build" go build -trimpath \
      -o "$runtime_directory/history-harness" ./deploy/acceptance/account-inventory-history-rollback \
      >"$runtime_directory/current-harness-build.log" 2>&1; then
    fixed_failure 'current_harness_build_failed'
  fi
  if ! (
    cd "$old_source"
    env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
      CGO_ENABLED=0 GOOS=linux GOCACHE="$runtime_directory/go-build" go build -trimpath \
        -o "$runtime_directory/control-old" ./cmd/control
  ) >"$runtime_directory/old-control-build.log" 2>&1; then
    fixed_failure 'old_control_build_failed'
  fi
  if ! env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    CGO_ENABLED=0 GOOS=linux GOCACHE="$runtime_directory/go-build" go build -trimpath \
      -o "$runtime_directory/history-fake-node" ./deploy/acceptance/account-inventory-history-fake-node \
      >"$runtime_directory/fake-node-build.log" 2>&1; then
    fixed_failure 'fake_node_build_failed'
  fi
  chmod 0555 "$runtime_directory/history-harness" "$runtime_directory/control-old" \
    "$runtime_directory/history-fake-node"
}

prepare_auth_files() {
  bootstrap_value="$(openssl rand -hex 32 2>/dev/null)" || fixed_failure 'secret_generation_failed'
  keyring_value="$(openssl rand -base64 32 2>/dev/null | tr -d '\r\n=')" \
    || fixed_failure 'secret_generation_failed'
  management_key="$(openssl rand -hex 32 2>/dev/null)" || fixed_failure 'secret_generation_failed'
  fake_token="$(openssl rand -hex 32 2>/dev/null)" || fixed_failure 'secret_generation_failed'
  mkdir "$runtime_directory/control" "$runtime_directory/fake"
  printf '%s\n' "$bootstrap_value" >"$runtime_directory/control/bootstrap-secret"
  printf '{"format_version":1,"environment":"dev","current":1,"keys":[{"version":1,"key":"%s"}]}\n' \
    "$keyring_value" >"$runtime_directory/control/auth-keyring.json"
  printf '%s\n' "$management_key" >"$runtime_directory/control/management-key"
  printf '%s\n' \
    '{"provider":"file","references":[{"reference":"file://history-forward/management-key","path":"/run/history/management-key"}]}' \
    >"$runtime_directory/control/secret-mapping.json"
  printf '{"listen":"0.0.0.0:8081","management_key":"%s","email":"history-rollback@example.invalid","token":"%s"}\n' \
    "$management_key" "$fake_token" >"$runtime_directory/fake/config.json"
  chmod 0700 "$runtime_directory/control" "$runtime_directory/fake"
  chmod 0400 "$runtime_directory/control/bootstrap-secret" \
    "$runtime_directory/control/auth-keyring.json" "$runtime_directory/control/management-key" \
    "$runtime_directory/control/secret-mapping.json" "$runtime_directory/fake/config.json"
}

start_fake_node() {
  local attempts=100
  docker_no_proxy run --detach --name "$fake_node_name" --network "$network_name" \
    --user "$(id -u):$(id -g)" \
    --read-only --tmpfs /tmp:rw,noexec,nosuid,size=16m --cap-drop ALL \
    --security-opt no-new-privileges:true \
    --mount "type=bind,source=$runtime_directory/history-fake-node,target=/usr/local/bin/history-fake-node,readonly" \
    --mount "type=bind,source=$runtime_directory/fake,target=/run/history,readonly" \
    "$runtime_image" /usr/local/bin/history-fake-node -config /run/history/config.json \
    >"$runtime_directory/fake-node-start.log" 2>&1 || fixed_failure 'fake_node_start_failed'
  fake_node_active=true
  until docker_no_proxy logs "$fake_node_name" 2>&1 \
    | grep -Fqx 'account_inventory_history_fake_node=ready'; do
    if [ "$(docker_no_proxy inspect "$fake_node_name" --format '{{.State.Running}}' 2>/dev/null)" != true ]; then
      fixed_failure 'fake_node_exited'
    fi
    attempts=$((attempts - 1))
    [ "$attempts" -gt 0 ] || fixed_failure 'fake_node_start_timeout'
    sleep 0.1
  done
}

start_old_control() {
  local attempts=100 control_port="$1" node_ip="$2" phase
  phase=$(($(date -u '+%s') % 300))
  if [ "$phase" -gt 270 ]; then
    sleep $((301 - phase))
  fi
  date -u '+%Y-%m-%dT%H:%M:%S.000000000Z' >"$CONTROL_HISTORY_ROLLBACK_PROCESS_STARTED_FILE"
  chmod 0400 "$CONTROL_HISTORY_ROLLBACK_PROCESS_STARTED_FILE"
  docker_no_proxy run --detach --name "$old_control_name" --network "$network_name" \
    --user "$(id -u):$(id -g)" \
    --publish "127.0.0.1:${control_port}:8080" \
    --read-only --tmpfs /tmp:rw,noexec,nosuid,size=16m --cap-drop ALL \
    --security-opt no-new-privileges:true \
    --mount "type=bind,source=$runtime_directory/control-old,target=/usr/local/bin/control-old,readonly" \
    --mount "type=bind,source=$runtime_directory/control,target=/run/history,readonly" \
    -e HTTP_PROXY= -e HTTPS_PROXY= -e ALL_PROXY= \
    -e http_proxy= -e https_proxy= -e all_proxy= -e NO_PROXY='*' -e no_proxy='*' \
    -e CONTROL_HTTP_ADDR=0.0.0.0:8080 \
    -e CONTROL_ENVIRONMENT_ID=history-forward -e CONTROL_ENVIRONMENT=dev \
    -e CONTROL_COOKIE_SECURE=true -e CONTROL_MFA_REQUIRED=false \
    -e CONTROL_BOOTSTRAP_SECRET_FILE=/run/history/bootstrap-secret \
    -e CONTROL_AUTH_KEYRING_FILE=/run/history/auth-keyring.json \
    -e CONTROL_PROVIDER_POLICY_MUTATION_ENABLED=false \
    -e CONTROL_ACCOUNT_INVENTORY_LIFECYCLE_ENABLED=true \
    -e CONTROL_ACCOUNT_INVENTORY_POLL_ENABLED=true \
    -e CONTROL_ACCOUNT_INVENTORY_POLL_MAX_NODES=1 \
    -e CONTROL_ACCOUNT_INVENTORY_POLL_CONCURRENCY=1 \
    -e CONTROL_ACCOUNT_INVENTORY_POLL_START_GRACE=299s \
    -e CONTROL_ACCOUNT_INVENTORY_POLL_REQUEST_TIMEOUT=1s \
    -e CONTROL_ACCOUNT_INVENTORY_POLL_LEASE=15s \
    -e CONTROL_ACCOUNT_INVENTORY_POLL_MAX_ATTEMPTS=1 \
    -e CONTROL_ACCOUNT_INVENTORY_POLL_SCHEDULER_INTERVAL=100ms \
    -e CONTROL_ACCOUNT_INVENTORY_POLL_WORKER_SCAN_INTERVAL=100ms \
    -e CONTROL_ACCOUNT_INVENTORY_POLL_RECONCILE_INTERVAL=1s \
    -e CONTROL_ACCOUNT_INVENTORY_POLL_DATABASE_BACKOFF_INITIAL=100ms \
    -e CONTROL_ACCOUNT_INVENTORY_POLL_DATABASE_BACKOFF_MAXIMUM=1s \
    -e CONTROL_ACCOUNT_INVENTORY_POLL_SHUTDOWN_GRACE=5s \
    -e CONTROL_CLIPROXYAPI_DRIVER_ENABLED=true \
    -e CONTROL_CLIPROXYAPI_CONNECT_TIMEOUT=1s \
    -e CONTROL_CLIPROXYAPI_REQUEST_TIMEOUT=1s \
    -e CONTROL_CLIPROXYAPI_MANAGEMENT_DNS=history-fake.invalid \
    -e "CONTROL_CLIPROXYAPI_MANAGEMENT_CIDRS=${node_ip}/32" \
    -e "CONTROL_CLIPROXYAPI_PLAIN_HTTP_CIDRS=${node_ip}/32" \
    -e CONTROL_CLIPROXYAPI_SECRET_MAPPING_FILE=/run/history/secret-mapping.json \
    -e 'DATABASE_URL=postgres://relay_control_app_dev:relay_control_runtime_dev_only@postgres:5432/relay_station_control?sslmode=disable' \
    "$runtime_image" /usr/local/bin/control-old \
    >"$runtime_directory/control-old-start.log" 2>&1 || fixed_failure 'old_control_start_failed'
  old_control_active=true
  docker_no_proxy network connect "$host_network_name" "$old_control_name" \
    || fixed_failure 'old_control_host_network_failed'
  until curl --noproxy '*' --silent --show-error --fail \
    "http://127.0.0.1:${control_port}/api/healthz" >/dev/null 2>&1; do
    if [ "$(docker_no_proxy inspect "$old_control_name" --format '{{.State.Running}}' 2>/dev/null)" != true ]; then
      fixed_failure 'old_control_exited'
    fi
    attempts=$((attempts - 1))
    [ "$attempts" -gt 0 ] || fixed_failure 'old_control_start_timeout'
    sleep 0.1
  done
}

stop_old_control() {
  local exit_code
  docker_no_proxy stop --time 10 "$old_control_name" >/dev/null \
    || fixed_failure 'old_control_sigterm_failed'
  docker_no_proxy logs "$old_control_name" >"$runtime_directory/control-old.log" 2>&1 \
    || fixed_failure 'old_control_log_failed'
  exit_code="$(docker_no_proxy inspect "$old_control_name" --format '{{.State.ExitCode}}')" \
    || fixed_failure 'old_control_inspect_failed'
  [ "$exit_code" -eq 0 ] || fixed_failure 'old_control_exit_failed'
  docker_no_proxy rm "$old_control_name" >/dev/null || fixed_failure 'old_control_remove_failed'
  old_control_active=false
}

stop_fake_node() {
  local exit_code expected
  docker_no_proxy stop --time 10 "$fake_node_name" >/dev/null \
    || fixed_failure 'fake_node_sigterm_failed'
  docker_no_proxy logs "$fake_node_name" >"$runtime_directory/fake-node.log" 2>&1 \
    || fixed_failure 'fake_node_log_failed'
  exit_code="$(docker_no_proxy inspect "$fake_node_name" --format '{{.State.ExitCode}}')" \
    || fixed_failure 'fake_node_inspect_failed'
  [ "$exit_code" -eq 0 ] || fixed_failure 'fake_node_exit_failed'
  docker_no_proxy rm "$fake_node_name" >/dev/null || fixed_failure 'fake_node_remove_failed'
  fake_node_active=false
  expected='account_inventory_history_fake_node=stopped total=1 health=0 inventory=1 unauthorized=0 rejected=0'
  [ "$(grep -Fxc "$expected" "$runtime_directory/fake-node.log")" -eq 1 ] \
    || fixed_failure 'fake_node_request_count_invalid'
}

main() {
  local suffix port published control_port node_ip
  trap cleanup EXIT
  trap 'exit 130' HUP INT TERM
  for command_name in curl date docker git go make openssl sed tar tr; do
    command -v "$command_name" >/dev/null 2>&1 || fixed_failure 'required_command_unavailable'
  done
  runtime_directory="$(mktemp -d "$temporary_root/relay-control-history-rollback.XXXXXX")"
  suffix="$(printf '%s' "${runtime_directory##*.}" | tr '[:upper:]' '[:lower:]')"
  project_name="relay-control-history-rollback-${suffix}"
  network_name="${project_name}_isolated"
  host_network_name="${project_name}_host-access"
  fake_node_name="${project_name}-fake-node"
  old_control_name="${project_name}-old-control"
  export CONTROL_HISTORY_POSTGRES_PROJECT="$project_name"
  control_port=$((24000 + ($$ % 10000)))
  lock_directory="$temporary_root/relay-control-history-rollback-${control_port}.lock"
  mkdir "$lock_directory" >/dev/null 2>&1 || fixed_failure 'control_port_lock_unavailable'
  lock_acquired=true

  compose up --detach --wait postgres >"$runtime_directory/compose-up.log" 2>&1 \
    || fixed_failure 'postgres_start_failed'
  docker_no_proxy network inspect "$network_name" >/dev/null 2>&1 \
    || fixed_failure 'isolated_network_unavailable'
  docker_no_proxy image inspect "$runtime_image" >/dev/null 2>&1 \
    || fixed_failure 'runtime_image_unavailable'
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
  start_fake_node
  node_ip="$(docker_no_proxy inspect "$fake_node_name" \
    --format "{{with index .NetworkSettings.Networks \"${network_name}\"}}{{.IPAddress}}{{end}}" 2>/dev/null)" \
    || fixed_failure 'fake_node_address_unavailable'
  case "$node_ip" in
    [0-9]*.[0-9]*.[0-9]*.[0-9]*) ;;
    *) fixed_failure 'fake_node_address_invalid' ;;
  esac
  export CONTROL_HISTORY_ROLLBACK_NODE_ENDPOINT="http://${node_ip}:8081"
  export CONTROL_HISTORY_ROLLBACK_NODE_SECRET_REFERENCE='file://history-forward/management-key'
  export CONTROL_HISTORY_ROLLBACK_KEYRING_FILE="$runtime_directory/control/auth-keyring.json"
  export CONTROL_HISTORY_ROLLBACK_SESSION_FILE="$runtime_directory/session.json"
  export CONTROL_HISTORY_ROLLBACK_PROCESS_STARTED_FILE="$runtime_directory/process-started-at"
  export CONTROL_HISTORY_ROLLBACK_CONTROL_URL="http://127.0.0.1:${control_port}"
  if ! "$runtime_directory/history-harness" prepare >"$runtime_directory/prepare.log" 2>&1; then
    grep -E '^account_inventory_history_rollback_harness=failed reason=prepare_[A-Za-z0-9_]+$' \
      "$runtime_directory/prepare.log" >&2 || true
    fixed_failure 'forward_state_prepare_failed'
  fi
  session_value="$(sed -n 's/^.*"session":"\([^"]*\)".*$/\1/p' "$CONTROL_HISTORY_ROLLBACK_SESSION_FILE")"
  csrf_value="$(sed -n 's/^.*"csrf":"\([^"]*\)".*$/\1/p' "$CONTROL_HISTORY_ROLLBACK_SESSION_FILE")"
  [ -n "$session_value" ] && [ -n "$csrf_value" ] || fixed_failure 'http_session_file_invalid'
  start_old_control "$control_port" "$node_ip"
  "$runtime_directory/history-harness" wait >"$runtime_directory/wait.log" 2>&1 \
    || fixed_failure 'old_control_poll_failed'
  "$runtime_directory/history-harness" http-query >"$runtime_directory/http-query.log" 2>&1 \
    || fixed_failure 'old_control_http_query_failed'
  stop_old_control
  stop_fake_node
  "$runtime_directory/history-harness" verify >"$runtime_directory/verify.log" 2>&1 \
    || fixed_failure 'history_changed_during_old_control'
  for forbidden in 'postgres://' 'relay_control_runtime_dev_only' "$bootstrap_value" "$keyring_value" \
    "$management_key" "$fake_token" "$session_value" "$csrf_value" 'history-rollback@example.invalid'; do
    if grep -Fq -- "$forbidden" "$runtime_directory"/*.log; then
      fixed_failure 'old_control_log_not_redacted'
    fi
  done
  strict_cleanup
  echo 'account_inventory_history_rollback=success migration=9 pinned_old_revision=covered snapshot_cleanup=controlled poll_cleanup=controlled current_fk_null=covered old_control_poll_promotion=covered old_control_http_current_query=covered history_and_history_audit_unchanged=covered fake_node_inventory_requests=1 production_down=not_used cleanup_containers=0 cleanup_volumes=0 cleanup_networks=0 cleanup_temp=0 cleanup_lock=0'
}

cd "$repository_root"
main
