# Design

## Context
Requirements freeze implementation baseline：Control `f4173242aef83afd95d3240573c68eb4299987a2`；前置 Ops 路线规划基线：`a4f01f80c588f32a51625315f09d252045652edc`。这些是需求冻结前的既有基线，不是本 change 的最终提交或 Architecture Review 通过证据。

Architecture Review target：评审时两个仓库 main 上当前已提交的 Phase 5 requirement documents。历史批准的 exact reviewed repository SHAs 已记录于独立的 [Architecture Review evidence](./planning-validation.md)；本 change 不硬编码自身最终 commit SHA，amend 不要求更新自引用 SHA。

Detailed Requirements = FROZEN；Architecture Review = REOPENED / CHANGES REQUIRED；Implementation readiness = NOT READY；Implementation = NOT STARTED；Runtime Acceptance = NOT STARTED。

现有实现接入点：

| Concern | 真实路径 / contract | Phase 5 接入方式 |
|---|---|---|
| Node Account Quality | `internal/store/node_account_quality.go` | 新 v2 read contract，保留 v1 |
| Inventory | `internal/store/account_inventory_readonly_query.go`；current promoted snapshot 与 provider states | 复用当前资格门禁，不独立采集 |
| Request Quality | `internal/store/account_request_quality.go` | 只关联既有请求事实，不重新分类 |
| Availability | `internal/store/account_availability_reconcile.go`；`control_reconcile_account_availability_v1` | 在现有 SERIALIZABLE/page/retry 边界内返回 transitions 并 enqueue |
| Duplicate | `internal/store/cross_node_duplicate_ownership_lifecycle.go` 的 `Evaluate`；`cross_node_duplicate_ownership_reconciliation.go` | 在现有 Evaluate transaction 内集成，保留 account/environment identity |
| Job enqueue | `internal/jobs/enqueue.go` 的 `EnqueueTx` | 复用 registry、payload hash、InsertBundle、event、wake outbox |
| API | `api/openapi.yaml` | 唯一 API 真相；生成代码不手改 |

适用系统设计 v1.8/R4.7 的系统边界与阶段验收、ADR-0001 §2–3 的单环境 PostgreSQL/模块化单体、ADR-0002 §2 的原生调度边界。未来 account mutation 不在本 change 内。

## Decisions

### Token projection and qualification
唯一算法在 PostgreSQL query 层计算，使用同一 statement 的 DB 时间与一致 read snapshot：ACTIVE token_invalid → INVALID；否则 Inventory 不合格 → UNKNOWN；否则 last_refresh_at 为空 → UNKNOWN；否则 last_refresh_at > database_now → UNKNOWN；否则 database_now < last_refresh_at + 3599 seconds → VALID；其余 UNKNOWN。last_refresh_at == database_now 不是 future，合格且无 ACTIVE TOKEN_INVALID 时为 VALID；不新增 clock skew tolerance、grace 或时钟同步能力。expected_valid_until 在 refresh 非空时返回 refresh + 3599 秒，即使为 future、已过期或 Token INVALID；refresh 为空返回 null。它不是实际 expiration。

Qualification 复用 current promoted Inventory、present 生命周期、当前 Provider 策略、fresh/complete/eligible 证据及最新采集/health 门禁；不允许 stale、incomplete、missing、out_of_scope、disk fallback、不可验证或更新失败仅凭历史 refresh 得到 VALID。不得把 `availability = AVAILABLE` 当 Token VALID 的等价条件，不改变既有 duplicate eligibility。首版 antigravity 范围之外不生成本阶段 Token/Problems/通知能力，不推断其他 Provider。

15 分钟 Request Quality 窗口、Phase 4 failure confirmation 窗口仍属于原领域；删除的是“15 分钟成功请求才使 Token VALID”规则，不是删除合法故障确认窗口。恢复仍要求真实 success 严格晚于 occurrence.last_failure_at 且既有 guards 满足；refresh、file_active 不恢复故障。

### Problems and API/UI
新增 `POST /api/problem-accounts/query`，返回 items/next_cursor。Node Account Quality DTO 添加 token_state、expected_valid_until；DB 使用 `control_query_node_account_quality_v2` 与新 Problems v1 query，函数签名在实现时与 sqlc 同步，不覆盖 v1。

一行是 instance_id（领域 node_id）+ account_key，issues 聚合四种 supported ACTIVE occurrence；按 occurrence 驱动再关联 current diagnostics，不以 present/fresh/active Node 的 inner join 过滤掉 confirmed problems。Availability issue 仅在对应 occurrence 合法 RESOLVED 后消失；UNKNOWN、DISABLED、stale、missing/out_of_scope、Node retired 不构成 recovery。duplicate 按 ACTIVE occurrence 的 current affected-node membership 展开并共享 occurrence_id；某 Node 经既有领域确认 absence_confirmed 并移出 membership，该 Node 的 duplicate issue 立即消失，不要求整体 RESOLVED。stale/unavailable/unverifiable/degraded/incomplete 不足以移除 membership。该行 supported ACTIVE issues 归零才退出，不复制领域状态。

