# Account inventory lifecycle foundation acceptance evidence

Date: 2026-08-27

Change: `add-control-account-inventory-lifecycle-foundation`

Status: complete and archived. Static, scanner-unit, enhanced PostgreSQL lifecycle, snapshot-only binary rollback, lifecycle-retention data-plane, enhanced official-container, component-input canary and all local release gates passed on the final acceptance patch. GitHub Actions run [33033459871](https://github.com/sunxu/relay-station-control/actions/runs/33033459871) passed all five active-change jobs. The enhanced PostgreSQL run included fake Driver→real Store lifecycle sequences, uncommitted termination, commit-unknown replay, policy-mutation fail-closed behavior, empty-side Provider-set transitions and 1/10/50 Node capacity checks. The archived tree remains subject to the same branch CI gates.

Scope: fake Driver observations, synthetic official CLIProxyAPI fixtures and isolated PostgreSQL 18 only. Real Nodes, production databases, production credentials and production account identities are outside this evidence. The lifecycle runner keeps real-Node mode fail closed with `request_count=0`.

Retained evidence MUST contain only fixed classifications, bounded aggregate counts, durations and tool exit status. It MUST omit connection strings and SQL parameters, poll/policy identifiers, endpoints/IPs, Secret references and values, email/account keys, source version/commit, request/response material, raw errors, database rows/dumps and local runtime paths.

## Required evidence matrix

| Area | Required proof | Request accounting | Status |
|---|---|---:|---|
| Migration baseline | Existing snapshot/provider history remains unchanged and lifecycle starts empty | 0 | passed: focused PostgreSQL |
| First baseline | First qualified promotion creates only actually observed `present` rows; qualified empty snapshot creates none | direct database fixture; 0 network | passed: focused PostgreSQL |
| Consecutive missing | `present→suspected_missing/1→missing/2`, second-miss timestamp stable and count saturated | direct database fixture; 0 network | passed: focused PostgreSQL |
| Recovery | suspected/missing reappearance restores present, preserves first seen and refreshes last seen/source | direct database fixture; 0 network | passed: focused PostgreSQL |
| Skip matrix | transport/contract/disk/identity/duplicate/policy/stale/abandoned evidence changes no lifecycle | no lifecycle-added call or retry | passed: PostgreSQL guardrail matrix |
| Provider scope | active→out-of-scope is atomic; re-add does not restore absent accounts; actual reappearance does | direct policy/promotion fixture; 0 network | passed: focused PostgreSQL |
| Atomicity/fencing | terminated uncommitted lifecycle upsert/missing transactions roll back; old fencing, timeout and commit-unknown replay cannot duplicate or split lifecycle/snapshot/poll | 0 additional calls | passed: PostgreSQL failure/recovery/fencing gates |
| Permissions/read boundary | Runtime role has no arbitrary lifecycle DML/enumeration; internal list is bounded/stable | 0 | passed: focused PostgreSQL |
| Rollback mutation switch | Unset/false policy mutation fails before a transaction; true reaches only the controlled database function; either Provider set may be empty individually | 0 | passed: enhanced PostgreSQL |
| Old binary rollback | Snapshot-only binary runs against forward schema with poll/policy mutation disabled; committed state remains frozen and the restored binary advances only the next qualified poll | management/data-plane/real Node 0; three local product reads | passed: isolated rollback drill |
| Observability | Current totals survive restart; labels/reasons remain closed; no non-durable transition counter | 0 | passed: restart and metrics gates |
| Capacity | 1/10/50 Nodes × 1000 accounts measure lifecycle rows, WAL, lock waiters and transaction/batch duration within lease/dispatch budgets | direct database fixture; 0 network | passed: enhanced PostgreSQL |
| Data-plane isolation | Stopped lifecycle dependencies pause state while synthetic loopback data-plane remains successful | management 0; synthetic data-plane `50/50` | passed: lifecycle data-plane |
| Official synthetic image | Production Driver/Worker request boundary remains one fixed read and 10-second cooldown | 1 synthetic GET | passed: enhanced official-container |
| Canary scan | All 15 classes traverse Driver/Worker/sqlc inputs for success/failure/policy-race/rollback, then remain absent from final logs, metric counts, fixed errors and test artifacts | 1 synthetic management GET; 2 in-process fake Driver calls; 0 real Node | passed: component-input matrix |
| Product boundary | OpenAPI, generated client and React routes remain unchanged; attempted lifecycle enumeration routes and write methods remain unregistered | 0 | passed: generation/frontend plus negative HTTP route test |

## Acceptance classifications to retain

After each successful execution, replace only the matching `pending` status and retain only exact bounded outputs emitted by the runners. Do not infer unreported classifications or paste raw Go, Goose, PostgreSQL, Docker or test logs.

The accepted local static and focused PostgreSQL runs retained:

```text
account_inventory_lifecycle_acceptance=success mode=static real_node_requests=0 gateway_requests=0 data_plane_requests=0 management_writes=0
account_inventory_lifecycle_postgres=success server_major=18 migration_no_backfill=covered baseline=covered consecutive_missing=covered recovery=covered out_of_scope=covered re_add=covered permissions=covered request_count=0 gateway_requests=0 data_plane_requests=0
```

The lifecycle scanner package tests passed for every configured canary class, unsafe symlinks and malformed/duplicate configuration. The scanner entrypoint then emitted its sole fixed success classification against a protected empty artifact directory. This proves the scanner boundary only; it does not mark the full success/failure/policy-race artifact matrix complete:

```text
account_inventory_lifecycle_canary_scan=success
```

Accepted enhanced PostgreSQL gate output:

```text
account_inventory_lifecycle_postgres=success server_major=18 migration_no_backfill=covered baseline=covered consecutive_missing=covered recovery=covered out_of_scope=covered re_add=covered fake_driver_real_store=covered uncommitted_termination=covered commit_unknown=covered permissions=covered policy_mutation_switch=covered capacity_1_10_50=covered request_count=0 gateway_requests=0 data_plane_requests=0
```

Accepted snapshot-only binary rollback output:

```text
account_inventory_lifecycle_rollback=success old_revision=e482d8e forward_schema=7 poll_enabled=false policy_mutation_enabled=false old_product_reads=3 lifecycle_frozen=true next_qualified_poll_advanced=true finalized_replay_advanced=false management_requests=0 real_node_requests=0 gateway_requests=0 data_plane_requests=0
```

Accepted official synthetic wrapper output:

```text
account_inventory_lifecycle_acceptance=success mode=official_synthetic lifecycle_gate=postgres official_lifecycle_rows=1 official_lifecycle_present=1 driver_request_count=1 request_wait_seconds=10 real_node_requests=0 gateway_requests=0 data_plane_requests=0 management_writes=0
```

Accepted data-plane isolation wrapper output:

```text
account_inventory_lifecycle_acceptance=success mode=data_plane_isolation lifecycle_rows_before_stop=2 lifecycle_rows_after_restart=2 retained_window_data_plane_http=50/50 lifecycle_progress=paused management_requests=0 real_node_requests=0
```

The enhanced official harness directly checks that the one production Driver/Worker/Store invocation persists exactly one `present` lifecycle row with the same poll/source times. The separate PostgreSQL fixtures establish multi-promotion transitions with `request_count=0`; the one official response MUST NOT be reinterpreted as two missing transitions and recovery.

The enhanced data-plane harness passed after checking two persisted `present` lifecycle rows immediately before the retained-state PostgreSQL stop, running a separate synthetic loopback `50/50` while PostgreSQL was stopped, and reading the same two rows plus persisted lifecycle metrics after restart.

CI also runs `scan-smoke`, which constructs a protected synthetic aggregate artifact and exercises scanner configuration and traversal. Its fixed output is still only:

```text
account_inventory_lifecycle_canary_scan=success
```

That smoke alone is not full-path evidence. The separate `scan-matrix` gate configures all 15 distinct canary classes, then actually passes them through the production CLIProxyAPI Driver and Worker success path, a raw transport failure, Worker lost-lease policy-race and database-error rollback classifications, and the generated sqlc lifecycle finalize parameter formatter. The test components write their resulting structured logs, measured request count, fixed Driver error, redacted SQL parameter format and Go test output into protected success, failure, policy-race and rollback subdirectories. The formal scanner then traverses the complete final directory and retained only:

```text
account_inventory_lifecycle_canary_scan=success
```

This is synthetic component-input evidence: the success path issued one local management GET, the network-failure path attempted one dial without reaching an HTTP server, and policy-race/rollback used two in-process fake Driver calls. It proves those generated final artifacts are canary-free; it does not claim that production artifacts were collected, that a real Node was contacted, or that arbitrary raw logs are safe to retain.

The final acceptance patch passed twice-reproducible generation, `make test`, `make build`, all Go tests, race detection, vet, frontend test/typecheck/build, workflow lint, change strict validation, all-spec strict validation and `git diff --check`. The generated diff hash was identical across both consecutive generation runs. The final active-change GitHub Actions run passed before archive.

## Transition evidence to record

Record only bounded counts/classifications for these checkpoints:

- Migration with existing snapshot history: lifecycle rows `0`, historical promotion rewrites `0`.
- First non-empty qualified promotion: present count equals the bounded fixture count; first/last seen relation valid; missing/out-of-scope counts `0`.
- First qualified absence: suspected count advances once, missing count remains `0`, source/last-seen refresh count `0`.
- Second qualified absence and saturation: missing count advances once, repeated-miss timestamp rewrite count `0`.
- Recovery: present recovery count matches fixture, first-seen rewrite count `0`, missing fields remaining count `0`.
- Complete empty set: active lifecycle rows advance by exactly one missing step; absent out-of-scope rows advance `0`.
- Scope removal/re-add: provider/account transition is all-or-nothing; absent old accounts restored `0`; actually observed accounts restored only by the next qualified promotion.
- Skip/failure matrix: lifecycle changed rows `0`, Provider pointer changed rows `0` where promotion did not apply.
- Old fencing, finalized replay and commit-unknown recovery: duplicate lifecycle advancements `0`.

Do not record the fixture's provider, email, account key, timestamps, UUIDs, endpoints, version/commit or raw SQL assertion output.

## Failure, rollback and request accounting

The accepted session record MUST distinguish:

- Direct PostgreSQL lifecycle tests: `request_count=0`.
- Fake Driver sequences: exactly one existing Driver call per poll observation; lifecycle adds `0` calls.
- Scope activation/re-add transactions: Node requests `0`.
- Snapshot-only binary rollback: three local health/bootstrap/metrics reads, management/real-Node/Gateway/data-plane requests `0`.
- Official synthetic container: exactly one GET followed by its final 10-second cooldown.
- Data-plane isolation: management requests `0`, synthetic loopback data-plane `50/50`; this is process isolation and does not claim a real Gateway/Node request.
- Real-Node lifecycle mode: not executed and fixed at `request_count=0` unless a later, separately approved evidence session replaces this statement.

Rollback evidence must show both lifecycle poll and Provider policy mutation were disabled before an old binary ran; forward schema and committed lifecycle rows remained; down was not executed; restart of the new version continued only from a next qualified promotion and did not backfill stopped slots.

## Canary boundary

Use distinct canaries for endpoint, IP, Secret reference/value, email, account key, response body/header, version/commit, raw error, SQL parameter, poll/policy ID and unknown field. Identity canaries MAY appear only inside the three expected protected identity column families during isolated database assertions. Before scanning artifacts, project database results to fixed counts/classifications; the artifact scanner rejects every canary everywhere and never emits a matching value or filename.

Retain only:

```text
account_inventory_lifecycle_canary_scan=success
```

## Verification commands

Every command clears uppercase and lowercase HTTP/HTTPS/ALL proxy variables. npm uses the approved mirror.

```sh
unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
export NO_PROXY='*' no_proxy='*'
export GOPROXY='https://goproxy.cn,direct'
export npm_config_registry='https://registry.npmmirror.com'

deploy/acceptance/account-inventory-lifecycle-run.sh static
deploy/acceptance/account-inventory-lifecycle-run.sh postgres
deploy/acceptance/account-inventory-lifecycle-run.sh rollback
deploy/acceptance/account-inventory-lifecycle-run.sh container
deploy/acceptance/account-inventory-lifecycle-run.sh data-plane
deploy/acceptance/account-inventory-lifecycle-run.sh scan-smoke
deploy/acceptance/account-inventory-lifecycle-run.sh scan-matrix
deploy/acceptance/account-inventory-lifecycle-run.sh scan

make generate
make test
make build
go test ./...
go test -race ./...
go vet ./...
npx --yes @fission-ai/openspec@1.10.0 validate add-control-account-inventory-lifecycle-foundation --strict
npx --yes @fission-ai/openspec@1.10.0 validate --all --strict
git diff --check
```

## Completion rule

Do not change this evidence to `complete` and do not archive the OpenSpec change until every matrix row is backed by an executed gate on the final commit, CI is green, request accounting is reconciled per run, temporary Docker resources and protected artifact directories are absent, and the worktree contains no sensitive/runtime material. Known unrelated baseline failures, if any, must be named only by fixed count and ownership; they cannot be silently treated as lifecycle passes.
