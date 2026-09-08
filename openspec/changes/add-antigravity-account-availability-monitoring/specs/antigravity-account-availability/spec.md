## Purpose

为 Control 中每个 Node 上已被当前 Inventory 证明的 Google Antigravity 认证文件账号提供保守的六种可用性展示与持久故障发生/恢复证据，严格隔离账号身份、保留并发幂等和只读数据面边界。

## ADDED Requirements

### Requirement: Availability SHALL only observe Antigravity file accounts

Control SHALL只从现有Inventory、Provider state、Request Quality events及已有auth-files采集读取证据，按node_id+canonical account_key隔离。provider MUST为antigravity且runtime source=file。MUST NOT读取或保存access/refresh Token、下载auth文件、请求Google、新增Node API、修改CLIProxy、身份或数据面。其他Provider无availability；Antigravity模式不明为UNKNOWN。

#### Scenario: Node isolation and unresolved identity
- **WHEN** A/B上存在同account_key，只有A出现认证失败，或event account_key为NULL
- **THEN** 只有A可形成该账号故障；B状态独立，unresolved不制造账号或告警

#### Scenario: Unsupported mode and management failure
- **WHEN** source=memory、disk fallback、模式不明，或management端点本身401/403
- **THEN** 不把响应当账号token/封禁证据；Antigravity显示UNKNOWN，其他Provider不适用

### Requirement: Availability SHALL use exactly six evidence states

Control MUST只展示AVAILABLE/TOKEN_INVALID/ACCOUNT_BLOCKED/FORBIDDEN/UNKNOWN/DISABLED。AVAILABLE要求present、current fresh complete Inventory、Provider最新health正常、file_active且非未来retry、无当前或待确认认证故障。stale/degraded/incomplete/最新采集失败/非present/证据冲突为UNKNOWN；普通403且runtime仍active MUST为UNKNOWN/pending_confirmation，即使出现两个不同request_id也不能建立FORBIDDEN。合格fresh证据明确disabled为DISABLED。UNKNOWN MUST永远不告警，DISABLED不得告警；不得按持续时间或重复次数升级。业务UNKNOWN与DB/API读取失败的Unavailable MUST分开。

#### Scenario: Healthy inventory without requests
- **WHEN** fresh完整file账号明确active、disabled=false、unavailable=false、无未来retry和故障，即使没有请求
- **THEN** availability=AVAILABLE，原Request Quality可为Unknown，互不改写

#### Scenario: Degraded overrides a fresh last complete snapshot
- **WHEN** last_complete仍fresh但health_degraded=true或更新poll失败/不完整
- **THEN** availability=UNKNOWN，不伪造Available，不改变Duplicate eligibility

#### Scenario: Disabled and unknown do not alert
- **WHEN** 合格观察disabled，或观察stale/incomplete/失败
- **THEN** 分别DISABLED/UNKNOWN，不创建新occurrence，不把此状态当恢复证据

#### Scenario: Retry and ambiguous runtime status
- **WHEN** next_retry_after在未来或runtime unavailable却没有明确认证子原因
- **THEN** AVAILABLE不可成立，按UNKNOWN展示且不将其推断为token或封禁

### Requirement: Confirmed failures SHALL distinguish token invalidity from forbidden

安全子原因 MUST仅限token_invalid/account_blocked/forbidden/other。上游请求401或精确invalid_grant/token_revoked/token_invalidated映射token_invalid；精确account_deactivated/account_disabled/account_suspended/account_blocked映射account_blocked；无这些明确语义的403映射forbidden。blocked优先于token与HTTP；原文不落库/DTO/日志。确认 MUST遵守reason专属规则：token_invalid/account_blocked可由最近15分钟同reason两个不同非空request_id确认，或一个失败（可无request_id）与fresh完整runtime error/unavailable共同支持。MUST NOT仅凭runtime source重复确认故障；无匹配request failure时，明确runtime token/blocked语义也只为待确认UNKNOWN。普通403 MUST有请求失败+fresh runtime error/unavailable交叉证据才可确认FORBIDDEN；仅有重复403或forbidden文本不得确认。单次瞬时401以及任意数量普通403加active均为待确认UNKNOWN。

#### Scenario: Token failure confirmation
- **WHEN** 两个不同非空request_id的401/invalid_grant失败，或一个token_revoked失败（可无request_id）加同窗口fresh完整runtime error且没有更新成功
- **THEN** TOKEN_INVALID，产生Critical ACTIVE occurrence

#### Scenario: Explicit block and ordinary forbidden
- **WHEN** 按既有去抖确认的错误为account_deactivated/account_disabled/account_suspended/account_blocked，或普通403与fresh完整runtime error/unavailable共同支持
- **THEN** 前者ACCOUNT_BLOCKED/Critical，后者FORBIDDEN/Warning；普通403绝不直接判封号