字段、固定 severity/oldest/email/instance 排序、filters 见 spec。Critical 高于 Warning；服务端 keyset cursor 绑定 scope/filters，沿用 no-store、401/403/400/503 语义，不把 DB 失败当空结果。batch query 避免 N+1。UI 仅新增 Problems 导航并复用现有 Topology/Account Quality/Inventory 详情与 occurrence history，显示完整邮箱和 Expected Valid Until。DB 使用 UTC/timestamptz，UI 使用系统时区。

### Transaction and concurrency
Availability 的既有 SERIALIZABLE、100-key page 与 40001/40P01 有界事务重试保持。Duplicate 保持现有 Evaluate 的事务/锁/唯一性规则。两个领域采用 additive transition-returning contracts，向 Go 暴露本次事务中实际创建/恢复的 occurrence 及 display snapshot，不扫描事后已提交 occurrence 推导 intent。

```text
BEGIN existing domain transaction
  reconcile using existing confirmation/recovery/identity rules
  collect actual ACTIVE / RESOLVED transition records
  if configured: jobs.EnqueueTx(tx, transition snapshot, fixed key)
COMMIT
```

`EnqueueTx` 不自行提交。occurrence、job、初始 event、wake outbox 全部提交或全部回滚。任何 enqueue/registry/payload 错误必须使该事务失败；网络发送始终在 worker、事务提交后进行。未知 commit 结果按原事务重试/幂等机制处理；不重建不同 snapshot 绕过 payload-hash 冲突，不覆盖已有 job payload。同一 occurrence transition 至多一个逻辑 intent。现有 after-commit duplicate observer 仅用于观测，不能承担通知可靠性。

ACTIVE/RESOLVED 使用 spec 固定四种 key；recurrence 产生新 occurrence ID。连续 ACTIVE、名称变化、证据降级及不导致 lifecycle transition 的 membership change 不 enqueue。合法 ACTIVE→RESOLVED 必须产生一次 RESOLVED intent（配置启用时）。A/B/C 重复时 A 经 absence_confirmed 退出，仅清除 A 的 duplicate issue，B/C 保留且无通知；B 随后确认退出并满足领域 recovery guards，则整体 RESOLVED、清除剩余 duplicate issues 并同事务 enqueue 一次 recovery intent。URL 关闭时领域照常提交但不 enqueue；后续启用不补发过去 ACTIVE。各 transition 独立排队，不承诺外部消息严格有序；通知通过 occurrence_id/transition/时间关联，不另建排序状态机。

### Delivery execution
注册首个 production `dingtalk_alert_delivery` kind，payload schema 1、default timeout 10s（job execution budget；单次 HTTP total timeout 仍固定 5s）、max attempts 5（包括第一次）、replay_safe=true、allow_unknown_effect_replay=true、allow_direct_success=true、rollback_allowed=false。数据库 registration 与 Go registry 同步并检查兼容。框架如要求 verification attempts 字段，使用其合法默认值，但业务永不进入 verifying、rolling_back、rolled_back。

Phase 5 增加两个独立默认 false 的 job-kind execution policies：`allow_unknown_effect_replay` 与 `allow_direct_success`，仅 DingTalk 启用；不扩大 replay_safe，不使用 kind-name 条件硬编码。Worker Execute 的 timeout/write 后 reset 等未知结果，以及 Reconciler 接管 running expired lease，仅按 allow_unknown_effect_replay 和剩余预算决定 retry_wait；普通 job 保持 Verify-first，unknown 不伪装为 effect_not_applied。复用原 durable backoff 和有界扫描，不新建状态、retry engine、queue/outbox。恢复更新仍须取得有效恢复 lease/fencing；后续 Execute 必须正常 claim 新 execution lease/token。job_id、operation_id、payload/hash、idempotency key 均不变，Execute 最多五次、耗尽 failed；已有取消请求或终止期限仍阻止重放，未知效果不得伪装为安全取消。完整行为以本 change 的 durable-job 增量 spec 为准。

### Direct-success amendment
Slice A preflight 确认现有 ExecuteDisposition 无成功结果，Worker、DB lifecycle/event/fenced-transition 仅经 verifying 完成 succeeded；本节冻结最小扩展，尚待重新评审，不代表实施授权。

新增通用 `ExecuteSucceeded`，仅表示 Executor 从本次同步 Execute 获得足以确认操作成功的结果，且 job kind 被显式授权跳过 Verify。DingTalk HTTP 与 business response 均成功才返回此结果；未知结果、可重试无效果、needs verification 均不能冒充成功。

