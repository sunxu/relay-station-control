# add-cross-node-duplicate-ownership Design

## Context

System Design v1.8（R4.7）第 12.2 节已正式冻结 Cross-node Duplicate Ownership：

> 同一个规范化 `account_key` 同时出现在两个或以上不同 Relay Node 的 current、fresh、complete、eligible Inventory snapshot 中。

该节同时冻结：

- Node Inventory 沿用 Phase 2/3 已冻结的 current/fresh/complete/eligible 与 promotion 规则，R4.7 不修改 Inventory polling/freshness 数值，也不得套用 Gateway Directory 的 180 秒/540 秒。
- Cross-node conflict identity 固定为 `environments.environment_id + account_key + conflict_type = cross_node_duplicate_ownership`，每个 occurrence 关联一个 affected Node set，不产生 pairwise alert。
- 第一次合格 evidence 证明至少两个 owner 时创建 ACTIVE occurrence，severity 固定 Critical；持续存在且 owner membership 未变化时刷新同一 occurrence，membership 变化时按 affected Node set 增减规则处理（见第 4 节）。
- Occurrence 第一版 lifecycle 只有 ACTIVE/RESOLVED；alert/notification delivery persistence 留 Phase 6 决策。证据退化只作为 metadata，不增加中间状态。
- Absence 只在 fresh、complete、current Inventory snapshot 中才是证据；poll failed、传输失败、Provider 不完整、契约无效、disk fallback、stale/missing snapshot、超时、abandoned 或 policy promotion skipped 均不能证明账号已不存在。
- Gateway binding、Account active/disabled、schedulable、weight、cooldown、breaker、Node unbound 或 Directory stale 只能作为 Gateway Usage Context，不能创建、消除 ownership fact，也不能改变固定 Critical severity。
- Control 只 detect、persist evidence、alert、display 和 audit，不自动修复。

本 change 是把上述架构级冻结转译为 Control 可实现的最小契约，供后续按 Phase 拆分的实现 change 使用。

已有可复用基础设施：

- **Account Inventory（Phase 2/3，已归档）**：`account_inventory` 保存每个 `(instance_id, account_key)` 的 current lifecycle 状态（`present/missing/out_of_scope`）；`account_inventory_provider_states` 保存每个 `(instance_id, provider)` 的 `current_poll_run_id`、`state`（是否 `current`）和 `last_complete_at`；`account_inventory_poll_provider_results` 保存对应 poll run 的 Provider 级 `degraded`/`provider_snapshot_complete` 结果。既有 readonly-query（`00008_account_inventory_readonly_query.sql`）已经建立 fresh/stale 判定范式：`state.state = 'current'` 且 `database_now - state.last_complete_at <= interval '15 minutes'` 才视为 fresh；`state.state <> 'current'`、`last_complete_at IS NULL` 或 `source.poll_run_id IS NULL` 视为不一致，直接 fail closed。
- **Gateway Directory ingestion（已归档）** 与 **Relay Node ↔ Gateway Account Binding（已归档）**：两者均已明确声明与 duplicate ownership 完全独立，不能作为 evidence，也不能被 duplicate 影响。

## Goals

- 把 R4.7 的 ownership source of truth、duplicate 定义、conflict identity、occurrence 生命周期、severity 和 evidence 边界转译为可验收的 requirement/scenario。
- 明确 Phase 拆分与每个 Phase 的最小交付边界，使后续实现可以逐阶段、可回滚地推进。
- 冻结但不实现的技术决策点，避免实现阶段各自发明不一致的 schema/并发/生命周期语义。
- 明确本 change 与 Account Inventory、Gateway Directory、Relay Node Binding 三个既有 capability 的只读边界。

## Non-Goals

- 不实现 detection worker、schema Migration、read model、API、UI、alert/metric 落地代码；均留给后续实现阶段。
- 不修改 Account Inventory Phase 2/3 的 polling、freshness、promotion、lifecycle 或 readonly-query 契约。
- 不修改 Gateway Directory ingestion 或 Relay Node ↔ Gateway Account Binding 的既有契约。
- 不自动修改 Gateway、CLIProxyAPI、Sub2API Account/Group/Routing，不自动 unbind/rebind，不做 request routing/scheduling/failover/retry/breaker。
- 不做 duplicate 自动修复、capacity recommendation。
- 不建立 Cluster、Node Pool、Relay Scheduler、Redis truth 或跨 Node 全局同步屏障。

