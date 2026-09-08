## Context

见 proposal.md。唯一源码参考是本机 `/Users/keedle/workspace/CPA-Manager-Plus`，HEAD `1ae656c8`；未查询远端。Control 研究基线 `41ccd72280b6aca3391aff4e6832cb928603b310`。Architecture Review 已将身份门槛改为 resolved/unresolved，正式实现按该决定推进。

## Goals / Non-Goals

**Goals:** 在不修改 CLIProxy 的条件下，把可证明归属于既有 canonical account_key 的真实事件写入 PostgreSQL，并支持单账号 15m/1h 查询。没有事件表示 unknown，不表示健康。

**Non-Goals:** 见 proposal 的冻结排除范围。尤其不复制 CPA 完整事件/SQLite/enrichment 平台，不扩 Node quality，不为脱敏、过滤或审计新增工作。

## Decisions

### 1. 已确认的 source 与接入位置

CPA `internal/httpqueue/client.go:46-136` 的 Pop 请求为 GET `/v0/management/usage-queue?count=N`、Bearer 管理认证。200 body 是数组，元素为 JSON object 或包含 JSON 的 string；null/空元素按参考 client 处理。404/405/501 表示 unsupported，不能 fallback。Node 本地 `internal/api/handlers/management/usage.go:24-43` 在响应前 `PopOldest`，因此不能把它当作无副作用健康检查。

扩展 Control `internal/drivers/cliproxyapi/transport.go` 的固定 operation 与小型 method，复用既有 transport、SecretResolver、管理认证、context 和完整 body timeout，不建立新 framework、不重构原 transport。batch=100、poll=1s、bounded timeout 沿用 15s 请求预算，指数 backoff 上限30s，不增加大量参数。默认关闭；只有确认每个 Node 不存在 CPA Manager Plus 或其它 queue consumer 后才允许启用。

CPA `usage/event.go:504-640` 的 normalizer 支持别名、时间、latency、failed/fail 字段；`buildEventHash:944-961` 内容 hash 不是 request_id 唯一性证明。将来只移植必要逻辑及其依赖，不复制完整 Event schema。substantial 源码复制须附原 MIT notice（Copyright (c) 2026 Seakee）。必要normalization helpers已移植，MIT notice保留于internal/requestquality/CPA-MIT-LICENSE.txt。

### 2. Identity resolution：无法证明时保留 NULL

目标是 Every provably attributable event is attributed correctly，不是所有合法事件都有 account_key。复用 `inventorypoll.NormalizeAccountIdentity` 导出的既有 trim/lowercase 规则，canonical identity 不变。

- A：事件明确 provider + email/account 邮箱信息 → resolved。`source` 是混合字段，只有可证明是邮箱的值才作直接身份，不能把API key、project、filename作为email。
- B：事件只有auth_index，读取同 Node 当前 `/v0/management/auth-files`，仅使用明确provider/email，构造本批内存lookup。不同条目归一化为同一account_key仍是exactly-one；只有一个不同account_key且无矛盾证据才resolved。管理快照本身为现有账号证据，无需新增DB canonical identity。
- C：0或多个不同key、删除/缺失、无效身份条目、provider或直接email与snapshot冲突 → unresolved。明确direct identity与缺失lookup不是同一情况：前者有独立证据可归属；若有实际冲突则仍NULL。
- 不实现auth_index历史表、valid_from/valid_to、generation、历史assignment重建、reconciliation worker或CLIProxy patch。当前lookup仅作为当前证据，不宣称可重建历史归属。

每批pop前读取当前auth-files；读取失败时记录lookup不可用，继续保留queue事件，缺乏独立identity的事件NULL。所有合法事件写入同一表，无identity_status字段。AccountQuality必须按具体非空account_key过滤；Node/ProviderQuality包含NULL并返回unresolved_request_count。

### 3. 冻结最小 raw → Relay mapping

| Relay 字段 | CPA 输入与规则 |
|---|---|
| event_hash | 复用 CPA normalized content hash；不依赖 request_id 唯一性 |
| request_id | 可选，request_id/requestId/id；原字符串保留 |
| node_id | Control 注册 Node UUID，不相信 payload 自报 |
| provider/account_key | 可靠证据使用canonical provider:normalized_email，否则account_key=NULL；provider仍保留事件观测，缺失且不能从唯一lookup证明时unknown |
| model | CPA requested alias / requested_model 优先，再 resolved model |
| occurred_at | CPA timestamp 规范化 UTC；缺失或非法不能用每次重试的当前时间制造不同事件 |
| duration_ms | latency_ms/duration_ms 等，非负；缺失为 NULL，不能伪造0 |
| success | CPA failed / success / ok / status / error 的既有优先顺序 |
| failure_class | 成功 NULL；失败固定五类 |

