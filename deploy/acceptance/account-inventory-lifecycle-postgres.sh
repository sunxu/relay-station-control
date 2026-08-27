#!/usr/bin/env bash
set -euo pipefail

unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
export NO_PROXY='*' no_proxy='*'
export GOPROXY="${CONTROL_LIFECYCLE_ACCEPTANCE_GOPROXY:-https://goproxy.cn,direct}"
export GOCACHE="${CONTROL_LIFECYCLE_ACCEPTANCE_GOCACHE:-${TMPDIR:-/tmp}/relay-control-lifecycle-acceptance-go-build}"
export npm_config_registry='https://registry.npmmirror.com'

script_directory="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
repository_root="$(CDPATH='' cd -- "$script_directory/../.." && pwd)"
compose_file="$script_directory/account-inventory-snapshot-postgres.compose.yaml"
runtime_directory="$(mktemp -d /tmp/relay-control-lifecycle-postgres.XXXXXXXXXXXX)"
project_name="relay-control-lifecycle-pg-$$"
export CONTROL_SNAPSHOT_POSTGRES_PROJECT="$project_name"

fixed_failure() {
  echo "account_inventory_lifecycle_postgres=failed reason=$1 request_count=0" >&2
  exit 1
}

compose() {
  docker compose --project-name "$project_name" --file "$compose_file" "$@"
}

cleanup() {
  local exit_code=$?
  trap - EXIT HUP INT TERM
  compose down --volumes --remove-orphans >/dev/null 2>&1 || true
  case "$runtime_directory" in
    /tmp/relay-control-lifecycle-postgres.*|/private/tmp/relay-control-lifecycle-postgres.*)
      rm -rf -- "$runtime_directory"
      ;;
  esac
  return "$exit_code"
}

database_port() {
  local published port
  published="$(compose port postgres 5432 2>/dev/null)" || fixed_failure 'postgres_port_unavailable'
  port="${published##*:}"
  case "$port" in
    ''|*[!0-9]*) fixed_failure 'postgres_port_invalid' ;;
  esac
  printf '%s\n' "$port"
}

