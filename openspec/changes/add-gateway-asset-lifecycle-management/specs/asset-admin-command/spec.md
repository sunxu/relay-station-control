## ADDED Requirements

### Requirement: Durable admin command identity and replay

共享durable command MUST 先完成authentication、active session、super_admin、CSRF，再进入transaction。command_id全局唯一；取得由完整UUID派生的advisory lock后lookup receipt，先校验actor_admin_id，再校验canonical intent。receipt lookup MUST 早于lifecycle/revision/current-state evaluation。每个accepted/completed command exactly one immutable receipt；不完整transaction无receipt。state-changing success同事务exactly one mutation audit，completed no-op只有receipt无transition audit。receipt保存完整原success body/status，不依赖后续asset状态。

#### Scenario: 认证先于receipt可见性
- **WHEN** 未认证、session失效、非super_admin或CSRF失败
- **THEN** 按既有安全契约拒绝，不能读取receipt或探测其存在

#### Scenario: 相同actor与intent重放
- **WHEN** 同actor提交同command_id与同canonical intent且已有receipt
- **THEN** 返回原persisted status/body；无第二mutation、lineage、receipt或success audit

#### Scenario: actor不匹配
- **WHEN** 另一个super_admin复用command_id
- **THEN** 409 command_conflict，不暴露原result

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

canonical_intent_hash MUST 等于 SHA-256(canonical_intent_v1_bytes)。v1使用design的fixed-order UTF-8 JSON arrays，区分absent/clear/set，revision string与endpoint normalization一致；Secret只贡献HMAC-SHA-256 fingerprint与key version，raw reference不得进入canonical bytes或receipt。receipt.secret_fingerprint_key_version set时为1，无secret set intent时NULL；replay MUST 使用历史receipt version。K1为稳定独立deployment Secret；v1 non-rotating，v1 receipts可replay期间不可删除K1，未来rotation需要独立contract change。key不可用503 fail closed，不能退回unkeyed或ephemeral key。算法、field order与每action fields见design Canonical intent v1 bytes，属于共享encoding约束，Node后续扩展独立command_kind不得重新定义现有编码。

#### Scenario: 跨restart与upgrade重放
- **WHEN** 重启或支持的升级后请求同一Secret intent
- **THEN** 使用持久version=1与K1计算相同fingerprint/hash，不泄露Secret

#### Scenario: key缺失
- **WHEN** 历史receipt的key不可解析
- **THEN** 503拒绝，不把不同key的hash当成正常conflict，也不重建key

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
