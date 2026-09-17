## Context

见 [proposal.md](./proposal.md)。Phase 8 Stage 0 Requirements、Architecture A–L、Base TCCR 与 Compatibility Addendum O01–O05 已冻结；本设计只把批准结论映射到当前 Control 模块，不重新选择产品语义。Planning baseline 为 `7e73fb87ea46e8930afd94a3bd25f009ece5bedb`，pre-Stage0 validated runtime 为 `fab6aadc36a9f8ebe1309e5db457dcbac0136880`，Ops truth baseline 为 `5367d4a9546610221cb21addde64e8550e016aed`。

当前 Node/Gateway asset row 使用 `reader_secret_ref`，运行时经 `FileSecretResolver` 取得 credential；现有 asset command registry 以 actor-first、K1 commitment、revision/lifecycle transaction、receipt/audit 实现 durable replay。Stage 0 将 credential source 改为 asset-owned sealed state，但不得改变 Gateway Directory protocol、CLIProxyAPI native protocol、Phase 6/7 lifecycle/operation state machine或数据面边界。

## Goals / Non-Goals

**Goals:**

- 为恰好两类 management credential 提供 asset-owned protected-at-rest state、窄 DB access、K2 bootstrap/commitment 与 fail-closed runtime resolver。
- 保持 asset command actor-first/replay/atomicity，冻结 tri-state 与 Replace/Retire erase semantics。
- 规划 Migration `00051`、compatibility class 4 / floor 4、signed manifest v1 O01–O05 proof。
- 使 Node Inventory、Phase 7 account operations 与 Gateway Directory ingestion 全部切换到 protected resolver，生产路径零 legacy fallback。
- 通过最小 API/UI、ops/recovery 与 acceptance 变更完成 fresh-install cutover。

**Non-Goals:**

- generic Vault、Secret Manager/KMS plugin、key registry/version/rotation、per-asset key、provider/OAuth/API credential storage。
- importer、backfill、dual-read、dual-write、rolling upgrade、旧 deployment rollback、manifest v2。
- Gateway Account/Group CRUD、CLIProxyAPI credential ownership变化、scheduler/data-plane/automatic repair/move。
- Phase 8 Stage 1–4 或任何未冻结能力。

## Decisions

### 1. 一个外部 K2，K1 职责不变

`CONTROL_ASSET_INTENT_KEY_FILE`（K1）只继续承担 canonical command intent 的不可恢复等价 commitment。新增 `CONTROL_ASSET_CREDENTIAL_KEY_FILE`（K2）是 path-only process configuration：目标必须是 PostgreSQL 外、非 symlink 的 regular file，由预期 Control runtime owner 持有，mode 为批准的 `0400` 或 `0600`，内容恰好 32 raw bytes。进程启动时读取一次，进程生命周期不可变且不 hot reload。Raw K2 不得进入 CLI argument、environment value、Compose YAML literal、DB、API、UI、log、audit、metric、trace、receipt 或 diagnostic evidence。

Runtime 只验证文件结构，不声称能从任意 32-byte 输入推断 entropy，且 MUST NOT silently generate K2。受支持的 deployment/bootstrap tooling 仅可为 fresh environment create K2 exactly once：必须用批准的 OS-backed CSPRNG 直接产生 32 raw bytes，以 atomic safe file creation 安装；若文件已存在，重复 bootstrap MUST 为 no-op，不得 replace、truncate、rewrite 或 regenerate，existing K2 必须 byte-for-byte unchanged。RNG、create 或 write failure不得留下 partial usable K2 file。password/passphrase、UUID、timestamp、hostname、environment identity、deterministic seed、predictable metadata、`math/rand` 与 repeated/static operator-chosen bytes 均不受支持。

数据库保存一个 environment-level write-once K2 identity commitment，算法固定为：

```text
K2_identity_commitment =
SHA-256(UTF8("relay-station/control-asset-credential-key/v1") || 0x00 || K2)
```

