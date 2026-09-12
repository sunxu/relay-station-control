## Apply gate

proposal/spec/design/tasks complete -> strict PASS -> independent planning/readiness review -> readiness PASS -> explicit user implementation authorization -> `openspec instructions apply` -> execute tasks -> implementation evidence -> Runtime Acceptance -> archive readiness。当前 Independent readiness review = PASS；Planning readiness = PASS / READY；Implementation readiness = READY；implementation workflow = AUTHORIZED AFTER EXPLICIT USER AUTHORIZATION；instructions apply = NOT RUN；Implementation = NOT STARTED。所有checkbox是未来implementation计划。

## Tasks

- [ ] 1. 实现独立gate artifact及签名manifest校验单元测试（≤2h）。
- [ ] 2. 配置支持的Compose/systemd mandatory wrapper与release sequencing（≤2h）。
- [ ] 3. 实现marker monotonic floor和schema/gate fail-closed检查（≤2h）。
- [ ] 4. 用本地PostgreSQL 18运行PK/FK DDL proof；逐步验证conindid、临时index清除、失败rollback（≤2h）。
- [ ] 5. 编写Gateway additive columns/backfill/PK-FK切换forward migration（≤2h）。
- [ ] 6. 实现lifecycle/revision CHECK及retired_by actor FK，补DB invariant测试（≤2h）。
- [ ] 7. 创建immutable lineage与replaced_by FK、UPDATE/DELETE/TRUNCATE拒绝测试（≤2h）。
- [ ] 8. 实现shared receipt persistence及immutable约束（≤2h）。
- [ ] 9. 实现command advisory lock与actor-first receipt lookup（≤2h）。
- [ ] 10. 实现canonical arrays/SHA-256及per-action encoding fixtures（≤2h）。
- [ ] 11. 实现stable K1加载/version retention与HMAC测试，验证restart/upgrade replay（≤2h）。
- [ ] 12. 实现原完整body/status receipt与completed no-op contract测试（≤2h）。
- [ ] 13. 增加Gateway queries/sqlc定义与revision读写（≤2h）。
- [ ] 14. 实现Register transaction与current-slot竞争翻译（≤2h）。
- [ ] 15. 实现Edit transaction与patch/revision validation（≤2h）。
- [ ] 16. 实现Retire transaction和同boundary binding close（≤2h）。
- [ ] 17. 实现Replace transaction与lineage/no-inheritance（≤2h）。
- [ ] 18. 实现binding Node->Gateway->Directory->binding锁协议及Gateway reasons（≤2h）。
- [ ] 19. 实现Directory planner current eligibility及zero-current tests，复用现有Gateway Directory HTTP-only endpoint validator（≤2h）。
- [ ] 20. 实现Directory outbound target fence及retirement分类；覆盖HTTP成功、`https://` client construction前拒绝且零request，不恢复TLS/dual-protocol branch（≤2h）。
- [ ] 21. 实现Directory promotion/recovery fence及独立freshness（≤2h）。
- [ ] 22. 更新OpenAPI固定action/routes/status/body与schemas（≤2h）。
- [ ] 23. 生成Go/TypeScript clients并验证revision string无损（≤2h）。
- [ ] 24. 接入Gateway HTTP handlers、super_admin、CSRF、no-store与固定错误翻译（≤2h）。
- [ ] 25. 复用现有 `gatewaydirectory` HTTP-only origin validator/transport safety primitives，实现management target上的固定health/probe、timeout与observation audit；覆盖`http://` + `/health`、`https://` probe前拒绝且零request，无TLS/fallback branch（≤2h）。
- [ ] 26. 实现asset_gateway audit allowlist/atomic failure tests（≤2h）。
- [ ] 27. 实现bounded metric families和label canary测试（≤2h）。
- [ ] 28. 实现history/current read、counts compatibility与cursor generation（≤2h）。
- [ ] 29. 补分页stable snapshot、cursor stale/filter mismatch测试（≤2h）。
- [ ] 30. 实现现有Asset Registry Gateway mutation controls（≤2h；static prerequisite完成后）。
- [ ] 31. 实现Gateway历史detail/lineage和active counts展示（≤2h）。
- [ ] 32. 补Gateway lifecycle/lineage集成测试（≤2h）。
- [ ] 33. 补binding-vs-Gateway lifecycle race测试（≤2h）。
- [ ] 34. 补Directory-vs-Retire/Replace race及HTTP-only transport组合回归测试（≤2h）。
- [ ] 35. 补同command/actor mismatch/concurrent Register/Replace/crash replay测试（≤2h）。
- [ ] 36. 以正式wrapper运行pinned old binary rollback，证明未启动process/HTTP/workers和DB不变（≤2h）。
- [ ] 37. 验收tampered manifest/digest、missing key/DB与gate bypass配置拒绝（≤2h）。
- [ ] 38. 更新runbook、签名artifact登记、K1 backup/restore和证据引用（≤2h）。
- [ ] 39. 执行generation reproducibility检查（≤2h）。
- [ ] 40. 执行make test build及错误处理（≤2h单次工作块，失败新增明确修复task）。
- [ ] 41. 执行Gateway PostgreSQL/container acceptance并记录结果（≤2h单次工作块）。
- [ ] 42. 记录implementation evidence、durable truth reconciliation及clean-worktree/archive readiness（≤2h）。
