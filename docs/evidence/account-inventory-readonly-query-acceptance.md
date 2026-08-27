# Account inventory read-only query acceptance evidence

Date: `TBD — fill only after an acceptance run`

Change: `add-control-account-inventory-readonly-query`

Candidate commit: `TBD`

Status: **not accepted — evidence template only**

This file currently records no passing acceptance claim. Every row remains `not run` until the named gate has actually executed against the final candidate commit and its bounded, non-sensitive result has been reviewed. Development-time compilation, a test name existing in the tree, an earlier commit's CI, or a command planned in the Runbook MUST NOT be converted into `passed` evidence.

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
| Contract and generation | Strict POST body/schema/enums/errors/no-store; two consecutive `make generate` runs with no second diff | external/Node/Gateway `0` | not run | — |
| Forward Migration | PostgreSQL 18 applies `00008`; existing lifecycle/provider/snapshot fields are byte-for-byte/logically unchanged; no identity copy/backfill | network `0` | not run | — |
| Protected down | Empty fresh environment can down/up; audit rows or later dependencies make down fail closed; committed state remains | network `0` | not run | — |
| Runtime permission matrix | runtime has function EXECUTE but no account/provider/snapshot/asset table enumeration or DML; unauthorized role denied | network `0` | not run | — |
| Query semantics | one instance, exact filters, limit `1..100`, limit+1, stable account-key keyset, empty/boundary/combined/deep pages, no account key in product DTO | network `0` | not run | — |
| Capability/error mapping | unknown instance `404/not_found`, unsupported capability `409/conflict`, inconsistent Provider state `503/temporarily_unavailable` | Node/Gateway/external `0` | development HTTP pass; final rerun required | PostgreSQL 18 HTTP and race runs; four fixed error subtests passed; forbidden network counters `0` |
| Freshness semantics | fresh/stale 15-minute boundary, degraded+fresh coexistence, out-of-scope fixed state and missing-source fail closed | network `0` | not run | — |
| Cursor security | random nonce AEAD, environment/actor/instance/all-filter binding, strict 15-minute TTL, tamper/truncation/version/key/tag/length/plaintext rejection | network `0` | not run | — |
| Key rotation | old cursor readable only while key retained and TTL valid; removed/expired/cross-environment cursor gets the same fixed `400` | network `0` | not run | — |
| Authentication and CSRF | enabled super-admin succeeds; missing/revoked/disabled session and invalid CSRF fail before account read | database query count on rejection `0` | development HTTP pass; final rerun required | enabled success; missing session, disabled admin and missing CSRF rejected; Store calls on rejection `0` |
| Query/audit atomicity | non-empty, empty, subsequent and repeated pages commit `account_inventory.view` before response; insert/commit failure returns no items | external `0`; one audit per successful page | measured on non-final worktree; release rerun required | PostgreSQL 18 deferred-commit fault: three bounded pages, empty page and two repeats produced six audits; failed commit returned zero page/audit; post-commit context disconnect retained one audit |
| Audit allowlist | only instance, filter-used booleans, cursor-used and result-count details; identity/cursor/filter hash/unknown keys rejected | network `0` | not run | — |
| HTTP response allowlist | only approved current-state fields, fixed errors/request ID and `Cache-Control: no-store`; no forbidden product fields | network `0` | development HTTP pass; final rerun required | exact top-level/item key sets, nullable fields, no-store/request ID and forbidden-field absence asserted; network `0` |
| React behavior | capability-only Node selection, exact filters, paging/reset, narrow screen/keyboard/a11y, explicit empty/error states and memory-state cleanup | browser→Control only | not run | — |
| Negative UI boundary | no export/copy/detail/bulk/mutation/promotion routes or controls; no URL/persistent-storage identity | external `0` | passed on non-final worktree; release rerun required | focused DOM/transport matrix: forbidden controls `0`; URL query/redirect/Location and persistent browser/cache identity occurrences `0` |
| Observability/canary | success, empty, invalid filter/cursor, audit failure and UI error artifacts contain no protected canary; labels remain low cardinality | Node/Gateway/Prometheus/external `0` | passed on non-final worktree; release rerun required | 11 distinct protected classes scanned through six scenarios; final bounded artifact occurrences `0`; focused Go race and UI matrix passed |
| Concurrency | query races with full/empty promotion, Provider scope transition and rollback; only committed current state, no partial/default page or state-machine blocking | query adds Node calls `0` | measured on non-final worktree; release rerun required | PostgreSQL 18 overlapping transactions covered full/empty promotion, Provider scope switch and rollback; eight audited reads saw only committed lifecycle, partial/default rows `0`, query-induced commit delay gate passed |
| Capacity | 1/10/50 Nodes, total 1,000 synthetic accounts, worst filters/deep pages/concurrent admins; P50/P95/P99, buffers and audit cost measured | network `0` | measured on non-final worktree; release rerun required | PostgreSQL 18/Migration 8; three 1,000-account matrices, 120 samples/scenario, 8×30 concurrent queries/matrix, errors `0`; see bounded capacity record below |
| Database fault/recovery | stop/restart, pool exhaustion, statement timeout, audit commit failure; query fails closed then resumes from PostgreSQL current truth | data-plane result recorded separately | partial database-side development run; task 7.5 remains open | statement timeout, one-connection pool exhaustion, deferred audit commit failure and blocked-backend termination failed closed; three recovery reads succeeded; Control/PostgreSQL stop-restart and data-plane continuity not run |
| Data-plane isolation | Control/PostgreSQL query outage does not disturb the approved Gateway/Relay Node data-plane probe | management external calls `0` | not run | — |
| Rollback | old compatible binary with query navigation closed; forward schema/index/audit retained; production down not executed | external `0` | not run | — |
| Release and CI | full Go/race/vet, frontend, build, strict OpenSpec/all-spec, diff check and active-change CI pass on candidate | reconciled per run | not run | — |
| Cleanup | isolated databases/containers/networks, protected request files, Cookie jars, browser output and canary artifacts removed | residual sensitive artifacts `0` | not run | — |

