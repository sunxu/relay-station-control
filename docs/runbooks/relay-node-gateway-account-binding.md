# Relay Node ↔ Gateway Account Binding Runbook

## 1. 适用范围

本手册覆盖 `relay_node_gateway_account_bindings` 的 bind/rebind/unbind 事务、resolution 语义、故障处置和 Migration 回滚边界。Binding 是 Control 管理的关联元数据，只表达 `Relay Node → Gateway → Gateway Account(accounts.id)` 的当前与历史关系；不进入请求调度路径，不修改 Sub2API 或 CLIProxyAPI 配置。

Gateway Account identity 唯一使用 Directory 中的 `accounts.id`；`name`、`url`、`platform`、`type`、`status` 均不是 identity proof，不能替代 Account ID 参与匹配或冲突解决。

## 2. Cardinality 与 identity

- 第一版 current cardinality 固定为 `Relay Node 0..1 ↔ 0..1 Gateway Account`。
- 一个 Node 同一时刻只能有一个 current binding；一个 Gateway Account 同一时刻只能被一个 Node 绑定。
- identity 由 `relay_node_id` + `gateway_instance_id` + `gateway_account_id` 三元组组成，全部使用稳定 ID，不使用可变展示字段。
- 数据库使用双向 partial unique index `(relay_node_id) WHERE ended_at IS NULL` 与 `(gateway_instance_id, gateway_account_id) WHERE ended_at IS NULL` 强制上述唯一性。

## 3. Bind

`POST /api/relay-bindings/bind`，请求体只接受 `relay_node_id`、`gateway_instance_id`、`gateway_account_id`（`additionalProperties: false`，不接受 name/url/platform/type/status 作为输入）。

同一 PostgreSQL 事务内完成：

1. 锁定 Relay Node 资产身份（`LockRelayNodeAssetForBinding`）。
2. 锁定 Gateway `gateway_directory_current_state`。
3. 锁定 Node 的 current binding（若已绑定，返回 `node_conflict`）。
4. 锁定目标 Account 的 current binding（若已被其它 Node 绑定，返回 `account_conflict`）。
5. 所有必要锁获取后，调用一次 `GetRelayBindingDBTime` 取得 `operation_at`，用于 freshness 校验与本次写入的全部持久时间戳。
6. 要求存在 accepted Directory 且 `operation_at - last_success_received_at <= 540s`（fresh）。
7. 要求 `gateway_account_id` 存在于 current snapshot 的 snapshot items。
8. 插入 open binding：`bound_at = operation_at`、`bind_reason = administrator_bind`、`evidence_snapshot_id` = 实际验证的 current snapshot。
9. 写入 `relay_binding.bind` immutable audit（与 binding 插入同事务提交）。

稳定结果：

| Outcome | HTTP | 含义 |
|---|---|---|
| `success` | 200 | binding 建立 |
| `account_not_found` | 404 | Account ID 不在 current snapshot |
| `directory_stale` | 409 | 最近成功观测超过 540s |
| `directory_unavailable` | 503 | Gateway 已登记但无 accepted Directory |
| `node_not_found` / `gateway_not_found` | 404 | 资产未登记 |
| `node_conflict` | 409 | 目标 Node 已有 current binding |
| `account_conflict` | 409 | 目标 Account 已被其它 Node 绑定 |

任何一步失败，整个事务回滚，不产生部分状态。

## 4. Rebind

`POST /api/relay-bindings/rebind`，请求体为 `relay_node_id`、`new_gateway_instance_id`、`new_gateway_account_id`。

同一事务内 close-old + insert-new，不使用 UPDATE 迁移 identity；锁获取顺序固定为：

1. 锁定 Node 资产身份（`LockRelayNodeAssetForBinding`）；不存在 → `node_not_found`。
2. 锁定新 Gateway 的 `gateway_directory_current_state`（`LockGatewayDirectoryCurrentState`）；Gateway 未登记 → `gateway_not_found`；已登记但无 accepted Directory → `directory_unavailable`。
3. 锁定 Node 的 current binding（`LockCurrentRelayNodeGatewayAccountBindingByNode`）。**`no_current_binding` 在此步骤判定**（Gateway lock 之后，而不是之前），不得隐式退化为 bind。
4. 锁定目标新 Account 的 current binding（`LockCurrentRelayNodeGatewayAccountBindingByAccount`）；已被其它 Node 绑定 → `account_conflict`。
5. 所有必要锁获取后，调用一次 `GetRelayBindingDBTime` 取得 `operation_at`。
6. 用 `operation_at` 校验新 Gateway freshness（`operation_at - last_success_received_at <= 540s`，否则 `directory_stale`）与新 Account 是否存在于 current snapshot（否则 `account_not_found`）。
7. 旧 interval：`ended_at = operation_at`、`ended_by = admin`、`end_reason = administrator_rebind`。
8. 新 interval：`bound_at = operation_at`（与旧 interval 的 `ended_at` 完全相同）、`bind_reason = administrator_rebind`。
9. 写入一条 `relay_binding.rebind` audit。

