# Durable job foundation acceptance evidence

Date: 2026-08-26

Change: `add-control-durable-job-foundation`
Scope: synthetic PostgreSQL 18 and local container acceptance only

Validated toolchain: Go `1.27.0`, Node.js `24.19.0`, npm `11.19.0` and PostgreSQL `18.6`.

This file records bounded, redacted results rather than raw output. It excludes database connection strings, host addresses, administrator and asset identifiers, cookies, credentials, key material, task payloads, idempotency keys, lease tokens, Outbox envelopes, error details, local runtime locations and browser artifacts.

## Verified results

### PostgreSQL model and recovery

- A fresh PostgreSQL 18 database accepted the forward migration and the empty protected down path. Protected down failed closed when any durable-job catalog, task, event or Outbox evidence remained.
- The closed database matrix rejected `65` invalid combinations across job kinds, jobs, events and Outbox. Six critical `EXPLAIN` plans used their bounded indexes, while `24` jobs claimed by `8` concurrent workers had no duplicate ownership.
- Schema and store race tests passed for strict payload validation, the 64 KiB bound, deterministic hash verification, global idempotency conflict detection, atomic task/event/Outbox commit and runtime-role minimum privileges.
- Concurrent `SKIP LOCKED` claim, cancellation, lease renewal and result submission tests passed. Replaced, stale and expired fencing tokens could not commit state or terminal results.
- Worker and Reconciler crash/recovery cases passed for pre-claim, pre-execute, post-effect/pre-writeback, verifying, rolling-back and post-terminal boundaries. An unknown effect was verified or exhausted to fixed failure; it was never blindly re-executed.
- PostgreSQL outage tests returned fixed unavailable results without busy-looping or fabricated success. Recovery resumed from persisted database evidence and retained attempt/reconciliation budgets.
- Disabled Publisher and notification loss, duplication, reordering, timeout, stale publisher fencing and post-publish/pre-writeback crash tests passed. PostgreSQL polling remained authoritative and the default Outbox result was `suppressed/publisher_disabled`.

### Read-only API and security surfaces

- The generated contract and strict response-schema tests passed for `GET /api/jobs` and `GET /api/jobs/{job_id}`. List ordering and signed cursor binding use descending `(created_at, job_id)` with combined kind/status/UTC time filters and a maximum page size of `200`.
- PostgreSQL-backed HTTP race tests passed for valid administrators, forged/revoked/expired sessions, malformed UUID/cursor/filter input, detail `404`, database `503`, explicit recovery retry and `Cache-Control: no-store`.
- `POST`, `PUT`, `PATCH` and `DELETE` against both task resources returned method-not-allowed and exposed no task mutation path.
- Canary checks found no payload, payload hash, idempotency key, lease owner/token, Outbox envelope, raw error, database connection material or error summary in API bodies, response headers, structured logs or audit rows.
- Metrics exposed only four aggregate families. The only label is the closed `status` enum; task kind, task/operation/event identifiers, parameters, errors and lease material were absent. Invalid metric and log dimensions failed closed.
- No trace exporter or durable-job span/event payload is enabled in this foundation. The trace-like observability serialization canary test passed and the code path has no trace attribute projection; therefore an external trace sink is not applicable to this change.

### React administration view

- The `/jobs` route remained lazy: initial administration rendering did not load or request the task view until navigation.
- Component tests passed for normal rows, stable job-ID React keys, explicit UTC rendering, fixed kind/status/time filters, empty results, previous/next cursor navigation and page sizes `50`, `100` and `200`.
- Detail rendering included only the public summary and redacted lifecycle event fields. Injected payload/hash/key/lease/error/Outbox canaries did not enter the browser DOM, and no create/retry/cancel/delete control was present.
- A `401` returned control to authentication; bounded `404` and `503` states removed stale data and required an explicit retry. Recovery succeeded without reloading the page.
- The complete Web suite passed `9` files and `41` tests. Type checking and the production build passed, including generated Orval client output.

### Generation, container and data-plane isolation

- OpenAPI Go generation, sqlc generation and Orval generation completed reproducibly. Contract tests, Go race tests and generated-client tests passed.
- Container and Playwright acceptance phases completed with the production non-root process, protected bootstrap/keyring inputs and `no-store` sensitive responses. Browser reporting retained no trace, screenshot, video or secret-bearing report.
- The existing phase-0 data-plane baseline was unchanged before and while the acceptance Control, its PostgreSQL service and TLS edge were stopped: two Nodes, six accounts and zero cross-Node duplicates. The check did not alter Node accounts or fixtures, and Control services were restored afterward.
- The first aggregate acceptance run reached the data-plane precheck with an incorrect phase-0 runtime selection and stopped on its authentication `401`; the container and Playwright phases before it had passed. After selecting the correct existing phase-0 runtime, the standalone data-plane phase exited successfully. No runtime location or authentication material is retained here.
- The synthetic registry made no Gateway, Node or Internet execution call and added no real business task, account collection, asset mutation, history compression or Redis dependency.

