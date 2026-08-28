## Purpose

为 Control 建立按完整 UTC 日运行、可崩溃恢复且先汇总后删除的账号历史压缩与最终日级汇总能力，使高频证据在受控保留期内收敛，并为覆盖率、后续趋势和告警提供唯一且不被部分清理污染的历史真相。

## ADDED Requirements

### Requirement: Control MUST 只枚举达到保留边界的完整 UTC 日与预期压缩键

Control MUST以PostgreSQL UTC时间判断资格，只处理`UTC day_end <= clock_timestamp() - 72 hours`的完整自然日。账号snapshot summary date MUST取`date(observed_at AT TIME ZONE 'UTC')`；Provider coverage、漏槽、abandoned和retention归属MUST取poll `scheduled_at` UTC日。合法snapshot的observed日与scheduled日MUST相同，任何跨日源行都使scheduled日及observed日涉及的压缩键fixed failed并保留源数据。预期`(summary_date, instance_id, provider_policy_version)`键SHALL来自不可变Provider策略激活区间与Node inventory monitoring激活区间的半开交集；同一策略版本的多个交集区间MUST合并去重，并按交集内实际存在的对齐五分钟槽计算Provider `expected_poll_count`。只有交集内至少存在一个预期槽时才创建key；有预期槽但没有poll或snapshot时MUST创建零数据segment，无预期槽时MUST NOT伪造key或partial。正常planner MUST只枚举`day_end > database_now - 30 days`的日期；更旧日期在仍存在同日source poll或该Node/日已有compaction lineage时MUST继续枚举全部预期key以bootstrap，两个条件均不存在时MUST NOT从activation truth创建新lineage。Migration MUST NOT自行生成run或retired marker。存在`(summary_date, instance_id)` retired-day cutoff时，planner MUST NOT再为该日创建compaction或rollup lineage。

#### Scenario: 日期尚未到达 72 小时边界
- **WHEN** 某 UTC 日结束距数据库当前时间不足 72 小时
- **THEN** scheduler 不创建或认领该日压缩键，也不读取或删除其历史行

#### Scenario: 日期恰好达到资格边界
- **WHEN** 数据库时间满足该日 `day_end <= now - 72h`
- **THEN** scheduler 可以幂等创建去重后的预期压缩键，且所有日期计算不受进程本地时区影响

#### Scenario: 升级前旧源数据跨过正常 horizon
- **WHEN** Migration 8升级后的eligible UTC日已超过30天但仍存在同日source poll，或该Node/日已有未退休compaction lineage
- **THEN** planner通过普通幂等run bootstrap使全部预期key继续相同证明链；Migration本身不生成run或marker

#### Scenario: 已退休日期不复活
- **WHEN** 某Node/日已有durable retired-day cutoff，即使activation、旧source metadata或部分compaction-run删除窗口仍可见
- **THEN** planner不创建任何compaction或rollup lineage

#### Scenario: 日内切换、移出并重新加入策略
- **WHEN** 同一 UTC 日存在多个策略版本或同一版本多个不重叠激活交集
- **THEN** 每个策略版本只有一个压缩键，expected 槽只覆盖合并后的交集且不重复计数

#### Scenario: 激活交集内完全漏采
- **WHEN** 某预期键的激活交集包含至少一个对齐五分钟槽但没有poll run、Provider result或snapshot item
- **THEN** Control 仍创建零数据压缩键和 Provider 分段摘要，expected count 大于零且 promotion count 为零

#### Scenario: 非空短区间不含计划槽
- **WHEN** 激活交集非空但其中没有任何对齐的五分钟scheduled slot
- **THEN** Control不创建压缩键、不增加expected count且不把该短区间标记partial

#### Scenario: snapshot观察跨越UTC日
- **WHEN** snapshot的UTC observed date与其poll scheduled date不同
- **THEN** Control把对应压缩键标记固定source-day-mismatch失败，保留snapshot/poll且不生成或覆盖摘要

