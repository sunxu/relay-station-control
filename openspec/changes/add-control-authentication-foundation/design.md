## Context

当前 `control` 只有单例环境 Migration、`/api/healthz`、生成链和嵌入式 React 骨架，没有身份、会话或审计模型。技术栈与安全方向已经由 v1.0 系统设计第 2.1、19、21.1、23、24.1 节和 ADR 0001 第 3.3、5、8.2 节固定；本设计只决定 `administrator-access` 规格的实现方式，动机见 `proposal.md`。

认证属于 Control 模块化单体内部能力，PostgreSQL 是身份、会话和审计真相源。Control 不参与 Gateway 到 Relay Node 的请求链路，也不持有 Gateway/Node 可登录到本 UI 的凭证。

## Goals / Non-Goals

**Goals:**

- 用数据库约束和事务保证 bootstrap、激活、恢复码、会话撤销和审计在并发、重启与重试下仍正确。
- 让所有后续 Control 管理路由复用同一认证、MFA、CSRF、固定角色和重新认证中间件。
- 让 Secret 只以运行时文件、加密密文或不可逆摘要存在，并建立可自动验证的脱敏边界。
- 维持 OpenAPI、Go/TypeScript 生成物、Migration 和 sqlc 的可重复生成。

**Non-Goals:**

- 不实现 RBAC、OIDC/SSO、WebAuthn、邮件或短信投递、双人审批和跨环境身份共享。
- 不在本 change 建设通用异步任务/Outbox、审计查询与清理 UI、资产/Node/Gateway 能力。
- 不让管理员认证状态、Redis 或指标后端成为请求数据面的依赖。

## Decisions

### 1. 使用单一 `auth` 模块和显式 HTTP 状态机

后端新增 `internal/auth` 领域模块，内部按 bootstrap、administrator、credential、mfa、session、authorization 和 audit 分包或文件组织；API handler 只负责 OpenAPI 输入输出、Cookie 和请求上下文转换。所有受保护路由通过统一中间件装载不可变 `AuthContext`：管理员 ID、固定角色、MFA assurance、会话 ID、创建/最后活动/绝对过期时间和最近重新认证时间。

OpenAPI 增加以下资源组，具体 schema 名称在编码时保持语义一致：

- `GET /api/bootstrap/status`：只返回 `required|in_progress|completed`，不返回管理员资料。
- `POST /api/bootstrap/start`、`POST /api/bootstrap/complete`、`POST /api/bootstrap/reset-pending`：均要求 bootstrap Secret；start 返回待确认的 TOTP `otpauth` 内容，complete 单次返回恢复码。
- `POST /api/auth/login`、`POST /api/auth/mfa`、`POST /api/auth/logout`、`GET /api/auth/session`、`POST /api/auth/reauthenticate`、`POST /api/auth/password`。
- `POST /api/admin-activations/complete`：使用一次性激活令牌完成密码与 MFA 设置。
- `GET /api/admins`、`POST /api/admins`、`POST /api/admins/{id}/disable`、`POST /api/admins/{id}/activation-token`、`POST /api/admins/{id}/mfa-reset`。
- `POST /api/auth/recovery-codes/regenerate`：重新认证后轮换恢复码。

login 成功只返回 5 分钟有效的 MFA challenge；challenge 使用独立不透明 Cookie，不能访问管理 API。完成 MFA 后删除 challenge Cookie 并创建正式会话。对所有公开认证端点使用统一错误 envelope 和固定响应时间下限加小幅随机抖动，降低账号枚举信号；不会承诺完全消除网络侧时序差异。

选择显式状态机而不是让未完成 MFA 的普通会话带权限标记，原因是后者容易被遗漏的授权检查误放行。没有采用 JWT，因为管理员禁用、密码/MFA 变更和会话撤销需要立即生效且 ADR 已指定 PostgreSQL 服务端会话。

### 2. bootstrap 由数据库单例行永久关断

新增 `control_bootstrap_state`，以 `singleton_id=1` 和 `CHECK` 保证单例，状态为 `required|in_progress|completed`，记录可空 `pending_admin_id`、`started_at`、`completed_at`。所有 bootstrap 变更先 `SELECT ... FOR UPDATE` 锁定该行：

