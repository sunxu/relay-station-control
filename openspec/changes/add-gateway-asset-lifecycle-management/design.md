## Baseline and ownership

本 design 基于 Ops approval `5add9cb54f0a488cc547b2ba72287c8f492a32f7`、frozen requirements `8c7cdbcf5ea3480da16d51408581a4be3e72c994`、reviewed Control `5e2caeb031a47510744cada56978b963da39b4e9`、Gateway `6b045698e6e5e62e35dbd103abf20c1407f8a0bb` 和 CLIProxyAPI `273d624c70f6eb8bdd7b049df396c306acd3f8d0`。

本 change owns the shared command/compatibility foundation and all Gateway-specific lifecycle behavior. Node-specific lifecycle、monitoring cancellation 和 Inventory fencing 由后续 Node lifecycle change owns，必须复用本 change 的 foundation。

当前已归档 `internal-http-transport` baseline 同时约束本 change：Control-managed Gateway `management_endpoint`、Directory target 以及 Health/Connection Test target MUST 使用 `http://`。`https://` MUST 在 metadata validation 或 client construction 阶段 fail closed，并发出零个 outbound request。Stage 1 MUST 复用现有 `gatewaydirectory` HTTP-only validator/client，不恢复 TLS、certificate skip-verify、HTTP/HTTPS toggle、dual-protocol 或 fallback branch。该约束仅适用于 Control-managed internal management/Directory transport，不改变 Gateway Account/upstream 或 request data-plane endpoint scheme。

## Persistence direction

现有 `gateway_instances` 的物理约束为 `gateway_instances_pkey PRIMARY KEY (singleton_id)` 与 `gateway_instances_instance_id_key UNIQUE (instance_id)`。已核对的 direct FKs 为：`gateway_directory_ingestion_runs(gateway_instance_id)`、`gateway_directory_snapshots(gateway_instance_id)`、`gateway_directory_current_state(gateway_instance_id)` 和 `relay_node_gateway_account_bindings(gateway_instance_id)`；后者另有 `(evidence_snapshot_id,gateway_instance_id)` FK 指向 snapshot。目标决定为 physical `PRIMARY KEY (instance_id)`，`singleton_id` 改为 nullable current-slot marker，并保留 `instance_id` 值不变。

单一 forward migration transaction 冻结如下顺序（停止应用 writer，按固定表顺序取得 DDL locks）：

1. additive 增加可回填 lifecycle/revision/retirement metadata 列。
2. backfill 原 Gateway 为 active、revision=1、retired_at/by/reason=NULL，不改 instance_id。
3. 验证 instance_id NOT NULL、唯一、四个 direct FK referential integrity。
4. 创建 standalone UNIQUE index `gateway_instances_instance_id_pk_stage`；允许 migration window 暂时与旧 UNIQUE 重复。
5. drop `gateway_instances_pkey`（singleton_id）；旧 `gateway_instances_instance_id_key` 仍支持所有既有 FK。
6. 用 standalone index `ADD CONSTRAINT gateway_instances_pkey PRIMARY KEY USING INDEX gateway_instances_instance_id_pk_stage`，新 physical PK 现在存在。
7. drop 四个 direct FK：`gateway_directory_ingestion_runs_gateway_instance_id_fkey`、`gateway_directory_snapshots_gateway_instance_id_fkey`、`gateway_directory_current_state_gateway_instance_id_fkey`、`relay_node_gateway_account_bindings_gateway_instance_id_fkey`。不得使用 CASCADE。
8. drop 旧 `gateway_instances_instance_id_key` 及其 backing index；新 PK 继续存在。然后按原名重建四个 FK REFERENCES gateway_instances(instance_id)，ON UPDATE RESTRICT ON DELETE RESTRICT。此时唯一候选 referenced key 就是新 PK，避免 PostgreSQL 重新选择旧 UNIQUE；同事务内短暂拆除 FK 期间不允许并发 writer。
9. drop singleton_id NOT NULL；保留 default=1，retirement 显式写 NULL。
10. 保留/验证既有 singleton_id=1 CHECK 的 nullable 语义，并安装 `gateway_instances_current_slot_uidx` UNIQUE(singleton_id) WHERE singleton_id=1。
11. 在 backfill 后设置 lifecycle_status/revision NOT NULL 并安装完整 CHECK：active/current IFF、retired metadata completeness、revision>=1、retired_at>=created_at、固定 reason taxonomy。
12. 验证 FK、shape、conindid dependency 与最终索引数后 commit；每个 committed boundary 均有完整 FK 和 referenced key。最终 instance_id 仅一个 PK backing index，临时 duplicate index=none。
13. compatibility gate PASS 后由匹配的新 runtime 使用 lifecycle-aware queries。旧 runtime 不在 DDL transaction 内切换。

