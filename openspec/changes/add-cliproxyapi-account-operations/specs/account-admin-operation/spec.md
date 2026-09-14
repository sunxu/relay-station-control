## ADDED Requirements

### Requirement: Account operations SHALL remain explicit and data-plane isolated

Control SHALL expose only single-account Antigravity Disable, Enable, Remove, Upload New and Replace Existing, protected operation read, and Override Unknown Operation Lifecycle Block. Mutation and override MUST require active authenticated `super_admin`, same-origin and CSRF; override additionally requires typed high-risk confirmation. Browser MUST NOT receive Node Management Key, raw credential, native basename/auth_index or raw native response. Phase 7 MUST NOT add batch/all mutation, OAuth/Re-auth, automatic repair/move/remove, Gateway mutation, scheduler ownership or data-plane participation.

#### Scenario: Unsupported batch or passthrough action
- **WHEN** a client requests multiple accounts, native all-delete, arbitrary management passthrough, OAuth, repair or move
- **THEN** Control rejects before remote mutation and creates no accepted account operation

### Requirement: Account command identity SHALL use the global registry and actor-first ordering

After authentication/session/super_admin/CSRF, mutations MUST acquire shared global command serialization and perform actor-first registry lookup before target lookup, upload Secret parsing/fingerprinting or Node calls. Cross-actor/domain/kind/intent reuse MUST return `command_conflict` with zero remote mutation. Upload equality MUST use the separate Phase 7 keyed fingerprint and MUST NOT alter asset K1.

Account commands MUST use domain `account_admin`, encoding version 1 and exact kinds `account.disable|account.enable|account.remove|account.upload_new|account.replace_existing|account.lifecycle_override`. Canonical intent MUST use the exact fixed-order compact UTF-8 JSON arrays, UUID/account/confirmation/detail encoding and absent-detail null rule in design. Upload arrays contain only key version 1 plus lowercase HMAC fingerprint, never raw credential. The key file, HMAC domain/input and wrong-key replay behavior MUST follow the exact frozen design; missing or unsafe key fails closed without asset K1 fallback.

#### Scenario: Asset command ID is reused for upload
- **WHEN** an upload uses a UUID reserved by Gateway, Node or Monitoring command
- **THEN** `command_conflict` occurs before credential parsing and no native request is sent

### Requirement: Control SHALL call only the reviewed native CLIProxyAPI subset

Against CLIProxyAPI v7.3.2 at `7fa443dc8bf8ca2f1ffd81c2472deb31b097b697`, Control MUST use only `GET /v0/management/auth-files`, `PATCH /v0/management/auth-files/status` with exact `name/auth_index/disabled`, single-name query `DELETE /v0/management/auth-files?name=...`, and raw-JSON `POST /v0/management/auth-files?name=...`. Management transport MUST remain HTTP-only, fixed-target, bounded, no redirect/proxy/fallback/retry and sanitized.

Control MUST NOT use `DELETE all=true`, multi-name/body delete, multipart native upload, auth-files fields/download/refresh, OAuth routes or generic passthrough. Management Key MUST remain server-side only and absent from durable/observable surfaces.

#### Scenario: Adapter is asked to delete all files
- **WHEN** any Control path attempts native `DELETE all=true` or multiple names
- **THEN** the adapter rejects locally and sends zero native request

### Requirement: Native snapshot SHALL pass runtime identity and physical-target eligibility before safe projection

Every fresh `GET /v0/management/auth-files` response MUST contain exactly one bounded `X-CPA-VERSION` and one bounded `X-CPA-COMMIT`, each byte-for-byte equal to the independently frozen final runtime artifact header value. No prefix normalization or source-tag inference is allowed. Missing, duplicate, malformed, unknown or mismatched identity MUST return `unsupported_node_version` before body interpretation and send zero mutation. Upstream source commit `7fa443dc8bf8ca2f1ffd81c2472deb31b097b697` is the v7.3.2 semantic baseline, not automatically the runtime header commit. Until exact artifact headers and image digest are frozen, Stage 7B is not implementation-ready.

