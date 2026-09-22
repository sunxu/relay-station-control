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

Control SHALL 以数据库为资产真相源，支持本环境 0..1 current active Gateway 和 0..N historical
retired Gateway、按稳定 `instance_id` 唯一的 Relay Node、版本化 Node Driver，以及每个 Node 对
固定 capability 的声明，并增加 Node active/retired lifecycle、revision 和受限 retirement
metadata。每个 Node MUST 引用已登记的 Driver 合约版本，其 capability MUST 属于该 Driver 版本
允许的固定集合；孤立引用、未知 capability 和重复身份 MUST 被数据库拒绝。Gateway MUST 仅允许
active -> retired，retired terminal；stable instance_id 不可复用，active/current slot 与
lifecycle/revision shape MUST 由数据库保护。Node MUST 只能 active→retired，retired terminal；
历史 identity 不得复用，Node capability ownership、Driver contract 和 stable identity 不得被
lifecycle edit 改写。

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

#### Scenario: Gateway 历史与current共存
- **WHEN** 已有 retired Gateway，普通 Register 使用全新 identity
- **THEN** 允许创建唯一 current active Gateway，历史 row 与 FK 保留

#### Scenario: 历史与current共存
- **WHEN** 已有 retired Gateway，普通 Register 使用全新 identity
- **THEN** 允许创建唯一 current active Gateway，历史 row 与 FK 保留

#### Scenario: Node 历史与current共存
- **WHEN** 已有 retired Node，Register 使用全新 identity，或 Replace 以旧 Node 的 revision
  为前提创建新 identity
- **THEN** 允许创建新的 active Node，retired Node 的历史 row、revision 与 lineage FK 保留，
  不复活为 active

### Requirement: Endpoint 与 protected credential state SHALL remain isolated
资产 endpoint MUST 是规范化的绝对 `http` 或 `https` URL，包含 host，且不得包含 userinfo、query 或 fragment。Control MAY 仅为 Relay Node management credential 与 Gateway Directory credential 保存 approved protected-at-rest representation，但 MUST NOT 保存 plaintext credential；其他 Secret 继续仅允许既有引用模式。API、UI、ordinary query、日志、指标、Trace、审计与业务 surface MUST NOT 返回或记录 plaintext、sealed blob、K2、K2 identity commitment、legacy reference 或 crypto metadata，且 API 只能用布尔值表示 Secret 是否已配置。`secret_configured` MUST 精确定义为 `sealed_credential IS NOT NULL`，不得 decrypt，也不得依赖 K2 availability、Open success 或 blob authenticity。

#### Scenario: 读取已配置 Secret 的资产
- **WHEN** 管理员读取包含 approved protected credential state 的 Gateway 或 Node
- **THEN** 响应仅包含 `secret_configured: true`，不包含 plaintext、sealed blob、K2、K2 commitment、legacy reference、crypto metadata或可逆派生值

#### Scenario: configured projection is independent of credential usability
- **WHEN** sealed credential存在但K2 missing、K2 wrong或ciphertext corrupt
- **THEN** ordinary store/API读取仍返回`secret_configured=true`，不得尝试Open；credential-dependent operation在其owning layer独立fail closed

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

Control SHALL为每个Node保存半开区间`[effective_from, effective_to)`的账号清单监控激活历史，并以 Node active、activation 未取消且数据库UTC当前时间落在有效区间内计算当前状态。启用记录MUST保存固定启用reason、实名actor和数据库创建时间；关闭或预约关闭MUST另存固定关闭reason、实名actor和数据库登记时间。同一Node未取消的有效区间MUST NOT重叠，新区间不得从过去开始；Gateway状态、Compose状态或Node可达性MUST NOT隐式改变监控状态。仍参与history expected slot、segment、rollup、compaction或保留期证明的激活历史MUST NOT被删除或追溯改写；关闭当前open interval不得改变已经结束UTC日的历史交集。