### Requirement: 策略分段摘要 MUST 确定、事务化且一经 summarized 不可变

账号分段摘要 SHALL以`(summary_date, instance_id, account_key, provider_policy_version)`唯一保存首末scheduled/observed时间、末次基础状态、样本数、固定基础状态计数、首末累计成功/失败计数与段内计数重置次数，不复制normalized email。Provider分段摘要 SHALL以`(summary_date, instance_id, provider, provider_policy_version)`唯一保存expected、transport success、contract valid、snapshot complete、promotion applied/skipped、abandoned、degraded、首末实际提升、coverage ratio与`complete|partial`。摘要、确定性源行数/版本化链式校验和compaction `summarized`状态MUST在同一事务提交；`summarized`后摘要MUST NOT被重新聚合、upsert或UPDATE，只有满足history retention全部前置条件的固定受控函数MAY有界DELETE。

#### Scenario: 正常策略分段形成摘要
- **WHEN** eligible 分段包含多个完整和失败 poll、多个账号状态样本及 promotion 结果
- **THEN** 账号与 Provider 摘要按固定字段、排序和计数规则原子写入，缺槽与 abandoned 进入 expected 分母但不进入 promotion 分子

#### Scenario: 累计计数下降
- **WHEN** 同一账号后续样本的累计成功或失败计数小于前一有效样本
- **THEN** 摘要增加 reset count 并保留末次累计值，不产生负用量或猜测跨重置差值

#### Scenario: 摘要事务失败或重复执行
- **WHEN** 摘要任一写入/校验失败，或同一压缩键被重复调度
- **THEN** 失败事务不留下部分摘要；已 summarized 键返回既有确定结果且不从源历史重算或覆盖

### Requirement: compaction run MUST 持久化可恢复状态并禁止从残余源数据重算

`account_inventory_compaction_runs` SHALL 以 `(summary_date, instance_id, provider_policy_version)` 唯一保存 `pending|summarized|deleting|completed|failed`、`failed_from`、确定性源计数/校验、实际删除数、重试数、固定错误分类和阶段时间。合法主路径 MUST 为 `pending -> summarized -> deleting -> completed`；失败恢复 MUST 从 `failed_from` 所指阶段继续。只要曾到达 summarized，任何恢复、未知提交结果或并发执行 MUST 只核验已固化摘要并继续删除，不得读取残余 snapshot items 重新生成摘要。

#### Scenario: summarize 提交前或提交结果未知时崩溃
- **WHEN** Control 在摘要事务提交前崩溃，或未收到 commit 结果后重启
- **THEN** 未提交时回到 pending 安全重做；已提交时识别 summarized 和唯一摘要并继续，最终只存在一份结果

#### Scenario: summarized 后只剩部分源行
- **WHEN** 删除已提交一部分后 Worker 崩溃并由另一个执行循环恢复
- **THEN** 恢复只从已固化 source/deleted 计数继续扫描剩余行，不用残余行缩小摘要

#### Scenario: 非法状态跳转
- **WHEN** 调用尝试从 pending 直接 completed、从 completed 回退或改变 summarized 的源校验
- **THEN** PostgreSQL 受控函数拒绝且摘要、源行和状态保持不变

### Requirement: 每个历史删除批次 MUST 有界且与实际删除计数原子提交

删除 snapshot items MUST 在短事务内锁定唯一 compaction run、核验状态与压缩键、按稳定主键选择配置上限内的行，并以实际 `DELETE ... RETURNING` 数量增加 `deleted_row_count`。删除和计数 MUST 同事务提交；任务仅在该键 snapshot items 为零且初始可删除源行数等于累计实际删除数时进入 completed。任一不一致 MUST fail closed 且不得继续删除 poll run。

#### Scenario: 删除或计数更新失败
- **WHEN** 数据库在批次 DELETE、RETURNING 计数或 run 更新任一步失败
- **THEN** 整个批次回滚，源行与 deleted count 同时保持原值

