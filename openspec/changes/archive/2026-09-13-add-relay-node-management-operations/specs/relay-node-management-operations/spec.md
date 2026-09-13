## ADDED Requirements

### Requirement: Node operations SHALL remain explicit and isolated

Control SHALL 仅提供 Node Health / Connection Test、immediate Monitoring Enable/Disable，使用现有Asset Registry和CLIProxyAPI Driver。MUST NOT新增Node/Gateway lifecycle、binding/account/credential/OAuth mutation、scheduled monitoring product API/UI、arbitrary HTTP client、scheduler、health-history subsystem或data-plane participation。依赖Stage1/2和static prerequisite先实施验收；本change planning不授权apply。

#### Scenario: 页面加载和Control启动
- **WHEN** 用户进入/刷新Asset Registry或Control启动
- **THEN** 不自动probe，不新增后台健康任务，不修改monitoring；Gateway/CLIProxyAPI数据面不受影响

#### Scenario: 越界输入
- **WHEN** 请求包含endpoint/path/method/headers/body/credential/schedule/cron/timezone/effective_from/expected_revision
- **THEN** API拒绝未声明字段且无outbound或monitoring mutation

### Requirement: Node operation HTTP SHALL use exact protected routes

Control MUST 使用以下精确 routes：`GET /api/assets/nodes/{instance_id}/health`（Health，独立于 Connection Test 的只读 product surface）、`POST /api/assets/nodes/{instance_id}/connection-test`（Connection Test，独立显式 admin action）、`POST /api/assets/nodes/{instance_id}/monitoring-enable`、`POST /api/assets/nodes/{instance_id}/monitoring-disable`。Health MUST NOT body、MUST NOT `command_id`/`expected_revision`；Connection Test body必须为`{}`；monitoring 两路由body仅包含非零UUID `command_id`；path identity同样为非零UUID。Health与Connection Test共享同一个底层 Driver.Probe 调用与响应模型，MUST NOT 复制第二套 transport/client/parser。请求Content-Type为application/json（Health无body）、最大1KiB，禁止query、unknown/duplicate字段与尾随JSON。

四个route MUST 在资产/receipt可见前完成 active session 与 super_admin 验证；Connection Test/Enable/Disable 三个 POST route 额外MUST 完成 same-origin 和 X-CSRF-Token 验证，Health 作为只读 GET MUST NOT 要求 CSRF/same-origin，与其它资产 GET 路由遵循同一 read-only 安全 contract。所有响应no-store。Health/Connection Test完成均返回200安全observation（远端失败同样200 result=failure），monitoring完成或no-op返回200 persisted result。错误MUST使用既有`{code,message,request_id}`，固定安全message：400 validation_failed；401 unauthorized；403 forbidden（四路由均适用）/csrf_invalid（仅三个POST路由）；404 asset_not_found；409 asset_retired/command_conflict/monitoring_future_conflict/monitoring_boundary_conflict/monitoring_state_conflict/generation_exhausted/capability_unsupported；503 service_unavailable。各code触发条件依design §1错误表，不透传SQL、URL、raw driver error。

#### Scenario: 安全拒绝
- **WHEN** 无session、过期session、非super_admin，或（仅限三个POST路由）CSRF/same-origin不合法
- **THEN** 分别401/403，无资产或receipt存在性泄漏、无HTTP probe、无domain mutation，保持no-store与既有security audit

#### Scenario: Health路由无需CSRF
- **WHEN** 已认证 super_admin 请求 `GET /api/assets/nodes/{instance_id}/health` 且未提供 `X-CSRF-Token`
- **THEN** 请求正常处理，不返回 csrf_invalid，与其它资产只读 GET 路由行为一致

#### Scenario: 请求超限或非法字段
- **WHEN** body超过1KiB、字段重复、包含future时间或arbitrary URL
- **THEN** 400 validation_failed，零domain mutation、receipt和outbound，错误不回显输入

#### Scenario: 不存在与已退休资产
- **WHEN** 无匹配receipt且目标不存在或已retired
- **THEN** 分别404 asset_not_found或409 asset_retired；不为新Disable伪造成功或复活资产

### Requirement: Node Health SHALL reuse one bounded credential-free Probe

