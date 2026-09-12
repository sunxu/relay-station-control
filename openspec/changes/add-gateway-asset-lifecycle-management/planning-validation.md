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
| P2-2 receipt/intent | RESOLVED | 全局command_id+actor；SHA-256 canonical arrays；HMAC-SHA-256 K1/version持久化与non-rotating retention；完整原HTTP body/status replay |
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

## Validation results

- 四个change逐项 openspec validate --strict：PASS。
- openspec validate --all --strict：27 passed / 0 failed。
- MODIFIED准确baseline标题检查：PASS。
- git diff --check：PASS。
- 所有42个implementation tasks均未完成（0 checked）。

CLI语法通过不等于planning readiness批准。Artifact密钥/签名与gate部署行为需后续实现验收验证，当前没有编造binary digest、DDL运行结果或runtime evidence。

## Scope and apply gate

Implementation readiness = AWAITING REVIEW
openspec apply = NOT AUTHORIZED
Implementation = NOT STARTED
Runtime Acceptance = NOT STARTED

顺序：planning artifacts complete -> strict PASS -> independent readiness review -> readiness PASS -> 单独授权apply -> execute implementation tasks -> evidence -> Runtime Acceptance -> archive readiness。
