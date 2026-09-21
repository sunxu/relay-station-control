# Design: Phase 10 Control Web Management Shell Modernization

## 1. Design intent

本 change 是 presentation / information architecture modernization。它不新建业务真相源，不改变 Control / Gateway / Relay Node 职责，也不进入请求数据面。

设计优先级：

1. 先冻结 IA 与 truth-source；
2. 再统一 shell / theme；
3. 再按领域迁移页面；
4. 每个 slice 单独证明不改变业务语义。

## 2. Current frontend baseline

当前 `web/src/App.tsx` 通过 `AuthContext` 的 `AuthRoute` 选择 lazy page。

当前已认证 route：

- `/` -> management
- `/assets` -> assets
- `/jobs` -> jobs
- `/topology` -> topology
- `/problems` -> problems

`AuthContext` 通过 `window.location.pathname`、`window.history.pushState()` 与 `popstate` 实现轻量 routing，没有 React Router。

Phase 10 不引入第二套路由框架。

## 3. Target IA and route migration

目标一级 IA：

| Sidebar | Target canonical route | Existing source surface |
|---|---|---|
| Dashboard / 仪表盘 | `/` | 新 presentation overview |
| Accounts / 账号 | `/accounts` | Topology/Account sections + existing APIs |
| Relay Nodes / 节点 | `/nodes` | current assets/topology Node portions |
| Operations / 操作 | `/operations` | `/jobs` + account operation contextual flow |
| Monitoring / 监控 | `/monitoring` | read-only topology/quality/capacity composition |
| Problems / 问题 | `/problems` | existing ProblemsPage |
| Settings / 设置 | `/settings` | current ManagementPage |

迁移期兼容：

- `/assets` 保留为 Gateway 辅助管理入口；
- `/jobs` alias 到 Operations/Durable Jobs；
- `/topology` alias 到 Monitoring，直到相关 Node/Account slices 完成；
- `/problems` 保持；
- `/` 在 Stage 1 shell 建立后成为 Dashboard；Settings 使用 `/settings`；
- Browser back/forward MUST 继续由 `popstate` 正确工作。

旧 route 的 alias 删除必须等 Stage 4 unified acceptance 后独立评审，Phase 10 初始实现不删除。

所有 Phase 10 product route MUST 采用统一 pathname normalization：canonical path 使用无 trailing slash 形式，但直接访问单个 trailing slash（例如 `/accounts/`、`/nodes/`、`/operations/`、`/monitoring/`、`/problems/`、`/settings/`）必须解析为相同 product route，而不是 fallback 到 Dashboard。不得引入 React Router 或 site-wide rewrite framework。

## 4. Asset / Gateway auxiliary surface

一级 Sidebar 不包含 Gateway / Assets，但既有 Asset Registry 中未获得一级导航的能力不能丢失。

第一阶段 `/assets` 继续承担：

- Environment identity 只读展示；
- Gateway lifecycle management；
- Driver catalog 只读展示；
- Current Provider Policy 只读展示。

Relay Node lifecycle / monitoring 的 presentation ownership 迁移到 `/nodes`。Stage 3B 完成后，`/assets` 不得保留第二套可执行 Node lifecycle / health / monitoring 控件；Node 区域应删除或变为 navigation-only link。

`/assets` 从 Command / Navigation Search 可达；Dashboard / Monitoring 中存在真实 Gateway、environment 或 policy context 时可跳转。该拆分不改变 Asset/Gateway/Node API、安全、revision、credential 或 lifecycle contract。

## 5. App Shell composition

认证完成后的普通页面：

```text
AppShell
├── AppSidebar
├── GlobalHeader
└── PageRegion
    └── PageShell
        ├── Breadcrumb
        ├── PageHeader
        └── PageContent
```

Bootstrap/Login/Activation/OneTimeMaterial 不进入 AppShell。

AppShell 只负责 navigation / presentation，不拥有业务 fetch、状态机或 mutation。

## 6. Theme

`FrontendFoundationProvider` 继续持有 Ant Design `ConfigProvider`。

`foundationTheme` 是唯一主题 token 入口，计划冻结：

- primary color
- layout / card background
- border
- radius
- control height
- typography hierarchy
- success/warning/error semantic colors
- table density
- form density

允许 AppShell/AppSidebar/GlobalHeader/MetricCard/StatusBadge/PageToolbar 等有应用语义组件；禁止 Button/Card/Table 的纯重命名 wrapper。

## 7. Dashboard data ownership

详见 `dashboard-truth-source-matrix.md`。

第一阶段权威数字只允许来自完整 API：

