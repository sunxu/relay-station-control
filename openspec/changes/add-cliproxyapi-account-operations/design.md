## Context

Phase 7 is the first Relay Station phase that intentionally invokes remote credential-side mutations. Unlike Phase 6 PostgreSQL-only lifecycle commands, a Node may apply a mutation and the Control response may be lost. The design therefore separates global command identity, Control execution/recovery state, Node native credential truth and Inventory observation.

## Goals / Non-Goals

### Goals

- explicit Antigravity Disable/Enable/Remove/Create/Replace;
- exact business identity `(node_instance_id, account_key)` with fresh exactly-one target resolution;
- no Secret persistence in Control;
- global command/actor-first behavior through Change A;
- deterministic response-loss and concurrent-target behavior;
- lifecycle safety: no irreversible old-Node mutation after Retire/Replace commit;
- normal Inventory verification only.

### Non-Goals

No repair automation, OAuth, move, batch/all, credential vault, scheduler ownership, special Inventory truth, Gateway mutation or generic workflow framework.

## Decisions

### 1. Product API shape

The protected v1 product routes are frozen as:

```text
POST /api/account-operations/disable          JSON {command_id,node_instance_id,account_key}
POST /api/account-operations/enable           JSON {command_id,node_instance_id,account_key}
POST /api/account-operations/remove           JSON {command_id,node_instance_id,account_key,confirmation}
POST /api/account-operations/upload-new       multipart: request,credential
POST /api/account-operations/replace-existing multipart: request,credential
GET  /api/account-operations/{command_id}
```

JSON requests are at most 8192 bytes and reject unknown fields. Disable/Enable require exactly canonical lowercase UUID `command_id`, UUID `node_instance_id` and canonical `account_key` of at most 385 UTF-8 bytes; Remove additionally requires `confirmation="REMOVE"`. Each upload multipart body is at most 270336 bytes and contains exactly one `request` part (`application/json`, at most 8192 bytes) and one `credential` part (at most 262144 bytes). The Upload New request object contains `command_id`, `node_instance_id` and `account_key`; Replace Existing uses the same fields and resolves that existing account. Credential identity MUST equal the request account identity.

POST routes require active authenticated `super_admin`, same-origin and CSRF. `GET` is protected/read-only. Every response, including errors, carries `Cache-Control: no-store`. Browser never receives Node Management Key, dispatch token, target precondition, postcondition proof, raw Node response, path or credential.

The public operation projection is exactly `command_id`, `node_instance_id`, `account_key`, `operation_kind`, `execution_state`, `verification_state`, nullable `result`, nullable `error_code`, `created_at` and `updated_at`. `operation_kind` is `disable|enable|remove|upload_new|replace_existing`; `result` is null or `applied|noop|partial|failed`; timestamps are UTC RFC3339 strings. `prepared|dispatched` use null result/error; `remote_applied` uses `applied`; `remote_noop` uses `noop`; `remote_partial` uses `partial/remote_partial`; `outcome_unknown` uses null/`remote_outcome_unknown`; `failed` uses `failed/<fixed error code>`. A terminal success returns `200 {"operation":<projection>}`. A nonterminal accepted command (`prepared`, `dispatched` or `outcome_unknown`) returns `202` with the same body shape; exact same-command nonterminal replay returns the current projection with `202` and zero redispatch. A terminal `remote_partial` returns `502 {"error":{"code":"remote_partial","message":<sanitized>},"operation":<projection>}`. Other terminal failures use the fixed mapping in Decision 18 and include the operation projection. `GET` returns `200 {"operation":<current projection>}` or `404 operation_not_found`; it is the only surface that exposes later verification updates.

### 2. Global command acceptance

Change B depends on `add-global-admin-command-registry`. After auth/session/super_admin/CSRF, Control acquires the shared UUID-derived advisory serialization and performs actor-first global lookup before target resolution, upload Secret parsing/fingerprinting or Node calls.

