## MODIFIED Requirements

### Requirement: 每次连接都执行目标 allowlist 与 SSRF 防护

原目标allowlist与SSRF防护要求 SHALL 被统一管理出站契约替代：CLIProxyAPI Driver直接允许HTTP/HTTPS，不检查DNS/IP/CIDR许可、特殊地址类别或DNS重绑定。HTTPS MUST 不验证证书链、有效期或主机名。仍解析请求URL并使用普通DNS/拨号；固定方法、路径、Secret和响应契约不变。

#### Scenario: HTTP 不在隔离管理网
- **WHEN** 合法HTTP endpoint未出现在任何列表且返回合法响应
- **THEN** Driver正常执行对应观察，不因目标许可拒绝

#### Scenario: DNS 结果混合或发生重绑定
- **WHEN** 合成本地fixture使目标为loopback/link-local类别或DNS结果变化
- **THEN** Driver不因地址类别、混合DNS结果或重绑定检查拒绝，按普通连接结果处理；测试不得访问真实元数据服务

#### Scenario: 授权 HTTPS hostname
- **WHEN** 已登记HTTPS hostname可连接并返回合法响应，无论是否匹配旧allowlist
- **THEN** Driver正常完成观察，不检查目标许可或服务端证书

#### Scenario: 环回、链路本地或云元数据目标
- **WHEN** endpoint或DNS结果在合成fixture中属于上述地址类别
- **THEN** Driver不按地址类别阻止连接，按普通网络结果处理，不在验收中访问真实云元数据服务

#### Scenario: 不可信HTTPS证书
- **WHEN** 目标使用自签名、未知CA、过期或hostname不匹配证书且TLS握手和响应合法
- **THEN** Driver观察成功，不因证书校验拒绝

#### Scenario: 网络或证书恢复
- **WHEN** DNS、连接或TLS握手失败后外部服务恢复
- **THEN** 后续调用可恢复，失败期间不产生成功观察，不自动改用HTTP

### Requirement: Driver 只允许两个固定 GET 且不使用代理或重定向

CLIProxyAPI Driver MUST 只生成对已登记 base endpoint 下 `/healthz` 和 `/v0/management/auth-files` 的 GET，不得接受调用者提供的 method、path、query、Host、header 或 body。专用 transport MUST 忽略本地代理环境，MUST 不保存 cookie、不自动重试并拒绝所有 HTTP redirect。

#### Scenario: 环境配置了本地代理
- **WHEN** Control 进程存在 uppercase 或 lowercase HTTP(S)/ALL proxy 环境变量
- **THEN** Driver 仍直接连接management endpoint解析出的IP，Management Key 和响应不经过代理

#### Scenario: Node 返回重定向
- **WHEN** 任一固定路径返回 3xx 和不同或相同目标 Location
- **THEN** Driver 返回 `redirect_rejected`，不发起第二个请求，也不向 Location 发送 Management Key

#### Scenario: 调用者尝试其他管理路径
- **WHEN** 内部调用者尝试指定 download、upload、patch、delete 或任意其他路径/方法
- **THEN** Driver API 无法表达该请求，底层固定路径检查也必须拒绝任何不一致

#### Scenario: 请求超时或取消
- **WHEN** 连接超过默认 3 秒、总调用超过默认 15 秒或 context 被取消
- **THEN** Driver 有界终止并返回固定 timeout/cancelled 分类，不自动重复请求

### Requirement: Driver 观测保持低基数且不泄露敏感数据

Control SHALL 只以固定 node type、operation、result/reason 记录 Driver 聚合指标和结构化日志。instance ID、endpoint、hostname/IP、provider、email、版本/提交、Secret 引用、Management Key、请求/响应 header、原始错误和 body MUST NOT 成为指标标签、日志字段、Trace 属性、审计详情或测试证据。

#### Scenario: 成功和失败观测
- **WHEN** Probe 或 inventory 调用成功、超时、DNS 失败、TLS 失败、HTTP 失败或契约无效
- **THEN** 指标和日志只包含注册的固定枚举，不产生逐 Node、逐 Provider 或逐账号序列

#### Scenario: 注入敏感 canary
- **WHEN** endpoint、Secret 引用、Management Key、email、上游字段、错误和响应正文包含唯一 canary
- **THEN** canary 只可在测试输入和单次必要内存 DTO 中出现，不得出现在任何观测、错误字符串、持久存储或报告

#### Scenario: 观测后端失败
- **WHEN** 日志或指标输出失败
- **THEN** Driver 的方法/路径、Secret、固定方法/路径、响应限制和契约判定保持原有约束，传输采用统一管理出站策略，不以观测失败为由放宽安全边界

### Requirement: Driver 不持久化、不自动调度且不影响数据面

本 change 的 CLIProxyAPI Driver SHALL 只在显式调用期间执行只读观察，MUST NOT 创建数据库记录、durable job、Outbox、周期 goroutine、账号状态、告警或 Node/Gateway 写入。Driver、Control 或 management 网络不可用 MUST NOT 影响 Gateway 和 Relay Node 的既有请求处理。

#### Scenario: 仅启动 Control
- **WHEN** 部署包含 Driver 和安全配置但没有后续 poll-run 调用者
- **THEN** Control 不向任何 Node 发起请求，也不创建后台任务或账号证据

#### Scenario: Driver 调用失败或 Control 停止
- **WHEN** management DNS/网络/Node/Control 不可用
- **THEN** 只读观察失败或暂停，Gateway/Node 数据面继续独立运行且不被 Driver 修改

#### Scenario: 官方镜像冒烟
- **WHEN** 验收对官方 CLIProxyAPI v7.2.141 原版镜像和最多两个受控测试 Node 执行健康与账号只读调用
- **THEN** Node 产品代码、配置和账号保持不变，请求串行且相邻至少等待 10 秒，证据只保存固定分类和计数

#### Scenario: 应用回滚与恢复
- **WHEN** Control 回滚到不包含 Driver 的版本后再升级
- **THEN** 数据库和 Node 无需迁移或修复；恢复对应版本配置后下一次显式调用重新解析Secret并发起请求；新版本不恢复旧DNS/IP许可或证书检查
