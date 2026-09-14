## Context

Phase 7 is the first Relay Station phase that invokes remote credential-side mutation. The current architecture candidate uses upstream CLIProxyAPI native management behavior rather than the historical Stage 7N Relay-specific protocol. Control owns command/replay, authorization, ingress protection, durable serialization, conservative execution state and lifecycle blocking/override. Normal Inventory remains an independent business observation surface. CLIProxyAPI owns credential schema, auth-file persistence, runtime synchronization, refresh, Provider/Account selection, retry and cooldown.

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
- normal Inventory independent business observation only.

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

### 2. Runtime artifact gate and two-stage snapshot classification

`GET /v0/management/auth-files` is untrusted management input. Before parsing or interpreting its body, the adapter MUST require exactly one bounded `X-CPA-VERSION` and exactly one bounded `X-CPA-COMMIT`. Each value MUST match the independently frozen final runtime artifact header byte-for-byte; no `v` prefix normalization, short-SHA expansion or source-tag inference is permitted. Missing, duplicate, malformed, unknown or mismatched values return `unsupported_node_version` with zero mutation.

The semantic upstream source baseline is release `v7.3.2` at `7fa443dc8bf8ca2f1ffd81c2472deb31b097b697`; it is distinct from runtime artifact identity. Before Stage 7B implementation readiness, release evidence MUST build the final reviewed artifact, observe and record its exact version/commit headers and image digest, and freeze all three values. A stock artifact may use the upstream commit; a fork artifact may emit a short build commit. Configuration or database expectation MUST NOT substitute for absent response headers. Runtime artifact identity is currently **NOT YET FROZEN**, so Stage 7B is not implementation-ready.

After the header gate, snapshot handling has two stages:

```text
raw native entry
-> transient mutation-eligibility classification
-> safe target projection
```

The adapter first performs transient classification and then safe projection. A `mutation_eligible_targets` entry must be runtime-manager-backed with exact `source="file"`, `runtime_only=false`, auth_index of 1..256 UTF-8 bytes with no control character, validated safe basename, and valid provider/email. If both provider and type are present they MUST normalize identically. Memory/runtime-only/incomplete entries are never physical mutation targets. A disk-fallback, malformed or otherwise degraded non-empty snapshot returns `node_management_unavailable` and performs zero mutation. A clean version-valid structurally valid empty `files=[]` snapshot may establish absence for Upload New only; existing-target operations return `account_target_not_found`.

Upload New uses a separate transient `occupancy_evidence` set. It conservatively includes any usable entry that proves the requested identity or generated basename is occupied, including memory, runtime-only, or incomplete-for-mutation entries. A malformed/degraded record that prevents safe occupancy classification fails closed. A clean, structurally valid, version-valid empty `files=[]` response may establish absence for Upload New only; it cannot resolve an existing target.

Only after classification does the adapter produce:

```text
provider/type
normalized email
name
auth_index
disabled
```

`source` and `runtime_only` may exist only transiently during classification. They and full path, ID token, status/status_message, unavailable, success/failure counters, recent requests, quota/model quota, cooldown/next-retry data, timestamps, project/routing metadata, headers, proxy, notes, token-like values, unknown fields and the raw object MUST be discarded and MUST NOT enter PostgreSQL, receipts, audit, logs, traces, metrics or Control API/browser responses.

### 3. Fresh exactly-one target resolution

Control business identity remains:

```text
account_key = lowercase(trim(provider)) + ":" + lowercase(trim(email))
target = (node_instance_id, account_key)
```

Disable, Enable, Remove and Replace Existing MUST obtain a fresh native snapshot immediately before dispatch and match normalized provider plus normalized email. Zero matches returns `account_target_not_found`; more than one returns `account_target_ambiguous`; exactly one yields ephemeral `name/auth_index`. Control MUST NOT choose first/latest, infer a filename, or use Inventory alone as physical target truth. Native name/auth_index MUST NOT be persisted or exposed as business identity.

