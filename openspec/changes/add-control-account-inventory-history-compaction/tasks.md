## 1. 契约、时间与安全边界

- [x] 1.1 对照系统设计v1.0第12、13.4、16、21、23～25节、ADR-0001和现有poll/snapshot/lifecycle/query主规格，制作字段、状态、保留期、覆盖率、失败分类与明确非目标对照表并通过review
- [x] 1.2 固化账号snapshot按UTC observed日、Provider coverage/abandoned按UTC scheduled日、72小时day-end资格及跨日source fail-closed规则，以DST/主机非UTC/午夜跨界表驱动测试验证
- [x] 1.3 固化compaction/rollup状态转换、lease/fencing、未知commit结果、failed_from恢复和completed不可变矩阵，以状态机模型测试覆盖每条合法/非法边
- [x] 1.4 定义账号/Provider segment、final rollup、source/segment checksum的版本化字段编码与稳定tie-breaker，使用golden vectors验证重复计算一致
- [ ] 1.5 建立敏感数据流图，证明新summary/rollup只复制受保护account key而不复制email，checksum/identity不进入日志、指标、history审计details、错误或artifact
- [x] 1.6 明确OpenAPI/UI/alerts/HMAC/current-lifecycle-cleanup零变更边界，以OpenAPI diff、route/DOM负向测试和OpenSpec strict验证未扩展产品范围

## 2. Additive Migration 9、Schema 与最小权限

- [x] 2.1 新增下一号forward Goose Migration并验证既有PostgreSQL core `sha256(bytea)`能力，以PostgreSQL18空库up/down/up和function compatibility测试通过且不引入extension
- [ ] 2.2 创建账号/Provider策略分段summary表及唯一键、字段allowlist、UTC/coverage/check约束、索引和immutable triggers，以非法组合/重复键/UPDATE/TRUNCATE/普通DELETE拒绝及合法retention DELETE测试验证
- [ ] 2.3 创建账号/Provider最终daily rollup表及唯一键、固化阈值、completed-only读取索引和immutable triggers，以partial/complete/重复Provider/非法覆盖、普通DELETE拒绝及合法retention DELETE测试验证
- [ ] 2.4 创建compaction/rollup run表及status、failed_from、lease/fencing、attempt、source/deleted count、checksum、阶段时间约束，并创建不可变的day-level retired cutoff表，以状态组合、过期lease、marker伪造/晚到poll拒绝测试验证
- [x] 2.5 为Provider current state增加最近finalized health时间、degraded和固定原因字段，并从仍存在的current Provider result安全回填，以Migration前后current产品字段逐项一致测试验证
- [ ] 2.6 调整snapshot/provider-result/duplicate/terminal-poll及history summary/rollup/run保护trigger与外键，为snapshot batch、poll cascade、history row和run retention设置互异精确transaction-local gate，以普通DML拒绝和各合法路径矩阵验证
- [ ] 2.7 创建固定签名、schema-qualified、固定search_path/UTC的planner/claim/summarize/delete/rollup/retention/metrics `SECURITY DEFINER`函数，撤销PUBLIC并仅授runtime EXECUTE，以ACL catalog和绕过测试验证
- [ ] 2.8 验证Migration不生成summary/run/retired marker、不删除或改写poll/snapshot/promotion/lifecycle/audit、不复制身份，只新增并回填允许的Provider健康字段；以Migration8旧列值/行数fingerprint保持和新增列来源等价测试通过
- [x] 2.9 实现受保护down，仅允许无history row、无删除计数和无后续依赖的隔离新环境；以空环境成功、已有summary/run/delete进度环境拒绝且数据保持测试验证
- [ ] 2.10 在至少完成一次snapshot/poll清理且current FK已NULL、history summary/run已存在的Migration9 schema运行固定旧二进制，验证启动、poll/promotion/current query和停止正常、不修改history表且不要求generic job kind
- [ ] 2.11 扩展audit category/action/details allowlist，允许actor-null history summarized/completed/failed与retention事件并保持180天不可变边界，以未知detail/identity/checksum拒绝和事务原子性测试验证

## 3. sqlc、Planner 与策略分段摘要

