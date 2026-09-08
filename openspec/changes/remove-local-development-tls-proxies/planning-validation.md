# Planning Validation

2026-09-09，Control baseline f25eb89，ops baseline 4e4be1e，两仓起始干净。最初用户批准本地移除TLS并执行，随后明确要求暂停开发；当时授权仅继续规划修订，实施和部署均暂停，不push/archive。Gateway Directory当前内部HTTP、Node两个HTTP入口保留。未读取或记录Secret值。

## Simplified Architecture Decision

2026-09-09，用户接受移除 CONTROL_COOKIE_SECURE、不新增 CONTROL_DEV_ALLOW_INSECURE_HTTP。冻结dev固定HTTP、staging/production固定HTTPS；复用现有Cookie/CSRF/同源机制，不增加传输策略系统。旧变量不再解析或影响结果；开发环境不再单独选择HTTPS。本地容器非loopback由dev策略允许，宿主仅loopback映射由ops负责。

规划阶段仅修订proposal/design/spec/tasks与本证据文档，代码和runtime未改变。当时7项任务中仅规划项完成，6项实施/验收/部署任务保持未完成。不得把设计strict通过当成实现测试通过。

## Implementation and Validation

用户后续明确要求“提交规划文档，再实施、测试”，当时实施已授权，本地运行环境尚不部署。规划提交 `e34a353`，Control 实现提交 `473af11`。

- Control：删除环境变量解析及可配置Cookie字段，以 `Config.CookieSecure()` 按Environment派生；所有构造路径一致，unknown环境/keyring/production MFA限制保留。删除dev进程loopback限制，Compose宿主loopback映射负责网络边界；不引入新开关。旧变量在当前代码中只作为退休回归测试输入；固定旧revision的历史回滚fixture仍保留旧binary所需变量，不能以新契约修改旧binary验收。
- ops：删除TLS服务、初始化、listener和证书要求，Gateway公共HTTP入口保留。Control operations变为单服务，Gateway继续应用+proxy，旧Control pair pending的update/directory/recovery-drill均显式拒绝。首次拓扑转换需完整发布、无pending和私有备份，不能绕过same-schema gate；历史证书/卷不删除。详见ops `dev/OPERATIONS.md`。System Design已记录仅本地dev的部署例外。

### Control Evidence

`TestCookieTransportIsDerivedOnlyFromEnvironment`：dev/staging/production × loopback/wildcard/IPv6 × 旧变量true/false/invalid，配置验证及内部派生一致。
`TestRetiredCookieVariableDoesNotAffectStartup`：真实main子进程不再解析旧变量，dev wildcard通过认证配置校验并到达独立数据库配置检查。
`TestEnvironmentCookieAndOriginPolicy`：Cookie名称、Secure、HttpOnly、SameSite、Domain与HTTP/HTTPS Origin矩阵；不由X-Forwarded-Proto覆盖。
`TestDevHTTPLoginSessionAndCSRF`：真实PostgreSQL/runtime role，合成管理员登录，HTTP dev Cookie、session读取、CSRF轮换、跨源/错误scheme/缺失与错误proof拒绝，正确logout及会话撤销。

首次PG执行发现共享测试库没有auth表，未将其误报为实现缺陷或PASS；随后在localhost:55432新建隔离 `relay_control_tls_policy_test` 并用既有migration初始化，未操作实际部署55434。HTTP测试初版未使用GetSession轮换后的CSRF，已按真实Web行为读取新proof，保留全部负例。

设置 README 风格的 `CONTROL_DATABASE_TEST_URL` 和 `CONTROL_RUNTIME_DATABASE_TEST_URL` 指向上述隔离库后：

```sh
go test -race ./internal/api ./internal/auth ./cmd/control -run 'TestDevHTTPLoginSessionAndCSRF|TestCookieTransport|TestConfigFailClosed|TestEnvironmentCookieAndOriginPolicy|TestHTTPAuthenticationRouteMatrix|TestSameOriginPolicy|TestRetiredCookieVariable' -count=1
```

PASS：API 10.011s，auth 1.984s，cmd/control 2.759s。此前runtime管理员权限/日志canary专项亦PASS。`make test build` PASS，frontend 20 files/127 tests PASS，构建成功；Go写module stat cache权限警告非致命，命令退出0。没有API/生成代码/数据库migration变更。

### Ops Evidence

```sh
bash -n dev/devctl
RELAY_DEV_CONTAINER_TEST=1 python3 -m unittest discover -s dev -p 'test_*.py'
```

64 tests / 39.940s，全部PASS，无skip。包括真实隔离Docker Control坏镜像回滚、缺容器恢复、正常更新/peer保护；旧TLS pending拒绝；真实Compose渲染只loopback HTTP、无TLS服务/挂载/配置开关；真实nginx的internal Directory路径403，普通路径在合成缺失upstream下502（证明未一律拒绝公共入口）。测试容器独立于relay-station-dev。

沙箱首次阻止Docker构建缓存写入，随后使用获准环境重跑完整ops测试通过；不把前次环境失败计为PASS。

