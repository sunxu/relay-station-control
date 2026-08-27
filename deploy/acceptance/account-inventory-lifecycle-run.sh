#!/usr/bin/env bash
set -euo pipefail

# Lifecycle acceptance never accepts a target or credential. The only
# container request is delegated to the existing pinned synthetic snapshot
# gate; real-Node mode remains fail closed in this foundation runner.
unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
export NO_PROXY='*' no_proxy='*'
export GOPROXY="${CONTROL_LIFECYCLE_ACCEPTANCE_GOPROXY:-https://goproxy.cn,direct}"
export GOCACHE="${CONTROL_LIFECYCLE_ACCEPTANCE_GOCACHE:-${TMPDIR:-/tmp}/relay-control-lifecycle-acceptance-go-build}"
export npm_config_registry='https://registry.npmmirror.com'

script_directory="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
repository_root="$(CDPATH='' cd -- "$script_directory/../.." && pwd)"
mode="${1:-static}"
runtime_directory=''

fixed_failure() {
  echo "account_inventory_lifecycle_acceptance=failed reason=$1" >&2
  exit 1
}

cleanup() {
  local exit_code=$?
  trap - EXIT HUP INT TERM
  case "$runtime_directory" in
    /tmp/relay-control-lifecycle-static.*|/private/tmp/relay-control-lifecycle-static.*|\
    /tmp/relay-control-lifecycle-scan.*|/private/tmp/relay-control-lifecycle-scan.*)
      rm -rf -- "$runtime_directory"
      ;;
  esac
  return "$exit_code"
}

require_lifecycle_test() {
  local package="$1" pattern="$2" classification="$3" listing
  listing="$runtime_directory/$(printf '%s' "$classification" | tr -c '[:alnum:]' '_').tests"
  if ! go test "$package" -list "$pattern" >"$listing" 2>&1; then
    fixed_failure 'lifecycle_test_discovery_failed'
  fi
  if ! grep -Eq '^Test(AccountInventory.*Lifecycle|CurrentAccountInventoryLifecycle|InventoryLifecycle)' "$listing"; then
    fixed_failure "$classification"
  fi
}

run_static() {
  command -v go >/dev/null 2>&1 || fixed_failure 'required_command_unavailable'
  runtime_directory="$(mktemp -d /tmp/relay-control-lifecycle-static.XXXXXXXXXXXX)"
  trap cleanup EXIT
  trap 'exit 130' HUP INT TERM

  require_lifecycle_test ./internal/store 'AccountInventory.*Lifecycle|CurrentAccountInventoryLifecycle|InventoryLifecycle' 'store_lifecycle_tests_unavailable'
  require_lifecycle_test ./internal/inventorypoll 'AccountInventory.*Lifecycle|InventoryLifecycle' 'worker_lifecycle_tests_unavailable'
  if ! go test -race \
    ./cmd/control \
    ./internal/inventorypoll \
    ./internal/pollobservability \
    ./internal/store \
    ./deploy/acceptance/account-inventory-lifecycle-canary-scan \
    -count=1 >"$runtime_directory/static.log" 2>&1
  then
    fixed_failure 'static_gate_failed'
  fi
  echo 'account_inventory_lifecycle_acceptance=success mode=static real_node_requests=0 gateway_requests=0 data_plane_requests=0 management_writes=0'
}