Upload New also obtains a fresh version-valid snapshot. It requires both the expected provider+normalized-email identity and the generated basename to be absent from `occupancy_evidence`; existing identity returns `account_target_exists`, and another identity occupying the basename returns `account_filename_conflict`, both with zero native POST. This remains a best-effort admission observation, not atomic create-if-absent.

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
proxy-url
note
websockets
prefix
models
disable_cooling
disable-cooling
fingerprint_profile
fingerprint-profile
base_url
base-url
model_aliases
model-aliases
request_scoped_errors
request-scoped-errors
tool_prefix_disabled
tool-prefix-disabled
```

Control MUST apply the pinned v7.3.2 `CanonicalCredentialMetadataKey()` alias mapping before testing the canonical key against this denylist; canonical and legacy aliases therefore have identical disposition. Any denied occurrence is `upload_invalid`; Control MUST NOT silently strip it. `api_key`/`api-key` is classified as provider credential material rather than runtime control and is not denied solely by this list; normal identity/minimum checks and CLIProxyAPI schema handling still apply. New provider credential fields not in this denylist may pass through to CLIProxyAPI validation. This does not make Control provider credential schema truth.

Upload New generates `antigravity-<normalized_email>.json`. `MAX_NATIVE_BASENAME_BYTES=255`, fixed prefix/suffix is 17 bytes and `MAX_CREATE_EMAIL_BYTES=238`, all measured as UTF-8 bytes. The generated name MUST then pass the same validator as Replace: non-empty basename only, no `/`, `\\`, NUL, control character or traversal form, and at most 255 UTF-8 bytes. Oversize or unsafe input returns `invalid_request` with zero Node request; no truncation, hash fallback or alternate filename is allowed. Replace inherits the fresh exact basename and applies the same validator.

### 6. Product API shape

Protected routes remain:

```text
POST /api/account-operations/disable
POST /api/account-operations/enable
POST /api/account-operations/remove
POST /api/account-operations/upload-new
POST /api/account-operations/replace-existing
GET  /api/account-operations/{command_id}
POST /api/account-operations/{operation_command_id}/lifecycle-override
POST /api/account-operations/{operation_command_id}/same-account-override
```

Disable/Enable JSON bodies contain exactly lowercase UUID `command_id`, UUID `node_instance_id` and canonical `account_key` (maximum 385 UTF-8 bytes). Remove additionally requires `confirmation="REMOVE"`. Upload routes accept exactly one `request` JSON part with those three fields and one credential part under Decision 5. In each override route, the path UUID is the target operation command ID; its body contains exactly a new independent lowercase UUID `command_id`, the route-specific reason, confirmation (`OVERRIDE UNKNOWN OPERATION LIFECYCLE BLOCK` or `OVERRIDE UNKNOWN OPERATION SAME-ACCOUNT BLOCK`) and optional audit-only `detail` up to 512 UTF-8 bytes.

Unknown JSON fields, duplicate multipart parts, a missing part, any extra part and malformed UUID/account identity are `400 invalid_request`. The credential part is sent to CLIProxyAPI as the exact validated JSON bytes; browser input never supplies native basename, auth_index, Management Key or final path.

POST mutation routes require active authenticated `super_admin`, same-origin and CSRF. GET is authenticated/read-only. Both overrides have the same checks plus typed high-risk confirmation. Every response, including errors, is `Cache-Control: no-store`. Public operation projection contains exactly `command_id,node_instance_id,account_key,operation_kind,execution_state,result,error_code,lifecycle_overridden,lifecycle_override_reason,same_account_overridden,same_account_override_reason,created_at,updated_at`; it excludes native name/auth_index, Management Key, upload fingerprint, raw response and credential. `result` is exactly null or `applied|noop|failed`: prepared/dispatched/outcome-unknown use null, remote-applied uses applied, remote-noop uses noop, and failed uses failed with a stable error_code.

Terminal success returns exactly `200 {"operation":<projection>}`. `prepared|dispatched|outcome_unknown` returns exactly `202 {"operation":<projection>}`. A terminal mapped failure with an existing operation row returns `{"error":{"code":"<stable-code>","message":"<bounded-sanitized-message>"},"operation":<projection>}` at the mapped status. Errors for which no operation projection is available or permitted, including authentication/authorization/CSRF failure, malformed request before acceptance, actor-first command conflict and either override target missing, return an error-only body `{"error":{"code":"<stable-code>","message":"<bounded-sanitized-message>"}}`; it never fabricates an operation projection. GET returns `200 {"operation":<current-projection>}` or `404 operation_not_found`; GET never resumes `prepared`. Exact terminal replay returns the original persisted status and canonical body bytes, whether the receipt body is error-only, error-plus-operation, or operation-only. A same-command POST replay resumes the same `prepared` operation using fresh evidence and may perform its first dispatch; replay of `dispatched` or `outcome_unknown` returns the current projection with 202 and zero remote mutation. Either override success returns `200 {"operation":<current-projection>}` and does not change execution state.

The public status mapping is frozen as follows:

```text
400 invalid_request | upload_invalid | identity_mismatch
401 authentication_required
403 authorization_required | csrf_failed
404 node_not_found | account_target_not_found | operation_not_found
409 node_retired | node_monitoring_ineligible | unsupported_provider
409 account_target_ambiguous | account_target_exists | account_filename_conflict
409 account_operation_in_progress | account_operation_not_overridable | lifecycle_override_already_set | same_account_override_already_set | command_conflict
413 upload_too_large
503 node_management_unavailable | unsupported_node_version | service_unavailable
```

An accepted request whose native outcome is ambiguous is not returned as an error envelope: it returns the 202 operation projection with `execution_state=outcome_unknown`, `result=null` and `error_code=remote_outcome_unknown`. Normal Inventory is a separate business-observation surface and never changes this execution projection or the immutable terminal POST receipt.

### 7. Durable operation schema

The additive migration candidate creates one row per accepted command:

```text
command_id uuid PRIMARY KEY FK admin_command_registry(command_id)
node_instance_id uuid NOT NULL FK relay_node_assets(instance_id)
account_key text NOT NULL
operation_kind text CHECK IN ('disable','enable','remove','upload_new','replace_existing')
execution_state text CHECK IN ('prepared','dispatched','remote_applied','remote_noop','outcome_unknown','failed')
dispatch_started_at timestamptz NULL
remote_result_code text NULL
upload_fingerprint_key_version smallint NULL
upload_intent_fingerprint bytea NULL CHECK length=32
lifecycle_override_at timestamptz NULL
lifecycle_override_by uuid NULL FK control_admin_users(admin_id) ON UPDATE RESTRICT ON DELETE RESTRICT
lifecycle_override_reason text NULL CHECK IN ('process_restarted','node_stopped','risk_accepted')
same_account_override_at timestamptz NULL
same_account_override_by uuid NULL FK control_admin_users(admin_id) ON UPDATE RESTRICT ON DELETE RESTRICT
same_account_override_reason text NULL CHECK IN ('process_restarted','node_stopped','risk_accepted')
created_at timestamptz NOT NULL
updated_at timestamptz NOT NULL
```

Each three-field override group is all-null or all-non-null. Free-form override detail is audit-only. A same-account override is eligible only for `dispatched` or `outcome_unknown`; it changes no execution state and only removes the same-account blocker. The current design has no `target_precondition`, `dispatch_token`, `remote_mutation_deadline`, `quiescence_deadline`, `remote_quiesced_at`, `write_token` or `postcondition_proof`. No native filename/path/auth_index/raw body/Management Key/credential is stored. Runtime roles receive no unrestricted DML; controlled functions enforce state shape and monotonic transitions. There is no Phase 7 verification state, scheduler, reconciler, lease, worker or durable verification workflow.

### 8. Global command and terminal receipt

After auth/session/super_admin/CSRF, Control acquires the shared UUID-derived advisory serialization and performs actor-first global registry lookup before target, upload Secret parsing/fingerprinting or Node calls. New reservation and operation creation are atomic. Cross-actor/domain/kind/intent reuse is `command_conflict`. An exact same-command POST whose existing operation is `prepared` resumes that same row; it does not create a new operation or command identity. Upload resume requires the same credential bytes and HMAC to be re-supplied. A GET never resumes an operation.

Change B adds immutable `account_admin_command_receipts`, separate from asset-only receipts. For account mutations, `remote_applied`, `remote_noop` and `failed` are receipt-eligible; `prepared`, `dispatched` and `outcome_unknown` are not. Phase 7 v1 has no additional intermediate failure state. If no stable evidence exists, state is `outcome_unknown` and there is no receipt.

A failure before global acceptance creates no operation/receipt. A stable terminal transition, terminal audit and receipt insertion commit atomically before response. Exact same actor/domain/kind/intent terminal replay returns stored HTTP status and canonical response bytes. Inventory observations and either lifecycle or same-account override changes MUST NOT rewrite that immutable original POST response.

The receipt relation supports account mutation commands and both independent `account.lifecycle_override` and `account.same_account_override` commands:

```text
command_id uuid PRIMARY KEY FK admin_command_registry(command_id)
target_operation_command_id uuid NULL FK account_admin_operations(command_id)
actor_admin_id uuid NOT NULL FK control_admin_users(admin_id) ON UPDATE RESTRICT ON DELETE RESTRICT
command_domain text NOT NULL CHECK = 'account_admin'
command_kind text NOT NULL
intent_encoding_version smallint NOT NULL CHECK = 1
canonical_intent_hash bytea NOT NULL CHECK octet_length=32
secret_fingerprint_key_version smallint NULL
http_status smallint NOT NULL
content_type text NOT NULL CHECK = 'application/json'
response_body bytea NOT NULL
committed_at timestamptz NOT NULL
```

Registry actor/domain/kind/encoding/hash/key-version integrity is enforced by the same composite identity relationship as Stage 7A. For a mutation receipt, `command_id=target_operation_command_id`; for a successful override they differ. A terminal override failure after actor-first reservation may have a null target FK when the requested operation does not exist; the canonical override intent still contains that UUID and binds exact replay. Runtime roles receive no unrestricted INSERT/UPDATE/DELETE/TRUNCATE.

Both override kinds use one PostgreSQL transaction from the transaction-scoped command advisory lock through registry lookup/reservation, target row lookup/lock and state validation, override field update or terminal failure, high-risk audit where applicable, and immutable receipt insertion. The registry reservation MUST NOT commit independently. Any crash before commit rolls back the reservation, target-field change, audit mutation and receipt; a committed transaction followed by response loss is recovered by exact receipt replay. No network I/O occurs in this transaction.

Lifecycle override ordering is authentication/session/super-admin/CSRF, global actor-first reservation and canonical intent validation, then target lookup/state validation under the target row lock. A missing target returns terminal `404 operation_not_found` with an error-only body and its own immutable override receipt; the requested target UUID remains in canonical intent. If the target exists but is not `dispatched` or unresolved `outcome_unknown`, it returns `409 account_operation_not_overridable` with error plus the current operation projection and its own receipt. Only after that check does an already-set lifecycle field return `409 lifecycle_override_already_set`, with its own receipt and no field change.

Same-account override uses the same actor-first ordering and receipt relation. A missing target returns terminal `404 operation_not_found` with an error-only body and its own override-command receipt; a target outside `dispatched` or unresolved `outcome_unknown` returns `409 account_operation_not_overridable` with error plus the current operation projection and its own receipt; only then does an already-set same-account field return `409 same_account_override_already_set` with its own receipt and no existing field change. The first valid override returns `200` and records its fields atomically. Neither override reuses the original account-mutation receipt.

The pinned v7.3.2 adapter has one explicit pre-mutation native exception: `POST /v0/management/auth-files` returning HTTP 503 when the reviewed route reaches `authManager == nil` before reading or writing the credential body maps to `failed` with `node_management_unavailable`, creates the normal terminal receipt, and sends no credential mutation. This mapping is based on reviewed route/artifact/status, never raw error text, and does not generalize to other native 503 responses. Any unreviewed 5xx remains `outcome_unknown`.

#### Canonical account intent v1

All account commands use `command_domain="account_admin"`, `intent_encoding_version=1` and these exact command kinds:

```text
account.disable
account.enable
account.remove
account.upload_new
account.replace_existing
account.lifecycle_override
account.same_account_override
```

Canonical bytes are UTF-8 JSON arrays with no insignificant whitespace. UUIDs are lowercase hyphenated ASCII. `account_key` is the validated canonical UTF-8 string. Strings use literal UTF-8 except JSON-required escapes for quote, reverse solidus and U+0000..U+001F; those control escapes use lowercase `\u00xx`, and HTML escaping is disabled. No Unicode normalization is performed after request validation. Array shapes and field order are exact:

```json
["account-intent-v1","account.disable","<node_instance_id>","<account_key>"]
["account-intent-v1","account.enable","<node_instance_id>","<account_key>"]
["account-intent-v1","account.remove","<node_instance_id>","<account_key>","REMOVE"]
["account-intent-v1","account.upload_new","<node_instance_id>","<account_key>",1,"<lowercase-hex-upload-hmac>"]
["account-intent-v1","account.replace_existing","<node_instance_id>","<account_key>",1,"<lowercase-hex-upload-hmac>"]
["account-intent-v1","account.lifecycle_override","<target_operation_command_id>","<reason>","OVERRIDE UNKNOWN OPERATION LIFECYCLE BLOCK",null]
["account-intent-v1","account.lifecycle_override","<target_operation_command_id>","<reason>","OVERRIDE UNKNOWN OPERATION LIFECYCLE BLOCK","<present-detail>"]
["account-intent-v1","account.same_account_override","<target_operation_command_id>","<reason>","OVERRIDE UNKNOWN OPERATION SAME-ACCOUNT BLOCK",null]
["account-intent-v1","account.same_account_override","<target_operation_command_id>","<reason>","OVERRIDE UNKNOWN OPERATION SAME-ACCOUNT BLOCK","<present-detail>"]
```

The two override forms distinguish absent detail from present detail. If present, detail MUST be 1..512 UTF-8 bytes, contain no control character, and is encoded exactly as validated without trimming or normalization; an empty present value is invalid. `canonical_intent_hash=SHA-256(canonical bytes)`. Non-upload and override reservations use null `secret_fingerprint_key_version`; upload commands use version 1.

Upload credential bytes never enter canonical intent. `CONTROL_ACCOUNT_OPERATION_INTENT_KEY_FILE` is a stable deployment Secret containing exactly 32 raw bytes in a regular non-symlink file with strict owner-only permissions equivalent to asset K1 checks. The HMAC is `HMAC-SHA-256(key, UTF8("relay-station/account-operation-upload-intent/v1") || 0x00 || exact_credential_bytes)` and is encoded as 64 lowercase hexadecimal ASCII characters in the array. Missing, unreadable, unsafe, symlink or wrong-length key returns `service_unavailable`; there is no automatic generation or asset-K1 fallback. A structurally valid wrong key deterministically produces canonical hash mismatch and `command_conflict` for historical replay; restoring the original key restores replay.

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
-> reject another operation in dispatched or unresolved outcome_unknown when same_account_override_at IS NULL
-> lock current operation
-> require execution_state=prepared
-> set dispatch_started_at and prepared -> dispatched
COMMIT
-> perform one native HTTP request outside transaction
```