四个 direct FK 之外，snapshot/run composite FKs 不 drop/recreate，它们的 referenced composite UNIQUE 不变。实际 PostgreSQL 18 DDL proof 是独立 implementation task，必须检查 pg_constraint.conindid、新旧 indexes、并发阻塞、历史 FK 与失败 rollback；本 planning 没有运行 migration/DDL proof，不能把上述设计当成已经执行验证。retired_by 和 lineage replaced_by MUST 是 uuid FK -> control_admin_users.admin_id，ON UPDATE RESTRICT ON DELETE RESTRICT，禁止 login/display name 作为 durable actor。生产不做 destructive down；存在 Phase 6 evidence 后回退旧 schema/binary均受 gate 限制。

Gateway row MUST persist `lifecycle_status`、`retired_at`、`retired_by`、`retire_reason` 和 `revision bigint NOT NULL`。Database MUST enforce `revision >= 1`、states only `active|retired`、`active IFF singleton_id=1`、active retired metadata NULL、retired slot NULL 且 retired metadata complete、`retired_at >= created_at`。`administrator_retire` 与 `replacement` 是共享 machine reason codes；第一版不提供 operator note，未来如增加必须独立于machine reason。

## Shared command receipt and replay

所有 durable admin mutation 带客户端生成的 `command_id`；existing asset mutation 还带 `expected_revision`。HTTP/session preconditions（authenticated、active session、`super_admin`、CSRF）MUST 在进入 durable transaction 前完成；这不绕过 receipt lookup，而是保证未授权请求不能观察 receipt。数据库 receipt 表至少保存 `command_id`、`command_kind`、`intent_encoding_version=1`、`canonical_intent_hash`、sanitized result、actor admin ID、committed time。Receipt immutable，不保存 raw secret reference、credential、headers 或 raw response body。

command_id 是全局 UUID PK，不以 actor 分区。认证/授权/CSRF 后获取 transaction advisory lock：key = SHA-256(UUID 的16字节) 的前8字节按 big-endian signed int64 解释。碰撞只串行化无关 command，lookup 始终使用完整 UUID，不能改变正确性。先查 receipt actor，再用 receipt-recorded encoding/key version 比较 intent，最后才进入 lifecycle/revision validation。

### Canonical intent v1 bytes

canonical_intent_hash = SHA-256(canonical_intent_v1_bytes)，存32字节 bytea。固定 UTF-8 compact JSON array，不使用 object key iteration；无空白、无尾 newline。字符串保留 Unicode code points，不做额外 NFC；只转义 JSON quote/backslash/control，control 固定小写四位 \uXXXX，其他 Unicode 使用 UTF-8 原字节。UUID 用 lowercase hyphenated；revision 用规范正 int64 十进制字符串（API 同样用 string，避免 Web 数值精度损失）。endpoint 用现有 control_normalize_asset_endpoint 的规范化结果；display_name按既有验证、不 trim/改写；opaque reference 按既有 validator 验证后逐字使用，不把 reference 当 credential resolve。

每个 mutable field 使用 `["absent",null]`、`["clear",null]` 或 `["set",value]`；display_name/endpoint 不允许 clear，Edit absent表示不改，明确 set 相同值仍为成功 Edit并推进revision；空patch为400。secret clear允许；Register/Replace缺省secret表示absent（存NULL），与显式clear的intent不同。

固定字段顺序（方括号是实际 JSON array）：
- Register: `[1,"gateway.register",new_instance_id,display_name,normalized_management_endpoint,secret_triplet]`。
- Edit: `[1,"gateway.edit",instance_id,expected_revision,display_name_pair,management_endpoint_pair,secret_triplet]`。
- Retire: `[1,"gateway.retire",instance_id,expected_revision,"administrator_retire"]`。
- Replace: `[1,"gateway.replace",old_instance_id,expected_revision,new_instance_id,new_display_name,new_normalized_management_endpoint,secret_triplet,"replacement"]`。
- secret_triplet = `[operation,secret_fingerprint_key_version,secret_fingerprint]`；operation是absent/clear/set。set时version=1、fingerprint为64字符lowercase hex；其余version/fingerprint均null。

