## Context

状态：**Architecture Contract（含P-READ及原三项契约）已Final APPROVED；用户已授权实施及分批提交，已完成本地实现验收。** 实施遵循下列冻结契约；本轮按授权保存实现提交；生产发布不在执行范围。

本文件替代上一版候选选择、绑定表单、`expected_binding_id`、单一Provider observation状态和全域摘要方案；实施仅允许P-READ专用query-access migration。依据 [proposal](proposal.md)、[系统设计](../../../../ops/docs/RELAY_STATION_SYSTEM_DESIGN_CN.md) §15/§24、[ADR-0001](../../../../ops/docs/adr/0001-control-technology-stack.md) 与 [ADR-0002](../../../../ops/docs/adr/0002-use-sub2api-native-downstream-scheduling.md)。最新用户的P-READ Architecture Contract Final Review决定优先，原三项批准契约保持不变。

事实索引：

- Current membership filter：[occurrence read SQL](../../../queries/cross_node_duplicate_ownership_occurrence_read_model.sql)；absence 后删除 membership：[lifecycle repository](../../../internal/store/cross_node_duplicate_ownership_lifecycle.go)。
- Provider 持久健康来源：[00009 migration](../../../migrations/00009_account_inventory_history_compaction.sql) 的 `control_refresh_account_inventory_provider_health_v1`；原表权限：[00007 migration](../../../migrations/00007_account_inventory_lifecycle_foundation.sql)。
- API 真相：[OpenAPI](../../../api/openapi.yaml)、[Go generated API](../../../internal/api/api.gen.go)、[TS generated client](../../../web/src/api/generated/control.ts)、[Binding handler](../../../internal/api/relay_binding_handlers.go)。
- 当前 Web 路由：[AuthContext](../../../web/src/auth/AuthContext.tsx)、[App](../../../web/src/App.tsx)。现有页面有 assets/jobs/account-inventory，**没有 Binding 管理 UI**；只有生成的 Binding API 方法，不能把它称为既有管理入口。

## Goals / Non-Goals

**Goals:**

- Topology 只读地展示 Node、Inventory evidence、Binding truth/resolution、current duplicate 和 historical involvement。
- 以既有 append-only evidence 查询历史，以既有 promoted state 和 health state 分别展示 freshness/health。
- 定义现有 Binding transport identity correctness fix，确保浏览器接触的全部 Account ID read/write 均无损，作为展示这些 ID 前必须完成的兼容性前置任务。
- 明确事实、拟定契约、已知权限/外部兼容性缺口和已批准的架构范围，不能用“已有 API”或 OpenSpec 校验冒充生产可用。

**Non-Goals:**

- Topology 不拥有 bind/rebind/unbind、候选选择/submit、account mutation、确认弹窗或写入恢复队列；不新增 expected_binding_id。
- 不改变已有 Binding cardinality/事务/锁/审计，不改变 Duplicate Ownership eligibility/detect/resolve/evidence truth。
- **0 persistence migration；P-READ 允许一个最小 additive readonly query-access migration。** 不新增表/列、复制历史membership、topology/provider summary persistence、materialized view或索引；不扩大runtime对provider_states的直接SELECT。
- 不建设不存在的 Binding 管理 UI；不重写 Sub2API 原生 Account/Group 管理界面，不扩大数据面职责。

## Decisions

### 1. Topology readonly boundary

页面 `/topology?instance_id=<UUID>` 复用既有认证、导航、React/Ant Design/React Query。列表以 Node 资产为主集合，保留未绑定、无账号或观测不可用的 Node。详情四区为 Inventory evidence、Gateway binding、Binding resolution、Duplicate ownership；明确 Ownership Fact 和 Gateway Usage Context。

