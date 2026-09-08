## Context

基线main `9e22c21`，工作树干净，Account Quality已归档。00020已有全部11字段与 `(node_id,account_key,occurred_at DESC) WHERE account_key IS NOT NULL` 索引；00021只组合Inventory与窗口统计。`control_query_current_account_inventory_v1` 可用精确provider/email筛选，且验证Node/capability/provider-current-source一致性。相关系统边界见proposal。

## Goals / Non-Goals

只解释现有账号的质量，不改变质量分类、Inventory/lifecycle/Binding/Duplicate/Node真相。账号必须在既有current Inventory read model中存在（沿用其lifecycle集合，不把“current”重新定义为仅present）。使用已存事件，不修改采集、来源、字段或7天retention。其余非目标见proposal。

## Decisions

1. 新00022 `control_query_account_request_history_v1(target_node uuid,target_account_key text,after_occurred_at timestamptz,after_event_hash text,page_limit integer)`。先校验参数；用account_key的首个冒号提取provider/剩余email仅作已有安全函数精确lookup，结果必须同一account_key。禁止identity重写/猜测。Inventory gate失败抛错；不存在P0404→404（包括event-only），能力/一致性/DB故障→503。账号存在且无event才empty。精确lookup limit1，不遍历账号，不复制Inventory truth或聚合SQL。
2. gate和events查询在同一个STABLE函数、同一statement snapshot内；固定 `occurred_at >= statement_timestamp()-interval '7 days' AND occurred_at <= statement_timestamp()`，显式非NULL且等于目标node/account。DB UTC为基准，排除future。排序occurred_at DESC,event_hash DESC，tuple `<` cursor；limit默认25、最大100，SQL返回limit+1。复用现有索引，timestamp并列可能小范围排序；10000events性能先测，不预加index。一次Go→DB查询，无额外membership roundtrip。只读取消与5sHTTP context传播；无写事务、worker、幂等/重试请求系统。
3. 新GET `/api/topology/nodes/{instance_id}/request-history`，required query `account_key`（opaque identity，最大385 UTF-8 bytes），可选limit/cursor；采用query而非path避免key中/等字符影响路由。没有start/end或额外filters。返回instance_id/account_key/items/next_cursor，每item仅occurred_at/model/success/failure_class/duration_ms/request_id；failure/duration NULL，request_id可空。event_hash仅store及cursor内部使用，不作为item字段。
4. opaque base64url JSON cursor绑定Node/account_key/occurred_at/event_hash；严格检查完整性、Node/account错配、时间有效、hash1–256bytes。上限8192容纳Go JSON转义后的最大合法identity/hash，时间以RFC3339Nano往返、不丢数据库微秒。无offset；cursor不签名沿用Topology约定，super_admin可读整个Node但不能串账号。未来事件不返回；跨页实时7天窗口，不承诺冻结snapshot或无retention删行。
5. super_admin、no-store、request ID；401清会话，403拒绝，400参数/cursor非法，404 Node或Inventory账号不存在，503 DB/Inventory不可用。既有规格禁止account_key进入URL，与用户明确GET身份传输要求存在范围变化：仅允许本API的account_key query与内部cursor携带身份；浏览器导航URL、storage、日志、metrics仍不得写入key，不增加其它治理系统。现有GET handlers不记录query/body，不新增访问日志。
6. Topology Account Quality每行View History用row.account_key选择，新增独立AccountRequestHistorySection。上下文固定Node/account，无provider/date/search。Node切换清选择并取消旧query，账号变化重置cursor；Quality filter/page变化可保留当前选择（显示明确账号上下文），不影响其它分区。query key含Node/account/cursor，禁用placeholder旧数据，错误优先；History no-selection/loading/empty/unavailable/populated独立。404显示账号不存在且需重新选择，不冒充empty。
7. History仅六列，无详情页/raw数据/action。成功failure显示—，duration NULL与request_id空为—，时间UTC可见。不能假定request_id唯一（可能空），不公开hash；History只读行可使用账号/页scope下的展示序号作为React render key，不用于任何账号选择/服务端identity，Account Quality仍key=account_key。
8. migration为SECURITY DEFINER STABLE fixed search_path=pg_catalog、owner migrator、revoke PUBLIC、仅runtime EXECUTE。仅CREATE function/ACL，Down只drop新签名；表、columns、index、已有functions和data不变，runtime无direct SELECT。使用make generate刷新OpenAPI Go/TS，不手改generated。

## Risks / Trade-offs

- event来源destructive pop/noACK可能有缺口 → 保留原runbook限制，不把7天history称完整账本；过期或未采到不伪装失败。
- 当前Inventory账号已移除 → History404，保留事件不代表可绕过membership；不实现历史身份系统。
- 同timestamp大量事件需要tie排序 → 10000event fixture包含相同timestamp与下一页，记录单query/latency和现有index计划；不自行扩schema。

## Migration Plan

未来发布先additive readonly function再backend/Web，生产回滚应用保留forward schema。隔离PG验证Down/Up及原表/索引/函数/数据不变。本轮不部署、不push、不archive；本地分阶段commit，等待Final Review。
