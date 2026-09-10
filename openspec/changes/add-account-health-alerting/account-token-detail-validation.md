# Task 5.2 Account Token detail validation

## Scope and baseline

- Baseline / HEAD：`dcca00b6144e873c32adea6ee5f6365e08c5a3bd`。
- 独立 worktree：`/Volumes/DevRAM/phase5-account-token-detail`；branch：`phase5-account-token-detail`。
- 只实施现有 Account Quality / Inventory detail 的 Token diagnostics，不创建新页面、Token timeline 或 API。
- 不修改主工作树、其它仓库、tasks.md、design/spec/planning-validation；本文件仅为 Task 5.2 专属实施证据。
- 本记录不是正式 Implementation Review PASS，不是 backend / DingTalk / deployment-state Runtime Acceptance。

## Contract and implementation

已提交 generated `NodeAccountQualityItem` 提供可选的 `token_state`（VALID/INVALID/UNKNOWN）与可选 nullable `expected_valid_until`。`AccountQualityItem` 已 alias generated DTO，现有 Account Quality v4 / account list transport 直接返回该 projection；无需 backend/OpenAPI 变更。

现有 `TopologyView` 将 quality response 显式转换为 `AccountListRow`，之前没有透传两个 Token 字段。此次仅：

1. 在既有 `AccountListRow` adapter 增加两个可选字段，Token enum 直接复用 generated 类型。
2. 在既有 row mapping 透传 `item.token_state` / `item.expected_valid_until`。
3. 在 `AccountDetailsDrawer` 的“采集信息”Descriptions 增加 `Token Health` 与 `Expected Valid Until`。

Tag 颜色与 Problems 一致：VALID green、INVALID red、UNKNOWN default。缺省字段显示 `—`，不隐藏账号、不制造新状态。时间直接调用 `web/src/time.ts` 的 `formatDateTime()`；不以浏览器时钟、refresh、Availability 或 request history 重算状态，不计算 3599 秒。

原请求历史与可用性事件 Tabs、history hook、查询与排序保持原实现，没有 Token occurrence/history domain。切换账号时直接使用新 row 的字段；保留现有 account history 的 keyed lifecycle。Incident 打开的账号若不在当前列表页，继续显示既有“采集信息未加载”，不为 Token 增加 detail lookup。

没有新增请求、凭据读取、mutation、token refresh 或 N+1。生产 diff 仅 3 个前端文件的 7 行 additive 增量，无新 production 文件。

## Validation

构建/测试使用工作区规定的 GO111MODULE、GOPROXY、TMPDIR、GOCACHE、GOTMPDIR、npm_config_cache、XDG_CACHE_HOME 与 Node 24。

| Check | Result |
|---|---|
| AccountDetailsDrawer / AccountList / TopologyView focused | PASS，3 files / 47 tests |
| Related Inventory API/transport、request/incident history、App inventory focused | PASS，6 files / 34 tests |
| Frontend full `npm test` | PASS，26 files / 179 tests |
| `npm run typecheck` | PASS |
| `npm run build` | PASS |
| `make generate` | PASS；无 generated diff，无 OpenAPI diff |
| `make test build` | **FAIL — committed baseline OpenAPI operation-count mismatch**，见下节；不声称完整 backend suite PASS |
| OpenSpec current strict | PASS |
| OpenSpec all strict | PASS，20/20 |
| `git diff --check` | PASS |
| Browser E2E | NOT RUN；现有 E2E 无 Account detail 主路径，需要补充 quality/history 等 fixtures；本次按授权以组件与 Topology 集成测试为主要证据，不新建 framework |

已验证：Token 三态；有值的 Expected Valid Until 使用系统时区 formatter；null 时间显示 `—`；旧 refresh 仍 UNKNOWN；future Expected Valid Until 不覆盖 INVALID；同一组件/client 中 A VALID → B INVALID → missing 的替换；原 Availability history 两项顺序与分页语义；Topology 实际字段透传及现有 quality 请求次数；无新增 Token 请求。

