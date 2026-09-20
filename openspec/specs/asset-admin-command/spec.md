# asset-admin-command Specification

## Purpose
定义 Gateway 与 Relay Node 管理 mutation 共用的 durable command receipt、actor-first lookup、canonical intent、Secret 指纹、幂等 replay、事务原子性和不可变审计基础。

## Requirements

### Requirement: Durable admin command identity and replay

共享durable command MUST 先完成authentication、active session、super_admin、CSRF，再进入transaction。command_id全局唯一；取得由完整UUID派生的advisory lock后lookup receipt，先校验actor_admin_id，再解析receipt-recorded encoding/key version并lazy构造canonical intent。receipt actor lookup MUST 早于endpoint/Secret validation、K1访问以及lifecycle/revision/current-state evaluation。每个accepted/completed command exactly one immutable receipt；不完整transaction无receipt。state-changing success同事务exactly one mutation audit，completed no-op只有receipt无transition audit。receipt保存完整原success body/status，不依赖后续asset状态。

#### Scenario: 认证先于receipt可见性
- **WHEN** 未认证、session失效、非super_admin或CSRF失败
- **THEN** 按既有安全契约拒绝，不能读取receipt或探测其存在

#### Scenario: 相同actor与intent重放
- **WHEN** 同actor提交同command_id与同canonical intent且已有receipt
- **THEN** 返回原persisted status/body；无第二mutation、lineage、receipt或success audit

#### Scenario: actor不匹配
- **WHEN** 另一个super_admin复用command_id
- **THEN** 409 command_conflict，不暴露原result

#### Scenario: actor conflict优先于后续错误
- **WHEN** receipt属于actor A，而actor B以同command_id提交malformed endpoint、invalid Secret、unavailable K1、stale revision或retired target
- **THEN** 全部立即返回409 command_conflict，且不能执行或暴露任何后续validation结果

#### Scenario: intent不匹配
- **WHEN** 同command_id对应不同canonical intent
- **THEN** 409 command_conflict，无mutation或success audit

#### Scenario: 并发同command
- **WHEN** 两个同actor同intent请求并发
- **THEN** advisory lock串行化，只有一个completed receipt及最多一次state transition，另一个replay

#### Scenario: commit前crash
- **WHEN** domain transaction rollback或crash
- **THEN** domain/audit/receipt全rollback，重试可重新执行

#### Scenario: 后续状态改变
- **WHEN** command A commit后B修改asset，A再次replay
- **THEN** 返回A的持久化body/status，不返回B后的current state

#### Scenario: state-idempotent no-op
- **WHEN** 后续Node已满足enable/disable目标的新command被接受
- **THEN** exactly one receipt，零domain mutation、零asset revision变化、零successful transition audit

### Requirement: Canonical intent cryptography and durable key version

canonical_intent_hash MUST 等于 SHA-256(canonical_intent_v1_bytes)。v1使用design的fixed-order UTF-8 JSON arrays，区分absent/clear/set，revision string与endpoint normalization一致；Secret只贡献HMAC-SHA-256 fingerprint与key version，raw reference不得进入canonical bytes或receipt。receipt.secret_fingerprint_key_version set时为1，无secret set intent时NULL；replay MUST 使用历史receipt version。

K1是由`CONTROL_ASSET_INTENT_KEY_FILE`提供的稳定独立32-byte deployment Secret。文件 MUST 是regular file、不得是symlink、权限安全且内容exactly 32 raw bytes；missing、path/read failure、unsafe permissions、symlink或wrong length均表示K1 unavailable。K1 MUST 保持稳定、备份并跨restart/upgrade恢复，不得自动生成、记录或写入receipt。Stage 1不认证K1 identity，不使用signed digest、identity anchor、额外trust metadata或deployment identity file；未来rotation属于独立change。

K1只在canonical intent包含SecretSet时使用：SecretSet记录key version 1并计算HMAC fingerprint；SecretAbsent、SecretClear和其它non-SecretSet intent的receipt key version为NULL且 MUST NOT 读取K1。receipt schema保持既有encoding/hash/key-version字段，不增加key digest/id/table。算法、field order与每action fields见design Canonical intent v1 bytes，属于共享encoding约束，Node后续扩展独立command_kind不得重新定义现有编码。

#### Scenario: 跨restart与upgrade重放
- **WHEN** 重启或支持的升级后请求同一Secret intent
- **THEN** 使用持久version=1与K1计算相同fingerprint/hash，不泄露Secret

#### Scenario: key缺失
- **WHEN** 新command或actor-matched历史receipt需要SecretSet K1 v1，而文件missing/unreadable、不安全、是symlink或长度不是32 bytes
- **THEN** 503 service_unavailable，零mutation、receipt和success audit，且不得自动生成或fallback key

