## Why

Phase 6 Stage 1 shared foundation、Stage 2 Node lifecycle 和 static-prefix planning 已经通过独立评审。管理员仍缺少产品内的 Node 进程健康观察与立即启停 Inventory monitoring。本 Stage 3 在已有 Asset Registry 中补齐这些操作，保持 Control 与 CLIProxyAPI 原生数据面的职责边界。

## What Changes

- 复用 CLIProxyAPI `Driver.Probe`，提供两个独立 product surface：只读 GET Node Health（观察）与显式 POST Connection Test（admin action），固定 `GET /healthz`，共享同一 Probe/response model，无持续监控或 health-history subsystem。
- 增加 immediate-only Monitoring Enable/Disable；复用 activation、Stage 2 cancellation metadata、Node-first locks；`node_generation` 仅保留 Stage 2 原有 Node list/projection cursor 职责。复用 shared Disable receipt 形成有界 per-Node race fence，并在Node serialization下保证Disable receipt `committed_at`严格单调，使 Disable 只失效其 commit 前已形成但尚未提交的 scheduled intent，不建立 durable disabled latch。
- 复用全局 command receipt / actor-first replay / canonical v1；Monitoring 不要求 expected_revision、不推进 Node revision。
- 为 administrator enable/disable additive 扩展既有 reason allowlist，以及对应 API/UI、audit/metrics、测试与运行手册规划。

## Capabilities

### New Capabilities

- `relay-node-management-operations`：显式健康观察、立即 monitoring commands、replay、安全和验收。

### Modified Capabilities

- `asset-registry`：完整合成 baseline + Stage 1/2 的 monitoring、受保护 API 和 Asset Registry UI 三个 Requirement，再增加 Stage 3 operations；不覆盖既有 lifecycle controls。

- `relay-node-asset-lifecycle`：仅对已批准Stage2新增的cancellation Requirement完整合成，扩充administrator_disable reason，保持原场景与不可变性。

## Dependencies

- `add-gateway-asset-lifecycle-management`：shared receipt、canonical encoding、审计/metrics 和 external compatibility gate。
- `add-relay-node-asset-lifecycle-management`：Node lifecycle、activation cancellation、node_generation、Node-first lock graph；planning commit `4cf8eae`。
- `fix-control-web-static-resource-prefix`：mutation UI rollout 前完成，planning commit `f8e600e`。
- Ops Phase 6 frozen requirements / Architecture Review D4–D8；具体来源见 planning-validation.md。

这些依赖已成为 archived canonical baseline：Stage 1 与 Stage 2 均已实施并归档，Stage 2 archive commit 为 `5199b611a99ac36b46a5a0309db1c01d3fe50929`。Stage 3 只在该最终基线上增加本 change 明确列出的 operations。

## Impact

实施已触及 Control OpenAPI、sqlc queries/generated clients、受控 Store/API、Asset Registry UI、additive reason/ACL/index migration、audit/metrics 与 runbook。Gateway、CLIProxyAPI 无产品修改；内部管理出站遵守当前 HTTP-only 真相，未恢复旧 HTTPS 分支。

## Non-Goals

不拥有 Node Register/Edit/Retire/Replace、Node lifecycle schema、replacement lineage、Gateway lifecycle、binding mutation、credential/account/OAuth mutation、CLIProxyAPI account selection/retry/cooldown。禁止 scheduled monitoring 产品 API/UI、arbitrary URL client、Relay Scheduler、pool/weight/P2C、cross-node retry/failover/replay、generic workflow/job framework、Kubernetes 或模型请求 data-plane participation。

## Planning status

Detailed planning = COMPLETE；Stage 3 planning = COMPLETE；Independent readiness review = PASS；Planning readiness = PASS / READY；Implementation readiness = READY。`openspec instructions apply add-relay-node-management-operations` = RUN；Implementation = COMPLETE；Runtime Acceptance = PASS。First independent implementation review = CHANGES REQUIRED（historical P0 = 0 / P1 = 3 / P2 = 1）；三个P1已在第二次独立复审确认FIXED。Second independent implementation re-review = P0 = 0 / P1 = 0 / P2 = 1；唯一P2 stale evidence finding已修正。Final independent implementation re-review = PASS（P0 = 0 / P1 = 0 / P2 = 0）；Independent implementation review = PASS；completed implementation tasks = 57 / 58；Task 50 = UNBLOCKED / NOT COMPLETED；Task 50 closeout evidence reconciliation = COMPLETE；Git/worktree closeout = IN PROGRESS / AUTHORIZED；Archive readiness = PENDING GIT CLOSEOUT。
