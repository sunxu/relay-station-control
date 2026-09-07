## Implementation status

2026-09-07：Architecture Contract（含原三项与P-READ）已Final APPROVED，用户随后明确授权实施。本轮已实现安全读取、HTTP身份修正及只读Topology，完成下列隔离验证。用户随后授权分批保存已验证实现；未生产部署或归档。

仓库外实际 Binding HTTP 消费者尚未确认；1.3 与相关最终发布门禁保持未勾选，不把仓库内检索当成外部消费者确认。源码内已统一HTTP读写为string；存在稳定外部兼容承诺时，必须停止发布并回到版本化兼容契约评审。

## Self review

| # | 问题 | 实现与证据 |
| --- | --- | --- |
| 1 | B从current删除后能否看RESOLVED history？ | 可以；独立history query对append-only evidence做EXISTS。实际PG的D2断言A/B均可查到，B current不包含。 |
| 2 | 是否新增第二套membership history truth？ | 否；没有history表、membership复制、索引或相关migration。 |
| 3 | fresh snapshot与latest degraded可否同时表达？ | 可以；双badge和独立时间，HTTP/组件/Chrome均验证。 |
| 4 | 是否修改Duplicate Ownership eligibility？ | 否；ownership写入/eligibility实现未改，独立于node-local duplicate与生命周期回归通过。 |
| 5 | >2^53−1是否无损？ | 四个冻结值通过source Go解析、真实PG HTTP、generated TS + React state + request分段贯通验证；overflow拒绝400。 |
| 6 | TS中Gateway Account ID是否仍number？ | 否；六个HTTP schema及嵌套引用均string（可空字段string/null）。 |
| 7 | 是否有Number/parseInt/parseFloat转换？ | Account ID路径没有；候选测试使用原始string option/state，生成客户端接收带引号JSON。 |
| 8 | read/write表示是否一致？ | 所有Control/Web read/candidate/detail/bind/rebind/解绑response均decimal string；internal int64、audit JSONB和source v1 numeric不变。 |
| 9 | Topology是否read-only？ | 是；只调用GET；账号清单只含Node UUID深链，不新增或调用Binding mutation/候选submit。Chrome全请求断言通过。 |
| 10 | migration决策？ | 0 persistence migration；P-READ 允许一个最小 additive readonly query-access migration。实际新增00017，仅在隔离DB执行Up/Down。 |
| 11 | 0 account Provider是否可见？ | 可以；Expected UNION Held独立集合，真实零账号、缺state与out_of_scope组合验证通过。 |
| 12 | health时间/reason是否来自safe read？ | 是；九字段函数→sqlc→runtime store→HTTP，固定reason，未返回poll ID/raw payload。 |
| 13 | runtime是否仍无direct SELECT？ | 是；真实runtime EXECUTE通过，provider_states/account_inventory直接SELECT返回42501；PUBLIC/registrar无新函数EXECUTE。 |
| 14 | 是否只有query-access migration？ | 是；Up仅函数/owner/EXECUTE ACL，Down仅DROP该uuid签名且无CASCADE；隔离回滚检查既有account函数定义、ACL和数据行数不变。 |

## Acceptance evidence matrix

以下使用合成账号/Node和独立 PostgreSQL 18容器。HTTP测试使用真实认证数据库和httptest，其中Topology新read使用可控reader验证error/projection；Store测试另行调用真实runtime安全函数和SQL。浏览器使用真实生成客户端、真实Chrome和合成HTTP route，不声称对生产服务运行验收。