1. start 在 `required` 下建立 pending 管理员、密码凭证和未确认 TOTP，切换为 `in_progress`。
2. complete 验证 TOTP 后在同一事务中确认 MFA、生成恢复码摘要、启用管理员、写审计并切换为 `completed`。
3. reset 只允许持有运行时 bootstrap Secret 时删除未完成身份及凭证并回到 `required`；已完成状态没有任何回退路径。

`CONTROL_BOOTSTRAP_SECRET_FILE` 指向权限收紧的 Secret 文件。进程只在请求校验时读取或缓存锁页内字节，使用常量时间比较，绝不持久化其摘要；完成后 Runbook 要求移除文件。即使文件仍存在，数据库完成状态也优先拒绝。启动时缺少该文件只影响未完成 bootstrap，不影响已完成环境启动。

备选方案是启动时打印随机 token，但日志系统容易复制 Secret；因此不采用。只允许本机访问也不适合容器反向代理拓扑。

### 3. 数据模型用约束表达固定角色和单次消费

新增一条 forward Migration，按依赖顺序建立：

- `control_bootstrap_state`：永久 bootstrap 状态单例。
- `control_admin_users`：UUID 主键、规范化 `login_name`、`display_name`、固定 `auth_source='local'`、固定 `role='super_admin'`、`status=pending|enabled|disabled`、最后登录与数据库 UTC 时间。`CHECK` 禁止其他角色/来源；部分唯一索引保护登录名。
- `control_admin_passwords`：每个管理员一条当前 Argon2id PHC 字符串、参数版本和变更时间。
- `control_admin_totp`：加密 Secret、密钥版本、确认时间、最后成功使用的时间步；每个管理员至多一个当前因子。
- `control_admin_recovery_codes`：随机代码摘要、批次 ID、创建/消费/撤销时间；条件更新保证单次消费。
- `control_admin_activation_tokens`：随机 token 摘要、管理员、创建人、24 小时过期、消费/撤销时间；每个 pending 管理员至多一个有效 token。
- `control_auth_challenges`：密码已验证但 MFA 未完成的短期 challenge 摘要、管理员、来源指纹、创建/过期/消费时间。
- `control_admin_sessions`：会话令牌摘要、CSRF 摘要、管理员、MFA 方式、创建/最后活动/绝对过期/重新认证/撤销时间和固定撤销原因。
- `control_auth_failure_events` 与 `control_auth_failure_windows`：前者保存账号或来源指纹的短期失败事件，后者保存阻断截止时间；两者的键和值都不包含原始登录名或 IP。滚动计数只统计数据库当前时间之前 15 分钟内的事件，过期事件可安全清理。
- `audit_logs`：UUID/时间、固定 category/action/result、可空 actor/target 稳定 UUID、密钥化 actor/source 指纹、原因、request ID 和结构受限的 `details jsonb`。actor/target UUID 不建立指向管理员表的外键，使未完成 bootstrap 身份可以 reset 删除而历史事件仍保持稳定关联；应用运行时角色只授予 insert/select，不授予 update/delete/truncate，本 change 不提供 delete API。

所有业务时间由 PostgreSQL 数据库时钟生成，连接保持 UTC；普通业务行使用事务稳定的 `CURRENT_TIMESTAMP`，并发限流函数在取得 subject 锁后使用 `clock_timestamp()`，避免较早启动、较晚取得锁的事务写入倒退时间。过期判断使用数据库时间，避免多副本应用时钟偏差。除未完成 bootstrap reset 外，管理员只通过状态迁移禁用，不物理删除。审计 actor/target 使用没有外键的稳定 UUID 保持责任链，并由服务事务保证写入时引用合法。

Migration 所有者/迁移角色与产品运行时角色分离。迁移负责创建对象、触发器和授权；Control 产品连接仅获得所需表的 `SELECT`、`INSERT`、`UPDATE` 等最小权限，明确不拥有 schema/table/function，也不能 `UPDATE`、`DELETE` 或 `TRUNCATE audit_logs`，权限集成测试必须使用该运行时角色执行。

