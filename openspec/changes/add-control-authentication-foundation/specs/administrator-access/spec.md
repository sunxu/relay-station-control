## Purpose

为 Relay Station Control 提供可审计、可恢复且与服务身份和请求数据面隔离的管理员访问边界，使后续所有管理能力都只能由完成规定认证强度的实名 `super_admin` 使用。

## ADDED Requirements

### Requirement: 一次性 bootstrap 建立首个管理员
Control MUST 仅在当前环境数据库尚未完成 bootstrap 时，允许持有运行时 bootstrap Secret 的运维人员创建首个实名 `super_admin`。bootstrap MUST 在数据库事务中串行化；只有首个管理员完成密码设置和当前环境要求的 MFA 注册后，bootstrap 才能永久标记为完成。bootstrap Secret MUST 来自运行时 Secret 文件，不得写入数据库、响应、日志、指标、Trace 或审计详情。

#### Scenario: 生产环境完成首次 bootstrap
- **WHEN** 未初始化的生产 Control 收到有效 bootstrap Secret、合法实名管理员资料和密码，且操作者完成 TOTP 注册确认
- **THEN** 系统在一个受控流程中创建首个已启用 `super_admin`、恢复码和认证审计记录，将 bootstrap 永久标记为完成，并签发已完成 MFA 的管理员会话

#### Scenario: bootstrap 在 MFA 确认前中断
- **WHEN** 首个管理员资料已暂存但 TOTP 尚未确认时进程或浏览器中断
- **THEN** bootstrap 保持未完成，持有同一运行时 Secret 的运维人员可以恢复或安全重置该未完成流程，且系统不得留下可登录但未满足生产 MFA 要求的管理员

#### Scenario: bootstrap Secret 无效
- **WHEN** 未初始化 Control 收到缺失或无效的 bootstrap Secret
- **THEN** 系统拒绝创建任何管理员，返回不泄露 Secret 校验细节的响应，并记录不包含 Secret 和管理员密码的失败安全事件

#### Scenario: 并发 bootstrap
- **WHEN** 两个有效 bootstrap 请求并发尝试创建首个管理员
- **THEN** 数据库约束和事务锁只允许一个流程成功，另一个流程不得创建第二个初始管理员或覆盖已完成状态

#### Scenario: 已完成后重复 bootstrap
- **WHEN** bootstrap 已完成后再次访问初始化接口，即使请求携带原有效 Secret
- **THEN** 系统拒绝初始化且不得重新打开入口，并记录重复初始化安全事件

#### Scenario: 重启和应用回滚不重开 bootstrap
- **WHEN** 已完成 bootstrap 的 Control 重启或回滚到兼容的旧应用版本
- **THEN** 数据库中的完成状态继续阻止 bootstrap，删除或更换运行时 Secret 文件不得改变该状态

### Requirement: 固定的实名管理员生命周期
Control SHALL 仅提供固定 `super_admin` 交互角色。每个管理员 MUST 具有唯一登录名、非空实名显示名、启用状态和不可变管理员 ID；系统 MUST 不提供其他角色、权限组合、角色编辑器或服务身份登录能力。登录名 MUST 由 3 至 64 个 ASCII 小写字母、数字、点、下划线或连字符组成，并按小写唯一；显示名 MUST 去除首尾空白且长度为 1 至 100 个 Unicode 字符。

#### Scenario: 创建后续管理员
- **WHEN** 已重新认证的有效 `super_admin` 提供唯一登录名、实名显示名和原因创建管理员
- **THEN** 系统创建尚不可登录的待激活 `super_admin`，生成有效期 24 小时且只显示一次的一次性激活令牌，并记录操作者、目标和原因

#### Scenario: 激活后续管理员
- **WHEN** 待激活管理员在令牌有效期内设置合规密码并完成当前环境要求的 MFA 注册
- **THEN** 系统单次消费令牌、启用该管理员并签发独立会话，令牌之后不得再次使用或显示

#### Scenario: 激活令牌无效或过期
- **WHEN** 请求使用错误、已消费、已撤销或超过 24 小时的激活令牌
- **THEN** 系统返回统一失败结果，不启用管理员，不泄露令牌或目标账号状态，并记录失败事件

#### Scenario: 禁用管理员
- **WHEN** 已重新认证的另一名有效 `super_admin` 提供原因并禁用目标管理员
- **THEN** 系统原子地禁用目标、撤销其全部会话及未使用激活令牌，并记录审计