## 1. Ownership Source of Truth

Duplicate detection 的唯一合格 evidence 来源是：**各 Node/Provider 最近一次已成功 promotion 的 current、fresh、complete、eligible runtime Inventory**。具体转译为既有 Account Inventory 术语：

```text
eligible_owner_evidence[node, provider, account_key] MUST 同时满足：
  account_inventory_provider_states.state = 'current'
  AND account_inventory_provider_states.current_poll_run_id 对应的 poll provider result
      具备 inventory_mode = 'runtime' 且 provider_snapshot_complete = true
  AND database_now - account_inventory_provider_states.last_complete_at
      <= 既有 Phase 2/3 freshness 阈值（沿用既有实现使用的数值，本 change 不重新定义）
  AND account_inventory.lifecycle = 'present'
  AND account_inventory.instance_id = node
  AND account_inventory.account_key = account_key
```

MUST NOT 使用以下内容创造、消除或覆盖 ownership fact：

- 历史 poll run、72 小时历史趋势、daily summary、任意 previous/stale snapshot；
- `lifecycle IN ('missing', 'out_of_scope')` 的记录；
- Provider 结果 `provider_snapshot_complete = false`、传输失败、契约无效、disk fallback、超时、abandoned 或 `promotion_skipped_reason = policy_changed` 的 poll run；
- Gateway Directory snapshot、current state 或 freshness；
- Relay Node ↔ Gateway Account Binding（current binding、resolution 或 evidence snapshot）；
- Gateway Account `status`（active/disabled）、`schedulable`、`weight`、cooldown、breaker 状态；
- 任何 scheduler、runtime、health check 或进程存活状态。

## 2. Duplicate 定义

```text
cross_node_duplicate(account_key) :=
    |{ node : eligible_owner_evidence[node, provider, account_key] 成立 }| >= 2
```

MUST 同时满足 `same account_key` 与 `different relay_node_assets.instance_id`；`account_key` 沿用 Account Inventory 既有规范化定义（`normalized_provider + ':' + normalized_email`），仍是 Account Inventory 唯一 ownership identity，不新增第二套业务 identity。`account_key`（及其组成的 plaintext normalized email）属于敏感 identity，其存储、展示与脱敏边界见第 6c 节。

Node-local duplicate（同一 Node 单次/current Inventory 内部重复 identity）已经由 Phase 2/3 的 `account_inventory_poll_duplicates` 与 promotion 阻断机制独立处理，属于该 Provider snapshot 完整性错误。Cross-node duplicate 的实现 MUST NOT 复用 node-local duplicate 的事件、告警或 metric，也 MUST NOT 用同一个 `duplicate=true` 布尔值合并二者语义。

## 3. Conflict Identity

```text
logical_conflict_key = environments.environment_id + account_key + conflict_type
conflict_type = 'cross_node_duplicate_ownership'
```

`environments.environment_id`（稳定 text identity）MUST 用于 conflict identity；`environments.name`、display label、hostname 或任何 config display string MUST NOT 作为 conflict identity 的一部分。

每个 occurrence 领域语义上关联一个 **affected Node set**（本设计只冻结领域语义，不冻结具体数据库表示；Phase 1 可以在不改变该语义的前提下选择 normalized child rows、canonical array 或其他最小表示）。第一版 MUST 固定单一 `logical_conflict_key` 对应单一 ACTIVE occurrence；MUST NOT 按 Node pair（A-B、A-C、B-C ...）建立多个 occurrence 或多条告警——**Node pair 永远不得成为 occurrence identity**。affected Node set 的增减规则见第 4 节，只更新同一 occurrence 的关联集合，不创建新记录。

## 4. Occurrence Lifecycle

固定状态机（第一版只有两个终态 + 隐式 evidence health metadata，不新增中间状态）：

