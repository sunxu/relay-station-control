> 本文件是未来获得明确 implementation authorization 后的执行计划。当前创建/勾选本文件不授权任何 implementation task；本轮所有任务保持未勾选。

## 0. Planning Reconciliation

- [ ] 0.1 复核 Ops Requirements R1–R21、Architecture A–L、Base TCCR 与 Addendum O01–O05 均能映射到本 change 的 delta specs/tasks，并以 `openspec validate simplify-node-gateway-management-credentials --strict` 验证 planning artifacts。
- [ ] 0.2 在开始实现前取得新的 Implementation Readiness PASS 与明确授权，并记录授权基线；验证未授权前 `migrations/00051*`、product/API/frontend/runtime 文件均不存在 Stage 0 改动。

## 1. K2 / Crypto Foundation

- [ ] 1.1 为 `CONTROL_ASSET_CREDENTIAL_KEY_FILE` 编写 runtime structural failing matrix：missing、unreadable、symlink、non-regular、31/32/33 bytes、unsafe permissions、wrong owner；再实现 path-only、一次加载、无 hot reload 的 loader，并证明 raw K2 不进入 CLI/env value/Compose literal/DB/API/UI/evidence surfaces。
- [ ] 1.2 更新dev/bootstrap与人工provisioning：仅用批准的OS-backed CSPRNG生成恰好32 raw bytes，以atomic safe file creation安装为批准owner及`0400`/`0600`；测试first create、repeat no-op/no-replace、existing bytes preservation、RNG/create/write failure无partial usable file。Control runtime missing K2时只feature-limit且绝不生成文件；不得使用password/passphrase、UUID、timestamp、hostname、environment identity、deterministic seed、predictable metadata、`math/rand`或repeated/static operator-chosen bytes，且runtime不声称推断entropy。
- [ ] 1.3 为 exact K2 commitment 算法编写测试，覆盖已有 match/mismatch、fresh init lock→recheck commitment→recheck zero sealed state→write once、K2-A/K2-B exactly-one-winner race，以及 commitment absent + sealed state present 的 invalid DB；实现后证明 loser/mismatch 进程不可 Seal/Open/Set/authenticated outbound。
- [ ] 1.4 为`AssetCredentialCipher`编写AES-256-GCM测试，冻结Node AAD=`UTF8("relay-station/node-management-credential/v1")||0x00||16-byte binary UUID`与Gateway AAD=`UTF8("relay-station/gateway-directory-credential/v1")||0x00||16-byte binary UUID`；覆盖Node A→Node A成功、Node A→Node B失败、Node→Gateway失败、Gateway A→Gateway B失败、injected crypto RNG每次Seal消费新的12-byte output、RNG failure无partial durable mutation、tamper/wrong key/truncated/opaque layout；不得使用textual UUID或概率型nonce uniqueness断言。
- [ ] 1.5 增加plaintext生命周期、single bounded startup warning与shared secret scanner负向测试，扫描plaintext credential、raw K2、sealed credential blob、K2 identity commitment value、credential-bearing headers与raw native body，证明其不进入API/UI/ordinary query/log/audit/metrics/trace/receipt/Browser state/test evidence/diagnostic evidence，再运行focused security tests。

## 2. Migration / Protected State / Compatibility Gate

- [ ] 2.1 创建forward-only `migrations/00051_<descriptive_name>.sql`，先实现non-null legacy `reader_secret_ref` hard guard，再在各owning asset row添加恰好一个opaque sealed credential field和K2 commitment；禁止credential table与nonce column，以structural minimum-length constraint拒绝无法容纳12-byte nonce+non-empty GCM output的非NULL blob，验证DB不冒充authenticity检查及0→51/50→51。
- [ ] 2.2 实现`secret_configured = sealed_credential IS NOT NULL`及store/API tests，覆盖sealed present + missing K2、wrong K2、corrupt ciphertext均为true且读取不Open；同时为protected columns实现本OpenSpec选择的窄SECURITY DEFINER读写函数、固定owner/search_path/grants，并以角色矩阵验证ordinary direct read/write被拒绝。
- [ ] 2.3 删除/禁用production schema/query对legacy reference的依赖并重新生成sqlc；用静态搜索与store tests证明零production dual-read/dual-write/importer/backfill路径。
- [ ] 2.4 更新`internal/compatgate`到`SupportedClass=4`与`Stage0MigrationVersion=51`，增加O01–O03 unit/PostgreSQL integration，验证signed manifest v1格式与class0..3结构兼容不变。
- [ ] 2.5 更新`cmd/relay-control-compat-gate`、`deploy/compatibility`及wrapper acceptance，完成O04 class-3 pre-start reject与`compatibility_floor_rejected` proof，验证Control artifact未启动。
- [ ] 2.6 增加O05 PostgreSQL observer，内部证明O04 reject及class-4 restore/acceptance不改Node/Gateway sealed state或K2 commitment，并验证wrapper fail-closed sequencing不变；evidence只输出PASS/FAIL或`present`/`match`/`unchanged`布尔值，不打印、记录、snapshot或导出commitment value。

## 3. Asset Command Semantics

