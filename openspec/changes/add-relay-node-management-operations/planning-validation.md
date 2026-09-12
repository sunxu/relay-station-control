## Stage 3 planning validation

### Baseline / dependencies

- Control main / static planning：`f8e600e28f22f88d672fff550c9d0ca2cfea1d2d`，开始时worktree clean。
- Stage2 Node lifecycle planning：`4cf8eae`，独立readiness PASS/READY。
- Stage1 Gateway/shared foundation：`14c0e80` planning baseline，当前main中的已批准artifacts为依赖真相，PASS/READY。
- Ops批准记录：`5add9cb54f0a488cc547b2ba72287c8f492a32f7`；frozen requirements reviewed baseline：`8c7cdbcf5ea3480da16d51408581a4be3e72c994`。
- Ops当前可读来源：[Phase6 requirements](../../../../ops/docs/phase-5-7/PHASE6_GATEWAY_RELAY_NODE_MANAGEMENT_CN.md)、[Architecture Review](../../../../ops/docs/phase-5-7/PHASE6_GATEWAY_RELAY_NODE_MANAGEMENT_ARCHITECTURE_REVIEW_CN.md)。仓库间引用按workspace兄弟ops定位；不修改Ops。
- 当前scope为Control Stage3 planning。Stage1/2/static尚未implementation；其commit不是runtime binary或digest。

### Source scan and precedence

已检索23个baseline capability的Requirement目录与monitoring/health/security相关契约，重点读取asset-registry、readonly-driver、management-outbound/internal-http、administrator-access、Inventory/current/history以及Stage1/2的完整合成与shared/cancellation决策。非本change拥有的account/alerting/job/Topology能力保持原状，不复制为MODIFIED。

| Source | 核实事实 / Stage3决定 |
|---|---|
| AGENTS.md、openspec/config.yaml | planning-only、zh-CN、最小权限、MODIFIED完整替换、tasks≤2h、no data plane |
| migrations/00003_asset_registry_foundation.sql | activation真实字段、CHECK/GiST、Node-first registrar函数；当前函数不覆盖all-future cancellation |
| queries/assets.sql、internal/store/assets.go | 当前Node列表/详情派生monitoring、counts与只读Store；不假定存在Stage2 runtime实现 |
| internal/drivers/cliproxyapi/driver.go、health.go、transport.go、target_policy.go；internal/drivers/config.go/types.go | 现有Probe/closed parser/固定path/HTTP-only/无Secret；timeout/response bounds/typed reasons已存在 |
| internal/api/server.go、auth_handlers.go；api/openapi.yaml | requireSession、sameOrigin、CSRF、no-store、bounded JSON与code/message/request_id envelope、plural Node paths |
| migrations/00002* audit_logs；Stage1/2 audit设计 | action/category/result CHECK；actor顶层UUID；固定allowlist扩展，不照搬短reason导致长度CHECK失败 |
| web/src/pages/AssetRegistryView.tsx、AssetsPage.tsx、api/asset-* | 现有Asset Registry与生成客户端/分页hooks是唯一操作UI，不新增router/state platform |
| Stage1/2 design/specs、static-prefix artifacts | shared receipt/no-op、canonical arrays、Node revision不由monitoring推进、cancellation metadata与generation、static /assets/识别契约 |

最新internal-http与实际target_policy.go优先于readonly-driver历史HTTPS语句：Stage3只允许内部HTTP；TLS fixture只做防御error mapping，不建立HTTPS功能。

### Existing persistence reused / new schema inventory

复用Stage2的relay_node_assets lifecycle/read projection、relay_node_inventory_monitoring_activations和cancelled_at/cancelled_by/cancel_reason、empty active_range/GiST、asset_registry_generations.node_generation/read_as_of；复用Stage1 asset_admin_command_receipts、K1/encoding/advisory helper、audit_logs。

