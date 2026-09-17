## ADDED Requirements

### Requirement: Phase 7 account operations SHALL use the protected Node credential resolver
所有 authenticated CLIProxyAPI account-operation consumers，包括 Inventory admission dependency 与 Disable/Enable/Remove/Upload New/Replace Existing native calls，MUST 使用同一 Node protected credential resolver。Credential source 改变 MUST NOT 改变 Node-first durable eligibility、same-account serialization、prepared/dispatched/remote_noop/outcome_unknown、override、receipt/audit 或 native error mapping。

#### Scenario: terminal replay does not depend on K2
- **WHEN** exact same command 已有 durable terminal result，随后 K2 缺失或错误
- **THEN** replay 返回已存 terminal result，K2 不参与 command classification，且不产生 native request

#### Scenario: new mutation cannot resolve credential
- **WHEN** genuinely new accepted operation 到达需要 authenticated native call 的边界但 protected credential 无法 Open
- **THEN** 操作按既有 bounded failure/receipt/audit contract fail closed，零 native mutation且不泄漏 Secret
