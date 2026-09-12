# 阶段 1 资产注册与只读视图 Runbook

本文适用于 `add-control-asset-registry-foundation`。资产登记是受控部署操作，不是产品 API；浏览器只读取 Control 同源 API，Control 不探测 Gateway 或 Relay Node endpoint，也不修改数据面配置。

所有示例值都必须来自目标环境的部署清单或 Secret Manager。不要把数据库口令、Reader Secret、Secret 引用、连接串或带 query 的 URL 写入 Git、工单、终端录屏和验收输出。

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

## 受控登记顺序

最终受控模板位于：

1. `deploy/asset-registry/register-assets.sql`：Gateway、Driver/capability、Relay Node/capability；
2. `deploy/asset-registry/activate-provider-policy.sql`：不可变 Provider 策略版本、当前绑定和激活区间；
3. `deploy/asset-registry/set-node-monitoring.sql`：Node 账号监控启停或预约；
4. `deploy/asset-registry/reconcile.sql`：登记后只读对账。

四个模板都只允许环境专用 registrar LOGIN 执行，其中前三个执行受控变更，`reconcile.sql` 只调用固定 SECURITY DEFINER 对账函数。模板输入必须由受控部署系统注入；不要把 Secret 引用或连接串固化在脚本中。以下变量名与模板头部一致，示例没有默认值，任何缺失都会 fail closed：

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

`register-assets.sql` 只从固定环境变量读取两个 Secret 引用，并以 PostgreSQL extended-query bind 参数发送；引用不会出现在 `psql` argv/process list、`pg_stat_activity` 查询文本或 statement log 中（数据库仍可能按独立参数审计策略记录 bind 参数，因此生产必须禁用敏感参数日志）。空值表示未配置。空的 `effective_at` 表示使用数据库当前时间；非空值必须是带 `Z` 或数值 offset 的 RFC 3339 时间（例如 `2026-08-25T15:00:00Z` 或 `2026-08-25T23:00:00+08:00`），无 offset 的本地时间会 fail closed。Provider/capability CSV 必须已经排序且无重复。模板各自显式开启 `SERIALIZABLE` 事务并固定事务时区为 UTC，不要再嵌套事务或拆开其中语句。

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

数据库仅保存 Secret Manager 的 opaque Reader Secret 引用，不保存凭证内容。Secret 引用自身同样按 Secret 处理：只可通过登记模板传入，不得出现在 API、页面、日志、指标、Trace、审计或对账输出。只读 API 仅返回 `secret_configured` 布尔值。若怀疑泄露，先轮换引用目标和数据库凭据，再按安全事件流程处理输出副本。

## Provider 策略

Provider 策略以 `(node_type, driver_contract_version)` 为作用域。`active` 与 `out_of_scope` 集合必须排序、去重、互不交叠；每次内容变化都通过 `activate-provider-policy.sql` 创建或复用不可变版本，并以数据库时间写入半开区间 `[effective_from, effective_to)`。

禁止对历史版本执行 `UPDATE` 或 `DELETE`。回滚策略也必须把旧内容作为新的受控激活操作，保留版本与区间历史。预约时间只能是数据库当前时间或未来时间；过去时间、跨作用域绑定和重叠区间会被拒绝。

## Node 监控区间

监控状态只来自 `set-node-monitoring.sql` 写入的数据库半开区间，不从 Node 可达性、Compose 状态或 Gateway 状态推断。操作必须使用模板允许的固定 reason，并记录实名部署 actor。启用、停用和预约都使用数据库时间边界；重放相同当前状态不新增重叠区间。

启用区间保存启用 reason/actor；关闭或预约关闭会在同一历史行保存独立的关闭 reason、actor 和数据库登记时间。启用操作不得使用 disable reason，关闭操作也不得使用 enable reason；NULL 或冲突重放均 fail closed。只读 API 不投影这些运维审计字段。

停止 Control、Gateway 或 Node 不会隐式关闭监控区间。若需修正错误预约，使用新的受控边界操作，禁止直接改历史行。

## 对账与只读页面

`reconcile.sql` 必须确认：

- 环境单例仍唯一且身份未变；
- Gateway 至多一个；
- Node、Driver 和 capability 复合外键完整；
- 当前策略绑定与当前激活区间同作用域；
- Node 监控区间不重叠。

对账输出只能包含固定计数和通过/失败状态，不得选择 endpoint、名称、instance ID、Provider 名称或 Secret 引用。随后由实名 `super_admin` 访问 `/assets`，分别检查环境、Gateway、Driver、当前策略和 Node 页面。页面无编辑入口，endpoint 仅为不可点击文本，浏览器不得请求登记的外部 endpoint。

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

应用回滚只切回旧二进制/前端，保留资产表、不可变策略版本、绑定和全部激活历史。旧版本会忽略 additive 表，重新升级后恢复读取。

普通回滚严禁执行 `make migrate-down`。只有全新数据库且 Gateway、Driver、Node、策略、绑定和所有激活区间均为空，经人工双人确认后，才允许使用 Migration owner 执行受保护 down；`environments` 单例必须保持不变。任何已有资产或历史都会使 down fail closed。

## 文档命令 dry run 清单

在一次性 PostgreSQL 18 环境逐项执行并记录脱敏退出码：

1. 未设置 `CONTROL_ENVIRONMENT_ID` 时进程非零退出且端口未监听；设置匹配值后启动；
2. `make migrate-up` 成功，三类角色权限与上文预期一致；
3. 四个 `deploy/asset-registry/*.sql` 模板按顺序成功，相同输入重放成功，冲突输入非零且无部分写入；
4. `reconcile.sql` 只输出固定计数/结果且不含 Secret 引用或 endpoint；
5. 策略历史更新/删除、过去或重叠区间、Node 监控重叠操作均失败；
6. 停止资产数据库时 `/assets` 显示独立失败与重试，恢复后读取当前值；
7. 回滚应用镜像后表和历史仍在，重新升级后可读；非空库 down 被拒绝。
