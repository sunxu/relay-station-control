# Planning and validation

## 当前阶段
Architecture Review: PASS。P0/P1/P2 = 0。Implementation readiness: READY。Change approved for implementation, but implementation has NOT started.

## Acceptance matrix

| ID | 场景 | 预期 |
|---|---|---|
| R1 | TOKEN_INVALID + repeated file_active | ACTIVE，不恢复 |
| R2 | ACCOUNT_BLOCKED + repeated file_active | ACTIVE，不恢复 |
| R3 | FORBIDDEN + repeated file_active | 不 RESOLVED |
| R4 | success.occurred_at > occurrence.last_failure_at | RESOLVED |
| R5 | success.occurred_at == occurrence.last_failure_at | ACTIVE |
| R6 | runtime-only evidence | last_failure_at unchanged |
| R7 | success 后 runtime-only evidence | 不阻止 success 恢复 |
| R8 | TOKEN_INVALID + fresh file_active | current=TOKEN_INVALID |
| R9 | ACCOUNT_BLOCKED + fresh file_active | current=ACCOUNT_BLOCKED |
| R10 | FORBIDDEN + runtime active | current=UNKNOWN/pending_confirmation |
| R11 | stale/incomplete | current=UNKNOWN，occurrence unchanged |
| R12 | DISABLED | current=DISABLED，occurrence unchanged |

原 CREATE confirmation、ACTIVE request-failure persistence、request_id/event_hash、FORBIDDEN confirmation、RESOLVED recurrence、occurrence identity、concurrency、SERIALIZABLE、writer、ingestion ordering 仅列入 regression suite，不形成新的 acceptance contract。

## Review history

- 前一轮 Architecture Final Review：REQUEST CHANGES（要求并发协议与 ACTIVE persistence redesign）。
- 本轮 scope reduction 后 Architecture Final Review：PASS。P0/P1/P2 = 0。
- Architecture/Implementation Final Review：PASS；P0/P1/P2 = 0；无 blocker。

## Implementation evidence

- Migration：`migrations/00027_tighten_account_availability_recovery_evidence.sql`；`00026` 未修改。
- PostgreSQL availability suite：`go test ./internal/store -run '^TestAccountAvailability.*Postgres$' -count=1`：PASS。
- Relevant race：`go test -race ./internal/store -run '^TestAccountAvailability(RecoveryEvidence|FreshnessDisabledAndConcurrency)Postgres$' -count=1`：PASS。
- Full `make test build`：PASS。
- Scope：request writer、event schema、collector、usage queue、CLIProxyAPI、Gateway、Binding、Duplicate Ownership 未修改。

## P1 regression evidence

- runtime error after success：success 严格晚于 failure，随后 `file_error` 且 source_at 更晚，occurrence 仍 RESOLVED。
- FORBIDDEN runtime-only：ACTIVE occurrence 的 `last_failure_at` 在 file_error/file_unavailable 后保持不变。
- FORBIDDEN active projection：runtime 切回 file_active 且 Inventory fresh/complete/healthy 时 current 为 `UNKNOWN/pending_confirmation`，ACTIVE occurrence 保留。

## Runtime acceptance — 2026-09-09

- IMPLEMENTED / COMMITTED / RUNTIME ACCEPTED。
- Control revision：`402e0d355fb147c3b60e5681e48956e4c22dba62`。
- Control image digest：`sha256:2c2897d677e53838605fe0121617c2da7aa9716b9732bf1f17125579b884eba2`。
- Local PostgreSQL migration version：`27`。
- P0/P1/P2 = 0。
- A–G runtime acceptance 全部 PASS：file_active 不恢复 confirmed fault；runtime-only watermark 保持；严格 success watermark recovery；equal timestamp 不恢复；current projection 与 stale/disabled 语义正确。
- `standardize-internal-http-transport runtime acceptance complete` 为其它 change 的历史文案；本 change 的 runtime acceptance complete。
