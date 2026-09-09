# account-request-quality Specification

## Purpose
在不修改 CLIProxyAPI、且只依赖 HTTP usage queue 的前提下，为 Relay Station 已有账号身份提供最小请求事件存储和短时间窗口质量统计，明确未知数据、错误与重复处理边界，不扩展为配额或自动控制平台。

## Requirements

### Requirement: 唯一 HTTP 请求事件来源

Control SHALL 仅消费 CLIProxy `/v0/management/usage-queue`，批量100、正常间隔1s、有限请求超时、最大退避30s；每个 Node MUST 只有一个消费者，取消 SHALL 终止拉取。不得 fallback 至RESP/Subscribe或修改Node。

#### Scenario: 正常与空队列
- **WHEN** 返回有效对象/JSON字符串数组或空数组
- **THEN** 分别处理批次或等待下一轮，空队列不制造事件

#### Scenario: 非法事件或 unsupported
- **WHEN** payload malformed或endpoint unsupported
- **THEN** 返回明确采集错误而不是空数据，不访问其他source；malformed单项计数后保留同批其它合法事件

#### Scenario: 取消
- **WHEN** runtime context取消
- **THEN** 停止HTTP请求与退避等待，不开始下一次pop

### Requirement: Unproven Account Identity Must Remain Unresolved

Control MUST 正确归属每个能够证明身份的事件（Every provably attributable event is attributed correctly），而非要求每个合法事件都有account_key。直接provider/email沿用既有canonicalization；auth_index只作为当前Node management快照lookup。只有exactly-one不同account_key且没有冲突才resolved，其余持久化account_key=NULL。MUST NOT使用latest/first wins、任意fallback或当前账号覆盖无法证明的历史事件，不新增temporal identity subsystem。

#### Scenario: Direct identity
- **WHEN** event提供足够provider/email且无冲突
- **THEN** 归一化为既有account_key，resolved

#### Scenario: Unique auth_index
- **WHEN** event只有auth_index且当前同Node快照证明exactly-one不同account_key
- **THEN** 关联该account_key；重复相同映射不制造歧义

#### Scenario: Missing lookup
- **WHEN** auth_index缺失或lookup读取失败/不存在且无独立direct identity
- **THEN** 保留事件且account_key=NULL

#### Scenario: Conflicting lookup
- **WHEN** 同auth_index对应多个不同account_key，或当前snapshot与event身份/provider证据冲突
- **THEN** 保留事件且account_key=NULL，不选first/latest

#### Scenario: Deleted account
- **WHEN** account已删除、当前lookup缺失且event只有auth_index
- **THEN** unresolved，不重建历史assignment

#### Scenario: Unresolved account exclusion
- **WHEN** 查询某个account_key质量
- **THEN** NULL身份事件不得出现在该账号计数/失败率/latency中

#### Scenario: Unresolved Node and Provider inclusion
- **WHEN** 查询Node或Provider质量且包含unresolved失败事件
- **THEN** 计入请求/失败/latency及unresolved_request_count，不伪装无请求

### Requirement: 最小事件与固定失败类别

Control SHALL 只持久化event_hash、可选request_id、node_id、provider、account_key、model、occurred_at、duration_ms、success、failure_class，以及可空的Antigravity安全认证子原因auth_failure_reason。失败类别 MUST 限于auth/quota/rate_limit/upstream/unknown；成功类别为NULL。

#### Scenario: 成功与五类失败
- **WHEN** 输入成功、认证错误、明确quota错误、429、5xx或未明确错误
- **THEN** 分别为成功/NULL、auth、quota、rate_limit、upstream、unknown

新增子原因 MUST限于token_invalid/account_blocked/forbidden/other，成功及旧event为NULL；不保存HTTP原文/status_message/Token。只在新Antigravity失败normalization中解析；failure_class仍保留原五类，明确新增认证代码可归auth，其他Provider不变。event_hash、resolved/unresolved、insert-ignore、7天retention及已有Quality/History/Incidents响应不变。历史auth MUST NOT回填为token或blocked。

#### Scenario: Antigravity auth refinement
- **WHEN** 新Antigravity失败含明确token/blocked代码或普通401/403
- **THEN** 在保持五类failure_class兼容的前提下保存安全子原因，普通403为forbidden而不是account_blocked

#### Scenario: Legacy and duplicate compatibility
- **WHEN** 旧writer省略子原因、非Antigravity事件、成功事件或已有hash重放
- **THEN** 旧值保持NULL且不猜测；success为NULL；不修改旧event或hash，不新增第二次计数

