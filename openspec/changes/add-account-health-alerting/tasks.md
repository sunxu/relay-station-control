# Tasks

Detailed Requirements = FROZEN；Architecture Review = PASS；Implementation readiness = READY；Implementation = IN PROGRESS；Runtime Acceptance = NOT STARTED。历史批准、amendment 与 reviewed SHAs 见 [Architecture Review evidence](./planning-validation.md)，不以批准勾选任何实施任务。

以下按真实验证结果跟踪实施任务。文档冻结不表示实现完成；每项按一个可独立核验的改动组织，预计超过两小时的项在实施前拆分。

Slice A/B/C 均已由用户确认 Implementation Review PASS 并提交：`2eee43d` / `dc510ce` / `b7c4d11`，历史证据见 [Slice A](./slice-a-validation.md)、[Slice B](./slice-b-validation.md)、[Slice C](./slice-c-validation.md)。因 Slice D 依赖真实 production Executor，用户批准先完成 Slice E Executor foundation；其正式 Implementation Re-review PASS 后已提交为 `ac21b99c2b70fd863fdf37ed492a89858352a54f`，见 [Slice E evidence](./slice-e-executor-validation.md)。Slice D 随后由用户正式确认 Implementation Review PASS（P0/P1/P2=0），并提交为 `187718223f62669e9c722187c0833f5478a5f313`；[Slice D evidence](./slice-d-validation.md) 保留提交前验证历史。本轮以该提交为基线，仅补 DingTalk runtime/restart/replay focused evidence；Architecture Review 保持 PASS，Problems UI 与正式 Runtime Acceptance 不在本轮范围。

## 1. Contract and compatibility
- [x] 1.1 实施前核对 proposal/design/spec 与 Ops baseline 和已批准的 Architecture Review evidence 一致；保留前置 implementation baseline 与独立 evidence 中的 reviewed SHAs，不在 change 内硬编码自身最终 SHA；后续架构契约变更须重新评审，继续分开记录需求、架构审批、实施和运行验收状态。
- [x] 1.2 核对 Inventory qualification 与 current snapshot/health 门禁，增加边界 fixture，不建立第二套资格状态。
- [x] 1.3 在 api/openapi.yaml 定义 Quality Token 字段、Problems query/DTO/filter/cursor/error，核验排序键身份唯一性。
- [ ] 1.4 统一现行邮箱安全约束（含 AGENTS.md、openspec/config.yaml、canonical specs、OpenAPI 与 Ops 系统文档），保留 Secret 与 metrics 高基数边界。
- [ ] 1.5 修正现行 Availability 陈旧 recovery scenario；明确 durable-job 从空生产 registry 到固定 DingTalk kind 及独立默认关闭 unknown-result replay/direct-success policies 的规范增量，保留历史 archive。

## 2. Read models
- [x] 2.1 在新 forward read-model migration 增加共享只读 SQL/query-layer Token projection，同一 DB statement 时间计算且无 Token 状态持久化/表/history/checkpoint，覆盖 future→UNKNOWN、相等当前时间→VALID、3598.x→VALID、恰好3599→UNKNOWN、null/不合格→UNKNOWN、ACTIVE invalid+相等当前时间→INVALID；不新增时钟容差。
- [x] 2.2 增加 Node Account Quality additive contract（基线 v2/v3 已存在，实际使用下一版本 v4）并验证既有 v1/v2/v3 签名/行为兼容。
- [x] 2.3 增加 Problems v1 聚合 query、四类 ACTIVE issue、按 current membership 展开 duplicate，absence_confirmed 移出的 Node 立即清除对应 issue；保留降级成员与未恢复 Availability issues。
- [x] 2.4 加入有界 filters/keyset/no-store 与 ACL；通过实际 query plan 判断是否需要索引。
- [x] 2.5 更新 sqlc adapter/OpenAPI generated clients（make generate），实现 API 及 400/401/403/503 测试。

