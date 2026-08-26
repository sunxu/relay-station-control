## Context

Control 已有 `relay_node_assets`、Driver/capability 目录、opaque `reader_secret_ref`、管理员只读资产页面和通用持久任务基础，但尚无任何 Node 出站 Adapter。系统设计 v1.0 第 9.1–9.6、19、23、24 节要求 Control 的 Node Driver 与 Gateway 的 Node Connector 分离：Control 只调用 CLIProxyAPI 已有 `/healthz` 与 `/v0/management/auth-files`，Gateway 和浏览器不得访问管理接口，CLIProxyAPI 不增加 Relay Station 专用代码。

阶段 0 已在 `ops/contracts/cliproxyapi/auth-files/v1` 固化脱敏契约样本和 v7.2.141 兼容性基线。样本证明 `source=file` 是正常 runtime 形态，空数组应保守判为 disk fallback，未知字段必须丢弃。Node Management Key 具备管理写权限，因此即使本 Driver 只发 GET，也必须把凭证、目标解析和 HTTP 行为视为高风险边界。

本 change 不建立采集调度或数据库真相。Driver 返回一次调用的内存观察结果；后续 change 才能定义 UTC poll slot、`poll_start_grace`、租约、事务化证据、Provider 当前快照和账号生命周期。这样网络调用不会被通用 durable job 自动重放，也不会把 CLIProxyAPI 专用 poll 状态强塞入 `async_jobs`。

## Goals / Non-Goals

**Goals:**

- 提供固定、可注册且不绑定数据面的 Node Driver 契约，并实现首个 `cliproxyapi` Driver。
- 在 Management Key 加入请求前完成 Secret 解析、目标 allowlist、逐次 DNS/IP、方法/路径、代理、重定向和 TLS 检查。
- 对健康与 auth-files 响应执行严格大小/数量/形态边界，只返回固定分类和字段白名单 DTO。
- 使用阶段 0 fixtures 证明 runtime/disk fallback、身份缺口、Provider 作用域和节点内重复分类一致。
- 让所有失败可恢复、可观察且不泄露 endpoint、Secret、email、原始错误或响应。

**Non-Goals:**

- 不新增或修改 PostgreSQL Migration、sqlc、OpenAPI、HTTP handler、React 页面或产品写入口。
- 不创建 `account_inventory_poll_runs`，不按五分钟槽调度，不持久化任何账号、计数、版本头或 Driver 结果。
- 不推进 missing/out-of-scope、当前快照、跨 Node 重复、日级汇总、压缩或告警状态。
- 不把 Driver 注册成 durable-job Executor，不自动重试结果未知的网络调用。
- 不实现 Sub2API Adapter、Prometheus Adapter、Gateway Node Connector、Docker/GitHub/SSH 操作或 CLIProxyAPI 修改。
- 不把 `/healthz=200` 解释为账号可调度或数据面健康。

## Decisions

### 1. Node Driver registry 固定类型与能力，不提供任意 URL 调用器

在 `internal/drivers` 定义小型接口：`Probe` 和 `ListAccountInventory` 接受受控 `NodeTarget`/`InventoryRequest`，返回固定 `ProbeObservation`/`InventoryObservation`。`NodeTarget` 只携带稳定 instance ID、固定 node type/Driver 合约版本、规范化 management endpoint、Secret 引用及已登记 capability；它不接受任意方法、路径、header 或请求 body。

registry 以固定 `node_type` 为 key，拒绝重复注册和未知类型。首期仅登记 `cliproxyapi`；健康调用要求 `management_health_read`，账号清单要求 `management_account_inventory_read`。缺少 capability 返回固定 `capability_unsupported`，且在解析 Secret 或 DNS 前结束。未来 Node 类型必须通过独立 OpenSpec change 注册，不能由数据库字符串动态加载代码。

