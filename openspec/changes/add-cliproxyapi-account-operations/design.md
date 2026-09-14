## Context

Phase 7 is the first Relay Station phase that invokes remote credential-side mutation. The current architecture candidate uses upstream CLIProxyAPI native management behavior rather than the historical Stage 7N Relay-specific protocol. Control owns command/replay, authorization, ingress protection, durable serialization, conservative outcome state, lifecycle blocking/override and Inventory verification. CLIProxyAPI owns credential schema, auth-file persistence, runtime synchronization, refresh, Provider/Account selection, retry and cooldown.

Native baseline:

```text
CLIProxyAPI release = v7.3.2
exact tag commit = 7fa443dc8bf8ca2f1ffd81c2472deb31b097b697
Phase 7 Relay-specific Node mutation protocol = ZERO
```

Historical Stage 7N revision `72c435b1b1b85b341a734e3860081c7782d9cbd2` and image `sha256:c5d2cc476c5c99cff994528920151c3ecee0f37832ba82943b8b54ab7d9610c4` remain review evidence, but are `HISTORICAL / SUPERSEDED CANDIDATE / NOT CURRENT IMPLEMENTATION DEPENDENCY / NOT CURRENT DEPLOYMENT BASELINE`.

## Goals / Non-Goals

### Goals

- explicit Antigravity Disable, Enable, Remove, Upload New and Replace Existing;
- business identity `(node_instance_id,account_key)` with fresh exactly-one native target resolution;
- bounded safe use of an exact native management API subset;
- no Secret persistence or raw native response retention in Control;
- global command/actor-first exact replay through archived Change A;
- durable same-account Control serialization and Node-first lifecycle locking;
- conservative `outcome_unknown` with no automatic mutation redispatch;
- explicit audited lifecycle-block override;
- normal Inventory business convergence only.

### Non-Goals

No Relay-specific Node mutation protocol, CAS, ETag, target incarnation, postcondition proof, automatic quiescence proof, OAuth/Re-auth, automatic repair/move/remove, batch/all, credential vault, scheduler ownership, special Inventory truth, Gateway mutation or generic workflow framework.

## Decisions

### 1. Exact native management API subset

The adapter MUST call only:

```http
GET /v0/management/auth-files

PATCH /v0/management/auth-files/status
Content-Type: application/json
{"name":"<exact native basename>","auth_index":"<current native auth_index>","disabled":true|false}

DELETE /v0/management/auth-files?name=<urlencoded exact native basename>

POST /v0/management/auth-files?name=<urlencoded exact controlled basename>
Content-Type: application/json
<one bounded credential JSON object>
```

Control MUST NOT call `DELETE all=true`, multi-name/body delete, multipart native upload, `PATCH /auth-files/fields`, `GET /auth-files/download`, `POST /auth-files/refresh`, OAuth endpoints or any arbitrary management passthrough. The management key remains server-side memory/config only and never reaches browser, PostgreSQL, receipt, audit, log, trace or metric. Transport remains HTTP-only, fixed-target, no redirect, no proxy/fallback/retry, bounded response/body/time and sanitized error mapping.

### 2. Safe native snapshot projection

`GET /v0/management/auth-files` is untrusted management input. The adapter MUST parse a bounded response and immediately produce only:

```text
provider/type
normalized email
name
auth_index
disabled
```

`name` MUST be a validated basename and `auth_index` a bounded opaque string. Full path, ID token, status/status_message, unavailable/runtime_only/source, success/failure counters, recent requests, quota/model quota, cooldown/next-retry data, timestamps, project/routing metadata, headers, proxy, notes, token-like values, unknown fields and the raw object are discarded. Raw native bytes/object MUST NOT enter PostgreSQL, receipts, audit, logs, traces, metrics or Control API/browser responses.

### 3. Fresh exactly-one target resolution

Control business identity remains:

```text
account_key = lowercase(trim(provider)) + ":" + lowercase(trim(email))
target = (node_instance_id, account_key)
```

Disable, Enable, Remove and Replace Existing MUST obtain a fresh native snapshot immediately before dispatch and match normalized provider plus normalized email. Zero matches returns `account_target_not_found`; more than one returns `account_target_ambiguous`; exactly one yields ephemeral `name/auth_index`. Control MUST NOT choose first/latest, infer a filename, or use Inventory alone as physical target truth. Native name/auth_index MUST NOT be persisted or exposed as business identity.

Upload New also obtains a fresh snapshot and requires the expected identity to be absent before native POST. This is a best-effort admission observation, not atomic create-if-absent.

### 4. Native mutation semantics and accepted races