No DB lock crosses native HTTP. Same-target races, Control restart and lifecycle races are resolved from durable rows/constraints, not process memory. The durable blocker predicate is conceptually `execution_state IN ('dispatched','outcome_unknown') AND same_account_override_at IS NULL`; an equivalent PostgreSQL constraint may implement it. There is no Relay mutation capability discovery and no Node mutation protocol constant gate; deployment compatibility evidence is pinned upstream v7.3.2 plus the future reviewed Control adapter artifact.

If the current operation is `prepared`, the same-command POST reruns every runtime, snapshot, target, lifecycle, monitoring, policy and serialization check with fresh evidence. It may record `remote_noop`, a stable reviewed pre-mutation `failed`, or atomically transition `prepared -> dispatched` before the first native request. Two concurrent exact retries are serialized so at most one can dispatch. This request-driven resume is not redispatch because `prepared` proves that no remote request was previously dispatched. `dispatched` and `outcome_unknown` replay only the current 202 projection and never automatically redispatch.

### 10. Disable/Enable no-op and conservative native outcome mapping

After global command acceptance, runtime artifact gate, fresh mutation-eligible exactly-one resolution and durable dispatch eligibility, Control evaluates the safe snapshot `disabled` value before sending PATCH. Disable with `disabled=true`, or Enable with `disabled=false`, sends zero native PATCH and terminalizes as `remote_noop`; audit, immutable terminal receipt and exact replay apply. When desired state differs, Control commits `prepared -> dispatched` and sends PATCH exactly once; stable native 2xx is always `remote_applied` and MUST NOT be reclassified as noop by later read-back.

