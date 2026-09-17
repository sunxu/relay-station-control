## ADDED Requirements

### Requirement: Stage 0 acceptance SHALL prove immutable class-4 provenance and compatibility obligations
Acceptance harness MUST 将 exact source SHA、class-4 signed manifest v1、immutable Control artifact、Migration 00051、floor 4、running artifact 与K2 identity commitment的ownership/match/unchanged状态对齐，并覆盖Compatibility Addendum O01–O05。Commitment检查必须在内部完成，evidence只输出PASS/FAIL或`present`、`match`、`unchanged`布尔值；不得打印、记录、snapshot或导出commitment value。Class 0..3 manifest structure MUST保持有效，signed class-3 artifact在floor-4/Migration-51 DB上 MUST在Control启动前被拒绝。

#### Scenario: reject and restore preserve credential state
- **WHEN** harness 先执行 O04 class-3 reject，再以 exact class-4 current candidate 验证同一数据库
- **THEN** reject不修改Node/Gateway sealed state或K2 commitment，class-4 candidate可读取并使用原有合法state而不因gate admission静默重写；evidence仅报告`unchanged=true/false`或PASS/FAIL而不输出commitment value

### Requirement: Stage 0 acceptance SHALL remain representative and secret-safe
Runtime acceptance MUST 证明 Node authenticated read/account mutation、Gateway Directory read、credential set/clear/Replace/Retire、wrong/missing K2 fail-closed 与 representative UI credential flows；并 MUST 扫描 API/DOM/log/audit/metrics/trace/test evidence/acceptance artifact，确保 plaintext credential、raw K2、sealed credential blob、K2 identity commitment value、credential-bearing header 与 raw native body 不泄漏。Browser MUST 不承担 crypto、ACL、migration、race 或 compatgate matrix 的 owning proof。

#### Scenario: representative runtime acceptance completes
- **WHEN** exact class-4 candidate 在 production-like stack 上运行最小 Stage 0 acceptance set
- **THEN** changed cross-layer paths通过，Secret scan为零泄漏，Control仍在数据面之外且Gateway/Node artifact不变
