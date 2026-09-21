## ADDED Requirements

### Requirement: Phase 10 management shell SHALL target PC Desktop Browser only
Control Web SHALL 以 PC Desktop Browser 为本阶段产品范围，主要宽度 SHALL 为 `1280px` 及以上，统一验收 MUST 覆盖 `1280×720` 与 `1440×900`。Mobile / Tablet responsive behavior MUST NOT 作为 Phase 10 新管理界面的产品验收契约，历史 `390×844` responsive expectations SHALL 被本 change 明确 supersede，而不得继续反向限制 PC 信息密度。

#### Scenario: 1280 desktop management shell
- **WHEN** 已认证管理员以 `1280×720` 打开任一 Phase 10 主要管理页面
- **THEN** Sidebar、Global Header、Page Content 与关键操作均可见且可达
- **AND** 页面不存在阻塞核心操作的 overflow

#### Scenario: historical mobile acceptance is superseded
- **WHEN** 历史 Phase 8 Browser acceptance 仍包含 `390×844` 的 Mobile / responsive expectation
- **THEN** Phase 10 测试 SHALL 将该 expectation 调整或废止
- **AND** 实现 MUST NOT 为通过历史 Mobile gate 引入 Drawer Sidebar、Mobile table card 化或 Touch-first interaction

### Requirement: Authenticated management pages SHALL share one application shell
Bootstrap、Login、Activation 与一次性 Secret/Material 安全流程 MAY 继续使用独立布局。其他已认证普通管理页面 SHALL 共享统一 App Shell，包括固定 Desktop Sidebar、Global Header 与统一 PageShell / PageHeader presentation。App Shell MUST 仅拥有 presentation/navigation，不得成为业务 query、mutation 或 durable state 的新 truth source。

#### Scenario: authenticated domain page enters shared shell
- **WHEN** 已认证管理员打开 Accounts、Relay Nodes、Operations、Monitoring、Problems 或 Settings
- **THEN** 页面渲染于同一 App Shell
- **AND** 页面业务组件继续通过既有 Control API 获取和修改其 own domain state

#### Scenario: security flow remains outside ordinary shell
- **WHEN** 用户处于 Bootstrap、Login、Activation 或 One-Time Material 流程
- **THEN** 页面 MAY 使用独立安全布局
- **AND** 不要求显示普通管理 Sidebar

### Requirement: Primary navigation SHALL expose exactly the frozen real product entries
第一阶段一级 Sidebar SHALL 使用以下映射：

| zh-CN | en |
|---|---|
| 仪表盘 | Dashboard |
| 账号 | Accounts |
| 节点 | Relay Nodes |
| 操作 | Operations |
| 监控 | Monitoring |
| 问题 | Problems |
| 设置 | Settings |

`zh-CN` 普通产品导航 MUST NOT 出现 `Dashboard`、`Accounts`、`Relay Nodes`、`Operations`、`Monitoring`、`Problems`、`Settings` 等无必要英文菜单名。当前后端不存在的 `Audit Logs` 与 `API Keys` MUST NOT 进入 Sidebar。

#### Scenario: Chinese navigation
- **WHEN** locale 为 `zh-CN`
- **THEN** 七个一级入口分别显示“仪表盘、账号、节点、操作、监控、问题、设置”
- **AND** 不显示对应英文菜单名或中英双语并列

#### Scenario: English navigation
- **WHEN** locale 为 `en`
- **THEN** 七个一级入口分别显示 `Dashboard, Accounts, Relay Nodes, Operations, Monitoring, Problems, Settings`

#### Scenario: unavailable system capabilities stay hidden
- **WHEN** 当前 Control OpenAPI 仍不存在 Web UI Audit Logs list API 与 API Keys management API
- **THEN** Sidebar 与 Command / Navigation Search MUST NOT 暗示这些能力已实现

### Requirement: The problem domain SHALL use Problems rather than Issues
问题域中文 SHALL 为“问题”，英文 SHALL 为 `Problems`。Phase 10 MUST 保留现有 `/problems`、`ProblemsPage`、`ProblemsView` 与 Problems domain 语义。`Issues` MUST NOT 作为该领域的英文产品名称，以避免引入 ticket / issue-tracking workflow 含义。

