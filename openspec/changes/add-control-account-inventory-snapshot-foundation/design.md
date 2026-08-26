## Context

`account-inventory-poll-run` 已解决 UTC 固定槽、资格过滤、有限并发、Driver 安全边界、lease/fencing、崩溃恢复和无逐账号数据的聚合证据。现有 Driver 已返回字段白名单化的 `AccountObservation`，包含 provider、email、基础状态、累计成功/失败计数、近期请求数和三个源时间；poll 投影当前只保留聚合计数并丢弃这些账号 DTO。

系统设计要求完整 runtime Provider 可以独立推进，即使同 Node 其他 Provider 不完整；策略在 poll 创建后变化时，旧观察只能形成采集证据，不能提升当前状态。`provider_snapshot_complete` 表示响应内容是否完整，不能代替 `promotion_applied`。本 change 必须在不增加 Node 请求、不保存原始响应、不引入账号生命周期的前提下建立这条事务边界。

当前部署仍是单 Control、单 PostgreSQL 真相源。数据库中的环境单例是隔离边界，因此快照唯一键不重复携带环境 ID；Provider 策略 binding 的现有 `(node_type, driver_contract_version)` 行就是 promotion 的串行化点。

## Goals / Non-Goals

**Goals:**

- 确定性标准化 provider/email 并生成稳定 `account_key`。
- 在事务前识别无法生成 identity 与节点内重复，防止数据库唯一冲突替代业务判断。
- 只为完整 runtime Provider 保存不可变、字段白名单化的 snapshot items。
- 将策略版本检查、snapshot 写入、Provider 当前指针和 promotion 结果加入现有 fenced finalize 原子事务。
- 允许同 Node 不同 Provider 独立 promotion，异常 Provider 保留旧指针。
- 保持失败、重试、重启、策略切换和幂等重放下没有部分提升或指针倒退。
- 为后续账号生命周期、本地查询和历史压缩提供稳定输入。

**Non-Goals:**

- 不实现 `account_inventory` 当前账号生命周期、`present|suspected_missing|missing|out_of_scope`、连续缺失计数或恢复语义。
- 不实现 72 小时保留、日级摘要/rollup、覆盖率、压缩、清理或历史 API。
- 不实现基础状态告警、重复归属告警、stale 判定、Prometheus 逐账号指标或 HMAC `account_id`。
- 不新增 OpenAPI、React 页面、人工采集/retry/promotion 或导出入口。
- 不修改 Driver HTTP 契约、Gateway、Node、真实测试账号或请求速率。

## Decisions

### 1. 标准化与账号键在 Control 内存边界完成

只处理 Driver 已契约化的 `AccountObservation`。provider 使用现有 Provider 规范化/allowlist；email 去除首尾空白并转为小写。`account_key` 固定为 `normalized_provider + ":" + normalized_email`。空 provider、非 active Provider、空白 email、非法 UTF-8、超过既有字段上限或标准化后空值均不生成 key。

本 change 不删除 plus-address、不解析 display name、不折叠 Gmail 点号、不使用文件名/path/name/auth index，也不引入 DNS 或外部目录查询。标准化函数必须是纯函数、版本稳定并以 golden/属性测试覆盖大小写、空白、边界长度和 Unicode 输入。未来如需改变算法，必须新增受审 Migration，而不能静默改变历史 key。

`account_key` 本身包含标准化 email，因此按敏感账号身份处理。它允许进入受保护 PostgreSQL 快照表和未来经授权 API，但禁止进入普通日志、指标标签、错误文本或验收输出。未来指标使用独立环境 Secret 生成完整 HMAC-SHA256 `account_id`，不在本 change 实现。

### 2. 事务前预分组，数据库只接收合法投影

投影器按 pinned policy 处理账号 DTO：

