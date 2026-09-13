# account-inventory-lifecycle Specification

## Purpose
为 Control 建立由完整且已提升的 runtime Provider 快照驱动、受数据库事务与 fencing 保护的当前账号生命周期真相，同时明确连续缺失、恢复、退出监控范围、敏感身份和内部读取边界。

## Requirements

### Requirement: PostgreSQL SHALL 保存唯一且受保护的当前账号生命周期

PostgreSQL SHALL 以 `(instance_id, account_key)` 唯一保存 `account_inventory`。每行 MUST 包含标准化 provider/email、最近完整基础状态、`first_seen_at`、`last_seen_at`、`consecutive_missing_count`、封闭 lifecycle `present|suspected_missing|missing|out_of_scope`、可空 `missing_since`/`out_of_scope_since`、最近来源元数据和可空 current poll 外键。生命周期与时间字段组合 MUST 由数据库约束保护，current poll 删除时 MAY `ON DELETE SET NULL`，但复制的来源元数据 MUST 保持可解释。

#### Scenario: 首次完整 promotion 发现账号
- **WHEN** 生命周期表尚无该 `(instance_id, account_key)` 且所属 Provider 的完整 runtime snapshot promotion 成功
- **THEN** PostgreSQL 创建 `present` 行，first/last seen 取本次数据库 observed time、缺失计数为零且 missing/out-of-scope 时间为空

#### Scenario: 非法生命周期组合绕过应用写入
- **WHEN** 写入包含错误 account key/provider/email 关系、非法 lifecycle、负数计数、present 携带 missing_since 或 missing 携带空 missing_since
- **THEN** PostgreSQL 拒绝整个受控事务且不留下部分 lifecycle、snapshot 或 poll 变化

#### Scenario: current poll 历史将来被受控清理
- **WHEN** 后续保留 change 合法删除当前来源 poll 并使 lifecycle 外键置空
- **THEN** 账号仍保留最近来源时间、版本、提交和基础状态，不把空外键解释为账号未知或缺失

### Requirement: 只有 promotion-applied runtime Provider SHALL 推进生命周期

只有当前 poll 对某 active Provider 同时满足 contract-valid runtime、provider snapshot complete、Node active、当前 monitoring eligible、策略未变化、槽位较新且 `promotion_applied=true` 时，Control SHALL 使用该 Provider 的完整 snapshot item 集推进生命周期。transport/HTTP/contract 失败、disk fallback、Provider 不完整、重复身份、`policy_changed`、`monitoring_ineligible`、`node_retired`、`node_replaced`、`stale_poll`、pending/retry/running/abandoned 槽、Gateway/Compose 状态和未提交事务 MUST NOT 增加、清零或重解释任何账号缺失状态。

Promotion MUST additionally prove the Node is active, retains the same stable identity and has a
current eligible monitoring interval. Retired/replaced old Nodes cannot advance
`account_inventory` lifecycle or be retargeted to a replacement identity.

#### Scenario: Node lifecycle delta
- **WHEN** the Node lifecycle condition described by this change is evaluated
- **THEN** the existing baseline behavior remains intact and the lifecycle fence is also enforced

#### Scenario: disk fallback 或 Provider 不完整
- **WHEN** poll finalized 但某 Provider 未形成 promotion-applied runtime 快照
- **THEN** 该 Provider 全部 lifecycle 行保持原值，既不累计 missing 也不把已有 missing 恢复为 present

#### Scenario: monitoring-ineligible evidence 不推进 lifecycle
- **WHEN** active Node 的 poll 以 `promotion_skipped_reason=monitoring_ineligible` finalized
- **THEN** transport/Provider historical evidence 保留，但账号 lifecycle、availability、
  request-quality 与 Provider current health 均不推进

#### Scenario: 合法完整空集合
- **WHEN** active Provider 的完整 runtime 空集合成功 promotion
- **THEN** Control 将该 Provider 现有 active lifecycle 行按连续缺失规则推进，而不是把空集合当作无证据

#### Scenario: 较旧 poll 迟到
- **WHEN** 较新槽已经推进 Provider 当前来源和 lifecycle，较旧 poll 随后 finalize
- **THEN** 较旧 poll 以 stale_poll 跳过 promotion且 lifecycle 完全不变

### Requirement: 连续完整缺失 SHALL 确定性转换 present、suspected_missing 与 missing

对每次合格 promotion，本轮实际出现的账号 SHALL 写为 `present`、刷新最近基础状态与来源、设置 `last_seen_at` 为本次 observed time、将 `consecutive_missing_count` 清零并清空 `missing_since`。未出现且当前属于该 active Provider 的 `present` 账号 SHALL 转为 `suspected_missing` 并设计数 1；下一次连续合格 promotion 仍未出现时 SHALL 转为 `missing`、设计数 2 并将 `missing_since` 固定为第二次缺失观察时间。后续连续缺失 MUST 饱和在 2 且不得改写 `missing_since`。

#### Scenario: 第一次完整缺失
- **WHEN** 一个 present 账号未出现在所属 Provider 的下一次合格完整 promotion
- **THEN** lifecycle 原子变为 suspected_missing、计数为 1、missing_since 仍为空且 last_seen_at 保持最后实际出现时间

