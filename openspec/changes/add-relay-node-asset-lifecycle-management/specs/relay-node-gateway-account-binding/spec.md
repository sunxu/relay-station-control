## MODIFIED Requirements

### Requirement: Control SHALL 以 temporal interval 保存 current binding 与完整历史

Control SHALL 使用可关闭的 binding interval 保存关系。`ended_at IS NULL` MUST 表示 current
binding；关闭的 interval MUST 保留 bound/ended actor、reason 和 DB timestamp。每次
bind/rebind/unbind transaction MUST在完成所需row locks后通过`GetRelayBindingDBTime`执行一次
`clock_timestamp()`并将结果作为唯一`operation_at`；service MUST NOT使用Go `time.Now()`生成持久
时间。rebind旧interval的`ended_at`与新interval的`bound_at` MUST使用完全相同的`operation_at`。
`bind_reason` MUST只允许`administrator_bind|administrator_rebind`，`end_reason` MUST只允许
`administrator_unbind|administrator_rebind|gateway_retired|gateway_replaced|node_retired|node_replaced`，
并由后续Migration使用CHECK constraints固化。bind的新interval MUST使用`administrator_bind`；
rebind MUST以`administrator_rebind`关闭旧interval并以`administrator_rebind`创建新interval；
unbind MUST以`administrator_unbind`关闭旧interval。`binding_id`、`relay_node_id`、
`gateway_instance_id`、`gateway_account_id`、`evidence_snapshot_id`、`bound_at`、`bound_by`和
`bind_reason`创建后 MUST不可修改。open interval只允许一次`ended_at NULL → non-NULL`，并 MUST在
同一UPDATE原子写入non-NULL `ended_by/end_reason`；closed interval MUST完全immutable。rebind
MUST在同一事务close-old + insert-new，且 MUST NOT通过UPDATE修改target identity。DELETE和
TRUNCATE MUST由数据库拒绝。正常产品操作 MUST NOT物理删除binding history，也 MUST NOT增加
enabled、disabled、confirmed、ambiguous、resolved或unresolved binding truth状态。

Gateway Retire MUST 在同事务、同 retirement boundary 关闭其所有 current binding，
reason=gateway_retired；Replace 用同 replacement boundary、reason=gateway_replaced，ended_by
为执行 actor。不得迁移到新 Gateway；retired Gateway MUST NOT 有 ended_at IS NULL binding。

Node Retire/Replace MUST 在同一 lifecycle transaction 内以 reason=node_retired/node_replaced
关闭 current Node binding，MUST NOT rebind 到 replacement identity；replacement Node 以零
current binding 起始。两组 lifecycle close 共享同一 binding history 不可变、不可物理删除约束。

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
- **WHEN** 合法unbind/rebind或Gateway/Node lifecycle close以同一UPDATE将`ended_at`从NULL改为
  non-NULL并写入`ended_by/end_reason`
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

#### Scenario: Gateway关闭reason
- **WHEN** Gateway Retire/Replace提交
- **THEN** end_reason分别为gateway_retired/gateway_replaced，end timestamp和actor与lifecycle一致

#### Scenario: Node关闭reason
- **WHEN** Node Retire/Replace提交
- **THEN** end_reason分别为node_retired/node_replaced，end timestamp和actor与lifecycle一致，且
  replacement Node不继承该binding

### Requirement: Control SHALL 原子处理并发 binding 写入

Control SHALL 通过 PostgreSQL短事务、row locking和 partial unique constraints串行化同一 Node及
同一 Gateway Account的 current binding mutation。实现 MUST NOT 先 SELECT 到进程内再无条件
UPDATE。冲突写入 MUST 返回稳定 conflict，并 MUST NOT自动抢占、unbind或覆盖其它 Node的 binding。

Bind/Rebind MUST 使用 Node asset（`SELECT ... FOR UPDATE` 并验证 `lifecycle_status = active`）
-> target Gateway asset（同样验证 active 且 singleton_id=1）-> Gateway Directory current-state
-> Node/current-account binding rows 的锁顺序；retired Node 或 retired Gateway MUST NOT 创建或
rebind 到 current binding。DB-time/fresh Directory/account existence/partial unique/audit 约束
保持。

