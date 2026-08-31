# Account inventory history compaction contract crosswalk

Date: `2026-08-28`

Change: `openspec/changes/add-control-account-inventory-history-compaction`

Status: **reviewed contract with partial implementation evidence — not production-ready**

This document is the review crosswalk for OpenSpec tasks 1.1, 1.5, 1.6, 2.11, 8.3, and 8.4. It records the approved contract and the bounded implementation evidence listed below. It is not evidence that the complete production runtime, capacity, external-request, fault-injection, or release gates have passed. Those claims require their task-specific executed tests on the final candidate.

## Sources and precedence

| Source | Contract used here |
|---|---|
| [System design v1.0 §§12, 13.4, 16, 21, 23–25](../../../ops/docs/RELAY_STATION_SYSTEM_DESIGN_CN.md) | Snapshot/promotion truth, history table intent, UTC-day compaction, coverage, initial retention, alert inputs, capacity and failure acceptance. |
| [ADR-0001](../../../ops/docs/adr/0001-control-technology-stack.md) | Single-Control modular monolith; PostgreSQL 18 as durable truth; `pgx`/`sqlc`/Goose; explicit transactions; bounded workers; Redis optional only; low-cardinality Prometheus; structured redacted logs. |
| [Active change proposal](../../openspec/changes/add-control-account-inventory-history-compaction/proposal.md), [design](../../openspec/changes/add-control-account-inventory-history-compaction/design.md), [spec](../../openspec/changes/add-control-account-inventory-history-compaction/specs/account-inventory-history-compaction/spec.md) | The reviewed Phase 3 refinement: six aggregate/run history tables plus one durable retired-day cutoff table, dedicated run state machines, exact UTC/date rules, fixed 72-hour/30-day/95% policy, retention gates, compatibility, privacy, and explicit non-goals. |
| Canonical [poll](../../openspec/specs/account-inventory-poll-run/spec.md), [snapshot](../../openspec/specs/account-inventory-snapshot/spec.md), [lifecycle](../../openspec/specs/account-inventory-lifecycle/spec.md), and [current query](../../openspec/specs/account-inventory-readonly-query/spec.md) specs | Existing current-truth semantics that history must preserve. |

The active change is the narrower contract when an older design statement is broader. In particular, system design v1.0 described initial retention as environment-configurable and discussed general PostgreSQL background tasks. This change deliberately fixes `72 hours`, `30 days`, and `9500` basis points against runtime reinterpretation, and uses dedicated compaction/rollup run tables rather than registering a production `async_job_kind`. Both refinements retain ADR-0001's PostgreSQL-truth, lease/fencing, bounded-worker, and Redis-optional decisions.

## Field and storage crosswalk

| Record | Stable identity / uniqueness | Approved value fields | Derivation and mutation boundary | Identity boundary |
|---|---|---|---|---|
| Account policy segment | `(summary_date, instance_id, account_key, provider_policy_version)` | First/last scheduled and observed time; last basic status; sample and fixed status counts; first/last cumulative success/failure; within-segment reset counts | Built only from eligible, promotion-applied immutable snapshots; segment rows, source counts/checksum, and run `summarized` commit atomically; immutable afterward | Contains protected `account_key`; never copies normalized email |
| Provider policy segment | `(summary_date, instance_id, provider, provider_policy_version)` | Expected, transport-success, contract-valid, snapshot-complete, promotion-applied/skipped, abandoned and degraded counts; first/last promotion; ratio/status; threshold basis points | Expected slots come only from the Node-monitoring and Provider-policy half-open intersection; missing slots and abandoned remain in the denominator; no aligned slot means no segment | Contains no account identity |
| Final account rollup | `(summary_date, instance_id, account_key)` | Summed sample/status counts; first/last time; deterministic last status/counters; within- plus cross-segment reset counts | Reads every immutable expected account segment only after all expected compactions complete; final rows and rollup-run completion commit atomically | Contains protected `account_key`; never copies normalized email |
| Final Provider rollup | `(summary_date, instance_id, provider)` | Segment counts summed field-by-field; final applied/expected ratio; fixed threshold and `complete|partial` | Recomputes from sums, never averages segment ratios; one row across same-day policy switches/re-entry; only completed final rows are publishable | Contains no account identity |
| Compaction run | `(summary_date, instance_id, provider_policy_version)` | Closed status, `failed_from`, claim owner/lease/fencing, attempt, source snapshot/poll/provider/duplicate counts, checksum version/value, actual deleted snapshot/poll/Provider-result/duplicate counts, fixed failure class, phase timestamps | PostgreSQL is sole truth; summary and each bounded delete transition use fenced controlled functions and short transactions | Run/policy/fencing/checksum are protected recovery evidence and forbidden from ordinary output |
| Daily rollup run | `(summary_date, instance_id)` | Closed status, claim owner/lease/fencing, attempt, expected/completed segment counts, checksum version/value, fixed failure class, phase timestamps | Publishes only when all expected compaction keys are completed; completed is immutable | Run/fencing/checksum are protected recovery evidence and forbidden from ordinary output |
| Retired day cutoff | `(summary_date, instance_id)` | Database-authored `retired_at` only | Inserted only by completed rollup-run retention after all same-day poll and segment/final dependencies are empty; immutable and retained after compaction-run deletion so the planner cannot recreate lineage | Contains no account identity; protected day/instance control evidence is not an ordinary output |
| Provider current state additions | Existing `(instance_id, provider)` | Latest qualified finalized health slot, degraded boolean and fixed reason | Migration may backfill non-identity health from the current Provider result; later finalize updates only an existing promoted state for a strictly newer, still-active, non-stale/non-policy-changed result | Does not copy email/account key; current-source FK may later become null without losing copied current metadata |
| Current account query | Existing versioned PostgreSQL function and unchanged HTTP DTO | Existing Provider degraded/freshness and lifecycle projection | Reads denormalized Provider current health when a retained source poll has been deleted; missing/invalid current truth remains fixed `503` | Existing audited authorized email response remains unchanged; no history route or history identity projection is added |

