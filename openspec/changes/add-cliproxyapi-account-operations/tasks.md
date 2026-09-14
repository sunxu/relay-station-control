## 1. Dependency and native baseline

- [x] 1.1 Record satisfied `add-global-admin-command-registry` dependency: migration 37 and compatibility class/floor 3/3.
- [x] 1.2 Pin CLIProxyAPI upstream release v7.3.2 at exact commit `7fa443dc8bf8ca2f1ffd81c2472deb31b097b697` as the reviewed native adapter baseline.
- [x] 1.3 Mark Stage 7N as historical implemented/reviewed design, superseded candidate, and neither current Change B dependency nor deployment baseline.

## 2. Control durable operation foundation

- [ ] 2.1 Add an additive migration for `account_admin_operations`, separate immutable `account_admin_command_receipts`, lifecycle-override fields, constraints, ACL, controlled writers, audit allowlists and required indexes; no raw credential, native response or physical target evidence.
- [ ] 2.2 Add Store state transitions with row locking, restart-visible execution truth and durable same-account serialization for `(node_instance_id,account_key)`.
- [ ] 2.3 Integrate global admin registry actor-first reservation and separate upload fingerprint key while preserving existing asset K1 and canonical asset intents.

## 3. Native CLIProxyAPI adapter

- [ ] 3.1 Implement the fixed v7.3.2 native allowlist only: auth-files GET, exact status PATCH, single-name DELETE and raw-JSON POST; reject all-delete, multi-delete, native multipart, fields/download/refresh/OAuth and arbitrary passthrough.
- [ ] 3.2 Require exact-once bounded runtime headers whose values match independently frozen final artifact metadata; do not infer `v` prefixes, short-SHA expansion or expected values from the upstream source tag; mismatch returns `unsupported_node_version` with zero mutation.
- [ ] 3.3 Classify transient `mutation_eligible_targets` separately from broader `occupancy_evidence`; permit only clean version-valid empty snapshots to establish Upload New absence, reject degraded/fallback evidence, then discard classification/path/token/runtime/unknown fields.
- [ ] 3.4 Resolve Disable/Enable/Remove/Replace from a fresh exactly-one provider+normalized-email match; return stable missing/ambiguous errors and keep name/auth_index ephemeral.
- [ ] 3.5 Keep Management Key server-side only and use a fixed HTTP-only bounded client with no redirect, proxy, retry or raw native error exposure.

## 4. Credential ingress and filename admission

- [ ] 4.1 Bound credential ingress to 1 MiB and request metadata to the frozen multipart envelope; reject malformed, duplicate and oversized parts before native dispatch.
- [ ] 4.2 Validate a top-level JSON object, `type=antigravity`, expected normalized email and the complete alias-canonicalized v7.3.2 runtime-control denylist while allowing unrecognized provider credential fields to pass through unchanged.
- [ ] 4.3 Generate Upload New basename `antigravity-<normalized_email>.json` with 255-byte component and 238-byte normalized-email limits, apply the shared safe-basename validator, and require both identity and basename absence; Replace inherits and validates the exact fresh native basename.
- [ ] 4.4 Implement the exact canonical account intent v1 arrays and `CONTROL_ACCOUNT_OPERATION_INTENT_KEY_FILE` HMAC contract, including hard-coded golden vectors and deterministic wrong-key replay, without treating native read-back as credential proof.

## 5. Dispatch, outcome and lifecycle serialization

- [ ] 5.1 In a short Node-first transaction require active lifecycle, current monitoring eligibility, Inventory-read capability, provider policy and durable same-account availability; transition `prepared -> dispatched`, then perform native HTTP outside the transaction.
- [ ] 5.2 Block new destructive dispatch for the same account while an operation is `dispatched` or unresolved `outcome_unknown`, regardless of lifecycle override; prove Retire/Replace alone may consult override and the blocker survives Control restart.
- [ ] 5.3 Decide Disable/Enable already-desired noop from the fresh eligible snapshot with zero PATCH; map every sent stable 2xx to applied, stable reviewed pre-mutation status/context to failed, and timeout/connection loss/response loss/ambiguous native 5xx to `outcome_unknown`; never invent noop, an unproven terminal stage or redispatch.
- [ ] 5.3a Map only the reviewed v7.3.2 upload POST HTTP 503-before-body-read/write (`authManager == nil`) to terminal `failed/node_management_unavailable` with receipt; keep all other unreviewed 503/5xx outcomes unknown.
- [ ] 5.4 Implement high-risk **Override Unknown Operation Lifecycle Block** as an independent globally reserved `account.lifecycle_override` command with its own canonical intent, receipt and already-set behavior; leave target execution and same-account blockers unchanged.
- [ ] 5.5 PostgreSQL 18 race acceptance: Retire-first/dispatch-first, same-account A/B, Control restart with live operation, and override behavior.

## 6. Product API, replay, UI and observability

- [ ] 6.1 Implement the five frozen mutation POST routes, operation GET and lifecycle-override POST with exact no-store schemas/statuses and bounded stable errors.
- [ ] 6.2 Implement separate immutable account terminal receipts for mutation and override command IDs: exact replay only for stable terminal classification; `outcome_unknown` returns current projection with 202 and zero redispatch; Phase 7 v1 has no extra intermediate failure state.
- [ ] 6.3 Add super-admin UI for single-account operations and high-risk lifecycle override without native physical evidence or arbitrary filename input.
- [ ] 6.4 Add bounded audit actions, Secret-safe logs and low-cardinality metrics; prove Management Key, credential bytes, native paths/responses and account identifiers do not leak.

## 7. Inventory boundary

- [ ] 7.1 Keep normal Inventory on its existing independent cadence; do not add Phase 7 verification state, scheduler wake, special run, reconciler, durable job, lease or worker.
- [ ] 7.2 Expose operation execution truth separately from Inventory observation and prove Inventory cannot terminalize, rewrite or resolve an account operation.
- [ ] 7.3 Test that later Inventory convergence leaves `remote_applied`, `remote_noop`, `failed` and `outcome_unknown` execution states unchanged.

## 8. Compatibility, regression and evidence

- [ ] 8.1 Assign the additive migration compatibility transition only with signed Control release artifacts; preserve Stage 7A registry and existing asset/monitoring behavior.
- [ ] 8.2 Run PostgreSQL 18 acceptance, native adapter tests against pinned v7.3.2, API/UI E2E, lifecycle/restart/response-loss races, Inventory regressions, `make test`, `make build`, strict OpenSpec and diff hygiene.
- [ ] 8.3 Capture implementation validation, exact Control artifact metadata, CLIProxyAPI baseline, Secret scans and independent implementation-review evidence.

## Planning gate

Native-First Simplification Corrective Round 8 incorporates the previous P0=0, P1=1, P2=1 re-review findings as P0=0, P1=0 candidate, P2=0 candidate. Architecture status is **READY FOR INDEPENDENT ARCHITECTURE RE-REVIEW**. ADR is **PROPOSED**, runtime artifact identity is **NOT YET FROZEN**, Node revert is **NOT RUN**, and implementation is **NOT STARTED**. This task list does not authorize implementation.
