## MODIFIED Requirements

### Requirement: Control SHALL 按 epoch-aligned 180s slot 轮询 Directory 并记录每次结果

Control SHALL 为唯一 current active Gateway（lifecycle_status=active 且 singleton_id=1） 以 epoch-aligned 的 180 秒 slot 触发 Directory ingestion run。`scheduled_at` MUST 等于该 epoch-aligned slot，且 `(gateway_instance_id, scheduled_at)` MUST 唯一。每次轮询 MUST 持久化一条 ingestion result；成功、失败、未变化、恢复、超时和重试都 MUST 形成可恢复的 run 证据。重复 scheduler tick 或重启遇到同一 slot MUST 复用已有 run，不得创建第二条。Control MUST NOT 为同一 Gateway 并发保留多个 active ingestion run，也 MUST NOT backfill 从未实际创建过的历史 slot。

#### Scenario: 到达下一个轮询槽
- **WHEN** 某 Gateway 的下一轮 epoch-aligned 180 秒 slot 到达
- **THEN** Control 以该 slot 作为 `scheduled_at` 创建或唤醒 ingestion run，并以数据库时间记录该轮证据

#### Scenario: 同一 slot 被重复调度
- **WHEN** scheduler tick 或重启再次处理同一 Gateway 的同一 `scheduled_at`
- **THEN** `(gateway_instance_id, scheduled_at)` 唯一约束确保只复用已有 run，不创建第二条

#### Scenario: 同一 Gateway 的并发 worker
- **WHEN** 两个 worker 同时认领同一 Gateway 的待处理 run
- **THEN** 只有一个 worker 获得 lease/fencing，另一个跳过该 run 且不得产生第二份成功证据

#### Scenario: 重启后恢复待完成 run
- **WHEN** Control 或 worker 在 run 进行中重启
- **THEN** Reconciler 依据持久 run 状态恢复同一轮 ingestion，不创建重复 snapshot 或倒退 current pointer

#### Scenario: 停机期间错过一个 cadence
- **WHEN** Control 在某个 180 秒槽期间停机，且该槽没有实际执行 worker
- **THEN** Control 重启后只能恢复已创建的 non-terminal run，不能伪造或回填该历史槽的 Directory observation，并从当前 cadence 继续

所有正常 fetch/re-fetch eligibility MUST 为同一个 source Gateway active 且 singleton_id=1；run.gateway_instance_id 必须匹配。Retire/Replace commit 后不得授权新的 outbound；已授权并发生的有界 transport evidence 可保留，promotion 仍必须独立重查。固定 cadence、retry预算、严格 response/source-time 验证不变。

#### Scenario: 零current Gateway
- **WHEN** 不存在 current active Gateway
- **THEN** 不创建 normal ingestion run，历史 run 仍可读

### Requirement: Control SHALL 使用固定 HTTP fetch contract

Control SHALL 以 `GET /internal/v1/api-account-directory` 通过 HTTP 或 HTTPS fetch Directory（无目标许可列表，HTTPS不验证证书），并 MUST 使用 `Authorization: Bearer token`，其中 token 由现有 `gateway_instances.reader_secret_ref` 经 `SecretResolver` resolve 后获得；reference 本身不得被当作 token/path。HTTP fetch MUST NOT follow redirects；single response body 的读取上限 MUST be 4 MiB；`accounts` 上限 MUST be 10,000；非 200、timeout、partial body、retryable failure 以及读取超限 MUST 先记录 attempt failure，再按 retryability 分类；raw body MUST NOT 被持久化。

#### Scenario: 正常 fetch
- **WHEN** Control 以HTTPS或HTTP对 `GET /internal/v1/api-account-directory` 发起带 Bearer token 的请求
- **THEN** fetch 继续进入验证流程

#### Scenario: redirect
- **WHEN** Gateway 返回 redirect
- **THEN** Control 不跟随 redirect，整轮 ingestion failed

#### Scenario: body 超限
- **WHEN** response body 超过 4 MiB
- **THEN** Control 停止读取并把整轮 ingestion 标记为 failed

#### Scenario: 账号数超限
- **WHEN** `accounts` 数量超过 10,000
- **THEN** Control 整轮 rejected/failed，不保存 raw body

所有正常 fetch/re-fetch eligibility MUST 为同一个 source Gateway active 且 singleton_id=1；run.gateway_instance_id 必须匹配。Retire/Replace commit 后不得授权新的 outbound；已授权并发生的有界 transport evidence 可保留，promotion 仍必须独立重查。固定 cadence、retry预算、严格 response/source-time 验证不变。

