## Planning validation

Phase 6 Architecture Review = PASS（Ops approval 既有事实）。本次仅修Gateway planning，不代替独立readiness review。

- reviewed Ops approval commit: `5add9cb54f0a488cc547b2ba72287c8f492a32f7`
- requirements baseline: `8c7cdbcf5ea3480da16d51408581a4be3e72c994`
- reviewed Control: `5e2caeb031a47510744cada56978b963da39b4e9`
- Gateway compatibility baseline: `6b045698e6e5e62e35dbd103abf20c1407f8a0bb`
- CLIProxyAPI compatibility baseline: `273d624c70f6eb8bdd7b049df396c306acd3f8d0`

## P2 disposition

RESOLVED表示planning选择已冻结，不表示DDL或runtime验收通过。

| Finding | Status | Frozen contract |
|---|---|---|
| P2-1 PK/index | RESOLVED | design Persistence direction：backfill ->新PK存在->移除旧FK/UNIQUE dependency->重建FK->nullable slot/shape；实际PG18 proof为task4 |
| P2-2 receipt/intent | RESOLVED | 全局command_id+actor；actor-first lazy intent；SHA-256 canonical arrays；SecretSet-only HMAC-SHA-256 K1；validly computed hash mismatch统一command_conflict；完整原HTTP body/status replay |
| P2-3 monitoring cancellation | DEFERRED | Node lifecycle change ownership，两个Node changes保持skeleton |
| P2-4 Gateway routes/filter | RESOLVED | exact current/list/detail/action routes；lifecycle active/retired/all；UUID keyset及cursor generation |
| P2-5 GetAssetCounts | RESOLVED | legacy gateways保持total；additive gateway_counts.active/retired/total，UI operational用active |
| P2-6 reasons | RESOLVED/shared | administrator_retire/replacement；binding gateway_retired/gateway_replaced |
| P2-7 compatibility | RESOLVED | signed manifest digest/class、external mandatory production wrapper、floor1、deployment sequencing、pinned old-artifact wrapper acceptance |

## Semantic validation

三个MODIFIED delta从baseline完整复制并修改：asset-registry 3个、Directory 6个、binding 4个Requirement。标题集合与对应baseline精确比较PASS（13/13）；既有scenario保留，已更新冲突前提，并增加生命周期race。

- asset-registry：数据库保存单环境资产关系；管理员可通过受保护只读 API 查看资产；只读资产页面处理空状态与故障。
- Directory：180s slot；固定HTTP fetch；last_success_received_at freshness；current state写入时钟；恢复/幂等/未知回写；retryability分类。
- binding：temporal interval；原子并发写；Account与Node生命周期边界；fresh Directory bind/rebind。

静态prefix已获独立Planning readiness PASS，本轮不改其artifact。Node两个skeleton未改。生产代码零修改，PG18 proof/真实binary rollback/production build都只列task，未执行。

## Independent readiness review round 1

Historical disposition: one P1 transport-composition finding was raised and resolved before the final readiness review.

Finding:
Stage 1 MODIFIED Gateway Directory planning preserved historical HTTP/HTTPS wording even though the newer archived `internal-http-transport` baseline makes Control-managed Gateway Directory/management endpoints HTTP-only.

Disposition:
Compose Stage 1 with the current HTTP-only baseline. Gateway `management_endpoint`、Directory target 和 Gateway Health/Connection Test target 仅允许 `http://`；`https://` 在 metadata validation 或 client construction 阶段 fail closed、发出零个 outbound request。不得恢复 TLS、certificate skip-verify、HTTP/HTTPS toggle、dual-protocol 或 HTTPS fallback。该组合不改变 Gateway Account/upstream 或 request data-plane endpoint scheme。

修订已同步 proposal、design、tasks、`asset-registry` delta、`gateway-account-directory-ingestion` delta 与 `gateway-asset-lifecycle` spec。Directory Requirement 现在由原 Gateway Directory Requirement、已归档 `internal-http-transport` baseline 和 Stage 1 lifecycle delta 完整合成；未来 implementation task 明确复用当前 `gatewaydirectory` HTTP-only validator/client，并验证 HTTPS target 在 outbound 前拒绝。

## K1 Contract Simplification Decision

Stage 1 implementation re-review发现既有contract无法区分“same Secret intent + accidentally replaced structurally-valid K1”和“different Secret intent + correct K1”。Architecture Review决定接受该诊断限制，拒绝signed K1 identity manifest、K1 signed digest/identity anchor、额外trust metadata和deployment identity file。

最终planning contract：K1 v1仍为`CONTROL_ASSET_INTENT_KEY_FILE`中的稳定32 raw bytes；regular file、no symlink、safe permissions、exact length通过即为可用。K1必须备份、跨restart/upgrade保持且不得自动生成或记录。只有SecretSet intent读取K1；non-SecretSet和receipt key version NULL不读取K1。required K1缺失或基础文件验证失败返回503；使用结构可用K1成功计算后，hash相等replay，hash不同统一409 command_conflict。

明确trade-off：Stage 1不区分genuinely different Secret intent与same Secret intent evaluated with an accidentally replaced but structurally valid 32-byte K1；两者在computed hash不同时均返回command_conflict。恢复正确历史K1后same request恢复正常replay。该选择保留fail-closed correctness，仅损失错误诊断精度。

actor-first顺序不变：认证/授权/CSRF后，transaction advisory lock和receipt actor lookup发生在endpoint/Secret validation、K1 access及domain validation前。receipt schema、migration 33、compatibility class 1和floor 1保持不变。

K1 identity architecture gap = RESOLVED BY CONTRACT SIMPLIFICATION
K1 architecture decision = COMPLETE
Independent architecture re-review = PASS
P0 = 0
P1 = 0
P2 = 0

## Validation results

- 四个change逐项 openspec validate --strict：PASS。
- openspec validate --all --strict：27 passed / 0 failed。
- MODIFIED准确baseline标题检查：PASS。
- git diff --check：PASS。
- Planning 阶段当时所有42个implementation tasks均未完成（0 checked）；该记录是实施授权前的历史快照。

CLI语法通过不等于planning readiness批准。Artifact密钥/签名与gate部署行为需后续实现验收验证，当前没有编造binary digest、DDL运行结果或runtime evidence。

## Scope and apply gate

Detailed planning = COMPLETE
Independent readiness review = PASS
Independent architecture re-review = PASS
P0 = 0
P1 = 0
P2 = 0
Planning readiness = PASS / READY
Implementation readiness = READY
implementation workflow = AUTHORIZED AFTER EXPLICIT USER AUTHORIZATION
openspec instructions apply = RUN（仅本 change）
Implementation = COMPLETE
Runtime Acceptance = PASS
completed implementation tasks = 42 / 42
Independent implementation review = PASS
P0 = 0
P1 = 0
P2 = 0
Task 9 = COMPLETE
Task 11 = COMPLETE
Task 42 = COMPLETE
Task 42 closeout evidence reconciliation = COMPLETE
Stage 1 implementation commit = `d22da75e8ca49bd05bbb641592238213484ed757`
post-implementation-commit clean worktree verification = PASS
Git/worktree closeout = COMPLETE
Archive readiness = READY FOR INDEPENDENT ARCHIVE-READINESS REVIEW
Archive = NOT RUN

顺序：planning artifacts complete -> strict PASS -> independent readiness review -> readiness PASS -> 单独授权apply -> execute implementation tasks -> evidence -> Runtime Acceptance -> archive readiness。