## 3. Durable delivery integration
- [x] 3.1 添加 delivery migration 的 job-kind registration 与 Go registry/schema 校验（10s/5 Execute attempts/replay_safe/no rollback）；按已冻结的 async_job_kinds boolean default false 与 async_jobs enqueue snapshot 契约实施，保持 forward additive，不修改历史 migration。
- [x] 3.1a 为既有 job-kind execution policy 增加 allow_unknown_effect_replay，默认 false，仅 DingTalk 启用；enqueue 从 definition/catalog 复制到 job，沿用所有既有策略快照校验；不按 kind-name 硬编码、不扩大 replay_safe。
- [x] 3.1b 在既有 Worker/Reconciler 集成 policy 的 unknown→retry_wait 有界恢复，保留所有稳定标识、payload/hash、退避、lease/fencing 和普通 job Verify-first；不新增状态或 retry engine。
- [x] 3.1c 将两个 policy 字段加入重复 enqueue compatibility、Registry.Catalog/DB catalog comparison、Worker/Reconciler job-policy consistency；执行以持久化 job snapshot 为准，registry/config 不得动态改变旧授权。
- [x] 3.1d 在 Go Registry、DB catalog/job constraint 或等价 persistence validation、catalog compatibility validation 实施 allow_unknown_effect_replay => replay_safe，非法 false/true 组合全部 fail closed。
- [x] 3.1e 增加 ExecuteSucceeded 与 allow_direct_success 默认 false（仅 DingTalk true）；在 Definition/CatalogEntry/Job、两表 BOOLEAN NOT NULL DEFAULT FALSE、EnqueueTx snapshot、policyMatches、DB mapping 及全部 compatibility 路径一致传递；不改变旧 job 授权。
- [x] 3.1f 在 Worker 与 DB lifecycle/event/fenced-transition contracts 实施受 persisted direct policy 约束的 running→succeeded，复用既有 status/event、actor=worker；未授权返回成功 fail closed，不新增 direct⇒replay_safe invariant。
- [x] 3.2 增加 Availability additive transition contract，保留 v1/SERIALIZABLE/confirmation/recovery。
- [x] 3.3 在 Availability 既有事务中接入 jobs.EnqueueTx，覆盖 disabled/no backfill。
- [x] 3.4 增加 Duplicate transition-returning contract/adapter，保持 identity/membership/conservative recovery。
- [x] 3.5 在 Duplicate Evaluate 事务中 EnqueueTx，observer 保留为纯 observability。
- [x] 3.6 实现固定 key 与 transition-time snapshot；测试连续 ACTIVE、并发重试、recurrence、多个 issue、rename/不导致 lifecycle transition 的 membership 变化不重发；合法 RESOLVED 仍一次 intent。
- [x] 3.6a 验证 A/B/C→A absence_confirmed：只清 A duplicate issue、B/C ACTIVE 无通知；随后 B absence_confirmed 且合法恢复：清剩余 duplicate issues、一次 RESOLVED intent；其他 Availability issue 不受影响。
- [x] 3.7 故障注入验证 occurrence/job/event/outbox 全提交或全回滚，以及未知 commit 结果重试的幂等性。

## 3A. Cancellation amendment — future implementation after re-review

- [x] 3.1g 在 re-review PASS 后修订未提交/未发布/未部署的 00028（仅届时已成为不可修改 baseline 才使用新 migration），由 framework 固定生成三种 reason_code；Worker/current unknown 与 expired-running recovery 记录 effect_unknown_unverified，no-effect 记录 execute_retryable_no_effect，VerifyEffectAbsent verifying→retry_wait 记录 effect_absent_verified；按 latest verified 之后的 immutable sequence 判 unresolved unknown，不以 policy/error_code/永久历史 sticky 替代事实。
- [x] 3.1h 修订现有 cancel request 与 fenced transition guards：有效 Worker+cancel 可见+明确当前 no-effect+无 unresolved unknown 才 running→cancelled，EventCancelled/worker/cancel_verified_safe/release lease；direct-success 并发取消 running→failed/cancel_after_effect_applied。锁内重检，保持 NeedsVerification、PermanentFailure、budget/deadline/fencing；不新增自动 rollback 路径。
- [x] 3.1i focused unit/DB 对照覆盖 cancellation matrix A～E、true/false policy 不充当 evidence、prior unknown + later no-effect 保留风险、Verify marker 消解旧 unknown 但不消解其后的新 unknown、current unknown 与 direct-success 并发取消、无 proof/旧 fence 拒绝、deadline/max-attempt 阻止 replay、普通 Verify-first/永久失败回归；验证事件与状态原子提交。只在真实通过后勾选，不以旧 Slice A 测试替代。

## 4. DingTalk executor

本轮 4.5 / 4.5a / 4.5b / 4.5d / 4.5e 的 focused PostgreSQL 与回归证据见 [Slice E runtime validation](./slice-e-runtime-validation.md)；Tasks 36/50，不代表正式 Runtime Acceptance 或本轮 Implementation Review 已通过。

