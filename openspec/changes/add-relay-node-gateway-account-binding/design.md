# add-relay-node-gateway-account-binding Design

## Context

System Design v1.8 R4.3/R4.4/R4.7 与 ADR-0002 已冻结以下边界：

- Relay Node 身份是 `relay_node_assets.instance_id`。
- Gateway Account 身份只由 Directory 中的 `accounts.id` 决定，并限定在对应 Gateway 下。
- 第一版 current binding 是严格 `Relay Node 0..1 ↔ 0..1 Gateway Account`。
- Directory current snapshot 是 Account 可绑定性的唯一 Source of Truth。
- binding 只存在于 Control，不写回 Gateway，不进入 Sub2API 或 CLIProxyAPI 调度。
- binding resolution 是派生观察，不是管理员可写状态。
- duplicate ownership 不使用 binding 或 Directory evidence，本 change 不实现 duplicate detection。

当前 Control 为单环境、至多一个 `gateway_instances` row，但 binding identity 仍显式包含 `gateway_instance_id`，避免把 Account ID 误当成跨 Gateway 全局身份。Directory 已提供 immutable snapshot、snapshot items、current state 和 540 秒 freshness 语义。

## Goals

- 建立最小、显式、可审计的 Node ↔ Gateway Account current binding。
- 保存完整关系历史，同时避免独立 binding 状态机和额外 history framework。
- bind/rebind 只接受 current、fresh、已验证 Directory 中存在的 Account ID。
- 在 Account 消失、Directory stale、A→B→A 和并发写入下保持 identity 与 resolution 一致。
- 提供 Node-centric 与 Account-centric 的最小查询模型。

## Non-Goals

- 不实现 routing、request scheduling、weights、retry、failover、breaker、cooldown 或 health decision。
- 不修改 Sub2API Account、Group、Account–Group membership、用户/API Key 路由或 scheduler state。
- 不修改 CLIProxyAPI credential、provider、account、retry 或 cooldown。
- 不实现 automatic binding、candidate matching、URL/name/platform 推断或 automatic rebinding。
- 不实现 duplicate ownership detection、告警或修复；该能力属于下一 change。
- 不建设 Cluster、Node Pool、Relay Scheduler、Redis truth 或 process-local binding truth。
- 不直接访问 Gateway PostgreSQL，也不保存 Gateway credential。
- 本轮只产出 OpenSpec，不实现 Migration、业务代码、OpenAPI 或 UI。

## Identity and Cardinality

current binding identity 由以下三项组成：

```text
relay_node_id
gateway_instance_id
gateway_account_id
```

其中：

- `relay_node_id` 必须引用 `relay_node_assets.instance_id`。
- `gateway_instance_id` 必须引用 `gateway_instances.instance_id`。
- `gateway_account_id` 必须是正整数，并引用创建 binding 时 current accepted Directory snapshot 中的 `accounts.id`。

第一版 current cardinality 固定为：

```text
Relay Node:      0 or 1 current binding
Gateway Account: 0 or 1 current binding
```

因此不允许一个 Account 同时绑定多个 Node，也不允许一个 Node 同时绑定多个 Gateway Account。未来任何 1:N 或 N:M 需求必须通过独立 OpenSpec 重新评审，不能通过放松索引或复用 Group 语义偷偷引入。

`name`、Node/Gateway display name、`platform`、`type`、`url`、IP、port 和 `status` 均不是 identity proof。上述字段变化不触发 rebind；新 `accounts.id` 即使其它字段完全相同，也不能继承旧 binding。

## Persistence Model

后续 Migration 优先新增一张 temporal binding 表，不新增独立 current/history 双表：

```text
relay_node_gateway_account_bindings
  binding_id
  relay_node_id
  gateway_instance_id
  gateway_account_id
  evidence_snapshot_id
  bound_at
  bound_by
  bind_reason
  ended_at nullable
  ended_by nullable
  end_reason nullable
```

