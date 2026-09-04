# add-cross-node-duplicate-ownership Tasks

本轮只产出 OpenSpec（proposal/design/spec delta/tasks），不实现任何 Phase 的生产代码、Migration 或 UI。以下 Phase 任务均待后续独立实现轮次逐项勾选并附带证据；本轮全部保持 `[ ]`。

## 0. Contract freeze（本轮交付）

- [x] 0.1 对照 System Design R4.7（12.2 节）、Account Inventory Phase 2/3 已归档契约、Gateway Directory ingestion 与 Relay Node ↔ Gateway Account Binding 已归档契约，冻结 ownership source of truth、duplicate 定义、conflict identity、occurrence 生命周期、severity、evidence 模型与 Binding 边界
- [x] 0.2 冻结 non-goals：不自动修改 Gateway/CLIProxyAPI、不自动 unbind/rebind、不参与 routing/scheduling、不做 failover/retry/breaker、不自动修复 duplicate、不做 capacity recommendation、不把 Gateway binding/status 当作 ownership truth
- [x] 0.3 冻结 Phase 1A/1B、2–7 拆分边界与每阶段最小交付范围
- [x] 0.4 列出待实现阶段逐项决策但本轮不实现的技术边界（schema 拆分为 Phase 1A/1B、查询条件复用、freshness 阈值来源、worker cadence、事务边界、并发/lease/fencing、occurrence dedupe 约束、evidence 存储形态、ACTIVE/RESOLVED persistence、degraded metadata、reconciliation/restart、retention、acknowledgement、alert/notification 关系、账号识别信息展示字段、metrics 命名、read model/API、Topology 接入）

## 1. Phase 1A — source query feasibility

- [x] 1.1 证明 ownership source query（合格 owner 集合判定）能否直接复用既有 `account_inventory*` 表的只读查询满足 —— **证明成立**：`ListEligibleOwnersByAccountKey`/`ListCrossNodeDuplicateCandidates` 均可只 join `account_inventory` + `account_inventory_provider_states` 两表实现，无需新表，无需依赖 `state.current_poll_run_id` 非空（可空 retention pointer）；单次 evaluation 只取一次 `clock_timestamp()`；证据与精确 predicate 见 design.md "Phase 1A Investigation Findings"
- [x] 1.2 验证该判定不复制 Account Inventory 的 identity/lifecycle/current truth，只做只读查询 —— 已验证：两个候选查询均为纯 SELECT（含只读 `GROUP BY`/`HAVING`），不写入、不新增列，不复制 `lifecycle` 等字段到别处；已确认 `account_inventory.current_poll_run_id` 不携带 absence evidence（absence transition 不更新该字段），resolve evidence 引用不得使用它
- [x] 1.3 若确实无法通过既有查询满足，报告具体 query/index 缺口，query-side schema/index 变更单独 Review —— 已报告：现有 index 均以 `instance_id` 前导，不支持跨 Node 按 `account_key` 精确查找/聚合，需要时可加 `(account_key) INCLUDE (instance_id, lifecycle)` 或等价 partial index，该 index 变更留给 Phase 2 视 detection cadence 决定并单独 Review

## 2. Phase 1B — occurrence/evidence persistence

是否新增 occurrence/evidence schema 与 Phase 1A 是否需要新 index 是两个独立问题，互不隐式触发。

- [x] 2.1 调查是否存在可安全复用的 generic occurrence/alert persistence；若存在，评估复用可行性 —— 已确认不存在（design.md Phase 1A persistence survey），Phase 1B 需从零设计
- [x] 2.2 设计最小 additive occurrence/evidence persistence 的结构（occurrence mutable projection + affected-node child table + append-only evidence observation）—— 已在 design.md §1B.2/§1B.3/§1B.4 完成设计；`account_key` 直接复用 Account Inventory canonical plaintext 表示，不再是待定占位列
- [x] 2.3 明确 occurrence dedupe 唯一约束设计草案 —— 已给出：`(environment_id, account_key, conflict_type) WHERE status='ACTIVE'` 部分唯一索引（design.md §1B.2），RESOLVED 后释放，允许 reopen；另加 RESOLVED 行数据库层不可变触发器
- [x] 2.4 明确 evidence observation 的 append-only/immutable 实现方式（禁止 UPDATE/DELETE）—— 已完成：复用 `audit_logs` 的 `BEFORE UPDATE OR DELETE`/`BEFORE TRUNCATE` 拒绝触发器范式（design.md §1B.4）；`source_poll_run_id` 刻意不建 FK，避免 retention 删除触发系统级 UPDATE 命中拒绝触发器（design.md §1B.4/§1B.5）
- [x] 2.5 明确账号 identity 直接复用 plaintext canonical `account_key`，不新增第二套 fingerprint/HMAC identity —— 用户已明确决定原始账号/邮箱允许持久化与展示；此前的 Security Design Gap（fingerprint/HMAC 长期 dedupe 不可行）已撤销，因为其阻塞前提（account_key 必须脱敏）已被取消（design.md §1B.1/§1B.7）
- [x] 2.6 任何 Migration 设计草案必须先单独 Review，不得在本 Phase 静默提交 —— migrations/00013_cross_node_duplicate_ownership_foundation.sql 已完成独立 Migration Architecture Review（含 P1/P2 修正轮）；schema/privilege/up-down-up/history fail-closed acceptance 均通过

