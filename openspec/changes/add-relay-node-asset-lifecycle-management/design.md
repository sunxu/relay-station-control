## 1. 已验证的 baseline 与复用边界

本规划实际扫描了以下 source：

- `migrations/00003_asset_registry_foundation.sql`：`relay_node_assets` 当前字段为
  `instance_id`、`display_name`、`node_type`、`driver_contract_version`、
  `management_endpoint`、`reader_secret_ref`、generated `reader_secret_configured`、
  `created_at`、`updated_at`；`node_capabilities` 通过 `(instance_id,node_type,
  driver_contract_version)` FK 关联。既有 registrar 是
  `control_register_relay_node(uuid,text,text,text,text,text,text[])`。
- 同一 migration：`relay_node_inventory_monitoring_activations` 使用
  `effective_from/effective_to`、generated stored `active_range =
  tstzrange(effective_from,effective_to,'[)')`、`reason/actor` 和
  `end_reason/end_actor/end_recorded_at`；writer
  `control_set_node_inventory_monitoring` 已先锁 Node，再锁 monitoring row。
- `migrations/00011_relay_node_gateway_account_binding_foundation.sql`：
  current binding 由 `ended_at IS NULL` 表示，Node 与 Gateway 都有 partial unique
  index；binding history、identity 和 closed row 已有 database guard。
- `migrations/00004_durable_job_foundation.sql`：job status 为
  `pending|running|verifying|retry_wait|rolling_back|succeeded|failed|rolled_back|cancelled`，
  terminal row 由 `completed_at` 固化，reconciler/lease/fencing 已存在。
- `migrations/00005`、`00006`、`00007` 及 `queries/account_inventory_poll_runs.sql`：
  poll 的 `pending/running/retry_wait/finalized/abandoned`、Node/slot、lease/fencing、
  snapshot promotion 和 account lifecycle 均是 PostgreSQL durable truth。
- `queries/assets.sql`：现有 Node list/detail 仅按 `instance_id` keyset，现有
  `GetAssetCounts.nodes` 是全部 Node row 数；不可静默改成 active。
- `api/openapi.yaml`：既有 Node read routes 是 `GET /api/assets/nodes` 与
  `GET /api/assets/nodes/{instance_id}`；现有 binding routes 是独立的
  `/api/relay-bindings/...`。

Shared foundation 直接复用 Gateway Stage 1：唯一全局 `command_id`、
`asset_admin_command_receipts`、完整 UUID 派生事务 advisory lock、actor-first receipt
lookup、canonical intent encoding、`revision bigint`、`administrator_retire`/
`replacement`、signed compatibility manifest、external `relay-control-compat-gate`、
mandatory production wrapper、bounded audit/metrics。Node 不定义第二套 receipt、hash、
HMAC、lock 或 generic lifecycle framework。

## 2. Node persistence 与字段 ownership

### 2.1 `relay_node_assets`

Additive migration 增加并冻结：

```text
lifecycle_status text NOT NULL DEFAULT 'active'
revision bigint NOT NULL DEFAULT 1
retired_at timestamptz NULL
retired_by uuid NULL REFERENCES control_admin_users(admin_id)
retire_reason text NULL
```

约束为：

- `lifecycle_status IN ('active','retired')`、`revision >= 1`；
- active 时 `retired_at/retired_by/retire_reason` 全 NULL；
- retired 时三者全 non-NULL，`retire_reason IN ('administrator_retire','replacement')`；
- `retired_at >= created_at`；
- actor FK `ON UPDATE RESTRICT ON DELETE RESTRICT`；
- instance_id 继续 physical identity/PK，禁止 DELETE、identity reuse、retired→active、
  identity mutation、history overwrite。

Node field ownership 以当前 schema 为准：

- **不可变**：`instance_id`、`node_type`、`driver_contract_version`、capability
  declaration 的集合和 `(node_type,driver_contract_version,capability)` ownership。
  Edit 不允许重写 capability；Driver capability 变更必须走既有 driver/policy
  registration，不能借 lifecycle 命令偷换 contract。
- **可变**：`display_name`、规范化 `management_endpoint`（仅 `absent|set`，`clear`
  对这两个字段无效——不存在“空” display_name/management_endpoint 状态）、opaque
  `reader_secret_ref` 的完整 tri-state 配置（`absent|clear|set`）。它们均需显式 patch
  presence；endpoint 继续调用现有 `control_normalize_asset_endpoint`，Secret 只保存
  opaque reference。`updated_at` 由数据库写入。
- Replace 的 new row 重新验证全部 registration fields，revision=1；不复制旧
  display/endpoint/secret/capability，除非 request 明确提供且仍通过现有约束。

Register 要求新 `instance_id` 在全历史不存在，new row active/revision=1。历史 identity
不允许通过同 instance_id 再注册。

### 2.2 replacement lineage

新增 `relay_node_asset_replacements`：

```text
old_instance_id uuid NOT NULL
new_instance_id uuid NOT NULL
replaced_at timestamptz NOT NULL
replaced_by uuid NOT NULL REFERENCES control_admin_users(admin_id)
command_id uuid NOT NULL
PRIMARY KEY (old_instance_id)
UNIQUE (new_instance_id)
CHECK (old_instance_id <> new_instance_id)
FOREIGN KEY old/new -> relay_node_assets(instance_id)
```

