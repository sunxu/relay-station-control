# Control 账号清单历史压缩运行手册

## 1. 当前状态与适用边界

本手册描述阶段 3 `add-control-account-inventory-history-compaction` 的目标运行契约。**当前工作树已具备 Migration 9、确定性核心算法、纯 compaction/daily-rollup/retention 模型、严格 compatibility/Store adapter、受控 compaction summarize/分批snapshot delete/complete/fail、daily-rollup planner/claim/renew/reconcile/finalize/fail、四阶段 retention 与对应audit事务、低基数metrics，以及已接入 `cmd/control/main.go` 的默认关闭运行循环；完整生产验收尚未完成，因此不能在任何环境声称已通过完整功能验收或允许生产启用。** 当前实现是可验证候选，不是已批准部署功能。完成全部 OpenSpec、故障、容量、隐私、旧二进制和发布门禁前，操作动作只有“保持关闭”和“评审实现”。

History 只消费本环境 Control PostgreSQL 已提交的 poll、Provider result、snapshot、activation 和 current state。它不得调用 Node、Gateway、Prometheus、互联网或模型数据面，不依赖 Redis，也不注册普通 durable-job kind。PostgreSQL 的 UTC 时间、专用 run 行、lease/fencing、不可变摘要和实际删除计数是唯一真相源。

本 change 不提供历史 API/UI、导出、人工重建/删除、趋势页面、告警路由、HMAC 账号指标、跨 Node 检测或 current lifecycle 清理。详细字段、权限和敏感数据边界见 [history compaction contract crosswalk](../evidence/account-inventory-history-compaction-contract.md)。

## 2. 运行配置与启动门禁

| 环境变量 | 默认值 | 安全范围/组合 |
|---|---:|---|
| `CONTROL_ACCOUNT_INVENTORY_HISTORY_ENABLED` | `false` | `false`仍执行只读compatibility gate与metrics状态采集，但不构造或启动mutation loops；只有显式`true`且兼容时才运行循环 |
| `CONTROL_ACCOUNT_INVENTORY_HISTORY_SCAN_INTERVAL` | `30s` | `1s..5m` |
| `CONTROL_ACCOUNT_INVENTORY_HISTORY_CLAIM_LEASE` | `30s` | `5s..5m`；必须严格大于 statement timeout |
| `CONTROL_ACCOUNT_INVENTORY_HISTORY_CONCURRENCY` | `1` | `1..8`；staging 首次只能使用 `1` |
| `CONTROL_ACCOUNT_INVENTORY_HISTORY_DELETE_BATCH_SIZE` | `500` | `1..5000`；生产首次从批准的最小值开始 |
| `CONTROL_ACCOUNT_INVENTORY_HISTORY_STATEMENT_TIMEOUT` | `10s` | `1s..30s`；必须严格小于 claim lease |
| `CONTROL_ACCOUNT_INVENTORY_HISTORY_DATABASE_BACKOFF_INITIAL` | `1s` | `100ms..5m` |
| `CONTROL_ACCOUNT_INVENTORY_HISTORY_DATABASE_BACKOFF_MAXIMUM` | `30s` | initial 到 `5m`；不得小于 initial |
| `CONTROL_ACCOUNT_INVENTORY_HISTORY_SHUTDOWN_GRACE` | `15s` | statement timeout 到 `1m` |
| `CONTROL_DATABASE_MAX_CONNS` | 未设置 | Control PostgreSQL pool环境覆盖值`1..100`；缺失时保留现有pgx URL/default行为，仅按容量批准值设置，连接耗尽验收使用`1` |

以下历史语义不是运行配置：snapshot 完整日资格 `72h`、poll/summary/run 保留 `30d`、coverage complete 阈值 `9500` basis points。不要创建相邻环境变量试图缩短窗口或重解释已持久化日期。

Control 启动后无论 enabled 与否都必须先在 statement timeout 内完成只读 compatibility gate，并核对：

- Migration 9 的六张 aggregate/run history 表及一张 durable retired-day cutoff 表、必要约束、不可变 trigger和固定函数签名；
- PostgreSQL core `sha256(bytea)`，不得以 `pgcrypto` 或其他 extension 替代；
- runtime 对精确 `SECURITY DEFINER` 函数的 `EXECUTE` 和对受保护表无任意读写权限；
- Provider current health 字段及安全回填形状；
- `control_query_current_account_inventory_v1` 签名与合法空 current-source 语义。

