## 1. 合同、依赖与配置骨架

- [x] 1.1 扩展 `api/openapi.yaml`，定义 bootstrap、login/MFA、session、reauth、password、activation、administrator 和 recovery-code 接口及统一错误 envelope；运行 OpenAPI 解析和 `make generate` 验证 operationId 与 schema 可生成
- [x] 1.2 在 OpenAPI 中声明 Cookie/CSRF 安全约定、匿名端点和敏感响应 `Cache-Control: no-store`，并用契约测试验证 `/api/healthz` 仍无认证要求且其他管理端点默认受保护
- [x] 1.3 添加经维护的 Argon2id、TOTP、UUID、指标和前端路由/测试依赖，固定兼容版本并运行 `go mod tidy`、`npm --prefix web install` 与许可证/漏洞检查确认依赖可复现
- [x] 1.4 增加认证配置结构和启动校验，覆盖环境类型、bootstrap Secret 文件、auth keyring、受信代理、Cookie 和 MFA 强制设置；用表驱动测试验证 staging/production 对缺失 keyring或不安全 Cookie fail closed，并验证只有 production 对关闭 MFA fail closed
- [x] 1.5 定义固定认证错误码、audit action/result、MFA method、session revoke reason 和 metric label 枚举，并用测试证明未知或无界值不能进入 API、审计或指标标签

## 2. Migration 与 sqlc 数据基础

- [x] 2.1 新增单个不可变 Goose forward Migration，建立 `control_bootstrap_state` 与初始单例行；执行 up/down/up 并验证 `completed` 没有普通 SQL/API 状态回退路径
- [x] 2.2 在同一 Migration 建立 `control_admin_users` 与 `control_admin_passwords`，加入固定 `local`/`super_admin`、登录名、状态和最后可用管理员所需约束/索引；用数据库测试验证非法角色、来源、登录名和重复登录名被拒绝
- [x] 2.3 建立 TOTP、恢复码和激活令牌表及单次消费/有效 token 索引；用并发 SQL 集成测试验证同一恢复码或激活令牌最多成功一次
- [x] 2.4 建立 MFA challenge、管理员 session 和认证失败窗口表及过期查询索引；用数据库测试验证 UTC 时间、撤销状态和摘要唯一约束
- [x] 2.5 建立 `audit_logs` 及固定分类、actor/target、原因、request ID 和 details 约束，配置应用角色无 update 权限；用权限测试验证产品连接不能修改审计记录
- [x] 2.6 为 bootstrap、管理员、凭证、MFA、token、session、限流和审计编写 `queries/*.sql`，运行 sqlc 生成并用 `git diff` 验证第二次生成无变化
- [x] 2.7 在空数据库和现有阶段 0 `environments` 数据库分别执行 Migration up/down/up 或兼容 up，核对环境单例不变并保存 schema/约束验证结果

## 3. 密码学与 Secret 处理原语

- [x] 3.1 实现密码 Unicode 长度/登录名差异校验和 Argon2id PHC hash/verify/参数升级判断；用固定向量、随机盐、畸形 PHC、14/128 边界和 dummy hash 测试验证
- [x] 3.2 实现版本化 auth keyring 文件解析、权限/长度检查和 HKDF domain separation；用测试验证跨环境/跨 domain 结果不同、未知版本失败且 production 没有硬编码回退 key
- [x] 3.3 实现绑定管理员 ID 与 key version AAD 的 AES-256-GCM TOTP 加解密；用篡改、错 key、错管理员、nonce 唯一性和旧 key 解密测试验证
- [x] 3.4 实现至少 256 位 bearer token、至少 128 位恢复码和分域 HMAC 摘要；用熵长度、编码、常量时间比较和原文不落库测试验证
- [x] 3.5 实现 RFC 6238 TOTP 生成/校验与 `last_used_step` 防重放；用前后一个时间步、边界、错误码和并发重放测试验证
- [x] 3.6 添加全链路 Secret redaction helper 和结构化审计 details 白名单；用 canary Secret 扫描响应、日志、审计、metrics 与 trace 捕获结果，验证所有敏感类型被排除

## 4. 审计、来源识别与失败限制

- [x] 4.1 实现 request ID 和客户端来源解析，只信任显式 CIDR allowlist 的代理 header；用伪造 forwarded header、IPv4/IPv6、直连和代理链测试验证原始 IP 不持久化
- [x] 4.2 实现登录名/来源密钥化指纹，验证同环境稳定、跨环境不可关联，且日志、指标、Trace 和匿名错误中没有登录名、显示名或 IP 原文
- [x] 4.3 实现结构化 audit writer 与事务内 `AuditIntent`，用故障注入验证管理员/MFA/session 状态和成功审计同时提交或同时回滚
- [x] 4.4 实现认证失败、CSRF、限流和重复 bootstrap 的独立失败审计；用数据库不可用测试验证认证 fail closed、无伪造成功审计且仅输出脱敏诊断
- [x] 4.5 实现 PostgreSQL 账号 5 次/15 分钟和来源 20 次/15 分钟、阻断 15 分钟的窗口逻辑；用并发、跨进程重建、窗口边界和不存在账号测试验证
- [x] 4.6 实现固定低基数认证指标和活动会话 gauge；用 descriptor/label 测试证明管理员、登录名、IP、session/request ID 和错误原文不能成为标签