The version-1 source and segment checksums are deterministic integrity evidence, not authentication credentials: stable row ordering, versioned length-prefixed field encoding, a zero 32-byte initial chain value, and PostgreSQL core `sha256(bytea)`. Tasks 1.4 and 3.7 have Go golden vectors for empty/single/multiple rows, stable read schemas and tie-breakers, field/order sensitivity, canonical-row rejection, and a diagnostic million-row max-RSS observation. Segment checksum version 1 includes every persisted account/Provider aggregate and identity field except generated/reference row ids and `created_at`, orders account rows before Provider rows, and fixes each family's identity order. PostgreSQL tests cross-check both source and full-field segment schemas against the Go chain and prove an additive source-table column does not change source checksum version 1.

## Closed state and recovery crosswalk

### Compaction

| From | Allowed next state | Required invariant |
|---|---|---|
| `pending` | `summarized` or `failed` with `failed_from=pending` | Eligibility, activation truth, source rows/counts/checksum, both segment families and audit are validated in one transaction. |
| `summarized` | `deleting` or `failed` with `failed_from=summarized` | Existing summaries/checksum are authoritative; source history must never be re-aggregated. |
| `deleting` | `deleting`, `completed`, or `failed` with `failed_from=deleting` | Every batch uses stable key order and actual `DELETE … RETURNING` count in the same transaction; completion requires remaining `0` and source/deleted conservation. |
| `failed` | Resume only from its closed `failed_from` phase | `pending` may safely summarize again; `summarized|deleting` may only verify and continue deletion. |
| `completed` | none | State, source/checksum evidence, and summaries are immutable until independent retention eligibility is proven. |

Direct `pending → completed`, any completed rollback, checksum/source mutation after summarized, and summary overwrite from residual source rows are forbidden. A stale fencing token affects zero rows. An unknown commit outcome is resolved by reading persisted state: absent commit can retry its legal phase; present summarized/delete state continues without duplicate accounting.

The pure state model and PostgreSQL implementation keep the same live lease and fencing token when `pending` becomes `summarized`, so that the claimed worker can renew and proceed to `deleting` without a second claim. `completed` and `failed` clear the lease. Transition tests require stale fencing to have zero effect, use `failed_from` to select summarize versus deletion-only recovery, and refuse completion unless actual deleted rows exactly equal the recorded source count. PostgreSQL 18 integration tests additionally exercise claim/renew, an atomic summarize with both segment families and source proof, a stable-key bounded snapshot batch with actual-count accumulation, checksum-guarded completion and idempotent completed replay, fixed failed transitions, expired-lease reconciliation, and rejection of a renewal that waited past lease expiry. Repository-loop tests cover phase resume, bounded completion retry after an unknown commit result, continuation from a persisted partial-delete count, and the prohibition on re-aggregation after `summarized`.

### Daily rollup

| From | Allowed next state | Required invariant |
|---|---|---|
| `pending` | `completed` or `failed` | Every expected deduplicated compaction key exists and is completed; final account rows, Provider rows, segment count/checksum, audit, and completed status are one transaction. |
| `failed` | Retry only for `segment_incomplete`, `statement_timeout`, `lease_expired`, or `database_unavailable` | Recomputes only from immutable completed segments; segment count/checksum, activation, and internal integrity failures remain stopped. |
| `completed` | none | Completed final rows and run cannot be recomputed or overwritten. |