## 3. Phase 2 — current ownership query

- [x] 3.1 实现只读、bounded 的"当前合格 owner 集合"查询，直接复用 Phase 1A 冻结的 current/fresh/complete/eligible 判定 —— `ListEligibleOwnersByAccountKey` / `ListCrossNodeDuplicateCandidates`（`queries/cross_node_duplicate_ownership_query.sql`、`internal/store/cross_node_duplicate_ownership_query.go`），两个 SECURITY DEFINER 查询函数均在单次调用开始时只取得一次 `database_now := clock_timestamp()`，并对该次 evaluation 的全部行复用同一时间基准，逐字翻译 design.md 冻结谓词；`ListCrossNodeDuplicateCandidates` 按 account_key 聚合一次，不产生 Node pair
- [x] 3.2 验证该查询在既有 Account Inventory 数据上与既有 readonly-query fresh/stale 判定完全一致 —— `internal/store/cross_node_duplicate_ownership_query_schema_integration_test.go`：A/B fresh 均 present→duplicate；A fresh+B stale→B 不计；degraded 但 fresh/current/present 仍 eligible；suspected_missing/missing/out_of_scope 均不 eligible；retention-cleared `current_poll_run_id` 仍 eligible；15 分钟边界同一 database_now 下 fresh/stale 判定一致；3 Node 同 account_key→1 candidate+3 owners；未知 account_key→空；canonical account_key 校验拒绝非法输入。`go test -race` 全部通过。`EXPLAIN (ANALYZE, BUFFERS)`（260 行代表性 fixture）显示 `account_inventory` 上为 Seq Scan、`account_inventory_provider_states` 走既有 PK Index Scan，两查询均 <1ms；证据支持"当前规模顺序扫描可接受，暂不新增 index"的既定结论
- [x] 3.3 验证该查询不读取 Gateway Directory、Binding 或 scheduler/runtime 状态 —— SQL 文本仅 JOIN `account_inventory`/`account_inventory_provider_states`；"Gateway Directory/Binding mutation does not affect ownership query results" 子测试对同一 Node 插入 Gateway Directory snapshot + Binding 后重跑查询，结果不变

**Phase 2 生产 runtime 权限缺口 —— 已解决**：`relay_control_runtime` 对 `account_inventory` / `account_inventory_provider_states` 的 `REVOKE ALL`（migrations/00007 L841-843）保持不变、未被放宽。改为新增 additive migration `migrations/00014_cross_node_duplicate_ownership_query_access.sql`，沿用既有 `control_query_current_account_inventory_v1`（`00008`）的 SECURITY DEFINER readonly-query 范式，新增 `control_list_eligible_cross_node_owners_v1(account_key)` 与 `control_list_cross_node_duplicate_candidates_v1()` 两个 `STABLE SECURITY DEFINER` 函数（`OWNER TO relay_control_migrator`，`REVOKE EXECUTE FROM PUBLIC`，仅 `GRANT EXECUTE TO relay_control_runtime`）；`queries/cross_node_duplicate_ownership_query.sql` 改为调用这两个函数（不再直接 `SELECT account_inventory*`）。新增 `TestCrossNodeDuplicateOwnershipQueryRuntimePrivileges` 证明：(a) `database.runtime` 直接调用两个 repository 方法得到正确结果；(b) `database.runtime` 直接 `SELECT account_inventory`/`account_inventory_provider_states` 仍 `42501`；(c) PUBLIC 与非授权角色（`relay_control_asset_registrar`）对两个函数均无 `EXECUTE`。原 3.1–3.3 全部 10 个场景测试改为经由 `database.runtime` 执行并全部通过，`migrations/00014` 的 14→13→14 up/down/up acceptance 通过，且不改动 `00013` 的 persistence 历史。`ListCrossNodeDuplicateCandidates` 为 bounded batch detection query（bounded by 当前 Account Inventory 全量数据集），不是分页/交互式 API，第一版不因人为 LIMIT 漏检；详见 design.md「Phase 2 Implementation Findings」。

## 4. Phase 3 — detection + occurrence lifecycle

