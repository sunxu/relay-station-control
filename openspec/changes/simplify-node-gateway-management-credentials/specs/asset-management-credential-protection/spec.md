## Purpose

定义 Relay Node management credential 与 Gateway Directory credential 的窄范围 protected-at-rest 存储、K2、数据库隔离、兼容性门禁与 fail-closed 安全契约，同时保持其他 Secret 类型和数据面边界不变。

## ADDED Requirements

### Requirement: Management credential K2 SHALL have one exact external file contract
Control MUST 仅通过 `CONTROL_ASSET_CREDENTIAL_KEY_FILE` 路径读取 K2。该文件 MUST 位于 PostgreSQL 外，是非 symlink 的 regular file，由预期 Control runtime owner 持有，mode MUST 为批准的 `0400` 或 `0600`，内容 MUST 恰好是 32 raw bytes。Control MUST 每进程只加载一次且不得 hot reload。Raw K2 MUST NOT 进入 CLI argument、environment value、Compose YAML literal、PostgreSQL、API、UI、log、audit、metric、trace、receipt 或 diagnostic evidence。

Runtime structural validation MUST NOT 声称能从任意 32-byte 输入推断 entropy，且 Control runtime MUST NOT silently generate K2。Deployment/bootstrap tooling MAY 仅为 fresh environment create K2 exactly once，且 MUST 使用批准的 OS-backed CSPRNG 生成恰好 32 raw bytes并以atomic safe file creation安装。K2已存在时，bootstrap MUST NOT silently replace、truncate、rewrite或regenerate，重复bootstrap MUST保持existing K2 byte-for-byte unchanged。RNG、create或write failure MUST NOT留下partial usable K2 file。不得从password/passphrase、UUID、timestamp、hostname、environment identity、deterministic seed、predictable metadata、`math/rand`或repeated/static operator-chosen bytes派生。

#### Scenario: runtime rejects an unsafe K2 file
- **WHEN** K2 文件缺失、不可读、为 symlink、非 regular file、长度为 31 或 33 bytes、权限不是批准的 `0400`/`0600`，或 owner 不符合预期 runtime owner
- **THEN** process 仍启动但 credential cipher 在整个进程生命周期不可用，且不得 Seal/Open 或执行需要 credential 的 authenticated outbound

#### Scenario: supported provisioning creates the exact K2 representation
- **WHEN** dev/bootstrap 或人工 Runbook provision K2
- **THEN** 它使用批准的 OS-backed CSPRNG 直接生成恰好 32 raw bytes，以批准 owner/mode 写入外部文件，且 raw bytes 不出现在任何普通 surface

#### Scenario: normal startup never creates missing K2
- **WHEN** normal Control startup发现K2文件不存在
- **THEN** process以credential cipher unavailable的feature-limited状态启动，并且不创建任何K2文件

#### Scenario: bootstrap creates once and preserves an existing key
- **WHEN** bootstrap首次运行后再次运行
- **THEN** 首次运行原子创建一个有效K2；第二次运行不replace、truncate、rewrite或regenerate，文件内容byte-for-byte unchanged

#### Scenario: bootstrap failure leaves no partial usable key
- **WHEN** OS CSPRNG、exclusive create、write、permission或atomic publish步骤失败
- **THEN** bootstrap失败且目标路径不存在partial usable K2；任何既有K2保持不变

### Requirement: K2 identity commitment SHALL be exact and write-once
K2 identity commitment MUST 精确计算为：

```text
K2_identity_commitment =
SHA-256(
  UTF8("relay-station/control-asset-credential-key/v1")
  || 0x00
  || K2
)
```

Commitment 是 non-secret、每环境唯一、write-once 的 identity evidence；它 MUST NOT 被用作 key version、key registry、rotation metadata、ciphertext 或 raw K2。Commitment value MUST NOT出现在normal API、UI、ordinary query output、log、audit、metrics、trace/telemetry、receipt、Browser state、test evidence或acceptance artifact/evidence output。

内部PostgreSQL/store/acceptance observer MAY使用commitment判断present/absent、matches expected/mismatch与before/after unchanged，但proof/evidence MUST只输出`PASS/FAIL`、适用时的`present=true/false`、`match=true/false`或`unchanged=true/false`。Observer MUST NOT打印、记录、snapshot或导出commitment value本身。