`GET /api/assets/nodes/{instance_id}/health` MUST先短事务锁Node验证active/driver/management_health_read并复制registry endpoint，commit释放锁后才通过现有Driver.Probe请求固定GET /healthz（保留registry base path的固定append）。MUST只用当前HTTP-only管理传输；拒绝HTTPS/userinfo/query/fragment，不恢复allowlist/TLS配置。Health不依赖monitoring enabled或Management Key，不Resolve Secret、不调用auth-files/Usage Queue/Gateway/模型接口。Health是只读 GET，MUST NOT 要求 CSRF/same-origin、MUST NOT 使用 `command_id`/receipt/Node revision，不产生任何 monitoring mutation 或 current truth 变更。

MUST复用connect默认3s（100ms..10s）、总timeout默认15s（1..30s且>=connect）、健康body默认64KiB（128B..1MiB），无proxy/cookie/redirect/retry/fallback；禁止DB transaction/Node lock跨HTTP。Health成功仅证明HTTP200、有界JSON object且status=ok，不能提升为账号/Provider/Inventory或可调度真相。

结果MUST仅为`{result:"success|failure",reachable:boolean,reason:enum,latency_ms:integer}`，latency_ms为0..30000的有界elapsed；只有成功时reachable=true。reason只允许none/http_status/response_invalid/response_too_large/timeout/cancelled/network_unavailable/dns_rejected/tls_rejected/redirect_rejected/target_rejected，不得返回原body/header/status text/IP/endpoint/debug error。HTTP-only下TLS异常仅防御映射，不授权TLS请求。审计写独立`node.health` action，与Connection Test的`node.connection_test` action分开计数，MUST NOT 混淆。

#### Scenario: active healthy Node
- **WHEN** 未配置Management Key且monitoring disabled的active Node健康接口返回200与合法status=ok
- **THEN** 一次credential-free GET，200 result=success、reachable=true、reason=none；不改变任何durable asset/current truth

#### Scenario: 非200或无效健康响应
- **WHEN** 固定path返回非200、非法JSON、非ok或超限body
- **THEN** 200 result=failure、reachable=false，reason对应http_status/response_invalid/response_too_large；非200 body不读取、不泄漏

#### Scenario: timeout或DNS连接失败
- **WHEN** 超时、取消、DNS/连接错误或typed TLS错误发生
- **THEN** 有界结束，映射固定reason，无raw error，无retry/fallback；TLS fixture不建立真实HTTPS支持

#### Scenario: redirect与arbitrary fetch
- **WHEN** Node返回任意3xx或客户端企图修改URL/path/header
- **THEN** redirect_rejected且无第二请求；非法客户端字段400，不调用其他path或host

#### Scenario: retired Node
- **WHEN** 已认证 super_admin 请求一个 retired Node 的 Health 路由
- **THEN** 409 asset_retired，零HTTP，不使用receipt/revision/current truth

#### Scenario: Retire或Replace先于probe授权
- **WHEN** lifecycle事务先获得Node锁并commit，随后probe read fence取锁
- **THEN** 409 asset_retired，零HTTP

#### Scenario: probe授权先于Retire或Replace
- **WHEN** probe短事务先commit，lifecycle随后提交而HTTP仍在运行
- **THEN** 只允许本次bounded observation完成，无锁跨HTTP、无current promotion；新probe需重新校验且被拒绝

#### Scenario: 缺少health capability
- **WHEN** Node driver/type/contract或management_health_read不匹配
- **THEN** 409 capability_unsupported，Secret/DNS/网络均不执行

#### Scenario: 页面mount/reload零probe
- **WHEN** 管理员进入或刷新Asset Registry页面
- **THEN** Health路由不被自动调用，无后台轮询，不建立health history

### Requirement: Node Connection Test SHALL reuse the same bounded Probe as an explicit admin action

`POST /api/assets/nodes/{instance_id}/connection-test` MUST复用与 Health 完全相同的短事务授权、Driver.Probe调用、超时/body边界、response model与reason taxonomy（design §1/§2），不新增第二套transport/client/parser，仅入口route、安全惯例（CSRF/same-origin）和audit/metrics action不同。Connection Test 是 explicit admin-triggered、read-only、bounded、non-durable remote observation；MUST NOT 使用 `command_id`/receipt/Node revision，不产生任何 monitoring mutation 或 current truth 变更。

