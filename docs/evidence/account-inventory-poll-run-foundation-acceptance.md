# Account inventory poll-run foundation acceptance evidence

Date: 2026-08-26

Change: `add-control-account-inventory-poll-run-foundation`
Scope: synthetic PostgreSQL 18, fake Drivers and an isolated official-image container; no real Relay Node was contacted.

Validated toolchain: Go `1.27.0`, Node.js `26.7.0`, npm `11.19.0` and PostgreSQL `18.6`.

This record contains only bounded aggregate results. It omits database connection strings, poll-run and policy identifiers, Node endpoints and IPs, Secret references and values, management keys, account identities and email addresses, request or response material, raw errors and local runtime paths.

## Verified results

### PostgreSQL scheduling and evidence

- Migration `00005` applied to PostgreSQL 18 and its protected down/up cycle completed successfully while both poll tables were empty. The down path refused non-empty evidence.
- PostgreSQL computed the UTC five-minute slot, enforced one run per Node and slot, pinned the active Provider policy and stored each run's immutable start grace. Repeated scheduling returned the existing run rather than inserting another one.
- The scheduler rejected a slot atomically when the count of eligible monitored Nodes exceeded the configured capacity; it did not gradually exceed the bound over repeated ticks.
- Capability, monitoring activation, policy activation and binding form one fail-closed eligibility boundary. Invalid slots, ambiguous policy state, incomplete Provider evidence and abandoned-run evidence were rejected.
- Runtime-role tests confirmed that direct `INSERT`, `UPDATE`, `DELETE` and `TRUNCATE` are unavailable. Scheduling, claim, reconcile and fenced finalize remain the only mutation surface.
- Claim/finalize tests confirmed short `SKIP LOCKED` ownership, random fencing, database leases and atomic complete-Provider evidence. A stale worker could not finalize after ownership changed.
- Reconciliation classified an expired lease using the grace pinned on that historical run, not a newer Control configuration value.

### Runtime and failure behavior

- The process semaphore is acquired before database claim, so a run does not become `running` while waiting for local HTTP capacity.
- The 50-Node fake-Driver test completed all runs with a maximum concurrency of 10. Configuration tests rejected insufficient dispatch margin, a lease that cannot cover request plus finalize, an unsafe five-minute schedule and unbounded limits.
- Driver context uses the lesser of PostgreSQL's remaining grace and the fixed 15-second request budget. Cancellation, shutdown drain, lost lease and database-unavailable paths remained bounded.
- Transport, HTTP, contract, disk-fallback and identity failures produced closed aggregate evidence. A completed Node failure finalized the slot; only an unknown Control execution could use the bounded second attempt.
- Persisted rows, metrics, structured logs and returned errors were restricted to allowlisted aggregate fields. Canary tests found no account identity, email, endpoint, Secret, header/body or raw-error value.

### Observability and application wiring

- Seven closed Prometheus metric families cover run state, scheduler lag, queue wait, start lag, transport, contract and Provider snapshot completeness. Values rebuild from PostgreSQL evidence after restart.
- Logs use fixed component/action/result/reason/node-type/state/attempt dimensions. Stable instance ID is the only controlled troubleshooting identifier.
- Polling is disabled by default. It starts only when both the poll service and the existing CLIProxyAPI Driver are explicitly enabled and all capacity and timing checks pass before any network call.
- PostgreSQL outage handling pauses Scheduler, Worker and Reconciler with bounded backoff. It does not create an in-memory truth source or affect Gateway/Relay Node data-plane traffic.

### Official image and request-rate boundary

- The container acceptance used the unmodified `eceasy/cli-proxy-api:v7.2.141` image pinned to digest `sha256:7f598ce64478a8a5f90ed76875e0e9b0e7d77b80e17184b13df18c3d5bdb3def`, an internal Docker network and a synthetic empty auth directory.
- The production Driver issued exactly one fixed auth-files read, returned a redacted `disk_fallback` aggregate and waited 10 seconds after that final request. It did not call Probe, a write endpoint, Gateway, a model API or the Internet.
- The global serial gate test covered success, failure and panic paths and verified a 10-second wait after every request, including the last request.
- Real-Node mode remained fail closed and made no management request.

## Final verification gates

The following completed with exit code `0` after clearing uppercase and lowercase HTTP/HTTPS/ALL proxy variables. npm used `https://registry.npmmirror.com`.

```sh
make generate
make test
make build
go test -race -count=1 ./...
go vet ./...
```

The generated OpenAPI, sqlc and Orval outputs were reproducible. The Web suite passed 9 files and 41 tests, TypeScript type checking passed, and the production Web and Go builds passed.

The focused PostgreSQL poll schema/store suite passed against PostgreSQL 18, including migration protection, scheduler idempotency and capacity, claim/finalize/fencing, pinned-policy recovery and runtime-role negative tests.

```sh
npx --yes @fission-ai/openspec@latest validate \
  add-control-account-inventory-poll-run-foundation --strict
npx --yes @fission-ai/openspec@latest validate --all --strict
git diff --check
```

The change and all four main specifications passed strict validation. No runtime directory, database dump, container filesystem, real account data or raw Node response is part of the working tree.

## Reviewable commit sequence

The implementation can be reviewed in these dependency-ordered commits:

1. PostgreSQL migration, sqlc query/generated model, repository and schema/store integration tests.
2. Poll configuration, Scheduler, Worker, Reconciler, projection and runtime tests.
3. Closed metrics/logging implementation and security canary tests.
4. Control process wiring, environment validation and command-level tests.
5. Official-image/static acceptance runners, Runbook, this evidence record and completed OpenSpec task checklist.

## Explicit limits

- This foundation stores only run-level and Provider-level aggregates. Account snapshots, lifecycle derivation, history compression, coverage/promotion, alerting, OpenAPI and administration UI are not implemented here.
- No real Relay Node, real account, production database or production credential was used.
- The official-image acceptance proves the fixed auth-files disk-fallback path. Other transport and contract failures are exercised with fake Drivers and PostgreSQL integration tests.