Node mutation semantics are native last-writer-wins. Change B uses no CAS, target revision, ETag, If-Match, target incarnation or compare-and-swap.

Upload New:

```text
fresh snapshot absent
-> native POST to generated controlled basename
-> best-effort create
```

There is no atomic create-if-absent guarantee. A concurrent native/external writer may create the target after the snapshot and before POST; the native POST may overwrite it. Phase 7 v1 accepts this race.

Replace Existing:

```text
fresh exactly-one target
-> validate/inherit exact native basename
-> native POST
-> best-effort replace / last-writer-wins
```

Native credential refresh may occur between snapshot and POST, and the administrator replacement may overwrite newer credential state. This lost-update risk is accepted. Remove is best-effort native single-file delete without compare-and-delete.

### 5. Credential ingress, schema ownership and filename admission

CLIProxyAPI remains credential schema truth. Control validates only a valid top-level JSON object, `type=antigravity`, present email, normalized email equal to expected account identity, and a credential body of at most 1048576 bytes. The Control public multipart aggregate limit is 1073152 bytes: one `request` JSON part at most 8192 bytes, one credential part at most 1048576 bytes and at most 16384 bytes of multipart framing. This 1 MiB credential value is Control ingress protection, not a Node protocol or provider schema limit.

Pinned-v7.3.2 runtime/routing/management denylist is:

```text
disabled
weight
priority
headers
request_retry
request-retry
excluded_models
excluded-models
proxy_url
note
websockets
prefix
models
disable_cooling
fingerprint_profile
```

Any occurrence is `upload_invalid`; Control MUST NOT silently strip it. New provider credential fields not in this denylist may pass through to CLIProxyAPI validation. This does not make Control provider credential schema truth.

Upload New generates `antigravity-<normalized_email>.json`. `MAX_NATIVE_BASENAME_BYTES=255`, fixed prefix/suffix is 17 bytes and `MAX_CREATE_EMAIL_BYTES=238`, all measured as UTF-8 bytes. Oversize input returns `invalid_request` with zero Node mutation; no truncation, hash fallback or alternate filename is allowed. Replace inherits the fresh exact basename and requires non-empty basename-only UTF-8, no separator/traversal/control character and at most 255 bytes.

### 6. Product API shape

Protected routes remain:

```text
POST /api/account-operations/disable
POST /api/account-operations/enable
POST /api/account-operations/remove
POST /api/account-operations/upload-new
POST /api/account-operations/replace-existing
GET  /api/account-operations/{command_id}
POST /api/account-operations/{command_id}/lifecycle-override
```

Disable/Enable JSON bodies contain exactly lowercase UUID `command_id`, UUID `node_instance_id` and canonical `account_key` (maximum 385 UTF-8 bytes). Remove additionally requires `confirmation="REMOVE"`. Upload routes accept exactly one `request` JSON part with those three fields and one credential part under Decision 5. Lifecycle override contains exactly `reason`, `confirmation="OVERRIDE UNKNOWN OPERATION LIFECYCLE BLOCK"` and optional audit-only `detail` up to 512 UTF-8 bytes.

Unknown JSON fields, duplicate multipart parts, a missing part, any extra part and malformed UUID/account identity are `400 invalid_request`. The credential part is sent to CLIProxyAPI as the exact validated JSON bytes; browser input never supplies native basename, auth_index, Management Key or final path.

POST mutation routes require active authenticated `super_admin`, same-origin and CSRF. GET is authenticated/read-only. Override has the same checks plus typed high-risk confirmation. Every response, including errors, is `Cache-Control: no-store`. Public operation projection contains exactly `command_id,node_instance_id,account_key,operation_kind,execution_state,verification_state,result,error_code,lifecycle_overridden,lifecycle_override_reason,created_at,updated_at`; it excludes native name/auth_index, Management Key, upload fingerprint, raw response and credential.

Terminal success returns exactly `200 {"operation":<projection>}`. `prepared|dispatched|outcome_unknown` returns exactly `202 {"operation":<projection>}`. A terminal mapped failure returns `{"error":{"code":"<stable-code>","message":"<bounded-sanitized-message>"},"operation":<projection>}` at the mapped status. GET returns `200 {"operation":<current-projection>}` or `404 operation_not_found`. Same-command nonterminal replay returns the same current-projection shape with 202 and zero redispatch. Exact terminal replay returns the original persisted status and canonical body bytes. Override success returns `200 {"operation":<current-projection>}` and does not change execution or verification state.

The public status mapping is frozen as follows:

