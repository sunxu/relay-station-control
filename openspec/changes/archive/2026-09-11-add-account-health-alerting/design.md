# Design

## Context
Requirements freeze implementation baseline：Control `f4173242aef83afd95d3240573c68eb4299987a2`；前置 Ops 路线规划基线：`a4f01f80c588f32a51625315f09d252045652edc`。这些是需求冻结前的既有基线，不是本 change 的最终提交或 Architecture Review 通过证据。

Architecture Review target：评审时两个仓库 main 上当前已提交的 Phase 5 requirement documents。历史批准的 exact reviewed repository SHAs 已记录于独立的 [Architecture Review evidence](./planning-validation.md)；本 change 不硬编码自身最终 commit SHA，amend 不要求更新自引用 SHA。

Detailed Requirements = FROZEN；Architecture Review = PASS；Implementation readiness = READY；Implementation = COMPLETE；Runtime Acceptance = 86/86 PASS。Runtime candidate pin=`a91117b5ffd9a3e8ed8c92080d97759efd80a235`；P0=0，Product P1=0；real DingTalk logical messages=4，additional sends=0。

现有实现接入点：

| Concern | 真实路径 / contract | Phase 5 接入方式 |
|---|---|---|
| Node Account Quality | `internal/store/node_account_quality.go` | 新 additive read contract；实施基线已有 v1/v2/v3，使用下一版本 v4 并保留旧版本 |
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
新增 `POST /api/problem-accounts/query`，返回 items/next_cursor。Node Account Quality DTO 添加 token_state、expected_valid_until；DB 新 Quality read contract 在 Slice B 实施基线中使用 `control_query_node_account_quality_v4`（v2/v3 已由 Phase 4 占用），保留 v1/v2/v3 签名与行为；这只是落实新版本命名，不增加 HTTP endpoint。新 Problems v1 query 属于后续 Slice C，函数签名在实现时与 sqlc 同步。

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

Slice E implementation-level registration decision：用户本轮明确批准 LeaseDuration=30s、HeartbeatInterval=5s、MaxVerifyAttempts=1，后者仅满足 Registry 最小合法值，不建立 Verify/receipt polling 协议。这些数值不是先前 Architecture Review 的历史冻结值；Architecture Review 保持 PASS。实施顺序调整为真实 Executor foundation → review/commit → Slice D transaction integration → 后续运行验收，未新增 enqueue-only registry exception。官方协议依据与验证见 [Slice E Executor evidence](./slice-e-executor-validation.md)。

Phase 5 增加两个独立默认 false 的 job-kind execution policies：`allow_unknown_effect_replay` 与 `allow_direct_success`，仅 DingTalk 启用；不扩大 replay_safe，不使用 kind-name 条件硬编码。Worker Execute 的 timeout/write 后 reset 等未知结果，以及 Reconciler 接管 running expired lease，以真实未知结果为证据，并依据持久化 allow_unknown_effect_replay、剩余预算与锁内取消/期限检查决定 retry_wait，记录 effect_unknown_unverified；policy=true 本身不是 unknown evidence；普通 job 保持 Verify-first，unknown 不伪装为 effect_not_applied。复用原 durable backoff 和有界扫描，不新建状态、retry engine、queue/outbox。恢复更新仍须取得有效恢复 lease/fencing；后续 Execute 必须正常 claim 新 execution lease/token。job_id、operation_id、payload/hash、idempotency key 均不变，Execute 最多五次、耗尽 failed；已有取消请求或终止期限仍阻止重放，未知效果不得伪装为安全取消。完整行为以本 change 的 durable-job 增量 spec 为准。

### Direct-success amendment
Slice A preflight 确认现有 ExecuteDisposition 无成功结果，Worker、DB lifecycle/event/fenced-transition 仅经 verifying 完成 succeeded；本节冻结的最小扩展已正式 re-review PASS，证据见 planning-validation.md；该历史 PASS 保留；cancellation-race amendment 也已正式 re-review PASS，Stage 1 完成后仅恢复 Slice A。

