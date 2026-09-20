## MODIFIED Requirements

### Requirement: Endpoint 与 Secret 引用安全隔离
资产 endpoint MUST 是规范化的绝对 `http` 或 `https` URL，包含 host，且不得包含 userinfo、query 或 fragment。Control MAY 仅为 Relay Node management credential 与 Gateway Directory credential 保存 approved protected-at-rest representation，但 MUST NOT 保存 plaintext credential；其他 Secret 继续仅允许既有引用模式。API、UI、ordinary query、日志、指标、Trace、审计与业务 surface MUST NOT 返回或记录 plaintext、sealed blob、K2、K2 identity commitment、legacy reference 或 crypto metadata，且 API 只能用布尔值表示 Secret 是否已配置。`secret_configured` MUST精确定义为`sealed_credential IS NOT NULL`，不得decrypt，也不得依赖K2 availability、Open success或blob authenticity。

#### Scenario: 读取已配置 Secret 的资产
- **WHEN** 管理员读取包含 approved protected credential state 的 Gateway 或 Node
- **THEN** 响应仅包含 `secret_configured: true`，不包含 plaintext、sealed blob、K2、K2 commitment、legacy reference、crypto metadata或可逆派生值

#### Scenario: configured projection is independent of credential usability
- **WHEN** sealed credential存在但K2 missing、K2 wrong或ciphertext corrupt
- **THEN** ordinary store/API读取仍返回`secret_configured: true`，不得尝试Open；credential-dependent operation在其owning layer独立fail closed

#### Scenario: 非法 endpoint
- **WHEN** 受控部署流程提交相对 URL、不受支持的 scheme、缺少 host 或包含 userinfo、query、fragment 的 endpoint
- **THEN** 数据库写入失败，且诊断不得回显潜在凭证内容

#### Scenario: Endpoint 无网络副作用
- **WHEN** endpoint 被登记或通过资产接口读取
- **THEN** Control 不解析远端能力、不探测连通性，也不向该 endpoint 发起请求

## ADDED Requirements

### Requirement: Asset Registry frontend SHALL remain a minimum functional adaptation
Stage 0 frontend MUST 只适配 Node `management_credential`、Gateway `directory_credential` 的输入、clear/keep 语义、configured/unavailable 状态和既有错误呈现；不得引入 design-system、i18n、页面重构或新的资产工作流。

#### Scenario: representative credential workflow uses existing asset flow
- **WHEN** 管理员通过既有 Asset Registry 注册或编辑一个 credential
- **THEN** 交互继续使用既有 authenticated/CSRF/confirmation/command flow，并只新增冻结 Stage 0 所需字段和状态
