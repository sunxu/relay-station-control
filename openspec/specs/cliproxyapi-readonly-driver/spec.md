# cliproxyapi-readonly-driver Specification

## Purpose
为 Control 提供只读、固定路径、运行时 Secret 隔离的 CLIProxyAPI Node Driver，管理出站直接支持 HTTP/HTTPS 且不执行目标许可或 HTTPS 证书验证，并将官方 auth-files 响应转换为有界、脱敏、可供持久采集使用的内存观察结果。

## Requirements

### Requirement: Control 只注册固定 Node Driver 与能力

Control SHALL 使用代码定义的 Node Driver registry，并 MUST 只为已登记 node type 和 capability 调用对应方法。首个 `cliproxyapi` Driver MUST 支持健康探测和账号清单读取；未知 node type、重复 Driver 或缺少 capability MUST 在 Secret 解析、DNS 和网络调用前 fail closed。

#### Scenario: 调用已登记 CLIProxyAPI Driver
- **WHEN** `cliproxyapi` Node 声明对应只读 management capability，调用方请求健康或账号清单观察
- **THEN** registry 选择固定 CLIProxyAPI Driver 并只执行该能力定义的操作

#### Scenario: 未知类型或缺少能力
- **WHEN** Node type 未登记，或 Node 未声明所请求的 management capability
- **THEN** Control 返回固定 unsupported 分类，不解析 Secret、不访问 DNS，也不发起网络请求

#### Scenario: 重复注册 Driver
- **WHEN** 启动代码尝试为同一 node type 注册第二个 Driver
- **THEN** Control 在提供 Driver 服务前拒绝该 registry，不能按注册顺序静默覆盖实现

### Requirement: Management Secret 仅在运行时受保护地解析

CLIProxyAPI Driver MUST 只把数据库中的 opaque Secret 引用交给受控 runtime resolver，并 MUST NOT 把引用文本当作 Management Key。Management Key MUST 只进入单次 auth-files 请求的 `X-Management-Key` header，不得进入数据库、URL、query、cookie、健康请求、API、UI、日志、指标、Trace、审计、错误或测试证据。

#### Scenario: 解析有效 Secret 引用
- **WHEN** 已登记引用在受保护 resolver 配置中映射到安全且有界的 Secret 文件
- **THEN** Driver 为单次 auth-files GET 设置 Management Key header，并在请求完成后不保留可观测副本

#### Scenario: 引用未知或 Secret 文件不安全
- **WHEN** 引用未映射、provider 不支持、文件不是普通文件、可被非 owner 写入、为空或超过上限
- **THEN** Driver 在任何 DNS/网络调用前返回固定 Secret 错误分类，且响应和日志不包含引用、路径或文件内容

#### Scenario: 健康探测
- **WHEN** Driver 调用 `/healthz`
- **THEN** 请求不携带 Management Key、cookie 或其他管理凭证

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

### Requirement: 健康探测不表示账号或数据面可调度

CLIProxyAPI Driver SHALL 仅在 `/healthz` 返回 HTTP 200、未超限的 JSON object 且 `status="ok"` 时报告进程健康接口可达。该结果 MUST NOT 被解释为账号池完整、Provider 可用、Gateway enabled 或 Node 可调度，也 MUST NOT 修改 Gateway/Node 状态。

#### Scenario: 合法健康响应
- **WHEN** 固定健康路径返回 `200` 和 `{"status":"ok"}`
- **THEN** Driver 返回健康观察成功，且不读取账号接口或创建持久状态

#### Scenario: 非 200、非法或超限响应
- **WHEN** 健康路径返回非 200、非法 JSON、非 ok 状态或超过健康响应上限
- **THEN** Driver 返回固定失败分类，不返回状态文本或原始 body

#### Scenario: 健康成功但账号接口失败
- **WHEN** `/healthz` 成功而 auth-files 传输或契约失败
- **THEN** 两个观察保持独立，Control 不把健康成功提升为账号清单成功

### Requirement: auth-files transport 与契约结果严格分层且有界

CLIProxyAPI Driver SHALL 将 HTTP 状态、JSON/`files` 形态和 inventory mode 分层。响应体 MUST 默认不超过 5 MiB，记录 MUST 默认不超过 1,000 条；超限、JSON 非法、缺少/非法 `files` 或无法判定 mode 时 MUST fail closed，且原始响应不得持久化、记录或返回。

#### Scenario: HTTP 非 200
- **WHEN** auth-files 返回任意非 200 状态
- **THEN** `transport_success=false`、`response_shape_valid=false`、`contract_valid=false`，Driver 不解析或回显错误 body

#### Scenario: HTTP 200 但 JSON 或 files 无效
- **WHEN** 响应为 200，但 JSON 非法、顶层错误、缺少 `files` 或 `files` 不是数组
- **THEN** `transport_success=true`、`response_shape_valid=false`、`contract_valid=false`，mode 保持为空

#### Scenario: 响应体或记录数超限
- **WHEN** 编码 body 超过 5 MiB 或 `files` 超过 1,000 条
- **THEN** Driver 有界停止处理，返回契约无效分类且不保留部分账号结果

