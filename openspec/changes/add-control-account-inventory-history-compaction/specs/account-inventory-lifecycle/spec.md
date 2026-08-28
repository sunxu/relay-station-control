## MODIFIED Requirements

### Requirement: lifecycle产品读取 MUST 保持只读与数据面隔离

Lifecycle foundation SHALL 允许 `account-inventory-readonly-query` 向受认证管理员提供有界 current 投影，并允许独立 `account-inventory-history-compaction` 只从已提交历史生成日级摘要、覆盖率和受控清理到期历史。Query 与 history MUST NOT 提供账号导出、人工状态操作、补采、promotion、重建或删除入口；history MUST NOT 重算、删除或改变 current lifecycle，受控删除 current source poll 后外键可以置空但冗余来源和当前字段保持不变。本 capability 仍不实现 HMAC account ID、跨 Node 重复、趋势产品页或告警路由，也不修改 Gateway/Node 或增加既有固定账号清单只读 GET 之外的外部请求。

#### Scenario: 管理员访问现有产品界面
- **WHEN** history compaction 部署后管理员访问产品界面
- **THEN** 可继续读取有界 current 账号列表，但没有历史趋势、导出、状态修改、补采、promotion、重建、删除或告警入口

#### Scenario: 查询与promotion并发
- **WHEN** 管理员 query、lifecycle-aware finalize 和到期历史清理并发
- **THEN** query只观察已提交 current 状态，promotion按原规则推进，history不改变missing计数且current source外键置空不丢失冗余来源含义

#### Scenario: 网络调用范围验收
- **WHEN** lifecycle、query、history恢复和策略切换验收运行
- **THEN** history/query不调用Gateway、Prometheus、模型数据面、Node写接口或未登记目标，Node请求数量与既有poll-run一致

#### Scenario: Control 或 PostgreSQL 停止
- **WHEN** lifecycle/query/history依赖停止或旧应用回滚
- **THEN** 已形成current状态和摘要保留，新状态/读取/压缩按相应依赖暂停，Gateway与Relay Node已有模型请求继续