该 commitment 是 non-secret、每环境唯一的 write-once identity evidence，不是 key version、registry、rotation metadata、ciphertext 或 raw K2。Commitment value MUST NOT 出现在normal API、UI、ordinary query output、log、audit、metrics、trace/telemetry、receipt、Browser state、test evidence或acceptance artifact/evidence output。内部PostgreSQL/store/acceptance observer MAY用它判断present/absent、matches expected/mismatch及before/after unchanged，但proof/evidence只能输出`PASS/FAIL`、适用时的`present=true/false`、`match=true/false`或`unchanged=true/false`；不得打印、记录、snapshot或导出value本身。已有 commitment 时 derived 值相等才使 cipher AVAILABLE；不等时本进程永久 UNAVAILABLE，禁止 Seal/Open、credential-bearing Set 与需要 credential 的 authenticated outbound。

Fresh install 仅在 commitment absent 且 Node/Gateway sealed state 均为零时初始化，顺序固定为：锁定 canonical environment ownership → 重查 commitment → 重查 zero sealed state → write once → commit。K2-A/K2-B 并发初始化恰好一个 winner；loser 锁后观察 mismatch 并保持 unavailable。Commitment absent 但任一 sealed state 已存在是 invalid database state：不得 adopt K2、不得写 commitment、不得 Seal/Open。

Missing、structurally invalid 或 wrong K2 不阻止进程启动；cipher unavailable、unrelated feature 可用，并且每进程恰好记录一条 bounded sanitized availability warning，不含 raw K2、credential、sealed blob、filesystem secret content 或 DB secret state。

备选的 per-asset key、Node/Gateway separate K2、KMS plugin 与 key registry 会引入资产数相关 provisioning、rotation/versioning 和额外恢复状态，均超出冻结 scope。

### 2. AES-256-GCM sealed value 与身份绑定

每个 owning asset row 恰好存一个 opaque sealed credential value：Node row 一个 sealed management credential field，Gateway row 一个 sealed Directory credential field。不得创建 separate credential table 或 separate nonce column。Representation 固定为 `12-byte nonce || AES-GCM ciphertext+authentication tag`。数据库 structural constraint 必须拒绝短到无法容纳12-byte nonce与non-empty authenticated GCM output的非NULL值；该长度检查仅证明shape，AES-GCM Open仍是authenticity/integrity的authoritative check。

每次 Seal 从注入的 cryptographic RNG 请求新的 12-byte nonce；deterministic test 验证每次 Seal 消耗下一份 RNG output，而不是概率性断言 nonce uniqueness。RNG failure 导致 Seal 失败且不得产生 partial durable mutation。AAD 精确为：

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

UUID使用canonical 16-byte binary UUID representation，不使用textual UUID characters。固定domain、`0x00` delimiter和binary UUID不得留给implementation重新选择。测试覆盖Node A→Node A成功、Node A→Node B失败、Node→Gateway失败、Gateway A→Gateway B失败。Open只在窄resolver中产生短生命周期plaintext；caller使用后清空/释放可控buffer，不把其写入durable/result/evidence surfaces。

### 3. Migration 00051 与数据库隔离

Migration `00051` 是 forward-only fresh-install target，计划：

- 在任何 schema mutation 前检查所有 legacy `reader_secret_ref` 均为 NULL；非 NULL 时整个 migration fail。
- 在 Node/Gateway owning row 各增加一个opaque sealed credential field；不建credential table或nonce column，并以structural minimum-length约束拒绝过短non-NULL blob。
- `secret_configured`精确定义为`sealed_credential IS NOT NULL`；该投影不得decrypt，不依赖K2 availability、Open success或blob authenticity。因此sealed blob存在时，即使K2 missing/wrong或ciphertext corrupt，读取仍返回true，credential-dependent operation随后独立fail closed。
- 增加 environment K2 identity commitment 及唯一/写一次约束。
- revoke ordinary runtime/registrar 对 protected columns 的直接读。本OpenSpec为当前Control选择并复用narrowly scoped SECURITY DEFINER write/read functions，固定owner/search_path/ACL；冻结Architecture/TCCR只要求effective protected-column isolation与constrained internal access，并未强制某一种数据库mechanism。
- preserve existing callable lifecycle/command transaction boundaries；credential mutation 通过受控函数原子参与。
- 将 durable compatibility floor 提升到 4，并与 Migration 51 双向校验。

不创建 importer/backfill/cutover state machine；legacy columns 可在同一 migration 按批准方案移除或变为不可用，但 production runtime不得读取。Fresh DB 直接到 target；开发环境用 fresh DB + re-register。