run_policy_mutation_switch_gate() {
  local template="$repository_root/deploy/asset-registry/activate-provider-policy.sql"
  local before after unchanged status setting log
  compose exec -T postgres psql --username relay_control_migrator \
    --dbname relay_station_control --command \
    "INSERT INTO node_drivers(node_type,driver_contract_version,display_name) VALUES ('policy-switch-test','v1','Policy Switch Test Driver')" \
    >/dev/null 2>&1 || fixed_failure 'policy_switch_fixture_failed'
  before="$(compose exec -T postgres psql --username relay_control_migrator \
    --dbname relay_station_control --tuples-only --no-align \
    --command "SELECT (SELECT count(*) FROM provider_inventory_policy_versions)::text || '|' || (SELECT count(*) FROM account_inventory_scope_transition_audits)::text" \
    2>/dev/null)" || fixed_failure 'policy_switch_count_failed'
  if [ "$before" != '0|0' ]; then
    fixed_failure 'policy_switch_fixture_not_empty'
  fi

  for setting in unset false invalid-reason true; do
    log="$runtime_directory/policy-switch-${setting}.log"
    set +e
    if [ "$setting" = unset ]; then
      compose exec -T \
        -e PGPASSWORD=relay_control_asset_registrar_dev_only \
        postgres psql -X --set=ON_ERROR_STOP=on \
        --username relay_control_asset_registrar_dev --dbname relay_station_control \
        --set=node_type=policy-switch-test \
        --set=driver_contract_version=v1 \
        --set=active_providers_csv=openai \
        --set=out_of_scope_providers_csv=legacy \
        --set=actor=acceptance \
        --set=reason=policy-switch-acceptance \
        --set=effective_at= <"$template" >"$log" 2>&1
      status=$?
    elif [ "$setting" = invalid-reason ]; then
      compose exec -T \
        -e PGPASSWORD=relay_control_asset_registrar_dev_only \
        -e CONTROL_PROVIDER_POLICY_MUTATION_ENABLED=true \
        postgres psql -X --set=ON_ERROR_STOP=on --set=VERBOSITY=verbose \
        --username relay_control_asset_registrar_dev --dbname relay_station_control \
        --set=node_type=policy-switch-test \
        --set=driver_contract_version=v1 \
        --set=active_providers_csv=openai \
        --set=out_of_scope_providers_csv=legacy \
        --set=actor=acceptance \
        --set=reason= \
        --set=effective_at= <"$template" >"$log" 2>&1
      status=$?
    elif [ "$setting" = true ]; then
      unchanged="$(compose exec -T postgres psql --username relay_control_migrator \
        --dbname relay_station_control --tuples-only --no-align \
        --command "SELECT (SELECT count(*) FROM provider_inventory_policy_versions)::text || '|' || (SELECT count(*) FROM account_inventory_scope_transition_audits)::text" \
        2>/dev/null)" || fixed_failure 'policy_switch_count_failed'
      if [ "$unchanged" != "$before" ]; then
        fixed_failure 'policy_switch_disabled_changed_state'
      fi
      compose exec -T \
        -e PGPASSWORD=relay_control_asset_registrar_dev_only \
        -e CONTROL_PROVIDER_POLICY_MUTATION_ENABLED=true \
        postgres psql -X --set=ON_ERROR_STOP=on \
        --username relay_control_asset_registrar_dev --dbname relay_station_control \
        --set=node_type=policy-switch-test \
        --set=driver_contract_version=v1 \
        --set=active_providers_csv=openai \
        --set=out_of_scope_providers_csv=legacy \
        --set=actor=acceptance \
        --set=reason=policy-switch-acceptance \
        --set=effective_at= <"$template" >/dev/null 2>&1
      status=$?
    else
      compose exec -T \
        -e PGPASSWORD=relay_control_asset_registrar_dev_only \
        -e "CONTROL_PROVIDER_POLICY_MUTATION_ENABLED=$setting" \
        postgres psql -X --set=ON_ERROR_STOP=on \
        --username relay_control_asset_registrar_dev --dbname relay_station_control \
        --set=node_type=policy-switch-test \
        --set=driver_contract_version=v1 \
        --set=active_providers_csv=openai \
        --set=out_of_scope_providers_csv=legacy \
        --set=actor=acceptance \
        --set=reason=policy-switch-acceptance \
        --set=effective_at= <"$template" >"$log" 2>&1
      status=$?
    fi
    set -e
    case "$setting:$status" in
      unset:3|false:3|invalid-reason:3|true:0) ;;
      *) fixed_failure "policy_switch_${setting}_exit_${status}_invalid" ;;
    esac
    case "$setting" in
      unset|false)
        grep -Fq 'Provider policy mutation is disabled' "$log" || fixed_failure 'policy_switch_disabled_classification_missing'
        ;;
      invalid-reason)
        grep -Fq 'Provider policy reason is invalid' "$log" || fixed_failure 'policy_reason_classification_missing'
        ;;
      true) ;;
    esac
  done

  for setting in all-out-of-scope all-active; do
    set +e
    if [ "$setting" = all-out-of-scope ]; then
      compose exec -T \
        -e PGPASSWORD=relay_control_asset_registrar_dev_only \
        -e CONTROL_PROVIDER_POLICY_MUTATION_ENABLED=true \
        postgres psql -X --set=ON_ERROR_STOP=on \
        --username relay_control_asset_registrar_dev --dbname relay_station_control \
        --set=node_type=policy-switch-test \
        --set=driver_contract_version=v1 \
        --set=active_providers_csv= \
        --set=out_of_scope_providers_csv=openai,legacy \
        --set=actor=acceptance \
        --set=reason=policy-switch-empty-active-acceptance \
        --set=effective_at= <"$template" >/dev/null 2>&1
      status=$?
    else
      compose exec -T \
        -e PGPASSWORD=relay_control_asset_registrar_dev_only \
        -e CONTROL_PROVIDER_POLICY_MUTATION_ENABLED=true \
        postgres psql -X --set=ON_ERROR_STOP=on \
        --username relay_control_asset_registrar_dev --dbname relay_station_control \
        --set=node_type=policy-switch-test \
        --set=driver_contract_version=v1 \
        --set=active_providers_csv=openai,legacy \
        --set=out_of_scope_providers_csv= \
        --set=actor=acceptance \
        --set=reason=policy-switch-empty-out-of-scope-acceptance \
        --set=effective_at= <"$template" >/dev/null 2>&1
      status=$?
    fi
    set -e
    if [ "$status" -ne 0 ]; then
      fixed_failure "policy_switch_${setting}_exit_${status}_invalid"
    fi
  done

  after="$(compose exec -T postgres psql --username relay_control_migrator \
    --dbname relay_station_control --tuples-only --no-align \
    --command "SELECT (SELECT count(*) FROM provider_inventory_policy_versions)::text || '|' || (SELECT count(*) FROM account_inventory_scope_transition_audits)::text" \
    2>/dev/null)" || fixed_failure 'policy_switch_count_failed'
  if [ "$after" != '3|2' ]; then
    fixed_failure 'policy_switch_enabled_state_invalid'
  fi
}

