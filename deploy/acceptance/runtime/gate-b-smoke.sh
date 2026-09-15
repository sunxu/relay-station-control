#!/usr/bin/env bash
set -euo pipefail

image="${CONTROL_E2E_NODE_IMAGE:-relay-station-node:phase7-gate4}"
node_port="${CONTROL_E2E_NODE_PORT:?set CONTROL_E2E_NODE_PORT}"
node_password="${CONTROL_E2E_NODE_MANAGEMENT_PASSWORD:?set CONTROL_E2E_NODE_MANAGEMENT_PASSWORD}"
expected_digest="sha256:886804e0569c619433d3603772c162ff193ce57da6cb95e6250178966d3c7b3d"
expected_version="7.3.2"
expected_commit="2be99911510c3168199015aad915b8457fc82111"

actual_digest="$(docker image inspect "$image" --format '{{.Id}}')"
[[ "$actual_digest" == "$expected_digest" ]] || { echo "pinned_node_digest_mismatch" >&2; exit 1; }

headers=""
for _ in $(seq 1 90); do
  headers="$(curl --noproxy '*' --silent --show-error --fail --dump-header - --output /dev/null \
    --header "Authorization: Bearer $node_password" "http://127.0.0.1:${node_port}/v0/management/auth-files" || true)"
  version_count="$(printf '%s\n' "$headers" | awk 'tolower($0) ~ /^x-cpa-version:/{n++}END{print n+0}')"
  commit_count="$(printf '%s\n' "$headers" | awk 'tolower($0) ~ /^x-cpa-commit:/{n++}END{print n+0}')"
  if [[ "$version_count" == 1 && "$commit_count" == 1 ]]; then
    break
  fi
  sleep 1
done
[[ "$(printf '%s\n' "$headers" | awk 'tolower($0) ~ /^x-cpa-version:/{n++}END{print n+0}')" == 1 ]] || { echo "version_header_cardinality" >&2; exit 1; }
[[ "$(printf '%s\n' "$headers" | awk 'tolower($0) ~ /^x-cpa-commit:/{n++}END{print n+0}')" == 1 ]] || { echo "commit_header_cardinality" >&2; exit 1; }
printf '%s\n' "$headers" | awk -F': ' 'tolower($1)=="x-cpa-version"{sub(/\r$/,"",$2); print $2}' | grep -Fxq "$expected_version" || { echo "pinned_node_version_mismatch" >&2; exit 1; }
printf '%s\n' "$headers" | awk -F': ' 'tolower($1)=="x-cpa-commit"{sub(/\r$/,"",$2); print $2}' | grep -Fxq "$expected_commit" || { echo "pinned_node_commit_mismatch" >&2; exit 1; }

if curl --noproxy '*' --silent --show-error --fail --output /dev/null \
  --header 'Authorization: Bearer invalid-management-secret' \
  "http://127.0.0.1:${node_port}/v0/management/auth-files"; then
  echo "node_auth_rejection_missing" >&2
  exit 1
fi

echo "GATE_B_NODE_IDENTITY=PASS"
echo "GATE_B_NODE_AUTH=PASS"
