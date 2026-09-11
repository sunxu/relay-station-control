#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
CONTROL_DIR="$(cd -- "$ROOT/../../.." && pwd)"
COMPOSE_FILE="$CONTROL_DIR/deploy/acceptance/compose.yaml"
RUNTIME_DIR="${ACCEPTANCE_RUNTIME_DIR:-}"
OVERRIDE_FILE=""
PROJECT="${ACCEPTANCE_COMPOSE_PROJECT:-relay-control-harness-$$}"
HTTP_PORT="${ACCEPTANCE_HTTP_PORT:-$((19080 + $$ % 500))}"
DB_PORT="${ACCEPTANCE_DB_PORT:-$((19543 + $$ % 500))}"
TLS_PORT="${ACCEPTANCE_TLS_PORT:-$((19443 + $$ % 500))}"
EXPECTED_SHA="${EXPECTED_SHA:-$(git -C "$CONTROL_DIR" rev-parse HEAD)}"
SHORT_SHA="${EXPECTED_SHA:0:12}"
IMAGE="${ACCEPTANCE_IMAGE:-relay-station/control:acceptance-${SHORT_SHA}}"
CONTROL_CONTAINER="${PROJECT}-control-1"
COMPOSE=(docker compose -p "$PROJECT" -f "$COMPOSE_FILE")
[[ -n "$RUNTIME_DIR" && "$RUNTIME_DIR" == /* && "$RUNTIME_DIR" != "$CONTROL_DIR"/* ]] || { echo "ACCEPTANCE_RUNTIME_DIR must be repo-external" >&2; exit 2; }
[[ -z "${DINGTALK_WEBHOOK_URL:-}" && -z "${DINGTALK_SIGNING_SECRET:-}" ]] || { echo "real DingTalk configuration must be absent" >&2; exit 2; }
mkdir -p "$RUNTIME_DIR"; chmod 700 "$RUNTIME_DIR"
cleanup() {
  "${COMPOSE[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true
  CONTROL_E2E_RUNTIME_DIR="$RUNTIME_DIR" CONTROL_E2E_IMAGE="$IMAGE" CONTROL_E2E_CONTAINER="$CONTROL_CONTAINER" CONTROL_E2E_PORT="$HTTP_PORT" CONTROL_E2E_DB_PORT="$DB_PORT" CONTROL_E2E_TLS_PORT="$TLS_PORT" \
    docker compose -p "$PROJECT" -f "$COMPOSE_FILE" down --volumes --remove-orphans >/dev/null 2>&1 || true
  rm -rf -- "$RUNTIME_DIR"
}
trap cleanup EXIT
umask 077
openssl rand -hex 32 > "$RUNTIME_DIR/bootstrap-secret"
key="$(openssl rand -base64 32 | tr -d '\r\n=')"
printf '{"format_version":1,"environment":"production","current":1,"keys":[{"version":1,"key":"%s"}]}\n' "$key" > "$RUNTIME_DIR/auth-keyring.json"
unset key
openssl rand -base64 36 | tr -d '\r\n' > "$RUNTIME_DIR/admin-password"
openssl rand -base64 36 | tr -d '\r\n' > "$RUNTIME_DIR/second-admin-password"
openssl req -x509 -newkey rsa:2048 -sha256 -nodes -days 1 -subj '/CN=localhost' -addext 'subjectAltName=DNS:localhost' -addext 'basicConstraints=critical,CA:FALSE' -addext 'keyUsage=critical,digitalSignature,keyEncipherment' -addext 'extendedKeyUsage=serverAuth' -keyout "$RUNTIME_DIR/tls.key" -out "$RUNTIME_DIR/tls.crt" >/dev/null 2>&1
chmod 400 "$RUNTIME_DIR/bootstrap-secret" "$RUNTIME_DIR/auth-keyring.json" "$RUNTIME_DIR/admin-password" "$RUNTIME_DIR/second-admin-password" "$RUNTIME_DIR/tls.key"
chmod 444 "$RUNTIME_DIR/tls.crt"
OVERRIDE_FILE="$RUNTIME_DIR/compose.override.yaml"
cat > "$OVERRIDE_FILE" <<'YAML'
services:
  control:
    environment:
      CONTROL_GATEWAY_DIRECTORY_ENABLED: "false"
      CONTROL_ACCOUNT_INVENTORY_POLL_ENABLED: "false"
      CONTROL_ACCOUNT_INVENTORY_LIFECYCLE_ENABLED: "false"
      CONTROL_ACCOUNT_REQUEST_QUALITY_ENABLED: "false"
      CONTROL_CLIPROXYAPI_DRIVER_ENABLED: "false"
      CONTROL_ACCOUNT_INVENTORY_HISTORY_ENABLED: "false"
      CONTROL_JOB_WORKER_CONCURRENCY: "1"
      CONTROL_JOB_RECONCILER_CONCURRENCY: "1"
      CONTROL_JOB_POLL_INTERVAL: "1s"
      CONTROL_JOB_RECONCILE_INTERVAL: "1s"
      CONTROL_JOB_DATABASE_BACKOFF: "1s"
      CONTROL_JOB_SHUTDOWN_GRACE: "3s"
YAML
COMPOSE+=( -f "$OVERRIDE_FILE" )
compose() { CONTROL_E2E_RUNTIME_DIR="$RUNTIME_DIR" CONTROL_E2E_IMAGE="$IMAGE" CONTROL_E2E_CONTAINER="$CONTROL_CONTAINER" CONTROL_E2E_PORT="$HTTP_PORT" CONTROL_E2E_DB_PORT="$DB_PORT" CONTROL_E2E_TLS_PORT="$TLS_PORT" "${COMPOSE[@]}" "$@"; }
wait_http() { local url="$1"; for _ in $(seq 1 60); do curl --noproxy '*' -ksSf --max-time 2 "$url" >/dev/null && return 0; sleep 1; done; return 1; }
image_revision() { docker image inspect "$1" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' 2>/dev/null || true; }
ensure_candidate_image() {
  local revision
  revision="$(image_revision "$IMAGE")"
  if [[ "$revision" != "$EXPECTED_SHA" ]]; then
    [[ -n "${ACCEPTANCE_IMAGE:-}" ]] && { echo "candidate_image_mismatch" >&2; exit 1; }
    local buildx_state
    buildx_state="$(mktemp -d /private/tmp/relay-control-buildx.XXXXXX)"
    BUILDX_CONFIG_DIR="$buildx_state" EXPECTED_SHA="$EXPECTED_SHA" IMAGE="$IMAGE" ALLOW_DIRTY=1 "$ROOT/build-image.sh"
    rm -rf -- "$buildx_state"
    revision="$(image_revision "$IMAGE")"
  fi
  [[ "$revision" == "$EXPECTED_SHA" ]] || { echo "candidate_image_mismatch" >&2; exit 1; }
  echo "CANDIDATE_IMAGE_REVISION=PASS"
}
MODE="${1:-all}"
[[ "$MODE" == "all" || "$MODE" == "auth" || "$MODE" == "startup" ]] || { echo "usage: ACCEPTANCE_RUNTIME_DIR=/external/path $0 [all|auth|startup]" >&2; exit 2; }
ensure_candidate_image
compose up -d --wait postgres
DATABASE_URL="postgres://relay_control_migrator:relay_control_migrator_dev_only@127.0.0.1:${DB_PORT}/relay_station_control?sslmode=disable"
DATABASE_URL="$DATABASE_URL" make --silent migrate-up
compose exec -T postgres psql --set ON_ERROR_STOP=1 --username relay_control_migrator --dbname relay_station_control --command "INSERT INTO environments(environment_id,name,environment_type) VALUES ('development','Acceptance harness','production') ON CONFLICT (singleton_id) DO NOTHING" >/dev/null
compose up -d secret-init
compose up -d control
wait_http "http://127.0.0.1:${HTTP_PORT}/api/healthz"
if [[ "$MODE" == "startup" ]]; then
  sleep 3
  wait_http "http://127.0.0.1:${HTTP_PORT}/api/healthz"
  compose stop -t 15 control
  exit_code="$(docker inspect "$CONTROL_CONTAINER" --format '{{.State.ExitCode}}' 2>/dev/null || true)"
  [[ "$exit_code" == "0" ]] || { echo "control did not exit cleanly: ${exit_code:-unknown}" >&2; exit 1; }
  echo "CONTROL_STARTUP_GATE=PASS"
  exit 0
fi
compose up -d tls
wait_http "https://127.0.0.1:${TLS_PORT}/api/healthz"
export ACCEPTANCE_RUNTIME_DIR="$RUNTIME_DIR" ACCEPTANCE_BASE_URL="https://127.0.0.1:${TLS_PORT}" ACCEPTANCE_STORAGE_STATE="$RUNTIME_DIR/storage-state.json" ACCEPTANCE_CONTROL_CONTAINER="$CONTROL_CONTAINER"
CONTROL_E2E_BROWSER_CHANNEL="${CONTROL_E2E_BROWSER_CHANNEL:-chromium}" node "$ROOT/auth-session.mjs"
chmod 600 "$RUNTIME_DIR/storage-state.json"
echo "AUTH_COMPOSITION_SMOKE=PASS"
if [[ "${1:-all}" == "all" ]]; then
  export CONTROL_DATABASE_TEST_URL="$DATABASE_URL"
  export CONTROL_RUNTIME_DATABASE_TEST_URL="postgres://relay_control_app_dev:relay_control_runtime_dev_only@127.0.0.1:${DB_PORT}/relay_station_control?sslmode=disable"
  env -u DINGTALK_WEBHOOK_URL -u DINGTALK_SIGNING_SECRET go test ./internal/store -run '^TestRuntimeAcceptanceLifecycleFixture$' -count=1 -v
  env -u DINGTALK_WEBHOOK_URL -u DINGTALK_SIGNING_SECRET go test ./internal/store -run '^TestRuntimeAcceptanceLifecycleFixture$' -count=1 -v
  echo "LIFECYCLE_REPEATED=PASS"
fi
