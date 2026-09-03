# add-gateway-account-directory-ingestion Design

## Context

System Design v1.8 / R4.7 已冻结 Phase 4 Directory ingestion 的核心语义：Control 以 180 秒 cadence 轮询 Gateway Directory，540 秒后按 `last_success_received_at` 判定 stale，失败不刷新 freshness，`generated_at` 只做 source-time / replay sanity。Directory 只允许整体接收或整体拒绝，Control 不得做过滤、修补或 re-sanitize。

该 change 只落 Control 侧 ingestion 真相源，不碰 binding、duplicate ownership、Gateway/CLIProxyAPI routing/scheduling，也不新增 Directory 专用 Secret 存储。Secret 解析必须复用现有 `gateway_instances.reader_secret_ref` 与 `SecretResolver`，不得新增 Directory 专用 Secret 形态。

## Goals

- 把 Gateway Directory 变成 Control 内部可恢复、可幂等、可审计的持久真相源。
- 将一次成功观察与一次内容变更解耦，避免无变化 snapshot 膨胀。
- 让 freshness 只依赖 Control PostgreSQL 的成功观察时间，不依赖 Gateway clock。
- 在重启、并发 worker、重复调度和部分故障下保持单份真相。

## Non-Goals

- 不做 Gateway、Account、Group、scheduler 或 CLIProxyAPI 的任何写操作。
- 不实现 Node ↔ Gateway binding、duplicate ownership 或 routing/scheduling 变更。
- 不新增产品 API/UI，也不把 Directory 结果直接暴露给普通请求数据面。
- 不保存 raw response、Gateway service token、Gateway DB credential 或任何可逆 Secret 派生值。
- 不新增 Directory 专用 Secret 表或字段；只复用 `gateway_instances.reader_secret_ref`。

## Data Model

逻辑上只有三个生命周期：`ingestion run`、`content snapshot`、`current state`。物理上可以用四张表承载它们：`run`、`snapshot`、`snapshot_items` 和 `current_state`。`run` 在 terminal 之前可变，terminal 之后 immutable；`snapshot` 与 `snapshot_items` 必须在同一事务创建并在之后保持 immutable。`snapshot_items` 只是 `snapshot` 的 child rows，不是独立生命周期。

1. **Directory ingestion run**：每次轮询的状态机、状态、失败分类、时间戳、lease/fencing 和 fingerprint。
2. **Directory content snapshot**：规范化后的 immutable content-addressed snapshot；当 normalized content 相对 current 变化时，按 `(gateway_instance_id, fingerprint)` create-or-reuse，只有历史中不存在该 fingerprint 时才创建。
3. **Directory current state**：保存 `current_snapshot_id`、`current_content_fingerprint`、`last_success_received_at`、`last_source_generated_at`、`last_success_run_id`。
4. **Snapshot items**：保存已验证的 Directory account items，受唯一键和全量一致性约束保护。

content fingerprint 必须来自规范化后的全量响应，不得从局部字段、日志或异常路径重建。
empty Directory 是合法的 content snapshot；它对应一个 snapshot 加 0 条 snapshot_items。

## Lifecycle Boundaries

- **Ingestion run**：可重启、可重试、可被 Reconciler 接管的 durable state machine。只有 terminal evidence 才把它变成不可变终态。
- **Content snapshot**：immutable content-addressed 结果；normalized content 相对 current 变化时 create-or-reuse，历史 fingerprint 已存在则复用。
- **Current state**：保存最后一次成功观察及当前指针，独立于 run 和 snapshot 的生命周期。

## Ingestion Flow

1. Scheduler 以 epoch-aligned 180 秒 slot 为每个 Gateway 创建/唤醒 ingestion run；`scheduled_at` 必须落在该 slot，且 `(gateway_instance_id, scheduled_at)` 唯一，重复 tick 或重启只能复用同一 run。
2. Worker 通过 PostgreSQL lease/fencing 认领唯一 active run。
3. Worker 读取 Gateway Directory，先做 whole-response validation，再做 canonical normalization。
4. 如果 validation 失败、timeout、partial read、response 含 contract 外额外字段，或 fetch 发生 retryable / non-retryable failure：先记录 attempt failure，再按 retryability 分类；retryable 且 attempt/window 尚可用时进入 retry_wait，non-retryable 或预算耗尽时进入 failed。validation/contract reject 直接 failed。
5. 如果 validation 成功但 normalized content 未变化：仅在最终成功提交时刷新 `last_success_received_at`、`last_source_generated_at` 和 last success run 引用。
6. 如果 normalized content 相对 current 变化：先按 `(gateway_instance_id, fingerprint)` 查找历史 snapshot；存在则复用，不存在才在同一事务创建 snapshot + items，然后推进 current pointer 并写成功观察状态。
7. 如果 worker 需要重试，只有最终成功提交的那次 attempt 才能刷新 freshness；中间失败 attempt 只能留下失败证据。

## Validation and Canonicalization

- `generated_at` 只允许用于 source-time / replay sanity，不得参与 freshness 计算。
- schema、type、required field、id uniqueness、sort order、URL safety、size limits、duplicate detection、contract 外额外字段任一失败都整体拒绝。
- redaction 只允许用于诊断输出；不得先 redaction 再修补响应并继续接受。
- Control 必须按冻结 contract fail closed；不得通过缺省值、过滤、re-sanitize 或诊断 redaction 把坏响应改造成可接受响应。

## Frozen Source-Time Policy

- `future tolerance = 30s`
- `maximum source age = 24h`
- `allowed backward skew = 5m`

边界规则：

- `generated_at > received_at + 30s` → reject
- `generated_at < received_at - 24h` → reject
- `generated_at` 比上一条成功观察回退超过 5m → reject
- 精确等号边界按上式包含在允许范围内

## Concurrency and Recovery

- 每个 Gateway 同时最多一个 active ingestion run。
- Worker、Reconciler 和重启后恢复逻辑必须保持幂等；同一轮轮询不能产生两条成功快照或倒退 current pointer。
- commit 前丢失的内存响应不可重放；只有持久化到 run 的证据才可用于恢复。
- lease 过期后，若仍在允许重试窗口内，可复用同一 durable run 进行新的 fenced re-fetch attempt；否则该 run 终结为失败。
- unknown commit 只可通过幂等键、唯一约束和 fencing 判定，不得重建内存结果或猜测部分响应。
- 失败 run 不推进 freshness，不改变 current pointer，不生成 partial snapshot。
- stale/fresh/recovered 的切换只由成功观察推进，失败只会让旧成功时间继续老化。

## Secrets and Redaction

- 只允许保存 service token reference，不保存 token 原文；reference 必须复用 `gateway_instances.reader_secret_ref`。
- 不保存 Gateway DB credential。
- raw response、Secret、错误原文、endpoint、URL userinfo、查询串和 body 内容都不得进入普通日志、指标标签、审计 detail 或测试 artifact。

## Rollout / Rollback

- 变更应保持 additive：先上 schema，再上 worker/scheduler，再启用实际轮询。
- 回滚必须停止新的 ingestion 认领，但保留已提交的 run / snapshot / current state。
- 旧版本不得依赖新的 ingestion 表才能继续正常启动 Control 其他控制面能力。

## Risks

- **坏响应被部分接受** → 用 whole-response validation 和单事务提交阻断。
- **重复 snapshot 膨胀** → 以 `(gateway_instance_id, fingerprint)` 唯一约束和 create-or-reuse 阻止重复内容 snapshot。
- **freshness 误判** → 只看 `last_success_received_at`，失败不刷新。
- **Secret 泄漏** → 只保留引用，所有输出路径统一脱敏。
