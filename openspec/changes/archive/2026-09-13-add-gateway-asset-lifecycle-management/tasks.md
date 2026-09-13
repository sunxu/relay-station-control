## Apply gate

proposal/spec/design/tasks complete -> strict PASS -> independent planning/readiness review -> readiness PASS -> explicit user implementation authorization -> `openspec instructions apply` -> execute tasks -> implementation evidence -> Runtime Acceptance -> independent implementation review -> archive readiness。K1 architecture gap已由contract simplification解决；Independent architecture re-review = PASS；Independent implementation review = PASS（P0=0 / P1=0 / P2=0）；Planning readiness = PASS / READY；Implementation readiness = READY；Implementation = COMPLETE；Runtime Acceptance = PASS；completed implementation tasks = 42 / 42；post-implementation-commit clean worktree verification = PASS；Git/worktree closeout = COMPLETE；implementation、closeout与reconciliation commits均已PUSHED；Archive readiness = PASS / APPROVED；Archive = COMPLETE。Stage 1 implementation commit为`d22da75e8ca49bd05bbb641592238213484ed757`，closeout commit为`58d4d1b4d3c7e6e905c5b0c180a3945cd9a5c264`，reconciliation commit为`7e106b83662517f53f7e54927ba44d88bcba8735`。

## Tasks

- [x] 1. 实现独立gate artifact及签名manifest校验单元测试（≤2h）。
- [x] 2. 配置支持的Compose/systemd mandatory wrapper与release sequencing（≤2h）。
- [x] 3. 实现marker monotonic floor和schema/gate fail-closed检查（≤2h）。
- [x] 4. 用本地PostgreSQL 18运行PK/FK DDL proof；逐步验证conindid、临时index清除、失败rollback（≤2h）。
- [x] 5. 编写Gateway additive columns/backfill/PK-FK切换forward migration（≤2h）。
- [x] 6. 实现lifecycle/revision CHECK及retired_by actor FK，补DB invariant测试（≤2h）。
- [x] 7. 创建immutable lineage与replaced_by FK、UPDATE/DELETE/TRUNCATE拒绝测试（≤2h）。
- [x] 8. 实现shared receipt persistence及immutable约束（≤2h）。
- [x] 9. 实现command advisory lock、真正的receipt actor-first lookup和lazy canonical intent builder；覆盖actor mismatch优先于invalid endpoint/Secret、K1、stale revision与retired target，receipt-recorded未知encoding返回503（≤2h）。
- [x] 10. 实现canonical arrays/SHA-256及per-action encoding fixtures（≤2h）。
- [x] 11. 实现K1 v1稳定文件处理和SecretSet-only lazy K1 requirement；覆盖基础文件验证、missing K1=503、non-SecretSet/NULL key version不访问K1、可用K1 hash mismatch=409、restart/upgrade replay及backup/restore运维证据（≤2h；不增加identity anchor或改receipt schema/class/floor）。
- [x] 12. 实现原完整body/status receipt与completed no-op contract测试（≤2h）。
- [x] 13. 增加Gateway queries/sqlc定义与revision读写（≤2h）。
- [x] 14. 实现Register transaction与current-slot竞争翻译（≤2h）。
- [x] 15. 实现Edit transaction与patch/revision validation（≤2h）。
- [x] 16. 实现Retire transaction和同boundary binding close（≤2h）。
- [x] 17. 实现Replace transaction与lineage/no-inheritance（≤2h）。
- [x] 18. 实现binding Node->Gateway->Directory->binding锁协议及Gateway reasons（≤2h）。
- [x] 19. 实现Directory planner current eligibility及zero-current tests，复用现有Gateway Directory HTTP-only endpoint validator（≤2h）。
- [x] 20. 实现Directory outbound target fence及retirement分类；覆盖HTTP成功、`https://` client construction前拒绝且零request，不恢复TLS/dual-protocol branch（≤2h）。
- [x] 21. 实现Directory promotion/recovery fence及独立freshness（≤2h）。
- [x] 22. 更新OpenAPI固定action/routes/status/body与schemas（≤2h）。
- [x] 23. 生成Go/TypeScript clients并验证revision string无损（≤2h）。
- [x] 24. 接入Gateway HTTP handlers、super_admin、CSRF、no-store与固定错误翻译（≤2h）。
- [x] 25. 复用现有 `gatewaydirectory` HTTP-only origin validator/transport safety primitives，实现management target上的固定health/probe、timeout与observation audit；覆盖`http://` + `/health`、`https://` probe前拒绝且零request，无TLS/fallback branch（≤2h）。
- [x] 26. 实现asset_gateway audit allowlist/atomic failure tests（≤2h）。
- [x] 27. 实现bounded metric families和label canary测试（≤2h）。
- [x] 28. 实现history/current read、counts compatibility与cursor generation（≤2h）。
- [x] 29. 补分页stable snapshot、cursor stale/filter mismatch测试（≤2h）。
- [x] 30. 实现现有Asset Registry Gateway mutation controls（≤2h；static prerequisite完成后）。
- [x] 31. 实现Gateway历史detail/lineage和active counts展示（≤2h）。
- [x] 32. 补Gateway lifecycle/lineage集成测试（≤2h）。
- [x] 33. 补binding-vs-Gateway lifecycle race测试（≤2h）。
- [x] 34. 补Directory-vs-Retire/Replace race及HTTP-only transport组合回归测试（≤2h）。
- [x] 35. 补同command/actor mismatch/concurrent Register/Replace/crash replay测试（≤2h）。
- [x] 36. 以正式wrapper运行pinned old binary rollback，证明未启动process/HTTP/workers和DB不变（≤2h）。
- [x] 37. 验收tampered manifest/digest、missing key/DB与gate bypass配置拒绝（≤2h）。
- [x] 38. 更新runbook、签名artifact登记、K1 backup/restore和证据引用（≤2h）。
- [x] 39. 执行generation reproducibility检查（≤2h）。
- [x] 40. 执行make test build及错误处理（≤2h单次工作块，失败新增明确修复task）。
- [x] 41. 执行Gateway PostgreSQL/container acceptance并记录结果（≤2h单次工作块）。
- [x] 42. 记录implementation evidence、durable truth reconciliation及clean-worktree/archive readiness（≤2h；implementation commit后clean worktree、strict validation与archive-readiness evidence均已核对）。