当前 binding 由 `ended_at IS NULL` 表达；历史 binding 是已关闭区间。表中不增加 `enabled/disabled/resolved/unresolved/unknown` 状态列。

约束方向：

- partial unique `(relay_node_id) WHERE ended_at IS NULL`
- partial unique `(gateway_instance_id, gateway_account_id) WHERE ended_at IS NULL`
- FK `relay_node_id → relay_node_assets.instance_id`，`ON DELETE RESTRICT`
- FK `gateway_instance_id → gateway_instances.instance_id`，`ON DELETE RESTRICT`
- composite FK `(evidence_snapshot_id, gateway_account_id) → gateway_directory_snapshot_items(snapshot_id, account_id)`
- composite FK `(evidence_snapshot_id, gateway_instance_id) → gateway_directory_snapshots(snapshot_id, gateway_instance_id)`
- FK `bound_by → control_admin_users.admin_id`，`ON UPDATE RESTRICT ON DELETE RESTRICT`
- FK `ended_by → control_admin_users.admin_id`，`ON UPDATE RESTRICT ON DELETE RESTRICT`
- `gateway_account_id > 0`
- `ended_at >= bound_at`
- current row 的 end metadata 全部为 NULL；closed row 的 end metadata 全部非 NULL
- `bind_reason IN ('administrator_bind', 'administrator_rebind')`
- `end_reason IN ('administrator_unbind', 'administrator_rebind')`

`bound_by` 与 `ended_by` 只保存稳定 `control_admin_users.admin_id`。display name、login name、email 或其它可变管理员属性不得作为 actor identity 或 FK。

后续 Migration 必须用 CHECK constraints 固定 reason values。操作映射固定为：

- bind：新 interval `bind_reason = administrator_bind`
- rebind：旧 interval `end_reason = administrator_rebind`
- rebind：新 interval `bind_reason = administrator_rebind`
- unbind：旧 interval `end_reason = administrator_unbind`

audit `details.reason_code` 必须复用对应 interval 的固定 reason code，不创建独立 reason taxonomy。

`evidence_snapshot_id` 固定创建或 rebind 当时的验证证据，不随 Directory current pointer 更新。该引用使被 binding history 使用的 snapshot 不能被删除。current resolution 始终读取最新 current snapshot，而不是旧 evidence snapshot。

每次 bind/rebind/unbind transaction 必须在事务开始后通过 `GetRelayBindingDBTime` 执行一次 `clock_timestamp()`，并将结果作为该操作唯一的 `operation_at` 传给后续 insert/close query。service 不得使用 Go `time.Now()` 生成持久时间；rebind 关闭旧 interval 的 `ended_at` 与创建新 interval 的 `bound_at` 必须使用完全相同的 `operation_at`。

绑定历史本身保留任意时间点的关系；安全审计复用现有 immutable `audit_logs`，不再创建第二张专用 audit/history 表。

### Database immutability

temporal binding 必须由数据库 guard 冻结：

- `binding_id`、`relay_node_id`、`gateway_instance_id`、`gateway_account_id`、`evidence_snapshot_id`、`bound_at`、`bound_by` 和 `bind_reason` 创建后不可修改。
- open interval 只允许一次 `ended_at NULL → non-NULL`，且同一 UPDATE 必须原子写入 non-NULL `ended_by/end_reason`。
- 不允许只写部分 end metadata，不允许把 `ended_at`、`ended_by` 或 `end_reason` 改回 NULL。
- closed interval 完全 immutable。
- rebind target identity 不得通过 UPDATE 实现，只能 close-old + insert-new。
- DELETE 与 TRUNCATE 必须由数据库拒绝。

后续 Migration 可以使用最小 trigger/guard 实现上述跨列转换约束；不得依赖 service 层约定替代数据库保护。

## Lifecycle

### Bind

对当前 unbound Node 和 unbound Gateway Account 插入一条 open binding interval。绑定必须保存实名 operator、`bind_reason = administrator_bind`、数据库时间和 current Directory evidence snapshot。

### Rebind

修改 target identity 不允许原地 UPDATE。rebind 在一个数据库事务内：

