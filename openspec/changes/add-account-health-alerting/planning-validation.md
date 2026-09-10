# Planning Validation — Phase 5 Architecture Review

## Reviewed baseline

Phase: 5 — Account Health & Alerting

- Reviewed Control commit: `1b0eb7b9d4232861ca70a01c5883572b22e6e778`
- Reviewed Ops commit: `2e008cecc1f2ad4878a4ae55583ca51c43661278`
- 基线确认：两个仓库在本次 evidence/status update 前的 `git status --short` 均为空；以上为 `git rev-parse HEAD` 实际返回的已提交 SHA。
- 批准来源：用户在本次 Architecture Review evidence/status update 指令中明确确认正式评审完成及下列 PASS 结论。

以上 SHA 标识已评审的 requirement documents，不是本 evidence 文件或后续 status update 的提交 SHA。需求真相源为本 change 的 [design](./design.md)、[account-health-alerting spec](./specs/account-health-alerting/spec.md) 与 [durable-job delta](./specs/durable-job/spec.md)；Ops 规划见 [Phase 5 baseline](../../../../ops/docs/phase-5-7/PHASE5_ACCOUNT_HEALTH_ALERTING_CN.md)。精确评审内容以对应 reviewed commit 为准。

## Historical approval and status

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

## Historical core review checks

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

## Architecture Review amendment — direct success (2026-09-10)

本记录追加于历史 PASS 之后；以上批准、reviewed SHAs 与验证结果保留原含义，不覆盖为本 amendment 的批准证据。

Preflight baseline（已提交且四仓工作树干净；不是本修订最终 SHA）：

- Control: `50d696a099fb4994ffa5103f3cc689ef41b40284`
- Ops: `d407e4b1259ff0f9ee3951d9cf76984131a5b9ae`
- Gateway: `6b045698e6e5e62e35dbd103abf20c1407f8a0bb`
- CLIProxyAPI: `273d624c70f6eb8bdd7b049df396c306acd3f8d0`

Slice A 预检发现 P1 contract gap：ExecuteDisposition 无成功结果，Worker 与 DB lifecycle/event/fenced-transition 只支持经 verifying 完成，与 DingTalk 明确成功后不得 Verify 的冻结语义冲突。依据用户 amendment 指令，暂停实施，仅补充契约。

冻结最小增量：一个 ExecuteSucceeded disposition 加一个独立默认关闭的 allow_direct_success persisted boolean。仅 DingTalk 启用；两表→enqueue snapshot→Worker/Reconciler 与全部 policy compatibility 同步，registry 不改变旧授权。仅明确同步成功、持久化授权、兼容性检查通过及有效 running lease/fencing 才允许 worker running→succeeded，复用既有 status/event；未授权 fail closed。unknown-effect replay 独立，普通 Verify-first 与 unknown⇒replay_safe 保留，不添加 direct⇒replay_safe。DB guards、Worker 对照、fencing 与三条结果路径验收均已列入待实施 tasks/spec。

### Current status after amendment

```text
Detailed Requirements: FROZEN
Architecture Review: REOPENED / CHANGES REQUIRED
Implementation readiness: NOT READY
Architecture Review readiness: READY FOR RE-REVIEW
Implementation: NOT STARTED
Runtime Acceptance: NOT STARTED
```

契约缺口已在文档层解决；此处不声明重新评审 PASS。精确 reviewed SHAs 须在后续独立 re-review approval/evidence 更新中记录，不能使用本文件自引用 SHA。Token、Duplicate、同事务 EnqueueTx、Phase 4 及 Phase 6/7 边界不变。本轮无 production/migration/test implementation，不勾选任何实施任务。

### Amendment document validation

- 当前 change OpenSpec strict：PASS；全仓 strict：20 passed / 0 failed。
- 修改文档 Markdown fences 与 31 个本地引用路径：PASS；Ops compatibility YAML parse：PASS。
- 四仓范围检查：仅 Control 当前 change 六份 Markdown 与 Ops 规划 Markdown/compatibility metadata；production、migration、test implementation、archive、Gateway、CLIProxyAPI 改动均为 false。
- 全部 47 个 implementation tasks 保持未勾选；未执行 runtime tests、migration、部署或 acceptance。
- P0: 0；P1: 0；P2: 0（本 amendment 文档自检，不替代正式 re-review approval）。
- Phase 4 compatibility / Phase 5 internal consistency / Phase 6 boundary / Phase 7 boundary / Minimalism：PASS（契约层）。
