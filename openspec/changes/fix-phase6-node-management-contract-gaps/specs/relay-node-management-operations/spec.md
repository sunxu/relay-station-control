## ADDED Requirements

### Requirement: Node Probe authorization SHALL serialize with lifecycle commits

Health 与 Connection Test 共用的 secret-free authorizer MUST 在短 PostgreSQL transaction 内取得目标 Node 的 lifecycle read lock，验证 identity 存在、`lifecycle_status=active`、node type、driver contract、capability 与固定 management endpoint，复制 immutable Probe target 后 COMMIT；只有 commit 并释放 Node lock 后才可调用一次 bounded `Driver.Probe`。事务与 Node lock MUST NOT 跨 HTTP 保持，授权投影 MUST NOT 读取或返回 Reader Secret，Probe MUST NOT 调用 SecretResolver。

#### Scenario: Retire lifecycle-first
- **WHEN** Retire 先持有 Node lifecycle write lock，Health 或 Connection Test authorizer 随后请求同一 Node
- **THEN** authorizer 等待 Retire commit，醒来后返回 `409 asset_retired`，且零 Driver HTTP

#### Scenario: Replace lifecycle-first
- **WHEN** Replace 先持有旧 Node lifecycle write lock并提交 replacement
- **THEN** 等待中的 Probe authorizer 对旧 identity 返回 `409 asset_retired`，不 retarget 新 identity 且零 Driver HTTP

#### Scenario: Probe-first
- **WHEN** authorizer 先取得 lifecycle read lock、复制固定 target 并 commit
- **THEN** Retire/Replace 随后可以提交，已授权的一次 bounded Probe 可以完成；之后的新 Probe 返回 `409 asset_retired` 且零 HTTP

#### Scenario: Probe 保持 credential-free
- **WHEN** active Node 的 Reader Secret reference 为 NULL 或 runtime 无权读取该列
- **THEN** Health 与 Connection Test 仍可各执行一次 Probe，且 API、audit、metrics、log 与 receipt 均无 Secret reference/value
