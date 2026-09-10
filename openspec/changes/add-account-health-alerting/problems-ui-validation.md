# Problems UI implementation validation

## Scope and baseline

本记录仅覆盖独立 Problems UI Slice；不是正式 Implementation Review 批准，也不是 Phase 5 backend / DingTalk / deployment-state Runtime Acceptance。

- Control baseline：`ac21b99c2b70fd863fdf37ed492a89858352a54f`，`feat(phase5): add dingtalk delivery executor`。
- 独立 worktree：`/Volumes/DevRAM/phase5-problems-ui`；branch：`phase5-problems-ui`。
- 主 Slice D worktree 未被本任务读取实现、修改、reset、rebase、merge 或 cherry-pick。
- Ops / Gateway / CLIProxyAPI 不修改；本 Slice 沿用用户给定的 `dd3041f916dc16578f21790e71e49c3f751decda` / `6b045698e6e5e62e35dbd103abf20c1407f8a0bb` / `273d624c70f6eb8bdd7b049df396c306acd3f8d0` baseline，不将这些值冒充本次重新部署或验收证据。
- Problems UI Preflight：GO；本 Slice architecture amendment：NO；backend changes：NONE。
- 本记录为独立 Slice 专属 evidence，不修改 planning-validation、design、active specs 或 tasks.md。

## Implementation

`/problems` 是唯一新增 authenticated route，使用 lazy ProblemsPage；管理控制台入口放在 Node Topology 旁。不新增 route alias、fresh reauthentication、permission 或 mutation 入口。

新增 generated DTO alias、API wrapper 与可取消的 query hook，transport 只调用既有 `queryProblemAccounts()`：POST、same-origin、no-store、CSRF；无自动重试、无缓存保留。新请求、reset 与 unmount 取消已有请求，mutate 入口建立 controller，覆盖同 tick reset/unmount；迟到响应不能替换新结果或恢复 reset 后数据。

进入页面自动首查 `{ limit: 25 }`。Provider/Node/Severity/Reason/Email 过滤和 25/50/100 page size 复用后端契约；空值省略，Node trim，Email trim/lowercase。每次 filter 变更清除结果与 cursor history，page size 变更自动首查。Next/Previous 只维护 opaque cursor history；Retry 保持当前 filters/cursor。

表格仅 8 个逻辑列，行 identity 为 `(instance_id, account_key)`；同账号跨 Node 保持多行，同一行保留全部 server issues。items 与 issues 不做前端排序；不解码 cursor、不全量拉取、不发 Node/account detail 或 N+1 请求。

Availability 缺失显示 `—`；Token 只显示服务端 VALID/INVALID/UNKNOWN，缺失显示 `—`；使用 `Expected Valid Until`，全部时间复用系统时区 `formatDateTime()`。confirmed issue 行不依赖 diagnostics 是否完整。完整邮箱可显示、选择，不 mask/HMAC。

400、403 使用约定文案，503/network/unexpected error 显示 unavailable 与显式 Retry。401 取消请求、清除结果和筛选/分页状态，再调用既有 `auth.clearSession()`，页面返回登录且旧邮箱/issue 消失。

## Validation

所有构建/测试命令使用工作区要求的 GO111MODULE、GOPROXY、TMPDIR、GOCACHE、GOTMPDIR、npm_config_cache、XDG_CACHE_HOME；Node 使用本机 Node 24。未运行完整 PostgreSQL suite、迁移、部署态 Runtime Acceptance 或真实 DingTalk。

