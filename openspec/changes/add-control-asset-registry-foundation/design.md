## Context

参见 [proposal.md](./proposal.md) 的动机。Control 当前已有 `environments` 单例、PostgreSQL 连接、管理员认证、OpenAPI 生成链和 React 管理壳，但环境单例尚未参与启动身份校验，也没有 Gateway/Node/Driver/策略/监控区间模型。阶段 1 后续的 Adapter、账号清单采集和持久任务都需要这些稳定外键；本 change 必须先建立纯数据库、只读、无外部调用的基础。

以下约束决定实现方式：Control 是单环境模块化单体且永不进入请求数据面；PostgreSQL 18 和不可变 forward Goose Migration 是数据真相源；API 以 `api/openapi.yaml` 为真相源；Go 与 TypeScript 生成物不得手改；所有时间为 PostgreSQL `timestamptz`/UTC；Secret 仅保存 opaque 引用，引用值本身也视为敏感信息。

## Goals / Non-Goals

**Goals:**

- 用关系约束表达单环境、唯一 Gateway、稳定 Node 身份、Driver/capability 兼容性、策略不可变性及时间区间不重叠，而不是只依赖 Go 校验。
- 在应用监听前完成环境身份校验，使错误数据库、错误环境或错误配置不能以“空资产”形式静默运行。
- 提供稳定、分页、脱敏的只读 API/UI 以及可恢复的数据库故障语义。
- 为后续 change 留出清晰外键和查询接口，但不提前引入 Adapter、调度器、Outbox 或业务写 API。

**Non-Goals:**

- 不自动发现 Gateway/Node，不连接其 Management API，也不验证 endpoint 的 DNS、证书或可达性。
- 不导入或采集账号，不解释 Provider 策略之外的运行时账号状态。
- 不设计资产自助登记、策略编辑、监控启停或审批工作流；本 change 的数据由受控 Migration/部署流程维护。
- 不实现 Gateway 接受快照、漂移检测、desired state、任务、租约、重试、SSE 或通知。

## Decisions

### 1. 启动时把运行配置绑定到数据库环境单例

新增必填 `CONTROL_ENVIRONMENT_ID`，继续使用现有 `CONTROL_ENVIRONMENT` 表示 `dev|staging|production`。数据库连接和 Migration 检查完成后、HTTP listener 创建前，应用以只读查询取得 `singleton_id = 1`，使用常量时间无关的精确字符串比较确认 ID 和类型。缺失、重复/损坏、查询失败或任一不一致均直接返回启动错误；错误日志只使用 `missing_config`、`missing_row`、`id_mismatch`、`type_mismatch`、`database_unavailable` 等固定原因码，不打印期望值、实际值或连接串。

新 Migration 为 `environments.environment_id` 与 `environment_type` 增加不可变保护，允许将来单独修改展示名称，但拒绝改变身份字段或删除单例。应用不得调用现有 `CreateEnvironment` 自动修复。这样恢复路径只有修正部署配置或恢复原数据库，避免误连数据库后形成第二套资产真相。

替代方案是把数据库值当作配置，或在首次启动自动插入。前者无法发现错连环境，后者会把部署错误固化，因此不采用。该决定是部署配置 breaking change：升级前必须先读取既有 `environment_id` 并配置环境变量。

### 2. 使用规范化关系表和复合外键表达资产不变量

新增一个 forward Migration，包含以下表；所有表均使用 UTC `timestamptz`，业务 ID 使用 UUID，受控流程生成的字符串 ID/名称设定明确长度上限：

- `gateway_instances`：`singleton_id = 1` 强制本环境至多一行；保存稳定 `instance_id`、显示名称、规范化 endpoint、opaque `reader_secret_ref` 和创建/更新时间。
- `node_drivers`：以 `(node_type, driver_contract_version)` 为主键，保存固定显示元数据与生命周期状态；定义由代码/Migration 控制，不接收任意产品输入。
- `driver_capabilities`：以 `(node_type, driver_contract_version, capability)` 为主键，capability 受数据库枚举检查约束，例如本阶段只允许 `management_health_read` 与 `management_account_inventory_read`。
- `relay_node_assets`：以稳定 `instance_id` 为主键，保存显示名称、Node 类型、Driver 合约版本、规范化 management endpoint 和 opaque Secret 引用；复合外键保证 Driver 已登记。
- `node_capabilities`：同时携带 Node 的 `instance_id`、Node 类型、Driver 版本和 capability，通过复合外键校验 Node 身份与 `driver_capabilities`，使 Node 只能声明其 Driver 支持的固定能力。

每个 Node 的登记由单一数据库事务完成，先写资产再写 capability；任一步失败即全部回滚。此 change 不提供运行时写方法，因此不需要 HTTP 幂等键；受控脚本/Migration 使用主键、唯一键和 `ON CONFLICT DO NOTHING` 仅实现“相同内容可重放”，冲突内容必须报错，不能覆盖。

替代方案是把 capability 存为 JSONB。JSONB 简化写入，但无法用外键证明 Driver 支持关系，也会让过滤和演进缺少约束，因此采用关系表。表仍带环境含义但不重复 `environment_id`：数据库本身就是单环境，重复字段会制造两个可能不一致的身份源。