首轮全量测试有一项新测试定位错误：相同时间同时出现在历史与详情，未限定单元格。修正为 Expected Valid Until 对应单元格断言后，最终全量 179/179 通过；未放宽产品语义或改用任意匹配时间。

### Known baseline failure

`tools/openapi_contract_test.go:154` 的 `TestOpenAPIContainsAuthenticationFoundationOperations` 期望 44 个 operations，但 committed OpenAPI 有 45 个。`git diff --exit-code HEAD -- tools/openapi_contract_test.go api/openapi.yaml` 通过，两份文件未被本次修改。

本次不修 Go test 或 OpenAPI。`make test build` 在 tools tests 阶段退出，后续前端全量 tests/typecheck/build 已单独完成；不把此失败记作通过，不执行额外 PostgreSQL 或运行验收。

### Environment and temporary evidence

最终回归前 DevRAM 空间不足，一次日志重定向失败，测试尚未启动。仅将本 worktree 自建的 npm dependencies 迁移至 `/private/tmp/phase5-account-token-detail-deps/node_modules`，临时链接用于完成测试；不清理其它任务缓存。迁移后修正了依赖目录命名/命令工作目录，最终全量通过。收尾移除本任务的临时 symlink，不把它加入变更。

初始 generate/build/typecheck/make 日志位于 `/Volumes/DevRAM/tmp/account-token-detail-*`；最终全量及 related focused 日志位于 `/private/tmp/account-token-detail-frontend-tests-final.log` 与 `/private/tmp/account-token-detail-related-focused.log`。这些是本地临时证据，不进入仓库。

## Review readiness and stop

5.2 implementation：COMPLETE。

5.2 eligible to close：YES，待正式 Implementation Review 与 patch integration 后统一勾选；本次 tasks.md 不修改。

限定本 Slice 自查：P0=0 / P1=0 / P2=0。Readiness for Implementation Review：READY；完整 make 检查的基线失败仍需明确保留。

Backend Go / migrations / OpenAPI / generated manual changes：NONE。Problems / Jobs / DingTalk / Availability backend / Duplicate backend：NONE。

Commit：NONE；Push：NONE；patch integration / merge / rebase / cherry-pick：NONE。Runtime Acceptance：NOT STARTED。不实施 4.6/4.7，完成本 Slice 后 STOP。

## Final focused validation — 2026-09-11

Implementation baseline：`dcca00b6144e873c32adea6ee5f6365e08c5a3bd`。
Future integration target：`8b91770df7c7399acfd79b4a7cfb0e585508c2f8`，仅为后续计划；本轮未集成、未在该 SHA 验证。

本轮保留原 implementation，只补强测试：账号切换后旧 expected time 消失；同 Node 的 A 历史迟到响应不能覆盖 B；detail 不展示额外 credential material，不新增 mutation controls。复用现有 Topology Playwright 文件增加独立 detail fixture，不改旧 E2E 流程。生产变更仍仅三个前端文件、七行增量。

| Final check | Result |
|---|---|
| Detail/List/Topology + Inventory/API/history focused | PASS，9 files / 83 tests |
| Frontend full | PASS，26 files / 181 tests |
| Typecheck | PASS |
| Frontend build | PASS |
| make generate | PASS，tracked generated diff NONE |
| make test build | KNOWN BASELINE 44/45：tools test expected 44 / actual 45；tools/openapi_contract_test.go 与 api/openapi.yaml 均未修改；后续 make build 未执行，frontend build 已单独通过 |
| OpenSpec current strict | PASS |
| OpenSpec all strict | PASS，20/20 |
| Browser E2E | NOT RUN / environment blocked：以 CONTROL_E2E_BROWSER_CHANNEL=chromium 尝试启动，但缺少 Playwright chromium-1234 binary，测试步骤未执行；新增 E2E 不声称 PASS |

