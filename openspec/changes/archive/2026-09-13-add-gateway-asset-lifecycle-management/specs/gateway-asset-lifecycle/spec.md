## ADDED Requirements

### Requirement: Gateway Register and Edit

Gateway Register MUST require no current slot, valid metadata with an HTTP-only `management_endpoint`, and a new identity absent from all history; success creates active/current with revision 1. An `https://` management endpoint MUST be rejected during validation before any outbound request. Register MUST NOT probe, mutate Account/Group/routing, or reuse retired identity. Gateway Edit MUST require active state, matching `expected_revision`, `command_id`, authenticated `super_admin` and CSRF; only display name, HTTP-only management endpoint and approved secret configuration are mutable and success advances revision. Edit of the endpoint to `https://` MUST fail before outbound activity.

#### Scenario: Register success and conflict

- **WHEN** a valid Register has no current Gateway
- **THEN** one active current Gateway with revision 1, receipt and success audit is committed
- **WHEN** current slot is occupied, identity is historical/duplicate, or command conflicts
- **THEN** Register conflicts without mutation or success audit

#### Scenario: Stale Edit

- **WHEN** Edit expected revision differs from current revision
- **THEN** Edit conflicts without mutation or success audit

### Requirement: Gateway Health and Connection Test

Gateway Health/Connection Test MUST be an explicit bounded `GET /health` observation against the asset's validated HTTP-only `management_endpoint`, with no redirect, retry or fallback. The current runtime MUST NOT initiate TLS; an `https://` target MUST be rejected before the probe with zero outbound request. It MUST NOT mutate lifecycle, revision, Directory or routing, or persist raw response body.

#### Scenario: Fixed observation

- **WHEN** an authorized administrator runs Connection Test
- **THEN** only the fixed probe executes and a sanitized observation is returned

#### Scenario: HTTPS probe target is rejected

- **WHEN** a Gateway Health or Connection Test target resolves to an `https://` management endpoint
- **THEN** Control rejects it before the probe, issues zero outbound requests, and does not restore a TLS or dual-protocol branch

### Requirement: Gateway Retire

Retire MUST be one durable command and one PostgreSQL transaction. After authentication/session/super_admin/CSRF, it MUST acquire command advisory lock, lookup receipt, lock current Gateway, validate active/current/revision, lock all current bindings in `relay_node_id ASC`, establish one boundary, close bindings with `gateway_retired`, retire/clear slot, advance revision, write one audit and receipt, and commit. No mutation precedes the boundary; no network call occurs.

#### Scenario: Successful Retire

- **WHEN** a valid current Gateway is retired
- **THEN** it is historical retired, slot is empty, all current bindings are closed, and no replacement lineage is created

#### Scenario: Retire replay and stale command

- **WHEN** the same actor retries the same committed intent, or a new command uses stale revision/already-retired state
- **THEN** replay returns the persisted result; the other cases conflict without a second transition or success audit

#### Scenario: Retire crash before commit

- **WHEN** the transaction crashes before commit
- **THEN** Gateway, bindings, receipt and audit remain at pre-command state

### Requirement: Gateway Replace

Replace MUST be one durable command and one PostgreSQL transaction. It MUST lock command/receipt, old Gateway, validate active/current/revision/new identity/metadata, lock old bindings in `relay_node_id ASC`, establish exactly one replacement boundary, then close bindings with `gateway_replaced`, retire old with reason `replacement` and revision +1, create new active/current revision 1, insert immutable lineage, write one audit/receipt, and commit. The new Gateway has zero bindings and independent Directory truth.

#### Scenario: Successful Replace and binding race

- **WHEN** a valid replacement commits, including after a binding commits first
- **THEN** old is retired with old bindings closed, new is active/current with zero bindings, and lineage exists

#### Scenario: Replace replay, conflict and duplicate identity

- **WHEN** same actor retries same old/new intent, or command identity/expected revision/new identity conflicts
- **THEN** replay returns the persisted result; conflict cases have no mutation or success audit

#### Scenario: Replace crash before commit

- **WHEN** the transaction crashes before commit
- **THEN** old remains active/current, new/lineage do not exist, and no success receipt/audit exists

### Requirement: Current/history reads and lineage

Control MUST retain `GET /api/assets/gateway` for current singular read; provide `GET /api/assets/gateways?lifecycle=active|retired|all&limit=<n>&cursor=<opaque>` with default `active`; and provide `GET /api/assets/gateways/{instance_id}` for stable detail. Detail exposes predecessor/successor lineage. Responses expose lifecycle, revision and `secret_configured` only; counts expose active/retired/total.

#### Scenario: Historical detail