```text
detect:  首次证明 |eligible owners| >= 2 → 创建 ACTIVE occurrence，
         记录 first_seen_at = 本次 detect 时间，
         affected Node set = 本次证明的合格 owner 集合
refresh: occurrence 仍为 ACTIVE 期间，再次证明 |eligible owners| >= 2 时：
         若本次合格 evidence 证明的 owner membership 与 affected Node set 相同，
         → 只刷新 last_seen_at、追加 evidence observation，affected Node set 不变；
         若本次新的合格 evidence 证明 membership 发生变化（新增/缩减），
         → 按下方 "affected Node set 增减与 resolve coverage" 规则处理：
           新 owner 按增加规则加入，fresh+complete 证明不再 present 的 Node 按缩减规则移除，
           stale/failed/incomplete/unavailable evidence MUST NOT 触发移除
degrade: 涉及 occurrence 的某个 Node evidence 变为 stale/failed/incomplete/unavailable
         → MUST NOT resolve，只更新 evidence health metadata
         （例如 evidence_state ∈ {complete, degraded}、
         last_fully_verified_at），ACTIVE 状态与 affected Node set 保持不变
resolve: 见下方 "affected Node set 增减与 resolve coverage"
reopen:  RESOLVED 之后再次证明 |eligible owners| >= 2
         → 创建一条新 occurrence 记录（新的 first_seen_at/resolved_at 生命周期），
         MAY 复用相同 logical_conflict_key 以便统计复发次数，
         MUST NOT 复用旧 occurrence 记录的 primary identity
```

### affected Node set 增减与 resolve coverage

affected Node set 的增减 MUST 遵循以下规则，避免 stale/incomplete evidence 被误当作"该 Node 已放弃 ownership"的证据：

- **增加**：只有新的合格（current/fresh/complete）evidence 明确证明某 Node 是该 `account_key` 的 owner，才能把该 Node 加入 affected Node set。
- **缩减**：只有该 Node **自己**新的合格（current/fresh/complete）evidence 明确证明该 `account_key` 在该 Node 上已不再 `present`，才能把该 Node 从 affected Node set 移除。
- stale、failed、incomplete 或 unavailable evidence MUST NOT 导致把该 Node 从 affected Node set 移除；evidence degrade 时 occurrence 保持 ACTIVE，affected Node set 保持此前已证明为 owner 的全部 Node。
- **resolve**：MUST 取得足够的新合格 evidence，覆盖 occurrence 当前 affected Node set 中的**全部**Node，证明其中仍为 `present` owner 的数量 `<= 1`。不能只凭部分 Node 的新证据或凭其余 Node 的 evidence 缺失就 resolve。
- 从未参与该 occurrence 的无关 Node（不在 affected Node set 中）不需要 fresh evidence 才能 resolve；MUST NOT 建立全环境 fresh barrier。

MUST NOT 因为某个涉及 Node 的 evidence stale/failed/incomplete/unavailable 就认为其 ownership 已消失并据此缩减 affected Node set 或 resolve；resolve 只能由更强证据（覆盖当前全部 affected Node、证明 present owner ≤ 1）触发，不能由"证据缺失"触发。

#### 场景校验（用于验收，非新增 requirement 文本）

- A/B duplicate，B 变 stale → 不 resolve，affected Node set 仍为 `{A, B}`，evidence_state=degraded。
- A/B duplicate，B 取得新 fresh+complete evidence 证明 `account_key` 在 B 上已不 present → 可 resolve（因为已覆盖 affected Node set 中全部 Node：A 仍 present、B 已证明不再 present，present owner 数量降至 1）。
- A/B/C duplicate，C 变 stale/incomplete → affected Node set 不缩减，仍为 `{A, B, C}`。
- A/B/C duplicate，C 取得新 fresh+complete evidence 证明 `account_key` 在 C 上已不 present → 可缩减 affected Node set 为 `{A, B}`，但 occurrence 仍为 ACTIVE（因为 A、B 仍是合格 owner，present owner 数量为 2）。


## 5. Severity

Cross-node duplicate occurrence 的 severity 第一版固定为 **Critical**，且：

- 不因 Gateway binding 存在/不存在而改变；
- 不因 Gateway Account `status`（active/disabled）而改变；
- 不因 `schedulable`、`weight`、cooldown、breaker open 而改变；
- 不因涉及 Node 数量超过 2 而升级或降级；
- 只有 occurrence 转为 RESOLVED 才不再计入当前告警面，severity 字段本身不随生命周期改变含义。