1. 锁定 Node 的 current binding 与 Directory current state。
2. 验证新 Account 可绑定且未被其它 Node current binding 占用。
3. 以同一个 DB time 关闭旧 interval，`end_reason = administrator_rebind`。
4. 插入 `bind_reason = administrator_rebind` 的新 open interval。
5. 写入一条现有 immutable audit log。

事务外不得观察到中间 unbound 或双绑定。

### Unbind / Delete

产品语义中的 delete 是 unbind：以 `end_reason = administrator_unbind` 关闭 current interval并写审计，不物理删除 row。重复 unbind 返回稳定 no-op 或 already-unbound 结果，不伪造新的历史 interval。

### Disable

binding 不增加独立 disable 状态。Gateway Account 的持久 `status=disabled` 仍属于 Directory member，因此不改变 identity 或 resolution；Node monitoring inactive、暂时不可达或运行停止也不自动修改 binding。

当前资产模型没有 Node retirement 产品操作。本 change 选择 fail closed：存在 current 或历史 binding 引用时，FK `RESTRICT` 阻止物理删除 Node。未来引入 Node retirement 时，必须先显式 unbind，再以保留资产 identity 的 soft retirement 表达退役；不得物理删除被历史引用的 Node，也不得级联删除 binding 历史。

## Write Validation

bind 与 rebind 必须在同一短 PostgreSQL transaction 中使用 DB time完成：

1. 读取并锁定目标 Gateway 的 `gateway_directory_current_state`。
2. 要求存在成功 accepted Directory。
3. 要求 `db_now - last_success_received_at <= 540s`。
4. 要求 `gateway_account_id` 存在于 `current_snapshot_id` 对应的 snapshot items。
5. 要求 Node 与 Gateway 均是当前 Control 数据库中已登记资产。
6. 依靠锁与 partial unique constraints 原子建立新 current binding。

没有 accepted Directory、Directory stale、目标 Account 不在 current snapshot、Node/Gateway 不存在或并发冲突时，写操作 fail closed。unbind 不依赖 Directory freshness，因为 stale 或 missing Directory 不能阻止管理员移除 Control 自己的关联元数据。

不允许使用 name、URL、platform、type、status 或旧 snapshot 中的相似字段替代 Account ID 验证。

## Directory Changes and Resolution

Binding Resolution 不持久化为管理员可写 truth，按查询时 PostgreSQL DB time派生：

```text
if no current binding:
    unbound
elif no accepted Directory:
    unknown
elif db_now - last_success_received_at > 540s:
    unknown
elif gateway_account_id exists in current snapshot:
    resolved
else:
    unresolved
```

- Account 从 fresh Directory 消失时，binding 保留并显示 `unresolved`。
- stale、failed、contract-invalid 或 unavailable Directory 不删除 binding，resolution 为 `unknown`，不能误报 unresolved。
- fresh empty Directory 会使所有 current bindings 为 `unresolved`。
- 同一 `accounts.id` 再次出现时自动恢复 `resolved`。
- 新 ID 即使 name/url/platform/type/status 与旧 Account 相同也保持 unbound。
- A→B→A snapshot reuse 不改变 binding identity；只要相同 ID 在 current A snapshot 中再次出现，resolution 自动恢复。

若未来为性能物化 resolution，必须能从 current binding、Directory current snapshot 和 freshness 完整重算；本 change 默认查询派生，不增加校正 worker。

## Read Model

Control 至少提供两种 bounded query：

1. **Node-centric**：给定 Relay Node，返回 current binding（若存在）、Gateway identity、Gateway Account ID、Account context、resolution、Directory freshness 和 last success time。
2. **Account-centric**：给定 Gateway，从 current Directory 列出 Account，并返回每个 Account 当前绑定的 Node（若存在）；也能查询绑定目标已不在 current Directory 的 unresolved rows。

read model 必须区分：

