## MODIFIED Requirements

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

### Requirement: Node 账号监控状态源自显式激活区间

Control SHALL为每个Node保存半开区间`[effective_from, effective_to)`的账号清单监控激活历史，并以 Node active、activation 未取消且数据库UTC当前时间落在有效区间内计算当前状态。启用记录MUST保存固定启用reason、实名actor和数据库创建时间；关闭或预约关闭MUST另存固定关闭reason、实名actor和数据库登记时间。同一Node未取消的有效区间MUST NOT重叠，新区间不得从过去开始；Gateway状态、Compose状态或Node可达性MUST NOT隐式改变监控状态。仍参与history expected slot、segment、rollup、compaction或保留期证明的激活历史MUST NOT被删除或追溯改写；关闭当前open interval不得改变已经结束UTC日的历史交集。

Control SHALL 保留既有 Node monitoring activation history，并支持 current interval close 与
future durable cancellation。cancelled future row MUST 保留原 schedule metadata，generated
`active_range` MUST 为空 range，不得成为 current、eligible 或 expected slot；current close
仍使用 `effective_to` 与既有 end actor/reason metadata。所有 monitoring writer（启用、关闭、
预约关闭、cancellation）MUST 先以 `SELECT ... FOR UPDATE` 锁定目标 Node row，验证
`lifecycle_status = active`，再按稳定 activation 顺序锁 monitoring rows；retired Node MUST NOT
创建新的 current 或 future 监控激活区间。

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

### Requirement: 管理员可通过受保护只读 API 查看资产

Control SHALL 提供 `GET /api/environment`、`GET /api/assets/gateway`、`GET /api/assets/gateways`、
`GET /api/assets/gateways/{instance_id}`、`GET /api/assets/nodes`、
`GET /api/assets/nodes/{instance_id}`、`GET /api/assets/drivers` 和
`GET /api/assets/provider-policies/current`。这些接口 MUST 仅允许现有实名且未过期的
`super_admin` 会话，MUST 使用 `Cache-Control: no-store`，且 MUST NOT 提供本 change 与
Gateway Stage 1 change 之外未声明的资产写接口；仅允许已冻结 Gateway lifecycle action
（Register/Edit/Retire/Replace/独立 observation）和已冻结 Node lifecycle action
（Register/Edit/Retire/Replace）。

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

#### Scenario: 管理员读取资产
- **WHEN** 有效 `super_admin` 会话请求任一资产只读接口
- **THEN** Control 返回当前数据库快照的脱敏结果和 `Cache-Control: no-store`

#### Scenario: 未认证读取资产
- **WHEN** 缺少、过期或已撤销的会话请求资产接口
- **THEN** Control 按现有管理员访问契约拒绝请求，且响应不得泄露资产是否存在

#### Scenario: 尝试产品写操作
- **WHEN** 客户端向资产路径发送 `POST`、`PUT`、`PATCH` 或 `DELETE`，且目标既不是
  Gateway Stage 1 冻结的 Gateway lifecycle action，也不是本 change 冻结的四个 Node
  lifecycle mutation 路径（`POST /api/assets/nodes`、`PATCH /api/assets/nodes/{instance_id}`、
  `POST /api/assets/nodes/{instance_id}/retire`、`POST /api/assets/nodes/{instance_id}/replace`）
- **THEN** Control 不执行资产变更，且 OpenAPI 不为该路径声明写操作；Node 上的 `DELETE` 永远不存在

#### Scenario: Gateway lifecycle mutation 路径
- **WHEN** 已认证 `super_admin` 会话向 Gateway Stage 1 change 冻结的 Gateway lifecycle
  mutation 路径之一发送带 `command_id` 的合法请求
- **THEN** Control 按 Gateway Stage 1 change 冻结的 canonical intent、锁顺序、receipt replay
  和 audit/metrics 语义执行该 action，本 change 不修改或收窄这些路径

#### Scenario: Node lifecycle mutation 路径
- **WHEN** 已认证 `super_admin` 会话向本 change 冻结的四个 Node lifecycle mutation 路径之一发送
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

### Requirement: 只读资产页面处理空状态与故障

Control SHALL 为已认证管理员提供懒加载的资产页面，展示环境、Gateway、Node、Driver/capability、
当前 Provider 策略和监控状态。页面 MUST 只调用 Control 的生成客户端，不得直接调用 Gateway、Node
或其他外部服务，不新增第二套页面。页面 MUST 显示 Gateway Stage 1 change 冻结的 Gateway lifecycle
控件（Register/Edit/Retire/Replace/history）。本 change 拥有 Node lifecycle 的读取投影与
mutation 控件：页面 MUST 提供 Node Register、Edit、Retire 确认、Replace 流程、
`expected_revision` 冲突刷新提示、retired 历史详情与 predecessor/successor 导航，以及仅提示
`secret_configured` 而不回显 Secret 引用的输入控件。页面 MUST NOT 显示 Connection Test、
Monitoring Enable 或 Monitoring Disable 控件（属于 `add-relay-node-management-operations`）。

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
- **THEN** 页面只调用本 change 冻结的四个 lifecycle mutation 路径，`expected_revision` 冲突时提示
  刷新而不是静默重试，且不显示 Connection Test 或 Monitoring Enable/Disable 入口

#### Scenario: retired 历史详情导航
- **WHEN** 管理员查看一个 retired Node 的详情
- **THEN** 页面显示其 predecessor/successor lineage 与 retirement metadata，不提供任何使其复活为
  current 的操作