```text
pre-dispatch desired state already satisfied -> remote_noop, zero PATCH
known successful terminal native 2xx after PATCH/POST/DELETE -> remote_applied
stable reviewed pre-mutation status/context -> failed
timeout / connection loss / response loss -> outcome_unknown
ambiguous native 5xx after request may have arrived -> outcome_unknown
```

Control maps only adapter-reviewed status/context and never parses raw native error strings to infer filesystem/runtime commit stage. Read-back and Inventory may show business convergence but MUST NOT retroactively invent exact execution evidence or noop. Every mutation kind follows this rule. Automatic redispatch of `dispatched` or `outcome_unknown` is prohibited, including same-command POST replay and restart recovery.

### 11. Node lifecycle blocking and manual override

Retire/Replace locks the same Node first, then inspects same-Node account operations. `dispatched` and unresolved `outcome_unknown` block lifecycle unless that exact operation has a durable reviewed lifecycle override. There is no automatic quiescence proof or deadline expiry release.

The action name is **Override Unknown Operation Lifecycle Block**. The path UUID identifies the target account operation; the body `command_id` is a new independent administrator command. After authentication/session/super_admin/CSRF, Control acquires the shared global command advisory serialization, performs actor-first registry lookup/reservation, and validates the canonical `account.lifecycle_override` intent before locking and changing the target operation. Cross-actor/domain/kind/intent reuse is `command_conflict`. Exact replay returns the override command's immutable terminal receipt with zero second mutation; it never reuses or overwrites the target account mutation receipt.