`replaced_by` 为 `ON UPDATE RESTRICT ON DELETE RESTRICT`；`old_instance_id` 与
`new_instance_id` 均不可 UPDATE/DELETE/TRUNCATE。transactional insertion 与 old/new
lifecycle mutation 同一 transaction。A→B→C 合法；old/new unique、防重复 predecessor/
successor、锁内检查禁止 fork/merge/cycle/A→B→A。lineage 是 identity replacement，
不是 metadata edit；不继承 Gateway Account binding、monitoring current/future schedule、
Inventory current snapshot/truth、availability/request-quality current truth、durable job
ownership/result、credential/account state 或 CLIProxyAPI account state。

## 3. Shared command、canonical intent 与 receipt

每个 mutation 先在 transaction 外完成 authenticated active session、`super_admin`、CSRF、
JSON shape 和 `Cache-Control: no-store`，再用完整 command UUID 派生 advisory lock。
receipt lookup 顺序固定为：同 actor + 同 canonical intent replay；actor 不同或 intent
不同返回 `409 command_conflict`，不泄露旧结果；只有没有 receipt 才锁 asset、校验
revision/precondition。receipt 与 mutation audit、domain state 同事务提交；commit 前 crash
全部 rollback；commit 后 response 丢失时 replay 返回原始持久化 status/body，不重新读当前 row。

Node 在 shared canonical framework 中只注册以下 action-specific fields，不改变 Gateway
encoding，且必须逐字节复用 Stage 1 shared v1 encoding：

```text
Register: [1, "node.register", new_instance_id, display_name,
           normalized_management_endpoint, node_type, driver_contract_version,
           sorted_capabilities, secret_triplet]
Edit:     [1, "node.edit", instance_id, expected_revision,
           display_name_patch, management_endpoint_patch, secret_triplet]
Retire:   [1, "node.retire", instance_id, expected_revision,
           "administrator_retire"]
Replace:  [1, "node.replace", old_instance_id, expected_revision, new_instance_id,
           new_display_name, new_normalized_management_endpoint, new_node_type,
           new_driver_contract_version, sorted_new_capabilities,
           new_secret_triplet, "replacement"]
```

`secret_triplet` 必须与 Gateway shared foundation 完全相同的
`[operation, secret_fingerprint_key_version, secret_fingerprint]` 三元组：
`absent -> ["absent",null,null]`、`clear -> ["clear",null,null]`、
`set -> ["set",1,"<64 lowercase hex>"]`。Edit 的 `display_name_patch`/
`management_endpoint_patch` 复用既有 shared `absent/clear/set` patch-presence 词汇，但只
支持 `absent|set` 子集——`clear` 对这两个字段无效（不存在“空” display_name/
management_endpoint 状态）；只有 `reader_secret_ref`（即 `secret_triplet`）使用完整
`absent|clear|set` 三态。字段数组结构不变，仅描述性词汇与合法取值集合更正。

实际 implementation 使用 shared v1 fixed-order UTF-8 JSON arrays、SHA-256、K1/version
HMAC；Secret raw reference 不进入 bytes、receipt 或 audit。已发布的 `node.register`、
`node.edit`、`node.retire`、`node.replace` 这四个 `command_kind` 的 v1 数组冻结不可变；新增
`command_kind` 可以定义自己的 shared-v1 数组，但修改既有 `command_kind` 的字段集或语义必须
走显式评审的新 encoding version 或经证明的向后兼容机制，不允许原地扩展既有 v1 数组。Node 不
新增 encoding version、hash 算法或 HMAC key version scheme；implementation task 需增加
encoding fixture，逐字节校验每个 action 的数组顺序与上表一致。

Sanitized receipt result 固定保存成功 HTTP status/body。每个 action 都返回统一的
`result`、asset identity、`lifecycle_status`、decimal `revision`、display-safe metadata、
`secret_configured` 以及三个 non-negative count 字段
`closed_binding_count`、`closed_monitoring_count`、`cancelled_future_monitoring_count`；
Register/Edit 的这些 count 固定为 0，Replace 另有 lineage：

- Register：`result=registered`、asset projection 和三个 count=0。
- Edit：`result=updated`、asset projection 和三个 count=0。
- Retire：`result=retired`、asset projection、`closed_binding_count`、
  `closed_monitoring_count`、`cancelled_future_monitoring_count`。
- Replace：`result=replaced`、old/new asset projections、以上 counts、lineage
  (`old_instance_id,new_instance_id,replaced_at,replaced_by,command_id`)。

Replay 只返回 receipt 保存的旧 result，不重构当前 Node。所有 projection 不含
`reader_secret_ref`、Management Key、credential、OAuth、raw response 或 endpoint 之外的
敏感资料；endpoint 是既有公开 asset metadata，仍只在 approved API/display projection 中
出现，audit/metric 不记录它。

## 4. Monitoring cancellation persistence

既有 activation row 增加：

```text
cancelled_at timestamptz NULL
cancelled_by uuid NULL REFERENCES control_admin_users(admin_id)
cancel_reason text NULL
```