任一项不兼容时只将 history 标记为 `schema_incompatible` 并保持 runner 关闭；不得因此阻止既有 poll、lifecycle、current query 或 Control HTTP 服务启动，更不得调用外部目标。兼容已通过后，单次history metrics聚合或coverage读取失败也只保留独立观测的enabled/reason并省略数据库派生history族，不得使进程级`/metrics`、其他collector或Control失败。原始数据库错误、对象身份和 SQL 参数不能进入日志或指标。

## 3. UTC 资格、分段与 partial

日期资格完全由 PostgreSQL UTC 计算：

```text
day_start = summary_date 00:00:00 UTC
day_end   = next UTC day 00:00:00 UTC
eligible  = day_end <= clock_timestamp() - 72 hours
```

- 账号 snapshot 按 `observed_at` UTC 日进入账号分段；Provider expected、漏槽、abandoned、策略分段和 poll retention 按 `scheduled_at` UTC 日。
- observed 日与 scheduled 日不同是 `source_day_mismatch`。两个相关键都只失败并保留源证据；不要把行移动到任一日期。
- 预期槽只枚举 Node monitoring 与 Provider policy 半开激活区间交集中的五分钟对齐点。非对齐起点取其后的第一个对齐槽。
- 交集有至少一个槽但没有 poll/snapshot 时仍生成零数据 Provider segment：expected 为正、applied 为零。
- 非空交集没有任何对齐槽时不生成 key、expected 或虚假 partial。
- 日内策略切换或重新加入可产生多个策略 segment；只有该 Node/日全部去重预期 compaction completed 后才发布唯一 final Provider rollup。
- Coverage 必须从总 applied 除以总 expected 重算，不能平均 segment ratio。`>=95%` 为 `complete`，否则 `partial`。Partial 是已完成但证据有缺口，不是失败；不得补零、外推、进入正常趋势比较或自动恢复历史异常。

Planner 的正常扫描只覆盖 `day_end > database_now - 30 days` 的已达72小时日期，避免从无限 activation 历史反复创建工作。升级前已存在且超过30天的日期只有在仍有同日 source poll，或已经存在 compaction lineage 时才进入 bootstrap；Migration 9 本身不生成summary/run，也不直接删除旧源数据，bootstrap仍必须走相同claim、checksum、删除守恒、finalize和retention证明链。Rollup-run retention会在同一事务先写入不可变的 `(summary_date, instance_id)` retired-day cutoff再删除run；该marker在后续分批删除compaction runs期间及删除完成后持续阻止planner复活，并拒绝该UTC日的晚到poll。不要删除、修改或人工伪造retired marker。

## 4. 状态、积压与失败处置

Compaction 主路径固定为：

```text
pending -> summarized -> deleting -> completed
   |            |           |
   +----------> failed <-----+
                failed_from=pending|summarized|deleting
```

Daily rollup 只有 `pending|completed|failed`。Planner只在该Node/日全部去重预期compaction completed后幂等创建run；claim/renew使用数据库lease和随机fencing，过期lease由reconcile固定失败后，仅`segment_incomplete|statement_timeout|lease_expired|database_unavailable`可重新claim，segment count/checksum、activation和internal一致性失败保持fail-closed。`completed`及两类final rows不可回退或覆盖。

纯状态模型与 PostgreSQL 实现都规定同一次 claim 的 lease/fencing 在 `pending → summarized` 后保留，Worker在每个后续事务前 renew；只有 completed 或 failed 清除 lease。旧 fencing token必须零影响。未知 summarize/delete commit不在内存猜测：Worker退出当前尝试，下一次claim从持久化 `pending|summarized|deleting`与累计deleted count恢复；completed允许同checksum的有界幂等重读重试。删除计数按每批实际 `DELETE ... RETURNING` 数量在同一事务增加，不能超过固化 source count，且只有 remaining为零、`deleted == source`且checksum身份相同时才能 completed。

