# account-inventory-poll-run Specification

## Purpose
定义 Control 账号清单采集的 UTC 固定时间槽、监控资格、不可变策略版本、有限并发、租约恢复和无逐账号数据的聚合持久证据。

## Requirements

### Requirement: Control SHALL 只为符合资格的 Node 创建唯一 UTC 固定槽

Control SHALL 使用 PostgreSQL UTC 时间计算五分钟固定 `scheduled_at`，并 MUST 只为该时间点处于显式账号监控激活区间、声明 `management_account_inventory_read` 且存在匹配 Node type/Driver contract 有效 Provider 策略的 Node 创建 poll run。每个 `(instance_id, scheduled_at)` MUST 最多一行，首次创建后 `provider_policy_version` MUST 不可改变。

#### Scenario: 当前槽首次调度
- **WHEN** 数据库时间进入一个五分钟槽，Node 的 capability、监控区间和 Provider 策略在该 `scheduled_at` 均有效
- **THEN** Control 创建一条 `status=pending` 的 poll run，并固定该槽对应的不可变策略版本

#### Scenario: 重复 tick 或重启后调度同一槽
- **WHEN** Scheduler 对同一 Node 和 `scheduled_at` 重复运行，或 Control 在当前槽内重启
- **THEN** Control 复用原 poll run，不创建第二行、不重置时间/attempt，也不把策略版本替换为当前新版本

#### Scenario: Node 不在监控区间或缺少能力
- **WHEN** `scheduled_at` 不属于 Node 监控激活区间，或 Node 未声明账号清单 capability
- **THEN** Control 不创建 poll run、不调用 Driver，且该槽不因 Gateway/Compose 状态被隐式纳入监控

#### Scenario: 资产或策略真相不一致
- **WHEN** capability/Driver contract 不匹配、策略激活重叠或 binding 与激活版本矛盾
- **THEN** Scheduler fail closed 并记录固定分类，不猜测策略、不自动修复数据，也不调用 Node

### Requirement: Control MUST 不回填已过启动宽限的历史槽

Control MUST 以数据库时间执行初始 120 秒 `poll_start_grace`。Scheduler MUST NOT 为过期的空槽调用当前 Node 接口补造历史；已存在但未 dispatch 的 pending/retry_wait 或无法及时恢复的 running MUST 进入 `abandoned`，且 abandoned run MUST NOT 包含伪造 Node 观察。

#### Scenario: Control 在历史槽之后恢复
- **WHEN** Control 或 PostgreSQL 恢复时一个过去槽已超过 `scheduled_at + poll_start_grace`
- **THEN** Control 不为该槽请求 `/v0/management/auth-files`，未创建槽保持缺口，已创建未完成 run 只转为 abandoned

#### Scenario: pending 在队列中超过宽限
- **WHEN** pending run 因容量或停机从未认领且宽限已过
- **THEN** Reconciler 将其置为 abandoned，不填 transport/contract/mode/provider 结果，也不把下一槽响应关联到该槽

#### Scenario: 下一有效槽到来
- **WHEN** 一个槽 abandoned 后下一五分钟槽进入有效宽限且 Node 仍符合监控资格
- **THEN** Control 只为新槽创建并执行新 poll run，不重开旧终态 run

### Requirement: Worker SHALL 先取得并发额度再认领并立即调用 Driver

Worker SHALL 在认领数据库记录前取得有界 HTTP 并发额度。认领 MUST 使用短事务、`FOR UPDATE SKIP LOCKED`、数据库时间、随机 fencing token 和执行 lease，原子设置 running、增加 attempt 并返回剩余 grace；提交后 MUST 不再排入其他队列，并以剩余 grace 与请求总超时的较小值立即调用固定 Node Driver。

#### Scenario: HTTP 并发额度已满
- **WHEN** 所有 poll 并发额度正在使用
- **THEN** 其他 poll run 保持 pending 且不开始 lease，释放额度后才允许认领

#### Scenario: 两个 Worker 竞争同一 poll run
- **WHEN** 两个 Worker 同时尝试认领同一 pending/retry_wait run
- **THEN** 只有一个 Worker 获得 running lease/fencing 并增加 attempt，另一个跳过该行且不调用 Node

