#!/bin/sh
set -eu

: "${CONTROL_COMPAT_GATE:?CONTROL_COMPAT_GATE is required}"
: "${CONTROL_COMPAT_MANIFEST:?CONTROL_COMPAT_MANIFEST is required}"
: "${CONTROL_COMPAT_PUBLIC_KEY:?CONTROL_COMPAT_PUBLIC_KEY is required}"
: "${CONTROL_IMAGE:?CONTROL_IMAGE is required}"
: "${CONTROL_COMPOSE_FILE:?CONTROL_COMPOSE_FILE is required}"
: "${CONTROL_COMPAT_COMPOSE_FILE:?CONTROL_COMPAT_COMPOSE_FILE is required}"

detach=""
if [ "$#" -gt 1 ] || { [ "$#" -eq 1 ] && [ "$1" != "--detach" ]; }; then
  echo "relay-control-compat-compose-wrapper: unsupported_argument" >&2
  exit 78
fi
if [ "$#" -eq 1 ]; then
  detach="--detach"
fi

case "$CONTROL_IMAGE" in
  *@sha256:????????????????????????????????????????????????????????????????)
	selected_repository="${CONTROL_IMAGE%@sha256:*}"
    selected_digest="sha256:${CONTROL_IMAGE##*@sha256:}"
    ;;
  *)
    echo "relay-control-compat-compose-wrapper: immutable_image_required" >&2
    exit 78
    ;;
esac

case "$selected_repository" in
  ""|*@*|*[!A-Za-z0-9._:/-]*)
    echo "relay-control-compat-compose-wrapper: immutable_image_required" >&2
    exit 78
    ;;
esac

case "$selected_digest" in
  sha256:*[!0-9a-f]*|sha256:)
    echo "relay-control-compat-compose-wrapper: immutable_image_required" >&2
    exit 78
    ;;
esac

"$CONTROL_COMPAT_GATE" \
  --manifest "$CONTROL_COMPAT_MANIFEST" \
  --public-key "$CONTROL_COMPAT_PUBLIC_KEY" \
  --database-url-env "${CONTROL_COMPAT_DATABASE_URL_ENV:-DATABASE_URL}" \
  --oci-manifest-digest "$selected_digest" \
  --check-only

# The protected compatibility overlay is always last, so the selected image
# cannot be replaced by a later Compose override after verification.
if [ -n "$detach" ]; then
  exec docker compose -f "$CONTROL_COMPOSE_FILE" -f "$CONTROL_COMPAT_COMPOSE_FILE" up --detach control
fi
exec docker compose -f "$CONTROL_COMPOSE_FILE" -f "$CONTROL_COMPAT_COMPOSE_FILE" up control