| New schema item | Count / why |
|---|---|
| table | 0，现有activation/receipt/audit已表达全部durable truth |
| column | 0，无boolean shadow、health history、revision或新durable token column |
| index | 1，新增`asset_admin_command_receipts_node_disable_fence_idx` UNIQUE partial expression index，以`(sanitized_result.instance_id, committed_at DESC) INCLUDE(command_id)`支持`node.monitoring_disable`的bounded latest lookup并拒绝同一 Node timestamp tie；仅为查询/invariant支持，不是business truth |
| reason allowlist | reason加administrator_enable；end_reason与cancel_reason加administrator_disable；原system/lifecycle值全部保留，产品admin不能冒充deployment/reconciliation |
| audit action allowlist | node.health/node.connection_test/node.monitoring_enable/node.monitoring_disable；category=asset_node复用；Health与Connection Test各自独立action，不合并 |
| functions / ACL | 最小product monitoring受控事务；operational writer先捕获F0，READ COMMITTED写事务内显式VOLATILE函数在Node lock后的下一内部SQL fresh snapshot有界读F1；mismatch为SQLSTATE 55000/monitoring_disable_fence_conflict；旧5参数函数撤销/删除EXECUTE，新签名强制nullable F0；PUBLIC revoke/runtime最小EXECUTE，底表无任意写权限 |

以上是未来forward migration计划，本轮没有新增migration/SQL production文件。所有actor/history字段类型不变；cancelled_by仍admin UUID FK，actor/end_actor仍text。

### Lock graph / scheduling race

product command顺序为 command advisory lock → actor-first receipt lookup → Node FOR UPDATE → DB boundary → affected monitoring rows（`effective_from`, `monitoring_activation_id`）→ domain mutation → **仅当current projection改变时** generation row lock/+1 → audit/receipt → commit。future-only cancellation不锁/不更新generation，no-op不做domain/generation mutation。无Gateway/Directory/binding lock，无HTTP。

scheduled writer在write transaction外形成intent并有界捕获nullable F0；READ COMMITTED write transaction的显式VOLATILE函数执行Node FOR UPDATE → lifecycle active → 下一内部SQL fresh snapshot有界读取F1 → equality check → stable monitoring-row locks → 既有preconditions。writer先commit时Disable随后取消其future；Disable先commit时旧intent F1≠F0并以SQLSTATE 55000/monitoring_disable_fence_conflict失败；Disable后新形成intent捕获最新F0后可正常执行。`node_generation`只保留Stage2 Node list/projection cursor职责，不参与fence。Disable不建立durable disabled latch。

### Receipt / revision decision

Enable/Disable只要求command_id，不接收expected_revision、不变Node revision/updated_at；依据Ops §17与Stage1 Existing asset optimistic concurrency Requirement。只有实际改变current monitoring projection才锁generation row并推进node_generation一次；future-only cancellation不锁/不更新generation但写transition audit+receipt；no-op写receipt/fence，不变generation且不写transition audit。same-command actor/intent match先于domain判定返回原persisted body/status。

### Disable fence representation and no-op semantics

最终选择为shared receipt fence。每个Node的token是latest committed `node.monitoring_disable` receipt的nullable command_id，但latest只按strictly-monotonic per-Node `committed_at DESC`确定；随机command_id不承担temporal ordering。每个新Disable在Node lock下读取previous committed_at并分配`max(clock_timestamp(),previous+1 microsecond)`（无previous则clock time），因此A先于B序列化必有A.committed_at < B.committed_at；无法取得finite strictly-greater值则fail closed。receipt完整result已有canonical instance_id。existing truth足够，因为每个accepted Disable（含already-disabled）都在持有Node lock的transaction中原子写immutable receipt；无需新增business column/table。唯一新增schema是bounded lookup partial expression index，不扫描历史。

scheduled intent形成时捕获F0；真正写入取得Node lock后用fresh READ COMMITTED statement snapshot查询F1。F1变化固定conflict且零insert；same-command replay不写新receipt/fence；新的already-disabled command写严格更晚的receipt/fence但无domain/generation/transition audit。Node Disable receipts继承Stage1 immutable guard并明确拒绝UPDATE/DELETE/TRUNCATE，支持部署不得GC/prune latest fence，避免F0/F1因receipt消失false equality。旧5参数函数不可执行，避免省略F0；F0不得以`node_generation`、revision、updated_at、进程counter或wall clock替代。

