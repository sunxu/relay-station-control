# Lifecycle fixture adapter

此目录是 acceptance runner 的安全门，不是第二套生产通知实现。正式 fixture 必须放在 `store_test` test-only adapter 中复用现有 Availability/Duplicate fixture 语义；transition 只能由 production `Reconcile()` / `Evaluate()` 产生。

`run.sh` 默认拒绝真实 DingTalk 环境变量和网络。它不直接调用 `jobs.EnqueueTx`，也不复制 payload hash、idempotency 或 policy snapshot 算法。数据库必须是 disposable isolated PostgreSQL；只输出 safe IDs/status/counts。