Gateway Retire/Replace 只锁 Gateway 后再锁 bindings，MUST NOT 在持 Gateway 锁后反向获取 Node row
lock。Node Retire/Replace 使用 Node→monitoring→binding 顺序锁定，MUST NOT 在持 Node 锁后反向
获取 Gateway/Directory 锁。两条 lifecycle 路径都 MUST NOT 与 Bind/Rebind 的
Node→Gateway→Directory→binding 顺序产生反向加锁死锁。

#### Scenario: 两个管理员同时绑定同一 Node
- **WHEN** 两个事务并发把同一 Node绑定到不同 Account
- **THEN** 最多一个事务成功，另一个返回 conflict，数据库中只有一个 current interval

#### Scenario: 两个 Node同时绑定同一 Account
- **WHEN** 两个事务并发绑定同一 `(gateway_instance_id, gateway_account_id)`
- **THEN** 最多一个事务成功，Account不会出现两个 current bound Node

#### Scenario: rebind中途失败
- **WHEN** close old、insert new或audit任一步失败
- **THEN** 整个事务回滚，原 current binding保持可见且不产生部分history

#### Scenario: Gateway lifecycle先commit
- **WHEN** Gateway Retire/Replace先持Gateway锁并commit
- **THEN** 并发Bind/Rebind等待后conflict，不产生current binding

#### Scenario: binding先于Gateway lifecycle commit
- **WHEN** Bind/Rebind先commit
- **THEN** Gateway lifecycle随后关闭该binding，最终无retired+current binding

#### Scenario: Node lifecycle先commit
- **WHEN** Node Retire/Replace先持Node锁并commit lifecycle_status=retired
- **THEN** 并发Bind/Rebind在验证`lifecycle_status = active`时失败并返回conflict，不产生
  current binding

#### Scenario: binding先于Node lifecycle commit
- **WHEN** Bind/Rebind先取得Node锁并在Node仍active时commit
- **THEN** Node Retire/Replace随后仍可关闭该binding（reason=node_retired/node_replaced），
  最终无retired+current binding

### Requirement: Control SHALL 保留 Account与Node生命周期边界

Gateway Account persistent status变化 MUST NOT自动修改binding；只要Account ID仍在Directory中，
disabled或unknown status仍可保持resolved。Account从fresh Directory消失时binding MUST保留为
unresolved。Node monitoring停用、暂时不可达或运行停止 MUST NOT自动修改binding。存在binding
history时Node/Gateway物理删除 MUST受FK RESTRICT保护。

Gateway Retire MUST 在同事务、同 retirement boundary 关闭其所有 current binding，
reason=gateway_retired；Replace 用同 replacement boundary、reason=gateway_replaced，ended_by
为执行 actor。不得迁移到新 Gateway；retired Gateway MUST NOT 有 ended_at IS NULL binding。历史
不可变且不物理删除。

Node lifecycle MUST preserve binding history and MUST NOT infer account migration from
monitoring, Gateway status, Directory evidence or replacement lineage. Node Retire/Replace MUST
在同一 lifecycle transaction 内关闭 current Node binding（reason=node_retired/node_replaced），
replacement Node 以零 current binding 起始，不继承旧 binding；不得级联删除 history。

#### Scenario: Gateway Account disabled
- **WHEN** 同一 Account ID仍在fresh Directory但status变为disabled
- **THEN** binding identity不变且resolution仍为resolved；Control不修改Gateway Account

#### Scenario: Node monitoring停用
- **WHEN** Node账号监控区间结束或Node暂时不可达
- **THEN** current binding保持不变，不触发自动unbind

#### Scenario: 删除有binding history的Node
- **WHEN** 调用尝试物理删除仍被current或historical binding引用的Node
- **THEN** PostgreSQL拒绝删除，binding history保持完整

#### Scenario: Gateway退休保留history
- **WHEN** Gateway lifecycle提交
- **THEN** current关系关闭，历史FK保留，新Gateway零绑定

#### Scenario: Node退休保留history
- **WHEN** Node Retire/Replace提交
- **THEN** current binding关闭，历史FK保留，新Node（Replace的replacement identity）零绑定
