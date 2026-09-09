# Proposal

## 目标与阶段

这是 Relay Station 的跨仓库架构契约修订，目标是把 local、staging、production 的所有内部 service-to-service HTTP 接口统一为 HTTP。该 change 先完成 Architecture Review；通过后才允许修改实现、部署和验收资料。

## 受影响仓库

- `control/`：Control 出站客户端、运行配置、OpenAPI/运行手册和内部 HTTP 安全边界。
- `gateway/`：Gateway Directory 内部只读路由、公开入口隔离、部署文档和测试。
- `ops/`：系统设计、部署拓扑、Compose/运行手册和 acceptance。
- `node-cliproxyapi/`：仅核对现有管理端点和部署配置；本 change 不修改 CLIProxyAPI。

## 用户与运营结果

运营人员在所有部署环境使用受限内部网络中的 HTTP 完成：Control→Gateway Directory、Control→Relay Node 管理接口、Gateway→Relay Node 数据接口及其它 Relay Station 内部 HTTP 调用。浏览器或外部 ingress 到 Control 的 HTTPS/Secure Cookie 策略保持独立，由现有入口配置决定。

## 非目标

本 change 不改变 PostgreSQL/Redis 协议、Gateway Account/Group routing、数据面调度、CLIProxyAPI credential/scheduler、请求语义、API payload、数据库 schema、迁移数据或 mTLS；不新增内部 TLS proxy、证书/CA/hostname 校验，也不以 production 环境自动强制内部 HTTPS。

## 安全与兼容影响

内部 HTTP 不提供对东西向嗅探者的机密性；token 机密性依赖 private Docker network/VPC/firewall/Security Group 隔离。内部管理端口不得经 public ingress 暴露，Gateway public ingress 必须拒绝 `/internal/v1/*`。Directory 继续使用独立 high-entropy `relay_control_reader` token，且只授权精确 `GET /internal/v1/api-account-directory`；CLIProxyAPI Management Key 继续保留并以 Secret file/Secret Manager 注入。现有无代理、拒绝 redirect、超时、响应限制和 Secret 不落日志的边界保持不变。

这是对旧“staging/production internal TLS”文档契约的明确 supersede。旧 archived OpenSpec 只作为历史记录，不修改；如已有外部消费者依赖 HTTPS，实施前必须在各仓库列出兼容影响并制定切换窗口。

## 回滚

回滚仅恢复旧版本客户端/部署配置和必要证书材料，不执行数据库 down，不删除历史证书或数据。回滚不得扩大 public ingress；内部 HTTP 契约的启用与停用必须由部署版本成组切换。