`cancelled_by` 为 `ON UPDATE RESTRICT ON DELETE RESTRICT`。三列必须全 NULL 或全 non-NULL；
本 change 第一版将 `cancel_reason` 固定为仅 `node_retired|node_replaced`
两个值，`cancelled_by` 为 `control_admin_users.admin_id` UUID FK。既有更精确的 current-close
`end_reason` disable taxonomy（`deployment_disable|scheduled_disable|reconciliation`，使用独立
text `end_actor` 列）是一套不同的语义家族，不得并入 `cancel_reason`——这些既有 reason 的 actor
是 system/deployment 语义，与 `cancelled_by` 的 admin UUID FK 不一致，本轮不强行统一。current
close 的既有 `end_reason` allowlist 单独 additive 扩展为
`deployment_disable|scheduled_disable|reconciliation|node_retired|node_replaced`；`end_actor`
仍是 baseline text 列，Node lifecycle 写入认证后的 canonical `actor_admin_id` UUID string，不
借机重构整个 actor schema。未来 `add-relay-node-management-operations` 的 administrator
Disable 若需要新增 `cancel_reason` 值（例如 `administrator_disable`），必须由该 change 做独立
delta 并复用同一 cancellation columns，本 change 不预先加入。

Architecture Review D6 的 generated expression 冻结为：

```sql
CASE
  WHEN cancelled_at IS NULL
    THEN tstzrange(effective_from, effective_to, '[)')
  ELSE 'empty'::tstzrange
END
```

仍使用 `active_range` 的 GiST exclusion。cancelled future row 保留原
`effective_from/effective_to/reason/actor/created_at`，不 DELETE、不 shift、不 rewrite；
其 range 精确为空，不 overlap、不 eligible、不 current、不产生 expected slot。`cancelled_at`
必须 `< effective_from`，所以只允许 future row cancellation。已开始 current interval
不能用 cancellation metadata：在 boundary 用 `effective_to=boundary`、既有 end reason/
actor metadata 关闭，且不得回写已经结束的 UTC 历史。`effective_to=effective_from` 的
刚开始竞争由 Node lock、monitoring row lock 和 DB time 处理；若相等，返回稳定 boundary
conflict，不写非法 zero-length range。

DB guard 必须允许 future row 从全 NULL 到全 non-NULL 一次，禁止 uncancel、改变 cancellation
actor/reason/time、改变原 schedule fields、cancel current/past row、DELETE/TRUNCATE；既有
current close 唯一合法 UPDATE 保持可用。history/read model 明确输出 `cancelled`，不会把
cancelled schedule 当缺失。Future Monitoring Disable（属于 operations change）必须复用这一
schema，不创建第二套 cancellation persistence。

## 5. Retire transaction（冻结顺序）

认证/session/super_admin/CSRF 在事务外。事务内精确顺序：

1. `pg_advisory_xact_lock`（完整 command_id 派生）。
2. actor-first receipt lookup；同 actor/intent replay，否则 `command_conflict`。
3. `relay_node_assets` target `FOR UPDATE`。
4. 校验 target 存在、active、`expected_revision` 相等、revision 未溢出和
   `retire_reason=administrator_retire`。
5. 只有必要 Node lock 已持有后建立 lifecycle DB boundary（一次
   `clock_timestamp()`）。
6. 按 `effective_from ASC, monitoring_activation_id ASC` 锁 current monitoring row 和
   所有 future monitoring rows；current 用 boundary close，future 逐行 durable cancel。
7. 锁 target current binding（Node partial unique，最多一行）；以同 boundary
   `end_reason=node_retired` close；不再拿 Gateway 或 Directory lock。
8. 按 8.3 节冻结的处置规则处理该 Node 尚无 transport evidence 或未持有效 authorization 的
   `account_inventory_poll_runs`：`pending`/`retry_wait` 与无有效 current-attempt
   authorization 的 `running` 立即 abandoned；持有效 current-attempt authorization 的
   `running` 不在本步骤 abandon，允许其完成 transport 后走 finalize+promotion-skip 路径
   （generic `async_jobs` 不受影响）。
9. Node `active→retired`、`revision+1`、写 `retired_at/by/reason`。
10. 写一条 bounded mutation audit。
11. 写 immutable receipt。
12. commit。

实现前必须用现有 Store/queries 和 lock graph integration tests 验证无 inversion；Node
lifecycle 在 binding lock 后禁止反向拿 Gateway/Directory lock。所有 counts 与 boundary
来自数据库，Go 不使用 `time.Now()`。

## 6. Replace transaction（冻结顺序）

认证/session/super_admin/CSRF、body validation 在事务外。事务内：

1. command advisory lock；
2. actor-first receipt lookup；
3. lock old Node row；
4. 校验 old active、expected revision、revision 未溢出；
5. 校验 new identity 在 `relay_node_assets` 全历史中不存在，且 old≠new；
6. 校验 new metadata、driver contract、capability declaration、endpoint/secret；
7. 按稳定顺序锁 old Node 的 current/future monitoring rows；
8. 锁 old current binding（不获取 Gateway/Directory lock）；
9. 锁并检查 old Node 的 Inventory current-state 相关 rows 与其
   `account_inventory_poll_runs`（不涉及 generic `async_jobs`）；
10. 建立单一 replacement boundary；
11. close current monitoring、cancel future monitoring；
12. close binding `end_reason=node_replaced`；
13. 按 8.3 节规则处置 old Node 的 `account_inventory_poll_runs`：无有效 authorization 的
    run 立即 abandoned；持有效 current-attempt authorization 的 `running` run 不在本步骤
    abandon（不涉及 generic `async_jobs`）；
