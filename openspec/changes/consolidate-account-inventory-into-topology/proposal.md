## Why

账号清单和Node Topology已共用同一账号POST读取、AccountList和详情抽屉，独立页面产生重复入口与交互维护。用户批准统一到Topology，并明确不保留旧/account-inventory地址或跳转。

## What Changes

- Topology成为唯一账号工作入口；合入exact email、基础状态、page size及环境采集容量诊断。
- 账号筛选统一为编辑后显式查询；Node选择/深链接加载默认首页。保留当前账号默认值、质量/最近请求、详情和关联观察。
- **BREAKING** 删除旧前端/account-inventory路由、菜单、页面及重复hook；旧路径使用通用未知路径行为，无兼容跳转。后端/api/account-inventory/*不删除。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `node-centric-topology-ui`: 唯一账号入口及完整筛选、容量诊断。
- `account-inventory-readonly-query`: 账号UI迁入Topology，调整Node选择触发语义，保留HTTP读取契约。

## Impact

仅control仓库前端、测试、OpenSpec和现行runbook。无OpenAPI、migration、sqlc、generated、metrics、audit schema、采集或数据面修改；CSRF、逐页审计、加密cursor仍复用。遵守System Design v1.8/R4.7及ADR-0001/0002的Control只读边界。旧浏览器书签有意失效；回滚仅恢复前端版本，无数据回滚。不push/deploy/archive。