## 5. bootstrap 与管理员生命周期

- [x] 5.1 实现 bootstrap status 与运行时 Secret 常量时间校验；用缺失、错误、正确 Secret 和已完成数据库状态测试验证 Secret 不进入持久化或输出
- [x] 5.2 实现锁定单例行的 bootstrap start，原子创建 pending 管理员、密码和未确认 TOTP；用两个并发 start 测试验证只建立一个流程
- [x] 5.3 实现 bootstrap resume/reset-pending，验证重启后可恢复、reset 只删除未完成身份且永远不能把 completed 改回 required
- [x] 5.4 实现 production bootstrap complete，原子确认 TOTP、生成 10 个恢复码、启用管理员、写审计并永久完成 bootstrap；用故障注入验证无半完成管理员和未持久化恢复码泄露
- [x] 5.5 实现后续管理员创建和 24 小时一次性激活令牌，只在创建/重新生成响应显示一次；用过期、撤销、并发消费和响应缓存头测试验证
- [x] 5.6 实现管理员 activation，原子设置密码、当前环境 MFA、恢复码、enabled 状态、session 与审计；用中途失败测试验证 pending 管理员仍不可登录
- [x] 5.7 实现管理员列表、禁用和激活令牌重新生成，要求 5 分钟重新认证与 10–500 字原因；用 self-disable、最后管理员、旧 token 和目标会话撤销测试验证
- [x] 5.8 实现 MFA reset 的受控流程，撤销目标 TOTP、恢复码、challenge 与 session 并回到安全的待激活状态；用故障注入和另一管理员操作测试验证不能绕过 production MFA

## 6. 登录、MFA 与恢复码服务

- [x] 6.1 实现统一密码登录和 dummy Argon2id 路径，对不存在、禁用、错误密码和需要 MFA 返回不可枚举结果；用 HTTP 状态、响应体与粗粒度时序回归测试验证
- [x] 6.2 实现 5 分钟 MFA challenge 的创建、Cookie、摘要存储、过期与单次消费；用 challenge 访问管理 API、错会话和重放测试验证始终拒绝越权
- [x] 6.3 实现 TOTP 登录，锁定因子并原子更新 `last_used_step`、消费 challenge、清除账号失败窗口和创建 session；用并发同时间步测试验证只成功一次
- [x] 6.4 实现恢复码登录，原子消费代码、消费 challenge、创建 session 并返回剩余数量；用并发消费和事务回滚测试验证
- [x] 6.5 实现重新生成 10 个恢复码，要求有效重新认证并原子撤销旧批次；用失败注入验证事务失败时旧码可用且新码不返回
- [x] 6.6 实现密码修改，验证当前密码/MFA或重新认证、更新 PHC、撤销其他会话并轮换当前会话；用旧密码、旧 session 和审计测试验证

## 7. Session、CSRF、重新认证与授权

- [x] 7.1 实现正式 session 创建、HMAC 摘要查询、30 分钟 idle/12 小时 absolute expiry 和每分钟限频活动更新时间；用数据库 UTC 边界与进程重启测试验证
- [x] 7.2 实现 production `__Host-` Cookie 和 dev loopback 例外；用 handler 测试验证 Secure、HttpOnly、SameSite=Strict、Path=/、无 Domain、无 remember-me
- [x] 7.3 实现每 session CSRF token、Origin/Referer 同源校验和 unsafe method 中间件；用缺失、跨 session、跨 origin、safe method 和 Cookie 重放测试验证
- [x] 7.4 实现 logout、过期和撤销的幂等处理，保证服务端先撤销再清 Cookie；用重复请求和数据库失败测试验证旧 session 不会复活
- [x] 7.5 实现重新认证并在轮换 session 上记录 5 分钟证明；用密码/TOTP 错误、过期、logout、密码/MFA变化和跨 session 使用测试验证
- [x] 7.6 实现统一 authorization middleware，除明确匿名路由外要求 enabled `super_admin`、有效 session 和当前环境 MFA assurance；用表驱动路由矩阵验证默认拒绝
- [x] 7.7 添加 Secret 明文、Gateway 写操作和服务身份交互登录的显式负向测试，验证 `super_admin` 不能突破系统边界且 `/api/healthz` 保持匿名

## 8. HTTP 适配与生成物

