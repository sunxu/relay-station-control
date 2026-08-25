#!/usr/bin/env bash
set -euo pipefail

unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
CONTROL_DIR="$(cd -- "${SCRIPT_DIR}/../.." && pwd)"
WORKSPACE_DIR="$(cd -- "${CONTROL_DIR}/.." && pwd)"
COMPOSE_FILE="${SCRIPT_DIR}/compose.yaml"

CONTROL_E2E_IMAGE="${CONTROL_E2E_IMAGE:-relay-station/control:auth-e2e}"
CONTROL_E2E_PROJECT="${CONTROL_E2E_PROJECT:-relay-control-auth-e2e}"
CONTROL_E2E_CONTAINER="${CONTROL_E2E_CONTAINER:-relay-control-auth-e2e}"
CONTROL_E2E_PORT="${CONTROL_E2E_PORT:-18080}"
CONTROL_E2E_DB_PORT="${CONTROL_E2E_DB_PORT:-55433}"
CONTROL_E2E_TLS_PORT="${CONTROL_E2E_TLS_PORT:-18443}"

usage() {
  echo "Usage: CONTROL_E2E_RUNTIME_DIR=/absolute/private/path $0 [all|container|playwright|data-plane]"
  echo
  echo "The runtime directory must be outside the workspace and is deleted only when"
  echo "CONTROL_E2E_CLEAN_RUNTIME=1 is explicitly set. No secret value is printed."
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || { echo "Required command not found: $1" >&2; exit 1; }
}

