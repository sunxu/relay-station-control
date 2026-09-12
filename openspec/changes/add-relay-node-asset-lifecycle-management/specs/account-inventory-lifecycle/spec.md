## MODIFIED Requirements

### Requirement: 只有 promotion-applied runtime Provider SHALL 推进生命周期

只有当前 poll 对某 active Provider 同时满足 contract-valid runtime、provider snapshot complete、策略未变化、槽位较新且 `promotion_applied=true` 时，Control SHALL 使用该 Provider 的完整 snapshot item 集推进生命周期。transport/HTTP/contract 失败、disk fallback、Provider 不完整、重复身份、`policy_changed`、`stale_poll`、pending/retry/running/abandoned 槽、Gateway/Compose 状态和未提交事务 MUST NOT 增加、清零或重解释任何账号缺失状态。

Promotion MUST additionally prove the Node is active, retains the same stable identity and has a
current eligible monitoring interval. Retired/replaced old Nodes cannot advance
`account_inventory` lifecycle or be retargeted to a replacement identity.

#### Scenario: Node lifecycle delta
- **WHEN** the Node lifecycle condition described by this change is evaluated
- **THEN** the existing baseline behavior remains intact and the lifecycle fence is also enforced

#### Scenario: disk fallback 或 Provider 不完整
- **WHEN** poll finalized 但某 Provider 未形成 promotion-applied runtime 快照
- **THEN** 该 Provider 全部 lifecycle 行保持原值，既不累计 missing 也不把已有 missing 恢复为 present

#### Scenario: 合法完整空集合
- **WHEN** active Provider 的完整 runtime 空集合成功 promotion
- **THEN** Control 将该 Provider 现有 active lifecycle 行按连续缺失规则推进，而不是把空集合当作无证据

#### Scenario: 较旧 poll 迟到
- **WHEN** 较新槽已经推进 Provider 当前来源和 lifecycle，较旧 poll 随后 finalize
- **THEN** 较旧 poll 以 stale_poll 跳过 promotion且 lifecycle 完全不变

### Requirement: lifecycle promotion MUST 原子、fenced、幂等且单调

Snapshot items、Provider 当前指针、promotion 标记、lifecycle 转换和 poll finalized MUST 在同一个 PostgreSQL 事务中提交，并只允许当前未过期 lease/fencing token 执行。实现 MUST 以 Provider/账号稳定顺序取得所需锁；同一 poll 的提交未知恢复、旧 Worker、重复 finalize 或并发策略切换 MUST NOT 重复增加 missing、倒退来源或形成混合状态。任一检查、写入或提交失败 MUST 全部回滚。

Lifecycle promotion MUST use the short Node lock and poll fencing token. Restart, stale workers,
replacement and duplicate finalize attempts cannot revive old current truth or advance a new
identity.

#### Scenario: Node lifecycle delta
- **WHEN** the Node lifecycle condition described by this change is evaluated
- **THEN** the existing baseline behavior remains intact and the lifecycle fence is also enforced

#### Scenario: lifecycle 批量转换中数据库失败
- **WHEN** 事务已更新部分账号后发生约束、连接或提交失败
- **THEN** lifecycle、snapshot、Provider pointer、promotion 和 poll 终态全部回滚，旧状态继续有效

#### Scenario: COMMIT 结果未知后恢复
- **WHEN** Worker 不知道提交是否成功并按同一 poll 恢复
- **THEN** 已提交 poll 不再次推进账号，未提交事务可在有效 lease/attempt 内整体重做且最终只有一次转换

#### Scenario: 旧 fencing token 携带 lifecycle candidates
- **WHEN** 恢复者已替换 token 或 poll 已终态后旧 Worker尝试 finalize
- **THEN** 受控函数影响零行，旧 candidates 被丢弃且账号状态不变

### Requirement: lifecycle产品读取 MUST 保持只读与数据面隔离

Lifecycle foundation SHALL 允许 `account-inventory-readonly-query` 向受认证管理员提供有界 current 投影，并允许独立 `account-inventory-history-compaction` 只从已提交历史生成日级摘要、覆盖率和受控清理到期历史。Query 与 history MUST NOT 提供账号导出、人工状态操作、补采、promotion、重建或删除入口；history MUST NOT 重算、删除或改变 current lifecycle，受控删除 current source poll 后外键可以置空但冗余来源和当前字段保持不变。本 capability 仍不实现 HMAC account ID、跨 Node 重复、趋势产品页或告警路由，也不修改 Gateway/Node 或增加既有固定账号清单只读 GET 之外的外部请求。

Current operational reads MUST explicitly filter active Node plus monitoring/current eligibility;
history/rollup/compaction preserve retired evidence and never infer a current target from it.

#### Scenario: Node lifecycle delta
- **WHEN** the Node lifecycle condition described by this change is evaluated
- **THEN** the existing baseline behavior remains intact and the lifecycle fence is also enforced

#### Scenario: 管理员访问现有产品界面
- **WHEN** history compaction 部署后管理员访问产品界面
- **THEN** 可继续读取有界 current 账号列表，但没有历史趋势、导出、状态修改、补采、promotion、重建、删除或告警入口

#### Scenario: 查询与promotion并发
- **WHEN** 管理员 query、lifecycle-aware finalize 和到期历史清理并发
- **THEN** query只观察已提交 current 状态，promotion按原规则推进，history不改变missing计数且current source外键置空不丢失冗余来源含义

#### Scenario: 网络调用范围验收
- **WHEN** lifecycle、query、history恢复和策略切换验收运行
- **THEN** history/query不调用Gateway、Prometheus、模型数据面、Node写接口或未登记目标，Node请求数量与既有poll-run一致

#### Scenario: Control 或 PostgreSQL 停止
- **WHEN** lifecycle/query/history依赖停止或旧应用回滚
- **THEN** 已形成current状态和摘要保留，新状态/读取/压缩按相应依赖暂停，Gateway与Relay Node已有模型请求继续
