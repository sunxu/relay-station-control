# Implementation Validation

## Historical finding

Post-closeout independent review:

```text
P0 = 0
P1 = 2
P2 = 1
Disposition = CHANGES REQUIRED
```

## Corrective disposition

- P1-1 HTTP-only Node lifecycle: FIXED
- P1-2 Probe lifecycle fence: FIXED
- P2-1 canonical Purpose: FIXED
- Additional UI internal-host validator finding: FIXED
- Corrective implementation: COMPLETE
- Independent corrective implementation review: PASS
- Final corrective review: P0 = 0 / P1 = 0 / P2 = 0
- Corrective implementation commit: `0ef6700c5d95af411138e1caf77ce80849a6e58d`
- Corrective closeout commit: `493d3bdfc33edce7fd55637e018d13fca190c54b`
- Git/worktree closeout: COMPLETE
- Independent archive-readiness review: PASS
- Archive-readiness review: P0 = 0 / P1 = 0 / P2 = 0
- Archive readiness: PASS / APPROVED
- Phase 6: CLOSED / IMPLEMENTED / ARCHIVED
- Corrective change: CLOSED / IMPLEMENTED / ARCHIVED
- Phase 7: PLANNED / NOT STARTED

## Runtime Acceptance

| Area | Result | Evidence |
|---|---|---|
| Node Register/Edit/Replace HTTPS admission | PASS | API/store tests reject new HTTPS commands with `invalid_endpoint` before mutation, revision, lineage, receipt, success audit, or outbound work. |
| Node HTTP and internal host admission | PASS | `http://node:8317` and `http://host.docker.internal:8317` accepted. |
| Historical HTTPS receipt replay | PASS | Actor-matched committed v1 receipt replays the exact persisted response through replay-only legacy canonicalization. |
| Migration 36 clean database | PASS | PostgreSQL 18.6 migrated to version 36; compatibility class/floor remain `2 / 2`. |
| Existing durable HTTPS row | PASS | Migration fails closed, reports the affected instance ID, leaves migration at 35, and does not rewrite the row. |
| DB controlled/direct HTTPS writes | PASS | Controlled create, direct insert, and runtime update reject HTTPS with zero partial asset, capability, or generation mutation. |
| Probe secret boundary | PASS | Authorizer projection excludes `reader_secret_ref`; Probe uses no SecretResolver. |
| Lifecycle-first Probe race | PASS | Real two-connection PostgreSQL 18 Retire and Replace tests prove authorization waits for the Node lock, then observes `asset_retired`; zero Driver HTTP follows failed authorization. |
| Probe-first race | PASS | Real two-connection PostgreSQL 18 test proves the lifecycle writer waits until the short authorization transaction commits; one copied fixed target may be probed and later authorization is rejected. |
| Probe transport/audit/metrics regression | PASS | Health and Connection Test targeted API tests preserve one bounded credential-free request, no retry, and separate audit/metric surfaces. |
| Internal HTTP UI validator | PASS | Shared Gateway/Node validator accepts Docker DNS, `host.docker.internal`, domain, IPv4, and supported base paths; HTTPS and malformed inputs are rejected. |
| Authenticated Asset Registry E2E | PASS | Gateway management and Node lifecycle/Health/Connection Test scenarios: 2 passed. |
| Canonical Purpose cleanup | PASS | Exact archive-generated Purpose placeholders remaining: 0; requirement and scenario contracts were unchanged. |
| Inventory regression | PASS | PostgreSQL 18 current-provider promotion test passed with an eligible Stage 2 Node fixture. |
| Monitoring Enable/Disable regression | PASS | PostgreSQL 18 migration, ordering, F0/F1, and already-disabled fence tests passed. |
| Node Retire/Replace regression | PASS | Node lifecycle and HTTP integration suites passed. |
| Gateway lifecycle regression | PASS | PostgreSQL 18 Gateway lifecycle migration suite and UI E2E passed. |
| Stage 2 artifact against schema 36 | PASS | Fixed source commit `5199b611a99ac36b46a5a0309db1c01d3fe50929`; artifact digest `sha256:b8cc8d7a6a95fff3c57146dff11eb924d7fd5fd4aa52764d8e917df42f21b507`; signed manifest and formal wrapper accepted class 2 at floor 2; Node read/reconcile/Retire/Replace passed and old five-parameter writer failed closed. |
| `make test` | PASS | Go, tools, 30 frontend files / 224 frontend tests, and TypeScript typecheck passed. |
| `make build` | PASS | OpenAPI/sqlc/client generation, production web build, and Control Go build completed. |
| OpenSpec strict | PASS | Corrective change and all changes/specs validate with zero failures. |
| `git diff --check` | PASS | Final unstaged corrective worktree passed whitespace validation. |

Independent archive-readiness review re-ran the complete build, targeted PostgreSQL 18 migration and lifecycle-lock suites, authenticated Gateway/Node UI E2E, and the fixed Stage 2 class-2 artifact rollback against schema 36. The results matched the durable evidence above.

## Boundaries

- New business table: 0
- Compatibility class/floor: `2 / 2`
- Migration: 36
- Corrective implementation commit: CREATED
- Git/worktree closeout: COMPLETE
- Archive: COMPLETE
