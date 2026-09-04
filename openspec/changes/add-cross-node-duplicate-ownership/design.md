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

## Phase 1A Investigation Findings（本轮新增，只做调查，不改契约/不新增 Migration/不新增表/不写 detection worker）

以下内容基于对实际 schema、SQL 函数与 store Go 代码的检查，用于证明/推翻 §1 的 `eligible_owner_evidence` 是否可以由既有 `account_inventory*` 表只读满足。全部引用均可在下列文件复核：`migrations/00005_account_inventory_poll_run_foundation.sql`、`migrations/00006_account_inventory_snapshot_foundation.sql`、`migrations/00007_account_inventory_lifecycle_foundation.sql`、`migrations/00008_account_inventory_readonly_query.sql`、`migrations/00009_account_inventory_history_compaction.sql`（该文件內多次 `CREATE OR REPLACE`，Up 中最终生效版本在第 4643 行 `-- +goose Down` **之前**的最后一次定义，即约第 1103–1240 行；第 4927 行之后的同名定义属于 Down 回滚段，不是当前生效版本）、`internal/store/account_inventory_readonly_query.go`。

### 输出一：精确 eligible-owner predicate

```sql
-- eligible_owner(node, account_key) 为真，当且仅当（单次 evaluation 只取一次 wall-clock，见下方 single database_now 说明）：
WITH evaluation AS (SELECT clock_timestamp() AS database_now)
SELECT 1
FROM account_inventory AS account
JOIN account_inventory_provider_states AS state
  ON state.instance_id = account.instance_id
 AND state.provider    = account.provider
CROSS JOIN evaluation
WHERE account.instance_id       = :node                        -- Node identity：account_inventory.instance_id（00007 L77，FK → relay_node_assets.instance_id）
  AND account.account_key       = :account_key                  -- account_inventory.account_key（00007 L78，CHECK = provider || ':' || normalized_email，00007 L112）
  AND account.lifecycle         = 'present'                     -- 00007 L149-163 account_inventory_lifecycle_shape CHECK；present 是四态之一
  AND state.state               = 'current'                     -- 00006 L128 account_inventory_provider_state_fixed CHECK：该列当前恒为 'current'（无其它取值）
  AND state.last_complete_at    IS NOT NULL                     -- 00006 L120（NOT NULL 列，恒非空，此判断为防御性对齐既有 readonly-query 的一致性写法）
  AND (evaluation.database_now - state.last_complete_at)
      <= interval '15 minutes'                                  -- freshness：见下方"freshness 来源"，使用单一 evaluation.database_now，不逐行重新取 wall-clock
  AND (account.lifecycle = 'out_of_scope')
      = (state.monitoring_status = 'out_of_scope')               -- 与 account_inventory_provider_states.monitoring_status 保持一致（00007 L64-73），镜像既有 readonly-query 的一致性 guard（00009 L1199-1201/L4927 附近）
```

`state.current_poll_run_id` **MUST NOT** 出现在 eligibility predicate 中——`current_poll_run_id` 是可空 source pointer（`00006` L118，无 `NOT NULL` 约束），既有 retention/compaction 路径（`00006` L127 `ON DELETE SET NULL (current_poll_run_id)`）允许在保留策略生效后合法地把该指针清空，此时 `account_inventory_provider_states` 这一行的 `state='current'`、`last_complete_at`、`monitoring_status` 等 current-state 字段依然完全有效、依然可以参与 eligibility 判定。`current_poll_run_id` 非 NULL 时可以作为 consistency/evidence reference（例如 Phase 1B 设计 evidence observation 时用它引用具体 poll run），但它是否存在**不得**成为 ownership eligibility 的前提条件。

逐字段来源与结论：

