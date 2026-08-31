## Context

参见 [proposal.md](./proposal.md) 的动机。当前 Migration 5～8 已把五分钟 poll、Provider result、不可变 snapshot items、Provider current state、current account lifecycle 和产品只读 query 固化在 PostgreSQL 18。`account_inventory_snapshot_items` 与终态 poll 由 trigger 拒绝删除，Provider/account current source 外键已经预留 `ON DELETE SET NULL` 并冗余来源时间、版本和提交。

系统设计 v1.0 第 12、21、23～25 节要求在完整 UTC 日结束至少 72 小时后，先生成按不可变策略版本的分段摘要，再等待全部预期分段完成后生成唯一最终日级 rollup，最后才可断点清理高频历史。正常窗口为 72～96 小时；在 50 Node、总计 1,000 个合成账号的验收规模下，窗口可能包含约 86.4 万～115.2 万条 snapshot items，因此聚合、锁和删除都必须有界。

现有 `control_query_current_account_inventory_v1` 通过 `account_inventory_provider_states.current_poll_run_id` join `account_inventory_poll_provider_results` 取得 degraded，并把空 current poll 视为不一致。合法 retention 会删除旧 poll 并置空外键，所以本 change 必须先把产品读取所需的 Provider 健康字段冗余到 current state，再允许 poll 清理。

## Goals / Non-Goals

**Goals:**

- 用 PostgreSQL 专用持久状态机完成可并发、可重启、可核验的分段摘要、最终 rollup 和断点续删。
- 精确表达日内策略切换、Node 监控启停、漏槽、abandoned、未提升观察和零数据激活区间，固化 95% Provider 日级覆盖率语义。
- 把 snapshot items 与 poll 历史从绝对不可删除改为仅能通过固定前置条件、固定批量和最小权限函数清理。
- 保持 current lifecycle、current query、旧应用 forward-schema 兼容和数据面隔离。
- 为后续趋势和告警提供 completed final rollup 与低基数健康指标，不让后续消费者重新解释残余高频行。

**Non-Goals:**

- 不提供历史产品 API/UI、导出、人工重建/删除、趋势比较或告警路由。
- 不生成 HMAC `account_id` 或任何账号级 Prometheus series。
- 不清理current account、missing/out-of-scope lifecycle、既有认证审计、本change新增的180天history系统审计或未来告警历史；这些分别需要产品语义或安全保留change。
- 不修改 Gateway、Node、Driver 外部契约、poll 周期、promotion/lifecycle 判定或普通 durable-job catalog。
- Migration DDL不生成摘要或删除数据；runner启用后可以处理升级前仍完整且达到资格边界的日期，不完整日期fail closed。
- 不支持多个Control副本共享同一数据库；lease/fencing只保护单Control内并发、重启恢复和旧执行者。

## Decisions

### 1. 使用专用 history 状态机，不注册 generic async job kind

`account_inventory_compaction_runs` 和 `account_inventory_daily_rollup_runs` 自身保存 claim owner、lease expiry、fencing token、attempt、状态与恢复证据。Scheduler 以数据库时间幂等创建 run，Worker 通过 `FOR UPDATE SKIP LOCKED` 认领，Reconciler 只回收数据库时间已过期的 lease。

不把 history job 注册到 `async_job_kinds`。当前旧二进制的 production registry 为空，并要求数据库 active catalog 精确匹配；Migration 若注册 active kind，会使旧二进制在 forward schema 上拒绝启动，破坏应用回滚。通用 durable-job 仍可复用循环、退避和观测代码模式，但不是 history 的第二真相源。

备选方案是先修改 generic catalog 兼容协议再注册任务；它扩大阶段 1 durable-job 契约和回滚风险，留给独立 change。

### 2. 账号摘要遵循 observed UTC 日，coverage 遵循 scheduled UTC 日

每个 history key 使用：

```text
day_start = summary_date 00:00:00 UTC
day_end   = summary_date + 1 day
eligible  = day_end <= PostgreSQL clock_timestamp() - 72 hours
```

