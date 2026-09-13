## 1. Planning 与基线

- [x] 1.1 校验 baseline `10c8e79ee7c419828be43a84b062d99392abd3bd`、Phase 6 archived truth 与 scope mapping，并以 strict OpenSpec validation 验证 planning artifacts
- [x] 1.2 运行本 change 的 `openspec instructions apply`，记录 post-closeout corrective 状态且不把 Phase 6 改回 implementing

## 2. Node HTTP-only admission 与 replay

- [x] 2.1 将 Node Register/Edit/Replace 新命令 normalization 收紧为 HTTP-only，并以单元测试覆盖内部 DNS、host.docker.internal、base path 与既有 unsafe inputs
- [x] 2.2 分离 replay-only legacy normalization 与新 command admission，以 Store/API tests 验证历史 HTTPS receipt exact replay 且新 HTTPS command 零 side effect
- [x] 2.3 增加 migration 36 Node-specific HTTP CHECK 与既有 HTTPS row fail-closed preflight，以真实 PostgreSQL 18 验证 clean migration、row unchanged 和 affected identity evidence
- [x] 2.4 以真实 PostgreSQL 18 验证 direct INSERT/runtime UPDATE/controlled create HTTPS 均被拒绝且 asset/capability/generation 无 partial write
- [x] 2.5 增加 Register/Edit/Replace HTTPS API acceptance，并验证 400、零 revision/lineage/receipt/audit/outbound及合法 HTTP fixtures

## 3. Probe lifecycle fence

- [x] 3.1 将 secret-free Probe authorizer 改为短事务内 Node lifecycle read lock，并验证 transaction/lock 在 Driver HTTP 前释放且 SecretResolver 调用为零
- [x] 3.2 使用两条真实 PostgreSQL 18 connection 验证 Retire-first 与 Replace-first 时 authorizer 阻塞后返回 asset_retired、零 outbound
- [x] 3.3 验证 Probe-first commit 后一次固定 target Probe 可完成、lifecycle 可继续且后续 Health/Connection Test 均 409/零 outbound
- [x] 3.4 重跑 Probe transport、安全、audit/metrics separation 与 secret canary regression

## 4. Internal HTTP UI validation

- [x] 4.1 实现小型 shared internal HTTP endpoint validator并接入 Gateway/Node forms，不使用 Ant Design public URL rule
- [x] 4.2 增加 frontend tests，覆盖 single-label Docker DNS、host.docker.internal、域名/IPv4/base path、HTTPS与 malformed inputs
- [x] 4.3 运行 authenticated Asset Registry相关 E2E，验证 Gateway/Node endpoint form 与 Health/Connection Test 无回归

## 5. Canonical documentation

- [x] 5.1 扫描并仅替换所有 exact archive-generated canonical Purpose placeholder，复核 requirement/scenario 内容未变化且 placeholder count 为零

## 6. Compatibility 与完整验收

- [x] 6.1 使用 Stage 2 commit `5199b611a99ac36b46a5a0309db1c01d3fe50929` 的真实 class2 artifact和正式 wrapper，在 schema 36 上验证 startup、Node read/reconcile/Retire/Replace及 old 5-param writer fail closed
- [x] 6.2 重跑 Inventory、Monitoring Enable/Disable、Node Retire/Replace、Gateway lifecycle targeted regression与 PostgreSQL 18 migration acceptance
- [x] 6.3 仅在生成输入变化时运行两次 `make generate` 并证明第二次无 delta，然后运行 `make test` 与 `make build`
- [x] 6.4 运行本 change 与全量 OpenSpec strict、`git diff --check` 和 filename-only scope检查
- [x] 6.5 更新 planning/implementation validation 与 Runtime Acceptance matrix；仅在全部真实 PASS 后标记 READY FOR INDEPENDENT CORRECTIVE IMPLEMENTATION REVIEW，保持 Git/archive NOT RUN