- [x] 3.1 新增history sqlc查询与Store DTO/adapter，只调用受控函数并让全部DTO formatter脱敏，以生成代码、compile和fmt canary测试验证
- [ ] 3.2 实现eligible UTC day发现和预期compaction key幂等创建：正常扫描限定30天horizon，旧日仅以仍存source poll或既有compaction lineage bootstrap，durable retired-day cutoff永久排除；覆盖72小时边界、Migration8超过30天旧poll首次收敛且删后不复活、未来日期、同版本多段、日内策略切换和无交集不创建
- [ ] 3.3 实现Node monitoring与Provider policy半开交集的五分钟slot planner，覆盖00:02非对齐起点、边界关闭、暂停/重入和多Provider active集合
- [ ] 3.4 实现零数据segment规划，仅在激活交集包含至少一个scheduled slot但无poll时生成expected>0/promotion=0；以无slot短区间不创建、漏槽和纯abandoned日期测试验证
- [x] 3.5 实现账号segment聚合，覆盖首末scheduled/observed、last basic status、sample/status counts、first/last cumulative counters和reset counts，以正常/乱序/计数下降golden测试验证
- [ ] 3.6 实现Provider segment聚合，覆盖transport/contract/snapshot/promotion/skipped/abandoned/degraded、首末promotion、expected和95% coverage，以Provider独立降级和policy_changed测试验证
- [x] 3.7 实现版本1链式checksum：`H0=zero32`、`R_i=sha256(length-prefixed canonical row)`、`H_i=sha256(H_(i-1)||R_i)`，以空/单/多行golden、读取计划一致、字段变化和百万行max RSS观测验证
- [ ] 3.8 将两类segment、source counts/checksum和run summarized置于同一事务，以每个写入点故障注入证明全有或全无
- [ ] 3.9 对summarized/completed摘要实施不可覆盖门禁，验证重复scheduler/worker和残余源行重试不能INSERT/UPDATE/TRUNCATE或普通DELETE，仅满足30天与依赖条件的固定retention函数可删

## 4. Compaction Scheduler、Worker 与断点续删

- [ ] 4.1 实现compaction claim/renew/reclaim，使用数据库时间、`FOR UPDATE SKIP LOCKED`、有界lease和随机fencing，以双Worker及旧token影响零行测试验证
- [ ] 4.2 实现`pending|failed_from=pending` summarize执行与固定错误映射，以statement timeout、连接断开、commit结果未知和重启测试验证安全重做/识别已提交
- [ ] 4.3 实现summarized到deleting转换和稳定主键有界snapshot选择，以batch 1/边界上限/空批次/并发插入不可发生测试验证
- [ ] 4.4 实现单批`DELETE ... RETURNING`与actual deleted count同事务累计，以删除失败、计数更新失败、提交前/后崩溃测试验证守恒
- [ ] 4.5 实现从summarized/deleting及对应failed_from只续删、不重聚合，以部分删除后篡改残余fixture仍不能缩小summary测试验证
- [ ] 4.6 实现completed gate，只有remaining=0且source snapshot count等于deleted count/checksum身份不变才完成，以不一致进入fixed failed且poll仍保留测试验证
- [ ] 4.7 实现过期lease Reconciler和优雅停止，覆盖Control/PostgreSQL在pending/summarized/deleting各阶段重启且最终单份完成

## 5. 最终日级 Rollup 与 Coverage 发布边界

- [ ] 5.1 实现daily rollup expected segment枚举和幂等run创建，缺少/pending/deleting/failed segment时保持未发布，以最后segment并发完成竞态测试验证
- [x] 5.2 实现account final rollup的计数求和、首末时间、按latest last_scheduled_at/固定唯一键选择末值，以及`sum(segment resets)+segment边界下降`规则，以日内策略切换golden测试验证
- [x] 5.3 实现Provider final rollup逐项求和并按总applied/总expected重算ratio，禁止平均segment ratio，以部分时段active和重新加入测试验证
- [ ] 5.4 固化9500 basis-point阈值及`complete|partial`，覆盖0/0无虚假记录、94.99%、95%、100%边界和partial不进入正常趋势读取
- [ ] 5.5 将两类final rows、segment count/checksum和rollup completed同事务提交，以每个写入点/commit故障注入证明无部分发布
- [ ] 5.6 实施completed final rollup不可覆盖且只读指标只消费completed结果，以重复Worker、segment篡改尝试和未完成日负向测试验证

> 第三批已有部分证据：daily-rollup planner/claim/renew/reconcile/finalize/fail、两类final rows与run/audit同事务、全字段segment checksum PostgreSQL↔Go一致、同fence幂等finalize、unknown-commit有界重放及纯聚合边界均已验证。5.1仍缺“最后segment并发完成”专项竞态；5.4仍缺完整发布/趋势读取矩阵；5.5仍缺每个写入点故障注入；5.6仍缺全部segment篡改和未完成日读取负向矩阵，因此本批次不提前勾选这些复合项。

## 6. Retention、级联清理与 Current Query 兼容

