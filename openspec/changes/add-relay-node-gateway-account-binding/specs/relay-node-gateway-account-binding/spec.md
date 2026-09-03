## ADDED Requirements

### Requirement: Control SHALL 使用稳定 Node ID 与 Gateway Account ID 建立显式 binding

Control SHALL 只以 `relay_node_assets.instance_id` 作为 Relay Node identity，并只以 `(gateway_instance_id, gateway_directory_snapshot_items.account_id)` 作为 Gateway Account identity。第一版 current binding cardinality MUST 为 `Relay Node 0..1 ↔ 0..1 Gateway Account`。name、display name、platform、type、url、IP、port 和 status MUST NOT 作为 identity proof、fallback key 或自动匹配依据。

#### Scenario: 建立合法 binding
- **WHEN** 管理员选择已登记 Relay Node 和 current fresh Directory 中存在的 Gateway Account ID
- **THEN** Control 建立唯一 current binding，并保存对应 Gateway、Account ID 和 Directory evidence snapshot

#### Scenario: 非 identity 字段相同
- **WHEN** 两个不同 Account ID 具有相同 name、platform、type、url 或 status
- **THEN** Control 将它们视为不同 identity，且不得根据这些字段继承、合并或自动迁移 binding

#### Scenario: 尝试一对多 binding
- **WHEN** 调用尝试让同一 Node current bind 多个 Account，或让同一 Gateway Account current bind 多个 Node
- **THEN** PostgreSQL 唯一约束拒绝冲突写入，既有 binding 保持不变

### Requirement: Control SHALL 以 temporal interval 保存 current binding 与完整历史

Control SHALL 使用可关闭的 binding interval 保存关系。`ended_at IS NULL` MUST 表示 current binding；关闭的 interval MUST 保留 bound/ended actor、reason 和 DB timestamp。每次bind/rebind/unbind transaction MUST在事务开始后通过`GetRelayBindingDBTime`执行一次`clock_timestamp()`并将结果作为唯一`operation_at`；service MUST NOT使用Go `time.Now()`生成持久时间。rebind旧interval的`ended_at`与新interval的`bound_at` MUST使用完全相同的`operation_at`。`bind_reason` MUST只允许`administrator_bind|administrator_rebind`，`end_reason` MUST只允许`administrator_unbind|administrator_rebind`，并由后续Migration使用CHECK constraints固化。bind的新interval MUST使用`administrator_bind`；rebind MUST以`administrator_rebind`关闭旧interval并以`administrator_rebind`创建新interval；unbind MUST以`administrator_unbind`关闭旧interval。`binding_id`、`relay_node_id`、`gateway_instance_id`、`gateway_account_id`、`evidence_snapshot_id`、`bound_at`、`bound_by`和`bind_reason`创建后 MUST不可修改。open interval只允许一次`ended_at NULL → non-NULL`，并 MUST在同一UPDATE原子写入non-NULL `ended_by/end_reason`；closed interval MUST完全immutable。rebind MUST在同一事务close-old + insert-new，且 MUST NOT通过UPDATE修改target identity。DELETE和TRUNCATE MUST由数据库拒绝。正常产品操作 MUST NOT物理删除binding history，也 MUST NOT增加enabled、disabled、confirmed、ambiguous、resolved或unresolved binding truth状态。

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
- **WHEN** 合法unbind/rebind以同一UPDATE将`ended_at`从NULL改为non-NULL并写入`ended_by/end_reason`
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

### Requirement: Control SHALL 从 binding、current Directory 和 freshness 派生 resolution

Binding Resolution MUST 是 query-derived observation，不是管理员可写 truth。无 current binding MUST 为 `unbound`；有 binding但没有 accepted Directory或 Directory stale MUST 为 `unknown`；fresh Directory 包含 target Account ID MUST 为 `resolved`；fresh Directory 不包含 target ID MUST 为 `unresolved`。

#### Scenario: Account 从 fresh Directory 消失
- **WHEN** binding target 不再存在于 current fresh Directory
- **THEN** binding 保留且 resolution 为 unresolved，Control 不自动 unbind 或 rebind

#### Scenario: Directory stale 或 fetch failed
- **WHEN** binding 存在但没有 accepted Directory，或 current observation 已 stale
- **THEN** resolution 为 unknown，last-known resolved 不得冒充 current resolved

