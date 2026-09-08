# Node-centric Topology UI

Topology 是已认证管理员使用的只读 Node 观察页，入口为 `/topology?instance_id=<UUID>`。页面只读取 Node 资产、Binding/resolution、Provider snapshot/health 、Duplicate Ownership current/history 和 Account Quality；不会 bind、rebind、unbind、修改账号或触发数据面动作。

## 当前实现边界

- Node 列表复用资产注册表读取，保留未绑定、没有账号或暂时无法观测的 Node。
- Provider 的 snapshot freshness 与 latest health 分开显示；`fresh + degraded` 等组合不合并成总健康状态。
- Duplicate current 使用现有 `instance_id` 过滤；History 使用独立历史涉及读取，不能把历史评估涉及误称为历史 owner。
- 页面中的 Gateway Account ID 必须按 Control 的 decimal string 契约处理。Binding 管理 UI 当前不存在，也不由 Topology 创建替代入口。
- 所有读取使用 `no-store`。401 清除会话；局部 503 显示 unavailable 和重试；失败不能伪装为空结果。

## 发布门禁

本仓库内目前没有发现 Topology 或其他 Web 页面之外的稳定 Binding HTTP 消费者；这不等于已确认仓库外不存在消费者。正式发布前必须完成活动 OpenSpec change 的消费者清点，确认 `/api/relay-bindings` 的 Account ID numeric → decimal string 是成对升级的 breaking contract，并保留现有 Binding 的认证、CSRF、事务和审计语义。

Binding 后端边界与回滚约束见 [`relay-node-gateway-account-binding.md`](relay-node-gateway-account-binding.md)。Topology 不拥有任何 Binding mutation responsibility。

## 验收

应覆盖登录/401、直接深链、浏览器前进后退、未知 Node、未绑定 Node、current 与 history 分离、ACTIVE/RESOLVED 分页、Provider 双 badge 和各自来源时间、局部 503、过期响应丢弃、390px/键盘可用性，以及网络捕获中不存在 bind/rebind/unbind 或其他业务写请求。


## 安全读取与 query-access migration

Provider Summary 使用 `public.control_query_account_inventory_provider_states_v1(uuid)`。集合是同一 statement timestamp 下的当前监控策略 Provider 与全部已持有 Provider state 的并集；不读 account rows 推导集合。0 account 的 Provider 仍可见，应监控但尚无 state 时显示 not-yet-observed，原 state、snapshot/health 时间和 health 值均为 null。

原 `add-node-centric-topology-ui` 的 `00017_account_inventory_provider_state_query_access.sql` 是该 change 唯一新增 migration：0 persistence migration；P-READ 允许一个最小 additive readonly query-access migration。函数为 SECURITY DEFINER / STABLE，固定 `search_path=pg_catalog` 和 UTC，owner 为 migrator，PUBLIC 无 EXECUTE，仅 runtime 获得 EXECUTE；runtime 对 provider_states 仍无直接 SELECT。产品使用 runtime 连接，不得切换 owner 连接处理读取失败。

Up 只创建函数并设置 owner/EXECUTE ACL。Down 只 DROP 此 uuid 签名函数，默认 RESTRICT，不使用 CASCADE；只在隔离验收中验证 Down。生产回滚成对回滚 API/Web 并保留 forward schema，不执行 destructive down。函数缺失、权限不足或查询失败时，新 Provider API 返回 503，页面显示 unavailable；不得替换为 providers=[]。

Provider 读取、账号清单、Binding 和 Duplicate 各自沿用独立 read model；`control_query_current_account_inventory_v1` 的签名、函数体及账号分页不变。History 通过 append-only occurrence evidence 的 EXISTS 查询历史涉及，页面的 affected Nodes 始终标作 current membership；历史涉及不证明历史 confirmed owner。

Gateway Directory source v1 继续 numeric JSON → Go int64 → 原 persistence；本 change 不引入 source v2。只有 Control/Web HTTP 边界统一 decimal string。验收命令和证据见 [planning-validation.md](../../openspec/changes/archive/2026-09-07-add-node-centric-topology-ui/planning-validation.md)。


## Account Quality 只读投影

选择 Node 后，Account Quality 默认显示15m窗口和 `present` 生命周期，可切换1h、Provider、Quality及生命周期过滤并分页。选择 `missing` 查看缺失记录，清空生命周期查看全部；这不改变 Inventory lifecycle。Provider选项复用该Node的Provider Summary。API为 `GET /api/topology/nodes/{instance_id}/account-quality`，使用现有super_admin会话，无账号操作。API省略lifecycle保持全部账号兼容语义；页面显式发送present。默认limit25、最大100，cursor绑定Node和filters（含lifecycle）；窗口/筛选/Node变更后从首页读取。

账号集合来自现有current Inventory安全函数，沿用其lifecycle集合和一致性检查，绝不从request events枚举。Inventory账号没有请求时仍显示Unknown；unresolved和只有event没有Inventory的账号不制造行。分类只表示窗口内真实请求：95%及以上Good，80%及以上且低于95% Degraded，低于80% Bad；0请求Unknown。Latency不参与分类。Inventory active与Quality Bad可同时成立，查看此表不改变Inventory、Binding、Duplicate或Node运行时状态。

Loading是请求进行中；Empty仅表示成功返回无账号/无匹配；Unknown是存在账号但窗口内无请求；Unavailable表示读取失败，应重试并检查Control/DB。不得把503转成No accounts或Unknown。Success/P95/最后事件无值显示—；Last Failure只显示现有类别和时间，无raw body。