#### Scenario: English Problems naming
- **WHEN** locale 为 `en` 且用户进入问题域
- **THEN** Sidebar、Breadcrumb、Page Title 与普通产品文案使用 `Problems`
- **AND** MUST NOT 将该领域命名为 `Issues`

### Requirement: Gateway SHALL remain distinct from Relay Nodes
Gateway 与 Relay Node MUST 保持独立领域。Phase 10 第一阶段 SHALL 保留现有 `/assets` 作为 Gateway 辅助管理入口，但 MUST NOT 新增一级 Gateway Sidebar 项，也 MUST NOT 把 Gateway 包装成 Relay Node 子类型。该辅助入口 MAY 从 Command / Navigation Search 或真实 Gateway 上下文进入。

#### Scenario: Gateway management remains reachable
- **WHEN** 管理员需要执行现有 Gateway 生命周期管理
- **THEN** 既有 Gateway `/assets` 辅助管理入口仍可达
- **AND** 其 Gateway 标题、状态与操作语义保持独立

#### Scenario: Relay Nodes page excludes Gateway lifecycle ownership
- **WHEN** 管理员进入一级“节点 / Relay Nodes”页面
- **THEN** 页面只以 Relay Node domain 为主要 ownership
- **AND** MUST NOT 把 Gateway lifecycle action 表达为 Node lifecycle action

### Requirement: Header search SHALL be Command / Navigation Search only
Global Header 第一阶段 SHALL 提供 Command / Navigation Search，而不是跨业务领域的 Global entity search。它 MAY 跳转已有页面、设置或管理入口，并 MAY 搜索当前页面或当前已完整加载的受支持实体；它 MUST NOT 使用局部 cache 或分页数据伪装跨 Accounts、Nodes、Jobs、Problems 的完整全局搜索。

#### Scenario: navigation search
- **WHEN** 用户搜索已存在的页面名称或管理入口
- **THEN** Search MAY 返回该导航目标并执行客户端跳转
- **AND** 不需要新增搜索后端

#### Scenario: no fake global entity search
- **WHEN** 当前只加载了某一分页 Accounts 或 Problems 结果
- **THEN** Header search MUST NOT 将该局部结果标记为全局完整结果

### Requirement: Dashboard SHALL only present summaries with registered truth semantics
每个 Dashboard metric / summary MUST 在 `dashboard-truth-source-matrix.md` 登记 source API、authoritative/navigation-only classification、aggregation semantics、time window、denominator 与 pagination completeness。前端 MUST NOT 从分页结果推导 global count、rate、trend 或 comparison。若现有 API 不提供完整语义，Dashboard SHALL 只提供 Navigation Summary、明确 unavailable，或等待新的 Backend/OpenSpec contract。

#### Scenario: authoritative asset count
- **WHEN** Dashboard 使用 `/api/assets/nodes` 的 `node_counts` 或 `/api/assets/gateways` 的 `gateway_counts`
- **THEN** MAY 以 API 自带的完整 counts 作为 Authoritative Summary
- **AND** MUST NOT 把 asset count 描述成 runtime health

#### Scenario: paginated result cannot become global metric
- **WHEN** `/api/jobs`、`/api/problem-accounts/query` 或 account query 仅返回 `items` 与 continuation cursor 而没有全局 aggregate
- **THEN** Dashboard MUST NOT 使用 `items.length` 或当前 pagination 状态制造全局总数、成功率或趋势

#### Scenario: dashboard does not auto-probe managed endpoints
- **WHEN** Dashboard 首次 mount
- **THEN** MUST NOT 为填充卡片自动触发 Gateway/Node connection test 或 per-instance explicit health probe

### Requirement: Accounts SHALL remain the account-domain primary work surface
Accounts SHALL 逐步整合现有 Account Inventory、Account Quality、Account Availability、Request History、Quality Incidents、Provider snapshot、Inventory freshness、Account Operations 与 Account detail。每个 slice MUST 继续使用现有 Control API / Orval client，并 MUST NOT 新增假字段、假过滤、假统计。Account Operations 的执行语义 SHALL 继续由既有业务 contract 拥有。

