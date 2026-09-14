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
```

Disable/Enable JSON bodies contain exactly lowercase UUID `command_id`, UUID `node_instance_id` and canonical `account_key` (maximum 385 UTF-8 bytes). Remove additionally requires `confirmation="REMOVE"`. Upload routes accept exactly one `request` JSON part with those three fields and one credential part under Decision 5. In the lifecycle-override route, the path UUID is the target operation command ID; its body contains exactly a new independent lowercase UUID `command_id`, `reason`, `confirmation="OVERRIDE UNKNOWN OPERATION LIFECYCLE BLOCK"` and optional audit-only `detail` up to 512 UTF-8 bytes.

Unknown JSON fields, duplicate multipart parts, a missing part, any extra part and malformed UUID/account identity are `400 invalid_request`. The credential part is sent to CLIProxyAPI as the exact validated JSON bytes; browser input never supplies native basename, auth_index, Management Key or final path.

POST mutation routes require active authenticated `super_admin`, same-origin and CSRF. GET is authenticated/read-only. Override has the same checks plus typed high-risk confirmation. Every response, including errors, is `Cache-Control: no-store`. Public operation projection contains exactly `command_id,node_instance_id,account_key,operation_kind,execution_state,result,error_code,lifecycle_overridden,lifecycle_override_reason,created_at,updated_at`; it excludes native name/auth_index, Management Key, upload fingerprint, raw response and credential. `result` is exactly null or `applied|noop|failed`: prepared/dispatched/outcome-unknown use null, remote-applied uses applied, remote-noop uses noop, and failed uses failed with a stable error_code.

Terminal success returns exactly `200 {"operation":<projection>}`. `prepared|dispatched|outcome_unknown` returns exactly `202 {"operation":<projection>}`. A terminal mapped failure returns `{"error":{"code":"<stable-code>","message":"<bounded-sanitized-message>"},"operation":<projection>}` at the mapped status. GET returns `200 {"operation":<current-projection>}` or `404 operation_not_found`. Same-command nonterminal replay returns the same current-projection shape with 202 and zero redispatch. Exact terminal replay returns the original persisted status and canonical body bytes. Override success returns `200 {"operation":<current-projection>}` and does not change execution state.

The public status mapping is frozen as follows:

```text
400 invalid_request | upload_invalid | identity_mismatch
401 authentication_required
403 authorization_required | csrf_failed
404 node_not_found | account_target_not_found | operation_not_found
409 node_retired | node_monitoring_ineligible | unsupported_provider
409 account_target_ambiguous | account_target_exists | account_filename_conflict
409 account_operation_in_progress | lifecycle_override_already_set | command_conflict
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
created_at timestamptz NOT NULL
updated_at timestamptz NOT NULL
```

The three override fields are all-null or all-non-null. Free-form override detail is audit-only. The current design has no `target_precondition`, `dispatch_token`, `remote_mutation_deadline`, `quiescence_deadline`, `remote_quiesced_at`, `write_token` or `postcondition_proof`. No native filename/path/auth_index/raw body/Management Key/credential is stored. Runtime roles receive no unrestricted DML; controlled functions enforce state shape and monotonic transitions. There is no Phase 7 verification state, scheduler, reconciler, lease, worker or durable verification workflow.

### 8. Global command and terminal receipt

After auth/session/super_admin/CSRF, Control acquires the shared UUID-derived advisory serialization and performs actor-first global registry lookup before target, upload Secret parsing/fingerprinting or Node calls. New reservation and operation creation are atomic. Cross-actor/domain/kind/intent reuse is `command_conflict`.

Change B adds immutable `account_admin_command_receipts`, separate from asset-only receipts. For account mutations, `remote_applied`, `remote_noop` and `failed` are receipt-eligible; `prepared`, `dispatched` and `outcome_unknown` are not. Phase 7 v1 has no additional intermediate failure state. If no stable evidence exists, state is `outcome_unknown` and there is no receipt.

A failure before global acceptance creates no operation/receipt. A stable terminal transition, terminal audit and receipt insertion commit atomically before response. Exact same actor/domain/kind/intent terminal replay returns stored HTTP status and canonical response bytes. Verification and lifecycle override changes MUST NOT rewrite that immutable original POST response.

The receipt relation supports both account mutation commands and independent lifecycle-override commands:

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

Lifecycle override ordering is authentication/session/super-admin/CSRF, global actor-first reservation and canonical intent validation, then target lookup/state validation. A missing target returns terminal `404 operation_not_found` with its own immutable override receipt and exact replay. A target that is not `dispatched` or unresolved `outcome_unknown` returns the reviewed terminal conflict with its own receipt. A later override after one is recorded returns `409 lifecycle_override_already_set`, records its own receipt and changes no existing override fields.

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
-> reject another operation in dispatched or unresolved outcome_unknown, regardless of lifecycle override
-> lock current operation
-> require execution_state=prepared
-> set dispatch_started_at and prepared -> dispatched
COMMIT
-> perform one native HTTP request outside transaction
```