Connection Test MUST 要求 active session、super_admin、same-origin 和 `X-CSRF-Token`（与 monitoring mutation 相同的 CSRF 惯例），保持显式管理员动作语义，不因为是 observation 而放宽 CSRF。审计写独立`node.connection_test` action，与 Health 的`node.health` action分开计数，MUST NOT 混淆。

#### Scenario: 与Health相同的Probe契约
- **WHEN** 已认证 super_admin 对 active Node 发起 Connection Test
- **THEN** 结果模型、reason taxonomy、超时边界与 Health 完全一致，仅通过独立 route 和 action 区分

#### Scenario: CSRF保护
- **WHEN** Connection Test 请求缺少 `X-CSRF-Token` 或 same-origin 校验失败
- **THEN** 403 csrf_invalid，零HTTP、零audit的成功记录

#### Scenario: 独立audit与metric action
- **WHEN** 一次Health与一次Connection Test分别针对同一Node完成
- **THEN** 分别写入`node.health`与`node.connection_test`两条独立audit，`control_asset_health_total`与`control_asset_connection_test_total`两个独立metric family各自计数一次，互不覆盖或合并

#### Scenario: retired Node
- **WHEN** 已认证 super_admin 对 retired Node 发起 Connection Test
- **THEN** 409 asset_retired，零HTTP


### Requirement: Monitoring Enable SHALL create only immediate intervals

MUST复用Stage2 monitoring表与Node-first locks。无receipt路径先Node FOR UPDATE、验证active、建立一次DB clock_timestamp boundary，再按effective_from ASC, monitoring_activation_id ASC锁current和uncancelled future。future存在优先返回409 monitoring_future_conflict（即使也存在current）；无future且current存在返回already_enabled no-op；两者皆无时创建一个effective_from=created_at=boundary、effective_to=NULL的open interval，reason=administrator_enable、actor=admin UUID text。不silent取消/前移future，不改变planned current end，无network/policy/credential前置要求。

实际变更MUST在同事务推进node_generation一次、写一次transition audit和receipt；no-op只有receipt，不变Node lifecycle/revision/updated_at/generation。无需expected_revision，不创造第二token。

#### Scenario: disabled立即启用
- **WHEN** active Node无current和future
- **THEN** 新建一个DB当前时刻open interval，200 enabled，generation+1，revision不变，一audit一receipt

#### Scenario: already current
- **WHEN** 新command_id请求Enable且已有current、无future
- **THEN** 200 already_enabled，保留原interval/end，一receipt、零transition audit、零generation/revision变化

#### Scenario: future阻止Enable
- **WHEN** 存在uncancelled future activation
- **THEN** 409 monitoring_future_conflict，所有schedule/history保持，零receipt/变更/audit

#### Scenario: 两个并发Enable
- **WHEN** 不同command_ids同时为无current/future的Node启用
- **THEN** Node锁串行化，一次enabled、一次already_enabled，两receipt、一个interval和transition audit，generation仅+1

### Requirement: Monitoring Disable SHALL neutralize current and future atomically

MUST按auth→command lock/receipt→Node FOR UPDATE→active校验→读取previous Disable fence并分配strictly-greater `new_disable_fence_at`→建立一次DB monitoring boundary→稳定顺序锁current/future→close/cancel domain mutation→仅当current monitoring projection实际改变时锁generation row并将`node_generation`恰好+1→audit/receipt（receipt.committed_at=new_disable_fence_at）→commit执行。future-only cancellation是真实domain mutation，但MUST NOT锁定或更新generation；already-disabled无domain mutation且MUST NOT锁generation，但仍写新fence receipt。current用effective_to=end_recorded_at=boundary、end_reason=administrator_disable、end_actor=admin UUID text；所有future用Stage2 cancelled_at=boundary/cancelled_by=admin UUID FK/cancel_reason=administrator_disable。future effective_from必须>boundary，原schedule字段保留，active_range为空，永远不能再次effective。