#### Scenario: 认领后预处理耗尽 grace
- **WHEN** Secret/DNS 等 Driver 预处理尚未真正发出 HTTP 时，认领返回的剩余 grace 已耗尽
- **THEN** context 阻止 HTTP dispatch，Control 不越过宽限请求 Node，并按未完成 Control 执行恢复或 abandoned

#### Scenario: 容量配置危险
- **WHEN** 最大 Node 数、并发、15 秒最坏请求时长、120 秒 grace、30 秒 lease 和调度余量不能满足容量公式，或 50 Node 配置的并发低于 10
- **THEN** poll service 在任何 Node 请求前拒绝启动并暴露固定配置错误，Control 数据面隔离保持不变

### Requirement: poll run SHALL 使用有界 lease、fencing 和恢复尝试

Control SHALL 只允许持有当前未过期 lease 与 fencing token 的 Worker finalize。Reconciler MUST 使用数据库时间处理过期 running：宽限内且 attempt 未耗尽时复用同一 run 进入 retry_wait，过 grace 或耗尽 attempt 时进入 abandoned。finalized/abandoned MUST 为不可重开的终态。

#### Scenario: 旧 Worker 在 lease 丢失后回写
- **WHEN** 旧 Worker 的 lease 已过期或 fencing 已被恢复者替换后尝试 finalize
- **THEN** fenced 更新影响零行，旧 Worker 丢弃内存结果且不能覆盖新执行或终态

#### Scenario: running 在宽限内过期
- **WHEN** Control 崩溃使 running lease 过期，但数据库时间仍在本槽 grace 内且 attempt 未耗尽
- **THEN** Reconciler 将原 run 转为 retry_wait，下一 Worker 最多按剩余 attempt 再执行一次固定只读 GET

#### Scenario: running 在宽限外过期
- **WHEN** lease 过期时本槽 grace 已结束
- **THEN** Reconciler 将原 run 置为 abandoned，不再次调用 Node、不保存旧 Worker 的迟到观察

#### Scenario: 终态被再次调度或人工重试
- **WHEN** Scheduler、Worker 或非授权操作尝试离开 finalized/abandoned
- **THEN** 数据库状态约束/最小权限拒绝转换，且不产生 Node 请求

### Requirement: Node 失败 MUST 作为 finalized 观察而非隐藏重试

Control MUST 将 Driver 已返回的 transport、HTTP、contract、mode 和 identity 结果作为该槽观察。Node 调用失败或非 200 MUST 保存 `transport_success=false`、`contract_valid=false` 并 finalized；HTTP 200 但响应契约无效 MUST 保存 transport 成功、contract 失败并 finalized。poll service MUST NOT 在同槽自动重试已形成的 Node 失败观察。任何 transport/contract 失败、disk fallback 或 Provider 不完整 MUST 同时 `promotion_applied=false`，不得写 snapshot items或更新 Provider 当前指针。

#### Scenario: HTTP 非 200 或网络失败
- **WHEN** Driver 返回固定 transport 失败观察且 Control 可提交数据库
- **THEN** poll run 以 `transport_success=false`、`contract_valid=false` finalized，全部 Provider 不提升且同槽不因该失败进入 retry_wait

#### Scenario: HTTP 200 但契约无效
- **WHEN** auth-files 响应 JSON/shape/mode 无效或超过既有限制
- **THEN** poll run 以 transport 成功、contract 失败、空 inventory mode finalized，不保存原始 body、部分账号或 snapshot items

#### Scenario: disk fallback 或 Provider identity 不完整
- **WHEN** Driver 返回 contract-valid disk fallback、缺 provider、缺 email或节点内重复分类
- **THEN** poll run 和固定 active Provider 的完整性/degraded 聚合结果 finalized，受影响 Provider promotion false且旧当前指针不变

#### Scenario: finalize 事务失败
- **WHEN** Driver 已返回但数据库在 provider results、duplicates、snapshot items、Provider pointer 与 poll result 完整提交前失败
- **THEN** 整个 finalize 回滚，run 保持可由 lease 恢复的非终态，数据库中不存在部分 finalized 或部分 promotion 证据

### Requirement: finalize MUST 原子保存固定策略的完整聚合结果

