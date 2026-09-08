# Final Release Validation

2026-09-08，用户Final Review APPROVED，授权归档、同步canonical specs、本地commit与push main；不部署、不增加功能。

## Reviewed baseline

起始HEAD为 `1fa62d1dcf06c6d89d78dd9ae909728d9a4b0fec`，工作树干净，13/13任务完成。此次真实PG/API及race均基于该HEAD；使用本地55432既有测试migrator/runtime DSN并创建隔离数据库，未触碰运行环境数据库或Node。

## Final validation

| Command | Result |
|---|---|
| `go test ./internal/store ./internal/api -run 'TestAccountRequestHistory' -count=1 -v`（显式测试PG双DSN） | PASS，store 6.045s / API 1.605s，无SKIP |
| `go test -race ./internal/store ./internal/api -run 'TestAccountRequestHistory' -count=1 -v`（同PG环境） | PASS，store 6.816s / API 3.777s，无race报告 |
| `make test build` | 首次发现OpenAPI expected-operation清单缺少已存在History endpoint；只补一行清单后重跑PASS，包含全Go/tests及生成、前端17 files / 97 tests、typecheck/build、Control build |
| `openspec validate add-account-request-history --type change --strict --no-interactive` | PASS |
| `openspec validate --all --strict`（归档前） | PASS，18/18 |
| `git diff --check` | PASS |

`1fa62d1`只补了Quality endpoint的测试清单；本次 `959caeb test(openapi): include request history operation` 补入History同一清单，未修改OpenAPI、generated产物或生产代码。完整构建exit 0；默认Go缓存的非致命stat-cache沙箱提示没有影响构建，不覆盖GOCACHE/GOTMPDIR。

可查本地日志：`/private/tmp/request-history-archive-pg.log`、`/private/tmp/request-history-archive-race.log`、`/private/tmp/request-history-archive-make-final.log`。持久fixture与精确断言见本change planning-validation，不依赖临时日志保留。

## Frozen contract confirmation

- 当前Inventory安全read model先证明membership；event-only/NULL identity不创建账号。
- DB时间最近7天、time DESC/hash DESC稳定keyset，默认25/最大100。
- Missing 404不等于Empty，Inventory/DB失败503不等于Empty。
- 未修改CLIProxy、collector、queue、event schema、retention或failure taxonomy。
- 只有既有event表的readonly query-access function；无新表/index/mutation/raw detail。
- 原始设计/验收证据全部随归档保留；两个canonical capability同步已批准delta，不扩大范围。
- 本次不部署；上线需独立部署任务。

## Archive verification

标准CLI `openspec archive add-account-request-history --yes` 形成 `2026-09-08-add-account-request-history`。迁移前后8文件SHA256逐一相同（随后只修planning-validation相对runbook路径并补此归档结果）。canonical account-request-quality新增3项、node-centric-topology-ui新增1项/修改2项；逐项比对delta全文及scenario均已同步。归档后 `openspec validate --all --strict` 17/17 PASS，`git diff --check` PASS。