新增通用 `ExecuteSucceeded`，仅表示 Executor 从本次同步 Execute 获得足以确认操作成功的结果，且 job kind 被显式授权跳过 Verify。DingTalk HTTP 与 business response 均成功才返回此结果；未知结果、可重试无效果、needs verification 均不能冒充成功。

只有经过 policy compatibility 校验、persisted job.allow_direct_success=true 且当前 running lease/fencing 有效、DB lock 下没有取消请求时，Worker 才允许 running→succeeded，复用 StatusSucceeded/EventSucceeded；event 为 from_status=running、to_status=succeeded、actor=worker。后续 forward migration 必须同步 lifecycle guard、event constraint 与 fenced-transition contract，并在 DB 检查持久化授权。旧/过期 Worker 即使持有成功响应也不能提交。普通 job 未授权却返回 ExecuteSucceeded 必须 fail closed，不能全局开放直接成功。

allow_direct_success 控制已知成功是否跳过 Verify；allow_unknown_effect_replay 只控制未知效果能否有界重放，两者互不替代或依赖。不新增 allow_direct_success⇒replay_safe invariant；保留 unknown replay⇒replay_safe。两个默认 false 的普通 job 继续 ExecuteNeedsVerification→verifying→Verify→succeeded/retry/rollback/failed。已知 DingTalk 成功、未知 DingTalk 结果、普通 job unknown 必须分别覆盖验收。

此前 direct-success amendment 的最大增量为一个 Execute disposition 加一个默认关闭的 persisted boolean；不新增状态、事件类型、execution mode enum、policy table/DSL、verification receipt、delivery ledger 或通知状态机。

### Cancellation-race amendment
既有 running→retry_wait 在 claim 后收到 cancellation 时缺少确定性收敛：Worker 排除 cancel_requested 行，Reconciler 不扫描 retry_wait，最终仅等待 deadline/max-attempt sweep。本 amendment 已正式批准，Stage 1 不实施代码，Stage 2 按本契约修订 Slice A；此前 Slice A 测试证据不代表新契约已实现。

效果证据复用 immutable async_job_events.reason_code，固定由 framework 生成，Executor 不得自由提供：

| reason_code | 事实与范围 |
|---|---|
| effect_unknown_unverified | Worker ExecuteResultUnknown 或 Reconciler expired-running recovery，经授权进入 retry_wait，外部效果未知且未 Verify |
| execute_retryable_no_effect | 当前 ExecuteRetryableNoEffect 只证明本次 attempt 无效果 |
| effect_absent_verified | VerifyEffectAbsent 导致 verifying→retry_wait，证明稳定 operation 无效果，消解此前 unknown |

按同 job 的既有 event sequence，只有最新 effect_absent_verified 之后的 effect_unknown_unverified 才是 unresolved unknown；无 verified marker 时考虑全部。当前 unknown transition 在事件落盘前也须参与锁内判断。当前 no-effect 不清除以前 unknown；历史 unknown 被 Verify 消解后不永久 sticky。该语义明确 supersede 未提交 Slice A 的 capability/evidence 混淆及“历史曾 unknown”判断。

control_request_async_job_cancel() 与 control_transition_async_job_fenced() 必须依据这些真实证据而非 policy boolean 判断。DB lock 下重新检查 cancellation、lease/fencing、预算与 deadline，并与事件原子提交：

