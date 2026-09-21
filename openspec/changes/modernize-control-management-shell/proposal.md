## Why

Phase 10 需要在不改变 Relay Station 业务模型、API、安全边界和数据真相源的前提下，将现有 Control Web 重组为统一的 PC 管理控制台。当前前端已有 Phase 8 i18n / foundation 基础，但页面导航、视觉层级、跨页面信息架构和桌面信息密度仍不统一；历史 Mobile responsive 验收也与新的 PC-only 产品范围冲突。

本 change 以 `docs/phase10/CONTROL_WEB_UI_REQUIREMENTS_CN.md` 为 Phase 10 Requirements 真相源，先冻结 information architecture、page/domain ownership、Dashboard truth-source matrix 与 test contract，再按 slice 实施。

## What Changes

- 建立统一的 PC-only authenticated management shell：固定 Desktop Sidebar、Global Header、统一 PageShell / PageHeader 与 foundation theme token；Bootstrap、Login、Activation、One-Time Material 保持独立安全布局。
- 冻结一级信息架构为 `Dashboard / Accounts / Relay Nodes / Operations / Monitoring / Problems / Settings`；`zh-CN` 对应“仪表盘 / 账号 / 节点 / 操作 / 监控 / 问题 / 设置”，英文问题域统一使用 `Problems`，不使用 `Issues`。
- 保留 Gateway 与 Relay Node 的领域边界；既有 `/assets` 继续作为 Gateway 辅助管理入口，但不新增一级 Gateway Sidebar 项，也不把 Gateway 包装成 Relay Node。
- Header 第一阶段仅实现 Command / Navigation Search，不实现跨业务领域 Global entity search。
- 新增只读 Dashboard presentation，但每个 metric/summary 必须先登记真实 source API、aggregation semantics、time window、denominator 与 pagination completeness；分页/query surface 不得被包装成全局统计。
- Accounts、Relay Nodes、Operations、Monitoring、Problems、Settings 按既有业务 ownership 迁移；Operations 第一阶段以 Durable Jobs 为主列表，Monitoring 保持 read-only cross-domain diagnostics。
- 正式 supersede Phase 8 的 Mobile / Tablet responsive product acceptance；Phase 10 仅以 `1280×720`、`1440×900` 等 Desktop Browser contract 验收。
- 保留现有 `zh-CN / en` i18n、locale persistence、formatting、Authentication、Authorization、CSRF、MFA、Reauthentication 与 Playwright `getByTestId()` locator policy。
- 扩展当前 `pathname + history.pushState + popstate` 的轻量 routing model；不引入 React Router，并在迁移期保留 `/assets`、`/jobs`、`/topology` 等既有 deep-link compatibility。
- 若实现中发现 Dashboard aggregate、Global Search、Account Operation History 或跨设备 preference 必须依赖新后端契约，对应功能必须停止并先更新 OpenSpec；不得以前端缓存、分页聚合或 Mock 数据绕过。

## Capabilities

### New Capabilities

- `control-management-shell`：Phase 10 Control Web 的 PC-only management shell、information architecture、Dashboard truth-source policy、domain-surface presentation ownership、i18n/desktop acceptance 与安全/测试边界。

### Modified Capabilities

- `asset-registry`：仅修改既有 Web presentation ownership。`/assets` 保留 Environment / Gateway / Driver catalog / Current Provider Policy 辅助入口；Relay Node lifecycle / monitoring presentation 迁移到 `/nodes`，既有 API、CSRF、revision、credential、lifecycle 与 operation contract 不变。
- `node-centric-topology-ui`：仅修改历史窄屏 presentation acceptance；Phase 10 supersede `390px` Mobile requirement，以 Desktop PC contract 验收。其 read-only truth、identity protection、Inventory / Binding / Duplicate / Provider / Account Quality semantics 不变。

Backend/API/Database/Gateway/Relay Node business capability contract 不因本 change 修改；上述 modified capability 仅用于正式 reconciliation 已存在的 UI canonical contracts。

## Impact

受影响仓库仅为 `relay-station-control`，主要影响 `web/` frontend、前端 Browser/unit/component tests、translation resources 与 Phase 10 planning/evidence。

Truth-source impact：

- OpenAPI: NO CHANGE
- Database / Migration / sqlc: NO CHANGE
- Generated API clients: NO semantic change；最终必须 `GENERATED_DRIFT = NONE`
- Metrics / Audit: NO CHANGE
- Gateway source: NO CHANGE
- Relay Node source: NO CHANGE
- Data plane: NO IMPACT

Security / compatibility：

- Browser 继续只调用 Control API。
- Authentication、Authorization、CSRF、MFA、Reauthentication、session expiration 不变。
- Node/Gateway/Provider credentials 与其他 Secret 不进入 Dashboard、Search、DOM、log 或 ordinary error。
- 现有 `/problems`、ProblemsPage、ProblemsView 保持。
- `/assets`、`/jobs`、`/topology` 在初始 rollout 保持可达或提供明确兼容 alias。
- 回滚方式为回退 frontend artifact / Phase 10 frontend commits；无 migration down 或数据修复。

本 change 遵循 System Design v1.8 / R4.7、ADR-0001 / ADR-0002 和当前 `AGENTS.md` 的 Control management/observability 边界。

## Non-goals

- 不新增 Backend API、Migration、sqlc、数据库表/函数或状态机。
- 不修改 Gateway / Relay Node 源码或职责边界。
- 不引入 React Router、Tailwind CSS、shadcn/ui、Material UI 或第二套 Design System。
- 不实现 Global entity search。
- 不实现 Audit Logs / API Keys 等当前后端不存在的能力。
- 不实现全局 Account Operations history。
- 不用 Mock 数据冒充 production UI 数据。
- 不重新设计 Authentication / Authorization / CSRF / MFA / Reauthentication。
- 不要求 Mobile / Tablet 支持。

## Status

Phase 10 Stage 0 Requirements / IA Freeze candidate。

Planning / Architecture Review 已授权；implementation 在本 change strict validation、Test Contract Coverage Review 与独立 Architecture Review PASS 后开始。
