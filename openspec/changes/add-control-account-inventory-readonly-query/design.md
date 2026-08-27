## Context

`account_inventory` 已经由 lifecycle-aware fenced finalize 维护，并通过 `(instance_id, account_key)` 保存当前身份、最后报告基础状态、lifecycle 和来源时间。`account_inventory_provider_states` 保存 Provider 当前监控范围、最后完整 promotion 时间和 degraded 状态。现有 `control_list_current_account_inventory_lifecycle` 只允许内部 Store 按一个 instance、可选 Provider/lifecycle 和 account-key cursor 有界读取；产品 OpenAPI、生成客户端和 React 路由刻意没有接线。

本 change 只把这份当前真相安全投影给实名管理员。完整 email 是内部敏感信息，但系统设计要求已认证 `super_admin` 能展示并筛选，同时每次完整邮箱查看与筛选都留下操作者和时间证据。普通 GET query 会把 email 和可能含身份 continuation key 的 cursor 放入 URL、浏览器历史、反向代理 access log 与排障采样，因此不适合作为此接口载体。

当前部署仍是单环境、单 Control、单 PostgreSQL；数据库是查询和审计的唯一真相源。页面不是数据面组件，不能直接请求 Node、Gateway 或 Prometheus，也不能改变 poll/lifecycle 状态。

## Goals / Non-Goals

**Goals:**

- 为具备账号清单 capability 的单个已登记 Node 提供 authenticated、bounded、audited 的当前账号查询。
- 支持可选 Provider、lifecycle、basic status 和 normalized email 精确筛选。
- 保证分页 cursor 不向客户端可解码地暴露 account key/email，并不能跨管理员或筛选条件重放。
- 只投影账号页面所需字段，准确区分最后报告基础状态、账号 lifecycle、Provider degraded 和当前快照新鲜度。
- 在返回任何完整 email 前持久提交实名查看审计，失败时不泄露结果。
- 提供响应式、可访问、空态/错误态明确的 React 页面，并复用现有资产 Node 列表。

**Non-Goals:**

- 不提供跨 Node 无界查询、详情 endpoint、导出、批量复制、模糊/前缀 email 搜索或保存的筛选视图。
- 不修改 lifecycle、Provider scope、snapshot、poll run、告警或 Node/Gateway 配置。
- 不计算 10 分钟/1 小时成功率、质量分、日级覆盖率、历史趋势、压缩或保留清理。
- 不生成 HMAC `account_id` 或逐账号 Prometheus series；这些属于后续指标/告警 change。
- 不从 snapshot items 动态重建当前状态，不读取原始 Node 响应，不新增外部请求。

## Decisions

### 1. 使用 POST query，敏感筛选不进入 URL

新增：

```http
POST /api/account-inventory/query
Cookie: control_session=...
X-CSRF-Token: ...
Content-Type: application/json
```

请求体固定为：

```text
instance_id    required UUID
provider       optional normalized provider exact match
lifecycle      optional present|suspected_missing|missing|out_of_scope
basic_status   optional reported_active|disabled|unavailable|error|unknown
email          optional trim+lower exact match, max 320 Unicode code points/bytes under existing identity rule
cursor         optional opaque encrypted token
limit          optional, default 50, min 1, max 100
```

POST 是只读语义，但继续经过 unsafe-method CSRF middleware。所有响应包含 `Cache-Control: no-store` 和 request ID。请求 body、cursor 和响应 body不得进入 access/application log。非法 email/provider/enum/cursor 返回固定 400 error code，不回显输入；未知 Node 返回 404；已登记但未声明 `management.account_inventory` 返回 409。服务不会为这些状态探测 Node。

备选 GET 被拒绝，因为 email 与 cursor 会进入 URL。备选“GET 浏览、POST email 搜索”会产生两套分页和审计行为，增加绕过风险，因此统一为一个 POST query。

### 2. 查询严格限定一个 Node，并只读当前 lifecycle

`instance_id` 必填。Handler 先在数据库事务中确认资产存在、环境匹配且 Driver capability 包含 `management.account_inventory`，再调用新版本化只读函数 `control_query_current_account_inventory_v1`。函数仅访问：

