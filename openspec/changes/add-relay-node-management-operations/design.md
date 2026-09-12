## Context

基线为 Control `f8e600e28f22f88d672fff550c9d0ca2cfea1d2d`（clean main）。Stage 1/2 只有已批准 planning，当前 migration/Go 中尚不存在 lifecycle/revision、shared receipts、cancellation columns 或 asset_registry_generations。本文依赖未来先实施的 Stage 1/2 schema，绝不把 planning commit 当 accepted runtime binary。

已核对 `migrations/00003_asset_registry_foundation.sql`：activation UUID PK、Node FK、半开 active_range、GiST exclusion、reason/actor/end metadata、effective_to>effective_from、禁止回填；`control_set_node_inventory_monitoring` 已先锁 Node，但现版本只处理一个命中区间，不会取消所有 future rows，不能直接作为产品 Disable 完整实现。

Stage 2 已冻结 cancelled_at/cancelled_by/cancel_reason、empty active_range、immutable history、Node-first locks、node_generation/read_as_of。Stage 3 只扩充 reason、一个 shared-receipt partial expression index 和最小受控函数，不新建表/列或新的业务状态。

`internal/drivers/cliproxyapi/driver.go:Probe` 只调用 health，不 Resolve Secret；`health.go` 有 closed parser；`transport.go` 固定路径、无代理/redirect/retry；`target_policy.go` 已只接受 HTTP。最新 `internal-http-transport` / `control-management-outbound-transport` 优先于旧 readonly-driver 中残留 HTTPS 描述。`internal/api/server.go` 已有 requireSession/sameOrigin/CSRF/no-store/有界 JSON/error envelope。`web/src/pages/AssetRegistryView.tsx` 与生成客户端是唯一 UI 扩展位置。

## Goals / Non-Goals

增加两个独立的显式 product surface：Node Health（GET observation）与 Node Connection Test（POST explicit admin action），共享同一个底层 `Driver.Probe` 实现，不新增第二套 transport/client；以及 immediate monitoring commands 和相应 Asset Registry surface。不引入任何可被页面自动轮询的后台 probe。页面进入、刷新、列表读取不自动 probe。其余边界见 proposal；不改任何账号或 binding truth。

## Decisions

### 1. Exact HTTP contract

复用 plural `/api/assets/nodes/{instance_id}` 命名。Health 是只读 GET route，复用既有 read-only GET 安全惯例（active authenticated super_admin session、`Cache-Control: no-store`），不要求 CSRF——它与其它资产 GET 路由遵循相同安全 contract，不是 mutation。Connection Test、Enable、Disable 均为 Control-side POST，使用 active authenticated super_admin session、same-origin、`X-CSRF-Token` 和 `Cache-Control: no-store`（含错误），这避免跨站触发有网络成本的显式 admin 动作。

| Action | Method/path | JSON body | command_id | expected_revision | Success |
|---|---|---|---|---|---|
| Health | `GET /api/assets/nodes/{instance_id}/health` | 无 | 无 | 无 | 200，安全 observation（失败观察也200） |
| Connection Test | `POST /api/assets/nodes/{instance_id}/connection-test` | `{}` | 无 | 无 | 200，安全 observation（失败观察也200） |
| Enable | `POST /api/assets/nodes/{instance_id}/monitoring-enable` | `{"command_id":"uuid"}` | 必须 | 无 | 200，完整 persisted result |
| Disable | `POST /api/assets/nodes/{instance_id}/monitoring-disable` | `{"command_id":"uuid"}` | 必须 | 无 | 200，完整 persisted result |

Health 与 Connection Test 都不接受 body 字段、不使用 `command_id`/`expected_revision`、不产生 receipt。两者共用完全相同的底层短事务授权 + `Driver.Probe` 调用路径（见 §2）与响应模型（见下），仅在 HTTP method/path、安全惯例（GET 无 CSRF vs POST 有 CSRF）和 audit/metrics action 上区分，不复制 parser、HTTP client 或 response model。

