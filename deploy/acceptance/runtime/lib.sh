#!/usr/bin/env bash

# Shared mechanical acceptance-runtime helpers.  Callers keep fixture and
# business-specific assertions in run.sh.

wait_for_http() {
  local layer="$1" url="$2" attempts="$3" attempt=0 failure_layer
  failure_layer="${layer%_READINESS}"
  failure_layer="${failure_layer,,}"
  while (( attempt < attempts )); do
    attempt=$((attempt + 1))
    if curl --noproxy '*' -ksSf --max-time 2 "$url" >/dev/null; then
      return 0
    fi
    sleep 1
  done
  echo "ACCEPTANCE_FAILURE_LAYER=$failure_layer" >&2
  echo "${layer}_READINESS_FAILURE target=$url elapsed=${attempt}s last_observed=unhealthy" >&2
  return 1
}

wait_for_node_ready() {
  local layer="$1" node_container="$2" attempts="$3" attempt=0 state health
  while (( attempt < attempts )); do
    attempt=$((attempt + 1))
    state="$(docker inspect "$node_container" --format '{{.State.Status}}' 2>/dev/null || true)"
    health="$(docker inspect "$node_container" --format '{{if .State.Health}}{{.State.Health.Status}}{{end}}' 2>/dev/null || true)"
    [[ "$health" == "healthy" ]] && return 0
    if [[ "$state" == "exited" || "$state" == "dead" ]]; then
      echo "ACCEPTANCE_FAILURE_LAYER=node" >&2
      echo "${layer}_READINESS_FAILURE target=$node_container elapsed=${attempt}s last_observed=state:$state health:${health:-unknown}" >&2
      compose logs --no-color node >&2 || true
      return 1
    fi
    sleep 1
  done
  echo "ACCEPTANCE_FAILURE_LAYER=node" >&2
  echo "${layer}_READINESS_FAILURE target=$node_container elapsed=${attempt}s last_observed=state:${state:-unknown} health:${health:-unknown}" >&2
  compose logs --no-color node >&2 || true
  return 1
}

psql_scalar() {
  local result
  if ! result="$(compose exec -T postgres psql --username relay_control_migrator --dbname relay_station_control \
    --tuples-only --no-align --command "$1" | tr -d '\r\n[:space:]')"; then
    echo "ACCEPTANCE_FAILURE_LAYER=db_assertion" >&2
    echo "DB_ASSERTION_FAILURE query_execution=failed" >&2
    return 1
  fi
  printf '%s' "$result"
}

psql_count() {
  ACCEPTANCE_FAILURE_LAYER=db_assertion
  psql_scalar "$1"
}

native_mutation_count() {
  ACCEPTANCE_FAILURE_LAYER=mutation
  local log_file="$1" method="$2" path="$3"
  grep -Ec "${method} ${path} [0-9]{3}$" "$log_file" 2>/dev/null || true
}

assert_native_mutation_count() {
  local label="$1" expected="$2" log_file="$3" method="$4" path="$5" actual
  actual="$(native_mutation_count "$log_file" "$method" "$path")"
  [[ "$actual" == "$expected" ]] || {
    echo "ACCEPTANCE_FAILURE_LAYER=mutation" >&2
    echo "NATIVE_MUTATION_COUNT_FAILURE label=$label expected=$expected actual=${actual:-0}" >&2
    return 1
  }
}

capture_compose_logs() {
  local output="$1"
  shift
  mkdir -p "$(dirname -- "$output")"
  if ! compose logs --no-color "$@" >"$output"; then
    echo "ACCEPTANCE_FAILURE_LAYER=evidence" >&2
    echo "COMPOSE_LOG_CAPTURE_FAILURE output=$output" >&2
    return 1
  fi
}

run_browser_spec() {
  local label="$1" output="$2"
  shift 2
  ACCEPTANCE_FAILURE_LAYER=browser
  set +e
  CONTROL_E2E_BROWSER_CHANNEL=chromium \
    CONTROL_E2E_LOCALE="${CONTROL_E2E_LOCALE:-zh-CN}" \
    npm --prefix "$CONTROL_DIR/web" run test:e2e -- "$@" 2>&1 | tee "$output"
  local status="${PIPESTATUS[0]}"
  set -e
  if [[ "$status" != 0 ]]; then
    echo "ACCEPTANCE_FAILURE_LAYER=browser" >&2
    echo "BROWSER_TEST_FAILURE mode=$label" >&2
    compose logs --no-color control node node-counter >&2 || true
  fi
  return "$status"
}

secret_scan_targets() {
  printf '%s\n' \
    "$RUNTIME_DIR/logs" \
    "$RUNTIME_DIR/node/logs" \
    "$RUNTIME_DIR/playwright-output" \
    "$RUNTIME_DIR/browser-console.log" \
    "$RUNTIME_DIR/acceptance-output.log" \
    "$RUNTIME_DIR/fixture-output.log" \
    "$RUNTIME_DIR/enable-output.log" \
    "$RUNTIME_DIR/enable-fixture-evidence.json" \
    "$RUNTIME_DIR/enable-evidence.json" \
    "$RUNTIME_DIR/upload-evidence.json" \
    "$RUNTIME_DIR/disable-evidence.json" \
    "$RUNTIME_DIR/replace-evidence.json" \
    "$RUNTIME_DIR/remove-evidence.json" \
    "$RUNTIME_DIR/override-evidence.json" \
    "$RUNTIME_DIR/security-replay-evidence.json"
}

scan_secret_value() {
  ACCEPTANCE_FAILURE_LAYER=secret
  local category="$1" value="$2" target
  [[ -n "$value" ]] || return 0
  while IFS= read -r target; do
    [[ -e "$target" ]] || continue
    if rg -l -F -- "$value" "$target" >/dev/null 2>&1; then
      echo "ACCEPTANCE_FAILURE_LAYER=secret" >&2
      echo "SECRET_SCAN_FAILURE category=$category location=<redacted>" >&2
      return 1
    fi
  done < <(secret_scan_targets)
}

scan_secret_file() {
  ACCEPTANCE_FAILURE_LAYER=secret
  local category="$1" secret_file="$2"
  local -a targets
  mapfile -t targets < <(secret_scan_targets)
  python3 - "$category" "$secret_file" "${targets[@]}" <<'PY'
import pathlib
import sys

category, secret_file, *targets = sys.argv[1:]
needle = pathlib.Path(secret_file).read_bytes()
for target in targets:
    path = pathlib.Path(target)
    paths = path.rglob("*") if path.is_dir() else (path,)
    for candidate in paths:
        if candidate.is_file():
            try:
                if needle in candidate.read_bytes():
                    print("ACCEPTANCE_FAILURE_LAYER=secret", file=sys.stderr)
                    print(f"SECRET_SCAN_FAILURE category={category} location=<redacted>", file=sys.stderr)
                    raise SystemExit(1)
            except OSError:
                pass
PY
}
