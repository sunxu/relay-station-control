## ADDED Requirements

### Requirement: Account operations SHALL remain explicit, single-account and data-plane isolated

Control SHALL expose only explicit Antigravity Disable, Enable, Remove, Upload New and Replace Existing administrator actions plus a protected operation-status read. Every mutation MUST require active authenticated `super_admin`, same-origin/CSRF and global `command_id`; browser MUST NOT receive Node Management Key or raw credential. Phase 7 MUST NOT add batch/all mutation, OAuth/Re-auth, automatic repair/move/remove, Credential Vault, Gateway Account/Group mutation, scheduler ownership or AI request data-plane participation.

#### Scenario: Unsupported batch or repair action
- **WHEN** a client requests multiple accounts, `all=true`, OAuth/repair/move or arbitrary Node management behavior
- **THEN** Control rejects the request before remote mutation and creates no Phase 7 operation

### Requirement: Account command identity SHALL use the global registry and actor-first ordering

After authentication/session/super_admin/CSRF, account mutations MUST acquire the shared global command serialization and perform actor-first registry lookup before target resolution, upload Secret parsing/fingerprinting or Node management calls. A cross-actor or cross-domain/kind reuse MUST return `command_conflict` with zero remote mutation. New accepted account commands atomically reserve the global ID and create one `account_admin_operations` row.

#### Scenario: Existing asset command ID reused for upload
- **WHEN** an upload request uses a UUID reserved by an existing Gateway/Node/Monitoring command
- **THEN** `command_conflict` is returned before file Secret validation and no Node call occurs

### Requirement: Account operations SHALL resolve exactly one current physical target

Disable/Enable/Remove/Replace MUST resolve `(node_instance_id, canonical account_key)` against a current Node management snapshot and require exactly one normalized provider/email match. Zero matches MUST return `account_target_not_found`; multiple matches MUST return `account_target_ambiguous`. Control MUST NOT infer a target from filename conventions, first/latest record or stale Inventory alone. Mutation MUST include the opaque physical-target precondition returned by the Node management contract; mismatch returns `account_target_changed` with zero mutation.

#### Scenario: Duplicate account files on one Node
- **WHEN** two current physical records normalize to the same account_key
- **THEN** Control fails closed as ambiguous and does not choose either record

### Requirement: Account dispatch SHALL require lifecycle and monitoring eligibility

Before remote mutation, Control MUST first perform fresh authenticated `GET /v0/management/account-contract/v1` discovery and require `contract=node-account-management`, `version=v1`, provider `antigravity`, capabilities `management_account_inventory_read` and `management_account_mutation_v1`, and exact constants `max_credential_bytes=262144`, `max_request_read_duration_ms=5000`, `max_mutation_duration_ms=15000`, `max_quiescence_duration_ms=25000`, `min_post_commit_quiescence_reserve_ms=10000`. A missing capability, unsupported store, version/constant mismatch, invalid response or unavailable discovery MUST return `unsupported_node_contract` and send zero resolve/mutation request. Artifact metadata MUST NOT substitute for discovery.

After successful discovery, a short transaction MUST lock the Node first, verify active lifecycle, then lock/read current monitoring according to the existing Node-first graph and require a current non-cancelled activation, both required persisted Node capabilities and matching active Provider policy. It MUST then reject a same-target live operation, lock the prepared account operation, persist dispatch/quiescence metadata and atomically transition `prepared -> dispatched`. No DB lock is held across HTTP. Runtime discovery and DB authorization are independent mandatory gates.

#### Scenario: Active Node monitoring is disabled
- **WHEN** Node lifecycle is active but no current eligible monitoring activation exists
- **THEN** Control returns `node_monitoring_ineligible`, keeps zero live dispatch fence and sends zero remote request

#### Scenario: Monitoring Disable wins the Node lock
- **WHEN** administrator Disable commits before account dispatch authorization obtains the Node lock
- **THEN** account dispatch re-reads monitoring ineligible and sends zero remote request

#### Scenario: Runtime contract differs from persisted capability
- **WHEN** the Node row advertises mutation capability but fresh authenticated discovery omits it or returns any mismatched v1 constant
- **THEN** Control returns `unsupported_node_contract`, sends zero target resolve/mutation request and does not treat commit headers as authorization

### Requirement: Remote mutation SHALL remain fenced until quiescence is proven