Control SHALL 保留既有 Node monitoring activation history，并支持 current interval close 与
future durable cancellation。cancelled future row MUST 保留原 schedule metadata，generated
`active_range` MUST 为空 range，不得成为 current、eligible 或 expected slot；current close
仍使用 `effective_to` 与既有 end actor/reason metadata。所有 monitoring writer（启用、关闭、
预约关闭、cancellation）MUST 先以 `SELECT ... FOR UPDATE` 锁定目标 Node row，验证
`lifecycle_status = active`，再按稳定 activation 顺序锁 monitoring rows；retired Node MUST NOT
创建新的 current 或 future 监控激活区间。

Stage 3 产品 Enable/Disable MUST 只立即生效，不接受future时间；Node锁内先建立DB boundary，再按effective_from ASC, monitoring_activation_id ASC锁所有current/future。Enable有future即conflict，无future且已有current为no-op，否则创建immediate open interval。Disable同事务关闭current并durable cancel全部future；两者皆无为no-op。reason新增administrator_enable；end_reason/cancel_reason新增administrator_disable，actor/end_actor保持UUID text、cancelled_by保持admin UUID FK。`node_generation` MUST 只在实际改变 monitoring current projection 的变更（Enable创建current、Disable关闭current）时推进一次；no-op（already_enabled/already_disabled）、仅取消future而无current变化、receipt replay、Health、Connection Test MUST NOT 推进generation或Node revision。既有 operational scheduled writer MUST NOT 使用 `node_generation` 作为其自身的 optimistic concurrency token 或 intent precondition；它在intent形成时捕获该Node最新Disable receipt fence，取得Node lock后有界重读，fence变化则conflict且零activation write。同一Node Disable receipt的`committed_at`在Node lock serialization下严格递增，latest不依赖随机command_id；receipt不可UPDATE/DELETE/TRUNCATE且latest不得prune。Disable commit后新形成并捕获最新fence的intent可按既有operational preconditions执行，因此不建立durable disabled latch。所有原Node lifecycle/history/cancellation约束保留，不新增boolean shadow state。

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

#### Scenario: Retire 与 monitoring writer 竞争
- **WHEN** Node Retire 事务先取得 Node row lock 并 commit lifecycle_status=retired
- **THEN** 随后等待该锁的 monitoring writer 在验证 lifecycle_status 时失败，MUST NOT 创建新的
  current 或 future 监控激活区间

#### Scenario: monitoring writer 先于 Retire 提交
- **WHEN** monitoring writer 先取得 Node row lock 并在 Node 仍 active 时提交新激活区间
- **THEN** 该区间正常生效；随后的 Node Retire 仍可关闭该区间的 current interval

#### Scenario: 关闭操作保留实名元数据
- **WHEN** 受控部署操作立即或预约关闭一个监控区间
- **THEN** 同一历史行保存关闭 reason、actor 和数据库登记时间，NULL、启停 reason 错配或冲突重放均被拒绝

#### Scenario: coverage保留期内改变监控历史
- **WHEN** 调用尝试删除或追溯改写仍可能影响expected slot或已固化coverage的Node监控区间
- **THEN** PostgreSQL拒绝操作，正常未来启停仍通过既有受控路径追加或关闭区间

#### Scenario: Stage 3 operations additive surface
- **WHEN** active Node管理员通过受保护的Stage 3路径执行健康或立即监控操作
- **THEN** 仅发生该operation规定的结果，Stage1/2 lifecycle、安全、history和分页语义均保留，retired无可执行operation

### Requirement: 管理员可通过受保护只读 API 查看资产