**不变量**：`old.ended_at == new.bound_at`。事务外不会观察到中间 unbound 或双绑定状态。

冲突/失败结果与 Bind 相同的稳定分类（`no_current_binding`、`account_not_found`、`directory_stale`、`directory_unavailable`、`node_not_found`、`gateway_not_found`、`account_conflict`）。

## 5. Unbind

`POST /api/relay-bindings/unbind`，请求体为 `relay_node_id`。

- **不检查 Directory freshness**：stale 或不可用的 Directory 不能阻止管理员移除 Control 自己的关联元数据。
- 已 unbound → 返回稳定 `already_unbound`（200，幂等 no-op），不写虚假 history 或 audit。
- 有 current binding → 取得一次 `operation_at`（来自 `GetRelayBindingDBTime`，不使用 Go `time.Now()`），以 `end_reason = administrator_unbind` 关闭 interval 并写入 `relay_binding.unbind` audit，同事务提交。

`already_unbound` 响应中的 `operation_at` 同样来自 PostgreSQL DB time；应用层不得伪造该字段。

## 6. Resolution

Resolution 是查询时派生的观察，不是可写状态列：

```text
if no current binding:              unbound
elif no accepted Directory:         unknown
elif stale (> 540s):                unknown
elif account_id in current snapshot: resolved
else:                               unresolved
```

Account context 来源区分：

- **fresh + target present**（`context_source = current`）：使用 fresh current snapshot 中的 Account 内容。
- **stale + target present**（`context_source = last_known`）：使用 stale current snapshot（latest current snapshot，而不是绑定时的 evidence snapshot）中的 Account 内容；明确标注不是 current evidence。
- **stale + target missing（已不在 stale current snapshot 中）**（`context_source = last_known`）：fallback 使用 binding 自身的 `evidence_snapshot_id`/`evidence_account_id` 历史内容。
- **fresh + target missing**（`context_source = none`）：resolution 为 `unresolved`，Node-centric 视图的 `account_context` 为 `nil`，不展示任何历史/current 内容。
- **无 current binding**（`context_source = none`）：resolution 为 `unbound`。
- **无 accepted/current snapshot**（Directory `unavailable`）：可 fallback binding `evidence_snapshot_id`/`evidence_account_id` 作为历史 last_known 内容；若 binding 自身也无 evidence，则 `context_source = none`。

Unresolved 列表（Gateway Account-centric 的“目标已消失”视图）额外携带独立的 `last_known_account_context` 字段，用于展示该已消失 binding 的历史内容，不与主视图的 current context 混淆。

固定行为：

- Account 从 fresh Directory 消失 → binding 保留，resolution 变为 `unresolved`。
- 同一 `accounts.id` 再次出现于 current snapshot → 自动恢复 `resolved`，无需人工干预。
- 新 ID 即使 name/url/platform/type/status 与旧 Account 完全相同，也不会继承旧 binding（保持 unbound）。
- A→B→A snapshot reuse 不改变 binding identity。
- fresh empty Directory 会使所有 current binding 变为 `unresolved`。

## 7. Troubleshooting

| 现象 | 排查 |
|---|---|
| `directory_unavailable` | Gateway 已登记但从未有成功 Directory ingestion run，或 `gateway_directory_current_state` 无 row。检查 Directory ingestion 是否正常运行（参见 gateway-directory-ingestion runbook）。不要绕过验证直接写 binding。 |
| `directory_stale` | 最近成功观测早于 540s 前。等待下一次成功 ingestion，或按 ingestion runbook 排查采集故障。Unbind 不受此影响，可正常执行。 |
| `account_not_found` | 目标 `gateway_account_id` 不在当前 accepted snapshot。确认 Account 是否已从 Gateway 移除或 ID 输入错误；不要用 name/url 猜测替代 ID。 |
| `node_conflict` | 目标 Node 已有 current binding。需要先 rebind 或 unbind 该 Node，而不是重复 bind。 |
| `account_conflict` | 目标 Account 已被其它 Node 绑定。需要先对占用该 Account 的 Node 执行 unbind 或 rebind。 |
| `no_current_binding`（rebind） | Node 当前未绑定。改用 bind，而不是期望 rebind 自动建立新绑定。 |