## 6. Evidence Model

Occurrence 的数据模型 MUST 明确区分两类数据，语义与可变性完全不同：

### 6a. Occurrence mutable projection

以下字段是 occurrence 当前状态的**可变投影**，随 detect/refresh/degrade/resolve 更新，代表"当前已知最新状态"，不是历史记录本身：

```text
occurrence mutable projection:
  logical_conflict_key (environments.environment_id + account_key + conflict_type)
  occurrence_id
  status (ACTIVE | RESOLVED)
  severity = Critical (fixed)
  affected Node set semantics (领域概念；物理表示见第 3 节，Phase 1 决策)
  first_seen_at (创建后不变)
  last_seen_at (随 refresh 更新)
  resolved_at (nullable，resolve 时写入)
  evidence_state (complete | degraded) -- metadata only，不是新状态
  last_fully_verified_at
  latest evidence references (指向下方 append-only evidence observation 的最新记录)
```

### 6b. Evidence observation（append-only，immutable）

每次 detect/refresh/degrade/resolve 都 MUST 追加必要的 evidence observation 记录，而不是更新已有记录；写入后 MUST NOT UPDATE 或 DELETE：

```text
evidence observation (append-only):
  observation_id
  occurrence_id (归属)
  relay_node_assets.instance_id
  evidence source 引用：对应 current promoted
    (account_inventory_provider_states.current_poll_run_id, provider) 或
    等价的 current snapshot identity，用于可追溯但不可逆向还原原始响应
  observed_at (evidence 对应的 poll run/complete 时间)
  evidence freshness 判定结果 (fresh | stale，记录本次判定，不是持续实时字段)
  observation_kind (例如：owner_confirmed | absence_confirmed | degraded)
  recorded_at (本条 observation 写入时间)
```

```text
resolve evidence (append-only，其写入方式与生命周期同 evidence observation，MUST NOT UPDATE/DELETE):
  resolved_at
  resolving evidence 引用集合（必须覆盖 resolve 时 affected Node set 中的全部 Node）
  per-node resolving evidence 引用（证明各 Node 是否仍为 present owner 的具体 poll run/provider state 引用）
```

occurrence mutable projection 只更新"最新引用"指向这些 append-only observation；MUST NOT 反过来修改或覆盖已写入的 evidence observation 本身。

MUST NOT 保存：

- credential、secret、token、API key、raw upstream 响应；
- Gateway Directory 或 Binding 的任何字段作为 evidence（只能在展示层作为独立 context 关联展示，不写入 evidence 结构）。

account_key/完整 normalized email 的持久化边界见第 6c 节。

### 6c. account_key 隐私边界

`account_key = normalized_provider + ':' + normalized_email` 仍是 Account Inventory 唯一 ownership identity，本 change 不建立第二套业务 identity；`logical_conflict_key` 在**语义上**仍由 `environments.environment_id + account_key + conflict_type` 决定，但其**物理持久化表示**（是否直接存储 plaintext account_key，或使用等价的安全引用）留给 Phase 1 决策，不得据此创建第二套 ownership truth。

- Plaintext `account_key` / normalized email 属于敏感 identity。
- Detection 逻辑内部 MAY 在既有受保护 Account Inventory 数据边界内使用 plaintext `account_key`（例如与 `account_inventory.account_key` 做等值比较），因为该数据本身已经在既有保护边界内。
- 新的 occurrence/evidence persistence（第 6a/6b 节新增的存储）默认 MUST NOT 重复存储 plaintext `account_key` 或完整 normalized email。
- Phase 1 MUST 优先复用既有 masked/HMAC identity 或安全 source reference（例如引用 `account_inventory` 主键而非复制 email 明文）表达 occurrence/evidence 中的账号标识。
- UI/metrics/alerts/logs 永远 MUST NOT 输出 plaintext `account_key` 或完整 normalized email。
- 若实现阶段（Phase 1）经过调查证明新表必须持久化 plaintext `account_key`，MUST 停止并单独发起 security review，不得静默加入 schema。

## 7. Binding Relationship

三者严格独立，互不覆盖：