#### Scenario: 第二次及后续完整缺失
- **WHEN** suspected_missing 账号在下一次连续合格 promotion 仍未出现
- **THEN** lifecycle 变为 missing、计数为 2并记录本次 observed time；再缺失时状态、计数和 missing_since 保持稳定

#### Scenario: 中间槽无合格 promotion
- **WHEN** 第一次完整缺失后若干槽因失败、降级或策略变化未提升，随后下一次合格 promotion 仍缺失
- **THEN** 只有两个实际 promotion 构成连续缺失证据，账号在后一 promotion 转为 missing

### Requirement: 账号重新出现 SHALL 恢复 present 且保持首次见证

`suspected_missing` 或 `missing` 账号在所属 active Provider 的合格 promotion 中重新出现时 SHALL 原子恢复为 `present`，清零缺失计数与 `missing_since`，刷新 `last_seen_at`、基础状态和来源，但 MUST 保留原 `first_seen_at`。恢复不得要求人工操作或额外 Node 请求。

#### Scenario: suspected_missing 后重新出现
- **WHEN** 账号第一次完整缺失后在下一次合格 promotion 中出现
- **THEN** 账号恢复 present、缺失计数归零且不曾形成 missing_since

#### Scenario: missing 后重新出现
- **WHEN** missing 账号在后续合格 promotion 中出现
- **THEN** 账号恢复 present并清空 missing_since，同时保留历史 first_seen_at

### Requirement: Provider 范围切换 MUST 原子迁移 out_of_scope

Provider 从 active 移入已登记 out-of-scope 集的受审计策略事务 SHALL 同时将 Provider monitoring status 和该 instance/provider 全部现有账号转为 `out_of_scope`，设置同一数据库 `out_of_scope_since`，并清零缺失计数与 `missing_since`。重新加入 active 时 MUST 只切换 Provider monitoring status；旧账号保持 out_of_scope，直到下一次合格 promotion 中实际出现才恢复 present，未出现账号不得开始 missing 计数。

#### Scenario: active Provider 被移出范围
- **WHEN** 实名管理员以有效原因提交 active 到已登记 out-of-scope 的策略切换
- **THEN** policy activation、Provider status、账号 out_of_scope 转换和不可变审计全有或全无提交

#### Scenario: Provider 重新加入且只返回部分旧账号
- **WHEN** Provider 重新 active 后首个合格 promotion 只包含部分旧账号
- **THEN** 实际出现账号恢复 present；未出现旧账号继续 out_of_scope且缺失计数保持零

#### Scenario: 未登记 Provider 或并发 finalize
- **WHEN** 请求移入未登记范围或策略切换与 finalize 竞争同一 binding
- **THEN** 非法切换 fail closed；合法竞争只产生旧策略 promotion 先完成或新策略 out-of-scope 先完成两种原子结果

### Requirement: lifecycle promotion MUST 原子、fenced、幂等且单调

Snapshot items、Provider 当前指针、promotion 标记、lifecycle 转换和 poll finalized MUST 在同一个 PostgreSQL 事务中提交，并只允许当前未过期 lease/fencing token 执行。实现 MUST 以 Provider/账号稳定顺序取得所需锁；同一 poll 的提交未知恢复、旧 Worker、重复 finalize 或并发策略切换 MUST NOT 重复增加 missing、倒退来源或形成混合状态。任一检查、写入或提交失败 MUST 全部回滚。

Lifecycle promotion MUST use the short Node lock and poll fencing token. Restart, stale workers,
replacement and duplicate finalize attempts cannot revive old current truth or advance a new
identity.

#### Scenario: Node lifecycle delta
- **WHEN** the Node lifecycle condition described by this change is evaluated
- **THEN** the existing baseline behavior remains intact and the lifecycle fence is also enforced

#### Scenario: lifecycle 批量转换中数据库失败
- **WHEN** 事务已更新部分账号后发生约束、连接或提交失败
- **THEN** lifecycle、snapshot、Provider pointer、promotion 和 poll 终态全部回滚，旧状态继续有效

#### Scenario: COMMIT 结果未知后恢复
- **WHEN** Worker 不知道提交是否成功并按同一 poll 恢复
- **THEN** 已提交 poll 不再次推进账号，未提交事务可在有效 lease/attempt 内整体重做且最终只有一次转换

#### Scenario: 旧 fencing token 携带 lifecycle candidates
- **WHEN** 恢复者已替换 token 或 poll 已终态后旧 Worker尝试 finalize
- **THEN** 受控函数影响零行，旧 candidates 被丢弃且账号状态不变

### Requirement: Migration MUST 从下一次合格 promotion 建立基线

Additive forward Migration SHALL 创建 lifecycle schema、约束、权限和兼容的受控函数，但 MUST NOT 从历史 snapshot items、Provider 当前指针或旧聚合结果猜测/回填账号生命周期。既有 Provider SHALL 保持 active 监控语义，生命周期表初始为空；部署后的下一次合格 promotion只为实际出现账号建立 present 基线，之后的合格 promotion 才可累计缺失。

