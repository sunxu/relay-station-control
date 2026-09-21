# Relay Station Control Web 管理界面重构需求

> Phase 10 Stage 0 reconciliation（2026-09-21）：本文件以用户提供的 v3 需求为来源。v3 中英文导航存在 `Issues` / `Problems` 冲突；Stage 0 按文档后部“Problems 命名约定”冻结为中文“问题”、英文 `Problems`，沿用现有 `/problems`、`ProblemsPage`、`ProblemsView`。除该显式命名 reconciliation 与下述 Gateway 辅助入口归属说明外，不改变 v3 的业务范围和约束。


## 1. 目标

对 Relay Station Control Web 管理界面进行系统性的视觉与信息架构重构。

本次工作重点是：

-   建立统一的 PC Web 管理控制台布局。
-   提升信息密度、可读性和页面间一致性。
-   将现有业务能力重新组织为清晰的仪表盘、账号、节点、操作、监控、问题和设置等管理入口。
-   保留现有业务语义、API 契约、安全边界、认证流程和数据真相源。
-   完整保留现有 `zh-CN / en` 国际化能力。
-   不以 UI 重构名义引入新的后端业务能力。

本次工作属于 **presentation / information architecture
modernization**，不是重新设计 Relay Station 的业务模型。

------------------------------------------------------------------------

## 2. 项目与仓库边界

相关仓库：

``` text
https://github.com/sunxu/relay-station-ops
https://github.com/sunxu/relay-station-control
https://github.com/sunxu/relay-station-gateway
https://github.com/sunxu/relay-station-node-cliproxyapi
```

职责边界：

-   `control/`：管理与可观测服务。
    -   读取 `control/AGENTS.md`。
    -   行为或契约变更读取 `control/openspec/config.yaml` 和已批准
        change。
    -   按任务选用 `control/.agents/skills/` 下对应 OpenSpec 技能。
-   `gateway/`：基于 Sub2API 的请求数据面。
    -   行为或契约变更读取 `gateway/openspec/config.yaml`、已批准 change
        及相关项目文档。
-   `node-cliproxyapi/`：CLIProxyAPI Relay Node。
    -   读取 `node-cliproxyapi/AGENTS.md`。
    -   保持其原生职责边界。
-   `ops/`：系统设计、ADR 与运行资料。
    -   跨仓行为以 `ops/docs/RELAY_STATION_SYSTEM_DESIGN_CN.md` 及相关
        ADR 为依据。
    -   只读解释只读取相关部分，不启动实施流程。

本次 UI 重构原则上仅修改 `control/`
前端，除非新的产品要求明确需要新增后端契约。

------------------------------------------------------------------------

## 3. 当前前端技术基线

继续使用现有技术体系：

``` text
React 19
TypeScript
Vite
Ant Design 6
TanStack React Query
i18next
react-i18next
Orval generated API client
Vitest
Testing Library
Playwright
```

不得为了本次重构引入：

``` text
React Router
Tailwind CSS
shadcn/ui
Material UI
第二套 Component Library
第二套 Design System Framework
远程 Translation Backend
Runtime Translation Service
Locale Detector Plugin
```

Ant Design 继续作为基础组件平台。

不得建立仅用于重新命名 Ant Design `Button`、`Card`、`Table`、`Select`
等组件的通用 wrapper 层。

------------------------------------------------------------------------

## 4. 平台范围

本次 Control Web UI 重构 **仅支持 Web PC 端**。

目标运行环境：

-   Desktop Browser。
-   PC / Mac 浏览器。
-   主要宽度范围按 `1280px` 及以上设计。
-   主要验收视口：
    -   `1280 × 720`
    -   `1440 × 900`

本次不要求支持：

-   Mobile Web。
-   Tablet。
-   手机端 Sidebar Drawer。
-   手机端表格重排。
-   小屏幕自适应布局。
-   Touch-first interaction。

布局可以设置合理的最小宽度。当浏览器窗口小于支持范围时，不要求重新组织为移动端布局。

### 4.1 与历史响应式验收的关系

当前项目历史上存在 `390×844` 等移动端 Browser acceptance。

新的产品要求明确为 **Web PC only**，因此本次新的 OpenSpec 应明确：

> Mobile / Tablet responsive behavior 不再属于新管理界面的产品验收契约。

