# Asset Registry Foundation Acceptance Evidence

Date: 2026-08-25

Change: `add-control-asset-registry-foundation`

Database: PostgreSQL 18.6

This record contains bounded, non-sensitive outcomes only. It intentionally
omits database URLs, authentication material, Secret references, endpoint
values, cookies, request bodies, and generated runtime paths.

## Database and migration

- A fresh PostgreSQL 18 database completed all Goose migrations through
  `00003_asset_registry_foundation.sql`.
- The controlled registrar executed the Gateway/Driver/Node registration,
  provider-policy activation, monitoring activation, and reconciliation
  templates successfully.
- Replaying identical registration input was idempotent; conflicting input
  failed atomically.
- Concurrent provider-policy and monitoring interval writes could not create
  overlapping ranges.
- Monitoring enable/disable reasons were direction-bound; NULL input and
  conflicting replays failed closed, while a successful close retained its
  independent actor, reason, and database-recorded time.
- The runtime role could read only the generated `secret_configured` projection
  and could not read raw Secret references or modify registry data.
- The registrar could execute only the controlled functions and could not read
  authentication-sensitive tables, read raw Secret references, or write asset
  tables directly.
- Protected down migration succeeded for an empty registry and failed closed
  for a populated or concurrently-written registry while preserving the
  environment singleton and restoring the version-2 runtime/registrar ACL.

Verification command family:

```text
go test -race ./internal/store/... -run 'Test(Asset|ProviderPolicy|Monitoring)' -count=1
```

Result: passed.

## Service and HTTP security

- Environment identity validation ran before listener creation and rejected
  missing, malformed, mismatched, or unavailable identity state without
  exposing configured values.
- The database pool established UTC sessions.
- All six asset read endpoints required an active `super_admin` session and
  returned `no-store` responses.
- Anonymous, forged, expired, and revoked sessions were rejected; asset write
  methods were unavailable.
- Node cursors rejected tampering, trailing data, invalid UUIDs, and replay
  under different filters.
- Database interruption returned a bounded retryable response without stale
  data; the next read recovered without restarting Control.
- Endpoint, Secret-reference, and database-error canaries were absent from HTTP
  responses, headers, logs, metrics, audit output, and browser DOM.
- Asset metrics accepted only closed `asset_kind`, `operation`, and `result`
  values.

Verification command families:

```text
go test -race ./... -count=1
go test -race ./internal/api -run TestAssetRegistry -count=1
```

Result: passed.

## Generated contracts and frontend

- OpenAPI, sqlc, and Orval generation completed reproducibly.
- OpenAPI contract tests passed.
- Frontend unit/component suite passed: 6 files, 33 tests.
- TypeScript typecheck and production build passed.
- `/assets` remained an independent lazy chunk (27.06 kB in the acceptance
  build).
- Browser acceptance passed with one same-origin end-to-end scenario in
  22.6 seconds. The browser made no request to registered Gateway or Node
  management endpoints and exposed no Secret reference.

Verification command families:

```text
make generate
make test
make build
```

Result: passed.

## Container and data-plane isolation

- Fresh Linux arm64 HTTPS acceptance completed successfully with a non-root
  runtime, read-only `0400` secret mounts, environment mismatch fail-closed,
  authentication lifecycle, asset reads, and recovery.
- Before, during, and after stopping Control, its PostgreSQL database, and its
  TLS endpoint, the existing phase 0 data plane reported 2 Nodes, 6 accounts,
  and 0 duplicate accounts.
- Control and database recovery completed without changing phase 0 state.
- Acceptance containers and browser test artifacts were removed. Synthetic
  runtime material remains outside the repository for operator-controlled
  disposal.

The acceptance command explicitly removed local proxy variables and supplied
only the approved Go and npm mirrors. Result: passed with exit status 0.

## Scope review

The implementation was checked against the proposal, design, asset-registry
specification, and system design v1.0. It adds no Adapter invocation, account
collection, Worker/Outbox creation, Gateway write path, or request-data-plane
dependency. The UI and API are read-only; registration remains an explicit,
least-privilege operational workflow.
