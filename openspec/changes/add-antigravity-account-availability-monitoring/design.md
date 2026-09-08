## Context

Control 已有 Inventory promotion/fencing、Provider 双维度 freshness/health、7天 Request Quality、只读 History/Incidents。实际证据缺口及本地源码位置见 source-investigation.md。现存 auth 历史无法区分401/403/封禁；当前 basic_status 折叠了 disabled/unavailable/未来 retry，不能反推所有原始布尔字段是否存在。

## Goals / Non-Goals

**Goals:** 对每个 Node 当前 Inventory 中可证明属于 Antigravity 认证文件模式的账号提供六态及持久故障 occurrence；有界、保守、可恢复、并发幂等；保留现有账号入口。

**Non-Goals:** Google探测、Token读取/下载/解码、CLIProxy修改、额外queue consumer、其他Provider或内存账号模式、自动处置、泛化规则引擎、历史身份系统、通知投递平台、quota、Prometheus/Grafana、缓存、rollup/partition。AVAILABLE只代表现有证据支持可用，不保证下一次Google请求成功。

## Decisions

### 1. Scope and evidence projection

- Account identity 仍为 `(node_id, account_key)`；provider 规范化后必须严格等于 `antigravity`，auth-files 已有 runtime 模式且本条 `source=file`。source=memory、disk fallback、缺字段或旧行未观测均不得猜为支持模式。其他Provider显示 `—`（不适用，不是第七状态）；Antigravity但文件模式未被证明显示 UNKNOWN。
- 复用 Inventory 已有 `/v0/management/auth-files` 请求、HTTP transport、management key、timeout、Provider策略和finalize。新能力不额外轮询auth-files，也不访问磁盘auth文件或Token字段。管理HTTP本身401/403是Node采集失败，只能导致UNKNOWN，绝非账号认证失败。
- 在现有parser增加两个可空安全字段：`availability_runtime_evidence`（`file_active|file_disabled|file_error|file_unavailable|file_unknown`）和 `auth_failure_reason`（`token_invalid|account_blocked|forbidden|other`）。NULL表示旧版本/非适用/未证明file source。active必须明确status=active、disabled=false、unavailable=false，且next_retry_after为空或已到期；缺少必要字段归file_unknown，不改变原basic_status算法。
- 两个字段仅作为已有 snapshot item/current Inventory 的附加元数据，由原合格promotion同事务写入。非完整/degraded不能刷新这份current证据；最新Provider health与poll失败独立使availability失效。现有 snapshot表只是既有原子promotion的暂存/来源链，不是新增数据源或新历史系统。保留当前poll identity、scheduled_at/observed_at；不可用old NULL不回填。
- Request事件仅增加 nullable `auth_failure_reason`。success、非Antigravity、无法分类的历史值为NULL；新Antigravity失败可为四个子原因。原 failure_class 仍为auth/quota/rate_limit/upstream/unknown；明确新增认证白名单允许进入auth，旧已识别映射及其他Provider不变。other不把quota/rate_limit/upstream改成auth，也永远不作为availability occurrence reason。event_hash算法与resolved/unresolved身份逻辑不变，旧同hash重放仍insert-ignore，不能借新解析补写旧event。

### 2. Safe classifier

优先级：明确blocked代码 > 明确token代码 > 上游请求401 > 上游请求403 > other。只看失败事件；成功事件即使携带旧error文本也不分类。

初始blocked allowlist严格为 `account_deactivated`、`account_disabled`、`account_suspended`、`account_blocked`；token allowlist为 `invalid_grant`、`token_revoked`、`token_invalidated`。从已有失败结构的固定error/code/type字段，或可解析错误JSON内同名字段匹配完整标识符；纯文本仅允许去空白后整个值等于白名单标识符。不复制CPA的宽泛Contains与Action策略，不把任意长文中的单词、否定描述、`permission_denied`、普通403、quota、组织策略/地区限制猜为blocked。新增别名需独立fixture与评审。