精确canonical v1 fixture使用design中UUID：

| Action | UTF-8 bytes | SHA-256 hex |
|---|---|---|
| enable | 90 | f41f88d2693f203399544c5cd210d48057abfd845f3d09ef3c0f5ca276cb25fe |
| disable | 92 | b226ed7bfcc3cf1ec1c10eaf4084965217f3ce2fa914eef338b01c71732fbaf2 |

secret_fingerprint_key_version=NULL；新kind无Secret字段，不变shared K1/v1规则，不引入新encoding version。

### Connection Test / Health persistence decision

Health（GET，独立product surface）与Connection Test（POST，独立显式admin action）是两个独立route，共享同一个底层Driver.Probe调用与response model，不复制parser/transport。两者都只写既有sanitized observation audit（各自独立action：node.health/node.connection_test），details固定为canonical target instance_id/result/reason/latency_ms，不要command_id/receipt/health-history/last-health持久列；instance_id不得成为metric label。现有migrations无Node process health history；Inventory provider health是另一领域，不得借用。短Node读事务commit是ephemeral授权点，release后HTTP；response只在客户端内存，probe不刷新任何current truth。网络与audit跨crash不可原子化，503/未知结果不自动重试，不伪造exactly-once或新health job。

### Compatibility decision

设计维持class2/floor2：receipt fence只加强既有`committed_at` assignment/immutability并增加lookup index，不增加Stage2 reader必须解释的durable field；新reason不改变cancelled_at/empty range资格。Stage2 artifact必须证明不以committed_at解释冲突业务语义且forward receipt可读；代码扫描必须证明Control不调用旧5参数operational函数。fence-aware external script与forward DB函数不随binary rollback降级。rollback前停止并排空所有Control/writer session，所以pre-Disable ephemeral F0/intent不能跨restart；已提交future由Disable取消。真实Stage2 class2 artifact验收还必须证明rollback后新形成intent可用保留的fence-aware入口按baseline执行、旧入口fail closed。任何扫描/验收失败均为release blocker，不能静默保持class2、升class3或上线。保留forward schema与floor，不做destructive down。

### MODIFIED baseline-preservation audit

OpenSpec MODIFIED replaces the whole Requirement。合成源必须是baseline normative body + 全部baseline scenarios + 已批准Stage1/2语义 + Stage3 additive delta；泛化“baseline remains intact”不替代正文。

| Capability / Requirement | Heading source | Preservation check |
|---|---|---|
| asset-registry / Node 账号监控状态源自显式激活区间 | baseline exact title | 保留半开区间/DB UTC/实名reason/非重叠/无回填/历史与Stage2 cancellation/Node-first；追加立即操作、administrator reasons与shared-receipt Disable fence；`node_generation`仅在实际推进current projection时推进一次且不参与fence |
| asset-registry / 管理员可通过受保护只读 API 查看资产 | baseline exact title | 保留Stage1 Gateway routes/counts/history与Stage2 Node pagination/generation/read_as_of/安全；向产品write allowlist加入GET health只读路由与三个Stage3 POST（Connection Test/Enable/Disable），GET与POST安全惯例差异化；原拒绝未声明路径场景同步限定 |
| asset-registry / 只读资产页面处理空状态与故障 | baseline exact title | 保留lazy loading/generated client/no raw Secret/empty/error与全部Stage1/2生命周期控件和历史；Stage2 deferred operations禁令按本change ownership替换为Health/Connection Test/Enable/Disable四个独立active detail controls |
| relay-node-asset-lifecycle / Monitoring cancellation SHALL be durable and cancellation-aware | approved Stage2 ADDED exact title（尚未archive，不伪称当前baseline已有） | 完整保留triplet/FK/actor区分/current close/immutable/empty range及两个既有场景；精确加入administrator_disable，删去已经落实的future deferral措辞 |