```text
Inventory Ownership Fact  (本 change 的 evidence/occurrence)
        ≠
Gateway Binding            (Relay Node ↔ Gateway Account binding, 已归档 change)
        ≠
Binding Resolution         (unbound/resolved/unresolved/unknown, 已归档 change)
```

- Duplicate detection、refresh、resolve 的判定逻辑 MUST NOT 读取、依赖或引用 `relay_node_gateway_account_bindings` 或其 resolution 派生结果。
- Directory stale 导致 Binding Resolution 为 `unknown` 时，只要 Node Inventory evidence 仍合格，duplicate occurrence 必须继续 ACTIVE，不受影响。
- 反之 Directory fresh 也不能把 stale/incomplete Inventory "洗白"为合格证据。
- 展示层（后续 Phase 5 read model）可以把 Binding 信息作为独立 Gateway Usage Context 并列展示，但 MUST 在数据结构上与 ownership evidence 分离，避免调用方误把二者当作同一 truth。

## 8. 冻结待实现阶段决策的技术边界（本轮不实现，逐项列出以避免实现阶段自由发挥）

以下问题必须在对应实现 Phase 中给出具体方案并接受独立评审，本 change 只冻结决策原则和禁止项，不预先固定具体 schema/参数：

1. **PostgreSQL schema**：新增表/索引必须 additive。Phase 1A 与 1B 是两个独立问题：Phase 1A 只调查 ownership source query 能否复用既有 `account_inventory*` 只读查询，若不足需报告具体 query/index 缺口，query-side schema/index 变更必须单独 Review，不得自动并入 Phase 1B；Phase 1B 只调查是否存在可安全复用的 generic occurrence/alert persistence，若无法复用再设计最小 occurrence/evidence persistence，不得复制 Account Inventory 的 identity 或 lifecycle 语义。是否新增 occurrence/evidence schema 与 source query 是否需要新 index 是两个独立决策，互不隐式触发。
2. **current/fresh/complete/eligible 查询条件**：必须直接复用 Phase 2/3 已冻结的判定（`state.state = 'current'`、`provider_snapshot_complete`、既有 freshness 阈值来源），不得重新定义或引入第二套数值。
3. **freshness threshold 的复用来源**：必须明确引用 Account Inventory 既有实现中实际使用的阈值来源（例如既有 readonly-query 或 poll run 配置），不得硬编码 R4.6 Directory 的 180s/540s。
4. **detection worker cadence**：必须说明触发方式（随 poll run 事件驱动、独立轮询或按需查询），并证明不会引入 global inventory barrier 或等待所有 Node 完成。
5. **transaction boundary**：detect/refresh/degrade/resolve 各自的事务边界与可见性保证。
6. **concurrency/lease/fencing 是否需要**：是否需要类似既有 poll run 的 lease/租约机制防止并发误判，或是否可以纯靠幂等 upsert + 唯一约束解决。
7. **occurrence dedupe unique constraint**：如何保证同一 `logical_conflict_key` 在 ACTIVE 状态下只有一条记录，且 reopen 后旧 RESOLVED 记录与新 ACTIVE 记录的唯一约束设计。
8. **evidence 模型的具体存储形态**：evidence observation（第 6b 节）是内嵌 JSON 还是独立 append-only 表，以及历史 evidence 的保留粒度；MUST 保持 append-only、immutable，不因存储形态选择而允许 UPDATE/DELETE。
9. **ACTIVE/RESOLVED persistence**：状态字段、时间戳字段的具体列设计与索引。
10. **unverifiable/degraded evidence metadata** 的具体字段与展示语义。
11. **reconciliation/restart 行为**：Control 进程重启或补跑后如何重新计算 occurrence，是否需要幂等重建。
12. **retention**：occurrence 和 evidence 历史的保留期限，是否对齐既有 audit/history 保留策略。
13. **acknowledgement 是否属于 occurrence truth**：若引入人工确认，必须明确它是否影响 ACTIVE/RESOLVED 状态机（默认倾向于不影响，acknowledgement 只是独立 metadata）。
14. **alert/notification 与 occurrence 的关系**：本轮只冻结 occurrence lifecycle（`ACTIVE | RESOLVED`）与 severity（固定 Critical）；alert/notification delivery record 是否与 occurrence 1:1，或 occurrence 只是 alert 的数据来源而非同一张记录，仍是 Phase 6 的独立决策项，本设计不预先假定两者是同一记录。
15. **masked identity 输出**：occurrence 展示层的账号标识脱敏规则，必须复用既有 Account Inventory masked/HMAC identity 约束。
16. **metrics names**：必须遵循既有 Prometheus 标签约束（`environment + alert_type + account_key` fingerprint 范式，`account_key` 只能以既有 masked/HMAC 形式出现，不得输出 plaintext），具体指标名留给实现阶段。
17. **read model/API needs**：查询维度（按 Node、按 account_key、按状态）留给 Phase 5 设计。
18. **Node-centric Topology 后续接入方式**：occurrence 如何在 Node-centric 视图中展示为只读关联信息，不得反向影响 Node/Binding/Inventory 状态。