所有失败结果均不暴露原始 PostgreSQL 错误、Secret、Gateway credential 或 raw Directory response；只返回上表固定错误码。

## 8. Migration 回滚边界

当前 Migration head 为 12（`00012_relay_binding_runtime_lock_privileges.sql`，只授予 `relay_control_runtime` 对 `relay_node_assets.created_at` / `gateway_instances.created_at` 的列级 `UPDATE` 权限，用于满足 PostgreSQL `FOR UPDATE` row-lock 的 ACL 要求；不涉及 schema/数据）。

- **Clean 环境**（未产生任何 binding row，也未产生 `category = 'relay_binding'` 的 audit row）：`12 → 11 → 10` 均可回滚。
- **12 → 11**：仅撤销上述 runtime row-lock 权限授予，不改变 schema/数据，任何环境下技术上都能成功。**但如果已经存在 binding/audit history，不应在生产执行 12 → 11**：撤销该权限会导致 `LockRelayNodeAssetForBinding` / `LockGatewayDirectoryCurrentState`/`LockGatewayDirectoryInstance` 的 `SELECT ... FOR UPDATE` 报 `permission denied`（SQLSTATE `42501`），使 Bind/Rebind/Unbind 全部不可用（读操作不受影响）。
- 若误执行了 12 → 11 导致 binding mutation 不可用，恢复方式是重新执行 `goose up`（migration 12 Up），重新授予该权限，不需要重启应用或数据迁移。
- **11 → 10**：一旦存在任意 binding row（current 或历史 closed interval）或任意 `relay_binding` audit row，Migration Down 必须 fail closed，抛出 SQLSTATE `55000`，拒绝执行。
- **不得**通过删除/清理 binding 或 audit 历史来强行让 11 → 10 rollback 成功。
- 已产生历史的环境中，正常的应用 rollback 流程 = 停止/回退应用 runtime 二进制，保留数据库 schema（维持在 12）与全部历史数据；重新部署新版本后从持久状态继续。不对 schema 执行 12 → 11 → 10。
- Rollout 顺序：先以 Migration owner 执行 Goose up，再发布新应用；发布失败时先回退应用，不回退 schema。

## 9. Non-goals（本 change 不做）

- 不修改 Sub2API Account/Group、用户/API Key 路由或 scheduler state。
- 不修改 CLIProxyAPI credential、provider、account、retry 或 cooldown 配置。
- 不参与请求 routing、scheduling、weights、retry、failover、breaker、cooldown 或 health decision。
- 不实现 duplicate ownership 检测、告警或修复（属于后续独立 change）。
- 不实现 automatic binding、candidate matching 或基于 URL/name/platform 的自动推断/自动 rebind。
- 不直接访问 Gateway PostgreSQL，也不保存 Gateway credential。

## 10. 安全边界

- 写操作（bind/rebind/unbind）只允许已认证、未过期、`super_admin` 角色的会话，并遵守现有 CSRF 与 `Cache-Control: no-store`。
- 读操作（Node-centric / Gateway Account-centric / unresolved 列表）只要求已认证会话，不产生 mutation 或 audit。
- `bound_by`/`ended_by` 与 audit actor 只使用 `control_admin_users.admin_id`；display/login name 不作为 actor identity。
- audit `details` 固定 allowlist：`relay_node_id`、`gateway_instance_id`、`old_gateway_account_id`、`new_gateway_account_id`、`evidence_snapshot_id`、`reason_code`；不允许额外 key，不允许 Secret/raw URL/raw response/credential/display/login name。
- 所有 binding API 响应不返回 Secret reference、token、Gateway credential、raw Directory response 或原始数据库错误。

## 11. 验收命令

```sh
make generate
go test -count=1 -v ./internal/store ./internal/api
go test -race -count=1 -v ./internal/store ./internal/api
go test -count=1 ./...
openspec validate add-relay-node-gateway-account-binding --type change --strict --no-interactive
git diff --check
```

PostgreSQL binding acceptance（`TestRelayBindingHTTPCompleteSuite` 及 `internal/store` 下 binding 相关 integration tests）需要设置 `CONTROL_DATABASE_TEST_URL` 与 `CONTROL_RUNTIME_DATABASE_TEST_URL` 指向专用测试数据库；未设置时用例会 `SKIP`，不构成验收证据。
