# 资产注册、Gateway 与 Node lifecycle Runbook

本文覆盖资产注册基础、Gateway lifecycle、Relay Node lifecycle，以及 Phase 6 Stage 3 Node 管理操作。Driver、Provider policy 和预约式 Node monitoring 仍由受控部署流程维护；实名 `super_admin` 可在 Control 同源 Asset Registry 中执行 Node lifecycle、显式 Health/Connection Test 和立即 Monitoring Enable/Disable。Control 不进入模型请求数据面。

当前部署的 Gateway、Driver 和 Relay Node 登记，以及 Provider policy 和 Node
monitoring 配置，以认证的 Control API/Asset Registry 管理流程为准。下文的
registrar SQL 模板保留用于历史隔离 fixture 与 recovery procedure；其中
`register-assets.sql` 是 `LEGACY / NOT CURRENT DEPLOYMENT ENTRY`。不得恢复已
撤销的历史 registrar 权限，也不得用直接表写入替代当前受控 API。

所有示例值都必须来自目标环境的部署清单或 Secret Manager。不要把数据库口令、management credential、Directory credential、Secret 引用、连接串或带 query 的 URL 写入 Git、工单、终端录屏和验收输出。

## Stage 0 protected credential operations

当前部署通过认证 Control API/Asset Registry 管理 Node `management_credential` 与 Gateway `directory_credential`。Control 将批准的 credential 以 asset-bound protected-at-rest sealed state 保存；外部 K2 由 `CONTROL_ASSET_CREDENTIAL_KEY_FILE` 提供，位于 PostgreSQL 外且只在进程启动时读取一次。

- Register：credential 字段省略表示 unconfigured；合法 string 表示 set；显式 null 非法。
- Edit：字段省略表示 keep；合法 string 表示 set；显式 null 表示 clear。
- Replace：字段省略表示 unconfigured；合法 string 表示 set；不得继承 predecessor credential；显式 null 非法。
- `secret_configured` 只表示 sealed credential 非 NULL，不表示当前 K2/Open 可用。K2 缺失、错误或损坏时，clear、Retire、unconfigured Replace 与 credential-independent reads 仍按既有 contract 工作；credential-dependent outbound 必须 fail closed。
- 不得把 plaintext credential、sealed blob、K2、K2 commitment 或 legacy `reader_secret_ref` 放入 API/UI/query/log/audit/metrics/trace/evidence。不得恢复 `FileSecretResolver` 或 legacy reference fallback。

生产命令不把含口令连接串放入 argv。数据库平台在 workspace 外提供 owner-only 的 `pg_service.conf` 与 `.pgpass`，部署作业只注入 `CONTROL_PGSERVICE_FILE`、`CONTROL_PGPASS_FILE`、`CONTROL_MIGRATOR_PGSERVICE` 和 `CONTROL_ASSET_REGISTRAR_PGSERVICE`。资产参数变量同样是部署作业的临时输入，不是 Control 进程配置；禁止写入仓库内 `.env` 或 shell history，作业结束后立即销毁。

## 升级前环境身份检查

新版本必填以下两个配置：

- `CONTROL_ENVIRONMENT_ID`：必须与数据库 `environments.singleton_id = 1` 的 `environment_id` 精确一致；
- `CONTROL_ENVIRONMENT`：`dev`、`staging` 或 `production`，必须与同一行的 `environment_type` 精确一致。

先使用 Migration owner 的受保护连接读取现有单例；以下比较只在当前 shell 内保存非 Secret 的环境身份，不输出不匹配值：

```sh
database_identity="$(
  PGSERVICEFILE="$CONTROL_PGSERVICE_FILE" \
  PGPASSFILE="$CONTROL_PGPASS_FILE" \
  PGSERVICE="$CONTROL_MIGRATOR_PGSERVICE" \
  psql -X --no-psqlrc \
    --set=ON_ERROR_STOP=1 --tuples-only --no-align --field-separator='|' \
    --command="SELECT environment_id, environment_type FROM public.environments WHERE singleton_id = 1"
)"
expected_identity="${CONTROL_ENVIRONMENT_ID}|${CONTROL_ENVIRONMENT}"
test -n "$CONTROL_ENVIRONMENT_ID"
test "$database_identity" = "$expected_identity"
unset database_identity expected_identity
```

