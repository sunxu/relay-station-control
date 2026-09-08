## Why

Account Quality已提供窗口表现，管理员还需要最近具体请求解释Good/Degraded/Bad。本change只在现有Node detail增加单账号7天History，不建设请求详情或监控平台。

## What Changes

- 新增受Inventory membership gate约束的只读7天event query和GET API，复用原事件表/索引。
- Account Quality增加View History入口，独立状态和keyset分页。
- 新增一个additive query-access function；不更改事件schema、collector、CLIProxy、usage queue、retention、taxonomy或classification。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `account-request-quality`: 增加现有Inventory账号的7天请求证据安全读取。
- `node-centric-topology-ui`: Account Quality单账号只读drill-down，明确HTTP身份传输的窄范围例外。

## Impact

仅control：OpenAPI/generated Go/TS、store、00022 readonly migration（实施时确认编号）、Topology组件/tests/runbook。遵循系统设计v1.8/R4.7 §4.2/§5.1、ADR-0001 §3.3–3.5、ADR-0002 §2.3，只观察不控制。新API additive；原API不变。super_admin、no-store、runtime EXECUTE安全读取，不增加audit/metrics。生产回滚应用保留forward函数，隔离Down仅drop新函数。无table/index/cache/Redis/rollup/partition；无raw body/headers/tokens/prompt、request详情页、charts、search/export/date picker、retry执行请求、quota/inspection/automation/Prometheus/Grafana。本轮只本地commit，不push/deploy/archive。