作者自查：MODIFIED heading exact-title comparison = PASS；baseline/approved-dependency scenario title coverage = PASS；完整正文与Stage1+Stage2+Stage3 overlapping semantic composition人工自查 = PASS。自动检查只证明标题/场景覆盖与encoding fixture，不替代独立semantic review。未新增无意义MODIFIED capabilities；取消allowlist只在其原owner Requirement中扩展。

### Independent readiness review round 1 disposition

第一轮 independent readiness review 结果：P0=0，P1=2，P2=0，Planning readiness = CHANGES REQUIRED。本轮已针对两项修复：

1. **Health / Connection Test product surfaces restored separately, sharing one Driver.Probe implementation.** 恢复为两个独立product surface：`GET /api/assets/nodes/{instance_id}/health`（只读GET，遵循read-only GET安全惯例，不要求CSRF/same-origin）与`POST /api/assets/nodes/{instance_id}/connection-test`（独立显式admin action，要求CSRF/same-origin）；两者共享完全相同的短事务授权+Driver.Probe调用路径与ProbeResult响应模型，不新增第二套transport/client/parser，仅入口route、安全惯例与audit/metrics action不同。audit分为独立的`node.health`/`node.connection_test`两个action；metrics分为独立的`control_asset_health_total`/`control_asset_connection_test_total`两个family，互不计入对方。UI在active Node detail提供两个独立控件，页面mount/reload对两者均MUST NOT自动触发，不建立health history、不后台轮询。

2. **node_generation restored to Stage 2 read/projection-generation semantics.** 删除了`node_generation`作为operational scheduling intent precondition/scheduled writer optimistic concurrency token/stale future intent epoch的全部新用途。`node_generation`只保留Stage2 Node list/projection cursor consistency，其推进条件为：Enable建立current、Disable关闭current各推进一次；already_enabled/already_disabled、future-only cancellation、receipt replay、Health、Connection Test均不推进。后续Round 5另以shared receipt实现冻结的Disable race fence，不恢复generation新职责。`monitoring_state_conflict`仍只用于overlap/inconsistent persisted interval。

### Independent readiness review round 2 disposition

第二轮 independent readiness review 结果：P0=0，P1=2，P2=0，Planning readiness = CHANGES REQUIRED。本轮已针对两项修复：

1. **future-only cancellation no longer advances `node_generation`.** 删除并修正了所有“任一close/cancel即generation+1”的旧措辞。唯一一致契约为：Enable disabled→current +1；Enable already current unchanged；Disable current +1 exactly once；Disable current+future +1 exactly once；Disable future-only unchanged；Disable no current/no future unchanged；Health、Connection Test、replay unchanged。future-only cancellation仍是真实monitoring domain mutation，必须保留cancelled future、transition audit与receipt语义，不得误写成state no-op。

2. **Disable does not create a durable-disabled latch.** 该结论仅表示Disable后新形成的受控intent可执行；它不允许Disable前已经形成且等待提交的旧intent越过Disable boundary。Round 5通过shared receipt fence补齐两者区分，且不使用`node_generation`。

### Independent readiness review round 3 disposition

第三轮 independent readiness review 结果：P0=0，P1=1，P2=0，Planning readiness = CHANGES REQUIRED。本轮已移除唯一剩余的normative contradiction：

- 所有monitoring writer统一采用Node row `FOR UPDATE`优先、随后按`effective_from ASC, monitoring_activation_id ASC`稳定锁定affected monitoring rows。
- 只有实际改变current projection的Stage 3 product Enable/Disable才在monitoring-row locks之后锁定并推进`node_generation`；future-only cancellation和no-op均不锁定或推进generation。
- existing operational scheduled writer保持`node_generation`-independent；Round 5增加独立shared-receipt F0/F1 fence，以满足冻结race而不改变generation职责。

### Independent readiness review Round 5 disposition

最新 independent readiness review 结果：P0=0，P1=3，P2=0，Planning readiness = CHANGES REQUIRED。本轮修复如下：

