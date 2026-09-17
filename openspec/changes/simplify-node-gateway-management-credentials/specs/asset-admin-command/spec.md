## MODIFIED Requirements

### Requirement: Durable admin command identity and replay

共享durable command MUST 先完成authentication、active session、super_admin、CSRF，再进入transaction。command_id全局唯一；取得由完整UUID派生的advisory lock后lookup receipt，先校验actor_admin_id，再解析receipt-recorded encoding/key version并lazy构造canonical intent。receipt actor lookup MUST 早于endpoint/credential validation、K1/K2访问以及lifecycle/revision/current-state evaluation。每个accepted/completed command exactly one immutable receipt；不完整transaction无receipt。state-changing success同事务exactly one mutation audit，completed no-op只有receipt无transition audit。receipt保存完整原success body/status，不依赖后续asset状态。

Same actor existing command MUST 先完成existing command/canonical intent classification，再决定replay或command conflict；即使credential syntactically parseable但semantically invalid，也不得提前返回validation error。K2 MUST NOT参与该classification。只有genuinely new command才进入target lookup、credential semantic validation、lifecycle/revision与K2/remote阶段。

#### Scenario: 认证先于receipt可见性
- **WHEN** 未认证、session失效、非super_admin或CSRF失败
- **THEN** 按既有安全契约拒绝，不能读取receipt或探测其存在

#### Scenario: 相同actor与intent重放
- **WHEN** 同actor提交同command_id与同canonical intent且已有receipt
- **THEN** 返回原persisted status/body；无第二mutation、lineage、receipt或success audit，且无需K2时不得访问K2

#### Scenario: actor不匹配
- **WHEN** 另一个super_admin复用command_id
- **THEN** 409 command_conflict，不暴露原result

#### Scenario: actor conflict优先于后续错误
- **WHEN** receipt属于actor A，而actor B以同command_id提交malformed endpoint、invalid credential、unavailable K1/K2、stale revision或retired target
- **THEN** 全部立即返回409 command_conflict，且不能解析credential、访问K1/K2、执行或暴露任何后续validation结果

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

#### Scenario: same-actor Node semantic-invalid credential preserves command precedence
- **WHEN** same actor以existing Node command_id提交syntactically parseable但semantically invalid credential
- **THEN** 系统先按receipt-recorded encoding与canonical intent分类replay或command_conflict，不提前返回validation error且不访问K2

#### Scenario: same-actor Gateway semantic-invalid credential preserves command precedence
- **WHEN** same actor以existing Gateway command_id提交syntactically parseable但semantically invalid credential
- **THEN** 系统先按receipt-recorded encoding与canonical intent分类replay或command_conflict，不提前返回validation error且不访问K2

### Requirement: Canonical intent cryptography and durable key version

Historical v1 `canonical_intent_hash` MUST继续等于SHA-256(canonical_intent_v1_bytes)。Historical v1使用design的fixed-order UTF-8 JSON arrays，区分absent/clear/set，revision string与endpoint normalization一致；Secret只贡献HMAC-SHA-256 fingerprint与key version，raw reference不得进入canonical bytes或receipt。Historical receipt.secret_fingerprint_key_version set时为1，无secret set intent时NULL；replay MUST使用历史receipt version。既有v1 durable command bytes、receipt、hash与commitment MUST immutable且不得重写。

Stage 0 genuinely new credential-bearing commands MUST使用reviewed intent encoding v2，同时保持`secret_fingerprint_key_version=1`。Set contribution MUST精确为`["set", secret_fingerprint_key_version, credential_commitment]`，其中：

```text
credential_commitment = HMAC-SHA-256(
  existing K1,
  v2 domain || command_kind || exact credential bytes
)
```

V2 domain与command_kind MUST沿用canonical encoder的无歧义编码，其他tri-state contribution按冻结Architecture使用deterministic representation。Raw credential与recoverable sealed ciphertext MUST NOT进入canonical durable bytes、registry、receipt或audit；K2 MUST NOT参与semantic equivalence；不得建立第二套fingerprint framework。

