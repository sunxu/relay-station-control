## ADDED Requirements

### Requirement: Control MUST 按环境派生 HTTP 与 Cookie 策略

Control SHALL 以既有Environment作为HTTP/HTTPS认证策略的唯一配置输入。dev MUST使用HTTP同源、non-Secure的既有dev会话Cookie，允许loopback或容器非loopback listener；staging/production MUST使用HTTPS同源和Secure Cookie。Control MUST移除CONTROL_COOKIE_SECURE配置，不新增CONTROL_DEV_ALLOW_INSECURE_HTTP；遗留配置不得覆盖环境策略，未知环境仍拒绝。所有构造路径 MUST 保持同一派生规则。dev不再提供独立HTTPS登录模式，production MFA要求不变。

HTTP模式 MUST 保留服务端会话、HttpOnly、SameSite、同源与CSRF校验，不改变API权限。ops本地部署 MUST 仅发布127.0.0.1端口，Control直连HTTP且Gateway公共HTTP入口继续拒绝/internal/v1/。MUST保留现有数据与历史备份。

#### Scenario: 本地容器 HTTP
- **WHEN** dev容器监听0.0.0.0且宿主仅loopback映射，未设置任何Cookie或HTTP开关
- **THEN** Control正常启动，浏览器可通过HTTP登录和已授权查询，不依赖TLS代理或证书，Cookie保持HttpOnly/SameSite且不设置Secure

#### Scenario: loopback开发兼容
- **WHEN** dev使用原有loopback listener
- **THEN** 仍使用HTTP及dev Cookie，不需要额外配置

#### Scenario: 非开发环境固定HTTPS
- **WHEN** staging或production启动并处理浏览器认证请求
- **THEN** Cookie始终Secure且同源要求HTTPS，HTTP Origin的管理POST拒绝；production MFA约束保留

#### Scenario: 旧配置不能覆盖环境
- **WHEN** dev/staging/production运行环境遗留CONTROL_COOKIE_SECURE=true或false
- **THEN** 该变量不再参与解析或选择策略，结果始终由Environment确定；模板和部署说明不再要求此变量

#### Scenario: 切换入口重新登录
- **WHEN** 本地环境从旧HTTPS入口切换到HTTP入口
- **THEN** 用户使用dev Cookie重新登录，旧Secure Cookie不被转换，不修改会话存储schema或账号数据

#### Scenario: HTTP认证与CSRF
- **WHEN** 本地HTTP浏览器发送有效session/同源/CSRF，或缺少session、跨源、错误CSRF
- **THEN** 前者按既有授权执行，后者按既有401/403策略拒绝，不把HTTP视为免认证

#### Scenario: Gateway内部入口隔离
- **WHEN** 宿主通过18082请求/internal/v1/，或Control通过内部http://gateway:8080调用Directory
- **THEN** 前者403，后者仍需原service token；Node及数据面行为不变

#### Scenario: 无代理更新与恢复
- **WHEN** 无TLS本地拓扑执行Control更新或中断恢复
- **THEN** 仅重建Control且保留锁/备份/schema/peer检查；旧TLS pending不得按新拓扑恢复；历史数据和证书备份不删除
