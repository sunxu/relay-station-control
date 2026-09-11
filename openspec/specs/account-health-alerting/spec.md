# account-health-alerting Specification

## Purpose
为单环境 Relay Station Control 定义 Antigravity 只读账号健康投影、现有领域事实驱动的 Problems 查询及 DingTalk 持久通知契约，保持 Phase 4 领域恢复、请求数据面及 Phase 6/7 边界独立。

## Requirements

### Requirement: Phase 5 SHALL enforce Scope

系统 SHALL 满足以下冻结契约。

Phase 5 提供：

- Antigravity Account Health
- Token Health projection
- Problem Accounts
- DingTalk Webhook alerting
- ACTIVE / RESOLVED notification lifecycle
- 相应只读 API/UI 与运维验收

第一版 Provider 固定：

`antigravity`

第一版通知渠道固定：

`DingTalk Webhook`

Phase 5 不修改 CLIProxyAPI，不修改 Gateway，不进入账号操作流程。

#### Scenario: 首版范围
- **WHEN** 请求 Phase 5 健康与通知能力
- **THEN** 仅 antigravity 与 DingTalk 生效，数据面不被修改

### Requirement: Phase 5 SHALL enforce Existing truth reused

系统 SHALL 满足以下冻结契约。

Phase 5 MUST 复用现有：

- Account Inventory
- Request Quality
- Antigravity Availability
- Availability occurrences
- Cross-node Duplicate Ownership occurrences
- Relay Node assets
- durable async jobs

不得复制或重新定义这些领域状态。

Phase 4 Availability 六态继续是账号总体可用性的唯一状态模型：

- AVAILABLE
- TOKEN_INVALID
- ACCOUNT_BLOCKED
- FORBIDDEN
- UNKNOWN
- DISABLED

不得增加 HEALTHY / UNHEALTHY / DEGRADED / health_score 等第二套总体健康状态。

#### Scenario: 复用事实
- **WHEN** 同账号存在 Inventory、Quality 和 occurrence
- **THEN** 保留 Availability 六态，不创建另一套总体健康状态

### Requirement: Phase 5 SHALL enforce Token Health

系统 SHALL 满足以下冻结契约。

Token Health 是只读派生状态，不持久化。

状态固定为：

- VALID
- INVALID
- UNKNOWN

优先级：

```text
ACTIVE TOKEN_INVALID occurrence
→ INVALID

否则 Inventory 当前不具备合格证据
→ UNKNOWN

否则 last_refresh_at IS NULL
→ UNKNOWN

否则 last_refresh_at > database_now
→ UNKNOWN

否则 database_now < last_refresh_at + 3599 seconds
→ VALID

否则
→ UNKNOWN
```

`expected_valid_until`：

```text
last_refresh_at + 3599 seconds
```

它只是 Expected Valid Until，不得称为实际 Token expiration。refresh 为空时返回 null；非空时即使为 future、过期或 Token INVALID 仍返回该 expected 时间供诊断。

`last_refresh_at == database_now` 不是 future，合格 Inventory 且无 ACTIVE TOKEN_INVALID 时为 VALID。第一版不引入 clock skew tolerance、可配置 grace 或 future allowance；Node 时钟超前时保守 UNKNOWN。

当：

```text
database_now >= expected_valid_until
```

在没有 ACTIVE TOKEN_INVALID 的分支中，状态为 UNKNOWN，不使用 EXPIRED。

不得再使用“最近 15 分钟成功请求才是 VALID”的规则。

真实成功请求继续只承担 Phase 4 已冻结的 occurrence recovery evidence。

ACTIVE TOKEN_INVALID 的优先级高于新的 `last_refresh_at`：

```text
TOKEN_INVALID ACTIVE
+ fresh last_refresh
→ INVALID
```

只有 Phase 4 合法 recovery 后才能重新计算 VALID / UNKNOWN。

Token Health 不新增：

- Token table
- Token history
- Token checkpoint
- Token occurrence
- Token reconciler
- Google probe
- Token parsing/download

#### Scenario: 时间边界与优先级
- **WHEN** 合格 Inventory 无 ACTIVE TOKEN_INVALID，refresh 距 DB 当前时间为 3598.9 秒或恰好 3599 秒
- **THEN** 分别 VALID、UNKNOWN；TTL 边界使用同一数据库时间

#### Scenario: Future refresh 与当前时间
- **WHEN** 合格 Inventory 无 ACTIVE TOKEN_INVALID，last_refresh_at 分别大于、等于 database_now
- **THEN** 分别 UNKNOWN、VALID；不引入时钟容差，expected_valid_until 仍返回 refresh + 3599 秒

#### Scenario: Invalid 优先于当前 refresh
- **WHEN** 存在 ACTIVE TOKEN_INVALID，且 last_refresh_at 等于 database_now
- **THEN** 返回 INVALID；ACTIVE TOKEN_INVALID 始终最高优先级，不受 refresh、TTL 或 Inventory 资格覆盖

### Requirement: Phase 5 SHALL enforce Inventory qualification

系统 SHALL 满足以下冻结契约。

Token VALID 必须建立在当前合格 Inventory evidence 上。

旧、stale、incomplete、missing、out_of_scope 或不可验证的 Inventory 不得仅凭历史 `last_refresh_at` 判为 VALID。

出现这些情况时：

```text
token_state = UNKNOWN
```