不为角色、权限或用户角色建立表；这是数据库级防止范围膨胀的刻意选择。后续若引入其他角色，必须新增 ADR、规格和 Migration。

### 4. 密码、令牌和 TOTP 使用分域密钥

密码使用维护良好的 Go Argon2id 封装输出 PHC 字符串。v1 参数为 64 MiB memory、3 iterations、parallelism 2、16-byte salt 和 32-byte output；启动测试和容量测试需验证部署内存预算，参数随 PHC 字符串保存以支持登录时渐进升级。密码不做 Unicode 归一化，按 Unicode code point 检查 14 至 128 长度并按原始 UTF-8 字节哈希，避免用户输入被无感改写。

`CONTROL_AUTH_KEYRING_FILE` 保存版本化的 32-byte master keys，只能由 Secret 文件或外部 Secret Manager 挂载。通过 HKDF-SHA256 按固定 domain 分别导出：

- TOTP AES-256-GCM 加密密钥；每条密文使用随机 nonce 并绑定管理员 ID 与 key version 作为 AAD。
- session、challenge、activation、recovery-code 摘要的 HMAC-SHA256 keys。
- 登录名与来源审计指纹 key；指纹不能在不同环境间关联。

keyring 标记一个 current key 并保留仍被 TOTP 密文引用的旧 key；新写入只用 current。密钥轮换先增加新版本、再后台或受控命令重加密 TOTP，确认无引用后才移除旧 key。移除摘要旧 key 会主动使对应会话/令牌失效，必须作为受控登出处理。production 缺少合法 keyring 时拒绝启动；dev 可使用显式测试 key 文件，但不提供硬编码默认 key。

所有 bearer token 使用系统 CSPRNG 生成至少 256 位熵，恢复码每个至少 128 位熵并用易抄写编码。TOTP 采用 RFC 6238 SHA-1/6 位/30 秒以兼容常见 authenticator；接受窗口为当前步前后各一，并通过锁定 `last_used_step` 防重放。

选择版本化 keyring 而不是单个环境变量，是为了支持不中断 TOTP 的密钥轮换和文件权限管理。没有自研密码学原语，只组合标准 HKDF、AES-GCM、HMAC 和成熟 TOTP/Argon2id 实现。

### 5. 认证限流与统一失败结果落在 PostgreSQL

登录名先按语法校验并转小写，再使用环境 key 生成账号指纹；来源取直连地址，只有请求来自显式 CIDR allowlist 的反向代理时才解析约定的 forwarded header。原始 IP 只存在于单次请求内存，不写入持久化、日志、指标或 Trace。

每次密码/MFA 失败在独立短事务中插入对应账号与来源失败事件，使用 PostgreSQL `CURRENT_TIMESTAMP` 统计过去 15 分钟事件并原子更新 `blocked_until`。账号阈值 5、来源阈值 20，阻断均从达到阈值的数据库时间起持续 15 分钟。不存在的账号仍执行固定 dummy Argon2id 校验并更新指纹窗口。成功完成 MFA 后清除账号失败事件和阻断状态，但保留来源事件与阻断状态到自然过期。并发写入通过同一 subject 的行锁或事务级 advisory lock 串行化，重试只用于可识别的序列化冲突且最终失败时认证 fail closed。

限流状态以 PostgreSQL 为真相，确保重启不清零；进程内只允许做更严格的突发保护，不能替代数据库限制。失败审计在认证事务失败后用独立短事务记录；审计数据库写失败时认证仍 fail closed，并产生脱敏错误日志与固定错误指标，不能因为审计失败放行。

### 6. 会话令牌、CSRF 和重新认证绑定同一数据库记录

正式 Cookie 名为 `__Host-relay_control_session`，production 固定 `Secure; HttpOnly; SameSite=Strict; Path=/` 且无 Domain。dev 只有在环境类型为 `dev`、绑定回环地址且显式配置时才允许关闭 Secure；staging/production 拒绝该组合。MFA challenge 使用同等级但不同名称和仅认证路径可见的 Cookie。

会话表保存 HMAC 摘要而非 token。认证中间件原子检查 token、管理员 enabled、MFA assurance、30 分钟 idle 和 12 小时 absolute expiry；最后活动时间按最多每分钟一次的条件更新降低写放大。边界判断仍以实际最后成功认证活动为准，过期或撤销时清 Cookie。

