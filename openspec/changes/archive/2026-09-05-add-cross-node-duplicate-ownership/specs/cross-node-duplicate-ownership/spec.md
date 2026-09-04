## ADDED Requirements

### Requirement: Control SHALL 只使用合格 current Inventory 作为 ownership evidence

Control SHALL 只把各 Relay Node/Provider 最近一次已成功 promotion 的 current、fresh、complete、eligible runtime Inventory 记录作为 ownership evidence。Control MUST NOT 使用历史/72 小时/daily summary/任意 previous 或 stale snapshot、`provider_snapshot_complete = false` 的 poll 结果、传输失败/契约无效/disk fallback/超时/abandoned/`promotion_skipped_reason = policy_changed` 的 poll run、`lifecycle IN ('missing', 'out_of_scope')` 的记录、Gateway Directory snapshot/current state、Relay Node ↔ Gateway Account Binding（含其 resolution）、Gateway Account `status`/`schedulable`/`weight`/cooldown/breaker 或任何 scheduler/runtime/health 状态创造、消除或覆盖 ownership fact。

#### Scenario: 合格 current evidence 参与判断
- **WHEN** 某 Node/Provider 的 `account_inventory_provider_states.state = 'current'`、对应 poll 结果 `provider_snapshot_complete = true`、仍在既有 freshness 阈值内，且该 `account_key` 的 `account_inventory.lifecycle = 'present'`
- **THEN** 该 Node 可以被计入该 `account_key` 的 eligible owner 集合

#### Scenario: stale 或不完整 evidence 不参与判断
- **WHEN** 某 Node/Provider 的 inventory 处于 stale、`provider_snapshot_complete = false`、传输失败、契约无效、abandoned 或 `promotion_skipped_reason = policy_changed`
- **THEN** Control MUST NOT 把该 Node 计入 eligible owner 集合，也 MUST NOT 因此判定该 Node 已放弃 ownership

#### Scenario: Gateway Directory/Binding/scheduler 状态被排除
- **WHEN** 判断某 `account_key` 是否 cross-node duplicate
- **THEN** Control 的判断逻辑 MUST NOT 读取 Gateway Directory snapshot、Relay Node ↔ Gateway Account Binding 或其 resolution、Gateway Account status、scheduler/runtime 状态作为 ownership evidence 输入

### Requirement: Control SHALL 定义并区分 Cross-node Duplicate 与 Node-local Duplicate

Cross-node Duplicate Ownership MUST 定义为同一规范化 `account_key` 同时存在于至少两个不同 `relay_node_assets.instance_id` 的合格 current Inventory 中。Node-local duplicate（同一 Node 单次/current Inventory 内部重复 identity）MUST 保持独立语义、独立事件、独立告警与独立 metric，MUST NOT 与 cross-node conflict 合并为统一 `duplicate=true` 布尔值。

#### Scenario: 跨 Node 重复触发 cross-node duplicate
- **WHEN** 同一 `account_key` 同时出现在 Node A 与 Node B 各自合格的 current Inventory 中
- **THEN** Control 判定为 cross-node duplicate ownership

#### Scenario: 单 Node 内部重复不算 cross-node
- **WHEN** 同一 Node 的单次 poll 内部对同一 `account_key` 报告重复 identity（Node-local duplicate）
- **THEN** Control MUST NOT 将其计入 cross-node duplicate occurrence，MUST 继续沿用既有 Node-local duplicate 事件与告警路径

### Requirement: Control SHALL 使用固定 conflict identity 避免 pairwise alert

Cross-node duplicate 的 logical conflict identity MUST 在语义上固定为 `environments.environment_id + account_key + conflict_type`，其中 `conflict_type = cross_node_duplicate_ownership`；`environments.environment_id` MUST 使用稳定 text identity，`environments.name`、display label、hostname 或任何 config display string MUST NOT 作为 conflict identity 的一部分。每个 occurrence 领域语义上关联一个 affected Node set；该字段的物理持久化表示（独立子表、canonical array 或其他最小表示）留给实现阶段决策，但 Node pair MUST NOT 成为 occurrence identity 的一部分，Control MUST NOT 按 Node pair（如 A-B、A-C、B-C）创建多条 occurrence 或多条告警。

#### Scenario: 三个及以上 Node 同时声明同一账号
- **WHEN** 同一 `account_key` 同时出现在 Node A、B、C 各自合格的 current Inventory 中
- **THEN** Control 只创建一条 occurrence，其 affected Node set 包含 {A, B, C}，不产生 A-B、A-C、B-C 多条 pairwise occurrence 或告警

