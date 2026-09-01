## 1. 契约、时间与安全边界

- [x] 1.1 对照系统设计v1.0第12、13.4、16、21、23～25节、ADR-0001和现有poll/snapshot/lifecycle/query主规格，制作字段、状态、保留期、覆盖率、失败分类与明确非目标对照表并通过review
- [x] 1.2 固化账号snapshot按UTC observed日、Provider coverage/abandoned按UTC scheduled日、72小时day-end资格及跨日source fail-closed规则，以DST/主机非UTC/午夜跨界表驱动测试验证
- [x] 1.3 固化compaction/rollup状态转换、lease/fencing、未知commit结果、failed_from恢复和completed不可变矩阵，以状态机模型测试覆盖每条合法/非法边
- [x] 1.4 定义账号/Provider segment、final rollup、source/segment checksum的版本化字段编码与稳定tie-breaker，使用golden vectors验证重复计算一致
- [x] 1.5 建立敏感数据流图，证明新summary/rollup只复制受保护account key而不复制email，checksum/identity不进入日志、指标、history审计details、错误或artifact
- [x] 1.6 明确OpenAPI/UI/alerts/HMAC/current-lifecycle-cleanup零变更边界，以OpenAPI diff、route/DOM负向测试和OpenSpec strict验证未扩展产品范围

## 2. Additive Migration 9、Schema 与最小权限

- [x] 2.1 新增下一号forward Goose Migration并验证既有PostgreSQL core `sha256(bytea)`能力，以PostgreSQL18空库up/down/up和function compatibility测试通过且不引入extension
- [x] 2.2 创建账号/Provider策略分段summary表及唯一键、字段allowlist、UTC/coverage/check约束、索引和immutable triggers，以非法组合/重复键/UPDATE/TRUNCATE/普通DELETE拒绝及合法retention DELETE测试验证
- [x] 2.3 创建账号/Provider最终daily rollup表及唯一键、固化阈值、completed-only读取索引和immutable triggers，以partial/complete/重复Provider/非法覆盖、普通DELETE拒绝及合法retention DELETE测试验证
- [x] 2.4 创建compaction/rollup run表及status、failed_from、lease/fencing、attempt、source/deleted count、checksum、阶段时间约束，并创建不可变的day-level retired cutoff表，以状态组合、过期lease、marker伪造/晚到poll拒绝测试验证
- [x] 2.5 为Provider current state增加最近finalized health时间、degraded和固定原因字段，并从仍存在的current Provider result安全回填，以Migration前后current产品字段逐项一致测试验证
- [x] 2.6 调整snapshot/provider-result/duplicate/terminal-poll及history summary/rollup/run保护trigger与外键，为snapshot batch、poll cascade、history row和run retention设置互异精确transaction-local gate，以普通DML拒绝和各合法路径矩阵验证
- [x] 2.7 创建固定签名、schema-qualified、固定search_path/UTC的planner/claim/summarize/delete/rollup/retention/metrics `SECURITY DEFINER`函数，撤销PUBLIC并仅授runtime EXECUTE，以ACL catalog和绕过测试验证
- [x] 2.8 验证Migration不生成summary/run/retired marker、不删除或改写poll/snapshot/promotion/lifecycle/audit、不复制身份，只新增并回填允许的Provider健康字段；以Migration8旧列值/行数fingerprint保持和新增列来源等价测试通过
- [x] 2.9 实现受保护down，仅允许无history row、无删除计数和无后续依赖的隔离新环境；以空环境成功、已有summary/run/delete进度环境拒绝且数据保持测试验证
- [x] 2.10 在至少完成一次snapshot/poll清理且current FK已NULL、history summary/run已存在的Migration9 schema运行固定旧二进制，验证启动、poll/promotion/current query和停止正常、不修改history表且不要求generic job kind
- [x] 2.11 扩展audit category/action/details allowlist，允许actor-null history summarized/completed/failed与retention事件并保持180天不可变边界，以未知detail/identity/checksum拒绝和事务原子性测试验证

> 第三十六批扩展既有Migration8→9 health fixture：poll run、snapshot item、Provider result（含promotion）、current account/lifecycle、scope audit和audit log均以稳定主键排序后锁定旧列行数与SHA-256 digest；Provider state投影只排除M9新增的三个health列。Migration后要求全部count/digest精确不变，七类投影均含真实sentinel行；新health时间、degraded与reason则必须精确等于current poll及对应Provider result。runner仅在该exact test通过后输出`account_inventory_history_migration8_fingerprint=success old_columns=count_digest_covered provider_health=current_poll_result`。
>
> 第四十批新增`TestAccountInventoryHistoryAggregateSchemaConstraintAndProtectionGateMatrix`并纳入exact discovery：对四张summary/rollup表逐一验证重复业务键23505、非法字段组合CHECK拒绝、UPDATE/TRUNCATE/普通DELETE被immutable gate拒绝而合法retention路径通过；compaction/rollup run状态机与lease/fencing、retired cutoff不可变和晚到同日poll拒绝复用既有矩阵；函数/trigger catalog与ACL固定。Migration9同时为poll run和Provider result补齐TRUNCATE guard，并把account key前缀校验从LIKE改为定长`left(...)=provider||':'`避免通配符语义。真实PostgreSQL18 runner输出`aggregate_schema_constraints_protection_gates=covered`及`account_inventory_history_migration8_fingerprint=success old_columns=count_digest_covered provider_health=current_poll_result`，据此完成2.2/2.3/2.4/2.6/2.7/2.8。

