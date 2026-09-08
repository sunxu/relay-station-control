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
- **THEN** 仅deep-link稳定Node ID；原页面自己的CSRF/view audit保留，不由Topology绕过或执行该管理页面查询；独立Account Quality只读投影可复用Inventory安全读取，不改变原页面审计

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

页面及新history读取SHALL沿用实名super_admin会话、no-store与request ID。canonical account_key按最新duplicate规格仅供管理员文本展示，MUST NOT进入浏览器导航URL/storage/logs/metrics；仅Account Request History GET的account_key参数及内部cursor允许在认证HTTP请求URL中传递账号身份；不得泄露凭据或raw response。各区SHALL独立loading/empty/unavailable/retry，401清理全部会话数据，切换Node/筛选丢弃迟到响应；时间字段的 instant 来源和传输契约保持不变，用户可见时间 MUST 按浏览器系统时区以 `YYYY-MM-DD HH:mm:ss` 展示。重启只重读数据库。

#### Scenario: 局部失败与乱序

- **WHEN** B已选中而A请求迟到，或History失败但Binding成功
- **THEN** A结果不覆盖B；History显示unavailable且不清除独立成功Binding，更不发mutation

#### Scenario: 会话与窄屏

- **WHEN** 会话失效，或在390px/桌面/键盘模式查看
- **THEN** 失效清理会话和内存；有权限时各区和两个Provider badge均可辨识，可操作控件有可访问名称且状态不只靠颜色，时间按系统时区以固定格式显示

### Requirement: Inventory 账号集合 SHALL 驱动 Account Quality

Node detail SHALL 提供单账号Account Quality表格，以现有Node current Inventory集合为准，LEFT merge已有15m/1h统计。MUST返回account_key opaque string、email、provider、quality、request_count、success_count、failure_count、success_rate、p95_latency_ms、last_success_at、last_failure_at、last_failure_class。MUST NOT由event枚举账号或修改Inventory/lifecycle/Binding/Duplicate/CLIProxy状态。

#### Scenario: 零请求账号仍可见
- **WHEN** Inventory账号存在且窗口内无请求
- **THEN** 账号仍返回，三项count为0、rate/p95/last_*为null、quality为unknown

#### Scenario: Unresolved 不污染账号
- **WHEN** 同Node包含account_key=NULL事件或只有events没有Inventory的账号
- **THEN** 不归属其它账号且不制造fake row；原Node/Provider聚合不变

### Requirement: Quality SHALL 仅表示近期请求表现

分类 MUST仅存在read/API/UI层：count=0为unknown；有请求时success_rate>=95%为good，>=80%且<95%为degraded，<80%为bad。Latency MUST NOT参与分类或制造持久health score。

#### Scenario: 分类边界
- **WHEN** 请求成功率分别为100%、95%、94%、80%、79%
- **THEN** 分别为good、good、degraded、degraded、bad；active Inventory与bad并存

### Requirement: Account Quality API SHALL 有界且安全

GET `/api/topology/nodes/{instance_id}/account-quality` SHALL使用既有super_admin read授权、no-store，默认window15m/provider All/quality All/limit25；仅允许15m或1h，quality good/degraded/bad/unknown，limit1–100。SHALL provider和quality过滤后按account_key稳定cursor分页；cursor MUST绑定Node与filters。MUST复用已有统计，不逐账号发起Go到DB或HTTP请求。

#### Scenario: 过滤跨越Inventory页
- **WHEN** 满足quality的账号位于第一个Inventory chunk之后
- **THEN** 仍在对应过滤分页中可见，页大小有界且顺序确定

#### Scenario: 授权与参数
- **WHEN** 无session、非super_admin、非法window/quality/limit或跨filter cursor
- **THEN** 分别返回401、403或400，不读取账号结果

#### Scenario: 数据库失败和恢复
- **WHEN** read model/DB失败后重试恢复
- **THEN** 失败返回503不可用，不能empty或unknown；恢复后返回真实结果

#### Scenario: 100账号读取
- **WHEN** Node包含100 Inventory账号，查询两种窗口或provider/quality过滤
- **THEN** 使用一个store composition数据库请求，无账号级网络fan-out，记录内部函数成本和latency

### Requirement: Account Quality UI SHALL 区分四种读取状态

现有Node detail SHALL增加Account/Provider/Quality/Success/Requests/P95/Last Failure只读表格，支持15m/1h、现有Node Provider集合、All/Good/Degraded/Bad/Unknown、bounded pagination。SHALL以account_key为row identity；无值显示—，失败仅class和时间；MUST NOT展示raw failure body、mutation、request detail、chart或account action；仅允许View History进入本Node内的单账号只读Request History。

#### Scenario: Loading empty unknown unavailable
- **WHEN** 请求等待、成功空集合、账号无请求、请求错误
- **THEN** 分别显示loading、empty、Unknown账号行、Unavailable，不互相替代

#### Scenario: 筛选分页和Node切换
- **WHEN** 切换window/provider/quality或Node
- **THEN** 使用匹配参数读取并重置分页，旧Node延迟结果不能覆盖新Node；下一页保留筛选

#### Scenario: 只读交互
- **WHEN** 浏览、切换筛选、翻页或重试
- **THEN** 仅执行读取，不出现account action、quota、inspection、automation或全局health score

### Requirement: Topology SHALL 提供单账号Request History区域

Account Quality SHALL通过View History以account_key选择目标账号，在当前Node detail显示最近7天的Time/Model/Result/Failure/Latency/Request ID。成功显示Success及failure —，失败显示Failed及原五类failure；duration NULL和request ID空显示—。MUST NOT展示raw body/headers/tokens/prompt/completion、详情页、chart/export/search/date picker、retry request或account actions。

