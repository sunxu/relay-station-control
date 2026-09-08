# Planning validation

## Baseline and scope

2026-09-08 main `9e22c21`，Control工作树干净。已读canonical account-request-quality/node-centric-topology-ui、migration00020/21及Inventory00008。复用现有11个event字段和已有索引。规划先经 `openspec validate add-account-request-history --type change --strict --no-interactive` PASS，再以 `48c2dad` 提交并开始实施。

仅Control readonly function/API/UI/tests/docs；CLIProxy、collector、HTTP queue、event persistence schema、retention、failure taxonomy、Quality classification均未修改。不push、不deploy、不archive。验证使用隔离PostgreSQL测试库（本地55432），没有操作当前部署数据库55434或真实Node。

## Acceptance matrix

| ID | Implementation / fixture / exact assertion | Command and result |
|---|---|---|
| H1 | `00022_account_request_history_query_access.sql`调用既有Inventory安全函数精确provider/email/account_key gate；`TestAccountRequestHistoryPostgres`断言`a-new`唯一返回、existing-empty零条且无next、实际插入event-only后仍not-found；`TestAccountRequestHistoryReadFailuresPostgres`验证取消/Inventory capability失败不返回empty；HTTP reader故障503/not-found404与恢复 | PG/API专项PASS |
| H2 | 同store fixture含NULL account、另一account、另一Node及expired事件，只返回目标`a-new`；`BoundaryAndStableKeyset`在同一SQL DO statement内插入恰好7天、早1微秒、future，返回hash数组必须精确等于`['exact-7d']`，其它记录不能替代 | PG专项PASS |
| H3 | SQL与store外层均time DESC/hash DESC；同timestamp分页必须先`z-tie`后`a-tie`，第一页HasMore=true、末页false，时间戳相同无重复。`OpaqueHash`验证已有1..256-byte text identity不trim；性能fixture逐行验证25/next25/100的确切hash序列 | PG专项PASS |
| H4 | `TestAccountRequestHistoryHTTPReadContracts`实际PG session/auth + injectable reader；精确cursor时间/完整hash往返、Node/account错配、零time、空/NUL hash、非法/过长cursor、非法key/limit400；default25/max100；item精确6字段、NULL及空数组，无hash。401/403/POST405不调用reader，GET no-store/request-ID；404/503与恢复 | API专项PASS；403仅隔离测试库临时放宽role约束构造observer，产品migration未改 |
| H5 | `TestAccountRequestHistoryACLAndRollbackPostgres`runtime读精确`retained-history`；pg_proc验证SECURITY DEFINER/STABLE/migrator/fixed pg_catalog/PUBLIC无EXECUTE/runtime有EXECUTE；三表direct SELECT均42501。Down到21只移除新函数，函数缺失为error；Up后精确事件仍在，表/列/数据快照及旧函数/index定义前后不变 | PG专项PASS |
| U1 | `AccountRequestHistorySection.test.tsx`未选不调用、独立loading、六列success/failed/null、7天empty、503及retry recovery、404区别empty、401清会话、无mutation；成功Failure强制— | frontend专项及全套PASS |
| U2 | 同测试验证cursor下一页/首页、换账号重置、same QueryClient旧Node AbortSignal.aborted及迟到success/401隔离；`TopologyView.test.tsx`验证row.account_key打开History、真正切Node清选择且不向新Node读取旧账号 | frontend专项及全套PASS |
| P1 | `TestAccountRequestHistoryPerformancePostgres`1账号10000同timestamp事件，三页逐行精确hash断言；pgx tracer每次恰好1query；EXPLAIN验证现有索引计划，不添加index | PG专项PASS，详见下表 |
| V1 | targeted PG/API、相关race、全量Go/frontend tests/typecheck/build、generation | 下列最终命令PASS |
| V2 | change/all strict、diffcheck、runbook、分阶段本地commit；13/13任务完成，未宣称Final Review已批准 | strict PASS，diffcheck PASS |

## Reproducible validation