- **Node identity**：`account_inventory.instance_id`（`00007` L77），FK 到 `relay_node_assets(instance_id)`（`00007` L165-166）。
- **provider**：`account_inventory.provider`（`00007` L77），仅用于 join `account_inventory_provider_states`，不是 eligible-owner 的独立筛选维度（一个 account_key 只属于一个 provider，`account_key = provider || ':' || normalized_email` 已强制关联，`00007` L112）。
- **account_key**：`account_inventory.account_key`（`00007` L78），主键组成部分（`(instance_id, account_key)`，`00007` L100）。
- **lifecycle = present**：`account_inventory.lifecycle`（`00007` L91），四态 CHECK 见 `00007` L149-163（`present` / `suspected_missing` / `missing` / `out_of_scope`）。
- **provider state = current**：`account_inventory_provider_states.state`（`00006` L128），当前 schema 下该列被 CHECK 固定为 `'current'`（`account_inventory_provider_state_fixed`，`00006` L128），即该列目前恒真，只作为未来可能引入其它取值时的防御性判断保留。
- **current_poll_run_id attribution（谁的 pointer 对应谁的证据）**：`account_inventory.current_poll_run_id`（`00007` L94）**只**在"present"路径（`INSERT ... ON CONFLICT (instance_id, account_key) DO UPDATE`，`00007` L531-576）里被写入/刷新为本次 promoted poll run 的 `target_poll_run_id`——即：对当前 `lifecycle='present'` 且在最新 promoted snapshot 中出现的 account，其 `current_poll_run_id` 对应"最近一次证明它 present 的 promotion"。而对 `suspected_missing`/`missing` 的 absence transition（`00007` L495-520 的 UPDATE 分支），SET 子句只更新 `lifecycle`/`consecutive_missing_count`/`missing_since`/`updated_at`，**不更新** `current_poll_run_id`——因此该账号变为 `suspected_missing`/`missing` 之后，其行上的 `current_poll_run_id` 仍然指向它最后一次 `present` 时的那次 promotion，而不是"本次证明它缺席"的那次 poll run。**结论：`account.current_poll_run_id` 不得作为 absence evidence 的来源**；absence evidence 的真正来源是"新的合格 Provider promotion（即触发本次 absence transition 的那次 finalize 事务本身）+ lifecycle transition 的结果（`lifecycle` 列的新值）+ 对应的 Provider current state（`account_inventory_provider_states` 在同一次 finalize 事务中被刷新的 `current_poll_run_id`/`last_complete_at` 等）或等价的 promotion evidence（例如该次 finalize 事务的 `poll_run_id` 本身，如需要引用）"，而不是账号行自身陈旧的 `current_poll_run_id`。Phase 1B 设计 resolve evidence（第 4 节 resolve 规则要求的"新合格 evidence 证明 present owner ≤ 1"）时 MUST 遵守这一点：resolve 时用于证明"该 Node 上 account_key 已不 present"的 evidence 引用，必须指向触发该次 absence transition 的那次 finalize/poll run（或 Provider current state 的最新引用），不能引用账号行上过时的 `current_poll_run_id`。
- **inventory_mode = runtime / provider_snapshot_complete = true**：**不需要在查询时重新判断**。触发器 `control_validate_account_inventory_promotion`（`00006` L280-317）强制：只要某 `account_inventory_poll_provider_results.promotion_applied = true`（即该 provider 在该次 poll run 被提升为 current），就必须同时满足 `NEW.contract_valid`、`NEW.inventory_mode = 'runtime'`、`NEW.node_identity_complete`、`result.snapshot_complete`，且 `account_inventory_provider_states.current_poll_run_id` 已经指向这个 poll run（`00006` L295-303，此为 promotion **发生那一刻**的一次性一致性约束，不代表该 pointer 此后必须永久非空）。因此 `account_inventory.lifecycle = 'present'` 这一事实本身就传递性地保证了它来自一次 `runtime` 且 `snapshot_complete = true` 的 promotion，无需额外 join `account_inventory_poll_provider_results` 复查（原始 `00008` 函数体确实多 join 了一次这张表作为额外一致性 guard，属于防御性冗余，不是 eligibility 的必要条件；Phase 1B 可自行决定是否保留这层防御性 join）。
- **degraded 是否影响 eligibility**：**不影响**。当前 schema 已把"健康状态"从 promotion 路径解耦：`account_inventory_provider_states.health_degraded` / `health_reason` / `health_scheduled_at`（`00009` L866-868 新增列，`00009` L920 起 NOT NULL + CHECK）由触发器 `control_refresh_account_inventory_provider_health_v1`（`00009` L1055-1084）在**每次** finalize（不限定是否 promoted，只要不是 `policy_changed`/`stale_poll` 跳过）时刷新，反映"最近一次 poll 是否失败/降级"，与 `lifecycle='present'` 的 promotion 事实完全独立。当前生效版本的 `control_query_current_account_inventory_v1`（`00009` L1103-1240）只把 `health_degraded` 作为输出的 `provider_degraded` 展示字段，不参与 WHERE 过滤。
- **freshness 使用哪一个 DB timestamp / 阈值来源 / single database_now**：`account_inventory_provider_states.last_complete_at`（`00006` L119，NOT NULL，且 CHECK `last_complete_at = source_observed_at`，`00006` L138）。阈值是硬编码字面量 `interval '15 minutes'`，出现在 `control_query_current_account_inventory_v1` 函数体内（`00008` L141，`00009` L1222 与 L4927 附近各一次，三处字面量完全相同），**不是**任何配置表/常量/Go 代码里的可配置值；Go 代码（`internal/store/account_inventory_readonly_query.go`）里搜索不到该阈值，说明它只存在于这条 PL/pgSQL 函数体里。Phase 1B 若要复用，必须直接复用这个字面量语义（同一个 15 分钟窗口），不得引入第二个数值或让它可配置。一次 `ListEligibleOwnersByAccountKey` 或 `ListCrossNodeDuplicateCandidates` evaluation **MUST** 只取得一次 `clock_timestamp()`（如上 `WITH evaluation AS (SELECT clock_timestamp() AS database_now)`），所有参与判定的行统一对比同一个 `evaluation.database_now`，不得对每一行分别重新调用 `clock_timestamp()`（否则同一次 evaluation 内不同行可能因执行耗时跨越 15 分钟边界而得到不一致的 fresh/stale 判定，破坏"同一次 evaluation 内 fresh/stale 判定必须一致"的前提）。既有 `control_query_current_account_inventory_v1` 已经遵循这一范式（函数体首行 `database_now timestamptz := clock_timestamp();`，`00008` L44/`00009` L1116，只取一次），Phase 1A/1B 的新查询 MUST 保持同样的单次求值范式。
- **policy_changed / skipped promotion 如何排除**：`account_inventory_poll_provider_results.promotion_skipped_reason = 'policy_changed'`（或其它 `promotion_skipped_reason`）时 `promotion_applied = false`（CHECK `account_inventory_poll_provider_promotion_shape`，`00006` L20-24），该 provider 在该 poll run 上完全不产生 `account_inventory`/`account_inventory_provider_states` 的任何写入（`control_finalize_account_inventory_poll_run_with_lifecycle` 只在 `promotion_applied` 的 provider 上执行 UPDATE/INSERT 循环，`00007` L479-486 起的 FOR 循环条件），也就是说：eligible-owner predicate 天然排除它——它根本不出现在或不刷新 `account_inventory`/`account_inventory_provider_states` 里，无需额外 WHERE 条件。
- **suspected_missing / missing / out_of_scope 如何处理**：均不满足 `lifecycle = 'present'`，天然被上面的 predicate 排除，不需要额外分支。