现有 `fail_status_code/fail_summary` 及auth-files `status_message`仅允许在边界有界瞬时解析上述标识符/结构（已有响应上限内，再限制单错误字段8KiB、JSON深度8）；超限/无法解析为other，不能破坏既有Inventory完整性或event ingestion。原文及Token未知字段不进入domain DTO/DB/API/日志/metrics/trace。禁止调用download endpoint或Token有效性探测；HTTP列表原响应缓冲不可避免，但不解析、使用或投影access/refresh token值。

### 3. Quality gate and six-state ordering

时间以PostgreSQL statement timestamp为准；Inventory fresh阈值复用15分钟。合格前提：Node仍受监控、账号lifecycle=present、Provider current complete且在freshness窗口、最新health非degraded、没有由现有Provider health_scheduled_at/health_reason证明的更新失败或不完整观察、file模式被证明。不得以旧last_complete仍fresh忽略最新health失败。该门槛只用于availability，绝不改变Duplicate eligibility。

顺序：
1. 前提不满足 → UNKNOWN，safe reason按 `stale|incomplete|node_collection_failed|not_present|unsupported_mode|unproven`，不新建/恢复故障。
2. 合格证据明确disabled → DISABLED，不新建故障，不把人为关闭视为恢复。
3. 已确认且尚未被新恢复证据清除的认证故障 → ACCOUNT_BLOCKED > TOKEN_INVALID > FORBIDDEN。多个reason可有独立ACTIVE occurrence，表格展示最高优先级，不因换reason自动resolve其他reason。
4. 有待确认的一次故障、runtime error/unavailable、未来retry或相互冲突的证据 → UNKNOWN（reason=`pending_confirmation|runtime_unavailable|retry_wait|conflicting_evidence`），不得显示Available。
5. 无待确认/当前故障，fresh完整file_active → AVAILABLE；无请求也可AVAILABLE，Request Quality仍可Unknown。

这是证据状态，不修改Inventory lifecycle/basic_status/Binding/Duplicate。UNKNOWN因DB/API不可读则HTTP503/UI Unavailable，而不是伪造业务UNKNOWN row；仅在成功读取到stale/incomplete等事实时返回UNKNOWN。

### 4. Confirmation and current failure

去抖固定，不增加配置：同账号同reason在最近15分钟内至少2个不同 `(node_id,event_hash)` 的失败，且严格晚于最近已知成功/恢复水位；或1个失败 + 同一新鲜完整runtime观察明确error/unavailable，且两者位于15分钟且没有更新成功；或两个不同成功promotion source identity的连续runtime观察均给出相同明确token/blocked/forbidden子原因。单次401/403且runtime active只进入pending_confirmation UNKNOWN，不创建occurrence。

不同reason不得凑数；重复pop/insert、重复reconcile和同一poll重试不能计为第二份证据。只接受账号身份已resolved且在当前Inventory的请求；unresolved/event-only/其他Node/Provider忽略。future事件不参与，窗口包含下边界，旧于窗口的迟到事件不确认。事件时间相同的success/failure不能证明先后，保守不恢复；hash仅用于确定性排序，不代表因果顺序。

已确认故障在没有恢复证据时保持ACTIVE；窗口滑出或7天event删除不能resolve。freshness失效只让展示UNKNOWN并保留ACTIVE历史，不反复发新告警。仅有历史auth而无子原因不能新建token/blocked/forbidden。

### 5. Recovery and recurrence

恢复必须有新证据且严格晚于该reason最后确认失败：
- 同账号成功请求，时间晚于所有已知该reason event/runtime失败且没有更新的相反证据，可resolve该reason；若Inventory同时stale/不完整，occurrence可按成功证据RESOLVED，但展示仍UNKNOWN，不能直接AVAILABLE。
- 无流量或缺少成功请求时，至少两个不同source identity、连续的fresh完整file_active promotion，均晚于故障，disabled=false/unavailable=false/retry不在未来。一次fresh active只累计恢复候选，不直接消除已确认故障。
- 任何中间明确失败、degraded/不完整、missing、disabled或过期打断连续恢复；恢复次数按源snapshot/poll身份累计，不能按20秒reconcile累计；两次必须来自既有5分钟调度的相邻槽；跳槽/间隔不为5分钟即重置，避免reconciler停机期间的失败被最新current覆盖后误计连续。超过fresh窗口也不能算连续。

