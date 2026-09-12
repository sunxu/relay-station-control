## Implementation plan

以下均为未来获得apply授权后执行的任务，单项≤2h；本轮不执行任务。每项输出对应测试或evidence，不把planning完成当作implementation完成。

## Baseline / contract reconciliation

- [ ] 1. 以Stage1/2实际实施结果复核schema/Driver/auth基线，输出source→contract映射及scope证据（≤2h）。
- [ ] 2. 建立MODIFIED exact-heading和baseline/Stage1/Stage2 scenario/body合成检查，输出可复核diff；先Stage2 archive再Stage3（≤2h）。
- [ ] 3. 冻结测试fixtures（active/retired/current/future/取消/history及操作actor），输出隔离PG18 fixture与cleanup说明（≤2h）。

## Spec and API contract

- [ ] 4. 在OpenAPI声明四个精确routes（GET health、POST connection-test、POST monitoring-enable、POST monitoring-disable）、body上限/unknown-field策略、GET vs POST差异化安全（Health无CSRF、其余三个POST需CSRF）与错误映射，输出schema contract测试（≤2h）。
- [ ] 5. 声明ProbeResult和MonitoringCommandResult完整字段/enum/nullability；ProbeResult由Health/Connection Test共享单一模型，输出响应fixture与无raw字段检查（≤2h）。

## Monitoring persistence / reason migration

- [ ] 6. additive扩展reason/end_reason/cancel_reason三个allowlist，输出PG18旧row保持不变的migration验收（≤2h）。
- [ ] 7. 扩展audit action allowlist为node.health/node.connection_test/node.monitoring_enable/node.monitoring_disable，probe details固定包含canonical instance_id/result/reason/latency_ms并保持旧category/result/actor约束，输出合法/非法shape PG18测试（≤2h）。
- [ ] 8. 为product Enable/Disable建立最小受控函数/ACL，输出runtime/PUBLIC direct-write拒绝与migrator owner/search_path证据（≤2h）。
- [ ] 8a. 增加`asset_admin_command_receipts_node_disable_fence_idx` UNIQUE partial expression index`(sanitized_result.instance_id, committed_at DESC) INCLUDE(command_id)`及bounded latest-fence helper，输出同一 Node timestamp tie拒绝、EXPLAIN命中index、ORDER BY committed_at DESC LIMIT 1和无全表扫描证据（≤2h）。
- [ ] 8b. 在Node lock下实现Disable receipt committed_at=`max(clock_timestamp(),previous+1 microsecond)`严格单调分配、finite/overflow fail-closed及同Node timestamp tie拒绝，输出D2先启动但后序列化、forced same-clock和多Disable PG18测试（≤2h）。
- [ ] 9. 验证Stage2 cancellation guard对administrator_disable一次写入/禁止uncancel/empty range，输出future历史保留测试（≤2h）。
- [ ] 10. 将受支持operational writer更新为intent形成时捕获nullable F0、READ COMMITTED写事务内显式VOLATILE受控函数在Node lock后读取F1，撤销/删除旧5参数函数EXECUTE，并保持lifecycle/monitoring既有preconditions与`node_generation`无关，输出ACL及旧入口fail-closed测试（≤2h）。
- [ ] 10a. 以PostgreSQL 18证明VOLATILE函数在Node lock等待后的下一内部SQL fresh snapshot可见先commit Disable receipt，并验证mismatch映射SQLSTATE 55000/monitoring_disable_fence_conflict且旧intent不自动重试，输出双向事务时序证据（≤2h）。
- [ ] 10b. 复用/加固Stage1 receipt immutable guard与ACL，验证node.monitoring_disable receipt拒绝UPDATE/DELETE/TRUNCATE且支持部署不prune latest fence，输出F0/F1不会因receipt移除false-equal的回归证据（≤2h）。

## Receipt / canonical intent

- [ ] 11. 注册monitoring_enable/disable command kinds并复用shared receipt helper，输出无新receipt subsystem的schema/调用检查（≤2h）。
- [ ] 12. 实现两种精确canonical array及SHA256，输出逐字节fixture/hash golden和UUID normalization测试（≤2h）。
- [ ] 13. 实现完整success/no-op persisted projection，输出原status/body回放fixture而非当前asset重构（≤2h）。
- [ ] 14. 验证auth→actor→intent→domain lookup顺序及bounded UUID PK查询，输出跨actor/跨action/Node冲突测试（≤2h）。

