## ADDED Requirements

### Requirement: Account operations SHALL remain explicit and data-plane isolated

Control SHALL expose only single-account Antigravity Disable, Enable, Remove, Upload New and Replace Existing, protected operation read, and Override Unknown Operation Lifecycle Block. Mutation and override MUST require active authenticated `super_admin`, same-origin and CSRF; override additionally requires typed high-risk confirmation. Browser MUST NOT receive Node Management Key, raw credential, native basename/auth_index or raw native response. Phase 7 MUST NOT add batch/all mutation, OAuth/Re-auth, automatic repair/move/remove, Gateway mutation, scheduler ownership or data-plane participation.

#### Scenario: Unsupported batch or passthrough action
- **WHEN** a client requests multiple accounts, native all-delete, arbitrary management passthrough, OAuth, repair or move
- **THEN** Control rejects before remote mutation and creates no accepted account operation

### Requirement: Account command identity SHALL use the global registry and actor-first ordering

After authentication/session/super_admin/CSRF, mutations MUST acquire shared global command serialization and perform actor-first registry lookup before target lookup, upload Secret parsing/fingerprinting or Node calls. Cross-actor/domain/kind/intent reuse MUST return `command_conflict` with zero remote mutation. Upload equality MUST use the separate Phase 7 keyed fingerprint and MUST NOT alter asset K1.

#### Scenario: Asset command ID is reused for upload
- **WHEN** an upload uses a UUID reserved by Gateway, Node or Monitoring command
- **THEN** `command_conflict` occurs before credential parsing and no native request is sent

### Requirement: Control SHALL call only the reviewed native CLIProxyAPI subset

Against CLIProxyAPI v7.3.2 at `7fa443dc8bf8ca2f1ffd81c2472deb31b097b697`, Control MUST use only `GET /v0/management/auth-files`, `PATCH /v0/management/auth-files/status` with exact `name/auth_index/disabled`, single-name query `DELETE /v0/management/auth-files?name=...`, and raw-JSON `POST /v0/management/auth-files?name=...`. Management transport MUST remain HTTP-only, fixed-target, bounded, no redirect/proxy/fallback/retry and sanitized.

Control MUST NOT use `DELETE all=true`, multi-name/body delete, multipart native upload, auth-files fields/download/refresh, OAuth routes or generic passthrough. Management Key MUST remain server-side only and absent from durable/observable surfaces.

#### Scenario: Adapter is asked to delete all files
- **WHEN** any Control path attempts native `DELETE all=true` or multiple names
- **THEN** the adapter rejects locally and sends zero native request

### Requirement: Native snapshot SHALL be projected immediately to safe target evidence

Raw `GET /v0/management/auth-files` input MUST be bounded and immediately projected to provider/type, normalized email, validated basename, bounded auth_index and disabled only. Full path, status message, ID/token-like fields, raw metadata, quota/cooldown/request counters, headers/proxy/note/routing fields, unknown fields and raw objects MUST be discarded and MUST NOT enter PostgreSQL, receipts, audit, logs, traces, metrics or browser/API output.

#### Scenario: Snapshot contains path and token-adjacent metadata
- **WHEN** native snapshot includes a full path, id_token, status_message, quota, headers and unknown fields
- **THEN** target resolution sees only the safe projection and no discarded value reaches any Control durable or public surface

### Requirement: Account operations SHALL resolve exactly one fresh native target

Disable, Enable, Remove and Replace Existing MUST read a fresh safe snapshot immediately before dispatch and match canonical provider plus normalized email for `(node_instance_id,account_key)`. Zero matches returns `account_target_not_found`; more than one returns `account_target_ambiguous`; one match yields ephemeral name/auth_index. Control MUST NOT choose first/latest, infer a filename or use Inventory alone as physical target truth. Name/auth_index MUST NOT become durable/public business identity.

#### Scenario: Duplicate native auth files normalize to one account
- **WHEN** two safe snapshot entries match the requested provider and normalized email
- **THEN** Control returns `account_target_ambiguous` and sends zero mutation

### Requirement: Upload SHALL preserve CLIProxyAPI credential schema ownership and Control runtime boundaries

Control MUST accept one top-level Antigravity JSON credential of at most 1048576 bytes, require `type=antigravity`, require email and match its normalized value to the intended account identity. CLIProxyAPI remains credential schema truth; new provider credential fields may pass through.