The first valid override sets only `lifecycle_override_at/by/reason`; it does not change execution state or redispatch authority. `outcome_unknown` remains unknown. Reasons are `process_restarted|node_stopped|risk_accepted`. `risk_accepted` explicitly waives the guarantee that a previously dispatched request can never mutate the old Node after lifecycle proceeds. All reasons require super_admin, active session, same-origin/CSRF, typed confirmation and distinct high-risk audit; operator detail is audit-only. Override mutation, audit and its separate receipt commit atomically.

A later different lifecycle override command after those fields are set returns stable `409 lifecycle_override_already_set`; a later different same-account override returns `409 same_account_override_already_set`. Each accepted command records its own terminal receipt without changing the first override. Lifecycle override is consulted only by Node Retire/Replace. Same-account override is consulted only by same-account serialization. A same-account override does not waive the lifecycle blocker, and a lifecycle override does not waive the same-account blocker. Both remain one-time durable fields on the original operation, not separate workflows. The earlier native request may already have committed or may still complete after a same-account override; the resulting last-writer-wins risk is explicitly accepted and no outcome is inferred.

Node-first lock order is mandatory for dispatch and lifecycle. Acceptance covers Retire-first vs dispatch, dispatch-first vs Retire, same-account A vs B and Control restart with a live operation.