Topology 模块仅调用 GET 读取接口，不导入/调用生成的 bind/rebind/unbind mutation 方法，不发送其他业务写请求，不触发 poll/ingestion/reconciliation。账号清单只 deep-link `instance_id` 到既有页面，其 POST query/view audit 留在原页面执行。现有 Binding 管理 UI 不存在，因此不渲染“管理绑定”链接；未来独立能力建立并验证入口后，Topology 才可加只含稳定 ID 的导航，不附带 action/payload，不替该页面提交。

目录 Account 列表只用于只读上下文，不能改造成可提交的候选选择器。Account 名称/URL 不用于身份匹配或推断 Node。HTTP transport correction 会触及既有 bind/rebind handlers 和生成 request DTO，但这是 **existing Binding transport identity correctness prerequisite**，不是 Topology 获得 mutation responsibility。

### 2. Duplicate historical involvement

| 概念 | 唯一来源 | UI / 查询语义 |
| --- | --- | --- |
| Current involvement | `cross_node_duplicate_occurrence_nodes` | ACTIVE/current 区，Node 当前仍在 affected set；按现有 `instance_id` filter |
| Historical involvement | append-only `cross_node_duplicate_occurrence_evidence` | Node 曾被该 occurrence authoritative evaluation 涉及；不要求目前仍为 owner 或 membership 仍存在 |

Historical involvement 使用：

```sql
EXISTS (
  SELECT 1
  FROM cross_node_duplicate_occurrence_evidence evidence
  WHERE evidence.occurrence_id = occurrence.occurrence_id
    AND evidence.instance_id = target_node
)
```

不额外要求 `observation_kind=owner_confirmed`，因为“曾被 authoritative evaluation 涉及”也包含 absence/degraded 证据，不等同“曾确认拥有账号”。UI 文案明确“历史评估涉及”，不能把被评估的 Node 宣告为历史 owner。

新增明确命名的 store read method：`ListCrossNodeDuplicateOccurrencesHistoricallyInvolvingNode`。拟定只读 HTTP：`GET /api/topology/nodes/{instance_id}/duplicate-history`，operationId=`listNodeDuplicateHistory`。不修改既有 `/api/cross-node-duplicate-occurrences?instance_id=...` 的 current-membership contract。

新 history 接口允许可选 `status=ACTIVE|RESOLVED`；省略时返回所有历史涉及的状态。UI “Resolved” 固定传 RESOLVED，“History” 不传；current 区仍调用既有 list 并传 ACTIVE。默认 limit=25，范围1–200，cursor 最长512，按 `(last_seen_at DESC, occurrence_id DESC)` keyset，与既有 occurrence 排序一致；cursor 绑定 target Node/status，筛选变更重置，非法返回400。同一 occurrence 多条 evidence 用 EXISTS 去重；不先把 evidence 分页后再过滤 occurrence，不需要预加载所有历史证据。

响应 `{involvement: "historical", instance_id, observed_at, items, next_cursor}` 复用 occurrence summary 字段。summary 的 `affected_nodes` 仍表示当前保留集合，不把它重命名为历史参与者；页面另以响应 involvement/target Node 说明为何该条属于本 Node 历史。若需查看其他涉及 Node，通过已有 evidence 独立分页展示，不能从当前 affected_nodes 补造历史集合。

使用已有 occurrence/evidence 表的 runtime SELECT 与 sqlc 查询，不新增 migration、history table 或 copied membership truth。单次列表一致视图，不锁写表；query timeout2s、HTTP5s、超时503，不返回部分成功。对现有 occurrence detail/evidence 不改检测、保留期或权限；source_poll_run_id 清理为空不影响 evidence 的 Node identity，历史查询不能 join poll 表才能成立。

### 3. Provider evidence dimensions

**snapshot freshness ⟂ latest health。** 使用独立Provider-state read model，完全保留已批准的两个badge、两个来源时间及不改变ownership eligibility的规则。现有账号查询只负责分页账号/lifecycle/identity/per-account projection，其 `control_query_current_account_inventory_v1` **signature、返回契约和实现均不修改**。

#### P-READ final readonly function contract

