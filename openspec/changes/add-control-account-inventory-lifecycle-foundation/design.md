## Context

阶段 2 已经把字段白名单化的 `AccountObservation` 投影为不可变 snapshot items，并用 Provider 独立、策略 binding 加锁、poll lease/fencing 保护的 finalize 原子提升当前来源。当前系统可以证明“某 Provider 在某完整 runtime 观察里有哪些账号”，但还没有唯一的当前账号行，也不能区分首次完整缺失、连续缺失、恢复和退出监控范围。

生命周期必须建立在 promotion 事实而不是 poll 成功、Node/Gateway 可用性或历史快照推断之上。完整空集合是有效证据；失败、disk fallback、身份不完整、策略变化或迟到槽则不是。Provider 范围变化来自受审计策略 activation，并与 finalize 竞争同一 binding 串行化点。

当前部署仍是单 Control、单 PostgreSQL 真相源。环境单例是隔离边界，账号唯一键保持 `(instance_id, account_key)`。本 change 不向产品 API/UI 暴露 email，也不增加任何 Node、Gateway 或模型数据面请求。

## Goals / Non-Goals

**Goals:**

- 建立受约束、可恢复的 `account_inventory` 当前 lifecycle 真相。
- 只用 `promotion_applied=true` 的完整 runtime Provider 快照推进 present、suspected_missing、missing 和恢复。
- 将 snapshot promotion、Provider pointer、lifecycle 转换和 poll finalized 保持为同一 fenced 事务。
- 在受审计策略切换中原子处理 Provider/账号 out-of-scope，并定义重新加入语义。
- 保证旧槽、重复 finalize、提交未知、策略竞态和数据库故障下不重复累计或倒退。
- 提供后续只读 API/页面、告警和历史汇总可消费的有界内部 Store 边界。
- 保持 email/account key 只在受保护列和授权内部返回值中出现。

**Non-Goals:**

- 不新增或修改产品 OpenAPI、生成客户端、React 页面、导出或人工状态编辑。
- 不发送告警，不计算 10 分钟/1 小时趋势、日级覆盖率、摘要或历史压缩。
- 不删除账号，不实现保留清理、跨 Node 重复检测或 HMAC `account_id` 指标。
- 不改变 Driver HTTP 契约、poll 调度/重试次数、Gateway、Node 或模型数据面。
- 不从 Migration 前历史 snapshot 猜测当前账号或 missing。

## Decisions

### 1. 当前生命周期使用单独的受保护表

新增 `account_inventory`，唯一键 `(instance_id, account_key)`。字段分为四组：

- 身份与当前状态：标准化 provider/email、最近完整 `basic_status` 和现有字段白名单计数/源时间。
- 生命周期：`present|suspected_missing|missing|out_of_scope`、`consecutive_missing_count`、`missing_since`、`out_of_scope_since`。
- 时间：数据库生成的 `first_seen_at`、`last_seen_at`、`updated_at`。
- 来源：可空 `current_poll_run_id` 以及复制的 scheduled/observed/node version/commit；poll 外键 `ON DELETE SET NULL`。

约束固定状态组合：present 的缺失计数为零且两个 since 为空；suspected_missing 的计数为 1 且 missing_since 为空；missing 的计数为 2且 missing_since 非空；out_of_scope 的计数为零、missing_since 为空且 out_of_scope_since 非空。计数饱和在 2，避免无界增长。`first_seen_at` 永不改写，`last_seen_at` 只表示实际出现，不表示缺失观察。

备选方案是每次从两个最新 snapshot 动态计算状态。这无法稳定表达 out-of-scope、提交未知恢复或历史清理后的当前含义，也会让每个消费者重新实现规则，因此拒绝。

### 2. promotion 是唯一缺失证据

新的 lifecycle-aware finalize 继续使用当前 snapshot candidates，不重新查询 Node、不读取原始响应，也不从 Provider pointer 之外另起任务。只有该 Provider 实际满足 `promotion_applied=true` 时才执行：

1. 本轮 item 按 account key upsert：新账号创建 present；既有 active 状态恢复/保持 present，刷新白名单状态、last seen 与来源，清零缺失。
2. 同 instance/provider 中本轮未出现的 present 行变为 suspected_missing/1。
3. 本轮仍未出现的 suspected_missing 行变为 missing/2，`missing_since` 使用本次由 PostgreSQL 生成的 observed time。
4. 已 missing 且仍未出现的行保持 2 和原 missing_since。
5. out_of_scope 行只有本轮实际出现才恢复 present；未出现时保持 out_of_scope，不参与缺失累计。

同一个 observed time 用于 snapshot、Provider state 和本轮全部 lifecycle 转换。源时间只更新本轮实际出现账号；缺失账号保留最后出现来源。完整空集合执行第 2至 4 步，因此是有效缺失证据。

备选方案是把第一次缺失时间保存为 missing_since，但 suspected_missing 尚不构成 missing。这里选择第二次完整缺失转换时记录，使字段名与确证状态一致；首次缺失仍可由最新 transition/计数观察，不在本 foundation 建历史事件表。

