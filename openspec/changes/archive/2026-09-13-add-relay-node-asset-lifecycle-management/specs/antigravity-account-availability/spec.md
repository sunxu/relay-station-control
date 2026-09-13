## MODIFIED Requirements

### Requirement: Availability SHALL use exactly six evidence states

对于通过下述 Node lifecycle/monitoring eligibility gate 的 current projection，Control MUST只展示AVAILABLE/TOKEN_INVALID/ACCOUNT_BLOCKED/FORBIDDEN/UNKNOWN/DISABLED。AVAILABLE要求present、current fresh complete Inventory、Provider最新health正常、file_active且非未来retry、无当前或待确认认证故障。stale/degraded/incomplete/最新采集失败/非present/证据冲突为UNKNOWN；普通403且runtime仍active MUST为UNKNOWN/pending_confirmation，即使出现两个不同request_id也不能建立FORBIDDEN。合格fresh证据明确disabled为DISABLED。UNKNOWN MUST永远不告警，DISABLED不得告警；不得按持续时间或重复次数升级。业务UNKNOWN与DB/API读取失败的Unavailable MUST分开。

Availability current projection MUST require active Node lifecycle plus the existing fresh,
complete Inventory and monitoring eligibility gates. Retired/replaced Nodes may preserve
historical occurrence evidence but cannot be an eligible current target.

#### Scenario: Node lifecycle delta
- **WHEN** the Node lifecycle condition described by this change is evaluated
- **THEN** the existing baseline behavior remains intact and the lifecycle fence is also enforced

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

### Requirement: Availability reads SHALL preserve security and existing truths

新增Control读取MUST复用super_admin、no-store、有界分页、cursor绑定、SECURITY DEFINER/fixed pg_catalog/migrator/PUBLIC revoke/runtime EXECUTE。DB失败503，不伪造Empty/Unknown；401/403保持。新告警作为独立availability occurrence，MUST NOT改变History membership、只读Incidents聚合、Inventory/Binding/Duplicate/CLIProxy状态。来源时间/窗口以DB UTC为准，展示沿用系统时区。

Availability reads MUST explicitly filter active Node lifecycle and current monitoring eligibility
instead of relying on a missing monitoring row. Stable historical identity and occurrence evidence
remain readable through existing bounded read contracts.

#### Scenario: Node lifecycle delta
- **WHEN** the Node lifecycle condition described by this change is evaluated
- **THEN** the existing baseline behavior remains intact and the lifecycle fence is also enforced

#### Scenario: ACL and failure
- **WHEN** runtime调用安全query或直接SELECT底表，或HTTP无权限/DB失败
- **THEN** 仅安全query获准，direct SELECT拒绝；HTTP分别401/403/503，不伪造账号状态

#### Scenario: Bounded account and occurrence pages
- **WHEN** 读取100账号页及ACTIVE/RESOLVED occurrence页
- **THEN** availability采用batch，无每账号HTTP/DB N+1；occurrence默认25最大100且keyset稳定，Node/account/status cursor错配400

### Requirement: Current projection SHALL remain separate from occurrence lifecycle

对于通过 Node active/monitoring eligibility gate 的 current projection，以下状态规则 MUST 保持；retired/replaced Node 不作为 current eligible target。

ACTIVE TOKEN_INVALID 或 ACCOUNT_BLOCKED 在 Inventory fresh+complete、Provider health qualified、runtime=file_active 且无成功恢复时，current MUST 分别为 TOKEN_INVALID 或 ACCOUNT_BLOCKED；不得 AVAILABLE 或仅因 file_active 降为 UNKNOWN。ACTIVE FORBIDDEN 且 runtime active 时 current MUST 为 UNKNOWN/pending_confirmation。任一 ACTIVE fault 遇 stale/incomplete 时 current MUST 为 UNKNOWN，stale/incomplete 本身不 resolve occurrence 且不作为 recovery evidence；DISABLED 时 current MUST 为 DISABLED，DISABLED 本身不 resolve occurrence 且不作为 recovery evidence。若独立存在满足既有 guards 的 qualified success，occurrence lifecycle 仍按 success recovery rule 处理。无 ACTIVE fault 时既有 AVAILABLE baseline 保持不变。

Retirement/replacement MUST not delete or resolve availability occurrences. It only removes the
old Node from current eligible projection; historical occurrence lifecycle follows its existing
evidence rules.

#### Scenario: Node lifecycle delta
- **WHEN** the Node lifecycle condition described by this change is evaluated
- **THEN** the existing baseline behavior remains intact and the lifecycle fence is also enforced

#### Scenario: Active TOKEN_INVALID with fresh file-active
- **WHEN** ACTIVE TOKEN_INVALID、Inventory fresh+complete、Provider health qualified、runtime=file_active 且无成功恢复
- **THEN** current 为 TOKEN_INVALID，occurrence 保持 ACTIVE

#### Scenario: Active ACCOUNT_BLOCKED with fresh file-active
- **WHEN** ACTIVE ACCOUNT_BLOCKED、Inventory fresh+complete、Provider health qualified、runtime=file_active 且无成功恢复
- **THEN** current 为 ACCOUNT_BLOCKED，occurrence 保持 ACTIVE

#### Scenario: Active FORBIDDEN with active runtime
- **WHEN** ACTIVE FORBIDDEN 且 runtime active
- **THEN** current 为 UNKNOWN/pending_confirmation，occurrence 保持 ACTIVE

#### Scenario: Active fault with stale or disabled inventory
- **WHEN** ACTIVE fault 的 Inventory stale/incomplete，或合格观察为 DISABLED
- **THEN** stale/incomplete 时 current 为 UNKNOWN，DISABLED 时 current 为 DISABLED；occurrence lifecycle 不变且不作为恢复证据
