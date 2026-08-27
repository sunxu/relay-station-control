# account-inventory-poll-run Specification Delta

## MODIFIED Requirements

### Requirement: finalize MUST 原子保存固定策略的完整聚合结果

Control SHALL 在一个 fenced PostgreSQL 事务中保存 poll run、其固定策略中全部 active Provider 的聚合结果、节点内重复证据、允许的 Provider snapshot promotion 和对应账号 lifecycle 转换，并 MUST 使用数据库时间生成 `observed_at`。结果集合 MUST 与 pinned policy 完全相等。事务 MUST 锁定当前 policy binding：版本变化时只保存采集证据并以 `policy_changed` 跳过所有 promotion/lifecycle；版本未变化时只为完整 runtime Provider 原子写 snapshot items、更新其当前指针、推进 lifecycle 并设置 `promotion_applied=true`。任一检查/写入失败 MUST 整体回滚。

#### Scenario: 多 Provider 中一个不完整
- **WHEN** contract-valid runtime 观察中一个 active Provider 缓存缺 email/重复，而另一个 active Provider 完整
- **THEN** finalized provider rows 只将问题 Provider 标为不完整/degraded且不提升或推进 lifecycle，完整 Provider 保存快照、更新当前指针、推进 lifecycle并 promotion applied

#### Scenario: active Provider 返回零记录
- **WHEN** 合法 runtime 观察对某 active Provider 完整且返回零记录
- **THEN** finalize 为该 Provider 保存完整空范围聚合结果、零条 item，并原子推进其当前指针、现有 active 账号缺失状态与 promotion applied

#### Scenario: poll 创建后策略切换
- **WHEN** poll run 固定旧策略后当前 binding 已切换到新版本
- **THEN** 本轮 Driver 解析和 provider rows 仍使用旧版本，采集证据 finalized但全部 promotion 以 `policy_changed` 跳过，不按新策略重解释旧响应或推进 lifecycle

#### Scenario: 敏感或逐账号数据进入持久化路径
- **WHEN** Driver observation 包含账号 DTO、email、endpoint、Secret 元数据或原始错误上下文
- **THEN** poll 表只保存固定枚举与聚合值；只有合法完整 Provider 的标准化 email/account key 和字段白名单进入受保护 snapshot/duplicate/lifecycle 表，其他内容不落库

### Requirement: 本 foundation SHALL 推进当前账号状态但不实现产品界面

Poll-run 与 snapshot foundation SHALL 在同一 fenced finalize 中保存完整 Provider 的字段白名单快照、节点内重复聚合、Provider 当前来源和 `present|suspected_missing|missing|out_of_scope` 当前生命周期；只有 promotion-applied Provider MAY 改变 lifecycle，其他观察 MUST NOT 推进或清零连续缺失。它们 MUST NOT 实现日级摘要、趋势、压缩、覆盖率或告警，也 MUST NOT 新增产品 OpenAPI/UI 或人工补采/promotion/状态修改入口，并 MUST NOT 修改 Gateway/Node 或调用任何管理写路径。

#### Scenario: poll run 成功且 Provider 完整
- **WHEN** 一轮 runtime 观察中 active Provider 完整、策略未变化并 finalized
- **THEN** Control 原子保存该 Provider 快照、当前指针和 lifecycle 转换，但不计算产品趋势、摘要或告警

#### Scenario: poll run 失败或 Provider 未提升
- **WHEN** transport/contract 失败、disk fallback、Provider 不完整、policy_changed 或 stale_poll finalized
- **THEN** 既有账号 lifecycle 和缺失计数保持原值，且同槽不为状态推进额外请求 Node

#### Scenario: 管理员访问现有 Control 页面
- **WHEN** lifecycle foundation 部署后管理员使用现有 API/UI
- **THEN** 现有契约保持兼容，不出现账号列表、创建、重试、补采、promotion、状态修改或删除入口

#### Scenario: 网络调用范围检查
- **WHEN** Scheduler、Worker、Reconciler、snapshot 和 lifecycle 验收运行
- **THEN** 除固定 Node Driver 账号清单只读 GET 外不调用 Gateway、Node 写接口、模型数据面或任意未登记目标

## RENAMED Requirements

- FROM: `### Requirement: 本 foundation MUST 不提前实现账号状态或产品界面`
- TO: `### Requirement: 本 foundation SHALL 推进当前账号状态但不实现产品界面`
