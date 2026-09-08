# account-inventory-readonly-query Specification

## Purpose

定义 Control 中 current account inventory 的管理员只读查询边界，包括认证与逐页审计、最小字段投影、敏感筛选、加密 cursor、稳定分页、快照新鲜度和 React 页面行为，同时保持 lifecycle 只读、最小数据库权限及数据面隔离。

## Requirements

### Requirement: account inventory query MUST 只允许实名管理员读取受支持 Node

Control SHALL 提供只读 current account inventory query，只允许已认证、已启用的 `super_admin` 会话使用，并 MUST 对 POST 请求验证 session-bound CSRF。请求 MUST 指定本环境已登记且声明 `management.account_inventory` capability 的单个 Node；未知或不支持的 Node MUST fail closed，且任何结果均不得来自其他环境、Node、Gateway、Prometheus 或实时外部请求。

#### Scenario: 合法管理员查询受支持 Node
- **WHEN** 已启用 super_admin 使用有效 session/CSRF 查询已登记且具备 capability 的 instance
- **THEN** Control 只从本环境 PostgreSQL 返回该 instance 的有界当前账号结果

#### Scenario: 会话、CSRF 或角色无效
- **WHEN** 请求没有 session、会话过期/管理员已禁用、CSRF 缺失或不匹配，或主体不是固定 super_admin
- **THEN** Control 返回现有 401/403 安全错误且不执行账号读取、不返回 email

#### Scenario: Node 未登记或不支持账号清单
- **WHEN** instance 不存在或其 Driver 未声明 management account inventory read capability
- **THEN** Control 分别返回固定 404 或 409，不探测 Node也不泄露任何账号

### Requirement: query request MUST 避免在 URL 暴露敏感筛选

Account inventory query MUST 使用 `POST /api/account-inventory/query` 和 JSON body 承载 instance、filters、cursor 与 limit。email、cursor 和 account identity MUST NOT 出现在 URL、route parameter、redirect、Location header、普通 access/application log 或浏览器持久 storage。所有成功与错误响应 MUST 设置 `Cache-Control: no-store`。

#### Scenario: 管理员按 email 定位账号
- **WHEN** 管理员提交带标准化 exact email filter 的 query
- **THEN** email 只存在于 HTTPS 请求 body、受保护数据库比较和授权响应，不进入请求 URL或重定向

#### Scenario: 非法敏感筛选
- **WHEN** email/provider 超长、标准化为空、enum 非法或 body 含未知字段
- **THEN** Control 返回固定 400且错误、日志和审计不回显输入

### Requirement: query MUST 有界、稳定并绑定全部筛选

请求 MUST 要求一个 instance，limit SHALL 默认为 50且只允许 1至100。Control MUST 在 Provider、lifecycle、basic status 和规范化 email 精确筛选后按不可变 account key升序执行 keyset pagination，并只在存在下一行时返回 cursor。它 MUST NOT 提供无界结果、offset pagination、模糊/前缀 email 搜索或跨 Node 聚合。

#### Scenario: 多页读取稳定账号集合
- **WHEN** 第一页达到 limit且存在额外行，管理员在相同 instance和filters下提交 next cursor
- **THEN** 下一页从上一页最后 account key之后继续，既有 key不重复且 API不返回内部 account key

#### Scenario: filter 变化后重放 cursor
- **WHEN** 客户端把 cursor用于不同 instance、Provider、lifecycle、basic status或email筛选
- **THEN** Control 以统一 invalid cursor 400拒绝，不执行部分查询或泄露 cursor payload

#### Scenario: promotion 与翻页并发
- **WHEN** lifecycle promotion在两个 page请求之间更新状态或插入新账号
- **THEN** 每页都返回请求时的 current view且按不可变 key继续；Control不声称跨页冻结快照，也不按更新时间重排既有账号

### Requirement: account cursor MUST 加密、短期有效且绑定管理员