#### Scenario: 版本与提交响应头缺失或非法
- **WHEN** `X-CPA-VERSION` 或 `X-CPA-COMMIT` 缺失、超长或字符非法
- **THEN** 对应观察值为固定 `unknown`，不使用资产期望版本冒充，也不使其他合法契约结果失败

### Requirement: Driver 以白名单解析并正确判定 inventory mode

CLIProxyAPI Driver MUST 只投影批准的账号观察字段，并 MUST 在边界丢弃路径、project/auth/token/account/name及所有未知字段；仅对Antigravity runtime source=file允许有界解析status_message中的批准错误标识符，输出安全availability枚举后立即丢弃原文，不读取access/refresh Token值。非空响应全部 `source=file|memory` SHALL 判为 runtime；非空响应全部缺少 source 且符合固定磁盘形态 SHALL 判为 disk fallback；空数组 SHALL 保守判为 disk fallback；混合缺失、未知 source 或未知形态 MUST 使契约无效。

#### Scenario: file 与 memory runtime 响应
- **WHEN** 非空 `files` 的每条记录都有 `source=file|memory`，包括两者混合
- **THEN** Driver 返回 `inventory_mode=runtime`，且不得把 `source=file` 判为磁盘降级

#### Scenario: 磁盘降级或空数组
- **WHEN** 全部记录缺少 source 且符合固化磁盘形态，或 `files` 为空
- **THEN** Driver 返回 `inventory_mode=disk_fallback` 和 degraded 结果，不宣称完整 Provider 快照

#### Scenario: 来源形态混合或未知
- **WHEN** 同一非空响应混合带/不带 source、出现未知 source，或不符合任一固化形态
- **THEN** `contract_valid=false`、mode 为空，Driver 不按多数记录猜测

#### Scenario: 上游包含敏感或未知字段
- **WHEN** 记录包含 status message、路径、项目 ID、认证索引、Token、account/name 或新增未知字段
- **THEN** Driver丢弃这些字段原文；仅Antigravity file模式的批准错误标识符可转为固定安全枚举。合法白名单字段仍可解析，禁用原文不进入DTO、日志、指标、Trace或错误

#### Scenario: Safe runtime projection without changing inventory truth
- **WHEN** Antigravity file账号有明确active/disabled/unavailable及可选错误语义
- **THEN** 仅附加file_active/file_disabled/file_error/file_unavailable/file_unknown与固定auth子原因；缺字段为未证明，不改变现有basic_status、completeness、promotion或Duplicate eligibility

#### Scenario: Unknown error text does not poison collection
- **WHEN** status_message过大、结构过深或未匹配白名单
- **THEN** 认证原因保守为other，原文丢弃；不因扩展解析破坏原合法Inventory采集，也不调用新端点

### Requirement: Provider 身份与完整性按不可变策略独立计算

CLIProxyAPI Driver SHALL 使用调用时提供的不可变 Provider 策略快照，对 provider/email 进行 trim/lower 后分类。缺少 provider MUST 阻止所有 active Provider 完整；active Provider 缺少/空 email 或节点内重复 MUST 只阻止该 Provider；out-of-scope 与新 unsupported Provider MUST 分别计数且不写成 active 账号。disk fallback、契约无效或身份不完整 MUST NOT 产生完整 Provider 快照。

#### Scenario: 一个 active Provider 身份不完整
- **WHEN** runtime 响应中某 active Provider 存在缺失 email 或相同规范化 provider/email 重复，而其他 active Provider 完整
- **THEN** 仅问题 Provider 的 `provider_snapshot_complete=false`，其他 Provider 可保持完整

#### Scenario: 记录缺少 provider
- **WHEN** 任一记录缺少 provider 或规范化后为空
- **THEN** `node_identity_complete=false`，全部 active Provider 均不完整，并增加固定无法识别计数而不保存原记录

#### Scenario: Provider 零记录
- **WHEN** 合法非空 runtime 响应中某 active Provider 没有记录且不存在全局 provider 缺口
- **THEN** Driver 仍为该 Provider 产生完整的空观察结果，供未来状态层决定 missing

#### Scenario: out-of-scope 与 unsupported Provider
- **WHEN** 记录属于策略登记的 out-of-scope Provider或全新 Provider
- **THEN** Driver 分别计数；out-of-scope 不令 Node 汇总降级，unsupported 可令 Node 汇总降级，但两者都不阻止其他 active Provider 完整

#### Scenario: disk fallback
- **WHEN** inventory mode 为 disk fallback
- **THEN** 所有 active Provider 均为 degraded 且 snapshot incomplete，不推进任何账号可用、missing 或删除结论

### Requirement: Driver 观测保持低基数且不泄露敏感数据

本 Driver 聚合观测 SHALL 只以固定 node type、operation、result/reason 记录 Driver 聚合指标和结构化日志。instance ID、endpoint、hostname/IP、provider、email、版本/提交、Secret 引用、Management Key、请求/响应 header、原始错误和 body MUST NOT 成为指标标签、日志字段、Trace 属性、审计详情或测试证据。 这是 Driver 本身的最小字段契约，不是全局邮箱敏感分类：普通业务邮箱可按其他已批准 API/UI/DB/audit/controlled business logs/DingTalk body 契约完整使用，不要求 mask/HMAC 或邮箱专属权限；Prometheus/Alertmanager labels 仍禁止 raw email/account_key，Secret 边界不变。

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