```text
400 invalid_request | upload_invalid | identity_mismatch
401 authentication_required
403 authorization_required | csrf_failed
404 node_not_found | account_target_not_found | operation_not_found
409 node_retired | node_monitoring_ineligible | unsupported_provider
409 account_target_ambiguous | account_operation_in_progress | command_conflict
413 upload_too_large
503 node_management_unavailable | service_unavailable
```

An accepted request whose native outcome is ambiguous is not returned as an error envelope: it returns the 202 operation projection with `execution_state=outcome_unknown`, `result=null` and `error_code=remote_outcome_unknown`. Verification fields in every operation projection are current mutable truth and remain independent of the immutable terminal POST receipt.

### 7. Durable operation schema

The additive migration candidate creates one row per accepted command:

```text
command_id uuid PRIMARY KEY FK admin_command_registry(command_id)
node_instance_id uuid NOT NULL FK relay_node_assets(instance_id)
account_key text NOT NULL
operation_kind text CHECK IN ('disable','enable','remove','upload_new','replace_existing')
execution_state text CHECK IN ('prepared','dispatched','remote_applied','remote_noop','remote_partial','outcome_unknown','failed')
verification_state text CHECK IN ('not_started','pending','verified','timeout','inconclusive')
dispatch_started_at timestamptz NULL
remote_result_code text NULL
upload_fingerprint_key_version smallint NULL
upload_intent_fingerprint bytea NULL CHECK length=32
verification_started_at timestamptz NULL
verification_deadline timestamptz NULL
verified_at timestamptz NULL
lifecycle_override_at timestamptz NULL
lifecycle_override_by uuid NULL FK administrator_users(id)
lifecycle_override_reason text NULL CHECK IN ('process_restarted','node_stopped','risk_accepted')
created_at timestamptz NOT NULL
updated_at timestamptz NOT NULL
```

The three override fields are all-null or all-non-null. Free-form override detail is audit-only. The current design has no `target_precondition`, `dispatch_token`, `remote_mutation_deadline`, `quiescence_deadline`, `remote_quiesced_at`, `write_token` or `postcondition_proof`. No native filename/path/auth_index/raw body/Management Key/credential is stored. Runtime roles receive no unrestricted DML; controlled functions enforce state shape and monotonic transitions.

### 8. Global command and terminal receipt

After auth/session/super_admin/CSRF, Control acquires the shared UUID-derived advisory serialization and performs actor-first global registry lookup before target, upload Secret parsing/fingerprinting or Node calls. New reservation and operation creation are atomic. Cross-actor/domain/kind/intent reuse is `command_conflict`.

Change B adds immutable `account_admin_command_receipts`, separate from asset-only receipts. `remote_applied`, `remote_noop` and `failed` are receipt-eligible. `prepared`, `dispatched` and `outcome_unknown` are not. `remote_partial` remains in the generic enum for a future machine-stable protocol, but the v7.3.2 adapter MUST NOT manufacture it from ambiguous 5xx/raw text; if no stable evidence exists, state is `outcome_unknown` and there is no receipt.

A failure before global acceptance creates no operation/receipt. A stable terminal transition, terminal audit and receipt insertion commit atomically before response. Exact same actor/domain/kind/intent terminal replay returns stored HTTP status and canonical response bytes. Verification and lifecycle override changes MUST NOT rewrite that immutable original POST response.

### 9. Durable same-account dispatch serialization

In-memory mutex is insufficient. Dispatch authorization uses PostgreSQL durable state and the Node-first order:

```text
BEGIN
lock Node
-> require lifecycle_status=active
-> lock/read monitoring under existing Node-first graph
-> require current non-cancelled monitoring
-> require management_account_inventory_read + matching active Provider policy
-> acquire/check durable (node_instance_id,account_key) serialization
-> reject another operation in dispatched or unresolved outcome_unknown without override
-> lock current operation
-> require execution_state=prepared
-> set dispatch_started_at and prepared -> dispatched
COMMIT
-> perform one native HTTP request outside transaction
```

No DB lock crosses native HTTP. Same-target races, Control restart and lifecycle races are resolved from durable rows/constraints, not process memory. There is no Relay mutation capability discovery and no Node mutation protocol constant gate; deployment compatibility evidence is pinned upstream v7.3.2 plus the future reviewed Control adapter artifact.

### 10. Conservative native outcome mapping

```text
known successful terminal native 2xx -> remote_applied or remote_noop
provably pre-mutation mapped native 4xx -> failed
timeout / connection loss / response loss -> outcome_unknown
ambiguous native 5xx after request may have arrived -> outcome_unknown
```

Control maps only adapter-reviewed status/context and never parses raw native error strings to infer filesystem/runtime commit stage. Native 500 MUST NOT become `remote_partial`. Read-back and Inventory may show business convergence but MUST NOT retroactively invent exact execution evidence. Every mutation kind follows this rule. Automatic redispatch of `dispatched` or `outcome_unknown` is prohibited, including same-command POST replay and restart recovery.