14. old `retired`, `revision+1`, `reason=replacement`；
15. insert new active Node `revision=1`，capabilities 作为新 registration 写入；
16. insert immutable lineage；
17. 写一条 replace audit、receipt；
18. commit。

old/new lifecycle、lineage、monitoring current `effective_to`、future `cancelled_at`、binding
`ended_at`、lineage `replaced_at` 均优先使用同一 boundary。poll run evidence 不能覆盖既有
`started_at`/transport observed time：`account_inventory_poll_runs` schema 要求这些是执行事实，
abandonment 只在同一 transaction 对无有效 current-attempt authorization 的 run 写
`abandoned_at` 为 transition DB time，`execution_reason=node_retired|node_replaced`；持有效
current-attempt authorization 的 `running` run 不在本 transaction abandon，其后续处置见
8.1/8.3 节；已有 transport evidence 的 run 走既有 finalize 路径（见 8.3 节）。

## 7. Lock graph 与 writer contract

冻结图：

```text
Bind/Rebind                 Node -> Gateway -> Gateway Directory current -> binding
Monitoring writer           Node -> monitoring rows (effective_from, activation_id)
Node Retire/Replace         Node -> monitoring rows (stable order) -> binding
Inventory promotion         Node -> inventory/current-state rows (stable identity order)
Gateway Retire/Replace      Gateway -> current binding rows
```

全图不得出现 `binding -> Node` 或 `binding -> Gateway`。特别禁止 Node lifecycle 在已持
binding lock 后再拿 Gateway/Directory lock，避免与 Bind/Rebind 的 Node→Gateway→binding
形成 inversion。所有 monitoring writer（现有 writer、future operations、lifecycle）
必须先 `Node FOR UPDATE`，再按 `effective_from, monitoring_activation_id`（或真实 PK）
锁 monitoring rows；禁止 monitoring→Node 反向路径。

## 8. Inventory、availability、request-quality fences

### 8.1 Scheduler/claim/preflight/outbound

- Scheduler eligibility 必须同时为 Node `lifecycle_status=active`、在 DB time 命中的
  non-cancelled monitoring activation、`management_account_inventory_read` capability
  和匹配 policy；retired Node 不创建新 poll。
- claim 短事务重新读取并锁 Node/必要 activation，验证 active + eligible；否则将既有
  pending/retry_wait run 置为 durable `abandoned`（固定 reason），不留可重新 claim
  的 row。
- preflight 同样重新读取 active/monitoring；claim→Retire race 只能产生 abandoned/terminal，
  不把进程内 error 当结果。
- network fetch 前必须在短事务内建立 **dispatch authorization**：这不是 claim/lease，而是
  `account_inventory_poll_runs` 上冻结的三个持久列——`dispatch_authorized_attempt integer NULL`
  （MUST 等于当前 `attempt_count`）、`dispatch_authorized_at timestamptz NULL`（数据库转换
  时间）、`dispatch_authorized_fencing_token uuid NULL`（MUST 等于当前 `lease_fencing_token`）。
  三列必须同为 NULL 或同为非 NULL，且非 NULL 当且仅当 `status='running'` 且与当前
  `attempt_count`/`lease_fencing_token` 一致；`pending`/`retry_wait`/`finalized`/`abandoned`
  时三列 MUST 全为 NULL。授权事务读取单一 `database_now` 后必须同时满足：`status='running'`、
  三列为 NULL、`attempt_count` 等于当前值、`lease_fencing_token` 等于当前值、
  `lease_expires_at > database_now`、`scheduled_at + make_interval(secs =>
  poll_start_grace_seconds) > database_now`、Node `lifecycle_status=active`、当前 monitoring
  eligibility 成立；任一不满足则不写入任何授权列、不发出 outbound。全部满足后写入这三列绑定到
  当前 `attempt_count`（同一 attempt 内只能写入一次），并计算返回 `lease_remaining`
  （`lease_expires_at - database_now`）与 `grace_remaining`（`scheduled_at +
  poll_start_grace_seconds - database_now`），再释放 Node lock 后才发起 transport；claim 本身
  不是 authorization。Worker 的 outbound transport deadline 不得晚于 `min(lease_expires_at,
  scheduled_at + poll_start_grace_seconds, database_now + T)`，其中 `T` 为
  `account-inventory-poll-capacity` 既有最坏请求时长配置，不引入第二套 timeout 配置或新的
  durable deadline 列。`running → retry_wait`、`running → finalized`、`running → abandoned`
  这三种转换 MUST 各自在同一 UPDATE 原子清空这三列；下一次 `retry_wait → running`（新
  `attempt_count`、新 `lease_fencing_token`）必须重新独立获得
  authorization，不得复用旧 attempt 的授权列判定为已授权。race 由该 authorization 写入的事务
  提交顺序决定，不是 wall-clock socket 发出时刻——Retire 先提交则 authorization 写入失败、不
  发 outbound；authorization 先提交，则该 bounded attempt 可以完成 transport 即便 Retire 随后
  提交，但 finalize/promotion 仍必须再次 fence 并拒绝 current truth。不得跨 HTTP 持有 Node DB
  lock。restart/reconciler 通过三列是否非 NULL 且等于当前 `attempt_count`/`lease_fencing_token`
  区分已授权与未授权 attempt，再决定按有界 transport 恢复还是终止；Node Retire/Replace 提交时
  若某 run 的 `running` 持有效 current-attempt authorization，lifecycle transaction 不在本步骤
  abandon 它——Node 正常变为 retired/replaced，该 authorized attempt 仍 MAY 完成 transport，
  此后不得为其授予新 retry/attempt/authorization；若该 authorized attempt 崩溃或 lease 到期且
  未产生 evidence，reconciler 必须以 `node_retired`/`node_replaced` 将其 abandoned，不得复活
  outbound。