按现有 `control_query_*_v1` 命名惯例冻结：`public.control_query_account_inventory_provider_states_v1(target_instance_id uuid)`。这是已批准的函数契约。单次调用读取一个Node的完整Provider集合，按provider字典顺序返回，每个provider至多一行，不分页、不截断、不从account rows聚合。最小返回字段如下：

| 字段 | 类型/空值 | 来源与语义 |
| --- | --- | --- |
| `provider` | text，非空 | 完整Provider集合中的规范名称 |
| `monitoring_status` | text，非空 | 已有state直接保留active/out_of_scope；预期但未建state的Provider为active，仅表示监控范围 |
| `state` | text或null | 已有state原值current；尚未建立state为null，不写入not-yet-observed新状态 |
| `current_scheduled_at` | timestamptz或null | 已有promoted/current state字段；合成缺state行为null |
| `last_complete_at` | timestamptz或null | 最近完整promoted snapshot；合成缺state行为null |
| `snapshot_freshness` | fresh/stale/unknown/out_of_scope | 仅按state/current、monitoring_status、last_complete_at与既有15min阈值计算 |
| `health_scheduled_at` | timestamptz或null | 直接读取最新持久health时间；缺state为null |
| `health_degraded` | boolean或null | 直接读取最新持久health标志，缺state为null，禁止coalesce为false |
| `health_reason` | text或null | 只返回既有CHECK允许的固定枚举，缺state为null，不返回raw error |

health_reason允许值固定沿用 `none|transport_failed|contract_invalid|disk_fallback|node_identity_incomplete|identity_incomplete`，不重新解释/拼接外部错误。不返回current_poll_run_id、账号行/邮箱/account_key、raw poll payload、raw response/errors、Secret或credential；无需通过poll表取最新health，retention清理poll引用不影响读取。

#### Provider completeness source

在同一语句观察时点，对已登记Node确定两个集合：

1. **Expected**：经 `provider_inventory_policy_activations.active_range` 选择当前生效策略（按Node type/driver scope），关联不可变policy版本并展开 `active_providers`；Node同时处于 `relay_node_inventory_monitoring_activations.active_range` 时才属于当前应监控集合。不能只读未来/尚未激活policy binding。
2. **Held**：该Node的 `account_inventory_provider_states.provider` 全集，包括out_of_scope旧state，不因当前账号数或监控停用过滤掉既有state。

以 **Expected UNION Held** 作为主集合，再LEFT JOIN该Node/provider的state。禁止 `SELECT DISTINCT provider FROM account_inventory`，不JOIN账号表、不数账号，不从account query分页结果推导集合。即便openai为0 account rows，只要有state或在Expected中就必须返回。

Expected存在但state未建立是合法情况（首次promotion之前），不是账号缺失。返回provider、monitoring_status=active、state=null、全部snapshot/health来源字段null、snapshot_freshness=unknown；UI显示 **not-yet-observed**，health=unknown，不显示empty/fresh/healthy。该行是查询投影，不INSERT provider_state。Held存在时原样读取其scope/state/health字段，不为了符合当前策略改写truth；监控停用的Node由既有资产监控状态单独说明，不把inactive伪造为数据库out_of_scope。

没有生效策略或监控未激活时Expected为空，仍返回全部Held；当两个集合均为空且查询成功时才可返回providers=[]。state存在但违反既有非空/current/health约束应整体查询失败，不能过滤坏行后返回部分成功。

#### Snapshot freshness and latest health

已有state且monitoring_status=out_of_scope时snapshot_freshness=out_of_scope；其余有效current state在 `statement_timestamp()-last_complete_at <= interval '15 minutes'` 时fresh，超过阈值stale；只有不存在state的合法合成行使用unknown。health按health_degraded=false/true分别映射UI normal（healthy文案）/degraded，时间始终为health_scheduled_at；缺健康观察为unknown。health_reason来自本次明确允许的安全projection。