Enable/Disable 的 UUID 为非零 UUID，规范化 lowercase hyphenated。Content-Type application/json；body 最大 1 KiB，拒绝 unknown/duplicate fields、尾随 JSON、query parameters。effective_from/timezone/schedule/cron/expected_revision/endpoint/method/path/header/body/credential 均不是输入字段；不允许透传客户端 Authorization/Cookie 到 Node。JSON 错误/超限统一400 validation_failed，不回显输入。现有资产 GET route 不改变。

Observation body 固定为 `{"result":"success|failure","reachable":true|false,"reason":"enum","latency_ms":0}`。`reachable=true` 仅代表现有 parser 证明 HTTP 200、有界 JSON object、status=ok，不代表模型/Provider/账号健康。latency_ms 为单次 Probe 单调时钟耗时的非负整数，限制0..30000（终止清理超限时饱和）；不返回 endpoint、IP、原 status/body/headers 或 Driver debug detail。reason 固定为 `none|http_status|response_invalid|response_too_large|timeout|cancelled|network_unavailable|dns_rejected|tls_rejected|redirect_rejected|target_rejected`；驱动未知 error 映射 network_unavailable。HTTP-only 下 TLS handshake 不会执行；tls_rejected 只作现有 typed error 防御映射/单元 fixture，不引入 HTTPS route。非200包含所有非redirect HTTP失败；3xx映射redirect_rejected且无第二请求。

Monitoring success body（四类 result，字段均必返）：

```json
{"result":"enabled|already_enabled|disabled|already_disabled","instance_id":"uuid","lifecycle_status":"active","revision":"1","boundary":"RFC3339 UTC","monitoring_active":false,"monitoring_activation_id":null,"effective_from":null,"effective_to":null,"closed_monitoring_count":0,"cancelled_future_monitoring_count":0}
```

Enable 类返回当前 activation UUID/from/to（already_enabled 可为已安排未来关闭的 current interval）；Disable 类返回 false、UUID/from/to 全 null，counts 精确表示本次关闭/取消数量。revision 是命令时 Node revision 原值，不是 concurrency token。boundary 是本次 DB evaluation instant。receipt 保存完整原200 body；后续 Node 变化或退休后 replay 仍返回原结果，UI 必须另读 detail 刷新，不能把 receipt 当最新状态。响应不包含 scheduling rows/actor/secret/ref/endpoint，不为 replay 扫描当前资产。

错误统一既有 `{code,message,request_id}` envelope，message 固定、不携带 raw error。`csrf_invalid` 只适用于 Connection Test/Enable/Disable 三个 POST route；Health 是安全 GET，不校验 CSRF/same-origin，与其它资产 GET 路由遵循同一 read-only 安全 contract：

| HTTP | Code | Trigger |
|---|---|---|
| 400 | validation_failed | UUID/body/query/unknown field/非法格式 |
| 401 | unauthorized | 无有效 session（先于资产/receipt visibility） |
| 403 | forbidden | 非super_admin（四个 route 均适用） |
| 403 | csrf_invalid | CSRF或same-origin失败（仅 Connection Test/Enable/Disable 三个 POST route） |
| 404 | asset_not_found | 无receipt且Node不存在 |
| 409 | asset_retired | 无receipt且Node已retired |
| 409 | command_conflict | receipt actor或intent不同（仅 Enable/Disable） |
| 409 | monitoring_future_conflict | Enable发现uncancelled future activation |
| 409 | monitoring_boundary_conflict | 不可合法关闭的零长度或不一致区间 |
| 409 | monitoring_state_conflict | 仅限 overlap/inconsistent persisted interval 真相完整性冲突（狭义 state-integrity conflict）；不再用于任何 generation/scheduling-intent 场景 |
| 409 | generation_exhausted | node_generation无法递增，整事务失败 |
| 409 | capability_unsupported | health缺health capability/driver不匹配 |
| 503 | service_unavailable | DB/受控函数/审计/基础配置不可用 |