除非存在 ACTIVE TOKEN_INVALID，此时仍为 INVALID。

#### Scenario: 降级证据
- **WHEN** Inventory stale/incomplete/missing/out_of_scope 或当前不可验证，但历史 refresh 仍新鲜
- **THEN** 无 ACTIVE TOKEN_INVALID 时 UNKNOWN；有该 occurrence 时 INVALID

### Requirement: Phase 5 SHALL enforce Problem Accounts

系统 SHALL 满足以下冻结契约。

新增全局只读运维页面：

`Problems`

Problem Accounts 是当前 ACTIVE confirmed problems 的聚合 read model，不是新的领域实体。

一个 Node/account 行进入页面，当且仅当关联以下任一 supported ACTIVE issue；duplicate 还须属于当前领域确认的 affected-node membership：

- TOKEN_INVALID
- ACCOUNT_BLOCKED
- FORBIDDEN
- CROSS_NODE_DUPLICATE_OWNERSHIP

以下状态本身不得使账号进入 Problem Accounts：

- UNKNOWN
- DISABLED
- Token UNKNOWN
- TTL elapsed
- Inventory stale
- missing
- out_of_scope
- Node unreachable
- ordinary request failure

一行 identity：

```text
node_id + account_key
```

同一行允许多个 ACTIVE issues。

当且仅当：

```text
active supported issue count = 0
```

该行退出默认 Problems 页面。

#### Scenario: 多个问题与退出
- **WHEN** 同一 Node/account 同时有 blocked 与 duplicate，先后合法 resolve
- **THEN** 一行包含两个 issue；仅最后一个 supported ACTIVE issue 消失后退出

### Requirement: Phase 5 SHALL enforce Confirmed problem persistence semantics

系统 SHALL 满足以下冻结契约。

Availability 的 TOKEN_INVALID、ACCOUNT_BLOCKED、FORBIDDEN issue 只在对应 occurrence 合法 ACTIVE → RESOLVED 后消失，ACTIVE occurrence 不得被当前 projection 覆盖。

以下变化都不是 Availability recovery evidence，不得移除这些 Availability issues：

- Availability 变 UNKNOWN
- Account DISABLED
- Inventory stale
- Account missing
- Account out_of_scope
- Node RETIRED
- Duplicate evidence degraded
- `last_refresh_at` 变化

对于上述 Availability issues，只有领域 occurrence 自身：

```text
ACTIVE → RESOLVED
```

才能关闭对应 issue。Duplicate issue 的移除遵循下一节 current membership 规则，不要求整体 occurrence 先 RESOLVED。该行所有 supported ACTIVE issues 消失后才退出 Problems。

因此例如：

```text
TOKEN_INVALID ACTIVE
Availability UNKNOWN
Token INVALID
Problem YES
```

是合法且预期的状态。

#### Scenario: 投影不能关闭故障
- **WHEN** ACTIVE Availability issue 遇 UNKNOWN、DISABLED、missing、out_of_scope、Node RETIRED 或 refresh 更新
- **THEN** 保留该 issue；只有对应 Availability occurrence 合法 ACTIVE→RESOLVED 才关闭

### Requirement: Phase 5 SHALL enforce Cross-node duplicate

系统 SHALL 满足以下冻结契约。

Cross-node duplicate 保持现有 occurrence identity。

Node pair / affected Node set 不成为 occurrence identity。

Problems MUST 按 ACTIVE duplicate occurrence 当前领域确认的 affected-node membership 展开 Node 行，共享同一个 occurrence ID。某 Node 经既有 Duplicate Ownership 逻辑确认 `absence_confirmed` 并从 current membership 移除后，该 Node 行的 duplicate issue 立即消失，即使 occurrence 仍 ACTIVE；若无其他 supported ACTIVE issue，该行退出 Problems。

stale、inventory unavailable、unverifiable、evidence degraded、incomplete evidence 均不足以移除 membership。必须复用既有保守 membership 与 occurrence recovery 算法，不新增状态机或表。

不导致 occurrence lifecycle transition 的 membership 变化不产生通知；合法 ACTIVE → RESOLVED 必须产生一次 RESOLVED notification intent（DingTalk 配置启用时），所有该 occurrence 的 duplicate issues 随之消失。

#### Scenario: Duplicate 成员变化
- **WHEN** 同一 ACTIVE duplicate 的 membership 变化未导致 lifecycle transition，或证据降级
- **THEN** 保持 occurrence identity，不新增通知；降级不移除成员、不作为恢复证据

#### Scenario: 三个 Node 逐步确认不再持有账号
- **WHEN** DingTalk 已启用，A/B/C 的 duplicate occurrence 为 ACTIVE，A 经 absence_confirmed 移出后 B/C 仍重复；随后 B 也经 absence_confirmed 移出且满足现有合法 recovery guards
- **THEN** 首次 A 的 duplicate issue 消失、occurrence 仍 ACTIVE、无新通知；随后 occurrence ACTIVE→RESOLVED，B/C duplicate issues 清除且同事务产生一次 RESOLVED intent；有其他 ACTIVE Availability issue 的行继续保留

### Requirement: Phase 5 SHALL enforce Problem Accounts read model

系统 SHALL 满足以下冻结契约。

新增：

```text
POST /api/problem-accounts/query
```

POST 是 query ergonomics 决策，不是 email privacy 决策。

ProblemAccountItem 至少包含：

