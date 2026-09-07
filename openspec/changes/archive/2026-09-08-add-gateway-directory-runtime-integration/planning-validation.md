# Planning and acceptance evidence

状态：Architecture Contract Re-review APPROVED（2026-09-07）；Implementation/Release Final Review（2026-09-08）未发现P1/P2 blocker，27/27，本地已更新为干净commit构建镜像并通过发布冒烟，等待最终归档授权。下文历史 planning/review 段落保留其当时状态；当前结果以文末 Final review and local release 为准。未部署生产、归档或修改已归档 Topology change。

## Acceptance matrix

| ID | Fixture / command family | 必须证明的断言 | 当前结果（详见 Implementation evidence） |
| --- | --- | --- | --- |
| R1 | runtime配置与fake HTTP/HTTPS计数 | disabled零请求/零run；invalid enabled配置fail closed | PASS，见实际专项与范围说明 |
| R2 | 真实PG+进程启动/退出 | 固定cadence/budgets、受限pool、停止领取与bounded shutdown | 专项 PASS，见最新精确断言；发布范围另列 |
| R7 | 多Gateway慢/失败前项、跨槽及取消fixture | runtime无重复ScheduleTick；后项继续、按当前DB槽调度、同槽去重、不回填 | PASS；Accepted traversal acceptance、FrozenBudgets 与进程竞争共同覆盖，A/B为已批准合成工作项 |
| R3 | 多进程/lease/unknown commit | 单槽唯一、旧fence不得finalize、restart可恢复 | 专项 PASS，见最新精确断言；发布范围另列 |
| R4 | 真实PG+慢header/body/finalize、timeout收尾 | 独立5s attempt；失败写入另有最多5s且不延长成功/lease；取消/unknown commit恢复 | 专项 PASS，见最新精确断言；发布范围另列 |
| R5 | A NULL/B正常、多tick、补填与调度双顺序 | A零run/零请求且仍可补填；B成功；无NULL新run | PASS；合成遍历、真实PG UnconfiguredNoWork 与 InitialConcurrency 双顺序 |
| R6 | A非NULL但凭据失败/B正常、历史NULL run | A正常durable failure，B成功；历史保留且reconcile，不绕过补填条件 | PASS；合成遍历、真实PG SourceLifecycle 与 HistoricalNullRun 专项 |
| T1 | HTTP与自签名/未知CA/过期/错误主机名HTTPS | 无许可列表均可采集；Node同策略、范围外客户端不变；public Directory拒绝 | PASS，见实际专项与范围说明 |
| T2 | 无效URL/非HTTP协议、TLS握手失败、双协议redirect | 正常失败、不降级、不转发token、不刷新freshness | PASS，见实际专项与范围说明 |
| T3 | reader token缺失/错误/轮换 | 失败不刷新freshness；同引用恢复后成功 | 专项 PASS，见最新精确断言；发布范围另列 |
| A1 | registrar+空历史真实PG | UUID不变，合法补全；reference精确no-op不新增audit | PASS，见实际专项与范围说明 |
| A2 | failed/running/succeeded run、snapshot、observation、Binding history | 任一历史均拒绝真实配置变更；无清表绕过 | 专项 PASS，见最新精确断言；发布范围另列 |
| A3 | 两个补全及首次schedule/Binding竞争 | NULL条件CAS、串行化、endpoint不变且不覆盖已有reference | 专项 PASS，见最新精确断言；发布范围另列 |
| A4 | runtime/PUBLIC/registrar与audit失败 | 最小ACL、成功audit同事务、失败完整rollback，无敏感值 | 专项 PASS，见最新精确断言；发布范围另列 |
| A5 | migration Up/Down/应用rollback | 不删除资产/审计/采集数据，保留forward兼容 | PASS，见实际专项与范围说明 |
| E1 | 真实source v1 changed/unchanged/失败/恢复 | int64无损、去重、last_success_received_at只随成功更新 | PASS，见实际专项与范围说明 |
| E2 | 已有HTTP候选/bind/read | decimal string精确ID、BOUND/resolved/current，未action不自动绑定 | PASS，见实际专项与范围说明 |
| E3 | Directory暂停/故障与数据面请求 | Binding保留、stale→unknown、恢复resolved，数据面独立 | PASS；Local recovery acceptance，真实540秒自然过期和正常180秒槽恢复 |
| O1 | metrics/logs/配置dump canary | 无token/reference/账号身份/原始响应输出 | PASS；既有metrics专项与最终真实main stdout/stderr、HTTP metrics、runtime错误日志canary；无产品config dump surface |
| N1 | Node HTTP/各类不可信HTTPS合成fixture | 无许可及证书检查；健康/账号/版本观察遵守固定接口 | PASS，见实际专项与范围说明 |
| N2 | 普通DNS变化、混合结果、特殊地址fixture | 不按地址类别或DNS重绑定拒绝；不访问真实元数据 | PASS，见实际专项与范围说明 |
| N3 | 旧DNS/CIDR/CA变量缺失/非法/残留 | 不阻止启动、不读取CA、不影响新策略；rollback前恢复旧条件 | PASS，见实际专项与范围说明 |
| N4 | Secret/无代理/redirect/预算/响应及范围外配置 | 原有认证和数据验证保持，入站/数据面不变 | PASS，见实际专项与范围说明 |

