## MODIFIED Requirements

### Requirement: PostgreSQL SHALL 保存不可变且字段白名单化的账号快照

PostgreSQL SHALL 只为 promotion-applied 的完整 runtime Provider 保存 `account_inventory_snapshot_items`。每个 item MUST 关联 finalized poll run、同一 Node 和 pinned active Provider，并保存 account key、标准化 provider/email、封闭基础状态、有界计数、受校验源时间和数据库 `observed_at`；允许可空availability_runtime_evidence与auth_failure_reason安全枚举，仅用于Antigravity认证文件可用性观察，随同一合格promotion原子复制到account_inventory。唯一键 MUST 防止同一 poll/Node/account key 重复。快照和重复证据在提交后 MUST 不可更新。

#### Scenario: 完整 runtime Provider 包含账号
- **WHEN** Provider 集合完整、策略未变化且 finalize 输入中的 key/email/status/计数合法
- **THEN** PostgreSQL 为每个唯一账号保存一条不可变 snapshot item，并使 item/poll/Node/Provider 关系可由约束验证

#### Scenario: 非法 item 绕过应用写入
- **WHEN** 输入包含错误 Node、非 pinned Provider、key 与 provider/email 不一致、非法状态、负数/溢出计数或重复 key
- **THEN** PostgreSQL 拒绝整个 finalize，poll、Provider 结果、快照、重复证据和当前指针均不产生部分变化

#### Scenario: 终态后修改快照
- **WHEN** 运行时或维护误操作尝试 UPDATE/DELETE/TRUNCATE snapshot item 或 duplicate evidence
- **THEN** 最小权限和不可变保护拒绝操作，历史证据保持不变

#### Scenario: Migration 前已有 finalized Provider 证据
- **WHEN** additive Migration 遇到旧 finalize 契约形成且没有 promotion 判定的 Provider 结果
- **THEN** 历史行保持 snapshot-complete 聚合并以 promotion false/reason NULL 表示未评估，不伪造 snapshot、当前指针或 skipped 原因

#### Scenario: Additive availability metadata
- **WHEN** 新合格promotion携带安全可用性枚举或旧writer未携带
- **THEN** 分别保存受CHECK约束的安全值或NULL；不回填旧snapshot/current、不改lifecycle/basic_status/Provider eligibility，raw错误和Token仍不允许持久化

#### Scenario: Incomplete evidence cannot refresh availability
- **WHEN** Provider不完整或不合格promotion
- **THEN** 不刷新current安全projection；availability读取根据最新Provider健康/失败将旧证据视为UNKNOWN，不能伪造新的完整观察