### 输出二：absence / unverifiable matrix

| 情况 | owner present evidence | absence evidence | unverifiable | 依据 |
|---|---|---|---|---|
| fresh complete + account present | 是（`lifecycle='present'` 且 `last_complete_at` 在 15 分钟内） | 否 | 否 | `00007` L149-151 present shape；`00009` L1222 freshness |
| fresh complete + account absent（本次 promoted 快照未见该 account_key） | 否 | **是**（`lifecycle` 立即翻转为 `suspected_missing`，见下） | 否 | `00007` L479-509：仅当该 provider 本次 `promotion_applied` 且 `snapshot_items` 中不含该 `account_key` 时才发生此 UPDATE，天然要求"fresh+complete"前提 |
| suspected_missing（1 次连续 promoted 未见） | 否 | 是（第一次 fresh+complete 缺席证据） | 否 | `00007` L152-154；`consecutive_missing_count=1` |
| missing（2 次连续 promoted 未见） | 否 | 是（更强的 fresh+complete 缺席证据，`missing_since` 落定） | 否 | `00007` L155-157 |
| out_of_scope（provider 被移出 active policy scope） | 否 | **否**——`out_of_scope` 只表示"Control 主动停止监控"，不是"upstream 证明账号不存在"的证据 | **是**（对 ownership 判定而言不可验证，不能当作缺席证据） | `00007` L158-160；`account_inventory_provider_states.monitoring_status`（`00007` L64-70）语义是"是否仍在监控范围"，不是账号存在性 |
| stale（`state.last_complete_at` 超过 15 分钟） | 沿用上次已知 lifecycle（不因 stale 本身改变） | 否 | 是（无法用当前证据重新确认，只能作为 evidence_state=degraded 的 metadata） | `00009` L1222 freshness 判定；本身不改写 `lifecycle` |
| provider incomplete（`identity_complete=false` 或 `snapshot_complete=false`，即 `reason<>'complete'`） | 否（不产生新的 present 证据） | 否（不产生新的 absence 证据） | **是**——`promotion_applied` 恒为 false（CHECK `account_inventory_poll_provider_promotion_shape`，且触发器要求 `promotion_applied` 才允许覆盖 lifecycle），本次周期对 `account_inventory` 完全不写入 | `00006` L20-24；`00006` L280-317 |
| failed/timeout/abandoned（poll run 未 finalized 或 `contract_valid=false`） | 否 | 否 | 是——同上，未 promote，`account_inventory` 不受影响；仅 `health_degraded`/`health_reason` 被刷新为 metadata | `00005` L104-179 状态机；`00009` L1055-1084 health 刷新触发器 |
| disk fallback（`inventory_mode='disk_fallback'`） | 否 | 否 | 是——`control_validate_account_inventory_promotion` 强制 `promotion_applied` 需要 `inventory_mode='runtime'`，disk_fallback 永不 promote | `00006` L295-297 |
| policy_changed promotion skipped | 否 | 否 | 是——显式跳过，无任何写入 | `00006` L20-24 |

