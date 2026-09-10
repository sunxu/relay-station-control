# Proposal

## Phase and outcome
Phase 5 — Account Health & Alerting。Detailed Requirements = FROZEN；Architecture Review = SEPARATELY TRACKED（不声明 PASS/APPROVED）；Implementation = NOT STARTED；Runtime Acceptance = NOT STARTED。本 change 是待实施的正式项目记录，不代表代码已实现或 runtime acceptance 已通过。运维可查看 Antigravity Token 派生状态、全局 Problems，并收到 confirmed occurrence 的 DingTalk ACTIVE/RESOLVED 通知。

## Why
当前 Inventory、Request Quality、Availability 与 Duplicate occurrences 已提供持久事实，但尚无本阶段统一 Token projection、Problem Accounts 与事务可靠通知集成。复用这些事实及 durable jobs，避免第二套健康状态与通知存储。

## What Changes
- 新增 `account-health-alerting` capability：Node Account Quality 增加 token_state/expected_valid_until；Problems 聚合 Availability ACTIVE issues 与 duplicate ACTIVE occurrence 的 current affected-node membership；absence_confirmed 移出的 Node 立即退出对应 duplicate issue。future last_refresh_at 保守 UNKNOWN。
- DingTalk direct HTTPS、no proxy、no redirect、at-least-once；复用 jobs.EnqueueTx 同事务记录通知 intent。
- 冻结全系统完整邮箱非敏感策略，实施时统一现行文档/代码约束；禁止邮箱 Prometheus labels 的原因仍为高基数。

## Capabilities
### New Capabilities
- `account-health-alerting`：Token projection、Problem Accounts、DingTalk 生命周期与运行时验收。
### Modified Capabilities
- `durable-job`：增加默认 false 的 job-kind execution policy `allow_unknown_effect_replay`，仅 dingtalk_alert_delivery 启用；在剩余五次 Execute 总预算内允许 unknown result 经原退避/lease/fencing 重放。未启用者保持 Verify-first，不扩大 replay_safe、不伪造 effect_not_applied。字段作为 async_job_kinds 的默认 false 布尔策略，在 enqueue 时快照到 async_jobs；Worker/Reconciler 依据每条 job 持久化值执行。重复 enqueue、Registry/Catalog 与 DB catalog、job recovery 的策略兼容性检查均包含该字段；不匹配 fail closed。allow_unknown_effect_replay=true 必须要求 replay_safe=true，非法组合拒绝注册/持久化。增量位于 `specs/durable-job/spec.md`，本轮不实施。

本 change 不改变 Phase 4 领域确认、恢复、identity 和 duplicate eligibility。实施归档前须按本 change 的明确覆盖范围统一现行安全/OpenAPI/UI 文档，并修正已被最新 recovery 决定废止的陈旧文字；历史 archive/migrations 保持原样。

## Impact
仅 Control 实施与 Ops 文档/部署配置受影响；Gateway 与 CLIProxyAPI 无改动。受影响真相源：api/openapi.yaml、后续 forward migrations、sqlc queries、生成 Go/TypeScript、认证只读 UI、audit/log 规则与 Runbook。复用 durable-job metrics，不新增账号 metrics 或 DingTalk 平台。

## Compatibility and safety
保留所有 v1 DB contracts；新增版本化 read/reconcile contracts。无新领域表、新 RBAC、凭据持久化。HTTP 5s、job timeout 10s、max attempts 5。生产只做 forward additive migration，回滚停止新行为并保留 schema/job/evidence；旧 worker 的 job-kind 兼容性必须先核验。

## Non-goals
不实施 Phase 6 Gateway/Node 管理、Phase 7 account operations、Token probe/vault、自动修复、通道 CRUD、告警平台、路由 DSL、ACK/silence、exactly-once、监控仪表盘或容量智能。

## References
- `../ops/docs/RELAY_STATION_SYSTEM_DESIGN_CN.md` v1.8 / R4.7：系统边界、Control、阶段验收。
- `../ops/docs/adr/0001-control-technology-stack.md` §2–3；ADR-0002 §2：PostgreSQL/durable-job 与原生调度边界。
- `../ops/docs/phase-5-7/ROADMAP_CN.md` Phase 5。
- 本 change 的 `design.md`、`tasks.md`、`specs/account-health-alerting/spec.md`、`specs/durable-job/spec.md`；相对 ops 路径按项目相邻仓库惯例书写。