## 3. sqlc、Planner 与策略分段摘要

- [x] 3.1 新增history sqlc查询与Store DTO/adapter，只调用受控函数并让全部DTO formatter脱敏，以生成代码、compile和fmt canary测试验证
- [x] 3.2 实现eligible UTC day发现和预期compaction key幂等创建：正常扫描限定30天horizon，旧日仅以仍存source poll或既有compaction lineage bootstrap，durable retired-day cutoff永久排除；覆盖72小时边界、Migration8超过30天旧poll首次收敛且删后不复活、未来日期、同版本多段、日内策略切换和无交集不创建
- [x] 3.3 实现Node monitoring与Provider policy半开交集的五分钟slot planner，覆盖00:02非对齐起点、边界关闭、暂停/重入和多Provider active集合
- [x] 3.4 实现零数据segment规划，仅在激活交集包含至少一个scheduled slot但无poll时生成expected>0/promotion=0；以无slot短区间不创建、漏槽和纯abandoned日期测试验证
- [x] 3.5 实现账号segment聚合，覆盖首末scheduled/observed、last basic status、sample/status counts、first/last cumulative counters和reset counts，以正常/乱序/计数下降golden测试验证
- [x] 3.6 实现Provider segment聚合，覆盖transport/contract/snapshot/promotion/skipped/abandoned/degraded、首末promotion、expected和95% coverage，以Provider独立降级和policy_changed测试验证
- [x] 3.7 实现版本1链式checksum：`H0=zero32`、`R_i=sha256(length-prefixed canonical row)`、`H_i=sha256(H_(i-1)||R_i)`，以空/单/多行golden、读取计划一致、字段变化和百万行max RSS观测验证
- [x] 3.8 将两类segment、source counts/checksum和run summarized置于同一事务，以每个写入点故障注入证明全有或全无
- [x] 3.9 对summarized/completed摘要实施不可覆盖门禁，验证重复scheduler/worker和残余源行重试不能INSERT/UPDATE/TRUNCATE或普通DELETE，仅满足30天与依赖条件的固定retention函数可删

> 第八批对账号segment INSERT、Provider segment INSERT、source counts/checksum/status UPDATE和summarized audit INSERT逐点注入失败，均证明run原样且segment/audit零残留，并在解除trigger后成功重试，完成3.8。
> 同fence残余source重放不增写segment/run/audit，重复planner不建新run；summarized/completed两状态下两类segment的非受控INSERT/UPDATE/DELETE/TRUNCATE均被拒绝，且既有retention批次仅通过精确gate删除，完成3.9。
> 第九批以生产planner PostgreSQL18行为矩阵覆盖正常horizon、旧日无证据排除、既有lineage补齐、真正未来日、同版本多段去重、日内策略切换、完全无交集和重复调用幂等；结合既有Migration8旧poll bootstrap及retired cutoff删除窗口零复活证据，完成3.2。

## 4. Compaction Scheduler、Worker 与断点续删

- [x] 4.1 实现compaction claim/renew/reclaim，使用数据库时间、`FOR UPDATE SKIP LOCKED`、有界lease和随机fencing，以双Worker及旧token影响零行测试验证
- [x] 4.2 实现`pending|failed_from=pending` summarize执行与固定错误映射，以statement timeout、连接断开、commit结果未知和重启测试验证安全重做/识别已提交
- [x] 4.3 实现summarized到deleting转换和稳定主键有界snapshot选择，以batch 1/边界上限/空批次/并发插入不可发生测试验证
- [x] 4.4 实现单批`DELETE ... RETURNING`与actual deleted count同事务累计，以删除失败、计数更新失败、提交前/后崩溃测试验证守恒
- [x] 4.5 实现从summarized/deleting及对应failed_from只续删、不重聚合，以部分删除后篡改残余fixture仍不能缩小summary测试验证
- [x] 4.6 实现completed gate，只有remaining=0且source snapshot count等于deleted count/checksum身份不变才完成，以不一致进入fixed failed且poll仍保留测试验证
- [x] 4.7 实现过期lease Reconciler和优雅停止，覆盖Control/PostgreSQL在pending/summarized/deleting各阶段重启且最终单份完成

> 第十批以两个独立runtime事务证明首个compaction run持锁时第二Worker通过`SKIP LOCKED`领取下一run，并验证数据库时间生成的有界lease、唯一随机fence和attempt；过期run经Reconciler reclaim后生成新fence，旧fence续租影响零行且run快照不变，完成4.1。
> 第十一批在真实summarized run分别注入snapshot DELETE与deleted count UPDATE失败，均证明run、source和audit全量回滚；提交前终止backend同样回滚，明确commit后再终止backend则actual DELETE、持久计数和source守恒保持，完成4.4。
> 第十二批以真实PostgreSQL18函数证明snapshot删除按稳定复合主键选择，拒绝5001上限、接受batch 1与5000并在空批次保持零删除；终态来源poll的晚到snapshot插入被既有不可变门禁拒绝，完成4.3。
> 第十三批以真实PostgreSQL18恢复链证明summarized和deleting失败均从持久阶段续删并更换fence；首批删除后即使测试夹具篡改残余snapshot，固化summary、source checksum和summarized audit仍不变，完成4.5。
> 第十四批以真实PostgreSQL18完成门禁分别隔离remaining非零与source/deleted计数不守恒，均固定进入`failed_from=deleting`且不可重新claim；即使snapshot已空，失败run仍阻止到期poll清理，结合既有checksum mismatch与成功完成证据完成4.6。
> 第十五批收窄claim使过期active lease只能先由Reconciler固定为`failed/lease_expired`，真实PostgreSQL18三阶段矩阵证明reconcile前Worker不能绕过、`failed_from`精确、旧fence零影响且新fence从原阶段续做后每个run/segment/audit仅一份；真实Control进程把pending/summarized/deleting三个20秒claim组合经过一次Control重启和PostgreSQL停启，在lease过期后全部以attempt 2收敛为单份completed compaction/final rollup。既有runtime测试证明停止新claim后仅排空当前有界事务且不启动下一删除批；持锁事务SIGTERM drain/timeout和真实pool exhaustion仍属于7.4，不据此提前勾选。
> 第十六批在真实PostgreSQL18 summarize写入链注入数据库statement timeout、提交前backend断连和提交后连接断开，分别证明全量回滚后安全重做、已提交摘要保持唯一且同fence重读不重算；runtime矩阵锁定`pending|failed_from=pending`均执行summarize、timeout/database unavailable固定失败原因，以及commit unknown不误写失败。结合第十五批真实Control/PostgreSQL重启从pending或summarized持久阶段恢复，完成4.2。