A dispatched account operation MUST persist a durable UUID `dispatch_token` plus bounded `dispatch_deadline`, `remote_mutation_deadline` and conservative `quiescence_deadline`. Control MUST send that exact lowercase UUID as Node `dispatch_token_v1`. Client timeout or any deadline expiry MUST NOT by itself prove remote stop. The selected Node Contract v1 MUST guarantee synchronous bounded mutation, no background credential mutation after handler quiescence, no irreversible mutation start after its server-side budget, and durable same-token recovery fencing. Node Retire/Replace MUST reject `account_operation_in_progress` while a same-Node dispatched operation lacks proven quiescence.

After response loss, Control MUST perform an authenticated resolve using the same UUID as `fence_dispatch_token_v1`. Quiescence is proven only if Node successfully acquires its shared mutation gate, durably persists that token fence, and completes sanitized target/postcondition read-back; a late mutation carrying the token is then rejected before target lookup or mutation. A timeout, unavailable Node, invalid contract response, or elapsed `quiescence_deadline` leaves the lifecycle fence active. The other permitted proofs are a terminal mutation response with `quiescent=true` or proven termination/restart of the exact Node process instance.

#### Scenario: Control times out while Node handler continues
- **WHEN** the HTTP client times out but Node-side bounded mutation may still be running
- **THEN** operation becomes/remains outcome-unknown, Retire/Replace remains blocked, and no automatic redispatch occurs

#### Scenario: Restart before remote quiescence
- **WHEN** Control crashes after dispatch and restarts before quiescence proof
- **THEN** the durable fence is restored and Node lifecycle mutation remains blocked

#### Scenario: Recovery resolve overtakes a late mutation request
- **WHEN** a fenced resolve reaches Node gate admission before the earlier-dispatched HTTP mutation carrying the same token
- **THEN** Node durably fences the token, completes recovery read-back, and rejects the late mutation with zero credential mutation

### Requirement: Remote execution and verification SHALL use orthogonal durable states

`execution_state` MUST be one of `prepared|dispatched|remote_applied|remote_noop|remote_partial|outcome_unknown|failed`; `verification_state` MUST be `not_started|pending|verified|timeout|inconclusive`. Successful dispatch authorization MUST transition directly `prepared -> dispatched`; there is no hidden durable `dispatch_authorized` state. No third duplicate durable overall-state truth may be persisted.

#### Scenario: Mutation succeeds but Inventory never proves convergence
- **WHEN** remote execution is confirmed but accepted Inventory evidence does not arrive before the verification deadline
- **THEN** execution remains `remote_applied` while verification becomes `timeout`

### Requirement: Terminal account receipts SHALL preserve exact original POST replay

Change B MUST create a separate immutable `account_admin_command_receipts` relation keyed and integrity-bound to `admin_command_registry`; it MUST NOT add account commands to `asset_admin_command_receipts`. Receipt rows MUST preserve actor, `account_admin` domain, kind, intent encoding/hash/key version, HTTP status, `application/json` content type, exact canonical response bytes and DB commit time. Runtime roles MUST have no unrestricted INSERT/UPDATE/DELETE/TRUNCATE privilege, and controlled insertion MUST reject a missing/divergent registry reservation or nonterminal operation.

Only `remote_applied`, `remote_noop`, `remote_partial` and `failed` are receipt-eligible. Transition to one of those states, terminal audit and receipt insertion MUST commit atomically before the response is sent. `prepared`, `dispatched` and `outcome_unknown` MUST NOT have a terminal receipt. A deterministic failure after operation acceptance but before remote dispatch becomes terminal `failed` with its exact mapped response; a failure before request acceptance/global reservation creates neither operation nor receipt. `remote_partial` is terminal and persists its exact `502` response.

Exact same actor/domain/kind/intent terminal replay MUST return the stored HTTP status and response bytes. Mutable recovery or verification updates MUST NOT rewrite the receipt; current verification is available only from the operation GET projection.

#### Scenario: Response is lost with unknown outcome
- **WHEN** the remote response is lost and recovery has not established a terminal execution result
- **THEN** execution remains `outcome_unknown`, no terminal receipt exists, and same-command POST replay returns `202` current projection with zero redispatch

