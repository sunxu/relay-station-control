# account-inventory-snapshot Specification

## Purpose
为 Control 提供受 poll lease/fencing 与固定 Provider 策略保护的不可变账号快照和 Provider 当前来源，使完整 runtime 观察可原子提升，同时隔离账号身份、拒绝部分写入且不提前实现账号生命周期或产品界面。

## Requirements

### Requirement: Control SHALL 确定性标准化账号身份并生成稳定 account key

Control SHALL 只从 Driver 字段白名单中的 provider/email 生成账号身份。provider MUST 使用受控规范值，email MUST 去除首尾空白并转为小写，`account_key` MUST 等于 `normalized_provider + ":" + normalized_email`。Control MUST NOT 使用 name、path、文件名、auth index、plus-address 折叠、域名别名或外部查询补造身份。标准化失败 MUST 只增加固定聚合计数，不保存或输出原始记录。

#### Scenario: 大小写和首尾空白不同
- **WHEN** 同一 active Provider 返回只在 provider/email 大小写或首尾空白上不同的两条记录
- **THEN** Control 为它们生成相同 account key，并按节点内重复规则处理而不是生成两个账号

#### Scenario: email 或 provider 无法识别
- **WHEN** 记录缺少 provider、provider 不受支持、email 为空或标准化后为空
- **THEN** Control 不生成 account key 或 snapshot item，只更新允许的 missing/unsupported/out-of-scope 聚合计数且不保存原始内容

#### Scenario: 尝试用非身份字段替代 email
- **WHEN** 无 email 记录仍包含 name、path、文件名或 auth index
- **THEN** Control 拒绝用这些字段生成账号身份，不猜测、不哈希也不持久化替代 key

### Requirement: Control MUST 在数据库事务前识别节点内重复账号

Control MUST 按 `(provider, account_key)` 对本轮 active Provider 记录预分组。出现次数为一的组才可成为 snapshot candidate；出现次数大于一的组 MUST 只产生 `account_key` 与 `occurrence_count` 聚合重复证据，并使所属 Provider 的 identity/snapshot completeness 为 false。Control MUST NOT 任意选择、合并或覆盖重复记录。

#### Scenario: 同 Provider 同 key 出现多次
- **WHEN** 一个 runtime 观察中同一 `(provider, account_key)` 出现两次或更多
- **THEN** Control 不为该 key 写 snapshot item，保存一条有界 occurrence count 重复证据，并阻止该 Provider promotion

#### Scenario: 不同 Provider 使用相同 email
- **WHEN** 两个 active Provider 返回标准化后相同 email
- **THEN** provider 前缀使其形成不同 account key，二者不构成节点内重复并可分别参与 Provider promotion

#### Scenario: 一个 Provider 重复而另一个完整
- **WHEN** Node 的一个 Provider 存在重复账号且另一个 Provider 的 runtime 集合完整
- **THEN** 重复 Provider 不提升，完整 Provider 仍独立保存快照并推进当前指针

### Requirement: PostgreSQL SHALL 保存不可变且字段白名单化的账号快照

PostgreSQL SHALL 只为 promotion-applied 的完整 runtime Provider 保存 `account_inventory_snapshot_items`。每个 item MUST 关联 finalized poll run、同一 Node 和 pinned active Provider，并保存 account key、标准化 provider/email、封闭基础状态、有界计数、受校验源时间和数据库 `observed_at`。唯一键 MUST 防止同一 poll/Node/account key 重复。快照和重复证据在提交后 MUST 不可更新。

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

### Requirement: finalize MUST 锁定当前策略并区分 completeness 与 promotion

Control MUST 在现有 poll lease/fencing finalize 事务中锁定 poll run 对应的当前 Provider policy binding，并在持锁后按 PostgreSQL 当前时间从 activation history 取得实际生效版本，与不可变 `provider_policy_version` 比较。binding 指针 MAY 为未来预约 activation 提前更新，因此 MUST NOT 单独作为当前生效版本。`provider_snapshot_complete` MUST 只描述返回内容完整性；`promotion_applied` MUST 只描述该 Provider 的 snapshot items 与当前指针已在本事务生效。两者不得互相替代。

#### Scenario: poll 后策略未变化
- **WHEN** finalize 锁定 binding 后数据库当前时间对应的实际生效版本仍等于 poll pinned policy
- **THEN** Control 按 Provider 完整性规则决定 promotion，并在同一事务保存结果、items、指针和 applied 标记

#### Scenario: poll 后策略已经变化
- **WHEN** finalize 持有 binding 锁时实际生效版本不同于 poll pinned policy
- **THEN** Control 保存 poll/provider/duplicate 采集证据，全部 active Provider 标记 `promotion_applied=false` 与 `policy_changed`，不写 snapshot items、不更新当前指针，也不按新策略重解释旧观察

