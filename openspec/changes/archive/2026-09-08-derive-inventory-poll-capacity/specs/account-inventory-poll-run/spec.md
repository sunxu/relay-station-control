## MODIFIED Requirements

### Requirement: Worker SHALL 先取得并发额度再认领并立即调用 Driver

Worker SHALL 在认领数据库记录前取得有界 HTTP 并发额度。认领 MUST 使用短事务、`FOR UPDATE SKIP LOCKED`、数据库时间、随机 fencing token 和执行 lease，原子设置 running、增加 attempt 并返回剩余 grace；提交后 MUST 不再排入其他队列，并以剩余 grace 与请求总超时的较小值立即调用固定 Node Driver。

#### Scenario: HTTP 并发额度已满
- **WHEN** 所有 poll 并发额度正在使用
- **THEN** 其他 poll run 保持 pending 且不开始 lease，释放额度后才允许认领

#### Scenario: 两个 Worker 竞争同一 poll run
- **WHEN** 两个 Worker 同时尝试认领同一 pending/retry_wait run
- **THEN** 只有一个 Worker 获得 running lease/fencing 并增加 attempt，另一个跳过该行且不调用 Node

#### Scenario: 认领后预处理耗尽 grace
- **WHEN** Secret/DNS 等 Driver 预处理尚未真正发出 HTTP 时，认领返回的剩余 grace 已耗尽
- **THEN** context 阻止 HTTP dispatch，Control 不越过宽限请求 Node，并按未完成 Control 执行恢复或 abandoned

#### Scenario: 容量配置危险
- **WHEN** 并发、最坏请求时长、grace、lease 和调度余量不能推导出至少一个安全 Node 容量，或 lease 校验不通过
- **THEN** poll service 在任何 Node 请求前拒绝启动并暴露固定配置错误；容量使用 account-inventory-poll-capacity 定义的统一公式，不使用人工 MAX_NODES 或独立50/10特例，Control 数据面隔离保持不变
