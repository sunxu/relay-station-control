## MODIFIED Requirements

### Requirement: 最小事件与固定失败类别

Control SHALL 只持久化event_hash、可选request_id、node_id、provider、account_key、model、occurred_at、duration_ms、success、failure_class，以及可空的Antigravity安全认证子原因auth_failure_reason。失败类别 MUST 限于auth/quota/rate_limit/upstream/unknown；成功类别为NULL。

#### Scenario: 成功与五类失败
- **WHEN** 输入成功、认证错误、明确quota错误、429、5xx或未明确错误
- **THEN** 分别为成功/NULL、auth、quota、rate_limit、upstream、unknown

新增子原因 MUST限于token_invalid/account_blocked/forbidden/other，成功及旧event为NULL；不保存HTTP原文/status_message/Token。只在新Antigravity失败normalization中解析；failure_class仍保留原五类，明确新增认证代码可归auth，其他Provider不变。event_hash、resolved/unresolved、insert-ignore、7天retention及已有Quality/History/Incidents响应不变。历史auth MUST NOT回填为token或blocked。

#### Scenario: Antigravity auth refinement
- **WHEN** 新Antigravity失败含明确token/blocked代码或普通401/403
- **THEN** 在保持五类failure_class兼容的前提下保存安全子原因，普通403为forbidden而不是account_blocked

#### Scenario: Legacy and duplicate compatibility
- **WHEN** 旧writer省略子原因、非Antigravity事件、成功事件或已有hash重放
- **THEN** 旧值保持NULL且不猜测；success为NULL；不修改旧event或hash，不新增第二次计数


安全子原因 MUST只表示事件解析结果，不代表availability已确认状态。普通403保留failure_class=auth与forbidden子原因，原Quality/History/Incidents继续处理；availability仅在fresh runtime error/unavailable旁证下确认FORBIDDEN。事件存储仍按node_id+event_hash幂等，MUST NOT为修复availability去重而改原taxonomy/hash/计数；availability的独立请求计数另按同Node/account非空request_id去重，不假设event_hash或request_id全局唯一。无request_id多个hash不能单独确认账号故障。

#### Scenario: Multiple events for one request
- **WHEN** 多个不同event_hash携带同一个request_id
- **THEN** 原事件存储与质量计数保持原契约；availability只算一份请求证据，retry/replay不能凑数

#### Scenario: Missing request identifier
- **WHEN** 多个失败event均无request_id
- **THEN** 正常保留已有事件/分类，但不能靠hash数量确认availability；一个已分类失败加fresh runtime error/unavailable才可走交叉确认

#### Scenario: Ordinary forbidden remains visible to incidents
- **WHEN** 普通403的runtime仍active，即使有两个不同request_id
- **THEN** Request Quality仍按auth、Incidents仍按原规则聚合；availability为UNKNOWN/pending_confirmation而非FORBIDDEN