Missing, pending, deleting, or failed expected segments block publication. A successfully computed Provider coverage below threshold is `partial`, not a failed rollup; it remains excluded from normal trend comparison.

The implemented planner creates the unique Node/day rollup run only after all expected deduplicated compactions complete. Claim, renew, reconcile, finalize, and fail use database time, lease and fencing; stale fences have zero mutation effect. Finalize recomputes account and Provider outputs from immutable segments and commits both final families, expected/completed segment counts, the full-field checksum, completed state, and fixed actor-null audit atomically. Recoverable failed reasons are allowlisted; integrity failures remain stopped. An unknown finalize commit is replayed only with the same run/fence and a bounded attempt, so persisted completed state is read idempotently rather than creating another truth.

## UTC date, eligibility, and activation rules

| Concern | Contract |
|---|---|
| Database clock | PostgreSQL `clock_timestamp()` and UTC database sessions are authoritative; host timezone and DST cannot change keys. |
| Day interval | `[summary_date 00:00:00 UTC, next day 00:00:00 UTC)`. |
| Eligibility | A date is eligible only when `day_end <= database_now - 72 hours`; this yields a normal 72–96 hour full-snapshot window. |
| Planning horizon and upgrade bootstrap | Normal activation-only planning requires `day_end > database_now - 30 days`. An older eligible day remains discoverable only while a same-day source poll or an existing compaction lineage is present, allowing pre-Migration-9 evidence and partially processed lineage to converge through the ordinary proof chain. Migration itself creates no summary or run. |
| Retired-day cutoff | A durable `(summary_date, instance_id)` marker excludes that day from both compaction and rollup planning regardless of remaining activation/source metadata. It also rejects later poll insertion into the retired UTC day. |
| Account segment date | `date(snapshot.observed_at AT TIME ZONE 'UTC')`. |
| Provider coverage, abandoned, policy segment and poll retention date | Poll `scheduled_at` UTC date. |
| Cross-day source | If a snapshot observed date differs from its poll scheduled date, both affected keys fail closed as `source_day_mismatch`; source rows and poll remain and no summary is overwritten. |
| Activation interval | Intersection of immutable Node monitoring and matching Provider policy `[effective_from,effective_to)` ranges, clipped to the UTC day. |
| Slot enumeration | Five-minute `scheduled_at` values satisfying `slot >= lower AND slot < upper`; non-aligned starts advance to the next aligned slot. |
| Same version re-entry | Merge overlapping/adjacent intersections and deduplicate the `(date, instance, policy)` key so a slot is counted once. |
| Zero-data segment | Required only when the intersection contains at least one aligned expected slot; expected is positive and applied is zero even if poll/snapshot rows are absent. |
| No-slot interval | Does not create a segment, expected count, or false partial status. |

## Retention and deletion order

| Data | Earliest eligibility | Additional gates |
|---|---|---|
| Full snapshot items | UTC `day_end <= now - 72h` | Corresponding compaction summarized; fenced bounded batch; actual delete count conserved before completed. |
| Terminal poll and Provider/duplicate children | `scheduled_at <= now - 30d` | Every applicable compaction completed and snapshot items empty; poll first, child history controlled cascade; current FKs may become null while copied current fields remain. |
| Account/Provider segments and final rollups | Both UTC `day_end <= now - 30d` and owning final rollup `completed_at <= now - 30d` | Poll history already cleared; completed rollup exists; no unfinished/failed dependency. |
| Completed daily rollup run | Its `completed_at <= now - 30d` | Dependent segment/final rows cleared; deleted before completed compaction runs. |
| Completed compaction run | Its `completed_at <= now - 30d` | All dependent poll, segment/final and rollup-run evidence already cleared; deleted last. |
| Retired day cutoff | Created when the completed daily rollup run is deleted | Inserted in the same transaction as rollup-run deletion, after all same-day poll and segment/final dependencies are empty; never deleted by this cleaner and blocks both replanning and late poll evidence. |
| Pending, summarized, deleting, failed, count/checksum-inconsistent evidence | Never automatically by this cleaner | Preserve regardless of age for recovery/investigation. |
| Current account and missing/out-of-scope lifecycle | Not in this change | Must not be deleted or rewritten by history retention. |
| History system audit | Initial `180d`; not cleaned by this change | Immutable actor-null record; later audit retention remains a separate capability. |
| Existing authentication audit and future alerts | Not in this change | No history-cleaner access. |

