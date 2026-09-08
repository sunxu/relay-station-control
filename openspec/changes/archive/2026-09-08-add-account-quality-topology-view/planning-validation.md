# Planning and validation

## Baseline

2026-09-08 main `c2e3cfa`，开始前git status --short为空。规划strict通过后本地commit `a7423da`，随后实施。既有Account Quality八统计字段、两窗口与NULL语义由canonical spec及migration00020确认。旧change证据在 `../archive/2026-09-08-add-account-request-quality-monitoring/final-release-validation.md`，不替代本change final验证。

只读采集链baseline命令 `go test ./internal/requestquality ./internal/drivers/cliproxyapi -count=1` PASS（0.488s/3.238s）。与c2e3cfa比较，`internal/requestquality`、`internal/drivers`、migration00020、collector wiring均无diff；未修改CLIProxy或其它仓库。

## Implementation and acceptance matrix

路径以control仓库为根。PG命令使用README隔离开发数据库的 `CONTROL_DATABASE_TEST_URL` / `CONTROL_RUNTIME_DATABASE_TEST_URL`（host55432），各测试自行创建临时数据库、应用migration并用restricted runtime查询；没有触碰本地部署数据库55434。测试明确实际执行，无SKIP冒充验收。

| ID | Implementation / fixture / assertion | Command / result |
|---|---|---|
| Q1 | `migrations/00021_node_account_quality_query_access.sql`；`TestNodeAccountQualityAcceptancePostgres/exact_classification_counts_nulls_and_account_enumeration`，100/95/94/80/79个成功÷100，逐项断言good/good/degraded/degraded/bad、count/rate/p95=95.05与last字段 | store专项 PASS |
| Q2 | 同测试Inventory有unknown账号无event，count全部0、五个nullable字段全NULL、unknown；账号集合恰好7，不丢零请求行 | store专项 PASS |
| Q3 | 同fixture有NULL身份和event-only账号；7条Inventory行，已有账号count精确100，window账号1h精确2请求/1失败，不制造额外行；Inventory present仍为7 | store专项 PASS |
| Q4 | `TestNodeAccountQualityCompositionPostgres`，110账号，bad目标在第110位且limit1，只返回目标且HasMore=false；unknown100+9；acceptance逐种quality精确计数与provider正反过滤 | store专项 PASS |
| Q5 | acceptance用limit2遍历7条，逐项account_key严格递增无重复；HTTP cursor完整round-trip及Node/window/provider/quality错配400 | store/HTTP专项 PASS |
| Q6 | `internal/api/account_quality_handlers.go`；`TestAccountQualityHTTPReadContracts`真实auth fixture，401/403不读、非法参数400、notfound404、DB/unsupported/nil reader503、恢复200、GET no-store/requestID/5sdeadline、POST405不读、double精度不丢、unknown字段显式null | HTTP专项 PASS |
| Q7 | `TestNodeAccountQualityACLAndRollbackPostgres`；runtime EXECUTE成功，三表direct SELECT为42501；catalog断言definer/STABLE/owner/search_path/PUBLIC revoke；Down缺function报错、所有表/列/数据指纹不变、旧函数定义不变；Up恢复原unknown账号 | store专项 PASS |
| U1 | `web/src/pages/TopologyView.test.tsx`新增独立loading/empty/unavailable恢复/unknown行；Unknown行精确四个—；success百分比/P95/class/UTC时间断言 | 前端专项19 tests；full83 tests PASS |
| U2 | 同测试window/provider/quality切换、next cursor参数、重置cursor、旧Node延迟结果隔离、401清会话、Provider来源错误、Account Quality区无mutation按钮；`TopologyView.tsx`select/popstate同时重置筛选 | 前端专项及full PASS |
| P1 | `TestNodeAccountQualityPerformancePostgres`，100 Inventory账号×100event=10,000；六种组合断言100或10精确行数、每账号100请求、最终无next、pgx QueryTracer精确1 query，<1s | 122–128ms，详下表 |
| V1 | targeted Go/PG、frontend unit/typecheck/build | PASS，详命令 |
| V2 | make test build、relevant race、change/all strict、diff check | make/race PASS；change strict PASS，all strict 18/18 PASS，diff check PASS |

## Query and performance evidence

实现只有一个新的safe query-access function；无persistence migration。账号来源完全复用 `control_query_current_account_inventory_v1`，按101分块；每账号统计调用既有 `control_query_account_request_quality_v1`，不复制aggregate SQL。provider下推，quality过滤先于limit+1；响应最大100，默认25。单次Go→DB往返，内部100次quality函数评估由fixture与扫描逻辑推导，不冒充单次set-based统计扫描。没有缓存/Redis/rollup。