## Connection Test transport

- [ ] 15. 实现短Node read/authorization事务与registry target/capability校验，共享于GET Health与POST Connection Test两个route，输出retired/unsupported/DB失败零outbound测试（≤2h）。
- [ ] 16. 接入既有Driver.Probe，输出HTTP-only fixed base-path/healthz、无Secret resolver调用测试（≤2h）。
- [ ] 17. 接入安全response映射与latency边界，输出非200/invalid/oversize/timeout/取消/error taxonomy测试（≤2h）。
- [ ] 18. 验证无proxy/redirect/retry/fallback/客户端header透传，输出请求计数和canary negative evidence（≤2h）。
- [ ] 19. 分别实现node.health与node.connection_test两条独立sanitized observation audit与失败503处理，输出每条均含canonical target instance_id、无health-history/receipt、两action不混淆计数及不重做probe测试（≤2h）。
- [ ] 19a. 实现GET Health route的安全中间件（session+super_admin，MUST NOT要求CSRF/same-origin），并验证其与POST Connection Test（CSRF-protected）行为差异，输出401/403/csrf_invalid对照测试（≤2h）。

## Monitoring transactions

- [ ] 20. 实现Enable Node→boundary→current/future锁与future优先冲突，输出enabled/already_enabled/future冲突PG18测试（≤2h）。
- [ ] 21. 实现Disable同boundary关闭current（含已有planned close），输出end metadata及zero-length冲突测试（≤2h）。
- [ ] 22. 实现Disable所有future durable cancellation，输出current+future/仅future/仅current counts与全量处理测试；验证future-only为真实domain mutation（audit/receipt保留）但generation unchanged（≤2h）。
- [ ] 23. 接入`node_generation`按冻结规则精确推进（Enable建立current/Disable关闭current各+1一次；already_enabled/already_disabled/仅取消future无current变化/receipt replay/Health/Connection Test均不推进），输出Node revision/updated_at不变及overflow整事务rollback测试（≤2h）。
- [ ] 24. 实现already_disabled receipt-only和retired新command conflict；验证no-op Disable仍提交新receipt fence但无虚假transition audit/interval/generation（≤2h）。

## Store / OpenAPI / handlers

- [ ] 25. 编写monitoring sqlc query adapter和事务错误翻译，输出SQLSTATE不透传、runtime最小权限测试（≤2h）。
- [ ] 26. 连接四个handler（GET health、POST connection-test、POST monitoring-enable、POST monitoring-disable）与requireSession/super_admin/CSRF（仅三个POST）/sameOrigin/no-store，输出401/403/400/404/409/503 contract测试（≤2h）。
- [ ] 27. 运行make generate刷新Go/TypeScript客户端，输出可复现生成diff，无手工generated编辑（≤2h）。
- [ ] 28. 接入body/query校验及固定ErrorResponse，输出重复字段/超限/额外时间和URL参数拒绝测试（≤2h）。

## UI

- [ ] 29. 在现有active Node detail增加两个独立显式控件——Health（GET观察）与Connection Test（POST admin action），输出mount/reload零probe、两控件互不混淆结果与计数的组件测试（≤2h）。
- [ ] 30. 增加immediate Enable/Disable控件及取消future确认，明确Disable不建立durable disabled latch，输出无schedule picker/credential编辑检查（≤2h）。
- [ ] 31. 接入command UUID稳定重试和current detail/list刷新，输出no-op/replay/conflict/unknown outcome组件测试（≤2h）。
- [ ] 32. 验证retired无可执行operations、切换Node取消旧请求、迟到response隔离，输出UI regression evidence（≤2h）。
- [ ] 33. 验证/assets与/assets/直达和reload、lazy chunks及既有Gateway/Node lifecycle控件，输出authenticated E2E稳定selector证据（≤2h）。

## Audit / metrics / security