1. 记录缺 provider/email 等无法识别计数，但不保留原记录内容。
2. 丢弃 unsupported 与 out-of-scope Provider 的逐账号内容，只保留现有聚合计数。
3. 对 active Provider 按 `(provider, account_key)` 分组。
4. `occurrence_count=1` 的记录才是 snapshot candidate；组内计数大于 1 时不选择或合并状态，只生成 `(provider, account_key, occurrence_count)` 重复证据并令该 Provider 不完整。
5. Provider 完整性仍同时要求 transport/contract 有效、`inventory_mode=runtime`、identity 完整且无重复。

空但完整的 active Provider 是合法快照：snapshot items 为零，但仍可更新该 Provider 当前指针并标记 promotion。这样“完整地观察到零账号”与“未形成完整观察”可区分。原始 DTO 只在 Worker/finalize 调用栈内短暂存在，完成或失败后释放，不进入队列、缓存或重试 payload。

### 3. 新增三张表并扩展 promotion 字段

单个 additive Migration 新增：

- `account_inventory_snapshot_items`：保存 `poll_run_id`、`instance_id`、`account_key`、标准化 provider/email、封闭 `basic_status`、有界累计成功/失败/近期请求计数、可空且范围校验的 refresh/retry/source 时间、数据库生成 `observed_at`。唯一键 `(poll_run_id, instance_id, account_key)`；外键证明 item 属于同一 poll Node。
- `account_inventory_poll_duplicates`：只保存 `poll_run_id`、`instance_id`、provider、`account_key`、`occurrence_count>=2` 与数据库时间；唯一键 `(poll_run_id, instance_id, account_key)`，不保存冲突状态或原始记录。
- `account_inventory_provider_states`：唯一键 `(instance_id, provider)`，保存可空 `current_poll_run_id`、`last_complete_at`、来源 observed/version/commit 与受控状态。当前指针外键 `ON DELETE SET NULL`，来源元数据不能只依赖将来可清理的 poll 行。

现有 `account_inventory_poll_runs` 增加可空封闭 `promotion_skipped_reason`；`account_inventory_poll_provider_results` 增加 `promotion_applied boolean NOT NULL DEFAULT false` 和可空封闭 `promotion_skipped_reason`。Migration 前已 finalized 的历史行以 `false/NULL` 明确表示“旧契约未评估 promotion”，不伪造 skipped 原因；新 finalize v2 的每个 Provider 必须产生 applied 或固定 skipped reason。约束保证 applied 与 skip reason 互斥，applied 只能属于 finalized、runtime、contract-valid、snapshot-complete Provider，并且必须存在匹配 provider state 当前指针；不完整 Provider 不得有 snapshot items。

运行时角色没有这些表的直接 INSERT/UPDATE/DELETE/TRUNCATE 权限，只能调用受控 finalize 和只读快照/指标函数。数据库 trigger 保护不可变 snapshot/duplicate 行和 Provider 指针不被绕过。

### 4. 策略 binding 是 promotion 的数据库串行化点

扩展现有 fenced finalize 函数，在锁定 poll run 并验证 running、未过 lease、fencing token 后：

1. 验证 Node 聚合、pinned active Provider 全集、snapshot candidates 与 duplicate evidence 的闭合集合和计数。
2. 锁定 poll run 对应 `(node_type, driver_contract_version)` 的当前 policy binding 行。
3. 在持有 binding 锁时，以最终数据库当前时间查询 activation history 中实际生效的版本，并与 poll 的 `provider_policy_version` 比较。binding 指针可能因未来预约 activation 提前指向下一版本，不能直接当作当前生效版本。
4. 版本不同：保存完整 poll/provider/duplicate 采集证据，所有 Provider `promotion_applied=false`、原因 `policy_changed`，不写 snapshot items、不更新任何 Provider 指针。
5. 版本相同：逐 Provider 应用下一节规则。
6. 写入最终 poll 结果并置 `finalized`，全部在同一事务提交。

