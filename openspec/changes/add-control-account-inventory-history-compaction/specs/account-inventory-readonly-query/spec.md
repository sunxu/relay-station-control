## MODIFIED Requirements

### Requirement: query response MUST 使用最小 current-state 字段白名单

每个item SHALL只包含instance、Provider、normalized email、最后报告basic status、lifecycle/count、first/last seen、missing/out-of-scope时间、last refresh、next retry、source updated、Provider last complete、Provider degraded和`fresh|stale|out_of_scope` freshness。Provider degraded MUST来自`account_inventory_provider_states`中最近符合当前策略、单调slot和scope门禁的finalized Provider健康字段；current poll外键被合法history retention置空时仍以冗余来源/健康字段返回，不得依赖已清理的poll/provider result。API MUST NOT返回account key、poll/policy ID、Node版本/提交、原始累计计数/桶、endpoint、Secret引用、未知字段或原始错误。

#### Scenario: suspected 或 missing 账号仍有旧基础状态
- **WHEN** lifecycle账号未在最新完整快照出现但保留最后报告 basic status
- **THEN** API/UI明确标为“最后报告基础状态”，不得解释为当前可调度或当前 Node已报告

#### Scenario: Provider 当前降级但旧快照尚新鲜
- **WHEN** 最近一次轮询不完整使 Provider current health 为 degraded，但 last complete仍未超过15分钟
- **THEN** 响应同时返回 degraded和fresh，两种语义不互相覆盖

#### Scenario: current source早于最近health结果
- **WHEN** current poll仍指向上次成功promotion，而严格更新的当前策略poll仅刷新Provider degraded health
- **THEN** query使用较新的冗余health和既有last complete，既不从旧provider result覆盖health也不移动snapshot pointer

#### Scenario: out-of-scope 账号
- **WHEN** lifecycle为out_of_scope
- **THEN** freshness固定为out_of_scope并返回out_of_scope_since，不伪造fresh/stale或参与active状态解释

#### Scenario: current source poll 已合法清理
- **WHEN** history retention删除到期poll使Provider/current account来源外键置空，但冗余来源时间与健康字段完整
- **THEN** query继续返回相同current产品投影，不返回503、不猜测poll ID且不要求恢复历史行

#### Scenario: lifecycle与Provider state不一致
- **WHEN** 数据库缺少必要Provider state、last complete、健康字段或出现非法状态组合
- **THEN** Control以固定503 fail closed，不返回默认填充或部分账号页
