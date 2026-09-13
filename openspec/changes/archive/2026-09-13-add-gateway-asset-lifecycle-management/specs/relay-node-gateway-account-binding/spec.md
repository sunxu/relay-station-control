## MODIFIED Requirements

### Requirement: Control SHALL 以 temporal interval 保存 current binding 与完整历史

Control SHALL 使用可关闭的 binding interval 保存关系。`ended_at IS NULL` MUST 表示 current binding；关闭的 interval MUST 保留 bound/ended actor、reason 和 DB timestamp。每次bind/rebind/unbind transaction MUST在完成所需row locks后通过`GetRelayBindingDBTime`执行一次`clock_timestamp()`并将结果作为唯一`operation_at`；service MUST NOT使用Go `time.Now()`生成持久时间。rebind旧interval的`ended_at`与新interval的`bound_at` MUST使用完全相同的`operation_at`。`bind_reason` MUST只允许`administrator_bind|administrator_rebind`，`end_reason` MUST只允许`administrator_unbind|administrator_rebind|gateway_retired|gateway_replaced`，并由后续Migration使用CHECK constraints固化。bind的新interval MUST使用`administrator_bind`；rebind MUST以`administrator_rebind`关闭旧interval并以`administrator_rebind`创建新interval；unbind MUST以`administrator_unbind`关闭旧interval。`binding_id`、`relay_node_id`、`gateway_instance_id`、`gateway_account_id`、`evidence_snapshot_id`、`bound_at`、`bound_by`和`bind_reason`创建后 MUST不可修改。open interval只允许一次`ended_at NULL → non-NULL`，并 MUST在同一UPDATE原子写入non-NULL `ended_by/end_reason`；closed interval MUST完全immutable。rebind MUST在同一事务close-old + insert-new，且 MUST NOT通过UPDATE修改target identity。DELETE和TRUNCATE MUST由数据库拒绝。正常产品操作 MUST NOT物理删除binding history，也 MUST NOT增加enabled、disabled、confirmed、ambiguous、resolved或unresolved binding truth状态。

#### Scenario: bind使用固定reason
- **WHEN** 管理员建立新的current binding
- **THEN** 新interval的`bind_reason`固定为`administrator_bind`

#### Scenario: rebind使用固定reasons
- **WHEN** 管理员将Node从Account A rebind到Account B
- **THEN** A interval的`end_reason`与B interval的`bind_reason`均固定为`administrator_rebind`

#### Scenario: rebind使用单一DB operation time
- **WHEN** rebind transaction关闭旧interval并插入新interval
- **THEN** transaction只取得一次DB wall-clock time，旧interval的`ended_at`与新interval的`bound_at`完全相同，且不使用Go进程时间

#### Scenario: unbind使用固定reason
- **WHEN** 管理员unbind current binding
- **THEN** closed interval的`end_reason`固定为`administrator_unbind`

#### Scenario: 非法binding reason
- **WHEN** 写入不在固定allowlist中的`bind_reason`或`end_reason`
- **THEN** PostgreSQL CHECK constraint拒绝整个写入

#### Scenario: 管理员 unbind
- **WHEN** 管理员移除 Node 的 current binding
- **THEN** Control 关闭该 interval、保留完整历史并使 Node 当前为 unbound，不物理删除历史 row

#### Scenario: 管理员 rebind
- **WHEN** 管理员把 Node 从 Account A 改绑到 Account B
- **THEN** close A、open B 和 audit 在一个事务中提交，任何读取都不能观察到中间双绑定

#### Scenario: 尝试修改 binding identity 或 bound metadata
- **WHEN** 调用尝试 UPDATE identity fields、bound metadata或evidence_snapshot_id
- **THEN** PostgreSQL拒绝修改并保留原interval

#### Scenario: 第一次关闭 open interval
- **WHEN** 合法unbind/rebind或Gateway lifecycle close以同一UPDATE将`ended_at`从NULL改为non-NULL并写入`ended_by/end_reason`
- **THEN** PostgreSQL接受一次完整close，且三个end字段原子可见

#### Scenario: 修改已关闭 interval
- **WHEN** 调用尝试再次关闭、重开或修改closed interval的任意字段
- **THEN** PostgreSQL拒绝修改，closed history保持完全immutable

#### Scenario: 删除或截断 binding history
- **WHEN** 任意调用尝试DELETE或TRUNCATE temporal binding表
- **THEN** PostgreSQL拒绝操作并保留全部current与historical intervals

#### Scenario: 重复 unbind
- **WHEN** Node 已经 unbound 且管理员重放相同 unbind
- **THEN** Control 返回稳定 no-op/already-unbound 结果，不新增虚假 history interval

Gateway Retire MUST 在同事务、同 retirement boundary 关闭其所有 current binding，reason=gateway_retired；Replace 用同 replacement boundary、reason=gateway_replaced，ended_by 为执行 actor。不得迁移到新 Gateway；retired Gateway MUST NOT 有 ended_at IS NULL binding。历史不可变且不物理删除。

#### Scenario: Gateway关闭reason
- **WHEN** Gateway Retire/Replace提交
- **THEN** end_reason分别为gateway_retired/gateway_replaced，end timestamp和actor与lifecycle一致

### Requirement: Control SHALL 原子处理并发 binding 写入