## 5. 最终日级 Rollup 与 Coverage 发布边界

- [x] 5.1 实现daily rollup expected segment枚举和幂等run创建，缺少/pending/deleting/failed segment时保持未发布，以最后segment并发完成竞态测试验证
- [x] 5.2 实现account final rollup的计数求和、首末时间、按latest last_scheduled_at/固定唯一键选择末值，以及`sum(segment resets)+segment边界下降`规则，以日内策略切换golden测试验证
- [x] 5.3 实现Provider final rollup逐项求和并按总applied/总expected重算ratio，禁止平均segment ratio，以部分时段active和重新加入测试验证
- [x] 5.4 固化9500 basis-point阈值及`complete|partial`，覆盖0/0无虚假记录、94.99%、95%、100%边界；当前partial只以`complete=false`健康指标发布，不提供趋势reader，并固化未来正常趋势reader的completed+complete-only边界
- [x] 5.5 将两类final rows、segment count/checksum和rollup completed同事务提交，以每个写入点/commit故障注入证明无部分发布
- [x] 5.6 实施completed final rollup不可覆盖且只读指标只消费completed结果，以重复Worker、segment篡改尝试和未完成日负向测试验证

> 第三批已有部分证据：daily-rollup planner/claim/renew/reconcile/finalize/fail、两类final rows与run/audit同事务、全字段segment checksum PostgreSQL↔Go一致、同fence幂等finalize、unknown-commit有界重放及纯聚合边界均已验证。第七批以PostgreSQL锁屏障证明最后segment完成期间不发布，并在完成后验证双planner只创建唯一run；同时覆盖missing/pending/deleting/failed不发布，完成5.1。Go边界测试与PG18 truth table锁定0/0、94.99%、95%、100%，catalog检查证明生产finalizer使用同一`applied*10000 >= expected*9500`算式，并以可持久的18/19、19/20、7/7经真实finalize验证partial/complete发布；生产API路由无趋势入口、runtime无history表权限，唯一metrics函数只读completed rollup并将partial发布为`complete=false`，完成5.4。第八批对两类final INSERT、run proof/completed UPDATE及deferred audit commit逐点注入失败，均证明无部分发布并在解除trigger后成功收敛，完成5.5。本批以同fence重复finalize证明final/run全量快照不变，segment篡改与final UPDATE/DELETE/TRUNCATE均被拒绝；即使pending/failed run下预先存在final-looking Provider row，metrics仍全部省略，只在completed后发布1 partial/2 complete，完成5.6。

## 6. Retention、级联清理与 Current Query 兼容

- [x] 6.1 固化snapshot day_end 72小时、poll scheduled_at 30天、summary/rollup day_end且completed_at均30天、completed run自身completed_at 30天资格，验证`<=`边界前后且运行配置无法缩短
- [x] 6.2 实现到期terminal poll候选扫描，要求所属compaction completed且snapshot为空，以未完成/failed/非终态/未到期/跨日错误key全部拒绝测试验证
- [x] 6.3 实现有界poll删除及Provider result/duplicate级联，验证删除顺序、实际行数、batch重启和任一子trigger失败整体回滚
- [x] 6.4 验证poll删除使Provider/account current FK `SET NULL`但来源时间/版本/提交、基础状态、lifecycle、missing计数和freshness逐字段保持
- [x] 6.5 升级`control_query_current_account_inventory_v1`从Provider current health读取degraded并接受合法空source FK，以清理前后HTTP/Store响应等价、current source早于最近health、旧result不覆盖health和非法缺字段503测试验证
- [x] 6.6 更新fenced finalize仅对已有state、仍属当前active策略、严格更新slot、非policy_changed/stale/out-of-scope的Provider刷新health，覆盖迟到旧槽/并发策略切换/从未promotion且不错误推进snapshot/lifecycle
- [x] 6.7 实现“poll先删、segment/final后删、completed rollup run与day-level retired cutoff同事务、completed compaction run最后删”的互异gate有界清理，以同为30天边界、依赖仍存在、compaction分批删除期间零复活和无retained rollup时coverage省略验证
- [x] 6.8 验证pending/summarized/deleting/failed、count/checksum异常及current lifecycle/audit/未来alerts无论年龄均不被本cleaner删除，并验证retired cutoff不可变、不会被cleaner删除且拒绝晚到同日poll
- [x] 6.9 并发运行retention、current query、promotion和Provider scope切换，验证稳定锁顺序、无死锁、query只见已提交current truth且promotion不被历史倒退