普通PG最终专项（非race）实测：

| Window | Filter | Rows | DB queries | Latency |
|---|---|---:|---:|---:|
| 15m | All | 100 | 1 | 122.32ms |
| 1h | All | 100 | 1 | 122.99ms |
| 15m | provider=openai | 100 | 1 | 122.85ms |
| 1h | provider=openai | 100 | 1 | 127.96ms |
| 15m | quality=bad | 10 | 1 | 122.11ms |
| 1h | quality=bad | 10 | 1 | 124.04ms |

这些是本地合成fixture、单次场景观测，不宣称生产p95或任意Node规模SLA。稀疏过滤可能扫描更多账号；5s request context限制查询时间，失败显示unavailable；跨页是实时窗口而非冻结snapshot。

## Validation commands

设置README两条测试DSN后执行（未覆盖GOCACHE/GOTMPDIR）：

```bash
go test ./internal/store -run '^TestNodeAccountQuality' -count=1 -v
go test ./internal/api -run '^TestAccountQualityHTTPReadContracts$' -count=1 -v
go test -race ./internal/store ./internal/api -run 'Test(NodeAccountQuality|AccountQualityHTTPReadContracts|TopologyHTTPReadContracts)' -count=1 -v
```

普通store专项4个顶层测试 PASS，5.117s；HTTP专项6个subtests PASS，1.482s。最初HTTP fixture遗漏必需csrf_digest，真实PG执行暴露23502；已按现有session fixture修复后通过，没有改生产契约。

```bash
cd web
npm test -- --run src/pages/TopologyView.test.tsx
npm run typecheck
npm run build
# 返回control仓库根
make test build
openspec validate add-account-quality-topology-view --type change --strict --no-interactive
openspec validate --all --strict
git diff --check
```

前端专项19 tests PASS。最终 `make test build` exit0，包含Go tests、16 frontend test files/83 tests、typecheck、Vite build与Go build；生成Go/TS通过make/OpenAPI可重复，不手改。构建有Go默认module cache的非致命`operation not permitted`提示，命令仍exit0；没有因此覆盖缓存环境变量。

本地日志：`/private/tmp/account-quality-view-source-baseline.log`、`account-quality-view-store-final.log`、`account-quality-view-http-final.log`、`account-quality-view-make-final.log`、`account-quality-view-race-final.log`；仓库此文保留关键断言与结果，临时日志不是唯一证据。

## Self review

| # | 检查 | 结果与证据 |
|---|---|---|
| 1 | 单账号为主视角 | 是，Node detail Account Quality每行account_key |
| 2 | 无请求仍显示 | 是，Inventory集合+Q2精确unknown/null断言 |
| 3 | unknown与unavailable | 分开，成功无事件与503/重试不同UI |
| 4 | N+1 | 无Go/HTTP网络N+1；内部逐账号复用函数，P1披露成本 |
| 5 | unresolved污染 | 否，Q3精确计数且无fake row |
| 6 | classification位置 | 仅readonly function/API/UI，不持久化 |
| 7 | Inventory/lifecycle变更 | 无，Q3验证present保持，既有函数未改 |
| 8 | mutation | 无，新GET only；POST405、不读 |
| 9 | request history | 无 |
| 10 | Prometheus/Grafana | 无 |
| 11 | bounded API | limit1–100/default25、5s、cursor2048 |
| 12 | deterministic分页 | account_key排序，filter后分页，cursor绑定参数；不承诺跨请求snapshot |
| 13 | UI只读 | 无action，仅读filters/page/retry；旧Inventory入口/audit不变 |
| 14 | 最小change | 一个query-access migration，无事件schema/collector/retention/taxonomy改动，source destructive-pop/no-ACK限制仍保留 |

## Delivery

本地分阶段commit：`a7423da`规划、`1408c40`API/readonly wrapper/PG和HTTP验收/generated、`303877c`Web。最后以 `docs(topology): validate account quality view` 提交此证据和runbook/tasks；不push、不deploy、不archive，等待Architecture + Implementation Final Review。每个commit前均git diff --cached --check通过；最后仅剩此三份预期文档待提交，最终git status以交付报告为准。

最终race真实PG：store PASS 6.399s，api PASS 5.034s（含既有TopologyHTTP regression）；无race报告。`openspec validate add-account-quality-topology-view --type change --strict --no-interactive` PASS；`openspec validate --all --strict` 为18/18 PASS。最终13/13 tasks完成，不等于已经Final Review批准或归档。
