# Tasks

> Stage 0 已冻结 requirements / IA candidate。实现任务在 Test Contract Coverage Review 与 Architecture Review PASS 后执行。每个实现 slice 完成 focused proof 后单独 commit。

## 0. Stage 0 — Requirements / IA Freeze

- [x] 0.1 将用户提供的 v3 Requirements 收敛为 `docs/phase10/CONTROL_WEB_UI_REQUIREMENTS_CN.md`，显式解决 `Issues` / `Problems` 冲突。
- [x] 0.2 冻结 PC-only 范围、`1280×720` / `1440×900` 验收，并声明 Phase 8 `390×844` responsive contract 被 supersede。
- [x] 0.3 冻结七项一级 Sidebar IA、Gateway `/assets` 辅助入口、页面/domain ownership。
- [x] 0.4 建立 Dashboard truth-source matrix，禁止 pagination-derived global metrics。
- [x] 0.5 冻结 Operations = Durable Jobs first、Monitoring = read-only composition、Settings browser-local preference。
- [x] 0.6 盘点当前手写 AuthContext routing，并冻结“不引入 React Router”与旧 deep-link alias。
- [x] 0.7 完成 Test Contract Coverage Review。
- [x] 0.8 完成 planning validation；确认 Backend/DB/Gateway/Node business contract change = NO。
- [x] 0.9 Architecture Review Round 1 识别并修正 canonical UI contract 冲突：`asset-registry` page ownership 与 `node-centric-topology-ui` 390px acceptance。
- [x] 0.10 冻结 `/assets` Environment/Gateway/Driver/Policy ownership、`/nodes` sole executable Node lifecycle ownership、route trailing-slash normalization 与 Search identity safety。
- [x] 0.11 Re-review 补齐 `relay-node-management-operations` presentation delta，并冻结 Dashboard/Monitoring/Node page mount 零自动 probe。
- [x] 0.12 在真实仓库运行 `openspec validate modernize-control-management-shell --strict` 与 `openspec show modernize-control-management-shell --json --deltas-only`，实际解析四个 delta capabilities 并记录 PASS。
- [x] 0.13 独立 Architecture Final Re-review PASS 后授权 Stage 1 implementation。
- [x] 0.14 Verify deploy-v0.9.3 production HTTPS acceptance / release closeout / immutable tag gate PASS before implementation authorization。

```text
Stage 0 = COMPLETE / PASS
Stage 1 Foundation = AUTHORIZED
```

## 1. Stage 1 — Foundation

- [ ] 1.1 扩展 `foundationTheme` tokens，并用 focused theme tests 固定 semantic token 行为。
- [ ] 1.2 实现 `AppShell` / `AppSidebar` / `GlobalHeader`，Bootstrap/Login/Activation/OneTimeMaterial 保持独立布局。
- [ ] 1.3 扩展 AuthContext 手写 routing 支持 `/accounts`、`/nodes`、`/operations`、`/monitoring`、`/settings`，保留 `/assets`、`/jobs`、`/topology` 兼容 alias。
- [ ] 1.4 实现 Command / Navigation Search；仅静态导航和明确允许的当前 loaded entities。
- [ ] 1.5 扩展 PageShell/PageHeader 支持 breadcrumb、统一 actions/description/layout。
- [ ] 1.6 新增 zh-CN/en navigation/shell translations，同步 Ant Design locale。
- [ ] 1.7 建立 1280/1440 desktop shell Browser proof；移除/调整与 Phase 10 冲突的 mobile-only acceptance。
- [ ] 1.8 运行 focused unit/component、typecheck、build、translation audits、Browser proof；独立 commit。

## 2. Stage 2 — Dashboard

- [ ] 2.1 实现 Dashboard skeleton 与 Navigation Summary。
- [ ] 2.2 只接入 truth-source matrix 标记为 Authoritative 的 API。
- [ ] 2.3 对 Jobs/Problems/Accounts 保持 navigation-only，不根据当前页推导 totals/rates。
- [ ] 2.4 验证 Dashboard mount 不自动执行 Gateway/Node connection/health probe。
- [ ] 2.5 双语、1280/1440、loading/empty/error/accessibility focused proof；独立 commit。