#### Scenario: Remote partial terminalization
- **WHEN** Node proves a physical commit followed by a bounded runtime or marker partial failure
- **THEN** Control atomically records `remote_partial`, terminal audit and immutable `502 remote_partial` receipt, then exact replay returns those persisted bytes

#### Scenario: Failure before remote dispatch after acceptance
- **WHEN** a command and operation were accepted but a deterministic dispatch prerequisite fails before any Node mutation request
- **THEN** Control records terminal `failed` and its exact mapped receipt atomically, with zero remote mutation

### Requirement: Account operation HTTP surfaces SHALL have a frozen exact contract

The routes MUST be exactly `POST /api/account-operations/disable`, `POST /api/account-operations/enable`, `POST /api/account-operations/remove`, `POST /api/account-operations/upload-new`, `POST /api/account-operations/replace-existing` and `GET /api/account-operations/{command_id}`.

JSON bodies MUST be at most 8192 bytes. Disable and Enable bodies MUST contain exactly lowercase UUID `command_id`, UUID `node_instance_id` and canonical `account_key` of at most 385 UTF-8 bytes. Remove MUST additionally contain exact `confirmation="REMOVE"`. Upload New and Replace Existing MUST use multipart of at most 270336 bytes with exactly one `request` part (`application/json`, at most 8192 bytes) and one `credential` part (at most 262144 bytes); request contains exactly `command_id`, `node_instance_id`, `account_key`, and credential identity MUST match. Unknown fields and parts are invalid.

The public operation projection MUST contain exactly `command_id`, `node_instance_id`, `account_key`, `operation_kind`, `execution_state`, `verification_state`, nullable `result`, nullable `error_code`, `created_at` and `updated_at`. `operation_kind` MUST be `disable|enable|remove|upload_new|replace_existing`; `result` MUST be null or `applied|noop|partial|failed`; timestamps MUST be UTC RFC3339. `prepared|dispatched` use null result/error, `remote_applied` uses `applied`, `remote_noop` uses `noop`, `remote_partial` uses `partial/remote_partial`, `outcome_unknown` uses null/`remote_outcome_unknown`, and `failed` uses `failed/<fixed error>`. The projection MUST exclude Management Key, dispatch token, target precondition, postcondition proof, raw Node response, filename/path and credential. Terminal success returns `200 {"operation":...}`; nonterminal acceptance/replay returns `202 {"operation":...}`; terminal partial returns `502` with sanitized error and operation; GET returns `200` current projection or `404 operation_not_found`. Every response MUST use `Cache-Control: no-store`.

The stable status mapping MUST be: `400 invalid_request`; `401 unauthenticated`; `403 forbidden`; `404 node_not_found|account_target_not_found|operation_not_found`; `409 command_conflict|node_retired|node_monitoring_ineligible|unsupported_node_contract|unsupported_provider|account_target_ambiguous|account_target_changed|account_operation_in_progress`; `413 upload_too_large`; `422 upload_invalid|identity_mismatch`; `502 remote_partial|remote_failed`; `503 node_management_unavailable|service_unavailable`. `remote_outcome_unknown` MUST be represented as a `202` projection. Error text is bounded and sanitized.

#### Scenario: Same command is still nonterminal
- **WHEN** the same actor and intent repeats a POST whose operation is `prepared`, `dispatched` or `outcome_unknown`
- **THEN** Control returns `202` with the current public projection, sends no new remote request and creates no terminal receipt

#### Scenario: Exact terminal replay after verification changes
- **WHEN** a terminal command is replayed after its mutable verification state changed
- **THEN** POST returns the original receipt status/body exactly and GET returns the newer verification projection

### Requirement: Disable and Enable SHALL confirm durable Node state before remote success

Disable/Enable remote success MUST require native mutation success, reliable persistence-error propagation and sanitized management read-back matching the desired `disabled` value. Already desired state is `remote_noop`. Enabled MUST NOT be interpreted as provider health or Control schedulability.

#### Scenario: Runtime update succeeds but persistence fails
- **WHEN** Node cannot durably persist the requested status
- **THEN** Control MUST NOT classify the operation as `remote_applied`; it returns/persists a bounded partial/failure outcome

### Requirement: Remove SHALL remain destructive and response-loss safe

Remove MUST represent physical credential-file deletion with no Control trash/vault. Partial disk/runtime cleanup MUST be `remote_partial`. Requests that already obtained credential MAY finish under native behavior. Response-loss recovery MUST NOT delete a new target that appears after the original target/precondition, and no dispatched operation may retarget a replacement Node.