No DB lock crosses native HTTP. Same-target races, Control restart and lifecycle races are resolved from durable rows/constraints, not process memory. There is no Relay mutation capability discovery and no Node mutation protocol constant gate; deployment compatibility evidence is pinned upstream v7.3.2 plus the future reviewed Control adapter artifact.

### 10. Disable/Enable no-op and conservative native outcome mapping

After global command acceptance, runtime artifact gate, fresh mutation-eligible exactly-one resolution and durable dispatch eligibility, Control evaluates the safe snapshot `disabled` value before sending PATCH. Disable with `disabled=true`, or Enable with `disabled=false`, sends zero native PATCH and terminalizes as `remote_noop`; audit, immutable terminal receipt and exact replay apply. When desired state differs, Control commits `prepared -> dispatched` and sends PATCH exactly once; stable native 2xx is always `remote_applied` and MUST NOT be reclassified as noop by later read-back.

```text
pre-dispatch desired state already satisfied -> remote_noop, zero PATCH
known successful terminal native 2xx after PATCH/POST/DELETE -> remote_applied
provably pre-mutation mapped native 4xx -> failed
timeout / connection loss / response loss -> outcome_unknown
ambiguous native 5xx after request may have arrived -> outcome_unknown
```

Control maps only adapter-reviewed status/context and never parses raw native error strings to infer filesystem/runtime commit stage. Read-back and Inventory may show business convergence but MUST NOT retroactively invent exact execution evidence or noop. Every mutation kind follows this rule. Automatic redispatch of `dispatched` or `outcome_unknown` is prohibited, including same-command POST replay and restart recovery.

### 11. Node lifecycle blocking and manual override

Retire/Replace locks the same Node first, then inspects same-Node account operations. `dispatched` and unresolved `outcome_unknown` block lifecycle unless that exact operation has a durable reviewed override. There is no automatic quiescence proof or deadline expiry release.

The action name is **Override Unknown Operation Lifecycle Block**. The path UUID identifies the target account operation; the body `command_id` is a new independent administrator command. After authentication/session/super_admin/CSRF, Control acquires the shared global command advisory serialization, performs actor-first registry lookup/reservation, and validates the canonical `account.lifecycle_override` intent before locking and changing the target operation. Cross-actor/domain/kind/intent reuse is `command_conflict`. Exact replay returns the override command's immutable terminal receipt with zero second mutation; it never reuses or overwrites the target account mutation receipt.

The first valid override sets only `lifecycle_override_at/by/reason`; it does not change execution state or redispatch authority. `outcome_unknown` remains unknown. Reasons are `process_restarted|node_stopped|risk_accepted`. `risk_accepted` explicitly waives the guarantee that a previously dispatched request can never mutate the old Node after lifecycle proceeds. All reasons require super_admin, active session, same-origin/CSRF, typed confirmation and distinct high-risk audit; operator detail is audit-only. Override mutation, audit and its separate receipt commit atomically.

A later different override command after those fields are set returns stable `409 lifecycle_override_already_set`, changes no override value and atomically records that command's terminal receipt for exact replay. An override is consulted only by Node Retire/Replace. A `dispatched` or unresolved `outcome_unknown` row continues to block every new same-account Disable, Enable, Remove, Upload New and Replace Existing regardless of override fields. Lifecycle override is neither operation resolution nor same-account serialization override.