| 契约 | 实际证据 |
| --- | --- |
| D1–D2 | `internal/store/cross_node_duplicate_ownership_read_model_test.go`：ACTIVE current与history、fresh absence后A/B RESOLVED history、B membership删除。 |
| D3–D6 | 同一PG测试：current集合全空、多个evidence去重、absence/degraded历史涉及、source_poll_run_id清空后新Repository仍读历史；没有把historical involvement当confirmed owner。 |
| D7 | history SQL status/keyset/limit验证；`internal/api/topology_http_integration_test.go` Node/status绑定cursor；旧current HTTP回归；Chrome实际Current/History/Evidence分页和Resolved筛选。 |
| P1–P3、P6、P-READ A/B/C/F/G/H | `account_inventory_provider_state_schema_integration_test.go`：零account、Expected缺state、Expected UNION Held、fresh/stale分别与normal/degraded组合、health时间保留。HTTP验证snapshot与health两个时间；UI双badge截图与组件测试。 |
| P4、P-READ E | migration回滚后真实query失败；HTTP reader模拟DB错误/deadline返回503且无providers/secret；组件局部503不变empty，Chrome刷新恢复。 |
| P5、P-READ J | `account_inventory_provider_state_boundary_integration_test.go`在同一SQL statement内设置并读取15min与15min+1ms边界；函数STABLE及集合查询统一statement_timestamp。并发可见性沿用PG statement snapshot；既有readonly-query committed promotion/rollback回归通过。 |
| P7 | `TestCrossNodeDuplicateOwnershipIndependentFromNodeLocalDuplicates`、`TestCrossNodeDuplicateOwnershipLifecycle`实际PG回归；ownership eligibility代码未改。 |
| P8、P-READ L | Provider函数直接读取retention-safe state，不join poll；HTTP九字段allowlist、OpenAPI schema字段测试和raw-error canary验证。固定reason来自state，禁止credential/raw/current_poll_run_id字段。 |
| P-READ D/K | 实际migrator/runtime隔离ACL、函数catalog（owner/STABLE/SECURITY DEFINER/search_path）、Down/Up-by-one以及旧account函数定义/ACL/数据不变检查。没有执行生产down。 |
| I1–I2、I9 | `internal/api/gateway_account_identity_test.go`真实Directory v1 numeric parser；`TestRelayBindingHTTPCompleteSuite/Lossless_decimal_int64_HTTP_lifecycle`真实DB/HTTP；`web/src/api/gateway-account-identity.test.tsx`实际generated candidate→React string option/state→generated bind/rebind序列化。四值逐字对比，不以JS numeric构造fixture。 |
| I3–I4 | 同一HTTP测试拒绝overflow 9223372036854775808、numeric JSON、小数/指数、0、负数、+、前导0、空白/null；失败无成功audit或绑定状态变化。 |
| I5–I6 | `tools/openapi_contract_test.go`覆盖六个string schema和nullable；生成后检索全部Account ID TS引用，无number/Number/parseInt/parseFloat中转；HTTP read/candidate/detail/bind/rebind/unbind投影与existing tests。 |
| I7–I8、R2 | numeric请求明确400；既有Binding认证/CSRF/并发/事务/审计/目录不可变回归；store/domain/audit和source实现无变更。Runbook明确API/Web成对升级回滚；未执行生产发布。 |
| R1 | `web/e2e/topology.spec.ts`拒绝任何非GET/未知API，捕获URL不含account_key；页面无mutation/管理入口；账号清单仅Node UUID导航。 |
| R4 | Topology组件10项含四态Binding、last_known、大ID、未知Node、A→B迟到响应和AbortSignal取消、401清cache、503；Chrome实际分页/键盘/前进后退/reload/evidence401，390px和1280px无页面溢出；UTC和双badge截图人工检查。 |
| R3、R5 | P-READ与实施授权已取得；完整make test build及strict/diff检查通过。外部HTTP消费者清点仍是未解决发布门禁。 |

## Commands and results

命令在Control仓库运行，沿用原Go/TMP/cache环境（/Volumes/DevRAM），没有覆盖GOCACHE/GOTMPDIR/TMPDIR。真实PG专项设置CONTROL_DATABASE_TEST_URL与CONTROL_RUNTIME_DATABASE_TEST_URL指向本次隔离容器的migrator/runtime测试登录；不把owner凭据提供给产品进程。

