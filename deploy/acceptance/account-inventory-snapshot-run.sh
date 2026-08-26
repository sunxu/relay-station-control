#!/usr/bin/env bash
set -euo pipefail

# Snapshot acceptance adds no Node operation. Container mode delegates exactly
# once to the pinned official-image runtime gate. That leaf owns the shared
# global lock and releases it only after the sole auth-files read and mandatory
# ten-second cooldown both finish successfully.
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
    go test -race ./cmd/control ./internal/drivers/cliproxyapi ./internal/inventorypoll ./internal/pollobservability ./internal/store ./deploy/acceptance/poll-canary-scan ./deploy/acceptance/account-inventory-poll-smoke ./deploy/acceptance/account-inventory-snapshot-container -count=1
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
    "$script_directory/account-inventory-snapshot-container.sh"
    ;;
  postgres)
    "$script_directory/account-inventory-snapshot-postgres.sh"
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
