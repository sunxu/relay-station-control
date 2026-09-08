## Why

Phase 3/4 的 Inventory 证明账号是否存在及最近 runtime 观察，Request Quality 证明请求表现，但现有 auth 大类无法区分 token 失效、明确账号封禁与普通 403。管理员需要在每个 Node 的 Google Antigravity 认证文件账号上查看 Availability，并以持久化 ACTIVE/RESOLVED occurrence 跟踪已确认故障。

## What Changes

- 只支持 provider=antigravity 且由现有 auth-files runtime 响应证明 source=file 的账号；沿用 node_id + canonical account_key，跨 Node 独立。
- 固定 AVAILABLE/TOKEN_INVALID/ACCOUNT_BLOCKED/FORBIDDEN/UNKNOWN/DISABLED 六种展示状态，采用新鲜完整证据、去抖和明确恢复规则。
- 在现有 auth-files 与 usage normalization 边界仅提取固定安全枚举；Request Quality 原 failure_class 五类不变，附加可空 auth_failure_reason。历史 auth 不回填、不猜测。
- 在 Control PostgreSQL 增加最小 availability checkpoint 与 occurrence 持久状态，复用既有 reconciliation 生命周期与受限查询函数；不是修改现有只读 Incidents 的状态机。
- 现有账号 workspace 增加 Availability/Reason/Since；复用 History/Incidents 入口，另以只读 availability occurrence 列表展示 ACTIVE/RESOLVED。

## Capabilities

### New Capabilities

- `antigravity-account-availability`：Antigravity 文件账号六态判定、确认/恢复、持久 occurrence 及安全读取。

### Modified Capabilities

- `cliproxyapi-readonly-driver`：auth-files 边界允许受限认证语义解析，仅输出枚举，原文仍丢弃。
- `account-inventory-snapshot`：合格快照允许附加可空安全 availability projection，并随已有 promotion 保存到 current Inventory。
- `account-request-quality`：最小事件允许可空认证子原因，不改变原 taxonomy、hash、identity、retention 或现有查询结果形状。
- `node-centric-topology-ui`：现有账号 workspace 增加只读 Availability/Reason/Since 及 occurrence 证据。

## Impact

仅修改 Control，遵循 System Design v1.8/R4.7 的 Control 观察边界、ADR-0001 与 ADR-0002；不修改 Gateway 或 CLIProxyAPI。数据来源仍仅为 account_inventory、account_inventory_provider_states、account_request_quality_events 及既有 `/v0/management/auth-files`。使用已经采集落库的 request events，不新增 queue consumer、Node API 或 Google 请求。

未来实现涉及 Driver safe projection、Inventory finalize/promotion、安全 event insert、PostgreSQL additive migration、store/reconciler、Control readonly OpenAPI、generated Go/TS、UI、tests/runbook。sqlc 如涉及新 SQL 则正常生成；禁止手改生成文件。零 Token 读取/保存：不读认证文件内容，不调用 download 或 Google；现有 management key 仍通过既有 SecretResolver 使用，与 access/refresh token 无关。不得存 raw error/body/status_message。

旧表、旧 v1 函数与 HTTP 字段保持兼容；新字段可空、旧写入留下未观测值。需要 persistence migration，不能声称零 migration。旧历史不回填、不清理；生产回滚停止新观察并保留 forward schema。无新 index 除 checkpoint 主键与 ACTIVE occurrence 唯一约束/必要列表索引；不增加 Redis、rollup、Prometheus/Grafana、quota、自动 disable/delete/re-auth、通知平台或数据面职责。

## Architecture decision

用户已明确：UNKNOWN 永远不告警，第一版不做 other/runtime_unavailable 告警。此决定关闭原 A1 冲突，不保留UNKNOWN告警例外。仅token_invalid/account_blocked/forbidden可成为availability occurrence reason；other只保留安全分类用途，runtime_unavailable只解释UNKNOWN，无严重度、确认阈值升级或故障occurrence。

已存在的明确认证故障在证据失效后保留历史ACTIVE记录，不把UNKNOWN伪造为恢复，但不因UNKNOWN创建、重发或升级告警。当前状态与既有故障历史分开呈现。本轮只完成规划与Architecture复核；实施仍须后续明确授权。