### 11. Node lifecycle blocking and manual override

Retire/Replace locks the same Node first, then inspects same-Node account operations. `dispatched` and unresolved `outcome_unknown` block lifecycle unless that exact operation has a durable reviewed override. There is no automatic quiescence proof or deadline expiry release.

The action name is **Override Unknown Operation Lifecycle Block**. It sets only `lifecycle_override_at/by/reason`; it does not change execution state, verification, receipt eligibility or redispatch authority. `outcome_unknown` remains unknown. Reasons are `process_restarted|node_stopped|risk_accepted`. `risk_accepted` explicitly waives the guarantee that a previously dispatched request can never mutate the old Node after lifecycle proceeds. All reasons require super_admin, active session, same-origin/CSRF, typed confirmation and distinct high-risk audit; operator detail is audit-only.

Node-first lock order is mandatory for dispatch and lifecycle. Acceptance covers Retire-first vs dispatch, dispatch-first vs Retire, same-account A vs B and Control restart with a live operation.

### 12. Verification

`execution_state` and `verification_state` remain independent. After execution is verification-eligible, Control only wakes/requests the existing UTC 300-second fixed-slot scheduler. It never creates off-grid/special runs or patches current Inventory.

- Disable: same account_key with `disabled=true`.
- Enable: same account_key with `disabled=false`.
- Remove: absence from fresh complete eligible provider-complete evidence.
- Upload New: expected account identity appears.
- Replace Existing: expected account identity remains present.

Inventory proves business convergence only. It cannot prove credential bytes, CAS, native request quiescence or that an old HTTP request can no longer execute. Verification deadline remains ten minutes; stale/incomplete/disk-fallback/duplicate evidence cannot prove success.

### 13. Secret, audit, metrics and errors

Control keeps credential bytes in bounded memory only and stores only the versioned keyed upload-intent fingerprint under `CONTROL_ACCOUNT_OPERATION_INTENT_KEY_FILE`; asset K1 is unchanged. Credential, Management Key, raw native body, full path and ephemeral target evidence never enter DB, receipt, response, audit, logs, traces or metrics.

Audit records actor/request/command/operation/Node/provider/protected business identity, sanitized outcome/verification and high-risk lifecycle override. Metrics use low-cardinality operation/provider/result/error/execution/verification classes and never email/account_key/command/node/path.

Stable Control errors include `invalid_request`, `node_not_found`, `node_retired`, `node_management_unavailable`, `node_monitoring_ineligible`, `unsupported_provider`, `account_target_not_found`, `account_target_ambiguous`, `account_operation_in_progress`, `command_conflict`, `upload_too_large`, `upload_invalid`, `identity_mismatch`, `remote_outcome_unknown`, verification errors and `service_unavailable`. Raw native messages are never relayed.

### 14. Compatibility, supersession and rollout

Stage 7A remains satisfied at migration 37 and class/floor 3/3. Native-First implementation compatibility class/floor is assigned only with reviewed Control artifact/schema evidence. Forward schema/receipts remain preserved on rollback.

No Node revert occurs in this planning change. Historical Stage 7N results remain true historical evidence while their protocol is superseded as the current Change B dependency. Any future Node alignment uses ordinary reviewed commits, never history rewrite or force push. The Ops supersession ADR remains PROPOSED until independent Native-First architecture re-review passes.

## Acceptance Strategy

Future acceptance MUST cover exact native route allowlisting; snapshot Secret/raw-field rejection; 1 MiB boundaries; denylist pass/reject vectors; 238/239-byte filename boundary; best-effort create/replace and accepted lost-update races; ambiguous 5xx/timeouts to outcome_unknown; zero automatic redispatch; terminal receipt eligibility; PostgreSQL same-account serialization; Node-first lifecycle races/restart; all override reasons and high-risk audit; Inventory convergence; Secret scans; pinned v7.3.2 adapter tests; API/UI; and compatibility rollback.

## Planning history and current gate

Historical Stage 7N contract/design/implementation reviews, corrective amendments and artifacts are preserved in Ops. They are not current Stage 7B dependencies and are not the current deployment baseline.

```text
Native-First Corrective Round 1
P0 = 0
P1 = 0 candidate
P2 = 0 candidate
Architecture status = READY FOR INDEPENDENT ARCHITECTURE RE-REVIEW
Stage 7B implementation = NOT STARTED
```

This candidate does not declare Architecture Review PASS, Detailed Requirements FROZEN, implementation readiness READY or Stage 7B authorization.