#### Scenario: fresh empty Directory
- **WHEN** current accepted Directory fresh 且 accounts 为空
- **THEN** 所有已有 current bindings 为 unresolved，而不是 unknown

#### Scenario: 同一 ID 再次出现
- **WHEN** 原 Account ID 再次出现在 current fresh Directory
- **THEN** 原 binding 自动恢复 resolved，不创建新 binding

#### Scenario: A→B→A snapshot reuse
- **WHEN** Directory content 从 A 变为 B 再回到复用的 A snapshot
- **THEN** binding identity 保持不变，resolution 只按 current A snapshot 是否包含同一 Account ID 重算

### Requirement: Control SHALL 提供 Node-centric 与 Account-centric binding read model

Control SHALL 至少支持按 Relay Node 查询 current binding，以及按 Gateway Account 查询 current bound Node。read model MUST返回Gateway Account ID、current binding是否存在、derived resolution、Directory freshness与last successful observation。fresh且target ID present时resolution MUST为resolved，并 MAY展示current Directory context；fresh且target ID missing时resolution MUST为unresolved，且 MUST NOT把historical evidence当成current evidence；stale或没有accepted Directory时resolution MUST为unknown。stale时 MAY展示last-known Account context，但 MUST明确其不是current evidence。API/UI MUST能区分current context与last-known context，具体字段名留给implementation design。目标已从current Directory消失的binding MUST仍可被查询为unresolved。

#### Scenario: 查询 Node current binding
- **WHEN** Control 按 Relay Node ID 查询 binding
- **THEN** 返回至多一个 current Gateway Account binding及其 resolution/freshness，不返回 Secret 或 Gateway credential

#### Scenario: 查询 Gateway Account bindings
- **WHEN** Control 按 Gateway 查询 current Directory Account 与 binding
- **THEN** 每个 current Account 返回至多一个 bound Node，且目标已消失的 current binding仍可作为 unresolved relation单独返回

#### Scenario: stale 时展示 last-known context
- **WHEN** Directory stale 且 read model展示历史 Account描述
- **THEN** 输出必须明确其为 last-known context，并将 current resolution标记为 unknown

#### Scenario: fresh且target存在
- **WHEN** Directory fresh且current snapshot包含binding target ID
- **THEN** resolution为resolved，展示的Account context来自current Directory

#### Scenario: fresh且target缺失
- **WHEN** Directory fresh但current snapshot不包含binding target ID
- **THEN** resolution为unresolved，historical evidence不得被标记或展示为current context

#### Scenario: API/UI区分context来源
- **WHEN** read model包含Account context
- **THEN** API/UI能够区分current与last-known来源，但本spec不冻结具体字段名

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

### Requirement: Control SHALL 保留 Account与Node生命周期边界

Gateway Account persistent status变化 MUST NOT自动修改binding；只要Account ID仍在Directory中，disabled或unknown status仍可保持resolved。Account从fresh Directory消失时binding MUST保留为unresolved。Node monitoring停用、暂时不可达或运行停止 MUST NOT自动修改binding。存在binding history时Node/Gateway物理删除 MUST受FK RESTRICT保护；Node retirement必须先显式unbind并保留资产identity，且不得级联删除history。

#### Scenario: Gateway Account disabled
- **WHEN** 同一 Account ID仍在fresh Directory但status变为disabled
- **THEN** binding identity不变且resolution仍为resolved；Control不修改Gateway Account

#### Scenario: Node monitoring停用
- **WHEN** Node账号监控区间结束或Node暂时不可达
- **THEN** current binding保持不变，不触发自动unbind

#### Scenario: 删除有binding history的Node
- **WHEN** 调用尝试物理删除仍被current或historical binding引用的Node
- **THEN** PostgreSQL拒绝删除，binding history保持完整

### Requirement: Control SHALL 复用现有管理员审计与安全边界

bind、unbind和rebind MUST只允许既有实名、未过期的`super_admin`管理路径，并 MUST与现有immutable `audit_logs`记录在同一事务提交。temporal binding的`bound_by/ended_by` MUST引用`control_admin_users.admin_id`，FK MUST为`ON UPDATE RESTRICT ON DELETE RESTRICT`；display/login name MUST NOT作为actor identity。后续Migration MUST仅additive扩展既有audit CHECK：`category`固定为`relay_binding`，`action`固定为`relay_binding.bind|relay_binding.unbind|relay_binding.rebind`。audit details MUST只允许`relay_node_id`、`gateway_instance_id`、`old_gateway_account_id`、`new_gateway_account_id`、`evidence_snapshot_id`和`reason_code`；不得包含额外key。`details.reason_code` MUST复用对应操作写入interval的固定`bind_reason/end_reason`，MUST NOT建立独立audit reason taxonomy。错误、日志、指标、审计和响应 MUST NOT包含Secret reference、token、Gateway DB credential、raw Directory response、raw URL、display/login name或原始内部错误。