## 3. Stage 3A — Accounts

- [ ] 3A.1 Accounts IA / 页面骨架。
- [ ] 3A.2 迁移 Account Inventory。
- [ ] 3A.3 迁移 Account list/detail。
- [ ] 3A.4 迁移 Quality / Availability / Request History / Incidents。
- [ ] 3A.5 迁移 Account Operation entry/result presentation，保持原 command semantics。
- [ ] 3A.6 每个 slice 单独 unit/component + Browser proof + commit。

## 4. Stage 3B — Relay Nodes and Gateway auxiliary surface

- [ ] 3B.1 建立 `/nodes` Relay Nodes 页面，迁移 Node asset/health/monitoring/lifecycle/context。
- [ ] 3B.2 保留 `/assets` Gateway auxiliary management；不得把 Gateway 作为 Node 子类型。
- [ ] 3B.3 验证 Node/Gateway credential 不进入 DOM、log、Search、Dashboard。
- [ ] 3B.4 focused proof + commit。

## 5. Stage 3C — Operations

- [ ] 3C.1 建立 `/operations`，Durable Jobs 为主列表。
- [ ] 3C.2 保留 `/jobs` alias。
- [ ] 3C.3 Account Operations 仅从 Account context / command lookup 进入；不得伪造 global list。
- [ ] 3C.4 focused proof + commit。

## 6. Stage 3D — Monitoring

- [ ] 3D.1 建立 `/monitoring` read-only cross-domain diagnostic view。
- [ ] 3D.2 组合已有 health/context/inventory/quality/capacity/problems/evidence，只读跳转。
- [ ] 3D.3 Browser transport proof：Monitoring 不发送业务 mutation。
- [ ] 3D.4 focused proof + commit。

## 7. Stage 3E — Problems

- [ ] 3E.1 将现有 Problems surface 接入统一 App Shell；英文统一 `Problems`。
- [ ] 3E.2 保留 issue taxonomy/machine values，优化 filter/status/evidence presentation。
- [ ] 3E.3 focused proof + commit。

## 8. Stage 3F — Settings

- [ ] 3F.1 将 ManagementPage 既有 session/admin/password/MFA/reauth/recovery/locale 能力迁入 `/settings`。
- [ ] 3F.2 UI preference 仅 browser-local；不得增加 backend persistence。
- [ ] 3F.3 高风险操作继续使用现有 reauth/CSRF/MFA 契约。
- [ ] 3F.4 focused proof + commit。

## 9. Stage 4 — Unified Acceptance

- [ ] 9.1 `npm test`、`npm run typecheck`、`npm run build` PASS。
- [ ] 9.2 RESOURCE_PARITY / REFERENCED_KEY_COMPLETENESS / TRANSLATION_SOURCE_AUDIT PASS。
- [ ] 9.3 ZH_CN_PC_BROWSER / EN_PC_BROWSER / LIVE_LOCALE_SWITCH PASS。
- [ ] 9.4 `1280×720`、`1440×900` semantic/layout invariants PASS。
- [ ] 9.5 ZH_CN_NAVIGATION_ENGLISH_LEAK = ZERO。
- [ ] 9.6 ZH_CN_UI_UNINTENDED_ENGLISH_LEAK = ZERO（约定例外除外）。
- [ ] 9.7 RELEVANT_BROWSER_E2E PASS，所有交互 locator 使用 `getByTestId()`。
- [ ] 9.8 GENERATED_DRIFT = NONE。
- [ ] 9.9 `make test build` 完整回归 PASS。

## 10. Stage 5 — Final Independent Review / Closeout

- [ ] 10.1 独立检查 IA、truth-source、业务/安全边界、跨 slice presentation consistency。
- [ ] 10.2 确认 Backend/API/Migration/Gateway/Node contract change = NONE。
- [ ] 10.3 reconcile durable decisions 与 acceptance evidence。
- [ ] 10.4 OpenSpec strict validation、clean worktree、final closeout。