- `make test build`：通过；含生成、Go全包、Web测试、typecheck/build及Go build。该调用未设置数据库测试URL，DB相关证据以下列显式专项为准，不能把skip算通过。
- 实际PG：`go test ./internal/store -run 'TestAccountInventoryProviderState|TestCrossNodeDuplicateOwnershipOccurrenceReadModel|TestAccountInventoryReadonlyQuery(StoreAndPermissionMatrix|RuntimeAndUnauthorizedPermissionMatrix|SeesOnlyCommittedPromotionsScopeAndRollback)$' -count=1 -v`：通过（9.255s），没有skip。后3组是既有deploy/acceptance/readonly-query PostgreSQL验收选用的权限与事务回归；未重跑无关50-Node容量或数据面验收。
- 实际PG：`go test ./internal/api -run 'TestRelayBindingHTTPCompleteSuite|TestTopologyHTTPReadContracts|TestCrossNodeDuplicateOccurrenceHTTPReadOnly|TestGatewaySourceV1ToHTTPIdentityPrecision' -count=1 -v`：通过（3.472s），没有skip。
- 实际PG：`go test ./internal/store -run 'TestRelayBindingRepository_|TestCrossNodeDuplicateOwnershipIndependentFromNodeLocalDuplicates|TestCrossNodeDuplicateOwnershipLifecycle$' -count=1 -v`：通过（22.322s），包含既有Binding并发与ownership状态机回归。
- Chrome：`CONTROL_E2E_BASE_URL=http://127.0.0.1:18080 CONTROL_E2E_PLAYWRIGHT_OUTPUT_DIR=/Volumes/DevRAM/tmp/topology-playwright npm --prefix web run test:e2e -- e2e/topology.spec.ts --timeout=45000`：通过，1个测试包含5组实际交互步骤（4.6s）。
- 截图为合成数据，保存在 `/Volumes/DevRAM/tmp/topology-390.png` 和 `/Volumes/DevRAM/tmp/topology-desktop.png`；已人工查看。表格在自身区域内横向滚动，Provider在390px使用同时展示两badge的卡片。
- `openspec validate add-node-centric-topology-ui --type change --strict --no-interactive`：通过。
- `git diff --check`：通过；未暂存文件另行检查行末空白，不以tracked diff掩盖新文件。

## Git and release scope

已按用户授权拆分本地提交，未push。仅Control仓库内本change相关OpenAPI/生成物、Go handlers/store/query、一条query-access migration、Web与测试/Runbook/OpenSpec；Gateway source、DB persistence schema与其他仓库未改。此前批准契约及三个规范文件的提交不回写。

不得把实现已验证等同发布完成或全change门禁关闭。仍需确认实际外部Binding HTTP消费者，随后按授权安排最终review及发布。没有以Topology便利为由引入任何mutation或source v2。


## Implementation checkpoint commits

按实际共享文件边界拆成四组，避免把同一OpenAPI及生成物中的身份修正与新operation拆成不匹配版本：

- `4f526e5 feat(inventory): add provider summary read access`：query-access migration、独立store/sqlc及ACL/回滚/语义测试。
- `f3a74af feat(api): add topology reads with lossless gateway identities`：共享OpenAPI/生成Go/TS、全部身份read/write边界、Provider HTTP与历史query/API、精度和后端契约测试。
- `39b6ab3 feat(web): add node-centric topology ui`：只读route/page/adapters/hooks及组件/Chrome验收。
- `docs(topology): add validation and runbook`：本验收记录、Runbook及OpenSpec状态同步。

每个commit前执行`git diff --cached --check`。本次只拆分提交和同步文档状态，没有改变已验证业务实现，不重复运行业务测试；文档重新执行OpenSpec strict及diff检查。外部消费者门禁继续保留，进度25/27，未归档。
