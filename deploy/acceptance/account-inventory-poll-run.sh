#!/usr/bin/env bash
set -euo pipefail

# Poll-run acceptance entrypoint. It never accepts an arbitrary command or URL.
# Real Node access remains fail closed. The container mode uses only the pinned
# official image and a synthetic empty auth directory in an internal network.

unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
export NO_PROXY='*' no_proxy='*'
export GOPROXY="${CONTROL_POLL_ACCEPTANCE_GOPROXY:-https://goproxy.cn,direct}"
export GOCACHE="${CONTROL_POLL_ACCEPTANCE_GOCACHE:-${TMPDIR:-/tmp}/relay-control-poll-acceptance-go-build}"
export npm_config_registry='https://registry.npmmirror.com'

script_directory="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
repository_root="$(CDPATH='' cd -- "$script_directory/../.." && pwd)"
mode="${1:-static}"

cd "$repository_root"
case "$mode" in
  static)
    go test -race ./cmd/control ./internal/inventorypoll ./internal/pollobservability ./internal/store ./deploy/acceptance/poll-canary-scan ./deploy/acceptance/account-inventory-poll-smoke -count=1
    ;;
  scan)
    go run ./deploy/acceptance/poll-canary-scan
    ;;
  container)
    "$script_directory/account-inventory-poll-container.sh"
    ;;
  real-node)
    lock_directory="${CONTROL_DRIVER_SMOKE_LOCK_DIR:-${TMPDIR:-/tmp}/relay-control-cliproxyapi-smoke.lock}"
    lock_acquired=false
    lock_releasable=false
    umask 077
    if ! mkdir "$lock_directory" 2>/dev/null; then
      echo 'account_inventory_poll_acceptance=failed reason=concurrent_or_stale_global_lock' >&2
      exit 1
    fi
    lock_acquired=true
    cleanup() {
      local exit_code=$?
      trap - EXIT HUP INT TERM
      if [ "$lock_acquired" = true ] && [ "$lock_releasable" = true ]; then
        rmdir "$lock_directory" 2>/dev/null || true
      fi
      return "$exit_code"
    }
    trap cleanup EXIT
    trap 'exit 130' HUP INT TERM
    go run ./deploy/acceptance/account-inventory-poll-smoke
    lock_releasable=true
    ;;
  *)
    echo 'account_inventory_poll_acceptance=failed reason=invalid_mode' >&2
    exit 2
    ;;
esac