New valid account command reservation and `account_admin_operations` creation occur atomically in one short DB transaction. Existing nonterminal same-actor/domain/intent POST retries return the current operation projection and MUST NOT redispatch solely because the POST repeated. Cross-actor or cross-domain/kind reuse is `command_conflict`.

Terminal immutable exact replay evidence remains separate from mutable operation state. Change B SHALL add `account_admin_command_receipts`; it MUST NOT broaden the asset-only `asset_admin_command_receipts` contract. Receipt eligibility is exact: `remote_applied`, `remote_noop`, `remote_partial` and `failed` are terminal and require a receipt; `prepared`, `dispatched` and `outcome_unknown` are nonterminal and MUST NOT have one. `outcome_unknown` remains recoverable and repeated POST returns `202` current projection without redispatch until recovery establishes a terminal execution state.

A failure before request acceptance/global reservation creates no operation and no receipt. Once a new command and operation are atomically accepted, a deterministic failure before remote dispatch transitions the operation to `failed` and atomically materializes the exact mapped terminal HTTP status/body. `remote_partial` is terminal and atomically materializes its `502` response. Transition to any terminal execution state, terminal audit and receipt insertion occur in one transaction; failure rolls all three back. The original POST terminal response is sent only after that transaction commits.

The immutable receipt stores the exact canonical response bytes and status returned by the original POST. Exact same actor/domain/kind/intent replay returns those bytes and status without reconstructing current operation or verification truth. Later `verification_state`, `verified_at` or other recovery/verification updates change only `account_admin_operations` and MUST NOT update the receipt. Runtime roles receive no direct receipt DML.

The additive receipt schema is frozen as:

```text
command_id uuid PRIMARY KEY FK admin_command_registry(command_id) AND FK account_admin_operations(command_id)
actor_admin_id uuid NOT NULL
command_domain text NOT NULL CHECK = 'account_admin'
command_kind text NOT NULL
intent_encoding_version smallint NOT NULL
canonical_intent_hash bytea NOT NULL CHECK length=32
secret_fingerprint_key_version smallint NULL
http_status smallint NOT NULL
content_type text NOT NULL CHECK = 'application/json'
response_body bytea NOT NULL
committed_at timestamptz NOT NULL
```

Registry actor/domain/kind/encoding/hash/key-version integrity is enforced at the database boundary by a composite FK/controlled insertion contract. UPDATE, DELETE and TRUNCATE are rejected; only the controlled terminalization path may insert. A receipt without the matching global reservation or matching terminal account operation fails closed.

### 3. Exact planned account operation schema

The additive migration SHALL create one row per Phase 7 command, keyed/FK to global registry:

```text
command_id uuid PRIMARY KEY FK admin_command_registry(command_id)
node_instance_id uuid NOT NULL FK relay_node_assets(instance_id)
account_key text NOT NULL, canonical and bounded
operation_kind text CHECK IN ('disable','enable','remove','upload_new','replace_existing')
execution_state text CHECK IN ('prepared','dispatched','remote_applied','remote_noop','remote_partial','outcome_unknown','failed')
verification_state text CHECK IN ('not_started','pending','verified','timeout','inconclusive')
target_precondition text NULL, opaque/bounded/non-secret
dispatch_token uuid NULL
dispatch_started_at timestamptz NULL
dispatch_deadline timestamptz NULL
remote_mutation_deadline timestamptz NULL
quiescence_deadline timestamptz NULL
remote_quiesced_at timestamptz NULL
remote_result_code text NULL, fixed enum
postcondition_proof text NULL, opaque/bounded/non-secret
upload_fingerprint_key_version smallint NULL
upload_intent_fingerprint bytea NULL CHECK length=32
verification_started_at timestamptz NULL
verification_deadline timestamptz NULL
verified_at timestamptz NULL
created_at timestamptz NOT NULL
updated_at timestamptz NOT NULL
```

No raw filename/path/auth JSON/token/Management Key/raw Node body is stored. The exact SQL constraint matrix SHALL enforce state/metadata shape and monotonic transitions through controlled functions; runtime roles receive no direct unrestricted DML.

