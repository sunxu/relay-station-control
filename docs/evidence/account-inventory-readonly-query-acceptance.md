# Account inventory read-only query acceptance evidence

Date: `2026-08-28`

Archived change: `openspec/changes/archive/2026-08-28-add-control-account-inventory-readonly-query`

Canonical specification: `openspec/specs/account-inventory-readonly-query/spec.md`

Code candidate commit: `517171099b8572e6253c4c007fbe0789b8d97a0b`

Status: **accepted — code-candidate local gates and CI passed; OpenSpec change archived**

The code candidate passed the complete local gate and all six jobs in [GitHub Actions run 33141524739](https://github.com/sunxu/relay-station-control/actions/runs/33141524739). The subsequent archive working tree passed the full archived-layout acceptance locally. The cited CI run validated the same executable candidate while the change was active; the final archive commit requires its own green CI before release handoff.

## Evidence handling rules

Retain only the candidate commit, UTC execution window, tool exit status, fixed classifications, bounded aggregate counts, durations, PostgreSQL major/Migration version, reconciled request counts and CI run link. Do not retain or paste:

- email, account key, cursor, filter value/hash, response body or database rows/dumps;
- instance/admin/poll/policy UUIDs, endpoint/IP, Node version/commit or request/response headers;
- Cookie, CSRF, password, TOTP, keyring material, Secret reference/value or connection string;
- SQL parameters, raw errors, unrestricted application/access logs, HAR/browser trace, screenshot or local absolute paths.

Canary failures must emit only a fixed failure classification. Never paste a matching value, filename or surrounding content into this file. A matrix row may move from `not run` to `passed` only when the actual final-candidate run is named and its request accounting and cleanup are complete. Record failures as `failed` or `blocked`; do not rewrite them as skips.

## Required evidence matrix

| Area | Required executed proof | Expected request boundary | Current status | Actual bounded evidence |
|---|---|---:|---|---|
| Contract and generation | Strict POST body/schema/enums/errors/no-store; two consecutive `make generate` runs with no second diff | external/Node/Gateway `0` | passed locally | Two final-candidate generation runs left the worktree unchanged; strict contract and focused HTTP/UI gates passed. |
| Forward Migration | PostgreSQL 18 applies `00008`; existing public table set, column descriptors, row counts and canonical row fingerprints remain unchanged; no identity copy/backfill | network `0` | passed locally and in CI | A populated schema `7` upgraded to `8` with identical public table/column descriptors, row counts and canonical row fingerprints; identity copy/backfill `0`. |
| Protected down | Empty fresh environment can down/up; audit rows or later dependencies make down fail closed; committed state remains | network `0` | passed locally | Empty `8→7→8` restored compatibility; an audit-bearing down was refused and remained at `8`. |
| Runtime permission matrix | runtime has function EXECUTE but no account/provider/snapshot/asset table enumeration or DML; unauthorized role denied | network `0` | passed locally | Runtime function access passed; whole-table SELECT and DML across five sensitive tables, asset Secret-column access, and registrar function execution were denied. |
| Query semantics | one instance, exact filters, limit `1..100`, limit+1, stable account-key keyset, empty/boundary/combined/deep pages, no account key in product DTO | network `0` | passed locally | PostgreSQL Store, contract, HTTP and capacity scenarios passed; product DTO forbidden-key count `0`. |
| Capability/error mapping | unknown instance `404/not_found`, unsupported capability `409/conflict`, inconsistent Provider state `503/temporarily_unavailable` | Node/Gateway/external `0` | passed locally | Fixed HTTP mappings passed; query-path external requests `0`. |
| Freshness semantics | fresh/stale 15-minute boundary, degraded+fresh coexistence, out-of-scope fixed state and missing-source fail closed | network `0` | passed locally | Store and unit matrices covered fixed boundary and fail-closed projections. |
| Cursor security | random nonce AEAD, environment/actor/instance/all-filter binding, strict 15-minute TTL, tamper/truncation/version/key/tag/length/plaintext rejection | network `0` | passed locally | Full cursor unit matrix plus focused race gate passed with one fixed invalid-cursor classification. |
| Key rotation | old cursor readable only while key retained and TTL valid; removed/expired/cross-environment cursor gets the same fixed `400` | network `0` | passed locally | Cursor retention/removal/expiry/cross-environment matrix passed; recovery also accepted a retained v1 session after rotated-keyring restart. |
| Authentication and CSRF | enabled super-admin succeeds; missing/revoked/disabled session and invalid CSRF fail before account read | database query count on rejection `0` | passed locally | Authorized and rejection matrix passed; Store calls on pre-read rejection `0`. |
| Query/audit atomicity | non-empty, empty, subsequent and repeated pages commit `account_inventory.view` before response; insert/commit failure returns no items | external `0`; one audit per successful page | passed locally | Atomic audit gate covered normal/empty/repeated pages, commit failure and post-commit disconnect. |
| Audit allowlist | only instance, filter-used booleans, cursor-used and result-count details; identity/cursor/filter hash/unknown keys rejected | network `0` | passed locally | Allowlist and bounded sensitive-artifact gate passed; forbidden detail acceptance `0`. |
| HTTP response allowlist | only approved current-state fields, fixed errors/request ID and `Cache-Control: no-store`; no forbidden product fields | network `0` | passed locally | Exact response keys, fixed errors, request ID and `no-store` assertions passed. |
| React behavior | capability-only Node selection, exact filters, paging/reset, narrow screen/keyboard/a11y, explicit empty/error states and memory-state cleanup | browser→Control only | passed locally | Focused Vitest/JSDOM matrix and full frontend tests passed; this is not a real-browser E2E claim. |
| Negative UI boundary | no export/copy/detail/bulk/mutation/promotion routes or controls; no URL/persistent-storage identity | external `0` | passed locally | Forbidden controls, URL identity and browser persistent/cache occurrences `0`. |
| Observability/canary | success, empty, invalid filter/cursor, audit failure and UI error artifacts contain no protected canary; labels remain low cardinality | Node/Gateway/Prometheus/external `0` | passed locally | Focused bounded artifact canary occurrences `0`; recovery logs were protected and deleted, not retained for canary scanning. |
| Concurrency | query races with full/empty promotion, Provider scope transition and rollback; only committed current state, no partial/default page or state-machine blocking | query adds Node calls `0` | passed locally | PostgreSQL concurrency and race gates passed; query-path Node calls `0`. |
| Capacity | 1/10/50 Nodes, total 1,000 synthetic accounts, worst filters/deep pages/concurrent admins; P50/P95/P99, buffers and audit cost measured | network `0` | passed locally | Three 1,000-account matrices, seven scenarios and 500 samples/matrix passed with errors `0`; see final record. |
| Database fault/recovery | stop/restart, pool exhaustion, statement timeout, audit commit failure; query fails closed then resumes from PostgreSQL current truth | data-plane result recorded separately | passed locally | PostgreSQL and Control restart, in-flight termination, three fail-closed faults and current-state recovery passed. |
| Data-plane isolation | Control/PostgreSQL query outage does not disturb the pinned official CLIProxyAPI authenticated `/v1/models` probe | management external calls `0` | passed locally and in CI | Baseline `1/1` and outage window `100/100`; no inference, Gateway/Relay Node E2E or real-upstream claim. |
| Rollback | old compatible binary with query navigation closed; forward schema/index/audit retained; production down not executed | external `0` | passed locally | Pinned snapshot-only `e482d8e` returned `404` for the new route, retained one view audit on schema `8`, kept lifecycle frozen, and the new binary resumed once; production down was not run. |
| Release and CI | full Go/race/vet, frontend, build, strict OpenSpec/all-spec, diff check and CI pass on candidate | reconciled per run | passed — CI run `33141524739` | All six jobs passed: quality, race, PostgreSQL snapshot, PostgreSQL lifecycle/rollback, official CLIProxyAPI lifecycle and readonly-query `all`. |
| Cleanup | isolated databases/containers/networks, protected request files, Cookie jars, browser output and canary artifacts removed | residual sensitive artifacts `0` | passed locally | Full gate reported container/volume/network residual `0`; bounded artifact and persistent browser occurrences `0`. |

### Final-candidate acceptance record

The [CI readonly-query job](https://github.com/sunxu/relay-station-control/actions/runs/33141524739/job/98753203884) executed against the clean code candidate from `2026-08-28T04:21:24Z` through `04:27:38Z` and exited `0`. After archiving, the complete local runner executed against the archive working tree from `2026-08-28T05:29:01Z` through `05:30:27Z` and exited `0`. `query_external_requests=0` describes the product query path and isolated acceptance network; it does not describe dependency or container-image downloads. The UI evidence is a Vitest/JSDOM matrix, not a real-browser E2E run.

```text
account_inventory_readonly_query_postgres=success server_major=18 migration=8 migration8_existing_state_unchanged=covered migration8_identity_copy_backfill=0 protected_down_empty_up=covered protected_down_audit_fail_closed=covered permissions=covered runtime_function_execute=allowed runtime_sensitive_table_enumeration=denied runtime_sensitive_table_dml=denied unauthorized_function_execute=denied query_semantics=covered http=covered concurrency=covered atomic_audit=covered database_faults=covered capacity_1_10_50=covered query_external_requests=0 cleanup_containers=0 cleanup_volumes=0 cleanup_networks=0
account_inventory_readonly_query_recovery=success server_major=18 postgres_stop_restart=1 control_process_stop_restart=1 http_inflight_stop_response_delivered=0 http_inflight_stop_audit_commits=0 http_restart_query=1 rotated_keyring_restart_session=1 statement_timeout_fail_closed=1 pool_connection_exhaustion_fail_closed=1 audit_commit_failure_fail_closed=1 store_recovered_current_items=1 official_data_plane_baseline=1 official_data_plane_http=100/100 cleanup_containers=0 cleanup_volumes=0 cleanup_networks=0
account_inventory_readonly_query_acceptance=success mode=all key_rotation=covered sensitive_canary_occurrences=0 browser_persistent_occurrences=0 query_external_requests=0
```

The official data-plane check was the pinned CLIProxyAPI authenticated `/v1/models` endpoint: one baseline request and 100/100 requests during the Control/PostgreSQL stop window. It is not an inference, Gateway/Relay Node end-to-end, or real-upstream claim. Recovery key rotation proved a retained v1 session survived a rotated-keyring restart; the separate cursor gate proved old-key retention, removal, expiry, cross-environment rejection and current-v2 issuance.

Capacity values are aggregate microseconds across seven sequential/concurrent scenarios. Each matrix used 1,000 accounts, 420 sequential samples plus 80 concurrent queries from eight administrators, and 500 committed view audits. Audit-adapter overhead is the bounded audited-P95 minus direct-P95 estimate.

| Nodes | P50 µs | P95 µs | P99 µs | Max plan µs | Max buffers | Errors | Audit rows | Audit overhead P95 µs |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 1 | 6,362 | 15,995 | 71,351 | 8,871 | 4,914 | 0 | 500 | 4,071 |
| 10 | 4,319 | 12,680 | 92,629 | 6,753 | 1,232 | 0 | 500 | 4,113 |
| 50 | 2,049 | 13,325 | 143,332 | 6,310 | 582 | 0 | 500 | 2,351 |

These capacity values are one observation on a GitHub `ubuntu-24.04` runner, not a production SLO or a cross-hardware benchmark. The archived local rerun also passed all three matrices with zero errors.

The pinned snapshot-only binary at `e482d8e` also passed in the [CI lifecycle job](https://github.com/sunxu/relay-station-control/actions/runs/33141524739/job/98753203842) on forward schema `8` (minimum forward schema `7`): three old product reads succeeded, one new-route request returned `404` without reflecting request identity, one bounded `account_inventory.view` audit remained unchanged, poll and policy mutation stayed disabled, lifecycle state stayed frozen, the restored binary advanced the next qualified poll exactly once, finalized replay remained fenced, and management/Node/Gateway/data-plane request counts were `0`.

## Execution records

Only final-candidate, green CI and archive-working-tree records are retained here; earlier development-worktree measurements remain available in Git history and are not release evidence.

| UTC window | Candidate commit | Environment/gate | Command family | Exit status | Fixed result/counts | Request accounting | Cleanup | Reviewer |
|---|---|---|---|---:|---|---|---|---|
| `2026-08-28 04:17:10–04:19:27` | `517171099b8572e6253c4c007fbe0789b8d97a0b` | final local code-candidate quality gates | `make test`, `make build`, full race, vet, diff check | `0` | frontend `13` files/`54` tests; generated/code drift `0` | test query boundaries as above | code/generated diff `0` | parallel Codex review; local pass |
| `2026-08-28 04:20:21–04:27:44` | `517171099b8572e6253c4c007fbe0789b8d97a0b` | [active-change CI](https://github.com/sunxu/relay-station-control/actions/runs/33141524739) | GitHub Actions | `0` | six jobs `6/6` green | reconciled by scoped acceptance summaries | job cleanup passed | GitHub-hosted `ubuntu-24.04` |
| `2026-08-28 04:21:24–04:27:38` | `517171099b8572e6253c4c007fbe0789b8d97a0b` | PostgreSQL 18/Migration 8 readonly-query CI | readonly-query acceptance `all` | `0` | migration preservation `1/1`; protected down `2/2`; faults `3/3`; restarts `2/2`; capacity `3/3`; canary/persistence `0/0` | query external `0`; official data plane baseline `1/1`, outage `100/100` | container/volume/network residual `0/0/0` | [job `98753203884`](https://github.com/sunxu/relay-station-control/actions/runs/33141524739/job/98753203884) |
| `2026-08-28 04:25:05–04:25:55` | `517171099b8572e6253c4c007fbe0789b8d97a0b` | pinned old-binary rollback on PostgreSQL 18/Migration 8 | lifecycle rollback acceptance | `0` | old reads `3`; route requests `1`; route closed `true`; identity occurrences `0`; retained audits `1`; frozen `true`; next advance `true`; replay advance `false` | management/Node/Gateway/data-plane `0/0/0/0` | bounded cleanup passed | [job `98753203842`](https://github.com/sunxu/relay-station-control/actions/runs/33141524739/job/98753203842) |
| `2026-08-28 05:29:01–05:30:27` | archive working tree atop `517171099b8572e6253c4c007fbe0789b8d97a0b` | archived-layout static, PostgreSQL 18, capacity and recovery | readonly-query acceptance `all` | `0` | OpenSpec archived branch; capacity `3/3`; faults/recovery passed | query external `0`; official data plane `100/100` | container/volume/network residual `0/0/0` | parallel Codex review; local pass |

If a run fails, record only its fixed failure class and exit status, correct the implementation, and execute the complete affected gate again on the new candidate. Do not carry a pass forward across code, Migration, generated artifact or test-harness changes; bounded evidence wording may be updated only when it does not broaden the recorded claim.

## Commands that require actual execution

These are the repeatable command families used by the recorded local gates. Every invocation must clear uppercase and lowercase HTTP/HTTPS/ALL proxy variables. PostgreSQL tests must use an isolated PostgreSQL 18 database and protected service/password files; secret-bearing values are intentionally absent here.

```sh
unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
export NO_PROXY='*' no_proxy='*'
export GOPROXY='https://goproxy.cn,direct'
export npm_config_registry='https://registry.npmmirror.com'

make generate
make generate
make test
make build
go test ./...
go test -race ./...
go vet ./...
(
  cd web
  npm test
  npm run typecheck
  npm run build
)
if [ -d openspec/changes/add-control-account-inventory-readonly-query ]; then
  npx --yes @fission-ai/openspec@1.10.0 validate add-control-account-inventory-readonly-query --strict
else
  test -f openspec/specs/account-inventory-readonly-query/spec.md
  test "$(find openspec/changes/archive -mindepth 1 -maxdepth 1 -type d -name '*-add-control-account-inventory-readonly-query' | wc -l | tr -d ' ')" -eq 1
fi
npx --yes @fission-ai/openspec@1.10.0 validate --all --strict
git diff --check
```

Record the two-generation reproducibility result only after comparing the actual candidate worktree before/after each run. A zero exit code from `go test -run` is insufficient unless the expected test names were first enumerated and matched.

## Canary and request-accounting record

The focused Go and Vitest/JSDOM matrices exercised 11 protected classes across success, empty, invalid-filter, audit-failure, cursor-failure and UI-error scenarios. Only bounded counts are retained:

```text
candidate_commit=517171099b8572e6253c4c007fbe0789b8d97a0b
canary_matrix_status=passed
retained_bounded_artifact_canary_occurrences=0
browser_url_identity_occurrences=0
browser_persistent_storage_identity_occurrences=0
audit_forbidden_detail_occurrences=0
node_requests=0
gateway_requests=0
prometheus_requests=0
other_query_external_requests=0
database_query_rejection_count=0
successful_page_audits=covered_by_scoped_atomicity_and_capacity_counts
official_data_plane_baseline=1
official_data_plane_http=100/100
temporary_container_volume_network_residual_count=0
```

The authorized API response and protected identity columns are test-only allowed locations during the active assertion. They must be removed or projected to bounded counts before artifact scanning and must never be copied into this document.

## Completion rule

The code candidate is accepted because every required matrix row has executed evidence, request counts are reconciled, the retained scan is clean, temporary resources were removed and CI run `33141524739` is green. The OpenSpec change is archived and the archived working tree passed the complete local acceptance. The archive commit still requires its own green CI before release handoff. Any later code, Migration, generated artifact or acceptance-harness change creates a new candidate and requires the complete affected gates again; evidence-only wording changes must not broaden the recorded claims.
