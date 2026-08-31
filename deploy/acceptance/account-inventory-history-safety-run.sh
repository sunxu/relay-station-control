#!/usr/bin/env bash
set -euo pipefail
umask 077

unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
export NO_PROXY='*' no_proxy='*'
export GOPROXY='https://goproxy.cn,direct'

if [ "$#" -ne 0 ]; then
  echo 'account_inventory_history_safety=failed reason=invalid_arguments' >&2
  exit 1
fi

script_directory="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
repository_root="$(CDPATH='' cd -- "$script_directory/../.." && pwd)"
runtime_directory=''

fixed_failure() {
  echo "account_inventory_history_safety=failed reason=$1" >&2
  exit 1
}

cleanup() {
  local exit_code=$?
  trap - EXIT HUP INT TERM
  case "$runtime_directory" in
    /tmp/relay-control-history-safety.*|/private/tmp/relay-control-history-safety.*)
      rm -rf -- "$runtime_directory"
      ;;
  esac
  return "$exit_code"
}

strict_cleanup() {
  case "$runtime_directory" in
    /tmp/relay-control-history-safety.*|/private/tmp/relay-control-history-safety.*)
      rm -rf -- "$runtime_directory"
      ;;
    *) fixed_failure 'cleanup_runtime_path_invalid' ;;
  esac
  [ ! -e "$runtime_directory" ] || fixed_failure 'cleanup_runtime_residual'
  trap - EXIT HUP INT TERM
}

require_test() {
  local exact_name="$1" listing
  listing="$runtime_directory/${exact_name}.tests"
  if ! env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    GOCACHE="$runtime_directory/go-build" go test ./internal/historyruntime \
      -list "^${exact_name}$" >"$listing" 2>&1; then
    fixed_failure 'test_discovery_failed'
  fi
  grep -Fxq "$exact_name" "$listing" || fixed_failure 'required_test_unavailable'
}

main() {
  command -v go >/dev/null 2>&1 || fixed_failure 'required_command_unavailable'
  runtime_directory="$(mktemp -d /tmp/relay-control-history-safety.XXXXXX)"
  trap cleanup EXIT
  trap 'exit 130' HUP INT TERM
  mkdir -m 700 "$runtime_directory/artifacts"

  export CONTROL_HISTORY_CANARY_SCAN_DIR="$runtime_directory/artifacts"
  export CONTROL_HISTORY_CANARY_ENDPOINT='https://history-endpoint-canary.invalid'
  export CONTROL_HISTORY_CANARY_IP='192.0.2.197'
  export CONTROL_HISTORY_CANARY_SECRET_REFERENCE='vault://history-secret-reference-canary'
  export CONTROL_HISTORY_CANARY_SECRET_VALUE='history-secret-value-canary-98f1'
  export CONTROL_HISTORY_CANARY_EMAIL='history-email-canary@example.invalid'
  export CONTROL_HISTORY_CANARY_ACCOUNT_KEY='openai:history-account-key-canary'
  export CONTROL_HISTORY_CANARY_RESPONSE_BODY='history-response-body-canary-86bc'
  export CONTROL_HISTORY_CANARY_RESPONSE_HEADER='history-response-header-canary-721a'
  export CONTROL_HISTORY_CANARY_RUN_ID='10000000-0000-4000-8000-000000000081'
  export CONTROL_HISTORY_CANARY_FENCING_TOKEN='10000000-0000-4000-8000-000000000082'
  export CONTROL_HISTORY_CANARY_CHECKSUM='history-checksum-canary-bf3a'
  export CONTROL_HISTORY_CANARY_RAW_ERROR='history-raw-error-canary-c9d2'
  export CONTROL_HISTORY_CANARY_SQL_PARAMETER='history-sql-parameter-canary-e71f'
  export CONTROL_HISTORY_CANARY_POLL_ID='10000000-0000-4000-8000-000000000083'
  export CONTROL_HISTORY_CANARY_POLICY_ID='10000000-0000-4000-8000-000000000084'

  # Reuse the bounded scanner while keeping the history sink names explicit above.
  export CONTROL_LIFECYCLE_CANARY_SCAN_DIR="$CONTROL_HISTORY_CANARY_SCAN_DIR"
  export CONTROL_LIFECYCLE_CANARY_ENDPOINT="$CONTROL_HISTORY_CANARY_ENDPOINT"
  export CONTROL_LIFECYCLE_CANARY_IP="$CONTROL_HISTORY_CANARY_IP"
  export CONTROL_LIFECYCLE_CANARY_SECRET_REFERENCE="$CONTROL_HISTORY_CANARY_SECRET_REFERENCE"
  export CONTROL_LIFECYCLE_CANARY_SECRET_VALUE="$CONTROL_HISTORY_CANARY_SECRET_VALUE"
  export CONTROL_LIFECYCLE_CANARY_EMAIL="$CONTROL_HISTORY_CANARY_EMAIL"
  export CONTROL_LIFECYCLE_CANARY_ACCOUNT_KEY="$CONTROL_HISTORY_CANARY_ACCOUNT_KEY"
  export CONTROL_LIFECYCLE_CANARY_RESPONSE_BODY="$CONTROL_HISTORY_CANARY_RESPONSE_BODY"
  export CONTROL_LIFECYCLE_CANARY_RESPONSE_HEADER="$CONTROL_HISTORY_CANARY_RESPONSE_HEADER"
  export CONTROL_LIFECYCLE_CANARY_VERSION="$CONTROL_HISTORY_CANARY_RUN_ID"
  export CONTROL_LIFECYCLE_CANARY_COMMIT="$CONTROL_HISTORY_CANARY_FENCING_TOKEN"
  export CONTROL_LIFECYCLE_CANARY_UNKNOWN_FIELD="$CONTROL_HISTORY_CANARY_CHECKSUM"
  export CONTROL_LIFECYCLE_CANARY_RAW_ERROR="$CONTROL_HISTORY_CANARY_RAW_ERROR"
  export CONTROL_LIFECYCLE_CANARY_SQL_PARAMETER="$CONTROL_HISTORY_CANARY_SQL_PARAMETER"
  export CONTROL_LIFECYCLE_CANARY_POLL_ID="$CONTROL_HISTORY_CANARY_POLL_ID"
  export CONTROL_LIFECYCLE_CANARY_POLICY_ID="$CONTROL_HISTORY_CANARY_POLICY_ID"

  require_test TestHistoryLocalOutputsExcludeCanaries
  require_test TestHistoryProductionSourcesHaveNoDirectNetworkImports
  if ! env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    GOCACHE="$runtime_directory/go-build" go test ./internal/historyruntime \
      -run '^(TestHistoryLocalOutputsExcludeCanaries|TestHistoryProductionSourcesHaveNoDirectNetworkImports)$' \
      -count=1 >"$runtime_directory/artifacts/history-safety.log" 2>&1; then
    fixed_failure 'history_safety_test_failed'
  fi
  if ! env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    GOCACHE="$runtime_directory/go-build" go run ./deploy/acceptance/account-inventory-lifecycle-canary-scan \
      >"$runtime_directory/scanner.log" 2>&1; then
    fixed_failure 'local_sink_canary_found'
  fi

  strict_cleanup
  echo 'account_inventory_history_safety=success scenarios=7 local_sink_canary=covered direct_network_client_imports=0 sensitive_canary_complete=not_covered external_requests=not_covered process_fake_endpoint_counter=not_covered database_non_identity_sink=not_covered cleanup_temp=0'
}

cd "$repository_root"
main