PostgreSQL 18集成测试已覆盖compaction claim/renew、summary/source proof/audit同事务、batch=1删除与累计、completed及相同checksum幂等重放、checksum不一致原子进入fixed failed、锁等待跨过lease到期时renew零行，以及直接runtime/owner gate伪造拒绝。Summarize恢复矩阵用数据库statement timeout证明无部分写入，以提交前backend断连证明全量回滚，并在提交后连接断开后验证唯一持久摘要和同fence重读不重算；runtime同时锁定timeout/database unavailable固定分类与commit unknown不误写失败。过期active lease不能由Worker直接claim；pending/summarized/deleting三阶段必须先由Reconciler原子写入精确`failed_from`和`lease_expired`、清除旧owner/lease/fence，随后新claim以attempt 2和新fence从原阶段继续。三阶段矩阵逐run验证旧fence续租零行、source checksum不变，并最终只有一条run、一份Provider segment、一条summarized audit和一条completed audit。Daily rollup测试覆盖未完成segment不规划、幂等规划、单claim、全字段segment checksum的PostgreSQL↔Go一致、account/Provider final rows与run/audit原子完成、无Provider时的原子发布、相同fence幂等重放和不同fence拒绝。Retention测试覆盖四个互异受控函数、`limit=1`多批、poll子行级联实际计数、audit失败整体回滚、current FK `SET NULL`与清理前后query等价，以及segment/final→rollup run→compaction run依赖顺序；两类级联子trigger故障矩阵证明poll、Provider result、duplicate、三项累计进度和audit整批回滚，解除后以`limit=1`逐批恢复且每poll实际删除三行。资格边界矩阵还覆盖poll `scheduled_at`、segment/final的UTC `day_end`与rollup `completed_at`双门禁，以及completed rollup/compaction run自身`completed_at`在固定30天边界两侧的删除与保留。Poll候选矩阵只允许completed、snapshot为空、已到期且同key全部terminal的正例，拒绝summarized、failed、`source_day_mismatch`、非终态、仍有snapshot和未到期证据。精确Migration8→9 fixture另覆盖超过30天legacy poll的source-backed首次收敛、retired marker原子cut-off、分批compaction删除期间和删除后的零复活、晚到poll拒绝及带marker的down拒绝，另一个精确fixture覆盖zero-poll但已有completed compaction lineage跨过正常horizon后仍完成rollup。Repository-loop测试覆盖各 `failed_from`恢复路径、unknown summarize/delete/complete/finalize commit、retention unknown commit等待下一次持久扫描、停止后不启动下一事务，以及compaction/rollup/retention共享总并发与固定错误脱敏。它们仍不是连接池耗尽或每个写入点崩溃注入验收。

生产函数catalog与固定30天边界矩阵共同锁定四阶段inclusive资格；同一主链证明依赖存在时后续阶段拒绝、最后retained completed Provider rollup删除后coverage从一条省略为零、rollup run与retired cutoff原子提交，并由legacy `limit=1`两批compaction删除证明中间及最终均零复活。该组合不覆盖6.8的异常状态/未来alerts保留矩阵或6.9的retention/query/promotion/scope四路并发。

Poll retention兼容fixture还在删除前后对Provider/account current整行移除唯一允许变化的`current_poll_run_id`后做JSON等价，并独立确认两个FK为NULL；这锁定来源时间/版本/提交、基础状态、lifecycle/missing、Provider health和query freshness均不被历史清理改写。

生产Store fixture还证明poll retention前后真实Repository page完全等价；独立HTTP fixture只将两个current-source FK合法置NULL，验证真实Repository/Handler的status/body等价，不重复执行retention。较新degraded health可在current source仍指向旧promotion时独立推进，迟到旧result不能覆盖；缺失health必须在Store fail closed，并经HTTP固定映射503且不泄露identity。

真实fenced-finalize矩阵进一步以A槽current、C槽较新health和中间B槽证明stale判定使用已持久health watermark：A<B<C时B只保留`stale_poll`证据，不倒退health或推进snapshot/lifecycle。并发finalize与策略scope切换只允许完整串行结果，scope先行不刷新health；从未promotion的失败结果不创建Provider current、snapshot或lifecycle。该矩阵不覆盖retention、query、promotion与scope四路同时并发，后者仍须按6.9验收。