- reconciler 处理一个 lease 已过期的 `running` run 时，MUST 先重新读取该 run 所属 Node 的当前
  lifecycle 状态再决定处置：Node 仍 active 时保留既有 baseline 行为（grace 未尽且
  attempt 未耗尽 → `retry_wait`，否则 → `abandoned`）；Node 已 `retired` → `abandoned`
  且 `execution_reason=node_retired`；Node 是已 Replace 的 old identity → `abandoned` 且
  `execution_reason=node_replaced`。`retired`/`replaced` 分支不得进入 `retry_wait`、不得递增
  `attempt_count`、不得获得新 lease/authorization、不得发起新 outbound；该 terminal transition
  须在同一事务原子清空 `lease_expires_at`/`lease_fencing_token`/三个 dispatch authorization
  列，并设置 `abandoned_at`/`execution_reason`。
- finalize/promotion 短事务锁 Node，验证 active、同 identity、monitoring current/
  eligibility 和 poll fencing token/run identity。若 Node 已 retired/replaced，run 级与
  Provider 级 `promotion_skipped_reason` 均须设为对应的 `node_retired`/`node_replaced`
  （precedence：Node lifecycle fence 先于 `policy_changed`，`policy_changed` 先于既有
  provider-specific evaluation），保留 transport evidence 但不写 snapshot/current
  pointer/account lifecycle/availability/request-quality current。
- `control_refresh_account_inventory_provider_health_v1()`（以及任何等价的
  `account_inventory_provider_states` current-health consumer）把
  `promotion_skipped_reason IN (policy_changed, stale_poll, node_retired, node_replaced)`
  一视同仁地排除在 health 刷新之外：命中该 allowlist 时 MUST NOT 更新
  `health_scheduled_at`/`health_degraded`/`health_reason`，也不得间接推进
  availability/request-quality current truth；`transport_failed`/`contract_invalid`/
  `disk_fallback`/`provider_identity_incomplete`/`provider_duplicate` 等既有
  provider-specific 原因不受影响，按既有语义继续刷新 health；transport/provider
  evidence 与 history/compaction 证据在两种情况下都完整保留，不删除、不重解释。
- replaced old Node 不 retarget 到 new identity；new Node 只从其自身新 poll 建立 truth。

### 8.2 Current operational reads

Inventory current、availability、request-quality derived target 需要显式 join
`relay_node_assets.lifecycle_status='active'` 加 monitoring/current eligibility，不能只
依赖 monitoring 查不到。retired/replaced history、poll evidence、snapshot、rollup、
compaction 保留；cancelled future activation 的 empty range 不生成 expected slot。
历史 current interval、poll evidence、history summary/rollup 不因 Retire retroactively delete。

### 8.3 Durable job abandonment scope

已实际扫描当前 durable job catalog：唯一生产 registered `async_job_kind` 是
`dingtalk_alert_delivery`（`migrations/00031`），其 payload 未声明任何
`NodeOwned=true`/stable node identity 字段，`instance_ids` 只是通知内容的一部分，不代表某个
Node-owned execution。因此本 change **不修改** generic `async_jobs`/`durable-job` capability
的 execution semantics：不要求每个 Worker recheck 一个 active Node，不要求 pending/running
job 因任意 Node retirement failed，也不因为某条 DingTalk payload 里的 Node 已 retired 就取消
已经合法产生的通知。`durable-job` capability 保持不属于本 change 的 Modified Capabilities。

真正的 Node-owned durable work 是 `account_inventory_poll_runs`（`account-inventory-poll-run`
capability，见该 capability 的 spec delta）。Node fence 触发时按 run 状态区分处置：
`pending`/`retry_wait`，以及没有有效 current-attempt authorization（三个 authorization 列
NULL 或与当前 `attempt_count`/`lease_fencing_token` 不一致）的 `running`，立即使用既有
`abandoned` 状态，固定 `execution_reason=node_retired|node_replaced`，`abandoned_at`=数据库
转换时间，不 DELETE；持有效 current-attempt authorization 的 `running` 不在本步骤 abandon——
Node 仍正常变为 retired/replaced，该 authorized attempt MAY 完成 transport，之后不再授予新
retry/attempt/authorization，若其崩溃或 lease 到期且未产生 evidence，reconciler 必须以
`node_retired|node_replaced` 将其 abandoned，不复活 outbound。若该 poll run 已经产生真实
transport evidence，则不得伪造为 `abandoned`（既有数据库约束已经拒绝 abandoned-with-evidence
组合），必须走既有 finalize 路径，由 8.1 的 promotion fence 跳过 snapshot/current truth，同时
保留 transport evidence。

