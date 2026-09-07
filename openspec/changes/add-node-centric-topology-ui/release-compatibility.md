# Local release compatibility gate — 2026-09-07

## Scope and decision

**PASS within the user-defined local deployment/development tree.** 用户明确相关Relay Station项目全部位于当前父目录，并指定当前本地文件为本轮source of truth。以此边界完成consumer enumeration；没有以远端状态或未获证明的全球外部消费者不存在作为结论。没有发现不兼容或来源不明的Control Binding HTTP consumer，没有发现配置引用未纳入本地树的Binding client。1.3可关闭；与已有实现验收逐项对账后7.1可关闭，27/27。等待Node-centric Topology Final Release Gate Review，不部署、不归档。

## Actual local projects

父目录：`/Users/keedle/workspace/relay-station`。下列相对路径拼接该父目录即绝对路径；没有名为relay-station-*的同级目录，按实际目录识别：

| 项目/目录 | 相对路径 | branch | 扫描开始status | 职责 |
| --- | --- | --- | --- | --- |
| Control | control/ | main | clean | 管理API、内置Web、read models |
| Gateway | gateway/ | main | clean | Sub2API数据面、原生管理API、Directory source |
| Relay Node | node-cliproxyapi/ | main | M AGENTS.md（既有） | CLIProxyAPI |
| Ops | ops/ | main | clean | 部署/验收/系统设计/版本资料 |
| 配置审计 | config-audits/ | 非Git目录 | 不适用 | 本地工具与配置修改审计备份，不是服务 |
| 辅助目录 | internal/ | 非Git目录 | 不适用 | 无文件，无客户端 |

父目录AGENTS.md是工作区索引，不是consumer。其他仓库与node既有AGENTS修改未触碰。ops/dev/compose.yaml、ops/phase0/compose*.yaml及ops/releases/compatibility.yaml覆盖本地Control/Gateway/Node与PostgreSQL、Redis、TLS/secret初始化辅助服务；辅助服务没有Binding调用。镜像来源/上游repository URL是本地配置文本，未访问远端。runtime目录挂载用于配置、证书、secret与数据，不加载未识别Binding客户端。

## Search method

扫描tracked、untracked、hidden和ignored源文件，保留scripts/tools/deploy/acceptance/Docker/Compose/CI/docs/fixtures/test clients；未发现Terraform或Ansible额外消费者。排除Git对象、第三方依赖和可再生build/bin/dist、缓存及test-results，不将编译产物中的重复字符串当成额外消费者。非文本资源逐项检查为图片、OS元数据，以及Gateway的source-freeze归档。归档`gateway/openspec/changes/add-openai-compatible-prompt-audit/source-freeze/aicodex-prompt-audit-untracked.tar.gz`额外以内存只读方式扫描全部24个成员（含AppleDouble元数据），无Control identity/Binding/base-URL命中；它是prompt-audit参考源码，不是本地部署的另一个Binding客户端。

在父目录使用：

```sh
rg --files --hidden --no-ignore control gateway node-cliproxyapi ops config-audits internal \
  -g '!**/.git/**' -g '!**/node_modules/**' -g '!**/vendor/**' \
  -g '!**/dist/**' -g '!**/build/**' -g '!**/bin/**' \
  -g '!**/.venv/**' -g '!**/__pycache__/**' -g '!**/test-results/**'
```

