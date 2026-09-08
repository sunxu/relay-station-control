## Context

账号清单页已有 Node 选择、筛选、分页和受控查询流程；Topology 等只读入口可以把 `instance_id` 放入 URL。需要只调整页面初始化时序，使这个上下文在首次进入时可用。详见 proposal.md 与现有 `account-inventory-readonly-query` 规范。

## Goals / Non-Goals

**Goals:**

- 将 URL 中的 `instance_id` 解析为一次性的初始 Node 选择上下文。
- 使用 URL 中的初始 Node，用默认筛选触发现有第一页查询，并复用现有 loading、结果和错误状态。
- 保持后续筛选、Node 切换、分页的显式操作语义与 cursor 重置规则。

**Non-Goals:**

- 不增加或修改 HTTP endpoint、OpenAPI schema、Go handler、数据库查询、migration、审计或生成客户端。
- 不把 URL 筛选参数扩展为自动提交机制；不自动提交 email 或其他筛选。
- 不新增重试器、轮询、补采或任何 binding/account mutation；页面继续 read-only。

## Decisions

1. **一次性初始化标记。** 页面读取浏览器 URL 的 `instance_id`，在挂载后的可取消 microtask 中触发一次默认查询，不依赖分页 Node 列表加载。使用现有 query state 和请求函数，避免创建第二套数据源或 query contract。

2. **沿用既有 Node 校验。** URL 值作为初始选择直接交给现有查询流程；解析失败、未知 Node 或 capability 不足由既有 API 返回并沿用页面已有的 invalid/unsupported/unavailable 展示。页面不会把未知值映射到第一个 Node，也不新增前端平行校验。

3. **初始化与后续交互分离。** 自动查询只绑定首次进入；一旦管理员修改筛选、切换 Node 或分页，后续请求由现有事件处理器控制，清空 cursor 历史。筛选编辑不触发即时请求，避免把 URL 初始化逻辑误用为搜索联动。

4. **错误与安全复用。** 自动请求使用既有认证、CSRF/审计和错误映射；请求失败直接进入既有状态，禁止以空数组代替失败，也不自动循环重试。仍只访问 Control API 与 PostgreSQL 受控读模型，不调用 Node、Gateway 或外部数据面。

5. **无持久化与生成影响。** 本变更只涉及 Web 页面初始化状态/时序，不改变 API 或类型，不新增 migration；按项目要求执行 make test build（包含生成检查），并执行页面专项测试和 OpenSpec 校验。

## Risks / Trade-offs

- [Risk] Node 列表与 URL 初始化存在竞态，可能在能力信息尚未到达前发起请求。→ 查询仍经过既有 API 的 Node/capability 校验；前端以一次性标记防止重复触发，失败按既有状态展示。
- [Risk] React effect 重新执行导致重复请求。→ 通过 effect cleanup 取消 StrictMode 丢弃挂载的 microtask，测试严格断言首次只发一次默认请求。
- [Risk] 历史链接带有无效 UUID。→ 复用现有输入校验和 fail-closed 状态，不回退其他 Node、不返回空成功结果。

## Migration Plan

无需 migration、OpenAPI 变更或运行时配置变更。发布 Web 资源后，旧的不带 `instance_id` 链接保持手动查询；回滚只需恢复上一版 Web bundle，不涉及数据库回滚。

## Open Questions

无。