PG专项与race均显式提供 `CONTROL_DATABASE_TEST_URL`（测试migrator）和 `CONTROL_RUNTIME_DATABASE_TEST_URL`（受限runtime），连接本机55432，再由现有fixture创建和回收隔离数据库；未以SKIP作为通过。具体账户配置沿用项目README数据库测试约定。

```sh
go test ./internal/store ./internal/api -run 'TestAccountRequestHistory' -count=1 -v
go test -race ./internal/store ./internal/api -run 'TestAccountRequestHistory' -count=1 -v
# make在不启用全量PG集成的常规环境运行，PG证明以上面显式DSN专项为准。
make test build
openspec validate add-account-request-history --type change --strict --no-interactive
openspec validate --all --strict
git diff --check
```

`make test build`同一次调用包含generated Go/TS/sqlc、Go全包测试、frontend全套tests、typecheck、frontend build及Control binary build。另有frontend专项：

```sh
cd web
npm test -- --run src/pages/AccountRequestHistorySection.test.tsx src/pages/TopologyView.test.tsx
npm run typecheck
npm run build
```

最终非race专项store 5.925s、API 1.316s，全部PASS。最终race store 9.023s、API 4.869s，PASS且无race报告。前端全套17 files / 97 tests PASS，History专项12项、Topology21项；typecheck/build与make exit 0。OpenSpec all strict 18/18 PASS。专项日志 `/private/tmp/request-history-targeted-final.log`；race日志 `/private/tmp/request-history-race-final.log`；完整构建日志 `/private/tmp/request-history-make-final.log`。临时日志不是唯一证据，fixture/断言和命令已保存在仓库。Go使用默认GOCACHE/GOTMPDIR；本地沙箱出现非致命module stat-cache写入提示，make最终exit 0，构建成功。

## Performance evidence

1个Inventory账号，10000条当前七天内事件，全部相同occurred_at，复用既有数据库索引，无新index/cache。耗时包括Go repository调用，第一请求包含连接/prepare；不是生产容量SLA。

| Query | Exact result | DB round trips | Local latency |
|---|---|---:|---:|
| first25 | `history-10000`…`history-09976`，HasMore=true | 1 | 9.906ms |
| next25 | `history-09975`…`history-09951`，HasMore=true | 1 | 1.066ms |
| limit100 | `history-10000`…`history-09901`，HasMore=true | 1 | 1.429ms |

现有index均保留；该fixture的等价内部SELECT之EXPLAIN选用既有 `account_request_quality_retention_idx` backward scan（含occurred_at/node/hash），无需新增index，也不强制planner使用account index。外层函数调用含Inventory membership gate，仍只有一次客户端SQL往返。隔离Down/Up另比对所有event index定义不变。

## Self review

- 单账号history只解释窗口quality；Inventory账号集合、canonical identity和分类不变。
- 仅`account_request_quality_events`为事件来源；NULL/event-only不制造账号，缺失404而非empty。
- 固定DB七天，不支持start/end；time/hash keyset有界，无offset、无额外查询往返。
- UI明确区分未选、loading、empty、unavailable、populated及404，Node切换清选择/取消/隔离。
- runtime仅安全函数EXECUTE；00022只加query-access function，Down只drop该签名；无persistence/index变化。
- 无mutation/raw/detail/chart/export/search/date-picker/新监控平台，未扩scope到quota/inspection/automation。
- destructive-pop/no-ACK与缺失事件限制保留；History不是完整账本，见更新的[Topology runbook](../../../docs/runbooks/node-centric-topology-ui.md)及[采集runbook](../../../docs/runbooks/account-request-quality.md)。
- 实现完成等待Architecture + Implementation Final Review，不push、不deploy、不archive。

## Local commits and handoff

- `48c2dad` — docs(openspec): plan account request history
- `2193305` — feat(api): expose account request history
- `58046d4` — feat(web): add account request history
- 收尾文档以 `docs(topology): validate account request history` 提交，最终SHA见git log。

13/13 implementation tasks已完成；Architecture + Implementation Final Review仍待用户评审。独立backend/Web自查未发现剩余P1/P2；复核发现的精确boundary证据、opaque hash与late401测试均已补齐并通过。最终工作树以收尾commit后的 `git status --short` 为准。
