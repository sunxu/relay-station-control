## ADDED Requirements

### Requirement: 新 Relay Node management endpoint SHALL 仅接受 HTTP

Node Register、Edit 与 Replace 的新 durable mutation MUST 仅接受满足既有 canonical endpoint 安全约束的绝对 `http://` management endpoint；`https://`、其它 scheme、userinfo、query、fragment、control character、无效 host/port 与不安全 encoded path MUST 在 domain mutation 前拒绝。合法单标签内部 DNS、`host.docker.internal`、域名、IPv4、可选端口及既有合法 base path MUST 可用。PostgreSQL MUST 对 `relay_node_assets.management_endpoint` 建立 Node-specific HTTP-only invariant，使 direct DML 与受控创建函数均不能绕过 admission。

#### Scenario: 新命令拒绝 HTTPS
- **WHEN** Register、Edit 或 Replace 的新 command 使用 canonical `https://` endpoint
- **THEN** 返回 `400 invalid_endpoint`，且资产、revision、lineage、receipt、success audit 与 outbound 均无变化

#### Scenario: 内部 HTTP endpoint 可用
- **WHEN** 新 command 使用 `http://node:8317`、`http://host.docker.internal:8317` 或满足既有 path 安全约束的 HTTP base path
- **THEN** endpoint canonicalization 成功并进入既有 lifecycle transaction

#### Scenario: 数据库 durable boundary
- **WHEN** runtime direct DML 或 `control_create_relay_node_asset` 尝试持久化 HTTPS Node endpoint
- **THEN** PostgreSQL 拒绝整个事务，且 asset、capability 与 generation 均无部分写入

#### Scenario: 历史 HTTPS row 阻断 migration
- **WHEN** migration 36 preflight 发现既有 `relay_node_assets` row 的 endpoint 不是 `http://`
- **THEN** migration fail closed 并报告受影响 identity，既有 row 不被重写、删除或自动 retired

### Requirement: 历史 Node command receipt SHALL 保持精确 replay

HTTP-only admission policy MUST 只约束 receipt 不存在的新 command。对 actor、kind、encoding 与 key-version 均匹配的历史 completed receipt，系统 MUST 在 actor-first lookup 后使用历史 v1 canonical-intent normalization 比较原 intent，并在 hash 相等时返回原 persisted status/body；该 replay-only normalization MUST NOT 创建或修改 HTTPS durable asset。

#### Scenario: 历史 HTTPS Register replay
- **WHEN** actor 使用同一 command ID 和历史已提交的 HTTPS Register intent 重试
- **THEN** 系统精确 replay 原 persisted response，且不进行第二次 mutation、receipt 或 audit

#### Scenario: 历史 HTTPS intent 不授权新命令
- **WHEN** 请求使用新的 command ID 提交相同 HTTPS endpoint
- **THEN** 系统按当前 HTTP-only admission 返回 `400 invalid_endpoint` 且零 side effect