> 第四批已有部分证据：四个互异retention gate/受控函数、持久poll/Provider-result/duplicate删除计数、`limit=1`多批、audit失败原子回滚、current FK `SET NULL`与清理前后current-query JSON等价、segment/final→rollup run+retired cutoff→compaction run顺序、固定30天纯模型边界及retention runtime unknown-commit持久重扫均已验证。精确Migration8→9 fixture还验证source-backed超过30天旧poll可首次收敛、Migration本身不造run/marker、rollup删除原子写marker、compaction `limit=1`删除窗口与最终删除后均不复活、晚到poll/marker伪造/down拒绝；独立fixture验证zero-poll既有completed lineage跨过正常horizon后仍完成rollup。这些早期证据当时不用于提前勾选6.8/6.9；异常保护和四路并发分别由第二十四、第二十五批独立闭环。
> 第十七批以生产PostgreSQL18受控函数逐项验证poll `scheduled_at`、segment/final的UTC `day_end`与rollup `completed_at`双门禁，以及completed rollup/compaction run自身`completed_at`在固定30天边界两侧的删除与保留；结合既有生产planner catalog和UTC/DST行为矩阵对snapshot `day_end <= database_now-72h`的证明、纯模型精确等号边界，以及配置测试确认72小时/30天均无运行时缩短入口，完成6.1。
> 第十八批在同一真实PostgreSQL18候选扫描中设置唯一合法completed/无snapshot/到期terminal正例，并逐项构造summarized未完成run、generic failed run、`source_day_mismatch` failed key、同key非终态poll、仍有snapshot和未到30天poll；生产retention函数只删除正例，第二次扫描处理零项，所有负例poll及受保护snapshot保持，完成6.2。
> 第十九批以真实finalize生成两个各含一条Provider result和一条duplicate的到期terminal poll，分别在两类子表DELETE trigger注入失败，均证明poll/子证据、三项持久进度和audit全量回滚；解除故障后以`limit=1`两批逐次得到`processed=1/deleted=3`，每批剩余行、三项累计计数与audit同步递进，完成6.3。
> 第二十批在既有真实poll retention两批删除前后，对Provider/account current整行分别移除唯一允许变化的`current_poll_run_id`后执行JSON指纹等价比较，并独立确认两个FK均为NULL；结合原有完整current-query JSON等价，逐字段覆盖来源时间/版本/提交、基础状态、lifecycle、missing计数、Provider health与freshness，完成6.4。
> 第二十一批证明current source仍指向旧promotion时，更新槽的degraded Provider health独立推进，迟到旧result不能覆盖；真实poll retention前后生产Repository page完全等价，独立HTTP fixture在合法清空两个current-source FK前后status/body完全等价；缺失health及其他非法current形状均由Store fail closed，其中缺失health经真实Repository/Handler固定映射503且不泄露identity，完成6.5。
> 第二十二批把Provider result的stale门禁统一到已持久health watermark：真实fenced finalize先建立A槽current promotion，再以C槽失败结果只刷新degraded health，随后finalize满足A<B<C的B槽；B固定为`stale_poll`，health保留C，snapshot pointer、current account lifecycle与missing计数均不推进。并发finalize与策略scope切换只产生两种完整串行结果，scope先行时不刷新health；从未promotion的失败结果只保留poll/Provider历史，不创建Provider current、snapshot或lifecycle。该矩阵完成6.6；它本身不是第二十五批的同链四路并发证据。
> 第二十三批结合生产retention函数catalog与PostgreSQL18固定30天边界矩阵，锁定poll、segment/final、completed rollup run和completed compaction run的inclusive资格；同一主链在poll或四类row仍存在时拒绝后续阶段，清空最后一条retained completed Provider rollup后coverage从一条省略为零，rollup run删除与retired cutoff原子提交。精确Migration8→9链再以`limit=1`分批删除两个compaction run，并在中间与最终状态证明planner零复活，完成6.7。
> 第二十四批在真实PostgreSQL18候选矩阵中证明pending/summarized/deleting/failed及`source_day_mismatch`/`source_checksum_mismatch`无论年龄均不被poll cleaner选中，并复用既有`source_count_mismatch`完成门禁与保留poll证据。四个cleaner执行前后current account/lifecycle指纹不变，预置的两类audit主键仍各自存在、未被删除；四函数catalog的直接`DELETE`目标精确限于poll、四类segment/final rows和两类completed run，无动态SQL，且本change不引入alert schema/route，因此current、audit和未来alerts不在cleaner所有权内。已由生产链写入的retired cutoff对owner `UPDATE`/`DELETE`/`TRUNCATE`均固定拒绝`42501`，四个cleaner空扫后仍唯一存在；结合既有retention/poll双顺序锁竞态与晚到同日poll `23514`拒绝，完成6.8。该证据不声称8.3的audit 180天边界与全量错误/details allowlist已完成。
> 第二十五批先使生产poll cleaner在稳定锁定目标poll后，按`instance/provider`和`instance/provider/account_key`顺序显式预锁Provider current rows再预锁account current rows，与promotion及scope切换保持一致的Provider→account顺序。真实PostgreSQL18同一Node/Provider链以`retention_first`/`scope_first`双顺序并发运行到期current-source retention、Repository current query、更新槽fenced promotion和即时Provider scope切换；`pg_stat_activity`确认另外两条writer实际等待，未提交期间query在2秒内只返回此前A present/Z suspected-missing的完整current truth，先行事务提交后全员在有界context内成功且无timeout/deadlock。两种顺序均精确得到retention `processed=2/deleted=4`、poll/Provider-result进度`2/2`、旧poll归零、新poll/result各一、scope audit一条和两个完整out-of-scope账号；retention先行时新promotion applied且Provider/A/Z均指向新poll、health为新槽，scope先行时新result为`policy_changed`、三类pointer均为NULL且health保留旧poll2槽，完成6.9。这不关闭7.4的持锁SQL停止/pool矩阵、9.4的故障注入或9.6的全仓race门禁。

