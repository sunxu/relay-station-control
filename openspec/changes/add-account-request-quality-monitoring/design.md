## Context

见 proposal.md。唯一源码参考是本机 `/Users/keedle/workspace/CPA-Manager-Plus`，HEAD `1ae656c8`；未查询远端。Control 研究基线 `41ccd72280b6aca3391aff4e6832cb928603b310`。这是准备阶段设计，**身份门槛未通过，禁止视作已实现或可启动 collector**。

## Goals / Non-Goals

**Goals:** 在不修改 CLIProxy 的条件下，把可证明归属于既有 canonical account_key 的真实事件写入 PostgreSQL，并支持单账号 15m/1h 查询。没有事件表示 unknown，不表示健康。

**Non-Goals:** 见 proposal 的冻结排除范围。尤其不复制 CPA 完整事件/SQLite/enrichment 平台，不扩 Node quality，不为脱敏、过滤或审计新增工作。

## Decisions

### 1. 已确认的 source 与接入位置

CPA `internal/httpqueue/client.go:46-136` 的 Pop 请求为 GET `/v0/management/usage-queue?count=N`、Bearer 管理认证。200 body 是数组，元素为 JSON object 或包含 JSON 的 string；null/空元素按参考 client 处理。404/405/501 表示 unsupported，不能 fallback。Node 本地 `internal/api/handlers/management/usage.go:24-43` 在响应前 `PopOldest`，因此不能把它当作无副作用健康检查。

扩展 Control `internal/drivers/cliproxyapi/transport.go` 的固定 operation 与小型 method，复用既有 transport、SecretResolver、管理认证、context 和完整 body timeout，不建立新 framework、不重构原 transport。batch=100、poll=1s、bounded timeout 沿用 15s 请求预算，指数 backoff 上限30s，不增加大量参数。默认关闭；只有确认每个 Node 不存在 CPA Manager Plus 或其它 queue consumer 后才允许启用。

CPA `usage/event.go:504-640` 的 normalizer 支持别名、时间、latency、failed/fail 字段；`buildEventHash:944-961` 内容 hash 不是 request_id 唯一性证明。将来只移植必要逻辑及其依赖，不复制完整 Event schema。substantial 源码复制须附原 MIT notice（Copyright (c) 2026 Seakee）。本轮没有复制生产源码。

### 2. Blocking decision: account identity

直接的 provider + 明确 email 可使用 `internal/inventorypoll/projection.go:127` 的 trim/lowercase 规则生成 `provider:email`，不做额外地址推断。queue 的 `source` 是混合语义字段，不能一律当 email：本地 Node `helps/usage_helpers.go:412-454` 可能返回 OAuth email、Vertex project 或 API key。`auth_index` 仅为 lookup key。

Control `AccountObservation`、inventory parser 和 `account_inventory` 均不保存 auth_index。CPA 则在 collector 中额外用 `/v0/management/auth-files` 补全快照，按 auth_index 索引并拒绝重复 index；它还允许 account/label/file display fallback。这些 fallback 不能当作 Relay email identity。

所以：直接 email 子集可关联，**现有 Control read model 无法对 auth_index-only 的合法事件证明归属**。不把“跳过无法映射事件”当成验收通过；也不声称所有 OAuth 当前样本可用就证明整个契约可用。

可能的最小解除路径（尚未实现/验证）：在既有 auth-files 读取中保留 `auth_index → provider/email` 的唯一映射作为 lookup，继续指向现有 account_key；必须验证重复 index、email 缺失、事件与当前 auth-file 的时间差/删除后的归属。它不应产生第二套 canonical identity、复杂同步或 Node patch。若不能证明，继续 BLOCKED。本轮按用户指定停止条件停止生产实现。

### 3. 拟定最小 raw → Relay mapping

| Relay 字段 | CPA 输入与规则 |
|---|---|
| event_hash | 复用 CPA normalized content hash；不依赖 request_id 唯一性 |
| request_id | 可选，request_id/requestId/id；原字符串保留 |
| node_id | Control 注册 Node UUID，不相信 payload 自报 |
| provider/account_key | 仅可靠身份解析；canonical provider:normalized_email，受上述 blocker 约束 |
| model | CPA requested alias / requested_model 优先，再 resolved model |
| occurred_at | CPA timestamp 规范化 UTC；缺失或非法不能用每次重试的当前时间制造不同事件 |
| duration_ms | latency_ms/duration_ms 等，非负；缺失为 NULL，不能伪造0 |
| success | CPA failed / success / ok / status / error 的既有优先顺序 |
| failure_class | 成功 NULL；失败固定五类 |

