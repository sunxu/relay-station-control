## Context

本 change 是 Phase 4 Directory 的部署接线补全，不是 Topology 后续实现。参考 System Design v1.8/R4.7、ADR-0002 §2.1–2.2/R4.6/R4.7，以及 canonical gateway-account-directory-ingestion、asset-registry 和 relay-node-gateway-account-binding。

本地只读调查事实：
- `cmd/control/main.go:225,361–390` 启动 Inventory、history、durable jobs，但没有 Directory service 调用；`internal/store/gateway_directory_service.go:39,159–232` 已提供构造、ScheduleTick、WorkOnce、ReconcileTick。
- `internal/drivers/gatewaydirectory/client.go:146–173` 已要求 HTTPS；client 使用 TLS1.2 最低版本和系统 trust，没有开发 HTTP 开关。
- `migrations/00003_asset_registry_foundation.sql:519–559` 的 register 仅接受相同输入重放，不能更新旧 endpoint/reference。
- 本地 aggregate：Gateway 1 行、reader reference 0、Directory runs/current state 0。开发 runbook 初始登记的是 HTTP 地址。现有 Gateway→Node 调用已经独立通过；不能据此推断 Directory 可用。

## Goals / Non-Goals

**Goals:** 默认关闭且可运维的持久采集 runtime；直接 HTTP/HTTPS 与专用 Secret 接入；无需清空数据库的首次reader reference补填；通过真实 Directory 和既有显式 Binding action 得到 resolved。

**Non-Goals:** source v2、自动 HTTPS→HTTP 降级、endpoint/已有reference编辑器、已有历史后的 Gateway 目标迁移、自动绑定、自动修改 Account/Group/Node、Topology UI/API 改造、Duplicate eligibility 改变、临时 sidecar runner 或直接 SQL 伪造 snapshot。已归档 `add-node-centric-topology-ui` 保持不变。

## Decisions

### 1. Runtime：复用现有实现

只新增两项配置：`CONTROL_GATEWAY_DIRECTORY_ENABLED=false`、`CONTROL_GATEWAY_DIRECTORY_SECRET_MAPPING_FILE`。复用file SecretResolver，独立于Node Driver开关；enabled时映射配置缺失/非法则启动失败。映射中的目标文件暂不可读仍按原secret_unavailable记录运行失败。disabled不加载外部文件、不创建run、不发请求。

用现有runtime pool构造Directory repository/service，一个循环每5s依次调用已有ReconcileTick、WorkOnce，单进程最多一个在途attempt。WorkOnce逐Gateway调用RunGatewayOnce，后者已调用共同ScheduleCurrent；runtime不再单独调用ScheduleTick，避免重复遍历和调度SQL。既有ScheduleTick入口保留给已有调用者，不为精简而删除公共方法。5s是检查频率，DB槽仍180s，freshness仍540s；attempt/lease/retry/fencing保持canonical契约，不新增可调预算或第二套scheduler。错误脱敏后下个tick继续；停机停止领取、取消context并等待有界收尾，未知提交由原lease/reconciler恢复。复用已有Directory metrics，不新增指标体系；仅补必要enabled状态，禁止身份/凭据标签。

#### Attempt截止时间与失败收尾

现有ExecuteAttempt透传ctx，HTTP client没有Timeout，因此不能宣称已实现5s预算。实施时每次成功claim返回后立即创建独立5s attempt context（父context更早取消则提前结束），覆盖Secret解析、fetch、body读取/验证和成功finalize；每次重试重新创建，不给整轮Gateway遍历共用一个5s context。所有I/O和成功写入使用该context，不能在超时后另开context提交成功。

超时后的失败记录使用从仍有效runtime父context派生的独立、有界5s收尾context，而非已过期attempt context；这5s只用于失败状态记录，不延长fetch、成功提交或15s lease。写入继续使用原run ID/fencing token并校验有效lease。父context已取消则不再新建脱离停机的后台写入；写入超时、失去lease或commit结果未知时交给既有reconciler，不能断言已failed或覆盖可能已提交的成功。只有DB确认成功才推进freshness。不得使用无界Background/WithoutCancel收尾。

