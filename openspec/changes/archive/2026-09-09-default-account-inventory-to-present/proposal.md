## Why

Phase 3 账号清单默认显示所有生命周期，账号迁出 Node 后的 missing 记录容易被误认为仍在当前 Node。管理员要求默认只看当前账号，并保留查看缺失记录的入口。最初仅影响 Control Web；后续同 change 扩展包含下述只读 API/query-access。

## What Changes

- 生命周期初始筛选为 present；深链接首次查询和手动首次查询均使用该值。
- 保留全部生命周期选项，清空筛选可查看全部记录，选择 missing 可查看确认缺失记录。
- 保持后续编辑显式查询、cursor 重置、错误恢复和只读职责。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `account-inventory-readonly-query`：账号清单默认 present，可显式切换其它生命周期或全部。
- `node-centric-topology-ui`：Account Quality 默认 present，增加可选只读生命周期过滤并保持旧调用兼容。

## Impact

账号清单部分仅 Web 状态、测试和 OpenSpec；Topology 扩展的 API/query-access 影响见下节。metrics、audit、Node/Gateway 数据面及 Inventory truth 不改变。不删除 missing 记录；账号清单部分无需数据库迁移。沿用当前认证与查看审计，回滚 Web bundle 即可。系统边界沿用 System Design v1.8 / R4.7 及 ADR-0001/0002。

## Topology Account Quality scope extension

同一默认当前账号需求也适用于 Node Topology 的 Account Quality。页面新增生命周期筛选，默认 present，清空为全部并可选择 missing/suspected_missing/out_of_scope；早期实现沿用当时 Quality 即时筛选；后续统一 Topology change 已改为编辑筛选清空结果/cursor、点击查询后提交，当前以该显式交互为准。

GET account-quality 新增可选 lifecycle 参数；省略保持全部账号的既有 HTTP 兼容语义，不改变 Quality response。cursor 必须绑定 lifecycle，筛选不匹配返回400；既有无 lifecycle cursor 仍适用于全部查询。

为保证过滤发生在分页之前，本 change 已新增最小 additive readonly query-access migration 00024，定义 control_query_node_account_quality_v2；在原组合读逻辑中将生命周期交给现有 Inventory 安全查询，复用原单账号统计函数。保留 v1 签名与行为。SECURITY DEFINER/STABLE/fixed pg_catalog/migrator owner/PUBLIC revoke/runtime EXECUTE only；Down只删除v2函数。无新表、列、index或事件持久化变化。API、store wrapper及generated Go/TS需同步。

Inventory truth、History membership、Incidents、collector、retention、taxonomy和数据面均不变。Unknown仍为当前筛选中零请求账号，Unavailable不得转换为空。发布需先应用forward query-access migration再部署Control；回滚保留forward schema，旧v1仍可用。

## Subsequent implementation and final review

本 change 的实现提交为 `5065a5d`。之后已归档的 `consolidate-account-inventory-into-topology` 将账号 UI 统一到 Topology：旧 AccountInventoryView 已删除，Node 首次选择/切换自动默认 present 首页，编辑筛选只清空结果和 cursor、等待显式查询。无 Node 不查询，缺失与全部入口继续保留。

v2 是本 change 当时新增的只读查询函数；后续统一账号读取已使用 v3 和组合 POST，原 GET 的省略 lifecycle=all 兼容语义及 v1/v2 函数继续保留。v3 不由本 change 新增，归档不得恢复旧页面、即时筛选或旧 store 调用。当前 canonical delta 已基于后续规范对齐。

2026-09-09 Architecture + Implementation Final Review 均 PASS，9/9 tasks 完成，无代码 blocker；归档收口仅更新文档，不新增 migration 或修改运行行为。具体证据及时间线见 planning-validation.md。
