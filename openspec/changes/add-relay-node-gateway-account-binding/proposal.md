# add-relay-node-gateway-account-binding Proposal

## Why

Control 已经能够从唯一 Gateway ingest 严格验证的 API Account Directory，并保存 current snapshot 与 freshness，但仍缺少 Relay Node 与 Gateway Account 的显式关联真相。Phase 4 需要先冻结 `Relay Node → Gateway → Gateway Account(accounts.id)` binding，后续 topology 展示才能基于稳定身份，而不是根据名称、URL 或部署约定猜测。

## What Changes

- 新增 Control 管理的 Relay Node ↔ Gateway Account binding 能力。
- 第一版 current cardinality 固定为严格 `0..1 ↔ 0..1`：一个 Relay Node 最多绑定一个 Gateway Account，一个 Gateway Account 最多绑定一个 Relay Node。
- Gateway Account identity 只使用 `(gateway_instance_id, accounts.id)`；`name/url/platform/type/status` 只作展示和人工识别。
- bind/rebind 必须以 PostgreSQL DB time 检查 current accepted Directory freshness，并确认目标 `accounts.id` 存在于 current snapshot。
- binding 使用可关闭的时间区间保存历史；unbind/rebind 不物理删除历史，不增加独立 binding 状态机。
- binding resolution 仅由 current binding、current accepted Directory snapshot 和 freshness 派生为 `unbound/resolved/unresolved/unknown`。
- Account 从 fresh Directory 消失时保留 binding 并显示 unresolved；Directory stale 或尚无成功 snapshot 时显示 unknown。
- rebind、unbind 和冲突写入保持事务原子，并复用现有管理员认证与不可变审计机制。
- binding 只用于 Control 侧关联、查询、诊断和后续派生 topology，不修改 Gateway、Sub2API、CLIProxyAPI 或请求调度。

## Capabilities

### New Capabilities

- `relay-node-gateway-account-binding`: 定义 Relay Node 与 Gateway Directory Account 的显式一对一 binding、生命周期、验证、派生 resolution、查询、安全和并发边界。

### Modified Capabilities

- None. Directory ingestion、资产注册、Sub2API 与 CLIProxyAPI 的既有契约保持不变。

## Impact

- **仓库**：后续实现仅修改 `control`。
- **Persistence**：后续采用最小 additive binding schema，复用 `relay_node_assets`、`gateway_instances`、Directory snapshot/current-state 和现有 `audit_logs`。
- **OpenAPI/UI**：本设计不冻结页面或复杂管理框架；后续最小管理入口必须遵守既有 `super_admin`、CSRF、no-store 与脱敏边界。
- **Gateway/Node**：不修改 Sub2API Account/Group，不修改 CLIProxyAPI credential/provider/account，不访问 Gateway PostgreSQL。
- **数据面**：不进入请求路径，不实现 routing、scheduler、weights、retry、failover 或自动 rebinding。
- **后续 change**：duplicate ownership detection 明确留给独立 change。