随后对可读内容交叉检索：`gateway_account_id|GatewayAccountId|gatewayAccountId|new_gateway_account_id`、`relay-bindings|relay_bindings`、bind/rebind/unbind与generated operation names；扩大到`CONTROL_`、Control base URL/endpoint、account/candidate、fetch/curl/HTTP/JSON解析与路径构造。数字危险点检索`Number(`、`parseInt(`、`parseFloat(`、一元+、Go float64、JSON number、shell $((、jq tonumber、SQL numeric/integer/bigint casts；逐项根据调用的endpoint归属分类，不把端口、计数、Gateway原生API或DB numeric误判成Control Binding消费者。敏感配置只记录路径/键与用途，不复制值。

同一排除规则下，扫描文件数：Control 560、Gateway 3712、Node 1374、Ops 47、config-audits 75、internal 0（本报告写入前）。数字处理命中文件分别76/664/78/2/0/0；Control identity/Binding operation/path强关联29文件，其余项目0。宽搜索的Control base URL、curl及文档引用另按下表核对。

## Breaking HTTP surfaces

全部旧表示为JSON number，新表示为canonical positive decimal string；原nullable/optional语义保留，null不变成0。共享schema：api/openapi.yaml的GatewayAccountId、GatewayAccountContext、RelayNodeGatewayAccountBindingDetail、NodeRelayBindingResponse、GatewayAccountCentricBindingItem、BindRelayNodeRequest、RebindRelayNodeRequest。

| Method | Endpoint | request/response及JSON路径 | number → decimal string |
| --- | --- | --- | --- |
| GET | /api/relay-bindings/nodes/{instance_id} | response gateway_account_id；current_binding.gateway_account_id；account_context.account_id | 全部，非空分支string |
| GET | /api/relay-bindings/gateways/{instance_id} | response accounts[].gateway_account_id；accounts[].account_context.account_id；accounts[].current_binding.gateway_account_id | 全部；该接口就是Directory/account/candidate只读projection，没有独立candidate endpoint |
| GET | /api/relay-bindings/unresolved | response items[].current_binding.gateway_account_id；items[].last_known_account_context.account_id | 全部 |
| POST | /api/relay-bindings/bind | request gateway_account_id；response binding.gateway_account_id（共享DTO也含previous_binding.gateway_account_id） | 全部 |
| POST | /api/relay-bindings/rebind | request new_gateway_account_id；response binding.gateway_account_id、previous_binding.gateway_account_id | 全部 |
| POST | /api/relay-bindings/unbind | request只有relay_node_id UUID，不变；response previous_binding.gateway_account_id（共享DTO的binding也string） | response改变；request无Account ID |

证据：`internal/api/relay_binding_handlers.go:82,132,231,270,421,442,453`进行FormatInt/严格ParseInt；`web/src/api/generated/control.ts:884`定义GatewayAccountId=string，六字段引用/nullable string见887/919/944/955/993/999。generated fetch的JSON.parse不会把带引号ID变number，JSON.stringify保留string。server拒绝旧numeric body及overflow，无number|string双轨。

**Gateway Directory source v1不属于上述breaking surface**：Gateway `GET /internal/v1/api-account-directory`提供numeric accounts.id，Control gatewaydirectory driver用Go int64接收，原persistence不变。Gateway原生`/api/v1/admin/accounts`及其JS numeric客户端也不属于Control Binding API。Topology没有新增Gateway Account ID独立投影或mutation责任，直接使用既有Binding read。

## Consumer classification

A = Not a Control Binding HTTP consumer；B = Compatible；C = Incompatible；D = Ambiguous。负向测试刻意提交number并断言400属于B验收，不是不兼容应用。

| 项目/文件或同职责命中组 | 分类 | endpoint/行为与证据 |
| --- | --- | --- |
| control/web/src/api/generated/control.ts | B | 全六个Binding operations的DTO为string，request/response JSON原样序列化；包括候选/嵌套context/current/previous |
| control/web/src/api/topology-api.ts:13；topology-types.ts；pages/TopologyView.tsx:166 | B | generated getNodeRelayBinding，Account ID仅字符串展示，GET-only，无数值转换 |
| control/web/src/api/gateway-account-identity.test.tsx:13 | B | generated candidate→React string option/state→bind/rebind body，四个冻结值精确相等 |
| control/web/e2e/topology.spec.ts:21；pages/TopologyView.test.tsx:75 | B | Binding read合成fixture、last_known与max-int64字符串显示；无numeric identity消费 |
| control/internal/api/relay_binding_http_integration_test.go:796 | B | httptest真实Binding HTTP全链，Go边界DTO string；DB对照int64；非法numeric/overflow是预期400的负向fixture |
| control/internal/api/gateway_account_identity_test.go:14 | B / A source段 | source numeric→Go int64→HTTP string→request string→strict parse四值逐字一致 |
| control/api/openapi.yaml；internal/api/api.gen.go、relay_binding_handlers.go；tools/openapi_contract_test.go:465 | A（服务端/契约定义） | 不是额外consumer，定义/验证所有read/write string与拒绝numeric |
| control/queries/relay_node_gateway_account_bindings.sql；internal/store/relay_node_gateway_account_binding.go、sqlc/models.go、sqlc/relay_node_gateway_account_bindings.sql.go | A | DB/internal int64；SQL bigint cast接收已由handler严格解析的int64，不是把HTTP string转JS number |
| control/migrations/00011_*；internal/store/*binding*test.go、gateway_directory_migration_acceptance_test.go、cross_node_duplicate_ownership_query_schema_integration_test.go | A | DB/schema/audit JSONB测试；float64/JSON number命中属于内部audit数值或断言，不消费Web wire identity |
| control/internal/drivers/gatewaydirectory及Gateway Directory ingestion/store/tests | A | source v1 numeric Go int64链路，保留原契约 |
| control/openspec/specs与archive Binding文档；当前change的design/tasks/planning；Binding/Topology Runbooks | A（文档） | 历史numeric契约不是可执行consumer；当前Runbook已明确string请求/响应，无仍提交numeric的curl例子 |
| control/deploy/acceptance、cmd、web/e2e/authentication.spec.ts、playwright.config.ts、docs/evidence及auth/jobs Runbooks | A（其他Control API） | CONTROL_* base URL、curl、shell算术命中用于auth/health/jobs/account-inventory验收，不调用Binding；没有jq/float64处理Binding HTTP identity |
| gateway/backend/internal/server/routes/directory.go:12；openspec/specs/api-account-directory/spec.md:8 | A | 提供source v1，不调用Control；routing不读取Control Binding |
| gateway/skills/sub2api-admin/scripts/sub2api-admin.js:92,193,215 | A | SUB2API_BASE_URL与/api/v1/admin/accounts；parseIds的JS number属于Gateway原生管理API，与Control HTTP无调用链 |
| gateway其余routing/service/frontend/scripts/deploy/CI/tests | A | 无Control Binding/candidate/client命中；数字处理用于自身API、分页、时间等 |
| node-cliproxyapi全树（含examples/plugins/tests/deploy） | A | 无Control Binding/candidate endpoint、Control SDK或身份字段；原生Node职责，不依赖Control Binding |
| ops/dev/devctl:271；ops/dev/compose.yaml:111,144；phase0脚本、releases/compatibility.yaml与系统设计 | A | Control healthz、镜像/依赖/架构资料；没有Binding curl/jq、monitoring client或动态Binding URL |
| config-audits/ | A | 工具配置备份和回滚/verify脚本；Control URL及旧命令是审计资料，不是运行时Binding client |
| internal/及父目录AGENTS.md | A | 无客户端文件/仅目录索引 |

Compatible consumers：Control内置generated client、Topology read adapter/UI、前后端测试客户端。**C：0；D：0。** 未发现其他本地Binding消费者；没有遗漏待识别Relay项目或指向未知Binding service/client的本地配置。上游仓库地址、镜像来源和工具审计中的外部路径不等于Control HTTP consumer。该完整性判断仅针对用户冻结的当前本地体系，不推广到未来新增集成。

## Evidence reconciliation

与planning-validation.md的原始命令/结果交叉核对，不重新运行昂贵测试。实现对应：H=history SQL/store+topology_handlers，P=00017+provider query/store/handler，I=OpenAPI+Binding handler+generated Go/TS，W=TopologyView/hooks/API。

| Matrix | implementation | fixture / assertion reference | command/result reference |
| --- | --- | --- | --- |
| D1/D2 | H | cross_node_duplicate_ownership_read_model_test.go:26,101，A/B ACTIVE与absence后RESOLVED/current移除 | planning Commands实际PG Store组，PASS |
| D3/D4/D5/D6 | H | 同文件128/192，空current、absence/degraded evidence、去重、retention后新repo仍历史可见 | 同Store组PASS |
| D7 | H/W | 同文件225；topology_http_integration_test.go:132；e2e/topology.spec.ts:33,52，status/node绑定cursor和独立分页 | PG Store/API及Chrome既有PASS |
| P1/P2/P3/P6；P-READ A/B/C/F/G/H | P/W | account_inventory_provider_state_schema_integration_test.go:146,195；topology_http_integration_test.go:112；TopologyView.test.tsx:62，零account、缺state、并集、fresh/stale与health组合、双时间 | PG Store/API与组件、Chrome既有PASS |
| P4；P-READ E | P/W | provider rollback test:13；topology_http_integration_test.go:156；TopologyView.test.tsx:97，函数缺失/error/deadline→503、不返回空集、可重试 | PG Store/API、组件、Chrome既有PASS |
| P5；P-READ J | P | account_inventory_provider_state_boundary_integration_test.go:11，在同statement设置15min/+1ms并读；00017 STABLE及query单SQL保证集合/时间一致；既有readonly-query事务回归补充证据 | PG边界与已有committed promotion/rollback组PASS；不声称新增Provider并发压测 |
| P7 | 既有ownership不变 | IndependentFromNodeLocalDuplicates、OwnershipLifecycle，独立eligibility与生命周期 | planning既有PG回归22.322s PASS |
| P8；P-READ L | P | 00017九字段/固定reason且不依赖poll；topology_http_integration_test.go:112,156，字段allowlist/raw-error canary；tools/openapi_contract_test.go:465 | PG/API及tools既有PASS；retention独立性另由SQL依赖审查证明 |
| P-READ D/K | P | account_inventory_provider_state_schema_integration_test.go:13,96，runtime权限、PUBLIC、catalog、Down/Up、原account定义/ACL/数据不变 | 实际PG PASS |
| I1/I2/I9 | I/W | gateway_account_identity_test.go:14；relay_binding_http_integration_test.go:796；gateway-account-identity.test.tsx:13，四值分段贯通exact round-trip | PG/API/前端既有PASS；source v1未改 |
| I3/I4 | I | HTTP test:796非法/overflow/numeric→400，无成功audit | 同PG API PASS |
| I5/I6 | I/W | 六schema、generated types、HTTP read/candidate/mutation断言及本次全本地consumer数字处理清点 | tools/API/前端既有PASS；本轮只读清点PASS |
| I7/I8；R2 | I | HTTP旧numeric拒绝；既有Binding auth/CSRF/concurrency/audit/immutability；DB/internal保持原表示 | 原完整HTTP/Store回归PASS，成对升级规则保留 |
| R1/R4 | W | e2e/topology.spec.ts五步骤；TopologyView.test.tsx:33,75,87,97,107,119，GET-only、UUID、乱序、401、503、四态、桌面/390px | 组件与Chrome既有PASS，截图已人工审查 |
| R3/R5 | 全change | Final Review APPROVED；本地consumer清点及本文件，原make test build/strict/diff记录 | 原完整验收PASS；本轮仅文档strict/diff，未发布 |

各行的具体go/npm命令、fixture类型、执行结果与耗时保留在planning-validation.md Commands and results。此次只补引用与消费者边界，没有把synthetic HTTP/浏览器fixture写成生产服务验收，没有以测试skip代替真实PG结果。没有发现需重新开发或补跑业务测试的证据缺口。

## Recommended release sequence (not executed)

1. 确认本地consumer均支持decimal string（本次完成）。
2. Control backend与generated/embedded Web打包为同一release；禁止number|string双轨。
3. 经Final Release Gate Review及部署授权后部署Control，应用获准P-READ query-access migration。
4. smoke Node/Gateway/unresolved Binding reads，核对嵌套ID均string。
5. 仅在已有安全测试环境smoke bind/rebind；Topology仍不提供mutation。
6. 验证9007199254740993经过读/写/server int64逐字不变，同时保持既有overflow拒绝。
7. source v1 numeric ingestion regression，schema_version=1不变。
8. metrics/API/UI smoke，包括Provider双维度、history与unavailable；回滚API/Web成对并保留forward schema。

本轮未执行上述部署/smoke，不push，不archive。新增本地文档提交后等待 **Node-centric Topology Final Release Gate Review**。

## Documentation validation

在Control仓库内执行`openspec validate add-node-centric-topology-ui --type change --strict --no-interactive`和`git diff --check`均PASS；提交前额外运行`git diff --cached --check`覆盖新增文件。未重复执行业务测试。
