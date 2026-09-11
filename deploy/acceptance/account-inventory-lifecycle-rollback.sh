#!/usr/bin/env bash
set -euo pipefail

# This exercise exports the archived snapshot-only source into a disposable
# directory. It never checks out or builds the old revision in the worktree.
unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
export NO_PROXY='*' no_proxy='*'
export GOPROXY="${CONTROL_LIFECYCLE_ACCEPTANCE_GOPROXY:-https://goproxy.cn,direct}"
export npm_config_registry='https://registry.npmmirror.com'

script_directory="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
repository_root="$(CDPATH='' cd -- "$script_directory/../.." && pwd)"
compose_file="$script_directory/account-inventory-snapshot-postgres.compose.yaml"
temporary_root="${TMPDIR:-/tmp}"
temporary_root="${temporary_root%/}"
runtime_directory="$(mktemp -d "$temporary_root/relay-control-lifecycle-rollback.XXXXXXXXXXXX")"
project_name="relay-control-lifecycle-rollback-pg-$$"
old_revision='e482d8eb19896a60b73a4144ee155d1f66a2b1d7'
minimum_forward_schema=7
old_source="$runtime_directory/old-source"
old_archive="$runtime_directory/old-source.tar"
old_binary="$runtime_directory/control-old"
new_binary="$runtime_directory/control-new"
harness="$runtime_directory/rollback-harness"
control_pid=''
old_binary_fail_closed=false
http_port=$((20000 + ($$ % 20000)))
export CONTROL_SNAPSHOT_POSTGRES_PROJECT="$project_name"

fixed_failure() {
  echo "account_inventory_lifecycle_rollback=failed reason=$1 management_requests=0" >&2
  exit 1
}

compose() {
  docker compose --project-name "$project_name" --file "$compose_file" "$@"
}

stop_control() {
  if [ -n "$control_pid" ]; then
    kill -TERM "$control_pid" >/dev/null 2>&1 || true
    wait "$control_pid" >/dev/null 2>&1 || true
    control_pid=''
  fi
}

cleanup() {
  local exit_code=$?
  trap - EXIT HUP INT TERM
  stop_control
  compose down --volumes --remove-orphans >/dev/null 2>&1 || true
  case "$runtime_directory" in
    "$temporary_root"/relay-control-lifecycle-rollback.*)
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

build_binaries() {
  mkdir -p "$old_source"
  git cat-file -e "${old_revision}^{commit}" >/dev/null 2>&1 || fixed_failure 'old_revision_unavailable'
  git archive --format=tar --output="$old_archive" "$old_revision" >/dev/null 2>&1 \
    || fixed_failure 'old_revision_export_failed'
  tar -xf "$old_archive" -C "$old_source" \
    || fixed_failure 'old_revision_export_failed'
  if ! (
    cd "$old_source"
    env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
      go build -trimpath -o "$old_binary" ./cmd/control
  ) >"$runtime_directory/old-build.log" 2>&1
  then
    fixed_failure 'old_binary_build_failed'
  fi
  if ! env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    go build -trimpath -o "$new_binary" ./cmd/control \
    >"$runtime_directory/new-build.log" 2>&1
  then
    fixed_failure 'new_binary_build_failed'
  fi
  if ! env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
    go build -trimpath -o "$harness" ./deploy/acceptance/account-inventory-lifecycle-rollback \
    >"$runtime_directory/harness-build.log" 2>&1
  then
    fixed_failure 'rollback_harness_build_failed'
  fi
}

prepare_auth_files() {
  local key
  openssl rand -hex 32 >"$runtime_directory/bootstrap-secret" \
    || fixed_failure 'auth_fixture_failed'
  key="$(openssl rand -base64 32 | tr -d '=\n')" \
    || fixed_failure 'auth_fixture_failed'
  printf '{"format_version":1,"environment":"dev","current":1,"keys":[{"version":1,"key":"%s"}]}\n' \
    "$key" >"$runtime_directory/auth-keyring.json"
  chmod 0400 "$runtime_directory/bootstrap-secret" "$runtime_directory/auth-keyring.json"
}