- [x] 4.1 添加部署环境配置读取/校验，未配置正常启动，非法 HTTPS/signing 配置失败，Secret 不持久化。
- [x] 4.2 实现独立 direct HTTPS client、Proxy disabled、redirect rejected、HTTP total timeout 5s 与有界响应解析。
- [x] 4.3 实现 ACTIVE/RESOLVED 消息与可选签名，完整邮箱、Node names、occurrence correlation，无动态诊断。
- [x] 4.4 测试 DNS/connect/TLS/408/429/5xx/timeout/临时业务失败重试与永久拒绝/invalid response；HTTP 200 非充分成功条件。
- [x] 4.5 测试第五次失败终态、restart/lease/fencing、发送成功后 crash/replay 重复可接受；DingTalk 不进入 verify/rollback。
- [x] 4.5a 对照测试同一 timeout/write-reset/expired-running-lease：DingTalk policy=true 有预算时重放，普通 job policy=false/缺省必须 Verify-first；unknown 不伪装成未生效。
- [x] 4.5b 验证原 job/operation/payload/hash/key/持久退避不变，正常 claim 新 lease/token，旧 worker fenced，五次 Execute 耗尽 failed；取消/期限不被 policy 绕过。
- [x] 4.5c 覆盖 false/true 非法组合在 registration/catalog/job persistence/compatibility 各入口拒绝；验证 enqueue snapshot、同 key policy 不匹配、catalog mismatch、重启/registry 更新后旧 job snapshot mismatch 在 Worker/Reconciler fail closed，不改旧权限。
- [x] 4.5d 对照验证 direct 默认 false/DingTalk true、enqueue snapshot、同 key/catalog/Worker/Reconciler policy mismatch；DB 直接调用也不能绕过持久化授权，旧/过期 Worker 成功响应不能提交。
- [x] 4.5e 验证已知 DingTalk HTTP+business success→ExecuteSucceeded→succeeded、不进入 verifying；unknown DingTalk→unknown replay；普通 unknown/ExecuteNeedsVerification→Verify-first；普通未授权 ExecuteSucceeded→fail closed。
- [x] 4.5f 验证 direct=true/unknown=false/replay_safe=false 与 direct=false/unknown=true/replay_safe=true 均允许注册；两个授权独立，保持 unknown⇒replay_safe，不新增状态/事件/执行模式/策略表。
- [x] 4.6 验证 Jobs UI 与 ERROR log 最终失败可见、healthz 不受影响、通知失败不修改 occurrence。
- [x] 4.7 完成 proxy env、redirect、DB/job/API/UI/audit/log/metrics/trace Secret-negative 测试，错误字符串不得泄漏 URL/query。

## 5. UI
- [x] 5.1 新增 Problems 只读列表与过滤/分页，多个 issues 一行，Critical/Warning 与 Since 排序。
- [x] 5.2 扩展现有 Account Quality/Inventory detail 的 Token 与 Expected Valid Until；复用 occurrence history 与系统时区。
- [x] 5.3 前端验证完整邮箱、UNKNOWN 与 Unavailable 区别、缺失/retired/disabled Availability ACTIVE 保留、duplicate 按领域 current membership 展开，不做 N+1 聚合。

## 6. Acceptance and delivery
- [ ] 6.1 验证 clean install 与现有数据库 forward upgrade、v1 兼容、function owner/search_path/PUBLIC/runtime ACL；生产不 destructive down。
- [ ] 6.2 运行 make test build 并核对生成物，不手改 generated clients。
- [ ] 6.3 在 deploy/acceptance 现有 PostgreSQL/container 体系落实 spec Runtime Acceptance 全部 42 项及 durable-job 增量场景并逐项留证。
- [ ] 6.4 验证 rollout 关闭配置、启用无历史补发、停止 worker/回滚旧 binary 兼容与恢复 durable jobs 的 Runbook。
- [ ] 6.5 汇总测试、query plan、事务故障/重启/Secret-negative 证据及无数据面变化的验收结论。
- [ ] 6.6 Reconcile canonical docs、Ops roadmap/compatibility/runbook；仅凭真实实现/部署证据更新状态与 pin。
- [ ] 6.7 执行 OpenSpec strict validation、引用/diff/clean-worktree 检查；全部任务与验收完成后才 archive。
