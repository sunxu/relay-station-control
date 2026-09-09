# Proposal

## Phase and outcome
Phase 4 可用性证据修复。修正 Antigravity 账号故障恢复，使 `TOKEN_INVALID`、`ACCOUNT_BLOCKED`、`FORBIDDEN` 只有真实成功请求才能恢复，避免 `file_active` 误报恢复。

## Scope
仅修改 Control 的可用性判定、恢复水位与回归验收；复用现有 Inventory、Request Quality、PostgreSQL occurrence/checkpoint。

## Non-goals
不修改 CLIProxyAPI、collector、usage queue、事件 schema、Gateway、Binding、Duplicate Ownership、Token 读取/刷新、Google 探测、自动处置、通知、Prometheus/Grafana 或历史归档内容。

## Compatibility and safety
保留六态、原故障确认、严格 success watermark、UNKNOWN 永不告警及现有 occurrence identity。预计使用 additive migration 更新 reconcile/read function，不修改已部署 migration 00026；保留 `consecutive_healthy_sources` legacy 列但取消其恢复语义。应用 binary/image rollback 与数据库语义 rollback 独立：旧 binary MAY 作为应用部署回滚，但不会恢复旧 availability recovery SQL semantics。本 change 是 forward-only correctness fix；production 正常流程不恢复旧 unsafe `file_active` no-traffic recovery。若确需数据库语义回滚，只能通过显式 operator-controlled migration 完成。