账号snapshot segment遵循系统设计，以`date(observed_at AT TIME ZONE 'UTC')`归日；Provider expected slot、漏槽、abandoned、poll retention和pinned policy segment按`scheduled_at` UTC日归属。正常poll在有界启动宽限与请求timeout内应保持两者同日。任何snapshot observed日与scheduled日不同都进入固定`source_day_mismatch`失败，两个相关日均不删除源数据，也不静默把账号证据移到错误策略分段。Abandoned没有observed time，只按scheduled日进入coverage。

备选方案是统一改用scheduled date；它与系统设计明确的snapshot observed归日冲突，因此不采用。Fail closed牺牲极端跨午夜历史的自动收敛，换取不猜测归属。

### 3. expected slot 取半开激活交集中的实际五分钟网格

Planner 读取不可变 Provider policy activations 与 Node inventory monitoring activations，裁剪到 `[day_start, day_end)`，合并同策略版本的重叠/相邻交集后，对齐枚举满足 `slot >= lower AND slot < upper` 的五分钟 `scheduled_at`。历史激活边界可能来自 `clock_timestamp()` 而非整五分钟，本 change 不追溯改写；例如 00:02 开始的区间首个 expected slot 是 00:05。

只有合并交集内实际存在至少一个对齐scheduled slot时才创建key；`[00:01,00:04)`这类无slot短区间不生成虚假expected。存在预期slot但没有poll时生成零数据segment。漏槽与abandoned进入expected，只有`promotion_applied=true`进入coverage分子。`policy_changed`、失败、不完整或stale poll保留各自固定计数但不进入分子。

为防止activation truth从无限历史持续生成新工作，正常planner只枚举`day_end > database_now - 30 days`的retained horizon。更旧的eligible日期只有仍存在同日source poll，或该Node/日已存在任一compaction run时才继续枚举全部预期key；前者让Migration 8升级前完整poll首次收敛，后者让zero-poll或晚完成的部分lineage跨过horizon后仍可完成。两条bootstrap路径都只创建普通run并走相同claim、checksum、守恒、finalize与retention门禁，Migration DDL本身不生成run。存在durable retired-day cutoff的Node/日永远先被planner排除。

备选方案是给历史 activation 增加五分钟对齐约束；既有记录无法无损追溯对齐，且会改变资产注册语义，因此不在本 change 中采用。

### 4. 六张 aggregate/run 表与一张 durable cutoff 表分离证据、真相和退休边界

- `account_inventory_daily_summaries`：账号策略分段，唯一`(summary_date, instance_id, account_key, provider_policy_version)`；保存first/last scheduled/observed、last basic status、sample/status counts、first/last cumulative counters和reset counts。保留first counters使最终rollup能识别分段边界计数下降，但累计差值仍不解释为准确用量；不复制normalized email。
- `account_inventory_daily_provider_summaries`：Provider 策略分段，唯一 `(date, instance, provider, policy)`；保存 expected、transport、contract、snapshot complete、promotion applied/skipped、abandoned、degraded、first/last promotion、coverage numerator/denominator、ratio/status 和固化的 threshold basis points `9500`。
- `account_inventory_daily_account_rollups`：唯一 `(date, instance, account_key)`；合并所有账号分段，末次字段以最新 scheduled/observed 和稳定 tie-breaker 决定。
- `account_inventory_daily_provider_rollups`：唯一 `(date, instance, provider)`；逐项求和后重新计算 `sum(applied)/sum(expected)`，不得平均分段 ratio；固化 `complete|partial` 和阈值。
- `account_inventory_compaction_runs`：唯一 `(date, instance, policy)`；保存状态、failed_from、lease/fencing、源 snapshot/poll/provider/duplicate counts、版本化 SHA-256 source checksum、deleted snapshot count、attempt、固定错误与阶段时间。
- `account_inventory_daily_rollup_runs`：唯一 `(date, instance)`；保存状态、lease/fencing、expected/completed segment counts、版本化 segment checksum、attempt、固定错误与阶段时间。
- `account_inventory_history_retired_days`：唯一 `(date, instance)`；只保存数据库生成的`retired_at`和无身份的day-level负向真相。它仅能由completed rollup-run retention在同日poll与四类segment/final rows全部为空后原子插入，不因后续compaction-run删除而清理。

