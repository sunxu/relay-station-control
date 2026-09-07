# node-centric-topology-ui Specification

## Purpose
为单环境Control管理员提供以稳定Node身份为入口的只读拓扑页面，独立展示Provider快照与健康证据、Gateway关联与解析，以及重复归属的当前和历史涉及关系，避免把当前membership当历史、把新鲜快照当最新健康正常或在浏览器边界损坏账号ID。

## Requirements

### Requirement: Topology SHALL 保持纯只读职责

Topology SHALL 展示Node资产、Inventory evidence、Binding truth/resolution和Duplicate Ownership，MUST NOT 提供或调用bind/rebind/unbind、candidate submit、account mutation、自动修复或采集/调度操作。Topology MUST NOT 增加expected_binding_id或修改既有Binding事务。若已有独立管理UI且已验证可用，Topology只能deep-link到该入口；当前没有Binding管理UI时MUST NOT 创建替代mutation页面或无效链接。

#### Scenario: 浏览与刷新没有写入
- **WHEN** 管理员打开Topology、切换Node、翻页、刷新或从错误恢复
- **THEN** 只发生只读查询，不调用Binding action或数据面，不创建确认草稿或mutation恢复队列

#### Scenario: 不存在Binding管理入口
- **WHEN** 仓库没有独立Binding管理页面
- **THEN** Topology不展示候选提交控件和管理绑定链接，不能因UI便利而新增bind/rebind/unbind

#### Scenario: 账号清单导航
- **WHEN** 管理员需要查看既有账号清单
- **THEN** 仅deep-link稳定Node ID；原页面自己的CSRF/view audit保留，不由Topology绕过或执行账号查询

### Requirement: Topology SHALL 独立呈现四类事实与来源

页面SHALL以Node资产为主集合，保留unbound、无账号和观测不可用的Node；Inventory evidence、Gateway binding、Binding resolution、Duplicate Ownership MUST分区展示。Gateway Usage Context MUST NOT 替代Ownership Fact。Account ID采用Binding transport的统一decimal string，不依据name/url猜测身份；四态resolution与current/last_known来源沿用既有读契约。

#### Scenario: Directory stale但冲突仍然ACTIVE
- **WHEN** Directory过期、Account disabled或Node unbound而后端occurrence仍ACTIVE
- **THEN** 页面分别展示binding/resolution和ACTIVE/Critical，不自动清除绑定或宣告ownership恢复

#### Scenario: Fresh empty Directory
- **WHEN** accepted Directory fresh但不含既有binding目标
- **THEN** 保留binding并显示unresolved，不把缺失目标解释为unbound或请求失败

### Requirement: Current involvement SHALL 继续使用current affected membership

Topology ACTIVE/current section SHALL 使用`cross_node_duplicate_occurrence_nodes`和既有occurrence接口`instance_id` current-membership filter。该filter MUST保持原义，不因增加History而改成历史参与。current affected set MUST NOT 被称为完整历史Node集合。

#### Scenario: B被证明absent
- **WHEN** A/B duplicate后B获得fresh complete absence evidence且membership被删除
- **THEN** B不再出现在该occurrence的current affected set，原current查询不得因历史evidence仍存在而包含B

### Requirement: Historical involvement SHALL 由append-only evidence证明

History SHALL 通过已有`cross_node_duplicate_occurrence_evidence`中同一occurrence/target Node的EXISTS证明历史涉及，MUST NOT 依赖当前membership或新增history表/复制membership truth。authoritative owner/absence/degraded evaluation均可证明“曾被涉及”，但MUST NOT据此推断“曾确认拥有账号”。

SHALL 新增明确的只读历史读取边界`GET /api/topology/nodes/{instance_id}/duplicate-history`；可选status=ACTIVE|RESOLVED，省略表示所有历史涉及状态。Resolved页传RESOLVED，History页省略；默认limit25、最大200，opaque cursor最长512且绑定Node/status，按last_seen_at DESC、occurrence_id DESC分页。MUST在occurrence层分页并以EXISTS去重，独立于evidence分页。响应SHALL标明involvement=historical与target Node；原affected_nodes仍为current集合。

#### Scenario: A/B恢复后两者都能查询历史
- **WHEN** A/B duplicate ACTIVE，B fresh complete absent，B membership DELETE，occurrence RESOLVED
- **THEN** A history与B history均包含该occurrence；B current不再属于affected set，两个查询语义不混淆

#### Scenario: 多条evidence与全部membership移除
- **WHEN** 同一Node有多条evidence，且current membership最终为空
- **THEN** history每个occurrence只返回一次，不因current集合为空丢失历史

#### Scenario: Source poll已清理
- **WHEN** evidence仍在而source_poll_run_id因retention为空
- **THEN** historical involvement仍由evidence.instance_id证明；刷新或重启不改变结果