安全子原因 MUST只表示事件解析结果，不代表availability已确认状态。普通403保留failure_class=auth与forbidden子原因，原Quality/History/Incidents继续处理；availability仅在fresh runtime error/unavailable旁证下确认FORBIDDEN。事件存储仍按node_id+event_hash幂等，MUST NOT为修复availability去重而改原taxonomy/hash/计数；availability的独立请求计数另按同Node/account非空request_id去重，不假设event_hash或request_id全局唯一。无request_id多个hash不能单独确认账号故障。

#### Scenario: Multiple events for one request
- **WHEN** 多个不同event_hash携带同一个request_id
- **THEN** 原事件存储与质量计数保持原契约；availability只算一份请求证据，retry/replay不能凑数

#### Scenario: Missing request identifier
- **WHEN** 多个失败event均无request_id
- **THEN** 正常保留已有事件/分类，但不能靠hash数量确认availability；一个已分类失败加fresh runtime error/unavailable才可走交叉确认

#### Scenario: Ordinary forbidden remains visible to incidents
- **WHEN** 普通403的runtime仍active，即使有两个不同request_id
- **THEN** Request Quality仍按auth、Incidents仍按原规则聚合；availability为UNKNOWN/pending_confirmation而非FORBIDDEN

### Requirement: 幂等 PostgreSQL 与七天保留

Control MUST 使用PostgreSQL additive append-only事件表，按node_id+event_hash幂等插入；仅七天到期事件可由retention删除，不使用SQLite、partition或rollup。DB写失败 MUST 保留当前内存批次重试而不是继续pop；source已弹出但尚未提交的崩溃窗口不能被描述成可重放。

#### Scenario: 重复与重启
- **WHEN** 相同Node和hash重复插入或collector重启后重放已提交事件
- **THEN** 数据库只保留一条，统计不重复

#### Scenario: 数据库失败
- **WHEN** 批次提交失败
- **THEN** 报错并重试原批次，不将失败当成成功或空队列

#### Scenario: 保留期限
- **WHEN** DB时间表明事件早于7天
- **THEN** 最小retention操作可删除到期事件，窗口内事件保持不变

### Requirement: 单账号窗口质量查询

Account Quality查询 SHALL 按node_id+非NULL account_key返回15m或1h的request_count、success_count、failure_count、success_rate、p95_latency_ms、last_success_at、last_failure_at、last_failure_class；窗口使用DB UTC时间，p95为有效duration的percentile_cont(0.95)。只补Node/Provider同字段聚合与unresolved_request_count，不扩展UI、完整Node quality产品、quota或自动行为。

#### Scenario: 两个窗口与 p95
- **WHEN** 存在窗口内外及不同账号事件
- **THEN** 两个窗口分别只统计目标账号窗口内事件，比例和p95与fixture一致

#### Scenario: 无请求
- **WHEN** 窗口内没有请求
- **THEN** 计数为0、比例和p95及最后事件字段为NULL，语义unknown而非健康

#### Scenario: 查询不可用
- **WHEN** PostgreSQL查询失败
- **THEN** 返回错误，不返回零请求或空成功结果

### Requirement: Account Request History SHALL 受当前Inventory membership约束

Control SHALL仅从已有account_request_quality_events读取目标Node与非NULL account_key的最近7天具体事件。MUST先在既有current Inventory安全read model证明该账号存在，不能从events反推账号；无账号返回404，Inventory/DB故障返回503，禁止伪装empty。MUST NOT修改CLIProxy、collector、usage queue、event schema、retention、taxonomy或质量classification。

#### Scenario: 存在账号有事件或无事件
- **WHEN** Inventory目标账号存在
- **THEN** 有7天事件时返回该账号事件，无事件时成功empty，既有lifecycle集合不被改写

#### Scenario: 假账号与隔离
- **WHEN** 目标只有events而不在Inventory，或目标事件属于其它Node/账号/NULL身份
- **THEN** event-only目标404，其它Node/账号及unresolved事件不进入目标结果

#### Scenario: Gate故障与恢复
- **WHEN** Inventory read或DB失败后恢复
- **THEN** 失败503而非empty；重试只读取恢复后的真实数据

### Requirement: Request History SHALL 使用数据库时间及稳定有界分页

History SHALL固定DB statement timestamp最近7天，包含下边界并排除future，按occurred_at DESC、event_hash DESC keyset分页。GET `/api/topology/nodes/{instance_id}/request-history` SHALL接受required account_key与可选limit/cursor，无日期或其它过滤。limit默认25、最大100；opaque有界cursor MUST绑定Node/account/time/hash，错配或非法参数400，不能使用offset。response SHALL含instance_id/account_key/items/next_cursor，item仅Time/model/success/failure_class/duration_ms/request_id（Time JSON字段occurred_at）；hash仅在cursor内部，不作item字段。