慢响应、慢body、成功finalize阻塞均需真实PG与合成HTTP专项证明：5s截止生效；失败收尾可写且有界；第一次timeout按预算retry_wait，第二次timeout终结failed；shutdown/失lease/unknown commit保持既有恢复规则。网络请求5s截止与失败收尾耗时必须分开断言，不能把10s当作attempt预算。

#### 未配置Gateway不进入调度

reader_secret_ref为NULL的Gateway属于not configured：不resolve、不发网络请求、不创建新run，不影响其他Gateway。共同ScheduleCurrent路径必须在Gateway行锁内检查当前reference并以NULL返回明确no-work；不能仅在列表预过滤，因为RunGatewayOnce也会调用ScheduleCurrent。可复用现有no-work返回，不新增持久状态或配置开关。

只有NULL表示未接入；非NULL引用映射缺失、文件不可读或token错误仍创建正常run并按既有Secret/auth失败分类持久记录，不能跳过以隐藏故障。历史遗留NULL且已有run不得删除或修复历史、不得用not configured掩盖在途run；由既有reconciler按原恢复规则处理，首次补填继续拒绝历史存在的记录。

补填与调度使用同一Gateway行锁：调度先看到NULL则无run退出，补填仍可成功；补填先提交则调度读取非NULL并正常创建run。原来“scheduler先完成则run存在”的描述仅适用于已经配置的Gateway。真实PG并发验收必须覆盖两个顺序。单Gateway失败完成分类/收尾后继续处理其他Gateway；单项操作error也不能终止整个有序列表，父context取消或共享DB不可用才结束本轮并下次重试，不能形成无界重试。

### 2. Transport：直接HTTP/HTTPS，不校验目标许可或HTTPS证书

按用户明确决定，Directory客户端直接使用登记的HTTP或HTTPS endpoint，不设origin白名单、CIDR/DNS许可列表、HTTP opt-in或证书验证开关。HTTPS专用transport使用`InsecureSkipVerify: true`，不验证证书链、有效期或主机名，不添加替代VerifyConnection/VerifyPeerCertificate校验；保留既有TLS最低版本。此设置覆盖Control的Gateway/Node管理出站客户端，包括CLIProxyAPI Driver；使用各客户端专用transport，不修改全局DefaultTransport。

“不需要校验”在本change指目标许可和HTTPS服务端证书校验。仍须解析可用于构造固定GET请求的HTTP/HTTPS URL；无法解析或不支持的协议正常返回配置/请求错误。既有资产登记URL契约、reader token认证、Directory响应schema/身份验证、超时、body/account上限均不变。两个scheme仍拒绝redirect，TLS握手失败不自动切换HTTP。不新增transport配置或通用factory。

本地直接使用已登记的`http://gateway:8080`；Gateway内部8080不映射宿主端口，public proxy仍拒绝/internal/v1/。Control仅取得专用reader token与映射，不读取gateway.env、Gateway DB或Node管理密钥。token每轮读取，同一reference轮换不用改库。

HTTP明文传输token与响应；HTTPS虽加密但不认证服务端，错误证书或中间人不再被证书校验阻止。部署方承担目标与网络可信性，不将该链路描述为已验证服务端身份。此决定替换此前HTTPS-only/HTTP白名单草案，覆盖Node管理客户端，不扩展到Control入站或数据面。

### 3. 资产：只补填缺失的reader reference

HTTP直接可用，当前登记的endpoint无需更改。新增受控函数概念名`control_set_gateway_directory_reader_initial_v1(gateway_id uuid, reader_reference text, actor_admin_id uuid)`，只允许reader_secret_ref从NULL变为合法非空opaque reference；不接受endpoint/name/environment参数，不支持替换已有reference。凭据轮换在原reference指向的文件内完成。actor需为有效启用super_admin，函数仅供受信registrar，无网络副作用。