#### Scenario: 只有absence或degraded评估
- **WHEN** evidence仅证明Node曾被authoritative absence/degraded evaluation涉及
- **THEN** History可展示“历史评估涉及”，不能把该Node描述为曾经confirmed owner

#### Scenario: 历史分页失败
- **WHEN** cursor与Node/status不匹配或历史查询失败
- **THEN** 分别返回400或503，不退回current-membership查询、不把失败显示为空history

### Requirement: Provider snapshot freshness SHALL 与latest health正交

Topology SHALL独立展示snapshot freshness和latest health两个badge及来源时间。freshness MUST仅使用既有promoted/current evidence、last_complete_at、既有15分钟阈值和适用范围；health MUST使用health_degraded、health_scheduled_at，health_reason使用下述安全Provider-state read contract允许的固定枚举。MUST NOT 以health_degraded改变snapshot freshness，MUST NOT 以last_complete_at替代health_scheduled_at，MUST NOT 增加数据库truth或综合健康状态。

snapshot freshness SHALL为fresh/stale/unknown/out_of_scope/unavailable，health SHALL为normal/degraded/unknown/unavailable；read_state不可用MUST显式unavailable而非empty或stale。Provider集合MUST来自适用策略和安全Provider证据，不从第一页账号推导。字段缺失/权限不足MUST NOT 默认normal或零。

#### Scenario: Fresh snapshot与normal health
- **WHEN** last_complete_at仍fresh且health_degraded=false
- **THEN** 分别显示freshness=fresh、health=normal及两个来源时间

#### Scenario: Fresh snapshot与degraded health并存
- **WHEN** T0完整snapshot成功，T0+5min新poll出现node-local duplicate/incomplete且health_degraded=true
- **THEN** 分别显示Fresh complete snapshot与Latest health degraded，不能只显示绿色Fresh，也不把freshness改为stale

#### Scenario: Stale snapshot与degraded health
- **WHEN** last_complete_at超过既有freshness window且health_degraded=true
- **THEN** freshness=stale、health=degraded，维度独立

#### Scenario: Read unavailable
- **WHEN** 安全读取不可用、权限不足、超时或网络失败
- **THEN** read_state及受影响维度为unavailable，不显示empty/stale/normal，不以占位数据关闭功能验收

#### Scenario: Freshness边界和空账号Provider
- **WHEN** 证据恰好15分钟或超出1ms，或Provider账号为空但promoted evidence存在
- **THEN** 按既有阈值判fresh/stale，空账号不意味着无Provider/无证据；health保持独立

### Requirement: Provider UI SHALL 不改变duplicate eligibility

本capability MUST NOT 修改已冻结Duplicate Ownership eligibility、owner集合计算、membership更新或resolve规则。health_degraded与ownership eligibility仍独立；Topology只改变read/display semantics，不将health条件添加进ownership查询或worker。

#### Scenario: 仅health变化
- **WHEN** promoted/current ownership证据不变而health_degraded从false变true
- **THEN** 只改变health展示，不因本UI/read change重算或更改duplicate eligibility/occurrence

### Requirement: Provider Summary SHALL 使用独立完整的安全read model

Provider Summary SHALL经`public.control_query_account_inventory_provider_states_v1(target_instance_id uuid)`读取，MUST NOT使用account_inventory行的DISTINCT、聚合或分页结果推导Provider集合。完整集合SHALL为同一statement timestamp下当前Node monitoring active时生效policy的active_providers，与该Node全部已有provider_states的并集；后者包括out_of_scope及0 account的Provider。查询SHALL以该集合LEFT JOIN既有state，不创建state或复制truth。

返回字段SHALL仅为provider、monitoring_status、state、current_scheduled_at、last_complete_at、snapshot_freshness、health_scheduled_at、health_degraded、health_reason。health_reason SHALL限既有安全固定枚举none/transport_failed/contract_invalid/disk_fallback/node_identity_incomplete/identity_incomplete；MUST NOT返回current_poll_run_id、raw payload/errors/response、secret或credential。

已有state的freshness SHALL依既有current/last_complete_at/15分钟规则及out_of_scope计算；health字段SHALL直接来自该state最新health metadata，不以account presence或freshness推断。当前policy要求监控但尚无state属于合法not-yet-observed：monitoring_status=active、state和全部时间/health字段=null、snapshot_freshness=unknown，UI health=unknown。MUST NOT伪造normal、stale或持久化占位state。

#### Scenario: A 零账号Provider仍然可见
- **WHEN** openai有current provider_state但0 account rows
- **THEN** Provider Summary仍包含openai及其state证据，不依赖account表是否有行

#### Scenario: B Fresh与更新的degraded health
- **WHEN** last_complete_at仍fresh且health_degraded=true、health_scheduled_at更新
- **THEN** 显示freshness=fresh和health=degraded，分别正确展示last_complete_at与health_scheduled_at及允许的reason

#### Scenario: C Stale与degraded health
- **WHEN** Provider snapshot stale且health_degraded=true
- **THEN** 同时显示freshness=stale和health=degraded

