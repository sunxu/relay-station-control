## 1. Driver 契约与配置

- [ ] 1.1 在 `internal/drivers` 定义固定 `NodeDriver`、`NodeTarget`、Probe/Inventory request 与 observation DTO，确保接口不能表达任意 method/path/header/body，并用编译期和单元测试覆盖固定枚举与无敏感 `String` 投影
- [ ] 1.2 实现 fail-closed registry，首期只登记 `cliproxyapi`，验证重复 node type、未知 node type、缺少 `management_health_read|management_account_inventory_read` 在 Secret/DNS/网络前被拒绝
- [ ] 1.3 新增 management DNS/CIDR、plain-HTTP CIDR、连接/总超时、body/record limit 和 Secret resolver 配置，设置保守默认/上限并测试缺失、超界、宽泛特殊地址和矛盾配置在启动构造阶段失败
- [ ] 1.4 增加配置与现有 asset `node_type`/Driver contract/capability 的兼容检查，不自动登记或修改资产，不为未知数据库字符串动态加载实现

## 2. Secret resolver 与凭证隔离

- [ ] 2.1 定义 `SecretResolver` 接口和 file-backed 引用映射实现，验证映射/Secret 为安全普通文件、大小有界且不可被 group/other 写入；未知引用/provider、symlink、目录、空值和超限均返回固定分类
- [ ] 2.2 将 Management Key 仅注入 auth-files 的 `X-Management-Key`，证明 `/healthz`、URL/query/cookie/User-Agent、context、错误与结果 DTO 均不携带凭证
- [ ] 2.3 使用唯一 Secret/引用/路径 canary 扫描成功与全部失败路径的日志、指标、trace-like 事件、错误、测试报告和数据库，验证无引用值、Key 或文件路径泄露
- [ ] 2.4 增加 resolver 取消、文件暂时不可用和恢复测试，证明失败不触发 DNS/网络，修复文件后下一次显式调用无需重启即可恢复

## 3. SSRF、DNS、TLS 与固定 HTTP 边界

- [ ] 3.1 实现严格 endpoint 重校验和 exact DNS name/suffix、management CIDR、plain-HTTP CIDR matcher，测试 IPv4/IPv6、IP literal、IDN/尾点/大小写、空解析和非规范 CIDR
- [ ] 3.2 实现每次连接重新解析、全部 IP 授权及锁定已验证 IP 的 DialContext，注入 DNS 重绑定、混合授权结果和拨号竞态，证明不会选择剩余地址绕过失败
- [ ] 3.3 无条件拒绝 loopback、link-local、unspecified、multicast、云元数据和未授权公网/私网目标，使用表驱动测试证明宽 CIDR 不能覆盖禁止项且诊断不泄露 host/IP
- [ ] 3.4 实现 HTTPS 原 hostname SNI/系统或显式 CA 校验与 HTTP 隔离网限制，测试错误 SNI、未知 CA、过期证书、HTTP 越界和恢复后的下一请求
- [ ] 3.5 构造专用无代理、无 cookie、无自动重试的 Transport/Client，固定 `GET /healthz` 与 `GET /v0/management/auth-files`，测试 uppercase/lowercase proxy 环境均不生效、base path 安全 join 且任意其他方法/路径不可表达
- [ ] 3.6 拒绝同源/跨源/协议变化等所有 3xx，使用捕获服务证明只发生一次请求，Management Key 不发送到 Location
- [ ] 3.7 实现连接默认 3 秒、总请求默认 15 秒和 context 取消，使用可控 Clock/服务测试慢 DNS、慢连接、慢 header/body、取消与 goroutine/连接回收，不发生隐藏重试

## 4. 健康与 auth-files 有界解析

- [ ] 4.1 实现 `/healthz` 小型有界 parser，仅接受 200 JSON object `status=ok`，测试非 200、非法/超限 JSON、未知状态和额外敏感字段不回显，结果不宣称账号或调度健康
- [ ] 4.2 实现 auth-files 5 MiB `max+1` 有界读取、顶层 object/`files` 数组和最大 1,000 条检查，测试边界值、超一字节、1,000/1,001 条、提前 EOF、慢 body 和部分结果不返回
- [ ] 4.3 实现 `transport_success`、`response_shape_valid`、`contract_valid` 与空 mode 的封闭映射，覆盖 HTTP 非 200、JSON 非法、缺少/非法 files、字段类型混淆和响应超限
- [ ] 4.4 实现 `X-CPA-VERSION`/`X-CPA-COMMIT` 有界 allowlist，测试缺失、非法、超长均为 `unknown`，且不使用资产期望版本冒充
- [ ] 4.5 定义账号字段白名单 DTO 和显式 JSON 投影，证明 `status_message`、path、project/auth/token/account/name、嵌套未知字段和未来新增字段不会进入返回值、格式化、观测或持久层