| 当前reference | 请求 | 结果 |
| --- | --- | --- |
| NULL且无任何Directory/Binding历史 | 合法非空reference | 原子填入并审计 |
| 与请求相同 | 重放 | no-op，不重复审计；即使已有历史也可重放 |
| 非NULL且不同 | 任意请求 | 冲突，绝不替换 |
| NULL但已有任一run/observation/snapshot/current state或Binding历史 | 补填 | 拒绝，不删除历史绕过 |

锁定Gateway行后检查当前值和全部历史，CAS为`reader_secret_ref IS NULL`；复用原ScheduleCurrent中同一Gateway的FOR UPDATE作为首次调度互斥点。scheduler先看到NULL时不得创建run，补填仍可成功；补填先完成则后续调度/attempt读取新reference。由于不换endpoint、且NULL旧reference不能发出授权请求，不再引入Directory/Binding全表写锁。真实PG必须验证两次补填与首次调度竞争、已存在失败/在途run拒绝、绑定历史保护；不能用只在新函数生效的advisory lock代替共同锁。

既有register signature/body保持不变，补填后用旧NULL输入重放register仍冲突。部署使用原endpoint启用采集，不增加transport许可检查；不合格endpoint需另行受控设计，本change不修改它。

成功UPDATE与既有audit_logs INSERT同事务。固定action为`asset.gateway_directory_reader_configured`；actor_admin_id记录操作者，target_admin_id=NULL，Gateway UUID放在details.gateway_instance_id，details只允许该UUID和reader_configured=true；request_id使用本次操作UUID，occurred_at为DB UTC时间。无endpoint/reference/token/哈希，失败整体回滚。安全definer/ACL和防伪约束不能因精简省略。

### 4. Migration 与兼容性

预期一个最小 forward migration（序号实施时确认，当前后继为00018）：添加SECURITY DEFINER函数、migrator owner、search_path=pg_catalog、全限定表名、PUBLIC revoke、仅registrar EXECUTE，以及同事务固定audit写入需要的最小allowlist/函数变更。仅使用既有Gateway行级互斥，不新增table/column/index或复制Directory/Binding truth，不扩展runtime UPDATE权限。需要超出该范围时先回到设计评审。

Up在同一migration事务内保留当前所有category/action，向`audit_logs_category_valid`追加`asset`，向`audit_logs_action_valid`追加`asset.gateway_directory_reader_configured`，并添加该category/action双向绑定的精确shape约束（result=success、非空有效actor、target_admin_id=NULL及上述固定details）。原有category/action和shape约束不能收窄或绕过。固定audit写入仅在获授权的补全函数事务内发生，内部audit helper不得向PUBLIC/runtime/registrar授予独立EXECUTE，registrar不新增直接INSERT；沿用既有history audit防伪模式保护新action，实际实现必须验证具有既有audit INSERT能力的runtime也不能伪造该action。

Down撤销新增补全函数/入口授权，但有意保留扩展后的category/action CHECK、精确shape及防伪保护，不尝试恢复旧枚举，即使当前零行也采取同一策略；这是保留历史兼容的逻辑回滚，不宣称schema逐字回到Up前。写入helper若为存续防伪保护所需则保留且无外部EXECUTE；不能留下引用已删除函数的trigger。Up需能在该逻辑Down后再次执行，约束安装应识别本change保留对象。不得删除新action历史行来允许回滚。正常应用rollback关闭Directory runtime并保留forward migration，不自动降级配置或删除数据。旧register重放必须使用当前存储配置，旧NULL reference输入与补全后配置不相同应继续冲突，部署文档明确这一运维兼容影响。