#### Scenario: 新策略已预约但尚未生效
- **WHEN** binding 指针已指向未来 activation，但数据库当前时间仍落在 poll pinned policy 的 active range
- **THEN** Control 仍按当前实际生效的旧策略执行 Provider promotion，不提前标记 `policy_changed`

#### Scenario: finalize 与策略切换并发
- **WHEN** finalize 和策略切换同时竞争同一 binding 行
- **THEN** 数据库锁只允许得到“旧策略先完整 promotion”或“新策略先切换且旧 poll 跳过 promotion”两种原子结果，不出现混合 Provider 版本

### Requirement: 完整 active Provider SHALL 独立推进当前快照

当策略版本未变化时，每个 `provider_snapshot_complete=true` 且 inventory mode 为 runtime 的 active Provider SHALL 独立写入全部去重 items，更新 `(instance_id, provider)` 当前快照指针和来源元数据，并设置 `promotion_applied=true`。同 Node 其他 Provider 不完整或 Node 汇总 degraded MUST NOT 阻止该 Provider。Provider 当前指针 MUST 只前进到更晚槽，不得由迟到旧 poll 倒退。

#### Scenario: 两个 Provider 只有一个完整
- **WHEN** Provider A 完整而 Provider B 因缺 identity 或重复不完整
- **THEN** A 写入 items 并更新当前指针，B 不写 items且保留旧指针；二者分别记录 applied 与固定 skipped reason

#### Scenario: active Provider 合法返回零账号
- **WHEN** runtime 响应对某 active Provider 完整但集合为空
- **THEN** Control 写入零条 item，仍将 Provider 当前指针推进到本 poll 并设置 promotion applied，表示完整观察到空集合

#### Scenario: 迟到旧槽试图覆盖新指针
- **WHEN** 较新槽已经成为 Provider 当前快照，较旧 poll 随后到达 finalize/promotion 路径
- **THEN** 较旧 poll 保存采集证据并以 `stale_poll` 标记 promotion false，较新来源元数据和当前快照保持不变且不会产生无界重试

### Requirement: 不完整或非 runtime 观察 MUST NOT 生成快照

transport 失败、HTTP 非 200、contract invalid、disk fallback、缺 provider、active Provider identity 不完整、节点内重复或 Provider 不属于 active policy 时 MUST NOT 为受影响 Provider 写 snapshot items或更新当前指针。已形成的 Node 失败仍 MUST 按 poll-run 契约 finalized，promotion skip MUST NOT 触发同槽 Node 重试。

#### Scenario: transport 或 contract 失败
- **WHEN** Driver 已返回固定 transport/contract 失败观察
- **THEN** poll 保存 finalized 失败证据，全部 active Provider promotion false且无 snapshot item/指针变化，同槽不再次请求 Node

#### Scenario: disk fallback
- **WHEN** Driver 返回 contract-valid `inventory_mode=disk_fallback`
- **THEN** Control 保存聚合观察但所有 Provider 跳过 promotion，旧 runtime 当前快照保持有效且不据此推进任何缺失判断

#### Scenario: unsupported 或 out-of-scope Provider 记录
- **WHEN** runtime 响应包含 pinned active policy 之外的受支持范围或未知 Provider
- **THEN** Control 只更新既有聚合计数，不为这些 Provider 创建 snapshot item或 Provider state

### Requirement: snapshot promotion SHALL 与 poll finalize 原子且受 fencing 保护

Poll 聚合、Provider 结果、duplicate evidence、snapshot items、Provider state 指针、promotion 标记、对应账号 lifecycle 转换和 `status=finalized` MUST 在一个 PostgreSQL 事务中提交。只有持有当前未过期 lease/fencing token 的 Worker可以执行。任一验证、锁、写入或提交失败 MUST 全部回滚；不得通过第二任务、Outbox 或进程内队列延后补做 promotion 或 lifecycle。

#### Scenario: snapshot 批量写入中数据库失败
- **WHEN** 事务在部分 item、账号状态、Provider 指针或 promotion 标记写入后失败
- **THEN** 全部变化回滚，poll 保持可由原 lease 恢复的非终态，旧 lifecycle 和当前指针继续有效

#### Scenario: 旧 Worker 在恢复后提交
- **WHEN** Reconciler 已替换 fencing 或 poll 已进入终态，旧 Worker 携带内存 snapshot candidates finalize
- **THEN** fenced finalize 影响零行，旧 candidates 被丢弃且不能追加 item、推进 lifecycle 或覆盖 Provider state

#### Scenario: 提交结果未知后重试
- **WHEN** Control 在数据库 COMMIT 响应前后失联并按同 poll 恢复
- **THEN** 唯一键、状态和 fencing 使结果保持单份；已提交终态不重复写或推进 lifecycle，未提交事务可在 grace/attempt 内整体重做