必须恰好返回一行且比较成功后，才把两个配置加入平台 Secret/配置并切换镜像。禁止修改数据库环境身份去迎合错误配置。缺配置、缺行、ID 不匹配、类型不匹配或数据库不可用时，Control 会在监听 HTTP 前 fail closed，并只记录固定原因码。

开发验收使用 `CONTROL_ENVIRONMENT_ID=development`；其他环境不得复制该值。部署系统应把两个配置作为一个原子版本发布。

## 角色和 Migration

生产环境在 Migration 前由数据库平台创建两个 NOLOGIN capability role，并分别绑定环境专用 LOGIN：

- `relay_control_runtime`：Control 产品进程，仅拥有脱敏资产投影和所需表的读取权限；
- `relay_control_asset_registrar`：只可读取环境单例并执行受控登记函数，不可直接读写资产表，也不可读取认证敏感表。

Migration owner 只用于 Goose，不得提供给 Control 或登记作业。开发角色示例位于 `deploy/postgres/init/001-runtime-role.sql`，其中固定口令只允许一次性本地数据库使用。

用 Migration owner 执行升级；命令显式清除代理，避免 Go 工具继承产品代理配置：

```sh
env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY \
    -u http_proxy -u https_proxy -u all_proxy \
    PGSERVICEFILE="$CONTROL_PGSERVICE_FILE" \
    PGPASSFILE="$CONTROL_PGPASS_FILE" \
    DATABASE_URL="postgres://?service=${CONTROL_MIGRATOR_PGSERVICE}" \
    make migrate-up
```

升级后验证权限边界；结果必须依次为 `f`、`f`、`f`、`t`、`t`，且不得用超级用户替代产品或登记 LOGIN 做验收：

```sh
PGSERVICEFILE="$CONTROL_PGSERVICE_FILE" \
PGPASSFILE="$CONTROL_PGPASS_FILE" \
PGSERVICE="$CONTROL_MIGRATOR_PGSERVICE" \
psql -X --no-psqlrc --set=ON_ERROR_STOP=1 <<'SQL'
SELECT has_table_privilege('relay_control_runtime', 'public.gateway_instances', 'INSERT');
SELECT has_table_privilege('relay_control_asset_registrar', 'public.gateway_instances', 'SELECT');
SELECT has_column_privilege(
  'relay_control_asset_registrar',
  'public.gateway_instances',
  'reader_secret_ref',
  'SELECT'
);
SELECT has_function_privilege(
  'relay_control_asset_registrar',
  'public.control_register_gateway(uuid,text,text,text)',
  'EXECUTE'
);
SELECT has_function_privilege(
  'relay_control_asset_registrar',
  'public.control_reconcile_asset_registry(text,text)',
  'EXECUTE'
);
SQL
```

## 登记契约与历史 SQL 模板

当前部署通过认证 Control API 按以下顺序完成登记；具体 endpoint 契约以
`api/openapi.yaml` 和本手册的 lifecycle 小节为准：

1. 登记 Gateway、Driver/capability 和 Relay Node；
2. 激活不可变 Provider policy；
3. 启用或预约 Node monitoring；
4. 执行只读 reconcile，并要求所有 issue count 为零。

以下 SQL 模板是历史隔离 fixture/recovery 入口，不是当前部署入口：

1. `deploy/asset-registry/register-assets.sql`：Gateway、Driver/capability、Relay Node/capability；
2. `deploy/asset-registry/activate-provider-policy.sql`：不可变 Provider 策略版本、当前绑定和激活区间；
3. `deploy/asset-registry/set-node-monitoring.sql`：Node 账号监控启停或预约；
4. `deploy/asset-registry/reconcile.sql`：登记后只读对账。

