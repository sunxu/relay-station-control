# Phase 10 Stage 0 Planning Validation

## Status

```text
REQUIREMENTS_SOURCE = docs/phase10/CONTROL_WEB_UI_REQUIREMENTS_CN.md
OPEN_SPEC_CHANGE = modernize-control-management-shell

PHASE10_STAGE0 = CORRECTED FROZEN CANDIDATE
TEST_COVERAGE_CONTRACT_GAP = NONE
ARCHITECTURE_REVIEW_ROUND1 = CHANGES_REQUIRED
ARCHITECTURE_REVIEW_REVIEW_CANDIDATE = READY

BACKEND_CHANGE = NO
DATABASE_CHANGE = NO
GATEWAY_CHANGE = NO
RELAY_NODE_CHANGE = NO
```

## 1. Source reconciliation

用户提供的 v3 Requirements 中存在英文问题域命名冲突：

- 早期导航与 §21 使用 `Issues`；
- §33 后部明确冻结 `Problems`，并给出 `/problems`、ProblemsPage、ProblemsView 兼容理由。

Stage 0 按后部明确命名约定统一为：

```text
zh-CN = 问题
en = Problems
route = /problems
```

`Issues` 仅在文档中作为“禁止使用/未来独立 Issue Tracking”说明存在。

## 2. Current executable baseline review

已核对当前前端：

- `App.tsx` lazy-loads Bootstrap/Login/Activation/Management/Assets/Jobs/Topology/Problems/OneTimeMaterial；
- `AuthContext.tsx` 使用 pathname + pushState + popstate，无 React Router；
- `FrontendFoundationProvider` 已提供 i18next + Ant Design ConfigProvider；
- `foundationTheme` 当前为空，适合作为 Phase 10 theme token 入口；
- `PageShell` / `PageHeader` 已存在，可扩展；
- `LocaleSwitcher` 与 `relay-control.locale` foundation 已存在；
- Phase 8 evidence 仍含 `390×844` responsive acceptance，需要由 Phase 10 PC-only contract 正式 supersede。

## 3. IA reconciliation

七项一级 Sidebar 不包含 Gateway，但现有 Gateway management 不能丢失。

冻结：

```text
Gateway /assets = auxiliary management route
not a Sidebar primary item
not a Relay Node subtype
reachable from Command/Nav Search and real Gateway context
```

该决定不新增后端能力。

## 4. Dashboard truth-source review

第一阶段可以安全作为 Authoritative Summary 的现有来源：

- `/api/healthz`
- `/api/assets/gateways` -> `gateway_counts`
- `/api/assets/nodes` -> `node_counts`
- `/api/account-inventory/poll-capacity`

Jobs、Problems、Account Inventory、Account Quality 等现有 surface 为 paginated/query-scoped；没有足够契约支持全局 totals/rates/trends。

因此：

```text
Global Success Rate = NOT AUTHORIZED
Last Week Comparison = NOT AUTHORIZED
Global Operations Trend = NOT AUTHORIZED
Global Alerts Trend = NOT AUTHORIZED
Global Recent Account Operations = NOT AUTHORIZED
```

## 5. Routing impact

Phase 10 可扩展手写 AuthContext routing，不引入 React Router。

目标 canonical routes：

```text
/            Dashboard
/accounts    Accounts
/nodes       Relay Nodes
/operations  Operations
/monitoring  Monitoring
/problems    Problems
/settings    Settings
```

兼容 routes：

```text
/assets     Gateway auxiliary
/jobs       Operations alias
/topology   Monitoring alias during migration
```

旧 deep links 初始 rollout 不删除。

## 6. Security / truth-source impact

保持：

- Browser 只调用 Control；
- OpenAPI 是 API truth；
- PostgreSQL 仍为 Control durable truth；
- UI aggregate 不成为 execution truth；
- Authentication / Authorization / MFA / CSRF / Reauthentication 不变；
- Secret 不进入 Search/Dashboard/DOM/log；
- Gateway / Relay Node 原生职责不变。

## 7. Test contract

`test-contract-coverage-review.md` 已为所有 frozen MUST/MUST NOT 指定 owning layer。

