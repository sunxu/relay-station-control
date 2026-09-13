# asset-registry Specification

## Purpose
为单环境 Control 提供可验证、可持久化且不接触外部系统的资产真相源，使管理员能够安全查看 Gateway、Relay Node、Driver 能力、Provider 策略和账号监控状态，并为后续采集与任务能力提供稳定边界。

## Requirements

### Requirement: Control 绑定唯一环境身份
Control MUST 要求部署提供稳定的环境 ID，并 SHALL 在开放 HTTP 监听前读取 `environments` 单例，确认配置的环境 ID 与环境类型均与数据库完全一致。Control MUST NOT 自动创建、替换或修正环境单例，数据库 MUST 拒绝改变既有环境 ID 或环境类型。

#### Scenario: 环境身份匹配
- **WHEN** `CONTROL_ENVIRONMENT_ID`、`CONTROL_ENVIRONMENT` 与数据库单例的 `environment_id`、`environment_type` 完全一致
- **THEN** Control 完成环境身份校验并继续启动

#### Scenario: 环境 ID 或类型不匹配
- **WHEN** 任一配置值与数据库单例不一致
- **THEN** Control 在开放 HTTP 监听前以非零状态退出，并仅记录不含连接串、Secret 或完整数据库内容的差异类型

#### Scenario: 环境配置或单例缺失
- **WHEN** `CONTROL_ENVIRONMENT_ID` 缺失、环境类型无效或数据库中不存在唯一环境单例
- **THEN** Control MUST fail closed，且不得自动写入环境记录

#### Scenario: 修复后恢复启动
- **WHEN** 运维人员修正配置或恢复原有环境单例后重新启动同一版本
- **THEN** Control 重新执行校验并在身份匹配时正常提供服务

### Requirement: 数据库保存单环境资产关系
Control SHALL 以数据库为资产真相源，支持本环境 0..1 current active Gateway 和 0..N historical retired Gateway、按稳定 instance ID 唯一的 Relay Node、版本化 Node Driver，以及每个 Node 对固定 capability 的声明。每个 Node MUST 引用已登记的 Driver 合约版本，其 capability MUST 属于该 Driver 版本允许的固定集合；孤立引用、未知 capability 和重复身份 MUST 被数据库拒绝。

#### Scenario: 空资产注册表
- **WHEN** 环境身份存在但尚未登记 Gateway、Node、Driver 或策略
- **THEN** 资产查询返回有效空状态，而不是伪造、自动发现或从外部系统补全资产

#### Scenario: 唯一 Gateway
- **WHEN** 受控部署流程尝试为同一环境登记第二个 current active Gateway
- **THEN** 数据库拒绝该写入并保留原 Gateway

#### Scenario: Node 使用受支持能力
- **WHEN** Node 引用已登记 Driver 合约版本且声明的每项 capability 都在该版本允许集合中
- **THEN** 该 Node 及其能力能够作为一个一致资产被读取

#### Scenario: Node 使用未知能力
- **WHEN** 受控部署流程登记重复 Node instance ID、未知 Driver 版本或 Driver 未支持的 capability
- **THEN** 整个相关事务失败，且不得留下部分 Node 或 capability 记录

#### Scenario: 重启后身份稳定
- **WHEN** Control 在已有资产记录的数据库上重启
- **THEN** Gateway 和 Node 的稳定身份、Driver 绑定及 capability 声明保持不变

Gateway MUST 仅允许 active -> retired，retired terminal；stable instance_id 不可复用，active/current slot 与 lifecycle/revision shape MUST 由数据库保护。Gateway `management_endpoint` MUST 是 Control-managed HTTP-only origin；Register/Edit/Replace 遇到 `https://` MUST 在 validation 阶段拒绝并保持零 outbound request。该约束不改变 Gateway Account/upstream 或 request data-plane endpoint scheme。

#### Scenario: 历史与current共存
- **WHEN** 已有 retired Gateway，普通 Register 使用全新 identity
- **THEN** 允许创建唯一 current active Gateway，历史 row 与 FK 保留