- [x] 4.1 实现 detect：首次证明合格 owner 数量 `>= 2` 时创建 ACTIVE occurrence，affected Node set 初始化为本次证明的合格 owner 集合 —— `CrossNodeDuplicateOwnershipLifecycleRepository.Evaluate`/`create`（`internal/store/cross_node_duplicate_ownership_lifecycle.go`）：顺序固定为 discovery（`ListEligibleOwnersByAccountKey`，`<2` 直接 no-op 不写入）→ `SelectClockTimestamp` 取单次 `evaluationAt` → `control_evaluate_cross_node_duplicate_evidence_v1` 权威重新分类 → `filterOwnerConfirmedCandidates` 过滤只保留 `owner_confirmed`，数量仍 `<2` 直接 no-op 不写入 → 只有 `owner_confirmed >= 2` 时才 `ON CONFLICT ... DO NOTHING RETURNING occurrence_id` 建 ACTIVE occurrence → 写入 confirmed 节点各一条 evidence → 更新 `latest_evaluation_id`；不得先 INSERT occurrence 再做 authoritative classification，避免 phantom occurrence。子测试 `detect: first duplicate creates ACTIVE occurrence with full affected set` 通过
- [x] 4.2 实现 refresh：occurrence 仍 ACTIVE 且未发生 membership 变化时刷新 `last_seen_at`/evidence，不创建新 occurrence；membership 变化按 4.4/4.5 处理 —— `reconcile()` 分支：`toAdd`/`toRemove` 均为空时走 `RefreshCrossNodeDuplicateOccurrenceProjection`，同一 `occurrence_id` 不变；子测试 `refresh: membership unchanged appends evidence without creating a new occurrence` 通过
- [x] 4.3 实现 degrade：evidence stale/failed/incomplete/unavailable 时只标记 unverifiable/degraded metadata，不 resolve、不缩减 affected Node set —— `reconcile()` 中 `anyDegradedRetained`（当前 affected Node 被评估为 `degraded` 或未被 `control_evaluate_cross_node_duplicate_evidence_v1` 返回，即无 provider-state 行可判定）时 `evidence_state='degraded'`、affected set 保持不变、不触发 resolve；子测试 `A/B duplicate + B stale: degrade, no resolve, affected set unchanged`、`A/B/C duplicate + C stale: no removal, evidence state degraded, still ACTIVE` 通过
- [x] 4.4 实现 affected Node set 增加：只有新 Node 自己的合格 evidence 证明其为 owner 才能加入 —— `reconcile()` 的 `toAdd`（eligible 且 `owner_confirmed`）先写 `owner_confirmed` evidence 再 `InsertCrossNodeDuplicateOccurrenceNode`；子测试 `add owner: A/B duplicate then C becomes fresh present joins the same occurrence` 通过
- [x] 4.5 实现 affected Node set 缩减：只有该 Node 自己的新合格 evidence 明确证明 account_key 已不 present 才能移除；stale/incomplete evidence 不得触发缩减 —— `reconcile()` 的 `toRemove`（`absence_confirmed`）严格按“先写 absence_confirmed evidence，再 `DeleteCrossNodeDuplicateOccurrenceNode`”顺序执行，满足 DB guard；degraded/无分类 Node 绝不进入 `toRemove`；子测试 `A/B duplicate + B fresh absent: remove then resolve`、`A/B/C duplicate + C fresh absent: removed but still ACTIVE (2 remain)` 通过
- [x] 4.6 实现 resolve：必须取得覆盖当前 affected Node set 全部 Node 的新合格 evidence，证明 present owner 数量 `<= 1` 才能 RESOLVE —— `reconcile()` 在 `evidenceState=="complete" && len(retained)<=1` 时执行 `ResolveCrossNodeDuplicateOccurrence`（ACTIVE→RESOLVED + `resolved_at`），写入顺序固定为 evidence→membership 调整→projection 更新→状态迁移，同一事务提交；子测试 `A/B duplicate + B fresh absent: remove then resolve` 通过
- [x] 4.7 实现 reopen：RESOLVED 后再次 duplicate 创建新 occurrence，验证不复用旧记录 identity —— partial unique index 只约束 `status='ACTIVE'`，`Evaluate()` 对已 RESOLVED 的 semantic key 走 `create()` 路径生成全新 `occurrence_id`/`first_seen_at`，旧 RESOLVED 行不被触碰；子测试 `reopen: RESOLVED history does not block a brand-new occurrence` 通过（并显式断言旧 occurrence 保持 RESOLVED 且行内容不变）
- [x] 4.8 验证 3+ Node 场景只产生一条 occurrence，不产生 pairwise occurrence/alert —— 子测试 `3+ Node duplicate is one occurrence, not pairwise` 断言 A/B/C 三个合格 owner 只产生一条 ACTIVE occurrence、三条 affected-node 行，`occurrenceRowCount` 恒为 1
- [x] 4.9 验证 Node-local duplicate 与 cross-node duplicate 使用独立事件/告警/metric，不合并 —— Phase 6 alert/metric 落地后最终关闭：persistence 独立性沿用 Phase 3 证据（cross-node lifecycle 从不写入 `account_inventory_poll_duplicates`）；alert 独立性：整个代码库对
      `account_inventory_poll_duplicates`/`PollDuplicate` 的引用里没有任何一处涉及
      metric/prometheus/collector/alert/slog（已 grep 确认零匹配），Node-local
      duplicate 从未拥有过告警机制，因此新增的 `CrossNodeDuplicateOwnershipAlertObserver`
      不可能与其共用或被其触发；metric 独立性：Node-local duplicate 同样没有任何
      Prometheus 指标，新增的 `relay_control_cross_node_duplicate_occurrences`
      只读 `cross_node_duplicate_occurrences` 表，与 `account_inventory_poll_duplicates`
      行数无关。测试证据：`TestCrossNodeDuplicateOwnershipIndependentFromNodeLocalDuplicates`
      在同一次 cross-node create（触发 1 次 active 告警 + metric ACTIVE 计数 +1）
      前后分别快照 `account_inventory_poll_duplicates` 行数，验证其行数不变，
      三个维度（persistence/alert/metric）在同一个测试里一次性证明独立，PASS。