允许fresh+normal、fresh+degraded、stale+degraded、**stale+normal（保留最近non-degraded历史健康观察及其真实时间）**。不根据health观察年龄改变freshness，也不把stale+normal显示成“当前健康保证”。同一Provider的last_complete_at与health_scheduled_at各自展示，不能互换、取最大或生成综合status。

**health_degraded不参与Cross-node Duplicate Ownership eligibility**，不改变owner集合、detection、membership或resolve；本函数不调用任何ownership写函数。Node-local duplicate仍属于既有诊断，不变成ownership eligibility条件。

#### Query-access security and time

函数采用 `SECURITY DEFINER`、`STABLE`、`SET search_path = pg_catalog`（如需文本时间显示同时固定UTC）；表和函数依赖全部schema-qualified，不使用动态SQL、行锁或任何写语句。owner为 `relay_control_migrator`，REVOKE EXECUTE FROM PUBLIC，授权仅 `relay_control_runtime` EXECUTE；除固有owner/superuser外不授予其他调用角色，runtime仍没有provider_states直接SELECT。

`STABLE`成立的前提是函数只SELECT，并使用单次SQL的 `statement_timestamp()`，不使用clock_timestamp/Go time.Now作为各行独立时钟。Expected策略/监控区间、state读取和freshness均共享该语句一致性视图与时点。调用方用一条SQL同时取得statement_timestamp作为响应observed_at并调用函数，函数签名不另加客户端可控time参数。

target_instance_id为null/零UUID时以既有invalid-query SQLSTATE 22023拒绝，未知Node采用现有not-found SQLSTATE P0404；函数/读取内部失败不得被捕获成零行成功。应用只用runtime连接执行此函数（sqlc typed read wrapper），不得使用Go owner connection、migration role或raw-table SQL bypass。此权限边界参照既有readonly-query pattern，而不是放开整张provider_states表。

#### Independent HTTP read and composition

独立只读端点：`GET /api/account-inventory/nodes/{instance_id}/providers`，operationId=`getNodeInventoryProviderStates`。使用既有启用的super_admin会话、no-store、request ID；响应为 `{instance_id, observed_at, providers}`，每行只有上述字段。成功200代表完整查询；合法空集合与not-yet-observed行不同。非法UUID400、未知Node404、认证失败401/403、函数缺失/权限不足/数据库或query失败503，复用既有错误envelope；timeout为query2s/HTTP5s，不在错误体返回providers=[]或raw error。

UI将503/网络错误映射为read unavailable，绝不转成empty/stale或正常health。freshness的SQL行枚举不含unavailable，因为不可用是整个读取失败；UI双维度view model可以呈现unavailable，但不伪造返回行。

```text
Provider Summary ← new provider-state readonly read model
Account Table    ← existing account inventory read model
Binding          ← existing binding read model
Duplicate        ← current/history duplicate read models
```

P-READ不再留给未来另选方案：以上完整契约已获Final Approval及实施授权，实际验收证据记录在planning-validation.md。

### 4. Lossless Gateway Account identity

**目标链路：DB/internal Go int64 → Control API JSON decimal string → generated TS string → UI string → existing Binding request decimal string → server int64。** 保持现有正数domain：1至9223372036854775807，不允许负数或零。

Control OpenAPI 引入共享 `GatewayAccountId`：

```yaml
type: string
minLength: 1
maxLength: 19
pattern: '^[1-9][0-9]{0,18}$'
example: '9007199254740993'
```

pattern/长度不足以保证int64上限，服务端必须在词法校验后严格以base10、64bit解析并检查大于0。超过9223372036854775807、numeric JSON、null（必填字段）、负数、零、前导+、前导零、空白、小数、指数和空串均返回既有400 `validation_failed`，不调用store，不写成功审计，绝不truncate/round/wrap。