- instance_id
- node_name
- account_key
- email
- provider
- issues[]
- availability
- token_state
- last_refresh_at
- expected_valid_until
- next_retry_at
- last_success_at
- last_failure_at
- highest_severity
- oldest_active_since

Issue 至少包含：

- occurrence_id
- type
- reason
- severity
- since

排序固定：

```text
severity DESC
oldest_active_since ASC
email ASC
instance_id ASC
```

使用服务端 keyset pagination。

支持最小过滤：

- provider
- node
- severity
- reason
- email

不建立 filter DSL / saved filters。

#### Scenario: 服务端聚合
- **WHEN** 带 provider/node/severity/reason/email 过滤查询跨 Node problems
- **THEN** 服务端聚合、固定排序及 keyset 分页；不做前端 N+1

### Requirement: Phase 5 SHALL enforce Existing Account Quality API

系统 SHALL 满足以下冻结契约。

现有 Node Account Quality DTO 增加：

- token_state
- expected_valid_until

数据库兼容采用新的版本化 query contract，例如：

```text
control_query_node_account_quality_v2
```

Slice B 实施版本说明：当前基线已有 v1/v2/v3，因此本节新版本示例在实际 SQL 中落为 `control_query_node_account_quality_v4`；所有既有 v1/v2/v3 SHALL 保持不变，不新增 HTTP endpoint。

不得破坏既有 v1 contract。

Token projection MUST 在 PostgreSQL query layer 统一计算，使用 PostgreSQL 时间，避免 Go/UI 出现不同判定。

#### Scenario: 兼容查询
- **WHEN** 旧调用方调用 v1、新调用方调用 v2
- **THEN** v1 contract 保持；v2 返回统一 DB Token 投影

### Requirement: Phase 5 SHALL enforce DingTalk notification scope

系统 SHALL 满足以下冻结契约。

第一版只通知 confirmed durable problems：

| Reason | Severity |
| --- | --- |
| TOKEN_INVALID | Critical |
| ACCOUNT_BLOCKED | Critical |
| FORBIDDEN | Warning |
| CROSS_NODE_DUPLICATE_OWNERSHIP | Critical |

不通知：

- UNKNOWN
- DISABLED
- Token UNKNOWN
- TTL elapsed
- Inventory stale
- missing
- Node unreachable
- ordinary request failure
- quota/rate-limit generic issues

一个 occurrence 最多产生：

```text
1 ACTIVE notification intent
1 RESOLVED notification intent
```

同一账号同时存在多个不同 occurrence 时，分别发送。

Problem Accounts 是“一行账号多个 issue”。

DingTalk 是“一条 occurrence 一条 lifecycle notification chain”。

#### Scenario: 独立生命周期
- **WHEN** 持续 ACTIVE、合法 RESOLVED、再次确认 recurrence
- **THEN** 原 occurrence 各至多一个 ACTIVE/RESOLVED intent；recurrence 使用新 occurrence 与新 intent

### Requirement: Phase 5 SHALL enforce DingTalk configuration

系统 SHALL 满足以下冻结契约。

运行时配置：

```text
DINGTALK_WEBHOOK_URL
DINGTALK_SIGNING_SECRET   # optional
```

URL 未配置：

```text
DingTalk disabled
Control 正常启动
不 enqueue notification jobs
不补发过去已经存在的 ACTIVE occurrences
```

配置非法，例如 URL 非 HTTPS：

```text
Control startup failure
```

不提供：

- DingTalk configuration UI
- channel CRUD
- routing rules
- template engine
- test-message API

#### Scenario: 配置关闭与非法配置
- **WHEN** URL 未配置、稍后启用，或配置非 HTTPS URL
- **THEN** 关闭时正常启动且无 job；启用不补发历史 ACTIVE；非法配置启动失败

### Requirement: Phase 5 SHALL enforce Email policy

系统 SHALL 满足以下冻结契约。

系统级策略统一为：

```text
Email = normal business identity
Email is not sensitive within Relay Station
```

完整邮箱允许进入：

- PostgreSQL
- API
- UI
- audit
- structured logs
- durable job payload
- DingTalk

不得再：

- mask email
- HMAC email
- 因查看完整 email 建特殊权限
- 因 email 本身使用特殊 security audit

仍不得使用 email 作为 Prometheus label，原因是高基数。

Canonical security/OpenAPI 文档 SHOULD supersede 旧的“email sensitive”措辞。

历史 archived migrations/specs 无需重写。

#### Scenario: 完整邮箱
- **WHEN** 认证用户查看账号或发送该 occurrence 通知
- **THEN** 完整邮箱出现在 UI/API/通知中，不 mask/HMAC；不用于 Prometheus label

### Requirement: Phase 5 SHALL enforce DingTalk transport

系统 SHALL 满足以下冻结契约。

DingTalk 是独立的 external notification egress。

与 Phase 4 internal management transport 明确分离。

固定：

```text
HTTPS only
direct connection
proxy disabled
environment proxy ignored
redirect disabled
```

不得读取：

- HTTP_PROXY
- HTTPS_PROXY
- ALL_PROXY

不得复用 Gateway/CLIProxyAPI management client。

不需要 DingTalk host allowlist / CIDR allowlist / DNS pinning。

Webhook URL 来自受控部署 Secret，不接受 API/UI 动态 URL。

