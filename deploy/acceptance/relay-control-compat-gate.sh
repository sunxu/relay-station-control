#!/bin/sh
set -eu

CONTROL_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$CONTROL_DIR"

export GO111MODULE=on
export GOPROXY="${GOPROXY:-https://goproxy.cn}"
export TMPDIR="${TMPDIR:-/tmp}"

go test ./internal/compatgate
go test ./cmd/relay-control-compat-gate
go build -o "${TMPDIR%/}/relay-control-compat-gate" ./cmd/relay-control-compat-gate
sh -n deploy/compatibility/relay-control-compat-wrapper.sh
sh -n deploy/compatibility/relay-control-compat-compose-wrapper.sh

test -x deploy/compatibility/relay-control-compat-wrapper.sh
test -x deploy/compatibility/relay-control-compat-compose-wrapper.sh
test -f deploy/compatibility/compose.compatibility.yaml
test -f deploy/compatibility/relay-control-compat.service

printf '%s\n' 'relay-control-compat-gate: repeatable unit/wrapper checks PASS'