- A：有效 Worker running + ExecuteRetryableNoEffect + cancel 已可见 + 无 prior unresolved unknown → running→cancelled，EventCancelled、actor=worker、error_code=cancel_verified_safe、release lease=true，reason=execute_retryable_no_effect。
- B：当前 unknown + replay 已授权 + 并发 cancel → failed / cancel_after_unknown_effect，不得安全取消。
- C：此前 unknown→retry_wait，后续 attempt no-effect，再取消（或取消在其 commit 前可见）→ failed / cancel_after_unknown_effect。
- D：unknown→verifying→VerifyEffectAbsent→retry_wait 记录 effect_absent_verified，此后无新 unknown 的取消 → cancelled / cancel_verified_safe。
- E：普通 retry_wait 无 unresolved unknown 的取消 → 保持既有 cancelled。
- ExecuteSucceeded + fenced commit 前 cancel 已可见 → running→failed / cancel_after_effect_applied、actor=worker，不得 succeeded；不引入 direct-success 自动 rollback 或 running→rolling_back，即使 generic kind 允许 rollback，未来需要时另开 change。
- ExecuteNeedsVerification + cancel 仍 running→verifying，保留请求并使用既有 Verify cancellation outcomes；ExecutePermanentFailure 保持 fail-closed，不意味着 no-effect。

running→cancelled 不是通用取消捷径：DB 必须同时验证当前有效 lease/fence、cancel_requested_at 非空、明确当前 known-no-effect framework proof、无 unresolved prior unknown。旧 fence、仅 policy=true/false 或无 proof 均不足以授权。普通 Verify-first、direct/unknown 两 policy 独立性、unknown⇒replay_safe、有限重试及终态保护保持。

本次最大增量只有一个窄 existing-state transition 加固定 reason semantics；不新增状态、event type、policy、列、表、ledger、queue、cancellation worker 或 retry engine。

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

Invariant：`allow_unknown_effect_replay=true` REQUIRES `replay_safe=true`。Go Registry validation、DB catalog/schema constraint 或等价持久化校验（catalog 与 job snapshot 均覆盖）、catalog compatibility validation MUST 拒绝 false/true 非法组合。DingTalk 固定 true/true；普通 job 保持 unknown replay=false，Verify-first 不变。此处冻结的是架构契约，需求冻结阶段未编写 migration/SQL；已有 Slice A 工作树保持不变，Stage 1 完成后按已批准 cancellation amendment 恢复。后续验收必须覆盖非法组合 registration/persistence rejected，以及旧 job snapshot 与新 registry 不匹配时 Worker/Recovery/重复 enqueue 均 fail closed。

不新增 execution policy table、policy DSL、notification table/outbox、delivery ledger、workflow engine、job state、DingTalk 专用 retry engine 或 RBAC；不按 job kind 名硬编码行为。

### Delivery payload and transport
payload 是 spec 列明的 transition-time display snapshot；完整 email 允许，Secret 和动态 Token/Quality 诊断不得加入。重试使用原快照。ACTIVE/RESOLVED 之间 rename 允许名称不同；不保存 display history。复用 lease/fencing/backoff/restart recovery；投递失败不修改 occurrence。

### Notification Identity / Payload Canonicalization amendment

原因：Slice D current-baseline preflight 在 transactional EnqueueTx 实施前发现 deterministic identity 与 payload representation 缺口。本节是 narrow architecture amendment，待独立 re-review；保留既有 Architecture Review PASS 历史，不将本次文档记录视为 amendment approval。Slice E implementation 暂停，已有未提交代码不因此完成或自动符合新契约。

