# Account inventory snapshot foundation acceptance evidence

Date: 2026-08-26

Change: `add-control-account-inventory-snapshot-foundation`

Status: change-scoped acceptance complete. The repository-wide PostgreSQL Store run retains four previously accepted asset/auth baseline failures; all snapshot-specific PostgreSQL and repository release gates pass.

Scope: synthetic PostgreSQL 18, fake Drivers and the isolated official CLIProxyAPI image only. No real Relay Node, production database, real account or production credential is permitted.

This record contains only fixed classifications, bounded counts and tool exit status. It omits database connection strings and parameters, poll/policy identifiers, Node endpoints and IPs, Secret references and values, management keys, provider account identities and email addresses, account keys, Node version/commit, request/response material, raw errors and local runtime paths.

## Required evidence matrix

### Identity projection and Provider independence

- Provider/email normalization is deterministic for case, surrounding whitespace, Unicode and length boundaries. Invalid UTF-8, empty identity, unsupported/out-of-scope Provider and non-identity fields do not create an account key.
- Unique active-Provider identities become bounded snapshot candidates. A repeated `(provider, account_key)` becomes one bounded duplicate record, produces no item for that key and marks only that Provider incomplete.
- A complete runtime Provider promotes independently when another Provider is incomplete. A complete zero-account Provider writes zero items and still advances its current pointer.
- Account identity canaries appear only in the expected protected snapshot/duplicate columns. Logs, metrics, fixed errors, non-snapshot database columns, test output and acceptance artifacts do not contain them.

The real CLIProxyAPI Driver-to-Worker projection regression was executed with `httptest`, a synthetic protected-file Secret and injected test DNS/dialing only; it used no Docker, external network or real Node. The full Driver package passed under the race detector. Each case made exactly one fixed auth-files GET:

- One active, one out-of-scope and one unsupported record produced Node `degraded=true` with aggregate counts `1/1`; the active Provider remained identity/snapshot complete and produced one finalize candidate.
- One missing-provider record plus one active-Provider missing-email record produced a global unidentified count of `2`, not a double-counted value. The affected Provider had local missing count `1`; the other Provider retained local `identity_complete=true`. Because a missing provider makes Node identity incomplete, both Provider snapshots correctly remained incomplete with `node_identity_incomplete`. Their two valid candidates were carried only to close database counts and were not eligible for snapshot persistence.

### Atomic PostgreSQL promotion

- One fenced finalize transaction validates the poll lease/token, pinned active Provider closure, candidate/duplicate counts and normalized key relation before writing any terminal evidence.
- The transaction locks the current policy binding. A policy switch first yields only `policy_changed`; finalize first yields one complete old-policy promotion. No mixed-policy Provider state is possible.
- Complete runtime Providers atomically write immutable items, advance a monotonic Provider pointer and mark promotion applied. Incomplete, disk-fallback, transport/contract failure and stale-poll paths preserve the previous pointer with a fixed skipped reason.
- Injected failures at item, duplicate, pointer, promotion and terminal-run stages leave the transaction entirely committed or entirely absent. Old fencing, expired lease and late terminal writes affect no snapshot state.
- Runtime-role direct INSERT/UPDATE/DELETE/TRUNCATE is denied. Protected down refuses non-empty snapshot, duplicate, provider-state or promotion evidence.

The final Migration was replayed down/up on the empty local v6 snapshot evidence set, then the complete snapshot-focused PostgreSQL 18 suite passed. It covers v5 legacy finalized-row upgrade, empty protected down/up, exact candidate/duplicate/aggregate closure, complete and empty Provider promotion, independent multi-Provider outcomes, duplicate and global identity skips, policy change/future activation/concurrency, stale-pointer monotonicity, old fencing, lease expiry rollback, strict JSON/null/type/count bounds, immutable evidence, runtime permissions, current reads and non-empty down refusal. The legacy row retained `promotion_applied=false/reason=NULL`, produced no snapshot state and emitted no fabricated promotion metric.

The negative closure case that previously could claim `1000` identifiable records with only two duplicate occurrences now fails atomically with poll state unchanged and zero Provider, duplicate or snapshot rows. The positive incomplete-Provider case passes one unique candidate plus two duplicate occurrences to finalize, records only the duplicate evidence and persists zero snapshot items/current state.

