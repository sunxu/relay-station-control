## Why

已有Quality呈现窗口表现、History呈现单请求证据，但管理员仍需逐账号检查重复失败。增加纯只读Incident聚合，在Node detail直接列出持续重复失败账号。以System Design v1.8/R4.7的Control观察边界和ADR-0001/0002原生数据面职责为约束。

## What Changes

- 最近15分钟同Node/account_key/failure_class至少3次失败计算active，仅auth/quota/rate_limit/upstream；unknown不生成。
- MVP只计算active；事件缺少持久episode语义且保留期滚动，不增加recovered重建。低于阈值即消失，不宣称恢复。
- 新readonly PostgreSQL query-access function、GET API与Node detail Incidents，点击账号复用Request History。
- 不修改CLIProxy、collector、queue、event schema/retention/taxonomy、Inventory/lifecycle/Binding/Duplicate、Quality classification。
- 无Incident persistence/index/worker/状态机、Durable Job remediation、通知、动作、quota、inspection、raw detail、Prometheus/Grafana、Redis/rollup/partition。

## Capabilities

### New Capabilities
- 无独立持久真相能力。

### Modified Capabilities
- `account-request-quality`: 增加active重复失败只读聚合、Inventory gate、授权分页及性能契约。
- `node-centric-topology-ui`: 增加Incidents只读区域与History入口。

## Impact

仅control仓库。OpenAPI与generated Go/TS新增GET；00023只加SECURITY DEFINER query-access function/ACL，sqlc无新查询；不新增metrics/audit truth。复用runtime身份与super_admin，不影响既有API；Runbook明确窗口计数与采集缺口。未来发布先function再API/Web，回滚保留forward schema，Down只在隔离测试删除新function。本轮本地commit，不push/deploy/archive。