- [ ] 6.1 固化snapshot day_end 72小时、poll scheduled_at 30天、summary/rollup day_end且completed_at均30天、completed run自身completed_at 30天资格，验证`<=`边界前后且运行配置无法缩短
- [ ] 6.2 实现到期terminal poll候选扫描，要求所属compaction completed且snapshot为空，以未完成/failed/非终态/未到期/跨日错误key全部拒绝测试验证
- [ ] 6.3 实现有界poll删除及Provider result/duplicate级联，验证删除顺序、实际行数、batch重启和任一子trigger失败整体回滚
- [ ] 6.4 验证poll删除使Provider/account current FK `SET NULL`但来源时间/版本/提交、基础状态、lifecycle、missing计数和freshness逐字段保持
- [ ] 6.5 升级`control_query_current_account_inventory_v1`从Provider current health读取degraded并接受合法空source FK，以清理前后HTTP/Store响应等价、current source早于最近health、旧result不覆盖health和非法缺字段503测试验证
- [ ] 6.6 更新fenced finalize仅对已有state、仍属当前active策略、严格更新slot、非policy_changed/stale/out-of-scope的Provider刷新health，覆盖迟到旧槽/并发策略切换/从未promotion且不错误推进snapshot/lifecycle
- [ ] 6.7 实现“poll先删、segment/final后删、completed rollup run与day-level retired cutoff同事务、completed compaction run最后删”的互异gate有界清理，以同为30天边界、依赖仍存在、compaction分批删除期间零复活和无retained rollup时coverage省略验证
- [ ] 6.8 验证pending/summarized/deleting/failed、count/checksum异常及current lifecycle/audit/未来alerts无论年龄均不被本cleaner删除，并验证retired cutoff不可变、不会被cleaner删除且拒绝晚到同日poll
- [ ] 6.9 并发运行retention、current query、promotion和Provider scope切换，验证稳定锁顺序、无死锁、query只见已提交current truth且promotion不被历史倒退

> 第四批已有部分证据：四个互异retention gate/受控函数、持久poll/Provider-result/duplicate删除计数、`limit=1`多批、audit失败原子回滚、current FK `SET NULL`与清理前后current-query JSON等价、segment/final→rollup run+retired cutoff→compaction run顺序、固定30天纯模型边界及retention runtime unknown-commit持久重扫均已验证。精确Migration8→9 fixture还验证source-backed超过30天旧poll可首次收敛、Migration本身不造run/marker、rollup删除原子写marker、compaction `limit=1`删除窗口与最终删除后均不复活、晚到poll/marker伪造/down拒绝；独立fixture验证zero-poll既有completed lineage跨过正常horizon后仍完成rollup。6.1仍缺全部数据库资格边界矩阵；6.2仍缺cross-day等完整候选拒绝矩阵；6.3仍缺每一种子trigger故障；6.4/6.5仍缺所列current字段和旧health覆盖全矩阵；6.7仍缺同一30天数据库等号与coverage省略组合；6.8/6.9仍缺未来alerts及并发promotion/scope矩阵，因此不提前勾选复合项。

## 7. Runtime 配置、生命周期与兼容门禁

- [x] 7.1 新增默认关闭的history enabled、scan/lease/in-process concurrency/batch/timeout配置与安全上下限，72小时/30天/95%不开放配置；以缺失、边界和非法组合config测试验证
- [x] 7.2 实现history service启动compatibility gate，检查Migration9表/函数/ACL/core-sha256/provider-health/query签名；disabled也执行只读探测，不兼容时只禁用history并仅导出enabled/reason、既有服务继续，避免把不可读取的oldest/backlog伪装为零
- [x] 7.3 接线planner/compaction/rollup/retention循环及有界退避，证明不注册`async_job_kinds`、不依赖Redis且生产durable-job registry继续为空
- [ ] 7.4 实现停止顺序和有限事务收尾，覆盖SIGTERM、lease未到期、连接耗尽与重启后Reconciler接管
- [ ] 7.5 验证应用rollback：完成真实snapshot/poll删除并使current FK为NULL后停止history、运行固定旧二进制、保留forward schema/summary/run并继续poll/current query且不修改history，生产不执行down

> 第六批新增真实Control process验收：覆盖默认disabled compatibility、metrics权限故障隔离、合法zero-poll日enabled收敛、仍有效的10秒消失执行者claim跨PostgreSQL stop/start与lease过期后的持久恢复，以及实际PID SIGTERM有界退出；该恢复不独立区分Reconciler和claim函数。另新增可选`CONTROL_DATABASE_MAX_CONNS=1..100`环境覆盖且缺失保持现有pgx URL/default行为，为真实pool exhaustion提供确定性注入。7.4仍缺持锁事务SIGTERM drain/timeout、Reconciler独占接管证明和真实pool exhaustion矩阵，因此保持未勾选。

## 8. 指标、隐私与安全负向门禁