#### Scenario: 可用K1下的不同Secret intent
- **WHEN** actor匹配、K1通过基础文件验证且请求Secret intent与receipt不同
- **THEN** 生成不同canonical hash并返回409 command_conflict

#### Scenario: 结构有效但被错误替换的K1
- **WHEN** 历史K1被另一把权限安全且长度为32 bytes的key替换，并用它重新计算同一Secret请求
- **THEN** validly computed hash mismatch返回409 command_conflict；系统不尝试判断mismatch原因，恢复正确历史K1后同一请求可再次正常replay

#### Scenario: 无Secret receipt不访问K1
- **WHEN** receipt的secret_fingerprint_key_version为NULL
- **THEN** actor lookup及对应intent comparison不得以K1 unavailable为由失败

#### Scenario: 未知intent encoding fail closed
- **WHEN** actor匹配但历史receipt记录Control不支持的intent_encoding_version
- **THEN** 返回503 service_unavailable，不执行domain validation且不把未知encoding误报为command_conflict

#### Scenario: non-SecretSet command不依赖K1
- **WHEN** command canonical intent不包含SecretSet且K1 unavailable
- **THEN** 不读取K1，command继续按actor、encoding及domain contract执行

### Requirement: Existing asset optimistic concurrency

已有asset Edit/Retire/Replace MUST 提供body expected_revision；no-receipt路径不匹配409 stale_revision且无mutation/audit。Register revision=1；Edit/Retire/Replace old +1，Replace new=1。Health/Connection Test及后续Monitoring action不改asset revision。合法receipt replay先于revision判定。

#### Scenario: stale revision
- **WHEN** 新command的expected_revision与锁内current值不匹配
- **THEN** 409 stale_revision，零mutation、receipt、success audit

### Requirement: Shared compatibility barrier SHALL verify selected artifact

本 change MUST 提供application rollback unit外的relay-control-compat-gate和mandatory supported deployment wrapper。class权威来源是独立release authority的Ed25519-signed manifest v1，payload=[1,control_artifact_digest,compatibility_class]。gate验证signature与selected binary SHA-256/OCI manifest digest；拒绝自由环境变量、自声明label和不匹配digest。Pinned 5e2caeb031a47510744cada56978b963da39b4e9 的验收artifact登记class0，Phase6-aware正式artifact class1；实际digest在构建验收时记录，不猜测。

DB public.control_runtime_compatibility单例schema_version=1、phase6_evidence_floor=1由forward migration写入并只增不减；migration后即floor1。强制顺序是停止old process/restart -> deploy gate/trust root/wrapper ->验证mandatory path -> forward migration/floor -> selected artifact验证+DB gate -> start Control ->开放mutation。signature/digest/schema/class错误exit78、DB故障exit75，process不启动。supported Compose/systemd wrapper不得绕过；不声称阻止host root手工执行任意docker run。rollback仍用同wrapper，不回滚gate/trust root，class0<floor1时old process不启动。receipt/lineage历史不清理以绕过floor。

#### Scenario: Pinned old artifact rollback through production wrapper
- **WHEN** 正式wrapper选择pinned class0 artifact且数据库已有Phase6 evidence/floor1
- **THEN** gate拒绝，HTTP/Directory/Inventory process均未启动，DB truth保持不变；必须记录wrapper验收而非只调用gate

#### Scenario: Tampered compatibility class
- **WHEN** operator把old artifact的class改为1、替换binary或选择不同image digest
- **THEN** signature/digest验证拒绝；不能以environment override提升class

#### Scenario: Missing metadata or unavailable database
- **WHEN** manifest缺失、签名无效、marker与migration不一致或DB不可读
- **THEN** fail closed，不启动Control；不从operator变量猜测class/floor

### Requirement: Shared asset admin command writers SHALL use the global command registry

All existing Gateway/Relay Node lifecycle and shared asset administrator command writers that accept `command_id` MUST reserve and lookup that ID through `admin_command_registry` under the existing UUID-derived advisory serialization before existing actor/intent/domain processing. Existing immutable `asset_admin_command_receipts` remain the exact completed replay evidence and existing K1/canonical-intent bytes remain unchanged.

#### Scenario: Existing receipt replay after registry migration
- **WHEN** the same actor replays a historical asset command with the same intent after the registry migration
- **THEN** actor/domain/intent resolve through the backfilled registry and Control returns the original persisted receipt body/status without a second mutation, audit or receipt

#### Scenario: Account-domain reservation collides with new asset command
- **WHEN** a Phase 7 account operation has already reserved a command ID and an asset writer receives that same UUID
- **THEN** the asset writer returns `command_conflict` before lifecycle/revision/Secret validation and commits no asset change

### Requirement: Phase 7 account commands SHALL preserve shared global identity without expanding asset K1