这些历史模板都只允许环境专用 registrar LOGIN 执行，其中前三个执行受控变更，`reconcile.sql` 只调用固定 SECURITY DEFINER 对账函数。它们仅用于隔离 fixture/recovery；当前部署不得以模板替代认证 Control API。模板输入必须由受控部署系统注入；不要把 Secret 引用或连接串固化在脚本中。以下变量名与模板头部一致，示例没有默认值，任何缺失都会 fail closed：

```sh
PGSERVICEFILE="$CONTROL_PGSERVICE_FILE" \
PGPASSFILE="$CONTROL_PGPASS_FILE" \
PGSERVICE="$CONTROL_ASSET_REGISTRAR_PGSERVICE" \
CONTROL_GATEWAY_READER_SECRET_REF="$ASSET_GATEWAY_READER_SECRET_REF" \
CONTROL_NODE_READER_SECRET_REF="$ASSET_NODE_READER_SECRET_REF" \
psql -X --no-psqlrc \
  --set=ON_ERROR_STOP=on \
  --set=gateway_instance_id="$ASSET_GATEWAY_INSTANCE_ID" \
  --set=gateway_display_name="$ASSET_GATEWAY_DISPLAY_NAME" \
  --set=gateway_endpoint="$ASSET_GATEWAY_ENDPOINT" \
  --set=driver_node_type="$ASSET_DRIVER_NODE_TYPE" \
  --set=driver_contract_version="$ASSET_DRIVER_CONTRACT_VERSION" \
  --set=driver_display_name="$ASSET_DRIVER_DISPLAY_NAME" \
  --set=driver_lifecycle_status="$ASSET_DRIVER_LIFECYCLE_STATUS" \
  --set=driver_capabilities_csv="$ASSET_DRIVER_CAPABILITIES_CSV" \
  --set=node_instance_id="$ASSET_NODE_INSTANCE_ID" \
  --set=node_display_name="$ASSET_NODE_DISPLAY_NAME" \
  --set=node_endpoint="$ASSET_NODE_ENDPOINT" \
  --set=node_capabilities_csv="$ASSET_NODE_CAPABILITIES_CSV" \
  --file=deploy/asset-registry/register-assets.sql

PGSERVICEFILE="$CONTROL_PGSERVICE_FILE" \
PGPASSFILE="$CONTROL_PGPASS_FILE" \
PGSERVICE="$CONTROL_ASSET_REGISTRAR_PGSERVICE" \
psql -X --no-psqlrc \
  --set=ON_ERROR_STOP=on \
  --set=node_type="$ASSET_DRIVER_NODE_TYPE" \
  --set=driver_contract_version="$ASSET_DRIVER_CONTRACT_VERSION" \
  --set=active_providers_csv="$ASSET_ACTIVE_PROVIDERS_CSV" \
  --set=out_of_scope_providers_csv="$ASSET_OUT_OF_SCOPE_PROVIDERS_CSV" \
  --set=actor="$ASSET_OPERATION_ACTOR" \
  --set=effective_at="$ASSET_POLICY_EFFECTIVE_AT" \
  --file=deploy/asset-registry/activate-provider-policy.sql

PGSERVICEFILE="$CONTROL_PGSERVICE_FILE" \
PGPASSFILE="$CONTROL_PGPASS_FILE" \
PGSERVICE="$CONTROL_ASSET_REGISTRAR_PGSERVICE" \
psql -X --no-psqlrc \
  --set=ON_ERROR_STOP=on \
  --set=node_instance_id="$ASSET_NODE_INSTANCE_ID" \
  --set=enabled="$ASSET_MONITORING_ENABLED" \
  --set=effective_at="$ASSET_MONITORING_EFFECTIVE_AT" \
  --set=reason="$ASSET_MONITORING_REASON" \
  --set=actor="$ASSET_OPERATION_ACTOR" \
  --file=deploy/asset-registry/set-node-monitoring.sql
```