接口返回的是本次观察而非持久状态。`Probe` 只表示 CLIProxyAPI 进程接口是否按契约响应；`ListAccountInventory` 的 transport/contract/provider 结果也不能直接成为账号当前状态，必须由未来 poll-run 事务结合固定策略版本后持久化。

替代方案是暴露通用 `Do(method, path)`。它会绕过完整路径白名单并把 Management Key 变成通用管理凭证，因此不采用。

### 2. Secret 引用通过运行时 resolver 解析，数据库值永不直接作为凭证

Driver 只接收 opaque Secret 引用，并调用 `SecretResolver.Resolve(ctx, reference)`。生产 file-backed resolver 使用受保护的引用映射配置，把引用映射到容器 Secret 文件；数据库引用不是主机路径，也不能拼接成路径。映射文件和 Secret 文件必须是普通文件、大小有界、不可被 group/other 写入，且读取错误只返回 `secret_unavailable|secret_reference_unknown|secret_file_unsafe`。

解析值去除单个末尾换行后必须非空且小于固定上限；它只在构造 auth-files 请求时写入 `X-Management-Key`，不加入 `/healthz`、URL、query、cookie、User-Agent、context value、错误包装或观测字段。HTTP 请求结束即释放引用；Go 无法保证内存擦除，因此安全性依赖短生命周期、禁止复制/格式化和进程 Secret 边界，而不宣称密码学擦除。

测试使用内存 resolver 和唯一 canary，扫描成功/失败日志、指标、trace-like 事件、HTTP 诊断和测试输出。外部 Secret Manager 客户端不在本 change；未知引用 scheme/provider fail closed，不能回退到把引用文本当作 Key。

### 3. 每次请求重新解析并锁定授权 IP，阻止 SSRF 与 DNS 重绑定

配置将目标权限拆成：允许的精确 DNS 名/后缀、允许的 management CIDR、允许明文 HTTP 的隔离 management CIDR，以及可选的测试专用 resolver/dialer 注入。配置为空、CIDR 非规范、域名含通配歧义、HTTP allowlist 超出 management CIDR 或允许环回/链路本地/未指定/多播/云元数据地址时，Control 在构造 Driver 前 fail closed。

每次连接执行：

1. 重新解析 endpoint host；IP literal 直接进入同一校验。
2. 要求主机名匹配 DNS allowlist，且全部解析结果属于授权 management CIDR；任一结果越界即拒绝整个请求。
3. 无条件拒绝 loopback、link-local、unspecified、multicast 和已登记云元数据地址，即使宽 CIDR 意外包含它们。
4. `http` 目标的每个 IP 还必须属于更窄的 plain-HTTP management CIDR；`https` 使用原始 hostname 作为 TLS ServerName，并执行系统或显式 CA 验证。
5. 自定义 `DialContext` 只拨号本次已验证的 IP，不让标准 Transport 再次解析 hostname；连接复用按目标策略隔离并设置有界 idle lifetime。

若解析为空、结果变化到越界 IP、拨号 IP 与授权集合不一致或 TLS 失败，返回固定分类且不发送 Management Key。Driver 不记录 hostname/IP/endpoint。恢复 DNS、网络或证书后，下一次显式调用重新执行全部检查即可恢复，无永久熔断或缓存放行。

### 4. HTTP 行为由代码固定：无代理、无重定向、无 body、无自动重试

Driver 使用专用 `http.Transport`，`Proxy=nil`，不读取 `HTTP_PROXY`/`HTTPS_PROXY`/`ALL_PROXY`；不共享应用默认 client、cookie jar或其他 Adapter transport。两个操作只能生成：

- `GET {normalized_base}/healthz`
- `GET {normalized_base}/v0/management/auth-files`

base path 若存在，只能作为已登记 endpoint 的固定前缀，通过 URL path join 生成并再次与预期完整路径比较。禁止调用者注入 path、query、fragment、method、Host 或 header。`CheckRedirect` 永远拒绝，因此 3xx 只形成 `redirect_rejected`，Management Key 不会发送到第二个目标。

