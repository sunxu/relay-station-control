## MODIFIED Requirements

### Requirement: Gateway HTTP mapping SHALL be explicit

Control MUST 实现下列固定HTTP契约；不得在OpenAPI implementation时重新决定method/path/status。

本节冻结API映射，implementation仅按此更新OpenAPI与generated clients。POST/PATCH沿用现有unsafe-request middleware（缺CSRF可先403），全部安全检查在receipt可见前完成。所有接口沿用Control session cookie、实名super_admin与Cache-Control:no-store；POST/PATCH使用既有CSRF header。Health是显式GET观察，仍须有效super_admin；不得由列表自动触发。未知request字段拒绝，不返回credential、sealed state或legacy reference。

| Action | Method/path | JSON request | Success |
|---|---|---|---|
| Register | POST /api/assets/gateways | command_id,new_instance_id,display_name,management_endpoint；directory_credential可省略/string，null非法 | 201 RegisterResult |
| Edit | PATCH /api/assets/gateways/{instance_id} | command_id,expected_revision；display_name/management_endpoint/directory_credential为显式patch字段 | 200 EditResult |
| Retire | POST /api/assets/gateways/{instance_id}/retire | command_id,expected_revision；machine reason由server固定administrator_retire，无free-text reason | 200 RetireResult |
| Replace | POST /api/assets/gateways/{instance_id}/replace | command_id,expected_revision,new_instance_id,display_name,management_endpoint；directory_credential可省略/string，null非法 | 200 ReplaceResult |
| Health | GET /api/assets/gateways/{instance_id}/health | 无body，无command_id/revision | 200 ProbeResult |
| Connection Test | POST /api/assets/gateways/{instance_id}/connection-test | 空object；无command_id/revision | 200 ProbeResult |

command_id在body中为UUID；expected_revision在body中为规范正int64十进制string，范围1..9223372036854775807，response revision同型。Register没有expected_revision。path identity为UUID，body不可另传冲突identity。display_name/endpoint必须有效非null，`management_endpoint` 必须是 `http://` origin；`https://` 返回400 invalid_endpoint且零 outbound。Edit省略directory_credential表示keep、显式null表示clear、合法string表示set；Register/Replace省略表示unconfigured、合法string表示set、显式null非法，Replace不得继承predecessor credential。Credential string MUST是非空、至多4096 UTF-8 bytes，且不得包含NUL、CR或LF；Control MUST保留exact bytes，不trim、不normalize。第一版不提供operator note。Gateway无DELETE能力。revision溢出返回409 revision_exhausted且无mutation。

Exact success bodies and receipt projection：

AssetResult固定字段：instance_id、lifecycle_status(active|retired)、revision(string)、display_name、management_endpoint、secret_configured(boolean)、created_at、updated_at、retired_at(nullable UTC timestamp)、retired_by(nullable admin UUID)、retire_reason(nullable administrator_retire|replacement)。无plaintext、sealed blob、K2/commitment、legacy reference或crypto metadata。

- RegisterResult = {result:"registered",asset:AssetResult}。
- EditResult = {result:"updated",asset:AssetResult}。
- RetireResult = {result:"retired",asset:AssetResult,closed_binding_count:nonnegative integer}。
- ReplaceResult = {result:"replaced",old_asset:AssetResult,new_asset:AssetResult,closed_binding_count:nonnegative integer,lineage:{old_instance_id,new_instance_id,replaced_at,replaced_by,command_id}}。
- ProbeResult = {instance_id,result:"healthy",observed_at:UTC timestamp}；在已验证的 HTTP-only management origin 上固定GET /health、5秒总timeout，无redirect/retry/fallback、不携带reader credential、不持DB事务做network call。`https://` 在 client construction 前拒绝且零 outbound；当前runtime不发起TLS。retired资产拒绝probe；已经授权的probe可作为独立transport observation收尾，不推进revision/Directory。

receipt.sanitized_result MUST保存对应完整success body及http_status，不通过后续asset row重构。same actor/same intent replay返回原status和body（Register仍201），request_id作为本次传输错误跟踪而非旧success body字段。success audit、asset mutation、lineage、receipt同事务；audit/receipt失败全部rollback。

Error contract：

错误body沿用 {code,message,request_id}；message是固定脱敏文本，code allowlist如下。认证优先于资产/receipt可见性；认证后无receipt才按target存在、active/current、expected_revision、new identity、metadata顺序判定；格式错误在安全校验后返回400。Stage 0 MUST NOT新增`k2_*`、`aes_*`、`decrypt_*`、`cipher_*`等crypto-specific public error code；K2 unavailable或Open失败复用既有批准的validation/unavailable family。