## Planning validation

仅运行本change strict、全部OpenSpec strict和git diff检查。change strict通过；全部strict为14 passed / 0 failed；diff检查通过，新增文件另行检查行尾空白。不运行昂贵构建、浏览器或PG业务验收。真实本地账号、凭据、配置文件内容不进入本change。实现任务全部保持未完成；架构复审结果见下文；实现验收仍未执行。

## Current transport decision

用户明确要求移除HTTP origin白名单和HTTPS证书验证，替换此前HTTPS-only及精确origin opt-in草案。Directory直接支持HTTP/HTTPS；不新增目标许可或证书配置，只保留enabled和secret mapping。HTTPS不验证证书链、有效期或主机名，作用于Control的所有Gateway/Node管理出站transport。HTTP明文与HTTPS不认证服务端的影响已记录在design。

URL解析、既有资产登记契约、专用token、响应schema/identity验证、预算与禁止redirect不变；Node客户端采用相同传输策略。MODIFIED Requirements覆盖固定fetch契约；canonical与归档内容不改。资产只补填NULL reader reference，复用Gateway行锁并保留原子audit/ACL。

本轮仅文档修订，未修改或启用生产transport。用户已决定上述安全边界，整份change已完成Architecture Contract Re-review；全部实现验收保持未完成。

## Scope expansion

用户明确批准扩大到Control所有Gateway/Node管理出站调用。新增统一能力与Node MODIFIED requirements，覆盖原SSRF、TLS及回滚陈述；Node版本仅读取现有响应头，不增加接口。旧网络许可/CA变量退役；两类客户端策略一致，范围外连接不改。仅规划文档，验收尚未运行。

## Architecture review revision

修订P1：现有ExecuteAttempt透传ctx且client无Timeout，设计现明确每次claim后5s执行截止与独立最多5s失败收尾，不能声称原实现已满足。修订P2：共同ScheduleCurrent在Gateway行锁内检查NULL，返回no-work；同步修正原先调度先行必然产生run的错误假设。R4–R6为新增验收，尚未执行。修订随后经主评审与独立复核确认，见下文；不代表实现完成。

## Architecture Contract Re-review result

2026-09-07：APPROVED，P1=0、P2=0。主Agent及独立review_transport_contract复核一致：5s attempt/有界失败收尾、NULL no-work/共同行锁/其他Gateway继续采集契约闭合。用户随后授权继续提交架构基线。tasks仅1.1、1.2、6.1评审项完成，全部实现与发布验收仍待执行。

提交前清理三份文档EOF多余空行；重新执行change strict、all strict及working-tree/cached diff检查。未运行业务测试、未修改生产代码、未部署或归档。

## Implementation simplification review

用户授权在当前change精简并amend架构提交。只读代码确认WorkOnce→RunGatewayOnce→ScheduleCurrent已覆盖逐Gateway调度，故runtime改为ReconcileTick→WorkOnce。针对性核对：共同NULL行锁入口保留、reconcile先行、当前DB槽/不回填和取消预算不变；R7明确慢/失败前项及后续进度验收，尚待实施证明。标准拨号、SQL模板、fixture复用和Runbook合并属于实现组织调整；原P1/P2修复与全部验收保留。

## Runtime ACL correction

首次真实runtime专项因gateway_instances reference直接SELECT权限不足失败，未用owner绕过。用户批准同migration新增fenced target read function，直接列权限保持不变；相关成功与越权断言必须重新验证，原失败不记为PASS。

## Implementation evidence (2026-09-07)

本节记录实际运行结果；不把代码存在或 planning validation 当作验收通过。Go 命令未覆盖 GOCACHE/GOTMPDIR，真实 PG 使用独立测试数据库和受限 runtime role。owner 仅用于 migration、fixture setup 和断言；产品读取使用 runtime pool。

