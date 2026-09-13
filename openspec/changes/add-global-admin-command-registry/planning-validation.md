# Phase 7 Change A Planning Validation — Global Admin Command Registry

## Status

- Ops requirements baseline: `594a349435dbb6c2d4265be79fb913015b1b05c5` — FROZEN / Architecture Review PASS.
- Control planning baseline: `905c621b7db5c167af0ec6d0b0104504196f7b76`.
- Planning: `COMPLETE` candidate.
- Independent readiness review: `REQUIRED`.
- Implementation readiness: `NOT YET APPROVED`.
- Implementation: `NOT STARTED`.

## Scope checks

- One global command namespace is isolated from Phase 7 remote account mutation.
- Historical asset/monitoring receipts are backfilled before enforcement.
- Existing K1/canonical intent and receipt success bodies are preserved.
- Existing Gateway/Node/Monitoring writers are explicitly migrated.
- Old-writer bypass is fail-closed through receipt FK/controlled writer plus compatibility barrier.
- Change A explicitly raises the compatibility floor; numeric release class/floor is deferred until signed implementation artifact metadata exists.
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

The planning artifacts follow the repository's `spec-driven` structure (`proposal.md`, `design.md`, `specs/**`, `tasks.md`) and the `openspec-propose` planning boundary. In this execution environment the OpenSpec CLI is not available, so `openspec validate --all --strict` has **not** been claimed as executed. It remains a mandatory command for the independent readiness review/local checkout before any apply authorization. Structural review and GitHub-path/reference checks were performed while creating this planning commit.

## Readiness findings

- P0 planning blockers: 0 candidate.
- P1 planning blockers: 0 candidate.
- Remaining gate: independent OpenSpec / implementation-readiness review plus real CLI strict validation.

Disposition: `PLANNING COMPLETE / INDEPENDENT READINESS REVIEW REQUIRED`.
