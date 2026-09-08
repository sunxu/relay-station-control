# Evidence

## Baseline

2026-09-08，HEAD `5065a5d`，起始工作树干净。原默认周期5秒，每轮追加。此change独立于`default-account-inventory-to-present`；不部署、不清理legacy数据。按用户后续决定采用20秒，默认lease仍为30秒，配置仍要求`0 < reconciliation interval < lease`，环境配置上限29秒不变。

proposal/design/spec/tasks已在实施前通过change strict；本轮canonical spec同步是用户明确要求，不代表archive；Final Review结果见下文。

## Implementation and Assertions

| 契约 | 实现 / fixture | 实际断言 |
|---|---|---|
| 每轮评估、只写material | lifecycle `reconcile`，sqlc `ListCrossNodeDuplicateOccurrenceLatestCheckpoint` | occurrence锁内重新评估并比较DB checkpoint；不缓存Go判断 |
| 100轮同来源同结论 | `TestCrossNodeDuplicateMaterialCheckpointPostgres` | 初始2行，100轮后仍2行；每轮新evaluation ID，last_seen/last_fully_verified推进，latest_evaluation_id保持原checkpoint |
| 新来源同结论 | 同上与`TestCrossNodeDuplicateEvidenceWriteAmplificationRegressionPostgres` | 新promoted source写完整2行checkpoint，总数2→4 |
| retention不制造新来源 | `TestCrossNodeDuplicateMaterialCheckpointPostgres` | current poll pointer置NULL后仍2行；原checkpoint evidence JSON逐字未变 |
| 无新采集fresh→stale | `TestCrossNodeDuplicateMaterialStaleAndRecoveryPostgres` | provider接近15分钟阈值后等待2.1秒，不再写provider；评估complete→degraded，总数2→4 |
| 重复degraded与零可引用行 | 同上 | 10轮degraded不增长；全部source不可引用时不伪造checkpoint；fixture以新source恢复为complete，造成material change后写新checkpoint；并非任意恢复都写入 |
| absence / resolve / reopen / history | `TestCrossNodeDuplicateAbsenceResolveReopenHistoryRegressionPostgres` | 真实目标occurrence的current membership=0；2条absence属于同evaluation；A和B history分别精确包含该RESOLVED occurrence与account_key；reopen新ID，旧occurrence保留。实现顺序为先append evidence再DELETE membership，同一事务提交 |
| 并发 / restart / retry | `TestCrossNodeDuplicateEvidenceWriteAmplificationRegressionPostgres` | 新source后8并发Evaluate仅追加一个2行checkpoint；重建repository重试不增长 |
| 原冻结规则 | 原Lifecycle/Reconciliation/Concurrency/OccurrenceReadModel/SnapshotRace专项 | eligibility、保守resolve、retention历史查询与并发snapshot语义回归通过 |
| 周期 / lease | config tests与`TestReconcilerRunUsesTwentySecondDefaultInterval` | 默认20秒、等于30秒lease拒绝；fake clock验证20秒初始timer/reset、取消退出；startup/finalize原回调保留 |

测试文件位于`internal/store/cross_node_duplicate_material_checkpoint_test.go`、`internal/store/cross_node_duplicate_evidence_write_amplification_integration_test.go`和`internal/inventorypoll/{config,runtime}_test.go`。PG使用README隔离开发数据库（localhost:55432）与独立runtime role，fixture创建独立测试库；没有操作本地部署的数据或legacy evidence。

## Commands and Results