#### Scenario: affected Node set 变化不创建新记录
- **WHEN** 涉及 occurrence 的 affected Node set 按第 4 节规则发生合法变化
- **THEN** Control 更新同一 occurrence 关联的 affected Node set，不创建新的 occurrence 记录

#### Scenario: environment display 属性不参与 identity
- **WHEN** `environments.name` 或其它 display 属性发生变化，而 `environments.environment_id` 不变
- **THEN** 既有 occurrence 的 `logical_conflict_key` 保持不变，不受影响

### Requirement: Control SHALL 按固定生命周期与 affected Node set 覆盖规则管理 duplicate occurrence

Cross-node duplicate occurrence MUST 遵循以下生命周期：首次证明合格 owner 数量 `>= 2` 时创建 ACTIVE occurrence 并记录 `first_seen_at`，affected Node set 初始化为本次证明的合格 owner 集合；occurrence 仍为 ACTIVE 期间再次证明合格 owner 数量 `>= 2` 时，若本次合格 evidence 证明的 owner membership 与当前 affected Node set 相同，MUST 只刷新 `last_seen_at` 与各已证明 Node 的最新 evidence 引用，不创建新记录、不改变 affected Node set；若本次新的合格 evidence 证明 membership 发生变化，MUST 按下方 affected Node set 增减规则处理（新增/缩减），MUST NOT 凭 membership 不变假设直接跳过增减判定。涉及 Node 的 evidence 变为 stale/failed/incomplete/unavailable 时 MUST NOT resolve occurrence 也 MUST NOT 将该 Node 从 affected Node set 移除，只能标记 evidence 为 unverifiable/degraded metadata。

affected Node set 的增减 MUST 遵循：只有新的合格（current/fresh/complete）evidence 明确证明某 Node 是该 `account_key` 的 owner，才能把该 Node 加入 affected Node set；只有该 Node 自己新的合格（current/fresh/complete）evidence 明确证明该 `account_key` 在该 Node 上已不再 present，才能把该 Node 从 affected Node set 移除；stale/failed/incomplete/unavailable evidence MUST NOT 导致移除。

Resolve MUST 取得足够的新合格 evidence，覆盖 occurrence 当前 affected Node set 中的全部 Node，证明其中仍为 present owner 的数量 `<= 1`，才能将 occurrence 标记为 RESOLVED 并记录 `resolved_at` 与 resolve evidence；从未参与该 occurrence 的无关 Node 不需要 fresh evidence 才能 resolve，MUST NOT 建立全环境 fresh barrier。RESOLVED occurrence 之后再次证明合格 owner 数量 `>= 2` 时 MUST 创建一条新的 occurrence 记录（独立的 `first_seen_at`/`resolved_at` 生命周期），MAY 复用相同 `logical_conflict_key` 以统计复发次数，但 MUST NOT 复用旧 occurrence 记录本身。

#### Scenario: 首次检测创建 ACTIVE occurrence
- **WHEN** 某 `account_key` 第一次被证明同时存在于至少两个 Node 的合格 current Inventory 中
- **THEN** Control 创建一条 ACTIVE occurrence，记录 `first_seen_at` 为本次检测时间，affected Node set 为本次证明的合格 owner 集合

#### Scenario: 持续存在且 membership 不变时刷新而非新建
- **WHEN** 一条 ACTIVE occurrence 对应的 duplicate 情况在下一次检测中仍然成立，且本次合格 evidence 证明的 owner membership 与当前 affected Node set 相同
- **THEN** Control 刷新该 occurrence 的 `last_seen_at` 与 evidence 引用，不创建新记录，affected Node set 不变；若本次合格 evidence 证明 membership 发生变化，MUST 按 affected Node set 增减规则处理，而非直接刷新

#### Scenario: A/B duplicate，B 变 stale 时不 resolve 也不缩减
- **WHEN** Node A、B 的 duplicate occurrence 处于 ACTIVE，B 的 evidence 变为 stale
- **THEN** occurrence 保持 ACTIVE，affected Node set 仍为 {A, B}，B 的 evidence 标记为 degraded，不 resolve

#### Scenario: A/B duplicate，B 取得合格 absence evidence 可 resolve
- **WHEN** Node A、B 的 duplicate occurrence 处于 ACTIVE，B 取得新的 fresh、complete evidence 明确证明该 `account_key` 在 B 上已不 present，A 仍为合格 owner
- **THEN** Control 判定 affected Node set 中 present owner 数量降至 1，可将 occurrence 标记为 RESOLVED