`register-assets.sql` 只从固定环境变量读取两个 Secret 引用，并以 PostgreSQL extended-query bind 参数发送；引用不会出现在 `psql` argv/process list、`pg_stat_activity` 查询文本或 statement log 中（数据库仍可能按独立参数审计策略记录 bind 参数，因此生产必须禁用敏感参数日志）。空值表示未配置。空的 `effective_at` 表示使用数据库当前时间；非空值必须是带 `Z` 或数值 offset 的 RFC 3339 时间（例如 `2026-08-25T15:00:00Z` 或 `2026-08-25T23:00:00+08:00`），无 offset 的本地时间会 fail closed。Provider/capability CSV 必须已经排序且无重复。登记和策略模板使用 `SERIALIZABLE`；Stage 3 的 monitoring writer 必须先捕获最新 Disable fence F0，再使用模板固定的 `READ COMMITTED` 事务，让受控函数在取得 Node lock 后读取 F1。不要嵌套事务或拆开其中语句。

`reconcile.sql` 使用 `REPEATABLE READ READ ONLY`，由同一个 registrar LOGIN 执行；其最终只读权限必须通过数据库权限测试证明不包含 endpoint、Secret 引用列或任何认证表。禁止为了让对账通过而给 registrar 或 runtime 增加资产表写权限：

```sh
PGSERVICEFILE="$CONTROL_PGSERVICE_FILE" \
PGPASSFILE="$CONTROL_PGPASS_FILE" \
PGSERVICE="$CONTROL_ASSET_REGISTRAR_PGSERVICE" \
psql -X --no-psqlrc \
  --set=ON_ERROR_STOP=on \
  --set=expected_environment_id="$CONTROL_ENVIRONMENT_ID" \
  --set=expected_environment_type="$CONTROL_ENVIRONMENT" \
  --file=deploy/asset-registry/reconcile.sql
```

模板会在参数缺失、endpoint 非规范、Driver/capability 未登记、稳定 ID 内容冲突、策略集合交叠或区间冲突时停止。相同内容可安全重放；同一 ID 的不同内容必须 fail closed，禁止用 `UPDATE` 绕过。

## Endpoint 与 Secret 引用

登记 endpoint 必须是规范化的绝对 `http`/`https` URL，包含 host，不含 userinfo、query 或 fragment。登记不会执行 DNS、TLS 或 HTTP 探测。

历史隔离 fixture SQL 仍可能使用 opaque `reader_secret_ref`，但这不是当前部署契约；它只用于明确标注的 historical fixture/recovery，并不得成为生产 runtime 或当前 operator workflow 的 credential source。当前只读 API 仅返回 `secret_configured` 布尔值。若怀疑 credential 泄露，按 K2/asset credential recovery runbook 处理，不在输出中保留明文或 sealed state。

## Provider 策略

Provider 策略以 `(node_type, driver_contract_version)` 为作用域。`active` 与 `out_of_scope` 集合必须排序、去重、互不交叠；每次内容变化都通过 `activate-provider-policy.sql` 创建或复用不可变版本，并以数据库时间写入半开区间 `[effective_from, effective_to)`。

禁止对历史版本执行 `UPDATE` 或 `DELETE`。回滚策略也必须把旧内容作为新的受控激活操作，保留版本与区间历史。预约时间只能是数据库当前时间或未来时间；过去时间、跨作用域绑定和重叠区间会被拒绝。

## Node 监控区间

监控状态只来自 `set-node-monitoring.sql` 写入的数据库半开区间，不从 Node 可达性、Compose 状态或 Gateway 状态推断。操作必须使用模板允许的固定 reason，并记录实名部署 actor。启用、停用和预约都使用数据库时间边界；重放相同当前状态不新增重叠区间。

启用区间保存启用 reason/actor；关闭或预约关闭会在同一历史行保存独立的关闭 reason、actor 和数据库登记时间。启用操作不得使用 disable reason，关闭操作也不得使用 enable reason；NULL 或冲突重放均 fail closed。只读 API 不投影这些运维审计字段。

