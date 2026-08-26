## Why

阶段 1 已建立管理员访问和单环境资产注册基础，但 Control 尚未实现系统设计要求的 PostgreSQL 持久异步任务、事务 Outbox、执行租约和 Reconciler。后续账号采集、历史压缩及受控 Node 运维若直接使用进程内 goroutine、内存队列或 Redis 作为真相源，将在进程崩溃、通知丢失或外部副作用完成但数据库回写失败时产生任务丢失、重复执行和不可审计状态。

本 change 先建立不绑定具体业务操作的持久任务基础和只读运行视图，使用无网络副作用的测试执行器验证完整恢复语义。完成后，后续 change 可以在同一事务边界内接入明确的业务期望状态、审计和任务，而不必各自重新实现队列、租约和恢复逻辑。

## What Changes

- 在 `control` 仓库新增 `async_job_kinds`、`async_jobs`、`async_job_events` 和 `operation_outbox` 数据模型，以 PostgreSQL 18、数据库 UTC 时间、唯一幂等键和状态约束作为任务真相源。
- 新增原子入队接口，使调用方能够在自己持有的数据库事务中同时写入业务期望状态、成功审计、持久任务和 Outbox；任一写入失败时整个事务回滚。
- 新增使用 `FOR UPDATE SKIP LOCKED`、执行租约、随机 fencing token、有限并发和有限重试的 Worker 基础；进程内通知只能缩短发现延迟，定期 PostgreSQL 扫描始终保留。
- 新增 Reconciler 基础；租约过期后必须先调用任务类型注册的验证逻辑，根据实际状态证明决定成功、重试、回滚或人工处置，禁止直接重放未知结果的外部操作。
- 新增可选唤醒 Publisher/Outbox Dispatcher 接口和禁用模式。本 change 不引入 Redis 客户端或 Redis 部署；通知重复、丢失、延迟或 Publisher 不可用均不得改变任务最终状态。
- 新增只读、受 `super_admin` 会话保护的任务列表/详情 OpenAPI 和 React 页面，展示固定状态、尝试次数、时间和脱敏事件，不暴露任务 payload、幂等键、fencing token、错误原文或 Outbox 内容。
- 新增固定低基数任务/租约/Outbox 指标、脱敏结构化日志、PostgreSQL 故障恢复测试、优雅停机测试和数据面零副作用验收。
- 不新增真实业务任务类型，不调用 Gateway、Relay Node、Prometheus 或其他外部服务，不采集账号，不压缩账号历史，不修改资产或部署期望状态，也不提供任务创建、重试、取消或删除的产品 HTTP API。

## Capabilities

### New Capabilities

- `durable-job`: 定义单实例 Control 的 PostgreSQL 持久任务、事务 Outbox、租约/fencing、有限重试、Reconciler、只读管理视图和数据面隔离边界。

### Modified Capabilities

无。现有 `administrator-access` 继续提供任务只读 API/UI 的认证边界，`asset-registry` 仅作为后续任务目标的稳定身份来源；本 change 不修改二者行为。

## Impact

- **阶段与结果**：阶段 1；运维人员可以查看持久任务是否排队、执行、验证、重试或终止，开发者可以为后续业务 change 复用经过崩溃恢复验证的任务基础。
- **仓库**：只修改 `control`；`ops` 系统设计 v1.0 和 ADR-0001 是输入真相源，本 change 不修改 Gateway、Node 或阶段 0 运行状态。
- **OpenAPI/生成物**：扩展 `api/openapi.yaml` 的任务只读路径并同步 oapi-codegen、Orval 和契约测试；不新增产品写路径，现有 API 保持兼容。
- **Migration/sqlc**：新增一个 additive、合并后不可修改的 Goose forward Migration，以及任务认领、续租、状态转换、Reconciler、Outbox 和只读查询；运行时角色只获得任务运行所需的最小权限。
- **事务与审计**：提供接受调用方事务的入队原语，要求未来业务能力将期望状态、任务、成功审计和 Outbox 原子提交；任务生命周期另写不可变、脱敏的 `async_job_events`。本 change 不伪造尚不存在的业务审计。
- **安全**：持久 payload 仅允许已注册 schema 的非 Secret JSON，大小有界并保存确定性 hash；Secret、凭证、原始外部响应和任意错误原文不得进入任务表、事件、API、UI、日志、指标或 Trace。
- **指标**：新增系统设计规定的任务状态、最老 pending、过期租约和最老 Outbox 指标；标签只使用固定枚举，不使用 job ID、operation ID、任务参数或错误内容。
- **UI/Runbook**：新增懒加载只读任务页面，并补充 Worker/Dispatcher/Reconciler 启停、租约过期、数据库恢复、通知积压和应用回滚处置。
- **兼容、迁移与回滚**：数据库变更仅新增表/索引；默认 PostgreSQL 轮询模式不要求 Redis。应用回滚保留任务和事件。只有任务、事件和 Outbox 全空的全新数据库才允许人工 down，普通应用回滚不得删除任务证据。
- **数据面隔离**：Worker、Reconciler 或 Control PostgreSQL 停止只能暂停控制面任务；Gateway 和 Relay Node 已有请求必须继续运行。本 change 的生产执行器集合为空，不会产生任何外部调用。
