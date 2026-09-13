## 1. Planning and schema foundation

- [x] 1.1 Re-read Ops baseline `594a349435dbb6c2d4265be79fb913015b1b05c5` and current canonical admin-command specs before implementation.
- [x] 1.2 Add one forward Goose migration for `admin_command_registry`, immutable guards, minimum privileges, controlled functions, historical backfill and receipt FK/integrity enforcement.
- [x] 1.3 Prove migration up behavior on a Phase 6 migration-36 fixture containing existing Gateway/Node/Monitoring receipts; no production destructive down path.

## 2. Shared command store

- [x] 2.1 Add sqlc/Store primitives for global advisory serialization, actor-first lookup and reservation; keep registry metadata bounded and Secret-free.
- [x] 2.2 Migrate existing Gateway and Relay Node lifecycle command paths to global registry without changing canonical intent bytes, K1 semantics or replay bodies.
- [x] 2.3 Migrate Node Monitoring Enable/Disable command paths while preserving Disable race-fence `committed_at` semantics.

## 3. Database and security acceptance

- [x] 3.1 PostgreSQL 18 two-session tests: same UUID/same actor/same intent, different actor, cross-domain kind conflict and concurrent reservation.
- [x] 3.2 Direct-DML tests: runtime roles cannot insert/update/delete/truncate registry or create receipt without reservation.
- [x] 3.3 Backfill parity/inconsistency tests and exact historical replay after migration/restart.

## 4. Compatibility / rollback

- [x] 4.1 Extend signed compatibility planning so Change A advances the floor; assign the exact numeric class/floor only with the implementation artifact/release manifest.
- [x] 4.2 Prove unsupported old binary cannot start through mandatory wrapper after floor advancement and cannot commit through DB enforcement if bypass attempted.
- [x] 4.3 Prove supported candidate restart/recovery and preserve registry/receipt evidence across rollback attempts.

## 5. Regression and evidence

- [x] 5.1 Run `make test build` after implementation plus focused Gateway/Node/Monitoring command replay tests.
- [x] 5.2 Run change-specific PostgreSQL/container acceptance, `openspec validate --all --strict`, `git diff --check` and generated-file checks.
- [ ] 5.3 Capture implementation-validation evidence, release ordering, final artifact digest/class/floor and clean-worktree status.

## Planning gate

Planning artifacts are complete candidates only. Independent readiness review is REQUIRED before implementation. This task list does not authorize apply.