如果现有测试仍强制要求移动端布局通过，应在本次 change
中正式调整或废止对应旧验收要求，而不是为了兼容历史移动端测试继续限制 PC
UI 设计。

PC-only 不得影响：

-   业务语义。
-   API。
-   Authentication。
-   Authorization。
-   数据真相。
-   Browser PC 端核心业务 E2E。

------------------------------------------------------------------------

## 5. Styling 与 Design Token

视觉样式按照现有 Phase 8 冻结的优先级实现：

``` text
1. Ant Design ConfigProvider theme tokens
2. Ant Design component tokens / semantic styles
3. 少量应用级 CSS variables
4. Scoped CSS
5. 仅动态几何场景使用 inline style
```

优先扩展现有：

``` text
FrontendFoundationProvider
foundationTheme
PageShell
PageHeader
ReadState
```

允许增加具有明确应用语义的组件，例如：

``` text
AppShell
AppSidebar
GlobalHeader
MetricCard
StatusBadge
PageToolbar
```

但不得建立大而全的通用 UI Framework。

`foundationTheme` 应成为整个 Control Web
的视觉主题入口，统一定义至少以下内容：

-   Primary color。
-   Layout background。
-   Card background。
-   Border color。
-   Border radius。
-   Control height。
-   Typography hierarchy。
-   Success / Warning / Error semantic colors。
-   Table density。
-   Form control density。

------------------------------------------------------------------------

## 6. Global App Shell

认证完成后的普通管理页面统一使用应用级 App Shell：

``` text
┌──────────── Sidebar ────────────┬──────────── Global Header ─────────────┐
│                                 │                                       │
│                                 ├───────────────────────────────────────┤
│                                 │                                       │
│                                 │              Page Content             │
│                                 │                                       │
└─────────────────────────────────┴───────────────────────────────────────┘
```

登录、Bootstrap、Activation、一次性 Secret/Material
等特殊安全流程可以继续使用独立布局，不强制进入普通管理 App Shell。

------------------------------------------------------------------------

## 7. Sidebar

Desktop 使用固定深色 Sidebar。

顶部展示：

``` text
Relay Station
控制台
```

其中 `Relay Station` 为品牌名称，可保留英文。

### 7.1 中文导航

`zh-CN` 模式下使用：

``` text
仪表盘
账号
节点
操作
监控
问题
设置
```

第一阶段不得为了"系统分组完整"创建当前后端并不存在的入口。

因此第一阶段：

``` text
审计日志
接口密钥
```

**不进入 Sidebar。**

`文档` 如确有稳定静态/外部入口，可以单独放入 `系统`
分组；否则第一阶段可以不显示 `系统` 分组。

其中：

-   `Relay Nodes` 中文统一显示为 **"节点"**。
-   中文导航不得出现
    `Dashboard`、`Accounts`、`Relay Nodes`、`Operations`、`Monitoring`、`Problems`、`Settings`
    等英文菜单名。
-   不允许"账号 Accounts""节点 Relay Nodes"等双语并列。

### 7.2 英文导航

`en` 模式下使用：

``` text
Dashboard
Accounts
Relay Nodes
Operations
Monitoring
Problems
Settings
```

### 7.3 导航映射

  中文       English         第一阶段状态
  ---------- --------------- ----------------------------
  仪表盘     Dashboard       显示
  账号       Accounts        显示
  节点       Relay Nodes     显示
  操作       Operations      显示
  监控       Monitoring      显示
  问题       Problems        显示
  设置       Settings        显示
  系统       System          仅在存在实际入口时显示
  文档       Documentation   可选静态/外部入口
  审计日志   Audit Logs      **不显示；后端能力不存在**
  接口密钥   API Keys        **不显示；后端能力不存在**

### 7.4 Sidebar 交互要求

-   当前页面必须有清晰 Active 状态。
-   每个一级导航项使用统一图标体系。
-   Sidebar 宽度固定在适合 PC 管理控制台的范围，建议约 `220–260px`。
-   Sidebar 底部可展示 Control 当前运行状态和版本信息。
-   不需要实现移动端 Hamburger / Drawer Sidebar。

------------------------------------------------------------------------

## 8. Global Header

认证后的普通管理界面统一提供 Header。

建议包含：

-   Command / Navigation Search 入口。
-   Control Online / health 状态。
-   Notification / Issue 入口。
-   当前管理员 Avatar。
-   Display Name / Login Name。
-   Locale 切换入口。
-   Account / Settings。
-   Logout。

