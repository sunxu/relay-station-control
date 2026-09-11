# Slice D — Transactional Notification Integration

## Baseline and status

日期：2026-09-11。Architecture Review = PASS；Implementation = IN PROGRESS；Runtime Acceptance = NOT STARTED。本记录是 implementation self-review / focused validation，不替代正式 Slice D Implementation Review。

用户确认 Slice E Executor Foundation 正式 Implementation Re-review PASS 后，只提交已审的 19 个 Slice E 文件：`ac21b99c2b70fd863fdf37ed492a89858352a54f`，`feat(phase5): add dingtalk delivery executor`。提交前 Control HEAD 为 `5fe9d851e63c21fc1d5af5d0d00e5581f1c6ba5e`；提交后工作树干净。没有 push。

Slice D implementation baseline：

- Control：`ac21b99c2b70fd863fdf37ed492a89858352a54f`
- Ops：`dd3041f916dc16578f21790e71e49c3f751decda`
- Gateway：`6b045698e6e5e62e35dbd103abf20c1407f8a0bb`
- CLIProxyAPI：`273d624c70f6eb8bdd7b049df396c306acd3f8d0`

遵循 [design](./design.md)、[account-health spec](./specs/account-health-alerting/spec.md) 和 [architecture evidence](./planning-validation.md)，不修改已冻结架构、不进入 UI 或 runtime acceptance。Slice D 工作树未提交。

## Implementation and compatibility

- 新增 [00032](../../../migrations/00032_transactional_notification_snapshots.sql)：仅 additive Availability v2 和受限 display snapshot helper，无新表/列/索引。00028～00031 不变。31→32 的 `pg_get_functiondef(v1)` 完全一致，v1 processed-key cardinality 不变。
- Availability v2 保留现行 v1 的 confirmation/recovery 算法，每 processed key 一行，另带实际 INSERT/UPDATE RETURNING 捕获的 transition array。105-key 测试包含同 key 两种 reason、尾页 transition、零 transition 行；仍为 100-key 有界分页。
- `reconcileAvailabilityPage` 保持 SERIALIZABLE 与 40001/40P01 bounded retry。读取全部 rows、关闭 cursor 后才在原 pgx transaction 调用 `jobs.EnqueueTx`，最后 commit。
- Duplicate 只在 `Evaluate` 最终 authoritative result 后、commit 前 enqueue；复用既有 Created/Status/AffectedNodes/EvaluationAt/FirstSeenAt。不改 create/reconcile 的领域算法、membership、outer reconciler；observer 仍在 commit 后，仅用于 observability。
- startup 仅在 `dingtalkConfig.Enabled()` 时注入通知依赖。缺省 nil 路径完全跳过 EnqueueTx；Availability 仍走原 v1。当前 production 未配置 Redis publisher，因此启用通知时复用 Worker PostgreSQL polling，并将已有 wake outbox 标为 suppressed；这与 feature disabled 完全不同，后者 job/event/outbox 都不创建。
- 共用 producer 固定四 key、UUIDv5 namespace `94db90f6-d7e6-4cce-a045-890b63171d86`、完整 key UTF-8 name、schema 1、Priority 50、Actor service。Registry.ValidateAndHash / EnqueueTx 仍是唯一 canonical/hash/幂等/持久化实现。
- display snapshot 在同一领域事务读取；整体按 UUID 排序成 positional arrays，重复名称保留。业务时间来自领域 transition，使用 UTC RFC3339Nano。未读取凭据或动态 Token/Quality 诊断。

### Integration-discovered payload compatibility fixes

没有重构 Slice E HTTP、签名、config、classifier、registry policy 或 generic Worker。

1. 既有资产名称为最多 100 个字符，而 schema 长度按 UTF-8 字节计数；旧 256-byte bound 会拒绝合法中文/四字节名称。仅将 environment_name/node_names bound 对齐为 400 bytes，account_key 对齐现行 Inventory 385-byte bound。新增生产 Registry/rendering 回归，避免合法领域快照因过窄字段限制回滚。
2. 既有 Duplicate `AffectedNodes` 明确为本次 evaluation 后、resolve 时冻结的 membership；所有节点合法 absence_confirmed 后可以为空。严格保留该事实，不替换成 pre-resolution membership、不引入第二份状态。只为 duplicate RESOLVED 接受 `instance_ids=[] / node_names=[]`；nil/null、不同 cardinality 与 ACTIVE 空成员仍拒绝。display helper 支持该空集合，真实 PostgreSQL 验证一次 RESOLVED intent 与领域恢复共同提交。旧 Slice E evidence 的“非空”是当时实现限制，本次修复不改写其历史记录。

## Focused evidence

数据库仅使用本机 55432 的隔离开发库与受限 runtime role；未访问部署库 55434、未连接真实 DingTalk 群。