1. **P1-1 — Stage3 scheduling race contradicted frozen Phase6 acceptance.** Disable-first现在必须使pre-existing waiting scheduled writer conflict。最终使用shared Disable receipt：F0在intent形成时有界捕获，F1在READ COMMITTED write transaction取得Node lock后有界重读；writer-first由Disable取消已提交future，Disable-first使旧intent零insert conflict，Disable后新intent可正常执行。already-disabled新command也写receipt/fence，不建立durable latch。
2. **P1-2 — Node probe audit lacked target identity.** Health与Connection Test audit details均加入canonical `instance_id`，保留独立action及bounded result/reason/latency_ms；metrics仍禁止instance_id label，所有网络原文继续禁止。
3. **P1-3 — generation transaction shorthand remained unconditional.** 所有transaction顺序改为domain mutation后仅在current projection实际改变时锁generation row并+1；future-only cancellation不锁/不更新generation但保留transition audit+receipt；already-enabled/already-disabled无generation。

最终fence选择与依据：

```text
Disable fence representation = latest committed node.monitoring_disable receipt command_id
latest order = SUPERSEDED BY ROUND 6; current contract uses strict per-Node committed_at DESC only
bounded lookup = partial expression index on sanitized_result.instance_id + LIMIT 1
existing truth sufficient = every accepted Disable, including no-op, has one immutable receipt
new durable business truth = none
no-op Disable fence = writes receipt/fence; zero domain/generation/transition audit
compatibility class/floor = 2 / 2, conditional on real Stage2 artifact and rollout/rollback acceptance
rollback evidence = drain writers; no stale F0 survives restart; forward fence-aware path retained; old five-argument entry fails closed
```

### Independent readiness review round 6

最新 independent readiness review 结果：P0=0，P1=1，P2=0，Planning readiness = CHANGES REQUIRED。

Finding：Disable receipt fence先前以`(committed_at DESC, command_id DESC)`选择latest，但随机command_id不能证明时间顺序，且`committed_at`尚无同一Node严格单调的serialization保证。

Disposition：冻结strict per-Node Disable receipt ordering invariant。每个新Disable在持有Node lock时读取previous fence timestamp，并分配`max(clock_timestamp(), previous + 1 microsecond)`；因此later serialized Disable MUST拥有strictly greater `committed_at`。latest lookup只按`committed_at DESC LIMIT 1`；command_id仅是返回的fence identity，不是temporal tie-break。新增transaction-start逆序、forced same-clock、A/B/C连续Disable、already-disabled fence及receipt retention验收。

### Validation commands and scope proof

从Control根目录运行：

```bash
python3 openspec/changes/add-relay-node-management-operations/check-planning.py
openspec validate add-relay-node-management-operations --strict
openspec validate add-relay-node-asset-lifecycle-management --strict
openspec validate add-gateway-asset-lifecycle-management --strict
openspec validate fix-control-web-static-resource-prefix --strict
openspec validate --all --strict
git diff --check
git status --short
git diff --name-only
```

- 四个change strict：PASS。
- all strict：27 passed / 0 failed。
- check-planning：4个exact MODIFIED标题、baseline/Stage2全部既有scenario标题及两个canonical bytes/hash fixture PASS。
- git diff --check：PASS；所有tracked/untracked新增修改仅在本change目录。
- 其他change、production代码、migrations/api/queries/internal/cmd/web/deploy/generated：未修改；未git add/commit/push。
- 58个implementation tasks全部unchecked，completed=0；没有运行Go/frontend build或runtime/migration验收（planning-only）。

### Readiness gate

```text
Detailed planning = COMPLETE
Independent readiness review = PASS
P0 = 0
P1 = 0
P2 = 0
Planning readiness = PASS / READY
Implementation readiness = READY
production code changed = false
openspec apply = NOT AUTHORIZED / NOT RUN
Implementation = NOT STARTED
Runtime Acceptance = NOT STARTED
completed implementation tasks = 0
```

独立readiness review已确认planning与implementation readiness；openspec apply仍未获授权，本change未开始implementation。