Header 高度建议约 `56–64px`。

### 8.1 Command / Navigation Search 限制

第一阶段的搜索能力定义为 **Command / Navigation
Search**，不是跨业务领域的全局实体搜索。

允许：

-   快捷跳转到已有页面；
-   快捷跳转到已有设置/管理入口；
-   可选搜索当前页面或当前已加载的受支持实体。

禁止：

-   暗示能够跨账号、节点、任务、问题进行完整全局搜索；
-   基于局部缓存伪装成全局结果；
-   为了 Header 完整性新增未定义的搜索后端。

如果未来要求真正的跨领域 Global Search，必须另行定义后端 query/search
contract。

------------------------------------------------------------------------

## 9. 页面统一结构

普通业务页面统一采用：

``` text
Breadcrumb

Page Title                              Primary Action
Page Description

Page Content
```

继续扩展现有 `PageShell` / `PageHeader`，而不是每个页面自行实现 Header。

页面必须统一：

-   Page gutter。
-   Section spacing。
-   Card gap。
-   Toolbar height。
-   Table density。
-   Typography。
-   Button hierarchy。
-   Status presentation。

------------------------------------------------------------------------

## 10. 中文界面语言要求

当当前 Locale 为：

``` text
zh-CN
```

所有面向用户的普通产品文案必须使用中文，包括：

-   Sidebar。
-   顶部导航。
-   Breadcrumb。
-   Page Title。
-   Section Title。
-   Button。
-   Tabs。
-   Filters。
-   Table Header。
-   Form Label。
-   Status。
-   Empty State。
-   Loading State。
-   Error State。
-   Confirmation Dialog。
-   Pagination。
-   Tooltip。

不得出现无必要的中英文混排。

### 10.1 允许保留英文或原始形式的内容

以下内容可以保留原始形式：

-   品牌名称，例如 `Relay Station`。
-   用户输入数据。
-   原始业务值。
-   Provider 名称。
-   ID / UUID。
-   URL。
-   HTTP。
-   OAuth。
-   TOTP。
-   CSRF。
-   P95 等必要技术缩写。

普通产品文案应优先中文化。

例如：

``` text
Node Status      → 节点状态
Success Rate     → 成功率
Recent Operations → 最近操作
```

而：

``` text
Node ID: 01H...
Provider: antigravity
```

中的技术标识和业务值可以保留。

------------------------------------------------------------------------

## 11. i18n 现有契约

本次重构必须 **复用现有 i18n foundation**，不得重新设计。

现有契约：

``` text
AppLocale = "zh-CN" | "en"

supportedLocales:
zh-CN
en

fallback:
zh-CN
```

Locale resolution：

``` text
valid persisted locale
→ browser locale
→ zh-CN
```

Normalization：

``` text
zh / zh-* → zh-CN
en / en-* → en
unsupported → zh-CN
```

LocalStorage key 保持：

``` text
relay-control.locale
```

只有用户显式选择语言时持久化。

不得因为浏览器语言自动写入 preference。

------------------------------------------------------------------------

## 12. Translation Resource

所有新增用户可见文案必须进入现有 source-controlled translation
resources。

必须同时提供：

``` text
zh-CN
en
```

不得先写硬编码英文，再以后补国际化。

Translation key 必须描述稳定产品语义，例如：

``` text
navigation.dashboard
navigation.accounts
navigation.nodes
navigation.operations
navigation.monitoring
navigation.problems
navigation.settings

dashboard.title
accounts.title
nodes.title
operations.title
```

不得使用翻译文本本身作为 key。

每次新增或删除 key 必须继续满足现有：

``` text
RESOURCE_PARITY
REFERENCED_KEY_COMPLETENESS
TRANSLATION_SOURCE_AUDIT
```

测试。

------------------------------------------------------------------------

## 13. Locale 切换

现有 `LocaleSwitcher` 行为必须保持。

允许修改其视觉形式，例如：

-   Header Dropdown。
-   Settings Select。
-   User Menu。

但必须继续支持：

-   `zh-CN → en` 即时切换。
-   `en → zh-CN` 即时切换。
-   Ant Design locale 同步变化。
-   Reload 后显式选择仍然存在。
-   localStorage 故障不导致页面崩溃。
-   当前 session 内切换仍然有效。