### Observability and security

- Existing poll metrics remain available. Promotion metrics use only controlled instance, Provider and fixed reason dimensions:

  ```text
  relay_control_account_inventory_provider_promotion_applied{instance_id,provider}
  relay_control_account_inventory_provider_promotion_skipped{instance_id,provider,reason}
  ```

- Pre-Migration finalized Provider rows remain “promotion not evaluated” and do not produce a synthetic zero or skipped reason. Applied/skipped evidence rebuilds from PostgreSQL after restart.
- Promotion logs contain only fixed component/action/result/reason/state plus controlled instance and Provider. Canary-bearing or contradictory records are dropped without formatting the rejected value.
- The artifact scanner covers endpoint, Secret reference/value, email, account key, response body/header, raw error, unknown field and SQL parameter canaries. It rejects symlinks and unsafe sizes and never echoes a match.

The focused security suite was executed under the race detector for Store, runtime projection, poll observability and the canary scanner and completed with exit code `0`. Direct and nested/container formatting of account-bearing runtime DTOs, Store DTOs, generated snapshot rows and generated finalize SQL parameters did not expose the identity canary under ordinary fmt verbs. Driver regression coverage also verified that its synthetic Secret path and value do not enter logs or finalize evidence.

The scanner entrypoint was executed with all ten snapshot canary classes against a protected temporary artifact directory and returned only its fixed success classification; the directory was removed afterward. All acceptance shell scripts passed syntax validation. A repository scan found no snapshot acceptance log, dump, mapping, management-key, runtime directory, symlink, private-key material or local absolute path in the new acceptance/docs surface.

### Capacity, recovery and data-plane isolation

- Fake 1/10/50-Node tests keep Node management concurrency within the configured bound and demonstrate that extended finalize work remains within the existing dispatch-grace and lease margins.
- PostgreSQL outage, connection exhaustion, transaction timeout and restart preserve database-owned recovery. Already observed Node failures and promotion skips do not trigger another request in the same slot.
- Stopping Control, PostgreSQL or the poll service only pauses snapshot promotion. It does not modify or call Gateway/Relay Node model data paths.

The complete `internal/inventorypoll` suite was executed with the race detector after clearing all local proxy variables and completed with exit code `0`. The focused snapshot cases recorded these bounded results:

- Nine fake observations each made exactly one Driver call and one finalize attempt: non-empty runtime, empty runtime, disk fallback, transport failure, contract failure, missing identity, duplicate identity, unsupported/out-of-scope aggregates, and two-Provider independent completeness.
- The 1/10/50-Node cases completed 1/10/50 finalizes with maximum concurrent Driver calls of 1/10/10. They used the approved 15-second worst-case request, 120-second dispatch grace and 30-second lease configuration; the 50-Node last-batch start remained 60 seconds.
- A synthetic unknown finalize outcome reused the same poll and pinned Provider evidence with a new fencing token, made exactly two Driver/finalize attempts and did not make a third attempt.
- During a synthetic PostgreSQL reconciliation outage and after stopping the poll service, an independent data-plane simulator completed 50 of 50 requests while the management Driver call count remained zero.

These pure-Go results do not claim database transaction fault injection, policy-lock races, pointer monotonicity or PostgreSQL restart completion; those are covered separately by the isolated PostgreSQL gate.

The isolated named-volume PostgreSQL recovery gate passed on PostgreSQL major 18 with these bounded results:

- A real finalize transaction was held at the policy-binding lock and PostgreSQL was stopped. Restart showed one persistent non-terminal poll and zero finalized/provider/snapshot/current-state rows; the expired lease reconciled to `retry_wait`, a second fencing token claimed attempt 2, the stale token was rejected and the new finalize atomically produced one Provider result, one item and one current state.
- A 500-millisecond `statement_timeout` while holding the same binding lock returned SQLSTATE `57014` and left one run with zero Provider/item/state rows. Releasing the lock allowed the same fenced poll to finalize once.
- The runtime LOGIN role was limited to one connection and that connection was held. A fresh PostgreSQL connection returned SQLSTATE `53300`; the service startup barrier made four bounded reconcile attempts, invoked the Driver zero times and stopped on context cancellation without busy-looping.
- While PostgreSQL was unavailable, an independent loopback HTTP path completed `50/50` synthetic requests and the management request count remained zero. This demonstrates process/data-path isolation only; it does not claim a real Gateway or Node request.
- A second database stop/start retained two current items, two current states and the latest run/Provider metric for each of two synthetic instances. The running server reported major version 18. The focused snapshot Store integration gate then passed in the same isolated database.