## 7. Runtime 配置、生命周期与兼容门禁

- [x] 7.1 新增默认关闭的history enabled、scan/lease/in-process concurrency/batch/timeout配置与安全上下限，72小时/30天/95%不开放配置；以缺失、边界和非法组合config测试验证
- [x] 7.2 实现history service启动compatibility gate，检查Migration9表/函数/ACL/core-sha256/provider-health/query签名；disabled也执行只读探测，不兼容时只禁用history并仅导出enabled/reason、既有服务继续，避免把不可读取的oldest/backlog伪装为零
- [x] 7.3 接线planner/compaction/rollup/retention循环及有界退避，证明不注册`async_job_kinds`、不依赖Redis且生产durable-job registry继续为空
- [x] 7.4 实现停止顺序和有限事务收尾，覆盖SIGTERM、lease未到期、连接耗尽与重启后Reconciler接管
- [x] 7.5 验证应用rollback：完成真实snapshot/poll删除并使current FK为NULL后停止history、运行固定旧二进制、保留forward schema/summary/run并继续poll/current query且不修改history，生产不执行down

> 第六批新增真实Control process验收：覆盖默认disabled compatibility、metrics权限故障隔离、合法zero-poll日enabled收敛、消失执行者claim跨PostgreSQL stop/start与lease过期后的持久恢复，以及实际PID SIGTERM有界退出；第十五批已将恢复扩展为20秒pending/summarized/deleting三阶段组合Control/PostgreSQL重启，并以数据库函数矩阵独立证明Reconciler独占接管。另有可选`CONTROL_DATABASE_MAX_CONNS=1..100`环境覆盖且缺失保持现有pgx URL/default行为，为真实pool exhaustion提供确定性注入。7.4仍缺持锁事务SIGTERM drain/timeout和真实pool exhaustion矩阵，因此保持未勾选。
> 第二十六批在真实Control与PostgreSQL18中将`CONTROL_DATABASE_MAX_CONNS=1`，以独立owner锁住Provider summary表并由`pg_stat_activity`确认唯一Control连接正阻塞于生产summarize函数；并发metrics请求固定在客户端期限内等待且Control保持存活。SIGTERM后，grace内释放锁只提交这一份summarized事务、不启动下一阶段；重启时未到期lease的owner/fence/attempt保持，过期后只能由Reconciler接管并以attempt 2完成。持续持锁分支由固定statement timeout原子回滚summary并只写`failed_from=pending/statement_timeout`，无segment、rollup或summarized/completed audit，进程同样有界正常退出。该矩阵完成7.4，但不替代9.4的完整双scheduler/worker、权限、trigger和数据库故障矩阵，也不关闭9.6全仓race。

## 8. 指标、隐私与安全负向门禁

- [x] 8.1 增加最近completed final Provider coverage ratio/complete指标，确保同日多策略只产生一组instance/provider序列且不含date/policy/run标签
- [x] 8.2 增加compaction/rollup固定state/result、oldest eligible unfinished、failure by failed_from、delete backlog/rows/duration指标，使用registry测试锁定低基数标签allowlist
- [x] 8.3 定义固定错误分类、结构化日志与actor-null history audit allowlist，验证每个summary/rollup/delete状态与审计同事务、180天边界且checksum/identity/SQL参数/raw error不进入日志或audit details
- [x] 8.4 向email/account key、poll/policy/run/fencing/checksum、endpoint、Secret和raw error注入唯一canary，扫描成功、零数据、partial、权限、超时、重启和清理失败的最终数据库非身份列/日志/指标/错误/artifact
- [x] 8.5 用runtime/migrator/未授权角色覆盖SET LOCAL伪造gate、savepoint rollback、函数异常、连接池复用、owner直接DML、嵌套函数/search_path、超大batch、未来日期和旧fencing，验证gate不泄漏且只有合法受控函数可产生预期变化
- [x] 8.6 使用fake network counters证明planner、summary、rollup、metrics、retention和全部错误路径对Node/Gateway/Prometheus/互联网/模型数据面请求均为零

