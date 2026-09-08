# Planning Validation

2026-09-09，Control baseline f25eb89，ops baseline 4e4be1e，两仓起始干净。最初用户批准本地移除TLS并执行，随后明确要求暂停开发；当前授权仅继续规划修订，实施和部署均暂停，不push/archive。Gateway Directory当前内部HTTP、Node两个HTTP入口保留。未读取或记录Secret值。

## Simplified Architecture Decision

2026-09-09，用户接受移除 CONTROL_COOKIE_SECURE、不新增 CONTROL_DEV_ALLOW_INSECURE_HTTP。冻结dev固定HTTP、staging/production固定HTTPS；复用现有Cookie/CSRF/同源机制，不增加传输策略系统。旧变量不再解析或影响结果；开发环境不再单独选择HTTPS。本地容器非loopback由dev策略允许，宿主仅loopback映射由ops负责。

本轮仅修订proposal/design/spec/tasks与本证据文档，代码和runtime未改变。7项任务中仅规划项完成，6项实施/验收/部署任务保持未完成。不得把设计strict通过当成实现测试通过。

## Implementation and Validation

用户后续明确要求“提交规划文档，再实施、测试”，本轮实施已授权，本地运行环境仍不部署。规划提交 `e34a353`，Control 实现提交 `473af11`。

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

独立代码审查PASS，无已知P1/P2。变更与设计一致，无新增API/DB/collector/Node/Gateway产品能力；staging/production边界未放宽。change strict PASS，all strict 18/18 PASS，两仓diff check PASS。

实施与验收6/7项完成；3.3实际部署未执行，不可宣称TLS已从当前本地运行环境移除，不archive。两仓分阶段本地提交，未push；等待Implementation Final Review与后续部署指令。