**重点回答**："fresh complete snapshot 中 account 不在当前 truth" 到底通过哪张表/字段证明：不是通过"历史 snapshot 里没有它"来猜测，而是通过 `account_inventory.lifecycle` 列本身的当前值（`present`/`suspected_missing`/`missing`）——这个值只在一次 `promotion_applied=true`（即 fresh、`contract_valid`、`runtime`、`node_identity_complete`、`snapshot_complete` 全部满足）的 poll run 里，由 `control_finalize_account_inventory_poll_run_with_lifecycle` 显式 UPDATE 推进（`00007` L479-509：对该 provider 本次 `snapshot_items` 中不存在的既有 `present`/`suspected_missing` 账号执行 lifecycle 降级）。也就是说，"缺席"从来不是通过比较两次快照的差集在查询时临时算出来的，而是 Account Inventory 自己的 finalize 事务在写入时就已经把"这次 fresh+complete 快照没有它"固化成了 `lifecycle` 列的当前值；cross-node duplicate 的 Phase 2 查询只需要读这个已经固化的 `lifecycle` 值，不需要、也不允许自己重新比较任何历史 snapshot。注意：这个"固化"依据是 `lifecycle` 列本身的新值，**不是** `account.current_poll_run_id`（该字段在 absence transition 时不会被更新，见上方"current_poll_run_id attribution"），absence evidence 引用应指向触发本次 transition 的 finalize/poll run 或 Provider current state，而非账号行上的陈旧 pointer。

### 输出三：query feasibility

**`ListEligibleOwnersByAccountKey(account_key)`**（给定一个 account_key，返回当前所有满足 predicate 的 Node）：

- 预计 SQL 形状：`WITH evaluation AS (SELECT clock_timestamp() AS database_now) SELECT account.instance_id FROM account_inventory account JOIN account_inventory_provider_states state ON (...) CROSS JOIN evaluation WHERE account.account_key = $1 AND account.lifecycle = 'present' AND state.state='current' AND evaluation.database_now - state.last_complete_at <= interval '15 minutes' AND ...`（即上面 predicate 加 `account_key = $1`，去掉 `instance_id = :node` 限制；同样只取一次 `database_now`）。
- 需要 join：`account_inventory` ⋈ `account_inventory_provider_states`（两表足够；不需要 `account_inventory_poll_runs`/`account_inventory_poll_provider_results`，理由见输出一）。
- 现有 index 是否覆盖：**不完全覆盖**。`account_inventory` 主键是 `(instance_id, account_key)`（`00007` L100），既有二级索引 `account_inventory_lifecycle_read_idx (instance_id, provider, lifecycle, account_key)`（`00007` L171-172）、`account_inventory_normalized_email_read_idx (instance_id, normalized_email, account_key)`、`account_inventory_basic_status_read_idx (instance_id, basic_status, account_key)`（均 `00008` L6-9）**全部以 `instance_id` 开头**，没有任何一个能支持"跨全部 Node、按 account_key 精确查找"而不做全表扫描。
- 是否会全表扫描：**是**，在当前 index 集合下，`WHERE account_key = $1`（不带 `instance_id`）只能走顺序扫描或对主键做全索引扫描后按 `account_key` 过滤（因为主键前导列是 `instance_id`，`account_key` 不是前导列，无法走 index 等值查找）。
- 约 1,000 accounts / 多 Node 时成本：单个 environment 内 Node 数量通常个位数到十几个，每 Node 账号数量上限受 `account_inventory_poll_runs_counts_bounded` 间接约束（每次 poll `source_record_count <= 1000`，`00005` L78-82），全表规模预计在"Node 数 × 账号数"量级（数千到一两万行），对全表扫描而言可接受，但**不是长期可扩展方案**——若 Node/账号规模显著增长，这类跨 Node 精确点查会退化。
- 是否需要 query-side 新 index：**若要避免全表扫描，需要**，例如 `(account_key) INCLUDE (instance_id, lifecycle)` 或 `(account_key, instance_id) WHERE lifecycle = 'present'`（partial index）。这类 index **只服务 Phase 2 的只读查询**，不引入任何新的 ownership truth 列，且必须单独 Review（见 §8 item 1 的 Phase 1A/1B 拆分原则）。