After this gate, Control MUST classify raw entries transiently. `mutation_eligible_targets` accept only manager-backed `source=file`, `runtime_only=false`, auth_index of 1..256 UTF-8 bytes without controls, safe basename and valid provider/email; provider and type must normalize identically when both exist. Memory/runtime-only, incomplete and disk-fallback entries are ineligible for target mutation. `occupancy_evidence` is broader and MUST conservatively include usable evidence of same identity or basename occupancy even when an entry is not mutation eligible. A clean, structurally valid, version-valid empty `files=[]` response may prove Upload New absence, but an existing-target operation returns `account_target_not_found`; degraded or unusable evidence returns `node_management_unavailable`. Eligible entries are then projected to provider/type, normalized email, basename, auth_index and disabled only. Classification fields, source/runtime flags, full path, status message, ID/token-like fields, raw metadata, quota/cooldown/request counters, headers/proxy/note/routing fields, unknown fields and raw objects MUST be discarded and MUST NOT enter PostgreSQL, receipts, audit, logs, traces, metrics or browser/API output.

#### Scenario: Snapshot is disk fallback
- **WHEN** the response has no runtime-manager-backed source/runtime/auth_index evidence
- **THEN** Control returns `node_management_unavailable`, sends zero mutation and persists/exposes no transient classification field

#### Scenario: Runtime identity header mismatches
- **WHEN** either version or exact pinned runtime commit header is absent, duplicated, malformed or mismatched
- **THEN** Control returns `unsupported_node_version` before interpreting snapshot content and sends zero mutation

### Requirement: Account operations SHALL resolve exactly one fresh native target

Disable, Enable, Remove and Replace Existing MUST read a fresh safe snapshot immediately before dispatch and match canonical provider plus normalized email for `(node_instance_id,account_key)`. Zero matches returns `account_target_not_found`; more than one returns `account_target_ambiguous`; one match yields ephemeral name/auth_index. Control MUST NOT choose first/latest, infer a filename or use Inventory alone as physical target truth. Name/auth_index MUST NOT become durable/public business identity.

#### Scenario: Duplicate native auth files normalize to one account
- **WHEN** two safe snapshot entries match the requested provider and normalized email
- **THEN** Control returns `account_target_ambiguous` and sends zero mutation

### Requirement: Upload SHALL preserve CLIProxyAPI credential schema ownership and Control runtime boundaries

Control MUST accept one top-level Antigravity JSON credential of at most 1048576 bytes, require `type=antigravity`, require email and match its normalized value to the intended account identity. CLIProxyAPI remains credential schema truth; new provider credential fields may pass through.

Control MUST canonicalize recognized v7.3.2 metadata aliases and reject, without silent stripping, canonical runtime/routing/management keys: `disabled,weight,priority,headers,request_retry,excluded_models,proxy_url,note,websockets,prefix,models,disable_cooling,fingerprint_profile,base_url,model_aliases,request_scoped_errors,tool_prefix_disabled`. Legacy aliases `request-retry,excluded-models,proxy-url,disable-cooling,fingerprint-profile,base-url,model-aliases,request-scoped-errors,tool-prefix-disabled` receive the same rejection. `api_key/api-key` is provider credential material and is not denied solely by this list. The public multipart aggregate is at most 1073152 bytes with one request part at most 8192 bytes, one credential part at most 1048576 bytes and at most 16384 bytes of framing.

#### Scenario: Upload includes a provider extension and runtime controls
- **WHEN** credential contains a new provider field and also `proxy_url`
- **THEN** Control rejects `upload_invalid` because of the runtime field and does not silently strip or send the body

### Requirement: Create and Replace SHALL use bounded native basenames

Upload New MUST generate `antigravity-<normalized_email>.json`, measure UTF-8 bytes, enforce `MAX_NATIVE_BASENAME_BYTES=255` and `MAX_CREATE_EMAIL_BYTES=238`, and then apply the same non-empty basename-only, no slash/reverse-slash/NUL/control/traversal validator as Replace. It prohibits truncation, hash fallback and browser-selected path. Replace Existing MUST inherit and validate the exact fresh native basename.

#### Scenario: Create email exceeds physical filename admission
- **WHEN** normalized email is 239 UTF-8 bytes or generated basename exceeds 255 bytes
- **THEN** Control returns `invalid_request` with zero native request

### Requirement: Create and Replace SHALL accept native last-writer-wins