### 4. Orthogonal state axes

`execution_state` and `verification_state` are independent. Successful dispatch authorization atomically transitions `prepared -> dispatched`; there is no durable `dispatch_authorized` state. `remote_applied + verification timeout` and `outcome_unknown + verification inconclusive` are valid combinations. No third duplicate durable overall-state truth is stored; UI derives a product projection.

### 5. Target resolution and same-target concurrency

Every operation resolves the current CLIProxyAPI management snapshot by normalized provider/email and requires exactly one match. Missing -> `account_target_not_found`; duplicate -> `account_target_ambiguous`.

The Node Contract v1 returns an opaque physical-target precondition that changes on content/target replacement, rename or removal. Node mutation handlers perform lookup->precondition compare->mutation in one management critical section. Control never guesses a filename.

Dispatch authorization holds the Node DB lock and rejects another same `(node,account_key)` operation whose execution is `dispatched` and not quiesced. Node-side mutation serialization/precondition remains the final remote fence.

### 6. Monitoring / lifecycle dispatch authorization

Short DB transaction order is frozen:

```text
lock Node
-> validate lifecycle=active
-> lock/read monitoring according to existing Node-first graph
-> validate current non-cancelled monitoring eligibility
-> validate management_account_inventory_read + management_account_mutation_v1 capabilities + active Provider policy
-> reject any same-target live operation
-> lock current operation
-> validate prepared
-> persist dispatch/quiescence metadata
-> execution_state=dispatched
COMMIT
```

Failure before commit sends zero mutation request and creates no live dispatch fence. `node_monitoring_ineligible` is the fixed product error for missing current eligible monitoring, Inventory-read capability or active Provider policy. Missing `management_account_mutation_v1` or any runtime contract mismatch is `unsupported_node_contract`. Control never auto-enables monitoring or silently adds Node capabilities.

Before this dispatch-authorization transaction, Control performs a fresh authenticated `GET /v0/management/account-contract/v1` against the selected fixed Node target. It requires `contract=node-account-management`, `version=v1`, provider `antigravity`, both `management_account_inventory_read` and `management_account_mutation_v1`, and exact constants `262144/5000/15000/25000/10000` for credential bytes, request-read, mutation, quiescence and post-commit reserve. Missing capability, version/constant mismatch, unsupported store, invalid response or failed authenticated discovery returns `unsupported_node_contract` with zero resolve/mutation request. The subsequent short DB transaction independently rechecks the persisted Node capabilities and lifecycle/monitoring/policy. Artifact commit/image headers are evidence only and cannot replace either gate.

### 7. Deadline and clock model

No Control/Node wall-clock equality is assumed. The pinned Node contract uses a local monotonic server budget. First-version planned budgets are:

```text
maximum Control dispatch-start allowance: 5s
maximum Node server-side mutation budget: 15s
quiescence safety margin: 5s
Control conservative quiescence bound: authorization DB time + 25s
verification deadline: 10 minutes after execution becomes verification-eligible
```

Control computes DB-time deadlines from the authorization transaction. Node receives/enforces the relative 15s mutation budget using its own local timer. Irreversible mutation MUST NOT begin after the Node budget. No background mutation may continue after handler quiescence. The 25s value is an accepted operational bound, not independent proof that an unobserved remote handler stopped.

These constants require independent readiness review and Node contract acceptance before apply; changing them later requires the same spec/review discipline.

### 8. Remote quiescence and Node Retire/Replace

`dispatch_deadline` is not remote-quiescence proof. Retire/Replace, after locking the Node, rejects `409 account_operation_in_progress` while any same-Node operation is `dispatched` and remote quiescence is not proven.

Fence release requires one of:

1. terminal Node handler response plus pinned synchronous contract proving completion/abort;
2. a successful authenticated recovery resolve that acquires the Node shared mutation gate, durably fences the exact dispatch token, and then reads back target/postcondition evidence;
3. proven termination/restart of the exact Node process instance.