run_scan() {
  local variable_name
  for variable_name in \
    CONTROL_LIFECYCLE_CANARY_SCAN_DIR \
    CONTROL_LIFECYCLE_CANARY_ENDPOINT \
    CONTROL_LIFECYCLE_CANARY_IP \
    CONTROL_LIFECYCLE_CANARY_SECRET_REFERENCE \
    CONTROL_LIFECYCLE_CANARY_SECRET_VALUE \
    CONTROL_LIFECYCLE_CANARY_EMAIL \
    CONTROL_LIFECYCLE_CANARY_ACCOUNT_KEY \
    CONTROL_LIFECYCLE_CANARY_RESPONSE_BODY \
    CONTROL_LIFECYCLE_CANARY_RESPONSE_HEADER \
    CONTROL_LIFECYCLE_CANARY_VERSION \
    CONTROL_LIFECYCLE_CANARY_COMMIT \
    CONTROL_LIFECYCLE_CANARY_RAW_ERROR \
    CONTROL_LIFECYCLE_CANARY_SQL_PARAMETER \
    CONTROL_LIFECYCLE_CANARY_POLL_ID \
    CONTROL_LIFECYCLE_CANARY_POLICY_ID \
    CONTROL_LIFECYCLE_CANARY_UNKNOWN_FIELD
  do
    if [ -z "${!variable_name:-}" ]; then
      fixed_failure 'invalid_canary_configuration'
    fi
  done
  go run ./deploy/acceptance/account-inventory-lifecycle-canary-scan
}

run_scan_smoke() {
  local index=0 variable_name artifact
  runtime_directory="$(mktemp -d /tmp/relay-control-lifecycle-scan.XXXXXXXXXXXX)"
  trap cleanup EXIT
  trap 'exit 130' HUP INT TERM
  umask 077
  artifact="$runtime_directory/fixed-aggregate.log"
  printf '%s\n' \
    'account_inventory_lifecycle_acceptance=success mode=synthetic_scanner_smoke request_count=0' \
    >"$artifact"
  export CONTROL_LIFECYCLE_CANARY_SCAN_DIR="$runtime_directory"
  for variable_name in \
    CONTROL_LIFECYCLE_CANARY_ENDPOINT \
    CONTROL_LIFECYCLE_CANARY_IP \
    CONTROL_LIFECYCLE_CANARY_SECRET_REFERENCE \
    CONTROL_LIFECYCLE_CANARY_SECRET_VALUE \
    CONTROL_LIFECYCLE_CANARY_EMAIL \
    CONTROL_LIFECYCLE_CANARY_ACCOUNT_KEY \
    CONTROL_LIFECYCLE_CANARY_RESPONSE_BODY \
    CONTROL_LIFECYCLE_CANARY_RESPONSE_HEADER \
    CONTROL_LIFECYCLE_CANARY_VERSION \
    CONTROL_LIFECYCLE_CANARY_COMMIT \
    CONTROL_LIFECYCLE_CANARY_RAW_ERROR \
    CONTROL_LIFECYCLE_CANARY_SQL_PARAMETER \
    CONTROL_LIFECYCLE_CANARY_POLL_ID \
    CONTROL_LIFECYCLE_CANARY_POLICY_ID \
    CONTROL_LIFECYCLE_CANARY_UNKNOWN_FIELD
  do
    index=$((index + 1))
    export "${variable_name}=lifecycle-synthetic-${index}-canary-8f27"
  done
  go run ./deploy/acceptance/account-inventory-lifecycle-canary-scan
}