```text
TEST_COVERAGE_CONTRACT_GAP = NONE
```

若 Architecture Review 增加 backend/search/aggregate/preference contract，必须重新执行 TCCR。

## 8. Implementation stages

Stage 1 Foundation -> Stage 2 Dashboard -> Stage 3 domain slices -> Stage 4 unified acceptance -> Stage 5 independent closeout。

Accounts 明确细分，避免一次性重写最大业务 surface。

## 9. Remaining executable validation

本交付是 planning artifact bundle，未在用户本地 Git worktree 执行 OpenSpec CLI。提交前必须在真实仓库运行：

```bash
openspec validate modernize-control-management-shell --strict
git diff --check
```

纯 planning 文档无需因此运行完整 `make test build`；Stage 1 implementation 前需取得独立 Architecture Review PASS。

## 10. Readiness

```text
Requirements = READY
Information Architecture = READY
Dashboard truth-source = READY
Test Contract Coverage Review = PASS
Architecture Review readiness = READY
Implementation = NOT STARTED
```

## 11. Architecture Review Round 1 corrective reconciliation

Round 1 findings：

```text
P0 = 0
P1 = 2
P2 = 2
DISPOSITION = CHANGES_REQUIRED
```

P1-1：canonical `asset-registry` 仍要求 Environment / Gateway / Node / Driver / Policy 在单一 Asset Registry 中呈现，并明确“不新增第二套页面”；Phase 10 `/nodes` 会形成 contract 冲突。已新增 `specs/asset-registry/spec.md` 的 `MODIFIED Requirements`，冻结 `/assets` = Environment/Gateway/Driver/Policy auxiliary，`/nodes` = sole executable Node lifecycle / monitoring owner，底层 API/安全/事务语义不变。

P1-2：canonical `node-centric-topology-ui` 仍要求 `390px` 窄屏可辨识；与 Phase 10 PC-only contract 冲突。已新增 `specs/node-centric-topology-ui/spec.md` 的 `MODIFIED Requirements`，正式 supersede 390px release gate，并保留 keyboard/accessibility/read-only truth semantics；`/topology` 作为 `/monitoring` compatibility alias。

P2-1：新 canonical routes 未明确 trailing slash normalization。已冻结 `/accounts/`、`/nodes/`、`/operations/`、`/monitoring/`、`/problems/`、`/settings/` 与无 trailing slash route 等价。

P2-2：可选 current-page entity search 未明确 identity persistence 安全边界。已冻结 Search 不得把 Secret/credential/token 或 canonical `account_key` 写入 URL/history/storage/log/metrics；无安全 deep-link identity 时只改变当前页面内存 selection。

Corrective 后仍保持：

```text
Backend API change = NO
Database/Migration change = NO
Gateway source change = NO
Relay Node source change = NO
```

上述 `asset-registry` / `node-centric-topology-ui` 为 canonical **UI presentation contract** delta，不是 backend/business capability 变更。

## 12. Architecture Re-review residual reconciliation

Round 1 corrective cross-spec scan found one additional residual canonical reference in `relay-node-management-operations`: the requirement `Node operations SHALL remain explicit and isolated` still pinned Health / Connection Test / Monitoring presentation to the historical Asset Registry.

This is the same ownership family as P1-1, but it requires its own exact-capability delta to avoid leaving a contradictory canonical requirement after archive.

Corrective adds:

```text
specs/relay-node-management-operations/spec.md
```

as `MODIFIED Requirements`:

- `/nodes` owns Node Health / Connection Test / Monitoring presentation;
- `/assets` has no duplicate executable Node operations after Stage 3B;
- `/nodes`, Dashboard and Monitoring mount/reload do not automatically probe;
- explicit Node observations/actions keep existing API / Driver.Probe / CSRF / receipt / audit / metrics / lifecycle semantics.

Cross-repository/source scan after this correction found no additional canonical spec that pins Node operation UI to Asset Registry, and no additional canonical `390px` product requirement outside `node-centric-topology-ui`.

Expected delta capabilities after final correction:

```text
control-management-shell
asset-registry
node-centric-topology-ui
relay-node-management-operations
```
