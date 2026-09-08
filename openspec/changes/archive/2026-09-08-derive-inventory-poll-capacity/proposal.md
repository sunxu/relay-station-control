## Why

阶段 3 Inventory 采集把最大 Node 数作为独立人工配置，新增 Node 后可能因遗留低上限导致整轮停采，页面却仅显示旧证据。改为按并发和时间预算推导容量，并向管理员明确显示超限原因。

## What Changes

- **BREAKING** 默认并发10的新安全容量为20（原人工默认50）；已有21..50 Node部署必须在发布前提高并发并核验，例如并发25支持50，不能无检查直接升级。
- **BREAKING** 移除 `CONTROL_ACCOUNT_INVENTORY_POLL_MAX_NODES` 的容量控制作用；旧变量存在时启动输出固定弃用警告，忽略其值，避免遗留配置阻止启动或静默误导。
- 从并发、最坏请求时间、启动 grace、dispatch margin 推导唯一内部容量，保留硬上限 50 和整轮超限拒绝，不加入队列公平性新设计。
- 统一替换原有“50 Node 必须并发至少 10”的特殊校验为时间公式；50 Node / 并发 25 必须通过专项容量验收后才可交付。
- 增加管理员只读采集容量诊断和账号清单提示，区分超限、正常、关闭与读取不可用；不把超限归类为数据库故障。

## Capabilities

### New Capabilities
- `account-inventory-poll-capacity`: 自动容量推导、配置兼容、管理员容量诊断和恢复展示。

### Modified Capabilities
- `account-inventory-poll-run`: Worker 容量校验改用推导值，去掉人工最大 Node 数与 50/10 特例。

## Impact

Control 涉及配置、调度错误分类、Store/sqlc、OpenAPI及生成 Go/TS、账号清单 UI、低基数 metrics 和 Runbook。Ops 后续同步删除 Compose 和私有 override 的旧变量；本轮不修改 sibling 仓库。参考 `ops/docs/RELAY_STATION_SYSTEM_DESIGN_CN.md` 阶段3管理面采集边界及 ADR-0001；Gateway、Node、Binding、Directory、Duplicate eligibility 和数据面不变。

0 persistence migration；若安全读取需新增受控 readonly function，允许一个最小 additive query-access migration，不扩表权限。新增 HTTP 只读端点是 additive，既有 API 不变。回滚恢复旧镜像及匹配 MAX_NODES 配置，不回滚数据；不修改已归档 change。本轮用户已授权完成修订、实施、验证并归档，部署不在本轮范围。