#### Scenario: New target appears after lost Remove response
- **WHEN** the old precondition target is gone and a new record for the same account_key exists
- **THEN** the original command does not issue another delete against the new target and remains conservatively recovered/inconclusive

### Requirement: Upload SHALL be bounded, strict and Secret-safe

Upload New/Replace MUST accept exactly one Antigravity JSON file of at most 262144 bytes. Client fields MUST be exactly the approved credential allowlist (`type,access_token,refresh_token,expires_in,timestamp,expired,email,project_id`); unknown and runtime-control metadata MUST be rejected. Create MUST use server canonical filename and refuse existing target; Replace MUST inherit the uniquely resolved target and enforce precondition. Control MUST keep bytes only in bounded memory and MUST NOT persist/log/audit/trace/metric/return credential content.

#### Scenario: Upload contains disabled or unknown field
- **WHEN** repaired JSON includes `disabled`, `weight`, headers/retry/scheduler metadata or any unapproved field
- **THEN** Control/Node rejects `upload_invalid` before credential mutation; it does not silently strip or pass through the field

### Requirement: Upload intent SHALL use a separate Phase 7 keyed fingerprint contract

Upload command equality MUST use a versioned HMAC fingerprint under a dedicated 32-byte `CONTROL_ACCOUNT_OPERATION_INTENT_KEY_FILE` with domain `relay-station/account-operation-upload-intent/v1`. The existing asset K1 file/domain/semantics MUST remain unchanged. Missing/unsafe/symlink/wrong-length Phase 7 key when upload fingerprinting is required MUST fail closed without automatic generation.

#### Scenario: Existing asset K1 is available but Phase 7 key is missing
- **WHEN** a new upload command requires Secret fingerprinting and only the asset K1 exists
- **THEN** Control returns `service_unavailable`; it MUST NOT fall back to the asset K1

### Requirement: Create and Replace SHALL require Secret-safe remote postcondition proof

Node Contract v1 MUST expose a bounded non-secret write/postcondition proof that can be read after response loss and proves that this dispatch committed the intended physical target. Control MAY send/store a non-secret write token and its own keyed upload-intent fingerprint; Node MUST NOT require the Control fingerprint key. Inventory account presence alone MUST NOT prove credential replacement. Upload/Replace verification is `verified` only when the remote proof matches this dispatch and normal accepted Inventory shows exactly one expected account_key.

#### Scenario: Inventory sees account but remote write proof is absent
- **WHEN** a fresh complete snapshot contains the expected account_key but Control cannot prove this Create/Replace dispatch committed
- **THEN** verification is not `verified`; it remains outcome-unknown/inconclusive according to execution evidence

### Requirement: Account verification SHALL use only the normal Inventory pipeline

After execution becomes verification-eligible, Control MAY wake/request the existing scheduler but MUST NOT create off-grid/duplicate/special Phase 7 runs, bypass start grace/lease/fencing/policy pinning or patch current Inventory directly. Verification deadline is ten minutes. Disable/Enable require desired state in fresh complete eligible Provider evidence; Remove requires absence only from fresh complete eligible provider-complete evidence; Create/Replace require both remote proof and exactly-one expected identity. Incomplete/stale/disk-fallback/duplicate evidence cannot prove success.

#### Scenario: Current fixed slot already exists
- **WHEN** Phase 7 requests a verification opportunity while the current `(Node,scheduled_at)` run already exists in any state
- **THEN** scheduler creates no second run; verification uses that normal run if eligible or waits for the next normal aligned slot

### Requirement: Account operation audit, metrics and error surfaces SHALL be bounded

Audit MUST record actor/request/command/operation/Node/provider/protected business identity, sanitized outcome and verification state without credential, Management Key, raw Node body or full filesystem path. Remove uses a distinct high-risk action. Metrics MUST use only low-cardinality operation/provider/result/error/execution/verification classes and MUST NOT label email/account_key/file/path/command/node UUID. API errors MUST use a fixed sanitized taxonomy and never relay raw Node strings.

#### Scenario: Node returns raw internal error
- **WHEN** Node management fails with a path/token-bearing internal error
- **THEN** Control maps it to the approved bounded error class, does not expose/store the raw body and records only sanitized evidence
