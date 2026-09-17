## MODIFIED Requirements

### Requirement: Node canonical intent SHALL reuse shared v1 encoding exactly

Historical Node action v1 canonical intent arrays、receipts、hashes与commitments MUST remain immutable且MUST NOT被重写。Stage 0改变新`node.register`、`node.edit`与`node.replace` command的credential semantics，因此genuinely new credential-bearing commands MUST使用explicitly reviewed encoding version 2，同时保留既有SHA-256 `canonical_intent_hash`、K1与`secret_fingerprint_key_version=1`；不得引入第二套fingerprint framework或K1 version。

Historical frozen v1 arrays remain：

```text
Register: [1, "node.register", new_instance_id, display_name,
           normalized_management_endpoint, node_type, driver_contract_version,
           sorted_capabilities, secret_triplet]
Edit:     [1, "node.edit", instance_id, expected_revision,
           display_name_patch, management_endpoint_patch, secret_triplet]
Retire:   [1, "node.retire", instance_id, expected_revision,
           "administrator_retire"]
Replace:  [1, "node.replace", old_instance_id, expected_revision, new_instance_id,
           new_display_name, new_normalized_management_endpoint, new_node_type,
           new_driver_contract_version, sorted_new_capabilities,
           new_secret_triplet, "replacement"]
```

Historical v1 `secret_triplet` remains `[operation, secret_fingerprint_key_version, secret_fingerprint]` with `absent -> ["absent",null,null]`, `clear -> ["clear",null,null]`, `set -> ["set",1,"<64 lowercase hex>"]`。Historical Edit的display_name/management_endpoint patch限制与v1 byte structure保持不变。

Stage 0 v2 MUST将Node credential contribution确定性编码为冻结tri-state。Set contribution精确为`["set", secret_fingerprint_key_version, credential_commitment]`，其中`secret_fingerprint_key_version=1`，`credential_commitment=HMAC-SHA-256(existing K1, v2_domain || command_kind || exact_credential_bytes)`；v2 domain与command_kind MUST无歧义编码。Raw credential、recoverable sealed ciphertext与K2 MUST NOT进入canonical durable bytes、registry、receipt或audit，K2不得参与semantic equivalence。

Credential string MUST非空、至多4096 UTF-8 bytes、无NUL/CR/LF，并保留exact bytes，不trim、不normalize。Actor ownership先于credential parsing；same-actor existing command先按durable encoding version与canonical intent/K1 commitment分类replay或command_conflict，K2不得参与。

#### Scenario: Encoding fixture parity
- **WHEN** 历史Node action canonical intent或receipt被读取/重放
- **THEN** 其v1 byte sequence、array order、secret_triplet、hash与K1 version保持冻结值，不被v2改写

#### Scenario: No second encoding scheme
- **WHEN** Stage 0为existing Node credential-bearing command生成新intent
- **THEN** 使用reviewed v2 array与既有SHA-256/K1/key-version-1机制，不引入第二套hash、fingerprint framework或K1 version；Retire及历史v1数据保持原定义

#### Scenario: Same-actor semantic-invalid credential preserves existing-command precedence
- **WHEN** same actor以existing command_id提交syntactically parseable但semantic-invalid的Node credential
- **THEN** 系统先用existing command的durable version与canonical/K1 evidence分类replay或command_conflict，不因后续credential semantic validation提前返回validation error，且不访问K2

## ADDED Requirements

### Requirement: Relay Node lifecycle SHALL own exact management credential state
Node Register/Edit/Replace MUST按冻结tri-state写入`management_credential` sealed state。Register missing=unconfigured、string=set、null invalid；Edit missing=keep、string=set、null=clear；Replace missing=unconfigured、string=set、null invalid且不得继承predecessor。Credential validation MUST覆盖0/1/4096/4097 UTF-8 bytes、multibyte boundary、NUL/CR/LF与leading/trailing spaces；有效spaces作为exact bytes保留。

#### Scenario: Edit clear removes configured state
- **WHEN** 管理员Edit active Node并显式提交`management_credential=null`
- **THEN** lifecycle保持active、revision按既有规则推进、sealed credential被原子清除且读模型返回`secret_configured=false`

#### Scenario: Node credential exact validation
- **WHEN** Register/Edit/Replace提交边界或禁止字符credential
- **THEN** 仅1..4096 UTF-8 bytes且无NUL/CR/LF被接受，输入不trim、不normalize

### Requirement: Node Retire and Replace predecessor SHALL erase credential atomically
Retire与Replace predecessor MUST在同一Node lifecycle transaction中清除sealed credential，并保持既有monitoring closure、binding closure、account-operation blocker/override、revision、receipt与audit语义。K2 unavailable时keep、clear、Retire和Replace-with-unconfigured MUST仍可执行且不得Open/Seal或require K2；只有Set、Replace-with-new-credential与authenticated outbound需要K2。

#### Scenario: blocked Retire leaves credential unchanged
- **WHEN** Node Retire被既有account operation blocker拒绝且没有有效override
- **THEN** lifecycle、revision与sealed credential均不改变，错误仍为既有exact contract

#### Scenario: K2 unavailable does not block credential erasure
- **WHEN** K2 unavailable且管理员Retire Node或Replace为unconfigured successor
- **THEN** lifecycle command与credential erase原子完成，不调用Open/Seal且不要求K2
