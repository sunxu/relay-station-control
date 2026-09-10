# Slice E Executor foundation validation

## Scope and baseline

Implementation order 调整为 Executor foundation → review/commit → Slice D transaction integration → 后续 runtime acceptance。原因是 production Registry 强制真实 Executor；未增加 enqueue-only exception，未注册 dummy/noop executor。

- Control：`b7c4d11ec80b5143ef3f18171711b9499f33576d`
- Ops：`dd3041f916dc16578f21790e71e49c3f751decda`
- Gateway：`6b045698e6e5e62e35dbd103abf20c1407f8a0bb`
- CLIProxyAPI：`273d624c70f6eb8bdd7b049df396c306acd3f8d0`

Architecture Review 保持 PASS；Implementation IN PROGRESS；Runtime Acceptance NOT STARTED。本记录不是 Implementation Review 批准或实际群投递验收。Slice D 仍未实现，Availability/Duplicate 不调用 notification EnqueueTx。

## 本轮批准的 registration parameters

`dingtalk_alert_delivery`，payload schema 1：Timeout=10s、LeaseDuration=30s、HeartbeatInterval=5s、MaxAttempts=5、MaxVerifyAttempts=1；ReplaySafe=true、AllowUnknownEffectReplay=true、AllowDirectSuccess=true、AllowRollback=false。

30s/5s/1 是用户本轮明确批准的 implementation-level decision，不追溯称为先前 Architecture Review 的历史冻结值。MaxVerifyAttempts 仅满足框架最小合法字段；真实 Executor 的 Verify/Rollback 接口防御方法不执行 I/O，不建设 receipt polling/rollback protocol。正常成功返回 ExecuteSucceeded。

## 官方协议依据（本轮读取的当前文档）

官方页面使用动态正文。通过页面所载官方 `lippi-dingtalk-doc-front/0.52.0/app.js` 确认其正文地址模板，再读取以下官方托管正文；未依赖博客、SDK 行为推断或历史复制示例。

