#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
CONTROL_DIR="$(cd -- "$ROOT/../../.." && pwd)"
COMPOSE_FILE="$CONTROL_DIR/deploy/acceptance/compose.yaml"
RUNTIME_DIR="${ACCEPTANCE_RUNTIME_DIR:-}"
OVERRIDE_FILE=""
PROJECT="${ACCEPTANCE_COMPOSE_PROJECT:-relay-control-harness-$$}"
MODE="${1:-all}"
[[ "$MODE" == "all" || "$MODE" == "auth" || "$MODE" == "startup" || "$MODE" == "upload" || "$MODE" == "disable" || "$MODE" == "enable-fixture" || "$MODE" == "enable" || "$MODE" == "replace" || "$MODE" == "replace-discovery" || "$MODE" == "remove" || "$MODE" == "override" ]] || { echo "usage: ACCEPTANCE_RUNTIME_DIR=/external/path $0 [all|auth|startup|upload|disable|enable-fixture|enable|replace|replace-discovery|remove|override]" >&2; exit 2; }
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
DISABLE_EMAIL="phase7-disable-${PROJECT}@example.invalid"
printf '{"type":"antigravity","email":"%s","access_token":"phase7-disposable-disable-token"}\n' "$DISABLE_EMAIL" > "$RUNTIME_DIR/node/auths/phase7-disable.json"
ENABLE_EMAIL="phase7-enable-${PROJECT}@example.invalid"
ENABLE_SECRET_MARKER="PHASE7_ENABLE_SECRET_${PROJECT##*-}"
printf '{"type":"antigravity","email":"%s","access_token":"phase7-disposable-enable-token","phase7_marker":"%s","disabled":false}\n' "$ENABLE_EMAIL" "$ENABLE_SECRET_MARKER" > "$RUNTIME_DIR/node/auths/phase7-enable.json"
REPLACE_EMAIL="phase7-replace-${PROJECT}@example.invalid"
REPLACE_BASE_SECRET_MARKER="PHASE7_REPLACE_BASE_${PROJECT##*-}"
REPLACE_SECRET_MARKER="PHASE7_REPLACE_SECRET_${PROJECT##*-}"
printf '{"type":"antigravity","email":"%s","access_token":"phase7-disposable-replace-base-token","phase7_marker":"%s","disabled":false}\n' "$REPLACE_EMAIL" "$REPLACE_BASE_SECRET_MARKER" > "$RUNTIME_DIR/node/auths/phase7-replace-base.json"
printf '{"type":"antigravity","email":"%s","refresh_token":"phase7-disposable-replace-refresh-token","phase7_marker":"%s","status":"active","unavailable":false}\n' "$REPLACE_EMAIL" "$REPLACE_SECRET_MARKER" > "$RUNTIME_DIR/replace-credential.json"
REMOVE_EMAIL="phase7-remove-${PROJECT}@example.invalid"
printf '{"type":"antigravity","email":"%s","access_token":"phase7-disposable-remove-token","disabled":false}\n' "$REMOVE_EMAIL" > "$RUNTIME_DIR/node/auths/phase7-remove.json"
OVERRIDE_LIFECYCLE_EMAIL="phase7-override-lifecycle-${PROJECT}@example.invalid"
OVERRIDE_SAME_EMAIL="phase7-override-same-${PROJECT}@example.invalid"
printf '{"type":"antigravity","email":"%s","access_token":"phase7-disposable-override-lifecycle-token","disabled":false}\n' "$OVERRIDE_LIFECYCLE_EMAIL" > "$RUNTIME_DIR/node/auths/phase7-override-lifecycle.json"
printf '{"type":"antigravity","email":"%s","access_token":"phase7-disposable-override-same-token","disabled":false}\n' "$OVERRIDE_SAME_EMAIL" > "$RUNTIME_DIR/node/auths/phase7-override-same.json"
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
INVENTORY_POLL_ENABLED="false"
INVENTORY_LIFECYCLE_ENABLED="false"
[[ "$MODE" == "disable" || "$MODE" == "enable-fixture" || "$MODE" == "enable" || "$MODE" == "replace" || "$MODE" == "replace-discovery" || "$MODE" == "remove" || "$MODE" == "override" ]] && INVENTORY_POLL_ENABLED="true"
[[ "$MODE" == "disable" || "$MODE" == "enable-fixture" || "$MODE" == "enable" || "$MODE" == "replace" || "$MODE" == "replace-discovery" || "$MODE" == "remove" || "$MODE" == "override" ]] && INVENTORY_LIFECYCLE_ENABLED="true"
export INVENTORY_POLL_ENABLED INVENTORY_LIFECYCLE_ENABLED
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
      CONTROL_ACCOUNT_INVENTORY_POLL_START_GRACE: "299s"
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
ensure_candidate_image
EXPECTED_CONTROL_IMAGE_ID="$(docker image inspect "$IMAGE" --format '{{.Id}}')"
echo "EXPECTED_CONTROL_IMAGE_ID=$EXPECTED_CONTROL_IMAGE_ID"
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
INSERT INTO relay_node_assets(instance_id,display_name,node_type,driver_contract_version,management_endpoint,reader_secret_ref)
VALUES ('00000000-0000-4000-8000-000000000047','Acceptance Node','cliproxyapi','cliproxyapi.auth-files.v1','http://node-counter:8318','file://phase7/node-management');
INSERT INTO node_capabilities(instance_id,node_type,driver_contract_version,capability) VALUES
  ('00000000-0000-4000-8000-000000000047','cliproxyapi','cliproxyapi.auth-files.v1','management_health_read'),
  ('00000000-0000-4000-8000-000000000047','cliproxyapi','cliproxyapi.auth-files.v1','management_account_inventory_read');