The isolated PostgreSQL capacity gate recorded `0/1/1000`-candidate finalizes in `6/7/106` milliseconds. The largest case retained about `29.9` seconds of a 30-second lease and `119.9` seconds of a 120-second grace window. A forced 300-millisecond binding lock wait completed in 314 milliseconds; a forced 1.2-second wait under a one-second lease returned the fixed lost-lease classification and rolled back all Provider, snapshot and state rows.

### Official image and request-rate boundary

- The official-image acceptance uses the unmodified `eceasy/cli-proxy-api:v7.2.141` image at its existing pinned digest, an internal Docker network and a synthetic empty auth directory.
- The production Driver performs exactly one fixed auth-files read. The serial gate waits 10 seconds after success or failure, including after the final request. Snapshot promotion adds no Node operation.
- The official empty-directory response is classified as disk fallback and does not promote. Runtime/policy/duplicate/pointer cases use fake fixtures and isolated PostgreSQL rather than another management request.
- Real-Node mode remains fail closed with `request_count=0`.

The official-image container gate was executed exactly once on 2026-08-26 after clearing all local proxy variables. It completed with exit code `0` and produced only this bounded classification:

```text
operation=account_inventory result=degraded reason=none mode=disk_fallback account_count=0 request_count=1 request_wait_seconds=10
snapshot_operation=auth_files_readonly image_version=v7.2.141 request_count=1 request_wait_seconds=10
```

The pinned-digest check passed before container start. The request count remained one, the final 10-second cooldown completed, and real-Node mode was not invoked. Post-run read-only checks found no matching temporary container, Docker network or runtime directory. This records only the official disk-fallback path; it does not mark the fake runtime, PostgreSQL, capacity or repository-wide gates below as complete.

## Verification commands

Every Go/npm/Docker/make command clears uppercase and lowercase HTTP/HTTPS/ALL proxy variables. npm uses `https://registry.npmmirror.com`.

The no-proxy static acceptance runner completed with exit code `0` under the race detector for `cmd/control`, the CLIProxyAPI Driver, `internal/inventorypoll`, `internal/pollobservability`, `internal/store`, the canary scanner and the fixed poll smoke gate. `make generate`, `make test`, `make build`, repository-wide Go race tests, Go vet, both strict OpenSpec validations and diff checks also completed successfully. The environment-gated snapshot PostgreSQL suite passed separately. The full Store/PostgreSQL command was executed and reproduced only the four known pre-existing asset/auth baseline failures; it introduced no snapshot failure.

```sh
make generate
make test
make build
go test ./...
go test -race ./...
go vet ./...

deploy/acceptance/account-inventory-snapshot-run.sh static
deploy/acceptance/account-inventory-snapshot-run.sh scan
deploy/acceptance/account-inventory-snapshot-run.sh container
deploy/acceptance/account-inventory-snapshot-run.sh postgres

npx --yes @fission-ai/openspec@latest validate \
  add-control-account-inventory-snapshot-foundation --strict
npx --yes @fission-ai/openspec@latest validate --all --strict
git diff --check
```

Final evidence records only exit status, bounded aggregate cases, one official-image request and its 10-second cooldown. It must not include raw test failures, connection strings, SQL parameters, identifiers, snapshot rows or canary values.

## Explicit limits

- This foundation stores immutable Provider snapshots and current source pointers only. It does not derive account lifecycle, consecutive missing, stale, coverage, rollups, retention, alerts, product API/UI or manual promotion.
- It does not change Driver HTTP contracts, Gateway or Relay Node, and it does not add a management request.
- Application rollback stops polling and retains the additive forward schema and committed evidence. Production down is not an application rollback mechanism.
