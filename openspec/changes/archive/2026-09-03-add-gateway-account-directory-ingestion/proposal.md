# add-gateway-account-directory-ingestion Proposal

## Why

Gateway 已冻结 API Account Directory contract，但 Control 仍缺少把该只读目录稳定 ingested、校验、落库并按数据库时间计算 freshness 的控制面能力。Phase 4 需要先把这个 ingestion 真相源固定下来，后续 binding 和派生关系才能建立在已验证的 Directory current state 上。

## What Changes

- 新增 Control 侧 `gateway-account-directory-ingestion` 能力，按每个 Gateway 180 秒轮询一次 Directory。
- 每次轮询都持久记录 ingestion result；normalized content 相对 current 变化时按 fingerprint create-or-reuse immutable content snapshot，未变化时只刷新成功观察时间。
- 将 current snapshot pointer 与 last successful observation 分离，freshness 只看 Control DB 的 `last_success_received_at`。
- 严格 whole-response validation：contract 外额外字段、schema、type、id、order、required field、duplicate id、unsafe non-null URL、hard limit、source-time sanity 任一失败都 fail closed。
- 冻结 HTTP fetch contract、epoch-aligned slot、attempt/lease/timeout 预算、generated_at 边界、fingerprint 字段集和 SecretResolver 取 token 语义。
- 保持 read-only：Control 只读取 Gateway Directory，不修改 Gateway Account/Group/scheduler，也不把 raw response、service token 或 Gateway DB credential 写入持久层。
- 复用既有 `gateway_instances.reader_secret_ref` 作为唯一 Secret 引用来源，不新增 Directory 专用 Secret 表或字段。
- 保持 binding、duplicate ownership、Gateway/CLIProxyAPI、routing/scheduling 全部 out of scope。

## Capabilities

### New Capabilities

- `gateway-account-directory-ingestion`: 定义 Gateway Directory 的轮询、严格验证、content-addressed snapshot、freshness、恢复、并发与审计边界。

### Modified Capabilities

- None. This change adds a new Control capability and does not alter the frozen Gateway Directory contract.

## Impact

- **仓库**：只修改 `control`。
- **OpenAPI/UI**：不新增产品 API 或页面。
- **Migration/sqlc**：后续优先使用最小 additive Migration + sqlc/store queries 完成 run / snapshot / current-state 持久化；仅在跨表原子 invariant 确实需要时增加最小 DB function。
- **Metrics/audit/logging**：后续需要低基数状态指标和脱敏失败审计，但不得暴露 raw response、Secret 或凭证。
- **Runbook**：后续需要补充 ingestion 启停、恢复和故障处置说明。
- **安全/兼容性**：变更必须 fail closed，且不得影响 Gateway、CLIProxyAPI 或数据面请求路径。