Control SHALL 在一个 fenced PostgreSQL 事务中保存 poll run、其固定策略中全部 active Provider 的聚合结果、节点内重复证据、允许的 Provider snapshot promotion 和对应账号 lifecycle 转换，并 MUST 使用数据库时间生成 `observed_at`。结果集合 MUST 与 pinned policy 完全相等。事务 MUST 锁定当前 policy binding：版本变化时只保存采集证据并以 `policy_changed` 跳过所有 promotion/lifecycle；版本未变化时只为完整 runtime Provider 原子写 snapshot items、更新其当前指针、推进 lifecycle 并设置 `promotion_applied=true`。任一检查/写入失败 MUST 整体回滚。

#### Scenario: 多 Provider 中一个不完整
- **WHEN** contract-valid runtime 观察中一个 active Provider 缓存缺 email/重复，而另一个 active Provider 完整
- **THEN** finalized provider rows 只将问题 Provider 标为不完整/degraded且不提升或推进 lifecycle，完整 Provider 保存快照、更新当前指针、推进 lifecycle并 promotion applied

#### Scenario: active Provider 返回零记录
- **WHEN** 合法 runtime 观察对某 active Provider 完整且返回零记录
- **THEN** finalize 为该 Provider 保存完整空范围聚合结果、零条 item，并原子推进其当前指针、现有 active 账号缺失状态与 promotion applied

#### Scenario: poll 创建后策略切换
- **WHEN** poll run 固定旧策略后当前 binding 已切换到新版本
- **THEN** 本轮 Driver 解析和 provider rows 仍使用旧版本，采集证据 finalized但全部 promotion 以 `policy_changed` 跳过，不按新策略重解释旧响应或推进 lifecycle

#### Scenario: 敏感或逐账号数据进入持久化路径
- **WHEN** Driver observation 包含账号 DTO、email、endpoint、Secret 元数据或原始错误上下文
- **THEN** poll 表只保存固定枚举与聚合值；只有合法完整 Provider 的标准化 email/account key 和字段白名单进入受保护 snapshot/duplicate/lifecycle 表，其他内容不落库

### Requirement: PostgreSQL SHALL 强制 poll-run 状态与时间不变量

PostgreSQL SHALL 强制五分钟 `scheduled_at`、唯一 Node/槽、封闭状态、attempt/lease/finalized/abandoned 字段组合、Provider 唯一性、promotion 字段组合和终态不可逆。`promotion_applied=true` MUST 只属于 finalized、contract-valid、runtime、snapshot-complete Provider，并与 snapshot items/Provider 当前指针在同一受控 finalize 中形成。运行时角色 MUST 只拥有调度、认领、恢复、finalize 和只读指标所需最小权限，不得直接删除历史或绕过状态转换。

#### Scenario: 非固定槽或重复 Node/槽写入
- **WHEN** 写入未对齐五分钟的 `scheduled_at` 或第二条相同 `(instance_id, scheduled_at)`
- **THEN** 数据库拒绝非法时间，重复调度只通过幂等路径取得既有 run

#### Scenario: abandoned 伪造 Node 结果
- **WHEN** 写入尝试为 abandoned run 设置 observed/transport/contract/mode、provider rows、snapshot items 或 promotion applied
- **THEN** 数据库约束/受控 finalize 函数拒绝该状态组合

#### Scenario: finalized Provider 集不完整
- **WHEN** finalize 缺少 pinned active Provider、包含额外/重复 Provider，或 applied 标记与 snapshot/current pointer 不一致
- **THEN** finalize 失败并整体回滚，poll run 不进入 finalized

#### Scenario: 运行时尝试删除或直接改终态
- **WHEN** Control 运行时角色直接 DELETE poll run/snapshot 或 UPDATE finalized/promotion/current pointer
- **THEN** 最小权限和状态保护拒绝操作，历史及当前来源证据保持不变

### Requirement: poll service MUST 在重启和依赖故障后安全恢复

Control MUST 在环境、Migration、Driver registry 和容量校验后启动 poll service。停止时 MUST 先停止新调度/认领并有限收尾；未确认 running 保留 lease。PostgreSQL 不可用时 MUST 有界退避且不调用 Node；恢复后 MUST 依据持久状态和数据库时间 Reconcile，不依赖内存队列。

#### Scenario: Control 在 pending、running 或 finalize 中崩溃
- **WHEN** 进程分别在 poll 创建后、Driver 调用中或 finalize 事务中崩溃并重启
- **THEN** 唯一 poll run/attempt/lease 保留，宽限内按原 run 有界恢复，宽限外 abandoned，且无重复终态或部分 provider 结果

