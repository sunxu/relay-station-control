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