> 第二十七批锁定history audit的12个固定phase：`summarize`、`snapshot_delete`、`complete`、`fail_pending`、`fail_summarized`、`fail_deleting`、`rollup_complete`、`rollup_fail_pending`及四个`retention_*`；每条只允许actor-null system事件和`instance/summary_date/phase/row_count`四个details键，action/result/phase/row-count错配、未知键、身份/checksum、非NULL actor/target/fingerprint/reason均被拒绝。生产函数catalog逐phase证明状态mutation、精确audit gate和audit INSERT位于同一函数/事务；既有summarize、rollup finalize、poll retention及rollup-run retention的audit失败注入代表性证明mutation整体回滚。位于数据库时钟180天前、等于及之后的三条合法history audit经过本change四个cleaner后全部保留，且owner `UPDATE/DELETE/TRUNCATE`仍固定拒绝`42501`。这只证明至少180天不可变保留和本change没有audit cleaner，不声称第181天自动删除。
>
> 同批将错误投影锁为两套各8项字典：compaction只接受source day/count/checksum、activation及`statement_timeout/lease_expired/database_unavailable/internal`；rollup只接受segment incomplete/count/checksum、activation及同四项运行时原因，跨字典原因在Store边界失败关闭。Control结构化顶层日志仅允许`service_stopped`、`runtime_stopped`、`shutdown_timed_out`三种reason，并精确省略raw error和`error`字段。由此完成2.11和8.3；数据库非身份列、日志、指标、错误与artifact的全路径唯一canary扫描仍属于1.5/8.4，不能据此提前关闭。
>
> 第二十八批把endpoint/IP、Secret引用/值、email、account key、response body/header、run/fence/checksum、raw error、SQL参数及poll/policy ID分别作为唯一canary。真实PostgreSQL18矩阵覆盖partial主链到rollup/retention，以及zero、permission、statement-timeout和backend reconnect；逐值只允许account key出现在受保护account segment/final identity、policy/run/fence/checksum出现在既有受保护identity/proof列，七张history表的其余非身份列、history audit details及Repository安全返回均零命中。独立local-sink gate对`success/zero_data/partial/permission/timeout/restart/cleanup_failure`七条路径各生成只含固定scenario/result的artifact，回读后扫描日志、指标、错误和最终artifact；aggregate `all`只有在local marker和database marker都成功后才输出`sensitive_canary=covered`。这完成1.5/8.4；源码direct-network-import为零和本次canary零泄漏均不等价于真实fake network counters，8.6仍保持开放。
>
> 第二十九批复用既有`account-inventory-history-fake-node`原子HTTP计数器，在宿主loopback为真实Control process矩阵提供同一个reachable Node endpoint与HTTP/HTTPS catch-all proxy。代理仅注入被测Control二进制；Go/Docker/make和验收probe继续清除六类proxy。默认disabled、zero-source planner→summary/delete→rollup、metrics provider权限失败、retention空扫、statement timeout、单连接pool exhaustion、Control/PostgreSQL restart、lease reconcile与shutdown完成后，计数器固定为`total/health/inventory/unauthorized/rejected=0`。该证据只标记`external_requests=partial_process_paths`：retention尚非source-backed删除，permission/cleanup及其余错误分支也未全部在同一counter下执行；待9.4 source-backed fault matrix扩展后再关闭8.6。
>
> 第三十批由`TestAccountInventoryHistorySecurityBoundaryMatrix`在隔离PostgreSQL18锁定安全边界：migrator伪造transaction-local gate及savepoint回滚、未授权asset registrar、运行时连接池复用、嵌套函数与`pg_temp` search path、超大planner/reconcile/delete batch、未来日期及compaction/rollup旧fencing均不能越权或泄漏gate，最终数据库指纹精确不变。migrator对run表的`INSERT`仅代表受信任forward migration/隔离fixture建模能力，不是生产运行时写路径；即使owner持有表所有权，直接`UPDATE/DELETE/TRUNCATE`仍固定拒绝`42501`，应用变化只能经允许的受控函数产生。PostgreSQL runner只有在该exact test通过后输出`security_boundary_matrix=covered`，据此完成8.5，不提前关闭8.6。

> 第三十二批把同一fake Node/catch-all原子计数器延伸到source-backed 9.4矩阵：每个fixture写入2条真实snapshot；两个eligible instance在同一Control、`CONCURRENCY=2`下由`pg_stat_activity`精确观察到2条生产summarize同时锁等待，释放后各一份compaction/rollup完成。生产保持单planner以避免重复调度，scheduler竞争由既有并发planner数据库幂等测试证明。旧fence在新fence/attempt下固定`P0002`且run/source/segment指纹不变；另一个超过30天fixture跨`MAX_CONNS=1` pool exhaustion与PostgreSQL stop/start后完成并由真实retention删除source poll。真实source statement timeout先原子固定`failed_from=pending/statement_timeout`且2条source保留，再由允许的retry claim完成；summarize `EXECUTE`撤销和`23514` statement trigger均在Control ready后触发固定`failed_from=pending/internal`、`runtime_stopped`和source保留，不声称自动恢复。planner、rollup finalize和poll retention的独立`EXECUTE`撤销均固定停止runtime；planner/retention保留原source与零history failure/success audit，rollup保留completed compaction proof/segments及原pending owner/fence/lease且零rollup-failure audit，不伪造数据库terminal fail。完整runner通过后counter固定`total/health/inventory/unauthorized/rejected=0`并输出`external_requests=0 node=0 gateway=0 prometheus=0 internet=0 model=0`，据此完成8.6。

## 9. PostgreSQL 18 恢复、并发与容量验收