### 4. Actor-first command pipeline 与 intent v2

Credential-bearing Register/Edit/Replace 的阶段顺序固定为：

```text
command identity lookup
→ actor ownership
→ same-actor existing-command canonical intent/K1 comparison
→ only for genuinely new command: target lookup
→ tri-state validation and K1 credential-input commitment
→ lifecycle/revision validation
→ K2 verification + Seal
→ atomic asset/credential/command/receipt/audit transaction
→ remote action only where the existing command owns one
```

Different actor 在 credential parsing 前失败。同 actor replay/conflict 可构造 K1 commitment，但 MUST 不读取 K2。Intent encoding v2 明确区分 missing/string/null；v1 durable commands 不迁移也不重定义。

Tri-state：Register missing=unconfigured、string=set、null invalid；Edit missing=keep、string=set、null=clear；Replace missing=unconfigured、string=set、null invalid且不继承 predecessor。

所有 Node/Gateway credential string 必须非空、UTF-8 encoded length <=4096 bytes、无 NUL/CR/LF；exact bytes 原样保留，不 trim、不 normalization。测试覆盖 0/1/4096/4097 bytes、multibyte boundary、NUL/CR/LF 与 leading/trailing spaces。

Stage 0 保持 `secret_fingerprint_key_version=1`。Set 的 secret contribution 固定为 `["set", secret_fingerprint_key_version, credential_commitment]`，其中 commitment 是 `HMAC-SHA-256(existing K1, v2 domain || command_kind || exact credential bytes)`，沿用既有 canonical encoder 的无歧义编码。Raw credential 与 recoverable sealed ciphertext 不进入 intent/registry/receipt/audit；K2 永不参与 semantic equivalence；其他 tri-state contribution 使用冻结的 deterministic representation。

同 actor existing command 即使携带 syntactically parseable 但 semantically invalid credential，也必须先完成 existing command/canonical intent classification，再返回 replay 或 command conflict；不得提前返回 validation error。Node 与 Gateway 各有 owning test。Different actor malformed case仍在 credential parsing 前返回 actor ownership error。

### 5. 生命周期原子性

Credential set/clear 与 asset revision、command registry、receipt、audit 同 transaction。Retire 和 Replace predecessor 在原 lifecycle transaction 中清除 sealed state；Replace replacement 只接受显式 string或unconfigured。Node monitoring/binding/account-operation blockers与override顺序不变；Gateway Directory fencing/revision/lineage不变。任何失败全体回滚。

### 6. 窄 runtime resolvers

实现一个内部 `AssetCredentialCipher`，只负责 Seal/Open；Node 与 Gateway 各有窄 resolver，把 asset identity/lifecycle/fencing 与 protected read绑定。普通 repository/query 不获得 sealed column read。

Node resolver覆盖全部 authenticated consumer：Inventory/readonly management calls，以及 Phase 7 Disable/Enable/Remove/Upload New/Replace Existing。Gateway resolver在既有 Directory run/Gateway/lease/fencing条件下取 credential。目标 endpoint、protocol、timeouts、native response handling 和 durable execution state不变。

生产代码不存在 `reader_secret_ref` 或 `FileSecretResolver` fallback。K2 unavailable 时，keep、clear、Retire including credential erase、Replace with unconfigured replacement、credential-independent reads、永不需要 credential 的 health/readiness 和不需 credential 的 terminal replay均继续工作，且不得 Open/Seal/require K2。只有 Set new credential、Replace with new credential、Open existing credential for authenticated outbound 要求 K2并在 unavailable 时 fail closed。

### 7. OpenAPI、生成代码与最小 UI

写 contract 删除 `reader_secret_ref`，Node 使用 `management_credential`，Gateway 使用 `directory_credential`；tri-state通过 field presence/string/null 精确编码。读 contract 只返回 `secret_configured`。修改 `api/openapi.yaml` 后统一运行 `make generate`，不手工编辑 generated Go/TS。

Asset Registry 只适配输入、clear/keep、configured/unavailable与既有错误呈现；不做页面重构、i18n或新 workflow。Browser只保留 Base TCCR 中 Node credential UX、Gateway credential UX、Replace/unavailable 的代表性场景。