remote失败是200 observation failure，不伪装Control DB故障；启动前拒绝非法内部HTTPS配置，动态registry target被拒绝时只返回安全target_rejected observation。无raw PostgreSQL 23505/23P01泄漏。CSRF失败复用既有security audit。

### 2. Probe reuse and DB/network boundary

Health 与 Connection Test 均使用 registry 已存 Node target + `management_health_read`；不要求 monitoring enabled 或reader_secret_ref存在。不调用 auth-files、Usage Queue、Gateway、模型数据面。两者复用完全相同的现有 Driver registry/ProbeRequest/ProbeObservation/health parser 调用路径，不新增第二套 client abstraction，仅入口 route 不同。

认证后短事务 `Node FOR UPDATE`，验证 active、driver/capability，并复制 approved endpoint/identity；commit/release 后才调用 Probe。commit 是本次 ephemeral probe authorization 的线性化点：Retire/Replace先提交则409且零HTTP；authorization先提交则允许该有界观察结束，即使随后退休，不改变任何current truth。Edit先提交读新endpoint；authorization先提交只使用已读endpoint一次。不得把该内存授权复用于第二次请求、restart或自动重试，不复用 Inventory durable dispatch columns。

固定拼接复用 transport 的 base-path append（registry `http://node/base` -> `/base/healthz`；无base path -> `/healthz`），禁止query/fragment/userinfo，普通DNS/direct HTTP，无代理、cookie、credential、redirect、retry、fallback。复用 ManagementConfig：connect默认3s、允许100ms..10s；总请求默认15s、允许1..30s且不少于connect；健康body默认64KiB、允许128B..1MiB；调用context不得晚于总timeout，取消立即终止。非200 body不读取；200只保留parser的固定结果，unknown fields立即丢弃。

每次实际调用 Probe 完成后，用独立短DB事务写一条 sanitized observation audit，成功/失败均记录，不写receipt、health table、last-health列或Inventory evidence。Health route 写 `node.health` action；Connection Test route 写 `node.connection_test` action；两者details固定为canonical Node UUID `instance_id`、bounded `result/reason/latency_ms`，仅 action 值不同，用于区分独立 metrics/audit 计数，不得混淆或合并计数。写audit失败返回503，不返回未审计成功、不自动重做HTTP；进程在HTTP与audit之间crash无法原子保证远端观察与审计，不声称跨网络exactly-once，不新增durable health job/outbox来弥补。已提交独立资产command绝不因probe失败回滚。

### 3. Monitoring persistence and exact reasons

复用 `relay_node_inventory_monitoring_activations`。最终 allowlist：

| Field | Complete allowed values |
|---|---|
| reason | deployment_enable, scheduled_enable, reconciliation, **administrator_enable** |
| end_reason | deployment_disable, scheduled_disable, reconciliation, node_retired, node_replaced, **administrator_disable** |
| cancel_reason | node_retired, node_replaced, **administrator_disable** |

Enable 写 reason=administrator_enable、actor=认证admin UUID canonical text、created_at=effective_from=boundary；不能冒充deployment/reconciliation。Disable current写 end_reason=administrator_disable、end_actor=相同UUID text、end_recorded_at=effective_to=boundary。future写 cancelled_at=boundary、cancelled_by=admin UUID FK（ON UPDATE/DELETE RESTRICT）、cancel_reason=administrator_disable；保留原effective_from/to/reason/actor/created_at和既有end metadata。Stage 2 cancellation triple/immutable/empty-range guards保持；不把end_actor改成UUID列、不迁移历史actor。

current定义为未取消且 `boundary <@ active_range`；future定义为未取消且 effective_from>boundary，包括已有future effective_to的scheduled interval。按effective_from ASC, monitoring_activation_id ASC锁所有affected rows。cancelled rows不参与overlap/current/future eligibility；不允许uncancel/DELETE/TRUNCATE或追溯改写已结束UTC日。boundary==current.effective_from时按Stage2返回409 monitoring_boundary_conflict，全部回滚，不制造zero-length interval。