The required destructive order is poll/children → segment/final rows → completed rollup run → completed compaction run. Snapshot deletion is the earlier compaction phase. The implemented entry points are `control_delete_account_inventory_poll_retention_v1`, `control_delete_account_inventory_rollup_row_retention_v1`, `control_delete_account_inventory_rollup_run_retention_v1`, and `control_delete_account_inventory_compaction_run_retention_v1`. Each category has a distinct transaction-local gate and bounded controlled function; `UPDATE` and `TRUNCATE` are never retention mechanisms. Poll cascade progress persists actual `deleted_poll_count`, `deleted_provider_result_count`, and `deleted_duplicate_count` on the completed compaction run before that run can be deleted last. Rollup-run retention atomically inserts the durable retired-day cutoff before deleting the run; therefore the planner cannot resurrect rollup or compaction lineage during later bounded compaction-run deletion, including zero-poll days. The fixed 72-hour and 30-day values are not runtime configuration.

PostgreSQL 18 evidence runs poll retention twice with `limit=1`, records one processed poll and two actually deleted rows per transaction, and proves a synthetic audit failure rolls back both the delete and persisted progress. A child-cascade matrix separately injects Provider-result and duplicate delete-trigger failures; each leaves both polls, all child rows, all three persisted progress counters, and audit unchanged, then two successful `limit=1` scans each delete exactly one poll plus its two children and advance matching counters/audits. The original fixture proves Provider/account current-source FKs become null while the complete versioned current-query JSON remains byte-for-byte equivalent. It then drains account/Provider final rows in separate `limit=1` batches, demonstrates that the rollup run remains while a child exists, and only then deletes the rollup and compaction runs. A dedicated boundary matrix proves the production functions delete an old-side poll but retain its new-side peer, require both old UTC day end and old completion for segment/final rows, and retain completed rollup/compaction runs whose own completion is newer than the fixed 30-day cutoff. A separate candidate matrix deletes only the completed, snapshot-empty, expired all-terminal positive case; summarized, generic failed, `source_day_mismatch`, non-terminal sibling, retained-snapshot, and not-yet-expired cases remain unchanged across a second scan. A second exact Migration-8-to-9 fixture proves that a source-backed poll older than 30 days is first bootstrapped through compaction and rollup, rollup deletion persists the retired marker, `limit=1` compaction-run deletion cannot reopen the day, the planner creates no replacement lineage after full deletion, direct marker forgery is denied, a late same-day poll is rejected, and protected down refuses the durable marker. A third exact fixture proves a zero-poll day with existing completed compaction lineage can still create and complete its rollup after crossing the normal retention horizon. The pure model covers equality and one-nanosecond-before boundaries, UTC versus host-zone interpretation, fixed non-completed states, conservation, and overflow fail-closed behavior. This bounded sequential evidence remains distinct from the same-lineage four-way concurrency evidence below.

The production retention catalog and fixed 30-day PostgreSQL boundary matrix establish the inclusive eligibility of all four stages. One ordered lineage then proves that remaining poll or segment/final dependencies block later stages, coverage changes from one retained completed Provider rollup to no sample when that final row is deleted, and rollup-run deletion atomically creates its retired cutoff. The exact legacy chain deletes two completed compaction runs with `limit=1` and proves no planner resurrection both between batches and after the final batch. This closes 6.7 independently of the four-way concurrency gate.

The PostgreSQL 18 abnormal-retention matrix keeps expired pending, summarized, deleting, failed, `source_day_mismatch`, and `source_checksum_mismatch` compaction candidates unchanged across production poll-cleaner scans; the existing completion-count fixture separately proves a fixed `source_count_mismatch` and its poll evidence remain even after fixture-only count repair makes every other retention predicate true. After all four production cleaners run, exact fingerprints of current account/lifecycle rows remain unchanged, and pre-existing administrator-authorization and scope-transition audit IDs both still exist. Catalog assertions constrain the four functions' direct `DELETE` targets to the poll table, four segment/final tables, and two completed-run tables, with no dynamic SQL; because this change also adds no alert schema or route, future alerts remain outside cleaner ownership rather than being represented by a synthetic product table. A production-created retired cutoff rejects owner `UPDATE`, `DELETE`, and `TRUNCATE` with SQLSTATE `42501` and remains exactly one row after all four cleaners scan. The existing two-order retention/poll lock fixture supplies the final late same-day poll rejection (`23514`). Together these close 6.8 independently; the exact 8.3 audit and error projection evidence is recorded below.

The real retention fixture additionally fingerprints both current rows before and after deleting their source polls, removing only `current_poll_run_id`; every source timestamp/version/commit, base state, lifecycle/missing, and Provider-health field remains byte-for-byte equal while both source FKs become null. Its complete current-query JSON remains equal as the independent freshness proof.