- `unbound`：没有 current binding；
- `resolved`：fresh Directory 中存在 target ID；
- `unresolved`：fresh Directory 中不存在 target ID；
- `unknown`：没有 accepted Directory 或 Directory stale。

Account context 语义固定为：

- fresh 且 target ID present：resolution 为 `resolved`，可展示 current Directory context。
- fresh 且 target ID missing：resolution 为 `unresolved`，不得把 historical evidence 当成 current evidence。
- stale 或没有 accepted Directory：resolution 为 `unknown`。
- stale 时可以展示 last-known Account context，但必须明确其不是 current evidence。
- API/UI 必须能区分 current context 与 last-known context；具体字段名留给 implementation design。

## Security and Audit

- 写操作只允许既有实名、未过期的 `super_admin` 会话，并遵守现有 CSRF/no-store 边界。
- bind/unbind/rebind 必须与 immutable `audit_logs` insert 同事务提交。
- `bound_by/ended_by` 与 audit actor 只使用 `control_admin_users.admin_id`；display/login name 不作为 actor identity。
- 后续 Migration 只 additive 扩展既有 `audit_logs` category/action CHECK：
  - `category = relay_binding`
  - `action ∈ {relay_binding.bind, relay_binding.unbind, relay_binding.rebind}`
- audit details 只允许以下固定 keys：
  - `relay_node_id`
  - `gateway_instance_id`
  - `old_gateway_account_id`
  - `new_gateway_account_id`
  - `evidence_snapshot_id`
  - `reason_code`
- details 不允许额外 key；不适用的 old/new Account ID 使用 JSON null，不伪造 identity。
- `details.reason_code` 必须复用对应 `bind_reason/end_reason`，不允许独立 audit reason taxonomy。
- audit、错误、日志和指标不得包含 Secret reference、token、Gateway credential、raw Directory response、raw URL、display/login name 或任意原始错误。
- 管理端提交 name/url/platform 等候选字段不得参与查找或冲突解决。
- Control 只读自己的 Directory snapshot；任何 binding 操作都不得连接 Gateway PostgreSQL 或调用 Gateway mutation API。

## Concurrency

- 同一 Node 的 bind/rebind/unbind 必须串行化。
- 同一 Gateway Account 的并发 bind 由 transaction ordering 和 partial unique constraint 保证最多一个成功。
- rebind 的 close-old + insert-new + audit 必须原子提交。
- 不允许先在 Go 中读取可用性后无条件写入。
- 冲突写入返回稳定 conflict，不自动抢占、解绑或覆盖已有 binding。
- Directory current pointer 在验证期间必须保持事务一致；bind 必须引用实际验证过的 current snapshot。

## Rollout / Rollback

- 后续实现保持 additive：先增加 binding schema、约束和审计 shape，再增加 store/service/read model。
- feature 尚未使用（未产生任何 binding rows 且未产生 `relay_binding` audit rows）时，允许执行 Migration 11 → 10 rollback。
- 一旦产生任何 binding history 或 `relay_binding` audit records，数据库 Migration Down 必须 fail closed 并抛出 SQLSTATE `55000` 拒绝 rollback；不得通过删除/清理这些 rows 来强制 rollback。
- 在已产生 history 的环境下，rollback 只能停止/回退应用 runtime，保留 schema 与历史数据。
- rollback 前必须阻止新 binding 写入；已存在 history 不得被静默删除。
- binding 能力不可用时不影响 Directory ingestion、Gateway、Relay Node 或请求数据面。

## Risks

- **非 identity 字段被误用**：所有写接口只接受显式 Account ID，并以 current snapshot items 验证。
- **stale evidence 建立错误 binding**：使用 DB time和 540 秒阈值在同一 transaction fail closed。
- **并发双绑定**：使用 row lock、短事务和双向 partial unique constraints。
- **Account 消失导致历史丢失**：binding interval 保留，resolution 派生为 unresolved。
- **审计与关系不同步**：binding mutation 与 immutable audit insert 同事务。
- **误入数据面**：binding 不写 Gateway/Node，不参与 routing/scheduler/health/duplicate decision。