#### Scenario: 五种读取状态
- **WHEN** 无选中账号、等待请求、账号存在无事件、读取失败或存在事件
- **THEN** 分别显示Select an account to view request history、独立Loading、7天无事件Empty、Unavailable和历史表；404提示账号不存在，不能把错误当empty

#### Scenario: 分页和账号切换
- **WHEN** 翻页或选择另一个账号
- **THEN** 翻页使用专属cursor，换账号清cursor，用精确account_key而非email/index/display text发请求

#### Scenario: Node切换和迟到结果
- **WHEN** 切换Node时旧History未返回
- **THEN** 清除已选账号并取消旧请求，迟到结果不得覆盖新Node；Quality筛选分页可保留同Node明确账号上下文

#### Scenario: 无副作用
- **WHEN** 选择账号、读历史、翻页或读取错误重试
- **THEN** 仅查询既有证据，不改变Inventory/lifecycle/Binding/Duplicate/quality classification或CLIProxy状态

### Requirement: Topology SHALL 展示只读Account Incidents

现有Node detail SHALL提供Incidents七列表Account/Provider/Reason/Status/Hits/First Seen/Last Seen，突出Active且固定active-only，支持Provider/四类Reason过滤和有界分页。点击Account MUST使用精确account_key打开已有Request History。MUST NOT增加resolve/disable/账号retry/ack/comment或其它mutation，也不进入通知、自动处置、Durable Jobs、Prometheus/Grafana。

#### Scenario: 四种读取状态
- **WHEN** 等待读取、成功无匹配、读取错误或有Incident
- **THEN** 分别Loading/Empty/Unavailable/Populated，503不能被解释成Empty

#### Scenario: 过滤分页与History
- **WHEN** 切换provider/reason、翻页或点击Account
- **THEN** 过滤变化回首页，分页保留filters，History复用当前Node和canonical account_key而非email/index

#### Scenario: Node与会话隔离
- **WHEN** 切Node或旧请求迟到、当前session失效
- **THEN** 清Node范围分页与History选择、取消旧query，迟到success/401不污染新Node；当前401按现有方式清理会话

#### Scenario: 无副作用
- **WHEN** 查看active或读取失败后重试
- **THEN** 只读取现有证据，不改变Inventory/lifecycle/Binding/Duplicate/Quality/CLIProxy状态，不展示recovered伪状态

### Requirement: Topology SHALL 复用统一账号列表和只读详情

Control MUST以/topology作为唯一账号UI入口，以Inventory为账号集合显示账号状态与请求质量；MUST提供provider/email/basic_status/lifecycle/window/quality及25/50/100有界分页与独立手动容量诊断。MUST使用account_key打开既有请求历史及采集抽屉。账号无窗口内请求MUST显示Unknown，read failure MUST显示Unavailable。账号状态与请求质量MUST独立，不改变Inventory、Binding、Duplicate Ownership或运行时状态。MUST删除/account-inventory前端路由和导航，不提供旧链接重定向或alias；后端HTTP API保留。

#### Scenario: 双入口与详情
- **WHEN** 管理员选择Topology Node查看账号
- **THEN** 使用唯一AccountList和只读详情，默认present/15m/25并保留missing等记录；原独立页面与跳转链接不存在

#### Scenario: 无请求与不可用
- **WHEN** Inventory账号存在但窗口内无请求，或质量读取失败
- **THEN** 前者保留账号且显示Unknown/0/null，后者Unavailable而非空列表

#### Scenario: 切换Node与Incident
- **WHEN** 切换Node、浏览器popstate或Incident选择账号
- **THEN** Node变化清空旧筛选/页/详情并取消隔离旧请求，自动默认首页；Incident按当前Node/account_key打开同一详情

#### Scenario: 容量诊断
- **WHEN** 管理员手动刷新容量，返回可用/超限/禁用或失败
- **THEN** 展示现有环境容量及超限原因或Unavailable，401沿用会话退出；不改变调度或账号状态

#### Scenario: 旧前端地址移除
- **WHEN** 打开/account-inventory或其instance_id链接
- **THEN** 按既有未知路径规则处理，不加载旧页，不读取该参数查询账号，也不跳转到Topology

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

### Requirement: Account Quality SHALL 默认展示当前账号并允许生命周期过滤

Node Topology Account Quality UI MUST 默认请求 lifecycle=present，提供全部及现有四种生命周期选择。GET account-quality MUST 接受可选 lifecycle；省略参数 MUST 保持既有全部 Inventory 账号语义。生命周期过滤 MUST 在quality筛选和有界keyset分页之前作用于 Inventory 账号集合，不在浏览器分页后隐藏行。cursor MUST 绑定生命周期；改变筛选 MUST 重置页面cursor。不改变Quality分类、Inventory truth、History、Incidents、collector或数据面。

#### Scenario: 默认当前账号与零请求

- **WHEN** 打开某Node Account Quality，当前Inventory包含present与missing账号
- **THEN** 默认只查询present，其中零请求账号仍显示Unknown

#### Scenario: 显式查看缺失或全部

- **WHEN** 管理员选择missing或清空生命周期
- **THEN** 分别查询missing或全部Inventory账号，cursor重置，分页前过滤且不会漏页

#### Scenario: 兼容既有调用

- **WHEN** HTTP调用未传lifecycle或使用旧v1数据库读函数
- **THEN** 保持原全部Inventory账号行为；新v2仅新增安全只读查询权限，不赋予runtime直接SELECT

#### Scenario: 非法筛选或游标不匹配

- **WHEN** lifecycle非法或cursor绑定的lifecycle与当前请求不同
- **THEN** 返回400，不执行查询；数据库失败仍返回503，不伪装Empty或Unknown