#### Scenario: 保护最后一个可用管理员
- **WHEN** 操作会禁用或删除最后一个已激活且启用的 `super_admin`，或管理员尝试禁用自身
- **THEN** 系统拒绝操作且不改变管理员或会话状态

#### Scenario: 禁止角色和服务身份登录
- **WHEN** 请求尝试创建非 `super_admin` 角色、修改角色，或使用 Gateway、Node、数据库等服务凭证登录
- **THEN** API 和数据库约束拒绝该请求，且交互会话不得建立

### Requirement: 密码认证与失败限制
Control MUST 使用成熟实现对密码执行 Argon2id 哈希并为每个密码使用独立随机盐；不得保存或记录明文密码。新密码 MUST 为 14 至 128 个 Unicode 字符、不得等于规范化登录名，并 MUST 接受密码管理器生成的空格和符号而不施加字符种类组合规则。认证失败响应 MUST 不区分账号不存在、账号禁用、密码错误或 MFA 状态。

#### Scenario: 合法密码进入第二认证阶段
- **WHEN** 已启用管理员提交正确登录名和密码且未触发失败限制
- **THEN** 系统在生产环境只创建短期、受限的 MFA 挑战，不得在 MFA 成功前建立可访问管理 API 的完整会话

#### Scenario: 非生产环境未强制 MFA
- **WHEN** dev 或 staging 环境明确配置为不强制 MFA，且已启用管理员提交正确凭证
- **THEN** 系统可以建立完整会话，但响应和审计 MUST 标明该会话未使用 MFA，且 production 环境不得接受相同配置

#### Scenario: 账号维度失败限制
- **WHEN** 同一规范化登录名在滚动 15 分钟内累计 5 次密码或 MFA 失败
- **THEN** 系统在之后 15 分钟内拒绝该账号的认证尝试，即使凭证正确，也不得通过响应确认账号是否存在

#### Scenario: 来源维度失败限制
- **WHEN** 同一服务端观察来源在滚动 15 分钟内累计 20 次认证失败
- **THEN** 系统在之后 15 分钟内拒绝该来源的新认证尝试，不影响已有会话，且日志、指标和 Trace 不得包含来源 IP 原文

#### Scenario: 成功登录后的失败计数
- **WHEN** 管理员完成密码和所需 MFA 验证
- **THEN** 系统清除该账号的连续失败状态，但不得清除来源维度用于防止凭证填充的滚动记录

#### Scenario: 修改密码
- **WHEN** 已登录管理员通过当前密码和当前 MFA 或有效重新认证证明设置合规新密码
- **THEN** 系统更新密码哈希、撤销除当前流程外的全部会话、轮换当前会话并记录审计；旧密码随后不得再用于登录

### Requirement: TOTP 与一次性恢复码
Production 环境 MUST 强制所有交互管理员使用 TOTP。TOTP MUST 使用兼容 RFC 6238 的 30 秒时间步长、6 位验证码和最多前后各一个时间步长的容差，并 MUST 防止同一时间步验证码被同一管理员重复接受。TOTP Secret MUST 加密保存；恢复码 MUST 使用密码学安全随机数生成、只显示一次、仅保存不可逆摘要且每个码最多消费一次。

#### Scenario: 注册 TOTP
- **WHEN** 管理员开始 MFA 注册
- **THEN** 系统生成独立 TOTP Secret，但在管理员提交有效验证码确认前不得把该因子标记为启用

#### Scenario: 首次显示恢复码
- **WHEN** TOTP 首次启用或已重新认证的管理员重新生成恢复码
- **THEN** 系统一次生成 10 个独立恢复码并仅在当前响应显示，之后任何接口均只能返回剩余数量而不能返回原码

#### Scenario: 使用 TOTP 登录
- **WHEN** 密码验证后的管理员提交有效且未被消费时间步的 TOTP 验证码
- **THEN** 系统完成 MFA、撤销挑战并建立或升级为完整会话

#### Scenario: 重放 TOTP 验证码
- **WHEN** 同一管理员再次提交已经成功使用过的 TOTP 时间步
- **THEN** 系统拒绝该验证码并计入 MFA 失败限制

#### Scenario: 使用恢复码
- **WHEN** 密码验证后的管理员提交尚未使用的有效恢复码
- **THEN** 系统完成 MFA、原子消费该恢复码并在会话中提示剩余恢复码数量

