# Planning Validation

2026-09-08 baseline HEAD 5b43fda，起始工作树干净。CPAMP只读参考AccountLatestRequest.tsx/AccountsPage.tsx；不复制实现或raw failure字段。旧Inventory与Quality两个分页不在浏览器merge。用户明确授权新建change并完成优化，包含最近请求成功/失败。仅control仓库，未部署。

## Backend Evidence

只读组合查询采用新受保护POST，未将email加进GET。经过设计审查纠正最初GET方案，保留旧POST/GET兼容；原CSRF、AEAD 15分钟actor/environment/filter/scope绑定和逐页audit沿用，不用可解码GET cursor处理新列表POST。

`TestUnifiedAccountPOSTContracts`验证默认15m/25、空过滤、CSRF错误、认证/非管理员、非法过滤/超限body、cursor跨filter、metadata/recent安全投影、audit不可用503；缺CSRF header由既有generated required-header边界返回400，错误proof返回403。`TestUnifiedAccountCursorBindingsAndLegacyCompatibility`验证旧filter hash字节兼容、新scope/window/quality及所有原filter、actor/Node、篡改/15分钟过期、identity密文不可读。

`TestNodeAccountQualityUnifiedReadModelPostgres`验证Inventory账号/无请求、10条上限、7天与未来排除、同时间hash排序、unresolved/event-only/其他Node隔离、metadata字段。`TestNodeAccountQualityUnifiedMigrationPostgres`验证24→25→24→25，owner/migrator、STABLE、SECURITY DEFINER/search_path、PUBLIC无EXECUTE/runtime可用、旧v2和event保留。`TestNodeAccountQualityUnifiedAuditPostgres`验证第一/空/后续页逐页audit、details白名单、审计故障回滚且不返回items。

真实PG使用README的localhost:55432测试服务和runtime/migrator凭据，经helper创建全新隔离数据库，不操作部署数据。命令（需设置`CONTROL_DATABASE_TEST_URL`、`CONTROL_RUNTIME_DATABASE_TEST_URL`）：

```sh
go test -race ./internal/api ./internal/store -run 'TestUnifiedAccount|TestAccountQualityHTTPReadContracts|TestNodeAccountQualityUnified.*Postgres|TestNodeAccountQualityCompositionPostgres' -count=1 -v
```

结果PASS：API 5.364s，store 6.854s。原GET质量read contract专项仍PASS。PG性能fixture为100账号/10000events，first page25约31.0ms（独立运行27.3–33.4ms），1h/provider/quality/next-page均成功。一次store SELECT调用v3，函数内部逐账号复用aggregate和bounded recent扫描；POST额外固定audit INSERT与事务开销，不宣称常数计算量或单SQL含audit。未新增index/cache。临时日志`/private/tmp/unified-account-final-race.log`仅辅助排障，持久证据为本节命令、fixture与断言。

## Final Implementation Evidence

2026-09-08，实现完成并已分阶段本地提交，尚未 push、deploy 或 archive，等待用户 Final Review；本节独立工程自查不代替用户 Final Review。

- `npm test`：19 files / 124 tests PASS。包括 Inventory 默认 present、深链接/StrictMode 自动一次、手动筛选、分页、容量诊断；Topology 筛选和 Node 隔离；共享列表 10 条截断/排序、成功失败 aria-label、Unknown/Empty/Unavailable、canonical identity 详情选择、抽屉 History/采集信息及离页 Incident 不伪造 metadata。新增共享组件测试 13 项，Inventory/Capacity/共享组件合计 30 项。
- `npm run typecheck`：PASS。
- `npm run build`：PASS，重新生成 TS client 并构建生产资源。
- `make test build`：PASS，包括 Go 测试、前端 124 项及构建；真实 PG 与 race 证据见上节，未把普通测试中的 PG skip 当作集成验收。
- `openspec validate unify-account-list-and-request-outcomes --type change --strict --no-interactive`：PASS。
- `openspec validate --all --strict`：18/18 PASS。
- `git diff --check`：PASS。

独立只读审查及主 Agent 对账：无 blocker。账号集合由 Inventory 驱动，默认只看当前账号且保留缺失筛选；Quality 窗口与最近 7 天最多 10 次请求独立；POST 保持 CSRF、加密 cursor 和 fail-closed audit；旧 API 保留兼容。Node 切换清空/隔离详情并取消旧请求；Unknown 不代表读取失败。Incident 选中当前列表页外账号时，仅查询 History，采集信息明确未加载。宽表使用横向滚动，属于非阻塞展示取舍。

仅新增 readonly query-access function，不新增 table/index 或第二份 event persistence。CLIProxy、collector、retention、failure taxonomy、Inventory truth、Binding 与 Duplicate Ownership 未改变。无 mutation、quota、自动处置或监控平台扩展。

任务 8/8 完成。本轮改动已在 control 本地提交供评审，不更新本地运行环境。

## Local Delivery Reconciliation

- `db65583`：OpenSpec proposal/design/spec delta。
- `16b83fa`：readonly query-access migration、组合 read/API、生成代码及后端验收。
- `85ab811`：共享账号列表、最近请求条、详情抽屉及前端测试。

每个提交前执行 `git diff --cached --check`。首次 staged 检查发现新 spec 文件末尾多余空行，已修正后通过；未改变产品代码或已验证行为，因此不重复完整构建。验证和 runbook 另以文档提交保存。用户 Final Review 仍待完成，未 push/deploy/archive。

## Final Review P2 Follow-up

对 `49ab258` 的后续 Final Review 确认 Architecture PASS、Implementation BLOCKED：真实 Web adapter 将 All lifecycle 的 `undefined` 覆盖成 `present`，并把未设置的 provider/basic_status/quality 写成 OpenAPI 不允许的空字符串。此前“无 blocker”的独立自查未覆盖真实 adapter，此处以后续发现为准。

修复只涉及 Web transport：页面初始 `present` 保留；adapter 不覆盖 lifecycle，未指定字段随 JSON serialization 省略。basicStatus 使用生成的枚举类型，删除空字符串的强制类型断言；API/schema/数据库及数据采集行为均未修改。

新增 `web/src/api/account-list-transport.test.ts` 使用真实 generated client、仅 mock fetch：分别以 25/50 page size 验证 present → missing → All 的实际 POST body；确认所有未指定筛选均省略，并验证显式过滤、CSRF、no-store、same-origin 和 AbortSignal 透传。补齐旧页面 mock 未覆盖的序列化边界。

`npm --prefix web test -- --run src/api/account-list-transport.test.ts src/pages/AccountInventoryView.test.tsx src/pages/TopologyView.test.tsx`：39/39 PASS；`npm --prefix web run typecheck`：PASS。

修复后完整 `make test build`：PASS，前端 20 files / 127 tests PASS，构建成功。Go build 输出一次 module stat cache 写入权限警告但命令退出 0，不影响构建结果。change strict PASS、all strict 18/18 PASS、`git diff --check` PASS。独立修复审查 PASS，实际 transport 专项 3/3 PASS，原 P2 已修复；等待 Final Re-review。仅本地提交，无 push/deploy/archive。
