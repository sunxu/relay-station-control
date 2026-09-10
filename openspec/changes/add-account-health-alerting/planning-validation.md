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

## Formal Re-review Approval — direct success (2026-09-10)

批准来源：用户明确确认 direct-success amendment 正式 re-review 完成，并授权记录下列 PASS。以上首次 PASS、preflight gap、REOPENED 与 amendment 过程全部保留；本节为最新批准状态。

### Stable reviewed baseline

更新前四仓 `git status --short` 均为空；已核对 `git rev-parse HEAD` 与 `git log -1 --oneline`：

- Reviewed Control SHA: `53b5a31ffe76156c8143e8ed65cef3728eaf0f46` — `docs(phase5): amend direct success architecture contract`
- Reviewed Ops SHA: `c7a35c00ca7fe5c9f4bc27586ecc9b1002c3f910` — `docs(phase5): align direct success architecture amendment`
- Gateway compatibility baseline: `6b045698e6e5e62e35dbd103abf20c1407f8a0bb`
- CLIProxyAPI compatibility baseline: `273d624c70f6eb8bdd7b049df396c306acd3f8d0`

Reviewed Control/Ops SHAs 指向已提交的 amendment commits，不是本 approval/status update 自身最终 SHA。Gateway/CLIProxyAPI 保持 unchanged compatibility baseline。

### Approved contract checks

1. Direct-success：ExecuteSucceeded 表示本次同步 Execute 已获得足够成功证据；只有 persisted allow_direct_success=true、job policy compatibility 通过且当前 running lease/fencing 有效，才允许 Worker running→succeeded。复用 StatusSucceeded/EventSucceeded，event from_status=running、to_status=succeeded、actor=worker；未授权或旧/过期 Worker 不得提交成功。
2. Persisted policy：allow_direct_success 默认 false，仅 Phase 5 DingTalk 启用；async_job_kinds→EnqueueTx snapshot→async_jobs→Worker/Reconciler。Definition/CatalogEntry/Job、DB catalog/read-write mapping、policyMatches、同 key enqueue 与 Registry/Catalog/DB/Worker/Reconciler compatibility 全覆盖，registry/config 不改变旧 job 授权。
3. 两个 policy 独立：direct success 只授权已知成功跳过 Verify；unknown-effect replay 只授权未知结果有界重新 Execute。保留 unknown⇒replay_safe，不加 direct⇒replay_safe；synthetic direct=true/unknown=false/replay_safe=false 合法，不能 unknown replay。
4. 三条路径：DingTalk HTTP+business 成功→ExecuteSucceeded→直接 succeeded；DingTalk unknown→unknown-replay policy；普通 job unknown→既有 Verify-first。DingTalk replay_safe/unknown/direct=true、rollback_allowed=false、Execute 最多5次、job10s/HTTP5s，不进入 verifying；普通未授权 ExecuteSucceeded fail closed。
5. Minimalism：amendment 仅一个 disposition 加一个 persisted boolean。不新增状态、事件类型、execution-mode enum、policy table/DSL、通知状态机、专用 queue/retry framework、verification receipt 或 delivery ledger；Token、Duplicate、同事务 EnqueueTx 及 Phase 4/6/7 边界不变。

### Approval and current status

```text
Detailed Requirements: FROZEN
Architecture Re-Review: PASS
Architecture Review: PASS
Implementation readiness: READY
Implementation: NOT STARTED
Runtime Acceptance: NOT STARTED

P0: 0
P1: 0
P2: 0

Direct-success contract: PASS
Unknown-effect replay isolation: PASS
Ordinary Verify-first: PASS
Persisted policy snapshot: PASS
Fencing / lease boundary: PASS
Phase 4 compatibility: PASS
Phase 5 internal consistency: PASS
Phase 6 boundary: PASS
Phase 7 boundary: PASS
Minimalism: PASS

Phase 6: PLANNED / NOT STARTED
Phase 7: PLANNED / NOT STARTED
```

本轮仅记录批准，不实现 Slice A；47 个 implementation tasks 全部保持未勾选。正式架构批准不等于 production implementation 或 Runtime Acceptance 通过。

### Re-review status-update validation

- 当前 change OpenSpec strict：PASS；全仓 strict：20 passed / 0 failed。
- Markdown fences 与 30 个本地引用路径检查：PASS；Ops YAML parse 与五项 Phase 5 拆分状态检查：PASS。
- 历史 evidence 前缀与 HEAD 原文完全一致，仅追加本次批准；47 个 task 条目内容及未勾选状态完全不变。
- Control/Ops git diff --check：PASS；仅 approval/status Markdown 与 compatibility metadata 变更。
- production changed = false；migration changed = false；test implementation changed = false；archive changed = false；Gateway changed = false；CLIProxyAPI changed = false。
- 未执行 production build/runtime tests、migration、deploy acceptance；未 commit/push。

## Cancellation-race discovery and Architecture Amendment — 2026-09-10