Upload New MUST require one fresh version-valid non-degraded snapshot to prove both expected identity absent and generated basename unoccupied by all usable `occupancy_evidence`. Existing identity is `409 account_target_exists`; another identity occupying the basename is `409 account_filename_conflict`; both send zero POST. It then issues native POST as best-effort create. There is no atomic create-if-absent guarantee: a concurrent writer may create after snapshot and native POST may overwrite it. Replace Existing MUST resolve exactly one entry and POST to its basename as best-effort replace; a native credential refresh may occur between snapshot and POST and may be overwritten. Phase 7 v1 explicitly accepts both races and MUST NOT claim CAS, ETag, revision, incarnation or postcondition proof.

#### Scenario: Native refresh races Replace
- **WHEN** credential refresh commits after Control snapshot but before administrator POST
- **THEN** native last-writer-wins may overwrite the refresh and Control does not claim lost-update prevention

### Requirement: Dispatch SHALL use durable same-account serialization and Node-first eligibility

Before native mutation, one short PostgreSQL transaction MUST lock Node first, require active lifecycle, lock/read current non-cancelled monitoring under the existing graph, require `management_account_inventory_read` and matching active Provider policy, acquire/check durable serialization for `(node_instance_id,account_key)`, reject any other `dispatched` or unresolved `outcome_unknown` operation regardless of lifecycle override, lock the prepared operation, persist dispatch start and transition `prepared -> dispatched`. It then commits before native HTTP. In-memory-only mutex is insufficient and no DB lock may cross HTTP.

#### Scenario: Same-account command races another dispatch
- **WHEN** command A is dispatched or outcome-unknown and command B targets the same Node/account, including after a lifecycle override
- **THEN** command B returns `account_operation_in_progress` and sends zero native mutation

### Requirement: Disable and Enable no-op SHALL be decided before native dispatch

After command acceptance, runtime gate, fresh exactly-one resolution and dispatch eligibility, Disable with safe snapshot disabled=true or Enable with disabled=false MUST send zero PATCH and terminalize `remote_noop` with normal audit and exact terminal replay. If desired state differs, Control transitions to dispatched and sends PATCH exactly once; stable 2xx is `remote_applied` and MUST NOT be reclassified as noop from read-back.

#### Scenario: Disable is already satisfied
- **WHEN** the eligible fresh target projection has disabled=true
- **THEN** Control sends zero PATCH and atomically records `remote_noop`, audit and exact terminal receipt

### Requirement: Native outcomes SHALL be classified conservatively without automatic redispatch

Known successful terminal 2xx after a mutation request MUST map to `remote_applied`. Only adapter-reviewed, provably pre-mutation 4xx may map to `failed`. Timeout, connection loss, response loss and ambiguous native 5xx after request may have arrived MUST map to `outcome_unknown`. Control MUST NOT parse raw native strings to infer commit stage.

Every mutation kind follows this rule. A `dispatched` or `outcome_unknown` operation MUST NOT be automatically redispatched after retry, reconciliation or restart. Same-command POST replay returns current projection with 202 and zero remote mutation.

#### Scenario: Native returns ambiguous 500
- **WHEN** native POST returns 500 after the request may have reached auth-file mutation
- **THEN** Control stores `outcome_unknown`, creates no terminal receipt, exposes no raw error and never automatically redispatches

### Requirement: Account execution truth SHALL remain independent of Inventory observation

`execution_state` MUST be `prepared|dispatched|remote_applied|remote_noop|outcome_unknown|failed`. Phase 7 MUST NOT add durable verification state or a verification workflow. Inventory or read-back MUST NOT retroactively manufacture exact execution proof or change `execution_state`.

The pinned v7.3.2 `POST /v0/management/auth-files` route has a reviewed pre-mutation exception: HTTP 503 caused by `authManager == nil` before credential-body read/write MUST map to terminal `failed` with `node_management_unavailable`, create the normal terminal receipt and perform zero credential mutation. This route/status/artifact rule MUST NOT be generalized to other native 503 responses; unreviewed 5xx remains `outcome_unknown`.

#### Scenario: Unknown execution later converges in Inventory
- **WHEN** Inventory observes the desired business state after an ambiguous native response
- **THEN** the separate observation surface may show convergence while execution remains `outcome_unknown` and no account operation state is written

### Requirement: Terminal account receipts SHALL preserve exact original POST replay