账号summary/rollup只含受保护account key，不复制normalized email；当前change无需把可读身份扩散到新历史表，未来授权历史页面必须另行评审identity映射。它们无runtime表权限。Provider summary和retired cutoff不复制账号身份。六张aggregate/run表用数据库CHECK、唯一/外键和immutable trigger保护completed/summarized结果；UPDATE/TRUNCATE始终拒绝，DELETE仅由后述精确retention gate放行。Retired cutoff为insert-once永久marker：普通runtime和migration owner不能伪造或修改，当前cleaner不删除它，并由poll insert trigger拒绝任何落入已退休UTC日的晚到poll。

### 5. Provider current state 冗余最近健康，解除 query 对历史行的依赖

Migration为`account_inventory_provider_states`增加最近合格finalized result的scheduled time、`degraded`和固定degraded reason，并从current pointer对应Provider result一次性回填这些非身份字段。之后只有state已由合格promotion建立，且结果在finalize持锁后仍属当前active policy、slot严格较新、不是`policy_changed|stale_poll`且Provider未out-of-scope时才刷新health；从未promotion的Provider失败观察只保存在poll history，不创建无法满足非空current来源约束的state。只有合格promotion推进current poll、last complete和来源元数据。

Migration 以同签名 `CREATE OR REPLACE FUNCTION control_query_current_account_inventory_v1` 升级 query：current source poll 存在时可做一致性核验，合法为空时直接使用 Provider state 冗余字段；Provider state/last complete/health 缺失或非法组合仍返回固定 503。OpenAPI 与 Go/TypeScript响应字段不变，Store 只改变 SQL 来源。

备选方案是永不删除被 current FK 引用的 poll；它会让保留期依赖未来 promotion，违背 30 天历史边界并掩盖 current state 本应自足的设计，因此不采用。

### 6. summarize 事务一次固化摘要、源计数和校验

Claim 后的 summarize 函数锁 run 并验证未过期 fencing，只允许 `pending` 或 `failed_from=pending`。函数在一个事务中：

1. 重新验证日期资格与激活交集；
2. 从尚未删除的 terminal poll/provider result 与 promotion-applied snapshot 计算两类 segment；
3. 以稳定列顺序、长度前缀和值域编码计算版本化链式SHA-256 source checksum；
4. 写入全部 segment rows、source row counts/checksum；
5. 将 run 置为 summarized。

Checksum复用仓库已使用的PostgreSQL core `sha256(bytea)`，不新增extension。版本1算法固定为`H0=32个零字节`，每行先按版本化length-prefixed canonical encoding得到`R_i=sha256(row_i)`，再按稳定主键顺序计算`H_i=sha256(H_(i-1)||R_i)`；golden同时固化空集、单行和多行结果。它是可由游标常量内存实现的链式校验，不声称等于对全体拼接字节做标准流式SHA-256，也不作为授权凭据。

事务失败全部回滚。进入summarized后，summary路径拒绝upsert/update/delete；`failed_from=summarized|deleting`只能继续snapshot删除。到期summary/final/run删除必须走独立history-retention函数并重新验证poll已清空、rollup completed、保留截止和无失败依赖。

### 7. 删除使用 fencing、transaction-local gate 和 DELETE RETURNING 守恒

每批 snapshot 删除是独立短事务：锁 run、校验 token/lease/status/date/policy、将状态推进/保持 deleting、按 `(poll_run_id, instance_id, account_key)` 稳定顺序选择 `1..configured_batch` 行、打开仅本事务和函数 owner 有效的 history gate、执行 `DELETE ... RETURNING`，再以实际返回数累加 deleted count。

