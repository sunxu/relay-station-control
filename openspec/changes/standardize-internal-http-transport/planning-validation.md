# Planning Validation

## Current phase

Architecture planning。尚未实施、提交或部署；本文件不表示 Architecture Review 已通过。

## Review checklist

- [ ] 已列出仍要求 internal TLS 的 canonical specs、代码、Compose/ops 配置。
- [ ] 已证明内部路由不经公网暴露，Directory 认证和精确 method/path 授权保留。
- [ ] 已证明 browser/external ingress HTTPS 与 Secure Cookie 不受影响。
- [ ] 已确认不修改数据面调度、Gateway Account/Group routing 或 CLIProxyAPI credential/scheduler。
- [ ] 已确认默认 0 database migration；删除 TLS proxy/cert 属部署变更，不删除历史数据。

## Evidence commands

实施阶段按仓库运行受影响的 Gateway/Control/deployment acceptance；最终运行相关 OpenSpec strict validation 与 `git diff --check`。本阶段只记录本地静态调查，不宣称实现测试通过。