#### Scenario: 直连隔离
- **WHEN** 环境配置 HTTP_PROXY/HTTPS_PROXY/ALL_PROXY 或服务返回 redirect
- **THEN** DingTalk 仍直接 HTTPS，redirect 拒绝且不跟随

### Requirement: Phase 5 SHALL enforce Secret boundary

系统 SHALL 满足以下冻结契约。

以下均视为 Secret：

- DingTalk Webhook URL
- DingTalk signing secret
- CLIProxyAPI management key
- access token
- refresh token
- auth file

不得进入：

- DB
- async job payload
- API
- UI
- audit detail
- logs
- metrics
- trace

DingTalk URL 本身可能携带 access token，因此日志绝不能输出 URL/query string。

#### Scenario: Secret 不落盘
- **WHEN** 成功、失败、重试或 trace 含网络错误
- **THEN** DB/job/API/UI/audit/log/metrics/trace 均无 URL、query token、signing secret 或上游凭据

### Requirement: Phase 5 SHALL enforce DingTalk durable job

系统 SHALL 满足以下冻结契约。

复用现有 durable-job framework。

新增固定 job kind：

```text
dingtalk_alert_delivery
payload_schema_version = 1
```

固定业务契约：

```text
default_timeout_seconds = 10
max_attempts = 5
replay_safe = true
allow_unknown_effect_replay = true
allow_direct_success = true
rollback_allowed = false
```

DingTalk delivery 语义：

```text
logical enqueue = idempotent
external delivery = at-least-once
duplicate delivery = accepted
```

`replay_safe=true` 的业务含义是：

重复发送同一 occurrence transition 的通知是允许的安全副作用；它不授权 unknown-result replay。该授权仅来自独立 job-kind execution policy `allow_unknown_effect_replay`，默认 false，Phase 5 仅 dingtalk_alert_delivery 启用。不得按 job kind 名称硬编码分支或把 unknown 伪装为 effect_not_applied。

HTTP timeout、write 后 connection reset 或 running lease 过期导致外部结果未知时，启用该 policy 且剩余 Execute budget > 0 才能经现有恢复/退避机制进入 retry_wait，后续正常 claim 获得新 execution lease/fencing token 后重放。保留 job_id、operation_id、payload、payload hash、idempotency key；总 Execute attempts <= 5，耗尽 failed。未启用 policy 的所有 job 保持 Verify-first。该 boolean 在 enqueue 时由 async_job_kinds definition/catalog 快照到 async_jobs，Worker/Reconciler 必须依据持久化 job policy 且先通过 registry/catalog/job consistency check；重复 enqueue 也比较该字段。allow_unknown_effect_replay=true REQUIRES replay_safe=true，非法组合拒绝注册/持久化；job snapshot 与当前 registry 不匹配时 fail closed，不继承新权限。完整增量见 [durable-job spec](../durable-job/spec.md)。

DingTalk HTTP success 与 business response success 同时满足时 SHALL 返回通用 ExecuteSucceeded；仅在 persisted allow_direct_success=true、policy compatibility 通过且 running lease/fencing 有效、DB lock 下无取消请求时，Worker 用既有 StatusSucceeded/EventSucceeded 直接 running→succeeded（event actor=worker），不进入 verifying。allow_direct_success 默认 false，仅 DingTalk 启用；沿用两表 BOOLEAN NOT NULL DEFAULT FALSE、enqueue per-job snapshot 与全部兼容性检查，不由 registry 动态改变旧 job 权限。普通 job 未授权却返回 ExecuteSucceeded MUST fail closed。 commit 前取消已可见则 running→failed / cancel_after_effect_applied、actor=worker，不自动 rollback。当前 no-effect 且无 unresolved unknown 的并发取消可经窄 running→cancelled 安全完成；unknown 证据与 Verify 消解规则遵循 durable-job delta，不由 policy boolean 推断。

allow_direct_success 只授权已知成功，allow_unknown_effect_replay 只授权未知效果有界重放，互不替代、互不依赖。不新增 direct⇒replay_safe invariant，保留 unknown⇒replay_safe。明确临时失败且证明无效果继续既有 retry disposition；unknown 不伪装为成功或 effect_not_applied。普通 job 两个 policy 默认 false，保留 Verify-first。详见 durable-job delta。

DingTalk executor 不进入：

- verifying
- rollback
- rolled_back

投递主要路径（取消额外遵循 durable-job delta 的安全证据矩阵）：

```text
pending
→ running
→ succeeded

or

running
→ retry_wait
→ running

or

running
→ failed
```

#### Scenario: 有限重试
- **WHEN** 临时失败或重启后执行 delivery
- **THEN** 复用 durable job，最多五次；不进入 verifying/rollback

### Requirement: Phase 5 SHALL enforce Transaction boundary

系统 SHALL 满足以下冻结契约。

Occurrence transition 和 notification intent MUST 在同一个 PostgreSQL transaction 中提交。

推荐流程：

```text
BEGIN

reconcile occurrence lifecycle
→ obtain lifecycle transitions

for each transition:
    jobs.EnqueueTx(...)

COMMIT
```

如果 DingTalk disabled：

```text
reconcile occurrence
不 enqueue
COMMIT
```

不得：

```text
先 commit occurrence
再异步扫描 occurrence 创建 job
```

因为会产生永久漏通知窗口。

复用现有 `jobs.EnqueueTx`，不得手写第二套：

- async_jobs INSERT logic
- job event logic
- wake outbox logic
- payload hash logic