start_control() {
  local binary="$1" lifecycle_enabled="$2" log="$3" attempts=100
  stop_control
  env \
    CONTROL_ENVIRONMENT_ID=rollback-acceptance \
    CONTROL_ENVIRONMENT=dev \
    CONTROL_HTTP_ADDR="127.0.0.1:${http_port}" \
    CONTROL_ACCOUNT_INVENTORY_POLL_ENABLED=false \
    CONTROL_ACCOUNT_INVENTORY_LIFECYCLE_ENABLED="$lifecycle_enabled" \
    CONTROL_PROVIDER_POLICY_MUTATION_ENABLED=false \
    CONTROL_JOB_WORKER_CONCURRENCY=1 \
    CONTROL_JOB_RECONCILER_CONCURRENCY=1 \
    CONTROL_BOOTSTRAP_SECRET_FILE="$runtime_directory/bootstrap-secret" \
    CONTROL_AUTH_KEYRING_FILE="$runtime_directory/auth-keyring.json" \
    DATABASE_URL="$CONTROL_LIFECYCLE_ROLLBACK_RUNTIME_URL" \
    "$binary" >"$log" 2>&1 &
  control_pid=$!
  until curl --noproxy '*' --silent --show-error --fail \
    "http://127.0.0.1:${http_port}/api/healthz" \
    >"$runtime_directory/health.json" 2>/dev/null
  do
    if ! kill -0 "$control_pid" >/dev/null 2>&1; then
      if grep -Eq 'registry_mismatch|durable job catalog mismatch' "$log"; then
        if [ "$binary" = "$old_binary" ]; then
          old_binary_fail_closed=true
          return 42
        fi
        fixed_failure 'control_job_catalog_incompatible'
      elif grep -Eq 'environment identity verification failed' "$log"; then
        fixed_failure 'control_environment_incompatible'
      elif grep -Eq 'authentication initialization failed' "$log"; then
        fixed_failure 'control_authentication_incompatible'
      elif grep -Eq 'asset registry initialization failed' "$log"; then
        fixed_failure 'control_asset_read_incompatible'
      elif grep -Eq 'account inventory poll initialization failed' "$log"; then
        fixed_failure 'control_poll_initialization_incompatible'
      elif grep -Eq 'address already in use' "$log"; then
        fixed_failure 'control_http_port_unavailable'
      fi
      fixed_failure 'control_start_failed'
    fi
    attempts=$((attempts - 1))
    if [ "$attempts" -eq 0 ]; then
      fixed_failure 'control_start_timeout'
    fi
    sleep 0.05
  done
}

verify_product_reads() {
  local prefix="$1"
  curl --noproxy '*' --silent --show-error --fail \
    "http://127.0.0.1:${http_port}/api/bootstrap/status" \
    >"$runtime_directory/${prefix}-bootstrap.json" 2>/dev/null \
    || fixed_failure "${prefix}_bootstrap_read_failed"
  curl --noproxy '*' --silent --show-error --fail \
    "http://127.0.0.1:${http_port}/metrics" \
    >"$runtime_directory/${prefix}-metrics.txt" 2>/dev/null \
    || fixed_failure "${prefix}_metrics_read_failed"
}

verify_old_readonly_route_closed() {
  local status response_size
  printf '%s\n' \
    '{"instance_id":"00000000-0000-4000-8000-000000000821","email":"rollback-route-canary@example.invalid","limit":1}' \
    >"$runtime_directory/old-readonly-request.json"
  status="$(curl --noproxy '*' --silent --request POST \
    --header 'Content-Type: application/json' \
    --data-binary "@$runtime_directory/old-readonly-request.json" \
    --output "$runtime_directory/old-readonly-response.txt" \
    --write-out '%{http_code}' --max-time 5 \
    "http://127.0.0.1:${http_port}/api/account-inventory/query" 2>/dev/null)" \
    || fixed_failure 'old_readonly_route_probe_failed'
  if [ "$status" != 404 ]; then
    fixed_failure 'old_readonly_route_not_closed'
  fi
  response_size="$(wc -c <"$runtime_directory/old-readonly-response.txt" | tr -d ' ')"
  case "$response_size" in
    ''|*[!0-9]*) fixed_failure 'old_readonly_route_response_invalid' ;;
  esac
  if [ "$response_size" -gt 4096 ]; then
    fixed_failure 'old_readonly_route_response_unbounded'
  fi
  if grep -Eq 'rollback-route-canary@example.invalid|00000000-0000-4000-8000-000000000821' \
    "$runtime_directory/old-readonly-response.txt"
  then
    fixed_failure 'old_readonly_route_identity_leaked'
  fi
}