停止 Control、Gateway 或 Node 不会隐式关闭监控区间。若需修正错误预约，使用新的受控边界操作，禁止直接改历史行。

## Node lifecycle 产品操作

Asset Registry 的 Node collection 默认只显示 active Node，并提供 `active|retired|all` lifecycle filter；历史 detail 保留 retired metadata 和 predecessor/successor lineage。Register 创建 revision 1 的新 identity；Edit、Retire 和 Replace 都携带十进制字符串 `expected_revision`。Retire 是 terminal transition；Replace 原子退休旧 identity 并创建 revision 1 的新 identity，禁止复用或复活历史 identity。

Node mutation 使用全局 `command_id` durable receipt。相同 actor、command 和 intent 重放原始 status/body；actor 或 intent 冲突返回固定冲突。Management credential 是 write-only protected state，产品响应和页面只显示 `secret_configured`。Retire/Replace 会在同一数据库 boundary 关闭旧 current binding、关闭 current monitoring、durable cancel future monitoring，并 fence 或终止旧 Node 的 Inventory work；replacement 不继承这些 runtime truth，也不继承 predecessor credential。

Node collection cursor 绑定 filter、`node_generation` 和首屏 DB `read_as_of`。真正改变 lifecycle/current collection projection 的事务推进 generation，使旧 cursor 返回 `cursor_stale`；单纯 wall-clock 跨过 monitoring boundary 不改变已签发 cursor chain。legacy `nodes` count 继续表示 total，`node_counts` 分别提供 active、retired 和 total。

## Node 管理操作

active Node detail 提供四个彼此独立、只在管理员点击时执行的操作：Health、Connection Test、Enable Monitoring 和 Disable Monitoring。页面加载、reload 或切换 Node 不会自动 Probe，也不会后台轮询。retired Node 不提供这些控件。

Health 是不需要 CSRF 的认证只读 GET；Connection Test 是需要同源与 CSRF 的 POST。两者复用固定 Driver Probe，只访问已登记 Node 的固定 `/healthz` 目标，一次请求最多一次 HTTP，不重定向、不重试、不使用代理、不读取或发送 Reader Secret。结果只保存脱敏 audit（instance ID、result、reason、latency），不建立 health history。audit 写入失败返回安全 503，管理员必须先确认远端是否已收到该次请求，系统不会自动重做 Probe。

Enable/Disable 都需要新的稳定 `command_id`，不接受 Node revision，也不改变 Node revision/updated_at。未知 outcome 应使用同一 command ID 重试，以便 immutable receipt 返回原 status/body。Enable 只建立立即 current activation；存在冲突 future activation 时返回冲突。Disable 在一个数据库 boundary 内关闭 current 并取消全部 future activation；确认框中的取消说明是持久化事实。

每次新的 Disable（包括 `already_disabled`）都会提交一个严格递增 `committed_at` 的 receipt fence。预约 writer 必须通过本仓库模板先捕获 F0，再在写事务内比较 F1；`monitoring_disable_fence_conflict` 表示 intent 已过期，作业应丢弃旧 intent 并由下一次正常调度重新形成 intent，禁止自动重试原事务。该 fence 不是 disabled latch，Disable 之后形成的新 intent 会捕获新 F0，并继续按普通 monitoring precondition 判断。支持部署不得清理每个 Node 最新的 Disable receipt。

## 对账与 Asset Registry 页面

`reconcile.sql` 必须确认：

- 环境单例仍唯一且身份未变；
- Gateway 保持一个 current slot，并可有 retired history；
- Node、Driver 和 capability 复合外键完整；
- 当前策略绑定与当前激活区间同作用域；
- Node 监控区间不重叠。