连接超时默认 3 秒，总超时固定默认 15 秒并设安全上下限；Driver 自身不自动重试。调用方未来是否在同一 poll slot 内重试必须由 poll-run 状态机评审，不能隐藏在 HTTP client 中。直接冒烟串行执行，任意相邻 Node 管理请求之间至少等待 10 秒，防止对现有节点造成突发负载。

### 5. 健康探测只接受小型固定响应，不推断账号或调度状态

`Probe` 对 `/healthz` 使用较小响应上限，只有 HTTP 200、JSON object 且 `status="ok"` 才返回 `reachable=true`。非 200、无效 JSON、超限或其他状态返回固定 `http_status|response_invalid|response_too_large`。响应 body、状态文本和原始错误均不透出。

观察结果可以包含固定分类和调用是否完成，不能包含 response header、endpoint 或 body。即使成功也只证明进程健康接口可用，不证明账号池完整、Provider 可用、Gateway enabled 或 Node 可调度。此 change 不把 Probe 接入 Gateway 或产品 API。

### 6. auth-files 先形成 transport/shape/mode，再做 Provider 身份分类

账号清单响应使用 `io.LimitedReader(max+1)` 有界读取，默认最大 5 MiB；超限不继续读取或记录内容。JSON 顶层必须是 object 且 `files` 必须是数组，默认最多 1,000 条。HTTP 非 200 时 `transport_success=false`；HTTP 200 时无论 JSON 是否有效均为 `transport_success=true`。`response_shape_valid` 只在 JSON、`files`、body 和记录数均合法时为 true。

上游记录只解码白名单字段：`provider`、`email`、`source`、`status`、`disabled`、`unavailable`、`success`、`failed`、`recent_requests`、`last_refresh`、`next_retry_after`、`updated_at`。`status_message`、路径、project/auth/token/account/name 字段及所有未知字段不进入返回 DTO、日志或错误。字段类型混淆使契约失败；不会把数字/布尔隐式转换成字符串。

模式判定固定：非空且每条都有 `source=file|memory` 为 `runtime`；非空且全部缺少 `source` 并符合阶段 0 磁盘字段形态为 `disk_fallback`；空数组保守为 `disk_fallback`；混合有无 source、未知 source 或未知形态令 `contract_valid=false` 且 mode 为空。`source=file` 仍是 runtime。

请求携带不可变 Provider 策略快照（版本 ID、active 与 out-of-scope 集合）。provider/email 仅在内存中 trim/lower：缺 provider 令所有 active Provider 身份不完整；active Provider 缺 email/规范化为空或同 `(provider,email)` 重复只令该 Provider 不完整；out-of-scope 只计数；新 Provider 只计入 unsupported。每个 active Provider 即使零记录也产生结果。`provider_snapshot_complete` 仅在 runtime、contract valid、无全局 provider 缺口且本 Provider 身份完整时为 true。disk fallback 永不产生完整 Provider 快照。

基础状态按 disabled、unavailable/未来 retry、error、active、unknown 的固定优先级映射；计数与时间只做有界类型解析，不在本 change 计算跨轮增量、10 分钟/1 小时趋势或持久账号 key。跨 Node 重复不属于单次 Driver 结果。

### 7. 版本头、错误、日志与指标都使用封闭投影

`X-CPA-VERSION` 与 `X-CPA-COMMIT` 缺失时为 `unknown`；存在时必须满足固定长度和字符集，否则也归为 `unknown`，不回显原值。Driver 错误使用 `operation` 与固定 `reason`，不包装任意 `error.Error()`。调试模式也不能输出 request/response header 或 body。

指标只允许例如 `relay_control_node_driver_requests_total{node_type,operation,result}` 和无身份标签的 duration；`node_type`、operation、result 均来自代码枚举。禁止 instance ID、endpoint、hostname、IP、provider、email、版本/提交、Secret 引用或错误内容成为标签。日志 allowlist 为 `component=node_driver`、固定 action/result/reason/node_type；不记录逐账号事件。此 change 无管理员业务写入，因此不新增 audit row。