Change B MUST use separate immutable `account_admin_command_receipts`, integrity-bound to the global registry and target operation; asset receipts remain asset-only. Stable `remote_applied|remote_noop|failed` mutation terminalization, terminal audit and receipt insertion MUST be atomic. `prepared|dispatched|outcome_unknown` MUST have no receipt. The receipt schema MUST also support independent `account.lifecycle_override` command IDs whose target operation ID differs from the receipt command ID or is null for a terminal target-not-found failure after actor-first reservation.

Exact same actor/domain/kind/intent terminal replay MUST return stored status and canonical body. Inventory observations and lifecycle override updates MUST NOT rewrite the original receipt. Runtime roles have no unrestricted receipt DML.

#### Scenario: Outcome unknown is replayed
- **WHEN** a same-command POST repeats while execution is `outcome_unknown`
- **THEN** Control returns current projection with 202, creates no receipt and sends zero native mutation

### Requirement: Manual lifecycle override SHALL waive only the lifecycle block

Control MUST expose **Override Unknown Operation Lifecycle Block** for a `dispatched` or unresolved `outcome_unknown` operation. The path UUID identifies that target operation and the request body contains a new independent command UUID. The override command MUST use global actor-first reservation with domain `account_admin`, kind `account.lifecycle_override`, encoding v1 and the exact canonical intent defined in design before changing the target operation. It persists all of `lifecycle_override_at/by/reason`, with reason `process_restarted|node_stopped|risk_accepted`; free-form detail is audit-only. The action MUST NOT change execution state, authorize redispatch or unblock any same-account operation.

`risk_accepted` explicitly waives the strict guarantee that the earlier request cannot mutate the old Node after lifecycle proceeds. Every override requires super_admin, active session, same-origin/CSRF, exact typed confirmation and distinct high-risk audit.

The first valid override mutation, audit and its own terminal receipt commit atomically. A target-not-found or invalid-target-state terminal failure after global reservation also commits its own immutable receipt. Exact same override command replay returns that receipt with zero mutation. A different later override command returns terminal `409 lifecycle_override_already_set`, records its own exact receipt and MUST NOT alter the first override reason. The original account mutation receipt is never reused or overwritten.

#### Scenario: Administrator accepts unresolved remote risk
- **WHEN** a super_admin submits exact confirmation with reason `risk_accepted`
- **THEN** lifecycle may ignore that operation blocker while execution remains unknown and the old mutation is never redispatched

#### Scenario: New mutation follows lifecycle override
- **WHEN** another account mutation targets the same Node/account while the overridden operation remains dispatched or outcome-unknown
- **THEN** durable same-account serialization still returns `account_operation_in_progress` with zero native request

### Requirement: Public API, audit and metrics SHALL remain bounded

Product routes MUST be exactly `POST /api/account-operations/disable`, `/enable`, `/remove`, `/upload-new`, `/replace-existing`, `GET /api/account-operations/{command_id}` and `POST /api/account-operations/{operation_command_id}/lifecycle-override`. Mutation bodies, multipart parts and lifecycle override fields MUST be the closed schemas in design; the override body includes its own independent `command_id`. Unknown fields/parts are invalid. Responses MUST be no-store and expose only the bounded operation projection defined in design, never native physical evidence or Secrets.

Terminal success MUST return 200 with `{"operation":...}`; accepted nonterminal and same-command nonterminal replay MUST return 202 with `{"operation":...}` and zero redispatch; GET MUST return 200 current projection; exact terminal replay MUST return the persisted original status/body. Stable errors MUST follow the frozen mapping in design: 400 malformed/upload/identity, 401 authentication, 403 authorization/CSRF, 404 Node/target/operation missing, 409 lifecycle/monitoring/provider/target-exists/filename/ambiguous/in-progress/override-set/command conflicts, 413 upload size and 503 management/version/service unavailable. An ambiguous native outcome returns the 202 projection with `outcome_unknown`, not a fabricated terminal error.

Audit MUST record actor/request/command/operation/Node/provider/protected account identity and override risk without credential/raw native body/path. Metrics MUST use low-cardinality operation/provider/result/error/execution labels and never email/account_key/command/node/path.

#### Scenario: Native error contains filesystem path or token
- **WHEN** CLIProxyAPI returns a path/token-bearing error
- **THEN** Control maps it to a stable code and stores/exposes none of the raw content