Control MUST reject, without silent stripping, pinned-v7.3.2 runtime/routing/management fields: `disabled,weight,priority,headers,request_retry,request-retry,excluded_models,excluded-models,proxy_url,note,websockets,prefix,models,disable_cooling,fingerprint_profile`. The public multipart aggregate is at most 1073152 bytes with one request part at most 8192 bytes, one credential part at most 1048576 bytes and at most 16384 bytes of framing.

#### Scenario: Upload includes a provider extension and runtime controls
- **WHEN** credential contains a new provider field and also `proxy_url`
- **THEN** Control rejects `upload_invalid` because of the runtime field and does not silently strip or send the body

### Requirement: Create and Replace SHALL use bounded native basenames

Upload New MUST generate `antigravity-<normalized_email>.json`, measure UTF-8 bytes, enforce `MAX_NATIVE_BASENAME_BYTES=255` and `MAX_CREATE_EMAIL_BYTES=238`, and prohibit truncation/hash fallback/browser-selected path. Replace Existing MUST inherit the exact fresh native basename after validating non-empty basename-only form, no separator/traversal/control character and at most 255 bytes.

#### Scenario: Create email exceeds physical filename admission
- **WHEN** normalized email is 239 UTF-8 bytes or generated basename exceeds 255 bytes
- **THEN** Control returns `invalid_request` with zero native request

### Requirement: Create and Replace SHALL accept native last-writer-wins

Upload New MUST require a fresh absent snapshot and then issue native POST as best-effort create. It has no atomic create-if-absent guarantee; a concurrent writer may create after snapshot and native POST may overwrite it. Replace Existing MUST resolve exactly one entry and POST to its basename as best-effort replace; a native credential refresh may occur between snapshot and POST and may be overwritten. Phase 7 v1 explicitly accepts both races and MUST NOT claim CAS, ETag, revision, incarnation or postcondition proof.

#### Scenario: Native refresh races Replace
- **WHEN** credential refresh commits after Control snapshot but before administrator POST
- **THEN** native last-writer-wins may overwrite the refresh and Control does not claim lost-update prevention

### Requirement: Dispatch SHALL use durable same-account serialization and Node-first eligibility

Before native mutation, one short PostgreSQL transaction MUST lock Node first, require active lifecycle, lock/read current non-cancelled monitoring under the existing graph, require `management_account_inventory_read` and matching active Provider policy, acquire/check durable serialization for `(node_instance_id,account_key)`, reject any other `dispatched` or unresolved `outcome_unknown` operation without valid override, lock the prepared operation, persist dispatch start and transition `prepared -> dispatched`. It then commits before native HTTP. In-memory-only mutex is insufficient and no DB lock may cross HTTP.

#### Scenario: Same-account command races another dispatch
- **WHEN** command A is dispatched or outcome-unknown and command B targets the same Node/account without override
- **THEN** command B returns `account_operation_in_progress` and sends zero native mutation

### Requirement: Native outcomes SHALL be classified conservatively without automatic redispatch

Known successful terminal 2xx MUST map to normal terminal applied/noop result. Only adapter-reviewed, provably pre-mutation 4xx may map to `failed`. Timeout, connection loss, response loss and ambiguous native 5xx after request may have arrived MUST map to `outcome_unknown`. Control MUST NOT parse raw native strings to infer commit stage and the v7.3.2 adapter MUST NOT manufacture `remote_partial` from native 500.

Every mutation kind follows this rule. A `dispatched` or `outcome_unknown` operation MUST NOT be automatically redispatched after retry, reconciliation or restart. Same-command POST replay returns current projection with 202 and zero remote mutation.

#### Scenario: Native returns ambiguous 500
- **WHEN** native POST returns 500 after the request may have reached auth-file mutation
- **THEN** Control stores `outcome_unknown`, creates no terminal receipt, exposes no raw error and never automatically redispatches

### Requirement: Execution and verification SHALL remain orthogonal

`execution_state` MUST be `prepared|dispatched|remote_applied|remote_noop|remote_partial|outcome_unknown|failed`; `verification_state` MUST be `not_started|pending|verified|timeout|inconclusive`. Native-First v7.3.2 does not produce `remote_partial` from ambiguous 5xx, but the enum remains reserved for future machine-stable protocols. Inventory or read-back MUST NOT retroactively manufacture exact execution proof.