#### Scenario: PostgreSQL 在 Node 可用时中断
- **WHEN** 数据库不可用而 Node 管理接口仍可访问
- **THEN** Control 不脱离数据库创建内存真相、不发起无可持久化归属的请求；数据库恢复后只处理仍有效槽

#### Scenario: Node 或 Secret 恢复
- **WHEN** 某槽的 Node/Secret/DNS 故障已作为 finalized 失败证据，依赖随后恢复
- **THEN** 旧槽保持不变，Control 仅在下一有效固定槽重新观察，不重写失败历史

#### Scenario: Control 或数据库停止
- **WHEN** poll service、Control 或 PostgreSQL 停止
- **THEN** 账号采集暂停，但 Gateway 与 Relay Node 已有模型请求继续，且不存在 Gateway/Node 写操作

### Requirement: poll-run 观测 MUST 有界且不泄露账号或凭证

Control SHALL 暴露固定 poll state、scheduler lag、queue wait、poll start lag、transport、contract、Provider snapshot-complete 和 promotion applied/skipped 指标。标签 MUST 仅使用受控 `instance_id`、状态、Provider 和固定 reason/mode；日志与验收证据 MUST 脱敏。poll-run ID、policy version、email/account key、endpoint/IP、Secret/Management Key、响应内容、版本/提交和原始错误 MUST NOT 成为指标标签或非受控输出。

#### Scenario: 进程重启后导出延迟指标
- **WHEN** Control 在 poll 创建、运行或 promotion 后重启
- **THEN** queue/start/scheduler lag 和 Provider promotion 从 PostgreSQL 持久时间与结果恢复，不从内存重新计时或猜测 applied

#### Scenario: finalized Node 失败影响 scheduler lag
- **WHEN** 最近槽以 transport/contract 失败证据 finalized且 promotion skipped
- **THEN** scheduler lag 将该槽视为调度已完成，具体失败与未提升由 transport/contract/promotion 指标分别表达

#### Scenario: canary 注入全部失败路径
- **WHEN** 测试向 endpoint、Secret、email/account key、原始错误和响应字段注入唯一 canary
- **THEN** 除受保护 snapshot/duplicate 身份列外，数据库其他列、指标、日志、错误、测试报告和 acceptance artifact 均不包含敏感 canary

#### Scenario: 真实 Node 验收请求
- **WHEN** 对阶段 0 受控真实测试 Node 执行 snapshot/promotion 验收
- **THEN** 管理请求全局串行，成功或失败后以及最后一次请求后均等待至少 10 秒，证据只保留脱敏状态、聚合计数和固定 promotion 分类

### Requirement: 本 foundation SHALL 推进当前账号状态但不实现产品界面

Poll-run 与 snapshot foundation SHALL 在同一 fenced finalize 中保存完整 Provider 的字段白名单快照、节点内重复聚合、Provider 当前来源和 `present|suspected_missing|missing|out_of_scope` 当前生命周期；只有 promotion-applied Provider MAY 改变 lifecycle，其他观察 MUST NOT 推进或清零连续缺失。它们 MUST NOT 实现日级摘要、趋势、压缩、覆盖率或告警，也 MUST NOT 新增产品 OpenAPI/UI 或人工补采/promotion/状态修改入口，并 MUST NOT 修改 Gateway/Node 或调用任何管理写路径。

#### Scenario: poll run 成功且 Provider 完整
- **WHEN** 一轮 runtime 观察中 active Provider 完整、策略未变化并 finalized
- **THEN** Control 原子保存该 Provider 快照、当前指针和 lifecycle 转换，但不计算产品趋势、摘要或告警

#### Scenario: poll run 失败或 Provider 未提升
- **WHEN** transport/contract 失败、disk fallback、Provider 不完整、policy_changed 或 stale_poll finalized
- **THEN** 既有账号 lifecycle 和缺失计数保持原值，且同槽不为状态推进额外请求 Node

#### Scenario: 管理员访问现有 Control 页面
- **WHEN** lifecycle foundation 部署后管理员使用现有 API/UI
- **THEN** 现有契约保持兼容，不出现账号列表、创建、重试、补采、promotion、状态修改或删除入口

#### Scenario: 网络调用范围检查
- **WHEN** Scheduler、Worker、Reconciler、snapshot 和 lifecycle 验收运行
- **THEN** 除固定 Node Driver 账号清单只读 GET 外不调用 Gateway、Node 写接口、模型数据面或任意未登记目标
