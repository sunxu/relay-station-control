## MODIFIED Requirements

### Requirement: 单账号窗口质量查询

Account Quality查询 SHALL 按node_id+非NULL account_key返回15m或1h的request_count、success_count、failure_count、success_rate、p95_latency_ms、last_success_at、last_failure_at、last_failure_class；窗口使用DB UTC时间，p95为有效duration的percentile_cont(0.95)。只补Node/Provider同字段聚合与unresolved_request_count，不扩展UI、完整Node quality产品、quota或自动行为。

Node and Provider quality derived targets MUST explicitly require active Node lifecycle and
current monitoring eligibility. Historical request events remain retained and queryable, but a
retired/replaced Node is never returned as a current eligible operational target.

#### Scenario: Node lifecycle delta
- **WHEN** the Node lifecycle condition described by this change is evaluated
- **THEN** the existing baseline behavior remains intact and the lifecycle fence is also enforced

#### Scenario: 两个窗口与 p95
- **WHEN** 存在窗口内外及不同账号事件
- **THEN** 两个窗口分别只统计目标账号窗口内事件，比例和p95与fixture一致

#### Scenario: 无请求
- **WHEN** 窗口内没有请求
- **THEN** 计数为0、比例和p95及最后事件字段为NULL，语义unknown而非健康

#### Scenario: 查询不可用
- **WHEN** PostgreSQL查询失败
- **THEN** 返回错误，不返回零请求或空成功结果

### Requirement: Account Request History SHALL 受当前Inventory membership约束

Control SHALL仅从已有account_request_quality_events读取目标Node与非NULL account_key的最近7天具体事件。MUST先在既有current Inventory安全read model证明该账号存在，不能从events反推账号；无账号返回404，Inventory/DB故障返回503，禁止伪装empty。MUST NOT修改CLIProxy、collector、usage queue、event schema、retention、taxonomy或质量classification。

Current membership checks MUST include active Node identity and lifecycle eligibility; they must
not infer a replacement Node from old events or current account data. Historical events remain
bound to the original Node.

#### Scenario: Node lifecycle delta
- **WHEN** the Node lifecycle condition described by this change is evaluated
- **THEN** the existing baseline behavior remains intact and the lifecycle fence is also enforced

#### Scenario: 存在账号有事件或无事件
- **WHEN** Inventory目标账号存在
- **THEN** 有7天事件时返回该账号事件，无事件时成功empty，既有lifecycle集合不被改写

#### Scenario: 假账号与隔离
- **WHEN** 目标只有events而不在Inventory，或目标事件属于其它Node/账号/NULL身份
- **THEN** event-only目标404，其它Node/账号及unresolved事件不进入目标结果

#### Scenario: Gate故障与恢复
- **WHEN** Inventory read或DB失败后恢复
- **THEN** 失败503而非empty；重试只读取恢复后的真实数据
