# gateway-account-directory-ingestion Specification

## Purpose
定义 Control 对 Gateway API Account Directory 的只读 ingestion、严格验证、immutable snapshot、freshness、恢复与脱敏边界。

## Requirements

### Requirement: Control SHALL 保留三个逻辑生命周期

Control SHALL 将 Directory 相关持久状态划分为三个互相独立的逻辑生命周期：`ingestion run`、`content snapshot` 和 `current state`。`snapshot_items` MUST 只是 `content snapshot` 的 child rows，不得被当作独立生命周期真相。`ingestion run` MUST 是可恢复的 durable state machine；只有 terminal evidence 完成后，run 才能变成不可变终态。`content snapshot` 与 `snapshot_items` MUST 在同一事务创建，并在提交后都变为 immutable；不得单独增删改其中任一侧。

#### Scenario: run、snapshot、current state 同时存在
- **WHEN** 某 Gateway 既有最近一次成功观察、又有历史 snapshot_items、同时还有进行中的 ingestion run
- **THEN** Control 必须分别保存三者，不得把其中任一生命周期重建成另两个生命周期

### Requirement: Control SHALL 按 epoch-aligned 180s slot 轮询 Directory 并记录每次结果

Control SHALL 为每个已登记 Gateway 以 epoch-aligned 的 180 秒 slot 触发 Directory ingestion run。`scheduled_at` MUST 等于该 epoch-aligned slot，且 `(gateway_instance_id, scheduled_at)` MUST 唯一。每次轮询 MUST 持久化一条 ingestion result；成功、失败、未变化、恢复、超时和重试都 MUST 形成可恢复的 run 证据。重复 scheduler tick 或重启遇到同一 slot MUST 复用已有 run，不得创建第二条。Control MUST NOT 为同一 Gateway 并发保留多个 active ingestion run，也 MUST NOT backfill 从未实际创建过的历史 slot。

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

### Requirement: Control SHALL 受固定执行预算约束

Control SHALL 固定以下第一版执行预算：`max_attempts = 2`、`attempt timeout = 5s`、`lease = 15s`、`retry start deadline = scheduled_at + 120s`。状态集 MUST 固定为 `pending/running/retry_wait/succeeded/failed`；`changed` 与 `unchanged` MUST 只是成功结果的 outcome，而不是独立状态。Control MUST NOT 新增 attempt history 表来表达第一版执行预算。

#### Scenario: 首次 attempt 在 5 秒内成功
- **WHEN** ingestion run 的首个 attempt 在 5 秒内成功提交
- **THEN** run 进入 succeeded，并记录 changed 或 unchanged outcome

#### Scenario: retryable failure 且预算仍可用
- **WHEN** 第一次 attempt 遇到 retryable failure，且在 `scheduled_at + 120s` 之前并且 attempts 未耗尽
- **THEN** run 进入 retry_wait

#### Scenario: 第二次 attempt 仍失败
- **WHEN** 第二次 attempt 失败或超时
- **THEN** run MUST 终结为 failed，不得继续新增 attempt history

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

### Requirement: Control SHALL 对整个 Directory 响应做 fail-closed 验证

Control SHALL 对 Gateway 返回的整个 Directory 响应执行全量严格验证。任何 schema、type、id、order、required field、duplicate id、unsafe non-null URL、hard-limit、空值位置、source-time sanity 或其它契约错误 MUST 整体拒绝，且 MUST NOT 通过过滤、修补或 re-sanitize 接受部分内容。`generated_at` 仅可用于 source-time/replay sanity，不得用于 freshness 计算。contract 外额外字段或敏感字段 MUST 整轮 reject；redaction 仅可用于诊断输出。

#### Scenario: 缺失 required field
- **WHEN** 响应缺少 required field 或字段类型不匹配
- **THEN** Control 整体拒绝该响应，不创建 snapshot，也不刷新 freshness

#### Scenario: 重复 id 或错误顺序
- **WHEN** 响应出现重复 `id` 或违反冻结顺序规则
- **THEN** Control fail closed，保留现有 current snapshot 与 current pointer