- 固定四种 notification idempotency keys 保持 spec 原样。operation_id 使用 UUIDv5 / SHA-1 name-based UUID，namespace 固定 `94db90f6-d7e6-4cce-a045-890b63171d86`，name 为完整 idempotency_key 的 exact UTF-8 bytes：`uuid.NewSHA1(uuid.MustParse("94db90f6-d7e6-4cce-a045-890b63171d86"), []byte(idempotencyKey))`。不加入 salt、时间、名称或 attempt；不建立 identity service。
- 同一已提交 logical occurrence transition 或 same-key durable replay 的 key、operation_id、canonical payload/hash 必须不变。完全回滚的 SERIALIZABLE/deadlock attempt 未产生 durable transition，不要求下一成功 attempt 复用其 occurrence_id、timestamp、payload 或 operation_id。未知 commit 结果不得直接假定已回滚并重建不同 snapshot 绕过既有幂等校验。
- started_at / transitioned_at 来自 authoritative domain timestamps，wire encoding 为 `t.UTC().Format(time.RFC3339Nano)`：UTC、Z offset、canonical RFC3339Nano。Validator 必须精确校验 parse 后重新格式化与原文相等，拒绝等价但非 canonical 表示；不以 enqueue-time time.Now() 替代。发送时 signing timestamp 仍只是 transport material。
- instance_ids / node_names 是等长 positional parallel arrays。按 canonical UUID ascending、unique 的 instance_ids 排序整个 pair，node_names[i] 属于 instance_ids[i]；名称允许重复，不独立排序。`[A,B] / ["Relay","Relay"]` 与 A→Zulu、B→Alpha 对应的 `[A,B] / ["Zulu","Alpha"]` 均合法；不同长度拒绝。此明确契约取代先前实现讨论中的 independent display collections 解释，不修改当前暂停的实现。
- 现有 FieldStringArray 保持 sorted/unique 默认语义。后续只允许最小 additive primitive 或 explicit field policy 表达 ordered bounded strings、duplicates allowed；继续 Registry.ValidateAndHash，不建立第二套 canonicalizer/schema framework。
- 四种通知 transition 的 enqueue priority 固定 50，不依赖 severity、ACTIVE/RESOLVED、领域或 retry path。
- 既有 issue tuple 不变：TOKEN_INVALID/token_invalid/Critical；ACCOUNT_BLOCKED/account_blocked/Critical；FORBIDDEN/forbidden/Warning；CROSS_NODE_DUPLICATE_OWNERSHIP/cross_node_duplicate_ownership/Critical。mismatch 必须 ErrInvalidPayload。

继续复用 jobs.EnqueueTx 与既有 durable jobs；不新增 notification/dedupe/operation table、identity service、第二 outbox、workflow engine、notification framework、状态或事件类型。实施验收要求见 [active spec](./specs/account-health-alerting/spec.md)，本次不勾 implementation tasks。

独立标准 HTTPS client，不复用 internal management client；显式关闭 proxy（忽略 HTTP_PROXY/HTTPS_PROXY/ALL_PROXY），拒绝所有 redirects，total timeout 5s。仅从受控部署配置读取 DINGTALK_WEBHOOK_URL 和可选 DINGTALK_SIGNING_SECRET，不提供 API/UI 动态 URL、allowlist/CIDR/DNS pinning。URL 未配置正常启动；非法配置启动失败。发送时按 DingTalk signing contract 在内存构造签名，URL/query/原始响应不落日志或 DB。

DNS/connect/TLS/timeout/ambiguous、408/429/5xx、明确临时业务失败重试；配置/签名错误、400/401/403/404、redirect、明确永久拒绝、invalid/unrecognized response 永久失败。200 必须同时具有成功业务响应。只解析有界响应于内存，使用固定安全 error_code，不透传客户端错误中的 URL。总尝试最多五次，因此 at-least-once 表示接受未知结果重放和重复副作用，不承诺永久故障下最终必达。

POST 成功后未提交 succeeded 即 crash 可造成重复通知，这是通过行为；不加 delivery ledger、sent 标记、verify 协议或 exactly-once。最终失败通过现有 Jobs UI 与 structured ERROR log 发现，DingTalk 不进入 /healthz。

### Security and explicit supersession
用户冻结策略优先于仓库旧措辞：邮箱在整个 Relay Station 是普通非敏感业务身份；可按批准契约完整进入 PostgreSQL/authenticated API/UI/audit/controlled structured logs/job payload/DingTalk，不 mask、不为业务展示使用 HMAC、不新增邮箱权限或特殊安全审计。此覆盖不限于 Problems 页面。Prometheus/Alertmanager labels 仍禁用 raw email/account_key；确需指标稳定身份时使用环境隔离的不可逆 HMAC account_id，不限制 DingTalk message body。