OpenAPI/生成HTTP客户端和TS Account ID不变；仅实际sqlc query变化时生成。source v1始终numeric JSON→Go int64；Control既有read/write为decimal string。Binding仅作为系统验收消费者，不修改事务、审计、CSRF、reauth或页面职责。

### 5. 发布步骤和验收边界

先批准设计，再实施/验证，不能把本轮planning验证视为实现通过。部署时先禁用Directory runtime，安装forward migration及reader凭据；确认历史为空后通过registrar补全；开启Gateway Directory验证所选HTTP/HTTPS传输及管理ACL；再启用Control并等待真实定时成功。用既有candidate读取精确Account ID，管理员显式bind，读取BOUND/resolved/current。不能从name/base_url猜identity，不能以手工finalize或owner读取替代runtime。

验证期间保持Gateway→Node请求可用；Directory关闭/认证错误后Binding仍保留，540s后resolution按既有规则unknown；恢复后重新resolved，不自动修改binding。使用合成隔离fixture覆盖账号精度边界，真实本地凭据和账号不得进入测试或仓库证据。

## Risks / Trade-offs

- 初始补全有意不解决已经产生失败run的旧配置修复；该环境需另行受控变更设计，不删run解除限制。当前调查环境满足零历史前置条件，实施前必须重新检查。
- HTTP明文传输；HTTPS不验证服务端证书。该安全边界由用户明确选择，覆盖Gateway/Node管理出站；TLS握手失败仍不触发HTTP降级。
- 新补全操作涉及ACL、并发和audit，必须通过真实PG验收；function-only外观不等于零风险。
- 5s轮询与5s attempt需使用已有请求context预算，shutdown、慢数据库和多个Control进程是必测边界。
- 本契约已通过Architecture Contract Re-review（2026-09-07，P1=0、P2=0）；不追认原Directory runtime为已部署。最终证据需从实际进程与所选HTTP/HTTPS source捕获。

## Expanded management outbound boundary

用户已明确将传输决定扩大到Control所有Gateway/Node管理出站调用。当前覆盖Gateway Directory、CLIProxyAPI `/healthz`、`/v0/management/auth-files`及从响应头读取版本元数据；不新增版本endpoint。未来管理客户端必须遵循此契约，但新增接口能力仍需独立批准。Control入站HTTPS/登录、Gateway→Node及上游模型连接不在范围内。

Node移除DNS/management CIDR/plain-HTTP CIDR检查、特殊IP拒绝以及DNS结果/实际拨号IP安全检查，使用普通DNS与拨号行为。loopback、link-local及元数据地址不再因地址类别被拒绝；不宣称保留原SSRF/DNS重绑定防护。仅在本地合成fixture验证，不请求真实云元数据服务。两类客户端HTTPS均不验证证书链、有效期或hostname；保留TLS握手、固定路径、禁止redirect、现有无代理策略、Secret保护、请求预算和响应验证。使用现有专用transport，不新增全局通用HTTP客户端。

Node旧配置`CONTROL_CLIPROXYAPI_MANAGEMENT_DNS`、`CONTROL_CLIPROXYAPI_MANAGEMENT_CIDRS`、`CONTROL_CLIPROXYAPI_PLAIN_HTTP_CIDRS`和`CONTROL_CLIPROXYAPI_CA_FILE`退役：新版本忽略这些变量且不读取CA文件；缺失、残留或非法旧值不阻止启动，也不恢复旧策略。Runbook明确其已无保护作用，部署模板移除这些变量。其余Node启用、Secret与预算配置保持不变。Directory仍只新增enabled与mapping两项。

这是现有Node传输安全行为的不兼容变更，不提供双轨开关。回滚旧Control前恢复旧DNS/CIDR/CA配置及符合旧证书策略的目标，否则停止受影响采集；不能假称旧二进制兼容新部署。只修改Control客户端和相关ops文档/模板，不改Node/Gateway服务端、入站TLS或数据面。同步canonical时更新Node Purpose中原SSRF保证的陈述，不能保留已取消的保证；本轮不直接修改canonical或archive。