#### Scenario: unsafe non-null URL
- **WHEN** 响应包含带 userinfo、query、fragment 或其它不安全成分的非空 URL
- **THEN** Control 整体拒绝该响应，不把坏 URL 进行清洗后继续接受

#### Scenario: source-time 不可信
- **WHEN** `generated_at` 缺失、格式错误、超出允许 skew，或与 replay sanity 不一致
- **THEN** Control 将整轮 ingestion 视为失败，不把该时间用于 freshness

#### Scenario: 响应含 contract 外字段
- **WHEN** Gateway 响应包含未冻结的额外字段或敏感字段
- **THEN** Control 整轮 reject，不以 redaction 后的诊断文本代替真实响应继续处理

### Requirement: Control SHALL 冻结 `generated_at` source-time policy

Control SHALL 将 `generated_at` 仅作为 source-time sanity / replay 检查使用，并冻结以下边界：`future tolerance = 30s`、`maximum source age = 24h`、`allowed backward skew = 5m`。candidate `received_at` MUST 使用 finalize transaction 的 PostgreSQL DB time。`generated_at` 超过未来容忍、超过最大来源年龄、或相较上一条成功观察回退超过允许后退幅度时，Control MUST reject 整轮 ingestion。

#### Scenario: 未来时间超限
- **WHEN** `generated_at > received_at + 30s`
- **THEN** Control reject 该轮 ingestion

#### Scenario: 来源年龄过大
- **WHEN** `generated_at < received_at - 24h`
- **THEN** Control reject 该轮 ingestion

#### Scenario: 后退幅度超限
- **WHEN** 本次合法响应的 `generated_at` 相较上一条成功观察回退超过 5m
- **THEN** Control reject 该轮 ingestion

#### Scenario: 边界值
- **WHEN** `generated_at` 恰好等于允许边界
- **THEN** Control MUST accept 该时间边界并继续后续验证

#### Scenario: 首次成功观察
- **WHEN** 某 Gateway 第一次成功提交合法 Directory
- **THEN** Control 仅跳过 backward-skew comparison；仍正常写入 `last_source_generated_at`、`last_success_received_at`，并按 fingerprint 创建或复用 snapshot、推进 current pointer

### Requirement: Control SHALL 在 normalized content 改变时 create-or-reuse immutable snapshot

Control SHALL 将通过验证的 Directory 规范化为 deterministic normalized content，并以内容 fingerprint 判定是否有实际变化。current snapshot pointer 与 last successful observation MUST 分离。fingerprint 等于 current 时，成功轮询 MUST 只刷新成功 observation，不创建或切换 snapshot；fingerprint 不同于 current 时，Control MUST 按 `(gateway_instance_id, fingerprint)` create-or-reuse immutable content snapshot：历史 snapshot 已存在则复用，不存在才创建 snapshot + items，随后推进 current pointer。

#### Scenario: 第一次成功获取 Directory
- **WHEN** 某 Gateway 首次产生合法 Directory 响应
- **THEN** Control 创建首个 content snapshot，并记录 current pointer 与 last success observation

#### Scenario: 相同内容再次成功轮询
- **WHEN** 下一次成功轮询得到相同 normalized content
- **THEN** Control 不创建新 snapshot，只更新 `last_success_received_at`、`last_source_generated_at` 和 last success run 引用

#### Scenario: 内容变化
- **WHEN** normalized content 与当前 fingerprint 不同
- **THEN** Control 先按 `(gateway_instance_id, fingerprint)` 查找历史 snapshot；已存在则复用，不存在才原子创建 snapshot + items，随后推进 current pointer 到该 fingerprint

#### Scenario: 空 Directory
- **WHEN** Gateway 返回合法但空的 `accounts:[]`
- **THEN** Control 仍创建合法 snapshot，并保存 0 个 snapshot_items

### Requirement: Control SHALL 冻结 content fingerprint 字段集

