# Implementation and validation evidence

## Architecture reconciliation

用户授权本轮自主完成修订、实现、验证并归档；不部署。原Architecture Review的P1通过完整额度预算修订：请求15秒、落库10秒、生命周期回调30秒、每Node claim1秒及dispatch余量10秒；Worker保持全execute semaphore持有，并显式限制claim/observer context。50/C7不再声称合格，50/C25为专项。P2通过00019只读函数复用scheduler的inconsistent_count前置检查，异常返回unavailable。

同环境仍为单Control实例，不引入跨实例状态。0 persistence migration，00019仅query-access function。日志复用固定reason链路，代码核查无现成scheduler计数器，因此不新增计数器或高基数标签。

## Evidence matrix

| Gate | Implementation / fixture | Command / assertion | Result |
|---|---|---|---|
| Capacity | inventorypoll/config.go、config_test.go | 默认C1/2/7/10/25对应2/4/14/20/50；等号拒绝；完整预算单一来源 | focused PASS |
| Worker bounds | worker.go、runtime_test.go | claim 1秒、observer 30秒deadline；并发额度不提前释放 | PASS；真实慢Worker 50/C25专项PASS |
| Deprecated config | cmd/control/account_inventory_poll_test.go | 旧环境变量忽略，固定警告不包含原值 | focused PASS |
| Scheduler observability | inventorypoll/runtime_test.go、pollobservability/logging_test.go、cmd/control adapter日志测试 | capacity_exceeded保留、database failure不混淆、低基数白名单 | focused PASS |
| HTTP | account_inventory_poll_capacity_http_test.go，真实PostgreSQL隔离session | 401不调用reader；ready/exceeded/disabled；503脱敏；重启重建server；零account_inventory.view audit | PASS |
| Web | AccountInventoryCapacity.test.tsx、account-inventory-api.test.ts、AccountInventoryView.test.tsx | generated client、预选兼容、未选Node诊断、loading/失败隐藏旧值/恢复、401、无账号查询 | PASS |
| Store/ACL/recovery | 00019、query及Store integration fixture | 固定search_path/UTC、runtime EXECUTE/PUBLIC及registrar拒绝、provider_states direct SELECT拒绝、1个0账号Node真实计数、3>2零新增poll、去掉第三Node capability后2个Node幂等恢复、未来监控槽排除、策略缺失23514、Down只删除函数且poll历史保留 | PASS |
| Full build | make test build | Go tests、TS生成/typecheck、前端72 tests、生产build | PASS（最终完整命令再次通过，后续仅专项测试补强） |

真实API命令：

```sh
CONTROL_DATABASE_TEST_URL='postgres://relay_control_migrator:relay_control_migrator_dev_only@127.0.0.1:55432/relay_station_control?sslmode=disable' \
CONTROL_RUNTIME_DATABASE_TEST_URL='postgres://relay_control_app_dev:relay_control_runtime_dev_only@127.0.0.1:55432/relay_station_control?sslmode=disable' \
go test ./internal/api -run '^TestInventoryPollCapacityHTTP$' -count=1 -v
```

上述地址为README固定隔离测试角色；fixture创建独立数据库并自动清理，未使用运行环境55434。曾出现旧API路由白名单失败，已明确纳入新增GET端点而未放开其它路径，重新make通过。生成链已解决sqlc函数列推导，generated文件必须由make generate复现，不手工维护。

## Release boundary

Ops只变更dev/compose.yaml删除旧变量及OPERATIONS.md清理/回滚说明，另仓独立提交。持久runtime override清理属于未来部署步骤，本轮未改运行配置、未deploy、未push。旧镜像回滚需要同时恢复旧MAX_NODES/并发配置；不执行生产migration down。

## Final reconciliation

12/12 tasks完成。主Agent最终复核和独立API/read-model审查无剩余P1/P2；曾发现的预算、策略一致性和测试fixture问题均已修正，未放宽验收断言。Strict单项和全量通过，git diff --check通过；全部门禁已满足，可按用户授权archive。

## PostgreSQL专项命令与结果

均使用上述README固定测试URL（55432），每项fixture自行创建隔离数据库：

```sh
go test ./internal/store ./internal/api -run '^(TestAccountInventoryPollCapacityRuntimeReadIsSchedulerCompatible|TestInventoryPollCapacityHTTP)$' -count=1 -v
go test ./internal/store -run '^TestAccountInventoryPollCapacityExpiredRetryRemainsBounded$' -count=1 -v
go test ./internal/store -run '^TestInventoryPoll(RepositoryClaimFinalizeAndFencing|SchedulerPinsPolicyAndReconcilesWithStoredGrace)$' -count=1 -v
```

全部PASS，无SKIP。expired retry不可claim、Reconcile一次abandoned、重复Reconcile不重复终态且attempt保留1。原有lease/fencing和同槽策略固定测试通过。

Read-model验收明确使用owner仅创建合规隔离fixture（包括created_at与激活slot匹配），不修改数据库时钟；runtime执行实际函数。先前仅测试0 eligible不能证明0-account Node可见，已替换成明确1 eligible/0 accounts，再验证3→2集合恢复，未将弱断言当证据。

慢Worker第一次fixture不完整（策略有legacy/openai，Driver只回openai），被已有完整性校验拒绝；已复用完整Provider响应helper修正。没有修改生产SQL/校验以让fixture通过。

## Slow Worker final acceptance

```sh
CONTROL_POLL_CAPACITY_WORKER_ACCEPTANCE=1 \
CONTROL_DATABASE_TEST_URL='postgres://relay_control_migrator:relay_control_migrator_dev_only@127.0.0.1:55432/relay_station_control?sslmode=disable' \
CONTROL_RUNTIME_DATABASE_TEST_URL='postgres://relay_control_app_dev:relay_control_runtime_dev_only@127.0.0.1:55432/relay_station_control?sslmode=disable' \
go test ./internal/store -run '^TestAccountInventoryPollCapacityWorkerSlowDriver$' -count=1 -v
```

PASS，302.28秒（包含等待正常UTC槽）。实际测试使用50个Node、默认T15/F10/L30/Q1/G120预算、C25，Driver延迟14秒、真实Store finalize之前延迟9秒、LifecycleObserver延迟29秒；未把后者放进只剩短时限的日志Observer。等待正常槽前5秒内开始，不修改数据库时间。明确断言：fixture poll=50；dispatch=50；最大并发25；每次dispatch早于scheduled_slot+120秒；第二批首次dispatch距离第一批至少51秒；完成LifecycleObserver=50；finalized=50；非首attempt=0。过期retry由单独真实PG专项证明不可claim并只转一次abandoned，不把本成功专项说成发生了retry。

最终完整 `make test build` PASS，前端16个文件/72 tests PASS。再次 `make generate` 对所有generated Go/sqlc/TS做SHA256前后比较，完全一致。专项、完整构建日志为本次执行结果，未追认旧测试。

Ops关联提交：`4e4be1e chore(dev): remove manual inventory poll capacity`，仅Compose模板和运行说明，Ops工作树干净。运行中的Control/Node/Gateway均未部署变更，runtime override清理仍属于未来发布步骤。