- [x] 4.10 补验收场景：A/B duplicate+B stale 不 resolve；A/B duplicate+B fresh complete absent 可 resolve；A/B/C duplicate+C stale 不缩减；A/B/C duplicate+C fresh complete absent 可缩减但仍 ACTIVE —— 全部四个场景均有独立子测试覆盖并通过（见 4.3/4.5 引用的子测试名）；另补充：no-candidate（单一 owner 不建 occurrence）、非法输入拒绝、事务回滚（半途失败不留残留 occurrence/evidence 行）三个额外场景

**Phase 3 evidence-evaluation 读模型与新增 migration**：Phase 2 的 `ListEligibleOwnersByAccountKey` 只发现 candidate Node 集合，本身自带独立 `clock_timestamp()`，不能满足 lifecycle 要求的“同一 evaluation 内所有行复用同一时间基准”。为此新增最小只读函数 `control_evaluate_cross_node_duplicate_evidence_v1(target_account_key text, target_instance_ids uuid[], at_time timestamptz)`（`migrations/00015_cross_node_duplicate_ownership_evidence_evaluation_query_access.sql`）。`at_time` 的获取顺序（review 修正后固定）：existing occurrence 路径先 `SELECT ... FOR UPDATE` 取得行锁，再做 discovery，再取一次 `SelectClockTimestamp` 作为 `at_time`；无 existing occurrence 路径先确认当前无 ACTIVE row，discovery 得到 `>= 2` 候选后才取 `SelectClockTimestamp`；`ON CONFLICT DO NOTHING RETURNING` 为空（insert race）时丢弃所有预锁值，重新 `FOR UPDATE` 后重新 discovery + 重新取 `evaluationAt` + 重新 authoritative evaluation——全程 `evaluationAt` 不得在锁定/确认无行之前获取，`at_time` 由 Go 端透传，取代函数内部自行调用 `clock_timestamp()`；分类规则严格复用 Phase 1A 冻结谓词：`owner_confirmed`（current/fresh/complete 且 `lifecycle='present'`）、`absence_confirmed`（current/fresh/complete 且 `lifecycle IN ('suspected_missing','missing')`，或该 Node 完全没有该 account_key 的 `account_inventory` 行）、其余（非 fresh/current/complete，或 `lifecycle='out_of_scope'`，或无 provider-state 行）一律省略/归为 degraded，不使用 `current_poll_run_id` 作为 absence 依据。`SECURITY DEFINER`/`STABLE`/`search_path=pg_catalog`，`OWNER TO relay_control_migrator`，`REVOKE EXECUTE FROM PUBLIC`，仅 `GRANT EXECUTE TO relay_control_runtime`；不新增对 `account_inventory*` 的直接 SELECT 授权。新增 `TestCrossNodeDuplicateOwnershipEvidenceEvaluationRuntimePrivileges` 证明 `relay_control_runtime` 可 EXECUTE 且仍对 `account_inventory`/`account_inventory_provider_states` 无直接 SELECT（`42501`），PUBLIC/其它角色无 EXECUTE；`migrations/00015` 的 15→14→15 up/down/up acceptance（`TestCrossNodeDuplicateOwnershipEvidenceEvaluationMigrationUpDownUp`）通过，不触及 00013/00014 的表/函数/历史。Lifecycle 的表级 DML（`cross_node_duplicate_occurrences`/`_occurrence_nodes`/`_occurrence_evidence`）全部复用 00013 已授予的最小权限，未新增任何表级 GRANT。`go test -race ./internal/store/... -run TestCrossNodeDuplicateOwnership` 全量（Phase 1B/2/3 共 11 组）通过。

## 5. Phase 4 — reconciliation/concurrency

- [x] 5.1 设计并实现进程重启/补跑后的一致性重算（幂等，不重复创建 occurrence）。
      实现：`CrossNodeDuplicateOwnershipReconciler.Reconcile(ctx, environmentID)`
      （`cross_node_duplicate_ownership_reconciliation.go`），不复制第二套
      lifecycle 逻辑——只计算 key set 后逐 key 委派给既有 Phase 3
      `Evaluate()`。Key set = Phase 2 `ListCrossNodeDuplicateCandidates`
      （当前 duplicate）∪ 新增只读查询
      `ListActiveCrossNodeDuplicateOccurrenceKeys`（当前 ACTIVE occurrence，
      即使已跌出 candidate 列表也必须能被 resolve/degrade），按
      environment_id+account_key 去重、确定性排序。
      证据（`TestCrossNodeDuplicateOwnershipReconciliation`，7 子测试全 PASS）：
      restart 复用既有 ACTIVE occurrence；停机期间新增 duplicate 被创建；
      停机期间 Node fresh-absent 被 resolve；停机期间 Node stale 被
      degrade（不 resolve）；A/B/C 停机期间 C fresh-absent 缩减为 A/B 仍
      ACTIVE；连续 reconcile 2/3 次幂等（occurrence 数量/occurrence_id/
      membership 不变，不产生 pairwise occurrence）；非法 environment_id
      输入在触达数据库前被拒绝。