Control SHALL 以 `schema_version` 加上按 `id` 升序排列的 `id`、`name`、`platform`、`type`、`url`、`status` 计算 content fingerprint。fingerprint MUST 使用 SHA-256 和明确的 canonical encoding version。canonical v1 preimage MUST be UTF-8 compact JSON array `[1,schema_version,[[id,name,platform,type,url,status],...]]`，accounts 按 `id ASC` 排序，`url` null 使用 JSON null，string 使用标准 JSON escaping，不使用 map/object，不包含 insignificant whitespace。`generated_at`、`received_at`、`request_id`、`run_id` 和任何 HTTP metadata MUST NOT 进入 fingerprint。`(gateway_instance_id, fingerprint)` MUST 唯一；A→B→A 时 Control MUST 复用已有 A snapshot 并仅更新 current pointer，不得创建重复 snapshot。fingerprint canonicalization MUST 依赖固定字段顺序和稳定编码；不允许把 child row 计数、log、诊断 redaction 或 response metadata 混入 fingerprint。

#### Scenario: fingerprint 含 schema 与 account fields
- **WHEN** Control 计算 content fingerprint
- **THEN** 只使用 `schema_version` 以及按 `id` 升序的 `id/name/platform/type/url/status`

#### Scenario: fingerprint 排除元数据
- **WHEN** 响应中存在 `generated_at`、`received_at`、`request_id`、`run_id` 或 HTTP metadata
- **THEN** 这些字段不参与 fingerprint，也不影响 snapshot 身份

#### Scenario: fingerprint canonical v1
- **WHEN** Control 计算 fingerprint
- **THEN** preimage MUST 为 `UTF-8` compact JSON array `[1,schema_version,[[id,name,platform,type,url,status],...]]`，并对该 preimage 取 `SHA-256`

#### Scenario: A→B→A 回到原内容
- **WHEN** 某 Gateway 的 fingerprint 依次变化为 A、B、再回到 A
- **THEN** Control 复用已有 A snapshot，仅更新 current pointer，不创建重复 snapshot

### Requirement: Control SHALL 使用固定 HTTP fetch contract

Control SHALL 以 `GET /internal/v1/api-account-directory` 通过 HTTPS fetch Directory，并 MUST 使用 `Authorization: Bearer token`，其中 token 由现有 `gateway_instances.reader_secret_ref` 经 `SecretResolver` resolve 后获得；reference 本身不得被当作 token/path。HTTP fetch MUST NOT follow redirects；single response body 的读取上限 MUST be 4 MiB；`accounts` 上限 MUST be 10,000；非 200、timeout、partial body、retryable failure 以及读取超限 MUST 先记录 attempt failure，再按 retryability 分类；raw body MUST NOT 被持久化。

#### Scenario: 正常 fetch
- **WHEN** Control 以 HTTPS 对 `GET /internal/v1/api-account-directory` 发起带 Bearer token 的请求
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

### Requirement: Control SHALL 在恢复、幂等和回写未知结果时保持单份真相

Control SHALL 为 ingestion run 使用 lease/fencing、数据库短事务和幂等结果写入，确保同一轮轮询只产生一次有效 success 或 failure 结论。commit 前丢失的内存响应不可重放；若同一 durable run 仍满足 retry 条件，Control MUST 复用同一 run 发起新的 fenced re-fetch attempt；否则该 run MUST 终结为 failed。unknown commit 只能通过幂等键、唯一约束和 fencing 判定，不得重建内存结果。任何失败恢复都 MUST 保留 current pointer、freshness 和已提交 snapshot 不变。

#### Scenario: worker 在提交前崩溃
- **WHEN** worker 已完成验证但在持久化前崩溃
- **THEN** 内存中的 response 不可重放；lease 到期后若同一 durable run 仍满足 retry 条件则进行新的 fenced re-fetch attempt，否则将该 run 终结为 failed

#### Scenario: 旧 fencing token 回写
- **WHEN** 旧 worker 使用过期 fencing token 尝试写回
- **THEN** 写入影响零行，旧 worker 的结果不得覆盖新结果

#### Scenario: 提交结果未知
- **WHEN** Control 不知道上一轮事务是否提交成功
- **THEN** 幂等键和 fencing 必须确保最终只有一份结果被记录