### Requirement: Endpoint 与 Secret 引用安全隔离
资产 endpoint MUST 是规范化的绝对 `http` 或 `https` URL，包含 host，且不得包含 userinfo、query 或 fragment。Control MAY 在数据库保存 opaque Secret 引用以供后续 Adapter 使用，但 MUST NOT 保存引用所指向的凭证内容；API、UI、日志、指标、Trace 和审计 MUST NOT 返回或记录 Secret 引用值，且 API 只能用布尔值表示 Secret 是否已配置。

#### Scenario: 读取已配置 Secret 的资产
- **WHEN** 管理员读取包含 Secret 引用的 Gateway 或 Node
- **THEN** 响应仅包含 `secret_configured: true`，不包含引用值、凭证内容或可逆派生值

#### Scenario: 非法 endpoint
- **WHEN** 受控部署流程提交相对 URL、不受支持的 scheme、缺少 host 或包含 userinfo、query、fragment 的 endpoint
- **THEN** 数据库写入失败，且诊断不得回显潜在凭证内容

#### Scenario: Endpoint 无网络副作用
- **WHEN** endpoint 被登记或通过资产接口读取
- **THEN** Control 不解析远端能力、不探测连通性，也不向该 endpoint 发起请求

### Requirement: Provider 策略版本不可变且作用域明确
Control SHALL 保存按环境、Node 类型和 Driver 合约版本确定作用域的不可变 Provider 策略版本。每个版本 MUST 包含互不重叠的 `active` 与 `out_of_scope` 规范化 Provider 集合、内容哈希、创建者和 UTC 创建时间；已保存版本 MUST NOT 被更新或删除，相同作用域和内容哈希 MUST 保持唯一。

#### Scenario: 保存有效策略版本
- **WHEN** 受控部署流程保存集合不重叠且内容哈希未在同一作用域出现的策略版本
- **THEN** 数据库接受新版本，同时保持已有版本不变

#### Scenario: 策略集合冲突
- **WHEN** 同一 Provider 同时出现在 `active` 与 `out_of_scope`，或 Provider 名称不是规范化值
- **THEN** 数据库拒绝整个策略版本

#### Scenario: 修改历史策略
- **WHEN** 任意调用方尝试更新或删除已保存策略版本
- **THEN** 数据库拒绝操作并保留原内容、哈希和元数据

### Requirement: Provider 策略绑定和激活历史一致

Control SHALL为每个策略作用域保存唯一作用域绑定及半开区间`[effective_from, effective_to)`的激活历史。作用域绑定MUST指向同一作用域最新登记选择的现有策略版本，并作为并发变更的串行化边界；当前有效策略MUST只由数据库UTC当前时间命中的激活区间确定。同一作用域的区间MUST NOT重叠，生效时间MUST使用数据库UTC当前时间或未来时间且不得回填过去时间。已参与或仍可能参与poll、history eligibility、segment、rollup、compaction或保留期证明的策略版本与激活历史MUST NOT被删除或追溯改写；正常关闭当前open interval只可写数据库当前/未来结束时间，且不得改变已经结束UTC日的历史交集。

#### Scenario: 读取当前策略
- **WHEN** 某作用域存在覆盖数据库当前时间的激活区间
- **THEN** 只读接口返回该区间引用的策略版本及其 active、out-of-scope 集合和生效时间，即使作用域绑定已指向一个未来生效版本

#### Scenario: 没有当前策略
- **WHEN** 某 Node 作用域没有覆盖数据库当前时间的激活区间
- **THEN** 只读接口明确返回未配置状态，不回退到其他 Node 类型、Driver 版本或历史策略

#### Scenario: 重叠或回填激活区间
- **WHEN** 受控部署流程尝试写入与既有区间重叠或早于数据库当前时间的新激活区间
- **THEN** 数据库拒绝写入并保留原绑定和历史

#### Scenario: 绑定跨越作用域
- **WHEN** 作用域绑定引用不同环境、Node 类型或 Driver 合约版本的策略
- **THEN** 数据库拒绝该绑定

