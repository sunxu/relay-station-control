# Node-centric Topology UI

Topology 是已认证管理员使用的只读 Node 观察页，入口为 `/topology?instance_id=<UUID>`。页面只读取 Node 资产、Binding/resolution、Provider snapshot/health 和 Duplicate Ownership current/history；不会 bind、rebind、unbind、修改账号或触发数据面动作。

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

`00017_account_inventory_provider_state_query_access.sql` 是唯一新增 migration：0 persistence migration；P-READ 允许一个最小 additive readonly query-access migration。函数为 SECURITY DEFINER / STABLE，固定 `search_path=pg_catalog` 和 UTC，owner 为 migrator，PUBLIC 无 EXECUTE，仅 runtime 获得 EXECUTE；runtime 对 provider_states 仍无直接 SELECT。产品使用 runtime 连接，不得切换 owner 连接处理读取失败。

Up 只创建函数并设置 owner/EXECUTE ACL。Down 只 DROP 此 uuid 签名函数，默认 RESTRICT，不使用 CASCADE；只在隔离验收中验证 Down。生产回滚成对回滚 API/Web 并保留 forward schema，不执行 destructive down。函数缺失、权限不足或查询失败时，新 Provider API 返回 503，页面显示 unavailable；不得替换为 providers=[]。

Provider 读取、账号清单、Binding 和 Duplicate 各自沿用独立 read model；`control_query_current_account_inventory_v1` 的签名、函数体及账号分页不变。History 通过 append-only occurrence evidence 的 EXISTS 查询历史涉及，页面的 affected Nodes 始终标作 current membership；历史涉及不证明历史 confirmed owner。

Gateway Directory source v1 继续 numeric JSON → Go int64 → 原 persistence；本 change 不引入 source v2。只有 Control/Web HTTP 边界统一 decimal string。验收命令和证据见 [planning-validation.md](../../openspec/changes/add-node-centric-topology-ui/planning-validation.md)。