现有稳定测试语义原则上继续保留：

``` text
locale-selector
locale-option-zh-CN
locale-option-en
```

------------------------------------------------------------------------

## 14. 业务值与翻译严格分离

后端 machine value 不得因为 i18n 修改。

例如：

``` text
online
degraded
failed
running
outcome_unknown
```

继续作为原始业务值。

只在 presentation 层转换：

``` text
online → 在线 / Online
failed → 失败 / Failed
```

API enum、数据库 enum、状态机和业务判断不得使用翻译文本。

硬性要求：

``` text
outcome_unknown
!= success
!= failed
```

------------------------------------------------------------------------

## 15. 日期、时间、数字与百分比

继续使用现有 `foundation/format.ts` 的统一格式化边界。

使用：

``` text
Intl.DateTimeFormat
Intl.NumberFormat
```

不得在业务组件中自行散落：

``` text
toLocaleString(...)
手工拼日期字符串
手工拼 %
```

Locale 只改变 presentation。

Timezone 继续以 browser/system local timezone 为显示真相。

不得因为语言切换改变 timezone。

------------------------------------------------------------------------

## 16. Dashboard

新增统一的 Control Overview 页面。

Dashboard 是 **只读汇总入口**，不得成为新的业务真相源。

允许展示的指标必须能够明确回答：

``` text
数据来自哪个 API？
统计窗口是什么？
分母是什么？
是否为完整数据？
是否来自分页结果？
```

不得根据一个分页结果推导全局指标。

### 16.1 第一阶段允许展示

Dashboard 是规划中的独立管理概览页面，不等同于当前 ManagementPage
重命名。`\n`{=tex}`\nDashboard`{=tex}展示分为两类：

``` text
Authoritative Summary
Navigation Summary
```

`Authoritative Summary` 只有在 API
本身能够提供完整、权威统计语义时才能展示数字/比例，例如：

-   Control health。
-   Gateway 状态。
-   节点状态摘要。
-   Account / Inventory 摘要。
-   Durable Jobs 摘要。
-   Problems 摘要。
-   最近业务异常。
-   Monitoring capacity 状态。

每个 Metric / Summary 在实施前必须能够登记：

``` text
source API
aggregation semantics
time window
denominator
pagination completeness
```

如果现有 API 只能提供局部、分页或当前加载数据，则只能作为
`Navigation Summary` / 状态入口展示，不得包装成全局统计。

### 16.2 当前不得直接假定存在

以下指标目前没有独立的全局统计契约：

``` text
Global Success Rate
Last Week Comparison
Global Operation Trend
Global Alerts Trend
Global Recent Account Operations
```

因此不得仅为了界面完整而填充：

``` text
96%
↑ 4%
Last 7 Days
+12% vs last week
```

等无真实语义数据。

如果产品确实要求这些指标，应作为新的统计 API / aggregation contract
单独设计。

------------------------------------------------------------------------

## 17. 账号页面

账号页面应成为账号相关能力的主要工作入口。

应逐步整合当前已经存在的：

-   Account Inventory。
-   Account Quality。
-   Account Availability。
-   Request History。
-   Quality Incidents。
-   Provider snapshot。
-   Inventory freshness。
-   Account Operations。
-   Account detail。

列表应支持现有真实过滤能力，不新增假过滤条件。

账号详情可以继续采用：

-   Drawer。
-   Modal。
-   Detail panel。

具体形式可以调整，但不得改变 underlying business semantics。

### 17.1 账号页面迁移顺序

账号域是当前 UI 重构中最宽的业务 surface，不得一次性整体重写。

建议按以下顺序迁移：

``` text
Accounts IA / 页面骨架
→ Account Inventory
→ Account List / Detail
→ Quality / Availability / Request History / Incidents
→ Account operation entry / result presentation
```

每个 slice：

-   继续使用现有 Control API / Orval client；
-   不新增假字段、假过滤、假统计；
-   focused tests 通过后再进入下一个 slice；
-   Account Operations 的执行语义仍由现有业务组件/接口拥有。

------------------------------------------------------------------------

## 18. 节点页面

中文导航名称统一为：

``` text
节点
```

英文为：

``` text
Relay Nodes
```

节点页面统一承载 Node 管理和可观测信息。

至少覆盖现有能力：