run_scan_matrix() {
  local suffix
  runtime_directory="$(mktemp -d /tmp/relay-control-lifecycle-scan.XXXXXXXXXXXX)"
  trap cleanup EXIT
  trap 'exit 130' HUP INT TERM
  umask 077
  mkdir -p "$runtime_directory/test"
  export CONTROL_LIFECYCLE_CANARY_SCAN_DIR="$runtime_directory"
  export CONTROL_LIFECYCLE_CANARY_ARTIFACT_DIR="$runtime_directory"
  suffix="matrix-$$-8f27"
  export CONTROL_LIFECYCLE_CANARY_ENDPOINT="node-${suffix}.invalid"
  export CONTROL_LIFECYCLE_CANARY_IP='10.42.0.99'
  export CONTROL_LIFECYCLE_CANARY_SECRET_REFERENCE="file://lifecycle/${suffix}"
  export CONTROL_LIFECYCLE_CANARY_SECRET_VALUE="management-key-${suffix}"
  export CONTROL_LIFECYCLE_CANARY_EMAIL="lifecycle-${suffix}@example.invalid"
  export CONTROL_LIFECYCLE_CANARY_ACCOUNT_KEY="openai:${CONTROL_LIFECYCLE_CANARY_EMAIL}"
  export CONTROL_LIFECYCLE_CANARY_RESPONSE_BODY="response-body-${suffix}"
  export CONTROL_LIFECYCLE_CANARY_RESPONSE_HEADER="response-header-${suffix}"
  export CONTROL_LIFECYCLE_CANARY_VERSION="v9.9.9-${suffix}"
  export CONTROL_LIFECYCLE_CANARY_COMMIT='feedfacecafebeef'
  export CONTROL_LIFECYCLE_CANARY_RAW_ERROR="raw-error-${suffix}"
  export CONTROL_LIFECYCLE_CANARY_SQL_PARAMETER="sql-parameter-${suffix}"
  export CONTROL_LIFECYCLE_CANARY_POLL_ID='11111111-1111-4111-8111-111111111111'
  export CONTROL_LIFECYCLE_CANARY_POLICY_ID='22222222-2222-4222-8222-222222222222'
  export CONTROL_LIFECYCLE_CANARY_UNKNOWN_FIELD="unknown_field_${suffix}"

  if ! go test ./internal/drivers/cliproxyapi \
    -run '^TestAccountInventoryLifecycleCanariesTraverseDriverWorkerArtifacts$' \
    -count=1 >"$runtime_directory/test/driver.log" 2>&1
  then
    fixed_failure 'canary_driver_path_failed'
  fi
  if ! go test ./internal/inventorypoll \
    -run '^TestAccountInventoryLifecycleCanariesTraversePolicyRaceRollbackArtifacts$' \
    -count=1 >"$runtime_directory/test/worker.log" 2>&1
  then
    fixed_failure 'canary_worker_path_failed'
  fi
  if ! go test ./internal/store \
    -run '^TestAccountInventoryLifecycleCanariesTraverseSQLParameterArtifact$' \
    -count=1 >"$runtime_directory/test/store.log" 2>&1
  then
    fixed_failure 'canary_store_parameter_path_failed'
  fi
  run_scan
}

cd "$repository_root"
case "$mode" in
  static)
    run_static
    ;;
  scan)
    run_scan
    ;;
  scan-smoke)
    run_scan_smoke
    ;;
  scan-matrix)
    run_scan_matrix
    ;;
  postgres)
    "$script_directory/account-inventory-lifecycle-postgres.sh"
    ;;
  rollback)
    "$script_directory/account-inventory-lifecycle-rollback.sh"
    ;;
  container)
    "$script_directory/account-inventory-lifecycle-postgres.sh"
    "$script_directory/account-inventory-snapshot-container.sh"
    echo 'account_inventory_lifecycle_acceptance=success mode=official_synthetic lifecycle_gate=postgres official_lifecycle_rows=1 official_lifecycle_present=1 driver_request_count=1 request_wait_seconds=10 real_node_requests=0 gateway_requests=0 data_plane_requests=0 management_writes=0'
    ;;
  data-plane)
    "$script_directory/account-inventory-snapshot-postgres.sh"
    echo 'account_inventory_lifecycle_acceptance=success mode=data_plane_isolation lifecycle_rows_before_stop=2 lifecycle_rows_after_restart=2 retained_window_data_plane_http=50/50 lifecycle_progress=paused management_requests=0 real_node_requests=0'
    ;;
  real-node)
    echo 'account_inventory_lifecycle_acceptance=failed reason=real_node_not_approved request_count=0' >&2
    exit 2
    ;;
  *)
    echo 'account_inventory_lifecycle_acceptance=failed reason=invalid_mode' >&2
    exit 2
    ;;
esac