### 12. Independent Inventory observation

Normal Inventory is an independent business-observation surface. It continues on its existing cadence and may show convergence for Disable, Enable, Remove, Upload New or Replace Existing, but it never changes `execution_state`, creates a Phase 7 run, writes account operation state, proves credential bytes/CAS/quiescence, or terminalizes an operation. An `outcome_unknown` operation remains `outcome_unknown` whether or not Inventory later observes the desired state.

### 13. Secret, audit, metrics and errors

Control keeps credential bytes in bounded memory only and stores only the versioned keyed upload-intent fingerprint under `CONTROL_ACCOUNT_OPERATION_INTENT_KEY_FILE`; asset K1 is unchanged. Credential, Management Key, raw native body, full path and ephemeral target evidence never enter DB, receipt, response, audit, logs, traces or metrics.

Audit records actor/request/command/operation/Node/provider/protected business identity, sanitized outcome and each high-risk override action, distinguishing lifecycle and same-account override. Metrics use low-cardinality operation/provider/result/error/execution classes and never email/account_key/command/node/path.

Stable Control errors include `invalid_request`, `node_not_found`, `node_retired`, `node_management_unavailable`, `node_monitoring_ineligible`, `unsupported_provider`, `account_target_not_found`, `account_target_ambiguous`, `account_operation_in_progress`, `account_operation_not_overridable`, `lifecycle_override_already_set`, `same_account_override_already_set`, `command_conflict`, `upload_too_large`, `upload_invalid`, `identity_mismatch`, `remote_outcome_unknown` and `service_unavailable`. Raw native messages are never relayed.

### Phase-aware acceptance and error contract

The acceptance boundary is normative: a new account mutation exists only after one PostgreSQL transaction commits the global registry reservation together with `account_admin_operations(execution_state=prepared)`. Before that commit, no reservation, operation, receipt or native request may survive. Pre-acceptance failures are error-only and include authentication/authorization/CSRF, request or Upload New validation (`invalid_request`, `upload_too_large`, `upload_invalid`, `identity_mismatch`), requested-provider `unsupported_provider`, `node_not_found`, upload intent-key `service_unavailable`, and actor-first `command_conflict`.

After acceptance, deterministic failures before native dispatch terminalize `prepared -> failed`, set no `dispatch_started_at`, persist terminal audit and an immutable receipt, and return error plus operation. This set includes `node_retired`, `node_monitoring_ineligible`, missing/inactive matching Provider policy as `unsupported_provider`, `unsupported_node_version`, `node_management_unavailable`, unsafe Replace Existing inherited basename as `invalid_request`, target/occupancy errors and `account_operation_in_progress`. Exact same-command replay returns the receipt; when conditions change, a new command ID is required. Dispatch eligibility checks Node lifecycle, monitoring, inventory-read capability, Provider policy and same-account serialization in that order.

| error code / cause | phase | operation | state | receipt | body | same command |
|---|---|---:|---|---|---|---|
| `unsupported_provider` — requested provider outside closed surface | pre-acceptance | no | none | no | error-only | may resubmit after correcting request |
| `unsupported_provider` — valid request but no active Provider policy | accepted pre-dispatch | yes | `failed` | yes | error + operation | exact receipt; new ID after policy change |
| `invalid_request` — Upload New generated basename unsafe | pre-acceptance | no | none | no | error-only | may resubmit corrected request |
| `invalid_request` — Replace Existing inherited basename unsafe | accepted pre-dispatch | yes | `failed` | yes | error + operation | exact receipt; new ID after repair |
| `service_unavailable` — upload intent key missing/unsafe/wrong length | pre-acceptance | no | none | no | error-only | may evaluate normally once key is restored |
| `node_retired`, `node_monitoring_ineligible`, `unsupported_node_version`, `node_management_unavailable`, target/occupancy errors, `account_operation_in_progress` | accepted pre-dispatch | yes | `failed` | yes | error + operation | exact receipt; new ID after conditions change |

