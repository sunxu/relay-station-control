## ADDED Requirements

### Requirement: Topology SHALL 复用统一账号列表和只读详情

Control MUST以Inventory为账号集合统一显示账号状态与请求质量，并允许使用account_key打开请求历史及采集信息抽屉。账号无窗口内请求MUST显示Unknown（可标为无请求），read failure MUST显示Unavailable。账号状态与请求质量MUST独立；不改变Inventory、Binding、Duplicate Ownership或运行时状态。

#### Scenario: 双入口与详情
- **WHEN** 管理员从账号清单或Node Topology查看同一Node
- **THEN** 使用同一列表展示规则与只读详情，默认present并保留查看missing等记录；不保留两套重复表格

#### Scenario: 无请求与不可用
- **WHEN** Inventory账号存在但窗口内无请求，或质量读取失败
- **THEN** 前者保留账号且显示Unknown/0/null统计，后者显示Unavailable而非空列表或Unknown

#### Scenario: 切换Node与Incident
- **WHEN** 切换Node或从Incident选中账号
- **THEN** Node切换清空旧选择并隔离取消请求，Incident使用当前Node/account_key打开同一详情；不串账号

### Requirement: Control SHALL 展示有界最近请求成功失败

每账号MUST返回最近7天最多10条真实请求摘要，按occurred_at DESC、event_hash DESC稳定选取；UI从旧到新显示成功与失败并提供非颜色信息。摘要窗口MUST独立于质量15m/1h，不能替代既有分类。无请求不得补造成功；unresolved及其他Node/账号不得污染结果。MUST只读现有事件，不增加事件字段或采集机制。

#### Scenario: 十次最近结果
- **WHEN** 账号最近7天有超过10条成功/失败混合请求
- **THEN** 只展示最新10条，时间相同时排序稳定，最新在右；tooltip仅安全元数据，点击账号可查看原7天History

#### Scenario: 无记录
- **WHEN** 最近7天没有可归属事件
- **THEN** 最近请求明确为空，不制造fake account或成功结果；质量按自身窗口计算

### Requirement: Unified account reads SHALL 保持安全分页与单次数据库往返

组合查询POST MUST认证super_admin及session-bound CSRF；筛选和cursor仅在body，cursor加密认证且15分钟有效并绑定actor。原GET兼容保留。POST MUST逐页复用view audit；、limit默认25最大100，所有过滤先于account_key keyset分页，cursor绑定Node及全部过滤。MUST通过只读受控函数读取Inventory/quality/recent，不能前端拼接分页或逐账号HTTP读取。数据库失败MUST返回503。只能增加additive readonly query-access migration，不新增表/index/rollup或direct SELECT授权。

#### Scenario: 过滤与权限
- **WHEN** 使用provider/lifecycle/quality/email/basic_status/window过滤分页或跨账号条件复用cursor
- **THEN** 正确过滤后分页；不匹配cursor为400，未登录401、非管理员/无效CSRF403、数据库失败503

#### Scenario: 安全查询和性能
- **WHEN** runtime读取100账号及10000事件的账号页
- **THEN** 一次store数据查询返回有界页（另有固定逐页审计写入）与每账号最多10条摘要；仅EXECUTE授权，direct SELECT继续拒绝