- [ ] 34. 接入asset_node audit allowlist（含node.health/node.connection_test/node.monitoring_enable/node.monitoring_disable四个action），输出success一条/no-op与replay零transition、Node A/B同结果仍按instance_id可归属、同Node两probe action可区分及失败probe脱敏测试（≤2h）。
- [ ] 35. 接入shared mutation metric family，以及两个独立probe metric family——`control_asset_health_total`（Health专用）与`control_asset_connection_test_total`（Connection Test专用），输出低基数labels和healthy/timeout/failed映射、两个family互不计入对方测试（≤2h）。
- [ ] 36. 执行raw Secret/body/URL/IP/error canary扫描，输出API/audit/receipt/log/metrics无泄漏证据（≤2h）。

## Race / restart / replay

- [ ] 37. 验证two Enable/two Disable（不同command_ids）串行化，输出interval/receipt/audit/generation精确计数（≤2h）。
- [ ] 38. 验证Enable与Retire/Replace双向锁顺序，输出old终态无monitoring与new无继承证据（≤2h）。
- [ ] 39. 验证Disable与Retire/Replace双向锁顺序，输出原administrator reason保留和retired冲突证据（≤2h）。
- [ ] 40. 验证writer先commit时Disable随后原子取消已提交future；Disable先commit时此前捕获F0并等待Node lock的旧intent因F1变化固定conflict且零insert，输出双向race及receipt/index证据（≤2h）。
- [ ] 40a. 验证already-disabled Disable在旧writer等待时仍提交fence并使其conflict，同时Disable commit后才形成的新intent捕获最新F0并按既有preconditions成功，证明无durable latch（≤2h）。
- [ ] 40b. 验证process/session restart丢弃未提交F0/intent、旧5参数入口fail closed、bounded fence lookup无退化，并输出restart/race evidence（≤2h）。
- [ ] 40c. 验证writer以Disable A为F0、B/C连续commit后F1严格选择C并conflict；强制接近/相同时钟不得依赖UUID，输出strict monotonic ordering与零activation证据（≤2h）。
- [ ] 41. 验证Health/Connection Test与Retire/Replace/Edit双向授权race，输出DB锁未跨HTTP、一次固定target观察证据（≤2h）。
- [ ] 42. 验证same-command并发、actor/intent conflict、commit响应丢失和no-op后状态变化replay，输出原body/无重复audit证据（≤2h）。
- [ ] 43. 验证Disable半途crash/receipt或audit失败/重启跨future时间，输出原子rollback和永久ineligible evidence（≤2h）。
- [ ] 44. 验证按冻结规则触发的monitoring mutation引起cursor_stale（含"仅取消future无current变化不推进generation"这一分支）、pure clock read_as_of、Inventory eligibility与history/coverage保留，输出回归证据（≤2h）。

## Build / acceptance / readiness

- [ ] 45. 扫描实际Stage2 class2证明Control不调用旧operational函数、不会以receipt committed_at解释冲突业务语义且forward receipt可读；产生Stage3 Disable evidence后停止/排空Control及writers并经正式wrapper回退，验证无pre-Disable intent跨restart、forward fence-aware script可创建新的post-rollback intent、旧5参数入口fail closed及read/reconcile/Retire/Replace兼容，输出digest/marker证据；失败阻断release不升class3（≤2h）。
- [ ] 46. 执行PG18 migration/ACL/generation/cancellation验收，输出隔离数据库证据与无历史改写检查（≤2h）。
- [ ] 47. 执行targeted API/Driver/Store/UI tests与production build，输出lazy chunk与API regression证据（≤2h）。
- [ ] 48. 运行make test build及全量OpenSpec strict，输出生成可复现与全部测试结果，失败只按已批准scope修复（≤2h）。
- [ ] 49. 更新operation runbook、secret-free smoke/回滚流程和mutation/probe失败解释，输出Runtime Acceptance evidence索引（≤2h）。
- [ ] 50. 逐个MODIFIED复核exact title、完整baseline正文/scenarios、Stage1/2合成与Stage3 additive自包含结果；执行git diff --check、approved-scope/durable-truth/generated复现/测试证据/authorized worktree reconciliation，输出archive readiness awaiting review证据（≤2h）。

completed implementation tasks = 0
Independent readiness review = PASS
P0 = 0
P1 = 0
P2 = 0
Planning readiness = PASS / READY
Implementation readiness = READY
openspec apply = NOT AUTHORIZED / NOT RUN
Implementation = NOT STARTED
Runtime Acceptance = NOT STARTED
