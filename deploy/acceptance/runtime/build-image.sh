#!/usr/bin/env bash
set -euo pipefail

CONTROL_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd)"
EXPECTED_SHA="${EXPECTED_SHA:-}"
IMAGE="${IMAGE:-relay-station/control:acceptance}"
PLATFORM="${PLATFORM:-linux/arm64}"
BUILDX_CONFIG_DIR="${BUILDX_CONFIG_DIR:-}"
[[ "$EXPECTED_SHA" =~ ^[0-9a-f]{40}$ ]] || { echo "EXPECTED_SHA must be a 40-character candidate SHA" >&2; exit 2; }
if [[ -z "$BUILDX_CONFIG_DIR" ]]; then BUILDX_CONFIG_DIR="$(mktemp -d /private/tmp/relay-control-buildx.XXXXXX)"; CLEAN_BUILDX=1; else CLEAN_BUILDX=0; fi
case "$BUILDX_CONFIG_DIR" in "$CONTROL_DIR"/*) echo "BUILDX_CONFIG_DIR must be outside repository" >&2; exit 2;; esac
umask 077; mkdir -p "$BUILDX_CONFIG_DIR"; test -w "$BUILDX_CONFIG_DIR"; export BUILDX_CONFIG="$BUILDX_CONFIG_DIR"
cleanup() { [[ "$CLEAN_BUILDX" == 1 ]] && rm -rf -- "$BUILDX_CONFIG_DIR"; }; trap cleanup EXIT
[[ "$(git -C "$CONTROL_DIR" rev-parse HEAD)" == "$EXPECTED_SHA" ]] || { echo "candidate SHA mismatch" >&2; exit 1; }
if [[ "${ALLOW_DIRTY:-0}" != 1 ]]; then
  [[ -z "$(git -C "$CONTROL_DIR" status --porcelain)" ]] || { echo "worktree must be clean" >&2; exit 1; }
fi
docker buildx build --platform "$PLATFORM" --build-arg "GOPROXY=${GOPROXY:-https://proxy.golang.org,direct}" --label "org.opencontainers.image.revision=$EXPECTED_SHA" --load --tag "$IMAGE" "$CONTROL_DIR"
revision="$(docker image inspect "$IMAGE" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}')"
image_platform="$(docker image inspect "$IMAGE" --format '{{.Os}}/{{.Architecture}}')"
[[ "$revision" == "$EXPECTED_SHA" && "$image_platform" == "$PLATFORM" ]] || { echo "image identity verification failed" >&2; exit 1; }
printf 'IMAGE=%s\nIMAGE_REVISION=%s\nPLATFORM=%s\nBUILDX_ISOLATED=YES\n' "$IMAGE" "$revision" "$image_platform"