fingerprint = HMAC-SHA-256(K1, UTF8("relay-station/asset-admin-intent/v1/") || UTF8(command_kind) || 0x00 || UTF8(validated_reference))。raw reference MUST NOT 出现在 canonical bytes/receipt。receipt 增加 nullable secret_fingerprint_key_version（set为1，否则NULL）；replay使用receipt的version，而非当前默认值。

K1 为独立32字节稳定 deployment Secret，由受控 Secret 文件提供（CONTROL_ASSET_INTENT_KEY_FILE），不复用 auth/session key，不依赖进程随机数或请求。Phase 6 v1 version=1 non-rotating；未来 rotation 是独立 contract change，v1 receipts 可重放期间 K1 MUST 可解析且不可删除。无key或key版本不支持时 fail closed 503，不退回unkeyed hash、不重新构造key。restart/upgrade沿用K1，key与fingerprint不返回或写日志。凭据文件权限/backup和restore验证作为部署task。

每个 accepted/completed durable command exactly one receipt。状态变更成功写 exactly one mutation audit；state-idempotent completed no-op 不改 domain/revision、不写成功状态转换 audit，但仍写一次 receipt。Health/Connection Test 是 observation，不使用 mutation receipt；每次真实 probe 写一条 sanitized observation audit。

Receipt replay projection MUST 按下文 HTTP contract 的完整success body与status持久化，禁止从后续asset state重构。

## Gateway lifecycle transactions

Register 要求无 current slot、new identity 在全部历史中不存在、metadata 合法、`management_endpoint` 为有效 `http://` origin、super_admin/CSRF 有效；`https://` 在 validation 阶段拒绝且不发 outbound request；同一 transaction 创建 active row、slot=1、revision=1 和一条 receipt/audit。不得 probe、修改 Account/Group/routing 或复用 retired identity。

Edit 只允许 `display_name`、`management_endpoint`、approved opaque secret reference configuration；设置 `management_endpoint` 时同样只接受 `http://` origin，`https://` 在 validation 阶段拒绝且不发 outbound request；active、revision match、command/auth/CSRF 通过后 +1 revision。不得自动 probe。

Retire 在单一 PostgreSQL transaction 中：锁 command；receipt lookup；锁 current Gateway；验证 active/current/revision；按 `relay_node_id ASC` 锁所有 current bindings；建立 DB boundary；以 `gateway_retired` 关闭 bindings；标记 retired、清 slot、revision +1；写 exactly one audit/receipt；commit。不得 network call，历史 binding/Directory evidence 保留，0 current 是稳定状态。

Replace 在单一 transaction 中严格按以下顺序：authentication/session/super_admin/CSRF 已在 transaction 外完成；获取 command advisory lock；receipt lookup（同 actor+same intent 立即 replay，actor mismatch/different intent conflict）；锁 old Gateway；验证 expected revision、active/current、new identity absent 和 metadata；按 `relay_node_id ASC` 锁全部 old current bindings；建立 exactly one replacement boundary；使用该 boundary 关闭 bindings（`gateway_replaced`）；old retired/slot NULL/revision +1/reason `replacement`；new active/slot=1/revision=1；lineage.replaced_at、old.retired_at、binding.ended_at均使用同replacement boundary；retired_by/replaced_by/ended_by均使用同actor_admin_id；insert lineage，写 one mutation audit 和 receipt；commit。任何 domain mutation（含 binding close）不得发生在 boundary 建立前。无 HTTP/network call。新 Gateway zero current bindings，不继承 Account ID binding、Directory freshness/current snapshot 或 source time。

## Lineage and read model

`gateway_asset_replacements` 为 dedicated immutable table，含 old/new FK、`UNIQUE(old)`、`UNIQUE(new)`、`CHECK(old <> new)`、boundary、actor、command_id。禁止 UPDATE/DELETE/TRUNCATE；A->B->C 合法，self-reference、分叉、汇合和 cycle 不可由合法 command 创建。

`GetGatewayAsset` 继续读取 `singleton_id=1`；`ListGatewayInstanceIDs` 只返回 current active operational target；reconcile 校验 current slot count <= 1，不再按历史总行数判断。Gateway current singular API 保持，历史 rows 按 stable identity 可查询，detail 展示 predecessor/successor；默认 operational view 只显示 active/current。

