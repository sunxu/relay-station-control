# Slice E — DingTalk runtime/restart/replay focused validation

## Baseline 与范围

- Control：`187718223f62669e9c722187c0833f5478a5f313`，`feat(phase5): integrate transactional notifications`。
- Slice D 已由用户正式确认 Implementation Review PASS（P0/P1/P2=0），从 `ac21b99c2b70fd863fdf37ed492a89858352a54f` 精确提交；提交后工作树干净。历史 migration 00028～00031 未改动，Slice D 新增 00032。
- Ops：`dd3041f916dc16578f21790e71e49c3f751decda`。
- Gateway：`6b045698e6e5e62e35dbd103abf20c1407f8a0bb`。
- CLIProxyAPI：`273d624c70f6eb8bdd7b049df396c306acd3f8d0`。

Architecture Review = PASS；Implementation = IN PROGRESS；Runtime Acceptance = NOT STARTED。

本轮仅补充已实现 DingTalk Executor、durable Worker/Reconciler 与 PostgreSQL 的 focused recovery 证明。只使用本地受控 HTTPS endpoint，不连接真实钉钉群。Worker/Reconciler reconstruction 与隔离数据库 fixture 属于 implementation focused evidence，不等价于部署进程 crash、真实通知或正式 86-case Runtime Acceptance。Problems UI 不在本工作树实施。

两份现有仓库外 working reports 已仅刷新 Slice D PASS / COMMITTED、上述 SHA 与提交时 Tasks 31/50，移除已解决的 transactional enqueue/stable producer/disabled gate prerequisite；86 个 case 行逐行保持不变。保留 Problems UI NOT IMPLEMENTED、remaining DingTalk runtime/restart/replay OPEN、Runtime Acceptance NOT STARTED。文件未加入 Git：

- `/private/tmp/phase5-runtime-acceptance-matrix-current.md`
- `/private/tmp/phase5-runtime-acceptance-harness-preflight-current.md`

## Focused recovery evidence

测试位于 `internal/dingtalk/runtime_integration_test.go`，使用真实 `dingtalk.Executor`、`jobs.Worker` / `Reconciler`、`store.JobRepository`、`store.NewJobTxStore` / `jobs.EnqueueTx`。`runtime_export_test.go` 仅向外部测试包开放现有 httptest TLS trust 接线；没有 production test hook，没有复制 Store/SQL adapter、enqueue 或 canonicalizer。

| 测试 | 实际证明 | 结果 |
|---|---|---|
| `TestRuntimeDingTalkUnknownReplayBudgetAndStableSnapshot` | 重建 Worker/Repository，五次真实 HTTPS unknown；前四次写持久退避、提前 claim 被拒绝；第五次 failed / max_attempts_exhausted；无第六次 claim；key/operation/payload/hash 不变、五个不同 fence、一个 job | PASS |
| `TestRuntimeDingTalkAcceptedCrashReconcilesAndFreshWorkerSucceeds` | 真实 POST/business success 后丢失未提交 transition；保留 running，经 expired lease 恢复到 retry_wait，新 Worker 再发成功；target_hits≥2、一个 job；新 running lease 有效时旧成功 token 被拒绝且无 event 写入，随后新 token 提交成功 | PASS |
| `TestRuntimeDingTalkTimeoutResetBothPolicies` | 同一受控 HTTPS timeout / post-write reset：ordinary false 实际 verifying→Verify、不再 POST；DingTalk true 两次 Execute/retry、固定 unknown reason，无 Verify/rollback | PASS |
| `TestRuntimeDingTalkExpiredRunningBothPolicies` | 两种 policy 均从真实 Execute 后未提交结果的 running 状态过期；DingTalk 恢复为 retry_wait，ordinary 调用 Verify，不在 Reconciler 中 Execute | PASS |
| `TestRuntimeDingTalkRetryWaitCancellationAndDeadlineGuards` | 已有 unknown evidence 的 retry_wait，取消/过期阻止再次 Execute，不伪装 safe cancellation | PASS |
| `TestRuntimeDingTalkExpiredRunningReplayGuards` | running unknown/crash 后分别设置 cancel、deadline、真实第五次 attempt；Reconciler failed，固定对应 code、无额外 claim/HTTP/Verify/Rollback，稳定身份与 payload/hash 保留 | PASS |
| `TestDefensiveVerifyRollbackDoNotPerformIO` | 即使 enabled + 有效 payload，防御接口无 HTTP I/O、无 mutation（含 cancelled context） | PASS |

测试保持 production Definition 的 timeout=10s、lease=30s、heartbeat=5s、max attempts=5、max verify attempts=1 和两个 persisted policies。重试使用测试配置的既有 durable backoff（1s），不改写 available_at，不重置 attempt。DingTalk fixture 在首次 enqueue 构造 frozen key 与 UUIDv5 operation，之后所有恢复复用原记录；仅 fixture 构造一次 occurrence ID，业务时间为固定 canonical UTC 字符串。

