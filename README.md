# Relay Station Control

Single-environment management and observability service for Relay Station.

## Development prerequisites

- Go 1.27
- Node.js 24 LTS
- Docker with Compose

## Local validation

```bash
export PATH="/opt/homebrew/opt/node@24/bin:$PATH"
unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
npm --prefix web ci
cd tools
go get -tool github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0
go get -tool github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1
go get -tool github.com/pressly/goose/v3/cmd/goose@v3.27.3
cd ..
go mod tidy
make test
docker compose -f compose.dev.yaml up -d --wait
DATABASE_URL='postgres://relay_control_migrator:relay_control_migrator_dev_only@127.0.0.1:55432/relay_station_control?sslmode=disable' make migrate-up
CONTROL_DATABASE_TEST_URL='postgres://relay_control_migrator:relay_control_migrator_dev_only@127.0.0.1:55432/relay_station_control?sslmode=disable' \
CONTROL_RUNTIME_DATABASE_TEST_URL='postgres://relay_control_app_dev:relay_control_runtime_dev_only@127.0.0.1:55432/relay_station_control?sslmode=disable' \
go test -count=1 -v ./internal/store/...
```

`web/.npmrc` fixes the npm registry to `https://registry.npmmirror.com/` and disables
npm proxy use. The Make targets also clear local proxy variables for every Go command;
set `GOPROXY` separately when an approved remote Go module mirror is required.

The development database uses an in-memory Docker `tmpfs` and contains no production data.
The `relay_control_migrator` database owner is only for Goose and schema tests.
Run the Control process with the restricted `relay_control_app_dev` URL. Production
must pre-provision the fixed `relay_control_runtime` NOLOGIN capability role and grant
it to an environment-specific LOGIN before migration; the product must never receive
the migration-owner credential.

## Durable job foundation

The phase-1 durable job runtime uses PostgreSQL as its only source of truth. Its
Worker and Reconciler start with an empty production executor registry; Redis and
external publishers are not required or enabled. Runtime concurrency and polling
can be tuned with the bounded `CONTROL_JOB_*` variables documented in
[`docs/runbooks/durable-jobs.md`](docs/runbooks/durable-jobs.md).

## Account inventory history compaction

Phase 3 history compaction is currently an in-progress, default-disabled capability.
The current candidate includes the additive Migration 9 schema with six
aggregate/run tables plus a durable retired-day cutoff table, compatibility,
eligible-key and final-daily-rollup planning, fenced compaction and rollup
claim/renew/reconcile/finalize/fail functions, transactional
summarize/bounded-snapshot-delete/complete/fail functions and their actor-null audit
events, four ordered retention functions for poll/children, segment/final rows,
completed rollup runs, and completed compaction runs, deterministic full-field
checksums, strict Store adapters, and bounded planner/compaction-worker/rollup-worker/
reconciler/retention loops. PostgreSQL 18 and Go tests
cover the compaction `pending -> summarized -> deleting -> completed` path, final
account/Provider rollup publication in one transaction, failure recovery,
lease/fencing, bounded unknown-commit replay, delete-count conservation, one shared
total concurrency limit across compaction, rollup, and retention work, `limit=1`
multi-batch retention with persisted poll/Provider/duplicate deletion counts,
current-source `ON DELETE SET NULL` with unchanged current-query output, and a shared
fail-closed stop after an integrity mismatch. Normal planning is limited to the
30-day retained horizon; source-backed or already-started lineage provides a bounded
bootstrap for older pre-upgrade days, while an immutable `(UTC day, instance)`
retired marker atomically closes the day before run evidence is removed, prevents
planner resurrection, and rejects later poll insertion for that retired day.
The Control process now loads and validates the history configuration, performs the
read-only compatibility probe even while disabled, registers the bounded metrics
collector, and starts the dedicated loops only when explicitly enabled. History
failure is isolated from the existing Control services and graceful shutdown drains
the bounded runtime. A history-local metrics read failure preserves the independently
observed enabled/reason signal, omits database-derived history families, and does not
fail the other process collectors or expose the raw database error. The capability remains default-disabled and must not be treated
as production-ready until the remaining crash, capacity, privacy-canary, old-binary,
and rollout gates have passed.

`CONTROL_DATABASE_MAX_CONNS` is an optional Control PostgreSQL pool-size environment
override (`1..100`). When absent, existing pgx URL/default behavior is unchanged.
It exists for explicit capacity control and deterministic pool-exhaustion
acceptance; production values must come from an approved capacity plan.

The fixed 72-hour eligibility, 30-day history retention, and 95% coverage threshold
are not runtime configuration. The contract evidence is recorded in
[`docs/evidence/account-inventory-history-compaction-contract.md`](docs/evidence/account-inventory-history-compaction-contract.md).
Rollout, pause/recovery, failure handling, privacy boundaries, and the current
limitations are documented in
[`docs/runbooks/account-inventory-history-compaction.md`](docs/runbooks/account-inventory-history-compaction.md).