## Implementation simplification

本次精简属于当前change，不新增能力或独立change。调度在每个Gateway实际轮到时使用DB当前槽，先reconcile再work；不预先创建整个列表的run，不回填等待期间错过的槽。180s槽、540s freshness、retry deadline与每个attempt预算不变；5s是循环唤醒频率，不保证整个列表每5s完成。保持串行处理和取消语义，不新增队列、并发池或第二套调度器。用户接受的验收组织：既有数据库继续只允许单Gateway。A/B及慢/失败前项的有序遍历采用调用产品遍历函数的合成fixture，证明后项继续及父取消生效；NULL no-work、失败持久化、补填竞争、当前DB槽、同槽去重、超时与恢复分别由真实单Gateway PostgreSQL/进程fixture证明。两层证据联合覆盖，不宣称同库支持多个Gateway；不为测试删除singleton约束或新增多Gateway数据模型。不能用一次全局5s context截断后续项。

Node直接复用标准net.Dialer和现有专用http.Transport，删除已无用途的目标策略、DNS筛选/IP复核与CA加载实现；保留URL结构解析、固定路径及错误脱敏。仅保留现有测试实际需要的窄拨号注入点，不新增通用transport factory、共享客户端框架或空壳策略层。Directory与Node分别修改原transport即可。

首次补填沿用deploy/asset-registry现有SQL模板与README惯例，只增加受控函数调用模板，不新增CLI程序、HTTP API或页面。仍保持受保护输入、明确actor、ACL、原子audit和幂等契约。

复用已有transport与Directory PostgreSQL fixtures；同一fixture/命令可支持多个验收ID，逐项保留独立断言及结果引用。HTTP/HTTPS可参数化共用测试流程，不重复搭建整套环境；不能省略两种协议、异常证书、超时、并发、恢复及精度验收。项目要求的make test build和相关真实PG/进程验收保留。

部署配置、Node旧变量退役、补填步骤、回滚和运行限制统一在一次Control/ops Runbook更新中交付；各任务引用同一资料，不复制多份证据。

## Runtime target read access correction

真实runtime-role验收发现gateway_instances只授予reader_secret_configured等安全列的SELECT，并未授予reader_secret_ref；此前GetGatewayDirectoryReadTarget直接SELECT不能运行。用户批准在同一个最小additive migration中增加control_query_gateway_directory_target_v1(target_run_id uuid, target_gateway_id uuid, target_fencing_token uuid)。SECURITY DEFINER、STABLE、固定pg_catalog search_path、全限定表名、owner=migrator、撤销PUBLIC/registrar EXECUTE，仅runtime执行。仅匹配Gateway且run为running、fencing有效、lease在statement_timestamp时未过期、reference非NULL时返回instance_id/management_endpoint/opaque reader_secret_ref，否则零行。返回值仅进入SecretResolver，不进入API、日志或指标。

不增加reader_secret_ref直接SELECT，不使用owner连接或原始SQL绕过。共同调度在Gateway行锁中使用已有可读generated reader_secret_configured作为NULL判断的等价条件。Down删除新增target read function；原补填函数及audit历史兼容回滚契约保持。新增真实PG断言：有效lease能读取；错误Gateway/token、过期/terminal run无结果；runtime直接SELECT reference、PUBLIC/registrar调用函数仍拒绝。

## Accepted acceptance organization

用户已接受保留单Gateway模型并将A/B遍历用合成测试验证。R5–R7中的A/B表示有序工作项的测试输入，不是同一数据库可登记两个Gateway的产品能力。真实PG继续验证NULL、非NULL凭据失败、当前槽/不回填、并发与恢复；调用产品私有遍历函数的合成测试验证前项no-work、durable failure、error或缓慢完成时后项继续，以及父取消时停止。只允许为复用该遍历添加私有函数接缝，不新增公开API、配置、调度器或持久状态。
