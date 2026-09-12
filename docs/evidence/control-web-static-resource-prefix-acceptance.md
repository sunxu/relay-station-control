# Control Web Static Resource Prefix Acceptance

> Change: `fix-control-web-static-resource-prefix`
> Acceptance date: 2026-09-12
> Baseline: `a93d0fc4ee55e17937d52aea05ac76d9d34869b9`

## Accepted behavior

| Contract | Evidence | Result |
| --- | --- | --- |
| `/assets` authenticated direct navigation | Playwright asserted `assets-page` and “资产注册表”; `management-page` absent | PASS |
| `/assets/` authenticated direct navigation | Same stable UI assertions | PASS |
| `/assets/` authenticated reload | Same stable UI assertions after browser reload | PASS |
| Production static namespace | Built `index.html` references JS/CSS under `/static/` | PASS |
| Asset Registry lazy chunk | Browser loaded `/static/assets/AssetsPage-*` with HTTP 200 | PASS |
| Static miss | `/static/not-found.js` returned HTTP 404 without SPA root | PASS |
| API precedence | `/api/healthz` returned API response without SPA root | PASS |
| Existing SPA deep link | Embedded-handler regression returned SPA shell for `/jobs` | PASS |

## Validation

- `go test ./internal/webui`: PASS.
- Targeted Vitest: 2 files / 5 tests PASS.
- Full Vitest: 28 files / 186 tests PASS.
- TypeScript typecheck: PASS.
- Production frontend build: PASS; main JS/CSS and lazy chunks use `/static/`.
- Authenticated production-image Playwright: 4/4 PASS.
- `make test build`: PASS, including generation, all Go tests, frontend tests/typecheck/build and Control binary build.

The authenticated acceptance used a unique Compose project and fresh PostgreSQL 18 runtime. An initial invocation encountered a pre-existing acceptance PostgreSQL container and stopped during migration; it did not reach browser acceptance. The isolated rerun migrated versions 1–32 and passed all required browser scenarios.

## Rollback

Rollback selects the prior accepted Control image. This change adds no migration or durable data, so rollback does not alter Asset Registry data. The rollback image restores its previous static routing behavior; reapplying the accepted image restores `/static/` routing.

```text
Implementation = COMPLETE
Runtime Acceptance = PASS
Static prerequisite = IMPLEMENTED / ACCEPTED
production code changed = true
```