所有必填Account ID引用该schema；已有可空response字段仍允许JSON null，但非空分支只能string，不把unknown变成"0"。generated Go **API boundary**类型改string/nullable string，handler输出用int64的十进制格式化，输入严格解析后传给原store int64。DB/sqlc/domain不改类型。内部Binding audit JSONB的old/new_gateway_account_id目前由00011 CHECK要求number，保留精确数据库JSONB numeric，不修改CHECK或历史；若未来通过HTTP导出这些审计字段，必须投影为string，不能直接透传numeric。

TypeScript Account ID、React state、form/option value、adapter、request DTO一律string；禁止number、Number/parseInt/parseFloat、一元+或经numeric JSON转换。不存在现成Binding表单，不能为测试新建生产mutation UI；端到端request环节验证既有Binding transport/client。未来独立管理UI也必须遵守同一string契约，Topology仅展示。

### 5. Affected API surfaces

以下为已统一修正的Control/Web HTTP surfaces；实际验收见planning-validation.md。

| 层/路径 | 字段或职责 | 修订契约 |
| --- | --- | --- |
| OpenAPI `GatewayAccountContext` | `account_id` | GatewayAccountId string |
| `RelayNodeGatewayAccountBindingDetail` | `gateway_account_id` | string；适用于current/previous/history引用 |
| `NodeRelayBindingResponse` | nullable `gateway_account_id` | string或null |
| `GatewayAccountCentricBindingItem` | `gateway_account_id`及嵌套context/binding | 全部string |
| `BindRelayNodeRequest` | `gateway_account_id` | 请求string |
| `RebindRelayNodeRequest` | `new_gateway_account_id` | 请求string |
| GET `/api/relay-bindings/nodes/{instance_id}` | ID和所有嵌套对象 | 统一string；path Node UUID不变 |
| GET `/api/relay-bindings/gateways/{instance_id}` | current Directory/candidate read、绑定目标 | 统一string；不新增候选提交UI |
| GET `/api/relay-bindings/unresolved` | current_binding/last-known context | 统一string |
| POST `/api/relay-bindings/bind`、`/rebind` | request + `binding/previous_binding` response | read/write同表示，action语义不变 |
| POST `/api/relay-bindings/unbind` | 请求只有Node UUID；response previous_binding内Account ID | 请求结构不变，response ID统一string |
| Topology只读投影 | 所有Gateway Account ID及嵌套binding/context | 使用同一schema，不能自建number/string双轨 |
| `internal/api/api.gen.go` | 六个schema及嵌套response | 从OpenAPI重生，不手改 |
| `internal/api/relay_binding_handlers.go` | request解码、account/binding response mapping | 严格parse/format；store仍int64 |
| `web/src/api/generated/control.ts`、`web/orval.config.ts` | generated types、JSON.parse/JSON.stringify | 类型为string且wire已加引号；不靠reviver补救已舍入数字 |
| `internal/api/relay_binding_http_integration_test.go` | numeric fixtures、read/write/error断言 | 全面string fixtures+大ID往返+拒绝numeric |
| `internal/store/relay_node_gateway_account_binding_repository_integration_test.go` | domain及audit numeric JSONB断言 | domain/audit保持既有形式；精度fixture不得以float64中转 |
| `web/src` consumers/components | 当前无Binding表单；generated client是已找到消费者 | 只读Topology使用string，生成客户端已统一string |
| Control Binding Runbook/OpenSpec examples | ID格式、现有API调用示例 | 明确breaking及string请求/响应 |

范围检索覆盖control生成链、handlers/store/tests/web及gateway/node/ops对Control `/api/relay-bindings`/Gateway Account字段的引用。仓库内未发现稳定external SDK/其他服务消费者；这不证明仓库外不存在消费者。

### 6. Breaking compatibility decision

**原三项契约已APPROVED：Control/Web采用forward contract correction。** 现有HTTP number→string是breaking change，不叫additive。统一修正所有read/write与生成客户端；不接受number|string兼容输入、不返回number旧响应，不出现read=string/write=number。依据是目前可确认消费者只有Control自带生成Web客户端，且没有已知稳定external消费者；最终部署前必须补充实际external消费者清点。若发现兼容承诺，停止发布并更新版本化迁移契约，不静默放行numeric。