The production poll-retention fixture also keeps the real Repository page equal before and after deletion. A separate real Repository/HTTP fixture clears both current-source FKs to the legal null shape and keeps status/body equal; it does not rerun retention. The health fixture proves a newer degraded health slot may outlive an older promoted source, an older result cannot overwrite it, and missing health fails closed through Store and fixed HTTP 503 without identity.

The fenced-finalize matrix uses current slot A, newer persisted health slot C, and an intermediate finalized slot B where A < B < C. B is retained as `stale_poll` evidence without moving health, snapshot pointers, lifecycle, or missing counts. A concurrent finalize/policy-scope transition permits only the two complete serial outcomes, and a failed observation for a never-promoted Provider creates no Provider current state, snapshot, or lifecycle row. This closes the Provider-health finalize matrix; by itself it is not the four-way concurrency proof.

Before deleting its stable target-poll set, production poll retention now explicitly locks referenced Provider current rows in `instance_id,provider` order and then account current rows in `instance_id,provider,account_key` order. This matches the Provider-before-account order used by fenced promotion and lifecycle-aware scope transition. The PostgreSQL 18 same-lineage fixture runs eligible current-source retention, a real Repository current query, a newer-slot fenced promotion, and an immediate Provider scope transition in both `retention_first` and `scope_first` orders. `pg_stat_activity` confirms the other two writers wait on row locks; while the lead transaction is uncommitted, the Repository returns the complete baseline A-present/Z-suspected-missing page within two seconds, and after its commit every participant succeeds within the bounded context without timeout or deadlock. Both orders yield retention `processed=2/deleted=4`, persisted poll/Provider-result progress `2/2`, zero old polls, one new poll/result, one scope audit, and two complete out-of-scope accounts. With retention first, the new promotion is applied, Provider/A/Z all point to the new poll, and health advances to its slot; with scope first, the new result is `policy_changed`, all three pointers are null, and health remains at old poll 2's slot. This closes 6.9 only; the broader fault matrix (9.4) and the full-repository race gate (9.6) remain open, and no latency or throughput SLO is inferred.

## Coverage and failure classification

| Item | Contract |
|---|---|
| Numerator | Sum of `promotion_applied_count` across all expected Provider segments. |
| Denominator | Sum of aligned expected slots in the active intersection, including missing slots and abandoned runs. |
| Excluded from numerator | Transport/contract failure, incomplete or degraded Provider, promotion skipped, `policy_changed`, stale/out-of-scope result, missing slot, and abandoned run. |
| Ratio | `sum(applied) / sum(expected)`; never the mean of segment ratios. |
| Threshold | `9500` basis points (`>= 0.95` is `complete`, otherwise `partial`); fixed per persisted row and not runtime-configurable. |
| Zero denominator | No false Provider rollup is created for a no-slot interval; coverage is omitted rather than invented. |
| Publication | Only the most recent retained `completed` final Node/Provider rollup; no date/policy label and no segment duplicate series. |
| Incomplete evidence | Any missing/non-completed expected segment blocks final publication. |

Failure handling has two dimensions and must not persist or expose a raw database/network error:

| Dimension | Closed contract |
|---|---|
| Recovery phase | Compaction `failed_from` is exactly `pending|summarized|deleting`; it controls whether summarize is legal or deletion-only recovery is mandatory. Daily rollup retries only fixed recoverable reasons and only from immutable segments. |
| Data-integrity failures | At minimum include the specified cross-day, source count/checksum, segment completeness/count/checksum, and activation-consistency classes. They retain evidence and block destructive successors. |
| Runtime/dependency failures | Statement timeout, lost/expired lease, PostgreSQL/connection unavailability, permission/contract failure, and unknown internal failure are projected to a reviewed fixed class; raw errors and SQL parameters are discarded. |
| Unknown commit result | Re-read durable state and unique rows; do not guess success and do not create a second truth. |
| Metrics/log/audit projection | Fixed phase/status/reason/result only. Compaction and rollup each have a separate closed eight-reason dictionary; Control top-level history failure logs have exactly three fixed reasons and no raw error field. |

The exact actor-null history-audit allowlist has twelve phases: `summarize`, `snapshot_delete`, `complete`, `fail_pending`, `fail_summarized`, `fail_deleting`, `rollup_complete`, `rollup_fail_pending`, `retention_poll`, `retention_rollup_rows`, `retention_rollup_run`, and `retention_compaction_run`. Every accepted event has the fixed system request ID and exactly four detail keys: `instance`, `summary_date`, `phase`, and non-negative `row_count`. The schema rejects unknown detail keys, identity/checksum fields, non-null actor/target/fingerprint/reason, and every mismatched action/result/phase/row-count shape. Per-phase catalog assertions place the state mutation, exact audit gate, and audit insert in the same production function and transaction; representative audit-insert failures for summarize, rollup finalize, poll retention, and rollup-run retention prove that their corresponding mutation rolls back rather than committing without its audit.

