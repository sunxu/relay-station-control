# Design

## 最小目标
本 change 只修复三项 recovery evidence 行为：

1. 任意数量 `file_active` 不得恢复 confirmed ACTIVE fault。
2. runtime-only `file_error`、`file_unavailable`、`auth_reason=other` 不得单独推进 ACTIVE occurrence 的 `last_failure_at`。
3. 真实成功请求仅在 `success.occurred_at > occurrence.last_failure_at` 时执行 `ACTIVE → RESOLVED`；相等时间不恢复。

## Current projection
保持既有投影：

- ACTIVE TOKEN_INVALID + fresh complete qualified `file_active` → TOKEN_INVALID。
- ACTIVE ACCOUNT_BLOCKED + fresh complete qualified `file_active` → ACCOUNT_BLOCKED。
- ACTIVE FORBIDDEN + runtime active → UNKNOWN/pending_confirmation。
- stale/incomplete → UNKNOWN，occurrence lifecycle 不变。
- DISABLED → DISABLED，existing ACTIVE occurrence 不 resolve。
- 无 ACTIVE fault 时，fresh complete healthy `file_active` 仍为 AVAILABLE。

## Existing behavior preserved
本 change 不重新定义以下行为，全部沿用既有实现并仅运行 regression：CREATE confirmation、ACTIVE request-failure persistence、request_id/event_hash semantics、FORBIDDEN confirmation、RESOLVED recurrence、occurrence identity、并发、SERIALIZABLE isolation、request writer、ingestion ordering。

## Persistence and migration
仅在实施阶段按需通过下一个 additive migration 更新 `control_reconcile_account_availability_v1` 及必要 read projection；不得修改 `00026_antigravity_account_availability.sql`、request writer 或 event schema。保留 `consecutive_healthy_sources` legacy column，但不再赋予 recovery semantics。沿用现有 owner、`SECURITY DEFINER`、fixed `search_path`、ACL 与事务/重试机制；验证 existing DB upgrade、clean install、up/down/up 与 ACL。

## Safety and rollback
不读取或保存 Token，不请求 Google，不修改 CLIProxyAPI、Gateway 或其它数据面。应用与数据库回滚分离；本 change 是 forward-only correctness fix，生产默认不恢复旧 unsafe `file_active` no-traffic recovery。数据库语义回滚仅可由显式 operator-controlled migration 执行。
