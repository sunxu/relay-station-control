## Why

阶段 4 已有 Inventory、Binding 和 Duplicate Ownership 后端真相，但缺少 Node-centric 只读观察页面。原三项契约问题已获Architecture Contract Final Review批准；P-READ独立Provider安全读取契约也已获Architecture Contract Final Approval。

## What Changes

- Topology 仅展示 Node、Inventory evidence、Gateway binding/resolution、current duplicate 与 history；移除候选选择、bind/rebind/unbind 表单、提交和确认流程，不新增 mutation responsibility。
- Current involvement 使用既有 `cross_node_duplicate_occurrence_nodes`；historical involvement 使用 append-only evidence 的 `EXISTS`，新增明确命名的只读 history query/API，不改变既有 `instance_id` current-membership filter。
- Provider 改为独立的 snapshot freshness 与 latest health 两个 badge，允许 fresh + degraded；`health_degraded` 不加入或改变 Duplicate Ownership eligibility。
- **BREAKING**：作为 existing Binding transport identity correctness prerequisite，Control 所有 Gateway Account ID read/write HTTP 字段统一为正 int64 的规范十进制字符串，generated Go boundary/TypeScript 为 string，DB/internal Go 保持 int64。原请求/响应 numeric JSON 不再兼容；兼容性方案、已查到的消费者和外部范围见 design。
- **0 persistence migration；P-READ 允许一个最小 additive readonly query-access migration。** 仅新增独立Provider-state SECURITY DEFINER只读函数和runtime EXECUTE grant，不新增表/列/物化视图/summary persistence，不扩大runtime原表SELECT。`expected_binding_id`扩展仍不在范围。
- Architecture Contract 已 Final APPROVED，用户已授权实施；实现与验收进行中，不包含生产发布。

## Capabilities

### New Capabilities

- `node-centric-topology-ui`：只读 Node 视图、current/history 区分、Provider freshness/health 双维度及独立安全Provider-state读取、历史只读读取边界、数据隔离和恢复。

### Modified Capabilities

- `relay-node-gateway-account-binding`：现有 HTTP identity 表示统一纠正为 decimal string；不新增 action、前置条件、事务、审计或 mutation UI。

## Impact

- **阶段/仓库**：Control 阶段 4 只读 UI；实现文件变更限Control仓库内本change批准范围。Gateway Directory source v1的numeric JSON→Go int64→persistence明确保持现状；source-v2仅列独立未来架构事项。系统边界依据 v1.8/R4.7 §15、§24.3–24.4 与 ADR-0001/0002。
- **OpenAPI/生成物**：新增history read operation 和 Provider evidence DTO只读接口；六个现有 Account ID schema 字段及全部嵌套 read/write surfaces 统一 string。API与sqlc生成物通过项目生成流程同步。
- **DB/sqlc**：history query 读取已有 evidence，无新增表、索引或函数 migration；DB account ID 和内部 audit JSONB 数字不变。P-READ允许一个仅安装readonly function及EXECUTE权限的additive query-access migration；既有account query签名不变，不允许应用以migrator身份读取或绕过权限。
- **UI**：保留认证导航、独立 loading/unavailable/empty、分页、UTC 来源时间和账号清单 deep-link。当前没有 existing Binding 管理 UI，因此不展示其链接，不创建替代 mutation 页面。
- **Audit/metrics**：Topology 读请求不执行 binding/ownership 写入；既有账号清单的 view audit、Binding 审计、低基数 metrics 和凭据禁泄露规则不变。
- **兼容/回滚**：明确提出 forward contract correction，统一发布匹配的 API 和生成客户端，不设置 number|string 双轨。已知源码消费者见 design；仓库外消费者状态未验证，发布前必须清点。回滚匹配的 API/Web 版本，不降库、不改历史。
- **非目标**：绑定管理 UI、新增 bind/rebind/unbind、expected_binding_id、自动匹配、自动修复、调度/部署/容量功能、第二套 history truth、修改 eligibility、persistence migration、原表SELECT扩权、生产发布或实现提交。