本轮 npm dependencies 安装在该 DevRAM worktree，缓存、临时目录和日志均沿用 `/Volumes/DevRAM`；不复用上文历史临时 symlink。日志为 `/Volumes/DevRAM/tmp/token-detail-{focused-final,full-current,typecheck-current,build-current,generate-current,make-current}.log`。Vite 测试进程已停止。

Token VALID/INVALID/UNKNOWN、null、UNKNOWN + old refresh、INVALID + future expected validity 均由组件测试证明；状态直接来自 server，系统时间格式复用 formatDateTime。既有 occurrence history 的顺序/分页保留，无 token history、credential read、额外 token request 或 N+1。OpenSpec 实施流程仅用于核对冻结范围；按本轮授权不勾选任务、不进入其它 slice。

Task 5.2 implementation COMPLETE；eligible to close YES，仍须正式 Implementation Review 和后续主线集成；tasks.md 未修改。限定本 slice self-review：P0=0 / P1=0 / P2=0，不代表正式 Review 结论。可选 browser evidence 仍未取得。

Final review artifact：`/Volumes/DevRAM/phase5-account-token-detail-final-review.diff`，包含全部 tracked UI/tests diff 与本新增 evidence。无 backend、migration、OpenAPI、generated client、Jobs、Problems、DingTalk 或其它仓库变更。Commit/PUSH/integration NONE；Runtime Acceptance NOT STARTED。

## Integrated validation — 2026-09-11

Source Implementation Review：PASS，用户正式结论 P0/P1/P2=0/0/0。
Source commit：`d0f420650bd9c60df98b766d5c5235971e64553b`；source baseline 保持 `dcca00b6144e873c32adea6ee5f6365e08c5a3bd`。
Integrated baseline：`8b91770df7c7399acfd79b4a7cfb0e585508c2f8`。
Integration：`git apply --3way`；Patch check PASS；Patch apply PASS；Conflicts NONE。七个 source 文件精确集成；六个 UI/test 文件与 source commit 内容相同，之后只追加本证据和主线 5.2 checkbox。

| Integrated check | Result |
|---|---|
| Focused detail/list/Topology/Inventory/API/history | PASS，9 files / 83 tests |
| Frontend full | PASS，28 files / 184 tests |
| Typecheck / frontend build | PASS / PASS |
| Playwright focused spec discovery | PASS，1 file / 1 test |
| Browser E2E | ENVIRONMENT BLOCKED / NON-GATING；缺少 chromium-1234 executable，未执行测试步骤 |
| make generate | PASS；generated tracked diff NONE |
| make test build | KNOWN BASELINE 44/45；tools expected 44、OpenAPI actual 45，两文件未修改；未将该结果记录为完整 make PASS |
| OpenSpec current / all strict | PASS / PASS，20/20 |
| Patch fidelity / diff check | PASS / PASS |

三态、Expected Valid Until/null、server authority、A→B stale diagnostics、迟到 A history 不覆盖 B、occurrence history、统一 formatter、无 Token 请求/N+1/mutation/credential rendering 均由上述 integrated focused suite 重新证明。生产仍只有三个 frontend 文件的只读投影增量；backend、migration、OpenAPI、generated、Jobs、Problems、DingTalk 变更 NONE。

依据正式 source Review 和本次 integrated gates，5.2 CLOSED，主线实际 Tasks 41/50；4.6/4.7 状态不变，6.x 继续 OPEN。44/45 historical evidence inconsistency OPEN；Gateway broad-test observations OPEN / UNCLASSIFIED，本轮未重跑或分类。

日志位于 `/Volumes/DevRAM/tmp/token-integrated-{focused,full,type,build,generate,make}.log`。Integrated review diff：`/Volumes/DevRAM/phase5-account-token-detail-integrated-review.diff`。Integrated tree 未提交；Push NONE；Runtime Acceptance NOT STARTED，real DingTalk NOT RUN。