#### Scenario: account data is sourced from existing APIs
- **WHEN** Accounts 页面展示账号、质量、可用性、历史或 operation state
- **THEN** 数据来自对应既有 Control API
- **AND** presentation state 不成为业务 execution truth

### Requirement: Relay Nodes SHALL own only Relay Node management and observation
Relay Nodes 页面 SHALL 承载现有 Node asset、stable instance identity、Node health、Driver、monitoring、connection test、lifecycle、Inventory status、Provider status、Gateway binding context 与 Account quality context。Gateway MUST NOT 被作为 Relay Node。

#### Scenario: node context includes Gateway without changing ownership
- **WHEN** Node 页面展示与 Gateway binding 相关的只读 context
- **THEN** Gateway 信息 MAY 作为 context/link 展示
- **AND** Gateway lifecycle truth 仍由 Gateway/Asset domain 拥有

### Requirement: Operations SHALL use Durable Jobs as its first-stage list
Operations 第一阶段 SHALL 以现有 `/api/jobs` 与 `/api/jobs/{job_id}` 为主列表和历史入口。由于当前没有全局 Account Operations list API，UI MUST NOT 构造、推断或暗示“所有 Account Operations 的全局历史”。Account Operation SHALL 继续从账号上下文进入已有 command/detail flow。

#### Scenario: Operations default view
- **WHEN** 管理员进入 Operations
- **THEN** 主列表展示 Durable Jobs
- **AND** 数据由现有 Jobs API 提供

#### Scenario: no global account-operation history
- **WHEN** 用户尚未处于特定账号或已知 command context
- **THEN** UI MUST NOT 展示伪造的“最近所有 Account Operations”列表

### Requirement: Monitoring SHALL be a read-only cross-domain diagnostic composition
Monitoring SHALL 仅组合现有 Node health、Gateway context、Provider Inventory state、Account Quality、Poll Capacity、Inventory freshness、Problems、Request evidence 与其他可用诊断。Monitoring MUST NOT 修改 Gateway routing、数据面调度、自动 drain、自动 repair duplicate ownership、自动改变 account selection，也 MUST NOT 成为任何领域状态机或执行动作的 owner。

#### Scenario: monitoring page observes without mutating
- **WHEN** 管理员打开 Monitoring 并浏览诊断信息
- **THEN** 页面只调用既有 read/query diagnostic surfaces
- **AND** MUST NOT 因页面 mount 或 refresh 触发业务 mutation

### Requirement: Problems presentation SHALL preserve existing business taxonomy
Problems 页面 MAY 优化 Severity badge、Reason、Provider、Node、Account、First seen、Last seen、Related evidence、filter toolbar 与 read-state presentation。UI MUST NOT 将 `unknown`、`unavailable`、`outcome_unknown` 映射为 `success`、`failed` 或其他不同业务语义。

#### Scenario: unknown outcome remains distinct
- **WHEN** backend machine value 为 `outcome_unknown`
- **THEN** presentation MUST 表达结果未知
- **AND** MUST NOT 显示为 success 或 failed

### Requirement: Settings SHALL reorganize only existing management and security capabilities
Settings MAY 重组 Current session、Administrator management、Password、MFA、Reauthentication、Recovery codes、Locale 与 UI preference。若没有后端 preference API，UI preference MUST 仅为 browser-local、非敏感 presentation preference，并 MUST NOT 暗示跨浏览器或跨设备同步。高风险管理操作 SHALL 继续遵守既有 Reauthentication、Authorization、CSRF、MFA 与 Audit contract。

#### Scenario: local UI preference
- **WHEN** 用户修改一个第一阶段 presentation preference
- **THEN** 该 preference 只保存在批准的 browser-local boundary
- **AND** 不调用新的 backend preference API

#### Scenario: high-risk action keeps security gate
- **WHEN** 用户从 Settings 执行既有高风险管理员操作
- **THEN** 既有 reauthentication / MFA / CSRF / authorization flow 仍被执行

### Requirement: Production UI SHALL display only data with a real truth source
所有 Count、Rate、Trend、Status、Alert、Latency、Duration 与 Comparison MUST 有明确真实真相源。若现有 API 不支持某项展示，UI SHALL 不显示、明确显示 unavailable，或先建立新的 Backend/OpenSpec change。生产 UI MUST NOT 使用 Mock 数据冒充真实管理数据。