#### Scenario: Stale与healthy health
- **WHEN** Provider snapshot stale且最新已知health_degraded=false
- **THEN** 显示freshness=stale、health=normal（healthy），保留实际health时间，不制造综合status或宣称当前新观测健康

#### Scenario: Policy Provider尚未建立state
- **WHEN** Node正在监控且active policy包含openai但其state尚未建立
- **THEN** 返回openai的not-yet-observed投影和unknown/null证据，不从account absence推断状态，也不写入任何占位state

### Requirement: Provider-state query SHALL 保持readonly ACL边界

新函数SHALL为SECURITY DEFINER、STABLE，固定search_path=pg_catalog、全限定public对象名、owner=relay_control_migrator；SHALL REVOKE EXECUTE FROM PUBLIC并仅向relay_control_runtime授予EXECUTE（保留owner/superuser固有权限）。runtime MUST继续没有account_inventory_provider_states direct SELECT。MUST NOT使用Go owner/migration连接、raw SQL权限绕过、动态SQL或启动时DDL。

函数SHALL使用statement_timestamp及同一SQL snapshot；HTTP observed_at与函数调用SHALL来自同一SQL statement。函数不接受额外时间参数，不改变既有`control_query_current_account_inventory_v1` signature/body。null/zero UUID为invalid，未知Node为not found，不伪装为空集合。

#### Scenario: D Runtime权限隔离
- **WHEN** runtime调用新函数并尝试SELECT * FROM account_inventory_provider_states
- **THEN** EXECUTE成功而直接SELECT仍permission denied；PUBLIC没有新函数EXECUTE权限

### Requirement: Provider HTTP读取 SHALL 显式处理失败并独立组合

未来只读`GET /api/account-inventory/nodes/{instance_id}/providers`（operationId=getNodeInventoryProviderStates）SHALL沿用super_admin、no-store和request ID，返回instance_id、observed_at、完整providers；invalid为400、未知Node为404、认证/授权为401/403、query unavailable为503。查询预算2秒、HTTP预算5秒。只有成功查询且完整集合确实为空才可返回providers=[]；函数缺失、ACL错误、超时或query failure MUST显示Topology read unavailable，不返回部分成功/空集合或泄漏原始错误。

Provider Summary SHALL仅使用新Provider-state read model；Account Table SHALL继续使用既有paginated account inventory read model及其lifecycle/identity/projection；Binding SHALL使用既有binding read model；Duplicate SHALL使用current/history独立read models。MUST NOT为Provider Summary扩展existing v1 account query。

#### Scenario: E Function或query失败
- **WHEN** function不存在、EXECUTE失败或query超时/失败
- **THEN** HTTP返回503且Topology显示unavailable，不能返回200 providers=[]或将失败显示stale

### Requirement: P-READ SHALL 仅允许最小query-access migration

本change SHALL保持“0 persistence migration；P-READ 允许一个最小 additive readonly query-access migration。”未来实施阶段才可新增一个migration，编号按当时序列确定，`00017_*provider_state_readonly_query*.sql`仅为示例。架构评审轮不创建或执行migration；当前实施已获用户授权，SHALL仅在隔离验收数据库验证，不在生产执行。

Up SHALL仅CREATE该readonly function、设置owner、REVOKE PUBLIC、GRANT runtime EXECUTE。Down SHALL仅DROP该函数的uuid签名，使用默认RESTRICT，MUST NOT CASCADE。MUST NOT新建table/column/index/materialized view、topology/provider summary persistence，修改account_inventory/provider_states、旧account query、已有ACL或历史数据。应用回滚优先保留additive function；单独执行Down后新caller只能报告unavailable，不得降级为越权读取。

#### Scenario: Additive Up与isolated Down
- **WHEN** 未来获准实施后应用Up再执行Down
- **THEN** 唯一数据库对象差异为新函数及其EXECUTE权限；既有表、数据、account v1函数和runtime direct SELECT权限不变

### Requirement: Topology SHALL 保护身份并安全恢复读取

页面及新history读取SHALL沿用实名super_admin会话、no-store与request ID。canonical account_key按最新duplicate规格仅供管理员文本展示，MUST NOT进入URL/storage/logs/metrics；不得泄露凭据或raw response。各区SHALL独立loading/empty/unavailable/retry，401清理全部会话数据，切换Node/筛选丢弃迟到响应；UTC来源时间保持可见，重启只重读数据库。

#### Scenario: 局部失败与乱序
- **WHEN** B已选中而A请求迟到，或History失败但Binding成功
- **THEN** A结果不覆盖B；History显示unavailable且不清除独立成功Binding，更不发mutation

#### Scenario: 会话与窄屏
- **WHEN** 会话失效，或在390px/桌面/键盘模式查看
- **THEN** 失效清理会话和内存；有权限时各区和两个Provider badge均可辨识，可操作控件有可访问名称且状态不只靠颜色