lifecycle_fingerprint() {
  compose exec -T postgres psql --username relay_control_migrator \
    --dbname relay_station_control --tuples-only --no-align --command "
      SELECT md5(
        coalesce((SELECT jsonb_agg(to_jsonb(account) ORDER BY account.account_key)::text
          FROM account_inventory AS account
          WHERE account.instance_id='00000000-0000-4000-8000-000000000821'), '[]') ||
        coalesce((SELECT jsonb_agg(to_jsonb(state) ORDER BY state.provider)::text
          FROM account_inventory_provider_states AS state
          WHERE state.instance_id='00000000-0000-4000-8000-000000000821'), '[]') ||
        coalesce((SELECT jsonb_agg(to_jsonb(run) ORDER BY run.scheduled_at)::text
          FROM account_inventory_poll_runs AS run
          WHERE run.instance_id='00000000-0000-4000-8000-000000000821'), '[]') ||
        coalesce((SELECT jsonb_agg(to_jsonb(binding) ORDER BY binding.node_type)::text
          FROM provider_inventory_policy_bindings AS binding
          WHERE binding.node_type='rollback-snapshot-test'), '[]') ||
        coalesce((SELECT jsonb_agg(to_jsonb(audit) ORDER BY audit.transitioned_at)::text
          FROM account_inventory_scope_transition_audits AS audit
          WHERE audit.node_type='rollback-snapshot-test'), '[]')
      )" 2>/dev/null
}

verify_policy_mutation_disabled() {
  local status
  set +e
  compose exec -T \
    -e PGPASSWORD=relay_control_asset_registrar_dev_only \
    -e CONTROL_PROVIDER_POLICY_MUTATION_ENABLED=false \
    postgres psql -X --set=ON_ERROR_STOP=on \
    --username relay_control_asset_registrar_dev --dbname relay_station_control \
    --set=node_type=rollback-snapshot-test \
    --set=driver_contract_version=v1 \
    --set=active_providers_csv=legacy \
    --set=out_of_scope_providers_csv=openai \
    --set=actor=rollback-acceptance \
    --set=reason=rollback-policy-mutation-disabled \
    --set=effective_at= \
    <"$script_directory/../asset-registry/activate-provider-policy.sql" \
    >"$runtime_directory/policy-disabled.log" 2>&1
  status=$?
  set -e
  if [ "$status" -ne 3 ]; then
    fixed_failure 'policy_mutation_disable_failed'
  fi
}

