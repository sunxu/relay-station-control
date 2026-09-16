# AGENTS.md

Relay Station Control 是单环境管理与可观测服务，采用 Go 1.27、React 19 和 PostgreSQL 18。Control 不在请求数据面中。

## 真相源

- 系统边界与阶段验收：`../ops/docs/RELAY_STATION_SYSTEM_DESIGN_CN.md` v1.8 / R4.7
- 架构决策：`../ops/docs/adr/0001-control-technology-stack.md`、`../ops/docs/adr/0002-use-sub2api-native-downstream-scheduling.md`
- 行为与契约变更：当前已批准的 OpenSpec change
- API：`api/openapi.yaml`
- 数据库：`migrations/` 中不可变的 forward Goose migrations
- OpenSpec 规则：`openspec/config.yaml`

不要手工修改生成的 Go 或 TypeScript 客户端。API、Migration 或 sqlc 变更后运行 `make generate`。

## 工作方式

- 行为或契约变更先创建或更新 OpenSpec change；实现不得超出已批准任务。
- 优先复用现有模块、Store adapter 和受控数据库函数，保持改动小且可独立评审。
- 使用 Conventional Commits。
- 不修改无关文件，不提交 Secret、真实账号数据、本地运行数据或未脱敏外部响应。

## 验证

纯文档或规则变更检查内容、引用和 diff；涉及可执行命令时核对命令。局部开发先运行受影响模块的检查；行为或契约变更交付前完成完整验证：

```bash
make test build
```

同一次 make 调用会共享 generate 依赖，无需先单独运行 make generate。不要为纯文档变更强制生成客户端或编译整个项目。
前端 npm run build 自带 generate:api；共享 Make 依赖不代表前端生成只执行一次。

数据库或状态机变更还需运行对应的 `deploy/acceptance/` 验收。使用 README 中的隔离开发数据库和受限 runtime role；产品进程不得持有 migration owner 凭据。

## Playwright E2E Locator Policy

This policy applies to all current and future phases.

### Interaction locators

All Playwright E2E locators used to perform user interactions MUST use `getByTestId()`.

This includes, but is not limited to:

- buttons
- links and actions
- menus
- tabs
- dialogs
- form controls
- selects
- select options
- interactive table/list rows
- pagination controls
- confirmation/cancel actions

Do not use the following for interaction locators:

- `getByText(...)`
- `getByRole(...)`
- `getByTitle(...)`
- CSS selectors
- XPath
- `.first()`
- `.last()`
- `.nth(...)`
- DOM-order-dependent selectors

Do not use `force: true`, large arbitrary `waitForTimeout(...)`, or increased retry counts to hide locator or synchronization problems.

### Assertions

Semantic locators such as `getByText()` or `getByRole()` may be used for read-only assertions when the visible content itself is part of the behavior being verified.

They MUST NOT be used to trigger an interaction.

Prefer exact and scoped assertions where practical.

### Test IDs

`data-testid` values MUST represent stable product/domain semantics.

Good examples:

- `account-upload-new`
- `account-remove-confirm`
- `relay-node-selector`
- `relay-node-option-<node-id>`
- `operation-lifecycle-override-<command-id>`

Bad examples:

- `button-1`
- `second-row`
- `dropdown-option-2`
- `left-panel-item-3`
- IDs derived from DOM position or visual layout

Test IDs must not depend on:

- translated display text
- DOM order
- CSS classes
- framework-internal DOM structure
- Ant Design implementation details

### Production testability changes

Adding stable `data-testid` attributes to production frontend components is allowed and is classified as a testability-only change.

Such changes MUST NOT alter:

- business semantics
- API contracts
- authentication/authorization
- backend behavior
- state transitions
- workflow behavior

E2E-only product backdoors or authentication bypasses are forbidden.

### Keyboard/accessibility coverage

Stable `getByTestId()` locators do not replace keyboard or accessibility acceptance.

If a test is intended to verify keyboard interaction, it must continue to perform the actual keyboard interaction using `focus()`, `press()`, or equivalent APIs.

Do not replace keyboard acceptance with mouse clicks merely to make the test stable.

### Synchronization

Prefer deterministic synchronization:

- `waitForResponse`
- `waitForURL`
- locator assertions
- bounded backend/database/native conditions

Do not use arbitrary sleeps or blind retries to hide races.

See `docs/testing/PLAYWRIGHT_E2E_POLICY.md` for the complete policy.

## Acceptance & E2E Rules

- Follow `docs/testing/ACCEPTANCE_E2E_POLICY.md` for the detailed workflow.
- Prove behavior at the lowest owning layer; keep Browser E2E focused on real user paths and unique cross-layer evidence.
- Check the frozen contract before asserting or waiting for a state. Keep execution truth separate from Inventory observation.
- Build and verify the production-like stack and runtime/artifact provenance before debugging behavior.
- For artifact or identity handoffs, distinguish mutable references from immutable identities; resolve references once, pass the immutable identity downstream, and verify execution consumed that identity.
- Review check-then-use handoffs for TOCTOU drift; when identity drift could affect correctness or reproducibility, prove immutable execution or fail-closed behavior with a negative handoff test.
- Diagnose failures layer-by-layer, classify them before changing production code, and stop at the earliest blocker.
- Keep waits bounded (normally no more than 30 seconds per async business layer); never hide failures with sleeps, blind retries, or timeout increases.
- Keep corrective rounds narrow and reuse proven harness infrastructure, real observers, real mutation evidence, and one shared secret scanner.
- Before a multi-case Browser suite, freeze every fixture's initial state, owner/setup, cross-case dependencies, and Inventory dependency. Cross-case mutable business dependencies are forbidden.
- Every focused case must pass independently before the suite; run 1 must pass before 3/3 repeatability.
- Do not casually modify pinned upstream/runtime code or re-freeze artifacts for unrelated findings; compare baselines before calling a failure a regression.

## 关键边界

- PostgreSQL 是 Control 持久状态的唯一真相；不得用进程内状态伪装持久成功。
- Control 只观察、关联、快照、分析、告警和建议；不得进入请求数据面、调度请求、修改 Gateway Account/Group 或路由、修改 CLIProxyAPI 凭证状态、镜像 Gateway runtime scheduler truth 或自动修复重复归属。
- Gateway 是一个 Sub2API deployment，Relay Node 是一个 CLIProxyAPI deployment；必须保留两者原生 routing/scheduling、Provider/Account selection、retry 和 cooldown 边界。
- Control 不得持久化或暴露上游凭据、Node Management Key、Gateway 管理凭据或原始响应正文。
- 邮箱是普通业务身份，不是 Secret；完整值可按批准契约用于 authenticated API/UI、数据库、审计、受控业务日志与 DingTalk 消息。Prometheus/Alertmanager labels 不得使用 raw email/account_key 等高基数字段；确需指标稳定身份时使用环境隔离的不可逆 HMAC account_id。credentials、token、Management Key、webhook URL/query、signing secret、password 与 raw upstream response/body 继续严格保护，不得泄漏。
- 默认关闭的能力必须保持关闭，除非其 Runbook 明确记录了已获批准的启用步骤。
- 生产禁止 destructive migration down；回滚应停止新行为并保留 forward schema 和审计证据。