- **WHEN** a retired Gateway is requested by stable identity
- **THEN** historical lifecycle and immutable lineage are returned without resurrection

### Requirement: Gateway authorization, replay and Secret safety

All durable Gateway mutations MUST require active authenticated session, `super_admin`, CSRF and `command_id`; existing-asset mutations MUST require `expected_revision`. Same actor plus same canonical intent replays the persisted result before domain-state evaluation; actor mismatch or different intent conflicts. Raw secret references, credentials, credential-bearing headers and raw probe body MUST NOT appear in API/UI/audit/receipt/logs.

#### Scenario: Unauthorized or secret-bearing request

- **WHEN** authorization, CSRF, revision or Secret redaction preconditions fail
- **THEN** no durable mutation or successful audit is committed and no raw Secret is returned

### Requirement: Gateway HTTP mapping SHALL be explicit

Control MUST 实现下列固定HTTP契约；不得在OpenAPI implementation时重新决定method/path/status。


本节冻结API映射，implementation仅按此更新OpenAPI与generated clients。POST/PATCH沿用现有unsafe-request middleware（缺CSRF可先403），全部安全检查在receipt可见前完成。所有接口沿用Control session cookie、实名super_admin与Cache-Control:no-store；POST/PATCH使用既有CSRF header。Health是显式GET观察，仍须有效super_admin；不得由列表自动触发。未知request字段拒绝，不返回secret reference。

| Action | Method/path | JSON request | Success |
|---|---|---|---|
| Register | POST /api/assets/gateways | command_id,new_instance_id,display_name,management_endpoint；reader_secret_ref可省略/string/null | 201 RegisterResult |
| Edit | PATCH /api/assets/gateways/{instance_id} | command_id,expected_revision；display_name/management_endpoint/reader_secret_ref为显式patch字段 | 200 EditResult |
| Retire | POST /api/assets/gateways/{instance_id}/retire | command_id,expected_revision；machine reason由server固定administrator_retire，无free-text reason | 200 RetireResult |
| Replace | POST /api/assets/gateways/{instance_id}/replace | command_id,expected_revision,new_instance_id,display_name,management_endpoint；reader_secret_ref可省略/string/null | 200 ReplaceResult |
| Health | GET /api/assets/gateways/{instance_id}/health | 无body，无command_id/revision | 200 ProbeResult |
| Connection Test | POST /api/assets/gateways/{instance_id}/connection-test | 空object；无command_id/revision | 200 ProbeResult |

command_id在body中为UUID；expected_revision在body中为规范正int64十进制string，范围1..9223372036854775807，response revision同型。Register没有expected_revision。path identity为UUID，body不可另传冲突identity。display_name/endpoint必须有效非null，`management_endpoint` 必须是 `http://` origin；`https://` 返回400 invalid_endpoint且零 outbound。Edit省略表示不修改；reader_secret_ref省略/显式null/合法string分别为absent/clear/set。第一版不提供operator note。Gateway无DELETE能力。revision溢出返回409 revision_exhausted且无mutation。

Exact success bodies and receipt projection：

AssetResult固定字段：instance_id、lifecycle_status(active|retired)、revision(string)、display_name、management_endpoint、secret_configured(boolean)、created_at、updated_at、retired_at(nullable UTC timestamp)、retired_by(nullable admin UUID)、retire_reason(nullable administrator_retire|replacement)。无raw secret_ref。

- RegisterResult = {result:"registered",asset:AssetResult}。
- EditResult = {result:"updated",asset:AssetResult}。
- RetireResult = {result:"retired",asset:AssetResult,closed_binding_count:nonnegative integer}。
- ReplaceResult = {result:"replaced",old_asset:AssetResult,new_asset:AssetResult,closed_binding_count:nonnegative integer,lineage:{old_instance_id,new_instance_id,replaced_at,replaced_by,command_id}}。
- ProbeResult = {instance_id,result:"healthy",observed_at:UTC timestamp}；在已验证的 HTTP-only management origin 上固定GET /health、5秒总timeout，无redirect/retry/fallback、不携带reader credential、不持DB事务做network call。`https://` 在 client construction 前拒绝且零 outbound；当前runtime不发起TLS。retired资产拒绝probe；已经授权的probe可作为独立transport observation收尾，不推进revision/Directory。

receipt.sanitized_result MUST 保存对应完整success body及http_status，不通过后续asset row重构。same actor/same intent replay返回原status和body（Register仍201），request_id作为本次传输错误跟踪而非旧success body字段。success audit、asset mutation、lineage、receipt同事务；audit/receipt失败全部rollback。

Error contract：

