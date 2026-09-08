## 1. Contract

- [x] 1.1 调查事务、checkpoint、retention与周期约束并完成设计
- [x] 1.2 change strict后开始实施

## 2. Implementation

- [x] 2.1 sqlc读取最新checkpoint与material比较，保留每轮authoritative评估
- [x] 2.2 no-op/完整评估projection时间与checkpoint指针分离
- [x] 2.3 reconciliation默认20秒，配置边界及runbook

## 3. Acceptance

- [x] 3.1 100次no-op、新source、stale/degraded、zero-evidence、retention与恢复
- [x] 3.2 absence/resolve历史精确断言、并发/重启/重试PG验收
- [x] 3.3 20秒调度/lease/freshness回归与相关race/acceptance
- [x] 3.4 make test build、canonical同步、strict、diff及证据对账
