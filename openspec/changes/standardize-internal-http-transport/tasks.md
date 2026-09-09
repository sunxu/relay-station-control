# Tasks

- [ ] 1.1 完成 Control/Gateway/Ops/Node 本地树的 internal TLS、HTTPS enforcement、proxy、certificate、CA 与 ingress 清单，并标注非目标外部 HTTPS。
- [ ] 1.2 由 Architecture Review 确认 internal HTTP trusted-network 假设、Directory token/精确路由、public ingress isolation 与 browser ingress 解耦。
- [ ] 2.1 修订 Control 内部 endpoint 配置与客户端契约为 HTTP-only；validator 拒绝内部 `https://`，移除适用的 HTTP/HTTPS runtime 分支，同时保留无代理、拒绝 redirect、Secret、超时和响应限制。
- [ ] 2.2 修订 Gateway Directory canonical spec、部署和 public `/internal/v1/*` 拒绝测试，移除 TLS 必需措辞。
- [ ] 2.3 修订 Ops 系统设计、Compose、部署/acceptance/runbook，删除 internal TLS proxy/cert/CA 要求且保留隔离证明。
- [ ] 2.4 核对 CLIProxyAPI 管理 endpoint 使用 HTTP；不修改 CLIProxyAPI 仓库或其 credential/scheduler。
- [ ] 3.1 更新跨仓库安全负例、HTTP-only 配置矩阵和回滚证据；回滚使用 old application/deployment version，不依赖当前 runtime 双协议兼容，确认不影响数据面与浏览器 Secure Cookie。
- [ ] 3.2 运行 Gateway/Control/deployment acceptance、OpenSpec strict 与 diff 检查，记录结果。
- [ ] 3.3 完成文档 reconciliation、git status 和跨仓库变更边界检查；仅 Architecture/Implementation Review 通过后实施提交。
