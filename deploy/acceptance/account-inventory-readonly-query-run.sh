#!/usr/bin/env bash
set -euo pipefail
umask 077

unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
export NO_PROXY='*' no_proxy='*'
export GOPROXY='https://goproxy.cn,direct'
export npm_config_registry='https://registry.npmmirror.com'

if [ "$#" -gt 1 ]; then
  echo 'account_inventory_readonly_query_acceptance=failed reason=invalid_arguments' >&2
  exit 1
fi

script_directory="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
repository_root="$(CDPATH='' cd -- "$script_directory/../.." && pwd)"
mode="${1:-static}"
temporary_root="${TMPDIR:-/tmp}"
temporary_root="${temporary_root%/}"
runtime_directory=''

fixed_failure() {
  echo "account_inventory_readonly_query_acceptance=failed reason=$1" >&2
  exit 1
}

cleanup() {
  local exit_code=$?
  trap - EXIT HUP INT TERM
  case "$runtime_directory" in
    "${temporary_root}"/relay-control-readonly-query-run.*)
      rm -rf -- "$runtime_directory"
      ;;
  esac
  return "$exit_code"
}

strict_cleanup() {
  case "$runtime_directory" in
    "${temporary_root}"/relay-control-readonly-query-run.*)
      rm -rf -- "$runtime_directory"
      ;;
    *) fixed_failure 'cleanup_runtime_path_invalid' ;;
  esac
  if [ -e "$runtime_directory" ]; then
    fixed_failure 'cleanup_runtime_residual'
  fi
  trap - EXIT HUP INT TERM
}

require_test() {
  local package="$1" pattern exact_name listing
  shift
  pattern="$1"
  shift
  exact_name="$1"
  listing="$runtime_directory/$(printf '%s' "$exact_name" | tr -c '[:alnum:]' '_').tests"
  if ! env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    go test "$package" -list "$pattern" >"$listing" 2>&1; then
    fixed_failure 'test_discovery_failed'
  fi
  if ! grep -Fxq "$exact_name" "$listing"; then
    fixed_failure 'required_test_unavailable'
  fi
}

validate_openspec() {
  local archive_count canonical_spec openspec_log
  openspec_log="$runtime_directory/openspec.log"
  if [ -d "$repository_root/openspec/changes/add-control-account-inventory-readonly-query" ]; then
    if ! env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
      npx --yes @fission-ai/openspec@1.10.0 validate add-control-account-inventory-readonly-query --strict \
        >"$openspec_log" 2>&1; then
      fixed_failure 'openspec_validation_failed'
    fi
  else
    for canonical_spec in \
      account-inventory-readonly-query \
      account-inventory-lifecycle \
      account-inventory-snapshot \
      account-inventory-poll-run
    do
      if [ ! -f "$repository_root/openspec/specs/$canonical_spec/spec.md" ]; then
        fixed_failure 'openspec_validation_failed'
      fi
    done
    if ! archive_count="$(find "$repository_root/openspec/changes/archive" \
      -mindepth 1 -maxdepth 1 -type d \
      -name '*-add-control-account-inventory-readonly-query' | wc -l | tr -d ' ')"; then
      fixed_failure 'openspec_validation_failed'
    fi
    if [ "$archive_count" -ne 1 ]; then
      fixed_failure 'openspec_validation_failed'
    fi
  fi
  if ! env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    npx --yes @fission-ai/openspec@1.10.0 validate --all --strict \
      >>"$openspec_log" 2>&1; then
    fixed_failure 'openspec_validation_failed'
  fi
}

run_static() {
  command -v go >/dev/null 2>&1 || fixed_failure 'required_command_unavailable'
  command -v npm >/dev/null 2>&1 || fixed_failure 'required_command_unavailable'
  command -v npx >/dev/null 2>&1 || fixed_failure 'required_command_unavailable'

  require_test ./internal/store '^TestAccountInventoryCursorTTLAndKeyRotation$' TestAccountInventoryCursorTTLAndKeyRotation
  require_test ./internal/api '^TestAccountInventorySensitiveCanaryFinalArtifactMatrix$' TestAccountInventorySensitiveCanaryFinalArtifactMatrix
  require_test ./internal/api '^TestAccountInventoryHTTPAuthorizationPaginationAndErrorMapping$' TestAccountInventoryHTTPAuthorizationPaginationAndErrorMapping

  if ! env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    go test -race ./internal/store ./internal/api \
      -run '^(TestAccountInventoryCursorTTLAndKeyRotation|TestAccountInventorySensitiveCanaryFinalArtifactMatrix)$' \
      -count=1 >"$runtime_directory/go-sensitive.log" 2>&1; then
    fixed_failure 'go_sensitive_gate_failed'
  fi
  if ! env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    go test ./deploy/acceptance/account-inventory-readonly-query-shell -count=1 >"$runtime_directory/shell-contract.log" 2>&1; then
    fixed_failure 'shell_contract_gate_failed'
  fi
  if ! (
    cd "$repository_root/web"
    env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
      npm test -- src/api/account-inventory-sensitive-boundary.test.ts src/pages/AccountInventoryView.test.tsx \
        >"$runtime_directory/frontend-sensitive.log" 2>&1
  ); then
    fixed_failure 'frontend_sensitive_gate_failed'
  fi
  if ! (
    cd "$repository_root/web"
    env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
      npm run typecheck >"$runtime_directory/frontend-typecheck.log" 2>&1
  ); then
    fixed_failure 'frontend_typecheck_failed'
  fi
  validate_openspec
}

cd "$repository_root"
case "$mode" in
  static|postgres|recovery|all) ;;
  *)
    echo 'account_inventory_readonly_query_acceptance=failed reason=invalid_mode' >&2
    exit 1
    ;;
esac
runtime_directory="$(mktemp -d "${temporary_root}/relay-control-readonly-query-run.XXXXXX")"
trap cleanup EXIT
trap 'exit 130' HUP INT TERM

case "$mode" in
  static)
    run_static
  strict_cleanup
    echo 'account_inventory_readonly_query_acceptance=success mode=static key_rotation=covered sensitive_canary_occurrences=0 browser_persistent_occurrences=0 query_external_requests=0'
    ;;
  postgres)
    "$script_directory/account-inventory-readonly-query-postgres.sh"
  strict_cleanup
    echo 'account_inventory_readonly_query_acceptance=success mode=postgres'
    ;;
  recovery)
    "$script_directory/account-inventory-readonly-query-recovery.sh"
  strict_cleanup
    echo 'account_inventory_readonly_query_acceptance=success mode=recovery'
    ;;
  all)
    run_static
    "$script_directory/account-inventory-readonly-query-postgres.sh"
    "$script_directory/account-inventory-readonly-query-recovery.sh"
  strict_cleanup
    echo 'account_inventory_readonly_query_acceptance=success mode=all key_rotation=covered sensitive_canary_occurrences=0 browser_persistent_occurrences=0 query_external_requests=0'
    ;;
esac