只有经过 policy compatibility 校验、persisted job.allow_direct_success=true 且当前 running lease/fencing 有效时，Worker 才允许 running→succeeded，复用 StatusSucceeded/EventSucceeded；event 为 from_status=running、to_status=succeeded、actor=worker。后续 forward migration 必须同步 lifecycle guard、event constraint 与 fenced-transition contract，并在 DB 检查持久化授权。旧/过期 Worker 即使持有成功响应也不能提交。普通 job 未授权却返回 ExecuteSucceeded 必须 fail closed，不能全局开放直接成功。

allow_direct_success 控制已知成功是否跳过 Verify；allow_unknown_effect_replay 只控制未知效果能否有界重放，两者互不替代或依赖。不新增 allow_direct_success⇒replay_safe invariant；保留 unknown replay⇒replay_safe。两个默认 false 的普通 job 继续 ExecuteNeedsVerification→verifying→Verify→succeeded/retry/rollback/failed。已知 DingTalk 成功、未知 DingTalk 结果、普通 job unknown 必须分别覆盖验收。

本 amendment 的最大增量为一个 Execute disposition 加一个默认关闭的 persisted boolean；不新增状态、事件类型、execution mode enum、policy table/DSL、verification receipt、delivery ledger 或通知状态机。

### Persisted execution-policy snapshot
`allow_unknown_effect_replay` 与 `allow_direct_success` 各作为现有 execution policy 的独立 boolean 扩展，沿用 Timeout、LeaseDuration、HeartbeatInterval、MaxAttempts、MaxVerifyAttempts、ReplaySafe、AllowRollback 的 per-job snapshot 模式：

```text
job-kind definition / async_job_kinds.{allow_unknown_effect_replay, allow_direct_success}
(each BOOLEAN NOT NULL DEFAULT FALSE)
→ enqueue snapshot
async_jobs.{allow_unknown_effect_replay, allow_direct_success}
(each BOOLEAN NOT NULL DEFAULT FALSE, copied from validated job-kind at enqueue)
→ Worker / Reconciler read persisted job policy after compatibility validation
```

只有 dingtalk_alert_delivery 的 definition/catalog 为 true；普通 job 默认 false。enqueue 在既有 EnqueueTx 中复制字段，不新增事务或 enqueue logic。同幂等键重入队的 execution-policy compatibility check、Registry.Catalog 与 DB catalog comparison、Worker/Reconciler job-policy comparison 都必须包含两个字段，覆盖 jobs.Definition、jobs.CatalogEntry、jobs.Job、policyMatches 与 DB read/write mapping。Worker/Reconciler MUST 以 job 上持久化的 snapshot 授权各自的重放或直接成功，不能由当前进程 registry/config 动态改变旧 job 权限；job snapshot 与当前 registry/catalog 不一致时 fail closed / policy mismatch，不覆盖 snapshot、不继续按新权限执行。

Invariant：`allow_unknown_effect_replay=true` REQUIRES `replay_safe=true`。Go Registry validation、DB catalog/schema constraint 或等价持久化校验（catalog 与 job snapshot 均覆盖）、catalog compatibility validation MUST 拒绝 false/true 非法组合。DingTalk 固定 true/true；普通 job 保持 unknown replay=false，Verify-first 不变。此处冻结的是架构契约，本轮不写 migration/SQL。后续验收必须覆盖非法组合 registration/persistence rejected，以及旧 job snapshot 与新 registry 不匹配时 Worker/Recovery/重复 enqueue 均 fail closed。

不新增 execution policy table、policy DSL、notification table/outbox、delivery ledger、workflow engine、job state、DingTalk 专用 retry engine 或 RBAC；不按 job kind 名硬编码行为。

### Delivery payload and transport
payload 是 spec 列明的 transition-time display snapshot；完整 email 允许，Secret 和动态 Token/Quality 诊断不得加入。重试使用原快照。ACTIVE/RESOLVED 之间 rename 允许名称不同；不保存 display history。复用 lease/fencing/backoff/restart recovery；投递失败不修改 occurrence。

独立标准 HTTPS client，不复用 internal management client；显式关闭 proxy（忽略 HTTP_PROXY/HTTPS_PROXY/ALL_PROXY），拒绝所有 redirects，total timeout 5s。仅从受控部署配置读取 DINGTALK_WEBHOOK_URL 和可选 DINGTALK_SIGNING_SECRET，不提供 API/UI 动态 URL、allowlist/CIDR/DNS pinning。URL 未配置正常启动；非法配置启动失败。发送时按 DingTalk signing contract 在内存构造签名，URL/query/原始响应不落日志或 DB。

