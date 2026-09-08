## Context

基线main d0090db，工作树干净。已检查canonical Quality/Topology、00020 event/index、00021 Quality与00022 History、Durable Jobs、Inventory00008/auth/API/query conventions。Durable Job有持久lease及workflow状态，不适合Incident；本change不接入。沿用System Design v1.8/R4.7与ADR-0001/0002只观察边界。

## Goals / Non-Goals

目标为单Node当前Inventory账号的重复失败模式。非目标见proposal：不添加Incident真相、worker、恢复状态机、通知、动作或新监控平台。

## Decisions

1. identity固定(node_id,account_key,failure_class)，不把provider或时间加入identity。active iff DB statement_timestamp最近15分钟同组失败>=3，包含下边界、排除future。四类auth/quota/rate_limit/upstream独立；unknown/NULL账号不生成。成功不减hit_count也不解除满足阈值的active。first_seen/last_seen为本15分钟组内min/max，hit_count本窗口count；last_success_at为同Node/account最近7天内、不晚于DB当前时间的最后成功时间，允许NULL。
2. MVP只计算active，status参数可省略或active，其它值（包括recovered）400，响应status恒active。当前成功不能证明历史episode已恢复，7天retention不能提供持久“曾active”真相；不做recovered或历史重建。低于阈值只消失，不宣称恢复。
3. 新00023 `control_query_node_account_quality_incidents_v1(target_instance_id uuid,target_provider text,target_failure_class text,after_last_seen timestamptz,after_account_key text,after_failure_class text,page_limit integer)`。在同一STABLE函数statement中按既有Inventory安全函数101条chunk完整读取当前账号key（provider filter传给既有函数，沿用lifecycle及source一致性门禁），用内存text[]汇集identity；不持久化、不截断首101。空集合仍经过Node/capability/一致性校验。单次集合聚合events JOIN/ANY当前keys，按account_key/class分组HAVING>=3，不逐账号调用质量函数。provider由已验证canonical key前缀读取；不改变identity。分页后用既有account索引读取每账号最近7天success。单次Go→DB查询，数据库内部为Inventory分块+事件集合聚合+有界成功时间lookup，不声称只有一次内部SQL执行。
4. 过滤先于分页；排序last_seen DESC,account_key ASC,failure_class ASC。混合方向cursor predicate：last_seen<after OR last_seen=after AND (account_key,failure_class)>(after_key,after_class)。limit默认25最大100，函数返回limit+1，store裁剪并生成next。固定数据下确定性无重复漏项；实时窗口跨页不冻结snapshot，新事件可改变last_seen，刷新从首页读取，不提供跨请求快照保证。
5. GET `/api/topology/nodes/{instance_id}/incidents`，operationId=listNodeAccountQualityIncidents。参数status(active默认)、provider、failure_class(仅四类)、limit、cursor；response instance_id/items/next_cursor，item为node_id/account_key/provider/failure_class/status/first_seen/last_seen/hit_count/last_success_at。cursor base64url JSON最大8192，绑定Node/status/provider/failure filter/last_seen/account_key/row failure class；时间保留微秒，key opaque最大385bytes，错配非法400。401/403沿用super_admin，Node不存在404，Inventory/DB失败503不能empty，no-store/request-ID，5s context可取消。
6. 00023只增加SECURITY DEFINER/STABLE/search_path=pg_catalog/migrator owner/PUBLIC revoke/runtime EXECUTE函数，runtime不增加direct SELECT。Down只drop新签名，测试比对原表/index/数据/函数不变。不修改旧migration。若100accounts/10000events测试超过合理本地1s预算，诊断后报告blocker，不新增index/persistence。
7. Topology Node detail新增Incidents独立区域，固定Active，Provider复用当前Node provider集合，Reason四类+All，默认All。七列Account/Provider/Reason/Status/Hits/First Seen/Last Seen，UTC时间，Active文字+醒目badge；点击账号只把精确account_key传给已有History，不新建详情页。row key=account_key+failure_class（Node组件scope）。loading/empty/unavailable/populated分离，无resolve/disable/retry request/ack/mutation；可用现有读取失败重试惯例。Node切换remount/cancel、清History选择，迟到成功或401不污染新Node。filters变更清cursor，History同Node可保留选择。
8. make generate刷新Go/TS并补tools OpenAPI expected-operation清单，避免新增GET漏验收。独立PG/API/race/frontend及make test build。只更新runbook，不新增audit、日志identity或metrics；源destructive-pop/no-ACK缺口不变，empty Incident不表示采集正常。

## Risks / Trade-offs

纯read model不保留episode或恢复状态；阈值消失不等于账号健康。Inventory完整key数组随账号数增长，测试100账号并覆盖首101之后账号；不引入任意容量上限或第二套membership。Last success只说明成功证据，不改变阈值。单statement保证本次读取一致，跨页允许新事件/retention变化。

## Migration Plan

未来发布先query-access migration再API/Web；回滚应用保留forward schema，Down仅隔离测试。实现已进入remote main，Architecture与Implementation Final Review均APPROVED；尚未deploy、尚未archive，评审证据补录及sequencing deviation见planning-validation.md。
