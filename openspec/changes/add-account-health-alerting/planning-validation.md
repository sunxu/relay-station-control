# Planning Validation — Phase 5 Architecture Review

## Reviewed baseline

Phase: 5 — Account Health & Alerting

- Reviewed Control commit: `1b0eb7b9d4232861ca70a01c5883572b22e6e778`
- Reviewed Ops commit: `2e008cecc1f2ad4878a4ae55583ca51c43661278`
- 基线确认：两个仓库在本次 evidence/status update 前的 `git status --short` 均为空；以上为 `git rev-parse HEAD` 实际返回的已提交 SHA。
- 批准来源：用户在本次 Architecture Review evidence/status update 指令中明确确认正式评审完成及下列 PASS 结论。

以上 SHA 标识已评审的 requirement documents，不是本 evidence 文件或后续 status update 的提交 SHA。需求真相源为本 change 的 [design](./design.md)、[account-health-alerting spec](./specs/account-health-alerting/spec.md) 与 [durable-job delta](./specs/durable-job/spec.md)；Ops 规划见 [Phase 5 baseline](../../../../ops/docs/phase-5-7/PHASE5_ACCOUNT_HEALTH_ALERTING_CN.md)。精确评审内容以对应 reviewed commit 为准。

## Approval and status

```text
Detailed Requirements: FROZEN
Architecture Review: PASS
Implementation readiness: READY
Implementation: NOT STARTED
Runtime Acceptance: NOT STARTED

P0: 0
P1: 0
P2: 0

Phase 4 compatibility: PASS
Phase 5 internal consistency: PASS
Phase 6 boundary: PASS
Phase 7 boundary: PASS
Minimalism: PASS

Phase 6: PLANNED / NOT STARTED
Phase 7: PLANNED / NOT STARTED
```

## Core review checks

1. **Durable-job unknown-effect replay policy — PASS.** 仅扩展现有 execution policy 的 boolean `allow_unknown_effect_replay`，默认 false，仅 DingTalk 启用；enqueue 从 kind/catalog snapshot 到具体 job，Worker/Reconciler 依据 persisted per-job policy，registry/catalog/job mismatch fail closed，不改变旧 job 授权。true 必须要求 replay_safe=true；普通 job 保持 Verify-first，不按 job-kind 名硬编码。DingTalk direct HTTPS、no proxy、no redirect，HTTP timeout 5s、job timeout 10s、总 Execute attempts <= 5，at-least-once 允许重复通知，Secret runtime-only。
2. **Transaction-bound occurrence → jobs.EnqueueTx — PASS.** occurrence lifecycle transition 与 jobs.EnqueueTx 同一 PostgreSQL transaction，沿用已有 job/event/wake outbox 原子入队，不复制 enqueue logic，不新增 Notification Outbox。
3. **Duplicate occurrence vs membership — PASS.** Problems 不新增领域状态；Availability issue 仅随对应 occurrence 合法 RESOLVED 消失；duplicate 按 current confirmed affected-node membership 展开，absence_confirmed 移出 Node 后立即清除该 Node issue，即使 occurrence 仍 ACTIVE。stale/unverifiable/degraded/incomplete 不移除成员。不导致 lifecycle transition 的 membership 变化不通知；合法 ACTIVE→RESOLVED 在通知启用时产生一次 RESOLVED intent，A/B/C 验收保留。
4. **Token projection / DB-time boundaries — PASS.** 保持 VALID/INVALID/UNKNOWN；ACTIVE TOKEN_INVALID 最高优先级，其后 Inventory 不合格、refresh 空或 future 均 UNKNOWN；refresh 等于 DB 当前时间且其他条件满足时 VALID。TTL 固定 3599 秒，到边界 UNKNOWN，不增加 clock skew tolerance；expected_valid_until 仅为 Expected，不是真实 expiration。
5. **Additive/minimal Phase 5 boundary — PASS.** 后续实施沿用现有事实与 durable jobs；不新增 notification/outbox/workflow/policy table、policy DSL、delivery ledger、job state、专用 retry engine 或 RBAC。CLIProxyAPI/Gateway 无改动，Phase 6/7 范围不扩张。

## Evidence scope

本次只记录已批准的 Architecture Review 及状态，不执行 Phase 5 production implementation；不注册真实 DingTalk kind、不实现 replay policy、不修改 migration/schema/generated clients/runtime tests/acceptance implementation 或 archived changes。所有实施任务保持未完成。

文档验证与 runtime acceptance 分开：本次仅运行 OpenSpec strict、Markdown/reference、YAML 与 diff 检查，不以这些检查声明运行验收通过。

## Document validation results

- `openspec validate add-account-health-alerting --strict`：PASS。
- `openspec validate --all --strict`：20 passed，0 failed。
- YAML parse、拆分状态与 evidence 路径检查：PASS。
- Markdown 围栏及新增引用检查：PASS（9 个新增引用）。
- 两仓 `git diff --check`：PASS；reviewed SHA 与更新前 HEAD 一致。
- task checkbox 全部保持原值；3.1a → 3.1b → 3.1c → 3.1d 顺序正确。
- production code changed = false；migration changed = false；test implementation changed = false；archive changed = false。
