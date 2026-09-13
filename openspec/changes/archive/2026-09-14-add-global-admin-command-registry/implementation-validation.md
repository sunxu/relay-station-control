# Phase 7 Stage 7A Implementation Validation

## Status

- Change: `add-global-admin-command-registry`
- Ops requirements baseline: `594a349435dbb6c2d4265be79fb913015b1b05c5`
- Planning: COMPLETE
- Independent readiness review: P0 = 0 / P1 = 0 / P2 = 1; non-blocking validation evidence finding corrected
- Implementation: COMPLETE
- Runtime Acceptance: PASS
- Initial independent implementation review: P0 = 0 / P1 = 1 / P2 = 0; CHANGES REQUIRED
- Initial finding: P1-1 compatibility artifact provenance
- Corrective acceptance commit: `550bdc31dbda0cbaf32632ee8d8aa54161daa2aa`
- Final independent implementation re-review: P0 = 0 / P1 = 0 / P2 = 0; PASS
- Independent implementation review: PASS
- Implementation commit: `a9463bc776ffa5cc7c6341f15f89385afa555d34`
- Completed implementation tasks: 15 / 15
- Git/worktree implementation closeout: COMPLETE
- Migration: 37
- Compatibility class/floor: `3 / 3`
- Independent archive-readiness review: P0 = 0 / P1 = 0 / P2 = 0; PASS
- Archive readiness: PASS / APPROVED
- Archive: COMPLETE
- Stage 7A: CLOSED / IMPLEMENTED / ARCHIVED
- Stage 7N: NOT STARTED
- Stage 7B: NOT STARTED
- Phase 7: PLANNED / NOT STARTED

## Implemented durable truth

- `admin_command_registry` is the single immutable UUID reservation namespace for existing Gateway, Relay Node and Node Monitoring administrator commands and the future `account_admin` domain.
- Migration 37 atomically validates and backfills every historical `asset_admin_command_receipts` row, adds receipt-to-registry referential and metadata enforcement, revokes unrestricted runtime DML, and exposes only bounded lookup and controlled write functions.
- Gateway Register/Edit/Retire/Replace, Node Register/Edit/Retire/Replace, and Monitoring Enable/Disable acquire the existing UUID-derived global advisory lock and perform actor-first registry lookup before canonical intent, Secret/K1 or domain-state validation.
- Registry reservation, domain mutation, audit and completed receipt share one transaction. Domain or receipt failure rolls back the reservation; an old direct receipt writer fails and its preceding domain mutation rolls back.
- Existing canonical intent bytes, K1 behavior, persisted replay bodies and Monitoring Disable `committed_at` fence ordering are unchanged. Health and Connection Test remain observations without command IDs or reservations.
- Migration 37 advances the compatibility floor from 2 to 3. The minimum next class is 3 because a class-2 pre-registry writer must fail closed once the registry relationship is authoritative.

## Runtime Acceptance matrix

