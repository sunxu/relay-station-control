## ADDED Requirements

### Requirement: Control Gateway和Node管理出站 SHALL 使用统一传输边界

Control当前及未来Gateway/Node管理客户端 SHALL 直接允许HTTP/HTTPS，不施加origin、DNS、IP、CIDR许可列表、特殊地址拒绝或DNS重绑定校验。HTTPS MUST 不验证证书链、有效期或主机名。此契约 MUST NOT 改变Control入站TLS/认证、Gateway到Node数据面或模型提供商连接，亦不授权新增管理写操作。客户端 MUST 保留固定接口、Secret隔离、禁止redirect、无代理策略、超时和响应限制及数据契约验证；TLS握手失败不得自动降级HTTP。

#### Scenario: 当前和后续管理客户端
- **WHEN** Gateway Directory、Node健康/账号/版本观察或后续批准的管理客户端执行请求
- **THEN** 均遵守相同传输策略，无额外网络许可或证书验证配置，操作能力仍受原契约限制

#### Scenario: 范围外链路
- **WHEN** 验证Control入站登录/TLS及Gateway到Node数据面配置
- **THEN** 本change不修改其认证、证书或网络策略

### Requirement: 旧Node传输配置 SHALL 明确退役

新Control MUST 忽略CONTROL_CLIPROXYAPI_MANAGEMENT_DNS、CONTROL_CLIPROXYAPI_MANAGEMENT_CIDRS、CONTROL_CLIPROXYAPI_PLAIN_HTTP_CIDRS和CONTROL_CLIPROXYAPI_CA_FILE，不加载该CA文件。Runbook SHALL 声明这些变量不再提供安全约束；旧版本回滚需要恢复旧配置和证书条件或停用受影响采集。

#### Scenario: 旧变量残留或缺失
- **WHEN** 旧变量为空、缺失、包含非法值或CA路径不存在，其余配置合法
- **THEN** 新Control正常启动，管理请求采用统一策略，不依赖这些变量
