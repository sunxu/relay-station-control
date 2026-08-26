#!/usr/bin/env bash
set -euo pipefail

# This leaf gate owns the shared CLIProxyAPI lock. A failed or interrupted run
# intentionally leaves the lock behind: the result is then unknown until an
# operator investigates the failed run and removes the stale lock.
unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
export NO_PROXY='*' no_proxy='*'
export GOPROXY="${CONTROL_SNAPSHOT_ACCEPTANCE_GOPROXY:-https://goproxy.cn,direct}"

script_directory="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
repository_root="$(CDPATH='' cd -- "$script_directory/../.." && pwd)"
official_image='eceasy/cli-proxy-api@sha256:7f598ce64478a8a5f90ed76875e0e9b0e7d77b80e17184b13df18c3d5bdb3def'
postgres_image='postgres:18.6-alpine@sha256:d3e1620b530c944afa6e887d22eb899824da68e19c52024bf98f5220c88a65b2'
golang_image='golang:1.27.0-alpine@sha256:4c9fe60190a2a3350ddc51de80d0224b8a6698d12bdfc999fee45ea9d6c46dbc'
expected_result='account_inventory_snapshot_official_runtime=success image_version=v7.2.141 mode=runtime request_count=1 request_wait_seconds=10 snapshot_items=1 provider_states=1 promotion_applied=1 management_writes=0 probe_requests=0 gateway_requests=0'
lock_directory="${CONTROL_DRIVER_SMOKE_LOCK_DIR:-${TMPDIR:-/tmp}/relay-control-cliproxyapi-smoke.lock}"
runtime_directory=''
network_name=''
node_name=''
postgres_name=''
harness_name=''
postgres_volume=''
lock_acquired=false
lock_releasable=false

fixed_failure() {
  echo "account_inventory_snapshot_official_runtime=failed reason=$1" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || fixed_failure 'required_command_unavailable'
}

cleanup() {
  local exit_code=$?
  trap - EXIT HUP INT TERM
  if [ -n "$harness_name" ]; then
    docker rm -f "$harness_name" >/dev/null 2>&1 || true
  fi
  if [ -n "$node_name" ]; then
    docker rm -f "$node_name" >/dev/null 2>&1 || true
  fi
  if [ -n "$postgres_name" ]; then
    docker rm -f "$postgres_name" >/dev/null 2>&1 || true
  fi
  if [ -n "$network_name" ]; then
    docker network rm "$network_name" >/dev/null 2>&1 || true
  fi
  if [ -n "$postgres_volume" ]; then
    docker volume rm "$postgres_volume" >/dev/null 2>&1 || true
  fi
  case "$runtime_directory" in
    /tmp/relay-control-snapshot-container.*|/private/tmp/relay-control-snapshot-container.*)
      rm -rf -- "$runtime_directory"
      ;;
  esac
  if [ "$lock_acquired" = true ] && [ "$lock_releasable" = true ]; then
    rmdir "$lock_directory" >/dev/null 2>&1 || true
  fi
  return "$exit_code"
}

wait_for_postgres() {
  local attempts=60
  until docker exec "$postgres_name" pg_isready \
    --username relay_control_migrator --dbname relay_station_control >/dev/null 2>&1
  do
    attempts=$((attempts - 1))
    if [ "$attempts" -eq 0 ]; then
      fixed_failure 'postgres_unavailable'
    fi
    sleep 1
  done
}

classify_official_image_exit() {
  local exit_code
  if docker logs "$node_name" 2>&1 | grep -qi 'exec format error'; then
    fixed_failure 'official_image_architecture_invalid'
  fi
  if docker logs "$node_name" 2>&1 | grep -qi 'permission denied'; then
    fixed_failure 'official_image_permission_denied'
  fi
  if docker logs "$node_name" 2>&1 | grep -qi 'read-only file system'; then
    fixed_failure 'official_image_read_only_violation'
  fi
  if docker logs "$node_name" 2>&1 | grep -Eqi '(invalid|failed|error).{0,32}config|config.{0,32}(invalid|failed|error)'; then
    fixed_failure 'official_image_config_invalid'
  fi
  exit_code="$(docker inspect "$node_name" --format '{{.State.ExitCode}}' 2>/dev/null)"
  case "$exit_code" in
    126|127) fixed_failure 'official_image_command_unavailable' ;;
    137) fixed_failure 'official_image_resource_exhausted' ;;
    *) fixed_failure 'official_image_exited' ;;
  esac
}