| 覆盖 | Implementation / fixture | 实际命令与结果 | 精确断言与剩余边界 |
| --- | --- | --- | --- |
| R1、R2（部分）、O1（标签） | `cmd/control/gateway_directory_runtime.go`、`gateway_directory_runtime_test.go` | `go test ./cmd/control -run 'TestLoadGatewayDirectoryRuntime\|TestGatewayDirectoryRuntime' -count=1 -v`：5 项 PASS | 默认关闭；缺失/非法 mapping 拒绝启动；独立 resolver；Reconcile→Work；取消退出；指标无身份/endpoint/Secret 标签。 |
| R2、R3（restart）、R4（取消）、T1、R1 | `cmd/control/gateway_directory_process_test.go` | `go test ./cmd/control -run '^TestGatewayDirectoryRuntimeProcessRecovery$' -count=1 -v`：HTTP/HTTPS PASS，132.10s | 真实子进程运行产品 runtime，disabled 零请求/零 run；SIGTERM 有界退出；新进程在原 lease 到期后 reconcile 并恢复成功；精确 numeric source ID。此测试不是完整 main bootstrap；双存活实例竞争由下方独立专项覆盖。 |
| R4 | `internal/store/gateway_directory_runtime_integration_test.go` | `go test ./internal/store -run '^TestGatewayDirectoryRuntimeAttemptTimeout$' -count=1 -v`：慢 header/body PASS | 每次 5s attempt；第一次 retry_wait、第二次 failed；每阶段两次请求；失败不生成 current state。等待真实 DB claim window，无 skip。 |
| R4、R3（lease expiry） | 同上 `TestGatewayDirectoryRuntimeFinalizeDeadlineAndRecovery` | 同名精确 `go test ./internal/store -run '^TestGatewayDirectoryRuntimeFinalizeDeadlineAndRecovery$' -count=1 -v`：PASS，15.69s | 持锁阻塞成功 finalize，5s 截止后无成功 state；释放锁并等待真实 15s lease 到期；新 service reconcile 恢复。未伪造延长 lease 或用新 context 重提交成功。 |
| R5（单 Gateway） | 同上 `TestGatewayDirectoryRuntimeUnconfiguredNoWork` | 对应精确专项：PASS | NULL reference 连续三个 WorkOnce 均 no_work、零 run。不替代 A/B 多 Gateway 验收。 |
| E1、T1、T3（错误凭据）、E2（无自动绑定） | 同上 `TestGatewayDirectoryRuntimeSourceLifecycle` | `go test ./internal/store -run '^TestGatewayDirectoryRuntimeSourceLifecycle$' -count=1 -v`：HTTP/HTTPS PASS，1.48s | changed→unchanged→401 failed→unchanged recovery；仅成功推进 observation；一个 snapshot、零 Binding；4 个边界 int64 精确持久化。fixture 在独立历史槽建立 running run，不修改 terminal run；实际 cadence 由 process fixture 验证。 |
| A1、A4 | `gateway_directory_reader_initial_integration_test.go` | `go test ./internal/store -run '^TestGatewayDirectoryReaderInitialConfiguration$' -count=1 -v`：PASS | 首次 NULL 补填、同引用 no-op、唯一 audit、runtime 拒绝补填、伪造审计拒绝。 |
| A5 | `gateway_directory_reader_migration_roundtrip_integration_test.go` | `go test ./internal/store -run '^TestGatewayDirectoryReaderMigrationRoundTrip$' -count=1 -v`：PASS | 18→17→18；仅入口函数撤回，历史 audit 及 shape guard 保留；再次相同补填无重复 audit。 |
| A4、R3（fence） | `gateway_directory_target_read_access_integration_test.go` | `go test ./internal/store -run '^TestGatewayDirectoryTargetReadAccess$' -count=1 -v`：PASS，38.416s | 有效 run/gateway/fence 可读；错误 gateway/token 和真实过期 lease 返回零行；runtime 直接 reference SELECT、registrar 调用均 42501。 |
| E2 | `internal/api` 既有 Binding HTTP suite | `go test ./internal/api -run '^TestRelayBindingHTTPCompleteSuite$' -count=1 -v`：9 subtests PASS | candidates、bind/read/rebind/unbind、无 action 不自动绑定、stale/conflict、精确 decimal-string int64 与 overflow。真实 PG。 |
| N1、N2、N4、T1、T2 | `internal/drivers/cliproxyapi`、`internal/drivers/gatewaydirectory` | `go test ./internal/drivers/cliproxyapi ./internal/drivers ./cmd/control -count=1`：PASS；`go test ./internal/drivers/gatewaydirectory -count=1`：PASS | 实际 HTTP、不可信 HTTPS、合成特殊地址、固定路径、Secret/响应预算回归。没有把旧拒绝测试改名后冒充执行；不适用策略断言已替换为新契约测试。 |
| 5.4（构建） | Makefile 既有流水线 | `make test build`：PASS | Go/tools/frontend tests、生成、typecheck、Vite 与 Go build；该命令未设置 PG URL，不代替独立真实 PG 专项。 |
| T1（现有 public proxy） | 本地已部署 Gateway public 入口，只读探测 | `curl --noproxy '*' -sS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:18082/internal/v1/api-account-directory`：403 | 未部署当前改动；仅证明现有 public proxy 拒绝 Directory。 |
| 7.2 | `docs/runbooks/gateway-directory-runtime.md`、`deploy/asset-registry/`、ops `dev/` | `bash -n ../ops/dev/devctl`：PASS | 默认关闭、独立 mapping、先 source 后 Control、HTTP/HTTPS、旧配置退役、补填与 rollback；未执行 devctl init/up。 |