DNS/connect/TLS/timeout/ambiguous、408/429/5xx、明确临时业务失败重试；配置/签名错误、400/401/403/404、redirect、明确永久拒绝、invalid/unrecognized response 永久失败。200 必须同时具有成功业务响应。只解析有界响应于内存，使用固定安全 error_code，不透传客户端错误中的 URL。总尝试最多五次，因此 at-least-once 表示接受未知结果重放和重复副作用，不承诺永久故障下最终必达。

POST 成功后未提交 succeeded 即 crash 可造成重复通知，这是通过行为；不加 delivery ledger、sent 标记、verify 协议或 exactly-once。最终失败通过现有 Jobs UI 与 structured ERROR log 发现，DingTalk 不进入 /healthz。

### Security and explicit supersession
用户冻结策略优先于仓库旧措辞：邮箱在整个 Relay Station 是普通非敏感业务身份；可完整进入 PostgreSQL/API/UI/audit/structured logs/job payload/DingTalk，不 mask、不 HMAC、不新增邮箱权限或特殊安全审计。此覆盖不限于 Problems 页面。Prometheus 仍禁用 email/account identity 高基数 label。

现行 `AGENTS.md`、`openspec/config.yaml`、Inventory readonly 与相关 canonical specs、OpenAPI 注释、Ops system design 中旧“邮箱不得进入日志/审计/告警”的限制须在实施文档 reconciliation 中统一；不是沿用旧策略缩小本次冻结范围。Secret（Webhook URL/signing secret/management key/access token/refresh token/auth file）仍禁止进入 DB/job/API/UI/audit/log/metric/trace，原始响应仍禁止保存。

新查询使用 SECURITY DEFINER、fixed search_path=pg_catalog、owner relay_control_migrator、REVOKE EXECUTE FROM PUBLIC、GRANT EXECUTE TO relay_control_runtime；runtime 无新增底表直接权限。继续唯一 super_admin 与既有会话/请求防护；不新增 RBAC。

## Migration, rollout and rollback
不在本文档交付中编造 migration 编号或实施 SQL。实施使用下一个可用 forward Goose migration，read-model 与 delivery concern 分开；不修改任何历史 migration，不新增领域表/Token表/通知表/outbox/config表。后续实施仅增加 read functions/ACL、transition-returning reconciliation contracts、job-kind registration 及既有 execution-policy/recovery contracts 的必要 additive 扩展，两个 policy 在 async_job_kinds 的布尔定义及 async_jobs 的 enqueue snapshot 持久化语义已在上节冻结；后续实施按该契约编写 additive migration，本轮不修改 migration 或 SQL/schema；索引仅在实际 EXPLAIN/acceptance 证明需要时增加。

先升级 additive schema，核验 runtime ACL 与旧 v1，然后部署匹配 registry/worker 和 API/UI；DingTalk 初始关闭，完成隔离验收后按部署授权配置 Secret。发布 pin 与 phase_4_closed 不因文档冻结而变更。

回滚停止新 enqueue 与 delivery worker，保留 forward schema、occurrences/jobs/events/outbox。仅取消 URL 配置不应被假定为安全处理已有 queued jobs 的完整回滚方案；必须验证 worker 暂停和恢复路径。旧 binary 可能不识别新 kind，先完成兼容性/停机排空决策再回滚，不删 registry 或未处理证据，不执行 production destructive down。重新启用处理既有 durable jobs，仍不扫描补发从未 enqueue 的 ACTIVE。

## Validation and reconciliation
spec 的全部 42 项 runtime acceptance 是未来实施门槛，tasks 记录未完成状态。重点验证时间边界、退化证据、并发/同事务故障注入、commit ambiguity、restart/lease replay、HTTP/业务响应分类、第五次失败终态、proxy 环境与 redirect、Secret-negative、完整邮箱、ACL/no-store/auth、API pagination、UI 复用。

仓库 canonical Availability 中 `Active runtime after previously confirmed forbidden` scenario 仍有“两次健康观察恢复”的旧句，与最新 `Recovery SHALL require newer independent evidence` 冲突。实施时仅统一现行文档到最新 success-only recovery，不修改历史 archive，不恢复旧 SQL 语义。durable-job canonical 的空 production registry 是旧 foundation 范围，Phase 5 增量显式扩展为固定 DingTalk kind，并增加独立默认关闭的 unknown-result replay 与 direct-success policies 及 ExecuteSucceeded；两个 policy 均未启用的普通 job 继续保持现有 Verify-first 语义。

文档交付运行 OpenSpec strict validation、diff/引用/patch 检查。实施后运行 `make test build`（含生成），并依赖真实 deploy/acceptance 体系新增本 change 的 PostgreSQL/container 故障与恢复验收；不把文档验证冒充运行验证。