新读取通过最小additive query-access migration调用既有Inventory及Account Quality函数，单次store数据库往返；内部仍逐账号复用统计，未新增事件表、索引、rollup或缓存。runtime只有function EXECUTE，不能直接SELECT account/request event/provider state表；产品不得使用owner连接绕过错误。原Inventory管理页的POST与view audit保持不变。

本change不改变usage collector启用开关、HTTP-only source、resolved/unresolved、7天retention以及destructive-pop/no-ACK丢失窗口，详见[Request Quality runbook](account-request-quality.md)。Quality Unknown不等于采集正常，采集未启用或没有事件都可能导致无请求证据。

未来发布需先应用additive function migration，再成对更新backend/Web。回滚应用即可停用此入口，生产保留forward schema；Down仅用于隔离测试且只能删除新增读取函数。本轮未部署，证据见[Account Quality view validation](../../openspec/changes/archive/2026-09-08-add-account-quality-topology-view/planning-validation.md)。

## Account Request History

在 Account Quality 行选择“查看 History”，使用该行的 canonical `account_key` 在当前 Node detail 读取最近七天具体请求。History 只解释窗口质量，不改变分类或任何账号状态。六列为 Time（UTC）、Model、Result、Failure、Latency、Request ID；空 latency/request ID 与成功事件的 Failure 显示 —。没有请求详情、raw body、导出或账号操作。

只读接口为 `GET /api/topology/nodes/{instance_id}/request-history?account_key=...`；账号参数应由 generated client 编码，保持 opaque string。只支持 limit（默认25，最大100）与 opaque cursor。使用现有 super_admin 会话、no-store 和5秒预算；cursor绑定 Node/account/time/hash，错配400。account_key仅在该认证API请求内传递，不加入浏览器导航URL或持久化前端状态。

账号必须仍在现有 current Inventory read model 中；不存在或只有events的账号返回404。NULL account_key事件不能归属到账号。数据库或Inventory读取失败返回503，页面显示Unavailable；仅成功读取且七天内无事件才Empty。未选择账号时不发请求；Node切换清除账号并取消旧请求，账号切换从首页开始。同一Node的Quality筛选/翻页可以保留所选History上下文。

`00022_account_request_history_query_access.sql` 仅增加 `control_query_account_request_history_v1(uuid,text,timestamptz,text,integer)` 与EXECUTE授权。函数在同一statement内验证Inventory并按DB时间读取 `occurred_at >= statement_timestamp()-interval '7 days'` 且不晚于当前时间的事件，排序time DESC/hash DESC，keyset取limit+1。单次应用数据库查询；复用已有表和索引，runtime仍不能direct SELECT。跨页不冻结snapshot，retention可能移除已过期事件。

该入口不启动collector，也不改变retention。HTTP queue仍是destructive pop/no-ACK；事件可能未被采到，因此空History不能证明账号从未收到请求，七天History也不是完整账本。限制沿用[采集runbook](account-request-quality.md)。未来发布先应用query-access migration，再更新API/Web；生产回滚保留forward schema，Down只在隔离测试中删除该函数。本轮不部署，证据见[Request History validation](../../openspec/changes/archive/2026-09-08-add-account-request-history/planning-validation.md)。

## Account Quality Incidents

Node detail 的 Incidents 只显示当前 Inventory 账号的重复失败模式：最近15分钟同 Node/account_key/failure_class 至少3次失败为 Active。auth、quota、rate_limit、upstream 分别计数；unknown/unresolved/event-only 不生成账号Incident。成功请求不抵消窗口内失败；低于阈值后从列表消失，不代表恢复。第一版不提供 Recovered、episode history 或持久状态。

First Seen/Last Seen/Hits 是当前15分钟该类别失败的最早/最晚时间和次数，并非账号生命周期累计。只读模型的 last_success_at 是同账号最近7天成功请求的最后时间，可能为NULL，不作为解除Active的条件。Inventory lifecycle、Quality分类、Binding及Duplicate均不受影响。

`GET /api/topology/nodes/{instance_id}/incidents` 只接受status=active（默认）、provider、failure_class、limit（默认25，最大100）、cursor。按last_seen DESC/account_key ASC/failure_class ASC过滤后keyset分页；cursor绑定Node和筛选。窗口实时计算，跨页有新事件时排序可改变，刷新应回首页。无session401、非管理员403、非法参数400、Node不存在404，DB/Inventory失败503不能显示为Empty。

页面七列表为Account/Provider/Reason/Status/Hits/First Seen/Last Seen；Provider/Reason过滤可回到首页。点击Account用canonical key打开已有Request History。Node切换取消旧读取并清除History选择；没有resolve、disable、请求retry、ack或其它账号操作。Empty仅表示当前筛选下没有达标Incident，采集未启用或destructive-pop/no-ACK丢失也会影响证据，不能将Empty当作健康证明。

`00023_account_quality_incidents_query_access.sql`只新增受控readonly function/ACL，产品使用runtime EXECUTE，不能direct SELECT。Inventory按既有安全函数完整分块读取，事件集合聚合与分页在单次数据库往返中完成；不新增表/index/worker/materialized view/cache/rollup，也不接入Durable Jobs。未来发布先应用query-access migration再更新API/Web；生产回滚保留forward schema，Down仅在隔离测试删除该function。Incidents实现已进入remote main，Architecture与Implementation Final Review均APPROVED；已archive、尚未deploy，交付时间线见[Incidents validation](../../openspec/changes/archive/2026-09-08-add-account-quality-incidents/planning-validation.md)。

当前 `default-account-inventory-to-present` 扩展尚未部署。发布需先应用 migration 00024 的只读 v2 查询函数，再更新 Control/Web；旧 v1 函数保留，回滚应用不执行数据库 Down。