后续补强结果见下方 Additional targeted evidence；当前未闭合项目统一列在 Remaining gates and review scope，任务状态不由 make PASS 自动推导。

### Additional targeted evidence

- `TestGatewayDirectoryRuntimeUnknownCommitRecovery`：真实 PG runtime 连接在收到服务端 COMMIT acknowledgement 后丢弃响应并返回 EOF；产品调用返回错误，但 DB 同一 run 已 succeeded。新 repository/service reconcile 不改变 terminal state，同槽 no_work，仅一条 run/一份 snapshot/一次 source 请求，snapshot 与 received_at 原值不变。`go test ./internal/store -run '^TestGatewayDirectoryRuntimeUnknownCommitRecovery$' -count=1 -v`：PASS，77.664s（包含等待真实 claim window）。测试仅包装隔离 plaintext PG 连接，不改变生产连接或提交实现。
- `TestGatewayDirectoryReaderInitialConcurrency`：真实 sqlc 调度在 NULL 行锁内返回零行；补填等待后成功且零 run；反向顺序补填先提交，真实调度创建 exactly 1 run。不同引用竞争恰为一成功/一23505，reference、endpoint、audit数量逐项断言。精确专项 PASS，3.067s。
- `TestGatewayDirectoryReaderInitialRejectionMatrix/(failed terminal|succeeded terminal)`：真实合法 terminal run 的两种 status 均使 NULL 补填返回23505；succeeded fixture 保留合法 snapshot。精确专项 PASS，2.079s。running、snapshot-only、audit rollback 专项也已 PASS；Binding/current-history 的补强结果见下文。
- `TestNodeDriverRuntimeEnabledConstructsWithoutSecretOrNetworkAccess`：旧 DNS/CIDR/plain HTTP 值非法、CA_FILE 指向不存在文件仍可构造 Node runtime；不访问 Secret/网络。`go test ./cmd/control -run '^TestNodeDriverRuntimeEnabledConstructsWithoutSecretOrNetworkAccess$' -count=1`：PASS。
- `TestManagementRedirectAndHandshakeFailure`：HTTP/HTTPS redirect 均拒绝，redirect trap 零调用且错误不含合成 token；TLS 请求指向 HTTP fixture 正常失败，没有降级 HTTP 请求。`go test ./internal/drivers/gatewaydirectory -run '^TestManagement' -count=1 -v`：PASS，0.568s。
- change strict：PASS；`openspec validate --all --strict`：14 passed / 0 failed；`git diff --check`：PASS。strict 仅证明规范格式有效，不替代未完成的实现验收。

失败记录：首次失败收尾阻塞 fixture 锁了 Gateway 行，而实际失败 UPDATE 锁 run 行，未制造预期阻塞；因此该次 FAIL 不计为产品缺陷或验收通过。已改为锁精确目标 run，并加强超时、错误与持久状态断言；复跑 PASS 见下文。

- `TestGatewayDirectoryRuntimeProcessSameSlotCompetition` 加强后 PASS，36.705s：两个子进程各自在完成真实 runtime.tick 后写入各自 marker，父测试等两者完成后才核对同槽 run=1/source request=1/current=1、精确 account ID；均 SIGTERM 有界退出。没有以“第二进程启动成功”代替其实际执行 tick。
- `go test ./internal/store -run '^TestGatewayDirectoryFailureBookkeeping' -count=1 -v` 修正 fixture 后 PASS，50.271s：目标 run 行被锁，401 收尾 4.5–8s 内返回错误，run 仍 running、failure class 空、current 未推进；真实 lease 到期后 reconcile 同一个 runID。parent cancellation 返回错误、run 保持 running，不脱离父 context 写入。
- `TestGatewayDirectoryReaderInitialConcurrency/registrar_lock_timeout`：200ms lock_timeout 返回55P03，ref仍NULL/audit0；解锁后可正常补填。PASS。
- `go test ./internal/store -run '^TestRelayBindingRepository_ReadModel$/^lifecycle' -count=1 -v`：PASS，1.030s；同一 Binding 在 account disappearance、新ID同metadata、旧snapshot重用、stale→unknown→fresh→resolved 下保留identity；不代替本轮真实数据面故障隔离演练。
- 删除无调用的旧 DNS/CIDR 校验 helper 后，`go test ./internal/drivers ./internal/drivers/cliproxyapi ./cmd/control -run 'TestManagement|Test.*Config|TestNodeDriverRuntime' -count=1`：三包 PASS。保留旧配置输入字段仅用于源码兼容，值不再执行许可策略。
- 现有本地部署只读检查：Control `/api/healthz`、Node `/healthz`、Node `/management.html`、Gateway `/` 均 HTTP 200。命令为 `curl --noproxy '*' --max-time 10 -sS -o /dev/null -w '%{http_code}\n' <对应本地URL>`；未启动或替换服务，这些结果不冒充当前代码已部署。