**`ListCrossNodeDuplicateCandidates()`**（找出所有当前存在 ≥2 个不同 Node 满足 predicate 的 account_key）：

- 预计 SQL 形状：`WITH evaluation AS (SELECT clock_timestamp() AS database_now) SELECT account.account_key FROM account_inventory account JOIN account_inventory_provider_states state ON (...) CROSS JOIN evaluation WHERE account.lifecycle='present' AND state.state='current' AND evaluation.database_now - state.last_complete_at <= interval '15 minutes' AND ... GROUP BY account.account_key HAVING count(DISTINCT account.instance_id) >= 2`（同样只取一次 `database_now`）。
- 需要 join：同上，两表。
- 现有 index 是否覆盖：**不覆盖**，同样因为需要按 `account_key` 聚合而非按 `instance_id` 过滤，现有 index 都以 `instance_id` 前导，`GROUP BY account_key` 无法利用任何现有 index 做聚合前排序/分组，只能靠顺序扫描 + 内存 hash 聚合。
- 是否会全表扫描：**是**（这是一次全量扫描 + 聚合的查询，天然如此，不太可能靠单一 index 完全避免，即使加了 `(account_key)` 前导 index，`HAVING count(...) >=2` 仍需要扫描该 account_key 的所有匹配行——但扫描范围会从"全表"收窄到"该 account_key 的所有行"，对全量 detection 场景来说，仍然需要遍历全表以枚举所有 account_key）。
- 约 1,000 accounts / 多 Node 时成本：全表规模数千到一两万行，做一次全表 `GROUP BY + HAVING` 的聚合查询在这个量级下是可接受的批量查询（每个 detection cycle 跑一次，不是每请求跑一次）；不适合作为高频交互式 API 查询，适合作为 Phase 3 detection worker 的周期性批量扫描。
- 是否需要 query-side 新 index：**可选而非必须**。若 detection cadence 较低（例如与 poll run 周期对齐，几分钟一次），全表扫描 + 内存聚合的成本可接受，可以不加新 index；若要压缩扫描范围（例如只扫 `lifecycle='present'` 的行），partial index `(account_key) WHERE lifecycle='present'` 有帮助，但同样只是 query-side 优化，不改变 ownership 语义。
- 是否需要 materialized ownership table：**不需要**。以上两个查询都可以用纯只读 SQL（必要时加 query-side index）在既有 `account_inventory`/`account_inventory_provider_states` 上完成，不需要新建一张物化的 "当前 ownership" 表来复制 Inventory truth。

**结论**：优先目标"纯 SQL query-derived，不新增 ownership truth"**可以达成**。Phase 1A 不需要新表；是否需要 1-2 个 query-side 索引留给 Phase 2 视实际 detection cadence/量级决定，且必须单独 Review，不与 Phase 1B 的 occurrence/evidence persistence 决策混在一起（见 §8 item 1）。

### 输出四：identity/privacy

