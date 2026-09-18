# Local test layers

Use the fast or Gate 4 focused command during implementation. Both require the
PostgreSQL test URLs shown below only when they run integration tests:

```bash
export CONTROL_DATABASE_TEST_URL='postgres://relay_control_migrator:relay_control_migrator_dev_only@127.0.0.1:55434/relay_station_control?sslmode=disable'
export CONTROL_RUNTIME_DATABASE_TEST_URL='postgres://relay_control_app_dev:relay_control_runtime_dev_only@127.0.0.1:55434/relay_station_control?sslmode=disable'
```

Fast feedback:

```bash
./scripts/test-store-fast.sh
```

Gate 4 owning PostgreSQL and runtime proof:

```bash
./scripts/test-gate4-focused.sh
```

The deliberate long-running Directory process acceptance proofs are separate:

```bash
./scripts/test-gate4-slow.sh
```

They remain required at Gate closeout or unified review, but are not part of
the corrective loop because these process acceptance paths include real slot,
lease, and recovery timing.

The complete store package remains a closeout or unified-review check. It
creates many isolated databases and applies the complete migration chain per
fixture, so it is intentionally not part of the corrective loop:

```bash
go test ./internal/store -count=1
```

Most latest-schema store integration fixtures clone one immutable, per-process
pre-migrated template database. Migration-owning tests that pass migration
arguments keep using a raw empty database and execute the migration chain.
The template contains schema and grants only; environment identity,
credential commitment, sealed credentials, and business rows are created by
each clone's test.

Slow acceptance, performance, history, and concurrency coverage remains in the
full suite and is not removed from the supported regression set.