### Review and Delivery State

首次独立代码审查曾报告PASS，随后Final Review发现1项P2：legacy TLS pending在prepared/applying的recover入口中先改写owner，之后才拒绝不兼容记录。此前仅内部校验测试通过，不能证明入口无副作用。Architecture PASS，Implementation当时BLOCKED；后续修复与验收见下节。变更无新增API/DB/collector/Node/Gateway产品能力，staging/production边界未放宽。

当时实施与验收6/7项完成；3.3实际部署未执行，不可宣称TLS已从当前本地运行环境移除，不archive。两仓分阶段本地提交，未push；本轮修复复核通过，后续部署仍待执行。

### Final Review P2 Reconciliation

2026-09-09，用户授权继续修复。ops提交 `445ad33` 将既有Control legacy shape检查提取复用，并在 `recover()` 中于owner检查、接管及pending写入之前执行；未改变Gateway恢复策略、正常恢复校验或产品契约。

`test_legacy_control_tls_pending_recover_rejects_before_adoption` 通过真实临时pending文件和完整recover入口，覆盖update/directory/recovery-drill × prepared/applying/verified × 两种独立legacy条件（旧pair且无proxy字段、单Control但残留old.proxy），共18组。每组均拒绝为 `recovery_record_incompatible`；pending原始字节及owner不变、未进入owner接管检查、runner零调用。临时跳过前置检查的回归有效性实验按预期失败，未保留实验改动。

```sh
RELAY_DEV_CONTAINER_TEST=1 python3 -m unittest discover -s dev -p 'test_*.py'
```

最终65 tests / 39.952s，全部PASS、无skip，包括真实隔离容器更新/失败回滚/中断恢复。首次沙箱内运行3个Docker构建因缓存权限失败；获准环境重跑通过，不计首次为PASS。此次仅ops恢复顺序及测试、Control证据文档变更，复用上文已通过的Control Go/PG/race/make证据，无需重跑无关构建。

主Agent复核确认legacy拒绝先于pending修改，两个触发条件独立覆盖，原P2已关闭：Architecture PASS、Implementation PASS，当前无已知P1/P2。change strict PASS、all strict 18/18 PASS、两仓diff check PASS。修复验收时6/7任务完成，3.3仍未部署；未push、未archive。

### Local HTTP Deployment — PASS

2026-09-09 Asia/Shanghai，用户要求继续后完成3.3。Control部署revision `7025629d79d3253b6e10182f8eaf0a4960d47be7`，镜像 `sha256:4fa27bbdf3e1461e4c1a678cb58b741287f2c426c5b9ba7f90fa3c212a8ed5e8`；ops部署revision `445ad330d28ae028e0d979c4899c76592706a642`。Docker构建PASS，以真实revision标注，不绕过单服务same-schema gate。

持有既有RuntimeLock且确认无pending后，复用Operations备份Control/Gateway数据库与匹配配置，pg_restore --list通过（不声称完整还原演练）。备份目录使用UTC时间戳，因此日期前缀为前一天：

- `/Users/keedle/.relay-station/dev/backups/20260908T172618Z-control-7af4a4f8`
- `/Users/keedle/.relay-station/dev/backups/20260908T172619Z-gateway-31afaca9`

前者 `full-release.json` 保存阶段与不变量，最终phase=complete。首次拓扑转换按完整发布执行：更新私有override中的Control镜像并移除TLS服务配置；同步Gateway HTTP nginx配置；仅force-recreate Control和gateway-proxy，显式核对project/service标签后停止并移除control-tls/control-tls-init两个退役容器。不使用down -v/remove-orphans，不删除数据或证书volume，不运行migration。

部署后实际断言全部PASS：

- Control HTTP UI与health、Gateway HTTP UI与health、两个Node health返回200；八个长运行服务均healthy。
- 新会话HTTP登录成功，Cookie为non-Secure/HttpOnly/SameSite Strict；session读取成功，错误/缺失CSRF、跨源和错误scheme logout均403；正确logout204，撤销后session401。未输出或提交凭据、Cookie、CSRF。
- Gateway `/internal/v1/api-account-directory` 返回403；127.0.0.1:18443/18444均不可连接；新公开入口均绑定loopback。
- Gateway应用、两个Node、数据库、Redis及未参与发布的init容器ID/image/config摘要完全不变；两个Node各3个账号文件及原TLS证书逐文件hash不变。
- Control管理员/Node/Binding/Inventory记录数量不变；Goose保持25，所有原project数据/证书卷仍存在。未对持续采集的事件总数作不合理的恒等断言。

当前入口：Control `http://127.0.0.1:18080/`，Gateway `http://127.0.0.1:18082/`，Node管理 `http://127.0.0.1:18319/management.html`、`http://127.0.0.1:18320/management.html`。旧临时local-stack.sh不作为执行入口，沿用仓库devctl及正式runbook。

7/7任务完成。文档对账后change strict PASS、all strict 18/18 PASS、两仓diff check PASS。本地部署完成，未push、未archive。
