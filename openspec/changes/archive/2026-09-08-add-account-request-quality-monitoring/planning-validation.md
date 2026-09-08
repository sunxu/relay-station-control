# Implementation validation

日期：2026-09-08。Architecture Review 的 nullable identity 决定已实施。
验证目标是 **Every provably attributable event is attributed correctly**；所有合法事件都可保留，不能证明归属的事件account_key=NULL。

## Implementation map

| 能力 | 实现 |
|---|---|
| HTTP queue 与当前lookup | internal/drivers/cliproxyapi/usage_queue.go；扩展现有transport固定operation |
| CPA normalization/hash | internal/requestquality/cpa_normalize.go、normalize.go；CPA-MIT-LICENSE.txt |
| canonical identity | internal/inventorypoll/projection.go 的既有规则通过小型export wrapper复用 |
| 最小event与quality类型 | internal/requestquality/types.go；十个持久字段，无identity_status |
| collector | internal/requestquality/collector.go；每Node一个loop、批次重试、cancel/retention |
| PostgreSQL | migrations/00020_account_request_quality_events.sql；internal/store/account_request_quality.go |
| runtime | cmd/control/account_request_quality.go、node_drivers.go、main.go；复用driver与shutdownContext |
| runbook | docs/runbooks/account-request-quality.md；默认关闭，无本轮部署 |

## Acceptance matrix

| 验收 | Fixture / assertion | Result |
|---|---|---|
| 1 HTTP正常pull | TestUsageQueueTransportContract：Driver.PopUsage实际验证路径、count=100、Bearer、object/string元素 | PASS |
| 2 空queue | transport empty、TestCollectorEmptyQueueDoesNotWrite | PASS |
| 3 malformed | TestNormalizeFailuresAndMalformed；malformed string保留至normalizer；TestCollectorRetainsFailedBatchUntilCommit同批valid仍写 | PASS |
| 4 success | TestNormalizeFailuresAndMalformed验证CPA failed/success优先级，成功class=NULL | PASS |
| 5 五类failure | auth、明确quota、429 rate_limit、5xx upstream、unknown fixture | PASS |
| 6 identity | TestNormalizeIdentityEvidence：direct email/source/account，unique index、相同映射重复，missing/deleted、不同key歧义、provider/direct/snapshot冲突 | PASS |
| 7 duplicate | TestAccountRequestQualityRepositoryPostgres：同Node/hash只一条，不同Node同hash独立 | PASS |
| 8 restart | TestAccountRequestQualityHTTPCollectorPostgresClosedLoop：实际重启collector、等待第二次成功提交相同batch，仍3行 | PASS |
| 9 15m | TestAccountRequestQualityRepositoryErrorsAndTargets：2min事件进入，20min和future不进入 | PASS |
| 10 1h | 同fixture的20min事件进入，future排除，失败字段保留 | PASS |
| 11 p95 | repository fixture延迟100/200精确p95=195；100k性能fixture另记录p95 | PASS |
| 12 no request | missing account计数0、rate/p95=NULL，unknown | PASS |
| 13 DB failure | mixed valid/invalid batch整批回滚；closed pool查询返回error；collector固定batch重试且不继续pop | PASS |
| 14 cancellation | TestCollectorCancellationStopsHTTPAndBackoff、TestUsageQueueInFlightCancellation、runtime shutdown跨层测试 | PASS |
| unresolved不污染账号 | AccountQuality只统计resolved；HTTP→PG fixture账号2次成功、Node/Provider3次含1次unresolved failure | PASS |
| unresolved count | NodeProviderQuality.UnresolvedRequestCount=1，无新页面 | PASS |
| auth-files来源 | TestCurrentIdentitiesHTTPProjection：重复auth_index不消失，只有explicit email，拒绝fallback/trailing/超限 | PASS |
| 单owner/目标范围 | TestCollectorSingleOwnerAndMonitoringRemoval；PG active+capable唯一进入，active无cap与inactive有cap均排除 | PASS |
| runtime ACL | PG拒绝基础表SELECT/UPDATE/DELETE/TRUNCATE；四函数migrator owner、SECURITY DEFINER、fixed pg_catalog search_path | PASS |
| 7天保留 | 8天事件删除，窗口内事件保留；collector周期实际调用retention | PASS |