-   Node asset。
-   Stable instance identity。
-   Node health。
-   Driver。
-   Monitoring enabled / disabled。
-   Connection test。
-   Lifecycle management。
-   Inventory status。
-   Provider status。
-   Gateway binding context。
-   Account quality context。

Gateway 与 Node 必须保持领域边界。

不得把 Gateway 当成一种 Relay Node。


### 18.1 Asset / Gateway 辅助管理入口归属

Phase 10 一级 Sidebar 不新增 `Gateway` 或 `Assets` 项，也不得把 Gateway 当成 Relay Node。为避免 UI 重构丢失现有 Asset Registry 中已经存在但未分配一级导航的能力，第一阶段保留 `/assets` 作为 **辅助资产与运行配置入口**。

`/assets` 第一阶段继续拥有：

- Environment identity 的只读展示；
- Gateway lifecycle management；
- Driver catalog 的只读展示；
- Current Provider Policy 的只读展示。

`/nodes` 成为 Relay Node lifecycle / monitoring 的唯一主要页面 ownership。Stage 3B 完成后，`/assets` MUST NOT 继续提供第二套可执行 Node Register / Edit / Retire / Replace / Health / Connection Test / Monitoring Enable / Monitoring Disable 控件；原 Node 区域应移除或收敛为指向 `/nodes` 的 navigation-only 入口。

`/assets`：

- 不进入一级 Sidebar；
- 可由 Command / Navigation Search 跳转；
- 可从 Dashboard / Monitoring 中真实存在的 Gateway / environment / policy context 进入；
- Gateway、Driver / Provider Policy 与 Relay Node 保持独立领域语义；
- 不新增后端能力，也不改变既有 Asset / Gateway / Node API contract。

后续若产品要求 Gateway 或 Assets 成为一级导航，必须另行更新信息架构需求；不得在实现中自行增加。

------------------------------------------------------------------------

## 19. 操作页面

操作页面应整合当前持久任务和管理操作的可观察入口。

当前真正存在的能力包括：

``` text
/api/jobs
/api/jobs/{job_id}

/api/account-operations/disable
/api/account-operations/enable
/api/account-operations/remove
/api/account-operations/upload-new
/api/account-operations/replace-existing
/api/account-operations/{command_id}
...
```

需要特别注意：

> 当前 Account Operations 没有"列出所有 account operations"的全局 list
> API。

因此不能直接把 `最近操作` 表格解释为"所有 Account Operations
的全局历史"。

第一阶段的 `Operations` 页面以 **Durable Jobs 为主列表 / 历史入口**。

``` text
Operations
└── Durable Jobs
```

Account Operation 仍从账号上下文进入已有 command/detail flow；不得在没有
list API 的情况下伪造"最近所有 Account Operations"。

如果产品要求独立的 Account Operation History，则应另行定义 list/query
API，再扩展 Operations 页面。

------------------------------------------------------------------------

## 20. 监控页面

监控页面只负责观察和诊断。

可组织现有：

-   Node health。
-   Gateway health。
-   Provider inventory state。
-   Account quality。
-   Poll capacity。
-   Inventory freshness。
-   Problems。
-   Request evidence。
-   Available runtime diagnostics。

不得让监控页面：

-   修改 Gateway routing。
-   修改数据面调度状态。
-   自动 drain。
-   自动 repair duplicate ownership。
-   自动改变 account selection。

Control
仍然只负责观察、关联、快照、分析、告警和管理操作，不进入请求数据面。

### 20.1 Monitoring ownership

Monitoring 是 **cross-domain read-only diagnostic
view**，不是新的业务真相源。

所有数据 ownership 保持在原领域：

``` text
Node health        → Node domain
Gateway health     → Gateway/Asset domain
Account quality    → Account domain
Problems           → Problems domain
Inventory evidence → Inventory domain
```

Monitoring 只负责组合、关联和跳转，不得复制业务状态机、重新定义 status
taxonomy，或成为任何执行动作的 owner。

------------------------------------------------------------------------

## 21. 问题页面

现有 `Problems` 能力统一呈现为中文"问题"、英文 `Problems`。

必须保留现有业务分类和 error taxonomy。

UI 可以优化：

-   Severity badge。
-   Reason。
-   Provider。
-   Node。
-   Account。
-   First seen。
-   Last seen。
-   Related evidence。
-   Filter toolbar。
-   Retry / read-state。