#### Scenario: Retire先于fetch
- **WHEN** run 尚未 outbound，Gateway Retire 已 commit
- **THEN** 无 HTTP call，run terminal non-success，不 promotion、不刷新freshness

### Requirement: Control SHALL 用 last_success_received_at 计算 freshness，失败不得刷新

Control SHALL 只使用 Control DB 的 `last_success_received_at` 计算 freshness。未曾成功观察时 freshness MUST 为 unknown；当 `now - last_success_received_at` 超过 540 秒时该 Gateway MUST 视为 stale。失败、timeout、validation reject、partial read、重试 attempt、或未变内容的中间 attempt 都 MUST NOT 刷新 freshness；只有最终成功提交的 successful observation 才能刷新 freshness。

#### Scenario: 成功观察后仍在 540 秒内
- **WHEN** 轮询成功且 `now - last_success_received_at <= 540s`
- **THEN** 该 Gateway 视为 fresh

#### Scenario: 超过 540 秒未成功
- **WHEN** 没有新的成功观察且已超过 540 秒
- **THEN** 该 Gateway 视为 stale，而 current snapshot pointer 仍保持最近成功内容

#### Scenario: 失败后不刷新
- **WHEN** 某次 ingestion 失败或被拒绝
- **THEN** `last_success_received_at` 保持不变，freshness 继续按旧成功时间老化

#### Scenario: 重试中间 attempt 失败
- **WHEN** 同一 durable run 的某次 retry attempt 失败但后续还会重试
- **THEN** 失败 attempt 不刷新 `last_success_received_at`

#### Scenario: 重试最终成功
- **WHEN** 同一 durable run 在允许重试窗口内的最终 attempt 成功提交
- **THEN** 只有这次最终成功提交刷新 `last_success_received_at`

Finalize/promotion MUST 先锁 source Gateway row 并重查 lifecycle_status=active、singleton_id=1、run source identity 匹配，再取得 DB time 并按原 fencing/lease/strict validation 提交。若已 retired/replaced，MUST NOT promote、刷新 freshness 或改 current pointer；run 以 failed 和 gateway_retired/gateway_replaced 固定 non-retryable classification 收敛，既有 immutable terminal evidence 不重写。新 Gateway 的 current snapshot、last_success_received_at、last_source_generated_at 完全独立。

#### Scenario: Replace独立freshness
- **WHEN** old -> new Replace commit
- **THEN** new freshness unknown，current pointer 和 last_success_received_at 不继承 old

### Requirement: Control SHALL 冻结 current state 的写入时钟

Control SHALL 在 finalize transaction 中以 PostgreSQL DB time 记录 `received_at` 候选值。只有成功提交的 attempt 才能写入 `last_success_received_at`；失败 attempt、timeout、validation reject、partial body 或被 lease/fencing 否决的 attempt MUST NOT 写入该值。`generated_at` 的 backward-skew MUST 只与上一条成功的 `last_source_generated_at` 比较；首次成功观察 MUST NOT 执行 backward comparison，但仍 MUST 正常写入 `last_source_generated_at`、`last_success_received_at` 并按内容创建或复用 snapshot/current pointer。

#### Scenario: successful commit
- **WHEN** 轮询最终成功提交
- **THEN** Control 以 finalize transaction 的 DB time 写入 `last_success_received_at`

#### Scenario: failed attempt
- **WHEN** attempt 失败或超时
- **THEN** Control 不写入 `last_success_received_at`

#### Scenario: 首次成功
- **WHEN** 某 Gateway 的第一条合法成功观察提交
- **THEN** Control 不执行 backward comparison，但仍正常记录 `last_source_generated_at`、`last_success_received_at`，并按内容创建或复用 snapshot/current pointer

#### Scenario: stale 后恢复成功
- **WHEN** stale Gateway 随后再次成功返回合法 Directory
- **THEN** Control 更新 `last_success_received_at` 并将 freshness 恢复为 fresh

Finalize/promotion MUST 先锁 source Gateway row 并重查 lifecycle_status=active、singleton_id=1、run source identity 匹配，再取得 DB time 并按原 fencing/lease/strict validation 提交。若已 retired/replaced，MUST NOT promote、刷新 freshness 或改 current pointer；run 以 failed 和 gateway_retired/gateway_replaced 固定 non-retryable classification 收敛，既有 immutable terminal evidence 不重写。新 Gateway 的 current snapshot、last_success_received_at、last_source_generated_at 完全独立。

#### Scenario: Retire先于promotion
- **WHEN** HTTP已发生，Retire先取得Gateway锁并commit
- **THEN** 保留有界transport evidence，拒绝promotion/current pointer/freshness更新