故障注入 MUST 证明：

```text
occurrence + job + event + outbox
全部提交
或
全部回滚
```

#### Scenario: 同事务故障注入
- **WHEN** 在 occurrence、job、event、wake outbox 写入之间注入失败，或正常提交
- **THEN** 失败全部回滚，正常全部提交；不得留下已提交 occurrence 却漏 intent

### Requirement: Phase 5 SHALL enforce Lifecycle transition DB contract

系统 SHALL 满足以下冻结契约。

Phase 5 SHOULD 用 additive reconciliation contract 暴露本次 transaction 内的 occurrence lifecycle transitions 给 Go。

不得破坏已有 v1 contract。

Availability 与 Duplicate Ownership 都采用相同模式：

```text
domain reconciliation
→ transition records
→ jobs.EnqueueTx in same transaction
```

现有 logger observer 可以继续作为 observability，但不得作为 notification delivery truth。

#### Scenario: 并发 transition
- **WHEN** 多个 reconcile 并发处理相同证据或事务重试
- **THEN** 只返回实际提交的 lifecycle transition，并在同一事务 EnqueueTx；v1 保持兼容

### Requirement: Phase 5 SHALL enforce DingTalk job idempotency

系统 SHALL 满足以下冻结契约。

固定逻辑 key：

```text
dingtalk:availability:<occurrence_id>:active
dingtalk:availability:<occurrence_id>:resolved

dingtalk:duplicate:<occurrence_id>:active
dingtalk:duplicate:<occurrence_id>:resolved
```

operation_id SHALL 为 UUIDv5 / SHA-1 name-based UUID。namespace UUID 固定 `94db90f6-d7e6-4cce-a045-890b63171d86`；name MUST 是完整 frozen idempotency_key 的 exact UTF-8 bytes。Go 对应：

```go
uuid.NewSHA1(
    uuid.MustParse("94db90f6-d7e6-4cce-a045-890b63171d86"),
    []byte(idempotencyKey),
)
```

不得加入随机 salt、时间、node name 或 attempt number。同 key 必须得到同 operation_id；ACTIVE/RESOLVED 或不同 occurrence 的 key 必须产生不同 operation_id。

稳定性范围 SHALL 是同一 **committed logical occurrence transition** 或 **same-key durable replay**：idempotency_key、operation_id、canonical payload、payload_hash 全部不变。已完全回滚的 SERIALIZABLE/deadlock attempt 没有 durable transition，不要求与下一成功 attempt 保持 occurrence_id、timestamp、payload、operation_id 相同。未知 commit outcome 不等于确定 rollback，不得绕过既有幂等兼容检查。

四种 notification transition SHALL 均以 priority=50 enqueue；severity、transition、availability/duplicate 领域或 retry path 不得动态改变 priority。继续复用 jobs.EnqueueTx、Registry.ValidateAndHash 与 existing durable-job framework，不新增 identity service、operation/dedupe/notification table、第二 outbox、workflow engine 或 notification framework。

不需要：

- notification_sent
- delivery_version
- dedupe table
- last_notified_at

#### Scenario: 幂等 intent
- **WHEN** 同一 occurrence transition 被重复处理
- **THEN** 固定 idempotency key 只创建一个逻辑 job，不建 dedupe 表

#### Scenario: Stable operation identity and fixed priority
- **WHEN** 同一 committed transition 重放，或分别构造 ACTIVE/RESOLVED 与不同 occurrence 的通知
- **THEN** 同 key 使用相同 UUIDv5 operation_id 与 canonical payload/hash，不同 key 的 operation_id 不同；全部 priority=50

#### Scenario: Rolled-back transaction retry
- **WHEN** SERIALIZABLE/deadlock attempt 完全回滚后重新执行领域事务
- **THEN** 不要求保留未提交 transition 的 identity/time/payload；一旦存在 committed transition 或 same-key durable replay，则必须保持上述稳定性

### Requirement: Phase 5 SHALL enforce Notification payload

系统 SHALL 满足以下冻结契约。

Notification payload 是 transition-time display snapshot。

至少包含：

- occurrence_id
- occurrence_type
- transition
- reason
- severity
- environment_id
- environment_name
- account_key
- email
- provider
- instance_ids
- node_names
- started_at
- transitioned_at

started_at 与 transitioned_at SHALL 使用 authoritative domain timestamp，编码严格为 `t.UTC().Format(time.RFC3339Nano)`：UTC only、canonical RFC3339Nano、Z offset。Validator MUST 拒绝任何与 parse 后 canonical re-format 不一致的 wire representation，包括语义等价的 `+08:00`、`+00:00` 或非 canonical 小数秒；不得使用 enqueue-time time.Now() 构造业务时间。发送时生成的 signing timestamp 仅属 transport material，不写入 durable payload。

instance_ids 与 node_names SHALL 是 positional parallel arrays，`len(instance_ids) == len(node_names)`。instance_ids MUST 为 canonical UUID、ascending、unique；node_names[i] 属于 instance_ids[i]，保持对应 ID 排序，允许重复且 MUST NOT 独立 lexical sort。字符串仍须有界。现有 FieldStringArray 的 sorted/unique 默认契约保持不变；后续实现可用最小 additive primitive / explicit field policy 表达此 ordered strings collection，不新增 schema framework 或 canonicalizer。

既有 issue/reason/severity mapping SHALL 严格一致（这是既有契约的重申）：

