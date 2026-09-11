## Why

Phase 5 的临时验收已证明现有 `deploy/acceptance/`、认证流程、durable job 与生命周期 fixture 可以组合使用，但缺少统一、可复用且安全的 harness。本 change 将这些能力整理为 acceptance tooling，不改变任何 Control production behavior。

## What Changes

- 增加 repo-external runtime orchestration、isolated Buildx image builder 与最小 authenticated storage-state 流程。
- 增加 lifecycle-fixture 的 test-only 入口约束，要求通过 production `Reconcile()` / `Evaluate()` 产生 Availability/Duplicate transition 与 durable jobs。
- 明确 durable-job recovery orchestration（pending、retry_wait、running lease、old-binary）为后续独立 change，不在本 change 内实现。
- 增加 Secret、runtime path、cleanup 和 safe evidence 规则。

## Non-goals

- 不修改 `internal/*` production behavior、Migration、OpenAPI、generated clients、Gateway、CLIProxyAPI 或已归档 Phase 5 change。
- 不新增 notification framework、workflow、queue 或生产数据模型。
- 不实现 self-contained pending/retry_wait process-restart、running-lease SIGKILL/expiry/reconciler takeover 或 old-binary runtime recovery orchestration；这些均为 `DEFERRED — separate Recovery Harness Hardening follow-up`。

## Impact

仅影响 Control 仓库的 acceptance tooling、test-only fixture adapter、README 与 OpenSpec。runtime dir 必须位于 repo 外；storage-state、TOTP URI、密码、webhook、签名 Secret、私钥和 raw sensitive logs 不得进入仓库或 evidence。

## Current status

已完成范围包括 candidate image revision fail-closed、isolated Buildx、self-contained PostgreSQL provision/migration/cleanup、Control startup、authenticated session/restart restore、repo-external `storageState`（0600）、Availability/Duplicate lifecycle fixture，以及 Secret/runtime artifact cleanup。

现有 durable-job focused tests 与 Phase 5 deployment-readiness 继续证明 production recovery behavior；本 change 提供可复用的 acceptance environment、auth 与 lifecycle 基础设施，不重新实现 durable-job recovery framework。