INSERT INTO relay_node_inventory_monitoring_activations(instance_id,effective_from,reason,actor,created_at)
VALUES ('00000000-0000-4000-8000-000000000047',
        to_timestamp(floor(extract(epoch FROM statement_timestamp()) / 300) * 300),
        'deployment_enable','acceptance-harness',
        to_timestamp(floor(extract(epoch FROM statement_timestamp()) / 300) * 300));
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
RUNNING_CONTROL_IMAGE_ID="$(docker inspect "$CONTROL_CONTAINER" --format '{{.Image}}')"
[[ "$RUNNING_CONTROL_IMAGE_ID" == "$EXPECTED_CONTROL_IMAGE_ID" ]] || { echo "control_image_provenance_mismatch" >&2; exit 1; }
echo "RUNNING_CONTROL_IMAGE_ID=$RUNNING_CONTROL_IMAGE_ID"
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
if [[ "$MODE" == "override" ]]; then
  OVERRIDE_LIFECYCLE_COMMAND_ID="$(python3 -c 'import uuid; print(uuid.uuid4())')"
  OVERRIDE_SAME_COMMAND_ID="$(python3 -c 'import uuid; print(uuid.uuid4())')"
  OVERRIDE_ADMIN_ID="$(compose exec -T postgres psql --username relay_control_migrator --dbname relay_station_control --tuples-only --no-align --command "SELECT admin_id FROM control_admin_users WHERE status='enabled' ORDER BY activated_at LIMIT 1" | tr -d '\r\n[:space:]')"
  compose exec -T postgres psql --set ON_ERROR_STOP=1 --username relay_control_migrator --dbname relay_station_control <<SQL