- [x] 8.1 增加最近completed final Provider coverage ratio/complete指标，确保同日多策略只产生一组instance/provider序列且不含date/policy/run标签
- [x] 8.2 增加compaction/rollup固定state/result、oldest eligible unfinished、failure by failed_from、delete backlog/rows/duration指标，使用registry测试锁定低基数标签allowlist
- [ ] 8.3 定义固定错误分类、结构化日志与actor-null history audit allowlist，验证每个summary/rollup/delete状态与审计同事务、180天边界且checksum/identity/SQL参数/raw error不进入日志或audit details
- [ ] 8.4 向email/account key、poll/policy/run/fencing/checksum、endpoint、Secret和raw error注入唯一canary，扫描成功、零数据、partial、权限、超时、重启和清理失败的最终数据库非身份列/日志/指标/错误/artifact
- [ ] 8.5 用runtime/migrator/未授权角色覆盖SET LOCAL伪造gate、savepoint rollback、函数异常、连接池复用、owner直接DML、嵌套函数/search_path、超大batch、未来日期和旧fencing，验证gate不泄漏且只有合法受控函数可产生预期变化
- [ ] 8.6 使用fake network counters证明planner、summary、rollup、metrics、retention和全部错误路径对Node/Gateway/Prometheus/互联网/模型数据面请求均为零

## 9. PostgreSQL 18 恢复、并发与容量验收

- [x] 9.1 新增change-specific PostgreSQL18 acceptance runner，要求exact test discovery、固定安全failure mapping、禁止go/raw PostgreSQL日志直出、受保护mktemp与EXIT trap、唯一资源前缀、固定镜像digest/受限角色、清六类代理并严格清理container/volume/network/temp
- [ ] 9.2 覆盖72小时边界、UTC/DST、日内策略切换、非对齐activation、暂停/重入、零数据、漏槽、abandoned、Provider独立降级和95% partial/complete矩阵
- [ ] 9.3 在summarize事务前/中/commit未知、summarized后、每批delete前/后、complete前、final rollup事务中注入崩溃，验证恢复不重算/不多删/不部分发布
- [ ] 9.4 注入stale fencing、同一Control内双scheduler/worker、statement timeout、PostgreSQL重启、pool exhaustion及repository/SQL权限/trigger故障，不执行宿主机disk-full，验证固定failed_from、源证据保留和最终收敛
- [ ] 9.5 Permanent CI运行1/10/50 Node、总计1,000账号的短slot功能矩阵；nightly/manual对86.4万/115.2万snapshot运行至少5次summary/rollup与20个delete batch，按nearest-rank记录P50/P95/P99、DB/index/WAL/max RSS/buffers/lock wait/守恒，硬门禁仅为无OOM/timeout/deadlock和正确性
- [ ] 9.6 运行全仓Go race覆盖planner/worker/reconciler/Store/metrics并并发current query/promotion/retention，确认无race、死锁或高基数series
- [ ] 9.7 在Control与PostgreSQL同时停止窗口，对独立pinned官方CLIProxyAPI digest执行认证`/v1/models` baseline 1/1和outage 100/100，记录这是data-plane isolation而非Gateway inference E2E
- [ ] 9.8 验证所有acceptance成功/失败路径最终container/volume/network/temp residual为零，报告仅保存固定计数、时延和状态且不含身份

## 10. Runbook、证据与最终门禁

- [x] 10.1 编写history compaction Runbook，覆盖UTC资格、状态解释、partial、积压/失败、WAL/lock、暂停恢复、checksum/count不一致、只增不删处置和禁止人工SQL重建
- [ ] 10.2 固化rollout/rollback：Migration→新二进制disabled→compatibility gate→staging单Worker→逐步启用；回滚先停runner、保留forward schema且生产禁止down，并逐项dry run
- [x] 10.3 更新README/config reference和现有poll/snapshot/lifecycle/query Runbook，说明受控历史删除、合法空current source、Provider health冗余与当前产品字段兼容
- [x] 10.4 运行`make generate`两次确认OpenAPI/Go/TypeScript生成物零差异，并运行`make test`、`make build`、全部Go tests、full race、vet、前端typecheck/test/build和actionlint；新增独立PG18 history CI job、明确timeout并与其他container jobs使用唯一资源前缀隔离
- [ ] 10.5 运行Migration9 schema/ACL/rollback、旧二进制forward-schema、PostgreSQL18功能/故障/容量、零外部请求、敏感canary和数据面隔离完整验收，保存脱敏证据摘要
- [x] 10.6 运行本change strict、全部canonical specs strict、`git diff --check`和generated diff检查，对照proposal/design/spec/tasks与系统设计确认无漂移
- [x] 10.7 检查`git status --short`、Migration编号、临时容器/volume/network/目录和敏感内容扫描，确认worktree只包含本change计划/实现并整理Conventional Commits分层提交计划
