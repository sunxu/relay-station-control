#!/usr/bin/env bash
set -euo pipefail

# This leaf is the only real-Node snapshot gate. A failed or interrupted run
# deliberately leaves the shared lock stale so an operator must resolve the
# unknown request outcome before any other CLIProxyAPI acceptance can run.
unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
export NO_PROXY='*' no_proxy='*'
export GOPROXY="${CONTROL_SNAPSHOT_ACCEPTANCE_GOPROXY:-https://goproxy.cn,direct}"

script_directory="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
repository_root="$(CDPATH='' cd -- "$script_directory/../.." && pwd)"
workspace_root="$(CDPATH='' cd -- "$repository_root/.." && pwd)"
postgres_image='postgres:18.6-alpine@sha256:d3e1620b530c944afa6e887d22eb899824da68e19c52024bf98f5220c88a65b2'
golang_image='golang:1.27.0-alpine@sha256:4c9fe60190a2a3350ddc51de80d0224b8a6698d12bdfc999fee45ea9d6c46dbc'
phase0_image='relay-station/cliproxyapi:7.2.141-phase0'
expected_result='account_inventory_snapshot_real_node=success node_count=2 request_count=2 request_wait_seconds=10 runtime_mode_count=2 disk_fallback_mode_count=0 snapshot_items=6 provider_results=2 promotion_applied=2 promotion_skipped=0 management_writes=0 probe_requests=0 gateway_requests=0'
lock_directory="${CONTROL_DRIVER_SMOKE_LOCK_DIR:-${TMPDIR:-/tmp}/relay-control-cliproxyapi-smoke.lock}"
runtime_directory=''
database_network=''
postgres_name=''
postgres_volume=''
harness_name=''
resource_owner=''
database_network_created=false
postgres_created=false
postgres_volume_created=false
harness_created=false

fixed_failure() {
  echo "account_inventory_snapshot_real_node=failed reason=$1" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || fixed_failure 'required_command_unavailable'
}

protected_file_mode_owner() {
  if [ "$(uname -s)" = Darwin ]; then
    stat -f '%Lp:%u' "$1"
  else
    stat -c '%a:%u' "$1"
  fi
}

cleanup_resources() {
  local cleanup_failed=false actual_owner
  if [ "$harness_created" = true ]; then
    actual_owner="$(docker inspect "$harness_name" --format '{{index .Config.Labels "relay-control-acceptance"}}' 2>/dev/null)" || actual_owner=''
    if [ "$actual_owner" != "$resource_owner" ]; then
      cleanup_failed=true
    elif docker rm -f "$harness_name" >/dev/null 2>&1; then
      harness_created=false
    else
      cleanup_failed=true
    fi
  fi
  if [ "$postgres_created" = true ]; then
    actual_owner="$(docker inspect "$postgres_name" --format '{{index .Config.Labels "relay-control-acceptance"}}' 2>/dev/null)" || actual_owner=''
    if [ "$actual_owner" != "$resource_owner" ]; then
      cleanup_failed=true
    elif docker rm -f "$postgres_name" >/dev/null 2>&1; then
      postgres_created=false
    else
      cleanup_failed=true
    fi
  fi
  if [ "$database_network_created" = true ]; then
    actual_owner="$(docker network inspect "$database_network" --format '{{index .Labels "relay-control-acceptance"}}' 2>/dev/null)" || actual_owner=''
    if [ "$actual_owner" != "$resource_owner" ]; then
      cleanup_failed=true
    elif docker network rm "$database_network" >/dev/null 2>&1; then
      database_network_created=false
    else
      cleanup_failed=true
    fi
  fi
  if [ "$postgres_volume_created" = true ]; then
    actual_owner="$(docker volume inspect "$postgres_volume" --format '{{index .Labels "relay-control-acceptance"}}' 2>/dev/null)" || actual_owner=''
    if [ "$actual_owner" != "$resource_owner" ]; then
      cleanup_failed=true
    elif docker volume rm "$postgres_volume" >/dev/null 2>&1; then
      postgres_volume_created=false
    else
      cleanup_failed=true
    fi
  fi
  case "$runtime_directory" in
    /tmp/relay-control-snapshot-real-node.*|/private/tmp/relay-control-snapshot-real-node.*)
      if rm -rf -- "$runtime_directory"; then
        runtime_directory=''
      else
        cleanup_failed=true
      fi
      ;;
  esac
  [ "$cleanup_failed" = false ]
}