- [x] 5.2 评估并按需实现并发 lease/fencing，或证明幂等 upsert + 唯一约束已足够。
      **结论：Phase 4 不需要 lease/fencing。**
      证据（`TestCrossNodeDuplicateOwnershipConcurrency`，使用独立
      `pgxpool` 真实并发连接/事务，非 Go goroutine 内存模拟，8 子测试全
      PASS，并以 `-count=10` 重复验证无 flaky）：
      (1) 并发 first detect：两 worker 同 account_key 竞争，最终只有一条
      ACTIVE occurrence，两侧读到同一 occurrence_id——由 partial unique
      index 的 `INSERT ... ON CONFLICT DO NOTHING RETURNING` 空结果触发
      重新 `FOR UPDATE` + 重新 discovery/evaluate 的既有路径覆盖；
      (1b) 同一 insert-race fallback 分支额外用确定性 test-only hook
      （`crossNodeDuplicateOwnershipInsertRaceObserved`，仅存在于
      production `.go` 文件中的一个未导出包级变量，配合白盒 `_test.go`
      文件里导出的 `SetCrossNodeDuplicateOwnershipInsertRaceHookForTest`
      安装函数——该导出符号只存在于测试二进制，不出现在生产构建里）直接
      证明该分支被真正进入 >=1 次，而不是仅凭最终 occurrence_id 相同去
      推断；
      (2) 并发 refresh/refresh：由 occurrence `FOR UPDATE` 行锁天然串行化，
      membership 正确、无 lost update；
      (3) 并发 refresh/resolve：后拿到锁的一方重新读取 source truth 后
      决策，不会用锁前的判断覆盖已经 RESOLVED 的结果；
      (4) 并发 add/remove：同一行锁下 membership 变更不丢失；
      (5) 并发 reopen：RESOLVED 历史后两 worker 同时看到 duplicate，只创建
      一条新 ACTIVE occurrence，旧 RESOLVED 行不被触碰；
      (6) reconciler vs 正常 worker：`CrossNodeDuplicateOwnershipReconciler.Reconcile`
      与 `CrossNodeDuplicateOwnershipLifecycleRepository.Evaluate` 对同一
      ACTIVE duplicate 并发执行，最终只有一条 ACTIVE occurrence、
      occurrence_id 唯一、membership 正确；
      (7) reconciler vs 正常 worker 的 resolve-eligible 场景：两者对同一
      resolve-eligible account_key 并发执行，最终 occurrence 只能是
      RESOLVED，不会被恢复为 ACTIVE。
      以上场景均只依赖已有 DB 原语（ACTIVE partial unique index、
      occurrence `FOR UPDATE`、evidence
      `(occurrence_id,evaluation_id,instance_id)` 唯一约束、单事务
      lifecycle、single-statement evaluationAt/MVCC snapshot，见 5.3），
      未观察到 duplicate occurrence 重复创建、stale 决策覆盖、
      resolve/refresh membership 破坏、evidence/projection 不一致，或
      restart worker 与正常 worker 竞争产生错误状态；因此不引入分布式
      lease/fencing。
- [x] 5.3 补并发 detection/refresh/resolve 竞态的 PostgreSQL regression。
      见 5.2 证据（`TestCrossNodeDuplicateOwnershipConcurrency`，真实独立
      连接/事务）。另外发现并修复一个真实的 source-truth 快照竞态
      （Phase 4 review item F）：`control_evaluate_cross_node_duplicate_evidence_v1`
      原本在 READ COMMITTED 下，`SelectClockTimestamp` 与该函数自身的 SQL
      语句是两条独立语句，各自拿到独立快照；若 Inventory 恰好在
      `evaluationAt` 之后、第二条语句执行之前提交新 promotion，旧的
      `fresh_verifiable` 判断（`(at_time - last_complete_at) <= interval
      '15 minutes'`，在 `last_complete_at > at_time` 时因负数区间恒真）会把
      "evaluationAt 之后才出现的事实"误判为"evaluationAt 时刻已经存在"，
      从而持久化非法的 `source_completed_at > evaluation_at` evidence。
      **根因修复**：不再由 Go 分两条独立语句执行
      `SelectClockTimestamp` 再调用 evidence function，改为单一 sqlc
      查询 `EvaluateCrossNodeDuplicateEvidenceAtDatabaseNow`：
      `WITH evaluation AS (SELECT clock_timestamp()::timestamptz AS
      evaluation_at) SELECT evaluation.evaluation_at, to_jsonb(evaluated)
      FROM evaluation LEFT JOIN LATERAL
      control_evaluate_cross_node_duplicate_evidence_v1(...) AS evaluated
      ON TRUE`——`clock_timestamp()` 取值与 evidence 读取现在是同一条
      SQL 语句、共享同一个 MVCC snapshot，`LEFT JOIN LATERAL ... ON TRUE`
      保证即使 evidence 为空也仍能取得本次 evaluationAt。Go 侧
      `evaluateEvidenceAtDatabaseNowTx` 统一产出该 evaluationAt，供
      occurrence timestamps / evidence.evaluation_at / last_seen_at /
      resolved_at / first_confirmed_at 复用。
      migration `00016_cross_node_duplicate_ownership_evidence_evaluation_snapshot_guard.sql`
      作为 defense-in-depth 保留并加强：除原有
      `AND j.last_complete_at <= at_time AND j.health_scheduled_at <= at_time`
      外，新增 `AND j.state_updated_at <= at_time AND
      (j.account_updated_at IS NULL OR j.account_updated_at <= at_time)`
      两个上界谓词（联查 `account_inventory.updated_at`），因为 degraded
      的 `source_completed_at` 代理值取自 `state.updated_at`，而
      policy-only 迁移（如
      `control_activate_provider_policy_with_lifecycle`）可以推进
      `provider_states.updated_at`/`account_inventory.updated_at` 而不
      推进 `last_complete_at`/`health_scheduled_at`。
      测试证据：
      `TestCrossNodeDuplicateOwnershipEvidenceEvaluationSnapshotRace`
      （defense-in-depth 回归，人工传入更早 at_time 验证仍被拦截）、
      新增 `TestCrossNodeDuplicateOwnershipEvidenceEvaluationPolicyOnlyTimestampGuard`
      （用真实 `control_activate_provider_policy_with_lifecycle` policy-only
      迁移推进 `state.updated_at`/`account_updated_at` 至晚于 at_time，
      `last_complete_at`/`health_scheduled_at` 保持不变，验证函数返回
      0 行——不会把该行当作可持久化的 degraded evidence 返回）、
      `TestCrossNodeDuplicateOwnershipEvidenceEvaluationSnapshotGuardMigrationUpDownUp`
      验证 16→15→16 up/down/up 干净可逆、不触碰 00013 表/00014 函数，均
      PASS。