但不得把：

``` text
unknown
unavailable
outcome_unknown
```

错误翻译成：

``` text
success
failed
```

或其他不同业务语义。

------------------------------------------------------------------------

## 22. 设置页面

设置页面可以重新组织现有 Management 能力，包括：

-   Current session。
-   Administrator management。
-   Password。
-   MFA。
-   Reauthentication。
-   Recovery codes。
-   Locale。
-   UI preference。

`UI preference` 第一阶段只允许 browser-local、非敏感的 presentation
preference。

如果当前没有后端 preference
API，不得暗示这些设置会跨设备/跨浏览器同步，也不得为了 Settings
页面新增后端 preference 存储。

高风险管理操作仍然遵守现有 reauthentication、安全和 audit 契约。

UI 重构不得削弱：

-   MFA。
-   CSRF。
-   session expiration。
-   reauthentication。
-   password policy。
-   administrator authorization。

------------------------------------------------------------------------

## 23. 不得虚构尚不存在的 System 功能

当前 Control OpenAPI 中没有面向 Web UI 的：

``` text
Audit Logs list API
API Keys management API
```

因此此次 UI 重构不得：

-   创建假的审计日志数据。
-   创建假的接口密钥管理页面。
-   用 Mock 数据伪装成生产能力。
-   暗示尚不存在的后端能力已经实现。

如未来需要：

``` text
审计日志 / Audit Logs
接口密钥 / API Keys
```

必须另行定义后端契约/OpenSpec change。

`文档 / Documentation` 可以作为静态或外部文档入口，不要求后端 API。

因此第一阶段 Sidebar 不显示 `审计日志 / Audit Logs` 与
`接口密钥 / API Keys`；`系统 / System`
分组只有在存在真实可用入口时才出现。

------------------------------------------------------------------------

## 24. 数据真实性

生产 UI 中不得使用 Mock 数据填充真实管理页面。

所有：

-   Count。
-   Rate。
-   Trend。
-   Status。
-   Alert。
-   Latency。
-   Duration。
-   Comparison。

都必须有明确真相源。

如果当前 API 不支持某项展示：

``` text
不显示
或
显示明确 unavailable / not available
或
另起 Backend/OpenSpec change
```

不得通过前端猜测。

------------------------------------------------------------------------

## 25. PC 页面布局要求

新的管理界面采用固定 Desktop 管理控制台结构。

建议：

-   Sidebar 固定宽度约 `220–260px`。
-   Header 固定高度约 `56–64px`。
-   主内容区域保持统一 padding。
-   Dashboard 卡片使用 Desktop Grid。
-   Table 优先保证 PC 信息密度。
-   不为了移动端牺牲表格字段。
-   不需要在窄屏将表格转换成 Card List。
-   不需要实现 Hamburger Menu / Drawer Sidebar。

------------------------------------------------------------------------

## 26. PC 表格原则

由于只支持 PC，可以充分利用横向空间。

例如账号 / 操作 / 节点表格允许直接展示较完整字段：

``` text
账号
Provider
节点
状态
质量
最近活动
成功率
延迟
操作
```

无需为了手机适配：

-   隐藏大量列。
-   将每行变成 Card。
-   使用横向 swipe 作为主要操作方式。
-   创建独立 Mobile column configuration。

如果数据列很多，可以使用：

-   合理的列宽。
-   Ellipsis。
-   Tooltip。
-   Fixed action column。
-   PC 横向滚动。

但应优先在常见 PC 宽度下完成主要信息展示。

------------------------------------------------------------------------

## 27. Accessibility

视觉重构不得降低现有可访问性。

必须保留：

-   Semantic label。
-   Accessible name。
-   Keyboard navigation。
-   Focus state。
-   Loading semantics。
-   Error semantics。
-   非纯颜色状态表达。

状态不能只通过红/绿颜色区分。

例如应采用：

``` text
● 失败
● Failed
```

而不是只有一个红点。

------------------------------------------------------------------------

## 28. Playwright / data-testid

现有 Locator Policy 继续作为强制要求。

所有用于用户交互的 Browser E2E locator 必须使用：

``` text
getByTestId()
```

新的 `data-testid` 必须表达稳定业务语义。

例如：

``` text
sidebar-accounts
sidebar-nodes
sidebar-operations
account-upload-new
node-health-refresh
operation-details-<id>
```

