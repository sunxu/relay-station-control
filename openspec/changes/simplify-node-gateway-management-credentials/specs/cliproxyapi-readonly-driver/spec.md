## MODIFIED Requirements

### Requirement: Management Secret 仅在运行时受保护地解析

CLIProxyAPI Driver MUST通过Node protected credential resolver读取owning asset的sealed `management_credential`并在有界内存生命周期中Open为ephemeral Management Key；production MUST NOT读取legacy opaque reference、Secret文件或使用`FileSecretResolver` fallback。Management Key MUST只进入单次auth-files请求的`X-Management-Key` header，不得进入数据库plaintext、URL、query、cookie、健康请求、API、UI、日志、指标、Trace、审计、错误或测试证据。现有transport、timeouts、no proxy、no redirect、response limits、parsing与Provider policy语义保持不变。

#### Scenario: 解析有效 Secret 引用
- **WHEN** active eligible Node具有有效sealed management credential且K2/commitment正确
- **THEN** Driver为单次auth-files GET设置ephemeral Management Key header，并在请求完成后不保留可观测副本

#### Scenario: 引用未知或 Secret 文件不安全
- **WHEN** sealed credential未配置、损坏、protected read被拒或K2不可用
- **THEN** Driver在任何DNS/网络调用前返回既有批准的validation/unavailable错误分类，且响应和日志不包含plaintext、sealed blob、K2、filesystem content或crypto-specific public code

#### Scenario: 健康探测
- **WHEN** Driver调用`/healthz`
- **THEN** 请求不携带Management Key、cookie或其他管理凭证，且不Open/Seal、不要求K2
