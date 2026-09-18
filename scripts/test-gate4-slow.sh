#!/usr/bin/env bash
set -euo pipefail

: "${CONTROL_DATABASE_TEST_URL:?set CONTROL_DATABASE_TEST_URL to the migrator PostgreSQL URL}"
: "${CONTROL_RUNTIME_DATABASE_TEST_URL:?set CONTROL_RUNTIME_DATABASE_TEST_URL to the runtime PostgreSQL URL}"

# Deliberate long-running Directory process acceptance proofs. Keep them out
# of the corrective loop; run them at Gate closeout or unified review.
go test ./cmd/control \
  -run '^(TestGatewayDirectoryRuntimeProcessSameSlotCompetition|TestGatewayDirectoryRuntimeProcessRecovery|TestGatewayDirectoryMainDeploymentHTTP)$' \
  -count=1 -v