Stage 0 不新增 crypto-specific public error code（包括 `k2_*`、`aes_*`、`decrypt_*`、`cipher_*`）；K2 unavailable 与 decrypt/Open failure 复用现有冻结 taxonomy 中适用的 validation/unavailable family。

### 8. Compatgate 4/4 复用现有产品

修改既有 `internal/compatgate`、`cmd/relay-control-compat-gate`、`deploy/compatibility` 与 `deploy/acceptance/relay-control-compat-gate.sh`：

- `SupportedClass` 3→4；`Stage0MigrationVersion=51`。
- floor >=4 requires Migration 51；Migration 51 applied requires floor >=4。
- signed manifest v1格式、release signer、artifact identity与wrapper fail-closed sequencing不变。
- class 0..3 manifests仍结构有效；class < floor返回`compatibility_floor_rejected`。
- O04必须证明signed class-3 artifact在Control启动前被拒；O05内部观察reject/restore期间Node/Gateway sealed state与K2 commitment不变，但evidence只输出redacted boolean/PASS-FAIL结论，不输出commitment value。

不设计 manifest v2 或新的 compatibility framework。

### 9. Recovery 与运维

`devctl`/deployment provisioning必须生成或安装32-byte K2文件并使用受限权限。完整恢复集是 PostgreSQL backup + 同一K2；restore后重启在内部验证commitment match并验证Node/Gateway各一条credential path，外部evidence只输出`match=true/false`或PASS/FAIL，不输出commitment value。Wrong/missing K2按feature-level fail closed，绝不初始化新commitment覆盖已有数据库。Host migration复制DB与K2；rotation不在scope。

本地旧fixture数据库不导入：重建fresh DB、provision K2、重新注册资产。Runbook明确K2无hot reload、变更需restart。

### 10. TCCR 与验收所有权

`planning-validation.md`维护R1–R21→Architecture A–L→Base TCCR proof IDs + O01–O05→delta specs→tasks的crosswalk。Crypto/AAD/K2由unit；ACL/migration/atomicity/concurrency由PostgreSQL/store；runtime resolver由integration；OpenAPI/API由HTTP；recovery与compatgate由acceptance；Browser只证明用户交互。每一冻结事实在最低owning layer证明。

## Risks / Trade-offs

- [K2丢失导致两类credential不可用] → DB+K2联合备份、restore/host-migration acceptance、wrong/missing fail-closed Runbook。
- [wrong K2写入mixed-key state] → write-once identity commitment在任何Seal/Open前验证；不自动重置。
- [sealed column扩大普通runtime读取面] → column ACL + narrow SECURITY DEFINER function + role-negative tests。
- [actor-first被credential解析错误覆盖] → command pipeline顺序测试覆盖different actor、same actor replay/conflict与genuinely-new三类。
- [Replace/Retire留下orphan credential] → lifecycle transaction内原子erase与并发可见性测试。
- [compatibility floor与migration漂移] → O02/O03双向gate，O04 pre-start reject，O05状态不变observer。
- [fresh-install策略不支持旧DB平滑升级] → 这是冻结取舍；non-null legacy guard明确失败，使用fresh DB/re-register，不建设迁移框架。
- [UI或Browser重复lower-layer proof] → frontend最小适配，Browser仅保留TCCR批准的representative flows。

## Migration Plan

1. 完成 planning reconciliation 与 OpenSpec strict validation；Implementation保持NOT AUTHORIZED直至独立readiness re-review。
2. 授权后先实现K2/cipher/commitment基础及负向unit tests，不接入产品路径。
3. 实现Migration 00051、ACL、受控函数和compatgate class/floor 4，验证0→51与50→51、legacy guard、O01–O05 focused proof。
4. 按actor-first顺序改造asset commands与lifecycle atomicity，再接入Node/Gateway runtime consumers。
5. 更新OpenAPI并`make generate`，完成最小UI适配。
6. 更新provisioning/recovery harness并执行focused→owning groups→full non-Browser regression。
7. 从clean exact candidate构建immutable class-4 artifact，完成runtime/secret/provenance acceptance与最小Browser场景。

Rollback不使用Migration Down、dual-read或旧artifact启动。若candidate未通过gate/acceptance，停止candidate并恢复最后受支持环境；一旦数据库floor 4/Migration51成立，只允许兼容的class-4 artifact。Protected state与audit保留。