The PostgreSQL boundary fixture retains valid history events just before, exactly at, and just after the database-clock 180-day cutoff after all four cleaners run. It also proves owner `UPDATE`, `DELETE`, and `TRUNCATE` remain denied with `42501`. This establishes an immutable minimum 180-day boundary and that this change owns no audit cleaner; it does not claim automatic deletion on day 181.

The fixed compaction dictionary is `source_day_mismatch`, `source_count_mismatch`, `source_checksum_mismatch`, `activation_inconsistent`, `statement_timeout`, `lease_expired`, `database_unavailable`, and `internal`. The fixed rollup dictionary replaces the three source reasons with `segment_incomplete`, `segment_count_mismatch`, and `segment_checksum_mismatch`, retaining activation plus the same four runtime reasons. Cross-machine reasons fail closed at the Store boundary. At the process boundary, the only top-level history failure-log reasons are `service_stopped`, `runtime_stopped`, and `shutdown_timed_out`; decoded JSON assertions require the fixed component/message and forbid both a raw marker and an `error` field.

This evidence closes 2.11 and 8.3 only; 1.5 and 8.4 are closed separately by the dual canary gate below.

## Permission and compatibility boundary

| Principal/path | Allowed | Forbidden |
|---|---|---|
| Migration owner | Add forward schema, constraints, triggers, fixed functions and safe non-identity Provider-health backfill | Generate summaries/runs, forge or alter retired-day cutoffs, delete source, rewrite poll/promotion/lifecycle, copy account identity, introduce an extension |
| Runtime role | `EXECUTE` on the exact schema-qualified compatibility, planner, compaction claim/renew/reconcile/summarize/delete/complete/fail, daily-rollup claim/renew/reconcile/finalize/fail, four ordered retention, and metrics functions; existing minimal poll reads/functions. | Arbitrary history/snapshot/identity table `SELECT`, any direct table DML/TRUNCATE, owner/gate impersonation, arbitrary historical rebuild/delete |
| Function owner | Execute fixed `SECURITY DEFINER` functions with fixed `search_path`, UTC, row locks, dependency checks and exact transaction-local gate | Unchecked owner DML, caller-controlled SQL/search path, persistent or reusable deletion gate |
| Product API/UI | Existing current query with unchanged DTO/route; legitimate null current-source FK supported by denormalized current fields | History API/page/export, manual compaction/rebuild/delete, new mutation route |
| Old binary | Start and continue existing poll/promotion/current query against forward schema without touching history | Requirement to know a generic history job kind or mutate new history tables |
| Other database/API principals | Existing unrelated privileges only | Start, inspect, rebuild, update, truncate or delete history |

Compatibility failure disables only the history runner and leaves poll, lifecycle, current query, Gateway and Relay Node behavior intact. Production rollback stops history and uses the old binary on retained forward schema; production does not run down. An isolated down is legal only when no history row, retired-day cutoff, deletion progress, or later dependency exists.

## Sensitive-data flow and sink allowlist

```text
existing protected snapshot identity columns
    ├─ account_key ──> protected account segment ──> protected account final rollup
    └─ aggregate/provider evidence ──> provider segment ──> provider final rollup

protected run identity + canonical aggregate fields ──> checksum columns in protected run rows

aggregate state/counts only ──> low-cardinality metrics
fixed phase/result + bounded row count ──> actor-null immutable history audit
fixed component/action/result/reason only ──> structured log
```

| Data class | Allowed locations | Forbidden locations |
|---|---|---|
| Normalized email | Existing protected snapshot/lifecycle/current-query storage and existing authenticated, audited current response only | Every new summary/rollup/run table; history audit; metrics; ordinary logs/errors; SQL parameter logs; acceptance artifacts |
| Account key | Existing protected identity tables; protected account segment/final identity columns | Provider history; product history output; metrics/labels; audit details; ordinary logs/errors; artifacts |
| Summary date | Protected summary/run identity and actor-null history audit allowlist | Prometheus labels and unbounded ordinary logs |
| Policy, poll, compaction/rollup run and fencing identity | Protected source/run rows and controlled-function parameters | Metrics, ordinary logs/errors, audit details, artifacts |
| Checksum and canonical encoding | Protected run checksum columns and in-transaction comparison | Metrics, logs/errors, audit details, product output, artifacts |
| Instance/provider | Protected rows; bounded audit instance; approved low-cardinality coverage labels | Unapproved query-log labels or identity-bearing free text |
| Secret, endpoint, response/header/body, Node version/commit, raw error, SQL parameters | Existing protected runtime locations only where already required; none are inputs to history summaries | All history rows except no such field is defined; all metrics/log/audit/error/artifact sinks |