| Status | code | Meaning |
|---|---|---|
| 401 | unauthorized | 缺失/失效/撤销session，沿用administrator-access |
| 403 | forbidden / csrf_invalid | 非super_admin或CSRF失败，沿用administrator-access |
| 400 | validation_failed | UUID、revision、patch、分页参数或JSON shape非法 |
| 400 | invalid_endpoint | endpoint不满足既有规范化/transport验证 |
| 400 | secret_configuration_invalid | credential格式或secret operation非法 |
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
| 503 | service_unavailable | DB、receipt key、credential cipher或内部依赖不可用，fail closed |

Current/history reads, cursor and counts：

GET /api/assets/gateway 保持现有response envelope（包括空Gateway的既有空态），target仅active/current；Gateway对象additive新增lifecycle_status/revision/retirement fields。GET /api/assets/gateways/{instance_id} 返回 {asset:AssetResult,predecessor:Lineage|null,successor:Lineage|null}，缺失404；Lineage字段同ReplaceResult。GET /api/assets/gateways 返回 {items:AssetResult[],next_cursor:string|null,gateway_counts:{active,retired,total}}。

list query生命周期过滤名lifecycle，值active/retired/all，缺省active；limit默认50、上限100、下限1。沿用Node的stable identity排序：ORDER BY instance_id ASC，cursor keyset为last_instance_id。cursor为有完整性保护的opaque payload，包含encoding_version=1、environment、lifecycle、last_instance_id和registry_generation；复用当前Node cursor签名/validation机制。修改filter或损坏cursor返回400 validation_failed。

registry_generation定义为同一REPEATABLE READ查询快照下所有Gateway revision之和（numeric无溢出）。Register增加1，任何Edit/Retire增加1，Replace增加2；不允许identity delete/revision降低，因此是单调generation。第一页面保存generation，后续page在同一事务内验证generation并查询；generation变化则409 cursor_stale，不在变化集合上静默遗漏/重复。合法cursor链因此对应同一稳定Gateway集合，无需跨HTTP持长DB transaction。没有变化的集合按UUID keyset恰好一次返回；并发mutation时明确重启。Directory observation不改generation；historical rows与lineage不可改写。detail是当前读取，不声称与旧list cursor共享快照。

GetAssetCounts原gateways继续表示total rows（不静默改成active）；新增gateway_counts:{active,retired,total}，gateways=total。原nodes/drivers等字段保持。UI、reconcile不得用gateways total推断current slot个数；operational UI改用gateway_counts.active，数据库shape约束负责single-current。

#### Scenario: HTTP映射
- **WHEN** 调用Register/Edit/Retire/Replace/Health/Connection Test
- **THEN** 使用表中method/path/body/status，未知字段400，不返回plaintext、sealed state、K2或legacy reference

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

#### Scenario: Gateway credential exact validation
- **WHEN** Register/Edit/Replace分别提交0、1、4096、4097 UTF-8 bytes、跨4096边界的multibyte input、NUL、CR、LF、leading spaces或trailing spaces
- **THEN** 仅1..4096 UTF-8 bytes且无NUL/CR/LF的值被接受，leading/trailing spaces按exact bytes保留且不trim/normalize

#### Scenario: Same-actor semantic-invalid credential preserves existing-command precedence
- **WHEN** same actor以existing command_id提交syntactically parseable但semantic-invalid的Gateway credential
- **THEN** 系统先按existing durable command intent分类replay或command_conflict，不提前返回credential validation error且不访问K2

## ADDED Requirements

### Requirement: Gateway Retire and Replace predecessor SHALL erase credential atomically
Retire与Replace predecessor MUST在同一asset lifecycle transaction中清除sealed credential，并保持既有revision、lineage、command replay、receipt与audit语义。K2 unavailable时keep、clear、Retire和Replace-with-unconfigured MUST仍可执行且MUST NOT Open/Seal或要求K2；只有Set、Replace-with-new-credential和authenticated outbound需要K2。

#### Scenario: Retire transaction rolls back
- **WHEN** Gateway Retire transaction在提交前失败
- **THEN** lifecycle、revision与sealed credential全部保持原值，不出现retired asset仍携带credential或active asset credential被单独清除

#### Scenario: Replace does not inherit predecessor credential
- **WHEN** 管理员Replace Gateway且省略`directory_credential`
- **THEN** predecessor在同一transaction被retired并清除credential，replacement为`secret_configured=false`，即使K2 unavailable也不读取或写入credential material