错误body沿用 {code,message,request_id}；message是固定脱敏文本，code allowlist如下。认证优先于资产/receipt可见性；认证后无receipt才按target存在、active/current、expected_revision、new identity、metadata顺序判定；格式错误在安全校验后返回400。

| Status | code | Meaning |
|---|---|---|
| 401 | unauthorized | 缺失/失效/撤销session，沿用administrator-access |
| 403 | forbidden / csrf_invalid | 非super_admin或CSRF失败，沿用administrator-access |
| 400 | validation_failed | UUID、revision、patch、分页参数或JSON shape非法 |
| 400 | invalid_endpoint | endpoint不满足既有规范化/transport验证 |
| 400 | secret_configuration_invalid | reader reference格式或secret operation非法 |
| 404 | asset_not_found | target identity不存在 |
| 409 | asset_retired | 新command操作retired target；合法receipt replay优先返回原结果 |
| 409 | current_gateway_exists | Register current slot已占用或并发slot竞争失败 |
| 409 | duplicate_identity | 新identity已在history中存在（同一请求同时占slot时优先current_gateway_exists） |
| 409 | stale_revision | existing asset revision不匹配 |
| 409 | revision_exhausted | bigint revision无法再+1 |
| 409 | command_conflict | globally unique command_id对应actor或canonical intent不匹配 |
| 409 | cursor_stale | Gateway集合在分页期间发生durable mutation，要求从第一页重启 |
| 504 | probe_timeout | 实际probe超出5秒，不回滚独立asset command |
| 502 | probe_failed | HTTP transport/non-200/redirect等固定probe失败，raw error/body不回显；`https://` target在outbound前以400 invalid_endpoint拒绝 |
| 503 | service_unavailable | DB、receipt key或内部依赖不可用，fail closed |

Current/history reads, cursor and counts：

GET /api/assets/gateway 保持现有response envelope（包括空Gateway的既有空态），target仅active/current；Gateway对象additive新增lifecycle_status/revision/retirement fields。GET /api/assets/gateways/{instance_id} 返回 {asset:AssetResult,predecessor:Lineage|null,successor:Lineage|null}，缺失404；Lineage字段同ReplaceResult。GET /api/assets/gateways 返回 {items:AssetResult[],next_cursor:string|null,gateway_counts:{active,retired,total}}。

list query生命周期过滤名lifecycle，值active/retired/all，缺省active；limit默认50、上限100、下限1。沿用Node的stable identity排序：ORDER BY instance_id ASC，cursor keyset为last_instance_id。cursor为有完整性保护的opaque payload，包含encoding_version=1、environment、lifecycle、last_instance_id和registry_generation；复用当前Node cursor签名/validation机制。修改filter或损坏cursor返回400 validation_failed。

registry_generation定义为同一REPEATABLE READ查询快照下所有Gateway revision之和（numeric无溢出）。Register增加1，任何Edit/Retire增加1，Replace增加2；不允许identity delete/revision降低，因此是单调generation。第一页面保存generation，后续page在同一事务内验证generation并查询；generation变化则409 cursor_stale，不在变化集合上静默遗漏/重复。合法cursor链因此对应同一稳定Gateway集合，无需跨HTTP持长DB transaction。没有变化的集合按UUID keyset恰好一次返回；并发mutation时明确重启。Directory observation不改generation；historical rows与lineage不可改写。detail是当前读取，不声称与旧list cursor共享快照。

GetAssetCounts原gateways继续表示total rows（不静默改成active）；新增gateway_counts:{active,retired,total}，gateways=total。原nodes/drivers等字段保持。UI、reconcile不得用gateways total推断current slot个数；operational UI改用gateway_counts.active，数据库shape约束负责single-current。

#### Scenario: HTTP映射
- **WHEN** 调用Register/Edit/Retire/Replace/Health/Connection Test
- **THEN** 使用表中method/path/body/status，未知字段400，不返回原Secret

#### Scenario: 两个Register竞争
- **WHEN** 空current slot上不同command_id并发Register
- **THEN** 恰好一个201，另一409 current_gateway_exists，无失败方asset/audit/receipt残留，不泄露23505

#### Scenario: 并发Replace
- **WHEN** 两个新command以同old revision并发Replace
- **THEN** 最多一个成功，另一个锁后asset_retired；same-command则replay

#### Scenario: Register与Replace
- **WHEN** 已有old current且Register与Replace并发
- **THEN** Register为409 current_gateway_exists；Replace若成功整个commit前后始终只有一个current

#### Scenario: Retire与Register
- **WHEN** Retire old与Register new并发
- **THEN** Register若观察占slot则409；若Retire先commit则允许Register成功，0 current中间态仅由Retire产生

