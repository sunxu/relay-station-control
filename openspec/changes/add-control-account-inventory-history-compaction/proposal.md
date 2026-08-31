## Why

阶段 3 已经形成每五分钟的不可变账号快照、Provider 当前来源、账号生命周期和审计化当前查询，但高频 `snapshot items`、poll/provider result 与节点内重复证据仍会无限增长，也没有可供覆盖率和后续趋势/告警消费的唯一日级历史真相。现在需要先用可崩溃恢复、先汇总后删除的受控状态机固化历史，避免数据库膨胀，并确保策略切换、漏采和部分删除不会把历史统计永久缩小或伪装为完整。

## What Changes

- 新增按 `(UTC date, Node, provider_policy_version)` 唯一的历史压缩状态机，只处理 `UTC day_end <= PostgreSQL now - 72h` 的完整自然日；账号snapshot摘要按UTC `observed_at`归日，Provider覆盖率/abandoned按固定`scheduled_at`槽归日，二者跨日不一致时fail closed并保留源数据。正常运行时全量快照保留72～96小时，任务失败时延长保留且不得继续破坏性清理。
- 新增不可变的账号与 Provider 策略分段摘要，保存确定性源计数/校验、基础状态样本、Provider 计划槽、传输/契约/快照/promotion/degraded/abandoned 计数和覆盖率；激活区间存在但没有数据时仍生成零数据分段，显式保留覆盖缺口。
- 新增按 `(UTC date, Node)` 唯一的日级 rollup 状态机；只有该日全部预期策略分段压缩完成后，才原子生成不含策略版本的账号与 Provider 最终日级汇总。`coverage_ratio = promotion_applied / expected_poll_count`，初始阈值 95%，不足时固定为 `partial`，不得进入正常趋势比较。
- Planner正常枚举限定在`day_end > database_now - 30 days`的retained horizon；更旧日期只有仍存在同日source poll或既有compaction lineage时才bootstrap，使升级前完整历史和晚完成lineage继续走相同证明链。新增按`(UTC date, Node)`唯一且不可变的durable retired-day cutoff：completed rollup run清理在同一事务写入marker后删除run，planner永久排除marker日且poll写入拒绝晚到同日证据，避免后续分批删除compaction runs时复活已退休lineage。
- 将 snapshot 的“任意删除不可变”边界收窄为“仅受控 compactor 可删除已固化摘要对应的历史行”：`summarized` 后禁止重新聚合或覆盖摘要，只能按主键有界分批删除；每批删除和实际删除计数同事务提交，计数或校验不一致时 fail closed。
- 增加到期 poll run 的受控清理：仅在对应压缩键 completed、snapshot items 已为空且 poll run 达到初始 30 天保留期后按有界批次删除；provider results、snapshot items、duplicates 使用受控级联清理，Provider/current account 来源外键置空但冗余来源元数据保持可解释。
- 在 `account_inventory_provider_states` 冗余 current query 所需的最近 Provider degraded 状态/固定原因；Migration 从仍存在的 current poll source 安全回填非身份健康字段。之后只有仍属当前active策略、scheduled slot严格较新、非`policy_changed|stale_poll|out_of_scope`且已有current state的finalized Provider结果才能原子刷新健康字段；从未promotion的Provider不由本change创建空current state。只读查询不再要求历史poll/provider-result永久存在，来源外键被合法清理后仍返回相同产品字段语义。
- 初始保留策略固定为：全量 snapshot items 至少 72 小时，poll runs/Provider results/duplicates 30 天，策略分段摘要、最终日级汇总和 completed compaction/rollup runs 30 天；failed、pending、summarized 或 deleting 任务不得被自动清理。本 change 不自动删除 current `account_inventory`、missing/out-of-scope 账号、认证审计或未来告警历史。
- 新增 PostgreSQL 专用表、约束、受控 `SECURITY DEFINER` 函数、sqlc/Store adapter、进程内 scheduler/worker/reconciler 和有界配置；PostgreSQL 时间、唯一键、行锁与状态转换是唯一真相，不使用 Redis 或普通 `async_jobs` 代替专用状态机。
- 新增低基数最终日级覆盖率和压缩健康指标；Prometheus只读取retained completed最终rollup，partial仅以`complete=false`表示健康缺口。本change不提供趋势读取；未来正常趋势reader必须同时限定rollup run completed且coverage complete。指标不导出日期、策略版本、run ID、email或account key，策略分段仅供受保护的内部Store、验收和排障使用。
- 每次compaction、rollup和retention成功/失败都原子写入actor-null、不可变、初始保留180天的系统审计；审计details只允许instance、summary date、固定phase和实际聚合/删除行数，不保存account identity、policy/poll/run ID、checksum或原始错误。
- 新增 history compaction Runbook 与 PostgreSQL 18 验收，覆盖策略/监控区间交集、零数据分段、崩溃恢复、并发 runner、分批删除、30 天清理、1/10/50 Node 与 1,000 合成账号容量、旧应用 forward-schema 兼容和数据面隔离。
- 不新增历史 OpenAPI、React 趋势/详情/导出页面、账号或 Provider 告警路由、HMAC `account_id`/逐账号指标、人工重建/删除入口、当前账号生命周期清理、跨 Node 重复归属、Gateway/Node 写操作或任何新外部请求。