现有immutable triggers不移除。Snapshot batch、poll/cascade retention、segment/final retention和run retention分别使用不同transaction-local gate与精确key；只有`current_user`为固定migrator function owner且受控函数已重检全部依赖时放行目标DELETE。普通runtime即使`SET LOCAL`伪造GUC也不满足owner/函数重检且无表级DML。异常/savepoint rollback必须清除gate语义，连接池复用后无残留；嵌套调用、search_path欺骗、owner直接DML和错误gate均由trigger拒绝。

提交前崩溃时删除和计数一起回滚；提交后崩溃时两者一起存在。完成函数要求 remaining snapshot=0、初始 snapshot source count=deleted count、checksum/run identity不变，否则置 fixed failed 并禁止 poll cleanup。

### 8. final rollup 只从全部 completed segments 原子发布

Planner 每次从 activation truth 重算预期去重 key，并与 completed compaction runs 比较。缺少、pending、deleting 或 failed 的 segment 阻止发布。Rollup Worker 在 fencing 事务内只读取 immutable segment summaries，生成账号/Provider final rows、segment count/checksum，并与 run completed 同事务提交。

Completed final rows和rollup run不可覆盖。当前change只有Prometheus健康读取：它只读retained completed final rollup，允许partial样本但必须导出`complete=false`；没有retained完成日时省略coverage样本。当前change不提供账号历史或趋势产品读取；未来正常趋势reader必须同时限定rollup run `completed`且Provider rollup `coverage_status='complete'`，不得消费partial。Coverage threshold固定为9500 basis points，未来变化需要新change且不重解释旧日。

### 9. retention 顺序保留可证明的删除资格

初始顺序固定为：

1. 72 小时后完成 segment summarize/delete；
2. 全部 segments completed 后完成 final rollup；
3. poll满足`scheduled_at <= database_now-30 days`时，逐条验证scheduled date/policy compaction completed且snapshot items为空，再有界删除terminal poll；Provider result与duplicate级联删除，current Provider/account FK置空；
4. segment/final同时满足UTC `day_end <= now-30 days`和对应rollup `completed_at <= now-30 days`且poll已清空后，才按segment rows、final rows清理；completed rollup run只有同日poll与四类segment/final rows全部为空才可清理，并在同一事务先插入durable retired-day cutoff再删除run；completed compaction run还需自身`completed_at <= now-30 days`并最后分批删除。

同为30天时poll必须先于证明其安全的summary/run删除。Rollup-run删除与marker插入的事务原子性关闭了rollup run已消失但compaction runs尚未删尽的复活窗口；marker在部分删除和最终删除后均使planner创建零lineage，并使晚到poll失败。每类DELETE使用独立gate、有界批次和实际计数；UPDATE/TRUNCATE永远禁止。pending/summarized/deleting/failed、source/deleted不一致、非终态poll和仍有依赖的activation/policy永不自动清理，retired marker也永不由本cleaner删除。当前lifecycle、missing/out-of-scope、180天history/admin audit和alerts不在本change cleaner范围。

### 10. runtime 配置默认关闭且全部有界

新增`CONTROL_ACCOUNT_INVENTORY_HISTORY_ENABLED=false`、scan interval、claim lease、in-process worker concurrency、delete batch和statement timeout。72小时snapshot最低窗口、30天poll/summary/run窗口和9500 basis-point coverage阈值在本change固定，不开放运行配置，避免既有key被追溯提前删除。Batch/concurrency/timeout有安全上下限。启动compatibility check成功后才启动runner；停止顺序为停止planner/claim，再让当前短事务有界结束。Enabled状态、oldest eligible day和storage/delete backlog必须可观测，防止默认关闭时静默无界增长。

### 11. 指标、日志与审计保持低基数和身份隔离

指标只导出最近 completed final Provider rollup 的 coverage ratio/complete，以及 compaction/rollup 固定 state/result、oldest eligible unfinished seconds、failure total by failed_from、delete backlog/rows/duration。标签允许受控 instance/provider 和固定 phase/status/reason；不允许 date、policy/poll/run/fencing、checksum、email/account key、Secret、endpoint或原始错误。

