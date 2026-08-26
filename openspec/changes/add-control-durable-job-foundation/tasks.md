## 1. 数据库模型与权限

- [x] 1.1 新增单一 forward Goose Migration，创建 `async_job_kinds`、`async_jobs`、`async_job_events` 和 `operation_outbox`，封闭状态/长度/时间/attempt/终态组合，并用 PostgreSQL 18 集成测试验证合法记录和全部非法组合
- [x] 1.2 为 job kind 目录实现 Migration-only 登记与不可变保护，验证运行时角色不能新增/修改/删除类型，未知类型和 schema version 不能创建任务，且生产 Migration 不含测试或真实业务类型
- [x] 1.3 为全局幂等键、可执行任务排序、过期 lease、job event sequence、Outbox 待发送排序建立约束和索引，并用 `EXPLAIN`/并发测试确认关键扫描不会退化为无界全表竞争
- [x] 1.4 实现 payload JSON object、64 KiB 上限、确定性 hash 和 Secret-like 字段数据库防线，使用恶意 JSON、超限值、hash 不一致及凭证 canary 测试验证 fail closed
- [x] 1.5 配置 Migration owner 与运行时角色最小权限，验证运行时只能通过预期 DML/函数推进任务，不能修改 job kind、更新/删除历史 event、绕过终态或禁用保护触发器
- [x] 1.6 实现仅四张表和 job kind 目录全空时允许的 down 防护，在 PostgreSQL 18 验证空库可 down，任一类型、任务、事件或 Outbox 存在时 fail closed 且既有环境/认证/资产表保持不变

## 2. 类型注册、payload 和原子入队

- [x] 2.1 在 `internal/jobs` 定义固定 job kind registry、严格版本化 payload validator、确定性编码/hash 和执行策略，单元测试覆盖未知字段、未知版本、类型混淆、大小边界和 Secret-like 名称
- [x] 2.2 新增 sqlc 入队查询和接受调用方 `pgx.Tx` 的 `EnqueueTx`，使用真实 PostgreSQL 测试证明相同 key/内容返回同一 job，不同 kind/operation/payload/policy 冲突失败且不覆盖旧记录
- [x] 2.3 在同一事务创建初始 job event 与 Outbox，并用故障触发器证明业务夹具、成功 audit、job、event 和 Outbox 全部提交或全部回滚，提交前不发送任何通知
- [x] 2.4 实现内部 `RequestCancelTx` 状态原语，测试 pending/retry_wait 安全取消、running/verifying/rolling_back 仅登记请求、终态不可取消以及并发取消幂等；不添加产品 HTTP 写入口
- [x] 2.5 运行 sqlc 与 `make generate`，确认查询、Go 模型和生成物可复现且生成文件无手工差异

## 3. Worker 认领、租约与状态机

- [x] 3.1 实现按固定 priority/available_at/created_at/job_id 使用 `FOR UPDATE SKIP LOCKED` 的短事务认领，测试有界并发、无重复认领、未来任务不提前执行、取消任务不认领和数据库 UTC 判定
- [x] 3.2 在认领时原子写 running、attempt、lease owner、随机 fencing token、lease expiry 和 event，使用并发 PostgreSQL 测试证明只有一个执行权且事务失败不增加 attempt
- [x] 3.3 实现 fenced 续租和结果提交，所有更新同时校验 status、token 与数据库 lease 有效期；测试过期/被替换 token、旧 goroutine、续租竞态和零行更新均不能提交业务结果或终态
- [x] 3.4 实现封闭 Execute 结果映射、有限指数退避/有界 jitter、timeout/max-attempt 和终态保护，使用可注入 Clock 控制循环但由数据库时间裁决持久状态
- [x] 3.5 实现有界 Worker pool、数据库故障退避和优雅停机，进程级测试验证先停止认领、取消本地执行、保留未确认 lease，并且不 busy-loop、不丢失任务或伪造成功

## 4. Reconciler 与崩溃恢复

- [x] 4.1 实现过期 `running|verifying|rolling_back` 任务的 Reconciler 认领和新 fencing token，使用 PostgreSQL 并发测试证明 Worker 与 Reconciler 不会同时拥有有效提交权
- [x] 4.2 定义固定 Verify 结果并实现 `effect_applied|effect_not_applied|effect_partial_or_rollback_required|effect_unknown` 转换，测试未证明结果绝不直接重放 Execute
- [x] 4.3 实现验证重试、回滚允许位、人工处置 failed 和取消请求协作，测试每条路径的 attempt/available_at/终态/event 原子一致且重启不重置预算
- [x] 4.4 使用仅测试构建可用的无网络合成执行器做进程崩溃矩阵：认领提交前、Execute 前、Execute 后回写前、verifying、rolling_back 和终态提交后分别崩溃并验证恢复结果
- [x] 4.5 测试未知 job kind/schema、payload hash 损坏、Verify 不可用和数据库中断均 fail closed，恢复数据库或注册表一致性后可继续诊断，不会执行任意 payload

## 5. Outbox 与可选唤醒

