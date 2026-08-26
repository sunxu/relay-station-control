# CLIProxyAPI read-only Driver acceptance evidence

Date: 2026-08-26

Change: `add-control-cliproxyapi-readonly-driver`

This record contains only bounded classifications, aggregate counts and public
version identifiers. It intentionally excludes management endpoints, DNS/IP
results, Secret references and paths, Management Keys, account identities,
project identifiers, request/response headers and bodies, raw errors, database
connection material and local runtime paths.

## Verified implementation boundaries

- The fixed registry contains only `cliproxyapi.auth-files.v1` and the two
  read-only capabilities. Unknown node types, contract versions, missing,
  duplicate or extra capabilities fail before Driver, Secret, DNS or network
  work. Database strings cannot dynamically load an implementation.
- The production constructor cannot inject a test resolver or dialer, starts no
  goroutine and performs no target Secret read, DNS lookup or connection.
  Control only constructs the dormant registry when explicitly enabled; there
  is no poll scheduler, durable-job Executor or product HTTP caller in this
  change.
- The only expressible requests are `GET /healthz` without credentials and
  `GET /v0/management/auth-files` with `X-Management-Key`. The dedicated client
  has no environment proxy, cookie jar, redirect or automatic retry.
- Endpoint, DNS, every resolved IP, actual remote IP, plain HTTP CIDR and TLS
  hostname/CA validation fail closed. Every connection re-resolves and locks
  dialing to the validated address; forbidden special and metadata addresses
  remain forbidden even under a broad CIDR.
- Mapping, Secret and CA files use component-by-component `openat` traversal
  with `O_NOFOLLOW`. Unsafe owners, parent permissions, intermediate/final
  symlinks, non-regular files and size/permission violations are rejected.
- Health and inventory responses are read under the total timeout with strict
  byte/record limits. JSON duplicate keys, invalid shapes, unknown source modes,
  oversized identities and control characters fail closed. Only the reviewed
  DTO field allowlist survives parsing.
- Logs, metrics, errors and formatting use closed enums and low-cardinality
  fields. Canary tests cover credential, reference, path, endpoint, IP, account,
  discarded-field, raw-body and raw-error values without projecting them.

## Contract and fixture verification

- The sanitized `auth-files/v1` fixture set is pinned to official CLIProxyAPI
  `v7.2.141`, commit `dc3c3b1ec3ed04bb0917e76451eaf98c6842674d`, and image digest
  `sha256:7f598ce64478a8a5f90ed76875e0e9b0e7d77b80e17184b13df18c3d5bdb3def`.
- Tests parse the complete YAML manifest and execute every case, including
  runtime, disk fallback, empty, invalid shape/JSON, identity errors, provider
  scope, Node-local duplicate, cross-Node and unknown-field/source behavior.
- Generated boundary fixtures cover the 5 MiB plus one-byte response and
  1,001-record rejection. Repository fixtures use only synthetic
  `example.invalid` identities; no raw Node response is copied into Control.

## Official image smoke

The original `eceasy/cli-proxy-api:v7.2.141` image was used without modifying
Node code, configuration, protocol or accounts. Two controlled phase-0 Nodes
were queried serially through the production Driver, Secret resolver, target
policy, transport and parser. The fixed order was Node 1 health, Node 1
inventory, Node 2 health, Node 2 inventory. The helper waited `10s` after every
request, including the final request and failure paths, and performed no hidden
retry.

Sanitized result:

```text
node=1 operation=probe result=success reason=none
node=1 operation=account_inventory result=success reason=none mode=runtime
node=2 operation=probe result=success reason=none
node=2 operation=account_inventory result=success reason=none mode=runtime
cliproxyapi_readonly_smoke=success node_count=2 account_count=6 cross_node_duplicate_count=0 request_wait_seconds=10
```

Cross-Node comparison occurred only in memory over normalized
`(provider,email)` identities. The helper emits only aggregate counts and fails
if any cross-Node duplicate is present. It wrote no raw response, header, error
or identity artifact.

## Verification gates

Validated toolchain: Go `1.27.0`, Node.js `24.19.0`, npm `11.19.0`, Docker
client `29.4.0`, OpenSpec CLI `1.10.0` and Git `2.55.0`.

All Go, npm, Docker and make commands removed uppercase and lowercase local
proxy variables. Go used the approved direct mirror fallback and npm used
`https://registry.npmmirror.com`.

- `go test ./... -count=1`: passed.
- `go test -race ./... -count=1`: passed.
- `go vet ./internal/drivers/...`: passed.
- Linux/amd64 cross-compilation of the protected-file and Driver test packages:
  passed.
- `make generate`: passed and produced no generated diff.
- `make test`: passed; Go packages, 9 Web test files / 41 tests and TypeScript
  type checking succeeded.
- `make build`: passed for the Web production bundle and Control binary.
- `deploy/acceptance/control-auth-e2e.sh container`: passed with a linux/arm64,
  non-root Control image, protected `0400` inputs, `no-store` responses and
  bounded configuration failures. Its disposable PostgreSQL, volumes,
  containers and runtime were removed.
- `openspec validate add-control-cliproxyapi-readonly-driver --strict`: passed.
- `openspec validate --specs --strict`: 3 passed, 0 failed.
- `bash -n deploy/acceptance/cliproxyapi-readonly-smoke.sh` and
  `git diff --check`: passed.

The scoped runtime-literal scan found no phase-0 hostname, address, runtime path
or earlier tunnel endpoint in the repository changes. Test caches, smoke
binaries, mappings, lock directories and acceptance containers were removed.

## Scope and zero-side-effect reconciliation

- Proposal, design, Driver spec, system design v1.0, ADR-0001 and the pinned
  phase-0 cases were reconciled. The Driver remains Control management-plane
  code and imports no Gateway/Node data-plane implementation.
- No Migration, sqlc query/model, OpenAPI, React, Store, durable-job, Outbox,
  Redis, Sub2API/Prometheus Adapter, Gateway Connector or Node product file was
  changed. There is no account persistence, poll run, lifecycle advancement,
  alerting or automatic network caller.
- Constructor and failure/recovery tests use Secret/DNS/dial counters to prove
  zero work before an explicit operation. Secret, DNS, certificate and service
  recovery are re-evaluated by the next explicit call without a restart or
  cached authorization decision.
- Driver or Control unavailability can only pause this optional read-only
  observation path. No dependency from the established Gateway/Node data path
  to this registry was introduced.

## Reviewable commit sequence

1. Add the fixed Driver contract, registry, configuration and protected file
   resolver.
2. Add the CLIProxyAPI target policy, secure transport and bounded health/parser
   implementation with sanitized pinned fixtures.
3. Add Driver composition, observability and Control runtime wiring.
4. Add the rate-limited official-image smoke helper and security/acceptance
   tests.
5. Add the Runbook, acceptance evidence and completed OpenSpec task checklist.

## Explicit limits

- This foundation intentionally has no automatic or product-facing caller. A
  future OpenSpec change must define poll slots, leases, persistence and UI.
- The Management Key remains an upstream management credential; deployment
  must retain the isolated management network and exact-GET reverse-proxy
  policy documented in the Runbook.
- Cross-Node duplicate detection in this change is an acceptance-only in-memory
  assertion, not a stored account lifecycle feature.