#### Scenario: 七天边界与相同时间
- **WHEN** fixture有七天边界前后、future和相同timestamp不同hash事件
- **THEN** 只返回窗口内事件，按时间降序及hash降序分页，无重复漏行

#### Scenario: Cursor及limit
- **WHEN** cursor Node/account错配或时间/hash字段非法、limit不在1–100
- **THEN** 返回400，不读取其它账号或无界结果

#### Scenario: 单账号一万事件
- **WHEN** 查询10000事件账号的首25条、next page及limit100
- **THEN** 单次DB query、有界结果，复用现有index并记录本地耗时，不增加索引/缓存/rollup

### Requirement: History SHALL 保持只读授权与数据库ACL

API SHALL要求super_admin session、no-store、GET only及有界超时；无session401、非super_admin403。安全函数 SHALL为SECURITY DEFINER STABLE、固定search_path、migrator owner、PUBLIC revoke、runtime EXECUTE only；MUST NOT扩大direct SELECT或新增持久数据。

#### Scenario: 授权与方法
- **WHEN** 无session、非管理员或非GET请求
- **THEN** 401/403/方法拒绝且不执行history读取

#### Scenario: ACL及迁移回滚
- **WHEN** runtime执行function、直接读event表，或隔离执行Down/Up
- **THEN** EXECUTE可用、直接SELECT denied；Down只去掉新function、Up恢复，原表/索引/数据/函数不变

### Requirement: Account Incident SHALL 仅聚合当前账号重复失败

Control SHALL从现有events计算(node_id,account_key,failure_class)的active：DB时间最近15分钟至少3次失败，包含下边界、排除future，四类auth/quota/rate_limit/upstream独立；unknown、unresolved、event-only不生成。SHALL先复用完整current Inventory read model，不能遗漏首101条以后的账号。MUST NOT持久化Incident、改变Inventory/lifecycle/identity或Quality classification。

#### Scenario: 阈值与类别
- **WHEN** 同账号auth有3次失败、另一账号2次，或其它三类各3次，且混有success与unknown
- **THEN** 仅达到3次的四类独立产生active；success不抵消失败，unknown被忽略

#### Scenario: 账号隔离和时间边界
- **WHEN** NULL身份、event-only、其它Node、15分钟前或future事件存在
- **THEN** 不计入目标Incident，恰好15分钟事件纳入，当前Inventory中后续分页账号仍可生成

#### Scenario: 无恢复推断
- **WHEN** 同组低于3次或发生成功请求
- **THEN** 低于3次不返回该组，成功不解除仍达标active；不显示recovered或保存状态

### Requirement: Incident SHALL 保持只读有界查询契约

GET `/api/topology/nodes/{instance_id}/incidents` SHALL要求super_admin、no-store、5秒预算。status仅省略或active，provider/failure_class可选，limit默认25最大100。按last_seen DESC/account_key ASC/failure_class ASC过滤后keyset分页；opaque cursor MUST绑定Node与所有filters及完整排序位置，错配400。item SHALL提供node_id/account_key/provider/failure_class/status/first_seen/last_seen/hit_count/last_success_at；前三个统计时间和计数描述15m组，last_success_at为7天同账号成功最大时间或NULL。

#### Scenario: 分页过滤与身份
- **WHEN** provider/reason过滤、相同last_seen多个账号类别或继续cursor
- **THEN** 精确过滤后按混合方向排序，有界limit+1，无offset；Node/filters错配400且不会返回其它范围

#### Scenario: 授权与故障
- **WHEN** 无session、非super_admin、非GET、Node不存在或DB/Inventory失败
- **THEN** 分别401/403/方法拒绝/404/503，故障不能返回empty，恢复后只读重试成功

#### Scenario: 安全与重启
- **WHEN** runtime读function或direct SELECT，或隔离Down/Up及进程重启
- **THEN** 只有SECURITY DEFINER STABLE固定pg_catalog/migrator owner/PUBLIC revoke函数EXECUTE被授权；direct SELECT拒绝，Down只drop新function，旧表/index/数据/函数不变，重启从现有事件重算

#### Scenario: 本地容量
- **WHEN** 100账号10000事件进行active/provider/failure/pagination查询
- **THEN** 每次单个客户端DB query并记录latency，在合理预算内不新增表/index/worker/cache/materialized view/rollup
