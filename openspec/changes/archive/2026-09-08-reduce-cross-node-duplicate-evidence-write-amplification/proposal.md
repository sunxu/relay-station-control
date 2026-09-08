## Why

Phase 4 duplicate reconciliation 每轮即使重复引用同一次采集证据也追加 per-node evidence，产生存储和诊断噪声。本change仅优化Control写入及既有周期，不改变Ownership判断。系统边界遵循System Design v1.8 / R4.7与ADR-0001/0002。

## What Changes

- 默认inventorypoll reconciliation由5秒改为20秒；成功finalize及启动回调保留。
- 每轮仍锁定occurrence并执行authoritative evaluation，只有material变化才追加完整可引用的per-node checkpoint。
- 复用三表与latest_evaluation_id；旧evidence全部保留，无cleanup。

## Capabilities

### Modified Capabilities

- `cross-node-duplicate-ownership`：material checkpoint追加、无变化抑制、并发与历史契约。

## Impact

仅Control lifecycle/reconciliation、sqlc query/generated Go、测试、canonical spec和runbook。无需数据库migration、新表/列/index；不改OpenAPI/TS、eligibility、fresh/absence/degraded/resolve规则、告警身份、Node/Gateway或数据面。不扩Topology UI，本次继续提供原始evidence诊断入口。源信息比较不输出账号或Secret日志。回滚代码只恢复高频评估及原追加行为，保留全部数据。