本节追加新的发现与当前状态，不覆盖首次 PASS、direct-success preflight reopen/amendment、Formal Re-review PASS 或 [Slice A implementation evidence](./slice-a-validation.md)。此前批准仍是对应历史 baseline 的结论，不表示本 cancellation amendment 已获批准。

Discovery context（当前 HEAD，附有未提交 Slice A 工作树；不是本 amendment 的 reviewed/approval commit）：

- Control: `ec0e8a2951cc158509c62942f0e622b98bd4e27f`
- Ops: `6d7f5d86deb613d62262475b675e75d55b571148`
- Gateway compatibility baseline: `6b045698e6e5e62e35dbd103abf20c1407f8a0bb`
- CLIProxyAPI compatibility baseline: `273d624c70f6eb8bdd7b049df396c306acd3f8d0`

Slice A Implementation Review 发现 policy/evidence 混淆；进一步核对既有 cancellation contract，确认 running 执行 ExecuteRetryableNoEffect 时，claim 后、retry commit 前登记取消会落入 retry_wait，而 Worker 排除取消行、Reconciler 不扫描 retry_wait，缺少确定性收敛，只能等待 deadline/max-attempt sweep。停止实现，不通过未经评审的新状态转换绕过 gap。

```text
Detailed Requirements: FROZEN
Architecture Review: REOPENED / CHANGES REQUIRED
Implementation readiness: NOT READY
Implementation: IN PROGRESS — PAUSED AT SLICE A
Runtime Acceptance: NOT STARTED
```

本轮冻结的 amendment 见 [design](./design.md) cancellation-race section 与 [durable-job delta](./specs/durable-job/spec.md)：

1. 窄 running→cancelled 仅授权有效 Worker、当前 lease/fence、锁内可见取消、明确当前 no-effect proof、无 unresolved prior unknown；EventCancelled / worker / cancel_verified_safe，释放 lease。
2. policy != evidence。使用既有 immutable events 的 framework-generated reason codes：effect_unknown_unverified、execute_retryable_no_effect、effect_absent_verified；当前 no-effect 不能清除之前 unknown，VerifyEffectAbsent verifying→retry_wait 的 marker 消解此前 unknown，只考虑最新 verified marker 之后的 unknown。
3. cancellation matrix A～E 冻结：current/prior unresolved unknown 加取消必须 failed / cancel_after_unknown_effect；Verify 已消解后可安全取消，普通 retry_wait 取消保持。DB cancel request 与 fenced transition 均按真实证据判定。
4. ExecuteSucceeded 与锁内并发取消必须 failed / cancel_after_effect_applied，不提交 succeeded，不新增自动 rollback；NeedsVerification 与 PermanentFailure 保持既有语义。
5. 最大增量仅一个窄 existing-state transition 加固定 reason semantics；无新 status/event type/policy/column/table/ledger/queue/worker/retry engine。普通 Verify-first、两个 persisted policies 及 Phase 4/6/7 边界保持。

本轮仅文档修订；已有未提交 Slice A production/migration/generated/test 工作树及验证记录保留，不回退、不继续实现。未来 re-review PASS 后直接修订尚未 commit/release/deploy 的 00028，不为此新建 00029；仅当届时 00028 已成为正式不可修改 baseline 才使用新的合法 forward migration。

tasks.md 保留此前 7 个完成勾选作为旧 Slice A evidence，新增 3.1g～3.1i 均未勾选；旧勾选不代表 amended contract 已实现。proposal 的 stale approval-only wording 已修正。Full-store suite 仍为此前记录的 NOT GREEN，本轮不执行或修复其 baseline failures，也不把旧 focused tests 宣称为 amendment 验收。

### Cancellation amendment validation

- 当前 change OpenSpec strict：PASS；全仓 strict：20 passed / 0 failed。
- Markdown fences 与 38 个本地引用路径检查：PASS；Ops YAML parse 与五项 Phase 5 拆分状态检查：PASS。
- 旧 planning-validation 全文作为前缀保留，仅追加本次 discovery/amendment；tasks 保留原 7 个完成项，总计 50 项，新增 3 项均 open。
- 与本轮开始时全部 tracked/untracked 文件 SHA-256 对比：仅 Control 六个 active change 文档与 Ops 五个规划文件改变；既有 Slice A production、migration、generated、test 与 slice-a-validation.md 完全保留。四仓 HEAD 与 Git index 指纹均不变；Gateway/CLIProxyAPI 全部文件指纹不变。
- Control/Ops git diff --check：PASS。整个工作树仍有 pre-existing uncommitted Slice A implementation changes；只有本 amendment 的增量是 docs/status-only，不能称整个 working tree 为 docs-only。
- 本轮未运行生产测试、build/generate、migration 或运行验收，未修复 full-store baseline failures；未 commit/push。

Architecture re-review readiness: READY（文档已可供重审，不是批准）。Architecture Review 保持 REOPENED / CHANGES REQUIRED，Implementation readiness = NOT READY；恢复实施须另获 re-review PASS。
