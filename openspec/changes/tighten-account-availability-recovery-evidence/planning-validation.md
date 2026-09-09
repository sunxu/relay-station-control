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
- Change approved for implementation, but implementation has NOT started。