#### Scenario: 删除批次提交后崩溃
- **WHEN** 一批删除和计数已经提交但 Control 在处理响应前崩溃
- **THEN** 恢复后从剩余稳定主键继续，不重复累计已删除行

#### Scenario: 完成校验不一致
- **WHEN** 源计数、累计删除数、剩余行或确定性校验不能同时满足完成条件
- **THEN** run 进入固定 failed 状态并保留剩余证据，不发布错误完成状态或删除关联 poll run

### Requirement: 最终日级 rollup MUST 等待全部预期分段并只消费不可变摘要

`account_inventory_daily_rollup_runs` SHALL以`(summary_date, instance_id)`唯一保存`pending|completed|failed`、预期/完成分段数、分段校验、固定错误和阶段时间。只有该Node/日全部去重后的预期compaction runs completed，Control才能从不可变分段摘要生成最终账号与Provider rollup；最终rows、分段计数/校验与run completed MUST同事务提交，completed后MUST NOT被覆盖。账号rollup以`(date, instance, account_key)`唯一，合并计数和首末时间；末次字段先按最新`last_scheduled_at`、再按固定唯一键决定，final reset count为各段reset之和加相邻segment边界累计计数下降次数。Provider rollup以`(date, instance, provider)`唯一逐项求和并按`sum(promotion_applied_count) / sum(expected_poll_count)`计算覆盖率，初始`>=0.95`为complete，否则为partial。Segment/final rows只有满足history retention全部前置条件的固定受控函数MAY有界DELETE，UPDATE/TRUNCATE始终禁止。

#### Scenario: 预期分段缺失或失败
- **WHEN** 任一预期 compaction key 不存在、未完成或 failed
- **THEN** daily rollup 不发布 completed 最终 rows，普通指标也不把该日视为最新完成日期

#### Scenario: 日内多个策略分段完成
- **WHEN** Provider 在同一日跨策略版本或重新加入且全部分段 completed
- **THEN** 最终表只生成一条该 Node/Provider 日记录，计数求和且不暴露策略版本重复样本

#### Scenario: 最终事务失败或 partial
- **WHEN** 最终账号/Provider rows 或 run 完成写入失败，或覆盖率低于 95%
- **THEN** 失败不留下部分 completed rollup；成功但不足的日期固定标记 partial，不补零、不外推且不得进入正常趋势比较

### Requirement: 历史保留清理 MUST 按依赖顺序且保护当前真相

全量snapshot items MUST等到所属UTC `day_end <= database_now-72h`且compaction完成后才能删除。Poll run只有`scheduled_at <= database_now-30 days`才到期；策略segment/final rollup只有其UTC `day_end <= database_now-30 days`且对应rollup `completed_at <= database_now-30 days`才到期；completed run只有自身`completed_at <= database_now-30 days`且依赖已清空才到期。这些72小时、30天和95%值在本change固定，不由运行配置追溯重解释。Control MUST先确认compaction completed和对应snapshot items为空，才能有界删除到期poll；其历史子表SHALL受控级联删除，Provider state与current account来源外键SHALL `ON DELETE SET NULL`，冗余observed/version/commit、基础状态和lifecycle MUST保持不变。删除顺序MUST为poll及子证据、segment/final rows、completed rollup run、completed compaction run，每类使用独立精确retention gate；UPDATE/TRUNCATE始终禁止。Completed rollup-run retention MUST在确认同日poll及四类segment/final rows全部为空后，于删除run的同一事务先创建唯一、不可变的`(summary_date, instance_id)` retired-day cutoff。该marker MUST在后续compaction-run分批删除及全部删除后持续存在、阻止planner复活，并使任何写入该UTC日的晚到poll失败；本cleaner MUST NOT删除marker。pending、summarized、deleting、failed或校验异常任务MUST NOT自动清理。本capability MUST NOT自动删除current account、missing/out-of-scope lifecycle、认证审计或告警历史；没有retained completed rollup时coverage指标MUST省略而非永久导出已过期值。