对账输出只能包含固定计数和通过/失败状态，不得选择 endpoint、名称、instance ID、Provider 名称、plaintext credential、sealed state、K2、commitment 或 Secret 引用。随后由实名 `super_admin` 访问 `/assets`，分别检查环境、Gateway、Driver、当前策略和 Node 页面。Node lifecycle 控件只调用 Control 同源 API；浏览器不得请求登记的外部 endpoint，也不得显示 raw credential、sealed state 或 legacy reference。retired Node 只提供历史读取和 lineage 导航，不提供 resurrection action。

### Web 路由与静态资源

生产前端资源统一使用 `/static/` public prefix。Control 对 `/static/<file>` 去除该前缀后读取嵌入的 frontend dist；命中文件时返回静态资源，缺失文件固定返回 HTTP 404，不得回退到 SPA shell。

`/assets` 与 `/assets/` 都是 Asset Registry SPA route。已认证用户直接访问任一路径，或在 `/assets/` reload，均必须渲染带有 `assets-page` 稳定标识和“资产注册表”标题的页面，且不得渲染管理员控制台默认页。其它既有 SPA deep link 保持 shell fallback；已注册 `/api/...` route 始终由 API router 优先处理。

发布验收至少检查 production build 的入口 JS/CSS 与 lazy chunk URL 都位于 `/static/`、Asset Registry lazy chunk 可加载、`/static/not-found.js` 返回 404，以及 `/api/healthz` 不返回 SPA shell。回滚只切回上一已接受的 Control 镜像，不修改资产数据或数据库 schema。

## 故障恢复

- `missing_config`：补齐 `CONTROL_ENVIRONMENT_ID` 和 `CONTROL_ENVIRONMENT` 后重启；
- `missing_row`：恢复原数据库或一致备份，禁止由应用自动创建环境；
- `id_mismatch` / `type_mismatch`：修正部署目标或配置，禁止修改数据库身份；
- `database_unavailable`：恢复数据库连通性。运行期资产 API 返回脱敏 `503` 且不展示旧缓存；恢复后用户显式重试即可，无需重启 Control；
- 登记冲突：停止变更，保留事务回滚结果，对照部署输入和 `reconcile.sql`；不要覆盖稳定身份或历史版本；
- registrar 权限错误：由数据库平台恢复 capability role/LOGIN 继承关系，禁止临时授予表写权限或认证表读取权限。

Control 或资产数据库故障只影响管理视图。Gateway 和 Relay Node 既有请求路径不依赖 Control；排障期间禁止为了“验证”而停止或修改数据面配置。

## 应用回滚

应用回滚只选择已签名、digest 固定的 Control artifact，并继续通过外置 compatibility wrapper。migration 34 将 evidence floor 提升为 2；class 0/1 artifact 必须在 Control HTTP/worker 启动前 fail closed，只有 class 2 artifact 可读取 Node lifecycle/cancellation truth。数据库保留资产、lineage、不可变策略版本、binding 和全部 monitoring 历史。

普通回滚严禁执行 `make migrate-down`。只有全新数据库且 Gateway、Driver、Node、策略、绑定和所有激活区间均为空，经人工双人确认后，才允许使用 Migration owner 执行受保护 down；`environments` 单例必须保持不变。任何已有资产或历史都会使 down fail closed。

## 文档命令 dry run 清单

在一次性 PostgreSQL 18 环境逐项执行并记录脱敏退出码：

1. 未设置 `CONTROL_ENVIRONMENT_ID` 时进程非零退出且端口未监听；设置匹配值后启动；
2. `make migrate-up` 成功，三类角色权限与上文预期一致；
3. 历史 `deploy/asset-registry/*.sql` 模板在隔离 fixture/recovery 场景按顺序成功，相同输入重放成功，冲突输入非零且无部分写入；当前部署仍以认证 Control API 的结果为准；
4. `reconcile.sql` 只输出固定计数/结果且不含 Secret 引用或 endpoint；
5. 策略历史更新/删除、过去或重叠区间、Node 监控重叠操作均失败；
6. 停止资产数据库时 `/assets` 显示独立失败与重试，恢复后读取当前值；
7. 回滚应用镜像后表和历史仍在，重新升级后可读；非空库 down 被拒绝。