未来发布将匹配的API与内嵌Web作为一个版本交付，旧页面/SDK需要刷新或更新；新API拒绝旧numeric body。回滚也成对回滚API/Web，暂停新Topology入口，不降库或改审计。此为未来契约方案，本轮未发布。

### Compatibility Boundary

#### Gateway Directory source contract

**Architecture decision 已冻结：本 change 不修改既有 Gateway Directory source v1。** 保留schema_version=1、numeric JSON account ID、Control Go int64 ingestion和existing persistence semantics。`internal/drivers/gatewaydirectory/model.go`直接严格解码到int64，不经过JavaScript，因此没有本次JS safe-integer精度问题。source v1 canonical preimage/fingerprint和00010 schema约束全部不变。source numeric不能直接透传为Web numeric。

#### Control/Web transport contract

所有跨Control HTTP API与Web client的Gateway Account ID统一decimal string：read API、candidate API、binding detail、bind/rebind request、所有mutation response、generated Go API boundary、generated TS、React option/form/state均采用同一表示。内部Go/store仍int64；JSON formatter/parser是边界转换点。source v1保持numeric不能作为Web保留number的理由。

#### Deferred architecture item

未来是否将Directory source改为string ID、schema_version=2，及其dual-read/migration/compatibility contract，必须由独立Gateway Directory architecture change决定。本change不引入source v2、不改schema_version=1、不新增相关migration，也不把source改造列为本次发布前置任务。

### 7. Read interfaces, authorization and recovery

只读Node列表复用assets分页；单Node binding/Directory context和current duplicate复用既有读取。上一版 `/api/topology/nodes` 全域聚合仍撤回；仅新增§3冻结的独立Provider-state只读接口，不存在逐Node全量账号预读。仅选中Node时加载各详情资源，合理有界并发，不引入新持久缓存或背景写任务。

freshness/health、Binding resolution、duplicate lifecycle分别保持来源时点，跨HTTP请求不宣称原子一致。每区独立loading/empty/unavailable/retry；503/网络失败不是empty，refresh失败不能把旧成功继续标current。401清会话/内存数据；切换Node或分页丢弃旧响应。重启/刷新只重新读，不恢复写入草稿。使用UTC和DB时间判断证据窗口，不按浏览器时钟改写truth。

页面和新history读取沿用现有启用的实名super_admin管理会话/no-store/request ID；无新角色矩阵。occurrence canonical account_key仍按最新已归档规格允许管理员文本展示，不新增凭据曝光、HMAC或reauth政策；不上URL、storage、logs或metrics。只以Node UUID/status/cursor发请求，避免account_key query filter。不开任何原生管理端点代理。

## Acceptance Matrix

以下是冻结的验收契约；实现验证结果、证据与未完成门禁单独记录在planning-validation.md，不以契约条目代替测试结果。

### Duplicate historical involvement

| ID | 场景 | 必须结果 |
| --- | --- | --- |
| D1 | A/B duplicate ACTIVE，二者有authoritative evidence | current A/B均包含，History A/B均包含同一occurrence |
| D2 | B fresh complete absent，membership DELETE，occurrence RESOLVED | A history包含，B history也包含；B current不再在affected set；两者查询语义不变 |
| D3 | A也随后不再拥有账号，current集合为空 | 历史依然由各Node evidence证明，不能依赖current集合或source poll |
| D4 | 同一Node有多条owner/absence/degraded evidence | occurrence只返回一次，分页在occurrence层，不按evidence计数 |
| D5 | Node只被degraded/absence authoritative evaluation涉及 | history可包含但不能声称该Node曾为confirmed owner |
| D6 | source_poll_run_id已因retention变null；重启后查询 | 历史结果不丢失；不新增history表或恢复membership |
| D7 | current旧endpoint回归、History status筛选和翻页 | 旧instance_id仍current；History独立cursor/filter，ACTIVE/RESOLVED不混淆 |