Final rollup只读取不可变completed segments。账号final逐项求和，边界reset按`first_scheduled_at,last_scheduled_at,policy UUID`顺序计算，末值按最新`last_scheduled_at,policy UUID`选择；Provider final从总applied/expected重算coverage并省略0 expected。Finalize在一个事务内校验预期/完成segment数与版本1全字段segment checksum，写入全部account/Provider final rows、run completed和固定audit；任一步失败均不留下部分发布。未知finalize commit只以相同run/fence有界重放并读取持久化completed结果，不创建第二份真相。

### oldest eligible 或 backlog 持续增长

1. 先确认 enabled/compatibility 固定状态；默认关闭造成增长是可见容量信号，不是允许跳过门禁的理由。
2. 按固定状态、`failed_from`、delete backlog rows 和最近批次结果判断卡在 planner、summarize、delete 还是 rollup；不要按日期、policy、run ID 建 Prometheus 标签。
3. 检查 PostgreSQL 连接池、statement timeout、过期 lease、锁等待和磁盘/WAL 趋势。只保留聚合计数和固定分类，不复制原始 SQL、参数或标识符到工单。
4. 若 run 是 pending，可在兼容且数据库恢复后由 Reconciler/Worker按原唯一键恢复；若已 summarized/deleting，只续删。
5. 若任一 source/deleted/remaining count 或 checksum 不一致，立即暂停 runner，进入“只增不删”模式并按下节升级；不得为了降低 backlog 跳过失败键或删除证明记录。

### PostgreSQL、timeout、pool 或 lease 故障

- 数据库不可用时所有循环有界退避，不建立内存任务，不请求 Node，不删除无持久 claim/fencing 归属的数据。
- 旧 fencing token 更新零行；不要把它改成可接受 token，也不要直接延长数据库 lease。
- 未知 commit 结果通过重新读取唯一 run/摘要/计数判断；daily finalize仅允许相同run/fence的有界幂等重放。不要假定失败后重复写，也不要假定成功后人工推进。
- 恢复后先运行过期 lease Reconciler，再允许新 planner/claim。高频源数据超期保留是安全降级。

数据库renew/reconcile和运行循环已实现并有定向测试。Compaction、rollup worker和单个有序retention scanner共同使用`CONTROL_ACCOUNT_INVENTORY_HISTORY_CONCURRENCY`这一总上限，不各自获得一份额度。Retention始终按poll/children→segment/final→completed rollup run（同事务持久化retired-day cutoff）→completed compaction run调用四个有界事务；某次提交结果未知时不在内存立即重放，而是等待下一次扫描由数据库持久真相重新选择候选。当前循环把state/day/count/checksum/activation/repository不一致视为全局fatal：先停止planner和所有其他新claim/新事务；触发异常的原Worker只允许执行一次带原fencing的terminal-fail事务来持久化固定失败原因，其他Worker仅排空已经进入的单个有界事务，然后history service进入固定`runtime_stopped`。它不会自动越过异常键继续删除或发布rollup。真实Control process验收已覆盖默认disabled兼容探测、history metrics权限故障不毒化全局registry、合法zero-poll activation日从planner到compaction/final rollup收敛，并以同一instance连续三日留下pending/summarized/deleting三个20秒claim；Control在lease仍有效时SIGTERM退出并重启，PostgreSQL随后停机超过lease再恢复，三阶段均只能经Reconciler后以attempt 2完成，最终恰有三条completed compaction/rollup、三份Provider segment/final row及每阶段各一条summarized/complete/rollup-complete audit。实际PID停止始终有界；runtime测试进一步证明已进入的短事务可在grace内完成，随后不启动下一事务或删除批。持锁SQL事务下的真实SIGTERM drain/timeout、真实pool exhaustion和其余7.4故障矩阵仍待验收。

## 5. WAL、锁和批量删除压力

只观察聚合数据库指标：WAL 增长率、checkpoint、autovacuum backlog、buffer、连接池使用、锁等待数量/时长、批次行数和事务时长。不要采集 `pg_stat_activity.query`、SQL 参数或逐 run identity 到普通日志/artifact。

出现 WAL 或锁异常时：

