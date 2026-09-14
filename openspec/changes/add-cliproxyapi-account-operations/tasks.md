## 1. Dependency and Node contract gate

- [x] 1.1 Record satisfied `add-global-admin-command-registry` dependency: migration 37 and compatibility class/floor 3/3.
- [x] 1.2 Record independently accepted Node Contract v1 revision `72c435b1b1b85b341a734e3860081c7782d9cbd2` and image `sha256:c5d2cc476c5c99cff994528920151c3ecee0f37832ba82943b8b54ab7d9610c4`.
- [x] 1.3 Reconcile accepted Node v1 bounded mutation/quiescence, durable dispatch fencing, strict upload, proof and sanitized error dependencies into Change B planning.

## 2. Control durable operation foundation

- [ ] 2.1 Add additive migration for `account_admin_operations`, separate immutable `account_admin_command_receipts`, constraints/ACL/controlled writers and any audit allowlist/indexes; no raw credential/path.
- [ ] 2.2 Add sqlc/Store projection and state-transition functions with row locking, monotonic state shape, dispatch/quiescence metadata and restart recovery reads.
- [ ] 2.3 Integrate global admin command registry actor-first reservation; preserve separate Phase 7 upload fingerprint key and existing asset K1.

## 3. Node adapter and target resolution

- [ ] 3.1 Add a bounded Control Node account-management client/adapter using HTTP-only base endpoint + Management Key server-side only; require fresh exact authenticated contract discovery before mutation.
- [ ] 3.2 Implement fresh exactly-one `(Node,account_key)` resolution, opaque target precondition and fixed sanitized error mapping.
- [ ] 3.3 Enforce no browser/raw path/auth_index identity exposure and no `/auth-files/download` proxy.

## 4. Dispatch / quiescence / monitoring fences

- [ ] 4.1 Implement short Node-first dispatch authorization: lifecycle -> monitoring -> capability/policy -> same-target live fence -> operation row; atomically `prepared -> dispatched` with DB-time deadlines.
- [ ] 4.2 Add Node Retire/Replace check for dispatched-unquiesced account operations; no lock across HTTP.
- [ ] 4.3 Implement restart/reconciler logic that sends the durable operation `dispatch_token` as Node `fence_dispatch_token_v1`, proves quiescence only after successful durable fencing plus shared-gate read-back, and never blindly redispatches dispatched destructive operations; deadline expiry alone keeps the lifecycle fence active.
- [ ] 4.4 PostgreSQL 18 race acceptance: monitoring Disable-first/dispatch-first, Retire/Replace vs live handler, Control crash/restart before quiescence, same-target concurrent commands.

## 5. Operations and Secret ingress

- [ ] 5.1 Implement Disable/Enable desired-state/no-op/read-back semantics and persistence-failure mapping.
- [ ] 5.2 Implement Remove confirmation, partial-failure and response-loss protections.
- [ ] 5.3 Implement streaming single-file Upload New / Replace Existing with 256 KiB limit, strict Antigravity allowlist, identity checks and explicit create/replace semantics.
- [ ] 5.4 Implement `CONTROL_ACCOUNT_OPERATION_INTENT_KEY_FILE` validation/HMAC v1 without changing asset K1.
- [ ] 5.5 Implement Node write-token/postcondition proof integration and response-loss read-back.

## 6. API / UI / audit / metrics

- [ ] 6.1 Add OpenAPI routes/schemas, regenerate Go/TypeScript clients via `make test build`, and keep all mutation responses no-store/sanitized.
- [ ] 6.2 Add super-admin UI for single-account Disable/Enable/Remove/Upload New/Replace Existing, destructive confirmation and operation state projection; no raw filename/Management Key/credential.
- [ ] 6.3 Add bounded audit actions/error taxonomy and low-cardinality metrics; security-negative tests prove Secret absence.

## 7. Inventory verification

- [ ] 7.1 Integrate scheduler wake/request only; prove no off-grid/duplicate run and preserve grace/lease/fencing/policy behavior.
- [ ] 7.2 Implement verification updater/reconciler using accepted normal Inventory and ten-minute deadline; no direct current Inventory mutation.
- [ ] 7.3 Acceptance for Disable/Enable/Remove/Create/Replace conservative verification, monitoring-disabled-after-dispatch, stale/incomplete/disk-fallback/duplicate evidence.

## 8. Compatibility / rollout / evidence

- [ ] 8.1 Plan/implement additive migration compatibility and signed artifact gate after Change A; assign exact class/floor only with release artifacts.
- [ ] 8.2 Run Node pinned-artifact contract tests, PostgreSQL 18 acceptance, API/UI E2E, response-loss/crash tests, `make test build`, `openspec validate --all --strict`, `git diff --check` and rollback wrapper acceptance.
- [ ] 8.3 Capture implementation-validation, final Node fork/image digest, Control artifact metadata, Secret scans and clean-worktree evidence.

## Planning gate

Planning = COMPLETE. Dependencies = SATISFIED candidate. Implementation readiness = READY FOR INDEPENDENT RE-REVIEW. Implementation = NOT STARTED. This task list does not authorize implementation.