#### Scenario: existing commitment matches
- **WHEN** 数据库已有 expected commitment 且进程 derived commitment 与之相等
- **THEN** credential cipher 为 AVAILABLE，并可在其他 eligibility 条件满足时 Seal/Open

#### Scenario: existing commitment mismatches
- **WHEN** 数据库已有 expected commitment 且 derived commitment 不相等
- **THEN** credential cipher 在整个进程生命周期为 UNAVAILABLE，不得 Seal/Open、不得 credential-bearing Set、不得执行需要 credential 的 authenticated outbound，且 commitment/protected state 不变

#### Scenario: fresh install initializes exactly once
- **WHEN** commitment absent 且 Node/Gateway sealed credential state 均为零
- **THEN** Control 锁定 canonical environment ownership，重新检查 commitment absent 与 sealed state 为零，写入 commitment 一次并提交；之后才可使用 cipher

#### Scenario: competing fresh-install keys have one winner
- **WHEN** 使用 K2-A 与 K2-B 的两个进程并发初始化同一 fresh database
- **THEN** 恰好一个进程提交 commitment，loser 在锁后观察 mismatch 并使 cipher 在其进程生命周期内不可用

#### Scenario: sealed state without commitment is invalid
- **WHEN** commitment absent 但任一 Node/Gateway sealed credential state 已存在
- **THEN** Control fail closed，不 adopt 当前 K2、不写 commitment、不 Seal/Open 且不修改 protected state

#### Scenario: commitment observer evidence is value-redacted
- **WHEN** store或acceptance proof检查commitment是否存在、是否匹配expected或操作前后是否不变
- **THEN** evidence仅包含PASS/FAIL或`present`、`match`、`unchanged`布尔结果，不包含commitment value、snapshot或可导出副本

### Requirement: Sealed credentials SHALL use one asset-bound opaque value
每个owning asset row MUST恰好保存一个opaque sealed credential value：Node为一个sealed management credential field，Gateway为一个sealed Directory credential field。MUST NOT创建separate credential table或separate nonce column。Representation MUST为`12-byte nonce || AES-GCM ciphertext+authentication tag`。非NULL sealed value的DB structural constraint MUST拒绝短到无法容纳12-byte nonce与non-empty authenticated GCM output的值；该check仅验证structure，AES-GCM Open仍是authenticity/integrity的authoritative proof。

每次Seal MUST从注入的cryptographic RNG请求一个新的12-byte nonce。AAD MUST精确为：

```text
Node AAD =
UTF8("relay-station/node-management-credential/v1")
|| 0x00
|| 16-byte UUID

Gateway AAD =
UTF8("relay-station/gateway-directory-credential/v1")
|| 0x00
|| 16-byte UUID
```

UUID MUST使用canonical 16-byte binary UUID representation，不得使用textual UUID characters。Plaintext MUST只存在于有界内存生命周期。

#### Scenario: ciphertext cannot move between assets or domains
- **WHEN** sealed credential 被复制到不同 asset UUID 或另一 credential domain 后尝试 Open
- **THEN** authentication 失败且不返回 plaintext、不修改 durable state

#### Scenario: exact AAD matrix
- **WHEN** 分别尝试Node A→Node A、Node A→Node B、Node→Gateway与Gateway A→Gateway B的Seal/Open
- **THEN** 仅Node A→Node A成功，其余均authentication fail且无durable mutation

#### Scenario: structurally short sealed state is rejected
- **WHEN** 写入无法容纳12-byte nonce与non-empty authenticated GCM output的非NULL value
- **THEN** DB structural constraint拒绝写入；对于长度合格的value仍由AES-GCM Open决定真实性

#### Scenario: RNG fails while sealing
- **WHEN** cryptographic RNG 不能提供下一份完整 12-byte nonce
- **THEN** Seal 失败，且 asset、credential、command、receipt 与 audit 均不得部分提交

### Requirement: K2 unavailability SHALL affect only operations that require cryptography
K2 unavailable 时，keep、clear、Retire（含 credential erase）、Replace with unconfigured replacement、credential-independent reads、永不需要 credential 的 health/readiness path，以及不需要 credential 的 terminal replay MUST 继续可用。这些路径 MUST NOT Open、Seal 或要求 K2。