### 3. 生命周期加入现有事务和锁顺序

新 finalize 的锁顺序固定为：poll run并验证 running/lease/fencing；policy binding；对应 Provider state；该 instance/provider lifecycle 行按 account key 排序。随后在一个事务内验证输入、写 snapshot/duplicate、推进 Provider pointer、转换 lifecycle、写 promotion 结果并 finalized。

Provider 之间按 pinned policy 的规范排序处理，账号按 account key 排序，降低多个迟到槽或策略事务死锁概率。Provider pointer 的 scheduled_at 单调检查先于 lifecycle 更新；一旦判定 `stale_poll`，该 Provider 完全不改 lifecycle。任一 SQL、约束或提交失败整体回滚。

同一 poll 已 finalized 时返回既有终态，不再次执行转换；旧 fencing 或过期 lease 影响零行。COMMIT 结果未知沿用原 poll、pinned policy 和现有 attempt/grace 恢复：已提交行由终态挡住，未提交事务可以整体重做。因此 missing 只按成功 promotion 计一次。

不采用 finalize 后的 job/outbox，因为那会制造 snapshot 已提升但 lifecycle 未推进的可见窗口，还需要第二套幂等与恢复协议。

### 4. 新增 lifecycle-aware 函数，保留旧应用回滚边界

Migration 保留当前 snapshot-only finalize 函数，并新增显式 lifecycle-aware 受控函数；新 sqlc/Store 只调用新函数。两者都继续由数据库验证完整输入、权限和 fencing，但只有新函数能推进 `account_inventory`。这允许应用紧急回滚时关闭 poll/Provider 策略写入后运行旧二进制读取既有产品功能，已提交生命周期保持不变且不会被旧代码重算。

同理，新增 lifecycle-aware Provider policy activation 函数供新管理路径调用；它复用现有 policy validation、实名 actor、reason、activation history 与 binding 行锁，同时执行 out-of-scope 转换。旧 activation 函数在新版本运行时从应用角色撤销执行权，仅为受保护 down/旧二进制回滚预留；回滚期间必须禁用 Provider policy mutation，避免策略已变化而 lifecycle 未同步。

备选方案是就地替换现有同签名函数。那会让旧二进制回滚后仍隐式推进 lifecycle，且旧策略写路径可能绕过 out-of-scope 转换，不符合可操作回滚边界。

### 5. Provider monitoring status 来自策略事务

扩展 `account_inventory_provider_states`，增加封闭 `monitoring_status=active|out_of_scope` 和可空 `out_of_scope_since`。Migration 对既有 state 设置 active，不创建账号 lifecycle 行；这只保存当前监控范围的兼容默认，不从历史观察猜账号状态。

新策略 activation 在锁定 binding 并建立生效版本时比较旧/新集合：

- active 移入登记 out-of-scope：Provider state 与全部现有该 Provider 账号原子变为 out_of_scope，使用同一数据库时间，清空 missing。
- out-of-scope 重新 active：只把 Provider state 改 active；账号仍 out_of_scope。
- 重新 active 后，合格 promotion 中实际出现的旧账号恢复 present；未出现账号保持 out_of_scope。新出现账号直接建立 present。

未来预约 activation 必须在其实际生效事务/执行点应用范围转换，不能在预约创建时提前改变账号。若当前资产实现把 binding 指向未来版本，lifecycle-aware activation 必须保持 activation time 与状态切换一致；无法原子安排未来执行时，本 change 对生命周期相关切换 fail closed，仅允许立即生效。具体实现与验收必须证明不会出现策略 effective range 与 Provider monitoring status 分离。

### 6. Migration 不回填账号，首次 promotion 建基线

Migration 是 additive forward：创建表、约束、索引、受控函数、权限，并扩展 Provider state。它不扫描 snapshot items，也不把 current pointer 当作当前账号集合。原因是旧 snapshot foundation 部署前后可能有缺口，单个历史快照无法证明连续缺失或当前策略范围。

部署后每个 Provider 的下一次合格 promotion只为实际 item 创建 present 基线；此前生命周期为空。首次合格空集合也保持为空。第二次及后续合格 promotion 才可能对已建立基线的账号累计 missing。

索引至少支持 `(instance_id, provider, lifecycle, account_key)` 的转换/有界读取和 `(instance_id, account_key)` 唯一查询。批量输入继续受现有单 poll 记录上限保护，数据库重算 account key 与 Provider/poll 关系，拒绝额外或重复项。

### 7. 读取边界只供内部 Store，不改变 OpenAPI

新增 sqlc 查询与 Store DTO，用 instance、可选 provider/lifecycle、严格最大 limit 和 account key cursor 做稳定排序读取。它是后续产品查询 change 的内部构件，不注册 HTTP route，不修改 `api/openapi.yaml`，也不生成前端客户端。运行时角色只获得受控 finalize、lifecycle-aware policy activation和必要有界 SELECT 函数的 EXECUTE；无表级任意 INSERT/UPDATE/DELETE/TRUNCATE。