## 9. Non-Goals（显式禁止）

- 不自动修改 Gateway 配置（Sub2API Account/Group/Routing）。
- 不自动修改 CLIProxyAPI 配置（credential/provider/account）。
- 不自动 unbind/rebind Relay Node ↔ Gateway Account binding。
- 不参与 request routing/scheduling。
- 不做 failover/retry/breaker。
- 不做 duplicate 自动修复（例如自动禁用其中一个 Node 的账号）。
- 不做 capacity recommendation。
- 不把 Gateway binding/status 当作 ownership truth 或用于改变 severity。

## Phased Delivery Plan

后续实现必须按以下阶段拆分，每阶段独立可验收、独立可回滚；本 change 本轮只冻结阶段边界，不实现任何阶段：

- **Phase 1A — source query feasibility**：证明 ownership source query（合格 owner 集合判定）能否直接复用既有 `account_inventory*` 表的只读查询；若不足，报告具体 query/index 缺口，query-side schema/index 变更单独 Review；不复制 Account Inventory 的 identity 或 lifecycle truth；不写入任何 occurrence。
- **Phase 1B — occurrence/evidence persistence**：只调查是否存在可安全复用的 generic occurrence/alert persistence；若没有，设计最小 additive occurrence/evidence persistence，schema 只保存 conflict lifecycle/evidence（第 6a/6b/6c 节边界），不复制 Inventory truth；是否新增 occurrence/evidence schema 与 Phase 1A 是否需要新 index 是两个独立问题；任何 Migration 仍必须先单独 Review。
- **Phase 2 — current ownership query**：实现只读、bounded 的"当前合格 owner 集合"查询，验证与 Phase 2/3 Inventory 判定完全一致，不写入任何 occurrence。
- **Phase 3 — detection + occurrence lifecycle**：实现 detect/refresh/degrade/resolve/reopen 状态机与 dedupe 约束。
- **Phase 4 — reconciliation/concurrency**：实现重启/补跑一致性、并发安全性与（如需要）lease/fencing。
- **Phase 5 — read model/API**：实现只读查询接口与 masked identity 输出，不做 UI。
- **Phase 6 — alerts/metrics acceptance**：接入告警与指标，验收 fingerprint 与命名规范。
- **Phase 7 — validation/runbook/archive**：完整回归、runbook 文档与 OpenSpec archive。

## Risks

- **evidence 来源被错误替换**：detection 逻辑若误读 Gateway Directory/Binding/scheduler 状态会产生假阳性或假阴性；本设计通过第 1/7 节显式列出禁止来源约束实现。
- **pairwise alert 爆炸**：若实现阶段按 Node pair 建表会违反第 3 节固定 identity；必须在 Phase 3 验收中显式测试 3+ Node 场景只产生一条 occurrence。
- **stale evidence 误 resolve**：第 4 节明确"证据缺失不能触发 resolve"，必须在 Phase 3/4 验收中覆盖"部分 Node stale 时 occurrence 保持 ACTIVE"的场景。
- **与 Binding 耦合**：第 7 节明确三者独立；必须在验收中证明 Binding 状态变化不触发 detection 重算，也不改变 severity。
- **evidence 泄露**：第 6 节明确禁止保存 credential/secret/raw payload/完整 email；必须在实现阶段复用既有 masked identity 机制。