`GetAssetCounts` 原 gateways 是 total row count；为避免静默改变语义，原 `gateways` MUST 保持 total（包括历史）。新增 `gateway_counts:{active,retired,total}`，total=gateways、active仅slot=1、retired仅retired；现有nodes及其它count字段不改。所有 operational UI必须改用gateway_counts.active，不以gateways判断singleton违规。Gateway summary/list返回gateway_counts；不新增 Node lifecycle projection。

## Directory and binding interaction

Planner 只为 active/current Gateway 建 normal run；无 current 不创建新 run。Directory target 使用 current Gateway 的 HTTP-only `management_endpoint`；复用现有 `gatewaydirectory` validator/client，在任何 outbound 前拒绝 `https://`。run fetch 前和 finalize/promotion transaction 都 re-read/lock Gateway active/current 与 source identity；不满足则 no fetch/no promotion/no freshness refresh，历史 transport evidence 可保留。Replace 后 new Gateway freshness/current snapshot 独立开始。

Gateway Retire/Replace 锁所有 current bindings 并分别使用 `gateway_retired`/`gateway_replaced` 关闭。任何 bind/rebind 在 commit 前必须持有稳定 Node asset -> target Gateway asset -> Gateway Directory current-state -> current binding rows lock order，并验证两者 active、Gateway slot=1；Gateway lifecycle command 先 commit 时 bind/rebind conflict，binding 先 commit 时随后被 lifecycle transaction 关闭，最终不得存在 retired Gateway + current binding。

## Compatibility and rollback

本 change 拥有独立 deployment artifact `relay-control-compat-gate` 和正式 startup wrapper。它们安装于支持的 Compose/systemd deployment（gate版本独立于Control image/binary rollback unit）；release operator部署。不可绕过指受支持 production deployment path，不声称能阻止系统管理员任意手工 docker run。

### Artifact trust and class

权威来源是 release authority 签名的 manifest。manifest v1 payload 是 UTF-8 compact JSON array `[1,control_artifact_digest,compatibility_class]`，digest格式 `sha256:<64 lowercase hex>`，class非负整数；签名为Ed25519，gate使用独立部署、root-owned read-only公钥验证。release signer私钥不在runtime operator环境中；operator不能通过环境变量、image自声明label或修改manifest提升class。

裸binary用SHA-256文件内容校验；OCI用selected immutable image manifest digest校验且只能按该digest启动，禁止mutable tag。gate对验证后的同一binary FD/immutable image digest执行wrapper启动，防止检查后换artifact。Pinned source 5e2caeb031a47510744cada56978b963da39b4e9 对应 acceptance 实际构建/选择的 artifact digest 由release manifest登记class=0；Phase6-aware已验收artifact登记class=1。这里不猜binary/image digest。缺manifest、签名无效、digest不匹配、unknown schema/class全部exit78，不启动Control。验收加入“把old artifact改标class1”拒绝场景。

### Marker and rollout

Control PostgreSQL public.control_runtime_compatibility：singleton_id=1、schema_version=1、phase6_evidence_floor整数0或1（单调不可降低）。forward migration写floor=1并commit；这是保守schema barrier，即使尚无业务mutation也拒绝class0，避免旧writer在新schema下继续工作。retired/lineage/receipt/cancellation evidence存在时floor至少1；floor缺失但Phase6 migration已存在、floor错误或DB不可读都fail closed。gate用只读DB权限查询marker/migration版本；尚无Phase6 migration且无marker才按floor0。未知schema或class低于floor exit78；DB不可达exit75；失败不写DB。

支持的release顺序 MUST 为：
1. 停止旧Control和自动restart；deploy/update外部gate、签名信任根与强制wrapper。
2. 验证Compose entrypoint/systemd ExecStart只经过wrapper，preflight失败绝不启动Control；runtime DB credential不作为未校验进程的启动输入。
3. operator执行forward migration并commit floor1（本轮不执行）。
4. wrapper验证selected artifact digest/signature/class，读取DB floor，通过后启动同一artifact。
5. Control完成DB检查后启动scheduler/workers与HTTP；全部检查PASS后才开放mutation surface。
6. 持续使用同一wrapper作为restart/rollback入口，gate不随app回滚。

Rollback选择pinned class0 artifact -> 同一正式wrapper -> 验证manifest/digest -> class0<floor1 -> fail closed，Control process根本不启动，HTTP、Directory、Inventory均不能先启动。验收必须使用实际支持的Compose/systemd wrapper，不得用单独调用gate代替；对比DB truth前后相同并保留进程/HTTP/worker未启动证据。manifest trust、mandatory wrapper和真实PG18 proof尚待implementation tests，本design冻结不等于runtime acceptance已通过。