Client timeout, `dispatch_deadline`, `remote_mutation_deadline`, `quiescence_deadline`, or 25s elapsed alone MUST NOT release the fence. Every dispatch reuses the operation row's durable `dispatch_token` as the Node wire `dispatch_token_v1`; response-loss recovery sends the same UUID as `fence_dispatch_token_v1`. The Node persists a no-GC durable token fence before read-back, so an earlier request that has not yet reached Node gate admission is rejected if it arrives later. Crash/restart restores the Control-side lifecycle fence. After lifecycle commit, no previously dispatched operation may begin/continue irreversible mutation on the old Node; operations never retarget replacement identity.

### 9. Node Account Management Contract v1 (external prerequisite)

The satisfied Node dependency is pinned to revision `72c435b1b1b85b341a734e3860081c7782d9cbd2` and image `sha256:c5d2cc476c5c99cff994528920151c3ecee0f37832ba82943b8b54ab7d9610c4`. The artifact exposes generic management semantics for:

- status persistence error propagation;
- serialized mutation;
- physical-target precondition;
- single-file <=256 KiB upload;
- exact Antigravity upload allowlist;
- explicit create vs replace;
- crash-safe same-filesystem replacement;
- secret-safe durable/read-back write postcondition;
- synchronous <=15s mutation budget, no background mutation and bounded quiescence;
- canonical lowercase UUID `dispatch_token_v1` on every mutation and same-token durable fenced resolve for response-loss recovery;
- stable sanitized error classes.

Stage 7A is also satisfied at migration `37` and compatibility class/floor `3 / 3`. Control implementation MUST keep these exact dependency pins in release evidence and fail closed if runtime contract discovery does not match them semantically.

### 10. Antigravity Secret/upload policy

First-version upload is one file, max 262144 bytes, top-level JSON object. Client input allowlist is exactly:

```text
type, access_token, refresh_token, expires_in, timestamp, expired, email, project_id
```

Unknown fields and runtime-control fields (`disabled`, `weight`, `headers`, `request_retry`, excluded-model/scheduler-like metadata) are rejected, not stripped. `type=antigravity`; email/provider identity must match create/replace intent. Browser cannot choose arbitrary destination path/name; Create uses server canonical filename, Replace inherits uniquely resolved target.

Control streams bounded content in memory only and never writes credential bytes to PostgreSQL/temp disk/audit/log/trace/metric/response.

### 11. Phase 7 fingerprint key

Upload canonical intent uses a separate stable key, not asset K1. Planned configuration contract:

```text
CONTROL_ACCOUNT_OPERATION_INTENT_KEY_FILE
32 raw bytes
regular file; no symlink
strict owner permissions equivalent to current K1 checks
version=1
HMAC domain: relay-station/account-operation-upload-intent/v1
```

Missing/unsafe/wrong length when upload fingerprinting is required -> fail closed `service_unavailable`; no auto-generation. Backup/restore is required; rotation is deferred. Existing `CONTROL_ASSET_INTENT_KEY_FILE` semantics remain unchanged.

### 12. Create/Replace postcondition v1

Control generates a non-secret random `write_token` per dispatch and computes/stores only its own keyed upload-intent fingerprint for command equality. Node Contract v1 MUST persist a secret-safe write/postcondition marker atomically with the credential commit or in another mechanism with equivalent crash semantics, and expose it in a sanitized management read-back. The marker proves that the intended physical target was committed by this dispatch without exposing credential bytes; Node does not receive Control's fingerprint key.

The exact Node wire/storage representation is external-contract-owned, but readiness MUST prove response-loss recovery: Control can compare its dispatched write token/target precondition with Node read-back and determine whether this command committed. Inventory identity alone is never credential-byte proof.

### 13. Operation execution and recovery