hash 生成可能读取 token/endpoint/source 等输入，但这些不是持久化列。CPA normalizer 包含其它领域逻辑，移植时需用 fixture 证明必要子集兼容，不能自己另造 raw parser 格式。

### 4. 最小 failure classifier

拟定顺序：401/403 或明确 token_revoked/token_invalidated/account_deactivated → auth；明确 insufficient_quota/quota_exceeded → quota；否则429 → rate_limit；500–599 → upstream；其它或无法确定 → unknown。成功事件没有 failure class。分类只借用 `failed`、`fail_status_code`、`fail_summary`，不新增类别、不调用 quota API。CPA 并没有可直接搬用的完整五类 classifier，因此不能声称完全复用既有分类器。

### 5. PostgreSQL 与查询（未实施）

最小表拟名 `account_request_events`，仅包含上述十列，主键/唯一约束 `(node_id,event_hash)`，索引 `(node_id,account_key,occurred_at DESC,event_hash)` 及 retention 所需 `occurred_at`。普通写入只有 insert；唯一允许 delete 是超过7天 retention，禁止更新事件。无 partition、rollup、SQLite、raw payload、auth snapshot persistence。

批次事务 `INSERT ... ON CONFLICT (node_id,event_hash) DO NOTHING`。runtime 权限按当前 repository/migration 受限函数模式，不持有 migrator 连接。migration additive、编号实施时确认；现在无 SQL 文件。

Go repository 最小查询按 node_id+account_key，仅允许15m或1h，窗口 `[DB now-window, DB now]`，UTC。返回 request_count、success_count、failure_count、success_rate、p95_latency_ms、last_success_at、last_failure_at、last_failure_class。success_rate=success_count/request_count；零请求时NULL/unknown。p95 对非NULL duration 采用 percentile_cont(0.95)，没有 duration 则NULL。last failure 以 occurred_at,event_hash 确定顺序。DB错误直接返回错误，不能返回零计数。

保留7天，使用DB时间的最小有界 delete-old-events operation。现有 `internal/history` retention 与 inventory lineage/compaction 紧耦合，不修改其30天语义；可在本 collector 现有周期中每小时调用一次独立有界删除，不建额外平台。100k synthetic events 验证两个窗口及 EXPLAIN，未测前不承诺性能阈值。

### 6. Collector 生命周期与可靠性边界

ADR-0001 明确单实例 Control；本版假设一个进程且每个 Node 一个 loop，复用 `cmd/control/main.go` shutdownContext。禁止多个部署或其它消费者同时消费同 Node，未来多实例另行设计，不新增 distributed scheduler。

每批 pop → normalize → resolve identity → classify → transactional insert。DB临时失败时保留当前批次并 backoff 重试，提交前不继续pop。取消终止HTTP和等待；已提交批次重放安全，重启后的重复 hash 不重复计数。**幂等不等于无丢失**：Node已pop但HTTP响应丢失，或Control提交前进程崩溃，会有无法重放的缺口。无 ACK 的现有 source 不允许宣称 exactly-once；不为此引入RESP/额外日志平台。

## Risks / Trade-offs

- [Risk] 缺少可靠身份导致错误账号归属。→ 当前明确 BLOCKED，不猜、不用文件名/API key作email、不静默缩减验收。
- [Risk] destructive source 丢失窗口。→ 明确来源限制、单消费者、批次提交前不继续消费；重复安全与丢失风险分别验证。
- [Risk] 当前 auth-files lookup 不代表事件发生时身份。→ 解除 blocker 必须补时间差与删除场景证明，不能假设当前映射永久有效。

## Migration Plan

仅在身份门槛解除并完成专项真实 PostgreSQL 验收后实施 additive migration。发布时先schema后开启collector；回滚关闭collector并保留forward表。**本轮禁止 push/deploy/archive**，不运行真实 Node queue pop，避免提前消耗事件。