## Security and verification

所有 mutation 仅 super_admin、CSRF、Cache-Control no-store；raw secret reference、credentials、credential-bearing headers、raw probe body 不进入 API/UI/audit/receipt/metrics/logs。Health/Connection Test 使用资产中已验证的 HTTP-only `management_endpoint`，固定 bounded `GET /health`，observation-only，不改变 lifecycle/revision、Directory 或 routing；无 redirect/retry/fallback。当前 runtime 不发起 TLS；`https://` target 在 probe 前拒绝并保持零 outbound。Control 不进入 data plane、不持有 Gateway DB access、不提供 Account/Group CRUD。

后续实现必须覆盖 migration/FK safety、sqlc generation、Directory/binding races、command replay/no-op、stale revision、security-negative、production build、old-binary rollback acceptance 和 clean evidence；本 artifact 只做 planning。

## HTTP contract

本节冻结API映射，implementation仅按此更新OpenAPI与generated clients。POST/PATCH沿用现有unsafe-request middleware（缺CSRF可先403），全部安全检查在receipt可见前完成。所有接口沿用Control session cookie、实名super_admin与Cache-Control:no-store；POST/PATCH使用既有CSRF header。Health是显式GET观察，仍须有效super_admin；不得由列表自动触发。未知request字段拒绝，不返回secret reference。

| Action | Method/path | JSON request | Success |
|---|---|---|---|
| Register | POST /api/assets/gateways | command_id,new_instance_id,display_name,management_endpoint；reader_secret_ref可省略/string/null | 201 RegisterResult |
| Edit | PATCH /api/assets/gateways/{instance_id} | command_id,expected_revision；display_name/management_endpoint/reader_secret_ref为显式patch字段 | 200 EditResult |
| Retire | POST /api/assets/gateways/{instance_id}/retire | command_id,expected_revision；machine reason由server固定administrator_retire，无free-text reason | 200 RetireResult |
| Replace | POST /api/assets/gateways/{instance_id}/replace | command_id,expected_revision,new_instance_id,display_name,management_endpoint；reader_secret_ref可省略/string/null | 200 ReplaceResult |
| Health | GET /api/assets/gateways/{instance_id}/health | 无body，无command_id/revision | 200 ProbeResult |
| Connection Test | POST /api/assets/gateways/{instance_id}/connection-test | 空object；无command_id/revision | 200 ProbeResult |

command_id在body中为UUID；expected_revision在body中为规范正int64十进制string，范围1..9223372036854775807，response revision同型。Register没有expected_revision。path identity为UUID，body不可另传冲突identity。display_name/endpoint必须有效非null，`management_endpoint` 必须是 `http://` origin；`https://` 返回 `400 invalid_endpoint` 且零 outbound。Edit省略表示不修改；reader_secret_ref省略/显式null/合法string分别为absent/clear/set。第一版不提供operator note。Gateway无DELETE能力。revision溢出返回409 revision_exhausted且无mutation。

### Exact success bodies and receipt projection

AssetResult固定字段：instance_id、lifecycle_status(active|retired)、revision(string)、display_name、management_endpoint、secret_configured(boolean)、created_at、updated_at、retired_at(nullable UTC timestamp)、retired_by(nullable admin UUID)、retire_reason(nullable administrator_retire|replacement)。无raw secret_ref。

- RegisterResult = {result:"registered",asset:AssetResult}。
- EditResult = {result:"updated",asset:AssetResult}。
- RetireResult = {result:"retired",asset:AssetResult,closed_binding_count:nonnegative integer}。
- ReplaceResult = {result:"replaced",old_asset:AssetResult,new_asset:AssetResult,closed_binding_count:nonnegative integer,lineage:{old_instance_id,new_instance_id,replaced_at,replaced_by,command_id}}。
- ProbeResult = {instance_id,result:"healthy",observed_at:UTC timestamp}；在已验证的 HTTP-only management origin 上固定GET /health、5秒总timeout，无redirect/retry/fallback、不携带reader credential、不持DB事务做network call。`https://` 在 client construction 前拒绝且零 outbound；当前 runtime 不发起 TLS。retired资产拒绝probe；已经授权的probe可作为独立transport observation收尾，不推进revision/Directory。

