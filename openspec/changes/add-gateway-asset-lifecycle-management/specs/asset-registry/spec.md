## MODIFIED Requirements

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
