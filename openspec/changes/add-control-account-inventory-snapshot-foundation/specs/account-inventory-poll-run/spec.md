## MODIFIED Requirements

### Requirement: Node 失败 MUST 作为 finalized 观察而非隐藏重试

Control MUST 将 Driver 已返回的 transport、HTTP、contract、mode 和 identity 结果作为该槽观察。Node 调用失败或非 200 MUST 保存 `transport_success=false`、`contract_valid=false` 并 finalized；HTTP 200 但响应契约无效 MUST 保存 transport 成功、contract 失败并 finalized。poll service MUST NOT 在同槽自动重试已形成的 Node 失败观察。任何 transport/contract 失败、disk fallback 或 Provider 不完整 MUST 同时 `promotion_applied=false`，不得写 snapshot items或更新 Provider 当前指针。

#### Scenario: HTTP 非 200 或网络失败
- **WHEN** Driver 返回固定 transport 失败观察且 Control 可提交数据库
- **THEN** poll run 以 `transport_success=false`、`contract_valid=false` finalized，全部 Provider 不提升且同槽不因该失败进入 retry_wait

#### Scenario: HTTP 200 但契约无效
- **WHEN** auth-files 响应 JSON/shape/mode 无效或超过既有限制
- **THEN** poll run 以 transport 成功、contract 失败、空 inventory mode finalized，不保存原始 body、部分账号或 snapshot items

#### Scenario: disk fallback 或 Provider identity 不完整
- **WHEN** Driver 返回 contract-valid disk fallback、缺 provider、缺 email或节点内重复分类
- **THEN** poll run 和固定 active Provider 的完整性/degraded 聚合结果 finalized，受影响 Provider promotion false且旧当前指针不变

#### Scenario: finalize 事务失败
- **WHEN** Driver 已返回但数据库在 provider results、duplicates、snapshot items、Provider pointer 与 poll result 完整提交前失败
- **THEN** 整个 finalize 回滚，run 保持可由 lease 恢复的非终态，数据库中不存在部分 finalized 或部分 promotion 证据

### Requirement: finalize MUST 原子保存固定策略的完整聚合结果

Control SHALL 在一个 fenced PostgreSQL 事务中保存 poll run、其固定策略中全部 active Provider 的聚合结果、节点内重复证据和允许的 Provider snapshot promotion，并 MUST 使用数据库时间生成 `observed_at`。结果集合 MUST 与 pinned policy 完全相等。事务 MUST 锁定当前 policy binding：版本变化时只保存采集证据并以 `policy_changed` 跳过所有 promotion；版本未变化时只为完整 runtime Provider 原子写 snapshot items、更新其当前指针并设置 `promotion_applied=true`。任一检查/写入失败 MUST 整体回滚。

#### Scenario: 多 Provider 中一个不完整
- **WHEN** contract-valid runtime 观察中一个 active Provider 缓存缺 email/重复，而另一个 active Provider 完整
- **THEN** finalized provider rows 只将问题 Provider 标为不完整/degraded且不提升，完整 Provider 保存快照、更新当前指针并 promotion applied

#### Scenario: active Provider 返回零记录
- **WHEN** 合法 runtime 观察对某 active Provider 完整且返回零记录
- **THEN** finalize 为该 Provider 保存完整空范围聚合结果、零条 item，并原子推进其当前指针与 promotion applied

#### Scenario: poll 创建后策略切换
- **WHEN** poll run 固定旧策略后当前 binding 已切换到新版本
- **THEN** 本轮 Driver 解析和 provider rows 仍使用旧版本，采集证据 finalized但全部 promotion 以 `policy_changed` 跳过，不按新策略重解释旧响应

#### Scenario: 敏感或逐账号数据进入持久化路径
- **WHEN** Driver observation 包含账号 DTO、email、endpoint、Secret 元数据或原始错误上下文
- **THEN** poll 表只保存固定枚举与聚合值；只有合法完整 Provider 的标准化 email/account key 和字段白名单进入受保护 snapshot/duplicate 表，其他内容不落库

### Requirement: PostgreSQL SHALL 强制 poll-run 状态与时间不变量

PostgreSQL SHALL 强制五分钟 `scheduled_at`、唯一 Node/槽、封闭状态、attempt/lease/finalized/abandoned 字段组合、Provider 唯一性、promotion 字段组合和终态不可逆。`promotion_applied=true` MUST 只属于 finalized、contract-valid、runtime、snapshot-complete Provider，并与 snapshot items/Provider 当前指针在同一受控 finalize 中形成。运行时角色 MUST 只拥有调度、认领、恢复、finalize 和只读指标所需最小权限，不得直接删除历史或绕过状态转换。

