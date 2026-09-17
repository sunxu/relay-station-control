## MODIFIED Requirements

### Requirement: Control SHALL 使用固定 HTTP fetch contract

Control SHALL 仅通过 HTTP 对已配置 Gateway management/Directory endpoint 执行 `GET /internal/v1/api-account-directory`。该 endpoint MUST 使用 `http://`；`https://` MUST 在配置验证或 client construction 阶段被拒绝，且 MUST 发出零个 outbound request。Control MUST 使用 `Authorization: Bearer token`，其中token由同一个fenced Gateway target的protected credential read取得sealed `directory_credential`并以K2 Open；sealed value、K2、commitment或legacy reference不得被当作token/path或普通query结果。HTTP fetch MUST NOT follow redirects；single response body 的读取上限 MUST be 4 MiB；`accounts` 上限 MUST be 10,000；非 200、timeout、partial body、retryable failure 以及读取超限 MUST 先记录 attempt failure，再按 retryability 分类；raw body MUST NOT 被持久化。该 internal management transport 约束不改变 Gateway Account/upstream 或 request data-plane endpoint 的 scheme。

#### Scenario: 正常 fetch
- **WHEN** Control 以 HTTP 对 `GET /internal/v1/api-account-directory` 发起带由protected credential Open所得Bearer token的请求
- **THEN** fetch 继续进入验证流程

#### Scenario: HTTPS target被预先拒绝
- **WHEN** Gateway management/Directory endpoint 使用 `https://`
- **THEN** Control 在配置验证或 client construction 阶段拒绝 target，发出零个 outbound request，且不得恢复 TLS、dual-protocol 或 HTTPS fallback branch

#### Scenario: redirect
- **WHEN** Gateway 返回 redirect
- **THEN** Control 不跟随 redirect，整轮 ingestion failed

#### Scenario: body 超限
- **WHEN** response body 超过 4 MiB
- **THEN** Control 停止读取并把整轮 ingestion 标记为 failed

#### Scenario: 账号数超限
- **WHEN** `accounts` 数量超过 10,000
- **THEN** Control 整轮 rejected/failed，不保存 raw body

所有正常 fetch/re-fetch eligibility MUST 为同一个 source Gateway active 且 singleton_id=1；run.gateway_instance_id 必须匹配。Retire/Replace commit 后不得授权新的 outbound；已授权并发生的有界 transport evidence 可保留，promotion 仍必须独立重查。固定 cadence、retry预算、严格 response/source-time 验证不变。

#### Scenario: Retire先于fetch
- **WHEN** run 尚未 outbound，Gateway Retire 已 commit
- **THEN** 无 HTTP call，run terminal non-success，不 promotion、不刷新freshness

### Requirement: Control SHALL 仅保存 Secret reference 且不得泄露 raw response

该canonical标题为OpenSpec MODIFIED matching保留；Stage 0正文supersede其中“仅保存Secret reference”的旧存储语义。Control SHALL仅为Gateway Directory credential保存approved protected-at-rest representation；plaintext仅可由受限resolver在单次authenticated fetch的有界内存生命周期中Open。Control MUST NOT保存service token原文、Gateway DB credential或legacy reference，且不得建立Directory专用generic Secret table。Raw response、原始错误、endpoint userinfo、query string、body、plaintext、sealed blob、K2、K2 commitment、crypto metadata、日志、指标标签、审计detail和测试artifact MUST NOT包含可逆凭证内容。若响应出现contract外敏感字段或无法安全处理的额外字段，Control MUST fail closed。

#### Scenario: 失败路径注入 canary
- **WHEN** 测试向token、URL、错误文本和body注入唯一canary
- **THEN** canary不能出现在普通日志、指标标签、审计detail或持久化失败原因中

#### Scenario: 缺少 token reference
- **WHEN** Gateway sealed Directory credential缺失、损坏或K2不可用
- **THEN** Control fail closed，并把该轮ingestion记录为失败而不是退化为无认证访问或legacy reference fallback

#### Scenario: 只存在 reader_secret_ref
- **WHEN** Migration 00051 检测到Gateway存在non-null legacy `reader_secret_ref`
- **THEN** migration在Stage 0 runtime启动前原子失败；operator使用fresh DB/re-register transition，production schema/runtime不得通过保留legacy column、SecretResolver或dual-read来接受该状态

### Requirement: Directory runtime SHALL 通过受限有效lease读取目标

冻结Architecture/TCCR要求effective protected-column isolation与constrained internal access，但不强制具体数据库机制。本OpenSpec为当前Control选择并复用SECURITY DEFINER只读函数：runtime通过该函数获取instance_id、management_endpoint和sealed Directory credential，且仅在run/Gateway/fencing匹配、running、lease未过期及sealed state存在时返回。函数MUST STABLE、固定search_path、migrator owner，只有runtime可EXECUTE。Runtime MUST NOT直接SELECT protected column；返回值只供同一fenced target的credential Open与采集。共同调度SHALL使用`secret_configured = sealed_credential IS NOT NULL`判断presence，不得Open或依赖K2/authenticity。

#### Scenario: 有效与无效lease
- **WHEN** runtime分别以有效、错误Gateway/token、过期或terminal run调用目标函数
- **THEN** 只有有效lease返回目标和sealed credential；其他调用零行，不能Open或发送Secret

#### Scenario: 直接读取和越权调用
- **WHEN** runtime直接SELECT protected credential column，或PUBLIC/registrar角色调用函数
- **THEN** permission denied；既有列级读取权限不扩大