Cursor SHALL 使用现有环境 keyring的独立 domain和 AEAD加密认证，MUST 绑定 key version、环境、actor admin、instance、规范化 filter hash、continuation account key和签发/过期时间。有效期 MUST 为15分钟。客户端可见 token MUST NOT 可解码得到 email/account key；cursor MUST NOT 代替每次请求的 session、enabled admin和CSRF验证。

#### Scenario: 合法管理员在有效期内翻页
- **WHEN** 原管理员在15分钟内以相同筛选、有效 session和CSRF提交未篡改 cursor
- **THEN** Control 解密并继续读取下一页

#### Scenario: cursor 被篡改、过期或交给另一管理员
- **WHEN** token认证失败、key不可用、超过有效期或actor不匹配
- **THEN** Control统一返回 invalid cursor且不区分失败细节、不返回账号

#### Scenario: 浏览器刷新或离开页面
- **WHEN** 管理员刷新、退出账号页或关闭会话
- **THEN** 前端丢弃内存 cursor栈和email筛选，不从URL、localStorage或sessionStorage恢复

### Requirement: query response MUST 使用最小 current-state 字段白名单

每个item SHALL只包含instance、Provider、normalized email、最后报告basic status、lifecycle/count、first/last seen、missing/out-of-scope时间、last refresh、next retry、source updated、Provider last complete、Provider degraded和`fresh|stale|out_of_scope` freshness。Provider degraded MUST来自`account_inventory_provider_states`中最近符合当前策略、单调slot和scope门禁的finalized Provider健康字段；current poll外键被合法history retention置空时仍以冗余来源/健康字段返回，不得依赖已清理的poll/provider result。API MUST NOT返回account key、poll/policy ID、Node版本/提交、原始累计计数/桶、endpoint、Secret引用、未知字段或原始错误。

#### Scenario: suspected 或 missing 账号仍有旧基础状态
- **WHEN** lifecycle账号未在最新完整快照出现但保留最后报告 basic status
- **THEN** API/UI明确标为“最后报告基础状态”，不得解释为当前可调度或当前 Node已报告

#### Scenario: Provider 当前降级但旧快照尚新鲜
- **WHEN** 最近一次轮询不完整使 Provider current health 为 degraded，但 last complete仍未超过15分钟
- **THEN** 响应同时返回 degraded和fresh，两种语义不互相覆盖

#### Scenario: current source早于最近health结果
- **WHEN** current poll仍指向上次成功promotion，而严格更新的当前策略poll仅刷新Provider degraded health
- **THEN** query使用较新的冗余health和既有last complete，既不从旧provider result覆盖health也不移动snapshot pointer

#### Scenario: out-of-scope 账号
- **WHEN** lifecycle为out_of_scope
- **THEN** freshness固定为out_of_scope并返回out_of_scope_since，不伪造fresh/stale或参与active状态解释

#### Scenario: current source poll 已合法清理
- **WHEN** history retention删除到期poll使Provider/current account来源外键置空，但冗余来源时间与健康字段完整
- **THEN** query继续返回相同current产品投影，不返回503、不猜测poll ID且不要求恢复历史行

#### Scenario: lifecycle与Provider state不一致
- **WHEN** 数据库缺少必要Provider state、last complete、健康字段或出现非法状态组合
- **THEN** Control以固定503 fail closed，不返回默认填充或部分账号页

### Requirement: 完整 email 页面返回 MUST 先持久审计

每个 query page，包括空结果和使用 cursor的后续页，MUST 在返回前持久写入实名 `account_inventory.view` 审计。账号查询与审计 SHALL 位于同一短数据库事务，只有 audit commit成功后才可返回 items。Audit details MUST 只包含受控 instance、各filter是否使用、cursor是否使用、结果数量和request ID；MUST NOT 包含 email、account key、cursor、filter value/hash或结果identity。