#### Scenario: A/B/C duplicate，C 变 stale 时 affected Node set 不缩减
- **WHEN** Node A、B、C 的 duplicate occurrence 处于 ACTIVE，C 的 evidence 变为 stale 或 incomplete
- **THEN** affected Node set 仍为 {A, B, C}，occurrence 保持 ACTIVE，不因 C 证据缺失而缩减或 resolve

#### Scenario: A/B/C duplicate，C 取得合格 absence evidence 可缩减但不 resolve
- **WHEN** Node A、B、C 的 duplicate occurrence 处于 ACTIVE，C 取得新的 fresh、complete evidence 明确证明该 `account_key` 在 C 上已不 present，A、B 仍为合格 owner
- **THEN** Control 将 affected Node set 缩减为 {A, B}，但 occurrence 仍为 ACTIVE（present owner 数量为 2）

#### Scenario: RESOLVED 后再次 duplicate 创建新 occurrence
- **WHEN** 一条 occurrence 已 RESOLVED，随后合格 evidence 再次证明同一 `account_key` 的 owner 数量 `>= 2`
- **THEN** Control 创建一条新的 occurrence 记录，拥有独立的 `first_seen_at`；MAY 复用相同 `logical_conflict_key` 统计复发次数，但 MUST NOT 复用或覆盖旧的 RESOLVED occurrence 记录

### Requirement: Cross-node duplicate occurrence SHALL 固定 Critical severity

Cross-node duplicate occurrence 的 severity MUST 固定为 Critical。Gateway binding 存在与否、Gateway Account `status`（active/disabled）、`schedulable`、`weight`、cooldown、breaker 状态 MUST NOT 动态改变该 severity。

#### Scenario: Gateway binding 状态不影响 severity
- **WHEN** 涉及 duplicate occurrence 的某个 Node 当前存在或不存在 Relay Node ↔ Gateway Account binding
- **THEN** occurrence 的 severity 保持 Critical 不变

#### Scenario: Gateway Account 状态不影响 severity
- **WHEN** 涉及 duplicate occurrence 的 Gateway Account `status` 为 disabled，或 `schedulable=false`、`weight=0`、cooldown 或 breaker open
- **THEN** occurrence 的 severity 保持 Critical 不变

### Requirement: Control SHALL 区分 occurrence mutable projection 与 append-only evidence observation

Occurrence 的数据模型 MUST 区分两类数据：occurrence mutable projection（`status`、affected Node set 关联、`last_seen_at`、`evidence_state`、`last_fully_verified_at`、latest evidence 引用等，随 detect/refresh/degrade/resolve 更新，代表当前已知最新状态）与 evidence observation（append-only，记录每次 detect/refresh/degrade/resolve 产生的具体证据）。Evidence observation MUST NOT 被 UPDATE 或 DELETE；每次状态变化 MUST 追加新的 evidence observation 记录，occurrence mutable projection 只更新指向最新 observation 的引用。Resolve evidence 同样 MUST 是 append-only、immutable。

#### Scenario: 状态变化追加而非覆盖 evidence
- **WHEN** occurrence 发生 detect、refresh、degrade 或 resolve
- **THEN** Control 追加一条新的 evidence observation 记录，不修改此前已写入的 evidence observation

#### Scenario: evidence observation 不可修改
- **WHEN** 任意调用尝试 UPDATE 或 DELETE 已写入的 evidence observation 记录
- **THEN** Control/数据库拒绝该操作，历史 evidence observation 保持完整

#### Scenario: resolve evidence 同样 append-only
- **WHEN** occurrence 被标记为 RESOLVED
- **THEN** 对应 resolve evidence 以追加方式写入且不可修改，occurrence mutable projection 只更新 `resolved_at` 与最新引用

### Requirement: Control SHALL 保存最小 evidence 且不持久化敏感 credential 数据

Cross-node duplicate occurrence 的 evidence MUST 至少能够证明：涉及的 Relay Node ID、各 Node 对应的 current promoted snapshot/evidence identity 引用、`observed_at`/evidence freshness 判定结果、occurrence 的 detect/refresh/resolve 时间、resolve evidence。Evidence MUST NOT 保存 credential、API key、access token、refresh token、password、Secret 实际内容或 raw upstream 响应/payload。`account_key`（`normalized_provider + ':' + normalized_email`）仍是 Account Inventory 唯一 ownership identity，本 capability 不建立第二套业务 identity；`account_key`/normalized email/原始账号识别信息 MAY 直接持久化与展示，不属于本 capability 的禁止敏感字段，不要求 masked/HMAC 标识。

#### Scenario: evidence 包含必要可追溯字段
- **WHEN** Control 创建或刷新一条 occurrence
- **THEN** evidence 中可查得涉及 Node ID、各 Node 对应的 current promoted snapshot 引用、`observed_at`、evidence freshness 判定结果和 occurrence 生命周期时间戳