同一个健康snapshot回放、重启、retain后current poll外键变NULL，都不能重复计数：保留已处理source UUID（不做cascade FK）与source scheduled/observed watermark。迟到且不晚于已记录恢复水位的故障不能重开；恢复后新的合格故障创建新的occurrence UUID，保留旧RESOLVED记录，不把旧行改回ACTIVE。

DISABLED、missing、停止监控或更换Node不是成功，不自动resolve旧ACTIVE。已有ACTIVE在这些状态下只作为历史未解故障保留，不重复通知、不升级；UI明确标注当前UNKNOWN/DISABLED与既有未解故障历史，不能把旧ACTIVE显示为本轮新确认故障，不隐藏持久历史。恢复不改变既有Incidents：它仍按最近窗口失败次数只读计算。

### 6. PostgreSQL and concurrency

不能纯内存推导ACTIVE/RESOLVED。允许未来一个additive migration组（编号实施时确认，当前最高00025）：
- 现有 snapshot items、account_inventory 增加上述两个nullable安全元数据；现有events增加nullable auth_failure_reason及固定CHECK。不更改旧行、hash、七天retention。
- `account_availability_checkpoints`：PK `(node_id,account_key)`，derived state/reason/since、last processed runtime source UUID/time、连续healthy source计数(0..2)、recovery watermarks。仅保存有界安全摘要；不是第二套Inventory或canonical identity。已有PK account身份关系校验，reason/state固定CHECK。
- `account_availability_occurrences`：occurrence UUID、node_id/account_key、reason、severity、status、first_seen/last_failure/confirmed/resolved timestamps、最小确认与恢复 source IDs/times。reason CHECK仅允许token_invalid/account_blocked/forbidden，分别固定Critical/Critical/Warning，禁止other/runtime_unavailable；partial UNIQUE `(node_id,account_key,reason) WHERE status='ACTIVE'`；保留RESOLVED且本change不新增cleanup。不能依赖被7天retention删除的event FK来维持历史。

每次对某账号先INSERT checkpoint ON CONFLICT DO NOTHING，再SELECT FOR UPDATE；在同一事务、同一DB snapshot读取current/provider/events并判定、更新checkpoint及插入/resolve occurrence。使用SERIALIZABLE或既有REPEATABLE READ+完整锁策略，serialization/deadlock按现有有限重试。相同source/窗口结果无变化不刷新Since、不新增occurrence。失败事务不推进任何watermark；retry/restart从DB恢复，partial unique是最后防线。跨账号固定node/key顺序、有界批量100、按keyset推进，不能永远只扫前100。

confirm摘要保留最多两份独立event/source键与时间，不复制event body。每次判定在15分钟已有有界索引范围取证并与checkpoint水位比较；不使用max occurred_at作为唯一ingestion游标从而漏掉窗口内迟到事件。首次部署缺少safe字段时UNKNOWN直到新采集，旧历史不追造告警。

### 7. Lifecycle, alert delivery and security

复用 Inventory 生命周期成功finalize/startup/周期reconcile回调（当前默认20秒，30秒lease不改），作为独立有界availability分支；不另建scheduler/queue，不新开Google/Node调用。每分支独立超时和错误处理，availability失败不能阻断poll finalize、duplicate reconciliation或数据面。请求成功/失败落库后最迟下一轮reconcile读取，不新增destructive consumer。服务关闭遵守context取消，checkpoint/occurrence结果仅以DB提交为准。

告警的可持久事实就是occurrence表，TOKEN_INVALID/ACCOUNT_BLOCKED=Critical、FORBIDDEN=Warning。UNKNOWN永远不告警；other/runtime_unavailable无告警映射，不因持续时间、重复次数或重启升级为Warning/Critical。只允许这三个明确reason进入确认分支，other不得累计到确认阈值。不引入通知投递/Outbox系统；现有observer可在提交后输出固定reason/severity/transition的日志，不能输出账号/Token/raw。日志投递不是exactly-once承诺，commit后进程崩溃不丢DB occurrence。用户可从只读API查看所有ACTIVE/RESOLVED事实。