Phase 7 account commands MUST use the same global command reservation, advisory serialization and actor-first namespace introduced by `add-global-admin-command-registry`. Existing `CONTROL_ASSET_INTENT_KEY_FILE` K1 bytes, domains, versions and asset command encodings MUST remain unchanged. Upload Secret equality MUST use the separately reviewed Phase 7 keyed-fingerprint contract.

Change B MUST use a separate immutable `account_admin_command_receipts` relation and MUST NOT extend the asset-only receipt domain. A stable terminal `remote_applied`, `remote_noop` or `failed` account mutation MUST atomically create exact original POST status/body replay evidence. `prepared`, `dispatched` and `outcome_unknown` MUST NOT create a terminal receipt. The separate relation MUST also receipt independent `account.lifecycle_override` and `account.same_account_override` commands while linking each to its target operation when present; an override receipt MUST NOT reuse or rewrite the target mutation receipt. Inventory observations and target-operation override fields MUST NOT rewrite immutable replay evidence.

#### Scenario: Upload command tries to use asset K1 fallback
- **WHEN** the Phase 7 upload fingerprint key is unavailable but asset K1 is valid
- **THEN** upload fails closed and asset K1 is neither read as fallback nor semantically expanded

#### Scenario: Outcome unknown same-command retry
- **WHEN** the same actor and intent retry while execution is `outcome_unknown`
- **THEN** Control returns the current operation projection with 202, creates no receipt and sends zero native mutation

#### Scenario: Inventory changes after terminal POST
- **WHEN** a terminal POST receipt exists and later Inventory observes a different business state
- **THEN** exact POST replay returns the original persisted status/body while the separate Inventory surface shows its current observation

#### Scenario: Ambiguous native server error
- **WHEN** a native request may have reached mutation and returns an ambiguous 500 or loses its response
- **THEN** execution becomes `outcome_unknown` and Control creates no terminal receipt or inferred execution result

### Requirement: Stage 0 credential command semantics SHALL preserve actor-first precedence

Stage 0 genuinely new Node/Gateway asset commands MUST use reviewed intent encoding v2 while preserving K1 version 1 and the existing SHA-256 canonical intent mechanism. Receipt actor lookup and existing-command classification MUST precede credential validation and K2 access. Raw credential, recoverable sealed ciphertext and K2 MUST NOT enter canonical durable bytes, registry, receipt or audit.

#### Scenario: same-actor semantic-invalid credential preserves command precedence
- **WHEN** same actor以existing Node或Gateway command_id提交syntactically parseable但semantic-invalid credential
- **THEN** 系统先按existing durable command intent分类replay或command_conflict，不提前返回credential validation error且不访问K2

### Requirement: Credential values SHALL have one exact validation contract

Node `management_credential` 与 Gateway `directory_credential` string MUST 非空、UTF-8 encoded length 不超过4096 bytes，且不得包含 NUL、CR 或 LF。系统 MUST 保留 exact bytes，不得 trim 或 normalization。

#### Scenario: boundary values are classified by exact UTF-8 bytes
- **WHEN** credential分别为0、1、4096、4097 UTF-8 bytes，或multibyte input跨越4096-byte boundary
- **THEN** 仅1..4096 bytes且不含禁止字符的值通过验证，leading/trailing spaces按exact bytes保留

### Requirement: Asset and credential mutation SHALL commit atomically

Credential set/clear MUST 与既有 asset mutation、command registry、receipt 与 audit 在同一 transaction 提交或回滚；失败不得留下 asset/credential/command 的部分状态。

#### Scenario: credential sealing or durable write fails
- **WHEN** 新 command 的 Seal 或 protected state 写入失败
- **THEN** asset revision、credential state、command terminal result、receipt 与 audit 均不得部分提交

### Requirement: K2-unavailable command behavior SHALL be operation-specific

K2 unavailable时，keep、clear、Retire（包括 credential erase）、Replace with unconfigured replacement及不需credential的terminal replay MUST继续按既有command contract工作且 MUST NOT Open/Seal/require K2。Set new credential 与 Replace with new credential MUST fail closed。

#### Scenario: unconfigured replacement does not require K2
- **WHEN** cipher unavailable 且 Replace 明确创建 unconfigured replacement
- **THEN** Replace 按既有 lifecycle/atomicity contract 执行，不 Open、不 Seal 且不要求 K2

### Requirement: Stage 0 SHALL NOT expose crypto-specific public errors

Stage 0 MUST NOT 新增 `k2_*`、`aes_*`、`decrypt_*` 或 `cipher_*` 等 crypto-specific public error code；K2 unavailable、Seal/Open 或 decrypt failure MUST 映射到既有 validation/unavailable family，且不得泄露 cryptographic detail。

#### Scenario: credential cipher failure is externally bounded
- **WHEN** API operation 因 K2 unavailable 或 credential Open failure 无法继续
- **THEN** response 使用既有批准的 validation/unavailable family，且 body 不包含 K2、ciphertext、algorithm 或 filesystem detail