- `TestGatewayDirectoryRuntimeSourceLifecycle` 最终六阶段 fixture：正确 token changed/unchanged、旧 token 被真实 Authorization 校验拒绝401、同引用更新文件恢复、rename 使文件缺失产生 failed/secret_unavailable、恢复文件后 unchanged。每个成功严格推进 observation，每个失败保持原值，HTTP 请求总数5（缺失文件阶段零请求）；四个 int64、snapshot1、Binding0 原断言保留。HTTP/HTTPS 精确专项 PASS，2.166s；替代前述四阶段结果作为最新证据。
- `TestGatewayDirectoryReaderInitialRejectionMatrix/binding_history_conflict` 最终 fixture 包含合法 Binding、snapshot items、snapshot、current observation 及 succeeded run；NULL 补填返回23505，Binding/snapshot/current/succeeded run保留，asset audit0。真实 PG 精确专项 PASS，A2全部历史类型已覆盖。
- `TestManagementEndpointKeepsURLStructureValidation`：HTTP/HTTPS及合成特殊地址可解析；非HTTP协议、userinfo、query/fragment、非法port/path保持拒绝。精确专项 PASS，0.632s。

- `TestGatewayDirectoryReaderInitialConcurrency/binding_and_fill_share_gateway_lock`：合法 snapshot/current/succeeded run 和未绑定Node；同时运行生产 Bind 与 registrar补填，Gateway锁持有时均等待。解锁后Bind成功，补填23505；readerNULL、Binding1、asset audit0、endpoint不变。真实 PG 精确专项 PASS。
- `TestGatewayDirectoryRuntimeFrozenBudgets`：真实 runtime repository 同槽重复调度返回既有 skipped/no-work，DB恰保留原run；scheduled_at 对齐180秒、lease精确15秒；DB observation年龄539s为fresh、541s为stale。精确专项 PASS，7.144s。首次 fixture 错误期待返回原 active row，已按既有 skipped contract修正，并保留精确runID/count断言；未修改生产SQL。

### Accepted traversal acceptance

用户接受保留单Gateway数据库模型，A/B使用合成有序工作项测试；真实单Gateway PostgreSQL保持NULL、凭据失败、调度、并发、预算、恢复验收。design/spec/tasks已同步，不删除singleton约束，不增加多Gateway产品能力。

`WorkOnce`调用私有`workGatewayDirectoryInstances`，不新增公开接口或配置。`go test ./internal/store -run '^Test(ValidateGatewayDirectoryAttemptResult|WorkGatewayDirectoryInstances)' -count=1 -v`：6 tests PASS，0.662s。保留原有attempt-result形状测试；新增error后继续、durable secret_unavailable后继续、no-work后继续、channel barrier证明慢A完成后才处理B、父取消阻止B。配合真实PG与进程证据关闭3.2/3.6。

最终`make test build`：exit 0；Go全仓、tools、65个Web tests（15文件）、typecheck、Web build和Control build通过。该命令不替代此前已完成的真实PG专项。

### Local deployment acceptance and blocker

本地已先备份Control数据库和受保护运行配置；原6条Inventory记录及Node账号文件保留。使用标准Goose将本地数据库17→18，使用已有registrar SQL模板完成零历史Gateway reader首次补填，返回configured。未直接修改endpoint，未手工创建run/snapshot/observation，未使用owner作为产品连接。

构建本地工作树验收镜像`relay-station/control:directory-runtime-local`，镜像ID `sha256:16c238af0f5ab47bd6d4568302987bfaaa8f3dae99209de7887c035c7bef04f1`，版本标识`460f15b-directory-runtime-worktree`。它不是干净Git revision的正式发布镜像。前端构建复用缓存；采用现有Compose栈并保留全部volume，更新受保护post-bootstrap override安装独立Directory凭据。没有执行devctl的正式clean-revision发布流程，也没有声称该检查已通过。

实际命令/结果：

- `docker build --build-arg VERSION=460f15b-directory-runtime-worktree -t relay-station/control:directory-runtime-local .`：PASS。
- `go tool goose -dir ../migrations postgres <本地受保护连接> up`：00018 PASS；数据库版本18。
- `deploy/asset-registry/configure-gateway-directory-reader.sql`：registrar身份、受保护reference环境输入，configured；凭据和真实身份不写入仓库。
- Directory关闭时新Control容器healthy，runs=0、Inventory=6；Gateway source开启后再启用Control。
- 既有受保护Gateway测试client执行`POST /v1/chat/completions`：HTTP200、choices非空、耗时6.93s；不保存响应内容。请求在Control Directory关闭时发起，证明该关闭不阻断现有Gateway→Node调用。
- 开启真实Directory后，产品runtime记录`contract_invalid`；没有current snapshot，没有自动Binding。
- 从管理网络用正确专用token只读探测`http://gateway:8080/internal/v1/api-account-directory`：HTTP200，Content-Type=`text/html; charset=utf-8`，是SPA HTML，不是source JSON。只输出状态、类型、长度和JSON布尔值，不保存raw payload。