| Check | Result / evidence |
|---|---|
| problem-accounts API tests | PASS，9 tests；真实 generated client + mocked fetch，POST/CSRF/same-origin/no-store、规范化/省略、400/401/403/503、network rejection |
| problem-accounts hook tests | PASS，8 tests；新旧请求、reset/unmount、同 tick 竞态、迟到响应、显式禁用 retry |
| ProblemsView tests | PASS，18 tests；行 key/多 issue/四类 issue、缺失诊断、三种 Token、server order、filters/page-size/cursor、pending、400/401/403/503/network、只读 DOM |
| App.problems route tests | PASS，5 tests；lazy、直接访问、管理入口、/problem 与 /probs 不作为 alias、popstate |
| Frontend full suite | PASS，26 files / 173 tests；包含 existing App/Auth/Jobs/Topology regressions |
| npm run typecheck | PASS |
| npm run build | PASS；Problems 独立 chunk，generated API 无 diff |
| make generate | PASS；Go/TypeScript generated contracts 无 diff |
| make test build | **FAIL — committed baseline tools test mismatch**，见下节；未完成整个 make test，不宣称 backend tests PASS |
| make build（单独） | PASS，exit 0；Go build 有宿主 module stat-cache 写入权限 warning，但成功构建二进制，未更改宿主权限 |
| Browser E2E | PASS，1 Playwright 主路径；真实 Chrome、独立 Vite、合成 API fixtures；login → Management → Problems → default query → multi-issue/multi-node → email filter → Next/Previous → 401 返回 login；Problems 仅 query 请求，无 N+1 |
| Browser layout | PASS，desktop 与 390px viewport 无 document 横向溢出，表格自身 horizontal scroll；主 Agent 检查两张合成 fixture screenshot |
| OpenSpec current strict / all strict | PASS；all 20/20 |
| git diff --check | PASS |
| Scope guard | PASS；backend Go、migrations、OpenAPI、generated client、tasks、其它仓库均未修改，无 mutation API import/call、client sorting、AccountDetailsDrawer 或额外 detail 请求 |

API 的 error-shape fallback 针对可解析 JSON 的非标准 ErrorResponse；generated JSON parser 的异常沿用 unexpected/unavailable 路径，不声称可从非法 JSON 中恢复 HTTP status。

### Baseline failure

`tools/openapi_contract_test.go:154` 的 `TestOpenAPIContainsAuthenticationFoundationOperations` 仍断言 44 个 operations；已提交 OpenAPI 有 45 个，包括 `POST /api/problem-accounts/query`。`git diff --exit-code HEAD -- tools/openapi_contract_test.go api/openapi.yaml` 通过，证实这两份输入与指定 baseline 完全一致。

本问题不属于 UI 实现，不改 Go test 或 OpenAPI，不把 `make test build` 改写为 PASS。该 make 调用在 tools tests 阶段退出，随后单独运行前端全量 tests/typecheck/build 与 `make build`。

### Browser harness corrections

首次 Chrome 在沙箱内 SIGABRT，升级到获准的沙箱外执行后正常启动。随后修正 test-only fixture：AntD 两字中文按钮的可访问名称空格、现有管理员接口实际路径 `/api/admins`、邮箱 exact locator 避免同时匹配 account_key。最终测试通过；未为 fixture 修正生产 auth 或新增故障 endpoint。

临时日志、Playwright output、截图均在 `/Volumes/DevRAM/tmp/problems-ui-*`，不纳入仓库。不存在真实账号/Secret fixture。

## Task accounting and readiness

Problems UI implementation complete：YES。

Tasks potentially closable after merge + formal Implementation Review PASS：

- **5.1**：本 Slice 完成全局 Problems 列表、过滤/分页、多 issue 同行，服从服务端 severity/Since 排序。
- **5.3**：本 Slice 提供 Problems 前端对应证据：完整邮箱、UNKNOWN 与 unavailable 区分、diagnostics 缺失仍保留 confirmed rows、按 server current-membership projection 显示、无 N+1。领域 membership/恢复正确性仍由既有 backend 契约与对应 evidence 承担，不声称本次重验领域 lifecycle。

**5.2 不可据此关闭**：现有 Account Quality/Inventory detail 扩展不在本次 global Problems UI scope；本页显示 Token 不是完成现有 detail 改动。

tasks.md 未修改；独立 worktree checkbox 仍保留 baseline 24/50，不能代表并行 Slice D 的当前进度。

限定 UI self-review：P0=0 / P1=0 / P2=0。Problems UI readiness for Implementation Review：READY；正式 Review 尚待进行。仓库全量检查仍存在上述基线 FAIL，不能据此宣称整个 Phase 5 验收通过或直接发布。

Commit：NONE；Push：NONE；merge/cherry-pick 到主 worktree：NONE。完成此 Slice 后 STOP。

## Formal review and integrated regression — 2026-09-11

以上内容保留原始 `ac21b99c2b70fd863fdf37ed492a89858352a54f` baseline 的实施和验证历史，不将其测试改写为最新基线的结果。其后用户已正式确认 Problems UI Implementation Review PASS，P0/P1/P2=0/0/0；本节记录批准后的集成，不替代新的 integrated review 或 Runtime Acceptance。