The reviewed v7.3.2 Upload POST `503` at `authManager == nil` before credential-body read/write remains a separate dispatched `failed/node_management_unavailable` exception. Stable native 2xx is `remote_applied`; timeout, connection/response loss and ambiguous or unreviewed 5xx remain `outcome_unknown`. No raw native error text is parsed.

### 14. Compatibility, supersession and rollout

Stage 7A remains satisfied at migration 37 and class/floor 3/3. Native-First implementation compatibility class/floor is assigned only with reviewed Control artifact/schema evidence. Forward schema/receipts remain preserved on rollback.

No Node revert occurs in this planning change. Historical Stage 7N results remain true historical evidence while their protocol is superseded as the current Change B dependency. Any future Node alignment uses ordinary reviewed commits, never history rewrite or force push. The Ops supersession ADR is ACCEPTED after the independent Native-First architecture re-review passed.

## Acceptance Strategy

Future acceptance MUST cover exact native route allowlisting; exact-once runtime identity headers including duplicate/mismatch; manager/file versus memory/runtime-only/disk-fallback/empty classification; snapshot Secret/raw-field rejection; 1 MiB boundaries; complete alias denylist pass/reject vectors; shared Create/Replace safe-basename and 238/239-byte boundaries; identity and basename collision admission; best-effort create/replace and accepted lost-update races; pre-PATCH noop versus sent-PATCH applied; ambiguous 5xx/timeouts to outcome_unknown; zero automatic redispatch; exact canonical intent/HMAC golden vectors and wrong-key replay; prepared crash/resume and concurrent retry serialization; same-account override eligibility/replay/ordering-risk and orthogonality with lifecycle override; PostgreSQL same-account serialization; Node-first lifecycle races/restart; first/later override semantics and high-risk audit; normal Inventory independent business observation; Secret scans; pinned v7.3.2 adapter/runtime-artifact tests; API/UI; and compatibility rollback.

## Planning history and current gate

Historical Stage 7N contract/design/implementation reviews, corrective amendments and artifacts are preserved in Ops. They are not current Stage 7B dependencies and are not the current deployment baseline.

```text
Historical Native-First Crash Recovery Corrective Round 11
Previous independent crash-recovery re-review = P0 0 / P1 1 / P2 2 / CHANGES REQUIRED
Round 11 resolutions = INCORPORATED
P0 = 0
P1 = 0 candidate
P2 = 0 candidate
Architecture status = READY FOR INDEPENDENT CRASH-RECOVERY RE-REVIEW
Gate 1 = NOT CLOSED（历史候选状态）
ADR = PROPOSED（历史候选状态）
Runtime artifact identity = NOT YET FROZEN
Node revert = NOT RUN
Stage 7B implementation = NOT STARTED
```

This historical candidate did not declare Architecture Review PASS, Detailed Requirements FROZEN, implementation readiness READY or Stage 7B authorization. Current Gate 1 finalization and Gate 2 planning candidate are recorded above and do not authorize implementation.

## Current Gate 2 candidate

### Gate 2 Corrective Round 2 status

Independent Gate 2 final re-review: `P0=0 / P1=0 / P2=2 non-blocking / PASS`；final Gate 2 findings `P0=0 / P1=0 / P2=0`。Gate 1 remains `CLOSED / PASS`; Detailed Requirements are `FROZEN`; OpenSpec Change B is `READY`; planning/specification readiness is `PASS`; Gate 2 is `CLOSED / PASS`; Gate 3 is `NEXT`。Runtime artifact identity remains `NOT YET FROZEN`; implementation is not authorized.

```text
Architecture Review = PASS
ADR = ACCEPTED
Gate 1 = CLOSED / PASS
Gate 2 = CLOSED / PASS
Detailed Requirements = FROZEN
OpenSpec Change B = READY
Runtime artifact identity = NOT YET FROZEN
Final Implementation Readiness = NOT READY
Node revert = NOT RUN
Stage 7B implementation = NOT STARTED
```

## Acceptance boundary and pre-dispatch terminality