### 8. 测试先使用无网络 fixture，再做受控官方镜像冒烟

单元/组件测试通过注入 resolver、dialer、RoundTripper、Clock 和阶段 0 fixtures 覆盖所有分类，且断言没有真实 DNS、代理、Gateway、Node 或互联网访问。安全测试注入 endpoint、Secret、email、响应 body、DNS/IP、证书和错误 canary，扫描日志、指标、trace-like 序列化与返回对象。

集成测试使用隔离 TLS/HTTP 测试服务验证 Host/SNI、CA、重定向、body limit、慢连接/响应、取消、连接复用和 DNS 变化。plain HTTP 仅在测试专用 management CIDR 配置中允许；生产构造器不能启用 test bypass。

最后以官方 CLIProxyAPI v7.2.141 原版镜像和现有阶段 0 节点执行有界只读冒烟：不修改 Node 配置/账号，先健康再读取 auth-files，最多对两个测试 Node 各执行固定请求，所有请求串行且相邻至少 10 秒。测试只保存计数与固定分类，不保存 endpoint、Management Key、email、响应 body、路径或项目 ID。Control/Driver 停止前后 Gateway/Node 数据面继续通过既有请求验收。

## Risks / Trade-offs

- [DNS 每次解析增加延迟] → 管理采集低频且安全优先；只在单次连接内复用已验证结果，不跨请求缓存授权决定。
- [全部解析 IP 必须授权可能降低混合 DNS 可用性] → 混合授权结果是配置或 DNS 风险，fail closed 并由固定原因告警，不随机选择一个看似安全的地址。
- [Management Key 仍具写权限] → 独立管理网络、反向代理精确 GET 路径、运行时 Secret、无重定向和固定 Driver 路径共同缩小暴露面；CLIProxyAPI 权限分级需上游能力，不能由 Control 伪造。
- [内存 DTO 暂时包含 email] → DTO 不持久化、不观测、不格式化，生命周期受单次调用限制；后续持久化 change 必须另行定义数据权限与 HMAC 指标身份。
- [不自动重试降低瞬时故障成功率] → 防止 Driver 隐藏请求次数或越过未来 poll slot 宽限；重试由可审计的 poll-run 状态机统一决定。
- [官方响应新增字段] → DTO 白名单忽略未知字段；必需字段类型或来源形态变化则契约失败并保留未来最后完整快照。
- [本 change 没有产品页面可展示结果] → 这是刻意的网络/契约基础；后续 poll-run 与账号状态 change 才提供持久查询和 UI。

## Migration Plan

1. 验证资产中 `cliproxyapi` Driver/capability、management endpoint 和 opaque Secret 引用已按现有资产流程登记；准备不进入仓库的 Secret 引用映射、CA 和 management allowlist。
2. 发布包含 Driver registry、安全 transport、file-backed Secret resolver 和 fixtures 测试的 Control，但不注册调度器或 HTTP 入口；默认没有调用者，因此发布本身不产生任何出站请求。
3. 在隔离测试服务执行 DNS/IP/TLS/重定向/超限/超时和 canary 验收，再以官方 v7.2.141 镜像执行固定次数只读冒烟；请求串行且间隔至少 10 秒。
4. 核对日志、指标、容器文件、测试报告和 Git 工作区不含 Secret、endpoint、email 或原始响应，并验证 Gateway/Node 数据面不依赖 Driver。
5. 后续另建 poll-run change，将 Driver 注入显式调度服务并定义 PostgreSQL 事务、UTC 时间槽、租约、重试和持久证据；不得直接从本 change 启动周期采集。

应用回滚只回退 Control 二进制和 Driver 配置，无数据库 down。删除或禁用 Driver 不改变资产、Node 或账号；恢复相同安全配置后下一次显式调用重新解析 Secret/DNS 并自然恢复。
