## MODIFIED Requirements

### Requirement: Control SHALL 按固定生命周期与 affected Node set 覆盖规则管理 duplicate occurrence

Cross-node duplicate occurrence MUST 遵循以下生命周期：首次证明合格 owner 数量 `>= 2` 时创建 ACTIVE occurrence 并记录 `first_seen_at`，affected Node set 初始化为本次证明的合格 owner 集合；occurrence 仍为 ACTIVE 期间再次证明合格 owner 数量 `>= 2` 时，若本次合格 evidence 证明的 owner membership 与当前 affected Node set 相同，MUST 只刷新 `last_seen_at` 与各已证明 Node 的最新 evidence 引用，不创建新记录、不改变 affected Node set；若本次新的合格 evidence 证明 membership 发生变化，MUST 按下方 affected Node set 增减规则处理（新增/缩减），MUST NOT 凭 membership 不变假设直接跳过增减判定。涉及 Node 的 evidence 变为 stale/failed/incomplete/unavailable 时 MUST NOT resolve occurrence 也 MUST NOT 将该 Node 从 affected Node set 移除，只能标记 evidence 为 unverifiable/degraded metadata。

affected Node set 的增减 MUST 遵循：只有新的合格（current/fresh/complete）evidence 明确证明某 Node 是该 `account_key` 的 owner，才能把该 Node 加入 affected Node set；只有该 Node 自己新的合格（current/fresh/complete）evidence 明确证明该 `account_key` 在该 Node 上已不再 present，才能把该 Node 从 affected Node set 移除；stale/failed/incomplete/unavailable evidence MUST NOT 导致移除。

Resolve MUST 取得足够的新合格 evidence，覆盖 occurrence 当前 affected Node set 中的全部 Node，证明其中仍为 present owner 的数量 `<= 1`，才能将 occurrence 标记为 RESOLVED 并记录 `resolved_at` 与 resolve evidence；从未参与该 occurrence 的无关 Node 不需要 fresh evidence 才能 resolve，MUST NOT 建立全环境 fresh barrier。RESOLVED occurrence 之后再次证明合格 owner 数量 `>= 2` 时 MUST 创建一条新的 occurrence 记录（独立的 `first_seen_at`/`resolved_at` 生命周期），MAY 复用相同 `logical_conflict_key` 以统计复发次数，但 MUST NOT 复用旧 occurrence 记录本身。

#### Scenario: 首次检测创建 ACTIVE occurrence
- **WHEN** 某 `account_key` 第一次被证明同时存在于至少两个 Node 的合格 current Inventory 中
- **THEN** Control 创建一条 ACTIVE occurrence，记录 `first_seen_at` 为本次检测时间，affected Node set 为本次证明的合格 owner 集合

#### Scenario: 持续存在且 membership 不变时刷新而非新建
- **WHEN** 一条 ACTIVE occurrence 对应的 duplicate 情况在下一次检测中仍然成立，且本次合格 evidence 证明的 owner membership 与当前 affected Node set 相同
- **THEN** Control 刷新该 occurrence 的 `last_seen_at`，仅有 material evidence change 时追加checkpoint并推进 evidence 引用，不创建新记录，affected Node set 不变；若本次合格 evidence 证明 membership 发生变化，MUST 按 affected Node set 增减规则处理，而非直接刷新

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

### Requirement: Control SHALL 区分 occurrence mutable projection 与 append-only evidence observation

Control MUST 每轮执行authoritative evaluation并更新mutable projection，但只在material evidence change时追加不可变checkpoint。Material MUST 包括source identity、observation_kind、可评估Node集合、membership add/remove、evidence_state、occurrence status变化（含resolve与新occurrence reopen）。Node/source/kind/membership/evidence_state/status与最近持久化checkpoint相同，MUST NOT追加evidence。latest_evaluation_id MUST指向最近实际写入的checkpoint，而不是无变化评估。每轮last_seen_at与完整评估last_fully_verified_at MUST继续正确更新。

历史evidence MUST全部保留，禁止本change删除、清理、迁移压缩或新增第二套history truth。Historical involvement MUST继续通过evidence EXISTS证明。零可引用行降级MUST继续不伪造evidence，保留latest checkpoint并更新projection；恢复造成 material change 时才追加。来源poll ID仅因retention缺失时MUST使用保留来源元数据比较，不能因此制造新source。

#### Scenario: 连续100轮无变化

- **WHEN** 同一occurrence连续100轮authoritative evaluation的source、结论与membership均相同
- **THEN** 每轮仍评估，evidence数量与latest_evaluation_id不增长/不变，projection时间正确推进

#### Scenario: 新source同结论

- **WHEN** 新promoted来源仍确认相同owner
- **THEN** 追加完整可引用per-node checkpoint并推进latest_evaluation_id

#### Scenario: 无新采集而fresh变stale

- **WHEN** 原source在评估时过期导致owner变degraded
- **THEN** 追加变化证据、保留membership且不resolve；后续相同degraded评估不追加

#### Scenario: absence与历史参与

- **WHEN** A/B重复后fresh完整证据确认缺失并解除重复
- **THEN** 先追加absence_confirmed再删除membership；即使affected set为空，A/B历史仍能查到同一目标RESOLVED occurrence

#### Scenario: 并发和重启

- **WHEN** 并发refresh、重试或Repository重启评估同一material变化
- **THEN** 在occurrence锁内与最新checkpoint比较，最多追加一次，legacy evidence不变

#### Scenario: 零来源降级与恢复

- **WHEN** 所有受影响Node均无可引用来源，随后来源恢复
- **THEN** 降级只更新projection、不伪造或删除证据；重复降级不增长，恢复造成 material change 时才追加 checkpoint；若恢复后的状态与最近持久化 checkpoint 相同且无其他 material change，则不追加

#### Scenario: evidence observation 不可修改

- **WHEN** 任意调用尝试UPDATE或DELETE已有evidence
- **THEN** 现有数据库保护继续拒绝，legacy evidence保留完整

#### Scenario: 状态变化追加而非覆盖 evidence

- **WHEN** occurrence 的 detect、refresh、degrade 或 resolve 产生 material change 且有可引用来源
- **THEN** Control 追加本轮checkpoint，不修改此前已写入的evidence

#### Scenario: resolve evidence 同样 append-only

- **WHEN** authoritative evaluation支持解除duplicate
- **THEN** Control追加resolve所需的不可变evidence，不覆盖先前证据，先保留absence再移除membership

## ADDED Requirements

### Requirement: Duplicate reconciliation SHALL 默认每20秒执行周期评估

Control MUST复用inventorypoll现有reconciliation loop，默认周期20秒；启动及成功finalize回调MUST保留。不得新增duplicate scheduler或更改eligibility、freshness阈值、absence、degraded或resolve规则。默认lease保持30秒，周期继续严格小于lease；数据库fencing继续防止expired lease写入。

#### Scenario: 无新采集的20秒周期

- **WHEN** 没有新finalize且数据库正常
- **THEN** 既有loop按20秒默认周期继续authoritative evaluation，按评估时DB时间识别stale而非延长freshness window

#### Scenario: Startup与新采集

- **WHEN** 启动reconcile成功或新poll finalize成功
- **THEN** 仍触发既有低延迟回调，失败/过期lease不获得额外执行资格
