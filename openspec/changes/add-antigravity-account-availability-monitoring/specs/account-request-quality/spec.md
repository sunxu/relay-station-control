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