## Execution record template

### Bounded capacity development record

This is an actual development-worktree measurement, not final release acceptance. The implementation was still uncommitted on top of `16f683ed42a6`, so the complete capacity gate MUST be rerun on the final candidate before the matrix row can be marked `passed`.

The isolated PostgreSQL 18 run created three independent matrices: `1×1000`, `10×100`, and `50×20` Nodes/accounts. Each sequential scenario used 10 warmups followed by 120 measured audited queries. `deep_page` began after the bounded 80% key position. The concurrency case used 8 workers × 30 audited queries. Values are milliseconds.

| Nodes | Scenario | P50 | P95 | P99 | Plan rows | Shared hit buffers | Plan execution |
|---:|---|---:|---:|---:|---:|---:|---:|
| 1 | unfiltered | 2.701 | 4.814 | 16.880 | 101 | 4913 | 3.173 |
| 1 | Provider | 2.755 | 3.850 | 4.824 | 101 | 4460 | 1.439 |
| 1 | exact email | 1.114 | 1.788 | 2.933 | 1 | 61 | 0.647 |
| 1 | combined Provider/lifecycle/basic status | 2.730 | 3.094 | 3.289 | 101 | 4460 | 1.569 |
| 1 | deep page | 1.930 | 2.628 | 6.881 | 101 | 1226 | 0.893 |
| 10 | unfiltered | 1.845 | 2.221 | 2.684 | 100 | 1231 | 2.388 |
| 10 | Provider | 1.792 | 2.103 | 2.265 | 100 | 821 | 0.676 |
| 10 | exact email | 0.870 | 1.048 | 1.372 | 1 | 25 | 0.563 |
| 10 | combined Provider/lifecycle/basic status | 1.846 | 2.453 | 3.352 | 100 | 820 | 0.843 |
| 10 | deep page | 1.037 | 1.219 | 1.376 | 19 | 169 | 0.618 |
| 50 | unfiltered | 1.021 | 1.224 | 1.343 | 20 | 582 | 2.033 |
| 50 | Provider | 1.078 | 1.245 | 1.369 | 20 | 172 | 0.524 |
| 50 | exact email | 0.932 | 1.159 | 1.332 | 1 | 20 | 0.579 |
| 50 | combined Provider/lifecycle/basic status | 1.075 | 1.328 | 1.633 | 20 | 172 | 0.557 |
| 50 | deep page | 0.906 | 1.112 | 1.226 | 3 | 36 | 0.638 |

All retained plans were PostgreSQL `Function Scan` plans over the versioned controlled function; the bounded buffer counts include work performed inside that security boundary. No identity-bearing plan parameters or rows were retained.