#### Scenario: Transient and mixed failures
- **WHEN** 仅一个401或403且runtime active，或两个事件属于不同reason
- **THEN** 不凑成同类确认，不产生长期故障；重复读取同request_id、event或source不增加确认计数，换event_hash也不使同request_id变为独立请求

#### Scenario: Unknown error content
- **WHEN** raw message含否定文本、任意包含blocked的句子、quota/permission_denied或超限内容
- **THEN** 不匹配明确blocked；只认可固定字段完整标识符，未知为other且原文丢弃

### Requirement: Availability occurrences SHALL be durable and idempotent

Control SHALL以node_id/account_key/reason为故障身份，使用PostgreSQL ACTIVE/RESOLVED occurrence。occurrence reason MUST仅允许token_invalid/account_blocked/forbidden，严重度依次为Critical/Critical/Warning；other/runtime_unavailable MUST NOT成为告警reason。FORBIDDEN occurrence MUST具有普通403请求失败与fresh runtime error/unavailable交叉依据，不能以两个普通403单独创建。每个reason最多一个ACTIVE，确认与checkpoint同事务并受唯一约束/锁保护。重复运行不得刷新Since或新增相同ACTIVE。多个reason独立保存，UI优先显示blocked/token/forbidden，不将其他reason的出现视为恢复。UNKNOWN/DISABLED不新建且不虚假resolve已有故障。

#### Scenario: Concurrent creation and restart
- **WHEN** 并发reconcile、commit结果未知后重试或进程重启处理同一source
- **THEN** 最多一个ACTIVE且确认计数不重复，DB失败不推进watermark

#### Scenario: Expiry is not recovery
- **WHEN** 请求滑出15分钟、7天retention删除event、Inventory过期或账号被禁用/移走
- **THEN** ACTIVE不会仅因此RESOLVED；当前可用性可为UNKNOWN/DISABLED，历史事实仍可查

### Requirement: Recovery SHALL require newer independent evidence

同账号严格晚于已确认该reason全部失败、且无更新冲突的成功请求 MUST可resolve；无流量时两个不同source identity、既有五分钟相邻poll槽的连续fresh完整active观察 MUST可resolve。中间失败/源证据unknown/disabled/缺失或过期打断计数；仅等待第二个健康source的展示UNKNOWN不打断合格健康观察计数。同timestamp不按hash猜因果。已恢复水位以前的迟到失败不得重开；恢复后的新确认故障新建occurrence，保留原RESOLVED。成功恢复不保证Inventory已fresh，因此RESOLVED可与当前UNKNOWN同时存在。

#### Scenario: Success after failure
- **WHEN** 已确认故障后有严格更新的成功，且无更新runtime或event失败
- **THEN** 对应ACTIVE→RESOLVED；Inventory不合格仍显示UNKNOWN，不强行Available

#### Scenario: No traffic recovery
- **WHEN** 故障后连续两次不同promotion fresh完整active，期间无失败/降级/禁用
- **THEN** 第二次才RESOLVED；一次snapshot重复100次reconcile仍只计一次

#### Scenario: Recurrence and late evidence
- **WHEN** 已RESOLVED后收到旧失败重放或新的两次确认失败
- **THEN** 旧失败不重开；新故障创建一个新ACTIVE UUID，保留旧RESOLVED

### Requirement: Availability reads SHALL preserve security and existing truths

新增Control读取MUST复用super_admin、no-store、有界分页、cursor绑定、SECURITY DEFINER/fixed pg_catalog/migrator/PUBLIC revoke/runtime EXECUTE。DB失败503，不伪造Empty/Unknown；401/403保持。新告警作为独立availability occurrence，MUST NOT改变History membership、只读Incidents聚合、Inventory/Binding/Duplicate/CLIProxy状态。来源时间/窗口以DB UTC为准，展示沿用系统时区。

#### Scenario: ACL and failure
- **WHEN** runtime调用安全query或直接SELECT底表，或HTTP无权限/DB失败
- **THEN** 仅安全query获准，direct SELECT拒绝；HTTP分别401/403/503，不伪造账号状态

#### Scenario: Bounded account and occurrence pages
- **WHEN** 读取100账号页及ACTIVE/RESOLVED occurrence页
- **THEN** availability采用batch，无每账号HTTP/DB N+1；occurrence默认25最大100且keyset稳定，Node/account/status cursor错配400


### Requirement: Unknown evidence MUST never generate availability alerts

UNKNOWN MUST永远不创建、重发或升级availability告警。安全子原因other及runtime_unavailable解释只供只读诊断，MUST NOT进入确认计数或产生Warning/Critical occurrence；不论持续多久、重复多少次都保持此规则。已有明确reason的ACTIVE遇UNKNOWN只保留此前确认的未解故障历史，不伪造RESOLVED或声称本轮仍有新确认故障。

#### Scenario: Persistent runtime unavailable
- **WHEN** 无明确认证原因的runtime unavailable持续超过15分钟并经过100次reconciliation或restart/retry
- **THEN** 当前状态保持UNKNOWN，无other/runtime_unavailable occurrence、无告警或严重度升级

