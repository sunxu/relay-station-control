#!/usr/bin/env bash
set -euo pipefail

# Official-image acceptance uses only a synthetic empty auth directory. The
# production Driver performs one account-inventory management read and waits
# 10s after it, including when that final request fails.
unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
export NO_PROXY='*' no_proxy='*'
export GOPROXY='off'

script_directory="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
repository_root="$(CDPATH='' cd -- "$script_directory/../.." && pwd)"
official_image='eceasy/cli-proxy-api:v7.2.141'
official_digest='eceasy/cli-proxy-api@sha256:7f598ce64478a8a5f90ed76875e0e9b0e7d77b80e17184b13df18c3d5bdb3def'
runtime_directory=''
temporary_root="${TMPDIR:-/tmp}"
temporary_root="${temporary_root%/}"
suffix="$$"
network_name="relay-control-poll-${suffix}"
node_name="relay-control-poll-node-${suffix}"
lock_directory="${CONTROL_DRIVER_SMOKE_LOCK_DIR:-${TMPDIR:-/tmp}/relay-control-cliproxyapi-smoke.lock}"
lock_releasable=false

umask 077
if ! mkdir "$lock_directory" 2>/dev/null; then
  echo 'account_inventory_poll_container=failed reason=concurrent_or_stale_global_lock' >&2
  exit 1
fi

cleanup() {
  local exit_code=$?
  trap - EXIT HUP INT TERM
  docker rm -f "$node_name" >/dev/null 2>&1 || true
  docker network rm "$network_name" >/dev/null 2>&1 || true
  case "$runtime_directory" in
    "$temporary_root"/relay-control-poll-container.*)
      rm -rf -- "$runtime_directory"
      ;;
  esac
  if [ "$lock_releasable" = true ]; then
    rmdir "$lock_directory" 2>/dev/null || true
  fi
  return "$exit_code"
}
trap cleanup EXIT
trap 'exit 130' HUP INT TERM

runtime_directory="$(mktemp -d "$temporary_root/relay-control-poll-container.XXXXXX")"

case "$(docker image inspect "$official_image" --format '{{json .RepoDigests}}')" in
  *"$official_digest"*) ;;
  *)
    echo 'account_inventory_poll_container=failed reason=official_image_digest_mismatch' >&2
    exit 1
    ;;
esac

printf '%s\n' \
  'host: "0.0.0.0"' \
  'port: 8317' \
  'remote-management:' \
  '  allow-remote: true' \
  '  secret-key: "poll-acceptance-management-key"' \
  '  disable-control-panel: true' \
  'auth-dir: "/tmp/auth"' \
  'api-keys: ["poll-acceptance-api-key"]' \
  'debug: false' \
  'logging-to-file: false' \
  'usage-statistics-enabled: false' \
  'proxy-url: ""' >"$runtime_directory/config.yaml"
printf '%s\n' 'poll-acceptance-management-key' >"$runtime_directory/management-key"
printf '%s\n' \
  '{"provider":"file","references":[{"reference":"file://poll-acceptance/management-key","path":"/run/poll/management-key"}]}' \
  >"$runtime_directory/mapping.json"
chmod 0600 "$runtime_directory/config.yaml" "$runtime_directory/management-key" "$runtime_directory/mapping.json"

docker network create --internal "$network_name" >/dev/null
docker run -d \
  --name "$node_name" \
  --network "$network_name" \
  --user "$(id -u):$(id -g)" \
  --read-only \
  --tmpfs /tmp:rw,noexec,nosuid,size=16m \
  --cap-drop ALL \
  --security-opt no-new-privileges:true \
  -v "$runtime_directory/config.yaml:/CLIProxyAPI/poll-config.yaml:ro" \
  "$official_digest" ./CLIProxyAPI -config /CLIProxyAPI/poll-config.yaml -local-model >/dev/null

ready=false
for _ in $(seq 1 60); do
  if docker logs "$node_name" 2>&1 | grep -q 'API server started successfully'; then
    ready=true
    break
  fi
  sleep 0.25
done
if [ "$ready" != true ]; then
  echo 'account_inventory_poll_container=failed reason=official_image_not_ready' >&2
  exit 1
fi

node_ip="$(docker inspect "$node_name" --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}')"
case "$node_ip" in
  [0-9]*.[0-9]*.[0-9]*.[0-9]*) ;;
  *)
    echo 'account_inventory_poll_container=failed reason=container_address_invalid' >&2
    exit 1
    ;;
esac
module_cache="$(go env GOMODCACHE)"
if [ ! -d "$module_cache" ]; then
  echo 'account_inventory_poll_container=failed reason=module_cache_unavailable' >&2
  exit 1
fi

docker run --rm \
  --network "$network_name" \
  --user "$(id -u):$(id -g)" \
  --workdir /src \
  --read-only \
  --tmpfs /tmp:rw,exec,nosuid,size=512m \
  --cap-drop ALL \
  --security-opt no-new-privileges:true \
  -v "$repository_root:/src:ro" \
  -v "$module_cache:/go/pkg/mod:ro" \
  -v "$runtime_directory:/run/poll:ro" \
  -e HTTP_PROXY= -e HTTPS_PROXY= -e ALL_PROXY= \
  -e http_proxy= -e https_proxy= -e all_proxy= \
  -e NO_PROXY='*' -e no_proxy='*' -e GOPROXY=off -e GOCACHE=/tmp/go-build \
  -e CONTROL_POLL_SMOKE_MODE=container \
  -e CONTROL_POLL_SMOKE_MANAGEMENT_DNS=poll-acceptance.invalid \
  -e "CONTROL_POLL_SMOKE_MANAGEMENT_CIDRS=${node_ip}/32" \
  -e "CONTROL_POLL_SMOKE_PLAIN_HTTP_CIDRS=${node_ip}/32" \
  -e CONTROL_POLL_SMOKE_SECRET_MAPPING_FILE=/run/poll/mapping.json \
  -e CONTROL_POLL_SMOKE_PROVIDER_POLICY_ID=11111111-1111-4111-8111-111111111111 \
  -e CONTROL_POLL_SMOKE_ACTIVE_PROVIDERS=openai \
  -e "CONTROL_POLL_SMOKE_NODE_ENDPOINT=http://${node_ip}:8317" \
  -e CONTROL_POLL_SMOKE_NODE_SECRET_REFERENCE=file://poll-acceptance/management-key \
  golang:1.27.0-alpine \
  go run ./deploy/acceptance/account-inventory-poll-smoke

lock_releasable=true
echo 'account_inventory_poll_container=success image_version=v7.2.141 request_count=1 request_wait_seconds=10'
