# Tasks

- [x] 1.1 将 change 收缩为删除 file_active recovery、禁止 runtime-only watermark 推进、严格 success recovery 三项行为。
- [x] 1.2 完成最终 Architecture Review，确认无 ACTIVE persistence/confirmation/concurrency 新契约。
- [x] 2.1 在下一个 additive migration 中按需更新 `control_reconcile_account_availability_v1` 及必要 read projection；不修改 00026、request writer 或 event schema。
- [x] 2.2 删除 evaluator 中 file_active no-traffic recovery；runtime-only 不推进 last_failure_at；success 严格使用 `>`。
- [x] 2.3 保持 current projection、stale/incomplete、DISABLED 与所有既有行为不变。
- [x] 3.1 增加真实 PostgreSQL 回归覆盖最小 R1–R12。
- [x] 3.2 运行既有 confirmation、recurrence、request_id/event_hash、dedupe、transition、concurrency/race regression，不改变其行为。
- [x] 3.3 验证 migration up/down/up、ACL、existing DB upgrade 与 clean install。
- [x] 4.1 更新 planning-validation 与 regression evidence；本 change 无额外 runbook 行为变更。
- [x] 4.2 运行 targeted tests、race、`make test build`、OpenSpec strict、`git diff --check`，确认无 unrelated worktree changes。