#### Scenario: 稳定管理员 identity
- **WHEN** 管理员执行bind、unbind或rebind
- **THEN** binding metadata和audit actor使用同一`control_admin_users.admin_id`，不复制display/login name作为identity

#### Scenario: 删除被binding metadata引用的管理员
- **WHEN** 调用尝试UPDATE或DELETE被`bound_by/ended_by`引用的admin identity
- **THEN** PostgreSQL FK RESTRICT拒绝操作并保留关系历史的actor identity

#### Scenario: 固定 binding audit shape
- **WHEN** bind、unbind或rebind成功
- **THEN** audit category为`relay_binding`、action为对应固定值，details只包含固定allowlist keys，`reason_code`复用对应binding reason

#### Scenario: audit包含额外或敏感字段
- **WHEN** audit details包含allowlist外字段、Secret、raw URL、raw response、credential或display/login name
- **THEN** PostgreSQL shape constraint拒绝audit和同事务binding mutation

#### Scenario: binding与audit原子提交
- **WHEN** binding mutation成功
- **THEN** 对应immutable audit在同一事务存在；audit失败时binding mutation回滚

#### Scenario: 敏感canary进入失败路径
- **WHEN** Secret、token、credential、raw response或unsafe URL canary进入binding请求或底层错误
- **THEN** 日志、指标、错误响应和audit details均不包含canary

#### Scenario: Node/Gateway ownership mismatch
- **WHEN** 请求引用未登记Node、不同Gateway的snapshot或不属于指定Gateway的Account evidence
- **THEN** FK与transaction validation拒绝整个写入，不创建binding或audit success

### Requirement: Binding SHALL 不进入请求数据面或其它原生配置真相

Binding SHALL仅作为Control管理的关联元数据、诊断上下文和派生topology输入。Control MUST NOT因binding创建、修改或删除Sub2API Account/Group、Account–Group membership、用户/API Key路由、Gateway scheduler state、CLIProxyAPI credential/provider/account，且 MUST NOT直接访问Gateway PostgreSQL。Binding MUST NOT参与request routing、weights、retry、failover、health decision或duplicate ownership fact。

#### Scenario: binding mutation
- **WHEN**管理员bind、unbind或rebind
- **THEN**只有Control binding/history/audit发生变化，Gateway、CLIProxyAPI、Directory snapshot/current state和请求数据面保持不变

#### Scenario: duplicate ownership evaluation
- **WHEN**后续系统判断cross-node duplicate ownership
- **THEN**binding与Directory freshness只能作为独立Gateway Usage Context，不得创建、消除或改变ownership fact

### Requirement: Control SHALL 在存在 binding 或 audit 历史时拒绝 Migration Rollback

当数据库中存在任一`relay_node_gateway_account_bindings`记录或任一`audit_logs.category = 'relay_binding'`记录时，Migration 11 Down MUST抛出SQLSTATE `55000`拒绝schema rollback，且 MUST NOT删除或清理这些记录来使rollback成功。未产生任何binding记录与`relay_binding` audit记录的干净环境允许正常11 → 10回滚。

#### Scenario: 干净环境下允许 migration rollback
- **WHEN** 数据库未产生任何binding记录且未产生任何`relay_binding` audit记录时执行Migration Down
- **THEN** Migration成功回滚至version 10，恢复原有audit约束

#### Scenario: 存在 binding 记录时拒绝 rollback
- **WHEN** 数据库存在current或historical binding记录时尝试执行Migration Down
- **THEN** PostgreSQL抛出SQLSTATE `55000`拒绝回滚，schema版本仍为11且binding记录保持不变

#### Scenario: 仅存在 relay_binding audit 时拒绝 rollback
- **WHEN** 数据库存在`relay_binding` audit记录时尝试执行Migration Down
- **THEN** PostgreSQL抛出SQLSTATE `55000`拒绝回滚，schema版本仍为11且audit记录保持不变