- `go test ./internal/store -run 'TestCrossNodeDuplicateMaterial.*Postgres' -count=1 -v`：真实PG PASS，4.697s。
- `go test ./internal/store -run 'TestCrossNodeDuplicate(EvidenceWriteAmplification|AbsenceResolveReopenHistory)RegressionPostgres' -count=1 -v`：真实PG PASS，2.152s。
- `go test -race ./internal/store -run 'TestCrossNodeDuplicate(Material|EvidenceWriteAmplification|AbsenceResolveReopenHistory|Ownership(Lifecycle|Reconciliation|Concurrency|OccurrenceReadModel|EvidenceEvaluationSnapshotRace))' -count=1 -v`：真实PG PASS，31.525s。上述Go PG命令使用`CONTROL_DATABASE_TEST_URL`与`CONTROL_RUNTIME_DATABASE_TEST_URL`，按README提供隔离库凭据；不覆盖GOCACHE/GOTMPDIR。
- `make test build`：PASS，含生成、Go测试、前端18文件/111测试、typecheck/build与后端build。普通make未传PG环境；真实PG证据以以上专项为准。
- `go test -race ./internal/inventorypoll ./cmd/control -count=1`：修正新增fake-clock测试的跨goroutine指针发布与reset等待后PASS；生产代码无race修复。
- `openspec validate reduce-cross-node-duplicate-evidence-write-amplification --type change --strict --no-interactive`：PASS。
- `openspec validate --all --strict`：18/18 PASS。
- `git diff --check`：PASS。工作树仅本change的实现、测试、sqlc生成、spec与runbook，无migration/API/Web改动。

本地临时日志：`/private/tmp/duplicate-checkpoint-targeted-race.log`、`/private/tmp/duplicate-checkpoint-make.log`。长期证据为此文档的命令、fixture与断言，不依赖临时文件永久存在。

## Known Baseline Failure

扩大执行`go test -race ./internal/store -run 'TestCrossNodeDuplicate' -count=1 -v`时，4个既有migration acceptance失败：`MigrationUpDownUp`、`QueryAccessMigrationUpDownUp`、`EvidenceEvaluationMigrationUpDownUp`、`EvidenceEvaluationSnapshotGuardMigrationUpDownUp`（共同前缀`TestCrossNodeDuplicateOwnership`）。均为`migration version = 24, want 16`，不是本次material断言失败。

在实现验收时，`internal/store/cross_node_duplicate_ownership_migration_acceptance_test.go:35/95/157/225`在当时HEAD与工作树完全一致，固定要求16；HEAD已包含00024，`newIsolatedJobDatabase`默认迁移完整production集合。因此这是既有测试基线落后于migration序列。本轮不修改这些无关测试、不声称扩大PG套件全绿；相关专项已单独以race重新运行并PASS。该基线在后续独立提交`63270e8`修复，见下方归档对账。

## Scope and Review

0 migration；无新table/column/index/history truth；legacy evidence不改不删。OpenAPI、TS、Topology UI、Node/Gateway、eligibility/freshness/absence/resolve、告警身份均无变更。raw evidence诊断入口保留。仅generated sqlc Go因query变更由make生成。

2026-09-08，主Agent与独立子Agent完成 Architecture + Implementation Final Review：PASS，P1/P2 blockers为0。审查覆盖source NULL、checkpoint范围、zero-evidence、verification时间、resolve、并发/重启。用户接受非阻塞措辞建议：恢复造成 material change 时才写 checkpoint，并授权本地提交本change。四个旧migration测试基线问题仍单独记录，不纳入本次修复。交付仅本地commit；未push、未deploy、未archive。

## Archive Reconciliation

2026-09-08，用户授权测试基线修复并收口归档。实现commit：`8616808`。独立测试修复commit：`63270e8`，仅固定四个历史测试的初始化`up-to 16`及恢复终点，未修改生产migration。

真实隔离PG执行：`go test ./internal/store -run 'TestCrossNodeDuplicateOwnership(MigrationUpDownUp|QueryAccessMigrationUpDownUp|EvidenceEvaluationMigrationUpDownUp|EvidenceEvaluationSnapshotGuardMigrationUpDownUp)$' -count=1 -v`，4/4 PASS，5.693s；使用前述测试DB环境变量。保留原失败时间线，本次没有重跑整个PG套件。

归档前change/all strict PASS（18/18），tasks 9/9。canonical spec在实现commit中已提前同步，归档前逐项比较delta的3个Requirement正文与canonical完全一致，因此使用标准CLI `openspec archive reduce-cross-node-duplicate-evidence-write-amplification --yes --skip-specs`避免重复添加已同步Requirement；不手工移动目录。所有设计、任务和验收文件随CLI归档保留。交付仍为本地提交，不push、不deploy。