根因（只读确认）：Gateway `backend/internal/web/embed_on.go:355` 的API bypass列表缺少`/internal/`，middleware在handler之前返回index.html；`backend/internal/server/routes/directory.go:8`虽注册Directory路由，却不能被正常访问。运行镜像还落后于Gateway工作树，但当前工作树同样存在该缺口，单纯重建现有代码不能解决。Gateway仓库未修改，Control严格响应验证保持不变。

处置：暂停本地Control Directory runtime并恢复Gateway source关闭；最终aggregate为Inventory=6、current=0、Binding=0、asset补填audit=1、contract_invalid failed run=5。保留00018、首次补填审计、真实失败run及账号数据，不清除失败记录来伪造验收成功。不创建Binding；现有数据面/服务健康独立检查。需要Gateway独立修复：让`/internal/`绕过SPA middleware，并覆盖embed模式下正确token/错误token/关闭状态及public proxy拒绝；不修改source v1或Control API契约。

### Historical remaining gates and review scope (before Gateway fix)

当前22/27。4.4、5.3因上述Gateway服务端路由缺陷阻塞，不能声称真实Directory采集、Binding/resolved/stale→恢复联合验收通过。6.4、7.1、7.3待联合验收闭合后完成最终证据与发布review。当前change明确不改Gateway/Node服务端；Gateway修复需独立change，不在本change中绕过边界。

2026-09-07 已拆分本地提交：Control `bedcbd3`（管理transport）、`76b50c5`（Directory runtime、受限query、首次补填、migration及专项验收）；ops `5bc0663`（独立Directory凭据与部署模板）。Runbook与本验收记录随独立文档提交保存。未push或archive。前述部署镜像仍是工作树验收镜像，不声称由这些干净commit构建；正式发布必须重新记录revision与镜像对应关系。

收尾只读检查：Control `/api/healthz`、Gateway `/`、Node `/healthz`均HTTP200；public `/internal/v1/api-account-directory`仍403。两端Directory开关实际为false，Gateway仓库在独立路由修复change创建前工作树干净；Control/ops已按上述边界提交。

历史NULL专项：`go test ./internal/store -run '^TestGatewayDirectoryHistoricalNullRunIsReconciledWithoutNewSchedule$' -count=1 -v`真实PG PASS，16.301s；正常Schedule/Claim后模拟历史reference变NULL，后续调度不新增run，真实lease到期后reconcile同一runID，From=running、FailureClass=lease_lost，run count仍1。fixture先等待真实claim window，不skip、不删除历史。最终遍历专项保留原有测试原文后再次PASS，0.528s。

### Local recovery acceptance (2026-09-08 Asia/Shanghai)

Gateway 独立 `fix-gateway-directory-embedded-ui-routing` 已完成13/13，生产修复 commit `dfe4419233ef62bf7f9a4c9a4b5a7bff9802165d`；本地部署镜像 `relay-station/gateway:dfe441923`，image ID `sha256:57b03961e744fe2b4c7d39dd9e9fe4c639eaf910cdfde52ba1bd972da20d4198`。其余后续提交仅测试/证据；修复和验收不并入 Control 产品代码。上述 SPA 阻塞记录保留为历史，当前入口返回 source v1 JSON。Control 仍使用前述工作树验收镜像，不声称 clean-revision 正式发布。

受保护本地 harness `/Volumes/DevRAM/tmp/relay-directory-http.py` 通过真实管理员会话调用既有 candidate/bind/read API；精确 Account ID 只从已有受保护配置取得，Binding ID 与 Account ID 保存于仓库外0600 baseline文件，不记录真实值。source numeric JSON→Go int64 和 Control HTTP decimal string 契约不变。以下时间统一为 UTC：

| 时间 / 操作 | 命令 | 实际断言与结果 |
| --- | --- | --- |
| 16:05:49，显式 bind | `python3 /Volumes/DevRAM/tmp/relay-directory-http.py bind` | candidate `accounts` 精确命中已知ID；既有 action 后 bound=true、resolved/fresh/current、string身份精确；非自动关联。 |
| source关闭后 16:09:35 baseline | `python3 /Volumes/DevRAM/tmp/relay-directory-http.py fault-baseline` | 最后成功观测固定为 `2026-09-07T16:06:03.684839Z`，保存同一Binding基线。 |
| 16:14:21，仍在窗口内 | `python3 /Volumes/DevRAM/tmp/relay-directory-http.py read` | bound=true、binding_same=true、resolved/fresh，last_success未推进。 |
| 16:15:07，自然超过540秒 | `python3 /Volumes/DevRAM/tmp/relay-directory-http.py assert-stale` | 同一Binding、同一string身份；unknown/stale/last_known，last_success逐字等于基线；无SQL回写时间或手动finalize。 |
| stale期间真实AI调用 | `python3 /Volumes/DevRAM/tmp/relay-directory-http.py data-plane` | Gateway→Node `POST /v1/chat/completions` HTTP200，choices存在，7.78s；不保存响应内容。 |
| 恢复source | `python3 /Volumes/DevRAM/tmp/relay-gateway-routing-release.py source true` | Compose保留volume、Gateway healthy；Control保持启用，等待真实调度，不手动触发run。 |
| 16:18:32，正常槽恢复 | `python3 /Volumes/DevRAM/tmp/relay-directory-http.py assert-recovered` | 同一Binding、同一string身份恢复resolved/fresh/current；last_success严格推进至 `2026-09-07T16:18:03.675169Z`。 |