#### Scenario: history引用期间删除或追溯改写
- **WHEN** 调用尝试删除仍被poll/summary/rollup/run/retention引用的策略版本或激活行，或改变已结束日的区间边界
- **THEN** PostgreSQL拒绝操作，既有expected slot、segment checksum和coverage语义保持不变

### Requirement: Node 账号监控状态源自显式激活区间

Control SHALL为每个Node保存半开区间`[effective_from, effective_to)`的账号清单监控激活历史，并以数据库UTC当前时间是否落在区间内计算当前状态。启用记录MUST保存固定启用reason、实名actor和数据库创建时间；关闭或预约关闭MUST另存固定关闭reason、实名actor和数据库登记时间。同一Node的区间MUST NOT重叠，新区间不得从过去开始；Gateway状态、Compose状态或Node可达性MUST NOT隐式改变监控状态。仍参与history expected slot、segment、rollup、compaction或保留期证明的激活历史MUST NOT被删除或追溯改写；关闭当前open interval不得改变已经结束UTC日的历史交集。

#### Scenario: 当前处于监控区间
- **WHEN** 数据库当前时间落在某 Node 的一个监控激活区间内
- **THEN** 资产接口将该 Node 报告为 `monitoring_active: true` 并返回区间边界

#### Scenario: 未配置或不在监控区间
- **WHEN** Node 没有激活区间，或当前时间不在任何区间内
- **THEN** 资产接口将该 Node 报告为 `monitoring_active: false`

#### Scenario: 外部运行状态变化
- **WHEN** Gateway、Compose 或 Node 的运行状态发生变化但数据库激活区间未变化
- **THEN** Control 报告的账号监控状态保持不变

#### Scenario: 重叠或回填监控区间
- **WHEN** 受控部署流程尝试写入重叠区间或从数据库当前时间之前开始的区间
- **THEN** 数据库拒绝写入且不修改既有历史

#### Scenario: 关闭操作保留实名元数据
- **WHEN** 受控部署操作立即或预约关闭一个监控区间
- **THEN** 同一历史行保存关闭 reason、actor 和数据库登记时间，NULL、启停 reason 错配或冲突重放均被拒绝

#### Scenario: coverage保留期内改变监控历史
- **WHEN** 调用尝试删除或追溯改写仍可能影响expected slot或已固化coverage的Node监控区间
- **THEN** PostgreSQL拒绝操作，正常未来启停仍通过既有受控路径追加或关闭区间

### Requirement: 管理员可通过受保护只读 API 查看资产
Control SHALL 提供 `GET /api/environment`、`GET /api/assets/gateway`、`GET /api/assets/nodes`、`GET /api/assets/nodes/{instance_id}`、`GET /api/assets/drivers` 和 `GET /api/assets/provider-policies/current`。这些接口 MUST 仅允许现有实名且未过期的 `super_admin` 会话，MUST 使用 `Cache-Control: no-store`，且 MUST NOT 提供本 change 未声明的资产写接口；本 change 仅允许新增已冻结 Gateway lifecycle action，不开放 Node product mutation。

#### Scenario: 管理员读取资产
- **WHEN** 有效 `super_admin` 会话请求任一资产只读接口
- **THEN** Control 返回当前数据库快照的脱敏结果和 `Cache-Control: no-store`

#### Scenario: 未认证读取资产
- **WHEN** 缺少、过期或已撤销的会话请求资产接口
- **THEN** Control 按现有管理员访问契约拒绝请求，且响应不得泄露资产是否存在

#### Scenario: 尝试产品写操作
- **WHEN** 客户端向 Node 资产路径或未声明的 Gateway action 发送 `POST`、`PUT`、`PATCH` 或 `DELETE`
- **THEN** Control 不执行资产变更，且 OpenAPI 不声明此类写操作

