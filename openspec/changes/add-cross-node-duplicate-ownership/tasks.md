# add-cross-node-duplicate-ownership Tasks

本轮只产出 OpenSpec（proposal/design/spec delta/tasks），不实现任何 Phase 的生产代码、Migration 或 UI。以下 Phase 任务均待后续独立实现轮次逐项勾选并附带证据；本轮全部保持 `[ ]`。

## 0. Contract freeze（本轮交付）

- [x] 0.1 对照 System Design R4.7（12.2 节）、Account Inventory Phase 2/3 已归档契约、Gateway Directory ingestion 与 Relay Node ↔ Gateway Account Binding 已归档契约，冻结 ownership source of truth、duplicate 定义、conflict identity、occurrence 生命周期、severity、evidence 模型与 Binding 边界
- [x] 0.2 冻结 non-goals：不自动修改 Gateway/CLIProxyAPI、不自动 unbind/rebind、不参与 routing/scheduling、不做 failover/retry/breaker、不自动修复 duplicate、不做 capacity recommendation、不把 Gateway binding/status 当作 ownership truth
- [x] 0.3 冻结 Phase 1A/1B、2–7 拆分边界与每阶段最小交付范围
- [x] 0.4 列出待实现阶段逐项决策但本轮不实现的技术边界（schema 拆分为 Phase 1A/1B、查询条件复用、freshness 阈值来源、worker cadence、事务边界、并发/lease/fencing、occurrence dedupe 约束、evidence 存储形态、ACTIVE/RESOLVED persistence、degraded metadata、reconciliation/restart、retention、acknowledgement、alert/notification 关系、masked identity、metrics 命名、read model/API、Topology 接入）

## 1. Phase 1A — source query feasibility

- [ ] 1.1 证明 ownership source query（合格 owner 集合判定）能否直接复用既有 `account_inventory*` 表的只读查询满足
- [ ] 1.2 验证该判定不复制 Account Inventory 的 identity/lifecycle/current truth，只做只读查询
- [ ] 1.3 若确实无法通过既有查询满足，报告具体 query/index 缺口，query-side schema/index 变更单独 Review

## 2. Phase 1B — occurrence/evidence persistence

是否新增 occurrence/evidence schema 与 Phase 1A 是否需要新 index 是两个独立问题，互不隐式触发。

- [ ] 2.1 调查是否存在可安全复用的 generic occurrence/alert persistence；若存在，评估复用可行性
- [ ] 2.2 若无法复用，设计最小 additive occurrence/evidence persistence：schema 只保存 conflict lifecycle/evidence（occurrence mutable projection + append-only evidence observation），不复制 Inventory truth
- [ ] 2.3 明确 occurrence dedupe 唯一约束设计草案（同一 `logical_conflict_key` 在 ACTIVE 状态下唯一）
- [ ] 2.4 明确 evidence observation 的 append-only/immutable 实现方式（禁止 UPDATE/DELETE）
- [ ] 2.5 明确 account_key 持久化边界：默认使用既有 masked/HMAC identity 或安全引用；若必须持久化 plaintext account_key，单独发起 security review 并停止等待批准
- [ ] 2.6 任何 Migration 设计草案必须先单独 Review，不得在本 Phase 静默提交

## 3. Phase 2 — current ownership query

- [ ] 3.1 实现只读、bounded 的"当前合格 owner 集合"查询，直接复用 Phase 2/3 冻结的 current/fresh/complete/eligible 判定
- [ ] 3.2 验证该查询在既有 Account Inventory 数据上与既有 readonly-query fresh/stale 判定完全一致
- [ ] 3.3 验证该查询不读取 Gateway Directory、Binding 或 scheduler/runtime 状态

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
- [ ] 6.2 实现 masked identity 输出，复用既有 Account Inventory masked/HMAC 与 `super_admin` 全量访问边界；不输出 plaintext account_key
- [ ] 6.3 设计 Node-centric Topology 后续接入方式（只读关联展示，不反向影响 Node/Binding/Inventory 状态）

## 7. Phase 6 — alerts/metrics acceptance

- [ ] 7.1 决策 alert/notification delivery record 与 occurrence 的对应关系（是否 1:1），接入 ACTIVE/RESOLVED 告警；semantic alert fingerprint identity = `environments.environment_id + alert_type + account_key`，但实际 alert label/emitted fingerprint material MUST 使用既有 masked/HMAC safe identity representation，不得输出 plaintext account_key
- [ ] 7.2 补 Prometheus metrics，验证命名遵循既有标签约束，account_key 只以 masked/HMAC 形式出现
- [ ] 7.3 验证 severity 固定 Critical，不受 Gateway binding/status 影响

## 8. Phase 7 — validation/runbook/archive

- [ ] 8.1 完整回归：`make generate`、相关 store/api 测试、`go test -race`、`go test ./...`
- [ ] 8.2 更新 runbook，覆盖 detect/refresh/degrade/resolve/reopen、severity、evidence 边界与 non-goals
- [ ] 8.3 `openspec validate add-cross-node-duplicate-ownership --type change --strict --no-interactive` 通过后归档