故障期间只读DB聚合证明 `http_non_retryable` failed run 增加，旧 `contract_invalid` failed run=5继续保留，current=1且失败不推进成功时间。未删除run、snapshot、Binding或6个Node账号。真实精度边界由前述合成source/Binding HTTP专项覆盖，本地真实账号不冒充大整数fixture。

恢复后运行 `python3 /Volumes/DevRAM/tmp/relay-gateway-routing-release.py check true`：正确token200/source JSON/no-store/account_count=1；缺失/错误token401；POST405；未知internal路由404；public Directory403；Gateway UI与health200；reader访问admin accounts401。该管理HTTPS探测验证既有CA，Control实际采集仍为登记的内部HTTP；不能将该探测作为Control不验证证书的HTTPS证明。

Control disable/restart：恢复成功后运行 `python3 /Volumes/DevRAM/tmp/relay-directory-release.py control false`，Compose healthy。停用基线为run=12、current=1、last_success=`2026-09-07T16:18:03.675169Z`；跨过16:21正常调度槽后，三项逐字不变，Control `/api/healthz` HTTP200。随后运行同一harness `control true`，16:24:40既有 `assert-recovered` PASS；新成功观测为 `2026-09-07T16:24:00.037031Z`，同一Binding/Account身份保持resolved/fresh。最终本地source和Control poller均为true，保留现有显式Binding与6条Inventory；产品默认false不变。没有修改Node配置、清空数据或正式生产发布。

### Documentation reconciliation

Control `docs/runbooks/cliproxyapi-readonly-driver.md` 与 `gateway-directory-runtime.md`、ops `docs/RELAY_STATION_SYSTEM_DESIGN_CN.md` 已统一直接HTTP/HTTPS、移除目标许可与证书验证、Secret/固定路径/预算保留、旧变量退役及回退旧版本必须恢复旧DNS/CIDR/CA与目标证书的条件。ops文档独立提交 `2c983c5`，不夹带Control代码。

Node canonical Purpose 的待同步准确文本保存在本change `design.md`，对应MODIFIED Requirements已准备；只有后续获得归档授权才同步canonical，当前没有手工修改canonical或任何archived change。6.4在本阶段的文档交付与归档同步准备完成，不宣称canonical已同步。

### Final targeted evidence

新增 `cmd/control/gateway_directory_main_deployment_test.go` 的 `TestGatewayDirectoryMainDeploymentHTTPS`：子进程直接调用产品 `main()`，仅传runtime数据库连接及合成启动配置，不继承owner/test连接变量。owner仅在父进程创建隔离DB、执行已有Goose migrations、准备合法environment/Gateway和读取断言。合成TLS source使用自签名证书，且明确断言证书 `VerifyHostname("localhost")` 失败；产品main通过该HTTPS源成功持久化source numeric ID `9007199254740993`。不替换产品transport、scheduler或SQL。

disabled main健康200且零请求/零run；enabled main等待真实current持久化，健康及`/metrics`均200。真实SIGTERM有界退出后重启main，运行超过完整5秒tick；同槽run=1、source request=1，重启后的current snapshot ID、last_success_received_at与重启前完全一致，重新join读取account_id精确为 `9007199254740993`。为避免测试跨过claim window，fixture按真实DB时钟等待剩余窗口足够，不改写时间、不skip。该完整main部署专项与本地HTTP Compose验收共同关闭4.4。

O1：同一main测试捕获disabled/enabled/restarted stdout/stderr及实际HTTP `/metrics`输出，断言不含合成reader token、reference、mapping路径、source endpoint或账号ID；`TestGatewayDirectoryRuntimeErrorsUseFixedRedactedLogFields` 经真实JSON slog handler和runtime.tick同时触发reconcile/work错误，输出固定component/reason，不含endpoint/reference/token/raw-error canary。既有 `TestGatewayDirectoryMetricsSnapshotAndCollector` 保留固定metric/label和canary验证。只读检查 `cmd/`、`internal/api/`、OpenAPI无config dump surface，本次检查可见启动日志与metrics；不虚构不存在的配置导出接口验收。

最终精确命令（使用README隔离测试PG的owner/runtime两个环境变量，不覆盖GOCACHE/GOTMPDIR）：