Control SHALL 提供 `GET /api/environment`、`GET /api/assets/gateway`、`GET /api/assets/gateways`、
`GET /api/assets/gateways/{instance_id}`、`GET /api/assets/nodes`、
`GET /api/assets/nodes/{instance_id}`、`GET /api/assets/drivers` 和
`GET /api/assets/provider-policies/current`。这些接口 MUST 仅允许现有实名且未过期的
`super_admin` 会话，MUST 使用 `Cache-Control: no-store`，且 MUST NOT 提供Stage 1/2/3 changes 之外未声明的资产写接口；仅允许已冻结 Gateway lifecycle action
（Register/Edit/Retire/Replace/独立 observation）和已冻结 Node lifecycle action
（Register/Edit/Retire/Replace），另允许Stage 3的Node Connection Test和immediate Monitoring Enable/Disable。

Control MUST 保留 `GET /api/assets/gateway` 的 current singular/空状态 envelope；
`GET /api/assets/gateways` 与 `GET /api/assets/gateways/{instance_id}` 的列表 query 为
`lifecycle=active|retired|all`（默认 active）、`limit`（默认50、最大100）和 opaque
`cursor`，排序为 instance_id ASC，detail 返回 predecessor/successor。Gateway projection
包含 lifecycle_status、revision 和 secret_configured，不含 raw reference。每环境
0..1 current active Gateway、0..N retired history。

Existing Node list/detail routes MUST remain the single collection/detail surface and MUST become
lifecycle-aware。The default collection is `lifecycle=active`; `retired|all` are explicit,
stable keyset pagination filters bound to an opaque cursor that carries encoding version,
environment, all active filters, `last_instance_id`, the durable `node_registry_generation`
value read from the frozen `asset_registry_generations` singleton table (`node_generation`
column; no in-memory counter), and `read_as_of` — the DB transaction timestamp captured when the
first page's snapshot was read. `node_generation` MUST advance monotonically, in the same
transaction, on every Node Register/Edit/Retire/Replace commit and on any monitoring
current-state change that alters default collection membership or projection; overflow MUST fail
closed and the value MUST NOT decrease. The first page of a list MUST read `node_generation`,
`read_as_of` and the Node rows from one consistent DB snapshot; a cursor whose generation no
longer matches the current value MUST fail with `409 cursor_stale` instead of silently skipping
or duplicating rows. Any monitoring-derived projection or filter (`monitoring_active`, current
interval membership, etc.) evaluated for a subsequent page within the same cursor chain MUST be
computed against the cursor's pinned `read_as_of` instant, not a fresh "now" read on that page;
pure wall-clock passage across an `effective_from`/`effective_to` boundary MUST NOT change
membership within an already-issued cursor chain, since evaluation stays pinned to `read_as_of`.
`node_generation` mismatch (a durable mutation actually happened) is the only trigger for
`409 cursor_stale`. Detail exposes retired metadata and immutable predecessor/successor lineage.
`nodes` remains total for compatibility; additive `node_counts` exposes active/retired/total. All
reads retain super_admin, no-store and DB-failure 503 behavior。

Stage 3 MUST 增加 `GET /api/assets/nodes/{instance_id}/health`（无body、无command_id、无CSRF要求，遵循read-only GET安全惯例）、`POST /api/assets/nodes/{instance_id}/connection-test`（body={}）、`POST /api/assets/nodes/{instance_id}/monitoring-enable`及`/monitoring-disable`（body仅command_id）。Connection Test/Enable/Disable三个POST route均验证session/super_admin/same-origin/CSRF；Health route验证session/super_admin，MUST NOT要求CSRF/same-origin。四个route响应均no-store；其完整response/error/replay契约见relay-node-management-operations。Health与Connection Test均不发起monitoring mutation，普通资产GET不发HTTP观察。Node lifecycle四条路径与Gateway全部Stage1路径保持，不允许Node DELETE或scheduled product API。

#### Scenario: 管理员读取资产
- **WHEN** 有效 `super_admin` 会话请求任一资产只读接口
- **THEN** Control 返回当前数据库快照的脱敏结果和 `Cache-Control: no-store`

#### Scenario: 未认证读取资产
- **WHEN** 缺少、过期或已撤销的会话请求资产接口
- **THEN** Control 按现有管理员访问契约拒绝请求，且响应不得泄露资产是否存在