#### Scenario: 成功返回第一页或后续页
- **WHEN** query验证、读取和audit insert/commit均成功
- **THEN** Control返回页面且审计能够关联actor、时间、request和非敏感查询形态

#### Scenario: email精确筛选返回空结果
- **WHEN** 合法管理员按email筛选但没有匹配账号
- **THEN** Control返回空页并以email_filter_used=true审计，不记录被搜索email

#### Scenario: 审计写入或提交失败
- **WHEN** 结果已在事务内读取但audit insert或commit失败
- **THEN** Control返回固定503且响应不包含任何item/email

#### Scenario: audit提交后客户端断开
- **WHEN** audit事务已提交但HTTP响应未送达或连接中断
- **THEN** 已提交审计继续保留，不尝试删除或去重该查看证据

### Requirement: 数据库读取 MUST 最小权限且不改变生命周期

Runtime role SHALL 只通过版本化受控函数读取 query白名单并通过既有受控入口写audit；MUST NOT获得账号、snapshot、Provider state或资产表的任意SELECT/DML。Query、重试、分页和进程恢复 MUST NOT 修改lifecycle、missing count、Provider pointer、poll/promotion或snapshot，也不得重新解释历史数据。

#### Scenario: runtime直接枚举或修改账号表
- **WHEN** runtime或未授权数据库角色尝试SELECT表、UPDATE、DELETE、INSERT或TRUNCATE账号相关表
- **THEN** PostgreSQL权限拒绝，只有合法受控query函数可返回白名单

#### Scenario: 同一页重复查询
- **WHEN** 管理员因刷新或网络重试重复提交相同请求
- **THEN** 当前账号状态保持不变，每次成功结果分别形成查看审计，不创建幂等写或推进missing

#### Scenario: Control在查询期间重启
- **WHEN** 进程在query/audit事务提交前停止
- **THEN** 事务回滚且没有结果被交付；恢复后新请求重新读取PostgreSQL当前真相

### Requirement: React账号页 MUST 清楚呈现筛选、降级和错误状态

Control SHALL 为已认证管理员提供账号清单页面。页面 MUST 先选择具备账号清单capability的Node，支持Provider、lifecycle、basic status和exact email筛选及前后页导航，并分别呈现loading、empty、invalid filter、unsupported、unauthorized和unavailable状态。页面 MUST 明确区分last-reported basic status、lifecycle、Provider degraded和snapshot freshness，且 MUST NOT提供导出、批量选择、详情、状态修改、删除、补采或promotion控件。

当页面 URL 含 `instance_id` 时，页面 MUST 在首次加载阶段预选该值，并自动提交一次现有默认筛选的第一页查询。Node 的合法性、登记状态和 capability 继续由既有 API 校验；自动查询完成或失败后，页面 MUST 呈现现有结果或对应错误状态，不得因失败而自动循环重试。URL 缺少 `instance_id` 时，页面 MUST 保持手动选择 Node 后由管理员触发查询的行为。

首次自动查询只适用于页面初始进入。管理员编辑筛选或切换 Node 时，页面 MUST 清空 cursor 历史；翻页沿用现有 cursor 历史。请求由现有显式查询/分页动作提交；这些动作不得被隐式的 URL 自动加载逻辑再次覆盖。该行为 MUST 复用现有 POST 只读查询及其查看审计，不新增业务写请求、补采、状态变更或数据面调用。

#### Scenario: 从带 instance_id 的链接首次加载

- **WHEN** 已认证管理员打开包含合法、受支持 `instance_id` 的账号清单 URL，且未提交筛选
- **THEN** 页面预选该 Node，并自动执行一次默认筛选第一页查询，成功后显示该 Node 的账号结果

#### Scenario: Node不具备账号清单能力

- **WHEN** 管理员浏览Node选择器
- **THEN** 页面不为不支持capability的Node提供账号查询入口

#### Scenario: 过滤器变化

