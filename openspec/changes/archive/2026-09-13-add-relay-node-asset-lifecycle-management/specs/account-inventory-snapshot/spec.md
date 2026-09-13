## MODIFIED Requirements

### Requirement: finalize MUST 锁定当前策略并区分 completeness 与 promotion

Control MUST 在现有 poll lease/fencing finalize 事务中锁定 poll run 对应的当前 Provider policy binding，并在持锁后按 PostgreSQL 当前时间从 activation history 取得实际生效版本，与不可变 `provider_policy_version` 比较。binding 指针 MAY 为未来预约 activation 提前更新，因此 MUST NOT 单独作为当前生效版本。`provider_snapshot_complete` MUST 只描述返回内容完整性；`promotion_applied` MUST 只描述该 Provider 的 snapshot items 与当前指针已在本事务生效。两者不得互相替代。

Snapshot promotion MUST include a short Node lifecycle and monitoring-eligibility fence in
addition to poll lease/fencing and policy binding locks. Finalize MUST lock the Node, read one
`database_now`, evaluate Node lifecycle first, and, only while the same Node remains active,
evaluate whether a non-cancelled monitoring activation satisfies `database_now <@ active_range`.
A retired/replaced or active-but-monitoring-ineligible Node may retain immutable transport and
Provider evidence but cannot create or refresh current snapshot pointers. An active Node that is
monitoring-ineligible at this boundary MUST finalize with run and all pinned active Provider
`promotion_skipped_reason=monitoring_ineligible` and Provider `promotion_applied=false`.

#### Scenario: Node lifecycle delta
- **WHEN** the Node lifecycle condition described by this change is evaluated
- **THEN** the existing baseline behavior remains intact and the lifecycle fence is also enforced

#### Scenario: poll 后策略未变化
- **WHEN** finalize 锁定 binding 后数据库当前时间对应的实际生效版本仍等于 poll pinned policy
- **THEN** Control 按 Provider 完整性规则决定 promotion，并在同一事务保存结果、items、指针和 applied 标记

#### Scenario: poll 后策略已经变化
- **WHEN** finalize 持有 binding 锁时实际生效版本不同于 poll pinned policy
- **THEN** Control 保存 poll/provider/duplicate 采集证据，全部 active Provider 标记 `promotion_applied=false` 与 `policy_changed`，不写 snapshot items、不更新当前指针，也不按新策略重解释旧观察

#### Scenario: 新策略已预约但尚未生效
- **WHEN** binding 指针已指向未来 activation，但数据库当前时间仍落在 poll pinned policy 的 active range
- **THEN** Control 仍按当前实际生效的旧策略执行 Provider promotion，不提前标记 `policy_changed`

#### Scenario: finalize 与策略切换并发
- **WHEN** finalize 和策略切换同时竞争同一 binding 行
- **THEN** 数据库锁只允许得到“旧策略先完整 promotion”或“新策略先切换且旧 poll 跳过 promotion”两种原子结果，不出现混合 Provider 版本

#### Scenario: active Node 在 finalize 时 monitoring ineligible
- **WHEN** finalize 持有 Node 锁并读取的同一 `database_now` 下 Node 仍为 active，但不存在
  `cancelled_at IS NULL AND database_now <@ active_range` 的 monitoring activation
- **THEN** run 保持 `finalized`，run 与全部 pinned active Provider 以
  `monitoring_ineligible` 跳过 promotion，保留 transport/Provider historical evidence，且不写
  snapshot items、Provider current pointer 或任何 account current truth

#### Scenario: monitoring writer 与 finalize 按 Node 锁串行
- **WHEN** monitoring writer 与 finalize 并发竞争同一 Node 锁
- **THEN** finalize 先取得锁且当时 eligible 时可按正常规则完成；monitoring writer 先提交使
  Node 在 finalize 的 `database_now` 下不再 eligible 时，finalize 必须记录
  `monitoring_ineligible` 并执行 evidence-only finalize

### Requirement: 完整 active Provider SHALL 独立推进当前快照

当 Node active、monitoring eligible 且策略版本未变化时，每个 `provider_snapshot_complete=true` 且 inventory mode 为 runtime 的 active Provider SHALL 独立写入全部去重 items，更新 `(instance_id, provider)` 当前快照指针和来源元数据，并设置 `promotion_applied=true`。同 Node 其他 Provider 不完整或 Node 汇总 degraded MUST NOT 阻止该 Provider。Provider 当前指针 MUST 只前进到更晚槽，不得由迟到旧 poll 倒退。