runtime_dir() {
  if [[ -z "${CONTROL_E2E_RUNTIME_DIR:-}" || "${CONTROL_E2E_RUNTIME_DIR}" != /* ]]; then
    echo "CONTROL_E2E_RUNTIME_DIR must be an explicit absolute path" >&2
    exit 1
  fi
  case "${CONTROL_E2E_RUNTIME_DIR%/}/" in
    "${WORKSPACE_DIR%/}/"*)
      echo "CONTROL_E2E_RUNTIME_DIR must be outside ${WORKSPACE_DIR}" >&2
      exit 1
      ;;
  esac
  printf '%s\n' "${CONTROL_E2E_RUNTIME_DIR%/}"
}

init_runtime() {
  local root key
  root="$(runtime_dir)"
  umask 077
  mkdir -p "$root"
  chmod 700 "$root"

  if [[ ! -e "$root/bootstrap-secret" ]]; then
    openssl rand -hex 32 > "$root/bootstrap-secret"
  fi
  if [[ ! -e "$root/auth-keyring.json" ]]; then
    key="$(openssl rand -base64 32 | tr -d '\r\n=')"
    printf '{"format_version":1,"environment":"production","current":1,"keys":[{"version":1,"key":"%s"}]}\n' "$key" > "$root/auth-keyring.json"
    unset key
  fi
  if [[ ! -e "$root/admin-password" ]]; then
    openssl rand -base64 36 | tr -d '\r\n' > "$root/admin-password"
  fi
  if [[ ! -e "$root/second-admin-password" ]]; then
    openssl rand -base64 36 | tr -d '\r\n' > "$root/second-admin-password"
  fi
  if [[ ! -e "$root/tls.key" || ! -e "$root/tls.crt" ]]; then
    openssl req -x509 -newkey rsa:2048 -sha256 -nodes -days 1 \
      -subj '/CN=localhost' \
      -addext 'subjectAltName=DNS:localhost' \
      -addext 'basicConstraints=critical,CA:FALSE' \
      -addext 'keyUsage=critical,digitalSignature,keyEncipherment' \
      -addext 'extendedKeyUsage=serverAuth' \
      -keyout "$root/tls.key" -out "$root/tls.crt" >/dev/null 2>&1
  fi
  chmod 400 "$root/bootstrap-secret" "$root/auth-keyring.json" "$root/admin-password" "$root/second-admin-password" "$root/tls.key"
  chmod 444 "$root/tls.crt"
}

compose() {
  CONTROL_E2E_RUNTIME_DIR="$(runtime_dir)" \
  CONTROL_E2E_IMAGE="$CONTROL_E2E_IMAGE" \
  CONTROL_E2E_PROJECT="$CONTROL_E2E_PROJECT" \
  CONTROL_E2E_CONTAINER="$CONTROL_E2E_CONTAINER" \
  CONTROL_E2E_PORT="$CONTROL_E2E_PORT" \
  CONTROL_E2E_DB_PORT="$CONTROL_E2E_DB_PORT" \
  CONTROL_E2E_TLS_PORT="$CONTROL_E2E_TLS_PORT" \
    docker compose -f "$COMPOSE_FILE" "$@"
}

cleanup() {
  local exit_code=$?
  compose down --volumes --remove-orphans >/dev/null 2>&1 || true
  if [[ "${CONTROL_E2E_CLEAN_RUNTIME:-0}" == "1" ]]; then
    local root
    root="$(runtime_dir)"
    case "$root" in
      /tmp/relay-control-auth-e2e.*|/private/tmp/relay-control-auth-e2e.*)
        rm -rf -- "$root"
        ;;
      *)
        echo "Runtime cleanup skipped for non-temporary path: $root" >&2
        ;;
    esac
  fi
  exit "$exit_code"
}

wait_for_http() {
  local attempts=60
  until curl --noproxy '*' --silent --show-error --fail "http://localhost:${CONTROL_E2E_PORT}/api/healthz" >/dev/null; do
    attempts=$((attempts - 1))
    if [[ "$attempts" -eq 0 ]]; then
      echo "Control did not become healthy" >&2
      compose logs --no-color control | sed -E 's#postgres://[^ ]+#postgres://[REDACTED]#g' >&2
      return 1
    fi
    sleep 1
  done
}

wait_for_tls() {
  local attempts=60
  until curl --noproxy '*' --insecure --silent --show-error --fail "https://localhost:${CONTROL_E2E_TLS_PORT}/api/healthz" >/dev/null; do
    attempts=$((attempts - 1))
    if [[ "$attempts" -eq 0 ]]; then
      echo "TLS acceptance proxy did not become healthy" >&2
      return 1
    fi
    sleep 1
  done
}

expect_start_failure() {
  local label="$1"
  shift
  local output exit_code=0
  output="$(docker run --rm --user 65532:65532 -e CONTROL_ENVIRONMENT_ID=development "$@" "$CONTROL_E2E_IMAGE" 2>&1)" || exit_code=$?
  if [[ "$exit_code" -eq 0 ]]; then
    echo "FAIL: $label unexpectedly started" >&2
    return 1
  fi
  if printf '%s' "$output" | grep -Eq 'bootstrap|keyring|secret|cookie|configuration|environment'; then
    echo "PASS: $label rejected without printing secret material"
  else
    echo "FAIL: $label did not return the bounded configuration error" >&2
    return 1
  fi
}

verify_container_contract() {
  local platform image_user effective_user secret_stat headers
  platform="$(docker image inspect "$CONTROL_E2E_IMAGE" --format '{{.Os}}/{{.Architecture}}')"
  image_user="$(docker image inspect "$CONTROL_E2E_IMAGE" --format '{{.Config.User}}')"
  [[ "$platform" == "linux/arm64" ]] || { echo "FAIL: image platform is $platform" >&2; return 1; }
  [[ "$image_user" == "65532:65532" ]] || { echo "FAIL: image user is $image_user" >&2; return 1; }

  effective_user="$(docker exec "$CONTROL_E2E_CONTAINER" id -u):$(docker exec "$CONTROL_E2E_CONTAINER" id -g)"
  [[ "$effective_user" == "65532:65532" ]] || { echo "FAIL: runtime user is $effective_user" >&2; return 1; }
  secret_stat="$(docker exec "$CONTROL_E2E_CONTAINER" stat -c '%u:%g:%a' /run/control-secrets/bootstrap-secret /run/control-secrets/auth-keyring.json)"
  [[ "$secret_stat" == $'65532:65532:400\n65532:65532:400' ]] || { echo "FAIL: secret ownership/mode mismatch" >&2; return 1; }

  headers="$(curl --noproxy '*' --silent --show-error --dump-header - --output /dev/null "http://localhost:${CONTROL_E2E_PORT}/api/bootstrap/status")"
  printf '%s' "$headers" | grep -Eiq '^Cache-Control:[[:space:]]*no-store' || { echo "FAIL: bootstrap status lacks Cache-Control: no-store" >&2; return 1; }
  headers="$(curl --noproxy '*' --silent --show-error --dump-header - --output /dev/null "http://localhost:${CONTROL_E2E_PORT}/")"
  printf '%s' "$headers" | grep -Eiq '^Cache-Control:[[:space:]]*no-store' || { echo "FAIL: authentication shell lacks Cache-Control: no-store" >&2; return 1; }
  echo "PASS: linux/arm64 image, non-root runtime, 65532:0400 secrets, no-store responses"
}

run_negative_configuration_tests() {
  local root bad_dir secret_volume
  root="$(runtime_dir)"
  secret_volume="${CONTROL_E2E_PROJECT}_control-secrets"
  bad_dir="$root/bad-permissions"
  mkdir -p "$bad_dir"
  cp "$root/bootstrap-secret" "$bad_dir/bootstrap-secret"
  cp "$root/auth-keyring.json" "$bad_dir/auth-keyring.json"
  chmod 444 "$bad_dir/bootstrap-secret" "$bad_dir/auth-keyring.json"

  expect_start_failure "missing keyring" \
    -e CONTROL_ENVIRONMENT=production -e CONTROL_HTTP_ADDR=0.0.0.0:8080 \
    -e CONTROL_COOKIE_SECURE=true -e CONTROL_MFA_REQUIRED=true \
    -e CONTROL_AUTH_KEYRING_FILE=/run/secrets/missing \
    -e CONTROL_BOOTSTRAP_SECRET_FILE=/run/secrets/bootstrap-secret \
    -v "$secret_volume:/run/secrets:ro"
  expect_start_failure "missing bootstrap secret" \
    -e CONTROL_ENVIRONMENT=production -e CONTROL_HTTP_ADDR=0.0.0.0:8080 \
    -e CONTROL_COOKIE_SECURE=true -e CONTROL_MFA_REQUIRED=true \
    -e CONTROL_AUTH_KEYRING_FILE=/run/secrets/auth-keyring.json \
    -e CONTROL_BOOTSTRAP_SECRET_FILE=/run/secrets/missing \
    -v "$secret_volume:/run/secrets:ro"
  expect_start_failure "over-wide keyring permissions" \
    -e CONTROL_ENVIRONMENT=production -e CONTROL_HTTP_ADDR=0.0.0.0:8080 \
    -e CONTROL_COOKIE_SECURE=true -e CONTROL_MFA_REQUIRED=true \
    -e CONTROL_AUTH_KEYRING_FILE=/run/input/bad-permissions/auth-keyring.json \
    -v "$root:/run/input:ro"
  expect_start_failure "over-wide bootstrap permissions" \
    -e CONTROL_ENVIRONMENT=production -e CONTROL_HTTP_ADDR=0.0.0.0:8080 \
    -e CONTROL_COOKIE_SECURE=true -e CONTROL_MFA_REQUIRED=true \
    -e CONTROL_AUTH_KEYRING_FILE=/run/secrets/auth-keyring.json \
    -e CONTROL_BOOTSTRAP_SECRET_FILE=/run/input/bad-permissions/bootstrap-secret \
    -v "$secret_volume:/run/secrets:ro" \
    -v "$root:/run/input:ro"
  expect_start_failure "insecure production cookies" \
    -e CONTROL_ENVIRONMENT=production -e CONTROL_HTTP_ADDR=0.0.0.0:8080 \
    -e CONTROL_COOKIE_SECURE=false -e CONTROL_MFA_REQUIRED=true

  expect_start_failure "environment ID mismatch" \
    --network "${CONTROL_E2E_PROJECT}_default" \
    -e CONTROL_ENVIRONMENT_ID=wrong-environment \
    -e CONTROL_ENVIRONMENT=production -e CONTROL_HTTP_ADDR=0.0.0.0:8080 \
    -e CONTROL_COOKIE_SECURE=true -e CONTROL_MFA_REQUIRED=true \
    -e CONTROL_AUTH_KEYRING_FILE=/run/secrets/auth-keyring.json \
    -e CONTROL_BOOTSTRAP_SECRET_FILE=/run/secrets/bootstrap-secret \
    -e "DATABASE_URL=postgres://relay_control_app_dev:relay_control_runtime_dev_only@postgres:5432/relay_station_control?sslmode=disable" \
    -v "$secret_volume:/run/secrets:ro"
  curl --noproxy '*' --silent --show-error --fail "http://localhost:${CONTROL_E2E_PORT}/api/healthz" >/dev/null
  echo "PASS: corrected environment identity remains available after mismatch rejection"
}

migrate_up() {
  DATABASE_URL="postgres://relay_control_migrator:relay_control_migrator_dev_only@127.0.0.1:${CONTROL_E2E_DB_PORT}/relay_station_control?sslmode=disable" \
    make --silent -C "$CONTROL_DIR" migrate-up
}

seed_environment() {
  compose exec -T postgres psql --set ON_ERROR_STOP=1 \
    --username relay_control_migrator --dbname relay_station_control \
    --command "INSERT INTO environments (environment_id, name, environment_type) VALUES ('development', 'Synthetic acceptance', 'production') ON CONFLICT (singleton_id) DO NOTHING"
}

build_and_start() {
  echo "[container] building pinned linux/arm64 image"
  docker build \
    --platform linux/arm64 \
    --build-arg "GOPROXY=${CONTROL_E2E_GOPROXY:-https://proxy.golang.org,direct}" \
    --load \
    --tag "$CONTROL_E2E_IMAGE" \
    "$CONTROL_DIR"
  compose up -d --wait postgres
  migrate_up
  seed_environment
  compose up -d secret-init
  compose up -d control
  wait_for_http
  compose up -d tls
  wait_for_tls
  verify_container_contract
  run_negative_configuration_tests
}

run_playwright() {
  local root output_dir exit_code=0
  root="$(runtime_dir)"
  output_dir="$root/playwright-output"
  rm -rf -- "$output_dir"
  CONTROL_E2E_BASE_URL="https://localhost:${CONTROL_E2E_TLS_PORT}" \
  CONTROL_E2E_CONTAINER="$CONTROL_E2E_CONTAINER" \
  CONTROL_E2E_BOOTSTRAP_SECRET_FILE="$root/bootstrap-secret" \
  CONTROL_E2E_ADMIN_PASSWORD_FILE="$root/admin-password" \
  CONTROL_E2E_SECOND_ADMIN_PASSWORD_FILE="$root/second-admin-password" \
  CONTROL_E2E_PLAYWRIGHT_OUTPUT_DIR="$output_dir" \
  PLAYWRIGHT_HTML_OPEN=never \
    env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
      npm --prefix "$CONTROL_DIR/web" run test:e2e || exit_code=$?
  rm -rf -- "$output_dir"
  return "$exit_code"
}

run_data_plane_smoke() {
  local phase0ctl="${WORKSPACE_DIR}/ops/phase0/phase0ctl"
  if [[ -z "${PHASE0_RUNTIME_DIR:-}" || ! -x "$phase0ctl" ]]; then
    echo "SKIP: data-plane isolation requires PHASE0_RUNTIME_DIR and the existing ops phase0ctl"
    return 0
  fi

  echo "[data-plane] checking existing Node baseline before Control outage"
  PHASE0_RUNTIME_DIR="$PHASE0_RUNTIME_DIR" "$phase0ctl" check
  compose stop tls control postgres
  echo "[data-plane] checking the same Node baseline while Control/PostgreSQL are stopped"
  PHASE0_RUNTIME_DIR="$PHASE0_RUNTIME_DIR" "$phase0ctl" check
  compose up -d --wait postgres
  # The acceptance PostgreSQL uses tmpfs, which is intentionally empty after a
  # container stop/start. Re-apply migrations before restoring the test Control.
  migrate_up
  seed_environment
  compose up -d control
  compose up -d tls
  wait_for_http
  wait_for_tls
  echo "PASS: existing Gateway/Node data plane remained available during Control outage"
}

main() {
  local command="${1:-all}"
  [[ "$command" != "-h" && "$command" != "--help" ]] || { usage; exit 0; }
  require_command docker
  require_command openssl
  require_command curl
  init_runtime
  trap cleanup EXIT

  case "$command" in
    all) build_and_start; run_playwright; run_data_plane_smoke ;;
    container) build_and_start ;;
    playwright) run_playwright ;;
    data-plane) run_data_plane_smoke ;;
    *) usage >&2; exit 2 ;;
  esac
}

main "$@"