cleanup() {
  local exit_code=$?
  trap - EXIT HUP INT TERM
  cleanup_resources >/dev/null 2>&1 || true
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

inspect_node() {
  local node_name="$1" expected_port="$2"
  local state image port_binding network_count
  state="$(docker inspect "$node_name" --format '{{if .State.Running}}running{{else}}stopped{{end}}' 2>/dev/null)" \
    || fixed_failure 'phase0_node_unavailable'
  [ "$state" = running ] || fixed_failure 'phase0_node_not_running'
  image="$(docker inspect "$node_name" --format '{{.Config.Image}}' 2>/dev/null)" \
    || fixed_failure 'phase0_node_unavailable'
  [ "$image" = "$phase0_image" ] || fixed_failure 'phase0_node_image_invalid'
  port_binding="$(docker inspect "$node_name" --format '{{with (index .NetworkSettings.Ports "8317/tcp")}}{{if eq (len .) 1}}{{(index . 0).HostIp}}:{{(index . 0).HostPort}}{{end}}{{end}}' 2>/dev/null)" \
    || fixed_failure 'phase0_node_port_invalid'
  [ "$port_binding" = "127.0.0.1:${expected_port}" ] || fixed_failure 'phase0_node_port_invalid'
  network_count="$(docker inspect "$node_name" --format '{{len .NetworkSettings.Networks}}' 2>/dev/null)" \
    || fixed_failure 'phase0_node_network_invalid'
  [ "$network_count" = 1 ] || fixed_failure 'phase0_node_network_invalid'
}

main() {
  local suffix module_cache harness_log migration_log phase0_root
  local first_network second_network first_ip second_ip
  local first_secret second_secret

  trap cleanup EXIT
  trap 'exit 130' HUP INT TERM
  umask 077

  if ! mkdir "$lock_directory" 2>/dev/null; then
    fixed_failure 'concurrent_or_stale_global_lock'
  fi

  require_command docker
  require_command go
  require_command id
  require_command stat
  require_command uname
  phase0_root="${PHASE0_RUNTIME_DIR:-}"
  case "$phase0_root" in
    /*) ;;
    *) fixed_failure 'phase0_runtime_invalid' ;;
  esac
  phase0_root="${phase0_root%/}"
  case "$phase0_root/" in
    "$workspace_root/"*) fixed_failure 'phase0_runtime_invalid' ;;
  esac
  first_secret="$phase0_root/node-a/secrets/management-key"
  second_secret="$phase0_root/node-b/secrets/management-key"
  for secret_file in "$first_secret" "$second_secret"; do
    [ -f "$secret_file" ] && [ ! -L "$secret_file" ] || fixed_failure 'phase0_secret_file_invalid'
    [ "$(protected_file_mode_owner "$secret_file" 2>/dev/null)" = "600:$(id -u)" ] \
      || fixed_failure 'phase0_secret_file_invalid'
  done

  if ! docker image inspect "$postgres_image" >/dev/null 2>&1; then
    fixed_failure 'postgres_image_unavailable'
  fi
  if ! docker image inspect "$golang_image" >/dev/null 2>&1; then
    fixed_failure 'golang_image_unavailable'
  fi
  inspect_node relay-phase0-node-a 18317
  inspect_node relay-phase0-node-b 18318
  first_network="$(docker inspect relay-phase0-node-a --format '{{range $name, $_ := .NetworkSettings.Networks}}{{$name}}{{end}}' 2>/dev/null)" \
    || fixed_failure 'phase0_node_network_invalid'
  second_network="$(docker inspect relay-phase0-node-b --format '{{range $name, $_ := .NetworkSettings.Networks}}{{$name}}{{end}}' 2>/dev/null)" \
    || fixed_failure 'phase0_node_network_invalid'
  [ -n "$first_network" ] && [ "$first_network" = "$second_network" ] || fixed_failure 'phase0_node_network_invalid'
  [ "$(docker network inspect "$first_network" --format '{{.Driver}}' 2>/dev/null)" = bridge ] \
    || fixed_failure 'phase0_node_network_invalid'
  first_ip="$(docker inspect relay-phase0-node-a --format "{{with index .NetworkSettings.Networks \"$first_network\"}}{{.IPAddress}}{{end}}" 2>/dev/null)" \
    || fixed_failure 'phase0_node_address_invalid'
  second_ip="$(docker inspect relay-phase0-node-b --format "{{with index .NetworkSettings.Networks \"$first_network\"}}{{.IPAddress}}{{end}}" 2>/dev/null)" \
    || fixed_failure 'phase0_node_address_invalid'
  case "$first_ip" in [0-9]*.[0-9]*.[0-9]*.[0-9]*) ;; *) fixed_failure 'phase0_node_address_invalid' ;; esac
  case "$second_ip" in [0-9]*.[0-9]*.[0-9]*.[0-9]*) ;; *) fixed_failure 'phase0_node_address_invalid' ;; esac
  [ "$first_ip" != "$second_ip" ] || fixed_failure 'phase0_node_address_invalid'

  module_cache="$(go env GOMODCACHE)"
  [ -d "$module_cache" ] || fixed_failure 'module_cache_unavailable'
  runtime_directory="$(mktemp -d /tmp/relay-control-snapshot-real-node.XXXXXXXXXXXX)"
  suffix="${runtime_directory##*.}"
  resource_owner="$suffix"
  database_network="relay-control-snapshot-real-db-${suffix}"
  postgres_name="relay-control-snapshot-real-postgres-${suffix}"
  postgres_volume="relay-control-snapshot-real-postgres-${suffix}"
  harness_name="relay-control-snapshot-real-harness-${suffix}"
  harness_log="$runtime_directory/harness.log"
  migration_log="$runtime_directory/migration.log"
  printf '%s\n' \
    '{"provider":"file","references":[' \
    '  {"reference":"file://phase0/node-a-management-key","path":"/run/phase0-secrets/node-a-management-key"},' \
    '  {"reference":"file://phase0/node-b-management-key","path":"/run/phase0-secrets/node-b-management-key"}' \
    ']}' >"$runtime_directory/mapping.json"
  chmod 0600 "$runtime_directory/mapping.json"

  docker network create --internal --label "relay-control-acceptance=${suffix}" "$database_network" >/dev/null 2>&1 \
    || fixed_failure 'database_network_create_failed'
  database_network_created=true
  if docker volume inspect "$postgres_volume" >/dev/null 2>&1; then
    fixed_failure 'postgres_volume_collision'
  fi
  docker volume create --label "relay-control-acceptance=${suffix}" "$postgres_volume" >/dev/null 2>&1 \
    || fixed_failure 'postgres_volume_create_failed'
  postgres_volume_created=true
  [ "$(docker volume inspect "$postgres_volume" --format '{{index .Labels "relay-control-acceptance"}}' 2>/dev/null)" = "$resource_owner" ] \
    || fixed_failure 'postgres_volume_collision'
  if ! docker create \
    --name "$postgres_name" \
    --label "relay-control-acceptance=${suffix}" \
    --network "$database_network" \
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
    fixed_failure 'postgres_create_failed'
  fi
  postgres_created=true
  docker start "$postgres_name" >/dev/null 2>&1 || fixed_failure 'postgres_start_failed'
  wait_for_postgres

  if ! docker run --rm \
    --network "$database_network" \
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

  if ! docker create \
    --name "$harness_name" \
    --label "relay-control-acceptance=${suffix}" \
    --network "$first_network" \
    --user "$(id -u):$(id -g)" \
    --workdir /src \
    --read-only \
    --tmpfs /tmp:rw,exec,nosuid,size=512m \
    --cap-drop ALL \
    --security-opt no-new-privileges:true \
    -v "$repository_root:/src:ro" \
    -v "$module_cache:/go/pkg/mod:ro" \
    -v "$runtime_directory/mapping.json:/run/acceptance/mapping.json:ro" \
    -v "$first_secret:/run/phase0-secrets/node-a-management-key:ro" \
    -v "$second_secret:/run/phase0-secrets/node-b-management-key:ro" \
    -e HTTP_PROXY= -e HTTPS_PROXY= -e ALL_PROXY= \
    -e http_proxy= -e https_proxy= -e all_proxy= \
    -e NO_PROXY='*' -e no_proxy='*' -e GOPROXY=off -e GOCACHE=/tmp/go-build \
    -e "CONTROL_SNAPSHOT_REAL_NODE_OWNER_URL=postgres://relay_control_migrator:relay_control_migrator_dev_only@${postgres_name}:5432/relay_station_control?sslmode=disable" \
    -e "CONTROL_SNAPSHOT_REAL_NODE_RUNTIME_URL=postgres://relay_control_app_dev:relay_control_runtime_dev_only@${postgres_name}:5432/relay_station_control?sslmode=disable" \
    -e CONTROL_SNAPSHOT_REAL_NODE_SECRET_MAPPING_FILE=/run/acceptance/mapping.json \
    -e "CONTROL_SNAPSHOT_REAL_NODE_MANAGEMENT_CIDRS=${first_ip}/32,${second_ip}/32" \
    "$golang_image" go run ./deploy/acceptance/account-inventory-snapshot-real-node >/dev/null 2>&1
  then
    fixed_failure 'harness_create_failed'
  fi
  harness_created=true
  docker network connect "$database_network" "$harness_name" >/dev/null 2>&1 \
    || fixed_failure 'harness_database_network_failed'
  if ! docker start -a "$harness_name" >"$harness_log" 2>&1; then
    for checkpoint in \
      owner_database runtime_database seed secret_resolver driver repository \
      worker finalize_wait worker_shutdown request_count finalize_count finalize \
      projection_contract projection_counts projection_provider projection_item persistence
    do
      if grep -Fqx "account_inventory_snapshot_real_node=failed reason=acceptance_invariant checkpoint=${checkpoint}" "$harness_log"; then
        fixed_failure "harness_${checkpoint}"
      fi
    done
    if grep -Fqx 'account_inventory_snapshot_real_node=failed reason=invalid_configuration' "$harness_log"; then
      fixed_failure 'harness_invalid_configuration'
    fi
    fixed_failure 'harness_failed'
  fi
  if [ "$(wc -l <"$harness_log" | tr -d ' ')" != 1 ] || ! grep -Fqx "$expected_result" "$harness_log"; then
    fixed_failure 'harness_result_invalid'
  fi

  # The second and final ListAccountInventory call has completed its ten-second
  # cooldown before the harness can exit. Do not report success or release the
  # lock until every Secret mount and identity-bearing database resource is
  # verifiably gone.
  if ! cleanup_resources; then
    fixed_failure 'cleanup_failed'
  fi
  if ! rmdir "$lock_directory" >/dev/null 2>&1; then
    fixed_failure 'unlock_failed'
  fi
  printf '%s\n' "$expected_result"
}

main "$@"
