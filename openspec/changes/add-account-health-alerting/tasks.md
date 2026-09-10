# Tasks

Detailed Requirements = FROZEN；Architecture Review = PASS；Implementation readiness = READY；Implementation / Runtime Acceptance = NOT STARTED。历史批准、amendment 与 reviewed SHAs 见 [Architecture Review evidence](./planning-validation.md)，不以批准勾选任何实施任务。

以下全部是待实施任务。文档冻结不表示实现完成；每项按一个可独立核验的改动组织，预计超过两小时的项在实施前拆分。

## 1. Contract and compatibility
- [ ] 1.1 实施前核对 proposal/design/spec 与 Ops baseline 和已批准的 Architecture Review evidence 一致；保留前置 implementation baseline 与独立 evidence 中的 reviewed SHAs，不在 change 内硬编码自身最终 SHA；后续架构契约变更须重新评审，继续分开记录需求、架构审批、实施和运行验收状态。
- [ ] 1.2 核对 Inventory qualification 与 current snapshot/health 门禁，增加边界 fixture，不建立第二套资格状态。
- [ ] 1.3 在 api/openapi.yaml 定义 Quality Token 字段、Problems query/DTO/filter/cursor/error，核验排序键身份唯一性。
- [ ] 1.4 统一现行邮箱安全约束（含 AGENTS.md、openspec/config.yaml、canonical specs、OpenAPI 与 Ops 系统文档），保留 Secret 与 metrics 高基数边界。
- [ ] 1.5 修正现行 Availability 陈旧 recovery scenario；明确 durable-job 从空生产 registry 到固定 DingTalk kind 及独立默认关闭 unknown-result replay/direct-success policies 的规范增量，保留历史 archive。

## 2. Read models
- [ ] 2.1 在新 forward read-model migration 增加共享只读 SQL/query-layer Token projection，同一 DB statement 时间计算且无 Token 状态持久化/表/history/checkpoint，覆盖 future→UNKNOWN、相等当前时间→VALID、3598.x→VALID、恰好3599→UNKNOWN、null/不合格→UNKNOWN、ACTIVE invalid+相等当前时间→INVALID；不新增时钟容差。
- [ ] 2.2 增加 Node Account Quality v2 contract 并验证 v1 签名/行为兼容。
- [ ] 2.3 增加 Problems v1 聚合 query、四类 ACTIVE issue、按 current membership 展开 duplicate，absence_confirmed 移出的 Node 立即清除对应 issue；保留降级成员与未恢复 Availability issues。
- [ ] 2.4 加入有界 filters/keyset/no-store 与 ACL；通过实际 query plan 判断是否需要索引。
- [ ] 2.5 更新 sqlc adapter/OpenAPI generated clients（make generate），实现 API 及 400/401/403/503 测试。

## 3. Durable delivery integration
- [ ] 3.1 添加 delivery migration 的 job-kind registration 与 Go registry/schema 校验（10s/5 Execute attempts/replay_safe/no rollback）；按已冻结的 async_job_kinds boolean default false 与 async_jobs enqueue snapshot 契约实施，当前只修订文档，不修改 schema。
- [ ] 3.1a 为既有 job-kind execution policy 增加 allow_unknown_effect_replay，默认 false，仅 DingTalk 启用；enqueue 从 definition/catalog 复制到 job，沿用所有既有策略快照校验；不按 kind-name 硬编码、不扩大 replay_safe。
- [ ] 3.1b 在既有 Worker/Reconciler 集成 policy 的 unknown→retry_wait 有界恢复，保留所有稳定标识、payload/hash、退避、lease/fencing 和普通 job Verify-first；不新增状态或 retry engine。
- [ ] 3.1c 将两个 policy 字段加入重复 enqueue compatibility、Registry.Catalog/DB catalog comparison、Worker/Reconciler job-policy consistency；执行以持久化 job snapshot 为准，registry/config 不得动态改变旧授权。
- [ ] 3.1d 在 Go Registry、DB catalog/job constraint 或等价 persistence validation、catalog compatibility validation 实施 allow_unknown_effect_replay => replay_safe，非法 false/true 组合全部 fail closed。
- [ ] 3.1e 增加 ExecuteSucceeded 与 allow_direct_success 默认 false（仅 DingTalk true）；在 Definition/CatalogEntry/Job、两表 BOOLEAN NOT NULL DEFAULT FALSE、EnqueueTx snapshot、policyMatches、DB mapping 及全部 compatibility 路径一致传递；不改变旧 job 授权。
- [ ] 3.1f 在 Worker 与 DB lifecycle/event/fenced-transition contracts 实施受 persisted direct policy 约束的 running→succeeded，复用既有 status/event、actor=worker；未授权返回成功 fail closed，不新增 direct⇒replay_safe invariant。
- [ ] 3.2 增加 Availability additive transition contract，保留 v1/SERIALIZABLE/confirmation/recovery。
- [ ] 3.3 在 Availability 既有事务中接入 jobs.EnqueueTx，覆盖 disabled/no backfill。
- [ ] 3.4 增加 Duplicate transition-returning contract/adapter，保持 identity/membership/conservative recovery。
- [ ] 3.5 在 Duplicate Evaluate 事务中 EnqueueTx，observer 保留为纯 observability。
- [ ] 3.6 实现固定 key 与 transition-time snapshot；测试连续 ACTIVE、并发重试、recurrence、多个 issue、rename/不导致 lifecycle transition 的 membership 变化不重发；合法 RESOLVED 仍一次 intent。
- [ ] 3.6a 验证 A/B/C→A absence_confirmed：只清 A duplicate issue、B/C ACTIVE 无通知；随后 B absence_confirmed 且合法恢复：清剩余 duplicate issues、一次 RESOLVED intent；其他 Availability issue 不受影响。
- [ ] 3.7 故障注入验证 occurrence/job/event/outbox 全提交或全回滚，以及未知 commit 结果重试的幂等性。