内部 DTO 只返回字段白名单，不包含 Secret、endpoint 或原始错误。调用者禁止把 DTO 整体格式化到日志。后续产品 API 必须单独定义 RBAC、分页、email 展示/搜索、审计和导出策略。

### 8. 观测只暴露低基数聚合

允许从 PostgreSQL 当前状态导出：

```text
relay_control_account_inventory_lifecycle_total{instance_id,provider,lifecycle}
relay_control_account_inventory_lifecycle_transition_total{instance_id,provider,lifecycle,reason}
```

reason 是 `promotion_present|first_complete_miss|second_complete_miss|reappeared|provider_out_of_scope` 等封闭值；如果 transition counter 无法在不新增事件表的情况下跨重启准确恢复，则本 change 只实现 current total，并把 transition total 延后，禁止以内存计数伪造持久事实。

email、account key、poll/policy ID、版本/提交、endpoint/IP、Secret、响应片段和 raw error 禁止进入标签、普通日志、错误、SQL 参数日志、测试输出与 artifact。结构化日志只记录固定 operation/result/reason 与受控 instance/provider，不记录逐账号 transition。验收 canary 扫描所有成功、失败、回滚和 policy race 路径且不回显命中值。

### 9. 时间、失败和数据面全部 fail closed

`observed_at`、missing/out-of-scope since 和 updated time均使用 PostgreSQL UTC。Worker/Node 时间不驱动 lifecycle；Node 源时间只作为实际出现账号的白名单来源字段。数据库不可用时不创建内存生命周期，也不发起无法归属的新 Node 请求；恢复后只按持久 poll 状态和有效槽继续。

生命周期不影响 Scheduler 资格、Driver 请求、Gateway 或 Relay Node。Control/PostgreSQL 停止、lifecycle-aware 函数拒绝输入或应用回滚，只暂停后续状态推进。已形成 Node 失败仍按 poll 契约 finalized，不因 lifecycle 跳过而重试同槽。

## Risks / Trade-offs

- **明文 email 成为当前表字段**：产品后续需要展示/搜索，但会扩大数据库内身份副本。通过受保护列、最小权限、无产品 API、日志/指标禁用和 canary 扫描限制暴露面；本 change 不声称静态加密。
- **finalize 事务进一步变长**：完整空集合也可能更新大量账号。使用现有 item 上限、批量 SQL、必要索引、稳定锁序和 1/10/50 Node 容量门禁；超过事务/lease 预算时 fail closed，而不拆成最终一致任务。
- **Provider 级行锁争用**：迟到槽和策略切换被有意串行。单 Provider 是正确一致性粒度，Provider 之间保持独立。
- **首次部署没有历史当前账号**：短期内页面仍无可用基线，但避免把旧快照误当连续证据。下一次完整 promotion 会自然建立状态。
- **missing_since 记录第二次缺失**：不能直接表达首次怀疑时间；换来字段与确证 missing 语义一致。未来历史事件/摘要 change 可保留两次 transition。
- **旧应用回滚需关闭两个写路径**：这是保留 forward schema 的明确运维成本。Runbook 和启动兼容检查必须把 poll 与 policy mutation 一起禁用，避免混用函数版本。
- **无 transition 事件表**：当前状态可可靠重建，累计转换计数未必可重启恢复。宁可延后 counter，也不引入尚未设计的历史保留模型。

## Migration Plan

1. 新增 additive Migration、lifecycle-aware finalize/activation、约束、权限、sqlc 和 Store；默认 lifecycle 写路径关闭。
2. 在 PostgreSQL 18 隔离环境验证 Migration、空基线、完整/空 promotion、两次缺失、恢复、out-of-scope、策略竞态、旧 fencing、提交未知和非空 down 保护。
3. 运行 fake Driver、官方 CLIProxyAPI 镜像和敏感 canary 验收，确认 Node 请求数不变且 lifecycle/snapshot/poll 全有或全无。
4. 用 1/10/50 Node 测量 finalize 行锁、事务耗时、WAL 和 120 秒 dispatch grace/30 秒 lease 余量；不满足则保持功能关闭。
5. 先部署 schema 与理解新函数的二进制，再启用 lifecycle-aware poll 和策略 mutation。观察 current lifecycle 聚合、错误分类和数据库资源。
6. 应用回滚时先停止 poll 与 Provider policy mutation，再回退二进制；保留 Migration 和全部已提交状态。恢复新版本后由下一合格 promotion继续，不回算停机槽。
7. 生产不执行 down。受保护 down 只允许 lifecycle 表为空、没有 out-of-scope 新状态且无后续依赖的全新环境恢复旧函数/权限并删除新增对象。

## Open Questions

无。`missing_since` 使用第二次完整缺失的数据库 observed time；计数在 2 饱和；重新 active 的旧账号只有实际出现才恢复；Migration 不历史回填；不可持久恢复的 transition counter 明确延后。
