## MODIFIED Requirements

### Requirement: Provider 当前快照状态 SHALL 独立且可在历史清理后解释

`account_inventory_provider_states` SHALL以`(instance_id, provider)`唯一保存当前poll来源、最近完整时间、来源observed/node version/commit，以及最近合格finalized Provider结果的scheduled time、degraded布尔值和固定原因。不同Provider MUST拥有独立指针。只有Provider state已由一次合格promotion建立，且新结果在finalize持锁后仍属于当前active策略、slot严格晚于已保存health、不是`policy_changed|stale_poll`且Provider未out-of-scope时，Control SHALL原子刷新health；从未promotion的Provider不创建缺少current来源的state。只有合格promotion才推进current snapshot指针和最近完整来源。`current_poll_run_id` MAY在`account-inventory-history-compaction`确认对应摘要和保留条件后`ON DELETE SET NULL`，但复制的来源与health元数据MUST保持当前含义；历史压缩MUST NOT从当前指针或残余items重建、倒退或改变Provider state。

#### Scenario: 一个 Provider 连续多槽提升
- **WHEN** 同一 Provider 的更新完整槽成功 promotion
- **THEN** 当前指针和来源元数据原子前进，旧 snapshot items 在达到压缩资格前保持不可变历史

#### Scenario: 下一槽 Provider 不完整
- **WHEN** Provider已有current state且当前active策略的严格更新槽finalized但该Provider不完整
- **THEN** 当前指针、最近完整时间和来源元数据保持旧值，最近健康字段更新为 degraded/固定原因且 poll/provider 证据记录本槽 skip

#### Scenario: 旧策略或迟到结果不得刷新health
- **WHEN** finalized结果为policy_changed、stale_poll、slot不比现有health新，或Provider已out-of-scope
- **THEN** 结果只保留poll/provider历史证据，不更新health、snapshot pointer或lifecycle

#### Scenario: Provider从未成功promotion
- **WHEN** active Provider尚无current state且一次失败或不完整结果finalized
- **THEN** Control不创建缺少last complete/source的Provider state，失败只保存在poll/provider历史并由后续告警能力消费

#### Scenario: 当前来源 poll 将来被清理
- **WHEN** history compaction 已固化摘要、完成对应 snapshot 删除并在保留期后删除当前来源 poll
- **THEN** Provider state 外键置空但最近完整时间和来源版本/提交等必要元数据不变，不把空外键解释为没有当前快照

### Requirement: 本 snapshot foundation SHALL 只在合格 promotion 推进当前账号生命周期

Snapshot foundation SHALL 继续形成不可变 snapshot items、节点内重复证据和 Provider 当前来源，并在同一 fenced finalize 内只为 `promotion_applied=true` 的完整 runtime Provider 推进 `account_inventory` 当前生命周期。它 MUST NOT 从未提升观察或历史快照计算当前状态；独立 `account-inventory-history-compaction` capability MAY 从已提交历史生成不可变日级摘要和覆盖率并在资格/校验满足后受控删除历史，但 MUST NOT 改变 promotion 或 lifecycle。Snapshot/lifecycle 本身不生成趋势页面、告警或产品 mutation，也不修改 Gateway/Node 或调用写接口。

#### Scenario: 新完整快照缺少旧账号
- **WHEN** Provider 新快照完整、策略未变化且 promotion applied，并没有某个现有 present 账号
- **THEN** finalize 在同一事务提升快照并按连续完整缺失规则推进该账号，history runner 不参与本次当前状态判定

#### Scenario: 新观察未获 promotion
- **WHEN** Provider 因失败、不完整、disk fallback、policy_changed 或 stale_poll 未提升
- **THEN** snapshot 当前来源和账号 lifecycle 都保持原值，日级覆盖率不得把该结果计入 promotion 分子

#### Scenario: 管理员使用现有 Control 页面
- **WHEN** history compaction 部署后管理员访问现有 API/UI
- **THEN** 现有 current 账号契约保持兼容，不出现历史、人工 promotion、重试或删除入口

#### Scenario: 网络调用范围检查
- **WHEN** snapshot/promotion/lifecycle/history 验收运行
- **THEN** history 不增加 Probe、Gateway、模型数据面、Node 写接口或任意外部请求，既有 poll 仍只使用固定账号清单只读 GET
