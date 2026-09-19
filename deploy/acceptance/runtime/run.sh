#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
CONTROL_DIR="$(cd -- "$ROOT/../../.." && pwd)"
COMPOSE_FILE="$CONTROL_DIR/deploy/acceptance/compose.yaml"
GOLANG_IMAGE='golang:1.27.0-alpine@sha256:4c9fe60190a2a3350ddc51de80d0224b8a6698d12bdfc999fee45ea9d6c46dbc'
RUNTIME_DIR="${ACCEPTANCE_RUNTIME_DIR:-}"
ACCEPTANCE_FAILURE_LAYER=source
OVERRIDE_FILE=""
PROJECT="${ACCEPTANCE_COMPOSE_PROJECT:-relay-control-harness-$$}"
MODE="${1:-all}"
[[ "$MODE" == "all" || "$MODE" == "internal" || "$MODE" == "auth" || "$MODE" == "startup" || "$MODE" == "upload" || "$MODE" == "disable" || "$MODE" == "enable-fixture" || "$MODE" == "enable" || "$MODE" == "replace" || "$MODE" == "replace-discovery" || "$MODE" == "remove" || "$MODE" == "override" || "$MODE" == "security-replay" ]] || { echo "usage: ACCEPTANCE_RUNTIME_DIR=/external/path $0 [all|internal|auth|startup|upload|disable|enable-fixture|enable|replace|replace-discovery|remove|override|security-replay]" >&2; exit 2; }
HTTP_PORT="${ACCEPTANCE_HTTP_PORT:-$((19080 + $$ % 500))}"
DB_PORT="${ACCEPTANCE_DB_PORT:-$((19543 + $$ % 500))}"
TLS_PORT="${ACCEPTANCE_TLS_PORT:-$((19443 + $$ % 500))}"
NODE_PORT="${ACCEPTANCE_NODE_PORT:-$((19317 + $$ % 500))}"
EXPECTED_SHA="${EXPECTED_SHA:-$(git -C "$CONTROL_DIR" rev-parse HEAD)}"
[[ -z "$(git -C "$CONTROL_DIR" status --porcelain)" ]] || { echo "ACCEPTANCE_FAILURE_LAYER=source" >&2; echo "DIRTY_SOURCE: candidate acceptance requires a clean source tree" >&2; exit 1; }
[[ "$EXPECTED_SHA" =~ ^[0-9a-f]{40}$ ]] || { echo "ACCEPTANCE_FAILURE_LAYER=artifact" >&2; echo "REVISION_MISMATCH: EXPECTED_SHA must be a 40-character candidate SHA" >&2; exit 2; }
[[ "$(git -C "$CONTROL_DIR" rev-parse HEAD)" == "$EXPECTED_SHA" ]] || { echo "ACCEPTANCE_FAILURE_LAYER=artifact" >&2; echo "REVISION_MISMATCH: candidate SHA does not match HEAD" >&2; exit 1; }
SHORT_SHA="${EXPECTED_SHA:0:12}"
IMAGE="${ACCEPTANCE_IMAGE:-relay-station/control:acceptance-${SHORT_SHA}}"
RESOLVED_CONTROL_IMAGE_ID=""
EXPECTED_CONTROL_IMAGE_ID_INPUT="${EXPECTED_CONTROL_IMAGE_ID:-}"
[[ -z "$EXPECTED_CONTROL_IMAGE_ID_INPUT" || "$EXPECTED_CONTROL_IMAGE_ID_INPUT" =~ ^sha256:[0-9a-f]{64}$ ]] || { echo "ACCEPTANCE_FAILURE_LAYER=artifact" >&2; echo "CONTROL_IMAGE_ID_MISMATCH: expected local image ID is invalid" >&2; exit 1; }
[[ -n "${ACCEPTANCE_IMAGE:-}" && -n "$EXPECTED_CONTROL_IMAGE_ID_INPUT" || -z "${ACCEPTANCE_IMAGE:-}" ]] || { echo "ACCEPTANCE_FAILURE_LAYER=artifact" >&2; echo "CONTROL_IMAGE_ID_MISSING: prebuilt ACCEPTANCE_IMAGE requires EXPECTED_CONTROL_IMAGE_ID" >&2; exit 1; }
CONTROL_E2E_NODE_IMAGE="${CONTROL_E2E_NODE_IMAGE:-}"
CONTROL_E2E_NODE_DIGEST="${CONTROL_E2E_NODE_DIGEST:-}"
CONTROL_E2E_NODE_VERSION="${CONTROL_E2E_NODE_VERSION:-}"
CONTROL_E2E_NODE_COMMIT="${CONTROL_E2E_NODE_COMMIT:-}"
[[ -n "$CONTROL_E2E_NODE_IMAGE" ]] || { echo "ACCEPTANCE_FAILURE_LAYER=artifact" >&2; echo "NODE_ARTIFACT_MISSING: set CONTROL_E2E_NODE_IMAGE" >&2; exit 1; }
[[ "$CONTROL_E2E_NODE_DIGEST" =~ ^sha256:[0-9a-f]{64}$ ]] || { echo "ACCEPTANCE_FAILURE_LAYER=artifact" >&2; echo "NODE_ARTIFACT_MISSING: set immutable CONTROL_E2E_NODE_DIGEST" >&2; exit 1; }
[[ -n "$CONTROL_E2E_NODE_VERSION" ]] || { echo "ACCEPTANCE_FAILURE_LAYER=artifact" >&2; echo "NODE_ARTIFACT_MISSING: set CONTROL_E2E_NODE_VERSION" >&2; exit 1; }
[[ "$CONTROL_E2E_NODE_COMMIT" =~ ^[0-9a-f]{40}$ ]] || { echo "ACCEPTANCE_FAILURE_LAYER=artifact" >&2; echo "NODE_ARTIFACT_MISSING: set full CONTROL_E2E_NODE_COMMIT" >&2; exit 1; }
CONTROL_CONTAINER="${PROJECT}-control-1"
NODE_MANAGEMENT_PASSWORD="$(openssl rand -hex 32)"
NODE_INSTANCE_ID="00000000-0000-4000-8000-000000000047"
COMPOSE=(docker compose -p "$PROJECT" -f "$COMPOSE_FILE")
[[ -n "$RUNTIME_DIR" && "$RUNTIME_DIR" == /* && "$RUNTIME_DIR" != "$CONTROL_DIR"/* ]] || { echo "ACCEPTANCE_FAILURE_LAYER=source" >&2; echo "ACCEPTANCE_RUNTIME_DIR must be repo-external" >&2; exit 2; }
[[ -z "${DINGTALK_WEBHOOK_URL:-}" && -z "${DINGTALK_SIGNING_SECRET:-}" ]] || { echo "ACCEPTANCE_FAILURE_LAYER=source" >&2; echo "real DingTalk configuration must be absent" >&2; exit 2; }
mkdir -p "$RUNTIME_DIR"; chmod 700 "$RUNTIME_DIR"
cleanup() {
  local exit_code=$?
  if [[ "$exit_code" != 0 ]]; then
    echo "ACCEPTANCE_FAILURE_LAYER=${ACCEPTANCE_FAILURE_LAYER}" >&2
    echo "ACCEPTANCE_FAILURE_DIAGNOSTICS_BEGIN" >&2
    if ! compose ps >&2; then
      echo "ACCEPTANCE_CLEANUP_FAILURE=diagnostic_compose_ps" >&2
    fi
    docker inspect "$CONTROL_CONTAINER" --format 'CONTROL_STATE={{.State.Status}} EXIT_CODE={{.State.ExitCode}} ERROR={{.State.Error}} IMAGE={{.Image}}' >&2 2>/dev/null || true
    compose port control 8080 >&2 || true
    if ! compose logs --no-color --tail=120 control >&2; then
      echo "ACCEPTANCE_CLEANUP_FAILURE=diagnostic_control_logs" >&2
    fi
    echo "ACCEPTANCE_FAILURE_DIAGNOSTICS_END" >&2
  fi
  if ! compose down --volumes --remove-orphans >/dev/null 2>&1; then
    echo "ACCEPTANCE_CLEANUP_FAILURE=compose_down" >&2
  fi
  if ! rm -rf -- "$RUNTIME_DIR"; then
    echo "ACCEPTANCE_CLEANUP_FAILURE=runtime_dir_removal" >&2
  fi
  exit "$exit_code"
}
trap cleanup EXIT
umask 077
port_available() {
  local label="$1" port="$2"
  if lsof -nP -iTCP:"$port" -sTCP:LISTEN 2>/dev/null | tail -n +2 | grep -q .; then
    echo "${label}_PORT_UNAVAILABLE=${port}" >&2
    lsof -nP -iTCP:"$port" -sTCP:LISTEN >&2 || true
    return 1
  fi
  echo "${label}_PORT_AVAILABLE=${port}"
}
port_available HTTP "$HTTP_PORT"
port_available DB "$DB_PORT"
port_available TLS "$TLS_PORT"
port_available NODE "$NODE_PORT"
openssl rand -hex 32 > "$RUNTIME_DIR/bootstrap-secret"
openssl rand -out "$RUNTIME_DIR/account-operation-intent-key" 32
printf '%s' "$NODE_MANAGEMENT_PASSWORD" > "$RUNTIME_DIR/node-management-key"
(cd "$CONTROL_DIR" && go run ./cmd/relay-control-asset-credential-key --path "$RUNTIME_DIR/asset-credential-key" >/dev/null)
UPLOAD_EMAIL="phase7-${PROJECT##*-}@example.invalid"
UPLOAD_SECRET_MARKER="PHASE7_E2E_SECRET_${PROJECT##*-}"
mkdir -p "$RUNTIME_DIR/node/auths" "$RUNTIME_DIR/node/logs"
DISABLE_EMAIL="phase7-disable-${PROJECT}@example.invalid"
ENABLE_EMAIL="phase7-enable-${PROJECT}@example.invalid"
ENABLE_SECRET_MARKER="PHASE7_ENABLE_SECRET_${PROJECT##*-}"
REPLACE_EMAIL="phase7-replace-${PROJECT}@example.invalid"
REPLACE_BASE_SECRET_MARKER="PHASE7_REPLACE_BASE_${PROJECT##*-}"
REPLACE_SECRET_MARKER="PHASE7_REPLACE_SECRET_${PROJECT##*-}"
REMOVE_EMAIL="phase7-remove-${PROJECT}@example.invalid"
OVERRIDE_LIFECYCLE_EMAIL="phase7-override-lifecycle-${PROJECT}@example.invalid"
setup_upload_fixture() {
  printf '{"type":"antigravity","email":"%s","phase7_marker":"%s"}\n' "$UPLOAD_EMAIL" "$UPLOAD_SECRET_MARKER" > "$RUNTIME_DIR/upload-credential.json"
}
setup_upload_seed_fixture() {
  printf '{"type":"antigravity","email":"phase7-seed-%s@example.invalid","access_token":"phase7-disposable-seed-token"}\n' "$PROJECT" > "$RUNTIME_DIR/node/auths/phase7-seed.json"
}
setup_disable_fixture() {
  printf '{"type":"antigravity","email":"%s","access_token":"phase7-disposable-disable-token"}\n' "$DISABLE_EMAIL" > "$RUNTIME_DIR/node/auths/phase7-disable.json"
}
setup_enable_fixture() {
  printf '{"type":"antigravity","email":"%s","access_token":"phase7-disposable-enable-token","phase7_marker":"%s","disabled":true}\n' "$ENABLE_EMAIL" "$ENABLE_SECRET_MARKER" > "$RUNTIME_DIR/node/auths/phase7-enable.json"
}
setup_replace_fixture() {
  printf '{"type":"antigravity","email":"%s","access_token":"phase7-disposable-replace-base-token","phase7_marker":"%s","disabled":false}\n' "$REPLACE_EMAIL" "$REPLACE_BASE_SECRET_MARKER" > "$RUNTIME_DIR/node/auths/phase7-replace-base.json"
  printf '{"type":"antigravity","email":"%s","refresh_token":"phase7-disposable-replace-refresh-token","phase7_marker":"%s","status":"active","unavailable":false}\n' "$REPLACE_EMAIL" "$REPLACE_SECRET_MARKER" > "$RUNTIME_DIR/replace-credential.json"
}
setup_remove_fixture() {
  printf '{"type":"antigravity","email":"%s","access_token":"phase7-disposable-remove-token","disabled":false}\n' "$REMOVE_EMAIL" > "$RUNTIME_DIR/node/auths/phase7-remove.json"
}
setup_override_fixtures() {
  printf '{"type":"antigravity","email":"%s","access_token":"phase7-disposable-override-lifecycle-token","disabled":false}\n' "$OVERRIDE_LIFECYCLE_EMAIL" > "$RUNTIME_DIR/node/auths/phase7-override-lifecycle.json"
}
case "$MODE" in
  upload)
    setup_upload_fixture
    setup_upload_seed_fixture
    ;;
  security-replay)
    setup_disable_fixture
    ;;
  disable) setup_disable_fixture ;;
  enable-fixture|enable) setup_enable_fixture ;;
  replace|replace-discovery) setup_replace_fixture ;;
  remove) setup_remove_fixture ;;
  override) setup_override_fixtures ;;
  auth|startup|internal|all) ;;
esac
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
cat > "$RUNTIME_DIR/node-counter.conf" <<'NGINX'
pid /tmp/nginx.pid;
events {}
http {
  log_format native_counter '$request_method $uri $status';
  access_log /dev/stdout native_counter;
  error_log /dev/stderr warn;
  client_body_temp_path /tmp/client_temp;
  proxy_temp_path /tmp/proxy_temp;
  fastcgi_temp_path /tmp/fastcgi_temp;
  uwsgi_temp_path /tmp/uwsgi_temp;
  scgi_temp_path /tmp/scgi_temp;
  server {
    listen 8318;
    location / {
      proxy_pass http://node:8317;
      proxy_http_version 1.1;
      proxy_set_header Host $host;
      proxy_set_header Authorization $http_authorization;
    }
  }
}
NGINX
key="$(openssl rand -base64 32 | tr -d '\r\n=')"
printf '{"format_version":1,"environment":"production","current":1,"keys":[{"version":1,"key":"%s"}]}\n' "$key" > "$RUNTIME_DIR/auth-keyring.json"
unset key
openssl rand -base64 36 | tr -d '\r\n' > "$RUNTIME_DIR/admin-password"
openssl rand -base64 36 | tr -d '\r\n' > "$RUNTIME_DIR/second-admin-password"
openssl req -x509 -newkey rsa:2048 -sha256 -nodes -days 1 -subj '/CN=localhost' -addext 'subjectAltName=DNS:localhost' -addext 'basicConstraints=critical,CA:FALSE' -addext 'keyUsage=critical,digitalSignature,keyEncipherment' -addext 'extendedKeyUsage=serverAuth' -keyout "$RUNTIME_DIR/tls.key" -out "$RUNTIME_DIR/tls.crt" >/dev/null 2>&1
chmod 400 "$RUNTIME_DIR/bootstrap-secret" "$RUNTIME_DIR/account-operation-intent-key" "$RUNTIME_DIR/node-management-key" "$RUNTIME_DIR/asset-credential-key" "$RUNTIME_DIR/auth-keyring.json" "$RUNTIME_DIR/admin-password" "$RUNTIME_DIR/second-admin-password" "$RUNTIME_DIR/tls.key"
chmod 444 "$RUNTIME_DIR/tls.crt"
OVERRIDE_FILE="$RUNTIME_DIR/compose.override.yaml"
INVENTORY_POLL_ENABLED="false"
INVENTORY_LIFECYCLE_ENABLED="false"
INVENTORY_POLL_START_GRACE="299s"
[[ "$MODE" == "disable" || "$MODE" == "enable-fixture" || "$MODE" == "enable" || "$MODE" == "replace" || "$MODE" == "replace-discovery" || "$MODE" == "remove" || "$MODE" == "override" ]] && INVENTORY_POLL_ENABLED="true"
[[ "$MODE" == "disable" || "$MODE" == "enable-fixture" || "$MODE" == "enable" || "$MODE" == "replace" || "$MODE" == "replace-discovery" || "$MODE" == "remove" || "$MODE" == "override" ]] && INVENTORY_LIFECYCLE_ENABLED="true"
[[ "$MODE" == "security-replay" ]] && { INVENTORY_POLL_ENABLED="true"; INVENTORY_LIFECYCLE_ENABLED="true"; INVENTORY_POLL_START_GRACE="299s"; }
if [[ "$MODE" == "disable" || "$MODE" == "security-replay" || "$MODE" == "enable-fixture" || "$MODE" == "enable" || "$MODE" == "replace" || "$MODE" == "remove" || "$MODE" == "override" ]]; then
  # The one-shot acceptance bootstrap owns this poll. Keep the production
  # Control scheduler disabled so two workers cannot claim the same run.
  INVENTORY_POLL_ENABLED="false"
  INVENTORY_LIFECYCLE_ENABLED="true"
fi
export INVENTORY_POLL_ENABLED INVENTORY_LIFECYCLE_ENABLED INVENTORY_POLL_START_GRACE
cat > "$OVERRIDE_FILE" <<'YAML'
services:
  control:
    environment:
      CONTROL_GATEWAY_DIRECTORY_ENABLED: "false"
      CONTROL_ACCOUNT_INVENTORY_POLL_ENABLED: "${INVENTORY_POLL_ENABLED}"
      CONTROL_ACCOUNT_INVENTORY_LIFECYCLE_ENABLED: "${INVENTORY_LIFECYCLE_ENABLED}"
      CONTROL_ACCOUNT_REQUEST_QUALITY_ENABLED: "false"
      CONTROL_CLIPROXYAPI_DRIVER_ENABLED: "true"
      CONTROL_ACCOUNT_INVENTORY_HISTORY_ENABLED: "false"
      CONTROL_JOB_WORKER_CONCURRENCY: "1"
      CONTROL_JOB_RECONCILER_CONCURRENCY: "1"
      CONTROL_JOB_POLL_INTERVAL: "1s"
      CONTROL_JOB_RECONCILE_INTERVAL: "1s"
      CONTROL_JOB_DATABASE_BACKOFF: "1s"
      CONTROL_JOB_SHUTDOWN_GRACE: "3s"
      CONTROL_ACCOUNT_INVENTORY_POLL_START_GRACE: "${INVENTORY_POLL_START_GRACE}"
YAML
COMPOSE+=( -f "$OVERRIDE_FILE" )
compose() { CONTROL_E2E_RUNTIME_DIR="$RUNTIME_DIR" CONTROL_E2E_IMAGE_REF="$IMAGE" CONTROL_E2E_RESOLVED_IMAGE_ID="$RESOLVED_CONTROL_IMAGE_ID" CONTROL_E2E_CONTAINER="$CONTROL_CONTAINER" CONTROL_E2E_PORT="$HTTP_PORT" CONTROL_E2E_DB_PORT="$DB_PORT" CONTROL_E2E_TLS_PORT="$TLS_PORT" CONTROL_E2E_NODE_PORT="$NODE_PORT" CONTROL_E2E_NODE_IMAGE="$CONTROL_E2E_NODE_IMAGE" CONTROL_E2E_NODE_DIGEST="$CONTROL_E2E_NODE_DIGEST" CONTROL_E2E_NODE_VERSION="$CONTROL_E2E_NODE_VERSION" CONTROL_E2E_NODE_COMMIT="$CONTROL_E2E_NODE_COMMIT" CONTROL_E2E_NODE_MANAGEMENT_PASSWORD="$NODE_MANAGEMENT_PASSWORD" "${COMPOSE[@]}" "$@"; }
source "$ROOT/lib.sh"
image_revision() { docker image inspect "$1" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' 2>/dev/null || true; }
ensure_candidate_image() {
  local revision
  revision="$(image_revision "$IMAGE")"
  if [[ "$revision" != "$EXPECTED_SHA" ]]; then
    [[ -n "${ACCEPTANCE_IMAGE:-}" ]] && { echo "candidate_image_mismatch" >&2; exit 1; }
    local buildx_state
    buildx_state="$(mktemp -d /private/tmp/relay-control-buildx.XXXXXX)"
    BUILDX_CONFIG_DIR="$buildx_state" EXPECTED_SHA="$EXPECTED_SHA" IMAGE="$IMAGE" "$ROOT/build-image.sh"
    rm -rf -- "$buildx_state"
    revision="$(image_revision "$IMAGE")"
  fi
  [[ "$revision" == "$EXPECTED_SHA" ]] || { echo "REVISION_MISMATCH: candidate image revision does not match expected SHA" >&2; exit 1; }
  RESOLVED_CONTROL_IMAGE_ID="$(docker image inspect "$IMAGE" --format '{{.Id}}' 2>/dev/null || true)"
  [[ "$RESOLVED_CONTROL_IMAGE_ID" =~ ^sha256:[0-9a-f]{64}$ ]] || { echo "CONTROL_IMAGE_RESOLUTION_FAILURE: candidate image has no valid local image ID" >&2; exit 1; }
  [[ -z "$EXPECTED_CONTROL_IMAGE_ID_INPUT" || "$RESOLVED_CONTROL_IMAGE_ID" == "$EXPECTED_CONTROL_IMAGE_ID_INPUT" ]] || { echo "CONTROL_IMAGE_ID_MISMATCH: resolved local image ID does not match expected prebuilt identity" >&2; exit 1; }
  echo "CANDIDATE_IMAGE_ID=$RESOLVED_CONTROL_IMAGE_ID"
  echo "CANDIDATE_IMAGE_REVISION=PASS"
}
ACCEPTANCE_FAILURE_LAYER=artifact
ensure_candidate_image
EXPECTED_CONTROL_IMAGE_ID="$RESOLVED_CONTROL_IMAGE_ID"
echo "EXPECTED_CONTROL_IMAGE_ID=$EXPECTED_CONTROL_IMAGE_ID"
ACCEPTANCE_FAILURE_LAYER=postgres
compose up -d --wait postgres
DATABASE_URL="postgres://relay_control_migrator:relay_control_migrator_dev_only@127.0.0.1:${DB_PORT}/relay_station_control?sslmode=disable"
DATABASE_URL="$DATABASE_URL" make --silent migrate-up
compose exec -T postgres psql --set ON_ERROR_STOP=1 --username relay_control_migrator --dbname relay_station_control <<SQL
INSERT INTO environments(environment_id,name,environment_type) VALUES ('development','Acceptance harness','production') ON CONFLICT (singleton_id) DO NOTHING;
INSERT INTO node_drivers(node_type,driver_contract_version,display_name) VALUES ('cliproxyapi','cliproxyapi.auth-files.v1','Acceptance Node');
INSERT INTO driver_capabilities(node_type,driver_contract_version,capability) VALUES
  ('cliproxyapi','cliproxyapi.auth-files.v1','management_health_read'),
  ('cliproxyapi','cliproxyapi.auth-files.v1','management_account_inventory_read');
INSERT INTO provider_inventory_policy_versions(policy_version_id,node_type,driver_contract_version,active_providers,out_of_scope_providers,created_by,created_at)
VALUES ('00000000-0000-4000-8000-000000000047','cliproxyapi','cliproxyapi.auth-files.v1',ARRAY['antigravity']::text[],ARRAY[]::text[],'acceptance-harness',
        to_timestamp(floor(extract(epoch FROM statement_timestamp()) / 300) * 300));
INSERT INTO provider_inventory_policy_bindings(node_type,driver_contract_version,policy_version_id,bound_by,bound_at)
VALUES ('cliproxyapi','cliproxyapi.auth-files.v1','00000000-0000-4000-8000-000000000047','acceptance-harness',statement_timestamp());
INSERT INTO provider_inventory_policy_activations(node_type,driver_contract_version,policy_version_id,effective_from,activated_by,created_at)
VALUES ('cliproxyapi','cliproxyapi.auth-files.v1','00000000-0000-4000-8000-000000000047',
        to_timestamp(floor(extract(epoch FROM statement_timestamp()) / 300) * 300),
        'acceptance-harness',
        to_timestamp(floor(extract(epoch FROM statement_timestamp()) / 300) * 300));
SQL
if [[ "$MODE" == "all" || "$MODE" == "internal" ]]; then
  ACCEPTANCE_FAILURE_LAYER=internal
  echo "ACCEPTANCE_MODE=INTERNAL_LIFECYCLE"
  [[ "$MODE" == "all" ]] && echo "ALL_COMPATIBILITY_ALIAS=internal"
  echo "ACCEPTANCE_AUTH_BOOTSTRAP=SKIPPED reason=mode_internal"
  export CONTROL_DATABASE_TEST_URL="$DATABASE_URL"
  export CONTROL_RUNTIME_DATABASE_TEST_URL="postgres://relay_control_app_dev:relay_control_runtime_dev_only@127.0.0.1:${DB_PORT}/relay_station_control?sslmode=disable"
  env -u DINGTALK_WEBHOOK_URL -u DINGTALK_SIGNING_SECRET go test ./internal/store -run '^TestRuntimeAcceptanceLifecycleFixture$' -count=1 -v
  env -u DINGTALK_WEBHOOK_URL -u DINGTALK_SIGNING_SECRET go test ./internal/store -run '^TestRuntimeAcceptanceLifecycleFixture$' -count=1 -v
  echo "LIFECYCLE_REPEATED=PASS"
  exit 0
fi
ACCEPTANCE_FAILURE_LAYER=node
compose up -d secret-init node
wait_for_node_ready NODE_READINESS "${PROJECT}-node-1" 90
secret_status="$(docker inspect "${PROJECT}-secret-init-1" --format '{{.State.Status}}' 2>/dev/null || true)"
secret_exit="$(docker inspect "${PROJECT}-secret-init-1" --format '{{.State.ExitCode}}' 2>/dev/null || true)"
[[ "$secret_status" == "exited" && "$secret_exit" == "0" ]] || { echo "ACCEPTANCE_FAILURE_LAYER=node" >&2; echo "secret_init_failed" >&2; exit 1; }
CONTROL_E2E_NODE_IMAGE="$CONTROL_E2E_NODE_IMAGE" CONTROL_E2E_NODE_DIGEST="$CONTROL_E2E_NODE_DIGEST" \
  CONTROL_E2E_NODE_VERSION="$CONTROL_E2E_NODE_VERSION" CONTROL_E2E_NODE_COMMIT="$CONTROL_E2E_NODE_COMMIT" \
  CONTROL_E2E_NODE_PORT="$NODE_PORT" CONTROL_E2E_NODE_MANAGEMENT_PASSWORD="$NODE_MANAGEMENT_PASSWORD" \
  "$ROOT/gate-b-smoke.sh"
ACCEPTANCE_FAILURE_LAYER=control
compose up -d control
RUNNING_CONTROL_IMAGE_ID="$(docker inspect "$CONTROL_CONTAINER" --format '{{.Image}}')"
[[ "$RUNNING_CONTROL_IMAGE_ID" == "$EXPECTED_CONTROL_IMAGE_ID" ]] || { echo "ACCEPTANCE_FAILURE_LAYER=artifact" >&2; echo "control_image_provenance_mismatch" >&2; exit 1; }
echo "RUNNING_CONTROL_IMAGE_ID=$RUNNING_CONTROL_IMAGE_ID"
echo "CONTROL_ACTUAL_PORT=$(compose port control 8080)"
wait_for_http CONTROL_READINESS "http://127.0.0.1:${HTTP_PORT}/api/healthz" 60
CONTROL_DATABASE_TEST_URL="$DATABASE_URL" \
  CONTROL_RUNTIME_DATABASE_TEST_URL="postgres://relay_control_app_dev:relay_control_runtime_dev_only@127.0.0.1:${DB_PORT}/relay_station_control?sslmode=disable" \
  env -u DINGTALK_WEBHOOK_URL -u DINGTALK_SIGNING_SECRET \
  go test ./cmd/control -run '^TestProductionAccountNodeResolverUsesMigratedCapabilitySchema$' -count=1 -v
echo "NODE_RESOLVER_SMOKE=PASS"
if [[ "$MODE" == "startup" ]]; then
  sleep 3
  wait_for_http CONTROL_READINESS "http://127.0.0.1:${HTTP_PORT}/api/healthz" 60
  compose stop -t 15 control
  exit_code="$(docker inspect "$CONTROL_CONTAINER" --format '{{.State.ExitCode}}' 2>/dev/null || true)"
  [[ "$exit_code" == "0" ]] || { echo "ACCEPTANCE_FAILURE_LAYER=control" >&2; echo "control did not exit cleanly: ${exit_code:-unknown}" >&2; exit 1; }
  echo "CONTROL_STARTUP_GATE=PASS"
  exit 0
fi
ACCEPTANCE_FAILURE_LAYER=control
run_inventory_bootstrap() {
  local target_email="$1" network module_cache
  network="$(docker network ls --filter "label=com.docker.compose.project=${PROJECT}" --format '{{.Name}}' | head -n 1)"
  [[ -n "$network" ]] || { echo "INVENTORY_BOOTSTRAP_NETWORK_MISSING" >&2; return 1; }
  module_cache="$(go env GOMODCACHE)"
  docker run --rm \
    --network "$network" \
    --user 65532:65532 \
    --read-only \
    --tmpfs /tmp:rw,exec,nosuid,size=512m,uid=65532,gid=65532 \
    --cap-drop ALL \
    --security-opt no-new-privileges:true \
    --workdir /src \
    -v "$CONTROL_DIR:/src:ro" \
    -v "$module_cache:/go/pkg/mod:ro" \
    -v "$RUNTIME_DIR/asset-credential-key:/run/control-secrets/asset-credential-key:ro" \
    -e HTTP_PROXY= -e HTTPS_PROXY= -e ALL_PROXY= \
    -e http_proxy= -e https_proxy= -e all_proxy= \
    -e NO_PROXY='*' -e no_proxy='*' -e GOPROXY=off -e GOCACHE=/tmp/go-build \
    -e DATABASE_URL='postgres://relay_control_app_dev:relay_control_runtime_dev_only@postgres:5432/relay_station_control?sslmode=disable' \
    -e CONTROL_ASSET_CREDENTIAL_KEY_FILE=/run/control-secrets/asset-credential-key \
    -e ACCOUNT_INVENTORY_NODE_ID="$NODE_INSTANCE_ID" \
    -e ACCOUNT_INVENTORY_TARGET_EMAIL="$target_email" \
    "$GOLANG_IMAGE" go run ./deploy/acceptance/stage0-inventory-bootstrap
}
if [[ "$MODE" == "disable" || "$MODE" == "security-replay" ]]; then
  ACCEPTANCE_FAILURE_LAYER=inventory_bootstrap
fi
compose up -d tls
wait_for_http HTTP_READINESS "https://127.0.0.1:${TLS_PORT}/api/healthz" 60
export ACCEPTANCE_RUNTIME_DIR="$RUNTIME_DIR" ACCEPTANCE_BASE_URL="https://127.0.0.1:${TLS_PORT}" ACCEPTANCE_STORAGE_STATE="$RUNTIME_DIR/storage-state.json"
[[ "$MODE" == "security-replay" || "$MODE" == "enable" || "$MODE" == "enable-fixture" ]] || export ACCEPTANCE_CONTROL_CONTAINER="$CONTROL_CONTAINER"
ACCEPTANCE_FAILURE_LAYER=auth
CONTROL_E2E_BROWSER_CHANNEL="${CONTROL_E2E_BROWSER_CHANNEL:-chromium}" node "$ROOT/auth-session.mjs"
chmod 600 "$RUNTIME_DIR/storage-state.json"
echo "AUTH_COMPOSITION_SMOKE=PASS"
if [[ "$MODE" != "auth" ]]; then
  ACCEPTANCE_FAILURE_LAYER=fixture
  ACCEPTANCE_BASE_URL="$ACCEPTANCE_BASE_URL" \
    ACCEPTANCE_STORAGE_STATE="$RUNTIME_DIR/storage-state.json" \
    ACCEPTANCE_NODE_INSTANCE_ID="$NODE_INSTANCE_ID" \
    ACCEPTANCE_NODE_MANAGEMENT_PASSWORD="$NODE_MANAGEMENT_PASSWORD" \
    ACCEPTANCE_NODE_ENDPOINT="http://node-counter:8318" \
    CONTROL_E2E_BROWSER_CHANNEL="${CONTROL_E2E_BROWSER_CHANNEL:-chromium}" \
    node "$ROOT/register-node.mjs"
  [[ "$(psql_count "SELECT count(*) FROM relay_node_assets WHERE instance_id='$NODE_INSTANCE_ID' AND management_credential_sealed IS NOT NULL AND reader_secret_ref IS NULL")" == 1 ]] || { echo "stage0_node_credential_sealed_missing" >&2; exit 1; }
  [[ "$(psql_count "SELECT count(*) FROM relay_node_inventory_monitoring_activations WHERE instance_id='$NODE_INSTANCE_ID'")" == 1 ]] || { echo "node_monitoring_activation_missing" >&2; exit 1; }
  echo "STAGE0_NODE_SEALED_PRESENCE=PASS"
  if [[ "$MODE" == "disable" || "$MODE" == "security-replay" ]]; then
    ACCEPTANCE_FAILURE_LAYER=inventory_bootstrap
    run_inventory_bootstrap "$DISABLE_EMAIL"
  elif [[ "$MODE" == "enable-fixture" || "$MODE" == "enable" ]]; then
    ACCEPTANCE_FAILURE_LAYER=inventory_bootstrap
    run_inventory_bootstrap "$ENABLE_EMAIL"
  elif [[ "$MODE" == "replace" ]]; then
    ACCEPTANCE_FAILURE_LAYER=inventory_bootstrap
    run_inventory_bootstrap "$REPLACE_EMAIL"
  elif [[ "$MODE" == "remove" ]]; then
    ACCEPTANCE_FAILURE_LAYER=inventory_bootstrap
    run_inventory_bootstrap "$REMOVE_EMAIL"
  elif [[ "$MODE" == "override" ]]; then
    ACCEPTANCE_FAILURE_LAYER=inventory_bootstrap
    run_inventory_bootstrap "$OVERRIDE_LIFECYCLE_EMAIL"
  fi
fi
if [[ "$MODE" == "override" ]]; then
  ACCEPTANCE_FAILURE_LAYER=fixture
  OVERRIDE_LIFECYCLE_COMMAND_ID="$(python3 -c 'import uuid; print(uuid.uuid4())')"
  OVERRIDE_ADMIN_ID="$(compose exec -T postgres psql --username relay_control_migrator --dbname relay_station_control --tuples-only --no-align --command "SELECT admin_id FROM control_admin_users WHERE status='enabled' ORDER BY activated_at LIMIT 1" | tr -d '\r\n[:space:]')"
  compose exec -T postgres psql --set ON_ERROR_STOP=1 --username relay_control_migrator --dbname relay_station_control <<SQL
SELECT public.control_accept_account_admin_operation_v1('$OVERRIDE_LIFECYCLE_COMMAND_ID','$OVERRIDE_ADMIN_ID','account.disable',decode(repeat('aa',32),'hex'),NULL,'$NODE_INSTANCE_ID','antigravity:$OVERRIDE_LIFECYCLE_EMAIL','disable',NULL);
SELECT public.control_admit_account_dispatch_v1('$OVERRIDE_LIFECYCLE_COMMAND_ID','$NODE_INSTANCE_ID','antigravity:$OVERRIDE_LIFECYCLE_EMAIL','override-fixture');
SELECT public.control_transition_account_admin_operation_v1('$OVERRIDE_LIFECYCLE_COMMAND_ID','dispatched','outcome_unknown');
SQL
  export ACCEPTANCE_OVERRIDE_LIFECYCLE_EMAIL="$OVERRIDE_LIFECYCLE_EMAIL" ACCEPTANCE_OVERRIDE_LIFECYCLE_COMMAND_ID="$OVERRIDE_LIFECYCLE_COMMAND_ID" ACCEPTANCE_OVERRIDE_EVIDENCE_FILE="$RUNTIME_DIR/override-evidence.json" ACCEPTANCE_BROWSER_CONSOLE_FILE="$RUNTIME_DIR/browser-console.log" CONTROL_E2E_PLAYWRIGHT_OUTPUT_DIR="$RUNTIME_DIR/playwright-output" CONTROL_E2E_BASE_URL="$ACCEPTANCE_BASE_URL"
  mkdir -p "$RUNTIME_DIR/logs"
  run_browser_spec override "$RUNTIME_DIR/acceptance-output.log" account-operations-override.spec.ts
  capture_compose_logs "$RUNTIME_DIR/logs/compose.log" control node node-counter
  [[ "$(psql_count "SELECT count(*) FROM account_admin_operations WHERE command_id='$OVERRIDE_LIFECYCLE_COMMAND_ID' AND account_key='antigravity:$OVERRIDE_LIFECYCLE_EMAIL'")" == 1 ]] || { echo "override_target_account_mismatch" >&2; exit 1; }
  [[ "$(psql_count "SELECT count(*) FROM account_admin_operations WHERE command_id='$OVERRIDE_LIFECYCLE_COMMAND_ID' AND lifecycle_override_at IS NOT NULL AND lifecycle_override_reason='process_restarted'")" == 1 ]] || { echo "lifecycle_override_mismatch" >&2; exit 1; }
  [[ "$(psql_count "SELECT count(*) FROM account_admin_operations WHERE command_id='$OVERRIDE_LIFECYCLE_COMMAND_ID' AND same_account_override_at IS NOT NULL AND same_account_override_reason='process_restarted'")" == 1 ]] || { echo "same_account_override_mismatch" >&2; exit 1; }
  [[ "$(psql_count "SELECT count(*) FROM account_admin_command_receipts WHERE target_operation_command_id='$OVERRIDE_LIFECYCLE_COMMAND_ID' AND command_kind='account.same_account_override' AND http_status=200")" == 1 ]] || { echo "same_account_receipt_mismatch" >&2; exit 1; }
  [[ "$(psql_count "SELECT count(*) FROM audit_logs WHERE category='account_admin' AND action='account.same_account_override' AND result='success' AND details->>'target_operation_command_id'='$OVERRIDE_LIFECYCLE_COMMAND_ID' AND details->>'reason'='process_restarted'")" == 1 ]] || { echo "same_account_audit_mismatch" >&2; exit 1; }
  [[ "$(native_mutation_count "$RUNTIME_DIR/logs/compose.log" PATCH "/v0/management/auth-files/status")" == 0 ]] || { echo "same_account_native_mutation_unexpected" >&2; exit 1; }
  scan_override_secret() {
    scan_secret_value "secret" "$1"
  }
  scan_override_secret "$NODE_MANAGEMENT_PASSWORD"
  scan_override_secret "$(cat "$RUNTIME_DIR/account-operation-intent-key")"
  scan_override_secret "$(cat "$RUNTIME_DIR/bootstrap-secret")"
  scan_override_secret "$(cat "$RUNTIME_DIR/admin-password")"
  scan_override_secret "$(cat "$RUNTIME_DIR/second-admin-password")"
  echo "OVERRIDE_SECRET_SCAN=PASS"
  echo "OVERRIDE_LIFECYCLE=PASS"
  echo "OVERRIDE_SAME_ACCOUNT=PASS"
  echo "OVERRIDE_CANCEL=PASS"
  exit 0
fi
if [[ "$MODE" == "security-replay" ]]; then
  ACCEPTANCE_FAILURE_LAYER=fixture
  export ACCEPTANCE_DISABLE_EMAIL="$DISABLE_EMAIL"
  export ACCEPTANCE_SECURITY_REPLAY_EVIDENCE_FILE="$RUNTIME_DIR/security-replay-evidence.json"
  export ACCEPTANCE_BROWSER_CONSOLE_FILE="$RUNTIME_DIR/browser-console.log"
  export CONTROL_E2E_PLAYWRIGHT_OUTPUT_DIR="$RUNTIME_DIR/playwright-output"
  export CONTROL_E2E_BASE_URL="$ACCEPTANCE_BASE_URL"
  mkdir -p "$RUNTIME_DIR/logs"
  if ! run_browser_spec security-replay "$RUNTIME_DIR/acceptance-output.log" account-operations-security-replay.spec.ts; then
    exit 1
  fi
  capture_compose_logs "$RUNTIME_DIR/logs/compose.log" control node node-counter
  duplicate_command_id="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["duplicate"]["command_id"])' "$RUNTIME_DIR/security-replay-evidence.json")"
  [[ "$(psql_count "SELECT count(*) FROM account_admin_operations WHERE command_id='$duplicate_command_id'")" == 1 ]] || { echo "duplicate_operation_count_mismatch" >&2; exit 1; }
  scan_security_replay_secret() { scan_secret_value "$1" "$2"; }
  scan_security_replay_secret "node-management-secret" "$NODE_MANAGEMENT_PASSWORD"
  scan_security_replay_secret "intent-key" "$(cat "$RUNTIME_DIR/account-operation-intent-key")"
  scan_security_replay_secret "bootstrap-secret" "$(cat "$RUNTIME_DIR/bootstrap-secret")"
  scan_security_replay_secret "auth-keyring" "$(cat "$RUNTIME_DIR/auth-keyring.json")"
  scan_security_replay_secret "admin-password" "$(cat "$RUNTIME_DIR/admin-password")"
  scan_security_replay_secret "second-admin-password" "$(cat "$RUNTIME_DIR/second-admin-password")"
  echo "SECURITY_REPLAY_SECRET_SCAN=PASS"
  echo "DUPLICATE_SUBMIT=PASS"
  echo "DUPLICATE_HTTP_MUTATIONS=1"
  exit 0
fi
if [[ "$MODE" == "upload" ]]; then
  ACCEPTANCE_FAILURE_LAYER=fixture
  export ACCEPTANCE_UPLOAD_CREDENTIAL_FILE="$RUNTIME_DIR/upload-credential.json"
  export ACCEPTANCE_UPLOAD_EMAIL="$UPLOAD_EMAIL"
  export ACCEPTANCE_UPLOAD_SECRET_MARKER="$UPLOAD_SECRET_MARKER"
  export ACCEPTANCE_UPLOAD_EVIDENCE_FILE="$RUNTIME_DIR/upload-evidence.json"
  export ACCEPTANCE_BROWSER_CONSOLE_FILE="$RUNTIME_DIR/browser-console.log"
  export CONTROL_E2E_PLAYWRIGHT_OUTPUT_DIR="$RUNTIME_DIR/playwright-output"
  export CONTROL_E2E_BASE_URL="$ACCEPTANCE_BASE_URL"
  if ! run_browser_spec upload "$RUNTIME_DIR/acceptance-output.log" account-operations-upload.spec.ts; then
    compose logs --no-color control node node-counter >&2 || true
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
  mkdir -p "$RUNTIME_DIR/logs"
  capture_compose_logs "$RUNTIME_DIR/logs/compose.log" control node node-counter
  native_post_count="$(native_mutation_count "$RUNTIME_DIR/logs/compose.log" POST "/v0/management/auth-files")"
  [[ "$native_post_count" == 1 ]] || { echo "native_upload_post_count_mismatch:${native_post_count:-0}" >&2; exit 1; }
  node_observation="$RUNTIME_DIR/node-observation.json"
  curl --noproxy '*' --silent --show-error --fail --header "Authorization: Bearer $NODE_MANAGEMENT_PASSWORD" "http://127.0.0.1:${NODE_PORT}/v0/management/auth-files" -o "$node_observation"
  grep -Fq "$UPLOAD_EMAIL" "$node_observation" || { echo "node_upload_observation_missing" >&2; exit 1; }
  rm -f -- "$node_observation"
  scan_secret() {
    scan_secret_value "secret" "$1"
  }
  scan_secret "$UPLOAD_SECRET_MARKER"
  scan_secret "$NODE_MANAGEMENT_PASSWORD"
  scan_secret 'phase7-disposable-seed-token'
  scan_secret "$(cat "$RUNTIME_DIR/account-operation-intent-key")"
  scan_secret "$(cat "$RUNTIME_DIR/bootstrap-secret")"
  scan_secret "$(cat "$RUNTIME_DIR/auth-keyring.json")"
  scan_secret "$(cat "$RUNTIME_DIR/admin-password")"
  scan_secret "$(cat "$RUNTIME_DIR/second-admin-password")"
  echo "SECRET_ARTIFACT_SCAN=PASS"
  echo "UPLOAD_NEW_BROWSER=PASS"
  echo "UPLOAD_NEW_CONTROL=PASS"
  echo "UPLOAD_NEW_POSTGRES=PASS"
  echo "UPLOAD_NEW_NODE=PASS"
  echo "UPLOAD_NEW_NATIVE_MUTATIONS=$native_post_count"
  exit 0
fi
if [[ "$MODE" == "disable" ]]; then
  ACCEPTANCE_FAILURE_LAYER=fixture
  export ACCEPTANCE_DISABLE_EMAIL="$DISABLE_EMAIL"
  export ACCEPTANCE_DISABLE_EVIDENCE_FILE="$RUNTIME_DIR/disable-evidence.json"
  export CONTROL_E2E_PLAYWRIGHT_OUTPUT_DIR="$RUNTIME_DIR/playwright-output"
  export CONTROL_E2E_BASE_URL="$ACCEPTANCE_BASE_URL"
  if ! run_browser_spec disable "$RUNTIME_DIR/acceptance-output.log" account-operations-disable.spec.ts; then
    compose logs --no-color control node node-counter >&2 || true
    exit 1
  fi
  mkdir -p "$RUNTIME_DIR/logs"
  capture_compose_logs "$RUNTIME_DIR/logs/compose.log" control node node-counter
  command_id="$(sed -n 's/.*"command_id":"\([0-9a-f-]*\)".*/\1/p' "$RUNTIME_DIR/disable-evidence.json")"
  [[ "$command_id" =~ ^[0-9a-f-]{36}$ ]] || { echo "disable_evidence_missing_command_id" >&2; exit 1; }
  [[ "$(psql_count "SELECT count(*) FROM account_admin_operations WHERE command_id='$command_id' AND operation_kind='disable' AND execution_state='remote_applied'")" == 1 ]] || { echo "disable_operation_mismatch" >&2; exit 1; }
  [[ "$(psql_count "SELECT count(*) FROM account_admin_command_receipts WHERE command_id='$command_id'")" == 1 ]] || { echo "disable_receipt_mismatch" >&2; exit 1; }
  [[ "$(psql_count "SELECT count(*) FROM audit_logs WHERE category='account_admin' AND details->>'command_id'='$command_id'")" == 1 ]] || { echo "disable_audit_mismatch" >&2; exit 1; }
  native_patch_count="$(native_mutation_count "$RUNTIME_DIR/logs/compose.log" PATCH "/v0/management/auth-files/status")"
  [[ "$native_patch_count" == 1 ]] || { echo "disable_native_patch_count_mismatch:${native_patch_count:-0}" >&2; exit 1; }
  scan_disable_secret() {
    scan_secret_value "secret" "$1"
  }
  scan_disable_secret "$NODE_MANAGEMENT_PASSWORD"
  scan_disable_secret "$(cat "$RUNTIME_DIR/account-operation-intent-key")"
  scan_disable_secret "$(cat "$RUNTIME_DIR/bootstrap-secret")"
  scan_disable_secret "$(cat "$RUNTIME_DIR/auth-keyring.json")"
  scan_disable_secret "$(cat "$RUNTIME_DIR/admin-password")"
  scan_disable_secret "$(cat "$RUNTIME_DIR/second-admin-password")"
  echo "DISABLE_SECRET_SCAN=PASS"
  echo "DISABLE_HTTP=PASS"
  echo "DISABLE_POSTGRES=PASS"
  echo "DISABLE_RECEIPT=PASS"
  echo "DISABLE_AUDIT=PASS"
  echo "DISABLE_NATIVE_PATCHES=$native_patch_count"
  exit 0
fi
if [[ "$MODE" == "replace" || "$MODE" == "replace-discovery" ]]; then
  ACCEPTANCE_FAILURE_LAYER=fixture
  [[ "$MODE" == "replace-discovery" ]] && export ACCEPTANCE_REPLACE_DISCOVERY_ONLY=1
  export ACCEPTANCE_REPLACE_EMAIL="$REPLACE_EMAIL"
  export ACCEPTANCE_REPLACE_CREDENTIAL_FILE="$RUNTIME_DIR/replace-credential.json"
  export ACCEPTANCE_REPLACE_SECRET_MARKER="$REPLACE_SECRET_MARKER"
  export ACCEPTANCE_NODE_PORT="$NODE_PORT"
  export ACCEPTANCE_NODE_MANAGEMENT_PASSWORD="$NODE_MANAGEMENT_PASSWORD"
  export ACCEPTANCE_REPLACE_EVIDENCE_FILE="$RUNTIME_DIR/replace-evidence.json"
  export ACCEPTANCE_BROWSER_CONSOLE_FILE="$RUNTIME_DIR/browser-console.log"
  export CONTROL_E2E_PLAYWRIGHT_OUTPUT_DIR="$RUNTIME_DIR/playwright-output"
  export CONTROL_E2E_BASE_URL="$ACCEPTANCE_BASE_URL"
  mkdir -p "$RUNTIME_DIR/logs"
  if ! run_browser_spec replace "$RUNTIME_DIR/acceptance-output.log" account-operations-replace.spec.ts; then
    compose logs --no-color control node node-counter >&2 || true
    exit 1
  fi
  if [[ "$MODE" == "replace-discovery" ]]; then
    [[ "$(psql_count "SELECT count(*) FROM account_inventory_poll_runs WHERE instance_id='00000000-0000-4000-8000-000000000047' AND status='finalized'")" -ge 1 ]] || { echo "replace_discovery_poll_run_missing" >&2; exit 1; }
    [[ "$(psql_count "SELECT count(*) FROM account_inventory_snapshot_items WHERE instance_id='00000000-0000-4000-8000-000000000047' AND provider='antigravity' AND normalized_email='$REPLACE_EMAIL'")" -ge 1 ]] || { echo "replace_discovery_snapshot_missing" >&2; exit 1; }
    [[ "$(psql_count "SELECT count(*) FROM account_inventory WHERE instance_id='00000000-0000-4000-8000-000000000047' AND provider='antigravity' AND normalized_email='$REPLACE_EMAIL' AND lifecycle='present'")" == 1 ]] || { echo "replace_discovery_inventory_missing" >&2; exit 1; }
    echo "REPLACE_DISCOVERY=PASS"
    echo "REPLACE_DISCOVERY_POLL_RUN=PASS"
    echo "REPLACE_DISCOVERY_SNAPSHOT=PASS"
    echo "REPLACE_DISCOVERY_INVENTORY_DB=PASS"
    echo "REPLACE_HTTP=NOT_RUN"
    echo "REPLACE_NATIVE_POSTS=0"
    exit 0
  fi
  capture_compose_logs "$RUNTIME_DIR/logs/compose.log" control node node-counter
  command_id="$(sed -n 's/.*"command_id":"\([0-9a-f-]*\)".*/\1/p' "$RUNTIME_DIR/replace-evidence.json")"
  [[ "$command_id" =~ ^[0-9a-f-]{36}$ ]] || { echo "replace_evidence_missing_command_id" >&2; exit 1; }
  [[ "$(psql_count "SELECT count(*) FROM admin_command_registry WHERE command_id='$command_id' AND command_kind='account.replace_existing'")" == 1 ]] || { echo "replace_registry_mismatch" >&2; exit 1; }
  [[ "$(psql_count "SELECT count(*) FROM account_admin_operations WHERE command_id='$command_id' AND operation_kind='replace_existing' AND account_key='antigravity:$REPLACE_EMAIL' AND execution_state='remote_applied'")" == 1 ]] || { echo "replace_operation_mismatch" >&2; exit 1; }
  [[ "$(psql_count "SELECT count(*) FROM account_admin_command_receipts WHERE command_id='$command_id'")" == 1 ]] || { echo "replace_receipt_mismatch" >&2; exit 1; }
  [[ "$(psql_count "SELECT count(*) FROM audit_logs WHERE category='account_admin' AND details->>'command_id'='$command_id'")" == 1 ]] || { echo "replace_audit_mismatch" >&2; exit 1; }
  native_post_count="$(native_mutation_count "$RUNTIME_DIR/logs/compose.log" POST "/v0/management/auth-files")"
  [[ "$native_post_count" == 1 ]] || { echo "replace_native_post_count_mismatch:${native_post_count:-0}" >&2; exit 1; }
  scan_replace_secret() {
    scan_secret_value "$1" "$2"
  }
  scan_replace_secret "replacement-marker" "$REPLACE_SECRET_MARKER"
  scan_replace_secret "base-marker" "$REPLACE_BASE_SECRET_MARKER"
  scan_replace_secret "replacement-refresh-token" "phase7-disposable-replace-refresh-token"
  scan_replace_secret "base-token" "phase7-disposable-replace-base-token"
  scan_replace_secret "node-management-secret" "$NODE_MANAGEMENT_PASSWORD"
  scan_replace_secret "bootstrap-secret" "$(cat "$RUNTIME_DIR/bootstrap-secret")"
  scan_replace_secret "auth-keyring" "$(cat "$RUNTIME_DIR/auth-keyring.json")"
  scan_replace_secret "admin-password" "$(cat "$RUNTIME_DIR/admin-password")"
  scan_replace_secret "second-admin-password" "$(cat "$RUNTIME_DIR/second-admin-password")"
  scan_replace_binary_secret() {
    scan_secret_file "$1" "$2"
  }
  scan_replace_binary_secret "intent-key" "$RUNTIME_DIR/account-operation-intent-key"
  echo "REPLACE_HTTP=PASS"
  echo "REPLACE_POSTGRES=PASS"
  echo "REPLACE_RECEIPT=PASS"
  echo "REPLACE_AUDIT=PASS"
  echo "REPLACE_NATIVE_POSTS=$native_post_count"
  echo "REPLACE_NODE=PASS"
  echo "REPLACE_INVENTORY=PASS"
  echo "REPLACE_FILE_LIFECYCLE=PASS"
  echo "REPLACE_SECRET_SCAN=PASS"
  exit 0
fi
if [[ "$MODE" == "remove" ]]; then
  ACCEPTANCE_FAILURE_LAYER=fixture
  export ACCEPTANCE_REMOVE_EMAIL="$REMOVE_EMAIL"
  export ACCEPTANCE_NODE_PORT="$NODE_PORT"
  export ACCEPTANCE_NODE_MANAGEMENT_PASSWORD="$NODE_MANAGEMENT_PASSWORD"
  export ACCEPTANCE_REMOVE_EVIDENCE_FILE="$RUNTIME_DIR/remove-evidence.json"
  export ACCEPTANCE_BROWSER_CONSOLE_FILE="$RUNTIME_DIR/browser-console.log"
  export CONTROL_E2E_PLAYWRIGHT_OUTPUT_DIR="$RUNTIME_DIR/playwright-output"
  export CONTROL_E2E_BASE_URL="$ACCEPTANCE_BASE_URL"
  mkdir -p "$RUNTIME_DIR/logs"
  if ! run_browser_spec remove "$RUNTIME_DIR/acceptance-output.log" account-operations-remove.spec.ts; then
    compose logs --no-color control node node-counter >&2 || true
    exit 1
  fi
  capture_compose_logs "$RUNTIME_DIR/logs/compose.log" control node node-counter
  command_id="$(sed -n 's/.*"command_id":"\([0-9a-f-]*\)".*/\1/p' "$RUNTIME_DIR/remove-evidence.json")"
  [[ "$command_id" =~ ^[0-9a-f-]{36}$ ]] || { echo "remove_evidence_missing_command_id" >&2; exit 1; }
  [[ "$(psql_count "SELECT count(*) FROM account_admin_operations WHERE command_id='$command_id' AND operation_kind='remove' AND account_key='antigravity:$REMOVE_EMAIL' AND execution_state='remote_applied'")" == 1 ]] || { echo "remove_operation_mismatch" >&2; exit 1; }
  native_delete_count="$(native_mutation_count "$RUNTIME_DIR/logs/compose.log" DELETE "/v0/management/auth-files")"
  [[ "$native_delete_count" == 1 ]] || { echo "remove_native_delete_count_mismatch:${native_delete_count:-0}" >&2; exit 1; }
  scan_remove_secret() {
    scan_secret_value "$1" "$2"
  }
  scan_remove_secret "access-token" "phase7-disposable-remove-token"
  scan_remove_secret "node-management-secret" "$NODE_MANAGEMENT_PASSWORD"
  scan_remove_secret "intent-key" "$(cat "$RUNTIME_DIR/account-operation-intent-key")"
  scan_remove_secret "bootstrap-secret" "$(cat "$RUNTIME_DIR/bootstrap-secret")"
  scan_remove_secret "auth-keyring" "$(cat "$RUNTIME_DIR/auth-keyring.json")"
  scan_remove_secret "admin-password" "$(cat "$RUNTIME_DIR/admin-password")"
  scan_remove_secret "second-admin-password" "$(cat "$RUNTIME_DIR/second-admin-password")"
  echo "REMOVE_HTTP=PASS"
  echo "REMOVE_POSTGRES=PASS"
  echo "REMOVE_NATIVE_DELETES=$native_delete_count"
  echo "REMOVE_NODE=PASS"
  echo "REMOVE_CANCEL=PASS"
  echo "REMOVE_SECRET_SCAN=PASS"
  exit 0
fi
if [[ "$MODE" == "enable-fixture" ]]; then
  ACCEPTANCE_FAILURE_LAYER=fixture
  export ACCEPTANCE_ENABLE_EMAIL="$ENABLE_EMAIL"
  export ACCEPTANCE_NODE_PORT="$NODE_PORT"
  export ACCEPTANCE_NODE_MANAGEMENT_PASSWORD="$NODE_MANAGEMENT_PASSWORD"
  export ACCEPTANCE_ENABLE_FIXTURE_EVIDENCE_FILE="$RUNTIME_DIR/enable-fixture-evidence.json"
  export ACCEPTANCE_BROWSER_CONSOLE_FILE="$RUNTIME_DIR/browser-console.log"
  export CONTROL_E2E_PLAYWRIGHT_OUTPUT_DIR="$RUNTIME_DIR/playwright-output"
  export CONTROL_E2E_BASE_URL="$ACCEPTANCE_BASE_URL"
  mkdir -p "$RUNTIME_DIR/logs"
  set +e
  run_browser_spec enable-fixture "$RUNTIME_DIR/acceptance-output.log" account-operations-enable-fixture.spec.ts
  fixture_test_status="$?"
  set -e
  [[ "$fixture_test_status" == 0 ]] || { compose logs --no-color control node node-counter >&2 || true; exit "$fixture_test_status"; }
  compose up -d --force-recreate control
  wait_for_http CONTROL_READINESS "http://127.0.0.1:${HTTP_PORT}/api/healthz" 60
  [[ "$(psql_count "SELECT count(*) FROM account_inventory WHERE instance_id='00000000-0000-4000-8000-000000000047' AND provider='antigravity' AND normalized_email='$ENABLE_EMAIL' AND lifecycle='present' AND basic_status='disabled'")" == 1 ]] || { echo "enable_fixture_inventory_db_mismatch" >&2; exit 1; }
  [[ "$(psql_count "SELECT count(*) FROM account_inventory_snapshot_items WHERE instance_id='00000000-0000-4000-8000-000000000047' AND provider='antigravity' AND normalized_email='$ENABLE_EMAIL' AND basic_status='disabled'")" == 1 ]] || { echo "enable_fixture_snapshot_db_mismatch" >&2; exit 1; }
  capture_compose_logs "$RUNTIME_DIR/logs/control.log" control
  capture_compose_logs "$RUNTIME_DIR/logs/node.log" node
  capture_compose_logs "$RUNTIME_DIR/logs/node-counter.log" node-counter
  scan_enable_fixture_secret() {
    scan_secret_value "$1" "$2"
  }
  scan_enable_fixture_secret "credential-marker" "$ENABLE_SECRET_MARKER"
  scan_enable_fixture_secret "access-token" "phase7-disposable-enable-token"
  scan_enable_fixture_secret "node-management-secret" "$NODE_MANAGEMENT_PASSWORD"
  scan_enable_fixture_binary_secret() {
    scan_secret_file "$1" "$2"
  }
  scan_enable_fixture_binary_secret "intent-key" "$RUNTIME_DIR/account-operation-intent-key"
  scan_enable_fixture_secret "bootstrap-secret" "$(cat "$RUNTIME_DIR/bootstrap-secret")"
  scan_enable_fixture_secret "auth-keyring" "$(cat "$RUNTIME_DIR/auth-keyring.json")"
  scan_enable_fixture_secret "admin-password" "$(cat "$RUNTIME_DIR/admin-password")"
  scan_enable_fixture_secret "second-admin-password" "$(cat "$RUNTIME_DIR/second-admin-password")"
  echo "ENABLE_FIXTURE_SECRET_SCAN=PASS"
  echo "ENABLE_FIXTURE_NODE=PASS"
  echo "ENABLE_FIXTURE_SNAPSHOT=PASS"
  echo "ENABLE_FIXTURE_INVENTORY_DB=PASS"
  echo "ENABLE_FIXTURE_INVENTORY=PASS"
  echo "ENABLE_FIXTURE_BROWSER=PASS"
  echo "ENABLE_FIXTURE_READY=PASS"
  exit 0
fi
if [[ "$MODE" == "enable" ]]; then
  ACCEPTANCE_FAILURE_LAYER=fixture
  export ACCEPTANCE_ENABLE_EMAIL="$ENABLE_EMAIL"
  export ACCEPTANCE_NODE_PORT="$NODE_PORT"
  export ACCEPTANCE_NODE_MANAGEMENT_PASSWORD="$NODE_MANAGEMENT_PASSWORD"
  export ACCEPTANCE_ENABLE_FIXTURE_EVIDENCE_FILE="$RUNTIME_DIR/enable-fixture-evidence.json"
  export ACCEPTANCE_ENABLE_EVIDENCE_FILE="$RUNTIME_DIR/enable-evidence.json"
  export ACCEPTANCE_BROWSER_CONSOLE_FILE="$RUNTIME_DIR/browser-console.log"
  export CONTROL_E2E_PLAYWRIGHT_OUTPUT_DIR="$RUNTIME_DIR/playwright-output"
  export CONTROL_E2E_BASE_URL="$ACCEPTANCE_BASE_URL"
  mkdir -p "$RUNTIME_DIR/logs"
  set +e
  run_browser_spec enable-fixture "$RUNTIME_DIR/fixture-output.log" account-operations-enable-fixture.spec.ts
  fixture_test_status="$?"
  set -e
  [[ "$fixture_test_status" == 0 ]] || { compose logs --no-color control node node-counter >&2 || true; exit "$fixture_test_status"; }
  compose up -d --force-recreate control
  wait_for_http CONTROL_READINESS "http://127.0.0.1:${HTTP_PORT}/api/healthz" 60
  capture_compose_logs "$RUNTIME_DIR/logs/control-before-enable.log" control
  capture_compose_logs "$RUNTIME_DIR/logs/node-before-enable.log" node
  capture_compose_logs "$RUNTIME_DIR/logs/node-counter-before-enable.log" node-counter
  set +e
  run_browser_spec enable "$RUNTIME_DIR/enable-output.log" account-operations-enable.spec.ts
  enable_test_status="$?"
  set -e
  [[ "$enable_test_status" == 0 ]] || { compose logs --no-color control node node-counter >&2 || true; exit "$enable_test_status"; }
  capture_compose_logs "$RUNTIME_DIR/logs/control.log" control
  capture_compose_logs "$RUNTIME_DIR/logs/node.log" node
  capture_compose_logs "$RUNTIME_DIR/logs/node-counter.log" node-counter
  command_id="$(sed -n 's/.*"command_id":"\([0-9a-f-]*\)".*/\1/p' "$RUNTIME_DIR/enable-evidence.json")"
  [[ "$command_id" =~ ^[0-9a-f-]{36}$ ]] || { echo "enable_evidence_missing_command_id" >&2; exit 1; }
  [[ "$(psql_count "SELECT count(*) FROM admin_command_registry WHERE command_id='$command_id' AND command_kind='account.enable'")" == 1 ]] || { echo "enable_registry_mismatch" >&2; exit 1; }
  [[ "$(psql_count "SELECT count(*) FROM account_admin_operations WHERE command_id='$command_id' AND operation_kind='enable' AND account_key='antigravity:$ENABLE_EMAIL' AND execution_state='remote_applied'")" == 1 ]] || { echo "enable_operation_mismatch" >&2; exit 1; }
  [[ "$(psql_count "SELECT count(*) FROM account_admin_command_receipts WHERE command_id='$command_id'")" == 1 ]] || { echo "enable_receipt_mismatch" >&2; exit 1; }
  [[ "$(psql_count "SELECT count(*) FROM audit_logs WHERE category='account_admin' AND details->>'command_id'='$command_id'")" == 1 ]] || { echo "enable_audit_mismatch" >&2; exit 1; }
  before_patch_count="$(native_mutation_count "$RUNTIME_DIR/logs/node-counter-before-enable.log" PATCH "/v0/management/auth-files/status")"
  after_patch_count="$(native_mutation_count "$RUNTIME_DIR/logs/node-counter.log" PATCH "/v0/management/auth-files/status")"
  native_patch_count=$((after_patch_count - before_patch_count))
  [[ "$native_patch_count" == 1 ]] || { echo "enable_native_patch_count_mismatch:${native_patch_count:-0}" >&2; exit 1; }
  curl --noproxy '*' --silent --show-error --fail --header "Authorization: Bearer $NODE_MANAGEMENT_PASSWORD" "http://127.0.0.1:${NODE_PORT}/v0/management/auth-files" -o "$RUNTIME_DIR/enable-node-observation.json"
  rg -Fq '"email":"'"$ENABLE_EMAIL"'"' "$RUNTIME_DIR/enable-node-observation.json" || { echo "enable_node_observation_missing" >&2; exit 1; }
  rg -Fq '"disabled":false' "$RUNTIME_DIR/enable-node-observation.json" || { echo "enable_node_state_mismatch" >&2; exit 1; }
  rm -f -- "$RUNTIME_DIR/enable-node-observation.json"
  scan_enable_secret() {
    scan_secret_value "$1" "$2"
  }
  scan_enable_secret "credential-marker" "$ENABLE_SECRET_MARKER"
  scan_enable_secret "access-token" "phase7-disposable-enable-token"
  scan_enable_secret "node-management-secret" "$NODE_MANAGEMENT_PASSWORD"
  scan_enable_binary_secret() {
    scan_secret_file "$1" "$2"
  }
  scan_enable_binary_secret "intent-key" "$RUNTIME_DIR/account-operation-intent-key"
  scan_enable_secret "bootstrap-secret" "$(cat "$RUNTIME_DIR/bootstrap-secret")"
  scan_enable_secret "auth-keyring" "$(cat "$RUNTIME_DIR/auth-keyring.json")"
  scan_enable_secret "admin-password" "$(cat "$RUNTIME_DIR/admin-password")"
  scan_enable_secret "second-admin-password" "$(cat "$RUNTIME_DIR/second-admin-password")"
  echo "ENABLE_FIXTURE_READY=PASS"
  echo "ENABLE_HTTP=PASS"
  echo "ENABLE_POSTGRES=PASS"
  echo "ENABLE_RECEIPT=PASS"
  echo "ENABLE_AUDIT=PASS"
  echo "ENABLE_NATIVE_PATCHES=$native_patch_count"
  echo "ENABLE_NODE=PASS"
  echo "ENABLE_SECRET_SCAN=PASS"
  exit 0
fi
