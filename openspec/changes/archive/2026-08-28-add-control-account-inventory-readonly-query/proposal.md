## Why

阶段 3 已经建立 `account_inventory` 当前生命周期真相和仅供内部 Store 使用的有界读取函数，但运维人员仍无法在 Control 产品界面确认账号属于哪个 Relay Node、当前基础状态、连续缺失阶段或快照是否过期。继续依赖数据库直查会绕过实名管理员认证、邮箱查看审计、稳定分页和字段白名单，也会诱使后续告警或历史功能各自重新解释 lifecycle。

现在需要先交付一个窄而完整的只读查询切片：只从 Control PostgreSQL 当前状态读取，不请求 Node/Gateway，不修改采集、promotion 或 lifecycle，并把完整邮箱的展示和筛选放在可审计、不可缓存且不进入 URL 的管理边界内。该切片为后续基础告警、历史压缩和日级趋势提供可复用的产品读取模型，但不提前实现这些能力。

## What Changes

- 新增只接受已认证、已启用 `super_admin` 会话的 `POST /api/account-inventory/query`；使用请求体承载 Node、Provider、lifecycle、basic status、精确 email、limit 和 opaque cursor，避免 email/cursor 进入 URL、代理访问日志或浏览器历史。
- `instance_id` 为必填作用域；服务验证 Node 已登记且声明 `management.account_inventory` capability。未知 Node 返回 404，不支持该能力返回固定 409，均不调用外部系统。
- 新增受控 PostgreSQL 只读函数和 sqlc/Store adapter，从 `account_inventory`、`account_inventory_provider_states` 与资产注册表返回严格字段白名单；按不可变 `account_key` keyset 排序，最多返回 100 条，不开放任意表 SELECT。
- 新增管理员绑定、筛选绑定、15 分钟有效且 AEAD 加密的 opaque cursor；cursor 内部可以携带继续读取所需的 `account_key`，但 API、日志、错误、浏览器可见状态和审计不得出现其明文。
- 返回当前账号的 normalized email、Provider、Node、最后报告基础状态、lifecycle、首次/末次出现、missing/out-of-scope 时间、刷新/重试时间，以及 Provider 当前完整快照时间、degraded 与 `fresh|stale|out_of_scope` 新鲜度；不返回 poll/policy ID、内部 account key、版本/提交、原始计数桶或未知源字段。
- 每一页完整邮箱结果在返回前，必须在同一数据库事务写入实名管理员 `account_inventory.view` 审计；审计只保存 Node、是否使用各类筛选、结果数量和 request ID，不保存 email、account key、cursor 或筛选摘要。审计提交失败时不返回结果。
- 新增账号清单 React 页面：先选择具备 capability 的 Node，再按 Provider、lifecycle、basic status 和精确 email 筛选，使用前后页 cursor 历史，明确显示最后报告状态、Provider degraded、快照 stale 和 out-of-scope；各查询独立显示 loading/empty/error，不提供导出或修改入口。
- 更新 OpenAPI 3.1、生成 Go/TypeScript 客户端、认证审计枚举/allowlist、Runbook 和脱敏验收证据。
- 不新增账号详情、批量导出、模糊/前缀 email 搜索、人工状态编辑、删除、补采、promotion、告警、HMAC 指标身份、10 分钟/1 小时趋势、历史摘要/压缩或跨 Node 重复归属检测。

## Capabilities

### New Capabilities

- `account-inventory-readonly-query`: 定义 current account inventory 的管理员只读 API、授权与审计、字段白名单、敏感筛选、加密 cursor、稳定分页、快照新鲜度和 React 页面行为。

### Modified Capabilities

- `account-inventory-lifecycle`: 将原来仅限内部 Store 的 lifecycle 读取边界扩展为受认证、逐页审计且有界的产品只读查询，同时继续禁止任意数据库读取、产品写操作和数据面耦合。

## Impact

- **阶段与结果**：阶段 3；实名 `super_admin` 首次可以在 Control 中按 Node 查询当前账号清单并定位 present、连续缺失或 out-of-scope 状态，所有完整邮箱页面读取均可审计。
- **仓库**：只修改 `control`。`ops` 系统设计 v1.0 第 9.4、9.5、12.1、15.3、21、23、24.1 节和 ADR-0001 是输入真相源；不修改 `ops`、Gateway 或 Node 产品代码。
- **OpenAPI/生成客户端**：修改 `api/openapi.yaml`，增加 `account-inventory` tag、query operation、封闭 enum 与请求/响应 schema；运行 `make generate` 刷新 Go server types 和 TypeScript client，生成文件不得手工编辑。
- **Migration/sqlc**：新增 additive forward Goose Migration，创建版本化只读函数、必要索引与最小 EXECUTE 权限；不增加账号身份副本、不回填/改写 lifecycle。新增 sqlc 查询和 Store adapter；旧内部 lifecycle 读取函数继续保留。
- **认证/审计**：复用 session、固定 `super_admin`、CSRF 和现有不可变 `audit_logs`；增加 `account_inventory.view` action/category allowlist。查询与审计同事务，审计不可用时 fail closed；未授权/CSRF 拒绝沿用现有安全审计。
- **UI**：新增账号清单页面、API hooks/types、导航和响应式表格；不新增导出、复制专用操作、详情页或 mutation 控件。
- **指标/日志**：只增加低基数 HTTP query count/latency/error 观测；不得把 email、account key、cursor、filter hash、poll/policy ID、版本/提交加入日志或指标。本 change 不实现 `metric_identity_secret` 或逐账号 Prometheus 指标。
- **兼容性与数据面**：现有 API/UI 兼容；新路由为 additive。查询只读取本环境 Control PostgreSQL，不增加 Node/Gateway/互联网请求，不影响 Scheduler、Worker、promotion、Gateway 或 Relay Node 模型流量。
- **安全与回滚**：响应统一 `Cache-Control: no-store`；敏感筛选使用 JSON body，加密 cursor 由环境认证 keyring 派生独立 domain。应用回滚时移除新 API/UI，保留 additive Migration、函数、索引和审计历史；生产不执行 destructive down。