| occurrence_type | reason | severity |
|---|---|---|
| TOKEN_INVALID | token_invalid | Critical |
| ACCOUNT_BLOCKED | account_blocked | Critical |
| FORBIDDEN | forbidden | Warning |
| CROSS_NODE_DUPLICATE_OWNERSHIP | cross_node_duplicate_ownership | Critical |

任何 tuple mismatch MUST 被拒绝为 jobs.ErrInvalidPayload，不发送语义冲突消息。

不得包含动态诊断信息：

- token_state
- last_refresh
- expected_valid_until
- Request Quality evidence
- raw failure evidence
- credentials

资产 display name 在 ACTIVE 和 RESOLVED 间发生 rename 时，允许两条消息显示不同当前名称。

稳定 correlation 唯一依赖：

```text
occurrence_id
```

不为 display snapshot 建历史表。

#### Scenario: 显示快照
- **WHEN** ACTIVE 后资产改名再 RESOLVED
- **THEN** 每条消息使用各自 transition-time display snapshot，以 occurrence_id 关联且无动态诊断/Secret

#### Scenario: Canonical timestamp encoding
- **WHEN** 输入 `2026-09-11T09:00:00+08:00`，或 canonical 值 `2026-09-11T01:00:00Z`
- **THEN** 前者即使语义等价仍拒绝，后者允许；业务时间不由 enqueue clock 重新生成

#### Scenario: Parallel node display snapshot
- **WHEN** canonical UUID A<B，两个 Node 均名为 Relay，或 A→Zulu、B→Alpha
- **THEN** 分别接受 `[A,B]/["Relay","Relay"]` 与 `[A,B]/["Zulu","Alpha"]`；保留重复名称及 positional pairing，不独立排序名称；`[A,B]/["A"]` 拒绝

#### Scenario: Legacy array and issue tuple compatibility
- **WHEN** 旧 FieldStringArray schema 收到重复值，或 notification tuple 的 reason/severity 与 occurrence_type 不一致
- **THEN** 旧数组仍拒绝重复值；不合法 tuple 返回 jobs.ErrInvalidPayload，包括 TOKEN_INVALID/forbidden、TOKEN_INVALID/Warning、FORBIDDEN/token_invalid、FORBIDDEN/Critical、ACCOUNT_BLOCKED/Warning、duplicate/Warning

### Requirement: Phase 5 SHALL enforce DingTalk message

系统 SHALL 满足以下冻结契约。

ACTIVE 示例：

```text
[Critical] TOKEN_INVALID

Account: user@example.com
Provider: antigravity
Node: node-free-001
Since: ...
Occurrence ID: ...
```

RESOLVED：

```text
[Resolved] TOKEN_INVALID

Account: user@example.com
Provider: antigravity
Node: node-free-001
Started: ...
Resolved: ...
Occurrence ID: ...
```

Duplicate 显示 affected Node names。

不发送：

- raw request
- raw response
- auth file
- token
- root-cause guess
- operator guess
- resolution reason guess

#### Scenario: 消息内容
- **WHEN** 发送 ACTIVE 或 RESOLVED duplicate
- **THEN** 完整账号及 affected Node names 与 occurrence ID 可定位，消息不推测恢复原因

### Requirement: Phase 5 SHALL enforce DingTalk HTTP behavior

系统 SHALL 满足以下冻结契约。

HTTP total timeout：

```text
5 seconds
```

Retry：

- DNS failure
- connection failure
- TLS failure
- timeout / ambiguous result
- HTTP 408
- HTTP 429
- HTTP 5xx
- 明确临时 DingTalk failure

Permanent failure：

- malformed configuration
- signing configuration failure
- HTTP 400
- HTTP 401
- HTTP 403
- HTTP 404
- redirect
- 明确 permanent rejection
- invalid/unrecognized response

HTTP 200 本身不等于成功。

必须同时确认 DingTalk business response 表示成功。

Raw response 只在内存解析，不持久化、不记录日志。

#### Scenario: HTTP 分类
- **WHEN** 分别返回 500、429、403、redirect、200 非成功业务结果或不可识别响应
- **THEN** 500/429 重试，403/redirect/无效响应永久失败；HTTP 200 单独不算成功，HTTP 总超时 5 秒

### Requirement: Phase 5 SHALL enforce At-least-once contract

系统 SHALL 满足以下冻结契约。

允许：

```text
POST 实际成功
→ worker 未能提交 succeeded
→ process crash
→ replay
→ DingTalk 收到重复消息
```

这是 PASS 行为。

不得为了避免重复引入：

- distributed exactly-once
- external delivery ledger
- DingTalk verification protocol
- new notification state machine

#### Scenario: 成功后崩溃
- **WHEN** 外部 POST 成功后未提交 succeeded 即进程退出
- **THEN** 租约恢复后可重放，重复通知是允许结果，不引入 exactly-once

### Requirement: Phase 5 SHALL enforce Persistence

系统 SHALL 满足以下冻结契约。

Phase 5 不新增任何新的领域表。

明确不新增：

- Token table
- Token history
- Problem Accounts table
- Notification table
- Notification outbox
- DingTalk config table
- Alert delivery table

新增数据库对象限于：

**Read models**

- Node Account Quality v2 query
- Problem Accounts v1 query
- 必要 ACL

**Delivery integration**