Disable commit后，transaction中锁定/观察到的current已关闭，transaction中锁定/观察到的所有future已durable cancelled；被取消的旧future永远不得再次effective。每个新的Disable command（包括already-disabled no-op）还MUST提交新的shared receipt race fence，使Disable commit前已经形成但尚未提交activation的scheduled intent在取得Node锁后conflict。Disable不建立durable disabled latch；commit后才形成并捕获最新fence的新operational intent MAY按既有Node-lock/lifecycle/monitoring preconditions创建future activation。若current被关闭，`node_generation`在同一transaction中+1 exactly once；若只有future被取消而没有current，`node_generation` unchanged；若current+future同时处理，`node_generation`+1 exactly once；若无current且无future，generation unchanged。future-only cancellation仍是真实monitoring domain mutation，按真实mutation写transition audit和receipt。全部在同一事务，无HTTP、无Gateway/Directory/binding lock。effective_from==boundary的current按Stage2稳定409 boundary conflict，不能写zero-length。失败全部rollback；不能truncate/delete/异步取消或改写历史UTC交集。

#### Scenario: current加多个future
- **WHEN** active Node有current及多个future（含已安排future结束时间）
- **THEN** 同一boundary关闭current并取消全部future，200 disabled及准确counts，一generation增量、一audit、一receipt

#### Scenario: 只有future或只有current
- **WHEN** Disable发现上述任一种状态
- **THEN** 只有future时`cancelled_future_monitoring_count > 0`、`closed_monitoring_count = 0`、future durable cancelled且generation unchanged；仍按真实domain mutation写transition audit和receipt。只有current时关闭current、`closed_monitoring_count > 0`、`cancelled_future_monitoring_count = 0`且generation +1 exactly once；不能把只有future误判already_disabled

#### Scenario: already disabled
- **WHEN** 新command_id且无current/uncancelled future
- **THEN** 200 already_disabled，一receipt及新的Disable race fence，零domain/generation/revision/transition-audit变化；该fence仍失效更早形成而尚未提交的scheduled intent

#### Scenario: cancellation重启后仍生效
- **WHEN** Disable成功后重启且时间越过原future effective_from
- **THEN** 原row仍可追溯但empty range始终无eligibility/expected slot，不被reconciliation复活

#### Scenario: 部分写入失败
- **WHEN** 任一close/cancel/generation/audit/receipt失败或commit前crash
- **THEN** 全事务rollback，无半关闭schedule、无receipt/success audit，不拆异步cleanup

#### Scenario: 两个并发Disable
- **WHEN** 两个不同command_ids竞争同一current/future集合
- **THEN** 首个disabled、第二个already_disabled，只有一次state transition和generation增量

### Requirement: Monitoring commands SHALL reuse exact shared replay

MUST复用asset_admin_command_receipts全局command_id和Stage1 advisory-lock算法、actor-first lookup、canonical encoding v1；不得新建receipt subsystem。canonical bytes分别为compact UTF-8 `[1,"node.monitoring_enable",instance_id,"administrator_enable"]` 和 `[1,"node.monitoring_disable",instance_id,"administrator_disable"]`，无空白/newline，UUID lowercase hyphenated；SHA-256 bytes→32-byte hash，intent_encoding_version=1、secret_fingerprint_key_version=NULL。无Secret intent，不改变K1/version规则。

same actor+command+intent MUST返回原完整persisted HTTP200 body；不同actor或intent409 command_conflict。receipt lookup先于lifecycle/current校验，每个accepted completed no-op也有一receipt。Monitoring结果body MUST完整包含design §1定义的result/instance_id/lifecycle_status/revision/boundary/monitoring_active/monitoring_activation_id/effective_from/effective_to/closed_monitoring_count/cancelled_future_monitoring_count；不得读后来的Node来重建旧响应。probe不使用command_id或receipt。

#### Scenario: command replay与后续状态变化
- **WHEN** Enable A提交，Disable B或Retire后来提交，再重放A
- **THEN** 返回A原status/body，无第二interval/audit/receipt/generation变更，不因已退休拒绝合法replay

#### Scenario: actor与intent冲突
- **WHEN** 另一actor使用相同command_id，或相同actor改Node/action
- **THEN** 409 command_conflict且不泄漏原result、不写任何success evidence

#### Scenario: concurrent same command与unknown outcome
- **WHEN** 同command并发或commit后客户端丢失response重试
- **THEN** advisory lock+PK lookup保证一个receipt和最多一次transition，返回原completed result