## Capabilities

### New Capabilities

- `account-inventory-history-compaction`: 定义完整 UTC 日的策略分段压缩、不可变摘要、最终日级 rollup、Provider 覆盖率、正常horizon与旧source/lineage bootstrap、durable retired-day cutoff、可恢复分批清理、保留期和低基数观测行为。

### Modified Capabilities

- `account-inventory-snapshot`: 将 snapshot items 的绝对删除保护扩展为只允许摘要已固化、状态受保护且可恢复的历史 compactor 有界删除，当前 Provider 来源继续可解释。
- `account-inventory-poll-run`: 允许在对应历史压缩完成并满足保留期后受控删除 poll run 及其历史子表，在durable retired-day cutoff存在后拒绝晚到同日poll，同时继续禁止普通运行时任意删除或伪造历史。
- `account-inventory-lifecycle`: 允许历史清理使 current poll 外键置空，同时保持current来源与生命周期可解释；当前生命周期不得被压缩任务重算或删除。
- `account-inventory-readonly-query`: 保持现有 OpenAPI 字段不变，但在 current poll 历史被合法清理后改从 Provider current state 的冗余健康字段读取 degraded/freshness，不把可空来源外键误判为数据库不一致。
- `asset-registry`: 将不可变Provider策略/激活区间和Node监控区间明确作为coverage计划真相，并保护仍被poll、summary、rollup、compaction或保留期引用的历史不被删除或追溯改写。

## Impact

- **阶段与结果**：阶段 3；Control 首次把高频账号证据安全压缩为唯一、可审计的日级历史，并能区分完整日期与覆盖缺口，数据库历史增长从无界转为受保留策略控制，为后续趋势和告警 change 提供稳定真相源。
- **仓库**：只修改 `control`。`ops` 系统设计 v1.0 第 8.2、9.4、12、13.4、16、21、23、24.1～24.3、25 节和 ADR-0001 是输入真相源；不修改 `ops`、Gateway 或 Node 产品代码。
- **OpenAPI/生成客户端/UI**：不修改产品 OpenAPI schema、生成 Go/TypeScript客户端和React路由；现有 current account query 响应字段保持兼容，只升级数据库函数/Store source 以容忍合法空来源外键，不自动暴露历史摘要或内部压缩任务。`make generate` 必须零差异。
- **Migration/sqlc**：新增下一号additive forward Goose Migration，创建六张aggregate/run历史表和一张durable retired-day cutoff表、必要索引、不可变/状态约束和版本化受控函数；调整历史外键/删除trigger以分别只放行受控compaction、poll retention和history retention，扩展audit allowlist，并安全回填current Provider非身份健康字段。Migration DDL不生成summary/run/retired marker或删除数据；runner可用source poll/既有lineage bootstrap处理升级前仍完整且eligible的历史。sqlc只调用受控函数，运行时角色对新history/identity表无任意表级权限并保留既有poll只读权限。
- **运行时/任务**：新增默认关闭的history scheduler/worker/reconciler与有界interval/batch/timeout配置；72小时、30天、95%阈值在本change固定而不由运行配置重解释。专用状态机由PostgreSQL lease/fencing、行锁、唯一键和短事务恢复，不改变durable-job的空production executor registry，也不依赖Redis。
- **指标/审计/日志**：增加仅含固定状态、阶段、instance/provider的低基数coverage/compaction指标；自动状态机写actor-null系统审计而不伪造管理员，审计与对应summary/rollup/删除状态同事务且按既有审计策略保留180天。email、account key、policy/poll/run ID、checksum、endpoint、Secret、原始错误和响应不得进入普通日志、指标标签、审计details或验收artifact；summary date只允许进入受保护summary/run与系统审计。
- **兼容性与数据面**：Migration 与 runner 均为 additive/默认关闭；新应用在 schema compatibility gate 通过后才启动 runner。压缩只读取 Control PostgreSQL 已提交历史，不请求 Node/Gateway/互联网，不改变 poll、promotion、current lifecycle 或 Gateway/Relay Node 模型流量。
- **安全与回滚**：部署顺序为 Migration 后新二进制，再显式启用 runner。应用回滚停止新压缩但保留摘要、任务、retired-day cutoff、已完成删除计数和 forward schema；旧应用必须继续采集和查询当前状态。生产不执行 destructive down；任何retired marker也使受保护down拒绝，未完成或校验异常的历史保持保留并由 Runbook 处置。