若未来发现某个新增 job kind 确实需要声明 `NodeOwned=true` 及稳定 node identity 字段，
应由该 job kind 自己的 change 增加显式 fence contract，而不是由本 change 扩大
`durable-job` capability 的通用语义。

## 9. Binding closure

扩展既有 `end_reason` CHECK 为：
`administrator_unbind|administrator_rebind|gateway_retired|gateway_replaced|node_retired|node_replaced`。
Node Retire/Replace 关闭当前 binding、保留 history，不自动 rebind/account migration；
new replacement Node 零 current binding。Gateway reasons 与 Node reasons 共存，分别由
对应 lifecycle transaction 写入。binding closure 与 Node mutation、audit、receipt 同事务。

## 10. HTTP/read contract

### 10.1 Mutation routes

固定为：

| Action | Method/path | body | success |
|---|---|---|---|
| Register | `POST /api/assets/nodes` | `command_id,new_instance_id,display_name,management_endpoint,node_type,driver_contract_version,capabilities,reader_secret_ref?` | `201` |
| Edit | `PATCH /api/assets/nodes/{instance_id}` | `command_id,expected_revision` + explicit mutable patch presence | `200` |
| Retire | `POST /api/assets/nodes/{instance_id}/retire` | `command_id,expected_revision` | `200` |
| Replace | `POST /api/assets/nodes/{instance_id}/replace` | `command_id,expected_revision,new_instance_id` + complete new registration fields | `200` |

`command_id` 是 UUID；`expected_revision` 和 response revision 是规范正 bigint 的 decimal
string；Node no DELETE。Retire reason 由 server 固定，Replace reason 由 server 固定。
不在本 change 冻结 Connection Test、Monitoring Enable/Disable。

成功 response body 也固定为完整 Node 读模型投影，不只返回变化字段：

```json
{
  "result": "registered|updated|retired|replaced",
  "asset": {
    "instance_id": "uuid",
    "lifecycle_status": "active|retired",
    "revision": "1",
    "display_name": "display-safe",
    "node_type": "existing node_type value",
    "driver_contract_version": "existing driver_contract_version value",
    "management_endpoint": "https://node.example.invalid",
    "capabilities": ["sorted capability list"],
    "secret_configured": false,
    "monitoring": {
      "current": false,
      "monitoring_active": false,
      "effective_from": null,
      "effective_to": null
    },
    "created_at": "UTC timestamp",
    "updated_at": "UTC timestamp",
    "retired_at": null,
    "retired_by": null,
    "retire_reason": null
  },
  "closed_binding_count": 0,
  "closed_monitoring_count": 0,
  "cancelled_future_monitoring_count": 0
}
```

对 retired asset，`monitoring.current` 必须显式为 `false`（historical projection），不省略该
字段。Register/Edit 返回单个 asset；Retire 返回 retired asset；Replace 返回
`old_asset`、`new_asset`（均使用同一完整投影）、counts 和
`lineage:{old_instance_id,new_instance_id,replaced_at,replaced_by,command_id}`。
Register=201，其余 lifecycle mutation=200；replay 返回 receipt 原 status/body。

错误 taxonomy：`400 validation_failed`、`400 invalid_endpoint`、
`400 secret_configuration_invalid`、`404 asset_not_found`、`409 asset_retired`、
`409 duplicate_identity`、`409 stale_revision`、
`409 revision_exhausted`、`409 command_conflict`、`409 cursor_stale`（list read 专用）、
`503 service_unavailable`。Node 没有
Gateway 式 singleton current-identity invariant，不保留 `current_identity_conflict`（无实际
触发条件、无消费者）；identity 冲突统一使用 `duplicate_identity`。不透传 raw PostgreSQL error。

### 10.2 Current/history reads

既有 `GET /api/assets/nodes` 与 `GET /api/assets/nodes/{instance_id}` 演进为 lifecycle-aware，
不另造 collection。list query `lifecycle=active|retired|all` 默认 active，stable keyset
pagination 的 filter/sort/cursor 绑定；sort key 为 `instance_id ASC`。

cursor generation 冻结为显式物理表 `asset_registry_generations`（单例，
`singleton_id smallint PRIMARY KEY DEFAULT 1 CHECK (singleton_id = 1)`，
`node_generation bigint NOT NULL DEFAULT 0`）——不使用任何进程内存计数器。锁顺序：
Node Register/Edit/Retire/Replace 与影响 default collection membership/projection 的
monitoring current-state 变化 MUST 在同一个 domain 事务内、完成 Node/monitoring row lock
之后，再以 `SELECT ... FOR UPDATE` 锁定该单例行并 `node_generation = node_generation + 1`，
随事务一起提交；数值只能单调递增，overflow（达到 bigint 上限）fail closed 而不是回绕或降低。
List 首页读取 MUST 在同一个只读事务/一致 DB snapshot 内先读 `node_generation`，再读该事务的
`read_as_of`（数据库转换时间戳，例如 `pg_catalog.transaction_timestamp()`），再读 Node rows，
把两者一起编入 cursor；cursor opaque payload 至少绑定 `encoding_version、environment、
全部生效 filter（lifecycle/node_type/capability/monitoring）、last_instance_id、
node_registry_generation、read_as_of`。请求携带的 cursor 若其 generation 与当前
`node_generation` 不一致，返回 `409 cursor_stale`，客户端须放弃该 cursor 重新从第一页开始；
不得只用 `sum(node.revision)` 近似 generation，因为 monitoring filter 可以在不改变任何 Node
revision 的情况下改变 collection membership。cursor chain 内后续页对任何 monitoring-derived
projection/filter（`monitoring_active`、current interval membership等）MUST 按该 cursor 冻结
的 `read_as_of` 计算，不得每页重新读取 DB 当前时间；这样纯 wall-clock 流逝跨越某个
`effective_from`/`effective_to` boundary 不会改变已发 cursor chain 内的 membership，只有真正的
durable mutation（`node_generation` 前进）才触发 `409 cursor_stale`。detail 可见 retired、
predecessor/successor。