所有新增query/write函数owner=migrator，SECURITY DEFINER、固定pg_catalog、PUBLIC revoke、runtime仅EXECUTE；runtime无新增表或现有provider/events的direct SELECT/写入。readonly函数STABLE；写函数VOLATILE。新增版本化finalize/event-insert/read wrapper而不改旧签名，旧调用字段为NULL；当前版本writer原子保存新元数据。SQL migration文件不可改旧编号，generated Go/TS/sqlc正常make generate。

### 8. API and UI

现有Topology组合账号POST保持filter/body/cursor/CSRF/审计语义，response每个item additive nullable `availability` 对象：`state,reason,since`。非Antigravity为NULL；适用但未证明为UNKNOWN。Since是DB记录的当前状态/原因开始时间；初始化未评估允许NULL，stale叠加以last_complete+threshold为开始而不伪造failure时间。UI显示系统时区 `YYYY-MM-DD HH:mm:ss`。

readonly GET `/api/topology/nodes/{instance_id}/account-availability-occurrences`：可选account_key、status=ACTIVE|RESOLVED（默认ACTIVE）、limit默认25最大100、opaque cursor；账号身份只允许既有认证HTTP请求、内存，不写浏览器导航URL/storage/log。排序confirmed_at DESC/occurrence_id DESC，cursor绑定Node/account/status及位置，错配400；既有super_admin/no-store，401/403；未知Node404、DB失败503。该read允许显示该Node曾确认的occurrence，即使当前Inventory缺失；不把其伪造成当前账号行。

账号表增加Availability/Reason/Since，UNKNOWN与API Unavailable区分。现有点击继续使用History/Incidents。账号详情增一个小只读availability occurrence区域展示ACTIVE/RESOLVED/严重度/确认与恢复时间，History入口仍受current Inventory membership gate；不改原Incidents endpoint、taxon或Active-only行为，不加ack/resolve/disable/retry/re-auth按钮。

账号页对当前有界账号页一次batch读取availability，不每行HTTP/DB N+1；occurrence独立keyset分页。验证100账号/10000事件，记录查询次数和耗时；不预建缓存/rollup/partition。

## Risks / Trade-offs

- HTTP usage queue destructive-pop/no-ACK仍存在未提交事件丢失窗口；AVAILABLE不是实时Google SLA，事件缺失不能证明绝对健康。
- 缺乏token内容和Google探测的情况下只能保守解释已有明确证据；不泛化CPAMP自动删除/重认证策略。
- 新列/persistence是区分安全子原因、稳定since、两次独立恢复及跨重启告警所需，无法只改UI得到可靠历史。不保存raw，旧event不可还原。
- 错误文本白名单刻意窄；未知新Google文案保持other/UNKNOWN，新增类型先fixture评审。
- 单Control仍是既有destructive queue owner假设；availability DB写入支持重试/并发，不扩展为多Control安全消费queue的承诺。

## Rollout and rollback

先forward additive migration，部署兼容旧写入的新schema，再部署新Control；仅现有显式监控的Antigravity file账号启用判断，无合格新元数据时UNKNOWN。初始保守不回填，不触发Google请求。回滚停止新availability分支并恢复旧镜像，保留所有schema/occurrence与旧v1函数；生产不Down。隔离测试Down仅撤销本change新增函数/表/列（仅测试库），旧基础数据/函数结果必须保留。

## Resolved architecture decision

A1已由用户明确关闭：UNKNOWN永远不告警，第一版不做other/runtime_unavailable告警。六态保持不变，occurrence reason只允许token_invalid/account_blocked/forbidden；other仍可作为normalization安全子原因，但无告警、严重度或自动升级路径。runtime error/unavailable仍可作为明确认证失败的旁证，不能单独生成other故障。

历史ACTIVE遇UNKNOWN不伪造RESOLVED，也不重发或升级通知；状态只解释当前证据是否足够，旧occurrence继续作为此前已确认故障的历史记录。该处理不改变冻结的成功/两次健康观察恢复规则。本轮不实施。