- [x] 5.1 实现最小 wake envelope、disabled Publisher 和 `suppressed/publisher_disabled` 入队语义，验证默认无 Redis 配置时任务仍靠 PostgreSQL 扫描推进且 Outbox 不形成永久 pending
- [x] 5.2 实现 Dispatcher 的 `FOR UPDATE SKIP LOCKED` 认领、发布 lease/fencing、有限退避和 sent/failed 转换，使用 fake Publisher 测试并发、超时、过期 token 和数据库故障
- [x] 5.3 注入通知丢失、重复、乱序、发布成功后回写前崩溃和 Publisher 长期不可用，证明消费者只触发数据库扫描、job 最终状态不依赖 Outbox 且通知允许 at-least-once
- [x] 5.4 增加 Outbox payload/日志泄露测试，证明 envelope 之外的 job payload、幂等键、错误摘要、Secret 和外部响应不能进入通知、日志、Trace 或指标

## 6. OpenAPI、Store 与只读 HTTP

- [x] 6.1 在 `api/openapi.yaml` 定义 `GET /api/jobs` 和 `GET /api/jobs/{job_id}`、固定状态 schema、有界 cursor、过滤器、脱敏 event、`400/401/404/503` Problem 和 `no-store`，运行 lint/契约测试并生成 Go/Orval 客户端
- [x] 6.2 新增任务列表/详情 sqlc 查询和 read-only `REPEATABLE READ` 快照，确保查询不选择 payload、hash、幂等键、lease、Outbox envelope 或错误摘要，并测试稳定倒序 cursor、最大 200、组合过滤和空结果
- [x] 6.3 实现列表/详情 handler，HTTP 集成测试覆盖合法管理员、非法 UUID/cursor、详情 404、数据库 503、恢复后重试和所有响应 `Cache-Control: no-store`
- [x] 6.4 将任务路径置于现有实名 `super_admin` 会话边界，安全负向测试覆盖无会话、过期/撤销会话、伪造 cookie、CSRF 混淆及 `POST|PUT|PATCH|DELETE` 均不能创建、重试、取消或删除任务
- [x] 6.5 注入 payload、idempotency key、lease token、Outbox envelope、错误和数据库连接串 canary，验证 API body/header、日志、Trace、指标和 audit 中均不可检出

## 7. React 任务页面

- [x] 7.1 新增 `/jobs` 懒加载路由、导航和生成客户端 hooks，测试未访问时不加载任务 chunk，页面只请求同源 Control API
- [x] 7.2 实现任务表、固定状态/kind/时间过滤、cursor 翻页和空状态，组件测试覆盖正常、空结果、稳定 key、UTC 时间和最大页边界
- [x] 7.3 实现任务详情与脱敏事件时间线，只显示公开字段和 Outbox 聚合状态；测试 payload/hash/key/lease/error 摘要不存在且没有创建、重试、取消、删除控件
- [x] 7.4 实现 `401` 交回认证、`404`、`503`、资源级错误和显式重试，测试旧数据不冒充当前状态且数据库恢复后无需整页重启

## 8. 指标、安全和数据面隔离

- [x] 8.1 实现 `relay_control_async_jobs{status}`、最老 pending 秒数、过期 lease 数和最老 Outbox pending 秒数，测试状态标签封闭且 job kind/ID、operation ID、参数、错误、lease 和事件 ID 不能成为标签
- [x] 8.2 增加结构化日志 allowlist 和错误清洗测试，固定 component/action/result/job_kind/error_code，验证 `error.Error()`、SQL 参数、命令、URL、Secret 和原始响应不被持久化或记录
- [x] 8.3 运行网络零副作用测试，在空生产 Executor registry 下启动 Worker/Dispatcher/Reconciler、读取 API/UI、模拟重启和数据库恢复，证明没有 Gateway/Node/互联网调用、账号采集、资产写入或请求数据面依赖
- [x] 8.4 停止 Control 和任务数据库并验证既有 Gateway/Node 请求继续运行；恢复后任务循环仅按 PostgreSQL 证据恢复，保存可复现的脱敏验收结果

## 9. 文档、综合验收与证据

- [x] 9.1 更新阶段 1 Runbook 和配置参考，说明 PostgreSQL-only 默认、并发/轮询/lease 配置、任务停滞、过期 lease、Outbox suppressed/积压、人工处置、停机和应用回滚，并逐项 dry run 文档命令
- [x] 9.2 在 PostgreSQL 18 容器执行 Migration up、合成入队、Worker/Reconciler/Outbox 故障矩阵、应用回滚、重新升级和受保护 down，保存版本、命令、退出码及无 Secret 摘要
- [x] 9.3 运行 Go 单元/集成/race 测试、OpenAPI/sqlc 生成检查、前端 lint/typecheck/test/build和容器验收，记录命令与结果并修复非预期跳过
- [x] 9.4 执行专项安全回归，覆盖幂等冲突、状态绕过、stale fencing、未知类型/payload、Secret canary、认证绕过、通知重复和数据库最小权限
- [x] 9.5 对照 proposal、design、`durable-job` spec、系统设计 v1.0 和 ADR-0001 核对实现，确认未引入真实业务任务、Redis 依赖、外部执行器、账号采集、历史压缩、资产写路径或多实例语义
- [x] 9.6 运行 `openspec validate add-control-durable-job-foundation --strict`、生成物 clean check 和 `git diff --check`，确认任务证据已引用、文档一致且仅存在本 change 预期文件
- [x] 9.7 整理可审查提交序列与最终验收摘要，使用 `git status --short` 和提交范围检查证明无临时凭证、测试数据库、runtime 文件或无关改动