- Phase 1A 的两个查询都在 PostgreSQL 内部对 `account_inventory.account_key`（plaintext，含 `normalized_provider:normalized_email`）做等值比较/分组，这已经发生在 Account Inventory 既有受保护数据边界内（`account_inventory` 表本身已经是受保护的 Control 内部表，非公开只读 API 直接暴露 plaintext），符合 design.md 第 6c 节"Detection 逻辑内部 MAY 在既有受保护 Account Inventory 数据边界内使用 plaintext account_key"的边界。
- Phase 1A 本轮**不新增任何 plaintext account_key persistence**——两个查询都是只读 SELECT，不写入任何新表/新列。
- Phase 1A 的调查过程与本文档**不将 account_key 输出到日志/metrics/普通 API**；本节仅在设计文档内以代码引用形式描述查询形状，不包含任何真实账号数据。
- 若后续 occurrence（Phase 1B/Phase 3）需要稳定的 safe identity 表示 account_key，本轮**不选择新算法**，留给 Phase 1B 按第 6c 节决定（复用既有 masked/HMAC 或安全引用）。

### 输出五：existing persistence survey（只调查，不设计）

在 `internal/store/`、`internal/api/`、`migrations/*.sql`（排除 `openspec/` 与本 change 自身）中搜索 `occurrence`、`incident`、`alert`、`acknowledg` 等词：

- **occurrence**：仅命中 `account_inventory_poll_duplicates.occurrence_count`（`00006` L84-103，node-local 重复计数列）与其对应 Go 代码；不存在任何通用 "occurrence" 实体/表/repository。**结论：不存在，需要 Phase 1B 自行设计**。
- **incident**：无命中（除本 change 自身的 openspec 文档）。**结论：不存在**。
- **alert / notification delivery**：在 `.go`/`.sql`（排除测试与 openspec）中**零命中**——Control 目前没有任何 alert/notification 持久化实体、表或 Go 包。**结论：不存在，Phase 6 需要从零决定 alert delivery 的持久化方式（或决定不持久化，只做只读展示+外部 webhook）**。
- **acknowledgement persistence**：搜索命中的唯一一处（`internal/store/account_inventory_lifecycle_failure_schema_integration_test.go:223`）是测试注释里的英文单词 "acknowledgement"（描述一次数据库写入确认），与人工确认/ack 功能无关。**结论：不存在**。
- **event/evidence history（可复用的通用审计基础设施）**：Control 已有 `audit_logs`（`migrations/00002_administrator_authentication.sql` L471-475：`CREATE TRIGGER audit_logs_reject_update_delete BEFORE UPDATE OR DELETE ON audit_logs`、`CREATE TRIGGER audit_logs_reject_truncate BEFORE TRUNCATE ON audit_logs`）——该表有数据库级 `BEFORE UPDATE OR DELETE`/`BEFORE TRUNCATE` rejection trigger，是真正的 immutable append-only pattern（不是"没有保护"的偶然 append-only，而是 DB 层强制拒绝任何修改/删除）；其 `category`/`action` 枚举是显式 allowlist（见 `00008` L167-184 等迁移中反复出现的 `audit_logs_category_valid`/`audit_logs_action_valid` CHECK 约束扩展模式）。但其语义是"哪个管理员做了什么管理操作"，是管理员审计日志，与"系统检测到的 ownership 冲突证据/occurrence 生命周期"语义完全不同（owner、触发主体、生命周期、查询维度均不同）。**结论：可以参考其 trigger-based rejection + CHECK allowlist 的保护范式（Phase 1B 设计 evidence observation 表时可以复用同样的"BEFORE UPDATE OR DELETE 拒绝触发器"模式来实现 append-only），但不能直接复用这张表本身来存储 occurrence/evidence 数据**。
- **Prometheus metrics 基础设施**：`internal/pollobservability/metrics.go`、`internal/historyruntime/metrics.go`、`internal/api/account_inventory_metrics.go`、`internal/store/gateway_directory_metrics.go` 等已建立通用 Prometheus 指标注册/命名模式。**结论：可复用其命名/注册模式（Phase 6 的 metrics 应遵循同样的包结构与标签约束），但没有现成的"occurrence 指标"，需要新增**。

**Phase 1B 输入（仅供后续 Phase 1B 参考，本轮不设计新表）**：不存在可直接复用的 generic occurrence/alert persistence；`audit_logs` 的 append-only 保护模式（触发器拒绝 UPDATE/DELETE + CHECK allowlist）值得作为 Phase 1B 设计 evidence observation 表时的参考范式，但需要一张新的、语义独立的最小 occurrence/evidence 表（对应 tasks.md 2.1-2.6，仍待 Phase 1B 单独设计与 Review）。

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