### 3. Endpoint 使用数据库域约束，Secret 引用从读取模型中剔除

Migration 定义可复用的 endpoint 检查函数或 domain，接受绝对 `http`/`https` URL，要求 host，拒绝 userinfo、query、fragment、控制字符和超长值。受控登记工具在写入前使用同一规则规范化 scheme/host 大小写和默认端口，数据库约束作为最终防线。本阶段不做 DNS 解析、私网分类、TLS 握手或 HTTP 探测；这些属于未来 Adapter 的逐连接 SSRF 校验。

Secret 列只保存秘密管理系统的 opaque 引用。sqlc 的 API 查询不选择这些列，只计算 `reader_secret_ref IS NOT NULL AS secret_configured`；生成的响应 DTO 不含 Secret 字段。结构化日志、Trace、指标和错误映射只携带固定操作/原因枚举，禁止附带 SQL 参数。即使引用本身不能直接解密，它仍可能暴露 vault 路径或部署结构，因此与凭证同级处理。

替代方案是返回脱敏引用尾部以帮助排障。尾部仍可关联部署拓扑，且管理员可通过配置系统排查，所以不返回任何片段。

### 4. Provider 策略采用不可变版本、当前绑定和独立激活历史

新增三张表：

- `provider_inventory_policy_versions`：UUID 版本 ID，作用域 `(node_type, driver_contract_version)`，规范化 `active_providers`、`out_of_scope_providers`，SHA-256 内容哈希、创建者和创建时间。集合在写入时排序去重，数据库约束/触发器保证非空元素、集合不交叠、同作用域哈希唯一；不可变触发器拒绝 `UPDATE`/`DELETE`。
- `provider_inventory_policy_bindings`：每个作用域至多一行，指向同作用域的当前版本。复合外键阻止跨作用域绑定。
- `provider_inventory_policy_activations`：记录版本的 `[effective_from, effective_to)`，使用 `tstzrange` 生成列和 GiST exclusion constraint 禁止同作用域重叠；复合外键保证版本作用域一致。

登记新版本、关闭当前区间、写入新区间及切换当前绑定必须在一个 `SERIALIZABLE` 事务中完成，并对作用域绑定行加锁。生效时间由数据库 `clock_timestamp()` 校验为当前或未来，应用传入过去时间会失败；所有边界保存为 UTC，同一时间点先结束旧半开区间再开始新区间，不产生双重激活。冲突事务重试整个事务，不能部分重放。此 change 只提供 schema 与只读查询，运行时角色不获得这些表的写权限；受控部署工具遵守上述事务协议，未来写 API 必须另开 change 才能授予权限。

当前读取以 binding 为入口并验证当前激活区间；无绑定时返回显式 `not_configured`，绝不回退到别的作用域或最近历史。阶段 0 的 `provider-inventory-policy-v1.yaml` 是首个部署输入，但 Migration 不硬编码具体环境资产或策略记录，避免把开发基线自动灌入其他环境。

替代方案是只保存一行可变当前策略。该模型无法审计历史或可靠回滚；将时间区间存 JSON 又无法防止并发重叠，因此不采用。

### 5. Node 监控启停同样使用数据库时间的半开区间

`relay_node_inventory_monitoring_activations` 以 UUID 为主键并引用 Node，保存 `[effective_from, effective_to)`、固定 reason、actor 和创建时间。`tstzrange` GiST exclusion constraint 禁止同一 Node 区间重叠，检查约束保证结束晚于开始；受控操作以数据库当前时间或未来时间为边界，在单事务和 Node 行锁内关闭旧区间/开启新区间。

当前监控状态直接用数据库 `CURRENT_TIMESTAMP <@ active_range` 查询，不缓存，也不从 Gateway、Compose、endpoint 可达性或未来采集结果推断。这样重启、多个 Control 实例或时钟轻微偏差不会产生不同真相；应用进程只负责展示数据库判定。

替代方案是 Node 上的布尔列。布尔值不能保留历史，也无法表达预约激活或严格边界，因此不采用。

### 6. OpenAPI 暴露六组只读资源，Node 列表使用有界 cursor

`api/openapi.yaml` 新增环境、Gateway、Node 列表/详情、Driver 列表和当前策略路径，统一复用现有 `super_admin` cookie/session security scheme、Problem 响应与 `Cache-Control: no-store`。Gateway/策略缺失使用带显式状态的 `200` 响应，而不是 `404`；Node 详情不存在返回 `404`，数据库读取失败映射为不含内部细节的可重试 `503`。

Node 列表默认 50、最大 200，以不可变 `instance_id` 升序 cursor 分页；可按固定 `node_type`、capability 和数据库计算的 `monitoring_active` 过滤。每个请求使用一个 read-only `REPEATABLE READ` 事务读取 Node、capability、当前策略/监控摘要，避免一个响应混合两个时间快照；短事务不跨网络或模板渲染。列表 cursor 只编码最后 instance ID 和规范化过滤哈希，不包含 Secret；篡改或过滤不匹配返回 `400`。

