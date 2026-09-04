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

- [ ] 4.1 实现 detect：首次证明合格 owner 数量 `>= 2` 时创建 ACTIVE occurrence，affected Node set 初始化为本次证明的合格 owner 集合
- [ ] 4.2 实现 refresh：occurrence 仍 ACTIVE 且未发生 membership 变化时刷新 `last_seen_at`/evidence，不创建新 occurrence；membership 变化按 4.4/4.5 处理
- [ ] 4.3 实现 degrade：evidence stale/failed/incomplete/unavailable 时只标记 unverifiable/degraded metadata，不 resolve、不缩减 affected Node set
- [ ] 4.4 实现 affected Node set 增加：只有新 Node 自己的合格 evidence 证明其为 owner 才能加入
- [ ] 4.5 实现 affected Node set 缩减：只有该 Node 自己的新合格 evidence 明确证明 account_key 已不 present 才能移除；stale/incomplete evidence 不得触发缩减
- [ ] 4.6 实现 resolve：必须取得覆盖当前 affected Node set 全部 Node 的新合格 evidence，证明 present owner 数量 `<= 1` 才能 RESOLVE
- [ ] 4.7 实现 reopen：RESOLVED 后再次 duplicate 创建新 occurrence，验证不复用旧记录 identity
- [ ] 4.8 验证 3+ Node 场景只产生一条 occurrence，不产生 pairwise occurrence/alert
- [ ] 4.9 验证 Node-local duplicate 与 cross-node duplicate 使用独立事件/告警/metric，不合并
- [ ] 4.10 补验收场景：A/B duplicate+B stale 不 resolve；A/B duplicate+B fresh complete absent 可 resolve；A/B/C duplicate+C stale 不缩减；A/B/C duplicate+C fresh complete absent 可缩减但仍 ACTIVE

## 5. Phase 4 — reconciliation/concurrency

- [ ] 5.1 设计并实现进程重启/补跑后的一致性重算（幂等，不重复创建 occurrence）
- [ ] 5.2 评估并按需实现并发 lease/fencing，或证明幂等 upsert + 唯一约束已足够
- [ ] 5.3 补并发 detection/refresh/resolve 竞态的 PostgreSQL regression

## 6. Phase 5 — read model/API

- [ ] 6.1 设计并实现只读 occurrence 查询（按 Node、按 account_key、按状态）
- [ ] 6.2 实现只读查询返回账号识别信息（`account_key`/provider/normalized email），复用既有 `super_admin` 全量访问边界；不要求 masked/HMAC 处理
- [ ] 6.3 设计 Node-centric Topology 后续接入方式（只读关联展示，不反向影响 Node/Binding/Inventory 状态）

## 7. Phase 6 — alerts/metrics acceptance

- [ ] 7.1 决策 alert/notification delivery record 与 occurrence 的对应关系（是否 1:1），接入 ACTIVE/RESOLVED 告警；alert 可包含 `account_key`/provider/email 作为可读上下文，不要求 masked/HMAC 处理
- [ ] 7.2 补 Prometheus metrics，验证命名遵循固定低基数标签约束（`environment`/`conflict_type`/`status`/`severity`），不得使用 `account_key`/email 作为 metrics label
- [ ] 7.3 验证 severity 固定 Critical，不受 Gateway binding/status 影响

## 8. Phase 7 — validation/runbook/archive

- [ ] 8.1 完整回归：`make generate`、相关 store/api 测试、`go test -race`、`go test ./...`
- [ ] 8.2 更新 runbook，覆盖 detect/refresh/degrade/resolve/reopen、severity、evidence 边界与 non-goals
- [ ] 8.3 `openspec validate add-cross-node-duplicate-ownership --type change --strict --no-interactive` 通过后归档