不得使用：

``` text
button-1
left-menu-item-2
row-3
blue-button
```

Test ID 不得依赖：

-   当前语言。
-   文案。
-   DOM order。
-   CSS class。
-   Ant Design 内部 DOM。

------------------------------------------------------------------------

## 29. API 与安全边界

前端继续只调用 Control API。

不得：

-   Browser 直接调用 Gateway。
-   Browser 直接调用 Relay Node。
-   Browser 直接调用 Prometheus。
-   Browser 获取 Node Management Credential。
-   Browser 获取 Gateway Credential。
-   Browser 获取 Provider credential/token。
-   将 Secret 暴露在 DOM、日志或错误信息中。

`api/openapi.yaml` 继续是前后端 API 契约真相源。

生成的 Orval client 不得手工修改。

------------------------------------------------------------------------

## 30. 业务兼容性

此次 UI 重构必须保持：

``` text
Backend API semantics
Authentication
Authorization
CSRF
MFA
Reauthentication
Account lifecycle
Node lifecycle
Gateway lifecycle
Inventory truth
Binding truth
Problem classification
Job lifecycle
Account-operation semantics
```

不变。

尤其不得将 UI aggregate / presentation state 反过来作为业务执行真相。

------------------------------------------------------------------------

## 31. 实施阶段与切片

本次 UI 重构采用分阶段、分页面 slice
的方式实施，不一次性重写全部管理界面。

### Stage 0 --- Requirements / IA Freeze

冻结：

``` text
information architecture
page/domain ownership
PC-only platform scope
i18n / Chinese navigation naming
Dashboard source-of-truth matrix
Operations = Jobs-first
Monitoring = read-only cross-domain diagnostics
System unavailable-entry policy
Settings preference boundary
test contract
```

### Stage 1 --- Foundation

实现：

``` text
Frontend theme tokens
Global App Shell
Sidebar
Global Header
Command / Navigation Search
PageShell / PageHeader refinement
PC-only shell contract
i18n navigation/shell integration
Loading / Empty / Error presentation
```

不得在 Foundation 阶段迁移业务语义或新增后端契约。

### Stage 2 --- Dashboard / Overview

实现只读 Dashboard。

只允许：

``` text
Authoritative Summary
Navigation Summary
```

不得为了卡片完整度新增假统计、趋势或比较。

### Stage 3 --- Domain Surfaces

按真实页面/领域逐个 slice：

``` text
Accounts
Relay Nodes
Operations / Durable Jobs
Monitoring
Problems
Settings
```

其中 Accounts 再按 §17.1 细分。

每个 slice：

``` text
implementation
→ focused unit/component
→ relevant Browser proof
→ local commit
```

不要等所有页面完成后才统一测试。

### Stage 4 --- Unified Acceptance

统一验证：

``` text
zh-CN
en
live locale switch
1280×720
1440×900
translation audits
relevant PC Browser E2E
generated drift
```

### Stage 5 --- Final Independent Review / Closeout

整体 review：

-   信息架构是否仍符合冻结要求；
-   是否引入了假能力/假数据；
-   是否改变 backend / database / Gateway / Node contract；
-   是否保留认证、安全、业务语义；
-   是否存在跨 slice presentation drift；
-   是否完成 PC-only acceptance contract。

不要在 Final Review 阶段新增 architecture workstream。

------------------------------------------------------------------------

## 32. 验收要求

至少必须通过：

``` text
npm test
npm run typecheck
npm run build

RESOURCE_PARITY = PASS
REFERENCED_KEY_COMPLETENESS = PASS
TRANSLATION_SOURCE_AUDIT = PASS

ZH_CN_PC_BROWSER = PASS
EN_PC_BROWSER = PASS
LIVE_LOCALE_SWITCH = PASS

1280x720 = PASS
1440x900 = PASS

RELEVANT_BROWSER_E2E = PASS
GENERATED_DRIFT = NONE
```

新增中文完整性要求：

``` text
ZH_CN_NAVIGATION_ENGLISH_LEAK = ZERO
ZH_CN_UI_UNINTENDED_ENGLISH_LEAK = ZERO
```

其中"英文泄漏"不包括：

-   Relay Station 品牌名。
-   用户输入数据。
-   原始业务标识。
-   ID / UUID。
-   Provider 名称。
-   必要技术缩写。