1. 停止新 planner/claim，允许已进入的短事务在 shutdown grace 内完成；超过 grace 取消 operation context并保留 lease供重启恢复。
2. 将 enabled 保持 `false` 后滚动重启 Control。当前只支持启动时读取配置，不支持热更新。
3. 确认没有仍持有 history 受控函数的事务，再观察数据库自然恢复；不要 kill 普通业务事务、禁用 trigger或使用维护者身份直接 DELETE。
4. 复核容量证据后，下一次启用只允许降低到批准的 concurrency/batch或延长 scan interval/timeout范围内调整；不得缩短 72h/30d 或提高 8/5000 上限。
5. 如果小批次仍造成不可接受压力，继续暂停并提交新的容量/索引/函数前向修复；积压保留优先于破坏性收敛。

## 6. checksum/count 不一致：只增不删

以下任一情况都进入固定 fail-closed处置：source checksum/count 改变、segment checksum/count 不一致、remaining snapshot 非零但 deleted/source 守恒无法成立、actual deleted 超过 source、completed 依赖缺失或 activation truth 不一致。纯模型、受控PostgreSQL函数和Worker守恒检查已验证正常compaction主路径及若干不一致路径；一旦触发完整性异常，共享fatal状态停止新planner、claim和破坏性事务。尚未执行全部崩溃/容量/并发矩阵，因此不能把一条合成主路径当作所有源数据都可安全删除的生产证据。

1. 关闭 history，确认 poll/lifecycle/current query继续运行且不修改历史失败证据。
2. 保留所有 remaining source、segment/final rows、run、retired-day cutoff、audit 和 forward schema；禁止任何 retention 类函数继续处理相关依赖。
3. 只收集固定状态、阶段、计数关系、PostgreSQL major/Migration version和时间窗口；不得导出 account identity、checksum值、run/policy ID、SQL或数据库 dump。
4. 新建问题和前向修复 change，复现于合成 PostgreSQL 18 fixture，并通过 checksum golden、故障恢复与canary门禁。
5. 修复只能新增可审计、可回滚的验证/状态路径。生产禁止人工 UPDATE checksum/count/status，禁止重建/覆盖 summarized/final rows，也禁止通过删除源数据让计数“看起来一致”。

“只增不删”允许新 poll/snapshot/current truth继续由既有采集链写入；它禁止 history 对受影响证据做破坏性操作。History 故障不能演变为采集停机或数据面停机。

## 7. 暂停、恢复与停止顺序

计划暂停：

1. 将 `CONTROL_ACCOUNT_INVENTORY_HISTORY_ENABLED=false` 应用于下一次 Control 部署/重启。
2. 停止 planner 和新 claim；Worker 不得再进入新的 summarize/delete/rollup事务。
3. 给已进入的短事务最多 shutdown grace；超时取消 operation context，不猜测提交结果。
4. 确认 enabled 为零且现有源/摘要/run 未被回滚脚本删除。

计划恢复：

1. 确认 Migration/ACL/query compatibility、PostgreSQL资源和失败原因已修复。
2. 先保持 disabled 运行新二进制并观察 compatibility状态。
3. 在 staging/隔离环境用单 Worker和小批次完成一个合成 eligible UTC 日，证明source/deleted守恒、final rollup、current query等价和外部请求零。
4. 生产只有在最终候选全部门禁通过后才显式 enabled；从单并发开始，由 Reconciler接管合法过期 lease。

不要删除 lease、手工把 failed改回pending、创建替代 run、重发历史 Node 请求或从当前账号表反推历史。

## 8. 发布、灰度与应用回滚

发布顺序固定为：

1. Migration owner在隔离 PostgreSQL 18完成 Migration 9空库和既有 Migration 8 up；确认 Migration 不生成摘要/run/retired marker、不删除源数据、不复制身份、不改写 poll/promotion/lifecycle，并由启用后的普通planner/worker链首次收敛升级前超过30天的source-backed poll。
2. 生产执行 additive Migration 9，保留受保护down约束。
3. 部署理解 forward schema 的新二进制，但保持 history disabled。此阶段既有 poll/lifecycle/current query必须继续；durable-job production registry保持不变。
4. Compatibility gate通过后，先在 staging启用 `concurrency=1` 和批准的小 batch，处理一个已达72小时的合成 UTC 日。
5. 保留已通过的 PostgreSQL 18 `1/10/50 Node、每档总计1,000账号`短slot功能矩阵，并继续完成状态/崩溃/并发/保留、86.4万/115.2万snapshot nightly/manual容量、完整敏感canary、旧二进制forward-schema和独立数据面隔离门禁。
6. 生产从单 Worker逐步启用，观察 unfinished age、failed_from、WAL、locks、batch duration与守恒；异常立即回到 disabled。