所有 schema 先改 OpenAPI，再运行 `make generate` 生成 oapi-codegen 与 Orval 客户端；sqlc 查询先改 `internal/store/queries` 再运行同一生成入口。CI 和仓库 clean check 验证生成可复现，生成文件不得手改。

替代方案是 offset 分页。并发登记会造成重复/遗漏且深页性能退化，所以采用稳定 cursor。当前没有产品写 API，但 cursor 仍为后续登记能力保留一致语义。

### 7. 资产页面只组合生成客户端的读取结果

React 路由 `/assets` 懒加载资产页，使用现有认证上下文和生成客户端并行读取六类资源。页面分别展示环境身份、唯一 Gateway、Node/能力/监控状态、Driver 目录和当前策略；缺失状态与错误状态按资源独立展示，用户点击重试才重新请求。页面不保留持久缓存、不呈现编辑控件、不渲染可点击的管理 endpoint，也不从浏览器请求资产 endpoint。

替代方案是先做单一聚合 BFF 接口。六类资源的变更频率和分页方式不同，聚合会放大任一查询故障并妨碍 Node 分页，因此使用清晰的小型只读资源；前端只做视图组合。

### 8. 故障恢复、审计和可观测性不引入高基数

启动身份校验失败时进程退出，由现有编排重启策略和 Runbook 引导修复；运行期数据库查询失败不缓存旧值，返回 `503`，连接池恢复后下一请求自然重试。资产读取无业务状态变更，不新增“读取即审计”记录；未认证/过期/撤销会话继续由 `administrator-access` 记录既有安全审计事件。未来任何资产写 API 必须另行定义实名、原因、旧值/新值和失败审计。

新增指标采用固定枚举：资产数量 gauge 只含 `asset_kind`，读取 counter 只含 `operation`/`result`；允许值在代码中封闭。禁止 environment ID、Node ID、endpoint、名称、Driver、策略版本、Provider 或 Secret 作为标签。环境启动失败主要依赖固定 reason 日志和进程退出指标，因为应用自身在 listener/metrics server 启动前已失败。

数据库恢复不需要人工清缓存或重启；若 Migration 部署失败，Goose 事务回滚完整 schema 变更。Control 停止或回滚不会向 Gateway/Node 发命令，也不会影响请求数据面。

## Risks / Trade-offs

- [必填 `CONTROL_ENVIRONMENT_ID` 会使未更新部署直接启动失败] → 发布前置检查读取数据库单例并比对配置；Compose/Runbook/验收脚本同时更新，灰度实例先验证。
- [GiST exclusion 需要 `btree_gist` 扩展权限] → 在 Migration 前置验证 PostgreSQL 18 目标角色可创建已批准扩展；失败时事务回滚，不以应用触发器弱化并发约束。
- [运行时无写接口导致初始资产必须由运维登记] → 提供参数化、可重复且冲突时 fail closed 的受控登记说明和验证 SQL；自助登记另开 change。
- [endpoint 语法校验不能证明连接安全] → 本阶段绝不连接；未来 Adapter 在每次连接前实施 scheme/host allowlist、DNS 重绑定和 TLS 策略。
- [策略数组的数据库约束和规范化较复杂] → 用确定性 SQL 函数和 PostgreSQL 集成测试覆盖排序、去重、交叠、哈希和不可变触发器。
- [读取事务与时间边界相交时状态会在下一请求改变] → 单次响应以数据库事务时间为一致快照，页面不宣称实时推送；边界后的刷新返回新状态。
- [保留表的应用回滚无法读取新资产] → 旧版本忽略 additive 表；历史继续保留，重新升级后可恢复读取。

## Migration Plan

1. 在目标环境读取现有 `environments` 单例，确认 ID/类型唯一且与预期环境一致；为部署 Secret/配置增加 `CONTROL_ENVIRONMENT_ID`，但暂不切换应用镜像。
2. 以 Migration owner 运行新的原子 forward Migration，创建批准的 extension、endpoint/规范化函数、资产/策略/激活表、约束、索引和不可变保护；运行结构与权限验证。
3. 通过受控部署流程在单事务中登记 Driver、Gateway、Node/capability、阶段 0 Provider 策略版本/绑定/激活和所需监控区间。空注册表也是合法状态，不能由应用自动补全。
4. 发布包含环境启动校验、只读查询/API/指标的 Control；先在一个实例验证环境校验、认证、空/非空读取、Secret 隔离和数据库故障恢复，再扩展实例。
5. 发布懒加载资产 UI，执行 PostgreSQL 18、HTTP/OpenAPI、生成物、前端、安全与容器验收；Runbook 记录登记证据和策略哈希。

应用回滚只回退二进制/前端，保留新增表、策略版本和区间历史，不执行 down。只有尚未登记任何 Gateway、Node、Driver、策略、绑定或激活记录的全新数据库，且经人工确认后，才可按逆依赖顺序执行 down；down 不删除或改写 `environments` 单例。若新版本因身份不匹配失败，先恢复正确配置或数据库目标，禁止修改数据库身份去迎合错误配置。