| Area | Result | Evidence |
|---|---|---|
| PostgreSQL version | PASS | Isolated acceptance used PostgreSQL 18.6. |
| Historical migration/backfill | PASS | A migration-36 fixture containing Gateway, Node and Monitoring receipts migrated to 37 with count parity `3 / 3`, exact actor/kind/hash/encoding/key-version metadata, and `reserved_at = committed_at`. |
| Invalid historical state | PASS | A malformed historical hash caused migration 37 to fail atomically; schema remained at 36 and no registry table survived. |
| Historical replay/restart | PASS | A valid pre-migration Gateway receipt replayed its persisted status/body after migration; repository restart replay also passed. |
| Global command races | PASS | Concurrent Gateway-vs-Node and asset-vs-Monitoring reuse of one UUID produced exactly one registry/receipt winner and one `command_conflict`. |
| Actor/domain/kind precedence | PASS | A different actor and same-actor cross-domain reuse were rejected before malformed target validation; existing same-command replay remained exact. |
| Atomic rollback | PASS | A stale-revision domain failure left zero registry reservation; old-writer receipt rejection rolled back its preceding Gateway insert. |
| Registry/receipt integrity | PASS | Receipt without reservation and mismatched metadata fail closed at the database boundary. |
| Minimum privilege / immutability | PASS | Runtime direct registry INSERT/UPDATE/DELETE/TRUNCATE and direct receipt INSERT were rejected; owner mutation guards rejected UPDATE/DELETE/TRUNCATE. |
| Existing writers | PASS | Gateway lifecycle, Node lifecycle, Node Monitoring Enable/Disable and HTTP integration regressions passed on migration 37. |
| Monitoring Disable fence | PASS | Strict receipt ordering, F0/F1 waiting, already-disabled fence and receipt immutability tests passed unchanged. |
| Compatibility barrier | PASS | Exact pre-Stage7A source revision `fa9825be3239bbd395bbaf0ecd7294e10cf7af64` rebuilt release compatibility artifact `sha256:36cbdf26d32e237097e52e89c6e4b2327e578d84ce98f889a849270c181bce91`; its signed class-2 manifest was rejected by the mandatory wrapper at migration 37 / floor 3 before Control start. |
| Compatible artifact | PASS | Exact Stage7A implementation source revision `a9463bc776ffa5cc7c6341f15f89385afa555d34` rebuilt release compatibility artifact `sha256:e037caa821947ad626c19dc23f4a007ae8b8109a9415dafc906f98df547265e7`; its signed class-3 manifest was accepted against migration 37 / floor 3. The gate binary was built from the same pinned Stage7A revision. |
| Generated artifacts | PASS | `make generate` ran twice; the second run produced no additional filename/status delta. |
| `make test` | PASS | All Go/tool packages, 30 frontend test files / 224 tests, and TypeScript typecheck passed. |
| `make build` | PASS | Generated inputs, production Vite bundle and Control binary completed; corrective-run local make-build digest `sha256:d416fbab275a972337c36044692919b9e0980650a8a3371d8289e44541d1dfc6`. This local artifact is distinct from the pinned release compatibility artifact above. |
| OpenSpec strict | PASS | Change-specific validation and `openspec validate --all --strict` passed, 30 passed / 0 failed. |
| Diff hygiene | PASS | `git diff --check` passed. |
| Git closeout | PASS | Implementation commit `a9463bc776ffa5cc7c6341f15f89385afa555d34` was created and the worktree was clean immediately afterward; this evidence-only reconciliation records that durable result. |

## Release and rollback ordering

Production rollout must stop old Control and automatic restart, install the class-3-aware gate/trust root/wrapper and signed class-3 artifact, apply forward migration 37, verify artifact and database floor through the mandatory wrapper, and only then start Control. The forward schema, registry, receipts and floor remain durable during rollback. A pre-registry class-2 artifact cannot be selected after floor advancement, and migration 37 has no destructive production down path.

## Independent implementation review correction

The first implementation review found one P1 provenance gap: compatibility acceptance archived `HEAD` for the old artifact and built the new artifact from the mutable implementation worktree. That historical run produced Stage7A digest `sha256:9677e70108d4a326fb00cdafb38ea9c6ac4e4efb73a29998c24dd02244d9494f`; it is retained here only as historical worktree evidence.

The corrective acceptance now runs `git archive` separately for immutable revisions `fa9825be3239bbd395bbaf0ecd7294e10cf7af64` and `a9463bc776ffa5cc7c6341f15f89385afa555d34`, fails if either revision cannot be resolved, and builds both Control artifacts plus the gate from those extracted committed sources. Future documentation commits and mutable worktree changes therefore cannot redefine either artifact provenance. P1-1 compatibility artifact provenance was corrected by commit `550bdc31dbda0cbaf32632ee8d8aa54161daa2aa`; final independent implementation re-review passed with P0/P1/P2 = 0/0/0.

## Boundaries

- Account operations and `account_admin_operations`: NOT IMPLEMENTED
- Node Account Management Contract v1 hardening: NOT IMPLEMENTED
- Stage 7N: NOT STARTED
- Stage 7B: NOT STARTED
- Archive: COMPLETE
- Stage 7A: CLOSED / IMPLEMENTED / ARCHIVED
- Phase 7 remains PLANNED / NOT STARTED until an authorized stage implementation closeout changes that project-level status.
