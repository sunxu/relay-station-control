NODE24_BIN ?= /opt/homebrew/opt/node@24/bin
PATH := $(NODE24_BIN):$(PATH)

.PHONY: generate test build migrate-up migrate-down

generate:
	cd tools && go tool oapi-codegen -config ../api/oapi-codegen.yaml ../api/openapi.yaml
	cd tools && go tool sqlc generate -f ../sqlc.yaml
	cd web && npm run generate:api

test: generate
	go test ./...
	cd web && npm test
	cd web && npm run typecheck

build: generate
	cd web && npm run build
	go build -o bin/control ./cmd/control

migrate-up:
	cd tools && go tool goose -dir ../migrations postgres "$(DATABASE_URL)" up

migrate-down:
	cd tools && go tool goose -dir ../migrations postgres "$(DATABASE_URL)" down