main() {
  local suffix module_cache harness_log migration_log

  trap cleanup EXIT
  trap 'exit 130' HUP INT TERM
  umask 077

  if ! mkdir "$lock_directory" 2>/dev/null; then
    fixed_failure 'concurrent_or_stale_global_lock'
  fi
  lock_acquired=true

  require_command docker
  require_command go
  if ! docker image inspect "$official_image" >/dev/null 2>&1; then
    fixed_failure 'official_image_unavailable'
  fi
  if ! docker image inspect "$postgres_image" >/dev/null 2>&1; then
    fixed_failure 'postgres_image_unavailable'
  fi
  module_cache="$(go env GOMODCACHE)"
  if [ ! -d "$module_cache" ]; then
    fixed_failure 'module_cache_unavailable'
  fi

  runtime_directory="$(mktemp -d /tmp/relay-control-snapshot-container.XXXXXX)"
  suffix="$$"
  network_name="relay-control-snapshot-${suffix}"
  node_name="relay-control-snapshot-node-${suffix}"
  postgres_name="relay-control-snapshot-postgres-${suffix}"
  harness_name="relay-control-snapshot-harness-${suffix}"
  postgres_volume="relay-control-snapshot-postgres-${suffix}"
  harness_log="$runtime_directory/harness.log"
  migration_log="$runtime_directory/migration.log"

  mkdir "$runtime_directory/auth"
  printf '%s\n' \
    '{"type":"antigravity","email":"snapshot-runtime@example.invalid"}' \
    >"$runtime_directory/auth/runtime-fixture.json"
  printf '%s\n' \
    'host: "0.0.0.0"' \
    'port: 8317' \
    'remote-management:' \
    '  allow-remote: true' \
    '  secret-key: "snapshot-acceptance-management-key"' \
    '  disable-control-panel: true' \
    'auth-dir: "/run/snapshot-auth"' \
    'api-keys: ["snapshot-acceptance-api-key"]' \
    'debug: false' \
    'logging-to-file: false' \
    'usage-statistics-enabled: false' \
    'proxy-url: ""' >"$runtime_directory/config.yaml"
  printf '%s\n' 'snapshot-acceptance-management-key' >"$runtime_directory/management-key"
  printf '%s\n' \
    '{"provider":"file","references":[{"reference":"file://snapshot-acceptance/management-key","path":"/run/snapshot/management-key"}]}' \
    >"$runtime_directory/mapping.json"
  chmod 0700 "$runtime_directory/auth"
  chmod 0600 \
    "$runtime_directory/auth/runtime-fixture.json" \
    "$runtime_directory/config.yaml" \
    "$runtime_directory/management-key" \
    "$runtime_directory/mapping.json"

  if ! docker network create --internal "$network_name" >/dev/null 2>&1; then
    fixed_failure 'internal_network_create_failed'
  fi
  if ! docker volume create "$postgres_volume" >/dev/null 2>&1; then
    fixed_failure 'postgres_volume_create_failed'
  fi
  if ! docker run -d \
    --name "$postgres_name" \
    --network "$network_name" \
    --security-opt no-new-privileges:true \
    --tmpfs /tmp:rw,noexec,nosuid,size=64m \
    -e POSTGRES_DB=relay_station_control \
    -e POSTGRES_USER=relay_control_migrator \
    -e POSTGRES_PASSWORD=relay_control_migrator_dev_only \
    -e TZ=UTC -e PGTZ=UTC \
    -v "$postgres_volume:/var/lib/postgresql" \
    -v "$repository_root/deploy/postgres/init:/docker-entrypoint-initdb.d:ro" \
    "$postgres_image" postgres -c max_connections=24 >/dev/null 2>&1
  then
    fixed_failure 'postgres_start_failed'
  fi
  wait_for_postgres

  if ! docker run --rm \
    --network "$network_name" \
    --user "$(id -u):$(id -g)" \
    --workdir /src/tools \
    --read-only \
    --tmpfs /tmp:rw,exec,nosuid,size=2048m \
    --cap-drop ALL \
    --security-opt no-new-privileges:true \
    -v "$repository_root:/src:ro" \
    -v "$module_cache:/go/pkg/mod:ro" \
    -e HTTP_PROXY= -e HTTPS_PROXY= -e ALL_PROXY= \
    -e http_proxy= -e https_proxy= -e all_proxy= \
    -e NO_PROXY='*' -e no_proxy='*' -e GOPROXY=off -e GOCACHE=/tmp/go-build \
    "$golang_image" \
    go tool goose -dir ../migrations postgres \
      "postgres://relay_control_migrator:relay_control_migrator_dev_only@${postgres_name}:5432/relay_station_control?sslmode=disable" up \
    >"$migration_log" 2>&1
  then
    fixed_failure 'migration_failed'
  fi

  if ! docker run -d \
    --name "$node_name" \
    --network "$network_name" \
    --read-only \
    --tmpfs /tmp:rw,noexec,nosuid,size=16m \
    --cap-drop ALL \
    --security-opt no-new-privileges:true \
    -v "$runtime_directory/config.yaml:/CLIProxyAPI/snapshot-config.yaml:ro" \
    -v "$runtime_directory/auth:/run/snapshot-auth:ro" \
    "$official_image" ./CLIProxyAPI -config /CLIProxyAPI/snapshot-config.yaml -local-model >/dev/null 2>&1
  then
    fixed_failure 'official_image_start_failed'
  fi

  local ready=false
  for _ in $(seq 1 240); do
    if docker logs "$node_name" 2>&1 | grep -q 'API server started successfully'; then
      ready=true
      break
    fi
    if [ "$(docker inspect "$node_name" --format '{{.State.Running}}' 2>/dev/null)" != true ]; then
      classify_official_image_exit
    fi
    sleep 0.25
  done
  if [ "$ready" != true ]; then
    fixed_failure 'official_image_not_ready'
  fi

  local node_ip
  node_ip="$(docker inspect "$node_name" --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' 2>/dev/null)" \
    || fixed_failure 'container_address_unavailable'
  case "$node_ip" in
    [0-9]*.[0-9]*.[0-9]*.[0-9]*) ;;
    *) fixed_failure 'container_address_invalid' ;;
  esac

  if ! docker run --rm \
    --name "$harness_name" \
    --network "$network_name" \
    --user "$(id -u):$(id -g)" \
    --workdir /src \
    --read-only \
    --tmpfs /tmp:rw,exec,nosuid,size=512m \
    --cap-drop ALL \
    --security-opt no-new-privileges:true \
    -v "$repository_root:/src:ro" \
    -v "$module_cache:/go/pkg/mod:ro" \
    -v "$runtime_directory:/run/snapshot:ro" \
    -e HTTP_PROXY= -e HTTPS_PROXY= -e ALL_PROXY= \
    -e http_proxy= -e https_proxy= -e all_proxy= \
    -e NO_PROXY='*' -e no_proxy='*' -e GOPROXY=off -e GOCACHE=/tmp/go-build \
    -e "CONTROL_SNAPSHOT_CONTAINER_OWNER_URL=postgres://relay_control_migrator:relay_control_migrator_dev_only@${postgres_name}:5432/relay_station_control?sslmode=disable" \
    -e "CONTROL_SNAPSHOT_CONTAINER_RUNTIME_URL=postgres://relay_control_app_dev:relay_control_runtime_dev_only@${postgres_name}:5432/relay_station_control?sslmode=disable" \
    -e "CONTROL_SNAPSHOT_CONTAINER_NODE_ENDPOINT=http://${node_name}:8317" \
    -e "CONTROL_SNAPSHOT_CONTAINER_MANAGEMENT_DNS=${node_name}" \
    -e "CONTROL_SNAPSHOT_CONTAINER_MANAGEMENT_CIDRS=${node_ip}/32" \
    -e "CONTROL_SNAPSHOT_CONTAINER_PLAIN_HTTP_CIDRS=${node_ip}/32" \
    -e CONTROL_SNAPSHOT_CONTAINER_SECRET_MAPPING_FILE=/run/snapshot/mapping.json \
    -e CONTROL_SNAPSHOT_CONTAINER_NODE_SECRET_REFERENCE=file://snapshot-acceptance/management-key \
    "$golang_image" \
    go run ./deploy/acceptance/account-inventory-snapshot-container >"$harness_log" 2>&1
  then
    for checkpoint in \
      owner_database runtime_database seed secret_resolver driver repository \
      worker finalize_wait worker_shutdown finalize request_count \
      projection_contract projection_completeness \
      projection_counts projection_result projection_collections \
      projection_provider projection_item persistence
    do
      if grep -Fqx "account_inventory_snapshot_official_runtime=failed reason=acceptance_invariant checkpoint=${checkpoint}" "$harness_log"; then
        fixed_failure "harness_${checkpoint}"
      fi
    done
    fixed_failure 'harness_failed'
  fi
  harness_name=''

  if [ "$(wc -l <"$harness_log" | tr -d ' ')" != 1 ] || ! grep -Fqx "$expected_result" "$harness_log"; then
    fixed_failure 'harness_result_invalid'
  fi

  # The harness returns only after the sole management GET and its mandatory
  # ten-second cooldown. The lock may therefore be released on normal exit.
  lock_releasable=true
  printf '%s\n' "$expected_result"
}

main "$@"