### 4. Shared receipt, encoding and revision

HTTP auth/session/super_admin/CSRF先完成；事务先拿由command UUID派生的advisory lock，再以完整UUID PK做单行receipt lookup；先actor再intent，最后domain locks/state evaluation。不同actor或intent均409 command_conflict，不暴露旧结果。command kind注册两值，不修改Stage1/2既有数组。

精确canonical v1 UTF-8 compact JSON（无空白/BOM/newline）：

```text
[1,"node.monitoring_enable",instance_id,"administrator_enable"]
[1,"node.monitoring_disable",instance_id,"administrator_disable"]
```

字节fixture（固定UUID，不含command_id/actor/DB时间，因为command_id全局PK、actor独立校验、boundary服务端产生）：

```text
[1,"node.monitoring_enable","11111111-1111-4111-8111-111111111111","administrator_enable"]
[1,"node.monitoring_disable","11111111-1111-4111-8111-111111111111","administrator_disable"]
```

canonical_intent_hash=SHA-256(bytes)，32-byte bytea；intent_encoding_version=1；不含Secret intent，secret_fingerprint_key_version=NULL，不读取/生成fingerprint，不改变shared K1/v1稳定与历史replay规则。测试逐字节及hash hex golden（见planning-validation），同UUID大小写输入规范化一致。

每个accepted completed command（含新command_id的state-no-op）恰好一receipt；state-changing success同事务一transition audit；no-op无domain/generation/revision变化、无transition audit但有一receipt；same-command replay无第二receipt/audit，返回原status/body。rollback无receipt，commit-response-loss安全重放。任何无receipt的retired操作409，不能伪造no-op。receipt lookup先于retired/current state，因此历史成功仍可replay。

依据Ops revision条款和Stage1 shared spec，Monitoring不使用expected_revision、不推进asset revision或updated_at；使用Node/interval locks防并发，node_generation用于列表一致性。不能因响应包含revision而要求If-Match；不新增token、receipt表或boolean shadow state。

### 5. Disable receipt race fence

现有 shared `asset_admin_command_receipts` 足以表达 Disable race fence：每个 accepted state-changing 或 already-disabled no-op command 都在持有 Node row lock 的 transaction 内恰好写一条 immutable `node.monitoring_disable` receipt，其完整 `sanitized_result` 顶层已有 canonical `instance_id`，并有 `committed_at` 与全局唯一 `command_id`。因此 fence token 冻结为该 Node 最新 committed Disable receipt 的 nullable `command_id`；latest只由严格单调的`committed_at`决定，`command_id`绝不承担temporal ordering。尚无 receipt 时 token 为 NULL。same-command replay不写第二receipt，因而不产生新fence；新的already-disabled command仍写新receipt并产生新fence。

同一Node的所有新Disable由Node row lock严格串行。取得Node lock后、写receipt前，transaction MUST先读取该Node latest Disable receipt的`previous_disable_fence_at`，再分配：`new_disable_fence_at = CASE WHEN previous IS NULL THEN clock_timestamp() ELSE GREATEST(clock_timestamp(), previous + interval '1 microsecond') END`，并将其写为新receipt的`committed_at`。该值必须finite且可表示；加1微秒溢出或无法得到严格更大值时以固定internal conflict fail closed，整事务rollback。由此对同一Node冻结：Disable A在Node lock serialization中先于B，必有`A.committed_at < B.committed_at`，与transaction启动顺序或随机UUID值无关。其它command kind的shared `committed_at`语义不变。

bounded lookup 使用唯一最小 additive **UNIQUE partial expression index** `asset_admin_command_receipts_node_disable_fence_idx`：key 为 `(sanitized_result->>'instance_id', committed_at DESC) INCLUDE (command_id)`，partial predicate固定`command_kind='node.monitoring_disable'`。查询同时固定相同predicate与canonical UUID text，`ORDER BY committed_at DESC LIMIT 1`，不得扫描无界receipt history。该index只是immutable receipt truth的查询及同一 Node timestamp tie拒绝支持，不是新durable business truth；Stage3 receipt写入约束必须保证此kind的result顶层存在canonical `instance_id`。如果检测到违反strict monotonic invariant的同一 Node timestamp tie，必须fail closed，不得按`command_id`大小猜测later Disable。

