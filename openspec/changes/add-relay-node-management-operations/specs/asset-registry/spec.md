## MODIFIED Requirements

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

Control SHALL 为已认证管理员提供懒加载的资产页面，展示环境、Gateway、Node、Driver/capability、
当前 Provider 策略和监控状态。页面 MUST 只调用 Control 的生成客户端，不得直接调用 Gateway、Node
或其他外部服务，不新增第二套页面。页面 MUST 显示 Gateway Stage 1 change 冻结的 Gateway lifecycle
控件（Register/Edit/Retire/Replace/history）。Stage 2 change 拥有 Node lifecycle 的读取投影与
mutation 控件：页面 MUST 提供 Node Register、Edit、Retire 确认、Replace 流程、
`expected_revision` 冲突刷新提示、retired 历史详情与 predecessor/successor 导航，以及仅提示
`secret_configured` 而不回显 Secret 引用的输入控件。Stage 3 MUST在active Node detail提供四个独立控件：Health（显式"检查健康"读取）、Connection Test（独立显式admin action）、Monitoring Enable及Monitoring Disable，按其当前状态和固定错误给予反馈；retired Node只保留历史查看，不提供可执行operation。

Operations MUST只调用Control生成客户端，不直接请求Node。Health与Connection Test均仅显式点击执行，各自独立展示安全结果且不写browser storage，页面mount/reload MUST NOT自动触发任一observation、不建立health history、不后台轮询。Monitoring mutation后丢弃旧cursor并重新读取current detail/list；already_enabled/already_disabled明确提示，future冲突指向受控运维流程，retired冲突刷新历史，command冲突不得自动换UUID重做。无future schedule picker、credential/account editor或新router。/assets与/assets/直达及reload必须渲染Asset Registry；static prerequisite仍独立，未验收不得rollout。

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

#### Scenario: Node lifecycle mutation 控件范围
- **WHEN** 管理员在资产页面发起 Node Register、Edit、Retire 或 Replace
- **THEN** 页面只调用Stage 2 change 冻结的四个 lifecycle mutation 路径，`expected_revision` 冲突时提示
  刷新而不是静默重试，Stage 3 operations通过独立的已声明路径执行，不改变这些lifecycle流程

#### Scenario: retired 历史详情导航
- **WHEN** 管理员查看一个 retired Node 的详情
- **THEN** 页面显示其 predecessor/successor lineage 与 retirement metadata，不提供任何使其复活为
  current 的操作

#### Scenario: Stage 3 operations additive surface
- **WHEN** active Node管理员通过受保护的Stage 3路径执行健康或立即监控操作
- **THEN** 仅发生该operation规定的结果，Stage1/2 lifecycle、安全、history和分页语义均保留，retired无可执行operation
