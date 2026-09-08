## Context

早期实现复用 AccountInventoryView 已有 lifecycle 参数、生命周期下拉框和 query mutation；服务端不修改默认过滤。

## Decisions

- 将页面 useState 初始 lifecycle 设为 present。可取消的初始 microtask 自动查询继续复用 execute，所以深链接默认也是 present。无 instance_id 不自动查询。
- 既有下拉框保留 present、suspected_missing、missing、out_of_scope；allowClear 清空为 undefined，显示“全部生命周期”，下次显式查询不传 lifecycle。
- 早期独立页面修改筛选、切换 Node 清空结果与 cursor且不即时查询；当前统一 Topology 的 Node 切换自动默认首页，筛选编辑仍等待显式查询。StrictMode、分页、失败恢复沿用既有机制。重新进入页面回到 present，不持久化筛选。
- 账号清单部分仅是展示默认值，不改变账号生命周期、时间语义、SQL、事务、审计或数据面。missing 记录继续保留；不新增删除或 mutation。

## Risks / Trade-offs

默认不再展示缺失记录；既有生命周期选项和清空入口可显式查看。用测试证明默认 present 与 missing/全部查询都正确，避免只在 UI 隐藏行导致分页不一致。

## Migration Plan

账号清单部分 0 migration、不需要生成文件变更；Topology 扩展见下节。发布包含新 Web 的 Control 镜像生效；回滚旧 Web 即恢复旧默认值。

## Topology Account Quality scope extension

同一默认当前账号需求也适用于 Node Topology 的 Account Quality。页面新增生命周期筛选，默认 present，清空为全部并可选择 missing/suspected_missing/out_of_scope；早期实现沿用当时 Quality 即时筛选；后续统一 Topology change 已改为编辑筛选清空结果/cursor、点击查询后提交，当前以该显式交互为准。

GET account-quality 新增可选 lifecycle 参数；省略保持全部账号的既有 HTTP 兼容语义，不改变 Quality response。cursor 必须绑定 lifecycle，筛选不匹配返回400；既有无 lifecycle cursor 仍适用于全部查询。

为保证过滤发生在分页之前，本 change 已新增最小 additive readonly query-access migration 00024，定义 control_query_node_account_quality_v2；在原组合读逻辑中将生命周期交给现有 Inventory 安全查询，复用原单账号统计函数。保留 v1 签名与行为。SECURITY DEFINER/STABLE/fixed pg_catalog/migrator owner/PUBLIC revoke/runtime EXECUTE only；Down只删除v2函数。无新表、列、index或事件持久化变化。API、store wrapper及generated Go/TS需同步。

Inventory truth、History membership、Incidents、collector、retention、taxonomy和数据面均不变。Unknown仍为当前筛选中零请求账号，Unavailable不得转换为空。发布需先应用forward query-access migration再部署Control；回滚保留forward schema，旧v1仍可用。

## Subsequent implementation and final review

本 change 的实现提交为 `5065a5d`。之后已归档的 `consolidate-account-inventory-into-topology` 将账号 UI 统一到 Topology：旧 AccountInventoryView 已删除，Node 首次选择/切换自动默认 present 首页，编辑筛选只清空结果和 cursor、等待显式查询。无 Node 不查询，缺失与全部入口继续保留。

v2 是本 change 当时新增的只读查询函数；后续统一账号读取已使用 v3 和组合 POST，原 GET 的省略 lifecycle=all 兼容语义及 v1/v2 函数继续保留。v3 不由本 change 新增，归档不得恢复旧页面、即时筛选或旧 store 调用。当前 canonical delta 已基于后续规范对齐。

2026-09-09 Architecture + Implementation Final Review 均 PASS，9/9 tasks 完成，无代码 blocker；归档收口仅更新文档，不新增 migration 或修改运行行为。具体证据及时间线见 planning-validation.md。
