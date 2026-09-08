## Why

现有 PostgreSQL Account Request Quality 已完成采集与单账号统计，但管理员无法在 Node Topology 查看窗口表现。本 change 在已批准的观察边界内增加单账号只读表格，保留无请求账号并明确区分 unknown 与 unavailable。

## What Changes

- 以当前 Inventory 安全 read model 为账号集合，复用既有质量函数，提供15m/1h、provider、quality及有界分页。
- 新增 GET `/api/topology/nodes/{instance_id}/account-quality` 和现有 Node detail 内的 Account Quality 表格。
- 增加最小 additive readonly query-access migration；不修改现有函数、事件持久化或采集。
- presentation-only good/degraded/bad/unknown，不改变任何 Inventory/lifecycle/Binding/Duplicate truth。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `node-centric-topology-ui`: 增加单账号窗口质量读取与只读可视化，保留原账号清单页面的独立入口和审计。

## Impact

仅 control 仓库：OpenAPI、生成Go/TS、store query wrapper、additive function migration、Topology UI、tests、runbook。遵循系统设计 v1.8/R4.7 的 Control 观察边界及 ADR-0001 §3.3–3.5、ADR-0002 原生数据面职责。现有 API兼容；新GET沿用super_admin授权、no-store和5s超时。runtime仅EXECUTE新函数，不扩大直接SELECT；不新增审计事件或metrics。回退前端/后端即可停用新入口，生产保留forward migration。CLIProxy、ingestion、事件schema/retention/taxonomy不变；不增加UI actions/history/charts、quota/inspection/automation、Prometheus/Grafana、rollup/partition/Redis/cache。