应用回滚：

1. 先 disabled并停止 planner/claim，有限等待短事务；确认未知commit可由持久状态恢复。
2. 回退应用二进制，**保留 Migration 9 forward schema、所有summary/final/run/retired-day cutoff/audit和剩余source**。
3. 旧二进制不得注册history任务、访问history表或要求generic job catalog变化；继续既有poll、promotion和current query。
4. 已经合规删除的snapshot不尝试恢复；completed final rollup是其替代证据。
5. 生产永远禁止执行 Migration down。Down只用于隔离全新环境，且必须证明无history row、无retired-day cutoff、无删除计数、无后续依赖；任一证明不成立即拒绝。

## 9. 禁止人工 SQL 重建或清理

禁止以下处置，即使使用 migration owner：

- 直接 INSERT/UPDATE/DELETE/TRUNCATE六张aggregate/run history表或retired-day cutoff表、snapshot、poll或current state；
- 手工设置 transaction-local history gate、伪造function owner/search path或临时禁用trigger；
- 从残余snapshot覆盖已 summarized segment，或从segment覆盖 completed final；
- 修改 checksum、source/deleted count、failed_from、fencing、lease、attempt或阶段时间；
- 追溯修改 activation 区间以改变已生成 expected/coverage；
- 为减少积压而删除 failed/未完成 run、poll、audit或current lifecycle；
- 调用 Node 当前接口补造过去时间槽。

人工重建/强制删除未来如有必要，必须新开 capability，要求重新认证、二次确认、理由、实名不可变审计、源数据仍存在及独立回滚/隐私评审；当前 change 没有这样的入口。

## 10. 指标、日志与敏感证据

目标指标仅包含：history enabled固定原因、compaction/rollup固定state/result、oldest eligible unfinished、failure by `failed_from`、delete backlog/rows/duration，以及最近 retained completed final Provider coverage。允许标签只有受控 instance/provider和固定reason/state/phase/result；日期、policy/poll/run/fencing、checksum、email/account key、endpoint、Secret和raw error都禁止作为标签或普通输出。

已实现的compaction、daily-rollup与retention history事件使用actor-null固定system category/action/result，与 summarized、每个snapshot/retention delete batch、compaction completed/failed及rollup completed/failed状态在各自事务写入不可变审计。独立exact gate拒绝普通runtime、migration owner或伪造transaction-local值直接插入，Details只允许instance、summary date、固定phase和实际summary/delete/final行数。Retention会在compaction run上持久累计实际删除的poll、Provider result和duplicate计数；history审计初始保留180天且不由本 cleaner删除。

验收 artifact只保留候选commit、UTC窗口、退出码、固定分类、聚合计数/时延、PostgreSQL major/Migration、请求计数和清理计数。任何canary命中只输出固定失败类，不输出值、文件名或上下文。

## 11. 当前候选限制与完成门禁

截至当前第五批实现：