#### Scenario: unsupported trend
- **WHEN** 产品设计包含 Last Week Comparison、Global Alerts Trend、Global Operation Trend 或其他当前无 aggregation contract 的指标
- **THEN** Phase 10 实现 MUST NOT 填充示例数字
- **AND** 对应功能必须隐藏、标记 unavailable 或触发 scope escalation

### Requirement: Frontend SHALL reuse the existing technology and foundation
Phase 10 SHALL 继续使用 React 19、TypeScript、Vite、Ant Design 6、TanStack React Query、i18next/react-i18next、Orval、Vitest、Testing Library 与 Playwright。MUST NOT 引入 React Router、Tailwind CSS、shadcn/ui、Material UI、第二套 Component Library、第二套 Design System Framework、远程 Translation Backend、Runtime Translation Service 或 Locale Detector Plugin。

`foundationTheme` SHALL 作为 Control Web theme token 入口。实现 MAY 新增具有明确应用语义的 AppShell、AppSidebar、GlobalHeader、MetricCard、StatusBadge、PageToolbar，但 MUST NOT 建立仅用于重命名 Ant Design primitives 的通用 wrapper framework。

#### Scenario: theme customization
- **WHEN** Phase 10 调整 layout、typography、color、radius、control height、table/form density
- **THEN** 优先通过 `foundationTheme` 与 Ant Design token 完成
- **AND** MUST NOT 为 Button/Card/Table 建立纯重命名 wrapper

### Requirement: Phase 10 routing SHALL extend the existing lightweight routing model
当前已认证 routing 使用 `window.location.pathname`、`history.pushState()` 与 `popstate`。Phase 10 SHALL 在现有模型上扩展目标 IA，MUST NOT 引入 React Router。

目标 canonical route SHALL 包括 `/`、`/accounts`、`/nodes`、`/operations`、`/monitoring`、`/problems`、`/settings`。迁移期 `/assets`、`/jobs`、`/topology` MUST 保持可达或具有明确兼容 alias，旧 deep link MUST NOT 静默失效。

#### Scenario: browser back-forward
- **WHEN** 管理员通过 Sidebar 在两个 Phase 10 页面之间跳转后使用 Browser Back/Forward
- **THEN** `popstate` 驱动的 route state 与 URL 保持一致

#### Scenario: legacy jobs deep link
- **WHEN** 用户访问既有 `/jobs` deep link
- **THEN** 迁移期 SHALL 仍能到达 Operations / Durable Jobs 语义
- **AND** 不因 Phase 10 IA 重命名返回无关页面

### Requirement: Existing i18n contract SHALL remain compatible
Phase 10 MUST 保持 `AppLocale = "zh-CN" | "en"`、fallback `zh-CN`、`relay-control.locale`、valid persisted locale -> browser locale -> zh-CN 的 resolution、zh/en normalization，以及“只有用户显式选择时持久化”的现有 contract。新增普通产品文案 MUST 同时提供 zh-CN 和 en。Machine value MUST 保持原始值，仅在 presentation 层转换。

#### Scenario: live locale switch
- **WHEN** 用户从 `zh-CN` 显式切换到 `en`
- **THEN** 当前 session 内导航与当前页面普通产品文案立即更新
- **AND** Ant Design locale 同步变化
- **AND** reload 后显式选择仍保留

#### Scenario: browser locale does not persist automatically
- **WHEN** 初始 locale 仅来自 browser locale 而非用户显式选择
- **THEN** application MUST NOT 自动写入 `relay-control.locale`

### Requirement: Date, number and percentage presentation SHALL use the existing formatting boundary
Phase 10 新增页面 SHALL 继续使用 `foundation/format.ts` 与 `Intl.DateTimeFormat` / `Intl.NumberFormat` 的既有统一边界。业务组件 MUST NOT 散落新的 `toLocaleString(...)`、手工日期拼接或手工 `%` 格式化。Locale MUST NOT 改变 timezone；显示 timezone 继续以 browser/system local timezone 为真相。

