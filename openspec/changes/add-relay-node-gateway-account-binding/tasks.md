# add-relay-node-gateway-account-binding Tasks

## 1. Contract and schema

- [x] 1.1 对照 System Design R4.3/R4.4/R4.7、ADR-0002、资产模型和已归档 Directory ingestion spec，冻结 identity、1:1 cardinality、freshness、resolution 与 non-goals
- [x] 1.2 设计并实现最小 additive temporal binding schema、双向 partial unique constraints、Directory evidence FK、Node/Gateway FK、admin identity FK、固定 bind/end reason CHECK 和 DB immutable history guard
- [x] 1.3 additive 扩展既有 immutable `audit_logs` CHECK：固定 `relay_binding` category、bind/unbind/rebind actions、details key allowlist，并复用 binding reason code，不新增第二套审计 framework

## 2. Binding writes

- [x] 2.1 实现 bind：使用同一 PostgreSQL transaction/DB time验证 fresh current Directory、Account ID、资产 identity、并发唯一性并写 audit
- [x] 2.2 实现 atomic rebind：关闭旧 interval、插入新 interval和写 audit 同事务，不产生可观察中间状态
- [x] 2.3 实现 unbind：逻辑关闭 current interval并保留历史；重复 unbind 保持幂等
- [x] 2.4 固化 stale/no Directory、missing Account、Node/Gateway 不存在和 current binding conflict 的稳定 fail-closed 结果
- [x] 2.5 补 DB immutability guard：identity/bound/evidence不可变、open interval仅可原子close一次、closed interval不可变、DELETE/TRUNCATE拒绝

## 3. Resolution and reads

- [x] 3.1 实现 query-derived `unbound/resolved/unresolved/unknown`，只依赖 current binding、current accepted Directory 和 DB freshness
- [x] 3.2 实现 Node-centric current binding 查询，返回脱敏 Account context、resolution、freshness 和 observation time，并区分 current/last-known context
- [x] 3.3 实现 Gateway Account-centric 查询，覆盖 bound/unbound current Account 和目标已消失的 unresolved binding
- [x] 3.4 固化 Account 消失/同 ID 再出现/new ID/A→B→A/empty Directory/stale Directory 对 resolution 的影响

## 4. Security, concurrency, and acceptance

- [x] 4.1 接入既有 `super_admin`、CSRF、no-store 与审计边界；禁止 Secret、raw response、credential、unsafe URL 和原始错误泄漏
- [x] 4.2 补 PostgreSQL concurrency tests，证明同 Node 和同 Account 并发写入最多一个成功，rebind 无中间双绑定
- [x] 4.3 补 identity negative tests，证明 name/url/platform/type/status 相似或相同不能替代 `accounts.id`
- [x] 4.4 补 stale snapshot、missing/deleted Account、Node/Gateway mismatch、Node/admin delete RESTRICT、temporal immutability 和固定 audit shape tests
- [x] 4.5 验证 binding 不修改 Directory snapshot/current state、Sub2API/CLIProxyAPI 配置，不进入请求调度或 duplicate ownership

## 5. Validation and documentation

- [x] 5.1 补最小 Migration up/down/up、store integration、API security 和 redaction acceptance
- [ ] 5.2 更新 runbook，覆盖 bind/rebind/unbind、freshness、unresolved/unknown、冲突和 rollback
- [ ] 5.3 运行 `make generate`、相关测试、`openspec validate add-relay-node-gateway-account-binding --type change --strict --no-interactive` 和 `git diff --check`
