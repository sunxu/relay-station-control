## Why

当前 Control Web UI 的 `/assets` Asset Registry 路由与前端构建静态资源命名空间存在冲突风险。Phase 6 mutation UI rollout 前必须消除该冲突，保证资产路由始终可达。

## What Changes

- 使 `GET /assets` 与 `GET /assets/` 始终解析为 Asset Registry SPA route。
- 将构建产物使用的静态资源命名空间与 `/assets` product route 隔离。
- 明确 embedded server、SPA fallback、lazy chunk 和 static miss 的可测试行为。

## Capabilities

### New Capabilities

- `asset-registry-web-routing`：冻结 `/assets` route 与静态资源命名空间隔离契约。

## Impact

只修改 Control Web build/server routing、测试和相关文档；不修改 Gateway/Node lifecycle、数据库、OpenAPI、data plane 或 CLIProxyAPI。Phase 6 mutation UI rollout 依赖本 change 完成并验收。

## Non-Goals

- 不创建 Gateway/Node asset mutation。
- 不新增 Migration、Gateway/Node API、routing scheduler 或凭证能力。
- 不修改 Gateway、CLIProxyAPI 或 Control 的业务资产模型。
