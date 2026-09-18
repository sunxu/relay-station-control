#!/usr/bin/env bash
set -euo pipefail

: "${CONTROL_DATABASE_TEST_URL:?set CONTROL_DATABASE_TEST_URL to the migrator PostgreSQL URL}"
: "${CONTROL_RUNTIME_DATABASE_TEST_URL:?set CONTROL_RUNTIME_DATABASE_TEST_URL to the runtime PostgreSQL URL}"

go test ./internal/environment \
  -run '^TestVerifyDatabaseIdentityAndRecovery$' -count=1 -v

go test ./internal/store \
  -run '^(TestStage0Migration51|TestGatewayDirectoryCoordinatorUsesStage0FencedCredential|TestGatewayDirectoryCoordinatorNoWorkAndShapeValidation|TestGatewayDirectoryRuntimeUnconfiguredNoWork|TestGatewayDirectoryLifecycleFenceAcceptancePG18|TestAccountInventoryReadonlyQueryStoreAndPermissionMatrix)$' \
  -count=1 -v

go test ./cmd/control \
  -run '^TestNodeDriverRuntimeEnabledConstructsWithoutSecretOrNetworkAccess$' \
  -count=1 -v

go test ./internal/drivers/cliproxyapi \
  -run '^(TestDriverProbeAndInventoryProjectFixedContract|TestDriverToWorkerPreservesProviderLocalCompletenessAndGlobalCounts|TestUsageQueueUsesStage0AssetCredentialResolver)$' \
  -count=1 -v