关键页面应增加稳定的视觉 / 布局验收，至少覆盖：

``` text
1280×720 zh-CN
1280×720 en
1440×900 zh-CN
```

验收以 **semantic/layout invariant** 为主：

-   Sidebar 存在且宽度符合 contract。
-   Header 高度/布局稳定。
-   主内容无阻塞性 overflow。
-   Page spacing / Card density / Table density 符合设计。
-   关键操作可见且可达。
-   状态表达不只依赖颜色。
-   翻译长度变化不破坏布局。
-   中文/英文模式无非预期语言泄漏。

截图可以作为 review evidence，但默认不建立大面积 `toHaveScreenshot()`
pixel-perfect gate。只有确有稳定价值的局部视觉 contract
才允许使用严格截图比较。

------------------------------------------------------------------------

## 33. OpenSpec 要求

已归档的
`openspec/changes/archive/2026-09-20-simplify-node-gateway-management-credentials`
属于此前 Phase 8 credential work，与 Phase 10 UI modernization 无关。

**不得把 Phase 10 UI 重构并入该历史 change。**
Phase 10 独立 change 为：
`modernize-control-management-shell`。

此次工作实施前应建立一个新的独立 OpenSpec change：

``` text
modernize-control-management-shell
```

该 change 的 scope 是：

``` text
management shell
+ information architecture
+ current surface presentation migration
```

不是只修改 Sidebar/Header，也不是重新设计业务模型。

Requirements 中冻结：

``` text
information architecture
visual foundation
page scope
PC-only platform scope
i18n preservation
Chinese navigation naming
routing impact
API impact
test contract
desktop acceptance
```

如果仅改变 presentation，不新增 API：

``` text
Backend change = NO
Database change = NO
Gateway change = NO
Relay Node change = NO
```

如果 Dashboard 等功能发现必须新增聚合 API，则先更新
OpenSpec，不能在前端实现过程中偷偷扩大 scope。

------------------------------------------------------------------------

### Problems 命名约定

``` text
中文：
问题

英文：
Problems
```

原因：

-   当前系统页面、路由和代码均使用 Problems：
    -   `/problems`
    -   `ProblemsPage`
    -   `ProblemsView`
-   Problems 表示系统观察到的问题/异常域。
-   不使用 Issues，避免产生"工单/问题跟踪系统"的产品含义。

未来如果引入独立 Issue Tracking / Workflow，应另行定义产品需求。

## 33.1 第一阶段冻结补充

实施前必须明确以下结论：

``` text
System unavailable entries:
Audit Logs / API Keys = HIDDEN

Header search:
Command / Navigation Search
!= Global entity search

Dashboard:
truth-source matrix required
no pagination-derived global metrics

Operations:
Durable Jobs first
no fake global Account Operations history

Monitoring:
read-only cross-domain diagnostic composition

Settings UI preference:
browser-local unless backend contract exists

Visual acceptance:
semantic/layout invariants + screenshot evidence
not broad pixel-perfect gating

Backend change = NO
Database change = NO
Gateway change = NO
Relay Node change = NO
```

------------------------------------------------------------------------

## 34. 最终硬性约束

### 34.1 平台约束

> Relay Station Control Web 本阶段仅面向 PC Desktop Browser，不要求
> Mobile 或 Tablet 响应式支持。所有 UI 设计、布局、信息密度和验收均以 PC
> Web 为目标。

### 34.2 语言约束

> Control Web 完整支持 `zh-CN` 和
> `en`。中文模式下所有导航、标题、按钮、筛选、状态及普通产品文案必须完整使用中文，不允许出现无必要的中英文混排；英文模式完整使用英文。品牌名称、原始数据值和必要技术标识除外。

### 34.3 节点命名

> 英文 `Relay Nodes` 在中文导航和普通页面标题中统一显示为
> **"节点"**。技术文档、协议说明、字段定义或需要精确指代领域对象时，可继续使用
> `Relay Node`。

### 34.4 数据约束

> 页面展示的所有生产数据必须来自明确的真实数据源。不得为了完成视觉布局而使用
> Mock 数据冒充真实业务数据。

### 34.5 架构约束

> UI 重构不得改变 Control、Gateway、Relay Node 的既有职责边界，不得让
> Control 进入请求数据面，不得借前端改造引入新的调度真相或执行真相。