An account mutation is **accepted** only when one atomic PostgreSQL transaction has committed both the Stage 7A `admin_command_registry` reservation and an `account_admin_operations` row with `execution_state=prepared`. Before that commit, a new command has no accepted operation, terminal account receipt or native mutation. Authentication/session/super-admin/CSRF, shared command serialization, actor-first conflict priority, closed request and canonical identity validation, upload bounds/schema/denylist/HMAC validation, and the required Node existence lookup precede that acceptance commit. Native management HTTP is never sent before acceptance.

For a new command ID, pre-acceptance authentication, authorization, CSRF, request, upload, identity, unsupported-provider, Node-not-found or actor/domain/kind/intent conflict errors create no registry reservation, operation row or receipt and return an error-only body. Actor-first lookup of an existing command remains first and preserves Stage 7A conflict priority; a conflict never discloses a target operation.

`prepared` means an accepted command for which zero remote mutation dispatch has occurred and no deterministic terminal result has yet committed. It is not a queue, retry state, scheduler state or waiting state. Only an exact same-command mutation POST may resume it; GET never resumes it. A prepared upload resume must re-supply the exact credential bytes and matching upload HMAC. A different fingerprint is `command_conflict` with zero native request.

After acceptance, every deterministic failure discovered before remote dispatch MUST atomically transition `prepared -> failed`, record the stable error, terminal audit and immutable receipt, then return the error plus the current operation projection. This includes `node_retired`, `node_monitoring_ineligible`, `unsupported_node_version`, `node_management_unavailable`, `account_target_not_found`, `account_target_ambiguous`, `account_target_exists`, `account_filename_conflict` and `account_operation_in_progress`. `dispatch_started_at` remains null and native mutation is zero. The committed receipt makes the same-command retry an exact replay with no re-evaluation; a new command ID is required after conditions change. The reviewed Upload POST 503/authManager-nil exception is a separate post-`prepared -> dispatched` terminal mapping, and is also `failed/node_management_unavailable` with a receipt and zero credential mutation.

### Account mutation response and receipt matrix

| Phase | Condition | New reservation | Operation row | State/result | dispatch_started_at | HTTP/body | Receipt | Same-command retry | New command after condition changes |
|---|---|---|---|---|---|---|---|---|---|
| Pre-acceptance | auth/authorization/CSRF, invalid request/upload/identity, unsupported provider or Node not found | no | no | none | none | mapped error-only | no | normal re-evaluation | no |
| Pre-acceptance | actor/domain/kind/intent conflict | no new reservation | no disclosure | existing command unchanged | unchanged | `409 command_conflict`, error-only | existing command rules | exact prior command behavior | yes |
| Accepted pre-dispatch | deterministic Node, monitoring, runtime, target, occupancy or same-account blocker failure | yes, committed with operation | yes | `failed` / stable error | null | mapped error + operation, normally `409` or `503` | yes | exact persisted receipt | yes |
| Accepted pre-dispatch | Disable/Enable desired state already satisfied | yes | yes | `remote_noop` / noop | null | `200` operation-only | yes | exact persisted receipt | no |
| Dispatched | stable native 2xx after request sent | yes | yes | `remote_applied` / applied | non-null | `200` operation-only | yes | exact persisted receipt | no |
| Dispatched | reviewed Upload POST 503 at authManager-nil before body read/write | yes | yes | `failed` / `node_management_unavailable` | non-null | `503` error + operation | yes | exact persisted receipt | no |
| Dispatched | timeout, connection/response loss or ambiguous/unreviewed 5xx | yes | yes | `outcome_unknown` | non-null | `202` operation-only | no | current projection, zero redispatch | explicit same-account override may be required |
| Override | target missing after actor-first reservation | yes | target absent | no target mutation | n/a | `404` error-only | override receipt | exact persisted receipt | no |
| Override | existing target not overridable | yes | yes | target unchanged | n/a | `409` error + operation | override receipt | exact persisted receipt | no |
| Override | eligible target already has route-specific override | yes | yes | target unchanged | n/a | `409` error + operation | override receipt | exact persisted receipt | no |
| Override | first valid override | yes | yes | execution state unchanged | n/a | `200` operation-only | override receipt | exact persisted receipt | no |

The only allowed mutation transitions are `prepared -> dispatched`, `prepared -> remote_noop`, `prepared -> failed`, `dispatched -> remote_applied`, reviewed/proven `dispatched -> failed`, and `dispatched -> outcome_unknown`. `remote_applied`, `remote_noop` and `failed` are terminal. `outcome_unknown` is never automatically terminalized. Receipt eligibility is exactly: `prepared` no, `dispatched` no, `outcome_unknown` no, `remote_applied` yes, `remote_noop` yes and `failed` yes.