### Provider evidence dimensions

| ID | source / read | 必须输出 |
| --- | --- | --- |
| P1 | last_complete_at fresh，health_degraded=false | freshness=fresh，health=normal，两个来源时间 |
| P2 | T0完整；T0+5min node-local duplicate/incomplete，health_degraded=true | freshness=fresh，health=degraded，同时显示两个badge，不能只显示绿色Fresh |
| P3 | last_complete_at stale，health_degraded=true | freshness=stale，health=degraded |
| P4 | read不可用/权限不足/timeout | read_state=unavailable及受影响维度unavailable；不能empty/stale/normal，不能把占位算验收通过 |
| P5 | 15min及15min+1ms；health不变 | freshness按既有边界变化，health独立；不使用Directory540秒 |
| P6 | 空账号Provider仍有current证据；缺state/非active范围 | 不由账号页推断Provider集合；按存在证据/unknown/out_of_scope分别展示 |
| P7 | 只翻转health_degraded，其他current/promoted证据不变 | duplicate eligibility、owners、occurrence lifecycle均不因本UI/read变化而改变 |
| P8 | 通过新safe read读取health_reason，或历史poll清理 | 固定reason枚举直接来自provider state，无raw error；freshness/health不从历史poll重建 |

### P-READ final acceptance

| ID | 场景 | 必须结果 |
| --- | --- | --- |
| A | openai有current provider_state，0 account rows | Provider Summary仍有openai，证明查询不从账号表推导完整集合 |
| B | last_complete_at fresh，health_degraded=true，health_scheduled_at更新 | freshness=fresh、health=degraded，分别展示正确完整snapshot时间与最新health时间 |
| C | Provider stale，health_degraded=true | freshness=stale、health=degraded，不合成单一status |
| D | 使用真实runtime role执行函数并尝试原表SELECT | 函数可EXECUTE；SELECT * FROM account_inventory_provider_states仍permission denied；PUBLIC无EXECUTE |
| E | 函数不存在/权限失败/query timeout或数据库失败 | API 503、Topology read unavailable，不能返回200 providers=[]或partial rows |
| F | active policy中openai尚无provider_state | 返回openai合成行，state/times/health为null、snapshot_freshness=unknown，UI not-yet-observed；不写state |
| G | active policy集合与Held含out_of_scope旧Provider，账号皆可为空 | 返回并集、每Provider一次；既有scope保留，零行仅在并集确实为空时合法 |
| H | stale snapshot但latest health_degraded=false | 同时显示stale+normal/healthy与历史health时间，不伪称最新snapshot healthy |
| J | policy/monitoring在时间边界或并发变更、15min/+1ms | 一条语句统一snapshot/time，完整集合无混合时点，freshness边界稳定 |
| K | migration Up/Down在隔离环境验证 | Up仅新增函数/owner/EXECUTE；Down只DROP该函数，无CASCADE，既有account函数signature、表/列/数据/ACL不变 |
| L | unsafe payload/secret/raw-error canary及字段allowlist | 只含九个最小字段，无current_poll_run_id、账号身份、credential/raw response；reason仅固定枚举 |

### Lossless Gateway Account identity