### Final verification gates

- `make generate`, `make test` and the sequential `make build` completed with exit code `0` after clearing local proxy variables. Two consecutive generation runs produced identical SHA-256 values for the OpenAPI input, generated Go API, generated sqlc model/query and generated Orval client.
- `go test -race -count=1 ./...` completed with exit code `0`; the dedicated PostgreSQL 18 durable-job and read-only HTTP race selection completed with exit code `0` for both `internal/store` and `internal/api`.
- The Web test, TypeScript typecheck and production build gates completed with exit code `0` (`9` test files, `41` tests). This repository has no frontend lint script; no lint step was silently skipped or invented for this change.
- Strict OpenSpec validation and `git diff --check` completed with exit code `0`. The scoped sensitive-material scan found only an intentional synthetic connection-string canary inside a negative HTTP test; no usable credential, private key, runtime database, browser artifact or acceptance runtime was added.
- Running the complete PostgreSQL-backed `internal/store` suite also reproduced four asset-registry failures: URL percent-escape case, NULL gateway SQLSTATE, provider-policy owner mutability and scheduled-disable replay timestamp equality. An isolated archive of the current Git `HEAD`, which contains neither migration `00004` nor any durable-job code, reproduced the same four assertions. Migration `00004` references only the four durable-job relations, their functions, triggers and privileges, and neither the pre-existing asset migration nor its tests are modified. These are therefore recorded as pre-existing out-of-scope baseline failures rather than attributed to this change.
- All disposable databases created for durable-job and asset-baseline diagnosis were removed after verification; the retained development database contains no synthetic durable-job catalog entry or task fixture.
- A parallel `make test`/`make build` experiment caused both processes to clean the same Orval output concurrently and one build exited on a transient missing generated file. The required gates are intentionally serialized; the immediate sequential `make build` rerun passed. This is test-runner contention, not a product or generation reproducibility failure.

### Scope reconciliation

- Proposal, design and durable-job requirements were checked against system design v1.0 sections 20.3, 21.1 and 24.2 and ADR-0001. PostgreSQL remains the sole task/lease/Outbox truth source; Redis is absent and optional; the deployment remains single-Control and the data plane is independent.
- The production registry is empty. No real job kind, Redis client, external executor, Gateway/Node call, account collection, historical compaction, asset write path, cleanup policy or multi-instance coordination was introduced.
- The only existing capability reused is the authenticated `super_admin` read boundary. The asset-registry schema and behavior are unchanged by this change.

## Reviewable commit sequence

The working tree can be reviewed and committed in the following dependency order without committing generated or runtime debris separately:

1. OpenSpec proposal, design, durable-job spec and initial task contract.
2. Forward migration, sqlc queries/generated store code and PostgreSQL acceptance tests.
3. Registry, payload validation, Worker, Reconciler, Dispatcher, observability and runtime tests.
4. OpenAPI contract, generated Go handler surface, read-only store/HTTP implementation and API security tests.
5. Lazy React task view, generated Orval client, hooks and component tests.
6. Main-process integration, configuration, README, Runbook, acceptance evidence and completed task checklist.

## Reproduction command families

Each command family was run after removing uppercase and lowercase HTTP(S)/ALL proxy variables. npm used the repository-approved mirror. The local PostgreSQL race run used environment-provided synthetic development connection configuration; its value is intentionally omitted. The Runbook separately requires protected PostgreSQL service/password files for production use.

```sh
make generate
make test
make build
go test -race ./... -count=1
go test -race ./internal/store ./internal/jobs ./internal/api ./cmd/control -count=1
```

```sh
cd tools
go test ./...
```

```sh
cd web
npm test
npm run typecheck
npm run build
```

```sh
deploy/acceptance/control-auth-e2e.sh container
deploy/acceptance/control-auth-e2e.sh playwright
deploy/acceptance/control-auth-e2e.sh data-plane
```

```sh
git diff --check
openspec validate add-control-durable-job-foundation --strict
```

The acceptance runner used a private location outside the repository and removed browser output after each run. This evidence intentionally records neither that location nor any generated material.

## Explicit limits

- The production Executor registry remains empty. Only a compile/test-only, no-network synthetic executor exercised state transitions and crash recovery.
- Redis wake publishing, real Gateway/Node operations, user-triggered task mutation and multi-instance production rollout remain outside this change.
- No production database or production task payload was used. Results demonstrate the foundation and its fail-closed boundaries, not a real business executor.
