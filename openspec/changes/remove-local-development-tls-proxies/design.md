## Context

当前 Control 容器 0.0.0.0:8080 映射宿主127.0.0.1:18080，但 CookieSecure=false 只允许进程 loopback，不能直接删除代理。sameOrigin 已支持 dev HTTP，Cookie 名称也有 dev 分支。Gateway Directory 实际直接走内部HTTP，不依赖8444。

## Goals / Non-Goals

本地开发无 TLS，保留所有数据/账号/会话认证/CSRF、Gateway公共HTTP路由隔离及Node运行。非目标：生产HTTP、取消授权、CSRF或Host校验、Gateway服务端改造、外部监听、数据清理。

## Decisions

- 不新增任何配置开关。移除 CONTROL_COOKIE_SECURE 的环境变量解析，以既有 Environment 为唯一策略输入：dev 派生 CookieSecure=false、HTTP同源、既有dev Cookie名称；staging/production派生 CookieSecure=true、HTTPS同源与既有安全Cookie名称。内部派生值可保留以复用现有Cookie代码，不再是调用方可配置字段；派生必须在配置校验/构造的唯一入口完成，不能只在main赋值却让其它调用路径绕过环境策略。未知环境继续拒绝，production MFA规则不变。
- dev 允许0.0.0.0容器监听和原有loopback监听，不再以进程loopback绑定决定Cookie策略。本地Compose宿主映射固定127.0.0.1；应用不新增Docker探测，也不声称能验证宿主端口映射。此变化减少防误配置的一层限制，网络隔离由部署配置和验收负责；不允许将本地模板改成公网发布。
- 旧 CONTROL_COOKIE_SECURE 彻底退休，不保留兼容分支、布尔解析或双轨模式；遗留变量无论true/false均不影响环境派生结果。实施时从已知模板、脚本和私有override移除，文档明确旧值不再起效；不设计配置转换worker。拟议的 CONTROL_DEV_ALLOW_INSECURE_HTTP 从未实施，也不引入。
- CSRF token、HttpOnly、SameSite、会话存储及API权限不变。HTTP/HTTPS同源仍使用现有逻辑，只改其策略来源；不通过X-Forwarded-Proto自动选择策略。dev不再单独支持HTTPS登录模式，访问URL切换后重新登录；旧Secure Cookie不转换为HTTP Cookie，不做会话数据库迁移。
- 删除control-tls、control-tls-init与TLS卷声明/挂载/生成/必需文件检查；历史文件、卷和备份不清理。Gateway nginx仅HTTP8082，宿主18082、internal/v1保持403；内部Directory endpoint、token及restricted Docker网络不改。此为本地部署例外，不修改生产GatewayTLS规范。
- Control operations改为单服务，Gateway仍应用+HTTP代理；复用锁、pending、备份、revision/schema/peer核验。旧带TLS拓扑pending必须先显式旧流程恢复，不能按新拓扑误处理；历史完成记录保留。无pending时迁移现有runtime override，仅改Control HTTP配置/镜像和退役TLS服务配置，先私有备份。移除已退役容器需目标明确，不使用remove-orphans或down -v。
- 运行服务短暂重建；仅Control及Gateway HTTP代理允许变动，Gateway应用和Node不重建。失败停止并保留备份/阶段记录；回滚恢复旧ops版本、override和旧Control镜像，可重新挂载原证书，不执行数据库回滚。HTTP入口基础/认证/CSRF负例及Gateway/internal隔离检查通过后完成。
- 不新增时间逻辑、审计或metrics；UTC存储及TZ Asia/Shanghai保留，不输出Secret或账号。

## Validation

Config矩阵：dev在loopback/容器监听均派生HTTP和non-Secure dev Cookie，staging/production始终派生HTTPS和Secure Cookie，未知环境拒绝；遗留CONTROL_COOKIE_SECURE=true/false不能覆盖派生结果，各构造路径不能绕过策略。HTTP登录/session/Cookie与same-origin/CSRF正负例。ops单Control更新/故障/中断恢复、Gateway保留proxy、旧pending拒绝、缺TLS文件初始化有效、Compose只loopback公开。Control make test build与针对性Go/race/必要PG，ops Python tests和真实隔离容器恢复验收，OpenSpec strict及两仓diff检查。发布备份后验证HTTP Control、Gateway/internal403、无18443/18444listener、Node/Gateway应用容器不变。

## Planning Status

2026-09-09用户先要求暂停开发，随后批准上述按环境派生的精简方向。本轮只修改OpenSpec，未修改代码、ops、运行配置或服务。实施和部署任务保持未完成，等待后续指令。