1. [自定义机器人安全设置](https://open.dingtalk.com/document/robots/customize-robot-security-settings)（[该官方页面加载的正文](https://icms-document.oss-cn-beijing.aliyuncs.com/zh-CN/dingtalk/robots/topics/customize-robot-security-settings.html)）：UTF-8 输入 `timestamp + "\n" + secret`，以 secret 为 HMAC-SHA256 key；Base64 后 URL 编码。timestamp 为发送时毫秒时间戳；query 参数为 timestamp/sign，保留 webhook access_token。签名与时间戳只存在本次 transport，不写回 payload。
2. [自定义机器人发送群消息](https://open.dingtalk.com/document/orgapp/custom-robots-send-group-messages)（[该官方页面加载的正文](https://icms-document.oss-cn-beijing.aliyuncs.com/zh-CN/dingtalk/orgapp/topics/custom-robots-send-group-messages.html)）：POST `/robot/send`，必填 access_token query；text 消息为 `msgtype=text` 与 `text.content`。返回 errcode/errmsg，成功示例 0/ok。字段表为 Number，当前返回示例为字符串 `"0"`：解析兼容整数和十进制整数字符串，拒绝 bool/null/float、重复字段、尾随文档、缺失字段及非明确成功组合。

官方发送接口错误表列出：

- `-1` 系统繁忙、建议稍后重试：返回 ResultUnknown，官方重试建议本身不证明外部效果不存在。
- `410100` 发送限流：明确拒绝发送，返回 RetryableNoEffect。
- `40035`、`43004`：请求参数/Content-Type 错误；`400013`、`400101`、`400102`、`400105`、`400106`：群/凭证/机器人/消息类型不可用；`430101`～`430104`：内容拒绝；`310000`：关键词/时间戳/签名/IP 安全校验未通过。均永久失败，不记录 errmsg 原文。
- 未识别 business code fail closed，不自行猜测为临时失败。

HTTP 408/429/5xx 按冻结契约允许重试，但 status 本身不证明 no-effect，保守返回 ResultUnknown。DNS/connect/TLS 在 GotConn 前失败证明尚不能写请求，返回 RetryableNoEffect；取得连接之后的网络错误、响应读取中断保守 ResultUnknown。非法/过大业务响应永久失败。ErrorCode 固定低基数，不包含 URL、正文、email、account key。

## Implementation boundaries

- 00031 只登记 async_job_kinds；00028/29/30 未修改，无新领域表/策略/状态/事件。
- runtime URL 缺失正常配置、保留 production Definition 和真实 Executor；既有排队任务遇到 disabled 返回固定永久失败，不伪装发送成功。本轮不生成业务 notification jobs。
- 独立 HTTPS transport：Proxy=nil、拒绝 redirect、HTTP total timeout=5s、有界 8KiB response；不复用 management client，不新增代理支持或目标 allowlist。
- 渲染只消费14字段 immutable snapshot，不读取当前 Inventory/Problems/Token/Node/occurrence。
- generic Worker/Reconciler 未增加 DingTalk 条件分支，仍使用 Slice A 的 policy/evidence、fencing、TransitionOutcome。

## Validation

不将单元/隔离 PostgreSQL 验证等同于 Runtime Acceptance。

| 检查 | 实际结果 |
|---|---|
| `go test ./internal/dingtalk/... ./internal/jobs/... ./cmd/control/... -count=1` | PASS；5.613s / 3.386s / 0.944s |
| `go test -race ./internal/dingtalk/... -count=1` | PASS；6.841s |
| 新增 malformed-config/四类消息/Secret 字段负例后的 dingtalk + cmd focused rerun | PASS；5.588s / 0.965s |
| 显式 owner/runtime DB URLs，`^TestDingTalk`，clean install、30→31、configured/absent、初始 jobs=0、persisted snapshot/Secret-negative | PASS；主 Agent 独立复跑 2.057s |
| 已迁移31的隔离测试库，`^Test(DurableJob\|DingTalk)`，完整 focused DB suite | PASS；主 Agent 42.144s，包含 cancellation matrix、TransitionOutcome/Mutation、policy snapshot、ordinary Verify-first、fencing、budget 与升级默认 false |
| `^TestAccountTokenHealth` / `^TestNodeAccountQualityV4` | PASS；5 / 4 个 top-level tests，19.108s / 7.629s |
| `^TestProblemAccount` | PASS；12 个 top-level tests，23.650s |
| Availability focused（下述完整 pattern） | PASS；8 个 top-level tests，15.135s |
| Duplicate focused（下述完整 pattern） | PASS；8 个 top-level tests，31.771s |
| `make generate` | PASS；生成物无 diff |
| `make test build`（默认环境） | PASS；Go、前端22文件/133 tests、typecheck、build；默认环境不代表 PostgreSQL full-store |
| OpenSpec current strict / all strict | PASS；all 20 items |
| `git diff --check` | PASS |
| 修改 Markdown 的本地引用检查 | PASS |

PostgreSQL tests 使用本机隔离开发实例 `127.0.0.1:55432` 的 migrator/runtime roles；没有访问部署数据库55434。Token/Quality/Problems/Availability/Duplicate tests 自建隔离库，无 skip。需要已迁移顶层库的 durable tests 最终使用专门创建并迁移到31的临时测试库。

Availability pattern：`^TestAccountAvailability(ACLAndMigrationPostgres|BatchPerformanceAndPaginationPostgres|NodeIsolationPostgres|ConfirmationPostgres|FailuresMustFollowSuccessWatermarkPostgres|RecoveryRestartAndRetentionPostgres|RecoveryEvidencePostgres|FreshnessDisabledAndConcurrencyPostgres)$`。

Duplicate pattern：`^TestCrossNodeDuplicate(OwnershipLifecycle|OwnershipQuery|OwnershipOccurrenceReadModel|EvidenceWriteAmplificationRegressionPostgres|AbsenceResolveReopenHistoryRegressionPostgres|MaterialCheckpointPostgres|MaterialStaleAndRecoveryPostgres|OwnershipReconciliation)$`。

### 本轮发现和修正，不归类为既有 full-store baseline

1. 第一轮 durable DB suite 有9项失败：6项因顶层测试 URL 指向无 schema 的 postgres 库（42P01）；改用专门已迁移31的测试库后通过，未改产品行为。
2. 另3项为00031带来的测试兼容调整：current catalog 精确预期从空变为唯一 DingTalk kind；00028 forward-only 测试固定到28；原 legacy down/up + Mutation 测试升级固定到30，继续核验00028的原错误与版本28，避免被00031更早拦截。原 lifecycle/ACL/取消/fencing/原子性断言未弱化，历史 migrations 未修改。
3. Payload UUID 校验加强时发现旧测试 fixture 的非 UUID 值，已修正并补四类 issue、非法 transition/type/time/ID 和全部禁止字段负例。
4. 一次独立复跑使用了已重命名的旧测试名而未匹配任何 tests；该命令不算 PASS 证据，随后用 `^TestDingTalk` 成功执行上述实际场景。

Full-store 状态仍为 **NOT GREEN / PRE-EXISTING**，本轮未运行完整带 DB URL 的 internal/store suite，也未修 unrelated Gateway TLS/history/query-plan/CI baseline。未连接真实 DingTalk 群、未部署、未执行 Phase 5 全套 runtime acceptance。

所有代码/test 修正后再次 `make test build` 退出0；Go build 输出宿主 module stat cache 写入权限 warning，但二进制构建成功，未修改宿主缓存权限。最终 strict/reference/diff 检查通过。专门创建的临时测试库已清理，未删除产品数据；Ops/Gateway/CLIProxyAPI HEAD 与干净工作树再次核对无变化。

## Task accounting and stop

本轮勾选 3.1、3.1a、3.1e、4.1～4.4；累计24/50。跨 Slice 的 notification enqueue、DingTalk crash/replay端到端、UI/最终失败可见性与完整 Secret-negative/runtime acceptance 仍保持 open，不以基础测试代替。

独立子 Agent 对 config/transport/main 接线的限定代码 review 未报告 P0/P1/P2；主 Agent 汇总 DB 与 generic durable regressions。Executor foundation 可以提交 Implementation Review，不自行声明正式 Implementation Review PASS。

Scope：Availability notification integration=false；Duplicate notification integration=false；Problems UI=false；新增 notification/receipt/workflow table=false；Gateway/CLIProxyAPI unchanged。不 commit/push，停在 Slice E foundation，不恢复 Slice D。

## Executor review fixes after Notification Identity amendment

此前 Executor Foundation Implementation Review 为 CHANGES REQUIRED（P0=0/P1=1/P2=1），不是上文限定自查的通过结论。随后用户正式批准 Notification Identity / Payload Canonicalization Architecture Re-review PASS，overall Architecture Review 保持 PASS；批准记录见 [planning-validation](./planning-validation.md#formal-notification-identity--payload-canonicalization-re-review-approval)。

Architecture amendment commit / 本轮 Slice E implementation baseline：`5fe9d851e63c21fc1d5af5d0d00e5581f1c6ba5e`。该提交仅包含 design.md、account-health-alerting/spec.md、planning-validation.md 三个文件；Slice E production/migration/test/evidence 未提交，未 push。其它三仓 baseline 与上文一致。

本轮修复范围：

- P1 node_names：既有 FieldStringArray 默认 sorted/unique 保持；新增显式 AllowDuplicates / PreserveOrder opt-in，仅 DingTalk node_names 启用。两个数组等长、非空；instance_ids 必须 canonical UUID ascending unique；名称按 ID 的 positional pairing 保留，不独立排序。重复 Relay/Relay 和非 lexical Zulu/Alpha 均合法，少一个名称拒绝。此前暂停工作树中 independent collections 解释已由正式 amendment 取代。
- P2 tuple：四类 occurrence_type/reason/severity 固定映射；错误组合返回 jobs.ErrInvalidPayload。Definition 的可选语义 validator 在 Registry.ValidateAndHash 已有 canonicalization 后、hash/enqueue 前执行；缺省 nil 保留普通 schema 行为。发送前复用同一语义检查，不让无效 payload 发出 HTTP。
- Timestamp：started_at/transitioned_at 均 parse 后与 UTC RFC3339Nano canonical re-format 精确相等，并保留 transitioned_at >= started_at；拒绝 +08:00、+00:00、冗余小数零等非 canonical 表示。业务时间只读原 snapshot，不由 Executor 创建；time.Now().UnixMilli() 仍仅用于 signing。
- 未实现 operation_id builder/producer；未来 Slice D 使用已批准 UUIDv5 namespace/key derivation。synthetic enqueue tests priority=50，Executor 不选择 priority。没有 Availability/Duplicate EnqueueTx、Problems UI、新表/状态/框架。

本轮不新增或勾选 task，累计保持24/50。以下验证仅为实现自查，不能替代正式 Executor Implementation Re-review，也不是 Runtime Acceptance。

### Review-fix validation

- 最终主 Agent `go test ./internal/dingtalk/... ./internal/jobs/... ./cmd/control/... -count=1`：PASS（5.524s / 3.760s / 1.006s）；`go test -race ./internal/dingtalk/... -count=1`：PASS（6.748s）。补强后的测试覆盖指定六种错误 tuple、两个 timestamp 字段的非 canonical offset/小数表示、UTC builder 正例、名称重复渲染两次、Zulu/Alpha positional 顺序以及 canonical/hash replay 稳定。

- 显式 PostgreSQL `go test ./internal/store/... -run '^Test(DingTalk|DurableJob)' -count=1 -v`：PASS，48.865s；37 个 durable-job tests + 3 个 DingTalk tests，包含新 duplicate-name/idempotent enqueue、parallel-array persistence、错误 tuple/time/cardinality 不写入 job，以及 clean install/30→31、catalog/snapshot、cancellation evidence、TransitionOutcome/Mutation、Verify-first、lease/fencing/budget 回归。
- Phase 5 A/B/C 显式 PostgreSQL focused regression：PASS，37/37 top-level tests、0 skip、0 failure，79.604s；Availability 8、Token Health 5、Quality v4 4、Problems 12、Duplicate 8，patterns 沿用上文。
- PostgreSQL 使用本机55432的开发 migrator/runtime roles；durable tests 使用本轮创建并迁移到31的 `relay_control_slice_e_fix_20260911`，测试完成后已清理。未访问部署库55434或真实 DingTalk 群。
- `make generate`、`make test build`：PASS；生成 Go/TS 无新 diff。Go build 有宿主 module stat-cache 写入权限 warning，但命令退出0；未修改宿主缓存权限，未修无关 Vitest/full-store baseline。
- OpenSpec current strict / all strict：PASS（20/20）；Markdown fences/本地路径及 anchor references：PASS；git diff --check：PASS。

Full-store：NOT GREEN / PRE-EXISTING。本轮没有重跑完整带 DB URL 的 internal/store suite；默认 make test 通过不能等同 full-store green。Runtime Acceptance 仍 NOT STARTED。暂停前追加测试曾有 NewJobTxStore 返回值未解包的未完成编译问题，本轮已修正；不将其归类为 baseline failure。

最终 self-review：P0=0/P1=0/P2=0；P1 node_names FIXED，P2 tuple FIXED，canonical timestamp implemented；Executor Foundation readiness for Implementation Re-review=READY。此结论不是正式 Implementation Review PASS。Implementation IN PROGRESS；Runtime Acceptance NOT STARTED。按 OpenSpec apply 工作流保留跨 Slice 未完成勾选，未新增架构或实施任务。

两个 `gpt-5.6-luna` 子 Agent 分别完成限定 payload/test 修改与只读 PostgreSQL A/B/C 回归，主 Agent 检查契约一致性、generic schema/default compatibility、真实 enqueue/persistence 与最终 focused/race 复跑。所有子任务已停止。Ops/Gateway/CLIProxyAPI 工作树干净且 baseline 不变；Slice D implemented=false。本轮唯一 commit 是上述 architecture docs commit，余下 Slice E 工作树未提交，暂存区为空，未 push。
