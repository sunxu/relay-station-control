## Why

Phase 7 的账号操作在接受后的最终 admission 边界仍有几项契约缺口：Node Resolver 的早期观察可能在真正 dispatch/no-op 前变旧，两个并发 prepared no-op 可能泄漏状态竞争错误，Node 生命周期 blocker 的 API 错误码仍使用通用 `conflict`。本 corrective 只收紧 Control 自己拥有的 durable admission 与可观察错误契约。

## What Changes

- 在同一个短 PostgreSQL transaction 中，以 Node row lock 为首，重新验证 lifecycle、当前 monitoring、`management_account_inventory_read`、请求 Provider 的 active policy 与 same-account serialization。
- Dispatch 与 no-op 共用同一套 durable eligibility；只有 prepared operation 才继续，已推进状态返回当前 durable projection。
- 并发 prepared no-op 竞争收敛到单一 terminal receipt，另一调用读取已提交 terminal 状态，不产生 native mutation。
- Node Retire/Replace 遇到 unresolved account operation 时返回 `409 account_operation_in_progress`。
- 删除 acceptance runner 中没有计数依据的 `DUPLICATE_NATIVE_MUTATIONS` 输出，并修正测试 policy 章节编号。

## Scope and Non-Goals

只修改 Control 的 account-admin admission、Node lifecycle error mapping、必要的 forward migration、测试、此独立 OpenSpec change 与 acceptance evidence。保留 `20e48709` 作为历史 closeout evidence，但修复后 candidate 使用新的 HEAD。

不修改 Gateway、CLIProxyAPI Node、Ops、UI、Browser matrix、历史 migrations、Phase 8 或新的 workflow/queue/scheduler；不持有数据库锁执行 native HTTP，不自动重试 unknown outcome，不把 Inventory 当执行真相。

## Truth Sources and Impact

OpenAPI/API handler 是外部错误契约来源，PostgreSQL forward migration 是 admission durable truth，store/service 负责 transaction 外的 orchestration，native adapter 仍只负责观察与 mutation。不会新增 public API 或 durable business identity；若 SQL function implementation 需要更新，使用下一个未占用的 forward-only migration。
