## Why

Control 已完成管理员认证、资产注册和 PostgreSQL 持久任务基础，但仍没有访问 Relay Node 管理面的实现。阶段 2 要求在不修改官方 CLIProxyAPI、不进入 Gateway 请求数据面、也不保存任何凭证或原始响应的前提下，只读调用现有 `/healthz` 与 `/v0/management/auth-files`。如果后续账号采集直接使用普通 `http.Client` 或把上游 JSON 交给业务层解析，将留下 SSRF、DNS 重绑定、重定向、代理绕行、响应体耗尽、Management Key 泄露和上游字段漂移等风险。

本 change 先建立具体且受限的 `CLIProxyAPIDriver`：固定方法和路径、运行时解析 Secret、逐次验证目标地址、严格限制响应并把阶段 0 脱敏契约样本映射为内存观察结果。完成后，后续 poll-run change 可以专注于数据库时间槽、租约、持久证据和快照提升，而不重复实现网络安全边界。

## What Changes

- 在 `control` 新增固定 Node Driver 契约和 registry，首个实现只登记 `cliproxyapi`，支持健康探测与账号清单读取；未知 node type 或未声明 capability fail closed。
- 新增 CLIProxyAPI 只读传输边界，只允许 `GET /healthz` 与 `GET /v0/management/auth-files`，禁用环境代理和重定向，执行 management host/CIDR allowlist、逐次 DNS 解析与 IP 校验、HTTP 隔离网限制和 HTTPS 证书校验。
- 新增运行时 Secret resolver 边界，通过受保护配置把数据库中的 opaque Secret 引用解析为 Management Key；Secret 只存在于单次请求内存和请求 header，不进入数据库、API、日志、指标、Trace、审计或测试证据。
- 新增固定连接/总超时、健康响应限制、账号清单 5 MiB/1,000 条限制及有界版本/提交响应头；非 200、JSON 非法、缺少/非法 `files`、超限或未知来源形态返回封闭分类，不回显响应正文。
- 使用 `ops/contracts/cliproxyapi/auth-files/v1` 的脱敏 fixtures 实现字段白名单、provider/email 规范化、runtime/disk-fallback 判定、身份完整性、Provider 作用域、节点内重复和固定基础状态映射；未知与敏感字段在 Driver 边界丢弃。
- 新增安全负向测试、故障恢复测试、无代理测试、低基数指标/日志 canary、官方 CLIProxyAPI 原版镜像直接冒烟和无数据面副作用验收。真实冒烟请求串行执行，相邻请求至少间隔 10 秒。
- 不新增数据库表或 sqlc 查询，不修改 OpenAPI/React，不调度或持久化 poll run，不保存账号快照，不推进 missing/out-of-scope 生命周期，不实现历史压缩、告警、Sub2API Adapter、Prometheus Adapter 或 Gateway Connector。

## Capabilities

### New Capabilities

- `cliproxyapi-readonly-driver`: 定义 Control 对官方 CLIProxyAPI 现有健康与账号清单接口的固定只读调用、安全目标解析、运行时 Secret 隔离、契约分类和零持久化边界。

### Modified Capabilities

无。现有 `asset-registry` 继续提供 Node 类型、capability、management endpoint 和 Secret 引用，`durable-job` 保持生产 Executor registry 为空；本 change 不修改两者的数据库或产品接口行为。

## Impact

- **阶段与结果**：阶段 2；开发者可通过统一只读 Driver 安全探测 CLIProxyAPI 并取得脱敏、分类后的内存账号观察结果，为后续持久采集奠定边界。
- **仓库**：只修改 `control`；`ops` 系统设计 v1.0 第 9.1–9.6、19、23、24 节、ADR-0001 和阶段 0 auth-files v1 fixtures 是输入真相源，不修改 `ops`、Gateway 或 Node 产品代码。
- **OpenAPI/UI**：无产品 HTTP 路径、schema、生成客户端或 React 页面变化；浏览器和 Prometheus 不直接访问 Node。
- **Migration/sqlc**：无新 Migration、表、权限或 sqlc 查询；Driver 结果只在内存中存在。账号持久化必须由后续 change 定义。
- **运行配置**：新增有界的 management DNS/CIDR、HTTP 管理网、连接/总超时、响应限制和 Secret resolver 配置；缺失、矛盾或危险配置在发起网络请求前 fail closed。
- **安全**：Management Key 不写入 Control 数据库；endpoint、DNS 结果和每次实际拨号 IP 都必须授权。固定路径、无代理、无重定向、TLS 和大小限制共同防止管理凭证被转发或响应耗尽。
- **指标与日志**：只使用固定 `node_type`、`operation`、`result`/`reason` 分类；禁止 instance ID、endpoint、IP、email、provider、Secret 引用、Management Key、原始错误或响应内容成为标签或日志字段。
- **兼容性**：以官方 CLIProxyAPI v7.2.141 和 auth-files v1 fixtures 为首个契约；缺失版本头使用 `unknown`，新增未知 JSON 字段被丢弃，破坏性形态变化 fail closed。
- **回滚**：纯应用代码/配置变更，不涉及数据库回滚。旧版本忽略 Driver 配置；回滚不会改变 Node、资产或数据面。
- **数据面隔离**：Driver 只由 Control 后端显式调用，永不代理模型请求或修改 Node；Driver、Control 或其管理网络故障不影响 Gateway/Relay Node 既有流量。