#### Scenario: promotion先于Retire
- **WHEN** promotion先持Gateway锁并commit
- **THEN** 已提交snapshot保留为old历史，Retire随后阻止未来promotion

### Requirement: Control SHALL 在恢复、幂等和回写未知结果时保持单份真相

Control SHALL 为 ingestion run 使用 lease/fencing、数据库短事务和幂等结果写入，确保同一轮轮询只产生一次有效 success 或 failure 结论。commit 前丢失的内存响应不可重放；若同一 durable run 仍满足 active/current eligibility 与 retry 条件，Control MUST 复用同一 run 发起新的 fenced re-fetch attempt；否则该 run MUST 终结为 failed。unknown commit 只能通过幂等键、唯一约束和 fencing 判定，不得重建内存结果。任何失败恢复都 MUST 保留 current pointer、freshness 和已提交 snapshot 不变。

#### Scenario: worker 在提交前崩溃
- **WHEN** worker 已完成验证但在持久化前崩溃
- **THEN** 内存中的 response 不可重放；lease 到期后若同一 durable run 仍满足 active/current eligibility 与 retry 条件则进行新的 fenced re-fetch attempt，否则将该 run 终结为 failed

#### Scenario: 旧 fencing token 回写
- **WHEN** 旧 worker 使用过期 fencing token 尝试写回
- **THEN** 写入影响零行，旧 worker 的结果不得覆盖新结果

#### Scenario: 提交结果未知
- **WHEN** Control 不知道上一轮事务是否提交成功
- **THEN** 幂等键和 fencing 必须确保最终只有一份结果被记录

#### Scenario: commit 前丢失内存响应
- **WHEN** worker 在持久化前丢失内存中的 Directory response
- **THEN** 该 response 不可被重放；若同一 durable run 仍满足 active/current eligibility 与 retry 条件，则发起新的 fenced re-fetch attempt，否则该 run 终结为 failed

#### Scenario: lease 过期且仍可重试
- **WHEN** durable run 的 lease 已过期，但仍在允许重试窗口内且 source Gateway active/current
- **THEN** Control 复用同一 run 进行新的 fenced re-fetch attempt

#### Scenario: lease 过期且不可再试
- **WHEN** durable run 的 lease 已过期且已超出允许重试窗口
- **THEN** 该 run 终结为失败，不得继续拉取该历史 slot

所有正常 fetch/re-fetch eligibility MUST 为同一个 source Gateway active 且 singleton_id=1；run.gateway_instance_id 必须匹配。Retire/Replace commit 后不得授权新的 outbound；已授权并发生的有界 transport evidence 可保留，promotion 仍必须独立重查。固定 cadence、retry预算、严格 response/source-time 验证不变。

#### Scenario: replacement后stale worker
- **WHEN** 旧worker恢复或持旧token回写
- **THEN** 不得retarget到new；old不fetch、不promotion、不刷新freshness

### Requirement: Control SHALL 冻结 retryability 分类

Control SHALL 将以下情况视为 retryable：transport/network failure、timeout、partial read、429、5xx、Control transient finalize/commit failure，以及允许恢复的 lease/unknown execution。Control SHALL 将以下情况视为 non-retryable：401、403、404、其它非 429 的 4xx、200 contract invalid、source-time reject、hard-limit reject、Secret unavailable/invalid。non-retryable 情况 MUST 直接 failed，不得进入 retry_wait。

#### Scenario: retryable failure 且预算仍可用
- **WHEN** 第一次 attempt 遇到 retryable failure，且在 `scheduled_at + 120s` 之前并且 attempts 未耗尽
- **THEN** run 进入 retry_wait

#### Scenario: transport failure
- **WHEN** fetch 遇到 transport/network failure
- **THEN** Control 视为 retryable

#### Scenario: 429 或 5xx
- **WHEN** fetch 返回 429 或 5xx
- **THEN** Control 视为 retryable

#### Scenario: 401/403/404
- **WHEN** fetch 返回 401、403 或 404
- **THEN** Control 视为 non-retryable 并直接 failed

#### Scenario: contract invalid
- **WHEN** HTTP 200 但 Directory contract invalid
- **THEN** Control 视为 non-retryable 并直接 failed

#### Scenario: Secret unavailable
- **WHEN** SecretResolver 无法解析现有 reference
- **THEN** Control 视为 non-retryable 并直接 failed

gateway_retired 与 gateway_replaced MUST 是 non-retryable lifecycle failure；不得因既有 retry budget 尚有余量而重启 old fetch。

#### Scenario: 退休不重试
- **WHEN** attempt被 lifecycle fence否决
- **THEN** terminal failed，固定 lifecycle failure code，零重试
