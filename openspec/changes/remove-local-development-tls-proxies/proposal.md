## Why

本地 Relay Station 通过两个 nginx TLS listener 和证书初始化容器暴露管理页面，增加证书、更新和恢复成本。用户已批准本地 dev 改用 HTTP，保留 Gateway 公共入口的内部路由隔离。仅影响 control 与 ops，不修改 Gateway/Node 产品代码。

## What Changes

- 移除外部 `CONTROL_COOKIE_SECURE` 配置，不新增 `CONTROL_DEV_ALLOW_INSECURE_HTTP`。直接由既有环境派生策略：dev 使用 HTTP/non-Secure dev Cookie，staging/production 使用 HTTPS/Secure Cookie；dev 允许容器非 loopback listener，由本地 Compose 保证宿主 loopback 映射。
- ops 删除 Control TLS 代理/初始化服务及 Gateway TLS listener，保留 HTTP gateway-proxy 和 `/internal/v1/` 403。
- 本地 Control 使用 http://127.0.0.1:18080，Gateway 使用 http://127.0.0.1:18082；Directory 继续内部 http://gateway:8080。
- 启动、备份、更新、恢复、健康检查与文档适配无 TLS 拓扑，旧完成记录/证书/数据保留。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `administrator-access`: 按环境派生 HTTP/HTTPS 与 Cookie 策略，保持会话和 CSRF。

## Impact

Control auth/main/tests 与 ops Compose/devctl/operations/tests/runbook；无 OpenAPI、generated code、SQL/schema/migration、collector、数据面或身份变更。用户最新授权先提交规划，再实施、测试；本轮不更新本地运行环境，不 push、不 archive。涉及 dev 安全边界变化（受限本地网络中的明文），不扩展 staging/production。遵循 System Design v1.8/R4.7 与 ADR-0001/0002；其入站 TLS 要求保留为非本地-dev规则，本 change 明确本地例外。dev 不再提供独立 HTTPS 登录模式，旧 Cookie 配置不再生效，部署模板和私有 override 在未来实施时移除旧键。