- Problems UI commit：`d3e3fd3552d88f8a5e477624b95f093d8962e5c9`，`feat(phase5): add problems read-only UI`。
- Runtime work 已正式 Implementation Review PASS 并提交：`f8eaf4757128ef105b4a67ba702e5e7fa9dd5275`，`test(phase5): validate dingtalk runtime recovery`。
- Integrated baseline：`f8eaf4757128ef105b4a67ba702e5e7fa9dd5275`。
- Integration：从 UI 原始 baseline 到 UI commit 导出纯 UI/evidence binary diff；`git apply --3way --check` 与 `git apply --3way` 均 PASS，无冲突，无 cherry-pick/merge/覆盖 fallback。Git 对新增文件显示 direct application 是该命令自身处理，不是人工替换集成方式。
- Patch：`/tmp/phase5-problems-ui.patch`。patch 不包含 tasks/backend/migration/OpenAPI/generated client/Runtime tests；最新主线仅额外将 5.1、5.3 勾选，5.2 保持 OPEN。实际计数 **38/50**，保留 Runtime Work 的已完成任务。

以下均在 integrated tree 重新执行，所有测试使用合成 fixture，不发送真实 DingTalk：

| Integrated check | Result |
|---|---|
| Problems API wrapper / hook / ProblemsView / App.problems | PASS，4 files / 40 tests |
| Problems backend HTTP/API | PASS，显式本地测试 PostgreSQL、隔离数据库，`TestProblemAccountsPOSTHTTPContracts` 及 3 subtests；非默认 skip |
| Frontend full suite | PASS，26 files / 173 tests，包含 App/Auth/Jobs/Topology |
| Typecheck / frontend build | PASS |
| make generate | PASS；无 generated contract diff |
| make test build | PASS，exit 0；tools OpenAPI tests 通过，未出现原 UI baseline 的 44/45 mismatch；未修改 tools test 或 OpenAPI |
| OpenSpec current / all strict | PASS，20/20 |
| Browser E2E | PASS，`CONTROL_E2E_BROWSER_CHANNEL=chromium`，1 test / 4.0s；独立 Vite `127.0.0.1:18123 --strictPort`，真实 Chromium + 合成 API fixtures，含 desktop/mobile、Management→Problems、过滤、Next/Previous、401 清空并返回 login |
| Markdown references / git diff --check | PASS，7 个 local references；暂存与工作树 whitespace 均通过 |
| Scope | backend/runtime/migrations/OpenAPI/generated client diff NONE；UI 只使用现有 read query，无 mutation/N+1/client sorting |

完整 `make test build` 未配置数据库 URL，不能据此将 full-store PostgreSQL suite 或部署态验收标为 PASS；上表 Problems API 单独显式配置测试 DB 并通过。既有 jsdom pseudo-element warning 和 Go module stat-cache 写权限 warning 未导致命令失败，未为此更改宿主或无关代码。

日志：`/private/tmp/phase5-integrated-focused.log`、`phase5-integrated-api-postgres.log`、`phase5-integrated-generate.log`、`phase5-integrated-make.log`。原历史 baseline failure 章节保留，仅本次 integrated make 结果为 PASS。

浏览器执行环境修正：先前一次 E2E 误指向既有 18080 部署，因旧页面没有 Problems 按钮而超时；不属于 integrated UI 验证结果。改为独立 Vite 18123 后，按用户指示使用 chromium；下载项目匹配的 revision 1234 到 `/Volumes/DevRAM/playwright-browsers`（未改依赖），沙箱启动 SIGABRT 后经授权在沙箱外运行，最终 PASS。未修改测试断言或产品代码。最终日志 `/private/tmp/phase5-integrated-e2e.log`，截图位于 `/Volumes/DevRAM/tmp/phase5-integrated-ui-e2e`。路由仅 `/problems`，无带空格 alias；表格直接使用服务端顺序，Problems 数据仅由既有 query endpoint 获取。

两份仓库外 Runtime Acceptance 工作底稿已只刷新 readiness/status；Matrix 的 86 条 case rows 与刷新前逐行一致。Problems UI feature gap RESOLVED / INTEGRATED，deployment process acceptance OPEN、real DingTalk NOT RUN、Runtime Acceptance NOT STARTED。

Architecture Review：PASS；Implementation：IN PROGRESS；Runtime Acceptance：NOT STARTED。未实施 4.6/4.7，不运行正式 86-case acceptance，不发送真实消息。Integrated UI commit：NONE；Push：NONE。
