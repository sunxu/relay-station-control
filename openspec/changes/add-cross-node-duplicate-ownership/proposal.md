# add-cross-node-duplicate-ownership Proposal

## Why

System Design v1.8 R4.7（第 12.2 节）已冻结 Cross-node Duplicate Ownership 的正式定义、evidence 边界、conflict identity 和 Alert 生命周期，但 Control 尚未有任何 OpenSpec change 冻结落地契约。当前 Relay Node Inventory（Phase 2/3）已经能够按 Provider 生成 current、fresh、complete、eligible 快照，Gateway Directory ingestion 和 Relay Node ↔ Gateway Account binding 也已独立落地，但没有能力检测同一个规范化 `account_key` 被两个不同 Node 同时声明为 current ownership 的严重配置错误。若不冻结这一契约，未来实现容易被误导使用 stale/failed inventory、Gateway Directory、binding 或 scheduler/runtime 状态作为 ownership evidence，从而产生 pairwise alert 爆炸或把无关证据当作 ownership fact。

## What Changes

- 冻结 Cross-node Duplicate Ownership 的 ownership source of truth：只允许使用各 Node/Provider 最近一次已成功 promotion 的 current、fresh、complete、eligible runtime Inventory 作为 evidence；禁止使用历史/72h/stale/failed/incomplete inventory、Gateway Directory、Gateway binding、Account status 或 scheduler/runtime state 创造、消除或覆盖 ownership fact。
- 冻结 duplicate 定义：同一 `account_key` 同时出现在至少两个不同 Relay Node 的合格 current Inventory 中即为 cross-node duplicate ownership；Node-local duplicate 保持独立语义，不与 cross-node conflict 合并。
- 冻结 conflict identity：`environments.environment_id + account_key + conflict_type` 在语义上固定为单一 logical key，每个 occurrence 领域语义上关联一个 affected Node set（物理表示留给实现阶段决策）；`environments.name`/display label 不得作为 identity；Node pair 永远不得成为 occurrence identity。
- 冻结 affected Node set 增减规则：新 Node 加入需要该 Node 自己新的合格 evidence 证明其为 owner；Node 移出需要该 Node 自己新的合格 evidence 明确证明账号已不在其上 present；stale/failed/incomplete evidence 不得导致移出；resolve 必须以覆盖 occurrence 当前全部 affected Node 的新合格 evidence，证明 present owner 数量 `<= 1`。
- 冻结 occurrence 生命周期：首次检测创建 ACTIVE occurrence；持续存在刷新同一 occurrence；evidence 退化只标记 unverifiable/degraded，不 resolve 也不缩减 affected Node set；满足覆盖条件的合格 evidence 才能 RESOLVE；RESOLVED 后再次 duplicate 创建新 occurrence，可复用 logical key 统计复发。
- 冻结 severity：cross-node duplicate 固定 Critical，不因 Gateway binding/use/status 动态改变；occurrence lifecycle 本身只有 `ACTIVE | RESOLVED` 两个状态，alert/notification delivery record 与 occurrence 的对应关系（是否 1:1）留给 Phase 6 决策，本轮不预先假定两者是同一记录。
- 冻结最小 evidence 模型，区分 occurrence mutable projection（随状态更新）与 append-only evidence observation（写入后不可 UPDATE/DELETE）：只保存涉及 Node ID、各 Node 对应 current promoted snapshot/evidence identity、observed_at/evidence freshness、occurrence detect/refresh/resolve 时间和 resolve evidence；不保存 credential、secret 或 raw upstream payload。
- 冻结账号识别信息边界：`account_key = normalized_provider + ':' + normalized_email` 仍是 Account Inventory 唯一 ownership identity，不建立第二套 fingerprint/HMAC identity；`account_key`/normalized email/原始账号识别信息允许在 Control 中直接持久化与展示（管理界面/授权 API/alert），不要求 masked/HMAC 处理；Prometheus metrics label 仍不得使用完整 email/account_key 作为高基数标签；credential、API key、access token、refresh token、password、Secret 实际内容、raw upstream payload 仍是本 capability 的禁止敏感字段。
- 冻结 Binding 关系边界：Inventory Ownership Fact、Gateway Binding、Binding Resolution 三者相互独立；detection/refresh/resolve 不得依赖 Binding。
- 冻结需要在实现阶段逐项决策但本轮不实现的技术边界（schema 拆分为 Phase 1A query feasibility 与 Phase 1B occurrence/evidence persistence、查询条件、freshness 阈值复用来源、detection worker cadence、事务边界、并发/lease/fencing、occurrence dedupe 唯一约束、evidence 存储形态、ACTIVE/RESOLVED persistence、unverifiable/degraded metadata、reconciliation/restart 行为、retention、acknowledgement、alert/notification 关系、账号识别信息展示字段、metrics 命名、read model/API 需求、Node-centric Topology 接入方式）。
- 冻结 non-goals：不自动修改 Gateway/CLIProxyAPI，不自动 unbind/rebind，不参与 routing/scheduling，不做 failover/retry/breaker，不自动修复 duplicate，不做 capacity recommendation，不把 Gateway binding/status 当作 ownership truth。

本轮只产出 proposal/design/spec delta/tasks，不写生产代码、不新增 Migration、不实现 UI。

## Capabilities

### New Capabilities

- `cross-node-duplicate-ownership`：定义跨 Node 重复账号归属检测的 evidence source of truth、duplicate 定义、conflict identity、occurrence 生命周期、severity、evidence 模型和与 Binding/Gateway 状态的边界。

### Modified Capabilities

- None。Relay Node Inventory（Phase 2/3）、Gateway Directory ingestion 和 Relay Node ↔ Gateway Account Binding 的既有契约保持不变；本 change 只读取既有 current/fresh/complete/eligible Inventory 状态，不修改其 schema、promotion 规则或语义。

## Impact

- **仓库**：后续实现仅涉及 `control`。
- **Persistence**：本轮不新增 Migration；后续实现阶段若确需新增 schema，必须先停止并单独报告，不得在本 change 或后续 change 中静默引入。
- **Inventory/Directory/Binding**：本 change 不修改 account inventory poll/snapshot/lifecycle/readonly-query 的既有契约，不修改 Gateway Directory ingestion 契约，不修改 Relay Node ↔ Gateway Account Binding 契约；三者只作为只读 evidence 来源或独立 context，不互相覆盖。
- **Gateway/Node**：不修改 Sub2API Account/Group，不修改 CLIProxyAPI credential/provider/account，不访问 Gateway PostgreSQL，不做任何自动修复动作。
- **数据面**：不进入请求路径，不实现 routing、scheduler、weights、retry、failover 或自动 rebinding/unbind。
- **后续 change**：detection worker、read model/API、alert/metrics 落地和 schema 实现均留给按 Phase 拆分的后续 change 或本 change 的后续实现轮次。