#### Scenario: 未完成压缩或未到期历史
- **WHEN** cleanup 遇到未 completed 压缩键、仍有 snapshot items、失败任务或未达到对应保留期
- **THEN** 数据库拒绝删除并保留全部源证据与任务状态

#### Scenario: 到期 poll 受控级联删除
- **WHEN** poll 已超过 30 天、所属键 completed 且 snapshot items 已为空
- **THEN** poll 与历史 Provider/duplicate 子项按有界批次删除，current Provider/account 外键可置空但当前字段逐项保持不变

#### Scenario: 已完成任务和摘要到期
- **WHEN** completed run 和其摘要超过 30 天且不存在未完成依赖
- **THEN** cleanup 按固定依赖顺序有界清理，rollup-run删除事务先持久化retired-day cutoff，随后删除compaction runs时不会复活lineage，且不影响更晚摘要、当前状态或任何失败证据

#### Scenario: 晚到 poll 不能重开 retired day
- **WHEN** rollup-run retention已为某Node/UTC日持久化retired-day cutoff后尝试插入该日poll
- **THEN** 数据库拒绝poll写入且marker、已完成清理和planner负向真相保持不变

### Requirement: history runner MUST 在并发、重启和 PostgreSQL 故障后安全恢复

History scheduler/worker/reconciler SHALL以PostgreSQL时间、唯一键、lease/fencing、行锁和受控状态函数为唯一真相；同一Control内并发worker、重复调度和旧执行者MUST产生等价于单一执行者的结果。本capability不支持多个Control副本共享数据库，不引入选主或分布式协调。runner默认关闭，配置MUST只对扫描间隔、进程内并发、删除批量和timeout设置安全上下限。PostgreSQL不可用时runner MUST有界退避且不得建立内存真相；停止时停止新认领并只让当前短事务完成。Redis、普通durable-job registry、Node或Gateway不得成为history正确性依赖。

#### Scenario: 两个 scheduler 或 worker 竞争同一键
- **WHEN** 并发执行者同时创建、汇总、删除或 rollup 同一 Node/日/策略键
- **THEN** 唯一约束和行锁使每个状态转换与删除计数只生效一次，最终摘要和 rollup 与串行执行一致

#### Scenario: PostgreSQL 中断并恢复
- **WHEN** runner 运行期间数据库断开、statement timeout 或连接池耗尽
- **THEN** runner 不删除无持久状态归属的数据；数据库恢复后从持久状态继续且不需要重采 Node

#### Scenario: runner 或 Control 停止
- **WHEN** history runner、Control 或 PostgreSQL 停止并随后重启
- **THEN** 高频历史在未确认完成时继续保留，恢复后从合法阶段继续，现有账号采集配置和模型数据面不依赖内存 history 状态

### Requirement: Migration 与权限 MUST additive、最小且 fail closed

Forward Migration SHALL创建六张aggregate/run history表和一张durable retired-day cutoff表及版本化约束、索引和受控函数，并把现有snapshot/poll删除保护仅收窄到合法固定函数路径；Migration DDL MUST NOT回填摘要、生成run/retired marker、复制身份、改写poll/promotion/current lifecycle或自动删除数据。Runner启用后MUST按前述source poll或既有lineage bootstrap规则处理升级前仍完整且eligible的历史，不完整历史MUST fail closed。运行时角色MUST只有固定history函数EXECUTE，对七张history表和snapshot/identity表无任意表级访问或DML/TRUNCATE；既有poll/provider-result最小只读权限和受控函数保持兼容。其他角色和产品API MUST无权启动、重建或删除历史或伪造retired marker。schema compatibility未通过时runner MUST fail closed，既有poll/lifecycle/current query继续按旧能力运行。

#### Scenario: 带既有数据升级
- **WHEN** Migration 8 数据库包含 poll、snapshot、Provider state、lifecycle、query audit 后执行新 Migration
- **THEN** 既有行与字段指纹保持一致，七张history表为空且未删除或回填任何身份/摘要/run/retired marker