#### Scenario: Unknown execution later converges in Inventory
- **WHEN** Inventory observes the desired business state after an ambiguous native response
- **THEN** verification may record convergence while execution remains `outcome_unknown`

### Requirement: Terminal account receipts SHALL preserve exact original POST replay

Change B MUST use separate immutable `account_admin_command_receipts`, integrity-bound to the global registry and terminal operation; asset receipts remain asset-only. Stable `remote_applied|remote_noop|failed` terminalization, terminal audit and receipt insertion MUST be atomic. `prepared|dispatched|outcome_unknown` MUST have no receipt. `remote_partial` is eligible only under future machine-stable evidence and MUST NOT be created from v7.3.2 ambiguous errors.

Exact same actor/domain/kind/intent terminal replay MUST return stored status and canonical body. Verification and lifecycle override updates MUST NOT rewrite the original receipt. Runtime roles have no unrestricted receipt DML.

#### Scenario: Outcome unknown is replayed
- **WHEN** a same-command POST repeats while execution is `outcome_unknown`
- **THEN** Control returns current projection with 202, creates no receipt and sends zero native mutation

### Requirement: Manual lifecycle override SHALL waive only the lifecycle block

Control MUST expose **Override Unknown Operation Lifecycle Block** for a `dispatched` or unresolved `outcome_unknown` operation. It persists all of `lifecycle_override_at/by/reason`, with reason `process_restarted|node_stopped|risk_accepted`; free-form detail is audit-only. The action MUST NOT change execution/verification state, create success/failure evidence, alter receipt eligibility or authorize redispatch.

`risk_accepted` explicitly waives the strict guarantee that the earlier request cannot mutate the old Node after lifecycle proceeds. Every override requires super_admin, active session, same-origin/CSRF, exact typed confirmation and distinct high-risk audit.

#### Scenario: Administrator accepts unresolved remote risk
- **WHEN** a super_admin submits exact confirmation with reason `risk_accepted`
- **THEN** lifecycle may ignore that operation blocker while execution remains unknown and the old mutation is never redispatched

### Requirement: Account verification SHALL use only normal Inventory convergence

After execution is verification-eligible, Control MAY request the existing scheduler only. Disable requires same account_key disabled=true; Enable requires disabled=false; Remove requires absence from fresh complete eligible provider-complete evidence; Upload New requires identity present; Replace Existing requires identity still present. Inventory MUST NOT be treated as exact credential-byte, CAS or remote-quiescence proof. Stale/incomplete/disk-fallback/duplicate evidence cannot verify.

#### Scenario: Replace identity remains present after response loss
- **WHEN** fresh Inventory still contains the account after an ambiguous Replace response
- **THEN** business convergence may be observed but execution remains `outcome_unknown`

### Requirement: Public API, audit and metrics SHALL remain bounded

Product routes MUST be exactly `POST /api/account-operations/disable`, `/enable`, `/remove`, `/upload-new`, `/replace-existing`, `GET /api/account-operations/{command_id}` and `POST /api/account-operations/{command_id}/lifecycle-override`. Mutation bodies, multipart parts and lifecycle override fields MUST be the closed schemas in design; unknown fields/parts are invalid. Responses MUST be no-store and expose only the bounded operation projection defined in design, never native physical evidence or Secrets.

Terminal success MUST return 200 with `{"operation":...}`; accepted nonterminal and same-command nonterminal replay MUST return 202 with `{"operation":...}` and zero redispatch; GET MUST return 200 current projection; exact terminal replay MUST return the persisted original status/body. Stable errors MUST follow the frozen mapping in design: 400 malformed/upload/identity, 401 authentication, 403 authorization/CSRF, 404 Node/target/operation missing, 409 lifecycle/monitoring/provider/ambiguous/in-progress/command conflicts, 413 upload size and 503 management/service unavailable. An ambiguous native outcome returns the 202 projection with `outcome_unknown`, not a fabricated terminal error.

Audit MUST record actor/request/command/operation/Node/provider/protected account identity and override risk without credential/raw native body/path. Metrics MUST use low-cardinality operation/provider/result/error/execution/verification labels and never email/account_key/command/node/path.

#### Scenario: Native error contains filesystem path or token
- **WHEN** CLIProxyAPI returns a path/token-bearing error
- **THEN** Control maps it to a stable code and stores/exposes none of the raw content