| ID | 场景 | 必须结果 |
| --- | --- | --- |
| I1 | 9007199254740991、9007199254740992、9007199254740993、9223372036854775807 | 每个值从DB/source internal int64→Control API带引号JSON→真实生成TS client→UI string→请求带引号JSON→server int64逐字不变 |
| I2 | 特别比较9007199254740993和9007199254740992 | UI选项/文本/state/请求不能合并或舍入；前者绝不变后者 |
| I3 | 请求字符串9223372036854775808 | 400 validation_failed，无store/action/成功审计，不能wrap |
| I4 | numeric JSON、小数/指数、零、负数、前导+、前导零、空/空白/必填null | 全部拒绝，不转换后接受 |
| I5 | 六个schema、三类Binding reads、bind/rebind/unbind所有嵌套responses | Account ID均string；nullable保留null；read/write无双重contract |
| I6 | generated TS + 所有Account ID state/option/form/adapter | 不存在Account ID number或Number/parseInt/parseFloat/一元+转换，计数等非identity number不误报 |
| I7 | 旧numeric客户端与新版API | 明确breaking拒绝，发布与回滚API/Web成对，无隐式兼容 |
| I8 | 内部DB/domain/audit JSONB对照 | int64和精确numeric审计不改，HTTP若投影该ID必须string；身份修正不涉及schema/data migration |
| I9 | source v1原始numeric响应经Go int64后进入Control Web链 | source仍schema_version=1且无损；对Web输出必须带引号string，不直接透传numeric；canonical/persistence不变 |

### Readonly, compatibility and release gates

R1：Topology网络测试断言无bind/rebind/unbind/其他业务mutation、无采集或数据面调用；不存在候选submit或无效管理链接。R2：已有Binding transport测试验证身份纠正不改原事务/权限/CSRF/审计；不是Topology操作。R3：P-READ按本轮冻结的query-access边界取得Final Approval，external消费者清点按既有兼容契约执行；未获实施授权不得动代码。R4：桌面/390px/键盘、独立资源失败/401/乱序/重启、UTC时间和账号隐私回归。R5：实施已获授权，必须完成专项验收、make test build、strict和diff检查；发布、提交与归档需另按授权执行。

## Risks / Trade-offs

- [只读函数扩大调用面] → 只允许九字段、单Node参数、SECURITY DEFINER/STABLE/fixed search_path和runtime EXECUTE；不放开原表SELECT，不持久化summary。
- [source v1与Web边界混淆] → 按已冻结Compatibility Boundary保留source numeric、强制Web string；source-v2只留给独立架构change。
- [破坏旧numeric consumers] → breaking标记、forward correction提案、发布前清点及API/Web成对升级回滚。
- [history被误认为历史owner名单] → 命名“historical involvement / 历史评估涉及”，保留current affected set原语义。
- [fresh被误认为最新健康正常] → 两个badge、两个来源、不把health加入eligibility。

## Migration Plan

**0 persistence migration；P-READ 允许一个最小 additive readonly query-access migration。** 核对最新Goose序列后，本轮按授权创建`00017_account_inventory_provider_state_query_access.sql`，仅在隔离验收数据库执行，不在生产执行迁移。

Up只执行：CREATE `public.control_query_account_inventory_provider_states_v1(uuid)` readonly function、设owner为relay_control_migrator、REVOKE PUBLIC EXECUTE、GRANT EXECUTE给relay_control_runtime。函数读取现有state/policy/monitoring资产，不新增或ALTER任何表/列，不建materialized view/index/topology/provider summary persistence，不修改既有account query signature/body，不增加runtime direct SELECT。

Down只 `DROP FUNCTION public.control_query_account_inventory_provider_states_v1(uuid)`，使用默认RESTRICT，不用CASCADE；随函数删除其EXECUTE授权，不单独修改表ACL，不触碰account_inventory/provider_states、existing v1 account query或历史数据。有意外依赖应失败而非级联删除。

生产应用回滚优先恢复兼容API/Web并保留只读函数，不自动执行migration down；如在明确批准的隔离回滚验收中执行Down，只移除本函数，调用者应得到unavailable而非空Provider集合。history与身份修正均不新增persistence migration，source-v2/schema约束仍不在本change范围。

原三项契约与P-READ均已获 **Architecture Contract Final Approval**；用户已另行授权进入实施。本轮执行生成、业务测试、隔离数据库/容器和构建验收；用户随后授权按实际diff分批提交；不生产部署。