hash 生成可能读取 token/endpoint/source 等输入，但这些不是持久化列。CPA normalizer 包含其它领域逻辑，移植时需用 fixture 证明必要子集兼容，不能自己另造 raw parser 格式。

### 4. 最小 failure classifier

冻结顺序：401/403 或明确 token_revoked/token_invalidated/account_deactivated → auth；明确 insufficient_quota/quota_exceeded → quota；否则429 → rate_limit；500–599 → upstream；其它或无法确定 → unknown。成功事件没有 failure class。分类只借用 `failed`、`fail_status_code`、`fail_summary`，不新增类别、不调用 quota API。CPA 并没有可直接搬用的完整五类 classifier，因此不能声称完全复用既有分类器。

### 5. PostgreSQL 与查询

最小表 `account_request_quality_events`，仅包含上述十列，主键/唯一约束 `(node_id,event_hash)`，索引 `(node_id,account_key,occurred_at DESC)` 、Node/Provider索引及 retention 所需 `occurred_at`。普通写入只有 insert；唯一允许 delete 是超过7天 retention，禁止更新事件。无 partition、rollup、SQLite、raw payload、auth snapshot persistence。

批次事务 `INSERT ... ON CONFLICT (node_id,event_hash) DO NOTHING`。runtime 权限按当前 repository/migration 受限函数模式，不持有 migrator 连接。migration additive、编号实施时确认；实现于 `00020_account_request_quality_events.sql`。

Go repository 最小查询按 node_id+account_key，仅允许15m或1h，窗口 `[DB now-window, DB now]`，UTC。返回 request_count、success_count、failure_count、success_rate、p95_latency_ms、last_success_at、last_failure_at、last_failure_class。success_rate=success_count/request_count；零请求时NULL/unknown。p95 对非NULL duration 采用 percentile_cont(0.95)，没有 duration 则NULL。last failure 以 occurred_at,event_hash 确定顺序。DB错误直接返回错误，不能返回零计数。

保留7天，使用DB时间的最小有界 delete-old-events operation。现有 `internal/history` retention 与 inventory lineage/compaction 紧耦合，不修改其30天语义；在本 collector 周期中每小时调用独立有界删除，每次1000、总预算30s，不建额外平台。100k synthetic events 验证两个窗口及 EXPLAIN，实际结果记录planning-validation.md。

### 6. Collector 生命周期与可靠性边界

ADR-0001 明确单实例 Control；本版假设一个进程且每个 Node 一个 loop，复用 `cmd/control/main.go` shutdownContext。禁止多个部署或其它消费者同时消费同 Node，未来多实例另行设计，不新增 distributed scheduler。

每批 current lookup → pop → normalize → resolve identity → classify → transactional insert。malformed单项记录计数warning并继续保留同批合法事件；响应级错误不伪装空队列。DB临时失败时保留当前批次并 backoff 重试，提交前不继续pop。取消终止HTTP和等待；已提交批次重放安全，重启后的重复 hash 不重复计数。**幂等不等于无丢失**：Node已pop但HTTP响应丢失，或Control提交前进程崩溃，会有无法重放的缺口。无 ACK 的现有 source 不允许宣称 exactly-once；不为此引入RESP/额外日志平台。

## Risks / Trade-offs

- [Risk] 缺少可靠身份导致错误账号归属。→ NULL保留，账号统计排除，Node/Provider保留并计数，不猜。
- [Risk] destructive source 丢失窗口。→ 明确来源限制、单消费者、批次提交前不继续消费；重复安全与丢失风险分别验证。
- [Risk] 当前auth-files不能重建历史。→ 仅接受当前唯一、无冲突证据，缺失/冲突NULL；不构建temporal subsystem。

## Migration Plan

采用 additive migration，完成专项真实 PostgreSQL 验收；发布时先schema后开启collector；回滚关闭collector并保留forward表。**本轮禁止 push/deploy/archive**，不运行真实 Node queue pop，避免提前消耗事件。

### Runtime 接线

`CONTROL_ACCOUNT_REQUEST_QUALITY_ENABLED` 默认false；开启时必须已配置CLIProxy management driver。复用同一driver实例与shutdownContext。只读取当前active monitoring、CLIProxy v1且inventory capability的Node，SECURITY DEFINER targets query不扩大reader_secret_ref直接权限。每30s刷新目标集；删除/停用/配置改变先取消并等待旧loop退出再重建。目标读取失败停止已有loop并重试，避免长期消费已停用Node。进程退出等待loop回收。

本次只提供Go repository `AccountQuality` 和 `NodeProviderQuality`，无新增HTTP API/生成client/UI。数据属于观测结果，不参与inventory freshness或duplicate ownership eligibility。