| Nodes | Concurrent P50/P95/P99 | Concurrent errors | Direct-read P50/P95/P99 | Audited-read P50/P95/P99 | Audit P50 delta | Committed view audits |
|---:|---|---:|---|---|---:|---:|
| 1 | 6.880 / 11.880 / 21.319 | 0 | 1.412 / 1.544 / 1.601 | 2.639 / 3.003 / 3.318 | 1.227 | 1010 |
| 10 | 3.942 / 6.356 / 16.914 | 0 | 0.623 / 0.762 / 0.788 | 1.823 / 2.272 / 2.814 | 1.200 | 1010 |
| 50 | 2.177 / 3.345 / 15.491 | 0 | 0.383 / 0.480 / 0.514 | 1.110 / 1.459 / 1.917 | 0.727 | 1010 |

The same isolated PostgreSQL 18 environment also executed `TestAccountInventoryHTTPAuthorizationPaginationAndErrorMapping`; its not-found, unsupported, inconsistent-state, and audit-failure subtests all passed. The capacity harness used only PostgreSQL connections: Node, Gateway, Prometheus, and other external requests were `0`.

### Transaction, concurrency and database-fault development record

Three independent PostgreSQL 18 schema integration tests executed against Migration 8. The concurrency test staged full promotion, empty promotion, recovery promotion rollback and Provider scope transition in open transactions. Reads completed against the prior committed truth, then observed the new state only after commit; rollback left the prior state unchanged. Eight successful pages were audited, and the final partial/default row count was `0`.

The audit test traversed three limit-one pages, then issued an empty page and two identical repeats. Those six successful calls committed six independent view audits. A deferred constraint trigger injected a commit-time failure after result construction; the returned page and committed audit delta were both `0`. A separate transaction committed its view audit before the client context was cancelled during a following statement; the durable audit count remained `1`.

The database-fault test injected a 100 ms statement timeout behind an exclusive lock, exhausted a one-connection pool, and terminated a query backend while it waited on a lock. All three fault calls returned zero items and committed zero view audits. Releasing each fault restored direct reads from current PostgreSQL state; the successful recovery audit count was `3`. This does not cover process/container stop-restart, Control restart, key rotation or data-plane continuity, so task 7.5 remains incomplete.

### Bounded HTTP development record

An isolated PostgreSQL 18 run executed the expanded HTTP acceptance test once normally and once with the Go race detector. It covered enabled and disabled administrators, missing session and CSRF, exact transmission of all four filters, first/subsequent pages, invalid cursor, twelve concurrent authorized requests, fixed Node qualification and unavailable errors, simulated audit failure, and the exact response field allowlist. Authentication/input rejections reached the fake Store `0` times. Across success and every error path, explicit forbidden adapter counters for Node, Gateway, Prometheus, model data plane, and arbitrary URL access were each `0`. The fake Store received `18` bounded calls per run; `14` were successful page attempts and `4` exercised fixed Store/audit errors. These are development-worktree results and MUST be rerun on the final candidate.

Add one entry per actual run. Never pre-fill a success classification.

| UTC window | Candidate commit | Environment/gate | Command family | Exit status | Fixed result/counts | Request accounting | Cleanup | Reviewer |
|---|---|---|---|---:|---|---|---|---|
| `2026-08-27 06:55–06:57` | `16f683ed42a6 + uncommitted implementation` | isolated PostgreSQL 18, Migration 8; capacity and HTTP database integration | protected capacity harness, `EXPLAIN (ANALYZE, BUFFERS)`, targeted Go integration | `0` | three matrices × 1,000 accounts; 120 samples/scenario; concurrent queries `720`, errors `0`; committed view audits `3030`; HTTP test/subtests passed `1/4` | Node `0`; Gateway `0`; Prometheus `0`; other external `0` | three temporary databases and harness removed; container/network/volume residual `0` | Codex development run; final-candidate review pending |
| `2026-08-27 07:21–07:24` | `16f683ed42a6 + uncommitted implementation` | isolated PostgreSQL 18, Migration 8; transaction/concurrency/database-fault gates | three targeted schema integration tests plus targeted race run | `0` | tests `3`; race tests `3`; concurrency audited reads `8`; atomicity durable audits `7`; recovery audits `3`; fault-path item/audit count `0/0` | Node `0`; Gateway `0`; Prometheus `0`; other external `0`; stop-restart/data-plane not run | temporary databases and container/network/volume removed; residual `0` | Codex development run; final-candidate review pending |
| `2026-08-27 07:21–07:22` | `16f683ed42a6 + uncommitted implementation` | isolated PostgreSQL 18; expanded HTTP boundary | targeted HTTP integration and race run | `0` | runs `2`; concurrent requests/run `12`; Store calls/run `18`; rejection Store calls `0`; fixed error subtests/run `4` | Node `0`; Gateway `0`; Prometheus `0`; model data plane `0`; URL `0` | container/network/volume/database residual `0` | Codex development run; final-candidate review pending |
| `2026-08-27 07:15–07:21` | `16f683ed42a6 + uncommitted implementation` | in-process Go/Vitest sensitive boundary | focused final-artifact scanner, API metrics/error/AAD cursor, real generated fetch, DOM/storage/cache matrix | `0` | Go artifact matrix `1/1` plus race `1/1`; two frontend files `9/9` twice; typecheck passed | Node `0`; Gateway `0`; Prometheus requests `0`; other external `0` | Go artifact temp directory removed automatically; browser persistent/cache occurrences `0` | Codex development run; final-candidate review pending |
| `TBD` | `TBD` | `TBD` | `TBD` | `TBD` | `TBD` | `TBD` | `TBD` | `TBD` |