Set new credential、Replace with new credential，以及 Open existing credential 以执行 authenticated outbound MUST 要求 available K2；missing、structurally invalid 或 wrong K2 时仅这些路径 fail closed。

#### Scenario: clear and retire remain available without K2
- **WHEN** cipher unavailable 且调用者执行 clear 或 Retire existing sealed state
- **THEN** 操作按既有 transaction/authorization contract 完成 credential erase，且不 Open、不 Seal、不读取 K2

#### Scenario: authenticated outbound is blocked without K2
- **WHEN** cipher unavailable 且 operation 需要 Open existing credential 执行 authenticated outbound
- **THEN** operation 使用既有批准的 unavailable/validation taxonomy fail closed，且不发送 outbound request

### Requirement: K2 startup degradation SHALL emit one sanitized warning
Missing、structurally invalid 或 wrong K2 时，Control process MUST 继续启动，credential cipher MUST unavailable，unrelated features MUST 保持可用，并且每进程 MUST 恰好发出一条 bounded sanitized availability warning。Warning MUST NOT 包含 raw K2、credential、sealed blob、filesystem secret content 或数据库 secret state。

#### Scenario: unavailable cipher warning is bounded
- **WHEN** Control 启动时 K2 missing、invalid 或 mismatch
- **THEN** 启动完成且只发出一条 sanitized availability warning，后续请求不得重复产生启动 warning

### Requirement: Secret configured state SHALL be exact and cryptography-independent
Node与Gateway读模型 MUST精确定义`secret_configured = (sealed_credential IS NOT NULL)`。计算MUST NOT decrypt，不得依赖K2 availability、Open success或blob authenticity。

#### Scenario: present sealed state remains configured when cipher is unavailable
- **WHEN** sealed blob存在且K2 missing或wrong
- **THEN** store/API仍返回`secret_configured=true`，真正credential-dependent operation随后fail closed

#### Scenario: present corrupt ciphertext remains configured
- **WHEN** structural length合格的sealed blob存在但AES-GCM authentication失败
- **THEN** store/API仍返回`secret_configured=true`，不得把Open failure伪装成unconfigured

### Requirement: Migration 00051 SHALL establish protected state without legacy import
Forward migration `00051` MUST 在 legacy `reader_secret_ref` 全部为 NULL 时，在各owning asset row建立恰好一个Node/Gateway sealed credential field、environment K2 identity commitment、`secret_configured = sealed_credential IS NOT NULL`投影、structural minimum-length constraint、protected column ACL与窄read path。不得创建credential table或nonce column。任一legacy reference非NULL时migration MUST fail；不得backfill、import、dual-read、dual-write或保留production fallback。

#### Scenario: legacy references block migration
- **WHEN** Migration 00051 检测到任一非 NULL legacy `reader_secret_ref`
- **THEN** migration 原子失败，不创建部分 Stage 0 protected state，operator 必须使用 fresh DB/re-register 路径

### Requirement: Compatibility class 4 and floor 4 SHALL be coherent with Migration 00051
Stage 0 MUST 扩展既有 signed manifest v1 gate，使 `SupportedClass=4`。Floor 4 MUST 要求 Migration 00051 已应用，Migration 00051 已应用 MUST 要求 floor >=4；class 0..3 signed manifests 仍保持结构有效，但 class < floor MUST 返回 `compatibility_floor_rejected` 且 Control artifact 不启动。Wrapper fail-closed sequencing MUST 保持不变。

#### Scenario: signed class-3 artifact is rejected before startup
- **WHEN** Stage 0 gate 检查一个有效 signed class-3 artifact，数据库为 floor 4 且已应用 Migration 00051
- **THEN** gate 返回 `compatibility_floor_rejected`，Control artifact 不启动，sealed state 与 K2 commitment 不发生变化

### Requirement: Scope SHALL remain limited to two management credentials
Protected persistence MUST 仅适用于 Relay Node management credential 与 Gateway Directory credential。Control MUST NOT 将该能力扩展为 generic Vault、KMS plugin framework、key registry、rotation、Provider/API/OAuth/access/refresh/password/DingTalk credential storage 或 raw upstream response persistence。

#### Scenario: unrelated secret cannot use the sealed asset path
- **WHEN** 实现或配置尝试把 Provider credential 或其他未批准 Secret 写入该 protected state
- **THEN** source/spec review 与对应负向测试必须拒绝该扩展