#### Scenario: no-op replay
- **WHEN** already_disabled command已持久化，随后Enable，再replay旧Disable
- **THEN** 返回原already_disabled body，不关闭新的interval、不新增audit或generation

### Requirement: Operations SHALL preserve lifecycle and scheduling serialization

所有monitoring write transaction MUST首先以Node row `FOR UPDATE`作为第一层domain serialization boundary，并在Node lock之后按`effective_from ASC, monitoring_activation_id ASC`稳定顺序锁定所需monitoring rows，不取Gateway/Directory/binding；scheduled writer形成intent时只在write transaction之前捕获design §5的bounded F0，不持有domain lock。只有实际改变current monitoring projection的Stage 3 product Enable/Disable操作，才在monitoring-row locks之后额外锁定并推进`asset_registry_generations.node_generation`；future-only cancellation不得锁定或推进generation。既有operational scheduled writer不依赖generation。Retire/Replace先commit则新Enable/Disable conflict；operations先commit则lifecycle随后关闭/取消remaining interval，历史administrator reason不重写。旧Node最终不得有current/future eligibility。

既有 operational scheduled writer MUST在形成具体intent时通过partial-index bounded lookup捕获该Node最新committed `node.monitoring_disable` receipt command_id为nullable F0；write transaction使用READ COMMITTED，显式VOLATILE受控函数随后执行 `Node FOR UPDATE` → lifecycle `active` 校验 → 在取得锁后的下一内部SQL fresh snapshot有界读取F1 → F0/F1 equality check → 稳定顺序锁 monitoring rows → 自身既有operational preconditions（例如禁止回填校验）。F1与F0不同MUST返回SQLSTATE 55000及固定运维分类`monitoring_disable_fence_conflict`、零activation write；调用方不得以同一旧intent自动重试。`node_generation` MUST NOT 被用作该 writer 的 optimistic concurrency token、intent precondition、stale epoch或Disable fence——它仅保留Stage2已冻结职责。

Disable fence token严格为latest receipt的nullable command_id；latest MUST只按strictly-monotonic per-Node `committed_at DESC`决定。每个新Disable在Node lock下读取previous committed_at，并写入`max(clock_timestamp(), previous + 1 microsecond)`（无previous则clock_timestamp）；后序serialized Disable MUST严格大于前序，无法得到finite strictly-greater值时fail closed。`command_id`是fence identity，不是temporal ordering或tie-break。查询MUST使用`command_kind='node.monitoring_disable'`和`sanitized_result.instance_id`的UNIQUE partial expression index并`ORDER BY committed_at DESC LIMIT 1`，不得无界扫描；index MUST拒绝同Node timestamp tie，任何检测到的invariant violation必须fail closed。旧5参数writer函数MUST不可执行；新入口MUST显式接收F0且不得提供绕过。Disable不建立durable disabled latch：它只失效其commit前形成的旧intent；commit后才形成的新intent捕获新F0，在没有更晚Disable时可按既有规则执行。

所有`node.monitoring_disable` receipts MUST继承并强化Stage1 immutable保证：MUST NOT UPDATE、DELETE或TRUNCATE；runtime、operational role和PUBLIC不得拥有绕过权限。任何仍支持operational scheduling intent的部署MUST NOT GC/prune latest fence receipt。receipt缺失不得使non-NULL fence退回NULL，本change不得以第二套retention framework替代数据库guard。

#### Scenario: Enable或Disable与Retire竞争
- **WHEN** 任意monitoring command与Retire竞争Node锁
- **THEN** lifecycle先commit则新command409；monitoring先commit则Retire随后终结remaining eligibility，旧receipt保留

#### Scenario: Enable或Disable与Replace竞争
- **WHEN** 任意monitoring command与Replace竞争
- **THEN** 同一Node锁序列决定结果，old最终retired无eligibility，new不继承monitoring

#### Scenario: 已有future writer先提交
- **WHEN** operational future writer先commit后产品Enable或Disable获得Node锁
- **THEN** Enable409 future conflict；Disable原子取消该future，不能漏锁或静默前移

#### Scenario: Disable先提交且旧operational intent在等待
- **WHEN** scheduled intent先捕获F0但尚未提交activation，Disable随后取得Node锁并commit receipt，旧writer之后才取得Node锁
- **THEN** F1不同于F0，writer返回固定conflict且零activation write；Disable即使already-disabled no-op也产生同一fence结果