#### Scenario: 尝试产品写操作
- **WHEN** 客户端向资产路径发送 `POST`、`PUT`、`PATCH` 或 `DELETE`，且目标既不是
  Gateway Stage 1 冻结的 Gateway lifecycle action，也不是Stage 2 change 冻结的四个 Node
  lifecycle mutation 路径（`POST /api/assets/nodes`、`PATCH /api/assets/nodes/{instance_id}`、
  `POST /api/assets/nodes/{instance_id}/retire`、`POST /api/assets/nodes/{instance_id}/replace`），也不是Stage 3的三个POST operations路径
- **THEN** Control 不执行资产变更，且 OpenAPI 不为该路径声明写操作；Node 上的 `DELETE` 永远不存在

#### Scenario: Gateway lifecycle mutation 路径
- **WHEN** 已认证 `super_admin` 会话向 Gateway Stage 1 change 冻结的 Gateway lifecycle
  mutation 路径之一发送带 `command_id` 的合法请求
- **THEN** Control 按 Gateway Stage 1 change 冻结的 canonical intent、锁顺序、receipt replay
  和 audit/metrics 语义执行该 action，本 change 不修改或收窄这些路径

#### Scenario: Node lifecycle mutation 路径
- **WHEN** 已认证 `super_admin` 会话向Stage 2 change 冻结的四个 Node lifecycle mutation 路径之一发送
  带 `command_id` 的合法请求
- **THEN** Control 按 `relay-node-asset-lifecycle` capability 冻结的 canonical
  intent、锁顺序、receipt replay 和 audit/metrics 语义执行该 action，且不放宽其它未声明路径

#### Scenario: Gateway 历史查询
- **WHEN** 请求 `lifecycle=retired` 的 Gateway 历史列表或 stable identity detail
- **THEN** 只返回相应历史 projection，不复活 retired identity

#### Scenario: 历史查询
- **WHEN** 请求 lifecycle=retired 的历史列表或 stable identity detail
- **THEN** 只返回相应历史 projection，不复活 retired identity

#### Scenario: Node 游标 generation 失效
- **WHEN** 分页过程中 `node_registry_generation` 因 Node lifecycle mutation 或监控 current-state
  变化而前进
- **THEN** 携带旧 generation 的 cursor 返回 `409 cursor_stale`，客户端 MUST 使用不带 cursor 的请求
  重新开始分页

#### Scenario: 纯时间流逝不改变已发 cursor 链的 membership
- **WHEN** 一个已发出的 cursor chain 尚未翻页完成，且期间没有任何 Node lifecycle mutation 或
  monitoring current-state 变化推进 `node_registry_generation`，仅 wall-clock 时间跨越了某个
  monitoring activation 的 `effective_from`/`effective_to` boundary
- **THEN** 后续页必须按该 cursor 冻结的 `read_as_of` 计算 monitoring-derived projection/filter，
  membership 与首页保持一致，不因 boundary 跨越而变化，且不返回 `409 cursor_stale`

#### Scenario: Node 列表分页
- **WHEN** 管理员按 Node 类型、capability 或监控状态过滤并分页读取 Node
- **THEN** Control 使用稳定排序和有上限的页大小返回不重复、不遗漏当前查询快照内记录的结果

#### Scenario: Stage 3 operations additive surface
- **WHEN** active Node管理员通过受保护的Stage 3路径执行健康或立即监控操作
- **THEN** 仅发生该operation规定的结果，Stage1/2 lifecycle、安全、history和分页语义均保留，retired无可执行operation

### Requirement: 只读资产页面处理空状态与故障

Control SHALL 为已认证管理员提供受控的资产管理 presentation，展示 Environment、Gateway、Node、Driver/capability、当前 Provider Policy 和 monitoring state。Phase 10 MAY 将 presentation ownership 拆分为 `/assets` 辅助资产与运行配置入口和 `/nodes` Relay Node 主要页面，但所有页面 MUST 只调用 Control 的生成客户端，不得直接调用 Gateway、Node 或其他外部服务。