#### Scenario: locale changes formatting without changing time truth
- **WHEN** 同一 UTC timestamp 在 zh-CN 与 en 间切换
- **THEN** display formatting MAY 改变
- **AND** underlying timestamp 与 browser/system timezone interpretation 不变

### Requirement: Browser security and API boundaries SHALL remain unchanged
Browser SHALL 只调用 Control API。Browser MUST NOT 直接调用 Gateway、Relay Node 或 Prometheus，MUST NOT 获取 Node Management Credential、Gateway Credential、Provider credential/token 或其他 Secret。`api/openapi.yaml` SHALL 继续是 API 契约真相源，generated Orval client MUST NOT 手工修改。

Authentication、Authorization、CSRF、MFA、Reauthentication、Session expiration、Account lifecycle、Node lifecycle、Gateway lifecycle、Inventory truth、Binding truth、Problems classification、Job lifecycle 与 Account-operation semantics MUST 保持不变。

#### Scenario: browser network boundary
- **WHEN** 管理员使用任一 Phase 10 页面
- **THEN** 产品 Browser 请求仅发送到 Control origin 的批准 API
- **AND** MUST NOT 向 Gateway、Relay Node 或 Prometheus 发起直接业务请求

#### Scenario: credential secrecy
- **WHEN** 页面展示 Gateway、Node、Account、Search result、Dashboard 或 error state
- **THEN** DOM、日志与普通错误信息 MUST NOT 包含受保护 credential/token/Secret

### Requirement: Accessibility SHALL not regress during visual modernization
Phase 10 SHALL 保留 semantic label、accessible name、keyboard navigation、focus state、loading semantics、error semantics 和非纯颜色状态表达。状态 MUST NOT 只依靠红/绿颜色区分。

#### Scenario: status is not color-only
- **WHEN** 页面展示 failed / degraded / warning 等状态
- **THEN** 状态包含可读文本或等价 semantic label
- **AND** 颜色不是唯一含义载体

### Requirement: Playwright interaction locators SHALL use stable product test IDs
所有 Phase 10 Playwright E2E 用户交互 locator MUST 使用 `getByTestId()`。新增 `data-testid` MUST 表达稳定业务语义，MUST NOT 依赖当前语言、显示文案、DOM order、CSS class 或 Ant Design 内部 DOM。

#### Scenario: sidebar interaction locator
- **WHEN** Browser E2E 点击 Accounts 一级导航
- **THEN** interaction 通过类似 `getByTestId("sidebar-accounts")` 的稳定产品 test ID 定位
- **AND** MUST NOT 使用菜单文案、DOM index 或 CSS selector 执行点击

### Requirement: Unified acceptance SHALL prove desktop, language, semantic and generated-code invariants
Phase 10 最终验收 MUST 至少包含：

- `npm test`
- `npm run typecheck`
- `npm run build`
- `RESOURCE_PARITY = PASS`
- `REFERENCED_KEY_COMPLETENESS = PASS`
- `TRANSLATION_SOURCE_AUDIT = PASS`
- `ZH_CN_PC_BROWSER = PASS`
- `EN_PC_BROWSER = PASS`
- `LIVE_LOCALE_SWITCH = PASS`
- `1280x720 = PASS`
- `1440x900 = PASS`
- `RELEVANT_BROWSER_E2E = PASS`
- `GENERATED_DRIFT = NONE`
- `ZH_CN_NAVIGATION_ENGLISH_LEAK = ZERO`
- `ZH_CN_UI_UNINTENDED_ENGLISH_LEAK = ZERO`

视觉验收 SHALL 以 semantic/layout invariants 为主；截图 MAY 作为 review evidence，但 MUST NOT 默认建立大面积 pixel-perfect screenshot gate。

#### Scenario: Chinese desktop acceptance
- **WHEN** Stage 4 在 `1280×720` 与 `1440×900` 运行 zh-CN Browser acceptance
- **THEN** Sidebar/Header/Page layout、关键操作、translation leak、loading/error/status semantics 均满足冻结 contract

#### Scenario: generated client remains unchanged without API change
- **WHEN** Phase 10 change 未修改 `api/openapi.yaml`
- **THEN** final generation/build proof SHALL 报告 `GENERATED_DRIFT = NONE`