- [x] 9.1 新增change-specific PostgreSQL18 acceptance runner，要求exact test discovery、固定安全failure mapping、禁止go/raw PostgreSQL日志直出、受保护mktemp与EXIT trap、唯一资源前缀、固定镜像digest/受限角色、清六类代理并严格清理container/volume/network/temp
- [x] 9.2 覆盖72小时边界、UTC/DST、日内策略切换、非对齐activation、暂停/重入、零数据、漏槽、abandoned、Provider独立降级和95% partial/complete矩阵
- [x] 9.3 在summarize事务前/中/commit未知、summarized后、每批delete前/后、complete前、final rollup事务中注入崩溃，验证恢复不重算/不多删/不部分发布
- [x] 9.4 注入stale fencing、同一Control内双scheduler/worker、statement timeout、PostgreSQL重启、pool exhaustion及repository/SQL权限/trigger故障，不执行宿主机disk-full，验证固定failed_from和源证据保留；可恢复故障最终收敛，权限/trigger故障terminal failed且不自动重试
- [x] 9.5 Permanent CI运行1/10/50 Node、总计1,000账号的短slot功能矩阵；nightly/manual对86.4万/115.2万snapshot运行至少5次summary/rollup与20个delete batch，按nearest-rank记录P50/P95/P99、DB/index/WAL/max RSS/buffers/lock wait/守恒，硬门禁仅为无OOM/timeout/deadlock和正确性
- [x] 9.6 运行全仓Go race覆盖planner/worker/reconciler/Store/metrics并并发current query/promotion/retention，确认无race、死锁或高基数series
- [x] 9.7 在Control与PostgreSQL同时停止窗口，对独立pinned官方CLIProxyAPI digest执行认证`/v1/models` baseline 1/1和outage 100/100，记录这是data-plane isolation而非Gateway inference E2E
- [x] 9.8 验证所有acceptance成功/失败路径最终container/volume/network/temp residual为零，报告仅保存固定计数、时延和状态且不含身份

> 第三十三批把`TestRepositoryLoopsUsesConfiguredConcurrency`和`TestRepositoryLoopsFatalMismatchStopsPlannerAndNewWorkerTransactions`加入static exact discovery及定向`go test -race`集合，锁定配置并发上限和fatal后停止planner/新worker事务。PostgreSQL18 runner另以`-race`精确执行`TestAccountInventoryHistoryConcurrentRetentionQueryPromotionAndScope`，覆盖同一lineage下retention、current query、promotion和scope transition的真实数据库并发。该最小门禁分别输出`history_targeted_race=covered`与`retention_query_promotion_scope_race=covered`；9.6保持开放，直到主线完成全仓`go test -race ./...`。

> 第四十一批执行主线全仓`go test -race ./... -count=1`：全部包通过，无race、死锁或高基数series（metrics低基数标签allowlist由既有collector与retention metrics测试在race下覆盖），结合第三十三批定向门禁完成9.6。

> 第三十五批统一七个history acceptance runner的成功报告为固定`cleanup_containers/volumes/networks/temp/lock=0`，shell contract逐个锁定EXIT/strict cleanup、Docker compose/volume/network和适用lock清理，并拒绝身份形状字段；全部invalid-arg路径只输出固定failure reason。opt-in失败门禁在隔离PostgreSQL与pinned数据面容器启动后注入migration失败，精确得到`migration_failed`且复核container/volume/network/temp/lock零残留；安全runner成功路径同时复核脱敏固定marker和零残留，完成9.8。

> 第七批以生产planner catalog门禁锁定`clock_timestamp()`、UTC归日和inclusive 72小时资格公式，并在PostgreSQL18行为矩阵覆盖非UTC/DST、策略切换、slot/零数据/abandoned/Provider独立及95%发布边界，完成9.2。
>
> 第三十批新增独立opt-in PostgreSQL18容量runner及manual/nightly workflow：生产规模只接受86.4万或115.2万snapshot，每档执行5个独立summary/rollup样本和至少20个delete batch，按nearest-rank输出P50/P95/P99，并记录DB/index/WAL、Go harness进程max RSS、PostgreSQL cgroup memory peak、buffers、lock wait、deadlock delta与逐批/最终守恒。`smoke`固定走同一生产函数链但只使用40行，marker明确为`not_evidence`。

> 第三十七批两次正式PostgreSQL18容量运行均`exit 0`。`864000×5`、3 Node、870 delete batches：summary P50/P95/P99=`47515/55590/55590ms`，rollup=`143/148/148ms`；DB/growth=`2415621823/2404081664`、index/growth=`1509728256/1508737024`、WAL=`6569937720`、harness RSS before/after=`20185088/24363008`、PostgreSQL peak=`3603832832`、blocks read/hit=`6190650/166804575`、temp=`2586302976`、max lock waiters/deadlock delta=`0/0`，source/deleted=`4320000/4320000`、account/Provider rollups=`15000/15`、conservation=`passed`。`1152000×5`、4 Node、1160 batches：summary=`61264/74343/74343ms`，rollup=`164/176/176ms`；DB/growth=`3207583423/3196035072`、index/growth=`2003296256/2002305024`、WAL=`8668940096`、RSS=`19972096/26427392`、PostgreSQL peak=`3892613120`、blocks=`6067993/219456540`、temp=`3336427520`、lock/deadlock=`0/0`，source/deleted=`5760000/5760000`、rollups=`20000/20`、conservation=`passed`。两次均无OOM/timeout/deadlock且container/volume/network/temp/lock残留为零；864000运行时success marker尚未追加temp/lock字段，但strict cleanup和独立残留检查均为零。以上时延和资源数仅为本次候选容量证据，不是生产SLO阈值，据此完成9.5。
>
> 第三十一批由`TestAccountInventoryHistoryCrashRecoveryMatrix`补齐集中崩溃矩阵，并与既有summarize pre-commit backend终止及pending/summarized/deleting真实重启证据共同覆盖全部指定窗口。真实commit-unknown使用同一连接执行`COMMIT`后阻塞并由短context只观察到传输错误，随后以持久run/segment/audit证明summary只提交一次且同fence重读不重算；三个snapshot按`batch=1`逐批先在事务内终止backend证明零变化，再制造commit-unknown证明每批最多删除一次、audit一次且恒有`deleted+remaining=source`。最后一批后在complete事务内终止backend，证明run仍为deleting、completed audit与rollup均为零；恢复后完成一次。非零account/Provider final rollup在事务内终止backend后保持pending且两类final/audit均为零，再以同fence完整收敛各一份。PostgreSQL runner只有在exact test通过后输出`crash_recovery_matrix=covered`；该矩阵只完成9.3，不替代9.4故障或9.5容量证据。
>
> 第三十四批复用既有pinned官方CLIProxyAPI digest与Bearer认证`/v1/models` probe：Migration9后先写入匹配Control配置的`history-data-plane`环境身份，Control、PostgreSQL与独立CLIProxyAPI同时在线时baseline固定通过1/1；随后有界停止Control并确认health不可达，再停止PostgreSQL并确认`pg_isready`失败，在两者同时停止窗口继续通过100/100。脱敏marker固定为`scope=data_plane_isolation gateway_inference_e2e=not_covered`并报告container/volume/network/temp/lock残留全零；这证明进程/数据库故障不影响独立数据面，不声称真实Gateway inference E2E，完成9.7。
>