Control MUST only promote a complete Provider when the Node is active and has a current eligible monitoring interval.
The Provider identity remains bound to the original `instance_id`; it is never silently moved to
the replacement Node.

#### Scenario: Node lifecycle delta
- **WHEN** the Node lifecycle condition described by this change is evaluated
- **THEN** the existing baseline behavior remains intact and the lifecycle fence is also enforced

#### Scenario: 两个 Provider 只有一个完整
- **WHEN** Provider A 完整而 Provider B 因缺 identity 或重复不完整
- **THEN** A 写入 items 并更新当前指针，B 不写 items且保留旧指针；二者分别记录 applied 与固定 skipped reason

#### Scenario: active Provider 合法返回零账号
- **WHEN** runtime 响应对某 active Provider 完整但集合为空
- **THEN** Control 写入零条 item，仍将 Provider 当前指针推进到本 poll 并设置 promotion applied，表示完整观察到空集合

#### Scenario: 迟到旧槽试图覆盖新指针
- **WHEN** 较新槽已经成为 Provider 当前快照，较旧 poll 随后到达 finalize/promotion 路径
- **THEN** 较旧 poll 保存采集证据并以 `stale_poll` 标记 promotion false，较新来源元数据和当前快照保持不变且不会产生无界重试

### Requirement: snapshot promotion SHALL 与 poll finalize 原子且受 fencing 保护

Poll 聚合、Provider 结果、duplicate evidence、snapshot items、Provider state 指针、promotion 标记、对应账号 lifecycle 转换和 `status=finalized` MUST 在一个 PostgreSQL 事务中提交。只有持有当前未过期 lease/fencing token 的 Worker可以执行。任一验证、锁、写入或提交失败 MUST 全部回滚；不得通过第二任务、Outbox 或进程内队列延后补做 promotion 或 lifecycle。

Node lifecycle, poll fencing, snapshot items, Provider pointer and account lifecycle checks MUST
be evaluated in the same short PostgreSQL finalize boundary. Failure of any fence prevents all
current promotion writes while retaining allowed immutable evidence.

#### Scenario: Node lifecycle delta
- **WHEN** the Node lifecycle condition described by this change is evaluated
- **THEN** the existing baseline behavior remains intact and the lifecycle fence is also enforced

#### Scenario: snapshot 批量写入中数据库失败
- **WHEN** 事务在部分 item、账号状态、Provider 指针或 promotion 标记写入后失败
- **THEN** 全部变化回滚，poll 保持可由原 lease 恢复的非终态，旧 lifecycle 和当前指针继续有效

#### Scenario: 旧 Worker 在恢复后提交
- **WHEN** Reconciler 已替换 fencing 或 poll 已进入终态，旧 Worker 携带内存 snapshot candidates finalize
- **THEN** fenced finalize 影响零行，旧 candidates 被丢弃且不能追加 item、推进 lifecycle 或覆盖 Provider state

#### Scenario: 提交结果未知后重试
- **WHEN** Control 在数据库 COMMIT 响应前后失联并按同 poll 恢复
- **THEN** 唯一键、状态和 fencing 使结果保持单份；已提交终态不重复写或推进 lifecycle，未提交事务可在 grace/attempt 内整体重做

### Requirement: Provider 当前快照状态 SHALL 独立且可在历史清理后解释

`account_inventory_provider_states` SHALL以`(instance_id, provider)`唯一保存当前poll来源、最近完整时间、来源observed/node version/commit，以及最近合格finalized Provider结果的scheduled time、degraded布尔值和固定原因。不同Provider MUST拥有独立指针。只有Provider state已由一次合格promotion建立，且新结果在finalize持锁后仍属于当前active策略、slot严格晚于已保存health、不是`policy_changed|stale_poll|monitoring_ineligible|node_retired|node_replaced`且Provider未out-of-scope时，Control SHALL原子刷新health；从未promotion的Provider不创建缺少current来源的state。只有合格promotion才推进current snapshot指针和最近完整来源。`current_poll_run_id` MAY在`account-inventory-history-compaction`确认对应摘要和保留条件后`ON DELETE SET NULL`，但复制的来源与health元数据MUST保持当前含义；历史压缩MUST NOT从当前指针或残余items重建、倒退或改变Provider state。