- **WHEN** 管理员修改任一筛选或切换Node
- **THEN** 页面清空cursor历史、回到第一页并只提交新筛选body

#### Scenario: 窄屏或键盘访问

- **WHEN** 管理员在窄屏或只使用键盘操作筛选和分页
- **THEN** 所有输入、状态、按钮和错误具有可访问名称且不因布局隐藏关键信息

#### Scenario: URL 缺少 instance_id

- **WHEN** 已认证管理员打开不带 `instance_id` 的账号清单 URL
- **THEN** 页面不自动查询，管理员选择受支持 Node 后点击现有查询动作才加载第一页

#### Scenario: instance_id 无效或不受支持

- **WHEN** URL 中的 `instance_id` 不是合法 UUID、未知、未启用或不具备账号清单 capability
- **THEN** 页面提交一次既有默认查询并显示 API 返回的 invalid、unsupported 或 unavailable 状态；不得回退查询其他 Node

#### Scenario: 首次自动查询失败

- **WHEN** 带 `instance_id` 的首次默认查询因认证、网络、数据库或受控查询错误失败
- **THEN** 页面显示对应 unauthorized 或 unavailable 状态，且同一次进入不会自动循环重试或伪造为空结果

#### Scenario: 首次加载后编辑筛选

- **WHEN** 首次自动查询完成后管理员修改 Provider、lifecycle、status 或 exact email 筛选
- **THEN** 页面清空 cursor 历史，并只在管理员触发现有查询动作后提交修改后的筛选第一页

#### Scenario: 首次加载后切换 Node 或翻页

- **WHEN** 管理员切换 Node 或使用下一页/上一页
- **THEN** 页面按现有显式动作重新查询对应 Node 或 cursor 页面，且 URL 初始 `instance_id` 不会覆盖该后续选择

### Requirement: query观测 MUST 低基数且不泄露身份

Query日志与指标 MAY 记录固定operation/result/error code、latency、result-size bucket和布尔filter-used。它们 MUST NOT包含instance、Provider、actor、email、account key、cursor、filter value/hash、request/poll/policy ID、版本/提交、endpoint、Secret或raw error。授权API响应中的email、受保护数据库identity列和AEAD ciphertext是唯一允许的相应敏感位置。

#### Scenario: 敏感canary贯穿成功与失败路径
- **WHEN** 测试把唯一email/account-key/cursor/Secret/endpoint/raw-error canary送入query、audit failure、cursor failure和UI错误路径
- **THEN** 最终日志、指标、错误、audit details、URL、浏览器storage和artifact均不含canary；只有授权响应/受保护列和不可读ciphertext允许包含对应信息

#### Scenario: 页面查询网络范围
- **WHEN** 管理员加载、筛选和翻页账号清单
- **THEN** 网络审计只看到浏览器到Control API及Control到PostgreSQL，不调用Node、Gateway、Prometheus、模型数据面或任意外部目标

#### Scenario: Control或PostgreSQL不可用
- **WHEN** 查询依赖停止
- **THEN** 账号管理页面fail closed，但Gateway与Relay Node现有模型流量继续且poll/lifecycle不会从页面建立内存真相

### Requirement: OpenAPI与生成客户端 MUST 可复现且兼容

`api/openapi.yaml` SHALL 是query contract唯一真相源，并 MUST 定义严格additional-properties、封闭enum、body limit、no-store、401/403/404/409/503和统一错误。生成Go/TypeScript客户端 MUST 由现有生成链产生且连续生成无差异。新增route MUST additive，不改变现有认证、资产、任务、poll或lifecycle API行为。

#### Scenario: 连续运行生成链
- **WHEN** 在固定工具版本下连续运行make generate两次
- **THEN** 第二次工作树零差异且生成server/client与OpenAPI operation一致

#### Scenario: 旧客户端继续使用现有API
- **WHEN** 未使用账号query的新旧客户端调用既有route
- **THEN** 状态码、schema、认证和行为保持兼容