策略切换事务必须锁同一 binding 行后关闭旧 activation、创建新 activation 并更新指针。因此 finalize 与策略切换只能有一个先完成：先 finalize 时旧策略 promotion 完成；先切换且新 activation 已生效时旧 poll 只留证据。未来预约但尚未生效的 activation 不得阻止仍有效旧策略的 promotion。不得先读后写、用 Go 锁或应用时间猜测顺序。

### 5. Provider 独立 promotion，不完整 Provider 保留旧指针

当 binding 未变化时：

- `provider_snapshot_complete=true` 且 mode 为 runtime：写入该 Provider 全部去重 items；即使集合为空，也把 `(instance_id, provider)` 当前指针更新为本 poll，保存来源元数据并设置 `promotion_applied=true`。
- Provider 不完整：不写其 items、不更新其当前指针，设置 applied=false 和固定原因，如 `transport_failed|contract_invalid|disk_fallback|provider_identity_incomplete|provider_duplicate`。
- 某 Provider 的不完整不会阻止同一 Node 其他完整 Provider promotion。Node 汇总 degraded 也不能覆盖 Provider 级判断。
- unsupported/out-of-scope Provider 永不创建 state 或 snapshot item。

指针只能前进到更晚 `scheduled_at`；同槽 fenced 幂等重放只能得到相同结果。较旧 poll 因延迟 Worker 到达时仍保存其采集证据，但受影响 Provider 必须以固定 `stale_poll` 记录 `promotion_applied=false`，不能覆盖新指针或把整个 finalize 变成无界重试。当前版本/commit 与 observed time 复制到 Provider state，未来清理 poll run 后仍保留当前来源语义。

### 6. 快照写入属于 finalize，不创建第二任务或请求

不新增 async job、Outbox、内存异步队列或“先 finalized 后提升”的后台步骤。Worker 把 Driver observation 投影成受限 finalize request；Repository 在现有 lease/fencing 事务中完成聚合证据和 promotion。这样数据库断连、进程崩溃或事务超时只会留下非终态 run，由现有 Reconciler 在 grace/attempt 边界内恢复；不会出现 finalized 但半个 Provider 指针已更新。

Node 已返回的 transport/contract/disk-fallback/identity 失败仍是 finalized 观察，不因无法 promotion 而在同槽请求 Node。只有 finalize 提交结果未知属于 Control 执行未知，继续使用现有最多两次、同 poll、同 pinned policy 的恢复规则。

### 7. 计数、时间与字段均 fail closed

数据库与 Go 层共同验证：

- 每个 Provider candidate+duplicate+missing identity 计数与 Provider/Node 聚合一致。
- item provider 必须属于 pinned active Provider，email 与 `account_key` 必须重新计算一致，status 只允许 Driver 封闭值。
- `uint64` 计数进入 PostgreSQL 前执行显式上界检查，禁止溢出或负数；单 poll item/duplicate 数受现有最大记录数约束。
- Node 源 Unix 时间只允许可空、合理范围且保持原含义；`observed_at`、创建和 promotion 时间只使用 PostgreSQL UTC。
- finalize request 中的额外、缺失、重复 Provider/item，错误 Node/poll 关联、非法 reason/status/time 一律整体拒绝。

### 8. 观测只暴露 promotion 聚合，不暴露身份

允许新增固定指标：

```text
relay_control_account_inventory_provider_promotion_applied{instance_id,provider}
relay_control_account_inventory_provider_promotion_skipped{instance_id,provider,reason}
```

reason 来自封闭集合；不新增 email、`account_key`、poll ID、policy ID、version/commit 或 raw error 标签。结构化日志沿用 poll allowlist，可记录固定 promotion action/result/reason 和受控 instance/provider；禁止格式化 snapshot request、AccountObservation 或数据库参数。

Migration 前的 legacy finalized Provider 仍导出原 snapshot-complete 指标，但因为没有真实 promotion 判定而不导出 applied/skipped 指标；不得把 `false/NULL` 猜测成 policy_changed 或其他失败。新 finalize v2 之后的记录必须完整导出 applied 或一个 skipped reason。