Stage1 receipt immutable guarantee在此是correctness dependency：所有`node.monitoring_disable` receipts MUST NOT UPDATE、DELETE或TRUNCATE；runtime、operational role与PUBLIC无此权限，数据库guard必须拒绝绕过。任何支持operational scheduling intent的部署MUST NOT GC/prune最新fence receipt，本change不建设第二套retention framework。这样F0与F1不能因receipt移除而从non-NULL错误退回NULL；retention/immutability必须有migration regression test。

所有受支持的 existing/future operational scheduled writer 在形成一次具体scheduled intent时，MUST先通过受控bounded lookup取得 `F0`，随后把这个nullable UUID作为该次intent不可变参数传入write入口。write transaction MUST 使用 PostgreSQL READ COMMITTED；写入函数必须显式为 `VOLATILE`，先 `Node FOR UPDATE`、校验lifecycle=active，再在取得Node锁后执行下一条内部SQL查询，以VOLATILE函数的fresh snapshot读取同一bounded lookup得到 `F1`。`F1 IS DISTINCT FROM F0` 时抛出SQLSTATE `55000`并映射固定运维结果`monitoring_disable_fence_conflict`，零activation write；调用方不得以同一个旧intent自动重试，必须重新形成intent。相等时才按稳定顺序锁monitoring rows并执行既有禁止回填/overlap/reason等preconditions。PostgreSQL 18 migration proof MUST验证等待Node lock后F1确实观察到先commit的Disable receipt，并验证strict monotonic assignment而不是UUID决定latest。

为防止绕过，Stage3 forward migration以需要expected Disable fence token的新受控函数签名替换现有5参数 `control_set_node_inventory_monitoring` 写入口，撤销/删除旧签名的runtime/registrar/PUBLIC EXECUTE；受支持的 `deploy/asset-registry/set-node-monitoring.sql` 先取F0再调用新入口。NULL是“形成intent时尚无Disable receipt”的显式token，不是省略校验。不得提供无token fallback，也不得使用Node revision、updated_at、`node_generation`、进程内counter或wall-clock作为fence。

这不是durable disabled latch。它只比较一次intent形成时的F0和真正取得Node锁后的F1：writer先commit时F0=F1，Disable随后锁定并取消该row；Disable先commit时等待中的旧writer看到F1变化并conflict；Disable commit后才形成的新writer捕获新F0，若没有更晚Disable则F0=F1并可按既有operational rules执行。进程restart丢弃尚未提交的ephemeral F0/intent；durable scheduled row若已先commit则由Disable取消。

### 6. Enable / Disable transaction order

共同顺序：auth → command advisory lock → receipt lookup → Node FOR UPDATE → lifecycle校验 → 建立单一 `clock_timestamp()` DB boundary → inspect/lock current+future monitoring rows（稳定顺序）→ domain mutation → **仅当Stage3 product operation实际改变current monitoring projection时**锁Stage2 generation row并将`node_generation`恰好+1 → audit/receipt → commit。Disable在Node lock后、monitoring boundary/mutation前还按§5读取previous fence并分配strictly-greater `new_disable_fence_at`，最终receipt使用该值；Enable没有此步骤。future-only cancellation是domain mutation但不锁定/更新generation；already-enabled/already-disabled不做domain mutation且不锁generation。Node锁是第一层domain serialization；不拿Gateway/Directory/binding，不跨事务HTTP。

Enable先检查future（即使有current也冲突），再检查current。无future且current存在：already_enabled receipt-only，保留原planned close；两者皆无：建一immediate open interval，generation+1、一次success audit、receipt。没有history不意味着capacity或policy已满足；不额外解析Management Key或测试Node，也不改policy/Inventory。