## 6. Phase 5 — read model/API

- [x] 6.1 设计并实现只读 occurrence 查询（按 Node、按 account_key、按状态）
      实现 `CrossNodeDuplicateOwnershipOccurrenceRepository`
      （`internal/store/cross_node_duplicate_ownership_read_model.go`）：
      `ListOccurrences`（bounded keyset pagination，`last_seen_at DESC,
      occurrence_id DESC`，过滤 status/account_key/instance_id）、
      `GetOccurrence`（detail + affected nodes）、
      `ListOccurrenceEvidence`（单独分页的 evidence history，keyset
      `recorded_at DESC, observation_id DESC`）。`relay_control_runtime`
      对 00013 三张 occurrence 表已有 `SELECT` 权限（见 00013 grants），
      因此无需新增 Migration/SECURITY DEFINER 函数/权限扩大，纯 sqlc 查询
      （`queries/cross_node_duplicate_ownership_occurrence_read_model.sql`）
      即可。9 个 store 层子测试
      （`TestCrossNodeDuplicateOwnershipOccurrenceReadModel`）覆盖按
      status/account_key/instance_id 过滤、detail 命中/未命中、非法
      limit/status、evidence 分页与 keyset 正确性，全部 PASS。
- [x] 6.2 实现只读查询返回账号识别信息（`account_key`/provider/normalized email），复用既有 `super_admin` 全量访问边界；不要求 masked/HMAC 处理
      API 层（`internal/api/cross_node_duplicate_occurrence_handlers.go`
      + `api/openapi.yaml` 新增 `GET /api/cross-node-duplicate-occurrences`、
      `GET /api/cross-node-duplicate-occurrences/{occurrence_id}`、
      `GET /api/cross-node-duplicate-occurrences/{occurrence_id}/evidence`）
      直接以明文返回 `account_key`（不做 mask/HMAC/fingerprint/rekey，
      冻结需求）。鉴权复用既有只读边界：`GetNodeRelayBinding`/`ListJobs`/
      `GetJob` 同款 `requireSession(w, r, false, "")`
      （任意已认证 Control 管理会话，无 mutation，无新增匿名/公开访问，
      与已经明文展示 account_key/email 的 Account Inventory 只读边界一致）。
      List 采用非签名的 opaque base64(JSON) cursor（区别于
      `JobCursorCodec`/`AccountInventoryCursorCodec` 的 HMAC 签名 cursor）——
      因为服务端始终会在 SQL WHERE 里重新套用 filters，被篡改的 cursor
      至多只能在调用方本就可见的数据范围内平移分页窗口，不能跨越权限/过滤
      边界，因此不必复制一套 HMAC codec（Phase 5 review 记录的刻意简化）。
      HTTP 层测试 `TestCrossNodeDuplicateOccurrenceHTTPReadOnly`
      （`internal/api/cross_node_duplicate_occurrence_http_integration_test.go`）
      9 子测试覆盖：未认证 401、明文 account_key/affected_nodes 返回、
      status 过滤空结果、非法 query 参数 400（status 枚举/limit
      越界/instance_id 非 UUID/cursor 非法/occurrence_id 非 UUID）、
      detail 命中/404、evidence 子资源、写方法 405、依赖未注入时 503，
      全部 PASS。
- [x] 6.3 设计 Node-centric Topology 后续接入方式（只读关联展示，不反向影响 Node/Binding/Inventory 状态）
      纯文档设计，见 `design.md`"Phase 5 — Topology Integration Note"：
      未来 Node-centric Topology 可在同一 Node 详情页并列展示
      Inventory/Binding/Binding Resolution/Duplicate Ownership（按
      `instance_id`/`account_key` read-only join），Duplicate Ownership
      不反向修改其它三者状态，也不影响其 detection/refresh 逻辑。本阶段
      不实现任何 Topology UI/聚合 endpoint。

## 7. Phase 6 — alerts/metrics acceptance

