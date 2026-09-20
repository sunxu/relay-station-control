# Proposal: Stabilize and simplify Control CI validation

## Outcome

The first actionable History Process blocker was classified as a shipped
schema-contract defect and independently corrected by the archived
`repair-current-inventory-query-security-context` change.

Migration 52 restored the required secured-function `TimeZone` contract.
History disabled-compatible validation now passes; the eligible-source seed
fixture has also been aligned with the current eligibility predicate.

Phase 9 已关闭；本 change 处理后续 Control CI validation 的 ownership 与
可诊断性，不改变已发布 production artifact。目标是：

- 修复仍承担 correctness 的 stale、flaky、opaque gate；
- 将 correctness、capacity、upstream compatibility、release evidence 分层；
- docs-only 变更不再运行无关的 heavy runtime acceptance；
- 保持既有 validation coverage，`VALIDATION_COVERAGE_GAP = 0`；
- 不通过删除失败 gate、扩大 timeout、sleep 或 blind retry 获得绿色。

## Scope

Affected repository: `relay-station-control`。

本 change 覆盖 `.github/workflows/`、`deploy/acceptance/`、相关 web test、
OpenSpec 与稳定 validation 文档。每项迁移都必须有明确 replacement owner
和实际通过的 replacement proof。

## Non-goals

- 不改变业务语义、API contract、数据库 schema 或 shipped migration；
- 不修改 Phase 9 release artifact，不重新打开 Phase 9；
- 不修改 production compose、Gateway、Node 或其他仓库；
- 不建立新的 CI 平台；
- 不以极端缩短 CI 时间为目标；
- 不修改生产 runtime，除非证据证明真实 production defect；
- 不移动、删除或重建 `deploy-v0.9.1` tag。

## Compatibility and release impact

预期 `PRODUCTION_CODE_CHANGED = NO`，因此 `CONTROL_RELEASE_IMPACT = NONE`，
Control `deploy-v0.9.1` 保持不变。CI evidence 会绑定当前 source candidate，
而不是改变 application artifact identity。

## Security and evidence

Acceptance diagnostics SHALL 只输出 phase、checkpoint、failed test 和固定的
sanitized reason；不得输出 secret、token、credential 或 raw sensitive
response。capacity 与 compatibility evidence 使用独立 owner，并保留
明确的 candidate、artifact、execution provenance。