Disable关闭current（含已预约未来结束的current row）到同boundary，取消所有future。只有current monitoring projection实际改变（即current interval被关闭）时，`node_generation`才在同一transaction中恰好+1；仅取消future且没有current时，`node_generation` MUST NOT改变。无current且无uncancelled future：already_disabled receipt-only，仍提交新的Disable receipt/fence，以失效此前形成但尚未提交的scheduled intent；它不写transition audit或generation。future-only cancellation仍是真实monitoring domain mutation，按真实mutation写transition audit和receipt。任何约束、generation overflow、audit或receipt失败整事务rollback，绝不异步清理剩余future。Disable不建立durable disabled latch；只有Disable commit后才形成并捕获最新fence的新operational intent MAY按既有Node-lock/lifecycle/monitoring preconditions创建新的future activation。

受控实现采用最小monitoring command函数/Store事务，复用shared receipt helper和Stage2 cancellation guard，不直接复用只返回一个activation ID的旧registrar函数来假装完整Disable。runtime只有对应SECURITY DEFINER函数EXECUTE（migrator owner、fixed pg_catalog、全限定对象名、PUBLIC revoke），无activation底表任意UPDATE/DELETE/INSERT权限。

### 7. Concurrency and scheduling writers

所有monitoring write transaction以Node row `FOR UPDATE`作为第一层domain serialization boundary，并在Node lock之后按`effective_from ASC, monitoring_activation_id ASC`稳定顺序锁定所需monitoring rows；scheduled writer在进入write transaction前只执行§5的bounded F0 capture，不持有domain lock。Stage 3 product Enable/Disable只有在实际改变current monitoring projection时，才在monitoring-row locks之后额外锁定并推进Stage 2 generation单例；future-only cancellation不得锁定或推进`node_generation`。`node_generation`仅保留Stage 2已冻结职责——Node list/projection cursor consistency，MUST NOT被用作operational scheduling intent precondition、scheduled writer的optimistic concurrency token或Disable race fence。

| Race | 唯一允许的serialization结果 |
|---|---|
| 两个Enable，无future | 首个创建；另一新command返回already_enabled，generation只增一次 |
| 两个Disable | 首个关闭/取消；另一新command返回already_disabled，只有首个有transition audit |
| Enable vs Retire/Replace | Enable先commit则lifecycle随后关闭/取消；lifecycle先commit则Enable409；old最终retired且无eligibility |
| Disable vs Retire/Replace | Disable先commit保留administrator_disable历史，lifecycle不重写reason；lifecycle先commit则新Disable409；原receipt仍可replay |
| 既有 operational scheduled writer 先commit | product Enable 随后看到 uncancelled future 时 409 monitoring_future_conflict；product Disable 随后锁到该 future row 时原子 durable cancel |
| product Disable 先commit，Disable前已形成的 operational scheduled intent 随后取得 Node lock | F1与其捕获的F0不同，固定conflict，零activation write；already-disabled Disable同样写receipt/fence并产生此结果 |
| product Disable commit后才形成的新 operational scheduled intent | 捕获最新F0；若取得Node锁前没有更晚Disable，则F1=F0，按既有operational preconditions正常执行 |
| Connection Test/Health vs Retire/Replace | 使用§2短事务授权commit排序；probe不修改任何lifecycle/monitoring/current truth |

既有 operational scheduled writer（`control_set_node_inventory_monitoring` 的Stage3受控替代签名）保持 Ops 已冻结的domain序列化契约：捕获F0 → `Node FOR UPDATE` → lifecycle `active` 校验 → bounded F1校验 → 稳定顺序锁 monitoring rows → 该 writer 自身既有的 operational preconditions（例如禁止回填的 `requested_effective_at` 校验）。Stage 3 MUST NOT 为该 writer 新增任何 `node_generation` 读取或前置校验；F0/F1只失效跨越一次已提交Disable的旧intent，不自动rebase，也不永久禁止后续intent。