- [x] 7.1 决策 alert/notification delivery record 与 occurrence 的对应关系（是否 1:1），接入 ACTIVE/RESOLVED 告警；alert 可包含 `account_key`/provider/email 作为可读上下文，不要求 masked/HMAC 处理
      调查结论：项目里不存在任何通用 alert/notification persistence 表
      （唯一相关的 `operation_outbox` 是无关的 durable job 投递机制）；
      项目已确立的可观测性栈是 Prometheus（`cmd/control/main.go`
      `prometheus.NewRegistry()`/`MustRegister`/`/metrics`）+ 结构化
      `slog`（`internal/jobs/logging.go` `Logger` 接口 +
      `cmd/control/main.go` `jobSlogLogger` 适配器的既有模式）。遵循最小
      原则，不新建复杂 notification platform，改为在
      `CrossNodeDuplicateOwnershipLifecycleRepository` 上新增一个可选、
      默认 no-op 的 `CrossNodeDuplicateOwnershipAlertObserver` 观察者接口
      （`SetAlertObserver`，同 `SetRelayBindingRepository` 一样的
      additive/optional wiring 方式），在 `Evaluate()` 事务 `tx.Commit()`
      成功后调用，只在两种 transition 触发，1:1 对应 occurrence：
      - `Created==true`（首次 detect 或 RESOLVED 后 reopen）→ `active` 告警，
        reopen 产生的是全新 occurrence_id，因此天然产生新的 alert
        occurrence，不复用旧的；
      - `Created==false && Status=="RESOLVED"`（reconcile 内 ACTIVE→RESOLVED
        的唯一转折点）→ `resolved` 告警；
      - 其余情况（refresh/degrade/affected node add/remove 但仍 ACTIVE）
        不触发，因此同一 occurrence 在其生命周期内不会因为每次 refresh
        产生新告警，保持同一 alert 逻辑身份。
      Event context：`occurrence_id`/`environment_id`/`account_key`/
      `provider`（从 account_key 的 `provider:email` 前缀截取）/
      `affected_nodes`/`first_seen_at`（resolve 时取原始 occurrence 的
      first_seen_at，不是本次 evaluationAt）/`severity`（固定
      `"Critical"`）；不包含 credential/API key/access
      token/refresh token/password/Secret/raw upstream payload——
      occurrence/evidence 数据模型本身就没有这些字段。
      测试证据：`TestCrossNodeDuplicateOwnershipAlertObserver`
      （create→仅一次 active 告警；同证据 refresh→告警数不变；Node
      fresh-absent→resolve→第二次 resolved 告警，occurrence_id/severity/
      first_seen_at 与最初 active 告警一致；reopen→第三次 active 告警，
      occurrence_id 与原 occurrence 不同）、
      `TestCrossNodeDuplicateOwnershipAlertObserverDefaultIsNoop`
      （未安装观察者时 Evaluate 行为不变），全部 PASS。
      注：项目里没有任何触发 `Evaluate()`/`Reconcile()` 的生产 scheduler/
      worker（`cmd/control/main.go` 未构造
      `CrossNodeDuplicateOwnershipLifecycleRepository`）——这是历次
      Phase 说明里明确"不要新增 scheduler"共同作用的结果，触发机制不在
      本 change 范围内。因此本阶段只交付并测试可被任何未来调用方
      （测试/未来 scheduler/运维脚本）复用的、正确的 alert 观察者钩子，
      不在 `main.go` 里构造一个当前无人调用的 lifecycle repository 实例
      （那将是无效的死代码）。
- [x] 7.2 补 Prometheus metrics，验证命名遵循固定低基数标签约束（`environment`/`conflict_type`/`status`/`severity`），不得使用 `account_key`/email 作为 metrics label
      新增 `CrossNodeDuplicateOwnershipMetricsRepository`/
      `CrossNodeDuplicateOwnershipMetricsCollector`
      （`internal/store/cross_node_duplicate_ownership_metrics.go`），
      只用 `count(*) ... GROUP BY environment_id, status`
      （`queries/cross_node_duplicate_ownership_metrics.sql`，
      `relay_control_runtime` 已有 SELECT 权限，无需新 Migration/grant）
      暴露唯一指标 `relay_control_cross_node_duplicate_occurrences`
      （gauge），label 严格为 `environment`/`conflict_type`/`status`/
      `severity` 四个：`conflict_type`（固定
      `"cross_node_duplicate_ownership"`）与 `severity`（固定
      `"Critical"`）是常量 label，不来自任何查询列；从不使用
      `account_key`/email/`occurrence_id`/`instance_id` 作为 label。
      按 Collector 模式（同 `internal/jobs.Collector`）每次 scrape 现查
      一次，不缓存陈旧计数；provider 失败时返回
      `prometheus.NewInvalidMetric`，不返回陈旧值。已在
      `cmd/control/main.go` 注册进 `metricsRegistry`（与 metric 无关的
      trigger 缺失问题不影响这里——该 collector 只读现有
      `cross_node_duplicate_occurrences` 表当前行数，即使目前没有生产
      scheduler 写入新 occurrence，也能正确反映"当前为 0"）。
      测试证据：`TestCrossNodeDuplicateOwnershipMetricsCollector`
      （固定 label 值、`CollectAndCompare` 精确匹配预期文本、provider
      失败时 gather 报错、nil provider 被拒绝）、
      `TestCrossNodeDuplicateOwnershipMetricsRepository`（真实
      PostgreSQL，验证按 environment_id/status 分组正确），全部 PASS。
