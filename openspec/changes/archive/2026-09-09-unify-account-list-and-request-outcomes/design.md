## Context

本地CPAMP `apps/web/src/features/accounts/components/AccountLatestRequest.tsx`提供最多10个状态条，`AccountsPage.tsx`使用Drawer。只参考交互思路，不复制其quota、inspection、raw failure或自动操作。Control现有AccountInventoryView是手动查询，Topology质量GET已以Inventory驱动账号集合；00024 v2在数据库内复用Inventory v1与quality v1，HTTP/Go无逐账号往返。事件已有(node_id,account_key,occurred_at DESC)索引，History按时间/hash排序。

## Goals / Non-Goals

共享账号列表与只读详情，行内解释真实请求结果。保持Inventory active与Quality bad同时成立。无数据不是不可用。非目标：综合health score、quota、图表、export、raw body、token、account mutation、额外监控系统；不调整数据采集和保留策略。

## Decisions

1. 新增`POST /api/topology/nodes/{instance_id}/account-quality/query`只读组合查询，body承载window/provider/lifecycle/quality/email/basic_status/limit/cursor，email为规范化精确匹配。复用session-bound CSRF、super_admin、16KiB严格body、no-store及逐页view audit。保留既有GET原参数（不增加email）；GET/POST复用相同store v3与响应，新增必有`inventory`（安全AccountInventoryItem）及`recent_requests`（History item，max10，非nullable数组）。旧字段语义不变。POST默认15m/limit25，max100；UI默认present，生命周期省略仍all。POST复用现有Inventory AEAD cursor与15分钟有效期，绑定actor/Node/所有filters/window/quality及独立scope；新增filter字段omitempty保留旧POST空字段哈希兼容。email/cursor只在body，账号key不进入列表URL。原GET cursor兼容不改。
2. 新00025 `control_query_node_account_quality_v3`仅readonly query-access。沿用v2扫描安全Inventory v1，在该函数中应用email/basic_status/lifecycle/provider，再调用既有quality v1分类，质量筛选后keyset分页；仅返回页账号读取recent requests。SQL内部按账号调用复用函数，明确不是常数计算量，但保持单次store数据查询；POST另有固定逐页audit写入，与读取同一事务，不逐账号往返，不使用HTTP/Go N+1。禁止前端拼接不同分页。性能用100账号/10000events真实PG验证。
3. v3返回inventory安全metadata和bounded recent JSON，来源仅现有事件表；按node_id+account_key且最近7天`statement_timestamp()`窗口（不含未来时间事件），`occurred_at DESC,event_hash DESC`取10。NULL account_key/event-only不制造账号。摘要与15m/1h质量窗口独立，UI明确标注7天内最近10次，不能用这10次重新计算质量。success/failure_class/duration/model/time/request_id沿用History安全字段，无raw值或event_hash公开。
4. SECURITY DEFINER/STABLE/fixed search_path=pg_catalog/owner migrator/PUBLIC revoke/runtime EXECUTE；runtime不增加direct SELECT。旧v1/v2保留，Down只drop v3。原GET认证不变；新只读POST为super_admin+CSRF认证与5s预算，400错误过滤/cursor、401/403、数据库失败503，绝不降级为空或Unknown。
5. 共享AccountList组件与AccountDetailsDrawer。全局账号页显示Node选择和容量诊断；Topology传当前Node并展示相同表。全局无深链保持手动选Node后查询；深链初次默认首页查询，StrictMode不重复。筛选编辑与查询分离；Topology沿用即时筛选。旧API保留给既有consumer，生产页面不再渲染第二份旧账号表。兼容现有深链接`/account-inventory?instance_id=...`。
6. 主表默认收起采集时间细节：抽屉请求历史复用已有分页History，采集信息显示basic_status/lifecycle/缺失次数/first_seen/last_seen/refresh/retry/provider snapshot。账号用account_key，Node切换同步卸载旧列表/抽屉并取消请求；Incident可打开当前Node账号History，不要求其在当前表页。详情只有查看无mutation。
7. 最近请求条最多10条，按旧→新展示，成功绿色/失败红色并配文字或可访问名称；tooltip只有安全时间/模型/结果/分类/延迟。空数组显示最近7天无请求，读取失败展示Unavailable。latest时间来自真实记录，不由last_success/last_failure猜造。页面统一中文标题，不重复Account Quality标题。日期遵循现有时间显示惯例明确时区。

## Risks / Trade-offs

- 最近10次不是窗口成功率样本 → 标明独立7天范围，质量仍读冻结aggregate。
- 质量过滤需在DB扫描多个chunk → 保留timeout/limit与性能证据；不增加cache/index，若性能失败再评审。
- 迁移与Web必须配套 → 先additive migration再backend/Web，同版本交付；回滚旧应用保留forward schema与全部数据。
- 老列表POST与新GET授权模型不同 → 旧POST安全/审计不改，新入口使用受保护只读POST并复用原view audit设施；容量诊断仍用原API。Inventory v1函数继续执行registered/capability gate（P0404/P0409），错误保留404/409，不将未知Node当空集合。

## Validation

PG：当前账号无请求、unresolved/event-only/其他Node隔离、10条上限、7天边界、相同时间hash排序、过滤后分页、metadata、ACL、Down/Up、100账号性能。API：新增参数/字段、cursor跨filter拒绝、旧参数兼容、401/403/503。Web：双入口共享、默认present、深链一次、手动查询、红绿/无请求/不可用、抽屉/history切页、Node切换/Incident隔离、无mutation。targeted Go/PG/race、frontend tests/typecheck/build、make test build、strict/all、diff check。