#### Scenario: Migration 面对既有快照历史
- **WHEN** 数据库已有多个当前/历史 snapshot 和 Provider pointer
- **THEN** Migration 不创建 account_inventory 行、不推断 missing，也不改写历史 promotion

#### Scenario: 首次部署后完整空集合
- **WHEN** 某 Provider 没有 lifecycle 基线且部署后首个合格 promotion 为空
- **THEN** promotion 成功但 lifecycle 仍为空，不从旧快照补出账号再标记 missing

### Requirement: lifecycle 读取与观测 MUST 有界且不泄露身份

Control SHALL 保留仅供内部Store使用的有界、稳定排序lifecycle读取函数，并 MAY 提供按受控instance/provider/lifecycle/固定transition reason聚合的指标，以及通过`account-inventory-readonly-query` capability向已认证、已启用super_admin提供单Node、有界、逐页审计的产品只读投影。标准化 email/account key MAY 进入受保护 snapshot、duplicate、lifecycle 列、授权内部返回值和该受控产品响应。邮箱为普通业务身份，亦可按批准契约进入 authenticated API/UI、audit、controlled business logs 和 DingTalk payload/body，不 mask、不为业务展示使用 HMAC、不新增邮箱专属权限；产品响应 MUST 不包含内部account key。它们与cursor明文、poll/policy ID、版本/提交、endpoint/IP、Secret、header/body和原始错误 MUST NOT进入普通日志、Prometheus标签、错误文本、SQL参数日志、测试输出或验收artifact。运行时角色 MUST无任意lifecycle表SELECT/DML权限。

#### Scenario: 内部读取生命周期
- **WHEN** 内部消费者按instance/provider与有界limit读取当前lifecycle
- **THEN** 数据库以稳定key顺序返回内部白名单字段且不扩大表权限

#### Scenario: 管理员读取当前账号页
- **WHEN** 合法super_admin通过受控query按单个具备capability的instance读取当前lifecycle
- **THEN** API在审计提交后返回产品字段白名单和email，不返回account key或任意内部/外部Secret

#### Scenario: 未经受控query读取身份
- **WHEN** 未授权主体、普通数据库角色或其他产品route尝试枚举lifecycle/email
- **THEN** 认证、route或数据库权限拒绝且不回显身份

#### Scenario: 敏感 canary 贯穿转换与失败路径
- **WHEN** 验收向email/account key、cursor、Secret、endpoint和原始错误注入唯一canary
- **THEN** 只有受保护身份列、授权产品响应/内部读取及不可读cursor ciphertext可包含相应值，日志、指标、错误、审计details和artifact不包含canary

#### Scenario: lifecycle 指标导出
- **WHEN** Control导出当前状态聚合
- **THEN** 标签只包含受控instance/provider/lifecycle/fixed reason且不包含逐账号身份或高基数字段

### Requirement: lifecycle产品读取 MUST 保持只读与数据面隔离

Lifecycle foundation SHALL 允许 `account-inventory-readonly-query` 向受认证管理员提供有界 current 投影，并允许独立 `account-inventory-history-compaction` 只从已提交历史生成日级摘要、覆盖率和受控清理到期历史。Query 与 history MUST NOT 提供账号导出、人工状态操作、补采、promotion、重建或删除入口；history MUST NOT 重算、删除或改变 current lifecycle，受控删除 current source poll 后外键可以置空但冗余来源和当前字段保持不变。本 capability 仍不实现 HMAC account ID、跨 Node 重复、趋势产品页或告警路由，也不修改 Gateway/Node 或增加既有固定账号清单只读 GET 之外的外部请求。

Current operational reads MUST explicitly filter active Node plus monitoring/current eligibility;
history/rollup/compaction preserve retired evidence and never infer a current target from it.

#### Scenario: Node lifecycle delta
- **WHEN** the Node lifecycle condition described by this change is evaluated
- **THEN** the existing baseline behavior remains intact and the lifecycle fence is also enforced

#### Scenario: 管理员访问现有产品界面
- **WHEN** history compaction 部署后管理员访问产品界面
- **THEN** 可继续读取有界 current 账号列表，但没有历史趋势、导出、状态修改、补采、promotion、重建、删除或告警入口

#### Scenario: 查询与promotion并发
- **WHEN** 管理员 query、lifecycle-aware finalize 和到期历史清理并发
- **THEN** query只观察已提交 current 状态，promotion按原规则推进，history不改变missing计数且current source外键置空不丢失冗余来源含义

#### Scenario: 网络调用范围验收
- **WHEN** lifecycle、query、history恢复和策略切换验收运行
- **THEN** history/query不调用Gateway、Prometheus、模型数据面、Node写接口或未登记目标，Node请求数量与既有poll-run一致

#### Scenario: Control 或 PostgreSQL 停止
- **WHEN** lifecycle/query/history依赖停止或旧应用回滚
- **THEN** 已形成current状态和摘要保留，新状态/读取/压缩按相应依赖暂停，Gateway与Relay Node已有模型请求继续
