## MODIFIED Requirements

### Requirement: Upload SHALL preserve CLIProxyAPI credential schema ownership and Control runtime boundaries

Control MUST accept one top-level Antigravity JSON credential of at most 1048576 bytes, require type=antigravity, require email and match its normalized value to the intended account identity. CLIProxyAPI remains credential schema truth; new provider credential fields may pass through.

Control MUST canonicalize recognized v7.3.2 metadata aliases and reject, without silent stripping, canonical runtime/routing/management keys: weight,priority,headers,request_retry,excluded_models,note,websockets,prefix,models,disable_cooling,fingerprint_profile,base_url,model_aliases,request_scoped_errors,tool_prefix_disabled. Legacy aliases request-retry,excluded-models,disable-cooling,fingerprint-profile,base-url,model-aliases,request-scoped-errors,tool-prefix-disabled receive the same rejection. api_key/api-key remains provider credential material and is not denied solely by this list. Multipart bounds remain 1073152 aggregate bytes, 8192 request bytes, 1048576 credential bytes and 16384 framing bytes.

账号级 proxy_url（及既有 alias proxy-url）SHALL 作为显式导入配置窄范围允许，值 MUST 是非空绝对代理 URL，协议为 http/https/socks5/socks5h，具有 host、有效端口（若提供），不得含 control characters、非根路径、query 或 fragment；不得同时提供两个 alias。代理认证材料 MUST 按 Secret 处理。Control SHALL 不连接或探测此代理，原始凭据字节继续参与既有 keyed fingerprint，不增加代理调度所有权。

disabled SHALL 仅在 upload_new 接受布尔值；缺失可保持既有 API 字节不变，新 UI 按导入契约生成 false。replace_existing MUST 继续拒绝 disabled，MUST NOT 通过本变更修改已有账号状态。后端 MUST 在既有接受事务前验证类型和代理，无效输入返回既有 upload_invalid 且无新 operation/reservation/receipt/native mutation。服务端 MUST NOT 为了默认值改写原始凭据字节或 fingerprint。

账号操作指纹密钥读取 SHALL 接受权限恰好为 0400 或 0600 的可读、非符号链接普通文件，内容 MUST 为 32 字节；组/其他用户可访问权限、其它权限、缺失/非普通文件、不可读或错误长度 MUST 继续返回接受事务前的 service_unavailable。读取 MUST NOT 改写权限、内容或自动生成/轮换密钥；权限从 0600 改为 0400 不得改变相同密钥/凭据的 HMAC 指纹与重放语义。生产只读 Secret 挂载保持不变。

#### Scenario: Production upload uses a read-only intent key
- **WHEN** production composition 绑定可读的 32 字节普通密钥文件，权限为 0400
- **THEN** 上传沿用既有 admission/执行边界，不因密钥不可写而返回 503，密钥内容和权限不变

#### Scenario: Unsafe intent key remains rejected
- **WHEN** 密钥允许组/其他用户访问、是符号链接、非普通文件、缺失、不可读或长度不是 32 字节
- **THEN** 返回接受事务前的 service_unavailable，无新增 reservation/operation/receipt/native mutation，不泄漏密钥

#### Scenario: Upload includes a provider extension and runtime controls
- **WHEN** credential 包含新 provider 字段以及 priority
- **THEN** Control 返回 upload_invalid，不剥离或发起原生 mutation

#### Scenario: Upload New preserves proxy and disabled
- **WHEN** upload_new 携带合法认证代理与布尔 disabled
- **THEN** 通过窄范围校验并沿用原始字节与既有单账号执行边界，Secret 不进入 projection/audit

#### Scenario: Replace cannot change disabled state
- **WHEN** replace_existing 携带 disabled，即使值为 false
- **THEN** 返回 upload_invalid，接受事务和原生调用均为零

#### Scenario: Invalid proxy fails before acceptance
- **WHEN** proxy 值为对象、含 query/control characters、缺 host 或协议不受支持
- **THEN** 返回 upload_invalid 且无持久化或远程动作，不静默直连