`/assets` SHALL 继续承载 Environment identity、Gateway lifecycle management、Driver catalog 与 Current Provider Policy；Gateway Stage 1 change 冻结的 Register/Edit/Retire/Replace/history contract MUST 保持。

`/nodes` SHALL 成为 Node lifecycle / monitoring 的唯一主要 presentation owner，并 MUST 保留 Stage 2/3 已冻结的 Node Register、Edit、Retire、Replace、retired history/lineage、Health、Connection Test、Monitoring Enable、Monitoring Disable、`expected_revision`、credential configured-state 与错误语义。Stage 3B 完成后 `/assets` MUST NOT 保留第二套可执行 Node lifecycle / health / monitoring 控件；可仅保留指向 `/nodes` 的 navigation-only 入口。

Health 与 Connection Test 均仅显式点击执行，各自独立展示安全结果且不写 browser storage；页面 mount/reload MUST NOT 自动触发任一 observation、不建立 health history、不后台轮询。Monitoring mutation 后丢弃旧 cursor 并重新读取 current detail/list；already_enabled/already_disabled 明确提示，future 冲突指向受控运维流程，retired 冲突刷新历史，command 冲突不得自动换 UUID 重做。无 future schedule picker、credential/account editor 或新 router framework。

`/assets` 与 `/assets/` 直达及 reload MUST 继续渲染 Asset Registry auxiliary surface；`/nodes` 与 `/nodes/` MUST 通过同一 SPA fallback 与 frontend route normalization 渲染 Relay Nodes surface。API router 与 `/static/` namespace 行为保持。

#### Scenario: 首次进入资产页面
- **WHEN** 已认证管理员打开资产页面
- **THEN** 页面按需加载 Environment、Gateway、Driver catalog 与 Current Provider Policy，并通过 Control API 获取数据
- **AND** 浏览器不向已登记 endpoint 发出直接请求

#### Scenario: 首次进入 Relay Nodes 页面
- **WHEN** 已认证管理员打开 `/nodes` 或 `/nodes/`
- **THEN** 页面展示 Node lifecycle / monitoring surface，并继续使用既有 Control generated client、CSRF、revision 与 credential configured-state contract
- **AND** 浏览器不直接请求 Node endpoint

#### Scenario: 注册表为空
- **WHEN** Environment/Gateway/Driver/Policy 或 Node API 返回合法空状态
- **THEN** 对应页面展示可区分的未登记、未配置或空状态，而不是构造默认资产或伪造成功状态

#### Scenario: API 读取失败
- **WHEN** 任一资产 API 返回可重试故障
- **THEN** owning 页面显示不含内部错误或 Secret 的失败状态和显式重试入口，不将旧数据冒充当前状态

#### Scenario: Gateway管理入口
- **WHEN** 管理员在 `/assets` 操作 Gateway
- **THEN** 复用既有生成客户端、CSRF、revision、command 与 credential contract
- **AND** Phase 10 不新增第二套 Gateway mutation owner

#### Scenario: Node lifecycle mutation 控件范围
- **WHEN** 管理员在资产页面发起 Node Register、Edit、Retire 或 Replace
- **THEN** 可执行控件位于 `/nodes` owning surface，并继续使用既有 lifecycle、revision、transaction 与 audit contract
- **AND** Stage 3 operations 通过独立的已声明路径执行，不改变这些 lifecycle 流程

#### Scenario: retired 历史详情导航
- **WHEN** 管理员在 `/nodes` 查看一个 retired Node 的详情
- **THEN** 页面显示其 predecessor/successor lineage 与 retirement metadata
- **AND** 不提供任何使其复活为 current 的操作

