## Context

见 `proposal.md`。Phase 6 已在 Control revision `10c8e79ee7c419828be43a84b062d99392abd3bd` 关闭并归档，schema version 35、compatibility class/floor `2 / 2`。本 change 是 post-closeout corrective evidence，不改变 Phase 6 官方状态。当前 Node application normalization 接受 HTTP/HTTPS，但 Driver transport 只接受 HTTP；Probe authorizer 使用不带 Node row lock 的聚合查询；Ant Design public URL rule 会拒绝合法内部 DNS。

## Goals / Non-Goals

**Goals:**

- 在新 command admission、PostgreSQL durable boundary 与 UI convenience validation 三层统一 Node HTTP-only contract。
- 保持 historical completed receipt 的 actor-first、原 intent hash 与原 response replay。
- 使用真实 PostgreSQL lifecycle read lock 串行化 Probe authorization 与 Retire/Replace，同时在 HTTP 前释放 transaction。
- 保持 class/floor `2 / 2` 并验证 Stage 2 artifact 对 migration 36 forward schema 的兼容。

**Non-Goals:**

- 不提供 HTTPS/TLS、双协议、Secret Probe、health history、background polling、Phase 7、scheduler/routing、credential/account mutation或通用 endpoint/lifecycle framework。

## Decisions

### 1. 将 legacy replay normalization 与新命令 admission 分离

shared durable command transaction 继续先锁 command ID、查 receipt、校验 actor/kind/encoding/key version。actor 匹配的历史 receipt 使用保留 HTTP/HTTPS canonicalization 的 replay-only builder 计算历史 v1 intent hash；receipt 不存在时使用 HTTP-only admission builder。这样不改变已发布 v1 array bytes，也不会让 legacy path 创建新 HTTPS durable truth。

替代方案是直接收紧共用 normalizer；该方案会把旧 HTTPS receipt 变成无法比较，违反 immutable replay，因此拒绝。

### 2. migration 36 使用 Node-specific constraint 与显式 preflight

migration 36 先查询并在发现非 `http://` row 时用包含受影响 instance IDs 的固定、有限错误 fail closed，然后添加 Node-specific CHECK。保持共享 `control_normalize_asset_endpoint()` 不变，避免改变 Gateway 或历史 schema primitive。CHECK 作用于 direct DML 和 SECURITY DEFINER controlled create，事务失败不会留下 capability/generation partial truth。

不自动将 HTTPS 改成 HTTP；scheme 改写可能改变实际目标与安全语义，必须由 operator 通过 audited Edit 处理后重试 migration。

### 3. Probe authorizer 在短 read-write transaction 中使用 `FOR SHARE`

`control_authorize_node_probe_v1` 改为窄 PL/pgSQL boundary：先单行 `relay_node_assets ... FOR SHARE`，再读取 capabilities，返回 secret-free projection。Go transaction 不声明 read-only，scan 完成后先 commit，再调用 Driver。Retire/Replace 的 `FOR UPDATE` 与该 read lock 按 commit order 串行化。

选择 `FOR SHARE` 而不是跨 HTTP 持锁；Probe 只需要冻结授权时 lifecycle target，一次已授权 observation 可以在随后 retirement 后完成。

### 4. UI 使用 pure internal HTTP helper

新增一个无副作用 helper，使用浏览器 URL parser 加 explicit HTTP-only 与既有安全检查，Gateway/Node 两个表单复用。后端与 DB 仍是安全权威。只去除 Ant Design `type: url` 误判，不建立表单框架。

### 5. Canonical Purpose cleanup 仅替换占位正文

扫描所有 exact archive placeholder；根据当前 canonical requirements 写 50 字以上 Purpose。Requirement/scenario 标题、正文和 WHEN/THEN 不改变。

## Risks / Trade-offs

- **历史 HTTPS asset 阻止 migration** → preflight 明确列出 identity，保持 row 原样，由 operator 先 audited Edit。
- **replay-only normalizer 被误用于新命令** → receipt-exists 分支私有调用；新命令测试验证零 HTTPS mutation/receipt/audit。
- **Probe lock 被误持至 HTTP** → repository transaction test 与双连接 PG18 race证明 commit 先于 Driver 调用。
- **UI 与后端规则漂移** → helper 只反映后端已冻结规则；API 与 DB negative tests仍为最终验收。
- **forward schema 影响 class2 rollback** → 使用 Stage 2 archive commit 构建的 artifact 和正式 wrapper在 migration 36 上执行真实 smoke；失败即 release blocker，不升 class 3。

## Migration Plan

1. 停止 Control/writers，执行 migration 36 preflight；若报告 HTTPS identity，保持部署停止并由 operator 使用已审计流程修复后重试。
2. clean database 应用 migration 36，确认 class/floor仍为 `2 / 2`。
3. 部署 corrective artifact并执行 API/PG18/UI acceptance。
4. rollback 时停止新 artifact，保留 forward schema 36，以正式 wrapper启动真实 Stage 2 class2 artifact并执行 read/reconcile/Retire/Replace smoke；不执行 destructive down。
