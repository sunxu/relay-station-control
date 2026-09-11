#!/usr/bin/env bash
set -euo pipefail
root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
if rg -n --glob 'web/e2e/**' '\.ant-|:nth-child\(|xpath=|/html/|/body/' "$root"; then
  echo "selector guard failed" >&2
  exit 1
fi
echo "selector guard passed"