#### Scenario: Node 列表分页
- **WHEN** 管理员按 Node 类型、capability 或监控状态过滤并分页读取 Node
- **THEN** Control 使用稳定排序和有上限的页大小返回不重复、不遗漏当前查询快照内记录的结果

Control MUST 保留 `GET /api/assets/gateway` 的 current singular/空状态 envelope；新增 `GET /api/assets/gateways` 和 `GET /api/assets/gateways/{instance_id}`。列表 query 为 `lifecycle=active|retired|all`（默认 active）、`limit`（默认50、最大100）和 opaque `cursor`，排序为 instance_id ASC。detail 返回 predecessor/successor。Gateway projection 包含 lifecycle_status、revision 和 secret_configured，不含 raw reference。精确 HTTP、cursor 一致性和 counts projection 见本 change design 的 HTTP contract；这些都是本 change 的规范契约。每环境 0..1 current active Gateway、0..N retired history。仅 Gateway Register/Edit/Retire/Replace 和独立 observation 是本 change 启用的新 action；Node product mutation 不启用，既有独立 binding API 保持。

#### Scenario: 历史查询
- **WHEN** 请求 lifecycle=retired 的历史列表或 stable identity detail
- **THEN** 只返回相应历史 projection，不复活 retired identity

### Requirement: 只读资产页面处理空状态与故障
Control SHALL 为已认证管理员提供懒加载的资产页面（Node 部分保持只读），展示环境、Gateway、Node、Driver/capability、当前 Provider 策略和监控状态。页面 MUST 只调用 Control 的生成客户端，不得直接调用 Gateway、Node 或其他外部服务，仅可显示本 change 冻结的 Gateway action 控件；不得显示 Node product mutation 控件或 Secret 引用。

#### Scenario: 首次进入资产页面
- **WHEN** 已认证管理员打开资产页面
- **THEN** 页面按需加载并通过 Control API 获取数据，不向已登记 endpoint 发出浏览器请求

#### Scenario: 注册表为空
- **WHEN** 资产接口返回空 Gateway、Node 或策略状态
- **THEN** 页面分别展示可区分的未登记或未配置空状态，而不是报错或构造默认资产

#### Scenario: API 读取失败
- **WHEN** 任一资产 API 返回可重试故障
- **THEN** 页面显示不含内部错误或 Secret 的失败状态和显式重试入口，不将旧数据冒充为当前状态

#### Scenario: Gateway管理入口
- **WHEN** 管理员在现有 Asset Registry 操作 Gateway
- **THEN** 复用生成客户端、CSRF、revision；static prerequisite PASS 后才 rollout mutation UI，不新增第二套页面

### Requirement: 数据库故障不扩散到请求数据面
资产注册能力 SHALL 只依赖 Control 数据库和现有管理员访问边界。读取数据库失败时，资产 API MUST 返回脱敏的可重试 `503`；数据库恢复后，后续请求 MUST 无需重启即可恢复。任何读取、启动校验或页面访问 MUST NOT 创建任务、写入 Outbox、采集账号、修改 Gateway/Node 配置或进入业务请求数据面。

#### Scenario: 数据库读取失败
- **WHEN** 已启动 Control 无法完成资产查询
- **THEN** 资产 API 返回 `503` 和脱敏错误，且不返回缓存资产或触发外部调用

#### Scenario: 数据库恢复
- **WHEN** 数据库连接恢复后管理员再次请求资产接口
- **THEN** Control 从数据库读取并返回当前资产，无需重启或人工刷新缓存

#### Scenario: Control 不可用
- **WHEN** Control 或其资产数据库停止服务
- **THEN** Gateway 和 Relay Node 的既有请求处理继续独立运行

### Requirement: 资产可观测性保持固定低基数
Control SHALL 暴露资产种类数量和读取结果的聚合指标，并 MUST 将标签值限制为代码定义的固定 `asset_kind`、`operation` 和 `result` 枚举。环境 ID、instance ID、endpoint、资产名称、Driver 名称、策略版本、Provider 名称及 Secret 引用 MUST NOT 作为指标标签；环境身份失败和读取失败日志 MUST 使用固定原因码并保持脱敏。

