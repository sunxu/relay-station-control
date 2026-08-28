## MODIFIED Requirements

### Requirement: Provider 策略绑定和激活历史一致

Control SHALL为每个策略作用域保存唯一作用域绑定及半开区间`[effective_from, effective_to)`的激活历史。作用域绑定MUST指向同一作用域最新登记选择的现有策略版本，并作为并发变更的串行化边界；当前有效策略MUST只由数据库UTC当前时间命中的激活区间确定。同一作用域的区间MUST NOT重叠，生效时间MUST使用数据库UTC当前时间或未来时间且不得回填过去时间。已参与或仍可能参与poll、history eligibility、segment、rollup、compaction或保留期证明的策略版本与激活历史MUST NOT被删除或追溯改写；正常关闭当前open interval只可写数据库当前/未来结束时间，且不得改变已经结束UTC日的历史交集。

#### Scenario: 读取当前策略
- **WHEN** 某作用域存在覆盖数据库当前时间的激活区间
- **THEN** 只读接口返回该区间引用的策略版本及其 active、out-of-scope 集合和生效时间，即使作用域绑定已指向一个未来生效版本

#### Scenario: 没有当前策略
- **WHEN** 某 Node 作用域没有覆盖数据库当前时间的激活区间
- **THEN** 只读接口明确返回未配置状态，不回退到其他 Node 类型、Driver 版本或历史策略

#### Scenario: 重叠或回填激活区间
- **WHEN** 受控部署流程尝试写入与既有区间重叠或早于数据库当前时间的新激活区间
- **THEN** 数据库拒绝写入并保留原绑定和历史

#### Scenario: 绑定跨越作用域
- **WHEN** 作用域绑定引用不同环境、Node 类型或 Driver 合约版本的策略
- **THEN** 数据库拒绝该绑定

#### Scenario: history引用期间删除或追溯改写
- **WHEN** 调用尝试删除仍被poll/summary/rollup/run/retention引用的策略版本或激活行，或改变已结束日的区间边界
- **THEN** PostgreSQL拒绝操作，既有expected slot、segment checksum和coverage语义保持不变

### Requirement: Node 账号监控状态源自显式激活区间

Control SHALL为每个Node保存半开区间`[effective_from, effective_to)`的账号清单监控激活历史，并以数据库UTC当前时间是否落在区间内计算当前状态。启用记录MUST保存固定启用reason、实名actor和数据库创建时间；关闭或预约关闭MUST另存固定关闭reason、实名actor和数据库登记时间。同一Node的区间MUST NOT重叠，新区间不得从过去开始；Gateway状态、Compose状态或Node可达性MUST NOT隐式改变监控状态。仍参与history expected slot、segment、rollup、compaction或保留期证明的激活历史MUST NOT被删除或追溯改写；关闭当前open interval不得改变已经结束UTC日的历史交集。

#### Scenario: 当前处于监控区间
- **WHEN** 数据库当前时间落在某 Node 的一个监控激活区间内
- **THEN** 资产接口将该 Node 报告为 `monitoring_active: true` 并返回区间边界

#### Scenario: 未配置或不在监控区间
- **WHEN** Node 没有激活区间，或当前时间不在任何区间内
- **THEN** 资产接口将该 Node 报告为 `monitoring_active: false`

#### Scenario: 外部运行状态变化
- **WHEN** Gateway、Compose 或 Node 的运行状态发生变化但数据库激活区间未变化
- **THEN** Control 报告的账号监控状态保持不变

#### Scenario: 重叠或回填监控区间
- **WHEN** 受控部署流程尝试写入重叠区间或从数据库当前时间之前开始的区间
- **THEN** 数据库拒绝写入且不修改既有历史

#### Scenario: 关闭操作保留实名元数据
- **WHEN** 受控部署操作立即或预约关闭一个监控区间
- **THEN** 同一历史行保存关闭 reason、actor 和数据库登记时间，NULL、启停 reason 错配或冲突重放均被拒绝

#### Scenario: coverage保留期内改变监控历史
- **WHEN** 调用尝试删除或追溯改写仍可能影响expected slot或已固化coverage的Node监控区间
- **THEN** PostgreSQL拒绝操作，正常未来启停仍通过既有受控路径追加或关闭区间
