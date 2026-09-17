## 1. Contract and schema

- [x] 1.1 固化 admission coverage matrix 与 frozen error mapping。
- [x] 1.2 新增一个 forward-only migration，更新现有 admission function implementation；不改历史 migration。

## 2. Store and service

- [x] 2.1 在 Node-first transaction 内重查 lifecycle、monitoring、capability、Provider policy 与 same-account blocker。
- [x] 2.2 让 dispatch/no-op 共用 durable eligibility，并让 concurrent prepared no-op 收敛到已提交 terminal state。
- [x] 2.3 保持 prepared resume、dispatched/outcome_unknown replay、override 与 no-HTTP-over-lock 语义。

## 3. API and evidence

- [x] 3.1 将 Retire/Replace account blocker 映射为 `409 account_operation_in_progress`，增加精确 HTTP proof。
- [x] 3.2 删除未测量的 `DUPLICATE_NATIVE_MUTATIONS` 输出，修正 acceptance policy 章节编号。
- [x] 3.3 运行 migration、store/service/API/race、vet、OpenSpec strict 与 consolidated checks，记录 clean worktree。
