## Why

这是 Phase 6 OpenSpec Planning Stage 2 的 Node asset lifecycle change。当前 baseline 中
`relay_node_assets` 只有 stable `instance_id`、注册元数据和 capability，监控 activation
只有可关闭的半开区间，Inventory poll run（Node-owned durable work）、快照/当前账号状态、
binding 尚未把 Node retired/replaced 作为持久化 fence。继续使用这些表的“存在即可用”语义会让
旧 Node 在 Retire/Replace 后重新调度、发出 outbound、推进 current truth 或保留 current binding。

本 change 在不进入请求数据面、不修改 Relay Node/CLIProxyAPI 的前提下，冻结 Node
Register/Edit/Retire/Replace 的 durable contract，复用 Gateway Stage 1 已批准的
`asset_admin_command_receipts`、`command_id`、事务 advisory lock、actor-first replay、
canonical intent、revision、signed compatibility manifest、`relay-control-compat-gate`
和 mandatory production wrapper。

## What Changes

- `relay_node_assets` 从 active-only row 演进为 `active -> retired` terminal lifecycle；
  `instance_id` 保持稳定且不可复用，增加 `lifecycle_status`、`revision`、`retired_at`、
  `retired_by`、`retire_reason`。
- 增加不可变 `relay_node_asset_replacements`，支持 A→B→C lineage，拒绝 fork、merge、
  cycle 和 identity reuse。
- 扩展既有 Node monitoring activation persistence：current interval 在同一 boundary
  关闭；future activation 通过 cancellation metadata durable cancel，不删除 schedule；
  `active_range` 对 cancelled row 变为空 range。
- 冻结 Retire/Replace 的 Node→monitoring→binding 锁顺序，扩展 binding
  `end_reason=node_retired|node_replaced`，不自动 rebind 或 account migration。
- 为 Inventory scheduler、claim/preflight、outbound、promotion/current truth、availability、
  request-quality 和 Node-owned `account_inventory_poll_runs` durable work 增加 active Node、
  monitoring eligibility、identity/fencing 校验；retired/replaced work 变为稳定
  abandoned/terminal。generic `async_jobs`（当前唯一注册 job kind 是
  `dingtalk_alert_delivery`，未声明任何 Node-owned identity 字段）execution semantics 保持
  不变，本 change 不修改 `durable-job` capability。
- 冻结 Node canonical intent、脱敏 receipt result、HTTP mutation/read routes、history
  pagination、counts、bounded audit/metrics taxonomy 及 class 2 compatibility floor。

## Capabilities

### Modified Capabilities

- `asset-registry`
- `relay-node-gateway-account-binding`
- `account-inventory-poll-run`
- `account-inventory-snapshot`
- `account-inventory-lifecycle`
- `account-request-quality`
- `antigravity-account-availability`

### New Capabilities

- `relay-node-asset-lifecycle`

## Affected Truth Sources

- OpenAPI：`api/openapi.yaml` 的既有 `/api/assets/nodes` collection/detail 读取约定；
  mutation routes 在 implementation 时加入。
- Database：`migrations/00003_asset_registry_foundation.sql` 的
  `relay_node_assets`、`relay_node_inventory_monitoring_activations`、既有 generated
  `active_range`，`00005` poll runs（Node-owned durable work，扩展 `execution_reason`），
  `00006` snapshot，`00007` lifecycle，`00011` binding。不修改 `00004` durable job foundation
  的 generic `async_jobs`/`async_job_kinds` execution semantics。
- Queries/sqlc/generated clients：`queries/assets.sql`、`queries/account_inventory_poll_runs.sql`
  和 binding queries；只规划生成，不手改 generated files。
- Audit/metrics：复用 immutable `audit_logs` 和 `control_asset_mutation_total`，Node 使用
  bounded `asset_node` taxonomy。
- UI/Runbook：本 change 拥有 Node lifecycle 的读取投影 **和** mutation 控件规划（Register/Edit/
  Retire/Replace/expected_revision 冲突刷新/retired 历史详情/predecessor-successor 导航/仅提示
  secret_configured）；operations 控件（Connection Test、Monitoring Enable/Disable）仍 deferred
  到 `add-relay-node-management-operations`。

## Dependencies and Compatibility

- 先决依赖：已批准的 Gateway Stage 1 change `14c0e80`（shared foundation；其余基线见
  `planning-validation.md`）。
- 本 change 决定 class 0 = pre-Phase6、class 1 = Gateway lifecycle-aware、class 2 =
  Node lifecycle/cancellation-aware；DB evidence floor 从 1 单调提升到 2。
- class 1 runtime 若继续读取本 schema，会把 retired Node、cancelled future activation、
  replacement lineage、new binding end reason 或 old durable work 当作可用，故不能沿用 floor 1。
- 不创建第二套 receipt、intent、lifecycle framework、cancellation table 或 compatibility
  marker；Node 只扩展既有 shared vocabulary。

## Security, Migration and Rollback

所有 mutation 先完成 authenticated active session、`super_admin`、CSRF 和
`Cache-Control: no-store` 边界；不暴露 Management Key、secret reference、credential、
OAuth、raw CLIProxyAPI response 或 credential-bearing header。migration additive-first，
不依赖 destructive down；floor 2 后 class 1 rollback 必须由 wrapper fail closed。

## Non-Goals

- 不实现 `add-relay-node-management-operations` 的 Connection Test、Monitoring Enable、
  Monitoring Disable HTTP/UI contract；该 change skeleton 保持不变。
- 不实现 Relay Scheduler、Node Pool、Account routing、Gateway Account migration、
  CLIProxyAPI account enable/disable/remove、credential editing、scheduled monitoring
  product API、Compose/Kubernetes control 或任意 Phase 7 capability。
- 不改变 Node identity、driver/capability ownership、Gateway/Account/CLIProxyAPI 原生
  truth，也不在 HTTP request data plane 中加入 Control 调度。

## Planning Status

Detailed planning = complete for independent readiness review only.
Implementation readiness = AWAITING REVIEW.
`openspec apply` = NOT AUTHORIZED; Implementation = NOT STARTED; Runtime Acceptance = NOT STARTED.