#### Scenario: commit 前丢失内存响应
- **WHEN** worker 在持久化前丢失内存中的 Directory response
- **THEN** 该 response 不可被重放；若同一 durable run 仍满足 retry 条件，则发起新的 fenced re-fetch attempt，否则该 run 终结为 failed

#### Scenario: lease 过期且仍可重试
- **WHEN** durable run 的 lease 已过期，但仍在允许重试窗口内
- **THEN** Control 复用同一 run 进行新的 fenced re-fetch attempt

#### Scenario: lease 过期且不可再试
- **WHEN** durable run 的 lease 已过期且已超出允许重试窗口
- **THEN** 该 run 终结为失败，不得继续拉取该历史 slot

### Requirement: Control SHALL 仅保存 Secret reference 且不得泄露 raw response

Control SHALL 只保存指向 service token 的 opaque reference，并 MUST 复用既有 `gateway_instances.reader_secret_ref`；reference 必须先经现有 `SecretResolver` resolve 成 Bearer token，resolve 失败时 Control MUST failed。Control MUST NOT 新增 Directory 专用 Secret 表或字段，也不得保存 service token 原文或 Gateway DB credential。raw response、原始错误、endpoint userinfo、query string、body、Secret、日志、指标标签、审计 detail 和测试 artifact MUST NOT 包含可逆凭证内容。若响应出现 contract 外敏感字段或无法安全处理的额外字段，Control MUST fail closed。

#### Scenario: 失败路径注入 canary
- **WHEN** 测试向 token、URL、错误文本和 body 注入唯一 canary
- **THEN** canary 不能出现在普通日志、指标标签、审计 detail 或持久化失败原因中

#### Scenario: 缺少 token reference
- **WHEN** Gateway 的 service token reference 不可用
- **THEN** Control fail closed，并把该轮 ingestion 记录为失败而不是退化为无认证访问

#### Scenario: 只存在 reader_secret_ref
- **WHEN** Gateway 只提供现有 `gateway_instances.reader_secret_ref`
- **THEN** Control 直接复用该 opaque reference，经 `SecretResolver` resolve 后再作为 Bearer token，不新增 Directory 专用 Secret 存储结构

### Requirement: Control SHALL 接受冻结 contract 的兼容响应

Control SHALL 接受合法空 `accounts:[]` 响应；unknown `platform` 与 `status` 字符串 MUST 保留并原样进入规范化结果；`type` MUST 只接受冻结 contract 的 `apikey` 和 `upstream`，Control MUST NOT 自行 filter、repair 或 re-sanitize 其它 type。

#### Scenario: 空 accounts
- **WHEN** Gateway 返回 `accounts:[]`
- **THEN** Control 视为合法 Directory，并创建 snapshot + 0 items

#### Scenario: unknown platform/status
- **WHEN** Gateway 返回未知 platform 或 status 字符串
- **THEN** Control 保留这些字符串，不自行改写或丢弃

#### Scenario: 非冻结 type
- **WHEN** `type` 不是 `apikey` 或 `upstream`
- **THEN** Control 整轮 reject，不自行 filter/repair

### Requirement: Control SHALL 不修改 Gateway、Account、Group、scheduler 或 CLIProxyAPI

Gateway Directory ingestion SHALL 为纯读路径。Control MUST NOT 通过 ingestion 修改 Gateway Account、Group、scheduler、binding、CLIProxyAPI 运行时状态或任何数据面配置；也 MUST NOT 为补救坏响应而调用 Gateway 的写路径。所有成功与失败结果都只影响 Control 自己的 Directory run、snapshot 和 current state。

#### Scenario: 验证失败
- **WHEN** Directory 响应因任何契约错误被拒绝
- **THEN** Control 不回写 Gateway，也不对 Account/Group/scheduler 进行修正

#### Scenario: 重复或恢复轮询
- **WHEN** 同一 Gateway 连续多轮成功或失败
- **THEN** 只更新 Control 的 Directory state，不产生外部写操作

#### Scenario: 任何外部写路径尝试
- **WHEN** ingestion 实现尝试调用 Gateway 写接口或 CLIProxyAPI 变更接口
- **THEN** 该实现违反契约，必须 fail closed，且不得进入生产