## 4. DingTalk executor
- [ ] 4.1 添加部署环境配置读取/校验，未配置正常启动，非法 HTTPS/signing 配置失败，Secret 不持久化。
- [ ] 4.2 实现独立 direct HTTPS client、Proxy disabled、redirect rejected、HTTP total timeout 5s 与有界响应解析。
- [ ] 4.3 实现 ACTIVE/RESOLVED 消息与可选签名，完整邮箱、Node names、occurrence correlation，无动态诊断。
- [ ] 4.4 测试 DNS/connect/TLS/408/429/5xx/timeout/临时业务失败重试与永久拒绝/invalid response；HTTP 200 非充分成功条件。
- [ ] 4.5 测试第五次失败终态、restart/lease/fencing、发送成功后 crash/replay 重复可接受；DingTalk 不进入 verify/rollback。
- [ ] 4.5a 对照测试同一 timeout/write-reset/expired-running-lease：DingTalk policy=true 有预算时重放，普通 job policy=false/缺省必须 Verify-first；unknown 不伪装成未生效。
- [ ] 4.5b 验证原 job/operation/payload/hash/key/持久退避不变，正常 claim 新 lease/token，旧 worker fenced，五次 Execute 耗尽 failed；取消/期限不被 policy 绕过。
- [ ] 4.5c 覆盖 false/true 非法组合在 registration/catalog/job persistence/compatibility 各入口拒绝；验证 enqueue snapshot、同 key policy 不匹配、catalog mismatch、重启/registry 更新后旧 job snapshot mismatch 在 Worker/Reconciler fail closed，不改旧权限。
- [ ] 4.5d 对照验证 direct 默认 false/DingTalk true、enqueue snapshot、同 key/catalog/Worker/Reconciler policy mismatch；DB 直接调用也不能绕过持久化授权，旧/过期 Worker 成功响应不能提交。
- [ ] 4.5e 验证已知 DingTalk HTTP+business success→ExecuteSucceeded→succeeded、不进入 verifying；unknown DingTalk→unknown replay；普通 unknown/ExecuteNeedsVerification→Verify-first；普通未授权 ExecuteSucceeded→fail closed。
- [ ] 4.5f 验证 direct=true/unknown=false/replay_safe=false 与 direct=false/unknown=true/replay_safe=true 均允许注册；两个授权独立，保持 unknown⇒replay_safe，不新增状态/事件/执行模式/策略表。
- [ ] 4.6 验证 Jobs UI 与 ERROR log 最终失败可见、healthz 不受影响、通知失败不修改 occurrence。
- [ ] 4.7 完成 proxy env、redirect、DB/job/API/UI/audit/log/metrics/trace Secret-negative 测试，错误字符串不得泄漏 URL/query。

## 5. UI
- [ ] 5.1 新增 Problems 只读列表与过滤/分页，多个 issues 一行，Critical/Warning 与 Since 排序。
- [ ] 5.2 扩展现有 Account Quality/Inventory detail 的 Token 与 Expected Valid Until；复用 occurrence history 与系统时区。
- [ ] 5.3 前端验证完整邮箱、UNKNOWN 与 Unavailable 区别、缺失/retired/disabled Availability ACTIVE 保留、duplicate 按领域 current membership 展开，不做 N+1 聚合。

## 6. Acceptance and delivery
- [ ] 6.1 验证 clean install 与现有数据库 forward upgrade、v1 兼容、function owner/search_path/PUBLIC/runtime ACL；生产不 destructive down。
- [ ] 6.2 运行 make test build 并核对生成物，不手改 generated clients。
- [ ] 6.3 在 deploy/acceptance 现有 PostgreSQL/container 体系落实 spec Runtime Acceptance 全部 42 项及 durable-job 增量场景并逐项留证。
- [ ] 6.4 验证 rollout 关闭配置、启用无历史补发、停止 worker/回滚旧 binary 兼容与恢复 durable jobs 的 Runbook。
- [ ] 6.5 汇总测试、query plan、事务故障/重启/Secret-negative 证据及无数据面变化的验收结论。
- [ ] 6.6 Reconcile canonical docs、Ops roadmap/compatibility/runbook；仅凭真实实现/部署证据更新状态与 pin。
- [ ] 6.7 执行 OpenSpec strict validation、引用/diff/clean-worktree 检查；全部任务与验收完成后才 archive。
