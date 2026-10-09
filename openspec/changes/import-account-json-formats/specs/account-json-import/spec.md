## Purpose

为 Accounts 新账号上传定义安全的双格式 JSON 导入：从凭据派生身份，保留账号级代理与初始启用状态，以既有独立单账号命令呈现部分成功，并防止敏感材料持久化或因异步竞态提交错误目标。

## ADDED Requirements

### Requirement: Import SHALL derive identity from bounded JSON

上传入口 SHALL 删除可编辑邮箱输入，接收一个不超过 1048576 bytes 的 JSON 文件，支持顶层 type=antigravity 的单账号对象，或包含非空 accounts 数组的 Sub2API 导出对象；批量最多 100 个账号。每个批量账号 MUST 为 platform=antigravity、type=oauth，具有 credentials 对象。邮箱 MUST 从单账号 email 或 credentials.email 提取并 trim/lowercase；MUST NOT 从文件名或 name 推测。空邮箱、不安全身份、文件内重复规范化身份、无效 JSON、不支持类型或超限 MUST 在任何提交前明确拒绝整个文件。身份长度与安全 basename MUST 遵循既有 Create admission。

#### Scenario: Filename differs from credential identity
- **WHEN** 文件名与 JSON 中的邮箱不同
- **THEN** 只读预览与 account_key 使用 JSON 邮箱，界面无邮箱输入框

#### Scenario: Batch contains duplicate identities
- **WHEN** 两个条目的邮箱仅大小写或首尾空白不同
- **THEN** 整个文件校验失败且 API mutation 为零

### Requirement: Import SHALL preserve account proxy and initial disabled state

单账号 SHALL 保留合法 proxy_url。批量 SHALL 用账号 proxy_key 精确关联 proxies 中唯一记录，将 protocol/host/port/可选 username/password 转成账号级 proxy_url；缺失或 null 的 proxy_key 表示未配置代理，非空引用不存在、重复或非法 MUST 拒绝，MUST NOT 默默直连。支持 http、https、socks5、socks5h，认证分量 MUST 安全编码。disabled MUST 只接受布尔值，单账号从顶层读取、批量从账号条目顶层读取；缺失时 MUST 生成 false，MUST NOT 从 auto_pause_on_expired 推导。预览 SHALL 显示邮箱、启用/禁用状态与代理是否配置，不显示代理认证材料。

#### Scenario: Batch references authenticated socks5h proxy
- **WHEN** 账号引用合法的带认证 socks5h 代理
- **THEN** 生成文件包含对应代理 URL 和 disabled=false，预览不反射用户名或密码

#### Scenario: Invalid disabled value
- **WHEN** disabled 是 null、字符串或数字
- **THEN** 校验失败且不提交，不进行 truthy 转换

### Requirement: Import SHALL convert credential fields without importing scheduler configuration

单账号 SHALL 保留原生允许的凭据字段，不静默剥离其它禁止字段。批量 SHALL 映射 email、access_token、refresh_token、project_id（缺失时用 antigravity_project_id）及可用 expires_at 到原生 expired；两个 project ID 同时存在且不同 MUST 拒绝。非空 refresh_token MUST 存在；可选 access_token/project ID MUST 为字符串，expires_at MUST 为可解析时间并保持同一 instant。批量外层并发、优先级、rate multiplier、auto_pause_on_expired，以及 credentials 中模型映射、自定义错误规则、版本信息和 extra MUST NOT 变为 Node 运行配置；界面 SHALL 说明仅导入凭据、代理与初始状态。MUST NOT 发起 OAuth refresh 或虚构有效期。

#### Scenario: Export includes scheduling settings
- **WHEN** Sub2API 文件带有 priority、model_mapping 和 auto_pause_on_expired
- **THEN** 生成凭据不包含这些配置，界面说明转换范围且不新增调度请求

### Requirement: Import SHALL use independent explicit account commands

确认提交 SHALL 按文件顺序串行调用既有 upload_new，每个账号具有独立新 command_id 与结果，保持真实 execution_state、错误及可用 operation projection。一个账号失败 SHALL 不撤销成功账号，非 401 失败后可继续其它账号；outcome_unknown MUST NOT 被视为成功或自动重试。MUST NOT 自动 replace、repair 或使用服务端批量事务。重复点击 MUST NOT 重复发起提交。目标 Node 与 CSRF SHALL 绑定本次确认的会话上下文；401 或组件退出 SHALL 停止未开始的提交并清理临时凭据。结果 SHALL 区分成功、失败、未知和未提交，保留命令身份供既有查询路径使用。

#### Scenario: Partial success and unknown outcome
- **WHEN** 三个独立调用分别返回 remote_applied、失败、outcome_unknown
- **THEN** 三项分别显示真实结果，每个调用一次且不自动补偿或 retry

### Requirement: Import SHALL protect secrets and asynchronous ownership

凭据、原始文件内容、代理密码和带认证 URL MUST 仅在本次导入所需的内存及受保护 Control 请求中使用，MUST NOT 进入普通 DOM 文本、URL/history、storage、日志、search、receipt 或 audit。取消、完成、401 与卸载 SHALL 释放敏感引用与文件输入；选文件 A 后换 B，迟到的 A 解析 MUST NOT 覆盖 B。Browser MUST 只访问 Control origin。用户交互 E2E SHALL 使用稳定 getByTestId；普通文案 SHALL 中英资源化，状态不只依赖颜色。

#### Scenario: Late file parsing
- **WHEN** A 的读取未完成时选择 B，B 先完成而 A 迟到
- **THEN** 只呈现并允许提交 B，A 的凭据不恢复

#### Scenario: Session expires mid-batch
- **WHEN** 当前账号返回 401
- **THEN** 进入既有 cache/session expiration boundary，未开始账号不提交，敏感数据不重新呈现