#### Scenario: explicit probe remains user-triggered
- **WHEN** 管理员仅打开或刷新 `/nodes`
- **THEN** 页面 MUST NOT 自动执行 Health 或 Connection Test
- **AND** 只有管理员显式点击对应操作才调用既有 probe/action endpoint

#### Scenario: Stage 3 operations additive surface
- **WHEN** active Node 管理员通过受保护的 Stage 3 路径执行健康或立即监控操作
- **THEN** 仅发生该 operation 规定的结果，Stage 1/2 lifecycle、安全、history 和分页语义均保留，retired 无可执行 operation

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

### Requirement: Gateway Directory credential state SHALL use the protected asset lifecycle

Gateway Directory credential MUST be represented by the approved protected-at-rest state and managed through the existing authenticated asset command flow. Register missing means unconfigured; Edit missing means keep and explicit null means clear; Replace missing means unconfigured and MUST NOT inherit the predecessor credential. Set, clear, Retire and Replace predecessor erase MUST preserve command identity, revision, lineage, receipt and audit atomicity. Runtime and registrar MUST NOT directly update credential state outside the owning protected path.

#### Scenario: unconfigured Gateway
- **WHEN** a Gateway has no protected Directory credential
- **THEN** scheduler returns no-work without creating a Directory run, resolving a credential or sending a request; the Gateway may later be configured through the authenticated asset flow

#### Scenario: configured Gateway
- **WHEN** a Gateway has a non-NULL protected Directory credential and is active
- **THEN** Directory scheduling may create a run and the fenced resolver may Open the credential only for the bounded authenticated fetch

#### Scenario: credential erase and replacement
- **WHEN** an administrator clears, Retires, or Replaces a Gateway with an unconfigured successor
- **THEN** sealed credential state is erased atomically with the lifecycle command, no K2 is required for the erase, and no credential is inherited

### Requirement: Gateway protected credential access SHALL remain least-privilege and auditable

Protected credential reads and writes MUST use the approved narrow internal access path with fixed owner/search_path and grants. Runtime MUST receive only the ephemeral plaintext needed for one bounded outbound; ordinary runtime, registrar and PUBLIC roles MUST NOT directly read or write protected columns. Successful mutation and append-only audit MUST commit atomically, and audit must not contain endpoint, plaintext, sealed value, K2, commitment or legacy reference.

#### Scenario: unauthorized protected access
- **WHEN** runtime, registrar or PUBLIC attempts direct protected-column access or an unapproved mutation
- **THEN** permission is denied and asset/audit state is unchanged

#### Scenario: audit failure
- **WHEN** a valid credential mutation cannot write its audit record
- **THEN** the complete transaction rolls back without a partially applied credential change

### Requirement: Management endpoint UI SHALL validate internal HTTP URLs

Gateway 与 Node management endpoint 表单 MUST 使用同一小型 internal HTTP validator，而不是 public-Web URL validator。该 validator MUST 接受 single-label Docker DNS、`host.docker.internal`、域名、IPv4、可选合法端口及满足后端既有约束的 base path；MUST 拒绝 HTTPS、其它 scheme、缺失 scheme/host、userinfo、query、fragment、control character、无效 port 与不安全 encoded path。UI validation 仅提供即时反馈，Go admission 与 PostgreSQL durable constraints 仍是权威安全边界。

#### Scenario: Docker DNS hostname
- **WHEN** 管理员输入 `http://gateway:8080` 或 `http://node:8317`
- **THEN** 表单接受该 endpoint，且不显示 public-Web URL 格式错误

#### Scenario: OrbStack host alias
- **WHEN** 管理员输入 `http://host.docker.internal:8080`
- **THEN** 表单接受该 endpoint

#### Scenario: HTTPS 与 malformed endpoint
- **WHEN** 管理员输入 `https://gateway:8080`、`ftp://gateway`、`gateway:8080`、userinfo、query、fragment 或 malformed endpoint
- **THEN** 表单拒绝并给出固定 HTTP-only 安全提示