Control SHALL 通过 PostgreSQL短事务、row locking和 partial unique constraints串行化同一 Node及同一 Gateway Account的 current binding mutation。实现 MUST NOT 先 SELECT 到进程内再无条件 UPDATE。冲突写入 MUST 返回稳定 conflict，并 MUST NOT自动抢占、unbind或覆盖其它 Node的 binding。

#### Scenario: 两个管理员同时绑定同一 Node
- **WHEN** 两个事务并发把同一 Node绑定到不同 Account
- **THEN** 最多一个事务成功，另一个返回 conflict，数据库中只有一个 current interval

#### Scenario: 两个 Node同时绑定同一 Account
- **WHEN** 两个事务并发绑定同一 `(gateway_instance_id, gateway_account_id)`
- **THEN** 最多一个事务成功，Account不会出现两个 current bound Node

#### Scenario: rebind中途失败
- **WHEN** close old、insert new或audit任一步失败
- **THEN** 整个事务回滚，原 current binding保持可见且不产生部分history

Bind/Rebind MUST 使用 Relay Node asset -> target Gateway asset -> Gateway Directory current-state -> Node/current-account binding rows 的锁顺序。Gateway row 锁内 MUST 验证 active 且 singleton_id=1；DB-time/fresh Directory/account existence/partial unique/audit 约束保持。Gateway Retire/Replace 只锁 Gateway 后再锁 bindings，MUST NOT 在持 Gateway 锁后反向获取 Node row lock。

#### Scenario: lifecycle先commit
- **WHEN** Gateway Retire/Replace先持Gateway锁并commit
- **THEN** 并发Bind/Rebind等待后conflict，不产生current binding

#### Scenario: binding先commit
- **WHEN** Bind/Rebind先commit
- **THEN** Gateway lifecycle随后关闭该binding，最终无retired+current binding

### Requirement: Control SHALL 保留 Account与Node生命周期边界

Gateway Account persistent status变化 MUST NOT自动修改binding；只要Account ID仍在Directory中，disabled或unknown status仍可保持resolved。Account从fresh Directory消失时binding MUST保留为unresolved。Node monitoring停用、暂时不可达或运行停止 MUST NOT自动修改binding。存在binding history时Node/Gateway物理删除 MUST受FK RESTRICT保护；本 change 不开放 Node retirement 产品操作；其后续事务由 Node lifecycle change 冻结，必须保留资产identity，且不得级联删除history。

#### Scenario: Gateway Account disabled
- **WHEN** 同一 Account ID仍在fresh Directory但status变为disabled
- **THEN** binding identity不变且resolution仍为resolved；Control不修改Gateway Account

#### Scenario: Node monitoring停用
- **WHEN** Node账号监控区间结束或Node暂时不可达
- **THEN** current binding保持不变，不触发自动unbind

#### Scenario: 删除有binding history的Node
- **WHEN** 调用尝试物理删除仍被current或historical binding引用的Node
- **THEN** PostgreSQL拒绝删除，binding history保持完整

Gateway Retire MUST 在同事务、同 retirement boundary 关闭其所有 current binding，reason=gateway_retired；Replace 用同 replacement boundary、reason=gateway_replaced，ended_by 为执行 actor。不得迁移到新 Gateway；retired Gateway MUST NOT 有 ended_at IS NULL binding。历史不可变且不物理删除。

#### Scenario: Gateway退休保留history
- **WHEN** Gateway lifecycle提交
- **THEN** current关系关闭，历史FK保留，新Gateway零绑定

### Requirement: Control SHALL 只允许基于 current fresh Directory 的 bind 与 rebind

Control SHALL 在一个短 PostgreSQL transaction 中使用 DB time读取并锁定 Gateway Directory current state。bind/rebind MUST 要求存在 accepted current snapshot、`db_now - last_success_received_at <= 540s`，且目标 Account ID 存在于该 current snapshot items。没有 accepted Directory、Directory stale、目标 ID 缺失或 Node/Gateway 未登记时 MUST fail closed。unbind MUST NOT 依赖 Directory freshness。

#### Scenario: fresh Directory 中存在目标 Account
- **WHEN** bind transaction 中 current Directory fresh 且 current snapshot 包含目标 Account ID
- **THEN** Control 可继续执行唯一性检查并原子创建 binding

#### Scenario: Directory stale
- **WHEN** `db_now - last_success_received_at > 540s`
- **THEN** Control 拒绝新 bind/rebind，不使用 last-known snapshot 猜测可绑定性

#### Scenario: Account 不在 current snapshot
- **WHEN** Account ID 只存在于历史 snapshot 或完全不存在
- **THEN** Control 拒绝 bind/rebind，不使用相似 name/url/platform/type/status 替代

#### Scenario: stale 时 unbind
- **WHEN** Directory stale 或暂时不可用且管理员 unbind 已有 binding
- **THEN** Control 允许关闭 Control 自己的 current binding并保留历史

Bind/Rebind MUST 使用 Relay Node asset -> target Gateway asset -> Gateway Directory current-state -> Node/current-account binding rows 的锁顺序。Gateway row 锁内 MUST 验证 active 且 singleton_id=1；DB-time/fresh Directory/account existence/partial unique/audit 约束保持。Gateway Retire/Replace 只锁 Gateway 后再锁 bindings，MUST NOT 在持 Gateway 锁后反向获取 Node row lock。

#### Scenario: 历史Gateway不可bind
- **WHEN** 目标Gateway是retired
- **THEN** 即使历史Directory未超过540s也拒绝bind/rebind
