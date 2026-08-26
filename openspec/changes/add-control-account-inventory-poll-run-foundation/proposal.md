## Why

Control 已具备单环境资产注册、Node 账号监控激活区间、不可变 Provider 策略和安全只读 `CLIProxyAPIDriver`，但 Driver 目前只能由显式调用方返回一次内存观察，尚不能按 UTC 固定时间槽形成可恢复的采集证据。若直接用进程内 ticker/goroutine 调用 Driver，Control 重启、数据库故障、租约过期或并发拥塞会造成重复时间槽、补造历史响应、策略漂移和无法区分“Node 请求失败”与“Control 未完成落库”。

本 change 建立独立于通用 `async_jobs` 的账号清单 poll-run 基础：以 PostgreSQL UTC 时间、Node 监控区间、不可变 Provider 策略版本、唯一时间槽、执行租约和事务化聚合证据为真相源。它只保存本轮传输、契约、inventory mode 和 Provider 完整性证据，不保存账号明细或推进当前账号状态，为后续账号快照与生命周期 change 提供稳定输入。

## What Changes

- 新增 `account_inventory_poll_runs` 与 `account_inventory_poll_provider_results`，封闭 `pending|running|retry_wait|finalized|abandoned` 状态，使用 `(instance_id, scheduled_at)` 唯一约束、数据库 UTC 时间、lease/fencing 和不可变 `provider_policy_version`。
- 新增五分钟 UTC 固定槽调度器，只为在 `scheduled_at` 时声明账号清单 capability、处于显式 Node 监控激活区间且存在有效 Provider 策略的 Relay Node 幂等创建 poll run；不追溯补建过期时间槽。
- 新增有限并发 Worker。Worker 必须先取得 HTTP 并发额度，再在短事务中认领 poll run，并以数据库计算的剩余 `poll_start_grace` 约束立即调用现有 Node Driver；初始宽限期 120 秒、请求总超时 15 秒、执行租约 30 秒，配置必须通过最多 50 个 Node 的容量校验。
- 新增过期租约 Reconciler：宽限期内只复用原 poll run、增加有界 attempt 并刷新 fencing；超过宽限期或耗尽尝试后只标记 `abandoned`，不得调用当前 Node 接口补造过去时间槽。
- 将每次已形成的 Driver 观察在一个 fenced PostgreSQL 事务中保存为 finalized poll run 和按固定策略 active Provider 补齐的聚合结果。Node 请求失败或非 200 是 `transport_success=false` 的 finalized 证据，不是可隐藏重试的 Control 执行失败。
- 新增固定低基数/受控 Node 标识的 poll state、scheduler lag、queue wait、poll start lag、transport、contract、mode 和 Provider 完整性指标，以及脱敏日志、运行配置、Runbook 和重启/数据库/Node 故障验收。
- 不复用 `async_jobs` 保存 poll run，不修改 Gateway/Node，不写账号邮箱或原始响应，不生成账号快照，不更新 Provider 当前指针，不推进 missing/out-of-scope 生命周期，不实现历史压缩、日级覆盖率、告警、产品 API 或 React 页面。

## Capabilities

### New Capabilities

- `account-inventory-poll-run`: 定义 Control 的 UTC 固定账号清单时间槽、监控区间过滤、策略版本固定、有限并发、租约/fencing、崩溃恢复和聚合证据持久化边界。

### Modified Capabilities

无。`asset-registry` 继续提供 Node、capability、监控激活区间和不可变 Provider 策略；`cliproxyapi-readonly-driver` 继续负责单次安全只读观察；`durable-job` 保持独立且不承载 poll-run 专用状态机。

## Impact

- **阶段与结果**：阶段 2；Control 可以按可审计 UTC 槽安全采集现有 CLIProxyAPI 账号清单，并在崩溃或数据库故障后区分已完成证据、可恢复执行与不可补采缺口。
- **仓库**：只修改 `control`；`ops` 系统设计 v1.0 第 9.6、12、13.4、20.3、21.1、23、24.2 节和 ADR-0001 是输入真相源，不修改 `ops`、Gateway 或 Node 产品代码。
- **OpenAPI/UI**：不新增或修改产品 HTTP 路径、OpenAPI schema、生成客户端或 React 页面；本轮运行状态通过 PostgreSQL、指标、脱敏日志和 Runbook 验证。
- **Migration/sqlc**：新增一个 additive、合并后不可修改的 Goose forward Migration，以及调度、认领、恢复、finalize 和指标读取所需 sqlc 查询。运行时角色只获得这些表和固定函数的最小权限。
- **配置**：新增固定五分钟周期、`poll_start_grace`、poll concurrency、请求最坏耗时、lease、最大 attempt 和扫描间隔配置；生产初始值固定为 300 秒、120 秒、至少 10、15 秒、30 秒和有界尝试，危险组合在启动网络调用前失败。
- **事务/幂等**：同一 Node/时间槽最多一行；策略版本只在首次创建时固定。认领和 finalize 均使用数据库时间、状态、未过期 lease 与随机 fencing token，旧 Worker 不能提交结果。
- **安全**：只保存稳定 UUID、固定枚举、聚合计数和有界版本/提交；不保存 email、account key、Management Key、Secret 引用、endpoint、IP、header、原始 body、原始错误或无法识别记录内容。
- **指标/日志/审计**：指标只使用系统设计允许的 `instance_id`、固定 `state`、受控 provider 和固定 reason/mode；不使用 email、poll-run ID、policy version、endpoint、Secret 或错误文本。自动调度不伪造实名管理员审计。
- **兼容性**：现有 API、UI、资产和 Driver 契约保持兼容；没有账号清单 capability、监控区间或有效策略的 Node 不进入调度。后续快照 change 通过 additive Migration 和同一 finalize 事务扩展提升逻辑。
- **回滚**：应用回滚停止新 poll run，保留表和历史证据；普通回滚不得执行 down 或删除 poll run。只有经确认表为空的全新环境才允许人工 down。
- **数据面隔离**：调度器只调用 Node 只读管理接口。Control、Worker、PostgreSQL 或管理网络停止只暂停采集，不影响 Gateway/Relay Node 已有模型请求；禁止任何 Gateway/Node 写操作。