SELECT public.control_accept_account_admin_operation_v1('$OVERRIDE_LIFECYCLE_COMMAND_ID','$OVERRIDE_ADMIN_ID','account.disable',decode(repeat('aa',32),'hex'),NULL,'00000000-0000-4000-8000-000000000047','antigravity:$OVERRIDE_LIFECYCLE_EMAIL','disable',NULL);
SELECT public.control_admit_account_dispatch_v1('$OVERRIDE_LIFECYCLE_COMMAND_ID','00000000-0000-4000-8000-000000000047','antigravity:$OVERRIDE_LIFECYCLE_EMAIL','override-fixture');
SELECT public.control_transition_account_admin_operation_v1('$OVERRIDE_LIFECYCLE_COMMAND_ID','dispatched','outcome_unknown');
SELECT public.control_accept_account_admin_operation_v1('$OVERRIDE_SAME_COMMAND_ID','$OVERRIDE_ADMIN_ID','account.disable',decode(repeat('bb',32),'hex'),NULL,'00000000-0000-4000-8000-000000000047','antigravity:$OVERRIDE_SAME_EMAIL','disable',NULL);
SELECT public.control_admit_account_dispatch_v1('$OVERRIDE_SAME_COMMAND_ID','00000000-0000-4000-8000-000000000047','antigravity:$OVERRIDE_SAME_EMAIL','override-fixture');
SELECT public.control_transition_account_admin_operation_v1('$OVERRIDE_SAME_COMMAND_ID','dispatched','outcome_unknown');
SQL
  export ACCEPTANCE_OVERRIDE_LIFECYCLE_EMAIL="$OVERRIDE_LIFECYCLE_EMAIL" ACCEPTANCE_OVERRIDE_SAME_EMAIL="$OVERRIDE_SAME_EMAIL" ACCEPTANCE_OVERRIDE_LIFECYCLE_COMMAND_ID="$OVERRIDE_LIFECYCLE_COMMAND_ID" ACCEPTANCE_OVERRIDE_SAME_COMMAND_ID="$OVERRIDE_SAME_COMMAND_ID" ACCEPTANCE_OVERRIDE_EVIDENCE_FILE="$RUNTIME_DIR/override-evidence.json" ACCEPTANCE_BROWSER_CONSOLE_FILE="$RUNTIME_DIR/browser-console.log" CONTROL_E2E_PLAYWRIGHT_OUTPUT_DIR="$RUNTIME_DIR/playwright-output" CONTROL_E2E_BASE_URL="$ACCEPTANCE_BASE_URL"
  mkdir -p "$RUNTIME_DIR/logs"
  CONTROL_E2E_BROWSER_CHANNEL=chromium npm --prefix "$CONTROL_DIR/web" run test:e2e -- account-operations-override.spec.ts
  compose logs --no-color control node node-counter > "$RUNTIME_DIR/logs/compose.log"
  psql_count() { compose exec -T postgres psql --username relay_control_migrator --dbname relay_station_control --tuples-only --no-align --command "$1" | tr -d '\r\n[:space:]'; }
  [[ "$(psql_count "SELECT count(*) FROM account_admin_operations WHERE command_id='$OVERRIDE_LIFECYCLE_COMMAND_ID' AND lifecycle_override_at IS NOT NULL AND lifecycle_override_reason='process_restarted'")" == 1 ]] || { echo "lifecycle_override_mismatch" >&2; exit 1; }
  [[ "$(psql_count "SELECT count(*) FROM account_admin_operations WHERE command_id='$OVERRIDE_SAME_COMMAND_ID' AND same_account_override_at IS NOT NULL AND same_account_override_reason='process_restarted'")" == 1 ]] || { echo "same_account_override_mismatch" >&2; exit 1; }
  scan_override_secret() { local value="$1"; [[ -n "$value" ]] && ! rg -l -F -- "$value" "$RUNTIME_DIR/logs" "$RUNTIME_DIR/playwright-output" "$RUNTIME_DIR/browser-console.log" "$RUNTIME_DIR/override-evidence.json" >/dev/null 2>&1; }
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
if [[ "$MODE" == "upload" ]]; then
  export ACCEPTANCE_UPLOAD_CREDENTIAL_FILE="$RUNTIME_DIR/upload-credential.json"
  export ACCEPTANCE_UPLOAD_EMAIL="$UPLOAD_EMAIL"
  export ACCEPTANCE_UPLOAD_SECRET_MARKER="$UPLOAD_SECRET_MARKER"
  export ACCEPTANCE_UPLOAD_EVIDENCE_FILE="$RUNTIME_DIR/upload-evidence.json"
  export ACCEPTANCE_BROWSER_CONSOLE_FILE="$RUNTIME_DIR/browser-console.log"
  export CONTROL_E2E_PLAYWRIGHT_OUTPUT_DIR="$RUNTIME_DIR/playwright-output"
  export CONTROL_E2E_BASE_URL="$ACCEPTANCE_BASE_URL"
  if ! CONTROL_E2E_BROWSER_CHANNEL=chromium npm --prefix "$CONTROL_DIR/web" run test:e2e -- account-operations-upload.spec.ts; then
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
  compose logs --no-color control node node-counter > "$RUNTIME_DIR/logs/compose.log"
  native_post_count="$(grep -Ec 'POST /v0/management/auth-files [0-9]{3}$' "$RUNTIME_DIR/logs/compose.log" || true)"
  [[ "$native_post_count" == 1 ]] || { echo "native_upload_post_count_mismatch:${native_post_count:-0}" >&2; exit 1; }
  node_observation="$RUNTIME_DIR/node-observation.json"
  curl --noproxy '*' --silent --show-error --fail --header "Authorization: Bearer $NODE_MANAGEMENT_PASSWORD" "http://127.0.0.1:${NODE_PORT}/v0/management/auth-files" -o "$node_observation"
  grep -Fq "$UPLOAD_EMAIL" "$node_observation" || { echo "node_upload_observation_missing" >&2; exit 1; }
  rm -f -- "$node_observation"
  scan_secret() {
    local value="$1"
    [[ -n "$value" ]] || return 0
    if rg -l -F -- "$value" "$RUNTIME_DIR/logs" "$RUNTIME_DIR/playwright-output" "$RUNTIME_DIR/browser-console.log" "$RUNTIME_DIR/upload-evidence.json" >/dev/null 2>&1; then
      echo "secret_artifact_match" >&2
      return 1
    fi
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
  export ACCEPTANCE_DISABLE_EMAIL="$DISABLE_EMAIL"
  export ACCEPTANCE_DISABLE_EVIDENCE_FILE="$RUNTIME_DIR/disable-evidence.json"
  export CONTROL_E2E_PLAYWRIGHT_OUTPUT_DIR="$RUNTIME_DIR/playwright-output"
  export CONTROL_E2E_BASE_URL="$ACCEPTANCE_BASE_URL"
  if ! CONTROL_E2E_BROWSER_CHANNEL=chromium npm --prefix "$CONTROL_DIR/web" run test:e2e -- account-operations-disable.spec.ts; then
    compose logs --no-color control node node-counter >&2 || true
    exit 1
  fi
  mkdir -p "$RUNTIME_DIR/logs"
  compose logs --no-color control node node-counter > "$RUNTIME_DIR/logs/compose.log"
  command_id="$(sed -n 's/.*"command_id":"\([0-9a-f-]*\)".*/\1/p' "$RUNTIME_DIR/disable-evidence.json")"
  [[ "$command_id" =~ ^[0-9a-f-]{36}$ ]] || { echo "disable_evidence_missing_command_id" >&2; exit 1; }
  psql_count() { compose exec -T postgres psql --username relay_control_migrator --dbname relay_station_control --tuples-only --no-align --command "$1" | tr -d '\r\n[:space:]'; }
  [[ "$(psql_count "SELECT count(*) FROM account_admin_operations WHERE command_id='$command_id' AND operation_kind='disable' AND execution_state='remote_applied'")" == 1 ]] || { echo "disable_operation_mismatch" >&2; exit 1; }
  [[ "$(psql_count "SELECT count(*) FROM account_admin_command_receipts WHERE command_id='$command_id'")" == 1 ]] || { echo "disable_receipt_mismatch" >&2; exit 1; }
  [[ "$(psql_count "SELECT count(*) FROM audit_logs WHERE category='account_admin' AND details->>'command_id'='$command_id'")" == 1 ]] || { echo "disable_audit_mismatch" >&2; exit 1; }
  native_patch_count="$(grep -Ec 'PATCH /v0/management/auth-files/status [0-9]{3}$' "$RUNTIME_DIR/logs/compose.log" || true)"
  [[ "$native_patch_count" == 1 ]] || { echo "disable_native_patch_count_mismatch:${native_patch_count:-0}" >&2; exit 1; }
  scan_disable_secret() {
    local value="$1"
    [[ -n "$value" ]] || return 0
    if rg -l -F -- "$value" "$RUNTIME_DIR/logs" "$RUNTIME_DIR/playwright-output" "$RUNTIME_DIR/browser-console.log" "$RUNTIME_DIR/disable-evidence.json" >/dev/null 2>&1; then
      echo "disable_secret_artifact_match" >&2
      return 1
    fi
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
  if ! CONTROL_E2E_BROWSER_CHANNEL=chromium npm --prefix "$CONTROL_DIR/web" run test:e2e -- account-operations-replace.spec.ts; then
    compose logs --no-color control node node-counter >&2 || true
    exit 1
  fi
  if [[ "$MODE" == "replace-discovery" ]]; then
    discovery_count() {
      compose exec -T postgres psql --username relay_control_migrator --dbname relay_station_control --tuples-only --no-align --command "$1" | tr -d '\r\n[:space:]'
    }
    [[ "$(discovery_count "SELECT count(*) FROM account_inventory_poll_runs WHERE instance_id='00000000-0000-4000-8000-000000000047' AND status='finalized'")" -ge 1 ]] || { echo "replace_discovery_poll_run_missing" >&2; exit 1; }
    [[ "$(discovery_count "SELECT count(*) FROM account_inventory_snapshot_items WHERE instance_id='00000000-0000-4000-8000-000000000047' AND provider='antigravity' AND normalized_email='$REPLACE_EMAIL'")" -ge 1 ]] || { echo "replace_discovery_snapshot_missing" >&2; exit 1; }
    [[ "$(discovery_count "SELECT count(*) FROM account_inventory WHERE instance_id='00000000-0000-4000-8000-000000000047' AND provider='antigravity' AND normalized_email='$REPLACE_EMAIL' AND lifecycle='present'")" == 1 ]] || { echo "replace_discovery_inventory_missing" >&2; exit 1; }
    echo "REPLACE_DISCOVERY=PASS"
    echo "REPLACE_DISCOVERY_POLL_RUN=PASS"
    echo "REPLACE_DISCOVERY_SNAPSHOT=PASS"
    echo "REPLACE_DISCOVERY_INVENTORY_DB=PASS"
    echo "REPLACE_HTTP=NOT_RUN"
    echo "REPLACE_NATIVE_POSTS=0"
    exit 0
  fi
  compose logs --no-color control node node-counter > "$RUNTIME_DIR/logs/compose.log"
  command_id="$(sed -n 's/.*"command_id":"\([0-9a-f-]*\)".*/\1/p' "$RUNTIME_DIR/replace-evidence.json")"
  [[ "$command_id" =~ ^[0-9a-f-]{36}$ ]] || { echo "replace_evidence_missing_command_id" >&2; exit 1; }
  psql_count() { compose exec -T postgres psql --username relay_control_migrator --dbname relay_station_control --tuples-only --no-align --command "$1" | tr -d '\r\n[:space:]'; }
  [[ "$(psql_count "SELECT count(*) FROM admin_command_registry WHERE command_id='$command_id' AND command_kind='account.replace_existing'")" == 1 ]] || { echo "replace_registry_mismatch" >&2; exit 1; }
  [[ "$(psql_count "SELECT count(*) FROM account_admin_operations WHERE command_id='$command_id' AND operation_kind='replace_existing' AND account_key='antigravity:$REPLACE_EMAIL' AND execution_state='remote_applied'")" == 1 ]] || { echo "replace_operation_mismatch" >&2; exit 1; }
  [[ "$(psql_count "SELECT count(*) FROM account_admin_command_receipts WHERE command_id='$command_id'")" == 1 ]] || { echo "replace_receipt_mismatch" >&2; exit 1; }
  [[ "$(psql_count "SELECT count(*) FROM audit_logs WHERE category='account_admin' AND details->>'command_id'='$command_id'")" == 1 ]] || { echo "replace_audit_mismatch" >&2; exit 1; }
  native_post_count="$(grep -Ec 'POST /v0/management/auth-files [0-9]{3}$' "$RUNTIME_DIR/logs/compose.log" || true)"
  [[ "$native_post_count" == 1 ]] || { echo "replace_native_post_count_mismatch:${native_post_count:-0}" >&2; exit 1; }
  scan_replace_secret() {
    local category="$1" value="$2"
    [[ -n "$value" ]] || return 0
    if rg -l -F -- "$value" \
      "$RUNTIME_DIR/logs" "$RUNTIME_DIR/node/logs" "$RUNTIME_DIR/playwright-output" \
      "$RUNTIME_DIR/browser-console.log" "$RUNTIME_DIR/acceptance-output.log" \
      "$RUNTIME_DIR/replace-evidence.json" >/dev/null 2>&1; then
      echo "replace_secret_artifact_match category=$category location=<redacted>" >&2
      return 1
    fi
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
    local category="$1" secret_file="$2"
    python3 - "$category" "$secret_file" \
      "$RUNTIME_DIR/logs" "$RUNTIME_DIR/node/logs" "$RUNTIME_DIR/playwright-output" \
      "$RUNTIME_DIR/browser-console.log" "$RUNTIME_DIR/acceptance-output.log" \
      "$RUNTIME_DIR/replace-evidence.json" <<'PY'
import pathlib
import sys

category, secret_file, *targets = sys.argv[1:]
needle = pathlib.Path(secret_file).read_bytes()
matches = 0
for target in targets:
    path = pathlib.Path(target)
    paths = path.rglob("*") if path.is_dir() else (path,)
    for candidate in paths:
        if candidate.is_file():
            try:
                matches += candidate.read_bytes().count(needle)
            except OSError:
                pass
if matches:
    print(f"replace_secret_artifact_match category={category} location=<redacted>", file=sys.stderr)
    raise SystemExit(1)
PY
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
  export ACCEPTANCE_REMOVE_EMAIL="$REMOVE_EMAIL"
  export ACCEPTANCE_NODE_PORT="$NODE_PORT"
  export ACCEPTANCE_NODE_MANAGEMENT_PASSWORD="$NODE_MANAGEMENT_PASSWORD"
  export ACCEPTANCE_REMOVE_EVIDENCE_FILE="$RUNTIME_DIR/remove-evidence.json"
  export ACCEPTANCE_BROWSER_CONSOLE_FILE="$RUNTIME_DIR/browser-console.log"
  export CONTROL_E2E_PLAYWRIGHT_OUTPUT_DIR="$RUNTIME_DIR/playwright-output"
  export CONTROL_E2E_BASE_URL="$ACCEPTANCE_BASE_URL"
  mkdir -p "$RUNTIME_DIR/logs"
  if ! CONTROL_E2E_BROWSER_CHANNEL=chromium npm --prefix "$CONTROL_DIR/web" run test:e2e -- account-operations-remove.spec.ts; then
    compose logs --no-color control node node-counter >&2 || true
    exit 1
  fi
  compose logs --no-color control node node-counter > "$RUNTIME_DIR/logs/compose.log"
  command_id="$(sed -n 's/.*"command_id":"\([0-9a-f-]*\)".*/\1/p' "$RUNTIME_DIR/remove-evidence.json")"
  [[ "$command_id" =~ ^[0-9a-f-]{36}$ ]] || { echo "remove_evidence_missing_command_id" >&2; exit 1; }
  psql_count() { compose exec -T postgres psql --username relay_control_migrator --dbname relay_station_control --tuples-only --no-align --command "$1" | tr -d '\r\n[:space:]'; }
  [[ "$(psql_count "SELECT count(*) FROM account_admin_operations WHERE command_id='$command_id' AND operation_kind='remove' AND account_key='antigravity:$REMOVE_EMAIL' AND execution_state='remote_applied'")" == 1 ]] || { echo "remove_operation_mismatch" >&2; exit 1; }
  native_delete_count="$(grep -Ec 'DELETE /v0/management/auth-files [0-9]{3}$' "$RUNTIME_DIR/logs/compose.log" || true)"
  [[ "$native_delete_count" == 1 ]] || { echo "remove_native_delete_count_mismatch:${native_delete_count:-0}" >&2; exit 1; }
  scan_remove_secret() {
    local category="$1" value="$2"
    [[ -n "$value" ]] || return 0
    if rg -l -F -- "$value" "$RUNTIME_DIR/logs" "$RUNTIME_DIR/node/logs" "$RUNTIME_DIR/playwright-output" "$RUNTIME_DIR/browser-console.log" "$RUNTIME_DIR/acceptance-output.log" "$RUNTIME_DIR/remove-evidence.json" >/dev/null 2>&1; then
      echo "remove_secret_artifact_match category=$category location=<redacted>" >&2
      return 1
    fi
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
  export ACCEPTANCE_ENABLE_EMAIL="$ENABLE_EMAIL"
  export ACCEPTANCE_NODE_PORT="$NODE_PORT"
  export ACCEPTANCE_NODE_MANAGEMENT_PASSWORD="$NODE_MANAGEMENT_PASSWORD"
  export ACCEPTANCE_ENABLE_FIXTURE_EVIDENCE_FILE="$RUNTIME_DIR/enable-fixture-evidence.json"
  export ACCEPTANCE_BROWSER_CONSOLE_FILE="$RUNTIME_DIR/browser-console.log"
  export CONTROL_E2E_PLAYWRIGHT_OUTPUT_DIR="$RUNTIME_DIR/playwright-output"
  export CONTROL_E2E_BASE_URL="$ACCEPTANCE_BASE_URL"
  mkdir -p "$RUNTIME_DIR/logs"
  set +e
  CONTROL_E2E_BROWSER_CHANNEL=chromium npm --prefix "$CONTROL_DIR/web" run test:e2e -- account-operations-enable-fixture.spec.ts 2>&1 | tee "$RUNTIME_DIR/acceptance-output.log"
  fixture_test_status="${PIPESTATUS[0]}"
  set -e
  [[ "$fixture_test_status" == 0 ]] || { compose logs --no-color control node node-counter >&2 || true; exit "$fixture_test_status"; }
  psql_count() { compose exec -T postgres psql --username relay_control_migrator --dbname relay_station_control --tuples-only --no-align --command "$1" | tr -d '\r\n[:space:]'; }
  [[ "$(psql_count "SELECT count(*) FROM account_inventory WHERE instance_id='00000000-0000-4000-8000-000000000047' AND provider='antigravity' AND normalized_email='$ENABLE_EMAIL' AND lifecycle='present' AND basic_status='disabled'")" == 1 ]] || { echo "enable_fixture_inventory_db_mismatch" >&2; exit 1; }
  [[ "$(psql_count "SELECT count(*) FROM account_inventory_snapshot_items WHERE instance_id='00000000-0000-4000-8000-000000000047' AND provider='antigravity' AND normalized_email='$ENABLE_EMAIL' AND basic_status='disabled'")" == 1 ]] || { echo "enable_fixture_snapshot_db_mismatch" >&2; exit 1; }
  compose logs --no-color control > "$RUNTIME_DIR/logs/control.log"
  compose logs --no-color node > "$RUNTIME_DIR/logs/node.log"
  compose logs --no-color node-counter > "$RUNTIME_DIR/logs/node-counter.log"
  scan_enable_fixture_secret() {
    local category="$1" value="$2"
    [[ -n "$value" ]] || return 0
    if rg -l -F -- "$value" \
      "$RUNTIME_DIR/logs" \
      "$RUNTIME_DIR/node/logs" \
      "$RUNTIME_DIR/playwright-output" \
      "$RUNTIME_DIR/browser-console.log" \
      "$RUNTIME_DIR/acceptance-output.log" \
      "$RUNTIME_DIR/enable-fixture-evidence.json" >/dev/null 2>&1; then
      echo "enable_fixture_secret_artifact_match category=$category location=<redacted>" >&2
      return 1
    fi
  }
  scan_enable_fixture_secret "credential-marker" "$ENABLE_SECRET_MARKER"
  scan_enable_fixture_secret "access-token" "phase7-disposable-enable-token"
  scan_enable_fixture_secret "node-management-secret" "$NODE_MANAGEMENT_PASSWORD"
  scan_enable_fixture_binary_secret() {
    local category="$1" secret_file="$2"
    if python3 - "$category" "$secret_file" \
      "$RUNTIME_DIR/logs" \
      "$RUNTIME_DIR/node/logs" \
      "$RUNTIME_DIR/playwright-output" \
      "$RUNTIME_DIR/browser-console.log" \
      "$RUNTIME_DIR/acceptance-output.log" \
      "$RUNTIME_DIR/enable-fixture-evidence.json" <<'PY'
import pathlib
import sys

category, secret_file, *targets = sys.argv[1:]
needle = pathlib.Path(secret_file).read_bytes()
matches = 0
for target in targets:
    path = pathlib.Path(target)
    paths = path.rglob("*") if path.is_dir() else (path,)
    for candidate in paths:
        if candidate.is_file():
            try:
                matches += candidate.read_bytes().count(needle)
            except OSError:
                pass
if matches:
    print(f"enable_fixture_secret_artifact_match category={category} location=<redacted>", file=sys.stderr)
    raise SystemExit(1)
PY
    then
      return 0
    fi
    return 1
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
  CONTROL_E2E_BROWSER_CHANNEL=chromium npm --prefix "$CONTROL_DIR/web" run test:e2e -- account-operations-enable-fixture.spec.ts 2>&1 | tee "$RUNTIME_DIR/fixture-output.log"
  fixture_test_status="${PIPESTATUS[0]}"
  set -e
  [[ "$fixture_test_status" == 0 ]] || { compose logs --no-color control node node-counter >&2 || true; exit "$fixture_test_status"; }
  compose logs --no-color control > "$RUNTIME_DIR/logs/control-before-enable.log"
  compose logs --no-color node > "$RUNTIME_DIR/logs/node-before-enable.log"
  compose logs --no-color node-counter > "$RUNTIME_DIR/logs/node-counter-before-enable.log"
  set +e
  CONTROL_E2E_BROWSER_CHANNEL=chromium npm --prefix "$CONTROL_DIR/web" run test:e2e -- account-operations-enable.spec.ts 2>&1 | tee "$RUNTIME_DIR/enable-output.log"
  enable_test_status="${PIPESTATUS[0]}"
  set -e
  [[ "$enable_test_status" == 0 ]] || { compose logs --no-color control node node-counter >&2 || true; exit "$enable_test_status"; }
  compose logs --no-color control > "$RUNTIME_DIR/logs/control.log"
  compose logs --no-color node > "$RUNTIME_DIR/logs/node.log"
  compose logs --no-color node-counter > "$RUNTIME_DIR/logs/node-counter.log"
  command_id="$(sed -n 's/.*"command_id":"\([0-9a-f-]*\)".*/\1/p' "$RUNTIME_DIR/enable-evidence.json")"
  [[ "$command_id" =~ ^[0-9a-f-]{36}$ ]] || { echo "enable_evidence_missing_command_id" >&2; exit 1; }
  psql_count() { compose exec -T postgres psql --username relay_control_migrator --dbname relay_station_control --tuples-only --no-align --command "$1" | tr -d '\r\n[:space:]'; }
  [[ "$(psql_count "SELECT count(*) FROM admin_command_registry WHERE command_id='$command_id' AND command_kind='account.enable'")" == 1 ]] || { echo "enable_registry_mismatch" >&2; exit 1; }
  [[ "$(psql_count "SELECT count(*) FROM account_admin_operations WHERE command_id='$command_id' AND operation_kind='enable' AND account_key='antigravity:$ENABLE_EMAIL' AND execution_state='remote_applied'")" == 1 ]] || { echo "enable_operation_mismatch" >&2; exit 1; }
  [[ "$(psql_count "SELECT count(*) FROM account_admin_command_receipts WHERE command_id='$command_id'")" == 1 ]] || { echo "enable_receipt_mismatch" >&2; exit 1; }
  [[ "$(psql_count "SELECT count(*) FROM audit_logs WHERE category='account_admin' AND details->>'command_id'='$command_id'")" == 1 ]] || { echo "enable_audit_mismatch" >&2; exit 1; }
  before_patch_count="$(grep -Ec 'PATCH /v0/management/auth-files/status [0-9]{3}$' "$RUNTIME_DIR/logs/node-counter-before-enable.log" || true)"
  after_patch_count="$(grep -Ec 'PATCH /v0/management/auth-files/status [0-9]{3}$' "$RUNTIME_DIR/logs/node-counter.log" || true)"
  native_patch_count=$((after_patch_count - before_patch_count))
  [[ "$native_patch_count" == 1 ]] || { echo "enable_native_patch_count_mismatch:${native_patch_count:-0}" >&2; exit 1; }
  curl --noproxy '*' --silent --show-error --fail --header "Authorization: Bearer $NODE_MANAGEMENT_PASSWORD" "http://127.0.0.1:${NODE_PORT}/v0/management/auth-files" -o "$RUNTIME_DIR/enable-node-observation.json"
  rg -Fq '"email":"'"$ENABLE_EMAIL"'"' "$RUNTIME_DIR/enable-node-observation.json" || { echo "enable_node_observation_missing" >&2; exit 1; }
  rg -Fq '"disabled":false' "$RUNTIME_DIR/enable-node-observation.json" || { echo "enable_node_state_mismatch" >&2; exit 1; }
  rm -f -- "$RUNTIME_DIR/enable-node-observation.json"
  scan_enable_secret() {
    local category="$1" value="$2"
    [[ -n "$value" ]] || return 0
    if rg -l -F -- "$value" \
      "$RUNTIME_DIR/logs" "$RUNTIME_DIR/node/logs" \
      "$RUNTIME_DIR/playwright-output" "$RUNTIME_DIR/browser-console.log" \
      "$RUNTIME_DIR/fixture-output.log" "$RUNTIME_DIR/enable-output.log" \
      "$RUNTIME_DIR/enable-fixture-evidence.json" "$RUNTIME_DIR/enable-evidence.json" >/dev/null 2>&1; then
      echo "enable_secret_artifact_match category=$category location=<redacted>" >&2
      return 1
    fi
  }
  scan_enable_secret "credential-marker" "$ENABLE_SECRET_MARKER"
  scan_enable_secret "access-token" "phase7-disposable-enable-token"
  scan_enable_secret "node-management-secret" "$NODE_MANAGEMENT_PASSWORD"
  scan_enable_binary_secret() {
    local category="$1" secret_file="$2"
    python3 - "$category" "$secret_file" \
      "$RUNTIME_DIR/logs" "$RUNTIME_DIR/node/logs" \
      "$RUNTIME_DIR/playwright-output" "$RUNTIME_DIR/browser-console.log" \
      "$RUNTIME_DIR/fixture-output.log" "$RUNTIME_DIR/enable-output.log" \
      "$RUNTIME_DIR/enable-fixture-evidence.json" "$RUNTIME_DIR/enable-evidence.json" <<'PY'
import pathlib
import sys

category, secret_file, *targets = sys.argv[1:]
needle = pathlib.Path(secret_file).read_bytes()
matches = 0
for target in targets:
    path = pathlib.Path(target)
    paths = path.rglob("*") if path.is_dir() else (path,)
    for candidate in paths:
        if candidate.is_file():
            try:
                matches += candidate.read_bytes().count(needle)
            except OSError:
                pass
if matches:
    print(f"enable_secret_artifact_match category={category} location=<redacted>", file=sys.stderr)
    raise SystemExit(1)
PY
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
if [[ "${1:-all}" == "all" ]]; then
  export CONTROL_DATABASE_TEST_URL="$DATABASE_URL"
  export CONTROL_RUNTIME_DATABASE_TEST_URL="postgres://relay_control_app_dev:relay_control_runtime_dev_only@127.0.0.1:${DB_PORT}/relay_station_control?sslmode=disable"
  env -u DINGTALK_WEBHOOK_URL -u DINGTALK_SIGNING_SECRET go test ./internal/store -run '^TestRuntimeAcceptanceLifecycleFixture$' -count=1 -v
  env -u DINGTALK_WEBHOOK_URL -u DINGTALK_SIGNING_SECRET go test ./internal/store -run '^TestRuntimeAcceptanceLifecycleFixture$' -count=1 -v
  echo "LIFECYCLE_REPEATED=PASS"
fi
