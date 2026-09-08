## ADDED Requirements

### Requirement: Account Incident SHALL 仅聚合当前账号重复失败

Control SHALL从现有events计算(node_id,account_key,failure_class)的active：DB时间最近15分钟至少3次失败，包含下边界、排除future，四类auth/quota/rate_limit/upstream独立；unknown、unresolved、event-only不生成。SHALL先复用完整current Inventory read model，不能遗漏首101条以后的账号。MUST NOT持久化Incident、改变Inventory/lifecycle/identity或Quality classification。

#### Scenario: 阈值与类别
- **WHEN** 同账号auth有3次失败、另一账号2次，或其它三类各3次，且混有success与unknown
- **THEN** 仅达到3次的四类独立产生active；success不抵消失败，unknown被忽略

#### Scenario: 账号隔离和时间边界
- **WHEN** NULL身份、event-only、其它Node、15分钟前或future事件存在
- **THEN** 不计入目标Incident，恰好15分钟事件纳入，当前Inventory中后续分页账号仍可生成

#### Scenario: 无恢复推断
- **WHEN** 同组低于3次或发生成功请求
- **THEN** 低于3次不返回该组，成功不解除仍达标active；不显示recovered或保存状态

### Requirement: Incident SHALL 保持只读有界查询契约

GET `/api/topology/nodes/{instance_id}/incidents` SHALL要求super_admin、no-store、5秒预算。status仅省略或active，provider/failure_class可选，limit默认25最大100。按last_seen DESC/account_key ASC/failure_class ASC过滤后keyset分页；opaque cursor MUST绑定Node与所有filters及完整排序位置，错配400。item SHALL提供node_id/account_key/provider/failure_class/status/first_seen/last_seen/hit_count/last_success_at；前三个统计时间和计数描述15m组，last_success_at为7天同账号成功最大时间或NULL。

#### Scenario: 分页过滤与身份
- **WHEN** provider/reason过滤、相同last_seen多个账号类别或继续cursor
- **THEN** 精确过滤后按混合方向排序，有界limit+1，无offset；Node/filters错配400且不会返回其它范围

#### Scenario: 授权与故障
- **WHEN** 无session、非super_admin、非GET、Node不存在或DB/Inventory失败
- **THEN** 分别401/403/方法拒绝/404/503，故障不能返回empty，恢复后只读重试成功

#### Scenario: 安全与重启
- **WHEN** runtime读function或direct SELECT，或隔离Down/Up及进程重启
- **THEN** 只有SECURITY DEFINER STABLE固定pg_catalog/migrator owner/PUBLIC revoke函数EXECUTE被授权；direct SELECT拒绝，Down只drop新function，旧表/index/数据/函数不变，重启从现有事件重算

#### Scenario: 本地容量
- **WHEN** 100账号10000事件进行active/provider/failure/pagination查询
- **THEN** 每次单个客户端DB query并记录latency，在合理预算内不新增表/index/worker/cache/materialized view/rollup
