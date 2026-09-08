# Planning validation

## Baseline and decisions

2026-09-08 main `d0090db`，Control工作树干净。现有00020/21/22、安全Inventory/auth与canonical Quality/Topology/Jobs已检查。先生成proposal/design/spec/tasks并strict PASS，再以 `39c154d` 提交规划开始实施。原功能验证参考已归档History evidence；本轮重新运行相关验证而非冒用之前的PASS。

active-only是用户允许的MVP分支：15m同Node/account/class至少3失败，成功不解除达标active，低于阈值不返回而不是recovered。first_seen/last_seen/hit_count为本15m组，last_success_at为该账号7天内最大成功时间。无持久episode、恢复或自动动作。

## Acceptance matrix

| ID | Implementation / fixture / exact assertion | Result |
|---|---|---|
| I1 | `node_account_quality_incidents_integration_test.go: TestAccountQualityIncidentsPostgres` 四类各3失败精确identity/class/hit_count=3，2次账号无行；最近成功仍保留active且last_success_at精确时间；unknown不返回 | PG PASS |
| I2 | 同fixture含110个padded账号，109确实在首101后；NULL3/event-only3/其它Node3排除，6个预期row逐项相等。`BoundaryPostgres`同SQL DO插入exact15m/早1微秒/future，唯一结果必须目标auth且精确min/max/count；success恰好7天纳入；后续statement窗口滚动后返回empty，不标Recovered。only-old/future success账号last_success_at必须NULL | PG PASS |
| I3 | 不同last_seen优先于key，时间相同按key/class升序；limit2逐页拼接与完整6row精确DeepEqual，不能其它记录替代；quota过滤精确1row。性能fixture验证每个返回row身份及class/25hits，覆盖provider/empty provider与next25/100 | PG PASS |
| I4 | `account_quality_incidents_http_integration_test.go`实际PG session+injectable reader；默认active/25、max100、status/recovered/unknown/invalid400；cursor真实结构每次只改一个字段（Node/status/provider/filter/time/key/rowclass）400且reader不调用；validroundtrip微秒时间/key/class精确相等，9字段和last_success_at NULL；401/403/POST405/no-store/request-ID，404/503/empty200及恢复互不混淆 | API PASS |
| I5 | `TestAccountQualityIncidentsACLAndRollbackPostgres`runtime精确读取retained账号auth3次；pg_proc检查SECURITY DEFINER/STABLE/migrator/fixed pg_catalog/PUBLIC revoke/runtime EXECUTE；三表direct SELECT42501。Down到22只drop新function后读取error；Up恢复精确组，原表/列/data快照及全部event index/旧Inventory/Quality/History函数定义不变 | PG PASS |
| U1 | `AccountQualityIncidentsSection.test.tsx`10tests：Loading、Empty、503且不Empty、populated Active、精确UTC时间、Provider/Reason filters（先next再改filter清cursor）、分页、provider source unavailable、当前401与无mutation | frontend PASS |
| U2 | section same QueryClient换Node明确old signal aborted、迟到401不清新Node会话；`TopologyView.test.tsx`真实Incident账号按钮以account_key调用已有History；现有Node switch测试继续证明History清选择。Incidents父组件key=Node，scope切换重建filters/pages，读取无placeholder旧结果 | frontend PASS |
| P1 | `TestAccountQualityIncidentsPerformancePostgres`100accounts10000events，四类共400active，每组25失败；每行精确identity/class/count/null校验，tracer每次1query，所有case <1s本地预算；无新增index/persistence | PG PASS |
| V1 | 下述targeted PG/API及History回归race、全量frontend/typecheck/build/make test build | PASS |
| V2 | change/all strict、diffcheck、runbook、13/13任务、本地分阶段commit | change PASS/all 18/18 PASS，Final Review待用户 |

## Commands and results

真实PostgreSQL使用既有55432测试服务并显式设置 `CONTROL_DATABASE_TEST_URL`（migrator）与 `CONTROL_RUNTIME_DATABASE_TEST_URL`（受限runtime）；fixture创建隔离数据库。未以SKIP作为PG通过，未操作55434部署DB或CLIProxy。Go保留默认GOCACHE/GOTMPDIR。

```sh
go test ./internal/store ./internal/api -run TestAccountQualityIncidents -count=1 -v
go test -race ./internal/store ./internal/api -run 'TestAccountQualityIncidents|TestAccountRequestHistory' -count=1 -v
make test build
openspec validate add-account-quality-incidents --type change --strict --no-interactive
openspec validate --all --strict
git diff --check
```

- targeted：store 9.875s、API 4.185s，PASS。
- race（含History regression）：store 15.079s、API 8.092s，PASS，无race报告。
- `make test build`同次调用含generate/Go全包test、frontend18 files/108 tests PASS、typecheck、frontend build与Control binary build，exit0。常规make不启用全量PG，PG证据来自上述显式DSN专项。
- 前端专项 `npm test -- --run src/pages/AccountQualityIncidentsSection.test.tsx src/pages/TopologyView.test.tsx` 32 tests PASS；随后只整理测试格式与补503不Empty断言，再执行Incidents10tests PASS。`npm run typecheck`专项亦PASS，build由make完整执行。
- 只新增GET，`tools/openapi_contract_test.go`清单同步，generated Go/TS由make生成无手工修改。
- 本地日志 `/private/tmp/incidents-targeted-final.log`、`/private/tmp/incidents-race-final.log`、`/private/tmp/incidents-make-final.log`；持久证据以本仓库fixture/精确断言和命令为准。

## Performance

最终专项与race/build同时运行，有本地竞争；耗时仅为fixture证据，不是生产SLA。1个数据库往返包括Inventory安全分块（完整keys）、一次事件集合聚合、分页结果的有界成功时间lookup，不声称只有一条内部SQL。

| Case | Rows | Client DB queries | Latency |
|---|---:|---:|---:|
| active default25 | 25 | 1 | 29.799ms |
| provider=openai | 25 | 1 | 9.310ms |
| failure_class=auth | 25 | 1 | 7.885ms |
| next25 | 25 | 1 | 9.335ms |
| limit100 | 100 | 1 | 6.889ms |
| provider=anthropic（无匹配） | 0 | 1 | 1.172ms |

已有account/provider/retention索引足够，不新增表/index/worker/materialized view/cache。返回只做当前窗口计算；跨页有新事件可改变last_seen，明确不提供冻结snapshot。

## Self review and handoff

- Incident不是新truth；identity仍node/account_key/class，provider来自已证明的canonical key。
- current Inventory完整gate不变，event-only/unresolved不制造账号；read failure不伪装空集合。
- active-only没有recovered字段选项或假恢复行，成功只提供证据不抵消阈值。
- CLIProxy、collector、queue、event schema、retention、taxonomy、Inventory/lifecycle/Binding/Duplicate/Quality classification均未修改。
- 无mutation/resolve/disable/请求retry/ack/comments/notifications/Jobs remediation、Prometheus/Grafana/quota/inspection。
- 00023只readonly query-access，默认runtime无direct SELECT；生产回滚保留forward schema，Down仅隔离测试。
- 采集destructive-pop/no-ACK限制仍在runbook，Empty不等于健康；所有操作只读，重启重算，不持久化Incident。
- 本地分阶段commit：规划`39c154d`、API`2fa935a`、Web`c41eb68`；证据收尾commit最终SHA见git log。
- 13/13 implementation tasks完成；不push、不deploy、不archive，等待Architecture + Implementation Final Review。