现行 `AGENTS.md`、`openspec/config.yaml`、Inventory readonly 与相关 canonical specs、OpenAPI 注释、Ops system design / executive overview 已统一上述邮箱策略；各既有 endpoint 的最小日志/审计白名单保留，但不再作为全局邮箱敏感分类。历史 archive/evidence 不改写。Secret（Webhook URL/query、signing secret、management key、access token、refresh token、auth file）仍禁止进入 DB/job/API/UI/audit/log/metric/trace，原始响应仍禁止保存。

新查询使用 SECURITY DEFINER、fixed search_path=pg_catalog、owner relay_control_migrator、REVOKE EXECUTE FROM PUBLIC、GRANT EXECUTE TO relay_control_runtime；runtime 无新增底表直接权限。继续唯一 super_admin 与既有会话/请求防护；不新增 RBAC。

## Migration, rollout and rollback
Stage 1 不修改 migration。Architecture re-review PASS 后，因为 00028_durable_job_execution_policies.sql 当前未 commit/release/deploy，应直接修订既有 00028 使其符合最终契约，不为本 amendment 新建 00029；只有届时 00028 已正式提交成为不可修改 baseline 才使用下一个合法 forward migration。

实施使用下一个可用 forward Goose migration，read-model 与 delivery concern 分开；不修改任何历史 migration，不新增领域表/Token表/通知表/outbox/config表。后续实施仅增加 read functions/ACL、transition-returning reconciliation contracts、job-kind registration 及既有 execution-policy/recovery contracts 的必要 additive 扩展，两个 policy 在 async_job_kinds 的布尔定义及 async_jobs 的 enqueue snapshot 持久化语义已在上节冻结；后续实施按该契约编写 additive migration，需求冻结阶段未修改 migration 或 SQL/schema；索引仅在实际 EXPLAIN/acceptance 证明需要时增加。

先升级 additive schema，核验 runtime ACL 与旧 v1，然后部署匹配 registry/worker 和 API/UI；DingTalk 初始关闭，完成隔离验收后按部署授权配置 Secret。发布 pin 与 phase_4_closed 不因文档冻结而变更。

回滚停止新 enqueue 与 delivery worker，保留 forward schema、occurrences/jobs/events/outbox。仅取消 URL 配置不应被假定为安全处理已有 queued jobs 的完整回滚方案；必须验证 worker 暂停和恢复路径。旧 binary 可能不识别新 kind，先完成兼容性/停机排空决策再回滚，不删 registry 或未处理证据，不执行 production destructive down。重新启用处理既有 durable jobs，仍不扫描补发从未 enqueue 的 ACTIVE。

## Validation and reconciliation
spec 的全部 42 项 runtime acceptance 是未来实施门槛，tasks 记录未完成状态。重点验证时间边界、退化证据、并发/同事务故障注入、commit ambiguity、restart/lease replay、HTTP/业务响应分类、第五次失败终态、proxy 环境与 redirect、Secret-negative、完整邮箱、ACL/no-store/auth、API pagination、UI 复用。

Canonical Availability 的 `Active runtime after previously confirmed forbidden` scenario 已统一到当前 success-only recovery requirement；历史 archive 中的 two-observation 语义保持原样，不恢复旧 SQL 语义。历史 durable-job foundation evidence 保留当时 empty production registry 的状态；当前 Control composition 显式加入已审 fixed `dingtalk_alert_delivery` definition/executor，并通过 active Phase 5 delta 增加两个独立、默认关闭且按 job 持久化快照的 unknown-result replay/direct-success policies 及 ExecuteSucceeded。generic `jobs.NewProductionRegistry()` 本身仍为空；普通 job 保持既有 Verify-first 语义。

文档交付运行 OpenSpec strict validation、diff/引用/patch 检查。实施后运行 `make test build`（含生成），并依赖真实 deploy/acceptance 体系新增本 change 的 PostgreSQL/container 故障与恢复验收；不把文档验证冒充运行验证。