main() {
  local port test_list test_name
  trap cleanup EXIT
  trap 'exit 130' HUP INT TERM
  command -v docker >/dev/null 2>&1 || fixed_failure 'required_command_unavailable'
  command -v go >/dev/null 2>&1 || fixed_failure 'required_command_unavailable'

  if ! compose up --detach --wait postgres >/dev/null 2>&1; then
    fixed_failure 'postgres_start_failed'
  fi
  port="$(database_port)"
  export CONTROL_DATABASE_TEST_URL="postgres://relay_control_migrator:relay_control_migrator_dev_only@127.0.0.1:${port}/relay_station_control?sslmode=disable"
  export CONTROL_RUNTIME_DATABASE_TEST_URL="postgres://relay_control_app_dev:relay_control_runtime_dev_only@127.0.0.1:${port}/relay_station_control?sslmode=disable"

  if ! (
    cd "$repository_root/tools"
    go tool goose -dir ../migrations postgres "$CONTROL_DATABASE_TEST_URL" up
  ) >"$runtime_directory/migration.log" 2>&1
  then
    fixed_failure 'migration_failed'
  fi
  if ! compose exec -T postgres psql --username relay_control_migrator --dbname relay_station_control \
    --tuples-only --no-align --command 'SHOW server_version_num' >"$runtime_directory/server-version.log" 2>&1
  then
    fixed_failure 'postgres_version_check_failed'
  fi
  if ! grep -Eq '^18[0-9]{4}$' "$runtime_directory/server-version.log"; then
    fixed_failure 'postgres_major_invalid'
  fi
  run_policy_mutation_switch_gate

  test_list="$runtime_directory/lifecycle-tests.list"
  if ! (
    cd "$repository_root"
    go test ./internal/store -list '^TestAccountInventoryLifecycle'
  ) >"$test_list" 2>&1
  then
    fixed_failure 'lifecycle_test_discovery_failed'
  fi
  for test_name in \
    TestAccountInventoryLifecycleConsecutiveMissingRecoveryAndSaturation \
    TestAccountInventoryLifecycleProviderOutOfScopeAndPartialReactivation \
    TestAccountInventoryLifecyclePermissionsAndProtectedWrites \
    TestAccountInventoryLifecycleMigrationDoesNotBackfillSnapshotHistory \
    TestAccountInventoryLifecycleFakeDriverRealStoreSequence \
    TestAccountInventoryLifecycleUncommittedTerminationRollsBackMissingAndUpsert \
    TestAccountInventoryLifecycleCommitUnknownReplayIsSingleState \
    TestAccountInventoryLifecycleCapacityOneTenFifty
  do
    if ! grep -Fxq "$test_name" "$test_list"; then
      fixed_failure 'lifecycle_implementation_unavailable'
    fi
  done
  if ! (
    cd "$repository_root"
    CONTROL_LIFECYCLE_CAPACITY_ACCEPTANCE=1 \
      go test ./internal/store -run '^TestAccountInventoryLifecycle' -count=1
  ) >"$runtime_directory/store.log" 2>&1
  then
    fixed_failure 'lifecycle_store_gate_failed'
  fi

  echo 'account_inventory_lifecycle_postgres=success server_major=18 migration_no_backfill=covered baseline=covered consecutive_missing=covered recovery=covered out_of_scope=covered re_add=covered fake_driver_real_store=covered uncommitted_termination=covered commit_unknown=covered permissions=covered policy_mutation_switch=covered capacity_1_10_50=covered request_count=0 gateway_requests=0 data_plane_requests=0'
}

main "$@"