#### Scenario: Disable后形成新operational intent
- **WHEN** Disable已经commit后才形成新的scheduled intent并捕获最新F0，且write前没有更晚Disable
- **THEN** F1等于F0，writer可按既有lifecycle、monitoring及禁止回填preconditions正常执行；这不构成durable disabled latch

#### Scenario: Fence lookup有界且旧入口fail closed
- **WHEN** 查询Node最新Disable fence或调用旧5参数scheduled-writer入口
- **THEN** 前者使用固定partial index和LIMIT 1，后者无EXECUTE/不存在；不得扫描全receipt history或省略F0校验

#### Scenario: Transaction启动顺序不同于Disable serialization顺序
- **WHEN** D2 transaction先启动，但D1先取得Node lock并commit，D2后取得Node lock并commit
- **THEN** `D1.committed_at < D2.committed_at`且latest fence为D2；不得按transaction start time或command UUID选择D1

#### Scenario: 同钟值仍形成严格顺序
- **WHEN** 两个串行Disable的clock_timestamp相同或后一个物理时钟值不大于previous fence
- **THEN** 后一个使用previous+1 microsecond，两个fence order keys严格不同；无法表示时整事务fail closed，不得用UUID决定later

#### Scenario: 多个Disable越过旧intent
- **WHEN** writer捕获Disable A为F0，随后Disable B与C依次commit，writer之后取得Node lock
- **THEN** F1必须解析为C且F1不同于F0，返回monitoring_disable_fence_conflict并零activation write

#### Scenario: Receipt retention保持fence
- **WHEN** runtime、operational role、GC或维护操作尝试UPDATE/DELETE/TRUNCATE Node Disable receipt或prune latest fence
- **THEN** 数据库/支持部署流程拒绝，F0/F1不能因receipt移除产生false equality

### Requirement: Monitoring reads SHALL reuse generation and eligibility

`node_generation` MUST 只保留 Stage2 已冻结职责——Node list/projection cursor consistency，MUST NOT 承担 operational scheduling intent precondition 或任何per-intent token 职责。实际状态变更的推进规则精确冻结为：

- Enable：disabled → 立即建立 current（改变 monitoring current projection）→ `node_generation`+1
- Enable：already current（no-op）→ `node_generation` 不变
- Disable：current interval 被关闭（改变 monitoring current projection）→ `node_generation`+1（恰好一次）
- Disable：current + future 同时处理 → current 改变 → `node_generation`+1（恰好一次，不因 future row 数量额外增加）
- Disable：只有 future、没有 current → future cancellation 不改变当前 Node list/default current projection → `node_generation` 不变
- Disable：无 current、无 future（no-op）→ `node_generation` 不变
- receipt replay / Health / Connection Test → `node_generation` 不变

实际推进 MUST 同事务锁 `asset_registry_generations` 单例并 `node_generation`+1，溢出fail closed，不可下降。Stage2 cursor/read_as_of契约不变，旧generation409 cursor_stale。Disable不直接改Inventory/poll/availability/request-quality/absence/Problem/Duplicate，既有eligibility自然排除target并fence在途promotion；history/coverage不回填、不删除。

#### Scenario: cursor失效与纯时间流逝
- **WHEN** Monitoring实际变更后使用旧Node list cursor，或只有时钟推进且无durable变更
- **THEN** 前者409 cursor_stale；后者仍按旧read_as_of，不产生伪generation变化

#### Scenario: Disable后Inventory与current reads
- **WHEN** Disable成功后scheduler/read/promotion检查Node资格
- **THEN** 无监控资格，旧snapshot/account evidence保持历史；不得人为产生absence/recovery

#### Scenario: 仅取消future不推进generation
- **WHEN** Disable只发现uncancelled future activation、没有current
- **THEN** future被durable cancel，但`node_generation`不变，不产生cursor_stale

#### Scenario: Enable/Disable no-op不推进generation
- **WHEN** Enable发现already current、或Disable发现无current无future
- **THEN** 均为receipt-only no-op，`node_generation`不变，不产生虚假cursor_stale

### Requirement: Operations audit and metrics SHALL remain bounded