legacy `nodes` count 继续表示 total；新增 `node_counts:{active,retired,total}`，operational
UI 使用 `node_counts.active`。read projection 只返回 display-safe metadata、revision、
lifecycle、retirement metadata、secret_configured、capability、monitoring derived state；
retired 默认不进入 operational list。

### 10.3 Node lifecycle UI ownership

本 change 拥有 Node lifecycle 的 UI/Runbook 规划：Register、Edit、Retire 确认、Replace
流程、`expected_revision`/stale conflict 刷新提示、retired 历史详情与
predecessor/successor 导航、以及仅提示 `secret_configured` 而不回显 Secret 引用的输入控件。
这些控件只调用本 change 冻结的四个 lifecycle mutation 路径。UI 规划 MUST NOT 包含 Health、
Connection Test、Monitoring Enable 或 Monitoring Disable 控件——这些仍属于
`add-relay-node-management-operations`，本 change 只需在 design 中说明其对本 change schema/
lock order/receipt/compatibility class 的 future 依赖。

## 11. Audit、metrics 与 security

Audit 复用 bounded `audit_logs`：

- category=`asset_node`；
- actions=`node.register|node.edit|node.retire|node.replace`；
- details allowlist：`command_id,instance_id,old_instance_id,new_instance_id,reason_code,
  old_revision,new_revision,closed_binding_count,closed_monitoring_count,
  cancelled_future_monitoring_count`；
- 禁止 secret ref/fingerprint、credential、endpoint、actor login/display、raw error/body；
- 每个成功 transition 恰好一条 audit；replay 无第二条；accepted no-op 不写 transition
  audit。

Metrics 复用 `control_asset_mutation_total{asset_type,action,result}`，Node 的
`asset_type=node`、action=`register|edit|retire|replace`、result=
`success|replay|noop|conflict|invalid|unavailable`。不把 Node ID、command_id、endpoint、
actor、reason text 或 cancellation count 当 label；数量可作为 counter/histogram value。

Mutation 要求 active session、super_admin、CSRF、no-store；unauthorized/CSRF 失败在
receipt 可见性之前拒绝。Node lifecycle 不保存/返回 Management Key、credential/OAuth、
raw CLIProxyAPI response、credential-bearing header；不新增 arbitrary HTTP client 或
account mutation。

## 12. Compatibility class 2 rollout/rollback

Class 0：pre-Phase6；class 1：Gateway lifecycle-aware；class 2：Node lifecycle、
monitoring cancellation、replacement lineage、new binding reasons 和 Inventory fences aware。
由于 class 1 binary 会继续 poll retired Node、把 empty-range cancellation 当 eligible、
重新 claim old job，必须 `compatibility_class=2`，`phase6_evidence_floor/runtime floor=2`。
这一决策现已作为规范性 Requirement 冻结在
`relay-node-asset-lifecycle` capability 的“Node lifecycle SHALL raise compatibility floor
atomically before schema exposure”，不只停留在本 design/planning-validation 描述层面。

不允许出现"class1-incompatible schema 已提交但 floor 仍为 1"的中间态。固定原子生产顺序：

1. class 2 signed artifact/manifest 就绪（真实 digest 在 implementation evidence 记录，不预
   编造）。
2. external gate/trust-root wrapper 已部署并 active。
3. 停止正在运行的 class 1 Control process 与 workers。
4. 禁用自动 restart/start path。
5. 开始一个 PostgreSQL migration transaction。
6. 在该 transaction 中应用第一批 class1-incompatible 的 Node schema 变更。
7. 在同一 transaction 中把 compatibility floor 1→2 原子提升。
8. 提交该 transaction。
9. 重新启用受支持的 startup path。
10. wrapper 验证运行中 artifact 的 class ≥ floor 2。
11. 启动 Control。
12. 只有在步骤 11 通过后才开放 Node lifecycle mutations。

对 class 1 runtime 确实安全的 additive column 可以放在更早的 compatibility window migration
中先落地；但 active_range cancellation semantics、retired Node durable semantics、新 binding
end reasons，以及任何 class 1 runtime 可能误读的 evidence，必须与 floor 2 用同一个原子
barrier 建立，不允许分离成两次独立 release。

floor 2 后 rollback 到 class 1 必须通过同一 mandatory wrapper fail closed，不删除
evidence、不降低 floor。验收使用 pinned Stage 1/Gateway-aware class 1 artifact，但不伪造
planning commit `14c0e80` 为未来 binary digest。

## 13. Migration strategy 与 PG18 proof

