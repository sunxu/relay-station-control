## MODIFIED Requirements

### Requirement: PostgreSQL SHALL 强制 poll-run 状态与时间不变量

PostgreSQL SHALL 强制五分钟 `scheduled_at`、唯一 Node/槽、封闭状态、attempt/lease/finalized/abandoned 字段组合、Provider 唯一性、promotion 字段组合和终态不可逆。`promotion_applied=true` MUST 只属于 finalized、contract-valid、runtime、snapshot-complete Provider，并与 snapshot items/Provider 当前指针在同一受控 finalize 中形成。运行时角色 MUST 只有调度、认领、恢复、finalize、history 受控函数和只读指标所需最小权限；普通路径不得直接删除历史或绕过状态转换，history 路径只可在对应 compaction completed、snapshot items 为空且保留期满足后删除 poll run。

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
- **WHEN** Control 普通运行路径直接 DELETE poll run/snapshot 或 UPDATE finalized/promotion/current pointer
- **THEN** 最小权限和状态保护拒绝操作，只有满足全部 history 前置条件的受控清理函数可删除到期历史

### Requirement: 本 foundation SHALL 推进当前账号状态但不实现产品界面

Poll-run 与 snapshot foundation SHALL 在同一 fenced finalize 中保存完整 Provider 的字段白名单快照、节点内重复聚合、Provider 当前来源和 `present|suspected_missing|missing|out_of_scope` 当前生命周期；只有 promotion-applied Provider MAY 改变 lifecycle，其他观察 MUST NOT 推进或清零连续缺失。独立 history capability MAY 从已提交终态计算策略分段摘要、最终 rollup、覆盖率并受控清理到期历史，但 MUST NOT 重写 poll 结果或当前状态。本 foundation 与 history capability MUST NOT 新增产品 OpenAPI/UI 或人工补采/promotion/状态修改/删除入口，并 MUST NOT 修改 Gateway/Node 或调用任何管理写路径。

#### Scenario: poll run 成功且 Provider 完整
- **WHEN** 一轮 runtime 观察中 active Provider 完整、策略未变化并 finalized
- **THEN** Control 原子保存该 Provider 快照、当前指针和 lifecycle 转换，历史摘要只在完整 UTC 日达到资格后异步形成

#### Scenario: poll run 失败或 Provider 未提升
- **WHEN** transport/contract 失败、disk fallback、Provider 不完整、policy_changed 或 stale_poll finalized
- **THEN** 既有账号 lifecycle 和缺失计数保持原值，该槽可以进入历史分母/失败计数但不得进入 promotion 分子

#### Scenario: 管理员访问现有 Control 页面
- **WHEN** history compaction 部署后管理员使用现有 API/UI
- **THEN** 现有契约保持兼容，不出现历史、创建、重试、补采、promotion、状态修改或删除入口

#### Scenario: 网络调用范围检查
- **WHEN** Scheduler、Worker、Reconciler、snapshot、lifecycle 和 history 验收运行
- **THEN** history 不增加外部调用，既有采集仍除固定 Node Driver 账号清单只读 GET 外不调用 Gateway、Node 写接口、模型数据面或任意未登记目标
