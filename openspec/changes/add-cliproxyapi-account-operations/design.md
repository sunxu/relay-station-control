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

Planned protected routes:

```text
POST /api/account-operations/disable          JSON {command_id,node_instance_id,account_key}
POST /api/account-operations/enable           JSON {command_id,node_instance_id,account_key}
POST /api/account-operations/remove           JSON {command_id,node_instance_id,account_key,confirmation}
POST /api/account-operations/upload-new       multipart: command_id,node_instance_id,file
POST /api/account-operations/replace-existing multipart: command_id,node_instance_id,account_key,file
GET  /api/account-operations/{command_id}
```

POST routes require active authenticated `super_admin`, same-origin and CSRF. `GET` is protected/read-only and no-store. Browser never receives Node Management Key or raw credential. Accepted remote execution returns a sanitized operation projection; verification is observed through the projection endpoint. Exact HTTP status/body details are spec-owned and generated from OpenAPI during implementation.

### 2. Global command acceptance

Change B depends on `add-global-admin-command-registry`. After auth/session/super_admin/CSRF, Control acquires the shared UUID-derived advisory serialization and performs actor-first global lookup before target resolution, upload Secret parsing/fingerprinting or Node calls.

New valid account command reservation and `account_admin_operations` creation occur atomically in one short DB transaction. Existing nonterminal same-actor/domain/intent POST retries return the current operation projection and MUST NOT redispatch solely because the POST repeated. Cross-actor or cross-domain/kind reuse is `command_conflict`.

Terminal immutable exact replay evidence remains separate from mutable operation state. OpenSpec implementation MUST define terminal receipt materialization so it never claims a remote completion before execution evidence is terminal enough; verification updates do not rewrite an immutable original POST receipt.

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
-> validate management_account_inventory_read capability + active Provider policy
-> reject any same-target live operation
-> lock current operation
-> validate prepared
-> persist dispatch/quiescence metadata
-> execution_state=dispatched
COMMIT
```

Failure before commit sends zero remote request and creates no live dispatch fence. `node_monitoring_ineligible` is the fixed product error for an active Node lacking current eligible monitoring/capability/policy. Control never auto-enables monitoring.

### 7. Deadline and clock model

No Control/Node wall-clock equality is assumed. The pinned Node contract uses a local monotonic server budget. First-version planned budgets are:

```text
maximum Control dispatch-start allowance: 5s
maximum Node server-side mutation budget: 15s
quiescence safety margin: 5s
Control conservative quiescence bound: authorization DB time + 25s
verification deadline: 10 minutes after execution becomes verification-eligible
```

Control computes DB-time deadlines from the authorization transaction. Node receives/enforces the relative 15s mutation budget using its own local timer. Irreversible mutation MUST NOT begin after the Node budget. No background mutation may continue after handler quiescence.

These constants require independent readiness review and Node contract acceptance before apply; changing them later requires the same spec/review discipline.

### 8. Remote quiescence and Node Retire/Replace

`dispatch_deadline` is not remote-quiescence proof. Retire/Replace, after locking the Node, rejects `409 account_operation_in_progress` while any same-Node operation is `dispatched` and remote quiescence is not proven.

Fence release requires one of:

1. terminal Node handler response plus pinned synchronous contract proving completion/abort;
2. recovery/read-back proving mutation can no longer begin/continue;
3. selected Node artifact has passed bounded-quiescence acceptance and DB time is later than the conservative `quiescence_deadline`.

If the Node artifact cannot prove bounded synchronous quiescence, time expiry alone MUST NOT release the fence. Crash/restart restores the durable fence. After lifecycle commit, no previously dispatched operation may begin/continue irreversible mutation on the old Node; operations never retarget replacement identity.

### 9. Node Account Management Contract v1 (external prerequisite)

The pinned Node artifact MUST expose generic management semantics for:

- status persistence error propagation;
- serialized mutation;
- physical-target precondition;
- single-file <=256 KiB upload;
- exact Antigravity upload allowlist;
- explicit create vs replace;
- crash-safe same-filesystem replacement;
- secret-safe durable/read-back write postcondition;
- synchronous <=15s mutation budget, no background mutation and bounded quiescence;
- stable sanitized error classes.

Control planning does not invent an OpenSpec tree in the Node repo. Before implementation, re-check upstream, pin exact upstream baseline, selectively port relevant upstream changes, add required hardening, build/pin fork commit and image digest. Do not wholesale rebase solely for Phase 7.

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

Remote HTTP is synchronous in the initiating request and bounded by the Node contract; no generic background dispatcher is introduced. A Phase 7-specific reconciler MAY inspect nonterminal/outcome-unknown rows, but MUST NOT blindly redispatch a `dispatched` operation. It can only prove quiescence/outcome via read-back, advance verification, or mark timeout/inconclusive.

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

Fixed errors include `invalid_request`, `node_not_found`, `node_retired`, `node_management_unavailable`, `node_monitoring_ineligible`, `unsupported_provider`, `account_target_not_found`, `account_target_ambiguous`, `account_target_changed`, `account_operation_in_progress`, `command_conflict`, upload errors, `remote_partial`, `remote_outcome_unknown`, verification errors and `service_unavailable`; raw Node strings never become API contract.

### 19. Compatibility / rollout

Change B cannot become implementation-ready until Change A and Node Contract v1 are ready/pinned. Any Control migration is additive/forward-only; supported rollback uses the existing signed compatibility gate. Exact class/floor is determined with implementation artifact metadata, not guessed here. Node capability/artifact mismatch fails closed before remote mutation.

## Acceptance Strategy

Required future acceptance includes global command conflict/replay, target/precondition races, monitoring/lifecycle lock-order races, same-target concurrent operations, Control timeout while Node still mutating, crash/restart before quiescence, no mutation after lifecycle commit, Secret non-persistence, strict upload allowlist, Node postcondition response-loss recovery, fixed-slot scheduler wake semantics, Inventory verification conservative rules and compatibility rollback.