Node-first lock order is mandatory for dispatch and lifecycle. Acceptance covers Retire-first vs dispatch, dispatch-first vs Retire, same-account A vs B and Control restart with a live operation.

### 12. Independent Inventory observation

Normal Inventory is an independent business-observation surface. It continues on its existing cadence and may show convergence for Disable, Enable, Remove, Upload New or Replace Existing, but it never changes `execution_state`, creates a Phase 7 run, writes account operation state, proves credential bytes/CAS/quiescence, or terminalizes an operation. An `outcome_unknown` operation remains `outcome_unknown` whether or not Inventory later observes the desired state.

### 13. Secret, audit, metrics and errors

Control keeps credential bytes in bounded memory only and stores only the versioned keyed upload-intent fingerprint under `CONTROL_ACCOUNT_OPERATION_INTENT_KEY_FILE`; asset K1 is unchanged. Credential, Management Key, raw native body, full path and ephemeral target evidence never enter DB, receipt, response, audit, logs, traces or metrics.

Audit records actor/request/command/operation/Node/provider/protected business identity, sanitized outcome and high-risk lifecycle override. Metrics use low-cardinality operation/provider/result/error/execution classes and never email/account_key/command/node/path.

Stable Control errors include `invalid_request`, `node_not_found`, `node_retired`, `node_management_unavailable`, `node_monitoring_ineligible`, `unsupported_provider`, `account_target_not_found`, `account_target_ambiguous`, `account_operation_in_progress`, `command_conflict`, `upload_too_large`, `upload_invalid`, `identity_mismatch`, `remote_outcome_unknown` and `service_unavailable`. Raw native messages are never relayed.

### 14. Compatibility, supersession and rollout

Stage 7A remains satisfied at migration 37 and class/floor 3/3. Native-First implementation compatibility class/floor is assigned only with reviewed Control artifact/schema evidence. Forward schema/receipts remain preserved on rollback.

No Node revert occurs in this planning change. Historical Stage 7N results remain true historical evidence while their protocol is superseded as the current Change B dependency. Any future Node alignment uses ordinary reviewed commits, never history rewrite or force push. The Ops supersession ADR remains PROPOSED until independent Native-First architecture re-review passes.

## Acceptance Strategy

Future acceptance MUST cover exact native route allowlisting; exact-once runtime identity headers including duplicate/mismatch; manager/file versus memory/runtime-only/disk-fallback/empty classification; snapshot Secret/raw-field rejection; 1 MiB boundaries; complete alias denylist pass/reject vectors; shared Create/Replace safe-basename and 238/239-byte boundaries; identity and basename collision admission; best-effort create/replace and accepted lost-update races; pre-PATCH noop versus sent-PATCH applied; ambiguous 5xx/timeouts to outcome_unknown; zero automatic redispatch; exact canonical intent/HMAC golden vectors and wrong-key replay; mutation and independent override receipt eligibility/replay; PostgreSQL same-account serialization unaffected by lifecycle override; Node-first lifecycle races/restart; first/later override semantics and high-risk audit; Inventory convergence; Secret scans; pinned v7.3.2 adapter/runtime-artifact tests; API/UI; and compatibility rollback.

## Planning history and current gate

Historical Stage 7N contract/design/implementation reviews, corrective amendments and artifacts are preserved in Ops. They are not current Stage 7B dependencies and are not the current deployment baseline.

```text
Native-First Simplification Corrective Round 6
Previous independent re-review = P0 0 / P1 4 / P2 3 / CHANGES REQUIRED
P0 = 0
P1 = 0 candidate
P2 = 0 candidate
Architecture status = READY FOR INDEPENDENT ARCHITECTURE RE-REVIEW
ADR = PROPOSED
Runtime artifact identity = NOT YET FROZEN
Node revert = NOT RUN
Stage 7B implementation = NOT STARTED
```

This candidate does not declare Architecture Review PASS, Detailed Requirements FROZEN, implementation readiness READY or Stage 7B authorization.
