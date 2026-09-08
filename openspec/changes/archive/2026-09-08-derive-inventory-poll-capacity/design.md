## Context

动机见 proposal.md。改动前 `cmd/control/account_inventory_poll.go` 将旧变量同时填入 MaxMonitoredNodes 与 ScheduleLimit；`internal/inventorypoll/config.go` 校验时间预算及50/10特例；Migration 5 的 scheduler 在 eligible_count 大于 schedule_limit 时抛22023，旧Go scheduler将错误统一报告database_unavailable。页面未显示该原因。

## Goals / Non-Goals

**Goals:** 一个推导函数作为容量唯一来源；真实数据库eligibility；管理员能判断当前超限和恢复。

**Non-Goals:** 不取消50硬上限、不引入公平队列/优先级/自动扩容/自动禁用监控；不改变5分钟周期、retry、grace/lease、账号freshness、Duplicate eligibility、Gateway或Node。已独立提交的URL预选bugfix不属于本change。

## Decisions

### Capacity

用1..50有界整数枚举，选择满足 `(ceil(N/C)-1)*(T+F+L)+N*Q+M<G` 的最大N，避免浮点、向下取整和等号错误。C/T/M/G继续沿用已有配置及验证边界，lease覆盖请求和finalize的校验不变。Worker的semaphore仍持有到execute返回；claim调用增加1秒有界context，生命周期回调增加30秒有界context。F/L计入每批额度占用，N*Q保守覆盖串行claim。投影CPU及唤醒开销由M覆盖；阻塞外部操作必须遵守context。删除独立MaxMonitoredNodes输入；内部ScheduleLimit必须来自推导结果，测试也不能独立配置两者。默认时间预算下 C1=2、C2=4、C6=12、C7=14、C10=20、C25=50。并发不是实际Node数，不随每次计数自动改变。

明确替换50/10特例：完整占用预算取代原仅HTTP预算，不再把50/C7当作容量承诺。50/C25在两批下前批完整执行预算55秒，50次串行claim预算50秒，加10秒dispatch余量共115秒，小于120秒；该预算是及时起槽且下游遵守超时条件下首attempt准入预算；数据库故障、启动延迟和同槽retry不承诺全部完成。所有attempt仍受数据库剩余grace和实际semaphore约束，不能越界dispatch或生成重复终态。必须用真实PostgreSQL和有界慢Driver专项验证dispatch、lease和恢复，不以算式代替验收。备选固定50/C10要求小环境扩大并发；无界容量需要另做调度架构，均不采用。

### Read model and security

拟新增 `GET /api/account-inventory/poll-capacity`，沿用管理员session鉴权；只读不触发账号查询、无邮箱，无新增业务审计事件。200字段：status、enabled、eligible_node_count、effective_capacity、concurrency、request_timeout_ms、finalize_timeout_ms、lifecycle_timeout_ms、claim_timeout_ms、dispatch_margin_ms、poll_start_grace_ms、evaluated_slot、evaluated_at；UTC RFC3339时间，计数为有界非负整数。status为ready/capacity_exceeded/disabled，disabled优先；无可靠数据库读取则503，包括关闭时，不能伪造计数。响应禁止缓存；页面加载及明确刷新读取，失败隐藏旧正常状态并显示不可用。只表示处理请求的Control进程配置，同环境所有实例必须同配置。

计数来源复用Migration5完整eligibility条件：资产能力、当前UTC槽monitoring activation、driver匹配、Provider策略激活/绑定且active providers非空；不能简单count资产或从account rows推导。安全函数必须首先执行scheduler同一inconsistent_count检查，策略缺失/不匹配等错误返回503而不是通过inner join隐去Node后返回ready。一条SQL/单次数据库时间快照取得slot、count和evaluated_at；结果可能随后被并发注册改变，最终调度仍以事务内计数权威裁决。

现有安全读取未提供完整调度前置检查与同槽聚合，采用一条additive readonly query-access migration创建固定search_path、STABLE、SECURITY DEFINER函数，owner migrator，撤销PUBLIC，仅runtime EXECUTE。无新表/列/持久状态，无扩大runtime直接SELECT；Down仅删除新增函数。现有scheduler签名、不可变migration和history不改。诊断与调度eligibility用一致性测试防止复制漂移。

### Error and recovery

Store将精确已知的capacity异常映射为固定domain错误，不把所有22023当容量超限；可用精确SQLSTATE加固定已知message识别，日志/API不转发SQL原文。其余DB错误仍unavailable。现有有界退避继续，容量恢复后下一正常槽可调度；不重启、不改数据库时间、不产生补采。现有running任务照常finalize。

核查当前无scheduler结果计数器，因此沿用现有scheduler结构化事件日志增加固定capacity_exceeded reason，并保留现有scheduler lag/容量指标；不为本change新建计数器。禁止Node ID/账号/环境变量值标签。页面显示“监控Node 3，当前容量2，整轮新采集暂停”，附预算信息及手工调整建议。Provider freshness/health不合并为容量状态。当前诊断ready不声称最近poll已成功。

## Risks / Trade-offs

- 超过推导容量仍整轮停采 → 显式诊断和固定错误分类；改变准入或公平调度留待独立change。
- 配置忽略旧值可能扩大容量 → 固定弃用警告和发布说明；并发上限保持原值，不自动增加并发。
- 完整额度预算仍存在进程调度抖动 → 保留10秒dispatch余量并强制慢claim/finalize/observer容量与恢复专项门禁；失败则停止提交并重新评审，不放宽断言。
- 多实例配置不一致 → 发布要求统一配置，不引入分布式配置truth；诊断不冒充跨实例全局执行状态。

## Migration Plan

先完成Control代码、生成client、专项和make test build；Ops独立关联提交清理base Compose及持久override旧变量，不改变其它运行配置。部署前备份当前配置，记录推导容量与eligible计数；默认C10容量从旧人工50变为20，21..50 Node环境必须先调整并发，否则不得进入部署；若需query-access函数先前向迁移，再部署backend和Web同版本。回滚镜像时恢复明确且符合旧容量规则的MAX_NODES/并发配置，保留数据和additive函数，不执行production down。无Gateway/Node发布。实施和隔离测试允许query-access migration；不对本地部署环境应用迁移或部署。
