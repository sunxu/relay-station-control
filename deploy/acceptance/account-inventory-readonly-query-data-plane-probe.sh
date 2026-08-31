#!/bin/sh
set -euo pipefail
umask 077

unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy

count="${1:-}"
case "$count" in
  1|100) ;;
  *)
    echo 'account_inventory_readonly_query_data_plane=failed reason=invalid_count' >&2
    exit 1
    ;;
esac

key_file='/run/acceptance/data-plane-key'
temporary_root="${TMPDIR:-/tmp}"
temporary_root="${temporary_root%/}"
response_file="$(mktemp "${temporary_root}/readonly-query-data-plane.XXXXXX")"
cleanup() {
  rm -f -- "$response_file"
}
trap cleanup EXIT HUP INT TERM

key="$(tr -d '\r\n' <"$key_file")"
if [ -z "$key" ]; then
  echo 'account_inventory_readonly_query_data_plane=failed reason=invalid_key_file' >&2
  exit 1
fi

completed=0
while [ "$completed" -lt "$count" ]; do
  if ! {
    printf 'GET /v1/models HTTP/1.1\r\n'
    printf 'Host: data-plane\r\n'
    printf 'Authorization: Bearer %s\r\n' "$key"
    printf 'Connection: close\r\n\r\n'
  } | nc -w 5 data-plane 8317 >"$response_file" 2>/dev/null; then
    echo 'account_inventory_readonly_query_data_plane=failed reason=request_failed' >&2
    exit 1
  fi
  if ! grep -Eq '^HTTP/1\.[01] 200 ' "$response_file" ||
    ! grep -Eq '"object"[[:space:]]*:[[:space:]]*"list"' "$response_file" ||
    ! grep -Eq '"data"[[:space:]]*:[[:space:]]*\[' "$response_file"; then
    echo 'account_inventory_readonly_query_data_plane=failed reason=response_invalid' >&2
    exit 1
  fi
  completed=$((completed + 1))
done
unset key

if [ "$count" -eq 1 ]; then
  echo 'account_inventory_readonly_query_data_plane=success official_data_plane_baseline=1'
else
  echo 'account_inventory_readonly_query_data_plane=success official_data_plane_http=100/100'
fi
