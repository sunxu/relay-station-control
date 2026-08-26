#!/usr/bin/env bash
set -euo pipefail

# Snapshot acceptance adds no Node operation. Container mode delegates exactly
# once to the existing pinned-image poll acceptance, whose only management
# request is GET /v0/management/auth-files and whose global gate waits 10s
# after success or failure, including after the final request.
unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
export NO_PROXY='*' no_proxy='*'
export GOPROXY="${CONTROL_SNAPSHOT_ACCEPTANCE_GOPROXY:-https://goproxy.cn,direct}"
export GOCACHE="${CONTROL_SNAPSHOT_ACCEPTANCE_GOCACHE:-${TMPDIR:-/tmp}/relay-control-snapshot-acceptance-go-build}"
export npm_config_registry='https://registry.npmmirror.com'

script_directory="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
repository_root="$(CDPATH='' cd -- "$script_directory/../.." && pwd)"
mode="${1:-static}"

cd "$repository_root"
case "$mode" in
  static)
    go test -race ./cmd/control ./internal/drivers/cliproxyapi ./internal/inventorypoll ./internal/pollobservability ./internal/store ./deploy/acceptance/poll-canary-scan ./deploy/acceptance/account-inventory-poll-smoke -count=1
    ;;
  scan)
    for variable_name in \
      CONTROL_POLL_CANARY_ACCOUNT_KEY \
      CONTROL_POLL_CANARY_UNKNOWN_FIELD \
      CONTROL_POLL_CANARY_SQL_PARAMETER
    do
      if [ -z "${!variable_name:-}" ]; then
        echo 'account_inventory_snapshot_acceptance=failed reason=invalid_canary_configuration' >&2
        exit 1
      fi
    done
    go run ./deploy/acceptance/poll-canary-scan
    ;;
  container)
    lock_directory="${CONTROL_SNAPSHOT_ACCEPTANCE_LOCK_DIR:-${CONTROL_DRIVER_SMOKE_LOCK_DIR:-${TMPDIR:-/tmp}/relay-control-cliproxyapi-smoke.lock}}"
    umask 077
    if ! mkdir "$lock_directory" 2>/dev/null; then
      echo 'account_inventory_snapshot_acceptance=failed reason=concurrent_or_stale_global_lock' >&2
      exit 1
    fi
    cleanup_snapshot_lock() {
      rmdir "$lock_directory" 2>/dev/null || true
    }
    trap cleanup_snapshot_lock EXIT HUP INT TERM
    "$script_directory/account-inventory-poll-container.sh"
    echo 'account_inventory_snapshot_container=success image_version=v7.2.141 request_count=1 request_wait_seconds=10 operation=auth_files_readonly'
    ;;
  real-node)
    echo 'account_inventory_snapshot_acceptance=failed reason=real_node_adapter_not_wired request_count=0' >&2
    exit 2
    ;;
  *)
    echo 'account_inventory_snapshot_acceptance=failed reason=invalid_mode' >&2
    exit 2
    ;;
esac