- [x] 7.3 验证 severity 固定 Critical，不受 Gateway binding/status 影响
      DB 层：`cross_node_duplicate_occurrences.severity` 受
      `CHECK (severity = 'Critical')`（00013）硬约束，整个代码库不存在
      任何写入路径可以写入其它值。Go 层：alert event 的 `Severity`
      字段与 metrics collector 的 `severity` label 都是硬编码常量字符串
      `"Critical"`，从不从 Gateway binding/Gateway Account
      status/scheduler state/traffic/health score 等来源读取或派生。
      `TestCrossNodeDuplicateOwnershipAlertObserver`/
      `TestCrossNodeDuplicateOwnershipMetricsCollector`
      均断言 severity 恒为 `"Critical"`。

## 8. Phase 7 — validation/runbook/archive

- [x] 8.1 完整回归：`make generate`（Go oapi-codegen/sqlc 与前端 `orval` 生成物均为零漂移，仅 Phase 5 新增的 3 个 occurrence 端点产生预期新增代码）、`go test -race ./internal/store/... -run TestCrossNodeDuplicateOwnership`（全部 PASS）、`go test ./internal/api/... -run CrossNodeDuplicate`（全部 PASS）、`go test ./...`（全仓库）。
  发现并修复一个由本 change migration 00013 引入的真实回归：`cross_node_duplicate_occurrences` 是第一个引用 `environments` 表的外键，导致 `TestAssetRegistryIdentityEndpointAndSecretConstraints` 的 `TRUNCATE environments` 断言从触发器 SQLSTATE `23514` 变为 FK-restrict SQLSTATE `0A000`（Postgres 在 BEFORE TRUNCATE 触发器执行前就因 FK 引用拒绝 TRUNCATE）。已在 `internal/store/asset_registry_schema_integration_test.go` 做最小修正：接受 `23514` 或 `0A000` 两种 SQLSTATE,两者都同样证明 environment 单例不可被 TRUNCATE。
  `go test ./...` 剩余 16 个失败全部证明为与本 capability 无关的既有缺陷：通过 `git worktree` 在本 change 最早提交（`1f54841`，`docs(openspec): archive relay node gateway account binding`）之前重新迁移一个干净数据库并重跑同一组测试,以下失败在 baseline 上逐字重现：`TestVerifyDatabaseIdentityAndRecovery`、`TestAccountInventoryHistoryMigrationEmptyDownUpAndChecksumGolden`、`TestAccountInventoryHistoryMigrationBackfillsHealthWithoutHistoryOrIdentityCopy`、`TestAccountInventoryHistoryMigrationBackfillsLegacyPollThenRetiresWithoutResurrection`、`TestAccountInventoryHistorySchemaACLAndProtectedDown`、`TestAccountInventoryReadonlyQueryMigrationPreservesExistingLifecycleState`、`TestAccountInventoryReadonlyQueryMigrationEmptyDownUpRestoresCompatibility`、`TestDurableJobProtectedDownRequiresEmptyEvidenceTables`、`TestGatewayDirectoryRecoveryEvidence`、`TestGatewayDirectorySensitiveValuesAreNotReflected`、`TestGatewayDirectoryMetricsSnapshotAndCollector`、`TestGatewayDirectoryRepositoryWorkflowAndRecovery`、`TestGatewayDirectoryCoordinatorSuccessChangedAndUnchanged`、`TestGatewayDirectoryCoordinatorSourceTimeInvalid`（均为迁移版本号硬编码于测试字面量、或 gateway_directory 测试之间共享开发数据库产生的 FK/唯一约束交叉污染，与 account_key/duplicate ownership 逻辑无关）。另外两个失败（`TestGatewayDirectoryAndRelayBindingMigrationsUpDownUp`、`TestRelayBindingRuntimeLockPrivilegeMigrationUpDownUp`）属于同一类"硬编码迁移版本号"缺陷,只是恰好在 baseline（迁移版本=12）下尚未触发,在本 change 新增 00013–00016 后（迁移版本=16）暴露;不是本 capability 的逻辑回归,且用户明确要求不为此类既有缺陷顺手修改无关测试。
  本 capability 自身相关测试（`internal/store` 下 `TestCrossNodeDuplicateOwnership*`、`internal/api` 下 `TestCrossNodeDuplicateOccurrenceHTTPReadOnly`）全部 PASS,无一失败。
- [x] 8.2 新增 `docs/runbooks/cross-node-duplicate-ownership.md`,覆盖 duplicate 定义、owner eligibility、detect/refresh/degrade/resolve/reopen、restart reconciliation、并发模型（含"为什么不需要 lease/fencing"结论与证据摘要）、evidence 来源语义（stale ≠ absence）、severity/alert/metrics、read API、troubleshooting、non-goals 与验收命令,风格与既有 `relay-node-gateway-account-binding.md` 一致。
- [ ] 8.3 `openspec validate add-cross-node-duplicate-ownership --type change --strict --no-interactive` 通过后归档（validate 已通过,archive 按指示暂不执行,等待最终 Review）
