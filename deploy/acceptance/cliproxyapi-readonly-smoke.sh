#!/usr/bin/env bash
set -euo pipefail

# Run the production Driver boundary in a dedicated process. No response body,
# header, endpoint, Secret reference, key, provider identity, or email is ever
# written to a temporary file or command argument.

unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
export NO_PROXY='*' no_proxy='*'
export GOPROXY="${CONTROL_DRIVER_SMOKE_GOPROXY:-https://goproxy.cn,direct}"

script_directory="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
repository_root="$(CDPATH='' cd -- "$script_directory/../.." && pwd)"
lock_directory="${CONTROL_DRIVER_SMOKE_LOCK_DIR:-${TMPDIR:-/tmp}/relay-control-cliproxyapi-smoke.lock}"

umask 077
if ! mkdir "$lock_directory" 2>/dev/null; then
  echo 'cliproxyapi_readonly_smoke=failed reason=concurrent_or_stale_lock' >&2
  exit 1
fi
cleanup() {
  rmdir "$lock_directory" 2>/dev/null || true
}
trap cleanup EXIT HUP INT TERM

cd "$repository_root"
go run ./deploy/acceptance/cliproxyapi-smoke