- [ ] 3.1 为Node/Gateway Register/Edit/Replace建立tri-state及exact credential validation matrix，覆盖missing/string/null、Replace不继承、0/1/4096/4097 UTF-8 bytes、multibyte boundary、NUL/CR/LF、leading/trailing spaces与no trim/no normalization，再实现API到command intent v2的表达。
- [ ] 3.2 为actor-first增加different-actor malformed credential，以及same-actor existing command + syntactically parseable但semantically invalid credential的Node/Gateway各一例；证明existing command/canonical intent classification先决定replay/conflict，genuinely-new command才进入credential/K2/lifecycle/revision/remote阶段。
- [ ] 3.3 实现`secret_fingerprint_key_version=1`、Set contribution `["set", version, commitment]`与`HMAC-SHA-256(existing K1, v2 domain || command_kind || exact credential bytes)`，验证其他tri-state deterministic、同actor replay不读取K2、不重新Seal、不新增receipt/audit，且v1 durable command不可变、raw/sealed/K2不进入semantic evidence。
- [ ] 3.4 将credential set/clear纳入Node/Gateway asset command transaction，故障注入验证asset revision、sealed state、registry、receipt与audit全体提交或回滚。
- [ ] 3.5 实现Node/Gateway Retire及Replace predecessor原子erase，覆盖blocker/override、并发Edit/Retire/Replace、lineage/monitoring/binding closure，验证不存在orphan或提前清除。
- [ ] 3.6 增加K2-unavailable operation matrix：keep、clear、Retire erase、unconfigured Replace、credential-independent reads/health及无需credential的terminal replay继续成功且零Open/Seal；Set、credential-bearing Replace及authenticated outbound fail closed。

## 4. Runtime Credential Consumers

- [ ] 4.1 实现窄Node protected credential resolver与ACL-backed read，切换CLIProxyAPI readonly/Inventory authenticated calls；验证endpoint/protocol/timeout/parser不变及unconfigured/corrupt/wrongK2零fallback。
- [ ] 4.2 将Phase 7 account operations全部authenticated Node consumer接入同一resolver；复用并运行Node-first admission、same-account、remote_noop、terminal replay、outcome_unknown与override回归，证明credential source外语义不变。
- [ ] 4.3 实现Gateway Directory protected resolver并绑定既有Gateway/run/lease/fencing条件；覆盖credential race、retired/replaced target、stale fencing、wrong/missing K2且不改变Directory protocol/snapshot/freshness。
- [ ] 4.4 静态与运行时证明production不存在`FileSecretResolver`/`reader_secret_ref` fallback，并验证credential-free health/list/detail/terminal replay在K2 unavailable时仍可用。

## 5. API / Generated / Frontend

- [ ] 5.1 修改`api/openapi.yaml`：移除write-side `reader_secret_ref`，增加Node `management_credential`、Gateway `directory_credential` tri-state，read-side仅保留`secret_configured`；以OpenAPI strict/contract tests验证。
- [ ] 5.2 运行`make generate`刷新Go/sqlc/TypeScript生成物，验证无手工生成代码编辑且生成链可重复。
- [ ] 5.3 更新API handler/service映射与HTTP tests，覆盖Register/Edit/Replace tri-state、actor-first error precedence、authorization/CSRF、no-reflection与exact bounded errors。
- [ ] 5.3a 验证K2 unavailable/Open failure仅使用现有批准的validation/unavailable family，且不存在`k2_*`、`aes_*`、`decrypt_*`、`cipher_*` public error code或crypto detail泄漏。
- [ ] 5.4 最小适配Asset Registry Node/Gateway表单与状态，使用稳定`data-testid`，以unit/component/typecheck证明set/keep/clear/configured/unavailable且无Secret DOM/browser-storage reflection。
- [ ] 5.5 仅实现Base TCCR批准的代表性Browser场景：Node credential UX、Gateway credential UX、Replace+unavailable；验证Browser不重复crypto/ACL/migration/race/compatgate矩阵。

## 6. Ops / Recovery

- [ ] 6.1 更新devctl/deployment provisioning生成或安装32-byte OS-CSPRNG K2文件、受限权限与启动配置；验证K2不进入镜像、Git、DB或普通日志。
- [ ] 6.2 更新backup/restore Runbook与harness，把PostgreSQL+K2作为恢复集；分别验证Node与Gateway credential在正确K2 restore后可用。
- [ ] 6.3 增加missing/wrong K2 restore与no-hot-reload测试，证明feature-level fail closed、sealed state/commitment不变且修改K2后必须restart。
- [ ] 6.4 更新host migration和fresh local DB/re-register流程，验证不需要per-asset Secret重建且不存在legacy importer/backfill/dual-read/dual-write。

## 7. Verification / Acceptance

- [ ] 7.1 依次运行crypto、store/migration/ACL、asset command、runtime resolver、API/frontend focused groups；每组首个真实blocker fail-fast，全部通过后运行full store/accountadmin/API与focused race。
- [ ] 7.2 运行`make test build`、`go vet ./...`、frontend unit/typecheck、`openspec validate simplify-node-gateway-management-credentials --strict`与`openspec validate --all --strict`，记录命令、runtime和结果。
- [ ] 7.3 从clean exact candidate构建immutable class-4 Control artifact，验证source→manifest→image→running identity及Migration51/floor4 provenance，不重建Gateway/Node。
- [ ] 7.4 在production-like stack执行O01–O05与最小Node/Gateway/Phase7/recovery runtime acceptance，记录最长business wait、zero data-plane dependency与zero Inventory-as-execution-truth。
- [ ] 7.5 运行一次可复用shared secret scan，证明plaintext credential、raw K2、sealed credential blob、K2 identity commitment value、credential-bearing headers与raw native body在API/DOM/log/audit/metrics/trace/Browser state/test evidence/acceptance artifact为零泄漏，并完成独立P0/P1/P2 review。
- [ ] 7.6 对照R1–R21、Architecture A–L、Base TCCR proof IDs与O01–O05完成最终coverage reconciliation，更新implementation evidence、canonical specs与Ops release truth，最后验证clean worktree；只有全部门禁PASS后才申请archive/closeout。
