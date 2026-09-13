## Why

Phase 6 已关闭并归档，但 post-closeout independent review 发现三个窄契约缺口：Relay Node durable endpoint admission 与既有 HTTP-only transport 不一致、Probe 授权未与 Retire/Replace 建立真实行锁串行化，以及内部 HTTP endpoint 前端校验错误拒绝 Docker/OrbStack 单标签主机；同时若干 canonical spec 仍保留 archive 生成的 Purpose 占位文本。本 corrective change 在不重开 Phase 6、不扩大产品边界的前提下补齐这些证据和防线。

## What Changes

- Node Register/Edit/Replace 的新命令只接受 canonical `http://` management endpoint；历史已完成 HTTPS command receipt 仍按原编码与原响应精确 replay。
- 通过 migration 36 为 `relay_node_assets.management_endpoint` 增加 Node-specific HTTP-only durable invariant；已有 HTTPS durable row 使 migration fail closed 且不被重写。
- Health 与 Connection Test 的共享、secret-free Probe authorizer 在短事务内以 Node lifecycle read lock 串行化 Retire/Replace，提交后才执行一次 bounded `Driver.Probe`。
- Gateway 与 Node 表单复用小型 internal HTTP endpoint validator，接受 Docker DNS、`host.docker.internal`、IPv4、域名和既有合法 base path，拒绝 HTTPS 及既有不安全输入。
- 仅清理 canonical OpenSpec 中 archive 生成的 Purpose 占位文本，不改变 requirement 或 scenario。
- 增加 PostgreSQL 18、API、UI、历史 replay、兼容回滚和回归验收证据。

## Capabilities

### New Capabilities

- 无。

### Modified Capabilities

- `relay-node-asset-lifecycle`: 收紧新 Node lifecycle command 的 endpoint admission，并冻结历史 receipt replay 兼容行为及数据库 durable boundary。
- `relay-node-management-operations`: 为 Health/Connection Test Probe authorization 增加 lifecycle row-lock fence。
- `asset-registry`: 明确 Gateway/Node internal HTTP endpoint UI 校验支持内部 DNS 主机并保持后端为权威边界。

## Impact

- **仓库与边界**：仅 `relay-station-control`；Control 继续不进入 AI 请求数据面，CLIProxyAPI 与 Gateway 不修改。
- **OpenAPI/sqlc/generated**：预计无需改变 OpenAPI 或 sqlc 输入；若实现证明需要改变生成输入，使用既有 `make generate` 并验证第二次无 delta。
- **Migration**：新增 forward Goose migration 36，不新增 business table/column，不自动重写历史 HTTPS row。
- **安全**：新 Node durable endpoint 与既有 HTTP-only transport 一致；Probe 继续不读取 Secret，不跨 HTTP 持有 DB transaction/lock。
- **兼容与回滚**：compatibility class/floor 保持 `2 / 2`；真实 Stage 2 class2 artifact 在 schema 36 上执行 startup/read/reconcile/Retire/Replace smoke，旧 5 参数 writer 继续 fail closed。
- **UI**：只替换 management endpoint 表单的内部 HTTP 校验，不引入通用表单框架或 HTTPS 能力。
- **文档**：canonical specs 仅更新精确 archive placeholder 的 Purpose。
- **Non-Goals**：Phase 7 account operations、HTTPS/TLS 或双协议支持、Relay Scheduler、自动修复/账号迁移、Gateway Account/Group CRUD、Docker/Compose control、新 lifecycle 状态、health history、后台 health polling、credential mutation、generic endpoint framework。
