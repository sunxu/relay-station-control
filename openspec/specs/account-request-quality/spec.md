# account-request-quality Specification

## Purpose
在不修改 CLIProxyAPI、且只依赖 HTTP usage queue 的前提下，为 Relay Station 已有账号身份提供最小请求事件存储和短时间窗口质量统计，明确未知数据、错误与重复处理边界，不扩展为配额或自动控制平台。

## Requirements

### Requirement: 唯一 HTTP 请求事件来源

Control SHALL 仅消费 CLIProxy `/v0/management/usage-queue`，批量100、正常间隔1s、有限请求超时、最大退避30s；每个 Node MUST 只有一个消费者，取消 SHALL 终止拉取。不得 fallback 至RESP/Subscribe或修改Node。

#### Scenario: 正常与空队列
- **WHEN** 返回有效对象/JSON字符串数组或空数组
- **THEN** 分别处理批次或等待下一轮，空队列不制造事件

#### Scenario: 非法事件或 unsupported
- **WHEN** payload malformed或endpoint unsupported
- **THEN** 返回明确采集错误而不是空数据，不访问其他source；malformed单项计数后保留同批其它合法事件

#### Scenario: 取消
- **WHEN** runtime context取消
- **THEN** 停止HTTP请求与退避等待，不开始下一次pop

### Requirement: Unproven Account Identity Must Remain Unresolved

Control MUST 正确归属每个能够证明身份的事件（Every provably attributable event is attributed correctly），而非要求每个合法事件都有account_key。直接provider/email沿用既有canonicalization；auth_index只作为当前Node management快照lookup。只有exactly-one不同account_key且没有冲突才resolved，其余持久化account_key=NULL。MUST NOT使用latest/first wins、任意fallback或当前账号覆盖无法证明的历史事件，不新增temporal identity subsystem。

#### Scenario: Direct identity
- **WHEN** event提供足够provider/email且无冲突
- **THEN** 归一化为既有account_key，resolved

#### Scenario: Unique auth_index
- **WHEN** event只有auth_index且当前同Node快照证明exactly-one不同account_key
- **THEN** 关联该account_key；重复相同映射不制造歧义

#### Scenario: Missing lookup
- **WHEN** auth_index缺失或lookup读取失败/不存在且无独立direct identity
- **THEN** 保留事件且account_key=NULL

#### Scenario: Conflicting lookup
- **WHEN** 同auth_index对应多个不同account_key，或当前snapshot与event身份/provider证据冲突
- **THEN** 保留事件且account_key=NULL，不选first/latest

#### Scenario: Deleted account
- **WHEN** account已删除、当前lookup缺失且event只有auth_index
- **THEN** unresolved，不重建历史assignment

#### Scenario: Unresolved account exclusion
- **WHEN** 查询某个account_key质量
- **THEN** NULL身份事件不得出现在该账号计数/失败率/latency中

#### Scenario: Unresolved Node and Provider inclusion
- **WHEN** 查询Node或Provider质量且包含unresolved失败事件
- **THEN** 计入请求/失败/latency及unresolved_request_count，不伪装无请求

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

Account Quality查询 SHALL 按node_id+非NULL account_key返回15m或1h的request_count、success_count、failure_count、success_rate、p95_latency_ms、last_success_at、last_failure_at、last_failure_class；窗口使用DB UTC时间，p95为有效duration的percentile_cont(0.95)。只补Node/Provider同字段聚合与unresolved_request_count，不扩展UI、完整Node quality产品、quota或自动行为。

#### Scenario: 两个窗口与 p95
- **WHEN** 存在窗口内外及不同账号事件
- **THEN** 两个窗口分别只统计目标账号窗口内事件，比例和p95与fixture一致

#### Scenario: 无请求
- **WHEN** 窗口内没有请求
- **THEN** 计数为0、比例和p95及最后事件字段为NULL，语义unknown而非健康

#### Scenario: 查询不可用
- **WHEN** PostgreSQL查询失败
- **THEN** 返回错误，不返回零请求或空成功结果
