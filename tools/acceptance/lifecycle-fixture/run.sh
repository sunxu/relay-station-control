#!/usr/bin/env bash
set -euo pipefail
if [[ -n "${DINGTALK_WEBHOOK_URL:-}" || -n "${DINGTALK_SIGNING_SECRET:-}" ]]; then echo "real DingTalk configuration must be absent" >&2; exit 2; fi
if [[ "${RA_ALLOW_NETWORK:-0}" != 0 ]]; then echo "network is forbidden by default" >&2; exit 2; fi
echo "LIFECYCLE_FIXTURE=TEST_ONLY"
echo "RECONCILE_AND_EVALUATE_ONLY=YES"
echo "DIRECT_ENQUEUETX=NO"
echo "Use an isolated CONTROL_DATABASE_TEST_URL with the store_test adapter; evidence is safe IDs/status/counts only."