自动history状态转换不伪造成实名管理员操作。每次summarized/completed/failed与每批retention success/failure使用actor-null固定category/action/result写入既有不可变`audit_logs`，与对应状态/删除同事务；details只含instance、summary date、固定phase和实际行数，初始保留180天，不含checksum或identity。Run rows继续承担状态恢复，audit承担长期清理证据。未来人工重建/强制删除必须另开带重新认证、原因和实名审计的change。

### 12. PostgreSQL 与数据面隔离是硬边界

History只访问本环境Control PostgreSQL。测试注入network counters，要求Node/Gateway/Prometheus/互联网/模型路径请求为零；它不调用Driver、不补采历史槽、不改current lifecycle。PostgreSQL/history故障只导致源历史超期保留和coverage发布滞后。在Control与PostgreSQL同时停止窗口，对独立pinned官方CLIProxyAPI镜像使用认证`/v1/models`执行baseline 1/1与outage 100/100，证明data-plane isolation但不夸大为Gateway inference E2E。

## Risks / Trade-offs

- [摘要算法错误后源数据已删除不可恢复] → 72 小时资格、不可变分段、版本化 checksum、source/deleted 守恒、final rollup gate、容量与故障注入门禁；生产不提供自动重建。
- [批量删除造成 WAL、锁或 autovacuum 压力] → 有界 batch/concurrency/statement timeout、稳定主键索引、短事务、容量记录 WAL/locks/buffers，并允许暂停 runner延长保留。
- [合法空 source FK 使旧 query 失败] → 先冗余 Provider health并同签名升级 query，真实旧二进制 forward-schema gate验证旧应用仍能启动；新 query验证清理前后字段一致。
- [activation边界非五分钟对齐造成 expected 偏差] → 只计算实际落在半开交集内的固定五分钟槽，测试日内启停、DST、本地时区和多段重入。
- [专用状态机与 durable-job 形成重复基础代码] → 复用循环/退避模式但保持专用表为唯一业务真相；避免破坏 generic catalog/rollback协议。
- [summary表含敏感account key] → 不复制normalized email；仅受保护列和SECURITY DEFINER函数可访问，无表级runtime权限、SQL参数日志关闭、format/canary测试和无产品API。
- [30天后删除summary让长期趋势不足] → 当前系统设计初值就是30天；本 change 固化该窗口。更长保留或外部归档需独立容量、隐私与合规评审。
- [不清理missing/out-of-scope会保留身份更久] → 本 change优先保证current语义不被误删；后续专门 retention change需定义恢复、重新出现和产品审计后再删除。

## Migration Plan

1. 在隔离PostgreSQL18对空库和Migration8数据库执行新forward Migration；验证既有旧列值/行数不变，只新增并回填Provider current非身份健康列，并验证旧二进制在新schema继续poll/current query。
2. 部署新二进制但保持history disabled；compatibility gate检查六张aggregate/run表和一张retired-day cutoff表、约束、core sha256、函数owner/ACL、provider health和query函数签名。
3. 在 staging先启用单 Worker，以一个已达到72小时的合成UTC日验证segment、checksum、分批删除、final rollup、current query字段保持和零外部请求。
4. Permanent CI运行1/10/50 Node、总计1,000账号的缩短slot功能矩阵；nightly/manual运行86.4万与115.2万snapshot规模、至少20个delete batches和5次独立summary/rollup样本，按nearest-rank记录P50/P95/P99、DB/index/WAL、max RSS、buffers、lock wait、恢复和残留。首次change只把完成、无OOM/timeout/deadlock、守恒和配置上限作为硬门禁，不伪造未经批准的生产时延SLO。
5. 生产先以最小concurrency/batch启用，观察unfinished age、failure、WAL与锁，再按Runbook有界调整；任何checksum/count异常立即停runner并保留源数据。

应用回滚先关闭/停止 history runner，再回退旧二进制；保留 forward schema、summary/run、retired-day cutoff和剩余源历史。已合规删除的snapshot不尝试恢复，final rollup是替代证据。生产永不执行down；隔离全新环境只有在没有history row、没有retired marker、没有任何deleted count、没有后续依赖时才允许受保护down。