#### Scenario: evidence 不包含敏感 credential/原始数据
- **WHEN** Control 持久化 occurrence evidence
- **THEN** evidence MUST NOT 包含 credential、API key、access token、refresh token、password、Secret 实际内容或 raw upstream 响应/payload

#### Scenario: 账号标识直接复用 canonical account_key，仅由 occurrence 持久化
- **WHEN** Control 在 occurrence persistence 中表达账号标识
- **THEN** occurrence 直接持久化既有 Account Inventory canonical `account_key`（`normalized_provider + ':' + normalized_email`），不新增第二套 fingerprint/HMAC identity，不需要脱敏存储；evidence persistence MUST NOT 重复持久化 `account_key`，evidence 通过所属 `occurrence_id` 继承账号 identity，read model 需要账号信息时经 `occurrence_id` join occurrence 取得

#### Scenario: 管理界面与授权 API 可返回账号标识
- **WHEN** 授权 Control 管理界面或 API 展示 occurrence 相关账号标识
- **THEN** 输出 MAY 包含 `account_key`、provider、normalized email 或原始账号识别信息；Prometheus metrics label MUST NOT 使用完整 email/account_key 作为高基数标签，账号级上下文只出现在 alert payload/read model，不出现在 metrics label

### Requirement: Ownership Fact、Gateway Binding 与 Binding Resolution SHALL 保持相互独立

Inventory Ownership Fact（本 capability 的 evidence/occurrence）、Gateway Binding（Relay Node ↔ Gateway Account binding）与 Binding Resolution（unbound/resolved/unresolved/unknown）MUST 保持三个相互独立的事实来源。Cross-node duplicate 的 detect、refresh、degrade、resolve MUST NOT 读取或依赖 Relay Node ↔ Gateway Account Binding 或其 resolution 结果。Directory stale 导致 Binding Resolution 为 unknown 时，只要 Node Inventory evidence 仍合格，duplicate occurrence MUST 继续 ACTIVE；Directory fresh MUST NOT 把 stale 或不完整的 Inventory evidence 视为合格。

#### Scenario: Binding 变化不触发或影响 duplicate 判断
- **WHEN** 管理员对涉及 duplicate occurrence 的某个 Node 执行 bind、rebind 或 unbind
- **THEN** 该操作 MUST NOT 触发 duplicate occurrence 的 detect/refresh/resolve 重算，也 MUST NOT 改变已有 occurrence 的状态或 severity

#### Scenario: Directory stale 时 Inventory evidence 仍合格
- **WHEN** 某 Node 的 Gateway Directory 处于 stale（Binding Resolution 为 unknown），但该 Node 的 account inventory evidence 仍为 current/fresh/complete
- **THEN** 涉及该 Node 的 cross-node duplicate occurrence 继续保持 ACTIVE，不因 Directory stale 而 resolve 或降级

#### Scenario: Directory fresh 不能掩盖 stale Inventory
- **WHEN** 某 Node 的 Gateway Directory fresh，但该 Node 的 account inventory evidence 本身 stale 或不完整
- **THEN** Control MUST NOT 因 Directory fresh 而把该 Node 计入合格 owner 集合

### Requirement: Cross-node Duplicate Ownership 能力 SHALL 禁止任何自动修复或数据面动作

Control 检测到 cross-node duplicate ownership 后 SHALL 只执行 detect、evidence 持久化、alert、display 和 audit。Control MUST NOT 自动修改 Sub2API Gateway 配置（Account/Group/Routing）、MUST NOT 自动修改 CLIProxyAPI 配置（credential/provider/account）、MUST NOT 自动执行 unbind/rebind、MUST NOT 参与 request routing/scheduling、MUST NOT 执行 failover/retry/breaker 动作、MUST NOT 自动修复 duplicate（例如自动禁用其中一个 Node 的账号）、MUST NOT 提供 capacity recommendation、MUST NOT 把 Gateway binding/status 当作 ownership truth。

#### Scenario: 检测到 duplicate 后无自动修复动作
- **WHEN** Control 检测到一条新的 cross-node duplicate occurrence
- **THEN** Control 只创建 evidence、occurrence 与告警记录，不调用任何 Gateway/CLIProxyAPI mutation API，不修改 Relay Node ↔ Gateway Account Binding

#### Scenario: 管理员必须在原生系统中处置
- **WHEN** 管理员需要解决一条 cross-node duplicate occurrence
- **THEN** 修复动作必须由管理员在真正拥有配置权的原生系统（Sub2API 或 CLIProxyAPI）中执行，Control 不提供自动化修复入口