If a run fails, record only its fixed failure class and exit status, correct the implementation, and execute the complete affected gate again on the new candidate. Do not carry a pass forward across code, Migration, generated artifact, test harness or acceptance-document changes.

## Commands that require actual execution

These are planned command families, not evidence that they passed. Every invocation must clear uppercase and lowercase HTTP/HTTPS/ALL proxy variables. PostgreSQL tests must use an isolated PostgreSQL 18 database and protected service/password files; secret-bearing values are intentionally absent here.

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
npx --yes @fission-ai/openspec@1.10.0 validate add-control-account-inventory-readonly-query --strict
npx --yes @fission-ai/openspec@1.10.0 validate --all --strict
git diff --check
```

Record the two-generation reproducibility result only after comparing the actual candidate worktree before/after each run. A zero exit code from `go test -run` is insufficient unless the expected test names were first enumerated and matched.

## Canary and request-accounting record

Fill this section only after the final protected matrix has run:

### Focused sensitive-boundary development record

This is an actual non-final worktree gate, not final release acceptance. The Go matrix passed protected email, account key, AEAD cursor, endpoint, Secret reference/value, poll/policy ID, version/commit and raw-error classes through authorized-success projection, empty result, invalid filter, audit failure, cursor failure, fixed logs, low-cardinality metrics, audit allowlist, DTO/sqlc formatters, access-log fixture and a temporary bounded artifact. The artifact retained zero protected occurrences. It initially detected an identity-bearing generated SQL parameter formatter; the focused test passed only after that parameter type received an overall-redacted formatter.

The browser matrix used the real generated fetch function and verified one POST body while URL query, redirect, `Location`, history, local/session storage and query-cache identity occurrences remained zero. UI error and unmount paths cleared mutation error/data before the final in-memory artifact scan. The two focused frontend files passed `9/9` twice and TypeScript typecheck passed. All commands cleared uppercase and lowercase HTTP/HTTPS/ALL proxy variables.

```text
focused_candidate=16f683ed42a6_plus_uncommitted_implementation
protected_canary_classes=11
covered_scenarios=success,empty,invalid_filter,audit_failure,cursor_failure,ui_error
go_bounded_artifact_canary_occurrences=0
metric_open_identity_dimensions=0
audit_forbidden_details_accepted=0
request_url_query_occurrences=0
redirect_responses=0
location_header_occurrences=0
access_log_body_occurrences=0
browser_history_identity_occurrences=0
browser_persistent_storage_identity_occurrences=0
frontend_query_cache_identity_occurrences=0
node_requests=0
gateway_requests=0
prometheus_requests=0
other_external_requests=0
temporary_artifact_residual_count=0
final_candidate_rerun_required=true
```

```text
candidate_commit=TBD
canary_matrix_status=TBD
retained_artifact_scan_status=TBD
browser_url_identity_occurrences=TBD
browser_persistent_storage_identity_occurrences=TBD
audit_forbidden_detail_occurrences=TBD
node_requests=TBD
gateway_requests=TBD
prometheus_requests=TBD
other_external_requests=TBD
database_query_rejection_count=TBD
successful_page_audit_count=TBD
synthetic_data_plane_result=TBD
temporary_resource_residual_count=TBD
```

The authorized API response and protected identity columns are test-only allowed locations during the active assertion. They must be removed or projected to bounded counts before artifact scanning and must never be copied into this document.

## Completion rule

Keep `Status: not accepted` until every required matrix row is backed by an actual final-candidate run, request counts are reconciled, failures/skips are disclosed, the active-change CI is green, all temporary resources are removed and the retained evidence scan is clean. Only then may the status and individual rows be updated with reviewed, bounded results; creating this template alone completes no acceptance task and does not justify checking OpenSpec tasks or archiving the change.