| 检查面 | 证据与结果 |
|---|---|
| Pagination / multiple transitions | `TestAccountAvailabilityNotificationPaginationZeroAndMultipleTransitions`：105 keys、同 key 两种 reason、尾页通知、100 个零 transition rows，PASS |
| Disabled / no-backfill | 两条领域路径：disabled 领域提交且 job/event/outbox=0；启用无新 transition 仍 0；新 RESOLVED 正常入队，PASS |
| ACTIVE / RESOLVED / recurrence | 持续 ACTIVE 不重发；合法恢复一次；新 occurrence 开新通知链，PASS |
| A/B/C + Problems | 3→2→0 duplicate Problems，membership-only 无新增 intent，最终一次 RESOLVED；独立 TOKEN_INVALID issue 始终保留，PASS |
| Positional snapshot / rename | 相同名称、非 lexical 名称顺序、UUID pairing；ACTIVE 保留旧名、RESOLVED 使用同 tx 当前新名；零 owner 不伪造旧 membership，PASS |
| Availability atomicity | 分别在 async_jobs、async_job_events、operation_outbox 的真实 BEFORE INSERT trigger 抛错；occurrence/job/event/outbox 全回滚；解除注入后全提交，PASS |
| Duplicate atomicity | 同上三个真实故障点；occurrence/membership/evidence/job/event/outbox 均无残留；解除故障正常提交，PASS |
| Committed replay | 四种 key 重放保留 job/operation/payload/hash，只一个 logical job，PASS |
| Concurrency / rollback | 两个 SERIALIZABLE 同 snapshot producer 并发后保持一个 bundle；Availability / Duplicate concurrent reconcile 只一个 ACTIVE intent；注入 40001 后完整回滚无残留，PASS |
| ACL / upgrade | clean install、31→32、无新增表、v1 source 不变；两函数 owner=migrator、SECURITY DEFINER、search_path=pg_catalog、PUBLIC 无 EXECUTE、runtime 可执行，PASS |
| Tuple / diagnostics | 三类 Availability 固定 tuple；FORBIDDEN=Warning；DISABLED diagnostic change 不通知，PASS |

Commit ambiguity 证据使用“数据库已 commit，但调用方丢弃成功确认后重放同一 snapshot”的 seam，以及真实 SERIALIZABLE 并发/回滚；没有声称制造 TCP 层 COMMIT response 丢失。完全回滚的 attempt 可获得新 occurrence/time；不将未知 commit outcome 当成确定 rollback。

## Validation commands and results

执行前按工作区 AGENTS 设置统一 DevRAM 缓存/临时目录与 GOPROXY。显式 PostgreSQL URL 由测试环境传入，不在证据记录密码。

- `go test ./internal/dingtalk/... ./internal/jobs/... ./cmd/control/... -count=1`：PASS。
- `go test -race ./internal/dingtalk/... ./internal/jobs/... -count=1`：PASS（6.938s / 5.108s）。
- 显式 PostgreSQL `go test ./internal/store/... -run '^Test(DingTalk|DurableJob)' -count=1 -v`：PASS（42.993s）；含 catalog、policy snapshot、cancellation evidence、TransitionOutcome/Mutation、Verify-first、lease/fencing、budget 回归。
- 显式 PostgreSQL Token Health / Quality v4 / Problems / notification migration ACL：PASS（40.015s）。
- 显式 PostgreSQL 原 Availability 8 项、Duplicate 8 项 focused regression：PASS（38.995s）；包含 confirmation/recovery/concurrency、保守 membership 与 history，不弱化旧测试。
- 显式 PostgreSQL `go test ./internal/store/... -run '^Test(AccountAvailabilityNotification|CrossNodeDuplicateNotification|Notification)' -count=1 -v`：PASS（19.772s，22/22 top-level、0 failure、0 skip）；包括 identity、pagination、same-tx、disabled/no-backfill、A/B/C、rename、fault injection、recurrence 和 upgrade。
- `make generate`、`make test build`：PASS；Go、前端 22 files / 133 tests、typecheck、build。生成 Go/TS 没有新 diff。宿主 Go module stat-cache 写权限 warning 未导致失败，未修改宿主缓存权限。
- OpenSpec current strict / all strict：PASS（20/20）；Markdown 本地引用与 `git diff --check`：PASS。

开发期曾有并行 test 文件未完成的编译错误，以及 A/B/C 将剩余 C 错误断言为零成员的测试错误，均已修正并复跑；不归类为历史 baseline。

Full-store：**NOT GREEN / PRE-EXISTING**。本轮未运行完整带 DB URL 的 internal/store suite，默认 make test PASS 不代表 full-store green。不修 Gateway TLS/history/query-plan/Vitest/CI 等无关 baseline。

## Task accounting and stop

本轮完成并勾选 3.2、3.3、3.4、3.5、3.6、3.6a、3.7，累计 **31/50**。跨 Slice E deployment/restart/crash acceptance、UI、全 Phase runtime acceptance 仍 open。

Self-review：P0=0 / P1=0 / P2=0；Slice D readiness for Implementation Review = READY。这不是正式 Implementation Review PASS。

Gateway / CLIProxyAPI / Ops 不变；无新 notification/dedupe/operation table、outbox、workflow、scheduler、policy 或状态。Problems UI 未实施，Runtime Acceptance = NOT STARTED。Slice D 不 commit、不 push，到此停止。