- `dingtalk_alert_delivery` job-kind registration
- lifecycle transition-returning reconciliation contracts
- 既有 durable-job execution policy / registration / recovery contracts 的最小 additive 扩展（allow_unknown_effect_replay 默认关闭；不新增表或状态）

索引只允许在实际 query-plan / acceptance 证明需要时新增。

#### Scenario: 无新表
- **WHEN** 实施 read model 和通知集成
- **THEN** 只添加列明的 query/ACL/registration/reconciliation 与 execution-policy contracts；无新领域表和通知 outbox

### Requirement: Phase 5 SHALL enforce Migration strategy

系统 SHALL 满足以下冻结契约。

所有变更 forward-only additive。

不得修改历史 Phase 4 migrations。

拆成两个 concern：

```text
Phase 5 read-model migration
Phase 5 DingTalk delivery migration
```

Read-model migration：

- Account Quality v2
- Problem Accounts query
- ACL

Delivery migration：

- job-kind registration 与默认关闭的 unknown-result replay policy 所需既有 contract 扩展
- Availability transition contract
- Duplicate transition contract

#### Scenario: 前向升级
- **WHEN** 从当前已部署 schema 升级
- **THEN** 新增 migration 保留历史文件及 v1；read 与 delivery concern 可独立核验

### Requirement: Phase 5 SHALL enforce Authorization

系统 SHALL 满足以下冻结契约。

Phase 5 不新增角色。

继续使用现有：

```text
super_admin
```

Problem Accounts、Token Health、occurrence details 都是 authenticated read-only。

不新增：

- viewer
- alert_admin
- notification_admin
- operator RBAC

Phase 7 出现账号 mutation 时再单独评审 mutation authorization。

#### Scenario: 认证与权限
- **WHEN** 匿名、无权限或现有 super_admin 查询
- **THEN** 分别沿用 401/403 与认证只读访问，无新角色

### Requirement: Phase 5 SHALL enforce Database least privilege

系统 SHALL 满足以下冻结契约。

新 query functions 继续采用既有模式：

```text
SECURITY DEFINER
fixed search_path = pg_catalog
owner = relay_control_migrator

REVOKE EXECUTE FROM PUBLIC
GRANT EXECUTE TO relay_control_runtime
```

runtime 不获得新的底表直接权限。

#### Scenario: 数据库 ACL
- **WHEN** runtime 调用 query 或直接读取底表
- **THEN** 仅受控函数允许，底表权限拒绝；PUBLIC 无 EXECUTE

### Requirement: Phase 5 SHALL enforce Observability

系统 SHALL 满足以下冻结契约。

Phase 5 不建设新的 monitoring platform。

不新增账号级 Prometheus metrics。

第一版不要求专用 DingTalk metrics。

复用已有 durable-job metrics 和 Jobs UI。

DingTalk final failure 通过：

- Jobs 页面
- structured ERROR log

发现。

Problem Accounts 是账号故障的主要 operational view。

Jobs 是通知 delivery 排障入口。

DingTalk 不进入 Control `/healthz`。

DingTalk failure 不得使 Control unhealthy。

#### Scenario: 通知失败与健康
- **WHEN** DingTalk 最终失败
- **THEN** Jobs 与 ERROR log 可见；occurrence 不变且 Control healthz 不受影响

### Requirement: Phase 5 SHALL enforce Structured logs

系统 SHALL 满足以下冻结契约。

DingTalk executor 仅记录有界结构化 lifecycle：

Success：

```text
component=dingtalk
action=deliver
result=success
job_id
occurrence_id
transition
attempt
```

Retry：

```text
result=retry
error_code
```

Final failure：

```text
result=failure
error_code
```

不得记录 Webhook URL、secret、request body 或 raw response。

#### Scenario: 有界日志
- **WHEN** delivery 成功、重试、最终失败
- **THEN** 按固定生命周期字段记录且失败有 error_code，无 request body/raw response/URL

### Requirement: Phase 5 SHALL enforce Runtime Acceptance

系统 SHALL 满足以下冻结契约。

Phase 5 MUST 至少验证：