- `account_inventory`：当前账号字段白名单；
- `account_inventory_provider_states`：monitoring status、last complete 和 degraded；
- `relay_node_assets`/capability 注册：作用域资格验证和页面 Node 名称所需稳定元数据。

函数接受 normalized filters、解密得到的 `after_account_key` 和 `limit + 1`，按 `account_key ASC` keyset 读取。filter 在 cursor 比较前应用。API 取前 `limit` 行并仅在存在额外行时生成 `next_cursor`。账号在分页期间可能被后续 promotion 更新，但 account key 不变；本 change 不声称跨页 snapshot isolation，也不冻结状态版本。稳定 keyset 保证同一筛选下不会因状态更新时间排序而重复/跳跃既有 key；并发新账号是否出现在后页取决于其 key 位置，这是 current view 的明确语义。

不提供跨 Node 查询，避免复合身份 cursor、一次请求枚举全环境 email 和跨 Node 结果不一致；页面先从既有 assets API 选择 capability Node。阶段 3 后续若需要全环境查询，必须另行评审审计量、cursor 和身份暴露面。

### 3. 新只读函数和索引使用 additive Migration

新增下一号 forward Migration：

- 创建 `control_query_current_account_inventory_v1(uuid,text,text,text,text,text,integer)` 或等价固定签名，只返回 schema 白名单；设置固定 owner、`SECURITY DEFINER`、空/固定 `search_path` 和输入上限。
- 只向 Control runtime role 授予该函数 EXECUTE，不授予 `account_inventory`、Provider state 或资产表的任意 SELECT/DML。
- 保留 lifecycle foundation 的内部函数和权限，避免 poll/metrics 回归。
- 按实际 `EXPLAIN (ANALYZE, BUFFERS)` 增加 instance/account-key、精确 normalized-email 和常用封闭筛选所需索引；不为任意组合盲目创建宽索引。
- 函数以数据库约束验证 instance、filter、after key 和 limit；非法或不一致状态 fail closed，不返回部分行。

email/account key 已存在于受保护表；Migration 不新增身份列、不复制数据、不回填或重写 lifecycle。sqlc 源只调用受控函数。数据库和应用 SQL 参数日志必须保持关闭/脱敏，测试与 Runbook 明确验证 email 参数不会进入最终日志。

### 4. cursor 使用 AEAD，不使用可解码 HMAC payload

现有资产 cursor 的明文 payload 只含非敏感 instance UUID；账号 cursor 的 continuation key 等于 `provider:email`，不能复用该格式。新增独立 `AccountInventoryCursorCodec`，从现有环境认证 keyring 以专用 domain 派生 AEAD key。token 使用随机 nonce 和 URL-safe base64，仅暴露版本、key version、nonce 与 ciphertext。

加密 payload 包含：

```text
version
key_version
actor_admin_id
instance_id
normalized_filter_hash
after_account_key
issued_at
expires_at = issued_at + 15 minutes
```

AEAD additional authenticated data 固定包含产品/domain/version/environment identity。Decode 必须验证结构、长度、key version、认证 tag、当前管理员、instance、规范化筛选 hash、15 分钟有效期和 after key 形态；任一失败统一返回 invalid cursor。cursor 不作为授权凭据，当前 session、管理员 enabled 状态和 CSRF 仍每次重新校验。Key rotation 只在 keyring 仍保留旧 key 且 token 未过期时允许短期续页。

页面仅在内存 state 保存当前页 cursor 栈，不写 localStorage/sessionStorage、URL、analytics 或错误报告；刷新页面回到第一页。

#### 敏感数据流与允许位置

```text
email filter: UI memory -> HTTPS POST body -> Handler/Store DTO -> protected SQL parameter
email result: protected SQL column -> Store/API response DTO -> authorized UI memory/DOM
account key: protected SQL row -> internal continuation DTO -> AEAD plaintext -> opaque ciphertext in UI memory
```

