## Planning validation

### P2 disposition: /assets/ frontend route recognition

已知 P2：HTTP 层即使正确把 `/assets/` 返回 SPA shell，前端 `AuthContext.tsx` 的 pathname
matcher 仍只精确匹配 `/assets`，`/assets/` 会落入 management/default 分支，导致 authenticated
用户直接导航/reload `/assets/` 看不到 Asset Registry。

修复（planning-only，未修改任何 production 代码）：

```text
P2 /assets/ frontend route recognition = RESOLVED

/assets  -> assets
/assets/ -> assets

authenticated direct navigation / reload E2E
must assert Asset Registry rendered
```

- `specs/asset-registry-web-routing/spec.md`：在 `Asset Registry route is isolated from
  static resources` Requirement 中新增前端 route resolver 契约段落，冻结
  `/assets`/`/assets/` 都必须解析为 `assets` 路由，且不得引入新 router framework、全站
  URL rewrite subsystem 或新的 route 抽象层；新增场景
  "Authenticated direct navigation and reload render Asset Registry"，要求 E2E 断言必须
  证明最终渲染的是 Asset Registry 页面本身（稳定 UI signal，如 page heading 或
  asset-page test id），不得只断言 HTTP 200/SPA root 存在。
- `design.md`：Decision 段落补充前端 matcher 现状（只精确匹配 `/assets`）与冻结的最小
  normalization 方案；Verification 段落补充 E2E 必须验证最终 UI 而非仅 HTTP 200，并列出
  三种必须覆盖的路径（直接导航 `/assets`、直接导航 `/assets/`、`/assets/` 上 reload）。
- `tasks.md`：新增 3a（未来 implementation task：更新
  `web/src/auth/AuthContext.tsx` 的 pathname matcher/normalization）、4a（对应
  unit/integration test）、4b（对应 E2E 场景，断言最终渲染 Asset Registry）。本轮
  仅新增 task 描述，不执行、不修改 `web/src/**`。

静态资源 namespace 契约保持不变：`Vite base = /static/`、embedded static handler 服务
`/static/*`、miss 返回 404、`/assets`/`/assets/` 是 SPA route、API route 优先于 SPA
fallback、其它既有 SPA deep link 行为不变；`/assets/` 不要求 HTTP redirect 前置条件。

### Readiness

```text
Independent readiness review = PASS

P0 = 0
P1 = 0
P2 = 0

P2 /assets/ frontend route recognition = RESOLVED
Planning readiness = PASS / READY
Implementation readiness = READY
Implementation = NOT STARTED
openspec apply = NOT RUN
production code changed = false
```

最终 independent review 已确认以下规划契约；本次仅记录批准状态，不代表实现或运行验收已完成。

```text
/assets  -> assets
/assets/ -> assets

authenticated direct navigation / reload
MUST render Asset Registry

HTTP 200 alone is insufficient
management/default page MUST NOT render

/static/* contract unchanged
static miss = 404
API precedence unchanged
other approved SPA deep links unchanged
no HTTP redirect prerequisite for /assets/
```

### Implementation and Runtime Acceptance

```text
Implementation = COMPLETE
Runtime Acceptance = PASS
Static prerequisite = IMPLEMENTED / ACCEPTED
production code changed = true
completed tasks = 10 / 10
```

2026-09-12 使用仓库当前 `openspec instructions apply --change fix-control-web-static-resource-prefix --json` 执行 apply workflow；当前 OpenSpec CLI 不提供独立 `openspec apply` 子命令。实施严格限定于本 change：Vite browser-facing base 为 `/static/`，embedded handler 对 `/static/*` 命中资源并对 miss 返回 404，`/assets` 与 `/assets/` 都由前端解析为 Asset Registry，API router 保持优先。

Runtime Acceptance 使用全新独立 PostgreSQL 18、生产 linux/arm64 Control image 与真实 authenticated Playwright browser 完成。`/assets` direct、`/assets/` direct、`/assets/` reload 均通过 `assets-page` test id 和“资产注册表”heading 验证，三者均未渲染 `management-page`。Asset Registry lazy chunk 从 `/static/assets/AssetsPage-*` 成功加载；`/static/not-found.js` 返回 404 且无 SPA root；`/api/healthz` 返回 API response 且无 SPA root。Playwright 4/4 PASS，`make test build` PASS，OpenSpec archive 前 strict validation 与 diff/scope checks 见实施 evidence。