The canary matrix assigns distinct values to endpoint/IP, Secret reference/value, normalized email, account key, response body/header, run/fence/checksum, raw error, SQL parameter, and poll/policy identity. Its PostgreSQL 18 branch drives a partial compaction through rollup and retention and independently covers zero-data, permission, statement-timeout, and backend-reconnect returns. The database scan permits account key only in protected account segment/final identity columns and policy/run/fence/checksum only in their existing protected identity/proof columns; every other non-identity column across the seven history tables, all history-audit details, and Repository-safe returns/errors must have zero forbidden hits.

The independent local branch covers exactly `success`, `zero_data`, `partial`, `permission`, `timeout`, `restart`, and `cleanup_failure`. Each of the seven scenario artifacts is re-read to require only fixed scenario/result output, while the eighth artifact contains only the fixed local marker; the bounded scanner then checks the complete artifact directory for all canaries. The safety runner emits `local_sink_canary=covered scenarios=7`; the PostgreSQL runner emits `sensitive_canary_database_sinks=covered`; only the aggregate `all` path, after both runners succeed, emits `sensitive_canary=covered`.

History reads only already committed Control PostgreSQL data. Its expected external request count is always Node `0`, Gateway `0`, Prometheus `0`, internet `0`, and model data plane `0`. Metrics are exposed to Prometheus pull; the history calculation itself does not call Prometheus.

The canary evidence closes 1.5 and 8.4, not 8.6. Static absence of direct network imports and zero sensitive-value hits do not measure actual requests; fake network counters remain the required independent proof.

## Explicit non-goals and zero-change checks

| Excluded capability | Required zero-change proof |
|---|---|
| History product API, route, page, export or account history query | OpenAPI generated diff `0`; route and DOM negative tests; existing current endpoint response schema unchanged. |
| Manual rebuild, force-delete, retry/promotion or state mutation control | No HTTP handler/UI control; no runtime table DML privilege; controlled functions are scheduler-internal only. |
| Alert rules/routes and automatic historical recovery decisions | Alert configuration diff `0`; this change only provides completed final-rollup inputs. |
| HMAC `account_id` and account-level Prometheus series | No metric-secret config and no account identity label. |
| 10-minute/1-hour product trend or long-term archive | No trend query/page; partial days remain excluded; 30-day history is not silently extended. |
| Current lifecycle retention | No deletion of current account, missing, or out-of-scope rows. |
| Cross-Node duplicate detection | No Gateway summary dependency or cross-Node account grouping. |
| Gateway, Node, Driver or model request-path changes | Repository diff limited to Control; fake network counters remain zero; independent data-plane isolation gate required later. |
| Poll period, promotion/lifecycle semantics or backfill | Existing fixed five-minute poll and promotion gates remain; Migration does not create history truth or rewrite current truth. |
| Generic durable-job registration or Redis dependency | Production durable-job catalog remains unchanged/empty; history correctness uses its dedicated PostgreSQL state machines. |
| Multi-Control coordination | No leader election or distributed lock; supported topology remains one Control per isolated environment database. |
| Destructive production down | Rollback retains forward schema and existing summaries/runs; protected down is isolated-environment verification only. |

## Implemented evidence and remaining work

The current candidate has bounded evidence for the additive Migration 9 schema and ACLs; compatibility/planner, compaction claim/renew/reconcile/summarize/bounded-delete/complete/fail, daily-rollup claim/renew/reconcile/finalize/fail, and metrics functions; deterministic UTC eligibility and aligned-slot planning; normal-horizon plus source/existing-lineage bootstrap planning; durable retired-day cutoff; coverage basis points; versioned canonical chained SHA-256; account reset aggregation; and both pure state models. The PostgreSQL compaction path atomically commits both segment families, source counts/checksum, run state, and summarized audit; each snapshot batch atomically commits `DELETE ... RETURNING`, actual accumulated count, run phase, and audit; completion checks persisted checksum, zero remaining snapshots, and source/deleted conservation. The daily-rollup path waits for completed expected segments, computes account and Provider final rows, and atomically commits both families, full-field segment checksum/counts, completed run, and audit. History audit insertion has its own exact transaction-local gate, rejects direct runtime or owner forgery, excludes protected identities/checksums, and is cleared with the other transaction-local gates.

