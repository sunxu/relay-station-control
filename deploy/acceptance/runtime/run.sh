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
NODE_PORT="${ACCEPTANCE_NODE_PORT:-$((19317 + $$ % 500))}"
EXPECTED_SHA="${EXPECTED_SHA:-$(git -C "$CONTROL_DIR" rev-parse HEAD)}"
SHORT_SHA="${EXPECTED_SHA:0:12}"
IMAGE="${ACCEPTANCE_IMAGE:-relay-station/control:acceptance-${SHORT_SHA}}"
CONTROL_CONTAINER="${PROJECT}-control-1"
NODE_MANAGEMENT_PASSWORD="$(openssl rand -hex 32)"
COMPOSE=(docker compose -p "$PROJECT" -f "$COMPOSE_FILE")
[[ -n "$RUNTIME_DIR" && "$RUNTIME_DIR" == /* && "$RUNTIME_DIR" != "$CONTROL_DIR"/* ]] || { echo "ACCEPTANCE_RUNTIME_DIR must be repo-external" >&2; exit 2; }
[[ -z "${DINGTALK_WEBHOOK_URL:-}" && -z "${DINGTALK_SIGNING_SECRET:-}" ]] || { echo "real DingTalk configuration must be absent" >&2; exit 2; }
mkdir -p "$RUNTIME_DIR"; chmod 700 "$RUNTIME_DIR"
cleanup() {
  "${COMPOSE[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true
  CONTROL_E2E_RUNTIME_DIR="$RUNTIME_DIR" CONTROL_E2E_IMAGE="$IMAGE" CONTROL_E2E_CONTAINER="$CONTROL_CONTAINER" CONTROL_E2E_PORT="$HTTP_PORT" CONTROL_E2E_DB_PORT="$DB_PORT" CONTROL_E2E_TLS_PORT="$TLS_PORT" CONTROL_E2E_NODE_PORT="$NODE_PORT" CONTROL_E2E_NODE_MANAGEMENT_PASSWORD="$NODE_MANAGEMENT_PASSWORD" \
    docker compose -p "$PROJECT" -f "$COMPOSE_FILE" down --volumes --remove-orphans >/dev/null 2>&1 || true
  rm -rf -- "$RUNTIME_DIR"
}
trap cleanup EXIT
umask 077
openssl rand -hex 32 > "$RUNTIME_DIR/bootstrap-secret"
openssl rand -out "$RUNTIME_DIR/account-operation-intent-key" 32
printf '%s' "$NODE_MANAGEMENT_PASSWORD" > "$RUNTIME_DIR/node-management-key"
UPLOAD_EMAIL="phase7-${PROJECT##*-}@example.invalid"
UPLOAD_SECRET_MARKER="PHASE7_E2E_SECRET_${PROJECT##*-}"
printf '{"type":"antigravity","email":"%s","phase7_marker":"%s"}\n' "$UPLOAD_EMAIL" "$UPLOAD_SECRET_MARKER" > "$RUNTIME_DIR/upload-credential.json"
mkdir -p "$RUNTIME_DIR/node/auths" "$RUNTIME_DIR/node/logs"
printf '{"type":"antigravity","email":"phase7-seed-%s@example.invalid","access_token":"phase7-disposable-seed-token"}\n' "$PROJECT" > "$RUNTIME_DIR/node/auths/phase7-seed.json"
cat > "$RUNTIME_DIR/node/config.yaml" <<YAML
host: "0.0.0.0"
port: 8317
remote-management:
  allow-remote: true
  secret-key: "$NODE_MANAGEMENT_PASSWORD"
  disable-control-panel: true
auth-dir: "/root/.cli-proxy-api"
logging-to-file: true
request-log: false
YAML
cat > "$RUNTIME_DIR/cliproxyapi-secret-map.json" <<'JSON'
{"provider":"file","references":[{"reference":"file://phase7/node-management","path":"/run/control-secrets/node-management-key"}]}
JSON
key="$(openssl rand -base64 32 | tr -d '\r\n=')"
printf '{"format_version":1,"environment":"production","current":1,"keys":[{"version":1,"key":"%s"}]}\n' "$key" > "$RUNTIME_DIR/auth-keyring.json"
unset key
openssl rand -base64 36 | tr -d '\r\n' > "$RUNTIME_DIR/admin-password"
openssl rand -base64 36 | tr -d '\r\n' > "$RUNTIME_DIR/second-admin-password"
openssl req -x509 -newkey rsa:2048 -sha256 -nodes -days 1 -subj '/CN=localhost' -addext 'subjectAltName=DNS:localhost' -addext 'basicConstraints=critical,CA:FALSE' -addext 'keyUsage=critical,digitalSignature,keyEncipherment' -addext 'extendedKeyUsage=serverAuth' -keyout "$RUNTIME_DIR/tls.key" -out "$RUNTIME_DIR/tls.crt" >/dev/null 2>&1
chmod 400 "$RUNTIME_DIR/bootstrap-secret" "$RUNTIME_DIR/account-operation-intent-key" "$RUNTIME_DIR/node-management-key" "$RUNTIME_DIR/cliproxyapi-secret-map.json" "$RUNTIME_DIR/auth-keyring.json" "$RUNTIME_DIR/admin-password" "$RUNTIME_DIR/second-admin-password" "$RUNTIME_DIR/tls.key"
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
      CONTROL_CLIPROXYAPI_DRIVER_ENABLED: "true"
      CONTROL_ACCOUNT_INVENTORY_HISTORY_ENABLED: "false"
      CONTROL_JOB_WORKER_CONCURRENCY: "1"
      CONTROL_JOB_RECONCILER_CONCURRENCY: "1"
      CONTROL_JOB_POLL_INTERVAL: "1s"
      CONTROL_JOB_RECONCILE_INTERVAL: "1s"
      CONTROL_JOB_DATABASE_BACKOFF: "1s"
      CONTROL_JOB_SHUTDOWN_GRACE: "3s"
YAML
COMPOSE+=( -f "$OVERRIDE_FILE" )
compose() { CONTROL_E2E_RUNTIME_DIR="$RUNTIME_DIR" CONTROL_E2E_IMAGE="$IMAGE" CONTROL_E2E_CONTAINER="$CONTROL_CONTAINER" CONTROL_E2E_PORT="$HTTP_PORT" CONTROL_E2E_DB_PORT="$DB_PORT" CONTROL_E2E_TLS_PORT="$TLS_PORT" CONTROL_E2E_NODE_PORT="$NODE_PORT" CONTROL_E2E_NODE_IMAGE="${CONTROL_E2E_NODE_IMAGE:-relay-station-node:phase7-gate4}" CONTROL_E2E_NODE_MANAGEMENT_PASSWORD="$NODE_MANAGEMENT_PASSWORD" "${COMPOSE[@]}" "$@"; }
wait_http() { local url="$1"; for _ in $(seq 1 60); do curl --noproxy '*' -ksSf --max-time 2 "$url" >/dev/null && return 0; sleep 1; done; return 1; }
wait_node_ready() {
  local node_container="${PROJECT}-node-1" state health
  for _ in $(seq 1 90); do
    state="$(docker inspect "$node_container" --format '{{.State.Status}}' 2>/dev/null || true)"
    health="$(docker inspect "$node_container" --format '{{if .State.Health}}{{.State.Health.Status}}{{end}}' 2>/dev/null || true)"
    [[ "$health" == "healthy" ]] && return 0
    if [[ "$state" == "exited" || "$state" == "dead" ]]; then
      compose logs --no-color node >&2 || true
      return 1
    fi
    sleep 1
  done
  compose logs --no-color node >&2 || true
  return 1
}
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
[[ "$MODE" == "all" || "$MODE" == "auth" || "$MODE" == "startup" || "$MODE" == "upload" ]] || { echo "usage: ACCEPTANCE_RUNTIME_DIR=/external/path $0 [all|auth|startup|upload]" >&2; exit 2; }
ensure_candidate_image
compose up -d --wait postgres
DATABASE_URL="postgres://relay_control_migrator:relay_control_migrator_dev_only@127.0.0.1:${DB_PORT}/relay_station_control?sslmode=disable"
DATABASE_URL="$DATABASE_URL" make --silent migrate-up
compose exec -T postgres psql --set ON_ERROR_STOP=1 --username relay_control_migrator --dbname relay_station_control <<SQL
INSERT INTO environments(environment_id,name,environment_type) VALUES ('development','Acceptance harness','production') ON CONFLICT (singleton_id) DO NOTHING;
INSERT INTO node_drivers(node_type,driver_contract_version,display_name) VALUES ('cliproxyapi','cliproxyapi.auth-files.v1','Acceptance Node');
INSERT INTO driver_capabilities(node_type,driver_contract_version,capability) VALUES
  ('cliproxyapi','cliproxyapi.auth-files.v1','management_health_read'),
  ('cliproxyapi','cliproxyapi.auth-files.v1','management_account_inventory_read');
INSERT INTO provider_inventory_policy_versions(policy_version_id,node_type,driver_contract_version,active_providers,out_of_scope_providers,created_by)
VALUES ('00000000-0000-4000-8000-000000000047','cliproxyapi','cliproxyapi.auth-files.v1',ARRAY['antigravity']::text[],ARRAY[]::text[],'acceptance-harness');
INSERT INTO provider_inventory_policy_bindings(node_type,driver_contract_version,policy_version_id,bound_by,bound_at)
VALUES ('cliproxyapi','cliproxyapi.auth-files.v1','00000000-0000-4000-8000-000000000047','acceptance-harness',statement_timestamp());
INSERT INTO provider_inventory_policy_activations(node_type,driver_contract_version,policy_version_id,effective_from,activated_by,created_at)
VALUES ('cliproxyapi','cliproxyapi.auth-files.v1','00000000-0000-4000-8000-000000000047',statement_timestamp(),'acceptance-harness',statement_timestamp());
INSERT INTO relay_node_assets(instance_id,display_name,node_type,driver_contract_version,management_endpoint,reader_secret_ref)
VALUES ('00000000-0000-4000-8000-000000000047','Acceptance Node','cliproxyapi','cliproxyapi.auth-files.v1','http://node:8317','file://phase7/node-management');
INSERT INTO node_capabilities(instance_id,node_type,driver_contract_version,capability) VALUES
  ('00000000-0000-4000-8000-000000000047','cliproxyapi','cliproxyapi.auth-files.v1','management_health_read'),
  ('00000000-0000-4000-8000-000000000047','cliproxyapi','cliproxyapi.auth-files.v1','management_account_inventory_read');
INSERT INTO relay_node_inventory_monitoring_activations(instance_id,effective_from,reason,actor,created_at)
VALUES ('00000000-0000-4000-8000-000000000047',statement_timestamp(),'deployment_enable','acceptance-harness',statement_timestamp());
SQL
compose up -d secret-init node
wait_node_ready
secret_status="$(docker inspect "${PROJECT}-secret-init-1" --format '{{.State.Status}}' 2>/dev/null || true)"
secret_exit="$(docker inspect "${PROJECT}-secret-init-1" --format '{{.State.ExitCode}}' 2>/dev/null || true)"
[[ "$secret_status" == "exited" && "$secret_exit" == "0" ]] || { echo "secret_init_failed" >&2; exit 1; }
CONTROL_E2E_NODE_IMAGE="${CONTROL_E2E_NODE_IMAGE:-relay-station-node:phase7-gate4}" \
  CONTROL_E2E_NODE_PORT="$NODE_PORT" CONTROL_E2E_NODE_MANAGEMENT_PASSWORD="$NODE_MANAGEMENT_PASSWORD" \
  "$ROOT/gate-b-smoke.sh"
compose up -d control
wait_http "http://127.0.0.1:${HTTP_PORT}/api/healthz"
CONTROL_DATABASE_TEST_URL="$DATABASE_URL" \
  CONTROL_RUNTIME_DATABASE_TEST_URL="postgres://relay_control_app_dev:relay_control_runtime_dev_only@127.0.0.1:${DB_PORT}/relay_station_control?sslmode=disable" \
  env -u DINGTALK_WEBHOOK_URL -u DINGTALK_SIGNING_SECRET \
  go test ./cmd/control -run '^TestProductionAccountNodeResolverUsesMigratedCapabilitySchema$' -count=1 -v
echo "NODE_RESOLVER_SMOKE=PASS"
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
if [[ "$MODE" == "upload" ]]; then
  export ACCEPTANCE_UPLOAD_CREDENTIAL_FILE="$RUNTIME_DIR/upload-credential.json"
  export ACCEPTANCE_UPLOAD_EMAIL="$UPLOAD_EMAIL"
  export ACCEPTANCE_UPLOAD_SECRET_MARKER="$UPLOAD_SECRET_MARKER"
  export ACCEPTANCE_UPLOAD_EVIDENCE_FILE="$RUNTIME_DIR/upload-evidence.json"
  export CONTROL_E2E_BASE_URL="$ACCEPTANCE_BASE_URL"
  if ! CONTROL_E2E_BROWSER_CHANNEL=chromium npm --prefix "$CONTROL_DIR/web" run test:e2e -- account-operations-upload.spec.ts; then
    compose logs --no-color control node >&2 || true
    exit 1
  fi
  upload_command_id="$(sed -n 's/.*"command_id":"\([0-9a-f-]*\)".*/\1/p' "$RUNTIME_DIR/upload-evidence.json")"
  [[ "$upload_command_id" =~ ^[0-9a-f-]{36}$ ]] || { echo "upload_evidence_missing_command_id" >&2; exit 1; }
  assert_upload_count() {
    local label="$1" query="$2" got
    got="$(compose exec -T postgres psql --username relay_control_migrator --dbname relay_station_control --tuples-only --no-align --command "$query" | tr -d '\r\n[:space:]')"
    [[ "$got" == 1 ]] || { echo "${label}_mismatch:${got:-empty}" >&2; exit 1; }
  }
  assert_upload_count registry "SELECT count(*) FROM admin_command_registry WHERE command_id='$upload_command_id' AND command_kind='account.upload_new'"
  assert_upload_count operation "SELECT count(*) FROM account_admin_operations WHERE command_id='$upload_command_id' AND operation_kind='upload_new' AND account_key='antigravity:$UPLOAD_EMAIL' AND execution_state='remote_applied'"
  assert_upload_count receipt "SELECT count(*) FROM account_admin_command_receipts WHERE command_id='$upload_command_id'"
  assert_upload_count audit "SELECT count(*) FROM audit_logs WHERE category='account_admin' AND details->>'command_id'='$upload_command_id'"
  node_observation="$RUNTIME_DIR/node-observation.json"
  curl --noproxy '*' --silent --show-error --fail --header "Authorization: Bearer $NODE_MANAGEMENT_PASSWORD" "http://127.0.0.1:${NODE_PORT}/v0/management/auth-files" -o "$node_observation"
  grep -Fq "$UPLOAD_EMAIL" "$node_observation" || { echo "node_upload_observation_missing" >&2; exit 1; }
  rm -f -- "$node_observation"
  echo "UPLOAD_NEW_BROWSER=PASS"
  echo "UPLOAD_NEW_CONTROL=PASS"
  echo "UPLOAD_NEW_POSTGRES=PASS"
  echo "UPLOAD_NEW_NODE=PASS"
  echo "UPLOAD_NEW_NATIVE_MUTATIONS=1"
  exit 0
fi
if [[ "${1:-all}" == "all" ]]; then
  export CONTROL_DATABASE_TEST_URL="$DATABASE_URL"
  export CONTROL_RUNTIME_DATABASE_TEST_URL="postgres://relay_control_app_dev:relay_control_runtime_dev_only@127.0.0.1:${DB_PORT}/relay_station_control?sslmode=disable"
  env -u DINGTALK_WEBHOOK_URL -u DINGTALK_SIGNING_SECRET go test ./internal/store -run '^TestRuntimeAcceptanceLifecycleFixture$' -count=1 -v
  env -u DINGTALK_WEBHOOK_URL -u DINGTALK_SIGNING_SECRET go test ./internal/store -run '^TestRuntimeAcceptanceLifecycleFixture$' -count=1 -v
  echo "LIFECYCLE_REPEATED=PASS"
fi