Remote HTTP is synchronous in the initiating request and bounded by the Node contract; no generic background dispatcher is introduced. Control sends the row's durable `dispatch_token` as `dispatch_token_v1`. A Phase 7-specific reconciler MAY inspect nonterminal/outcome-unknown rows, but MUST NOT blindly redispatch a `dispatched` operation. After response loss it uses the same token in a fenced resolve; only successful durable fencing plus shared-gate read-back proves quiescence and permits outcome recovery. A failed/timeout/unavailable resolve leaves the lifecycle fence active. The reconciler may otherwise advance verification or mark timeout/inconclusive without claiming quiescence.

Crash before dispatch commit: remains prepared, zero remote mutation, same command may continue. Crash/timeout after dispatched: outcome unknown until proof; lifecycle fence survives restart. Remove never deletes a new target that appeared after the original precondition.

### 14. Disable/Enable semantics

`remote_applied` requires Node mutation success + reliable persistence error propagation + management read-back of the desired `disabled` value. Already desired state -> `remote_noop`. Enabled does not mean provider healthy or Control schedulable.

### 15. Remove semantics

Remove is permanent physical auth-file deletion, not soft delete. UI requires explicit destructive confirmation. Disk-delete/runtime-cleanup partial failure -> `remote_partial`. Already-started provider requests are not globally cancelled by contract. There is no Control credential backup/vault.

### 16. Inventory scheduler integration

After execution reaches a verification-eligible state, Control only wake/requests the existing normal scheduler. If the current aligned UTC 300-second slot is not materialized and remains inside normal start grace, the scheduler MAY create/claim it; otherwise wait for the next normal slot. Existing slot in any state is never duplicated. No off-grid scheduled_at, special parser/finalize, policy bypass or direct current-Inventory patch.

If monitoring is disabled after dispatch, the already-dispatched mutation follows its quiescence fence, but verification obeys current monitoring truth and may timeout/inconclusive. Control never auto-enables monitoring.

### 17. Verification

Verification deadline is 10 minutes from execution becoming verification-eligible.

- Disable/Enable: fresh complete eligible Provider snapshot shows same account_key and desired disabled state.
- Remove: only fresh complete eligible provider-complete snapshot absence proves removal.
- Upload New/Replace: Node postcondition proof MUST match this dispatch AND fresh complete Inventory must show exactly one expected account_key.
- stale/incomplete/disk-fallback/duplicate evidence never fabricates success.

### 18. Audit / metrics / errors

Audit records actor/request/command/operation/node/provider/protected account identity, sanitized target/result and verification state; Remove has a distinct high-risk action. No credential/raw Node body/full path. Metrics use only low-cardinality operation/provider/result/error/execution-class/verification labels.

Fixed HTTP mapping is: `400 invalid_request`; `401 unauthenticated`; `403 forbidden` (including authorization/origin/CSRF); `404 node_not_found|account_target_not_found|operation_not_found`; `409 command_conflict|node_retired|node_monitoring_ineligible|unsupported_node_contract|unsupported_provider|account_target_ambiguous|account_target_changed|account_operation_in_progress`; `413 upload_too_large`; `422 upload_invalid|identity_mismatch`; `502 remote_partial|remote_failed`; and `503 node_management_unavailable|service_unavailable`. `remote_outcome_unknown` is represented by a `202` operation projection, not a terminal error receipt. Errors use `{"error":{"code":<fixed>,"message":<bounded sanitized>}}`; terminal operation errors additionally include `operation`. Raw Node strings never become API contract.

### 19. Compatibility / rollout

Change B cannot become implementation-ready until Change A and Node Contract v1 are ready/pinned. Any Control migration is additive/forward-only; supported rollback uses the existing signed compatibility gate. Exact class/floor is determined with implementation artifact metadata, not guessed here. Node capability/artifact mismatch fails closed before remote mutation.

## Acceptance Strategy

Required future acceptance includes global command conflict/replay, target/precondition races, monitoring/lifecycle lock-order races, same-target concurrent operations, Control timeout while Node still mutating, crash/restart before quiescence, no mutation after lifecycle commit, Secret non-persistence, strict upload allowlist, Node postcondition response-loss recovery, fixed-slot scheduler wake semantics, Inventory verification conservative rules and compatibility rollback.