#### Scenario: 分页一致性
- **WHEN** 同cursor链查询且registry generation未变
- **THEN** UUID升序无重复遗漏；generation变化返回409 cursor_stale要求重启

### Requirement: Gateway audit and metrics SHALL use bounded taxonomy

Gateway audit MUST 复用immutable audit_logs；category固定asset_gateway，action固定gateway.register/gateway.edit/gateway.retire/gateway.replace/gateway.health/gateway.connection_test。result为success/failure（实际schema沿用既有对应列映射）；actor_admin_id使用control_admin_users.admin_id，不以login/display name替代。durable成功exactly one audit，与domain/receipt同事务；receipt replay不再次audit，accepted no-op不写successful transition audit。认证/CSRF失败沿用已有security audit，不冒充domain success。

新category details的唯一allowlist：command_id、instance_id、old_instance_id、new_instance_id、reason_code、old_revision、new_revision、closed_binding_count、probe_result。revision字符串，UUID固定类型；不用的字段省略。Retire reason_code=administrator_retire，Replace=replacement，Register/Edit不带reason_code。probe_result只允许healthy/timeout/failed；Health和Connection Test每一次真正probe必须各写一条sanitized observation audit，包括失败，无command_id。audit失败返回503不能声称成功，因probe无durable receipt不得声称跨crash exactly-once执行外部观察；一个正常完成并已执行probe的请求只有一条observation audit。

raw secret_ref、credential、raw external body、credential header、endpoint、login/display name及allowlist之外的detail MUST NOT进入audit。Gateway-driven binding closure由top-level Gateway audit记录closed_binding_count，不为每个child close伪造relay_binding.unbind audit。

Prometheus counters冻结：
- control_asset_mutation_total{asset_type,action,result}：认证通过并进入command处理的每个HTTP request的结果计数，action=register/edit/retire/replace，asset_type=gateway（未来node需独立delta），result=success/replay/noop/conflict/invalid/unavailable。仅success表示实际committed transition；replay/noop不计success；不保证进程crash下计数与DBexactly-once一致。
- control_asset_connection_test_total{asset_type,result}：实际执行Connection Test probe完成结果，result=healthy/timeout/failed。Health使用control_asset_health_total{asset_type,result}同enum，不混入Connection Test计数。
- retire/replace total使用mutation family的action过滤，不建设重复counter；lifecycle conflict total使用result=conflict和action过滤。
- 认证/CSRF拒绝使用既有security指标，不进入asset mutation success。

以上labels MUST严格使用封闭enum。instance_id、display_name、endpoint、raw email、account_key、secret reference、actor ID、command_id、任意error text MUST NOT作为labels。既有资产读取指标继续使用原asset_kind/operation/result，不重命名旧family。

#### Scenario: Mutation audit atomicity
- **WHEN** Retire/Replace的audit或receipt写入失败
- **THEN** 全部domain state rollback，无孤立binding close或success receipt

#### Scenario: Replay/no-op accounting
- **WHEN** 请求为receipt replay或accepted state-idempotent no-op
- **THEN** 不增加successful transition audit，counter分别为replay/noop而非success

#### Scenario: Probe observation
- **WHEN** Health/Connection Test真正执行并完成
- **THEN** 一个sanitized observation audit，固定probe_result，指标没有asset/actor/endpoint身份label

#### Scenario: Cardinality and Secret rejection
- **WHEN** audit或metric输入含Secret、自由error text或身份label
- **THEN** 只投影固定allowlist，敏感输入不得进入持久audit或导出的series

### Requirement: Gateway persistence SHALL preserve actor and reference identity

instance_id MUST 成为physical PRIMARY KEY，singleton_id为nullable current-slot marker；migration MUST先backfill、建新PK，再在同事务切换四个FK依赖并移除旧UNIQUE，最后验证strict shape。retired_by/replaced_by MUST 引用control_admin_users.admin_id，ON UPDATE RESTRICT ON DELETE RESTRICT。retirement timestamps与binding/lineage timestamp MUST 使用locks完成后的一次DB boundary。不改instance_id、不丢历史、不留最终duplicate UNIQUE index。

#### Scenario: PostgreSQL 18 migration proof
- **WHEN** 实现阶段在PG18验证forward migration及失败rollback
- **THEN** conindid指向新PK，四个FK完整，backfill先于strict validation，actor FK RESTRICT有效；每个committed boundary无dangling reference

#### Scenario: Stable actor history
- **WHEN** 尝试删除或更改retired_by/replaced_by引用的admin UUID
- **THEN** PostgreSQL RESTRICT拒绝，禁止用login/display name替换durable actor