只有上述箭头和位置获准。email、account key、filter hash 与 cursor 不进入 URL、redirect/Location、普通日志、指标标签、audit details、浏览器 history/localStorage/sessionStorage 或保留测试 artifact；account key 不进入产品 OpenAPI/TypeScript schema。错误路径在序列化前丢弃内部 DTO，并只输出固定 code 与 request ID。

### 5. 返回字段区分账号状态与 Provider 新鲜度

每条 API item 只返回：

```text
instance_id
provider
email
basic_status
lifecycle
consecutive_missing_count
first_seen_at
last_seen_at
missing_since
out_of_scope_since
last_refresh_at
next_retry_at
source_updated_at
provider_last_complete_at
provider_degraded
snapshot_freshness = fresh|stale|out_of_scope
```

不返回 `account_key`、current poll run、policy version、Node version/commit、success/failed totals、recent bucket、endpoint、Secret ref 或原始错误。`basic_status` 明确标为“最后报告基础状态”；suspected/missing 不把旧 basic status解释为当前可调度。`provider_last_complete_at` 来自 Provider 当前状态，不使用账号 `last_seen_at` 冒充快照时间。

对 active Provider，数据库 `clock_timestamp() - last_complete_at` 超过配置的初始 15 分钟阈值时为 stale，否则 fresh；Provider 最新失败/不完整可以同时 `provider_degraded=true`，两者在 UI 分别展示。out-of-scope 行固定为 out_of_scope，不导出 fresh/stale 判断。生命周期行缺少对应 Provider state、时间或状态组合时整个 query 返回固定 503 inconsistent-state，不静默填默认值。

### 6. 完整 email 返回前审计必须提交

新增固定 audit category/action（名称最终与现有枚举风格一致，action 固定为 `account_inventory.view`）。Handler 在同一个短数据库事务中：

1. 验证 Node/capability；
2. 执行有界 query 并构造白名单结果；
3. 插入实名 actor、source fingerprint、request ID 和 allowlist details；
4. commit；
5. 只有 commit 成功后序列化/返回响应。

details 只允许：`instance_id`、`provider_filter_used`、`lifecycle_filter_used`、`basic_status_filter_used`、`email_filter_used`、`cursor_used`、`result_count`。不得保存 filter 值、email、account key、cursor、filter hash 或结果 identity。空结果和后续页也审计。审计 insert/commit 失败返回 503 且不返回 items；commit 成功后客户端断开仍保留审计，这是保守的“可能已查看”证据。无效请求未取得结果，不写 view audit；认证、授权和 CSRF 拒绝继续由现有安全审计记录。

查询不修改 lifecycle/snapshot/poll。唯一写入是不可变 audit log，因此重复请求会产生独立查看证据，不能使用 idempotency 去重。

### 7. React 页面复用资产选择并保持敏感状态短命

新增账号清单 route/page、生成 client wrapper、TanStack Query hook 和响应式表格。页面：

- 只列出声明 `management.account_inventory` 的 Node，必须选择一个 Node 后才查询账号；不支持能力的 Node 隐藏账号视图。
- Provider、lifecycle、basic status 和精确 email 过滤变化时清空 cursor 历史并回到第一页。
- email 输入仅保存在组件内存；query 完成后可以保留当前筛选以便管理员定位，但路由切换/unmount 清除，不持久化。
- 显示完整 email、Provider、Node、最后报告基础状态、lifecycle、last seen、刷新/重试、Provider last complete、degraded/stale；out-of-scope 使用独立视觉状态。
- 不提供导出、批量选择、复制按钮、详情 drawer、编辑、删除、补采或 promotion 控件。
- 桌面与窄屏均可使用，所有筛选、状态和分页按钮有可访问名称；loading、empty、400、401、409、503 独立呈现且错误不回显敏感输入。

### 8. 观测保持低基数并验证零外部请求

允许增加 query request count、latency、result bucket 与固定 error code 指标，标签只含 operation/result/error_code；不得使用 instance、Provider、email、account key、cursor、actor、request ID 或 filter value。普通日志只记录固定 operation/result/error_code、request ID 和布尔 filter-used；不记录 item、DTO formatter、SQL 参数或响应。

