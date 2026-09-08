## Why

为现有 Control 账号清单提供真实请求质量数据，先打通 CLIProxy HTTP queue → Control PostgreSQL → 单账号 15m/1h 统计。仅扩展控制面观察能力，遵循 System Design v1.8/R4.7 的 Node 原生职责边界与 ADR-0001 的单实例部署，不改变请求路由。

当前状态：**BLOCKED（账号身份映射）**。本地源码调研完成；合法 usage event 的 `source` 不保证是 email，Control 没有 `auth_index` 映射。直接带 provider/email 的事件可映射，但不能把仅此子集的采集宣称为完整闭环。按本轮停止条件，先记录设计和证据，不开始生产实现。

## What Changes

- 唯一事件来源为 `GET /v0/management/usage-queue?count=100`，参考本地 CPA Manager Plus 的 HTTP client、normalizer 和 event hash。
- 计划复用 Control CLIProxy transport、管理认证、超时及 runtime cancellation，按 Node 一个 collector，1s poll、最大 30s backoff。
- 仅存最小账号请求事件，PostgreSQL additive table、幂等写入、7 天 retention；只提供 15m/1h 单账号查询。
- identity blocker 解除前不添加 migration、collector 或质量查询。需证明缺失 email 时 auth_index 能唯一且正确地关联既有 canonical account_key，不能猜测或静默丢弃。
- CLIProxy/CPA 零修改。无 SQLite、RESP/Subscribe、quota、inspection、action queue、自动 disable/恢复、Capacity Advice、rollup、partition、UI/Grafana 或额外敏感信息治理工程。Node quality 留待后续。

## Capabilities

### New Capabilities

- `account-request-quality`: HTTP 请求事件采集、最小 PostgreSQL 持久化及单账号窗口质量查询。

### Modified Capabilities

无。既有 inventory canonical identity、Binding、ownership eligibility 不改变。

## Impact

仅 Control 仓库可修改；CPA/Node 仅只读源码参考。计划涉及 `internal/drivers/cliproxyapi`、最小 domain/collector、`internal/store`、additive migration 和 runtime 接线。首版最小查询可先采用 Go repository read method，不扩公共 HTTP/OpenAPI 或生成 Web client；无 UI、指标平台或额外审计能力。

数据库、sqlc 与受限 runtime ACL 依照既有惯例；实际 migration 在身份契约解除 blocker 后才实施。停用 collector 即停止消费，回滚保留 forward schema。HTTP queue 为 destructive pop 且没有 ACK；不能承诺跨 Node 弹出与 Control 提交的 exactly-once 或零丢失。