- Control health: `/api/healthz`
- Gateway asset counts: `/api/assets/gateways` 的 `gateway_counts`
- Node asset counts: `/api/assets/nodes` 的 `node_counts`
- Inventory poll capacity diagnostics: `/api/account-inventory/poll-capacity`

以下 surface 是分页/query，没有全局 total/aggregate，因此只做 Navigation Summary：

- `/api/jobs`
- `/api/problem-accounts/query`
- `/api/account-inventory/query`
- per-node account quality / request history / incidents
- per-instance health probe

Dashboard MUST NOT 自动执行 Gateway/Node connection test/health probe 来制造“实时健康总览”；显式 network-cost admin action 的所有权保持原页面。

## 8. Domain ownership

### Accounts

Accounts 是逻辑账号工作入口；按 requirements 指定顺序切片：

1. IA / skeleton
2. Inventory
3. Account list/detail
4. Quality/Availability/Request History/Incidents
5. Account operation entry/result presentation

### Relay Nodes

只承载 Node 领域和其关联观察。Gateway 仅作为 context/link，不被合并为 Node。

### Operations

Durable Jobs first。Account Operation 无全局 list，因此从 Account context 进入。

### Monitoring

只读 composition。不得拥有 mutation 或状态机。

`/monitoring` 在 Phase 10 成为既有 `node-centric-topology-ui` 诊断能力的主要 presentation owner；迁移期 `/topology` 作为兼容 alias。现有 Inventory evidence、Binding truth/resolution、Duplicate Ownership、Provider state、Account Quality 等 read-only contracts 必须继续满足，不得因改名或重组页面而丢失。

### Problems

保持现有 Problems domain / route / taxonomy。

### Settings

从 ManagementPage 迁移既有认证/管理员能力；UI preference 仅 browser-local。

## 9. Command / Navigation Search

第一阶段 search index 来自静态 navigation entries 与明确允许的当前页面 loaded entities。

结果项 MUST 标记来源，不得给局部实体结果“Global”语义。

若实现当前页面 entity search：
- search query / result state 仅驻留当前页面内存，不写 browser history、URL query、LocalStorage 或其他持久存储；
- canonical `account_key` MUST NOT 因 Search 进入 navigation URL/storage/logs/metrics；
- Secret / credential / token 永远不进入 Search index；
- entity jump 只能复用既有安全 selection/detail flow；没有安全 deep-link identity 时只在当前页面内选择，不创造新 URL contract。

不新增搜索 API。

## 10. i18n

复用现有 `FrontendFoundationProvider`、`LocaleSwitcher`、`resources.ts` typed resource contract。

Stage 1 起新增 `navigation.*`、`dashboard.*`、`accounts.*`、`nodes.*`、`operations.*`、`monitoring.*`、`problems.*`、`settings.*` 等稳定语义 key。

当前资源中仍存在历史中英文混排；Phase 10 对迁移到新页面的用户可见普通文案进行逐 slice 清理，且 Stage 4 要求中文泄漏为 ZERO（约定例外除外）。

machine value 不进入翻译 key。

## 11. Desktop layout

- Sidebar: 220–260px fixed desktop range
- Header: 56–64px
- content min width: 以 1280 viewport 可用为准
- tables: desktop density first，必要时 horizontal scroll
- 不实现 hamburger/drawer/mobile cards

不得通过隐藏关键 PC table columns 来兼容旧 mobile contract。

## 12. Accessibility

状态使用 icon/text/badge 组合，不能仅红/绿。

视觉 theme 改动需检查 focus、keyboard、accessible name、loading/error semantics。

## 13. Security and Secret handling

无 Secret 新 surface。

Header、Search、Dashboard 不得展示：

- Node management credential
- Gateway credential
- Provider/API/OAuth token
- DingTalk webhook/signing secret
- raw upstream response

浏览器仍只访问 Control。

## 14. Concurrency / idempotency / UTC

本 change 不新增服务端 transaction、worker、状态机、idempotency 或数据库写。

现有 mutation 的 concurrency/idempotency/UTC 行为保持由原业务契约拥有；UI 不缓存或推断执行成功。

## 15. Rollout and rollback

按 Stage 1–4 slice rollout；每个 slice 单独 commit 和 focused proof。

rollback = 回退前端 artifact / frontend commits；无 Migration down，无数据库 cleanup。

旧 route alias 在整个 Phase 10 初始 rollout 保留，降低 deep-link rollback 风险。

## 16. Scope escalation rule

任何发现的新后端统计/search/history/preference contract：

1. 标记 `BACKEND_CONTRACT_REQUIRED`；
2. 停止对应 UI 功能实现；
3. 更新 OpenSpec proposal/spec/design/TCCR；
4. 经独立 Architecture Review 后才可实现。

不得用前端聚合、Mock 或 local cache 绕过。