Current Provider state MUST include the original Node identity and MUST be operationally eligible
only when that Node is active and monitoring-eligible. Historical state and compaction evidence
remain readable after retirement; they do not make the Node a current target.

`control_refresh_account_inventory_provider_health_v1()`（以及其它等价的
`account_inventory_provider_states` current-health consumer）MUST 把
`promotion_skipped_reason IN (policy_changed, stale_poll, monitoring_ineligible, node_retired, node_replaced)`
一视同仁：命中该 allowlist 的 Provider 结果 MUST NOT 刷新
`account_inventory_provider_states.health_scheduled_at` / `health_degraded` /
`health_reason`，也 MUST NOT 间接推进 availability/request-quality current truth。
`account_inventory_poll_provider_results` 中的 transport/provider evidence 与
history/compaction evidence 仍完整保留、可读；本条只禁止把 lifecycle-skipped
evidence 推进为 current health truth，不删除也不重解释历史证据。

#### Scenario: Node lifecycle delta
- **WHEN** the Node lifecycle condition described by this change is evaluated
- **THEN** the existing baseline behavior remains intact and the lifecycle fence is also enforced

#### Scenario: 一个 Provider 连续多槽提升
- **WHEN** 同一 Provider 的更新完整槽成功 promotion
- **THEN** 当前指针和来源元数据原子前进，旧 snapshot items 在达到压缩资格前保持不可变历史

#### Scenario: 下一槽 Provider 不完整
- **WHEN** Provider已有current state且当前active策略的严格更新槽finalized但该Provider不完整
- **THEN** 当前指针、最近完整时间和来源元数据保持旧值，最近健康字段更新为 degraded/固定原因且 poll/provider 证据记录本槽 skip

#### Scenario: 旧策略或迟到结果不得刷新health
- **WHEN** finalized结果为policy_changed、stale_poll、monitoring_ineligible、node_retired、node_replaced，
  或slot不比现有health新，或Provider已out-of-scope
- **THEN** 结果只保留poll/provider历史证据，不更新health、snapshot pointer或lifecycle

#### Scenario: Node retired 时 finalize 不得刷新 Provider current health
- **WHEN** finalized 结果的 `promotion_skipped_reason = node_retired`
- **THEN** `account_inventory_provider_states.health_scheduled_at` /
  `health_degraded` / `health_reason` 保持不变，transport/provider evidence
  仍写入历史，不间接刷新 availability/request-quality current truth

#### Scenario: monitoring ineligible 时 finalize 不得刷新 Provider current health
- **WHEN** active Node 的 finalized 结果使用 `promotion_skipped_reason=monitoring_ineligible`
- **THEN** `account_inventory_provider_states.health_scheduled_at` / `health_degraded` /
  `health_reason` 保持不变，transport/Provider evidence 仍写入历史，不间接刷新
  availability/request-quality current truth

#### Scenario: Node replaced 时 finalize 不得刷新 Provider current health
- **WHEN** finalized 结果的 `promotion_skipped_reason = node_replaced`
- **THEN** `account_inventory_provider_states.health_scheduled_at` /
  `health_degraded` / `health_reason` 保持不变，transport/provider evidence
  仍写入历史，不间接刷新 availability/request-quality current truth

#### Scenario: 普通 provider-specific skip 原因不受本条影响
- **WHEN** finalized 结果的 `promotion_skipped_reason` 为
  `transport_failed`、`contract_invalid`、`disk_fallback`、
  `provider_identity_incomplete` 或 `provider_duplicate`
- **THEN** 既有 health refresh 语义不变，`control_refresh_account_inventory_provider_health_v1()`
  按原逻辑刷新 health 字段，不因本条新增的 lifecycle allowlist 被误伤

#### Scenario: Provider从未成功promotion
- **WHEN** active Provider尚无current state且一次失败或不完整结果finalized
- **THEN** Control不创建缺少last complete/source的Provider state，失败只保存在poll/provider历史并由后续告警能力消费

#### Scenario: 当前来源 poll 将来被清理
- **WHEN** history compaction 已固化摘要、完成对应 snapshot 删除并在保留期后删除当前来源 poll
- **THEN** Provider state 外键置空但最近完整时间和来源版本/提交等必要元数据不变，不把空外键解释为没有当前快照