The Store adapter exposes only fixed, schema-qualified function calls and fixed/redacted errors for both run types and all four retention stages. Repository loops implement bounded planner/compaction-worker/rollup-worker/reconciler/retention execution, renew before destructive or publishing phases, `failed_from` compaction resume without re-aggregation, bounded persisted-state replay for unknown summarize/delete/complete/finalize outcomes, database backoff, and claim-first shutdown. An expired active compaction lease is not worker-claimable: the Reconciler must first persist `lease_expired`, the exact `failed_from`, and clear the old ownership before a new claim restores that phase with a fresh fence. The ordered retention scanner does not immediately replay an unknown delete commit; it waits for the next scan so PostgreSQL can select from durable state. Compaction, rollup, and retention transactions share one configured total concurrency limit rather than receiving independent pools. A shared fatal signal stops the planner and all other new worker/retention transactions on state, day, count, checksum, activation, or repository inconsistency. The originating worker may execute exactly one fenced terminal-fail transaction to persist the fixed failure reason; other already-running bounded transactions are only allowed to drain.

PostgreSQL 18 acceptance covers isolated `up/down/up`, schema and core SHA-256 checks, PostgreSQL-to-Go source and full-field segment checksum goldens, additive-column stability, restricted-role and gate-forgery smoke, the compaction main path/recovery, atomic daily final-rollup publication/idempotency, the bounded retention/current-query path, the metrics backlog blind spots, the sensitive database-sink matrix, and the exact Migration-8 legacy bootstrap/retired-day non-resurrection path described above. Its summarize recovery evidence injects a database statement timeout, a pre-commit backend disconnect, and a post-commit connection loss: uncommitted work leaves the run and all segments/audits unchanged, while committed work remains one immutable summary and an exact same-fence read does not recalculate it. Runtime tests map timeout and database unavailable to their fixed recoverable reasons, leave commit-unknown without a guessed failure transition, and route both pending and `failed_from=pending` through summarize. The expired-lease phase matrix creates pending, summarized, and deleting claims, proves a worker cannot claim any after expiry but before reconciliation, verifies one exact `fail_<phase>` transition per run, then reclaims each with attempt 2 and a new fence. All three converge with unchanged source proof and exactly one run, Provider segment, summarized audit, and completed audit. Go tests cover rollup and retention mapping/redaction, failure recovery, stale fencing, bounded unknown-finalize replay, retention unknown-commit rescan, shutdown, shared total concurrency, strict metrics shape/cardinality, local sink canaries, and production Control wiring. History is now wired into `cmd/control/main.go`: disabled startup still probes compatibility and exports only truthful available metrics, while enabled startup constructs the mutation loops and participates in bounded shutdown. A valid independently observed runtime state also isolates a later metrics-provider failure: only enabled/reason remains, database-derived history families are omitted, the shared registry still serves HTTP 200, and the raw provider error is not projected. This is still not the full crash/concurrency/capacity/external-request/fault-injection acceptance matrix. The million-row observation is diagnostic and establishes no RSS or capacity threshold. History remains default-disabled and unsupported for production enablement until the remaining gates pass.

The real-process acceptance builds the candidate `cmd/control`, applies Migration 9 to isolated PostgreSQL 18, verifies default-disabled compatibility and database-derived metrics, revokes the history metrics function to prove process-wide scrape isolation, restores the grant, then runs an enabled zero-poll policy/monitoring day through planner, compaction, and final rollup. With `CONTROL_DATABASE_MAX_CONNS=1`, an owner transaction takes an exclusive Provider-summary lock; `pg_stat_activity` proves the sole Control connection waits inside the production summarize function, while a bounded metrics request proves the shared pool is exhausted without killing Control. In the drain branch, SIGTERM stops new claims, releasing the lock within grace commits only the entered summarize transaction, and an immediate restart preserves its unexpired owner/fence/attempt; after database-clock expiry, Reconciler alone clears the old fence and the run completes at attempt 2. In the timeout branch, the same held SQL reaches its fixed statement timeout, leaves no summary/rollup or success audit, atomically records only `failed_from=pending/statement_timeout`, and exits the real PID normally within the bound. A separate fixture leaves active twenty-second leases on consecutive days in pending, summarized, and deleting across Control and PostgreSQL restart; all three likewise require Reconciler and converge once with attempt 2. The runner scans logs for dynamic secrets and connection material and leaves zero project containers, volumes, networks, temporary directories, or locks. This closes 7.4 without claiming the broader 9.4 fault matrix or 9.6 race gate.

## Review completion rule

This crosswalk supports completion of 1.1, 1.5, 1.6, 2.11, 8.3, and 8.4 only with their cited executed gates. It does not complete 8.6 or any unrelated schema/ACL, UI/OpenAPI, capacity, fault-injection, or release task without separately executed evidence.