#### Scenario: 恢复码并发消费
- **WHEN** 两个请求并发提交同一个有效恢复码
- **THEN** 数据库只允许一个请求成功，另一个请求失败且不得建立完整会话

#### Scenario: 重新生成恢复码
- **WHEN** 已重新认证的管理员重新生成恢复码
- **THEN** 系统原子失效全部旧恢复码并只显示新的一组；事务失败时旧恢复码仍保持原状态且不得返回未持久化的新码

#### Scenario: 生产环境绕过 MFA
- **WHEN** production 配置缺少 MFA 加密密钥、尝试关闭 MFA 强制或管理员尚未完成 MFA
- **THEN** Control MUST fail closed：拒绝不安全启动或拒绝签发完整管理会话，并提供不包含 Secret 的运维诊断

### Requirement: 服务端会话、Cookie 与 CSRF 防护
Control SHALL 使用 PostgreSQL 保存服务端会话，仅向浏览器返回至少 256 位熵的随机不透明令牌；数据库只能保存令牌摘要。Production 会话 Cookie MUST 使用 `__Host-` 前缀并设置 `Secure`、`HttpOnly`、`SameSite=Strict`、`Path=/` 且不设置 `Domain`。会话 MUST 具有 30 分钟空闲超时和 12 小时绝对超时，不提供“记住我”。所有使用 Cookie 认证的非安全 HTTP 方法 MUST 同时通过绑定当前会话的 CSRF 校验。

#### Scenario: 完成登录后签发会话
- **WHEN** 管理员完成当前环境要求的全部认证步骤
- **THEN** 系统在提交会话记录后设置安全 Cookie 和独立 CSRF 证明，并在审计中记录新会话但不记录令牌值

#### Scenario: 缺失或错误 CSRF 证明
- **WHEN** 已登录浏览器向状态变更接口发送缺失、错误或属于其他会话的 CSRF 证明
- **THEN** 系统以 403 拒绝请求，不执行状态变更，并记录固定分类的安全事件

#### Scenario: 空闲超时
- **WHEN** 会话连续 30 分钟没有成功的已认证活动
- **THEN** 下一请求被拒绝，服务端将会话标记为过期并清除浏览器 Cookie

#### Scenario: 绝对超时
- **WHEN** 会话自创建起达到 12 小时，即使期间持续活跃
- **THEN** 下一请求被拒绝且管理员必须重新完成登录和 MFA

#### Scenario: 注销
- **WHEN** 管理员注销
- **THEN** 系统先撤销服务端会话再清除 Cookie；重复注销保持幂等且不得恢复会话

#### Scenario: 会话令牌轮换
- **WHEN** 登录、完成 MFA、修改密码、权限相关账号状态变化或重新认证成功
- **THEN** 系统轮换会话令牌并使旧令牌立即失效，防止会话固定和旧证明复用

#### Scenario: 数据库或认证状态不可用
- **WHEN** Control 无法读取会话、管理员启用状态或 MFA 状态
- **THEN** 管理 API fail closed 并拒绝访问；该故障不得影响 Gateway 或 Relay Node 的请求数据面

### Requirement: 高风险操作重新认证
Control MUST 要求修改管理员、生成激活令牌、重置 MFA、导出敏感数据以及后续规格标记的高风险操作使用不超过 5 分钟的重新认证证明，并要求 10 至 500 个字符的操作原因。重新认证 MUST 验证当前密码和当前 MFA，不得仅依赖已有会话。

#### Scenario: 成功重新认证
- **WHEN** 有效会话的管理员再次提交正确当前密码和当前 MFA
- **THEN** 系统在当前轮换后的会话上记录最多 5 分钟的重新认证时间，不返回可转移到其他会话的长期令牌

#### Scenario: 重新认证已过期
- **WHEN** 高风险操作使用超过 5 分钟的重新认证证明
- **THEN** 系统拒绝操作并要求重新验证，不得部分执行目标变更

#### Scenario: 缺少操作原因
- **WHEN** 高风险操作没有提供合规原因
- **THEN** 系统拒绝操作，且不得创建目标状态或伪造成功审计

#### Scenario: 会话变化使证明失效
- **WHEN** 管理员注销、密码或 MFA 改变、账号被禁用，或会话被撤销
- **THEN** 与旧会话绑定的重新认证证明立即失效