- `cmd/control/main.go`已无条件加载history配置、构造同一Store repository、注册metrics collector并纳入Control shutdown WaitGroup；disabled仍执行只读compatibility probe但不构造mutation loops，enabled且兼容时才启动planner/compaction-worker/rollup-worker/reconciler/retention；history不兼容或fatal只停止history；
- Migration 9已创建六张aggregate/run表和一张durable retired-day cutoff表、Provider health/current-query兼容、严格catalog/ACL/audit gate、normal horizon加source/既有lineage bootstrap的eligible key与daily-rollup planner、compaction claim/renew/reconcile/summarize/bounded-delete/complete/fail、daily-rollup claim/renew/reconcile/finalize/fail，以及poll/rollup-row/rollup-run/compaction-run四个retention函数；
- 纯 compaction模型与循环覆盖 `pending → summarized → deleting → completed`、Reconciler独占过期active lease、`failed_from`恢复、stale fencing零影响、unknown-commit持久状态决策和删除守恒；daily-rollup模型与循环覆盖可恢复失败allowlist、renew/reclaim、completed不可变、unknown-finalize有界重放和固定错误分类；
- Final account/Provider聚合、9500 basis-point边界和全字段version-1 segment checksum已有golden；PostgreSQL主路径验证final rows、segment count/checksum、rollup completed和audit原子发布、同fence幂等重放、audit失败整体回滚，以及无Provider/0 expected时原子空发布并省略coverage；
- Compaction、rollup与retention共享一个总并发上限；全局fatal会停止两类新claim和新的retention事务并排空当前有界事务；
- Retention已有固定30天资格、互异gate/函数、持久删除计数、`limit=1`多批、current FK置空/query等价、unknown-commit持久扫描、legacy source bootstrap与retired-day不复活证据；
- metrics provider只在compatibility通过后读取受控M9快照，导出固定state/failure/oldest/backlog/delete与最多`50×64`的instance/provider coverage；schema不兼容时只保留enabled/reason族，不伪造数据库派生零值；
- 真实Control process runner已覆盖Migration9、默认disabled+compatible、metrics函数权限撤销时HTTP 200与其他collector隔离、合法zero-poll日的enabled planner→compaction→rollup，以及pending/summarized/deleting三阶段claim组合跨Control重启、PostgreSQL stop/start和lease过期后的Reconciler独占恢复；最终三阶段各自只有单份completed compaction/final rollup与审计。实际PID多次SIGTERM正常退出，动态secret/连接串日志扫描及container/volume/network/temp/lock零残留；它不伪造旧日observation，也不替代真实snapshot deletion或7.4持锁SIGTERM/pool exhaustion证据；
- change-specific acceptance runner已通过PostgreSQL 18 schema `up/down/up`、core SHA-256、PostgreSQL↔Go checksum golden、ACL/gate、compaction主路径与恢复、retention/current-query主路径、Migration8 legacy poll首次收敛与retired-day cut-off、exact discovery、race、百万行观察和零容器/卷/网络残留；新增的 PostgreSQL 18短slot功能矩阵也已分别通过1/10/50 Node、每档总计1,000账号的compaction/rollup守恒。该矩阵不记录nightly/manual大规模样本的WAL/RSS/buffer/lock分位数，百万行最大RSS也仍只作为观测，不构成容量阈值；
- 局部safety gate已覆盖7类in-process成功/零数据/partial/固定失败输出、history runtime值格式化、最终本地artifact扫描及production history源码direct network client import边界；它尚未覆盖数据库非身份列sink、真实Control process/fake endpoint请求计数或完整网络路径，因此不能声明完整敏感canary或零外部请求门禁通过；
- pinned旧代码forward-schema rollback runner已在隔离PostgreSQL 18通过：Migration 9完成受控snapshot/poll清理并使current FK为NULL后，固定`d431002`旧Control在隔离网络自行发起唯一CLIProxyAPI账号清单GET，完成poll/promotion并恢复两个current FK；同一旧进程的认证HTTP current query返回唯一账号并原子写入唯一view audit。运行前后history表和history audit指纹不变，generic job三表保持空，fake Node请求精确为1，且container/volume/network/temp/lock残留均为零。生产未执行down；OpenSpec 2.10/7.5据此通过；
- 因此 `CONTROL_ACCOUNT_INVENTORY_HISTORY_ENABLED=true` 已具有候选执行效果，但在持锁SIGTERM、真实pool exhaustion、剩余故障/nightly容量、完整canary与发布门禁完成前仍禁止用于生产，也禁止把接线或单次process smoke通过记录为阶段 3完成。

最终候选至少必须执行：两次可复现生成、全部Go/test/race/vet/build、前端typecheck/test/build、OpenSpec strict、Migration 9 PostgreSQL 18 up/down/up与ACL、history状态/故障/容量/retention、旧二进制forward-schema、敏感canary、外部请求零和data-plane isolation。所有Go/npm/Docker/make命令显式清除大小写HTTP/HTTPS/ALL proxy；PostgreSQL使用隔离测试资源且不把连接串、SQL参数或原始日志保留到artifact。

在所有门禁、最终CI、资源清理和证据review完成前，本手册保持“设计/部分实现”状态，不得改写为已投产运行记录。
