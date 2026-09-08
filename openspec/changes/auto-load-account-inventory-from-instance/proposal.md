## Why

当前从 Node 拓扑或书签进入账号清单页时，URL 中已有的 `instance_id` 只用于预选 Node，页面仍可能以空表格结束，管理员必须再次操作才能看到该 Node 的账号。这个入口应在首次加载时兑现其明确的 Node 上下文，同时保留筛选和分页的显式查询语义。

## What Changes

- 当 URL 含合法 `instance_id` 时，账号清单页首次加载后预选该 Node，并自动执行一次默认账号查询。
- URL 缺少 `instance_id` 时继续要求管理员手动选择 Node，再由现有“查询”动作加载数据。
- 首次自动查询只使用默认筛选和第一页；筛选编辑、Node 切换和分页继续由现有手动动作触发。
- 保留认证、capability、审计、错误和只读边界；失败状态正常展示，不自动循环重试。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `account-inventory-readonly-query`：补充账号清单页从 URL `instance_id` 进入时的首次预选与默认查询行为。

## Impact

- 受影响仓库：`relay-station-control` 的 Web 账号清单页面及其现有查询客户端调用时序。
- 不修改 OpenAPI、Go API、数据库、sqlc、生成客户端、migration、审计格式或运行时真相；不新增外部依赖。
- 不改变 Gateway Directory source、Node/Gateway 调用链或任何账号/绑定 mutation。该页面仍是只读查询，数据面隔离不变。
- URL 中的 UUID 仅作为 Node 选择上下文；非法、未知或不具备 capability 的值沿用现有 fail-closed 状态。