MUST复用asset_node audit，actions新增node.health/node.connection_test/node.monitoring_enable/node.monitoring_disable——Health与Connection Test是两个独立action，各自对应独立route，MUST NOT共用同一个action值或合并计数；顶层actor_admin_id与request_id复用现有字段。实际monitoring transition一success audit，与receipt同事务；no-op/replay无transition audit。monitoring details仅command_id/instance_id/closed_monitoring_count/cancelled_future_monitoring_count。probe（Health与Connection Test）每次真实调用完成各自一sanitized observation audit，details仅canonical Node UUID `instance_id`、result、reason、latency_ms，无mutation receipt或health history；audit失败503、不自动重复HTTP，不宣称远端与DB跨crash原子性。

MUST复用control_asset_mutation_total（asset_type=node；新增action monitoring_enable/monitoring_disable；result success/replay/noop/conflict/invalid/unavailable）。MUST新增两个独立metric family：control_asset_health_total（asset_type=node；result healthy/timeout/failed，Health route专用）和control_asset_connection_test_total（asset_type=node；result healthy/timeout/failed，Connection Test route专用），分别由各自probe success/timeout/其余失败映射，MUST NOT把Health计入connection-test counter或反之。每个完成attempt计一次，无自动health轮询计数。MUST NOT把instance_id/endpoint/secret/credential/IP/actor/command_id/raw error/response body放metrics label、Driver日志或probe details。

#### Scenario: 审计失败
- **WHEN** monitoring audit或receipt失败，或probe完成但observation audit失败
- **THEN** 前者全mutation回滚；后者503无重做HTTP，也不回滚独立asset command

#### Scenario: Secret和raw response canary
- **WHEN** endpoint/body/error/header携带唯一canary
- **THEN** API/audit/receipt/log/metrics/evidence均不含canary，只有固定reason和bounded count/latency

#### Scenario: Health与Connection Test计数独立
- **WHEN** 针对同一Node分别完成一次Health与一次Connection Test
- **THEN** `control_asset_health_total`与`control_asset_connection_test_total`各自恰好+1，`node.health`与`node.connection_test`各自恰好一条audit，两个audit带相同canonical `instance_id`但action不同，互不覆盖

#### Scenario: Probe audit可归属且保持脱敏
- **WHEN** Node A与B得到相同probe结果，或任一Node的probe失败
- **THEN** 每条audit均以canonical `instance_id`明确归属目标并只含bounded result/reason/latency；不得包含endpoint、IP、raw HTTP status text/body/header、Secret、credential或raw driver error，且instance_id不得成为Prometheus label

### Requirement: Stage 3 SHALL reuse class 2 compatibility

MUST不新增table/column/revision/receipt/health history；只additive扩展reason/ACL/audit actions、一个shared-receipt bounded lookup index、strict per-Node Disable committed_at assignment及fence-aware operational函数签名。compatibility_class=2、floor=2，保留Stage1外置signed-artifact gate。receipt index/ordering不是新business field；Stage2 artifact MUST不以committed_at解释冲突业务语义，并保持forward receipt可读。Stage2 cancellation/readers按cancelled_at/empty range解释而非固定两个reason；旧class2对新receipt/audit action必须保留不可变数据、不擅自重放未知操作。Stage2 Control必须经代码扫描证明不调用旧5参数operational函数，受支持的external writer/script及forward函数不随binary rollback降级。真实旧Stage2 artifact回归失败属于release blocker，必须回Architecture Review，不能擅自升class3或忽略错误。

#### Scenario: class2 rollback with administrator cancellation
- **WHEN** Stage3产生administrator_enable/disable及receipt/audit后，经正式wrapper回退Stage2 class2 artifact
- **THEN** 停止并排空所有Control/operational writer session后，任何pre-Disable ephemeral F0/intent均不能跨restart存活；已取消future仍ineligible，current/read/reconcile/Retire/Replace正确，未知receipt不变；rollback后新intent使用保留的fence-aware operational path可按baseline执行，旧5参数入口fail closed，旧class1仍被floor2拒绝

#### Scenario: migration与权限
- **WHEN** forward migration完成，runtime/PUBLIC尝试直接写activation或删除history
- **THEN** 仅批准SECURITY DEFINER路径可执行，非法写入拒绝，history/既有reason/actor不回填改写