K1是由`CONTROL_ASSET_INTENT_KEY_FILE`提供的稳定独立32-byte deployment Secret。文件 MUST 是regular file、不得是symlink、权限安全且内容exactly 32 raw bytes；missing、path/read failure、unsafe permissions、symlink或wrong length均表示K1 unavailable。K1 MUST 保持稳定、备份并跨restart/upgrade恢复，不得自动生成、记录或写入receipt。Stage 0不认证K1 identity，不使用signed digest、identity anchor、额外trust metadata或deployment identity file；未来rotation属于独立change。

K1只在canonical intent包含SecretSet时使用：SecretSet记录key version 1并计算HMAC commitment；SecretAbsent、SecretClear和其它non-SecretSet intent的receipt key version为NULL且 MUST NOT读取K1。receipt schema保持既有encoding/hash/key-version字段，不增加key digest/id/table。Historical算法、field order与每action fields保持design Canonical intent v1 bytes；Stage 0 v2只改变新credential-bearing command，不重定义现有v1 encoding。

#### Scenario: 跨restart与upgrade重放
- **WHEN** 重启或支持的升级后请求同一credential intent
- **THEN** 使用receipt-recorded encoding、持久version=1与K1计算相同commitment/hash，不泄露credential且不访问K2

#### Scenario: key缺失
- **WHEN** 新command或actor-matched历史receipt需要SecretSet K1 version 1，而文件missing/unreadable、不安全、是symlink或长度不是32 bytes
- **THEN** 503 service_unavailable，零mutation、receipt和success audit，且不得自动生成或fallback key

#### Scenario: 可用K1下的不同Secret intent
- **WHEN** actor匹配、K1通过基础文件验证且请求credential intent与receipt不同
- **THEN** 生成不同canonical hash并返回409 command_conflict

#### Scenario: 结构有效但被错误替换的K1
- **WHEN** 历史K1被另一把权限安全且长度为32 bytes的key替换，并用它重新计算同一credential请求
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

## ADDED Requirements

### Requirement: Credential values SHALL have one exact validation contract
Node `management_credential` 与 Gateway `directory_credential` string MUST 非空、UTF-8 encoded length 不超过4096 bytes，且不得包含NUL、CR或LF。系统 MUST保留exact bytes，不得trim或normalization。

#### Scenario: boundary values are classified by exact UTF-8 bytes
- **WHEN** credential分别为0、1、4096、4097 UTF-8 bytes，或multibyte input跨越4096-byte boundary
- **THEN** 仅1..4096 bytes且不含禁止字符的值通过验证，并保持exact bytes

#### Scenario: whitespace is preserved but control separators are rejected
- **WHEN** credential含leading/trailing spaces或含NUL、CR、LF
- **THEN** spaces原样保留，而NUL、CR、LF被拒绝

### Requirement: Asset and credential mutation SHALL commit atomically
Credential set/clear MUST与既有asset mutation、command registry、receipt与audit在同一transaction提交或回滚；失败不得留下asset/credential/command的部分状态。

#### Scenario: credential sealing or durable write fails
- **WHEN** 新command的Seal或protected state写入失败
- **THEN** asset revision、credential state、command terminal result、receipt与audit均不得部分提交

### Requirement: K2-unavailable command behavior SHALL be operation-specific
K2 unavailable时，keep、clear、Retire（包括credential erase）、Replace with unconfigured replacement及不需credential的terminal replay MUST继续按既有command contract工作，并且 MUST NOT Open/Seal/require K2。Set new credential与Replace with new credential MUST fail closed。

#### Scenario: unconfigured replacement does not require K2
- **WHEN** cipher unavailable且Replace明确创建unconfigured replacement
- **THEN** Replace按既有lifecycle/atomicity contract执行，不Open、不Seal且不要求K2

### Requirement: Stage 0 SHALL NOT expose crypto-specific public errors
Stage 0 MUST NOT新增`k2_*`、`aes_*`、`decrypt_*`、`cipher_*`等crypto-specific public error code。K2 unavailable、Seal/Open或decrypt failure MUST映射到既有冻结API taxonomy中适用的validation/unavailable family，不得泄露cryptographic detail。

#### Scenario: credential cipher failure is externally bounded
- **WHEN** API operation因K2 unavailable或credential Open failure无法继续
- **THEN** response使用既有批准的validation/unavailable error family，且body不包含K2、ciphertext、algorithm或filesystem detail