#### Scenario: Other failures remain non-alerting
- **WHEN** 收到两个或更多独立other失败且runtime error/unavailable
- **THEN** 不满足任何availability故障确认规则，不创建token/blocked/forbidden或other occurrence

#### Scenario: Existing fault becomes unknown
- **WHEN** 已有TOKEN_INVALID ACTIVE，随后Inventory stale导致UNKNOWN
- **THEN** 不创建、重发或升级告警；保留此前ACTIVE历史并标识当前UNKNOWN，不因证据不足伪造RESOLVED


### Requirement: Independent request evidence MUST be based on distinct request identifiers

Availability确认 MUST在同Node/account内按非空request_id保守去重，同一request_id永远只算一份请求证据；event_hash不同不证明请求独立。仅请求确认路径 MUST至少有两个不同request_id且同reason，且只允许token_invalid/account_blocked使用。缺少request_id时 MUST NOT仅凭多个hash确认，只可使用一个已分类请求失败与fresh完整runtime error/unavailable的交叉证据。replay/retry/duplicate及已确认ID在重启/retention后重放 MUST NOT凑数或重新充当新请求。同request_id结果或原因冲突 MUST保守不参与失败确认，不按latest/first wins。

#### Scenario: Same request identifier with different hashes
- **WHEN** 同一request_id的401失败生成多个不同event_hash，runtime仍active
- **THEN** 只算一份请求证据，不创建TOKEN_INVALID occurrence

#### Scenario: Two distinct request identifiers
- **WHEN** 同reason的401/invalid_grant失败具有两个不同非空request_id，位于确认窗口且满足scope/新鲜度及水位条件
- **THEN** 才构成两份独立请求证据，可确认TOKEN_INVALID Critical

#### Scenario: Missing request identifiers cannot be replaced by hashes
- **WHEN** 两个401失败均无request_id但event_hash不同，runtime active
- **THEN** 不创建occurrence，不能伪造request_id或将hash当请求计数依据

#### Scenario: Missing request identifier with runtime corroboration
- **WHEN** 一个无request_id的401/invalid_grant失败与同窗口fresh完整runtime error共同支持且无更新成功
- **THEN** 可通过交叉证据确认TOKEN_INVALID，不需要第二个hash

#### Scenario: Request identifier replay after recovery
- **WHEN** 已确认occurrence恢复后，同一已使用request_id以不同hash重放，包括原event被retention清理后
- **THEN** 不算新请求、不因此再次确认故障；确认摘要保留ID，不依赖event保留期

### Requirement: Ordinary forbidden responses MUST require runtime corroboration

普通403 MUST只归类为安全forbidden子原因，不能单独证明账号级FORBIDDEN。runtime仍active时，无论不同request_id多少均为UNKNOWN/pending_confirmation且零availability告警。普通403与fresh完整账号级runtime error/unavailable交叉确认后才建立FORBIDDEN Warning；仅future retry导致的折叠unavailable或management HTTP失败MUST NOT作为该旁证。普通403 MUST NOT直接或经计数升级为ACCOUNT_BLOCKED；明确blocked白名单仍按其独立去抖规则确认ACCOUNT_BLOCKED Critical。原Incidents/Request Quality MUST继续按既有auth taxonomy处理普通403，不应用availability确认过滤。

#### Scenario: Two ordinary forbidden responses with active runtime
- **WHEN** 两个不同request_id均为普通403且runtime仍active
- **THEN** UNKNOWN/pending_confirmation，无FORBIDDEN occurrence，也无ACCOUNT_BLOCKED

#### Scenario: Forbidden response with runtime unavailable
- **WHEN** 一个普通403失败与fresh完整runtime unavailable共同支持，scope与窗口合格且无更新成功
- **THEN** FORBIDDEN，创建Warning ACTIVE occurrence，不能判blocked

#### Scenario: Explicit blocked code after confirmation
- **WHEN** 两个不同request_id携带明确account_deactivated/account_disabled/account_suspended/account_blocked代码且满足既有确认条件
- **THEN** ACCOUNT_BLOCKED，创建Critical ACTIVE occurrence，不能降为普通FORBIDDEN


#### Scenario: Active runtime after previously confirmed forbidden
- **WHEN** 既有FORBIDDEN ACTIVE之后runtime已active，但尚不满足成功请求或连续两次健康观察的恢复条件
- **THEN** 当前availability为UNKNOWN/pending_confirmation，不由旧ACTIVE强制展示FORBIDDEN、不重发告警；原occurrence保留ACTIVE历史直到可靠恢复，不伪造RESOLVED；下一相邻槽再有合格健康观察时按原两次规则RESOLVED


#### Scenario: Runtime-only observations do not replace request evidence
- **WHEN** 两个不同fresh完整runtime source均给出token/blocked语义，但没有相应request failure
- **THEN** 不能确认故障或创建occurrence；runtime来源数量不能替代不同request_id或request failure + runtime交叉证据