#### Scenario: 权限绕过或旧应用运行
- **WHEN** runtime 直接访问 history/snapshot/poll 表、调用错误状态函数，或旧二进制运行在 forward schema
- **THEN** history表任意访问与snapshot/poll直接DML被拒绝，既有poll受限读取/函数保持；旧二进制仍可poll、promotion和current query，且不会启动或误删history

#### Scenario: 受保护 down
- **WHEN** 数据库已有 history row、retired-day cutoff、摘要、删除进度或后续依赖时尝试 down
- **THEN** down 拒绝破坏性回退并保留全部数据；生产 Runbook 继续禁止执行 down

### Requirement: coverage 与 compaction 观测 MUST 低基数且不泄露身份

Control SHALL 只从每个 Node/Provider 最近一个 completed 最终 rollup 导出 daily coverage ratio/complete，并导出 oldest unfinished age 与按固定 failed-from 分类的 compaction failure count。标签 MUST 只使用受控 instance、provider 和固定状态/阶段；日期、策略版本、poll/compaction/rollup ID、account key、email、校验值、endpoint、Secret 和原始错误 MUST NOT 成为标签、普通日志、错误或验收 artifact。策略分段 MUST NOT 直接导出为与最终 rollup 重复的指标样本。

#### Scenario: 同日多策略版本导出覆盖率
- **WHEN** 同一 Provider 的多个策略分段形成一个 completed final rollup
- **THEN** Prometheus 仅看到一组该 instance/provider 的最终覆盖率序列且不含日期或策略标签

#### Scenario: 敏感 canary 贯穿成功和失败路径
- **WHEN** 测试向 email/account key、策略/run identity、Secret、endpoint与原始错误注入唯一 canary
- **THEN** 只有受保护的摘要身份列可以包含 account identity，指标、日志、错误、审计 details 和最终 artifact 不包含 canary

### Requirement: history状态转换与清理 MUST 写入不可变系统审计

每次compaction summarized/completed/failed、daily rollup completed/failed以及每批retention success/failure MUST与对应状态/删除事务原子写入`audit_logs`。事件actor MUST为空且使用固定history category/action/result；details只允许instance、summary date、固定phase和实际summary/delete row count，MUST NOT包含email、account key、policy/poll/run/fencing ID、checksum、Secret、endpoint或原始错误。History审计MUST遵循既有不可变保护和初始180天保留，本change不得清理它。

#### Scenario: 删除提交与审计原子性
- **WHEN** retention批次删除或对应audit insert任一步失败
- **THEN** 删除、实际计数、run状态和系统审计全部回滚，不出现已删未审计或有审计未删除

#### Scenario: 自动任务不伪造管理员
- **WHEN** scheduler/worker完成或失败history阶段
- **THEN** 审计以actor-null固定system action记录且details通过allowlist，不关联或猜测任何super_admin

### Requirement: history capability MUST 保持产品只读与数据面隔离

History compaction MUST 只消费 Control PostgreSQL 已提交的 poll、snapshot、activation 与 current-source 数据，不调用 Node、Gateway、Prometheus、模型数据面、互联网或任意外部目标，不改变 poll/promotion/current lifecycle 语义。本 change MUST NOT 增加历史 OpenAPI/UI、导出、人工重建/清理、告警路由、HMAC 逐账号指标或跨 Node 检测；后续趋势与告警只能消费 completed 最终 rollup，不得重新解释已清理的高频历史。

#### Scenario: current query 与历史删除并发
- **WHEN** 管理员当前账号 query、poll promotion 和 history 删除同时运行
- **THEN** query/lifecycle 只观察已提交 current truth，history 不改变账号状态、触发额外 Node 请求或暴露产品历史入口

#### Scenario: Control 历史依赖故障
- **WHEN** runner 关闭、压缩失败或 Control/PostgreSQL 停止
- **THEN** 只暂停历史收敛和新覆盖率发布，Gateway 与 Relay Node 已有模型流量继续且历史源证据优先保留