```sh
go test ./cmd/control -run '^TestGatewayDirectory(RuntimeErrorsUseFixedRedactedLogFields|MainDeploymentHTTPS)$' -count=1 -v
```

真实PG运行PASS：MainDeploymentHTTPS **64.36s**（包含真实claim window等待）、RuntimeErrorsUseFixedRedactedLogFields **0.00s**，package **65.026s**。先前草稿因过短等待窗口失败，不计为产品失败或PASS；修正窗口后首次PASS，再经独立review补强重启后精确持久值断言，以上为最终复跑结果。

仅新增测试和文档；生产实现保持 `bedcbd3` / `76b50c5`，复用已有最终 `make test build` 与PG验收结果，不重跑完整构建、Chrome或全量PG。OpenAPI/生成客户端、Node服务端、Control入站与数据面实现本轮均无diff。

最终change strict PASS；`openspec validate --all --strict` **14 passed / 0 failed**；working-tree及提交前cached diff检查PASS。当前27/27；未push、archive或生产部署。Control/ops按仓库分别提交；Node仓库的既有 `AGENTS.md` 修改不属于本change，未修改或提交。

### Final review and local release (2026-09-08 Asia/Shanghai)

主Agent与三个独立只读审查方向完成Final Review：存储/SQL/ACL/预算与恢复、Gateway/Node管理transport和回滚文档、main专项/27项任务与实际证据。未发现P1/P2 blocker；未新增产品行为或重跑完整构建测试、Chrome、全量PG。`74438ad`保存最终main与日志专项；`c7c244b`保存27/27证据和文档。

本地正式版本构建的source为干净Control commit `c7c244b03d3ddefc0e723b7f0dd21a8de329fa1f`，命令：

```sh
docker build --build-arg VERSION=c7c244b --label org.opencontainers.image.revision=c7c244b03d3ddefc0e723b7f0dd21a8de329fa1f -t relay-station/control:c7c244b .
```

构建PASS，前端层复用缓存；image ID为 `sha256:31758fb978eb878afdc2b232d5923c01ffe5e53081bc6c2cb4609ae1e1fc6e83`。后续本文件的发布记录提交仅为文档，不作为该镜像构建来源；正式版本表示commit可追溯的本地镜像，不表示生产发布。

运行 `python3 /Volumes/DevRAM/tmp/relay-directory-release.py release-control relay-station/control:c7c244b`：harness先核对Control工作树干净与image revision=HEAD，将现有Compose override备份到仓库外受保护目录，再只更新Control image并执行 `docker compose ... up -d --no-deps --wait control`。失败会恢复原override并重建原Control；实际更新成功且healthy。Gateway、Node容器ID在更新前后完全相同；不运行migration、不改开关、不清空volume或账号。

这是当前已运行栈的Control单服务更新，未执行整栈 `devctl up`，不声称整栈clean-HEAD preflight通过：Node有用户既有AGENTS.md未提交修改；Gateway镜像指向生产修复commit，后续HEAD为测试/证据提交。两者未被本轮修改或部署。既有Control入站TLS代理未改配置，实际HTTPS UI和管理员API冒烟成功。

容器实际启动时间为 `2026-09-07T16:35:38.722267359Z`；`docker inspect`核对配置image、实际image ID和healthy；健康API返回 `{"status":"ok","version":"c7c244b"}`。新进程的正常采集成功观测时间为 `2026-09-07T16:39:03.933362Z`，严格晚于容器启动时间；16:40:33执行既有 `assert-recovered`，同一Binding与decimal-string Account身份精确不变，resolved/fresh/current。没有用部署前遗留fresh快照冒充新进程采集成功。

发布后最小冒烟结果：

| 命令/路径 | 结果 |
| --- | --- |
| `python3 /Volumes/DevRAM/tmp/relay-directory-http.py assert-recovered` | PASS；同一Binding、精确string identity、fresh/resolved，新进程成功观测 |
| `python3 /Volumes/DevRAM/tmp/relay-directory-http.py data-plane` | HTTP200、choices存在、6.22s，不保存响应内容 |
| `curl --noproxy '*' ... http://127.0.0.1:18080/api/healthz` | HTTP200、version=c7c244b |
| 同类有界curl `/metrics`、Gateway `http://127.0.0.1:18082/`、Node `http://127.0.0.1:18319/healthz` | 全部HTTP200 |
| `curl --noproxy '*' --max-time 10 --cacert <既有受保护Control CA路径> ... https://localhost:18443/` | HTTP200，既有入站证书验证保持 |
| public `http://127.0.0.1:18082/internal/v1/api-account-directory` | HTTP403，公网拒绝保持 |

最终change strict与all strict重新PASS（14/14），working-tree/cached diff检查PASS。Directory source/poller继续启用，既有Binding与6个Node账号保留。未push、未archive；后续归档仍须按design同步Node Purpose。
