NODE24_BIN ?= /opt/homebrew/opt/node@24/bin
PATH := $(NODE24_BIN):$(PATH)
NO_LOCAL_PROXY := env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy

.PHONY: generate test build migrate-up migrate-down

generate:
	cd tools && $(NO_LOCAL_PROXY) go tool oapi-codegen -config ../api/oapi-codegen.yaml ../api/openapi.yaml
	cd tools && $(NO_LOCAL_PROXY) go tool sqlc generate -f ../sqlc.yaml
	cd web && $(NO_LOCAL_PROXY) npm_config_registry=https://registry.npmmirror.com npm run generate:api

test: generate
	cd tools && $(NO_LOCAL_PROXY) go test ./...
	$(NO_LOCAL_PROXY) go test ./...
	cd web && $(NO_LOCAL_PROXY) npm_config_registry=https://registry.npmmirror.com npm test
	cd web && $(NO_LOCAL_PROXY) npm_config_registry=https://registry.npmmirror.com npm run typecheck

build: generate
	cd web && $(NO_LOCAL_PROXY) npm_config_registry=https://registry.npmmirror.com npm run build
	$(NO_LOCAL_PROXY) go build -o bin/control ./cmd/control

migrate-up:
	cd tools && $(NO_LOCAL_PROXY) go tool goose -dir ../migrations postgres "$(DATABASE_URL)" up

migrate-down:
	cd tools && $(NO_LOCAL_PROXY) go tool goose -dir ../migrations postgres "$(DATABASE_URL)" down
