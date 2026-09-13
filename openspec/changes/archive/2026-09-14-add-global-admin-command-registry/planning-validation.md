# Phase 7 Change A Planning Validation — Global Admin Command Registry

## Status

- Ops requirements baseline: `594a349435dbb6c2d4265be79fb913015b1b05c5` — FROZEN / Architecture Review PASS.
- Control planning baseline: `905c621b7db5c167af0ec6d0b0104504196f7b76`.
- Planning: `COMPLETE`.
- Independent readiness review: `P0 = 0 / P1 = 0 / P2 = 1`.
- Implementation readiness: `READY`.
- Implementation: `COMPLETE — READY FOR INDEPENDENT IMPLEMENTATION REVIEW`.

## Scope checks

- One global command namespace is isolated from Phase 7 remote account mutation.
- Historical asset/monitoring receipts are backfilled before enforcement.
- Existing K1/canonical intent and receipt success bodies are preserved.
- Existing Gateway/Node/Monitoring writers are explicitly migrated.
- Old-writer bypass is fail-closed through receipt FK/controlled writer plus compatibility barrier.
- Change A raises compatibility class/floor from `2 / 2` to the minimum monotonic next value `3 / 3`; the signed implementation artifact and migration-37 floor evidence reject pre-registry class 2.
- No Gateway, Node product, account operation, credential upload or UI behavior is included.

## Planned acceptance

1. PostgreSQL 18 migration/backfill parity and immutable/direct-DML tests.
2. Same UUID global races across asset/monitoring/future account domains.
3. Actor-first ordering before Secret/target/current-state validation.
4. Exact historical asset receipt replay.
5. Monitoring Disable race-fence regression.
6. Mandatory compatibility wrapper rejects pre-registry rollback artifact.
7. Full Control regression and strict OpenSpec validation.

## Validation execution note

The planning artifacts follow the repository's `spec-driven` structure (`proposal.md`, `design.md`, `specs/**`, `tasks.md`) and the `openspec-propose` planning boundary. Independent readiness review 已在本地 checkout 执行正式验证：`openspec validate --all --strict` 为 `30 passed / 0 failed`，`git diff --check` 为 `PASS`。此前“OpenSpec CLI unavailable / validation not executed”仅是历史 planning 生成环境状态，不再表示当前 evidence。

## Readiness findings

- P0 planning blockers: 0.
- P1 planning blockers: 0.
- P2 readiness finding: stale validation evidence，已在本文件按真实结果修正。

Disposition: `PLANNING COMPLETE / READY`; implementation acceptance is complete and independent implementation review is required.
