## Why

账号清单与Topology Account Quality重复展示同一Inventory账号集合，管理员需要跨页面查看生命周期与请求表现。参考本地CPA Manager Plus账号行内最近请求和详情抽屉，在Control观察边界内统一入口及展示。

## What Changes

- 两个入口复用同一账号列表：账号、Provider、账号状态、请求质量、成功率、请求数、P95、最近请求成功/失败与最近失败。
- 以account_key选择账号，抽屉分请求历史与采集信息；Incident复用相同详情入口。
- 每账号最多10条最近7天请求摘要，旧→新显示，最新在右；空记录和读取不可用分开。
- 保留Node深链接、默认present、缺失记录入口、现有筛选与容量诊断。新增受保护的只读组合POST，复用既有质量read model，不拼接独立分页。

## Capabilities

### Modified Capabilities

- `node-centric-topology-ui`：共享账号视图、详情抽屉和最近请求结果。
- `account-inventory-readonly-query`：账号清单入口整合质量，保留显式查询和深链接语义。

## Impact

仅control仓库，遵循System Design v1.8/R4.7与ADR-0001/0002。涉及OpenAPI、Go/TS生成、store readonly query、一个additive query-access migration、Web与runbook/tests。不修改旧函数签名、事件schema、collector、retention、taxonomy、CLIProxy或Gateway。不新增表/index/缓存/rollup/metrics/告警/审计平台或mutation。旧Inventory POST API保留；既有GET响应新增字段且参数兼容，新POST复用CSRF/AEAD cursor/逐页审计，既有字段语义不变；backend/Web一起发布。未push、未deploy、未archive。