### Requirement: Provider 当前快照状态 SHALL 独立且可在历史清理后解释

`account_inventory_provider_states` SHALL 以 `(instance_id, provider)` 唯一保存当前 poll 来源、最近完整时间、来源 observed/node version/commit 与固定状态。不同 Provider MUST 拥有独立指针。`current_poll_run_id` MAY 在未来历史清理时 `ON DELETE SET NULL`，但复制的来源元数据 MUST 保持当前快照含义；本 change MUST NOT 据此创建账号生命周期。

#### Scenario: 一个 Provider 连续多槽提升
- **WHEN** 同一 Provider 的更新完整槽成功 promotion
- **THEN** 当前指针和来源元数据原子前进，旧 snapshot items 保持不可变历史

#### Scenario: 下一槽 Provider 不完整
- **WHEN** Provider 已有当前快照而下一 poll 的该 Provider 不完整
- **THEN** 当前指针、最近完整时间和来源元数据保持旧值，只在 poll/provider 证据中记录本槽 skip

#### Scenario: 当前来源 poll 将来被清理
- **WHEN** 后续受审历史保留 change 先以显式受控机制替换本 foundation 的不可变删除保护并合法清理证据，再删除旧 poll 使外键置空
- **THEN** Provider state 仍保留最近完整时间和来源版本/提交等必要元数据，不把空外键解释为没有当前快照

### Requirement: snapshot 身份数据 MUST 只进入受保护持久列

标准化 email 与 account key MAY 只进入 `account_inventory_snapshot_items`、必要的 `account_inventory_poll_duplicates` 和 `account_inventory` 受保护列。它们以及 AccountObservation、endpoint/IP、Secret/Management Key、header/body、原始错误和未知响应字段 MUST NOT 进入普通日志、Prometheus 标签、错误文本、SQL 参数日志、test output 或 acceptance artifact。运行时角色 MUST 没有任意快照或 lifecycle 写删改能力。

#### Scenario: 敏感 canary 注入成功路径和全部失败路径
- **WHEN** 测试向 email/account key、endpoint、Secret、header/body、原始错误与未知字段注入唯一 canary
- **THEN** 只有预期快照/重复/lifecycle 表的允许身份列可包含标准化 email/account key；其他数据库列、日志、指标、错误和 artifact 均不包含 canary

#### Scenario: 指标导出 Provider promotion
- **WHEN** Control 从 PostgreSQL 导出 promotion applied/skipped 或 lifecycle 聚合
- **THEN** 标签只包含受控 instance ID、Provider、lifecycle 与固定 reason，不包含 email、account key、poll/policy ID、版本/提交或错误

#### Scenario: legacy Provider 没有 promotion 判定
- **WHEN** 最新 finalized Provider 来自 Migration 前旧契约且 promotion false/reason NULL
- **THEN** Control 继续导出其 snapshot-complete 聚合但省略 applied/skipped 指标，不猜测跳过原因或 lifecycle

#### Scenario: 未授权读取或直接写快照
- **WHEN** 非产品运行时角色或不存在的产品 API 尝试枚举、创建、修改或删除账号快照/lifecycle
- **THEN** 数据库权限或路由边界拒绝操作；本 change 不新增对外账号 API

### Requirement: 本 snapshot foundation SHALL 只在合格 promotion 推进当前账号生命周期

Snapshot foundation SHALL 继续形成不可变 snapshot items、节点内重复证据和 Provider 当前来源，并在同一 fenced finalize 内只为 `promotion_applied=true` 的完整 runtime Provider 推进 `account_inventory` 当前生命周期。它 MUST NOT 从未提升观察或历史快照计算状态，不生成日级摘要、趋势、覆盖率、压缩任务或告警，也不新增产品 OpenAPI/UI。Gateway 或 Node MUST 不被修改或调用写接口。

#### Scenario: 新完整快照缺少旧账号
- **WHEN** Provider 新快照完整、策略未变化且 promotion applied，并没有某个现有 present 账号
- **THEN** 本 change 在同一事务提升快照并按连续完整缺失规则推进该账号，不产生告警或历史摘要

#### Scenario: 新观察未获 promotion
- **WHEN** Provider 因失败、不完整、disk fallback、policy_changed 或 stale_poll 未提升
- **THEN** snapshot 当前来源和账号 lifecycle 都保持原值

#### Scenario: 管理员使用现有 Control 页面
- **WHEN** lifecycle foundation 部署后管理员访问现有 API/UI
- **THEN** 现有契约保持兼容，不出现账号列表、历史、人工 promotion、重试或删除入口

#### Scenario: 网络调用范围检查
- **WHEN** snapshot/promotion/lifecycle 验收运行
- **THEN** 网络请求数量和目标与既有 poll-run 固定账号清单只读 GET 相同，不增加 Probe、Gateway、模型数据面、Node 写接口或任意目标
