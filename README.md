# Relay Station Control

Single-environment management and observability service for Relay Station.

## Development prerequisites

- Go 1.27
- Node.js 24 LTS
- Docker with Compose

## Local validation

```bash
export PATH="/opt/homebrew/opt/node@24/bin:$PATH"
npm --prefix web install
cd tools
go get -tool github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0
go get -tool github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1
go get -tool github.com/pressly/goose/v3/cmd/goose@v3.27.3
cd ..
go mod tidy
make test
docker compose -f compose.dev.yaml up -d --wait
DATABASE_URL='postgres://relay_station:relay_station_dev_only@127.0.0.1:55432/relay_station_control?sslmode=disable' make migrate-up
```

The development database uses an in-memory Docker `tmpfs` and contains no production data.