#### Scenario: 导出资产指标
- **WHEN** 指标端点被抓取
- **THEN** 指标仅包含固定枚举标签和聚合数值，不包含逐资产身份或 endpoint

#### Scenario: 记录读取失败
- **WHEN** 资产查询失败
- **THEN** Control 增加固定 `result` 的读取计数并记录脱敏原因码，不记录 SQL 参数、连接串或资产 Secret 引用

### Requirement: Gateway Directory reader reference SHALL 仅通过受控操作首次补填

Control SHALL 提供独立 registrar 操作，只为从未产生任何 Directory run/observation/snapshot/current state 且没有任何 Binding 历史的既有 Gateway 将NULL reader_secret_ref补填为合法非空opaque reference；MUST NOT修改management endpoint或替换非NULL reference。稳定 instance ID、环境身份和名称 MUST 保留，既有 register signature 与严格重放语义 MUST 不变。操作 MUST 以NULL为唯一可写前置值并以原子事务提交；必须防止与首次调度/绑定竞争，拒绝已有采集历史后的首次补填。上述零历史限制仅约束真实变更；授权调用的相同reference重放 SHALL 返回无写入、无新增审计的 no-op，即使其后已有历史。此能力 MUST NOT 成为 runtime 直接 UPDATE 或产品 HTTP/UI mutation。

#### Scenario: 未接入的既有 Gateway
- **WHEN** registrar 为无任何采集/绑定历史的 Gateway 为NULL reader reference提交合法非空reference
- **THEN** 原子补填reference，endpoint保持不变，保留 UUID、环境和名称；之后启用采集可获得 fresh Directory

#### Scenario: 有历史或并发竞争
- **WHEN** 请求将补填NULL reference且已存在成功、失败或未完成的 Directory run、任一 snapshot/observation/current state、任一 Binding 历史，或已配置Gateway已有调度记录
- **THEN** 补全操作拒绝且不修改资产；不能删除历史或失效证据来绕过条件

#### Scenario: 并发补全与重放
- **WHEN** 两个不同reference的补填竞争同一个NULL字段，或原操作被重放
- **THEN** 不允许丢失更新；已达到完全相同reference时返回幂等 no-op，不重复审计，非NULL且不同的reference请求拒绝

### Requirement: Gateway 配置补全 SHALL 保持最小权限与原子审计

新增操作 MUST 使用 migrator 所有的 SECURITY DEFINER 函数、fixed search_path、全限定对象名，撤销 PUBLIC 执行权，仅授予 registrar；runtime 不获得 endpoint/reference UPDATE 或函数 EXECUTE。成功变更与 append-only audit MUST 在同一事务；审计仅包含固定 action、管理员 actor、Gateway UUID、操作时间及非敏感变更标志。失败不得部分更新，审计不得包含 endpoint、reference、token 或其可逆派生值。

#### Scenario: Runtime 和 PUBLIC 拒绝
- **WHEN** runtime 或未授权角色调用补全函数或直接修改 endpoint/reference
- **THEN** permission denied，资产与审计均不改变

#### Scenario: 审计失败
- **WHEN** 合法补全过程的 audit 写入失败
- **THEN** 整个事务回滚；恢复后可安全重试，不出现没有审计的配置修改

#### Scenario: 非法目标与凭据泄漏
- **WHEN** 输入非法或空reference、错误UUID，或尝试替换非NULL reference
- **THEN** 操作拒绝，错误/日志/审计不回显输入，原身份及配置保留

#### Scenario: HTTP登记与补填
- **WHEN** registrar为既有HTTP endpoint补填reference
- **THEN** 资产补全本身不发网络请求；启用Directory后直接使用该endpoint，无额外HTTP许可配置

#### Scenario: NULL reference调度先取得锁
- **WHEN** 无历史Gateway尚未补填，scheduler先取得Gateway行锁
- **THEN** scheduler不建run；registrar随后仍能原子补填，不因启用runtime而锁死首次接入