CSRF 使用每会话独立随机 token：明文仅由受保护的 session endpoint 返回给同源前端，数据库保存摘要；进程重启后无法从摘要恢复明文，因此每次成功读取 session 时使用该 session 既有 key version 生成新的 CSRF、原子替换摘要并返回新明文，旧 CSRF 立即失效。前端把最近一次 session 响应作为唯一当前证明，不并发刷新，并对 POST/PUT/PATCH/DELETE 使用 `X-CSRF-Token`。Origin/Referer 在 production 同时执行同源校验；CSRF 不依赖 SameSite 单独成立。

登录、MFA 完成、密码/MFA 变化和重新认证通过“插入新会话或更新新 token 摘要 + 撤销旧摘要”的同事务轮换实现。重新认证不签发独立 bearer token，只把当前轮换会话的 `reauthenticated_at` 写为数据库当前时间；高风险 handler 在事务开始后再次校验其不超过 5 分钟，并把原因与状态变更、成功审计一起提交。

管理员禁用先锁定目标管理员，拒绝 self-disable 和最后一个 enabled/activated 管理员，然后在同一事务更新状态、撤销会话/challenge/activation token 并写审计。所有请求每次查询管理员状态，因此不存在仅靠缓存继续使用旧会话的窗口。

### 7. 审计是安全状态事务的一部分

状态变更服务接收结构化 `AuditIntent`，领域事务必须同时写目标状态和成功审计。固定枚举覆盖 bootstrap、admin、password、mfa、session、reauth 和 authorization；`details` 只接受每种 action 注册的字段白名单，不允许 handler 直接写任意请求对象。

认证失败、CSRF 拒绝、限流和重复 bootstrap 没有业务状态成功提交时，使用独立审计事务记录失败。若数据库整体不可用，系统 fail closed，只输出不含身份/Secret 的结构化错误和计数；恢复后不伪造缺失的成功审计。request ID 由入口生成或只接受符合格式且来自受信代理的值。

审计 API 在后续 change 再实现。本 change 仅提供内部写入和测试查询能力，数据库应用角色无 update 权限。180 天清理由后续持久任务 change 建设；在此之前记录保留更久，不提前删除。

### 8. OpenAPI 和生成链保持单向

`api/openapi.yaml` 是请求/响应、Cookie security scheme、错误 envelope 和 operationId 的唯一 API 真相源。先修改 OpenAPI，再运行 `make generate` 生成 `internal/api/api.gen.go` 和 Orval 客户端/Hooks；生成目录不得手改。敏感响应 schema 使用 `Cache-Control: no-store`，OpenAPI 示例不得出现可用格式的真实 Secret。

Migration 是数据库结构真相源，`queries/*.sql` 是 sqlc 查询真相源。新增查询后运行既有生成命令，检查二次生成无 diff。应用启动先运行或要求已运行 Migration，不在运行时自动创建/改变表。

### 9. 前端按认证状态拆分路由

React 建立轻量 auth shell，启动时只读取 bootstrap status 和 session：

- `required|in_progress` 进入 bootstrap 页面；Secret、密码、TOTP 和恢复码始终只保存在组件内存，不进入 URL、localStorage、sessionStorage 或遥测。
- 已完成且无会话进入登录/MFA；challenge 和 session 由 HttpOnly Cookie 承载。
- 有效会话进入管理 shell；管理员页面懒加载，恢复码一次显示页在离开后不能重新打开。
- 401 清理内存态并回登录，403 CSRF 错误要求刷新 session/CSRF，不自动重放非幂等请求。

页面不得把激活 token 放入查询参数；运维人员复制 token 后由新管理员粘贴到激活表单。这样避免浏览器历史、Referer 和代理访问日志泄露。前端对统一失败只展示通用提示；具体排障依赖有权限的审计能力和 request ID。

### 10. 可观测性只用固定枚举

