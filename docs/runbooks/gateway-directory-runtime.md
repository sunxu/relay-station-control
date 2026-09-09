# Gateway Directory runtime

Control 的 Gateway Directory runtime 默认关闭，由
`CONTROL_GATEWAY_DIRECTORY_ENABLED` 显式开启。它使用独立的
`CONTROL_GATEWAY_DIRECTORY_SECRET_MAPPING_FILE`，只读取 opaque reader
reference 映射，不读取 Gateway 的环境文件、数据库凭据或管理凭据。

本地部署时，Gateway source 与 Control poller 是两个独立开关。先在
Gateway 私有运行目录将 `DIRECTORY_ENABLED=true`，确认
`GET /internal/v1/api-account-directory` 在内部管理网络返回合法 source；再
在 Control 部署环境设置 `CONTROL_GATEWAY_DIRECTORY_ENABLED=true`。只有Control开关为false时，Control不创建新的ingestion run、不发送请求。
Gateway source关闭但Control仍开启时，Control仍会尝试读取并记录失败，freshness不会刷新。

Control 每五秒检查一次既有调度、工作和恢复流程；Directory slot、attempt、
lease 与 freshness 预算保持既有契约。单个 Gateway 的网络或 reader token
失败只记录脱敏的 durable failure，不停止其它 Control 能力。关闭或停机时，
runtime 停止领取新任务并等待有界收尾；未确认的 run 由既有 lease/reconciler
恢复。

本地 dev 的 dedicated reader mapping 由 `ops/dev/devctl init` 创建并挂载到
Control secret volume。mapping 与 token 文件必须位于外部受保护 runtime
目录，宿主目录 `0700`、文件 `0600`，容器挂载文件 `0400`；不得将 token、reference 或原始 response
写入仓库、日志或命令行。

### 回滚

先将 `CONTROL_GATEWAY_DIRECTORY_ENABLED=false`，再重启 Control。保留
forward migration、snapshot 和 failure evidence；不要通过 owner connection、
直接 SQL 或临时 runner 伪造 observation。回退到旧 Control 镜像时，恢复该
版本要求的 Node DNS/CIDR/CA 配置和目标证书策略。

### 首次补填与权限

Gateway已登记但reader reference为空时，先使用现有registrar身份运行
`deploy/asset-registry/configure-gateway-directory-reader.sql`，按模板从受保护环境输入Gateway UUID、有效管理员UUID和opaque reference。函数只允许无Directory/Binding历史时NULL首次补填；相同reference重放no-op，不改endpoint。不要先手工创建run，不删除历史来绕过前置条件。NULL reference在runtime中保持no-work，可稍后补填。

runtime仅通过有效run/fencing限定的`control_query_gateway_directory_target_v1`获取目标和opaque reference，无直接reference SELECT权限。新migration同时安装补填/目标读取函数；生产回滚只关闭新行为并保留forward schema。开发逻辑Down删除两个入口但保留审计历史约束与防伪保护。

### 管理出站传输

Control 的 Gateway Directory 管理出站当前版本仅支持内部 HTTP；HTTPS endpoint 在启动配置校验阶段拒绝。内部 HTTP 建立在受限网络与 service authentication 之上，不提供对可监听 east-west 流量攻击者的机密性保护。固定接口、认证、无代理、禁止 redirect、响应验证与限制仍生效；Control 入站和数据面不变。

Node 旧 `CONTROL_CLIPROXYAPI_MANAGEMENT_DNS`、`CONTROL_CLIPROXYAPI_MANAGEMENT_CIDRS`、`CONTROL_CLIPROXYAPI_PLAIN_HTTP_CIDRS`、`CONTROL_CLIPROXYAPI_CA_FILE` 均被忽略，不再提供限制。当前版本不接受内部 HTTPS；回滚旧版本前恢复其所需旧配置与证书，否则停用受影响采集。

### 验证与故障定位

Control的runtime循环为ReconcileTick→WorkOnce，后者在每Gateway内部调度；5秒是检查间隔，DB槽180秒，freshness 540秒。每attempt最多5秒，失败记录单独最多5秒，不延长15秒lease；未知提交按fencing/reconciler恢复。

启用后检查既有Directory metrics与current accepted snapshot，再用既有candidate/read API核对decimal-string ID。bind/rebind只通过已有管理员入口执行，不从名字猜测身份。关闭source或token错误时Binding保留，观察过期后resolution变unknown；恢复有效采集后再派生resolved。public proxy的`/internal/v1/`必须拒绝访问。
