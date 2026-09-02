# AGENTS.md

Relay Station Control 是单环境管理与可观测服务，采用 Go 1.27、React 19 和 PostgreSQL 18。Control 不在请求数据面中。

## 真相源

- 系统边界与阶段验收：`../ops/docs/RELAY_STATION_SYSTEM_DESIGN_CN.md` v1.8 / R4.7
- 架构决策：`../ops/docs/adr/0001-control-technology-stack.md`、`../ops/docs/adr/0002-use-sub2api-native-downstream-scheduling.md`
- 行为与契约变更：当前已批准的 OpenSpec change
- API：`api/openapi.yaml`
- 数据库：`migrations/` 中不可变的 forward Goose migrations
- OpenSpec 规则：`openspec/config.yaml`

不要手工修改生成的 Go 或 TypeScript 客户端。API、Migration 或 sqlc 变更后运行 `make generate`。

## 工作方式

- 行为或契约变更先创建或更新 OpenSpec change；实现不得超出已批准任务。
- 优先复用现有模块、Store adapter 和受控数据库函数，保持改动小且可独立评审。
- 使用 Conventional Commits。
- 不修改无关文件，不提交 Secret、真实账号数据、本地运行数据或未脱敏外部响应。

## 验证

```bash
make generate
make test
make build
```

数据库或状态机变更还需运行对应的 `deploy/acceptance/` 验收。使用 README 中的隔离开发数据库和受限 runtime role；产品进程不得持有 migration owner 凭据。

## 关键边界

- PostgreSQL 是 Control 持久状态的唯一真相；不得用进程内状态伪装持久成功。
- Control 只观察、关联、快照、分析、告警和建议；不得进入请求数据面、调度请求、修改 Gateway Account/Group 或路由、修改 CLIProxyAPI 凭证状态、镜像 Gateway runtime scheduler truth 或自动修复重复归属。
- Gateway 是一个 Sub2API deployment，Relay Node 是一个 CLIProxyAPI deployment；必须保留两者原生 routing/scheduling、Provider/Account selection、retry 和 cooldown 边界。
- Control 不得持久化或暴露上游凭据、Node Management Key、Gateway 管理凭据或原始响应正文。
- 指标和日志不得泄露邮箱、账号 key、Secret 或高基数身份字段。
- 默认关闭的能力必须保持关闭，除非其 Runbook 明确记录了已获批准的启用步骤。
- 生产禁止 destructive migration down；回滚应停止新行为并保留 forward schema 和审计证据。