新增计数器和 gauge，名称在实现时遵循项目指标前缀：认证 operation/result、MFA method、rate-limit dimension 和活动会话数。标签集合在代码中用枚举封闭，不接受登录名、管理员/会话/request ID、IP 或错误原文。结构化日志只写固定错误码、request ID 和内部组件；redaction 测试扫描日志、响应、审计详情、metrics 和 trace exporter 的捕获结果。

指标写入不参加数据库事务；失败不会改变认证结果。数据库和 auth 模块故障只让 Control 管理面 fail closed，不调用或修改 Gateway/Node。

### 11. 测试分层验证安全与恢复

- 纯单元：输入规范化、密码策略/PHC、token 摘要、TOTP 窗口和防重放、Cookie 属性、CSRF、错误映射、指标标签白名单和 redaction。
- PostgreSQL 集成：Migration up/down/up、单例/bootstrap 并发、激活和恢复码单次消费、限流跨重启、会话过期/撤销、最后管理员保护、状态与审计原子回滚、UTC 边界。
- HTTP：所有匿名/受保护路由、统一认证失败、Cookie/CSRF、重新认证、禁用后旧会话、服务身份拒绝和数据库不可用 fail-closed。
- 前端 Vitest/Testing Library：状态路由、Secret 不持久化、401/403 处理、一次性恢复码页面。
- Playwright：bootstrap、登录、TOTP、恢复码、创建/激活/禁用第二管理员、重启后会话与 bootstrap 状态。
- 容器：production 缺少 keyring fail closed、非 root、Secret 文件权限、Control 停止时 Gateway/Node 冒烟请求不受影响。

## Risks / Trade-offs

- **[本地认证扩大安全敏感代码面]** → 仅使用成熟密码学实现、固定参数版本、负向/并发/脱敏测试，并在合并前执行独立安全审查。
- **[Argon2id 在并发攻击下消耗内存]** → 数据库和进程内双层限流、请求并发上限、dummy hash 复用参数，并用目标容器容量基准校准但不降低规格下限。
- **[PostgreSQL 会话带来每请求读取与活动更新时间写入]** → 使用索引化摘要查询和一分钟条件更新时间；首期管理员规模很小，优先选择立即撤销与一致性。
- **[TOTP 加密 keyring 丢失会使管理员无法登录]** → Runbook 要求备份、版本化轮换和恢复演练；恢复不得通过重开 bootstrap 绕过已完成状态。
- **[一次性 token 手工传递可能被复制]** → 至少 256 位熵、24 小时到期、摘要存储、单次消费、重新生成即撤销旧 token，禁止 URL/日志/遥测传递。
- **[统一失败与限流降低排障可见性]** → 客户端只返回 request ID；数据库审计保存固定分类和密钥化指纹，后续受保护审计页面提供定位。
- **[down Migration 可能删除身份和审计]** → 生产回滚默认只回退应用并保留向前兼容表；破坏性 down 仅用于全新安装测试或明确备份后的人工回退。

## Migration Plan

1. 合并前更新 OpenAPI、Migration、sqlc 查询和生成物，验证二次生成无差异。
2. 在空数据库执行 Goose up/down/up；在现有仅含 `environments` 的阶段 0 数据库执行 up，确认环境单例不变。
3. 部署前为每个环境生成独立 bootstrap Secret 与 auth keyring 文件，收紧文件权限并备份 keyring；production 校验外部 HTTPS、Secure Cookie 和 MFA 强制配置。
4. 先执行 Migration，再部署新 Control。此时 Gateway/Node 无需重启；未 bootstrap 时只有健康和 bootstrap 流程可用。
5. 运维人员完成首个管理员 bootstrap、保存恢复码、移除 bootstrap Secret 文件并验证重复初始化失败。
6. 创建第二个实名管理员并验证独立激活、登录、审计和互相恢复能力后，才把后续管理功能接入统一授权中间件。
7. 回滚应用时保留新增表和 `completed` 状态；若新版本已创建管理员或审计，不执行 down。只有从未完成 bootstrap 且确认无依赖数据时，才允许备份后执行 down。

## Open Questions

无。密码、限流、MFA、会话、重新认证、管理员激活和 Secret 处理策略均在本 change 中固定；未来调整这些外部行为必须更新 OpenSpec 后再实施。