- [x] 8.1 将 bootstrap、activation、login/MFA 和 session service 接入生成的 strict server interface；用 OpenAPI HTTP 合约测试验证状态码、统一错误、request ID 和 `no-store`
- [x] 8.2 将 administrator、password、recovery-code 和 reauthenticate service 接入 strict server interface；用授权、CSRF、原因与重新认证矩阵测试验证
- [x] 8.3 将 auth/session/authorization middleware 接入 server 组合根，验证数据库或 keyring 故障时管理 API fail closed 而健康检查仍按既有语义响应
- [x] 8.4 运行 `make generate` 并检查 Go server 类型、sqlc 和 Orval 产物；再次运行生成命令并验证 worktree 无额外 diff，确认未手改生成文件

## 9. React 管理界面

- [x] 9.1 建立懒加载 auth shell 和 bootstrap/session 状态路由；用 Vitest 验证 required、in_progress、completed、401 和已登录状态进入正确页面且首包路由拆分
- [x] 9.2 实现 bootstrap start/resume/reset/complete 页面，Secret、密码、TOTP 和恢复码仅保存在组件内存；用存储/URL/DOM 生命周期测试验证离页后不能恢复 Secret
- [x] 9.3 实现登录、MFA 和恢复码页面及统一失败提示；用 Testing Library 验证 challenge 过期、限流、恢复码剩余数和账号不可枚举文案
- [x] 9.4 实现管理员激活页面，要求手工粘贴 token 且禁止 URL query/hash 载入；用测试验证 token 不进入 history、storage、日志或请求 URL
- [x] 9.5 实现 session/logout/password/reauth 和 CSRF 客户端处理；用测试验证 401 清状态、403 不自动重放非幂等请求、session 轮换后刷新 CSRF
- [x] 9.6 实现管理员列表、创建、禁用、激活 token 重新生成和 MFA reset 页面，所有高风险操作收集原因并要求重新认证；用 Vitest 覆盖最后管理员和 self-disable 错误
- [x] 9.7 实现一次性恢复码展示与确认离开流程，设置禁止缓存；用测试验证离开后页面和 API 均不能重新显示旧码
- [x] 9.8 运行 `npm --prefix web test`、`npm --prefix web run typecheck` 和生产 build，验证无敏感 console 输出且新增页面按路由分块

## 10. 集成、安全、恢复与容量验证

- [x] 10.1 添加 PostgreSQL 集成套件，覆盖 bootstrap/activation/recovery 并发、限流跨重启、session 到期/撤销、最后管理员保护和审计原子性；在 PostgreSQL 18 上运行通过
- [x] 10.2 添加完整 HTTP 安全负向套件，覆盖账号枚举、Cookie/CSRF、TOTP 重放、过期 token、禁用旧 session、服务身份、非法角色、Secret redaction 和数据库不可用 fail-closed
- [x] 10.3 添加 Playwright 端到端流程，覆盖 production bootstrap、TOTP 登录、恢复码登录、第二管理员创建/激活/禁用和进程重启后 bootstrap 仍关闭；保存不含 Secret 的报告
- [x] 10.4 对 Argon2id 参数和登录并发执行目标容器容量基准，记录 CPU/RSS/延迟和并发上限，验证不降低 64 MiB/3 iterations 的安全参数
- [x] 10.5 构建 Linux arm64 非 root 容器并验证 keyring/bootstrap Secret 文件权限、production 不安全配置拒绝启动和敏感响应不缓存
- [x] 10.6 停止或破坏 Control 认证/数据库后对既有 Gateway 与 Relay Node 发起受控冒烟请求，验证数据面成功且 Control 没有下发任何状态变化
- [x] 10.7 执行 `make test`、Go race tests、前端测试/typecheck/build、Migration up/down/up、容器测试和生成物洁净检查，修复全部失败并记录命令摘要

## 11. Runbook、证据与规格收尾

- [x] 11.1 编写 bootstrap Secret 生成/挂载/移除、auth keyring 备份/轮换/恢复和 HTTPS/Cookie 配置 Runbook，并用全新 dev 数据库演练步骤
- [x] 11.2 编写管理员激活、禁用、TOTP 丢失、恢复码耗尽、第二管理员恢复、全员锁定升级和 session 全量撤销 Runbook；验证流程不依赖重开 bootstrap
- [x] 11.3 编写 forward deploy、应用回滚保留表、允许 down 的严格条件和备份恢复步骤；在合成数据库演练并核对 `completed` 状态不被普通回滚打开
- [x] 11.4 建立规格场景到后端/数据库/前端/E2E 测试的追踪表，逐项确认 `administrator-access` 每个 Scenario 至少有一个验证证据
- [x] 11.5 运行 `openspec validate add-control-authentication-foundation --type change --strict --no-interactive` 并修复全部严格校验问题
- [x] 11.6 对照 v1.0 系统设计和 ADR 复核实现偏差；若存在行为或安全策略变化，先更新本 change 的 proposal/spec/design/tasks，再重新校验
- [x] 11.7 捕获不含 Secret 的 Migration、生成、测试、race、前端、容器、数据面隔离和安全审查证据，并确认没有真实管理员资料或运行时响应进入仓库
- [x] 11.8 运行格式化、`git diff --check`、生成物二次检查和 `git status --short`，确认只包含本 change 授权的可提交文件后再请求归档评审