**Boundary note**：Disable必须失效在其commit前已形成但尚未提交activation的operational intent；它不失效Disable commit后才形成的新intent，也不建立durable disabled latch。`node_generation`不参与此协议。

### 8. Current reads and UI

`node_generation` 精确冻结规则（只保留 Stage 2 已批准职责：Node list/projection cursor consistency）：

| 场景 | node_generation |
|---|---|
| Enable：disabled → 立即建立 current | 改变 monitoring current projection → generation+1 |
| Enable：already current（no-op） | 不改变 generation |
| Disable：current interval 被关闭 | 改变 monitoring current projection → generation+1（恰好一次） |
| Disable：current + future 同时处理 | current 改变 → generation+1（恰好一次，不因 future row 数量额外增加） |
| Disable：只有 future、没有 current | future cancellation 不改变当前 Node list/default current projection → 不改变 generation |
| Disable：无 current、无 future（no-op） | 不改变 generation |
| receipt replay / Health / Connection Test | 不改变 generation |

Stage 3冻结的Node default/detail current projection不包含future schedule truth，因此future-only cancellation不推进generation；实现不得自行改变此规则。

Stage2 read_as_of与cursor filter绑定不变；旧cursor在实际generation变化后409 cursor_stale。UI mutation完成后弃旧cursor、重新detail/list读取，即使返回的是历史receipt也如此。

Disable只改变monitoring truth：既有scheduler/claim/outbound/promotion/read-model使用eligibility停止current target，不直接写poll、snapshot、account lifecycle、availability、request-quality或Duplicate，也不伪造absence/recovery。已授权transport按Stage2 bounded fences收敛，既有history/coverage交集保留。

现有Asset Registry active Node detail同时提供四个独立控件：“检查健康”（显式 GET Health observation）、“Connection Test”（显式 POST admin action）、“立即启用监控”、“立即停用监控”（停用提示会取消已有预约）；两个 observation 控件各自只调用一次对应 route/Driver.Probe，互不混淆计数或结果展示。list保留monitoring状态与detail入口，不复制第二套controls。retired detail仅历史说明，无可执行operations。current active时Enable可disabled提示already-enabled，Disable仍可点击；current=false时Disable仍可点击以取消隐藏future。无future picker、credential/account editor。结果只在组件内存展示，离开/切换Node丢弃，迟到响应不能污染新选择，不写localStorage。future conflict显示“存在运维预约，请通过受控运维流程处理”，不自动cancel；retired conflict刷新历史detail；command conflict不得自动换command_id重做。新显式意图生成UUID，响应未知的重试保留原UUID/body；禁用自动probe retry/页面mount probe/后台轮询/health history。

### 9. Audit and metrics

category复用asset_node；additive actions `node.health|node.connection_test|node.monitoring_enable|node.monitoring_disable`——Health 与 Connection Test 是两个独立 bounded action，各自对应独立 route，不共用同一个 action 值。顶层actor_admin_id是认证UUID，request_id复用既有字段；不是metrics label，也不重复放details。transition result=success；probe按结果success/failure。固定reason用`administrator_enable|administrator_disable|node_probe_completed|node_probe_failed`，满足既有audit reason长度校验。

monitoring details allowlist仅 `command_id,instance_id,closed_monitoring_count,cancelled_future_monitoring_count`；probe details（Health 与 Connection Test 共用同一 details 形状）仅 `instance_id,result,reason,latency_ms`，其中instance_id为被探测Node的canonical UUID（不加目标IP/endpoint/raw status）。actor/command identity只在批准的durable command audit与receipt字段，不是日志/metrics维度。失败安全audit保持原契约。no-op与replay不写transition audit。审计失败与durable mutation同事务回滚。

