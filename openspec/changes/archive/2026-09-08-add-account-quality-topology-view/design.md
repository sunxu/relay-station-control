## Context

基线 main `c2e3cfa`，工作树干净。现有 `internal/store/account_request_quality.go` 与 migration00020 已提供15m/1h的全部统计，未有HTTP/UI；canonical account-request-quality 的“不扩展UI”是该采集change的范围，本change独立增加Topology读取。现有 TopologyView 分区独立请求，Inventory仅deep-link；Inventory current query 按account_key排序、最大101（含lookahead），维护能力检查和不一致报错。遵循 ADR-0001 §3.3–3.5 与 ADR-0002 数据面隔离。

## Goals / Non-Goals

只读单账号表现，以Inventory已持有的current lifecycle账号集合（包含既有present/suspected_missing/missing/out_of_scope，不自行重新筛除）为准。事件中仅有的账号和unresolved不制造行。不更改collector、CLIProxy、event schema、retention、failure taxonomy；其余非目标见proposal。

## Decisions

1. 新增 `control_query_node_account_quality_v1` 小型安全query wrapper。按既有 `control_query_current_account_inventory_v1` 的account_key cursor分块扫描（每块最多101），对返回账号复用 `control_query_account_request_quality_v1`。provider下推Inventory，quality分类过滤后才计入最终limit+1。Go只发一次查询，不先抓全部账号到内存；内部仍按账号调用已有聚合函数，明确记录成本。拒绝逐账号Go网络往返和复制统计SQL。
2. 默认window15m，允许1h；provider/quality空表示All。默认limit25，最大100；响应items与next_cursor。排序沿用account_key canonical排序；cursor为有界opaque base64 JSON，绑定Node/window/provider/quality与最后account_key，参数不符400。使用数据库statement时间计算窗口；分页是实时读取，跨页数据可能变化，不宣称跨请求snapshot一致，但账号identity不会混淆。过滤变更重置cursor。
3. `GET /api/topology/nodes/{instance_id}/account-quality` 返回instance_id、window、items、next_cursor；每项account_key、email、provider、quality及原8个quality字段。百分比底层0–1；NULL显式JSON null。GET only、super_admin session、无CSRF mutation、no-store、现有request ID、5s timeout。非法参数400，Node不存在404；缺能力/查询不一致/DB失败503。不把unavailable变empty。现有Inventory页面的POST与view audit完全保留；新只读GET不触发该管理页面，也不新增audit写。
4. 分类仅query/API/UI投影：count0 unknown，否则rate>=0.95 good，>=0.80 degraded，其余bad。Latency不分类。Inventory active与quality bad可同时成立；既有Account Quality函数signature/统计及所有ownership truth不变。
5. 新00021 additive query-access migration（以实施时序列为准）：SECURITY DEFINER、STABLE、fixed pg_catalog search_path、owner migrator、REVOKE PUBLIC、仅runtime EXECUTE。不扩大任何表直接SELECT。Up只创建新function/ACL；Down只drop该function，不触碰表/历史数据/旧函数。所有读在一个DB statement snapshot内，取消/timeout回滚读操作，无新事务写入/后台worker/幂等系统。
6. Topology Node detail独立Account Quality card，列Account/Provider/Quality/Success/Requests/P95/Last Failure。复用provider summary完整集合供筛选，provider读取失败明确显示筛选来源不可用，不伪装空集合。query key含Node/window/provider/quality/cursor；Node切换重置section状态并取消旧请求。Loading、真实empty、零事件unknown、错误unavailable分开；error优先不呈现旧成功数据。key=account_key，不使用index。只显示failure_class和UTC时间，无raw body。
7. OpenAPI为契约真相，make generate刷新Go/TS，前端复用TanStack Query与现有API adapter。无新依赖、metrics、高基数日志、Secret、数据面调用。只本地commit，不push/deploy/archive。

## Risks / Trade-offs

- 稀疏quality filter需扫描多个Inventory chunks → 单次HTTP/DB往返、5s timeout、有界响应，真实PG以100accounts测15m/1h及filters；不提前加缓存或rollup。
- 内部每账号复用聚合函数 → 性能证据须区分client query count和内部函数次数，若100账号达不到合理本地预算再定位；不把单往返声称单次全量聚合。
- 时间窗口变化导致跨页成员变化 → identity cursor稳定且绑定filter，不提供snapshot/export承诺。
- 旧source destructive-pop/no-ACK限制仍存在 → 本change不改变采集可靠性，保留原canonical/runbook。

## Migration Plan

未来发布顺序为additive migration → backend/generatedWeb同release。当前不部署。回滚应用停止新读取，生产保留forward schema；隔离测试验证Down只删除新function及Up可恢复。Final Review前保持change未archive。
