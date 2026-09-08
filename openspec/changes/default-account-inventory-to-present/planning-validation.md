# 验证证据

## 第一阶段：账号清单默认值（原本地提交 3634931，随后合并 amend）

- Baseline：main `4ee29a5`，工作树干净；页面 lifecycle 初值 undefined，已有全部生命周期清空入口。
- 实现：`web/src/pages/AccountInventoryView.tsx` 仅将生命周期初值改为 present；已有下拉框与 allowClear 保留，服务端 query contract 不变。
- 专项：`npm test -- --run src/pages/AccountInventoryView.test.tsx`，13/13 PASS。覆盖 StrictMode 默认 present、无 URL 手动查询、missing 显式提交、清空条件及 cursor 重置。新增测试曾因仅触发 mouseDown 而未执行实际 clear 失败，修正为真实 click 交互后通过。
- `make test build`：PASS（exit 0），前端 18 文件 / 110 测试、typecheck、前端 build、Go tests/build 通过；日志 `/private/tmp/inventory-present-make.log`。Go 模块 stat cache 出现非致命 sandbox warning，命令成功结束。
- Change strict 与 all strict（17/17）：PASS；`git diff --check`：PASS。
- 范围：仅页面默认值、页面测试、新 change 文档；无 API/DB/migration/generated code 修改，不修改已归档 change。本次仅本地提交；未 push、部署或归档。

## 第二阶段：Topology Account Quality 同步默认当前账号

- 用户确认同一需求覆盖Topology；仍使用本change，不修改已归档change。本阶段验证时未提交；用户随后授权合并 amend 到原本地提交。不 push、不部署、不 archive。
- UI：默认 present，生命周期选项保留 missing/suspected_missing/out_of_scope/清空全部；Node切换重置present，筛选重置cursor且query cache key包含lifecycle。
- HTTP：新增可选lifecycle；省略=all保持兼容，非法值/空值/跨lifecycle游标400且不调用reader；合法游标往返和原无lifecycle游标通过。
- SQL：migration 00024仅新增v2只读函数；过滤先传入Inventory安全函数再quality合成/分页；v1、事件schema、retention、taxonomy、History/Incidents范围不改变。生成Go/TS由make generate生成。
- TopologyView专项23/23 PASS；make test build PASS（18 files/111 tests、Go tests/build、Web typecheck/build；日志 /private/tmp/quality-lifecycle-make.log）。Go默认cache/GOTMPDIR未覆盖，stat cache sandbox warning不影响exit0。
- API真实PostgreSQL：55432隔离测试库，TestAccountQualityHTTPReadContracts常规1.392s、race3.044s PASS；日志 /private/tmp/quality-lifecycle-api.log 与 /private/tmp/quality-lifecycle-api-race.log。没有操作55434部署库。
- Store PostgreSQL最终专项：`go test -race ./internal/store -run '^TestNodeAccountQuality.*Postgres$' -count=1 -v`，PASS（8.686s）；环境变量使用 README 的 CONTROL_DATABASE_TEST_URL 与 CONTROL_RUNTIME_DATABASE_TEST_URL（55432），日志 `/private/tmp/quality-lifecycle-store-race.log`。
- 新增PG精确验收：2 present+2 missing；limit1逐页校验 present/missing/all 的目标account_key、HasMore与最终空页；unknown与lifecycle组合过滤。v2 SECURITY DEFINER/STABLE/migrator/fixed pg_catalog/PUBLIC无EXECUTE/runtime有EXECUTE；三张表直接SELECT明确42501；Down确认v2不存在、v1仍返回4账号；Up恢复missing精确目标，持久状态前后相等、v1定义不变。
- 原Account Quality composition/acceptance/ACL/rollback/performance回归一并PASS。100账号/10000事件，既有15m/1h、provider/quality case约127–130ms、每次1数据库请求（race环境）；没有新增index或缓存。
- 独立API/Web只读审查未发现P1/P2；旧页面initial默认行为、无参数HTTP兼容、cursor绑定、Node/cache隔离与只读边界保持。完整Final Review尚待用户。
- 最终change/all strict、git diff --check：PASS；所有9项任务完成。用户授权将本阶段与此前账号清单提交 3634931 合并 amend，提交标题为 `feat(inventory): default account views to present lifecycle`；不 push、不部署、不 archive。