#### Scenario: 非固定槽或重复 Node/槽写入
- **WHEN** 写入未对齐五分钟的 `scheduled_at` 或第二条相同 `(instance_id, scheduled_at)`
- **THEN** 数据库拒绝非法时间，重复调度只通过幂等路径取得既有 run

#### Scenario: abandoned 伪造 Node 结果
- **WHEN** 写入尝试为 abandoned run 设置 observed/transport/contract/mode、provider rows、snapshot items 或 promotion applied
- **THEN** 数据库约束/受控 finalize 函数拒绝该状态组合

#### Scenario: finalized Provider 集不完整
- **WHEN** finalize 缺少 pinned active Provider、包含额外/重复 Provider，或 applied 标记与 snapshot/current pointer 不一致
- **THEN** finalize 失败并整体回滚，poll run 不进入 finalized

#### Scenario: 运行时尝试删除或直接改终态
- **WHEN** Control 运行时角色直接 DELETE poll run/snapshot 或 UPDATE finalized/promotion/current pointer
- **THEN** 最小权限和状态保护拒绝操作，历史及当前来源证据保持不变

### Requirement: poll-run 观测 MUST 有界且不泄露账号或凭证

Control SHALL 暴露固定 poll state、scheduler lag、queue wait、poll start lag、transport、contract、Provider snapshot-complete 和 promotion applied/skipped 指标。标签 MUST 仅使用受控 `instance_id`、状态、Provider 和固定 reason/mode；日志与验收证据 MUST 脱敏。poll-run ID、policy version、email/account key、endpoint/IP、Secret/Management Key、响应内容、版本/提交和原始错误 MUST NOT 成为指标标签或非受控输出。

#### Scenario: 进程重启后导出延迟指标
- **WHEN** Control 在 poll 创建、运行或 promotion 后重启
- **THEN** queue/start/scheduler lag 和 Provider promotion 从 PostgreSQL 持久时间与结果恢复，不从内存重新计时或猜测 applied

#### Scenario: finalized Node 失败影响 scheduler lag
- **WHEN** 最近槽以 transport/contract 失败证据 finalized且 promotion skipped
- **THEN** scheduler lag 将该槽视为调度已完成，具体失败与未提升由 transport/contract/promotion 指标分别表达

#### Scenario: canary 注入全部失败路径
- **WHEN** 测试向 endpoint、Secret、email/account key、原始错误和响应字段注入唯一 canary
- **THEN** 除受保护 snapshot/duplicate 身份列外，数据库其他列、指标、日志、错误、测试报告和 acceptance artifact 均不包含敏感 canary

#### Scenario: 真实 Node 验收请求
- **WHEN** 对阶段 0 受控真实测试 Node 执行 snapshot/promotion 验收
- **THEN** 管理请求全局串行，成功或失败后以及最后一次请求后均等待至少 10 秒，证据只保留脱敏状态、聚合计数和固定 promotion 分类

### Requirement: 本 foundation MUST 不提前实现账号状态或产品界面

Poll-run 与 snapshot foundation MAY 在同一 fenced finalize 中保存完整 Provider 的字段白名单快照、节点内重复聚合和 Provider 当前来源，但 MUST NOT 推进账号 `present|suspected_missing|missing|out_of_scope` 生命周期、连续缺失、日级摘要、压缩、覆盖率或告警，也 MUST NOT 新增产品 OpenAPI/UI 或人工补采/promotion 入口。它们 MUST NOT 修改 Gateway/Node 或调用任何管理写路径。

#### Scenario: poll run 成功且 Provider 完整
- **WHEN** 一轮 runtime 观察中 active Provider 完整、策略未变化并 finalized
- **THEN** Control 可原子保存该 Provider 快照与当前指针，但不更新账号生命周期、不计算 missing、不产生告警

#### Scenario: 管理员访问现有 Control 页面
- **WHEN** snapshot foundation 部署后管理员使用现有 API/UI
- **THEN** 现有契约保持兼容，不出现账号列表、创建、重试、补采、promotion 或删除入口

#### Scenario: 网络调用范围检查
- **WHEN** Scheduler、Worker、Reconciler 和 snapshot 验收运行
- **THEN** 除固定 Node Driver 账号清单只读 GET 外不调用 Gateway、Node 写接口、模型数据面或任意未登记目标