receipt.sanitized_result MUST 保存对应完整success body及http_status，不通过后续asset row重构。same actor/same intent replay返回原status和body（Register仍201），request_id作为本次传输错误跟踪而非旧success body字段。success audit、asset mutation、lineage、receipt同事务；audit/receipt失败全部rollback。

### Error contract

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
| 502 | probe_failed | HTTP transport/non-200/redirect等固定probe失败，raw error/body不回显；`https://` target 在 outbound 前以400 invalid_endpoint拒绝 |
| 503 | service_unavailable | DB、receipt key或内部依赖不可用，fail closed |

### Current/history reads, cursor and counts

GET /api/assets/gateway 保持现有response envelope（包括空Gateway的既有空态），target仅active/current；Gateway对象additive新增lifecycle_status/revision/retirement fields。GET /api/assets/gateways/{instance_id} 返回 {asset:AssetResult,predecessor:Lineage|null,successor:Lineage|null}，缺失404；Lineage字段同ReplaceResult。GET /api/assets/gateways 返回 {items:AssetResult[],next_cursor:string|null,gateway_counts:{active,retired,total}}。

list query生命周期过滤名lifecycle，值active/retired/all，缺省active；limit默认50、上限100、下限1。沿用Node的stable identity排序：ORDER BY instance_id ASC，cursor keyset为last_instance_id。cursor为有完整性保护的opaque payload，包含encoding_version=1、environment、lifecycle、last_instance_id和registry_generation；复用当前Node cursor签名/validation机制。修改filter或损坏cursor返回400 validation_failed。

registry_generation定义为同一REPEATABLE READ查询快照下所有Gateway revision之和（numeric无溢出）。Register增加1，任何Edit/Retire增加1，Replace增加2；不允许identity delete/revision降低，因此是单调generation。第一页面保存generation，后续page在同一事务内验证generation并查询；generation变化则409 cursor_stale，不在变化集合上静默遗漏/重复。合法cursor链因此对应同一稳定Gateway集合，无需跨HTTP持长DB transaction。没有变化的集合按UUID keyset恰好一次返回；并发mutation时明确重启。Directory observation不改generation；historical rows与lineage不可改写。detail是当前读取，不声称与旧list cursor共享快照。

GetAssetCounts原gateways继续表示total rows（不静默改成active）；新增gateway_counts:{active,retired,total}，gateways=total。原nodes/drivers等字段保持。UI、reconcile不得用gateways total推断current slot个数；operational UI改用gateway_counts.active，数据库shape约束负责single-current。

## Audit and metrics contract

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

## Shared gate acceptance contract

### Requirement: Shared compatibility barrier SHALL verify selected artifact

本 change MUST 提供application rollback unit外的relay-control-compat-gate和mandatory supported deployment wrapper。class权威来源是独立release authority的Ed25519-signed manifest v1，payload=[1,control_artifact_digest,compatibility_class]。gate验证signature与selected binary SHA-256/OCI manifest digest；拒绝自由环境变量、自声明label和不匹配digest。Pinned 5e2caeb031a47510744cada56978b963da39b4e9 的验收artifact登记class0，Phase6-aware正式artifact class1；实际digest在构建验收时记录，不猜测。

DB public.control_runtime_compatibility单例schema_version=1、phase6_evidence_floor=1由forward migration写入并只增不减；migration后即floor1。强制顺序是停止old process/restart -> deploy gate/trust root/wrapper ->验证mandatory path -> forward migration/floor -> selected artifact验证+DB gate -> start Control ->开放mutation。signature/digest/schema/class错误exit78、DB故障exit75，process不启动。supported Compose/systemd wrapper不得绕过；不声称阻止host root手工执行任意docker run。rollback仍用同wrapper，不回滚gate/trust root，class0<floor1时old process不启动。receipt/lineage历史不清理以绕过floor。

#### Scenario: Pinned old artifact rollback through production wrapper
- **WHEN** 正式wrapper选择pinned class0 artifact且数据库已有Phase6 evidence/floor1
- **THEN** gate拒绝，HTTP/Directory/Inventory process均未启动，DB truth保持不变；必须记录wrapper验收而非只调用gate

#### Scenario: Tampered compatibility class
- **WHEN** operator把old artifact的class改为1、替换binary或选择不同image digest
- **THEN** signature/digest验证拒绝；不能以environment override提升class

#### Scenario: Missing metadata or unavailable database
- **WHEN** manifest缺失、签名无效、marker与migration不一致或DB不可读
- **THEN** fail closed，不启动Control；不从operator变量猜测class/floor
