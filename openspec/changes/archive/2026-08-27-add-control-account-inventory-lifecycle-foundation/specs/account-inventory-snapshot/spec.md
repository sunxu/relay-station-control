# account-inventory-snapshot Specification Delta

## MODIFIED Requirements

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

## RENAMED Requirements

- FROM: `### Requirement: 本 snapshot foundation MUST 不推进账号生命周期或历史产品功能`
- TO: `### Requirement: 本 snapshot foundation SHALL 只在合格 promotion 推进当前账号生命周期`
