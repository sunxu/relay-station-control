# Design

## Architecture Contract

Relay Station internal network is a trusted transport domain. Internal HTTP does not provide confidentiality against an attacker capable of sniffing east-west traffic; token confidentiality therefore depends on network isolation.

所有 local/staging/production 的 service-to-service HTTP 调用 MUST 使用 `http://`。范围包括：Control→Gateway Directory、Control→CLIProxyAPI `/healthz`、Control→CLIProxyAPI `/v0/management/auth-files`、Gateway→Relay Node AI data API 以及其它明确属于 Relay Station 内部的 HTTP API。PostgreSQL、Redis 和外部上游协议不在本契约内。

Gateway Directory 的服务认证与路由授权独立于传输：保留 `relay_control_reader` 独立 high-entropy token，只允许精确 `GET /internal/v1/api-account-directory`；其它 method/path 默认拒绝。Gateway public ingress MUST 拒绝 `/internal/v1/*`，内部 listener 只绑定 private Docker network/VPC/防火墙允许的管理地址。

## Browser ingress boundary

Browser/external ingress→Control 不由本 change 强制改为 HTTP。已有 production HTTPS、Secure Cookie、same-origin、CSRF、HttpOnly、SameSite 和 MFA 继续有效。文档必须拆开 browser ingress transport 与 service-to-service transport，不能用浏览器 HTTPS 作为内部 HTTP 的替代安全依据。

## Current runtime and rollback boundary

Control、Gateway 和 Ops 当前版本的内部 endpoint MUST 使用 `http://`。内部 endpoint validator MUST 拒绝 `https://`；runtime configuration 不得接受内部 HTTPS endpoint，部署模板不得生成内部 HTTPS endpoint。当前版本不得保留 scheme-dependent 的 HTTP/HTTPS runtime branch，也不得保留 CA、certificate 或 hostname-validation 的内部 runtime 配置。删除 internal TLS proxy、internal certificate/CA/hostname validation、mTLS future requirement 及“HTTP 仅 local 例外”的门禁。

历史 HTTPS 支持只能存在于 old released binaries/images、Git history、archived deployment configuration 以及 certificate backup/rollback material。当前版本不得为了 rollback 继续接受 `https://`；回滚必须通过部署 old application/deployment version 实现，而不是依赖当前 runtime 的双协议兼容。

Control 专用 transport 继续：无代理、拒绝重定向、固定 method/path、超时、响应体/记录数上限、Secret file/manager 注入、日志脱敏。HTTP 不得自动降级或通过 proxy 发送。不得把 Management Key、service token、Cookie 或上游凭据写入 DB、镜像、前端或日志。

## Deployment and isolation

Compose/private network、VPC、firewall、Security Group 和 ingress ACL 必须证明内部端口不可由公网访问；部署 acceptance 需要验证 public `/internal/v1/*` 返回拒绝、内部精确 GET 带 token 可用、其它 method/path 被拒绝。不得以 HTTP 本身声称传输机密性或服务身份认证。

## Migration and rollback

本 change 默认不新增数据库 migration。若实现只需配置/代码/文档调整，不改变 PostgreSQL/Redis schema。删除 TLS proxy/证书挂载属于部署变更，不清理历史证书备份。回滚恢复旧 Control/Gateway/Ops 版本和证书配置，保持数据面与数据库 forward schema 不变。

## Architecture Review evidence

当前只读调查发现：

- `ops/docs/RELAY_STATION_SYSTEM_DESIGN_CN.md`、ADR-0002 多处仍写 `service token over TLS`、TLS 与 restricted network 三项同时必需。
- `ops/dev/DEPLOYMENT.md`、`ops/dev/README.md` 仍写 staging/production HTTPS-only；`ops/dev/test_update_runtime.py` 和本地操作测试保留 `control-tls` 历史拓扑断言。
- `control/docs/runbooks/cliproxyapi-readonly-driver.md` 允许 HTTP/HTTPS，但仍描述 HTTPS 专用 transport、证书策略和旧 TLS 验收；Directory runbook 仍保留 CA/TLS 旧文案。
- `gateway/deploy/docker-compose.local.yml` 与 `gateway/deploy/config.example.yaml` 存在 TLS/HTTPS 通用配置；Gateway canonical Directory 文档需在实施阶段核对并 supersede TLS 要求。
- 部分 TLS/HTTPS 命中属于外部 OAuth、上游 API、数据库 SSL 或测试工具，不应被本 change 误删。

实施前必须逐项确认：生产 HTTPS enforcement 的真实代码路径、internal TLS proxy/cert/CA 挂载、Gateway public ingress 路由、Control/Gateway endpoint defaults，以及 browser Secure Cookie 是否独立。

## Required acceptance after Architecture approval

- 每类内部调用在 local/staging/production 的配置和代码都选择 HTTP。
- 公网无法访问内部管理路由；精确 token/method/path 授权仍生效。
- HTTP client 无 proxy、无 redirect，超时和响应限制保持。
- Browser production HTTPS/Secure Cookie 行为未被改变。
- Gateway Account/Group routing、CLIProxyAPI credential/scheduler 和数据面调度无变化。