## 5. inventory mode、Provider 与状态分类

- [ ] 5.1 使用阶段 0 fixtures 实现 runtime 判定，覆盖全 file、全 memory 与混合 file/memory，明确 `source=file` 不被判为 disk fallback
- [ ] 5.2 实现 disk-fallback/空数组和无效来源形态判定，覆盖全部无 source 的固化磁盘形态、空 files、混合有无 source、未知 source 与未知磁盘形态
- [ ] 5.3 接受不可变 Provider 策略快照并严格校验 version/active/out-of-scope 规范化与互斥；测试缺失/未知版本和非法集合在解析账号内容前 fail closed
- [ ] 5.4 实现 provider/email trim/lower、缺 provider 全局阻断、active 缺 email 局部阻断、out-of-scope/unsupported 分类及每个 active Provider 零记录结果，使用 `identity-errors`/`provider-scope` fixtures 验证
- [ ] 5.5 实现 Node 内 `(provider,email)` 重复预分组和 `occurrence_count`，测试不任意合并状态、只令对应 Provider 不完整，且不在本层执行跨 Node 重复检测
- [ ] 5.6 实现固定基础状态优先级与有界计数/时间解析，覆盖 disabled、unavailable/retry、error、active、unknown、计数回退输入和非法时间；不计算跨轮增量或持久 account key
- [ ] 5.7 对 `cases.yaml` 全清单建立数据驱动契约测试，生成超 5 MiB 与 1,001 条无凭证 fixture，确认所有仓库样本仅使用 `example.invalid` 且不复制真实响应

## 6. 观测、安全与零副作用

- [ ] 6.1 实现低基数 Driver request/duration 指标，封闭 `node_type|operation|result`，测试拒绝 instance/endpoint/IP/provider/email/version/Secret/error 等派生标签
- [ ] 6.2 实现结构化日志 allowlist 和固定错误 reason，覆盖 DNS、TLS、timeout、HTTP、contract、Secret 故障，证明不调用任意 `error.Error()` 或记录请求/响应 header/body
- [ ] 6.3 增加端到端 canary 测试，将 endpoint、IP、Secret 引用/值、email、禁用字段、原始错误和 body 注入所有路径，扫描日志、指标、trace-like、错误、数据库/audit 与测试产物
- [ ] 6.4 在无后续调用者的生产 registry 中启动 Control，使用网络计数器和数据库快照证明不会自动创建 goroutine 请求、durable job、Outbox、账号/资产写入或 Gateway/Node 调用
- [ ] 6.5 模拟 Driver、management DNS/网络、CLIProxyAPI 和 Control 停止/恢复，验证只读观察暂停且 Gateway/Node 既有请求继续，恢复后仅下一次显式调用发起请求

## 7. 官方镜像、文档与综合验收

- [ ] 7.1 使用隔离 HTTP/TLS 测试服务完成 Host/SNI、CA、DNS/IP、redirect、proxy、body/record limit、timeout/cancel、连接复用和 Secret header 的进程级验收
- [ ] 7.2 对官方 CLIProxyAPI v7.2.141 原版镜像执行 `/healthz` 与 auth-files 直接冒烟，不修改 Node 代码/协议/账号；最多两个受控测试 Node、请求串行且相邻等待至少 10 秒，只保存脱敏分类/计数
- [ ] 7.3 验证阶段 0 的 6 个专用真实测试账号可被 Driver 安全分类且两个 Node 账号不重复；不在仓库、日志、报告或数据库保存 email、Management Key、endpoint、path、project ID 或原始响应
- [ ] 7.4 更新 Driver/Secret/management allowlist Runbook，说明无代理、精确 GET 反向代理、TLS/CA、DNS 漂移、Secret 轮换、超限/契约失败、10 秒冒烟限速、停机和应用回滚，并 dry run 全部无 Secret 命令
- [ ] 7.5 对照 proposal、design、`cliproxyapi-readonly-driver` spec、系统设计 v1.0、ADR-0001 和 phase-0 cases 核对实现，确认无 Migration/sqlc/OpenAPI/UI、poll run、账号持久化、告警、Redis、Sub2API/Prometheus Adapter、Gateway Connector 或 Node 修改
- [ ] 7.6 运行 `make generate` clean check、Go unit/integration/race、容器验收、敏感扫描、`openspec validate add-control-cliproxyapi-readonly-driver --strict` 和全部主规格 strict 校验；记录命令、版本、退出码及无 Secret 摘要
- [ ] 7.7 整理可审查提交序列和最终证据，使用 `git status --short`、提交范围与临时目录/容器检查证明无真实响应、凭证、测试 runtime、缓存或无关改动