验收向 email、account_key、endpoint、Secret、header/body、错误和未知字段注入唯一 canary，并扫描 PostgreSQL 非快照表、日志、指标、错误、test output 与 acceptance artifact。标准化 email/account_key 只允许出现在受保护 snapshot/duplicate 表的预期列；scanner 不回显命中值。

### 9. Migration、回滚与后续兼容

Migration 为 forward-only additive。应用发布顺序是先 Migration、再同时理解新 finalize 契约的二进制；旧二进制回滚时停止 poll service，避免调用旧函数签名，并保留所有新表和字段。普通生产环境禁止 down。

受保护 down 只在 snapshot/duplicate/provider-state 全空、没有 poll/provider promotion 标记且没有后续外键依赖时恢复旧 finalize 函数并删除新增对象。当前不可变 trigger 会拒绝直接删除以及经 poll 父行触发的级联删除；未来历史保留 change 必须先引入显式受控且可审计的清理机制，完成 snapshot/duplicate 清理后才允许 poll 删除触发 Provider state 的 `ON DELETE SET NULL`，不得用 trigger 深度猜测放行。Migration 正反向、非空保护、角色权限、函数 owner/search_path 和生成物可复现必须有 PostgreSQL 18 测试。

后续生命周期 change 只消费 `promotion_applied=true` 的 Provider 当前快照并在同一事务边界扩展 `account_inventory`；不得重新读取 Node 或从不完整/策略变化快照推进 missing。历史压缩、API/UI 和 HMAC metrics 继续由独立 change 设计。

## Risks / Trade-offs

- **标准化规则固化错误**：`account_key` 会成为长期业务键。通过最小确定性算法、golden 测试和禁止启发式合并降低风险；变化必须显式 Migration。
- **email 明文进入 PostgreSQL**：管理产品最终需要按 email 查询展示。通过数据库访问边界、无 API 暴露、日志/指标禁止、备份与 Secret 管理规则限制范围；本 change 不声称加密静态数据。
- **finalize 事务变长**：每轮最多现有记录上限，仍可能增加锁/WAL。使用批量参数、固定上限、必要索引和短 binding 行锁；50 Node 容量测试必须证明不超过 grace/lease 余量。
- **策略锁与配置变更争用**：同一策略作用域的 finalize 与切换串行是有意的一致性成本。锁顺序固定为 poll run 后 binding，策略切换不得反向获取 poll run 锁。
- **空 Provider 快照易被误解**：零 items 也可 promotion，代表完整观察到空集合。Provider state/promotion 标记而非 item 数决定快照是否存在。
- **只做快照不做生命周期**：暂时没有账号当前状态或页面，但形成可审计、可独立验证的稳定事务输入，避免把 missing、告警和压缩同时塞入高风险 Migration。

## Migration Plan

1. 新增并验证 additive Migration、sqlc 和受控 finalize v2，默认 poll service 保持关闭。
2. 使用 fake Driver 与隔离 PostgreSQL 验证标准化、重复、Provider 独立 promotion、policy race、fencing 和崩溃矩阵。
3. 用官方 CLIProxyAPI v7.2.141 合成 fixtures 验证 runtime/disk fallback；保持 management 请求全局串行且每次后等待 10 秒。
4. 在无真实 Node 的 canary 环境开启 poll，确认 promotion/skip 指标、WAL/锁/事务耗时与现有容量余量。
5. 应用回滚只关闭 poll 并回退二进制，保留 forward schema；不删除已提升快照。

## Open Questions

无。本 change 采用系统设计 v1.0 的 provider/email 标准化、Provider 独立 promotion、policy binding 行锁和 PostgreSQL 原子 finalize；生命周期、保留压缩、告警、API/UI 与 HMAC 指标明确延后。
