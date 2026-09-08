## Purpose

在不修改 CLIProxyAPI、且只依赖 HTTP usage queue 的前提下，为 Relay Station 已有账号身份提供最小请求事件存储和短时间窗口质量统计，明确未知数据、错误与重复处理边界，不扩展为配额或自动控制平台。

## ADDED Requirements

### Requirement: 唯一 HTTP 请求事件来源

Control SHALL 仅消费 CLIProxy `/v0/management/usage-queue`，批量100、正常间隔1s、有限请求超时、最大退避30s；每个 Node MUST 只有一个消费者，取消 SHALL 终止拉取。不得 fallback 至RESP/Subscribe或修改Node。

#### Scenario: 正常与空队列
- **WHEN** 返回有效对象/JSON字符串数组或空数组
- **THEN** 分别处理批次或等待下一轮，空队列不制造事件

#### Scenario: 非法事件或 unsupported
- **WHEN** payload malformed或endpoint unsupported
- **THEN** 返回明确采集错误而不是空数据，不访问其他source

#### Scenario: 取消
- **WHEN** runtime context取消
- **THEN** 停止HTTP请求与退避等待，不开始下一次pop

### Requirement: 既有 canonical account identity

事件 MUST 可靠映射到既有规范化provider:email account_key，auth_index只能作lookup key。无法证明映射时 MUST 阻止完整采集验收，不得猜测归属或以跳过事件宣称闭环通过。

#### Scenario: 明确身份
- **WHEN** 事件带有可证明的provider和email
- **THEN** 沿用现有规范化得到同一account_key

#### Scenario: 只有 auth_index
- **WHEN** 事件没有可靠email且现有read model没有唯一映射
- **THEN** 报告identity blocker，不生成伪造account_key

### Requirement: 最小事件与固定失败类别

Control SHALL 只持久化event_hash、可选request_id、node_id、provider、account_key、model、occurred_at、duration_ms、success、failure_class。失败类别 MUST 限于auth/quota/rate_limit/upstream/unknown；成功类别为NULL。

#### Scenario: 成功与五类失败
- **WHEN** 输入成功、认证错误、明确quota错误、429、5xx或未明确错误
- **THEN** 分别为成功/NULL、auth、quota、rate_limit、upstream、unknown

### Requirement: 幂等 PostgreSQL 与七天保留

Control MUST 使用PostgreSQL additive append-only事件表，按node_id+event_hash幂等插入；仅七天到期事件可由retention删除，不使用SQLite、partition或rollup。DB写失败 MUST 保留当前内存批次重试而不是继续pop；source已弹出但尚未提交的崩溃窗口不能被描述成可重放。

#### Scenario: 重复与重启
- **WHEN** 相同Node和hash重复插入或collector重启后重放已提交事件
- **THEN** 数据库只保留一条，统计不重复

#### Scenario: 数据库失败
- **WHEN** 批次提交失败
- **THEN** 报错并重试原批次，不将失败当成成功或空队列

#### Scenario: 保留期限
- **WHEN** DB时间表明事件早于7天
- **THEN** 最小retention操作可删除到期事件，窗口内事件保持不变

### Requirement: 单账号窗口质量查询

查询 SHALL 按node_id+account_key返回15m或1h的request_count、success_count、failure_count、success_rate、p95_latency_ms、last_success_at、last_failure_at、last_failure_class；窗口使用DB UTC时间，p95为有效duration的percentile_cont(0.95)。不扩展UI、Node quality、quota或自动行为。

#### Scenario: 两个窗口与 p95
- **WHEN** 存在窗口内外及不同账号事件
- **THEN** 两个窗口分别只统计目标账号窗口内事件，比例和p95与fixture一致

#### Scenario: 无请求
- **WHEN** 窗口内没有请求
- **THEN** 计数为0、比例和p95及最后事件字段为NULL，语义unknown而非健康

#### Scenario: 查询不可用
- **WHEN** PostgreSQL查询失败
- **THEN** 返回错误，不返回零请求或空成功结果