## 10. Runbook、证据与最终门禁

- [x] 10.1 编写history compaction Runbook，覆盖UTC资格、状态解释、partial、积压/失败、WAL/lock、暂停恢复、checksum/count不一致、只增不删处置和禁止人工SQL重建
- [x] 10.2 固化rollout/rollback：Migration→新二进制disabled→compatibility gate→staging单Worker→逐步启用；回滚先停runner、保留forward schema且生产禁止down，并逐项dry run
- [x] 10.3 更新README/config reference和现有poll/snapshot/lifecycle/query Runbook，说明受控历史删除、合法空current source、Provider health冗余与当前产品字段兼容
- [x] 10.4 运行`make generate`两次确认OpenAPI/Go/TypeScript生成物零差异，并运行`make test`、`make build`、全部Go tests、full race、vet、前端typecheck/test/build和actionlint；新增独立PG18 history CI job、明确timeout并与其他container jobs使用唯一资源前缀隔离
- [ ] 10.5 运行Migration9 schema/ACL/rollback、旧二进制forward-schema、PostgreSQL18功能/故障/容量、零外部请求、敏感canary和数据面隔离完整验收，保存脱敏证据摘要
- [x] 10.6 运行本change strict、全部canonical specs strict、`git diff --check`和generated diff检查，对照proposal/design/spec/tasks与系统设计确认无漂移
- [x] 10.7 检查`git status --short`、Migration编号、临时容器/volume/network/目录和敏感内容扫描，确认worktree只包含本change计划/实现并整理Conventional Commits分层提交计划

> 第三十八批为10.2补齐最小可执行dry-run接线：真实process窗口在默认disabled且compatibility通过后，以`concurrency=1`、snapshot delete batch `1`处理独立2-snapshot合成UTC日，要求source/deleted精确守恒、compaction与final rollup各唯一completed、生产current query前后等价，并纳入同一零外部请求counter；随后既有`concurrency=2`双worker阶段构成逐步启用。rollback窗口在pinned旧Control前先启动当前候选且保持history disabled，要求固定`reason=disabled`、history/history-audit与generic-job指纹不变、有界停止后才允许启动旧二进制；shell contract锁定该顺序并禁止rollback runner调用任何Goose/migration down。seed/收敛检查新增生产current query等价断言，fixture同步注册`management_account_inventory_read` driver/node capability以避免fail-closed P0409。
>
> 第三十九批完成10.2两侧真实验收：process runner全窗口`exit 0`，输出`default_disabled=covered metrics_failure_isolation=covered enabled_zero_source=covered staging_concurrency_1=covered staging_delete_batch_1=covered staging_source_snapshots=2 staging_current_query_equivalent=covered history_concurrency=2 dual_workers_observed=2 ... fake_network_counter=covered external_requests=0 node=0 gateway=0 prometheus=0 internet=0 model=0`且七项cleanup残留为零；rollback runner全窗口`exit 0`，输出`current_candidate_disabled=covered compatibility_gate=covered runner_stopped_before_old_binary=covered pinned_old_revision=covered snapshot_cleanup=controlled poll_cleanup=controlled current_fk_null=covered old_control_poll_promotion=covered old_control_http_current_query=covered history_and_history_audit_unchanged=covered fake_node_inventory_requests=1 production_down=not_used`且cleanup残留为零，据此完成10.2。

> 第四十一批执行发布静态门禁与全仓race：连续两次`make generate`后工作树仅剩本change tasks.md改动（OpenAPI/Go/TypeScript生成物零差异）；`make test`全部通过（tools、全部Go包、前端13文件54测试、`tsc -b` typecheck）；`make build`（前端vite生产构建+`go build ./cmd/control`）与`go vet ./...`通过；actionlint v1.7.12对全部workflows零告警；主线全仓`go test -race ./... -count=1`全部包通过且无race/死锁/高基数series。ci.yml的独立`postgres_history` job以`timeout-minutes: 60`运行`account-inventory-history-run.sh all`，各history runner使用唯一项目名/锁目录与固定端口与其他container jobs隔离，nightly/manual容量workflow固定360分钟timeout与`fail-fast: false`，据此完成9.6与10.4。