API/Store/UI 测试注入不同 email、account key、cursor、endpoint、Secret、poll/policy ID、版本/提交和 raw error canary，扫描日志、指标、错误、审计 details、测试报告、浏览器 storage 与 URL。API 响应中的授权 email是唯一允许的外部 canary位置；数据库受保护 identity 列与加密 cursor ciphertext 是允许位置。

网络计数验收必须证明 query/page load 不调用 CLIProxyAPI、Gateway、Prometheus、模型数据面、任意 URL或 Node 写接口。Control/PostgreSQL停止只使管理查询不可用，Gateway/Relay Node 流量保持不变。

### 9. OpenAPI、生成链与回滚

`api/openapi.yaml` 是唯一产品契约，新增 tag、request/response、enum、error 和 `no-store` 定义后运行 `make generate` 两次，第二次必须零差异。Handler 实现生成的 strict interface；前端只消费生成 TypeScript type，不手写漂移 schema。

部署顺序为 additive Migration/权限与新二进制一起发布，启动 compatibility check 在注册 route 前确认新只读函数和 EXECUTE 权限。兼容检查失败时账号 query route fail closed/返回 503，现有认证、资产、任务和 poll/lifecycle 继续运行。回滚应用移除 route/UI并保留函数、索引和 audit rows；旧应用不调用新函数。生产不执行 down；受保护 down 只允许确认没有后续依赖时撤销 EXECUTE并删除只读函数/专用索引，不删除账号或审计数据。

## Risks / Trade-offs

- **完整 email 进入浏览器**：这是系统设计要求的管理员定位能力。通过固定 super_admin、no-store、POST body、短命内存状态、逐页审计、无导出和 canary 门禁缩小暴露面。
- **查询为了审计变成写事务**：audit 不可用会使只读页面不可用，但避免出现无法追踪的完整邮箱读取；事务保持短小且不锁 lifecycle 行。
- **实时分页不是冻结快照**：promotion 可在翻页时更新状态或插入账号。不可变 keyset 避免按更新时间翻页的重复；冻结全结果会放大存储和身份副本，因此本 change 接受 current-view 语义。
- **精确 email 搜索可确认身份存在**：只有同环境实名 super_admin 可使用且每次搜索被审计；不提供模糊搜索、跨 Node查询或匿名差异响应。
- **AEAD cursor 增加 key 管理复杂度**：复用现有环境 keyring并使用独立 domain，不引入新 Secret；15 分钟 TTL限制泄漏窗口。不能退化为含 account key 的明文/HMAC cursor。
- **审计先于 HTTP 交付**：commit 后连接断开可能留下未实际查看的记录，但这是安全侧保守误差，优于已查看却无审计。

## Migration Plan

1. 新增 OpenAPI delta、Migration、受控查询函数、索引、权限、sqlc 和 cursor/audit类型；route 默认只有 compatibility check 通过才可用。
2. 在 PostgreSQL 18 隔离环境验证空/多 Provider/各 lifecycle、exact email、limit+1、cursor、并发 promotion、非法状态、权限矩阵和 `EXPLAIN (ANALYZE, BUFFERS)`。
3. 生成 Go/TypeScript 客户端并实现 Handler/React 页面；验证每页 audit commit 后才返回、audit 故障 fail closed、所有响应 no-store。
4. 运行敏感 canary、浏览器 storage/URL、日志/指标/审计/错误扫描和零外部请求验收；使用 1,000 个合成账号验证查询与索引预算。
5. 先执行 additive Migration，再部署新二进制；观察固定 query latency/error 与 audit 写入失败指标后开放导航。
6. 回滚时回退应用并隐藏 route/UI，保留 forward schema、索引和审计。恢复新版本后直接读取当前 lifecycle，不回放或重算历史。

## Open Questions

无。接口固定使用 POST body；instance 必填；email 只支持标准化精确匹配；每页结果都必须审计；cursor 使用管理员/筛选绑定的 15 分钟 AEAD；本 change 不提供导出、详情、趋势或告警。