main() {
  local port schema_version before_old after_old before_advance
  umask 077
  trap cleanup EXIT
  trap 'exit 130' HUP INT TERM
  for command_name in curl docker git go grep openssl tar tr wc; do
    command -v "$command_name" >/dev/null 2>&1 || fixed_failure 'required_command_unavailable'
  done

  compose up --detach --wait postgres >/dev/null 2>&1 \
    || fixed_failure 'postgres_start_failed'
  port="$(database_port)"
  export CONTROL_LIFECYCLE_ROLLBACK_OWNER_URL="postgres://relay_control_migrator:relay_control_migrator_dev_only@127.0.0.1:${port}/relay_station_control?sslmode=disable"
  export CONTROL_LIFECYCLE_ROLLBACK_RUNTIME_URL="postgres://relay_control_app_dev:relay_control_runtime_dev_only@127.0.0.1:${port}/relay_station_control?sslmode=disable"
  if ! (
    cd "$repository_root/tools"
    GOOSE_DRIVER=postgres \
      GOOSE_DBSTRING="$CONTROL_LIFECYCLE_ROLLBACK_OWNER_URL" \
      GOOSE_MIGRATION_DIR=../migrations \
      go tool goose up
  ) >"$runtime_directory/migration.log" 2>&1
  then
    fixed_failure 'migration_failed'
  fi
  schema_version="$(compose exec -T postgres psql --username relay_control_migrator \
    --dbname relay_station_control --tuples-only --no-align \
    --command 'SELECT max(version_id) FROM goose_db_version WHERE is_applied' \
    2>/dev/null)" || fixed_failure 'schema_version_unavailable'
  case "$schema_version" in
    ''|*[!0-9]*) fixed_failure 'forward_schema_invalid' ;;
  esac
  if [ "$schema_version" -lt "$minimum_forward_schema" ]; then
    fixed_failure 'forward_schema_invalid'
  fi

  build_binaries
  prepare_auth_files
  if ! "$harness" prepare >"$runtime_directory/prepare.log" 2>&1; then
    if grep -Fq 'reason=prepare_timeout' "$runtime_directory/prepare.log"; then
      fixed_failure 'fixture_prepare_timeout'
    fi
    fixed_failure 'fixture_prepare_failed'
  fi
  before_old="$(lifecycle_fingerprint)" || fixed_failure 'fingerprint_failed'
  case "$before_old" in
    [0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]* ) ;;
    *) fixed_failure 'fingerprint_invalid' ;;
  esac

  if start_control "$old_binary" false "$runtime_directory/old-control.log"; then
    verify_product_reads old
    verify_old_readonly_route_closed
    if grep -Eq '^relay_control_account_inventory_lifecycle_total' \
      "$runtime_directory/old-metrics.txt"
    then
      fixed_failure 'old_binary_not_snapshot_only'
    fi
    verify_policy_mutation_disabled
    "$harness" verify-frozen >"$runtime_directory/old-frozen.log" 2>&1 \
      || fixed_failure 'old_binary_changed_lifecycle'
    stop_control
  elif [ "$old_binary_fail_closed" = true ]; then
    "$harness" verify-frozen >"$runtime_directory/old-fail-closed.log" 2>&1 \
      || fixed_failure 'old_binary_changed_lifecycle'
  else
    fixed_failure 'old_control_start_failed'
  fi
  "$harness" verify-frozen >"$runtime_directory/old-audit-retained.log" 2>&1 \
    || fixed_failure 'old_binary_changed_readonly_audit'
  after_old="$(lifecycle_fingerprint)" || fixed_failure 'fingerprint_failed'
  if [ "$after_old" != "$before_old" ]; then
    fixed_failure 'old_binary_changed_forward_state'
  fi

  start_control "$new_binary" true "$runtime_directory/new-control.log"
  verify_product_reads new
  grep -Eq '^relay_control_account_inventory_lifecycle_total' \
    "$runtime_directory/new-metrics.txt" \
    || fixed_failure 'new_binary_lifecycle_projection_missing'
  before_advance="$(lifecycle_fingerprint)" || fixed_failure 'fingerprint_failed'
  if [ "$before_advance" != "$before_old" ]; then
    fixed_failure 'new_binary_start_changed_forward_state'
  fi
  "$harness" advance >"$runtime_directory/advance.log" 2>&1 \
    || fixed_failure 'next_qualified_poll_failed'
  "$harness" replay >"$runtime_directory/replay.log" 2>&1 \
    || fixed_failure 'finalized_replay_advanced'
  "$harness" verify-advanced >"$runtime_directory/advanced.log" 2>&1 \
    || fixed_failure 'restored_state_invalid'
  curl --noproxy '*' --silent --show-error --fail \
    "http://127.0.0.1:${http_port}/metrics" \
    >"$runtime_directory/new-advanced-metrics.txt" 2>/dev/null \
    || fixed_failure 'new_binary_advanced_metrics_failed'
  grep -Eq '^relay_control_account_inventory_lifecycle_total.*lifecycle="missing".* 1$' \
    "$runtime_directory/new-advanced-metrics.txt" \
    || fixed_failure 'new_binary_missing_projection_invalid'
  stop_control

  if grep -Eq 'rollback-lifecycle@example.invalid|docker-secret://synthetic/rollback-reader' \
    "$runtime_directory/old-control.log" "$runtime_directory/new-control.log" \
    "$runtime_directory/policy-disabled.log"
  then
    fixed_failure 'identity_leaked_to_logs'
  fi
  echo "account_inventory_lifecycle_rollback=success old_revision=e482d8e forward_schema=${schema_version} minimum_forward_schema=${minimum_forward_schema} poll_enabled=false policy_mutation_enabled=false old_product_reads=3 old_readonly_route_requests=1 old_readonly_route_closed=true old_readonly_route_identity_occurrences=0 readonly_view_audits_retained=1 lifecycle_frozen=true next_qualified_poll_advanced=true finalized_replay_advanced=false management_requests=0 real_node_requests=0 gateway_requests=0 data_plane_requests=0"
}

main "$@"