## Commands and results

使用README中的隔离PostgreSQL `127.0.0.1:55432`，fixture创建独立测试数据库并清理；不是本地部署数据库55434。没有调用真实Node queue，也没有改变部署。

1. `make test build`：PASS。Go常规测试、生成、Web 16文件/74测试、类型检查、Go/Web build通过。日志 `/private/tmp/request-quality-full.log`。宿主Go模块cache写入提示sandbox warning，但命令退出0、构建完成；未覆盖GOCACHE/GOTMPDIR。
2. `go test -race ./internal/requestquality ./internal/drivers/cliproxyapi ./cmd/control -run 'TestNormalize|TestCollector|TestUsageQueue|TestCurrentIdentities|TestAccountRequestQuality' -count=1`：PASS。日志 `/private/tmp/request-quality-race-final.log`。
3. 设置 `CONTROL_DATABASE_TEST_URL`、`CONTROL_RUNTIME_DATABASE_TEST_URL` 为README隔离环境后，`go test -race ./internal/store -run '^TestAccountRequestQuality' -count=1 -v`：五项顶层专项全部PASS、无SKIP。日志 `/private/tmp/request-quality-postgres-final.log`，8.287s。
4. 随后补强source in-flight cancellation和PG target/ACL断言；直接相关 `go test -race ./internal/drivers/cliproxyapi -run '^TestUsageQueue|^TestCurrentIdentities' -count=1` PASS；两个PG target/repository专项PASS。日志 `/private/tmp/request-quality-source-final.log`、`/private/tmp/request-quality-acl-final.log`。生产实现未因此变化。
5. `openspec validate add-account-request-quality-monitoring --type change --strict --no-interactive`、`git diff --check`：PASS；提交前另执行cached check。

## 100000 event evidence

真实runtime受控函数写入100批×1000，实际count=100000；这是合成性能数据，不是生产流量。

最终带race专项中的一次测量：

| 查询 | request_count | success_rate | p95 latency | Go调用耗时 |
|---|---:|---:|---:|---:|
| account 15m | 99800 | 0.8998 | 951ms | 48.072ms |
| account 1h | 100000 | 0.9000 | 950ms | 46.288ms |

15m边界附近的事件会随插入/测试时间自然移出窗口，故不把固定边界count作为性能fixture断言；精确窗口正确性由独立2min/20min/future fixture验证。匹配过滤SQL EXPLAIN ANALYZE使用`account_request_quality_account_idx`，100000行Execution Time=22.508ms。未新增partition/rollup。

## Self-review and limits

- CLIProxy/CPA零修改；HTTP usage queue唯一事件source，auth-files仅identity lookup。
- 无SQLite/RESP/Subscribe、无历史identity子系统、无quota/inspection/automation、无UI/Grafana、无rollup/partition。
- account_key沿用现有canonicalization；NULL不进入任何具体账号统计，Node/Provider保留。
- 内容hash规范化不依赖lookup结果，DB `(node_id,event_hash)`幂等；DB失败不会被当作空数据。
- destructive queue无ACK：pop后响应丢失或提交前进程崩溃仍有不可重放窗口，不宣称exactly-once或零丢失。
- 默认关闭；没有push/deploy/archive。完成后等待Implementation Review。

## Local commits

- `92618a9`：HTTP source、CPA normalization、canonical identity resolution及专项测试。
- `182eacd`：PostgreSQL/read model、migration、collector/runtime及真实PG/跨层测试。
- 后续docs提交：本change契约、runbook和本文件证据。每个commit前`git diff --cached --check`通过。

OpenSpec change strict与all strict均PASS；最终任务12/12。Control最终工作树以交付git status为准；CPA/Node没有本轮修改。
