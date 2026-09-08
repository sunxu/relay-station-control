# Final Release validation

2026-09-08，Implementation Final Review已获用户APPROVED，按明确授权归档并push main，不部署、不增加功能。

## Baseline and scope

归档前HEAD `0efcf82`，工作树干净；13/13 tasks，所有planning artifacts done。与实施前`c2e3cfa`比较，CLIProxy integration、`internal/requestquality`、既有event migration00020、collector配置/runtime文件无diff。新增00021仅readonly query-access function。

冻结契约保持：Inventory驱动账号集合；无请求Unknown；NULL身份事件不归属账号；Unknown与Unavailable分开；GET only/no mutation；不修改CLIProxy、collector、event schema、retention或failure taxonomy；不增加Prometheus/Grafana/quota/inspection。

## Final commands and results

所有命令在control仓库执行。PG使用README开发测试DSN `CONTROL_DATABASE_TEST_URL`和`CONTROL_RUNTIME_DATABASE_TEST_URL`，host55432；fixture创建隔离数据库、runtime role查询，未触碰部署数据库55434。

```bash
go test ./internal/store ./internal/api -run 'Test(NodeAccountQuality|AccountQualityHTTPReadContracts|TopologyHTTPReadContracts)' -count=1 -v
go test -race ./internal/store ./internal/api -run 'Test(NodeAccountQuality|AccountQualityHTTPReadContracts|TopologyHTTPReadContracts)' -count=1 -v
make test build
openspec validate add-account-quality-topology-view --type change --strict --no-interactive
openspec validate --all --strict
git diff --check
```

- targeted真实PG：store PASS 5.142s，api PASS 2.472s，exit0，无SKIP。包括classification/null/分页/ACL/down-up/100accounts性能及auth/HTTP契约。
- relevant race真实PG：store PASS 6.405s，api PASS 5.029s，exit0，无race报告、无SKIP。
- make test build：exit0，包括Go tests、frontend16files/83tests、npm typecheck、npm build（Vite成功）及Go build，生成代码无diff。保留默认GOCACHE/GOTMPDIR；Go module cache出现非致命permission提示，构建仍成功。
- change strict PASS；archive前all strict18/18 PASS；diff check PASS。

日志位于 `/private/tmp/quality-view-archive-pg.log`、`quality-view-archive-race.log`、`quality-view-archive-build.log`、`quality-view-archive-pre-strict.log`。关键结果已持久保留于本文；这些临时日志无需成为发布依赖。

归档使用标准openspec archive CLI同步canonical `node-centric-topology-ui`；归档后再次all strict及文件/requirement完整性检查，结果见归档提交与交付报告。