1. `last_refresh + 3598.x sec` → VALID
2. `last_refresh + 3599 sec` → UNKNOWN
3. ACTIVE TOKEN_INVALID + fresh refresh → INVALID
4. Node restart / no last_refresh → UNKNOWN
5. stale Inventory + prior Availability problem / duplicate membership → remains
6. DISABLED + ACTIVE problem → problem remains
7. missing/out_of_scope + ACTIVE Availability problem → remains；duplicate 按既有领域确认的 current membership 展开
8. duplicate evidence degraded → problem remains
9. full email displayed in UI
10. full email delivered to DingTalk
11. Webhook unconfigured → no job, Control healthy
12. ACTIVE occurrence → exactly one logical ACTIVE job
13. continuous ACTIVE → no new job
14. RESOLVED → exactly one logical recovery job
15. recurrence → new occurrence + new job
16. multiple issues → independent jobs
17. HTTP 500 → retry
18. HTTP 429 → retry
19. HTTP 403 → failed
20. 5s ambiguous timeout → replay allowed
21. max attempts 5 → failed
22. delivery failure does not modify occurrence
23. Control restart resumes pending/retry jobs
24. external proxy environment present → DingTalk still direct
25. redirect → rejected
26. Secret absent from DB/API/log/trace
27. occurrence+job transaction injected rollback → neither persists
28. normal commit → occurrence/job/event/outbox all persist
29. delivery success followed by process crash → replay/duplicate accepted within remaining Execute budget
30. qualified Inventory + future last_refresh_at → UNKNOWN
31. qualified Inventory + last_refresh_at == database_now → VALID；同时存在 ACTIVE TOKEN_INVALID → INVALID
32. A/B/C duplicate → A absence_confirmed：A duplicate issue 消失、B/C 保留、occurrence ACTIVE、无新通知；B 再 absence_confirmed 且满足 recovery guards：合法 RESOLVED、清除剩余 duplicate issues、一次 RESOLVED intent
33. same unknown result（HTTP timeout / write 后 connection reset / running lease expired）：DingTalk policy=true → 有预算时 retry_wait/replay；普通 job policy=false/未配置 → 不直接 replay、Verify-first
34. unknown replay 保留 job_id/operation_id/payload/hash/idempotency key/backoff，新 claim 使用新有效 lease/fencing；第五次后 failed，旧 worker 不得提交
35. policy 默认 false、仅 DingTalk 注册启用；不扩大 replay_safe，不按 kind 名硬编码，不伪造 effect_not_applied，不引入新状态/queue/outbox/retry engine
36. replay_safe=false + allow_unknown_effect_replay=true → Go Registry / DB catalog 与 job persistence / catalog compatibility validation 均拒绝；DingTalk true/true 合法
37. enqueue 将 catalog policy 快照到 job；同 key 重入队比较此字段；Registry/Catalog 与 DB catalog 不匹配、existing job snapshot != current registry policy → fail closed / policy mismatch，Worker/Reconciler 不按新权限执行旧 job，不覆盖 snapshot

38. allow_direct_success 默认 false、仅 DingTalk true；两表/Definition/CatalogEntry/Job/DB mapping/EnqueueTx snapshot 与所有 compatibility 检查一致，旧 job 不继承 registry 新授权
39. HTTP+business 明确成功→ExecuteSucceeded；有 direct 持久化授权且有效 lease/fencing、锁内无取消时 worker running→succeeded 并原子写既有 EventSucceeded；普通 job 未授权返回相同结果→fail closed，DB 同样拒绝
40. 旧/过期 Worker 即使持有明确成功结果也不能提交 succeeded/event
41. known DingTalk success 且无取消→direct succeeded；unknown DingTalk→unknown-effect replay；普通 unknown/ExecuteNeedsVerification→Verify-first，三条路径严格分离
42. direct=true/unknown=false/replay_safe=false 与 direct=false/unknown=true/replay_safe=true 不因额外 invariant 拒绝；两 policy 独立且 unknown⇒replay_safe 仍强制

#### Scenario: 完整运行时验收
- **WHEN** Phase 5 准备声明验收通过
- **THEN** 逐项执行本节 42 项并留证，不把文档冻结当运行时 PASS

### Requirement: Phase 5 SHALL enforce Explicit non-goals

系统 SHALL 满足以下冻结契约。

Phase 5 MUST NOT implement:

- CLIProxyAPI code changes
- Gateway code changes
- Account Disable / Enable / Remove
- auth-file upload
- account repair
- account move
- Node management
- Gateway management
- automated remediation
- Google token probe
- Token vault
- health score
- notification platform
- notification channel CRUD
- routing DSL
- escalation
- ACK / silence
- PagerDuty
- Alertmanager
- Grafana dashboard
- capacity intelligence
- workflow engine

These belong to later phases or remain deferred.

#### Scenario: 越界请求
- **WHEN** 尝试启用账号 mutation、通道 CRUD 或自动修复
- **THEN** Phase 5 不提供该能力

### Requirement: Phase 5 SHALL enforce Phase boundary

系统 SHALL 满足以下冻结契约。

Phase 5:

```text
observe
derive
surface
alert
```

Phase 6:

```text
manage Gateway / Relay Node assets
```

Phase 7:

```text
explicit account operations
Disable / Enable / Remove / repaired-auth upload / Verify
```

Phase 5 MUST remain read-only with respect to Gateway, Relay Node and CLIProxyAPI account state.

#### Scenario: 阶段隔离
- **WHEN** 部署 Phase 5
- **THEN** Gateway/Relay Node/CLIProxyAPI 账号状态保持只读，Phase 6/7 未被提前实现

### Requirement: Queries SHALL preserve bounded and honest reads

查询 SHALL 沿用 super_admin、no-store、默认 25/最大 100 的有界分页及绑定过滤条件的 cursor；无效或错配 cursor 为 400，数据库不可用为 503，不得返回假空页或假 UNKNOWN。排序 severity 使用 Critical 高于 Warning 的语义顺序，不按字符串逆序；冻结四项排序键保持不变。实施 SHALL 验证同邮箱/Node 的排序键唯一性；若实际身份模型允许碰撞，须先更新契约评审，不得静默漏行。DTO 的 instance_id 对应领域 node_id。

#### Scenario: Invalid cursor and unavailable database
- **WHEN** cursor 与过滤不匹配或数据库读取失败
- **THEN** 分别返回 400/503，不伪造业务状态

#### Scenario: Detail and time display
- **WHEN** 用户在 Problems 点击账号查看详情
- **THEN** 复用现有 Topology/Account Quality/Inventory 详情与 occurrence history；DB 以 UTC 判定，UI 沿用系统时区，不新建 Problem Detail 平台