复用 `control_asset_mutation_total{asset_type,action,result}`：asset_type=node，action新增monitoring_enable/monitoring_disable，result沿用success/replay/noop/conflict/invalid/unavailable；每个通过认证的command attempt终结计数一次，no-op与replay分开。新增两个独立 bounded metric family：`control_asset_health_total{asset_type="node",result}` 用于 Health route（result=healthy/timeout/failed），`control_asset_connection_test_total{asset_type="node",result}` 用于 Connection Test route（result=healthy/timeout/failed）；每次真实一次 Driver.Probe 完成各自计数一次，Health 结果 MUST NOT 计入 connection-test counter，反之亦然。底层Driver原有operation/result/reason指标保持不变，不合并为current health。不得labels包含instance_id/display_name/endpoint/secret/credential/IP/actor/command_id/raw error/account_key。audit与metrics失败不触发remote retry。

### 10. Schema inventory and compatibility

新增table=0、column=0、index=1；除上述三个monitoring reason CHECK/相关guard与audit action allowlist外，只增加§5的receipt partial expression lookup index、per-Node strict-monotonic `committed_at` assignment与receipt retention regression guard，并替换最小受控scheduled-writer函数签名/EXECUTE grants。复用Stage2 cancellation、generation及Stage1 receipt；无新revision/health-history/receipt subsystem或durable latch。migration不改写历史reason/actor/schedule/receipt，不做destructive down。

决定保持compatibility_class=2、floor=2：strict per-Node Disable `committed_at`、receipt index与retention只加强既有receipt写入/查询，不增加Stage2 reader必须解释的durable字段；Stage2 artifact必须验证不会按`committed_at`推导冲突语义。Stage2 reader依据cancelled_at/empty active_range判断资格，不依赖cancel_reason；新reason不改变区间含义、lineage或Node生命周期。shared receipt按完整UUID和command_kind处理，old class2不执行不认识的新产品route，不能重写新receipt。Stage2 Control代码扫描必须证明不调用被替换的operational SQL函数；受支持的external operational script与forward DB函数留在Control binary rollback unit之外并保持Stage3 fence-aware版本。旧5参数入口已不可执行，不能绕过fence。

未来rollout先确认Stage1/2与static implementation acceptance完成，停止并排空Control及所有受支持的operational writer/session，部署fence-aware script，应用reason/ACL/index/function forward migration，再由同一signed-manifest external gate验证class2 artifact后启动，最后开放operations。rollback同样停止并排空writers、保留forward schema/script/floor2，再启动旧Stage2 class2 Control；任何尚未提交的F0/intent随进程/session终止，不跨restart持久化，已提交future已由Disable取消。真实rollback acceptance必须先产生Stage3 Disable evidence，再用正式wrapper回退Stage2 class2，证明无pre-Disable stale intent存活，并证明rollback后新形成intent可经fence-aware operational path按baseline规则执行；旧5参数调用fail closed。还必须验证administrator reasons、unknown receipt/audit action下read/reconcile/Retire/Replace及拒绝old class1。若class2调用旧函数或误解释新reason/receipt，则为release blocker，回到Architecture Review，不悄悄升class3。

## Risks / Trade-offs

- Probe不是durable远端命令：网络完成与audit之间crash无法提供原子exactly-once；不建health历史或自动恢复任务，明确503/未知结果和手工再观察。
- `node_generation` 不承担 operational scheduling intent precondition；receipt fence只失效跨越Disable commit的旧intent。Disable不建立durable disabled latch，commit后新形成且捕获最新fence的受控operational intent仍可按既有preconditions创建future。
- Future行数量不设任意截断：Disable必须全部原子取消；DB statement timeout失败整事务rollback，不拆成异步cleanup。使用真实多row fixture验证稳定锁和有界服务超时。
- compatibility=2是待实施验收的设计决定，不把planning通过等同rollback已验证。

## Planning gate

Detailed planning = COMPLETE；Independent readiness review = PASS；P0 = 0；P1 = 0；P2 = 0；Planning readiness = PASS / READY；Implementation readiness = READY。openspec apply = NOT AUTHORIZED / NOT RUN；Implementation = NOT STARTED；Runtime Acceptance = NOT STARTED。