Additive-first sequence：

1. 增加 Node lifecycle columns，backfill existing rows `active/revision=1`，先做 NOT VALID
   checks，再 validate；actor FK 与 reason CHECK 在全量满足后启用。
2. 新建 lineage table、immutable guard、index/FK。
3. 增加 monitoring cancellation columns、FK/reason/shape guard。
4. 在 schema compatibility window 内暂停 monitoring writers，锁定 dependent GiST
   exclusion，按“drop dependent exclusion（如 PG18 要求）→修改 generated expression
   →recreate exclusion→validate”演进 `active_range`；保留所有 activation rows。
5. 扩展 binding end reason CHECK 为
   `administrator_unbind|administrator_rebind|gateway_retired|gateway_replaced|node_retired|node_replaced`。
6. 在 `account_inventory_poll_runs` 增加 dispatch authorization 列：
   `dispatch_authorized_attempt integer NULL`、`dispatch_authorized_at timestamptz NULL`、
   `dispatch_authorized_fencing_token uuid NULL`；增加 CHECK/触发器 guard，冻结不变量：三列
   MUST 同时为 NULL 或同时非 NULL（不允许部分写入），且非 NULL **当且仅当**
   `status='running' AND dispatch_authorized_attempt=attempt_count AND
   dispatch_authorized_fencing_token=lease_fencing_token`；`pending`、`retry_wait`、
   `finalized`、`abandoned` 这四种状态下三列 MUST 全为 NULL；同一 `attempt_count` 内只允许一次
   `NULL -> authorized` 写入（禁止覆盖/二次写入）；`running -> retry_wait`、
   `running -> finalized`、`running -> abandoned` 这三种转换 MUST 各自在同一 UPDATE 原子将三列
   清空为 NULL；`execution_reason` allowlist 增加 `node_retired|node_replaced`。
6a. 扩展既有两层 promotion-skip allowlist（不新增字段）：
    `account_inventory_poll_runs_promotion_reason_fixed` CHECK 由仅允许
    `NULL|policy_changed` additive 扩展为 `NULL|policy_changed|node_retired|node_replaced`；
    `account_inventory_poll_provider_promotion_reason_fixed` CHECK 由
    `policy_changed|transport_failed|contract_invalid|disk_fallback|
    provider_identity_incomplete|provider_duplicate|stale_poll` additive 扩展为同一集合
    加 `node_retired|node_replaced`；既有 `account_inventory_poll_runs_promotion_terminal`
    (`promotion_skipped_reason IS NULL OR status = 'finalized'`) 与
    `account_inventory_poll_provider_promotion_shape`
    (`NOT promotion_applied OR promotion_skipped_reason IS NULL`) guard 不变。
6b. 同一 migration 内更新既有触发器函数
    `control_refresh_account_inventory_provider_health_v1()`（不改变其签名/挂载点）：
    在原有 `promotion_skipped_reason IS DISTINCT FROM 'policy_changed'`（run 级）与
    `promotion_skipped_reason IS DISTINCT FROM 'policy_changed' AND ... 'stale_poll'`
    （provider 级）判断中additive 加入 `node_retired`/`node_replaced` 排除；不新增列、
    不新增触发器、不改变函数 owner/SECURITY DEFINER/search_path。
7. 增加 `asset_registry_generations` 单例表：`singleton_id smallint PRIMARY KEY DEFAULT 1
   CHECK (singleton_id = 1)`、`node_generation bigint NOT NULL DEFAULT 0 CHECK
   (node_generation >= 0)`；Node Register/Edit/Retire/Replace 与影响 default collection
   membership/projection 的 monitoring current-state 变更 MUST 在同一事务内以
   `UPDATE ... SET node_generation = node_generation + 1` 递增，overflow（超过 bigint 上限）
   fail closed，不允许该值下降。
8. 增加 inventory/job fence query/function 和 fixed error code。
9. 同 class 2 release 更新 signed manifest/wrapper，并与 floor 2 release 同步。

implementation 必须在 PostgreSQL 18 实测 generated stored column 是否允许直接
`ALTER COLUMN ... SET EXPRESSION`；若不允许，必须在同一 migration transaction 中 drop
dependent exclusion、drop/recreate generated column、recreate exclusion，并证明旧 rows、
indexes、FK 和 rollback failure 不丢失。必须实测 `'empty'::tstzrange`、GiST overlap、
current/future eligibility、expected-slot exclusion 和 cancellation guard。生产不依赖
destructive down；中间旧 runtime 只有在其不读取新语义的 migration compatibility window
内运行，并在 floor 提升前停止。

## 14. Race/crash/security acceptance matrix

implementation tests 必须覆盖 concurrent Register same identity、Retire、Replace、
Retire-vs-Replace、Edit-vs-Retire、binding-vs-Retire/Replace、monitor writer-vs-Retire/
Replace、Inventory claim/outbound/promotion-vs-Retire、old worker after Replace、
future cancellation reaching `effective_from`、restart after cancellation、class1 rollback
against floor2；以及 Retire/Replace commit 前 crash 无 partial lifecycle/binding/monitoring/
lineage/receipt/audit、commit 后 response loss replay 原 body、old work restart 不复活。

所有验证均保持 DB-only Control truth 和 data-plane isolation；本 change 不实现 operations
change 的 product API/UI。