### Requirement: 管理 API 的固定授权边界
除显式匿名的健康检查、bootstrap 状态与流程、管理员激活和登录流程外，Control 管理 API MUST 要求有效、已启用、已完成当前环境 MFA 要求且角色为 `super_admin` 的交互会话。即使是 `super_admin`，系统 MUST 不提供 Secret 明文、Gateway 配置写操作或绕过后续 Node Agent 白名单的能力。

#### Scenario: 未认证访问管理 API
- **WHEN** 没有有效会话的请求访问受保护 API 或页面
- **THEN** API 返回 401，浏览器进入登录流程，响应不得包含受保护数据

#### Scenario: 已禁用管理员使用旧会话
- **WHEN** 已禁用管理员使用禁用前签发的会话访问管理 API
- **THEN** 系统拒绝访问并确保该管理员全部会话均已撤销

#### Scenario: 服务身份访问交互 API
- **WHEN** 服务 Token、Management Key、Gateway 管理凭证或数据库凭证被提交到交互登录或浏览器管理接口
- **THEN** 系统拒绝建立或升级管理员会话，且不得把服务身份映射为 `super_admin`

#### Scenario: 健康检查保持匿名
- **WHEN** 未认证请求访问 `/api/healthz`
- **THEN** 系统继续返回不包含管理员、会话或 Secret 信息的健康响应

#### Scenario: Control 认证服务故障与数据面隔离
- **WHEN** 认证模块、管理 UI 或 Control PostgreSQL 故障
- **THEN** Control 管理能力可以不可用，但不得向 Gateway 或 Relay Node 下发变化，也不得中断其已有请求

### Requirement: 不可变且脱敏的认证审计
Control MUST 为 bootstrap、登录、认证失败、限流、MFA 注册与恢复码使用、会话创建与撤销、密码变更、管理员创建/激活/禁用和重新认证写入审计。审计事件 MUST 使用数据库 UTC 时间并包含不可变事件 ID、固定动作与结果分类、可解析时的管理员 ID、目标 ID、原因和请求关联 ID；审计写入与对应安全状态变更 MUST 在同一数据库事务中提交或回滚。

#### Scenario: 状态变更与审计原子提交
- **WHEN** 管理员创建、禁用、密码变更、MFA 变更或 bootstrap 完成
- **THEN** 目标状态与成功审计同时提交；任一写入失败时二者均回滚

#### Scenario: 认证失败审计不泄露账号
- **WHEN** 登录名不存在、密码错误、MFA 错误或账号被禁用
- **THEN** 系统记录统一失败分类和密钥化的账号/来源指纹，不在日志、指标、Trace 或匿名响应中记录或确认登录名、显示名和 IP 原文

#### Scenario: Secret 与令牌脱敏
- **WHEN** 任意认证或管理员操作成功、失败或触发内部错误
- **THEN** 审计、日志、指标、Trace 和错误响应均不得包含密码、TOTP Secret、验证码、恢复码、Cookie、会话令牌、CSRF 证明、激活令牌或 bootstrap Secret

#### Scenario: 审计不可通过产品 API 修改
- **WHEN** 管理员尝试通过 Control API 更新或删除审计事件
- **THEN** 系统拒绝操作；本 change 不提供审计更新或删除接口，180 天保留清理只能由后续受控任务实现

#### Scenario: 进程重启后审计关联保持稳定
- **WHEN** Control 在已提交认证或管理员操作后重启
- **THEN** 审计事件仍可通过稳定管理员 ID、目标 ID 和请求关联 ID 定位，且不得依赖内存状态补写成功事件

### Requirement: 低基数认证可观测性
Control SHALL 导出认证尝试、结果分类、限流触发、MFA 方式和活动会话数量的低基数指标。指标标签 MUST 仅使用固定枚举，不得包含管理员 ID、登录名、显示名、IP、会话 ID、请求 ID 或其他无界值。

#### Scenario: 记录成功与失败指标
- **WHEN** 登录、MFA、恢复码或重新认证成功或失败
- **THEN** 对应固定操作、结果和环境类型计数更新，且指标无法用于还原具体管理员或来源

#### Scenario: 指标后端不可用
- **WHEN** 指标采集或暴露路径发生内部故障
- **THEN** 认证决策和审计真相仍由 PostgreSQL 状态决定，不得因指标写入失败放行或回滚已正确提交的认证事务