故障边界：test-only Repository wrapper 只观察或阻断提交前 transition，所有正常读写委派真实 Store。lease expiry 使用隔离 DB 的 owner fixture 将 lease_expires_at 置为过去，不缩短注册 lease；deadline fixture 在一个 owner transaction 内暂时关闭 mutation trigger、设置合法历史时间并恢复 trigger 后提交，随后才执行被测 runtime 操作。没有在被测操作期间关闭 guard；没有 production schema/policy 修改。该证据不声称等待真实 30s 或执行了 OS 进程 kill。

ordinary 对照是 test-only 注册，复用同一 HTTPS Execute 返回值但关闭两个 policy；Verify 返回保守 unknown，证明 Verify-first 而非新增钉钉 receipt 协议。正式 DingTalk job 的各正常链路 Verify/Rollback 调用为零。

任务映射：本轮完成 4.5 / 4.5a / 4.5b；结合重新通过的 `TestExecutionPolicyInvariantAndCatalogSnapshot`、`TestWorkerAndReconcilerPolicyMismatchFailsClosedWithoutOperation`、DingTalk catalog/snapshot、DB direct authorization/expired-token 与普通 ExecuteNeedsVerification/unauthorized ExecuteSucceeded 回归，完成 4.5d / 4.5e。Tasks 从 31/50 到 36/50。4.6（含 Jobs UI）、4.7（跨 API/UI 等完整 Secret-negative）、Problems UI 与全 Phase deployment acceptance 保持 open。

## Validation

显式设置 `CONTROL_DATABASE_TEST_URL` 与 `CONTROL_RUNTIME_DATABASE_TEST_URL`，使用本地 55432 隔离 PostgreSQL、migration owner 建 fixture、受限 runtime role 执行；未使用部署环境数据库。以下均 `-count=1`，零 skip / 零 fail，主 Agent 已复核测试日志：

| Focused suite | 顶层 tests | 时间 | 结果 |
|---|---:|---:|---|
| Store `^Test(DingTalk\|DurableJob)` | 40 | 41.664s | PASS |
| Token Health | 5 | 12.458s | PASS |
| Quality v4 | 4 | 5.450s | PASS |
| Problems | 12 | 19.539s | PASS |
| Availability confirmation/recovery/concurrency 等 | 8 | 9.522s | PASS |
| Duplicate lifecycle/membership 等 | 8 | 24.686s | PASS |
| Slice D notification integration | 22 | 20.822s | PASS |
| 既有 8 项 pinned historical compatibility | 8 | 12.244s | PASS |

其中 notification tests 保留 same-tx fault rollback、stable operation identity、disabled/no-backfill、membership-only silence、processed-key pagination 与 v1 compatibility；未修改历史 fixtures。历史组与其它组存在重叠，不将以上执行数声称为互不重复的案例总数。

- 显式 PostgreSQL `go test ./internal/dingtalk/... -run '^TestRuntimeDingTalk' -count=1 -v`：PASS（6 个顶层、7 个子场景，零 skip，33.790s）。
- 显式 PostgreSQL `go test -race ./internal/dingtalk/... -count=1`：最终工作树 PASS（41.034s，含所有新增 runtime tests；无 DB skip）。
- `go test ./internal/dingtalk/... ./internal/jobs/... ./cmd/control/... -count=1`：PASS；最终 jobs/cmd 再次 `-count=1` PASS，DingTalk 新增恢复链另以上述显式 DB 与 race 复验。
- `TestDefensiveVerifyRollbackDoNotPerformIO`：PASS；配置有效、payload 有效、正常/取消 context 下，防御方法无 HTTP I/O、无 mutation。
- `make generate`：PASS，生成物无 diff。
- `make test build`：PASS；Go、前端 22 files / 133 tests、typecheck、前后端 build 通过。该命令未设置 PostgreSQL test URLs，不能视为 full-store DB suite PASS。
- OpenSpec current strict / all strict：PASS（20/20）；Markdown 本地引用、`git diff --check`（含新增未跟踪文件）均 PASS。
- Full-store：NOT GREEN / PRE-EXISTING；本轮不运行或修复无关 full-store baseline，不将 focused 子集或默认跳过 DB 的 Go 测试冒充全套数据库验收。

测试基础设施已有 jsdom pseudo-element warning，以及 Go module stat-cache 写权限 warning；上述 make 命令 exit 0，不因此修改宿主缓存或无关实现。

## 状态

Self-review：P0=0 / P1=0 / P2=0；runtime-work readiness = READY for Implementation Review（不是正式批准）。本轮 runtime work 未提交、未 push；production code/migration/generated/Problems UI 均无本轮差异，Ops/Gateway/CLIProxyAPI 保持原 HEAD 与干净工作树。Runtime Acceptance = NOT STARTED，完成后停止，不继续 UI 或部署验收。
