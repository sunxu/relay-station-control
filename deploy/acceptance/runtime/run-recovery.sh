#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
CONTROL_DIR="$(cd -- "$ROOT/../../.." && pwd)"
BASE_SHA="${EXPECTED_SHA:-$(git -C "$CONTROL_DIR" rev-parse HEAD)}"
SHORT_SHA="${BASE_SHA:0:12}"
IMAGE="${ACCEPTANCE_RECOVERY_IMAGE:-relay-station/control:recovery-${SHORT_SHA}}"
RUNTIME_ROOT="${ACCEPTANCE_RECOVERY_RUNTIME_ROOT:-}"
SOURCE_DIR="$CONTROL_DIR"
BUILDX_STATE=""
PROJECT=""
DB_PORT=""

[[ "$BASE_SHA" =~ ^[0-9a-f]{40}$ ]] || { echo "expected candidate SHA must be 40 hex characters" >&2; exit 2; }
[[ -z "${DINGTALK_WEBHOOK_URL:-}" && -z "${DINGTALK_SIGNING_SECRET:-}" ]] || {
  echo "real DingTalk configuration must be absent" >&2
  exit 2
}

cleanup_project() {
  [[ -n "$PROJECT" ]] || return 0
  CONTROL_E2E_PROJECT="$PROJECT" CONTROL_E2E_DB_PORT="$DB_PORT" CONTROL_E2E_RUNTIME_DIR="${RUNTIME_ROOT:-/private/tmp}" \
    docker compose -p "$PROJECT" -f "$CONTROL_DIR/deploy/acceptance/compose.yaml" down --volumes --remove-orphans >/dev/null 2>&1 || true
}

cleanup() {
  cleanup_project
  [[ -n "$RUNTIME_ROOT" ]] && rm -rf -- "$RUNTIME_ROOT"
  [[ -n "$BUILDX_STATE" ]] && rm -rf -- "$BUILDX_STATE"
}
trap cleanup EXIT INT TERM

[[ "$(git -C "$CONTROL_DIR" rev-parse HEAD)" == "$BASE_SHA" ]] || { echo "candidate_image_mismatch" >&2; exit 1; }
[[ -z "$(git -C "$CONTROL_DIR" status --porcelain)" ]] || {
  echo "recovery_validation_requires_clean_worktree" >&2
  exit 1
}
TEMP_COMMIT="$BASE_SHA"

BUILDX_STATE="$(mktemp -d /private/tmp/relay-recovery-buildx.XXXXXX)"
EXPECTED_SHA="$TEMP_COMMIT" IMAGE="$IMAGE" ALLOW_DIRTY=0 \
  BUILDX_CONFIG_DIR="$BUILDX_STATE" \
  "$SOURCE_DIR/deploy/acceptance/runtime/build-image.sh" || BUILD_STATUS=$?
BUILD_STATUS="${BUILD_STATUS:-0}"
REVISION="$(docker image inspect "$IMAGE" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' 2>/dev/null || true)"
PLATFORM="$(docker image inspect "$IMAGE" --format '{{.Os}}/{{.Architecture}}' 2>/dev/null || true)"
[[ "$BUILD_STATUS" == 0 || ( "$BUILD_STATUS" == 1 && "$REVISION" == "$TEMP_COMMIT" && "$PLATFORM" == "linux/arm64" ) ]] || {
  echo "candidate_image_mismatch" >&2
  exit 1
}
[[ "$REVISION" == "$TEMP_COMMIT" && "$PLATFORM" == "linux/arm64" ]] || { echo "candidate_image_mismatch" >&2; exit 1; }
rm -rf -- "$BUILDX_STATE"
BUILDX_STATE=""

for run in 1 2; do
  PROJECT="relay-recovery-${SHORT_SHA}-${run}-$$"
  DB_PORT=$((20000 + (RANDOM % 1000)))
  RUNTIME_ROOT="$(mktemp -d /private/tmp/relay-recovery-runtime.XXXXXX)"
  chmod 700 "$RUNTIME_ROOT"
  export CONTROL_E2E_PROJECT="$PROJECT" CONTROL_E2E_DB_PORT="$DB_PORT" CONTROL_E2E_RUNTIME_DIR="$RUNTIME_ROOT"
  docker compose -p "$PROJECT" -f "$SOURCE_DIR/deploy/acceptance/compose.yaml" up -d --wait postgres
  OWNER_URL="postgres://relay_control_migrator:relay_control_migrator_dev_only@127.0.0.1:${DB_PORT}/relay_station_control?sslmode=disable"
  RUNTIME_URL="postgres://relay_control_app_dev:relay_control_runtime_dev_only@127.0.0.1:${DB_PORT}/relay_station_control?sslmode=disable"
  DATABASE_URL="$OWNER_URL" make --silent -C "$SOURCE_DIR" migrate-up
  CONTROL_DATABASE_TEST_URL="$OWNER_URL" CONTROL_RUNTIME_DATABASE_TEST_URL="$RUNTIME_URL" \
    DINGTALK_WEBHOOK_URL= DINGTALK_SIGNING_SECRET= \
    go test -C "$SOURCE_DIR" ./cmd/control -run '^TestRuntimeRecovery(PendingRestart|RetryWaitRestart|RunningLeaseCrash)$' -count=1 -v
  docker compose -p "$PROJECT" -f "$SOURCE_DIR/deploy/acceptance/compose.yaml" down --volumes --remove-orphans >/dev/null
  PROJECT=""
  rm -rf -- "$RUNTIME_ROOT"
  RUNTIME_ROOT=""
  echo "RECOVERY_RUN_${run}=PASS"
done

echo "RECOVERY_TESTS_SKIPPED=0"
echo "CANDIDATE_IMAGE_REVISION=PASS"
echo "RECOVERY_SOURCE_SHA=$BASE_SHA"
echo "RECOVERY_PROCESS_MODE=go-test-child-production-main"
echo "IMAGE_REVISION_GATE=PASS"
echo "REAL_DINGTALK_SENDS=0"
